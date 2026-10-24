# usage-insights 测试机：用例与预计行为

> 状态：测试机已就绪，基线已采集（2026-10-24，自愈实现落地前）
> 配套设计：`docs/plans/usage-insights-plan.md`
> 验收车：`python e2e/selfheal_check.py`（功能 C）；功能 A / B 是浏览器走查

## 一、测试机构成

复用 `e2e/run_matrix.py` 的机器，三个部分：

1. **桩上游**（`e2e/stub_upstream.py`）新增 **picky 家族**：一族"对参数挑剔的上游"，
   请求体带了指名参数就 400，措辞按成员各不相同——正向成员逐一摆出判据模式表的
   四种短语 + 链式 + 转换器注入参数；负向成员摆出四类"报 400 但不该自愈"的边界。
   行为由**提供商模型名的后缀**决定（`sh-01-picky-openai` → `picky-openai`），
   删掉参数后请求合法，于是"能不能自愈"只取决于 llmio 的判据，不取决于桩。
2. **验收车**（`e2e/selfheal_check.py`）：复用 run_matrix 的建档 / 打请求 / 三处
   核对（客户端拿到什么 · 上游收到什么 · 日志记了什么）小件，跑 12 条用例。
3. **独立二进制 + 独立工作目录**（`e2e/.run/selfheal/`），不碰仓库产物与真实库。

## 二、判定规则

| 组 | 含义 | 现在（未实现） | 实现落地后 |
|---|---|---|---|
| **S 组**（8 条） | 应自愈的场景 | **预期为红**（红就是待办本身） | 必须全绿 |
| **N 组**（4 条） | 判据边界，不该自愈 | **必须绿** | **必须绿** |

N 组在实现前后都必须绿——它绿着的时候，防的是"把不该删的也删了"。另外两条全局
回归：`python e2e/run_matrix.py --upstream stub` 全绿（boom 换家、记账、翻译语义
不回退）；`go test ./...` 全绿。

## 三、用例矩阵（三处核对）

每条按「客户端 / 上游 / 日志」三处核对。**现状**一列是 2026-10-24 基线实测。

### S 组：应自愈

| # | 场景（桩成员） | 客户端发什么 | 现状（实测） | 实施后预计 |
|---|---|---|---|---|
| S1 | 同协议透传，OpenAI 兼容短语（`picky-openai`：`Unrecognized request argument supplied: top_k`） | OpenAI 请求 + `top_k` | 500 `balancer pop err`；上游 1 发；1 条 400 失败行 + 1 条 pop 行 | **200**；上游**恰 2 发**，第 2 发无 `top_k`、`messages`/`max_tokens` 原样；失败行原文含 400 body 与 `Unrecognized`，成功行 `bridge_notes` 含 `self_heal_dropped:top_k`、`Retry=1` |
| S2 | 同上，Pydantic 短语（`picky-py`：`top_k: Extra inputs are not permitted`） | 同上 | 同 S1 | 同 S1，短语判据换 `Extra inputs are not permitted` |
| S3 | 同上，`Unknown parameter: 'top_k'` 短语 | 同上 | 同 S1 | 同 S1，短语判据换 `Unknown parameter` |
| S4 | **桥接·转换器搬过去的参数**：OpenAI 客户端 → Anthropic 上游（`picky-unsupported`：`Unsupported parameter: 'top_k'`）。翻译层会把 `top_k` 搬进 Anthropic 请求体 | OpenAI 请求 + `top_k` | 同 S1 | **200**（choices 形状）；上游 `/anthropic/messages` 恰 2 发，第 2 发无 `top_k`；成功行 `upstream_style=anthropic`、`prompt_tokens=137`、`self_heal_dropped:top_k` |
| S5 | **桥接·转换器注入的参数**：Anthropic 客户端流式 → OpenAI 上游（`picky-injected`）。翻译层为拿用量注入 `stream_options`，这家上游不收 | Anthropic 流式请求 | 同 S1 | **200**（Anthropic SSE：`message_start`…`message_stop`）；上游恰 2 发，第 2 发无 `stream_options`、`stream` 还在；成功行 `self_heal_dropped:stream_options`、**`prompt_tokens=0`**——删掉 `stream_options` 后上游不再报用量，token 记 0。这是自愈的可见代价：比整单失败好，但不是免费的 |
| S6 | 链式两参（`picky-chain`：先拒 `top_k`，删掉后再拒 `logit_bias`） | 请求 + `top_k` + `logit_bias` | 同 S1 | **200**；上游**恰 3 发**，第 3 发两个参数都没有；**2 条** 400 失败行 + 成功行；成功行记账同时含 `self_heal_dropped:top_k` 与 `self_heal_dropped:logit_bias`、`Retry=2` |
| S7 | 预算耗尽（`picky-chain` 拒 4 个参数，预算只许删 3） | 请求 + `top_k`/`logit_bias`/`frequency_penalty`/`presence_penalty` | 500；上游 **1 发**（一刀没删就换家） | 仍失败（非 200），但行为可判定：上游**恰 4 发**（3 次删参 + 1 次终判），最后一发已无前三个参数、仍带 `presence_penalty`；**4 条** 400 失败行，至少一条带 `self_heal_dropped:top_k`；无成功行 |
| S8 | 换家对照（`picky-openai` 权重 100000 + `stub-text` 权重 1） | OpenAI 请求 + `top_k` | **200，但靠备胎**：picky 400 一次即被移出，`stub-text` 接手，发出去的 body **仍带 `top_k`**；无自愈记账 | **200 且不换家**：`sh-08-picky-openai` 恰 2 发（第 2 发无 `top_k`），**`stub-text` 零发**；成功行带 `self_heal_dropped:top_k`。对照说明自愈省掉了一次换家往返，且被拒参数真的没再发出去 |

### N 组：判据边界（报 400 也不许自愈）

四条共同现状 = 共同预计：**客户端非 200；上游恰 1 发（没有重发）；恰 1 条 400
失败行 + 失败收尾行；任何行的 `bridge_notes` 都不出现 `self_heal_dropped:*`。**

| # | 场景（桩成员） | 上游报什么 | 为什么不许删 |
|---|---|---|---|
| N1 | `picky-type` | `Invalid type for 'top_k': expected an integer…` | 参数**在**请求里、名字也对得上，但错的是**值**——删参等于替客户端改语义 |
| N2 | `picky-ghost` | `Unrecognized request argument supplied: logit_bias`（客户端只发过 `top_k`） | 短语命中，但点名的参数**不在实发体里**——删无可删，说明上游抱怨的是别的东西 |
| N3 | `picky-protect` | `Unknown parameter: 'messages'` | 点名的是**保护字段**：删掉整个消息流从来不是自愈 |
| N4 | `picky-ctx` | `This model's maximum context length is 8192 tokens…` | 上下文超长与参数存在性无关，删参救不了 |

N 组同时是**现状行为快照**：这四条在实现落地前就是绿的，落地后再跑仍然绿，
就证明判据没有扩大化。

## 四、基线记录（2026-10-24，实现前）

```
应自愈（S 组，实现落地前预期为红）：0/8
判据边界（N 组，任何时候都必须绿）：4/4
```

S1–S6 现状失败形态一致：`500 {"message":"balancer pop err: no provide items or
all items are disabled, traceID: …"}`——上游的 400 原文只进了 `chat_logs.error`，
客户端拿不到（这正是设计文档 §3.1 的问题陈述）。S7 现状上游只收到 1 发；S8 现状
客户端 200 但由 `stub-text` 兜底、发往上游的请求仍带 `top_k`。

## 五、功能 A / B：浏览器走查预计

测试机 = 真实库备份 + `LLMIO_DB_PATH` 指过去 + `pnpm run build` 后 `go run main.go`。
走查项（实现前均为"不存在该列 / 该图"，实现后按下列判定）：

1. **下钻 Token 列**：分析页 → 下钻任一维度 → 表头出现「Token」列，数值为精确
   千分位（`formatNumber`），悬浮 title 显示「输入 X / 输出 Y」；横向滚动包容
   11 列（`min-w-[960px]`），窄屏不裁字。
2. **主页成本趋势**：总览页趋势区出现第三张「成本趋势」单系列面积图；Y 轴刻度带
   币种符号（`$`/`¥`），tooltip 四位小数；无请求的窗口为空态而非补零长图
   （空桶补零只发生在观测区间内，扫描触顶不补——沿用既有 `fillTrendWindow` 语义）。
3. **分析页成本趋势**：同一视图切到趋势 → 第三张 Panel；**点下钻任一分组名后**，
   三张趋势图连同成本一起按该切片重取——这就是"下钻成本趋势"的落点。
4. **币种缺失兜底**：历史行无币种时 KPI `currency` 为空，成本趋势按裸数字渲染
   不崩页。
5. 三语文案（zh-CN / zh-TW / en）键集一致（`i18n/locales.test.ts` 把门）。

## 六、改动清单（本次随测试机落地）

- `e2e/stub_upstream.py`：picky 家族（10 个成员 + 链式参数表）。
- `e2e/selfheal_check.py`：验收车，12 条用例 + 三处核对。
- 本文档。

run_matrix 既有用例不受影响（picky 只新增行为，不改任何现有成员的响应）。
