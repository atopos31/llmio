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

import { QuotaCard } from "@/routes/quota-card"
import { QuotaEditorDialog } from "@/routes/quota-editor"
import { QuotaImportDialog } from "@/routes/quota-import"
import { QuotaItemDialog } from "@/routes/quota-item-dialog"
import { QuotaSettingsDialog } from "@/routes/quota-settings"
import { QuotaSourceViewDialog } from "@/routes/quota-source-view"
import { EmptyState } from "@/components/state-views"
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
  clearSourceOverrides,
  DEFAULT_QUOTA_VIEW,
  itemsOf,
  loadQuotaView,
  pruneOverrides,
  QUOTA_VIEW_STORAGE_KEY,
  saveQuotaView,
  statusRank,
  type QuotaChartStyle,
  type QuotaItem,
  type QuotaOverridePatch,
  type QuotaSource,
  type QuotaSourceResult,
  type QuotaViewPrefs,
  type QuotaConfigResponse,
} from "@/lib/quota"
import { getQuotaConfig, runQuotaSources, refreshQuotaSource } from "@/lib/api"
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

  const [editing, setEditing] = useState<QuotaSourceResult | null>(null)
  const [editorOpen, setEditorOpen] = useState(false)
  const [viewing, setViewing] = useState<QuotaSourceResult | null>(null)
  const [itemCtx, setItemCtx] = useState<{
    source: QuotaSourceResult
    item: QuotaItem
  } | null>(null)
  const [importOpen, setImportOpen] = useState(false)
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
   */
  const load = useCallback(
    async (opts: { force?: boolean; silent?: boolean } = {}) => {
      if (opts.force) setRefreshing(true)
      else setLoading(true)
      try {
        const [conf, res] = await Promise.all([
          getQuotaConfig(),
          runQuotaSources({ force: opts.force }),
        ])
        setCfg(conf)
        setSources(res.sources ?? [])
        setUpdatedAt(res.generatedAt)
        setLoadError(null)
        // 数据源被删掉后清理它的展示覆盖：不清理的话重建同名源会"继承"旧设置
        persist(
          pruneOverrides(
            prefsRef.current,
            (res.sources ?? []).map((s) => s.id)
          )
        )
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

  const { visible, hidden } = useMemo(() => {
    const vis: QuotaSourceResult[] = []
    const hid: QuotaSourceResult[] = []
    for (const s of sources) {
      if (prefs.overrides[s.id]?.hidden) hid.push(s)
      else vis.push(s)
    }
    return { visible: vis, hidden: hid }
  }, [sources, prefs.overrides])

  const editable = cfg?.writeEnabled !== false

  const openManual = () => {
    setEditing(null)
    setEditorOpen(true)
  }
  const openEdit = useCallback((s: QuotaSourceResult) => {
    setEditing(s)
    setEditorOpen(true)
  }, [])

  const onSaved = useCallback(
    (saved: QuotaSource, deleted?: boolean) => {
      setEditorOpen(false)
      if (deleted) {
        // 删掉后整轮重跑：被删的源要立刻从网格里消失，
        // 而不是等下一次刷新（那期间点它还会打开一个已不存在的源）
        void load({ silent: true })
        return
      }
      // 保存后立刻重取这一条，让卡片马上反映新配置
      void refreshOne(saved.id)
    },
    [load, refreshOne]
  )

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
            <DropdownMenuItem onSelect={() => setImportOpen(true)}>
              {t("menu.import")}
            </DropdownMenuItem>
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
                <span
                  className={cn(
                    "size-2 rounded-full",
                    summary.worst.it.status === "exhausted"
                      ? "bg-status-critical"
                      : summary.worst.it.status === "warning"
                        ? "bg-status-warning"
                        : summary.worst.it.status === "ok"
                          ? "bg-status-good"
                          : "bg-muted-foreground"
                  )}
                />
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
          {updatedAt > 0 && (
            <span className="text-xs text-muted-foreground">
              · {t("summary.gen_time", { time: clock(updatedAt), defaultValue: "{{time}}" })}
            </span>
          )}
        </div>
      )}

      {/* ---- 内容 ---- */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading ? (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-32" />
            ))}
          </div>
        ) : loadError && sources.length === 0 ? (
          <EmptyState title={t("error.load")} hint={loadError} />
        ) : sources.length === 0 ? (
          <EmptyState
            title={t("empty.title")}
            hint={t("empty.desc")}
            action={
              editable && (
                <div className="flex gap-2">
                  <Button size="sm" onClick={openManual}>
                    <Plus className="size-3.5" />
                    {t("menu.manual")}
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => setImportOpen(true)}>
                    {t("menu.import")}
                  </Button>
                </div>
              )
            }
          />
        ) : (
          <div className="flex flex-col gap-3">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {visible.map((s) => (
                <QuotaCard
                  key={s.id}
                  source={s}
                  prefs={prefs}
                  editable={editable}
                  refreshing={refreshingId === s.id}
                  onRefresh={(id) => void refreshOne(id)}
                  onEdit={openEdit}
                  onView={setViewing}
                  onEditItem={(source, item) => setItemCtx({ source, item })}
                />
              ))}
            </div>

            {hidden.length > 0 && (
              <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                <span>{t("hidden_sources")}</span>
                {hidden.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    className="rounded-sm border border-border px-1.5 py-0.5 hover:bg-accent"
                    onClick={() => setOverride(s.id, { hidden: false })}
                  >
                    {s.name} · {t("restore")}
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
          source={editing}
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

      {importOpen && (
        <QuotaImportDialog
          open
          onOpenChange={setImportOpen}
          // 导入后的数据源 id 由服务端按 llmio-<upstreamId> 生成
          // （service.ImportedSourceID），这里用同一条规则换算，
          // 以便只重取刚导入的那一条而不是整轮重跑。
          onImported={(upstreamId) => void refreshOne(`llmio-${upstreamId}`)}
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
