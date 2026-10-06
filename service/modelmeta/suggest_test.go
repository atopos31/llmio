package modelmeta

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
)

// 这一组钉的是**建议本身**。三条纪律贯穿全部用例：
//
//  1. 三态：源没给的字段是 nil，前端不动表单里对应的那一格。折成 false 等于
//     替用户断言"这个模型不支持工具调用"，而这个断言会直接改变路由结果。
//  2. 候选不是可填写值：跨上游候选只用于展示提示，用户点"采用"才写。
//  3. 原因要分开：没对齐上 / 对齐了但没这个模型 / 数据还没准备好 / 找到了但
//     已废弃——四句话对用户是四件不同的事。

// policyFor 造一份只改来源的策略，其余取默认。
func policyFor(sources ...string) models.ModelAutofillPolicy {
	p := DefaultPolicy()
	if len(sources) > 0 {
		p.Sources = sources
	}
	return p
}

// singleSourceManager 只用一份索引的管理器。
//
// 用它的场合都是"这一条行为只跟一个源有关"——此时另一个源若也注册着，
// 它会因为没装索引而返回 ErrCatalogPreparing，把原因污染成
// catalog_unavailable，测的东西就不是原来那个了。
func singleSourceManager(t *testing.T, cat *Catalog) *Manager {
	t.Helper()
	m := NewManager(&fakeSource{name: cat.Source, cat: cat})
	m.now = func() time.Time { return testFetchedAt.Add(time.Hour) }
	m.SetCatalog(cat)
	return m
}

func TestSuggestMatched(t *testing.T) {
	m := fullTestManager(t)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderName:   "Anthropic 官方",
		ProviderModel:  "claude-sonnet-4-5",
	}, policyFor())

	if !got.Matched {
		t.Fatalf("期望命中，得到 reason=%q", got.Reason)
	}
	if got.Reason != "" {
		t.Errorf("命中时不该带 reason，得到 %q", got.Reason)
	}
	if got.Source != SourceModelsDev {
		t.Errorf("Source = %q，期望 %q", got.Source, SourceModelsDev)
	}
	if got.Provider != "anthropic" {
		t.Errorf("Provider = %q，期望 anthropic", got.Provider)
	}
	// 显示名写进提示文案：用户认得的是"Anthropic"，不是源里的 id。
	if got.ProviderName != "Anthropic" {
		t.Errorf("ProviderName = %q，期望 Anthropic", got.ProviderName)
	}
	// 官方上游在源里没有 api 字段，只能靠别名表对齐——这一步必须回给前端，
	// 因为对齐错了，模型 id 再准也会取到别家的价格（§3.3 末尾）。
	if got.ProviderMatchRule != ProviderRuleHostAlias {
		t.Errorf("ProviderMatchRule = %q，期望 %q", got.ProviderMatchRule, ProviderRuleHostAlias)
	}
	if got.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q，期望 claude-sonnet-4-5", got.Model)
	}
	if got.MatchRule != RuleExact {
		t.Errorf("MatchRule = %q，期望 %q", got.MatchRule, RuleExact)
	}
	if got.Status != "" {
		t.Errorf("Status = %q，期望空串", got.Status)
	}

	if !boolValue(t, got.ToolCall) {
		t.Error("ToolCall 期望 true")
	}
	if !boolValue(t, got.StructuredOutput) {
		t.Error("StructuredOutput 期望 true")
	}
	if !boolValue(t, got.Image) {
		t.Error("Image 期望 true")
	}
	if v := floatValue(t, got.InputPrice); v != 3 {
		t.Errorf("InputPrice = %v，期望 3", v)
	}
	if v := floatValue(t, got.CacheReadPrice); v != 0.3 {
		t.Errorf("CacheReadPrice = %v，期望 0.3", v)
	}
	if v := floatValue(t, got.OutputPrice); v != 15 {
		t.Errorf("OutputPrice = %v，期望 15", v)
	}
	// 价格必须跟币种一起给：现有表单默认 CNY，不一起改币种就会拿美元数字
	// 当人民币计价（§3.6）。
	if got.Currency != CurrencyUSD {
		t.Errorf("Currency = %q，期望 %q", got.Currency, CurrencyUSD)
	}
	if got.ContextLimit != 1000000 || got.OutputLimit != 64000 {
		t.Errorf("limits = (%d, %d)，期望 (1000000, 64000)", got.ContextLimit, got.OutputLimit)
	}
	if len(got.Candidates) != 0 {
		t.Errorf("命中时不该带候选，得到 %d 条", len(got.Candidates))
	}
}

// TestSuggestMissingFieldsAreNilNotFalse 与 TestSuggestExplicitFalseIsNotNil
// 是一对。它们合起来钉住三态里的后两态：**没这个字段**与**字段说 false**
// 在建议里必须是两种不同的东西。
func TestSuggestMissingFieldsAreNilNotFalse(t *testing.T) {
	m := fullTestManager(t)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://api.z.ai"}`,
		ProviderName:   "Z.ai",
		ProviderModel:  "glm-5",
	}, policyFor())

	if !got.Matched {
		t.Fatalf("期望命中，得到 reason=%q", got.Reason)
	}
	// 走的是 LiteLLM：这个模型只有那边有。
	if got.Source != SourceLiteLLM {
		t.Errorf("Source = %q，期望 %q", got.Source, SourceLiteLLM)
	}
	// 键带命名空间，命中的是尾名别名——报 id_prefix 而**不是** exact。
	if got.MatchRule != RuleIDPrefix {
		t.Errorf("MatchRule = %q，期望 %q", got.MatchRule, RuleIDPrefix)
	}
	if got.Model != "zai/glm-5" {
		t.Errorf("Model = %q，期望源里的全名 zai/glm-5", got.Model)
	}
	if !boolValue(t, got.ToolCall) {
		t.Error("ToolCall 期望 true")
	}
	// 这两条源里根本没有。nil 的含义是"不要碰表单里这两格"。
	if got.StructuredOutput != nil {
		t.Errorf("StructuredOutput = %v，源里没有，必须是 nil", *got.StructuredOutput)
	}
	if got.Image != nil {
		t.Errorf("Image = %v，源里没有，必须是 nil", *got.Image)
	}
	if v := floatValue(t, got.InputPrice); v != 1 {
		t.Errorf("InputPrice = %v，期望 1", v)
	}
}

func TestSuggestExplicitFalseIsNotNil(t *testing.T) {
	m := fullTestManager(t)

	// 这条的 structured_output 源里**明写了 false**，所以它必须带着
	// 非 nil 的 false 出去——用户该看到"这个模型不支持结构化输出"，
	// 而那与"不知道"是两回事。
	p := policyFor()
	p.AllowDeprecated = true
	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://api.qnaigc.com/v1"}`,
		ProviderName:   "七牛云",
		ProviderModel:  "gemini-2.0-flash",
	}, p)

	if !got.Matched {
		t.Fatalf("期望命中，得到 reason=%q", got.Reason)
	}
	if got.StructuredOutput == nil {
		t.Fatal("StructuredOutput 期望非 nil 的 false")
	}
	if *got.StructuredOutput {
		t.Error("StructuredOutput 期望 false")
	}
	// 同一条里 tool_call 与 image 都是 true，用来对照：不是整条都成了 false。
	if !boolValue(t, got.ToolCall) {
		t.Error("ToolCall 期望 true")
	}
	if !boolValue(t, got.Image) {
		t.Error("Image 期望 true")
	}
	// cost 整块不在，三档价格必须全 nil——**不能**变成 0，0 是"免费"。
	if got.InputPrice != nil || got.OutputPrice != nil || got.CacheReadPrice != nil {
		t.Error("源里没有 cost，三档价格都该是 nil")
	}
	// 但币种照给：它描述的是"这些价格（如果有）是什么单位"，
	// 前端只在确实写了至少一档价格时才连带写它（§3.6）。
	if got.Currency != CurrencyUSD {
		t.Errorf("Currency = %q，期望 %q", got.Currency, CurrencyUSD)
	}
	if got.ContextLimit != 1048576 || got.OutputLimit != 8192 {
		t.Errorf("limits = (%d, %d)，期望 (1048576, 8192)", got.ContextLimit, got.OutputLimit)
	}
}

// TestSuggestAlignmentRules 走一遍上游对齐的三级：base_url 精确、
// 主机名（用户多写一段路径是常态）、以及**没有 base_url 时的类型兜底**。
func TestSuggestAlignmentRules(t *testing.T) {
	m := fullTestManager(t)

	for _, tc := range []struct {
		name    string
		query   Query
		rule    string
		wantPvd string
	}{
		{
			name: "base_url 精确",
			query: Query{ProviderType: consts.StyleOpenAI, ProviderConfig: `{"base_url":"https://api.qnaigc.com/v1"}`,
				ProviderModel: "gemini-2.0-flash"},
			rule: ProviderRuleBaseURL, wantPvd: "qiniu-ai",
		},
		{
			name: "少写了 /v1，按主机对上",
			query: Query{ProviderType: consts.StyleOpenAI, ProviderConfig: `{"base_url":"https://api.qnaigc.com"}`,
				ProviderModel: "gemini-2.0-flash"},
			rule: ProviderRuleHostAlias, wantPvd: "qiniu-ai",
		},
		{
			name: "多写了路径，按主机对上",
			query: Query{ProviderType: consts.StyleOpenAI, ProviderConfig: `{"base_url":"https://api.qnaigc.com/v1/chat/completions"}`,
				ProviderModel: "gemini-2.0-flash"},
			rule: ProviderRuleHostAlias, wantPvd: "qiniu-ai",
		},
		{
			// 用户手填的 base_url 缺协议头是常态，补 https 之后能精确对上，
			// 所以这条报的是 base_url 而不是更弱的主机名对齐。
			name: "缺协议头",
			query: Query{ProviderType: consts.StyleOpenAI, ProviderConfig: `{"base_url":"api.deepseek.com"}`,
				ProviderModel: "deepseek-flash"},
			rule: ProviderRuleBaseURL, wantPvd: "deepseek",
		},
		{
			name: "配置里没有 base_url，只剩类型兜底",
			query: Query{ProviderType: consts.StyleGemini, ProviderConfig: `{}`,
				ProviderModel: "gemini-2.5-flash"},
			rule: ProviderRuleTypeFallback, wantPvd: "google",
		},
		{
			name: "配置根本不是 JSON",
			query: Query{ProviderType: consts.StyleAnthropic, ProviderConfig: `not json at all`,
				ProviderModel: "claude-haiku-4-5"},
			rule: ProviderRuleTypeFallback, wantPvd: "anthropic",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := m.Suggest(context.Background(), tc.query, policyFor())
			if !got.Matched {
				t.Fatalf("期望命中，得到 reason=%q", got.Reason)
			}
			if got.Provider != tc.wantPvd {
				t.Errorf("Provider = %q，期望 %q", got.Provider, tc.wantPvd)
			}
			if got.ProviderMatchRule != tc.rule {
				t.Errorf("ProviderMatchRule = %q，期望 %q", got.ProviderMatchRule, tc.rule)
			}
		})
	}
}

// TestSuggestDeprecatedModel：源标了 deprecated 时默认不给建议，而且
// **不能报成"未找到"**——那会让人怀疑自己拼错了模型名。
func TestSuggestDeprecatedModel(t *testing.T) {
	m := fullTestManager(t)
	q := Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://api.deepseek.com"}`,
		ProviderName:   "DeepSeek",
		ProviderModel:  "deepseek-v4-flash-vision-exp",
	}

	got := m.Suggest(context.Background(), q, policyFor())
	if got.Matched {
		t.Fatal("默认策略下不该给已废弃的模型建议")
	}
	if got.Reason != ReasonDeprecatedModel {
		t.Errorf("Reason = %q，期望 %q", got.Reason, ReasonDeprecatedModel)
	}
	// 就算不给值，"是按哪个上游、哪条命中的"也要照给：用户得能自己去看一眼。
	if got.Source != SourceModelsDev || got.Provider != "deepseek" {
		t.Errorf("来源信息 = (%q, %q)，期望 (models.dev, deepseek)", got.Source, got.Provider)
	}
	if got.ProviderName != "DeepSeek" {
		t.Errorf("ProviderName = %q，期望 DeepSeek", got.ProviderName)
	}
	if got.ProviderMatchRule != ProviderRuleBaseURL {
		t.Errorf("ProviderMatchRule = %q，期望 %q", got.ProviderMatchRule, ProviderRuleBaseURL)
	}
	if got.Model != "deepseek-v4-flash-vision-exp" {
		t.Errorf("Model = %q，期望 deepseek-v4-flash-vision-exp", got.Model)
	}
	if got.Status != "deprecated" {
		t.Errorf("Status = %q，期望 deprecated", got.Status)
	}
	// 已废弃这一支**一个可填写值都不带**：前端拿不到东西去填，
	// 就不存在"误填已废弃模型的价格"这条路径。
	if got.ToolCall != nil || got.StructuredOutput != nil || got.Image != nil {
		t.Error("已废弃时不该带能力值")
	}
	if got.InputPrice != nil || got.OutputPrice != nil || got.CacheReadPrice != nil {
		t.Error("已废弃时不该带价格")
	}
	if len(got.Candidates) != 0 {
		t.Errorf("已废弃时不该带候选，得到 %d 条", len(got.Candidates))
	}

	// 放开之后照给。
	p := policyFor()
	p.AllowDeprecated = true
	got = m.Suggest(context.Background(), q, p)
	if !got.Matched {
		t.Fatalf("放开 AllowDeprecated 后该命中，得到 reason=%q", got.Reason)
	}
	if !boolValue(t, got.ToolCall) {
		t.Error("ToolCall 期望 true")
	}
	if got.Status != "deprecated" {
		t.Errorf("Status = %q，期望 deprecated（命中了也要如实标注）", got.Status)
	}
}

// TestSuggestDeprecatedFromLiteLLM：两个源判定废弃的依据不同
// （models.dev 看 status 字段，LiteLLM 看有没有 deprecation_date），
// 但对外是同一个结论。
func TestSuggestDeprecatedFromLiteLLM(t *testing.T) {
	m := fullTestManager(t)
	q := Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	}

	got := m.Suggest(context.Background(), q, policyFor(SourceLiteLLM))
	if got.Reason != ReasonDeprecatedModel {
		t.Errorf("Reason = %q，期望 %q", got.Reason, ReasonDeprecatedModel)
	}
	if got.Source != SourceLiteLLM {
		t.Errorf("Source = %q，期望 %q", got.Source, SourceLiteLLM)
	}

	p := policyFor(SourceLiteLLM)
	p.AllowDeprecated = true
	got = m.Suggest(context.Background(), q, p)
	if !got.Matched {
		t.Fatalf("放开后该命中，得到 reason=%q", got.Reason)
	}
	// 同一个模型在两个源里的价格一致，这是这条断言能顺带钉住的事：
	// LiteLLM 那边是每 token 单价，折过来必须与 models.dev 的每百万单价相等。
	if v := floatValue(t, got.InputPrice); v != 3 {
		t.Errorf("InputPrice = %v，期望 3", v)
	}
}

// TestSuggestSourcesOrderMatters：策略里的来源是**按序尝试**的，
// 第一个命中的就是结论。
func TestSuggestSourcesOrderMatters(t *testing.T) {
	m := fullTestManager(t)
	q := Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	}

	// 默认顺序先问 models.dev，它没把这条标成废弃。
	p := policyFor()
	p.AllowDeprecated = true
	if got := m.Suggest(context.Background(), q, p); got.Source != SourceModelsDev {
		t.Errorf("默认顺序该命中 models.dev，得到 %q", got.Source)
	}

	// 把 LiteLLM 提到前面，结论就该来自它。
	p = policyFor(SourceLiteLLM, SourceModelsDev)
	p.AllowDeprecated = true
	if got := m.Suggest(context.Background(), q, p); got.Source != SourceLiteLLM {
		t.Errorf("调换顺序后该命中 litellm，得到 %q", got.Source)
	}

	// 而**废弃判定只看第一个命中的源**：LiteLLM 说废弃就是废弃，
	// 不会再去问 models.dev 是否同意。这是一条决策而不是巧合——
	// 想换个源的说法就调 Sources 的顺序，用户手里有这个旋钮。
	if got := m.Suggest(context.Background(), q, policyFor(SourceLiteLLM)); got.Reason != ReasonDeprecatedModel {
		t.Errorf("Reason = %q，期望 %q", got.Reason, ReasonDeprecatedModel)
	}
}

// TestSuggestCrossProviderCandidate：本家没有、别家有同名——这时**只给候选**。
func TestSuggestCrossProviderCandidate(t *testing.T) {
	m := fullTestManager(t)

	// poe 下没有 gemini-2.0-flash，而 qiniu-ai 有。
	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://api.poe.com/v1"}`,
		ProviderName:   "Poe",
		ProviderModel:  "gemini-2.0-flash",
	}, policyFor())

	if got.Matched {
		t.Fatal("本家没有这个模型，不该给可填写值")
	}
	// 对齐上了上游，只是没有这个模型——这与"上游都不在数据源里"是两件事。
	if got.Reason != ReasonNoModelMatch {
		t.Errorf("Reason = %q，期望 %q", got.Reason, ReasonNoModelMatch)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("期望 1 条候选，得到 %d 条", len(got.Candidates))
	}
	c := got.Candidates[0]
	if c.Provider != "qiniu-ai" {
		t.Errorf("候选上游 = %q，期望 qiniu-ai", c.Provider)
	}
	if c.ProviderName != "Qiniu" {
		t.Errorf("候选显示名 = %q，期望 Qiniu", c.ProviderName)
	}
	if c.Model != "gemini-2.0-flash" {
		t.Errorf("候选模型 = %q，期望 gemini-2.0-flash", c.Model)
	}
	if c.Source != SourceModelsDev {
		t.Errorf("候选来源 = %q，期望 %q", c.Source, SourceModelsDev)
	}
	// 候选自己带着它那份完整的值，供用户点"采用"之后填写。
	if !boolValue(t, c.ToolCall) {
		t.Error("候选的 ToolCall 期望 true")
	}
	if c.StructuredOutput == nil || *c.StructuredOutput {
		t.Error("候选的 StructuredOutput 期望非 nil 的 false")
	}
	if c.ContextLimit != 1048576 {
		t.Errorf("候选的 ContextLimit = %d，期望 1048576", c.ContextLimit)
	}
	// 同协议：qiniu-ai 是 openai 兼容，当前上游也是 openai 类型。
	if !c.SameProtocol {
		t.Error("SameProtocol 期望 true")
	}
}

// TestSuggestCandidatesAreNotFillable 是 §4.1.1 的硬约束。
//
// 顶层字段是**可填写值**，候选是**提示**。有候选时顶层必须一个值都没有——
// 否则前端的"自动预填"会把一家别家上游的价格写进当前上游的关联里，
// 这个错不会有任何报错，只会在日志页上体现为金额不对。
func TestSuggestCandidatesAreNotFillable(t *testing.T) {
	m := fullTestManager(t)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://api.poe.com/v1"}`,
		ProviderModel:  "gemini-2.0-flash",
	}, policyFor())

	if len(got.Candidates) == 0 {
		t.Fatal("这条查询该有候选")
	}
	if got.Matched {
		t.Fatal("有候选时 Matched 必须是 false")
	}
	if got.Model != "" || got.Provider != "" || got.ProviderName != "" || got.MatchRule != "" || got.ProviderMatchRule != "" {
		t.Error("有候选时不该带「这次是按哪个上游、哪条命中的」信息——那会与候选打架")
	}
	if got.ToolCall != nil || got.StructuredOutput != nil || got.Image != nil {
		t.Error("有候选时顶层不该带能力值")
	}
	if got.InputPrice != nil || got.CacheReadPrice != nil || got.OutputPrice != nil || got.Currency != "" {
		t.Error("有候选时顶层不该带价格")
	}
	if got.ContextLimit != 0 || got.OutputLimit != 0 {
		t.Error("有候选时顶层不该带上下文长度")
	}
}

// TestSuggestNoSimilarityMatching：宁可不给，也不编。
//
// claude-3-5-haiku-latest 在实测的两个源全库里都不存在（版本退役）。
// 如果这里返回一个"最相似"的 claude-haiku-4-5，用户会以为自动填写是对的。
func TestSuggestNoSimilarityMatching(t *testing.T) {
	m := fullTestManager(t)

	for _, model := range []string{
		"claude-3-5-haiku-latest",
		"claude-3-5-sonnet-20241022",
		"gpt-5.4-turbo",
	} {
		got := m.Suggest(context.Background(), Query{
			ProviderType:   consts.StyleAnthropic,
			ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
			ProviderName:   "Anthropic 官方",
			ProviderModel:  model,
		}, policyFor())

		if got.Matched {
			t.Errorf("%q 不该命中", model)
		}
		if got.Reason != ReasonNoModelMatch {
			t.Errorf("%q 的 Reason = %q，期望 %q", model, got.Reason, ReasonNoModelMatch)
		}
		if len(got.Candidates) != 0 {
			t.Errorf("%q 得到 %d 条候选，期望 0：同名才对，相似不算", model, len(got.Candidates))
		}
		if got.Model != "" {
			t.Errorf("%q 带了 Model = %q，没命中就不该有模型 id", model, got.Model)
		}
	}
}

// TestSuggestProviderNotInSource：对齐不上任何上游。
//
// 这与"对齐上了但没这个模型"必须分开说：前者是"这一家不在数据源里"，
// 后者是"数据源里有这一家，但没有这个模型"。
func TestSuggestProviderNotInSource(t *testing.T) {
	m := fullTestManager(t)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   "",
		ProviderConfig: `{"base_url":"https://api.my-own-gateway.example.com/v1"}`,
		ProviderName:   "自建网关",
		ProviderModel:  "my-model",
	}, policyFor())

	if got.Matched {
		t.Fatal("不该命中")
	}
	if got.Reason != ReasonNoProviderMatch {
		t.Errorf("Reason = %q，期望 %q", got.Reason, ReasonNoProviderMatch)
	}
	if len(got.Candidates) != 0 {
		t.Errorf("得到 %d 条候选，期望 0", len(got.Candidates))
	}
}

// TestSuggestCatalogUnavailable：数据还没准备好与"没有这个模型"必须分开——
// 前者稍后重试就能好，后者再重试也没用。前端对它们给两套文案（§5.2）。
func TestSuggestCatalogUnavailable(t *testing.T) {
	m := NewManager(&fakeSource{name: SourceModelsDev, err: errors.New("network down")})
	m.now = func() time.Time { return testFetchedAt }

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	}, policyFor(SourceModelsDev))

	if got.Matched {
		t.Fatal("数据没准备好时不该命中")
	}
	// 关键点：**不能**报成 no_model_match——那个源里可能就有这个模型，
	// 只是还没抓下来。说"没有"是过度断言。
	if got.Reason != ReasonCatalogUnavailable {
		t.Errorf("Reason = %q，期望 %q", got.Reason, ReasonCatalogUnavailable)
	}
}

// TestSuggestStaleCatalogStillServes：缓存过期时**先给陈旧的**，
// 同时在后台刷新。一次网络抖动或一次 TTL 过期不该让用户看到空建议。
func TestSuggestStaleCatalogStillServes(t *testing.T) {
	stale := modelsDevCatalog(t)
	stale.FetchedAt = testFetchedAt.Add(-48 * time.Hour) // 早过 TTL

	src := &fakeSource{name: SourceModelsDev, err: errors.New("refresh fails")}
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt }
	m.SetCatalog(stale)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	}, policyFor(SourceModelsDev))

	if !got.Matched {
		t.Fatalf("陈旧的缓存也该照常给建议，得到 reason=%q", got.Reason)
	}
	if !boolValue(t, got.StructuredOutput) {
		t.Error("StructuredOutput 期望 true")
	}

	// 后台确实去刷新了（而且失败了也不影响答案，因为 Refresh 会保住旧缓存）。
	deadline := time.Now().Add(2 * time.Second)
	for src.fetches == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if src.fetches == 0 {
		t.Error("过期之后该起一次后台刷新")
	}
	// 刷新失败后旧缓存还在。
	if cat, err := m.Catalog(context.Background(), SourceModelsDev); err != nil || cat != stale {
		t.Errorf("抓取失败该保住旧缓存，得到 (%v, %v)", cat, err)
	}
}

// TestSuggestMissingSourceInManagerIsSkipped：策略里点名了一个这个管理器
// 没注册的源时，另一个源给出的答案不该被它扣住。
//
// 报成 catalog_unavailable 的话，用户看到的是一句"数据还没准备好，请稍后
// 重试"——而这句话永远等不好，因为问题不在数据，在配置。
func TestSuggestMissingSourceInManagerIsSkipped(t *testing.T) {
	m := singleSourceManager(t, modelsDevCatalog(t))

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	}, policyFor(SourceModelsDev, SourceLiteLLM))

	if !got.Matched {
		t.Fatalf("该由 models.dev 给出答案，得到 reason=%q", got.Reason)
	}
	if got.Source != SourceModelsDev {
		t.Errorf("Source = %q，期望 %q", got.Source, SourceModelsDev)
	}
}

// TestSuggestDoesNotConsultEnabled：这个端点是**纯读的**（§4.2）。
//
// Enabled 管的是"弹窗要不要预填"，落库与否完全由用户点的那个保存按钮决定。
// 服务端在这里检查它，只会让"策略里改了开关但接口行为跟着变"这种
// 谁也说不清的现象出现。
func TestSuggestDoesNotConsultEnabled(t *testing.T) {
	m := fullTestManager(t)

	p := policyFor()
	p.Enabled = false
	p.Overwrite = true // 同样不该被读

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	}, p)

	if !got.Matched {
		t.Fatalf("建议端点不该看 Enabled，得到 reason=%q", got.Reason)
	}
}

// TestSuggestCandidateDeprecatedFiltered：已废弃的候选默认不呈现。
func TestSuggestCandidateDeprecatedFiltered(t *testing.T) {
	entries := []Entry{
		{Source: SourceModelsDev, Provider: "b", ProviderName: "B", Model: "m1", Status: "deprecated", Currency: CurrencyUSD},
		{Source: SourceModelsDev, Provider: "d", ProviderName: "D", Model: "m1", Currency: CurrencyUSD},
	}
	infos := map[string]ProviderInfo{
		"a": {API: "https://a.example.com"}, // 当前上游：存在，但没有 m1
	}
	cat := NewCatalog(SourceModelsDev, testFetchedAt, entries, infos)
	m := singleSourceManager(t, cat)

	q := Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://a.example.com"}`,
		ProviderModel:  "m1",
	}

	got := m.Suggest(context.Background(), q, policyFor(SourceModelsDev))
	if got.Reason != ReasonNoModelMatch {
		t.Fatalf("Reason = %q，期望 %q", got.Reason, ReasonNoModelMatch)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Provider != "d" {
		t.Fatalf("期望只剩 d 一条候选，得到 %+v", got.Candidates)
	}

	p := policyFor(SourceModelsDev)
	p.AllowDeprecated = true
	got = m.Suggest(context.Background(), q, p)
	if len(got.Candidates) != 2 {
		t.Fatalf("放开后期望 2 条候选，得到 %d 条", len(got.Candidates))
	}
	// 顺序跟着条目顺序走，是确定的。
	if got.Candidates[0].Provider != "b" || got.Candidates[1].Provider != "d" {
		t.Errorf("候选顺序 = %q, %q，期望 b, d", got.Candidates[0].Provider, got.Candidates[1].Provider)
	}
}

// TestSuggestCandidateOrderSameProtocolFirst：同协议的排前面。
//
// 判据是 models.dev 的 npm 包名折出来的协议——只为排序服务，折不出来的
// 排在后面，不做任何猜测（§4.1.1）。
func TestSuggestCandidateOrderSameProtocolFirst(t *testing.T) {
	entries := []Entry{
		{Source: SourceModelsDev, Provider: "z", ProviderName: "Z", Model: "m1"},
		{Source: SourceModelsDev, Provider: "y", ProviderName: "Y", Model: "m1"},
	}
	infos := map[string]ProviderInfo{
		"a": {API: "https://a.example.com"},
		"z": {API: "https://z.example.com", Npm: "@ai-sdk/anthropic"},
		"y": {API: "https://y.example.com", Npm: "@ai-sdk/openai-compatible"},
	}
	cat := NewCatalog(SourceModelsDev, testFetchedAt, entries, infos)
	m := singleSourceManager(t, cat)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://a.example.com"}`,
		ProviderModel:  "m1",
	}, policyFor(SourceModelsDev))

	if len(got.Candidates) != 2 {
		t.Fatalf("期望 2 条候选，得到 %d 条", len(got.Candidates))
	}
	if got.Candidates[0].Provider != "y" {
		t.Errorf("第一条候选 = %q，期望同协议的 y", got.Candidates[0].Provider)
	}
	if !got.Candidates[0].SameProtocol {
		t.Error("y 的 SameProtocol 期望 true")
	}
	if got.Candidates[1].Provider != "z" || got.Candidates[1].SameProtocol {
		t.Errorf("第二条 = (%q, same=%v)，期望 (z, false)", got.Candidates[1].Provider, got.Candidates[1].SameProtocol)
	}
}

// TestSuggestCandidatesTruncated：候选上限 5 条。
//
// 一家模型可能被几十个转售上游挂着，全列出来是一面墙，用户在其中挑
// 与在 5 条里挑是一样的（转售同协议的那个总是在前面）。
func TestSuggestCandidatesTruncated(t *testing.T) {
	var entries []Entry
	infos := map[string]ProviderInfo{"a": {API: "https://a.example.com"}}
	for _, id := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"} {
		entries = append(entries, Entry{Source: SourceModelsDev, Provider: id, ProviderName: id, Model: "m1"})
		infos[id] = ProviderInfo{API: "https://" + id + ".example.com"}
	}
	cat := NewCatalog(SourceModelsDev, testFetchedAt, entries, infos)
	m := singleSourceManager(t, cat)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleOpenAI,
		ProviderConfig: `{"base_url":"https://a.example.com"}`,
		ProviderModel:  "m1",
	}, policyFor(SourceModelsDev))

	if len(got.Candidates) != maxCandidates {
		t.Fatalf("候选数 = %d，期望 %d", len(got.Candidates), maxCandidates)
	}
	var ids []string
	for _, c := range got.Candidates {
		ids = append(ids, c.Provider)
	}
	if !reflect.DeepEqual(ids, []string{"p1", "p2", "p3", "p4", "p5"}) {
		t.Errorf("候选 = %v，期望前五条", ids)
	}
}

// TestOrderCandidatesDedup：同一个(来源, 上游, 模型)三元组只留一条。
func TestOrderCandidatesDedup(t *testing.T) {
	in := []Candidate{
		{Source: "s", Provider: "p", Model: "m", SameProtocol: false},
		{Source: "s", Provider: "p", Model: "m", SameProtocol: true},
		// 同一个上游的**不同模型**是两条，不能被当成重复。
		{Source: "s", Provider: "p", Model: "m2"},
	}
	got := orderCandidates(in, consts.StyleOpenAI)
	if len(got) != 2 {
		t.Fatalf("去重后 = %d 条，期望 2 条", len(got))
	}
	// 先出现的那条被留下（SameProtocol=false），两者都不在同协议桶里，
	// 所以顺序就是出现顺序。
	if got[0].Model != "m" || got[1].Model != "m2" {
		t.Errorf("顺序 = %q, %q，期望 m, m2", got[0].Model, got[1].Model)
	}
}

func TestOrderCandidatesEmpty(t *testing.T) {
	if got := orderCandidates(nil, consts.StyleOpenAI); got != nil {
		t.Errorf("空输入该返回 nil，得到 %v", got)
	}
}

func TestBaseURLFromConfig(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"base_url":"https://api.deepseek.com"}`, "https://api.deepseek.com"},
		{`{"base_url":"","proxy":""}`, ""},
		{`{}`, ""},
		{``, ""},
		{`not json`, ""},
		// 大小写不同的字段名解不出来。这是有意的：四种协议的配置结构体
		// 用的都是 base_url，多认几种写法只会把"两边不一致"藏起来。
		{`{"BaseURL":"https://api.deepseek.com"}`, ""},
	} {
		if got := baseURLFromConfig(tc.in); got != tc.want {
			t.Errorf("baseURLFromConfig(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestSuggestEmptyModel：模型名空着时不该命中任何东西，也不该炸。
func TestSuggestEmptyModel(t *testing.T) {
	m := fullTestManager(t)
	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "",
	}, policyFor())

	if got.Matched {
		t.Error("空模型名不该命中")
	}
	if got.Reason != ReasonNoModelMatch {
		t.Errorf("Reason = %q，期望 %q", got.Reason, ReasonNoModelMatch)
	}
}

// TestSuggestUnknownSourceIsSkipped：策略里写了不认识的名字时，
// 它不该把整个查询拖成 catalog_unavailable。
func TestSuggestUnknownSourceIsSkipped(t *testing.T) {
	m := fullTestManager(t)

	got := m.Suggest(context.Background(), Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	}, policyFor("no-such-source"))

	if !got.Matched {
		t.Fatalf("不认识的来源该被丢掉、退回默认，得到 reason=%q", got.Reason)
	}
}

// TestSuggestPackageLevelEntry 只验证便捷入口不炸。
//
// Suggest(ctx, q) 走的是进程级管理器和库里的策略；这里没有装索引，
// 也**不该**让它去联网——第一次调用会起一个后台抓取并返回
// catalog_unavailable。测试只要求它给出一个明确的结论而不是卡住。
func TestSuggestPackageLevelEntry(t *testing.T) {
	setupMetaDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 先调一次 Default() 把 sync.Once 消化掉，再换掉它——否则 Do 会在
	// 第一次调用时把假管理器覆盖回真的，测试就真去抓两个几 MB 的文件了。
	_ = Default()
	prev := defaultManager
	defaultManager = testManager(t, modelsDevCatalog(t), liteLLMCatalog(t))
	t.Cleanup(func() { defaultManager = prev })

	got := Suggest(ctx, Query{
		ProviderType:   consts.StyleAnthropic,
		ProviderConfig: `{"base_url":"https://api.anthropic.com"}`,
		ProviderModel:  "claude-sonnet-4-5",
	})
	if !got.Matched {
		t.Fatalf("期望命中，得到 reason=%q", got.Reason)
	}
	if got.Source != SourceModelsDev {
		t.Errorf("Source = %q，期望 %q", got.Source, SourceModelsDev)
	}
}
