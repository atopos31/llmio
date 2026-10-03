import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { updateQuotaConfig } from "@/lib/api"
import type { QuotaConfigResponse } from "@/lib/quota"

/**
 * 全局设置：缓存时长与告警阈值。
 *
 * 两者都是**服务端**配置（写进 quota.config.json），不是浏览器偏好：
 * 缓存时长决定上游被打多勤，告警阈值决定"告警"是什么意思——同一份配置的
 * 所有使用者必须看到同一个答案，因此它们不能像图表样式那样存在 localStorage。
 *
 * 最小值 10 秒与服务端的 MinCacheTTL 对齐：即便这里填 1，服务端也不会
 * 真的每秒去打一遍上游，所以让控件就不允许填更小的值，免得给出
 * 一个不会生效的假选项。
 */
export function QuotaSettingsDialog({
  open,
  onOpenChange,
  config,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  config: QuotaConfigResponse
  onSaved: () => void
}) {
  const { t } = useTranslation(["quota", "common"])
  const [refresh, setRefresh] = useState(config.config.refreshInterval)
  const [warning, setWarning] = useState(config.config.warningAt)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    setRefresh(config.config.refreshInterval)
    setWarning(config.config.warningAt)
  }, [open, config])

  const save = async () => {
    setSaving(true)
    try {
      await updateQuotaConfig({ refreshInterval: refresh, warningAt: warning })
      toast.success(t("settings.title"))
      onOpenChange(false)
      onSaved()
    } catch (err) {
      toast.error(t("error.save"), {
        description: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setSaving(false)
    }
  }

  const invalid = refresh < 10 || warning <= 0 || warning > 100

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* 只有标题没有说明文字，显式声明"没有描述"：不写的话 Radix 会
          在控制台告警，而告警多了就没人看了。 */}
      <DialogContent className="sm:max-w-md" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{t("settings.title")}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted-foreground">
              {t("settings.refresh_interval")}
            </span>
            <Input
              type="number"
              min={10}
              className="w-32"
              value={refresh}
              onChange={(e) => setRefresh(Number(e.target.value))}
            />
            <span className="text-[11px] text-muted-foreground">
              {t("settings.refresh_interval_hint")}
            </span>
          </div>

          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted-foreground">
              {t("settings.warning_at")}
            </span>
            <Input
              type="number"
              min={1}
              max={100}
              className="w-32"
              value={warning}
              onChange={(e) => setWarning(Number(e.target.value))}
            />
            <span className="text-[11px] text-muted-foreground">
              {t("settings.warning_at_hint")}
            </span>
          </div>

          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted-foreground">
              {t("settings.config_path")}
            </span>
            <code className="reading rounded-sm border border-border bg-muted/40 px-2 py-1 text-xs break-all">
              {config.configPath}
            </code>
            <span className="text-[11px] text-muted-foreground">{t("source_file")}</span>
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common:actions.cancel")}
          </Button>
          <Button disabled={saving || invalid} onClick={() => void save()}>
            {saving && <Loader2 className="size-3.5 animate-spin" />}
            {t("common:actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
