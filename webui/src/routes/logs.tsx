import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { ChevronLeft, ChevronRight, Eye, GitCompareArrows, MessageSquare, RefreshCw, Search, Trash2, X } from "lucide-react"

import { StatusMark } from "@/components/status-mark"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  cleanLogs,
  getAuthKeysList,
  getLogs,
  getModelOptions,
  getProviderTemplates,
  getProviders,
  type AuthKeyItem,
  type ChatLog,
  type Model,
  type Provider,
} from "@/lib/api"
import { compactNumber, formatBytes, formatCost, formatDurationNs, formatFull, formatNumber } from "@/lib/format"
import { cn } from "@/lib/utils"

/** 对比上限。与后端一次返回的详情体积、以及人眼能同时比较的条数都有关。 */
const MAX_COMPARE = 6

/** 自动刷新档位（秒）。0 表示关闭。 */
const REFRESH_OPTIONS = [0, 5, 10, 30, 60]

/** 请求状态到 i18n 键的映射。后端的 status 是固定的三个英文值。 */
function statusKey(status: string): "success" | "error" | "running" | "unknown" {
  return status === "success" || status === "error" || status === "running" ? status : "unknown"
}

/** 缓存命中率（%）。输入为 0 时返回 null，表示"不适用"而非 0%。 */
function cacheRate(log: ChatLog): number | null {
  const prompt = log.prompt_tokens ?? 0
  if (prompt <= 0) return null
  return ((log.prompt_tokens_details?.cached_tokens ?? 0) / prompt) * 100
}

export default function LogsPage() {
  const { t } = useTranslation(["logs", "common"])
  const navigate = useNavigate()

  const [logs, setLogs] = useState<ChatLog[]>([])
  const [loading, setLoading] = useState(true)
  const [total, setTotal] = useState(0)
  const [pages, setPages] = useState(0)
  const [providers, setProviders] = useState<Provider[]>([])
  const [models, setModels] = useState<Model[]>([])
  const [authKeys, setAuthKeys] = useState<AuthKeyItem[]>([])
  const [availableStyles, setAvailableStyles] = useState<string[]>([])

  // 筛选与分页状态同步到 URL，浏览器返回时自动恢复，链接也可分享
  const [searchParams, setSearchParams] = useSearchParams()
  const page = Math.max(1, Number(searchParams.get("page")) || 1)
  const pageSize = Math.max(1, Number(searchParams.get("pageSize")) || 20)
  const providerNameFilter = searchParams.get("providerName") ?? "all"
  const modelFilter = searchParams.get("model") ?? "all"
  const statusFilter = searchParams.get("status") ?? "all"
  const styleFilter = searchParams.get("style") ?? "all"
  const authKeyFilter = searchParams.get("authKey") ?? "all"
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
            const isDefault =
              s === "" ||
              (["providerName", "model", "status", "style", "authKey"].includes(k) && s === "all") ||
              (k === "page" && s === "1") ||
              (k === "pageSize" && s === "20")
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

  const hasActiveFilter =
    providerNameFilter !== "all" ||
    modelFilter !== "all" ||
    statusFilter !== "all" ||
    styleFilter !== "all" ||
    authKeyFilter !== "all" ||
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

  // 自动刷新
  const [refreshSec, setRefreshSec] = useState(0)
  const silentRef = useRef(false)

  const fetchOptions = useCallback(async () => {
    // 筛选下拉的选项来源与日志本身无关，失败只影响可选范围，不阻断列表
    const [p, m, a, tpl] = await Promise.allSettled([
      getProviders(),
      getModelOptions(),
      getAuthKeysList(),
      getProviderTemplates(),
    ])
    if (p.status === "fulfilled") setProviders(p.value)
    if (m.status === "fulfilled") setModels(m.value)
    if (a.status === "fulfilled") setAuthKeys(a.value)
    if (tpl.status === "fulfilled") setAvailableStyles(tpl.value.map((x) => x.type))
  }, [])

  const fetchLogs = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true)
      try {
        const result = await getLogs(page, pageSize, {
          providerName: providerNameFilter === "all" ? undefined : providerNameFilter,
          name: modelFilter === "all" ? undefined : modelFilter,
          status: statusFilter === "all" ? undefined : statusFilter,
          style: styleFilter === "all" ? undefined : styleFilter,
          authKeyId: authKeyFilter === "all" ? undefined : authKeyFilter,
          traceId: traceIdFilter.trim() || undefined,
          sessionId: sessionIdFilter.trim() || undefined,
          id: idFilter.trim() || undefined,
        })
        setLogs(result.data)
        setTotal(result.total)
        setPages(result.pages)
        // 静默刷新失败不弹错：自动刷新期间的偶发失败不值得打断用户
      } catch (err) {
        if (!silent) {
          toast.error(err instanceof Error ? err.message : String(err))
        }
      } finally {
        if (!silent) setLoading(false)
      }
    },
    [page, pageSize, providerNameFilter, modelFilter, statusFilter, styleFilter, authKeyFilter, traceIdFilter, sessionIdFilter, idFilter]
  )

  useEffect(() => {
    void fetchOptions()
  }, [fetchOptions])

  useEffect(() => {
    silentRef.current = false
    void fetchLogs()
  }, [fetchLogs])

  // 自动刷新：标签页隐藏时跳过（省资源），回到前台立即补一次
  useEffect(() => {
    if (refreshSec <= 0) return
    const tick = () => {
      if (document.hidden) return
      silentRef.current = true
      void fetchLogs(true)
    }
    const timer = window.setInterval(tick, refreshSec * 1000)
    const onVisible = () => {
      if (!document.hidden) tick()
    }
    document.addEventListener("visibilitychange", onVisible)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener("visibilitychange", onVisible)
    }
  }, [refreshSec, fetchLogs])

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

  const canViewChatIO = (log: ChatLog) => log.Status === "success" && log.ChatIO

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

      {/* 筛选行在内容之上一整行，作用于其下的表格与分页 */}
      <div className="flex flex-wrap items-end gap-2">
        <FilterSelect
          label={t("filters.model")}
          value={modelFilter}
          onChange={(v) => setFilter("model", v)}
          options={models.map((m) => ({ value: m.Name, label: m.Name }))}
        />
        <FilterSelect
          label={t("filters.project")}
          value={authKeyFilter}
          onChange={(v) => setFilter("authKey", v)}
          options={authKeys.map((k) => ({ value: String(k.id), label: k.name }))}
        />
        <FilterSelect
          label={t("filters.status")}
          value={statusFilter}
          onChange={(v) => setFilter("status", v)}
          options={[
            { value: "success", label: t("common:status.success") },
            { value: "running", label: t("common:status.running") },
            { value: "error", label: t("common:status.error") },
          ]}
        />
        <FilterSelect
          label={t("filters.type")}
          value={styleFilter}
          onChange={(v) => setFilter("style", v)}
          options={availableStyles.map((s) => ({ value: s, label: s }))}
        />
        <FilterSelect
          label={t("filters.provider")}
          value={providerNameFilter}
          onChange={(v) => setFilter("providerName", v)}
          options={providers.map((p) => ({ value: p.Name, label: p.Name }))}
        />

        <div className="ml-auto flex items-center gap-2">
          <div className="flex items-center gap-1.5">
            <RefreshCw className="size-3.5 text-muted-foreground" aria-hidden="true" />
            <Select value={String(refreshSec)} onValueChange={(v) => setRefreshSec(Number(v))}>
              <SelectTrigger className="h-8 w-[104px] text-xs" aria-label={t("auto_refresh.label")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {REFRESH_OPTIONS.map((s) => (
                  <SelectItem key={s} value={String(s)}>
                    {s === 0 ? t("auto_refresh.off") : t("auto_refresh.every", { sec: s })}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

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
                <TableHead className="w-10">
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
                </TableHead>
                {/* 冻结 ID 列：横向滚动时保持可见，否则右移后就认不出是哪条 */}
                <TableHead className="sticky left-0 z-20 w-20 bg-muted">{t("table.id")}</TableHead>
                <TableHead>{t("table.time")}</TableHead>
                <TableHead>{t("table.model")}</TableHead>
                <TableHead>{t("table.project")}</TableHead>
                <TableHead>{t("table.status")}</TableHead>
                <TableHead>{t("table.tokens")}</TableHead>
                <TableHead>{t("table.cache")}</TableHead>
                <TableHead>{t("table.size")}</TableHead>
                <TableHead>{t("table.duration")}</TableHead>
                <TableHead>{t("table.provider_model")}</TableHead>
                <TableHead>{t("table.type")}</TableHead>
                <TableHead>{t("table.provider")}</TableHead>
                <TableHead className="w-24">{t("table.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {logs.map((log) => {
                const rate = cacheRate(log)
                const isSelected = selected.has(log.ID)
                return (
                  <TableRow
                    key={log.ID}
                    data-state={isSelected ? "selected" : undefined}
                    className={cn("cursor-pointer", isSelected && "bg-accent/50")}
                    onClick={() => setDetailLog(log)}
                  >
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <Checkbox
                        checked={isSelected}
                        disabled={!selectable(log)}
                        onCheckedChange={() => toggleSelect(log)}
                        aria-label={t("compare.select_row", { id: log.ID })}
                      />
                    </TableCell>
                    <TableCell className="reading sticky left-0 z-10 bg-background text-xs text-muted-foreground">
                      {log.ID}
                    </TableCell>
                    <TableCell className="reading whitespace-nowrap text-xs text-muted-foreground">
                      {formatFull(new Date(log.CreatedAt).getTime())}
                    </TableCell>
                    <TableCell className="max-w-[160px] truncate font-medium" title={log.Name}>
                      {log.Name}
                    </TableCell>
                    <TableCell className="max-w-[120px] truncate text-xs" title={log.key_name}>
                      {log.key_name || "-"}
                    </TableCell>
                    <TableCell>
                      <StatusMark status={log.Status} label={t(`common:status.${statusKey(log.Status)}` as never)} />
                    </TableCell>
                    <TableCell className="reading">{formatNumber(log.total_tokens)}</TableCell>
                    <TableCell className="reading text-xs">
                      {rate === null ? (
                        <span className="text-muted-foreground">—</span>
                      ) : (
                        t("table.cache_value", { percent: rate.toFixed(0), tokens: compactNumber(log.prompt_tokens_details?.cached_tokens ?? 0) })
                      )}
                    </TableCell>
                    <TableCell className="reading text-xs text-muted-foreground">
                      {log.Size ? formatBytes(log.Size) : "-"}
                    </TableCell>
                    <TableCell className="reading text-xs">
                      {formatDurationNs(log.ChunkTime + log.FirstChunkTime + log.ProxyTime)}
                    </TableCell>
                    <TableCell className="max-w-[140px] truncate text-xs" title={log.ProviderModel}>
                      {log.ProviderModel}
                    </TableCell>
                    <TableCell className="text-xs">{log.Style}</TableCell>
                    <TableCell className="max-w-[120px] truncate text-xs" title={log.ProviderName}>
                      {log.ProviderName}
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
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
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>

          {loading && (
            <div className="p-8 text-center text-sm text-muted-foreground">{t("loading")}</div>
          )}
          {!loading && logs.length === 0 && (
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
        <DialogContent className="w-[92vw] sm:max-w-md">
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

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  options: { value: string; label: string }[]
}) {
  const { t } = useTranslation("common")
  return (
    <div className="flex flex-col gap-1">
      <Label className="text-[11px] tracking-wide text-muted-foreground uppercase">{label}</Label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger className="h-8 w-[132px] px-2 text-xs">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">{t("status.all")}</SelectItem>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

function DetailField({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] tracking-wide text-muted-foreground uppercase">{label}</div>
      <div className={cn("text-sm break-words", mono && "reading text-xs")}>{value ?? "-"}</div>
    </div>
  )
}

/**
 * 单条日志详情。
 *
 * 用右侧抽屉而非弹窗：读者要在列表与详情之间来回比对（看这条是不是刚才那条），
 * 遮住整页反而妨碍操作。关闭后焦点由 Radix 归还到触发行。
 */
function LogDetailSheet({ log, onClose }: { log: ChatLog | null; onClose: () => void }) {
  const { t } = useTranslation(["logs", "common"])
  const navigate = useNavigate()

  const cached = log?.prompt_tokens_details?.cached_tokens ?? 0
  const hasPricing = (log?.input_price ?? 0) > 0 || (log?.output_price ?? 0) > 0
  const totalCost = log
    ? (Math.max(0, (log.prompt_tokens ?? 0) - cached) / 1e6) * (log.input_price ?? 0) +
      (cached / 1e6) * (log.cache_read_price ?? 0) +
      ((log.completion_tokens ?? 0) / 1e6) * (log.output_price ?? 0)
    : 0

  return (
    <Sheet open={log !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{log ? t("logs:detail.title", { id: log.ID }) : ""}</SheetTitle>
          {log && (
            <SheetDescription className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <StatusMark status={log.Status} label={t(`common:status.${statusKey(log.Status)}` as never)} />
              <span className="reading">{formatFull(new Date(log.CreatedAt).getTime())}</span>
              <span>{log.Name}</span>
            </SheetDescription>
          )}
        </SheetHeader>

        {log && (
          <SheetBody className="space-y-5">
            {log.Error && (
              <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3">
                <p className="mb-1 text-[11px] tracking-wide text-status-critical-ink uppercase">
                  {t("logs:detail.error_title")}
                </p>
                <div className="text-sm break-words whitespace-pre-wrap text-status-critical-ink">{log.Error}</div>
              </div>
            )}

            <Section title={t("logs:detail.basic_info")}>
              <div className="grid grid-cols-2 gap-3">
                <DetailField label={t("logs:detail.model_name")} value={log.Name} />
                <DetailField label={t("logs:detail.provider")} value={log.ProviderName || "-"} />
                <DetailField label={t("logs:detail.provider_model")} value={log.ProviderModel || "-"} mono />
                <DetailField label={t("logs:detail.type")} value={log.Style || "-"} />
                <DetailField label={t("logs:detail.size")} value={log.Size ? formatBytes(log.Size) : "-"} />
                <DetailField label={t("logs:detail.remote_ip")} value={log.RemoteIP || "-"} mono />
                <DetailField
                  label={t("logs:detail.io_log")}
                  value={log.ChatIO ? t("logs:detail.io_yes") : t("logs:detail.io_no")}
                />
                <DetailField label={t("logs:detail.retry")} value={log.Retry ?? 0} />
                <DetailField
                  label={t("logs:detail.trace_id")}
                  value={log.TraceID ? <span className="reading break-all">{log.TraceID}</span> : "-"}
                />
                <DetailField
                  label={t("logs:detail.session_id")}
                  value={log.SessionID ? <span className="reading break-all">{log.SessionID}</span> : "-"}
                />
              </div>
              <div className="mt-3">
                <DetailField label={t("logs:detail.user_agent")} value={log.UserAgent || "-"} mono />
              </div>
            </Section>

            <Section title={t("logs:detail.performance")}>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <DetailField label={t("logs:detail.proxy_time")} value={formatDurationNs(log.ProxyTime)} />
                <DetailField label={t("logs:detail.first_chunk_time")} value={formatDurationNs(log.FirstChunkTime)} />
                <DetailField label={t("logs:detail.chunk_time")} value={formatDurationNs(log.ChunkTime)} />
                <DetailField label={t("logs:detail.tps")} value={log.Tps ? log.Tps.toFixed(2) : "-"} />
              </div>
            </Section>

            <Section title={t("logs:detail.token_usage")}>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <DetailField label={t("logs:detail.input")} value={formatNumber(log.prompt_tokens)} />
                <DetailField label={t("logs:detail.cached")} value={formatNumber(cached)} />
                <DetailField label={t("logs:detail.output")} value={formatNumber(log.completion_tokens)} />
                <DetailField label={t("logs:detail.total")} value={formatNumber(log.total_tokens)} />
              </div>
            </Section>

            <Section title={t("logs:detail.billing")}>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <DetailField
                  label={t("logs:detail.billing_input")}
                  value={hasPricing ? formatCost((Math.max(0, log.prompt_tokens - cached) / 1e6) * log.input_price, log.currency) : "-"}
                />
                <DetailField
                  label={t("logs:detail.billing_cache")}
                  value={hasPricing ? formatCost((cached / 1e6) * log.cache_read_price, log.currency) : "-"}
                />
                <DetailField
                  label={t("logs:detail.billing_output")}
                  value={hasPricing ? formatCost((log.completion_tokens / 1e6) * log.output_price, log.currency) : "-"}
                />
                <DetailField
                  label={t("logs:detail.billing_total")}
                  value={hasPricing ? formatCost(totalCost, log.currency) : "-"}
                />
              </div>
            </Section>

            {canView(log) && (
              <Button variant="outline" size="sm" className="w-full" onClick={() => navigate(`/logs/${log.ID}/chat-io`)}>
                <MessageSquare className="mr-1.5 size-3.5" aria-hidden="true" />
                {t("logs:table.view_io")}
              </Button>
            )}
          </SheetBody>
        )}
      </SheetContent>
    </Sheet>
  )
}

function canView(log: ChatLog) {
  return log.Status === "success" && log.ChatIO
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{title}</h3>
      {children}
    </section>
  )
}
