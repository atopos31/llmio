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
import { Switch } from "@/components/ui/switch"
import {
  defaultFormat,
  FORMAT_TOKENS,
  renderItemText,
  type QuotaItem,
  type QuotaOverride,
  type QuotaOverridePatch,
  type QuotaSourceResult,
} from "@/lib/quota"
import { cn } from "@/lib/utils"

/**
 * 单条余量的自定义：显示名称、显示格式、是否隐藏。
 *
 * ## 为什么实时预览必须走本地镜像
 *
 * 用户在这里调格式时，每敲一个字符都想知道结果。走一趟服务端意味着
 * 每次按键等一个 RTT——那不是"编辑"，那是"提交"。因此预览用
 * `@/lib/quota` 的镜像引擎算，而**保存后的正式渲染仍以服务端下发的
 * `text` 为准**（服务端算得对，前端这一份只服务于"尚未保存"的预览）。
 *
 * ## token 芯片是点击插入而不是拖拽
 *
 * 模板语法光靠占位符提示学不会，给一排可点的芯片，用户点一下就知道
 * `{used:2}` 长什么样。
 */
export function QuotaItemDialog({
  open,
  onOpenChange,
  source,
  item,
  override,
  onChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  source: QuotaSourceResult
  item: QuotaItem
  override: QuotaOverride
  onChange: (patch: QuotaOverridePatch) => void
}) {
  const { t } = useTranslation(["quota", "common"])
  const [label, setLabel] = useState(override.label ?? "")
  const [format, setFormat] = useState(override.format ?? "")

  useEffect(() => {
    if (!open) return
    setLabel(override.label ?? "")
    setFormat(override.format ?? "")
  }, [open, override])

  // 预览里的名称也要反映正在编辑的值，否则改了名字预览不动，很困惑
  const previewItem: QuotaItem = { ...item, label: label || item.label }
  const preview = renderItemText(previewItem, format || undefined)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("item.title")}</DialogTitle>
          <DialogDescription>
            {source.name} · {item.label}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <Row label={t("item.display_name")}>
            <Input
              value={label}
              placeholder={item.label}
              onChange={(e) => setLabel(e.target.value)}
            />
          </Row>

          <div className="flex flex-col gap-2">
            <span className="text-xs font-medium text-muted-foreground">
              {t("item.format")}
            </span>
            <Input
              className="reading"
              value={format}
              placeholder={item.format || defaultFormat(item)}
              onChange={(e) => setFormat(e.target.value)}
            />

            {/* token 芯片：点一下插到格式末尾 */}
            <div className="flex flex-wrap gap-1.5">
              {FORMAT_TOKENS.map((tok) => (
                <button
                  key={tok.token}
                  type="button"
                  className={cn(
                    "reading rounded-sm border border-border px-1.5 py-0.5 text-[11px]",
                    "hover:bg-accent focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none"
                  )}
                  title={t(`item.token_${tok.key}` as never, { defaultValue: tok.token })}
                  onClick={() => setFormat((f) => f + tok.token)}
                >
                  {tok.token}
                </button>
              ))}
              {/* 精度后缀单独给一个：它是语法里最容易忘的一部分 */}
              <button
                type="button"
                className="reading rounded-sm border border-border px-1.5 py-0.5 text-[11px] hover:bg-accent"
                onClick={() => setFormat((f) => f + "{used:2}")}
              >
                {"{used:2}"}
              </button>
            </div>

            <span className="text-[11px] text-muted-foreground">{t("item.format_hint")}</span>
          </div>

          <Row label={t("item.preview")}>
            <span className="reading text-sm font-semibold">{preview || "—"}</span>
          </Row>

          <Row label={t("item.hidden")}>
            <div className="flex items-center gap-2">
              <Switch
                checked={!!override.hidden}
                onCheckedChange={(v) => onChange({ hidden: v })}
              />
              <span className="text-[11px] text-muted-foreground">
                {t("item.hidden_hint")}
              </span>
            </div>
          </Row>

          <p className="text-[11px] text-muted-foreground">
            {t("item.chart_style_hint")}
          </p>
        </div>

        <DialogFooter className="sm:justify-between">
          <Button
            variant="ghost"
            className="text-status-critical-ink"
            onClick={() => {
              onChange({ label: "", format: "", hidden: false })
              onOpenChange(false)
            }}
          >
            {t("item.reset")}
          </Button>
          <div className="flex gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common:actions.cancel")}
            </Button>
            <Button
              onClick={() => {
                onChange({ label, format })
                onOpenChange(false)
              }}
            >
              {t("common:actions.save")}
            </Button>
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
