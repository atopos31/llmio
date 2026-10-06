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
- **不做 OpenRouter 直连。** 其 ToS（2026-08-31 版）第 7 节「Prohibited Conduct」把禁抓取列为**独立并列项**，原文：

  > develop, support or use software, devices, scripts, robots or any other means or processes (such as crawlers, browser plugins, add-ons or any other automated technology) to scrape or copy any information on the Site or the Services

  这一项**没有商业用途限定**，与相邻的"reselling API access / developing a competing service"是各自独立的分号并列项。因此「非商业自用」**不能豁免**——禁止的是抓取行为本身，触发条件是"BY USING THE SERVICE"。
  
  两个有利事实但不足以推翻上述：`robots.txt` 为 `User-Agent: * / Allow: /`（只禁 `/seo/`），故"绕过技术措施"不成立；该端点无鉴权、带 `Access-Control-Allow-Origin: *`，实际执法风险对个人自托管用户很低。但"风险低"不等于"没问题"。
  
  改为经 models.dev（MIT）间接取得。**如实说明**：models.dev 的数据本身也源自 OpenRouter，这转移了"谁抓的"、没有消除上游限制——但那是 models.dev 与 OpenRouter 之间的事，我们消费的是 MIT 许可的成品。

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

### 4.2 自动填写只在弹窗内发生

自动填写是**关联弹窗里的前端行为**：开关开启时，弹窗里选定「上游 + 上游模型」后拉取建议并预填表单，用户在保存前可逐项复核与修改。

**不改服务端的保存逻辑。** `CreateModelProvider` / `UpdateModelProvider` 与 `ModelWithProviderRequest` 保持原样，不做"保存时补值"。理由有三条：

1. **不需要**：需求就是关联时自动填写，弹窗内预填已经完全满足，值随表单正常提交即可。
2. **做了也无效**：`use-model-provider-form.ts:65` 的 `getDefaultFormValues` 把三项能力默认成 `false`，`buildPayload`（同文件 122-146 行）又**无条件发送**这三项——界面保存时永远显式传 `false`，服务端根本没有"未给出"这个状态可言。若只做服务端补值，新建关联时必然永远不补，功能看起来接好了、实际一次都不会触发。
3. **省掉一次破坏性契约变更**：原本要判定"未给出"就得把请求结构体的 `bool`/`float64` 改成指针，那是会打断现有 API 客户端的改动。不做这条路，这个变更整条消失。

因此本轮**只新增一个只读建议端点**（§4.1），写入路径完全沿用现有实现。

### 4.3 策略

复用 `Config` 表的通用键值端点（`GET/PUT /api/config/:key`），新增键：

```go
KeyModelAutofillPolicy = "model_autofill_policy"
```

```go
type ModelAutofillPolicy struct {
    // Enabled 是总开关：关闭后弹窗不再自动预填。
    Enabled bool `json:"enabled"`
    // Overwrite 决定预填是否覆盖已有值。默认 false：只补空。
    Overwrite bool `json:"overwrite"`
    // AllowDeprecated 是否给已废弃模型建议。默认 false。
    AllowDeprecated bool `json:"allow_deprecated"`
    // Sources 是按序尝试的来源，留空用默认 ["models.dev", "litellm"]。
    Sources []string `json:"sources"`
}
```

**默认值：`Enabled=true`（默认启用）。**

理由：这是本轮明确的产品选择——开箱即用优先。与 `LogReclaimPolicy` 那类「默认关闭」不同，自动填写是**纯前端的、幂等的、可在表单里逐项复核的**动作：它只往用户正看着的表单里预填值，不落库、不占锁、不改变运行中的服务，用户点保存前始终有机会改。这与回收/VACUUM 那种「一开就占写锁」的动作性质不同，因此不套用同一默认。

但**配套的三条约束必须同时生效**，否则「默认启用」会变成「默认改坏数据」：

1. `Overwrite=false`：不覆盖已有值，只补空。
2. `AllowDeprecated=false`：260 个 `deprecated` 模型默认不给建议。
3. 源未提供的能力**不写**（§3.5），不得把缺失当 `false`。

`Overwrite=false` 是调研的直接结论：聚合源存在可测缺口（`structured_output` 仅 55.8%、LiteLLM 的 `supports_vision` 仅覆盖 42% 对话条目），覆盖用户已确认的值风险高于收益。

#### 4.3.1 预填与覆盖的关系

「默认启用」+「不覆盖」在**新建**时没有矛盾：新行的三项能力是表单默认 `false`、三档价格是 `0`，预填会正常写入。矛盾只出现在**编辑既有行**时——那时 `Overwrite=false` 意味着预填**不改动**已有值，用户会看到"已跳过 N 项（已有值）"。

这是刻意的：编辑既有关联时，用户已确认过的能力与价格不应被社区数据静默改写。用户若想强制刷新，可在弹窗里手动改，或在策略里临时打开 `Overwrite`。

#### 4.3.2 用户手工改过的不覆盖

除"已有值"之外，还有一层同类的保护：用户在这次弹窗里**已经手工改过**的字段，后续不再被预填改写（见 §5.1）。两层合起来保证自动填写只填"空着的"和"用户没碰的"。

## 5. 前端设计

### 5.1 关联弹窗

弹窗是自动填写的**唯一入口**（§4.2）：开关开启时，选定「上游 + 上游模型」后**自动拉取并预填**，无需用户额外点击。同时保留一个**「重新填写」按钮**供手动重取。

预填规则：

1. 调 §4.1 端点；
2. 命中则把三态值填入表单：`true`→勾选、`false`→取消勾选、**`null`→不动**；
3. 价格同理，并同步把币种切到 `USD`；
4. 给出结果提示：填写了哪几项、哪几项源未提供、哪几项因已有值被跳过（§4.3.1）。

三个必须守住的交互细节：

- **不得打断用户输入**：预填只在「上游 + 模型」刚确定、且用户尚未手工改动这些字段时进行。用户改过之后不再自动覆盖（§4.3.2），避免"正在输入时值被改掉"。
- **失败不阻塞保存**：数据源不可用时，弹窗照常可填可存，只在能力分组下给一行提示。自动填写是便利功能，不能成为保存的前置条件。
- **原因要分开**：`catalog_unavailable`（数据还没准备好，可重试）与 `no_model_match`（确实没有这个模型）对用户是两件事，不能都显示「未找到」。

### 5.2 弹窗内说明文案

在弹窗底部（或能力分组下）加一行常驻说明，指向开关位置。要求**官方、小白化**，不用口语：

> 自动填写的数据来自公开模型数据库，可能滞后于上游实际能力。可在「系统配置」→「模型能力与价格自动填写」中调整默认填写行为。

### 5.3 系统配置页

在 `webui/src/routes/config.tsx` 新增一张卡片（沿用现有 `Card` + 编辑 `Dialog` + `Switch` 的结构），暴露 §4.3 的四个字段。

因为 `Enabled` 默认为**开**，卡片上要有一句说明当前行为，避免用户以为没配过就是关闭：

> 当前状态：已启用。新建关联时将自动填写模型能力与价格，可在保存前逐项修改。

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
| 策略默认值 | `Enabled=true`、`Overwrite=false` |
| 预填不打断输入 | 用户改过字段后，再次预填不得覆盖（前端测试） |
| 写入路径未变 | `Create/UpdateModelProvider` 的请求与落库行为与改动前一致（钉住 §4.2「不改服务端保存逻辑」） |

### 6.2 集测（集成测试）

- `handler` 层：新端点的命中/未命中/数据源不可用三条路径。
- 回归：`Create/UpdateModelProvider` 在策略开/关下行为一致（写入路径不读策略，§4.2）。
- 契约测试：新端点的响应字段与前端 `api.ts` 类型一致（沿用仓库既有 `docs/api-frontend-parity.md` 的做法）。
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
| 5 | `feat(api): 自动填写建议端点` | 只读新端点，**不动写入路径** |
| 6 | `feat(webui): 关联弹窗自动填写与说明` | 自动预填、重新填写按钮、结果提示、常驻说明、三语文案 |
| 7 | `feat(webui): 系统配置页自动填写开关` | 新卡片与编辑弹窗 |
| 8 | `test: 自动填写单测与集测` | §6.1/§6.2 的用例 |

第 5、6 笔之间前端还调不到端点，属正常的分笔顺序（后端先就绪）。**没有任何破坏性契约变更**——写入路径与请求结构体都不动，因此每笔都可独立编译、独立通过测试。
