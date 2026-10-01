package bridge

import (
	"testing"
)

// 响应侧互转的测试。
//
// 与请求侧最大的不同：这里**没有拒绝这条退路**——字已经生成出来了，翻不过去的地方
// 只能带上并记 Note。因此每个用例除了看转出来的形状，还要看"丢的东西有没有留下记号"。

func toAnthropicResponse(t *testing.T, body string) (map[string]any, []Note) {
	t.Helper()
	out, notes, err := OpenAIResponseToAnthropic([]byte(body))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	return decode(t, out), notes
}

func toOpenAIResponse(t *testing.T, body string) (map[string]any, []Note) {
	t.Helper()
	out, notes, err := AnthropicResponseToOpenAI([]byte(body))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	return decode(t, out), notes
}

func choice(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	choices, ok := out["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("结果里没有 choices: %v", out)
	}
	return choices[0].(map[string]any)
}

func contentBlocks(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["content"].([]any)
	if !ok {
		t.Fatalf("结果里没有 content 数组: %v", out)
	}
	blocks := make([]map[string]any, 0, len(raw))
	for _, b := range raw {
		blocks = append(blocks, b.(map[string]any))
	}
	return blocks
}

// ---------------------------------------------------------------------------
// OpenAI → Anthropic
// ---------------------------------------------------------------------------

func TestOpenAIResponseToAnthropicText(t *testing.T) {
	out, notes := toAnthropicResponse(t, `{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "你好"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13,
			"prompt_tokens_details": {"cached_tokens": 4}}
	}`)

	if out["type"] != "message" || out["role"] != "assistant" {
		t.Fatalf("信封字段不对: %v", out)
	}
	if out["id"] != "msg_abc" {
		t.Fatalf("id 应当换掉 OpenAI 的前缀: %v", out["id"])
	}
	if out["stop_reason"] != "end_turn" || out["model"] != "gpt-4o" {
		t.Fatalf("收尾字段不对: %v", out)
	}
	if got := contentBlocks(t, out); len(got) != 1 || got[0]["type"] != "text" || got[0]["text"] != "你好" {
		t.Fatalf("正文块不对: %v", got)
	}
	// Anthropic 的 input_tokens 不含缓存命中，要把命中数拆出来单列，
	// 否则 llmio 记成本时会把缓存价按全价算
	usage := out["usage"].(map[string]any)
	if usage["input_tokens"] != float64(6) || usage["output_tokens"] != float64(3) || usage["cache_read_input_tokens"] != float64(4) {
		t.Fatalf("用量没拆对: %v", usage)
	}
	if len(notes) != 0 {
		t.Fatalf("这次不该有 Note: %v", notes)
	}
}

func TestOpenAIResponseToAnthropicMissingUsage(t *testing.T) {
	out, _ := toAnthropicResponse(t, `{
		"id": "x", "model": "m",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}, "finish_reason": "stop"}]
	}`)
	if _, ok := out["usage"]; ok {
		t.Fatalf("上游没给用量就不该编一个: %v", out["usage"])
	}
}

func TestOpenAIResponseToAnthropicIDs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"chatcmpl-abc", "msg_abc"},
		{"msg_xyz", "msg_xyz"},
		{"plain", "msg_plain"},
		{"", "msg_bridge"},
	}
	for _, tc := range cases {
		if got := anthropicMessageID(tc.in); got != tc.want {
			t.Fatalf("anthropicMessageID(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
	if got := openAIChatID(""); got != "chatcmpl-bridge" {
		t.Fatalf("空 id 的兜底不对: %q", got)
	}
	if got := openAIChatID("msg_01XY"); got != "chatcmpl-01XY" {
		t.Fatalf("id 前缀换算不对: %q", got)
	}
}

func TestOpenAIResponseToAnthropicToolCalls(t *testing.T) {
	out, notes := toAnthropicResponse(t, `{
		"id": "chatcmpl-1", "model": "m",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": null, "tool_calls": [
			{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"bj\"}"}},
			{"function": {"name": "noop", "arguments": "{}"}}
		]}, "finish_reason": "tool_calls"}]
	}`)

	if out["stop_reason"] != "tool_use" {
		t.Fatalf("finish_reason 应当映射成 tool_use: %v", out["stop_reason"])
	}
	got := contentBlocks(t, out)
	if len(got) != 2 || got[0]["type"] != "tool_use" {
		t.Fatalf("工具调用没转成 tool_use: %v", got)
	}
	if got[0]["id"] != "call_1" || got[0]["input"].(map[string]any)["city"] != "bj" {
		t.Fatalf("tool_use 的 id/input 不对: %v", got[0])
	}
	// 没给 id 的调用要补一个：客户端要拿 id 把结果配回来
	if got[1]["id"] != "toolu_bridge_1" {
		t.Fatalf("缺 id 的调用应当补一个: %v", got[1]["id"])
	}
	if len(notes) != 0 {
		t.Fatalf("不该有 Note: %v", notes)
	}
}

func TestOpenAIResponseToAnthropicUnparsableArguments(t *testing.T) {
	// 被 max_tokens 截断的 arguments 是常见的坏形状。请求侧遇到会拒绝；响应侧拒绝了
	// 等于把整次生成丢掉，只能落空 input 并留下记号
	out, notes := toAnthropicResponse(t, `{
		"id": "x", "model": "m",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "接着", "tool_calls": [
			{"id": "call_1", "function": {"name": "f", "arguments": "{\"city\":"}}
		]}, "finish_reason": "length"}]
	}`)

	got := contentBlocks(t, out)
	if got[1]["type"] != "tool_use" || len(got[1]["input"].(map[string]any)) != 0 {
		t.Fatalf("解不出参数应当落成空 input: %v", got[1])
	}
	if out["stop_reason"] != "max_tokens" {
		t.Fatalf("length 应当映射成 max_tokens: %v", out["stop_reason"])
	}
	requireNote(t, notes, NoteUnparsableToolArguments)
}

func TestOpenAIResponseToAnthropicStopReasons(t *testing.T) {
	cases := []struct {
		finish   string
		want     string
		wantNote Note
	}{
		{finish: "", want: "end_turn"},
		{finish: "stop", want: "end_turn"},
		{finish: "length", want: "max_tokens"},
		{finish: "tool_calls", want: "tool_use"},
		{finish: "function_call", want: "tool_use"},
		{finish: "content_filter", want: "refusal", wantNote: NoteContentFiltered},
		{finish: "brand_new_reason", want: "end_turn", wantNote: NoteUnknownFinishReason},
	}
	for _, tc := range cases {
		t.Run(tc.finish, func(t *testing.T) {
			body := `{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":` +
				string(stringJSON(tc.finish)) + `}]}`
			out, notes := toAnthropicResponse(t, body)
			if out["stop_reason"] != tc.want {
				t.Fatalf("stop_reason 应为 %q: %v", tc.want, out["stop_reason"])
			}
			if tc.wantNote != "" {
				requireNote(t, notes, tc.wantNote)
			}
		})
	}
}

func TestOpenAIResponseToAnthropicLooseContent(t *testing.T) {
	t.Run("多条候选只取 index 最小的", func(t *testing.T) {
		out, notes := toAnthropicResponse(t, `{
			"id": "x", "model": "m",
			"choices": [
				{"index": 2, "message": {"role": "assistant", "content": "第三份"}, "finish_reason": "stop"},
				{"index": 0, "message": {"role": "assistant", "content": "第一份"}, "finish_reason": "stop"}
			]
		}`)
		if got := contentBlocks(t, out); got[0]["text"] != "第一份" {
			t.Fatalf("应当取 index 最小的那条而不是数组里第一条: %v", got)
		}
		requireNote(t, notes, NoteDroppedExtraChoices)
	})

	t.Run("块数组里的非文本块丢掉并记账", func(t *testing.T) {
		out, notes := toAnthropicResponse(t, `{
			"id": "x", "model": "m",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": [
				{"type": "text", "text": "看"},
				{"type": "image_url", "image_url": {"url": "https://a/b.png"}}
			]}, "finish_reason": "stop"}]
		}`)
		if got := contentBlocks(t, out); len(got) != 1 || got[0]["text"] != "看" {
			t.Fatalf("应当只剩文本块: %v", got)
		}
		requireNote(t, notes, NoteDroppedUnknownBlock)
	})

	t.Run("content 形状认不出时原样当文本", func(t *testing.T) {
		out, notes := toAnthropicResponse(t, `{
			"id": "x", "model": "m",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": 42}, "finish_reason": "stop"}]
		}`)
		if got := contentBlocks(t, out); got[0]["text"] != "42" {
			t.Fatalf("认不出的形状应当原样带过去: %v", got)
		}
		requireNote(t, notes, NoteUnparsableContent)
	})

	t.Run("推理内容丢掉并记账", func(t *testing.T) {
		// 两套字段名都要认：流式那条路径一直两套都看，非流式少记这一笔的话，
		// 同一个请求走流式与非流式会给出不同的账
		for _, field := range []string{"reasoning_content", "reasoning"} {
			out, notes := toAnthropicResponse(t, `{
				"id": "x", "model": "m",
				"choices": [{"index": 0, "message": {"role": "assistant", "content": "答案", "`+field+`": "心里盘算了一下"}, "finish_reason": "stop"}]
			}`)
			blocks := contentBlocks(t, out)
			if len(blocks) != 1 || blocks[0]["text"] != "答案" {
				t.Fatalf("%s 不该进 content: %v", field, blocks)
			}
			requireNote(t, notes, NoteDroppedReasoning)
		}
	})

	t.Run("没有推理内容就不记这一笔", func(t *testing.T) {
		// 反向用例：记账是"改了什么"，没有可改的就不该有一条
		_, notes := toAnthropicResponse(t, `{
			"id": "x", "model": "m",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "答案"}, "finish_reason": "stop"}]
		}`)
		requireNoNote(t, notes, NoteDroppedReasoning)
	})

	t.Run("refusal 丢掉并记账", func(t *testing.T) {
		out, notes := toAnthropicResponse(t, `{
			"id": "x", "model": "m",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": null, "refusal": "我不能帮这个忙"}, "finish_reason": "stop"}]
		}`)
		if got := contentBlocks(t, out); len(got) != 0 {
			t.Fatalf("拒答文本没有对应块，不该进 content: %v", got)
		}
		requireNote(t, notes, NoteDroppedRefusal)
	})

	t.Run("空内容的文本块不产生", func(t *testing.T) {
		out, _ := toAnthropicResponse(t, `{
			"id": "x", "model": "m",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": ""}, "finish_reason": "stop"}]
		}`)
		if got := contentBlocks(t, out); len(got) != 0 {
			t.Fatalf("空文本不该产生块: %v", got)
		}
	})
}

func TestOpenAIResponseToAnthropicErrors(t *testing.T) {
	t.Run("上游把错误塞在 200 的响应体里", func(t *testing.T) {
		out, notes := toAnthropicResponse(t, `{"error": {"type": "rate_limit_error", "message": "慢一点"}}`)
		if out["type"] != "error" {
			t.Fatalf("应当转成 Anthropic 的 error 形状: %v", out)
		}
		inner := out["error"].(map[string]any)
		if inner["type"] != "rate_limit_error" || inner["message"] != "慢一点" {
			t.Fatalf("错误内容没带过去: %v", inner)
		}
		if len(notes) != 0 {
			t.Fatalf("错误体不算有损改写: %v", notes)
		}
	})

	t.Run("错误体里没有认得的字段", func(t *testing.T) {
		out, _ := toAnthropicResponse(t, `{"error": "上游炸了"}`)
		inner := out["error"].(map[string]any)
		if inner["type"] != "api_error" || inner["message"] == "" {
			t.Fatalf("兜底形状不对: %v", inner)
		}
	})

	t.Run("没有候选", func(t *testing.T) {
		if _, _, err := OpenAIResponseToAnthropic([]byte(`{"id":"x","choices":[]}`)); err == nil {
			t.Fatal("没有候选时应当报错")
		}
	})

	t.Run("不是 JSON", func(t *testing.T) {
		if _, _, err := OpenAIResponseToAnthropic([]byte(`{`)); err == nil {
			t.Fatal("非 JSON 应当报错")
		}
	})
}

// ---------------------------------------------------------------------------
// Anthropic → OpenAI
// ---------------------------------------------------------------------------

func TestAnthropicResponseToOpenAIText(t *testing.T) {
	out, notes := toOpenAIResponse(t, `{
		"id": "msg_01XY",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-5",
		"content": [{"type": "text", "text": "你好"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 6, "output_tokens": 3, "cache_read_input_tokens": 4}
	}`)

	if out["object"] != "chat.completion" || out["id"] != "chatcmpl-01XY" {
		t.Fatalf("信封字段不对: %v", out)
	}
	if out["created"].(float64) <= 0 {
		t.Fatalf("created 必须有值: %v", out["created"])
	}
	c := choice(t, out)
	message := c["message"].(map[string]any)
	if message["role"] != "assistant" || message["content"] != "你好" {
		t.Fatalf("正文不对: %v", message)
	}
	if c["finish_reason"] != "stop" {
		t.Fatalf("stop_reason 应当映射成 stop: %v", c["finish_reason"])
	}
	// 与 llmio 记 Anthropic 用量时同一个口径：缓存命中并进 prompt_tokens
	usage := out["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(10) || usage["completion_tokens"] != float64(3) || usage["total_tokens"] != float64(13) {
		t.Fatalf("用量没算对: %v", usage)
	}
	if usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != float64(4) {
		t.Fatalf("缓存命中没带过去: %v", usage)
	}
	if len(notes) != 0 {
		t.Fatalf("不该有 Note: %v", notes)
	}
}

func TestAnthropicResponseToOpenAIToolUse(t *testing.T) {
	out, notes := toOpenAIResponse(t, `{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
		"content": [
			{"type": "text", "text": "我查一下"},
			{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "bj"}},
			{"type": "tool_use", "name": "noop"}
		],
		"stop_reason": "tool_use"
	}`)

	c := choice(t, out)
	message := c["message"].(map[string]any)
	if message["content"] != "我查一下" {
		t.Fatalf("正文没带过去: %v", message)
	}
	if c["finish_reason"] != "tool_calls" {
		t.Fatalf("stop_reason 应当映射成 tool_calls: %v", c["finish_reason"])
	}
	calls := message["tool_calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("工具调用数不对: %v", calls)
	}
	first := calls[0].(map[string]any)
	if first["id"] != "toolu_1" || first["type"] != "function" {
		t.Fatalf("tool_call 信封不对: %v", first)
	}
	if first["function"].(map[string]any)["arguments"] != `{"city": "bj"}` {
		t.Fatalf("input 应当变成 arguments 字符串: %v", first)
	}
	// 缺 input 的补空对象，并且要留下记号：请求侧会把它原样发回上游
	second := calls[1].(map[string]any)
	if second["function"].(map[string]any)["arguments"] != "{}" {
		t.Fatalf("缺 input 应当落成 {}: %v", second)
	}
	if second["id"] != "call_bridge_2" {
		t.Fatalf("缺 id 应当补一个（按块下标取名，保证同一条响应里不重）: %v", second["id"])
	}
	requireNote(t, notes, NoteUnparsableToolArguments)
}

func TestAnthropicResponseToOpenAIDroppedBlocks(t *testing.T) {
	out, notes := toOpenAIResponse(t, `{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
		"content": [
			{"type": "thinking", "thinking": "心里话", "signature": "sig"},
			{"type": "text", "text": "答案"},
			{"type": "redacted_thinking", "data": "xxx"},
			{"type": "server_tool_use", "id": "srv_1"}
		],
		"stop_reason": "end_turn"
	}`)

	message := choice(t, out)["message"].(map[string]any)
	if message["content"] != "答案" {
		t.Fatalf("只剩正文才对: %v", message)
	}
	requireNote(t, notes, NoteDroppedThinking)
	requireNote(t, notes, NoteDroppedUnknownBlock)
}

func TestAnthropicResponseToOpenAIStopReasons(t *testing.T) {
	cases := []struct {
		stop     string
		want     string
		wantNote Note
	}{
		{stop: "", want: "stop"},
		{stop: "end_turn", want: "stop"},
		{stop: "stop_sequence", want: "stop"},
		{stop: "max_tokens", want: "length"},
		{stop: "tool_use", want: "tool_calls"},
		{stop: "refusal", want: "content_filter", wantNote: NoteContentFiltered},
		{stop: "pause_turn", want: "stop", wantNote: NotePausedTurn},
		{stop: "brand_new_reason", want: "stop", wantNote: NoteUnknownFinishReason},
	}
	for _, tc := range cases {
		t.Run(tc.stop, func(t *testing.T) {
			body := `{"id":"m","type":"message","role":"assistant","model":"x","content":[{"type":"text","text":"hi"}],"stop_reason":` +
				string(stringJSON(tc.stop)) + `}`
			out, notes := toOpenAIResponse(t, body)
			if choice(t, out)["finish_reason"] != tc.want {
				t.Fatalf("finish_reason 应为 %q: %v", tc.want, choice(t, out)["finish_reason"])
			}
			if tc.wantNote != "" {
				requireNote(t, notes, tc.wantNote)
			}
		})
	}
}

func TestAnthropicResponseToOpenAINormalStopNeedsNoFilterNote(t *testing.T) {
	// 反向用例：这一笔是"换了档位名"的记号，正常收尾不许有。两个方向都要看——
	// o2a 方向原本只在非流式路径上记，a2o 方向则一笔都不记，正是这种"两头各记一半"
	// 的写法让 content_filter 漏了账
	out, notes := toOpenAIResponse(t, `{"id":"m","type":"message","role":"assistant","model":"x",
		"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`)
	if choice(t, out)["finish_reason"] != "stop" {
		t.Fatalf("正常收尾应当映射成 stop: %v", choice(t, out)["finish_reason"])
	}
	requireNoNote(t, notes, NoteContentFiltered)

	_, notes = toAnthropicResponse(t, `{"id":"x","model":"m",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	requireNoNote(t, notes, NoteContentFiltered)
}

func TestAnthropicResponseToOpenAIEmpty(t *testing.T) {
	t.Run("没有内容时不给 content 字段", func(t *testing.T) {
		out, _ := toOpenAIResponse(t, `{"id":"m","type":"message","role":"assistant","model":"x","content":[],"stop_reason":"end_turn"}`)
		message := choice(t, out)["message"].(map[string]any)
		if _, ok := message["content"]; ok {
			t.Fatalf("空内容不该给 content: %v", message)
		}
	})

	t.Run("没有用量时不给 usage", func(t *testing.T) {
		out, _ := toOpenAIResponse(t, `{"id":"m","type":"message","role":"assistant","model":"x","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`)
		if _, ok := out["usage"]; ok {
			t.Fatalf("上游没给用量就不该编一个: %v", out["usage"])
		}
	})

	t.Run("error 类型", func(t *testing.T) {
		out, _ := toOpenAIResponse(t, `{"type":"error","error":{"type":"overloaded_error","message":"忙"}}`)
		inner := out["error"].(map[string]any)
		if inner["message"] != "忙" || inner["code"] != "overloaded_error" || inner["type"] != "api_error" {
			t.Fatalf("错误体转错了: %v", inner)
		}
	})

	t.Run("error 类型但没有 error 字段", func(t *testing.T) {
		out, _ := toOpenAIResponse(t, `{"type":"error"}`)
		inner := out["error"].(map[string]any)
		if inner["message"] == "" {
			t.Fatalf("至少要留下可读的正文: %v", inner)
		}
	})

	t.Run("不是 JSON", func(t *testing.T) {
		if _, _, err := AnthropicResponseToOpenAI([]byte(`{`)); err == nil {
			t.Fatal("非 JSON 应当报错")
		}
	})
}

// ---------------------------------------------------------------------------
// 共用小工具
// ---------------------------------------------------------------------------

func TestAnthropicUsageClamp(t *testing.T) {
	// 有的中转站把缓存命中数与 prompt_tokens 各算各的，命中数可能更大。给出负数的话
	// 成本计算会往下倒扣，夹到 0
	got := anthropicUsage(&OpenAIUsage{
		PromptTokens:     2,
		CompletionTokens: 1,
		PromptTokensDetails: &struct {
			CachedTokens int64 `json:"cached_tokens"`
		}{CachedTokens: 5},
	})
	if got.InputTokens != 0 || got.CacheReadInputTokens != 5 || got.OutputTokens != 1 {
		t.Fatalf("负数应当夹到 0: %+v", got)
	}
}

func TestTextOfParts(t *testing.T) {
	// 响应侧与请求侧不同：这些块是模型刚吐出来的，无从拒绝，只能跳过非文本块
	got := textOfParts([]OpenAIContentPart{
		{Type: "text", Text: "前"},
		{Type: "image_url", ImageURL: &OpenAIImageURL{URL: "https://a/b.png"}},
		{Type: "input_text", Text: "后"},
	})
	if got != "前后" {
		t.Fatalf("拼接结果不对: %q", got)
	}
}
