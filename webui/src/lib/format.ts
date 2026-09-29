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
 * 分桶宽度 → 人读的档位名。
 *
 * 不直接把桶宽当刻度标签用：1 天的桶应该显示日期而不是 "24h"，
 * 因为读者关心的是"这是哪一天"，不是"桶有多宽"。
 */
export function formatBucketLabel(ts: number, bucketMs: number): string {
  const dayMs = 24 * 60 * 60 * 1000
  if (bucketMs >= dayMs) {
    const d = new Date(ts)
    return `${d.getMonth() + 1}/${d.getDate()}`
  }
  return formatClock(ts)
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
