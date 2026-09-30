package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// ---------------------------------------------------------------------------
// JSON 提取
// ---------------------------------------------------------------------------

func TestExtractJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		wantOK   bool
		wantDesc string // 期望结果的形状描述
	}{
		{name: "空串", in: "", wantOK: false},
		{name: "仅空白", in: "   \n  ", wantOK: false},
		{name: "整段是对象", in: `{"a":1}`, wantOK: true, wantDesc: "object"},
		{name: "整段是数组", in: `[1,2]`, wantOK: true, wantDesc: "array"},
		{name: "整段是标量", in: `42`, wantOK: true, wantDesc: "number"},
		{name: "纯文本里没有 JSON", in: `这是一段普通日志，没有括号`, wantOK: false},
		{name: "前后有噪音", in: `[INFO] 开始 {"used":1,"total":2} 结束`, wantOK: true, wantDesc: "object"},
		{name: "括号不配对", in: `{"a":1`, wantOK: false},
		{name: "只有左括号", in: `{`, wantOK: false},
		{name: "fenced json 块", in: "前置说明\n```json\n{\"a\":1}\n```\n后置", wantOK: true, wantDesc: "object"},
		{name: "fenced 无语言标记", in: "```\n{\"a\":1}\n```", wantOK: true, wantDesc: "object"},
		{name: "fenced 内容不是 JSON 时继续找", in: "```\nnot json\n```\n{\"a\":1}", wantOK: true, wantDesc: "object"},
		{name: "fenced 未闭合", in: "```json\n{\"a\":1}", wantOK: true, wantDesc: "object"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, ok := ExtractJSON(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok: 期望 %v，实得 %v（值 %v）", tc.wantOK, ok, v)
			}
			if !ok {
				return
			}
			switch tc.wantDesc {
			case "object":
				if _, isMap := v.(map[string]any); !isMap {
					t.Fatalf("期望对象，实得 %T", v)
				}
			case "array":
				if _, isArr := v.([]any); !isArr {
					t.Fatalf("期望数组，实得 %T", v)
				}
			case "number":
				if _, isNum := v.(float64); !isNum {
					t.Fatalf("期望数字，实得 %T", v)
				}
			}
		})
	}
}

// 字符串里的括号不能被当作结构括号——余量数据的模板字段里很常见。
func TestBalancedEndRespectsStrings(t *testing.T) {
	t.Parallel()

	s := `{"tmpl":"{used} / {total}","n":1}`
	end := balancedEnd(s, 0)
	if end != len(s)-1 {
		t.Fatalf("应当在最后的 } 处结束（下标 %d），实得 %d", len(s)-1, end)
	}
}

func TestBalancedEndEscapedQuote(t *testing.T) {
	t.Parallel()

	// 字符串里出现被转义的引号，随后还有括号
	s := `{"k":"a\"}b","n":2}`
	end := balancedEnd(s, 0)
	if end != len(s)-1 {
		t.Fatalf("转义引号不应提前结束字符串，实得 %d（期望 %d）", end, len(s)-1)
	}
}

func TestBalancedEndNonBracketStart(t *testing.T) {
	t.Parallel()

	if got := balancedEnd("hello", 0); got != -1 {
		t.Fatalf("非括号起点应返回 -1，实得 %d", got)
	}
	// 起点越界
	if got := balancedEnd("", 0); got != -1 {
		t.Fatalf("越界起点应返回 -1，实得 %d", got)
	}
}

func TestBalancedEndNested(t *testing.T) {
	t.Parallel()

	s := `[[1,[2,3]],4]`
	if got := balancedEnd(s, 0); got != len(s)-1 {
		t.Fatalf("嵌套数组应在末尾闭合，实得 %d", got)
	}
}

// 打分决定多个候选里选哪个。形状越像契约分越高。
func TestScoreCandidatePrefersContractShapes(t *testing.T) {
	t.Parallel()

	score := func(s string) int {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("测试数据非法: %v", err)
		}
		return scoreCandidate(v)
	}

	itemsArr := score(`{"items":[{"used":1}]}`)
	dataArr := score(`{"data":[{"used":1}]}`)
	itemsMap := score(`{"items":{"a":1}}`)
	single := score(`{"used":1,"total":2}`)
	bareArr := score(`[1,2,3]`)
	otherObj := score(`{"foo":"bar"}`)
	dataMap := score(`{"data":{"a":1}}`)
	scalar := score(`42`)

	// 契约形状应当压过其它
	if !(itemsArr > dataArr) {
		t.Fatalf("items 数组应比 data 数组分高: %d vs %d", itemsArr, dataArr)
	}
	if !(dataArr > itemsMap) {
		t.Fatalf("data 数组应比 items 对象映射分高: %d vs %d", dataArr, itemsMap)
	}
	if !(itemsMap > single) {
		t.Fatalf("items 对象映射应比单条分高: %d vs %d", itemsMap, single)
	}
	if !(single > bareArr) {
		t.Fatalf("含余量字段的单条应比裸数组分高: %d vs %d", single, bareArr)
	}
	if !(bareArr > otherObj) {
		t.Fatalf("裸数组应比无关对象分高: %d vs %d", bareArr, otherObj)
	}
	if !(otherObj > scalar) {
		t.Fatalf("对象应比标量分高: %d vs %d", otherObj, scalar)
	}
	if !(itemsMap > dataMap) {
		t.Fatalf("items 映射应比 data 映射分高: %d vs %d", itemsMap, dataMap)
	}
}

func TestExtractJSONPicksBestCandidate(t *testing.T) {
	t.Parallel()

	// 同时存在一个无关对象与一个契约对象，应当选契约对象
	in := `{"debug":{"elapsed":12}} 然后真正的结果是 {"items":[{"used":1,"total":2}]}`
	v, ok := ExtractJSON(in)
	if !ok {
		t.Fatal("应当提取到 JSON")
	}
	m, isMap := v.(map[string]any)
	if !isMap {
		t.Fatalf("期望对象，实得 %T", v)
	}
	if _, hasItems := m["items"]; !hasItems {
		t.Fatalf("应当选到含 items 的候选，实得 %v", m)
	}
}

func TestExtractJSONCandidateLimit(t *testing.T) {
	t.Parallel()

	// 大量候选不应导致超长耗时（这里只验证不挂死且能返回）
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, `{"i":%d} `, i)
	}
	if _, ok := ExtractJSON(b.String()); !ok {
		t.Fatal("应当仍能提取出对象")
	}
}

func TestExtractFromLogs(t *testing.T) {
	t.Parallel()

	t.Run("从后往前找", func(t *testing.T) {
		t.Parallel()
		logs := []string{`{"old":true}`, "普通日志", `{"items":[{"used":9,"total":10}]}`}
		v, ok := ExtractFromLogs(logs)
		if !ok {
			t.Fatal("应当提取到")
		}
		// 末尾那条是契约形状，应胜出
		m := v.(map[string]any)
		if _, has := m["items"]; !has {
			t.Fatalf("应选到 items 候选: %v", m)
		}
	})

	t.Run("空日志", func(t *testing.T) {
		t.Parallel()
		if _, ok := ExtractFromLogs(nil); ok {
			t.Fatal("空日志不应有结果")
		}
	})

	t.Run("逐行都不成立时拼接再试", func(t *testing.T) {
		t.Parallel()
		logs := []string{"{", `"items": [{"used": 1, "total": 2}]`, "}"}
		v, ok := ExtractFromLogs(logs)
		if !ok {
			t.Fatal("拼接后应当能识别")
		}
		if _, err := Normalize(v, NormalizeOptions{}); err != nil {
			t.Fatalf("归一失败: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// 子进程侧内部（进程内直接测）
//
// 这些函数正常在子进程里执行，父进程的覆盖率探针看不到它们。
// 但它们是纯逻辑或只依赖 goja，可以直接在进程内构造并测试——
// 否则这一整块会成为覆盖盲区。
// ---------------------------------------------------------------------------

func newTestSandbox(t *testing.T, req ScriptRequest) *sandbox {
	t.Helper()
	sb := &sandbox{
		logs:       make([]string, 0, 8),
		maxOutput:  1 << 20,
		allowFetch: req.AllowFetch,
		fetchTO:    time.Duration(req.FetchTimeoutMs) * time.Millisecond,
	}
	if sb.fetchTO <= 0 {
		sb.fetchTO = 5 * time.Second
	}
	sb.vm = goja.New()
	sb.install(req)
	return sb
}

func TestSandboxInstallExposesGlobals(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{Env: map[string]string{"K": "v"}})

	globals := map[string]string{
		"console": "object",
		"output":  "function",
		"env":     "object",
		"fetch":   "undefined", // 未开启
	}
	for name, want := range globals {
		v, err := sb.vm.RunString("typeof " + name)
		if err != nil {
			t.Fatalf("求值 %s 失败: %v", name, err)
		}
		if v.String() != want {
			t.Fatalf("typeof %s: 期望 %q，实得 %q", name, want, v.String())
		}
	}

	got, err := sb.vm.RunString("env.K")
	if err != nil {
		t.Fatalf("读取 env 失败: %v", err)
	}
	if got.String() != "v" {
		t.Fatalf("env.K 应为 v，实得 %q", got.String())
	}
}

func TestSandboxInstallFetchWhenAllowed(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	v, err := sb.vm.RunString("typeof fetch")
	if err != nil {
		t.Fatalf("求值失败: %v", err)
	}
	if v.String() != "function" {
		t.Fatalf("开启后 fetch 应为 function，实得 %q", v.String())
	}
}

func TestSandboxOutputRecordsValue(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{})
	if _, err := sb.vm.RunString(`output({used: 1})`); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !sb.outputSet {
		t.Fatal("output 应被记录")
	}
	m, ok := sb.output.Export().(map[string]any)
	if !ok || m["used"] != int64(1) {
		t.Fatalf("output 值不符: %v", sb.output.Export())
	}
}

func TestSandboxOutputWithNoArgs(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{})
	if _, err := sb.vm.RunString(`output()`); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !sb.outputSet {
		t.Fatal("无参调用也应被记录")
	}
	if !goja.IsUndefined(sb.output) {
		t.Fatalf("无参时应为 undefined，实得 %v", sb.output)
	}
}

func TestSandboxLogLevels(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{})
	if _, err := sb.vm.RunString(`console.log("a", 1, true); console.warn("b"); console.error("c")`); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if len(sb.logs) != 3 {
		t.Fatalf("期望 3 条日志，实得 %d: %v", len(sb.logs), sb.logs)
	}
	if sb.logs[0] != "a 1 true" {
		t.Fatalf("多参数应用空格连接，实得 %q", sb.logs[0])
	}
	if sb.logs[1] != "warn: b" || sb.logs[2] != "error: c" {
		t.Fatalf("级别前缀不符: %v", sb.logs)
	}
}

func TestStringifyJSValue(t *testing.T) {
	t.Parallel()

	vm := goja.New()
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "undefined", expr: "undefined", want: "null"},
		{name: "null", expr: "null", want: "null"},
		{name: "字符串", expr: `"hi"`, want: "hi"},
		{name: "数字", expr: "42", want: "42"},
		{name: "对象转 JSON", expr: `({a:1})`, want: `{"a":1}`},
		{name: "数组转 JSON", expr: `[1,2]`, want: `[1,2]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := vm.RunString(tc.expr)
			if err != nil {
				t.Fatalf("求值失败: %v", err)
			}
			if got := stringifyJSValue(v); got != tc.want {
				t.Fatalf("期望 %q，实得 %q", tc.want, got)
			}
		})
	}

	// nil 值
	if got := stringifyJSValue(nil); got != "null" {
		t.Fatalf("nil 应为 null，实得 %q", got)
	}
}

func TestSandboxLogCapInProcess(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{})
	for i := 0; i < maxLogLines+50; i++ {
		sb.appendLog(fmt.Sprintf("line-%d", i))
	}
	if len(sb.logs) != maxLogLines {
		t.Fatalf("应恰好截到 %d 行，实得 %d", maxLogLines, len(sb.logs))
	}
	if !sb.capped {
		t.Fatal("应标记截断")
	}
}

func TestSandboxLogByteCap(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{})
	big := strings.Repeat("x", 4096)
	for i := 0; i < 1000 && !sb.capped; i++ {
		sb.appendLog(big)
	}
	if !sb.capped {
		t.Fatal("字节超限后应标记截断")
	}
	if sb.logBytes > maxLogBytes+len(big) {
		t.Fatalf("日志字节数失控: %d", sb.logBytes)
	}
}

func TestNormalizeJSError(t *testing.T) {
	t.Parallel()

	vm := goja.New()

	// 普通异常
	_, err := vm.RunString(`throw new Error("boom")`)
	if err == nil {
		t.Fatal("应当有错误")
	}
	if got := normalizeJSError(err); !strings.Contains(got, "boom") {
		t.Fatalf("异常信息应保留原因，实得 %q", got)
	}

	// 中断
	vm2 := goja.New()
	vm2.Interrupt("停止")
	_, err = vm2.RunString(`1`)
	if err == nil {
		t.Fatal("被中断后应当有错误")
	}
	if got := normalizeJSError(err); !strings.Contains(got, "终止") {
		t.Fatalf("中断信息应可读，实得 %q", got)
	}

	// 非 goja 错误
	if got := normalizeJSError(fmt.Errorf("普通错误")); got != "普通错误" {
		t.Fatalf("普通错误应原样，实得 %q", got)
	}
}

// ---------------------------------------------------------------------------
// fetch
// ---------------------------------------------------------------------------

// callFetch 调用 sandbox 的 fetch 并捕获脚本级 panic（宿主用 panic 抛 JS 异常）。
func callFetch(t *testing.T, sb *sandbox, args ...any) (result goja.Value, panicked any) {
	t.Helper()
	fn := sb.fetchFunc()
	call := goja.FunctionCall{}
	for _, a := range args {
		call.Arguments = append(call.Arguments, sb.vm.ToValue(a))
	}
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
	}()
	return fn(call), nil
}

// 允许访问本机，仅用于测试成功路径。
func allowLocalhost(t *testing.T) {
	t.Helper()
	prev := fetchHostGuard
	fetchHostGuard = func(string) error { return nil }
	t.Cleanup(func() { fetchHostGuard = prev })
}

func TestFetchMissingURLError(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	_, panicked := callFetch(t, sb)
	if panicked == nil {
		t.Fatal("缺少 url 应当抛错")
	}
}

func TestFetchRejectsNonHTTPScheme(t *testing.T) {
	t.Parallel()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://x"} {
		if _, panicked := callFetch(t, sb, u); panicked == nil {
			t.Fatalf("%s 应当被拒绝", u)
		}
	}
}

// 真实守卫必须拦掉内网——这是"默认零能力"的一部分。
func TestFetchBlocksInternalHosts(t *testing.T) {
	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})

	for _, u := range []string{
		"http://127.0.0.1:1/x",
		"http://localhost:1/x",
		"http://169.254.169.254/latest/meta-data/", // 云元数据服务
		"http://10.0.0.1/x",
	} {
		if _, panicked := callFetch(t, sb, u); panicked == nil {
			t.Fatalf("%s 应当被 SSRF 防护拦下", u)
		}
	}
}

func TestFetchSuccess(t *testing.T) {
	allowLocalhost(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"used":1,"total":2}]}`))
	}))
	defer srv.Close()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	v, panicked := callFetch(t, sb, srv.URL)
	if panicked != nil {
		t.Fatalf("意外抛错: %v", panicked)
	}
	obj := v.(*goja.Object)
	if obj.Get("status").ToInteger() != 200 {
		t.Fatalf("status 应为 200，实得 %v", obj.Get("status"))
	}
	if !obj.Get("ok").ToBoolean() {
		t.Fatal("ok 应为 true")
	}
	// text 与 json 是属性而非方法——同步实现下没有"稍后再读"
	if !strings.Contains(obj.Get("text").String(), "items") {
		t.Fatalf("text 应含响应体，实得 %q", obj.Get("text").String())
	}
	j := obj.Get("json").Export()
	m, ok := j.(map[string]any)
	if !ok {
		t.Fatalf("json 应为对象，实得 %T", j)
	}
	if _, has := m["items"]; !has {
		t.Fatalf("json 内容不符: %v", m)
	}
	if obj.Get("truncated").ToBoolean() {
		t.Fatal("小响应不应标记截断")
	}
}

func TestFetchNonJSONBodyLeavesJSONNull(t *testing.T) {
	allowLocalhost(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain text, not json"))
	}))
	defer srv.Close()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	v, panicked := callFetch(t, sb, srv.URL)
	if panicked != nil {
		t.Fatalf("意外抛错: %v", panicked)
	}
	obj := v.(*goja.Object)
	if !goja.IsNull(obj.Get("json")) {
		t.Fatalf("非 JSON 响应时 json 应为 null，实得 %v", obj.Get("json"))
	}
	if obj.Get("text").String() != "plain text, not json" {
		t.Fatalf("text 应保留原文，实得 %q", obj.Get("text").String())
	}
}

func TestFetchPassesMethodHeadersBody(t *testing.T) {
	allowLocalhost(t)

	var gotMethod, gotHeader, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Test")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	_, panicked := callFetch(t, sb, srv.URL, map[string]any{
		"method":  "post",
		"headers": map[string]any{"X-Test": "yes"},
		"body":    `{"q":1}`,
	})
	if panicked != nil {
		t.Fatalf("意外抛错: %v", panicked)
	}
	if gotMethod != "POST" {
		t.Fatalf("method 应被转成大写 POST，实得 %q", gotMethod)
	}
	if gotHeader != "yes" {
		t.Fatalf("header 未透传，实得 %q", gotHeader)
	}
	if gotBody != `{"q":1}` {
		t.Fatalf("body 未透传，实得 %q", gotBody)
	}
}

func TestFetchNon2xxStillReturns(t *testing.T) {
	allowLocalhost(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	v, panicked := callFetch(t, sb, srv.URL)
	if panicked != nil {
		t.Fatalf("非 2xx 不应抛错（交给脚本判断），实得 %v", panicked)
	}
	obj := v.(*goja.Object)
	if obj.Get("status").ToInteger() != 418 {
		t.Fatalf("status 应为 418，实得 %v", obj.Get("status"))
	}
	if obj.Get("ok").ToBoolean() {
		t.Fatal("418 时 ok 应为 false")
	}
}

func TestFetchTruncatesLargeBody(t *testing.T) {
	allowLocalhost(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for i := 0; i < int(maxFetchBytes/(64<<10))+8; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()

	sb := newTestSandbox(t, ScriptRequest{AllowFetch: true})
	v, panicked := callFetch(t, sb, srv.URL)
	if panicked != nil {
		t.Fatalf("意外抛错: %v", panicked)
	}
	obj := v.(*goja.Object)
	if !obj.Get("truncated").ToBoolean() {
		t.Fatal("超大响应应标记截断")
	}
	if int64(len(obj.Get("text").String())) > maxFetchBytes {
		t.Fatalf("响应体应被限制在 %d 字节内", maxFetchBytes)
	}
}

func TestFetchRedirectRevalidatesHost(t *testing.T) {
	// 不并行的原因：这个测试依赖 fetchHostGuard 的真实实现（要拦 127.0.0.1），
	// 而另外两个重定向测试会把该包级变量替换成放行。并行跑时谁先设桩不确定，
	// 会出现"内网未被拒"的假失败。触及这个全局桩的测试都保持串行。
	// 只校验首个 URL 是不够的：指向内网的 302 可以绕过防护。
	// 这里直接测 CheckRedirect 的行为。
	client := newGuardedClient(time.Second)
	req, _ := http.NewRequest("GET", "http://127.0.0.1/x", nil)
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("重定向到内网应被拒绝")
	}

	req2, _ := http.NewRequest("GET", "http://example.com/x", nil)
	prev := fetchHostGuard
	fetchHostGuard = func(string) error { return nil }
	defer func() { fetchHostGuard = prev }()
	if err := client.CheckRedirect(req2, nil); err != nil {
		t.Fatalf("外部地址应放行，实得 %v", err)
	}
}

func TestFetchRedirectTooMany(t *testing.T) {
	client := newGuardedClient(time.Second)
	prev := fetchHostGuard
	fetchHostGuard = func(string) error { return nil }
	defer func() { fetchHostGuard = prev }()

	via := make([]*http.Request, 5)
	for i := range via {
		via[i], _ = http.NewRequest("GET", "http://example.com/x", nil)
	}
	req, _ := http.NewRequest("GET", "http://example.com/x", nil)
	if err := client.CheckRedirect(req, via); err == nil {
		t.Fatal("重定向次数超限应被拒绝")
	}
}

func TestFetchRedirectBadScheme(t *testing.T) {
	client := newGuardedClient(time.Second)
	prev := fetchHostGuard
	fetchHostGuard = func(string) error { return nil }
	defer func() { fetchHostGuard = prev }()

	req, _ := http.NewRequest("GET", "ftp://example.com/x", nil)
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("重定向到非 http/https 应被拒绝")
	}
}

// ---------------------------------------------------------------------------
// SandboxMain（进程内，通过替换 stdin/stdout 捕获协议）
// ---------------------------------------------------------------------------

// runSandboxRaw 用 bytes.Buffer 承载协议，在进程内跑一次沙箱。
//
// 不替换 os.Stdin/os.Stdout：父进程自己持有管道写端时，读端永远等不到 EOF
// ——重构前正是这样死锁的。RunSandbox 与 SandboxMain 走的是同一条路径，
// 协议与退出码完全一致，只差一个 os.Stdin/os.Stdout 的绑定。
func runSandboxRaw(t *testing.T, payload string) (ScriptResponse, int) {
	t.Helper()

	out := &bytes.Buffer{}
	code := RunSandbox(strings.NewReader(payload), out)

	var resp ScriptResponse
	if out.Len() > 0 {
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("沙箱输出不是合法 JSON: %v（原文 %q）", err, out.String())
		}
	}
	return resp, code
}

// runSandboxMain 跑一次 RealPath 的入口——即真正覆盖 SandboxMain 的那个。
//
// 它替换 os.Stdin/os.Stdout，因此必须串行（见文件末尾的 TestCoverageEntryPoints）。
// 承载介质用**临时文件**而不是管道：管道下父进程自己持有写端，读端就永远等不到
// EOF——这正是上一次把测试挂死的原因。文件写完之后读端能正常读到 EOF，无需并发。
func runSandboxMain(t *testing.T, req ScriptRequest) (ScriptResponse, int) {
	t.Helper()

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}

	in, err := os.CreateTemp(t.TempDir(), "sandbox-in-*.json")
	if err != nil {
		t.Fatalf("建输入文件失败: %v", err)
	}
	defer in.Close()
	if _, err := in.Write(payload); err != nil {
		t.Fatalf("写输入失败: %v", err)
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("回绕输入失败: %v", err)
	}
	outF, err := os.CreateTemp(t.TempDir(), "sandbox-out-*.json")
	if err != nil {
		t.Fatalf("建输出文件失败: %v", err)
	}
	defer outF.Close()

	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, outF
	defer func() { os.Stdin, os.Stdout = origIn, origOut }()

	code := SandboxMain()

	if _, err := outF.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("回绕输出失败: %v", err)
	}
	raw, err := io.ReadAll(outF)
	if err != nil {
		t.Fatalf("读输出失败: %v", err)
	}

	var resp ScriptResponse
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(bytes.TrimSpace(raw), &resp); err != nil {
			t.Fatalf("沙箱输出不是合法 JSON: %v（原文 %q）", err, string(raw))
		}
	}
	return resp, code
}

// TestSandboxMainEntry 覆盖 SandboxMain 本体（替换标准流的那两行）。
// 串行：它临时改写 os.Stdin/os.Stdout。
func TestSandboxMainEntry(t *testing.T) {
	resp, code := runSandboxMain(t, ScriptRequest{
		Source:    `console.log("via main"); output({used: 1, total: 2})`,
		TimeoutMs: 5000,
	})
	if code != 0 {
		t.Fatalf("退出码应为 0，实得 %d（error=%s）", code, resp.Error)
	}
	if !resp.OK {
		t.Fatalf("应当成功，实得 error=%s", resp.Error)
	}
	if len(resp.Output) == 0 {
		t.Fatal("应当带回 output")
	}
	if len(resp.Logs) != 1 || resp.Logs[0] != "via main" {
		t.Fatalf("日志不符: %v", resp.Logs)
	}
}

func TestSandboxMainSuccess(t *testing.T) {
	resp, code := runSandboxMain(t, ScriptRequest{
		Source:    `console.log("hello"); output({used: 5, total: 10})`,
		TimeoutMs: 5000,
	})
	if code != 0 {
		t.Fatalf("退出码应为 0，实得 %d（error=%s）", code, resp.Error)
	}
	if !resp.OK {
		t.Fatalf("应当成功，实得 error=%s", resp.Error)
	}
	if len(resp.Output) == 0 {
		t.Fatal("应当带回 output")
	}
	if len(resp.Logs) != 1 || resp.Logs[0] != "hello" {
		t.Fatalf("日志不符: %v", resp.Logs)
	}
}

func TestSandboxMainScriptError(t *testing.T) {
	resp, code := runSandboxMain(t, ScriptRequest{
		Source:    `throw new Error("业务失败")`,
		TimeoutMs: 5000,
	})
	if code == 0 {
		t.Fatal("脚本失败时退出码应非 0")
	}
	if resp.OK {
		t.Fatal("不应当标记成功")
	}
	if !strings.Contains(resp.Error, "业务失败") {
		t.Fatalf("应带回脚本给出的原因，实得 %q", resp.Error)
	}
}

func TestSandboxMainNoOutput(t *testing.T) {
	resp, code := runSandboxMain(t, ScriptRequest{Source: `1 + 1`, TimeoutMs: 5000})

	// 脚本本身没报错，子进程按成功返回；"没有产出"由父进程判定
	if code != 0 {
		t.Fatalf("脚本无错时退出码应为 0，实得 %d", code)
	}
	if !resp.OK {
		t.Fatalf("应当标记成功，实得 error=%s", resp.Error)
	}
	if len(resp.Output) != 0 {
		t.Fatalf("未调用 output 时不应带回 output，实得 %s", resp.Output)
	}
}

func TestSandboxMainBadRequest(t *testing.T) {
	resp, code := runSandboxRaw(t, "{not json")

	if code == 0 {
		t.Fatal("请求非法时退出码应非 0")
	}
	if resp.Error == "" {
		t.Fatal("应带回错误原因")
	}
}

func TestSandboxMainOutputNotSerializable(t *testing.T) {
	// 含循环引用的值无法序列化为 JSON
	resp, code := runSandboxMain(t, ScriptRequest{
		Source: `
			var a = {};
			a.self = a;
			output(a);
		`,
		TimeoutMs: 5000,
	})
	if code == 0 {
		t.Fatal("无法序列化时退出码应非 0")
	}
	if !strings.Contains(resp.Error, "无法序列化") {
		t.Fatalf("错误信息应说明原因，实得 %q", resp.Error)
	}
}

func TestSandboxMainTimeout(t *testing.T) {
	resp, code := runSandboxMain(t, ScriptRequest{
		Source:    `while(true){}`,
		TimeoutMs: 500,
	})
	// 被 Interrupt 打断后脚本报错退出
	if code == 0 {
		t.Fatal("超时后退出码应非 0")
	}
	if resp.Error == "" {
		t.Fatal("应带回终止原因")
	}
}
