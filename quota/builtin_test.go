package quota

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 注册表
// ---------------------------------------------------------------------------

func TestListBuiltins(t *testing.T) {
	list := ListBuiltins()
	if len(list) != len(builtinRegistry) {
		t.Fatalf("清单长度不符：%d vs %d", len(list), len(builtinRegistry))
	}
	byID := map[string]BuiltinInfo{}
	for _, b := range list {
		byID[b.ID] = b
	}
	for _, want := range []string{"deepseek", "moonshot", "scnet", "opencode", "commandcode", "custom"} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("缺少内置适配器 %q", want)
		}
	}
	// 登录型必须标出来，前端据此显示账号口令表单
	if !byID["scnet"].NeedsLogin || !byID["opencode"].NeedsLogin {
		t.Fatal("登录型适配器应标记 NeedsLogin")
	}
	// custom 是通用占位
	if !byID["custom"].Generic {
		t.Fatal("custom 应标记 Generic")
	}
	if byID["deepseek"].Generic {
		t.Fatal("deepseek 不应是 Generic")
	}
	// commandcode 走 API Key 鉴权，既不是登录型也不是通用占位
	if byID["commandcode"].Generic || byID["commandcode"].NeedsLogin {
		t.Fatal("commandcode 不应标记 Generic/NeedsLogin")
	}
}

func TestFindBuiltin(t *testing.T) {
	if _, ok := findBuiltin("deepseek"); !ok {
		t.Fatal("应找到 deepseek")
	}
	if _, ok := findBuiltin("nope"); ok {
		t.Fatal("不存在的适配器不应被找到")
	}
}

func TestRunBuiltinUnknown(t *testing.T) {
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "nope"}, nil)
	if err == nil || !strings.Contains(err.Error(), "未知内置适配器") {
		t.Fatalf("应报未知适配器，实得 %v", err)
	}
}

func TestRunBuiltinGenericRequiresPath(t *testing.T) {
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "custom"}, nil)
	if err == nil || !strings.Contains(err.Error(), "请求路径") {
		t.Fatalf("自定义端点缺 path 应报错，实得 %v", err)
	}
}

// ---------------------------------------------------------------------------
// joinURL / formEncode
// ---------------------------------------------------------------------------

func TestJoinURL(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		path  string
		query any
		want  string
	}{
		{"基础", "https://a.com", "/v1/x", nil, "https://a.com/v1/x"},
		{"base 末尾斜杠", "https://a.com/", "/v1/x", nil, "https://a.com/v1/x"},
		{"path 无前导斜杠", "https://a.com", "v1/x", nil, "https://a.com/v1/x"},
		{"空 path", "https://a.com", "", nil, "https://a.com"},
		{"对象 query", "https://a.com", "/x", map[string]any{"b": "2", "a": "1"}, "https://a.com/x?a=1&b=2"},
		{"对象 query 跳过空值", "https://a.com", "/x", map[string]any{"a": "1", "b": ""}, "https://a.com/x?a=1"},
		{"字符串 query 去问号", "https://a.com", "/x", "?a=1&b=2", "https://a.com/x?a=1&b=2"},
		{"字符串 query", "https://a.com", "/x", "a=1", "https://a.com/x?a=1"},
		{"url 已带 ? 时用 &", "https://a.com", "/x?y=0", map[string]any{"a": "1"}, "https://a.com/x?y=0&a=1"},
		{"nil query", "https://a.com", "/x", nil, "https://a.com/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := joinURL(c.base, c.path, c.query); got != c.want {
				t.Fatalf("joinURL = %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestFormEncode(t *testing.T) {
	got := readAll(t, formEncode([][2]string{{"a", "1 2"}, {"b", "x&y"}}))
	want := "a=1+2&b=x%26y"
	if got != want {
		t.Fatalf("formEncode = %q，期望 %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// deepseek / moonshot 解析
// ---------------------------------------------------------------------------

func TestParseDeepseek(t *testing.T) {
	t.Run("有余额明细", func(t *testing.T) {
		items, err := parseBuiltin("deepseek", parseJSONAny(t, `{
			"is_available": true,
			"balance_infos": [
				{"currency":"CNY","total_balance":12.5,"granted_balance":1,"topped_up_balance":11.5},
				{"currency":"USD","total_balance":0}
			]}`))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("应得 2 条，实得 %d", len(items))
		}
		if items[0]["id"] != "balance-CNY" || items[0]["label"] != "余额 (CNY)" {
			t.Fatalf("第一条标识不符: %#v", items[0])
		}
		if items[0]["status"] != string(StatusOK) {
			t.Fatalf("正余额应为 ok: %#v", items[0])
		}
		if items[1]["status"] != string(StatusExhausted) {
			t.Fatalf("零余额应为 exhausted: %#v", items[1])
		}
		// 产出必须能被契约归一
		norm, err := Normalize(items, NormalizeOptions{})
		if err != nil {
			t.Fatalf("归一应成功: %v", err)
		}
		if len(norm) != 2 || norm[0].Remaining == nil || *norm[0].Remaining != 12.5 {
			t.Fatalf("归一结果不符: %#v", norm)
		}
	})

	t.Run("无明细但账户可用", func(t *testing.T) {
		items, err := parseBuiltin("deepseek", parseJSONAny(t, `{"is_available": true}`))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 1 || items[0]["id"] != "available" {
			t.Fatalf("应给出一条账户可用: %#v", items)
		}
		if items[0]["status"] != string(StatusUnknown) {
			t.Fatalf("可用但无余额数应为 unknown: %#v", items[0])
		}
	})

	t.Run("无明细且账户不可用", func(t *testing.T) {
		items, err := parseBuiltin("deepseek", parseJSONAny(t, `{"is_available": false}`))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[0]["status"] != string(StatusExhausted) {
			t.Fatalf("不可用应为 exhausted: %#v", items[0])
		}
		if items[0]["remaining"] != float64(0) {
			t.Fatalf("不可用应给出余额 0: %#v", items[0])
		}
	})

	t.Run("响应不是对象", func(t *testing.T) {
		if _, err := parseBuiltin("deepseek", []any{}); err == nil {
			t.Fatal("非对象应报错")
		}
	})

	t.Run("明细里含非对象元素则跳过", func(t *testing.T) {
		items, err := parseBuiltin("deepseek", parseJSONAny(t, `{
			"is_available": true, "balance_infos": [42, {"currency":"CNY","total_balance":1}]}`))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("应只保留对象元素，实得 %d", len(items))
		}
	})

	t.Run("全部明细都不是对象时报错", func(t *testing.T) {
		_, err := parseBuiltin("deepseek", parseJSONAny(t, `{"balance_infos":[1,2]}`))
		if err == nil || !strings.Contains(err.Error(), "没有可用条目") {
			t.Fatalf("应报没有可用条目，实得 %v", err)
		}
	})
}

func TestParseMoonshot(t *testing.T) {
	t.Run("包一层 data", func(t *testing.T) {
		items, err := parseBuiltin("moonshot", parseJSONAny(t, `{
			"data":{"available_balance":8.25,"voucher_balance":1,"cash_balance":7.25}}`))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 1 || items[0]["remaining"] != 8.25 {
			t.Fatalf("余额不符: %#v", items)
		}
		if items[0]["unit"] != string(UnitCNY) || items[0]["window"] != string(WindowTotal) {
			t.Fatalf("单位/窗口不符: %#v", items[0])
		}
		if items[0]["status"] != string(StatusOK) {
			t.Fatalf("正余额应为 ok: %#v", items[0])
		}
	})

	t.Run("驼峰别名", func(t *testing.T) {
		items, err := parseBuiltin("moonshot", parseJSONAny(t, `{"availableBalance": 3}`))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		// JSON 数字一律是 float64，不能与 int 字面量直接比较
		if items[0]["remaining"] != float64(3) {
			t.Fatalf("驼峰字段应被识别: %#v", items[0])
		}
	})

	t.Run("零余额为 exhausted", func(t *testing.T) {
		items, err := parseBuiltin("moonshot", parseJSONAny(t, `{"available_balance": 0}`))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[0]["status"] != string(StatusExhausted) {
			t.Fatalf("零余额应为 exhausted: %#v", items[0])
		}
	})

	t.Run("缺字段报错", func(t *testing.T) {
		_, err := parseBuiltin("moonshot", parseJSONAny(t, `{"x":1}`))
		if err == nil || !strings.Contains(err.Error(), "available_balance") {
			t.Fatalf("应报缺字段，实得 %v", err)
		}
	})

	t.Run("非对象报错", func(t *testing.T) {
		if _, err := parseBuiltin("moonshot", "str"); err == nil {
			t.Fatal("非对象应报错")
		}
	})
}

func TestParseBuiltinDispatch(t *testing.T) {
	if _, err := parseBuiltin("custom", nil); err == nil {
		t.Fatal("custom 不应走到内置解析")
	}
	if _, err := parseBuiltin("nope", nil); err == nil {
		t.Fatal("未知 id 应报错")
	}
}

// ---------------------------------------------------------------------------
// 手写 PKCS#1 v1.5 —— 与标准库对拍
// ---------------------------------------------------------------------------

// TestRSAEncryptPKCS1v15MatchesStdlib 是这一处手写实现的核心保障：
// Go 1.24+ 的 crypto/rsa 拒绝 512 位密钥，而 scnet 的公钥就是 512 位，
// 所以这里自己实现公钥运算。必须证明它与标准库语义一致。
//
// 做法：临时放开位数下限生成一个 512 位密钥对，用本实现加密、
// 用标准库的私钥解密，能还原原文即证明 EM 构造正确。
func TestRSAEncryptPKCS1v15MatchesStdlib(t *testing.T) {
	// crypto/rsa 的位数下限由 GODEBUG 控制；测试里只在需要时放开。
	// 用 t.Setenv 保证作用域干净，跑完自动恢复。
	prev, had := os.LookupEnv("GODEBUG")
	t.Cleanup(func() {
		if had {
			os.Setenv("GODEBUG", prev)
		} else {
			os.Unsetenv("GODEBUG")
		}
	})
	os.Setenv("GODEBUG", "rsa1024min=0")

	priv, err := rsa.GenerateKey(rand.Reader, 512)
	if err != nil {
		t.Skipf("本环境无法生成 512 位密钥（%v），跳过对拍", err)
	}

	for _, msg := range []string{"p", "password123", strings.Repeat("a", 53)} {
		ct, err := rsaEncryptPKCS1v15(&priv.PublicKey, []byte(msg))
		if err != nil {
			t.Fatalf("加密 %d 字节失败: %v", len(msg), err)
		}
		if len(ct) != 64 {
			t.Fatalf("512 位密钥的密文应为 64 字节，实得 %d", len(ct))
		}
		back, err := rsa.DecryptPKCS1v15(nil, priv, ct)
		if err != nil {
			t.Fatalf("标准库解密失败: %v", err)
		}
		if string(back) != msg {
			t.Fatalf("往返不符：期望 %q，实得 %q", msg, back)
		}
	}
}

func TestRSAEncryptPKCS1v15TooLong(t *testing.T) {
	os.Setenv("GODEBUG", "rsa1024min=0")
	defer os.Unsetenv("GODEBUG")
	priv, err := rsa.GenerateKey(rand.Reader, 512)
	if err != nil {
		t.Skipf("本环境无法生成 512 位密钥（%v），跳过", err)
	}
	// 512 位 = 64 字节，明文上限 64-11 = 53
	if _, err := rsaEncryptPKCS1v15(&priv.PublicKey, make([]byte, 54)); err == nil {
		t.Fatal("超长明文应报错")
	}
}

func TestParseRSAKeyErrors(t *testing.T) {
	if _, err := parseRSAKey("!!!not base64!!!"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	if _, err := parseRSAKey(base64.StdEncoding.EncodeToString([]byte("not a key"))); err == nil {
		t.Fatal("非 DER 内容应报错")
	}
	// 合法的非 RSA 公钥（Ed25519）
	const edPub = "MCowBQYDK2VwAyEAGb9ECWmEzf6FQbrBZ9w7lshQhqowtrbLDFw4rXAxZuE="
	if _, err := parseRSAKey(edPub); err == nil || !strings.Contains(err.Error(), "不是 RSA") {
		t.Fatalf("非 RSA 公钥应报错，实得 %v", err)
	}
}

func TestScnetEncryptPassword(t *testing.T) {
	// 用真实的 scnet 公钥加密：验证整条链路可用，密文为 64 字节 base64（88 字符）
	out, err := scnetEncryptPassword("password123")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		t.Fatalf("输出不是合法 base64: %v", err)
	}
	if len(raw) != 64 {
		t.Fatalf("512 位密钥密文应为 64 字节，实得 %d", len(raw))
	}
	if len(out) != 88 {
		t.Fatalf("base64 长度应为 88，实得 %d", len(out))
	}
	// 每次加密都应有不同的随机填充
	out2, _ := scnetEncryptPassword("password123")
	if out == out2 {
		t.Fatal("两次加密结果相同说明填充没有随机化")
	}
}

func TestScnetAnonIDAndRandHex(t *testing.T) {
	if got := randHex(8); len(got) != 8 {
		t.Fatalf("长度应为 8，实得 %q", got)
	}
	id := scnetAnonID()
	parts := strings.Split(id, "-")
	want := []int{14, 16, 8, 8, 12}
	if len(parts) != len(want) {
		t.Fatalf("分段数应为 %d，实得 %q", len(want), id)
	}
	for i, w := range want {
		if len(parts[i]) != w {
			t.Fatalf("第 %d 段长度应为 %d，实得 %q", i, w, parts[i])
		}
	}
}

func TestScnetServiceURL(t *testing.T) {
	got := scnetServiceURL("https://www.scnet.cn")
	// 双层编码：originalUrl 的值本身被编码了两次
	if !strings.Contains(got, "/ac/api/auth/loginSsoRedirect.action?originalUrl=") {
		t.Fatalf("路径不符: %q", got)
	}
	if strings.Contains(got, "#/llm/token-plan") {
		t.Fatalf("目标地址应被编码，实得 %q", got)
	}
	if !strings.Contains(got, "25") { // %25 = 被编码的 %
		t.Fatalf("应为双层编码，实得 %q", got)
	}
}

func TestScnetWindowOf(t *testing.T) {
	t.Run("空 request", func(t *testing.T) {
		w, ws := scnetWindowOf(map[string]any{})
		if w != WindowNone || ws != nil {
			t.Fatalf("应为空，实得 %v %#v", w, ws)
		}
	})

	t.Run("首元素非对象", func(t *testing.T) {
		w, _ := scnetWindowOf(map[string]any{"request": []any{42}})
		if w != WindowNone {
			t.Fatalf("应为空窗口，实得 %v", w)
		}
	})

	t.Run("unit 为空", func(t *testing.T) {
		w, _ := scnetWindowOf(map[string]any{"request": []any{map[string]any{"time": 1}}})
		if w != WindowNone {
			t.Fatalf("应为空窗口，实得 %v", w)
		}
	})

	t.Run("month 带多档", func(t *testing.T) {
		w, ws := scnetWindowOf(map[string]any{"request": []any{
			map[string]any{"unit": "month", "time": 1, "limit": 100},
			map[string]any{"unit": "day", "time": 5, "limit": 10},
			map[string]any{"unit": "week", "time": 1, "limit": 50},
		}})
		if w != WindowMonth {
			t.Fatalf("窗口应为 month，实得 %v", w)
		}
		if len(ws) != 3 {
			t.Fatalf("应有 3 档，实得 %d", len(ws))
		}
		if ws[0]["text"] != "每月" || ws[1]["text"] != "5 每日" {
			t.Fatalf("档位文案不符: %#v", ws)
		}
	})

	t.Run("hour 映射为 5h", func(t *testing.T) {
		w, _ := scnetWindowOf(map[string]any{"request": []any{map[string]any{"unit": "hour", "time": 5}}})
		if w != Window5h {
			t.Fatalf("hour 应映射为 5h，实得 %v", w)
		}
	})

	t.Run("week", func(t *testing.T) {
		w, _ := scnetWindowOf(map[string]any{"request": []any{map[string]any{"unit": "week", "time": 1}}})
		if w != WindowWeek {
			t.Fatalf("应为 week，实得 %v", w)
		}
	})

	t.Run("未知单位走契约归一", func(t *testing.T) {
		w, _ := scnetWindowOf(map[string]any{"request": []any{map[string]any{"unit": "total", "time": 1}}})
		if w != WindowTotal {
			t.Fatalf("未知单位应交由契约处理，实得 %v", w)
		}
	})

	t.Run("非对象档位被跳过", func(t *testing.T) {
		_, ws := scnetWindowOf(map[string]any{"request": []any{
			map[string]any{"unit": "month", "time": 1, "limit": 1}, 42,
		}})
		if len(ws) != 1 {
			t.Fatalf("应跳过非对象档位，实得 %d", len(ws))
		}
	})
}

func TestScnetBuildItems(t *testing.T) {
	overview := []any{
		map[string]any{
			"code": "plan-a", "name": "TokenPlan A", "models": "m1,m2,m3",
			"request": []any{map[string]any{"unit": "month", "time": 1, "limit": 100}},
		},
	}
	plans := []any{
		map[string]any{
			"status": "enable", "resourceId": "plan-a", "name": "套餐甲",
			"usedAmount": 30, "totalAmount": 100, "unit": "CREDITS",
			"resourceAccountId": "acc-1", "maxExpireTime": "2026-12-31",
			"totalDays": 30, "minValidTime": "2026-01-01",
		},
	}

	items := scnetBuildItems(plans, overview)
	if len(items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(items))
	}
	it := items[0]
	if it["id"] != "plan-acc-1" || it["label"] != "套餐甲" {
		t.Fatalf("标识不符: %#v", it)
	}
	if it["window"] != WindowMonth {
		t.Fatalf("窗口应取自定义: %#v", it["window"])
	}
	// item 里不能带显式 status —— 交给契约按百分比判定
	if _, has := it["status"]; has {
		t.Fatalf("启用中的套餐不应预置 status: %#v", it)
	}
	extra := it["extra"].(map[string]any)
	if extra["modelCount"] != 3 {
		t.Fatalf("模型数不符: %#v", extra)
	}
	if extra["windowText"] != "每月 上限 100" {
		t.Fatalf("窗口文案不符: %#v", extra["windowText"])
	}

	// 必须能被契约归一
	norm, err := Normalize(items, NormalizeOptions{})
	if err != nil {
		t.Fatalf("归一应成功: %v", err)
	}
	if norm[0].Percent == nil || *norm[0].Percent != 30 {
		t.Fatalf("百分比应归一为 30%%，实得 %#v", norm[0].Percent)
	}
}

func TestScnetBuildItemsFiltersDisabled(t *testing.T) {
	plans := []any{
		map[string]any{"status": "disabled", "resourceId": "p", "name": "停用", "usedAmount": 1, "totalAmount": 2},
		map[string]any{"status": "enable", "resourceId": "q", "name": "启用", "usedAmount": 1, "totalAmount": 2},
	}
	items := scnetBuildItems(plans, nil)
	if len(items) != 1 || items[0]["label"] != "启用" {
		t.Fatalf("应只保留启用中的套餐: %#v", items)
	}
}

func TestScnetBuildItemsFallbacks(t *testing.T) {
	t.Run("无 id 与名称时给默认值", func(t *testing.T) {
		items := scnetBuildItems([]any{
			map[string]any{"status": "enable", "usedAmount": 1, "totalAmount": 2},
		}, nil)
		if len(items) != 1 {
			t.Fatalf("应得 1 条，实得 %d", len(items))
		}
		if items[0]["id"] != "plan-0" || items[0]["label"] != "TokenPlan" {
			t.Fatalf("默认标识不符: %#v", items[0])
		}
		if items[0]["unit"] != string(UnitCredits) {
			t.Fatalf("默认单位应为 CREDITS: %#v", items[0])
		}
	})

	t.Run("非对象套餐被跳过", func(t *testing.T) {
		items := scnetBuildItems([]any{42}, nil)
		if len(items) != 0 {
			t.Fatalf("应跳过，实得 %#v", items)
		}
	})
}

func TestScnetBuildItemsFromDefinitionOnly(t *testing.T) {
	// 没有套餐记录但有套餐定义时，至少把限额结构报出来
	overview := []any{
		map[string]any{
			"code": "p1", "name": "TokenPlan X",
			"request": []any{
				map[string]any{"unit": "month", "time": 1, "limit": 100},
				map[string]any{"unit": "day", "time": 1, "limit": 5},
			},
		},
	}
	items := scnetBuildItems(nil, overview)
	if len(items) != 2 {
		t.Fatalf("应得 2 条，实得 %d", len(items))
	}
	if items[0]["status"] != string(StatusUnknown) {
		t.Fatalf("仅有定义时应为 unknown: %#v", items[0])
	}
	if items[0]["label"] != "TokenPlan X 1每月额度" {
		t.Fatalf("文案不符: %#v", items[0]["label"])
	}
	// time 缺失时按 1 处理
	items2 := scnetBuildItems(nil, []any{
		map[string]any{"code": "p2", "request": []any{map[string]any{"unit": "week", "limit": 3}}},
	})
	if items2[0]["label"] != "TokenPlan 1每周额度" {
		t.Fatalf("缺失 time 的文案不符: %#v", items2[0]["label"])
	}
}

func TestScnetBuildItemsDefinitionNonObjectSkipped(t *testing.T) {
	// overview 里混入非对象、request 里混入非对象，都不该 panic
	items := scnetBuildItems(nil, []any{42, map[string]any{"request": []any{7}}})
	if len(items) != 0 {
		t.Fatalf("应全部跳过，实得 %#v", items)
	}
}

// ---------------------------------------------------------------------------
// opencode
// ---------------------------------------------------------------------------

func TestOpencodeSessionValue(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"空", "", ""},
		{"已是 st_ 值", "st_abc", "st_abc"},
		{"裸 uuid 补前缀", "12345678-1234-1234-1234-123456789abc", "st_12345678-1234-1234-1234-123456789abc"},
		{"从 Cookie 串里抽", "a=1; __Host-console_session=st_xyz; b=2", "st_xyz"},
		{"其他形式原样", "weird-value", "weird-value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := opencodeSessionValue(c.in); got != c.want {
				t.Fatalf("opencodeSessionValue(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestOpencodeCookie(t *testing.T) {
	cases := []struct {
		name, cookie, session, want string
	}{
		{"cookie 优先", "k=v", "st_s", "k=v"},
		{"cookie 是裸值时补成整条", "st_raw", "", "__Host-console_session=st_raw"},
		{"用 session 拼", "", "st_s", "__Host-console_session=st_s"},
		{"都为空", "", "", ""},
		{"session 是 cookie 串", "", "x=1; __Host-console_session=st_y; z=2", "__Host-console_session=st_y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := opencodeCookie(c.cookie, c.session); got != c.want {
				t.Fatalf("opencodeCookie = %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestMicroToUSD(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want *float64
	}{
		{"零", float64(0), fptr(0)},
		{"一美元", float64(1e8), fptr(1)},
		{"小额保留小数", float64(1e4), fptr(0.0001)},
		{"非法值", "abc", nil},
		{"nil", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := microToUSD(c.in)
			if c.want == nil {
				if got != nil {
					t.Fatalf("应为 nil，实得 %v", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("不应为 nil")
			}
			if diff := *got - *c.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("换算不符：实得 %v，期望 %v", *got, *c.want)
			}
		})
	}
}

func TestOpencodeBuildItems(t *testing.T) {
	t.Run("三档齐全", func(t *testing.T) {
		items, err := opencodeBuildItems(parseJSONAny(t, `{
			"access":{"meters":{
				"fiveHour":{"usedMicroCents":2.5e8,"limitMicroCents":5e8,"resetsAt":"2026-01-01T00:00:00Z"},
				"week":{"usedMicroCents":1e8,"limitMicroCents":1e9},
				"month":{"usedMicroCents":0,"limitMicroCents":2e9}
			}}}`).(map[string]any))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 3 {
			t.Fatalf("应得 3 条，实得 %d", len(items))
		}
		if items[0]["id"] != "fiveHour" || items[0]["window"] != Window5h {
			t.Fatalf("第一档不符: %#v", items[0])
		}
		if items[1]["id"] != "week" || items[2]["id"] != "month" {
			t.Fatalf("顺序应为 5h→周→月: %#v", items)
		}
		norm, err := Normalize(items, NormalizeOptions{})
		if err != nil {
			t.Fatalf("归一应成功: %v", err)
		}
		if norm[0].Percent == nil || *norm[0].Percent != 50 {
			t.Fatalf("第一档应为 50%%，实得 %#v", norm[0].Percent)
		}
	})

	t.Run("缺档位时只出有的", func(t *testing.T) {
		items, err := opencodeBuildItems(parseJSONAny(t, `{
			"access":{"meters":{"month":{"usedMicroCents":1e8,"limitMicroCents":1e8}}}}`).(map[string]any))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 1 || items[0]["id"] != "month" {
			t.Fatalf("应只出 month: %#v", items)
		}
	})

	t.Run("两个字段都取不到则跳过", func(t *testing.T) {
		items, err := opencodeBuildItems(parseJSONAny(t, `{
			"access":{"meters":{"month":{"resetsAt":"x"}}}}`).(map[string]any))
		if err == nil || !strings.Contains(err.Error(), "没有可用") {
			t.Fatalf("应报没有可用字段，实得 %v / %#v", err, items)
		}
	})

	t.Run("缺 access", func(t *testing.T) {
		_, err := opencodeBuildItems(map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "access") {
			t.Fatalf("应报缺 access，实得 %v", err)
		}
	})

	t.Run("有 access 无 meters", func(t *testing.T) {
		_, err := opencodeBuildItems(map[string]any{"access": map[string]any{}})
		if err == nil || !strings.Contains(err.Error(), "meters") {
			t.Fatalf("应报缺 meters，实得 %v", err)
		}
	})
}

// 真机抓下来的响应（2026-10-01，字段原样）。
//
// 它钉的是**出口**而不是取数：适配器把 window 与 resetsAt 都填进了行，
// 但归一之后 window 全变成了空串——toStr 的 default 不认命名 string 类型。
// 卡片上表现为三档都既没有「每周」这类周期，也没有重置时间，而取数本身
// 一直是对的。所以这里断言的是 Normalize 之后的 Item，不是 map。
func TestOpencodeRealPayloadSurvivesNormalize(t *testing.T) {
	items, err := opencodeBuildItems(parseJSONAny(t, `{
		"subscriberUserId":"acc_01KZ0P8AHTP7K2HW2JSXZV2ZS6","product":"go","renewalCurrency":"usd",
		"access":{"startsAt":"2026-09-28T01:22:15.000Z","endsAt":"2026-10-28T01:22:15.000Z","meters":{
			"fiveHour":{"startsAt":"2026-10-01T00:47:05.773Z","resetsAt":"2026-10-01T05:47:05.773Z","limitMicroCents":"1200000000","usedMicroCents":"10022267"},
			"week":{"startsAt":"2026-09-28T00:00:00.000Z","resetsAt":"2026-10-05T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"1241957938"},
			"month":{"resetsAt":"2026-10-28T01:22:15.000Z","limitMicroCents":"6000000000","usedMicroCents":"1241957938"}}}}`).(map[string]any))
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	norm, err := Normalize(items, NormalizeOptions{})
	if err != nil {
		t.Fatalf("归一应成功: %v", err)
	}

	want := []struct {
		id     string
		window Window
		reset  string
	}{
		{"fiveHour", Window5h, "2026-10-01T05:47:05Z"},
		{"week", WindowWeek, "2026-10-05T00:00:00Z"},
		{"month", WindowMonth, "2026-10-28T01:22:15Z"},
	}
	if len(norm) != len(want) {
		t.Fatalf("应得 %d 条，实得 %d", len(want), len(norm))
	}
	for i, w := range want {
		if norm[i].ID != w.id {
			t.Fatalf("第 %d 条 id 应为 %s，实得 %s", i, w.id, norm[i].ID)
		}
		if norm[i].Window != w.window {
			t.Fatalf("%s 的 window 应为 %q，实得 %q", w.id, w.window, norm[i].Window)
		}
		// 重置时间是这张卡唯一会变的读数，丢了它用户只能靠刷新猜
		if norm[i].ResetAt != w.reset {
			t.Fatalf("%s 的 resetAt 应为 %q，实得 %q", w.id, w.reset, norm[i].ResetAt)
		}
	}
	// {window} 占位符走的是同一个字段，一并钉住
	if got := RenderTemplate("{window}", norm[1]); got != "每周" {
		t.Fatalf("{window} 应渲染成「每周」，实得 %q", got)
	}
}

func TestOpencodeMeta(t *testing.T) {
	m := opencodeMeta(map[string]any{
		"product": "go", "renewalCurrency": "usd",
		"access": map[string]any{"endsAt": "2026-08-01"},
	})
	if m["product"] != "go" || m["currency"] != "USD" || m["periodEndsAt"] != "2026-08-01" {
		t.Fatalf("元信息不符: %#v", m)
	}
	// 缺字段时的默认
	m2 := opencodeMeta(map[string]any{})
	if m2["currency"] != "USD" || m2["product"] != "" || m2["periodEndsAt"] != nil {
		t.Fatalf("默认值不符: %#v", m2)
	}
}

func TestOpencodeStatusHint(t *testing.T) {
	if !strings.Contains(opencodeStatusHint(400, false), "OPENCODE_ORG_ID") {
		t.Fatal("400 且无 org 应提示配 org")
	}
	if !strings.Contains(opencodeStatusHint(400, true), "不合法") {
		t.Fatal("400 且有 org 应提示请求不合法")
	}
	if !strings.Contains(opencodeStatusHint(401, true), "会话失效") {
		t.Fatal("401 应提示会话失效")
	}
	if !strings.Contains(opencodeStatusHint(404, true), "x-org-id") {
		t.Fatal("404 应提示 x-org-id")
	}
	if opencodeStatusHint(500, true) != "" {
		t.Fatal("其他状态码不应有提示")
	}
}

// ---------------------------------------------------------------------------
// RunBuiltin 端到端（httptest）
// ---------------------------------------------------------------------------

func TestRunBuiltinDeepseekEndToEnd(t *testing.T) {
	srv, rec := newJSONServer(t, 200, `{"is_available":true,"balance_infos":[
		{"currency":"CNY","total_balance":9.5}]}`)

	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "deepseek",
		BaseURL:   srv.URL,
		APIKey:    "sk-d",
	}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(res.Items))
	}
	if got := rec.Headers.Get("Authorization"); got != "Bearer sk-d" {
		t.Fatalf("应带 Bearer 鉴权，实得 %q", got)
	}
}

func TestRunBuiltinCustomEndpoint(t *testing.T) {
	srv, _ := newJSONServer(t, 200, `{"data":{"list":[{"a":1},{"a":2}]}}`)
	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "custom",
		BaseURL:   srv.URL,
		Path:      "/whatever",
		ItemsPath: "data.list",
		Map:       map[string]any{"used": "a"},
	}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 2 || res.Items[1]["used"] != float64(2) {
		t.Fatalf("通用映射结果不符: %#v", res.Items)
	}
}

func TestRunBuiltinGenericMappingOverridesParser(t *testing.T) {
	// 内置适配器 + 用户给了 itemsPath/map -> 走通用映射，不用自带的 parse
	srv, _ := newJSONServer(t, 200, `{"rows":[{"v":"x"}]}`)
	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "deepseek",
		BaseURL:   srv.URL,
		ItemsPath: "rows",
		Map:       map[string]any{"label": "v", "used": "=1", "total": "=2"},
	}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0]["label"] != "x" {
		t.Fatalf("通用映射应生效: %#v", res.Items)
	}
}

func TestRunBuiltinCustomWithoutMapPassesRowThrough(t *testing.T) {
	// custom 给了 path 但没写 map：整行透传，交给契约按别名识别。
	// 对端本就返回接近契约形状的结构时，写一份 map 是多余的。
	srv, _ := newJSONServer(t, 200, `{"used":25,"total":100}`)
	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "custom",
		BaseURL:   srv.URL,
		Path:      "/x",
	}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0]["used"] != float64(25) {
		t.Fatalf("整行应透传: %#v", res.Items)
	}
	norm, err := Normalize(res.Items, NormalizeOptions{})
	if err != nil || len(norm) != 1 || norm[0].Percent == nil || *norm[0].Percent != 25 {
		t.Fatalf("透传后应能归一: %v %#v", err, norm)
	}
}

func TestRunBuiltinCustomUnrecognizableShapeReportsClearError(t *testing.T) {
	// 对端返回的形状契约也认不出时，错误必须明确，而不是静默无数据
	srv, _ := newJSONServer(t, 200, `{"hello":"world"}`)
	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "custom", BaseURL: srv.URL, Path: "/x",
	}, nil)
	if err != nil {
		t.Fatalf("取数本身不应报错: %v", err)
	}
	if _, err := Normalize(res.Items, NormalizeOptions{}); err == nil {
		t.Fatal("契约应报认不出余量数值")
	}
}

func TestRunBuiltinUpstreamError(t *testing.T) {
	srv, _ := newJSONServer(t, 500, `boom`)
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "deepseek", BaseURL: srv.URL}, nil)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("应报上游错误，实得 %v", err)
	}
}

func TestRunBuiltinWithTimeoutConfig(t *testing.T) {
	srv, _ := newJSONServer(t, 200, `{"is_available":true}`)
	if _, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "deepseek", BaseURL: srv.URL, TimeoutMs: 5000,
	}, nil); err != nil {
		t.Fatalf("带超时配置不应报错: %v", err)
	}
}

// ---------------------------------------------------------------------------
// scnet 登录链路（httptest 模拟真实流程）
// ---------------------------------------------------------------------------

// scnetTestServer 模拟 scnet 的登录 + 控制台接口。
func scnetTestServer(t *testing.T, opts scnetTestOpts) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)

		switch r.URL.Path {
		case "/sso/login":
			if r.Method == http.MethodGet {
				if opts.loginPageNoExecution {
					_, _ = w.Write([]byte(`<html>没有那个字段</html>`))
					return
				}
				_, _ = w.Write([]byte(`<html><input name="execution" value="EXEC-1"/></html>`))
				return
			}
			// POST 登录
			if opts.loginFails {
				w.WriteHeader(400)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TGC", Value: "tgc-1", Path: "/"})
			w.WriteHeader(400) // 上游真实行为：最终落点是 400，但 cookie 已种下
		case "/acx/user/users/current-user-info":
			writeEnvelope(w, `{"id":"u1","userName":"tester","fullName":"测试员"}`)
		case "/acx/charge/account/currentuser/tokenplan/list":
			if opts.plansNotArray {
				writeEnvelope(w, `{"nope":1}`)
				return
			}
			writeEnvelope(w, `[{"status":"enable","resourceId":"pa","name":"套餐甲",
				"usedAmount":30,"totalAmount":100,"unit":"CREDITS","resourceAccountId":"a1",
				"maxExpireTime":"2026-12-31"}]`)
		case "/acx/llm/api/package/overview":
			if opts.overviewFails {
				// 返回可解析的信封但 code 非 0：业务失败（会话失效/接口异常），
				// 而不是"非 JSON"——后者是另一条分支，两者要分开覆盖。
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":"500","data":null}`))
				return
			}
			writeEnvelope(w, `[{"code":"pa","name":"TokenPlan A","models":"m1,m2",
				"request":[{"unit":"month","time":1,"limit":100}]}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

type scnetTestOpts struct {
	loginPageNoExecution bool
	loginFails           bool
	plansNotArray        bool
	overviewFails        bool
}

// writeEnvelope 写 { code:"0", data:... } 信封。
func writeEnvelope(w http.ResponseWriter, dataJSON string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":"0","data":` + dataJSON + `}`))
}

func TestScnetLoginAndFetch(t *testing.T) {
	srv, paths := scnetTestServer(t, scnetTestOpts{})

	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet",
		BaseURL:   srv.URL,
		Env:       map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(res.Items))
	}
	if res.Items[0]["id"] != "plan-a1" {
		t.Fatalf("条目标识不符: %#v", res.Items[0])
	}
	// 登录页 -> 登录 POST -> 三个接口
	wantOrder := []string{"/sso/login", "/sso/login",
		"/acx/user/users/current-user-info",
		"/acx/charge/account/currentuser/tokenplan/list",
		"/acx/llm/api/package/overview"}
	if len(*paths) != len(wantOrder) {
		t.Fatalf("请求序列长度不符：%#v", *paths)
	}
	for i, w := range wantOrder {
		if (*paths)[i] != w {
			t.Fatalf("第 %d 个请求应为 %s，实得 %s", i, w, (*paths)[i])
		}
	}
}

func TestScnetMissingCredentials(t *testing.T) {
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "scnet"}, nil)
	if err == nil || !strings.Contains(err.Error(), "SCNET_USER") {
		t.Fatalf("缺账号应报错，实得 %v", err)
	}
}

func TestScnetLoginPageWithoutExecution(t *testing.T) {
	srv, _ := scnetTestServer(t, scnetTestOpts{loginPageNoExecution: true})
	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "execution") {
		t.Fatalf("应报找不到 execution，实得 %v", err)
	}
}

func TestScnetLoginFailsNoCookie(t *testing.T) {
	srv, _ := scnetTestServer(t, scnetTestOpts{loginFails: true})
	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "登录失败") {
		t.Fatalf("应报登录失败，实得 %v", err)
	}
}

func TestScnetAPIBusinessError(t *testing.T) {
	// code 非 0 表示业务失败（会话失效）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="execution" value="E"/>`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TGC", Value: "t", Path: "/"})
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"401","data":null}`))
		}
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "会话可能已失效") {
		t.Fatalf("应报会话失效，实得 %v", err)
	}
}

func TestScnetOverviewFailureIsNonFatal(t *testing.T) {
	srv, _ := scnetTestServer(t, scnetTestOpts{overviewFails: true})
	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err != nil {
		t.Fatalf("套餐定义接口失败不应让整条失败: %v", err)
	}
	// 仍应有套餐记录，只是缺窗口定义
	if len(res.Items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(res.Items))
	}
	raw := res.Raw.(map[string]any)
	if _, ok := raw["packageOverviewError"]; !ok {
		t.Fatalf("应记录定义接口的失败原因: %#v", raw)
	}
}

func TestScnetNoPlansAndNoOverview(t *testing.T) {
	// 既无套餐记录、又无定义 -> 明确报"没查到"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="execution" value="E"/>`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TGC", Value: "t", Path: "/"})
		case "/acx/llm/api/package/overview":
			writeEnvelope(w, `[]`)
		default:
			writeEnvelope(w, `[]`)
		}
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "未查询到") {
		t.Fatalf("应报未查询到套餐，实得 %v", err)
	}
}

func TestScnetNonJSONAPIResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="execution" value="E"/>`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TGC", Value: "t", Path: "/"})
		default:
			_, _ = w.Write([]byte(`<html>登录页</html>`))
		}
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "返回非 JSON") {
		t.Fatalf("应报非 JSON，实得 %v", err)
	}
}

// ---------------------------------------------------------------------------
// opencode 端到端
// ---------------------------------------------------------------------------

func TestRunOpencodeEndToEnd(t *testing.T) {
	var gotCookie, gotOrg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotOrg = r.Header.Get("x-org-id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"product":"go","renewalCurrency":"usd",
			"access":{"endsAt":"2026-08-01","meters":{
				"fiveHour":{"usedMicroCents":2.5e8,"limitMicroCents":5e8},
				"month":{"usedMicroCents":1e8,"limitMicroCents":1e9}}}}`))
	}))
	defer srv.Close()

	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "opencode", BaseURL: srv.URL,
		Env: map[string]string{"OPENCODE_SESSION": "st_abc", "OPENCODE_ORG_ID": "wrk_1"},
	}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("应得 2 条，实得 %d", len(res.Items))
	}
	if gotCookie != "__Host-console_session=st_abc" {
		t.Fatalf("Cookie 不符: %q", gotCookie)
	}
	if gotOrg != "wrk_1" {
		t.Fatalf("x-org-id 不符: %q", gotOrg)
	}
	meta := res.Raw.(map[string]any)["meta"].(map[string]any)
	if meta["currency"] != "USD" || meta["product"] != "go" {
		t.Fatalf("元信息不符: %#v", meta)
	}
}

func TestRunOpencodeMissingSession(t *testing.T) {
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "opencode"}, nil)
	if err == nil || !strings.Contains(err.Error(), "OPENCODE_COOKIE") {
		t.Fatalf("缺会话应报错，实得 %v", err)
	}
}

func TestRunOpencodeStatusHints(t *testing.T) {
	// 缺 org 时上游返回 400，错误里应提示配 org
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`bad request`))
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "opencode", BaseURL: srv.URL,
		Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "OPENCODE_ORG_ID") {
		t.Fatalf("应提示缺 org，实得 %v", err)
	}
}

func TestRunOpencodeNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>login</html>`))
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "opencode", BaseURL: srv.URL,
		Env: map[string]string{"OPENCODE_COOKIE": "st_x", "OPENCODE_ORG_ID": "wrk_1"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "不是 JSON") {
		t.Fatalf("应报非 JSON，实得 %v", err)
	}
}

func TestRunOpencodeCustomPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"access":{"meters":{"month":{"usedMicroCents":1e8,"limitMicroCents":1e8}}}}`))
	}))
	defer srv.Close()

	if _, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "opencode", BaseURL: srv.URL, Path: "/custom/status",
		Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
	}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if gotPath != "/custom/status" {
		t.Fatalf("应走自定义路径，实得 %q", gotPath)
	}
}

// ---------------------------------------------------------------------------
// commandcode
// ---------------------------------------------------------------------------

// 下面两份是 2026-10-20 实测响应（结构说明见 quota/commandcode.go 顶部注释）。
// 单测直接用真实载荷，而不是照着实现手写一份"刚好对得上"的 JSON——那种用例
// 只能证明代码自洽，证明不了它读得懂上游。
const commandCodeCreditsPayload = `{"credits":{"belowThreshold":false,"creditThreshold":0,"monthlyCredits":69.7968224856,"purchasedCredits":0,"freeCredits":0},"windowLimits":{"limited":true,"fiveHour":{"used":0.2031775144,"cap":14,"exceeded":false,"resetAt":1792494834039},"weekly":{"used":0.2031775144,"cap":35,"exceeded":false,"resetAt":1793081634039}},"sandboxAccess":true}`

const commandCodeSubsPayload = `{"success":true,"data":{"planId":"individual-goat","status":"active","currentPeriodStart":"2026-10-20T06:05:41.000Z","currentPeriodEnd":"2026-11-20T06:05:41.000Z","cancelAtPeriodEnd":false}}`

// commandCodeHit 是假上游收到的一次请求。
type commandCodeHit struct {
	Path string
	Auth string
}

// newCommandCodeServer 起一个同时提供两个端点的假上游。
//
// 请求记录走缓冲通道而不是共享字段：handler 在别的 goroutine 里跑，
// 共享字段在 -race 下就是数据竞争。
func newCommandCodeServer(t *testing.T, creditsBody, subsBody string, creditsStatus, subsStatus int) (*httptest.Server, <-chan commandCodeHit) {
	t.Helper()
	hits := make(chan commandCodeHit, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- commandCodeHit{Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case commandCodeCreditsPath:
			w.WriteHeader(creditsStatus)
			_, _ = w.Write([]byte(creditsBody))
		case commandCodeSubscriptionsPath:
			w.WriteHeader(subsStatus)
			_, _ = w.Write([]byte(subsBody))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`not found`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// drainCommandCodeHits 取出通道里已完成的请求记录。
//
// RunBuiltin 返回时两次请求的响应都已读完，handler 里的 send 必然发生在返回之前，
// 因此此刻 len(hits) 就是全部记录数。
func drainCommandCodeHits(hits <-chan commandCodeHit) []commandCodeHit {
	n := len(hits)
	out := make([]commandCodeHit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, <-hits)
	}
	return out
}

// commandCodeFixture 返回真实载荷解析出的两个入参。
func commandCodeFixture(t *testing.T) (map[string]any, commandCodePlan) {
	t.Helper()
	credits, ok := parseJSONAny(t, commandCodeCreditsPayload).(map[string]any)
	if !ok {
		t.Fatal("credits 载荷应为对象")
	}
	return credits, commandCodeParseSubscription(parseJSONAny(t, commandCodeSubsPayload))
}

func closeTo(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRunCommandCodeEndToEnd(t *testing.T) {
	srv, hits := newCommandCodeServer(t, commandCodeCreditsPayload, commandCodeSubsPayload, 200, 200)

	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "commandcode", BaseURL: srv.URL, APIKey: "user_test_key",
	}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("应得 3 条（5 小时/每周/每月），实得 %d：%#v", len(res.Items), res.Items)
	}

	got := drainCommandCodeHits(hits)
	if len(got) != 2 {
		t.Fatalf("应打两个端点，实得 %d 次请求：%#v", len(got), got)
	}
	if got[0].Path != commandCodeCreditsPath || got[1].Path != commandCodeSubscriptionsPath {
		t.Fatalf("端点顺序不符: %#v", got)
	}
	for _, h := range got {
		if h.Auth != "Bearer user_test_key" {
			t.Fatalf("%s 的鉴权头不符: %q", h.Path, h.Auth)
		}
	}

	// 月度那行要知道上限才能算已用，上限只能从订阅端点拿到
	monthly := res.Items[2]
	if monthly["id"] != "monthly" || monthly["total"] != float64(70) {
		t.Fatalf("月度行不符: %#v", monthly)
	}
	used, _ := monthly["used"].(float64)
	if !closeTo(used, 70-69.7968224856) {
		t.Fatalf("月度已用应等于上限减剩余，实得 %v", used)
	}

	raw, ok := res.Raw.(map[string]any)
	if !ok {
		t.Fatalf("Raw 应是对象: %#v", res.Raw)
	}
	if _, ok := raw["subscriptionsError"]; ok {
		t.Fatalf("订阅成功时不应有 subscriptionsError：%v", raw["subscriptionsError"])
	}
	if raw["credits"] == nil || raw["subscriptions"] == nil {
		t.Fatal("原始数据应同时保留两个端点的响应")
	}
	meta, _ := raw["meta"].(map[string]any)
	if meta["planName"] != "GOAT" || meta["status"] != "active" {
		t.Fatalf("元信息不符: %#v", meta)
	}
}

func TestRunCommandCodeMissingAPIKey(t *testing.T) {
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "commandcode", BaseURL: "http://127.0.0.1:1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "apiKey") {
		t.Fatalf("缺 API Key 应报错并指明填在哪，实得 %v", err)
	}
}

func TestRunCommandCodeCreditsNotObject(t *testing.T) {
	srv, _ := newCommandCodeServer(t, `[1,2]`, commandCodeSubsPayload, 200, 200)
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "commandcode", BaseURL: srv.URL, APIKey: "k"}, nil)
	if err == nil || !strings.Contains(err.Error(), "不是对象") {
		t.Fatalf("credits 非对象应报错，实得 %v", err)
	}
}

func TestRunCommandCodeSubscriptionsFailureTolerated(t *testing.T) {
	srv, _ := newCommandCodeServer(t, commandCodeCreditsPayload, `{"error":"boom"}`, 200, 500)

	res, err := RunBuiltin(BuiltinConfig{BuiltinID: "commandcode", BaseURL: srv.URL, APIKey: "k"}, nil)
	if err != nil {
		t.Fatalf("订阅失败不该让整次取数失败: %v", err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("两个窗口 + 月度剩余仍应出数，实得 %d", len(res.Items))
	}
	raw := res.Raw.(map[string]any)
	if _, ok := raw["subscriptionsError"]; !ok {
		t.Fatal("订阅失败的原因应留在 subscriptionsError 里，否则查不出为什么缺上限")
	}
	monthly := res.Items[2]
	if monthly["remaining"] == nil || monthly["total"] != nil {
		t.Fatalf("上限未知时月度行应只报剩余，实得 %#v", monthly)
	}
	if monthly["format"] != `{remaining:2} CREDITS` {
		t.Fatalf("上限未知时格式应只报剩余，实得 %q", monthly["format"])
	}
	meta := raw["meta"].(map[string]any)
	if meta["monthlyCapKnown"] != false {
		t.Fatalf("认不出套餐时 monthlyCapKnown 应为 false: %#v", meta)
	}
}

func TestRunCommandCodeStatusHints(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{401, "API Key 无效"},
		{404, "path 覆盖"},
		{429, "刷新间隔"},
	}
	for _, c := range cases {
		srv, _ := newCommandCodeServer(t, `boom`, `boom`, c.status, c.status)
		_, err := RunBuiltin(BuiltinConfig{BuiltinID: "commandcode", BaseURL: srv.URL, APIKey: "k"}, nil)
		if err == nil {
			t.Fatalf("HTTP %d 应报错", c.status)
		}
		if !strings.Contains(err.Error(), "取 credits 失败") {
			t.Fatalf("HTTP %d 的错误应指明是 credits 失败: %v", c.status, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("HTTP %d 应提示「%s」，实得 %v", c.status, c.want, err)
		}
	}
}

func TestRunCommandCodeCustomPath(t *testing.T) {
	paths := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/custom/credits" {
			_, _ = w.Write([]byte(commandCodeCreditsPayload))
			return
		}
		_, _ = w.Write([]byte(commandCodeSubsPayload))
	}))
	defer srv.Close()

	// 只覆盖 credits 的路径；订阅端点仍走默认路径（它没有配置项）
	if _, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "commandcode", BaseURL: srv.URL, Path: "/custom/credits", APIKey: "k",
	}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if first := <-paths; first != "/custom/credits" {
		t.Fatalf("credits 应走自定义路径，实得 %q", first)
	}
	if second := <-paths; second != commandCodeSubscriptionsPath {
		t.Fatalf("订阅端点应走默认路径，实得 %q", second)
	}
}

func TestCommandCodePlanCredits(t *testing.T) {
	// 这张表是月度上限的唯一来源，错了会让「已用 = 上限 - 剩余」整体偏移
	want := map[string]float64{
		"individual-goat":     70,
		"individual-pro":      30,
		"individual-pro-v1":   80,
		"individual-provider": 15,
		"individual-max":      150,
		"individual-ultra":    300,
		"teams-pro":           40,
	}
	if len(commandCodePlanCredits) != len(want) {
		t.Fatalf("套餐数量与参考映射不符：%d vs %d", len(commandCodePlanCredits), len(want))
	}
	for id, credits := range want {
		if got, ok := commandCodePlanCredits[id]; !ok || got != credits {
			t.Fatalf("%s 的月度上限应为 %v，实得 %v（存在=%v）", id, credits, got, ok)
		}
	}
}

func TestCommandCodePlanName(t *testing.T) {
	cases := map[string]string{
		"individual-goat":  "GOAT",
		"individual-ultra": "ULTRA",
		"teams-pro":        "TEAMS-PRO",
		" individual-max ": "MAX",
		"individual-":      "",
		"":                 "",
	}
	for in, want := range cases {
		if got := commandCodePlanName(in); got != want {
			t.Fatalf("planName(%q) 应为 %q，实得 %q", in, want, got)
		}
	}
}

func TestCommandCodeParseSubscription(t *testing.T) {
	t.Run("真实载荷", func(t *testing.T) {
		plan := commandCodeParseSubscription(parseJSONAny(t, commandCodeSubsPayload))
		if plan.ID != "individual-goat" || plan.Name != "GOAT" || plan.Status != "active" {
			t.Fatalf("套餐信息不符: %#v", plan)
		}
		if !plan.CapKnown || plan.MonthlyCap != 70 {
			t.Fatalf("GOAT 的月度上限应为 70: %#v", plan)
		}
		if plan.CancelAtPeriod {
			t.Fatal("cancelAtPeriodEnd=false 不应读成 true")
		}
		if plan.PeriodStart == nil || plan.PeriodEnd == nil {
			t.Fatalf("计费周期应保留原始值: %#v", plan)
		}
	})

	t.Run("取消续费", func(t *testing.T) {
		plan := commandCodeParseSubscription(parseJSONAny(t, `{"data":{"planId":"individual-max","cancelAtPeriodEnd":true}}`))
		if !plan.CancelAtPeriod {
			t.Fatal("cancelAtPeriodEnd=true 应读出来")
		}
		if !plan.CapKnown || plan.MonthlyCap != 150 {
			t.Fatalf("MAX 的月度上限应为 150: %#v", plan)
		}
	})

	t.Run("认不出的套餐", func(t *testing.T) {
		plan := commandCodeParseSubscription(parseJSONAny(t, `{"data":{"planId":"individual-unknown"}}`))
		if plan.CapKnown || plan.MonthlyCap != 0 {
			t.Fatalf("认不出的套餐不该编出上限: %#v", plan)
		}
		if plan.Name != "UNKNOWN" {
			t.Fatalf("认不出的套餐仍应给出名字，实得 %q", plan.Name)
		}
	})

	t.Run("结构不符返回零值", func(t *testing.T) {
		for _, raw := range []any{
			nil, "x", []any{}, 42,
			map[string]any{"data": 42},
			map[string]any{"data": map[string]any{}},
		} {
			plan := commandCodeParseSubscription(raw)
			if plan.ID != "" || plan.CapKnown || plan.CancelAtPeriod {
				t.Fatalf("结构不符应返回零值，输入 %#v 得到 %#v", raw, plan)
			}
		}
	})
}

func TestCommandCodeBuildItems(t *testing.T) {
	t.Run("真实载荷三行", func(t *testing.T) {
		credits, plan := commandCodeFixture(t)
		items, err := commandCodeBuildItems(credits, plan)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 3 {
			t.Fatalf("应得 3 行，实得 %d：%#v", len(items), items)
		}
		for i, wantID := range []string{"fiveHour", "weekly", "monthly"} {
			if items[i]["id"] != wantID {
				t.Fatalf("第 %d 行 id 应为 %q，实得 %v", i, wantID, items[i]["id"])
			}
		}

		fiveHour := items[0]
		if fiveHour["label"] != "5 小时限额" || fiveHour["window"] != string(Window5h) {
			t.Fatalf("5 小时行的标签/窗口不符: %#v", fiveHour)
		}
		if fiveHour["unit"] != string(UnitCredits) {
			t.Fatalf("单位应为 CREDITS: %#v", fiveHour)
		}
		if u, _ := fiveHour["used"].(float64); !closeTo(u, 0.2031775144) {
			t.Fatalf("5 小时已用不符: %v", fiveHour["used"])
		}
		if fiveHour["total"] != float64(14) {
			t.Fatalf("5 小时总量应取 cap(14): %v", fiveHour["total"])
		}
		// resetAt 原样交给契约的 CoerceTime（毫秒 epoch），这里不该提前转
		if fiveHour["resetAt"] != float64(1792494834039) {
			t.Fatalf("resetAt 应原样保留，实得 %#v", fiveHour["resetAt"])
		}
		if _, ok := fiveHour["status"]; ok {
			t.Fatalf("未超限的窗口不该带状态: %#v", fiveHour)
		}

		if items[1]["total"] != float64(35) {
			t.Fatalf("每周总量应取 cap(35): %v", items[1]["total"])
		}

		monthly := items[2]
		if monthly["label"] != "每月额度" || monthly["window"] != string(WindowMonth) {
			t.Fatalf("月度行的标签/窗口不符: %#v", monthly)
		}
		if u, _ := monthly["used"].(float64); !closeTo(u, 70-69.7968224856) {
			t.Fatalf("月度已用应为上限减剩余: %v", monthly["used"])
		}
		if monthly["total"] != float64(70) {
			t.Fatalf("月度总量应取套餐上限: %v", monthly["total"])
		}
		extra, _ := monthly["extra"].(map[string]any)
		if extra["planName"] != "GOAT" || extra["note"] != "GOAT 套餐" {
			t.Fatalf("月度行应带套餐信息: %#v", extra)
		}
		if extra["periodEnd"] == nil || extra["periodStart"] == nil {
			t.Fatalf("月度行应带计费周期: %#v", extra)
		}
	})

	t.Run("认不出套餐时月度只报剩余", func(t *testing.T) {
		credits, _ := commandCodeFixture(t)
		items, err := commandCodeBuildItems(credits, commandCodePlan{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		monthly := items[2]
		if r, _ := monthly["remaining"].(float64); !closeTo(r, 69.7968224856) {
			t.Fatalf("月度剩余不符: %#v", monthly["remaining"])
		}
		if _, ok := monthly["used"]; ok {
			t.Fatalf("上限未知时不该编出已用（那是拿剩余当总数算的）: %#v", monthly)
		}
		if monthly["format"] != `{remaining:2} CREDITS` {
			t.Fatalf("上限未知时格式应只报剩余: %q", monthly["format"])
		}
	})

	t.Run("低于阈值标记预警", func(t *testing.T) {
		raw := strings.Replace(commandCodeCreditsPayload, `"belowThreshold":false`, `"belowThreshold":true`, 1)
		credits := parseJSONAny(t, raw).(map[string]any)
		items, err := commandCodeBuildItems(credits, commandCodePlan{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[2]["status"] != string(StatusWarning) {
			t.Fatalf("belowThreshold=true 应标记预警，实得 %#v", items[2]["status"])
		}
	})

	t.Run("窗口超限标记用尽", func(t *testing.T) {
		raw := strings.Replace(commandCodeCreditsPayload, `"exceeded":false`, `"exceeded":true`, 1)
		credits := parseJSONAny(t, raw).(map[string]any)
		items, err := commandCodeBuildItems(credits, commandCodePlan{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[0]["status"] != string(StatusExhausted) {
			t.Fatalf("exceeded=true 应标记用尽，实得 %#v", items[0]["status"])
		}
	})

	t.Run("缺 windowLimits 仍有月度", func(t *testing.T) {
		credits := map[string]any{"credits": map[string]any{"monthlyCredits": float64(5)}}
		items, err := commandCodeBuildItems(credits, commandCodePlan{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 1 || items[0]["id"] != "monthly" {
			t.Fatalf("应只剩月度行: %#v", items)
		}
	})

	t.Run("两者皆空报错", func(t *testing.T) {
		_, err := commandCodeBuildItems(map[string]any{}, commandCodePlan{})
		if err == nil || !strings.Contains(err.Error(), "没有可识别的额度") {
			t.Fatalf("空响应应明确报错，实得 %v", err)
		}
	})
}

func TestRunCommandCodeNormalizes(t *testing.T) {
	srv, _ := newCommandCodeServer(t, commandCodeCreditsPayload, commandCodeSubsPayload, 200, 200)
	res, err := RunBuiltin(BuiltinConfig{BuiltinID: "commandcode", BaseURL: srv.URL, APIKey: "k"}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}

	items, err := Normalize(res.Items, NormalizeOptions{})
	if err != nil {
		t.Fatalf("适配器产出应能被契约归一: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("应归一出 3 条，实得 %d", len(items))
	}

	// 契约的 toFloat 只认数值本体，所以适配器必须写解引用后的 float64；
	// 这几条断言就是那一课的回回归测试。
	fiveHour := items[0]
	if fiveHour.Used == nil || fiveHour.Total == nil || fiveHour.Percent == nil {
		t.Fatalf("三个分量都不该丢: %#v", fiveHour)
	}
	if !closeTo(*fiveHour.Used, 0.2031775144) || !closeTo(*fiveHour.Total, 14) {
		t.Fatalf("5 小时行数值不符: used=%v total=%v", *fiveHour.Used, *fiveHour.Total)
	}
	if !closeTo(*fiveHour.Percent, 0.2031775144/14*100) {
		t.Fatalf("5 小时百分比不符: %v", *fiveHour.Percent)
	}
	if fiveHour.Window != Window5h || fiveHour.Unit != UnitCredits {
		t.Fatalf("单位/窗口不符: %#v", fiveHour)
	}
	if fiveHour.Text != `0.20 / 14.00 CREDITS` {
		t.Fatalf("展示文本不符: %q", fiveHour.Text)
	}
	wantReset := time.UnixMilli(1792494834039).UTC().Format(time.RFC3339)
	if fiveHour.ResetAt != wantReset {
		t.Fatalf("resetAt 应归一成 %s，实得 %q", wantReset, fiveHour.ResetAt)
	}
	if fiveHour.Status != StatusOK {
		t.Fatalf("用量尚浅应判 OK，实得 %q", fiveHour.Status)
	}

	weekly := items[1]
	if weekly.Window != WindowWeek || !closeTo(*weekly.Total, 35) {
		t.Fatalf("每周行不符: %#v", weekly)
	}

	monthly := items[2]
	if monthly.Window != WindowMonth || monthly.Remaining == nil {
		t.Fatalf("月度行不符: %#v", monthly)
	}
	if !closeTo(*monthly.Remaining, 69.7968224856) {
		t.Fatalf("月度剩余不符: %v", *monthly.Remaining)
	}
	if !closeTo(*monthly.Percent, 0.2031775144/70*100) {
		t.Fatalf("月度百分比不符: %v", *monthly.Percent)
	}
	if monthly.Text != `0.20 / 70.00 CREDITS` {
		t.Fatalf("月度展示文本不符: %q", monthly.Text)
	}
	if monthly.Extra["planName"] != "GOAT" {
		t.Fatalf("套餐名应进 extra: %#v", monthly.Extra)
	}
	if monthly.ResetAt == "" {
		t.Fatal("月度重置时间应来自计费周期结束")
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func fptr(v float64) *float64 { return &v }

func readAll(t *testing.T, r interface{ Read([]byte) (int, error) }) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 64)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}

// parseJSONAny 把 JSON 文本解析成 any（测试用）。
func parseJSONAny(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("测试 JSON 不合法: %v（原文 %s）", err, s)
	}
	return v
}

var _ = time.Second

// ---------------------------------------------------------------------------
// fetchJSON 的各条分支（内置适配器共用的基础设施）
// ---------------------------------------------------------------------------

func TestFetchJSONBranches(t *testing.T) {
	t.Run("构造请求失败", func(t *testing.T) {
		_, err := fetchJSON("http://[::1]:named/x", "", nil, time.Second, nil)
		if err == nil || !strings.Contains(err.Error(), "构造请求失败") {
			t.Fatalf("应报构造请求失败，实得 %v", err)
		}
	})

	t.Run("非 2xx", func(t *testing.T) {
		srv, _ := newJSONServer(t, 503, `down`)
		_, err := fetchJSON(srv.URL, "", nil, time.Second, nil)
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Fatalf("应报 503，实得 %v", err)
		}
	})

	t.Run("非 JSON", func(t *testing.T) {
		srv, _ := newJSONServer(t, 200, `nope`)
		_, err := fetchJSON(srv.URL, "", nil, time.Second, nil)
		if err == nil || !strings.Contains(err.Error(), "不是 JSON") {
			t.Fatalf("应报非 JSON，实得 %v", err)
		}
	})

	t.Run("超过体积上限", func(t *testing.T) {
		prev := maxHTTPBodyBytes
		maxHTTPBodyBytes = 8
		t.Cleanup(func() { maxHTTPBodyBytes = prev })
		srv, _ := newJSONServer(t, 200, `{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":1}`)
		if _, err := fetchJSON(srv.URL, "", nil, time.Second, nil); err == nil ||
			!strings.Contains(err.Error(), "上限") {
			t.Fatalf("应报超上限，实得 %v", err)
		}
	})

	t.Run("读取响应失败", func(t *testing.T) {
		_, err := fetchJSON("http://example.com/x", "", nil, time.Second,
			&http.Client{Transport: brokenTransport{}})
		if err == nil || !strings.Contains(err.Error(), "读取响应失败") {
			t.Fatalf("应报读取失败，实得 %v", err)
		}
	})

	t.Run("带 apiKey 与自定义头", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := fetchJSON(srv.URL, "sk-k", map[string]any{"X-H": "{{apiKey}}"}, time.Second, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("Authorization"); got != "Bearer sk-k" {
			t.Fatalf("鉴权头不符: %q", got)
		}
		if got := rec.Headers.Get("X-H"); got != "sk-k" {
			t.Fatalf("自定义头占位符未插值: %q", got)
		}
	})

	t.Run("nil client 且超时为零走默认", func(t *testing.T) {
		srv, _ := newJSONServer(t, 200, `{}`)
		if _, err := fetchJSON(srv.URL, "", nil, 0, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
	})

	t.Run("传入 client 时由其负责超时", func(t *testing.T) {
		srv, _ := newJSONServer(t, 200, `{"a":1}`)
		got, err := fetchJSON(srv.URL, "", nil, time.Second, &http.Client{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if m, ok := got.(map[string]any); !ok || m["a"] != float64(1) {
			t.Fatalf("响应不符: %#v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// 少量可达的边界分支
// ---------------------------------------------------------------------------

func TestMicroToUSDNegative(t *testing.T) {
	// 负数保留符号（sign 的负分支）；上游理论上不会给，但不该被静默改成正数
	got := microToUSD(float64(-1e8))
	if got == nil || *got != -1 {
		t.Fatalf("负数应保留符号，实得 %v", got)
	}
}

func TestHasScnetSessionCookieNilJar(t *testing.T) {
	if hasScnetSessionCookie(nil, &url.URL{}) {
		t.Fatal("jar 为 nil 时应为 false")
	}
	jar, _ := cookiejar.New(nil)
	if hasScnetSessionCookie(jar, &url.URL{Scheme: "https", Host: "x"}) {
		t.Fatal("空 jar 应为 false")
	}
	if hasScnetSessionCookie(jar, nil) {
		t.Fatal("url 为 nil 时应为 false")
	}
}

func TestNewScnetClientRedirectCap(t *testing.T) {
	c, err := newScnetClient(time.Second)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 首跳放行
	if err := c.CheckRedirect(&http.Request{}, nil); err != nil {
		t.Fatalf("首跳应放行，实得 %v", err)
	}
	// 达到 8 跳即拒绝，避免重定向环
	via := make([]*http.Request, 8)
	if err := c.CheckRedirect(&http.Request{}, via); err == nil {
		t.Fatal("超过 8 跳应被拒绝")
	}
}

func TestScnetAPIGetRequestFailures(t *testing.T) {
	t.Run("构造请求失败", func(t *testing.T) {
		_, err := scnetAPIGet(&http.Client{}, "", "http://[::1]:named/x")
		if err == nil || !strings.Contains(err.Error(), "构造请求失败") {
			t.Fatalf("应报构造请求失败，实得 %v", err)
		}
	})

	t.Run("读取响应失败", func(t *testing.T) {
		_, err := scnetAPIGet(&http.Client{Transport: brokenTransport{}}, "http://example.com", "/x")
		if err == nil || !strings.Contains(err.Error(), "读取") {
			t.Fatalf("应报读取失败，实得 %v", err)
		}
	})
}

func TestScnetLoginFailures(t *testing.T) {
	t.Run("登录页地址非法", func(t *testing.T) {
		c, _ := newScnetClient(time.Second)
		err := scnetLogin(c, "http://[::1]:named", "u", "p")
		if err == nil || !strings.Contains(err.Error(), "构造登录页请求失败") {
			t.Fatalf("应报构造失败，实得 %v", err)
		}
	})

	t.Run("连不上登录页", func(t *testing.T) {
		c, _ := newScnetClient(2 * time.Second)
		err := scnetLogin(c, "http://127.0.0.1:1", "u", "p")
		if err == nil || !strings.Contains(err.Error(), "打开登录页失败") {
			t.Fatalf("应报打开失败，实得 %v", err)
		}
	})

	t.Run("读取登录页失败", func(t *testing.T) {
		c := &http.Client{Transport: brokenTransport{}}
		err := scnetLogin(c, "http://example.com", "u", "p")
		if err == nil || !strings.Contains(err.Error(), "读取登录页失败") {
			t.Fatalf("应报读取失败，实得 %v", err)
		}
	})
}

func TestRunOpencodeBadURL(t *testing.T) {
	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "opencode", BaseURL: "http://[::1]:named",
		Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "构造请求失败") {
		t.Fatalf("应报构造请求失败，实得 %v", err)
	}
}

func TestRunBuiltinFetchJSONError(t *testing.T) {
	// deepseek 走 fetchJSON：上游 500 时应把错误带出来
	srv, _ := newJSONServer(t, 500, `x`)
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "moonshot", BaseURL: srv.URL}, nil)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("应报上游错误，实得 %v", err)
	}
}

func TestRunSCNetLoginFailureSurface(t *testing.T) {
	// 连不上时错误应能透出，而不是变成"未查询到套餐"
	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: "http://127.0.0.1:1",
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "打开登录页失败") {
		t.Fatalf("应透出登录失败原因，实得 %v", err)
	}
}

func TestRunSCNetTimeoutConfig(t *testing.T) {
	srv, _ := scnetTestServer(t, scnetTestOpts{})
	// 指定超时走非默认分支
	if _, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL, TimeoutMs: 8000,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
}

func TestRunOpencodeRemainingBranches(t *testing.T) {
	t.Run("带 TimeoutMs", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"access":{"meters":{"month":{"usedMicroCents":1e8,"limitMicroCents":1e8}}}}`))
		}))
		defer srv.Close()
		if _, err := RunBuiltin(BuiltinConfig{
			BuiltinID: "opencode", BaseURL: srv.URL, TimeoutMs: 3000,
			Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
		}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
	})

	t.Run("超时低于下限时抬到 1 秒", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"access":{"meters":{"month":{"usedMicroCents":1e8,"limitMicroCents":1e8}}}}`))
		}))
		defer srv.Close()
		if _, err := RunBuiltin(BuiltinConfig{
			BuiltinID: "opencode", BaseURL: srv.URL, TimeoutMs: 1,
			Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
		}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
	})

	t.Run("请求失败", func(t *testing.T) {
		_, err := RunBuiltin(BuiltinConfig{
			BuiltinID: "opencode", BaseURL: "http://127.0.0.1:1",
			Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "请求失败") {
			t.Fatalf("应报请求失败，实得 %v", err)
		}
	})

	t.Run("读取响应失败", func(t *testing.T) {
		// runOpencode 自建 client，无法注入 Transport —— 用提前关闭的服务端制造读取错误
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				return
			}
			conn, _, _ := hj.Hijack()
			// 声明一个大 body 却立刻断开，客户端读取时就会出错
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\nshort"))
			_ = conn.Close()
		}))
		defer srv.Close()
		_, err := RunBuiltin(BuiltinConfig{
			BuiltinID: "opencode", BaseURL: srv.URL,
			Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "读取响应失败") {
			t.Fatalf("应报读取失败，实得 %v", err)
		}
	})

	t.Run("meters 不可用时报错透出", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"access":{}}`))
		}))
		defer srv.Close()
		_, err := RunBuiltin(BuiltinConfig{
			BuiltinID: "opencode", BaseURL: srv.URL,
			Env: map[string]string{"OPENCODE_COOKIE": "st_x"},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "meters") {
			t.Fatalf("应报缺 meters，实得 %v", err)
		}
	})
}

func TestScnetRandHexSourceFailure(t *testing.T) {
	// 随机源不可用时退回全零（匿名 ID 不该让整条取数失败）。
	// 无法在测试里让 crypto/rand 失败，这里只验证格式契约本身。
	got := randHex(12)
	if len(got) != 12 {
		t.Fatalf("长度不符: %q", got)
	}
	for _, c := range got {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("含非十六进制字符: %q", got)
		}
	}
}

// errorTransport 让任何请求在传输层失败，用来覆盖 client.Do 的错误分支。
type errorTransport struct{}

func (errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("模拟传输失败")
}

func TestFetchJSONTransportError(t *testing.T) {
	_, err := fetchJSON("http://example.com/x", "", nil, time.Second,
		&http.Client{Transport: errorTransport{}})
	if err == nil || !strings.Contains(err.Error(), "请求失败") {
		t.Fatalf("应报请求失败，实得 %v", err)
	}
}

func TestRunBuiltinTransportError(t *testing.T) {
	// 走 fetchJSON 的适配器：传入的 client 在传输层直接失败
	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "deepseek", BaseURL: "http://example.com",
	}, &http.Client{Transport: errorTransport{}})
	if err == nil || !strings.Contains(err.Error(), "请求失败") {
		t.Fatalf("应报请求失败，实得 %v", err)
	}
}

func TestRunBuiltinGenericExtractError(t *testing.T) {
	// 通用映射分支里 extractItems 失败应把错误带出来
	srv, _ := newJSONServer(t, 200, `{"a":1}`)
	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "custom", BaseURL: srv.URL, Path: "/x", ItemsPath: "nope",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("应报取出失败，实得 %v", err)
	}
}

func TestRunBuiltinParserError(t *testing.T) {
	// 走自带解析的适配器：响应是合法 JSON 但解析不出来
	srv, _ := newJSONServer(t, 200, `{"balance_infos":[1,2]}`)
	_, err := RunBuiltin(BuiltinConfig{BuiltinID: "deepseek", BaseURL: srv.URL}, nil)
	if err == nil || !strings.Contains(err.Error(), "没有可用条目") {
		t.Fatalf("应报解析失败，实得 %v", err)
	}
}

func TestScnetAPIGetTransportError(t *testing.T) {
	_, err := scnetAPIGet(&http.Client{Transport: errorTransport{}}, "http://example.com", "/x")
	if err == nil || !strings.Contains(err.Error(), "请求") {
		t.Fatalf("应报请求失败，实得 %v", err)
	}
}

func TestScnetExecutionFromSecondRegexForm(t *testing.T) {
	// execution 的两种属性顺序都要认：value 在前、name 在后
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input value="EXEC-2" name="execution"/>`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TGC", Value: "t", Path: "/"})
		case "/acx/user/users/current-user-info":
			writeEnvelope(w, `{"id":"u"}`)
		case "/acx/charge/account/currentuser/tokenplan/list":
			writeEnvelope(w, `[{"status":"enable","resourceId":"p","name":"n","usedAmount":1,"totalAmount":2}]`)
		case "/acx/llm/api/package/overview":
			writeEnvelope(w, `[]`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	res, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err != nil {
		t.Fatalf("第二种属性顺序应被识别: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(res.Items))
	}
}

func TestScnetPostLoginConnectionDrops(t *testing.T) {
	// GET 正常、POST 时直接断开连接 -> client.Do 报错
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sso/login" {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<input name="execution" value="E"/>`))
			return
		}
		// 提交登录时把连接掐掉
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil {
		t.Fatal("连接被掐断应当报错")
	}
	if !strings.Contains(err.Error(), "提交登录失败") {
		t.Fatalf("应报提交登录失败，实得 %v", err)
	}
}

func TestScnetPlansAPIFailure(t *testing.T) {
	// 登录与用户信息正常，但套餐列表接口业务失败
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="execution" value="E"/>`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TGC", Value: "t", Path: "/"})
		case "/acx/user/users/current-user-info":
			writeEnvelope(w, `{"id":"u"}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"500","data":null}`))
		}
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "会话可能已失效") {
		t.Fatalf("应报套餐接口失败，实得 %v", err)
	}
}

func TestScnetAPIGetConnectionDrops(t *testing.T) {
	// 登录成功，但控制台接口读取时连接断开
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sso/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="execution" value="E"/>`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TGC", Value: "t", Path: "/"})
		default:
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	_, err := RunBuiltin(BuiltinConfig{
		BuiltinID: "scnet", BaseURL: srv.URL,
		Env: map[string]string{"SCNET_USER": "u", "SCNET_PASS": "p"},
	}, nil)
	if err == nil {
		t.Fatal("接口连接断开应当报错")
	}
}

func TestScnetModelsCappedAt60(t *testing.T) {
	models := make([]string, 0, 65)
	for i := 0; i < 65; i++ {
		models = append(models, "m"+strconv.Itoa(i))
	}
	overview := []any{map[string]any{
		"code": "p", "name": "P", "models": strings.Join(models, ","),
		"request": []any{map[string]any{"unit": "month", "time": 1, "limit": 10}},
	}}
	plans := []any{map[string]any{
		"status": "enable", "resourceId": "p", "name": "n", "usedAmount": 1, "totalAmount": 2,
	}}
	items := scnetBuildItems(plans, overview)
	if len(items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(items))
	}
	extra := items[0]["extra"].(map[string]any)
	if extra["modelCount"] != 65 {
		t.Fatalf("modelCount 应为真实数量 65，实得 %#v", extra["modelCount"])
	}
	if got := extra["models"].([]string); len(got) != 60 {
		t.Fatalf("models 应截到 60 条，实得 %d", len(got))
	}
}

func TestScnetBuildItemsUnknownWindowUnit(t *testing.T) {
	// 定义里出现契约不认识的时间单位时，窗口标签回退成原文而不是空串
	overview := []any{map[string]any{
		"code": "p", "name": "P",
		"request": []any{map[string]any{"unit": "fortnight", "time": 2, "limit": 5}},
	}}
	items := scnetBuildItems(nil, overview)
	if len(items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(items))
	}
	if items[0]["label"] != "P 2fortnight额度" {
		t.Fatalf("未知单位应回退原文，实得 %#v", items[0]["label"])
	}
}

func TestScnetBuildItemsPlanWithoutOverviewEntry(t *testing.T) {
	// 套餐记录找不到对应的定义时，窗口应为空但条目仍要出
	plans := []any{map[string]any{
		"status": "enable", "resourceId": "unknown", "name": "孤儿套餐",
		"usedAmount": 1, "totalAmount": 2,
	}}
	items := scnetBuildItems(plans, nil)
	if len(items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(items))
	}
	if items[0]["window"] != WindowNone {
		t.Fatalf("无定义时窗口应为空，实得 %#v", items[0]["window"])
	}
}

// failReader 立刻报错，用来覆盖读取失败的分支。
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("读取中断") }

func TestDecodeRequestReadError(t *testing.T) {
	if _, err := decodeRequest(failReader{}); err == nil {
		t.Fatal("读取失败应报错")
	}
}

func TestGuardHostReachableBranches(t *testing.T) {
	t.Run("空主机名", func(t *testing.T) {
		err := guardHost("")
		if err == nil || !strings.Contains(err.Error(), "缺少主机名") {
			t.Fatalf("应报缺主机名，实得 %v", err)
		}
	})

	t.Run("主机名过长导致解析失败", func(t *testing.T) {
		// 单个标签超过 63 字符会在发起 DNS 之前就被判为非法，
		// 比"查一个不存在域名"更确定（不受本地 DNS 影响）
		bad := strings.Repeat("a", 300) + ".com"
		err := guardHost(bad)
		if err == nil {
			t.Fatal("非法主机名应报错")
		}
		if !strings.Contains(err.Error(), "无法解析主机") && !strings.Contains(err.Error(), "拒绝访问") {
			t.Fatalf("应报解析失败或拒绝，实得 %v", err)
		}
	})
}

func TestEncryptPasswordWithKeyErrors(t *testing.T) {
	t.Run("公钥不可解析", func(t *testing.T) {
		if _, err := encryptPasswordWithKey("p", "not-a-key"); err == nil {
			t.Fatal("坏公钥应报错")
		}
	})

	t.Run("明文超长", func(t *testing.T) {
		// 512 位密钥的明文上限是 53 字节
		if _, err := encryptPasswordWithKey(strings.Repeat("x", 54), scnetPublicKeyBase64); err == nil ||
			!strings.Contains(err.Error(), "明文过长") {
			t.Fatalf("超长明文应报错，实得 %v", err)
		}
	})
}
