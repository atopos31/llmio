import type { StatusKey } from "@/lib/palette"

/**
 * 配额页的纯逻辑层：契约形状、格式化引擎镜像、展示偏好。
 *
 * ## 格式引擎为什么在这里有一份镜像，而不是只有 Go 一份
 *
 * 展示文本的**真相来源是服务端**（`quota/contract.go` 的 RenderItem），
 * 每一条余量的 `text` 都是服务端算好下发的。这一点没有变。
 *
 * 但「单条自定义格式」的实时预览必须在按键时立刻出结果，走一趟网络
 * 是不可接受的（用户正在调格式，每敲一个字符都要等 200ms 才知道对不对）。
 * 因此这里保留一份**同语义**的镜像，只服务于「尚未保存的格式覆盖」。
 *
 * 镜像的边界是明确的：
 *   - 服务端下发 `text` → 直接显示，**不**在这里重算
 *   - 用户在编辑器里改格式 → 用本文件的 renderItemText 出预览
 *
 * 原 dashboard 栽在同一处：前后端各一份 `quotaFormat.js` / `contract.js`，
 * 靠注释要求两侧同步，结果必然漂移。这里的镜像有测试钉在 Go 的语义上
 * （见 quota.test.ts 的对照表），但更重要的是**它不再参与主渲染路径**。
 */

// ---------------------------------------------------------------------------
// 单位与窗口（与 quota/contract.go 的归一结果一致）
// ---------------------------------------------------------------------------

/** 归一后的单位。与 Go 侧 Unit 的取值一一对应。 */
export type Unit = "%" | "CNY" | "USD" | "tokens" | "CREDITS" | "次" | ""

/** 单位大类，决定默认数值格式化。 */
export type UnitKind = "percent" | "money" | "amount" | "unknown"

const KIND_BY_UNIT: Record<Unit, UnitKind> = {
  "%": "percent",
  CNY: "money",
  USD: "money",
  tokens: "amount",
  CREDITS: "amount",
  次: "amount",
  "": "unknown",
}

export function unitKindOf(unit: Unit): UnitKind {
  return KIND_BY_UNIT[unit] ?? "unknown"
}

/** 归一后的窗口。与 Go 侧 Window 的取值一一对应。 */
export type Window = "5h" | "day" | "week" | "month" | "total" | ""

/**
 * 窗口的中文展示名。
 *
 * 与 Go 的 WindowLabel 一致：界面文案来自 i18n 的**其余部分**（页面标题、
 * 按钮），但这一处跟随服务端口径——{window} 占位符在服务端就是用中文渲染的，
 * 前端若改成 t() 就会出现"预览与保存后的结果不一样"。
 */
export function windowLabel(w: Window): string {
  switch (w) {
    case "5h":
      return "5 小时"
    case "day":
      return "每日"
    case "week":
      return "每周"
    case "month":
      return "每月"
    case "total":
      return "总额"
    default:
      return ""
  }
}

// ---------------------------------------------------------------------------
// 契约形状（对应 service/quota.go 的对外 JSON）
// ---------------------------------------------------------------------------

export type QuotaStatus = "ok" | "warning" | "exhausted" | "unknown"

/** 一条归一后的余量。字段与 quota/contract.go 的 Item 对齐。 */
export interface QuotaItem {
  id: string
  label: string
  /** nil 表示"上游没给"，与"给了 0"是两回事，因此用 null 而非 0。 */
  used: number | null
  total: number | null
  remaining: number | null
  /** **已用**百分比，不是剩余。 */
  percent: number | null
  unit: Unit
  window: Window
  resetAt?: string
  expireAt?: string
  status: QuotaStatus
  format?: string
  extra?: Record<string, unknown>
  /** 服务端按格式渲染好的展示文本。显示时**用它**，不要本地重算。 */
  text: string
}

export type QuotaSourceType = "builtin" | "http" | "script"

/** 一个数据源的取数结果。对应 service.SourceResult。 */
export interface QuotaSourceResult {
  id: string
  name: string
  type: QuotaSourceType
  enabled: boolean
  note?: string
  ok: boolean
  /**
   * 余量条目。**可能是 null**：Go 的 nil 切片序列化成 JSON null，
   * 而取数失败的源正是空的。别直接 .map/.filter，用 itemsOf()。
   */
  items: QuotaItem[] | null
  status: QuotaStatus
  error?: string
  warning?: string
  latencyMs: number
  updatedAt: number
  /** 本次结果来自缓存而非实取。 */
  cached: boolean
}

export interface QuotaWorstItem {
  id: string
  label: string
  status: QuotaStatus
  percent: number | null
  sourceId: string
  sourceName: string
}

export interface QuotaSummary {
  totalSources: number
  okSources: number
  failSources: number
  totalItems: number
  /** 成功源里状态最差的一条；没有任何条目时为 null。 */
  worst: QuotaWorstItem | null
}

export interface QuotaRunResult {
  generatedAt: number
  refreshInterval: number
  warningAt: number
  sources: QuotaSourceResult[]
  summary: QuotaSummary
}

/** 试跑结果。rawValue 是**未归一**的原始产出，供排查用。 */
export interface QuotaTestResult {
  ok: boolean
  /** 同上：解析不出条目时服务端发的是 null 而不是空数组。 */
  items: QuotaItem[] | null
  status: QuotaStatus
  error?: string
  warning?: string
  rawValue?: unknown
  durationMs: number
}

/** 数据源配置。对应 quota.Source，字段按类型分组。 */
export interface QuotaSource {
  id: string
  name: string
  enabled: boolean
  type: QuotaSourceType
  note?: string
  /** 覆盖全局告警阈值；0 表示沿用全局。 */
  warningAt?: number
  /** 单次取数超时（秒）；0 表示按类型取默认。 */
  timeout?: number

  // builtin
  builtin?: string
  baseUrl?: string
  apiKey?: string
  path?: string
  method?: string
  query?: unknown
  headers?: Record<string, unknown>
  itemsPath?: string
  map?: Record<string, unknown>

  // http
  url?: string
  body?: unknown
  /**
   * 鉴权。`user` 只对 basic 有意义，`token` 留空时后端回落到 {{apiKey}}
   * （因此密钥那一栏填的是 apiKey，不是这里）。
   */
  auth?: { type: string; header?: string; user?: string; token?: string }
  constants?: Record<string, unknown>

  // script
  scriptSource?: string
  env?: Record<string, string>
  allowFetch?: boolean
}

export interface QuotaConfig {
  refreshInterval: number
  warningAt: number
  sources: QuotaSource[]
}

/** 内置适配器的一条说明。对应 quota.BuiltinInfo。 */
export interface QuotaBuiltinInfo {
  id: string
  label: string
  doc: string
  defaultBaseUrl?: string
  defaultPath?: string
  /** 是否已实测。未实测的要在界面上标出来。 */
  verified: boolean
  /** 通用端点（走 itemsPath + map）而不是特定供应商。 */
  generic?: boolean
  /** 需要账号会话（登录型），导入时要留出待填的 env 键。 */
  needsLogin?: boolean
  /**
   * 该适配器从 env 白名单里读的键。登录型适配器的账号/口令/会话 Cookie
   * 都从这里进（沙箱与内置适配器都**不继承进程环境**），因此编辑器必须
   * 按它渲染对应的输入项——少了这个字段，超算与 opencode 就"选得出来、
   * 存不下去"：`quota.ValidateSource` 要求登录型的 env 非空。
   */
  envKeys?: string[]
}

export interface QuotaConfigResponse {
  config: QuotaConfig
  builtins: QuotaBuiltinInfo[]
  configPath: string
  /** 服务端是否允许写。false 时前端隐藏全部编辑入口。 */
  writeEnabled: boolean
  defaultRefresh: number
  defaultWarning: number
}

// ---------------------------------------------------------------------------
// 编辑器回填（完整配置 ↔ 表单）
// ---------------------------------------------------------------------------

/**
 * 编辑器的表单初值。
 *
 * 必须拿**完整配置**（`/api/quota/config` 下发的那份，密钥已脱敏）来构造，
 * 不能只用取数结果：`QuotaSourceResult` 是展示形状，只有 id / 名称 / 类型 /
 * 状态，拿它当表单初值等于让用户把 path、headers、字段映射、env 全部重填
 * 一遍——而保存走的是"整份替换"，没重填的那些就被静默清掉了。
 *
 * config 找不到时（两次读取之间配置被别处改过）退回展示形状已知的那几个
 * 字段，且**不猜** builtin：这里以前按名称反推适配器 id，名字里没有
 * "scnet" / "opencode" 字样就落到 deepseek——把超算源悄悄改成另一个适配器，
 * 比让用户自己选一次糟得多。
 */
export function editableSource(
  result: QuotaSourceResult,
  config: QuotaSource | undefined
): QuotaSource {
  if (config) return { ...config }
  return {
    id: result.id,
    name: result.name,
    enabled: result.enabled,
    type: result.type,
    note: result.note,
  }
}

/**
 * 「键=值」表 → 每行一条的文本。env / constants / query 共用。
 *
 * 字段映射（map）也走这里：字面量的标记 `=` 就存在值里（后端 MapRow 约定
 * 值以 `=` 开头即字面量），因此 `unit` = `=CREDITS` 写出来正是 `unit==CREDITS`，
 * 与表单里那套写法自然一致，不需要额外的反向规则。
 */
export function kvToText(v: Record<string, unknown> | undefined): string {
  if (!v) return ""
  return Object.entries(v)
    .map(([k, val]) => `${k}=${val === null || val === undefined ? "" : String(val)}`)
    .join("\n")
}

/**
 * JSON 形状（headers / body）→ 文本。null / undefined 给空串，
 * 不写出一句 "null" 让用户以为自己配过什么。
 *
 * 这里的值来自服务端下发的配置（JSON 往返过的），因此不必替 JSON.stringify
 * 兜底函数与 Symbol 那种"序列化不出来"的输入——那条分支够不着，写了就是
 * 一条永远没人走、也永远测不到的防御。
 */
export function jsonToText(v: unknown): string {
  if (v === null || v === undefined) return ""
  return JSON.stringify(v)
}

/** query 对象 → `page=1&size=20`。非对象（后端可能存成字符串）一律给空串。 */
export function queryToText(v: unknown): string {
  if (typeof v !== "object" || v === null) return ""
  const p = new URLSearchParams()
  for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
    if (val === null || val === undefined) continue
    p.append(k, String(val))
  }
  return p.toString()
}

// ---------------------------------------------------------------------------
// 格式化引擎（Go RenderTemplate / formatNum 的镜像）
// ---------------------------------------------------------------------------

/** 模板里可用的占位符。与 Go renderToken 的 switch 一一对应。 */
export const FORMAT_TOKENS = [
  { token: "{remaining}", key: "remaining" },
  { token: "{used}", key: "used" },
  { token: "{total}", key: "total" },
  { token: "{percent}", key: "percent" },
  { token: "{unit}", key: "unit" },
  { token: "{label}", key: "label" },
  { token: "{window}", key: "window" },
] as const

/**
 * ## 这份镜像的前置条件：条目必须是**服务端归一过的**
 *
 * 它读的是 `item.used / total / remaining` 三个字段**原样**，不重新做
 * 契约的"任二补一"（只给两个分量时推出第三个）。这是刻意的：补全规则只能
 * 有一处实现，把它再写一遍正是原 dashboard 前后端漂移的老路。
 *
 * 这个前提在真实路径上总是成立——预览面板的条目来自 `/api/quota/run`
 * 或 `/api/quota/test`，两者都跑过 Normalize，因此 `used=3,total=10`
 * 的条目到手时 `remaining` 已经是 7。
 *
 * 后果是：**不要拿未归一的原始数据构造 QuotaItem 再喂给这里**。
 * 那种输入下镜像会漏掉 `{remaining}`，而服务端不会。
 */

/**
 * 把大额数简写成万/亿，小数保留两位。Go humanizeAmount 的镜像。
 *
 * 中文计量习惯是四位一进（万、亿），不是英文的三位一进（K/M/B），
 * 因此这里不能复用 format.ts 的 compactNumber —— 那个是给英文界面用的。
 */
function humanizeAmount(n: number): string {
  const abs = Math.abs(n)
  if (abs >= 1e8) return trimZero(n / 1e8) + "亿"
  if (abs >= 1e4) return trimZero(n / 1e4) + "万"
  if (Number.isInteger(n)) return String(n)
  return trimZero(n)
}

/** 保留两位后去掉多余的尾零与小数点。Go trimZero 的镜像。 */
function trimZero(n: number): string {
  const s = n.toFixed(2).replace(/0+$/, "")
  return s.endsWith(".") ? s.slice(0, -1) : s
}

/**
 * 格式化一个数值。
 *
 * digits 由模板里的 `{used:2}` 决定；未指定时：金额两位、百分比一位、
 * 其余走万/亿简写（tokens 显示成 1234.5 没有意义）。
 */
export function formatNum(v: number | null, unit: Unit, digits: number): string {
  if (v === null || !Number.isFinite(v)) return ""
  if (digits >= 0) return v.toFixed(digits)

  switch (unitKindOf(unit)) {
    case "money":
      return v.toFixed(2)
    case "percent":
      return v.toFixed(1)
    default:
      return humanizeAmount(v)
  }
}

/**
 * 按条目**实际拿到的分量**给出默认格式模板。Go DefaultFormat 的镜像。
 *
 * 不能只看单位：额度类的默认模板 `{remaining} / {total} {unit}` 在缺 total 时
 * 会渲染成 `42 /  tokens`（悬空分隔符 + 双空格）。deepseek 的余额接口只返回
 * total_balance，正是最常见的"只有剩余"场景，等于是默认路径上的脏输出。
 */
export function defaultFormat(item: {
  used: number | null
  total: number | null
  remaining: number | null
  unit: Unit
}): string {
  const kind = unitKindOf(item.unit)

  // 百分比自带单位符号，不能拼 unit（否则会出 "8% %"）；
  // 它在归一阶段就固定由 remaining 推出，因此含义恒为"还剩多少"。
  if (kind === "percent") return "{remaining}%"

  // 单位为空时不拼 {unit}，避免模板里留下一个渲染成空串的尾随占位符
  const withUnit = (base: string) => (item.unit === "" ? base : `${base} {unit}`)

  if (item.used !== null && item.total !== null) {
    // 与百分比默认同一口径：这是余量面板，主体报"还剩多少"
    return withUnit("{remaining} / {total}")
  }
  if (item.remaining !== null) {
    // 金额类要报出币种（"12.50" 不如 "12.50 CNY" 有用），其余单位无歧义
    return kind === "money" ? withUnit("{remaining}") : "{remaining}"
  }
  if (item.used !== null) {
    return kind === "money" ? withUnit("{used}") : "{used}"
  }
  if (item.total !== null) return withUnit("{total}")
  return "{remaining}"
}

/** 模板键到渲染值的映射。 */
function tokenValue(key: string, item: QuotaItem): string | null {
  switch (key) {
    case "used":
      return item.used === null ? "" : formatNum(item.used, item.unit, -1)
    case "total":
      return item.total === null ? "" : formatNum(item.total, item.unit, -1)
    case "remaining":
      return item.remaining === null ? "" : formatNum(item.remaining, item.unit, -1)
    case "percent":
      return item.percent === null ? "" : item.percent.toFixed(1)
    case "unit":
      return item.unit
    case "label":
      return item.label
    case "window":
      return windowLabel(item.window)
    default:
      return null
  }
}

/**
 * 渲染一条余量的展示文本。
 *
 * **显示时不要调用它**：服务端已经把 text 算好下发了（含默认格式与条目
 * 自己的 format）。这里只用于编辑器里对**尚未保存**的自定义格式出预览。
 *
 * 未知占位符**原样保留**（Go 同样如此）：静默吞掉会让"模板写错了"
 * 看起来像"没数据"，而后者的排查方向完全不同。
 */
export function renderItemText(item: QuotaItem, formatOverride?: string): string {
  const format = formatOverride || item.format || defaultFormat(item)
  let out = ""
  let i = 0
  while (i < format.length) {
    if (format[i] !== "{") {
      out += format[i]
      i++
      continue
    }
    const end = format.indexOf("}", i)
    if (end < 0) {
      out += format.slice(i)
      break
    }
    const token = format.slice(i + 1, end)
    out += renderToken(token, item)
    i = end + 1
  }
  return out.trim()
}

function renderToken(token: string, item: QuotaItem): string {
  let name = token
  let digits = -1
  const colon = token.indexOf(":")
  if (colon >= 0) {
    name = token.slice(0, colon)
    const n = Number.parseInt(token.slice(colon + 1), 10)
    // 与 Go 一致：`:2` 生效，`:-1` / `:x` 视为未指定
    if (Number.isInteger(n) && n >= 0 && String(n) === token.slice(colon + 1)) digits = n
  }

  // 精度只作用于数值占位符
  if (digits >= 0 && (name === "used" || name === "total" || name === "remaining")) {
    const raw = name === "used" ? item.used : name === "total" ? item.total : item.remaining
    return raw === null ? "" : formatNum(raw, item.unit, digits)
  }
  if (name === "percent" && digits >= 0) {
    return item.percent === null ? "" : item.percent.toFixed(digits)
  }

  const v = tokenValue(name, item)
  return v === null ? `{${token}}` : v
}

// ---------------------------------------------------------------------------
// 展示偏好（纯前端，localStorage）
// ---------------------------------------------------------------------------

/** 图表样式。ring 是 **meter**（单一比例对上限），不是仪表盘。 */
export type QuotaChartStyle = "progress" | "ring" | "bar" | "text"

export const CHART_STYLES: { value: QuotaChartStyle; labelKey: string }[] = [
  { value: "progress", labelKey: "chartStyle.progress" },
  { value: "ring", labelKey: "chartStyle.ring" },
  { value: "bar", labelKey: "chartStyle.bar" },
  { value: "text", labelKey: "chartStyle.text" },
]

/** 数据源级或条目级的展示覆盖。 */
export interface QuotaOverride {
  name?: string
  note?: string
  label?: string
  format?: string
  chartStyle?: QuotaChartStyle
  hidden?: boolean
}

/**
 * 覆盖的**补丁**形态：字段给 null 表示清除该字段。
 *
 * 与存储形态分开，是为了让"null 即清除"只存在于写入边界上——
 * 存进 localStorage 的 `overrides` 里永远不会出现 null，
 * 读出来的对象因此不需要在每个消费点判空。
 */
export type QuotaOverridePatch = {
  [K in keyof QuotaOverride]?: QuotaOverride[K] | null
}

export interface QuotaViewPrefs {
  chartStyle: QuotaChartStyle
  showMeta: boolean
  /** 键：数据源 id，或 `sourceId::itemId`。 */
  overrides: Record<string, QuotaOverride>
  /** 存储格式版本，见 QUOTA_VIEW_VERSION。 */
  version: number
}

export const QUOTA_VIEW_STORAGE_KEY = "llmio-quota-view-v1"

/**
 * 当前存储格式版本。
 *
 * 1 → 2：条目级 `chartStyle` 的含义变了。v1 的样式是**整张卡片**生效的，
 * 早期版本却把它写在条目上，因此读到 v1 数据时要把条目级样式提升到数据源级，
 * 否则用户此前设置过的样式会被静默忽略。v2 起条目级样式是**真正的**条目级，
 * 提升会反过来毁掉用户的选择（把卡片的默认样式也一起改掉），所以只对 v1 做。
 *
 * 兼容性：v1 数据升级后**保留**条目上的样式——提升是把同一个值同时放在两级，
 * 与升级前渲染出来的一模一样。
 */
export const QUOTA_VIEW_VERSION = 2

export const DEFAULT_QUOTA_VIEW: QuotaViewPrefs = {
  chartStyle: "progress",
  showMeta: true,
  overrides: {},
  version: QUOTA_VIEW_VERSION,
}

const CHART_STYLE_VALUES: QuotaChartStyle[] = ["progress", "ring", "bar", "text"]

/**
 * 读取展示偏好。
 *
 * 任何异常都回落到默认值：这段代码在首屏渲染路径上，
 * 为一个纯装饰性的偏好抛错会让整页打不开（隐私模式、损坏的 localStorage、
 * 旧版本写下的异构结构都会走到这里）。
 */
export function loadQuotaView(raw: string | null): QuotaViewPrefs {
  if (!raw) return { ...DEFAULT_QUOTA_VIEW, overrides: {} }
  try {
    const parsed = JSON.parse(raw) as Partial<QuotaViewPrefs>
    const overrides = normalizeOverrides(parsed.overrides, parsed.version !== QUOTA_VIEW_VERSION)
    return {
      chartStyle:
        parsed.chartStyle && CHART_STYLE_VALUES.includes(parsed.chartStyle)
          ? parsed.chartStyle
          : "progress",
      showMeta: parsed.showMeta !== false,
      overrides,
      // 读出来就是当前版本：否则每次加载都要重跑一遍 v1 的提升，
      // 而用户已经明确选过"这条画环、卡片默认进度条"时那会改掉卡片样式
      version: QUOTA_VIEW_VERSION,
    }
  } catch {
    return { ...DEFAULT_QUOTA_VIEW, overrides: {} }
  }
}

/**
 * 归一覆盖表。
 *
 * `promoteLegacy` 只在读到 v1 数据时为真（见 QUOTA_VIEW_VERSION）：那时
 * 条目上的 chartStyle 表达的是**卡片**样式，提升到数据源级才不会让用户
 * 此前的设置被静默忽略（表现为"我改过样式，怎么没生效"，且无从排查）。
 *
 * 提升时**保留**条目上的原值：v2 起条目样式是条目级的，留着它渲染结果与
 * v1 完全一致，而删掉就意味着用户再点开这条时会看到"没设置过"。
 */
function normalizeOverrides(
  input: unknown,
  promoteLegacy: boolean
): Record<string, QuotaOverride> {
  if (!input || typeof input !== "object") return {}
  const out: Record<string, QuotaOverride> = {}
  for (const [key, value] of Object.entries(input as Record<string, unknown>)) {
    if (!value || typeof value !== "object") continue
    const patch = { ...(value as QuotaOverride) }
    if (promoteLegacy && key.includes("::") && patch.chartStyle) {
      const sid = key.split("::")[0]
      if (!out[sid]?.chartStyle) {
        out[sid] = { ...out[sid], chartStyle: patch.chartStyle }
      }
    }
    // 对象展开 undefined 是空操作，因此这里不需要兜底分支
    if (Object.keys(patch).length) out[key] = { ...out[key], ...patch }
  }
  return out
}

export function saveQuotaView(prefs: QuotaViewPrefs): string {
  return JSON.stringify(prefs)
}

/**
 * 合并式写入一条覆盖。patch 里给空字符串 / null / false 表示**清除**该字段。
 *
 * 清除而非置假值：`hidden: false` 与"没设置过"在语义上等价，
 * 把它们区分开只会让 overrides 里堆满无意义的空对象。
 */
export function applyOverride(
  prefs: QuotaViewPrefs,
  key: string,
  patch: QuotaOverridePatch
): QuotaViewPrefs {
  const cur: QuotaOverride = { ...(prefs.overrides[key] || {}) }
  for (const [k, v] of Object.entries(patch) as [keyof QuotaOverride, unknown][]) {
    if (v === "" || v === null || v === undefined || v === false) delete cur[k]
    else (cur as Record<string, unknown>)[k] = v
  }
  const overrides = { ...prefs.overrides }
  if (Object.keys(cur).length) overrides[key] = cur
  else delete overrides[key]
  return { ...prefs, overrides }
}

export function itemOverrideKey(sourceId: string, itemId: string): string {
  return `${sourceId}::${itemId}`
}

/** 该数据源最终生效的图表样式：源级覆盖 > 全局默认。 */
export function styleOf(prefs: QuotaViewPrefs, sourceId: string): QuotaChartStyle {
  return prefs.overrides[sourceId]?.chartStyle || prefs.chartStyle
}

/**
 * 这一条**自己**指定的样式；没指定过就是 undefined（跟随卡片）。
 *
 * 与 styleOf 的区别是要紧的：卡片样式决定整张卡怎么摆，条目样式只决定
 * 这一条在卡片里长什么样。卡片是 ring 时条目指定 progress，意思是
 * "别的画环，这条画进度条"。
 */
export function itemStyleOf(
  prefs: QuotaViewPrefs,
  sourceId: string,
  itemId: string
): QuotaChartStyle | undefined {
  return prefs.overrides[itemOverrideKey(sourceId, itemId)]?.chartStyle
}

/**
 * 环样式卡片的分组：哪几条画环、哪几条不画。
 *
 * 规则（用户要的是"选定哪几个显示用量环，其他画进度条"）：
 *
 *   - 明确指定了 ring 的条目画环；
 *   - 一个都没指定时回落到**默认那一条**（最紧张的，仅在有百分比的条目里挑），
 *     保持卡片在没有人工干预时与从前一样；
 *   - 明确指定了别的样式的条目**不参与**默认回落——用户既然说了"这条画进度条"，
 *     它就不该因为恰好最紧张又被拎出来画环。
 *
 * 返回值里 `rest` 是除环以外的全部条目（顺序不变），由调用方决定各自画什么。
 */
export function ringLayout(
  prefs: QuotaViewPrefs,
  sourceId: string,
  items: QuotaItem[]
): { rings: QuotaItem[]; rest: QuotaItem[] } {
  const rings = items.filter((it) => itemStyleOf(prefs, sourceId, it.id) === "ring")
  if (rings.length) {
    return { rings, rest: items.filter((it) => !rings.includes(it)) }
  }
  const pool = items.filter(
    (it) => it.percent !== null && itemStyleOf(prefs, sourceId, it.id) === undefined
  )
  const fallback = tightestItem(pool)
  if (!fallback) return { rings: [], rest: items }
  return { rings: [fallback], rest: items.filter((it) => it.id !== fallback.id) }
}

/** 数据源被删除时清理它的全部覆盖，避免残留（否则重建同名源会"继承"旧设置）。 */
export function pruneOverrides(prefs: QuotaViewPrefs, validIds: string[]): QuotaViewPrefs {
  const keep = new Set(validIds)
  const overrides: Record<string, QuotaOverride> = {}
  for (const [key, value] of Object.entries(prefs.overrides)) {
    if (keep.has(key.split("::")[0])) overrides[key] = value
  }
  return { ...prefs, overrides }
}

/**
 * 清掉某个数据源的**全部**覆盖，含 `::` 条目级的。
 *
 * 与 pruneOverrides 都是"删除"，区别在触发面：pruneOverrides 按一组存活 id
 * 批量清（源被删时用），这里只清一个源（卡片上的"重置"用）。
 *
 * 重置必须连条目级一起清：卡片代表的就是这一个源，只清源级键会留下半截
 * 状态——名称和样式恢复了，条目上的自定义还在，那不是"重置"。
 * 也不能靠 `setOverride(key, {...全部字段置空})` 拼出来：patch 的类型是
 * `QuotaOverridePatch`，根本收不下 `::` 条目级的键。
 */
export function clearSourceOverrides(
  prefs: QuotaViewPrefs,
  sourceId: string
): QuotaViewPrefs {
  const prefix = `${sourceId}::`
  const overrides: Record<string, QuotaOverride> = {}
  for (const [key, value] of Object.entries(prefs.overrides)) {
    if (key !== sourceId && !key.startsWith(prefix)) overrides[key] = value
  }
  return { ...prefs, overrides }
}

// ---------------------------------------------------------------------------
// 余量状态的呈现映射
// ---------------------------------------------------------------------------

/**
 * 余量状态 → 语义状态色槽。
 *
 * 不能复用 `statusForRequest`：那个是请求三态（success/running/error），
 * 而余量有第四态 `exhausted`，且它的语义是"已经用光"（critical），
 * 不是请求意义上的"出错"。硬套会把"用尽了"渲染成普通告警。
 *
 * `unknown` 走中性色而不是 warning：它是"取数看不懂"，不是"余量紧张"。
 * 把它染成告警色会让真正紧张的条目淹没在同样的黄色里。
 */
export function statusForQuota(status: QuotaStatus): StatusKey | "muted" {
  switch (status) {
    case "ok":
      return "good"
    case "warning":
      return "warning"
    case "exhausted":
      return "critical"
    default:
      return "muted"
  }
}

/** 状态对应的文字色类名。与 status-mark.tsx 的 statusToneClass 同一形态。 */
export function quotaStatusTone(status: QuotaStatus): string {
  const key = statusForQuota(status)
  return key === "muted" ? "text-muted-foreground" : `text-status-${key}-ink`
}

/**
 * 取一个数据源的条目列表，永远给数组。
 *
 * 存在的理由：Go 把 nil 切片序列化成 `null`，因此"取数失败/还没解析出条目"
 * 在 JSON 里是 `items: null`。类型上写成 `QuotaItem[] | null` 是诚实，
 * 但每个消费点都写 `?? []` 既啰嗦又容易漏一处——漏掉就是整页白屏
 * （实测过：卡片在 `source.items.filter` 上抛错，React 卸载了整棵树）。
 * 因此把归一收在一个有测试的函数里。
 */
export function itemsOf(source: { items: QuotaItem[] | null } | null | undefined): QuotaItem[] {
  return source?.items ?? []
}

// ---------------------------------------------------------------------------
// 摘要与派生量
// ---------------------------------------------------------------------------

/**
 * 取一条余量的"紧张程度"排序键。
 *
 * 与 Go 的 StatusRank 一致：未知状态按 unknown 计，而**不是排到最后**——
 * 排最后会让它盖过真实的 exhausted，"最差"就报错了。
 */
const STATUS_RANK: Record<QuotaStatus, number> = {
  exhausted: 3,
  warning: 2,
  unknown: 1,
  ok: 0,
}

export function statusRank(status: QuotaStatus): number {
  return STATUS_RANK[status] ?? STATUS_RANK.unknown
}

/**
 * 一张卡片里"最紧张的一条"：先比状态序，再比百分比。
 *
 * 只统计可比较的条目（percent 不为 null）。没有总量就算不出百分比，
 * 拿它当"最紧张"会得到一个恒为 null 的答案。
 */
export function tightestItem(items: QuotaItem[]): QuotaItem | null {
  // 用类型谓词把 percent 收窄成 number：这样下面比较时不必写 `?? 0`，
  // 也就不会留下一个永远走不到的兜底分支去干扰覆盖率。
  const ranked = items.filter(
    (it): it is QuotaItem & { percent: number } => it.percent !== null
  )
  if (!ranked.length) return null
  return ranked.reduce((a, c) => {
    const byStatus = statusRank(c.status) - statusRank(a.status)
    if (byStatus !== 0) return byStatus > 0 ? c : a
    return c.percent > a.percent ? c : a
  })
}

/** 摘要条的文案片段。没有最紧张条目时返回 null（不编造"一切正常"）。 */
export function worstLabel(worst: QuotaWorstItem | null): string | null {
  if (!worst) return null
  const pct = worst.percent === null ? "" : ` ${formatPercentValue(worst.percent)}%`
  return `${worst.sourceName} · ${worst.label}${pct}`
}

function formatPercentValue(v: number): string {
  const rounded = Math.round(v * 10) / 10
  return String(rounded)
}

/** 数据源类型 → i18n 键。 */
export function sourceTypeKey(type: QuotaSourceType): string {
  switch (type) {
    case "builtin":
      return "type.builtin"
    case "http":
      return "type.http"
    default:
      return "type.script"
  }
}
