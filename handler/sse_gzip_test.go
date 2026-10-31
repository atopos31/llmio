package handler

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
)

func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"GZIP", true},
		{"gzip, deflate, br", true},
		{"br, gzip;q=0.5", true},
		{"gzip;q=0", false},
		{"gzip;q=0, *;q=1", false},
		{"*", true},
		{"*;q=0", false},
		{"identity", false},
		{"identity;q=1", false},
		{"br", false},
		{"x-gzip", true},
		{"gzip;q=0.000", false},
		{"deflate;q=1, gzip;q=0.001", true},
	}
	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			if got := acceptsGzip(tc.header); got != tc.want {
				t.Fatalf("acceptsGzip(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

type stubGinWriter struct {
	header  http.Header
	body    bytes.Buffer
	status  int
	size    int
	written bool
	flushes int
}

func newStubGinWriter() *stubGinWriter {
	return &stubGinWriter{header: http.Header{}}
}

func (s *stubGinWriter) Header() http.Header { return s.header }

func (s *stubGinWriter) Write(p []byte) (int, error) {
	s.written = true
	s.size += len(p)
	return s.body.Write(p)
}

func (s *stubGinWriter) WriteString(v string) (int, error) { return s.Write([]byte(v)) }

func (s *stubGinWriter) WriteHeader(code int) { s.status = code }

func (s *stubGinWriter) WriteHeaderNow() { s.written = true }

func (s *stubGinWriter) Status() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

func (s *stubGinWriter) Size() int { return s.size }

func (s *stubGinWriter) Written() bool { return s.written }

func (s *stubGinWriter) Flush() { s.flushes++ }

func (s *stubGinWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("not supported")
}

func (s *stubGinWriter) CloseNotify() <-chan bool { return make(chan bool) }

func (s *stubGinWriter) Pusher() http.Pusher { return nil }

func TestSSEGzipWriterWritesCompressedStreamAndFlushes(t *testing.T) {
	stub := newStubGinWriter()
	w := newSSEGzipWriter(stub)
	w.Header().Set("Content-Length", "123")

	first := []byte("data: {\"i\":0}\n\n")
	second := []byte("data: {\"i\":1}\n\n")
	if _, err := w.Write(first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if _, err := w.Write(second); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if stub.flushes < 2 {
		t.Fatalf("每次 Write 必须刷新底层连接，flushes = %d", stub.flushes)
	}
	if got := w.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length 应删除，got %q", got)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close twice: %v", err)
	}

	zr, err := gzip.NewReader(bytes.NewReader(stub.body.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if !bytes.Equal(decoded, append(first, second...)) {
		t.Fatalf("解压内容不一致: %q", decoded)
	}
}

func TestWriteHeaderSSEGzip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	header := http.Header{
		"Content-Length": {"999"},
		"X-Upstream":     {"1"},
		"Vary":           {"Origin"},
	}
	writeHeader(c, true, true, header)

	got := rec.Header()
	if got.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got.Get("Content-Encoding"))
	}
	if got.Get("Content-Length") != "" {
		t.Errorf("Content-Length = %q, want empty", got.Get("Content-Length"))
	}
	if got.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q", got.Get("Content-Type"))
	}
	if got.Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q", got.Get("X-Accel-Buffering"))
	}
	if got.Get("X-Upstream") != "1" {
		t.Errorf("X-Upstream = %q", got.Get("X-Upstream"))
	}
	vary := strings.Join(got.Values("Vary"), ",")
	if !strings.Contains(vary, "Accept-Encoding") || !strings.Contains(vary, "Origin") {
		t.Errorf("Vary = %q, want Origin + Accept-Encoding", vary)
	}
}

func TestWriteHeaderKeepsContentLengthWhenNotCompressing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	writeHeader(c, false, false, http.Header{"Content-Length": {"42"}})
	if got := rec.Header().Get("Content-Length"); got != "42" {
		t.Fatalf("未压缩时应保留上游 Content-Length，got %q", got)
	}
}

// 走完整 chatHandler，确认 SSE 压缩的协商、响应头与解压内容。
func TestChatHandlerSSEGzipNegotiation(t *testing.T) {
	tests := []struct {
		name     string
		policy   string
		accept   string
		wantGzip bool
	}{
		{
			name:     "声明 gzip 且开关开启",
			policy:   `{"enabled":true,"sse_response":true,"upstream_request":false}`,
			accept:   "gzip, deflate",
			wantGzip: true,
		},
		{
			name:     "未声明 gzip",
			policy:   `{"enabled":true,"sse_response":true,"upstream_request":false}`,
			accept:   "",
			wantGzip: false,
		},
		{
			name:     "声明 gzip 但总开关关闭",
			policy:   `{"enabled":false,"sse_response":true,"upstream_request":false}`,
			accept:   "gzip",
			wantGzip: false,
		},
		{
			name:     "gzip;q=0",
			policy:   `{"enabled":true,"sse_response":true,"upstream_request":false}`,
			accept:   "gzip;q=0",
			wantGzip: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			t.Cleanup(upstream.Close)

			setupErrstageDB(t, upstream.URL)
			if err := models.DB.Create(&models.Config{
				Key:   models.KeyTrafficCompressionPolicy,
				Value: tc.policy,
			}).Error; err != nil {
				t.Fatalf("create policy: %v", err)
			}

			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			ctx := context.WithValue(context.Background(), consts.ContextKeyAllowAllModel, true)
			body := `{"model":"m-test","messages":[{"role":"user","content":"hi"}],"stream":true}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.accept != "" {
				c.Request.Header.Set("Accept-Encoding", tc.accept)
			}

			ChatCompletionsHandler(c)
			waitFor(t, "日志结算", func() bool {
				var log models.ChatLog
				err := models.DB.Where("status <> ?", consts.StatusRunning).First(&log).Error
				return err == nil
			})

			bodyBytes := rec.Body.Bytes()
			if tc.wantGzip {
				if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
					t.Fatalf("Content-Encoding = %q, want gzip", got)
				}
				if !strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), "Accept-Encoding") {
					t.Fatal("缺少 Vary: Accept-Encoding")
				}
				zr, err := gzip.NewReader(bytes.NewReader(bodyBytes))
				if err != nil {
					t.Fatalf("gzip reader: %v", err)
				}
				decoded, err := io.ReadAll(zr)
				if err != nil {
					t.Fatalf("gunzip: %v", err)
				}
				if !strings.Contains(string(decoded), "data:") {
					t.Fatalf("解压后不是 SSE：%q", decoded)
				}
			} else {
				if got := rec.Header().Get("Content-Encoding"); got != "" {
					t.Fatalf("Content-Encoding = %q, want empty", got)
				}
				if !strings.Contains(string(bodyBytes), "data:") {
					t.Fatalf("明文响应里没有 SSE：%q", bodyBytes)
				}
			}
		})
	}
}
