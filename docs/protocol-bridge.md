# OpenAI ↔ Anthropic 协议互转（`feat/protocol-bridge`）

> 拟稿 2026-10-01 · 探索阶段（**只做纯函数层，不动转发路径**）
> 前置：`docs/merge-dashboard-plan.md`、`providers/`、`service/chat.go`

## 1. 要解决的问题

llmio 现在是**透明转发**：客户端发什么 body，就原样带给上游，只改 `model` 字段
（`providers/*.go` 的 `BuildReq` 用 `sjson` 做这件事）。这在「客户端协议 = 上游协议」
时是对的，也最省事。

OpenCode 那类上游把这条路堵死了一半：

- 有的上游**只说 OpenAI**（`/v1/chat/completions`），不接受 `/v1/messages`
- 有的上游**只说 Anthropic**（`/v1/messages`），不接受 `/v1/chat/completions`

而客户端也分成两派。于是出现四个象限，其中两个现在走不通：

| 客户端 ↓ / 上游 → | OpenAI | Anthropic |
|---|---|---|
| OpenAI 格式 | ✅ 现状 | ❌ **要翻译** |
| Anthropic 格式 | ❌ **要翻译** | ✅ 现状 |

这两个 ❌ 就是本分支要做的事。注意它**不是**「把所有请求统一成一种内部格式」——
那是重写整个转发层；这里是**只在两端协议不同时**插一层翻译，同协议路径一个字节都不改。

## 2. 三条原则

转换层最怕的不是做错，而是**做错了还看不出来**。因此三条原则按优先级排：

1. **要语义等价，不要"差不多"**。乘数、温度、max_tokens 这类会影响输出的参数，
   能精确映射就映射；不能的**不许猜**。
2. **模型要读的内容搬不过去就拒绝，提示与参数丢了才记账**。
   - 客户端要 `n=4`（四份候选），上游只给一份 → 结果里少三份，调用方看得见 → **拒绝**
     （返回错误，路由器当作"这家服务不了"，换下一家，与 `BuildReq` 失败同一条路）
   - 工具结果里夹着图片（截图类工具天天这么发），而 OpenAI 的 tool 消息正文只有
     字符串 → 也要**拒绝**：模型少看一张图，答案就不一样了，而调用方从响应里看不出来
   - 客户端带 `user: "abc"`（滥用追踪提示）、`seed`（尽力而为的确定性提示）、
     `top_k`、思考块 → 影响的是"软"的东西，换个上游本来也常常没有 → 丢，记 `Note`
   - 判据是**调用方能否从响应里发现**，不是字段大小
3. **有损但可以继续的，记账，不静默**。历史里有一条悬空的 `tool_result`（OpenAI 允许，
   Anthropic 会 400）这类情况，既不该整体拒绝（真实客户端天天这么发，拒了这功能就没用），
   也不该悄悄改掉：转换返回一份 `[]Note`，调用方记进日志。**"改了什么"本身是可观测的**。

**第三种处置：不记账的纯结构重排**（合并连续同角色消息、把同一消息里的多个文本块并成
一段）。它们一个字不多一个字不少，而且在 Anthropic 侧**没有表示法**——任何转发都必须
做。之所以要单独拎出来：连续的 tool 消息合并、Claude Code 每轮发好几个文本块，是几乎
每次请求都会发生的事；这类 `Note` 天天出现，看的人就会学会无视整个列表，真正有损的那
几条也就没人看了。判据是"内容变没变"，不是"形状变没变"。

`Note` 与 `error` 的分工就是第 2 条与第 3 条的边界。

## 3. 请求侧映射

### 3.1 OpenAI → Anthropic

| OpenAI | Anthropic | 说明 |
|---|---|---|
| `model` | `model` | 由 `BuildReq` 覆盖，转换不管 |
| `messages[].role=system` | `system`（顶层） | 多条 system **按出现顺序用 `\n\n` 拼接**；出现在中途的 system 一并上提（见 3.3） |
| `messages[].role=user`（字符串） | `content:[{type:text}]` | |
| `messages[].content[].type=text` | `{type:text,text}` | |
| `content[].image_url.url = data:...` | `{type:image,source:{type:base64,media_type,data}}` | 拆 data URL；**解析不出 media_type 就拒绝**（不猜 `image/png`） |
| `content[].image_url.url = http(s):...` | `{type:image,source:{type:url,url}}` | 需要较新的 API 版本，记 `Note` |
| `messages[].role=assistant` + `tool_calls[]` | `content:[{type:tool_use,id,name,input}]` | `arguments` 是 JSON **字符串**，要解成对象；空串→`{}`；**解不出就拒绝**（上游收到空 `input` 比报错更难查） |
| `messages[].role=tool` | `user` 消息里的 `{type:tool_result,tool_use_id,content}` | 连续的若干条 tool 消息**必须合并进同一条 user 消息**，见 3.2 |
| `max_tokens` / `max_completion_tokens` | `max_tokens` | Anthropic **必填**；缺省时落 `DefaultMaxTokens`，并记 `Note`（见 4） |
| `stop`（字符串或数组） | `stop_sequences`（数组） | |
| `temperature` / `top_p` | 同名 | |
| `stream` | `stream` | |
| `tools[].function.{name,description,parameters}` | `{name,description,input_schema}` | `parameters` 原样带过（`RawMessage`），不解析 |
| `tool_choice:"auto"` | `{type:auto}` | |
| `tool_choice:"required"` | `{type:any}` | |
| `tool_choice:"none"` | **丢掉 `tools`** | Anthropic 没有"禁用工具"这一档；不传 tools 等价于此 |
| `tool_choice:{type:function,function:{name}}` | `{type:tool,name}` | |
| `response_format:{type:text}` | （丢） | 与默认行为一致 |
| `response_format:{type:json_object|json_schema}` | **拒绝** | Anthropic 没有 JSON 模式；静默丢掉等于让调用方以为拿到了保证 |
| `n>1` | **拒绝** | 见原则 2 |
| `presence_penalty` / `frequency_penalty` ≠ 0 | **拒绝** | Anthropic 无此参数 |
| `logprobs:true` / `top_logprobs` | **拒绝** | 同上 |
| `user` / `seed` / `top_logprobs:0` | （丢，记 `Note`） | 原则 2 |

### 3.2 tool_result 必须合并（最容易踩的一处）

Anthropic 要求 `tool_result` 块出现在**紧跟在带 `tool_use` 的 assistant 消息之后的那条
user 消息**里，且 `tool_use` 与 `tool_result` 一一对应。OpenAI 则是每条 tool 结果一条
独立的 `role:"tool"` 消息。并发调三个工具时，OpenAI 客户端会发三条连续 tool 消息——
逐条转成 user 消息会被 Anthropic 以"角色必须交替"拒掉。

因此转换时**把连续的 tool 消息合并成一条 user 消息**，`content` 里依次排三个
`tool_result` 块。

反过来（Anthropic→OpenAI）则是展开：一条 user 消息里的每个 `tool_result` 块各成一条
`role:"tool"` 消息，`tool_call_id` 就是 `tool_use_id`。

### 3.3 历史清洗（记 `Note`，不静默）

OpenAI 宽松、Anthropic 严，真实历史经常是前者：

| 情况 | 处理 | 记账 |
|---|---|---|
| 首条是 assistant | **拒绝** | — |
| 连续的同类角色 | 合并成一条 | 不记（纯结构，见原则 3） |
| assistant 的 `tool_use` 没有对应 `tool_result` | 补一条 `tool_result`，`is_error:true`，内容是说明文字 | `filled_missing_tool_result` |
| 孤儿 `tool_result`（没有对应 `tool_use`） | 丢弃 | `dropped_orphan_tool_result` |
| `tool_result` 里的非文本块（截图） | **拒绝** | — |
| 空白文本块 | 丢弃（Anthropic 拒空文本块） | `dropped_empty_text` |
| 转换后一块不剩的消息 | 丢弃 | `dropped_empty_message` |
| `tool_result` 的块与消息里其他内容的相对次序 | `tool_result` 一律挪到最前 | `reordered_tool_result` |

首条是 assistant 选**拒绝**而不是"前面补一条 user 消息"：补的那条要么是空文本块
（Anthropic 拒），要么得编一句话——而模型看得见编出来的东西，等于凭空塞给它一段它
从没见过的上下文。宁可说清楚这份历史转不过去，让路由器换一家。

补的 `tool_result` 用 `is_error:true` + 一句说明，而不是编一个成功结果：模型读到的是
"这个工具没返回"，而不是一个假数据。

配平（`tool_use` ↔ `tool_result`）必须是**一趟从左到右扫完所有轮次**，不能只看
"assistant 后面紧跟的那条消息"：连续的 tool 消息会被合并成同一条 user 轮，只看相邻一条
时，合并进来的第二份结果就成了没人检查的孤儿，原样发给上游直接 400。

## 4. `max_tokens` 的默认值是个有损决定

Anthropic 的 `max_tokens` 必填，OpenAI 的可选。绝大多数 OpenAI 客户端不填（用模型默认）。
两条路：

- **拒绝**：那么只要客户端不填 `max_tokens`，就永远用不了 Anthropic 上游——把功能的主要
  用途砍掉
- **补默认值**：`DefaultMaxTokens`（8192）。有损：本想生成 32k 的请求被截到 8k

取后者，理由是**截断是可观测的**（`stop_reason` 是 `max_tokens`，调用方看得见），
而拒绝是功能整体不可用。默认值取常量、可由上游配置覆盖，并记 `Note`。

## 5. 响应侧映射（非流式）

| OpenAI | Anthropic |
|---|---|
| `choices[0].message.content` | `content:[{type:text,text}]`（空串则不产生文本块） |
| `message.tool_calls[]` | `content:[{type:tool_use,id,name,input}]`，`arguments` 解成对象 |
| `finish_reason:stop` | `stop_reason:end_turn` |
| `finish_reason:length` | `max_tokens` |
| `finish_reason:tool_calls` | `tool_use` |
| `finish_reason:content_filter` | `refusal`（有损，记 `Note`） |
| `choices` 多于一条 | 只取 `index=0`，记 `Note`（配合请求侧的拒绝，正常不会出现） |
| `usage.prompt_tokens` / `completion_tokens` | `input_tokens` / `output_tokens` |
| — | `id` / `type:message` / `role:assistant` / `model` 由转换层补 |

反向同理。Anthropic 的 `thinking` 块**不进 OpenAI 的输出**：它是带签名的思考内容，
不进上下文也不展示；多轮里丢掉历史 thinking 是 API 允许的。这一条也记 `Note`。

## 6. 流式状态机

两个方向都是**有状态**的增量转换，这是这个功能里最容易写错的地方。

OpenAI → Anthropic：

- 第一个 chunk 到达时发 `message_start`（`id` / `model` 从 chunk 取）
- 文本增量：若当前没有打开的文本块，先发 `content_block_start(index=0)`；再发
  `text_delta`
- `delta.tool_calls[]`：每个 `index` 是一条**独立通道**（并发调用时会在多个 index 之间
  交错出现，不能假设顺序）。**参数片段攒在各通道的缓冲里，直到收尾才一次性变成
  `tool_use` 块 + `input_json_delta`**。Anthropic 的内容块一旦 `content_block_stop`
  就不能重开，边收边发的话交错的那两路参数会被搅进同一个块，客户端拼出来的 JSON 直接
  是坏的。代价是工具调用不是实时可见——客户端本来也要等 `finish_reason` 才去执行工具，
  这点延迟不损失什么
- **`[DONE]` 前没有 `finish_reason`**（工具调用流里常见）→ 按"有没有见过 tool_call"
  推断 `tool_use` / `end_turn`，**不能什么都不发**：不发 `message_delta` 客户端会一直等
- **收尾不在看见 `finish_reason` 的那一刻发生，而是等到 `[DONE]` 或上游断开**。开了
  `stream_options.include_usage` 的上游会把收尾拆成两段：先是带 `finish_reason`、
  `usage` 为空的那条，紧跟着才是 `choices` 为空、只带 `usage` 的那条。前一条一到就
  收尾的话，用量永远落在收尾之后被丢掉——而 llmio 记 Anthropic 用量只认 `message_delta`
  这一条（`service.ProcesserAnthropic`），输入 token 会被记成 0
- 流中间报错（`{"error":{...}}`）→ 转成 Anthropic 的 `error` 事件，**此后再不补收尾**：
  补出来的 `message_stop` 会让客户端把半截回答当成一次完整的生成

Anthropic → OpenAI 是逆过程：

- `message_start` → 一条 `delta:{role:"assistant"}` 的 chunk
- `text_delta` / `input_json_delta` → 各自的增量 chunk
- 内容块下标与 OpenAI 的 `tool_calls[].index` **不是一回事**：前者按块排列（中间夹文本
  块就会出现 0/1/2），后者必须从 0 起连续编号，要维护一张映射表
- `thinking` / `redacted_thinking` 块与 `thinking_delta` / `signature_delta` 没有对应物，
  丢掉并记 `Note`
- `message_delta` 的 `usage` 要并进最后一个 chunk（llmio 的 OpenAI 用量统计只认收尾那
  一条），末尾补 `[DONE]`

两边的收尾事件都保证**恰好发一次**，即使上游断流（`Close` 兜底）或出现重复的收尾标记。

## 7. 接线

翻译层是纯函数（`bridge` 包，只依赖标准库），**不碰转发路径**。接线是单独一步，落在
`service/bridge.go`（判断与记账）与 `service/chat.go`（转发路径上的三个接入点）：

- **谁进候选池**：`ServableTypes(style)` 决定 provider 查询的 `type IN (...)`。OpenAI 客户端
  的池子是 `openai / openai-res / anthropic`，Anthropic 客户端的池子是 `anthropic / openai`；
  `openai-res` 与 `gemini` 不参与（形状差得远，翻过去只会得到一个像模像样的错答案）。
  模型列表接口用同一份类型表，因此"能调用的模型"与"列表里能看见的模型"永远一致——
  否则 Anthropic 上游上的模型对 OpenAI 客户端根本不可见。

- **直连优先、转换为兜底**：`poolFor` 在池子里只要有一个说客户端话的上游，就**只留**
  它们。转换只在该模型下"没人说客户端的话"时启用。这样现有部署（上游与客户端同协议）
  的候选集与转换功能上线前完全一致，转换带来的语义损失不会悄悄落到本来直连的请求上。
  模型上的 `PreferDirect` 开关可以关掉这条：关掉后整池按权重摇，本协议与可转换协议的
  上游一起参与（给"我就要按权重分配"留的口子）。该列为 NULL（转换功能上线前建的模型）
  时按**开**处理，升级不会静默改变既有流量分配。

- **方向按候选项逐个判**：候选池里可能混着两种协议（开关关掉时是必然，开关打开时
  也可能——池子里没有本协议的，只剩 `openai-res` 与 `anthropic` 两家可转换的），所以
  `TranslatorFor(style, provider.Type)` 在每次 `balancer.Pop()` 之后重新判一次，
  而不是在循环外算一次。

- **请求体：先翻译，再套 `extra_body`**。顺序不能反——`extra_body` 挂在"模型 × 上游"的
  关联行上，是按**上游协议**写的；先套再翻的话，翻译层会把这些键当成不认识的字段丢掉。
  翻译失败（`Unsupported`）与 `BuildReq` 失败同路：`balancer.Delete` + 换下一家。

- **响应体：翻完再交出去**，且必须在转发与记录**之前**——客户端看到的和 ChatLog 记的
  都得是客户端协议（记录用的 processer 是按客户端协议选的）。流式走 `bridge.BridgedBody`，
  读挂或解不开时先发已翻好的字节，下一次 `Read` 再把错误透出去，不补收尾事件。

- **落库与提示**：`ChatLog.UpstreamStyle` 记上游协议（直连时等于 `Style`，不同即说明转过），
  `ChatLog.BridgeNotes` 记这次改过什么（逗号分隔的短码）。响应侧的记账要到流读完才完整，
  所以取值的时机是 `RecordLog` 里日志落库之前。日志页详情把短码译成人话展示；转发结束后
  还有一条 `slog.Warn("protocol bridged", ...)`，同协议路径没有记账，也就不会有这条。
- **重试时记账跟着尝试走**：一次请求可能试好几家上游，而记账描述的是**产出这次响应的那
  一家**——每次尝试开头清空（`BridgeNotes.reset`），失败的那次把自己的快照进它自己的
  重试日志。不这么做的话，前一家的记账会挂到最终这条日志上，读起来像"产出响应的上游丢了
  参数"。

`Processer` 一侧零改动：它收到的字节流已经是客户端协议。

## 8. 参考实现与它们的坑

看过这几个，坑列在下面（都写进了上面的设计）：

- **litellm #19061**：Anthropic 严格要求 `tool_use` ↔ `tool_result` 一一对应，而 OpenAI
  允许悬空调用与孤儿结果，直接转会让上游 400。对应 §3.3 的清洗
- **litellm #27468**：`function.arguments` 在转换里被 `model_dump()` 吃掉，`tool_use.input`
  变成 `{}`——工具"跑了"但什么参数都没带。对应 §3.1 里"解析不出就拒绝"
- **CLIProxyAPI #5310**：OpenAI 的纯工具调用流**不设 `finish_reason`**，转换层若只在
  `finish_reason` 上发 `message_delta`，客户端会一直等下去。对应 §6 最后一条
- **ghc-proxy 翻译矩阵**：`content_filter` → `refusal` 是有损的；多个 `choices` 只取
  第一条；交错的多 index 工具调用要各自成通道。对应 §5 与 §6
- **Anthropic 自己的 OpenAI 兼容层**是"测试用"的，并且是**静默**丢字段
  （`response_format`、prompt caching…）——正是这里要避免的做法

## 9. 分步

1. **第一步（已完成）**：`bridge` 包 + 两个方向的**请求**互转 + 清洗 + `Note`，
   连同"改了什么"的记账
2. **第二步（已完成）**：非流式响应互转（含错误体）
3. **第三步（已完成）**：流式状态机（两个方向）+ 接到转发路径上的 `BridgedBody`
4. **第四步（已完成）**：接线（候选池与直连优先、`Note` 进 ChatLog、日志页展示）
5. **第五步（已完成）**：拿真实上游（Opencode）端到端跑了一遍，见 §10

第 1–3 步的验收方式：`go test -cover ./bridge/` 语句覆盖 100%；每条新钉再**逐条证伪**
一遍（把源码改坏 → 对应的那条测试必须转红 → 还原）。证伪要区分"断言失败"与"编译不过"：
后者说明变异本身无效，不算数。

第 4 步的验收方式分两层：`service/bridge.go` 里的判断（`ServableTypes` / `preferDirect` /
`TranslatorFor` / 记账盒 / 响应包装）由 `service/bridge_test.go` 逐条钉住并同样逐条证伪；
日志页的呈现由 `webui/src/routes/logs.test.tsx` 的「协议转换」一组钉住（含"同协议直连不摆
这一节"与"码表里没有的新码不把 i18n 键路径漏到界面上"两条反向用例）。

## 10. 端到端验收（`e2e/`）

单元测试只能验"我以为的输入会给出我以为的输出"，验不了"真上游发来的东西长什么样"。
`e2e/` 下因此有一辆车，两轮共用同一套建档与核对逻辑：

- `stub_upstream.py`：双向协议的**桩**上游。它挑剔（按真上游的规矩校验：Anthropic 的
  `max_tokens` 必填、`content` 必须是块数组、`tool_result` 必须有 `tool_use_id`；
  OpenAI 的 role 白名单、`tool` 消息必须有 `tool_call_id`）并留痕（收到的请求体原样写进
  jsonl）。翻译层漏掉的东西会在这里变成一次**可见的失败**，而不是一个"看起来也对"的答案。
- `run_matrix.py --upstream stub`：19 条，两个方向的非流式与流式、工具调用、并发工具结果
  合并、图片、拒答、记账、重试换家、候选池三条拓扑。每发核三处：客户端拿到的形状、
  上游收到的字节、ChatLog 里的 `Style`/`upstream_style`/`bridge_notes`/tokens。
- `run_matrix.py --upstream opencode`：5 条，打真上游 `https://opencode.ai/zen/go/v1`
  （`space-bunny-free`，文档里标着免费；总用量 4 发，`max_tokens` 封在 256）。

这一趟抓出三个真缺陷，都不是单测能发现的：

1. **上游的 `Content-Length` 被原样复制给客户端**（`service/bridge.go`）。翻译后的字节数
   几乎不可能与上游那份相同，`net/http` 据此校验并**掐断连接**，客户端拿到半截响应。
   修法：翻完响应就作废这个头，长度交给分块传输。
2. **Anthropic 的 `content` 字符串简写被当成"翻不过去"**（`bridge/anthropic.go`）。
   Anthropic 允许 user/assistant 把"就一段文本"写成裸字符串，Claude Code 的普通轮次发的
   正是这个形状；只认块数组的话，这类请求会被判成"这个上游用不了"，成因却在翻译层。
3. **非流式的推理内容既不带上也不记账**（`bridge/response.go`）。流式那条路径一直记
   `dropped_reasoning`，非流式连 `reasoning_content`/`reasoning` 两个字段都没声明——同一个
   请求走流式与非流式会给出两份不同的账。顺带查出同类的第四处：`content_filter` ↔
   `refusal` 换档位时只有 o2a 方向记账。

真上游那一轮的日志（`e2e/.run/work-opencode/db/llmio.db`）正好把三条原则都照了出来：

| 用例 | `style` → `upstream_style` | `bridge_notes` | tokens |
|---|---|---|---|
| 直连·OpenAI 客户端→OpenAI 端点 | openai → openai | （空） | 161 / 3 |
| 转换·Anthropic 客户端→OpenAI 端点 | anthropic → openai | （空） | 161 / 3 |
| 流式·同上 | anthropic → openai | `dropped_reasoning` | 161 / 14 |
| 转换·OpenAI 客户端→Anthropic 端点 | openai → anthropic | `defaulted_max_tokens` | 161 / 3 |
| 拒绝·结构化输出 | openai → anthropic | （空） | 0 / 0 |

两处细节值得记下来：**直连那一行没有任何记账**（原则 3 的"纯结构重排不记账"）；
**被拒绝的那一发 token 是 0**，因为它一个字节都没发给上游——拒绝发生在翻译层，
不花上游的钱。第三行是免费模型在流里吐 reasoning，翻译层丢掉了原样记了一笔。
