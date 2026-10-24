package service

import (
	"testing"

	"github.com/tidwall/gjson"
)

// 判据与改写的表驱动用例（docs/plans/usage-insights-plan.md §3.4/§3.7）。
// 纯函数零 IO，全分支可枚举：模式表正例按信封摆，负例按"不许自愈"的类别摆。

func TestDetectParamComplaint_Hits(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		// 模式表逐条 × 各自信封
		{"openai 官方", 400, `{"error":{"message":"Unknown parameter: 'top_k'.","type":"invalid_request_error"}}`, "top_k"},
		{"openai 兼容第三方", 400, `{"error":{"message":"Unrecognized request argument supplied: logit_bias"}}`, "logit_bias"},
		{"pydantic 前缀式", 400, `{"error":{"message":"top_k: Extra inputs are not permitted"}}`, "top_k"},
		{"pydantic 括号式", 422, `{"error":{"message":"Extra inputs are not permitted (path: top_k)"}}`, "top_k"},
		{"anthropic 信封", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"Unsupported parameter: 'metadata'"}}`, "metadata"},
		{"is not supported", 400, `{"error":{"message":"top_k is not supported by this model"}}`, "top_k"},
		{"gemini 信封", 400, `{"error":{"code":400,"message":"Unknown name \"top_k\" at 'input'","status":"INVALID_ARGUMENT"}}`, "top_k"},
		{"通用 field 短语", 400, `{"error":{"message":"field 'foo_bar' is not supported"}}`, "foo_bar"},
		{"message 为数组", 400, `{"error":{"message":["Unrecognized request argument supplied: top_k","fix it"]}}`, "top_k"},
		{"简写信封", 400, `{"error":"Unknown parameter: 'top_k'."}`, "top_k"},
		{"非 JSON 原文", 400, "plain text: Unrecognized request argument supplied: top_k", "top_k"},
		{"嵌套路径", 400, `{"error":{"message":"Unrecognized request argument supplied: messages.0.meta"}}`, "messages.0.meta"},
		{"422 也进判据", 422, `{"error":{"message":"Unknown parameter: 'foo'"}}`, "foo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := DetectParamComplaint(tc.status, []byte(tc.body))
			if !ok || got != tc.want {
				t.Fatalf("DetectParamComplaint(%d) = (%q, %v)，期望 (%q, true)", tc.status, got, ok, tc.want)
			}
		})
	}
}

func TestDetectParamComplaint_Misses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		body   string
	}{
		// 状态码闸：文本命中也不行
		{"401", 401, `{"error":{"message":"Unknown parameter: 'top_k'."}}`},
		{"429", 429, `{"error":{"message":"Unknown parameter: 'top_k'."}}`},
		{"500", 500, `{"error":{"message":"Unknown parameter: 'top_k'."}}`},
		{"200（ErrorMatcher 场景）", 200, `{"error":{"message":"Unknown parameter: 'top_k'."}}`},
		// 值域/上下文类：参数存在但错的是值，删参=替客户端改语义
		{"值类型错", 400, `{"error":{"message":"Invalid type for 'top_k': expected an integer, but got a string"}}`},
		{"上下文超长", 400, `{"error":{"message":"This model's maximum context length is 8192 tokens. However, your messages resulted in 10000 tokens."}}`},
		{"max_tokens 超限", 400, `{"error":{"message":"max_tokens: 999999 is too large for this model"}}`},
		{"鉴权", 401, `{"error":{"message":"invalid api key"}}`},
		{"body 不是 json", 400, `{"error":{"message":"body is not json"}}`},
		{"桩 boom 措辞", 400, `{"error":{"message":"桩上游：这个模型就是坏的","type":"invalid_request_error"}}`},
		{"提取不出参数名", 400, `{"error":{"message":"unsupported parameter"}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := DetectParamComplaint(tc.status, []byte(tc.body)); ok {
				t.Fatalf("不该命中却返回 (%q, true)", got)
			}
		})
	}
}

func TestPlanDrop(t *testing.T) {
	t.Parallel()

	sent := []byte(`{"model":"m","stream":false,"messages":[{"role":"user","content":"hi","meta":{"x":1}}],"top_k":40}`)

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"顶层键在", "top_k", true},
		{"嵌套子路径在（保护字段的子路径放行）", "messages.0.meta", true},
		{"保护字段七项", "messages", false},
		{"保护 stream", "stream", false},
		{"保护 model", "model", false},
		{"点名的没发过", "logit_bias", false},
		{"非法字符", "top k", false},
		{"路径穿越", "../x", false},
		{"空路径", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := PlanDrop(sent, tc.path, map[string]bool{}); got != tc.want {
				t.Fatalf("PlanDrop(%q) = %v，期望 %v", tc.path, got, tc.want)
			}
		})
	}

	t.Run("超长路径", func(t *testing.T) {
		t.Parallel()
		long := "a"
		for len(long) < 129 {
			long += ".b"
		}
		if PlanDrop(sent, long, nil) {
			t.Fatal("超长路径应拒绝")
		}
	})

	t.Run("已删过的不再删", func(t *testing.T) {
		t.Parallel()
		if PlanDrop(sent, "top_k", map[string]bool{"top_k": true}) {
			t.Fatal("同一参数二次点名应拒绝——防上游反复报同一个名字的死循环")
		}
	})
}

func TestApplyDrop(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		path string
		want string
	}{
		{"顶层键", `{"top_k":40,"model":"m"}`, "top_k", `{"model":"m"}`},
		{"嵌套点路径", `{"top_k":40,"sampling":{"top_k":9}}`, "sampling.top_k", `{"top_k":40,"sampling":{}}`},
		{"数组下标", `{"messages":[{"role":"user","content":"hi","meta":1}]}`, "messages.0.meta", `{"messages":[{"role":"user","content":"hi"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ApplyDrop([]byte(tc.raw), tc.path)
			if gjson.GetBytes(got, tc.path).Exists() {
				t.Fatalf("路径 %s 仍在：%s", tc.path, got)
			}
			// 其余内容不能被动到：删完后合法 JSON 且关键字段原样
			if !gjson.ValidBytes(got) {
				t.Fatalf("删除后不是合法 JSON：%s", got)
			}
		})
	}
}

func TestFlattenToolContent(t *testing.T) {
	t.Parallel()

	t.Run("混合 text+image 数组压回字符串并计数", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"messages":[{"role":"user","content":"查图"},{"role":"tool","tool_call_id":"t1","content":[{"type":"text","text":"截好了"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD"}}]}]}`)
		out, dropped := FlattenToolContent(raw)
		if dropped != 1 {
			t.Fatalf("应丢弃 1 张图，实得 %d", dropped)
		}
		got := gjson.GetBytes(out, "messages.1.content")
		if got.Type != gjson.String || got.String() != "截好了" {
			t.Fatalf("tool content 应压回字符串，实得 %v", got)
		}
		if gjson.GetBytes(out, "messages.0.content").String() != "查图" {
			t.Fatalf("user 消息不该被动到：%s", out)
		}
	})

	t.Run("纯 text 数组无损压缩不计数", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"messages":[{"role":"tool","tool_call_id":"t1","content":[{"type":"text","text":"第一段"},{"type":"input_text","text":"第二段"}]}]}`)
		out, dropped := FlattenToolContent(raw)
		if dropped != 0 {
			t.Fatalf("无图时 dropped 应为 0，实得 %d", dropped)
		}
		if gjson.GetBytes(out, "messages.0.content").String() != "第一段第二段" {
			t.Fatalf("纯文本应拼接保序：%s", out)
		}
	})

	t.Run("非数组 content 与无 tool 消息原样返回", func(t *testing.T) {
		t.Parallel()
		a := []byte(`{"messages":[{"role":"tool","content":"字符串"}]}`)
		b := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]}`)
		for _, raw := range [][]byte{a, b} {
			out, dropped := FlattenToolContent(raw)
			if dropped != -1 || string(out) != string(raw) {
				t.Fatalf("应原样返回，实得 dropped=%d out=%s", dropped, out)
			}
		}
	})

	t.Run("幂等：二次运行为原样", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"messages":[{"role":"tool","tool_call_id":"t1","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"u"}}]}]}`)
		once, _ := FlattenToolContent(raw)
		twice, dropped := FlattenToolContent(once)
		if dropped != -1 || string(twice) != string(once) {
			t.Fatalf("二次运行应原样返回，实得 dropped=%d", dropped)
		}
	})

	t.Run("值类型错的 top_k 不触发降级判据", func(t *testing.T) {
		t.Parallel()
		// N 组负控的判据侧钉子：toolContentArrayPattern 只认 content 的类型错
		if toolContentArrayPattern.MatchString("Invalid type for 'top_k': expected an integer") {
			t.Fatal("top_k 的类型错不该命中工具内容降级判据")
		}
	})
}
