import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import {
  AlertTriangle,
  CircleHelp,
  Database,
  HardDrive,
  Loader2,
  Pause,
  Play,
  RotateCcw,
  Square,
} from "lucide-react"
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
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  getCompression,
  pauseCompression,
  reclaimStorage,
  rollbackCompression,
  runCompression,
  stopReclaim,
  updateCompressionPolicy,
  updateReclaimPolicy,
  type CompressionStatus,
  type ReclaimPolicy,
} from "@/lib/api"
import {
  estimateReclaim,
  estimateRowsSec,
  MIGRATE_ROWS_PER_SEC,
  ROLLBACK_ROWS_PER_SEC,
} from "@/lib/compression"
import { formatBytes, formatDurationMs } from "@/lib/format"
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

/**
 * 三个动作共用的确认窗：「会发生什么」+「代价与退路」+ 可选的警告块。
 *
 * 这三段不是装饰。点这一下要付的代价——多久、期间服务什么样、事后能不能退、
 * 数据显示成什么形态——**只有这张卡说得出来**：服务端只回一个 200 和一行日志，
 * 事后想弄明白就得去读源码。所以三个动作**都**过这个窗；有备份也不跳过。
 * 备份挡的是数据丢失，挡不住"点下去要跑半小时、期间写请求会排队"这类预期落差。
 *
 * 真机那次「回滚之后再迁移什么都没做」也说明同一件事：根因是水位没退，但
 * **"回滚会把迁移进度重置"这句话本来就在这个窗里**——说在前面，能省掉一次排查。
 */
function ActionDialog({
  open,
  onOpenChange,
  title,
  desc,
  flowTitle,
  flow,
  costTitle,
  cost,
  choice,
  warning,
  warningTone = "warning",
  confirmLabel,
  confirmVariant = "default",
  busy,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  title: string
  desc: string
  flowTitle: string
  /** 点下去之后按顺序发生的事。空串会被丢掉，方便调用方按条件拼。 */
  flow: string[]
  costTitle?: string
  cost?: string[]
  /**
   * 动作有档位可选时放这里（目前只有回收的"持续到放完"）。
   * 它排在两个列表**下面**、确认按钮**上面**：先看完"会发生什么"，再决定怎么跑。
   */
  choice?: ReactNode
  warning?: ReactNode
  warningTone?: "warning" | "critical"
  confirmLabel: string
  confirmVariant?: "default" | "destructive"
  busy: boolean
  onConfirm: () => void
}) {
  const { t } = useTranslation("common")
  const tone =
    warningTone === "critical"
      ? { box: "border-status-critical/40 bg-status-critical/5", ink: "text-status-critical-ink" }
      : { box: "border-status-warning/40 bg-status-warning/5", ink: "text-status-warning-ink" }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{desc}</DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <ActionSection title={flowTitle}>
            <ol className="space-y-1.5">
              {flow.filter(Boolean).map((step, i) => (
                <li key={i} className="flex gap-2 text-xs">
                  <span className="reading shrink-0 text-muted-foreground/70">{i + 1}.</span>
                  <span className="min-w-0 text-muted-foreground">{step}</span>
                </li>
              ))}
            </ol>
          </ActionSection>

          {costTitle && cost && cost.length > 0 && (
            <ActionSection title={costTitle}>
              <ul className="space-y-1.5">
                {cost.filter(Boolean).map((item, i) => (
                  <li key={i} className="min-w-0 text-xs text-muted-foreground">
                    {item}
                  </li>
                ))}
              </ul>
            </ActionSection>
          )}

          {choice}

          {warning && (
            <div className={cn("flex items-start gap-2 rounded-md border px-3 py-2", tone.box)}>
              <AlertTriangle className={cn("mt-0.5 size-4 shrink-0", tone.ink)} aria-hidden="true" />
              <div className="min-w-0 space-y-1 text-xs">{warning}</div>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("actions.cancel")}
          </Button>
          <Button variant={confirmVariant} onClick={onConfirm} disabled={busy}>
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ActionSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <p className="text-xs font-medium">{title}</p>
      {children}
    </div>
  )
}

/**
 * 输入项标题旁的「？」：悬停或键盘聚焦时展开一段说明。
 *
 * 表单里每个数字都有两类读者。一类只想确认「这个框填什么」——那一行常显的
 * hint 就够。另一类要知道「允许填多少、填超了会怎样、改了它影响的是什么」——
 * 把这些也塞进 hint，那行常显的小字就变成一段说明书，而常显的字一长就没人读。
 * 所以拆开：常显的说「这是什么」，悬停的说「边界与副作用」。
 *
 * 用 `button` 而不是图标本身：它能被 Tab 聚焦，Radix 的 Tooltip 在聚焦时同样
 * 展开，所以键盘用户不必碰鼠标也看得到；`aria-label` 同时给读屏一个名字。
 */
function HelpHint({ label, text }: { label: string; text: string }) {
  const [open, setOpen] = useState(false)

  /**
   * 「这一次聚焦不该展开」的标记，供 `onOpenChange` 兜底用（见下）。
   */
  const vetoFocusOpen = useRef(false)

  return (
    <Tooltip
      open={open}
      onOpenChange={(next) => {
        // 兜底：万一没拦住（例如 Radix 换了处理函数的合并顺序），展开也要在这里被否掉。
        if (next && vetoFocusOpen.current) {
          vetoFocusOpen.current = false
          return
        }
        setOpen(next)
      }}
    >
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          // Radix 的 Tooltip 在悬停**和聚焦**时都展开。悬停是我们要的；聚焦要分两种：
          // Tab 走过来该展开（否则键盘用户永远看不到这段话），而弹窗打开时浏览器会把
          // 焦点自动放到第一个可聚焦元素上——那个位置正好可能是这个「？」——就不该展开。
          // 不收的话，点开弹窗帮助文案自己就冒出来了（真机复现过，在「数据库压缩策略」
          // 窗里，焦点落在「后台自动推进」旁边的「？」上）。
          //
          // 判据用 `:focus-visible`：它回答的正是「这次聚焦是不是键盘来的」——鼠标点开
          // 弹窗时程序聚焦不带它，Tab 走过来时带（两个方向都在真机上量过）。
          //
          // `preventDefault()` 是拦截的**主要**手段：Radix 的聚焦处理函数是
          // `composeEventHandlers(props.onFocus, 展开)` 包出来的，它看到
          // `defaultPrevented` 就不再往下走（`react-tooltip` 里那行
          // `if (!isPointerDownRef.current) context.onOpen()`）。
          // 而我们的处理函数一定排在它前面——`react-slot` 合并同名的 `on*` 时
          // 子元素的先跑（`childPropValue(...args)` 在 `slotPropValue(...args)` 之前）。
          onFocus={(event) => {
            const byKeyboard = event.currentTarget.matches(":focus-visible")
            vetoFocusOpen.current = !byKeyboard
            if (!byKeyboard) event.preventDefault()
          }}
          // 标记不能留到下一次交互：悬停是明确的展开意图，一进指针就作废标记。
          // （不这么做的话，被否掉的那次聚焦会把标记留下，用户随后把鼠标移上来
          // 反而展不开。）
          onPointerMove={() => {
            vetoFocusOpen.current = false
          }}
          onBlur={() => {
            vetoFocusOpen.current = false
          }}
          className="inline-flex size-4 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
        >
          <CircleHelp className="size-3.5" aria-hidden="true" />
        </button>
      </TooltipTrigger>
      <TooltipContent side="top" className="max-w-xs leading-relaxed">
        {text}
      </TooltipContent>
    </Tooltip>
  )
}

/** 标题 + 「？」。几个输入项的标题都是这个形状，抽出来免得每处各写一遍对齐。 */
function FieldLabel({
  htmlFor,
  label,
  help,
}: {
  htmlFor?: string
  label: string
  help: string
}) {
  return (
    <div className="flex items-center gap-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      <HelpHint label={label} text={help} />
    </div>
  )
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
  const [reclaimOpen, setReclaimOpen] = useState(false)
  const [reclaimPolicyOpen, setReclaimPolicyOpen] = useState(false)
  /**
   * 回收弹窗里那个「持续到放完」开关。默认**打开**：
   *
   *   - 它点一次就把活干完（否则一个 5 GiB 的洞要点十次），
   *   - 而且它**对写请求更好**——持续模式一批一轮、批间松手，写锁最多被握一批
   *     的时间（实测最坏 4.4 秒 < busy_timeout 的 5 秒），写请求是排队而不是失败。
   *
   * 所以这不是"更凶"的那一档，是更温和的那一档，只是总耗时更长。
   */
  const [reclaimContinuous, setReclaimContinuous] = useState(true)

  /**
   * `watching` 是"有一轮任务在飞、界面得盯着它"。
   *
   * 它不能直接等于 `status.running`：点完「开始」到后端那个 goroutine 真正
   * 起来之间有一个空档（HTTP 已经返回、CAS 还没置上），只看 running 的话
   * 轮询根本挂不上，进度条会一直停在原地。所以由动作自己把它置起来，
   * 再由"看到它不再是 running"这个条件把它放下去。
   */
  const [watching, setWatching] = useState(false)

  /**
   * `watchingReclaim` 是同一件事，但盯的是**空间回收**。
   *
   * 两个标志分开而不是合成一个：迁移与回收是两个任务，可以一个在跑另一个
   * 不在。合成一个的话，迁移跑完的那一次刷新会把"回收还在跑"也一起放掉，
   * 界面就停在"回收中"再也不动了。
   */
  const [watchingReclaim, setWatchingReclaim] = useState(false)

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
      // 回收这条同理：in_flight 是个内存里的值，跟随进程，进程重启后它必然是
      // false，而盘上那份 running 记录会一直留着——所以判据用 reclaiming。
      if (next?.reclaiming) setWatchingReclaim(true)
    })
  }, [load])

  // 轮询只在确实有任务在飞时挂着。常驻的话配置页会永远每 2 秒打一次
  // 全表 count（状态接口的 pending_rows 是整表扫 typeof）。
  useEffect(() => {
    if (!watching && !watchingReclaim) return
    const id = window.setInterval(() => {
      void load().then((next) => {
        if (!next) return
        if (!next.running) setWatching(false)
        if (!next.reclaiming) setWatchingReclaim(false)
      })
    }, RUNNING_POLL_MS)
    return () => window.clearInterval(id)
  }, [watching, watchingReclaim, load])

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

  const onReclaim = async () => {
    try {
      setBusy(true)
      await reclaimStorage(reclaimContinuous)
      setWatchingReclaim(true)
      toast.success(t("compression.toast.reclaim_started"))
      setReclaimOpen(false)
      await load()
    } catch (err) {
      toast.error(t("compression.toast.reclaim_failed", { message: errorText(err) }))
    } finally {
      setBusy(false)
    }
  }

  /**
   * 请求停止持续回收。
   *
   * 它**不**把 `watchingReclaim` 放掉：停止在批间生效，真正退出之前状态接口
   * 还会一直报 reclaiming。提前放掉的话轮询就停了，界面会卡在"回收中"不动。
   * 由轮询那条路看到 `reclaiming` 变假再收工。
   */
  const onStopReclaim = async () => {
    try {
      setBusy(true)
      await stopReclaim()
      toast.success(t("compression.toast.reclaim_stopped"))
      await load()
    } catch (err) {
      toast.error(t("compression.toast.reclaim_stop_failed", { message: errorText(err) }))
    } finally {
      setBusy(false)
    }
  }

  // ── 弹窗里那几个估算 ──
  //
  // 全部以 `status.db` 为输入，而它**可能是 null**（迁移正在写库时量不到）。
  // 那种时候估算一律留空：连"还剩多少行"都不知道，编一个数出来比不说更糟。
  const db = status?.db ?? null

  const noBackup = status !== null && status.backup.source !== "manual"

  const pendingRows = db?.pending_rows ?? null
  const runEta =
    pendingRows !== null && pendingRows > 0
      ? t("compression.run_eta", {
          eta: formatDurationMs(estimateRowsSec(pendingRows, MIGRATE_ROWS_PER_SEC) * 1000),
          rows: pendingRows.toLocaleString(),
          rate: MIGRATE_ROWS_PER_SEC.toLocaleString(),
        })
      : ""

  // 回滚要还原的是**已经压过的**行，不是待迁移的行——分母反了会给出一个
  // 与真机差一个数量级的数（真机待迁移 12,483、已迁移 12,469，正好接近，
  // 所以这个错不会自己暴露出来）。
  const framedRows = db?.framed_rows ?? null
  const rollbackEta =
    framedRows !== null && framedRows > 0
      ? t("compression.rollback_eta", {
          eta: formatDurationMs(estimateRowsSec(framedRows, ROLLBACK_ROWS_PER_SEC) * 1000),
          rows: framedRows.toLocaleString(),
          rate: ROLLBACK_ROWS_PER_SEC.toLocaleString(),
        })
      : ""

  const reclaimBytes = db !== null ? db.freelist_count * db.page_size : 0
  const reclaimPlan = estimateReclaim(db?.freelist_count ?? 0, reclaimContinuous)
  const reclaimEta =
    db !== null && reclaimBytes > 0
      ? t(reclaimContinuous ? "compression.reclaim.eta_continuous" : "compression.reclaim.eta", {
          size: formatBytes(reclaimBytes),
          rounds: reclaimPlan.rounds,
          eta: formatDurationMs(reclaimPlan.seconds * 1000),
        })
      : ""

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
          <CompressionBody
            status={status}
            onEdit={() => setEditOpen(true)}
            reclaiming={watchingReclaim || status.reclaiming}
            onReclaim={() => setReclaimOpen(true)}
            onStop={() => void onStopReclaim()}
            onEditPolicy={() => setReclaimPolicyOpen(true)}
          />
        ) : null}
      </CardContent>

      {status && !loadError && (
        <CardFooter className="flex flex-wrap gap-2">
          <Button
            // **总是**先弹确认，有备份也不例外。原先有备份就直接跑——那是拿
            // "备份存在"当成了"用户知道会发生什么"，而这两件事没有关系。
            // 没有备份时这个窗会多出一段警告、确认按钮变红，并把本次执行以
            // "无备份"记入存证（后端也有一份同样的门，没确认就是 400）。
            onClick={() => setRunOpen(true)}
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
            disabled={
              busy || status.running || (status.db !== null && status.db.framed_rows === 0)
            }
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
        <ReclaimPolicyDialog
          open={reclaimPolicyOpen}
          onOpenChange={setReclaimPolicyOpen}
          policy={status.reclaim_policy}
          onSaved={load}
        />
      )}

      {status && (
        <ActionDialog
          open={runOpen}
          onOpenChange={setRunOpen}
          title={t("compression.run_title")}
          desc={t("compression.run_desc")}
          flowTitle={t("compression.run_flow_title")}
          flow={[
            t("compression.run_step_1"),
            t("compression.run_step_2"),
            t("compression.run_step_3"),
            t("compression.run_step_4"),
            runEta,
          ]}
          costTitle={t("compression.run_cost_title")}
          cost={[t("compression.run_cost_1"), t("compression.run_cost_2")]}
          warningTone="critical"
          warning={
            noBackup ? (
              <>
                <p className="font-medium">{t("compression.no_backup_title")}</p>
                <p>{t("compression.no_backup_desc")}</p>
                {/* 探测结论与路径照实带出来：用户要据此判断"我到底把备份
                    放对地方了没有"，只写"未找到备份"他会不知道该看哪儿。 */}
                <p className="reading break-all">
                  {t(`compression.backup.${status.backup.source}` as never, {
                    defaultValue: status.backup.source,
                  })}
                  {status.backup.path ? ` · ${status.backup.path}` : ""}
                </p>
                <p>{t("compression.no_backup_hint")}</p>
              </>
            ) : undefined
          }
          confirmLabel={
            noBackup ? t("compression.no_backup_confirm") : t("compression.run_confirm")
          }
          confirmVariant={noBackup ? "destructive" : "default"}
          busy={busy}
          onConfirm={() => void startRun(noBackup)}
        />
      )}

      <ActionDialog
        open={rollbackOpen}
        onOpenChange={setRollbackOpen}
        title={t("compression.rollback_title")}
        desc={t("compression.rollback_desc")}
        flowTitle={t("compression.rollback_flow_title")}
        flow={[
          t("compression.rollback_step_1"),
          t("compression.rollback_step_2"),
          rollbackEta,
        ]}
        costTitle={t("compression.rollback_cost_title")}
        // `rollback_reset` 那一句是**必须说的**：回滚跑完会把迁移进度重置成
        // "从未运行"，想回到压缩形态得从头再做一遍。不说的话，用户回滚完看到
        // 界面一片"从未运行"，会以为这一趟白跑了或者功能坏了。
        cost={[t("compression.rollback_reset")]}
        warningTone="critical"
        warning={<p>{t("compression.rollback_warn")}</p>}
        confirmLabel={t("compression.rollback_confirm")}
        confirmVariant="destructive"
        busy={busy}
        onConfirm={() => void onRollback()}
      />

      {/* 回收也要过一次确认。它不是"随手点一下"：动作本身不动数据（可重复、
          可中断、不丢东西），但它**全程持写锁**——其间所有写请求排队，
          超过 busy_timeout（5 秒）的直接失败。也就是说，点这一下的代价是
          "这段时间里的请求可能失败"，这句话必须点之前说，不是点之后从日志里看。
          `why` 那一段回答的是另一半疑问："既然 VACUUM 快得多，为什么这里慢"——
          不回答它，"慢"就会被当成"坏"。 */}
      <ActionDialog
        open={reclaimOpen}
        onOpenChange={setReclaimOpen}
        title={t("compression.reclaim.title")}
        desc={t("compression.reclaim.desc")}
        flowTitle={t("compression.reclaim.flow_title")}
        flow={[
          reclaimContinuous
            ? t("compression.reclaim.step_continuous")
            : t("compression.reclaim.step_1"),
          reclaimEta,
        ]}
        costTitle={t("compression.reclaim.cost_title")}
        cost={[
          reclaimContinuous ? t("compression.reclaim.warn_continuous") : t("compression.reclaim.warn"),
          t("compression.reclaim.why"),
        ]}
        choice={
          // 两档的区别**不是凶不凶，而是写请求会不会失败**，所以这个开关必须
          // 把两边的代价都写在旁边——只写"持续到放完"会让人以为它更激进。
          <div className="flex items-start justify-between gap-3 rounded-lg border p-3">
            <div className="space-y-1">
              <div className="flex items-center gap-1.5">
                <Label htmlFor="reclaim-continuous">{t("compression.reclaim.continuous")}</Label>
                <HelpHint
                  label={t("compression.reclaim.continuous")}
                  text={t("compression.reclaim.continuous_help")}
                />
              </div>
              <p className="text-[11px] text-muted-foreground">
                {t("compression.reclaim.continuous_hint")}
              </p>
            </div>
            <Switch
              id="reclaim-continuous"
              checked={reclaimContinuous}
              onCheckedChange={setReclaimContinuous}
            />
          </div>
        }
        confirmLabel={t("compression.reclaim.confirm")}
        busy={busy}
        onConfirm={() => void onReclaim()}
      />
    </Card>
  )
}

// ---------------------------------------------------------------------------
// 读数
// ---------------------------------------------------------------------------

function CompressionBody({
  status,
  onEdit,
  onReclaim,
  onStop,
  onEditPolicy,
  reclaiming,
}: {
  status: CompressionStatus
  onEdit: () => void
  onReclaim: () => void
  onStop: () => void
  onEditPolicy: () => void
  reclaiming: boolean
}) {
  const { t } = useTranslation(["config", "common"])
  const { state, db, policy, backup, reclaim } = status

  // db 可能是 null：**量不到库的现状不是错误**（迁移正在写库时这一读会撞锁，
  // 后端重试后仍失败就把它标成 null）。那时不画 0——`auto_vacuum=0` 与
  // `freelist_count=0` 都是有确切含义的值（前者意味着文件永不缩，界面据此
  // 画警告线），拿 0 顶替"不知道"会把结论画反。整块如实说"没量到"。
  //
  // 真正落库 = input 列 + 组表 + 块表。块表那 40 字节/行是估算（见 BLOCK_ROW_BYTES）。
  const blockMetaBytes = db ? db.block_rows * BLOCK_ROW_BYTES : 0
  const storedBytes = db
    ? db.input_column_bytes + db.block_group_bytes + blockMetaBytes
    : null

  // 压缩比的分子是 **state.bytes_total**（全表原文合计，起跑时量一次）。
  //
  // 这里原先的门是 `pending_rows === 0`：理由是 bytes_before 只覆盖扫过的行，
  // 拿它配全表的分母会算出一个看起来很专业的错数——那个顾虑是对的，但门开错了
  // 地方。**该判的是"分子与分母盖的是不是同一批行"，不是"还有没有明文行"**：
  // 而 pending_rows 永远到不了 0，因为**压不动的行**（帧比原文还大）会被迁移
  // 有意留成明文，真机上就有 15 行。于是这个比值一次都没显示过。
  //
  // 换成 bytes_total 之后两个数盖的都是全表，中途报的也是真数——而且它会随着
  // 行改形态**一路往上爬**（起跑时约 1.0×），这正是"实时"该有的样子。
  const ratio =
    state.bytes_total > 0 && storedBytes !== null && storedBytes > 0
      ? state.bytes_total / storedBytes
      : null
  // 还在推进时这个数只会涨，得说出来，否则那个初始的 1.0× 会被当成"压了没用"。
  const climbing = status.running || state.status === "running"

  // 进度分母：本轮要扫的总行数。没有候选行时（稳态）说"已完成"而不是 0/0。
  const total = state.total_rows
  const done = state.scanned
  const progress = total > 0 ? Math.min((done / total) * 100, 100) : 100

  // freelist 里那些页是"删了/改了但还没还给文件系统"的量。它就是
  // "省了这么多但文件没缩"的那段距离，所以拿它和文件大小并排说。
  const freelistBytes = db ? db.freelist_count * db.page_size : null

  // 回收按钮为什么点不动。**每一条都要说得出理由**：一个灰着的按钮不写原因，
  // 用户只会以为这功能是坏的。
  const reclaimBlocked: string | null = status.running
    ? "busy_migration"
    : db === null
      ? "no_stats"
      : // auto_vacuum=0 时 `PRAGMA incremental_vacuum` 是个 0.000 秒的空操作
        // （实测），文件一字节不缩。让人点一个注定什么都没发生的按钮，
        // 比直接告诉他"这个库得先转一次"要糟得多。
        db.auto_vacuum !== 2
        ? "no_auto_vacuum"
        : db.freelist_count === 0
          ? "nothing"
          : null

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
          value={state.bytes_total > 0 ? formatBytes(state.bytes_total) : "—"}
          hint={
            state.bytes_total > 0
              ? t("compression.original_hint")
              : t("compression.original_unmeasured")
          }
        />
        <Reading
          label={t("compression.stored")}
          value={storedBytes !== null ? formatBytes(storedBytes) : "—"}
          hint={
            storedBytes !== null
              ? t("compression.stored_hint", { block: formatBytes(blockMetaBytes) })
              : t("compression.db_unavailable")
          }
        />
        <Reading
          label={t("compression.ratio")}
          value={ratio ? `${ratio.toFixed(1)}×` : "—"}
          hint={climbing ? t("compression.ratio_running") : t("compression.ratio_hint")}
        />
      </div>

      {/* 库的现状 */}
      {db === null ? (
        <p className="text-xs text-muted-foreground">{t("compression.db_unavailable_short")}</p>
      ) : (
        <div className="space-y-1.5">
          {/* 这一组数带 5 秒冷却（见 service/db_stats.go）：迁移期间每 2 秒轮询
              一次全表聚合，等于自己给自己造锁竞争。所以它**可能是上一次的**，
              说清楚是几点量到的，而不是让人以为这是此刻的。 */}
          {db.stale && (
            <p className="text-xs text-status-warning-ink">
              {t("compression.db_stale", { time: new Date(db.stats_at).toLocaleTimeString() })}
            </p>
          )}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Reading
              label={t("compression.file_size")}
              value={formatBytes(db.file_size)}
              hint={t("compression.file_size_hint")}
            />
            <Reading
              label={t("compression.reclaimable")}
              value={formatBytes(freelistBytes ?? 0)}
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
        </div>
      )}

      {/* 空间回收：把 freelist 里的页还给文件系统。
          它与上面那个「可回收」读数是同一件事的两半，所以挨着放——
          隔着半屏的话，用户看到"可回收 5 GiB"会不知道下一步该干什么。 */}
      <ReclaimBlock
        reclaim={reclaim}
        policy={status.reclaim_policy}
        reclaiming={reclaiming}
        stopping={status.reclaim_stopping}
        blocked={reclaimBlocked}
        freelistBytes={freelistBytes}
        onReclaim={onReclaim}
        onStop={onStop}
        onEditPolicy={onEditPolicy}
      />

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
        {/* 只在非 0 时露出来：默认（0）不占位置，而一旦有人调过它，
            迁移变慢就有了看得见的解释——否则那个旋钮会被忘在那儿。 */}
        {policy.batch_interval_ms > 0 && (
          <span className="reading">
            {t("compression.policy_interval", { ms: policy.batch_interval_ms })}
          </span>
        )}
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

/**
 * 空间回收那一块：上一次回收的结论 + 触发按钮。
 *
 * ## 为什么"上一次为什么停的"要当正文显示
 *
 * 因为 `no_auto_vacuum` 这一个值，是"点了按钮、瞬间完成、什么都没变"
 * 与"这个库得先转一次"之间的全部区别。后端如实记了它，界面就有义务说出来。
 *
 * ## 为什么按钮会灰
 *
 * 每种灰都有理由，而且都写在按钮下面（见调用方算的 `blocked`）。一个不写
 * 原因的灰按钮会被当成故障——尤其 `no_auto_vacuum` 这一种，它不是"暂时不行"，
 * 是这个库的形态决定了这条路走不通，得换一条路。
 */
function ReclaimBlock({
  reclaim,
  policy,
  reclaiming,
  stopping,
  blocked,
  freelistBytes,
  onReclaim,
  onStop,
  onEditPolicy,
}: {
  reclaim: CompressionStatus["reclaim"]
  policy: ReclaimPolicy
  reclaiming: boolean
  stopping: boolean
  blocked: string | null
  freelistBytes: number | null
  onReclaim: () => void
  onStop: () => void
  onEditPolicy: () => void
}) {
  const { t } = useTranslation("config")
  const ran = reclaim.status === "done" || reclaim.status === "failed"
  // 换过多少：只有真放过页才说得出这个数。启动期的转换/VACUUM 也记了，
  // 所以它同时是"上次启动时做过什么"的回执。
  const shrank =
    ran && reclaim.file_size_before > 0 && reclaim.file_size_after > 0
      ? reclaim.file_size_after < reclaim.file_size_before
      : false

  return (
    <div className="space-y-2 border-t border-dashed border-border pt-2">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-0.5">
          <div className="flex flex-wrap items-baseline gap-x-2 text-xs">
            <span className="font-medium text-muted-foreground">
              {t("compression.reclaim.label")}
            </span>
            {ran ? (
              <span className="reading">
                {t("compression.reclaim.freed", {
                  size: formatBytes(reclaim.freed_bytes),
                })}
              </span>
            ) : (
              <span className="text-muted-foreground">{t("compression.reclaim.never")}</span>
            )}
            {reclaiming && (
              <span className="flex items-center gap-1 text-status-good-ink">
                <Loader2 className="size-3 animate-spin" />
                {stopping ? t("compression.reclaim.stopping") : t("compression.reclaim.running")}
              </span>
            )}
            {/* 定时回收开着就挂个小标：它是**后台自己会动**的状态，不显示的话
                用户只能从"我没点过，它怎么跑过了"里反推出来。 */}
            {policy.enabled && (
              <span className="text-[11px] text-muted-foreground">
                {t("compression.reclaim.auto_on", {
                  size: formatBytes(policy.min_bytes),
                  min: Math.round(policy.check_interval_sec / 60),
                })}
              </span>
            )}
          </div>
          {ran && (
            <p className="reading text-[11px] break-all text-muted-foreground">
              {t("compression.reclaim.how", {
                reason: t(`compression.reclaim.reason.${reclaim.stop_reason}` as never, {
                  defaultValue: reclaim.stop_reason,
                }),
                source: t(`compression.reclaim.source.${reclaim.source}` as never, {
                  defaultValue: reclaim.source,
                }),
                duration: formatDurationMs(reclaim.duration_ms),
                calls: reclaim.calls,
              })}
              {/* 持续那一趟要额外说"几轮"：同一个库，`1 轮` 和 `37 轮` 是两次
                  完全不同的运维动作，光看耗时看不出来（一次是到点收工，
                  一次是一路放到底）。 */}
              {reclaim.continuous &&
                " · " +
                  t("compression.reclaim.rounds", { rounds: reclaim.rounds.toLocaleString() })}
              {shrank &&
                " · " +
                  t("compression.reclaim.file_change", {
                    before: formatBytes(reclaim.file_size_before),
                    after: formatBytes(reclaim.file_size_after),
                  })}
            </p>
          )}
          {/* 有 last_error 就显示，不只在 failed 时：`stalled` 是 done 但要带话说清
              为什么提前收工——那一句在收工理由里指了过来说"细节见下方报错"。 */}
          {reclaim.last_error && (
            <p className="reading text-[11px] break-all text-status-critical-ink">
              {reclaim.last_error}
            </p>
          )}
          {blocked && (
            <p className="text-[11px] text-muted-foreground">
              {t(`compression.reclaim.blocked.${blocked}` as never, { defaultValue: blocked })}
            </p>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-2">
          {/* 「停止」只在真有一趟在跑时出现。持续回收可能跑十几分钟，
              没有这个按钮，用户唯一能做的就是等——而它偏偏是"我现在不想让它
              继续占库了"这种最常见的念头。 */}
          {reclaiming && (
            <Button variant="outline" size="sm" onClick={onStop} disabled={stopping}>
              {stopping ? <Loader2 className="size-4 animate-spin" /> : <Square className="size-4" />}
              {stopping ? t("compression.reclaim.stopping") : t("compression.reclaim.stop")}
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            onClick={onReclaim}
            disabled={reclaiming || blocked !== null}
          >
            {reclaiming ? (
              <>
                <Loader2 className="size-4 animate-spin" />
                {t("compression.reclaim.running")}
              </>
            ) : (
              <>
                <HardDrive className="size-4" />
                {freelistBytes !== null && freelistBytes > 0
                  ? t("compression.reclaim.button", { size: formatBytes(freelistBytes) })
                  : t("compression.reclaim.button_bare")}
              </>
            )}
          </Button>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
        {/* 只在这一轮确实有东西可放时说代价。没东西可放时的警告是噪音，
            而噪音会让真正的警告失效。 */}
        {blocked === null && freelistBytes !== null && freelistBytes > 0 && (
          <span className="text-status-warning-ink">{t("compression.reclaim.cost")}</span>
        )}
        <Button variant="link" size="sm" className="h-auto px-0 text-[11px]" onClick={onEditPolicy}>
          {t("compression.reclaim.policy_edit")}
        </Button>
      </div>
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
  const [batchIntervalMs, setBatchIntervalMs] = useState(String(status.policy.batch_interval_ms))
  const [saving, setSaving] = useState(false)

  // 每次打开都从服务端的最新值重置：这张卡是会被轮询刷新的，
  // 若沿用上一次打开时填了一半的值，用户会以为自己填的还在。
  useEffect(() => {
    if (!open) return
    setEnabled(status.policy.enabled)
    setBatchRows(String(status.policy.batch_rows))
    setBatchBytes(String(status.policy.batch_bytes))
    setQuiesceSec(String(status.policy.quiesce_sec))
    setBatchIntervalMs(String(status.policy.batch_interval_ms))
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
        batch_interval_ms: Number(batchIntervalMs) || 0,
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
              <div className="flex items-center gap-1.5">
                <Label>{t("compression.policy_switch")}</Label>
                <HelpHint
                  label={t("compression.policy_switch")}
                  text={t("compression.policy_switch_help")}
                />
              </div>
              <p className="text-xs text-muted-foreground">{t("compression.policy_switch_hint")}</p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="space-y-2">
              <FieldLabel
                htmlFor="comp-batch-rows"
                label={t("compression.batch_rows")}
                help={t("compression.batch_rows_help")}
              />
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
              <FieldLabel
                htmlFor="comp-batch-bytes"
                label={t("compression.batch_bytes")}
                help={t("compression.batch_bytes_help")}
              />
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
              <FieldLabel
                htmlFor="comp-quiesce"
                label={t("compression.quiesce_sec")}
                help={t("compression.quiesce_help")}
              />
              <Input
                id="comp-quiesce"
                type="number"
                min={0}
                value={quiesceSec}
                onChange={(e) => setQuiesceSec(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t("compression.quiesce_hint")}</p>
            </div>
            <div className="space-y-2">
              <FieldLabel
                htmlFor="comp-interval"
                label={t("compression.batch_interval")}
                help={t("compression.batch_interval_help")}
              />
              <Input
                id="comp-interval"
                type="number"
                min={0}
                max={5000}
                step={50}
                value={batchIntervalMs}
                onChange={(e) => setBatchIntervalMs(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">
                {t("compression.batch_interval_hint")}
              </p>
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

// ---------------------------------------------------------------------------
// 回收策略（定时回收）
// ---------------------------------------------------------------------------

/**
 * 空间回收的自动推进策略。
 *
 * 与迁移策略分成两个弹窗而不是并成一个：两者的触发时机不是一回事——迁移是
 * "改一次数据形态就完事"，回收是"跟着删除量一直跑"。并在一起的话，用户改完
 * 迁移的批大小会顺手动到回收的门槛，而这两个数之间没有任何关系。
 *
 * 这个开关默认关着，而且**界面上要说清楚它为什么值得开**：回收占写锁，
 * 而一个"忙时开着服务"的库最不需要的就是一个会在任意时刻来占写锁的后台任务。
 * 它的正当用途是"我知道这台机器晚上没人用"或者"我删了一大批日志，让它自己收"。
 */
function ReclaimPolicyDialog({
  open,
  onOpenChange,
  policy,
  onSaved,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  policy: ReclaimPolicy
  onSaved: () => Promise<unknown>
}) {
  const { t } = useTranslation(["config", "common"])
  const [enabled, setEnabled] = useState(policy.enabled)
  const [minBytes, setMinBytes] = useState(String(policy.min_bytes))
  const [checkSec, setCheckSec] = useState(String(policy.check_interval_sec))
  const [saving, setSaving] = useState(false)

  // 每次打开都从服务端的最新值重置：这张卡会被轮询刷新，沿用上一次填了一半的
  // 值会让用户以为自己填的还在。
  useEffect(() => {
    if (!open) return
    setEnabled(policy.enabled)
    setMinBytes(String(policy.min_bytes))
    setCheckSec(String(policy.check_interval_sec))
  }, [open, policy])

  const save = async () => {
    try {
      setSaving(true)
      // 越界由后端夹回（不报错），这里不自己校验——两份校验迟早会不一致，
      // 而后端那份才是权威。
      await updateReclaimPolicy({
        enabled,
        min_bytes: Number(minBytes) || 0,
        check_interval_sec: Number(checkSec) || 0,
      })
      toast.success(t("toast.save_success"))
      onOpenChange(false)
      await onSaved()
    } catch (err) {
      // 走 `compression.toast.save_failed` 这个带占位符的键，不要拿顶层
      // `toast.save_failed` 拼字符串——那个键里也带 `{{message}}`，
      // 手工拼会让它原样显示成 "保存策略失败: {{message}}: ..."。
      toast.error(t("compression.toast.save_failed", { message: errorText(err) }))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("compression.reclaim.policy_title")}</DialogTitle>
          <DialogDescription>{t("compression.reclaim.policy_desc")}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex flex-row items-center justify-between rounded-lg border p-4">
            <div className="space-y-0.5 pr-4">
              <div className="flex items-center gap-1.5">
                <Label>{t("compression.reclaim.policy_switch")}</Label>
                <HelpHint
                  label={t("compression.reclaim.policy_switch")}
                  text={t("compression.reclaim.policy_switch_help")}
                />
              </div>
              <p className="text-xs text-muted-foreground">
                {t("compression.reclaim.policy_switch_hint")}
              </p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="space-y-2">
              <FieldLabel
                htmlFor="reclaim-min-bytes"
                label={t("compression.reclaim.min_bytes")}
                help={t("compression.reclaim.min_bytes_help")}
              />
              <Input
                id="reclaim-min-bytes"
                type="number"
                min={0}
                value={minBytes}
                onChange={(e) => setMinBytes(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">
                {t("compression.reclaim.min_bytes_hint")}
              </p>
            </div>
            <div className="space-y-2">
              <FieldLabel
                htmlFor="reclaim-check-sec"
                label={t("compression.reclaim.check_interval")}
                help={t("compression.reclaim.check_interval_help")}
              />
              <Input
                id="reclaim-check-sec"
                type="number"
                min={60}
                max={86400}
                step={60}
                value={checkSec}
                onChange={(e) => setCheckSec(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">
                {t("compression.reclaim.check_interval_hint")}
              </p>
            </div>
          </div>

          {/* 自动跑的那一趟总是**持续模式**：一次只收 90 秒的话，一个 5 GiB 的洞
              要十个周期才收得完，那还不如不自动。这句话必须写出来——它决定了
              用户对"自动回收期间服务会不会有一阵子写入变慢"的预期。 */}
          <p className="text-[11px] text-muted-foreground">
            {t("compression.reclaim.policy_continuous_note")}
          </p>
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
