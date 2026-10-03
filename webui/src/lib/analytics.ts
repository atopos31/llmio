import type { AuthKeyItem, ErrorGroup, GroupStat, StatsQuery, StatsResult } from "@/lib/api"
import { toggleValue } from "@/lib/utils"

/**
 * 分析页（`/analytics`）的纯逻辑层：时间范围口径、筛选到查询串的映射、
 * 下钻维度注册表、以及几处"呈现规则"的判定。
 *
 * ## 为什么这些决定要放在 lib 而不是页面里
 *
 * 它们每一个都是**语义承诺**而不是排版细节：预设时间范围的边界怎么算、
 * 什么情况下错误类别不该画条形图、筛选选项从哪来。这些决定一旦写歪，
 * 页面看上去仍然正常，只是数字的含义变了——是最难被发现的那类错误。
 * 放在这里就能被测试逐条钉住。
 *
 * 展示文本的**真相来源仍是服务端**：成功率的分母（排除 running）、
 * 缓存命中率的分母（prompt）、分位数的定义（最近秩）、各类 `avgTps` 的
 * 样本范围，全部在 `service/stats.go` 里算好下发。这里不做任何重新聚合，
 * 只做"选择与呈现"。
 */

// ---------------------------------------------------------------------------
// 时间范围
// ---------------------------------------------------------------------------

/**
 * 时间范围预设。
 *
 * `today` / `yesterday` 按**本地日历**对齐（用户说"今天"时指的是自然日），
 * `last_24h` / `last_7d` / `last_30d` 按**滚动窗口**（"最近 7 天"= 此刻往前
 * 168 小时）。两种口径混用是有意的：日历对齐的窗口在两套算法下都稳定，
 * 而滚动窗口才符合"最近"的直觉。混用时唯一的坑是边界日的桶不完整，
 * 因此在页面上用 `range.granularity` 的档位提示来提醒。
 */
export type RangePreset = "today" | "yesterday" | "last_24h" | "last_7d" | "last_30d" | "custom"

/** 预设里除自定义之外的项，顺序即界面顺序。 */
export const RANGE_PRESETS = [
  "today",
  "yesterday",
  "last_24h",
  "last_7d",
  "last_30d",
] as const satisfies readonly Exclude<RangePreset, "custom">[]

/** 滚动窗口预设对应的回看秒数。日历对齐的两个预设不在此表内。 */
const ROLLING_SECONDS: Record<"last_24h" | "last_7d" | "last_30d", number> = {
  last_24h: 24 * 3600,
  last_7d: 7 * 24 * 3600,
  last_30d: 30 * 24 * 3600,
}

/** 本地当日零点。 */
function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate())
}

/**
 * 预设 → 后端接受的 unix **秒**。
 *
 * `to` 一律是 `now`（除 `yesterday` 的右端是今日零点），因为"到现在为止"
 * 是这些预设唯一说得通的右端。自定义范围由调用方直接给时间戳，不走这里。
 */
export function presetRange(
  preset: Exclude<RangePreset, "custom">,
  now: Date
): { from: number; to: number } {
  const to = Math.floor(now.getTime() / 1000)

  if (preset === "today") {
    return { from: Math.floor(startOfDay(now).getTime() / 1000), to }
  }
  if (preset === "yesterday") {
    const todayStart = startOfDay(now)
    const yesterdayStart = new Date(todayStart)
    yesterdayStart.setDate(yesterdayStart.getDate() - 1)
    return {
      from: Math.floor(yesterdayStart.getTime() / 1000),
      // 右端是今日零点而不是 now：否则"昨天"会把今天的流量也算进来
      to: Math.floor(todayStart.getTime() / 1000),
    }
  }
  return { from: to - ROLLING_SECONDS[preset], to }
}

// ---------------------------------------------------------------------------
// 自定义范围
// ---------------------------------------------------------------------------

/**
 * unix 秒 → `<input type="datetime-local">` 的值。
 *
 * 手工按本地时间各部分拼字符串，**不用 `toISOString()`**：后者输出的是 UTC，
 * 而 datetime-local 的值被浏览器解释为**本地时间**，两者相差一个时区偏移——
 * 在东八区就是整整 8 小时的静默错位（用户选"14:00"却查了 06:00 的数据）。
 */
export function toLocalInput(seconds: number): string {
  const d = new Date(seconds * 1000)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** `<input type="datetime-local">` 的值 → unix 秒。空值或非法值返回 null。 */
export function fromLocalInput(value: string): number | null {
  if (!value) return null
  const ms = new Date(value).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : null
}

/** 自定义范围的两种不可用状态。文案由视图层给（这里不产出展示文本）。 */
export type CustomRangeError = "incomplete" | "order"

/**
 * 自定义范围校验。
 *
 * 未填全与前后颠倒分开报：前者是"还没填完"，后者是"填错了"，
 * 用户要做的动作不同。过去的右端**不算错误**——查未来等于查到现在为止，
 * 后端会如实返回空桶，不需要在客户端假装它是非法的。
 */
export function customRangeError(from: string, to: string): CustomRangeError | null {
  const a = fromLocalInput(from)
  const b = fromLocalInput(to)
  if (a === null || b === null) return "incomplete"
  if (a >= b) return "order"
  return null
}

// ---------------------------------------------------------------------------
// 筛选
// ---------------------------------------------------------------------------

/**
 * 筛选状态。多值字段为空数组表示不筛选。
 *
 * `key` 存的是 **AuthKey 的 id 字符串**而不是名字：后端只接受 `key_id`
 * （`parseUintCSV`），而名字不唯一——`resolveKeyGroups` 会按名字合并分组，
 * 说明历史上存在同名密钥。存名字再反查 id 会在同名时选错。
 */
export interface AnalyticsFilter {
  provider: string[]
  model: string[]
  key: string[]
  name: string[]
  ua: string[]
  status: string[]
}

export const EMPTY_FILTER: AnalyticsFilter = {
  provider: [],
  model: [],
  key: [],
  name: [],
  ua: [],
  status: [],
}

/**
 * 后端 `keyLabel(0)` 的约定值：AuthKeyID 为 0 表示管理台 TOKEN 直连。
 *
 * 这个常量与 `service/stats.go` 的 `keyLabel` 是同一份契约，改任一处都要同步。
 */
export const ADMIN_KEY_ID = "0"
export const ADMIN_KEY_LABEL = "admin"

/** 请求状态。与 `consts/log.go` 的 StatusSuccess / StatusRunning / StatusError 一致。 */
export const STATUS_VALUES = ["success", "running", "error"] as const

/** 已启用的筛选项数（用于决定是否显示"清除筛选"）。 */
export function activeFilterCount(f: AnalyticsFilter): number {
  return (
    f.provider.length + f.model.length + f.key.length + f.name.length + f.ua.length + f.status.length
  )
}

/** 在多选里切换一个值。实现在 `lib/utils`（日志页的多选也要用它）。 */
export { toggleValue }

/**
 * 筛选状态 + 时间范围 → 查询参数。
 *
 * `granularity` 为 `auto`（或空）时不发送：后端的 `ResolveBucket` 对
 * 空值与 `"auto"` 是同一分支，发送 `auto` 只是把默认值明写一遍。
 */
export function buildStatsQuery(input: {
  from: number
  to: number
  granularity: string
  filter: AnalyticsFilter
}): StatsQuery {
  const q: StatsQuery = { from: String(input.from), to: String(input.to) }
  if (input.granularity && input.granularity !== "auto") q.granularity = input.granularity

  // 空数组不发送——发送空串会被后端的 splitCSV 丢掉，但显式省略意图更清楚
  if (input.filter.provider.length) q.provider = input.filter.provider.join(",")
  if (input.filter.model.length) q.model = input.filter.model.join(",")
  if (input.filter.key.length) q.key_id = input.filter.key.join(",")
  if (input.filter.name.length) q.name = input.filter.name.join(",")
  if (input.filter.ua.length) q.ua = input.filter.ua.join(",")
  if (input.filter.status.length) q.status = input.filter.status.join(",")
  return q
}

// ---------------------------------------------------------------------------
// 视图与下钻维度
// ---------------------------------------------------------------------------

export type AnalyticsView = "trend" | "breakdown" | "latency" | "errors" | "models"

export const VIEWS = [
  "trend",
  "breakdown",
  "latency",
  "errors",
  "models",
] as const satisfies readonly AnalyticsView[]

export type DimensionKey = "model" | "provider" | "key" | "name" | "ua"

/** 下钻维度的界面顺序。 */
export const DIMENSIONS = ["model", "provider", "key", "name", "ua"] as const satisfies readonly DimensionKey[]

/**
 * 维度 → 结果字段。
 *
 * 五个维度共用一张映射表而不是五个分支：列定义、排序、空值处理对五者完全相同，
 * 差别只有"取哪个数组"。写成映射后，表格组件只认这一张表，加维度不必动组件。
 *
 * 用 `Record` 而不是数组 + `find`：`DimensionKey` 是闭集，查表必然命中，
 * 写成 `find` 会留下一个永远走不到的兜底分支。
 */
const DIMENSION_STAT: Record<DimensionKey, keyof StatsResult> = {
  model: "byModel",
  provider: "byProvider",
  key: "byKey",
  name: "byName",
  ua: "byUa",
}

/** 取某个维度的分组。分组顺序沿用服务端：按请求数降序。 */
export function dimensionGroups(stats: StatsResult, dim: DimensionKey): GroupStat[] {
  return stats[DIMENSION_STAT[dim]] as GroupStat[]
}

/**
 * 维度 → 筛选字段。
 *
 * 两者一一对应（下钻表的每一行都能变成一条筛选条件，这就是"下钻"的字面含义），
 * 但**不要**用 `dim as keyof AnalyticsFilter` 糊过去：那样 `ua` 这种
 * 只在一侧存在的名字会静默通过类型检查，等后端改名时才发现。
 */
const DIMENSION_FILTER: Record<DimensionKey, keyof AnalyticsFilter> = {
  model: "model",
  provider: "provider",
  key: "key",
  name: "name",
  ua: "ua",
}

/** 某维度当前已选中的筛选值。 */
export function dimensionFilterValues(f: AnalyticsFilter, dim: DimensionKey): string[] {
  return f[DIMENSION_FILTER[dim]]
}

/** 切换某维度的一个筛选值，返回新的筛选状态。 */
export function toggleDimensionValue(
  f: AnalyticsFilter,
  dim: DimensionKey,
  value: string
): AnalyticsFilter {
  const field = DIMENSION_FILTER[dim]
  return { ...f, [field]: toggleValue(f[field], value) }
}

/**
 * 用户代理串的展示长度上限。
 *
 * UA 动辄 120 字符以上且前 40 字符内就足以区分（浏览器/客户端名 + 主版本），
 * 因此截断展示、完整值放 `title`。
 */
export const UA_DISPLAY_MAX = 44

/** 过长时截断并加省略号。按字符数而非字节，避免切坏多字节字符。 */
export function shortenLabel(value: string, max = UA_DISPLAY_MAX): string {
  if (value.length <= max) return value
  return `${value.slice(0, max)}…`
}

// ---------------------------------------------------------------------------
// 筛选选项
// ---------------------------------------------------------------------------

/**
 * 从分组结果里取"在窗口内实际出现过"的选项值。
 *
 * 选项来自数据本身而不是配置列表：配置里存在但窗口内没有流量的供应商选了
 * 只会得到空结果，那是把用户往坑里带。空名（服务端对缺失值填 `未知`）
 * 保留——它是一个真实存在的分组，不是脏数据。
 */
export function observedOptions(groups: GroupStat[]): string[] {
  return groups.map((g) => g.name)
}

/**
 * 密钥筛选的选项。
 *
 * 后端按 `key_id` 筛选，而分组结果里只有**展示名**，因此这里做一次对齐：
 * 只保留"在窗口内出现过"的密钥（按名字匹配），并单独处理 `admin`——
 * 它是 `keyLabel(0)` 的固定值，代表管理台 TOKEN 直连，不在 AuthKey 表里。
 *
 * 匹配不上的观测名（例如密钥已删除、只剩历史日志）不出现在选项里：
 * 没有 id 就构造不出可用的筛选条件，给一个选了没反应的选项更糟。
 */
export function keyFilterOptions(
  observed: GroupStat[],
  keys: AuthKeyItem[]
): { value: string; label: string }[] {
  const names = new Set(observed.map((g) => g.name))
  const out = keys
    .filter((k) => names.has(k.name))
    .map((k) => ({ value: String(k.id), label: k.name }))

  if (names.has(ADMIN_KEY_LABEL)) {
    // 置前：TOKEN 直连是自用实例里最常见的来源
    out.unshift({ value: ADMIN_KEY_ID, label: ADMIN_KEY_LABEL })
  }
  return out
}

// ---------------------------------------------------------------------------
// 错误类别的呈现
// ---------------------------------------------------------------------------

/**
 * 条形图能可靠承载的类别上限。
 *
 * 超过就改上表格：§4.4 规定分类色板只有 8 槽、超 8 必须折叠或分面，
 * 而"折叠成其他"会把用户最需要看的少数大类淹掉。表格在类别多时
 * 本来也比条形图好读——能排序、能对齐数字。
 */
export const ERROR_BAR_LIMIT = 7

export function errorViewMode(categoryCount: number): "bars" | "table" {
  return categoryCount > ERROR_BAR_LIMIT ? "table" : "bars"
}

/** 条形宽度的基准：最大类别的计数。全为 0 时返回 1，避免除零。 */
export function errorBarBase(groups: ErrorGroup[]): number {
  return Math.max(1, ...groups.map((g) => g.count))
}

/** 计数 → 条形宽度百分比（0~100）。 */
export function errorBarWidth(count: number, base: number): number {
  if (base <= 0) return 0
  return Math.min(100, (count / base) * 100)
}

// ---------------------------------------------------------------------------
// 模型性能表的排序
// ---------------------------------------------------------------------------

/**
 * 模型性能表的呈现维度。
 *
 * 三个而不是两个：`model` 与 `provider` 是两张边际分布，各自把另一半抹平了。
 * 同一个请求名挂在多个上游上时（同一个模型走了几家），`model` 只给一行合计，
 * "这家比那家慢多少"在表里根本读不出来——那正是这张表最该回答的问题。
 */
export type ModelDimension = "model" | "provider" | "modelProvider"

/** 界面顺序。 */
export const MODEL_DIMENSIONS = [
  "model",
  "provider",
  "modelProvider",
] as const satisfies readonly ModelDimension[]

/** 维度 → 结果字段。与 DIMENSION_STAT 同一套查表办法。 */
const MODEL_DIMENSION_STAT: Record<ModelDimension, keyof StatsResult> = {
  model: "byModel",
  provider: "byProvider",
  modelProvider: "byModelProvider",
}

/** 取某维度下的分组。顺序沿用服务端（按请求数降序）。 */
export function modelDimensionGroups(stats: StatsResult, dim: ModelDimension): GroupStat[] {
  return stats[MODEL_DIMENSION_STAT[dim]] as GroupStat[]
}

/** 一个模型及其各上游的行。 */
export interface JointGroup {
  model: string
  rows: GroupStat[]
}

/**
 * 把模型×上游的行按模型归组，让同一个模型的上游在表里挨着。
 *
 * 组的顺序**由各行合计现算**（请求数降序，同量按名称码点升序），而不是另接
 * 一份 `byModel` 进来：两者本就得数相同（同一批日志的和），多传一个入参
 * 只会多一处可能对不上的地方。这样切到这一维时模型之间的相对次序不变，
 * 用户不会因为换了个维度就看到"第一名"换了人。
 *
 * 模型名取自分量字段而不是从行名里拆：行名是「模型 · 上游」拼的，
 * 而模型名里本身就可能带这个分隔符。
 */
export function groupByModel(joint: GroupStat[]): JointGroup[] {
  const byModel = new Map<string, GroupStat[]>()
  for (const g of joint) {
    const model = g.model ?? g.name
    const rows = byModel.get(model)
    if (rows) rows.push(g)
    else byModel.set(model, [g])
  }

  // 合计跟组一起存，不再另开一张 Map 反查：反查的键必然命中，
  // 于是 `?? 0` 那类兜底永远走不到，只是给覆盖率留一条假分支。
  const groups = Array.from(byModel, ([model, rows]) => ({
    model,
    rows,
    total: rows.reduce((n, r) => n + r.total, 0),
  }))
  groups.sort((a, b) => (a.total === b.total ? compareName(a.model, b.model) : b.total - a.total))

  return groups.map(({ model, rows }) => ({ model, rows }))
}

/**
 * 可排序的列，取的是 `GroupStat` 的字段名。
 *
 * 直接用字段名而不是另起一套列 id，是为了让"取值"退化成 `g[key]`——
 * 中间少一张映射表，就少一处字段改名后两边对不上的机会（与 DIMENSION_STAT 同理）。
 * 顺序即表头顺序。
 */
export const MODEL_SORT_KEYS = [
  "total",
  "successRate",
  "avgTps",
  "maxTps",
  "avgFirstChunkMs",
  "p95FirstChunkMs",
  "retries",
  "cost",
  "totalTokens",
] as const

export type ModelSortKey = (typeof MODEL_SORT_KEYS)[number]

export type SortDir = "asc" | "desc"

/** 排序状态。`key` 必须是 `MODEL_SORT_KEYS` 之一，方向二选一。 */
export interface ModelSort {
  key: ModelSortKey
  dir: SortDir
}

/**
 * 默认排序：请求数降序。
 *
 * 与服务端 `groupAcc.result()` 的顺序一致——打开视图时表格不能先自己动一下，
 * 否则用户会以为自己误点了什么。
 */
export const DEFAULT_MODEL_SORT: ModelSort = { key: "total", dir: "desc" }

/**
 * 这几个列的 0 是"没有可用样本"，不是一个读数。
 *
 * `avgTps` / `avgFirstChunkMs` / `p95FirstChunkMs` 只在**成功请求**上累加
 * （`service/stats.go` 的 `groupItem`），`maxTps` 也只有在日志记过 Tps 时才抬起来。
 * 于是全失败的模型、或非流式（没有吞吐可言）的模型，这些字段统统停在 0。
 *
 * 对比之下 `successRate` 的 0% 是真实的（全部失败）、`retries` / `cost` /
 * `totalTokens` 的 0 也是真实的。所以只有上面四个进入这张表。
 */
const ZERO_MEANS_NO_SAMPLE: ReadonlySet<ModelSortKey> = new Set([
  "avgTps",
  "maxTps",
  "avgFirstChunkMs",
  "p95FirstChunkMs",
])

/** 某列上的某值是否代表"没有样本"。 */
export function isMissingModelMetric(key: ModelSortKey, value: number): boolean {
  if (ZERO_MEANS_NO_SAMPLE.has(key)) return value === 0
  return false
}

/**
 * 点表头之后的新排序状态。
 *
 * 同一列再点一次翻转方向；换一列则**从降序起步**。降序起步是因为这一页要回答的
 * 都是"哪个最差/最大"——最慢、最贵、重试最多；即使用户问的是"哪个最快"，
 * 降序也直接把答案放在第一行，不必再点一下。
 */
export function nextModelSort(current: ModelSort, key: ModelSortKey): ModelSort {
  if (current.key === key) return { key, dir: current.dir === "asc" ? "desc" : "asc" }
  return { key, dir: "desc" }
}

/**
 * 按列排序模型分组。返回新数组，不改入参——入参往往是 props 里的 `stats.byModel`。
 *
 * 两条不能省的规则：
 *
 * 1. **缺值恒排最后，与方向无关。** `avgTps` 为 0 表示没有成功样本；若按升序
 *    （小在前）把它排到首位，读出来就是"这个模型最快"，与事实正好相反。
 *    "没有数据"不在刻度上，所以两个方向都往末尾放。这是本表与普通数字表
 *    最关键的区别，也是 `isMissingModelMetric` 存在的全部理由。
 * 2. **同值按名称定序。** 服务端只保证"按请求数降序"，同值行的相对次序来自
 *    map 迭代，本就不稳定；不额外钉一个次序的话，相同数据下表格的顺序会跳。
 *    用码点比较而非 `localeCompare`：后者随环境 locale 变，测试会飘。
 */
export function sortModelGroups(groups: GroupStat[], sort: ModelSort): GroupStat[] {
  const { key, dir } = sort
  return [...groups].sort((a, b) => {
    const av = a[key]
    const bv = b[key]
    const aMissing = isMissingModelMetric(key, av)
    const bMissing = isMissingModelMetric(key, bv)
    if (aMissing !== bMissing) return aMissing ? 1 : -1
    if (!aMissing && av !== bv) return dir === "asc" ? av - bv : bv - av
    return compareName(a.name, b.name)
  })
}

/** 名称定序：相等返回 0，否则按码点。无 locale 依赖，任何机器结果一致。 */
function compareName(a: string, b: string): number {
  if (a === b) return 0
  return a < b ? -1 : 1
}

// ---------------------------------------------------------------------------
// 跳转
// ---------------------------------------------------------------------------

/**
 * 日志详情路径。
 *
 * 排行榜与错误样本都跳到 `/logs/:id/chat-io`（请求内容页），而不是弹窗：
 * 分析页需要"从聚合数字跳到那一条请求"的通路，而内容页能同时给出
 * 请求体、响应体与分片重组结果，是排查的终点。
 */
export function logDetailPath(id: number): string {
  return `/logs/${id}/chat-io`
}
