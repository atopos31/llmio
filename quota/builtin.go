package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 内置适配器：官方用量/余额接口。全部归一化为契约行后再交给 Normalize。
//
// 为什么复杂登录型的源必须是 Go 内置而不是脚本（计划 §3.4 方案 B）：
// scnet 要 RSA 加密 SSO 登录 + 逐跳跟随重定向 + cookie jar，opencode 要会话
// Cookie + x-org-id。这些用 goja 写要么做不了（无 crypto、无 cookie jar），
// 要么把复杂登录态留在沙箱里难以调试。用 Go 原生实现健壮、可测、可断言。
// goja 因此退化为「接冷门供应商又不想重编译」的逃生舱——这正是它应有的定位。
//
// 「自定义 path」：每个内置适配器都有默认路径，但可在数据源配置里覆盖
// （path / method / query / headers）。若官方接口返回结构与适配器预期不符，
// 还可用 itemsPath + map 直接套通用映射（与 HTTP 类型同一套逻辑）。

// BuiltinInfo 描述一个内置适配器，供前端列出可选项。
type BuiltinInfo struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Verified       bool   `json:"verified"`
	Doc            string `json:"doc"`
	DefaultBaseURL string `json:"defaultBaseUrl"`
	DefaultPath    string `json:"defaultPath"`
	// Generic 为真表示"自定义端点"类型：必须由用户给 path 与字段映射。
	Generic bool `json:"generic"`
	// NeedsLogin 为真表示该适配器需要账号会话（env 里要填账号口令），
	// 前端据此显示不同的表单字段。
	NeedsLogin bool `json:"needsLogin"`
	// EnvKeys 是该适配器从 env 白名单读取的键，供前端提示。
	EnvKeys []string `json:"envKeys,omitempty"`
}

// builtinSpec 是注册表的一项。
type builtinSpec struct {
	info BuiltinInfo
	// defaultTimeout 该适配器的默认超时（登录型流程较慢）。
	defaultTimeout time.Duration
}

// builtinRegistry 是内置适配器注册表。
//
// 顺序即前端展示顺序：先已实测的，再未实测的，最后是通用占位。
var builtinRegistry = []builtinSpec{
	{
		info: BuiltinInfo{
			ID: "deepseek", Label: "DeepSeek 余额", Verified: true,
			Doc:            "GET /user/balance",
			DefaultBaseURL: "https://api.deepseek.com",
			DefaultPath:    "/user/balance",
		},
		defaultTimeout: 20 * time.Second,
	},
	{
		info: BuiltinInfo{
			ID: "moonshot", Label: "Moonshot / Kimi 余额", Verified: false,
			Doc:            "GET /users/me/balance",
			DefaultBaseURL: "https://api.moonshot.cn/v1",
			DefaultPath:    "/users/me/balance",
		},
		defaultTimeout: 20 * time.Second,
	},
	{
		info: BuiltinInfo{
			ID: "scnet", Label: "国家超算 TokenPlan（登录型）", Verified: false,
			Doc:            "走控制台会话：RSA 加密 SSO 登录 + cookie jar。需填账号口令",
			DefaultBaseURL: "https://www.scnet.cn",
			DefaultPath:    "",
			NeedsLogin:     true,
			EnvKeys:        []string{"SCNET_USER", "SCNET_PASS"},
		},
		defaultTimeout: 45 * time.Second,
	},
	{
		info: BuiltinInfo{
			ID: "opencode", Label: "opencode.ai 套餐（登录型）", Verified: false,
			Doc:            "GET /console/api/go/status。需填会话 Cookie 与 x-org-id",
			DefaultBaseURL: "https://opencode.ai",
			DefaultPath:    "/console/api/go/status",
			NeedsLogin:     true,
			EnvKeys:        []string{"OPENCODE_COOKIE", "OPENCODE_ORG_ID"},
		},
		defaultTimeout: 20 * time.Second,
	},
	{
		info: BuiltinInfo{
			ID: "custom", Label: "自定义端点（配 path + 字段映射）", Verified: true,
			Doc:            "自己填 path / 请求头，并用 itemsPath + 字段映射解析响应",
			DefaultBaseURL: "https://api.example.com",
			DefaultPath:    "/v1/usage",
			Generic:        true,
		},
		defaultTimeout: 20 * time.Second,
	},
}

// ListBuiltins 返回可用的内置适配器清单。
func ListBuiltins() []BuiltinInfo {
	out := make([]BuiltinInfo, 0, len(builtinRegistry))
	for _, s := range builtinRegistry {
		out = append(out, s.info)
	}
	return out
}

func findBuiltin(id string) (builtinSpec, bool) {
	for _, s := range builtinRegistry {
		if s.info.ID == id {
			return s, true
		}
	}
	return builtinSpec{}, false
}

// BuiltinConfig 是调用内置适配器的参数。
type BuiltinConfig struct {
	BuiltinID string
	BaseURL   string
	APIKey    string
	Path      string
	Method    string
	Query     any
	Headers   map[string]any
	ItemsPath string
	Map       map[string]any
	TimeoutMs int
	// Env 是登录型适配器读取的账号/会话白名单。**不继承进程环境**。
	Env map[string]string
	// ID / Name 供占位符与诊断使用。
	ID   string
	Name string
}

// RunBuiltin 执行一次内置适配器取数。
//
// client 为 nil 时按适配器默认超时新建。登录型适配器会自建带 cookie jar 的 client。
func RunBuiltin(cfg BuiltinConfig, client *http.Client) (*HTTPAdapterResult, error) {
	spec, ok := findBuiltin(cfg.BuiltinID)
	if !ok {
		return nil, fmt.Errorf("未知内置适配器：%s", cfg.BuiltinID)
	}

	// 自定义端点必须自己给路径
	if spec.info.Generic && cfg.Path == "" {
		return nil, fmt.Errorf("「自定义端点」类型需要填写请求路径(path)")
	}

	base := cfg.BaseURL
	if base == "" {
		base = spec.info.DefaultBaseURL
	}
	path := cfg.Path
	if path == "" {
		path = spec.info.DefaultPath
	}

	switch cfg.BuiltinID {
	case "scnet":
		return runSCNet(cfg, base)
	case "opencode":
		return runOpencode(cfg, base)
	}

	// deepseek / moonshot / custom：都是「拉一个 JSON 端点再解析」
	timeout := spec.defaultTimeout
	if cfg.TimeoutMs > 0 {
		timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	data, err := fetchJSON(joinURL(base, path, cfg.Query), cfg.APIKey, cfg.Headers, timeout, client)
	if err != nil {
		return nil, err
	}

	// 通用映射的三种情形：
	//  - custom 类型（必须，它没有自带解析）
	//  - 内置类型但用户显式给了 itemsPath / map（官方结构与预期不符时自救）
	if spec.info.Generic || cfg.ItemsPath != "" || len(cfg.Map) > 0 {
		items, err := extractItems(data, HTTPAdapterConfig{
			ItemsPath: cfg.ItemsPath,
			Map:       cfg.Map,
		})
		if err != nil {
			return nil, err
		}
		return &HTTPAdapterResult{Items: items, Raw: data}, nil
	}

	items, err := parseBuiltin(cfg.BuiltinID, data)
	if err != nil {
		return nil, err
	}
	return &HTTPAdapterResult{Items: items, Raw: data}, nil
}

// parseBuiltin 是各内置适配器自己的响应解析。
//
// 产出的是**契约行**（map），不是归一后的 Item——归一只在契约那一层做。
func parseBuiltin(id string, data any) ([]map[string]any, error) {
	switch id {
	case "deepseek":
		return parseDeepseek(data)
	case "moonshot":
		return parseMoonshot(data)
	}
	// 注意：custom 永远不会走到这里——它在 RunBuiltin 里已被通用映射分支拦下。
	return nil, fmt.Errorf("内置适配器 %s 没有解析实现", id)
}

// parseDeepseek 解析 DeepSeek 余额。
//
// GET {base}/user/balance
// → { is_available, balance_infos:[{currency,total_balance,granted_balance,topped_up_balance}] }
func parseDeepseek(data any) ([]map[string]any, error) {
	root, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("DeepSeek 余额响应不是对象")
	}
	infos, _ := root["balance_infos"].([]any)

	if len(infos) == 0 {
		// 没有余额明细时，至少把「账户是否可用」报出来——
		// 不可用是明确信号（欠费/停用），比空列表有用。
		available := deepseekAvailable(root)
		row := map[string]any{
			"id": "available", "label": "账户可用", "unit": "", "window": "total",
			"extra": map[string]any{"isAvailable": available},
		}
		if available {
			row["status"] = string(StatusUnknown)
		} else {
			// 不可用即余额为 0，给一个明确的数值而不是"未知"
			row["remaining"] = float64(0)
			row["status"] = string(StatusExhausted)
		}
		return []map[string]any{row}, nil
	}

	out := make([]map[string]any, 0, len(infos))
	for _, e := range infos {
		b, ok := e.(map[string]any)
		if !ok {
			continue
		}
		currency := toStr(b["currency"])
		row := map[string]any{
			"id":        "balance-" + currency,
			"label":     fmt.Sprintf("余额 (%s)", currency),
			"unit":      currency,
			"window":    string(WindowTotal),
			"format":    "{remaining} {unit}",
			"status":    string(StatusExhausted),
			"extra":     map[string]any{"granted": b["granted_balance"], "toppedUp": b["topped_up_balance"], "isAvailable": deepseekAvailable(root)},
			"remaining": b["total_balance"],
		}
		// 余额大于 0 才算 ok；否则保持 exhausted
		if n := toFloat(b["total_balance"]); n != nil && *n > 0 {
			row["status"] = string(StatusOK)
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("DeepSeek 响应里的 balance_infos 没有可用条目")
	}
	return out, nil
}

func deepseekAvailable(root map[string]any) bool {
	b, _ := root["is_available"].(bool)
	return b
}

// parseMoonshot 解析 Moonshot / Kimi 余额。
//
// GET {base}/users/me/balance（base 为 OpenAI 兼容根，含 /v1）
// → { data: { available_balance, voucher_balance, cash_balance } }
func parseMoonshot(data any) ([]map[string]any, error) {
	root, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Moonshot 余额响应不是对象")
	}
	// 有的部署直接给顶层，有的包一层 data
	d := root
	if inner, ok := root["data"].(map[string]any); ok {
		d = inner
	}

	total, has := firstPresent(d, "available_balance", "availableBalance")
	if !has {
		return nil, fmt.Errorf("Moonshot 响应里没有 available_balance")
	}
	row := map[string]any{
		"id":        "balance",
		"label":     "账户余额",
		"remaining": total,
		"unit":      string(UnitCNY),
		"window":    string(WindowTotal),
		"format":    "{remaining} {unit}",
		"status":    string(StatusExhausted),
		"extra":     map[string]any{"voucher": firstOrNil(d, "voucher_balance"), "cash": firstOrNil(d, "cash_balance")},
	}
	if n := toFloat(total); n != nil && *n > 0 {
		row["status"] = string(StatusOK)
	}
	return []map[string]any{row}, nil
}

// firstPresent 按顺序取第一个存在的键。
func firstPresent(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func firstOrNil(m map[string]any, keys ...string) any {
	v, _ := firstPresent(m, keys...)
	return v
}

// ---------------------------------------------------------------------------
// HTTP 基础设施（内置适配器共用）
// ---------------------------------------------------------------------------

// joinURL 拼接 base 与 path，并附上 query。
//
// query 允许对象或字符串：对象转成 URL 查询串（跳过空值），
// 字符串去掉前导 ? 后原样附加。与前端配置的两种写法对应。
func joinURL(base, path string, query any) string {
	b := strings.TrimSuffix(base, "/")
	p := ""
	if path != "" {
		if strings.HasPrefix(path, "/") {
			p = path
		} else {
			p = "/" + path
		}
	}
	u := b + p

	qs := ""
	switch q := query.(type) {
	case map[string]any:
		// 键排序保证同一配置每次发出同样的 URL（便于缓存与排查）
		vals := url.Values{}
		for _, k := range SortedKeys(q) {
			v := toStr(q[k])
			if v == "" {
				continue
			}
			vals.Set(k, v)
		}
		qs = vals.Encode()
	case string:
		qs = strings.TrimPrefix(q, "?")
	}
	if qs != "" {
		if strings.Contains(u, "?") {
			u += "&" + qs
		} else {
			u += "?" + qs
		}
	}
	return u
}

// fetchJSON 拉一个 JSON 端点。失败时错误里带上响应片段。
func fetchJSON(u, apiKey string, headers map[string]any, timeout time.Duration, client *http.Client) (any, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, Fill(toStrForHeader(v), HTTPAdapterVars{APIKey: apiKey}))
	}

	if client == nil {
		if timeout <= 0 {
			timeout = DefaultHTTPTimeout
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
	return data, nil
}

// formEncode 构造 application/x-www-form-urlencoded 请求体。
// 值按传入顺序写出，不排序——登录表单的字段顺序与原实现保持一致。
func formEncode(pairs [][2]string) io.Reader {
	var b bytes.Buffer
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p[0]))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p[1]))
	}
	return &b
}
