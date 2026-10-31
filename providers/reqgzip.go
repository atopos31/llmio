package providers

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
)

// MinRequestGzipBytes 是请求体压缩的最小体积。
// 小请求压缩后的收益不足以抵消头部与 CPU 开销。
const MinRequestGzipBytes = 4096

// GzipRequestBody 把已经构建好的请求体替换为 gzip 压缩后的字节。
//
// 调用方保证：
//   - 请求体是明文（BuildHeaders 已删除入站 Content-Encoding）；
//   - 压缩字节数会影响 ContentLength，因此必须重建 Body 与 GetBody。
//
// Header 会被复制后再修改：调用方的 header map 可能在同一提供商的
// 自愈与重试之间复用，直接写入会让后续明文请求带上错误的编码头。
func GzipRequestBody(req *http.Request) error {
	if req == nil || req.Body == nil {
		return errors.New("gzip request body: empty body")
	}
	if encoding := strings.TrimSpace(req.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return errors.New("gzip request body: request already has content encoding")
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	closeErr := req.Body.Close()

	restore := func() {
		req.ContentLength = int64(len(raw))
		req.Body = io.NopCloser(bytes.NewReader(raw))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(raw)), nil
		}
	}
	if closeErr != nil {
		restore()
		return closeErr
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		restore()
		return err
	}
	if err := zw.Close(); err != nil {
		restore()
		return err
	}
	payload := buf.Bytes()

	if req.Header == nil {
		req.Header = make(http.Header)
	} else {
		req.Header = req.Header.Clone()
	}
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Del("Content-Length")
	req.ContentLength = int64(len(payload))
	req.Body = io.NopCloser(bytes.NewReader(payload))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
	return nil
}
