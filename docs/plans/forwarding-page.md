# 转发透传专页（Forwarding）

- 日期: 2026-10 · 状态: **已实施**
- 前置设计: [提供商专属 URL 透传](./unknown-path-passthrough.md)（v6，已实施）
- 日志增强: [透传日志内容增强](./passthrough-log-enrichment.md)

## 背景与问题

v6 的透传能力已经可用，但配置面和地址语义混在提供商 CRUD 里，用起来有四类问题：

1. **设置会被冲掉**：`forward_mode` 原先放在提供商表单里，`UpdateProvider` 用
   `Select("name","type","config",...,"forward_mode")` 全量更新——改名称或 base_url
   时只要没带该字段，转发设置就被清空。
2. **名称即地址**：专属地址是 `/p/{name}/v1`，改名称必然丢地址，且地址标识无法单独
   规划（例如想用短标识 `gw` 而不是长名称）。
3. **地址一律 /v1 结尾是错的**：`dedicatedBaseUrl` 固定拼 `/p/{name}/v1`。
   Anthropic SDK 自己拼 `/v1/messages`，Gemini 客户端拼 `/models/{model}:...`，
   把带 `/v1` 的地址粘给它们，请求路径必然错位（Anthropic 会拼出 /v1/v1/messages）。
4. **没有说明入口**：透传的模式、改写、鉴权规则只写在设计文档里，页面上没有任何解释，
   新用户只能靠试。

## 设计决策

| 决策 | 内容 |
|---|---|
| 地址与命名解耦 | `Provider.ForwardSlug` 单独存地址标识，`/p/{slug}/...`；改名不影响地址 |
| 设置与 CRUD 解耦 | 转发设置只走 `PUT /api/providers/:id/forward`；通用更新不再读写转发列 |
| 改写三态 | `none` 原样 / `known` 认识且绑定才改写 / `all` 固定改写为 `ForwardTargetModel` |
| 地址按协议生成 | openai 系 `/p/{slug}/v1`；anthropic `/p/{slug}`；gemini `/p/{slug}/v1beta` |
| 首次引导 | 进入专页自动弹说明（可重开），已读标记独立版本化 localStorage 键 |

## 一、数据模型与迁移

`models.Provider` 在 `ForwardMode` 之后新增三列（`models/model.go`）：

| 字段 | 类型 | 语义 |
|---|---|---|
| `ForwardSlug *string` | 可空、uniqueIndex | 专属地址标识；NULL = 不提供地址，可多行 NULL |
| `ForwardRewrite string` | 文本 | `none` / `known` / `all`；空串按 `known`（升级前旧行） |
| `ForwardTargetModel string` | 文本 | `all` 的固定上游模型名；其余策略下恒为空 |

`consts/consts.go` 新增 `ForwardRewriteNone/Known/All/Default`（Default = known）。

迁移在 `models/init.go` 的 AutoMigrate 与既有回填之后执行 `backfillForwardSettings`
（失败 panic），规则幂等：

1. **slug 回填**：按 id 升序，`forward_slug IS NULL` 且名称满足
   `common.IsSafeForwardSlug`、又不与已占用 slug 重名的，写入 slug = name；
   不安全或重名的保持 NULL（用户可在专页手动设置）。
2. **rewrite 回填**：`forward_mode = true`（纯转发）且 rewrite 为空的 → `none`
   （保持旧行为：纯转写原样转发）；`forward_mode` 为 NULL/false 且为空的 → `known`
   （保持旧行为：混合模式命中绑定才改写）。已有 rewrite/target 的行不覆盖。

## 二、后端接口

### 路由（`main.go`）

```go
p := router.Group("/p", authDedicated)
p.Any("/:slug/*path", handler.ProviderDedicatedRoute)   // 原 /:name/*
api.PUT("/providers/:id/forward", handler.UpdateProviderForward)
```

已知端点分发与全局 `/v1` 完全不动；全局 `/v1` 未知路径仍然 404（安全回归测试
`TestGlobalV1UnknownPathStays404` 断言）。

### `PUT /api/providers/:id/forward`

请求体（`handler/provider_forward.go`）：

```json
{ "slug": "gw", "mode": "mixed", "rewrite": "all", "target_model": "gpt-4.1" }
```

| 校验 | 失败响应（管理接口 = HTTP 200 + code） |
|---|---|
| id 非数字 | 400 `Invalid ID format` |
| id 不存在 | 404 `Provider not found` |
| mode 非 off/mixed/pure | 400 `invalid forward mode` |
| rewrite 非 none/known/all（空 = known） | 400 `invalid forward rewrite` |
| slug 非空但不满足安全规则 | 400 `invalid forward slug: ...` |
| mode ≠ off 但 slug 为空 | 400 `forward slug is required unless forwarding is off` |
| slug 与其他提供商重复 | 400 `forward slug already in use` |
| rewrite = all 但 target 为空 | 400 `forward target model is required for full rewrite` |
| target 超 200 字符或含控制字符 | 400 `invalid forward target model` |

- 模式映射：off → `ForwardMode = nil`，mixed → `new(false)`，pure → `new(true)`。
- 落库用 map 形式的 Updates 显式写 NULL（清空 slug / 关闭转发必须能落库）。
- 成功后重查并返回完整 provider；唯一索引冲突也映射为 400 同一文案。
- `CreateProvider` 删除 `forward_mode` 入参，创建时用 `defaultForwardSlug` 按名称
  推导默认 slug（安全且未被占用才写），`ForwardRewrite` 置 `known`。
- `UpdateProvider` 的 Select 与赋值不再包含任何转发列——通用更新不可能再覆盖转发设置
  （测试 `TestUpdateProviderPreservesForwardSettings`）。

## 三、改写引擎（混合未知路径与纯转发共用）

`handler/provider_route.go`：`forwardRewritePolicy` 归一化策略（空串 → known），
`rewriteForwardModel` 产出 (转发路径, 实际上游模型名)：

| 策略 | body 里的 model | Gemini 风格路径 |
|---|---|---|
| `none` | 不动 | 不动 |
| `known` | 该模型在本提供商有启用绑定时改为绑定上的 `provider_model`（`rewriteModelForProvider`） | 不动 |
| `all` | 存在且是合法 JSON 就改为 `ForwardTargetModel` | `/models/{id}:action` 中的 id 一并改为 target（`rewriteModelInForwardPath`）；target 含 / : 空白时只改 body 不改路径 |

`all` 缺 target 时返回 500 `forward target model is not configured`（严格失败，不静默
放过）。鉴权矩阵：

| 模式 | 改写 | 鉴权 |
|---|---|---|
| 关闭（nil） | — | 未知路径 404，不转 |
| 混合 | none/known | 有 model → 按模型校验 `validateAuthKey`；无 model → 要求全量权限 |
| 混合 | all | **要求全量权限**（客户端模型名不再是鉴权维度），403 `forward rewrite requires a token with full model permission` |
| 纯转发 | 任意 | 要求全量权限，403 `forward mode requires a token with full model permission` |

其余安全约束沿用 v6：路径 `NormalizeForwardPath`（只剥开头完整 `/v1` 段）+
`SanitizeForwardPath`（`..`、控制字符、非 / 开头拒绝）；头清洗；hop-by-hop 头剔除；
混合未知路径方法白名单仅 GET/POST（405），纯转发任意方法。

## 四、地址规则（按协议，不再一律 /v1）

**地址是客户端配置的 base_url，不是完整请求 URL**——客户端会自己往后拼路径，
所以三种协议需要的基地址不同：

| 提供商类型 | 专属地址（base_url） | 客户端实际拼的路径 |
|---|---|---|
| `openai` / `openai-res` / 未知类型 | `{origin}/p/{slug}/v1` | `/chat/completions` 等 |
| `anthropic` | `{origin}/p/{slug}` | `/v1/messages`（SDK 自拼） |
| `gemini` | `{origin}/p/{slug}/v1beta` | `/models/{model}:generateContent` |

该规则由 `forwardBaseUrl(origin, type, slug)`（`webui/src/routes/forwarding/forwarding-utils.ts`）
统一生成，页面地址列、复制按钮、设置弹窗预览、引导弹窗地址表全部走它；未知/空类型按
OpenAI 兼容处理（与服务端转发假设一致）。

注意：`ForwardMode = off` 不等于没有地址——关闭只让未知路径 404，已知端点仍走全池
负载均衡；只要 slug 存在，页面照样展示地址，模式列单独标注「关闭」。

## 五、前端

- 路由与导航：`App.tsx` lazy `routes/forwarding` + `Route path="forwarding"`；
  `layout.tsx` 配置组在「提供商」之后加「转发透传」（`FaExchangeAlt`）。
- i18n：新命名空间 `forwarding`（zh-CN / zh-TW / en 三语，键集一致，`locales.test.ts`
  自动校验）；`i18n/index.ts` 的 imports / resources / ns / CustomTypeOptions 四处注册；
  `layout.json` 三语加 `nav.forwarding`。
- 页面 `webui/src/routes/forwarding.tsx`：拉全量 providers，表格 5 列
  （提供商 / 专属地址 / 模式 / 改写策略 / 操作）+ 移动端卡片；地址列点击复制
  （`copyToClipboard`，无 slug 显示「未设置」）；空态引导去 /providers；错误态可重试；
  保存后重新拉取。页面不做筛选。
- 设置弹窗 `forwarding-settings-dialog.tsx`：slug 输入（`/p/` 前缀 + 地址预览 +
  协议提示 + 改 slug 的「旧地址失效」警告）、模式三卡片、改写三卡片、all 时的目标模型
  输入；客户端校验与服务端同规则（slug 必填/格式、all 目标必填）；保存调
  `updateProviderForward` 后 toast + onSaved。
- 首次引导 `forwarding-guide-dialog.tsx`：五节（地址怎么读 / 三种模式 / 三种改写 /
  鉴权 / 常见坑），地址表用当前 origin 展示三种协议的基地址与客户端拼接路径。
- 已读标记 `guide-storage.ts`：`llmio-forwarding-guide-seen-v1`，读写都 try/catch
  静默（隐私模式可用）；页面初始 `!hasSeenForwardingGuide()` 自动弹，关闭（含 X）即
  写标记，右上「使用说明」可重开。
- 提供商页清理：删除旧「专属地址」列/复制按钮/纯转发徽章/`copyDedicatedUrl`、
  表单里的 `forward_mode` 三卡片与地址预览、`dedicatedBaseUrl`、`isSafeProviderName`；
  `name_hint` 改为「改名不影响专属地址」，三语 providers.json 同步删键。

## 六、日志

透传与纯转发共用一次转发实现（`forwardUpstream`），落一条 `Style = passthrough` 的
ChatLog，并可选写 ChatIO（请求体 + 响应体）、解析 usage、记录首字节耗时与
`request_path`。细节见 [passthrough-log-enrichment.md](./passthrough-log-enrichment.md)。

## 七、测试与验证

- `handler/provider_forward_test.go`（13 个用例）：保存/关闭清空/默认 known/非法与重复
  slug/缺 target/非法 mode/未知 provider/通用更新保留转发列/Create 默认 slug 三例。
- `handler/provider_route_test.go`（39 个用例）：slug 定位（`WithoutSlugNotFound`、
  `SlugIndependentOfName`）、三态改写（pure+known、pure+all、mixed+all、Gemini 路径）、
  all 缺 target 500、受限密钥 403、日志增强 6 例等。
- `models/forward_settings_test.go`：回填规则、幂等、不覆盖已有值。
- 前端：`forwarding-utils.test.ts`（协议地址、mode/rewrite 映射含空串）、
  `forwarding.test.tsx`（四态渲染、首次引导只弹一次并可重开、设置校验与保存 payload）。
- 验证命令：`go test ./...`；`cd webui && pnpm run lint && pnpm run test && pnpm run build`。

## 八、已知限制

- 混合模式未知路径仍只放行 GET/POST（DELETE/PUT 需要时再开）。
- `all` 的 target 含 `/`、`:`、空白时只改写 body 里的 model，不改 Gemini 路径。
- slug 一经修改，旧地址立即失效（客户端需更新 base_url）；页面在改 slug 时给出警告。
- 已有不安全名称或重名名称的提供商升级后没有默认 slug，需要手动设置一次。
- 地址按 `provider.Type` 推导协议；类型为空或自定义时按 OpenAI 兼容带 `/v1`。

## 文件清单

```
models/model.go                     ForwardSlug / ForwardRewrite / ForwardTargetModel
models/init.go                      backfillForwardSettings
common/slug.go                      IsSafeForwardSlug
consts/consts.go                    ForwardRewrite* 常量
handler/provider_forward.go         UpdateProviderForward + defaultForwardSlug
handler/provider_route.go           slug 寻址 + 三态改写 + 转发实现（含日志）
handler/api.go                      ProviderRequest/Create/Update 去掉 forward_mode
main.go                             路由：/p/:slug/*path、PUT /providers/:id/forward
webui/src/routes/forwarding.tsx     专页
webui/src/routes/forwarding/*       设置弹窗、引导弹窗、纯逻辑、已读标记
webui/src/lib/api.ts                Provider 字段 + updateProviderForward
webui/src/i18n/locales/*/forwarding.json
webui/src/i18n/locales/*/layout.json  nav.forwarding
```
