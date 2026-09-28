package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/pkg/token"
	"github.com/tidwall/gjson"
)

// sessionKeyPrefix 派生会话键的统一前缀，便于在日志与上游侧识别来源。
const sessionKeyPrefix = "llmio-"

// sessionHeaderKeys 会话线索请求头，按优先级排列。
// http.Header.Get 对键大小写不敏感，因此 X-Session-Id 等写法同样命中。
var sessionHeaderKeys = []string{"x-opencode-session", "x-session-id", "session_id"}

// placeholderRe 匹配自定义请求头值中的 {{占位符}}。
var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// randomChars 可替换的随机串生成函数，便于测试覆盖随机源异常分支。
var randomChars = token.GenerateRandomChars

// HeaderVars 提供自定义请求头模板（{{...}}）的求值来源。
type HeaderVars struct {
	Session       string // {{session}}    会话键，由 ResolveSessionKey 派生
	SessionID     string // {{session_id}} 仅请求体中显式传入的 session_id
	Model         string // {{model}}      客户端请求的模型名
	ProviderModel string // {{provider_model}} 上游模型名
	TraceID       string // {{trace_id}}   LLMIO 本次请求的 traceID
	AuthKeyID     uint   // {{auth_key_id}} 调用方 AuthKey ID
}

// ResolveSessionKey 按优先级派生会话键，并保证返回值非空：
//
//	请求体 session_id → 入站会话请求头 → 对话根哈希 → 随机 ID
//
// 部分上游（如 opencode）强制要求会话请求头，缺失或为空都会直接返回 400，
// 因此最后一级兜底必须给出有效值。
//
// 同一对话的各轮请求应得到相同的值：前两级由客户端保证，第三级以对话的
// 首条 user 消息为指纹计算哈希，使客户端零配合时依然保持会话内稳定。
func ResolveSessionKey(header http.Header, sessionID, model string, authKeyID uint, raw []byte) string {
	// 1. 请求体中显式声明的会话（最权威，客户端可通过 extra_body 传入）
	if sessionID != "" {
		return sessionID
	}

	// 2. 入站请求头中的会话线索（客户端已自带同名头时直接沿用）
	if header != nil {
		for _, key := range sessionHeaderKeys {
			if value := strings.TrimSpace(header.Get(key)); value != "" {
				return value
			}
		}
	}

	// 3. 对话根哈希：以首条 user 消息为对话指纹，客户端零配合也能获得稳定值
	if root := conversationRoot(raw); root != "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s", authKeyID, model, root)))
		return sessionKeyPrefix + hex.EncodeToString(sum[:8])
	}

	// 4. 兜底：保证非空，避免强制会话头的上游拒绝请求
	return newRandomID()
}

// newRandomID 生成带前缀的随机会话键。随机源异常时退化为时间戳，仍然保证非空。
func newRandomID() string {
	chars, err := randomChars(16)
	if err != nil {
		return sessionKeyPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return sessionKeyPrefix + chars
}

// conversationRoot 尽力从请求体中提取"对话根"文本，作为会话指纹。
// 依次尝试四种协议的载荷结构，取首条 user 消息的文本内容；无法提取时返回空串。
func conversationRoot(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}

	// openai / anthropic: messages[].content（content 可以是字符串或 content block 数组）
	if root := firstUserMessageText(gjson.GetBytes(raw, "messages"), "content"); root != "" {
		return root
	}

	// openai-res: input[]，也可能直接是字符串
	input := gjson.GetBytes(raw, "input")
	if input.Type == gjson.String {
		if text := strings.TrimSpace(input.String()); text != "" {
			return text
		}
	}
	if root := firstUserMessageText(input, "content"); root != "" {
		return root
	}

	// gemini: contents[].parts[].text（模型侧角色为 model，用户侧为 user 或省略）
	contents := gjson.GetBytes(raw, "contents")
	if contents.IsArray() {
		for _, content := range contents.Array() {
			role := content.Get("role").String()
			if role != "" && role != "user" {
				continue
			}
			if text := contentText(content.Get("parts")); text != "" {
				return text
			}
		}
	}

	return ""
}

// firstUserMessageText 遍历消息数组，返回首条 user 消息在 contentField 上的文本。
// role 缺省时视为 user，以兼容部分客户端省略角色字段的写法。
func firstUserMessageText(messages gjson.Result, contentField string) string {
	if !messages.IsArray() {
		return ""
	}
	for _, message := range messages.Array() {
		role := message.Get("role").String()
		if role != "" && role != "user" {
			continue
		}
		content := message.Get(contentField)
		if !content.Exists() {
			continue
		}
		if text := contentText(content); text != "" {
			return text
		}
	}
	return ""
}

// contentText 把 content 归一化为纯文本：
// 字符串原样返回；content block 数组则拼接其中的 text / input_text 字段。
func contentText(content gjson.Result) string {
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String())
	}
	if content.IsArray() {
		var builder strings.Builder
		for _, block := range content.Array() {
			text := block.Get("text").String()
			if text == "" {
				text = block.Get("input_text").String()
			}
			builder.WriteString(text)
		}
		return strings.TrimSpace(builder.String())
	}
	return ""
}

// renderHeaderValue 对自定义请求头的值做模板求值。
//
//   - 不含 "{{" 的值按字面量原样返回，保证既有配置行为不变；
//   - 未识别的占位符原样保留，便于暴露拼写错误而非静默丢值；
//   - 识别到占位符但求值结果为空时回落到随机值，避免上游因空值拒绝请求。
func renderHeaderValue(value string, vars HeaderVars) string {
	if !strings.Contains(value, "{{") {
		return value
	}

	recognized := false
	rendered := placeholderRe.ReplaceAllStringFunc(value, func(match string) string {
		name := strings.TrimSpace(match[2 : len(match)-2])
		replacement, ok := placeholderValue(name, vars)
		if !ok {
			return match
		}
		recognized = true
		return replacement
	})

	if recognized && strings.TrimSpace(rendered) == "" {
		return newRandomID()
	}
	return rendered
}

// placeholderValue 解析单个占位符。第二个返回值表示是否为已识别的占位符。
func placeholderValue(name string, vars HeaderVars) (string, bool) {
	switch name {
	case "session":
		return vars.Session, true
	case "session_id":
		return vars.SessionID, true
	case "model":
		return vars.Model, true
	case "provider_model":
		return vars.ProviderModel, true
	case "trace_id":
		return vars.TraceID, true
	case "auth_key_id":
		return strconv.FormatUint(uint64(vars.AuthKeyID), 10), true
	case "uuid":
		return newRandomID(), true
	}
	return "", false
}
