package service

import (
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 请求体参数自愈：上游因请求体里含有它不认识的参数而报 400/422 时，把被点名
// 的参数从实发请求体里删掉、同一家上游立即重发。判据与控制流见
// docs/plans/usage-insights-plan.md §3.4/§3.5；本文件只放纯函数，接线在 chat.go。

// SelfHealMaxDrops 一家上游最多删几个参数（多发自几次请求）。绝大多数请求至多
// 带一两个非标参数；预算同时是防死循环的硬闸。
const SelfHealMaxDrops = 3

// SelfHealNotePrefix 自愈记账短码前缀，后接参数路径（self_heal_dropped:top_k）。
// 经 BridgeNotes 落到 chat_logs.bridge_notes，日志详情抽屉既有通道直接展示。
const SelfHealNotePrefix = "self_heal_dropped:"

// healableStatusCodes 只有这两个状态码可能是"参数不认识"：OpenAI/Anthropic/
// vLLM 用 400，FastAPI 系网关默认 422。401/402/404/429/5xx 与参数存在性无关，
// 一律走既有换家路径。
var healableStatusCodes = map[int]bool{400: true, 422: true}

// paramComplaintPatterns 短语类模式表，捕获组 1 = 参数路径。
// 数据不是逻辑：新上游措辞 = 加一行正则 + 一条表驱动用例
// （docs/plans/usage-insights-plan.md §3.9 的校验循环）。
var paramComplaintPatterns = []*regexp.Regexp{
	// OpenAI 官方：Unknown parameter: 'top_k'.
	regexp.MustCompile(`(?i)unknown parameter:?\s*'([\w.\-]+)'`),
	// OpenAI 兼容第三方（vLLM/OpenRouter 一类）：Unrecognized request argument supplied: logit_bias
	regexp.MustCompile(`(?i)unrecognized request arguments? (?:supplied|provided):?\s*'?([\w.\-]+)'?`),
	// Pydantic 系网关（前缀式）：messages.0.meta: Extra inputs are not permitted
	regexp.MustCompile(`(?i)'?([\w.\-]+)'?\s*:\s*extra inputs are not permitted`),
	// Pydantic 系网关（括号式）：Extra inputs are not permitted (path: top_k)
	regexp.MustCompile(`(?i)extra inputs are not permitted[^\n]*path[=:]\s*'?([\w.\-]+)'?`),
	// Anthropic：Unsupported parameter: 'metadata'
	regexp.MustCompile(`(?i)unsupported parameter:?\s*'([\w.\-]+)'`),
	// Anthropic：top_k is not supported by this model / api
	regexp.MustCompile(`(?i)([\w.\-]+) is not supported by this (?:model|api)`),
	// Gemini：Unknown name "top_k" at 'input'
	regexp.MustCompile(`(?i)unknown name "([\w.\-]+)"`),
	// 通用：field 'foo' is not supported / parameter 'foo' is not supported
	regexp.MustCompile(`(?i)(?:field|parameter) '([\w.\-]+)' is not supported`),
}

// protectedFields 路径**恰好等于**其中之一时不自愈：整删消息流或改流式开关
// 从来不是"自愈"。嵌套子路径（messages.0.metadata）允许删——删的是消息里的
// 一个非标准子字段，消息本体保留。
var protectedFields = map[string]bool{
	"model":    true,
	"messages": true,
	"stream":   true,
	"tools":    true,
	"system":   true,
	"contents": true,
	"input":    true,
}

var dropPathShape = regexp.MustCompile(`^[A-Za-z_][\w.\-]*$`)

// DetectParamComplaint 判定一条上游错误是否点名了"我们不认识的参数"。
// 返回参数路径（JSON 点路径，支持 messages.0.foo 的数组下标写法）。
func DetectParamComplaint(status int, body []byte) (string, bool) {
	if !healableStatusCodes[status] {
		return "", false
	}
	text := complaintText(body)
	for _, re := range paramComplaintPatterns {
		if m := re.FindStringSubmatch(text); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// complaintText 从错误体提取报错文本。三种信封逐个试（message 可能是数组——
// Anthropic 校验器风格，逐项拼接）；都不是 JSON 就按原文匹配，桩上游与真实
// 网关都见过非 JSON 的 400 报文。
//
//	OpenAI:    {"error":{"message":"..."}}
//	Anthropic: {"type":"error","error":{"message":"..."}}
//	Gemini:    {"error":{"message":"...","status":"INVALID_ARGUMENT"}}
func complaintText(body []byte) string {
	for _, key := range []string{"error.message", "message"} {
		v := gjson.GetBytes(body, key)
		if !v.Exists() {
			continue
		}
		switch {
		case v.Type == gjson.String:
			return v.String()
		case v.IsArray():
			var parts []string
			for _, item := range v.Array() {
				parts = append(parts, item.String())
			}
			return strings.Join(parts, "\n")
		}
	}
	if e := gjson.GetBytes(body, "error"); e.Type == gjson.String {
		return e.String() // {"error":"text"} 的简写信封
	}
	return string(body)
}

// PlanDrop 四道闸，全过才允许删。sent 是**实发上游的字节**——上游点名的参数
// 若不在里面，说明它抱怨的是别的东西（形状、嵌套），删无可删，判据必须拒。
// 顺序刻意先廉后贵：形状/清单/去重都是 O(1)，存在性才碰 JSON。
func PlanDrop(sent []byte, path string, dropped map[string]bool) bool {
	if len(path) == 0 || len(path) > 128 || !dropPathShape.MatchString(path) {
		return false
	}
	if protectedFields[path] || dropped[path] {
		return false
	}
	return gjson.GetBytes(sent, path).Exists()
}

// ApplyDrop 从实发请求体删掉指定路径并返回新字节（sjson 点路径原生支持数组
// 下标）。路径已在 PlanDrop 验过，删除失败理论上不可达；真发生时保守返回
// 原样，由调用方的下一轮请求自然失败，不做半截改写。
func ApplyDrop(raw []byte, path string) []byte {
	out, err := sjson.DeleteBytes(raw, path)
	if err != nil {
		return raw
	}
	return out
}
