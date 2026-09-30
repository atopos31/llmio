/**
 * 展示层格式化。
 *
 * 放在 lib/ 而非组件内，是为了纳入覆盖率门禁——这些函数有真实分支
 * （单位切换的阈值、零值、负数），且它们的输出直接被人阅读，
 * 算错不会报错、只会给出误导性的数字。
 */

/** 紧凑数字：12900 → "12.9K"。用于卡片与轴刻度。 */
export function compactNumber(value: number): string {
  if (!Number.isFinite(value)) return "0"
  const abs = Math.abs(value)
  const sign = value < 0 ? "-" : ""
  if (abs >= 1e9) return `${sign}${trim(abs / 1e9)}B`
  if (abs >= 1e6) return `${sign}${trim(abs / 1e6)}M`
  if (abs >= 1e4) return `${sign}${trim(abs / 1e3)}K`
  return value.toLocaleString()
}

/** 千分位整数。用于精确值（表格里的计数、token 数）。 */
export function formatNumber(value: number): string {
  if (!Number.isFinite(value)) return "0"
  return Math.round(value).toLocaleString()
}

/** 保留一位小数且去掉无意义的 .0。 */
function trim(n: number): string {
  const rounded = Math.round(n * 10) / 10
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1)
}

/** 毫秒 → 自适应单位（µs / ms / s）。用于耗时展示。 */
export function formatDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms === 0) return "0ms"
  const abs = Math.abs(ms)
  if (abs < 1) return `${(ms * 1000).toFixed(0)}µs`
  if (abs < 1000) return `${trim(ms)}ms`
  return `${trim(ms / 1000)}s`
}

/** 百分比，一位小数。入参已是 0-100 的口径（后端就是这么返回的）。 */
export function formatPercent(value: number, digits = 1): string {
  if (!Number.isFinite(value)) return "0%"
  return `${value.toFixed(digits)}%`
}

/** 金额。未知币种时按 ¥ 展示并原样带出币种文本。 */
export function formatCost(value: number, currency: string): string {
  if (!Number.isFinite(value)) return "—"
  const symbol = currency === "USD" ? "$" : currency === "CNY" ? "¥" : ""
  // 成本通常很小，两位小数会把小额全部显示成 0.00，因此保四位
  const num = value.toFixed(4)
  return symbol ? `${symbol}${num}` : `${num} ${currency || ""}`.trim()
}

/** 币种符号，供图表轴用。 */
export function currencySymbol(currency: string): string {
  if (currency === "USD") return "$"
  if (currency === "CNY") return "¥"
  return ""
}

/** 时间戳（Unix 毫秒）→ HH:MM。趋势图的 X 轴刻度。 */
export function formatClock(ts: number): string {
  const d = new Date(ts)
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 时间戳 → MM-DD HH:MM。跨天窗口下 HH:MM 会看不出是哪天。 */
export function formatDateTime(ts: number): string {
  const d = new Date(ts)
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 时间戳 → YYYY-MM-DD HH:MM:SS。表格与详情用。 */
export function formatFull(ts: number): string {
  const d = new Date(ts)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes()
  )}:${pad(d.getSeconds())}`
}

/**
 * 趋势数据的桶宽（毫秒）：取相邻两点之差；不足两点时回落到 1 小时。
 *
 * 桶宽由服务端按档位阶梯决定，前端不重复维护一份阶梯——那是第二份真相来源。
 */
export function inferBucketMs(data: readonly { ts: number }[]): number {
  if (data.length < 2) return 60 * 60 * 1000
  return data[1].ts - data[0].ts
}

/** 序列是否跨过自然日（本地时区）。 */
function crossesDay(data: readonly { ts: number }[]): boolean {
  const first = new Date(data[0].ts)
  const last = new Date(data[data.length - 1].ts)
  return (
    first.getFullYear() !== last.getFullYear() ||
    first.getMonth() !== last.getMonth() ||
    first.getDate() !== last.getDate()
  )
}

/**
 * 生成趋势图 X 轴与提示条的标签格式化器。
 *
 * "要不要带日期"取决于**这条序列是否跨天**，而不是桶有多宽：1 小时的桶跨三天时，
 * 只显示 HH:MM 会让轴上出现三遍同样的时刻，读者分不清哪一段是哪天。
 * 判断收在工厂里，三个调用点就无法各自漏掉它——原来是各写一句
 * `formatBucketLabel(v, bucketMs)`，桶宽的判断对，跨天的判断缺。
 *
 * 标签是桶起点在**本地时区**的读数：服务端返回的是绝对时刻（Unix 毫秒），
 * 分桶也按绝对时间切，因此整小时偏移的时区下标签正好落在整点。
 */
export function bucketLabelFormatter(
  data: readonly { ts: number }[],
  bucketMs: number = inferBucketMs(data)
): (ts: number) => string {
  // 一天及以上的桶按"哪一天"读：读者关心的是这是几号，不是桶有多宽
  if (bucketMs >= 24 * 60 * 60 * 1000) {
    return (ts: number) => {
      const d = new Date(ts)
      return `${d.getMonth() + 1}/${d.getDate()}`
    }
  }
  // 单点序列没有"跨天"可言，且未必有可比的第二点
  if (data.length < 2 || !crossesDay(data)) return formatClock
  return formatDateTime
}

function pad(n: number): string {
  return String(n).padStart(2, "0")
}

/**
 * 把秒级时间轴上的值格式化为"3s / 1m20s"。
 * 首包耗时的原始单位是秒（后端 latency 系列如此）。
 */
export function formatSeconds(sec: number): string {
  if (!Number.isFinite(sec) || sec === 0) return "0s"
  if (sec < 60) return `${trim(sec)}s`
  const m = Math.floor(sec / 60)
  const s = Math.round(sec % 60)
  return `${m}m${s}s`
}

/**
 * 纳秒 → 自适应单位（µs / ms / s）。
 *
 * 与 formatDurationMs 分开是因为数据源单位不同：日志的 ProxyTime /
 * FirstChunkTime / ChunkTime 是 Go 的 time.Duration，JSON 出来是**纳秒**。
 * 交给调用方显式选择，胜过在同一函数里靠数值大小猜单位——
 * 猜测在跨数量级时会静默给出错一个量级的结果。
 */
export function formatDurationNs(ns: number): string {
  if (!Number.isFinite(ns) || ns === 0) return "0ms"
  const abs = Math.abs(ns)
  if (abs < 1_000) return `${ns.toFixed(0)}ns`
  if (abs < 1_000_000) return `${trim(ns / 1_000)}µs`
  if (abs < 1_000_000_000) return `${trim(ns / 1_000_000)}ms`
  return `${trim(ns / 1_000_000_000)}s`
}

/** 字节 → 自适应单位（B / KB / MB / GB）。 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes === 0) return "0 B"
  const abs = Math.abs(bytes)
  if (abs < 1024) return `${Math.round(bytes)} B`
  if (abs < 1024 ** 2) return `${trim(bytes / 1024)} KB`
  if (abs < 1024 ** 3) return `${trim(bytes / 1024 ** 2)} MB`
  return `${trim(bytes / 1024 ** 3)} GB`
}
