import { describe, expect, it } from "vitest"

import {
  compactNumber,
  currencySymbol,
  formatBucketLabel,
  formatClock,
  formatCost,
  formatDateTime,
  formatDurationMs,
  formatFull,
  formatNumber,
  formatPercent,
  formatSeconds,
} from "@/lib/format"

describe("compactNumber", () => {
  it("万以下按千分位原样显示", () => {
    expect(compactNumber(0)).toBe("0")
    expect(compactNumber(999)).toBe("999")
    expect(compactNumber(9999)).toBe("9,999")
  })

  it("1e4 起用 K", () => {
    expect(compactNumber(10000)).toBe("10K")
    expect(compactNumber(12900)).toBe("12.9K")
  })

  it("1e6 起用 M", () => {
    expect(compactNumber(1_000_000)).toBe("1M")
    expect(compactNumber(12_900_000)).toBe("12.9M")
  })

  it("1e9 起用 B", () => {
    expect(compactNumber(1_500_000_000)).toBe("1.5B")
  })

  it("负数保留符号", () => {
    expect(compactNumber(-12_900)).toBe("-12.9K")
    expect(compactNumber(-999)).toBe("-999")
  })

  it("整数不补 .0", () => {
    expect(compactNumber(20_000)).toBe("20K")
  })

  it("非法值回落到 0", () => {
    expect(compactNumber(Number.NaN)).toBe("0")
    expect(compactNumber(Number.POSITIVE_INFINITY)).toBe("0")
  })
})

describe("formatNumber", () => {
  it("千分位并四舍五入", () => {
    expect(formatNumber(1234)).toBe("1,234")
    expect(formatNumber(1234.6)).toBe("1,235")
    expect(formatNumber(0)).toBe("0")
  })

  it("非法值回落到 0", () => {
    expect(formatNumber(Number.NaN)).toBe("0")
  })
})

describe("formatDurationMs", () => {
  it("小于 1ms 用微秒", () => {
    expect(formatDurationMs(0.5)).toBe("500µs")
  })

  it("1ms 到 1s 用毫秒", () => {
    expect(formatDurationMs(1)).toBe("1ms")
    expect(formatDurationMs(250)).toBe("250ms")
    expect(formatDurationMs(999.4)).toBe("999.4ms")
  })

  it("1s 起用秒", () => {
    expect(formatDurationMs(1000)).toBe("1s")
    expect(formatDurationMs(1500)).toBe("1.5s")
  })

  it("零与非法值", () => {
    expect(formatDurationMs(0)).toBe("0ms")
    expect(formatDurationMs(Number.NaN)).toBe("0ms")
  })

  it("负数走同一套单位", () => {
    expect(formatDurationMs(-1500)).toBe("-1.5s")
  })
})

describe("formatPercent", () => {
  it("默认一位小数", () => {
    expect(formatPercent(50)).toBe("50.0%")
    expect(formatPercent(99.56)).toBe("99.6%")
  })

  it("进位遵循 IEEE754 的实际值，不是四舍五入直觉", () => {
    // 99.55 的二进制表示略小于 99.55，因此 toFixed(1) 得到 99.5。
    // 这是 JS 数字的既有行为，不是本函数的取舍；固化它以免有人
    // 误以为是 bug 而加一层"修正"，那反而会让别的值出错。
    expect(formatPercent(99.55)).toBe("99.5%")
  })

  it("可指定小数位", () => {
    expect(formatPercent(33.3333, 2)).toBe("33.33%")
    expect(formatPercent(33.3333, 0)).toBe("33%")
  })

  it("零", () => {
    expect(formatPercent(0)).toBe("0.0%")
  })

  it("非法值回落到 0%", () => {
    expect(formatPercent(Number.NaN)).toBe("0%")
  })
})

describe("formatCost", () => {
  it("CNY 用 ¥", () => {
    expect(formatCost(3.7, "CNY")).toBe("¥3.7000")
  })

  it("USD 用 $", () => {
    expect(formatCost(0.123456, "USD")).toBe("$0.1235")
  })

  it("未知币种带出原文", () => {
    expect(formatCost(1.5, "EUR")).toBe("1.5000 EUR")
  })

  it("空币种只显示数字", () => {
    expect(formatCost(1.5, "")).toBe("1.5000")
  })

  it("保四位小数——成本常很小，两位会全显示成 0.00", () => {
    expect(formatCost(0.00001, "CNY")).toBe("¥0.0000")
    expect(formatCost(0.0012, "CNY")).toBe("¥0.0012")
  })

  it("非法值显示占位符", () => {
    expect(formatCost(Number.NaN, "CNY")).toBe("—")
  })
})

describe("currencySymbol", () => {
  it("已知币种", () => {
    expect(currencySymbol("CNY")).toBe("¥")
    expect(currencySymbol("USD")).toBe("$")
  })

  it("未知币种返回空串（轴标签不该出现问号）", () => {
    expect(currencySymbol("EUR")).toBe("")
    expect(currencySymbol("")).toBe("")
  })
})

describe("时间格式化", () => {
  it("formatClock 输出 HH:MM", () => {
    const ts = new Date(2026, 8, 29, 9, 5).getTime()
    expect(formatClock(ts)).toBe("09:05")
  })

  it("formatDateTime 输出 MM-DD HH:MM", () => {
    const ts = new Date(2026, 8, 29, 9, 5).getTime()
    expect(formatDateTime(ts)).toBe("09-29 09:05")
  })

  it("formatFull 输出完整时间", () => {
    const ts = new Date(2026, 8, 29, 9, 5, 3).getTime()
    expect(formatFull(ts)).toBe("2026-09-29 09:05:03")
  })
})

describe("formatBucketLabel", () => {
  it("日内桶用时刻", () => {
    const ts = new Date(2026, 8, 29, 14, 30).getTime()
    expect(formatBucketLabel(ts, 60 * 60 * 1000)).toBe("14:30")
  })

  it("日级及以上用日期", () => {
    // 1 天的桶显示 "24h" 没有意义，读者关心的是"哪一天"
    const ts = new Date(2026, 8, 29, 0, 0).getTime()
    expect(formatBucketLabel(ts, 24 * 60 * 60 * 1000)).toBe("9/29")
  })

  it("更宽的桶也用日期", () => {
    const ts = new Date(2026, 11, 1, 0, 0).getTime()
    expect(formatBucketLabel(ts, 7 * 24 * 60 * 60 * 1000)).toBe("12/1")
  })
})

describe("formatSeconds", () => {
  it("小于一分钟用秒", () => {
    expect(formatSeconds(0.5)).toBe("0.5s")
    expect(formatSeconds(45)).toBe("45s")
  })

  it("一分钟起用 分+秒", () => {
    expect(formatSeconds(60)).toBe("1m0s")
    expect(formatSeconds(80)).toBe("1m20s")
    expect(formatSeconds(125)).toBe("2m5s")
  })

  it("零与非法值", () => {
    expect(formatSeconds(0)).toBe("0s")
    expect(formatSeconds(Number.NaN)).toBe("0s")
  })
})
