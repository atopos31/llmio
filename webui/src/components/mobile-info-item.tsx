import type { ReactNode } from "react"

type MobileInfoItemProps = {
  label: string
  value: ReactNode
}

/**
 * 手机卡片里的一格信息：上标签、下取值。
 *
 * 从模型路由页原地提升上来，是因为密钥页有一份逐字相同的副本——两份
 * 各自演化过一次（副本多出一个从未被传过 `mono` 的参数），正是这类
 * "复制一份小东西"的典型结局。
 */
export const MobileInfoItem = ({ label, value }: MobileInfoItemProps) => (
  <div className="space-y-1">
    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{label}</p>
    <div className="text-sm font-medium break-words">{value}</div>
  </div>
)
