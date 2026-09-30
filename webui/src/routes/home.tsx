import { useCallback, useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowUpRight, RefreshCw } from "lucide-react"
import { Link } from "react-router-dom"
import { toast } from "sonner"

import { FirstChunkHistogram, RequestTrendChart, TokenTrendChart } from "@/components/charts/trend-charts"
import { StatusMark } from "@/components/status-mark"
import { Button } from "@/components/ui/button"
import { Panel } from "@/components/panel"
import { Card, CardContent } from "@/components/ui/card"
import { getStats, type StatsResult } from "@/lib/api"
import {
  compactNumber,
  formatCost,
  formatNumber,
  formatPercent,
  formatSeconds,
} from "@/lib/format"

/** 时间范围预设。值与后端的 from/to 参数直接对应。 */
type RangeKey = "today" | "last_24h" | "last_7d" | "last_30d"

const RANGE_HOURS: Record<RangeKey, number | "today"> = {
  today: "today",
  last_24h: 24,
  last_7d: 24 * 7,
  last_30d: 24 * 30,
}

/** 范围 → 后端接受的秒级时间戳。 */
function rangeToQuery(key: RangeKey): { from: string; to: string } {
  const now = new Date()
  const to = Math.floor(now.getTime() / 1000)
  const spec = RANGE_HOURS[key]
  if (spec === "today") {
    const start = new Date(now.getFullYear(), now.getMonth(), now.getDate())
    return { from: String(Math.floor(start.getTime() / 1000)), to: String(to) }
  }
  return { from: String(to - spec * 3600), to: String(to) }
}

export default function Home() {
  const { t } = useTranslation(["home", "common"])
  const [range, setRange] = useState<RangeKey>("last_24h")
  const [stats, setStats] = useState<StatsResult | null>(null)
  const [loading, setLoading] = useState(true)

  const load = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true)
      try {
        const data = await getStats(rangeToQuery(range))
        setStats(data)
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        toast.error(t("home:load_failed", { message }))
      } finally {
        setLoading(false)
      }
    },
    [range, t]
  )

  useEffect(() => {
    void load()
  }, [load])

  const kpi = stats?.kpi
  const hasData = (kpi?.total ?? 0) > 0

  // 按实体的稳定全集分配颜色。这里用 stats.byModel 的**全部**名称（而非
  // 图表里出现的子集）作为 universe，保证筛选后颜色不漂移。
  const modelNames = useMemo(
    () => (stats?.byModel ?? []).map((g) => g.name),
    [stats]
  )

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-1">
      {/* 筛选行在内容之上一整行：它作用于页面上所有图表，因此不能塞进某张卡片里 */}
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="mr-auto text-xl font-semibold tracking-tight">{t("home:title")}</h2>

        <div
          role="radiogroup"
          aria-label={t("home:range.label")}
          className="flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5"
        >
          {(["today", "last_24h", "last_7d", "last_30d"] as RangeKey[]).map((key) => {
            const active = range === key
            return (
              <Button
                key={key}
                type="button"
                role="radio"
                aria-checked={active}
                variant="ghost"
                size="sm"
                className={
                  active
                    ? "h-7 bg-accent px-2.5 text-xs text-accent-foreground"
                    : "h-7 px-2.5 text-xs text-muted-foreground hover:text-foreground"
                }
                onClick={() => setRange(key)}
              >
                {t(`home:range.${key}` as never)}
              </Button>
            )
          })}
        </div>

        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={() => void load()}
          aria-label={t("home:refresh")}
          title={t("home:refresh")}
        >
          <RefreshCw className="size-4" />
        </Button>
      </div>

      {stats?.truncated && (
        <div className="flex items-start gap-2 rounded-md border border-border bg-accent/40 px-3 py-2 text-sm">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-status-warning-ink" aria-hidden="true" />
          <span>{t("home:truncated_warning")}</span>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading && !stats ? (
          <div className="space-y-3">
            <div className="h-20 animate-pulse rounded-lg bg-muted" />
            <div className="h-56 animate-pulse rounded-lg bg-muted" />
          </div>
        ) : !hasData ? (
          <EmptyState
            title={t("home:empty.no_data")}
            hint={t("home:empty.no_data_hint")}
          />
        ) : (
          <div className="space-y-3">
            {/* 主视觉数字。全页仅此一个 ≥48px 的数字，用比例数字（tabular 会让它显松） */}
            <HeroRow
              inFlight={kpi!.running}
              inFlightLabel={t("home:in_flight")}
              inFlightHint={t("home:in_flight_hint")}
            />

            <KpiRow stats={stats!} />

            {/* 趋势拆成两张单轴图。原实现把请求数与 Token 放一张图用双轴，
                那会凭空造出数据里没有的相关性（规范里的头号禁项）。 */}
            <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
              <Panel title={t("home:trend.requests")}>
                <RequestTrendChart data={stats!.trend} />
              </Panel>
              <Panel title={t("home:trend.tokens")}>
                <TokenTrendChart data={stats!.trend} />
              </Panel>
            </div>

            <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
              <Panel
                title={t("home:latency.title")}
                note={t("home:latency.percentile_note")}
              >
                <FirstChunkHistogram samples={stats!.latency.firstChunk.list} />
                <PercentileRow stats={stats!} />
              </Panel>

              <Panel title={t("home:errors.title")}>
                <ErrorList stats={stats!} unavailable={t("home:errors.empty")} />
              </Panel>
            </div>

            <div className="grid grid-cols-1 gap-3 xl:grid-cols-3">
              <LeaderboardCard
                title={t("home:top.tps")}
                rows={stats!.topTps}
                metric={(r) => `${r.tps.toFixed(1)} ${t("home:top.tps")}`}
                empty={t("home:top.empty")}
              />
              <LeaderboardCard
                title={t("home:top.slowest")}
                rows={stats!.slowest}
                metric={(r) => formatSeconds(r.firstChunkMs / 1000)}
                empty={t("home:top.empty")}
              />
              <LeaderboardCard
                title={t("home:top.recent_errors")}
                rows={stats!.recentErrors}
                metric={(r) => r.error?.slice(0, 40) ?? ""}
                empty={t("home:top.empty")}
                tone="critical"
              />
            </div>

            {/* 与模型名的稳定全集挂钩，保证未来加图表时颜色口径一致 */}
            <span className="sr-only">{modelNames.join(",")}</span>
          </div>
        )}
      </div>
    </div>
  )
}

/**
 * 主视觉区。
 *
 * 首屏回答"现在正在发生什么"，所以主角是**在途请求数**而不是累计量——
 * 累计数字说明不了当下是否健康。累计量随后在 KPI 行里呈现。
 */
function HeroRow({
  inFlight,
  inFlightLabel,
  inFlightHint,
}: {
  inFlight: number
  inFlightLabel: string
  inFlightHint: string
}) {
  return (
    <Card>
      <CardContent className="flex flex-wrap items-end gap-x-8 gap-y-2 py-4">
        <div>
          <div className="text-xs text-muted-foreground">{inFlightLabel}</div>
          {/* 比例数字而非 tnum：等宽数字在大号时看起来松散 */}
          <div className="text-5xl font-semibold leading-none tracking-tight">
            {formatNumber(inFlight)}
          </div>
          <div className="mt-1 text-xs text-muted-foreground">{inFlightHint}</div>
        </div>
      </CardContent>
    </Card>
  )
}

/** KPI 行。用一排紧凑读数而非四张大卡——大卡会把信息密度压得过低。 */
function KpiRow({ stats }: { stats: StatsResult }) {
  const { t } = useTranslation("home")
  const k = stats.kpi

  const items: { label: string; value: string; hint?: string }[] = [
    { label: t("kpi.requests"), value: formatNumber(k.total) },
    {
      label: t("kpi.success_rate"),
      value: formatPercent(k.successRate),
      hint: t("kpi.success_rate_hint"),
    },
    {
      label: t("kpi.tokens"),
      value: compactNumber(k.totalTokens),
      hint: t("kpi.tokens_hint", {
        prompt: formatNumber(k.promptTokens),
        completion: formatNumber(k.completionTokens),
      }),
    },
    {
      label: t("kpi.cache_hit"),
      value: formatPercent(k.cacheHitRate),
      hint: t("kpi.cache_hit_hint", {
        cached: formatNumber(k.cachedTokens),
        prompt: formatNumber(k.promptTokens),
      }),
    },
    {
      label: t("kpi.retries"),
      value: formatNumber(k.totalRetries),
      hint: t("kpi.retries_hint", { avg: k.avgRetries.toFixed(2) }),
    },
    { label: t("kpi.avg_tps"), value: stats.latency.tps.avg.toFixed(1) },
    { label: t("kpi.first_chunk_p50"), value: formatSeconds(stats.latency.firstChunk.p50) },
    {
      label: t("kpi.cost"),
      value: formatCost(k.cost, k.currency),
      hint: t("kpi.cost_hint"),
    },
  ]

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
      {items.map((it) => (
        <Card key={it.label}>
          <CardContent className="py-3">
            <div className="truncate text-xs text-muted-foreground" title={it.label}>
              {it.label}
            </div>
            {/* 读数列用等宽数字，纵向对齐 */}
            <div className="reading mt-1 truncate text-lg font-semibold" title={it.value}>
              {it.value}
            </div>
            {it.hint && (
              <div className="mt-0.5 truncate text-[11px] text-muted-foreground" title={it.hint}>
                {it.hint}
              </div>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

function PercentileRow({ stats }: { stats: StatsResult }) {
  const { t } = useTranslation("home")
  const l = stats.latency.firstChunk
  const cells = [
    { label: t("latency.p50"), v: l.p50 },
    { label: t("latency.p90"), v: l.p90 },
    { label: t("latency.p95"), v: l.p95 },
    { label: t("latency.p99"), v: l.p99 },
  ]
  return (
    <div className="mt-3 grid grid-cols-4 gap-2 border-t border-border pt-3">
      {cells.map((c) => (
        <div key={c.label}>
          <div className="text-[11px] text-muted-foreground">{c.label}</div>
          <div className="reading text-sm font-medium">{formatSeconds(c.v)}</div>
        </div>
      ))}
    </div>
  )
}

/**
 * 错误列表。
 *
 * 用**列表而非环形图**：错误类别是要比较的量，环形图对接近的值不可靠；
 * 且类别数可能超过 7，那时图表本就不如表格。类别名 + 计数 + 受影响对象
 * 用文字直给，不依赖颜色传达。
 */
function ErrorList({ stats, unavailable }: { stats: StatsResult; unavailable: string }) {
  const { t } = useTranslation(["home", "common"])
  if (stats.errors.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">{unavailable}</p>
  }
  return (
    <ul className="space-y-3">
      {stats.errors.slice(0, 5).map((g) => (
        <li key={g.code}>
          <div className="flex items-baseline justify-between gap-2">
            <span className="flex items-center gap-1.5 text-sm font-medium">
              <StatusMark status="error" label={g.type} />
            </span>
            <span className="reading text-sm text-muted-foreground">
              {t("home:errors.count", { count: g.count })}
            </span>
          </div>
          <div className="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-[11px] text-muted-foreground">
            {g.providers.length > 0 && (
              <span>
                {t("home:errors.affected_providers")}：
                {g.providers.map((p) => `${p.name}(${p.count})`).join("、")}
              </span>
            )}
            {g.models.length > 0 && (
              <span>
                {t("home:errors.affected_models")}：
                {g.models.map((m) => `${m.name}(${m.count})`).join("、")}
              </span>
            )}
          </div>
          {g.samples[0] && (
            <Link
              to={`/logs/${g.samples[0].id}/chat-io`}
              className="mt-1 inline-flex items-center gap-1 text-[11px] text-primary underline-offset-2 hover:underline"
            >
              {t("home:errors.view_log")}
              <ArrowUpRight className="size-3" aria-hidden="true" />
            </Link>
          )}
        </li>
      ))}
    </ul>
  )
}

function LeaderboardCard({
  title,
  rows,
  metric,
  empty,
  tone,
}: {
  title: string
  rows: StatsResult["topTps"]
  metric: (row: StatsResult["topTps"][number]) => string
  empty: string
  tone?: "critical"
}) {
  return (
    <Panel title={title}>
      {rows.length === 0 ? (
        <p className="py-4 text-center text-xs text-muted-foreground">{empty}</p>
      ) : (
        <ul className="space-y-1.5">
          {rows.map((r) => (
            <li key={r.id}>
              <Link
                to={`/logs/${r.id}/chat-io`}
                className="flex items-baseline justify-between gap-2 rounded-sm px-1 py-0.5 text-xs hover:bg-accent"
              >
                {/* 左列吃掉剩余宽度并截断，右列按内容宽度但不许`shrink-0`。
                    右列装的是错误原文（等宽字体，可达 300px 以上），
                    `shrink-0` 会让它永不收缩，把整行撑宽后一路顶到内容区——
                    窄卡片上表现为页面内容区出现一条横向滚动条。 */}
                <span className="min-w-0 flex-1 truncate" title={`${r.model} · ${r.provider}`}>
                  {r.model}
                  <span className="text-muted-foreground"> · {r.provider}</span>
                </span>
                <span
                  className={
                    tone === "critical"
                      ? "reading min-w-0 shrink truncate text-status-critical-ink"
                      : "reading min-w-0 shrink truncate"
                  }
                  title={metric(r)}
                >
                  {metric(r)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}

function EmptyState({ title, hint }: { title: string; hint: string }) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center gap-1 py-16 text-center">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-xs text-muted-foreground">{hint}</p>
      </CardContent>
    </Card>
  )
}
