package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 响应体的互转（非流式）。流式见 stream.go。
//
// 与请求侧最要紧的一处不同：**响应已经生成出来了，没有重试的机会**。请求侧遇到
// "表达不了"可以拒绝、让路由器换一家；响应侧只能"尽量把话说全"，做不到的部分记
// Note 并留下痕迹。因此这里没有 Unsupported，只有 Note——唯一的例外是响应体本身
// 就不是 JSON，那说明上游已经坏了，没什么可翻的。

// OpenAIResponse 是一份 /v1/chat/completions 响应体。
type OpenAIResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object,omitempty"`
	Created int64           `json:"created,omitempty"`
	Model   string          `json:"model"`
	Choices []OpenAIChoice  `json:"choices"`
	Usage   *OpenAIUsage    `json:"usage,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// OpenAIChoice 一条候选。响应里通常只有一条。
type OpenAIChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// OpenAIUsage token 用量。缓存命中数在 prompt_tokens_details 里，OpenAI 把它算进
// prompt_tokens，而 Anthropic 的 cache_read_input_tokens 是**单独的**一档。
type OpenAIUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

// AnthropicResponse 是一份 /v1/messages 响应体。
type AnthropicResponse struct {
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	Role         string           `json:"role"`
	Model        string           `json:"model"`
	Content      []AnthropicBlock `json:"content"`
	StopReason   string           `json:"stop_reason"`
	StopSequence *string          `json:"stop_sequence"`
	Usage        *AnthropicUsage  `json:"usage,omitempty"`
}

// AnthropicUsage token 用量。input_tokens **不含**缓存命中的部分。
type AnthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// OpenAI → Anthropic
// ---------------------------------------------------------------------------

// OpenAIResponseToAnthropic 把一份 Chat Completions 响应翻成 Messages 响应。
func OpenAIResponseToAnthropic(raw []byte) ([]byte, []Note, error) {
	var res OpenAIResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("not a JSON chat completions response: %w", err)
	}
	if len(res.Error) > 0 && string(res.Error) != "null" {
		return openAIErrorToAnthropic(res.Error)
	}
	if len(res.Choices) == 0 {
		return nil, nil, fmt.Errorf("chat completions response has no choices")
	}

	var notes []Note
	if len(res.Choices) > 1 {
		// 请求侧已经拒了 n>1，走到这里说明上游自作主张多给了几份
		notes = append(notes, NoteDroppedExtraChoices)
	}
	choice := pickOpenAIChoice(res.Choices)

	content := make([]AnthropicBlock, 0, len(choice.Message.ToolCalls)+1)
	text, parts, err := openAIContent(choice.Message.Content)
	if err != nil {
		// 响应侧的 content 只可能是字符串、块数组或 null；真拿到别的形状（数字、
		// 对象），原样当文本带过去也比丢掉整段回答强
		text = string(choice.Message.Content)
		notes = append(notes, NoteUnparsableContent)
	}
	if parts != nil {
		// 块数组是 OpenAI 的扩展写法；Anthropic 的响应里只认文本块，别的块只能丢
		for _, p := range parts {
			if p.Type != "text" && p.Type != "input_text" {
				notes = append(notes, NoteDroppedUnknownBlock)
			}
		}
		text = textOfParts(parts)
	}
	if text != "" {
		content = append(content, AnthropicBlock{Type: blockText, Text: text})
	}
	// OpenAI 的 refusal 是一段独立的拒绝文本，Anthropic 没有对应的块类型
	if len(choice.Message.Refusal) > 0 && string(choice.Message.Refusal) != "null" && string(choice.Message.Refusal) != `""` {
		notes = append(notes, NoteDroppedRefusal)
	}
	// 推理内容要在这里记一笔：Anthropic 只有带签名的 thinking 块，签名给不出来。
	// 流式那条路径（stream.go）一直是这么做的，非流式少记这一笔的话，同一个请求
	// 流式与非流式会给出不同的账，谁也不知道该信哪个
	if choice.Message.hasReasoning() {
		notes = append(notes, NoteDroppedReasoning)
	}
	for i, call := range choice.Message.ToolCalls {
		input, err := toolInput(call.Function.Arguments)
		if err != nil {
			// 请求侧解不出就拒绝；响应侧拒绝了等于把这次生成整个丢掉，只能落空 input 并
			// 留下记号——调用方至少能从 Note 里看出这个工具调用是坏的
			input = json.RawMessage(`{}`)
			notes = append(notes, NoteUnparsableToolArguments)
		}
		id := call.ID
		if id == "" {
			// Anthropic 要求每个 tool_use 都有 id，客户端要拿它配对结果
			id = fmt.Sprintf("toolu_bridge_%d", i)
		}
		content = append(content, AnthropicBlock{Type: blockToolUse, ID: id, Name: call.Function.Name, Input: input})
	}

	stopReason := anthropicStopReason(choice.FinishReason, &notes)

	out := AnthropicResponse{
		ID:           anthropicMessageID(res.ID),
		Type:         "message",
		Role:         "assistant",
		Model:        res.Model,
		Content:      content,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage:        anthropicUsage(res.Usage),
	}
	body, _ := json.Marshal(out)
	return body, dedupeNotes(notes), nil
}

// pickOpenAIChoice 取要转的那条候选。正常只有一条（请求侧拒了 n>1）；真有多条时取
// index 最小的那条，而不是切片里的第一条——顺序不保证与 index 一致。
func pickOpenAIChoice(choices []OpenAIChoice) OpenAIChoice {
	best := choices[0]
	for _, c := range choices[1:] {
		if c.Index < best.Index {
			best = c
		}
	}
	return best
}

// textOfParts 把响应里的块数组拼成文本，非文本块被跳过。
//
// 响应侧与请求侧不同：这些块是模型刚吐出来的，无从拒绝，只能带上并记账。
func textOfParts(parts []OpenAIContentPart) string {
	out := ""
	for _, p := range parts {
		if p.Type == "text" || p.Type == "input_text" {
			out += p.Text
		}
	}
	return out
}

// anthropicStopReason 把 finish_reason 映射成 stop_reason。
//
// 有损的档位就在这个函数里记账，而不是交给调用方：流式与非流式都从这儿走，谁少记
// 一笔，同一个请求就会给出两份不同的账（content_filter 就是这么漏过一次）。
func anthropicStopReason(finish string, notes *[]Note) string {
	switch finish {
	case "", "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		*notes = append(*notes, NoteContentFiltered)
		return "refusal"
	default:
		// 认不出的档位落到 end_turn，但要留下痕迹：调用方靠 stop_reason 判断"是不是
		// 被截断了"，猜错会误导它
		*notes = append(*notes, NoteUnknownFinishReason)
		return "end_turn"
	}
}

// anthropicUsage 转换用量。Anthropic 的 input_tokens 不含缓存命中，OpenAI 的
// prompt_tokens 含，因此要把命中数减出来单列。
func anthropicUsage(usage *OpenAIUsage) *AnthropicUsage {
	if usage == nil {
		return nil
	}
	cached := int64(0)
	if usage.PromptTokensDetails != nil {
		cached = usage.PromptTokensDetails.CachedTokens
	}
	input := usage.PromptTokens - cached
	if input < 0 {
		input = 0
	}
	return &AnthropicUsage{
		InputTokens:          input,
		OutputTokens:         usage.CompletionTokens,
		CacheReadInputTokens: cached,
	}
}

// anthropicMessageID 给 Anthropic 的 id 换个前缀。id 是给客户端做日志关联用的，
// 这里沿用上游的串，只把 OpenAI 的前缀换掉，方便对照排查。
func anthropicMessageID(openaiID string) string {
	if openaiID == "" {
		return "msg_bridge"
	}
	return "msg_" + trimIDPrefix(openaiID)
}

func trimIDPrefix(id string) string {
	for _, prefix := range []string{"chatcmpl-", "msg_"} {
		if len(id) > len(prefix) && id[:len(prefix)] == prefix {
			return id[len(prefix):]
		}
	}
	return id
}

// ---------------------------------------------------------------------------
// Anthropic → OpenAI
// ---------------------------------------------------------------------------

// AnthropicResponseToOpenAI 把一份 Messages 响应翻成 Chat Completions 响应。
func AnthropicResponseToOpenAI(raw []byte) ([]byte, []Note, error) {
	var res AnthropicResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("not a JSON messages response: %w", err)
	}
	if res.Type == "error" {
		return anthropicErrorToOpenAI(raw)
	}

	var notes []Note
	texts, calls := anthropicContentToOpenAI(res.Content, &notes)

	message := OpenAIMessage{Role: "assistant", ToolCalls: calls}
	if len(texts) > 0 {
		message.Content = stringJSON(strings.Join(texts, ""))
	}

	out := OpenAIResponse{
		ID:      openAIChatID(res.ID),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   res.Model,
		Choices: []OpenAIChoice{{
			Index:        0,
			Message:      message,
			FinishReason: openAIFinishReason(res.StopReason, &notes),
		}},
		Usage: openAIUsage(res.Usage),
	}
	body, _ := json.Marshal(out)
	return body, dedupeNotes(notes), nil
}

// anthropicContentToOpenAI 把响应里的内容块拆成正文段与工具调用。
func anthropicContentToOpenAI(blocks []AnthropicBlock, notes *[]Note) ([]string, []OpenAIToolCall) {
	var texts []string
	var calls []OpenAIToolCall
	for i, b := range blocks {
		switch b.Type {
		case blockText:
			texts = append(texts, b.Text)
		case blockToolUse:
			input := b.Input
			if len(input) == 0 || string(input) == "null" {
				// input 缺失或为空都不是合法 JSON 对象，OpenAI 侧要的是字符串
				input = json.RawMessage(`{}`)
				*notes = append(*notes, NoteUnparsableToolArguments)
			}
			id := b.ID
			if id == "" {
				id = fmt.Sprintf("call_bridge_%d", i)
			}
			calls = append(calls, OpenAIToolCall{
				ID:       id,
				Type:     "function",
				Function: OpenAIFunctionCall{Name: b.Name, Arguments: string(input)},
			})
		case blockThinking, blockRedacted:
			// 带签名的思考块不发回客户端：OpenAI 的响应里没有这一档，而丢了签名它就
			// 再也不能被上游收下
			*notes = append(*notes, NoteDroppedThinking)
		default:
			// 认不出的块（比如服务端工具的结果）只能丢，但要记账
			*notes = append(*notes, NoteDroppedUnknownBlock)
		}
	}
	return texts, calls
}

// openAIFinishReason 把 stop_reason 映射成 finish_reason。有损的档位在这里记账，
// 理由同 anthropicStopReason。
func openAIFinishReason(stop string, notes *[]Note) string {
	switch stop {
	case "", "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		// Anthropic 的 refusal 是模型自己拒答，OpenAI 的 content_filter 是内容被过滤器
		// 拦下——成因不同，只有档位名最接近。客户端看到 content_filter 会以为"被安全
		// 策略挡了"，实际未必，因此这一笔与 o2a 方向同样要记
		*notes = append(*notes, NoteContentFiltered)
		return "content_filter"
	case "pause_turn":
		// 服务端主动暂停（长任务），OpenAI 没有这一档；客户端只会当作正常结束
		*notes = append(*notes, NotePausedTurn)
		return "stop"
	default:
		*notes = append(*notes, NoteUnknownFinishReason)
		return "stop"
	}
}

// openAIUsage 转换用量。llmio 自己记 Anthropic 用量时把缓存命中并进 prompt_tokens
// （见 service.ProcesserAnthropic），这里照同一个口径还原，两边的成本算法才对得上。
func openAIUsage(usage *AnthropicUsage) *OpenAIUsage {
	if usage == nil {
		return nil
	}
	prompt := usage.InputTokens + usage.CacheReadInputTokens
	return &OpenAIUsage{
		PromptTokens:     prompt,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      prompt + usage.OutputTokens,
		PromptTokensDetails: &struct {
			CachedTokens int64 `json:"cached_tokens"`
		}{CachedTokens: usage.CacheReadInputTokens},
	}
}

func openAIChatID(id string) string {
	if id == "" {
		return "chatcmpl-bridge"
	}
	return "chatcmpl-" + trimIDPrefix(id)
}

// ---------------------------------------------------------------------------
// 错误体
// ---------------------------------------------------------------------------

// openAIErrorToAnthropic 把上游的错误对象翻成 Anthropic 的形状。
//
// llmio 的 ErrorMatcher 认的是 HTTP 200 + 体内 error 这种"假成功"（部分中转站这么
// 干）。这里不判断该不该重试——那是上游侧的事——只保证客户端看到的是它认识的形状。
func openAIErrorToAnthropic(raw json.RawMessage) ([]byte, []Note, error) {
	var payload struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
	if payload.Message == "" {
		payload.Message = string(raw)
	}
	kind := payload.Type
	if kind == "" {
		kind = "api_error"
	}
	body, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    kind,
			"message": payload.Message,
		},
	})
	return body, nil, nil
}

// anthropicErrorToOpenAI 反向。Anthropic 的错误类型（invalid_request_error 之类）
// 在 OpenAI 里没有对应枚举，原样放进 message 里，type 统一成 invalid_request_error
// 之外的那一档要按 HTTP 语义猜，因此不猜：一律给 "api_error"。
func anthropicErrorToOpenAI(raw []byte) ([]byte, []Note, error) {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &payload)
	message := payload.Error.Message
	if message == "" {
		message = string(raw)
	}
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "api_error",
			"code":    payload.Error.Type,
		},
	})
	return body, nil, nil
}
