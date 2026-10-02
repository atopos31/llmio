import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, Database, Loader2, Pause, Play, RotateCcw } from "lucide-react"
import { toast } from "sonner"

import { ErrorState, ListSkeleton } from "@/components/state-views"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Meter } from "@/components/ui/meter"
import { Switch } from "@/components/ui/switch"
import {
  getCompression,
  pauseCompression,
  rollbackCompression,
  runCompression,
  updateCompressionPolicy,
  type CompressionStatus,
} from "@/lib/api"
import { formatBytes } from "@/lib/format"
import { cn } from "@/lib/utils"

/**
 * 数据库压缩（历史行的形态迁移）的控制台卡片。
 *
 * 卡片自己取数，理由与峰谷日历那张一样：它走 `/logs/compression` 这一组独立
 * 端点，而配置页其余两张卡走通用 config 端点——混进那次 `Promise.all`，
 * 一处读失败会把整页说成"读取现有配置失败"，而另外两张其实是好的。
 *
 * ## 这一页必须同时给出**两个**数字
 *
 * "省了多少"有两层账，混起来会得出错一个数量级的结论：
 *
 *   - **行内字节**：迁移把 470 KiB 的明文行换成几十字节的引用帧。这个比值
 *     大得离谱（真机 1714x），因为它把"内容搬进了块表"这件事算成了"省了"。
 *   - **真正落库**：input 列 + 组表 + 块表。这才是库变小了多少（真机 93.95x）。
 *
 * 而且即便后者也不等于"文件变小了"：`auto_vacuum=0` 时迁移只把页还进
 * freelist，**文件一字节都不会缩**，要 VACUUM 才行。所以 frelist 那行也在这张
 * 卡上——不写它，用户看完"省了 5.6 GiB"再去看文件还是 7 GiB，会以为这功能是坏的。
 *
 * ## 轮询
 *
 * 迁移是分钟级的后台任务，没有推进事件可听，只能轮询。但**只在真有人看着
 * 并且确实在跑时才轮询**：状态接口里的 `pending_rows` 是全表扫 typeof，
 * 挂一个 2 秒的常驻轮询等于让配置页一直打全表。
 */

/** 迁移在跑时的轮询间隔。够密到进度条能动，够疏到不把全表扫打满。 */
const RUNNING_POLL_MS = 2000

/**
 * 块表每行的估算开销，与后端 `rpBlockRowBytes` 同值。
 *
 * 是估的：块表没有存 orig_len 这一列，精确值要 `SUM(payload)`，那是一次
 * 全表读。它翻一倍也只多一 MiB 量级，不改变结论——但界面上要说明这是估算。
 */
const BLOCK_ROW_BYTES = 40

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export function CompressionCard() {
  const { t } = useTranslation(["config", "common"])
  const [status, setStatus] = useState<CompressionStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [editOpen, setEditOpen] = useState(false)
  const [runOpen, setRunOpen] = useState(false)
  const [rollbackOpen, setRollbackOpen] = useState(false)

  /**
   * `watching` 是"有一轮任务在飞、界面得盯着它"。
   *
   * 它不能直接等于 `status.running`：点完「开始」到后端那个 goroutine 真正
   * 起来之间有一个空档（HTTP 已经返回、CAS 还没置上），只看 running 的话
   * 轮询根本挂不上，进度条会一直停在原地。所以由动作自己把它置起来，
   * 再由"看到它不再是 running"这个条件把它放下去。
   */
  const [watching, setWatching] = useState(false)

  const load = useCallback(async (): Promise<CompressionStatus | null> => {
    setLoadError(null)
    try {
      const next = await getCompression()
      setStatus(next)
      return next
    } catch (err) {
      setLoadError(errorText(err))
      return null
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load().then((next) => {
      // 上一次进程被 kill 时库里可能还留着 running：那种情况不该让界面
      // 一直转圈，但也不该装作没事——状态徽标照实显示 running，
      // 只是不轮询（后端 in-flight 为 false 就说明没人在跑）。
      if (next?.running) setWatching(true)
    })
  }, [load])

  // 轮询只在确实有任务在飞时挂着。常驻的话配置页会永远每 2 秒打一次
  // 全表 count（状态接口的 pending_rows 是整表扫 typeof）。
  useEffect(() => {
    if (!watching) return
    const id = window.setInterval(() => {
      void load().then((next) => {
        if (next && !next.running) setWatching(false)
      })
    }, RUNNING_POLL_MS)
    return () => window.clearInterval(id)
  }, [watching, load])

  const startRun = async (acknowledgeNoBackup: boolean) => {
    try {
      setBusy(true)
      await runCompression({ full: false, acknowledge_no_backup: acknowledgeNoBackup })
      setWatching(true)
      toast.success(t("compression.toast.started"))
      setRunOpen(false)
      await load()
    } catch (err) {
      toast.error(t("compression.toast.start_failed", { message: errorText(err) }))
    } finally {
      setBusy(false)
    }
  }

  const onPause = async () => {
    try {
      setBusy(true)
      await pauseCompression()
      setWatching(false)
      toast.success(t("compression.toast.paused"))
      await load()
    } catch (err) {
      toast.error(t("compression.toast.pause_failed", { message: errorText(err) }))
    } finally {
      setBusy(false)
    }
  }

  const onRollback = async () => {
    try {
      setBusy(true)
      await rollbackCompression()
      setWatching(true)
      toast.success(t("compression.toast.rollback_started"))
      setRollbackOpen(false)
      await load()
    } catch (err) {
      toast.error(t("compression.toast.rollback_failed", { message: errorText(err) }))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-sm font-medium">{t("compression.title")}</CardTitle>
          {status && <StatusBadge status={status} />}
        </div>
        <CardDescription className="text-[11px]">{t("compression.desc")}</CardDescription>
      </CardHeader>

      <CardContent className="space-y-4">
        {loading ? (
          <ListSkeleton label={t("common:loading")} rows={3} />
        ) : loadError ? (
          <ErrorState
            title={t("compression.load_failed")}
            message={loadError}
            retryLabel={t("retry")}
            onRetry={() => void load()}
          />
        ) : status ? (
          <CompressionBody status={status} onEdit={() => setEditOpen(true)} />
        ) : null}
      </CardContent>

      {status && !loadError && (
        <CardFooter className="flex flex-wrap gap-2">
          <Button
            onClick={() => {
              // 有可用备份就直接跑；没有就先弹确认。这道门在后端也有一份
              // （没确认就是 400），这里只是把"为什么被拦"提前说清楚。
              if (status.backup.source === "manual") void startRun(false)
              else setRunOpen(true)
            }}
            disabled={busy || status.running}
          >
            {status.running ? (
              <>
                <Loader2 className="size-4 animate-spin" />
                {t("compression.running")}
              </>
            ) : (
              <>
                <Play className="size-4" />
                {t("compression.start")}
              </>
            )}
          </Button>

          <Button
            variant="outline"
            onClick={onPause}
            disabled={busy || !status.running}
          >
            <Pause className="size-4" />
            {t("compression.pause")}
          </Button>

          <Button
            variant="outline"
            className="ml-auto text-status-critical-ink"
            onClick={() => setRollbackOpen(true)}
            disabled={busy || status.running || status.db.framed_rows === 0}
          >
            <RotateCcw className="size-4" />
            {t("compression.rollback")}
          </Button>
        </CardFooter>
      )}

      {status && (
        <PolicyDialog
          open={editOpen}
          onOpenChange={setEditOpen}
          status={status}
          onSaved={load}
        />
      )}

      {status && (
        <Dialog open={runOpen} onOpenChange={setRunOpen}>
          <DialogContent className="max-w-lg">
            <DialogHeader>
              <DialogTitle>{t("compression.no_backup_title")}</DialogTitle>
              <DialogDescription>{t("compression.no_backup_desc")}</DialogDescription>
            </DialogHeader>
            <div className="space-y-2 text-sm">
              <div className="flex items-start gap-2 rounded-md border border-status-warning/40 bg-status-warning/5 px-3 py-2">
                <AlertTriangle
                  className="mt-0.5 size-4 shrink-0 text-status-warning-ink"
                  aria-hidden="true"
                />
                <div className="min-w-0 space-y-1">
                  <p className="text-xs text-status-warning-ink">
                    {t(`compression.backup.${status.backup.source}` as never, {
                      defaultValue: status.backup.source,
                    })}
                  </p>
                  <p className="reading text-xs break-all text-muted-foreground">
                    {status.backup.path || "—"}
                  </p>
                </div>
              </div>
              <p className="text-xs text-muted-foreground">{t("compression.no_backup_hint")}</p>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setRunOpen(false)}>
                {t("common:actions.cancel")}
              </Button>
              <Button
                variant="destructive"
                onClick={() => void startRun(true)}
                disabled={busy}
              >
                {t("compression.no_backup_confirm")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      <Dialog open={rollbackOpen} onOpenChange={setRollbackOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("compression.rollback_title")}</DialogTitle>
            <DialogDescription>{t("compression.rollback_desc")}</DialogDescription>
          </DialogHeader>
          <div className="flex items-start gap-2 rounded-md border border-status-critical/40 bg-status-critical/5 px-3 py-2">
            <AlertTriangle
              className="mt-0.5 size-4 shrink-0 text-status-critical-ink"
              aria-hidden="true"
            />
            <p className="text-xs text-status-critical-ink">{t("compression.rollback_warn")}</p>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRollbackOpen(false)}>
              {t("common:actions.cancel")}
            </Button>
            <Button variant="destructive" onClick={() => void onRollback()} disabled={busy}>
              {t("compression.rollback_confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// 读数
// ---------------------------------------------------------------------------

function CompressionBody({
  status,
  onEdit,
}: {
  status: CompressionStatus
  onEdit: () => void
}) {
  const { t } = useTranslation(["config", "common"])
  const { state, db, policy, backup } = status

  // 真正落库 = input 列 + 组表 + 块表。块表那 40 字节/行是估算（见 BLOCK_ROW_BYTES）。
  const blockMetaBytes = db.block_rows * BLOCK_ROW_BYTES
  const storedBytes = db.input_column_bytes + db.block_group_bytes + blockMetaBytes

  // 原始大小只在**全部迁完**时才是 state.bytes_before：那个计数只覆盖扫过的行。
  // 还有没迁的行时报一个比值就是在替一个不完整的数下结论。
  const settled = db.pending_rows === 0 && state.bytes_before > 0
  const ratio = settled && storedBytes > 0 ? state.bytes_before / storedBytes : null

  // 进度分母：本轮要扫的总行数。没有候选行时（稳态）说"已完成"而不是 0/0。
  const total = state.total_rows
  const done = state.scanned
  const progress = total > 0 ? Math.min((done / total) * 100, 100) : 100

  // freelist 里那些页是"删了/改了但还没还给文件系统"的量。它就是
  // "省了这么多但文件没缩"的那段距离，所以拿它和文件大小并排说。
  const freelistBytes = db.freelist_count * db.page_size

  return (
    <div className="space-y-4">
      {/* 进度 */}
      <div className="space-y-1.5">
        <div className="flex flex-wrap items-baseline gap-x-2 text-xs">
          <span className="text-muted-foreground">{t("compression.progress")}</span>
          <span className="reading font-medium">
            {t("compression.progress_value", { done: done.toLocaleString(), total: total.toLocaleString() })}
          </span>
          {state.skipped > 0 && (
            <span className="text-muted-foreground">
              {t("compression.skipped_value", { n: state.skipped.toLocaleString() })}
            </span>
          )}
        </div>
        <Meter
          value={progress}
          trackLabel={t("compression.progress_label", { percent: Math.round(progress) })}
        />
        {state.status === "failed" && state.last_error && (
          <p className="reading text-xs break-all text-status-critical-ink">
            {t("compression.failed_after", { n: state.attempts })}: {state.last_error}
          </p>
        )}
      </div>

      {/* 两个口径的账 */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <Reading
          label={t("compression.original")}
          value={state.bytes_before > 0 ? formatBytes(state.bytes_before) : "—"}
          hint={t("compression.original_hint")}
        />
        <Reading
          label={t("compression.stored")}
          value={formatBytes(storedBytes)}
          hint={t("compression.stored_hint", { block: formatBytes(blockMetaBytes) })}
        />
        <Reading
          label={t("compression.ratio")}
          value={ratio ? `${ratio.toFixed(1)}×` : "—"}
          hint={settled ? t("compression.ratio_hint") : t("compression.ratio_partial")}
        />
      </div>

      {/* 库的现状 */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Reading
          label={t("compression.file_size")}
          value={formatBytes(db.file_size)}
          hint={t("compression.file_size_hint")}
        />
        <Reading
          label={t("compression.reclaimable")}
          value={formatBytes(freelistBytes)}
          hint={
            db.auto_vacuum === 0
              ? t("compression.reclaimable_hint_off")
              : t("compression.reclaimable_hint_on")
          }
        />
        <Reading
          label={t("compression.rows")}
          value={t("compression.rows_value", {
            pending: db.pending_rows.toLocaleString(),
            framed: db.framed_rows.toLocaleString(),
          })}
          hint={t("compression.rows_hint")}
        />
        <Reading
          label={t("compression.blocks")}
          value={t("compression.blocks_value", {
            blocks: db.block_rows.toLocaleString(),
            groups: db.block_group_rows.toLocaleString(),
          })}
          hint={t("compression.blocks_hint", { size: formatBytes(db.block_group_bytes) })}
        />
      </div>

      {/* 备份探测结论 */}
      <div className="flex flex-wrap items-center gap-2 border-t border-dashed border-border pt-2 text-xs">
        <span className="text-muted-foreground">{t("compression.backup.label")}</span>
        <span
          className={cn(
            "reading break-all",
            backup.source === "manual" ? "text-status-good-ink" : "text-status-warning-ink"
          )}
        >
          {t(`compression.backup.${backup.source}` as never, { defaultValue: backup.source })}
        </span>
        {backup.path && (
          <span className="reading break-all text-muted-foreground">{backup.path}</span>
        )}
      </div>

      {/* 策略 */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-dashed border-border pt-2 text-xs text-muted-foreground">
        <span>
          {policy.enabled ? t("compression.policy_enabled") : t("compression.policy_disabled")}
        </span>
        <span className="reading">
          {t("compression.policy_batch", {
            rows: policy.batch_rows,
            bytes: formatBytes(policy.batch_bytes),
          })}
        </span>
        <Button variant="link" size="sm" className="h-auto px-0 text-xs" onClick={onEdit}>
          {t("compression.edit_policy")}
        </Button>
      </div>
    </div>
  )
}

function Reading({
  label,
  value,
  hint,
}: {
  label: string
  value: string
  hint: string
}) {
  return (
    <div className="space-y-0.5">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <p className="reading text-sm font-semibold">{value}</p>
      <p className="text-[11px] text-muted-foreground">{hint}</p>
    </div>
  )
}

function StatusBadge({ status }: { status: CompressionStatus }) {
  const { t } = useTranslation("config")
  // 后端说在跑，就按在跑显示——库里的状态可能是上一次被 kill 剩下的。
  const key = status.running ? "running" : status.state.status
  const tone = status.running
    ? "border-status-good/40 text-status-good-ink"
    : {
        done: "border-status-good/40 text-status-good-ink",
        failed: "border-status-critical/40 text-status-critical-ink",
        paused: "border-status-warning/40 text-status-warning-ink",
      }[key as string] ?? "border-border text-muted-foreground"

  return (
    <Badge variant="outline" className={cn("gap-1 font-normal", tone)}>
      {status.running && <Loader2 className="size-3 animate-spin" />}
      {status.running && <Database className="size-3" />}
      {t(`compression.status.${key}` as never, { defaultValue: key })}
    </Badge>
  )
}

// ---------------------------------------------------------------------------
// 策略编辑
// ---------------------------------------------------------------------------

function PolicyDialog({
  open,
  onOpenChange,
  status,
  onSaved,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  status: CompressionStatus
  // Promise<unknown> 而不是 Promise<void>：调用方直接传 load，它的返回值
  // 是给轮询用的最新状态，不是"没有值"。收窄成 void 会逼调用方包一层丢弃。
  onSaved: () => Promise<unknown>
}) {
  const { t } = useTranslation(["config", "common"])
  const [enabled, setEnabled] = useState(status.policy.enabled)
  const [batchRows, setBatchRows] = useState(String(status.policy.batch_rows))
  const [batchBytes, setBatchBytes] = useState(String(status.policy.batch_bytes))
  const [quiesceSec, setQuiesceSec] = useState(String(status.policy.quiesce_sec))
  const [saving, setSaving] = useState(false)

  // 每次打开都从服务端的最新值重置：这张卡是会被轮询刷新的，
  // 若沿用上一次打开时填了一半的值，用户会以为自己填的还在。
  useEffect(() => {
    if (!open) return
    setEnabled(status.policy.enabled)
    setBatchRows(String(status.policy.batch_rows))
    setBatchBytes(String(status.policy.batch_bytes))
    setQuiesceSec(String(status.policy.quiesce_sec))
  }, [open, status.policy])

  const save = async () => {
    try {
      setSaving(true)
      // 越界值后端会夹回合法区间（不报错），所以这里不自己校验——
      // 两处校验迟早会不一致，而后端那份才是权威。
      await updateCompressionPolicy({
        enabled,
        batch_rows: Number(batchRows) || 0,
        batch_bytes: Number(batchBytes) || 0,
        quiesce_sec: Number(quiesceSec) || 0,
      })
      toast.success(t("toast.save_success"))
      onOpenChange(false)
      await onSaved()
    } catch (err) {
      toast.error(t("compression.toast.save_failed", { message: errorText(err) }))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("compression.edit_title")}</DialogTitle>
          <DialogDescription>{t("compression.edit_desc")}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex flex-row items-center justify-between rounded-lg border p-4">
            <div className="space-y-0.5 pr-4">
              <Label>{t("compression.policy_switch")}</Label>
              <p className="text-xs text-muted-foreground">{t("compression.policy_switch_hint")}</p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            <div className="space-y-2">
              <Label htmlFor="comp-batch-rows">{t("compression.batch_rows")}</Label>
              <Input
                id="comp-batch-rows"
                type="number"
                min={1}
                max={4096}
                value={batchRows}
                onChange={(e) => setBatchRows(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t("compression.batch_rows_hint")}</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="comp-batch-bytes">{t("compression.batch_bytes")}</Label>
              <Input
                id="comp-batch-bytes"
                type="number"
                min={1}
                value={batchBytes}
                onChange={(e) => setBatchBytes(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t("compression.batch_bytes_hint")}</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="comp-quiesce">{t("compression.quiesce_sec")}</Label>
              <Input
                id="comp-quiesce"
                type="number"
                min={0}
                value={quiesceSec}
                onChange={(e) => setQuiesceSec(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t("compression.quiesce_hint")}</p>
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common:actions.cancel")}
          </Button>
          <Button onClick={() => void save()} disabled={saving}>
            {t("common:actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
