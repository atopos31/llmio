# LLMIO

中文 | [English](README.md)

LLMIO 是一个基于 Go 的 LLM 负载均衡网关，为你的 LLM 客户端 (openclaw / claude code / codex / gemini cli / cherry studio / open webui ) 提供统一的 REST API、权重调度、可观测性与现代化管理界面，帮助你在一个服务中整合 OpenAI、Anthropic、Gemini 等不同模型能力。

**QQ 群：1083599685**

## 架构图

![LLMIO 架构图](./docs/llmio.svg)

## 功能特性
- **统一 API**：兼容 OpenAI Chat Completions、OpenAI Responses 、Gemini Native 与 Anthropic Messages 格式，支持透传流式与非流式响应。
- **协议互转**：客户端协议与上游协议不一致时自动翻译（OpenAI ⇄ Anthropic，含流式事件），转换边界内的字段要么整体拒绝并换下一家、要么逐条记账，不会静默丢弃。详见[协议互转](#协议互转)。
- **自定义请求头与会话键**：每个「模型 × 上游」关联可配置带 `{{...}}` 占位符的请求头，会话键按请求体、入站会话头、对话根哈希逐级派生并保证非空，因此强制要求会话头的上游（如 opencode）也能直接接入。详见[自定义请求头与会话键](#自定义请求头与会话键)。
- **权重调度**：`balancers/` 提供两种调度策略(根据权重大小随机/根据权重高低优先)，可按工具调用、结构化输出、多模态能力做智能分发。
- **可视化管理后台**：Web UI（React + TypeScript + Tailwind + Vite）覆盖总览、用量分析、快速开始、提供商、模型路由、请求日志、会话对比、配额、密钥与系统配置。
- **配额与余量**：把各家上游形态各异的用量 / 余额接口归一成同一结构，数据源分内置适配器、HTTP 与 JavaScript 沙箱脚本三类。详见[配额与余量](#配额与余量)。
- **峰谷计费**：按时段对输入 / 缓存读 / 输出三档单价乘以系数，条款挂在「模型 × 上游」关联上，全局一份工作日日历并支持节假日同步。详见[峰谷计费](#峰谷计费)。
- **用量分析**：时间序列、多维下钻、延迟分布、错误归类与模型性能五个视图，支持自定义时间范围与多值筛选。详见[用量分析](#用量分析)。
- **请求日志与会话对比**：日志页支持多维多选筛选，单条日志可查看完整请求 / 响应；对比页最多同时比较 6 条日志，按缓存真实的构造顺序做前缀分析。
- **访问密钥**：为不同客户端签发独立密钥，可单独启用 / 停用、设定过期时间、限制可用模型，并选择是否记录请求内容；每个密钥的使用次数与最近使用时间单独统计，日志页与分析页都可按其筛选。
- **日志保留**：「系统配置」页可设置保留天数与定时清理开关（后台每小时检查一次），日志页另有手动清理入口，历史清理记录可查。
- **速率与失败处理**：内建速率限制兜底、按模型可开关的熔断器与提供商连通性检测，保证故障隔离。
- **本地持久化**：通过纯 Go 实现的 SQLite (`db/llmio.db`) 保存配置和调用记录，开箱即用。
- **数据库压缩与空间回收**：请求正文按内容分块后全局去重，历史数据可原地迁移、可暂停续跑、可回滚；已释放的空间可在服务运行期间归还给文件系统，无需停机。本机生产库副本实测 7.06 GiB → 1.47 GiB。详见[数据库压缩与空间回收](#数据库压缩与空间回收)。
- **会话追踪**：在任意请求体中传入 `session_id` 字段（OpenAI SDK 可使用 `extra_body`），网关会将其记录到日志中，支持在管理界面搜索或通过 `GET /api/logs?session_id=` 接口过滤。
- **可观测性**：每次请求均记录 TraceID、延迟分解（代理耗时 / 首包耗时 / 完成耗时）、TPS、Token 用量（输入 / 缓存 / 输出）及可选全量 IO 日志。支持按每百万 Token 单价（人民币 / 美元）计算单次请求费用，在日志详情中与提供商、模型等元数据一并展示。

## 部署

### Docker Compose (推荐)
```yaml
services:
  llmio:
    image: atopos31/llmio:latest
    ports:
      - 7070:7070
    volumes:
      - ./db:/app/db
    environment:
      - GIN_MODE=release
      - TOKEN=<YOUR_TOKEN>
      - TZ=Asia/Shanghai
```
```bash
docker compose up -d
```

### Docker
```bash
docker run -d \
  --name llmio \
  -p 7070:7070 \
  -v $(pwd)/db:/app/db \
  -e GIN_MODE=release \
  -e TOKEN=<YOUR_TOKEN> \
  -e TZ=Asia/Shanghai \
  atopos31/llmio:latest
```

### 本地运行
前往 [releases](https://github.com/atopos31/llmio/releases) 下载对应操作系统及cpu架构的压缩包(版本大于0.5.13)，这里以 linux amd64 为例。
```bash
wget https://github.com/atopos31/llmio/releases/download/v0.5.13/llmio_0.5.13_linux_amd64.tar.gz
```
解压
```bash
tar -xzf ./llmio_0.5.13_linux_amd64.tar.gz
```
启动
```bash
GIN_MODE=release TOKEN=<YOUR_TOKEN> ./llmio
```
运行后会自动在当前目录下创建 `./db/llmio.db` 作为 `sqlite` 持久化数据文件。

## 环境变量

| 变量 | 说明 | 默认值 | 备注 |
|------|------|--------|------|
| `TOKEN` | 控制台登录与 `/openai` `/anthropic` `/gemini` `/v1` 等 API 鉴权凭证 | 无 | 公网访问必填 |
| `GIN_MODE` | 控制 Gin 运行模式 | `debug` | 线上请设置为 `release` 获得最佳性能 |
| `LLMIO_SERVER_PORT` | 服务监听端口 | `7070` | 服务监听端口 |
| `TZ` | 时区设置，用于日志与任务调度 | 宿主机默认值 | 建议在容器环境中显式指定，如 `Asia/Shanghai` |
| `DB_COMPRESS` | 新写入的请求正文是否以压缩形态存储 | `true` | 设为 `false` 只影响新写入的行，历史压缩行仍可正常读取 |
| `DB_VACUUM` | 启动时执行一次 SQLite VACUUM | 不执行 | 设置为 `true` 启用。VACUUM 独占写入，需要约 2 倍库大小的空闲磁盘 |
| `DB_AUTO_VACUUM_REBUILD` | 既有库是否在启动时重建以启用增量回收空间 | `auto` | `auto` 只对空库设置 `auto_vacuum`；`on` 对既有库执行一次 VACUUM 完成转换（分钟级、独占写入、需 2 倍磁盘）；`off` 一概不修改。其他取值按 `auto` 处理 |
| `LLMIO_QUOTA_CONFIG` | 配额配置文件路径 | `./db/quota.config.json` | 只改文件位置；文件内容（含凭据）由控制台读写，不落数据库 |
| `LLMIO_QUOTA_ALLOW_WRITE` | 配额的写操作是否可用 | 允许写入 | 仅当取值恰为 `false`（区分大小写）时进入只读模式：`/api/quota` 下的写端点一律返回 403；试跑不改变任何状态，不受影响 |

> 关于压缩与回收的完整说明、控制台入口与管理接口，见[数据库压缩与空间回收](#数据库压缩与空间回收)。

## 协议互转

客户端协议与上游协议不一致时，网关在中间插一层翻译；两端协议相同时请求原样转发，不经过翻译层。

- **方向自动判定**：方向由「客户端协议」与「本次实际选中的上游类型」决定，逐个候选项判定，不需要为单次请求做配置。候选池按客户端协议构造：OpenAI 客户端可选 `openai` / `openai-res` / `anthropic` 上游，Anthropic 客户端可选 `anthropic` / `openai` 上游；Gemini 与 OpenAI Responses 上游不参与互转。
- **优先匹配相同协议**：模型上的「优先匹配相同协议」开关（默认开启）决定两种协议的上游是否一起参与调度。开启时只要该模型下存在说客户端协议的上游，就只在这些上游中选择，转换仅在没有本协议上游时兜底；关闭后整个候选池按权重参与。
- **转换范围**：请求体、非流式响应与流式事件三处都覆盖，Anthropic 的 `message_start` / `content_block_*` / `message_delta` / `message_stop` 与 OpenAI 的 chunk 序列可互相收发，包含工具调用、结束原因与用量口径的对应。
- **翻不过去的部分不静默丢弃**：整体无法表达时按「拒绝」处理，路由器换下一家；只丢个别字段时按「记账」处理，在请求日志里留下短码。前者如 `n>1`、结构化输出、非零 penalty、logprobs，后者如 `top_k`、`thinking` 与 `reasoning_content`。
- **在哪里看**：请求日志详情页在上游协议与客户端协议不同时显示「上游协议」字段与「协议转换」区块，逐条把短码译成说明；后端另有一条 `protocol bridged` 的告警日志。

跨协议转换时上游请求的 `max_tokens` 会补默认值 8192（Anthropic 侧必填，客户端未给时按此值补，并记 `defaulted_max_tokens`）。完整的逐字段映射、默认值取舍与流式状态机见 [docs/protocol-bridge.md](docs/protocol-bridge.md)，端到端验收见 `e2e/run_matrix.py`。

## 自定义请求头与会话键

- **自定义请求头**：每个「模型 × 上游」关联可以配置若干请求头，值支持 `{{...}}` 占位符：不含 `{{` 的值按字面量发送（既有配置行为不变），含占位符的按本次请求求值。可用占位符为 `{{session}}`、`{{session_id}}`、`{{model}}`、`{{provider_model}}`、`{{trace_id}}`、`{{auth_key_id}}`、`{{uuid}}`；未识别的占位符原样保留，便于暴露拼写错误而不是静默丢值。求值结果为空时由随机串兜底，保证已配置的请求头始终有非空值。
- **会话键派生**：`{{session}}` 按优先级取值并保证非空——请求体 `session_id` → 入站会话头（`x-opencode-session` / `x-session-id` / `session_id`）→ 对话根哈希 → 随机串。对话根哈希以请求体中首条 user 消息为指纹，客户端不作任何配合时，同一对话的各轮请求仍得到相同的值，不同对话得到不同的值。
- **为什么需要它**：部分上游（如 opencode）强制要求每个请求携带会话头，缺失或为空都会直接返回 400；而在自定义请求头里写死一个值，会让该上游下所有客户端、所有会话塌缩成同一个会话，造成上下文串扰与缓存互相覆盖。
- **请求头优先级**：提供商自身配置 > 自定义请求头 > 透传的客户端请求头。「请求头透传」开关决定是否把客户端原始请求头带上去；无论是否透传，鉴权头与 `Accept-Encoding` 都会被剔除（前者避免凭据外泄，后者必须留给 Go 传输层自行协商，否则响应不会被透明解压）。

## 数据库压缩与空间回收

请求日志的体积几乎全部来自 `chat_ios` 表的三列正文（`input`、`of_string`、`of_string_array`）。
编码类客户端每一轮都会把整段对话历史重新发送，因此第 N 轮的请求体几乎逐字节包含第 N−1 轮，
同一段内容会在不同轮次之间重复出现数百次。逐行压缩看不到这一点（每行是独立的压缩窗口），
整行哈希去重也命中不了（字节完全相同的整行占比不到 1%）。有效的办法是**按内容分块后全局去重**：
同一段内容只保留一份。

### 迁移：把历史正文改写成压缩形态

- **分块与去重**：正文按内容定义的边界切成平均 4 KiB 的块（FastCDC 滚动哈希），内容相同的块
  全局只存一份，每行只保留自己那串块编号。块按 256 KiB 组成组，每组一个压缩帧；读取时按组解压，
  并在内存中保留 32 MiB 的组缓存。
- **逐行帧**：`of_string` 与 `of_string_array` 两列不进块表，直接使用逐行压缩帧。
- **无损**：块内容与每行的块编号序列可以逐字节重建原文。帧为 16 字节自描述头（魔数、版本、
  编码器、原长），明文与帧可以混装在同一列；读取只认帧头，判断不确定时一律按明文处理——
  合法的 JSON 正文首字节不可能是 `0x00`，即帧魔数的首字节。
- **迁移方式**：在**原位置**改写历史行，按批提交（默认每批 64 行 / 32 MiB，每批一个事务）。
  运行期间服务不中断：读取不受影响，写入请求最多等待一批的时间。可随时暂停；中断（关闭进程、
  断电）后重新启动会从水位继续，已迁移的部分不会重复处理。新写入的行自 `DB_COMPRESS` 启用起
  即以压缩形态落库，无需等待历史迁移结束。
- **冷静期**：默认不处理最近 60 秒内被写入过的行（`quiesce_sec`），避免读到尚未写入完成的数据。
- **回滚**：可将全部已压缩正文还原为明文。该操作会重置迁移进度，且数据库文件会明显增大，
  之后需要重新完整迁移一次才能回到压缩形态。

### 空间回收：把已释放的页归还给文件系统

SQLite 删除行只是把页放回空闲列表；默认配置（`auto_vacuum=0`）下文件不会缩小。回收需要数据库
处于 `auto_vacuum=INCREMENTAL`（值为 2）：

- **新库**：在建表之前自动设为 INCREMENTAL，无额外代价。
- **既有库**：默认**不做任何修改**。需要转换时显式设置 `DB_AUTO_VACUUM_REBUILD=on`，服务会在
  监听端口之前执行一次 VACUUM（本机 7 GiB 库实测约 1 分钟）。转换期间独占写入，需要约 2 倍库
  大小的空闲磁盘；磁盘不足时跳过并记录原因，不影响服务启动。
- **增量回收**：逐个空闲页归还给文件系统，不修改数据，可以重复执行，也可以中断。单轮最长 90 秒，
  未完成的部分留待下一轮；连续模式下批与批之间暂停 0.1 秒，使写入请求排队等待而不是失败。
  回收期间读取不受影响，写入请求会变慢。
- **两种方式的分工**：空闲空间很大时用 VACUUM（一次完成，但需离线且需 2 倍磁盘）；日常产生的小
  空洞用增量回收（在线，不需要额外磁盘）。

### 控制台

控制台「系统配置」页的**数据库压缩**卡片提供迁移进度与压缩比、数据库文件大小与可回收空间读数，
以及「开始迁移」「暂停」「回滚为明文」「回收空间」四个操作，并可在「调整策略」中配置迁移策略
（是否后台自动推进、每批行数、每批字节数、批间暂停、冷静期），在「回收策略」中配置回收策略
（是否后台自动回收、可回收空间门槛、检查周期）。输入项标题旁的「?」在悬停或键盘聚焦时给出
取值范围与越界行为。

### 管理接口

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/logs/compression` | GET | 迁移状态与进度、迁移策略、回收策略、数据库现状、上次回收结果 |
| `/api/logs/compression/policy` | PUT | 设置迁移策略（批大小、批间暂停、冷静期、是否后台自动推进） |
| `/api/logs/compression/run` | POST | 开始迁移 |
| `/api/logs/compression/pause` | POST | 暂停迁移（在当前批次执行完成后生效） |
| `/api/logs/compression/decompress` | GET | 查询回滚进度 |
| `/api/logs/compression/decompress` | POST | 回滚为明文 |
| `/api/logs/compression/reclaim` | POST | 回收空间。请求体 `{"continuous": true}` 为持续回收直到完成；空请求体为单轮（最长 90 秒） |
| `/api/logs/compression/reclaim/stop` | POST | 停止回收（在当前批次执行完成后生效） |
| `/api/logs/compression/reclaim/policy` | PUT | 设置回收策略（可回收空间门槛、检查周期） |

以上接口均需控制台鉴权（`Authorization: Bearer <TOKEN>`）。参数越界会被收敛到允许区间而不是报错：

| 参数 | 允许范围 | 越界处理 |
|---|---|---|
| 每批行数 `batch_rows` | 1–4096 | 小于 1 时恢复为默认值 64，大于 4096 时取 4096 |
| 每批字节数 `batch_bytes` | 不超过 1 GiB | 小于 1 时恢复为默认值 32 MiB，超过时取 1 GiB |
| 批间暂停 `batch_interval_ms` | 0–5000 毫秒 | 负值取 0 |
| 冷静期 `quiesce_sec` | 0–86400 秒 | 负值取 0 |
| 可回收空间门槛 `min_bytes` | 0–1 TiB | 负值取 0 |
| 检查周期 `check_interval_sec` | 60–86400 秒 | 小于 60 时取 60 |

### 注意事项

- **正文列不可用于 SQL 文本比较**。压缩后的行存的是二进制帧，针对正文的 `LIKE`、`json_extract`
  等查询会失效。读取单条会话 IO 请使用 `GET /api/logs/:id/chat-io`，该接口会透明地还原正文。
- **迁移与回收共用同一把维护锁**，两者不会同时进行；日志定时清理在迁移或回收进行期间会跳过本轮。
- **迁移前请确认存在可用备份**：`db/llmio.db.bak` 会被自动检测，若不存在或小于当前数据库，
  控制台会要求确认后才执行，并在记录中标注「无备份」。
- `DB_COMPRESS=false` 只停止**新写入行**的压缩，历史压缩行仍可正常读取。

压缩比与耗时取决于语料。以一份 12,483 行（正文合计 5.64 GiB）的生产库副本为例，本机实测：迁移
36 秒，配合 VACUUM 后数据库文件 7.06 GiB → 1.47 GiB；增量回收平均约 1,500 页/秒。完整的测量
过程与取舍见 [docs/db-compression-phase0.md](docs/db-compression-phase0.md)（读路径基准）、
[docs/db-compression-phase3.md](docs/db-compression-phase3.md)（块表与去重）、
[docs/db-compression-phase4.md](docs/db-compression-phase4.md)（迁移与调度）、
[docs/db-compression-phase5.md](docs/db-compression-phase5.md)（存储层与增量回收）、
[docs/db-compression-safety.md](docs/db-compression-safety.md)（帧格式与安全边界）。

## 配额与余量

把各家上游形态各异的「用量 / 余额」接口归一成同一种结构：已用、总量、剩余三者给出任意两个即可推算出第三个，
状态由已用百分比与剩余值推导，最紧张的一条上浮为整页摘要。展示文本由服务端渲染后下发。

数据源分三类：

- **内置适配器**：`deepseek`（余额）、`moonshot`（余额）、`scnet`（国家超算 TokenPlan，登录型）、
  `opencode`（套餐，登录型）与 `custom`（通用端点，需自填路径，再用 `itemsPath` 与映射表取数）。
  登录型适配器需要在数据源自己的 `env` 中给出凭据（键名分别为 `SCNET_USER` / `SCNET_PASS`
  与 `OPENCODE_COOKIE` / `OPENCODE_ORG_ID`），这类上游的登录与会话机制不是脚本能复现的，因此写在 Go 侧。
- **HTTP**：纯配置、不写代码，自填地址、方法、查询、请求头、请求体与鉴权方式
  （`bearer` / `header` / `basic` / `none`），支持 `{{apiKey}}`、`{{baseUrl}}`、`{{id}}`、`{{name}}` 占位符。
- **脚本**：一段 JavaScript（goja，以 ES5.1 为基准），在独立子进程中执行，超时默认 30 秒。
  脚本可用的全局只有 `console`、`output()`、该数据源自己的 `env` 白名单，以及显式开启后的 `fetch`；
  没有 `require`、文件系统与子进程。`fetch` 默认关闭，开启后仅允许 http/https，
  并拒绝 loopback 与内网地址、逐跳重定向最多 5 次。脚本引擎没有堆上限，
  隔离依靠进程边界——脚本失控只影响子进程，超时后被杀，网关不受影响。

凭据存放在 `./db/quota.config.json`（可用 `LLMIO_QUOTA_CONFIG` 改路径；文件权限 0600、原子写入），
不落数据库；接口返回的配置一律脱敏，保存时再按掩码还原为已存的真值。

余量结果在服务端按数据源缓存，TTL 取全局刷新周期（下限 10 秒），**只缓存成功的结果**，失败不写缓存。
服务端没有后台定时线程，取数只发生在收到请求时；页面上的自动刷新是另一层（档位 0–600 秒、默认关闭），
页面不可见时不打上游，回到前台立即补一轮。

管理接口（`Authorization: Bearer <TOKEN>`）：

| 路径 | 方法 | 功能 | 写权限 |
|---|---|---|---|
| `/api/quota/config` | GET | 读取配置（凭据脱敏）、内置适配器清单、配置文件路径、是否只读、默认刷新周期与告警阈值 | 不需要 |
| `/api/quota/config` | PUT | 修改全局的刷新周期与告警阈值 | 需要 |
| `/api/quota/sources` | POST | 新增一个数据源 | 需要 |
| `/api/quota/sources` | PUT | 按 id 更新数据源；找不到不会静默变成新增 | 需要 |
| `/api/quota/sources/:id` | DELETE | 删除数据源 | 需要 |
| `/api/quota/run` | POST | 拉取全部启用的数据源；`force=true` 忽略缓存，`ids=a,b` 只跑指定的源 | 不需要 |
| `/api/quota/sources/:id/refresh` | POST | 强制刷新单个数据源 | 不需要 |
| `/api/quota/test` | POST | 试跑一个尚未保存的数据源，不落盘、不写缓存 | 不需要（只读模式下仍开放） |

默认刷新周期 120 秒、告警阈值 80%。控制台「配额」页提供余量面板（数据源卡片、状态徽标、延迟、可折叠的隐藏项）
与数据源编辑器，卡片样式可选进度条 / 用量环 / 条形对比 / 纯文字；显示名与图表样式等展示设置按数据源与条目
分别保存在浏览器本地，不写服务端。

## 峰谷计费

- **条款挂在「模型 × 上游」关联上**：同一时刻 A 家打折、B 家按峰时计价，是上游各自的商务条款，
  因此乘数与其所乘的那套基础单价同行保存；全局只保留一份工作日日历。
- **条款内容**：按时段给出乘数（1 表示不变），可限定星期与工作日 / 休息日；匹配按顺序进行，
  首个命中的时段生效，全不命中按 1 计。
- **费用计算**：请求发起时把乘数同时乘到输入 / 缓存读 / 输出三档单价上，乘过之后的三档**实际价格**
  与命中的时段名随日志落库，因此之后修改条款不会改写历史成本。
- **工作日日历**：在控制台「系统配置」页的卡片中维护，默认 `Asia/Shanghai`、周一至周五；
  可按年份从上游同步节假日与调休（外网取数，取不到时回退到内置数据，两者都不可用时返回 502
  并保持日历原样）。同一年份重复同步是整体替换，幂等。
- 管理接口：`GET|PUT /api/peak-calendar`、`POST /api/peak-calendar/preview`（把请求体给的条款回放到未来若干天，
  默认 7 天，范围 1–31）、`POST /api/peak-calendar/holidays/sync`（同步指定年份，默认当年）。

## 用量分析

- **单端点**：`GET /api/metrics/stats` 一次返回整份切片，避免多张图各自请求造成时间窗漂移；
  `GET /api/metrics/granularities` 给出可用的粒度档位。
- **时间**：粒度 `5m / 15m / 30m / 1h / 2h / 6h / 12h / 1d / 7d`，`auto` 按跨度自动选择（目标约 60 个分桶）；
  范围支持预设（今天 / 昨天 / 最近 24 小时 / 最近 7 天 / 最近 30 天）与自定义，未指定时默认回看 7 天。
- **五个视图**：趋势（请求与 Token）、下钻（模型 / 提供商 / 模型 × 提供商 / 密钥 / 请求名 / User-Agent）、
  延迟（首包分布与分位读数、TPS 与代理耗时、TPS 榜与最慢榜）、错误（按类别归类，含受影响的上游、模型与样本）、
  模型性能（九列可排序，可在模型 / 提供商 / 模型 × 提供商三种维度间切换）。
- **筛选**：提供商、模型、密钥、请求名、User-Agent、状态六个维度；状态与密钥为精确匹配，其余为子串匹配。
  筛选项从同一时间窗单独取一次，不随已选筛选变化。
- **明细上限**：单次最多加载 20 万条日志，命中上限时页面顶部给出截断提示。

## 开发

克隆项目
   ```bash
   git clone https://github.com/atopos31/llmio.git
   cd llmio
   ```
编译前端(需要 pnpm 环境)
   ```bash
   make webui
   ```
运行后端(需要 go 版本 >= 1.26.1)
   ```bash
   TOKEN=<YOUR_TOKEN> make run
   ```
访问入口webui：`http://localhost:7070/`

测试
   ```bash
   go test ./...
   cd webui && pnpm run test:coverage
   python e2e/run_matrix.py --upstream stub
   ```

- `go test ./...` 跑后端单测；前端单测与覆盖率门禁由 `pnpm run test:coverage` 执行，阈值写在 `webui/vitest.config.ts`，低于阈值即失败。
- `e2e/run_matrix.py` 是协议互转的端到端验收车，默认使用本地桩上游，不依赖外部凭据；加 `--upstream opencode` 可对真实上游跑小矩阵（会消耗用量）。`e2e/check_compression.py` 不信任接口自报的数，直接打开库文件核对压缩形态与 `auto_vacuum` 是否真的落在磁盘上。
- 持续集成见 `.github/workflows/test.yml`：每次 push 与 PR 依次执行前端 lint、前端单测与覆盖率门禁、前端构建、`gofmt` 检查、`go vet`、`go test` 与单二进制构建自检。

## API 端点

LLMIO 提供多供应商兼容的 REST API，支持以下端点：

| 供应商 | 端点路径 | 方法 | 功能 | 认证方式 |
|--------|----------|------|------|----------|
| OpenAI | `/openai/v1/models` | GET | 获取可用模型列表 | Bearer Token |
| OpenAI | `/openai/v1/chat/completions` | POST | 创建聊天完成 | Bearer Token |
| OpenAI | `/openai/v1/responses` | POST | 创建响应 | Bearer Token |
| Anthropic | `/anthropic/v1/models` | GET | 获取可用模型列表 | x-api-key |
| Anthropic | `/anthropic/v1/messages` | POST | 创建消息 | x-api-key |
| Anthropic | `/anthropic/v1/messages/count_tokens` | POST | 计算Token数量 | x-api-key |
| Anthropic | `/anthropic/api/event_logging/batch` | POST | 接收 Claude Code 的批量事件上报并确认（不保存内容） | 无鉴权 |
| Gemini | `/gemini/v1beta/models` | GET | 获取可用模型列表 | x-goog-api-key |
| Gemini | `/gemini/v1beta/models/{model}:generateContent` | POST | 生成内容 | x-goog-api-key |
| Gemini | `/gemini/v1beta/models/{model}:streamGenerateContent` | POST | 流式生成内容 | x-goog-api-key |
| 通用 | `/v1/models` | GET | 获取模型列表（兼容） | Bearer Token |
| 通用 | `/v1/chat/completions` | POST | 创建聊天完成（兼容） | Bearer Token |
| 通用 | `/v1/responses` | POST | 创建响应（兼容） | Bearer Token |
| 通用 | `/v1/messages` | POST | 创建消息（兼容） | x-api-key |
| 通用 | `/v1/messages/count_tokens` | POST | 计算Token数量（兼容） | x-api-key |

### 认证方式

LLMIO 根据端点类型使用不同的认证方式：

#### 1. OpenAI 格式端点（Bearer Token）
适用于：`/openai/v1/*` 和 `/v1/*` 中的 OpenAI 兼容端点
```bash
curl -H "Authorization: Bearer YOUR_TOKEN" http://localhost:7070/openai/v1/models
```

#### 2. Anthropic 格式端点（x-api-key）
适用于：`/anthropic/v1/*` 和 `/v1/*` 中的 Anthropic 兼容端点
```bash
curl -H "x-api-key: YOUR_TOKEN" http://localhost:7070/anthropic/v1/messages
```

#### 3. Gemini Native 端点（x-goog-api-key）
适用于：`/gemini/v1beta/*` 中的 Gemini 原生端点
```bash
curl -H "x-goog-api-key: YOUR_TOKEN" http://localhost:7070/gemini/v1beta/models
```

对于cc或者codex, 使用如下环境变量接入鉴权
```bash
export OPENAI_API_KEY=<YOUR_TOKEN>
export ANTHROPIC_API_KEY=<YOUR_TOKEN>
export GEMINI_API_KEY=<YOUR_TOKEN>
```
> **注意**：`/v1/*` 路径为兼容性保留，建议使用新的供应商特定路径。

## 截图

<table>
  <tr>
    <td align="center"><img src="./docs/home.jpeg" alt="系统主页" /><br/><sub><b>系统主页</b> — 请求量、Token 用量与提供商指标总览</sub></td>
    <td align="center"><img src="./docs/with.jpeg" alt="多对一关联" /><br/><sub><b>模型关联</b> — 为同一模型配置多个提供商，支持权重、能力筛选与计费单价</sub></td>
  </tr>
  <tr>
    <td align="center"><img src="./docs/log.jpeg" alt="日志" /><br/><sub><b>请求日志</b> — 按模型、状态、TraceID、Session ID 等多维度检索与筛选</sub></td>
    <td align="center"><img src="./docs/chat-io.png" alt="会话 IO" /><br/><sub><b>会话 IO</b> — 查看单次请求的完整输入输出、延迟分解与 Token 计费明细</sub></td>
  </tr>
</table>

## 许可证

本项目基于 MIT License 发布。

## 星标历史

[![Stargazers over time](https://starchart.cc/atopos31/llmio.svg?variant=adaptive)](https://starchart.cc/atopos31/llmio)
