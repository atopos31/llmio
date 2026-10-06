package modelmeta

import (
	"reflect"
	"testing"

	"github.com/atopos31/llmio/consts"
)

// 模型 id 归一化是这套建议里唯一"猜"的地方，所以每一条规则都得能被单独
// 指名道姓地测出来。这一组做两件事：逐步验证六步阶梯本身，以及验证
// **报出来的规则名是实话**——把大小写差异报成 exact 会让用户高估可信度。

func rulesOf(forms []normalizeForm) []string {
	out := make([]string, 0, len(forms))
	for _, f := range forms {
		out = append(out, f.rule)
	}
	return out
}

func TestNormalizeFormsLadder(t *testing.T) {
	// 这一个查询同时触发全部六步，且每一步都真的产生了差异。
	got := rulesOf(normalizeForms("models/Claude-Opus-4.5-20251101:free"))
	want := []string{
		RuleExact, RuleCase, RuleModelsPrefix, RuleSuffixStrip, RuleDateStrip, RuleDotDash,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("阶梯 = %v，期望 %v", got, want)
	}

	forms := normalizeForms("models/Claude-Opus-4.5-20251101:free")
	values := make([]string, 0, len(forms))
	for _, f := range forms {
		values = append(values, f.value)
	}
	wantValues := []string{
		"models/Claude-Opus-4.5-20251101:free",
		"models/claude-opus-4.5-20251101:free",
		"claude-opus-4.5-20251101:free",
		"claude-opus-4.5-20251101",
		"claude-opus-4.5",
		"claude-opus-4-5",
	}
	if !reflect.DeepEqual(values, wantValues) {
		t.Errorf("阶梯取值 = %v，期望 %v", values, wantValues)
	}
}

// TestNormalizeFormsNoGhostSteps：没发生的变换不能记。
//
// 这是"规则名是实话"的地基：查询已经全小写时报 case，等于给一个精确命中
// 贴了张"我们做过归一化"的标签；反过来，用户看到 dot_dash 才知道
// "这条是按点号↔横线换过来的"，那正是他需要复核的时候。
func TestNormalizeFormsNoGhostSteps(t *testing.T) {
	// 已经小写、无点号、无后缀、无日期：一步都不该有。
	if got := rulesOf(normalizeForms("glm-5")); !reflect.DeepEqual(got, []string{RuleExact}) {
		t.Errorf("glm-5 的阶梯 = %v，期望只有 exact", got)
	}
	// 有点号才该出现 dot_dash。
	if got := rulesOf(normalizeForms("gpt-5.4")); !reflect.DeepEqual(got, []string{RuleExact, RuleDotDash}) {
		t.Errorf("gpt-5.4 的阶梯 = %v，期望 [exact dot_dash]", got)
	}
	// 只有真正去掉了 :free 才有 suffix_strip。
	if got := rulesOf(normalizeForms("gpt-5.4:free")); !reflect.DeepEqual(got, []string{RuleExact, RuleSuffixStrip, RuleDotDash}) {
		t.Errorf("gpt-5.4:free 的阶梯 = %v，期望 [exact suffix_strip dot_dash]", got)
	}
}

func TestTrimKnownSuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"gpt-5.4:free", "gpt-5.4"},
		{"gpt-5.4:FREE", "gpt-5.4"}, // 大小写不敏感：两个源里的写法并不统一
		{"gpt-5.4:batch", "gpt-5.4"},
		{"gpt-5.4", "gpt-5.4"},
		// :thinking 在源里是**另一个模型**，不是同一个模型的变体。
		// 去掉冒号会把它们混成一谈——这是有意不做的归一化。
		{"claude-opus-4-5:thinking", "claude-opus-4-5:thinking"},
		{"", ""},
	} {
		if got := trimKnownSuffix(tc.in); got != tc.want {
			t.Errorf("trimKnownSuffix(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestTrimDateSuffix(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		reason   string
	}{
		{"claude-opus-4-5-20251101", "claude-opus-4-5", "标准的 -YYYYMMDD"},
		{"claude-opus-4-5", "claude-opus-4-5", "没有日期后缀就不动"},
		{"claude-2025110", "claude-2025110", "7 位不是日期"},
		{"claude-202511011", "claude-202511011", "9 位不是日期"},
		{"claude-2025110a", "claude-2025110a", "含非数字"},
		{"claude", "claude", "一个短横线都没有"},
		{"-20251101", "-20251101", "短横线在开头，切掉会得到空串"},
		{"m-20251101", "m", "最短的可切形态"},
	} {
		if got := trimDateSuffix(tc.in); got != tc.want {
			t.Errorf("trimDateSuffix(%q) = %q，期望 %q（%s）", tc.in, got, tc.want, tc.reason)
		}
	}
}

func TestSwapDotDash(t *testing.T) {
	// 只做一个方向。反方向没有确定答案——`claude-opus-4-5` 里三个短横线
	// 哪个该变成点？实测到的差异是单向的。
	if got := swapDotDash("anthropic/claude-opus-4.5"); got != "anthropic/claude-opus-4-5" {
		t.Errorf("swapDotDash = %q", got)
	}
	if got := swapDotDash("claude-opus-4-5"); got != "claude-opus-4-5" {
		t.Errorf("不该动没有点号的 id，得到 %q", got)
	}
}

func TestPathTail(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"zai/glm-5", "glm-5"},
		{"together_ai/BAAI/bge-base-en-v1.5", "bge-base-en-v1.5"},
		{"glm-5", "glm-5"},
		{"/m", "m"},
	} {
		if got := pathTail(tc.in); got != tc.want {
			t.Errorf("pathTail(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestCanonicalID 是跨上游候选的判据。它必须把"同一个模型的几种写法"
// 压到同一个串上——压不到就漏候选，压过头就把不同模型混成一谈。
func TestCanonicalID(t *testing.T) {
	// 同一个 Claude，四种写法（源里的原样、带点号、带日期、带 :free）
	// 必须全部压成同一个形状。
	for _, in := range []string{
		"claude-opus-4-5",
		"claude-opus-4.5",
		"claude-opus-4-5-20251101",
		"models/Claude-Opus-4.5-20251101:free",
	} {
		if got := canonicalID(in); got != "claude-opus-4-5" {
			t.Errorf("canonicalID(%q) = %q，期望 claude-opus-4-5", in, got)
		}
	}
	// 不同模型不能被压到一起。
	if canonicalID("claude-opus-4-5") == canonicalID("claude-opus-4-6") {
		t.Error("4-5 与 4-6 是不同模型")
	}
	// canonicalID **不去厂商命名空间**——那是建索引时的尾名别名干的活
	// （报 id_prefix），两者是不同的东西，这里把它钉住免得日后被合并。
	if canonicalID("deepseek/deepseek-chat") != "deepseek/deepseek-chat" {
		t.Errorf("canonicalID 不该去命名空间，得到 %q", canonicalID("deepseek/deepseek-chat"))
	}
}

func TestNormalizeURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://api.deepseek.com", "api.deepseek.com"},
		{"https://api.deepseek.com/", "api.deepseek.com"}, // 末尾斜杠
		{"api.deepseek.com", "api.deepseek.com"},          // 缺协议
		{"  https://API.DeepSeek.com  ", "api.deepseek.com"},
		{"https://openrouter.ai/api/v1/", "openrouter.ai/api/v1"},
		{"", ""},
		{"https://", ""}, // 只有协议没有主机
	} {
		if got := normalizeURL(tc.in); got != tc.want {
			t.Errorf("normalizeURL(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestHostOf(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://openrouter.ai/api/v1", "openrouter.ai"},
		{"https://api.qnaigc.com:8443/v1", "api.qnaigc.com"}, // 端口要去掉
		{"api.deepseek.com", "api.deepseek.com"},
		{"", ""},
	} {
		if got := hostOf(tc.in); got != tc.want {
			t.Errorf("hostOf(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestLookupRules 是六步阶梯在真索引上的逐个点名。
func TestLookupRules(t *testing.T) {
	md := modelsDevCatalog(t)

	for _, tc := range []struct {
		name     string
		provider string
		model    string
		rule     string
		entry    string
	}{
		{"精确", "openai", "gpt-5.4", RuleExact, "gpt-5.4"},
		{"忽略大小写", "openai", "GPT-5.4", RuleCase, "gpt-5.4"},
		{"models/ 前缀", "google", "models/gemini-2.5-flash", RuleModelsPrefix, "gemini-2.5-flash"},
		{"去掉 :free", "openai", "gpt-5.4:free", RuleSuffixStrip, "gpt-5.4"},
		{"去掉日期", "anthropic", "claude-opus-4-5-20251101", RuleDateStrip, "claude-opus-4-5"},
		{"点号换横线", "anthropic", "claude-opus-4.5", RuleDotDash, "claude-opus-4-5"},
		{"整条阶梯走完", "anthropic", "models/Claude-Opus-4.5-20251101:free", RuleDotDash, "claude-opus-4-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx, rule, ok := md.lookupInProvider(tc.provider, tc.model)
			if !ok {
				t.Fatalf("%s/%s 该命中", tc.provider, tc.model)
			}
			if rule != tc.rule {
				t.Errorf("rule = %q，期望 %q", rule, tc.rule)
			}
			if got := md.entries[idx].Model; got != tc.entry {
				t.Errorf("命中的是源里的 %q，期望 %q", got, tc.entry)
			}
		})
	}
}

// TestLookupCaseInsensitiveBothWays：大小写两个方向都要通。
//
// 这条钉的是一个真实存在的不对称：源里有一批键带大写（models.dev 把 DeepInfra
// 记作 `stepfun-ai/Step-3.7-Flash`、Poe 记作 `GPT-5.4`），而用户在 llmio 里
// 多半照小写填。反过来（用户填大写、源里是小写）一直是对的，因为 case 那一步
// 会被记进阶梯；但查询本身已是小写时那一步"没产生差异"、不会被记，
// 小写表就再也轮不到——于是唯独"源大写、查询小写"这个方向不通，
// 用户看到的是"数据源里没这个模型"，而那条记录就在他配的那个上游下面。
func TestLookupCaseInsensitiveBothWays(t *testing.T) {
	md := modelsDevCatalog(t)

	for _, tc := range []struct {
		name     string
		provider string
		model    string
		entry    string
	}{
		{"源里是小写、用户填大写", "openai", "GPT-5.4", "gpt-5.4"},
		{"源里是大写、用户填小写", "deepinfra", "step-3.7-flash", "stepfun-ai/Step-3.7-Flash"},
		{"源里是大写、用户按源里填", "deepinfra", "Step-3.7-Flash", "stepfun-ai/Step-3.7-Flash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx, rule, ok := md.lookupInProvider(tc.provider, tc.model)
			if !ok {
				t.Fatalf("%s/%s 该命中", tc.provider, tc.model)
			}
			if got := md.entries[idx].Model; got != tc.entry {
				t.Errorf("命中的是源里的 %q，期望 %q", got, tc.entry)
			}
			if rule != RuleCase && rule != RuleIDPrefix {
				t.Errorf("rule = %q，期望靠忽略大小写命中（case 或 id_prefix）", rule)
			}
		})
	}

	// 只是忽略大小写，不是忽略别的：换成完全不同的串仍然不许命中。
	if _, _, ok := md.lookupInProvider("deepinfra", "step-3-7-flash"); ok {
		t.Error("点号与短横线的互换是另一步，不该在这一条里被顺带放行")
	}
}

// TestLookupNoSimilarityMatching：归一化只做**确定性的格式换算**，
// 不做任何相似度。这几个查询在实测里都被当成过"应该能匹配上"，
// 但它们对应的是已退役或不同版本的模型，编一个最接近的结果比空着更糟。
func TestLookupNoSimilarityMatching(t *testing.T) {
	md := modelsDevCatalog(t)
	for _, model := range []string{
		"claude-3-5-haiku-latest", // 版本退役，全库都没有
		"claude-3-5-sonnet-20241022",
		"gpt-5",     // 有 gpt-5.4，但不同名
		"gpt-5.4-t", // 子串包含
		"claude-opus",
	} {
		if _, rule, ok := md.lookupInProvider("anthropic", model); ok {
			t.Errorf("%q 不该命中（命中的规则是 %q）", model, rule)
		}
		if _, rule, ok := md.lookupInProvider("openai", model); ok {
			t.Errorf("%q 不该在 openai 下命中（命中的规则是 %q）", model, rule)
		}
	}
}

// TestLookupUnknownProvider：没对齐上的上游不该命中任何东西。
// exact / lower 里没有这个键时是 nil map 查找，必须安全返回 false——
// 这不是理论问题，"类型兜底"那一步随时会喂进来一个源里不存在的 id。
func TestLookupUnknownProvider(t *testing.T) {
	md := modelsDevCatalog(t)
	if _, _, ok := md.lookupInProvider("no-such-provider", "gpt-5.4"); ok {
		t.Error("不存在的上游不该命中")
	}
	if _, _, ok := md.lookupInProvider("", "gpt-5.4"); ok {
		t.Error("空上游 id 不该命中")
	}
}

// TestCrossProviderCandidates 钉的是候选的判据：**完整归一化后同名**。
func TestCrossProviderCandidates(t *testing.T) {
	md := modelsDevCatalog(t)

	// qiniu-ai 下的 gemini-2.0-flash，排除自己之后还剩谁？
	got := md.crossProviderCandidates("gemini-2.0-flash", map[string]bool{"qiniu-ai": true})
	if len(got) != 0 {
		t.Errorf("语料里只有 qiniu-ai 有 gemini-2.0-flash，得到 %d 条候选", len(got))
	}

	// 不排除时能查到它自己。
	got = md.crossProviderCandidates("gemini-2.0-flash", nil)
	if len(got) != 1 || md.entries[got[0]].Provider != "qiniu-ai" {
		t.Fatalf("期望恰好 1 条 qiniu-ai 的候选，得到 %v", got)
	}

	// 同一个模型在多个上游下时，每个上游只出一条。
	//
	// 这里的重点是**两个形态都要查**：openrouter 的键是
	// `anthropic/claude-opus-4.5`（带厂商命名空间 + 点号），anthropic 的键是
	// 裸名 `claude-opus-4-5`。只按完整形态查会只找到 openrouter 自己，
	// 只按尾名查则会漏掉用户直接填全名的那种情况。
	got = md.crossProviderCandidates("anthropic/claude-opus-4.5", nil)
	var providers []string
	for _, i := range got {
		providers = append(providers, md.entries[i].Provider)
	}
	if !reflect.DeepEqual(providers, []string{"anthropic", "openrouter"}) {
		t.Errorf("候选上游 = %v，期望 [anthropic openrouter]", providers)
	}

	// 裸名查询得到同一批（尾名索引优先，每个上游一条）。
	got = md.crossProviderCandidates("claude-opus-4-5", nil)
	providers = nil
	for _, i := range got {
		providers = append(providers, md.entries[i].Provider)
	}
	if !reflect.DeepEqual(providers, []string{"anthropic", "openrouter"}) {
		t.Errorf("裸名查询的候选上游 = %v，期望 [anthropic openrouter]", providers)
	}
}

func TestAlignProviders(t *testing.T) {
	md := modelsDevCatalog(t)
	llm := liteLLMCatalog(t)

	for _, tc := range []struct {
		name       string
		cat        *Catalog
		style      string
		baseURL    string
		wantIDs    []string
		wantRules  []string
		reasonNote string
	}{
		{
			name: "base_url 精确命中", cat: md, style: consts.StyleOpenAI,
			baseURL:   "https://api.deepseek.com",
			wantIDs:   []string{"deepseek", "openai"},
			wantRules: []string{ProviderRuleBaseURL, ProviderRuleTypeFallback},
		},
		{
			name: "base_url 多写了路径也能按主机对上", cat: md, style: consts.StyleOpenAI,
			baseURL:   "https://api.qnaigc.com/v1/chat/completions",
			wantIDs:   []string{"qiniu-ai", "openai"},
			wantRules: []string{ProviderRuleHostAlias, ProviderRuleTypeFallback},
		},
		{
			name: "官方上游只能靠别名表（源里 api 为空）", cat: md, style: consts.StyleAnthropic,
			baseURL:   "https://api.anthropic.com",
			wantIDs:   []string{"anthropic"},
			wantRules: []string{ProviderRuleHostAlias},
		},
		{
			name: "别名表命中后与类型兜底重合，不重复", cat: md, style: consts.StyleOpenAIRes,
			baseURL:   "https://api.openai.com",
			wantIDs:   []string{"openai"},
			wantRules: []string{ProviderRuleHostAlias},
		},
		{
			name: "没有 base_url 时只剩类型兜底", cat: md, style: consts.StyleGemini,
			baseURL:   "",
			wantIDs:   []string{"google"},
			wantRules: []string{ProviderRuleTypeFallback},
		},
		{
			name: "什么线索都没有", cat: md, style: "",
			baseURL: "",
			wantIDs: nil, wantRules: nil,
		},
		{
			name: "对不上任何东西的自建上游", cat: md, style: "",
			baseURL: "https://api.example.com",
			wantIDs: nil, wantRules: nil,
		},
		{
			name: "LiteLLM 的官方上游", cat: llm, style: consts.StyleGemini,
			baseURL:   "https://generativelanguage.googleapis.com",
			wantIDs:   []string{"gemini"},
			wantRules: []string{ProviderRuleHostAlias},
		},
		{
			name: "同名上游在两个源里的 id 不同：智谱", cat: llm, style: consts.StyleOpenAI,
			baseURL:   "https://open.bigmodel.cn",
			wantIDs:   []string{"zai", "openai"},
			wantRules: []string{ProviderRuleHostAlias, ProviderRuleTypeFallback},
		},
		{
			name: "LiteLLM 没有 base_url 索引，只剩类型兜底", cat: llm, style: consts.StyleGemini,
			baseURL:   "",
			wantIDs:   []string{"gemini"},
			wantRules: []string{ProviderRuleTypeFallback},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs := alignProviders(tc.cat, tc.style, tc.baseURL)
			ids := make([]string, 0, len(refs))
			rules := make([]string, 0, len(refs))
			for _, r := range refs {
				ids = append(ids, r.ID)
				rules = append(rules, r.Rule)
			}
			if len(tc.wantIDs) == 0 {
				if len(refs) != 0 {
					t.Fatalf("期望没有候选上游，得到 %v", ids)
				}
				return
			}
			if !reflect.DeepEqual(ids, tc.wantIDs) {
				t.Errorf("候选上游 = %v，期望 %v", ids, tc.wantIDs)
			}
			if !reflect.DeepEqual(rules, tc.wantRules) {
				t.Errorf("对齐规则 = %v，期望 %v", rules, tc.wantRules)
			}
		})
	}
}

// TestPreferProvider：同一主机挂多个上游时选谁。
//
// 实测会撞上的形态是"基名 + 变体"：open.bigmodel.cn 同时挂在 zhipuai 与
// zhipuai-coding-plan 下，api.z.ai 挂在 zai 与 zai-coding-plan 下，
// ark.cn-beijing.volces.com 挂在 volcengine 与 volcengine-coding-plan 下。
// 变体的 id 总是以基名开头并且更长，所以"短优先、同长字典序"这三处都选基名。
func TestPreferProvider(t *testing.T) {
	for _, tc := range []struct {
		current, candidate string
		want               bool
	}{
		{"", "x", true},
		{"x", "", false},
		{"", "", false},
		{"zhipuai-coding-plan", "zhipuai", true},
		{"zhipuai", "zhipuai-coding-plan", false},
		{"b", "a", true},  // 同长按字典序
		{"a", "b", false}, //
		{"a", "a", false},
	} {
		if got := preferProvider(tc.current, tc.candidate); got != tc.want {
			t.Errorf("preferProvider(%q, %q) = %v，期望 %v", tc.current, tc.candidate, got, tc.want)
		}
	}
}

// TestBuildCatalogHostCollisionDeterministic：两张对齐索引都必须是确定的。
//
// 建索引时迭代的是 infos 这个 map，顺序随机。用"先写的赢"会让同一个主机 /
// 同一个 URL 上的多个上游每次启动换一个人。跑多轮才能暴露——单跑一次
// 大概率"碰巧是对的"。
func TestBuildCatalogHostCollisionDeterministic(t *testing.T) {
	infos := map[string]ProviderInfo{
		"zhipuai":             {API: "https://open.bigmodel.cn/api/paas/v4"},
		"zhipuai-coding-plan": {API: "https://open.bigmodel.cn/api/paas/v4"},
		"zai":                 {API: "https://api.z.ai/api/paas/v4"},
		"zai-coding-plan":     {API: "https://api.z.ai/api/paas/v4"},
		"volcengine":          {API: "https://ark.cn-beijing.volces.com/api/v3"},
		"volcengine-coding-plan": {
			API: "https://ark.cn-beijing.volces.com/api/v3",
		},
	}
	wantByHost := map[string]string{
		"open.bigmodel.cn":          "zhipuai",
		"api.z.ai":                  "zai",
		"ark.cn-beijing.volces.com": "volcengine",
	}
	wantByURL := map[string]string{
		"open.bigmodel.cn/api/paas/v4":     "zhipuai",
		"api.z.ai/api/paas/v4":             "zai",
		"ark.cn-beijing.volces.com/api/v3": "volcengine",
	}

	for i := 0; i < 20; i++ {
		cat := NewCatalog(SourceModelsDev, testFetchedAt, nil, infos)
		for host, want := range wantByHost {
			if got := cat.providerByHost[host]; got != want {
				t.Fatalf("第 %d 轮 providerByHost[%s] = %q，期望 %q", i+1, host, got, want)
			}
		}
		for u, want := range wantByURL {
			if got := cat.providerByURL[u]; got != want {
				t.Fatalf("第 %d 轮 providerByURL[%s] = %q，期望 %q", i+1, u, got, want)
			}
		}
	}
}

func TestAlignProvidersPrefersBaseProvider(t *testing.T) {
	// 上面那张表建出来的索引喂进对齐：智谱那台机器该对齐到 zhipuai，
	// 不是 zhipuai-coding-plan。对齐错了，模型 id 再准也会取到别家的价格。
	infos := map[string]ProviderInfo{
		"zhipuai":             {API: "https://open.bigmodel.cn/api/paas/v4"},
		"zhipuai-coding-plan": {API: "https://open.bigmodel.cn/api/paas/v4"},
	}
	cat := NewCatalog(SourceModelsDev, testFetchedAt, nil, infos)

	for i := 0; i < 20; i++ {
		refs := alignProviders(cat, consts.StyleOpenAI, "https://open.bigmodel.cn/api/paas/v4")
		if len(refs) == 0 {
			t.Fatal("该对齐上 zhipuai")
		}
		if refs[0].ID != "zhipuai" {
			t.Fatalf("第 %d 轮对齐到 %q，期望 zhipuai", i+1, refs[0].ID)
		}
		if refs[0].Rule != ProviderRuleBaseURL {
			t.Errorf("rule = %q，期望 %q", refs[0].Rule, ProviderRuleBaseURL)
		}
	}
}

// TestNpmStyle：npm 包名折协议风格，只服务跨上游候选的排序。
// 折不出来的返回空串——宁可排在后面，也不猜。
func TestNpmStyle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"@ai-sdk/anthropic", consts.StyleAnthropic},
		{"@ai-sdk/google", consts.StyleGemini},
		{"@ai-sdk/google-vertex", consts.StyleGemini},
		{"@ai-sdk/openai", consts.StyleOpenAI},
		{"@ai-sdk/openai-compatible", consts.StyleOpenAI},
		{"@openrouter/ai-sdk-provider", ""},
		{"@ai-sdk/deepinfra", ""},
		{"", ""},
	} {
		if got := npmStyle(tc.in); got != tc.want {
			t.Errorf("npmStyle(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestHostAliasForSource：同一台主机在两个源里的 id 并不一样
// （实测 138 个 litellm_provider 与 226 个 models.dev 上游只有 32 个同名）。
// 别名表查错了，等于拿另一个上游的价格往用户表单里填。
func TestHostAliasForSource(t *testing.T) {
	for _, tc := range []struct {
		host, source, want string
	}{
		{"api.anthropic.com", SourceModelsDev, "anthropic"},
		{"api.anthropic.com", SourceLiteLLM, "anthropic"},
		{"open.bigmodel.cn", SourceModelsDev, "zhipuai"},
		{"open.bigmodel.cn", SourceLiteLLM, "zai"},
		{"generativelanguage.googleapis.com", SourceModelsDev, "google"},
		{"generativelanguage.googleapis.com", SourceLiteLLM, "gemini"},
		{"dashscope.aliyuncs.com", SourceModelsDev, "alibaba-cn"},
		{"dashscope.aliyuncs.com", SourceLiteLLM, "dashscope"},
		// 这家只在 models.dev 有对应条目，LiteLLM 侧留空就该是空串，
		// 而不是硬塞一个猜的名字。
		{"api.qnaigc.com", SourceModelsDev, "qiniu-ai"},
		{"api.qnaigc.com", SourceLiteLLM, ""},
	} {
		alias, ok := hostAliasFor(tc.host)
		if !ok {
			t.Errorf("别名表里没有 %s", tc.host)
			continue
		}
		if got := alias.forSource(tc.source); got != tc.want {
			t.Errorf("%s 在 %s 下的 id = %q，期望 %q", tc.host, tc.source, got, tc.want)
		}
	}

	// 表里没有的主机返回 false。这不是失败路径而是常态：用户的每一种自建
	// 上游都会走到这里，接着由跨上游候选给出"这一家不在数据源里"的说法。
	if _, ok := hostAliasFor("no-such-host.example.com"); ok {
		t.Error("不认识的主机不该在别名表里")
	}
	if _, ok := hostAliasFor(""); ok {
		t.Error("空主机不该在别名表里")
	}

	// 不认识的来源返回空串，不回落到另一个源的值。
	alias, _ := hostAliasFor("open.bigmodel.cn")
	if got := alias.forSource("something-else"); got != "" {
		t.Errorf("未知来源得到 %q，期望空串", got)
	}
}

func TestTypeFallbackFor(t *testing.T) {
	for _, tc := range []struct {
		source, style, want string
	}{
		{SourceModelsDev, consts.StyleOpenAI, "openai"},
		{SourceModelsDev, consts.StyleOpenAIRes, "openai"},
		{SourceModelsDev, consts.StyleAnthropic, "anthropic"},
		{SourceModelsDev, consts.StyleGemini, "google"},
		{SourceLiteLLM, consts.StyleGemini, "gemini"},
		{SourceLiteLLM, consts.StyleAnthropic, "anthropic"},
		{SourceModelsDev, "", ""},
		{"unknown-source", consts.StyleOpenAI, ""},
	} {
		got, ok := typeFallbackFor(tc.source, tc.style)
		if tc.want == "" {
			if ok {
				t.Errorf("typeFallbackFor(%q, %q) 该没有结果，得到 %q", tc.source, tc.style, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("typeFallbackFor(%q, %q) = (%q, %v)，期望 %q", tc.source, tc.style, got, ok, tc.want)
		}
	}
}
