package quota

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// HTTP 适配器：不写代码，纯配置地拉一个接口并映射字段。
//
// 移植自 dashboard 的 `adapters/http.js`。它只做两件事：
//  1. 按配置把请求发出去（占位符插值 + 四型鉴权）
//  2. 用 itemsPath / map 从响应里挑出条目
//
// **不做归一**：产出的仍是"像上游 JSON 那样"的原始行（map），
// 交给 contract.go 的 Normalize。契约是唯一的归一点，HTTP 类型不另起一套，
// 否则宽松别名、三者任意两个、状态派生这些语义会出现第二份实现并漂移。
//
// 这与原 JS 版一致：那边 runHttp 返回 {items} 后同样交给 normalizeResult。

// HTTPAdapterConfig 是 http 类型数据源的配置。
//
// 字段命名对应原 JS 的 cfg：url / method / headers / body / auth / itemsPath / map / constants。
type HTTPAdapterConfig struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Method string `json:"method"`
	// Headers 的值支持占位符插值。
	Headers map[string]any `json:"headers"`
	// Body 仅在非 GET 时发送。字符串按字面量（插值后）发；对象序列化为 JSON。
	Body any      `json:"body"`
	Auth HTTPAuth `json:"auth"`
	// Query 是附加在 URL 之后的查询参数，与内置适配器同一套写法（对象或字符串）。
	//
	// 这一项原先只有内置适配器有：http 类型的编辑器让你填、配置里也存下来了，
	// 但 toHTTPConfig 不拷贝、这里也不读，于是填了等于没填，而且没有任何提示。
	Query any `json:"query"`
	// ItemsPath 是数组所在的点路径；留空表示整个响应体就是条目容器。
	ItemsPath string `json:"itemsPath"`
	// Map 是「输出字段 → 上游路径」的映射。值以 "=" 开头表示字面量。
	Map map[string]any `json:"map"`
	// Constants 是固定补全的字段（unit / window 之类）。
	Constants map[string]any `json:"constants"`
	// TimeoutMs 单次请求超时（毫秒）。<=0 用 DefaultHTTPTimeout。
	TimeoutMs int `json:"timeoutMs"`
}

// HTTPAuth 是鉴权配置。
type HTTPAuth struct {
	// Type 取 bearer | header | basic | none。留空时：有密钥按 bearer，否则 none。
	Type string `json:"type"`
	// Header 是承载凭证的头名。bearer 默认 Authorization、header 默认 x-api-key。
	Header string `json:"header"`
	// User 仅 basic 用。
	User string `json:"user"`
	// Token 支持占位符；留空默认 {{apiKey}}。
	Token string `json:"token"`
}

// DefaultHTTPTimeout 是 HTTP 数据源的默认超时。
const DefaultHTTPTimeout = 20 * time.Second

// HTTPAdapterVars 是占位符可用的变量集。
type HTTPAdapterVars struct {
	APIKey  string
	BaseURL string
	ID      string
	Name    string
}

// Fill 替换 {{var}} 占位符。未提供的占位符**原样保留**——
// 原实现如此，好处是配置写错时能在界面上看见 {{apiKey}} 而不是变成空串难以察觉。
//
// 变量名限 \w（字母数字下划线），与 JS 版的 /\{\{(\w+)\}\}/ 一致。
func Fill(s string, vars HTTPAdapterVars) string {
	known := map[string]string{
		"apiKey":  vars.APIKey,
		"baseUrl": vars.BaseURL,
		"id":      vars.ID,
		"name":    vars.Name,
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		// 找下一个 "{{"
		if i+1 < len(s) && s[i] == '{' && s[i+1] == '{' {
			// 找配对的 "}}"
			end := -1
			for j := i + 2; j+1 < len(s); j++ {
				if s[j] == '}' && s[j+1] == '}' {
					end = j
					break
				}
			}
			if end >= 0 {
				key := s[i+2 : end]
				if isValidVarName(key) {
					// 已提供的变量做替换；未提供的原样保留
					if _, ok := known[key]; ok {
						b.WriteString(known[key])
						i = end + 2
						continue
					}
					// 未知变量名：原样保留整段
					b.WriteString(s[i : end+2])
					i = end + 2
					continue
				}
			}
			// 不是合法占位符，按普通字符处理
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isValidVarName 判断占位符内部是否是合法变量名（\w+）。
func isValidVarName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// Pick 按点路径取值。路径为空返回 nil（对应 JS 的 undefined）。
//
// 与 JS 版的差别：那边的 pick 对 map 之外的中间值会拿到 undefined 再往下取，
// Go 里 map 没有这个方法，因此中间遇到非 map 直接判为取不到——
// 语义一致（都是"取不到"），实现不同。
func Pick(obj any, pathStr string) any {
	if pathStr == "" {
		return nil
	}
	parts := strings.Split(pathStr, ".")
	cur := obj
	for _, p := range parts {
		if p == "" {
			// 空段对应 JS 里 filter(p => p !== '') 的忽略
			continue
		}
		// 支持数组下标写法 a.0.b —— 上游返回数组时很常见。
		// 必须先判数组：若先做 map 断言，数组下标分支永远走不到。
		if arr, ok := cur.([]any); ok {
			idx, err := parseIndex(p)
			if err != nil || idx < 0 || idx >= len(arr) {
				return nil
			}
			cur = arr[idx]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := m[p]
		if !ok {
			return nil
		}
		cur = v
	}
	return cur
}

// parseIndex 把纯数字段解析成数组下标。
func parseIndex(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("空")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("含非数字")
		}
	}
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
		if n > 1<<20 {
			return 0, fmt.Errorf("下标过大")
		}
	}
	return n, nil
}

// MapRow 按 map 配置把一行上游对象映射成契约行。
//
// 值以 "=" 开头表示字面量（去掉前缀直接当值）。空值跳过——
// 原实现 `if (!src) continue`。
func MapRow(row map[string]any, mapping map[string]any) map[string]any {
	out := map[string]any{}
	for key, src := range mapping {
		s, ok := src.(string)
		if !ok || s == "" {
			// 原 JS 也只在值为真值时才处理；非字符串直接跳过
			continue
		}
		if strings.HasPrefix(s, "=") {
			out[key] = s[1:]
			continue
		}
		if v := Pick(row, s); v != nil {
			out[key] = v
		}
	}
	return out
}

// HTTPAdapterResult 是 HTTP 取数的结果。
type HTTPAdapterResult struct {
	// Items 是映射后的原始行，尚未归一。交给 Normalize。
	Items []map[string]any
	// Raw 是完整响应体，供试跑面板回显。
	Raw any
}

// RunHTTP 执行一次 HTTP 取数。
func RunHTTP(cfg HTTPAdapterConfig, vars HTTPAdapterVars, client *http.Client) (*HTTPAdapterResult, error) {
	urlStr := Fill(cfg.URL, vars)
	if urlStr == "" {
		return nil, fmt.Errorf("未配置 url")
	}
	// 查询参数附加在填好占位符的 URL 之后；两种写法（对象 / 字符串）与内置适配器
	// 共用 joinURL，因此两边对"空值跳过""已有 ? 时接 &"的判定不会各自漂移。
	urlStr = joinURL(urlStr, "", cfg.Query)

	headers := map[string]string{"Accept": "application/json"}
	for k, v := range cfg.Headers {
		headers[k] = Fill(toStrForHeader(v), vars)
	}

	// 鉴权
	authType := strings.ToLower(strings.TrimSpace(cfg.Auth.Type))
	if authType == "" {
		if vars.APIKey != "" {
			authType = "bearer"
		} else {
			authType = "none"
		}
	}
	tokenTpl := cfg.Auth.Token
	if tokenTpl == "" {
		tokenTpl = "{{apiKey}}"
	}
	token := Fill(tokenTpl, vars)
	switch authType {
	case "bearer":
		if token != "" {
			name := cfg.Auth.Header
			if name == "" {
				name = "Authorization"
			}
			headers[name] = "Bearer " + token
		}
	case "header":
		if token != "" {
			name := cfg.Auth.Header
			if name == "" {
				name = "x-api-key"
			}
			headers[name] = token
		}
	case "basic":
		user := Fill(cfg.Auth.User, vars)
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
	case "none":
		// 不加
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader
	if method != http.MethodGet && cfg.Body != nil {
		if _, ok := headers["Content-Type"]; !ok {
			headers["Content-Type"] = "application/json"
		}
		switch b := cfg.Body.(type) {
		case string:
			bodyReader = strings.NewReader(Fill(b, vars))
		default:
			// 原实现 JSON.stringify(cfg.body)，不插值对象内部
			encoded, err := json.Marshal(cfg.Body)
			if err != nil {
				return nil, fmt.Errorf("请求体无法序列化: %w", err)
			}
			bodyReader = bytes.NewReader(encoded)
		}
	}

	req, err := http.NewRequest(method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if client == nil {
		timeout := DefaultHTTPTimeout
		if cfg.TimeoutMs > 0 {
			timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
		}
		client = &http.Client{Timeout: timeout}
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer res.Body.Close()

	text, err := io.ReadAll(io.LimitReader(res.Body, maxHTTPBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if int64(len(text)) > maxHTTPBodyBytes {
		return nil, fmt.Errorf("响应体超过 %d 字节上限", maxHTTPBodyBytes)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, truncateForError(string(text)))
	}

	var data any
	if err := json.Unmarshal(text, &data); err != nil {
		return nil, fmt.Errorf("响应不是 JSON: %s", truncateForError(string(text)))
	}

	items, err := extractItems(data, cfg)
	if err != nil {
		return nil, err
	}
	return &HTTPAdapterResult{Items: items, Raw: data}, nil
}

// maxHTTPBodyBytes 限制响应体大小：余量接口的响应都很小，
// 给足余量的同时避免误连到返回大文件的地址时把内存吃掉。
//
// 声明为变量而非常量，便于测试把上限调低来覆盖超限分支——
// 真实上限 8MiB 在测试里要造好几 MB 数据。与沙箱的 maxFetchBytes 同一套做法。
var maxHTTPBodyBytes int64 = 8 << 20 // 8MiB

// extractItems 从响应里挑出条目行。
//
// rowsFromMap 的键序问题在这里同样存在：原 JS 用 Object.entries 的顺序，
// Go 的 map 无序，因此按路径字符串排序后输出，保证同一份配置每次顺序稳定。
func extractItems(data any, cfg HTTPAdapterConfig) ([]map[string]any, error) {
	picked := data
	if cfg.ItemsPath != "" {
		picked = Pick(data, cfg.ItemsPath)
	}

	var list []any
	switch v := picked.(type) {
	case []any:
		list = v
	case map[string]any:
		list = []any{v}
	default:
		return nil, fmt.Errorf("itemsPath「%s」未取到数组或对象", itemsPathLabel(cfg.ItemsPath))
	}

	out := make([]map[string]any, 0, len(list))
	for i, e := range list {
		row, ok := e.(map[string]any)
		if !ok {
			// 非对象元素直接跳过。原 JS 版会为它生成一条空条目，
			// 到归一那步同样因为没有数值而被丢弃，结果与这里一致；
			// 但不猜成 remaining —— 数组元素是标量通常意味着 itemsPath 配错了，
			// 显示成一个看似合理的数字比报错更危险。
			continue
		}
		merged := map[string]any{}
		for k, v := range cfg.Constants {
			merged[k] = v
		}
		if len(cfg.Map) == 0 {
			// 未配字段映射时整行透传，交给契约按别名识别。
			//
			// 契约存在的意义就是认得 used/usedAmount/usage/已用 这一类写法。
			// 若对端本就返回接近契约形状的结构（自建网关很常见），
			// 这时写一份 map 反而是多余的。真认不出时契约会明确报错，不会静默。
			for k, v := range row {
				merged[k] = v
			}
		} else {
			for k, v := range MapRow(row, cfg.Map) {
				merged[k] = v
			}
		}
		// id 按**原始下标**编号（跳过元素后不重排），与原实现一致
		if s := toStr(merged["id"]); s == "" {
			merged["id"] = fmt.Sprintf("item-%d", i+1)
		}
		if s := toStr(merged["label"]); s == "" {
			merged["label"] = toStr(merged["id"])
		}
		out = append(out, merged)
	}
	return out, nil
}

func itemsPathLabel(p string) string {
	if p == "" {
		return "(空)"
	}
	return p
}

// toStrForHeader 把 header 值转成字符串。原实现 String(v)。
func toStrForHeader(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// SortedKeys 返回 map 的排序键，用于需要稳定顺序的场合（如常量回显）。
func SortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
