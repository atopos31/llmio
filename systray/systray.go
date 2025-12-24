package systray

import (
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"

	_ "embed"

	"github.com/getlantern/systray"
)

//go:embed icon.ico
var iconData []byte

var (
	serverURL string
	onExit    func()
)

// Run 启动系统托盘
func Run(url string, exitFunc func()) {
	serverURL = url
	onExit = exitFunc
	systray.Run(onReady, onExit)
}

// onReady 托盘初始化
func onReady() {
	systray.SetIcon(iconData)
	systray.SetTitle("LLMIO")
	systray.SetTooltip("LLMIO - AI 模型代理服务")

	// 打开管理界面菜单项
	mOpen := systray.AddMenuItem("打开管理界面", "在浏览器中打开管理界面")
	mOpen.SetIcon(iconData)

	systray.AddSeparator()

	// 退出菜单项
	mQuit := systray.AddMenuItem("退出", "退出 LLMIO")

	// 处理菜单点击事件
	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				openBrowser(serverURL)
			case <-mQuit.ClickedCh:
				slog.Info("用户从系统托盘退出应用")
				systray.Quit()
				return
			}
		}
	}()
}

// openBrowser 打开浏览器
func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		slog.Error("无法打开浏览器", "error", err)
	} else {
		slog.Info("已在浏览器中打开管理界面", "url", url)
	}
}