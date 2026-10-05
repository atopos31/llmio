# 在 Vercel 上跑一个展示型 Demo

这份文档说的是一条**只给展示用**的部署路径：一个 Vercel 项目同时构建 Vite 前端与
Go 后端，产出单个 URL。它不适用于生产——原因见文末。

## 一次性部署

1. 把仓库推到 GitHub，在 Vercel 里 Import。
2. **Framework Preset 选 `Go`**（`vercel.json` 里已写死 `framework: "go"`）。
3. Root Directory 保持仓库根目录（**不要**设成 `webui`）。
4. 环境变量（可选，见下一节）：`TOKEN`。
5. Deploy。

构建命令在 `vercel.json` 里，顺序不是随便定的：

```json
"buildCommand": "cd webui && npx --yes pnpm@10 install --frozen-lockfile && npx --yes pnpm@10 run build && cd .. && CGO_ENABLED=0 go build -trimpath -o server ."
```

前端**必须先构建**：`main.go` 用 `//go:embed webui/dist` 把静态资源编进二进制，
`dist` 不存在时 `go build` 直接失败。

**pnpm 版本必须钉死，不能沿用预装的。** 这里踩过一次坑（首次部署即失败）：

```
WARN  Ignoring not compatible lockfile at /vercel/path0/webui/pnpm-lock.yaml
ERROR Headless installation requires a pnpm-lock.yaml file
```

Vercel 构建镜像**预装了 pnpm，但版本偏旧**，读不了本仓库的
`lockfileVersion: '9.0'`（需要 pnpm ≥9）。原先写的是
`command -v pnpm || npm i -g pnpm@10`——这个 guard 恰恰失效：预装的 pnpm
能被 `command -v` 找到，于是安装分支根本不执行，最后跑的还是那个旧 pnpm。

所以这里用 `npx --yes pnpm@10` **无条件指定**版本，既不看预装的是什么，也不往
全局装（`npx` 按需取用，不留副作用）。仓库 `Dockerfile` 里钉的也是 `pnpm@10`，
两处保持一致。

## 环境变量

| 变量 | 作用 | 不设时 |
|---|---|---|
| `TOKEN` | 控制台与 `/api` 的口令 | **鉴权被整体关闭**，见下 |
| `VERCEL` | 平台自带，用来判定演示模式 | 由平台注入，不要手动设 |
| `LLMIO_DEMO_SEED` | 本地预览演示数据时手动开启 | 不灌演示数据 |
| `LLMIO_DB_PATH` | 覆盖库文件位置 | Vercel 上落到 `/tmp/llmio.db` |
| `LLMIO_SERVER_PORT` | 覆盖监听端口 | 平台注入的 `PORT` 优先 |

### `TOKEN` 不设时会发生什么

`middleware/auth.go` 的第一条分支是"不设置 token，则不进行验证"。所以：

- **不设 `TOKEN`**：任何人打开站点，在登录框里随便填一个值就能进控制台，拥有完整
  读写权限。演示体验最省事（不用把口令发给别人），代价是零保护。
- **设了 `TOKEN`**：必须把口令告诉要看演示的人，否则进不去。

演示数据里的供应商 API Key 全是假的（`sk-demo-*`），库又是临时的，所以"开放演示"
的破坏半径有限——但控制台里的增删改是真实生效的。

## 演示数据

`main.go` 在 `VERCEL` 存在（或显式 `LLMIO_DEMO_SEED`）且**库为空**时调用
`demoSeed`，灌入 3 个供应商、2 个模型、5 条「模型 × 上游」关联、1 个访问密钥和
120 条跨最近 24 小时的请求日志。判据是 `models.IsEmptyDB`，因此重启不会重复灌入。

日志是**裸 SQL** 插入的，因为 `created_at` 必须散在过去 24 小时里，而 GORM 创建时
会自己填当前时刻。两处易错点：

- `prompt_tokens_details` 是 `serializer:json` 列，必须喂 JSON 文本；
- `proxy_time` / `first_chunk_time` / `chunk_time` 按**纳秒**存（`time.Duration` 的
  GORM 约定），不是微秒。

`TPS` 由"上游吐字速率"反推耗时，而不是先定耗时再倒算速率——后者会随随机耗时乱飘
（实测能飘到 496 tok/s），而分析页的 TPS 排行榜是拿它排序的。

## 为什么不能用于生产

- **库在 `/tmp`，实例回收即失忆。** 每个函数实例各自持有一份库，多实例之间还不共享。
- **三个后台调度器形同虚设**（`main.go` 里的日志清理 / 压缩推进 / 空间回收）。
  函数空闲就冻结，多实例各跑各的。
- **存储层优化失去意义**：`service/storage.go`、`log_compress.go` 等约 3,500 行是围绕
  本地 SQLite 文件的 `VACUUM` / `freelist` / 页级迁移写的，在临时库上没有意义。

生产部署仍应走 `Dockerfile` / `docker-compose.yml`，把 `db/` 挂在持久卷上。
