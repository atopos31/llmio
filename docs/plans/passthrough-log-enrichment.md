# 透传日志内容增强（Passthrough Log Enrichment）

- 日期: 2026-10 · 状态: **已实施**
- 关联: [转发透传专页](./forwarding-page.md)、[提供商专属 URL 透传](./unknown-path-passthrough.md)

## 背景

v6 的专属地址透传与纯转发只落一条「骨架日志」：`Style = passthrough`、提供商、模型名、
耗时、鉴权密钥 ID，没有请求体、响应体、usage、请求路径，也不记录首字节耗时与调用方信息。
用户在日志页看到的是一条几乎无法排查的记录，与 /v1 已知端点（完整 ChatIO + usage + TPS）差距很大。

本次增强目标：让透传日志在日志页与常规请求一样可读、可排查，同时不改变转发行为、不引入
新的日志表。

## 一、记录条件

- `shouldRecordForwardIO(ctx)`（handler/provider_route.go）：管理 TOKEN 发起的请求
  （`AuthKeyID == 0` 即 allowAll 场景）默认记录；普通访问密钥按该密钥的
  `ContextKeyAuthKeyIOLog`（即 AuthKey.IOLog）决定，与原 /v1 端点一致。
- 请求体超过 `maxForwardIOBytes`（`1 << 20`，1 MiB）时**整条 IO 不记**，
  且 ChatLog 的 `ChatIO` 标志写 false——避免前端在「有 IO」的标记下跳到一行不存在的 IO。
- 响应体捕获上限同样是 1 MiB（传给 `relayForwardResponse` 的 captureLimit），超出只保留前缀。

## 二、记录内容

| 字段 | 内容 |
|---|---|
| `Input`（ChatIO） | 客户端**改写前**的原始请求体（`ioBody := append([]byte(nil), body...)` 复制；clients 看到的模型名就是它） |
| `OutputUnion`（ChatIO） | 响应体前缀：SSE 按事件拆成 `OfStringArray`（剥 `data:` 前缀、跳过空行与 `[DONE]`）；JSON / text / event-stream 或响应体本身是合法 JSON → `OfString`；二进制或不可捕获 → 不写 |
| `Usage` | 非流式看响应 JSON 的 `usage`；流式在捕获的事件 payload 里逐条解析后合并（按字段取最大值，Anthropic 的输入在 message_start、输出在 message_delta 分开出现，取 max 才能拿到完整值）；Gemini 读 `usageMetadata` |
| `Tps` | `completion_tokens / ProxyTime.Seconds()`，ProxyTime 为 0 时不写 |
| `FirstChunkTime` / `ChunkTime` | relay 写第一块时的耗时 / `ProxyTime - FirstChunkTime` |
| `Size` | 上游响应体字节数（透传阶段统计） |
| `RequestPath` | `"POST /p/{slug}/v1/embeddings"`——方法与路径，**不含 query**（密钥可能出现在 query 里，避免落库） |
| `TraceID` / `UserAgent` / `RemoteIP` | `token.GenerateRandomChars(10)` / 请求头 / `c.ClientIP()` |

usage 解析按 `provider.Type` 分派（`parseForwardUsage`）：

- `anthropic`：`prompt_tokens = input_tokens + cache_creation_input_tokens + cache_read_input_tokens`，
  `completion_tokens = output_tokens`，`cached_tokens = cache_read_input_tokens`；
- `gemini`：`usageMetadata.promptTokenCount / candidatesTokenCount / totalTokenCount / cachedContentTokenCount`；
- `openai-res`：优先 `input/output` 字段，缺失时回落到 OpenAI 结构；
- 其他（含默认）：标准 `prompt_tokens / completion_tokens / total_tokens / prompt_tokens_details.cached_tokens`。
- `total_tokens` 缺失时用 prompt + completion 补齐。

## 三、写入顺序与一致性

真实现（handler/provider_route.go 的 `forwardUpstream`）：

1. 发起上游请求，relay 响应时统计 `FirstChunkTime`、`Size`、捕获响应前缀、解析 usage；
2. `newPassthroughLog` 构造 ChatLog（`ChatIO = ioLog`、`RequestPath`、TraceID、UA、IP、耗时）；
3. 失败路径直接 `savePassthroughLog` 后返回 502（记录上游错误）；
4. 成功路径补 `Status`：上游状态码 `>= 400` 写 `upstream status: %d`，否则 `StatusSuccess`；
5. `savePassthroughLog(log)` 返回新日志 id（内部 `context.WithTimeout(Background, 10s)`，不随请求取消）；
6. 仅当 `ioLog` 时 `service.SaveChatIO(ctx, logID, ioBody, outputUnion)` 一次插入请求 + 响应。

`SaveChatIO` 是 service 层导出函数，内部沿用与 /v1 相同的撞锁重试策略；失败只 slog 记录，不
影响转发结果（日志少一份 IO，但请求已经成功返回）。

## 四、前端展示

- `webui/src/lib/api.ts`：ChatLog 增加可选 `request_path?: string`（注释说明只透传写、不含 query）。
- `webui/src/lib/logs-columns.ts`：注册候选列 `request_path`（`max-w-[220px] truncate font-mono text-xs`，
  默认隐藏，用户在列菜单里勾选）；空值显示 `-`。
- `webui/src/components/log-detail-sheet.tsx`：基本信息里 `remote_ip` 之后增加「请求路径」字段（等宽字体）。
- 三语 `webui/src/i18n/locales/{zh-CN,zh-TW,en}/logs.json`：`table.request_path` 与
  `detail.request_path`（请求路径 / 請求路徑 / Request path），键集三语一致（locales.test.ts 校验）。
- ChatIO 的展示沿用既有 `canViewChatIO = ChatIO && Status !== "running"` 判断；SSE 数组已在
  `log-chat.tsx` 的 `parseResponse` 中支持 `OfStringArray`。
- **通用 JSON 展示**（2026-10 补充）：透传的不一定是聊天接口（embeddings、rerank、图片……）。
  `webui/src/lib/chat-io/parse.ts` 的 `sniffProtocol` 只认 choices / output / candidates /
  content / messages 等聊天特征，返回 `null` 表示不是聊天协议；`sniffResponseProtocol`
  在 Style 未知时逐条响应片段嗅探。`log-chat.tsx` 据此把这类 IO 的请求与响应都渲染成
  「通用 JSON」区块，原始 JSON **默认展开**，不再先显示"(空)"再让用户点"查看原始 JSON"；
  Style 是已知协议、或响应体里能认出聊天结构时，仍走原来的解析面板。
- **未识别 usage 显示 `-`**：`log-detail-sheet.tsx` 的 Token 使用区域在输入/输出/合计
  都为 0 时四列统一显示 `-`（含 cached 缺失时），避免把"没解析到"读成"没消耗"。
- 三语 `logs.json` 增加 `chat_io.generic_json` / `chat_io.generic_hint`。

## 五、测试

`handler/provider_route_test.go` 新增 6 个用例（fixture 的上游支持自定义响应体/Content-Type/状态码/
延迟）：

- `TestDedicatedRouteForwardLogsRequestAndResponseIO`：TraceID / UA / IP / RequestPath / ChatIO /
  Size / ProxyTime / FirstChunkTime 非零；ChatIO.Input = 客户端原始 body，Output = 上游 JSON 字符串。
- `TestDedicatedRouteForwardLogsUsageAndTps`：非流式 usage（含 cached）落库，Tps > 0。
- `TestDedicatedRouteForwardLogsStreamingIO`：SSE 两条事件 + `[DONE]` → `OfStringArray` 长度 2、
  第二条含 usage 字段、usage 合并正确。
- `TestDedicatedRouteForwardLogsSkippedForPlainKey`：未开 IO 的受限密钥 → ChatIO false 且 0 行 ChatIO。
- `TestDedicatedRouteForwardLogsIOForIOLogKey`：开了 IO 的密钥 → AuthKeyID 正确、1 行 ChatIO。
- `TestDedicatedRouteForwardAllLogsRequestPath`：纯转发 POST /v1/embeddings → RequestPath 正确、
  Name/ProviderModel 为 body 里的模型名。

夹具注意：断言 ProxyTime / FirstChunkTime 时给上游加约 100ms 延迟——Windows 上 `time.Since`
粒度较粗，否则偶发 0 值（本次已加 `setResponseDelay`）。

## 六、已知限制

- 响应体只保留前 1 MiB 前缀；超出部分不落库（SSE 超长流同理）。
- 请求体超过 1 MiB 时整条 IO 不记（不做半截截断），ChatIO 标志为 false。
- usage 是透传层尽力解析：上游字段缺失或非标准结构时可能只有部分字段；解析失败不影响转发，
  日志详情里显示 `-` 而不是 0。
- `request_path` 不含 query string；需要 query 排查时暂时只能靠上游侧日志。
- 日志的 `Name` 记录客户端请求的模型名，`ProviderModel` 记录改写后实际上游模型名；两者相同
  表示未改写（none 或 known 未命中）。
- IO 写入失败只记录错误日志，不回滚已完成的转发（ChatIO 标志可能为 true 但无 IO 行）。

## 七、IO 展示的编码兜底（GB18030）

请求体是按**字节**原样落库的（input 列），客户端用什么编码发来，库里就是什么编码。中文
Windows 上的客户端常按 GBK/GB18030 发送，这些字节不是合法 UTF-8——encoding/json 序列化时
会把每个非法字节替换成 U+FFFD，于是日志详情页看到的是 `���ã�����` 这种不可逆的乱码。
库里的字节没坏（逐字节往返实测相同），坏的是**展示**这一层。

因此 `GET /api/logs/:id/io` 在读路径上兜底（handler/body_text.go）：

- `utf8.Valid` 通过 → 原样返回，InputEncoding = utf-8；
- 否则按 GB18030 解码，解出来不含替换符/控制字符 → 采用，InputEncoding = gb18030；
- 都不成立（图片等二进制）→ 非法字节替换成 U+FFFD，InputEncoding = binary。

响应侧同样返回 OutputEncoding。边界要说清楚：of_string 是 string 列，GBK 字节能原样保留、
可以解码；of_string_array 是 \[\]string 经 json.Marshal 存列，非法 UTF-8 字节在**写入时**
就已经被替换成 U+FFFD，读路径无法恢复（实际流量里上游响应基本都是 UTF-8，影响很小）。

写入路径与存储格式都不动：不重写历史行，也不需要迁移。

## 文件清单

```
handler/provider_route.go           forwardUpstream / 日志构造 / usage 解析 / IO 落库
service/chat.go                     导出 SaveChatIO（撞锁重试）
models/model.go                     ChatLog.RequestPath
handler/provider_route_test.go      6 个日志用例 + 夹具 setResponse / setResponseDelay
webui/src/lib/api.ts                ChatLog.request_path
webui/src/lib/logs-columns.ts       候选列 request_path
webui/src/lib/logs-columns.test.ts  两个分支用例
webui/src/components/log-detail-sheet.tsx  基本信息请求路径 / 未识别 usage 显示 -
webui/src/lib/chat-io/parse.ts      sniffProtocol / sniffResponseProtocol
webui/src/lib/chat-io/parse.test.ts 嗅探用例
webui/src/routes/log-chat.tsx       非聊天协议按通用 JSON 展示（默认展开）
webui/src/i18n/locales/*/logs.json  table/detail.request_path、chat_io.generic_json/generic_hint
```
