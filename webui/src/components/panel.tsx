import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { cn } from "@/lib/utils"

/**
 * 带标题（与可选口径说明）的内容面板。
 *
 * `note` 不是装饰：图表旁边那句"分位数取最近秩"、"桶为固定区间"是**读数的
 * 前提**，抽掉它数字就变成不可验证的断言。因此它和标题同属头部，
 * 不塞进正文。
 */
export function Panel({
  title,
  note,
  className,
  bodyClassName,
  children,
}: {
  title: string
  note?: string
  className?: string
  /** 正文区的补充类：整块列表要撑满高度时用（`flex-1 min-h-0 p-0`） */
  bodyClassName?: string
  children: React.ReactNode
}) {
  return (
    <Card className={cn("min-w-0", className)}>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {note && <p className="text-[11px] text-muted-foreground">{note}</p>}
      </CardHeader>
      <CardContent className={cn("min-w-0", bodyClassName)}>{children}</CardContent>
    </Card>
  )
}
