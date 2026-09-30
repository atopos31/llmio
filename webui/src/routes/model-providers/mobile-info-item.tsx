import type { ReactNode } from "react"

type MobileInfoItemProps = {
  label: string
  value: ReactNode
}

/** 手机卡片里的一格信息：上标签、下取值。模型列表与关联列表两套卡片共用 */
export const MobileInfoItem = ({ label, value }: MobileInfoItemProps) => (
  <div className="space-y-1">
    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{label}</p>
    <div className="text-sm font-medium break-words">{value}</div>
  </div>
)
