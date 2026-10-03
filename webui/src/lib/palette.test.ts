import { describe, expect, it } from "vitest"

import {
  SERIES_COUNT,
  SERIES_COUNT_ALL_PAIRS,
  STATUS,
  entitySeriesVar,
  seqVar,
  seriesVar,
  statusForRequest,
} from "@/lib/palette"

describe("seriesVar", () => {
  it("按 1 起始的槽位返回对应变量", () => {
    expect(seriesVar(0)).toBe("var(--series-1)")
    expect(seriesVar(1)).toBe("var(--series-2)")
    expect(seriesVar(7)).toBe("var(--series-8)")
  })

  it("小数向下取整", () => {
    expect(seriesVar(2.7)).toBe("var(--series-3)")
  })

  it("越界钳到最后一槽，而不是回绕", () => {
    // 回绕会让第 9 个系列与第 1 个同色——两个不同实体看着一样，
    // 是比"颜色重复"更糟的误导。钳位还会在视觉上立刻暴露"系列太多"。
    expect(seriesVar(8)).toBe("var(--series-8)")
    expect(seriesVar(99)).toBe("var(--series-8)")
  })

  it("负数与非法值回落到第一槽", () => {
    expect(seriesVar(-1)).toBe("var(--series-1)")
    expect(seriesVar(Number.NaN)).toBe("var(--series-1)")
  })

  it("无穷大按非法值处理，回落到第一槽而非末槽", () => {
    // Number.isFinite(Infinity) 为 false，走的是"非法输入"分支。
    // 这不是钳位——钳位只针对有限的越界值（8、99 等）。
    expect(seriesVar(Number.POSITIVE_INFINITY)).toBe("var(--series-1)")
    expect(seriesVar(Number.NEGATIVE_INFINITY)).toBe("var(--series-1)")
  })

  it("槽位数与常量一致", () => {
    expect(seriesVar(SERIES_COUNT - 1)).toBe(`var(--series-${SERIES_COUNT})`)
  })

  it("全配对图形的系列上限小于总槽位数", () => {
    // 散点等形式的每对颜色都可能相邻，验证器实测只有前 3 槽达标
    expect(SERIES_COUNT_ALL_PAIRS).toBeLessThan(SERIES_COUNT)
  })
})

describe("seqVar", () => {
  it("按 1 起始返回顺序色阶", () => {
    expect(seqVar(1)).toBe("var(--seq-1)")
    expect(seqVar(5)).toBe("var(--seq-5)")
  })

  it("越界钳位", () => {
    expect(seqVar(0)).toBe("var(--seq-1)")
    expect(seqVar(-3)).toBe("var(--seq-1)")
    expect(seqVar(99)).toBe("var(--seq-5)")
  })

  it("小数向下取整", () => {
    expect(seqVar(2.9)).toBe("var(--seq-2)")
  })

  it("非法值回落到第一级", () => {
    expect(seqVar(Number.NaN)).toBe("var(--seq-1)")
  })
})

describe("entitySeriesVar", () => {
  it("按实体的稳定字典序分配，与传入顺序无关", () => {
    const a = entitySeriesVar("beta", ["beta", "alpha", "gamma"])
    const b = entitySeriesVar("beta", ["gamma", "beta", "alpha"])
    expect(a).toBe(b)
  })

  it("排序后首个实体取第一槽", () => {
    expect(entitySeriesVar("alpha", ["beta", "alpha"])).toBe("var(--series-1)")
  })

  it("筛选后存活实体保持原色——前提是传入稳定的全集", () => {
    // 关键用法：两次都传**全集**（配置里的全部模型），而不是当前筛选后的子集。
    // 若第二次传 ["beta","gamma"]，gamma 会从第 3 槽滑到第 2 槽，颜色就变了。
    const universe = ["alpha", "beta", "gamma"]
    const before = entitySeriesVar("gamma", universe)
    const after = entitySeriesVar("gamma", universe)
    expect(after).toBe(before)
    expect(before).toBe("var(--series-3)")
  })

  it("传入子集会导致改色——这是调用方必须避免的误用", () => {
    // 固化这个失败模式：说明为什么 universe 参数必须是全集。
    // 本用例不是在肯定这个行为，而是把"误用会怎样"写成可执行的说明。
    const universe = ["alpha", "beta", "gamma"]
    const withUniverse = entitySeriesVar("gamma", universe)
    const withSubset = entitySeriesVar("gamma", ["beta", "gamma"])
    expect(withSubset).not.toBe(withUniverse)
  })

  it("未知实体回落到第一槽", () => {
    expect(entitySeriesVar("nope", ["alpha", "beta"])).toBe("var(--series-1)")
  })

  it("空列表不抛错", () => {
    expect(entitySeriesVar("alpha", [])).toBe("var(--series-1)")
  })

  it("实体数超过槽位时钳到末槽，不生成新色", () => {
    // 按字典序构造 12 个实体，取排序后位于第 9 位之后的那个
    const many = Array.from({ length: 12 }, (_, i) => `e${i}`)
    const sorted = [...many].sort((a, b) => a.localeCompare(b))
    const last = sorted[sorted.length - 1]
    expect(entitySeriesVar(last, many)).toBe(`var(--series-${SERIES_COUNT})`)
  })

  it("排序为字典序：e10 排在 e2 之前", () => {
    // 固化 localeCompare 的行为，避免有人误以为这里是自然数排序
    const many = ["e1", "e2", "e10"]
    expect(entitySeriesVar("e10", many)).toBe("var(--series-2)")
    expect(entitySeriesVar("e1", many)).toBe("var(--series-1)")
  })
})

describe("statusForRequest", () => {
  it("成功映射为 good", () => {
    expect(statusForRequest("success")).toBe("good")
  })

  it("失败映射为 critical", () => {
    expect(statusForRequest("error")).toBe("critical")
  })

  it("在途映射为 warning 而非 good", () => {
    // running 是"还没结束"，不是"成功"。归为 good 会让成功率看起来虚高。
    expect(statusForRequest("running")).toBe("warning")
  })

  it("未知状态回落到中性", () => {
    expect(statusForRequest("weird")).toBe("warning")
    expect(statusForRequest("")).toBe("warning")
  })
})

describe("STATUS", () => {
  it("每个状态都有填充色与文字色两套", () => {
    for (const key of ["good", "warning", "serious", "critical"] as const) {
      expect(STATUS[key].fill).toMatch(/^var\(--status-/)
      expect(STATUS[key].ink).toMatch(/^var\(--status-.*-ink\)$/)
      expect(STATUS[key].fill).not.toBe(STATUS[key].ink)
    }
  })

  it("四态齐备且互不相同", () => {
    const fills = Object.values(STATUS).map((s) => s.fill)
    expect(new Set(fills).size).toBe(4)
  })
})
