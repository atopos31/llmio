import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { Link } from "react-router-dom"
import { BookOpen, Copy, Settings2 } from "lucide-react"
import { toast } from "sonner"

import { Panel } from "@/components/panel"
import { ErrorState, ListSkeleton } from "@/components/state-views"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { getProviders, type ForwardModeSetting, type Provider } from "@/lib/api"
import { copyToClipboard } from "@/lib/utils"
import { ForwardingGuideDialog } from "@/routes/forwarding/forwarding-guide-dialog"
import { ForwardingSettingsDialog } from "@/routes/forwarding/forwarding-settings-dialog"
import {
  forwardBaseUrl,
  forwardProviderMode,
  forwardProviderRewrite,
} from "@/routes/forwarding/forwarding-utils"
import {
  hasSeenForwardingGuide,
  markForwardingGuideSeen,
} from "@/routes/forwarding/guide-storage"

/**
 * 转发透传专页。
 *
 * 这一页把"访问入口"从提供商 CRUD 里独立出来：
 * - 地址标识（slug）与提供商名称解耦：改名不影响地址，换地址也不必动名称；
 * - 转发模式（关闭/混合/纯转发）与改写策略（none/known/all）放在这里，
 *   保存走独立的 PUT /api/providers/:id/forward，改提供商配置不会再把它们冲掉；
 * - 地址按协议生成（Anthropic 不带 /v1），并在首次进入时给出可重开的说明弹窗。
 *
 * 页面不做筛选：需要配置转发的提供商本来就不多，一屏看完比先搜再改更直接。
 */

const MODE_BADGE_VARIANT: Record<ForwardModeSetting, "default" | "secondary" | "outline"> = {
  off: "outline",
  mixed: "secondary",
  pure: "default",
}

const MODE_LABEL_KEY = {
  off: "mode.off",
  mixed: "mode.mixed",
  pure: "mode.pure",
} as const

/** 已配置的专属地址（客户端要填的 base_url）；未设置 slug 时返回 null。 */
function providerBaseUrl(provider: Provider): string | null {
  if (!provider.ForwardSlug) return null
  return forwardBaseUrl(window.location.origin, provider.Type, provider.ForwardSlug)
}

/** 改写策略的展示文案。all 未配目标时把缺失说出来，而不是显示"完全改写"让人以为已生效。 */
function rewriteLabel(provider: Provider, t: TFunction<"forwarding">): string {
  const rewrite = forwardProviderRewrite(provider)
  if (rewrite === "none") return t("rewrite.none")
  if (rewrite === "known") return t("rewrite.known")
  return provider.ForwardTargetModel
    ? t("rewrite.all_target", { target: provider.ForwardTargetModel })
    : t("rewrite.all_missing")
}

export default function ForwardingPage() {
  const { t } = useTranslation("forwarding")
  const [providers, setProviders] = useState<Provider[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null)
  // 首次进入自动弹说明；已读标记在 localStorage，关闭弹窗时才写入
  const [guideOpen, setGuideOpen] = useState(() => !hasSeenForwardingGuide())

  const fetchProviders = useCallback(async () => {
    try {
      setLoading(true)
      setLoadError(null)
      setProviders(await getProviders())
    } catch (err) {
      console.error(err)
      setLoadError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchProviders()
  }, [fetchProviders])

  const handleGuideOpenChange = (open: boolean) => {
    setGuideOpen(open)
    if (!open) markForwardingGuideSeen()
  }

  const copyAddress = async (provider: Provider) => {
    const url = providerBaseUrl(provider)
    if (!url) return
    try {
      await copyToClipboard(url)
      toast.success(t("address_copied", { url }))
    } catch {
      toast.error(t("copy_failed"))
    }
  }

  const enabledCount = providers.filter((provider) => forwardProviderMode(provider) !== "off").length

  const addressButton = (provider: Provider) => {
    const url = providerBaseUrl(provider)
    if (!url) {
      return <span className="text-xs text-muted-foreground">{t("address_unset")}</span>
    }
    return (
      <button
        type="button"
        title={t("address_copy")}
        onClick={() => void copyAddress(provider)}
        className="inline-flex max-w-[520px] items-start gap-1.5 rounded border border-border px-2 py-1 text-left font-mono text-xs hover:bg-muted"
      >
        <span className="min-w-0 whitespace-normal break-all" title={url}>{url}</span>
        <Copy className="mt-0.5 size-3 shrink-0" aria-hidden="true" />
      </button>
    )
  }

  const modeBadge = (provider: Provider) => {
    const mode = forwardProviderMode(provider)
    return (
      <Badge variant={MODE_BADGE_VARIANT[mode]} className="text-[11px]">
        {t(MODE_LABEL_KEY[mode])}
      </Badge>
    )
  }

  const settingsButton = (provider: Provider, compact = false) => (
    <Button
      variant="outline"
      size={compact ? "icon" : "sm"}
      className={compact ? "h-7 w-7" : "gap-1.5"}
      aria-label={t("actions.settings_for", { name: provider.Name })}
      title={t("actions.settings")}
      onClick={() => setEditingProvider(provider)}
    >
      <Settings2 className="size-3.5" />
      {compact ? null : t("actions.settings")}
    </Button>
  )

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-shrink-0 flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-xl font-semibold tracking-tight">{t("title")}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">{t("subtitle")}</p>
        </div>
        <Button variant="outline" size="sm" className="gap-1.5" onClick={() => setGuideOpen(true)}>
          <BookOpen className="size-3.5" aria-hidden="true" />
          {t("guide_button")}
        </Button>
      </div>

      {loadError ? (
        <ErrorState
          title={t("load_failed")}
          message={loadError}
          retryLabel={t("retry")}
          onRetry={() => void fetchProviders()}
        />
      ) : (
        <Panel
          title={t("list_title")}
          note={
            enabledCount > 0
              ? `${t("count", { total: providers.length })} · ${t("count_forwarding", { count: enabledCount })}`
              : t("count", { total: providers.length })
          }
          className="flex min-h-0 flex-1 flex-col"
          bodyClassName="flex min-h-0 flex-1 flex-col p-0"
        >
          {loading ? (
            <ListSkeleton label={t("loading")} />
          ) : providers.length === 0 ? (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
              <div>
                <p className="text-sm font-medium">{t("no_data")}</p>
                <p className="mt-1 text-xs text-muted-foreground">{t("no_data_hint")}</p>
              </div>
              <Button asChild variant="outline" size="sm">
                <Link to="/providers">{t("go_providers")}</Link>
              </Button>
            </div>
          ) : (
            <div className="flex h-full flex-col">
              <div className="hidden flex-1 overflow-y-auto sm:block">
                <Table className="min-w-[900px]">
                  <TableHeader className="sticky top-0 z-10 bg-secondary/80 text-secondary-foreground">
                    <TableRow>
                      <TableHead>{t("table.provider")}</TableHead>
                      <TableHead>{t("table.address")}</TableHead>
                      <TableHead>{t("table.mode")}</TableHead>
                      <TableHead>{t("table.rewrite")}</TableHead>
                      <TableHead className="text-right">{t("table.actions")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {providers.map((provider) => (
                      <TableRow key={provider.ID}>
                        <TableCell>
                          <div className="font-medium">{provider.Name}</div>
                          <div className="text-[11px] text-muted-foreground">{provider.Type || t("unknown_type")}</div>
                        </TableCell>
                        <TableCell>{addressButton(provider)}</TableCell>
                        <TableCell>{modeBadge(provider)}</TableCell>
                        <TableCell className="text-xs text-muted-foreground">{rewriteLabel(provider, t)}</TableCell>
                        <TableCell className="text-right">
                          <div className="flex justify-end">{settingsButton(provider)}</div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>

              <div className="min-h-0 flex-1 divide-y divide-border overflow-y-auto px-2 py-3 sm:hidden">
                {providers.map((provider) => (
                  <div key={provider.ID} className="space-y-2 py-3">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <h3 className="truncate text-sm font-semibold">{provider.Name}</h3>
                        <p className="text-[11px] text-muted-foreground">
                          {provider.Type || t("unknown_type")}
                        </p>
                      </div>
                      {settingsButton(provider, true)}
                    </div>
                    <div className="min-w-0">{addressButton(provider)}</div>
                    <div className="flex flex-wrap items-center gap-2 text-xs">
                      {modeBadge(provider)}
                      <span className="text-muted-foreground">{rewriteLabel(provider, t)}</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </Panel>
      )}

      <ForwardingGuideDialog open={guideOpen} onOpenChange={handleGuideOpenChange} />
      <ForwardingSettingsDialog
        open={editingProvider !== null}
        provider={editingProvider}
        onOpenChange={(open) => {
          if (!open) setEditingProvider(null)
        }}
        onSaved={() => void fetchProviders()}
      />
    </div>
  )
}
