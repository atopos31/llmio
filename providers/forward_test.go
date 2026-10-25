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
			res, err := Forward(context.Background(), target, http.MethodPost, "/alpha/search", nil, "", "", []byte(`{"q":"x"}`))
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
			if _, err := Forward(context.Background(), target, http.MethodPost, tt.path, nil, "", "", nil); err == nil {
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

	res, err := Forward(context.Background(), target, http.MethodPost, "/alpha/search", query, "text/event-stream", "", []byte(body))
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
