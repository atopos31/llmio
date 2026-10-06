package modelmeta

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// 这一组钉的是 LiteLLM 解析器。它比 models.dev 那边多两处独有的坑：
// 价格是"每 token"（要乘 1e6）、键带 `<上游>/` 命名空间（要做尾名别名），
// 外加一个字段名陷阱（§2.2）。

func TestParseLiteLLMCounts(t *testing.T) {
	cat := liteLLMCatalog(t)
	if cat.Source != SourceLiteLLM {
		t.Errorf("Source = %q，期望 %q", cat.Source, SourceLiteLLM)
	}
	// 语料 10 个键 - sample_spec - fallback_generalizations - 2 条 embedding = 6。
	if cat.EntryCount != 6 {
		t.Errorf("EntryCount = %d，期望 6", cat.EntryCount)
	}
	// LiteLLM 没有上游显示名，ProviderName 用 id 顶——提示文案里那句
	// "数据源在「X」下"必须有东西可填。
	for _, e := range cat.entries {
		if e.ProviderName != e.Provider {
			t.Errorf("%s 的 ProviderName = %q，期望与 Provider 相同", e.Model, e.ProviderName)
		}
		if e.Currency != CurrencyUSD {
			t.Errorf("%s 的 Currency = %q，期望 %q", e.Model, e.Currency, CurrencyUSD)
		}
	}
}

// TestLiteLLMSkipsNonChatModes：这个文件里有 19 种 mode，embedding 有 151 条、
// image_generation 有 408 条。llmio 是对话代理，嵌入条目的能力字段与价格
// 与对话不是一回事，混进来只会污染建议。
func TestLiteLLMSkipsNonChatModes(t *testing.T) {
	cat := liteLLMCatalog(t)
	for _, e := range cat.entries {
		if e.Provider != "together_ai" {
			continue
		}
		t.Errorf("together_ai 的两条都是 embedding，不该进索引：%s", e.Model)
	}
	if hasEntry(cat, "together_ai", "together_ai/baai/bge-base-en-v1.5") ||
		hasEntry(cat, "together_ai", "together_ai/BAAI/bge-base-en-v1.5") {
		t.Error("embedding 条目被收进来了")
	}
}

// TestLiteLLMSkipsNonModelKeys：顶层有两个与模型条目**共用命名空间**的键——
// sample_spec 是字段说明占位符（值里 mode 是"one of: chat, embedding, ..."），
// fallback_generalizations 是正则规则表。逐条解码是为了能跳过它们而不整包失败。
func TestLiteLLMSkipsNonModelKeys(t *testing.T) {
	cat := liteLLMCatalog(t)
	for _, e := range cat.entries {
		if e.Model == "sample_spec" || e.Model == "fallback_generalizations" {
			t.Errorf("非模型键进了索引：%s", e.Model)
		}
		// sample_spec 的 litellm_provider 是一句说明文字。它要是漏进来，
		// 上游对齐会被一个不存在的上游名污染。
		if e.Provider == "one of https://docs.litellm.ai/docs/providers" {
			t.Error("sample_spec 的说明字符串被当成了上游 id")
		}
	}
}

// TestLiteLLMStructuredOutputUsesResponseSchema 钉的是字段名陷阱。
//
// 正确的字段是 supports_response_schema；supports_structured_output 在这个
// 文件里 0/4480 条。映射错了不会报错，只会静默 0 填充——所有模型的
// 结构化输出建议都变成"源没提供"，一行日志都不会有。
func TestLiteLLMStructuredOutputUsesResponseSchema(t *testing.T) {
	cat := liteLLMCatalog(t)
	e := entryOf(t, cat, "anthropic", "claude-sonnet-4-5")
	if got := boolValue(t, e.StructuredOutput); !got {
		t.Error("StructuredOutput 期望 true（来自 supports_response_schema）")
	}
	if got := boolValue(t, e.ToolCall); !got {
		t.Error("ToolCall 期望 true")
	}
	if got := boolValue(t, e.Image); !got {
		t.Error("Image 期望 true（supports_vision）")
	}
}

// TestLiteLLMIgnoresNativeStructuredOutput：即使只有一个"看起来更对"的
// 字段在，也不能读它。这条用一个最小载荷单独钉住——语料里两条字段总是同时
// 出现，光看语料分不出解析器读的是哪一个。
func TestLiteLLMIgnoresNativeStructuredOutput(t *testing.T) {
	payload := `{
		"acme/m1": {
			"mode": "chat", "litellm_provider": "acme",
			"supports_native_structured_output": true
		},
		"acme/m2": {
			"mode": "chat", "litellm_provider": "acme",
			"supports_response_schema": false
		}
	}`
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatalf("解内联载荷: %v", err)
	}
	cat := parseLiteLLM(raw, testFetchedAt)

	if e := entryOf(t, cat, "acme", "acme/m1"); e.StructuredOutput != nil {
		t.Errorf("StructuredOutput = %v，supports_native_structured_output 不该被读", *e.StructuredOutput)
	}
	// 明确写了 false 的要保住 false（非 nil）——这是三态里的第三态。
	e := entryOf(t, cat, "acme", "acme/m2")
	if e.StructuredOutput == nil {
		t.Fatal("StructuredOutput 期望非 nil 的 false")
	}
	if *e.StructuredOutput {
		t.Error("StructuredOutput 期望 false")
	}
}

// TestLiteLLMCostsScaledToPerMillion：LiteLLM 记的是每 token 单价，
// llmio 的表存的是每百万 token。少乘 1e6 会得到 0.000003 这种数字，
// 前端显示成 0，日志页的成本全是 0。
func TestLiteLLMCostsScaledToPerMillion(t *testing.T) {
	cat := liteLLMCatalog(t)
	e := entryOf(t, cat, "anthropic", "claude-sonnet-4-5")

	if got := floatValue(t, e.InputPrice); got != 3 {
		t.Errorf("InputPrice = %v，期望 3（语料里是 3e-06）", got)
	}
	if got := floatValue(t, e.CacheReadPrice); got != 0.3 {
		t.Errorf("CacheReadPrice = %v，期望 0.3（语料里是 3e-07）", got)
	}
	if got := floatValue(t, e.OutputPrice); got != 15 {
		t.Errorf("OutputPrice = %v，期望 15（语料里是 1.5e-05）", got)
	}
	if e.ContextLimit != 1000000 {
		t.Errorf("ContextLimit = %d，期望 1000000", e.ContextLimit)
	}
	if e.OutputLimit != 64000 {
		t.Errorf("OutputLimit = %d，期望 64000", e.OutputLimit)
	}
}

// TestLiteLLMMissingFieldsStayNil：zai/glm-5 没有 supports_response_schema，
// 也没有 supports_vision。两者都必须保持 nil——LiteLLM 的 supports_vision
// 只覆盖 42% 的对话条目，把缺的当 false 会给一半模型凭空断言"不支持图片"。
func TestLiteLLMMissingFieldsStayNil(t *testing.T) {
	cat := liteLLMCatalog(t)
	e := entryOf(t, cat, "zai", "zai/glm-5")

	if e.StructuredOutput != nil {
		t.Errorf("StructuredOutput = %v，期望 nil", *e.StructuredOutput)
	}
	if e.Image != nil {
		t.Errorf("Image = %v，期望 nil", *e.Image)
	}
	if !boolValue(t, e.ToolCall) {
		t.Error("ToolCall 期望 true（supports_function_calling 在）")
	}
	if got := floatValue(t, e.InputPrice); got != 1 {
		t.Errorf("InputPrice = %v，期望 1", got)
	}
	if got := floatValue(t, e.OutputPrice); got != 3.2 {
		t.Errorf("OutputPrice = %v，期望 3.2", got)
	}
}

func TestLiteLLMStatus(t *testing.T) {
	cat := liteLLMCatalog(t)
	// 有 deprecation_date 就是 deprecated，**不拿它跟今天比**：
	// 比日期会让同一个模型今年给建议、明年不给，测试也就钉不住。
	if got := entryOf(t, cat, "anthropic", "claude-sonnet-4-5").Status; got != "deprecated" {
		t.Errorf("Status = %q，期望 deprecated（语料里 deprecation_date=2026-11-30，晚于今天也一样）", got)
	}
	if got := entryOf(t, cat, "anthropic", "claude-opus-4-5-20251101").Status; got != "" {
		t.Errorf("Status = %q，期望空串", got)
	}
}

// TestLiteLLMNamespaceAlias：LiteLLM 的键带 `<上游>/` 命名空间
// （zai 的 16 条全是 `zai/glm-5` 这种形态），而用户在 llmio 里填的是
// 上游自己的名字 `glm-5`。不做这层索引侧别名，LiteLLM 对这批上游
// 一条也匹配不上。
func TestLiteLLMNamespaceAlias(t *testing.T) {
	cat := liteLLMCatalog(t)

	idx, rule, ok := cat.lookupInProvider("zai", "glm-5")
	if !ok {
		t.Fatal("zai/glm-5 该按尾名别名命中")
	}
	// 报告 id_prefix 而不是 exact：这一格是别名，说 exact 是在撒谎，
	// 而 match_rule 就是给用户判断可信度用的。
	if rule != RuleIDPrefix {
		t.Errorf("rule = %q，期望 %q", rule, RuleIDPrefix)
	}
	if got := cat.entries[idx].Model; got != "zai/glm-5" {
		t.Errorf("命中的模型 = %q，期望源里的全名 zai/glm-5", got)
	}

	// 全名也照样能命中，而且**报的是 exact**——别名不该顶掉真身。
	if _, rule, ok := cat.lookupInProvider("zai", "zai/glm-5"); !ok || rule != RuleExact {
		t.Errorf("全名查找 = (%q, %v)，期望 (exact, true)", rule, ok)
	}
}

// TestLiteLLMFullNameWinsOverAlias：deepseek 下同时有 `deepseek-chat`
// 与 `deepseek/deepseek-chat`（同一份数据的两种写法）。查 `deepseek-chat`
// 必须命中**真身**，不能被别名指到带命名空间的那条上——两条的字段并不完全
// 一样（命名空间那条多了 cache_creation_input_token_cost）。
func TestLiteLLMFullNameWinsOverAlias(t *testing.T) {
	cat := liteLLMCatalog(t)

	idx, rule, ok := cat.lookupInProvider("deepseek", "deepseek-chat")
	if !ok {
		t.Fatal("deepseek-chat 该命中")
	}
	if rule != RuleExact {
		t.Errorf("rule = %q，期望 exact", rule)
	}
	if got := cat.entries[idx].Model; got != "deepseek-chat" {
		t.Errorf("命中的模型 = %q，期望 deepseek-chat", got)
	}
}

// TestLiteLLMCaseDuplicateKeys：这个文件里真实存在只差大小写的重复键
// （together_ai/baai/... 与 together_ai/BAAI/...）。go 的 map 会静默取后者，
// 所以只有先排序再建索引，结果才是确定的。
func TestLiteLLMCaseDuplicateKeys(t *testing.T) {
	payload := `{
		"acme/BAAI/m1": {"mode": "chat", "litellm_provider": "acme", "input_cost_per_token": 1e-06},
		"acme/baai/m1": {"mode": "chat", "litellm_provider": "acme", "input_cost_per_token": 9e-06}
	}`
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatalf("解内联载荷: %v", err)
	}

	// 尾名别名把两条压到同一个键上，先写进去的赢。跑多次必须给同一个答案。
	var firstModel string
	var firstPrice float64
	for i := 0; i < 8; i++ {
		cat := parseLiteLLM(raw, testFetchedAt)
		idx, _, ok := cat.lookupInProvider("acme", "m1")
		if !ok {
			t.Fatal("m1 该按尾名命中")
		}
		model := cat.entries[idx].Model
		price := floatValue(t, cat.entries[idx].InputPrice)
		if i == 0 {
			firstModel, firstPrice = model, price
			continue
		}
		if model != firstModel || price != firstPrice {
			t.Fatalf("第 %d 次命中的是 %s(%v)，第一次是 %s(%v)", i+1, model, price, firstModel, firstPrice)
		}
	}
	// 排序是字节序：大写 B(0x42) 在小写 b(0x62) 前，所以 BAAI 那条赢。
	if firstModel != "acme/BAAI/m1" {
		t.Errorf("命中 %q，期望 acme/BAAI/m1", firstModel)
	}
	if firstPrice != 1 {
		t.Errorf("价格 = %v，期望 1（即语料里的 1e-06）", firstPrice)
	}
}

// TestLiteLLMBrokenEntryDoesNotKillTheRest：一条形态意外不该让整份建议消失。
func TestLiteLLMBrokenEntryDoesNotKillTheRest(t *testing.T) {
	payload := `{
		"acme/broken": "this is not an object",
		"acme/m1": {"mode": "chat", "litellm_provider": "acme", "supports_function_calling": true}
	}`
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatalf("解内联载荷: %v", err)
	}
	cat := parseLiteLLM(raw, testFetchedAt)

	if cat.EntryCount != 1 {
		t.Fatalf("EntryCount = %d，期望 1", cat.EntryCount)
	}
	if !boolValue(t, entryOf(t, cat, "acme", "acme/m1").ToolCall) {
		t.Error("好条目该照常解析")
	}
}

// TestLiteLLMNoProviderSkipped：没有 litellm_provider 的条目落不进任何上游，
// 留着只会变成一条永远对齐不上的死数据。
func TestLiteLLMNoProviderSkipped(t *testing.T) {
	payload := `{
		"m1": {"mode": "chat", "supports_function_calling": true}
	}`
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatalf("解内联载荷: %v", err)
	}
	if cat := parseLiteLLM(raw, testFetchedAt); cat.EntryCount != 0 {
		t.Errorf("EntryCount = %d，期望 0", cat.EntryCount)
	}
}

func TestScaleCostNil(t *testing.T) {
	if got := scaleCost(nil); got != nil {
		t.Errorf("scaleCost(nil) = %v，期望 nil", *got)
	}
	zero := 0.0
	if got := scaleCost(&zero); got == nil {
		t.Fatal("0 要保住 0，不能折成 nil")
	} else if *got != 0 {
		t.Errorf("scaleCost(0) = %v，期望 0", *got)
	}
}

func TestLiteLLMFetch(t *testing.T) {
	doer := &stubDoer{body: readFixture(t, fixtureLiteLLM)}
	cat, err := newLiteLLMSource(doer).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doer.gotURL != liteLLMURL {
		t.Errorf("请求 URL = %q，期望 %q", doer.gotURL, liteLLMURL)
	}
	if cat.EntryCount != 6 {
		t.Errorf("EntryCount = %d，期望 6", cat.EntryCount)
	}
	// LiteLLM 没有 models.dev 那种上游 api 字段，两张对齐索引必须为空——
	// 它的上游对齐只能靠静态别名表与类型兜底。
	if len(cat.providerByURL) != 0 || len(cat.providerByHost) != 0 {
		t.Error("LiteLLM 该没有 providerByURL / providerByHost 索引")
	}
}

func TestLiteLLMFetchErrors(t *testing.T) {
	t.Run("状态码不是 200", func(t *testing.T) {
		doer := &stubDoer{status: 404, body: []byte(`{}`)}
		if _, err := newLiteLLMSource(doer).Fetch(context.Background()); err == nil {
			t.Fatal("期望报错")
		}
	})
	t.Run("载荷为空", func(t *testing.T) {
		doer := &stubDoer{body: []byte(`{}`)}
		if _, err := newLiteLLMSource(doer).Fetch(context.Background()); err == nil {
			t.Fatal("期望报错")
		}
	})
	t.Run("网络错误", func(t *testing.T) {
		doer := &stubDoer{err: errors.New("boom")}
		if _, err := newLiteLLMSource(doer).Fetch(context.Background()); err == nil {
			t.Fatal("期望报错")
		}
	})
}
