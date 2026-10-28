# 专属地址（/p/{slug}）的端点清单

专属地址 `/p/{slug}` 有两种能力：**本地端点**（由本服务自己处理）和**其余路径**
（按转发模式决定 404 还是转发给本提供商）。这份文档说明哪些端点属于本地端点、
三种转发模式下它们各自怎么走，以及前后端各自的实现位置。

替代的旧说法是"关闭模式下只有 5 条已知端点可用"。那句话既不完整（Gemini 型地址
还有原生端点），也不解释各端点如何被处理，用户拿 `/v1/embeddings` 去试收到 404，
只会以为地址配错了。

## 一、地址协议

路径的"长相"由地址协议决定，而不是由固定 `/v1` 决定：

| 提供商类型 | 地址协议 | 基地址（客户端填的 base_url） |
|---|---|---|
| `anthropic` | Anthropic | `{origin}/p/{slug}` |
| `gemini` | Gemini | `{origin}/p/{slug}/v1beta` |
| 其余（`openai`、`openai-res`、空、未知） | OpenAI 兼容 | `{origin}/p/{slug}/v1` |

后端 `addressProtocol(provider.Type)`（`handler/provider_route.go`）与前端
`forwardProtocol(type)`（`webui/src/routes/forwarding/forwarding-utils.ts`）使用同一条
规则。协议只影响**接口形状与返回体形状**，不影响鉴权：三种协议的密钥都按各自的请求头
携带（`Authorization` / `x-api-key` / `x-goog-api-key`）。

## 二、本地端点（任何地址协议）

这五条在查表阶段不看协议，任何专属地址都接管：

| 方法 | 路径 | 处理方式 | 说明 |
|---|---|---|---|
| POST | `/v1/chat/completions` | 全池负载均衡 | OpenAI 形状对话 |
| POST | `/v1/responses` | 全池负载均衡 | 客户端协议与上游不同时自动翻译 |
| POST | `/v1/messages` | 全池负载均衡 | Anthropic 形状对话，同样可翻译 |
| POST | `/v1/messages/count_tokens` | 系统级配置 | 用系统级模型能力配置估算 token 数，不走模型绑定 |
| GET | `/v1/models` | 全池负载均衡 | 返回形状见下 |

`GET /v1/models` 的返回形状有两种：请求头带 `anthropic-version`，或者地址协议本身是
Anthropic，就返回 Anthropic 形状（`data` / `has_more` / `first_id` / `last_id`）；其余
情况返回 OpenAI 形状（`object` / `data`，每项带 `owned_by`）。这样同一份客户端代码
不必为"地址类型"再写一套分支。

## 三、本地端点（仅 Gemini 型地址）

Gemini 型地址额外接管三条原生端点，它们的处理方式也是本地流水线：

| 方法 | 路径 | 处理方式 |
|---|---|---|
| GET | `/v1beta/models` | 全池负载均衡（列出本服务登记的 Gemini 模型） |
| POST | `/v1beta/models/{model}:generateContent` | 全池负载均衡 |
| POST | `/v1beta/models/{model}:streamGenerateContent` | 全池负载均衡（流式，自动补 `?alt=sse`） |

模型名按绑定关系换成上游模型名；模型没有登记时返回"模型不存在"，**不回落透传**——
回落会让一个拼错的模型名看起来像上游的临时故障。

## 四、其余路径

不属于上面两张表的路径，按转发模式处理：

| 转发模式 | `ForwardMode` | 本地端点 | 其余路径 |
|---|---|---|---|
| 关闭（默认） | `nil` | 正常处理 | **一律 404，不转发任何请求** |
| 混合 | `false` | 正常处理 | GET/POST 透传给本提供商 |
| 纯转发 | `true` | **也透传**（先于查表判断） | 所有方法透传给本提供商 |

"关闭"的定义是**完全不转发**：它只服务本地端点，其余路径返回 404。地址本身仍然有效，
客户端仍然要带密钥——这一点与"地址失效"是两回事。

需要转发才能用的端点（关闭模式下 404、混合与纯转发下透传）：

- OpenAI / Anthropic 型地址：`POST /v1/embeddings`、`/v1/completions`、
  `/v1/moderations`、`/v1/rerank`、`/v1/images/generations`、
  `/v1/audio/transcriptions`；
- Gemini 型地址：`POST /v1beta/models/{model}:embedContent`、
  `:batchEmbedContents`、`:countTokens`。

## 五、后端实现

- 分发入口：`handler/provider_route.go` 的 `ProviderDedicatedRoute`。顺序是
  **纯转发判断 → `serveLocalEndpoint` 查表 → 关闭模式 404 → `passthroughUnknown` 透传**。
- `serveLocalEndpoint(c, provider, path)`：本地端点表；`/v1/models` 交给 `serveModels`，
  Gemini 原生端点交给 `serveGeminiModelChat`。
- `serveModels(c, provider)`：按 `anthropic-version` 头或地址协议选择返回形状。
- `geminiModelActionPath(path)`：从 `/v1beta/models/{model}:{action}` 解析模型名与动作。
- `handler/chat.go`：`GeminiGenerateContentHandler` 只保留解析与校验（缺冒号、未知方法
  仍返回 400），处理体抽成 `serveGeminiModelChat`，HTTP 行为与原实现一致。
- 鉴权与权限：本地端点沿用 `chatHandler` 内的模型级校验（带 `model` 的请求校验模型权限，
  不带 `model` 或纯转发/完全改写要求全模型权限的密钥）；其余路径沿用既有透传的鉴权逻辑。

## 六、前端实现

- 唯一数据源：`webui/src/routes/forwarding/forwarding-endpoints.ts`。
  类型 `EndpointHandling = "pool" | "global" | "forward"`，条目形如
  `{ method, path, handling, noteKey? }`；`forwardingEndpoints(type)` 按协议给出清单，
  `forwardEndpointUrls(origin, slug, type)` 拼出完整地址（没有标识时返回空数组）。
- 展示组件：`webui/src/routes/forwarding/forwarding-endpoint-list.tsx`，分两段渲染
  "本地址始终可用"与"需先开启转发"，并在关闭模式下点明后者会 404。
- 使用方：转发透传页（每个地址行可展开清单）、转发设置弹窗（地址预览下方常驻清单）、
  引导弹窗（模式与地址说明指向这份清单）。
- i18n：`forwarding` 命名空间新增 `endpoint_notice.*`、`endpoint_handling.*`、
  `endpoint_note.*`，三语键集一致（`locales.test.ts` 校验）；删除旧的
  `off_notice.*`。

## 七、测试

Go（`handler/provider_route_test.go`，夹具新增 Gemini 型地址 epsilon-name 关闭、
eta-name 混合）：

- `TestDedicatedRouteGeminiNativeModelsList`
- `TestDedicatedRouteGeminiNativeChatGoesLocalPipeline`
- `TestDedicatedRouteGeminiNativeStreamGoesLocalPipeline`
- `TestDedicatedRouteMixedModeGeminiNativeGoesLocal`
- `TestDedicatedRouteOffModeKeepsNonLocalEndpoints404`
- `TestDedicatedRouteMixedModeForwardsEmbeddings`
- `TestDedicatedRouteModelsShapeFollowsProtocol`

前端：

- `forwarding-endpoints.test.ts`：清单顺序、协议差异、URL 拼接；
- `forwarding.test.tsx`：页面清单、设置弹窗清单、复制提示；
- `forwarding-utils.test.ts`：删除已移除的 `forwardOffKnownUrls` 用例。

## 八、行为变化

1. Gemini 型地址在关闭模式下，`/v1beta/models` 与两条原生对话端点由 404 变为可用；
2. Gemini 型地址在混合模式下，原生对话端点由透传改为本地流水线（参与负载均衡、
   模型改写与记账）；
3. `GET /v1/models` 在 Anthropic 型地址或带 `anthropic-version` 头时返回 Anthropic 形状；
4. 关闭模式下 `/v1/embeddings` 等端点仍然 404，行为不变——只是界面不再用"5 条已知端点"
   去描述它，而是逐个列出端点及其处理方式。

## 九、相关文档

- [专属地址与未知路径透传](./unknown-path-passthrough.md)
- [转发透传页面](./forwarding-page.md)
- [透传日志增强](./passthrough-log-enrichment.md)
