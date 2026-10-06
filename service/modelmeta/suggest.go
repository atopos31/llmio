package modelmeta

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/atopos31/llmio/models"
)

// 建议查询的失败原因。前端对它们给**不同**的文案（§5.2「原因要分开」）——
// "数据还没准备好，可以重试"与"数据源里确实没有这个模型"对用户是两件事。
const (
	// ReasonNoProviderMatch 是连上游都没对齐上：这个上游不在数据源里。
	ReasonNoProviderMatch = "no_provider_match"
	// ReasonNoModelMatch 是对齐上了上游，但源里没有这个模型。
	ReasonNoModelMatch = "no_model_match"
	// ReasonCatalogUnavailable 是数据还没准备好（首次抓取在跑）或抓取一直失败。
	// 它与应用无关，稍后重试就能好。
	ReasonCatalogUnavailable = "catalog_unavailable"
	// ReasonDeprecatedModel 是命中了，但源把它标成了已废弃，而策略里没放开。
	//
	// 计划 §4.1 只列了前面三条。这一条是必须单独出来的：把它并进
	// no_model_match 会让用户看到"未找到"，而事实是"找到了但已废弃"——
	// 前者让人怀疑自己拼错了模型名，后者才是真相。
	ReasonDeprecatedModel = "deprecated_model"
)

// maxCandidates 是跨上游候选的条数上限（§4.1.1）。
const maxCandidates = 5

// Query 是一次建议查询的输入。
type Query struct {
	// ProviderType 是 llmio 的上游协议（openai / openai-res / anthropic / gemini），
	// 只在最后一步"按类型兜底"时用得上。
	ProviderType string
	// ProviderConfig 是上游的配置 JSON，base_url 从里面取。
	ProviderConfig string
	// ProviderName 是上游在 llmio 里的显示名，提示文案里写"未在「七牛云」中找到"
	// 用的是它——用户认得的是自己起的名字，不是数据源里的 id。
	ProviderName string
	// ProviderModel 是用户填的上游模型名。
	ProviderModel string
}

// Candidate 是一条跨上游候选。
//
// 它与 Suggestion 的字段几乎一样，但语义完全不同：候选**不是**可填写值，
// 前端拿到它只能展示提示，用户点了"采用"才写（§4.1.1）。
type Candidate struct {
	Source       string `json:"source"`
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	Model        string `json:"model"`

	ToolCall         *bool `json:"tool_call"`
	StructuredOutput *bool `json:"structured_output"`
	Image            *bool `json:"image"`

	InputPrice     *float64 `json:"input_price"`
	CacheReadPrice *float64 `json:"cache_read_price"`
	OutputPrice    *float64 `json:"output_price"`
	Currency       string   `json:"currency"`

	ContextLimit int `json:"context_limit,omitempty"`
	OutputLimit  int `json:"output_limit,omitempty"`

	// SameProtocol 说明这条候选的协议与当前上游一致。同协议的排前面，
	// 因为转售通常同协议（§4.1.1）。
	SameProtocol bool `json:"same_protocol"`
}

// Suggestion 是建议查询的响应。
//
// 三个能力与三档价格全是**指针**：nil 即"源未提供"，前端遇到 nil 不动表单
// 里的对应字段（§3.5）。这不是可省略的细节——把缺失当 false 写进去，
// 等于替用户断言"这个模型不支持工具调用"。
type Suggestion struct {
	Matched bool   `json:"matched"`
	Reason  string `json:"reason,omitempty"`

	// Source / Provider / ProviderName 说明"这次是按哪个上游查的"。
	// 上游对齐有可能靠协议类型兜底（最容易误配的那一步），所以它必须
	// 回给前端让人看得见（§3.3 末尾）。
	Source       string `json:"source,omitempty"`
	Provider     string `json:"provider,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	// ProviderMatchRule 是上游对齐命中的步：base_url / host_alias / type_fallback。
	ProviderMatchRule string `json:"provider_match_rule,omitempty"`
	// Model 是源里的模型 id（不是用户填的那个），用来核对归一化是否合理。
	Model string `json:"model,omitempty"`
	// MatchRule 是模型 id 命中的归一化步，取值见 Rule* 常量。
	MatchRule string `json:"match_rule,omitempty"`
	// Status 是源标出的生命周期：空 / deprecated / beta。
	Status string `json:"status,omitempty"`

	ToolCall         *bool `json:"tool_call"`
	StructuredOutput *bool `json:"structured_output"`
	Image            *bool `json:"image"`

	InputPrice     *float64 `json:"input_price"`
	CacheReadPrice *float64 `json:"cache_read_price"`
	OutputPrice    *float64 `json:"output_price"`
	// Currency 是上面三档价格的单位。两个源都是 USD / 每百万 token。
	// 前端只有在**确实写了至少一档价格**时才连带写币种，否则会把一个
	// 没改过价格的关联的币种也翻成 USD（§3.6）。
	Currency string `json:"currency,omitempty"`

	ContextLimit int `json:"context_limit,omitempty"`
	OutputLimit  int `json:"output_limit,omitempty"`

	// Candidates 只在 Matched=false 且当前上游没命中、别的上游有同名模型时出现。
	// **带 candidates 时前端不得自动填写**（§4.1.1）。
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Suggest 按策略给出建议。
//
// 策略在这里只用两个字段：Sources（按序尝试哪些源）与 AllowDeprecated。
// Enabled 与 Overwrite 是**前端行为**——前者决定弹窗要不要预填，后者决定
// 要不要改写已有值——服务端读了也没用，因为落库与否完全由用户点的那个保存
// 按钮决定（§4.2）。所以这个端点**不检查 Enabled**：它是一次纯读。
func (m *Manager) Suggest(ctx context.Context, q Query, policy models.ModelAutofillPolicy) Suggestion {
	policy = normalizePolicy(policy)
	baseURL := baseURLFromConfig(q.ProviderConfig)

	var candidates []Candidate
	unavailable := false
	alignedAny := false

	for _, name := range policy.Sources {
		cat, err := m.Catalog(ctx, name)
		if err != nil {
			// 首次抓取还没跑完、或抓取一直失败。两种都是"这次没查成"，
			// 与"查了但没有"必须分开——前者稍后重试就能好。
			//
			// ErrUnknownSource **不算**"没查成"：那是"这个管理器里没有注册
			// 这个源"，属于配置问题而不是临时状况。把它也算进去，会让一个
			// 还在正常工作的源给出的答案被另一个源的名字拼错扣住，
			// 而症状是"数据还没准备好，请稍后重试"——一句永远等不好的提示。
			if errors.Is(err, ErrCatalogPreparing) {
				unavailable = true
			}
			continue
		}

		refs := alignProviders(cat, q.ProviderType, baseURL)
		if len(refs) == 0 {
			continue
		}
		alignedAny = true

		idx, ref, rule, ok := cat.findInProviders(refs, q.ProviderModel)
		if !ok {
			candidates = append(candidates, cat.candidatesFor(q, refs, policy)...)
			continue
		}

		entry := cat.entries[idx]
		if entry.Status == "deprecated" && !policy.AllowDeprecated {
			return Suggestion{
				Matched:           false,
				Reason:            ReasonDeprecatedModel,
				Source:            cat.Source,
				Provider:          ref.ID,
				ProviderName:      cat.providerName(ref.ID),
				ProviderMatchRule: ref.Rule,
				Model:             entry.Model,
				Status:            entry.Status,
			}
		}
		return filledSuggestion(cat, entry, ref, rule)
	}

	out := Suggestion{Matched: false}
	switch {
	case unavailable:
		// 有源根本没查成，此时说"没有这个模型"是过度断言——那个源里可能就有。
		out.Reason = ReasonCatalogUnavailable
	case !alignedAny:
		out.Reason = ReasonNoProviderMatch
	default:
		out.Reason = ReasonNoModelMatch
	}
	out.Candidates = orderCandidates(candidates, q.ProviderType)
	return out
}

// Suggest 是进程级管理器的便捷入口，自带策略读取。
func Suggest(ctx context.Context, q Query) Suggestion {
	return Default().Suggest(ctx, q, LoadPolicy(ctx))
}

func filledSuggestion(cat *Catalog, e Entry, ref providerRef, rule string) Suggestion {
	return Suggestion{
		Matched:           true,
		Source:            cat.Source,
		Provider:          ref.ID,
		ProviderName:      cat.providerName(ref.ID),
		ProviderMatchRule: ref.Rule,
		Model:             e.Model,
		MatchRule:         rule,
		Status:            e.Status,
		ToolCall:          e.ToolCall,
		StructuredOutput:  e.StructuredOutput,
		Image:             e.Image,
		InputPrice:        e.InputPrice,
		CacheReadPrice:    e.CacheReadPrice,
		OutputPrice:       e.OutputPrice,
		Currency:          e.Currency,
		ContextLimit:      e.ContextLimit,
		OutputLimit:       e.OutputLimit,
	}
}

// candidatesFor 收集一个源里的跨上游同名候选。
func (c *Catalog) candidatesFor(q Query, refs []providerRef, policy models.ModelAutofillPolicy) []Candidate {
	exclude := make(map[string]bool, len(refs))
	for _, ref := range refs {
		exclude[ref.ID] = true
	}

	idxs := c.crossProviderCandidates(q.ProviderModel, exclude)
	out := make([]Candidate, 0, len(idxs))
	for _, i := range idxs {
		e := c.entries[i]
		if e.Status == "deprecated" && !policy.AllowDeprecated {
			continue
		}
		out = append(out, Candidate{
			Source:           c.Source,
			Provider:         e.Provider,
			ProviderName:     c.providerName(e.Provider),
			Model:            e.Model,
			ToolCall:         e.ToolCall,
			StructuredOutput: e.StructuredOutput,
			Image:            e.Image,
			InputPrice:       e.InputPrice,
			CacheReadPrice:   e.CacheReadPrice,
			OutputPrice:      e.OutputPrice,
			Currency:         e.Currency,
			ContextLimit:     e.ContextLimit,
			OutputLimit:      e.OutputLimit,
			SameProtocol:     c.providerStyle[e.Provider] == q.ProviderType,
		})
	}
	return out
}

// orderCandidates 把候选排成前端要的顺序并截断。
//
// 排序只有一条规则：**同协议的排前面**（§4.1.1）。不按"哪个更像"排——
// 那是替用户做判断。候选按名称完全一致筛出来的，彼此之间没有优劣。
func orderCandidates(in []Candidate, providerType string) []Candidate {
	if len(in) == 0 {
		return nil
	}
	same := make([]Candidate, 0, len(in))
	other := make([]Candidate, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, c := range in {
		key := c.Source + "\x00" + c.Provider + "\x00" + c.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		if c.SameProtocol {
			same = append(same, c)
		} else {
			other = append(other, c)
		}
	}
	out := append(same, other...)
	if len(out) > maxCandidates {
		out = out[:maxCandidates]
	}
	return out
}

// baseURLFromConfig 从上游配置里取 base_url。
//
// 四种协议的配置结构体字段名都是 base_url（providers/openai.go 等），
// 所以这里只解一个字段，不必按类型分支。
func baseURLFromConfig(config string) string {
	var c struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal([]byte(config), &c); err != nil {
		return ""
	}
	return c.BaseURL
}
