import type { ChatLog } from "@/lib/api"
import { formatBytes, formatCost, formatDurationNs, formatFull, formatNumber, formatTps } from "@/lib/format"

/**
 * 日志表列注册表。
 *
 * 表头与单元格共用这一份定义，杜绝"表头改了列名、单元格还在显示旧值"的漂移。
 * 每列有一个 `value` 取值函数（纯函数，无 IO，进覆盖率门禁）和一个可选的
 * `kind`（告诉渲染层这个单元格需要特殊处理：状态徽标、缓存率等）。
 *
 * `always` 列（勾选框、冻结 ID、操作）永远显示、不可隐藏；其余列的可见性
 * 由 `defaultVisible` 决定初始状态，用户可在"列"菜单里勾选并落 localStorage。
 * 被注册表移除的列 id 会在 prefs 侧被 `keepKnownColumns` 修剪掉。
 */

export interface LogColumn {
  /** 稳定标识，同时是 localStorage 里的列 id。 */
  id: string
  /** i18n 键（`logs:table.<id>`）。特殊列（勾选框、操作）留空。 */
  labelKey: string
  /** 永远显示、不可隐藏。 */
  always?: boolean
  /** 首次加载时的默认可见性。 */
  defaultVisible?: boolean
  /** 表头单元格的附加类名。 */
  headClass?: string
  /** 数据单元格的附加类名。 */
  cellClass?: string
  /**
   * 取值函数：把一条日志变成显示文本。纯函数，无 IO。
   * 特殊列（勾选框、操作、状态、缓存）不需要取值函数，渲染层按 `kind` 或 `id` 处理。
   */
  value?: (log: ChatLog) => string
  /** 特殊渲染器提示。缺省即纯文本。 */
  kind?: "status" | "cache"
}

/** 计算一条日志的总成本。单价按每 1M tokens 计，与 log-detail-sheet 同口径。 */
export function computeCost(log: ChatLog): number {
  const cached = log.prompt_tokens_details?.cached_tokens ?? 0
  const prompt = log.prompt_tokens ?? 0
  const completion = log.completion_tokens ?? 0
  const inputPrice = log.input_price ?? 0
  const cachePrice = log.cache_read_price ?? 0
  const outputPrice = log.output_price ?? 0
  return (
    (Math.max(0, prompt - cached) / 1e6) * inputPrice +
    (cached / 1e6) * cachePrice +
    (completion / 1e6) * outputPrice
  )
}

/** 缓存命中率（%）。输入为 0 时返回 null，表示"不适用"而非 0%。 */
export function cacheRate(log: ChatLog): number | null {
  const prompt = log.prompt_tokens ?? 0
  if (prompt <= 0) return null
  return ((log.prompt_tokens_details?.cached_tokens ?? 0) / prompt) * 100
}

/**
 * 列注册表。
 *
 * 顺序就是用户在"列"菜单里看到的顺序，也是表头从左到右的顺序。
 * `always` 列放在首尾（勾选框在最左、操作在最右），冻结 ID 列紧跟其后。
 */
export const LOG_COLUMNS: readonly LogColumn[] = [
  { id: "select", labelKey: "", always: true, headClass: "w-10" },
  { id: "id", labelKey: "table.id", always: true, headClass: "sticky left-0 z-20 w-20", cellClass: "reading sticky left-0 z-10 bg-background text-xs text-muted-foreground", value: (l) => String(l.ID) },
  { id: "time", labelKey: "table.time", defaultVisible: true, cellClass: "reading whitespace-nowrap text-xs text-muted-foreground", value: (l) => formatFull(new Date(l.CreatedAt).getTime()) },
  { id: "request_name", labelKey: "table.request_name", defaultVisible: true, cellClass: "max-w-[160px] truncate font-medium", value: (l) => l.Name },
  { id: "project", labelKey: "table.project", defaultVisible: true, cellClass: "max-w-[120px] truncate text-xs", value: (l) => l.key_name || "-" },
  { id: "status", labelKey: "table.status", defaultVisible: true, kind: "status" },
  { id: "tokens", labelKey: "table.tokens", defaultVisible: true, cellClass: "reading", value: (l) => formatNumber(l.total_tokens) },
  { id: "cache", labelKey: "table.cache", defaultVisible: true, cellClass: "reading text-xs", kind: "cache" },
  { id: "size", labelKey: "table.size", defaultVisible: true, cellClass: "reading text-xs text-muted-foreground", value: (l) => (l.Size ? formatBytes(l.Size) : "-") },
  { id: "duration", labelKey: "table.duration", defaultVisible: true, cellClass: "reading text-xs", value: (l) => formatDurationNs(l.ChunkTime + l.FirstChunkTime + l.ProxyTime) },
  { id: "model", labelKey: "table.model", defaultVisible: true, cellClass: "max-w-[140px] truncate text-xs", value: (l) => l.ProviderModel },
  { id: "type", labelKey: "table.type", defaultVisible: true, cellClass: "text-xs", value: (l) => l.Style },
  { id: "provider", labelKey: "table.provider", defaultVisible: true, cellClass: "max-w-[120px] truncate text-xs", value: (l) => l.ProviderName },
  { id: "actions", labelKey: "table.actions", always: true, headClass: "w-24" },
  // ── 候选列（已入库、默认隐藏）──
  { id: "trace_id", labelKey: "table.trace_id", cellClass: "max-w-[120px] truncate font-mono text-xs", value: (l) => l.TraceID || "-" },
  { id: "session_id", labelKey: "table.session_id", cellClass: "max-w-[120px] truncate font-mono text-xs", value: (l) => l.SessionID || "-" },
  { id: "tps", labelKey: "table.tps", cellClass: "reading text-xs", value: (l) => formatTps(l.Tps) },
  { id: "cost", labelKey: "table.cost", cellClass: "reading text-xs", value: (l) => formatCost(computeCost(l), l.currency) },
  { id: "retry", labelKey: "table.retry", cellClass: "reading text-xs", value: (l) => (l.Retry > 0 ? String(l.Retry) : "-") },
  { id: "remote_ip", labelKey: "table.remote_ip", cellClass: "max-w-[120px] truncate font-mono text-xs", value: (l) => l.RemoteIP || "-" },
  { id: "user_agent", labelKey: "table.user_agent", cellClass: "max-w-[200px] truncate text-xs", value: (l) => l.UserAgent || "-" },
  { id: "upstream_style", labelKey: "table.upstream_style", cellClass: "text-xs", value: (l) => l.upstream_style || "-" },
  { id: "bridge_notes", labelKey: "table.bridge_notes", cellClass: "max-w-[160px] truncate text-xs", value: (l) => l.bridge_notes || "-" },
]

/** 当前所有列的 id，按注册顺序排列。用于 prefs 的 `keepKnownColumns`。 */
export const LOG_COLUMN_IDS: readonly string[] = LOG_COLUMNS.map((c) => c.id)

/** 所有可切换的列 id（不含 `always` 的列——它们不需要持久化）。 */
export const LOG_TOGGLEABLE_IDS: readonly string[] = LOG_COLUMNS.filter((c) => !c.always).map((c) => c.id)

/** 默认可见的列 id（`always` + `defaultVisible`）。 */
export const LOG_DEFAULT_VISIBLE_IDS: readonly string[] = LOG_COLUMNS.filter(
  (c) => c.always || c.defaultVisible
).map((c) => c.id)

/** 按可见性过滤列：给定用户勾选的 id 列表，返回应渲染的列（按注册顺序）。 */
export function visibleColumns(columns: string[]): LogColumn[] {
  if (columns.length === 0) {
    return LOG_COLUMNS.filter((c) => c.always || c.defaultVisible)
  }
  const set = new Set(columns)
  return LOG_COLUMNS.filter((c) => c.always || set.has(c.id))
}
