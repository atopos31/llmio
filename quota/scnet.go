package quota

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// 国家超算（scnet.cn）TokenPlan 套餐余量。
//
// 为什么不用官方 OpenAI 兼容接口：实测 https://api.scnet.cn/api/llm/v1/user/balance
// 返回 401，官方兼容口不暴露余量。只能走控制台会话：
//
//	① GET  /sso/login                       提取 execution（一次性令牌）
//	② RSA-512 PKCS#1 v1.5 加密密码（公钥全站硬编码）
//	③ POST /sso/login → 302 ticket 校验 → 种会话 Cookie
//	④ GET  /acx/charge/account/currentuser/tokenplan/list     套餐额度 used/total(CREDITS)
//	⑤ GET  /acx/llm/api/package/overview?packageType=TokenPlan 限额窗口定义 + 模型清单
//
// 这是计划 §3.4 方案 B 的落点：复杂登录流用 Go 原生 net/http + crypto/rsa +
// cookie jar 实现，而不是塞进 goja 沙箱。沙箱没有 crypto、没有 cookie jar，
// 硬要支持就得把宿主 API 面积撑大，反而削弱安全边界。

const (
	scnetUserInfoPath  = "/acx/user/users/current-user-info?includeToken=true&refresh=true"
	scnetTokenPlanPath = "/acx/charge/account/currentuser/tokenplan/list"
	scnetOverviewPath  = "/acx/llm/api/package/overview?packageType=TokenPlan"

	scnetUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"
)

// scnetPublicKeyBase64 是 scnet.cn 前端 login-*.js 里硬编码的 RSA 公钥，全站通用。
const scnetPublicKeyBase64 = "MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALaXEnbjI6fjy+t9W9AiO/KS0q+b/OZ" +
	"FS+7ykinLbiriUx9P8BcuuHnVbXNiZp5jW70eVGBtX4DhGUPzJa1YT/8CAwEAAQ=="

// rsaEncryptPKCS1v15 手写 PKCS#1 v1.5 公钥加密。
//
// **为什么不用 crypto/rsa.EncryptPKCS1v15**：Go 1.24 起 crypto/rsa 对小于 1024 位的
// 密钥一律拒绝（可由 GODEBUG=rsa1024min=0 放开），而 scnet 的公钥是 512 位、
// 由上游写死，我们改不了。两条路：
//
//   - 设 GODEBUG：进程级全局状态，等于为了一个数据源降低整个程序的安全策略，
//     而且要在任何 rsa 调用之前设置，容易在别处被覆盖或顺序踩坑
//   - 自己实现公钥运算（本实现）
//
// 选后者：公钥加密只是一个模幂 c = m^e mod n，不涉及私钥侧的常量时间要求，
// 用 math/big 实现既正确又不留全局副作用。
//
// 正确性已与标准库对拍（scnet_test.go）：自造 512 位密钥对，
// 本实现加密后由 crypto/rsa 的私钥解密能还原原文，密文长度也一致。
func rsaEncryptPKCS1v15(pub *rsa.PublicKey, msg []byte) ([]byte, error) {
	k := (pub.N.BitLen() + 7) / 8
	if len(msg) > k-11 {
		return nil, fmt.Errorf("明文过长：%d 字节 > %d", len(msg), k-11)
	}

	// PS 是非零随机填充，长度 k-3-len(msg)
	ps := make([]byte, k-3-len(msg))
	buf := make([]byte, 1)
	for i := range ps {
		for {
			if _, err := rand.Read(buf); err != nil {
				return nil, err
			}
			if buf[0] != 0 {
				ps[i] = buf[0]
				break
			}
		}
	}

	em := make([]byte, 0, k)
	em = append(em, 0x00, 0x02)
	em = append(em, ps...)
	em = append(em, 0x00)
	em = append(em, msg...)

	m := new(big.Int).SetBytes(em)
	if m.Cmp(pub.N) >= 0 {
		return nil, fmt.Errorf("消息代表值超出模数范围")
	}
	c := new(big.Int).Exp(m, big.NewInt(int64(pub.E)), pub.N)
	out := make([]byte, k)
	c.FillBytes(out)
	return out, nil
}

// parseRSAKey 从 PKIX/DER 公钥字符串解析公钥。
func parseRSAKey(b64 string) (*rsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("公钥 base64 解码失败: %w", err)
	}
	pk, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("公钥解析失败: %w", err)
	}
	rp, ok := pk.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("公钥不是 RSA")
	}
	return rp, nil
}

// scnetEncryptPassword 用 scnet 的硬编码公钥加密口令，返回 base64。
func scnetEncryptPassword(password string) (string, error) {
	return encryptPasswordWithKey(password, scnetPublicKeyBase64)
}

// encryptPasswordWithKey 是加密的实际实现，公钥从参数传入。
//
// 拆这一层是为了让"公钥无法解析"这条防御分支可被测试：
// scnet 的公钥是写死的常量且当前有效，不拆的话那条分支永远走不到。
// 上游若哪天换了公钥格式，这里就会真正报错——值得有测试守着。
func encryptPasswordWithKey(password, keyB64 string) (string, error) {
	pub, err := parseRSAKey(keyB64)
	if err != nil {
		return "", err
	}
	ct, err := rsaEncryptPKCS1v15(pub, []byte(password))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// randHex 生成 n 个十六进制随机字符。神策匿名 ID 与数据源 id 都用它。
func randHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用时退回全零：匿名 ID 只是给上游的追踪字段，
		// 不该因为它而让整条取数失败。
		return strings.Repeat("0", n)
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = hex[int(v)%16]
	}
	return string(out)
}

// scnetAnonID 生成神策格式的匿名 ID：seg(14)-seg(16)-seg(8)-seg(8)-seg(12)。
func scnetAnonID() string {
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		randHex(14), randHex(16), randHex(8), randHex(8), randHex(12))
}

// executionRe 匹配登录页里的 execution 隐藏字段（两种属性顺序都认）。
var executionRe = regexp.MustCompile(`name="execution"\s+value="([^"]+)"|value="([^"]+)"\s+name="execution"`)

// scnetServiceURL 构造控制台登录的 service 参数。
//
// 抓包里的双层编码：service=...originalUrl=<quote(quote(目标地址))>，
// 上游服务端就是这么解码的，少一层编码会被拒。
func scnetServiceURL(base string) string {
	target := base + "/ui/console/index.html#/llm/token-plan"
	return base + "/ac/api/auth/loginSsoRedirect.action?originalUrl=" +
		url.QueryEscape(url.QueryEscape(target))
}

// newScnetClient 构造带 cookie jar 的 client。
//
// cookie 由 jar 自动管理与回送，不再手写 Set-Cookie 解析——
// 原 Node 版手写 cookie jar 是因为 Node 的 fetch 不带；Go 的标准库自带。
//
// 关于 cookie 复用：原实现把会话 cookie 落盘到临时文件，下次先试缓存以省一次登录。
// 本实现**不做持久化**：每源刷新间隔以分钟计，每次完整登录（3~4 个请求）完全可接受，
// 换来的是没有会话文件的过期判定与清理问题。若日后刷得太勤，再按需加。
func newScnetClient(timeout time.Duration) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("创建 cookie jar 失败: %w", err)
	}
	return &http.Client{
		Timeout: timeout,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// 限制重定向深度，避免环。方法与 cookie 交给标准库处理：
			// 301/302/303 会按规范转成 GET，这与原实现"逐跳改 GET"一致。
			if len(via) >= 8 {
				return fmt.Errorf("重定向次数过多")
			}
			return nil
		},
	}, nil
}

// scnetLogin 完成 SSO 登录。成功后 client 的 jar 里会有会话 cookie。
func scnetLogin(client *http.Client, base, user, pass string) error {
	service := scnetServiceURL(base)
	loginURL := base + "/sso/login?service=" + url.QueryEscape(service) +
		"&t=" + fmt.Sprint(time.Now().UnixMilli())

	// ① 取登录页，抽 execution
	req, err := http.NewRequest(http.MethodGet, loginURL, nil)
	if err != nil {
		return fmt.Errorf("构造登录页请求失败: %w", err)
	}
	req.Header.Set("User-Agent", scnetUserAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("DNT", "1")

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("打开登录页失败: %w", err)
	}
	html, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	res.Body.Close()
	if err != nil {
		return fmt.Errorf("读取登录页失败: %w", err)
	}

	m := executionRe.FindSubmatch(html)
	if m == nil {
		return fmt.Errorf("登录页未找到 execution 字段（页面结构可能已变化）")
	}
	execution := string(m[1])
	if execution == "" {
		execution = string(m[2])
	}

	// ② RSA 加密口令
	encrypted, err := scnetEncryptPassword(pass)
	if err != nil {
		return fmt.Errorf("加密口令失败: %w", err)
	}

	// ③ POST 登录表单。字段顺序与原实现一致。
	form := formEncode([][2]string{
		{"username", user},
		{"encrypted", "true"},
		{"sensorsAnonId", scnetAnonID()},
		{"mode", "0"},
		{"captcha", ""},
		{"execution", execution},
		{"_eventId", "submit"},
		{"geolocation", ""},
		{"submit", "登录"},
		{"password", encrypted},
	})

	req2, err := http.NewRequest(http.MethodPost, loginURL, form)
	if err != nil {
		return fmt.Errorf("构造登录请求失败: %w", err)
	}
	req2.Header.Set("User-Agent", scnetUserAgent)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Referer", loginURL)
	req2.Header.Set("Origin", base)
	req2.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req2.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	res2, err := client.Do(req2)
	if err != nil {
		return fmt.Errorf("提交登录失败: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res2.Body, 1<<20))
	res2.Body.Close()

	// 判定成功看 cookie，而不是看最终状态码：ticket 校验后的落点是个 400 的
	// 怪地址（上游对 originalUrl 解码的怪癖），但此时 cookie 已经种下了。
	u, _ := url.Parse(base)
	if !hasScnetSessionCookie(client.Jar, u) {
		return fmt.Errorf("登录失败：未获得会话 Cookie（最终状态 %d），请检查账号口令", res2.StatusCode)
	}
	return nil
}

// hasScnetSessionCookie 判断 jar 里有没有登录态的 cookie。
func hasScnetSessionCookie(jar http.CookieJar, u *url.URL) bool {
	// u 也要判空：cookiejar 的 Cookies 会解引用 URL，传 nil 直接 panic。
	if jar == nil || u == nil {
		return false
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == "Token" || c.Name == "TGC" {
			return true
		}
	}
	return false
}

// scnetAPIGet 调一个控制台接口，返回 data 字段。
//
// 上游约定 { code: "0", data: ... }，code 非 0 表示业务失败（多半是会话失效）。
func scnetAPIGet(client *http.Client, base, p string) (any, error) {
	req, err := http.NewRequest(http.MethodGet, base+p, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("User-Agent", scnetUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", base+"/ui/console/index.html")
	req.Header.Set("Version", "2.7.5")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败: %w", p, err)
	}
	defer res.Body.Close()
	text, err := io.ReadAll(io.LimitReader(res.Body, maxHTTPBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 %s 响应失败: %w", p, err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(text, &envelope); err != nil {
		return nil, fmt.Errorf("接口 %s 返回非 JSON（HTTP %d）：%s", p, res.StatusCode, truncateForError(string(text)))
	}
	// code 可能是字符串 "0" 或数字 0，两种都认
	if code := toStr(envelope["code"]); code != "0" {
		return nil, fmt.Errorf("接口 %s 返回错误：%s（会话可能已失效）",
			p, truncateForError(string(text)))
	}
	return envelope["data"], nil
}

// runSCNet 执行 scnet 取数。
func runSCNet(cfg BuiltinConfig, base string) (*HTTPAdapterResult, error) {
	user := cfg.Env["SCNET_USER"]
	pass := cfg.Env["SCNET_PASS"]
	if user == "" || pass == "" {
		return nil, fmt.Errorf("缺少账号或口令：请在数据源的 env 里配置 SCNET_USER 与 SCNET_PASS")
	}

	timeout := 45 * time.Second
	if cfg.TimeoutMs > 0 {
		timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	client, err := newScnetClient(timeout)
	if err != nil {
		return nil, err
	}

	if err := scnetLogin(client, base, user, pass); err != nil {
		return nil, err
	}

	userInfo, err := scnetAPIGet(client, base, scnetUserInfoPath)
	if err != nil {
		return nil, err
	}
	plansRaw, err := scnetAPIGet(client, base, scnetTokenPlanPath)
	if err != nil {
		return nil, err
	}
	// 套餐定义接口失败不影响额度本身，降级处理
	overviewRaw, overviewErr := scnetAPIGet(client, base, scnetOverviewPath)

	plans, _ := plansRaw.([]any)
	overview, _ := overviewRaw.([]any)
	raw := map[string]any{
		"userInfo": userInfo, "tokenplanList": plansRaw, "packageOverview": overviewRaw,
	}
	if overviewErr != nil {
		raw["packageOverviewError"] = overviewErr.Error()
	}

	items := scnetBuildItems(plans, overview)
	if len(items) == 0 {
		return nil, fmt.Errorf("未查询到任何 TokenPlan 套餐记录（账号可能未购买套餐）")
	}
	return &HTTPAdapterResult{Items: items, Raw: raw}, nil
}

// scnetWindowLabels 窗口单位的中文标签。
var scnetWindowLabels = map[string]string{
	"hour": "小时", "day": "每日", "week": "每周",
	"month": "每月", "year": "每年",
}

// scnetWindowOf 从套餐定义的 request 数组推导窗口与各档限额。
func scnetWindowOf(def map[string]any) (Window, []map[string]any) {
	reqs, _ := def["request"].([]any)
	if len(reqs) == 0 {
		return WindowNone, nil
	}
	first, _ := reqs[0].(map[string]any)
	if first == nil {
		return WindowNone, nil
	}
	unit := toStr(first["unit"])
	if unit == "" {
		return WindowNone, nil
	}

	windows := make([]map[string]any, 0, len(reqs))
	for _, r := range reqs {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		u := toStr(rm["unit"])
		label := scnetWindowLabels[u]
		if label == "" {
			label = u
		}
		n := toFloat(rm["time"])
		text := label
		if n != nil && *n > 1 {
			text = fmt.Sprintf("%s %s", toStr(rm["time"]), label)
		}
		windows = append(windows, map[string]any{
			"text": text, "limit": rm["limit"], "unit": u,
		})
	}

	// hour → 5h：TokenPlan 的小时档实为 5 小时窗
	switch unit {
	case "month":
		return WindowMonth, windows
	case "week":
		return WindowWeek, windows
	case "hour":
		return Window5h, windows
	default:
		return NormalizeWindow(unit), windows
	}
}

// scnetBuildItems 把套餐记录与定义组装成契约行。
func scnetBuildItems(plans, overview []any) []map[string]any {
	defByKey := map[string]map[string]any{}
	for _, o := range overview {
		d, ok := o.(map[string]any)
		if !ok {
			continue
		}
		if k := toStr(d["code"]); k != "" {
			defByKey[k] = d
		}
		if k := toStr(d["name"]); k != "" {
			defByKey[k] = d
		}
	}

	out := []map[string]any{}
	for i, p := range plans {
		plan, ok := p.(map[string]any)
		if !ok {
			continue
		}
		// 只取启用中的套餐（原实现默认过滤 status !== 'enable'）
		if toStr(plan["status"]) != "enable" {
			continue
		}

		def := defByKey[toStr(plan["resourceId"])]
		if def == nil {
			def = defByKey[toStr(plan["name"])]
		}
		win, windows := WindowNone, []map[string]any{}
		if def != nil {
			win, windows = scnetWindowOf(def)
		}

		id := toStr(plan["resourceAccountId"])
		if id == "" {
			id = fmt.Sprint(i)
		}
		label := toStr(plan["name"])
		if label == "" {
			label = "TokenPlan"
		}

		windowTexts := make([]string, 0, len(windows))
		for _, w := range windows {
			windowTexts = append(windowTexts, fmt.Sprintf("%s 上限 %s", toStr(w["text"]), toStr(w["limit"])))
		}

		models := []string{}
		if def != nil {
			for _, m := range strings.Split(toStr(def["models"]), ",") {
				if m = strings.TrimSpace(m); m != "" {
					models = append(models, m)
				}
			}
		}
		modelsCapped := models
		if len(modelsCapped) > 60 {
			modelsCapped = modelsCapped[:60]
		}

		unit := toStr(plan["unit"])
		if unit == "" {
			unit = string(UnitCredits)
		}

		out = append(out, map[string]any{
			"id":       "plan-" + id,
			"label":    label,
			"used":     plan["usedAmount"],
			"total":    plan["totalAmount"],
			"unit":     unit,
			"window":   win,
			"expireAt": plan["maxExpireTime"],
			"extra": map[string]any{
				"planCode":          plan["resourceId"],
				"planStatus":        plan["status"],
				"totalDays":         plan["totalDays"],
				"validFrom":         plan["minValidTime"],
				"resourceAccountId": plan["resourceAccountId"],
				"windows":           windows,
				"windowText":        strings.Join(windowTexts, "、"),
				"modelCount":        len(models),
				"models":            modelsCapped,
			},
		})
	}

	// 套餐定义里有窗口、但账号没有任何套餐记录时，至少把限额结构报出来
	if len(out) == 0 && len(overview) > 0 {
		for _, o := range overview {
			def, ok := o.(map[string]any)
			if !ok {
				continue
			}
			reqs, _ := def["request"].([]any)
			for _, r := range reqs {
				rm, ok := r.(map[string]any)
				if !ok {
					continue
				}
				u := toStr(rm["unit"])
				label := scnetWindowLabels[u]
				if label == "" {
					label = u
				}
				n := toStr(rm["time"])
				if n == "" {
					n = "1"
				}
				name := toStr(def["name"])
				if name == "" {
					name = "TokenPlan"
				}
				out = append(out, map[string]any{
					"id":     fmt.Sprintf("def-%s-%s-%s", toStr(def["code"]), u, toStr(rm["time"])),
					"label":  fmt.Sprintf("%s %s%s额度", name, n, label),
					"total":  rm["limit"],
					"unit":   string(UnitCredits),
					"window": NormalizeWindow(u),
					"status": string(StatusUnknown),
					"extra": map[string]any{
						"note": "仅有套餐定义，账号暂无可用套餐记录", "planCode": def["code"],
					},
				})
			}
		}
	}
	return out
}
