import { renderHook } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { useEntityColors, useIndexColors } from "@/hooks/use-series-color"
import { SERIES_COUNT } from "@/lib/palette"

describe("useEntityColors", () => {
  it("按字典序给实体分配槽位", () => {
    const { result } = renderHook(() => useEntityColors(["gamma", "alpha", "beta"]))
    const color = result.current

    expect(color("alpha")).toBe("var(--series-1)")
    expect(color("beta")).toBe("var(--series-2)")
    expect(color("gamma")).toBe("var(--series-3)")
  })

  it("与传入顺序无关", () => {
    const a = renderHook(() => useEntityColors(["one", "two", "three"])).result.current
    const b = renderHook(() => useEntityColors(["three", "one", "two"])).result.current

    for (const name of ["one", "two", "three"]) {
      expect(a(name)).toBe(b(name))
    }
  })

  it("只在 universe 变化时才重建查表（引用稳定性）", () => {
    const universe = ["a", "b"]
    const { result, rerender } = renderHook(({ u }) => useEntityColors(u), {
      initialProps: { u: universe },
    })
    const first = result.current

    // 同一个数组引用重渲染：useMemo 不应重建
    rerender({ u: universe })
    expect(result.current).toBe(first)

    // 新数组引用：重建
    rerender({ u: ["a", "b", "c"] })
    expect(result.current).not.toBe(first)
  })

  it("未知实体回落到第一槽", () => {
    const { result } = renderHook(() => useEntityColors(["alpha", "beta"]))
    expect(result.current("nope")).toBe("var(--series-1)")
  })

  it("空 universe 不抛错", () => {
    const { result } = renderHook(() => useEntityColors([]))
    expect(result.current("anything")).toBe("var(--series-1)")
  })

  it("实体数超过槽位时钳到末槽，不生成第 9 色", () => {
    const many = Array.from({ length: 12 }, (_, i) => `m${String(i).padStart(2, "0")}`)
    const { result } = renderHook(() => useEntityColors(many))
    // 取字典序最后一个 → 应钳到第 8 槽
    const last = [...many].sort((a, b) => a.localeCompare(b)).at(-1)!
    expect(result.current(last)).toBe(`var(--series-${SERIES_COUNT})`)
  })

  it("给定稳定全集时，筛选后存活实体保持同色", () => {
    const universe = ["alpha", "beta", "gamma"]
    const { result } = renderHook(() => useEntityColors(universe))
    const gamma = result.current("gamma")

    // 同一个 universe 下重复查询不应变色
    expect(result.current("gamma")).toBe(gamma)
    expect(gamma).toBe("var(--series-3)")
  })
})

describe("useIndexColors", () => {
  it("按索引取槽位", () => {
    const { result } = renderHook(() => useIndexColors())
    expect(result.current(0)).toBe("var(--series-1)")
    expect(result.current(7)).toBe("var(--series-8)")
  })

  it("越界钳位，不生成新色", () => {
    const { result } = renderHook(() => useIndexColors())
    expect(result.current(99)).toBe(`var(--series-${SERIES_COUNT})`)
    expect(result.current(-1)).toBe("var(--series-1)")
  })

  it("函数引用稳定", () => {
    const { result, rerender } = renderHook(() => useIndexColors())
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })
})
