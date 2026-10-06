package modelmeta

import (
	"net/url"
	"strings"
)

// 模型 id 归一化的规则名，也就是响应里的 match_rule。
//
// 前六条是方案 §3.4.1 钉住的六步，按代价递增、命中即停。第七条是**索引侧
// 别名**，不是对查询做的变换：LiteLLM 的键带 `<上游>/` 命名空间（zai 的 16 条
// 全是 `zai/glm-5` 这种形态），而用户在 llmio 里填的是上游自己的名字 `glm-5`。
// 它同样是纯格式换算、不涉及语义猜测，所以与六步同属"确定性"档。
//
// 第八条 cross_provider 不是"匹配上了"，而是"没本家匹配、只有别家同名"，
// 它出现时**不返回可填写值**（§3.4.2）。
const (
	RuleExact         = "exact"
	RuleCase          = "case"
	RuleModelsPrefix  = "models_prefix"
	RuleSuffixStrip   = "suffix_strip"
	RuleDateStrip     = "date_strip"
	RuleDotDash       = "dot_dash"
	RuleIDPrefix      = "id_prefix"
	RuleCrossProvider = "cross_provider"
)

// 上游对齐的规则名，也就是响应里的 provider_match_rule。
//
// 它必须回给前端（§3.3 末尾）：对齐错了，模型 id 再准也会取到别家的价格，
// 用户得看得见"这次是按哪个上游查的、凭什么这么对齐的"。
const (
	// ProviderRuleBaseURL 是按 base_url 精确命中 models.dev 的 api 字段。
	ProviderRuleBaseURL = "base_url"
	// ProviderRuleHostAlias 是按主机名命中（含手工别名表）。
	ProviderRuleHostAlias = "host_alias"
	// ProviderRuleTypeFallback 是按 llmio 的协议类型兜底。最容易误配——
	// 任何 OpenAI 兼容上游都会被判成 openai——所以它只在模型 id 也命中时
	// 才被采用（§3.3 第 3 条）。
	ProviderRuleTypeFallback = "type_fallback"
)

// 归一化只认得这两个后缀。计划里点名的是 :free / :batch；
// 其余带冒号的形态（如 :thinking）在源里是**另一个模型**，不是同一个模型的
// 变体，去掉冒号会把它们混成一谈。
var knownSuffixes = []string{":free", ":batch"}

// normalizeForm 是一步归一化的结果。
type normalizeForm struct {
	value string
	rule  string
}

// normalizeForms 返回按代价递增的归一化形态：第一步是原样，其后每一步都在
// 前一步的结果上再动一刀，且**不重复记录没有产生变化的步**。
//
// 不重复记录是刻意的：查询已经是小写时报 case 就是在编一个没发生过的变换。
// 报出来的 rule 因此是"第一个真正造成差异的步"。
func normalizeForms(model string) []normalizeForm {
	forms := []normalizeForm{{value: model, rule: RuleExact}}

	appendStep := func(next, rule string) {
		if next == "" || next == forms[len(forms)-1].value {
			return
		}
		forms = append(forms, normalizeForm{value: next, rule: rule})
	}

	appendStep(lowerID(model), RuleCase)
	appendStep(strings.TrimPrefix(forms[len(forms)-1].value, "models/"), RuleModelsPrefix)
	appendStep(trimKnownSuffix(forms[len(forms)-1].value), RuleSuffixStrip)
	appendStep(trimDateSuffix(forms[len(forms)-1].value), RuleDateStrip)
	appendStep(swapDotDash(forms[len(forms)-1].value), RuleDotDash)
	return forms
}

func lowerID(s string) string { return strings.ToLower(s) }

// trimKnownSuffix 去掉结尾的 :free / :batch。大小写不敏感，
// 因为它在不同源里的写法并不统一。
func trimKnownSuffix(s string) string {
	lower := strings.ToLower(s)
	for _, suffix := range knownSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return s[:len(s)-len(suffix)]
		}
	}
	return s
}

// trimDateSuffix 去掉结尾的 -YYYYMMDD。
//
// 只看"最后一节是 8 个数字"，不校验它是不是一个真实日期：源里出现过的形态
// 就是 -20250929 这种，多一层日历校验只会多一处可能与源不一致的地方。
func trimDateSuffix(s string) string {
	i := strings.LastIndex(s, "-")
	if i <= 0 {
		return s
	}
	tail := s[i+1:]
	if len(tail) != 8 {
		return s
	}
	for _, r := range tail {
		if r < '0' || r > '9' {
			return s
		}
	}
	return s[:i]
}

// swapDotDash 把点号换成短横线。
//
// 只做一个方向。反方向（短横线换点号）没有确定答案——`claude-opus-4-5` 里的
// 三个短横线哪个该变成点？而实测到的差异是单向的：OpenRouter 那一系用点号
// （anthropic/claude-opus-4.5），Anthropic 官方用短横线（claude-opus-4-5）。
func swapDotDash(s string) string { return strings.ReplaceAll(s, ".", "-") }

// pathTail 取路径尾名：`zai/glm-5` -> `glm-5`。没有斜杠时原样返回。
func pathTail(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// canonicalID 是"完整归一化"：把能确定应用的变换全部应用一遍。它只用于
// 跨上游候选的比对——那里要求"同名"，所以两边都得压到同一个形状，而且
// **不做任何相似度**（§3.4.3：候选必须同名才呈现，不同名就是猜测）。
func canonicalID(s string) string {
	v := trimKnownSuffix(lowerID(strings.TrimPrefix(s, "models/")))
	return swapDotDash(trimDateSuffix(v))
}

// normalizeURL 把 base_url 压成 `host[:port]/path` 的形状用于精确比对。
// 缺协议时补 https，末尾斜杠去掉——这两处差异在用户手填的配置里太常见，
// 为它判"没对齐"不值当。
func normalizeURL(raw string) string {
	host, path, ok := splitURL(raw)
	if !ok {
		return ""
	}
	return host + path
}

// hostOf 只取主机名（不含端口），用于按主机对齐。
func hostOf(raw string) string {
	host, _, ok := splitURL(raw)
	if !ok {
		return ""
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}

func splitURL(raw string) (host, path string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	return strings.ToLower(u.Host), strings.TrimRight(u.Path, "/"), true
}

// providerRef 是一个候选上游。
type providerRef struct {
	ID   string
	Rule string
}

// alignProviders 按可信度递减的顺序给出候选上游（§3.3）。
//
// 顺序即优先级：调用方**依次**在候选里找模型，第一个命中的就是结论。
// 类型兜底排在最后，而且它天然只在模型 id 也命中时才被选中——
// 因为它也是靠"在这个上游里找到了这个模型"才成立的。
func alignProviders(cat *Catalog, providerType, baseURL string) []providerRef {
	var refs []providerRef
	seen := make(map[string]bool, 4)
	add := func(id, rule string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		refs = append(refs, providerRef{ID: id, Rule: rule})
	}

	// 1. base_url 精确匹配源的 api 字段。
	if id, ok := cat.providerByURL[normalizeURL(baseURL)]; ok {
		add(id, ProviderRuleBaseURL)
	}
	// 2. base_url 主机名匹配源自己带的 api 字段。用户多写一段 /v1 是常态，
	//    这一步专门收这种差异。
	if id, ok := cat.providerByHost[hostOf(baseURL)]; ok {
		add(id, ProviderRuleHostAlias)
	}
	// 3. 手工别名表。官方上游在源里的 api 字段是空的（226 家里 200 家带 api，
	//    且只有 @ai-sdk/openai-compatible 的那 185 家填了值），所以 anthropic /
	//    openai / google 这几家只能靠这张表。
	if alias, ok := hostAliasFor(hostOf(baseURL)); ok {
		add(alias.forSource(cat.Source), ProviderRuleHostAlias)
	}
	// 4. 按协议类型兜底。
	if id, ok := typeFallbackFor(cat.Source, providerType); ok {
		add(id, ProviderRuleTypeFallback)
	}
	return refs
}

// lookupInProvider 在一个已对齐的上游里按六步找模型。
//
// 返回 entries 下标、命中的规则名。第一步（exact）也查一次小写表作为兜底，
// 其余各步都只查小写表——从 case 那一步开始的形态都是小写的（normalizeForms
// 保证）。为什么第一步也要兜底，见下面那段注释。
func (c *Catalog) lookupInProvider(providerID, model string) (int, string, bool) {
	byProviderExact := c.exact[providerID]
	byProviderLower := c.lower[providerID]
	for _, f := range normalizeForms(model) {
		var hit indexHit
		var ok bool
		rule := f.rule
		if f.rule == RuleExact {
			hit, ok = byProviderExact[f.value]
			if !ok {
				// 第一步也要能反着来：源里有一批键带大写（实测 models.dev 把
				// DeepInfra 记作 `stepfun-ai/Step-3.7-Flash`、Poe 记作 `GPT-5.4`），
				// 而用户多半照小写填。查询本身已是小写时，case 那一步因为"没产生
				// 差异"不会被记进阶梯，小写表就永远轮不到——于是"源大写、查询小写"
				// 这个方向反而不通，恰好与 case 那一步存在的意义相反。
				//
				// 报 case 不是编造：这次命中确实只有忽略大小写才成立。写成 exact
				// 才是谎话——它会让用户以为源里存的就是他填的那个串。
				hit, ok = byProviderLower[f.value]
				if ok {
					rule = RuleCase
				}
			}
		} else {
			hit, ok = byProviderLower[f.value]
		}
		if !ok {
			continue
		}
		if hit.aliased {
			return hit.idx, RuleIDPrefix, true
		}
		return hit.idx, rule, true
	}
	return 0, "", false
}

// findInProviders 依次在候选上游里找，返回第一个命中的结论。
func (c *Catalog) findInProviders(refs []providerRef, model string) (int, providerRef, string, bool) {
	for _, ref := range refs {
		if idx, rule, ok := c.lookupInProvider(ref.ID, model); ok {
			return idx, ref, rule, true
		}
	}
	return 0, providerRef{}, "", false
}

// crossProviderCandidates 找**同名**的跨上游条目（§3.4.2）。
//
// 判据只有一条：完整归一化后字符串相等。没有子串包含、没有编辑距离、
// 没有按相似度取 top-1——实测到的真实坑是版本退役（claude-3-5-haiku-latest
// 和 claude-3-5-sonnet-20241022 在 models.dev 全库都不存在），此时任何
// "最相似"的结果都是编出来的，比空着更糟（§3.4.3）。
//
// 键查两个形态，尾名在前。源里有一批键带 `<厂商>/` 命名空间（OpenRouter 的
// `anthropic/claude-opus-4.5`、DeepInfra 的 `stepfun-ai/Step-3.7-Flash`），
// 建索引时这类条目在两个形态下都留了格，查询侧就得对称地在两个形态下都查。
// 只查一个的后果很具体：用户填 `anthropic/claude-opus-4.5` 时能查到
// OpenRouter 自己，却查不到它真正对应的 anthropic 那条——而把这两条连起来
// 恰恰是这个功能要做的事。
//
// 尾名在前是因为它就是用户最常填的那个形态，也是每个条目都有格的那个索引。
func (c *Catalog) crossProviderCandidates(model string, exclude map[string]bool) []int {
	tail := canonicalID(pathTail(model))
	keys := []string{tail}
	if full := canonicalID(model); full != tail {
		keys = append(keys, full)
	}

	out := make([]int, 0, 8)
	// 两条键会指向同一批下标（带命名空间的条目两边都有格），要去重。
	seenIdx := make(map[int]bool, 8)
	seenProvider := make(map[string]bool, 8)
	for _, key := range keys {
		for _, i := range c.byModel[key] {
			if seenIdx[i] {
				continue
			}
			seenIdx[i] = true
			p := c.entries[i].Provider
			if exclude[p] || seenProvider[p] {
				continue
			}
			seenProvider[p] = true
			out = append(out, i)
		}
	}
	return out
}
