import { useTranslation } from "react-i18next"
import { Link } from "react-router-dom"
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"
import { ArrowUpRight } from "lucide-react"

import { FirstChunkHistogram } from "@/components/charts/trend-charts"
import { Panel } from "@/components/panel"
import { StatusMark } from "@/components/status-mark"
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  DIMENSIONS,
  dimensionFilterValues,
  dimensionGroups,
  ERROR_BAR_LIMIT,
  errorBarBase,
  errorBarWidth,
  errorViewMode,
  logDetailPath,
  shortenLabel,
  type AnalyticsFilter,
  type DimensionKey,
} from "@/lib/analytics"
import {
  compactNumber,
  formatBucketLabel,
  formatDurationMs,
  formatNumber,
  formatPercent,
  formatSeconds,
} from "@/lib/format"
import type { ErrorGroup, StatsResult, StatsLogRow } from "@/lib/api"

// ---------------------------------------------------------------------------
// 下钻
// ---------------------------------------------------------------------------

/**
 * 五维下钻表。
 *
 * 五个维度共用一张表：列定义、排序、空值处理对五者完全相同，差别只有
 * "取哪个数组"（见 lib 里的 `dimensionGroups`）。写成一份而不是五份，
 * 是为了让"加一个维度"和"改一列"都只发生在一个地方。
 *
 * 点行首的名称即把该值加进筛选——这就是"下钻"的字面含义，也是这一页
 * 从聚合数字走到具体请求的入口。再次点击取消。
 */
export function BreakdownView({
  stats,
  dimension,
  onDimension,
  filter,
  onToggleValue,
}: {
  stats: StatsResult
  dimension: DimensionKey
  onDimension: (d: DimensionKey) => void
  filter: AnalyticsFilter
  onToggleValue: (dim: DimensionKey, value: string) => void
}) {
  const { t } = useTranslation("analytics")
  const groups = dimensionGroups(stats, dimension)
  const selected = dimensionFilterValues(filter, dimension)

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ToggleGroup
          type="single"
          variant="outline"
          value={dimension}
          onValueChange={(v) => {
            if (v) onDimension(v as DimensionKey)
          }}
          aria-label={t("breakdown.dimension_label")}
          // 同上：五个维度在 280px 上比容器宽，不换行会被外壳裁掉
          className="flex-wrap"
        >
          {DIMENSIONS.map((d) => (
            <ToggleGroupItem key={d} value={d} size="sm" className="px-2">
              {t(`analytics:breakdown.dimensions.${d}` as never)}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <span className="text-[11px] text-muted-foreground">{t("breakdown.row_hint")}</span>
      </div>

      <Panel title={t("breakdown.dimensions." + dimension as never)}>
        {groups.length === 0 ? (
          <p className="py-8 text-center text-sm text-muted-foreground">
            {t("breakdown.empty")}
          </p>
        ) : (
          // 十列在窄屏上放不下，横向滚动而不是砍列：砍掉的列往往是
          // 用户正在找的那一列，而横向滚动至少保证信息都在
          <div className="overflow-x-auto">
            <Table className="min-w-[880px]">
              <TableHeader>
                <TableRow>
                  <TableHead>{t("breakdown.columns.name")}</TableHead>
                  <TableHead className="text-right">{t("breakdown.columns.total")}</TableHead>
                  <TableHead className="text-right">
                    {t("breakdown.columns.success_rate")}
                  </TableHead>
                  <TableHead className="text-right">{t("breakdown.columns.avg_tps")}</TableHead>
                  <TableHead className="text-right">{t("breakdown.columns.max_tps")}</TableHead>
                  <TableHead className="text-right">
                    {t("breakdown.columns.avg_first_chunk")}
                  </TableHead>
                  <TableHead className="text-right">
                    {t("breakdown.columns.p95_first_chunk")}
                  </TableHead>
                  <TableHead className="text-right">
                    {t("breakdown.columns.cache_hit")}
                  </TableHead>
                  <TableHead className="text-right">{t("breakdown.columns.retries")}</TableHead>
                  <TableHead className="text-right">
                    {t("breakdown.columns.avg_proxy")}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groups.map((g) => {
                  const isSelected = selected.includes(g.name)
                  const shown = dimension === "ua" ? shortenLabel(g.name) : g.name
                  return (
                    <TableRow key={g.name}>
                      <TableCell className="max-w-[220px]">
                        <button
                          type="button"
                          onClick={() => onToggleValue(dimension, g.name)}
                          aria-pressed={isSelected}
                          title={t(
                            isSelected ? "breakdown.filter_off" : "breakdown.filter_on",
                            { value: g.name }
                          )}
                          className={
                            isSelected
                              ? "block w-full truncate rounded-sm bg-primary px-1.5 py-0.5 text-left text-primary-foreground"
                              : "block w-full truncate rounded-sm px-1.5 py-0.5 text-left hover:bg-accent"
                          }
                        >
                          {shown}
                        </button>
                      </TableCell>
                      <TableCell className="reading text-right">{formatNumber(g.total)}</TableCell>
                      <TableCell className="reading text-right">
                        {formatPercent(g.successRate)}
                      </TableCell>
                      <TableCell className="reading text-right">{g.avgTps.toFixed(1)}</TableCell>
                      <TableCell className="reading text-right">{g.maxTps.toFixed(1)}</TableCell>
                      <TableCell className="reading text-right">
                        {formatDurationMs(g.avgFirstChunkMs)}
                      </TableCell>
                      <TableCell className="reading text-right">
                        {formatDurationMs(g.p95FirstChunkMs)}
                      </TableCell>
                      {/* 缓存命中率的分母是 prompt（后端口径），因此列名用"缓存命中"
                          而不是"缓存率"——它说的是输入的哪一部分来自缓存 */}
                      <TableCell className="reading text-right">
                        {formatPercent(g.cacheHitRate)}
                      </TableCell>
                      <TableCell className="reading text-right">{formatNumber(g.retries)}</TableCell>
                      <TableCell className="reading text-right">
                        {formatDurationMs(g.avgProxyMs)}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </Panel>
    </div>
  )
}

// ---------------------------------------------------------------------------
// 延迟
// ---------------------------------------------------------------------------

/**
 * 延迟视图。
 *
 * 分位数用**最近秩**定义（后端 `Percentile`），不是插值——两者在样本少时
 * 可以差出一整个档位。这不是可以省略的细节，因此写在面板说明里而不是注释里。
 *
 * TPS 与代理耗时只给读数不给图：样本量小的时候分布形状本身没有意义，
 * 而这两个量的"典型值 + 尾部"用四个数字就能说清。
 */
export function LatencyView({ stats }: { stats: StatsResult }) {
  const { t } = useTranslation("analytics")
  const fc = stats.latency.firstChunk

  const percentiles: { label: string; value: string }[] = [
    { label: t("latency.p50"), value: formatSeconds(fc.p50) },
    { label: t("latency.p90"), value: formatSeconds(fc.p90) },
    { label: t("latency.p95"), value: formatSeconds(fc.p95) },
    { label: t("latency.p99"), value: formatSeconds(fc.p99) },
    { label: t("latency.avg"), value: formatSeconds(fc.avg) },
    { label: t("latency.max"), value: formatSeconds(fc.max) },
  ]

  return (
    <div className="flex flex-col gap-3">
      <Panel
        title={t("latency.distribution")}
        note={`${t("latency.samples", { count: fc.list.length })} · ${t(
          "latency.histogram_note"
        )} · ${t("latency.percentile_note")}`}
      >
        <FirstChunkHistogram samples={fc.list} />
        <div className="mt-3 grid grid-cols-3 gap-2 border-t border-border pt-3 sm:grid-cols-6">
          {percentiles.map((p) => (
            <div key={p.label}>
              <div className="text-[11px] text-muted-foreground">{p.label}</div>
              <div className="reading text-sm font-medium">{p.value}</div>
            </div>
          ))}
        </div>
      </Panel>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Panel title={t("latency.tps_title")}>
          <StatRow
            items={[
              { label: t("latency.avg"), value: stats.latency.tps.avg.toFixed(1) },
              { label: t("latency.p50"), value: stats.latency.tps.p50.toFixed(1) },
              { label: t("latency.p95"), value: stats.latency.tps.p95.toFixed(1) },
              { label: t("latency.max"), value: stats.latency.tps.max.toFixed(1) },
            ]}
            unit={t("latency.tps_unit")}
          />
        </Panel>

        <Panel title={t("latency.proxy_title")}>
          <StatRow
            items={[
              {
                label: t("latency.avg"),
                value: formatDurationMs(stats.latency.proxyMs.avg),
              },
              {
                label: t("latency.p95"),
                value: formatDurationMs(stats.latency.proxyMs.p95),
              },
            ]}
          />
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Board
          title={t("latency.tps_board")}
          rows={stats.topTps}
          metric={(r) => `${r.tps.toFixed(1)} ${t("latency.tps_unit")}`}
        />
        <Board
          title={t("latency.slowest_board")}
          rows={stats.slowest}
          metric={(r) => formatSeconds(r.firstChunkMs / 1000)}
        />
      </div>
    </div>
  )
}

function StatRow({
  items,
  unit,
}: {
  items: { label: string; value: string }[]
  unit?: string
}) {
  return (
    <div>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {items.map((it) => (
          <div key={it.label}>
            <div className="text-[11px] text-muted-foreground">{it.label}</div>
            <div className="reading text-sm font-medium">{it.value}</div>
          </div>
        ))}
      </div>
      {unit && <p className="mt-2 text-[11px] text-muted-foreground">{unit}</p>}
    </div>
  )
}

/**
 * 单条请求榜。
 *
 * 与总览页的同名榜同形，但数据随筛选走——筛选之后"最慢的请求"是
 * 筛选后切片里的最慢，而不是全局最慢，这正是分析页需要的语义。
 */
export function Board({
  title,
  rows,
  metric,
}: {
  title: string
  rows: StatsLogRow[]
  metric: (row: StatsLogRow) => string
}) {
  const { t } = useTranslation("analytics")
  return (
    <Panel title={title}>
      {rows.length === 0 ? (
        <p className="py-4 text-center text-xs text-muted-foreground">
          {t("latency.board_empty")}
        </p>
      ) : (
        <ul className="space-y-1.5">
          {rows.map((r) => (
            <li key={r.id}>
              <Link
                to={logDetailPath(r.id)}
                className="flex items-baseline justify-between gap-2 rounded-sm px-1 py-0.5 text-xs hover:bg-accent"
              >
                <span className="min-w-0 flex-1 truncate" title={`${r.model} · ${r.provider}`}>
                  {r.model}
                  <span className="text-muted-foreground"> · {r.provider}</span>
                </span>
                <span className="reading min-w-0 shrink truncate" title={metric(r)}>
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

// ---------------------------------------------------------------------------
// 错误
// ---------------------------------------------------------------------------

/**
 * 错误视图。
 *
 * 呈现方式按类别数切换：≤7 类用条形，>7 类改用表格（`errorViewMode`）。
 * 依据是分类色板只有 8 槽——第 9 个色相在色盲模拟下与已有槽位无法区分，
 * 而"折叠成其他"会把用户最需要看的少数大类淹掉。
 *
 * 条形只承载"比较"，数值、类别名、受影响范围一律用文字直给：
 * 灰色的条形对读屏用户等于不存在。
 */
export function ErrorsView({ stats }: { stats: StatsResult }) {
  const { t } = useTranslation("analytics")
  const groups = stats.errors
  const failedTotal = stats.kpi.failed

  if (groups.length === 0) {
    return (
      <Panel title={t("errors.title")}>
        <p className="py-8 text-center text-sm text-muted-foreground">
          {t("errors.empty")}
        </p>
      </Panel>
    )
  }

  const mode = errorViewMode(groups.length)
  const base = errorBarBase(groups)

  return (
    <div className="flex flex-col gap-3">
      <Panel title={t("errors.trend_title")}>
        <ErrorTrendChart data={stats.errorTrend} />
      </Panel>

      <Panel
        title={t("errors.title")}
        note={mode === "table" ? t("errors.table_note", { limit: ERROR_BAR_LIMIT }) : undefined}
      >
        {mode === "bars" ? (
          <ul className="space-y-3">
            {groups.map((g) => (
              <li key={g.code}>
                <div className="relative h-7 overflow-hidden rounded-sm bg-muted/50">
                  <div
                    className="absolute inset-y-0 left-0 bg-primary/25"
                    style={{ width: `${errorBarWidth(g.count, base)}%` }}
                    aria-hidden="true"
                  />
                  <div className="relative flex h-full items-center gap-2 px-2 text-xs">
                    <StatusMark status="error" label={g.type} />
                    <span className="reading ml-auto shrink-0">
                      {t("errors.count", { count: g.count })}
                    </span>
                  </div>
                </div>
                <ErrorScope group={g} failedTotal={failedTotal} />
              </li>
            ))}
          </ul>
        ) : (
          <div className="overflow-x-auto">
            <Table className="min-w-[720px]">
              <TableHeader>
                <TableRow>
                  <TableHead>{t("errors.columns.type")}</TableHead>
                  <TableHead className="text-right">{t("errors.columns.count")}</TableHead>
                  <TableHead className="text-right">{t("errors.columns.share")}</TableHead>
                  <TableHead>{t("errors.columns.providers")}</TableHead>
                  <TableHead>{t("errors.columns.models")}</TableHead>
                  <TableHead>{t("errors.columns.sample")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groups.map((g) => (
                  <TableRow key={g.code}>
                    <TableCell>
                      <StatusMark status="error" label={g.type} />
                    </TableCell>
                    <TableCell className="reading text-right">
                      {formatNumber(g.count)}
                    </TableCell>
                    <TableCell className="reading text-right">
                      {sharePercent(g.count, failedTotal)}
                    </TableCell>
                    <TableCell className="max-w-[180px] truncate">
                      {countList(g.providers)}
                    </TableCell>
                    <TableCell className="max-w-[180px] truncate">{countList(g.models)}</TableCell>
                    <TableCell>
                      <SampleLink group={g} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </Panel>
    </div>
  )
}

/** 失败时间序列。单序列不需要图例——标题已经说明了它是什么。 */
function ErrorTrendChart({ data }: { data: StatsResult["errorTrend"] }) {
  const { t } = useTranslation("analytics")
  const bucketMs = data.length > 1 ? data[1].ts - data[0].ts : 60 * 60 * 1000
  const config = { error: { label: t("errors.columns.count") } } satisfies ChartConfig

  return (
    <ChartContainer config={config} className="aspect-auto h-[180px] w-full">
      <BarChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" strokeWidth={1} />
        <XAxis
          dataKey="ts"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={24}
          tickFormatter={(v: number) => formatBucketLabel(v, bucketMs)}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={36}
          allowDecimals={false}
          tickFormatter={(v: number) => compactNumber(v)}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(v) => formatBucketLabel(Number(v), bucketMs)}
            />
          }
        />
        <Bar dataKey="error" fill="var(--series-8)" maxBarSize={24} radius={[4, 4, 0, 0]} />
      </BarChart>
    </ChartContainer>
  )
}

/** 类别 → 受影响范围 + 样本入口。条形与表格两种模式共用。 */
function ErrorScope({ group, failedTotal }: { group: ErrorGroup; failedTotal: number }) {
  const { t } = useTranslation("analytics")
  return (
    <div className="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-[11px] text-muted-foreground">
      <span className="reading">{t("errors.share_hint", { percent: sharePercent(group.count, failedTotal) })}</span>
      {group.providers.length > 0 && (
        <span>
          {t("errors.affected_providers")}：{countList(group.providers)}
        </span>
      )}
      {group.models.length > 0 && (
        <span>
          {t("errors.affected_models")}：{countList(group.models)}
        </span>
      )}
      <SampleLink group={group} />
    </div>
  )
}

function SampleLink({ group }: { group: ErrorGroup }) {
  const { t } = useTranslation("analytics")
  const sample = group.samples[0]
  if (!sample) return null
  return (
    <Link
      to={logDetailPath(sample.id)}
      className="inline-flex items-center gap-1 text-primary underline-offset-2 hover:underline"
      title={sample.error}
    >
      {t("errors.view_log")}
      <ArrowUpRight className="size-3" aria-hidden="true" />
    </Link>
  )
}

/** "p1(3)、p2(1)"。空列表返回空串而不是"—"：调用方已按长度判断是否渲染。 */
function countList(items: { name: string; count: number }[]): string {
  return items.map((i) => `${i.name}(${i.count})`).join("、")
}

/** 占失败总数的百分比。总数为 0 时给 0 而不是 NaN。 */
function sharePercent(count: number, total: number): string {
  if (total <= 0) return formatPercent(0)
  return formatPercent((count / total) * 100)
}
