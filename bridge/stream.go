package bridge

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
)

// 流式（SSE）互转。
//
// 两个方向都是有状态机：一条 SSE 事件流被逐块喂进来，吐出去的是**另一种协议的事件**。
// 与响应侧同理，这里也没有拒绝的余地——第一个字节发出去之后就没有回头路，只能把能翻的
// 翻过去、翻不动的记 Note。
//
// 两处最容易写错的地方，都在下面用注释标了：
//   - 收尾事件**必须发**：上游在 `[DONE]` 前不给 finish_reason 是常事（纯工具调用流），
//     少了 message_delta 客户端会一直等下去
//   - 工具调用的参数片段要攒起来最后发：OpenAI 允许多个 tool_call 的片段交错（各自一条
//     通道），而 Anthropic 的内容块一旦 stop 就不能重开，逐块转发会把参数搅在一起

// Streamer 把上游的 SSE 字节流翻译成客户端协议。
//
// Write 吃一段（可能是半截的）上游字节，返回此刻应当发给客户端的字节，允许返回空。
// Close 在上游流结束时调用，负责把收尾事件补上。
type Streamer interface {
	Write(p []byte) ([]byte, error)
	Close() ([]byte, error)
	// Notes 返回迄今为止记录的有损改写。流跑完再看。
	Notes() []Note
}

// ---------------------------------------------------------------------------
// SSE 解码与编码
// ---------------------------------------------------------------------------

// sseEvent 一条 SSE 事件：event 名（OpenAI 的流里没有）与 data 内容。
type sseEvent struct {
	name string
	data string
}

// sseDecoder 把任意切分的字节流切成完整事件。
//
// 上游的字节边界与事件边界无关（一个 Read 可能是半个事件，也可能是三个半），所以必须
// 自己攒行。事件以空行结束，未结束的那条留在 carry 里等下一段。
type sseDecoder struct {
	carry []byte
	name  string
	data  []string
}

func (d *sseDecoder) Decode(p []byte) []sseEvent {
	d.carry = append(d.carry, p...)
	var out []sseEvent
	for {
		idx := indexByte(d.carry, '\n')
		if idx < 0 {
			return out
		}
		line := strings.TrimSuffix(string(d.carry[:idx]), "\r")
		d.carry = d.carry[idx+1:]
		if ev, ok := d.feedLine(line); ok {
			out = append(out, ev)
		}
	}
}

// feedLine 吃一行，返回它凑齐的那条事件（如果有）。
func (d *sseDecoder) feedLine(line string) (sseEvent, bool) {
	switch {
	case line == "":
		// 空行 = 事件结束。没有 data 的（比如只有注释行的心跳）不算事件
		if len(d.data) == 0 {
			return sseEvent{}, false
		}
		ev := sseEvent{name: d.name, data: strings.Join(d.data, "\n")}
		d.name = ""
		d.data = nil
		return ev, true
	case strings.HasPrefix(line, "event:"):
		d.name = strings.TrimSpace(line[len("event:"):])
	case strings.HasPrefix(line, "data:"):
		d.data = append(d.data, dataValue(line))
	}
	return sseEvent{}, false
}

// dataValue 取出一行 data 的值。规范只允许去掉一个前导空格，这里多去一个是沿用
// 上游实现的宽松写法。
func dataValue(line string) string {
	return strings.TrimPrefix(strings.TrimPrefix(line[len("data:"):], " "), " ")
}

// Flush 取出手里未结束的那条事件。上游没发结尾空行就断开时用得上。
func (d *sseDecoder) Flush() []sseEvent {
	// 上游断在行中间（carry 里只剩半行，没有换行）时，这半行也是最后一行：
	// 不认它，整条事件就丢了
	if len(d.carry) > 0 {
		line := strings.TrimSuffix(string(d.carry), "\r")
		d.carry = nil
		if ev, ok := d.feedLine(line); ok {
			return []sseEvent{ev}
		}
	}
	if len(d.data) == 0 {
		return nil
	}
	ev := sseEvent{name: d.name, data: strings.Join(d.data, "\n")}
	d.name = ""
	d.data = nil
	return []sseEvent{ev}
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// encodeSSE 拼一条 SSE 事件。event 名为空时只发 data 行（OpenAI 的形状）。
func encodeSSE(name, data string) string {
	if name == "" {
		return "data: " + data + "\n\n"
	}
	return "event: " + name + "\ndata: " + data + "\n\n"
}

// marshalSSE 编码成 JSON 后再拼成 SSE。所有输入都来自已解析的 JSON，编码不会失败。
func marshalSSE(name string, v any) string {
	body, _ := json.Marshal(v)
	return encodeSSE(name, string(body))
}

// ---------------------------------------------------------------------------
// OpenAI → Anthropic
// ---------------------------------------------------------------------------

// openAIStreamChunk 是 OpenAI 流里的一段。
type openAIStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string           `json:"content"`
			ToolCalls        []OpenAIToolCall `json:"tool_calls"`
			ReasoningContent string           `json:"reasoning_content"`
			Reasoning        string           `json:"reasoning"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *OpenAIUsage    `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// OpenAIToAnthropicStream 把 OpenAI 的流式响应翻成 Anthropic 的事件流。
type OpenAIToAnthropicStream struct {
	dec   sseDecoder
	notes []Note

	started    bool
	textOpen   bool
	blockIndex int

	// tailDone 收尾事件（message_delta / message_stop）是否已经发过，只发一次。
	tailDone bool
	// errored 流中间报过错，此后不再补收尾：客户端已经把半截回答当失败处理了。
	errored bool
	// finishReason 上游给的 finish_reason。**不立刻收尾**，原因见 finish 的注释。
	finishReason string

	id    string
	model string

	// 上游的用量通常只在最后一段给（stream_options.include_usage）
	usage *AnthropicUsage

	tools map[int]*bufferedToolCall
	order []int
}

// bufferedToolCall 一路工具调用的参数攒在这里。
//
// 攒而不是边收边发，是因为 OpenAI 允许多路工具调用的参数片段交错到达，而 Anthropic 的
// 内容块一旦关闭就不能重开：交错时只有两条路，要么把 A 的参数发进 B 的块里（客户端拼
// 出来的 JSON 直接是坏的），要么在这里等它攒齐。工具参数通常只有几十个字节，客户端本来
// 也要等 finish_reason 才会去执行工具，所以等这一会儿不损失什么。
type bufferedToolCall struct {
	id   string
	name string
	args strings.Builder
}

// NewOpenAIToAnthropicStream 起一条 OpenAI → Anthropic 的流。
func NewOpenAIToAnthropicStream() *OpenAIToAnthropicStream {
	return &OpenAIToAnthropicStream{tools: map[int]*bufferedToolCall{}}
}

func (s *OpenAIToAnthropicStream) Notes() []Note { return dedupeNotes(s.notes) }

func (s *OpenAIToAnthropicStream) Write(p []byte) ([]byte, error) {
	var out strings.Builder
	for _, ev := range s.dec.Decode(p) {
		if ev.data == "[DONE]" {
			out.WriteString(s.finish())
			continue
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(ev.data), &chunk); err != nil {
			// 认不出的载荷直接跳过：SSE 流里常有厂商自己的心跳帧
			s.notes = append(s.notes, NoteSkippedChunk)
			continue
		}
		out.WriteString(s.handleChunk(chunk))
	}
	return []byte(out.String()), nil
}

func (s *OpenAIToAnthropicStream) handleChunk(chunk openAIStreamChunk) string {
	var out strings.Builder

	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		// 上游在流中间报错：转成 Anthropic 的 error 事件，客户端才不会把半截回答当成完整的
		s.errored = true
		out.WriteString(marshalSSE("error", map[string]any{
			"type":  "error",
			"error": json.RawMessage(chunk.Error),
		}))
		return out.String()
	}

	if chunk.ID != "" {
		s.id = chunk.ID
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.Usage != nil {
		s.usage = anthropicUsage(chunk.Usage)
	}
	out.WriteString(s.start())

	if len(chunk.Choices) == 0 {
		return out.String()
	}
	choice := chunk.Choices[0]
	delta := choice.Delta

	if delta.ReasoningContent != "" || delta.Reasoning != "" {
		// Anthropic 的 thinking 块必须带签名，这里给不出来，只能丢
		s.notes = append(s.notes, NoteDroppedReasoning)
	}
	if delta.Content != "" {
		out.WriteString(s.openText())
		out.WriteString(marshalSSE("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": s.blockIndex,
			"delta": map[string]string{"type": "text_delta", "text": delta.Content},
		}))
	}
	for _, call := range delta.ToolCalls {
		s.bufferToolCall(call)
	}
	if choice.FinishReason != "" {
		s.finishReason = choice.FinishReason
	}
	return out.String()
}

// start 发 message_start。第一条被认出的载荷到达时才发，且只发一次。
func (s *OpenAIToAnthropicStream) start() string {
	if s.started {
		return ""
	}
	s.started = true
	id := anthropicMessageID(s.id)
	usage := map[string]int64{"input_tokens": 0, "output_tokens": 0}
	return marshalSSE("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            id,
			"type":          "message",
			"role":          "assistant",
			"model":         s.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			// 输入侧用量在 OpenAI 的流里通常只在最后一段给，开头给不出来：Anthropic 的
			// 协议允许这里先报 0，客户端以 message_delta 里的数为准
			"usage": usage,
		},
	})
}

// openText 确保有一个打开的文本块，返回为此要发的事件（可能为空）。
func (s *OpenAIToAnthropicStream) openText() string {
	if s.textOpen {
		return ""
	}
	var out strings.Builder
	out.WriteString(s.closeBlock())
	out.WriteString(marshalSSE("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         s.blockIndex,
		"content_block": map[string]any{"type": "text", "text": ""},
	}))
	s.textOpen = true
	return out.String()
}

// closeBlock 关掉当前打开的内容块。
func (s *OpenAIToAnthropicStream) closeBlock() string {
	if !s.textOpen {
		return ""
	}
	s.textOpen = false
	return marshalSSE("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.blockIndex,
	})
}

// bufferToolCall 收下一段工具调用的参数。id 与 name 只在第一片里给，后面靠通道号认。
func (s *OpenAIToAnthropicStream) bufferToolCall(call OpenAIToolCall) {
	idx := 0
	if call.Index != nil {
		idx = *call.Index
	}
	channel, ok := s.tools[idx]
	if !ok {
		channel = &bufferedToolCall{}
		s.tools[idx] = channel
		s.order = append(s.order, idx)
	}
	if call.ID != "" {
		channel.id = call.ID
	}
	if call.Function.Name != "" {
		channel.name = call.Function.Name
	}
	channel.args.WriteString(call.Function.Arguments)
}

// finish 收尾：补上工具调用块、message_delta 与 message_stop。重复调用只生效一次。
//
// 收尾**不在看见 finish_reason 的那一刻发生**，而是等到 [DONE] 或 Close。因为开了
// stream_options.include_usage 的上游会把收尾拆成两段发：先是带 finish_reason、usage
// 为空的那条，紧跟着才是 choices 为空、只带 usage 的那条。看见前一条就收尾的话，用量
// 永远落在收尾之后被丢掉——而 llmio 记 Anthropic 用量只认 message_delta 这一条。
func (s *OpenAIToAnthropicStream) finish() string {
	if s.tailDone || s.errored {
		return ""
	}
	s.tailDone = true

	var out strings.Builder
	// 一条载荷都没认出来就收尾（上游直接断流）：也得把 message_start 补上，
	// 否则客户端收到的是半条流
	out.WriteString(s.start())
	out.WriteString(s.closeBlock())
	out.WriteString(s.flushToolCalls())

	stop := "end_turn"
	if s.finishReason != "" {
		stop = anthropicStopReason(s.finishReason, &s.notes)
		if s.finishReason == "content_filter" {
			s.notes = append(s.notes, NoteContentFiltered)
		}
	} else if len(s.tools) > 0 {
		// [DONE] 前不给 finish_reason 是工具调用流的常态（见 docs/protocol-bridge.md §6）。
		// 见没见过工具调用是唯一能拿来推断的依据，不发 message_delta 客户端会一直等
		stop = "tool_use"
	}
	// 用量放进收尾事件：Anthropic 的 message_delta 里的 usage 是**累计值**，llmio 记
	// Anthropic 用量时也只认这一条（service.ProcesserAnthropic），所以输入侧的数字
	// 必须在这里补齐，否则这条链路上的输入 token 会被记成 0
	usage := map[string]int64{"input_tokens": 0, "output_tokens": 0}
	if s.usage != nil {
		usage["input_tokens"] = s.usage.InputTokens
		usage["output_tokens"] = s.usage.OutputTokens
		if s.usage.CacheReadInputTokens > 0 {
			usage["cache_read_input_tokens"] = s.usage.CacheReadInputTokens
		}
	}
	out.WriteString(marshalSSE("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": usage,
	}))
	out.WriteString(marshalSSE("message_stop", map[string]any{"type": "message_stop"}))
	return out.String()
}

// flushToolCalls 把攒下的工具调用一次性发成 tool_use 块。
func (s *OpenAIToAnthropicStream) flushToolCalls() string {
	var out strings.Builder
	for _, idx := range s.order {
		channel := s.tools[idx]
		s.blockIndex++
		id := channel.id
		if id == "" {
			id = "toolu_bridge_" + strconv.Itoa(idx)
		}
		out.WriteString(marshalSSE("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": s.blockIndex,
			"content_block": map[string]any{
				"type": "tool_use", "id": id, "name": channel.name, "input": map[string]any{},
			},
		}))
		args := channel.args.String()
		if args == "" {
			// 空参数是"无参数"的常见写法，落成空对象而不是空串（空串客户端解不出来）
			args = "{}"
		}
		out.WriteString(marshalSSE("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": s.blockIndex,
			"delta": map[string]string{"type": "input_json_delta", "partial_json": args},
		}))
		out.WriteString(marshalSSE("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": s.blockIndex,
		}))
	}
	return out.String()
}

// Close 补齐收尾事件。上游没发 [DONE] 就断开时，这一步是唯一的补救。
func (s *OpenAIToAnthropicStream) Close() ([]byte, error) {
	var out strings.Builder
	for _, ev := range s.dec.Flush() {
		if ev.data == "[DONE]" {
			out.WriteString(s.finish())
			continue
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(ev.data), &chunk); err != nil {
			continue
		}
		out.WriteString(s.handleChunk(chunk))
	}
	out.WriteString(s.finish())
	return []byte(out.String()), nil
}

// ---------------------------------------------------------------------------
// Anthropic → OpenAI
// ---------------------------------------------------------------------------

// anthropicStreamEvent 是 Anthropic 流里的一段。
type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID    string          `json:"id"`
		Model string          `json:"model"`
		Usage *AnthropicUsage `json:"usage"`
	} `json:"message"`
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *AnthropicUsage `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// openAIToolCallDelta 是 OpenAI 流里工具调用增量。id 与 name 只在第一片里发。
type openAIToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type openAIStreamDelta struct {
	Role      string                `json:"role,omitempty"`
	Content   string                `json:"content,omitempty"`
	ToolCalls []openAIToolCallDelta `json:"tool_calls,omitempty"`
}

type openAIStreamChoice struct {
	Index        int               `json:"index"`
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason *string           `json:"finish_reason"`
}

type openAIStreamOut struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *OpenAIUsage         `json:"usage,omitempty"`
}

// AnthropicToOpenAIStream 把 Anthropic 的事件流翻成 OpenAI 的流式响应。
type AnthropicToOpenAIStream struct {
	dec   sseDecoder
	notes []Note

	started  bool
	finished bool
	created  int64

	id    string
	model string

	// Anthropic 的内容块下标 → OpenAI 的 tool_calls 下标
	toolIndex map[int]int
	nextTool  int

	inputTokens  int64
	cacheRead    int64
	outputTokens int64
}

// NewAnthropicToOpenAIStream 起一条 Anthropic → OpenAI 的流。
func NewAnthropicToOpenAIStream() *AnthropicToOpenAIStream {
	return &AnthropicToOpenAIStream{
		toolIndex: map[int]int{},
		created:   time.Now().Unix(),
	}
}

func (s *AnthropicToOpenAIStream) Notes() []Note { return dedupeNotes(s.notes) }

func (s *AnthropicToOpenAIStream) Write(p []byte) ([]byte, error) {
	var out strings.Builder
	for _, ev := range s.dec.Decode(p) {
		out.WriteString(s.handleEvent(ev.name, ev.data))
	}
	return []byte(out.String()), nil
}

func (s *AnthropicToOpenAIStream) handleEvent(name, data string) string {
	var ev anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		s.notes = append(s.notes, NoteSkippedChunk)
		return ""
	}
	kind := ev.Type
	if kind == "" {
		kind = name
	}

	var out strings.Builder
	switch kind {
	case "message_start":
		s.id = ev.Message.ID
		s.model = ev.Message.Model
		if ev.Message.Usage != nil {
			s.inputTokens = ev.Message.Usage.InputTokens
			s.cacheRead = ev.Message.Usage.CacheReadInputTokens
		}
		out.WriteString(s.start())
	case "content_block_start":
		out.WriteString(s.start())
		switch ev.ContentBlock.Type {
		case blockToolUse:
			idx := s.nextTool
			s.nextTool++
			s.toolIndex[ev.Index] = idx
			call := openAIToolCallDelta{Index: idx, ID: ev.ContentBlock.ID, Type: "function"}
			call.Function.Name = ev.ContentBlock.Name
			out.WriteString(s.chunk(openAIStreamDelta{ToolCalls: []openAIToolCallDelta{call}}, nil))
		case blockThinking, blockRedacted:
			// 思考块没有对应物；它下面的 thinking_delta 也一并丢掉
			s.notes = append(s.notes, NoteDroppedThinking)
		}
	case "content_block_delta":
		out.WriteString(s.start())
		switch ev.Delta.Type {
		case "text_delta":
			out.WriteString(s.chunk(openAIStreamDelta{Content: ev.Delta.Text}, nil))
		case "input_json_delta":
			call := openAIToolCallDelta{Index: s.toolIndex[ev.Index]}
			call.Function.Arguments = ev.Delta.PartialJSON
			out.WriteString(s.chunk(openAIStreamDelta{ToolCalls: []openAIToolCallDelta{call}}, nil))
		case "thinking_delta", "signature_delta":
			// OpenAI 的响应里没有思考块，丢了
			s.notes = append(s.notes, NoteDroppedThinking)
		default:
			s.notes = append(s.notes, NoteDroppedUnknownEvent)
		}
	case "message_delta":
		if ev.Usage != nil {
			s.outputTokens = ev.Usage.OutputTokens
			// 有些上游在收尾事件里才把输入用量补齐
			if ev.Usage.InputTokens > 0 {
				s.inputTokens = ev.Usage.InputTokens
			}
		}
		out.WriteString(s.finish(ev.Delta.StopReason))
	case "message_stop":
		out.WriteString(s.finish(""))
	case "error":
		// 流中间的错误必须原样送给客户端，否则它会把半截回答当成完整的
		s.finished = true
		out.WriteString(encodeSSE("", `{"error":`+string(ev.Error)+`}`))
	case "ping", "content_block_stop":
		// 这两个在 OpenAI 的流里没有对应物，也没有信息量
	default:
		s.notes = append(s.notes, NoteDroppedUnknownEvent)
	}
	return out.String()
}

// start 发第一条 chunk。OpenAI 的流以一条带 role 的 delta 开头。
func (s *AnthropicToOpenAIStream) start() string {
	if s.started {
		return ""
	}
	s.started = true
	return s.chunk(openAIStreamDelta{Role: "assistant", Content: ""}, nil)
}

// chunk 拼一条 OpenAI 的流式 chunk。
func (s *AnthropicToOpenAIStream) chunk(delta openAIStreamDelta, finishReason *string) string {
	out := openAIStreamOut{
		ID:      s.openAIID(),
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []openAIStreamChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
	}
	return marshalSSE("", out)
}

// finish 发收尾 chunk 与 [DONE]。重复调用只生效一次。
func (s *AnthropicToOpenAIStream) finish(stopReason string) string {
	if s.finished {
		return ""
	}
	s.finished = true

	var out strings.Builder
	out.WriteString(s.start())
	reason := openAIFinishReason(stopReason, &s.notes)
	prompt := s.inputTokens + s.cacheRead
	// 用量放进收尾那一条：OpenAI 的流只在最后报 token，llmio 的用量统计也是从这条里取的
	out.WriteString(marshalSSE("", openAIStreamOut{
		ID:      s.openAIID(),
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []openAIStreamChoice{{Index: 0, Delta: openAIStreamDelta{}, FinishReason: &reason}},
		Usage: &OpenAIUsage{
			PromptTokens:     prompt,
			CompletionTokens: s.outputTokens,
			TotalTokens:      prompt + s.outputTokens,
			PromptTokensDetails: &struct {
				CachedTokens int64 `json:"cached_tokens"`
			}{CachedTokens: s.cacheRead},
		},
	}))
	out.WriteString(encodeSSE("", "[DONE]"))
	return out.String()
}

func (s *AnthropicToOpenAIStream) openAIID() string {
	if s.id == "" {
		return "chatcmpl-bridge"
	}
	return openAIChatID(s.id)
}

// Close 补齐收尾。上游没发 message_stop 就断开时，这一步是唯一的补救。
func (s *AnthropicToOpenAIStream) Close() ([]byte, error) {
	var out strings.Builder
	for _, ev := range s.dec.Flush() {
		out.WriteString(s.handleEvent(ev.name, ev.data))
	}
	out.WriteString(s.finish(""))
	return []byte(out.String()), nil
}

// ---------------------------------------------------------------------------
// 接到转发路径上
// ---------------------------------------------------------------------------

// BridgedBody 把上游响应体包成翻译后的响应体：读出来的是客户端协议，推送与记录都拿它。
type BridgedBody struct {
	src    io.ReadCloser
	stream Streamer
	out    []byte
	done   bool
	err    error
}

// NewBridgedBody 包一层翻译。src 会在读到 EOF 或出错时关闭。
func NewBridgedBody(src io.ReadCloser, stream Streamer) *BridgedBody {
	return &BridgedBody{src: src, stream: stream}
}

// Notes 返回流跑到此刻记录的有损改写。
func (b *BridgedBody) Notes() []Note { return b.stream.Notes() }

func (b *BridgedBody) Read(p []byte) (int, error) {
	for len(b.out) == 0 {
		if b.done {
			if b.err != nil {
				return 0, b.err
			}
			return 0, io.EOF
		}
		buf := make([]byte, 4096)
		n, err := b.src.Read(buf)
		if n > 0 {
			chunk, cerr := b.stream.Write(buf[:n])
			if cerr != nil {
				b.done = true
				return 0, cerr
			}
			b.out = append(b.out, chunk...)
		}
		if err == nil {
			continue
		}
		b.done = true
		if err != io.EOF {
			// 上游是读挂了，不是正常结束。这里**不能补收尾**：补出来的 message_delta
			// 与 message_stop 等于告诉客户端"模型一句话没说就干净地答完了"，而事实是
			// 这条流断了。已经翻好的那几个字节照发，之后再把这个错误原样透出去。
			b.err = err
			if len(b.out) == 0 {
				return 0, err
			}
			continue
		}
		tail, cerr := b.stream.Close()
		if cerr != nil {
			return 0, cerr
		}
		b.out = append(b.out, tail...)
		if len(b.out) == 0 {
			return 0, io.EOF
		}
	}
	n := copy(p, b.out)
	b.out = b.out[n:]
	return n, nil
}

func (b *BridgedBody) Close() error { return b.src.Close() }
