# console-iter4：日志保真与总览可观测性

> 状态：进行中
> 拟稿 2026-10-15
> 分支：`feat/console-iter4`（基线 `7e861cc`，= `origin/master` = `upstream/master`）
> 前置阅读：`docs/merge-dashboard-plan.md`（前三轮的方案与实测记录）、`docs/api-frontend-parity.md`（前后端接口对照）、`.impeccable.md`（设计上下文）
> 说明：提交正文里不写哈希——历史一改写必然失准，需要留痕的提交号只写进「进度」表

## 进度

| 步 | 内容 | 状态 | 提交 |
|---|---|---|---|
| 0 | 本文档 | ✅ | |
| 1 | UI 偏好存储 + `useAutoRefresh` | ⬜ | |
| 2 | 总览页自动刷新与偏好持久化 | ⬜ | |
| 3 | 日志页 / 配额页改用 hook 并持久化档位 | ⬜ | |
| 4 | 抽取日志详情抽屉为共享组件 | ⬜ | |
| 5 | 首页错误请求改为打开抽屉 | ⬜ | |
| 6 | 后端：模型维度口径 + 文本维度精确匹配 | ⬜ | |
| 7 | 后端：`/api/logs` 上游模型筛选 + 候选端点 | ⬜ | |
| 8 | 前端：模型 / 请求名口径对齐 | ⬜ | |
| 9 | 四协议结构化请求内容解析 | ⬜ | |
| 10 | 请求内容页按结构化模型渲染 | ⬜ | |
| 11 | 日志表头自定义数据列 | ⬜ | |
| 12 | CI：Go 覆盖率门禁 | ⬜ | |
| 13 | 本文档回填与 API 对照表更新 | ⬜ | |

---

## 一、问题、根因与方案

用户提的五条，全部先在代码里定位到根因再动手。下面每条按「现象 → 根因（证据）→ 方案 → 测试」写。

### 问题 1：总览没有自动刷新，自动刷新设置不保留

**根因。**

- 总览页（`webui/src/routes/home.tsx`）只有 `useEffect(() => void load(), [load])` 与一个手动刷新按钮（:114-123），全文件没有一处 `setInterval`。
- 日志页 `logs.tsx:166`（`const [refreshSec, setRefreshSec] = useState(0)`）与配额页 `quota.tsx:74`（`const [interval, setIntervalSec] = useState(0)`）的档位都是裸的组件状态——组件卸载即丢失，于是**刷新页面就回到「不自动」**。
- 全项目没有共享的轮询 hook：`src/hooks/` 里只有 `use-series-color.ts`。日志页（`logs.tsx:222-238`）与配额页（`quota.tsx:161-176`）各自手写了一遍 `window.setInterval` + `visibilitychange` 监听，是同一段逻辑的复制粘贴。

**方案。**

1. 新增 `webui/src/lib/prefs.ts`（纯逻辑层，进覆盖率门禁）。键 `llmio-ui-prefs-v1`，载荷内含 `version`。照 `lib/quota.ts:658` 的 `loadQuotaView(raw)` 写法：
   - `loadUiPrefs(raw: string | null): UiPrefs` —— 纯函数，只做解析与兜底，不碰 `localStorage`；
   - `serializeUiPrefs(prefs): string`、`setRefreshSec(prefs, page, sec)`、`setHomeRange(prefs, range)`、`setLogsColumns(prefs, cols)`；
   - 读写薄封装 `readUiPrefs()` / `writeUiPrefs()` —— 隐私模式或站点数据被禁时 `localStorage` 会抛异常，一律吞掉回默认（为一个装饰性偏好抛错会让整页打不开）。
2. 新增 `webui/src/hooks/use-auto-refresh.ts`：`useAutoRefresh(sec: number, tick: () => void)`。行为：
   - `sec <= 0` 时完全不起计时器；
   - `document.hidden` 时跳过本次（省资源）；
   - 标签页回到前台时立即补一次（否则回来之后要干等一个周期）；
   - 回调用 `useRef` 存，避免不稳定的回调重启计时器；
   - 卸载时清理定时器与事件监听。
   日志页与配额页的重复实现改为调用它（删掉两处复制）。
3. 档位：日志页 `[0,5,10,30,60]`、配额页 `[0,60,120,300,600]` 不变；**总览页新增 `[0,10,30,60,300]`**——`/api/metrics/stats` 是全量聚合（最多扫 `StatsScanLimit = 200_000` 行），档位刻意比日志页保守，理由写进常量注释。
4. 总览页 `load(silent?)` 加静默语义，与日志页 `fetchLogs(silent)` 一致：静默刷新的失败**不落错误条、不闪骨架屏**——一次网络瞬断把整页换成失败态比保留上一次的结果更打扰人；静默期间已有数据则原样保留。
5. 总览页的时间范围（`homeRange`）一并持久化——同一份偏好存储，顺手的事。
6. i18n：文案收敛到 `common:auto_refresh.*`（label / off / every），删掉 logs 与 quota 命名空间里重复的三份键。

**测试。** `prefs.test.ts`（往返、null / 损坏 JSON / 未知版本 / 非法值兜底、列 id 剪枝、隐私模式抛错）；`use-auto-refresh.test.tsx`（六条行为：关闭、滴答、隐藏跳过、回前台补一次、改档位重启、卸载清理）；`home.test.tsx` 补「N 秒后被调用且不闪骨架」「重挂载后档位与时间范围仍在」；`logs.test.tsx` / `quota.test.tsx` 补「档位从偏好读入并写回」。

---

### 问题 2：请求内容解析不够细致，特别是 tool call

**根因。**

`webui/src/routes/log-chat.tsx` 的解析分请求侧与响应侧两块，两者都是「取到字符串再拼」：

- **流式 tool call 被塞进正文**：`log-chat.tsx:519` 的 `if (delta?.tool_calls) content += JSON.stringify(delta.tool_calls)` —— 工具调用被 `JSON.stringify` 拼进回复正文，既不可读也无法按名字取。
- Anthropic 的 `content_block_delta` 只认 `delta.text`，`thinking_delta` / `signature_delta` / `input_json_delta` 全丢；响应侧 `extractResponseContent`（:448-454）对 `tool_use` 块一律 `c.text ?? JSON.stringify(c)`。
- OpenAI Responses 的 `function_call` 项、Gemini 的 `functionCall` part 同样落 `JSON.stringify`。
- 无任何 `thinking` / `reasoning` 处理：全仓 `grep -rniE "thinking|reasoning"` 在 `webui/src` 下零命中。

**参照实现。** `E:\Projects\llmio_dashboard` 的 `web/src/views/ChatIo.vue`（Vue，全客户端解析）在这几处明显更细：

- `:324-355` —— 非流式响应归一成**一个合成增量**（`choices[].delta = choices[].message`），流式与非流式共用同一条装配路径；
- `:242-259` —— 流式 tool call 按 `tc.index` 累积 `name` 与 `arguments` 分片，末尾 `prettyJson` 美化；
- `:227-240` —— `reasoning_content` 单独装配成 thinking 文本；
- `:192-194` —— `role:"tool"` 的结果按 `tool_call_id` 回扫 assistant 消息，把调用名一并显示出来。

它的短板也要一并记下（本轮要补的正是这些）：只支持 OpenAI 协议、Anthropic 完全没处理、`tool_calls.id` 被丢弃、thinking 只认 OpenAI 非标准字段、工具结果的关联是 O(n²) 的线性回扫、无任何测试。

**方案。** 新增 `webui/src/lib/chat-io/`，把解析从页面里彻底拿出去：

```
lib/chat-io/
  types.ts        # 统一结构模型
  normalize.ts    # 单装配器：增量 → 结构化
  parse.ts        # 编排：协议探测 + 请求/响应解析
  openai.ts       # chat completions
  openai-res.ts   # responses
  anthropic.ts    # messages
  gemini.ts       # generateContent
  fixtures/       # 真实报文夹具（从 db/llmio.db 的 chat_ios 与 e2e 跑批里抽）
```

统一结构模型：

```ts
type ContentPart =
  | { kind: "text"; text: string }
  | { kind: "thinking"; text: string; signature?: string }
  | { kind: "image"; url?: string; raw: unknown }
  | { kind: "tool_use"; call: ToolCall }
  | { kind: "tool_result"; result: ToolResult }
  | { kind: "unknown"; raw: unknown }

interface ToolCall { id?: string; index?: number; name: string
                     argumentsRaw: string; arguments?: unknown; parseError?: string }
interface ToolResult { callId?: string; callName?: string; parts: ContentPart[]; isError?: boolean }
interface Message  { role: string; blocks: ContentPart[]; toolCalls: ToolCall[]; toolResult?: ToolResult }
interface ParsedRequest  { protocol; model?; system: ContentPart[]; messages: Message[]
                           tools: unknown[]; params: Record<string, unknown> }
interface ParsedResponse { protocol; model?; stream: boolean; text: string
                           thinking: ContentPart[]; toolCalls: ToolCall[]
                           usage?: unknown; finishReason?: string
                           chunks: string[]; malformed: number }
```

三处刻意比参照实现做得更完整：

1. **单装配器**：协议适配器只负责把「一个增量」或「整个非流式响应」翻译成统一的增量序列（非流式 = 一个合成增量），之后交给同一个装配器。流式 / 非流式从此一条代码路径，不会出现「两个分支各写一遍」的漂移。
2. **tool call 按 index 累积，同时按 id 建索引**：分片可以切在 JSON token 中间（累积字符串再统一解析，参照 `:242-259`）；装配结束按 `id` 建一张 `Map<id, callName>`，`tool_result` 用 O(1) 回挂调用名——**不用参照实现的 O(n²) 线性回扫**。Gemini 没有 id，退化为按名匹配。
3. **thinking 全协议**：OpenAI `reasoning_content` / `reasoning`；Anthropic `thinking` / `redacted_thinking` 块与 `thinking_delta` / `signature_delta` 事件（signature 一并保留，回放推理需要）；OpenAI Responses 的 reasoning item 与 `response.reasoning_summary_text.delta`；Gemini 的 `thought: true` part。

**降级契约（逐条都有测试，宁可选一条也别静默吞掉）**：

| 情况 | 行为 |
|---|---|
| 增量解析失败 | 计入 `malformed`，界面显示「N 段未能解析」；不静默丢 |
| tool arguments 解析失败 | 保留原文 `argumentsRaw`，记 `parseError`，仍展示 |
| 未知块类型 | 落 `{kind:"unknown"}`，展示原始 JSON |
| `Style` 未知 | 先按形状嗅探（`choices` / `content[].type` / `candidates`），嗅探不出才退原始 |

`log-chat.tsx` 改为消费结构化模型：`tool_use` / `tool_result` 卡片（名字 + 美化参数 + id，result 上标出它回应的调用名）、thinking 独立折叠卡、`finish_reason` 与 usage 表；原始 JSON 与逐段增量视图保留不动。

**明确延后**：对比页 `lib/compare.ts` 的 `extractStructure` 本轮**不复用**新解析器。它只关心 `tools → system → messages` 的**前缀顺序**与稳定的条目标题串，是有意的另一套口径（见该文件 :1-15 的设计说明）；换解析器会改动已 100% 覆盖的语义，收益只是少几行重复。留待下一轮，届时先改它的测试再改实现。

---

### 问题 3：首页错误请求无请求内容，点击是无效动作

**根因。**

- `home.tsx:367` 的错误分组 `<li>` **没有 onClick**；唯一能点的是 :390-398 那条小字链接 `home:errors.view_log`。
- 那条链接指向 `/logs/<id>/chat-io`，而 `logs.tsx:279` 的 `canViewChatIO = Status === "success" && ChatIO` —— **对 error 恒为假**，于是落到请求内容页的「未找到会话记录」。用户视角就是「点了也没用」。

**为什么错误日志没有请求内容。** `ChatIO.Input` 是在 `service/chat.go` 的 `RecordLog`（:292-330）里写的，而 `RecordLog` 只在 `BalanceChat` 成功之后才被 `chatHandler` 起 goroutine 调用；失败路径在 `handler/chat.go:117-120` 直接 return，`ChatIO` 行压根不创建。

> 决定：本轮**不改后端**，失败路径不补记请求体（避免失败风暴时占用存储）。抽屉只展示已有信息；若日后要补，方案是失败路径按 `AuthKey.IOLog` 开关只写 `ChatIO.Input`（不写响应），走 `models/init.go` 的迁移与 `models/migrate_test.go` 的幂等用例。

**方案。**

1. 把 `logs.tsx:769-920` 的 `LogDetailSheet` 抽成 `webui/src/components/log-detail-sheet.tsx`，两种入参：
   - `{ log }` —— 日志页已有整行，不发请求；
   - `{ logId }` —— 只有 id（首页样本），走 `getLogById`（`lib/api.ts:820`，复用列表端点的 `id` 过滤），补**加载中**与**该条已不存在**两态。
   文案继续用 `logs` 命名空间（跨页可用，不做键搬迁，避免大动 logs 页面测试）。
2. 抽屉内「查看请求内容」按钮维持只对 `success && ChatIO` 显示；对错误日志改给「在日志页查看」→ `/logs?id=<id>`，替掉通往 chat-io 的死链。
3. `home.tsx` 错误面板：分组行改为按钮 → 打开抽屉（用 `g.samples[0].id`）；`samples` 逐条列出（时间 + 错误摘要），每条各自可点开；无样本的分组不可点。

**测试。** `home.test.tsx`：点错误行 → 抽屉出现且带错误原文、**不发生导航**、无样本时禁用；`logs.test.tsx` 回归：抽屉行为与文案不变。

---

### 问题 4：模型与请求名没有理清，下钻-模型筛选显示无数据

**根因（这是最实的一条）。** 两个名字、两个字段、三个地方各说各话：

| 位置 | 「模型」指的是 |
|---|---|
| `chat_logs.name` | 客户端请求体里写的模型名 = llmio 别名 |
| `chat_logs.provider_model` | 真正发给提供商的模型 id（`ModelWithProvider.ProviderModel`） |

- `service/stats.go:641` 的 `byModel` 与 `:645` 的 `byName` **都按 `l.Name` 分组** —— 也就是说分析页「模型」和「请求名」两个维度**内容完全一样**，两个字段是冗余的。
- 但查询参数 `model=` 匹配的是 `l.ProviderModel`（`service/stats.go:302`，SQL 下推在 :1090）。
- 下钻值来自 `byModel`（= `l.Name`），却被当成 `?model=`（= `provider_model`）发出去。名字与上游 id 不同（常态）→ **筛出 0 行 → 表格显示「该维度在所选范围内没有数据」**。
- 前端 `webui/src/lib/analytics.ts:230-236` 的维度映射与 :250-256 的筛选映射各有一份，两侧名字对齐了、语义没对齐，所以前端单测（mock 掉 `getStats`）测不出来。
- 连带一处死参数：`/api/logs` **没有** `provider_model` 参数（`handler/api.go:799-855` 只认 provider_name / name / status / style / auth_key_id / trace_id / session_id / id），而 `webui/src/lib/api.ts:801` 一直在发 `provider_model` —— 被后端静默丢弃。

**术语（本轮先定义，再改代码，全站以此为准）**：

| 术语 | 字段 | 含义 | 注意 |
|---|---|---|---|
| **请求名** | `chat_logs.name` | 客户端请求里写的模型名，即 llmio 别名（`models.name`） | 同一请求名可路由到**多个**上游模型 |
| **模型** | `chat_logs.provider_model` | 真正发给提供商的模型 id | **同名可跨提供商**，模型维度必须与提供商维度搭配看 |
| 提供商 | `chat_logs.provider_name` | | |

**下钻契约**：点分组名筛选 ⇔ 结果恰为该分组。现状是子串匹配（`containsSubstr`），一旦有一个分组名是另一个的前缀就会串味：`gpt-4o` 会命中 `gpt-4o-mini`，`claude-cli/2.1` 会命中 `2.1.281`。这正是「同名处理」要管的事，所以四个文本维度一并改为**精确 IN**，与 `/api/logs` 的既有口径（`splitCSV` + `IN`）对齐。

**后端改动。** `service/stats.go`：

- `models.add(l.ProviderModel, l)`（原 :641 用 `l.Name`）
- `modelProviders.addJoint(l.ProviderModel, l.ProviderName, l)`（原 :643）
- 错误分组 `g.Models = bump(g.Models, l.ProviderModel)`（原 :659）
- `LogRow.Model = l.ProviderModel`，并**新增 `RequestName = l.Name`**（`:679` 构造处、`:452` 结构体）
- 四个文本维度（model / name / provider / ua）子串 → 精确 IN，内存 `match`（:288-310）与 SQL 下推（:1084-1104 的 `LIKE`）同步改，两处口径必须一致（现有注释已经点明这个约束）
- `names.add(l.Name, l)` 不变 —— `byName` 就是「请求名」维度

`handler/api.go`：

- `GET /api/logs` 增 `provider_model`（精确 IN，与 `name` 同形）
- 新增 `GET /api/provider-models`：`chat_logs` 的 distinct 非空 `provider_model`（照 `/api/user-agents` :931 的写法）

**前端改动。**

- 日志页列：`模型`（现显示 `log.Name`）→ 改名为 **请求名**；`提供商模型` → 改名为 **模型**（数据与列序不变，只改标签）。
- 日志页筛选：**请求名**（URL `requestName` → 后端 `name`，选项来自 `/models/select`，即 llmio 别名表）+ 新增 **模型**（URL `providerModel` → 后端 `provider_model`，选项走新端点）。
- URL 兼容：`lib/logs.ts` 的 `LOGS_MULTI_KEYS` 增 `requestName` / `providerModel`，旧键 `model` 降级为**只读别名**——读到就当请求名，任何一次写回都改写成 `requestName`。新版本永不写 `model`，因此不会与新语义相撞，**老书签的含义不变**。
- 分析页：维度文案对齐（模型 = 上游模型名、请求名 = 客户端请求名）；筛选选项来源不用动（口径改对后 `byModel` / `byName` 各自自动正确）；补一条同名说明文案。
- 首页：错误面板「受影响模型」改后会显示上游模型名；排行榜在 `model ≠ requestName` 时并列显示请求名，免得看不出「这条是哪个别名发来的」。

**测试。** Go 表驱动：分组口径、**下钻往返**（对 `byModel` 的每个分组名按 `model=` 过滤，回来的条数必须等于该组的 total）、前缀同名不串味、`/api/logs?provider_model=` 与 `/api/provider-models`。TS：URL 只读别名的两条路径、筛选映射。

---

### 问题 5：请求日志页表头不支持自定义数据列

**根因。** `logs.tsx:465-505` 的表头与 `:506-589` 的单元格是**两份写死的清单**——没有列注册表、没有显隐 UI、没有任何持久化。两份清单各改各的，天然存在表头与单元格对不上的风险。

**方案。**

1. 新增 `webui/src/lib/logs-columns.ts`（纯逻辑层，进覆盖率门禁）：**一份列注册表**同时描述列名（i18n key）、宽度、是否常显、是否默认可见、以及取值函数。
   - 默认可见 = 现状 13 列；
   - 候选再纳入 9 个**已入库但页面没展示**的字段：TraceID、SessionID、TPS、成本、重试次数、远端 IP、User-Agent、上游协议、桥接说明。
   - 勾选框列、冻结 ID 列、操作列常显，不可隐藏。
2. **表头与单元格都由这一份注册表驱动**（一处定义，从根上杜绝两边漂移）。
3. 工具栏加「列」Popover（复用 `components/ui/popover.tsx` + `ui/checkbox.tsx`）：勾选即存，另有「恢复默认」。
4. 成本列由行内 `input_price` / `cache_read_price` / `output_price` + tokens 现算——纯函数放 `lib/logs.ts`（进门禁、有测试），展示复用既有 `formatCost`。
5. 显隐选择进 `prefs.logsColumns`（问题 1 的偏好存储），刷新后保持。

**测试。** 注册表：默认集合、归一化（未知 id 丢弃、新增列按注册表顺序补位、序列化往返、恢复默认）、**每个列 id 在样例行上都能取到值**；页面：关一列 → 表头与单元格**同时**消失、刷新后仍关着。

---

## 二、数据库兼容

**本轮零 schema 变更，不涉及第四次数据库改动。**

- `chat_logs.provider_model` 及其单列索引（`models/model.go:87`）自合并轮 S1 起就在；
- 本轮所有新查询（`/api/logs?provider_model=`、`GET /api/provider-models`、stats 的分组与筛选）只读既有列，并吃这条既有索引；
- 不新增列、不加索引、不改 `gorm` tag、不新增 `AutoMigrate` 的表。

**纪律（若实施中发现确需加列或加索引，必须走这条线）**：改 `models/model.go` 的 gorm tag → 补 `models/migrate_test.go` 的升级用例（用 `testdata/upstream_schema.sql` 起旧库 → `Init` → 断言列与索引存在、顺序正确）→ 保住 `TestInit_UpgradeIsIdempotent` 与 `TestChatLogIndexes` → 在 `docs/` 留迁移说明。**禁止只改模型不打测试**——前几轮的教训就是 schema 漂移没人发现。

**回归命令**：`go test ./models/...`；`python e2e/check_compression.py`、`python e2e/check_vacuum.py`（两者都会走「旧库启动路径」，并且直接读 SQLite 文件字节校验）。

---

## 三、覆盖率要求

**前端（已有硬门禁，本轮新模块全部纳入）**。`webui/vitest.config.ts:63-68` 已对纯逻辑层（A 层）设 **100%** statements / branches / functions / lines。本轮把 `lib/prefs.ts`、`lib/logs-columns.ts`、`lib/chat-io/**`、`hooks/use-auto-refresh.ts` 与 `lib/logs.ts` 的新增函数都加进 `include` 清单——**理由与既有注释一致：这些模块的每个分支都是一个语义决定**（档位非法值兜底、tool call 分片切在 JSON 中间、下钻往返一致），漏测就是漏语义，而不是漏了一行展示代码。组件与页面仍按四态覆盖、不设百分比。

**后端（本轮新增全仓统一阈值）**。

- 先量基线：`go test -coverprofile=/tmp/cover.out ./... && go tool cover -func=/tmp/cover.out | tail -1`，把基线数字记进本文档；
- 阈值取 `min(70%, 基线向下取整到 5 的倍数)`；
- 新增 `make cover` 目标（跑 coverprofile + `go tool cover -func` + 阈值比较，不足即非零退出），并在 `.github/workflows/test.yml` 加一步；
- 本轮新增的后端代码（stats 口径、logs 新参数、新端点）必须全部有表驱动测试。

**Windows 注意**：`go test` 权限不足时先 `GOCACHE=$PWD/.gocache`，跑完立刻删掉该目录（AGENTS.md 的约定）。

---

## 四、验证矩阵

### 单测（提交前必跑）

```
GOCACHE=$PWD/.gocache go test -count=1 ./...     # 跑完删 .gocache
cd webui && pnpm run lint && pnpm run test:coverage && pnpm run build
```

### 集测

- **handler 级**：沿用 `handler/logs_test.go` 的驱动方式（temp-file SQLite + 换装 `models.DB` + `gin.CreateTestContext`），覆盖 `/api/logs?provider_model=`、`GET /api/provider-models`、stats 的口径与精确匹配。
- **e2e 真机跑批**：`python e2e/run_matrix.py --upstream stub`，并补一列断言——`/api/logs?provider_model=` 的过滤正确性、`byModel` 的分组口径、**下钻往返**、前缀同名不串味。三处核对（客户端拿到什么 / 上游收到什么 / 日志记下什么）缺一不可，这条沿用 `e2e/run_matrix.py` 头部的既有论证。

### 真机调试

```
cd webui && pnpm run build
LLMIO_DB_PATH=<真实库的拷贝> TOKEN=<token> go run main.go
```

用浏览器逐条走查并截图留档：

1. 总览设 10s → 「在途请求数」自己会动、不闪骨架；**刷新页面档位仍在**；
2. 日志页关两列 → 表头与单元格同时消失；刷新后仍关着；
3. 打一发带 `tools` 的真实请求 → 请求内容页的 tool call 卡片显示名字与美化参数；
4. 分析页 下钻 → 模型 → 点名字 → **有数据**，且条数与该行计数一致；
5. 首页点错误行 → 抽屉打开、**不跳页**，看得到错误原文。

### 用户实测（未确认前不合并 PR）

1. 总览页：设档位 → 关掉浏览器重开 → 档位与时间范围是否还在？在途数是否自己刷新？
2. 日志页：隐藏几列 → 刷新 → 是否仍在？表头与数据是否仍对齐？
3. 请求内容页：找一条带工具调用的请求 → tool call 是否有名字、参数是否可读、思考内容是否独立成块？
4. 分析页：下钻 → 模型 → 点任一模型名 → 是否还有数据？条数是否对得上？（改前这里是「无数据」）
5. 首页：点一条错误 → 抽屉是否打开、能看到什么信息？

---

## 五、风险与回滚

- **对外语义变更**：`byModel` / `byName` 的分组口径变了，四个文本维度从子串改精确。缓解：本文档与 `docs/api-frontend-parity.md` 明写变更；分析页的筛选选项本就来自观测值，不受影响；测试钉住两种语义各自的行为。回滚只需回退该笔提交。
- **URL 兼容**：老书签的 `model=` 仍按请求名解释（只读别名 + 写回改写），不会静默变味。
- **解析层是新模块而非原地改写**：`log-chat.tsx` 切换渲染口径前，旧函数与新模块并存；页面测试全绿后才删旧路径，避免「解析器写坏导致请求内容页整页空白」。
- 每次提交前跑 lint + 前端 build（`main.go` 用 `//go:embed webui/dist`，产物缺失时 `go vet` / `go build` 是编译失败而非测试失败）。

---

## 六、实测记录

（实施时回填：每步的验证命令输出、真机截图链接、用户实测结论。）
