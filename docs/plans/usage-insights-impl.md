# usage-insights 实施方案（分步实施册）

> 状态：实施稿，按步执行；每步独立提交、独立验证、独立可回滚
> 设计依据：`docs/plans/usage-insights-plan.md`（含 §3.10 C6、§3.11 C7）；
> 验收线：`docs/plans/usage-insights-test-plan.md` + `python e2e/selfheal_check.py`
> 拟稿 2026-10-24。所有行号以分支当前基线（`e662cc6`）为准

## 总提交序列

| 步 | 提交 | 内容 | 前置 |
|---|---|---|---|
| 1 | `feat(analytics): 下钻表新增 Token 列` | A1 | 无 |
| 2 | `feat(stats): 趋势点新增成本` | B1 | 无 |
| 3 | `feat(webui): 主页与分析页新增成本趋势图` | B2 | 步 2 |
| 4 | `feat(service): 请求体参数自愈判据与改写` | C1 | 无 |
| 5 | `feat(configs): 自愈策略配置键` | C3 | 无 |
| 6 | `feat(service): BalanceChat 内层自愈重试` | C2 | 步 4、5 |
| 7 | `fix(service): 重试耗尽时透出最后一次真实失败原因` | C6c | 无（建议先落） |
| 8 | `feat(bridge): 工具结果图片双向搬运` | C6a | 无 |
| 9 | `feat(service): 工具内容数组的运行时降级` | C6b | 步 6、8；配套桩 `picky-toolimg` |
| 10 | `feat(bridge): response_format 翻译为 output_config.format` | C7 | 步 7（失败透出） |

依赖关系：A/B 与 C 全程无交集；C2 是 C6b 的结构前置；C6c 建议最先落（独立小改，
且是 C7 失败路径的展示前提）。每步完成即跑该步的验证命令，全绿才进下一步。

---

## 步 1（A1）：下钻表新增 Token 列

**改动文件**：

1. `webui/src/routes/analytics-views.tsx`
   - 表头（:126-145）：在 `cache_hit`（:139-141）之后插入
     `<TableHead className="text-right">{t("breakdown.columns.tokens")}</TableHead>`；
   - 行体（:172-192）：在缓存命中单元格（:186-188）之后插入
     `<TableCell className="reading text-right" title={t("breakdown.columns.tokens_title", { prompt: formatNumber(g.prompt), completion: formatNumber(g.completion) })}>{formatNumber(g.totalTokens)}</TableCell>`；
   - `min-w-[880px]`（:123）→ `min-w-[960px]`；:120-121 注释「十列」→「十一列」。
2. `webui/src/i18n/locales/{zh-CN,zh-TW,en}/analytics.json` 的
   `breakdown.columns` 段各加两键（三份键集必须一致，`locales.test.ts` 把门）：
   - `tokens`：zh-CN「Token」/ zh-TW「Token」/ en「Tokens」
   - `tokens_title`：zh-CN「输入 {{prompt}} / 输出 {{completion}}」/
     zh-TW「輸入 {{prompt}} / 輸出 {{completion}}」/
     en「Input {{prompt}} / Output {{completion}}」

**测试**（`webui/src/routes/analytics.test.tsx` 增例）：

- mock `getStats` 的分组数据带非零 `totalTokens` / `prompt` / `completion` →
  断言表头出现「Token」、单元格显示千分位精确值、`title` 含「输入 … / 输出 …」；
- `totalTokens=0` → 显示 `0`（0 是真实读数，不是缺值，不隐藏）。

**验证**：`cd webui && corepack pnpm run lint && corepack pnpm run test:coverage && corepack pnpm run build`。
后端零改动，无 Go 步骤。

---

## 步 2（B1）：`TrendPoint` 新增成本

**改动文件**：`service/stats.go`

1. `TrendPoint`（:365-380）在 `Cached` 之后加：

```go
// Cost 该桶的成本（口径同 KPI.Cost：三档价 × 三类 token / 1e6，价格取请求时
// 快照）。单位由 StatsResult.KPI.Currency 决定，桶内不带币种。
Cost float64 `json:"cost"`
```

2. 时间序列累计段（:632-637）`p.Cached += ...` 之后加一行 `p.Cost += Cost(l)`。
3. **不改** `normalizeTrendSeries` / `fillTrendWindow`：空桶以零值
   `TrendPoint{Ts: ts}` 构造，`Cost` 自动为 0。

**测试**（`service/stats_test.go`）：

- 既有趋势对拍用例补 `Cost` 断言；
- 新增表驱动用例「成本按三档计价进桶」：一行缓存读（`prompt=1000`，
  `cached_tokens=400`，`input_price=3`，`cache_read_price=0.3`，`output_price=15`，
  `completion=200`）→ 桶 Cost =
  `(600*3 + 400*0.3 + 200*15) / 1e6 = 0.00522`。这条钉死「按费用计」的口径，
  防 `Σ单价` 的量纲错误回潮（`Cost()` 注释点过 dashboard 原实现的这个错）；
- 一行 `currency=""`（历史行）→ KPI.Currency 取首条非空、桶 Cost 照常累计。

**验证**：`GOCACHE=$PWD/.gocache go test -count=1 ./service/... && rm -rf .gocache`。

---

## 步 3（B2）：成本趋势图

**改动文件**：

1. `webui/src/lib/api.ts`：`TrendPoint`（:484-498）加 `cost: number`（文件头
   注释要求与后端 json tag 一一对应）。
2. `webui/src/components/charts/trend-charts.tsx` 末尾新增（完整实现）：

```tsx
/**
 * 成本趋势。第三张单轴图（请求 / Token / 成本各自独立，双轴是规范头号禁项）。
 *
 * 单系列不画 legend（标题即系列名）；颜色取 4 号槽（琥珀）——0/1/2/3/5/7 已被
 * 请求与 Token 两图占用，且琥珀与「铜金 = 强调色」的项目语义一致。
 * Y 轴用币种符号 + 紧凑数（$1.2K）；tooltip 用四位小数（单桶成本常在 0.01 量级，
 * formatCost 的 toFixed(4) 正是为它设的）。
 */
export function CostTrendChart({ data, currency }: { data: TrendPoint[]; currency: string }) {
  const { t } = useTranslation("home")
  const label = useMemo(() => bucketLabelFormatter(data), [data])

  const config = {
    cost: { label: t("trend.cost"), color: seriesVar(4) },
  } satisfies ChartConfig

  return (
    <ChartContainer config={config} className="aspect-auto h-[220px] w-full">
      <AreaChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" strokeWidth={1} />
        <XAxis dataKey="ts" tickLine={false} axisLine={false} tickMargin={8}
               minTickGap={24} tickFormatter={(v: number) => label(v)} />
        <YAxis tickLine={false} axisLine={false} width={44}
               tickFormatter={(v: number) => `${currencySymbol(currency)}${compactNumber(v)}`} />
        <ChartTooltip content={
          <ChartTooltipCost currency={currency} labelFormatter={(v) => label(Number(v))} />
        } />
        <Area type="monotone" dataKey="cost" stroke="var(--color-cost)"
              fill="var(--color-cost)" fillOpacity={0.1} strokeWidth={2} />
      </AreaChart>
    </ChartContainer>
  )
}
```

   - `ChartTooltipCost`：`ui/chart` 的 `ChartTooltipContent` 对数值取 `.toLocaleString()`
     ——成本需要四位小数，写一个 12 行的薄包装（复制 `ChartTooltipContent` 的
     label 逻辑，值渲染换成 `formatCost(v, currency)`）。`formatCost` /
     `currencySymbol`（`format.ts:61-74`）现成；空币种已有兜底（裸数字）。
   - import 增补 `currencySymbol, formatCost`（from `@/lib/format`）。
3. `webui/src/routes/home.tsx` 趋势 grid（:253-260）加第三张：

```tsx
<Panel title={t("home:trend.cost")} note={t("home:trend.cost_note")}>
  <CostTrendChart data={stats!.trend} currency={stats!.kpi.currency} />
</Panel>
```

   布局：`xl:grid-cols-2` 下三图呈 2+1。若 1600px 实测第三张半宽局促，改
   `xl:grid-cols-3`（图表组件对布局无假设，实施时定，两行改动）。
4. `webui/src/routes/analytics.tsx` 的 `TrendView`（:330-361）同款加第三张
   Panel（`t("analytics:trend.cost")`）。注意该视图已有
   `trend.requests_note` / `trend.tokens_note` 汇总行，成本 note 用
   `trend.cost_note`（内容参照 `kpi.cost_hint` 口径：「按请求时的价格快照累计」）。
5. i18n 三份 locale：`home.json` 与 `analytics.json` 的 `trend` 段各加
   `cost` / `cost_note`：
   - `cost`：zh-CN「成本趋势」/ zh-TW「成本趨勢」/ en「Cost trend」
   - `cost_note`：zh-CN「按请求时的价格快照累计」/ zh-TW「按請求時的價格快照累計」/
     en「Accumulated from price snapshots taken at request time」

**测试**：

- `home.test.tsx` / `analytics.test.tsx`：mock 数据 `trend[].cost` 非零 →
  断言「成本趋势」Panel 出现；`trend` 为空数组 → 不渲染图（四态惯例的空态）；
- `currency=""` → 图仍渲染、轴为裸数字（钉住兜底路径）。

**验证**：`cd webui && corepack pnpm run lint && corepack pnpm run test:coverage && corepack pnpm run build`，
然后 `GOCACHE=$PWD/.gocache go test -count=1 ./...`（`//go:embed` 要 dist 就绪）。

---

## 步 4（C1）：`service/selfheal.go` 判据与改写

新文件，纯函数、零 IO，表驱动全覆盖。**本步不接线**（接线在步 6）。

```go
package service

import (
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"llmio/bridge"
)

// SelfHealMaxDrops 一家上游最多删几个参数（多发自几次请求）。
const SelfHealMaxDrops = 3

// SelfHealNotePrefix 自愈记账短码前缀，后接参数路径：self_heal_dropped:top_k。
// 通过 BridgeNotes 落到 chat_logs.bridge_notes，日志详情抽屉既有通道直接展示。
const SelfHealNotePrefix = "self_heal_dropped:"

// healableStatusCodes 只有这两个状态码可能是"参数不认识"：OpenAI/Anthropic/
// vLLM 用 400，FastAPI 系网关默认 422。401/402/404/429/5xx 与参数存在性无关。
var healableStatusCodes = map[int]bool{400: true, 422: true}

// paramComplaintPatterns 短语类模式表，捕获组 1 = 参数路径。
// 数据不是逻辑：新上游措辞 = 加一行 + 一条表驱动用例（计划 §3.9 的校验循环）。
var paramComplaintPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)unknown parameter:?\s*'([\w.\-]+)'`),
	regexp.MustCompile(`(?i)unrecognized request arguments? (?:supplied|provided):?\s*'?([\w.\-]+)'?`),
	regexp.MustCompile(`(?i)'?([\w.\-]+)'?\s*:\s*extra inputs are not permitted`),
	regexp.MustCompile(`(?i)extra inputs are not permitted[^\n]*path[=:]\s*'?([\w.\-]+)'?`),
	regexp.MustCompile(`(?i)unsupported parameter:?\s*'([\w.\-]+)'`),
	regexp.MustCompile(`(?i)([\w.\-]+) is not supported by this (?:model|api)`),
	regexp.MustCompile(`(?i)unknown name "([\w.\-]+)"`),
	regexp.MustCompile(`(?i)(?:field|parameter) '([\w.\-]+)' is not supported`),
}

// protectedFields 路径**恰好等于**其中之一时不自愈：整删消息流或改流式开关
// 从来不是"自愈"。嵌套子路径（messages.0.metadata）允许删——删的是非标准子字段，
// 消息本体保留。
var protectedFields = map[string]bool{
	"model": true, "messages": true, "stream": true, "tools": true,
	"system": true, "contents": true, "input": true,
}

var dropPathShape = regexp.MustCompile(`^[A-Za-z_][\w.\-]*$`)

// DetectParamComplaint 判定一条上游错误是否点名了"我们不认识的参数"。
// 返回参数路径（JSON 点路径，支持 messages.0.foo 的数组下标）。
func DetectParamComplaint(status int, body []byte) (string, bool) {
	if !healableStatusCodes[status] {
		return "", false
	}
	text := complaintText(body)
	for _, re := range paramComplaintPatterns {
		if m := re.FindStringSubmatch(text); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// complaintText 从错误体提取报错文本。三种信封逐个试，message 可能是数组
// （Anthropic 校验器风格，逐项 join）；都不是 JSON 就按原文匹配。
//   OpenAI:   {"error":{"message":"..."}}
//   Anthropic:{"type":"error","error":{"message":"..."}}
//   Gemini:   {"error":{"message":"...","status":"INVALID_ARGUMENT"}}
func complaintText(body []byte) string {
	for _, key := range []string{"error.message", "message"} {
		v := gjson.GetBytes(body, key)
		if !v.Exists() {
			continue
		}
		switch {
		case v.Type == gjson.String:
			return v.String()
		case v.IsArray():
			var parts []string
			for _, item := range v.Array() {
				parts = append(parts, item.String())
			}
			return strings.Join(parts, "\n")
		}
	}
	if e := gjson.GetBytes(body, "error"); e.Type == gjson.String {
		return e.String() // {"error":"text"} 的简写信封
	}
	return string(body)
}

// PlanDrop 四道闸，全过才允许删。sent 是**实发上游的字节**——上游点名的参数
// 若不在里面，说明它抱怨的是别的东西（形状/嵌套），删无可删。
func PlanDrop(sent []byte, path string, dropped map[string]bool) bool {
	if len(path) == 0 || len(path) > 128 || !dropPathShape.MatchString(path) {
		return false
	}
	if protectedFields[path] || dropped[path] {
		return false
	}
	return gjson.GetBytes(sent, path).Exists()
}

// ApplyDrop 从实发请求体删掉指定路径，返回新字节（sjson 点路径原生支持
// 数组下标）。调用方用返回值替换 rawBody 后重发。
func ApplyDrop(raw []byte, path string) []byte {
	out, err := sjson.DeleteBytes(raw, path)
	if err != nil {
		return raw // 理论不可达（路径已在 PlanDrop 验过），保守原样返回
	}
	return out
}
```

（`bridge` import 在本文件暂未用到——`bridge.Note(SelfHealNotePrefix+path)`
的构造在步 6 接线处；本步先不 import，避免未用导入。）

**测试**（新文件 `service/selfheal_test.go`，表驱动，目标全分支覆盖）：

| 组 | 用例 |
|---|---|
| 模式表正例 | 每条模式 × OpenAI 信封；pydantic 两条（前缀式 / 括号式）；Anthropic 信封；Gemini 信封（`"status":"INVALID_ARGUMENT"`）；`message` 为数组的信封（逐项 join 后命中）；裸 `{"error":"text"}`；非 JSON 原文命中 |
| 状态码 | 400 命中 / 422 命中 / 401、429、500 不进判据（即使文本命中） |
| PlanDrop 四道闸 | 非法字符（`top k`、`../x`）、超长（>128）、保护字段七项逐一、嵌套保护例外（`messages.0.metadata` 放行）、已在 dropped、路径不在实发体（`logit_bias` 未发）、顶层键在（放行）、数组下标路径在（`messages.0.meta` 放行） |
| ApplyDrop | 顶层键删除、嵌套点路径、`messages.0.meta` 数组下标、删除后其余键原样 |
| 负例短语 | `Invalid type for 'top_k'`（值类型错）、`maximum context length`、`max_tokens is too large`、`body is not json`、`桩上游：这个模型就是坏的`（e2e boom 措辞）——全部 `ok=false` |

**验证**：`GOCACHE=$PWD/.gocache go test -count=1 ./service/ -run TestSelfHeal -v && rm -rf .gocache`；
`go vet ./...`（前置 `pnpm run build`）。

---

## 步 5（C3）：配置键 `self_heal_policy`

**改动文件**：

1. `models/config.go` 常量段（:11-31）加
   `KeySelfHealPolicy = "self_heal_policy"`；结构体（照 `LogCleanupPolicy` 的
   注释密度）：

```go
// SelfHealPolicy 是"上游拒收请求体参数时的同家删参重试"策略。
//
// Enabled 默认 true：自愈只发生在"上游明确拒收"之后，替代方案是整单失败；
// 记账短码（self_heal_dropped:*）与失败行都落库，可观测。关掉即回到现状
// （全换家后 500）。
type SelfHealPolicy struct {
	Enabled  bool `json:"enabled"`
	MaxDrops int  `json:"max_drops"` // <=0 用 service.SelfHealMaxDrops
}
```

2. `service/selfheal.go` 加读写（照 `GetLogCleanupPolicy` `log_cleanup.go:19-40`
   的模式：单行 KV 查询，缺行/空值/坏 JSON 一律回默认，不报错）：

```go
func DefaultSelfHealPolicy() *models.SelfHealPolicy {
	return &models.SelfHealPolicy{Enabled: true, MaxDrops: SelfHealMaxDrops}
}

func GetSelfHealPolicy(ctx context.Context) *models.SelfHealPolicy {
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeySelfHealPolicy).First(ctx)
	if err != nil || strings.TrimSpace(config.Value) == "" {
		return DefaultSelfHealPolicy()
	}
	policy := DefaultSelfHealPolicy()
	if err := json.Unmarshal([]byte(config.Value), policy); err != nil {
		return DefaultSelfHealPolicy() // 坏配置不当借口，回默认继续服务
	}
	return policy
}
```

   每请求一次单行 KV 读：这条转发路径上本来就有多次 DB 读
   （`ProvidersWithMetaBymodelsName`），一次索引点查不构成瓶颈，v1 不做缓存。

**测试**：缺行回默认 / 空 value 回默认 / 坏 JSON 回默认 / 合法 JSON 覆写
（`enabled=false`、`max_drops=1`）/ `max_drops=0` 归一为默认 3（在消费侧归一，
本函数只存取）。

**验证**：`go test ./service/ ./models/`。

---

## 步 6（C2）：`BalanceChat` 内层自愈重试

**改动文件**：`service/chat.go`。外层循环结构不动，重排
「BuildReq → Do → 非 2xx」段（:283-315）为内层闭环。

1. `retryLog` 容量（:124）：`providersWithMeta.MaxRetry` →
   `providersWithMeta.MaxRetry * (1 + SelfHealMaxDrops)`——内层每次失败都推
   一条，通道打满会阻塞转发路径。

2. 内层闭环（替换 :283-315；伪码即实现稿，变量名与现有一致）：

```go
selfHeal := GetSelfHealPolicy(ctx)
maxDrops := selfHeal.MaxDrops
if maxDrops <= 0 {
	maxDrops = SelfHealMaxDrops
}

drops := map[string]bool{}
healed := 0
var res *http.Response
outerNext := false // 跳出内层后是否继续外层（换家）
for {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-timer.C:
		return nil, nil, errors.New("retry time out")
	default:
	}

	req, err := chatModel.BuildReq(ctx, headers, modelWithProvider.ProviderModel, rawBody)
	if err != nil {
		fail(err)
		balancer.Delete(id)
		outerNext = true
		break
	}
	res, err = client.Do(req)
	if err != nil {
		fail(err)
		balancer.Delete(id)
		outerNext = true
		break
	}
	if res.StatusCode == http.StatusOK {
		break // 出内层，走 ErrorMatcher / bridgeResponse / Success（现路径 :317-354 一字不动）
	}

	byteBody, rerr := io.ReadAll(res.Body)
	res.Body.Close()
	if rerr != nil {
		slog.Error("read body error", "error", rerr)
	}

	// ---- 自愈判定：同家删参重发（计划 §3.4 三层判据全在两个函数里）----
	if selfHeal.Enabled && healableStatusCode(res.StatusCode) && healed < maxDrops {
		if path, ok := DetectParamComplaint(res.StatusCode, byteBody); ok &&
			PlanDrop(rawBody, path, drops) {
			drops[path] = true
			healed++
			rawBody = ApplyDrop(rawBody, path)
			bridgeNotes.record([]bridge.Note{bridge.Note(SelfHealNotePrefix + path)})
			// 自愈也是一次真实上游调用：失败行与最终成功行都如实计数
			log.Retry = retry + healed
			fail(fmt.Errorf("status: %d, body: %s (self-heal: dropped %s)",
				res.StatusCode, string(byteBody), path))
			continue // 同一家、同一翻译产物，只重发改过的 body
		}
	}

	fail(fmt.Errorf("status: %d, body: %s", res.StatusCode, string(byteBody)))
	if res.StatusCode == http.StatusTooManyRequests {
		balancer.Reduce(id)
	} else {
		balancer.Delete(id)
	}
	res = nil
	outerNext = true
	break
}
if outerNext {
	continue
}
// res != nil 且 200：接现有 :317（ErrorMatcher）→ :343（bridgeResponse）→ :352（Success）
```

   实现要点（都有出处）：

   - **改实发字节，不重建**：内层重发不重跑翻译 / `extra_body`（同家翻译产物
     确定）；外层换家后从 `before.raw` 重建，`drops` 不跨家携带（每轮新建，
     随 `bridgeNotes.reset()`（:202）同一生命周期）。
   - **熔断零污染**：自愈成功不 `Delete`（`breaker.go:68-76` 只计 Delete/Reduce）。
   - **timer 进内层头**：自愈多发也受总超时约束（:164 的 timer 是全请求预算）。
   - `healableStatusCode` 是步 4 里 map 的小包装（或直接用 map 查询）。
   - `fail` 闭包（:244-247）不动——它快照 `log.BridgeNotes` 与 `log`，内层每次
     失败推一条失败行，`RecordRetryLog`（:380-390）异步落库。

**测试**：

- `service/chat_selfheal_test.go`（新，照 `failed_io_test.go` 的 temp-file
  SQLite + 换装 `models.DB` 方式，伪上游用 `httptest.NewServer`）：
  1. 先 400 `Unrecognized request argument supplied: top_k` 后 200 →
     客户端 200；伪上游第二次收到的 body 无 `top_k`、`messages`/`max_tokens`
     原样；`chat_logs` 两行：失败行 Error 含 400 body 与 `(self-heal: dropped top_k)`，
     成功行 `bridge_notes` 含 `self_heal_dropped:top_k`、`Retry=1`；
  2. 预算耗尽：伪上游连报 4 个不同参数 → 4 次 400 后 Delete 换家（单家池 →
     pop err）；4 条失败行、无成功行；
  3. `Enabled=false`：行为与现状一致（1 次 400 即 Delete）；
  4. 429 不进自愈：Reduce 路径回归。
- **验收车转绿**：`python e2e/selfheal_check.py` → S 组 8/8、N 组 4/4。
- **回归**：`python e2e/run_matrix.py --upstream stub` → 23/23。

**验证**：`go test ./...` + 上面两条 e2e。

---

## 步 7（C6c）：重试耗尽时透出真实原因

**改动文件**：`service/chat.go`

1. 外层循环前加 `var lastErr error`；三处失败路径在 `fail(...)` 的同时
   `lastErr = err`（用同一个 err 变量）：桥接拒绝（:268）、`buildUpstreamBody`
   （:278）、`BuildReq`（:285）、`client.Do`（:293）、非 2xx（内层两处
   `fail` 的错误）、ErrorMatcher（:330）、bridgeResponse（:345）。
   最省事的做法：把 `fail` 闭包改成同时赋值
   `fail := func(err error) { lastErr = err; log.BridgeNotes = ...; retryLog <- ... }`
   ——一处改，全路径覆盖。
2. 两处收尾带上 lastErr（截断 500 字符，与 `ClassifyError` 的 raw 截断同款）：
   - 池空（:176-179）：`balancer pop err: %v, traceID: %s, last error: %.500s`
   - 耗尽（:358）：`All retry failed, trace ID: %s, last error: %.500s`
   （`lastErr == nil` 时省略尾段——`balancer pop err` 首轮就可能发生。）
3. 客户端可见性：`handler/chat.go:117-120` 把 `err.Error()` 原样进 500 envelope，
   不用改。**注意**：e2e 断言只查状态码不查消息文本（已核对
   `run_matrix.py` `check_shape` 与 `selfheal_check.py`），追加尾段安全。

**测试**：伪上游恒 400 → 客户端 500 body 里含上游报错原文片段与
`(self-heal: dropped ...)`（与步 6 的用例 2 合并断言）；桥接全拒（发
`response_format` 给 anthropic 池）→ 500 body 含
`cannot bridge to the upstream protocol`——这正是样本①的用户视角修复。

**验证**：`go test ./...` + `python e2e/run_matrix.py --upstream stub`。

---

## 步 8（C6a）：工具结果图片双向搬运

**改动文件**：`bridge/request.go`、`bridge/bridge.go`

1. `bridge.go` Note 常量段加：
   `NoteCarriedToolResultImage Note = "carried_tool_result_image"`
   （注释：工具结果里的图片按块数组搬运而非拒绝/丢弃——OpenAI 2025 起支持
   tool 消息 content 块数组含 image_url，Anthropic tool_result 本就收 image 块。
   依据与核对日期见 docs/plans/usage-insights-plan.md §3.11 表第 1/2 行。）

2. **A→O 方向**：`anthropicToolResultText`（:817-844）改名为
   `anthropicToolResultContent`，签名加 `notes *[]Note`，返回
   `(json.RawMessage, error)`：

```go
// 纯文本（字符串或全 text 块）→ 仍拼成字符串（兼容性最好，与 openAIContentJSON
// 的约定一致：只有文本给字符串，夹了图片才给块数组）。
// 含 image 块 → [text…, image_url…] 块数组 + NoteCarriedToolResultImage。
// document / search_result 块 → 维持 Unsupported（OpenAI 侧无对应物，§3.11 表）。
func anthropicToolResultContent(raw json.RawMessage, notes *[]Note) (json.RawMessage, error)
```

   内部：text 块收集进 `texts`；image 块走现成的 `openAIImagePart(b.Source)`
   （:971）收集进 `parts`；`len(parts) == 0` → `stringJSON(strings.Join(texts, ""))`
   （与现状逐字节等价）；否则组 `[]OpenAIContentPart`（先 text 后 image）+
   `*notes = append(...)` 后 `json.Marshal`。
   调用点（:697-711）：`text, err := anthropicToolResultText(b.Content)` →
   `content, err := anthropicToolResultContent(b.Content, notes)`；
   `is_error` 前缀（:701-706）适配两种形态——字符串照旧 `toolErrorPrefix + text`；
   块数组则把前缀拼进**首个 text 块**（没有 text 块就插一个纯前缀 text 块），
   `NotePrefixedToolError` 逻辑不变；`Content: content`。

3. **O→A 方向**：`case "tool"`（:239-255）的 `textOnlyParts` 拒绝分支改为搬运：

```go
case "tool":
	text, parts, err := openAIContent(m.Content)
	if err != nil { ... 原样 ... }
	content := stringJSON(text)
	if parts != nil {
		blocks := make([]AnthropicBlock, 0, len(parts))
		for _, p := range parts {
			switch p.Type {
			case "text", "input_text":
				blocks = append(blocks, AnthropicBlock{Type: blockText, Text: p.Text})
			case "image_url":
				b, err := anthropicImageBlock(p.ImageURL, notes) // 现成助手（:535）
				if err != nil { return ..., &Unsupported{Field: ..., Reason: err.Error()} }
				blocks = append(blocks, b)
				*notes = append(*notes, NoteCarriedToolResultImage)
			default:
				return ..., &Unsupported{Field: fmt.Sprintf("messages[%d].content[].type", i), Reason: ...}
			}
		}
		content, err = json.Marshal(blocks) // err 不可达：块内都是值类型
	}
	turns = append(turns, anthropicTurn{role: "user", blocks: []AnthropicBlock{{
		Type: blockToolResult, ToolUseID: m.ToolCallID, Content: content,
	}}})
```

   （`anthropicImageBlock` 自带 `NoteDroppedImageDetail` / `NoteImageByURL` 记账，
   base64 / http URL 两种源都覆盖；`AnthropicBlock` 的 json tag 两个方向通用。）

**测试**（`bridge/request_test.go` 增例，1752 行既有风格）：

- A→O：tool_result 带 text+image 块 → tool 消息 content 为数组
  `[{text},{image_url:data:...}]`，notes 含 `carried_tool_result_image`；
  纯文本块数组 → content 仍是字符串（**与现状逐字节等价**，回归钉）；
  `is_error + image` → 首个 text 块带 `[tool_error]` 前缀 + 两个 note；
  document 块 → 仍 Unsupported。
- O→A：tool 消息 content 数组 `[{image_url:https}]` → tool_result.content 为
  image 块（url 源），notes 含 `carried_tool_result_image` + `image_by_url`；
  纯文本数组 → content 仍是字符串（回归钉）。
- e2e：`run_matrix.py` 加一例「工具图片·tool_result 搬运」（openai 客户端 →
  anthropic 桩，工具结果带图 → 上游 dump 含 `"type":"image"`、log notes 含
  `carried_tool_result_image`）。

**验证**：`go test ./bridge/... ./service/...` + `python e2e/run_matrix.py --upstream stub`。

---

## 步 9（C6b）：工具内容数组的运行时降级

**前置**：步 6 的内层闭环、步 8 的搬运。**配套桩**：`stub_upstream.py` 加成员
`picky-toolimg`——`role:"tool"` 消息 content 为数组即 400
`Invalid type for 'content': expected one of a string, but got an array instead.`
（OpenAI 信封；anthropic 路径同措辞换信封）。

**改动文件**：

1. `service/selfheal.go` 加协议感知规则（独立于参数删除，预算 1 次/家）：

```go
// toolContentArrayRejected 判定"上游不收 tool 消息的块数组 content"。
// 这类 400 是类型错，按 §3.4 不进通用删参；正确动作是压形状不是删参数。
var toolContentArrayPattern = regexp.MustCompile(
	`(?i)invalid type for 'content': expected a string`)

// FlattenToolContent 把 rawBody 里所有 role:"tool" 且 content 为数组的消息
// 压回纯文本字符串（丢 image 块、保 text 块）。返回 (新 body, 丢弃的图片块数)。
// 仅当确有数组 content 时才改写，幂等：再跑一遍找不到目标，返回原 body。
func FlattenToolContent(raw []byte) ([]byte, int)
```

   实现：`gjson.GetBytes(raw, "messages").Array()` 遍历，`role=="tool"` 且
   `content` `IsArray()` 的项逐个 `sjson.SetBytes(raw, fmt.Sprintf("messages.%d.content", i), joined)`，
   join 取块数组里 `type=="text"` 的 `text` 字段；统计 `image_url` 块数。

2. `bridge.go` 加
   `NoteDroppedToolResultImage Note = "dropped_tool_result_image"`。

3. `service/chat.go` 内层闭环的自愈判定段（步 6 引入处）后面并列一条：

```go
if selfHeal.Enabled && !downgraded && healed < maxDrops &&
	healableStatusCode(res.StatusCode) &&
	toolContentArrayPattern.MatchString(complaintText(byteBody)) {
	if next, droppedImgs := FlattenToolContent(rawBody); droppedImgs >= 0 && !bytes.Equal(next, rawBody) {
		downgraded = true
		rawBody = next
		if droppedImgs > 0 {
			bridgeNotes.record([]bridge.Note{bridge.Note("dropped_tool_result_image")})
		}
		log.Retry = retry + healed + 1
		fail(fmt.Errorf("status: %d, body: %s (downgrade: tool content to text)", res.StatusCode, string(byteBody)))
		continue
	}
}
```

   `downgraded` 与 `drops` 同生命周期（每轮外层新建）。**不降级保证级内容**：
   此规则只处理类型错提及 `content` 且确有数组可压的场景；其余类型错照旧
   Delete 换家。

**测试**：

- `selfheal_test.go`：`FlattenToolContent` 表驱动——混合 text+image 数组 →
  字符串 + 图片计数 1；纯 text 数组 → 字符串 + 计数 0（无损压缩，不记 note）；
  非数组 content / 无 tool 消息 → 原 body；幂等（二次跑原样）。
- `chat_selfheal_test.go`：伪上游先拒数组后收字符串 → 客户端 200，成功行
  notes 含 `dropped_tool_result_image`（带图时）。
- **验收车**：`selfheal_check.py` 加用例 S9「桥接·工具结果图片：搬运后被拒，
  降级后成功」——anthropic 客户端带图工具结果 → openai `picky-toolimg` 上游：
  第 1 发含 image 块数组（步 8 搬运）、400、第 2 发 content 为字符串、200；
  成功行 notes 含 `dropped_tool_result_image`。
- N 组不动：`picky-type` 的措辞是 `'top_k'` 不是 `'content'`，不会误入此规则
  （表驱动用例钉住：`Invalid type for 'top_k'` 不触发 Flatten）。

**验证**：`go test ./...` + `python e2e/selfheal_check.py`（S 9/9、N 4/4）+
`python e2e/run_matrix.py --upstream stub`。

---

## 步 10（C7）：`response_format` → `output_config.format` 翻译

**依据**（§3.11 审计第 3 行 + 2026-10-24 官方文档核对）：现行参数是
`output_config.format`（旧 `output_format` 已弃用），形状
`{"format":{"type":"json_schema","schema":{…}}}`；无 `name` 字段；schema 过于
复杂 / 含不支持特性时上游回 400。

**改动文件**：

1. `bridge/anthropic.go`：`AnthropicRequest`（:11-25）加
   `OutputConfig json.RawMessage \`json:"output_config,omitempty"\``。
2. `bridge/openai.go`：确认 `OpenAIResponseFormat` 已声明 `JsonSchema` 子结构
   （含 `Name` / `Schema` / `Strict`；若 `Schema` 未声明则补——只声明转换要用到
   的字段，遵守该文件头注释的白名单纪律）。
3. `bridge/request.go`：
   - `rejectUnsupportedOpenAIFields`（:81-98）：`response_format` 分支从
     「非 text 即拒」改为「`json_schema` / `json_object` 放行（翻译），其余类型仍拒」；
   - `OpenAIRequestToAnthropic` 组装处加翻译（独立小函数）：

```go
// openAIResponseFormatToOutputConfig 把 OpenAI 的 response_format 翻成 Anthropic
// 的 output_config.format（2025-11 起原生结构化输出）。
//   json_schema  → {"format":{"type":"json_schema","schema": <schema>}}
//   json_object  → {"format":{"type":"json_schema","schema":{"type":"object"}}}
//                  （合成"任意 JSON 对象"——官方文档只记档了 json_schema 一档，
//                   不依赖未核实的 json 档形状）
// json_schema.name / strict 无对应物：name 是 OpenAI 侧的标识，Anthropic 不收；
// strict 由 Anthropic 侧默认严格，不需要显式位。记 Note（见下）。
// 保证级契约：上游（老模型/兼容网关）不认而 400 时**不降级不静默丢**，
// 失败原因经步 7 透给客户端。
func openAIResponseFormatToOutputConfig(rf *OpenAIResponseFormat) (json.RawMessage, error)
```

   - `bridge.go` 加 `NoteTranslatedOutputFormat Note = "translated_output_format"`，
     翻译发生即记。
4. **响应侧零改动**：Anthropic 把 JSON 放在 text 块里返回，OpenAI 客户端从
   `choices[].message.content` 取——现有翻译路径天然工作。

**测试**：

- `request_test.go`：
  - `response_format=json_schema`（带 name/strict/schema）→ 上游体含
    `output_config.format.schema` 与原 schema 逐字段相等、**不含** `name`；
    notes 含 `translated_output_format`；
  - `json_object` → 合成 `{"type":"object"}` schema；
  - `type=text` → 上游体无 `output_config`（现状回归钉）；
  - `type=其它` → 仍 Unsupported（现状回归钉）。
- e2e：`run_matrix.py` 加一例——openai 客户端带 `json_schema` 打 anthropic 桩：
  上游 dump 含 `output_config`，客户端拿到 `choices`，notes 含翻译短码
  （桩的 `anthropic_validate` 不校验该字段，天然放行，无需改桩）。

**验证**：`go test ./bridge/... ./service/...` + `run_matrix`。

---

## 全局验证矩阵（每步提交前 + 收尾）

```
# 后端（Windows 注意：权限不足时 GOCACHE=$PWD/.gocache，跑完删目录）
go fmt ./... && go vet ./...
GOCACHE=$PWD/.gocache go test -count=1 ./...

# 前端（改到 webui 的步：1、3）
cd webui && corepack pnpm run lint && corepack pnpm run test:coverage && corepack pnpm run build

# e2e（改到转发/桥接的步：6、7、8、9、10）
python e2e/run_matrix.py --upstream stub          # 既有 23+ 条不回退
python e2e/selfheal_check.py                      # 步 6 后 S 8/8；步 9 后 S 9/9；N 恒 4/4

# 覆盖率门禁（收尾）
make cover                                        # 全仓 ≥70% 不回退
```

**覆盖率口径**：`service/selfheal.go` 与 `FlattenToolContent` 表驱动全覆盖
（纯函数，A 层待遇）；bridge 新分支（搬运/翻译）进 `request_test.go` 既有体系；
前端无新增 A 层模块，`analytics.test.tsx` / `home.test.tsx` 按四态惯例。

**真机走查**（收尾，真实库备份 + 浏览器）：按 `usage-insights-test-plan.md`
第五节五条执行；自愈三条（成功自愈 / boom 不自愈 / 工具图片降级）用
`LLMIO_DB_PATH` 指向备份库起真进程，curl 复现。

**回滚**：每步独立提交，回退单步即回到该步之前的行为；C 系列共享的内层闭环
（步 6）是唯一结构性改动，单独回退它需同时回退步 9（依赖其结构）。
