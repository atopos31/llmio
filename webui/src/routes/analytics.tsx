import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, Loader2 } from "lucide-react"
import { toast } from "sonner"

import { CostTrendChart, RequestTrendChart, TokenTrendChart } from "@/components/charts/trend-charts"
import { Panel } from "@/components/panel"
import { EmptyState, ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
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
import { compactNumber, formatCost, formatNumber, formatPercent } from "@/lib/format"

/**
 * 判断一个错误是不是"请求被主动取消"。
 *
 * fetch 被 AbortController 取消时抛的是 `AbortError`。不同运行时给的类型不一样
 * （浏览器是 DOMException，测试环境里往往只是个带 name 的对象），所以只认 name，
 * 不认构造器。
 */
/** 取数的三个可见阶段：首屏（无数据）、刷新（有旧数据）、空闲。 */
type LoadPhase = "loading" | "refreshing" | "idle"

/** 结果缓存有效期。慢库上"刚才看过的那一屏"不该因为切走再切回就消失。 */
const cacheTTL = 60_000

/** 超过这个秒数才提示"可以先缩小范围"——短请求闪一下提示反而更烦。 */
const slowHintAfterSec = 10

/**
 * 进度条 / 已用时间 / 取消这一组"长时加载"信息，延迟这么久才出现。
 *
 * 绝大多数取数是几百毫秒就回来的：立刻显示再立刻消失，只会让页面闪一下，
 * 反而像出了故障。先给 2 秒宽限，真正慢的请求再显示这些。
 */
const busyDelayMs = 2000

function isAbortError(err: unknown): boolean {
  return (
    typeof err === "object" && err !== null && (err as { name?: string }).name === "AbortError"
  )
}

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
  /**
   * 取数的三个阶段。
   *
   * `refreshing` 与 `loading` 分开，是因为它们的**界面表现完全不同**：首屏没有
   * 数据可看，只能给骨架和进度；刷新时旧数据还在，就不该把整页打成空白。
   * 慢库上一次要几十秒，这个区别是"能用"和"看着像卡死"的区别。
   */
  const [phase, setPhase] = useState<LoadPhase>("loading")
  /** 本次取数的起点，用来算已用时间；null 表示没在取数 */
  const [startedAt, setStartedAt] = useState<number | null>(null)
  const [elapsedSec, setElapsedSec] = useState(0)
  /**
   * 用户主动取消了取数。
   *
   * 必须与"查了但没有数据"区分开：取消之后数据压根没取回来，此时说
   * 「所选范围内没有请求」是在撒谎，还会把人引去改时间范围/清筛选——
   * 而真正该做的是重新加载。
   */
  const [canceled, setCanceled] = useState(false)
  /**
   * 这次取数是否已经慢到值得显示"长时加载"那一组信息（进度条/已用时间/取消）。
   *
   * 2 秒内的加载什么都不显示：不闪进度条、也不闪"已用 0 秒"。
   */
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const hasFilter = activeFilterCount(filter) > 0
  const customError = preset === "custom" ? customRangeError(customFrom, customTo) : null

  /**
   * 已用时间只在取数期间跳动。
   *
   * 慢窗口下"转圈"不提供任何信息，而"已用 12 秒"至少让人知道它还在动、
   * 大概还要一会儿。
   */
  useEffect(() => {
    if (phase === "idle") return
    const t0 = startedAt ?? Date.now()
    setElapsedSec(Math.floor((Date.now() - t0) / 1000))
    const timer = window.setInterval(() => {
      setElapsedSec(Math.floor((Date.now() - t0) / 1000))
    }, 1000)
    return () => window.clearInterval(timer)
  }, [phase, startedAt])

  // 2 秒宽限：绝大多数取数几百毫秒就回来，那一组信息闪一下只会让人以为出了故障
  useEffect(() => {
    if (phase === "idle") {
      setBusy(false)
      return
    }
    const timer = window.setTimeout(() => setBusy(true), busyDelayMs)
    return () => window.clearTimeout(timer)
  }, [phase, startedAt])

  /**
   * 取数用的翻译函数走 ref，不进 `load` 的依赖。
   *
   * `t` 的身份由 i18n 决定，不是"用户换了筛选"。把它写进依赖里，任何一次
   * 语言/命名空间重载都会让 `load` 换新身份、effect 跟着重跑——在慢库上就是
   * 凭空多出一次几十秒的统计请求。历史上那两条相隔 3 秒、参数几乎相同的
   * `/api/metrics/stats` 就是这么来的。
   */
  const tRef = useRef(t)
  tRef.current = t

  /**
   * 在途请求。新的取数一开始就掐掉上一笔。
   *
   * 分析页在慢库上一次要几十秒：不掐的话，用户改完筛选后旧响应会盖掉新结果，
   * 数字看起来"闪一下又变回去"；而且两个几十秒的读事务叠在一起还会一起拖住
   * 写入（回滚日志模式读挡写）。
   */
  const inflight = useRef<AbortController | null>(null)

  /**
   * 结果缓存（stale-while-revalidate）。
   *
   * 键取的是**用户意图**（范围档位 / 自定义两端 / 分桶档位 / 筛选），刻意不带
   * `to=now` 算出来的时间戳——带上它每次键都不一样，缓存永远不会命中。
   * 切回刚看过的范围时先出缓存、再后台刷新：慢库上一次几十秒，这一下能省掉
   * 一次干等。
   */
  const cache = useRef(
    new Map<string, { stats: StatsResult; universe: StatsResult | null; at: number }>()
  )
  /** 有无数据不能进 load 的依赖（否则每次取数回来就重建 load、effect 再跑一遍） */
  const hasDataRef = useRef(false)
  hasDataRef.current = stats !== null

  const cancel = useCallback(() => {
    inflight.current?.abort()
    inflight.current = null
    setPhase("idle")
    setStartedAt(null)
    setCanceled(true)
  }, [])

  const load = useCallback(
    async (silent = false) => {
      // 自定义范围还没填完或前后颠倒时**不发请求、也不清空**：正在编辑的半截
      // 输入不该把已有视图打成空白。控件旁边已经写明为什么。
      // 每次取数时才读 now：刷新不会沿用上一次的右端
      const span =
        preset === "custom" ? customSpan(customFrom, customTo) : presetRange(preset, new Date())
      if (!span) {
        setPhase("idle")
        setStartedAt(null)
        return
      }

      inflight.current?.abort()
      const ac = new AbortController()
      inflight.current = ac
      setCanceled(false)

      // 命中缓存就先上屏，再后台刷新——"看得见旧数据"比"骨架转圈"好得多
      const key = `${preset}|${customFrom}|${customTo}|${granularity}|${JSON.stringify(filter)}`
      const hit = cache.current.get(key)
      if (hit && Date.now() - hit.at < cacheTTL) {
        setStats(hit.stats)
        setUniverse(hit.universe)
      }

      if (!silent) {
        // 有东西看的时候只做"更新中"，别把整页换成骨架
        setPhase(hasDataRef.current || hit ? "refreshing" : "loading")
        setStartedAt(Date.now())
      }
      try {
        const rangeOnly = buildStatsQuery({ ...span, granularity, filter: EMPTY_FILTER })
        const filtered = buildStatsQuery({ ...span, granularity, filter })
        const [data, base] = hasFilter
          ? await Promise.all([getStats(filtered, ac.signal), getStats(rangeOnly, ac.signal)])
          : [await getStats(filtered, ac.signal), null]
        // 被取代的那一笔直接丢弃：它的数据对应的是一个已经不成立的筛选
        if (ac.signal.aborted) return
        setStats(data)
        setUniverse(base)
        setError(null)
        cache.current.set(key, { stats: data, universe: base, at: Date.now() })
      } catch (err) {
        // 主动取消不是失败：既不置错误态，也不弹提示
        if (ac.signal.aborted || isAbortError(err)) return
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
        toast.error(tRef.current("analytics:load_failed", { message }))
      } finally {
        // 只有"还是我这一笔"才收尾：否则会把后来那笔的 loading 提前关掉
        if (inflight.current === ac) {
          inflight.current = null
          setPhase("idle")
          setStartedAt(null)
        }
      }
    },
    [preset, customFrom, customTo, granularity, filter, hasFilter]
  )

  useEffect(() => {
    void load()
  }, [load])

  // 离开页面时掐掉在途请求：慢库上那一笔会一直挂着读事务
  useEffect(() => () => inflight.current?.abort(), [])

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
  // FilterRow 的按钮/禁用态沿用原来的"是否在取数"语义
  const loading = phase !== "idle"
  const slow = loading && elapsedSec >= slowHintAfterSec
  const rangeLabel =
    preset === "custom" ? t("analytics:range.custom") : t(`analytics:range.${preset}` as never)

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

      {/* 刷新时旧数据还在：慢过 2 秒才给细进度条 + 已用时间 + 取消，而不是把整页换成骨架。
          快请求什么都不显示，免得闪一下 */}
      {phase === "refreshing" && busy && (
        <div
          className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground"
          role="status"
          aria-live="polite"
        >
          <div className="h-0.5 min-w-16 flex-1 animate-pulse rounded bg-primary/40" />
          <span>{t("analytics:refreshing", { seconds: elapsedSec })}</span>
          <Button size="sm" variant="ghost" className="h-6 px-2 text-xs" onClick={cancel}>
            {t("analytics:cancel")}
          </Button>
        </div>
      )}

      {/* 慢窗口的出口：与其让人干等，不如给一个"先看 24 小时"的按钮 */}
      {slow && preset !== "last_24h" && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-accent/40 px-3 py-2 text-sm">
          <span>{t("analytics:slow_hint", { range: rangeLabel, seconds: elapsedSec })}</span>
          <Button
            size="sm"
            variant="outline"
            className="h-7"
            onClick={() => handlePreset("last_24h")}
          >
            {t("analytics:view_last_24h")}
          </Button>
        </div>
      )}

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
            {/* 骨架立刻有（否则页面是空的），但"已用时间 + 取消"要慢过 2 秒才出现 */}
            {busy && (
              <div className="flex flex-wrap items-center gap-3">
                <Loader2 className="size-4 shrink-0 animate-spin text-muted-foreground" aria-hidden="true" />
                <span className="text-sm text-muted-foreground">
                  {t("analytics:loading_window", { range: rangeLabel, seconds: elapsedSec })}
                </span>
                <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={cancel}>
                  {t("analytics:cancel")}
                </Button>
              </div>
            )}
            <div className="h-16 animate-pulse rounded-lg bg-muted" />
            <Skeleton className="h-56 w-full" />
          </div>
        ) : !hasData && canceled ? (
          // 取消 ≠ 空。数据没取回来时不能宣称"范围内没有请求"，
          // 那样会把用户引去改时间范围/清筛选，而真正该做的是重新加载
          <div className="flex flex-col items-center gap-3 rounded-lg border border-border px-4 py-12 text-center">
            <p className="text-sm font-medium">{t("analytics:canceled.title")}</p>
            <p className="text-xs text-muted-foreground">{t("analytics:canceled.hint")}</p>
            <Button size="sm" variant="outline" onClick={() => void load()}>
              {t("analytics:retry")}
            </Button>
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
 * 三张图而不是一张双轴图：请求数与 Token 量级差着几个数量级，双轴会把
 * 缩放对齐关系变成任意的，从而凭空造出数据里不存在的相关性。成本同理独立。
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

      {/* 点下钻任一分组名后本视图随同一筛选重取——成本趋势因此天然支持
          按模型/提供商/Key/请求名/UA 切片，不需要给每个分组单独开趋势接口 */}
      <Panel
        title={t("trend.cost")}
        note={`${t("trend.cost_note")} · ${formatCost(k.cost, k.currency)}`}
      >
        <CostTrendChart data={stats.trend} currency={k.currency} />
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

