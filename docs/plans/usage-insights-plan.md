# usage-insights：下钻 Token 列、成本趋势与请求体自愈

> 状态：设计稿，待评审。功能 C 的判据模式表在用户提供实测报错后补一轮校验（见 §3.9）
> 拟稿 2026-10-24
> 分支：`feat/usage-insights-plan`（基线 `d51e8cf` = `origin/master`）
> 前置阅读：`docs/console-iter4-plan.md`（术语：请求名 / 模型 / 提供商；下钻契约）、
> `docs/protocol-bridge.md`（bridge 的判据体系：拒绝 / 记账 / 不记账三档）
> 说明：提交正文里不写哈希，需要留痕的提交号只写进「进度」表

## 进度

| 步 | 内容 | 状态 | 提交 |
|---|---|---|---|
| 0 | 本文档 | ✅ | |
| A1 | 下钻表加 Token 列（纯前端） | ⬜ | |
| B1 | 后端 `TrendPoint.Cost` + 累计 | ⬜ | |
| B2 | 前端 `CostTrendChart` + 主页 / 分析页接线 | ⬜ | |
| C1 | `service/selfheal.go` 判据与改写（表驱动） | ⬜ | |
| C2 | `BalanceChat` 内层自愈重试 + 记账 | ⬜ | |
| C3 | 配置键 `self_heal_policy` | ⬜ | |
| C4 | e2e：`stub-picky` 行为 + 用例 | ✅ | `e4dffed` |
| C5 | 实测报错校验轮（等用户样本） | 🟡 样本①已判定，见 §3.9 | |
| C6 | bridge 搬运 tool_result 图片 + 运行时降级 + 错误透出（§3.10） | ⬜ | |
| C7 | `response_format`→`output_format` 翻译（§3.11 首轮审计逮到的第二条过期前提） | ⬜ | |

---

## 一、功能 A：下钻表显示 tokens

**现状与根因。** 数据全都在，只有表格没显示：

- 后端 `GroupStat.TotalTokens`（`service/stats.go:397`）在 `groupAcc.count`（`stats.go:933-961`，
  累计在 :946）按行累计，六个分组维度（含联合维度与 `ByKey` 合并路径 `mergeGroupStats`
  `stats.go:1253-1284`）全部下发；TS 镜像 `GroupStat.totalTokens`（`webui/src/lib/api.ts:517`）
  也在。
- 下钻表 `BreakdownView`（`webui/src/routes/analytics-views.tsx:114-198`）目前 10 列
  （表头 :126-145，行体 :172-192），没有 tokens；`Prompt` / `Completion` / `Cached` /
  `Cost` 四个字段同样已下发未展示。
- 模型性能视图（`ModelsView`）**已有** `totalTokens` 与 `cost` 列（渲染
  `analytics-views.tsx:657-658`，排序键 `MODEL_SORT_KEYS` `analytics.ts:437-447`），
  措辞与格式化直接沿用即可。

**方案。** 后端零改动，前端三处：

1. `analytics-views.tsx` 的 `BreakdownView` 加一列 **Token**，插在「缓存命中」之后
   （token 语义相邻）：表头 `breakdown.columns.tokens`，单元格
   `formatNumber(g.totalTokens)`（`format.ts:21-24`，表格用精确千分位，不用
   `compactNumber`——下钻表其余数值列都是精确值）。`title` 悬浮给构成：
   `输入 formatNumber(g.prompt) / 输出 formatNumber(g.completion)`。
2. `min-w-[880px]`（:123）加宽到 `min-w-[960px]`；:120-121 的「十列」注释改为十一列。
   下钻表不支持排序（现状如此），Token 列同样只是展示，不引入 `MODEL_SORT_KEYS` 机制。
3. i18n 三份 locale 的 `analytics.json` 加 `breakdown.columns.tokens`
   （措辞沿用 `models.columns.totalTokens` = "Token"）。`i18n/locales.test.ts` 的键集
   一致性测试会把门。

**刻意不做**：下钻表不加 cost 列（用户只点了 tokens；cost 列数据同在，将来加是一行
表头一行单元格的事）；不加 prompt/completion 独立列（悬浮 title 已给构成，十列变十四列
对横向滚动不友好）。

**测试。** `analytics.test.tsx`：mock 的 `getStats` 分组数据带非零 `totalTokens`，
断言表头出现 Token 列、单元格值与 `formatNumber` 输出一致、悬浮 title 含输入/输出构成。

---

## 二、功能 B：主页与下钻新增成本趋势

**现状与根因。**

- 主页趋势区两张图（请求 / Token，`home.tsx:253-260`），分析页 `TrendView` 同样两张
  （`analytics.tsx:330-361`）。都没有成本。
- 后端 `TrendPoint`（`stats.go:365-380`）**没有 Cost 字段**——这是后端唯一缺口。
  单条成本 `Cost(l)`（`stats.go:255-263`，三档价 × 三类 token / 1e6）与 KPI 累计
  （`stats.go:599`）、币种（首条非空，`stats.go:600-603`）都已就绪。
- 空桶由 `normalizeTrendSeries`（`stats.go:825-865`）与 `fillTrendWindow`
  （`stats.go:1156-1177`）以零值 `TrendPoint{Ts: ts}` 构造，加字段后自动为 0，
  这两处不用动。

**方案。**

### B1 后端（`service/stats.go`）

1. `TrendPoint` 加 `Cost float64 \`json:"cost"\``（:365-380），注释写明口径沿用
   `Cost(l)` 的三档计价、单位由 `KPI.Currency` 决定（桶内不带币种）。
2. 时间序列累计处（:632-637 一段）加一行 `p.Cost += Cost(l)`。
3. `service/stats_test.go`：既有趋势对拍用例补 cost 断言（含缓存读按 `cache_read_price`
   计价的用例，防口径退回 `Σ单价` 的老错误——`Cost()` 注释里点过 dashboard 原实现的
   量纲错误）。

### B2 前端

1. `webui/src/lib/api.ts` 的 `TrendPoint` 加 `cost: number`（:484-498；文件头注释
   要求与后端 json tag 一一对应）。
2. `trend-charts.tsx` 新增 `CostTrendChart({ data, currency })`，**单系列面积图**：
   - 颜色 `seriesVar(4)`（琥珀）。已占用槽位：请求趋势 5/7/3、Token 趋势 0/2/1
     （`trend-charts.tsx:43-45`、`:110-113`），4 号空闲，且琥珀与本项目"铜金 = 强调色"
     的语义一致（成本是要被注意的量）。
   - Y 轴 `currencySymbol(currency) + compactNumber(v)`（`format.ts:70-74` 现成）；
     tooltip 用 `formatCost(v, currency)`（四位小数——单桶成本常在 0.01 量级，
     `toFixed(4)` 正是为它设的）。
   - 单系列**不画 legend**（标题即系列名）；X 轴照旧 `bucketLabelFormatter`。
   - **独立成图，绝不与请求数 / Token 共轴**——双轴是本项目图表规范头号禁项
     （`trend-charts.tsx:26-31` 注释、`home.tsx:251-252`）。
   - `currency` 取 `stats.kpi.currency`；空币种（历史行无币种）时 `formatCost` /
     `currencySymbol` 已有兜底（裸数字），图表不额外处理。
3. 主页 `home.tsx`：趋势 grid（:253-260）加第三个 `Panel`，标题 `home:trend.cost`。
   三张图在 `xl:grid-cols-2` 下呈 2+1，实施时按 1600px 实测决定是否改三列，图表组件
   自身对布局无假设。
4. 分析页 `analytics.tsx` 的 `TrendView`（:330-361）同加第三个 Panel（标题
   `analytics:trend.cost`）。下钻（点分组名 → 并入筛选）后趋势视图随同一筛选重取——
   成本趋势因此天然支持"按模型 / 提供商 / Key / 请求名 / UA 切片"的下钻视角，
   这是「下钻新增成本趋势」的落点，不需要给每个分组单独开趋势接口。
5. i18n 三份 locale：`home.json` / `analytics.json` 各加 `trend.cost`（"成本趋势"），
   说明文案口径沿用 `home:kpi.cost_hint`（"按请求时的价格快照累计"）。

**测试。** `home.test.tsx` / `analytics.test.tsx` 断言新 Panel 与标题出现、空数据时
不渲染图（four-state 惯例）；`format.test.ts` 已覆盖 `currencySymbol` / `formatCost`
无需新增；Go 侧见 B1-3。

---

## 三、功能 C：转换器请求体不适应参数的自愈

### 3.1 问题定义

客户端请求经（同协议透传或跨协议桥接后）发给上游，上游因请求体里有它不认识的参数
而报 400。现状的行为链（`service/chat.go:299-315`）：读错误体 → `fail()` 记一条
error 日志 → `Delete` 移出候选 → 换下一家。参数不适应是**确定性、跟请求体走的失败**：
下一家若也报同样的 400，继续换；`MaxRetry` 耗尽后客户端拿到
`"All retry failed, trace ID: ..."`（`chat.go:358`），而上游的真实报错只在
`chat_logs.error` 里（`"status: 400, body: {...}"`，`chat.go:304`）。

**自愈**：识别这类错误，把被点名的参数从**实际发出的请求体**里删掉，**同一家上游**
立即重发；删够预算仍失败才走既有的换家路径。

### 3.2 现状证据（调用链与重试语义）

- 每轮外层迭代从 `before.raw` 重建请求体：翻译（`chat.go:262-274`）→ 套
  `extra_body` + 删 `session_id`（`buildUpstreamBody` `chat.go:361-378`）→ `BuildReq`
  （:283）→ `client.Do`（:291）。**改请求体在这条链上没有先例之外的负担**——
  `BeforerOpenAI` 注入 `stream_options`（`service/before.go:136-145`）与
  `AnthropicRequestToOpenAI` 注入同款（`bridge/request.go:582-587`）就是 sjson 改写的
  既有模式。
- 同协议直连是**原样透传**（`bridge/openai.go:6-9` 的白名单只作用于跨协议桥接；
  providers 各文件只 sjson 覆写 `model`）。客户端带的非标准参数
  （`top_k` / `logit_bias` / 厂商私有字段）、`extra_body` 注入的键、桥接注入的
  `stream_options`（部分非标 OpenAI 兼容上游会拒）都会原样到达上游——
  这是"参数不适应 400"的主发生地。
- `Balancer` 接口只有 `Pop / Success / Delete / Reduce`（`balancers/balancers.go:12-17`），
  **没有把节点加回池子的 API**。所以"同一家重发"必须在 `Delete` 之前完成——
  自愈只能做成外层迭代内的内层闭环，不能"删了再 Pop 回来"。
- 熔断器把 `Delete / Reduce` 都计失败（`balancers/breaker.go:68-76`）。参数不适应
  不是上游健康问题：自愈成功的路径**不触发 Delete**，也就不污染熔断计数；自愈耗尽
  才 Delete，计一次，语义正确。
- `retryLog` channel 容量 = `MaxRetry`（`chat.go:124`）。内层自愈每次失败也走
  `fail()` 推一条，容量需同步放大（见 3.5）。
- `bridgeNotes.reset()` 在**外层**每轮开头（`chat.go:202`）：内层自愈期间记账不清空，
  自愈短码会留到最终成功那条日志上——正是想要的（"产出这次响应的那一家做过什么
  改写"）。

### 3.3 设计立场：自愈与 bridge 既有判据体系的关系

`bridge` 包对"目标协议没有的参数"已有一套判据（`bridge/bridge.go:24-37`）：

| 处置 | 条件 | 例 |
|---|---|---|
| 拒绝（`Unsupported`） | 丢了**模型要读的内容或调用方可观察的保证** | `n>1`、`logprobs`、JSON 保证 |
| 记账丢弃（`Note`） | 影响的是提示与参数，"软"损失 | `seed`、`top_k`、`strict` |
| 不记账 | 纯结构重排 | 消息合并 |

**本方案不改动这套判据**：`Unsupported` 依旧拒绝（`chat.go:264-271` 的换家路径不变）。
理由写在 `bridge.go:122-130` 的注释里——调用方看不出差别的东西不能静默丢。
自愈判定的输入是**上游的显式拒绝**（它点名了这个参数且拒绝服务），与"网关自己猜
上游不认识"是两种置信度完全不同的事实。上游拒收后删参重发，调用方拿得到答案、
日志里有记账，这与"静默丢"划清了界限。

同理，`top_k` 在跨协议桥接时已被 Note 丢弃；自愈覆盖的是**其余所有路径**（同协议
透传、`extra_body` 注入、桥接产物里目标上游仍不收的字段如 `stream_options`）——
一个机制統一兜底，判据只认上游的实际反应。

### 3.4 判据（哪些 400 可以自愈）

三层判定，全部通过才动请求体：

1. **状态码** ∈ {400, 422}。OpenAI / Anthropic / Gemini / vLLM 对未知参数都用 400，
   FastAPI 系网关默认 422。**只认这两个**：401/402/404/429/5xx 与参数存在性无关，
   一律走既有路径。
2. **错误体短语类**属于"参数不认识/不支持"，且**能提取出参数名**。错误体先按 JSON
   解（三种信封：OpenAI `{"error":{"message"}}`、Anthropic
   `{"type":"error","error":{...}}`、Gemini `{"error":{"message","status"}}`；message
   可能是数组，逐项拼），解不出按原文匹配。首版模式表：

   | 来源风格 | 错误样例 | 提取模式（捕获组 = 参数路径） |
   |---|---|---|
   | OpenAI | `Unknown parameter: 'top_k'.` | `(?i)unknown parameter:?\s*'([\w.\-]+)'` |
   | OpenAI 兼容第三方 | `Unrecognized request argument supplied: logit_bias` | `(?i)unrecognized request arguments? (?:supplied|provided):?\s*'?([\w.\-]+)'?` |
   | Pydantic 系网关（vLLM 等） | `messages.0.meta: Extra inputs are not permitted` | `(?i)'?([\w.\-]+)'?\s*:\s*extra inputs are not permitted` |
   | Pydantic 系网关（括号式） | `Extra inputs are not permitted (path: top_k)` | `(?i)extra inputs are not permitted[^\n]*path[=:]\s*'?([\w.\-]+)'?` |
   | Anthropic | `Unsupported parameter: 'metadata'` / `top_k is not supported by this API` | `(?i)unsupported parameter:?\s*'([\w.\-]+)'`；`(?i)([\w.\-]+) is not supported by this (?:model|api)` |
   | Gemini | `Unknown name "top_k" at <input>` | `(?i)unknown name "([\w.\-]+)"` |
   | 通用 | `field 'foo' is not supported` | `(?i)(?:field|parameter) '([\w.\-]+)' is not supported` |

   模式表是**数据不是逻辑**：新增一种上游措辞 = 加一行正则 + 一条表驱动用例。
   §3.9 的校验轮就是等用户拿真实报错来逐行补这张表。
3. **删得了**：提取出的路径必须满足——
   - 合法形状：`^[A-Za-z_][\w.\-]*$` 兼容点路径（`messages.0.foo` 这种数组下标
     sjson 直接支持），长度 ≤ 128；
   - **在实发请求体里存在**（`gjson.GetBytes(sent, path).Exists()`）——上游点名的
     参数如果不在我们发的 body 里，说明它抱怨的是别的（形状/嵌套），删无可删，
     判为不可自愈；
   - **非保护字段**：路径**恰好等于** `model` / `messages` / `stream` / `tools` /
     `system` / `contents` / `input` 之一时不自愈（整删消息流或改流式开关从来不是
     "自愈"）。嵌套子路径（如 `messages.0.metadata`）允许删——删的是消息里的一个
     非标准子字段，消息本体保留；
   - 未重复：同一参数本轮已删过再被点名 → 不可自愈（防上游反复报同一个名字的
     死循环）。

**明确不自愈**（短语类不匹配，天然被排除，写进测试当负控）：
上下文超长（`context length`）、`max_tokens` 超上限、值的类型/范围错误
（`Invalid type for 'temperature'`——参数存在但值不对，删了就改语义）、
鉴权 / 配额 / 模型不存在、`body is not json`、以及一切提取不出参数名的报错。

### 3.5 挂载点与控制流（`service/chat.go`）

外层循环（换家）结构不变；在**每轮迭代内部**，把「BuildReq → Do → 非 2xx 判定」
包成内层闭环：

```
外层每轮（chat.go:166-356 现有结构）:
    Pop → provider/关联 → chatModel/client → bridgeNotes.reset → 翻译 → buildUpstreamBody
    ↓ 得到 rawBody（实发上游协议的字节）
    drops := {}            // 本轮已删参数，map[path]bool
    healed := 0
    内层 loop:
        BuildReq(headers 不变, ProviderModel 不变, rawBody)      // chat.go:283
        res := client.Do(req)                                    // :291
        网络错误 → fail + Delete + 跳出内层（既有路径不变）
        res.StatusCode == 200 → 跳出内层 → ErrorMatcher / bridgeResponse /
                                Success / return（:317-354 一字不动）
        非 200:
            读 byteBody（:300-303 现有代码）
            if 判据三层全过 && healed < MaxDrops:
                drops[path] = true; healed++
                rawBody = sjson.DeleteBytes(rawBody, path)
                bridgeNotes.record(self_heal_dropped:<path>)
                log.Retry = retry + healed          // 自愈也是一次真实上游调用
                fail("status: %d, body: %s (self-heal dropped %s)", ...)
                res.Body.Close(); continue 内层      // 同一家、同一翻译产物，只重发改过的 body
            else:
                fail（:304 原样）; 429→Reduce / 其余→Delete（:306-312 原样）
                跳出内层 → 外层 continue
```

要点：

- **改的是实发字节，不重建**。内层重发不重跑翻译 / `extra_body`——同一家上游的
  翻译产物是确定的，对它 sjson 删参再发即可。外层换家后从 `before.raw` 重建，
  drops **不跨家携带**（A 家不收的参数 B 家可能收；每家自己用一次 400 换取判据，
  代价可接受，见"刻意不做"）。
- **预算**：`MaxDrops = 3`（一家最多删 3 个参数、多发 3 次请求）。绝大多数真实
  场景一个请求至多带一两个非标参数。
- **`retryLog` 容量**：`chat.go:124` 的 `make(chan, MaxRetry)` 放大为
  `MaxRetry * (1 + MaxDrops)`——内层每次失败都推一条，通道打满会阻塞转发路径。
- **`Retry` 计数**：自愈尝试是真实的上游调用，计入 `log.Retry`（失败行与最终
  成功行都是），KPI 的 `AvgRetries`（`stats.go:356-361`，"平均每请求重试次数"）
  因此如实反映自愈开销。
- **记账**：`BridgeNotes`（`service/bridge.go:105-163`）的 `record` 收
  `[]bridge.Note`，自愈短码在 service 侧定义为
  `bridge.Note("self_heal_dropped:" + path)`——按整串去重，不同参数各自一条；
  外层 `reset()` 保证只挂产出响应的那家。日志详情抽屉（BridgeNotes 列）与
  `slog.Warn("protocol bridged")`（`handler/chat.go:153-156`）的既有展示通道
  自动带上它，无需新增 UI。
- **流式无关**：自愈只发生在首个字节转发之前（`BalanceChat` 返回前），流式 200
  中途 error 的既有语义（不可重试，`process.go` 标 error）不变。

新代码落 `service/selfheal.go`（判据与改写，纯函数）：

```go
// DetectParamComplaint(status int, body []byte) (path string, ok bool)
//     状态码 + 信封解析 + 模式表 → 参数路径
// PlanDrop(sent []byte, path string, dropped map[string]bool) bool
//     形状 / 存在性 / 保护清单 / 去重 四道闸
// ApplyDrop(raw []byte, path string) []byte                 // sjson.DeleteBytes
```

### 3.6 配置

`configs` 表新增键 `self_heal_policy`（`models/config.go` 加常量与结构体，读写沿用
`log_cleanup_policy` 同一条路）：

```go
type SelfHealPolicy struct {
    Enabled  bool `json:"enabled"`   // 默认 true
    MaxDrops int  `json:"max_drops"` // 默认 3，<=0 用默认
}
```

默认开启的论证与 `ModelAutofillPolicy.Enabled`（`models/config.go:44-51`）同构但
更弱一手：自愈改变的是"这份请求会不会被服务"，不碰落库数据；关掉它的代价是
回到现状（全换家后 500）。键不存在 = 默认值，**v1 不做管理页 UI**（文档记录键名
与 JSON 形状，系统页入口留作后续）。

### 3.7 测试矩阵

**`service/selfheal_test.go`（表驱动，新增代码全覆盖）**：

- 模式表逐条正例 × 三种信封（OpenAI / Anthropic / Gemini）+ 非数组与数组 message；
- 负例：上下文超长、`Invalid type`、`max_tokens` 超限、鉴权 401（状态码不过）、
  429/500（状态码不过）、`body is not json`、提取名为空、名不在实发体、保护字段
  （`messages`、`stream`）、嵌套保护例外（`messages.0.metadata` 可删）、同参数
  二次点名、路径超长 / 非法字符；
- `ApplyDrop`：顶层键、嵌套点路径、数组下标路径。

**`service/chat` 级（httptest 伪上游，照 `failed_io_test.go` 的建库方式）**：

- 正路径：伪上游第一次 400 `Unrecognized request argument supplied: top_k`、
  第二次 200 → 断言客户端拿到 200；伪上游收到的第二次 body **不含** `top_k` 且
  其余键原样；`chat_logs` 多了失败行（error 原文含 400 body 与 self-heal 标记），
  成功行 `BridgeNotes` 含 `self_heal_dropped:top_k`、`Retry` 计数含自愈次数；
- 预算耗尽：伪上游连报**不同**参数名 4 次 → 第 3 次删后仍 400 → 走 Delete 换家，
  行为与现状一致；
- 负控：`boom` 风格 400（`stub_upstream.py:332-338` 的措辞不匹配任何模式）→
  不自愈、不多发请求、既有断言全绿；
- 429 降权路径回归：429 不进自愈。

**e2e（`e2e/stub_upstream.py` + `run_matrix.py`）**：

- 桩上游加行为 `picky`（模型名 `stub-picky`）：body 带 `top_k` 时回
  OpenAI 风格 400（anthropic 路径回 anthropic 信封），否则正常；
- `run_matrix.py` 加用例：客户端带 `top_k` 打 `stub-picky` → 三处核对
  （客户端拿到 200 / 桩上游第二次收到的 body 无 `top_k` / 日志记了失败行 +
  成功行带短码）——沿用该文件头"三处缺一不可"的既有论证；
- `boom` 用例保留，充当全局负控。

### 3.8 刻意不做

- **`bridge.Unsupported` 不降级为丢弃**（3.3 的立场）。样本 ① 引出的
  tool_result 图片一支**升级为搬运**（§3.10）——搬运不是丢弃，判据仍是
  "目标协议装得下就搬"。
- **不做请求前预净化 / 参数黑名单配置**：未知厂商行为无法穷举，运行时判据
  （上游实际反应）是唯一可靠来源；配置面是猜的。
- **drops 不跨 provider 记忆**：跨家共享会引入"上游类型 → 参数集"的假设，
  省下的是一次 400 的延迟，赔上的是正确性。
- **流式 200 中途 error 不重试**（响应已在转发，现状不变）。
- **`Invalid type` / 值域类 400 不自愈**：删参数改的是"参数在不在"，值错误删参
  等于替客户端改语义。

### 3.9 校验轮（实测报错逐条判定）

用户每提供一条真实报错，逐条走 `DetectParamComplaint` 的表驱动路径判定：

- 命中现有模式 → 补一条用例固化；
- 新措辞 → 模式表加一行正则 + 用例（数据变更，不动逻辑）；
- 提取不出参数名 / 属值域错误 → 归入"明确不自愈"，同样补负控用例——
  **判不了比乱判好**，这是 3.4 三层判据的兜底立场。

**样本 ①（2026-10-24，用户提供）**

```
bridge request: cannot bridge to the upstream protocol:
tool_result.content: the openai protocol cannot carry a "image" block inside a tool result
```

判定：

1. **类别**：`bridge.Unsupported` 预检拒绝（`bridge/request.go:835-839`
   `anthropicToolResultText`），发生在翻译阶段——请求**从未发往上游**。功能 C 的
   判据挂在非 2xx 分支（`chat.go:299-315`），结构上不可能看到这条错误；模式表
   逐条核对也无一命中（无 "unknown/unsupported parameter" 类短语）。**与自愈
   机制零交互，判据表不需要为此加行。**
2. **设计归属**：按 §3.3/§3.8 的立场**不自愈**——删掉工具结果里的截图是内容级
   损失（模型瞎着往下推），不是参数不适应。这个判定维持不变。
3. **但样本暴露了两个真缺口**，见 §3.10：拒绝的前提已过时（双方协议现在都装得下
   工具结果图片，应搬运而非拒绝）；且预检拒绝的原因对客户端不可见（客户端只看到
   `All retry failed` / `balancer pop err`）。

### 3.10 C6：工具结果图片的搬运、降级与错误透出（样本 ① 引出）

**根因：bridge 的前提过时了。** `anthropicToolResultText` 的注释
（`request.go:819-820`）写着"OpenAI 的 tool 消息正文是字符串，装不下它"；反向
转换的注释（`request.go:245-246`）同款前提。两点事实已变：

- OpenAI Chat Completions 自 2025 起支持 `role:"tool"` 消息的 `content` 为**块
  数组**（含 `image_url`，工具结果图片，gpt-4.1 / o 系列可用）——代理侧基建
  全都在：`openAIImagePart`（`request.go:971`）已会把 Anthropic 图片源转成
  `image_url` 块（user 消息在用，`:719-725`），`OpenAIMessage.Content` 是
  `RawMessage`（"字符串、块数组或 null 三选一"，`openai.go:39-40`）装得下数组。
- Anthropic 的 `tool_result.content` 本就收块数组（text / image 块）——Claude
  Code 截图进工具结果走的就是这条路。即 O→A 方向（`:244-250`）也在拒收
  双方协议都装得下的东西。

按 bridge 自己的最高判据——"能搬却不搬"比"搬不了"更糟（run_matrix 用例 10 的
立论）——正确处置是**搬运**，不是拒绝，更不是自愈删图。

**C6a 搬运（bridge 层，双向）**

- A→O：`tool_result` 块数组含图片时，`role:"tool"` 消息的 `content` 改为块数组
  `[text…, image_url…]`（复用 `openAIImagePart`）；`is_error` 前缀逻辑保留，
  拼在首个 text 块前。记 Note `carried_tool_result_image`——第三方 OpenAI 兼容
  上游未必支持这个形状，记账让运维看得见发出了什么。
- O→A：`openAIContent` 解析出的 image 块转成 `tool_result.content` 的 image 块
  （不再 `textOnlyParts` 拒绝），同记 Note。

**C6b 运行时降级（与功能 C 同构，挂同一处内层重试）**

上游若不收 tool 块数组，回的是 400 类型错（如 `Invalid type for 'content':
expected a string`）——按 §3.4 这**不进**通用删参（类型错误类，刻意排除；正确
动作也不是删参数而是压形状）。给它一条**协议感知的降级规则**：识别 tool content
类型错 → 把 tool 消息 content 压回纯文本字符串（丢图、留文本）→ 记
`dropped_tool_result_image` → **同家重发**。认识论与功能 C 一致：网关不猜上游
支不支持，让上游的实际反应裁决——能收就收完整形状，明确拒收才降级且留痕。

**C6c 错误透出（独立小改，先行落地）**

`BalanceChat` 全部尝试耗尽时的最终错误（`chat.go:358`）与 `balancer pop err`
（`:176-179`）带上**最后一次失败的真实原因**（预检拒绝或上游报错原文，截断防
爆破）。现状客户端只拿到 `All retry failed, trace ID: …`，真实原因只在
`chat_logs.error` 里——样本 ① 的用户视角就是"一句没用的 500"。

**测试**：bridge 表驱动双向用例（tool_result 带图往返、`is_error` + 图、
base64/url 两种源）；stub 新成员（如 `picky-toolimg`：tool content 为数组即
400 `Invalid type for 'content'`）驱动 C6b 的 e2e；`selfheal_check.py` 加一条
「降级后成功」用例；run_matrix 既有 23 条回归。

**实施顺序**：C6c 独立可先落；C6a 是 bridge 保真升级（改的是拒绝分支，风险低）；
C6b 依赖 C2 的内层重试结构，排在其后。

### 3.11 预检先进性的审计方法与首轮结果（2026-10-24）

**问题的一般化**：预检拒绝（`bridge.Unsupported`）每条都是「目标协议装不下 X」的
断言。断言会随上游 API 演进过期，而仓库里没有任何机制能发现它过期——桩上游与
e2e 校验的是与**我们自己假设**的一致性（循环论证），`tool_result 图片`这条过期
前提能在 100% 通过的 e2e 里活到今天，就是这个原因。

**确定先进性的三层方法**：

1. **枚举与分类**（仓内一次 grep）：`grep "&Unsupported{" bridge/` 共 40 处，
   按 Reason 文本分两类——**输入形状错**（"not a JSON request"、"image part
   without a url"、"tool choice without a name" 等约 33 处：拒绝的是畸形输入，
   不对上游能力做断言，**不会过期，无需审计**）与**能力断言**（7 条，会过期）。
2. **外部真相源核对**（唯一权威途径）：逐条对官方 API reference + changelog
   核对并记录日期。这是主动通道；用户实测报错（§3.9 的 C5 循环）是被动反证通道。
3. **运行时事实替代预测**（设计规则，管未来）：对「OpenAI/Anthropic 兼容」的第
   三方上游，官方文档说了也不算（vLLM / OpenRouter / 自建网关实现各异）。凡是
   **按上游实现而异**的前提，不该做成预检硬拒绝——搬运 + 运行时降级（C6 模式），
   让上游的实际 400 裁决。**保证级契约是例外**（如 JSON schema）：不许静默降级，
   失败时透出真实原因。

**首轮审计结果**（2026-10-24，官方文档核对）：

| # | 断言（拒绝点） | 位置 | 核实结果 | 处置 |
|---|---|---|---|---|
| 1 | OpenAI tool 消息装不下图片 | `request.go:836`（A→O） | **过期**：Chat Completions 的 `role:"tool"` content 支持块数组含 `image_url`（视觉模型） | C6a 搬运 + C6b 降级 |
| 2 | Anthropic tool_result 收不了非文本 | `request.go:244-250`（O→A） | **过期**：官方文档 tool_result content 可为块数组，块类型含 text/**image**/document/search_result | C6a 反向搬运（document/search_result 在 OpenAI 侧无对应物，仍拒绝） |
| 3 | Anthropic 没有 JSON mode | `request.go:95`（response_format 拒绝） | **过期**：2025-11 起原生结构化输出，参数 `output_format`（JSON mode + JSON Schema 两档），Sonnet 4.5 / Opus 4.1 起支持 | **新工作项 C7**（见下） |
| 4 | Anthropic 不返回 logprobs | `request.go:90/92` | 仍成立：官方无此能力，社区仍在请求 | 维持拒绝 |
| 5 | Anthropic 无 penalty 参数 | `request.go:86/88` | 仍成立：官方参数表无 presence/frequency_penalty | 维持拒绝 |
| 6 | Anthropic 单候选（n>1） | `request.go:84` | 仍成立：无 n 参数 | 维持拒绝 |
| 7 | Anthropic 首条消息须为 user | `request.go:275` | 仍成立：官方要求第一条为 user（末条可 assistant 作 prefill） | 维持拒绝（结构性规则） |

**C7：`response_format` → `output_format` 翻译**（样本①引出的审计又逮到一条）：
Anthropic 侧有原生结构化输出后，O→A 的 `response_format: json_schema/json_object`
应翻译为 `output_format`（字段形状按官方文档核对后映射），让带结构化输出要求的
请求**能用** Anthropic 上游，而不是一律预检拒绝。与 C6b 的关键差别：这是
**保证级契约**——上游（老模型 / 兼容网关）不认 `output_format` 而报 400 时
**不降级、不静默丢**（丢了等于装作答应了，正是 bridge 立场反对的），走 C6c
把上游真实报错透出给客户端。记 Note `translated_output_format`。

**可重复性纪律（本轮立规）**：凡能力断言类的拒绝点，注释里记
「断言内容 + 依据来源 + 核对日期」，与上表同步维护（落在
`docs/protocol-bridge.md` 或本表）；上游 changelog 出新特性事件时重审对应条目。
下一轮审计是核一张表，不是重新考古。

---

## 四、数据库兼容

**零 schema 变更。**

- 功能 A / B：`TrendPoint.Cost` 是 JSON 响应字段，不是列；不新增列 / 索引 /
  `gorm` tag / `AutoMigrate` 目标。tokens 与价格列（`models/model.go:110-114`）
  早在库里，stats 全量加载后内存累计（`LoadChatLogs`，扫描上限
  `StatsScanLimit = 200_000`），不需要新索引。
- 功能 C：只加代码与 `configs` 表**一行 KV**（运行时写入，非 schema；与
  `log_cleanup_policy` 同性质）。

---

## 五、覆盖率

- 后端：全仓 70% 门禁（`make cover`）不回退；`service/selfheal.go` 与
  `TrendPoint.Cost` 累计全部表驱动覆盖；`BalanceChat` 内层闭环由 chat 级伪上游
  测试覆盖。
- 前端：A 层 100% 门禁范围无新增纯逻辑模块（A/B 是组件与类型改动；i18n 键集由
  `locales.test.ts` 把门）；页面按四态惯例补 `home` / `analytics` 断言。
- Windows：`go test` 权限不足时 `GOCACHE=$PWD/.gocache`，跑完删目录。

---

## 六、验证矩阵

```
GOCACHE=$PWD/.gocache go test -count=1 ./...        # 跑完删 .gocache
cd webui && corepack pnpm run lint && corepack pnpm run test:coverage && corepack pnpm run build
go vet ./...                                        # 前置：先 pnpm build（//go:embed）
python e2e/run_matrix.py --upstream stub
python e2e/selfheal_check.py                        # 功能 C 的可执行验收车（12 条用例）
```

功能 C 的用例矩阵、基线实测与逐条预计行为见 `docs/plans/usage-insights-test-plan.md`
（验收车已随设计落地：S 组 8 条应自愈用例在实现前红、落地后必须绿；N 组 4 条判据
边界在任何时候都必须绿）。

真机走查清单（用真实库备份，`LLMIO_DB_PATH` 指过去）：

1. 分析页 → 下钻 → Token 列出现、悬浮显示输入/输出构成；
2. 主页 / 分析页 → 成本趋势图渲染，Y 轴带币种符号，tooltip 四位小数，空窗口为空态；
3. 配一个 `picky` 类真实上游（或伪上游）：带非标参数的请求自愈成功，日志详情
   BridgeNotes 含 `self_heal_dropped:*`，失败行与成功行成对出现；
4. `boom` 类 400 行为与现状一致（不自愈）。

---

## 七、风险与回滚

- **删参改变模型行为**（如 `top_k` 真的生效过）：只在"上游明确拒收"之后发生，
  替代方案是整单失败；记账短码 + 失败行落库保证可观测；`self_heal_policy`
  一键关。回滚 = 关配置或回退 C2 提交。
- **模式误伤**：三层判据里"名在实发体中存在"是最硬的闸——上游点的名我们没发过
  就绝不删；保护清单挡住整删核心字段。误伤面收敛到"上游点名且我们确实发了的
  非保护参数"，这正是要删的东西。
- **重试放大**：最坏情况每家多 3 次请求；`MaxRetry × (MaxDrops+1)` 有通道容量
  兜底，超时 timer（`chat.go:164`）总量兜底。
- **A/B 是纯展示改动**，风险集中在布局（1600px 实测）与 i18n 键集，回滚单提交。

---

## 八、实施顺序与提交切分

> 分步实施册（每步的代码级改动、完整测试用例清单与逐步验证命令）：
> `docs/plans/usage-insights-impl.md`，共 10 步提交序列，与本节顺序一致。

1. **A1**（独立提交，最小）：下钻 Token 列 + i18n + 测试。
2. **B1 → B2**（两个提交，先后端后前端）：`TrendPoint.Cost` → 图表与接线。
3. **C1 → C2 → C3**（每步独立提交，C1 先行钉死判据语义，C2 只做接线）：
   `selfheal.go` → `BalanceChat` 内层闭环 → 配置键；C4 的桩与验收车已就绪。
4. **C5**：实测报错逐条判定（样本 ① 已判定并引出 C6，后续样本继续走 §3.9）。
5. **C6c → C6a → C6b**：错误透出（独立小改，可先行）→ tool_result 图片搬运
   （bridge 保真升级）→ 运行时降级（依赖 C2 的内层重试结构）。

每步提交前跑第六节命令；A/B 与 C 无相互依赖，可分别评审分别合并。
