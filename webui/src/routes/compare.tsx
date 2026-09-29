import { useCallback, useEffect, useMemo, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowLeft, CircleDot, FileJson, Wrench } from "lucide-react"

import { StatusMark } from "@/components/status-mark"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { getChatIO, getLogById, type ChatIO, type ChatLog } from "@/lib/api"
import {
  cacheHitRate,
  commonPrefixEntries,
  describePrefix,
  divergenceOf,
  extractStructure,
  toEntries,
  type PromptEntry,
  type PromptStructure,
} from "@/lib/compare"
import { formatDurationNs, formatNumber, formatPercent, formatFull } from "@/lib/format"
import { cn } from "@/lib/utils"

/** 对比上限。与日志页的 MAX_COMPARE 保持一致。 */
const MAX_COMPARE = 6

type Item = {
  id: number
  log: ChatLog | null
  io: ChatIO | null
  structure: PromptStructure
  /** 加载失败的原因，用于把"取不到"与"没记录"区分开 */
  error: string | null
}

export default function ComparePage() {
  const { t } = useTranslation("compare")
  const [searchParams] = useSearchParams()

  const ids = useMemo(() => {
    const raw = searchParams.get("ids") ?? ""
    return raw
      .split(",")
      .map((s) => Number(s.trim()))
      .filter((n) => Number.isInteger(n) && n > 0)
      .slice(0, MAX_COMPARE)
  }, [searchParams])

  const [items, setItems] = useState<Item[]>([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    const results = await Promise.all(
      ids.map(async (id): Promise<Item> => {
        const [logRes, ioRes] = await Promise.allSettled([getLogById(id), getChatIO(id)])
        const log = logRes.status === "fulfilled" ? logRes.value : null
        const io = ioRes.status === "fulfilled" ? ioRes.value : null
        return {
          id,
          log,
          io,
          structure: io ? extractStructure(io.Input) : { tools: [], system: null, messages: [] },
          error:
            logRes.status === "rejected"
              ? String(logRes.reason)
              : ioRes.status === "rejected"
                ? String(ioRes.reason)
                : null,
        }
      })
    )
    setItems(results)
    setLoading(false)
  }, [ids])

  useEffect(() => {
    void load()
  }, [load])

  // 只对有内容的请求做前缀分析：一条没记录 IO 的请求会把前缀误算成 0
  const analyzable = useMemo(() => items.filter((i) => toEntries(i.structure).length > 0), [items])
  const prefixLen = useMemo(
    () => commonPrefixEntries(analyzable.map((i) => i.structure)),
    [analyzable]
  )
  const prefixEntries = useMemo(
    () => (analyzable[0] ? toEntries(analyzable[0].structure).slice(0, prefixLen) : []),
    [analyzable, prefixLen]
  )
  const prefixBreakdown = useMemo(() => describePrefix(prefixEntries), [prefixEntries])

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-1">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="sm" className="h-8 gap-1 text-xs" asChild>
          <Link to="/logs">
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            {t("back")}
          </Link>
        </Button>
        <h2 className="mr-auto text-xl font-semibold tracking-tight">
          {t("title", { count: items.length })}
        </h2>
        <Button variant="outline" size="sm" className="h-8 text-xs" onClick={() => void load()}>
          {t("reload")}
        </Button>
      </div>

      {ids.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-1 py-16 text-center">
            <p className="text-sm font-medium">{t("empty.no_ids")}</p>
            <Link to="/logs" className="text-xs text-primary underline-offset-2 hover:underline">
              {t("empty.go_logs")}
            </Link>
          </CardContent>
        </Card>
      ) : loading ? (
        <div className="space-y-3">
          <div className="h-24 animate-pulse rounded-lg bg-muted" />
          <div className="h-64 animate-pulse rounded-lg bg-muted" />
        </div>
      ) : (
        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto">
          {analyzable.length < items.length && (
            <div className="flex items-start gap-2 rounded-md border border-border bg-accent/40 px-3 py-2 text-sm">
              <AlertTriangle className="mt-0.5 size-4 shrink-0 text-status-warning-ink" aria-hidden="true" />
              <span>{t("partial", { ok: analyzable.length, total: items.length })}</span>
            </div>
          )}

          {/* 头条结论：缓存前缀到底有多长、由什么构成 */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium">{t("prefix.title")}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              {prefixLen === 0 ? (
                <p className="text-sm text-muted-foreground">{t("prefix.none")}</p>
              ) : (
                <>
                  <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
                    <div>
                      <span className="text-xs text-muted-foreground">{t("prefix.length")}</span>
                      <div className="text-2xl font-semibold">{prefixLen}</div>
                    </div>
                    <div className="text-sm text-muted-foreground">
                      {t("prefix.composition", {
                        tools: prefixBreakdown.tools,
                        system: prefixBreakdown.system,
                        messages: prefixBreakdown.messages,
                      })}
                    </div>
                  </div>
                  <EntryList entries={prefixEntries} max={8} />
                  <p className="text-[11px] text-muted-foreground">{t("prefix.note")}</p>
                </>
              )}
            </CardContent>
          </Card>

          {/* 每条请求的分叉点 + 缓存命中率 */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium">{t("diverge.title")}</CardTitle>
            </CardHeader>
            <CardContent className="overflow-x-auto">
              <table className="w-full min-w-[860px] text-sm">
                <thead>
                  <tr className="border-b border-border text-left text-[11px] tracking-wide text-muted-foreground uppercase">
                    <th className="py-1.5 pr-3 font-normal">{t("table.id")}</th>
                    <th className="py-1.5 pr-3 font-normal">{t("table.status")}</th>
                    <th className="py-1.5 pr-3 font-normal">{t("table.model")}</th>
                    <th className="py-1.5 pr-3 font-normal">{t("table.diverge")}</th>
                    <th className="py-1.5 pr-3 font-normal">{t("table.cache")}</th>
                    <th className="py-1.5 pr-3 text-right font-normal">{t("table.tokens")}</th>
                    <th className="py-1.5 pr-3 text-right font-normal">{t("table.tps")}</th>
                    <th className="py-1.5 text-right font-normal">{t("table.first_chunk")}</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((item) => {
                    const d = divergenceOf(item.structure, prefixLen)
                    const rate =
                      item.log && cacheHitRate(item.log.prompt_tokens ?? 0, item.log.prompt_tokens_details?.cached_tokens ?? 0)
                    return (
                      <tr key={item.id} className="border-b border-border/60 last:border-0">
                        <td className="reading py-1.5 pr-3 text-xs">
                          <Link to={`/logs/${item.id}/chat-io`} className="text-primary underline-offset-2 hover:underline">
                            #{item.id}
                          </Link>
                        </td>
                        <td className="py-1.5 pr-3">
                          {item.log ? (
                            <StatusMark
                              status={item.log.Status}
                              label={t(`common:status.${item.log.Status}` as never, { defaultValue: item.log.Status })}
                            />
                          ) : (
                            <span className="text-xs text-muted-foreground">{t("table.missing")}</span>
                          )}
                        </td>
                        <td className="max-w-[140px] truncate py-1.5 pr-3 text-xs" title={item.log?.Name}>
                          {item.log?.Name ?? "-"}
                        </td>
                        <td className="py-1.5 pr-3 text-xs">
                          {item.error ? (
                            <span className="text-status-critical-ink">{t("table.load_failed")}</span>
                          ) : d.divergesAt === null ? (
                            <span className="text-status-good-ink">{t("table.no_diverge")}</span>
                          ) : (
                            <span>
                              {t("table.from_entry", { n: d.divergesAt + 1 })}
                              {d.entry && (
                                <span className="text-muted-foreground">
                                  {" · "}
                                  {entryLabel(d.entry, t)}
                                </span>
                              )}
                            </span>
                          )}
                        </td>
                        <td className="reading py-1.5 pr-3 text-xs">
                          {rate === null ? (
                            <span className="text-muted-foreground">—</span>
                          ) : (
                            formatPercent(rate, 0)
                          )}
                        </td>
                        <td className="reading py-1.5 pr-3 text-right text-xs">
                          {item.log ? formatNumber(item.log.total_tokens) : "-"}
                        </td>
                        <td className="reading py-1.5 pr-3 text-right text-xs">
                          {item.log?.Tps ? item.log.Tps.toFixed(1) : "-"}
                        </td>
                        <td className="reading py-1.5 text-right text-xs">
                          {item.log ? formatDurationNs(item.log.FirstChunkTime) : "-"}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </CardContent>
          </Card>

          {/* 逐条列出分叉之后的条目，方便直接看出差在哪 */}
          <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
            {items.map((item) => (
              <DivergenceCard key={item.id} item={item} prefixLen={prefixLen} />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function entryLabel(e: PromptEntry, t: (k: never, o?: never) => string): string {
  if (e.kind === "tool") return t("entry.tool" as never, { name: e.label } as never)
  if (e.kind === "system") return t("entry.system" as never)
  return t("entry.message" as never, { role: e.label } as never)
}

function kindIcon(kind: PromptEntry["kind"]) {
  if (kind === "tool") return <Wrench className="size-3" aria-hidden="true" />
  if (kind === "system") return <FileJson className="size-3" aria-hidden="true" />
  return <CircleDot className="size-3" aria-hidden="true" />
}

/**
 * 条目列表。
 *
 * 直接标注每一条的种类与名称，而不是只靠颜色区分——颜色在色盲下不可依赖，
 * 而"这是工具还是消息"恰恰是本页最关键的区分。
 */
function EntryList({ entries, max }: { entries: PromptEntry[]; max: number }) {
  const { t } = useTranslation("compare")
  const shown = entries.slice(0, max)
  return (
    <ol className="space-y-1">
      {shown.map((e, i) => (
        <li key={`${e.kind}-${e.label}-${i}`} className="flex items-center gap-2 text-xs">
          <span className="reading w-6 shrink-0 text-muted-foreground">{i + 1}</span>
          <Badge variant="secondary" className="shrink-0 gap-1 font-normal">
            {kindIcon(e.kind)}
            {entryLabel(e, t as never)}
          </Badge>
          <span className="min-w-0 flex-1 truncate text-muted-foreground" title={e.text}>
            {preview(e)}
          </span>
        </li>
      ))}
      {entries.length > max && (
        <li className="pl-8 text-[11px] text-muted-foreground">{t("more", { count: entries.length - max })}</li>
      )}
    </ol>
  )
}

/** 条目内容的简短预览。去掉用于比较的分隔符后截断。 */
function preview(e: PromptEntry): string {
  const body = e.text.includes("\u0000") ? e.text.split("\u0000").slice(1).join("\u0000") : e.text
  return body.replace(/\s+/g, " ").slice(0, 120)
}

function DivergenceCard({ item, prefixLen }: { item: Item; prefixLen: number }) {
  const { t } = useTranslation("compare")
  const entries = toEntries(item.structure)
  const d = divergenceOf(item.structure, prefixLen)
  const tail = d.divergesAt === null ? [] : entries.slice(d.divergesAt)

  return (
    <Card className="min-w-0">
      <CardHeader className="pb-2">
        <CardTitle className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <span className="reading">#{item.id}</span>
          {item.log && <span className="truncate font-normal text-muted-foreground">{item.log.Name}</span>}
          {item.log && (
            <span className="reading ml-auto text-[11px] font-normal text-muted-foreground">
              {formatFull(new Date(item.log.CreatedAt).getTime())}
            </span>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="min-w-0 space-y-2">
        {item.error ? (
          <p className="text-xs text-status-critical-ink">{item.error}</p>
        ) : entries.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("detail.no_content")}</p>
        ) : d.divergesAt === null ? (
          <p className="text-xs text-status-good-ink">{t("detail.contains_prefix")}</p>
        ) : (
          <>
            <p className="text-xs text-muted-foreground">
              {t("detail.share", { n: d.divergesAt, total: entries.length })}
            </p>
            <ol className="space-y-1 border-l border-border pl-3">
              {tail.slice(0, 12).map((e, i) => (
                <li key={`${e.kind}-${e.label}-${i}`} className={cn("flex items-center gap-2 text-xs")}>
                  <span className="reading w-6 shrink-0 text-muted-foreground">{prefixLen + i + 1}</span>
                  <Badge variant="outline" className="shrink-0 gap-1 font-normal">
                    {kindIcon(e.kind)}
                    {entryLabel(e, t as never)}
                  </Badge>
                  <span className="min-w-0 flex-1 truncate text-muted-foreground" title={e.text}>
                    {preview(e)}
                  </span>
                </li>
              ))}
            </ol>
            {tail.length > 12 && (
              <p className="pl-8 text-[11px] text-muted-foreground">{t("more", { count: tail.length - 12 })}</p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
