import { cn } from "@/lib/utils"

/**
 * 骨架屏。
 *
 * 用它而不是转圈：加载中的版面应当**占住真实内容的位置**，
 * 内容到位时不会把页面顶得跳一下。转圈则会让首屏高度先塌后撑，
 * 在快刷新（余量面板 2 分钟一刷）的场景里尤其刺眼。
 *
 * 动画用 `animate-pulse`（透明度呼吸）而不是 shimmer 扫光：
 * 后者在深色面上很容易亮得刺眼，而本项目的深色是主场景之一。
 */
function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      aria-hidden="true"
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  )
}

export { Skeleton }
