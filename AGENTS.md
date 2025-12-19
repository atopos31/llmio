# Repository Guidelines

## 项目结构与模块组织
- `main.go` 负责启动 HTTP 服务与路由注册；核心业务拆分在 `service/` `handler/` `middleware/` 与 `providers/` ，对应业务服务、请求处理、拦截器和多模型供应商适配。
- `common/` 汇聚通用工具，`models/` 定义 GORM 实体并连接 `db/llmio.db` SQLite 数据库。
- 前端代码位于 `webui/` ，使用 React + TypeScript + Tailwind + Vite；构建产物输出至 `webui/dist/` 并由 Go 服务托管。
- 运维说明存放在 `docs/` ，`balancer/` 封装多供应商权重与请求调度策略。

## 构建、测试与开发命令
- `go run main.go` 本地启动 REST API 与管理后台，默认读取 `TOKEN` 等环境变量。
- `make run` 依次执行 `go fmt` `go mod tidy` `go run .` ，确保代码格式与依赖同步后再启动。
- `go test ./...` 跑通所有 Go 单元测试，目前集中在 `handler/test_test.go` ，每次提交前需保持通过。
- 使用 `go test ./...` 若遇到权限不足，请在当前目录下设置 `GOCACHE=$PWD/.gocache` 后重试，测试结束立刻删除 `.gocache` 目录。
- `cd webui && pnpm install && pnpm run dev` 开启 Vite HMR ，`pnpm run build` 生成生产静态资源。

## 代码风格与命名约定
- Go 代码统一使用 `go fmt` 和 tab 缩进；导出符号使用 UpperCamelCase ，局部变量使用 lowerCamelCase ，JSON 标签使用 snake_case。
- 错误处理遵循 `if err != nil` 模式，返回具备上下文的 `fmt.Errorf` 消息。
- `webui/src/` 中 React 组件采用 PascalCase 文件和组件名，目录建议 kebab-case；提交前执行 `pnpm run lint` 保持 ESLint + TypeScript 规范。

## 测试指南
- Go 测试文件与实现同目录，文件名追加 `_test.go` ，函数命名 `Test<Feature>` 并优先采用表驱动验证路由、限流与供应商切换逻辑。
- 增量功能需补充针对中间件（鉴权、速率限制、流式响应）的覆盖；出现 bug 时先写回归测试再修复。
- 前端改动至少通过 `pnpm run build` 或 `pnpm run preview` 做冒烟验证，并在 PR 中记录手动检查步骤。
- 如果可用 `gopls` MCP，每次代码编写完成后运行一次 `gopls` 检查，确保无诊断错误。

## 提交与 PR 指南
- 沿用 Conventional Commits 前缀，如 `feat:` `fix:` `refactor:` ，必要时追加模块范围（例如 `feat(webui): adjust theme`）。
- 每个提交聚焦单一问题，关联 Issue 或需求编号，可使用中文或英文摘要但需明确意图。
- PR 描述应包含背景、主要变更点、验证命令输出（`go test` `pnpm run build`），前端改动附截图或录屏，并邀请相关维护者审阅。
- 在提交前确认未泄露 API Key、Token 等敏感信息，必要时使用 `.env` 或 Secrets 管理。

## 安全与配置提示
- 供应商凭证放置于环境变量或未纳入版本控制的 `.env` 文件，通过管理后台或迁移脚本写入 `providers` 表。
- 分享访问权限时及时轮换 `TOKEN` ，生产部署通过反向代理时确认 TLS 终端配置正确，并对外暴露端口前启用访问控制。
