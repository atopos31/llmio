package service

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/providers"
	"github.com/tidwall/gjson"
)

func TestBuildUpstreamBodyDeletesSessionID(t *testing.T) {
	raw := []byte(`{"model":"local-model","session_id":"owu-session","messages":[{"role":"user","content":"hi"}]}`)
	body, err := buildUpstreamBody(raw, map[string]any{
		"temperature": 0.2,
		"session_id":  "extra-session",
	})
	if err != nil {
		t.Fatalf("buildUpstreamBody() error = %v", err)
	}

	if gjson.GetBytes(body, "session_id").Exists() {
		t.Fatalf("session_id should not be sent upstream, body=%s", string(body))
	}
	if got := gjson.GetBytes(body, "model").String(); got != "local-model" {
		t.Fatalf("model=%q, want local-model", got)
	}
	if got := gjson.GetBytes(body, "temperature").Float(); got != 0.2 {
		t.Fatalf("temperature=%v, want 0.2", got)
	}
}

// Accept-Encoding 必须被剔除：Go Transport 只在自己协商该头时才透明解压响应。
// 若透传客户端的 Accept-Encoding，上游返回的 gzip 字节会被记录进 ChatIO（乱码），
// 且 usage 无法解析（token 统计为 0）。
func TestBuildHeadersStripsAcceptEncoding(t *testing.T) {
	src := http.Header{}
	src.Set("Accept-Encoding", "gzip, deflate, br")
	src.Set("Accept", "application/json")
	src.Set("X-Trace", "keep-me")

	t.Run("透传模式下剔除且保留其他头", func(t *testing.T) {
		header := BuildHeaders(src, true, nil, false, HeaderVars{})
		if got := header.Get("Accept-Encoding"); got != "" {
			t.Errorf("Accept-Encoding = %q, want empty", got)
		}
		if got := header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		if got := header.Get("X-Trace"); got != "keep-me" {
			t.Errorf("X-Trace = %q, want keep-me", got)
		}
	})

	t.Run("非透传模式下为空", func(t *testing.T) {
		header := BuildHeaders(src, false, nil, false, HeaderVars{})
		if got := header.Get("Accept-Encoding"); got != "" {
			t.Errorf("Accept-Encoding = %q, want empty", got)
		}
	})

	t.Run("自定义头无法重新引入", func(t *testing.T) {
		header := BuildHeaders(src, true, map[string]string{"Accept-Encoding": "gzip"}, false, HeaderVars{})
		if got := header.Get("Accept-Encoding"); got != "" {
			t.Errorf("Accept-Encoding = %q, want empty even when set via customer headers", got)
		}
	})

	t.Run("流式请求关闭 Nginx 缓冲", func(t *testing.T) {
		header := BuildHeaders(src, false, nil, true, HeaderVars{})
		if got := header.Get("X-Accel-Buffering"); got != "no" {
			t.Errorf("X-Accel-Buffering = %q, want no", got)
		}
	})

	t.Run("非流式请求不设置缓冲头", func(t *testing.T) {
		header := BuildHeaders(src, false, nil, false, HeaderVars{})
		if got := header.Get("X-Accel-Buffering"); got != "" {
			t.Errorf("X-Accel-Buffering = %q, want empty", got)
		}
	})
}

// 端到端复现：mock 一个按 Accept-Encoding 返回 gzip 的上游（真实网关/CDN 的常见行为），
// 验证 BuildHeaders → Transport → ProcesserOpenAI 全链路拿到的是明文 JSON 且 usage 可解析。
func TestBuildHeadersKeepsGzipResponseReadable(t *testing.T) {
	const wantContent = "llmio-gzip-check"
	payload := fmt.Sprintf(
		`{"model":"mock-model","choices":[{"message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":11,"completion_tokens":22,"total_tokens":33}}`,
		wantContent,
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 仅当上游请求声明支持 gzip 时才压缩，模拟真实上游行为
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			if _, err := gz.Write([]byte(payload)); err != nil {
				t.Errorf("write gzip payload: %v", err)
			}
			if err := gz.Close(); err != nil {
				t.Errorf("close gzip writer: %v", err)
			}
			return
		}
		if _, err := io.WriteString(w, payload); err != nil {
			t.Errorf("write payload: %v", err)
		}
	}))
	defer upstream.Close()

	// 模拟 Python AsyncOpenAI / httpx 等客户端：入站请求自带 Accept-Encoding
	src := http.Header{}
	src.Set("Accept-Encoding", "gzip, deflate")
	src.Set("Content-Type", "application/json")

	// withHeader=true 即开启请求头透传的关联配置
	header := BuildHeaders(src, true, nil, false, HeaderVars{})

	req, err := http.NewRequest(http.MethodPost, upstream.URL, strings.NewReader(`{"model":"mock-model"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header = header

	res, err := providers.GetClient(10*time.Second, "").Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer res.Body.Close()

	if !res.Uncompressed {
		t.Errorf("res.Uncompressed = false，上游响应未被透明解压，ChatIO 将记录 gzip 二进制")
	}

	// 与 handler/chat.go 一致：用 Processer 解析原始响应体
	log, output, err := ProcesserOpenAI(context.Background(), res.Body, false, time.Now())
	if err != nil {
		t.Fatalf("ProcesserOpenAI() error = %v", err)
	}

	if output.OfString != payload {
		t.Fatalf("记录到的输出不是明文 JSON\n got: %q\nwant: %q", output.OfString, payload)
	}
	if got := gjson.Get(output.OfString, "choices.0.message.content").String(); got != wantContent {
		t.Errorf("content = %q, want %q", got, wantContent)
	}
	if log.TotalTokens != 33 {
		t.Errorf("TotalTokens = %d, want 33（usage 解析失败会导致 token 统计为 0）", log.TotalTokens)
	}
}
