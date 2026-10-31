package providers

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGzipRequestBody(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("请求体", 200) + `"}]}`)
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	originalHeader := http.Header{"X-Test": []string{"1"}}
	req.Header = originalHeader

	if err := GzipRequestBody(req); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if got := req.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if req.Header.Get("Content-Length") != "" {
		t.Fatal("Content-Length 头应删除，由 Request.ContentLength 表达")
	}
	if req.ContentLength <= 0 || req.ContentLength >= int64(len(raw)) {
		t.Fatalf("ContentLength 异常: %d", req.ContentLength)
	}
	if originalHeader.Get("Content-Encoding") != "" {
		t.Fatal("调用方的 header map 被污染了")
	}

	compressed, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("解压结果与原请求体不一致")
	}

	again, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	againBytes, err := io.ReadAll(again)
	if err != nil {
		t.Fatalf("read GetBody: %v", err)
	}
	if !bytes.Equal(againBytes, compressed) {
		t.Fatal("GetBody 必须返回同一份压缩字节")
	}
}

func TestGzipRequestBodyRejectsExistingEncoding(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewReader([]byte("body")))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Encoding", "gzip")
	if err := GzipRequestBody(req); err == nil {
		t.Fatal("已有 Content-Encoding 时必须拒绝，避免双重压缩")
	}
}

func TestMinRequestGzipBytes(t *testing.T) {
	if MinRequestGzipBytes != 4096 {
		t.Fatalf("MinRequestGzipBytes = %d, want 4096", MinRequestGzipBytes)
	}
}
