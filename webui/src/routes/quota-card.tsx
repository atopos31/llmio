import { useTranslation } from "react-i18next"
import { Brush, Loader2, PencilLine, Power, RefreshCw, Settings2 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { Meter } from "@/components/ui/meter"
import {
  itemOverrideKey,
  itemStyleOf,
  itemsOf,
  renderItemText,
  resetHint,
  ringLayout,
  sourceTypeKey,
  styleOf,
  windowLabel,
  type QuotaChartStyle,
  type QuotaItem,
  type QuotaSource,
  type QuotaSourceResult,
  type QuotaViewPrefs,
} from "@/lib/quota"
import { STATUS, type StatusKey } from "@/lib/palette"
import { cn } from "@/lib/utils"

/**
 * 一个数据源的余量卡片。
 *
 * 四种图表样式是**卡级**的（不是条目级）：同一个数据源的条目用同一种形态
 * 呈现，读者不必在每张卡里重新学一遍怎么读。
 */
export function QuotaCard({
  source,
  prefs,
  editable,
  refreshing,
  onRefresh,
  onEdit,
  onView,
  onEditItem,
}: {
  source: QuotaSourceResult
  prefs: QuotaViewPrefs
  editable: boolean
  refreshing: boolean
  onRefresh: (id: string) => void
  onEdit: (source: QuotaSourceResult) => void
  onView: (source: QuotaSourceResult) => void
  onEditItem: (source: QuotaSourceResult, item: QuotaItem) => void
}) {
  // 注意：本组件里有几处动态 key 需要 `as never`（契约名字是运行时才知道的），
  // 那会把 t 的重载集放宽，于是**带插值的静态调用也必须补 defaultValue**，
  // 否则类型落在"动态 key"那一支上要求第二参为字符串。看着冗余，但删了会编译失败。
  const { t } = useTranslation(["quota", "common"])
  const style = styleOf(prefs, source.id)
  const showMeta = prefs.showMeta

  const srcOverride = prefs.overrides[source.id] ?? {}
  const srcName = srcOverride.name || source.name
  const note = srcOverride.note || source.note || ""

  const allItems = itemsOf(source)
  const visibleItems = allItems.filter(
    (it) => !prefs.overrides[itemOverrideKey(source.id, it.id)]?.hidden
  )
  const hiddenCount = allItems.length - visibleItems.length

  return (
    <Card className="flex min-w-0 flex-col gap-2 py-3">
      <CardHeader className="gap-1.5 px-3">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className={cn("size-2 shrink-0 rounded-full", dotClass(source.status))} />
          <span className="min-w-0 truncate text-sm font-semibold" title={srcName}>
            {srcName}
          </span>
          <QuotaStatusBadge status={source.status} />
          <Badge variant="outline" className="font-normal">
            {t(`quota:${sourceTypeKey(source.type)}` as never, { defaultValue: source.type })}
          </Badge>
          {source.cached && (
            <Badge variant="secondary" className="font-normal">
              {t("card.cached")}
            </Badge>
          )}
          {hiddenCount > 0 && (
            <Badge variant="secondary" className="font-normal">
              {t("card.hidden_items", { count: hiddenCount, defaultValue: "{{count}} hidden" })}
            </Badge>
          )}

          <div className="ml-auto flex shrink-0 items-center gap-0.5">
            {source.latencyMs > 0 && (
              <span className="reading mr-1 text-xs text-muted-foreground">
                {t("card.latency", { ms: source.latencyMs })}
              </span>
            )}
            {editable && (
              <>
                {/* 次要操作只在悬停/聚焦时显形，但键盘 focus 必须等效触发——
                    否则纯键盘用户根本发现不了这些入口。 */}
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label={t("card.refresh_one")}
                  title={t("card.refresh_one")}
                  disabled={refreshing}
                  onClick={() => onRefresh(source.id)}
                >
                  {refreshing ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <RefreshCw className="size-3.5" />
                  )}
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label={t("card.display_settings")}
                  title={t("card.display_settings")}
                  onClick={() => onView(source)}
                >
                  <Brush className="size-3.5" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label={t("card.source_settings")}
                  title={t("card.source_settings")}
                  onClick={() => onEdit(source)}
                >
                  <Settings2 className="size-3.5" />
                </Button>
              </>
            )}
          </div>
        </div>
      </CardHeader>

      <CardContent className="flex flex-col gap-2.5 px-3">
        {!source.ok ? (
          <div className="rounded-md border border-status-critical/40 bg-status-critical/5 p-2">
            <p className="text-sm text-status-critical-ink">
              {firstLine(source.error) || t("error.load")}
            </p>
            {source.error && (
              <pre className="reading mt-1 max-h-24 overflow-auto text-xs break-all whitespace-pre-wrap text-muted-foreground">
                {source.error}
              </pre>
            )}
            {editable && (
              <Button
                variant="link"
                size="sm"
                className="h-auto px-0 text-xs"
                onClick={() => onEdit(source)}
              >
                {t("card.check_config")}
              </Button>
            )}
          </div>
        ) : (
          <>
            {source.warning && (
              <p className="text-xs text-status-warning-ink">{source.warning}</p>
            )}

            {visibleItems.length === 0 ? (
              <p className="py-3 text-center text-xs text-muted-foreground">
                {t("card.no_items")}
              </p>
            ) : (
              <QuotaChart
                style={style}
                items={visibleItems}
                showMeta={showMeta}
                prefs={prefs}
                sourceId={source.id}
                editable={editable}
                onEditItem={(it) => onEditItem(source, it)}
              />
            )}
          </>
        )}

        {(note || source.updatedAt > 0) && (
          <div className="mt-0.5 border-t border-dashed border-border pt-1.5 text-xs text-muted-foreground">
            {note || t("last_updated", { time: clockTime(source.updatedAt) })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

/**
 * 已停用数据源的卡片。
 *
 * 它存在的理由是**别处都没有它的落点**：停用源不进取数结果（服务端只跑
 * 启用源），于是卡片、条目、摘要条上都没有它。停用本身是个可逆的开关，
 * 界面上就必须留着一条走回去的路——否则用户只能去 shell 里改
 * db/quota.config.json，而那正是这张卡要消掉的事。
 *
 * 与正常卡的差别刻意做成"看得出、但不像坏掉"：虚线边框 + 中性徽标，
 * 不用红色——停用是用户的意图，不是故障。
 */
export function QuotaDisabledCard({
  source,
  prefs,
  editable,
  busy,
  onEnable,
  onEdit,
}: {
  source: QuotaSource
  prefs: QuotaViewPrefs
  editable: boolean
  busy: boolean
  onEnable: (source: QuotaSource) => void
  onEdit: (source: QuotaSource) => void
}) {
  const { t } = useTranslation(["quota", "common"])
  // 改名覆盖与正常卡同一口径：停用不该把用户起的显示名丢回原名
  const srcName = prefs.overrides[source.id]?.name || source.name

  return (
    <Card
      data-slot="quota-disabled-card"
      className="flex min-w-0 flex-col gap-2 border-dashed py-3"
    >
      <CardHeader className="gap-1.5 px-3">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span
            className="min-w-0 truncate text-sm font-semibold text-muted-foreground"
            title={srcName}
          >
            {srcName}
          </span>
          {/* 状态靠文字而不是靠"整张卡发灰"：灰度对读屏与色觉障碍都不成立 */}
          <Badge variant="secondary" className="font-normal">
            {t("card.disabled")}
          </Badge>
          <Badge variant="outline" className="font-normal">
            {t(`quota:${sourceTypeKey(source.type)}` as never, { defaultValue: source.type })}
          </Badge>

          {editable && (
            <div className="ml-auto flex shrink-0 items-center gap-0.5">
              <Button
                variant="outline"
                size="sm"
                className="h-7"
                disabled={busy}
                onClick={() => onEnable(source)}
              >
                {busy ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <Power className="size-3.5" />
                )}
                {t("card.enable")}
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label={t("card.source_settings")}
                title={t("card.source_settings")}
                disabled={busy}
                onClick={() => onEdit(source)}
              >
                <Settings2 className="size-3.5" />
              </Button>
            </div>
          )}
        </div>
      </CardHeader>

      <CardContent className="flex flex-col gap-2 px-3">
        <p className="text-xs text-muted-foreground">{t("card.disabled_hint")}</p>
        {source.note && (
          <p className="border-t border-dashed border-border pt-1.5 text-xs text-muted-foreground">
            {source.note}
          </p>
        )}
      </CardContent>
    </Card>
  )
}

/**
 * 卡片内的图表。四种样式各自成体，共用同一份条目数据。
 *
 * 图表一律**不把颜色当作唯一编码**：每条同时给出名称与数值文本；
 * 填充色只承载严重度，且与状态徽标（图标 + 文字）配对出现。
 */
function QuotaChart({
  style,
  items,
  showMeta,
  prefs,
  sourceId,
  editable,
  onEditItem,
}: {
  style: QuotaChartStyle
  items: QuotaItem[]
  showMeta: boolean
  prefs: QuotaViewPrefs
  sourceId: string
  editable: boolean
  onEditItem: (item: QuotaItem) => void
}) {
  const { t } = useTranslation("quota")

  const labelOf = (it: QuotaItem) =>
    prefs.overrides[itemOverrideKey(sourceId, it.id)]?.label || it.label
  const textOf = (it: QuotaItem) =>
    renderItemText(it, prefs.overrides[itemOverrideKey(sourceId, it.id)]?.format)

  /**
   * 什么时候重置。这是余量卡上唯一会随时间自己变旧的读数，也是用户看这
   * 一页最想知道的答案——"还要等多久"。opencode 这类套餐按 5 小时/周/月
   * 三档放量，接口本来就回了 resetsAt，只是此前没有一处渲染它。
   *
   * 按渲染时刻现算：卡片每次取数都会重渲染，读数跟着刷新的节奏走。
   * 因此不额外挂定时器——秒级跳动的倒计时对一个"多久"的问题没有增益。
   */
  const resetOf = (it: QuotaItem) => {
    const hint = resetHint(it.resetAt, Date.now())
    if (!hint) return ""
    if (hint.kind === "due") return t("card.reset_due")
    return t(`card.reset_in_${hint.unit}s` as never, { n: hint.value })
  }

  const withPercent = items.filter((it) => it.percent !== null)
  const withoutPercent = items.filter((it) => it.percent === null)

  const itemRow = (it: QuotaItem, extra?: React.ReactNode) => (
    <div key={it.id} className="group/item flex min-w-0 flex-col gap-1">
      <div className="flex min-w-0 items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-[13px]" title={labelOf(it)}>
          {labelOf(it)}
        </span>
        <span className="reading shrink-0 text-[13px] font-semibold">{textOf(it)}</span>
        {editable && (
          <Button
            variant="ghost"
            size="icon"
            className="size-6 opacity-0 transition-opacity group-hover/item:opacity-100 focus-visible:opacity-100"
            aria-label={t("card.edit_item")}
            title={t("card.edit_item")}
            onClick={() => onEditItem(it)}
          >
            <PencilLine className="size-3" />
          </Button>
        )}
      </div>
      {extra}
      {showMeta && (
        <div className="flex flex-wrap gap-1 text-[11px] text-muted-foreground">
          {windowLabel(it.window) && <span>{windowLabel(it.window)}</span>}
          {resetOf(it) && <span>· {resetOf(it)}</span>}
          {it.extra?.note != null && <span>· {String(it.extra.note)}</span>}
        </div>
      )}
    </div>
  )

  // 纯文字：最紧凑，连进度条都不画
  if (style === "text") {
    return (
      <div className="flex flex-col gap-1.5">
        {items.map((it) =>
          itemRow(
            it,
            <div className="flex items-center gap-2">
              <span className="reading text-xs text-muted-foreground">
                {percentText(it)}
              </span>
              <QuotaStatusBadge status={it.status} compact />
            </div>
          )
        )}
      </div>
    )
  }

  // 进度条：每条一行，信息最全
  if (style === "progress") {
    return (
      <div className="flex flex-col gap-2">
        {items.map((it) =>
          itemRow(
            it,
            it.percent === null ? null : (
              <Meter
                value={it.percent}
                fill={fillFor(it.percent)}
                trackLabel={`${labelOf(it)} ${percentText(it)}`}
              />
            )
          )
        )}
      </div>
    )
  }

  // 用量环：meter（单一比例对上限），不是仪表盘。
  //
  // 画哪几条由条目自己的样式决定（`ringLayout`）：用户在条目对话框里勾了
  // "用量环"的就画环，没勾过时回落到最紧张的那一条——一张卡上放不下几个环，
  // 因此默认仍然只挑一个。其余的画进度条而不是只列文字：同一份数据，
  // 有比例的至少该把比例画出来，"其余一律退化成文字"是旧版固定死的取舍。
  if (style === "ring") {
    const { rings, rest } = ringLayout(prefs, sourceId, items)
    if (!rings.length) {
      return (
        <p className="py-3 text-center text-xs text-muted-foreground">
          {t("card.no_percent")}
        </p>
      )
    }
    return (
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
          {rings.map((it) => {
            const pct = it.percent as number
            return (
              <div key={it.id} className="flex min-w-0 items-center gap-3">
                <UsageRing percent={pct} fill={fillFor(pct)} label={labelOf(it)} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-medium" title={labelOf(it)}>
                    {labelOf(it)}
                  </p>
                  <p className="reading text-sm font-semibold">{textOf(it)}</p>
                  <div className="mt-1 flex items-center gap-1.5">
                    <QuotaStatusBadge status={it.status} compact />
                    <span className="reading text-xs text-muted-foreground">
                      {t("card.bar_used", { percent: round1(pct) })}
                    </span>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
        {/* 画环之外的条目仍要能看到，否则"隐藏了几条"就成了信息黑洞 */}
        {rest.length > 0 && (
          <div className="flex flex-col gap-2 border-t border-dashed border-border pt-2">
            {rest.map((it) =>
              itemRow(
                it,
                it.percent === null || itemStyleOf(prefs, sourceId, it.id) === "text" ? null : (
                  <Meter
                    value={it.percent}
                    fill={fillFor(it.percent)}
                    trackLabel={`${labelOf(it)} ${percentText(it)}`}
                  />
                )
              )
            )}
          </div>
        )}
      </div>
    )
  }

  // 条形对比：把多条余量按同一坐标（0-100%）横排。
  // 用 HTML 而不是图表库：这里是"几条同构的条"，DOM 版本可键盘遍历、
  // 可选中复制，也不必为此加载一个图表库。
  return (
    <div className="flex flex-col gap-2">
      {withPercent.length === 0 ? (
        <p className="py-2 text-center text-xs text-muted-foreground">
          {t("card.no_percent")}
        </p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {withPercent.map((it) => (
            <div key={it.id} className="flex min-w-0 items-center gap-2">
              <span
                className="w-20 shrink-0 truncate text-[11px] text-muted-foreground"
                title={labelOf(it)}
              >
                {labelOf(it)}
              </span>
              <div className="min-w-0 flex-1">
                <Meter
                  value={it.percent as number}
                  fill={fillFor(it.percent as number)}
                  trackLabel={`${labelOf(it)} ${percentText(it)}`}
                />
              </div>
              <span className="reading w-24 shrink-0 text-right text-xs">
                {textOf(it)}
              </span>
            </div>
          ))}
        </div>
      )}
      {withoutPercent.map((it) => itemRow(it))}
    </div>
  )
}

/**
 * 用量环。
 *
 * 画的是 meter 的环形表达（单值占上限的比例），**不是仪表盘**：
 * 没有指针、没有刻度区间。数值在环心，名称在环下——环本身不承载文字，
 * 因为环越长可读区域越窄。
 *
 * 用 SVG 而不是图表库：一个圆 + 一段弧，用 dasharray 就能表达，
 * 为此引入一整个图表库不划算，而且库通常只提供 gauge（正是这里要避开的）。
 */
function UsageRing({
  percent,
  fill,
  label,
}: {
  percent: number
  fill: string
  label: string
}) {
  const clamped = Math.min(Math.max(percent, 0), 100)
  const r = 34
  const c = 2 * Math.PI * r
  return (
    // data-slot 与 Meter 的那一处同义：环是装饰性的 SVG，
    // 无障碍树上没有可依赖的 role，"哪几条画了环"只能靠它数出来
    <div data-slot="usage-ring" className="relative size-20 shrink-0">
      <svg viewBox="0 0 80 80" className="size-20 -rotate-90">
        <circle cx="40" cy="40" r={r} fill="none" stroke="var(--muted)" strokeWidth="8" />
        <circle
          cx="40"
          cy="40"
          r={r}
          fill="none"
          stroke={fill}
          strokeWidth="8"
          strokeLinecap="round"
          strokeDasharray={`${(clamped / 100) * c} ${c}`}
        />
      </svg>
      <span
        className="reading absolute inset-0 flex items-center justify-center text-sm font-semibold"
        aria-hidden="true"
      >
        {round1(clamped)}%
      </span>
      {/* 环本身对读屏是装饰：真实数值由旁边的文字与下方的 meter 语义承载 */}
      <span className="sr-only">{`${label} ${round1(clamped)}%`}</span>
    </div>
  )
}

export function QuotaStatusBadge({
  status,
  compact,
}: {
  status: QuotaSourceResult["status"]
  compact?: boolean
}) {
  const { t } = useTranslation("quota")
  return (
    <Badge
      variant="outline"
      className={cn(
        "gap-1 border-current/30 bg-transparent font-normal",
        toneClass(status),
        compact && "px-1.5 py-0 text-[11px]"
      )}
    >
      <StatusGlyph status={status} />
      {t(`quota:status.${status}` as never, { defaultValue: status })}
    </Badge>
  )
}

/**
 * 状态字形。
 *
 * 图标与文字必须同时出现（调色板的既定取舍）：warning 与 serious 的填充色
 * 在浅色面上低于 3:1，且红绿对立对色盲用户不可用。因此这里不用圆点，
 * 而是给每种状态一个形状不同的字形。
 */
function StatusGlyph({ status }: { status: QuotaSourceResult["status"] }) {
  const common = "size-3 shrink-0"
  switch (status) {
    case "ok":
      return (
        <svg viewBox="0 0 12 12" className={common} aria-hidden="true">
          <circle cx="6" cy="6" r="5" fill="currentColor" />
        </svg>
      )
    case "warning":
      return (
        <svg viewBox="0 0 12 12" className={common} aria-hidden="true">
          <path d="M6 1 L11.2 10.5 H0.8 Z" fill="currentColor" />
        </svg>
      )
    case "exhausted":
      return (
        <svg viewBox="0 0 12 12" className={common} aria-hidden="true">
          <path d="M2 2 L10 10 M10 2 L2 10" stroke="currentColor" strokeWidth="2" />
        </svg>
      )
    default:
      return (
        <svg viewBox="0 0 12 12" className={common} aria-hidden="true">
          <circle cx="6" cy="6" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="2 1.5" />
        </svg>
      )
  }
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

const TOKEN_KEY: Record<StatusKey | "muted", string> = {
  good: "text-status-good-ink",
  warning: "text-status-warning-ink",
  serious: "text-status-serious-ink",
  critical: "text-status-critical-ink",
  muted: "text-muted-foreground",
}

function statusToken(status: QuotaSourceResult["status"]): StatusKey | "muted" {
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

function toneClass(status: QuotaSourceResult["status"]): string {
  return TOKEN_KEY[statusToken(status)]
}

function dotClass(status: QuotaSourceResult["status"]): string {
  switch (status) {
    case "ok":
      return "bg-status-good"
    case "warning":
      return "bg-status-warning"
    case "exhausted":
      return "bg-status-critical"
    default:
      return "bg-muted-foreground"
  }
}

/**
 * 进度填充色。
 *
 * 用**状态色**而不是连续顺序色阶：余量的填充承载的是"还剩多少 → 是否该
 * 采取行动"，这是一个状态语义而不是量级语义。但颜色只做增强——
 * 旁边的文字与状态徽标已经说明了同样的事。
 */
function fillFor(percent: number | null): string {
  if (percent === null) return "var(--muted-foreground)"
  if (percent >= 100) return STATUS.critical.fill
  if (percent >= 80) return STATUS.warning.fill
  return STATUS.good.fill
}

function round1(v: number): number {
  return Math.round(v * 10) / 10
}

function percentText(it: QuotaItem): string {
  if (it.percent === null) return "—"
  return `${round1(it.percent)}%`
}

function firstLine(s?: string): string {
  return String(s ?? "").split("\n")[0]
}

function clockTime(ms: number): string {
  if (!ms) return ""
  const d = new Date(ms)
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`
}
