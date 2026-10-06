package modelmeta

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
)

// 这一组钉的是 models.dev 解析器的**三态纪律**：源里没有的字段必须一路保持
// nil，不能在任何一步被折成 false / 0。三个能力字段与三档价格里，
// 有 44% 的模型没有 structured_output、437/8389 条缺 limit 或 cost——
// 这不是边角情况，是被引用最多的那批模型以外的常态。

func TestParseModelsDevCounts(t *testing.T) {
	cat := modelsDevCatalog(t)
	if cat.Source != SourceModelsDev {
		t.Errorf("Source = %q，期望 %q", cat.Source, SourceModelsDev)
	}
	// 语料里 8 家上游共 11 条模型。数字本身不重要，重要的是它是**精确**的：
	// 少一条说明有条目在解析时被悄悄吞了。
	if cat.EntryCount != 11 {
		t.Errorf("EntryCount = %d，期望 11", cat.EntryCount)
	}
	if len(cat.entries) != cat.EntryCount {
		t.Errorf("entries 长度 %d 与 EntryCount %d 不一致", len(cat.entries), cat.EntryCount)
	}
}

// TestModelsDevMissingStructuredOutputStaysNil 是**最重要的一条**。
//
// deepinfra 的 stepfun 那条没有 structured_output 字段。如果解析器把它折成
// false，用户保存后这个上游就会从结构化输出的候选池里消失——而且不会有
// 任何报错，只是某个请求突然路由到了别的上游。
func TestModelsDevMissingStructuredOutputStaysNil(t *testing.T) {
	cat := modelsDevCatalog(t)
	e := entryOf(t, cat, "deepinfra", "stepfun-ai/Step-3.7-Flash")

	if e.StructuredOutput != nil {
		t.Errorf("StructuredOutput = %v，源里没有这个字段，必须是 nil", *e.StructuredOutput)
	}
	// 同一条里 tool_call 是有的，用来对照：不是"整条都没解析出来"。
	if !boolValue(t, e.ToolCall) {
		t.Error("ToolCall 期望 true")
	}
	if !boolValue(t, e.Image) {
		t.Error("Image 期望 true（modalities.input 含 image）")
	}
}

// TestModelsDevMissingCostKeepsAllPricesNil：poe 的 cerebras 那条整个 cost
// 块都不在。三档价格必须全 nil，**不能**变成 0——0 是"免费"，nil 才是
// "不知道"，而日志页会拿 0 去算成本并显示为 0 元。
func TestModelsDevMissingCostKeepsAllPricesNil(t *testing.T) {
	cat := modelsDevCatalog(t)
	e := entryOf(t, cat, "poe", "cerebras/qwen3-32b-cs")

	if e.InputPrice != nil {
		t.Errorf("InputPrice = %v，期望 nil", *e.InputPrice)
	}
	if e.OutputPrice != nil {
		t.Errorf("OutputPrice = %v，期望 nil", *e.OutputPrice)
	}
	if e.CacheReadPrice != nil {
		t.Errorf("CacheReadPrice = %v，期望 nil", *e.CacheReadPrice)
	}
	// limit 块在，但两个数都是 0。这两个 0 是**源自己写的 0**，
	// 与"缺字段"不同，所以它们原样带 0 走（当前只用于候选文案）。
	if e.ContextLimit != 0 || e.OutputLimit != 0 {
		t.Errorf("limits = (%d, %d)，语料里就是 0", e.ContextLimit, e.OutputLimit)
	}
	// 这条的 modalities.input 只有 text，所以 Image 是**明确的 false**
	// （非 nil）——它和上面那种 nil 是两回事，这条断言把两者分开了。
	if e.Image == nil {
		t.Fatal("Image 期望非 nil 的 false（modalities 存在且不含 image）")
	}
	if *e.Image {
		t.Error("Image 期望 false")
	}
}

// TestModelsDevImageFromModalities：视觉是从 modalities.input 里有没有
// "image" 折出来的，源里没有独立的布尔字段。
func TestModelsDevImageFromModalities(t *testing.T) {
	cat := modelsDevCatalog(t)
	if !boolValue(t, entryOf(t, cat, "anthropic", "claude-sonnet-4-5").Image) {
		t.Error("claude-sonnet-4-5 的 Image 期望 true")
	}
	if boolValue(t, entryOf(t, cat, "poe", "cerebras/qwen3-32b-cs").Image) {
		t.Error("poe/cerebras 的 Image 期望 false")
	}
}

// TestModelsDevMissingModalitiesStaysNil：整个 modalities 块缺失时，
// Image 必须是 nil 而不是 false。语料里没有这种条目（实测 8389 条都有），
// 所以用一个最小内联载荷补上这一个分支。
func TestModelsDevMissingModalitiesStaysNil(t *testing.T) {
	var raw map[string]modelsDevProvider
	payload := `{
		"acme": {
			"id": "acme", "name": "Acme",
			"models": {"m1": {"tool_call": true}}
		}
	}`
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatalf("解内联载荷: %v", err)
	}
	cat := parseModelsDev(raw, testFetchedAt)
	e := entryOf(t, cat, "acme", "m1")

	if e.Image != nil {
		t.Errorf("Image = %v，modalities 整块缺失时必须是 nil", *e.Image)
	}
	if e.StructuredOutput != nil {
		t.Errorf("StructuredOutput = %v，期望 nil", *e.StructuredOutput)
	}
	if e.InputPrice != nil || e.OutputPrice != nil || e.CacheReadPrice != nil {
		t.Error("cost 整块缺失时三档价格都该是 nil")
	}
	if e.ContextLimit != 0 || e.OutputLimit != 0 {
		t.Error("limit 整块缺失时两个上限都该是 0")
	}
}

func TestModelsDevPricesAndLimits(t *testing.T) {
	cat := modelsDevCatalog(t)
	e := entryOf(t, cat, "openai", "gpt-5.4")

	if got := floatValue(t, e.InputPrice); got != 2.5 {
		t.Errorf("InputPrice = %v，期望 2.5", got)
	}
	if got := floatValue(t, e.CacheReadPrice); got != 0.25 {
		t.Errorf("CacheReadPrice = %v，期望 0.25", got)
	}
	if got := floatValue(t, e.OutputPrice); got != 15 {
		t.Errorf("OutputPrice = %v，期望 15", got)
	}
	// models.dev 的价格口径就是"每百万 token 美元"，不做任何换算。
	// 与 LiteLLM 那边（每 token，要乘 1e6）是两个单位，混了就是 100 万倍的错。
	if e.Currency != CurrencyUSD {
		t.Errorf("Currency = %q，期望 %q", e.Currency, CurrencyUSD)
	}
	if e.ContextLimit != 1050000 {
		t.Errorf("ContextLimit = %d，期望 1050000", e.ContextLimit)
	}
	if e.OutputLimit != 128000 {
		t.Errorf("OutputLimit = %d，期望 128000", e.OutputLimit)
	}
}

// TestModelsDevProviderIndexes：上游对齐的三级索引。
//
// models.dev 的 api 字段只有 226 家里的 200 家带、填了值的又只有
// openai-compatible 的那 185 家，所以官方上游（anthropic / openai / google）
// 在这两张表里**必然缺席**，只能靠别名表——这条断言同时钉住了那个事实。
func TestModelsDevProviderIndexes(t *testing.T) {
	cat := modelsDevCatalog(t)

	for raw, want := range map[string]string{
		"https://api.deepseek.com":     "deepseek",
		"https://openrouter.ai/api/v1": "openrouter",
		"https://api.qnaigc.com/v1":    "qiniu-ai",
		"https://api.poe.com/v1":       "poe",
	} {
		got, ok := cat.providerByURL[normalizeURL(raw)]
		if !ok {
			t.Errorf("providerByURL 里没有 %s", raw)
			continue
		}
		if got != want {
			t.Errorf("providerByURL[%s] = %q，期望 %q", raw, got, want)
		}
	}

	for host, want := range map[string]string{
		"api.deepseek.com": "deepseek",
		"api.qnaigc.com":   "qiniu-ai",
		"openrouter.ai":    "openrouter",
	} {
		if got := cat.providerByHost[host]; got != want {
			t.Errorf("providerByHost[%s] = %q，期望 %q", host, got, want)
		}
	}

	// 官方上游的 api 是空的：两张表里都不该有它们。这不是遗漏，
	// 是源的真实形态，对齐得靠 hostAliases。
	for _, host := range []string{"api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com"} {
		if got, ok := cat.providerByHost[host]; ok {
			t.Errorf("providerByHost[%s] = %q，源里这几家的 api 为空，不该有索引", host, got)
		}
	}
}

func TestModelsDevProviderStyleFromNpm(t *testing.T) {
	cat := modelsDevCatalog(t)

	if got := cat.providerStyle["qiniu-ai"]; got != consts.StyleOpenAI {
		t.Errorf("qiniu-ai 的风格 = %q，期望 %q", got, consts.StyleOpenAI)
	}
	if got := cat.providerStyle["anthropic"]; got != consts.StyleAnthropic {
		t.Errorf("anthropic 的风格 = %q，期望 %q", got, consts.StyleAnthropic)
	}
	if got := cat.providerStyle["google"]; got != consts.StyleGemini {
		t.Errorf("google 的风格 = %q，期望 %q", got, consts.StyleGemini)
	}
	// 折不出来的包名留空**且不留键**。留一个空串键会让"有没有风格"
	// 这个判断从"键在不在"变成"值空不空"，两处写法迟早会不一致。
	if got, ok := cat.providerStyle["openrouter"]; ok {
		t.Errorf("openrouter 的风格 = %q，包名不认得，期望没有这个键", got)
	}
}

func TestModelsDevProviderName(t *testing.T) {
	cat := modelsDevCatalog(t)
	if got := cat.providerName("qiniu-ai"); got != "Qiniu" {
		t.Errorf("providerName(qiniu-ai) = %q，期望 Qiniu", got)
	}
	// 没有显示名时用 id 顶。提示文案里必须写出"是哪个上游"，
	// 空字符串会让那句话读不通。
	if got := cat.providerName("no-such-provider"); got != "no-such-provider" {
		t.Errorf("providerName(未知) = %q，期望回落成 id", got)
	}
}

func TestModelsDevStatus(t *testing.T) {
	cat := modelsDevCatalog(t)
	if got := entryOf(t, cat, "deepseek", "deepseek-v4-flash-vision-exp").Status; got != "deprecated" {
		t.Errorf("Status = %q，期望 deprecated", got)
	}
	if got := entryOf(t, cat, "deepseek", "deepseek-flash").Status; got != "" {
		t.Errorf("Status = %q，期望空串", got)
	}
}

// TestParseModelsDevDeterministic：JSON 对象解进 map 之后迭代顺序是随机的，
// 而索引规则是"先写进去的赢"。不排序就会让同一份数据每次启动给出不同的
// 匹配结果——包括这里没测到的那些大小写重复键。
func TestParseModelsDevDeterministic(t *testing.T) {
	var raw map[string]modelsDevProvider
	if err := json.Unmarshal(readFixture(t, fixtureModelsDev), &raw); err != nil {
		t.Fatalf("解语料: %v", err)
	}
	first := parseModelsDev(raw, testFetchedAt)
	for i := 0; i < 8; i++ {
		again := parseModelsDev(raw, testFetchedAt)
		if !reflect.DeepEqual(first.entries, again.entries) {
			t.Fatalf("第 %d 次解析的条目顺序与第一次不同", i+1)
		}
	}
	// 排序的另一个可见后果：反过来也能查。语料里 "deepseek-flash" 排在
	// "deepseek-v4-flash-vision-exp" 前面，两者都在同一个上游下。
	if !hasEntry(first, "deepseek", "deepseek-flash") || !hasEntry(first, "deepseek", "deepseek-v4-flash-vision-exp") {
		t.Error("deepseek 的两条都该在索引里")
	}
}

func TestModelsDevFetch(t *testing.T) {
	doer := &stubDoer{body: readFixture(t, fixtureModelsDev)}
	cat, err := newModelsDevSource(doer).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doer.gotURL != modelsDevURL {
		t.Errorf("请求 URL = %q，期望 %q", doer.gotURL, modelsDevURL)
	}
	if doer.gotAccept != "application/json" {
		t.Errorf("Accept = %q，期望 application/json", doer.gotAccept)
	}
	if cat.EntryCount != 11 {
		t.Errorf("EntryCount = %d，期望 11", cat.EntryCount)
	}
	// Fetch 里的时间戳是**抓取那一刻**，缓存新鲜度全靠它。
	if time.Since(cat.FetchedAt) > time.Minute {
		t.Errorf("FetchedAt = %v，期望接近现在", cat.FetchedAt)
	}
}

func TestModelsDevFetchErrors(t *testing.T) {
	t.Run("状态码不是 200", func(t *testing.T) {
		doer := &stubDoer{status: 503, body: []byte(`{}`)}
		if _, err := newModelsDevSource(doer).Fetch(context.Background()); err == nil {
			t.Fatal("期望报错")
		}
	})

	t.Run("载荷为空", func(t *testing.T) {
		// 空载荷要当失败，不能当"零条模型"：后者会让缓存存下一份空索引，
		// 之后 24 小时里所有建议都查不到东西。
		doer := &stubDoer{body: []byte(`{}`)}
		if _, err := newModelsDevSource(doer).Fetch(context.Background()); err == nil {
			t.Fatal("期望报错")
		}
	})

	t.Run("网络错误", func(t *testing.T) {
		doer := &stubDoer{err: errors.New("boom")}
		if _, err := newModelsDevSource(doer).Fetch(context.Background()); err == nil {
			t.Fatal("期望报错")
		}
	})

	t.Run("载荷不是合法 JSON", func(t *testing.T) {
		doer := &stubDoer{body: []byte(`not json`)}
		if _, err := newModelsDevSource(doer).Fetch(context.Background()); err == nil {
			t.Fatal("期望报错")
		}
	})
}
