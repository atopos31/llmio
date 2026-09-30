import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * Meter：一个比例对上限的**单值**呈现。
 *
 * ## 为什么不是仪表盘（gauge）或环形进度
 *
 * 规范把这两者分得很清楚：
 *   - **meter** —— "某个量占其上限的多少"。轨道 + 填充，读的是**长度**。
 *   - **gauge** —— 测量仪器（速度表、温度表）。它的语义是"在一个区间里
 *     现在指到哪"，读者要去找指针，还要理解刻度。
 *
 * 余量是前者。用仪表盘不但要多画一圈刻度与指针（占了卡片一半高度），还会
 * 让读者以为 0..100 是个需要解读的刻度区间，而不是"用掉了多少"。
 * 原 dashboard 这里用的是 ECharts gauge，属于规范硬约束里的错用。
 *
 * 另外环形图（pie/donut）也不行：比较接近的数值时人眼读弧长不可靠。
 *
 * ## 状态色只做**增强**，不做唯一编码
 *
 * fill 支持按严重度取色，但调用方**必须**同时给出文字（余量的百分比与
 * 状态名）。色盲用户拿不到颜色信息，而 warning 与 serious 的对比度低于 3:1
 * 是调色板的既定取舍（靠配对编码补偿）。
 */
function Meter({
  className,
  value,
  max = 100,
  fill,
  trackLabel,
  ...props
}: React.ComponentProps<"div"> & {
  /** 当前值。越界会被钳到 [0, max]。 */
  value: number
  max?: number
  /** 填充的 CSS 颜色。不传则用主色。 */
  fill?: string
  /**
   * 无障碍名称。meter 是"值有含义"的图形，读屏必须能念出来，
   * 因此这个属性是必填的（而不是像 className 那样可选）。
   */
  trackLabel: string
}) {
  const safeMax = max > 0 ? max : 100
  const clamped = Math.min(Math.max(value, 0), safeMax)
  const pct = (clamped / safeMax) * 100

  return (
    <div
      data-slot="meter"
      role="meter"
      aria-valuenow={clamped}
      aria-valuemin={0}
      aria-valuemax={safeMax}
      aria-label={trackLabel}
      className={cn(
        "relative h-2 w-full overflow-hidden rounded-full bg-muted",
        className
      )}
      {...props}
    >
      <div
        data-slot="meter-fill"
        className="h-full rounded-full transition-[width] duration-300 ease-out"
        style={{ width: `${pct}%`, backgroundColor: fill ?? "var(--color-primary)" }}
      />
    </div>
  )
}

export { Meter }
