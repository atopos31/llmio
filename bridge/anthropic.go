package bridge

import "encoding/json"

// Anthropic Messages 协议的类型。声明范围与 openai.go 同理：只声明转换要用到的字段。

// AnthropicRequest 是一份 /v1/messages 请求体。
//
// System 在线上既可以是字符串、也可以是块数组（带 cache_control 的那种），故用
// RawMessage，由 anthropicSystemText 取文本。
type AnthropicRequest struct {
	Model         string               `json:"model"`
	MaxTokens     int                  `json:"max_tokens"`
	System        json.RawMessage      `json:"system,omitempty"`
	Messages      []AnthropicMessage   `json:"messages"`
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	TopK          *int                 `json:"top_k,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	Tools         []AnthropicTool      `json:"tools,omitempty"`
	ToolChoice    *AnthropicToolChoice `json:"tool_choice,omitempty"`
	Thinking      json.RawMessage      `json:"thinking,omitempty"`
	Metadata      json.RawMessage      `json:"metadata,omitempty"`
}

// AnthropicMessage 一条消息。Content 是块数组。
type AnthropicMessage struct {
	Role    string           `json:"role"`
	Content []AnthropicBlock `json:"content"`
}

// AnthropicBlock 一块内容。一个结构体承载全部块类型（比 Go 的 JSON 多态省事）：
// 各字段按 Type 取用，其余留空并由 omitempty 省掉。
//
// Input 用 RawMessage：tool_use 的 input 在 Anthropic 侧是对象，转到 OpenAI 时正好
// 就是 arguments 那个字符串，不必来回反序列化。指针语义因此由 nil 承担——**空对象
// 与"没有这个字段"是两回事**，用 map 加 omitempty 会把 `{}` 省掉，而 tool_use 少了
// input 上游会直接报错。
type AnthropicBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image
	Source *AnthropicImageSource `json:"source,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result：Content 既可以是字符串、也可以是块数组
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   *bool           `json:"is_error,omitempty"`

	// thinking / redacted_thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

// AnthropicImageSource 图片的来源。Type 取 base64（带 MediaType 与 Data）或 url。
type AnthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// AnthropicTool 一份可用的工具声明。InputSchema 是 JSON Schema，只搬运不解析。
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// AnthropicToolChoice 工具选择。Type 取 auto / any / tool（tool 时带 Name）。
type AnthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// 内容块类型常量。散落的字符串字面量最容易拼错，集中在这里。
const (
	blockText       = "text"
	blockImage      = "image"
	blockToolUse    = "tool_use"
	blockToolResult = "tool_result"
	blockThinking   = "thinking"
	blockRedacted   = "redacted_thinking"
)
