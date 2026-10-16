import { useEffect, useRef, useState } from "react"
import { useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { ExternalLink, MessageSquare } from "lucide-react"

import { StatusMark } from "@/components/status-mark"
import { ListSkeleton } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { getLogById, type ChatLog } from "@/lib/api"
import { formatBytes, formatCost, formatDurationNs, formatFull, formatNumber } from "@/lib/format"
import { canViewChatIO } from "@/lib/logs"
import { cn } from "@/lib/utils"

/** 这条日志是不是走了一层协议转换：直连时后端写入的 upstream_style 与 Style 相同，转换时才分叉。 */
function isBridged(log: ChatLog): boolean {
  return !!log.upstream_style && log.upstream_style !== log.Style
}

function statusKey(status: string): "success" | "error" | "running" | "unknown" {
  return status === "success" || status === "error" || status === "running" ? status : "unknown"
}

/**
 * 单条日志详情。
 *
 * 用右侧抽屉而非弹窗：读者要在列表与详情之间来回比对（看这条是不是刚才那条），
 * 遮住整页反而妨碍操作。关闭后焦点由 Radix 归还到触发行。
 *
 * 两种入参：
 * - `log`：调用方手里已有整行（日志页的行本来就在列表里），直接展示，不发请求；
 * - `logId`：调用方只有一个 id（首页的错误样本），组件自己去取，因此多两态
 *   ——加载中、以及这条记录已经不存在（清理策略生效后就取不到）。
 */
export function LogDetailSheet({
  log,
  logId,
  onClose,
}: {
  log?: ChatLog | null
  logId?: number | null
  onClose: () => void
}) {
  const { t } = useTranslation(["logs", "common"])
  const [fetched, setFetched] = useState<{ log: ChatLog | null; error: string | null } | null>(null)
  const [loading, setLoading] = useState(false)
  // 连点开几条样本时会有多次在途请求，只靠"是否卸载"挡不住迟到的响应
  const requestSeq = useRef(0)

  useEffect(() => {
    if (logId === undefined || logId === null) return
    const seq = ++requestSeq.current
    let alive = true
    setLoading(true)
    getLogById(logId)
      .then((res) => {
        if (!alive || seq !== requestSeq.current) return
        setFetched({ log: res, error: null })
        setLoading(false)
      })
      .catch((err: unknown) => {
        if (!alive || seq !== requestSeq.current) return
        setFetched({ log: null, error: err instanceof Error ? err.message : String(err) })
        setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [logId])

  const active = log ?? fetched?.log ?? null
  const open = log !== undefined ? log !== null : logId !== undefined && logId !== null
  const shownId = logId ?? active?.ID ?? null

  // 加载中与「这条记录不存在」两态没有描述节点，而 Radix 在 aria-describedby
  // 指向的节点不在 DOM 里时会告警——告警多了就没人看了。这两态显式声明
  // 「没有描述」；有内容时不传这个属性，让 Radix 自己接上描述 id。
  const describedByProps = active ? {} : { "aria-describedby": undefined }

  return (
    <Sheet open={open} onOpenChange={(open) => !open && onClose()}>
      <SheetContent {...describedByProps}>
        <SheetHeader>
          <SheetTitle>{shownId ? t("logs:detail.title", { id: shownId }) : ""}</SheetTitle>
          {active && (
            <SheetDescription className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <StatusMark status={active.Status} label={t(`common:status.${statusKey(active.Status)}` as never)} />
              <span className="reading">{formatFull(new Date(active.CreatedAt).getTime())}</span>
              <span>{active.Name}</span>
            </SheetDescription>
          )}
        </SheetHeader>

        <SheetBody className="space-y-5">
          {loading && <ListSkeleton label={t("logs:detail.loading")} rows={5} />}
          {!loading && !active && fetched && (
            <p className="py-6 text-center text-sm text-muted-foreground">
              {fetched.error ? t("logs:detail.load_failed") : t("logs:detail.not_found")}
              {fetched.error && <span className="mt-1 block break-words text-xs">{fetched.error}</span>}
            </p>
          )}
          {active && <DetailBody log={active} />}
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}

function DetailBody({ log }: { log: ChatLog }) {
  const { t } = useTranslation(["logs", "common"])
  const navigate = useNavigate()
  const cached = log.prompt_tokens_details?.cached_tokens ?? 0
  const hasPricing = (log.input_price ?? 0) > 0 || (log.output_price ?? 0) > 0
  const bridged = isBridged(log)
  const bridgeCodes = (log.bridge_notes ?? "")
    .split(",")
    .map((code) => code.trim())
    .filter(Boolean)
  const totalCost =
    (Math.max(0, (log.prompt_tokens ?? 0) - cached) / 1e6) * (log.input_price ?? 0) +
    (cached / 1e6) * (log.cache_read_price ?? 0) +
    ((log.completion_tokens ?? 0) / 1e6) * (log.output_price ?? 0)

  return (
    <>
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
          {/* 两个名字的次序与标签都不能混：请求名是客户端请求体里写的那个
              （chat_logs.name，llmio 别名），模型才是真正发给上游的 id
              （chat_logs.provider_model）。屉抽里把请求名标成"模型名称"，
              读者会拿着别名去筛选，而筛选打的是上游 id，于是筛出 0 行。 */}
          <DetailField label={t("logs:detail.model_name")} value={log.Name} />
          <DetailField label={t("logs:detail.provider")} value={log.ProviderName || "-"} />
          <DetailField label={t("logs:detail.provider_model")} value={log.ProviderModel || "-"} mono />
          <DetailField label={t("logs:detail.type")} value={log.Style || "-"} />
          {bridged && (
            <DetailField
              label={t("logs:bridge.upstream_type")}
              value={<span className="reading text-xs">{log.upstream_style}</span>}
            />
          )}
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

      {bridged && (
        <Section title={t("logs:bridge.title")}>
          <div className="grid grid-cols-2 gap-3">
            <DetailField
              label={t("logs:bridge.direction")}
              value={<span className="reading text-xs">{log.Style} → {log.upstream_style}</span>}
            />
          </div>
          <div className="mt-3 space-y-1.5">
            <div className="text-[11px] tracking-wide text-muted-foreground uppercase">
              {bridgeCodes.length > 0 ? t("logs:bridge.changed") : t("logs:bridge.clean")}
            </div>
            {bridgeCodes.map((code) => (
              <div key={code} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                <code className="reading rounded bg-muted px-1.5 py-0.5 text-[11px]">{code}</code>
                <span className="text-xs text-muted-foreground">
                  {t(`logs:bridge.notes.${code}` as never, { defaultValue: t("logs:bridge.unknown_note") })}
                </span>
              </div>
            ))}
          </div>
        </Section>
      )}

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
            value={
              hasPricing ? formatCost((Math.max(0, (log.prompt_tokens ?? 0) - cached) / 1e6) * (log.input_price ?? 0), log.currency) : "-"
            }
          />
          <DetailField
            label={t("logs:detail.billing_cache")}
            value={hasPricing ? formatCost((cached / 1e6) * (log.cache_read_price ?? 0), log.currency) : "-"}
          />
          <DetailField
            label={t("logs:detail.billing_output")}
            value={hasPricing ? formatCost(((log.completion_tokens ?? 0) / 1e6) * (log.output_price ?? 0), log.currency) : "-"}
          />
          <DetailField
            label={t("logs:detail.billing_total")}
            value={hasPricing ? formatCost(totalCost, log.currency) : "-"}
          />
        </div>
      </Section>

      {canViewChatIO(log) ? (
        <Button variant="outline" size="sm" className="w-full" onClick={() => navigate(`/logs/${log.ID}/chat-io`)}>
          <MessageSquare className="mr-1.5 size-3.5" aria-hidden="true" />
          {t("logs:table.view_io")}
        </Button>
      ) : (
        <Button variant="outline" size="sm" className="w-full" onClick={() => navigate(`/logs?id=${log.ID}`)}>
          <ExternalLink className="mr-1.5 size-3.5" aria-hidden="true" />
          {t("logs:detail.open_in_logs")}
        </Button>
      )}
    </>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{title}</h3>
      {children}
    </section>
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
