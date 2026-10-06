package modelmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"
)

// modelsDevURL 是全量端点。
//
// 三个实测过的约束（§2.1）决定了这里的写法：
//   - **没有分片端点**：`api.json?provider=anthropic` 静默忽略参数、照返回全量
//     5.3 MB，所以取子集只能整包拉下来再本地建索引。
//   - **不支持条件请求**：带正确 If-None-Match 仍返回 200 + 全量而不是 304，
//     所以缓存必须按时间（见 Manager.ttl），不能靠 revalidate。
//   - `model-schema.json` **不是字段 schema**，它的 $defs.Model 是 8k 个模型 ID
//     的枚举，所以下面这些结构体只能手写。
const modelsDevURL = "https://models.dev/api.json"

// CurrencyUSD 是两个源共同的口径：USD / 每百万 token。
//
// 写死成常量是因为它**必须**跟着价格一起写进表单：现有表单默认 CNY，
// 不一起改币种就会拿美元数字当人民币计价，而日志页的成本是按 currency
// 渲染的，这个错会直接体现为金额错误（§3.6）。
const CurrencyUSD = "USD"

type modelsDevSource struct {
	client httpDoer
}

func newModelsDevSource(client httpDoer) modelsDevSource { return modelsDevSource{client: client} }

func (modelsDevSource) Name() string { return SourceModelsDev }

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Npm    string                    `json:"npm"`
	API    string                    `json:"api"`
	Name   string                    `json:"name"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModalities struct {
	Input []string `json:"input"`
}

type modelsDevLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

// cost 的三个字段都是指针：源里 cost 会整个缺失（437/8389 条 limit 或 cost
// 任一缺失），单档也可能缺失，而缺失必须一路保持成 nil 到建议里。
type modelsDevCost struct {
	Input     *float64 `json:"input"`
	Output    *float64 `json:"output"`
	CacheRead *float64 `json:"cache_read"`
}

type modelsDevModel struct {
	ToolCall         *bool                `json:"tool_call"`
	StructuredOutput *bool                `json:"structured_output"`
	Modalities       *modelsDevModalities `json:"modalities"`
	Limit            *modelsDevLimit      `json:"limit"`
	Cost             *modelsDevCost       `json:"cost"`
	Status           string               `json:"status"`
}

func (s modelsDevSource) Fetch(ctx context.Context) (*Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevURL, nil)
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
		return nil, fmt.Errorf("models.dev: status code %d", res.StatusCode)
	}

	var raw map[string]modelsDevProvider
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("models.dev: decode: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("models.dev: empty payload")
	}
	return parseModelsDev(raw, time.Now()), nil
}

// parseModelsDev 把解出来的 JSON 折成 Catalog。
//
// 上游与模型都**排序后**再建索引。JSON 对象解进 map 之后迭代顺序是随机的，
// 而索引的规则是"先写进去的赢"（putIfAbsent），不排序就会让同一份数据
// 每次启动给出不同的匹配结果。
func parseModelsDev(raw map[string]modelsDevProvider, fetchedAt time.Time) *Catalog {
	providerIDs := make([]string, 0, len(raw))
	for id := range raw {
		providerIDs = append(providerIDs, id)
	}
	slices.Sort(providerIDs)

	entries := make([]Entry, 0, 8192)
	infos := make(map[string]ProviderInfo, len(raw))
	for _, pid := range providerIDs {
		p := raw[pid]
		infos[pid] = ProviderInfo{API: p.API, Npm: p.Npm}

		modelIDs := make([]string, 0, len(p.Models))
		for mid := range p.Models {
			modelIDs = append(modelIDs, mid)
		}
		slices.Sort(modelIDs)

		for _, mid := range modelIDs {
			m := p.Models[mid]
			e := Entry{
				Source:           SourceModelsDev,
				Provider:         pid,
				ProviderName:     p.Name,
				Model:            mid,
				Status:           m.Status,
				ToolCall:         m.ToolCall,
				StructuredOutput: m.StructuredOutput,
				Currency:         CurrencyUSD,
			}
			// 视觉是从 modalities.input 里有没有 "image" 折出来的——
			// 它没有一个独立的布尔字段。整块缺失时是 nil 而不是 false。
			if m.Modalities != nil {
				e.Image = boolPtr(slices.Contains(m.Modalities.Input, "image"))
			}
			if m.Limit != nil {
				e.ContextLimit = m.Limit.Context
				e.OutputLimit = m.Limit.Output
			}
			if m.Cost != nil {
				e.InputPrice = m.Cost.Input
				e.OutputPrice = m.Cost.Output
				e.CacheReadPrice = m.Cost.CacheRead
			}
			entries = append(entries, e)
		}
	}
	return buildCatalog(SourceModelsDev, fetchedAt, entries, infos)
}

func boolPtr(v bool) *bool { return &v }
