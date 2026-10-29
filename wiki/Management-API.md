# 管理接口

管理接口位于 `/api` 下，用于配置提供商、模型、关联、密钥、配额、峰谷日历，以及读取日志与分析数据。

## 鉴权

所有 `/api` 接口都需要：

```http
Authorization: Bearer <TOKEN>
```

`TOKEN` 是部署时设置的环境变量。`TOKEN` 为空时，管理接口不校验。

聊天端点的鉴权方式不同，见[API 接入](API-Access)。

## 响应格式

大多数接口返回统一信封：

```json
{
  "code": 200,
  "message": "success",
  "data": {}
}
```

错误时：

```json
{
  "code": 400,
  "message": "错误说明"
}
```

或者：

```json
{
  "code": 500,
  "message": "错误说明",
  "error": "错误说明"
}
```

注意：

- 部分参数错误与资源不存在使用 HTTP 200 返回，`code` 为 400 或 404。
- 鉴权失败返回 HTTP 401/403。
- 内部错误返回 HTTP 500。
- 调用方应优先检查响应体中的 `code`，不要只看 HTTP 状态码。

分页接口返回：

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "data": [],
    "total": 0,
    "page": 1,
    "page_size": 20,
    "pages": 0
  }
}
```

分页参数：

| 参数 | 说明 |
|---|---|
| `page` | 页码，从 1 开始。 |
| `page_size` | 每页条数。超出允许范围时返回参数错误。 |

## 监控与分析

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/version` | GET | 当前版本。 |
| `/api/metrics/granularities` | GET | 可用分桶档位。 |
| `/api/metrics/stats` | GET | 一次返回分析所需的全部切片。 |

`/api/metrics/stats` 查询参数：

| 参数 | 说明 |
|---|---|
| `from` / `to` | 时间范围。支持 Unix 秒、Unix 毫秒、RFC3339、`YYYY-MM-DD`、`YYYY-MM-DD HH:MM:SS`。 |
| `granularity` | `5m`、`15m`、`30m`、`1h`、`2h`、`6h`、`12h`、`1d`、`7d` 或 `auto`。 |
| `provider` | 提供商名称，逗号分隔多值。 |
| `model` | 上游模型 ID，逗号分隔多值。 |
| `name` | 请求名，逗号分隔多值。 |
| `status` | 状态，逗号分隔多值。 |
| `ua` | User-Agent，逗号分隔多值。 |
| `key_id` | AuthKey ID，逗号分隔多值。 |

不传 `from` / `to` 时默认回看 7 天。

## 提供商

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/providers/template` | GET | 提供商类型与配置模板。 |
| `/api/providers` | GET | 列表，支持 `name`、`type` 筛选。 |
| `/api/providers/models/:id` | GET | 拉取该提供商返回的模型列表。 |
| `/api/providers` | POST | 创建。 |
| `/api/providers/:id` | PUT | 更新。 |
| `/api/providers/:id` | DELETE | 删除，并删除其关联。 |

创建示例：

```bash
curl -X POST http://localhost:7070/api/providers \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "OpenAI 主账号",
    "type": "openai",
    "config": "{\"base_url\":\"https://api.openai.com/v1\",\"api_key\":\"sk-...\"}",
    "console": "https://platform.openai.com/usage",
    "proxy": "",
    "error_matcher": ""
  }'
```

`config` 是 JSON 字符串，不是对象。

## 模型

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/models` | GET | 列表，支持 `search`、`strategy` 筛选。 |
| `/api/models/select` | GET | 轻量模型选项。 |
| `/api/models` | POST | 创建。 |
| `/api/models/order` | PATCH | 调整展示顺序。 |
| `/api/models/:id` | PUT | 更新。 |
| `/api/models/:id` | DELETE | 删除。 |

创建示例：

```bash
curl -X POST http://localhost:7070/api/models \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "gpt-4o-mini",
    "remark": "主模型",
    "max_retry": 3,
    "time_out": 60,
    "strategy": "lottery",
    "breaker": false,
    "prefer_direct": true
  }'
```

要求：

- `max_retry` 必须大于 0。省略或为 0 时，请求不会尝试任何一次。
- `time_out` 单位为秒。
- `strategy` 为 `lottery` 或 `rotor`。
- `prefer_direct` 省略时按开启处理。

删除模型不会自动删除该模型下的关联。

## 模型与提供商的关联

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/model-providers` | GET | 列表，必须提供 `model_id` 查询参数。 |
| `/api/model-providers/status` | GET | 查询一条关联最近 10 次请求的成功/失败状态。需要 `provider_id`、`model_name`、`provider_model`。 |
| `/api/model-providers/metadata` | GET | 返回模型能力与价格建议。需要 `provider_id`、`provider_model`。 |
| `/api/model-providers` | POST | 创建。 |
| `/api/model-providers/:id` | PUT | 更新。 |
| `/api/model-providers/:id/status` | PATCH | 启用或停用。 |
| `/api/model-providers/:id` | DELETE | 删除。 |

创建示例：

```bash
curl -X POST http://localhost:7070/api/model-providers \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "model_id": 1,
    "provider_id": 1,
    "provider_name": "gpt-4o-mini",
    "tool_call": true,
    "structured_output": true,
    "image": false,
    "with_header": false,
    "backup": false,
    "weight": 3,
    "input_price": 0.15,
    "cache_read_price": 0.075,
    "output_price": 0.6,
    "currency": "USD",
    "customer_headers": {},
    "extra_body": {}
  }'
```

字段说明：

- `provider_name` 是历史命名，实际含义是「提供商模型」，不是提供商名称。
- `weight` 必须大于 0。
- `backup` 为 `true` 时进入备用池。
- `customer_headers` 是字符串到字符串的映射。
- `extra_body` 是任意 JSON 对象，会合并进上游请求体。
- `peak` 可选，结构见[峰谷计费](Peak-Billing)。

启用/停用：

```bash
curl -X PATCH http://localhost:7070/api/model-providers/1/status \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"status": false}'
```

## 密钥

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/auth-keys` | GET | 分页列表，支持 `search`、`status`、`allow_all`。 |
| `/api/auth-keys/list` | GET | 轻量选项。 |
| `/api/auth-keys` | POST | 创建。 |
| `/api/auth-keys/:id` | PUT | 更新。 |
| `/api/auth-keys/:id/status` | PATCH | 启用或停用。 |
| `/api/auth-keys/:id` | DELETE | 删除。 |

创建示例：

```bash
curl -X POST http://localhost:7070/api/auth-keys \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "项目 A",
    "status": true,
    "io_log": false,
    "allow_all": false,
    "models": ["gpt-4o-mini"],
    "expires_at": "2027-01-01T00:00:00Z"
  }'
```

`expires_at` 使用 RFC3339。省略或为 `null` 表示永久有效。

建议显式提供 `status`、`allow_all`、`io_log`。`status` 省略时存为 NULL，鉴权查询只接受 `status=true` 的 Key，该 Key 将无法使用；`allow_all` 省略时按 false 处理，此时必须提供 `models`。

响应中的 `key` 是服务端生成的完整密钥。列表接口也会返回完整密钥，请按敏感信息处理。

## 日志

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/logs` | GET | 日志列表。 |
| `/api/logs/:id/chat-io` | GET | 单条日志的请求与响应正文。 |
| `/api/logs/cleanup` | POST | 手动清理。 |
| `/api/logs/cleanup/history` | GET | 清理历史。 |
| `/api/user-agents` | GET | User-Agent 去重列表。 |
| `/api/provider-models` | GET | 上游模型名去重列表。 |

`/api/logs` 查询参数：

| 参数 | 说明 |
|---|---|
| `provider_name` | 提供商名称，逗号分隔多值。 |
| `provider_model` | 上游模型 ID，逗号分隔多值。 |
| `name` | 请求名，逗号分隔多值。 |
| `status` | 状态，逗号分隔多值。 |
| `style` | 客户端协议，逗号分隔多值。 |
| `auth_key_id` | AuthKey ID，逗号分隔多值。 |
| `trace_id` | 精确匹配。 |
| `session_id` | 精确匹配。 |
| `id` | 精确匹配。 |
| `page` / `page_size` | 分页。 |

手动清理：

```bash
curl -X POST http://localhost:7070/api/logs/cleanup \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"type": "days", "value": 30}'
```

`type` 为 `count` 或 `days`。

## 数据库压缩

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/logs/compression` | GET | 状态与策略。 |
| `/api/logs/compression/policy` | PUT | 迁移策略。 |
| `/api/logs/compression/run` | POST | 开始迁移。 |
| `/api/logs/compression/pause` | POST | 暂停。 |
| `/api/logs/compression/decompress` | GET | 回滚进度。 |
| `/api/logs/compression/decompress` | POST | 回滚为明文。 |
| `/api/logs/compression/reclaim` | POST | 增量回收。 |
| `/api/logs/compression/reclaim/stop` | POST | 停止持续回收。 |
| `/api/logs/compression/reclaim/policy` | PUT | 回收策略。 |
| `/api/logs/compression/vacuum` | POST | 整库重整。 |

请求体与参数见[数据库维护](Database-Maintenance)。

## 系统配置

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/config/:key` | GET | 读取配置，值以字符串返回。 |
| `/api/config/:key` | PUT | 写入配置，请求体为 `{"value":"..."}`。 |

可用的键与默认值见[配置说明](Configuration)。

## 峰谷日历

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/peak-calendar` | GET | 读取全局日历。 |
| `/api/peak-calendar` | PUT | 保存日历。 |
| `/api/peak-calendar/preview` | POST | 预览条款。 |
| `/api/peak-calendar/holidays/sync` | POST | 同步指定年份节假日。 |

峰谷条款随关联创建/更新保存。

## 配额

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/quota/config` | GET | 读取配置与内置适配器。 |
| `/api/quota/config` | PUT | 修改刷新周期与告警阈值。 |
| `/api/quota/sources` | POST | 新增数据源。 |
| `/api/quota/sources` | PUT | 更新数据源。 |
| `/api/quota/sources/:id` | DELETE | 删除数据源。 |
| `/api/quota/run` | POST | 拉取全部启用数据源。 |
| `/api/quota/sources/:id/refresh` | POST | 刷新单个数据源。 |
| `/api/quota/test` | POST | 试跑未保存的数据源。 |

详细字段见[套餐余量](Quota)。

## 测试

| 路径 | 方法 | 功能 |
|---|---|---|
| `/api/test/:id` | GET | 测试一条模型-提供商关联的连通性。 |
| `/api/test/react/:id` | GET | 运行 React Agent 能力测试，仅支持 OpenAI 类型。 |
| `/api/test/count_tokens` | GET | 测试 Anthropic Token 计数配置。 |

`id` 是关联 ID，不是提供商 ID 或模型 ID。
