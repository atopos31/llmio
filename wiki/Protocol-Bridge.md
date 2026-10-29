# 协议互转

LLMIO 可以在客户端协议与上游协议不一致时进行翻译。两端协议相同时，请求和响应原样转发，不经过翻译层。

当前支持：

- OpenAI Chat Completions → Anthropic Messages
- Anthropic Messages → OpenAI Chat Completions

OpenAI Responses 与 Gemini 不参与互转，只能选择同类型上游。

## 适用矩阵

| 客户端协议 | OpenAI 上游 | Anthropic 上游 | OpenAI Responses 上游 | Gemini 上游 |
|---|---|---|---|---|
| OpenAI Chat Completions | 直连 | 互转 | 不支持 | 不支持 |
| Anthropic Messages | 互转 | 直连 | 不支持 | 不支持 |
| OpenAI Responses | 不支持 | 不支持 | 直连 | 不支持 |
| Gemini | 不支持 | 不支持 | 不支持 | 直连 |

## 方向如何决定

不需要为单次请求配置方向。LLMIO 在每次从候选池取出一条关联后，按以下两者决定：

- 客户端协议，来自请求路径。
- 上游协议，来自该关联的提供商类型。

每次尝试独立判断。如果第一条关联需要互转但翻译失败，LLMIO 会把这次尝试记为失败，换下一条关联；下一条可能是同协议，也可能需要另一个方向的互转。

## 优先匹配相同协议

模型上的「优先匹配相同协议」默认开启，决定候选池里是否混入可互转的协议：

- 开启：只要该模型下存在与客户端同协议的提供商，就只在这些提供商中选择；没有同协议提供商时才使用可互转的提供商。
- 关闭：本协议与可互转协议的提供商一起按权重参与。

建议保持开启。这样正常流量走同协议直连，只有缺少同协议上游时才发生翻译，行为更容易预测。

## 请求转换

请求转换覆盖以下内容：

- `system` 消息提升为 Anthropic 顶层 `system`。
- 文本、图片、工具调用、工具结果在两种消息格式之间转换。
- 连续的 `tool` 消息合并进同一条 Anthropic `user` 消息。
- `tools` 与 `tool_choice` 的对应关系。
- `stop` 与 `stop_sequences`、`temperature`、`top_p`、`top_k`。
- `max_tokens` / `max_completion_tokens`。
- `response_format` 的 JSON 输出约束。
- 流式开关与 `stream_options.include_usage`。

### max_tokens 默认值

Anthropic Messages 要求 `max_tokens` 必填。客户端没有提供时，LLMIO 补 `8192`，并在日志中记录 `defaulted_max_tokens`。

这是一个有损决定：如果实际输出超过 8192，上游会截断，结束原因会显示为达到最大长度。调用方应从结束原因判断是否被截断。

### 结构化输出

OpenAI 的 `response_format` 会转换为 Anthropic 的 `output_config.format`：

- `json_schema`：Schema 原样搬运。
- `json_object`：转换为一个「任意 JSON 对象」的 Schema。

转换成功时记录 `translated_output_format`。如果上游不支持该参数而返回 400，LLMIO 不会静默删除它，而是按失败处理，换下一家或返回错误。

### 无法表达的内容

转换层遵循一条规则：调用方能否从响应里发现差异。

| 处理 | 例子 | 结果 |
|---|---|---|
| 拒绝 | `n > 1`、非零 `presence_penalty` / `frequency_penalty`、`logprobs=true`、`top_logprobs>0`、无法识别的 `response_format.type` | 这次尝试失败，换下一家。 |
| 记账 | `seed`、`user`、Anthropic → OpenAI 的 `top_k`、`thinking`、`metadata`、图片精度档位 | 请求继续，日志记录短码。 |
| 结构重排 | 合并连续同角色消息、合并多个文本块、调整工具结果顺序 | 内容不变，不记录短码。 |

「拒绝」不是整次请求失败。它表示这家上游无法表达客户端的要求，LLMIO 会尝试其他候选。

### 常见记账短码

| 短码 | 含义 |
|---|---|
| `defaulted_max_tokens` | 请求没带最大输出长度，补了 8192。 |
| `translated_output_format` | `response_format` 被翻译成 Anthropic 结构化输出。 |
| `dropped_seed` | 丢掉了 `seed`。 |
| `dropped_user` | 丢掉了 `user`。 |
| `dropped_top_k` | 丢掉了 Anthropic 的 `top_k`。 |
| `dropped_thinking` | 丢掉了思考相关设置。 |
| `dropped_metadata` | 丢掉了 `metadata`。 |
| `image_by_url` | 图片改用 URL 传递。 |
| `dropped_image_detail` | 丢掉了图片精度档位。 |
| `reordered_tool_result` | 工具结果被移到消息前部。 |
| `filled_missing_tool_result` | 给没有结果的工具调用补了失败结果。 |
| `dropped_orphan_tool_result` | 丢掉了找不到对应调用的工具结果。 |
| `self_heal_dropped:参数名` | 上游点名参数后，自愈删除该参数并重发。 |

完整的短码列表与说明见仓库的 `docs/protocol-bridge.md` 和日志详情页。

## 响应转换

### 非流式

Anthropic 的 `content` 块数组会转换为 OpenAI 的 `choices[].message`：

- 文本块 → `content`。
- `tool_use` 块 → `tool_calls`。
- 结束原因按对应表转换。
- 用量从 `input_tokens` / `output_tokens` 转换为 `prompt_tokens` / `completion_tokens`。

反向转换同样处理文本、工具调用与用量。

### 流式

流式响应按事件序列转换：

- Anthropic 的 `message_start`、`content_block_start`、`content_block_delta`、`content_block_stop`、`message_delta`、`message_stop` 与 OpenAI 的 chunk 序列互相映射。
- 工具调用的参数可能分多个片段到达，转换层按通道累积后再输出。
- 用量在流结束时补齐。
- 无法识别的片段会记录短码并跳过，不会让整条流崩溃。

## 在日志中查看

当上游协议与客户端协议不同时，日志详情会显示：

- 「上游协议」字段。
- 「协议转换」区块。
- 转换方向，例如 `openai → anthropic`。
- 本次转换改动的短码及中文说明。

![带协议转换说明的日志详情](images/console-log-detail-bridge.png)

这些信息可以帮助判断：

- 请求是否经过了翻译。
- 哪个字段被丢弃或补了默认值。
- 失败是因为上游拒绝，还是翻译层无法表达。

## 测试

仓库提供一个不依赖外部凭据的端到端测试：

```bash
python e2e/run_matrix.py --upstream stub
```

它会在本地启动桩上游和独立的 LLMIO 实例，配置提供商、模型和关联，然后逐条验证：

- 客户端收到的响应形状。
- 上游实际收到的请求体。
- LLMIO 日志中的协议、记账与 Token。

要求真实上游时使用：

```bash
python e2e/run_matrix.py --upstream opencode
```

这会消耗真实用量，只建议在需要验证特定上游时使用。

## 使用建议

1. 优先给模型配置与客户端同协议的提供商。互转用于缺少同协议上游时的补充，不是常态。
2. 使用 Anthropic 上游承接 OpenAI 客户端时，注意 `max_tokens` 默认值与截断风险。
3. 依赖 JSON 结构化输出时，确认上游支持 Anthropic 的 `output_config`；不支持时会直接失败。
4. 依赖 `seed`、`logprobs`、惩罚项等参数时，不要把这些请求交给可互转的上游；这些参数要么被拒绝，要么被记账后丢弃。
5. 排查转换问题时，先看日志详情的「协议转换」区块，再对照上游错误原文。
