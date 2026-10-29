# 开发与测试

本页说明本地开发、测试、构建和新增功能的入口。

## 环境要求

| 工具 | 版本 |
|---|---|
| Go | 1.26.6（以 `go.mod` 为准） |
| Node.js | 20 或更高 |
| pnpm | 10.34.6（以 `webui/package.json` 的 `packageManager` 为准） |
| SQLite | 不需要单独安装。项目使用纯 Go 驱动。 |

## 目录结构

| 目录 | 内容 |
|---|---|
| `main.go` | 启动服务、注册路由、嵌入前端资源。 |
| `handler/` | HTTP 处理与请求参数。 |
| `service/` | 调度、重试、记录、统计、压缩、配额等业务逻辑。 |
| `providers/` | 上游适配器。 |
| `balancers/` | Lottery、Rotor 与熔断。 |
| `bridge/` | OpenAI 与 Anthropic 的协议转换纯函数。 |
| `models/` | GORM 实体、数据库初始化、压缩存储。 |
| `quota/` | 配额数据源、内置适配器、脚本沙箱。 |
| `webui/` | React + TypeScript + Vite 控制台。 |
| `e2e/` | 端到端验收脚本与桩上游。 |
| `docs/` | 设计文档与测量记录。 |

## 本地启动

### 后端

```bash
TOKEN=dev-token make run
```

`make run` 会依次执行：

1. `go fmt ./...`
2. `go mod tidy`
3. 创建 `db` 目录
4. `go run .`

服务监听 `http://localhost:7070`。数据库在 `./db/llmio.db`。

首次运行前必须先构建前端。`main.go` 使用 `//go:embed webui/dist` 嵌入页面资源；`webui/dist` 不在版本库中，缺失时 `go build` / `go run` 会直接失败。先执行一次 `make webui` 生成 `webui/dist`；之后日常改前端时再使用 `pnpm run dev`。

### 前端开发服务器

```bash
cd webui
pnpm install
pnpm run dev
```

Vite 开发服务器会把 `/api` 代理到 `http://localhost:7070`。页面支持热更新。

### 构建前端

```bash
make webui
```

等价于：

```bash
cd webui
pnpm install
pnpm run build
```

产物在 `webui/dist`，会被 Go 的 `//go:embed` 嵌入二进制。

### 构建二进制

```bash
go build -o llmio .
```

Windows 下生成 `llmio.exe`。

## 测试

### Go 单元测试

```bash
go test ./...
```

单个包：

```bash
go test ./balancers/...
```

单个用例：

```bash
go test -run TestWeightedList ./balancers/
```

Go 覆盖率门禁：

```bash
make cover
```

阈值为 70%。低于阈值时命令失败。

如果遇到缓存权限问题：

```bash
GOCACHE=$PWD/.gocache go test ./...
```

测试结束后删除 `.gocache`。

### 前端测试

```bash
cd webui
pnpm run lint
pnpm run test
pnpm run test:coverage
```

`test:coverage` 的阈值写在 `webui/vitest.config.ts`，低于阈值即失败。

### 协议互转端到端测试

```bash
python e2e/run_matrix.py --upstream stub
```

它会在本地启动桩上游与独立的 LLMIO 实例，覆盖：

- 直连与双向互转。
- 流式与非流式。
- 工具调用、图片、多轮工具结果。
- 参数搬移、记账与拒绝。
- 候选池的直连优先与权重行为。
- 上游 400 后换家。

本地桩不需要外部凭据。当前矩阵包含 24 个用例，全部通过时输出 `24/24 通过`。

真实上游小矩阵：

```bash
python e2e/run_matrix.py --upstream opencode
```

会消耗真实用量。

### 其他验收脚本

| 脚本 | 内容 |
|---|---|
| `e2e/check_compression.py` | 直接打开库文件，核对压缩形态与 `auto_vacuum` 是否真正落盘。 |
| `e2e/check_vacuum.py` | 复制库、制造空洞、执行重整，统计期间写入失败与最长等待。 |
| `e2e/backup_check.py` | 备用关联降级：主池失败后备用接管、主池健康时备用零请求、主备全部失败时的错误归因。 |
| `e2e/selfheal_check.py` | 参数自愈的正向与负向用例。 |

这些脚本会写入自己的副本或工作目录，不要指向仓库中的生产库。

## 持续集成

`.github/workflows/test.yml` 在 push 与 PR 时执行：

- 前端 lint。
- 前端单测与覆盖率门禁。
- 前端构建。
- `gofmt` 检查。
- `go vet`。
- `go test`。
- 单二进制构建自检。

其他工作流：

- `cicd.yml`：镜像构建与发布。
- `db-compat.yml`：数据库兼容性测试。
- `releaser.yml`：发布包。

## 新增上游类型

1. 在 `providers/` 实现 `Provider` 接口：
   - `BuildReq`：构造上游请求。
   - `Models`：拉取模型列表。
2. 在 `providers/provider.go` 的 `New` 工厂中注册。
3. 在 `consts/consts.go` 增加类型常量。
4. 在 `service/before.go` 增加请求解析器，提取模型名、能力与会话信息。
5. 在前端提供商表单与模型中增加该类型。
6. 补充单元测试与协议互转矩阵用例。

如果新类型不参与协议互转，确认 `service/bridge.go` 的 `ServableTypes` 与 `TranslatorFor` 不会把它放进可转换集合。

## 新增管理接口

1. 在 `handler/` 添加处理函数。
2. 在 `main.go` 的 `/api` 组注册路由。
3. 在 `webui/src/lib/api.ts` 添加前端调用。
4. 补充处理函数测试与前端页面测试。
5. 如涉及新配置项，在[配置说明](Configuration)中加入键与默认值。

## 数据库变更

- 实体定义在 `models/`。
- 启动时通过 GORM `AutoMigrate` 自动迁移。
- 新增列时注意 SQLite 的 `AutoMigrate` 行为：列类型、可空性、主键变化可能触发表重建。大表上要避免。
- 压缩相关列使用 `type:text` 固定列类型，避免 AutoMigrate 重建大表。
- 新增大表或索引时，先在真实规模的库副本上测量，再提交。

## 提交规范

使用 Conventional Commits：

```text
feat(webui): add quota card
fix(stats): avoid full table scan
docs(protocol): describe max_tokens default
```

要求：

- 一个提交只解决一个问题。
- 提交前确认没有密钥、Token 或请求正文进入版本库。
- 前端改动至少通过 `pnpm run build` 或 `pnpm run preview` 验证。
- 后端改动通过 `go test ./...`。
- PR 描述包含背景、变更点、验证命令与前端截图（如有界面变化）。
