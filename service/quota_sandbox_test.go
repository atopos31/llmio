package service

import (
	"os"
	"testing"

	"github.com/atopos31/llmio/quota"
)

// TestMain 让 service 包的测试二进制也能当沙箱子进程被拉起。
//
// RunScript 是 re-exec **当前可执行文件**（os.Executable()）来跑子进程的。
// 在测试里那个可执行文件就是本测试二进制，因此它必须识别隐藏子命令，
// 否则脚本一律以 "exit status 1" 失败——这也是生产 main() 里必须做的事，
// 这里先按同样的约定接上，顺便验证了那条接线是对的。
func TestMain(m *testing.M) {
	for _, a := range os.Args {
		if a == quota.SandboxCommand {
			os.Exit(quota.SandboxMain())
		}
	}
	os.Exit(m.Run())
}
