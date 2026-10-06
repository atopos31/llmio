package modelmeta

import "github.com/atopos31/llmio/consts"

// hostAlias 是一个 base_url 主机在某个来源里的上游 id。
//
// 两张 id 分开写是必须的：同一个上游在两个源里的名字并不一样——
// 智谱是 models.dev 的 `zhipuai` 与 LiteLLM 的 `zai`，Google 是 `google`
// 与 `gemini`，通义是 `alibaba-cn` 与 `dashscope`。实测 138 个
// litellm_provider 与 226 个 models.dev 上游只有 32 个同名。
type hostAlias struct {
	ModelsDev string
	LiteLLM   string
}

func (a hostAlias) forSource(source string) string {
	switch source {
	case SourceModelsDev:
		return a.ModelsDev
	case SourceLiteLLM:
		return a.LiteLLM
	default:
		return ""
	}
}

// hostAliases 是手工维护的别名表（§3.3 第 2 步）。
//
// 它要盖两类主机：
//
//  1. **官方上游**。它们在 models.dev 里的 api 字段是空的——官方 SDK 自带
//     base_url，不需要填。实测 226 家里 200 家带 api，填了值的又只有
//     @ai-sdk/openai-compatible 的那 185 家，anthropic / openai / google /
//     xai / mistral / groq 全都不在其中。
//  2. **带路径的上游**。models.dev 记的是 `https://open.bigmodel.cn/api/paas/v4`，
//     而用户在 llmio 里很可能只填主机名。按主机对齐这一步专收这种差异。
//
// 表里查不到的主机不做上游对齐，转而在别的上游里找同名模型当候选（§3.4.2）——
// 那不是"没查出来"，而是"这一家不在数据源里"，两者给用户的说法完全不同。
var hostAliases = map[string]hostAlias{
	// 官方上游（源里 api 字段为空）
	"api.anthropic.com":                 {ModelsDev: "anthropic", LiteLLM: "anthropic"},
	"api.openai.com":                    {ModelsDev: "openai", LiteLLM: "openai"},
	"generativelanguage.googleapis.com": {ModelsDev: "google", LiteLLM: "gemini"},
	"api.x.ai":                          {ModelsDev: "xai", LiteLLM: "xai"},
	"api.mistral.ai":                    {ModelsDev: "mistral", LiteLLM: "mistral"},
	"api.groq.com":                      {ModelsDev: "groq", LiteLLM: "groq"},
	"api.together.xyz":                  {ModelsDev: "togetherai", LiteLLM: "together_ai"},
	"api.deepinfra.com":                 {ModelsDev: "deepinfra", LiteLLM: "deepinfra"},
	"api.cerebras.ai":                   {ModelsDev: "cerebras", LiteLLM: "cerebras"},
	"api.minimax.chat":                  {ModelsDev: "minimax", LiteLLM: "minimax"},
	"api.minimaxi.com":                  {ModelsDev: "minimax", LiteLLM: "minimax"},
	"api.perplexity.ai":                 {ModelsDev: "perplexity", LiteLLM: "perplexity"},
	"api.sambanova.ai":                  {LiteLLM: "sambanova"},

	// 有 base_url 但用户在 llmio 里常常只填主机名的那批（国内上游为主）
	"api.deepseek.com":          {ModelsDev: "deepseek", LiteLLM: "deepseek"},
	"open.bigmodel.cn":          {ModelsDev: "zhipuai", LiteLLM: "zai"},
	"api.z.ai":                  {ModelsDev: "zai", LiteLLM: "zai"},
	"api.moonshot.cn":           {ModelsDev: "moonshotai-cn", LiteLLM: "moonshot"},
	"api.moonshot.ai":           {ModelsDev: "moonshotai", LiteLLM: "moonshot"},
	"dashscope.aliyuncs.com":    {ModelsDev: "alibaba-cn", LiteLLM: "dashscope"},
	"api.siliconflow.cn":        {ModelsDev: "siliconflow-cn"},
	"api.qnaigc.com":            {ModelsDev: "qiniu-ai"},
	"ark.cn-beijing.volces.com": {ModelsDev: "volcengine", LiteLLM: "volcengine"},
	"openrouter.ai":             {ModelsDev: "openrouter", LiteLLM: "openrouter"},
	"api.fireworks.ai":          {ModelsDev: "fireworks-ai", LiteLLM: "fireworks_ai"},
	"api.novita.ai":             {ModelsDev: "novita-ai", LiteLLM: "novita"},
	"api.inceptionlabs.ai":      {ModelsDev: "inception", LiteLLM: "inception"},
	"api.morphllm.com":          {ModelsDev: "morph", LiteLLM: "morph"},
	"api.scaleway.ai":           {ModelsDev: "scaleway", LiteLLM: "scaleway"},
}

func hostAliasFor(host string) (hostAlias, bool) {
	a, ok := hostAliases[host]
	return a, ok
}

// typeFallback 是按 llmio 的协议类型兜底时对应的源内上游 id（§3.3 第 3 步）。
//
// 这一步最容易误配——**任何** OpenAI 兼容上游都会被判成 openai——所以它排在
// 候选序列的最后，并且只有在"这个上游里确实找到了这个模型"时才会被选中。
var typeFallback = map[string]map[string]string{
	SourceModelsDev: {
		consts.StyleOpenAI:    "openai",
		consts.StyleOpenAIRes: "openai",
		consts.StyleAnthropic: "anthropic",
		consts.StyleGemini:    "google",
	},
	SourceLiteLLM: {
		consts.StyleOpenAI:    "openai",
		consts.StyleOpenAIRes: "openai",
		consts.StyleAnthropic: "anthropic",
		consts.StyleGemini:    "gemini",
	},
}

func typeFallbackFor(source, providerType string) (string, bool) {
	id, ok := typeFallback[source][providerType]
	return id, ok
}

// npmStyle 把 models.dev 的 npm 包名折成 llmio 的协议风格。
//
// 它只服务一件事：跨上游候选的排序（§4.1.1「按提供商是否与当前上游同 Type
// 排序——同协议的在前面，因为转售通常同协议」）。折不出来就返回空串，
// 那种候选排在后面，不做任何猜测。
func npmStyle(npm string) string {
	switch npm {
	case "@ai-sdk/anthropic":
		return consts.StyleAnthropic
	case "@ai-sdk/google", "@ai-sdk/google-vertex":
		return consts.StyleGemini
	case "@ai-sdk/openai", "@ai-sdk/openai-compatible":
		return consts.StyleOpenAI
	default:
		return ""
	}
}
