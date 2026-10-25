package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
)

// ForwardTarget 是透传目标的最小描述：从 Provider.Config 解出 base_url / api_key，
// 再按类型补齐上游鉴权头。不透传任何客户端头——鉴权由本结构重新注入，
// 避免客户端凭证串味与走私头（hop-by-hop 之类）进入上游。
type ForwardTarget struct {
	Type    string // consts.Style*
	BaseURL string
	APIKey  string
	Version string // 仅 anthropic 使用
	Proxy   string
}

// ForwardTargetFromConfig 按类型解析 Provider.Config（JSON 字符串）。
// 各类型的 config 字段名统一为 base_url / api_key（anthropic 另有 version）。
func ForwardTargetFromConfig(providerType, config, proxy string) (ForwardTarget, error) {
	cfg := gjson.Parse(config)
	target := ForwardTarget{
		Type:    providerType,
		BaseURL: strings.TrimRight(cfg.Get("base_url").String(), "/"),
		APIKey:  cfg.Get("api_key").String(),
		Version: cfg.Get("version").String(),
		Proxy:   proxy,
	}
	if target.BaseURL == "" {
		return target, fmt.Errorf("provider config missing base_url")
	}
	return target, nil
}

// setAuthHeaders 按类型注入上游鉴权头，与各 Provider.BuildReq 的约定一致。
func (f *ForwardTarget) setAuthHeaders(h http.Header) {
	switch f.Type {
	case "anthropic":
		h.Set("x-api-key", f.APIKey)
		if f.Version != "" {
			h.Set("anthropic-version", f.Version)
		}
	case "gemini":
		h.Set("x-goog-api-key", f.APIKey)
	default: // openai / openai-res
		h.Set("Authorization", fmt.Sprintf("Bearer %s", f.APIKey))
	}
}

// Forward 向 target 的 {base_url}{path} 发出请求并返回上游响应。
//
// 调用方负责读取/回传响应体并 Close。path 必须通过 SanitizeForwardPath 校验，
// query 允许为 nil。Accept 是唯一协商性透传头（SSE 客户端依赖它）；contentType
// 为空时对非空 body 默认 application/json。
func Forward(ctx context.Context, target ForwardTarget, method, path string, query url.Values, accept, contentType string, body []byte) (*http.Response, error) {
	path, err := SanitizeForwardPath(path)
	if err != nil {
		return nil, err
	}
	targetURL := target.BaseURL + path
	if len(query) > 0 {
		targetURL += "?" + query.Encode()
	}

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reqBody)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	target.setAuthHeaders(req.Header)

	client := GetClient(0, target.Proxy)
	return client.Do(req)
}

// SanitizeForwardPath 校验待透传的路径片段，杜绝 path traversal 与控制字符注入。
// 规则：必须以 / 开头、不含 ASCII 控制字符、不含 ".." 段。
func SanitizeForwardPath(path string) (string, error) {
	if path == "" || path[0] != '/' {
		return "", fmt.Errorf("invalid forward path: %q", path)
	}
	for i := 0; i < len(path); i++ {
		if path[i] < 0x20 || path[i] == 0x7f {
			return "", fmt.Errorf("invalid forward path: control character")
		}
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return "", fmt.Errorf("invalid forward path: traversal segment")
		}
	}
	return path, nil
}
