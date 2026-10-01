import { useCallback, useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle } from "lucide-react"
import { toast } from "sonner"

import { RequestTrendChart, TokenTrendChart } from "@/components/charts/trend-charts"
import { Panel } from "@/components/panel"
import { EmptyState, ErrorState } from "@/components/state-views"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { FilterRow, type FilterOption, type FilterOptions, type RangeKey } from "@/routes/analytics-filters"
import { BreakdownView, ErrorsView, LatencyView, ModelsView } from "@/routes/analytics-views"
import {
  activeFilterCount,
  buildStatsQuery,
  customRangeError,
  DEFAULT_MODEL_SORT,
  EMPTY_FILTER,
  fromLocalInput,
  keyFilterOptions,
  nextModelSort,
  observedOptions,
  presetRange,
  STATUS_VALUES,
  toLocalInput,
  toggleDimensionValue,
  toggleValue,
  VIEWS,
  type AnalyticsFilter,
  type AnalyticsView,
  type DimensionKey,
  type ModelDimension,
  type ModelSort,
  type ModelSortKey,
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
  /**
   * 自定义范围的起止，`datetime-local` 的本地时间字符串。
   *
   * 与 `preset` 分开存：切回预设再切回自定义时，用户刚填的两端不该丢。
   */
  const [customFrom, setCustomFrom] = useState("")
  const [customTo, setCustomTo] = useState("")
  const [granularity, setGranularity] = useState("auto")
  const [granularities, setGranularities] = useState<string[]>([])
  const [filter, setFilter] = useState<AnalyticsFilter>(EMPTY_FILTER)
  const [view, setView] = useState<AnalyticsView>("trend")
  const [dimension, setDimension] = useState<DimensionKey>("model")
  /**
   * 模型性能表的排序。与 `view` / `dimension` 一样只存在组件内，不进 URL：
   * 这一页的既有做法就是如此（筛选、时间范围也都不进 URL），排序状态再单独
   * 走一套 URL 同步会造出第二种状态机制。代价是刷新后回到默认排序，可接受。
   */
  const [modelSort, setModelSort] = useState<ModelSort>(DEFAULT_MODEL_SORT)
  /**
   * 模型性能表按哪个维度摆行。默认仍是"模型"——与改动前一致，
   * 打开这一页看到的第一眼不该变。
   */
  const [modelDimension, setModelDimension] = useState<ModelDimension>("model")

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
  const customError = preset === "custom" ? customRangeError(customFrom, customTo) : null

  const load = useCallback(
    async (silent = false) => {
      // 自定义范围还没填完或前后颠倒时**不发请求、也不清空**：正在编辑的半截
      // 输入不该把已有视图打成空白。控件旁边已经写明为什么。
      // 每次取数时才读 now：刷新不会沿用上一次的右端
      const span =
        preset === "custom" ? customSpan(customFrom, customTo) : presetRange(preset, new Date())
      if (!span) {
        setLoading(false)
        return
      }

      if (!silent) setLoading(true)
      try {
        const rangeOnly = buildStatsQuery({ ...span, granularity, filter: EMPTY_FILTER })
        const filtered = buildStatsQuery({ ...span, granularity, filter })
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
    [preset, customFrom, customTo, granularity, filter, hasFilter, t]
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

  /** 点表头：同列翻转方向，换列从降序起步（判定在 lib 里，可单测）。 */
  const handleModelSort = useCallback((key: ModelSortKey) => {
    setModelSort((s) => nextModelSort(s, key))
  }, [])

  /**
   * 切到自定义时用**当前预设的窗口**预填两端。
   *
   * 不预填（留两个空框）会让人先面对一个空状态再从头填；预填之后往往只需要
   * 改一端。两头都空时给"近 24 小时"当起点。
   */
  const handlePreset = useCallback(
    (p: RangeKey) => {
      if (p === "custom" && preset !== "custom") {
        // 这里的 preset 已被窄化掉 custom，因此直接当预填的起点
        const r = presetRange(preset, new Date())
        setCustomFrom(toLocalInput(r.from))
        setCustomTo(toLocalInput(r.to))
      }
      setPreset(p)
    },
    [preset]
  )

  const hasData = (stats?.kpi.total ?? 0) > 0

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-1">
      <FilterRow
        preset={preset}
        onPreset={handlePreset}
        granularities={granularities}
        granularity={granularity}
        onGranularity={setGranularity}
        customFrom={customFrom}
        customTo={customTo}
        customError={customError}
        onCustomFrom={setCustomFrom}
        onCustomTo={setCustomTo}
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
            {view === "models" && (
              <ModelsView
                stats={stats!}
                sort={modelSort}
                onSort={handleModelSort}
                dimension={modelDimension}
                onDimension={setModelDimension}
              />
            )}
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

/** 自定义范围 → 查询用的 unix 秒。不可用时返回 null（合法性只由 `customRangeError` 判定）。 */
function customSpan(from: string, to: string): { from: number; to: number } | null {
  if (customRangeError(from, to)) return null
  const a = fromLocalInput(from)
  const b = fromLocalInput(to)
  if (a === null || b === null) return null // 已由 customRangeError 排除，这里只为收窄类型
  return { from: a, to: b }
}

