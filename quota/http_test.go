package quota

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Fill：占位符插值
// ---------------------------------------------------------------------------

func TestFillPlaceholders(t *testing.T) {
	vars := HTTPAdapterVars{APIKey: "sk-1", BaseURL: "https://api.x", ID: "p1", Name: "P"}

	cases := []struct{ name, in, want string }{
		{"空串", "", ""},
		{"无占位符", "https://api.x/v1/usage", "https://api.x/v1/usage"},
		{"baseUrl", "{{baseUrl}}/user/balance", "https://api.x/user/balance"},
		{"apiKey", "Bearer {{apiKey}}", "Bearer sk-1"},
		{"多占位符", "{{id}}-{{name}}", "p1-P"},
		{"未提供的原样保留", "{{apiKey}}/{{missing}}", "sk-1/{{missing}}"},
		{"变量名含空格不认", "{{ apiKey }}", "{{ apiKey }}"},
		{"变量名含中文不认", "{{中文}}", "{{中文}}"},
		{"未闭合", "{{apiKey", "{{apiKey"},
		{"只有一个右括号", "{{apiKey}", "{{apiKey}"},
		{"单花括号", "{apiKey}", "{apiKey}"},
		{"空变量名", "{{}}", "{{}}"},
		{"混合", "a{{apiKey}}b{{}}c", "ask-1b{{}}c"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Fill(c.in, vars); got != c.want {
				t.Fatalf("Fill(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsValidVarName(t *testing.T) {
	for _, s := range []string{"a", "A1", "_x", "apiKey"} {
		if !isValidVarName(s) {
			t.Fatalf("%q 应为合法变量名", s)
		}
	}
	for _, s := range []string{"", "a b", "中", "a-b", "a.b"} {
		if isValidVarName(s) {
			t.Fatalf("%q 应为非法变量名", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Pick：点路径取值
// ---------------------------------------------------------------------------

func TestPick(t *testing.T) {
	obj := map[string]any{
		"a":   map[string]any{"b": map[string]any{"c": 42}},
		"arr": []any{map[string]any{"v": "x"}, map[string]any{"v": "y"}},
		"n":   nil,
		"s":   "str",
	}

	cases := []struct {
		name string
		path string
		want any
	}{
		{"空路径取不到", "", nil},
		{"深层路径", "a.b.c", 42},
		{"中间层对象", "a.b", map[string]any{"c": 42}},
		{"键不存在", "a.x", nil},
		{"数组下标 0", "arr.0.v", "x"},
		{"数组下标 1", "arr.1.v", "y"},
		{"数组下标越界", "arr.5.v", nil},
		{"数组下标非数字", "arr.x.v", nil},
		{"值为 nil 时取不到", "n.x", nil},
		{"中间不是容器", "s.x", nil},
		{"空段被忽略", "a..c", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 用 DeepEqual 而非 ==：want 可能是 map，直接用 == 会 panic
			if got := Pick(obj, c.path); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Pick(%q) = %#v，期望 %#v", c.path, got, c.want)
			}
		})
	}

	t.Run("全空段返回原对象", func(t *testing.T) {
		got := Pick(obj, "..")
		if _, ok := got.(map[string]any); !ok {
			t.Fatalf("期望原对象，实得 %#v", got)
		}
	})
}

func TestParseIndex(t *testing.T) {
	for _, s := range []string{"0", "12", "999"} {
		if _, err := parseIndex(s); err != nil {
			t.Fatalf("%q 应能解析", s)
		}
	}
	for _, s := range []string{"", "x", "1a", "99999999"} {
		if _, err := parseIndex(s); err == nil {
			t.Fatalf("%q 应解析失败", s)
		}
	}
}

// ---------------------------------------------------------------------------
// MapRow：字段映射
// ---------------------------------------------------------------------------

func TestMapRow(t *testing.T) {
	row := map[string]any{
		"used":  10,
		"total": 100,
		"deep":  map[string]any{"x": "y"},
		"nilv":  nil,
	}
	mapping := map[string]any{
		"used":    "used",
		"total":   "total",
		"nested":  "deep.x",
		"literal": "=字面量",
		"empty":   "",
		"skipped": "not.there",
		"nilpath": "nilv",
		"number":  123,
		"bareEq":  "=",
	}

	out := MapRow(row, mapping)

	if out["used"] != 10 || out["total"] != 100 {
		t.Fatalf("直接路径映射不符: %#v", out)
	}
	if out["nested"] != "y" {
		t.Fatalf("嵌套路径映射不符: %#v", out["nested"])
	}
	if out["literal"] != "字面量" {
		t.Fatalf("字面量映射不符: %#v", out["literal"])
	}
	if out["bareEq"] != "" {
		t.Fatalf("单个等号应映射成空串: %#v", out["bareEq"])
	}
	for _, k := range []string{"empty", "skipped", "nilpath", "number"} {
		if _, has := out[k]; has {
			t.Fatalf("键 %q 不应出现在结果里: %#v", k, out)
		}
	}
}

// ---------------------------------------------------------------------------
// extractItems：取条目行
// ---------------------------------------------------------------------------

func TestExtractItems(t *testing.T) {
	t.Run("裸数组", func(t *testing.T) {
		items, err := extractItems([]any{
			map[string]any{"used": 1},
			map[string]any{"used": 2},
		}, HTTPAdapterConfig{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("应有 2 条，实得 %d", len(items))
		}
		if items[0]["id"] != "item-1" || items[0]["label"] != "item-1" {
			t.Fatalf("id/label 未补默认值: %#v", items[0])
		}
		if items[1]["id"] != "item-2" {
			t.Fatalf("第二条 id 不符: %#v", items[1])
		}
	})

	t.Run("单个对象当一条", func(t *testing.T) {
		items, err := extractItems(map[string]any{"used": 7}, HTTPAdapterConfig{})
		if err != nil || len(items) != 1 {
			t.Fatalf("应得 1 条，实得 %d（err=%v）", len(items), err)
		}
	})

	t.Run("itemsPath 取嵌套数组", func(t *testing.T) {
		data := map[string]any{"data": map[string]any{"items": []any{map[string]any{"used": 3}}}}
		items, err := extractItems(data, HTTPAdapterConfig{ItemsPath: "data.items"})
		if err != nil || len(items) != 1 {
			t.Fatalf("应得 1 条，实得 %d（err=%v）", len(items), err)
		}
	})

	t.Run("itemsPath 取不到时报错", func(t *testing.T) {
		_, err := extractItems(map[string]any{"a": 1}, HTTPAdapterConfig{ItemsPath: "nope"})
		if err == nil {
			t.Fatal("取不到应报错")
		}
		if !strings.Contains(err.Error(), "nope") {
			t.Fatalf("错误里应带上路径，实得 %q", err.Error())
		}
	})

	t.Run("取到标量时报错", func(t *testing.T) {
		_, err := extractItems("scalar", HTTPAdapterConfig{ItemsPath: "x"})
		if err == nil {
			t.Fatal("标量应报错")
		}
	})

	t.Run("无 itemsPath 且响应是标量时报错", func(t *testing.T) {
		_, err := extractItems(42, HTTPAdapterConfig{})
		if err == nil {
			t.Fatal("标量应报错")
		}
		if !strings.Contains(err.Error(), "(空)") {
			t.Fatalf("空路径应显示 (空)，实得 %q", err.Error())
		}
	})

	t.Run("map 为空时整行透传", func(t *testing.T) {
		// 对端返回的就是接近契约形状的结构，无需写 map
		items, err := extractItems(map[string]any{"used": 25, "total": 100, "unit": "%"}, HTTPAdapterConfig{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[0]["used"] != 25 {
			t.Fatalf("字段应被透传: %#v", items[0])
		}
		// 透传后契约仍能正常归一
		norm, err := Normalize(items, NormalizeOptions{})
		if err != nil {
			t.Fatalf("归一应成功: %v", err)
		}
		if len(norm) != 1 || norm[0].Percent == nil || *norm[0].Percent != 25 {
			t.Fatalf("归一结果不符: %#v", norm)
		}
	})

	t.Run("数组元素非对象则跳过", func(t *testing.T) {
		items, err := extractItems([]any{42, map[string]any{"used": 1}, "str"}, HTTPAdapterConfig{})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("只应保留对象元素，实得 %d 条: %#v", len(items), items)
		}
		// id 按原始下标编号（跳过元素后不重排），因此是 item-2
		if items[0]["id"] != "item-2" {
			t.Fatalf("id 应按原始下标编号: %#v", items[0]["id"])
		}
	})

	t.Run("constants 被 map 覆盖", func(t *testing.T) {
		items, err := extractItems(
			map[string]any{"u": "CNY"},
			HTTPAdapterConfig{
				Constants: map[string]any{"unit": "USD", "window": "total"},
				Map:       map[string]any{"unit": "u"},
			})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[0]["unit"] != "CNY" {
			t.Fatalf("map 应覆盖 constants，实得 %#v", items[0]["unit"])
		}
		if items[0]["window"] != "total" {
			t.Fatalf("constants 未生效: %#v", items[0])
		}
	})

	t.Run("map 是白名单不透传未映射字段", func(t *testing.T) {
		// 配了 map 就只保留被映射的字段（与原实现一致）。
		// 上游自带的 id 不在 map 里，因此不会被当成条目 id。
		items, err := extractItems(map[string]any{"id": "mine", "used": 3}, HTTPAdapterConfig{
			Map: map[string]any{"used": "used"},
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[0]["used"] != 3 {
			t.Fatalf("被映射字段应保留: %#v", items[0])
		}
		if items[0]["id"] != "item-1" {
			t.Fatalf("未映射的 id 不应透传，应自动编号: %#v", items[0])
		}
	})

	t.Run("map 定义了 id 且无 label 时 label 跟随 id", func(t *testing.T) {
		items, err := extractItems(map[string]any{"k": "abc"}, HTTPAdapterConfig{
			Map: map[string]any{"id": "k", "used": "used"},
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if items[0]["id"] != "abc" {
			t.Fatalf("映射的 id 未生效: %#v", items[0])
		}
		if items[0]["label"] != "abc" {
			t.Fatalf("label 应跟随 id: %#v", items[0])
		}
	})
}

func TestItemsPathLabel(t *testing.T) {
	if itemsPathLabel("") != "(空)" {
		t.Fatal("空路径应显示 (空)")
	}
	if itemsPathLabel("a.b") != "a.b" {
		t.Fatal("非空路径应原样返回")
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func TestToStrForHeader(t *testing.T) {
	if got := toStrForHeader("x"); got != "x" {
		t.Fatalf("字符串应原样返回，实得 %q", got)
	}
	if got := toStrForHeader(12); got != "12" {
		t.Fatalf("数字应序列化，实得 %q", got)
	}
	if got := toStrForHeader(make(chan int)); got != "" {
		t.Fatalf("不可序列化应返回空串，实得 %q", got)
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]any{"c": 1, "a": 2, "b": 3})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("长度不符: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项应为 %q，实得 %q（全量 %#v）", i, want[i], got[i], got)
		}
	}
}

// ---------------------------------------------------------------------------
// RunHTTP：端到端（httptest 服务器）
// ---------------------------------------------------------------------------

// recordedReq 记录服务端收到的请求。
//
// 必须在 handler 内部就把 body 读出来：请求返回后 r.Body 已被消费，读不到内容。
type recordedReq struct {
	Method  string
	Headers http.Header
	Body    string
}

func newJSONServer(t *testing.T, status int, body string) (*httptest.Server, *recordedReq) {
	t.Helper()
	rec := &recordedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.Method = r.Method
		rec.Headers = r.Header.Clone()
		rec.Body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestRunHTTPMissingURL(t *testing.T) {
	if _, err := RunHTTP(HTTPAdapterConfig{}, HTTPAdapterVars{}, nil); err == nil {
		t.Fatal("未配置 url 应报错")
	}
	if _, err := RunHTTP(HTTPAdapterConfig{URL: "{{missing}}"}, HTTPAdapterVars{}, nil); err == nil {
		t.Fatal("插值后为空应报错")
	}
}

func TestRunHTTPSuccessBearerDefault(t *testing.T) {
	// 注意：不能用 unit:% 搭 used/total —— 契约规定 % 单位下 remaining 本身就是百分比，
	// used/total 会被当成「剩余 4%」。这里用普通数值验证百分比推导。
	srv, rec := newJSONServer(t, 200, `{"items":[{"used":25,"total":100}]}`)

	res, err := RunHTTP(HTTPAdapterConfig{
		URL:       srv.URL + "/usage",
		ItemsPath: "items",
		Auth:      HTTPAuth{Type: "bearer"},
	}, HTTPAdapterVars{APIKey: "sk-abc"}, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(res.Items))
	}
	if got := rec.Headers.Get("Authorization"); got != "Bearer sk-abc" {
		t.Fatalf("bearer 头不符: %q", got)
	}
	if got := rec.Headers.Get("Accept"); got != "application/json" {
		t.Fatalf("默认 Accept 头不符: %q", got)
	}
	if res.Raw == nil {
		t.Fatal("应带回原始响应体")
	}

	items, err := Normalize(res.Items, NormalizeOptions{})
	if err != nil {
		t.Fatalf("归一应成功: %v", err)
	}
	if len(items) != 1 || items[0].Percent == nil || *items[0].Percent != 25 {
		t.Fatalf("归一结果不符: %#v", items)
	}
}

func TestRunHTTPAuthTypes(t *testing.T) {
	t.Run("类型留空且有密钥则按 bearer", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL}, HTTPAdapterVars{APIKey: "k"}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("Authorization"); got != "Bearer k" {
			t.Fatalf("应默认 bearer，实得 %q", got)
		}
	})

	t.Run("类型留空且无密钥则不加头", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL}, HTTPAdapterVars{}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("Authorization"); got != "" {
			t.Fatalf("不应加鉴权头，实得 %q", got)
		}
	})

	t.Run("header 型默认头名", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{
			URL:  srv.URL,
			Auth: HTTPAuth{Type: "header"},
		}, HTTPAdapterVars{APIKey: "k"}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("x-api-key"); got != "k" {
			t.Fatalf("header 型默认头名不符: %q", got)
		}
	})

	t.Run("header 型自定义头名", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{
			URL:  srv.URL,
			Auth: HTTPAuth{Type: "header", Header: "X-Token"},
		}, HTTPAdapterVars{APIKey: "k"}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("X-Token"); got != "k" {
			t.Fatalf("自定义头名不符: %q", got)
		}
	})

	t.Run("bearer 型 token 为空时不加头", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{
			URL:  srv.URL,
			Auth: HTTPAuth{Type: "bearer"},
		}, HTTPAdapterVars{}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("Authorization"); got != "" {
			t.Fatalf("凭证为空不应加头，实得 %q", got)
		}
	})

	t.Run("basic 型", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{
			URL:  srv.URL,
			Auth: HTTPAuth{Type: "basic", User: "u1"},
		}, HTTPAdapterVars{APIKey: "p1"}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u1:p1"))
		if got := rec.Headers.Get("Authorization"); got != want {
			t.Fatalf("basic 头不符: %q，期望 %q", got, want)
		}
	})

	t.Run("none 型", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{
			URL:  srv.URL,
			Auth: HTTPAuth{Type: "none"},
		}, HTTPAdapterVars{APIKey: "k"}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("Authorization"); got != "" {
			t.Fatalf("none 型不应加头，实得 %q", got)
		}
	})

	t.Run("token 模板可插值", func(t *testing.T) {
		srv, rec := newJSONServer(t, 200, `{}`)
		if _, err := RunHTTP(HTTPAdapterConfig{
			URL:  srv.URL,
			Auth: HTTPAuth{Type: "header", Header: "X-K", Token: "tok-{{apiKey}}"},
		}, HTTPAdapterVars{APIKey: "z"}, nil); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got := rec.Headers.Get("X-K"); got != "tok-z" {
			t.Fatalf("token 模板插值不符: %q", got)
		}
	})
}

func TestRunHTTPHeadersInterpolated(t *testing.T) {
	srv, rec := newJSONServer(t, 200, `{}`)
	if _, err := RunHTTP(HTTPAdapterConfig{
		URL:     srv.URL,
		Headers: map[string]any{"X-Org": "{{id}}", "X-Num": 7},
	}, HTTPAdapterVars{ID: "p9"}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if got := rec.Headers.Get("X-Org"); got != "p9" {
		t.Fatalf("header 占位符未插值: %q", got)
	}
	if got := rec.Headers.Get("X-Num"); got != "7" {
		t.Fatalf("非字符串 header 值不符: %q", got)
	}
}

func TestRunHTTPMethodDefaultIsGet(t *testing.T) {
	srv, rec := newJSONServer(t, 200, `{}`)
	if _, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL}, HTTPAdapterVars{}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if rec.Method != http.MethodGet {
		t.Fatalf("默认方法应为 GET，实得 %q", rec.Method)
	}
}

func TestRunHTTPPostStringBodyInterpolated(t *testing.T) {
	srv, rec := newJSONServer(t, 200, `{}`)
	if _, err := RunHTTP(HTTPAdapterConfig{
		URL:    srv.URL,
		Method: "post",
		Body:   `{"key":"{{apiKey}}"}`,
	}, HTTPAdapterVars{APIKey: "sk-x"}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if rec.Method != http.MethodPost {
		t.Fatalf("方法应为 POST，实得 %q", rec.Method)
	}
	if rec.Body != `{"key":"sk-x"}` {
		t.Fatalf("字符串 body 应插值，实得 %q", rec.Body)
	}
	if got := rec.Headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("应补 Content-Type，实得 %q", got)
	}
}

func TestRunHTTPPostObjectBodyNotInterpolated(t *testing.T) {
	srv, rec := newJSONServer(t, 200, `{}`)
	if _, err := RunHTTP(HTTPAdapterConfig{
		URL:    srv.URL,
		Method: http.MethodPost,
		Body:   map[string]any{"k": "{{apiKey}}"},
	}, HTTPAdapterVars{APIKey: "sk-x"}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if rec.Body != `{"k":"{{apiKey}}"}` {
		t.Fatalf("对象 body 不应插值，实得 %q", rec.Body)
	}
}

func TestRunHTTPGetIgnoresBody(t *testing.T) {
	srv, rec := newJSONServer(t, 200, `{}`)
	if _, err := RunHTTP(HTTPAdapterConfig{
		URL:  srv.URL,
		Body: map[string]any{"k": 1},
	}, HTTPAdapterVars{}, nil); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if rec.Body != "" {
		t.Fatalf("GET 不应带 body，实得 %q", rec.Body)
	}
}

func TestRunHTTPBodyMarshalError(t *testing.T) {
	srv, _ := newJSONServer(t, 200, `{}`)
	_, err := RunHTTP(HTTPAdapterConfig{
		URL:    srv.URL,
		Method: http.MethodPost,
		Body:   make(chan int),
	}, HTTPAdapterVars{}, nil)
	if err == nil || !strings.Contains(err.Error(), "无法序列化") {
		t.Fatalf("应报序列化错误，实得 %v", err)
	}
}

func TestRunHTTPNon2xx(t *testing.T) {
	srv, _ := newJSONServer(t, 402, `{"error":"insufficient balance"}`)
	_, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL}, HTTPAdapterVars{}, nil)
	if err == nil {
		t.Fatal("非 2xx 应报错")
	}
	if !strings.Contains(err.Error(), "402") {
		t.Fatalf("错误里应带状态码，实得 %q", err.Error())
	}
	if !strings.Contains(err.Error(), "insufficient balance") {
		t.Fatalf("错误里应带响应片段，实得 %q", err.Error())
	}
}

func TestRunHTTPNonJSON(t *testing.T) {
	srv, _ := newJSONServer(t, 200, `not json at all`)
	_, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL}, HTTPAdapterVars{}, nil)
	if err == nil || !strings.Contains(err.Error(), "不是 JSON") {
		t.Fatalf("应报非 JSON 错误，实得 %v", err)
	}
}

func TestRunHTTPBodySizeCap(t *testing.T) {
	prev := maxHTTPBodyBytes
	maxHTTPBodyBytes = 16
	t.Cleanup(func() { maxHTTPBodyBytes = prev })

	srv, _ := newJSONServer(t, 200, `{"items":[{"used":1},{"used":2}]}`)
	_, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL}, HTTPAdapterVars{}, nil)
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("应报超过上限，实得 %v", err)
	}
}

func TestRunHTTPRequestError(t *testing.T) {
	_, err := RunHTTP(HTTPAdapterConfig{URL: "http://127.0.0.1:1/x"}, HTTPAdapterVars{}, nil)
	if err == nil || !strings.Contains(err.Error(), "请求失败") {
		t.Fatalf("应报请求失败，实得 %v", err)
	}
}

func TestRunHTTPBadURL(t *testing.T) {
	_, err := RunHTTP(HTTPAdapterConfig{URL: "http://[::1]:named/x"}, HTTPAdapterVars{}, nil)
	if err == nil || !strings.Contains(err.Error(), "构造请求失败") {
		t.Fatalf("应报构造请求失败，实得 %v", err)
	}
}

func TestRunHTTPCustomClient(t *testing.T) {
	srv, _ := newJSONServer(t, 200, `{"items":[{"used":5,"total":10}]}`)
	res, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL, ItemsPath: "items"}, HTTPAdapterVars{}, &http.Client{})
	if err != nil {
		t.Fatalf("传入自有 client 不应报错: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(res.Items))
	}
}

func TestRunHTTPDefaultTimeoutBranch(t *testing.T) {
	srv, _ := newJSONServer(t, 200, `{}`)
	if _, err := RunHTTP(HTTPAdapterConfig{
		URL:       srv.URL,
		TimeoutMs: 5000,
	}, HTTPAdapterVars{}, nil); err != nil {
		t.Fatalf("带 TimeoutMs 不应报错: %v", err)
	}
	if _, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL}, HTTPAdapterVars{}, nil); err != nil {
		t.Fatalf("默认超时分支不应报错: %v", err)
	}
}

func TestRunHTTPItemsExtractionError(t *testing.T) {
	// 响应是合法 JSON，但 itemsPath 取不到东西 —— 走 extractItems 的错误分支
	srv, _ := newJSONServer(t, 200, `{"a":1}`)
	_, err := RunHTTP(HTTPAdapterConfig{URL: srv.URL, ItemsPath: "nope"}, HTTPAdapterVars{}, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("应报取出失败，实得 %v", err)
	}
}

// brokenBody 读取时立刻报错，用来覆盖"读响应体失败"这条分支。
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("读取中断") }
func (brokenBody) Close() error             { return nil }

type brokenTransport struct{}

func (brokenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       brokenBody{},
		Header:     make(http.Header),
	}, nil
}

func TestRunHTTPBodyReadError(t *testing.T) {
	_, err := RunHTTP(
		HTTPAdapterConfig{URL: "http://example.com/x"},
		HTTPAdapterVars{},
		&http.Client{Transport: brokenTransport{}},
	)
	if err == nil || !strings.Contains(err.Error(), "读取响应失败") {
		t.Fatalf("应报读取响应失败，实得 %v", err)
	}
}
