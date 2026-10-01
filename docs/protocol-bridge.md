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
  交错出现，不能假设顺序）。首次见到某 index → 关掉当前块，发 `content_block_start`
  （`tool_use`，带 `id` / `name`，`input:{}`），随后该 index 的 `function.arguments`
  片段变成 `input_json_delta` 的 `partial_json`
- `finish_reason` → 关掉打开的块，发 `message_delta`（`stop_reason`）与 `message_stop`
- **`[DONE]` 前没有 `finish_reason`**（工具调用流里常见）→ 按"有没有见过 tool_call"
  推断 `tool_use` / `end_turn`，**不能什么都不发**：不发 `message_delta` 客户端会一直等

Anthropic → OpenAI 是逆过程，另需注意 `message_delta` 的 `usage` 要并进最后一个 chunk。

## 7. 接线

翻译层是纯函数（`bridge` 包，只依赖标准库），**不碰转发路径**。接线是单独一步：

- 判断要不要转：客户端 style（`chatHandler` 拿得到）× 上游 style（模型-上游关联上那一列）
- 要转的话，`BuildReq` 之前的 body 换成转换结果，响应体（含 SSE）过一遍转换
- 转换错误 → 当作"这家服务不了"，`balancer.Delete` + 换下一家（与 `BuildReq` 失败同路）
- `Note` 进 ChatLog，界面上能看见"这次请求被改过什么"

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
   `go test -cover ./bridge/` 语句覆盖 100%
2. 非流式响应互转
3. 流式状态机（两个方向）
4. 接线（判断要不要转、`Note` 进 ChatLog）——**就在本分支继续**，做完拿真实上游
   （Opencode）端到端跑一遍再合
