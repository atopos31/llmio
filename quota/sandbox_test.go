package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 子进程端到端
//
// 这些用例会真的把一个进程拉起来跑脚本，因此需要测试二进制自身能被 re-exec。
// Go 的测试二进制就是当前可执行文件，而 SandboxCommand 分支由 TestMain 识别，
// 所以 RunScript 里的 os.Executable() 会拿到测试二进制本身。
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	// 这个检查必须在沙箱分支**之前**：覆盖"子进程在写出结构化响应之前就死了"时，
	// 若把检查放在测试函数里，子进程会先进沙箱分支，永远走不到。
	if os.Getenv("LLMIO_TEST_SANDBOX_DIE") == "1" {
		os.Exit(3)
	}
	// 测试二进制被当作沙箱子进程拉起时，进入沙箱模式
	for _, a := range os.Args {
		if a == SandboxCommand {
			os.Exit(SandboxMain())
		}
	}
	os.Exit(m.Run())
}

func runScript(t *testing.T, source string, opts ...func(*ScriptRequest)) (*ScriptResult, error) {
	t.Helper()
	req := ScriptRequest{Source: source, TimeoutMs: 10000, AllowFetch: false}
	for _, o := range opts {
		o(&req)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return RunScript(ctx, req)
}

func TestRunScriptOutputExplicit(t *testing.T) {
	res, err := runScript(t, `output({used: 3, total: 10})`)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if res.Source != "output()" {
		t.Fatalf("应当来自 output()，实得 %q", res.Source)
	}
	items, err := Normalize(res.Value, NormalizeOptions{})
	if err != nil {
		t.Fatalf("归一失败: %v", err)
	}
	approx(t, items[0].Used, 3, "used")
	approx(t, items[0].Total, 10, "total")
}

// 兼容现存脚本的写法：靠 console.log 输出 JSON，由父进程提取。
func TestRunScriptOutputFromConsoleLog(t *testing.T) {
	res, err := runScript(t, `
		console.log("正在查询...");
		console.log(JSON.stringify({items: [{used: 1, total: 2}]}));
	`)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if res.Source != "console 输出" {
		t.Fatalf("应当来自 console 输出，实得 %q", res.Source)
	}
	items, err := Normalize(res.Value, NormalizeOptions{})
	if err != nil {
		t.Fatalf("归一失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("期望 1 条，实得 %d", len(items))
	}
}

func TestRunScriptLogsCaptured(t *testing.T) {
	res, err := runScript(t, `console.log("a"); console.warn("b"); console.error("c"); output(1)`)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	joined := strings.Join(res.Logs, "|")
	if !strings.Contains(joined, "a") || !strings.Contains(joined, "warn: b") || !strings.Contains(joined, "error: c") {
		t.Fatalf("日志未按级别捕获: %v", res.Logs)
	}
}

func TestRunScriptNoOutput(t *testing.T) {
	_, err := runScript(t, `console.log("只是打印了一句话")`)
	if err == nil {
		t.Fatal("没有任何可识别数据时应当报错")
	}
	if !strings.Contains(err.Error(), "output(") {
		t.Fatalf("错误信息应指引使用 output()，实得: %v", err)
	}
}

func TestRunScriptSyntaxError(t *testing.T) {
	_, err := runScript(t, `this is not javascript`)
	if err == nil {
		t.Fatal("语法错误应当报错")
	}
}

func TestRunScriptRuntimeError(t *testing.T) {
	_, err := runScript(t, `output(undefinedVariable.foo)`)
	if err == nil {
		t.Fatal("运行时错误应当报错")
	}
}

func TestRunScriptThrownError(t *testing.T) {
	_, err := runScript(t, `throw new Error("业务失败")`)
	if err == nil {
		t.Fatal("脚本主动抛出时应当报错")
	}
	if !strings.Contains(err.Error(), "业务失败") {
		t.Fatalf("错误信息应包含脚本给出的原因，实得: %v", err)
	}
}

func TestRunScriptEmptySource(t *testing.T) {
	if _, err := runScript(t, "   "); err == nil {
		t.Fatal("空脚本应当报错")
	}
}

// 超时：无限循环必须被终止，且是**进程级**终止。
func TestRunScriptTimeout(t *testing.T) {
	start := time.Now()
	_, err := runScript(t, `while(true){}`, func(r *ScriptRequest) { r.TimeoutMs = 700 })
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("死循环应当超时终止")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误信息应说明超时，实得: %v", err)
	}
	// 应当及时返回，而不是把整个测试拖住
	if elapsed > 15*time.Second {
		t.Fatalf("超时终止耗时过长: %v", elapsed)
	}
}

func TestRunScriptContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunScript(ctx, ScriptRequest{Source: `output(1)`, TimeoutMs: 5000})
	if err == nil {
		t.Fatal("ctx 已取消时应当报错")
	}
}

// env 只含白名单，且**不继承父进程环境**。
func TestRunScriptEnvIsIsolated(t *testing.T) {
	// 在父进程环境里放一个变量，脚本不应看到它
	t.Setenv("SECRET_FROM_PARENT", "should-not-leak")

	src := `
		output({
			provided: env.PROVIDED_KEY || null,
			leaked: env.SECRET_FROM_PARENT || null,
			process: typeof process,
			require: typeof require,
		})
	`
	res, err := runScript(t, src, func(r *ScriptRequest) {
		r.Env = map[string]string{"PROVIDED_KEY": "hello"}
	})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	got, _ := res.Value.(map[string]any)
	if got["provided"] != "hello" {
		t.Fatalf("白名单变量应可见，实得 %v", got["provided"])
	}
	if got["leaked"] != nil {
		t.Fatalf("父进程环境不应泄漏，实得 %v", got["leaked"])
	}
	if got["process"] != "undefined" {
		t.Fatalf("不应存在 process，实得 %v", got["process"])
	}
	if got["require"] != "undefined" {
		t.Fatalf("不应存在 require，实得 %v", got["require"])
	}
}

// 默认零能力：文件系统、子进程、定时器一律不可用。
func TestRunScriptNoDangerousGlobals(t *testing.T) {
	src := `output({
		fs: typeof fs,
		child_process: typeof require === 'function' ? typeof require('child_process') : 'no-require',
		setTimeout: typeof setTimeout,
		setInterval: typeof setInterval,
		XMLHttpRequest: typeof XMLHttpRequest,
		globalThis_process: typeof globalThis.process,
	})`
	res, err := runScript(t, src)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	got, _ := res.Value.(map[string]any)
	for k, v := range got {
		if v != "undefined" && v != "no-require" {
			t.Fatalf("%s 不应当存在，实得 %v", k, v)
		}
	}
}

// 未开启 fetch 时，fetch 根本不存在（而不是给出误导性的报错）。
func TestRunScriptFetchAbsentByDefault(t *testing.T) {
	res, err := runScript(t, `output(typeof fetch)`)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if res.Value != "undefined" {
		t.Fatalf("默认不应提供 fetch，实得 %v", res.Value)
	}
}

func TestRunScriptLogCap(t *testing.T) {
	res, err := runScript(t, `for (var i = 0; i < 5000; i++) { console.log("line-" + i) }; output(1)`)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if len(res.Logs) > maxLogLines {
		t.Fatalf("日志行数应被限制在 %d 以内，实得 %d", maxLogLines, len(res.Logs))
	}
	if !res.Truncated {
		t.Fatal("超限时应标记截断")
	}
}

// output 超限时**明确报错**，而不是截断。
// 截断一段 JSON 得到的是非法 JSON，往下传只会在别处炸出更难懂的错误。
func TestRunScriptOutputSizeCap(t *testing.T) {
	src := `
		var big = [];
		for (var i = 0; i < 20000; i++) { big.push({label: "这是用于填充体积的一段文本-" + i, value: i}); }
		output({items: big});
	`
	_, err := runScript(t, src, func(r *ScriptRequest) { r.MaxOutputBytes = 1024 })
	if err == nil {
		t.Fatal("输出超过上限时应当报错")
	}
	if !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("错误信息应说明超限，实得: %v", err)
	}
}

// 脚本把 JSON 分多次打印时，拼起来仍应能识别。
func TestRunScriptMultilineJSON(t *testing.T) {
	res, err := runScript(t, `
		console.log("{");
		console.log('"items": [{"used": 1, "total": 2}]');
		console.log("}");
	`)
	if err != nil {
		// 逐行都不成立时会拼起来重试；若实现正确这里不该报错
		t.Fatalf("分多次打印的 JSON 应当被识别: %v", err)
	}
	if _, err := Normalize(res.Value, NormalizeOptions{}); err != nil {
		t.Fatalf("归一失败: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SSRF 防护（不经网络，直接测判定函数）
// ---------------------------------------------------------------------------

func TestIsBlockedName(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"localhost", "LOCALHOST", "localhost.", "localhost.localdomain",
		"metadata.google.internal", "metadata", "instance-data",
		"foo.localhost", "svc.local", "api.internal", "printer.lan", "x.home.arpa",
	}
	for _, h := range blocked {
		if !isBlockedName(h) {
			t.Fatalf("%q 应被拦截", h)
		}
	}

	allowed := []string{"api.deepseek.com", "example.com", "cdn.example.org", "127.0.0.1"}
	for _, h := range allowed {
		if isBlockedName(h) {
			t.Fatalf("%q 不应被名字规则拦截（IP 由另一层规则处理）", h)
		}
	}
}

func TestGuardHostRejectsLocalhost(t *testing.T) {
	t.Parallel()

	for _, h := range []string{"localhost", "127.0.0.1", "::1", "0.0.0.0"} {
		if err := guardHost(h); err == nil {
			t.Fatalf("%q 应被拒绝", h)
		}
	}
	if err := guardHost(""); err == nil {
		t.Fatal("空主机名应被拒绝")
	}
}

func TestIsBlockedIP(t *testing.T) {
	t.Parallel()

	blocked := []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "169.254.1.1", "::1", "fe80::1", "0.0.0.0", "224.0.0.1"}
	for _, s := range blocked {
		ip := parseIP(t, s)
		if !isBlockedIP(ip) {
			t.Fatalf("%s 应被拦截", s)
		}
	}

	allowed := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, s := range allowed {
		ip := parseIP(t, s)
		if isBlockedIP(ip) {
			t.Fatalf("%s 不应被拦截", s)
		}
	}
}

func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("测试数据 %q 不是合法 IP", s)
	}
	return ip
}

// ---------------------------------------------------------------------------
// 协议解析
// ---------------------------------------------------------------------------

func TestDecodeRequest(t *testing.T) {
	t.Parallel()

	body := `{"source":"output(1)","env":{"K":"v"},"timeoutMs":5000,"allowFetch":true}`
	req, err := decodeRequest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if req.Source != "output(1)" || req.Env["K"] != "v" || req.TimeoutMs != 5000 || !req.AllowFetch {
		t.Fatalf("解析结果不符: %+v", req)
	}
}

func TestDecodeRequestInvalid(t *testing.T) {
	t.Parallel()

	if _, err := decodeRequest(strings.NewReader("{not json")); err == nil {
		t.Fatal("非法 JSON 应当报错")
	}
}

func TestScriptResponseRoundTrip(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	resp := ScriptResponse{OK: true, Output: json.RawMessage(`{"a":1}`), Logs: []string{"x"}, Truncated: true}
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var back ScriptResponse
	if err := json.Unmarshal([]byte(buf.String()), &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !back.OK || string(back.Output) != `{"a":1}` || len(back.Logs) != 1 || !back.Truncated {
		t.Fatalf("往返后不一致: %+v", back)
	}
}

func TestTruncateForError(t *testing.T) {
	t.Parallel()

	if got := truncateForError("   "); got != "(空)" {
		t.Fatalf("空内容应显示占位，实得 %q", got)
	}
	if got := truncateForError("short"); got != "short" {
		t.Fatalf("短内容原样，实得 %q", got)
	}
	long := strings.Repeat("x", 500)
	if got := truncateForError(long); len([]rune(got)) != 301 {
		t.Fatalf("长内容应截断，实得长度 %d", len([]rune(got)))
	}
}

// ---------------------------------------------------------------------------
// 父进程侧的兜底分支
// ---------------------------------------------------------------------------

// TestInterpretSubprocessResult 覆盖父进程侧的"子进程异常"分支。
//
// 走纯函数而不是真拉子进程：子进程的环境被刻意清空（cmd.Env = []string{}，
// 保证脚本隔离），连测试用的环境变量都传不进去，这些分支在真进程上无法确定性复现。
func TestInterpretSubprocessResult(t *testing.T) {
	t.Run("stdout 非结构化且 stderr 有内容", func(t *testing.T) {
		_, err := interpretSubprocessResult([]byte("垃圾输出"), "panic: 起不来了", errors.New("exit 2"), time.Second)
		if err == nil || !strings.Contains(err.Error(), "panic: 起不来了") {
			t.Fatalf("应带出 stderr，实得 %v", err)
		}
		if !strings.Contains(err.Error(), "异常退出") {
			t.Fatalf("应报异常退出，实得 %v", err)
		}
	})

	t.Run("stdout 与 stderr 都空但退出码非零", func(t *testing.T) {
		_, err := interpretSubprocessResult(nil, "  ", errors.New("exit status 3"), time.Second)
		if err == nil || !strings.Contains(err.Error(), "exit status 3") {
			t.Fatalf("应带出退出错误，实得 %v", err)
		}
	})

	t.Run("全空且无退出错误", func(t *testing.T) {
		_, err := interpretSubprocessResult(nil, "", nil, time.Second)
		if err == nil || !strings.Contains(err.Error(), "未产出任何结果") {
			t.Fatalf("应报未产出结果，实得 %v", err)
		}
	})

	t.Run("结构化响应里 ok=false 且无原因", func(t *testing.T) {
		_, err := interpretSubprocessResult([]byte(`{"ok":false}`), "", nil, time.Second)
		if err == nil || !strings.Contains(err.Error(), "未提供原因") {
			t.Fatalf("应报未提供原因，实得 %v", err)
		}
	})

	t.Run("ok=false 带原因", func(t *testing.T) {
		_, err := interpretSubprocessResult([]byte(`{"ok":false,"error":"业务失败"}`), "", nil, time.Second)
		if err == nil || err.Error() != "业务失败" {
			t.Fatalf("应原样带出原因，实得 %v", err)
		}
	})

	t.Run("stdout 前后有空白也能解析", func(t *testing.T) {
		// JSON 前后带空白；用反引号原始字符串，避免多层转义写错
		res, err := interpretSubprocessResult([]byte(`  
{"ok":true,"output":{"used":1}}  `), "", nil, 2*time.Second)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if res.Source != "output()" || res.Duration != 2*time.Second {
			t.Fatalf("结果不符: %#v", res)
		}
	})

	t.Run("未调用 output 时退回日志解析", func(t *testing.T) {
		// 用 Marshal 构造信封，避免手写嵌套 JSON 时的转义错误
		raw := mustMarshalResp(t, ScriptResponse{OK: true, Logs: []string{`{"used":7,"total":10}`}})
		res, err := interpretSubprocessResult(raw, "", nil, time.Second)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if res.Source != "console 输出" {
			t.Fatalf("应退回日志解析，实得 %q", res.Source)
		}
		if v, ok := res.Value.(map[string]any); !ok || v["used"] != float64(7) {
			t.Fatalf("解析出的值不符: %#v", res.Value)
		}
	})

	t.Run("output 为 JSON null 时退回日志解析", func(t *testing.T) {
		raw := mustMarshalResp(t, ScriptResponse{
			OK: true, Output: json.RawMessage("null"), Logs: []string{`{"used":1}`},
		})
		res, err := interpretSubprocessResult(raw, "", nil, time.Second)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if res.Source != "console 输出" {
			t.Fatalf("应退回日志解析，实得 %q", res.Source)
		}
	})

	t.Run("既无 output 日志里也没有 JSON", func(t *testing.T) {
		raw := mustMarshalResp(t, ScriptResponse{OK: true, Logs: []string{"只是普通日志"}})
		_, err := interpretSubprocessResult(raw, "", nil, time.Second)
		if err == nil || !strings.Contains(err.Error(), "没有产出可识别的余量数据") {
			t.Fatalf("应报无可用数据，实得 %v", err)
		}
	})
}

// mustMarshalResp 把响应信封序列化成子进程会产出的字节。
func mustMarshalResp(t *testing.T, resp ScriptResponse) []byte {
	t.Helper()
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("构造信封失败: %v", err)
	}
	return raw
}

func TestRunScriptSubprocessDiedWithoutResponse(t *testing.T) {
	t.Skip("由 TestInterpretSubprocessResult 直接覆盖该分支；真子进程的环境被清空，无法注入")
}

// TestRunScriptParentContextCancelled 覆盖父进程 ctx 先于子进程结束的情况。
func TestRunScriptParentContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立刻取消
	_, err := RunScript(ctx, ScriptRequest{Source: `output({used:1,total:2})`, TimeoutMs: 5000})
	if err == nil {
		t.Fatal("父进程 ctx 已取消时应报错")
	}
}

// TestRunScriptFailureWithoutReason 覆盖「脚本失败但没给出原因」。
// 造一个只写空响应的场景不容易，这里验证正常失败路径的错误文本足够具体。
func TestRunScriptFailureHasReason(t *testing.T) {
	_, err := runScript(t, `throw new Error("boom")`)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("失败原因应透出，实得 %v", err)
	}
}
