import { AlertTriangle, RefreshCw } from "lucide-react"
import type { ReactNode } from "react"

import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"

/**
 * 空态与失败态。
 *
 * 原先分析页、首页、额度页各有一份，差别只是多一个按钮位与一处间距。
 * 合成一份的理由不是省行数，而是"失败必须给原文"这条规矩：它已经在
 * 分析页写清楚了，散成三份就迟早有一份走样——额度页那份把加载失败
 * 塞进了空态的 desc 里，靠一份共享定义才看得出哪里不对。
 */

/** 空态：说明这里本来就没有内容，或给出去处 */
export function EmptyState({
  title,
  hint,
  action,
}: {
  title: string
  hint?: ReactNode
  action?: ReactNode
}) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center gap-1 py-16 text-center">
        <p className="text-sm font-medium">{title}</p>
        {hint ? <p className="max-w-md text-xs text-muted-foreground">{hint}</p> : null}
        {action ? <div className="mt-2">{action}</div> : null}
      </CardContent>
    </Card>
  )
}

/** 失败态。给出**原文**而不是"加载失败"四个字：原文才可能指向原因。 */
export function ErrorState({
  title,
  message,
  retryLabel,
  onRetry,
}: {
  title: string
  message: string
  retryLabel: string
  onRetry: () => void
}) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center gap-2 py-12 text-center">
        <AlertTriangle className="size-5 text-status-critical-ink" aria-hidden="true" />
        <p className="text-sm font-medium">{title}</p>
        <p className="reading max-w-full break-words text-xs text-muted-foreground">{message}</p>
        <Button variant="outline" size="sm" className="mt-1 gap-1.5" onClick={onRetry}>
          <RefreshCw className="size-3.5" aria-hidden="true" />
          {retryLabel}
        </Button>
      </CardContent>
    </Card>
  )
}
