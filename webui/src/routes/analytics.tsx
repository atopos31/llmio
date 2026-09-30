import { useCallback, useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, RefreshCw } from "lucide-react"
import { toast } from "sonner"

import { RequestTrendChart, TokenTrendChart } from "@/components/charts/trend-charts"
import { Panel } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { FilterRow, type FilterOption, type FilterOptions, type RangeKey } from "@/routes/analytics-filters"
import { BreakdownView, ErrorsView, LatencyView } from "@/routes/analytics-views"
import {
  activeFilterCount,
  buildStatsQuery,
  EMPTY_FILTER,
  keyFilterOptions,
  observedOptions,
  presetRange,
  STATUS_VALUES,
  toggleDimensionValue,
  toggleValue,
  VIEWS,
  type AnalyticsFilter,
  type AnalyticsView,
  type DimensionKey,
} from "@/lib/analytics"
import {
  getAuthKeysList,
  getStats,
  getStatsGranularities,
  type AuthKeyItem,
  type StatsResult,
} from "@/lib/api"
import { compactNumber, formatNumber, formatPercent } from "@/lib/format"

/**
 * 分析页。
 *
 * 结构上只有两层：**一行筛选，其下全部受它影响**。视图用分段控件切换而不是
 * 并列堆卡——并列堆卡会让人靠滚动找人，而且每张卡看起来都同样重要。
 *
 * 一次筛选 = 一个请求（`/api/metrics/stats` 是单端点、返回整份切片），
 * 因此各视图之间的数字必然吻合，不会出现"趋势图说 100 次、下钻表说 98 次"
 * 这种由多次请求的时间窗漂移造成的假矛盾。
 */
export default function AnalyticsPage() {
  const { t } = useTranslation(["analytics", "common"])

  const [preset, setPreset] = useState<RangeKey>("last_24h")
  const [granularity, setGranularity] = useState("auto")
  const [granularities, setGranularities] = useState<string[]>([])
  const [filter, setFilter] = useState<AnalyticsFilter>(EMPTY_FILTER)
  const [view, setView] = useState<AnalyticsView>("trend")
  const [dimension, setDimension] = useState<DimensionKey>("model")

  const [stats, setStats] = useState<StatsResult | null>(null)
  /**
   * 未加筛选的同一时间窗结果，仅用于生成筛选选项。
   *
   * 必须单独取一份：若选项从**筛选后**的结果里派生，筛了供应商 A 之后
   * 其余供应商就从下拉里消失了，用户再也切不到 B——只能先清除筛选。
   * 仅在确实有筛选时多发这一次请求。
   */
  const [universe, setUniverse] = useState<StatsResult | null>(null)
  const [authKeys, setAuthKeys] = useState<AuthKeyItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const hasFilter = activeFilterCount(filter) > 0

  const load = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true)
      try {
        const { from, to } = presetRange(preset, new Date())
        const rangeOnly = buildStatsQuery({ from, to, granularity, filter: EMPTY_FILTER })
        const filtered = buildStatsQuery({ from, to, granularity, filter })
        const [data, base] = hasFilter
          ? await Promise.all([getStats(filtered), getStats(rangeOnly)])
          : [await getStats(filtered), null]
        setStats(data)
        setUniverse(base)
        setError(null)
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
        toast.error(t("analytics:load_failed", { message }))
      } finally {
        setLoading(false)
      }
    },
    [preset, granularity, filter, hasFilter, t]
  )

  useEffect(() => {
    void load()
  }, [load])

  // 档位列表来自服务端（`service.BucketGranularities` 是档位的唯一真相来源）。
  // 拿不到时**不退回本地写死的一份**——那会造出第二份真相来源，在服务端改档位后
  // 前端会安静地继续提供已经不存在的档位。此时控件不显示，auto 仍然可用。
  useEffect(() => {
    let active = true
    getStatsGranularities()
      .then((list) => {
        if (active) setGranularities(list ?? [])
      })
      .catch(() => {
        if (active) setGranularities([])
      })
    return () => {
      active = false
    }
  }, [])

  // 密钥筛选传的是 id，而分组结果里只有展示名，因此需要一次名字→id 的对齐。
  // 拿不到就不提供选项（见 lib 里 keyFilterOptions 的说明）。
  useEffect(() => {
    let active = true
    getAuthKeysList()
      .then((list) => {
        if (active) setAuthKeys(list ?? [])
      })
      .catch(() => {
        if (active) setAuthKeys([])
      })
    return () => {
      active = false
    }
  }, [])

  const options: FilterOptions = useMemo(() => {
    const src = universe ?? stats
    const opts = (values: string[]): FilterOption[] =>
      values.map((v) => ({ value: v, label: v }))
    return {
      provider: opts(src ? observedOptions(src.byProvider) : []),
      model: opts(src ? observedOptions(src.byModel) : []),
      key: src ? keyFilterOptions(src.byKey, authKeys) : [],
      name: opts(src ? observedOptions(src.byName) : []),
      ua: opts(src ? observedOptions(src.byUa) : []),
      status: STATUS_VALUES.map((s) => ({ value: s, label: t(`common:status.${s}` as never) })),
    }
  }, [universe, stats, authKeys, t])

  const handleToggle = useCallback(
    (field: keyof AnalyticsFilter, value: string) => {
      setFilter((f) => ({ ...f, [field]: toggleValue(f[field], value) }))
    },
    []
  )

  const handleDrill = useCallback((dim: DimensionKey, value: string) => {
    setFilter((f) => toggleDimensionValue(f, dim, value))
  }, [])

  const hasData = (stats?.kpi.total ?? 0) > 0

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-1">
      <FilterRow
        preset={preset}
        onPreset={setPreset}
        granularities={granularities}
        granularity={granularity}
        onGranularity={setGranularity}
        filter={filter}
        options={options}
        onToggle={handleToggle}
        onClear={() => setFilter(EMPTY_FILTER)}
        onRefresh={() => void load()}
        loading={loading}
      />

      {stats?.truncated && (
        <div className="flex items-start gap-2 rounded-md border border-border bg-accent/40 px-3 py-2 text-sm">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-status-warning-ink" aria-hidden="true" />
          <span>{t("analytics:truncated_warning")}</span>
        </div>
      )}

      {/* 视图切换器在内容之上、筛选行之下：它换的是"看哪一面"，
          不改变切片，因此不属于筛选行 */}
      {hasData && !error && (
        <ToggleGroup
          type="single"
          variant="outline"
          value={view}
          onValueChange={(v) => {
            if (v) setView(v as AnalyticsView)
          }}
          aria-label={t("analytics:views.label")}
          // 四项在窄屏上等分整行，因此这里要允许换行：等分（flex-1）会把
          // 每项压到文字的 min-content 之下，只有换行才收得住
          className="w-full flex-wrap sm:w-fit"
        >
          {VIEWS.map((v) => (
            <ToggleGroupItem key={v} value={v} size="sm" className="flex-1 sm:flex-none">
              {t(`analytics:views.${v}` as never)}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      )}

      {/* 页面自己拥有滚动区：外壳不再滚（见 index.css 的说明），
          否则右边缘会出现两条并排的滚动条 */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {error ? (
          <ErrorState
            title={t("analytics:empty.error_title")}
            message={error}
            retryLabel={t("analytics:retry")}
            onRetry={() => void load()}
          />
        ) : loading && !stats ? (
          // 骨架对读屏用户等于空白，因此这里显式声明"正在加载"：
          // 否则加载态与"没有数据"在无障碍树上无法区分
          <div className="space-y-3" role="status" aria-busy="true">
            <span className="sr-only">{t("analytics:loading")}</span>
            <div className="h-16 animate-pulse rounded-lg bg-muted" />
            <Skeleton className="h-56 w-full" />
          </div>
        ) : !hasData ? (
          <EmptyState title={t("analytics:empty.title")} hint={t("analytics:empty.hint")} />
        ) : (
          <div className="space-y-3 pb-1">
            {view === "trend" && <TrendView stats={stats!} />}
            {view === "breakdown" && (
              <BreakdownView
                stats={stats!}
                dimension={dimension}
                onDimension={setDimension}
                filter={filter}
                onToggleValue={handleDrill}
              />
            )}
            {view === "latency" && <LatencyView stats={stats!} />}
            {view === "errors" && <ErrorsView stats={stats!} />}
          </div>
        )}
      </div>
    </div>
  )
}

/**
 * 趋势视图。
 *
 * 两张图而不是一张双轴图：请求数与 Token 量级差着几个数量级，双轴会把
 * 缩放对齐关系变成任意的，从而凭空造出数据里不存在的相关性。
 *
 * 图与读数并列而不是只给图：读者问的第一个问题往往是"一共多少"，
 * 而这个数字在图里读不出来（要加起来）。
 */
function TrendView({ stats }: { stats: StatsResult }) {
  const { t } = useTranslation("analytics")
  const k = stats.kpi
  return (
    <>
      <Panel
        title={t("trend.requests")}
        note={t("trend.requests_note", {
          total: formatNumber(k.total),
          success: formatNumber(k.success),
          error: formatNumber(k.failed),
          running: formatNumber(k.running),
        })}
      >
        <RequestTrendChart data={stats.trend} />
      </Panel>

      {/* 缓存命中率写在 Token 图上而不是 KPI 行里：它的分母是输入 Token，
          挨着输入/输出读数才读得懂 */}
      <Panel
        title={t("trend.tokens")}
        note={`${t("trend.tokens_note", {
          prompt: compactNumber(k.promptTokens),
          completion: compactNumber(k.completionTokens),
          cached: compactNumber(k.cachedTokens),
        })} · ${formatPercent(k.cacheHitRate)}`}
      >
        <TokenTrendChart data={stats.trend} />
      </Panel>
    </>
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

/** 失败态。给出**原文**而不是"加载失败"四个字：原文才可能指向原因。 */
function ErrorState({
  title,
  message,
  retryLabel,
  onRetry,
}: {
  title: string
  message: string
  retryLabel: string
  onRetry: () => void
}) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center gap-2 py-12 text-center">
        <AlertTriangle className="size-5 text-status-critical-ink" aria-hidden="true" />
        <p className="text-sm font-medium">{title}</p>
        <p className="reading max-w-full break-words text-xs text-muted-foreground">{message}</p>
        <Button variant="outline" size="sm" className="mt-1 gap-1.5" onClick={onRetry}>
          <RefreshCw className="size-3.5" aria-hidden="true" />
          {retryLabel}
        </Button>
      </CardContent>
    </Card>
  )
}
