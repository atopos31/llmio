import { useEffect, useId, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
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
import {
  updateProviderForward,
  type ForwardModeSetting,
  type ForwardRewriteSetting,
  type Provider,
} from "@/lib/api"
import { cn } from "@/lib/utils"
import {
  forwardBaseUrl,
  forwardProtocol,
  forwardProviderMode,
  forwardProviderRewrite,
  isValidForwardSlug,
} from "./forwarding-utils"

/**
 * 单个提供商的转发设置。
 *
 * 与提供商表单分开是这次改造的核心：名称、配置属于提供商 CRUD；地址标识、
 * 转发模式、改写策略属于"访问入口"的配置，各自单独保存。合并在一张表单里时，
 * 改一次名称或 base_url 就会把转发设置一起冲掉（旧后端的 Select 全量更新），
 * 而且"名称同时是地址"让改名必然丢地址。
 */

const MODE_ORDER: ForwardModeSetting[] = ["off", "mixed", "pure"]
const REWRITE_ORDER: ForwardRewriteSetting[] = ["none", "known", "all"]

const MODE_TITLE_KEY = {
  off: "settings.mode_off_title",
  mixed: "settings.mode_mixed_title",
  pure: "settings.mode_pure_title",
} as const

const MODE_DESC_KEY = {
  off: "settings.mode_off_desc",
  mixed: "settings.mode_mixed_desc",
  pure: "settings.mode_pure_desc",
} as const

const REWRITE_TITLE_KEY = {
  none: "settings.rewrite_none_title",
  known: "settings.rewrite_known_title",
  all: "settings.rewrite_all_title",
} as const

const REWRITE_DESC_KEY = {
  none: "settings.rewrite_none_desc",
  known: "settings.rewrite_known_desc",
  all: "settings.rewrite_all_desc",
} as const

const PROTOCOL_HINT_KEY = {
  openai: "settings.protocol_hint_openai",
  anthropic: "settings.protocol_hint_anthropic",
  gemini: "settings.protocol_hint_gemini",
} as const

export function ForwardingSettingsDialog({
  open,
  provider,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  provider: Provider | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation("forwarding")
  const slugId = useId()
  const targetId = useId()

  const [slug, setSlug] = useState("")
  const [mode, setMode] = useState<ForwardModeSetting>("off")
  const [rewrite, setRewrite] = useState<ForwardRewriteSetting>("known")
  const [targetModel, setTargetModel] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  // 每次打开都从 provider 重新回填：弹窗在关闭时仍挂载，不重置会带着上一次编辑的值
  useEffect(() => {
    if (!open || !provider) return
    setSlug(provider.ForwardSlug ?? "")
    setMode(forwardProviderMode(provider))
    setRewrite(forwardProviderRewrite(provider))
    setTargetModel(provider.ForwardTargetModel ?? "")
    setError(null)
    setSaving(false)
  }, [open, provider])

  const trimmedSlug = slug.trim()
  const trimmedTarget = targetModel.trim()
  const slugValid = isValidForwardSlug(trimmedSlug)
  const previewUrl =
    provider && slugValid ? forwardBaseUrl(window.location.origin, provider.Type, trimmedSlug) : null
  const slugChanged = Boolean(provider?.ForwardSlug) && trimmedSlug !== (provider?.ForwardSlug ?? "")

  const handleSave = async () => {
    if (!provider || saving) return

    // 客户端校验与服务端同规则：先在本地给出可读的原因，避免用户点了保存
    // 才收到一个 400。服务端仍会再校验一次（这里只是提前反馈）。
    if (mode !== "off" && !trimmedSlug) {
      setError(t("settings.slug_required"))
      return
    }
    if (trimmedSlug && !slugValid) {
      setError(t("settings.slug_invalid"))
      return
    }
    if (rewrite === "all" && !trimmedTarget) {
      setError(t("settings.target_required"))
      return
    }

    setSaving(true)
    setError(null)
    try {
      await updateProviderForward(provider.ID, {
        slug: trimmedSlug,
        mode,
        rewrite,
        target_model: rewrite === "all" ? trimmedTarget : "",
      })
      toast.success(t("settings.save_success", { name: provider.Name }))
      onOpenChange(false)
      onSaved()
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      toast.error(t("settings.save_failed", { message }))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {t("settings.title", { name: provider?.Name ?? "" })}
          </DialogTitle>
          <DialogDescription>{t("settings.desc")}</DialogDescription>
        </DialogHeader>

        <div className="max-h-[65vh] space-y-5 overflow-y-auto pr-1">
          <div className="space-y-2">
            <Label htmlFor={slugId} className="text-sm">
              {t("settings.slug_label")}
            </Label>
            <div className="flex items-center gap-1">
              <span className="font-mono text-xs text-muted-foreground">
                {window.location.origin}/p/
              </span>
              <Input
                id={slugId}
                value={slug}
                onChange={(event) => setSlug(event.target.value)}
                placeholder={t("settings.slug_placeholder")}
                spellCheck={false}
                autoComplete="off"
                className="h-8 font-mono text-xs"
              />
            </div>
            <p className="text-xs text-muted-foreground">{t("settings.slug_hint")}</p>
            {mode === "off" ? (
              <p className="text-xs text-muted-foreground">{t("settings.slug_off_hint")}</p>
            ) : null}
            {slugChanged ? (
              <p className="text-xs text-status-warning-ink">
                {t("settings.slug_change_warning")}
              </p>
            ) : null}
          </div>

          <div className="rounded-md border bg-muted/40 p-3 text-xs">
            <p className="font-medium">{t("settings.address_preview")}</p>
            <p className="mt-1 break-all font-mono">
              {previewUrl ?? t("settings.address_preview_empty")}
            </p>
            <p className="mt-1 text-muted-foreground">
              {t(PROTOCOL_HINT_KEY[forwardProtocol(provider?.Type ?? "")])}
            </p>
            <p className="text-muted-foreground">{t("settings.address_preview_hint")}</p>
          </div>

          <div className="space-y-2">
            <p className="text-sm font-medium">{t("settings.mode_label")}</p>
            <p className="text-xs text-muted-foreground">{t("settings.mode_hint")}</p>
            <div role="radiogroup" aria-label={t("settings.mode_label")} className="grid gap-2 sm:grid-cols-3">
              {MODE_ORDER.map((item) => (
                <button
                  key={item}
                  type="button"
                  role="radio"
                  aria-checked={mode === item}
                  onClick={() => setMode(item)}
                  className={cn(
                    "flex flex-col gap-1 rounded-md border p-3 text-left transition-colors",
                    mode === item
                      ? "border-primary bg-primary/5"
                      : "hover:bg-muted",
                  )}
                >
                  <span className="text-sm font-medium">{t(MODE_TITLE_KEY[item])}</span>
                  <span className="text-xs text-muted-foreground">{t(MODE_DESC_KEY[item])}</span>
                </button>
              ))}
            </div>
          </div>

          <div className="space-y-2">
            <p className="text-sm font-medium">{t("settings.rewrite_label")}</p>
            <p className="text-xs text-muted-foreground">{t("settings.rewrite_hint")}</p>
            <div role="radiogroup" aria-label={t("settings.rewrite_label")} className="grid gap-2 sm:grid-cols-3">
              {REWRITE_ORDER.map((item) => (
                <button
                  key={item}
                  type="button"
                  role="radio"
                  aria-checked={rewrite === item}
                  onClick={() => setRewrite(item)}
                  className={cn(
                    "flex flex-col gap-1 rounded-md border p-3 text-left transition-colors",
                    rewrite === item
                      ? "border-primary bg-primary/5"
                      : "hover:bg-muted",
                  )}
                >
                  <span className="text-sm font-medium">{t(REWRITE_TITLE_KEY[item])}</span>
                  <span className="text-xs text-muted-foreground">{t(REWRITE_DESC_KEY[item])}</span>
                </button>
              ))}
            </div>
          </div>

          {rewrite === "all" ? (
            <div className="space-y-2">
              <Label htmlFor={targetId} className="text-sm">
                {t("settings.target_label")}
              </Label>
              <Input
                id={targetId}
                value={targetModel}
                onChange={(event) => setTargetModel(event.target.value)}
                placeholder={t("settings.target_placeholder")}
                spellCheck={false}
                autoComplete="off"
                className="h-8 font-mono text-xs"
              />
              <p className="text-xs text-muted-foreground">{t("settings.target_hint")}</p>
            </div>
          ) : null}

          {error ? (
            <p role="alert" className="text-xs text-destructive">
              {error}
            </p>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            {t("settings.cancel")}
          </Button>
          <Button onClick={() => void handleSave()} disabled={saving}>
            {saving ? t("settings.saving") : t("settings.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
