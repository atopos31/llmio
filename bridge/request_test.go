package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

// 请求互转的测试。
//
// 这一层要钉的不是"能转"，而是**转错了会不会被发现**：工具调用参数有没有丢、
// tool_use 与 tool_result 有没有配上对、表达不了的东西是拒绝还是悄悄丢掉。因此
// 每个用例都同时看两样东西：转换结果，以及 []Note（有损改写的记录）。

// ---------------------------------------------------------------------------
// 脚手架
// ---------------------------------------------------------------------------

func toAnthropic(t *testing.T, body string, opt Options) (map[string]any, []Note) {
	t.Helper()
	out, notes, err := OpenAIRequestToAnthropic([]byte(body), opt)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	return decode(t, out), notes
}

func toOpenAI(t *testing.T, body string) (map[string]any, []Note) {
	t.Helper()
	out, notes, err := AnthropicRequestToOpenAI([]byte(body), Options{})
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	return decode(t, out), notes
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("结果不是合法 JSON: %v\n%s", err, raw)
	}
	return out
}

// rejected 断言这份请求被**拒绝**（而不是转换成功）。
func rejected(t *testing.T, body string, wantField string) {
	t.Helper()
	_, _, err := OpenAIRequestToAnthropic([]byte(body), Options{})
	assertUnsupported(t, err, wantField)
}

func rejectedReverse(t *testing.T, body string, wantField string) {
	t.Helper()
	_, _, err := AnthropicRequestToOpenAI([]byte(body), Options{})
	assertUnsupported(t, err, wantField)
}

func assertUnsupported(t *testing.T, err error, wantField string) {
	t.Helper()
	if err == nil {
		t.Fatal("期望被拒绝，但转换成功了")
	}
	var unsupported *Unsupported
	if !asUnsupported(err, &unsupported) {
		t.Fatalf("期望 *Unsupported，得到 %T: %v", err, err)
	}
	if unsupported.Field != wantField {
		t.Fatalf("拒绝的字段应为 %q，实际 %q（%s）", wantField, unsupported.Field, err)
	}
	if !strings.Contains(err.Error(), "cannot bridge") {
		t.Fatalf("错误文本应说明是桥接的问题: %v", err)
	}
}

func asUnsupported(err error, target **Unsupported) bool {
	u, ok := err.(*Unsupported)
	if ok {
		*target = u
	}
	return ok
}

func hasNote(notes []Note, want Note) bool {
	for _, n := range notes {
		if n == want {
			return true
		}
	}
	return false
}

func requireNote(t *testing.T, notes []Note, want Note) {
	t.Helper()
	if !hasNote(notes, want) {
		t.Fatalf("缺少 Note %q，实际 %v", want, notes)
	}
}

func requireNoNote(t *testing.T, notes []Note, unwanted Note) {
	t.Helper()
	if hasNote(notes, unwanted) {
		t.Fatalf("不该有 Note %q，实际 %v", unwanted, notes)
	}
}

func TestDedupeNotes(t *testing.T) {
	// 一条都没有时给 nil 而不是空切片：调用方拿它做判空
	if got := dedupeNotes(nil); got != nil {
		t.Fatalf("空输入应当原样给 nil: %v", got)
	}
	// 去重但保持首次出现的次序
	got := dedupeNotes([]Note{NoteDroppedUser, NoteDroppedSeed, NoteDroppedUser})
	if len(got) != 2 || got[0] != NoteDroppedUser || got[1] != NoteDroppedSeed {
		t.Fatalf("去重结果不对: %v", got)
	}
}

// blocks 取出结果里第 index 条消息的 content 数组。
func blocks(t *testing.T, out map[string]any, index int) []map[string]any {
	t.Helper()
	messages, ok := out["messages"].([]any)
	if !ok {
		t.Fatalf("结果里没有 messages: %v", out)
	}
	if index >= len(messages) {
		t.Fatalf("只有 %d 条消息，取不到第 %d 条", len(messages), index)
	}
	msg := messages[index].(map[string]any)
	raw, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("第 %d 条消息的 content 不是数组: %v", index, msg)
	}
	out2 := make([]map[string]any, 0, len(raw))
	for _, b := range raw {
		out2 = append(out2, b.(map[string]any))
	}
	return out2
}

func messageRoles(t *testing.T, out map[string]any) []string {
	t.Helper()
	messages := out["messages"].([]any)
	roles := make([]string, 0, len(messages))
	for _, m := range messages {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	return roles
}

// ---------------------------------------------------------------------------
// OpenAI → Anthropic
// ---------------------------------------------------------------------------

func TestOpenAIToAnthropicBasic(t *testing.T) {
	out, notes := toAnthropic(t, `{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "content": "你是助手"},
			{"role": "system", "content": "请简短"},
			{"role": "developer", "content": "务必用中文"},
			{"role": "user", "content": "你好"}
		],
		"temperature": 0.5,
		"top_p": 0.9,
		"stream": true
	}`, Options{})

	if out["model"] != "gpt-4o" {
		t.Fatalf("model 应原样带过: %v", out["model"])
	}
	// 多条 system 按出现顺序拼接，中间空一行
	if out["system"] != "你是助手\n\n请简短\n\n务必用中文" {
		t.Fatalf("system 拼接不对: %q", out["system"])
	}
	if out["temperature"] != 0.5 || out["top_p"] != 0.9 || out["stream"] != true {
		t.Fatalf("采样参数没了: %v", out)
	}
	// 只有一条 user 消息时，它就是必须补的 max_tokens 之外唯一的 Note 来源
	requireNote(t, notes, NoteDefaultedMaxTokens)
	if got := blocks(t, out, 0); got[0]["type"] != "text" || got[0]["text"] != "你好" {
		t.Fatalf("user 文本转错了: %v", got)
	}
}

func TestOpenAIToAnthropicEmptySystemDropped(t *testing.T) {
	out, _ := toAnthropic(t, `{
		"messages": [
			{"role": "system", "content": "   "},
			{"role": "user", "content": "hi"}
		]
	}`, Options{})

	// Anthropic 拒收空的 system：空白 system 应当整个不出现
	if _, ok := out["system"]; ok {
		t.Fatalf("空白 system 不该出现: %v", out["system"])
	}
}

func TestOpenAIToAnthropicSystemAsParts(t *testing.T) {
	out, _ := toAnthropic(t, `{
		"messages": [
			{"role": "system", "content": [{"type": "text", "text": "第一段"}, {"type": "text", "text": "第二段"}]},
			{"role": "user", "content": "hi"}
		]
	}`, Options{})

	if out["system"] != "第一段第二段" {
		t.Fatalf("块数组形式的 system 没拼对: %q", out["system"])
	}
}

func TestOpenAIToAnthropicMaxTokens(t *testing.T) {
	t.Run("请求带上就用请求的", func(t *testing.T) {
		out, notes := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"max_tokens":123}`, Options{})
		if out["max_tokens"] != float64(123) {
			t.Fatalf("max_tokens 应为 123: %v", out["max_tokens"])
		}
		requireNoNote(t, notes, NoteDefaultedMaxTokens)
	})

	t.Run("max_completion_tokens 是新名字，优先于 max_tokens", func(t *testing.T) {
		out, _ := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"max_tokens":123,"max_completion_tokens":456}`, Options{})
		if out["max_tokens"] != float64(456) {
			t.Fatalf("应取 max_completion_tokens: %v", out["max_tokens"])
		}
	})

	t.Run("都不带时补默认值并记账", func(t *testing.T) {
		out, notes := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}]}`, Options{})
		if out["max_tokens"] != float64(DefaultMaxTokens) {
			t.Fatalf("应落默认值 %d: %v", DefaultMaxTokens, out["max_tokens"])
		}
		requireNote(t, notes, NoteDefaultedMaxTokens)
	})

	t.Run("默认值可由调用方覆盖", func(t *testing.T) {
		out, _ := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}]}`, Options{MaxTokens: 999})
		if out["max_tokens"] != float64(999) {
			t.Fatalf("Options.MaxTokens 没生效: %v", out["max_tokens"])
		}
	})

	t.Run("显式给 0 等于没给", func(t *testing.T) {
		out, notes := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"max_tokens":0}`, Options{})
		if out["max_tokens"] != float64(DefaultMaxTokens) {
			t.Fatalf("0 应当落默认值: %v", out["max_tokens"])
		}
		requireNote(t, notes, NoteDefaultedMaxTokens)
	})
}

func TestOpenAIToAnthropicStop(t *testing.T) {
	t.Run("数组", func(t *testing.T) {
		out, _ := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"stop":["a","b"]}`, Options{})
		seqs := out["stop_sequences"].([]any)
		if len(seqs) != 2 || seqs[0] != "a" {
			t.Fatalf("stop_sequences 转错了: %v", seqs)
		}
	})

	t.Run("单个字符串", func(t *testing.T) {
		out, _ := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"stop":"###"}`, Options{})
		seqs := out["stop_sequences"].([]any)
		if len(seqs) != 1 || seqs[0] != "###" {
			t.Fatalf("单个 stop 转错了: %v", seqs)
		}
	})

	t.Run("空数组省略", func(t *testing.T) {
		out, _ := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"stop":[]}`, Options{})
		if _, ok := out["stop_sequences"]; ok {
			t.Fatalf("空 stop 不该出现: %v", out["stop_sequences"])
		}
	})

	t.Run("空字符串等于没给", func(t *testing.T) {
		out, _ := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"stop":""}`, Options{})
		if _, ok := out["stop_sequences"]; ok {
			t.Fatalf("空串 stop 不该出现: %v", out["stop_sequences"])
		}
	})

	t.Run("认不出的形状当没给", func(t *testing.T) {
		out, _ := toAnthropic(t, `{"messages":[{"role":"user","content":"hi"}],"stop":42}`, Options{})
		if _, ok := out["stop_sequences"]; ok {
			t.Fatalf("数字 stop 不该出现: %v", out["stop_sequences"])
		}
	})
}

func TestOpenAIToAnthropicRejectsObservableLoss(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"n>1", `{"messages":[{"role":"user","content":"hi"}],"n":4}`, "n"},
		{"presence_penalty", `{"messages":[{"role":"user","content":"hi"}],"presence_penalty":0.5}`, "presence_penalty"},
		{"frequency_penalty", `{"messages":[{"role":"user","content":"hi"}],"frequency_penalty":-0.5}`, "frequency_penalty"},
		{"logprobs", `{"messages":[{"role":"user","content":"hi"}],"logprobs":true}`, "logprobs"},
		{"top_logprobs", `{"messages":[{"role":"user","content":"hi"}],"top_logprobs":3}`, "top_logprobs"},
		{"response_format", `{"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`, "response_format"},
		{"json_schema", `{"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema"}}`, "response_format"},
		{"未知 role", `{"messages":[{"role":"user","content":"hi"},{"role":"critic","content":"x"}]}`, "messages[1].role"},
		{"未知 content 块", `{"messages":[{"role":"user","content":[{"type":"audio","text":"x"}]}]}`, "messages[0].content[].type"},
		{"助手消息里的图片", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"image_url","image_url":{"url":"https://a/b.png"}}]}]}`, "messages[1].content[].type"},
		{"图片缺 url", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{}}]}]}`, "messages[0].content[].image_url"},
		{"工具类型不是 function", `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search","function":{"name":"f"}}]}`, "tools[0].type"},
		{"工具调用参数不是对象", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"c1","function":{"name":"f","arguments":"[1,2]"}}]}]}`, "messages[1].tool_calls[0].function.arguments"},
		{"工具调用没有 id", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"function":{"name":"f","arguments":"{}"}}]}]}`, "messages[1].tool_calls[0].id"},
		{"历史以助手开头", `{"messages":[{"role":"assistant","content":"我先说一句"}]}`, "messages[0].role"},
		{"没有任何消息", `{"messages":[{"role":"system","content":"只有系统"}]}`, "messages"},
		{"tool_choice 认不出", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":"somehow"}`, "tool_choice"},
		{"tool_choice 形状认不出", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function"}}`, "tool_choice"},
		{"图片源认不出", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"ftp://a/b.png"}}]}]}`, "image_url.url"},
		{"data URL 缺逗号", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64"}}]}]}`, "image_url.url"},
		{"data URL 不是 base64", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png,abc"}}]}]}`, "image_url.url"},
		{"content 既不是字符串也不是数组", `{"messages":[{"role":"user","content":42}]}`, "messages[0].content"},
		{"system 的 content 形状不对", `{"messages":[{"role":"system","content":42},{"role":"user","content":"hi"}]}`, "messages[0].content"},
		{"tool 消息的 content 形状不对", `{"messages":[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"c1","content":42}]}`, "messages[1].content"},
		{"助手消息的 content 形状不对", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":42}]}`, "messages[1].content"},
		{"整份请求不是 JSON", `{`, "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rejected(t, tc.body, tc.field)
		})
	}
}

func TestOpenAIToAnthropicAcceptsHarmlessHints(t *testing.T) {
	// 这些是"响应契约上看不出区别"的：0 值的惩罚项、false 的 logprobs、
	// text 格式，都在等价于没写的范围内，不该把请求拦下
	out, notes := toAnthropic(t, `{
		"messages": [{"role": "user", "content": "hi"}],
		"n": 1,
		"presence_penalty": 0,
		"frequency_penalty": 0,
		"logprobs": false,
		"top_logprobs": 0,
		"response_format": {"type": "text"},
		"seed": 7,
		"user": "u-1"
	}`, Options{})

	if out["model"] != nil && out["model"] != "" {
		t.Fatalf("model 不该被编出来: %v", out["model"])
	}
	requireNote(t, notes, NoteDroppedSeed)
	requireNote(t, notes, NoteDroppedUser)
}

func TestOpenAIToAnthropicTopK(t *testing.T) {
	// top_k 不是 OpenAI 官方参数，但兼容实现普遍照收。Anthropic 有同名的 top_k 且语义一致，
	// 所以这里要**搬过去**：既有实现里它是被静默吞掉的（连字段都没声明），而"能搬却不搬"
	// 比"搬不了所以记一笔"更糟——调用方看不见任何痕迹
	out, notes := toAnthropic(t, `{
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 10,
		"top_k": 40
	}`, Options{})

	if out["top_k"] != float64(40) {
		t.Fatalf("top_k 应当原样搬过去: %v", out["top_k"])
	}
	if len(notes) != 0 {
		t.Fatalf("搬得过去就不该记账: %v", notes)
	}
}

func TestOpenAIToAnthropicTopKAbsentStaysAbsent(t *testing.T) {
	// 反向用例：没写 top_k 就不该凭空造一个 0 出来——Anthropic 的 top_k 是可选参数，
	// 出现一个 0 的意思是"只从概率最高的一个 token 里采"，与"不限制"完全是两回事
	out, _ := toAnthropic(t, `{
		"messages": [{"role": "user", "content": "hi"}]
	}`, Options{})
	if _, ok := out["top_k"]; ok {
		t.Fatalf("没写 top_k 就不该出现: %v", out["top_k"])
	}
}

func TestOpenAIToAnthropicTools(t *testing.T) {
	out, notes := toAnthropic(t, `{
		"messages": [{"role": "user", "content": "天气怎么样"}],
		"tools": [
			{"type": "function", "function": {"name": "get_weather", "description": "查天气", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}},
			{"type": "function", "function": {"name": "noop", "strict": true}}
		]
	}`, Options{})

	tools := out["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("工具数不对: %v", tools)
	}
	first := tools[0].(map[string]any)
	if first["name"] != "get_weather" || first["description"] != "查天气" {
		t.Fatalf("工具名/描述转错了: %v", first)
	}
	if first["input_schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("input_schema 应当原样搬运: %v", first["input_schema"])
	}
	// 没给 parameters 的要补一个空对象 Schema，否则上游直接 400；strict 没有对应物
	second := tools[1].(map[string]any)
	if second["input_schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("缺省 input_schema 没补上: %v", second)
	}
	requireNote(t, notes, NoteDefaultedInputSchema)
	requireNote(t, notes, NoteDroppedToolStrict)
}

func TestOpenAIToAnthropicToolChoice(t *testing.T) {
	cases := []struct {
		name       string
		choice     string
		wantType   string
		wantName   string
		dropTools  bool
		wantReject bool
	}{
		{name: "auto", choice: `"auto"`, wantType: "auto"},
		{name: "required 对应 any", choice: `"required"`, wantType: "any"},
		{name: "none 等于不带工具", choice: `"none"`, dropTools: true},
		{name: "指定函数", choice: `{"type":"function","function":{"name":"get_weather"}}`, wantType: "tool", wantName: "get_weather"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := toAnthropic(t, `{
				"messages": [{"role": "user", "content": "hi"}],
				"tools": [{"type": "function", "function": {"name": "get_weather", "parameters": {"type": "object"}}}],
				"tool_choice": `+tc.choice+`
			}`, Options{})

			if _, ok := out["tools"]; tc.dropTools && ok {
				t.Fatalf("tool_choice:none 应当把 tools 整个丢掉: %v", out["tools"])
			}
			if tc.dropTools {
				return
			}
			if !tc.dropTools {
				if _, ok := out["tools"]; !ok {
					t.Fatal("不该丢掉 tools")
				}
			}
			choice := out["tool_choice"].(map[string]any)
			if choice["type"] != tc.wantType {
				t.Fatalf("tool_choice.type 应为 %q: %v", tc.wantType, choice)
			}
			if tc.wantName != "" && choice["name"] != tc.wantName {
				t.Fatalf("tool_choice.name 应为 %q: %v", tc.wantName, choice)
			}
		})
	}
}

func TestOpenAIToAnthropicImages(t *testing.T) {
	t.Run("base64 data URL", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [{"role": "user", "content": [
				{"type": "text", "text": "看这张图"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,QUJD", "detail": "high"}}
			]}]
		}`, Options{})

		got := blocks(t, out, 0)
		if len(got) != 2 {
			t.Fatalf("应当有文本与图片两块: %v", got)
		}
		source := got[1]["source"].(map[string]any)
		if source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != "QUJD" {
			t.Fatalf("图片源拆错了: %v", source)
		}
		requireNote(t, notes, NoteDroppedImageDetail)
	})

	t.Run("http 链接走 url 源并记账", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [{"role": "user", "content": [{"type": "image_url", "image_url": {"url": "https://a/b.png"}}]}]
		}`, Options{})

		got := blocks(t, out, 0)
		source := got[0]["source"].(map[string]any)
		if source["type"] != "url" || source["url"] != "https://a/b.png" {
			t.Fatalf("url 源转错了: %v", source)
		}
		requireNote(t, notes, NoteImageByURL)
	})
}

func TestOpenAIToAnthropicToolCalls(t *testing.T) {
	out, notes := toAnthropic(t, `{
		"messages": [
			{"role": "user", "content": "北京和上海"},
			{"role": "assistant", "content": null, "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"beijing\"}"}},
				{"id": "call_2", "type": "function", "function": {"name": "get_weather", "arguments": ""}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "晴"},
			{"role": "tool", "tool_call_id": "call_2", "content": "多云"}
		]
	}`, Options{})

	roles := messageRoles(t, out)
	// 两条 tool 消息必须并进**同一条** user 消息：逐条转会被"角色必须交替"拒掉
	if len(roles) != 3 || roles[2] != "user" {
		t.Fatalf("消息形状不对: %v", roles)
	}

	assistant := blocks(t, out, 1)
	if len(assistant) != 2 || assistant[0]["type"] != "tool_use" {
		t.Fatalf("工具调用没转成 tool_use: %v", assistant)
	}
	if assistant[0]["id"] != "call_1" || assistant[0]["name"] != "get_weather" {
		t.Fatalf("tool_use 的 id/name 不对: %v", assistant[0])
	}
	if assistant[0]["input"].(map[string]any)["city"] != "beijing" {
		t.Fatalf("arguments 没解成对象: %v", assistant[0]["input"])
	}
	// 空 arguments 是 OpenAI 表示"无参数"的写法，要落成空对象而不是缺字段
	if empty, ok := assistant[1]["input"].(map[string]any); !ok || len(empty) != 0 {
		t.Fatalf("空 arguments 应转成 {}: %v", assistant[1]["input"])
	}

	results := blocks(t, out, 2)
	if len(results) != 2 || results[0]["type"] != "tool_result" {
		t.Fatalf("工具结果没合并成一条 user 消息: %v", results)
	}
	if results[0]["tool_use_id"] != "call_1" || results[1]["tool_use_id"] != "call_2" {
		t.Fatalf("tool_result 的配对 id 不对: %v", results)
	}
	// 这次没有发生任何有损改写，Note 里只该有补 max_tokens 那一条
	if len(notes) != 1 || notes[0] != NoteDefaultedMaxTokens {
		t.Fatalf("不该有额外的 Note: %v", notes)
	}
}

func TestOpenAIToAnthropicToolPairing(t *testing.T) {
	t.Run("悬空的工具调用补一条失败结果", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "查一下"},
				{"role": "assistant", "tool_calls": [{"id": "call_1", "function": {"name": "f", "arguments": "{}"}}]}
			]
		}`, Options{})

		roles := messageRoles(t, out)
		if len(roles) != 3 || roles[2] != "user" {
			t.Fatalf("应当补一条 user 消息承接工具结果: %v", roles)
		}
		results := blocks(t, out, 2)
		if results[0]["tool_use_id"] != "call_1" {
			t.Fatalf("补的结果没对上 id: %v", results[0])
		}
		// 补的是 is_error，不是编一个成功的结果
		if results[0]["is_error"] != true {
			t.Fatalf("补的结果必须标成失败: %v", results[0])
		}
		requireNote(t, notes, NoteFilledMissingToolResult)
	})

	t.Run("部分结果缺失时只补缺的那条", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "两个都查"},
				{"role": "assistant", "tool_calls": [
					{"id": "call_1", "function": {"name": "f", "arguments": "{}"}},
					{"id": "call_2", "function": {"name": "f", "arguments": "{}"}}
				]},
				{"role": "tool", "tool_call_id": "call_2", "content": "只回了第二个"}
			]
		}`, Options{})

		results := blocks(t, out, 2)
		if len(results) != 2 {
			t.Fatalf("应当是补一条 + 原本一条: %v", results)
		}
		// 补的排在最前：Anthropic 要求 tool_result 块先于同一条消息里的其他内容
		if results[0]["tool_use_id"] != "call_1" || results[1]["tool_use_id"] != "call_2" {
			t.Fatalf("补的那条应排在前面: %v", results)
		}
		requireNote(t, notes, NoteFilledMissingToolResult)
	})

	t.Run("孤儿工具结果丢掉并记账", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "你好"},
				{"role": "tool", "tool_call_id": "call_不存在", "content": "谁的结果"}
			]
		}`, Options{})

		// 这条 tool 消息会与前一条 user 合并成同一个轮：孤儿结果必须在这之后才被发现，
		// 只检查"assistant 后面紧跟的那条消息"会让它原样发给上游，上游直接 400
		if roles := messageRoles(t, out); len(roles) != 1 {
			t.Fatalf("只剩一条 user: %v", roles)
		}
		got := blocks(t, out, 0)
		if len(got) != 1 || got[0]["type"] != "text" || got[0]["text"] != "你好" {
			t.Fatalf("孤儿结果应当被丢掉，文本留下: %v", got)
		}
		requireNote(t, notes, NoteDroppedOrphanToolResult)
	})

	t.Run("整轮只有孤儿结果时整轮消失", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "你好"},
				{"role": "assistant", "content": "在呢"},
				{"role": "tool", "tool_call_id": "call_不存在", "content": "谁的结果"}
			]
		}`, Options{})

		// 这条孤儿结果与前一条 assistant 不同角色，合并不掉；结果被丢掉后整轮不剩内容
		if roles := messageRoles(t, out); len(roles) != 2 {
			t.Fatalf("被掏空的那轮应当消失: %v", roles)
		}
		requireNote(t, notes, NoteDroppedOrphanToolResult)
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("重复的结果算孤儿", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "查"},
				{"role": "assistant", "tool_calls": [{"id": "call_1", "function": {"name": "f", "arguments": "{}"}}]},
				{"role": "tool", "tool_call_id": "call_1", "content": "第一次"},
				{"role": "tool", "tool_call_id": "call_1", "content": "第二次"}
			]
		}`, Options{})

		results := blocks(t, out, 2)
		if len(results) != 1 || results[0]["content"] != "第一次" {
			t.Fatalf("同一个 id 的第二条结果应当被丢掉: %v", results)
		}
		requireNote(t, notes, NoteDroppedOrphanToolResult)
	})
}

func TestOpenAIToAnthropicMessageHygiene(t *testing.T) {
	t.Run("连续同角色合并", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "第一句"},
				{"role": "user", "content": "第二句"},
				{"role": "user", "content": "第三句"}
			]
		}`, Options{})

		if roles := messageRoles(t, out); len(roles) != 1 {
			t.Fatalf("三条 user 应当合成一条: %v", roles)
		}
		if got := blocks(t, out, 0); len(got) != 3 {
			t.Fatalf("合并后应当留下三个文本块: %v", got)
		}
		// 合并不记账：内容一块不少，被丢掉的是 Anthropic 本来就表示不了的消息边界，
		// 而连续的 tool 消息每次工具调用都会合并——记了只会淹掉真正有损的那几条
		requireNoNote(t, notes, NoteDroppedEmptyMessage)
		if len(notes) != 1 || notes[0] != NoteDefaultedMaxTokens {
			t.Fatalf("合并本身不该产生 Note: %v", notes)
		}
	})

	t.Run("空文本块丢掉", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [{"role": "user", "content": [
				{"type": "text", "text": "有内容"},
				{"type": "text", "text": "   "}
			]}]
		}`, Options{})

		if got := blocks(t, out, 0); len(got) != 1 {
			t.Fatalf("空文本块应当被丢掉: %v", got)
		}
		requireNote(t, notes, NoteDroppedEmptyText)
	})

	t.Run("同一个 Note 只记一次", func(t *testing.T) {
		// 历史里几十个空文本块是常事，逐条记会把日志淹掉
		_, notes := toAnthropic(t, `{
			"messages": [{"role": "user", "content": [
				{"type": "text", "text": "  "},
				{"type": "text", "text": "有内容"},
				{"type": "text", "text": ""}
			]}]
		}`, Options{})

		count := 0
		for _, n := range notes {
			if n == NoteDroppedEmptyText {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("同一个 Note 应当只出现一次: %v", notes)
		}
	})

	t.Run("整条空消息丢掉", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "真问题"},
				{"role": "assistant", "content": ""},
				{"role": "user", "content": "接着问"}
			]
		}`, Options{})

		// 空助手消息消失后，前后两条 user 变成相邻的——它们只能合并（角色必须交替）
		if roles := messageRoles(t, out); len(roles) != 1 {
			t.Fatalf("空助手消息应当消失: %v", roles)
		}
		if got := blocks(t, out, 0); len(got) != 2 {
			t.Fatalf("两条 user 的文本都要留下: %v", got)
		}
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("content 是空数组也算空消息", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": []},
				{"role": "user", "content": "有内容"}
			]
		}`, Options{})

		if roles := messageRoles(t, out); len(roles) != 1 {
			t.Fatalf("空数组消息应当消失: %v", roles)
		}
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("空白文本的 user 消息也算空消息", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "   "},
				{"role": "user", "content": "有内容"}
			]
		}`, Options{})

		if roles := messageRoles(t, out); len(roles) != 1 {
			t.Fatalf("空白消息应当消失: %v", roles)
		}
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("文本排在工具结果之后时要重排", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "查一下"},
				{"role": "assistant", "tool_calls": [{"id": "call_1", "function": {"name": "f", "arguments": "{}"}}]},
				{"role": "user", "content": "顺带说一句"},
				{"role": "tool", "tool_call_id": "call_1", "content": "结果"}
			]
		}`, Options{})

		// 中间那条 user 文本与后面的 tool 消息会被合并成一条 user 消息
		results := blocks(t, out, 2)
		if len(results) != 2 {
			t.Fatalf("应当是 tool_result + 文本两块: %v", results)
		}
		if results[0]["type"] != "tool_result" {
			t.Fatalf("tool_result 必须排在最前: %v", results)
		}
		requireNote(t, notes, NoteReorderedToolResult)
	})

	t.Run("本来就合规的顺序不记账", func(t *testing.T) {
		_, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "查一下"},
				{"role": "assistant", "tool_calls": [{"id": "call_1", "function": {"name": "f", "arguments": "{}"}}]},
				{"role": "tool", "tool_call_id": "call_1", "content": "结果"}
			]
		}`, Options{})

		requireNoNote(t, notes, NoteReorderedToolResult)
	})
}

func TestOpenAIToAnthropicFlattensToolResultParts(t *testing.T) {
	t.Run("纯文本块数组压成字符串", func(t *testing.T) {
		out, notes := toAnthropic(t, `{
			"messages": [
				{"role": "user", "content": "查图"},
				{"role": "assistant", "tool_calls": [{"id": "call_1", "function": {"name": "f", "arguments": "{}"}}]},
				{"role": "tool", "tool_call_id": "call_1", "content": [{"type": "text", "text": "第一段"}, {"type": "text", "text": "第二段"}]}
			]
		}`, Options{})

		results := blocks(t, out, 2)
		if results[0]["content"] != "第一段第二段" {
			t.Fatalf("工具结果的块数组应当压成字符串: %v", results[0]["content"])
		}
		// 压平是纯结构（一个字不少），不记账
		requireNoNote(t, notes, NoteDroppedEmptyMessage)
		if len(notes) != 1 || notes[0] != NoteDefaultedMaxTokens {
			t.Fatalf("压平不该产生 Note: %v", notes)
		}
	})

	t.Run("夹图片的工具结果整份拒绝", func(t *testing.T) {
		// 模型要看的图搬不过去，丢了答案就不一样——拒绝，由路由器换下一家
		rejected(t, `{
			"messages": [
				{"role": "user", "content": "查图"},
				{"role": "assistant", "tool_calls": [{"id": "call_1", "function": {"name": "f", "arguments": "{}"}}]},
				{"role": "tool", "tool_call_id": "call_1", "content": [
					{"type": "image_url", "image_url": {"url": "data:image/png;base64,QUJD"}}
				]}
			]
		}`, "messages[2].content[].type")
	})

	t.Run("system 里夹图片也拒绝", func(t *testing.T) {
		rejected(t, `{
			"messages": [
				{"role": "system", "content": [{"type": "image_url", "image_url": {"url": "https://a/b.png"}}]},
				{"role": "user", "content": "hi"}
			]
		}`, "messages[0].content[].type")
	})
}

// ---------------------------------------------------------------------------
// Anthropic → OpenAI
// ---------------------------------------------------------------------------

func TestAnthropicToOpenAIBasic(t *testing.T) {
	out, notes := toOpenAI(t, `{
		"model": "claude-sonnet-5",
		"max_tokens": 1024,
		"system": "你是助手",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "你好"}]}],
		"temperature": 0.3,
		"top_p": 0.8,
		"stop_sequences": ["###"],
		"stream": true
	}`)

	if out["model"] != "claude-sonnet-5" || out["max_tokens"] != float64(1024) {
		t.Fatalf("基础字段转错了: %v", out)
	}
	messages := out["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "你是助手" {
		t.Fatalf("system 应当落成首条 system 消息: %v", first)
	}
	if messages[1].(map[string]any)["content"] != "你好" {
		t.Fatalf("纯文本应当给字符串而不是块数组: %v", messages[1])
	}
	stop := out["stop"].([]any)
	if len(stop) != 1 || stop[0] != "###" {
		t.Fatalf("stop_sequences 转错了: %v", stop)
	}
	if len(notes) != 0 {
		t.Fatalf("这次不该有任何有损改写: %v", notes)
	}
}

func TestAnthropicToOpenAIStringContent(t *testing.T) {
	// Anthropic 允许 content 写成裸字符串（单块文本的简写），Claude Code 的普通轮次就是
	// 这个形状。判成"翻不过去"的话，这类请求会白白被换走一家上游
	out, notes := toOpenAI(t, `{
		"model": "claude-sonnet-5",
		"max_tokens": 64,
		"messages": [
			{"role": "user", "content": "你好"},
			{"role": "assistant", "content": "在的"},
			{"role": "user", "content": ""}
		]
	}`)

	messages := out["messages"].([]any)
	if len(messages) != 2 {
		// 空串那条归一成一个空文本块，随后按"空文本块"的规矩丢掉整条消息
		t.Fatalf("空串消息应当被丢掉，剩下两条: %v", messages)
	}
	if got := messages[0].(map[string]any)["content"]; got != "你好" {
		t.Fatalf("字符串简写没转成文本: %v", got)
	}
	if got := messages[1].(map[string]any)["content"]; got != "在的" {
		t.Fatalf("assistant 的字符串简写没转成文本: %v", got)
	}
	requireNote(t, notes, NoteDroppedEmptyText)
	requireNote(t, notes, NoteDroppedEmptyMessage)
}

func TestAnthropicToOpenAIContentShapes(t *testing.T) {
	// 除了字符串与块数组，其余写法（对象、数字）是坏数据，仍要拒绝——不然会静默变成
	// 一条没有内容的消息，模型读到的提示比客户端发的少
	for _, body := range []string{
		`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":{"type":"text","text":"你好"}}]}`,
		`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":42}]}`,
		// 连消息本身都不是对象，读 role/content 无从谈起
		`{"model":"m","max_tokens":1,"messages":["你好"]}`,
	} {
		if _, _, err := AnthropicRequestToOpenAI([]byte(body), Options{}); err == nil {
			t.Fatalf("这种 content 应当被拒绝: %s", body)
		}
	}

	// content 缺失或为 null 不算坏数据：就是"这条消息没有内容"，归一成没有块
	out, notes := toOpenAI(t, `{
		"model": "m", "max_tokens": 1,
		"messages": [{"role": "user", "content": null}, {"role": "user", "content": "嗨"}]
	}`)
	if messages := out["messages"].([]any); len(messages) != 1 {
		t.Fatalf("空消息应当被丢掉，只剩下有内容的那条: %v", messages)
	}
	requireNote(t, notes, NoteDroppedEmptyMessage)
}

func TestAnthropicToOpenAISystemShapes(t *testing.T) {
	t.Run("块数组形式", func(t *testing.T) {
		out, _ := toOpenAI(t, `{
			"max_tokens": 10,
			"system": [{"type": "text", "text": "第一段"}, {"type": "text", "text": "第二段"}],
			"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]
		}`)
		messages := out["messages"].([]any)
		if messages[0].(map[string]any)["content"] != "第一段第二段" {
			t.Fatalf("system 块数组没拼对: %v", messages[0])
		}
	})

	t.Run("空白 system 不产生消息", func(t *testing.T) {
		out, _ := toOpenAI(t, `{
			"max_tokens": 10,
			"system": "   ",
			"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]
		}`)
		messages := out["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("空白 system 不该产生消息: %v", messages)
		}
	})

	t.Run("system 里的非文本块直接拒绝", func(t *testing.T) {
		rejectedReverse(t, `{
			"max_tokens": 10,
			"system": [{"type": "image", "source": {"type": "url", "url": "https://a/b.png"}}],
			"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]
		}`, "system")
	})

	t.Run("system 形状不对", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens": 10, "system": 42, "messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]}`, "system")
	})
}

func TestAnthropicToOpenAISystemMessage(t *testing.T) {
	t.Run("messages 里的 system 原样搬过去", func(t *testing.T) {
		// Anthropic 侧这个形状不合规，但 OpenAI 收得了 system——判据是目标协议能不能表达。
		// 曾经这里直接返回 Unsupported，整条请求被换走一家上游
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": "你好"},
				{"role": "system", "content": "现在开始只回一个字"},
				{"role": "assistant", "content": "好"}
			]
		}`)

		messages := out["messages"].([]any)
		if len(messages) != 3 {
			t.Fatalf("三条都该在: %v", messages)
		}
		mid := messages[1].(map[string]any)
		// 位置不动、也不并进别处：OpenAI 允许中途插一条 system，原样带过去最忠实
		if mid["role"] != "system" || mid["content"] != "现在开始只回一个字" {
			t.Fatalf("中途的 system 没搬对: %v", mid)
		}
		if len(notes) != 0 {
			t.Fatalf("无损搬运不该记账: %v", notes)
		}
	})

	t.Run("system 里的思考块丢掉并记账", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "system", "content": [
				{"type": "thinking", "thinking": "心里盘算", "signature": "sig"},
				{"type": "text", "text": "正文"}
			]}]
		}`)
		if got := out["messages"].([]any)[0].(map[string]any)["content"]; got != "正文" {
			t.Fatalf("正文没搬对: %v", got)
		}
		requireNote(t, notes, NoteDroppedThinking)
	})

	t.Run("空的 system 消息整条丢掉", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "system", "content": "   "}, {"role": "user", "content": "hi"}]
		}`)
		if messages := out["messages"].([]any); len(messages) != 1 {
			t.Fatalf("空 system 消息该被丢掉: %v", messages)
		}
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("system 里的图片照搬", func(t *testing.T) {
		out, _ := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "system", "content": [
				{"type": "text", "text": "看这张图"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "QUJD"}}
			]}]
		}`)
		parts := out["messages"].([]any)[0].(map[string]any)["content"].([]any)
		if len(parts) != 2 {
			t.Fatalf("文本与图片各一份: %v", parts)
		}
		if parts[0].(map[string]any)["type"] != "text" {
			t.Fatalf("文本块该在最前: %v", parts[0])
		}
		url := parts[1].(map[string]any)["image_url"].(map[string]any)["url"]
		if url != "data:image/png;base64,QUJD" {
			t.Fatalf("图片没还原成 data URL: %v", url)
		}
	})

	t.Run("system 里的坏图片拒绝", func(t *testing.T) {
		rejectedReverse(t, `{
			"max_tokens": 10,
			"messages": [{"role": "system", "content": [{"type": "image", "source": {"type": "base64"}}]}]
		}`, "image.source")
	})

	t.Run("system 里的工具块仍拒绝", func(t *testing.T) {
		// 搬得过去的是角色，不是任意内容块：tool_use 放进 system 里没有意义，
		// 静默丢掉等于让模型少读一段它本该看到的东西
		rejectedReverse(t, `{
			"max_tokens": 10,
			"messages": [{"role": "system", "content": [{"type": "tool_use", "id": "t", "name": "f", "input": {}}]}]
		}`, "messages[0].content[].type")
	})
}

func TestAnthropicToOpenAIMaxTokens(t *testing.T) {
	t.Run("给了就带上", func(t *testing.T) {
		out, _ := toOpenAI(t, `{"max_tokens": 0, "messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]}`)
		if _, ok := out["max_tokens"]; ok {
			t.Fatalf("max_tokens 为 0 时不该出现: %v", out["max_tokens"])
		}
	})
}

func TestAnthropicToOpenAIUserBlocks(t *testing.T) {
	t.Run("图片 base64 还原成 data URL", func(t *testing.T) {
		out, _ := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "user", "content": [
				{"type": "text", "text": "看这张"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "QUJD"}}
			]}]
		}`)
		messages := out["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		if len(content) != 2 {
			t.Fatalf("应当是文本 + 图片两块: %v", content)
		}
		if content[0].(map[string]any)["text"] != "看这张" {
			t.Fatalf("文本块不对: %v", content[0])
		}
		url := content[1].(map[string]any)["image_url"].(map[string]any)["url"]
		if url != "data:image/png;base64,QUJD" {
			t.Fatalf("data URL 没拼回来: %v", url)
		}
	})

	t.Run("图片 url 源原样带过", func(t *testing.T) {
		out, _ := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "user", "content": [{"type": "image", "source": {"type": "url", "url": "https://a/b.png"}}]}]
		}`)
		messages := out["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		if content[0].(map[string]any)["image_url"].(map[string]any)["url"] != "https://a/b.png" {
			t.Fatalf("url 图片没带过去: %v", content[0])
		}
	})

	t.Run("空文本块丢掉", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "user", "content": [{"type": "text", "text": "  "}, {"type": "text", "text": "有内容"}]}]
		}`)
		messages := out["messages"].([]any)
		if messages[0].(map[string]any)["content"] != "有内容" {
			t.Fatalf("空文本块应当被丢掉: %v", messages[0])
		}
		requireNote(t, notes, NoteDroppedEmptyText)
	})

	t.Run("整条消息什么都不剩就丢掉并记账", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": []},
				{"role": "user", "content": [{"type": "text", "text": "真问题"}]}
			]
		}`)
		messages := out["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("空消息应当消失: %v", messages)
		}
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("多个文本块并成一段", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "user", "content": [{"type": "text", "text": "前半"}, {"type": "text", "text": "后半"}]}]
		}`)
		messages := out["messages"].([]any)
		if messages[0].(map[string]any)["content"] != "前半后半" {
			t.Fatalf("多个文本块应当并成一段: %v", messages[0])
		}
		// 纯结构拼接，不记账
		if len(notes) != 0 {
			t.Fatalf("拼接不该产生 Note: %v", notes)
		}
	})
}

func TestAnthropicToOpenAIToolResults(t *testing.T) {
	out, notes := toOpenAI(t, `{
		"max_tokens": 10,
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "查天气"}]},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "beijing"}}]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": "晴"},
				{"type": "tool_result", "tool_use_id": "toolu_2", "content": [{"type": "text", "text": "多云"}], "is_error": true}
			]}
		]
	}`)

	messages := out["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("应当展开成 user/assistant/tool/tool: %v", messages)
	}
	assistant := messages[1].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["id"] != "toolu_1" {
		t.Fatalf("tool_use 没转成 tool_calls: %v", calls)
	}
	// input 是对象，OpenAI 要的是 JSON 字符串
	if calls[0].(map[string]any)["function"].(map[string]any)["arguments"] != `{"city": "beijing"}` {
		t.Fatalf("arguments 应当是 input 的字符串形式: %v", calls[0])
	}

	first := messages[2].(map[string]any)
	if first["role"] != "tool" || first["tool_call_id"] != "toolu_1" || first["content"] != "晴" {
		t.Fatalf("第一条工具结果转错了: %v", first)
	}
	// 块数组形式的工具结果压成字符串；is_error 只能写进正文
	second := messages[3].(map[string]any)
	if second["content"] != toolErrorPrefix+"多云" {
		t.Fatalf("失败的工具有结果必须留下痕迹: %v", second["content"])
	}
	requireNote(t, notes, NotePrefixedToolError)
}

func TestAnthropicToOpenAIToolResultImage(t *testing.T) {
	// 工具返回的截图在 OpenAI 的 tool 消息里放不下（正文只有字符串）。丢掉等于让模型
	// 瞎着往下推，所以整份拒绝，由路由器换一家能原样收下的上游
	rejectedReverse(t, `{
		"max_tokens": 10,
		"messages": [
			{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "shot", "input": {}}]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": [
					{"type": "text", "text": "截好了"},
					{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "QUJD"}}
				]}
			]}
		]
	}`, "tool_result.content")
}

func TestAnthropicToOpenAIToolResultOrder(t *testing.T) {
	t.Run("文本排在结果之前时要重排", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "f", "input": {}}]},
				{"role": "user", "content": [
					{"type": "text", "text": "顺带说一句"},
					{"type": "tool_result", "tool_use_id": "toolu_1", "content": "结果"}
				]}
			]
		}`)
		messages := out["messages"].([]any)
		// tool 消息要紧接着 tool_calls，中间不能夹一条 user 文本消息
		if messages[1].(map[string]any)["role"] != "tool" {
			t.Fatalf("tool 消息应当排在前面: %v", messages)
		}
		if messages[2].(map[string]any)["content"] != "顺带说一句" {
			t.Fatalf("文本应当跟在后面: %v", messages[2])
		}
		requireNote(t, notes, NoteReorderedToolResult)
	})

	t.Run("本来就合规的顺序不记账", func(t *testing.T) {
		_, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "f", "input": {}}]},
				{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "结果"}]}
			]
		}`)
		requireNoNote(t, notes, NoteReorderedToolResult)
	})

	t.Run("工具结果内容形状不对", func(t *testing.T) {
		rejectedReverse(t, `{
			"max_tokens": 10,
			"messages": [{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t", "content": 42}]}]
		}`, "tool_result.content")
	})

	t.Run("工具结果没带 content 时为空串", func(t *testing.T) {
		out, _ := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "f", "input": {}}]},
				{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1"}]}
			]
		}`)
		messages := out["messages"].([]any)
		if messages[1].(map[string]any)["content"] != "" {
			t.Fatalf("缺 content 应当是空串: %v", messages[1])
		}
	})
}

func TestAnthropicToOpenAIAssistantBlocks(t *testing.T) {
	t.Run("正文与工具调用并存", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "查"}]},
				{"role": "assistant", "content": [
					{"type": "text", "text": "我查一下"},
					{"type": "tool_use", "id": "toolu_1", "name": "f", "input": {"a": 1}}
				]}
			]
		}`)
		messages := out["messages"].([]any)
		assistant := messages[1].(map[string]any)
		if assistant["content"] != "我查一下" {
			t.Fatalf("正文没带过去: %v", assistant)
		}
		if len(assistant["tool_calls"].([]any)) != 1 {
			t.Fatalf("工具调用没带过去: %v", assistant)
		}
		requireNote(t, notes, NoteFlattenedContentOrder)
	})

	t.Run("工具调用缺 input 时落成空对象", func(t *testing.T) {
		out, _ := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "查"}]},
				{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "f"}]}
			]
		}`)
		messages := out["messages"].([]any)
		args := messages[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["arguments"]
		if args != "{}" {
			t.Fatalf("缺 input 应当是 {}: %v", args)
		}
	})

	t.Run("思考块丢掉并记账", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "想一下"}]},
				{"role": "assistant", "content": [
					{"type": "thinking", "thinking": "心里话", "signature": "sig"},
					{"type": "text", "text": "答案"}
				]}
			]
		}`)
		messages := out["messages"].([]any)
		if messages[1].(map[string]any)["content"] != "答案" {
			t.Fatalf("思考块不该进正文: %v", messages[1])
		}
		requireNote(t, notes, NoteDroppedThinking)
	})

	t.Run("红acted 思考块也算思考块", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "想一下"}]},
				{"role": "assistant", "content": [{"type": "redacted_thinking", "data": "xxx"}]}
			]
		}`)
		messages := out["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("只剩思考块的助手消息应当整条消失: %v", messages)
		}
		requireNote(t, notes, NoteDroppedThinking)
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("user 轮里的思考块丢掉、文本留下", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [{"role": "user", "content": [
				{"type": "thinking", "thinking": "冒出来的", "signature": "sig"},
				{"type": "text", "text": "真问题"}
			]}]
		}`)
		messages := out["messages"].([]any)
		if len(messages) != 1 || messages[0].(map[string]any)["content"] != "真问题" {
			t.Fatalf("思考块不该进正文: %v", messages)
		}
		requireNote(t, notes, NoteDroppedThinking)
	})

	t.Run("空助手消息丢掉", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "问题"}]},
				{"role": "assistant", "content": []}
			]
		}`)
		messages := out["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("空助手消息应当消失: %v", messages)
		}
		requireNote(t, notes, NoteDroppedEmptyMessage)
	})

	t.Run("空白文本的助手块丢掉", func(t *testing.T) {
		out, notes := toOpenAI(t, `{
			"max_tokens": 10,
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "问题"}]},
				{"role": "assistant", "content": [{"type": "text", "text": "  "}, {"type": "text", "text": "答案"}]}
			]
		}`)
		messages := out["messages"].([]any)
		if messages[1].(map[string]any)["content"] != "答案" {
			t.Fatalf("空白块该丢、文本该留: %v", messages[1])
		}
		requireNote(t, notes, NoteDroppedEmptyText)
	})
}

func TestAnthropicToOpenAITools(t *testing.T) {
	out, notes := toOpenAI(t, `{
		"max_tokens": 10,
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}],
		"tools": [
			{"name": "get_weather", "description": "查天气", "input_schema": {"type": "object", "properties": {}}},
			{"name": "noop"}
		],
		"tool_choice": {"type": "any"}
	}`)

	tools := out["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("工具数不对: %v", tools)
	}
	first := tools[0].(map[string]any)
	if first["type"] != "function" || first["function"].(map[string]any)["name"] != "get_weather" {
		t.Fatalf("工具转错了: %v", first)
	}
	if first["function"].(map[string]any)["parameters"].(map[string]any)["type"] != "object" {
		t.Fatalf("input_schema 没搬成 parameters: %v", first)
	}
	// 缺 input_schema 的补一个空对象并记账
	requireNote(t, notes, NoteDefaultedInputSchema)
	// any 对应 required
	if out["tool_choice"] != "required" {
		t.Fatalf("any 应当对应 required: %v", out["tool_choice"])
	}
}

func TestAnthropicToOpenAIToolChoice(t *testing.T) {
	cases := []struct {
		name   string
		choice string
		want   string
	}{
		{name: "auto", choice: `{"type":"auto"}`, want: "auto"},
		{name: "none 原样对应", choice: `{"type":"none"}`, want: "none"},
		{name: "指定函数", choice: `{"type":"tool","name":"f"}`, want: `{"function":{"name":"f"},"type":"function"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := toOpenAI(t, `{
				"max_tokens": 10,
				"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}],
				"tool_choice": `+tc.choice+`
			}`)
			if tc.want == "auto" || tc.want == "none" {
				if out["tool_choice"] != tc.want {
					t.Fatalf("tool_choice 应为 %q: %v", tc.want, out["tool_choice"])
				}
				return
			}
			got := out["tool_choice"].(map[string]any)
			if got["type"] != "function" || got["function"].(map[string]any)["name"] != "f" {
				t.Fatalf("指定函数转错了: %v", got)
			}
		})
	}
}

func TestAnthropicToOpenAIDroppedFields(t *testing.T) {
	out, notes := toOpenAI(t, `{
		"max_tokens": 10,
		"top_k": 40,
		"thinking": {"type": "enabled", "budget_tokens": 2048},
		"metadata": {"user_id": "u-1"},
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]
	}`)

	// 这三样在 OpenAI 协议里没有对应物：参数丢掉、记账，删掉就不该出现在结果里
	for _, key := range []string{"top_k", "thinking", "metadata"} {
		if _, ok := out[key]; ok {
			t.Fatalf("%s 不该出现: %v", key, out[key])
		}
	}
	requireNote(t, notes, NoteDroppedTopK)
	requireNote(t, notes, NoteDroppedThinking)
	requireNote(t, notes, NoteDroppedMetadata)
}

func TestAnthropicToOpenAIRejects(t *testing.T) {
	base := `{"max_tokens": 10, "messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]}`

	t.Run("整份请求不是 JSON", func(t *testing.T) {
		rejectedReverse(t, `{`, "body")
	})

	t.Run("未知角色", func(t *testing.T) {
		// tool 是 OpenAI 的角色，Anthropic 侧没有；system 不在此列——它搬得过去
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"tool","content":[{"type":"text","text":"x"}]}]}`, "messages[0].role")
	})

	t.Run("未知内容块", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"document","source":{}}]}]}`, "messages[0].content[].type")
	})

	t.Run("助手消息里的未知块", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"document"}]}]}`, "messages[1].content[].type")
	})

	t.Run("图片缺 source", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"image"}]}]}`, "image.source")
	})

	t.Run("base64 图片缺字段", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png"}}]}]}`, "image.source")
	})

	t.Run("url 图片缺 url", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url"}}]}]}`, "image.source")
	})

	t.Run("图片源类型认不出", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"file","url":"x"}}]}]}`, "image.source.type")
	})

	t.Run("tool_choice 类型认不出", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tool_choice":{"type":"whatever"}}`, "tool_choice.type")
	})

	t.Run("tool_choice 指定函数却没给名字", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tool_choice":{"type":"tool"}}`, "tool_choice.name")
	})

	t.Run("没有消息", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[]}`, "messages")
	})

	t.Run("消息都转没了", func(t *testing.T) {
		rejectedReverse(t, `{"max_tokens":10,"messages":[{"role":"user","content":[]}]}`, "messages")
	})

	_ = base
}

// ---------------------------------------------------------------------------
// 翻译器入口
// ---------------------------------------------------------------------------

func TestNewTranslator(t *testing.T) {
	cases := []struct {
		client, upstream Protocol
		want             bool
	}{
		{ProtocolOpenAI, ProtocolAnthropic, true},
		{ProtocolAnthropic, ProtocolOpenAI, true},
		{ProtocolOpenAI, ProtocolOpenAI, false},
		{ProtocolAnthropic, ProtocolAnthropic, false},
		{ProtocolOpenAI, Protocol("gemini"), false},
		{Protocol("openai-res"), ProtocolOpenAI, false},
		{Protocol("openai-res"), ProtocolAnthropic, false},
	}
	for _, tc := range cases {
		tr, ok := NewTranslator(tc.client, tc.upstream)
		if ok != tc.want {
			t.Fatalf("NewTranslator(%q, %q) = %v，期望 %v", tc.client, tc.upstream, ok, tc.want)
		}
		if !ok {
			if tr != nil {
				t.Fatalf("两端不用翻时不该给出翻译器: %v", tr)
			}
			continue
		}
		if tr.From() != tc.client || tr.To() != tc.upstream {
			t.Fatalf("方向记错了: %v → %v", tr.From(), tr.To())
		}
	}
}

func TestTranslatorBothWays(t *testing.T) {
	t.Run("openai 客户端 → anthropic 上游", func(t *testing.T) {
		tr, _ := NewTranslator(ProtocolOpenAI, ProtocolAnthropic)
		body, _, err := tr.Request([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), Options{})
		if err != nil {
			t.Fatalf("请求转换失败: %v", err)
		}
		out := decode(t, body)
		if _, ok := out["messages"]; !ok {
			t.Fatalf("应当转成 messages 形状: %v", out)
		}
		if out["max_tokens"] != float64(DefaultMaxTokens) {
			t.Fatalf("Anthropic 方向的 max_tokens 必填: %v", out)
		}
		res, _, err := tr.Response([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"你好"}],"stop_reason":"end_turn"}`))
		if err != nil {
			t.Fatalf("响应转换失败: %v", err)
		}
		if choice(t, decode(t, res))["message"].(map[string]any)["content"] != "你好" {
			t.Fatalf("响应没翻回 OpenAI 形状: %s", res)
		}
		if _, ok := tr.Stream().(*AnthropicToOpenAIStream); !ok {
			t.Fatal("流式方向应当是从 Anthropic 翻回 OpenAI")
		}
	})

	t.Run("anthropic 客户端 → openai 上游", func(t *testing.T) {
		tr, _ := NewTranslator(ProtocolAnthropic, ProtocolOpenAI)
		body, _, err := tr.Request([]byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`), Options{})
		if err != nil {
			t.Fatalf("请求转换失败: %v", err)
		}
		out := decode(t, body)
		if out["messages"].([]any)[0].(map[string]any)["role"] != "user" {
			t.Fatalf("应当转成 OpenAI 的 messages 形状: %v", out)
		}
		res, _, err := tr.Response([]byte(`{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}]}`))
		if err != nil {
			t.Fatalf("响应转换失败: %v", err)
		}
		if decode(t, res)["type"] != "message" {
			t.Fatalf("响应没翻回 Anthropic 形状: %s", res)
		}
		if _, ok := tr.Stream().(*OpenAIToAnthropicStream); !ok {
			t.Fatal("流式方向应当是从 OpenAI 翻回 Anthropic")
		}
	})
}

func TestAnthropicRequestToOpenAIStreamOptions(t *testing.T) {
	// Anthropic 的流一定带 usage，OpenAI 的默认不带：不主动要，token 全记成 0
	out, _, err := AnthropicRequestToOpenAI([]byte(`{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`), Options{})
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	opts, ok := decode(t, out)["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Fatalf("流式请求应当注入 include_usage: %s", out)
	}

	// 非流式不注入：多带一个字段就可能让挑剔的上游 400
	out, _, err = AnthropicRequestToOpenAI([]byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`), Options{})
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if _, ok := decode(t, out)["stream_options"]; ok {
		t.Fatalf("非流式不该注入 stream_options: %s", out)
	}
}
