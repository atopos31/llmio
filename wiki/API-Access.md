# API 接入

LLMIO 对外提供四种客户端协议：

- OpenAI Chat Completions
- OpenAI Responses
- Anthropic Messages
- Gemini Native

每种协议都有独立的路径，另有 `/v1` 兼容路径。

## 基础地址

默认监听 `7070` 端口：

```text
http://localhost:7070
```

公网部署时替换为反向代理地址，例如 `https://llmio.example.com`。

## 端点

| 协议 | 端点 | 方法 | 说明 |
|---|---|---|---|
| OpenAI | `/openai/v1/models` | GET | 模型列表 |
| OpenAI | `/openai/v1/chat/completions` | POST | 聊天完成 |
| OpenAI | `/openai/v1/responses` | POST | Responses API |
| Anthropic | `/anthropic/v1/models` | GET | 模型列表 |
| Anthropic | `/anthropic/v1/messages` | POST | 消息 |
| Anthropic | `/anthropic/v1/messages/count_tokens` | POST | Token 计数 |
| Anthropic | `/anthropic/api/event_logging/batch` | POST | Claude Code 事件上报，无鉴权 |
| Gemini | `/gemini/v1beta/models` | GET | 模型列表 |
| Gemini | `/gemini/v1beta/models/{model}:generateContent` | POST | 生成内容 |
| Gemini | `/gemini/v1beta/models/{model}:streamGenerateContent` | POST | 流式生成 |
| 兼容 | `/v1/models` | GET | 等同 OpenAI 模型列表 |
| 兼容 | `/v1/chat/completions` | POST | 等同 OpenAI 聊天 |
| 兼容 | `/v1/responses` | POST | 等同 OpenAI Responses |
| 兼容 | `/v1/messages` | POST | 等同 Anthropic Messages |
| 兼容 | `/v1/messages/count_tokens` | POST | 等同 Anthropic Token 计数 |

`/v1` 路径为兼容保留。新接入建议使用带供应商前缀的路径。

`/anthropic/api/event_logging/batch` 只接收并确认 Claude Code 的批量事件，返回 `accepted_count` 与 `rejected_count`，不保存事件内容。该端点不需要鉴权。

## 鉴权

### OpenAI 端点

使用 Bearer Token：

```http
Authorization: Bearer <TOKEN 或 AuthKey>
```

适用于 `/openai/v1/*` 和 `/v1/*` 中的 OpenAI 端点。

### Anthropic 端点

两种方式都接受：

```http
x-api-key: <TOKEN 或 AuthKey>
```

或：

```http
Authorization: Bearer <TOKEN 或 AuthKey>
```

两个头同时出现时，`x-api-key` 优先。

Claude Code 在设置 `ANTHROPIC_API_KEY` 时发送 `x-api-key`，在设置 `ANTHROPIC_AUTH_TOKEN` 时发送 Bearer。两种都可以接入。

### Gemini 端点

使用：

```http
x-goog-api-key: <TOKEN 或 AuthKey>
```

### Token 与 AuthKey

- `TOKEN` 是部署时设置的管理员令牌，允许全部模型。
- AuthKey 在控制台「密钥管理」中创建，可以限制模型、设置有效期、控制 IO 记录。
- 使用 AuthKey 时，模型列表只返回该 Key 允许的模型；请求未授权的模型返回 403。

## 模型列表

### OpenAI 格式

```bash
curl http://localhost:7070/openai/v1/models \
  -H "Authorization: Bearer <KEY>"
```

返回 OpenAI 的模型列表结构：

```json
{
  "object": "list",
  "data": [
    {"id": "gpt-4o-mini", "object": "model", "created": 1700000000, "owned_by": "llmio"}
  ]
}
```

`id` 是 LLMIO 的请求名，不是上游模型 ID。

### Anthropic 格式

```bash
curl http://localhost:7070/anthropic/v1/models \
  -H "x-api-key: <KEY>"
```

### Gemini 格式

```bash
curl http://localhost:7070/gemini/v1beta/models \
  -H "x-goog-api-key: <KEY>"
```

模型列表按协议可服务范围返回。例如，OpenAI 模型列表会包含可互转的 Anthropic 上游模型。

## OpenAI Chat Completions

```bash
curl http://localhost:7070/openai/v1/chat/completions \
  -H "Authorization: Bearer <KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "你好"}
    ]
  }'
```

流式请求：

```bash
curl -N http://localhost:7070/openai/v1/chat/completions \
  -H "Authorization: Bearer <KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "stream": true,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

`-N` 关闭 curl 的缓冲，便于观察 SSE。

## OpenAI Responses

```bash
curl http://localhost:7070/openai/v1/responses \
  -H "Authorization: Bearer <KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "input": [
      {"role": "user", "content": [{"type": "input_text", "text": "你好"}]}
    ]
  }'
```

Responses 请求需要选择 `openai-res` 类型的上游关联。

## Anthropic Messages

```bash
curl http://localhost:7070/anthropic/v1/messages \
  -H "x-api-key: <KEY>" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "你好"}
    ]
  }'
```

流式请求在请求体中加 `"stream": true`。

`max_tokens` 是 Anthropic 协议的必填字段。使用互转时如果客户端没有提供，LLMIO 会补 8192。

## Gemini Native

```bash
curl "http://localhost:7070/gemini/v1beta/models/gemini-2.0-flash:generateContent" \
  -H "x-goog-api-key: <KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [
      {"parts": [{"text": "你好"}]}
    ]
  }'
```

流式：

```bash
curl -N "http://localhost:7070/gemini/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse" \
  -H "x-goog-api-key: <KEY>" \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"parts":[{"text":"你好"}]}]}'
```

Gemini 路径中的模型名会作为请求名使用，必须与「模型管理」中的名称一致。

## Token 计数

Anthropic 的 Token 计数端点会转发到「系统配置」中配置的 Anthropic 连接，不经过模型关联和负载均衡。

```bash
curl http://localhost:7070/anthropic/v1/messages/count_tokens \
  -H "x-api-key: <KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

需要先在「系统配置」→「Anthropic 令牌计数配置」中填写 Base URL、API Key 与 API Version。未配置时返回 `code=404` 的业务错误。

## 会话追踪

在请求体中传入 `session_id`：

```json
{
  "model": "gpt-4o-mini",
  "session_id": "my-session-001",
  "messages": [{"role": "user", "content": "你好"}]
}
```

使用 OpenAI SDK 时可通过 `extra_body`：

```python
response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "你好"}],
    extra_body={"session_id": "my-session-001"},
)
```

日志页可以按 Session ID 搜索。自定义请求头中的 `{{session}}` 会使用派生后的会话键：

1. 请求体 `session_id`。
2. 入站请求头 `x-opencode-session`、`x-session-id`、`session_id`。
3. 首条 user 消息的对话根哈希。
4. 随机串。

这个顺序保证会话键始终非空，适合强制要求会话头的上游。

## 流式响应

- 响应类型为 `text/event-stream`。
- LLMIO 会设置 `X-Accel-Buffering: no`，并立即 flush。
- 反向代理必须关闭响应缓冲，否则客户端会看到整段输出一次性到达。
- 流式请求的响应头等待时间为模型超时时间的三分之一。
- 流结束后才完成 Token 统计与日志写入；日志可能比响应稍晚出现。

## 错误响应

聊天端点在上游全部尝试失败时返回 HTTP 500：

```json
{
  "code": 500,
  "message": "All retry failed, trace ID: xxxxxxxx, last error: ...",
  "error": "All retry failed, trace ID: xxxxxxxx, last error: ..."
}
```

处理原则：

- 401：令牌缺失、无效或已过期。
- 403：令牌有效，但没有请求该模型的权限。
- 500：模型不存在、没有可用关联、全部上游失败或请求超时。

`message` 中会带 TraceID 和最后一次失败的原因。完整错误与每次尝试的记录在请求日志中。

管理接口的错误格式不同，详见[管理接口](Management-API#响应格式)。

## 客户端环境变量

Claude Code、Codex 等工具通常通过环境变量读取地址与密钥：

```bash
export OPENAI_API_KEY=<KEY>
export ANTHROPIC_API_KEY=<KEY>
export ANTHROPIC_AUTH_TOKEN=<KEY>
export GEMINI_API_KEY=<KEY>
```

把客户端的基础地址指向 LLMIO 对应的协议路径，例如：

```text
OpenAI:    http://localhost:7070/openai/v1
Anthropic: http://localhost:7070/anthropic
Gemini:    http://localhost:7070/gemini
```

具体变量名以客户端文档为准。接入后先在控制台「快速开发」页生成一份可用的 curl，确认网关、模型和密钥都正确。
