package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"

	"github.com/atopos31/llmio/models"
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

// DefaultSelfHealPolicy 默认开启：自愈替代的是"整单失败"，不是某个正常工作的
// 功能；且每次删参都有记账与失败行可查，不是静默改写。
func DefaultSelfHealPolicy() *models.SelfHealPolicy {
	return &models.SelfHealPolicy{Enabled: true, MaxDrops: SelfHealMaxDrops}
}

// GetSelfHealPolicy 读自愈策略。缺行、空值、坏 JSON 一律回默认继续服务——
// 一个装饰性配置读坏了不该让请求跟着失败。每请求一次单行 KV 点查，与转发
// 路径上既有的关联表读同量级，v1 不做缓存。
func GetSelfHealPolicy(ctx context.Context) *models.SelfHealPolicy {
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeySelfHealPolicy).First(ctx)
	if err != nil || strings.TrimSpace(config.Value) == "" {
		return DefaultSelfHealPolicy()
	}
	policy := DefaultSelfHealPolicy()
	if err := json.Unmarshal([]byte(config.Value), policy); err != nil {
		return DefaultSelfHealPolicy()
	}
	return policy
}

// toolContentArrayPattern 识别"上游不收 tool 消息的块数组 content"这类 400。
// 它是**类型错**，按 §3.4 不进通用删参（正确动作不是删参数而是压形状），
// 单独一条协议感知的降级规则处理。
var toolContentArrayPattern = regexp.MustCompile(`(?i)invalid type for 'content': expected a string`)

// ToolContentDowngradeNote 工具结果图片被降级丢弃的记账短码。
const ToolContentDowngradeNote = "dropped_tool_result_image"

// FlattenToolContent 把 rawBody 里所有 role:"tool" 且 content 为块数组的消息
// 压回纯文本字符串（丢 image 块、保 text 块）。返回 (新 body, 丢弃的图片块数)；
// 没有可压的目标时返回 (原 body, -1)。幂等：再跑一遍找不到目标，原样返回。
func FlattenToolContent(raw []byte) ([]byte, int) {
	msgs := gjson.GetBytes(raw, "messages")
	if !msgs.IsArray() {
		return raw, -1
	}
	out := raw
	dropped := 0
	changed := false
	i := 0
	msgs.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() == "tool" && msg.Get("content").IsArray() {
			var texts []string
			msg.Get("content").ForEach(func(_, part gjson.Result) bool {
				if part.Get("type").String() == "image_url" {
					dropped++
					return true
				}
				if part.Get("type").String() == "text" || part.Get("type").String() == "input_text" {
					texts = append(texts, part.Get("text").String())
				}
				return true
			})
			next, err := sjson.SetBytes(out, fmt.Sprintf("messages.%d.content", i), strings.Join(texts, ""))
			if err != nil {
				return true // 单条失败不动整份 body，保守继续
			}
			out = next
			changed = true
		}
		i++
		return true
	})
	if !changed {
		return raw, -1
	}
	return out, dropped
}
