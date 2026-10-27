import { useCallback, useEffect, useMemo, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { AlertTriangle, ChevronLeft, ChevronRight, Columns3, Eye, GitCompareArrows, MessageSquare, RefreshCw, Search, Trash2, X } from "lucide-react"

import { LogDetailSheet } from "@/components/log-detail-sheet"
import { MultiSelectFilter, type FilterOption } from "@/components/multi-select-filter"
import { RefreshSelect } from "@/components/refresh-select"
import { StatusMark } from "@/components/status-mark"
import { ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAutoRefresh } from "@/hooks/use-auto-refresh"
import {
  cleanLogs,
  getAuthKeysList,
  getLogs,
  getModelOptions,
  getObservedProviderModels,
  getProviderTemplates,
  getProviders,
  type AuthKeyItem,
  type ChatLog,
  type Model,
  type Provider,
} from "@/lib/api"
import { compactNumber } from "@/lib/format"
import {
  activeMultiCount,
  canViewChatIO,
  applyMultiFilter,
  joinMulti,
  readMultiFilter,
  readMultiValue,
  toggleMulti,
  type LogsMultiKey,
} from "@/lib/logs"
import {
  LOG_COLUMNS,
  LOG_DEFAULT_VISIBLE_IDS,
  LOG_TOGGLEABLE_IDS,
  cacheRate,
  visibleColumns,
} from "@/lib/logs-columns"
import {
  refreshSecFor,
  readUiPrefs,
  withLogsColumns,
  withRefreshSec,
  writeUiPrefs,
  type UiPrefs,
} from "@/lib/prefs"
import { cn } from "@/lib/utils"

/** 对比上限。与后端一次返回的详情体积、以及人眼能同时比较的条数都有关。 */
const MAX_COMPARE = 6

/** 自动刷新档位（秒）。0 表示关闭。 */
const LOGS_REFRESH_SECONDS = [0, 5, 10, 30, 60]

/** 请求状态到 i18n 键的映射。后端的 status 是固定的三个英文值。 */
function statusKey(status: string): "success" | "error" | "running" | "unknown" {
  return status === "success" || status === "error" || status === "running" ? status : "unknown"
}

export default function LogsPage() {
  const { t } = useTranslation(["logs", "common"])
  const navigate = useNavigate()

  const [logs, setLogs] = useState<ChatLog[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [total, setTotal] = useState(0)
  const [pages, setPages] = useState(0)
  const [providers, setProviders] = useState<Provider[]>([])
  const [models, setModels] = useState<Model[]>([])
  // 上游模型名候选来自历史日志（区别于 models 那份本地别名表）
  const [providerModels, setProviderModels] = useState<string[]>([])
  const [authKeys, setAuthKeys] = useState<AuthKeyItem[]>([])
  const [availableStyles, setAvailableStyles] = useState<string[]>([])

  // 筛选与分页状态同步到 URL，浏览器返回时自动恢复，链接也可分享
  const [searchParams, setSearchParams] = useSearchParams()
  const page = Math.max(1, Number(searchParams.get("page")) || 1)
  const pageSize = Math.max(1, Number(searchParams.get("pageSize")) || 20)
  /**
   * 六个多选维度。URL 里的取值是逗号拼接的一串（与后端 splitCSV + IN 同口径），
   * 页面只认这一份**解析后**的结果：控件的取值、发给后端的参数、有没有在筛选，
   * 全部由它派生。若某处直接读原始串，就会出现"界面上没筛、请求里却带着 all"
   * 这种两处口径不一——而它不会报错，只会筛出意料之外的东西。
   */
  const multiFilter = useMemo(() => readMultiFilter((k) => searchParams.get(k)), [searchParams])
  const traceIdFilter = searchParams.get("traceId") ?? ""
  const sessionIdFilter = searchParams.get("sessionId") ?? ""
  const idFilter = searchParams.get("id") ?? ""

  const patchParams = useCallback(
    (patch: Record<string, string | number>) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          for (const [k, v] of Object.entries(patch)) {
            const s = String(v)
            // 空串对每个参数都等于"没筛"：多选全取消后拼出来的正是空串，
            // 于是这个参数从 URL 上消失（旧版是写回 "all" 哨兵值）
            const isDefault =
              s === "" || (k === "page" && s === "1") || (k === "pageSize" && s === "20")
            if (isDefault) next.delete(k)
            else next.set(k, s)
          }
          return next
        },
        { replace: true }
      )
    },
    [setSearchParams]
  )

  /** 任一筛选变化都回到第 1 页——否则会停在一个可能已不存在的页码上。 */
  const setFilter = useCallback(
    (key: string, value: string) => patchParams({ [key]: value, page: 1 }),
    [patchParams]
  )

  /**
   * 多选维度上的一次勾选。
   *
   * 读写都发生在 `setSearchParams` 的函数式更新里，而不是"先读当前数组、
   * 算好新串、再写回去"：连点两下复选框时，后者两次都基于同一份旧 URL 计算，
   * 第二次会把第一次的结果覆盖掉——表现是"点了两下只选上了一个"。
   */
  const toggleMultiFilter = useCallback(
    (key: LogsMultiKey, value: string) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          return applyMultiFilter(next, key, toggleMulti(readMultiValue((k) => next.get(k), key), value))
        },
        { replace: true }
      )
    },
    [setSearchParams]
  )

  const hasActiveFilter =
    activeMultiCount(multiFilter) > 0 ||
    traceIdFilter !== "" ||
    sessionIdFilter !== "" ||
    idFilter !== ""

  const clearFilters = useCallback(() => {
    setSearchParams(new URLSearchParams(), { replace: true })
  }, [setSearchParams])

  // 详情抽屉
  const [detailLog, setDetailLog] = useState<ChatLog | null>(null)

  // 清理弹窗
  const [cleanType, setCleanType] = useState<"count" | "days">("count")
  const [cleanValue, setCleanValue] = useState("1000")
  const [isCleanDialogOpen, setIsCleanDialogOpen] = useState(false)
  const [cleanLoading, setCleanLoading] = useState(false)

  /**
   * 对比选中项。用 Map 而非 Set 保存整条记录：
   * 选中需要跨页/跨筛选存活，而翻页后原记录已不在当前列表里，
   * 只留 ID 就没法再展示它的模型名等信息。
   */
  const [selected, setSelected] = useState<Map<number, ChatLog>>(new Map())

  // 自动刷新档位与可见列落 localStorage：三页共用一份偏好，刷新页面后仍在
  const [uiPrefs, setUiPrefs] = useState(() => readUiPrefs())
  const refreshSec = refreshSecFor(uiPrefs, "logs", LOGS_REFRESH_SECONDS)
  const cols = useMemo(() => visibleColumns(uiPrefs.logsColumns), [uiPrefs.logsColumns])

  const setPref = (next: UiPrefs) => {
    setUiPrefs(next)
    writeUiPrefs(next)
  }

  const fetchOptions = useCallback(async () => {
    // 筛选下拉的选项来源与日志本身无关，失败只影响可选范围，不阻断列表
    const [p, m, pm, a, tpl] = await Promise.allSettled([
      getProviders(),
      getModelOptions(),
      getObservedProviderModels(),
      getAuthKeysList(),
      getProviderTemplates(),
    ])
    if (p.status === "fulfilled") setProviders(p.value)
    if (m.status === "fulfilled") setModels(m.value)
    if (pm.status === "fulfilled") setProviderModels(pm.value)
    if (a.status === "fulfilled") setAuthKeys(a.value)
    if (tpl.status === "fulfilled") setAvailableStyles(tpl.value.map((x) => x.type))
  }, [])

  const fetchLogs = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true)
      try {
        const result = await getLogs(page, pageSize, {
          // 空列表即"没筛"：拼出来是空串，于是这个参数根本不发
          providerName: joinMulti(multiFilter.providerName) || undefined,
          name: joinMulti(multiFilter.requestName) || undefined,
          providerModel: joinMulti(multiFilter.providerModel) || undefined,
          status: joinMulti(multiFilter.status) || undefined,
          style: joinMulti(multiFilter.style) || undefined,
          authKeyId: joinMulti(multiFilter.authKey) || undefined,
          traceId: traceIdFilter.trim() || undefined,
          sessionId: sessionIdFilter.trim() || undefined,
          id: idFilter.trim() || undefined,
        })
        setLogs(result.data)
        setTotal(result.total)
        setPages(result.pages)
        setLoadError(null)
        // 静默刷新（自动刷新）失败不落成页面上的错误：一次网络瞬断就把整页
        // 换成失败态，比保留上一次的结果更打扰人。手动的取数失败则必须说出来。
      } catch (err) {
        if (!silent) {
          setLoadError(err instanceof Error ? err.message : String(err))
        }
      } finally {
        if (!silent) setLoading(false)
      }
    },
    [page, pageSize, multiFilter, traceIdFilter, sessionIdFilter, idFilter]
  )

  useEffect(() => {
    void fetchOptions()
  }, [fetchOptions])

  useEffect(() => {
    void fetchLogs()
  }, [fetchLogs])

  // 自动刷新走静默取数：不闪骨架、失败不落错误条
  useAutoRefresh(refreshSec, () => {
    void fetchLogs(true)
  })

  const toggleSelect = useCallback(
    (log: ChatLog) => {
      setSelected((prev) => {
        const next = new Map(prev)
        if (next.has(log.ID)) {
          next.delete(log.ID)
          return next
        }
        if (next.size >= MAX_COMPARE) {
          // 达到上限时明确告知，而不是静默忽略点击
          toast.warning(t("compare.limit", { max: MAX_COMPARE }))
          return prev
        }
        next.set(log.ID, log)
        return next
      })
    },
    [t]
  )

  const compareIds = useMemo(() => [...selected.keys()].join(","), [selected])

  const handleCleanLogs = async () => {
    const value = parseInt(cleanValue, 10)
    if (Number.isNaN(value) || value <= 0) return

    setCleanLoading(true)
    try {
      const result = await cleanLogs({ type: cleanType, value })
      toast.success(t("clean.success", { count: result.deleted_count }))
      void fetchLogs()
    } catch {
      toast.error(t("clean.failed"))
    } finally {
      setCleanLoading(false)
      setIsCleanDialogOpen(false)
    }
  }

  /** 只有成功请求可参与对比——失败请求没有可比较的输出内容。 */
  const selectable = (log: ChatLog) => log.Status === "success"
  const allOnPageSelected = logs.length > 0 && logs.filter(selectable).every((l) => selected.has(l.ID))

  return (
    <div className="flex h-full min-h-0 flex-col gap-2 p-1">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="mr-auto text-xl font-semibold tracking-tight">{t("title")}</h2>

        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder={t("id_placeholder")}
            value={idFilter}
            onChange={(e) => setFilter("id", e.target.value)}
            className="h-8 w-40 pl-7 text-xs"
          />
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder={t("trace_id_placeholder")}
            value={traceIdFilter}
            onChange={(e) => setFilter("traceId", e.target.value)}
            className="h-8 w-44 pl-7 text-xs"
          />
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder={t("session_id_placeholder")}
            value={sessionIdFilter}
            onChange={(e) => setFilter("sessionId", e.target.value)}
            className="h-8 w-44 pl-7 text-xs"
          />
        </div>
      </div>

      {/* 筛选行在内容之上一整行，作用于其下的表格与分页。
          六个维度都是**多选**：看日志时的诉求多数是"这几个供应商/模型放在一起看"，
          单选时代只能一个一个来回切，切一次还得重看一遍上下文。 */}
      <div className="flex flex-wrap items-center gap-2">
        {/*
          两个"名字"维度必须分开：请求名是客户端请求体里写的（本地别名表），
          模型是真正发给上游的 id（取自历史日志）。同名可跨提供商，
          两者的取值集合不同源，混在一个下拉里就会筛到对不上的东西。
        */}
        <DimensionFilter
          label={t("filters.request_name")}
          selected={multiFilter.requestName}
          onToggle={(v) => toggleMultiFilter("requestName", v)}
          options={models.map((m) => ({ value: m.Name, label: m.Name }))}
        />
        <DimensionFilter
          label={t("filters.provider_model")}
          selected={multiFilter.providerModel}
          onToggle={(v) => toggleMultiFilter("providerModel", v)}
          options={providerModels.map((m) => ({ value: m, label: m }))}
        />
        <DimensionFilter
          label={t("filters.project")}
          selected={multiFilter.authKey}
          onToggle={(v) => toggleMultiFilter("authKey", v)}
          options={authKeys.map((k) => ({ value: String(k.id), label: k.name }))}
        />
        <DimensionFilter
          label={t("filters.status")}
          selected={multiFilter.status}
          onToggle={(v) => toggleMultiFilter("status", v)}
          options={[
            { value: "success", label: t("common:status.success") },
            { value: "running", label: t("common:status.running") },
            { value: "error", label: t("common:status.error") },
          ]}
        />
        <DimensionFilter
          label={t("filters.type")}
          selected={multiFilter.style}
          onToggle={(v) => toggleMultiFilter("style", v)}
          options={availableStyles.map((s) => ({ value: s, label: s }))}
        />
        <DimensionFilter
          label={t("filters.provider")}
          selected={multiFilter.providerName}
          onToggle={(v) => toggleMultiFilter("providerName", v)}
          options={providers.map((p) => ({ value: p.Name, label: p.Name }))}
        />

        <div className="ml-auto flex items-center gap-2">
          <RefreshSelect
            value={refreshSec}
            options={LOGS_REFRESH_SECONDS}
            onChange={(s) => setPref(withRefreshSec(uiPrefs, "logs", s))}
          />

          <Popover>
            <PopoverTrigger asChild>
              <Button variant="outline" size="icon" className="size-8" aria-label={t("table.columns")} title={t("table.columns")}>
                <Columns3 className="size-4" />
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-64 p-3" align="end">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-xs font-medium">{t("table.columns")}</span>
                <button
                  type="button"
                  className="text-xs text-muted-foreground hover:text-foreground"
                  onClick={() => setPref(withLogsColumns(uiPrefs, [...LOG_DEFAULT_VISIBLE_IDS], LOG_TOGGLEABLE_IDS))}
                >
                  {t("table.reset_columns")}
                </button>
              </div>
              <div className="space-y-1">
                {LOG_COLUMNS.filter((c) => !c.always && c.labelKey).map((c) => {
                  const checked = uiPrefs.logsColumns.length === 0
                    ? c.defaultVisible === true
                    : uiPrefs.logsColumns.includes(c.id)
                  return (
                    <label key={c.id} className="flex items-center gap-2 text-sm cursor-pointer">
                      <Checkbox
                        checked={checked}
                        onCheckedChange={(v) => {
                          const current = uiPrefs.logsColumns.length === 0
                            ? LOG_DEFAULT_VISIBLE_IDS
                            : uiPrefs.logsColumns
                          const next = v
                            ? [...current, c.id]
                            : current.filter((id) => id !== c.id)
                          setPref(withLogsColumns(uiPrefs, next, LOG_TOGGLEABLE_IDS))
                        }}
                      />
                      <span>{t(c.labelKey as never)}</span>
                    </label>
                  )
                })}
              </div>
            </PopoverContent>
          </Popover>

          {hasActiveFilter && (
            <Button variant="ghost" size="sm" className="h-8 gap-1 text-xs" onClick={clearFilters}>
              <X className="size-3.5" aria-hidden="true" />
              {t("filters.clear")}
            </Button>
          )}

          <Button
            onClick={() => void fetchLogs()}
            variant="outline"
            size="icon"
            className="size-8"
            aria-label={t("common:actions.refresh")}
            title={t("common:actions.refresh")}
          >
            <RefreshCw className="size-4" />
          </Button>
          <Button
            onClick={() => setIsCleanDialogOpen(true)}
            variant="outline"
            size="icon"
            className="size-8"
            aria-label={t("clean.tooltip")}
            title={t("clean.tooltip")}
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      </div>

      {/* 已经有行时失败不清屏——保留上一次的结果，但要说清"这次没取到"，
          否则翻页失败后页码变了、行还是上一页的，读者会以为那是新一页。 */}
      {loadError && logs.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-status-critical/40 bg-status-critical/5 px-3 py-2 text-sm">
          <AlertTriangle className="size-4 shrink-0 text-status-critical-ink" aria-hidden="true" />
          <span className="text-status-critical-ink">{t("error.stale")}</span>
          <span className="reading min-w-0 flex-1 truncate text-xs text-muted-foreground" title={loadError}>
            {loadError}
          </span>
          <Button variant="outline" size="sm" className="h-7" onClick={() => void fetchLogs()}>
            {t("retry")}
          </Button>
        </div>
      )}

      {selected.size > 0 && (
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-border bg-accent/40 px-3 py-2">
          <span className="text-sm">
            {t("compare.selected", { count: selected.size, max: MAX_COMPARE })}
          </span>
          <div className="flex flex-wrap gap-1.5">
            {[...selected.values()].map((l) => (
              <span
                key={l.ID}
                className="inline-flex items-center gap-1 rounded-sm bg-background px-1.5 py-0.5 text-xs"
              >
                <span className="reading text-muted-foreground">#{l.ID}</span>
                <span className="max-w-[120px] truncate" title={l.Name}>
                  {l.Name}
                </span>
                <button
                  type="button"
                  className="rounded-sm text-muted-foreground hover:text-foreground"
                  onClick={() => toggleSelect(l)}
                  aria-label={t("compare.remove", { id: l.ID })}
                >
                  <X className="size-3" aria-hidden="true" />
                </button>
              </span>
            ))}
          </div>
          <div className="ml-auto flex gap-2">
            <Button variant="ghost" size="sm" className="h-8 text-xs" onClick={() => setSelected(new Map())}>
              {t("compare.clear")}
            </Button>
            <Button
              size="sm"
              className="h-8 gap-1.5 text-xs"
              onClick={() => navigate(`/compare?ids=${compareIds}`)}
            >
              <GitCompareArrows className="size-3.5" aria-hidden="true" />
              {t("compare.open")}
            </Button>
          </div>
        </div>
      )}

      <div className="min-h-0 flex-1 rounded-md border border-border bg-background">
        <div className="h-full overflow-auto">
          <Table className="min-w-[1400px]">
            <TableHeader className="sticky top-0 z-10 bg-muted">
              <TableRow className="hover:bg-muted">
                {cols.map((col) => (
                  <TableHead key={col.id} className={cn(col.headClass)}>
                    {col.id === "select" ? (
                      <Checkbox
                        checked={allOnPageSelected}
                        onCheckedChange={(v) => {
                          if (v) {
                            setSelected((prev) => {
                              const next = new Map(prev)
                              for (const l of logs) {
                                if (selectable(l) && next.size < MAX_COMPARE) next.set(l.ID, l)
                              }
                              return next
                            })
                          } else {
                            setSelected((prev) => {
                              const next = new Map(prev)
                              for (const l of logs) next.delete(l.ID)
                              return next
                            })
                          }
                        }}
                        aria-label={t("compare.select_all")}
                      />
                    ) : col.labelKey ? (
                      t(col.labelKey as never)
                    ) : null}
                  </TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {logs.map((log) => {
                const isSelected = selected.has(log.ID)
                return (
                  <TableRow
                    key={log.ID}
                    data-state={isSelected ? "selected" : undefined}
                    className={cn("cursor-pointer", isSelected && "bg-accent/50")}
                    onClick={() => setDetailLog(log)}
                  >
                    {cols.map((col) => {
                      if (col.id === "select") {
                        return (
                          <TableCell key={col.id} onClick={(e) => e.stopPropagation()}>
                            <Checkbox
                              checked={isSelected}
                              disabled={!selectable(log)}
                              onCheckedChange={() => toggleSelect(log)}
                              aria-label={t("compare.select_row", { id: log.ID })}
                            />
                          </TableCell>
                        )
                      }
                      if (col.id === "actions") {
                        return (
                          <TableCell key={col.id} onClick={(e) => e.stopPropagation()}>
                            <div className="flex gap-1">
                              <Button
                                variant="ghost"
                                size="icon"
                                className="size-7"
                                onClick={() => setDetailLog(log)}
                                aria-label={t("detail.title", { id: log.ID })}
                                title={t("table.detail")}
                              >
                                <Eye className="size-3.5" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon"
                                className="size-7"
                                onClick={() => navigate(`/logs/${log.ID}/chat-io`)}
                                disabled={!canViewChatIO(log)}
                                aria-label={t("table.view_io")}
                                title={canViewChatIO(log) ? t("table.view_io") : t("table.io_unavailable")}
                              >
                                <MessageSquare className="size-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        )
                      }
                      const value = col.value?.(log) ?? ""
                      const titleAttr = col.cellClass?.includes("truncate") ? value || undefined : undefined
                      if (col.kind === "status") {
                        return (
                          <TableCell key={col.id} className={cn(col.cellClass)}>
                            <StatusMark status={log.Status} label={t(`common:status.${statusKey(log.Status)}` as never)} />
                          </TableCell>
                        )
                      }
                      if (col.kind === "cache") {
                        const rate = cacheRate(log)
                        return (
                          <TableCell key={col.id} className={cn(col.cellClass)}>
                            {rate === null ? (
                              <span className="text-muted-foreground">—</span>
                            ) : (
                              t("table.cache_value", { percent: rate.toFixed(0), tokens: compactNumber(log.prompt_tokens_details?.cached_tokens ?? 0) })
                            )}
                          </TableCell>
                        )
                      }
                      if (col.kind === "role") {
                        return (
                          <TableCell key={col.id} className={cn(col.cellClass)}>
                            {t(log.Backup ? "role.backup" : "role.primary")}
                          </TableCell>
                        )
                      }
                      return (
                        <TableCell key={col.id} className={cn(col.cellClass)} title={titleAttr}>
                          {value}
                        </TableCell>
                      )
                    })}
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>

          {loading && (
            <div role="status" aria-busy="true" className="p-8 text-center text-sm text-muted-foreground">
              {t("loading")}
            </div>
          )}
          {!loading && loadError && logs.length === 0 && (
            /* 失败用失败态而不是空态：空态那句"暂无请求日志"是关于服务端事实的
               断言，不该由一次失败的请求替它说 */
            <ErrorState
              title={t("error.load")}
              message={loadError}
              retryLabel={t("retry")}
              onRetry={() => void fetchLogs()}
            />
          )}
          {!loading && !loadError && logs.length === 0 && (
            <div className="flex flex-col items-center gap-1 p-16 text-center">
              <p className="text-sm font-medium">{t("no_data")}</p>
              {hasActiveFilter && (
                <button type="button" className="text-xs text-primary underline-offset-2 hover:underline" onClick={clearFilters}>
                  {t("filters.clear")}
                </button>
              )}
            </div>
          )}
        </div>
      </div>

      <div className="flex flex-shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border pt-2">
        <div className="text-sm whitespace-nowrap text-muted-foreground">
          {t("common:pagination.summary", { total, page, pages })}
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <Select value={String(pageSize)} onValueChange={(v) => patchParams({ pageSize: Number(v), page: 1 })}>
            <SelectTrigger className="h-8 w-[92px] text-xs" aria-label={t("common:pagination.per_page")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {[10, 20, 50, 100].map((size) => (
                <SelectItem key={size} value={String(size)}>
                  {size}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="icon"
              className="size-8"
              onClick={() => patchParams({ page: page - 1 })}
              disabled={page <= 1}
              aria-label={t("common:pagination.prev")}
            >
              <ChevronLeft className="size-4" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="size-8"
              onClick={() => patchParams({ page: page + 1 })}
              disabled={page >= pages}
              aria-label={t("common:pagination.next")}
            >
              <ChevronRight className="size-4" />
            </Button>
          </div>
        </div>
      </div>

      <LogDetailSheet log={detailLog} onClose={() => setDetailLog(null)} />

      <Dialog open={isCleanDialogOpen} onOpenChange={setIsCleanDialogOpen}>
        {/* 只有标题没有说明文字，显式声明"没有描述"：不写的话 Radix 会
            在控制台告警，而告警多了就没人看了。 */}
        <DialogContent className="w-[92vw] sm:max-w-md" aria-describedby={undefined}>
          <DialogHeader>
            <DialogTitle>{t("clean.title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="flex gap-2">
              {(["count", "days"] as const).map((type) => (
                <Button
                  key={type}
                  variant={cleanType === type ? "default" : "outline"}
                  size="sm"
                  className="flex-1"
                  onClick={() => {
                    setCleanType(type)
                    setCleanValue(type === "count" ? "1000" : "30")
                  }}
                >
                  {type === "count" ? t("clean.by_count") : t("clean.by_days")}
                </Button>
              ))}
            </div>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min="1"
                value={cleanValue}
                onChange={(e) => setCleanValue(e.target.value)}
                placeholder={cleanType === "count" ? t("clean.count_placeholder") : t("clean.days_placeholder")}
              />
              <span className="text-sm whitespace-nowrap text-muted-foreground">
                {cleanType === "count" ? t("clean.count_unit") : t("clean.days_unit")}
              </span>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setIsCleanDialogOpen(false)}>
              {t("clean.cancel")}
            </Button>
            <Button
              variant="destructive"
              onClick={handleCleanLogs}
              disabled={cleanLoading || !cleanValue || parseInt(cleanValue, 10) <= 0}
            >
              {cleanLoading ? t("clean.confirming") : t("clean.confirm")}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

/**
 * 一个多选筛选维度。
 *
 * 共享组件只认字符串，翻译留在这一层（本页的文案在 `logs` 命名空间里）。
 * 触发器上的无障碍名字带上已选计数：`getByRole("button", {name})` 因此能
 * 区分"供应商"和"供应商（已选 2 个）"，读屏用户也听得到筛了几项。
 */
function DimensionFilter({
  label,
  selected,
  onToggle,
  options,
}: {
  label: string
  selected: string[]
  onToggle: (value: string) => void
  options: FilterOption[]
}) {
  const { t } = useTranslation("logs")
  return (
    <MultiSelectFilter
      label={label}
      options={options}
      selected={selected}
      onToggle={onToggle}
      emptyText={t("filters.empty")}
      ariaLabel={
        selected.length > 0
          ? t("filters.active", { label, count: selected.length })
          : label
      }
    />
  )
}
