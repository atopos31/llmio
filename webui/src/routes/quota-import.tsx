import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2, Plus, RefreshCw } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { discoverQuotaSources, importQuotaSource } from "@/lib/api"
import { sourceTypeKey, type QuotaUpstreamCandidate } from "@/lib/quota"

/**
 * 从上游 llmio 供应商导入数据源。
 *
 * ## 密钥不经过浏览器
 *
 * 这张表只显示**掩码**，导入时也只提交 `upstreamId`——真实密钥由服务端
 * 直接从上游配置取。因此这里不会（也不该）出现任何密钥输入框：
 * 一旦让浏览器经手密钥，它就会进浏览器的历史、扩展和内存。
 *
 * ## 登录型适配器以禁用状态导入
 *
 * scnet / opencode 需要账号会话，导入时拿不到凭据，因此导入后是禁用的，
 * 并在界面上明说"还需补一步"。启用一个必然失败的源只会让面板一直报错。
 */
export function QuotaImportDialog({
  open,
  onOpenChange,
  onImported,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (id: number) => void
}) {
  const { t } = useTranslation(["quota", "common"])
  const [items, setItems] = useState<QuotaUpstreamCandidate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [importing, setImporting] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setItems(await discoverQuotaSources())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  const doImport = async (c: QuotaUpstreamCandidate) => {
    setImporting(c.upstreamId)
    try {
      await importQuotaSource(c.upstreamId)
      toast.success(t("import.imported_ok", { name: c.name, defaultValue: c.name }))
      onImported(c.upstreamId)
      // 重新拉一遍：让这一行的状态变成"已导入"，而不是等下次打开对话框
      await load()
    } catch (err) {
      toast.error(t("error.save"), {
        description: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setImporting(null)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{t("import.title")}</DialogTitle>
          <DialogDescription>{t("import.desc")}</DialogDescription>
        </DialogHeader>

        <div className="min-h-0 py-2">
          {loading ? (
            <div className="flex flex-col gap-2">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-9" />
              ))}
            </div>
          ) : error ? (
            <div className="flex flex-col items-start gap-2 py-6">
              <p className="text-sm text-status-critical-ink">{error}</p>
              <Button variant="outline" size="sm" onClick={() => void load()}>
                <RefreshCw className="size-3.5" />
                {t("refresh")}
              </Button>
            </div>
          ) : items.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              {t("import.empty")}
            </p>
          ) : (
            <div className="max-h-[60vh] overflow-auto rounded-md border border-border">
              <Table>
                <TableHeader className="sticky top-0 z-10 bg-muted">
                  <TableRow className="hover:bg-muted">
                    <TableHead>{t("import.name")}</TableHead>
                    <TableHead>{t("import.base_url")}</TableHead>
                    <TableHead>{t("import.api_key")}</TableHead>
                    <TableHead>{t("import.suggested")}</TableHead>
                    <TableHead className="text-right">{t("import.status")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((c) => (
                    <TableRow key={c.upstreamId}>
                      <TableCell className="font-medium">{c.name}</TableCell>
                      <TableCell className="reading max-w-[16rem] truncate text-xs">
                        {c.baseUrl || "—"}
                      </TableCell>
                      <TableCell className="reading text-xs">
                        {c.hasApiKey ? c.apiKeyMasked : t("import.no_key")}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col gap-1">
                          <Badge variant="outline" className="w-fit font-normal">
                            {t(sourceTypeKey(c.suggested.type) as never, {
                              ns: "quota",
                              defaultValue: c.suggested.type,
                            })}
                            {c.suggested.builtin ? ` · ${c.suggested.builtin}` : ""}
                          </Badge>
                          {c.suggested.note && (
                            <span className="text-[11px] text-muted-foreground">
                              {c.suggested.note}
                            </span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-right">
                        {c.alreadyImported ? (
                          <Badge variant="secondary" className="font-normal">
                            {t("import.imported")}
                          </Badge>
                        ) : (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={importing !== null}
                            onClick={() => void doImport(c)}
                          >
                            {importing === c.upstreamId ? (
                              <Loader2 className="size-3.5 animate-spin" />
                            ) : (
                              <Plus className="size-3.5" />
                            )}
                            {importing === c.upstreamId
                              ? t("import.importing")
                              : t("import.import")}
                          </Button>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </div>

        <DialogFooter className="sm:justify-between">
          <span className="text-[11px] text-muted-foreground">{t("import.note_hint")}</span>
          <div className="flex gap-2">
            <Button variant="ghost" size="sm" disabled={loading} onClick={() => void load()}>
              <RefreshCw className="size-3.5" />
              {t("refresh")}
            </Button>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common:actions.close")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
