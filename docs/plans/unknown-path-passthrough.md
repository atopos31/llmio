# 提供商专属 URL 透传（Codex 独立搜索路由）

- 日期: 2026-10 · 分支: `feat/provider-dedicated-route`（基线 master bfb507d）· 状态: **已实施（v6 设计）**

## 实施记录（2026-10-25）

按 v6 设计落地，两处与设计的偏差（实施中发现的必要修正）：

1. `common.NotFound/BadRequest` 是管理接口的"HTTP 200 + body code"约定；专属地址是
   客户端直连的 API 面，改用 `ErrorWithHttpStatus` 返回真实 404/400/403/405。
2. 混合模式透传前剥掉客户端写法里的 `/v1` 前缀（客户端 base_url 约定为 `/p/{name}/v1`，
   而提供商 base_url 惯例自带版本段，不剥会拼出 `/v1/v1/...`）。

实施新增的硬性规则：专属地址名称标识仅允许字母/数字开头 + `-_.`（≤64 字符），不满足的
名称不提供专属地址（表单实时提示，不阻断保存）；纯转发模式与无 model 透传要求全量权限
（admin TOKEN 或 AllowAll 密钥），受限密钥 403。

### Review 修正（2026-10-26）

1. **鉴权改挂 `AuthDedicated`**（x-api-key 优先、回落 Bearer，与 AuthAnthropic 共用双头
   解析）：/p 组原先只挂 `authOpenAI`，配 `ANTHROPIC_API_KEY` 的客户端会在专属地址上撞出
   全局 `/anthropic` 没有的 401。
2. **`/v1` 前缀剥离只认完整段**：`TrimPrefix(path, "/v1")` 会把 `/v1beta/...` 咬成
   `beta/...`（非 `/` 开头被路径清洗 400），Gemini 原生路径透传直接失败。改为
   `CutPrefix` + 段边界判断。
3. **客户端 Content-Type 原样透传**：原先硬编码空串被 `Forward` 强改为 application/json，
   multipart 上传（audio 转写等宣传过的顺带收益）的 boundary 会丢。纯转发模式本就透传，
   现两处一致。
4. **透传日志落库脱离请求 ctx**：客户端断连会取消请求 ctx，`SaveChatLog` 随之失败——
   恰是 client-cancel、上游断流最需要日志的现场。改为 `context.WithTimeout(Background, 10s)`。
5. **gzip 排除列表 `"/p"` → `"/p/"`**：排除列表是前缀匹配，裸 `"/p"` 会把 `/providers`
   等 SPA 路由的压缩一并关掉。

## 一、背景与结论（证据链）

Codex CLI 现行网络搜索不再走 `/responses` 请求内嵌的托管 `web_search` 工具（那是老通道，
llmio `/v1/responses` 逐字节透传已支持），而是模型调用 function 工具 `web.run`，由 CLI 向
`{provider_base_url}/alpha/search` 发**独立的 POST**（`SearchRequest`：必带 `model` 与
`id`（每线程固定的搜索会话 id）、`commands`、`settings`）。多步操作的连续性靠响应里的
`encrypted_output` 回传**同一家上游**维持。

- llmio `/v1` 只注册了 models / chat/completions / responses / messages，`/v1/alpha/search`
  直接 404——同理 `/v1/embeddings`、`/v1/images/...` 等标准端点也全部 404。
- ChatGPT 登录通道（`chatgpt.com/backend-api/codex/alpha/*`）是 OAuth 私有后端，无法代理；
  唯一可行路径是 Codex 配自定义 provider（`wire_api = "responses"` + `env_key`）指向 llmio。

**方案演进中被否决的四条路**（留档防止回潮）：

1. **按 provider 类型过滤路由未知路径**——DeepSeek 与实现了 `/alpha/search` 的 OpenAI 系中转
   同为 `openai` 型，类型元数据无法区分。
2. **全局 NoRoute 兜底透传**——把存量 404 空间变成转发能力，扩大攻击面，且 NoRoute 绕过
   gin 分组中间件。**否决：全局 `/v1` 未知路径维持 404。**
3. **专属 URL 钉死已知端点的上游**（v2-v4 的"池钉死/禁止 failover"）——已知端点（chat 等）
   本就该全池均衡与容错，钉死太局限。
4. **无名命名空间 + model 查池 ∩ 前缀声明找上游**（v5 的 `/p/v1`）——provider 选择不写明
   在 URL 里，要靠"model → 绑定池 → 声明过滤"三跳推断，机制绕且不可靠。**否决：provider
   直接写在 URL 路径里，未知路径转给它，不需要任何声明字段。**

## 二、设计

| 层 | 路径 | 行为 |
|---|---|---|
| 1. 存量路由 | `/v1/*` 及 `/openai` `/anthropic` `/gemini` 各组 | **零改动**；未知路径照旧 404 |
| 2. 专属 URL | `/p/{provider}/v1/*` | 已知端点=正常均衡（URL 里的 provider **不参与**）；未知路径=**转给 URL 指名的这家** |

**专属 URL（唯一新增面）**：gin 组上正常挂 `authDedicated` 中间件（x-api-key 优先、回落 Bearer），组内**单入口**
`Any("/*path")` 内部分发（静态路由与通配不能同层共存）。按 URL 中的 provider 名解析
（查无此 provider → 404），按其 `forward_mode` 分两档：

- **混合模式（默认，`forward_mode = false`）**：
  - **认识的端点**（chat/completions、responses、messages、models）→ 直调现有 handler，
    正常均衡/桥接/容错/记账，与 `/v1` 完全同语义——URL 里的 provider 不参与路由，全池可用。
  - **不认识的路径** → 方法白名单 GET/POST → **转发给 URL 指名的 provider**：
    `{provider.base_url}{path}`，query/body 原样；按 type 换鉴权头；body（JSON）带 `model`
    且该模型绑定了这家 provider 时改写为它的 `provider_model`（绑定表一跳查询），未绑定则
    原样透传（当它是上游原生模型名）；流式回传；极简日志（状态/字节数/时长，不进 token 统计）。
    **对应上游由 URL 固定**——不存在多家混选，`encrypted_output` 连续性天然成立。
- **纯转发模式（`forward_mode = true`）**：**无条件全量转发**——所有路径所有方法一律
  `{base_url}{path}` 原样直转（含已知端点），**不改写 model**（客户端直接用上游原生模型名，
  零模型绑定配置）。llmio 只做三件事：TOKEN 鉴权、**出站代理**（复用 provider 现有 `proxy`
  字段与 `GetClient` 体系）、流式透传。适用：内网穿透、出站走代理的网络受限环境、不想建模型
  的纯网关用法。

**安全设计（硬性要求）**：

1. 不引入任何 NoRoute/catch-all 于存量路由；全局 `/v1` 未知路径 404 照旧（回归测试断言）。
2. 新增面只有 `/p/{provider}/v1/*`，组内走正常 gin 中间件鉴权；provider 查无即 404——
   转发目标由 URL 显式指名，不存在推断环节，也就不存在推断错。
3. **路径清洗**：转发前拒绝 `..`、控制字符、空路径；拼接只接受相对路径，杜绝 path
   traversal / 代理到任意 URL。
4. **头清洗**：剥离客户端 `Authorization` 与 hop-by-hop 头后再注入上游鉴权。
5. 流式响应边拷边 flush；gzip 排除列表追加 `/p/`（带斜杠，避免前缀误伤 `/providers` 等 SPA 路由）。

**顺带收益——embeddings 等标准端点**：`/v1/embeddings`、`/v1/images/generations`、
`/v1/audio/...`、rerank 等全是"POST + body.model"形状，专属 URL 自动覆盖（混合模式下命中
绑定还改写模型名）。响应 usage 第一期不解析进 token 统计，要计量再升级为已知端点。

**字段**：`providers` 表仅加 `forward_mode`（bool，默认 false）。`passthrough_prefixes`
永久删除——provider 选择由 URL 完成，声明机制没有存在必要。

**已知限制（第一期接受）**：Gemini 型 provider 的模型名在 URL 路径里，不做改写（纯转发模式
原样转发已可用）；混合模式未知路径方法白名单仅 GET/POST（DELETE/PUT 需要时再开）。

## 三、工作拆解（第一期，写范围互斥）

| 卡 | 写范围 | 内容 | 验收信号 |
|---|---|---|---|
| T1 通用转发核心 | `providers/` 新文件 | `Forward(ctx, provider, method, path, query, header, body)`：按 type 套鉴权头、复用 GetClient(provider.proxy)、流式回传、可选 model 改写；**内置路径清洗与头清洗** | 单测：三种 type 鉴权头正确；`..`/控制字符路径被拒；客户端 Authorization 被剥离；body 原样；model 命中该 provider 绑定改写/未绑定不改；proxy 传给 GetClient |
| T2 专属 URL 组 | `main.go`、`handler/provider_route.go`、providers 实体/handler | `/p/:name/v1` 组（authOpenAI）单入口分发：provider 按名解析（404 兜底）；混合模式（已知端点直调现有 handler；未知路径方法白名单 + T1 转给这家 + 绑定改写）；纯转发全量直转；`forward_mode` 管理接口读写 | 已知端点与 `/v1` 同语义（全池均衡可容错，**不落向 URL 指名家**）；`alpha/search` 固定转给指名家且 model 命中绑定改写；查无 provider → 404 |
| T3 专项回归 | 同目录 `_test.go` | 表驱动：无 TOKEN 401；`/v1/alpha/search` **依旧 404（安全回归断言）**；路径清洗；`DELETE` 未知路径 405；embeddings 冒烟；纯转发任意方法直转不改写 | `go test ./...` 全绿 |

Wave 2（收口，串行）：`gofmt -l .` 无输出 → `go vet ./...` exit 0 → `go test ./...` 0 失败 →
显式路径 `git add` + Conventional Commits 提交。

## 四、契约（源码锚点，实施时对齐）

```go
// main.go:121-128 /v1 及各协议组——全部不动，不注册任何 NoRoute 透传

// main.go:73 gzip 排除列表——/p 前缀需追加（"/openai","/anthropic","/gemini","/v1" 现有）
router.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/openai", "/anthropic", "/gemini", "/v1", "/p/"})))

// 已知端点分发=直调现有 handler（handler.ChatCompletionsHandler 等就是 chatHandler(c,...)，
// 换个组挂载不改变其行为）；model 改写查绑定：model_providers 里 (model, provider) → provider_model

// providers/openai_res.go:21 改写套路照抄；:45 GetClient(30*time.Second, o.Proxy) 代理体系直接复用
body, err := sjson.SetBytes(rawBody, "model", model)

// middleware.AuthDedicated——专属 URL 组正常挂中间件（x-api-key 优先、回落 Bearer），无手动鉴权路径
```

## 五、验证

1. `go test ./...` 全绿（含 T3 安全回归断言）。
2. curl 冒烟：
   - `POST /p/{name}/v1/chat/completions`（model=latest）→ 正常均衡、可 failover（与 `/v1`
     同语义，不必然落向 {name} 家）；
   - `POST /p/{name}/v1/alpha/search`（model=latest）→ **固定转给 {name} 家**，命中绑定则
     model 已改写，未绑定原样；`POST /p/{name}/v1/embeddings` → 命中改写；
     `POST /p/不存在/v1/alpha/search` → 404；
     `POST /p/{name}/v1/../api/metrics/stats` → 400（路径清洗）；
   - 纯转发：`POST /p/{name}/v1/chat/completions`（原生模型名）→ 直转不改写；配 proxy 的
     provider 出站走代理；任意方法放行；
   - 安全断言：`POST /v1/alpha/search` → **404 照旧**；无 TOKEN 访问 `/p/*` → 401；
     受限密钥（x-api-key 或 Bearer）请求未授权模型 / 无 model 透传 / 纯转发 → 403。
3. Codex 手册级验证：`~/.codex/config.toml` 配
   `base_url = "http://<llmio>/p/<provider>/v1"`、`wire_api = "responses"`、
   `env_key = "LLMIO_TOKEN"`、`supports_standalone_web_search = true`，会话内触发 web 搜索，
   观察 `alpha/search` 命中日志且始终落同一 provider。
4. Anthropic 原生客户端手册级验证：Claude Code 配
   `ANTHROPIC_BASE_URL=http://<llmio>/p/<provider>`、`ANTHROPIC_API_KEY=<llmio-key>`
   （走 x-api-key 头，与 `ANTHROPIC_AUTH_TOKEN` 的 Bearer 等价），正常对话后确认日志落库；
   换受限密钥请求未授权模型应 403。

## 六、待拍板

- URL 标识用 provider `name` 还是 `id`：建议 `name`（可读），注明改名会使客户端配置失效。
- 纯转发模式要不要"绑定命中仍改写 model"：建议**不改写**（纯透明、零绑定依赖）；要改写用
  混合模式。
- 极简日志形态：复用 ChatLog 加一种 Style（如 `passthrough`），还是独立轻量表——建议前者，
  统计页天然可见，token 列留空。
