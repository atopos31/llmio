import { describe, expect, it } from "vitest"

import {
  bucketLabelFormatter,
  compactNumber,
  formatBytes,
  currencySymbol,
  formatClock,
  formatCost,
  formatDateTime,
  formatDurationMs,
  formatDurationNs,
  formatFull,
  formatNumber,
  formatPercent,
  formatSeconds,
  formatTps,
  inferBucketMs,
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

describe("inferBucketMs", () => {
  it("取相邻两点之差", () => {
    const base = new Date(2026, 8, 29, 10, 0).getTime()
    expect(inferBucketMs([{ ts: base }, { ts: base + 30 * 60 * 1000 }])).toBe(30 * 60 * 1000)
  })

  it("不足两点时回落到 1 小时", () => {
    expect(inferBucketMs([])).toBe(60 * 60 * 1000)
    expect(inferBucketMs([{ ts: 1 }])).toBe(60 * 60 * 1000)
  })
})

describe("bucketLabelFormatter", () => {
  const at = (y: number, m: number, d: number, h = 0, min = 0) =>
    new Date(y, m, d, h, min).getTime()
  /** 一段逐小时的序列，桶宽与跨度都由点数决定——真实的分桶数据就长这样 */
  const hourly = (start: number, count: number) =>
    Array.from({ length: count }, (_, i) => ({ ts: start + i * 60 * 60 * 1000 }))

  it("日内的小时桶只显示时刻", () => {
    const data = hourly(at(2026, 8, 29, 8), 6)
    expect(bucketLabelFormatter(data)(at(2026, 8, 29, 14, 30))).toBe("14:30")
  })

  it("跨天的小时桶带上日期", () => {
    // 这就是"超过一天的小时分析只有重复的小时段"那个缺陷：
    // 三天的小时桶只显示 HH:MM，轴上会出现三遍同样的时刻，分不清哪段是哪天
    const data = hourly(at(2026, 8, 29, 0), 73)
    const label = bucketLabelFormatter(data)
    expect(label(at(2026, 8, 29, 14, 30))).toBe("09-29 14:30")
    expect(label(at(2026, 9, 1, 2, 0))).toBe("10-01 02:00")
    // 相隔两天、同一时刻的两点不再同形，读者能分辨出它们不是同一个桶
    expect(label(data[0].ts)).not.toBe(label(data[48].ts))
  })

  it("跨天的半小时桶也带上日期", () => {
    // 判据是"序列是否跨天"，与桶宽无关：跨午夜的半小时桶同样会重复时刻
    const data = [
      { ts: at(2026, 8, 29, 23, 30) },
      { ts: at(2026, 8, 30, 0, 0) },
      { ts: at(2026, 8, 30, 0, 30) },
    ]
    expect(bucketLabelFormatter(data)(at(2026, 8, 30, 0, 30))).toBe("09-30 00:30")
  })

  it("跨年的序列也算跨天", () => {
    const data = [{ ts: at(2026, 11, 31, 23) }, { ts: at(2027, 0, 1, 1) }]
    expect(bucketLabelFormatter(data)(at(2027, 0, 1, 1))).toBe("01-01 01:00")
  })

  it("日级桶按天读，不显示时刻", () => {
    const data = [
      { ts: at(2026, 8, 29) },
      { ts: at(2026, 8, 30) },
      { ts: at(2026, 9, 5) },
    ]
    const label = bucketLabelFormatter(data)
    expect(label(at(2026, 8, 29, 13))).toBe("9/29")
  })

  it("更宽的桶也用日期", () => {
    expect(bucketLabelFormatter([], 7 * 24 * 60 * 60 * 1000)(at(2026, 11, 1))).toBe("12/1")
  })

  it("单点序列不判跨天，用时刻", () => {
    const ts = at(2026, 8, 29, 9, 5)
    expect(bucketLabelFormatter([{ ts }])(ts)).toBe("09:05")
  })

  it("空序列不炸", () => {
    // crossesDay 会取 data[0]，长度不足时不能走到它
    expect(bucketLabelFormatter([])(at(2026, 8, 29, 9, 5))).toBe("09:05")
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

describe("formatDurationNs", () => {
  it("小于 1µs 用纳秒", () => {
    expect(formatDurationNs(500)).toBe("500ns")
  })

  it("1µs 到 1ms 用微秒", () => {
    expect(formatDurationNs(1_000)).toBe("1µs")
    expect(formatDurationNs(250_000)).toBe("250µs")
  })

  it("1ms 到 1s 用毫秒", () => {
    expect(formatDurationNs(1_000_000)).toBe("1ms")
    expect(formatDurationNs(1_500_000)).toBe("1.5ms")
  })

  it("1s 起用秒", () => {
    expect(formatDurationNs(1_000_000_000)).toBe("1s")
    expect(formatDurationNs(2_500_000_000)).toBe("2.5s")
  })

  it("与 formatDurationMs 严格区分单位", () => {
    // 同一数值当纳秒读是 1s，当毫秒读是 1000000s——相差 1e6 倍。
    // 这正是不能用"按数值大小猜单位"的原因：猜错就是错一个量级。
    expect(formatDurationNs(1_000_000_000)).toBe("1s")
    expect(formatDurationMs(1_000_000_000)).toBe("1000000s")
    expect(formatDurationNs(1_000_000_000)).not.toBe(formatDurationMs(1_000_000_000))
  })

  it("零与非法值", () => {
    expect(formatDurationNs(0)).toBe("0ms")
    expect(formatDurationNs(Number.NaN)).toBe("0ms")
  })
})

describe("formatBytes", () => {
  it("小于 1KB 用字节", () => {
    expect(formatBytes(0)).toBe("0 B")
    expect(formatBytes(512)).toBe("512 B")
    expect(formatBytes(1023)).toBe("1023 B")
  })

  it("逐级换单位", () => {
    expect(formatBytes(1024)).toBe("1 KB")
    expect(formatBytes(2048)).toBe("2 KB")
    expect(formatBytes(1024 ** 2)).toBe("1 MB")
    expect(formatBytes(1024 ** 3)).toBe("1 GB")
  })

  it("保留一位小数（去掉无意义的 .0）", () => {
    expect(formatBytes(1536)).toBe("1.5 KB")
    expect(formatBytes(1024 ** 2 * 2.5)).toBe("2.5 MB")
  })

  it("非法值回落到 0 B", () => {
    expect(formatBytes(Number.NaN)).toBe("0 B")
  })
})

describe("formatTps", () => {
  it("固定一位小数", () => {
    expect(formatTps(40)).toBe("40.0")
    expect(formatTps(40.26)).toBe("40.3")
    expect(formatTps(0.04)).toBe("0.0")
  })

  it("0 照常显示 0.0（缺值语义只在排序里区分，展示层不搞第二套口径）", () => {
    expect(formatTps(0)).toBe("0.0")
  })

  it("非法值回落到 0.0", () => {
    expect(formatTps(Number.NaN)).toBe("0.0")
    expect(formatTps(Number.POSITIVE_INFINITY)).toBe("0.0")
  })
})
