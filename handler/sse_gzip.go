package handler

import (
	"compress/gzip"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// acceptsGzip 解析 Accept-Encoding 的 q 值。
//
// 不能只用 strings.Contains：`gzip;q=0` 表示客户端明确拒绝 gzip，
// 而 `*;q=0` 表示拒绝所有未显式列出的编码。未声明该头时按未压缩处理，
// 不返回 406——压缩是优化，不是请求成立的前提。
func acceptsGzip(header string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}

	gzipQ := -1.0
	wildcardQ := -1.0
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding == "" {
			continue
		}
		q := 1.0
		for _, param := range fields[1:] {
			param = strings.TrimSpace(param)
			if strings.HasPrefix(strings.ToLower(param), "q=") {
				value, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64)
				if err != nil {
					q = 0
				} else {
					q = value
				}
			}
		}
		switch coding {
		case "gzip", "x-gzip":
			if q > gzipQ {
				gzipQ = q
			}
		case "*":
			if q > wildcardQ {
				wildcardQ = q
			}
		}
	}

	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return wildcardQ > 0
}

// sseGzipWriter 把 SSE 写出包装成 gzip 流，并在每次 Write 后同步刷新。
//
// SSE 的可用性依赖事件按时到达，因此不能等缓冲区满再压缩；每次 Write
// 都执行一次 sync flush，再刷新底层连接。压缩率会因此低于整段压缩，
// 但事件时序与未压缩时保持一致。
type sseGzipWriter struct {
	gin.ResponseWriter
	gz         *gzip.Writer
	closed     bool
	plainBytes int64
	startSize  int
}

var _ gin.ResponseWriter = (*sseGzipWriter)(nil)

func newSSEGzipWriter(w gin.ResponseWriter) *sseGzipWriter {
	start := w.Size()
	if start < 0 {
		start = 0
	}
	return &sseGzipWriter{
		ResponseWriter: w,
		gz:             gzip.NewWriter(w),
		startSize:      start,
	}
}

func (w *sseGzipWriter) WriteHeader(code int) {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

func (w *sseGzipWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *sseGzipWriter) Write(p []byte) (int, error) {
	w.Header().Del("Content-Length")
	n, err := w.gz.Write(p)
	w.plainBytes += int64(n)
	if err != nil {
		return n, err
	}
	if err := w.gz.Flush(); err != nil {
		return n, err
	}
	w.ResponseWriter.Flush()
	return n, nil
}

// Stats 返回本次流写入的明文与压缩后字节数。
// compressed 包含 gzip 头尾，按底层 writer 的 Size 增量计算。
func (w *sseGzipWriter) Stats() (plain, compressed int64) {
	compressed = int64(w.ResponseWriter.Size() - w.startSize)
	if compressed < 0 {
		compressed = 0
	}
	return w.plainBytes, compressed
}

func (w *sseGzipWriter) Flush() {
	_ = w.gz.Flush()
	w.ResponseWriter.Flush()
}

// Close 写入 gzip 结尾标记，并保证重复调用安全。
func (w *sseGzipWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	err := w.gz.Close()
	w.ResponseWriter.Flush()
	return err
}
