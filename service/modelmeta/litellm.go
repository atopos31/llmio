package modelmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"
)

// liteLLMURL 是数据集本体。它在仓库根，适用根目录的 MIT 许可
// （例外只有 enterprise/ 目录，见 §2.2）。
const liteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// perTokenToPerMillion 把 LiteLLM 的"每 token 美元"折成 llmio 的口径
// "每百万 token 单价"。两个源的价格单位不同，这是唯一需要换算的地方。
const perTokenToPerMillion = 1_000_000

// liteLLMChatModes 是接受的 mode。
//
// 这个文件里有 19 种 mode（image_generation 408 条、embedding 151 条、
// audio_transcription 93 条…）。llmio 是对话代理，嵌入与图像条目的
// 能力字段与价格跟对话不是一回事，混进来只会污染建议。
var liteLLMChatModes = []string{"chat", "completion", "responses"}

type liteLLMSource struct {
	client httpDoer
}

func newLiteLLMSource(client httpDoer) liteLLMSource { return liteLLMSource{client: client} }

func (liteLLMSource) Name() string { return SourceLiteLLM }

type liteLLMEntry struct {
	Mode            string `json:"mode"`
	LiteLLMProvider string `json:"litellm_provider"`

	InputCostPerToken       *float64 `json:"input_cost_per_token"`
	OutputCostPerToken      *float64 `json:"output_cost_per_token"`
	CacheReadInputTokenCost *float64 `json:"cache_read_input_token_cost"`

	SupportsFunctionCalling *bool `json:"supports_function_calling"`
	// 结构化输出的正确字段是 supports_response_schema。
	// supports_structured_output 在这个文件里 0/4480 条——映射错了会静默
	// 0 填充，一行日志都不会有（§2.2 的字段名陷阱）。
	SupportsResponseSchema *bool `json:"supports_response_schema"`
	SupportsVision         *bool `json:"supports_vision"`

	DeprecationDate *string `json:"deprecation_date"`

	MaxInputTokens  *int `json:"max_input_tokens"`
	MaxOutputTokens *int `json:"max_output_tokens"`
}

func (s liteLLMSource) Fetch(ctx context.Context) (*Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liteLLMURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("litellm: status code %d", res.StatusCode)
	}

	// 先解成 RawMessage 再逐条解：顶层除了模型条目还有两个**必须排除**的键
	// （sample_spec 是文档占位符、fallback_generalizations 是正则规则表），
	// 它们与模型条目共用一个命名空间，逐条解才能既跳过它们又不因为某一条
	// 形态意外而整包失败。
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("litellm: decode: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("litellm: empty payload")
	}
	return parseLiteLLM(raw, time.Now()), nil
}

// parseLiteLLM 折成 Catalog。
//
// 键排序后再建索引，理由与 models.dev 那边相同：putIfAbsent 的规则是
// "先写的赢"，不排序就会让重复键（`together_ai/baai/...` 与
// `together_ai/BAAI/...` 这类大小写重复，go 的 map 会静默取后者）在
// 每次启动给出不同结果。
func parseLiteLLM(raw map[string]json.RawMessage, fetchedAt time.Time) *Catalog {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		if k == "sample_spec" || k == "fallback_generalizations" {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)

	entries := make([]Entry, 0, len(keys))
	for _, key := range keys {
		var e liteLLMEntry
		if err := json.Unmarshal(raw[key], &e); err != nil {
			// 单条解不动就跳过。一条形态意外不该让整份建议消失。
			continue
		}
		if e.LiteLLMProvider == "" || !slices.Contains(liteLLMChatModes, e.Mode) {
			continue
		}
		entries = append(entries, Entry{
			Source:           SourceLiteLLM,
			Provider:         e.LiteLLMProvider,
			ProviderName:     e.LiteLLMProvider, // LiteLLM 没有显示名，用 id 顶
			Model:            key,
			Status:           liteLLMStatus(e),
			ToolCall:         e.SupportsFunctionCalling,
			StructuredOutput: e.SupportsResponseSchema,
			Image:            e.SupportsVision,
			InputPrice:       scaleCost(e.InputCostPerToken),
			OutputPrice:      scaleCost(e.OutputCostPerToken),
			CacheReadPrice:   scaleCost(e.CacheReadInputTokenCost),
			Currency:         CurrencyUSD,
			ContextLimit:     intOrZero(e.MaxInputTokens),
			OutputLimit:      intOrZero(e.MaxOutputTokens),
		})
	}
	return buildCatalog(SourceLiteLLM, fetchedAt, entries, nil)
}

// liteLLMStatus 把 deprecation_date 折成 status。
//
// 只判"有没有这个字段"，**不拿它跟今天比**：比日期会让同一个模型在今年
// 得到建议、明年得不到，测试也就钉不住。而它的用途只有一个——默认不给建议——
// 所以取保守的那一侧是对的。
func liteLLMStatus(e liteLLMEntry) string {
	if e.DeprecationDate != nil && *e.DeprecationDate != "" {
		return "deprecated"
	}
	return ""
}

// costDecimals 是折算后保留的小数位。
//
// 它修的是浮点乘法的表示误差，不是"四舍五入到某个精度"：源里写的是
// 3.2e-06，乘 1e6 在数学上正好是 3.2，但 float64 给出 3.1999999999999997。
// 这个数字会一路进表单输入框、进库、再被读回来显示，肉眼可见的丑，
// 而它一个 bit 的信息量都没有。
//
// 6 位绰绰有余：源里最细的一档是 1e-09/token，折过来是 0.001/百万。
const costDecimals = 6

func scaleCost(perToken *float64) *float64 {
	if perToken == nil {
		return nil
	}
	v := *perToken * perTokenToPerMillion
	scale := math.Pow10(costDecimals)
	v = math.Round(v*scale) / scale
	return &v
}

func intOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
