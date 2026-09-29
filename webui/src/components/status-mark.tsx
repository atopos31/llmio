import { CheckCircle2, CircleDashed, XCircle } from "lucide-react"
import type { ComponentType } from "react"

import { cn } from "@/lib/utils"

/**
 * 请求状态标记。
 *
 * **图标 + 文字 + 颜色三者同时出现**，这是刻意的、不可省略的：
 * 状态色在浅色面上的对比度不足以单独承载含义（warning / serious 低于 3:1
 * 是调色板的既定取舍），而且色盲用户无法依赖红绿对立。
 * 因此本组件不提供"只显示颜色点"的用法——那会让状态信息对部分人完全丢失。
 */
export type RequestStatus = "success" | "running" | "error" | string

const STATUS_META: Record<
  "success" | "running" | "error" | "unknown",
  { icon: ComponentType<{ className?: string }>; tone: string; key: string }
> = {
  success: {
    icon: CheckCircle2,
    tone: "text-status-good-ink",
    key: "status.success",
  },
  error: {
    icon: XCircle,
    tone: "text-status-critical-ink",
    key: "status.error",
  },
  running: {
    // 在途用虚线圆而非实心点：它是"还没结束"，不是一种结果
    icon: CircleDashed,
    tone: "text-status-warning-ink",
    key: "status.running",
  },
  unknown: {
    icon: CircleDashed,
    tone: "text-muted-foreground",
    key: "status.unknown",
  },
}

function metaFor(status: RequestStatus) {
  if (status === "success" || status === "error" || status === "running") {
    return STATUS_META[status]
  }
  return STATUS_META.unknown
}

export function StatusMark({
  status,
  label,
  className,
}: {
  status: RequestStatus
  /** 已翻译的文案。不在此处调用 t() 是为了让组件与 i18n 解耦。 */
  label: string
  className?: string
}) {
  const meta = metaFor(status)
  const Icon = meta.icon
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <Icon className={cn("size-3.5 shrink-0", meta.tone)} aria-hidden="true" />
      <span className="text-sm">{label}</span>
    </span>
  )
}

/**
 * 状态对应的文字色类名，供表格单元格直接使用。
 *
 * 与 StatusMark 不同处一个模块：react-refresh 要求组件文件只导出组件，
 * 混导出函数会让热更新失效。此处定点豁免而不是放宽仓库配置，
 * 因为拆开会让 STATUS_META 这份单一真相来源被复制。
 */
// eslint-disable-next-line react-refresh/only-export-components
export function statusToneClass(status: RequestStatus): string {
  return metaFor(status).tone
}
