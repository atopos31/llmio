package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// 沙箱子进程侧：goja 运行时 + 最小宿主 API。
//
// ## 暴露给脚本的全部能力
//
//	console.log / warn / error   收集到日志缓冲（有行数与字节上限）
//	fetch(url, opts)             受限 HTTP。**同步阻塞**，原因见下
//	env                          只读，仅该数据源白名单里的变量
//	output(value)                提交契约值（推荐写法）
//
// 除此之外什么都没有：没有 require / import、没有文件系统、没有子进程、
// 没有 setTimeout、没有 crypto。需要复杂登录流程的供应商应做成 Go 内置适配器
// （见 builtin.go 里 scnet / opencode 的说明）。
//
// ## 为什么 fetch 必须是同步的
//
// goja 没有事件循环，也没有 setTimeout——它的 README 明确说并发执行应由宿主提供。
// 而 async/await 依赖微任务队列被驱动。如果照 Node 的语义提供一个返回 Promise 的
// fetch，脚本里的 await 会永远等不到结果而挂死。所以这里的 fetch 直接阻塞返回
// 结果对象，`.text` / `.json` 是**属性**而不是方法。
//
// 这个取舍必须写进给用户的文档里，否则照着 Node 习惯写脚本的人会踩到。

// sandbox 持有一次脚本执行的运行态。
type sandbox struct {
	vm       *goja.Runtime
	logs     []string
	logBytes int
	capped   bool

	output     goja.Value
	outputSet  bool
	maxOutput  int
	allowFetch bool
	fetchTO    time.Duration
}

// SandboxMain 是子进程入口。主程序在 main() 最前面识别隐藏子命令后调用它。
// 返回值即进程退出码。
func SandboxMain() int {
	return RunSandbox(os.Stdin, os.Stdout)
}

// RunSandbox 是沙箱的实际实现，输入输出显式传入。
//
// 之所以不直接绑 os.Stdin / os.Stdout：那样同进程测试就必须用管道替换标准流，
// 而父进程自己持有写端会导致读端永远等不到 EOF（写测试时踩到过）。
// 显式传 io 之后，测试用 bytes.Buffer 即可，不需要管道也不需要多进程。
func RunSandbox(in io.Reader, out io.Writer) int {
	resp := ScriptResponse{}

	req, err := decodeRequest(in)
	if err != nil {
		resp.Error = err.Error()
		writeResponse(out, resp)
		return 1
	}

	sb := &sandbox{
		logs:       make([]string, 0, 16),
		maxOutput:  req.MaxOutputBytes,
		allowFetch: req.AllowFetch,
		fetchTO:    time.Duration(req.FetchTimeoutMs) * time.Millisecond,
	}
	if sb.fetchTO <= 0 {
		sb.fetchTO = 20 * time.Second
	}
	if sb.maxOutput <= 0 {
		sb.maxOutput = DefaultMaxOutputBytes
	}

	sb.vm = goja.New()
	sb.install(req)

	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultScriptTimeout
	}
	timer := time.AfterFunc(timeout, func() {
		// 只能打断 JS 执行；卡在阻塞宿主调用里时由父进程杀进程兜底
		sb.vm.Interrupt("脚本执行超时")
	})
	defer timer.Stop()

	_, err = sb.vm.RunString(req.Source)
	if err != nil {
		resp.Error = normalizeJSError(err)
		resp.Logs = sb.logs
		resp.Truncated = sb.capped
		writeResponse(out, resp)
		return 1
	}

	if sb.outputSet && sb.output != nil {
		raw, err := json.Marshal(sb.output.Export())
		if err != nil {
			resp.Error = fmt.Sprintf("output(...) 的值无法序列化为 JSON: %v", err)
			resp.Logs = sb.logs
			writeResponse(out, resp)
			return 1
		}
		// 超限时**报错而不是截断**：截断一段 JSON 得到的是非法 JSON，
		// 往下传只会在别处炸出更难懂的错误。宁可在这里说清原因。
		if len(raw) > sb.maxOutput {
			resp.Error = fmt.Sprintf("output(...) 的序列化结果 %d 字节，超过上限 %d 字节。请只提交需要的字段。",
				len(raw), sb.maxOutput)
			resp.Logs = sb.logs
			resp.Truncated = true
			writeResponse(out, resp)
			return 1
		}
		resp.Output = raw
	}

	resp.OK = true
	resp.Logs = sb.logs
	resp.Truncated = resp.Truncated || sb.capped
	writeResponse(out, resp)
	return 0
}

func writeResponse(out io.Writer, resp ScriptResponse) {
	// stdout 只承载这一个 JSON：日志全部走 resp.Logs，
	// 因此脚本自己的打印不会污染协议。
	data, err := json.Marshal(resp)
	if err != nil {
		// 连信封都编码不出来时，退回一个最小可解析的错误信封——
		// 绝不能什么都不写：那会让父进程只看到空 stdout，排查时完全失去线索。
		data = []byte(`{"ok":false,"error":"沙箱响应编码失败"}`)
	}
	// 追加换行时用码点 0x0A而不写转义序列：
	// 这里跨了 bash / python / go 三层，写转义序列会被某一层解析成真实换行。
	data = append(data, 0x0A)
	_, _ = out.Write(data)
}

// normalizeJSError 把 goja 的错误转成对脚本作者有用的文本。
//
// goja 的语法/运行时错误默认带一长段栈信息，直接回传会让界面上的
// "错误"框塞满内部路径。这里保留消息本体与脚本内行号。
func normalizeJSError(err error) string {
	if interrupted, ok := err.(*goja.InterruptedError); ok {
		return fmt.Sprintf("脚本已被终止: %v", interrupted.Value())
	}
	if ex, ok := err.(*goja.Exception); ok {
		return ex.String()
	}
	return err.Error()
}

// install 挂载宿主 API。这是脚本能力的**唯一**来源。
func (s *sandbox) install(req ScriptRequest) {
	vm := s.vm

	console := vm.NewObject()
	_ = console.Set("log", s.logFunc("log"))
	_ = console.Set("warn", s.logFunc("warn"))
	_ = console.Set("error", s.logFunc("error"))
	_ = vm.Set("console", console)

	// output：推荐的提交方式。显式、无需靠"猜哪一行 JSON 是结果"。
	_ = vm.Set("output", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			s.output = goja.Undefined()
			s.outputSet = true
			return goja.Undefined()
		}
		s.output = call.Argument(0)
		s.outputSet = true
		return goja.Undefined()
	})

	// env：只读、只有白名单。不继承父进程环境。
	env := vm.NewObject()
	for k, v := range req.Env {
		_ = env.Set(k, v)
	}
	_ = vm.Set("env", env)

	if s.allowFetch {
		_ = vm.Set("fetch", s.fetchFunc())
	}
}

func (s *sandbox) logFunc(level string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, a := range call.Arguments {
			parts = append(parts, stringifyJSValue(a))
		}
		line := strings.Join(parts, " ")
		if level != "log" {
			line = level + ": " + line
		}
		s.appendLog(line)
		return goja.Undefined()
	}
}

func stringifyJSValue(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return "null"
	}
	exported := v.Export()
	switch t := exported.(type) {
	case string:
		return t
	case map[string]any, []any:
		// 对象/数组按 JSON 输出——脚本作者 console.log 对象时期望看到结构
		if b, err := json.Marshal(t); err == nil {
			return string(b)
		}
	}
	return v.String()
}

func (s *sandbox) appendLog(line string) {
	if s.logBytes >= maxLogBytes || len(s.logs) >= maxLogLines {
		s.capped = true
		return
	}
	s.logs = append(s.logs, line)
	s.logBytes += len(line)
	if s.logBytes >= maxLogBytes || len(s.logs) >= maxLogLines {
		s.capped = true
	}
}

// ---------------------------------------------------------------------------
// fetch
// ---------------------------------------------------------------------------

// errFetchDisabled 在未开启 fetch 时给出明确指引，而不是"fetch is not a function"
// 这种让人摸不着头脑的报错。
const fetchDisabledHint = "fetch 未启用：请在该数据源的配置里显式打开 allowFetch"

// fetchFunc 返回同步的 fetch。
//
// 返回值形状（与 Node 不同，必须文档化）：
//
//	{ status, ok, headers: {..}, text: "原始响应体", json: 解析结果或 null }
//
// text/json 是属性而非方法——同步实现下没有"稍后再读"的概念。
func (s *sandbox) fetchFunc() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		panicOnErr := func(msg string) {
			panic(s.vm.NewTypeError(msg))
		}

		if len(call.Arguments) == 0 {
			panicOnErr("fetch(url) 缺少 url 参数")
		}
		rawURL := call.Argument(0).String()
		parsed, err := url.Parse(rawURL)
		if err != nil {
			panicOnErr("fetch 的 url 无法解析: " + err.Error())
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			panicOnErr("fetch 只允许 http/https，收到: " + parsed.Scheme)
		}

		// SSRF 防护：脚本不应能借网关之手访问内网。
		// 这是"默认零能力"的一部分——不给内网访问能力，而不是给了再想办法拦。
		if err := fetchHostGuard(parsed.Hostname()); err != nil {
			panicOnErr(err.Error())
		}

		method := http.MethodGet
		var (
			body       io.Reader
			reqHeaders = map[string]string{}
		)
		if len(call.Arguments) > 1 {
			if opts, ok := call.Argument(1).Export().(map[string]any); ok {
				if m, ok := opts["method"].(string); ok && m != "" {
					method = strings.ToUpper(m)
				}
				if h, ok := opts["headers"].(map[string]any); ok {
					for k, v := range h {
						reqHeaders[k] = fmt.Sprint(v)
					}
				}
				if b, ok := opts["body"].(string); ok && b != "" {
					body = strings.NewReader(b)
				}
			}
		}

		req, err := http.NewRequest(method, rawURL, body)
		if err != nil {
			panicOnErr("构造请求失败: " + err.Error())
		}
		for k, v := range reqHeaders {
			req.Header.Set(k, v)
		}

		client := newGuardedClient(s.fetchTO)
		res, err := client.Do(req)
		if err != nil {
			panicOnErr("fetch 失败: " + err.Error())
		}
		defer res.Body.Close()

		// 限制响应体积：脚本不该能把任意大的响应读进内存
		raw, err := io.ReadAll(io.LimitReader(res.Body, maxFetchBytes+1))
		if err != nil {
			panicOnErr("读取响应失败: " + err.Error())
		}
		truncated := false
		if int64(len(raw)) > maxFetchBytes {
			raw = raw[:maxFetchBytes]
			truncated = true
		}

		obj := s.vm.NewObject()
		_ = obj.Set("status", res.StatusCode)
		_ = obj.Set("ok", res.StatusCode >= 200 && res.StatusCode < 300)
		_ = obj.Set("text", string(raw))
		_ = obj.Set("truncated", truncated)

		hdr := s.vm.NewObject()
		for k := range res.Header {
			_ = hdr.Set(k, res.Header.Get(k))
		}
		_ = obj.Set("headers", hdr)

		var parsedBody any
		if err := json.Unmarshal(raw, &parsedBody); err == nil {
			_ = obj.Set("json", s.vm.ToValue(parsedBody))
		} else {
			_ = obj.Set("json", goja.Null())
		}

		return obj
	}
}

// maxFetchBytes 单次 fetch 的响应体上限。
//
// 声明为变量而非常量，便于测试把上限调低来覆盖截断路径——
// 真实上限 4MiB 在测试里要造好几 MB 数据，慢且容易在管道上阻塞。
var maxFetchBytes int64 = 4 << 20 // 4MiB

// fetchHostGuard 是 fetch 的主机校验。
//
// 声明为变量是为了让测试能替换它——否则"成功路径"无从测试（真实校验会拦掉
// 一切本机地址，而测试服务器就在本机）。生产行为不受影响。
var fetchHostGuard = guardHost

// newGuardedClient 构造一个带超时、且**每次重定向都重新校验目标**的客户端。
//
// 重定向必须重新校验：只检查首个 URL 的话，一个指向内网的 302 就能绕过防护。
func newGuardedClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("重定向次数过多")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("重定向到非 http/https 被拒绝")
			}
			return fetchHostGuard(req.URL.Hostname())
		},
	}
}

// guardHost 拒绝指向本机与内网的地址。
//
// 按解析后的 IP 判断而不是按主机名：主机名可以解析到内网
// （例如某些域名指向 127.0.0.1），只看名字拦不住。
func guardHost(host string) error {
	if host == "" {
		return fmt.Errorf("fetch 的 url 缺少主机名")
	}
	// 先按字面量拦一层，避免为明显的内网名发起 DNS
	if isBlockedName(host) {
		return fmt.Errorf("fetch 拒绝访问内网地址: %s", host)
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("无法解析主机 %s: %w", host, err)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("fetch 拒绝访问内网地址: %s 解析到 %s", host, ip)
		}
	}
	return nil
}

func isBlockedName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	switch h {
	case "localhost", "localhost.localdomain", "metadata.google.internal",
		"metadata", "instance-data":
		return true
	}
	// 常见的容器/内网后缀
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home.arpa"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast()
}
