# 模型能力与价格自动填写方案

> 状态：待评审 · 拟稿 2026-10-06
> 分支：`feat/model-capability-autofill`（基于上游 `atopos31/llmio` master `18dbe47`）
> 前置阅读：[model-metadata-sources-research.md](model-metadata-sources-research.md)（聚合源横向对比）、[model-metadata-native-endpoints.md](model-metadata-native-endpoints.md)（原生接口逐家核实）

## 1. 目标与范围

在「模型 × 上游」关联的创建/编辑弹窗里，自动填写**三项能力**与**三档价格**，并提供一个总开关控制默认填写行为。

| 目标字段 | 表列 | 类型 | 来源 |
|---|---|---|---|
| 工具调用 `ToolCall` | `model_with_providers` | `*bool` | 聚合源 |
| 结构化输出 `StructuredOutput` | `model_with_providers` | `*bool` | 聚合源 |
| 视觉 `Image` | `model_with_providers` | `*bool` | 聚合源 |
| 输入价 `InputPrice` | `model_with_providers` | `*float64` | 聚合源 |
| 缓存命中价 `CacheReadPrice` | `model_with_providers` | `*float64` | 聚合源 |
| 输出价 `OutputPrice` | `model_with_providers` | `*float64` | 聚合源 |

### 1.1 明确不做

- **不做逐家原生接口适配。** 调研结论是原生接口无法单独完成任务：字段名互不相同（`capabilities.vision` / `supportsImageInput` / `input_modalities` / `supports_image_in` / `features`），而使用最广的 OpenAI 原生 `GET /v1/models` 经官方 OpenAPI 规范确认只返回 `id/object/created/owned_by`（外加可选 `shutdown_date`）。逐家适配要写十几套解析器，换回来的覆盖率还不如一个聚合源。**统一走聚合源。**
- **不做「上下文长度」自动填写。** 全库（`models/model.go`、`handler/`、`webui/src/lib/api.ts`）确认没有上下文/最大输出的列，填写它需要先改表。本方案只填已有列，上下文留待单独一轮（见 §7）。
- **不做 OpenRouter 直连。** 其 ToS（2026-08-31 版）明文禁止自动化抓取与「开发竞争服务」，自托管多上游代理很可能落入该条。改为经 models.dev 间接取得（其数据本身也源自 OpenRouter，但由 MIT 许可的产物承载）。

## 2. 数据源与 fallback 策略

### 2.1 主源：models.dev

| 项 | 值 |
|---|---|
| 端点 | `https://models.dev/api.json` |
| 许可 | **MIT**（`Copyright (c) 2025 models.dev`，仓库 `anomalyco/models.dev`） |
| 规模 | 226 上游 / 8,389 模型 |
| 体积 | 5,315,044 B；**`Accept-Encoding: gzip` 后 530,868 B**（10×） |
| 更新 | 小时级 CI |
| 字段 | `tool_call` 87.3% · `structured_output` 55.8% · `modalities.input` 59.1% · `limit.context` 98.1% · `cost.*` 94.8% |

关键实测约束（已逐条验证）：

- **不支持条件请求**：带正确 `If-None-Match` 仍返回 `200` + 全量，不是 `304`。缓存必须**按时间**，不能靠 revalidate。
- **没有分片端点**：`api.json?provider=anthropic` 静默忽略参数、返回全量 5.3 MB。取子集只能整包拉后本地索引。
- `model-schema.json` **不是字段 schema**，其 `$defs.Model` 是 8k 个模型 ID 的枚举。Go 结构体必须手写。
- 有 `status` 生命周期字段：8,389 个模型里 **260 个 `deprecated`**、71 个 `beta`。

### 2.2 备源：LiteLLM

| 项 | 值 |
|---|---|
| 端点 | `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json` |
| 许可 | **MIT**（`enterprise/` 目录除外；该数据集在仓库根，适用 MIT） |
| 规模 | 4,479 条（排除 `sample_spec`）/ 138 个 `litellm_provider` |

补充价值：models.dev 会淘汰旧模型 ID，LiteLLM 覆盖面更宽。实测互补性——models.dev 缺失的 `claude-3-5-sonnet-20241022`（ctx 200000、tools、vision、schema）、`deepseek-chat`（ctx 131072、tools）、`deepseek-reasoner` 在 LiteLLM 中都存在。

两个必须处理的坑：

- **字段名陷阱**：LiteLLM 没有 `supports_structured_output`（0/4480）。正确字段是 **`supports_response_schema`**。映射错了会静默 0 填充。
- **大小写重复键**：文件里有 `together_ai/baai/...` 与 `together_ai/BAAI/...` 这类重复键。Go 的 `map[string]X` 静默取后者（可接受），但严格解析器会硬失败，测试里要钉住这一点。
- `max_tokens` 是**遗留字段**（有 max_output 时是输出，否则是**输入**上限），不可当最大输出用。

### 2.3 是否引入第三个源

**本轮不引入。** 候选与放弃理由：

| 候选 | 许可 | 放弃理由 |
|---|---|---|
| `kingfs/go-llm-specs` | Apache-2.0 | 上下文/输出 100% 覆盖，但**无价格**，且 5★ 单人维护，作为依赖风险偏高 |
| Vercel AI Gateway | 仓库 Apache-2.0，端点未声明 | 无禁止抓取条款，可作运行时交叉校验，但非许可数据集 |
| modelscan/registry | CC-BY-4.0 | 设计最好，但已停更约一个月 |
| Helicone | Apache-2.0 | 只有 TS 源码无 JSON 产物，接口 502，`packages/cost` 停更约 6 个月 |
| Portkey | MIT | 1,928 条只有 `{id, object, provider, name}`，无任何目标字段 |
| llm-prices.com / InterwebAlchemy | **无 LICENSE** | 默认版权，不可再分发 |

多源 fallback 的**扩展点**留好（§3.1 的 `Source` 接口），加源不改调用方。

## 3. 后端设计

### 3.1 包结构

新增 `service/modelmeta/`，与 `providers/`、`balancers/` 平级：

```
service/modelmeta/
  source.go     Source 接口 + Catalog 索引
  modelsdev.go  models.dev 抓取与解析
  litellm.go    LiteLLM 抓取与解析
  match.go      上游对齐 + 模型 ID 归一化
  suggest.go    三态建议生成
  policy.go     策略读取
```

```go
// Source 一个可查询的模型元数据来源。
type Source interface {
    Name() string
    Fetch(ctx context.Context) (*Catalog, error)
}

// Catalog 是抓取结果建成的内存索引，查询只读它。
type Catalog struct {
    Source    string
    FetchedAt time.Time
    entries   map[string]Entry // key: 归一化后的 (上游, 模型)
}
```

`Entry` 的三个能力字段与三档价格一律用**指针**（`*bool` / `*float64`），直接对应 §4.1 的三态要求。

### 3.2 抓取与缓存

- 按 `Source` 分别缓存，TTL 默认 **24 小时**（源是小时级更新，24h 足够且不打扰上游）。
- 抓取失败时**沿用上一次成功的缓存**，并记下失败时间；无缓存才报错。绝不因为一次网络抖动让用户看到空建议。
- 请求带 `Accept-Encoding: gzip`（5.3 MB → 531 KB），超时 30 秒，走 `providers.GetClient` 以便继承代理配置。
- 抓取在**后台**进行，端点调用不阻塞在 5 MB 下载上：首次调用返回「正在准备数据」，由前端重试。

### 3.3 上游对齐

llmio 的 `Provider` 有 `Type`（`openai`/`anthropic`/`gemini`/`openai-res`）与 `Config`（含 `base_url`）。models.dev 的 provider 有 `id`、`api`（base_url）、`npm`。

匹配顺序：

1. **按 base_url 精确匹配** models.dev 的 `api` 字段。实测命中：`api.deepseek.com`→`deepseek`、`open.bigmodel.cn/api/paas/v4`→`zhipuai`、`api.moonshot.cn/v1`→`moonshotai-cn`、`dashscope.aliyuncs.com/compatible-mode/v1`→`alibaba-cn`、`api.siliconflow.cn/v1`→`siliconflow-cn`。
2. **按 base_url 主机名匹配**一张手工别名表（覆盖官方上游：`api.anthropic.com`→`anthropic`、`api.openai.com`→`openai`、`generativelanguage.googleapis.com`→`google`）。官方上游的 `api` 字段为空（226 家中 200 家带 `api`，且只有 `@ai-sdk/openai-compatible` 的 185 家填了值），所以必须有这一层。
3. **按 `Type` 兜底**：`anthropic`→`anthropic`、`gemini`→`google`、`openai`→`openai`。兜底最容易误配（任何 OpenAI 兼容上游都会被判成 `openai`），因此**只在能同时确定模型 ID 命中时才采用**。

手工别名表与代码同目录、可单测；表里查不到就返回「无建议」，不做模糊猜测。

### 3.4 模型 ID 归一化

`ProviderModel` 是用户输入或从上游 `/v1/models` 拉到的原生名。归一化按**代价递增、命中即停**的顺序，每步都要在测试里钉住：

| 步 | 规则 | 例子 |
|---|---|---|
| 1 | 精确匹配 | `claude-sonnet-4-5` |
| 2 | 忽略大小写 | `DeepSeek-V3` → `deepseek-v3` |
| 3 | 去 `models/` 前缀（Gemini） | `models/gemini-2.5-flash` → `gemini-2.5-flash` |
| 4 | 去 `:free` / `:batch` 后缀 | `anthropic/claude-opus-4.5:batch` |
| 5 | 去日期后缀（`-YYYYMMDD`） | `claude-sonnet-4-5-20250929` → `claude-sonnet-4-5` |

**不做模糊匹配。** 调研实测到两种真实错配：版本漂移（`claude-3-5-haiku-latest`、`gemini-1.5-pro` 在 models.dev 中已不存在）与命名漂移（`gemini-2.0-flash` 存在于 models.dev 但挂在第三方 `qiniu-ai` 下，而非 `google`）。跨上游搜同名会取到**别家**的限额与价格——错的价格比空价格更糟。因此第 5 步之后仍不命中就返回「无建议」。

### 3.5 三态：缺失 ≠ false

models.dev 有 **44% 的模型没有 `structured_output`**，LiteLLM 的 `supports_vision` 只出现在 **42%** 的对话类条目上（1,761 true / 460 明确 false）。因此建议值必须三态：`true` / `false` / **未提供（nil）**，未提供时**不写库、不断言**。

#### 3.5.1 必须先说清一件事：nil 在路由上等同于 false

这一条纠正了本方案早期的一个错误直觉。路由过滤在 `service/chat.go:400-412`：

```go
if before.toolCall {
    modelWithProviderChain = modelWithProviderChain.Where("tool_call = ?", true)
}
```

SQL 三值逻辑下 `NULL = true` 求值为 `NULL`，**不为真**，所以 NULL 行同样被排除。也就是说：

| 库中值 | 客户端要求工具调用时 | 前端表格显示 |
|---|---|---|
| `true` | 入选 | 支持 |
| `false` | 排除 | 不支持 |
| `NULL` | **排除** | **不支持**（`association-list.tsx:99` 的 `ok` 为 null，走 falsy 分支） |

结论：**nil 不是「未知所以放行」，而是「等同于不支持」**。要让某个上游接受工具调用，该列必须是显式 `true`。

这改变了自动填写的正确姿势：

- **源给了值就写**（`true` 或 `false`）——这是自动填写的价值所在，尤其能把大量手工漏配的 `true` 补上。
- **源没给值就不动**——不是因为它「保持可路由」，而是因为**不该把不知道的事写成断言**，并且**不能覆盖用户手工设的 `true`**。
- 若源缺失而该行是 NULL，路由结果与写 `false` **相同**；但两者语义不同：NULL 是「没配过」，`false` 是「确认不支持」。写成 `false` 会让后续想用「NULL 表示待补」的策略失去判据。

#### 3.5.2 覆盖既有值的风险

真正会**改变路由行为**的是覆盖：用户手工把 `ToolCall` 设为 `true`，自动填写若因源缺失或源数据有误而写 `false`，该上游就会从工具调用请求的候选池里消失。这是 §4.3 里 `Overwrite` **默认 false** 的直接理由。

- `deprecated` 状态的模型**默认不给建议**（260 个），策略里可放开。

### 3.6 价格单位与币种

- models.dev / LiteLLM 的 `cost.*` 是 **USD / 百万 token**，与 llmio 的「每百万 tokens 单价」口径一致。
- 现有表单默认 `currency: "CNY"`。自动填写时**同时写入 `Currency: "USD"`**，否则会拿美元数字当人民币计价——实测日志页成本按 `currency` 渲染，这个错会直接体现为金额错误。
- 三档价格任一为 nil 就不写该档；不做 0 填充。

#### 3.6.1 价格的 NULL 已被启动迁移抹平

`models/init.go:87-96` 在每次启动时把三档价格的 NULL 全部归零：

```go
zero := 0.0
if _, err := gorm.G[ModelWithProvider](DB).Where("input_price IS NULL").Update(ctx, "input_price", &zero); err != nil {
```

（`currency` 同理，NULL/空归 `CNY`。）

后果有两个，都要认下来：

1. **价格列实际上没有「未知」态**，只有 `0`。`0` 既表示「免费」也表示「没配」。因此「源缺失就不写」在价格上**无法**通过 NULL 表达，只能是不改动原值。
2. 想在价格上区分「未知 / 免费 / 已定价」，需要新增列或改用三态约定，属于表结构变更，**不在本轮范围**（与 §7 的上下文列一并考虑）。

本轮的价格行为因此是：**源有值且允许覆盖（或当前为 0）才写**；源无值则一律不动。判断「当前为 0」时要注意 `0` 可能是用户真的填了免费，所以默认策略下仍需用户显式开启 `Overwrite` 才会改写非零值。

## 4. 接口设计

### 4.1 建议查询

```
GET /api/model-providers/metadata?provider_id=<id>&provider_model=<name>
```

响应：

```json
{
  "matched": true,
  "source": "models.dev",
  "provider": "anthropic",
  "model": "claude-sonnet-4-5",
  "match_rule": "exact",
  "status": null,
  "tool_call": true,
  "structured_output": true,
  "image": true,
  "input_price": 3,
  "cache_read_price": 0.3,
  "output_price": 15,
  "currency": "USD"
}
```

`matched: false` 时其余字段为 `null`，并带 `reason`（`no_provider_match` / `no_model_match` / `catalog_unavailable`），前端据此给不同文案。

三个能力与三档价格在 JSON 里用 `*bool` / `*float64`，`null` 即「源未提供」。

### 4.2 保存时自动填写

在 `CreateModelProvider` / `UpdateModelProvider` 里，**当请求未显式给出该字段**且策略开启时补值。判定「未显式给出」需要把请求结构体的这三个能力与三档价格改成指针——现在 `ModelWithProviderRequest` 用的是非指针 `bool`/`float64`（`handler/api.go:49`），无法区分「用户取消勾选」与「用户没填」。

改动点：

```go
// 现在
ToolCall bool `json:"tool_call"`
InputPrice float64 `json:"input_price"`

// 改为
ToolCall *bool `json:"tool_call"`
InputPrice *float64 `json:"input_price"`
```

这是**破坏性契约变更**，前端必须同步（§5）。旧客户端不传这些字段时落 nil，此时策略开启才补值，关闭则维持现状（落 nil → 该行原值不变）。

### 4.3 策略

复用 `Config` 表的通用键值端点（`GET/PUT /api/config/:key`），新增键：

```go
KeyModelAutofillPolicy = "model_autofill_policy"
```

```go
type ModelAutofillPolicy struct {
    // Enabled 是总开关：关闭后保存不再自动补值，手动查询仍可用。
    Enabled bool `json:"enabled"`
    // Overwrite 决定补值是否覆盖已有值。默认 false：只补空。
    Overwrite bool `json:"overwrite"`
    // AllowDeprecated 是否给已废弃模型建议。默认 false。
    AllowDeprecated bool `json:"allow_deprecated"`
    // Sources 是按序尝试的来源，留空用默认 ["models.dev", "litellm"]。
    Sources []string `json:"sources"`
}
```

**默认值：`Enabled=false`。** 理由与仓库既有约定一致（见 `LogReclaimPolicy` 的注释：默认替用户做主，等于把有感知的动作变成默认行为）——自动填写会改动用户在表单里看到的值，属于有感知的行为，应由用户显式开启。

`Overwrite=false` 默认是调研的直接结论：聚合源存在可测缺口，覆盖用户已确认的值风险高于收益。

## 5. 前端设计

### 5.1 关联弹窗

在「模型能力」分组标题右侧加一个**「自动填写」按钮**（次要样式，带图标），点击后：

1. 调 §4.1 端点；
2. 命中则把三态值填入表单：`true`→勾选、`false`→取消勾选、**`null`→不动且不改动提示**；
3. 价格同理，并同步把币种切到 `USD`；
4. 给出结果提示：填写了哪几项、哪几项源未提供。

未命中时提示文案要区分原因，不能一律「未找到」——`catalog_unavailable`（数据还没准备好）与 `no_model_match`（确实没有这个模型）对用户是两件事。

### 5.2 弹窗内说明文案

在弹窗底部（或能力分组下）加一行常驻说明，指向开关位置。要求**官方、小白化**，不用口语：

> 自动填写的数据来自公开模型数据库，可能滞后于上游实际能力。默认填写行为可在「系统配置」→「模型能力与价格自动填写」中调整。

### 5.3 系统配置页

在 `webui/src/routes/config.tsx` 新增一张卡片（沿用现有 `Card` + 编辑 `Dialog` + `Switch` 的结构），暴露 §4.3 的四个字段。

### 5.4 文案规范

- 一律「官方、小白化」：陈述句、说清「是什么/影响什么」，不用「搞定」「一键」「试试」这类口语。
- 三语文案（`zh-CN` / `zh-TW` / `en`）同步补齐，键加在 `i18n/locales/*/models.json` 的 `association_form` 与 `config.json`。
- 现有 `association_form` 已有 `capabilities`、`billing_section` 等键，新键沿用同一层级。

## 6. 验证

按「单侧 → 集测 → 真机 → 用户实测」四段推进，每段都要有可复现的证据。

### 6.1 单侧（单元测试）

| 测试 | 钉住什么 |
|---|---|
| models.dev 解析 | 从**真实响应片段**（固化为 testdata）解析出三能力+三价格 |
| LiteLLM 解析 | `supports_response_schema` 映射正确；`supports_structured_output` 不被误用 |
| LiteLLM 重复键 | 大小写重复键不导致解析失败 |
| 三态 | 源缺字段时建议为 nil，**不是 false** |
| nil 的路由语义 | 构造 `tool_call=NULL` 的行，断言它在 `before.toolCall=true` 时**被排除**（钉住 §3.5.1 的结论，防止后人误以为 NULL 会放行） |
| 覆盖保护 | `Overwrite=false` 时，源给 `false` 不得覆盖库里已有的 `true` |
| 价格 NULL 迁移 | 断言启动迁移把价格 NULL 归零（钉住 §3.6.1，说明价格无「未知」态） |
| 归一化五步 | 每步各一例；第 5 步后不命中要返回未匹配 |
| 上游别名表 | 官方上游（api 字段为空）与 openai-compatible 上游都能命中 |
| 价格币种 | 自动填写必带 `USD` |
| deprecated | 默认不给建议，放开后给 |
| 策略默认值 | `Enabled=false`、`Overwrite=false` |

### 6.2 集测（集成测试）

- `handler` 层：新端点的命中/未命中/数据源不可用三条路径；`Create/Update` 在策略开/关 × 字段给/不给 的四种组合下的落库结果。
- 契约测试：请求结构体改指针后，前端 `api.ts` 类型与后端 JSON 标签一致（沿用仓库既有 `docs/api-frontend-parity.md` 的做法）。
- `go test ./...` 全绿（Windows 上按 AGENTS.md 设 `GOCACHE=$PWD/.gocache`）。

### 6.3 真机

- `go run main.go` 起服务，配一个真实上游（DeepSeek 或 Zhipu，二者 base_url 能命中别名表），走一遍：打开弹窗 → 点自动填写 → 保存 → 回读确认落库值。
- 前端 `pnpm run build` + `pnpm run lint` 通过。
- 网络受限场景验证：断网时端点返回 `catalog_unavailable` 而非 500，且沿用旧缓存。

### 6.4 用户实测

交给用户在真实上游组合上验证，重点确认：自动填的价格与上游账单口径是否一致、币种是否正确、能力勾选是否符合预期。

## 7. 遗留与后续

- **上下文长度**：需要先在 `ModelWithProvider` 加列（`ContextWindow int`、`MaxOutputTokens int`）并补前端展示，属于独立的表结构变更，单独一轮做。数据侧已就绪（models.dev `limit.context` 98.1%、`limit.output` 97.3%）。
- **第三数据源**：`Source` 接口已留扩展点，`go-llm-specs`（Apache-2.0、零网络、上下文 100%）可作为离线种子引入，代价是引入一个 5★ 单人维护的依赖。
- **OpenRouter 间接依赖**：models.dev 的数据源自 OpenRouter。当前经 MIT 产物消费，风险已转移但未消除；若上游收紧，需评估切到 LiteLLM 为主源。

## 8. 提交切分

按「一类修改一类提交」：

| # | 提交 | 内容 |
|---|---|---|
| 1 | `docs: 模型能力与价格自动填写方案` | 本文档 + 研究文档归档 |
| 2 | `feat(modelmeta): 聚合源抓取与解析` | `Source` 接口、models.dev、LiteLLM、缓存、testdata |
| 3 | `feat(modelmeta): 上游对齐与建议生成` | 别名表、五步归一化、三态建议 |
| 4 | `feat(config): 自动填写策略` | `KeyModelAutofillPolicy`、默认值、读写 |
| 5 | `feat(api): 自动填写建议端点与保存时补值` | 新端点、请求结构体改指针 |
| 6 | `feat(webui): 关联弹窗自动填写与说明` | 按钮、结果提示、常驻说明、三语文案 |
| 7 | `feat(webui): 系统配置页自动填写开关` | 新卡片与编辑弹窗 |
| 8 | `test: 自动填写单测与集测` | §6.1/§6.2 的用例 |

第 5 与第 6 笔必须**同时**可编译（请求结构体改指针是破坏性契约变更），中间不插入其他提交。
