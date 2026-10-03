import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  AlertTriangle,
  ChevronDown,
  Plus,
  RefreshCw,
  RotateCcw,
  Settings2,
} from "lucide-react"
import { toast } from "sonner"

import { QuotaCard, QuotaDisabledCard, QuotaStatusBadge } from "@/routes/quota-card"
import { QuotaEditorDialog } from "@/routes/quota-editor"
import { QuotaItemDialog } from "@/routes/quota-item-dialog"
import { QuotaSettingsDialog } from "@/routes/quota-settings"
import { QuotaSourceViewDialog } from "@/routes/quota-source-view"
import { EmptyState, ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  CHART_STYLES,
  cardEntryId,
  cardEntryName,
  clearSourceOverrides,
  DEFAULT_QUOTA_VIEW,
  editableSource,
  itemsOf,
  loadQuotaView,
  pruneOverrides,
  QUOTA_VIEW_STORAGE_KEY,
  quotaCardEntries,
  saveQuotaView,
  statusRank,
  type QuotaCardEntry,
  type QuotaChartStyle,
  type QuotaItem,
  type QuotaOverridePatch,
  type QuotaSource,
  type QuotaSourceResult,
  type QuotaViewPrefs,
  type QuotaConfigResponse,
} from "@/lib/quota"
import {
  getQuotaConfig,
  runQuotaSources,
  refreshQuotaSource,
  updateQuotaSource,
} from "@/lib/api"
import { cn } from "@/lib/utils"

/** 自动刷新档位（秒）。0 = 关闭。余量接口普遍较慢，因而起点比日志页高。 */
const REFRESH_OPTIONS = [0, 60, 120, 300, 600]

export default function QuotaPage() {
  const { t } = useTranslation("quota")

  const [cfg, setCfg] = useState<QuotaConfigResponse | null>(null)
  const [sources, setSources] = useState<QuotaSourceResult[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [refreshingId, setRefreshingId] = useState<string | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [updatedAt, setUpdatedAt] = useState(0)
  const [interval, setIntervalSec] = useState(0)

  const [prefs, setPrefs] = useState<QuotaViewPrefs>(() => {
    try {
      return loadQuotaView(localStorage.getItem(QUOTA_VIEW_STORAGE_KEY))
    } catch {
      // 隐私模式下 localStorage 访问本身就会抛，而不是返回 null
      return { ...DEFAULT_QUOTA_VIEW, overrides: {} }
    }
  })

  const [editing, setEditing] = useState<QuotaSourceResult | QuotaSource | null>(null)
  const [editorOpen, setEditorOpen] = useState(false)
  const [viewing, setViewing] = useState<QuotaSourceResult | null>(null)
  // 正在启用/停用的那一个源：按钮要禁用，免得点两下发出两次写请求
  const [busyId, setBusyId] = useState<string | null>(null)
  const [itemCtx, setItemCtx] = useState<{
    source: QuotaSourceResult
    item: QuotaItem
  } | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)

  const prefsRef = useRef(prefs)
  prefsRef.current = prefs

  const persist = useCallback((next: QuotaViewPrefs) => {
    setPrefs(next)
    try {
      localStorage.setItem(QUOTA_VIEW_STORAGE_KEY, saveQuotaView(next))
    } catch {
      // 存不进去不影响本次会话的展示
    }
  }, [])

  // -------------------------------------------------------------------------
  // 取数
  // -------------------------------------------------------------------------

  /**
   * 跑一轮。
   *
   * silent 用于自动刷新：失败时不弹 toast（网络瞬断就弹一次会让面板
   * 一直在冒泡），但**仍要**把错误留在页面上。
   *
   * quiet 用于"这只是个附带动作"的重取（启用/停用之后、保存之后）：不摆
   * 骨架屏。骨架说的是"这一页还没数据"，而这些时候页面上本来就有数据，
   * 把整页闪成占位块只会让人觉得刚才那一下把页面弄丢了。
   */
  const load = useCallback(
    async (opts: { force?: boolean; silent?: boolean; quiet?: boolean } = {}) => {
      if (opts.force) setRefreshing(true)
      else if (!opts.quiet) setLoading(true)
      try {
        const [conf, res] = await Promise.all([
          getQuotaConfig(),
          runQuotaSources({ force: opts.force }),
        ])
        setCfg(conf)
        setSources(res.sources ?? [])
        setUpdatedAt(res.generatedAt)
        setLoadError(null)
        // 展示覆盖按"配置里的源 ∪ 这一轮跑过的源"清，而不是只按取数结果清：
        // 停用源不在结果里，只按结果清会把它的显示名与图表样式一并抹掉——
        // 那意味着"停用一下再启用，这张卡就变回原样了"。配置才是"这个源还在
        // 不在"的权威，保留结果那一份是反过来兜底：两份名单不一致时
        // （配置被别处改过）宁可多留几条覆盖，也不要一次清空用户设置。
        const keep = new Set([
          ...conf.config.sources.map((s) => s.id),
          ...(res.sources ?? []).map((s) => s.id),
        ])
        persist(pruneOverrides(prefsRef.current, [...keep]))
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err)
        setLoadError(msg)
        if (!opts.silent) toast.error(t("error.load"), { description: msg })
      } finally {
        setLoading(false)
        setRefreshing(false)
      }
    },
    [persist, t]
  )

  useEffect(() => {
    void load()
  }, [load])

  // 自动刷新：页面隐藏时不打上游，回到前台立刻补一轮
  useEffect(() => {
    if (!interval) return
    const id = window.setInterval(() => {
      if (document.hidden) return
      void load({ silent: true })
    }, interval * 1000)
    const onVisible = () => {
      if (!document.hidden) void load({ silent: true })
    }
    document.addEventListener("visibilitychange", onVisible)
    return () => {
      window.clearInterval(id)
      document.removeEventListener("visibilitychange", onVisible)
    }
  }, [interval, load])

  const refreshOne = useCallback(
    async (id: string) => {
      setRefreshingId(id)
      try {
        const res = await refreshQuotaSource(id)
        setSources(res.sources ?? [])
        setUpdatedAt(res.generatedAt)
      } catch (err) {
        toast.error(t("error.load"), {
          description: err instanceof Error ? err.message : String(err),
        })
      } finally {
        setRefreshingId(null)
      }
    },
    [t]
  )

  // -------------------------------------------------------------------------
  // 展示偏好
  // -------------------------------------------------------------------------

  const setChartStyle = (style: QuotaChartStyle) => persist({ ...prefs, chartStyle: style })
  const setShowMeta = (on: boolean) => persist({ ...prefs, showMeta: on })

  const setOverride = useCallback(
    (key: string, patch: QuotaOverridePatch) => {
      // 与 applyOverride 同样的规则：空值表示清除该字段
      const cur = { ...(prefsRef.current.overrides[key] ?? {}) }
      for (const [k, v] of Object.entries(patch)) {
        if (v === "" || v === null || v === undefined || v === false) {
          delete (cur as Record<string, unknown>)[k]
        } else {
          ;(cur as Record<string, unknown>)[k] = v
        }
      }
      const overrides = { ...prefsRef.current.overrides }
      if (Object.keys(cur).length) overrides[key] = cur
      else delete overrides[key]
      persist({ ...prefsRef.current, overrides })
    },
    [persist]
  )

  const resetView = () => {
    persist({ ...DEFAULT_QUOTA_VIEW, overrides: {} })
    toast.success(t("menu.reset_view"))
  }

  /** 清掉单个数据源的全部覆盖，含条目级——卡片上的"重置"用这个。 */
  const resetSourceView = useCallback(
    (sourceId: string) => {
      persist(clearSourceOverrides(prefsRef.current, sourceId))
      toast.success(t("source_view.reset"))
    },
    [persist, t]
  )

  // -------------------------------------------------------------------------
  // 派生
  // -------------------------------------------------------------------------

  const summary = useMemo(() => {
    const ok = sources.filter((s) => s.ok)
    const items = ok.flatMap((s) =>
      itemsOf(s).map((it) => ({ it, source: s }))
    )
    const comparable = items.filter((x) => x.it.percent !== null)
    const worst = comparable.reduce<{ it: QuotaItem; source: QuotaSourceResult } | null>(
      (acc, cur) => {
        if (!acc) return cur
        const d = statusRank(cur.it.status) - statusRank(acc.it.status)
        if (d !== 0) return d > 0 ? cur : acc
        return (cur.it.percent ?? 0) > (acc.it.percent ?? 0) ? cur : acc
      },
      null
    )
    return {
      total: sources.length,
      okCount: ok.length,
      failCount: sources.length - ok.length,
      items: items.length,
      worst,
    }
  }, [sources])

  /**
   * 网格里的全部卡片：跑过一轮的源 + 配置里停用的源。
   *
   * 停用源必须并进来，否则它在这页上就没有落点（见 `quotaCardEntries`）。
   * 它不进 `sources`——摘要条与"最差"都只该看真的取到了数的那几个源。
   */
  const entries = useMemo(() => quotaCardEntries(sources, cfg?.config), [sources, cfg])

  const { visible, hidden, disabledCount } = useMemo(() => {
    const vis: QuotaCardEntry[] = []
    const hid: QuotaCardEntry[] = []
    for (const e of entries) {
      if (prefs.overrides[cardEntryId(e)]?.hidden) hid.push(e)
      else vis.push(e)
    }
    return {
      visible: vis,
      hidden: hid,
      disabledCount: entries.filter((e) => e.kind === "disabled").length,
    }
  }, [entries, prefs.overrides])

  const editable = cfg?.writeEnabled !== false

  const openManual = () => {
    setEditing(null)
    setEditorOpen(true)
  }
  const openEdit = useCallback((s: QuotaSourceResult | QuotaSource) => {
    setEditing(s)
    setEditorOpen(true)
  }, [])

  /**
   * 启用 / 停用一个已保存的源。
   *
   * 写入走的是**整份替换**的更新端点，因此必须带上配置里那一份完整源
   * （密钥是掩码，服务端会还原），不能只发 id 与 enabled——
   * 那会把 url、headers、env 通通抹掉。
   *
   * 写完之后重取一轮配置：`enabled` 在两个地方各有一份（配置说这个源是否
   * 启用，取数结果说它这一轮跑没跑），只更新结果的话，页面会拿旧配置继续
   * 把它当成停用（反之亦然），这个开关就会看起来时灵时不灵。
   */
  const setSourceEnabled = useCallback(
    async (source: QuotaSource, enabled: boolean) => {
      setBusyId(source.id)
      try {
        await updateQuotaSource({ ...source, enabled })
        await load({ quiet: true })
      } catch (err) {
        toast.error(t("error.save"), {
          description: err instanceof Error ? err.message : String(err),
        })
      } finally {
        setBusyId(null)
      }
    },
    [load, t]
  )

  /**
   * 交给编辑器的初值。
   *
   * 卡片上那份是**取数结果**，只有展示字段；配置里那一份才是完整的（密钥已
   * 脱敏）。编辑器必须拿完整的，否则保存时"整份替换"会把它没回填的字段
   * 一并清掉。cfg 与 sources 每次加载成对更新，因此正常情况下都找得到。
   */
  const editingSource = useMemo(
    () =>
      editing
        ? editableSource(
            editing,
            cfg?.config.sources.find((s) => s.id === editing.id)
          )
        : null,
    [editing, cfg]
  )

  /**
   * 编辑器保存/删除之后重取一轮。
   *
   * 保存后必须**连配置一起重取**，不能只重取这一个源：`enabled` 两处都有
   * （配置那份决定它进不进网格，结果那份决定卡片画什么），只更新结果的话，
   * 在编辑器里把开关一关，页面会拿旧配置继续把它当成启用的——卡片照旧
   * 画着上一轮的读数，直到下次整页刷新才消失。这条路径与停用卡片上的
   * "启用"是同一个道理。
   *
   * 不走 force：其余源命中缓存，只有刚动过的那一个（服务端在保存时已把
   * 它的缓存清了）会重新取数，因此"保存后立刻看到新结果"仍然成立。
   */
  const onSaved = useCallback(() => {
    setEditorOpen(false)
    void load({ quiet: true })
  }, [load])

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-1">
      {/* ---- 标题行 ---- */}
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="mr-auto text-xl font-semibold tracking-tight">{t("title")}</h2>

        {/* 图表样式：切换的是同一视图的呈现参数，因此用分段控件而非 tabs */}
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={prefs.chartStyle}
          onValueChange={(v) => v && setChartStyle(v as QuotaChartStyle)}
          aria-label={t("chart_style_label")}
        >
          {CHART_STYLES.map((s) => (
            <ToggleGroupItem key={s.value} value={s.value} size="sm" title={t(`${s.labelKey}_desc` as never, { defaultValue: "" })}>
              {t(s.labelKey as never, { ns: "quota" })}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>

        <Button
          variant="ghost"
          size="sm"
          onClick={() => setShowMeta(!prefs.showMeta)}
          aria-pressed={prefs.showMeta}
          className={cn(!prefs.showMeta && "text-muted-foreground")}
        >
          {t("show_meta")}
        </Button>

        <select
          className="h-8 rounded-md border border-border bg-background px-2 text-xs"
          value={interval}
          onChange={(e) => setIntervalSec(Number(e.target.value))}
          aria-label={t("auto_refresh")}
          title={t("auto_refresh")}
        >
          {REFRESH_OPTIONS.map((s) => (
            <option key={s} value={s}>
              {s === 0
                ? `${t("auto_refresh")}：${t("auto_refresh_off")}`
                : t("auto_refresh_every", { seconds: s })}
            </option>
          ))}
        </select>

        <Button variant="outline" size="sm" disabled={refreshing} onClick={() => void load({ force: true })}>
          <RefreshCw className={cn("size-3.5", refreshing && "animate-spin")} />
          {refreshing ? t("refreshing") : t("refresh")}
        </Button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm" disabled={!editable}>
              <Plus className="size-3.5" />
              {t("menu.add_source")}
              <ChevronDown className="size-3" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={openManual}>{t("menu.manual")}</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>{t("settings.title")}</DropdownMenuLabel>
            <DropdownMenuItem onSelect={() => setSettingsOpen(true)}>
              <Settings2 className="size-4" />
              {t("menu.settings")}
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={resetView}>
              <RotateCcw className="size-4" />
              {t("menu.reset_view")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* ---- 只读提示 ---- */}
      {cfg && !cfg.writeEnabled && (
        <div className="flex items-start gap-2 rounded-md border border-border bg-accent/40 px-3 py-2 text-sm">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-status-warning-ink" />
          <span>{t("readonly_notice")}</span>
        </div>
      )}

      {/* ---- 摘要条 ---- */}
      {!loading && sources.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          {summary.worst ? (
            <>
              <span className="text-muted-foreground">{t("summary.worst")}</span>
              <span className="flex items-center gap-1.5 font-medium">
                {/* 状态用卡片那枚"字形 + 文字"的徽标，而不是只换颜色的小圆点：
                    红绿对立对色盲用户不可用，圆点也不带任何可读的文字 */}
                <QuotaStatusBadge status={summary.worst.it.status} compact />
                {summary.worst.source.name} · {summary.worst.it.label}
                {summary.worst.it.percent !== null && (
                  <span className="reading">
                    {Math.round(summary.worst.it.percent * 10) / 10}%
                  </span>
                )}
              </span>
            </>
          ) : (
            <span className="text-muted-foreground">
              {sources.some((s) => s.ok) ? t("summary.no_items") : t("summary.all_ok")}
            </span>
          )}
          <span className="text-muted-foreground">
            · {t("summary.items", { count: summary.items, defaultValue: "{{count}} items" })}
          </span>
          <span className="text-muted-foreground">
            ·{" "}
            {t("summary.sources_ok", {
              ok: summary.okCount,
              total: summary.total,
              defaultValue: "{{ok}}/{{total}} ok",
            })}
          </span>
          {summary.failCount > 0 && (
            <span className="rounded-sm bg-status-critical/10 px-1.5 py-0.5 text-xs text-status-critical-ink">
              {t("summary.fails", { count: summary.failCount, defaultValue: "{{count}} failing" })}
            </span>
          )}
          {disabledCount > 0 && (
            <span className="text-muted-foreground">
              ·{" "}
              {t("summary.disabled", {
                count: disabledCount,
                defaultValue: "{{count}} disabled",
              })}
            </span>
          )}
          {updatedAt > 0 && (
            <span className="text-xs text-muted-foreground">
              · {t("summary.gen_time", { time: clock(updatedAt), defaultValue: "{{time}}" })}
            </span>
          )}
        </div>
      )}

      {/* 一个启用的源都没有时，摘要条整条不出现（0/0 个数据源正常没有意义）。
          但"没有可比数字"不等于"什么都没发生"：这页上有停用源，就得说出来，
          否则用户看到的是一片空，而空态又只在真的一个源都没配时才该出现。 */}
      {!loading && !loadError && sources.length === 0 && disabledCount > 0 && (
        <p className="text-sm text-muted-foreground">{t("all_disabled")}</p>
      )}

      {/* ---- 内容 ---- */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading ? (
          /* 骨架对读屏等于空白：不声明 role="status"，"正在读取余量"与
             "还没有配置数据源"在无障碍树上就是同一件事 */
          <div role="status" aria-busy="true" className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            <span className="sr-only">{t("loading")}</span>
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-32" />
            ))}
          </div>
        ) : loadError && sources.length === 0 ? (
          /* 失败用失败态而不是空态：空态没有原文、也没有重试。原先这两态
             长得一样，"没取到"就被读成了"没有配置数据源" */
          <ErrorState
            title={t("error.load")}
            message={loadError}
            retryLabel={t("refresh")}
            onRetry={() => void load({ force: true })}
          />
        ) : entries.length === 0 ? (
          /* 空态的门槛是"一个源都没有"，而不是"一张卡都没有"：只剩停用源时
             这一页仍然有东西可看、有事可做（把它们打开），给一句"还没有配置
             数据源"会让人以为配置丢了——他明明刚把它关掉。 */
          <EmptyState
            title={t("empty.title")}
            hint={t("empty.desc")}
            action={
              editable && (
                <Button size="sm" onClick={openManual}>
                  <Plus className="size-3.5" />
                  {t("menu.manual")}
                </Button>
              )
            }
          />
        ) : (
          <div className="flex flex-col gap-3">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {visible.map((entry) =>
                entry.kind === "result" ? (
                  <QuotaCard
                    key={entry.result.id}
                    source={entry.result}
                    prefs={prefs}
                    editable={editable}
                    refreshing={refreshingId === entry.result.id}
                    onRefresh={(id) => void refreshOne(id)}
                    onEdit={openEdit}
                    onView={setViewing}
                    onEditItem={(source, item) => setItemCtx({ source, item })}
                  />
                ) : (
                  <QuotaDisabledCard
                    key={entry.source.id}
                    source={entry.source}
                    prefs={prefs}
                    editable={editable}
                    busy={busyId === entry.source.id}
                    onEnable={(s) => void setSourceEnabled(s, true)}
                    onEdit={openEdit}
                  />
                )
              )}
            </div>

            {hidden.length > 0 && (
              <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                <span>{t("hidden_sources")}</span>
                {hidden.map((entry) => (
                  <button
                    key={cardEntryId(entry)}
                    type="button"
                    className="rounded-sm border border-border px-1.5 py-0.5 hover:bg-accent"
                    onClick={() => setOverride(cardEntryId(entry), { hidden: false })}
                  >
                    {cardEntryName(entry)} · {t("restore")}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {/* ---- 对话框 ---- */}
      {editorOpen && (
        <QuotaEditorDialog
          open
          onOpenChange={(o) => !o && setEditorOpen(false)}
          source={editingSource}
          builtins={cfg?.builtins ?? []}
          defaults={{
            refresh: cfg?.defaultRefresh ?? 20,
            warning: cfg?.defaultWarning ?? 80,
          }}
          onSaved={onSaved}
        />
      )}

      {viewing && (
        <QuotaSourceViewDialog
          open
          onOpenChange={(o) => !o && setViewing(null)}
          source={viewing}
          override={prefs.overrides[viewing.id] ?? {}}
          onChange={(patch) => setOverride(viewing.id, patch)}
          onReset={() => resetSourceView(viewing.id)}
        />
      )}

      {itemCtx && (
        <QuotaItemDialog
          open
          onOpenChange={(o) => !o && setItemCtx(null)}
          source={itemCtx.source}
          item={itemCtx.item}
          override={
            prefs.overrides[`${itemCtx.source.id}::${itemCtx.item.id}`] ?? {}
          }
          onChange={(patch) =>
            setOverride(`${itemCtx.source.id}::${itemCtx.item.id}`, patch)
          }
        />
      )}

      {settingsOpen && cfg && (
        <QuotaSettingsDialog
          open
          onOpenChange={setSettingsOpen}
          config={cfg}
          onSaved={() => void load({ force: true })}
        />
      )}
    </div>
  )
}


function clock(ms: number): string {
  const d = new Date(ms)
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`
}
