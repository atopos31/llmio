# LLMIO 系统托盘功能说明

## 功能概述

LLMIO 现已支持 Windows 系统托盘功能。双击 `llmio-systray.exe` 启动后，程序将在系统托盘显示图标，不会弹出命令行窗口。

## 功能特性

### 1. 系统托盘图标
- 程序启动后会在 Windows 系统托盘（任务栏右下角）显示 LLMIO 图标
- 鼠标悬停在图标上会显示提示文字："LLMIO - AI 模型代理服务"

### 2. 右键菜单
右键点击托盘图标会弹出菜单，包含以下选项：

- **打开管理界面**：在默认浏览器中打开 LLMIO 管理界面
- **退出**：关闭 LLMIO 服务并退出程序

### 3. 双击操作
双击托盘图标也会打开管理界面（与右键菜单中的"打开管理界面"效果相同）

## 编译说明

### 标准编译（带系统托盘，无控制台窗口）
```bash
go build -ldflags="-s -w -H windowsgui -X github.com/atopos31/llmio/consts.Version=v1.0.0" -o llmio-systray.exe .
```

参数说明：
- `-s -w`：压缩可执行文件体积
- `-H windowsgui`：隐藏控制台窗口（Windows GUI 模式）
- `-X`：设置版本信息

### 调试编译（带控制台窗口）
如果需要查看日志输出进行调试，可以去掉 `-H windowsgui` 参数：
```bash
go build -ldflags="-s -w -X github.com/atopos31/llmio/consts.Version=v1.0.0" -o llmio-debug.exe .
```

## 使用方法

1. **启动程序**
   - 双击 `llmio-systray.exe` 即可启动
   - 程序会在后台运行，系统托盘显示图标

2. **打开管理界面**
   - 方式一：双击托盘图标
   - 方式二：右键托盘图标 → 选择"打开管理界面"
   - 浏览器会自动打开 `http://localhost:8787`（默认端口）

3. **退出程序**
   - 右键托盘图标 → 选择"退出"
   - 程序会正常关闭所有服务

## 技术实现

### 项目结构
```
llmio/
├── main.go              # 主程序入口，集成系统托盘
├── systray/             # 系统托盘模块
│   ├── systray.go       # 托盘逻辑实现
│   └── icon.ico         # 托盘图标文件
└── build/
    └── icon.ico         # 应用程序图标
```

### 核心依赖
- `github.com/getlantern/systray`：跨平台系统托盘库（已在 go.mod 中配置）

### 实现要点

1. **HTTP 服务器异步启动**
   - 服务器在独立的 goroutine 中运行
   - 主线程由 systray 阻塞，保持程序运行

2. **图标嵌入**
   - 使用 `//go:embed` 将图标文件嵌入到可执行文件中
   - 无需外部图标文件依赖

3. **浏览器启动**
   - 根据操作系统自动选择合适的命令打开浏览器
   - Windows: `rundll32 url.dll,FileProtocolHandler`
   - Linux: `xdg-open`
   - macOS: `open`

## 注意事项

1. **首次运行**
   - 确保 `llmio.json` 配置文件存在
   - 可以从 `llmio.json.example` 复制并修改

2. **端口配置**
   - 默认端口：8787
   - 可在 `llmio.json` 中修改 `port` 配置

3. **防火墙**
   - 首次运行可能需要允许防火墙权限

4. **日志查看**
   - GUI 模式下无法在控制台查看日志
   - 如需调试，使用带控制台的编译版本

## 发布建议

### Windows 发布包应包含：
1. `llmio-systray.exe` - 主程序（带系统托盘）
2. `llmio.json.example` - 配置文件示例
3. `README.md` - 使用说明
4. `LICENSE` - 许可证文件

### 可选：
- `llmio-debug.exe` - 调试版本（带控制台窗口）
- 数据库文件将自动创建在 `db/llmio.db`

## 后续优化方向

1. **增强功能**
   - [ ] 添加"最小化到托盘"功能
   - [ ] 支持自动启动（开机启动）
   - [ ] 托盘菜单显示服务运行状态
   - [ ] 支持托盘气球通知

2. **跨平台**
   - [ ] 测试 Linux 系统托盘支持
   - [ ] 测试 macOS 菜单栏图标支持

3. **用户体验**
   - [ ] 添加托盘图标动画（表示运行状态）
   - [ ] 支持自定义端口和主机配置
   - [ ] 右键菜单添加"重启服务"选项