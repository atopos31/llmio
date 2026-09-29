package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── ResolveSessionKey：派生链优先级 ──

func TestResolveSessionKeyPriority(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"hello world"}]}`)

	tests := []struct {
		name      string
		header    http.Header
		sessionID string
		want      string
	}{
		{
			name:      "请求体 session_id 优先级最高",
			header:    http.Header{"X-Opencode-Session": []string{"from-header"}},
			sessionID: "from-body",
			want:      "from-body",
		},
		{
			name:   "无请求体时取 x-opencode-session",
			header: http.Header{"X-Opencode-Session": []string{"from-opencode"}},
			want:   "from-opencode",
		},
		{
			name:   "无 opencode 头时取 x-session-id",
			header: http.Header{"X-Session-Id": []string{"from-session-id"}},
			want:   "from-session-id",
		},
		{
			name:   "无 x-session-id 时取 session_id 头",
			header: http.Header{"Session_id": []string{"from-snake"}},
			want:   "from-snake",
		},
		{
			name: "空值与空白值的头被跳过",
			header: http.Header{
				"X-Opencode-Session": []string{"   "},
				"X-Session-Id":       []string{""},
			},
			want: "", // 落到对话根哈希，由下方断言前缀校验
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveSessionKey(tt.header, tt.sessionID, "m", 1, raw)
			if tt.want != "" {
				if got != tt.want {
					t.Fatalf("ResolveSessionKey() = %q, want %q", got, tt.want)
				}
				return
			}
			if !strings.HasPrefix(got, sessionKeyPrefix) {
				t.Fatalf("ResolveSessionKey() = %q, want 对话根哈希兜底（前缀 %q）", got, sessionKeyPrefix)
			}
		})
	}
}

func TestResolveSessionKeyFromConversationRootIsStable(t *testing.T) {
	raw1 := []byte(`{"messages":[{"role":"user","content":"同一个对话的首条消息"}]}`)
	raw2 := []byte(`{"messages":[{"role":"user","content":"同一个对话的首条消息"},{"role":"assistant","content":"回复"},{"role":"user","content":"追问"}]}`)
	rawOther := []byte(`{"messages":[{"role":"user","content":"另一个对话"}]}`)

	first := ResolveSessionKey(nil, "", "model-a", 7, raw1)

	// 同一对话的多轮请求必须派生出同一会话键
	if second := ResolveSessionKey(nil, "", "model-a", 7, raw2); second != first {
		t.Errorf("同一对话多轮派生不一致: %q != %q", second, first)
	}

	// 不同对话应派生出不同会话键
	if other := ResolveSessionKey(nil, "", "model-a", 7, rawOther); other == first {
		t.Errorf("不同对话派生出了相同会话键: %q", other)
	}

	// 不同 AuthKey 或不同模型应派生出不同会话键（隔离不同租户/模型的会话）
	if diffKey := ResolveSessionKey(nil, "", "model-a", 8, raw1); diffKey == first {
		t.Errorf("不同 AuthKey 派生出了相同会话键: %q", diffKey)
	}
	if diffModel := ResolveSessionKey(nil, "", "model-b", 7, raw1); diffModel == first {
		t.Errorf("不同模型派生出了相同会话键: %q", diffModel)
	}
}

func TestResolveSessionKeyRandomFallback(t *testing.T) {
	t.Run("无法提取对话根时回落到随机值", func(t *testing.T) {
		got := ResolveSessionKey(nil, "", "m", 0, nil)
		if !strings.HasPrefix(got, sessionKeyPrefix) {
			t.Fatalf("ResolveSessionKey() = %q, want 随机兜底", got)
		}
		if len(got) <= len(sessionKeyPrefix) {
			t.Fatalf("ResolveSessionKey() = %q, want 前缀以外还有随机内容", got)
		}
	})

	t.Run("随机源异常时退化为时间戳且非空", func(t *testing.T) {
		restore := stubRandomChars(func(int) (string, error) {
			return "", errors.New("random source unavailable")
		})
		defer restore()

		got := ResolveSessionKey(nil, "", "m", 0, nil)
		if !strings.HasPrefix(got, sessionKeyPrefix) {
			t.Fatalf("ResolveSessionKey() = %q, want 时间戳兜底", got)
		}
		if got == sessionKeyPrefix {
			t.Fatal("ResolveSessionKey() 返回了只有前缀的空值")
		}
	})
}

// ── conversationRoot：四种协议的对话根提取 ──

func TestConversationRoot(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "openai 字符串 content",
			raw:  `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"openai 首条"}]}`,
			want: "openai 首条",
		},
		{
			name: "openai content block 数组",
			raw:  `{"messages":[{"role":"user","content":[{"type":"text","text":"第一段"},{"type":"text","text":"第二段"}]}]}`,
			want: "第一段第二段",
		},
		{
			name: "anthropic 风格 content block",
			raw:  `{"system":"sys","messages":[{"role":"user","content":[{"type":"text","text":"anthropic 首条"}]}]}`,
			want: "anthropic 首条",
		},
		{
			name: "openai-res input 数组",
			raw:  `{"input":[{"role":"user","content":[{"type":"input_text","text":"responses 首条"}]}]}`,
			want: "responses 首条",
		},
		{
			name: "openai-res input 为字符串",
			raw:  `{"input":"responses 字符串输入"}`,
			want: "responses 字符串输入",
		},
		{
			name: "gemini contents parts",
			raw:  `{"contents":[{"parts":[{"text":"gemini 首条"}]}]}`,
			want: "gemini 首条",
		},
		{
			name: "gemini 跳过 model 角色",
			raw:  `{"contents":[{"role":"model","parts":[{"text":"模型旧回复"}]},{"role":"user","parts":[{"text":"gemini 用户消息"}]}]}`,
			want: "gemini 用户消息",
		},
		{
			name: "role 缺省时视为 user",
			raw:  `{"messages":[{"content":"无角色字段"}]}`,
			want: "无角色字段",
		},
		{
			name: "跳过非 user 角色",
			raw:  `{"messages":[{"role":"assistant","content":"助手"},{"role":"user","content":"用户"}]}`,
			want: "用户",
		},
		{
			name: "content 缺失时继续向后查找",
			raw:  `{"messages":[{"role":"user"},{"role":"user","content":"后面的用户消息"}]}`,
			want: "后面的用户消息",
		},
		{
			name: "空 body",
			raw:  ``,
			want: "",
		},
		{
			name: "无法识别结构",
			raw:  `{"foo":"bar"}`,
			want: "",
		},
		{
			name: "messages 非数组",
			raw:  `{"messages":"not-an-array"}`,
			want: "",
		},
		{
			name: "content 为空串时继续查找",
			raw:  `{"messages":[{"role":"user","content":""},{"role":"user","content":"有效内容"}]}`,
			want: "有效内容",
		},
		{
			name: "content 为非法结构时返回空",
			raw:  `{"messages":[{"role":"user","content":{"nested":"object"}}]}`,
			want: "",
		},
		{
			name: "input 为空白字符串时落到后续结构",
			raw:  `{"input":"   ","messages":[{"role":"user","content":"退回到 messages"}]}`,
			want: "退回到 messages",
		},
		{
			name: "gemini contents 无 parts",
			raw:  `{"contents":[{"role":"user"}]}`,
			want: "",
		},
		{
			name: "gemini parts 无文本",
			raw:  `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png"}}]}]}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conversationRoot([]byte(tt.raw)); got != tt.want {
				t.Fatalf("conversationRoot(%s) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestContentTextArrayPrefersTextThenInputText(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"input_text","input_text":"来自 input_text"},{"type":"text","text":"来自 text"}]}]}`)
	if got := conversationRoot(raw); got != "来自 input_text来自 text" {
		t.Fatalf("conversationRoot() = %q, want 两个字段都被拼接", got)
	}
}

// ── renderHeaderValue：模板求值 ──

func TestRenderHeaderValue(t *testing.T) {
	vars := HeaderVars{
		Session:       "llmio-sess-abc",
		SessionID:     "body-session",
		Model:         "gpt-x",
		ProviderModel: "upstream-x",
		TraceID:       "trace123",
		AuthKeyID:     42,
	}

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "字面量原样返回", value: "static-value", want: "static-value"},
		{name: "空字面量原样返回", value: "", want: ""},
		{name: "单个 session 占位符", value: "{{session}}", want: "llmio-sess-abc"},
		{name: "session_id 占位符", value: "{{session_id}}", want: "body-session"},
		{name: "model 占位符", value: "{{model}}", want: "gpt-x"},
		{name: "provider_model 占位符", value: "{{provider_model}}", want: "upstream-x"},
		{name: "trace_id 占位符", value: "{{trace_id}}", want: "trace123"},
		{name: "auth_key_id 占位符", value: "{{auth_key_id}}", want: "42"},
		{name: "混排字面量与占位符", value: "sess-{{session}}-{{model}}", want: "sess-llmio-sess-abc-gpt-x"},
		{name: "占位符允许内部空格", value: "{{  session  }}", want: "llmio-sess-abc"},
		{name: "未识别占位符原样保留", value: "{{unknown}}", want: "{{unknown}}"},
		{name: "未识别与已识别混排", value: "a{{unknown}}b{{model}}", want: "a{{unknown}}bgpt-x"},
		{name: "非法占位符语法不匹配", value: "{{}}", want: "{{}}"},
		{name: "单花括号不触发求值", value: "{session}", want: "{session}"},
		{name: "前缀加空占位符", value: "prefix-", want: "prefix-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderHeaderValue(tt.value, vars); got != tt.want {
				t.Fatalf("renderHeaderValue(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRenderHeaderValueUuidIsFreshAndNonEmpty(t *testing.T) {
	first := renderHeaderValue("{{uuid}}", HeaderVars{})
	second := renderHeaderValue("{{uuid}}", HeaderVars{})

	if !strings.HasPrefix(first, sessionKeyPrefix) || !strings.HasPrefix(second, sessionKeyPrefix) {
		t.Fatalf("{{uuid}} 未生成带前缀的值: %q, %q", first, second)
	}
	if first == second {
		t.Errorf("{{uuid}} 两次求值相同: %q", first)
	}
}

func TestRenderHeaderValueEmptySessionFallsBack(t *testing.T) {
	// 已识别占位符但求值为空 → 回落随机值，保证上游不会收到空 header
	got := renderHeaderValue("{{session}}", HeaderVars{Session: ""})
	if !strings.HasPrefix(got, sessionKeyPrefix) {
		t.Fatalf("renderHeaderValue() = %q, want 随机兜底", got)
	}

	// 纯空白渲染结果同样兜底
	got = renderHeaderValue("{{session_id}}", HeaderVars{SessionID: ""})
	if !strings.HasPrefix(got, sessionKeyPrefix) {
		t.Fatalf("renderHeaderValue() = %q, want 随机兜底", got)
	}
}

func TestRenderHeaderValueZeroAuthKeyID(t *testing.T) {
	// auth_key_id 为 0（admin token 调用）时仍应给出非空值，不做兜底替换
	if got := renderHeaderValue("{{auth_key_id}}", HeaderVars{AuthKeyID: 0}); got != "0" {
		t.Fatalf("renderHeaderValue() = %q, want \"0\"", got)
	}
}

// ── BuildHeaders：自定义头注入与非空保证 ──

func TestBuildHeadersRendersSessionTemplate(t *testing.T) {
	vars := HeaderVars{Session: "llmio-session-1", Model: "m1"}

	header := BuildHeaders(http.Header{}, false, map[string]string{
		"x-opencode-session": "{{session}}",
		"x-static":           "keep",
	}, false, vars)

	if got := header.Get("x-opencode-session"); got != "llmio-session-1" {
		t.Errorf("x-opencode-session = %q, want llmio-session-1", got)
	}
	if got := header.Get("x-static"); got != "keep" {
		t.Errorf("x-static = %q, want keep", got)
	}
}

func TestBuildHeadersConfiguredHeaderIsNeverEmpty(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "空字面量", value: ""},
		{name: "空白字面量", value: "   "},
		{name: "求值为空的占位符", value: "{{session}}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := BuildHeaders(http.Header{}, false, map[string]string{
				"x-opencode-session": tt.value,
			}, false, HeaderVars{})
			if got := header.Get("x-opencode-session"); got == "" {
				t.Fatal("已配置的自定义头为空值，上游会返回 400")
			}
		})
	}
}

func TestBuildHeadersTemplateOverridesForwardedHeader(t *testing.T) {
	// 自定义头求值发生在透传克隆之后，因此模板值应覆盖同名透传头
	src := http.Header{"X-Opencode-Session": []string{"from-client"}}
	header := BuildHeaders(src, true, map[string]string{
		"X-Opencode-Session": "{{session}}",
	}, false, HeaderVars{Session: "llmio-wins"})

	if got := header.Get("X-Opencode-Session"); got != "llmio-wins" {
		t.Errorf("X-Opencode-Session = %q, want llmio-wins（配置应覆盖透传）", got)
	}
}

func TestBuildHeadersLiteralStillStripsAcceptEncoding(t *testing.T) {
	// 模板求值不得重新引入 Accept-Encoding（该项目曾因此记录 gzip 二进制导致乱码）
	header := BuildHeaders(http.Header{}, false, map[string]string{
		"Accept-Encoding": "{{session}}",
	}, false, HeaderVars{Session: "s"})

	if got := header.Get("Accept-Encoding"); got != "" {
		t.Errorf("Accept-Encoding = %q, want empty", got)
	}
}

// ── 端到端：mock 一个强制要求会话头的上游 ──

// 复现 opencode 的行为：缺少会话头或值为空时返回 400 MissingSessionID
func TestSessionHeaderSatisfiesStrictUpstream(t *testing.T) {
	const sessionHeader = "x-opencode-session"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get(sessionHeader)) == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"type":"error","error":{"type":"MissingSessionID"}}`)
			return
		}
		io.WriteString(w, `{"id":"ok","choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()

	call := func(t *testing.T, customHeaders map[string]string, vars HeaderVars) (int, string) {
		t.Helper()
		header := BuildHeaders(http.Header{}, false, customHeaders, false, vars)
		req, err := http.NewRequest(http.MethodPost, upstream.URL, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header = header

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return res.StatusCode, string(body)
	}

	t.Run("未配置会话头时上游拒绝", func(t *testing.T) {
		status, _ := call(t, map[string]string{}, HeaderVars{})
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400（复现问题现象）", status)
		}
	})

	t.Run("静态值可以通过但会话不区分", func(t *testing.T) {
		static := map[string]string{sessionHeader: "llmio-fixed"}
		status, _ := call(t, static, HeaderVars{})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		// 静态值不随会话变化，这正是需要模板化的原因
		first := BuildHeaders(http.Header{}, false, static, false, HeaderVars{}).Get(sessionHeader)
		second := BuildHeaders(http.Header{}, false, static, false, HeaderVars{Session: "another"}).Get(sessionHeader)
		if first != second {
			t.Errorf("静态值不应随会话变化: %q != %q", first, second)
		}
	})

	t.Run("模板值通过且随会话变化", func(t *testing.T) {
		tmpl := map[string]string{sessionHeader: "{{session}}"}

		raw := []byte(`{"messages":[{"role":"user","content":"端到端对话"}]}`)
		key := ResolveSessionKey(nil, "", "mock-model", 1, raw)

		status, body := call(t, tmpl, HeaderVars{Session: key})
		if status != http.StatusOK {
			t.Fatalf("status = %d body=%s, want 200", status, body)
		}
		if got := BuildHeaders(http.Header{}, false, tmpl, false, HeaderVars{Session: key}).Get(sessionHeader); got != key {
			t.Errorf("session header = %q, want %q", got, key)
		}
		if other := BuildHeaders(http.Header{}, false, tmpl, false, HeaderVars{Session: "sess-2"}).Get(sessionHeader); other == key {
			t.Error("不同会话应得到不同的 header 值")
		}
	})
}

// stubRandomChars 临时替换随机源，返回还原函数。
func stubRandomChars(fn func(int) (string, error)) func() {
	original := randomChars
	randomChars = fn
	return func() { randomChars = original }
}
