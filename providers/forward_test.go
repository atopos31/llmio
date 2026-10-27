package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// forwardRecorder 记录假上游收到的请求。与 handler 包的 upstreamRecorder 同理：
// 上游在另一个 goroutine 里跑，读写需要锁。
type forwardRecorder struct {
	mu      sync.Mutex
	reqs    []http.Request
	bodies  []string
	served  int
	handler http.HandlerFunc
}

func newForwardRecorder() *forwardRecorder {
	r := &forwardRecorder{}
	r.handler = func(w http.ResponseWriter, req *http.Request) {
		body := ""
		if req.Body != nil {
			buf := make([]byte, 4096)
			n, _ := req.Body.Read(buf)
			body = string(buf[:n])
		}
		r.mu.Lock()
		r.reqs = append(r.reqs, *req)
		r.bodies = append(r.bodies, body)
		r.served++
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}
	return r
}

func (r *forwardRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.served
}

func TestForwardTargetFromConfig(t *testing.T) {
	tests := []struct {
		name      string
		providerT string
		config    string
		wantBase  string
		wantKey   string
		wantVer   string
		wantErr   bool
	}{
		{
			name:      "openai 配置解析并去掉尾部斜杠",
			providerT: "openai",
			config:    `{"base_url":"https://api.example.com/v1/","api_key":"sk-test"}`,
			wantBase:  "https://api.example.com/v1",
			wantKey:   "sk-test",
		},
		{
			name:      "anthropic 带 version",
			providerT: "anthropic",
			config:    `{"base_url":"https://api.anthropic.com","api_key":"ak","version":"2023-06-01"}`,
			wantBase:  "https://api.anthropic.com",
			wantKey:   "ak",
			wantVer:   "2023-06-01",
		},
		{
			name:      "缺 base_url 报错",
			providerT: "openai",
			config:    `{"api_key":"sk"}`,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := ForwardTargetFromConfig(tt.providerT, tt.config, "")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际成功: %+v", target)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if target.BaseURL != tt.wantBase {
				t.Errorf("BaseURL = %q, want %q", target.BaseURL, tt.wantBase)
			}
			if target.APIKey != tt.wantKey {
				t.Errorf("APIKey = %q, want %q", target.APIKey, tt.wantKey)
			}
			if target.Version != tt.wantVer {
				t.Errorf("Version = %q, want %q", target.Version, tt.wantVer)
			}
		})
	}
}

func TestForwardInjectsAuthHeadersPerType(t *testing.T) {
	tests := []struct {
		name        string
		providerT   string
		config      string
		check       func(h http.Header) bool
		description string
	}{
		{
			name:      "openai 走 Bearer",
			providerT: "openai",
			config:    `{"base_url":"PLACEHOLDER","api_key":"sk-bearer"}`,
			check: func(h http.Header) bool {
				return h.Get("Authorization") == "Bearer sk-bearer"
			},
			description: "Authorization: Bearer",
		},
		{
			name:      "openai-res 走 Bearer",
			providerT: "openai-res",
			config:    `{"base_url":"PLACEHOLDER","api_key":"sk-res"}`,
			check: func(h http.Header) bool {
				return h.Get("Authorization") == "Bearer sk-res"
			},
			description: "Authorization: Bearer",
		},
		{
			name:      "anthropic 走 x-api-key 与 version",
			providerT: "anthropic",
			config:    `{"base_url":"PLACEHOLDER","api_key":"ak-1","version":"2023-06-01"}`,
			check: func(h http.Header) bool {
				return h.Get("x-api-key") == "ak-1" && h.Get("anthropic-version") == "2023-06-01"
			},
			description: "x-api-key + anthropic-version",
		},
		{
			name:      "gemini 走 x-goog-api-key",
			providerT: "gemini",
			config:    `{"base_url":"PLACEHOLDER","api_key":"g-key"}`,
			check: func(h http.Header) bool {
				return h.Get("x-goog-api-key") == "g-key"
			},
			description: "x-goog-api-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newForwardRecorder()
			server := httptest.NewServer(rec.handler)
			defer server.Close()

			target, err := ForwardTargetFromConfig(tt.providerT, replaceBaseURL(tt.config, server.URL), "")
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			res, err := Forward(context.Background(), target, http.MethodPost, "/alpha/search", nil, nil, []byte(`{"q":"x"}`))
			if err != nil {
				t.Fatalf("转发失败: %v", err)
			}
			res.Body.Close()

			rec.mu.Lock()
			defer rec.mu.Unlock()
			if len(rec.reqs) == 0 {
				t.Fatal("上游没有收到请求")
			}
			if !tt.check(rec.reqs[0].Header) {
				t.Errorf("鉴权头不符合 %s 的约定: %v", tt.description, rec.reqs[0].Header)
			}
			if rec.reqs[0].Header.Get("Content-Type") != "application/json" {
				t.Errorf("JSON body 应带 Content-Type: %v", rec.reqs[0].Header)
			}
		})
	}
}

func TestForwardRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"traversal 段", "/alpha/../secret"},
		{"裸 traversal", "/.."},
		{"不以斜杠开头", "alpha/search"},
		{"空路径", ""},
		{"控制字符", "/alpha/sea\x00rch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := ForwardTarget{Type: "openai", BaseURL: "https://example.com"}
			if _, err := Forward(context.Background(), target, http.MethodPost, tt.path, nil, nil, nil); err == nil {
				t.Fatalf("路径 %q 应被拒绝", tt.path)
			}
		})
	}
}

func TestForwardPassesQueryAndBody(t *testing.T) {
	rec := newForwardRecorder()
	server := httptest.NewServer(rec.handler)
	defer server.Close()

	target := ForwardTarget{Type: "openai", BaseURL: server.URL}
	body := `{"model":"latest","commands":{"search_query":[{"q":"llmio"}]}}`
	query := url.Values{"stream": []string{"true"}}
	header := http.Header{"Accept": []string{"text/event-stream"}}

	res, err := Forward(context.Background(), target, http.MethodPost, "/alpha/search", query, header, []byte(body))
	if err != nil {
		t.Fatalf("转发失败: %v", err)
	}
	res.Body.Close()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) == 0 {
		t.Fatal("上游没有收到请求")
	}
	got := rec.reqs[0]
	if got.URL.Path != "/alpha/search" {
		t.Errorf("path = %q, want /alpha/search", got.URL.Path)
	}
	if got.URL.Query().Get("stream") != "true" {
		t.Errorf("query stream = %q, want true", got.URL.Query().Get("stream"))
	}
	if got.Header.Get("Accept") != "text/event-stream" {
		t.Errorf("Accept 应原样透传: %q", got.Header.Get("Accept"))
	}
	if rec.bodies[0] != body {
		t.Errorf("body 被改动: %q", rec.bodies[0])
	}
}

// replaceBaseURL 把配置模板里的占位 base_url 换成测试服务器地址。
func replaceBaseURL(config, base string) string {
	return strings.Replace(config, "PLACEHOLDER", base, 1)
}

// 转发路径归一化：客户端把版本段写在路径里（/p/{slug}/v1 或 /p/{slug}/v1beta），
// 而上游 base_url 自带版本段，拼接前必须剥掉与 base_url 相同的那一级，否则上游
// 收到 /v1/v1/...、/v1beta/v1beta/...。base_url 没有版本段时保留既有 /v1 剥离行为。
func TestNormalizeForwardPath(t *testing.T) {
	const (
		baseV1        = "https://api.example.com/v1"
		baseV1Beta    = "https://generativelanguage.googleapis.com/v1beta"
		baseNoVersion = "https://gateway.example.com"
	)
	tests := []struct {
		name    string
		path    string
		baseURL string
		want    string
	}{
		{"base 带 /v1：剥掉开头一级 /v1", "/v1/chat/completions", baseV1, "/chat/completions"},
		{"base 带 /v1：只剥一级", "/v1/v1/models", baseV1, "/v1/models"},
		{"base 带 /v1：只访问 /v1 本身回落根", "/v1", baseV1, "/"},
		{"base 带 /v1beta：剥掉 /v1beta 避免翻倍", "/v1beta/models/gemini-2.5-pro:generateContent", baseV1Beta, "/models/gemini-2.5-pro:generateContent"},
		{"base 带 /v1beta：只访问版本段本身回落根", "/v1beta", baseV1Beta, "/"},
		{"base 带 /v1beta：只认完整段，/v1beta2 原样", "/v1beta2/models/x", baseV1Beta, "/v1beta2/models/x"},
		{"base 带 /v1beta：客户端写 /v1 时回落剥 /v1", "/v1/models", baseV1Beta, "/models"},
		{"base 无版本段：剥 /v1", "/v1/chat/completions", baseNoVersion, "/chat/completions"},
		{"base 无版本段：/v1beta 原样", "/v1beta/models/x", baseNoVersion, "/v1beta/models/x"},
		{"base 带尾斜杠：仍按版本段剥", "/v1beta/models/x", baseV1Beta + "/", "/models/x"},
		{"没有版本段原样", "/chat/completions", baseV1, "/chat/completions"},
		{"根路径原样", "/", baseV1, "/"},
		{"空路径原样", "", baseV1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeForwardPath(tt.path, tt.baseURL); got != tt.want {
				t.Errorf("NormalizeForwardPath(%q, %q) = %q, want %q", tt.path, tt.baseURL, got, tt.want)
			}
		})
	}
}

// 头转发策略：默认全转发，只剥离客户端凭据与逐跳头。anthropic-beta / x-* 这类
// 上游特性头必须到上游；客户端 Authorization / x-api-key / x-goog-api-key / Cookie
// 不能串味，提供商自己的鉴权头最后覆盖。
func TestForwardForwardsClientHeadersButStripsCredentials(t *testing.T) {
	rec := newForwardRecorder()
	server := httptest.NewServer(rec.handler)
	defer server.Close()

	target := ForwardTarget{Type: "anthropic", BaseURL: server.URL, APIKey: "ak-upstream"}
	header := http.Header{
		"Anthropic-Beta":   {"feature-2025-01-01"},
		"X-Custom-Trace":   {"trace-123"},
		"Authorization":    {"Bearer client-token"},
		"X-Api-Key":        {"client-key"},
		"X-Goog-Api-Key":   {"client-goog-key"},
		"Cookie":           {"session=client"},
		"Cookie2":          {"legacy=client"},
		"Connection":       {"X-Drop-Me"},
		"X-Drop-Me":        {"must-not-arrive"},
		"Content-Encoding": {"gzip"},
	}
	res, err := Forward(context.Background(), target, http.MethodPost, "/v1/messages", nil, header, []byte(`{"model":"x"}`))
	if err != nil {
		t.Fatalf("转发失败: %v", err)
	}
	res.Body.Close()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) == 0 {
		t.Fatal("上游没有收到请求")
	}
	got := rec.reqs[0].Header
	if got.Get("Anthropic-Beta") != "feature-2025-01-01" {
		t.Errorf("anthropic-beta 特性头应透传, got %q", got.Get("Anthropic-Beta"))
	}
	if got.Get("X-Custom-Trace") != "trace-123" {
		t.Errorf("x-* 自定义头应透传, got %q", got.Get("X-Custom-Trace"))
	}
	if got.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding 应保留（请求体按字节转发）, got %q", got.Get("Content-Encoding"))
	}
	if got.Get("X-Api-Key") != "ak-upstream" {
		t.Errorf("客户端 x-api-key 必须被提供商凭据覆盖, got %q", got.Get("X-Api-Key"))
	}
	if got.Get("Authorization") != "" {
		t.Errorf("客户端 Authorization 不得到上游, got %q", got.Get("Authorization"))
	}
	if got.Get("X-Goog-Api-Key") != "" {
		t.Errorf("客户端 x-goog-api-key 不得到上游, got %q", got.Get("X-Goog-Api-Key"))
	}
	if got.Get("Cookie") != "" || got.Get("Cookie2") != "" {
		t.Errorf("Cookie 不得到上游, got Cookie=%q Cookie2=%q", got.Get("Cookie"), got.Get("Cookie2"))
	}
	if got.Get("X-Drop-Me") != "" {
		t.Errorf("Connection 头点名的头不得到上游, got %q", got.Get("X-Drop-Me"))
	}
	if got.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type 为空且有 body 时应补 application/json, got %q", got.Get("Content-Type"))
	}
}

// query 里的凭据参数转发前必须删掉，其余（Gemini 的 alt=sse 等协议参数）原样保留。
func TestForwardStripsCredentialQuery(t *testing.T) {
	rec := newForwardRecorder()
	server := httptest.NewServer(rec.handler)
	defer server.Close()

	target := ForwardTarget{Type: "gemini", BaseURL: server.URL, APIKey: "g-key"}
	query := url.Values{
		"key":     {"leak"},
		"API_KEY": {"leak-upper"},
		"token":   {"leak-token"},
		"alt":     {"sse"},
	}
	res, err := Forward(context.Background(), target, http.MethodPost,
		"/v1beta/models/gemini-2.5-pro:generateContent", query, nil, []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatalf("转发失败: %v", err)
	}
	res.Body.Close()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	got := rec.reqs[0].URL.Query()
	if got.Get("alt") != "sse" {
		t.Errorf("非凭据 query 应原样保留, got alt=%q", got.Get("alt"))
	}
	for _, key := range []string{"key", "API_KEY", "token"} {
		if _, ok := got[key]; ok {
			t.Errorf("query 凭据 %q 不应转发给上游: %v", key, got)
		}
	}
	if len(got) != 1 {
		t.Errorf("上游 query 只应剩 alt=sse, got %v", got)
	}
}

// 跨主机重定向必须拒绝：x-api-key / x-goog-api-key 不像 Authorization 那样会被
// Go 自动剥掉，跟随就会把上游凭据交给另一台主机。
func TestForwardRefusesCrossHostRedirect(t *testing.T) {
	leaked := newForwardRecorder()
	other := httptest.NewServer(leaked.handler)
	defer other.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/messages", http.StatusFound)
	}))
	defer redirector.Close()

	target := ForwardTarget{Type: "anthropic", BaseURL: redirector.URL, APIKey: "ak-secret"}
	if _, err := Forward(context.Background(), target, http.MethodPost, "/v1/messages", nil, nil, []byte(`{"model":"x"}`)); err == nil {
		t.Fatal("跨主机重定向应返回错误，不应跟随")
	}
	if n := leaked.count(); n != 0 {
		t.Fatalf("不应跟到另一台主机，实际收到 %d 个请求", n)
	}
}

// 同 host 重定向照常跟随（拒绝的是跨主机，不是所有重定向）。
func TestForwardFollowsSameHostRedirect(t *testing.T) {
	rec := newForwardRecorder()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			http.Redirect(w, r, "/after", http.StatusFound)
			return
		}
		rec.handler(w, r)
	}))
	defer server.Close()

	target := ForwardTarget{Type: "openai", BaseURL: server.URL, APIKey: "sk"}
	res, err := Forward(context.Background(), target, http.MethodGet, "/v1/messages", nil, nil, nil)
	if err != nil {
		t.Fatalf("同 host 重定向应跟随: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("最终状态 = %d, want 200", res.StatusCode)
	}
	if rec.count() == 0 {
		t.Fatal("重定向后应请求到 /after")
	}
}
