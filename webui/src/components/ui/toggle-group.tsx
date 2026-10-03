import * as React from "react"
import * as ToggleGroupPrimitive from "@radix-ui/react-toggle-group"

import { cn } from "@/lib/utils"

/**
 * 分段控件。
 *
 * 用 ToggleGroup 而不是 Tabs：这里切换的是**同一视图的参数**
 * （时间范围、图表样式、请求状态），不是换一屏内容。用 Tabs 会
 * 让读屏把每个选项都念成"标签页"，并期待下方内容整体替换。
 *
 * `variant="outline"` 用于筛选行，`variant="default"`（无边框底）用于
 * 卡片头部那种更轻的场合。
 */
function ToggleGroup({
  className,
  variant = "default",
  size = "default",
  children,
  ...props
}: React.ComponentProps<typeof ToggleGroupPrimitive.Root> & {
  variant?: "default" | "outline"
  size?: "default" | "sm"
}) {
  return (
    <ToggleGroupPrimitive.Root
      data-slot="toggle-group"
      data-variant={variant}
      data-size={size}
      className={cn(
        "group/toggle-group flex w-fit items-center gap-1",
        variant === "outline" &&
          "gap-0 rounded-md border border-border bg-background p-0.5 shadow-xs",
        className
      )}
      {...props}
    >
      {children}
    </ToggleGroupPrimitive.Root>
  )
}

function ToggleGroupItem({
  className,
  variant = "default",
  size = "default",
  children,
  ...props
}: React.ComponentProps<typeof ToggleGroupPrimitive.Item> & {
  variant?: "default" | "outline"
  size?: "default" | "sm"
}) {
  return (
    <ToggleGroupPrimitive.Item
      data-slot="toggle-group-item"
      data-variant={variant}
      data-size={size}
      className={cn(
        "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md font-medium transition-colors",
        "focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none",
        "disabled:pointer-events-none disabled:opacity-50",
        "hover:bg-muted hover:text-foreground",
        // 选中态用实底而不是浅色底：分段控件的"当前档位"要一眼可辨，
        // 而且它同时被 aria-pressed 表达，不依赖颜色单独承载。
        "data-[state=on]:bg-primary data-[state=on]:text-primary-foreground data-[state=on]:hover:bg-primary",
        size === "sm" ? "h-7 px-2 text-xs" : "h-8 px-2.5 text-sm",
        variant === "outline" && "rounded-sm data-[state=on]:shadow-xs",
        "[&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4",
        className
      )}
      {...props}
    >
      {children}
    </ToggleGroupPrimitive.Item>
  )
}

export { ToggleGroup, ToggleGroupItem }
