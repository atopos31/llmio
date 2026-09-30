import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  CHART_STYLES,
  type QuotaChartStyle,
  type QuotaOverride,
  type QuotaOverridePatch,
  type QuotaSourceResult,
} from "@/lib/quota"

/**
 * 数据源级展示设置。
 *
 * 与"数据源配置"（编辑器）是两件事，刻意分成两个入口：
 *   - 这里只改**怎么看**（名称、图表样式、备注、显隐）——纯前端偏好，
 *     写 localStorage，不影响取数，也不会上报服务端。
 *   - 编辑器改的是**怎么取**（接口、密钥、映射）——写配置文件。
 *
 * 合并成一个对话框会让人分不清"我这次改动会不会影响别人"。
 */
export function QuotaSourceViewDialog({
  open,
  onOpenChange,
  source,
  override,
  onChange,
  onReset,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  source: QuotaSourceResult
  override: QuotaOverride
  onChange: (patch: QuotaOverridePatch) => void
  /**
   * 清除该源的**全部**覆盖（含条目级）。必须是独立回调而不是
   * `onChange({...})`：patch 只认源级的四个字段，条目键（`id::item`）
   * 根本传不进去，仅靠 patch 重置会留下一半。
   */
  onReset: () => void
}) {
  const { t } = useTranslation(["quota", "common"])
  const [name, setName] = useState(override.name ?? "")
  const [note, setNote] = useState(override.note ?? "")

  useEffect(() => {
    if (!open) return
    setName(override.name ?? "")
    setNote(override.note ?? "")
  }, [open, override])

  // 空串表示"清除这条覆盖"，与 applyOverride 的规则一致
  const commit = () => {
    onChange({ name, note })
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("source_view.title")}</DialogTitle>
          <DialogDescription>{t("source_view.hint")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <Row label={t("source_view.source")}>
            <span className="reading text-sm">{source.name}</span>
          </Row>

          <Row label={t("source_view.display_name")}>
            <Input
              value={name}
              placeholder={t("source_view.display_name_placeholder")}
              onChange={(e) => setName(e.target.value)}
            />
          </Row>

          <Row label={t("source_view.chart_style")}>
            <Select
              value={override.chartStyle ?? "__inherit"}
              onValueChange={(v) =>
                onChange({ chartStyle: v === "__inherit" ? null : (v as QuotaChartStyle) })
              }
            >
              <SelectTrigger className="w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__inherit">{t("source_view.chart_style_placeholder")}</SelectItem>
                {CHART_STYLES.map((s) => (
                  <SelectItem key={s.value} value={s.value}>
                    {t(s.labelKey as never, { ns: "quota" })}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Row>

          <Row label={t("source_view.note")}>
            <Input
              value={note}
              placeholder={t("source_view.note_placeholder")}
              onChange={(e) => setNote(e.target.value)}
            />
          </Row>

          <Row label={t("source_view.hidden")}>
            <Switch
              checked={!!override.hidden}
              onCheckedChange={(v) => onChange({ hidden: v })}
            />
          </Row>
        </div>

        <DialogFooter className="sm:justify-between">
          <Button
            variant="ghost"
            className="text-status-critical-ink"
            onClick={() => {
              // 清除该源的全部覆盖（含 :: 条目级的），否则"重置"会留下一半
              onChange({ name: "", note: "", chartStyle: null, hidden: false })
              onReset()
              onOpenChange(false)
            }}
          >
            {t("source_view.reset")}
          </Button>
          <div className="flex gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common:actions.cancel")}
            </Button>
            <Button onClick={commit}>{t("common:actions.save")}</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[7rem_1fr] items-center gap-3">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <div className="min-w-0">{children}</div>
    </div>
  )
}
