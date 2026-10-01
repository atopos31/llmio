package bridge

import "encoding/json"

// OpenAI Chat Completions 协议的类型。
//
// 只声明**转换要用到**的字段：没声明到的字段反序列化时被忽略，这正是我们要的——
// 转换不是校验，客户端多带一个自家扩展字段不该让请求失败。真正需要"看见了就不能
// 装看不见"的字段（见 request.go 的 rejectUnsupportedOpenAIFields）另行显式声明。

// OpenAIRequest 是一份 /v1/chat/completions 请求体。
type OpenAIRequest struct {
	Model               string                `json:"model"`
	Messages            []OpenAIMessage       `json:"messages"`
	MaxTokens           *int                  `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                  `json:"max_completion_tokens,omitempty"`
	Temperature         *float64              `json:"temperature,omitempty"`
	TopP                *float64              `json:"top_p,omitempty"`
	Stop                json.RawMessage       `json:"stop,omitempty"`
	Stream              bool                  `json:"stream,omitempty"`
	StreamOptions       json.RawMessage       `json:"stream_options,omitempty"`
	Tools               []OpenAITool          `json:"tools,omitempty"`
	ToolChoice          json.RawMessage       `json:"tool_choice,omitempty"`
	ResponseFormat      *OpenAIResponseFormat `json:"response_format,omitempty"`
	N                   *int                  `json:"n,omitempty"`
	PresencePenalty     *float64              `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64              `json:"frequency_penalty,omitempty"`
	Logprobs            *bool                 `json:"logprobs,omitempty"`
	TopLogprobs         *int                  `json:"top_logprobs,omitempty"`
	Seed                *int                  `json:"seed,omitempty"`
	User                string                `json:"user,omitempty"`
}

// OpenAIMessage 一条消息。Content 是"字符串、块数组或 null"三选一，故用 RawMessage，
// 由 openAIContent 解析。
type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content,omitempty"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

// OpenAIContentPart 是 content 数组里的一块。type 取 text / image_url。
type OpenAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *OpenAIImageURL `json:"image_url,omitempty"`
}

// OpenAIImageURL 图片块。URL 既可以是 https 链接，也可以是 data URL。
type OpenAIImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// OpenAIToolCall 一次工具调用。Arguments 是 JSON **字符串**（不是对象），这是它与
// Anthropic 的 tool_use.input 最关键的一处形状差异。
type OpenAIToolCall struct {
	Index    *int               `json:"index,omitempty"`
	ID       string             `json:"id"`
	Type     string             `json:"type,omitempty"`
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall 工具调用的函数名与参数串。
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OpenAITool 一份可用的工具声明。
type OpenAITool struct {
	Type     string            `json:"type"`
	Function OpenAIFunctionDef `json:"function"`
}

// OpenAIFunctionDef 工具的函数部分。Parameters 是 JSON Schema，转换只搬运不解析。
type OpenAIFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// OpenAIResponseFormat 期望的输出格式。只有 anthropic 装不下它的 type 才会被读到。
type OpenAIResponseFormat struct {
	Type string `json:"type"`
}
