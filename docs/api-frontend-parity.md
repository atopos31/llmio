# 前后端功能对照表

这份表回答两个问题：**某个后端能力有没有前端入口**，以及**某个前端页面依赖哪些后端能力**。它不描述请求参数与响应结构（那些在各 handler 与 `webui/src/lib/*.ts` 的类型注释里）。

怎么用：

- 想加新功能，先在下表找对应端点，看"前端调用者"一栏是"无"还是有页面——有页面就改页面，没有就要连入口一起做。
- 想知道某个端点删了会不会出事，看"前端调用者"一栏是否为空。
- 状态列只有五档：**已接通** / **部分接通**（并说明缺什么）/ **无入口** / **仅 SDK** / **已废弃**。

整理时点：**截至本次整理时的工作区状态**（当前分支 `feat/console-iter3`）。所有 `file:line` 均为当时行号；`webui/src/lib/api.ts` 因本次整理删掉了两个死包装，该文件的行号已按删除后的状态更新，其余文件的引用随峰谷拆分的定稿一并更新（峰谷四个端点在第三轮改名为 `/api/peak-calendar/*`，条款那半并入「模型 × 上游」关联）。表格不会自动跟随代码变动，改动相关文件时请顺手更新受影响的行。

---

## ① 一页速览：管理侧端点（`/api`，middleware.Auth(token)）

| 后端端点 | 方法 | 处理函数 | 前端调用者（file:line） | 状态 |
|---|---|---|---|---|
| `/api/metrics/stats` | GET | `handler.Stats`（handler/stats.go:29） | webui/src/routes/home.tsx:53；webui/src/routes/analytics.tsx:110,111 | 已接通 |
| `/api/metrics/granularities` | GET | `handler.StatsGranularities`（handler/stats.go:45） | webui/src/routes/analytics.tsx:135 | 已接通 |
| `/api/providers/template` | GET | `handler.GetProviderTemplates` | webui/src/routes/providers.tsx:116；webui/src/routes/logs.tsx:174 | 已接通 |
| `/api/providers` | GET | `handler.GetProviders` | webui/src/routes/providers.tsx:102；webui/src/routes/logs.tsx:171；webui/src/routes/model-providers.tsx:168 | 已接通 |
| `/api/providers` | POST | `handler.CreateProvider` | webui/src/routes/providers/use-provider-form.ts:171 | 已接通 |
| `/api/providers/models/:id` | GET | `handler.GetProviderModels` | webui/src/routes/providers.tsx:128；webui/src/routes/model-providers.tsx:89 | 已接通 |
| `/api/providers/:id` | PUT | `handler.UpdateProvider` | webui/src/routes/providers/use-provider-form.ts:160 | 已接通 |
| `/api/providers/:id` | DELETE | `handler.DeleteProvider` | webui/src/routes/providers.tsx:158 | 已接通 |
| `/api/models` | GET | `handler.GetModels` | 无 | 无入口（见 ③-1） |
| `/api/models/select` | GET | `handler.GetModelList` | webui/src/routes/auth-keys.tsx:180；webui/src/routes/logs.tsx:172；webui/src/routes/model-providers.tsx:168；webui/src/routes/quickstart.tsx:496 | 已接通 |
| `/api/models` | POST | `handler.CreateModel` | webui/src/routes/model-providers/use-model-editor.ts:87 | 已接通 |
| `/api/models/order` | PATCH | `handler.UpdateModelOrder` | webui/src/routes/model-providers/use-model-order.ts:87 | 已接通 |
| `/api/models/:id` | PUT | `handler.UpdateModel` | webui/src/routes/model-providers/use-model-editor.ts:76 | 已接通 |
| `/api/models/:id` | DELETE | `handler.DeleteModel` | webui/src/routes/model-providers.tsx:460 | 已接通 |
| `/api/model-providers` | GET | `handler.GetModelProviders` | webui/src/routes/model-providers.tsx:215,235,347 | 已接通 |
| `/api/model-providers/status` | GET | `handler.GetModelProviderStatus` | webui/src/routes/model-providers.tsx:195 | 已接通 |
| `/api/model-providers` | POST | `handler.CreateModelProvider` | webui/src/routes/model-providers/use-model-provider-form.ts:169 | 已接通 |
| `/api/model-providers/:id` | PUT | `handler.UpdateModelProvider` | webui/src/routes/model-providers/use-model-provider-form.ts:165 | 已接通 |
| `/api/model-providers/:id/status` | PATCH | `handler.UpdateModelProviderStatus` | webui/src/routes/model-providers.tsx:403 | 已接通 |
| `/api/model-providers/:id` | DELETE | `handler.DeleteModelProvider` | webui/src/routes/model-providers.tsx:379 | 已接通 |
| `/api/version` | GET | `handler.GetVersion`（handler/version.go:9） | webui/src/routes/layout.tsx:182 | 已接通 |
| `/api/logs` | GET | `handler.GetRequestLogs` | webui/src/routes/logs.tsx:186；webui/src/routes/compare.tsx:56（经 `getLogById` 复用 id 过滤） | 已接通 |
| `/api/logs/:id/chat-io` | GET | `handler.GetChatIO` | webui/src/routes/log-chat.tsx:909；webui/src/routes/compare.tsx:56 | 已接通 |
| `/api/user-agents` | GET | `handler.GetUserAgents` | 无 | 无入口（见 ③-2） |
| `/api/logs/cleanup` | POST | `handler.CleanLogs` | webui/src/routes/logs.tsx:268 | 已接通 |
| `/api/logs/cleanup/history` | GET | `handler.GetCleanupHistory` | webui/src/routes/config.tsx:194 | 已接通 |
| `/api/auth-keys` | GET | `handler.GetAuthKeys` | webui/src/routes/auth-keys.tsx:196；webui/src/routes/quickstart.tsx:530 | 已接通 |
| `/api/auth-keys/list` | GET | `handler.GetAuthKeysList` | webui/src/routes/analytics.tsx:151；webui/src/routes/logs.tsx:173 | 已接通 |
| `/api/auth-keys` | POST | `handler.CreateAuthKey` | webui/src/routes/auth-keys.tsx:303 | 已接通 |
| `/api/auth-keys/:id` | PUT | `handler.UpdateAuthKey` | webui/src/routes/auth-keys.tsx:301 | 已接通 |
| `/api/auth-keys/:id/status` | PATCH | `handler.ToggleAuthKeyStatus` | webui/src/routes/auth-keys.tsx:329 | 已接通 |
| `/api/auth-keys/:id` | DELETE | `handler.DeleteAuthKey` | webui/src/routes/auth-keys.tsx:355 | 已接通 |
| `/api/config/:key` | GET | `handler.GetConfigByKey`（handler/api.go:925） | webui/src/routes/config.tsx:108（`anthropic_count_tokens`）、109（`log_cleanup_policy`） | 已接通（仅后端已定义的两个 key） |
| `/api/config/:key` | PUT | `handler.UpdateConfigByKey`（handler/api.go:949） | webui/src/routes/config.tsx:168,180 | 已接通 |
| `/api/peak-calendar` | GET | `handler.GetPeakCalendar`（handler/peak.go:21） | webui/src/routes/peak-calendar.tsx:73 | 已接通（第三轮由 `/api/peak-pricing` 改名而来） |
| `/api/peak-calendar` | PUT | `handler.UpdatePeakCalendar`（handler/peak.go:27） | webui/src/routes/peak-calendar.tsx:251 | 已接通（整份覆盖：不带 `holidaySyncedAt` / `holidaySource` 就等于抹掉同步记录） |
| `/api/peak-calendar/preview` | POST | `handler.PreviewPeakTerms`（handler/peak.go:51） | webui/src/routes/model-providers/peak-terms-editor.tsx:144 | 已接通（条款由请求体带过去，日历由服务端自己取；响应含判定所用 `timezone`） |
| `/api/peak-calendar/holidays/sync` | POST | `handler.SyncPeakHolidays`（handler/peak.go:89） | webui/src/routes/peak-calendar.tsx:267 | 已接通（服务端已落盘，响应回传整份日历） |
| `/api/quota/config` | GET | `handler.GetQuotaConfig`（handler/quota.go:37） | webui/src/routes/quota.tsx:113 | 已接通 |
| `/api/quota/config` | PUT | `handler.UpdateQuotaConfig`（handler/quota.go:57） | webui/src/routes/quota-settings.tsx:54 | 已接通 |
| `/api/quota/sources` | POST | `handler.UpsertQuotaSource`（handler/quota.go:85） | webui/src/routes/quota-editor.tsx:302 | 已接通 |
| `/api/quota/sources` | PUT | `handler.UpsertQuotaSource`（handler/quota.go:85） | webui/src/routes/quota-editor.tsx:303 | 已接通 |
| `/api/quota/sources/:id` | DELETE | `handler.DeleteQuotaSource`（handler/quota.go:115） | webui/src/routes/quota-editor.tsx:318 | 已接通 |
| `/api/quota/run` | POST | `handler.RunQuotaSources`（handler/quota.go:138） | webui/src/routes/quota.tsx:114 | 已接通 |
| `/api/quota/sources/:id/refresh` | POST | `handler.RefreshQuotaSource`（handler/quota.go:154） | webui/src/routes/quota.tsx:164 | 已接通 |
| `/api/quota/test` | POST | `handler.TestQuotaSource`（handler/quota.go:172） | webui/src/routes/quota-editor.tsx:285 | 已接通 |
| `/api/test/:id` | GET | `handler.ProviderTestHandler`（handler/test.go:84） | webui/src/routes/model-providers/use-model-provider-testing.ts:49 | 已接通 |
| `/api/test/react/:id` | GET | `handler.TestReactHandler`（handler/test.go:162） | webui/src/routes/model-providers/use-model-provider-testing.ts:79（SSE，直连 `/api/...` 不走 `apiRequest`） | 已接通 |
| `/api/test/count_tokens` | GET | `handler.TestCountTokens` | webui/src/routes/config.tsx:155 | 已接通 |

### 前端页面清单（App.tsx 注册的全部路由）

| 路由 | 页面文件 | 主要依赖的端点 |
|---|---|---|
| `/login` | webui/src/routes/login.tsx | 无（只写 localStorage） |
| `/`（外壳） | webui/src/routes/layout.tsx | `/api/version`；另调外部 GitHub Releases API（`checkLatestRelease`） |
| `/`（首页） | webui/src/routes/home.tsx | `/api/metrics/stats` |
| `/analytics` | webui/src/routes/analytics.tsx（+ analytics-filters.tsx / analytics-views.tsx） | `/api/metrics/stats`、`/api/metrics/granularities`、`/api/auth-keys/list` |
| `/quickstart` | webui/src/routes/quickstart.tsx | `/api/models/select`、`/api/auth-keys` |
| `/providers` | webui/src/routes/providers.tsx（+ providers/ 子组件） | `/api/providers*`、`/api/providers/template`、`/api/providers/models/:id` |
| `/models`、`/model-providers` | webui/src/routes/model-providers.tsx（+ model-providers/ 子组件） | `/api/models*`、`/api/model-providers*`、`/api/providers`、`/api/test/:id`、`/api/test/react/:id` |
| `/logs` | webui/src/routes/logs.tsx | `/api/logs`、`/api/logs/cleanup`、`/api/auth-keys/list`、`/api/models/select`、`/api/providers`、`/api/providers/template` |
| `/logs/:logId/chat-io` | webui/src/routes/log-chat.tsx | `/api/logs/:id/chat-io` |
| `/compare` | webui/src/routes/compare.tsx | `/api/logs`、`/api/logs/:id/chat-io` |
| `/quota` | webui/src/routes/quota.tsx（+ quota-editor.tsx / quota-settings.tsx / quota-card.tsx / quota-source-view.tsx / quota-item-dialog.tsx） | `/api/quota/*` 全部八个 |
| `/config` | webui/src/routes/config.tsx | `/api/config/:key`（两个 key）、`/api/test/count_tokens`、`/api/logs/cleanup/history`；页内嵌入 `PeakCalendarCard`（工作日日历） |
| （无独立路由） | webui/src/routes/peak-calendar.tsx | `/api/peak-calendar` 四个端点里的读取/保存/同步三个；只作为卡片挂在 `/config` 页面内，不占路由 |
| （无独立路由） | webui/src/routes/model-providers/peak-terms-editor.tsx | `/api/peak-calendar/preview`；峰谷**条款**不单独提交，随关联的 create/update 一起走（`peak: null` 表示移除配置） |

---

## ② 代理侧路由（面向 SDK，非控制台）

这些路由由 `main.go:61-103` 注册，服务的是 OpenAI / Anthropic / Gemini 三套 SDK 与兼容客户端，**控制台不调用它们中的任何一个**。它们没有"前端入口"一说——状态一律记"仅 SDK"，不算缺口。

| 端点 | 方法 | 处理函数 | 鉴权 |
|---|---|---|---|
| `/openai/v1/models` | GET | `handler.OpenAIModelsHandler` | `Authorization: Bearer` |
| `/openai/v1/chat/completions` | POST | `handler.ChatCompletionsHandler` | `Authorization: Bearer` |
| `/openai/v1/responses` | POST | `handler.ResponsesHandler` | `Authorization: Bearer` |
| `/anthropic/api/event_logging/batch` | POST | `handler.EventLogging` | 无鉴权（Claude Code 批量事件上报） |
| `/anthropic/v1/models` | GET | `handler.AnthropicModelsHandler` | `x-api-key` 或 `Authorization: Bearer` |
| `/anthropic/v1/messages` | POST | `handler.Messages` | `x-api-key` 或 `Authorization: Bearer` |
| `/anthropic/v1/messages/count_tokens` | POST | `handler.CountTokens` | `x-api-key` 或 `Authorization: Bearer` |
| `/gemini/v1beta/models` | GET | `handler.GeminiModelsHandler` | `x-goog-api-key` |
| `/gemini/v1beta/models/*modelAction` | POST | `handler.GeminiGenerateContentHandler` | `x-goog-api-key` |
| `/v1/models`、`/v1/chat/completions`、`/v1/responses` | GET/POST | 同 OpenAI 三处理器（兼容别名） | `Authorization: Bearer` |
| `/v1/messages`、`/v1/messages/count_tokens` | POST | 同 Anthropic 两处理器（兼容别名） | `x-api-key` 或 `Authorization: Bearer` |

静态资源与兜底（`main.go:191-206`）：`GET /assets/*` 由 `r.StaticFS` 服务内嵌前端；`NoRoute` 对非 `/api/`、非 `/v1/` 的 GET 一律返回 `index.html`（SPA 前端路由接管），其余返回纯文本 404。这两条不构成"功能"，不进对照表。

代理侧能力在控制台里的**间接**可见面是：`/api/test/:id`（连通性）、`/api/test/react/:id`（工具调用回放）、日志页（`/api/logs`）与分析页——即"发出去的请求"本身不出现在控制台，只有结果被观测到。

---

## ③ 缺口：后端有、前端无

判定口径：在 `webui/src`（排除 `*.test.*`）全目录搜过端点路径与对应 api 函数名，命中为零才记缺口。

### ③-1 `GET /api/models` —— 无入口（能力完整，缺的是入口）

- 后端：`handler.GetModels`，注册于 main.go:120，带分页/搜索/策略过滤。
- 前端：`webui/src/lib/api.ts:183` 有包装函数 `getModels`，但 `webui/src` 内**没有任何调用者**（唯一命中就是它自己的定义）。
- 缺的是入口：模型列表页用的是 `/api/models/select`（`getModelOptions`，不带分页），分页版列表接口没人用。要么给模型管理页接上服务端分页，要么承认列表量小、删掉这个包装函数。

### ③-2 `GET /api/user-agents` —— 无入口（能力完整，缺的是入口）

- 后端：`handler.GetUserAgents`，注册于 main.go:139，返回去重后的 User-Agent 清单。
- 前端：`webui/src/lib/api.ts:657` 有包装函数 `getUserAgents`，同样**无任何调用者**。
- 缺的是入口：日志页的 UA 筛选维度目前靠日志数据自行归纳，没有用这个"服务端权威清单"去填充筛选候选。

> 另有两个 `config` key 的边界情况已核实，**不算缺口**：`/api/config/:key` 是通用 KV，后端只定义了 `anthropic_count_tokens` 与 `log_cleanup_policy` 两个 key（models/config.go:12-13），两者在 config 页都有编辑入口。

---

## ④ 前端指向了不存在的端点

整理时发现两处，**已在同一次整理中删除**，留在这里是为了记住"这类东西长得什么样"：

| 前端函数 | 调用的端点 | 后端现状 | 处置 |
|---|---|---|---|
| `getSystemStatus` | `GET /api/status` | main.go 无此路由 | 删除包装与 `SystemStatus` 类型 |
| `getProviderMetrics` | `GET /api/metrics/providers` | main.go 无此路由（只有 `/api/metrics/stats` 与 `/api/metrics/granularities`） | 删除包装与 `ProviderMetric` 类型 |

两点说明：

1. 这两个函数在 `webui/src`（排除测试）里**没有任何调用者**，所以不会真的发出 404——它们是死代码，风险在于留着一个看似可用的入口：谁按名字接手就会踩空。
2. 它们对应的能力（系统状态、provider 指标）已被 `/api/metrics/stats` 覆盖，所以正确的处置是删掉包装，而不是去后端补路由。

除此之外，`webui/src` 全目录没有发现其他指向不存在端点的调用；更早删除的 `/api/quota/discover` 与 `/api/quota/import` 在前端已无任何残留引用（配额页只用 `config` / `sources` / `run` / `refresh` / `test` 五个路径）。

---

## ⑤ 非 HTTP 的后端能力

| 能力 | 位置 | 前端可及性 | 说明 |
|---|---|---|---|
| 熔断器开关 | `balancers/breaker.go`；Model.Breaker 字段 | **已接通（开关级）** | 每个模型可单独开/关熔断，见 webui/src/routes/model-providers/model-dialogs.tsx:151、use-model-editor.ts:13。 |
| 熔断器参数（失败阈值 5、冷却 60s、半开恢复请求数 2） | balancers/breaker.go:28-30（`MaxFailures` / `SleepWindow` / `MaxRequests`） | **完全无入口** | 硬编码包级变量，既不进 UI 也不读环境变量；要改只能改代码重编译。 |
| 负载均衡策略 Lottery / Rotor | `balancers/balancers.go:16`（Lottery）、:71（Rotor）；consts/consts.go:14-18 | **已接通** | 模型表单可选 lottery/rotor，见 model-dialogs.tsx:175,180、use-model-editor.ts:12,24；列表页显示当前策略。策略是模型级字段，不是全局开关。 |
| 配额写权限开关 `LLMIO_QUOTA_ALLOW_WRITE` | quota/config.go:138-140 | **仅环境变量** | 设为 `false` 进只读模式：`/api/quota` 的写端点全部拒绝。前端只通过 `GET /api/quota/config` 读到"当前是否只读"并禁用按钮，改不了它。 |
| 配额配置文件路径 `LLMIO_QUOTA_CONFIG` | quota/config.go:124-130 | **仅环境变量** | 默认 `./db/quota.config.json`（quota/config.go:34）。前端能读写配置**内容**，改不了文件**位置**。 |
| 脚本沙箱（goja 子进程） | `quota/sandbox.go`（`SandboxCommand` 常量 :50）；main.go:32-36 | **已接通（使用侧）** | 配额数据源可选 `script` 类型并编辑源码（quota-editor.tsx），试跑走 `/api/quota/test`。沙箱机制本身（超时、内存、是否允许 fetch）由配置项与硬编码决定，UI 暴露源码（quota-editor.tsx:522）、`env`（:430）与 `allowFetch`（:542）。沙箱超时、内存上限等参数仍由后端决定。 |
| 日志清理调度器 | `service.StartLogCleanupScheduler`（service/log_cleanup.go:78）；检查间隔 1 小时硬编码 :16 | **部分接通** | 开关与保留天数在 config 页（`log_cleanup_policy`）；**调度周期不可改**，且手动清理另有日志页按钮（`/api/logs/cleanup`，logs.tsx:268）。 |
| 内置节假日数据 → 外网同步 | `service/holidays/`、service/holidays.go；handler/peak.go:89 | **已接通** | 峰谷日历的"从上游同步"触发外网抓取，见 peak-calendar.tsx:267。抓不到时后端回 502，原文透出给用户重试。 |
| 上游版本检查 | `checkLatestRelease`（webui/src/lib/api.ts:786） | 纯前端 | 直接打 GitHub Releases API，不经过本服务；与本项目后端无关，列出仅为免生疑。 |

---

## ⑥ 环境变量 vs UI 可改项

后端全部环境变量读取点只有四处（`env.GetWithDefault` / `os.Getenv`）：main.go:54、main.go:182、models/init.go:79、quota/config.go:126 与 :139。

| 环境变量 | 作用 | 读取点 | 前端能否改 |
|---|---|---|---|
| `TOKEN` | 控制台登录与全部 API 鉴权 | main.go:54 | 不能。登录页只把它写进 localStorage 当凭据用，换不了服务端的值。 |
| `LLMIO_SERVER_PORT` | 监听端口 | main.go:182 | 不能。 |
| `GIN_MODE` | Gin 运行模式 | 由 Gin 自读 | 不能。 |
| `TZ` | 日志/调度的时区 | 由运行环境决定（main.go:40 仅打印） | 不能。峰谷计费的分时判定依赖它，属启动期决定。 |
| `DB_VACUUM` | 启动时对 SQLite 做 VACUUM | models/init.go:79 | 不能。启动期一次性动作。 |
| `LLMIO_QUOTA_CONFIG` | 配额配置文件路径 | quota/config.go:126 | 不能，见 ⑤。 |
| `LLMIO_QUOTA_ALLOW_WRITE` | 配额写操作开关 | quota/config.go:139 | 不能，见 ⑤；前端只能显示其后果。 |

**结论**：所有"能改的东西"都走数据库 KV（`/api/config/:key`）或各自的专用端点（peak-calendar / quota / 模型与 provider 记录），环境变量层一律是启动期决定、UI 改不了。反过来说，UI 改完即生效的能力（模型、provider、密钥、峰谷条款与日历、配额源、日志清理策略）都不需要重启进程。峰谷的**条款**不占专用端点，随「模型 × 上游」关联的 create/update 一起提交。
