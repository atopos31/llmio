package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// forwardResponseHeaderTimeout 只限制"等上游响应头"的时间，不设 Client.Timeout。
//
// 整体超时会把长响应切断：透传的 SSE 可以连续吐几十分钟，chat 管线里的长推理同理。
// 响应头超时足以让连不上的上游尽快失败（连接建立另有 dialer 的 30s 兜底），
// 连接一旦建立、响应头一到，读 body 的时间就没有上限。
const forwardResponseHeaderTimeout = 60 * time.Second

// ForwardTarget 是透传目标的最小描述：从 Provider.Config 解出 base_url / api_key，
// 再按类型补齐上游鉴权头。客户端凭据（Authorization / x-api-key / x-goog-api-key）
// 一律不转发，鉴权由本结构重新注入，避免客户端凭证串味。
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
// 必须最后调用：它覆盖同名头，客户端带上来的一律作废。
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

// forwardDroppedHeaders 请求侧不转发给上游的头，键为 http.CanonicalHeaderKey 形态。
//
// 两类：一是客户端凭据（上游鉴权由 setAuthHeaders 重新注入，客户端的不许串味），
// 二是逐跳头与由本跳自行决定的头（RFC 7230 §6.1，见 forwardRequestHeaders 与
// handler 里的响应中继）。
var forwardDroppedHeaders = map[string]struct{}{
	"Authorization":       {},
	"X-Api-Key":           {},
	"X-Goog-Api-Key":      {},
	"Cookie":              {},
	"Cookie2":             {},
	"Host":                {},
	"Content-Length":      {},
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Te":                  {},
	"Trailer":             {},
	"Proxy-Authorization": {},
	"Proxy-Authenticate":  {},
	"Accept-Encoding":     {},
}

// forwardRequestHeaders 按"默认全转发 + 黑名单剥离"构造上游请求头。
//
// 除 forwardDroppedHeaders 与 Connection 头点名的头之外一律原样带过去：透传的
// 价值就在于客户端能用上游原生能力，anthropic-beta 这类特性头、x-* 自定义头
// 一旦被吞掉，专属地址就只能跑最小子集。Content-Encoding 保留：请求体是按字节
// 原样转发的，上游得靠它才知道要不要解压。
//
// Accept-Encoding 是刻意剥离的例外：原样转给上游会拿回压缩响应体，日志里的 usage
// 抽不出来、ChatIO 还会把 gzip 二进制当文本记录；剥掉后由 http.Transport 自行协商
// 并透明解压，客户端拿到的是明文（所有客户端都接受）。
func forwardRequestHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	named := ConnectionNamedHeaders(src)
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if _, drop := forwardDroppedHeaders[canonical]; drop {
			continue
		}
		if _, drop := named[canonical]; drop {
			continue
		}
		for _, value := range values {
			dst.Add(canonical, value)
		}
	}
	return dst
}

// ConnectionNamedHeaders 返回 Connection 头里点名的头（RFC 7230 §6.1，已规范化）。
//
// 这些头由当前这一跳决定、不能被对端提前声明，请求与响应两侧都要剔除；
// 两侧共用一份实现，免得两边各写一个子集。
func ConnectionNamedHeaders(h http.Header) map[string]struct{} {
	named := make(map[string]struct{})
	for _, connection := range h.Values("Connection") {
		for _, token := range strings.Split(connection, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			named[http.CanonicalHeaderKey(token)] = struct{}{}
		}
	}
	return named
}

// forwardSensitiveQueryKeys 转发前必须从 query 里删掉的参数名（大小写不敏感）。
//
// 有的客户端把凭据写在 query（Gemini 官方 SDK 就支持 ?key=...），原样转发一份等于
// 把 llmio 的密钥递给上游；其余 query 必须保留——Gemini 的 ?alt=sse 之类是协议参数。
var forwardSensitiveQueryKeys = map[string]struct{}{
	"key":           {},
	"api_key":       {},
	"apikey":        {},
	"access_token":  {},
	"token":         {},
	"authorization": {},
}

// sanitizeForwardQuery 去掉 query 里的凭据参数，其余原样保留。
func sanitizeForwardQuery(query url.Values) url.Values {
	if len(query) == 0 {
		return nil
	}
	clean := make(url.Values, len(query))
	for key, values := range query {
		if _, drop := forwardSensitiveQueryKeys[strings.ToLower(key)]; drop {
			continue
		}
		clean[key] = values
	}
	return clean
}

// Forward 向 target 的 {base_url}{path} 发出请求并返回上游响应。
//
// 调用方负责读取/回传响应体并 Close。path 必须通过 SanitizeForwardPath 校验，
// query 允许为 nil。header 来自客户端请求（c.Request.Header）：默认全转发，
// 按 forwardRequestHeaders 的规则剥离凭据/逐跳头；Content-Type 为空时对非空
// body 默认 application/json；最后用提供商自己的凭据覆盖鉴权头。
func Forward(ctx context.Context, target ForwardTarget, method, path string, query url.Values, header http.Header, body []byte) (*http.Response, error) {
	path, err := SanitizeForwardPath(path)
	if err != nil {
		return nil, err
	}
	targetURL := target.BaseURL + path
	if q := sanitizeForwardQuery(query); len(q) > 0 {
		targetURL += "?" + q.Encode()
	}

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header = forwardRequestHeaders(header)
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	target.setAuthHeaders(req.Header)

	return forwardClient(target.Proxy).Do(req)
}

// forwardClient 取带响应头超时的共享 client，克隆一份后挂上重定向策略。
//
// GetClient 返回的是进程内共享的 *http.Client，就地改 CheckRedirect 会污染其他调用方，
// 所以只在副本上设置。共享的是 Transport（连接池），克隆开销可以忽略。
//
// 重定向策略：只允许同 host 跟随。x-api-key / x-goog-api-key 在 Go 的重定向实现里
// 不如 Authorization 那样被自动剥掉，跨主机跟随就会把上游凭据交到别的主机手里；
// 跨主机直接报错（502），不跟随。
func forwardClient(proxy string) *http.Client {
	client := *GetClient(forwardResponseHeaderTimeout, proxy)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if first := via[0]; req.URL.Host != first.URL.Host {
			return fmt.Errorf("refuse cross-host redirect: %s -> %s", first.URL.Host, req.URL.Host)
		}
		return nil
	}
	return &client
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

// forwardVersionSegmentPattern 识别 base_url 末尾的版本段：v1 / v1beta / v2 / v1alpha2 等。
var forwardVersionSegmentPattern = regexp.MustCompile("^v[0-9]+([a-z]+[0-9]*)?$")

// NormalizeForwardPath 把客户端在专属地址上写的路径归一化成上游 base_url 的相对路径。
//
// llmio 各类型的 base_url 模板都自带版本段（openai / openai-res / anthropic 是 .../v1，
// gemini 是 .../v1beta），BuildReq 只在其后拼 /chat/completions、/messages、
// /models/{model}:generateContent；而专属地址的客户端把版本段写在路径里（OpenAI 客户端
// base_url 约定为 /p/{name}/v1，Gemini 客户端是 /p/{name}/v1beta）。所以转发前要剥掉
// 与 base_url 末尾版本段相同的那一级，否则会拼出 /v1/v1/...、/v1beta/v1beta/...。
//
// 只认完整段（后随段边界）：base_url 是 .../v1beta 时 /v1beta/models/... 剥成
// /models/...；路径里的 /v1 不会被误剥。base_url 末尾不是版本段（自定义网关）时
// 回落到既有行为——剥掉开头一级 /v1。剥完为空（客户端只访问了版本段本身）时回落 "/"。
func NormalizeForwardPath(path, baseURL string) string {
	if segment := baseURLVersionSegment(baseURL); segment != "" {
		if rest, ok := cutForwardSegment(path, "/"+segment); ok {
			return rest
		}
	}
	// 回落到既有行为：客户端写了 /v1 而 base_url 没有版本段（或写的段不匹配）。
	if rest, ok := cutForwardSegment(path, "/v1"); ok {
		return rest
	}
	return path
}

// baseURLVersionSegment 取 base_url 路径的最后一段，且仅当它形如版本段时返回。
func baseURLVersionSegment(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return ""
	}
	segment := trimmed[idx+1:]
	if !forwardVersionSegmentPattern.MatchString(segment) {
		return ""
	}
	return segment
}

// cutForwardSegment 剥掉路径开头的一级 prefix（如 "/v1beta"），要求整段匹配。
// 返回 (剥后路径, 是否命中)；只有版本段本身时返回 "/"。
func cutForwardSegment(path, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok || (rest != "" && rest[0] != '/') {
		return path, false
	}
	if rest == "" {
		return "/", true
	}
	return rest, true
}
