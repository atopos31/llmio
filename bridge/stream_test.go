package bridge

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// 流式互转的测试。
//
// 流式的偶发故障（客户端一直等下去、参数片段串了通道）都出在"事件之间的状态"上，
// 因此用例尽量按真实的时间顺序喂字节，并且**故意把一条事件劈成两半**再喂——上游的
// 字节边界与事件边界无关，这正是真机上最容易崩的地方。

// feed 按给定的切片把数据喂给一条流，最后 Close 收尾，返回全部输出。
func feed(t *testing.T, s Streamer, chunks ...string) string {
	t.Helper()
	var out strings.Builder
	for _, chunk := range chunks {
		got, err := s.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write 失败: %v", err)
		}
		out.Write(got)
	}
	tail, err := s.Close()
	if err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	out.Write(tail)
	return out.String()
}

// events 把输出切成 (event 名, data) 对。OpenAI 方向没有 event 名。
func events(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var dec sseDecoder
	out := dec.Decode([]byte(raw))
	out = append(out, dec.Flush()...)
	return out
}

// payloads 取出输出里所有 data 的 JSON。
func payloads(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range events(t, raw) {
		if ev.data == "[DONE]" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.data), &payload); err != nil {
			t.Fatalf("输出不是合法 JSON: %v\n%s", err, ev.data)
		}
		payload["__event"] = ev.name
		out = append(out, payload)
	}
	return out
}

// eventNames 输出里的事件名序列。
func eventNames(t *testing.T, raw string) []string {
	t.Helper()
	var names []string
	for _, ev := range events(t, raw) {
		names = append(names, ev.name)
	}
	return names
}

func openAIChunk(t *testing.T, body string) string {
	t.Helper()
	return "data: " + body + "\n\n"
}

func anthropicEvent(name, body string) string {
	return "event: " + name + "\ndata: " + body + "\n\n"
}

// ---------------------------------------------------------------------------
// SSE 解码/编码
// ---------------------------------------------------------------------------

func TestSSEDecoder(t *testing.T) {
	t.Run("一条事件劈成两半也能认出来", func(t *testing.T) {
		var dec sseDecoder
		if got := dec.Decode([]byte("event: message_start\ndata: {\"a\"")); len(got) != 0 {
			t.Fatalf("半条事件不该吐出来: %v", got)
		}
		got := dec.Decode([]byte(":1}\n\n"))
		if len(got) != 1 || got[0].name != "message_start" || got[0].data != `{"a":1}` {
			t.Fatalf("拼回来的事件不对: %v", got)
		}
	})

	t.Run("CRLF 与多行 data", func(t *testing.T) {
		var dec sseDecoder
		got := dec.Decode([]byte("data: 第一行\r\ndata: 第二行\r\n\r\n"))
		if len(got) != 1 || got[0].data != "第一行\n第二行" {
			t.Fatalf("多行 data 应当用换行拼起来: %v", got)
		}
	})

	t.Run("没有 data 的心跳不算事件", func(t *testing.T) {
		var dec sseDecoder
		if got := dec.Decode([]byte(": keepalive\n\n")); len(got) != 0 {
			t.Fatalf("注释行不该产生事件: %v", got)
		}
	})

	t.Run("Flush 取出没等到空行的那条", func(t *testing.T) {
		var dec sseDecoder
		dec.Decode([]byte("data: {\"a\":1}"))
		got := dec.Flush()
		if len(got) != 1 || got[0].data != `{"a":1}` {
			t.Fatalf("Flush 应当取出手里那条: %v", got)
		}
		if again := dec.Flush(); again != nil {
			t.Fatalf("取过之后应当空了: %v", again)
		}
	})

	t.Run("断在纯 CR 上也算事件结束", func(t *testing.T) {
		// SSE 规范里 CR 单独也是行结束符。上游用 CRLF 而在最后那个 CR 之后断掉时，
		// carry 里只剩一个 "\r"，它就是那个空行
		var dec sseDecoder
		dec.Decode([]byte("data: {\"a\":1}\r\n\r"))
		got := dec.Flush()
		if len(got) != 1 || got[0].data != `{"a":1}` {
			t.Fatalf("CR 结尾也应当认成事件结束: %v", got)
		}
	})

	t.Run("carry 里认不出的半行不凑事件", func(t *testing.T) {
		var dec sseDecoder
		if got := dec.Decode([]byte("data: {\"a\":1}\n\nnot a field: x")); len(got) != 1 {
			t.Fatalf("完整的那条应当先发出来: %v", got)
		}
		if got := dec.Flush(); got != nil {
			t.Fatalf("认不出的半行不该凑出事件: %v", got)
		}
	})
}

func TestEncodeSSE(t *testing.T) {
	if got := encodeSSE("", `{"a":1}`); got != "data: {\"a\":1}\n\n" {
		t.Fatalf("OpenAI 形状不对: %q", got)
	}
	if got := encodeSSE("message_stop", `{"type":"message_stop"}`); got != "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n" {
		t.Fatalf("Anthropic 形状不对: %q", got)
	}
}

// ---------------------------------------------------------------------------
// OpenAI → Anthropic
// ---------------------------------------------------------------------------

func TestOpenAIToAnthropicStreamText(t *testing.T) {
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`),
		openAIChunk(t, `{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"你"},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{"content":"好"},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":4}}}`),
		openAIChunk(t, "[DONE]"),
	)

	names := eventNames(t, out)
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列不对:\n%v", names)
	}

	payloadsOut := payloads(t, out)
	if payloadsOut[0]["message"].(map[string]any)["id"] != "msg_1" {
		t.Fatalf("message_start 的 id 不对: %v", payloadsOut[0])
	}
	if payloadsOut[1]["content_block"].(map[string]any)["type"] != "text" {
		t.Fatalf("应当开一个文本块: %v", payloadsOut[1])
	}
	if payloadsOut[2]["delta"].(map[string]any)["text"] != "你" {
		t.Fatalf("文本增量不对: %v", payloadsOut[2])
	}
	if payloadsOut[5]["delta"].(map[string]any)["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason 不对: %v", payloadsOut[5])
	}
	// 用量必须在 message_delta 里给全：llmio 记 Anthropic 用量时只认这一条
	usage := payloadsOut[5]["usage"].(map[string]any)
	if usage["input_tokens"] != float64(6) || usage["output_tokens"] != float64(2) || usage["cache_read_input_tokens"] != float64(4) {
		t.Fatalf("用量没补进收尾事件: %v", usage)
	}
	if len(s.Notes()) != 0 {
		t.Fatalf("不该有 Note: %v", s.Notes())
	}
}

func TestOpenAIToAnthropicStreamToolCalls(t *testing.T) {
	// 两路工具调用的参数片段交错到达：Anthropic 的块一旦关闭就不能重开，所以只能
	// 各攒各的、最后一起发——逐片转发会把两路的参数搅成一段坏 JSON
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"我查两个"},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"get_time","arguments":"{\"tz\":\"UTC\"}"}}]},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"bj\"}"}}]},"finish_reason":null}]}`),
		openAIChunk(t, "[DONE]"),
	)

	if !strings.Contains(out, `"type":"tool_use"`) || !strings.Contains(out, `"type":"input_json_delta"`) {
		t.Fatalf("工具调用没转成 tool_use 块: %s", out)
	}
	calls := toolUseBlocks(t, out)
	if len(calls) != 2 {
		t.Fatalf("应当有两路工具调用: %v", calls)
	}
	if calls[0]["id"] != "call_1" || calls[0]["name"] != "get_weather" {
		t.Fatalf("第一路不对: %v", calls[0])
	}
	// 交错的两片必须回到各自的通道里
	var sb strings.Builder
	for _, ev := range events(t, out) {
		var payload map[string]any
		if json.Unmarshal([]byte(ev.data), &payload) != nil {
			continue
		}
		if payload["type"] != "content_block_delta" {
			continue
		}
		delta, _ := payload["delta"].(map[string]any)
		if delta["type"] == "input_json_delta" {
			sb.WriteString(delta["partial_json"].(string))
		}
	}
	if sb.String() != `{"city":"bj"}{"tz":"UTC"}` {
		t.Fatalf("参数片段串了通道: %q", sb.String())
	}
	// [DONE] 前没有 finish_reason，见过工具调用就该按 tool_use 收尾
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("[DONE] 时应当按工具调用收尾: %s", out)
	}
}

func TestOpenAIToAnthropicStreamNoFinishReason(t *testing.T) {
	// 纯文本流也不给 finish_reason：不发 message_delta，客户端会一直等下去
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"只有文字"},"finish_reason":null}]}`),
		openAIChunk(t, "[DONE]"),
	)

	if !strings.Contains(out, `"stop_reason":"end_turn"`) || !strings.Contains(out, `"type":"message_stop"`) {
		t.Fatalf("收尾事件没发全: %s", out)
	}
}

func TestOpenAIToAnthropicStreamCloseWithoutDone(t *testing.T) {
	// 上游直接断流（没有 [DONE]、没有 finish_reason）：Close 是唯一的补救机会
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s, openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"半截"},"finish_reason":null}]}`))

	if !strings.Contains(out, `"type":"content_block_stop"`) || !strings.Contains(out, `"type":"message_delta"`) {
		t.Fatalf("断流时也要把块关上并收尾: %s", out)
	}
	// 再 Close 一次不该重复发
	again, _ := s.Close()
	if len(again) != 0 {
		t.Fatalf("重复 Close 不该再发事件: %q", again)
	}
}

func TestOpenAIToAnthropicStreamEmptyStream(t *testing.T) {
	// 一个载荷都没有就断了：客户端至少得收到一条完整的空消息，而不是什么都没有
	s := NewOpenAIToAnthropicStream()
	out, err := s.Close()
	if err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if !strings.Contains(string(out), `"type":"message_start"`) {
		t.Fatalf("空流也要补 message_start: %s", out)
	}
}

func TestOpenAIToAnthropicStreamHalfChunkOnEOF(t *testing.T) {
	// 上游在事件中间断掉：decoder 手里那半条要在 Close 里被认出来，不能整段吞掉
	s := NewOpenAIToAnthropicStream()
	got, err := s.Write([]byte(`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"半"}}]}`))
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if got != nil && len(got) > 0 {
		t.Fatalf("还没成事件就不该吐东西: %s", got)
	}
	tail, err := s.Close()
	if err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if !strings.Contains(string(tail), `"text":"半"`) {
		t.Fatalf("Close 应当认出手里那半条: %s", tail)
	}
}

func TestOpenAIToAnthropicStreamTruncatedTail(t *testing.T) {
	t.Run("断在 [DONE] 之后", func(t *testing.T) {
		// 收尾标记到了、结尾空行没到：也算收到了收尾
		s := NewOpenAIToAnthropicStream()
		out := feed(t, s,
			openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`),
			"data: [DONE]",
		)
		if count := strings.Count(out, `"type":"message_stop"`); count != 1 {
			t.Fatalf("message_stop 应当恰好一条，实际 %d:\n%s", count, out)
		}
	})

	t.Run("断在认不出的帧上", func(t *testing.T) {
		s := NewOpenAIToAnthropicStream()
		out := feed(t, s, "data: {半截的 json")
		if !strings.Contains(out, `"type":"message_stop"`) {
			t.Fatalf("读不动的尾巴也要正常收尾: %s", out)
		}
	})
}

func TestOpenAIToAnthropicStreamUnknownChunk(t *testing.T) {
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `not json at all`),
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"真正的正文"},"finish_reason":"stop"}]}`),
	)
	if !strings.Contains(out, "真正的正文") {
		t.Fatalf("认不出的帧不该影响后面的内容: %s", out)
	}
	requireNote(t, s.Notes(), NoteSkippedChunk)
}

func TestOpenAIToAnthropicStreamReasoning(t *testing.T) {
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"deepseek","choices":[{"index":0,"delta":{"reasoning_content":"想一下"},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{"reasoning":"再想"},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{"content":"答案"},"finish_reason":"stop"}]}`),
	)
	if strings.Contains(out, "想一下") || strings.Contains(out, "再想") {
		t.Fatalf("思维链没有对应块，不该进内容: %s", out)
	}
	requireNote(t, s.Notes(), NoteDroppedReasoning)
}

func TestOpenAIToAnthropicStreamError(t *testing.T) {
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"开始"},"finish_reason":null}]}`),
		openAIChunk(t, `{"error":{"type":"server_error","message":"上游炸了"}}`),
	)

	if !strings.Contains(out, "event: error") || !strings.Contains(out, "上游炸了") {
		t.Fatalf("流中间的错误要原样给客户端: %s", out)
	}
	// 报错之后再来的收尾不该把半截回答说成完整的
	if strings.Contains(out, `"type":"message_stop"`) {
		t.Fatalf("报错后不该再补收尾事件: %s", out)
	}
}

func TestOpenAIToAnthropicStreamUsageOnlyChunk(t *testing.T) {
	// stream_options.include_usage 下最后会来一条只有 usage 的帧
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`),
		openAIChunk(t, `{"id":"c1","model":"m","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`),
	)
	if !strings.Contains(out, `"input_tokens":5`) {
		t.Fatalf("只有用量的帧也要吃下: %s", out)
	}
}

func TestOpenAIToAnthropicStreamContentFilterAndEmptyArgs(t *testing.T) {
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f"}}]},"finish_reason":"content_filter"}]}`),
	)
	if !strings.Contains(out, `"stop_reason":"refusal"`) {
		t.Fatalf("content_filter 应当映射成 refusal: %s", out)
	}
	// 空参数落成空对象而不是空串：空串客户端解不出 JSON
	if !strings.Contains(out, `"partial_json":"{}"`) {
		t.Fatalf("空参数应当落成 {}: %s", out)
	}
	requireNote(t, s.Notes(), NoteContentFiltered)
}

func TestOpenAIToAnthropicStreamToolCallWithoutID(t *testing.T) {
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`),
	)
	// 没有 index 的按 0 号通道算；没有 id 的补一个，否则客户端没法把结果配回来
	if !strings.Contains(out, `"id":"toolu_bridge_0"`) {
		t.Fatalf("缺 id 应当补一个: %s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("tool_calls 应当映射成 tool_use: %s", out)
	}
}

func TestOpenAIToAnthropicStreamTextThenToolCalls(t *testing.T) {
	// 文本块开着的时候来工具调用：得先把文本块关上，块下标才不会重叠
	s := NewOpenAIToAnthropicStream()
	out := feed(t, s,
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"先说一句"},"finish_reason":null}]}`),
		openAIChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`),
	)
	names := eventNames(t, out)
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("文本块与工具块的次序不对:\n%v", names)
	}
	// 块下标必须递增：0 文本、1 工具
	payloadsOut := payloads(t, out)
	if payloadsOut[4]["index"] != float64(1) {
		t.Fatalf("工具块的下标应当是 1: %v", payloadsOut[4])
	}
}

// toolUseBlocks 取出输出里所有 content_block_start 的 tool_use 块。
func toolUseBlocks(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range events(t, raw) {
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.data), &payload); err != nil {
			continue
		}
		if payload["type"] != "content_block_start" {
			continue
		}
		block, ok := payload["content_block"].(map[string]any)
		if ok && block["type"] == "tool_use" {
			out = append(out, block)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Anthropic → OpenAI
// ---------------------------------------------------------------------------

func TestAnthropicToOpenAIStreamText(t *testing.T) {
	s := NewAnthropicToOpenAIStream()
	// 故意把一条事件劈成两半喂进去
	out := feed(t, s,
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":6,\"output_tokens\":1,\"cache_read_input_tokens\":4}}}\n\n"[:90],
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":6,\"output_tokens\":1,\"cache_read_input_tokens\":4}}}\n\n"[90:],
		anthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好"}}`),
		anthropicEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		anthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`),
		anthropicEvent("message_stop", `{"type":"message_stop"}`),
	)

	chunks := payloads(t, out)
	if chunks[0]["object"] != "chat.completion.chunk" {
		t.Fatalf("第一条 chunk 不对: %v", chunks[0])
	}
	if chunks[0]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["role"] != "assistant" {
		t.Fatalf("第一条 chunk 应当带 role: %v", chunks[0])
	}
	if chunks[1]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["content"] != "你" {
		t.Fatalf("文本增量不对: %v", chunks[1])
	}
	last := chunks[len(chunks)-1]
	lastChoice := last["choices"].([]any)[0].(map[string]any)
	if lastChoice["finish_reason"] != "stop" {
		t.Fatalf("收尾 chunk 的 finish_reason 不对: %v", lastChoice)
	}
	// 用量进收尾 chunk：llmio 的 OpenAI 用量统计只从这一条里取
	usage := last["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(10) || usage["completion_tokens"] != float64(2) {
		t.Fatalf("用量没进收尾 chunk: %v", usage)
	}
	if !strings.HasSuffix(out, "data: [DONE]\n\n") {
		t.Fatalf("结尾必须是 [DONE]: %q", out)
	}
	if len(s.Notes()) != 0 {
		t.Fatalf("不该有 Note: %v", s.Notes())
	}
}

func TestAnthropicToOpenAIStreamToolUse(t *testing.T) {
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude","usage":{"input_tokens":6,"output_tokens":1}}}`),
		anthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"bj\"}"}}`),
		anthropicEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		anthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`),
	)

	chunks := payloads(t, out)
	start := chunks[1]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if start["id"] != "toolu_1" || start["type"] != "function" || start["index"] != float64(0) {
		t.Fatalf("tool_calls 的开头片段不对: %v", start)
	}
	if start["function"].(map[string]any)["name"] != "get_weather" {
		t.Fatalf("工具名没带过去: %v", start)
	}
	// 参数片段各走各的 chunk，客户端自己拼
	frag := chunks[2]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if frag["function"].(map[string]any)["arguments"] != `{"city":` {
		t.Fatalf("参数片段不对: %v", frag)
	}
	if _, ok := frag["id"]; ok {
		t.Fatalf("后续片段不该重复带 id: %v", frag)
	}
	last := chunks[len(chunks)-1]
	if last["choices"].([]any)[0].(map[string]any)["finish_reason"] != "tool_calls" {
		t.Fatalf("tool_use 应当映射成 tool_calls: %v", last)
	}
}

func TestAnthropicToOpenAIStreamTwoTools(t *testing.T) {
	// 两路工具、中间夹一个文本块：内容块下标是 0/1/2，而 OpenAI 的 tool_calls 下标必须
	// 从 0 起连续编号。直接把内容块下标当 tool_calls 下标用，客户端会把两路工具当成
	// 一路的第 0 与第 2 个，第二路的参数就落进空档里丢了
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude"}}`),
		anthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_a","name":"f"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}`),
		anthropicEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		anthropicEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"中间"}}`),
		anthropicEvent("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_b","name":"g"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"b\":2}"}}`),
		anthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`),
	)

	frags := toolCallFragments(t, out)
	var starts []map[string]any
	args := map[int]string{}
	for _, f := range frags {
		fn := f["function"].(map[string]any)
		if fn["name"] != nil {
			starts = append(starts, f)
		}
		if s, ok := fn["arguments"].(string); ok {
			args[int(f["index"].(float64))] += s
		}
	}
	if len(starts) != 2 {
		t.Fatalf("应当有两路工具调用: %v", starts)
	}
	if starts[0]["index"] != float64(0) || starts[0]["id"] != "toolu_a" {
		t.Fatalf("第一路的下标/ID 不对: %v", starts[0])
	}
	if starts[1]["index"] != float64(1) || starts[1]["id"] != "toolu_b" {
		t.Fatalf("第二路应当接在 1 号通道上: %v", starts[1])
	}
	if args[0] != `{"a":1}` || args[1] != `{"b":2}` {
		t.Fatalf("参数片段串了通道: %v", args)
	}
}

// toolCallFragments 取出输出里所有 tool_calls 片段。
func toolCallFragments(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, payload := range payloads(t, raw) {
		choices, _ := payload["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
		calls, _ := delta["tool_calls"].([]any)
		for _, raw := range calls {
			out = append(out, raw.(map[string]any))
		}
	}
	return out
}

func TestAnthropicToOpenAIStreamThinking(t *testing.T) {
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude"}}`),
		anthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"心里话"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`),
		anthropicEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"答案"}}`),
		anthropicEvent("message_stop", `{"type":"message_stop"}`),
	)

	if strings.Contains(out, "心里话") {
		t.Fatalf("思考内容不该进 OpenAI 的输出: %s", out)
	}
	if !strings.Contains(out, "答案") {
		t.Fatalf("正文丢了: %s", out)
	}
	requireNote(t, s.Notes(), NoteDroppedThinking)
}

func TestAnthropicToOpenAIStreamPingAndUnknown(t *testing.T) {
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude"}}`),
		anthropicEvent("ping", `{"type":"ping"}`),
		anthropicEvent("something_new", `{"type":"something_new","data":"x"}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"weird_delta"}}`),
		anthropicEvent("message_stop", `{"type":"message_stop"}`),
	)

	if strings.Contains(out, "something_new") {
		t.Fatalf("认不出的事件不该透传: %s", out)
	}
	requireNote(t, s.Notes(), NoteDroppedUnknownEvent)
}

func TestAnthropicToOpenAIStreamError(t *testing.T) {
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude"}}`),
		anthropicEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"忙"}}`),
	)
	if !strings.Contains(out, `{"error":`) || !strings.Contains(out, "忙") {
		t.Fatalf("错误事件要转成 OpenAI 的错误体: %s", out)
	}
	if strings.Contains(out, "[DONE]") {
		t.Fatalf("报错后不该再发 [DONE]: %s", out)
	}
}

func TestAnthropicToOpenAIStreamCloseWithoutMessageStop(t *testing.T) {
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude"}}`),
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"半截"}}`),
	)
	if !strings.Contains(out, "[DONE]") || !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatalf("上游断流时也要补收尾: %s", out)
	}
	again, _ := s.Close()
	if len(again) != 0 {
		t.Fatalf("重复 Close 不该再发: %q", again)
	}
}

func TestAnthropicToOpenAIStreamTruncatedTail(t *testing.T) {
	// 上游断在事件中间：手里那半条要在 Close 里被认出来
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"尾\"}}")
	if !strings.Contains(out, `"content":"尾"`) {
		t.Fatalf("Close 应当认出手里那半条: %s", out)
	}
}

func TestAnthropicToOpenAIStreamEventNameFallback(t *testing.T) {
	// 有的中转站只写 event 名，data 里没有 type 字段：以 event 名为准，否则整条被丢掉
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `{"message":{"id":"msg_1","model":"claude"}}`),
		anthropicEvent("content_block_delta", `{"index":0,"delta":{"type":"text_delta","text":"按名字认的"}}`),
	)
	if !strings.Contains(out, "按名字认的") {
		t.Fatalf("没有 type 字段时应当用 event 名: %s", out)
	}
}

// errStub 是造出来的翻译层错误，真实的两个实现都不报错。
var errStub = errors.New("stub stream failed")

// stubStream 用来把 Streamer 的报错路径逼出来：真实的两个实现都不报错。
type stubStream struct {
	writeOut []byte
	writeErr error
	closeOut []byte
	closeErr error
	notes    []Note
}

func (s *stubStream) Write([]byte) ([]byte, error) { return s.writeOut, s.writeErr }
func (s *stubStream) Close() ([]byte, error)       { return s.closeOut, s.closeErr }
func (s *stubStream) Notes() []Note                { return s.notes }

// sourceThenError 先给一段数据，再报错——上游读到一半断掉的样子。
type sourceThenError struct {
	data []byte
	err  error
}

func (r *sourceThenError) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		// 与最后一段数据一起报错：不少实现就是这么返回的（读到对端断开时，
		// 缓冲区里已有的字节连同错误一起交出来）
		return n, r.err
	}
	return n, nil
}

func (r *sourceThenError) Close() error { return nil }

func TestAnthropicToOpenAIStreamWithoutMessageStart(t *testing.T) {
	// 没有 message_start 就直接来增量：也得先把带 role 的第一条 chunk 补上
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"迟到"}}`),
	)
	chunks := payloads(t, out)
	if chunks[0]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["role"] != "assistant" {
		t.Fatalf("应当补发带 role 的第一条: %v", chunks[0])
	}
	// 没有 id 时兜底
	if chunks[0]["id"] != "chatcmpl-bridge" {
		t.Fatalf("缺 id 的兜底不对: %v", chunks[0]["id"])
	}
}

func TestAnthropicToOpenAIStreamUnknownPayload(t *testing.T) {
	s := NewAnthropicToOpenAIStream()
	out := feed(t, s,
		anthropicEvent("message_start", `not json`),
		anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude"}}`),
		anthropicEvent("message_stop", `{"type":"message_stop"}`),
	)
	if !strings.Contains(out, "[DONE]") {
		t.Fatalf("认不出的载荷不该影响收尾: %s", out)
	}
	requireNote(t, s.Notes(), NoteSkippedChunk)
}

func TestAnthropicToOpenAIStreamPauseTurnAndUnknownStop(t *testing.T) {
	for _, tc := range []struct {
		stop     string
		want     string
		wantNote Note
	}{
		{stop: "pause_turn", want: "stop", wantNote: NotePausedTurn},
		{stop: "brand_new", want: "stop", wantNote: NoteUnknownFinishReason},
		// 与 o2a 方向对称：换档位名同样要留痕，两个方向漏哪一边都会让同一件事只有
		// 一半的请求能查出原因
		{stop: "refusal", want: "content_filter", wantNote: NoteContentFiltered},
	} {
		t.Run(tc.stop, func(t *testing.T) {
			s := NewAnthropicToOpenAIStream()
			out := feed(t, s,
				anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude"}}`),
				anthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+tc.stop+`"},"usage":{"output_tokens":1,"input_tokens":7}}`),
			)
			if !strings.Contains(out, `"finish_reason":"`+tc.want+`"`) {
				t.Fatalf("finish_reason 应为 %q: %s", tc.want, out)
			}
			// message_delta 里补上的输入用量也要吃下
			if tc.stop == "pause_turn" && !strings.Contains(out, `"prompt_tokens":7`) {
				t.Fatalf("收尾事件里的输入用量没吃下: %s", out)
			}
			requireNote(t, s.Notes(), tc.wantNote)
		})
	}
}

// ---------------------------------------------------------------------------
// 接到转发路径上
// ---------------------------------------------------------------------------

func TestBridgedBody(t *testing.T) {
	// 上游一次只吐几个字节：适配层必须把半截事件攒住，且不能吞掉收尾事件
	upstream := &chunkedReader{data: []byte(
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":"stop"}]}`) +
			openAIChunk(t, "[DONE]"),
	), step: 7}
	body := NewBridgedBody(io.NopCloser(upstream), NewOpenAIToAnthropicStream())

	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	out := string(got)
	if !strings.Contains(out, `"type":"message_start"`) || !strings.Contains(out, `"text":"你好"`) || !strings.Contains(out, `"type":"message_stop"`) {
		t.Fatalf("翻译后的输出不完整:\n%s", out)
	}
	if len(body.Notes()) != 0 {
		t.Fatalf("不该有 Note: %v", body.Notes())
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
}

func TestBridgedBodyNoTrailingEvents(t *testing.T) {
	// 上游正常收尾（已经发过 message_delta/message_stop）时，Close 不该重复发
	upstream := &chunkedReader{data: []byte(
		openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`),
	), step: 4096}
	body := NewBridgedBody(io.NopCloser(upstream), NewOpenAIToAnthropicStream())
	out, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if count := strings.Count(string(out), `"type":"message_stop"`); count != 1 {
		t.Fatalf("message_stop 应当只有一条，实际 %d:\n%s", count, out)
	}
}

func TestBridgedBodyReadError(t *testing.T) {
	body := NewBridgedBody(io.NopCloser(&failingReader{}), NewOpenAIToAnthropicStream())
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("上游读挂了应当把错误透出去")
	}
}

func TestBridgedBodyReadErrorMidStream(t *testing.T) {
	// 读挂之前已经有内容翻好了：先把那几个字节发出去，下一轮再把错误甩出来。
	// 顺序反了的话客户端会先看到错误、再收到内容
	body := NewBridgedBody(
		io.NopCloser(&sourceThenError{
			data: []byte(openAIChunk(t, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"先到这儿"},"finish_reason":null}]}`)),
			err:  io.ErrUnexpectedEOF,
		}),
		NewOpenAIToAnthropicStream(),
	)
	out, err := io.ReadAll(body)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("错误应当透出来: %v", err)
	}
	if !strings.Contains(string(out), "先到这儿") {
		t.Fatalf("出错前翻好的内容不该丢: %s", out)
	}
	// 断流不该补收尾：补了等于说"模型一句话没说就答完了"
	if strings.Contains(string(out), `"type":"message_stop"`) {
		t.Fatalf("读挂时不该补收尾事件: %s", out)
	}
}

func TestBridgedBodyStreamErrors(t *testing.T) {
	t.Run("翻译时报错", func(t *testing.T) {
		body := NewBridgedBody(io.NopCloser(strings.NewReader("data: x\n\n")), &stubStream{writeErr: errStub})
		if _, err := io.ReadAll(body); err != errStub {
			t.Fatalf("Write 的错误应当透出来: %v", err)
		}
	})

	t.Run("收尾时报错", func(t *testing.T) {
		body := NewBridgedBody(io.NopCloser(strings.NewReader("")), &stubStream{closeErr: errStub})
		if _, err := io.ReadAll(body); err != errStub {
			t.Fatalf("Close 的错误应当透出来: %v", err)
		}
	})

	t.Run("收尾没有内容就干净结束", func(t *testing.T) {
		body := NewBridgedBody(io.NopCloser(strings.NewReader("")), &stubStream{})
		out, err := io.ReadAll(body)
		if err != nil || len(out) != 0 {
			t.Fatalf("空流应当干净地结束: %q %v", out, err)
		}
	})

	t.Run("Notes 透传", func(t *testing.T) {
		body := NewBridgedBody(io.NopCloser(strings.NewReader("")), &stubStream{notes: []Note{NoteSkippedChunk}})
		if got := body.Notes(); len(got) != 1 || got[0] != NoteSkippedChunk {
			t.Fatalf("Notes 应当透传: %v", got)
		}
	})
}

// chunkedReader 每次只给 step 个字节，模拟上游的分片。
type chunkedReader struct {
	data []byte
	step int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.step
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func (r *chunkedReader) Close() error { return nil }

// failingReader 一读就报错。
type failingReader struct{}

func (r *failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (r *failingReader) Close() error             { return nil }
