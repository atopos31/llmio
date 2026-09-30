package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// opencode.ai（opencode zen / go 套餐）余量。
//
// 接口：GET {base}/console/api/go/status
//
// 实测（2026-09-28）必需的请求要素只有两样：
//   - Cookie: __Host-console_session=<值>   真正管鉴权的会话
//   - 请求头: x-org-id: wrk_xxxxxxxx        缺失返回 400，填错返回 404
//
// 其余头（accept / referer / sec-* / user-agent / dnt / priority / traceparent…）
// 实测都不影响结果，这里只保留 accept 与 user-agent 以防上游加风控。
//
// Cookie 里的 `auth=`（Fe26.2**…）不是必需的，且它带签名有效期，
// 过期后反而会干扰判断，因此默认不发它。
//
// 返回结构（节选）：
//
//	{
//	  product: "go", renewalCurrency: "usd",
//	  access: { startsAt, endsAt,
//	    meters: {
//	      fiveHour: { resetsAt, limitMicroCents, usedMicroCents },
//	      week:     { resetsAt, limitMicroCents, usedMicroCents },
//	      month:    { resetsAt, limitMicroCents, usedMicroCents }
//	    } } }
//
// 金额单位是 microCents（微分）：1 USD = 100 分 = 1e8 microCents。

const opencodeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36"

// microCentsPerUSD 是 microCents 到 USD 的换算基数。
const microCentsPerUSD = 1e8

// sessionCookieRe 从一串 Cookie 里抽出 __Host-console_session 的值。
var sessionCookieRe = regexp.MustCompile(`__Host-console_session=([^;\s]+)`)

// uuidRe 判断裸 uuid（用户可能只贴了会话 id 本体）。
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// opencodeSessionValue 把用户给的会话值规整成 st_ 开头的形式。
//
// 允许三种输入（面板上用户手边有什么就贴什么）：
//
//	① 裸会话值 st_xxx        → 原样
//
// ② 误传整条 Cookie 串     → 抽出 __Host-console_session 的值
//
//	③ 裸 uuid                → 补上 st_ 前缀
func opencodeSessionValue(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "st_") {
		return s
	}
	if m := sessionCookieRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	if uuidRe.MatchString(s) {
		return "st_" + s
	}
	return s
}

// opencodeCookie 解析出要发的 Cookie 头。
//
// cookie 优先于 session（前者更完整、更可能是自救时贴的整串）。
func opencodeCookie(cookie, session string) string {
	raw := strings.TrimSpace(cookie)
	if raw == "" {
		if s := opencodeSessionValue(session); s != "" {
			return "__Host-console_session=" + s
		}
		return ""
	}
	// 不含 "=" 说明给的是裸 token 而不是整条 Cookie
	if !strings.Contains(raw, "=") {
		return "__Host-console_session=" + opencodeSessionValue(raw)
	}
	return raw
}

// opencodeMeterMeta 三个额度窗口的展示元信息。顺序固定 5h → 周 → 月。
var opencodeMeterMeta = []struct {
	Key    string
	Label  string
	Window Window
}{
	{"fiveHour", "5 小时限额", Window5h},
	{"week", "每周限额", WindowWeek},
	{"month", "每月限额", WindowMonth},
}

// microToUSD 把 microCents 换算成 USD，保留 6 位小数。
//
// 保留 6 位而不是 2 位：小额套餐换算后会小于 1 分，
// 四舍五入到 2 位会被抹成 0.00，看起来像"没额度了"。
func microToUSD(v any) *float64 {
	n := toFloat(v)
	if n == nil {
		return nil
	}
	r := *n / microCentsPerUSD
	// 手动截到 6 位，避免浮点尾数噪音进到展示文本里
	r = float64(int64(r*1e6+sign(r)*0.5)) / 1e6
	return &r
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

// runOpencode 执行 opencode 取数。
func runOpencode(cfg BuiltinConfig, base string) (*HTTPAdapterResult, error) {
	cookie := opencodeCookie(cfg.Env["OPENCODE_COOKIE"], cfg.Env["OPENCODE_SESSION"])
	if cookie == "" {
		return nil, fmt.Errorf("缺少会话：请在该数据源的 env 里配置 OPENCODE_COOKIE" +
			"（__Host-console_session 的值即可），或 OPENCODE_SESSION")
	}
	org := strings.TrimSpace(cfg.Env["OPENCODE_ORG_ID"])

	timeout := 20 * time.Second
	if cfg.TimeoutMs > 0 {
		timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	if timeout < time.Second {
		timeout = time.Second
	}

	path := cfg.Path
	if path == "" {
		path = "/console/api/go/status"
	}
	u := joinURL(base, path, nil)

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", opencodeUserAgent)
	req.Header.Set("Cookie", cookie)
	if org != "" {
		// 缺它是 400，填错是 404 —— 两种都单独给提示，否则用户很难自查
		req.Header.Set("x-org-id", org)
	}

	client := &http.Client{Timeout: timeout}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer res.Body.Close()

	text, err := io.ReadAll(io.LimitReader(res.Body, maxHTTPBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("上游 %d: %s%s", res.StatusCode,
			truncateForError(string(text)), opencodeStatusHint(res.StatusCode, org != ""))
	}

	var data map[string]any
	if err := json.Unmarshal(text, &data); err != nil {
		return nil, fmt.Errorf("上游返回的不是 JSON: %s", truncateForError(string(text)))
	}

	items, err := opencodeBuildItems(data)
	if err != nil {
		return nil, err
	}

	// 订阅周期一并回传，供面板显示「xx 后到期」
	raw := map[string]any{"status": data, "meta": opencodeMeta(data)}
	return &HTTPAdapterResult{Items: items, Raw: raw}, nil
}

// opencodeStatusHint 按状态码给出可操作的自查方向。
func opencodeStatusHint(status int, hasOrg bool) string {
	switch status {
	case 400:
		if !hasOrg {
			return "（缺少 x-org-id？→ 检查 OPENCODE_ORG_ID）"
		}
		return "（上游认为请求不合法）"
	case 401:
		return "（会话失效 → 重新登录 opencode.ai 控制台，复制新的 __Host-console_session）"
	case 404:
		return "（x-org-id 不存在或不属于该账号？）"
	default:
		return ""
	}
}

// opencodeMeta 提取订阅周期等元信息。
func opencodeMeta(data map[string]any) map[string]any {
	product := toStr(data["product"])
	currency := strings.ToUpper(toStr(data["renewalCurrency"]))
	if currency == "" {
		currency = "USD"
	}
	var endsAt any
	if access, ok := data["access"].(map[string]any); ok {
		endsAt = access["endsAt"]
	}
	return map[string]any{"product": product, "currency": currency, "periodEndsAt": endsAt}
}

// opencodeBuildItems 把 access.meters 转成契约行。
func opencodeBuildItems(data map[string]any) ([]map[string]any, error) {
	access, _ := data["access"].(map[string]any)
	if access == nil {
		return nil, fmt.Errorf("响应里没有 access（套餐可能未生效，或接口结构变了）")
	}
	meters, _ := access["meters"].(map[string]any)
	if meters == nil {
		return nil, fmt.Errorf("响应里没有 access.meters（套餐可能未生效，或接口结构变了）")
	}

	out := []map[string]any{}
	for _, meta := range opencodeMeterMeta {
		m, ok := meters[meta.Key].(map[string]any)
		if !ok {
			continue
		}
		used := microToUSD(m["usedMicroCents"])
		total := microToUSD(m["limitMicroCents"])
		if used == nil && total == nil {
			continue
		}
		row := map[string]any{
			"id":      meta.Key,
			"label":   meta.Label,
			"unit":    string(UnitUSD),
			"window":  meta.Window,
			"resetAt": m["resetsAt"],
			// 用更精确的金额展示，避免小额被四舍五入成 0.00
			"format": "{used:4} / {total:2} USD",
		}
		// 必须写**解引用后的 float64**：契约的 toFloat 只认数值本体，
		// 放 *float64 进去会被当成不可识别的类型，整条被丢弃。
		if used != nil {
			row["used"] = *used
		}
		if total != nil {
			row["total"] = *total
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("access.meters 存在但没有可用的用量字段")
	}
	return out, nil
}
