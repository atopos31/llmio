import type { ReactNode } from "react"
import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

// jsdom 里 ResponsiveContainer 量到的尺寸是 0×0，于是不渲染子节点，而这一组
// 断言测的是提示条本身、不是它怎么被布局。换成"直接渲染子节点"即可。
vi.mock("recharts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("recharts")>()
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children: ReactNode }) => <>{children}</>,
  }
})

import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart"
import { bucketLabelFormatter } from "@/lib/format"

/**
 * 提示条顶部那一行（label）的取值。
 *
 * 这一组钉的是一处真机上才暴露的缺陷：趋势图的 label 是 XAxis 上的时间戳
 * （数字），而 `ChartTooltipContent` 原先只在 label 是**字符串**时才把它交给
 * `labelFormatter`，数字会掉到"系列名"分支——于是格式化器收到"成功"，
 * `Number("成功")` 得 NaN，提示条顶部印出 `NaN:NaN`。五个挂载点（总览页与
 * 分析页的三张趋势图）一起坏，而坐标轴刻度是好的（tickFormatter 由 Recharts
 * 直接调用，不经过这段取值逻辑），所以从截图上只像是提示条的问题。
 *
 * 断言直接钉"格式化器收到了什么"，而不是钉渲染出来的字符串——后者要靠
 * class 名定位那一行，反而更脆。
 */

/** 一个桶起点：2026-09-30 14:05（本地时区），趋势图里就是这个毫秒数 */
const TS = new Date(2026, 8, 30, 14, 5).getTime()

function renderTooltip(opts: { label: unknown; hideLabel?: boolean }) {
  const seen: unknown[] = []
  // 用真实的格式化器，让这条测试覆盖"时间戳 → HH:MM"的完整链路
  const format = bucketLabelFormatter([{ ts: TS }])
  const utils = render(
    <ChartContainer config={{ success: { label: "成功", color: "var(--color-1)" } }}>
      <ChartTooltipContent
        active
        hideLabel={opts.hideLabel}
        label={opts.label}
        labelFormatter={(v) => {
          seen.push(v)
          return format(Number(v))
        }}
        payload={
          [
            {
              dataKey: "success",
              name: "success",
              value: 3,
              payload: { ts: TS, success: 3 },
            },
          ] as never
        }
      />
    </ChartContainer>
  )
  return { ...utils, seen }
}

describe("图表提示条 · 顶部那一行取什么值", () => {
  it("数字 label（趋势图的时间戳）原样交给 labelFormatter，顶部是时刻", () => {
    const { seen } = renderTooltip({ label: TS })

    // 根因就在这一条：传下去的必须是 label 本身，不是系列名
    expect(seen).toEqual([TS])
    expect(screen.getByText("14:05")).toBeInTheDocument()
  })

  it("不再出现 NaN:NaN", () => {
    renderTooltip({ label: TS })

    // 症状是 `new Date(NaN)` 拼出来的：getHours/getMinutes 都是 NaN
    expect(screen.queryByText(/NaN/)).not.toBeInTheDocument()
  })

  it("字符串 label 仍按 config 里的名字翻译（分类型的图用这条路径）", () => {
    const { seen } = renderTooltip({ label: "success" })

    expect(seen).toEqual(["成功"])
  })

  it("hideLabel 时整行不渲染，也不调用格式化器", () => {
    const { seen } = renderTooltip({ label: TS, hideLabel: true })

    expect(seen).toEqual([])
    expect(screen.queryByText("14:05")).not.toBeInTheDocument()
  })
})
