package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// 脚本沙箱。
//
// ## 为什么是"自己的子进程 + goja"而不是别的
//
// 三种方案对照（原 dashboard 用的是第一种）：
//
//	                spawn node/python   内进程 goja    自身子进程 + goja（本实现）
//	能力面          全开（含 TOKEN）     默认零          默认零
//	内存隔离        有                  无              有
//	单二进制        需外部运行时        是              是（re-exec 自身）
//	超时可杀干净    有                  只能 Interrupt  有
//
// 内进程 goja 的致命弱点是**没有内存隔离**：goja 不提供堆上限，脚本里
// 一个 while(true) arr.push(...) 会把整个网关拖垮。放进自己的子进程后，
// 最坏情况只是子进程被杀，网关不受影响——同时保住了"单二进制"。
//
// 代价（如实记录）：语言能力退步。只剩 JavaScript，且 goja 以 ES5.1 为基准、
// 无 npm、无 require、无 setTimeout/事件循环。这是 goja 的固有代价，
// 不是形态选择造成的。
//
// ## 安全立场
//
// goja 的 README 并不称自己是安全沙箱，只说自己提供"更好的执行环境控制"。
// 因此这里**不依赖 goja 提供隔离**，而是靠两点：
//  1. 默认零宿主能力——脚本拿到的全局只有 console / fetch / env / output
//  2. 进程边界——内存与 CPU 的失控只影响子进程
//
// 另外：`Runtime.ToValue` 的文档说"任何 Go 值都能传给 JS"，传进去的
// Go 对象其方法在 JS 里可调用。因此边界上**只传原始值**，
// 绝不把活着的 Go 结构体交给脚本。

// SandboxCommand 是子进程模式的隐藏子命令名。
//
// 主程序启动时先检查它，命中则进入沙箱模式并立即返回，
// 不初始化数据库、不监听端口。
const SandboxCommand = "__quota_script_sandbox"

// DefaultScriptTimeout 脚本默认超时。
const DefaultScriptTimeout = 30 * time.Second

// DefaultMaxOutputBytes 是 output(...) 序列化结果的默认上限。
//
// 父子两侧都要用到：父进程用它给未指定的请求补默认值，
// 子进程在收到 0 时也要补——否则上限 0 会把一切输出都判成超限。
// 写成同一个常量正是为了不让两处各写一遍而漂移。
const DefaultMaxOutputBytes = 1 << 20 // 1MiB

// maxLogLines / maxLogBytes 限制回传的日志量，避免脚本刷屏撑爆父进程内存。
const (
	maxLogLines = 2000
	maxLogBytes = 1 << 20 // 1MiB
)

// ScriptRequest 是父进程发给子进程的请求。
type ScriptRequest struct {
	Source string `json:"source"`
	// Env 只含该数据源显式配置的变量。**不继承父进程环境**。
	Env map[string]string `json:"env,omitempty"`
	// TimeoutMs 脚本总超时。
	TimeoutMs int `json:"timeoutMs"`
	// FetchTimeoutMs 单次 fetch 的超时。
	FetchTimeoutMs int `json:"fetchTimeoutMs"`
	// AllowFetch 是否开放 fetch。默认关，按源显式开启。
	AllowFetch bool `json:"allowFetch"`
	// MaxOutputBytes 限制 output 值的序列化体积。
	MaxOutputBytes int `json:"maxOutputBytes"`
}

// ScriptResponse 是子进程回给父进程的结果。
type ScriptResponse struct {
	OK bool `json:"ok"`
	// Output 是 output() 提交的值；未调用时为 null。
	Output json.RawMessage `json:"output,omitempty"`
	Logs   []string        `json:"logs,omitempty"`
	Error  string          `json:"error,omitempty"`
	// Truncated 表示日志或输出被截断。
	Truncated bool `json:"truncated,omitempty"`
}

// ScriptResult 是父进程侧的脚本执行结果。
type ScriptResult struct {
	// Value 是最终拿到的原始值：优先 output()，否则从日志里提取。
	Value any
	// Source 说明值是怎么来的，供排查用。
	Source string
	Logs   []string
	// Truncated 表示日志或输出被截断。
	Truncated bool
	Duration  time.Duration
}

// RunScript 在子进程里执行脚本并取回结果。
//
// 超时由父进程兜底：ctx 到期或计时器触发即杀子进程。
// 子进程内部另有 goja Interrupt，但那只能打断 JS 循环，
// 打断不了阻塞中的宿主调用（例如卡住的 fetch）——所以进程级兜底是必需的。
func RunScript(ctx context.Context, req ScriptRequest) (*ScriptResult, error) {
	if strings.TrimSpace(req.Source) == "" {
		return nil, fmt.Errorf("脚本内容为空")
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = int(DefaultScriptTimeout / time.Millisecond)
	}
	if req.MaxOutputBytes <= 0 {
		req.MaxOutputBytes = DefaultMaxOutputBytes
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("无法定位自身可执行文件: %w", err)
	}

	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(runCtx, exe, SandboxCommand)
	cmd.Stdin = bytes.NewReader(payload)
	// 明确不继承父进程环境：脚本只应看到自己那一份白名单变量。
	// 这里设成空切片而不是 nil，避免 Windows 上回落到继承。
	cmd.Env = []string{}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	if runCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("脚本执行超时（>%s）已终止", timeout)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return interpretSubprocessResult(stdout.Bytes(), stderr.String(), runErr, elapsed)
}

// interpretSubprocessResult 解析子进程的产物，是 RunScript 里唯一与"子进程"解耦的一段。
//
// 抽成纯函数（显式吃 stdout / stderr / 退出错误）的理由和 RunSandbox 一样：
// "子进程在写出响应之前就死了"这类分支无法在真实子进程上确定性地复现
// （连环境变量都传不进去——子进程的环境被刻意清空以保证脚本隔离），
// 做成纯函数后可以直接喂各种产物组合，分支也因此可测。
func interpretSubprocessResult(stdoutRaw []byte, stderrText string, runErr error, elapsed time.Duration) (*ScriptResult, error) {
	// 先解析 stdout：子进程的设计是"即使脚本失败也写一份结构化响应，
	// 再以非零码退出"（退出码只用于让父进程知道失败了）。
	// 若先判退出码，就会把带原因的结构化错误吞成无信息的"异常退出"。
	var resp ScriptResponse
	parsed := json.Unmarshal(bytes.TrimSpace(stdoutRaw), &resp) == nil

	if !parsed {
		// stdout 不是结构化响应，说明子进程在写响应之前就死了
		msg := strings.TrimSpace(stderrText)
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		if msg == "" {
			msg = "沙箱进程未产出任何结果"
		}
		return nil, fmt.Errorf("沙箱进程异常退出: %s", msg)
	}
	if !resp.OK {
		msg := resp.Error
		if msg == "" {
			msg = "脚本执行失败（未提供原因）"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	res := &ScriptResult{Logs: resp.Logs, Truncated: resp.Truncated, Duration: elapsed}

	// 优先显式 output()；没有再退回原来的"从日志里找 JSON"，
	// 以兼容现存脚本的 console.log(JSON.stringify(x)) 写法
	if len(resp.Output) > 0 && string(resp.Output) != "null" {
		var v any
		if err := json.Unmarshal(resp.Output, &v); err == nil {
			res.Value = v
			res.Source = "output()"
			return res, nil
		}
	}
	if v, ok := ExtractFromLogs(resp.Logs); ok {
		res.Value = v
		res.Source = "console 输出"
		return res, nil
	}

	return res, fmt.Errorf("脚本没有产出可识别的余量数据：既未调用 output(...)，" +
		"输出里也找不到 JSON。请用 output(...) 提交结果")
}

func truncateForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	if s == "" {
		return "(空)"
	}
	return s
}

// decodeRequest 子进程侧：从 stdin 读请求。
func decodeRequest(r io.Reader) (ScriptRequest, error) {
	var req ScriptRequest
	data, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return req, err
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, fmt.Errorf("请求解析失败: %w", err)
	}
	return req, nil
}
