import { describe, expect, it } from "vitest"

import {
  PREVIEW_DAY_OPTIONS,
  WEEKDAY_DISPLAY_ORDER,
  clockContains,
  clocksOverlap,
  compareClock,
  findPeriodConflicts,
  formatClock,
  formatMultiplier,
  formatSchedulePoint,
  formToPayload,
  holidayRowsFromOverrides,
  isCalendarDate,
  isOvernight,
  isValidTimezone,
  normalizeClock,
  overridesFromHolidayRows,
  parseClock,
  parseMultiplier,
  periodStartMinutes,
  periodsConflict,
  pricingToForm,
  validatePeakForm,
  weekdayKey,
  windowLength,
  type PeakForm,
  type PeakPeriod,
  type PeakPricing,
} from "@/lib/peak"

/**
 * 峰谷计费纯逻辑的 A 层测试。
 *
 * 每一条断言都对应后端的一处语义（handler/peak.go 的 validatePeakPricing、
 * service/peak.go 的 ParseClock / InClockWindow / PreviewSchedule）。
 * 为什么值得逐条钉：这些函数判错的后果不是"页面少一行"，而是**价格变了**——
 * 跨零点算反会让夜间优惠落到白天，start==end 放过去会让一段覆盖全天，
 * 校验比后端松会让用户在保存时才被拒、比后端紧会让合法配置存不进去。
 */

const 合法: PeakForm = {
  enabled: true,
  timezone: "Asia/Shanghai",
  weekdays: [1, 2, 3, 4, 5],
  periods: [
    { name: "标准时段", start: "08:30", end: "00:30", multiplier: "1", days: [], workday: "any" },
    { name: "夜间优惠", start: "00:30", end: "08:30", multiplier: "0.25", days: [], workday: "any" },
  ],
  holidays: [{ date: "2026-10-01", kind: "rest" }],
}

function form(over: Partial<PeakForm> = {}): PeakForm {
  return { ...合法, ...over }
}

describe("时刻解析（对齐 service.ParseClock）", () => {
  it("解析 HH:MM，并允许 24:00", () => {
    expect(parseClock("08:30")).toBe(510)
    expect(parseClock("00:00")).toBe(0)
    expect(parseClock("24:00")).toBe(1440)
    // 后端用 strconv.Atoi，认一位数；这里对齐
    expect(parseClock("8:5")).toBe(485)
    expect(parseClock(" 08:30 ")).toBe(510)
  })

  it("拒绝后端也会拒的写法", () => {
    expect(parseClock("24:30")).toBeNull() // 24 点只能配 00 分
    expect(parseClock("25:00")).toBeNull()
    expect(parseClock("12:60")).toBeNull()
    expect(parseClock("abc:00")).toBeNull()
    // Number("1e1") 是 10，Atoi("1e1") 是错误——不能图省事用 Number()
    expect(parseClock("1e1:00")).toBeNull()
    expect(parseClock("08:30:00")).toBeNull()
    expect(parseClock("08")).toBeNull()
    expect(parseClock("")).toBeNull()
  })

  it("字段缺失（undefined）时当作空串处理，不抛异常", () => {
    expect(parseClock(undefined as unknown as string)).toBeNull()
  })

  it("formatClock 补零，1440 输出 24:00", () => {
    expect(formatClock(0)).toBe("00:00")
    expect(formatClock(485)).toBe("08:05")
    expect(formatClock(1440)).toBe("24:00")
  })

  it("normalizeClock 把合法写法归一，非法写法给 null", () => {
    expect(normalizeClock("8:5")).toBe("08:05")
    // 24:00 不能被"归一"成 00:00，那会从"全天"变成"零点"
    expect(normalizeClock("24:00")).toBe("24:00")
    expect(normalizeClock("25:00")).toBeNull()
  })

  it("compareClock 比先后，非法值给 null", () => {
    expect(compareClock("08:00", "09:00")).toBeLessThan(0)
    expect(compareClock("09:00", "08:00")).toBeGreaterThan(0)
    expect(compareClock("08:00", "08:00")).toBe(0)
    expect(compareClock("08:00", "坏")).toBeNull()
    expect(compareClock("坏", "08:00")).toBeNull()
  })
})

describe("窗口判定（对齐 service.InClockWindow / windowLength）", () => {
  it("isOvernight 只在开始晚于结束时为真", () => {
    expect(isOvernight("22:00", "06:00")).toBe(true)
    expect(isOvernight("06:00", "22:00")).toBe(false)
    // 起止相同不是跨零点（那是覆盖全天，由 validatePeakForm 单独拒掉）
    expect(isOvernight("08:00", "08:00")).toBe(false)
    expect(isOvernight("08:00", "坏")).toBe(false)
  })

  it("windowLength：常规、跨零点、start==end 三种算法", () => {
    expect(windowLength("08:30", "12:00")).toBe(210)
    expect(windowLength("22:00", "06:00")).toBe(480)
    // start == end 后端 validator 会拒，但语义上它是"覆盖全天"，这里不给另一个答案
    expect(windowLength("08:00", "08:00")).toBe(1440)
    expect(windowLength("08:00", "坏")).toBeNull()
  })

  it("clockContains 是半开区间 [start, end)", () => {
    expect(clockContains(480, "08:00", "20:00")).toBe(true)
    expect(clockContains(1199, "08:00", "20:00")).toBe(true)
    // 20:00 属于下一个窗口，不属于这一段
    expect(clockContains(1200, "08:00", "20:00")).toBe(false)
    expect(clockContains(479, "08:00", "20:00")).toBe(false)
  })

  it("clockContains 跨零点：两头都算在内", () => {
    expect(clockContains(1320, "22:00", "06:00")).toBe(true) // 22:00
    expect(clockContains(300, "22:00", "06:00")).toBe(true) // 05:00
    expect(clockContains(360, "22:00", "06:00")).toBe(false) // 06:00 是这一段的终点
    expect(clockContains(800, "22:00", "06:00")).toBe(false)
  })

  it("clockContains 对 start==end 视为覆盖全天，对非法值给 false", () => {
    expect(clockContains(0, "08:00", "08:00")).toBe(true)
    expect(clockContains(800, "坏", "08:00")).toBe(false)
  })

  it("clocksOverlap 相接不算重叠，包含算重叠", () => {
    expect(clocksOverlap("08:00", "20:00", "20:00", "22:00")).toBe(false)
    expect(clocksOverlap("08:00", "20:00", "19:00", "22:00")).toBe(true)
    expect(clocksOverlap("08:00", "20:00", "12:00", "13:00")).toBe(true) // 前者包住后者
    // 跨零点窗口包住白天的一段
    expect(clocksOverlap("22:00", "06:00", "05:00", "23:00")).toBe(true)
    expect(clocksOverlap("22:00", "06:00", "07:00", "21:00")).toBe(false)
    expect(clocksOverlap("坏", "06:00", "05:00", "23:00")).toBe(false)
  })
})

describe("时段冲突是提示、不是错误", () => {
  const a: PeakPeriod = { name: "A", start: "08:00", end: "10:00", multiplier: 1 }

  it("星期不相交就不冲突", () => {
    expect(periodsConflict(a, { ...a, name: "B", days: [1] })).toBe(true)
    expect(periodsConflict({ ...a, days: [1] }, { ...a, name: "B", days: [2] })).toBe(false)
    // 一方不限就等于与任何一天都有交集
    expect(periodsConflict({ ...a, days: [] }, { ...a, name: "B", days: [2] })).toBe(true)
    // 空数组与缺省是同一件事
    expect(periodsConflict({ ...a, days: undefined }, { ...a, name: "B", days: [2] })).toBe(true)
  })

  it("工作日条件互斥就不冲突", () => {
    expect(periodsConflict({ ...a, workday: true }, { ...a, name: "B", workday: false })).toBe(false)
    expect(periodsConflict({ ...a, workday: true }, { ...a, name: "B", workday: true })).toBe(true)
    // 一方不限
    expect(periodsConflict({ ...a, workday: undefined }, { ...a, name: "B", workday: false })).toBe(true)
    expect(periodsConflict({ ...a, workday: true }, { ...a, name: "B", workday: undefined })).toBe(true)
  })

  it("findPeriodConflicts 只给重叠的对，并按窗口起点排序", () => {
    const periods: PeakPeriod[] = [
      { name: "晚高峰", start: "21:00", end: "23:30", multiplier: 2 },
      { name: "标准", start: "08:00", end: "10:00", multiplier: 1 },
      { name: "夜间", start: "22:00", end: "23:00", multiplier: 0.5 },
      // 后两段的数组顺序是**故意反着**放的：两者起点都是 08:00，
      // 只有比较第二个元素的起点才能把它们排对。按数组顺序也算不出这个结果
      { name: "早间", start: "08:30", end: "09:30", multiplier: 0.8 },
      { name: "白天", start: "08:00", end: "12:00", multiplier: 1 },
    ]
    // 重叠的是 (1,3)、(1,4)、(3,4) 与 (0,2)
    expect(findPeriodConflicts(periods)).toEqual([
      [1, 4],
      [1, 3],
      [3, 4],
      [0, 2],
    ])
  })

  it("没有重叠时返回空数组", () => {
    expect(
      findPeriodConflicts([
        { name: "A", start: "00:00", end: "08:00", multiplier: 1 },
        { name: "B", start: "08:00", end: "16:00", multiplier: 1 },
        { name: "C", start: "16:00", end: "00:00", multiplier: 1 },
      ])
    ).toEqual([])
  })

  it("解析不出来的时段排到最后（它由校验单独报错）", () => {
    expect(periodStartMinutes({ name: "坏", start: "坏", end: "10:00", multiplier: 1 })).toBe(
      Number.MAX_SAFE_INTEGER
    )
    expect(periodStartMinutes({ name: "好", start: "08:00", end: "10:00", multiplier: 1 })).toBe(480)
  })
})

describe("本地校验（逐条对齐 handler/peak.go 的 validatePeakPricing）", () => {
  it("一份正常配置没有问题", () => {
    expect(validatePeakForm(合法)).toEqual([])
  })

  it("开始时间不合法时点名第几段、哪一段、原值是什么", () => {
    const issues = validatePeakForm(
      form({
        periods: [{ name: "夜间优惠", start: "25:00", end: "08:30", multiplier: "0.25", days: [], workday: "any" }],
      })
    )
    expect(issues).toEqual([
      { key: "err_start_invalid", index: 1, name: "夜间优惠", value: "25:00" },
    ])
  })

  it("结束时间不合法单列一条，不与开始时间混为一谈", () => {
    const issues = validatePeakForm(
      form({
        periods: [{ name: "夜间优惠", start: "00:30", end: "8点半", multiplier: "0.25", days: [], workday: "any" }],
      })
    )
    expect(issues).toEqual([{ key: "err_end_invalid", index: 1, name: "夜间优惠", value: "8点半" }])
  })

  it("开始与结束相同要报出来（后端会拒：那会覆盖全天）", () => {
    const issues = validatePeakForm(
      form({ periods: [{ name: "全天", start: "08:00", end: "08:00", multiplier: "1", days: [], workday: "any" }] })
    )
    expect(issues).toEqual([{ key: "err_same_clock", index: 1, name: "全天" }])
  })

  it("生效星期越界、非整数都算越界", () => {
    expect(
      validatePeakForm(
        form({ periods: [{ name: "越界", start: "08:00", end: "09:00", multiplier: "1", days: [7], workday: "any" }] })
      )
    ).toEqual([{ key: "err_days_range", index: 1, name: "越界" }])
    expect(
      validatePeakForm(
        form({ periods: [{ name: "半格", start: "08:00", end: "09:00", multiplier: "1", days: [1.5], workday: "any" }] })
      )
    ).toEqual([{ key: "err_days_range", index: 1, name: "半格" }])
  })

  it("工作日定义越界", () => {
    expect(validatePeakForm(form({ weekdays: [0, 7] }))).toEqual([{ key: "err_weekday_range" }])
  })

  it("时区认不出来要报；留空是合法的（跟随服务器）", () => {
    expect(validatePeakForm(form({ timezone: "Not/AZone" }))).toEqual([
      { key: "err_timezone", value: "Not/AZone" },
    ])
    expect(validatePeakForm(form({ timezone: "" }))).toEqual([])
    expect(validatePeakForm(form({ timezone: "UTC" }))).toEqual([])
  })

  it("日期覆盖：格式不对、日子不存在都算不对", () => {
    expect(validatePeakForm(form({ holidays: [{ date: "2026/10/01", kind: "rest" }] }))).toEqual([
      { key: "err_override_date", value: "2026/10/01" },
    ])
    // 2 月 30 日格式完全正确，只有真去算一次才知道它不存在——比正则多这一步
    expect(validatePeakForm(form({ holidays: [{ date: "2026-02-30", kind: "rest" }] }))).toEqual([
      { key: "err_override_date", value: "2026-02-30" },
    ])
  })

  it("日期覆盖：类型只能是 work / rest", () => {
    expect(validatePeakForm(form({ holidays: [{ date: "2026-10-01", kind: "holiday" }] }))).toEqual([
      { key: "err_override_value", value: "2026-10-01=holiday" },
    ])
  })

  it("乘数空着、写了字、写了负数都拦下（后端没有这条，理由见 parseMultiplier）", () => {
    const 段 = (multiplier: string) => [
      { name: "夜间", start: "00:30", end: "08:30", multiplier, days: [], workday: "any" as const },
    ]
    expect(validatePeakForm(form({ periods: 段("") }))).toEqual([
      { key: "err_multiplier", index: 1, name: "夜间", value: "" },
    ])
    expect(validatePeakForm(form({ periods: 段("半价") }))).toEqual([
      { key: "err_multiplier", index: 1, name: "夜间", value: "半价" },
    ])
    expect(validatePeakForm(form({ periods: 段("-1") }))).toEqual([
      { key: "err_multiplier", index: 1, name: "夜间", value: "-1" },
    ])
    // 免费（0）是合法配置，不是错误
    expect(validatePeakForm(form({ periods: 段("0") }))).toEqual([])
  })

  it("一次报出全部问题，而不是遇到第一个就停", () => {
    const issues = validatePeakForm(
      form({
        timezone: "Not/AZone",
        weekdays: [9],
        periods: [
          { name: "甲", start: "坏", end: "09:00", multiplier: "1", days: [], workday: "any" },
          { name: "乙", start: "10:00", end: "10:00", multiplier: "1", days: [], workday: "any" },
        ],
      })
    )
    expect(issues.map((i) => i.key)).toEqual([
      "err_start_invalid",
      "err_same_clock",
      "err_weekday_range",
      "err_timezone",
    ])
  })

  it("重叠不算问题：这是后端允许的配置，前端不额外加规则", () => {
    // 两段完全重合，只有顺序能决定谁生效——保存时必须放行
    expect(
      validatePeakForm(
        form({
          periods: [
            { name: "特例", start: "08:00", end: "10:00", multiplier: "0.5", days: [], workday: "any" },
            { name: "一般", start: "08:00", end: "10:00", multiplier: "1", days: [], workday: "any" },
          ],
        })
      )
    ).toEqual([])
  })

  it("启用但没有时段也不拦（等价于全部按基础价）", () => {
    expect(validatePeakForm(form({ enabled: true, periods: [] }))).toEqual([])
  })
})

describe("表单 ↔ 载荷", () => {
  const 配置: PeakPricing = {
    enabled: true,
    timezone: "Asia/Shanghai",
    weekdays: [1, 2, 3, 4, 5],
    periods: [
      { name: "标准时段", start: "08:30", end: "00:30", multiplier: 1 },
      { name: "夜间优惠", start: "00:30", end: "08:30", multiplier: 0.25, days: [1, 2], workday: false },
    ],
    dateOverrides: { "2026-10-01": "rest" },
    holidaySyncedAt: 1759271400,
    holidaySource: "remote:https://example.com/2026.json",
  }

  it("回填成表单：时刻归一、乘数变字符串、workday 三态", () => {
    const f = pricingToForm(配置)
    expect(f.periods[0].start).toBe("08:30")
    expect(f.periods[0].multiplier).toBe("1")
    expect(f.periods[0].workday).toBe("any")
    expect(f.periods[1].workday).toBe("rest")
    expect(f.periods[1].days).toEqual([1, 2])
    expect(f.holidays).toEqual([{ date: "2026-10-01", kind: "rest" }])
    expect(f.holidaySyncedAt).toBe(1759271400)
    expect(f.holidaySource).toBe("remote:https://example.com/2026.json")
  })

  it("回填把手写的 8:30 归一成 08:30（两种写法混着摆会看不出是不是同一段）", () => {
    const f = pricingToForm({
      ...配置,
      periods: [{ name: "夜间", start: "8:30", end: "0:00", multiplier: 0.25 }],
    })
    expect(f.periods[0].start).toBe("08:30")
    expect(f.periods[0].end).toBe("00:00")
  })

  it("回填是深拷贝：在表单里改一段不会动到父组件持有的配置", () => {
    const f = pricingToForm(配置)
    f.periods[1].days.push(6)
    f.periods[0].name = "改了"
    expect(配置.periods[1].days).toEqual([1, 2])
    expect(配置.periods[0].name).toBe("标准时段")
  })

  it("回填容忍缺字段（后端早期数据或手工编辑过的配置）", () => {
    const 空壳 = { enabled: false } as unknown as PeakPricing
    const f = pricingToForm(空壳)
    expect(f.timezone).toBe("")
    expect(f.weekdays).toEqual([])
    expect(f.periods).toEqual([])
    expect(f.holidays).toEqual([])
    expect(f.holidaySyncedAt).toBeUndefined()
    expect(f.holidaySource).toBeUndefined()
  })

  it("回填容忍单条时段的缺字段与写坏的时刻", () => {
    const 残缺 = {
      enabled: false,
      timezone: "",
      periods: [
        // 缺字段：全部走兜底
        { name: undefined, start: undefined, end: "12:00", multiplier: undefined, days: undefined, workday: true },
        // 时刻写坏：归一不出来就原样留着，交给校验去点名
        { name: "坏的", start: " 25:00 ", end: "26:00", multiplier: 2, days: [3], workday: false },
        // 结束时间整个缺失
        { name: "缺尾", start: "08:00", end: undefined, multiplier: 1, days: [], workday: undefined },
      ],
    } as unknown as PeakPricing

    const f = pricingToForm(残缺)
    expect(f.periods[0]).toEqual({
      name: "",
      start: "",
      end: "12:00",
      multiplier: "1",
      days: [],
      workday: "work",
    })
    expect(f.periods[1]).toEqual({
      name: "坏的",
      start: " 25:00 ", // 原样（含空白）：归一不了就不能假装它是对的
      end: "26:00",
      multiplier: "2",
      days: [3],
      workday: "rest",
    })
    expect(f.periods[2]).toEqual({
      name: "缺尾",
      start: "08:00",
      end: "", // 缺字段 → 空串，由校验去报
      multiplier: "1",
      days: [],
      workday: "any",
    })
  })

  it("提交载荷：空数组省略、24:00 保留、覆盖表永远带对象", () => {
    const payload = formToPayload({
      ...合法,
      weekdays: [],
      holidays: [],
      periods: [
        { name: " 全天 ", start: "0:00", end: "24:00", multiplier: "1", days: [], workday: "any" },
      ],
    })
    expect(payload.periods).toEqual([{ name: "全天", start: "00:00", end: "24:00", multiplier: 1 }])
    expect(payload.weekdays).toBeUndefined()
    // 不带 omitempty 的字段：空也要发 {}，否则配置文件里看不到这个字段
    expect(payload.dateOverrides).toEqual({})
    expect(payload.holidaySyncedAt).toBeUndefined()
    expect(payload.holidaySource).toBeUndefined()
  })

  it("提交载荷：超过一天的时段、星期与工作日条件按后端形状发", () => {
    const payload = formToPayload({
      ...合法,
      weekdays: [3, 1],
      holidays: [{ date: "2026-10-01", kind: "rest" }],
      holidaySyncedAt: 1759271400,
      holidaySource: "bundled:holidays/2026.json",
      periods: [
        { name: "夜间", start: "22:00", end: "06:00", multiplier: "0.25", days: [5, 0], workday: "work" },
      ],
    })
    expect(payload.periods[0]).toEqual({
      name: "夜间",
      start: "22:00",
      end: "06:00",
      multiplier: 0.25,
      days: [0, 5], // 升序，避免同一组值出现两种写法
      workday: true,
    })
    expect(payload.weekdays).toEqual([1, 3])
    expect(payload.dateOverrides).toEqual({ "2026-10-01": "rest" })
    expect(payload.holidaySyncedAt).toBe(1759271400)
    expect(payload.holidaySource).toBe("bundled:holidays/2026.json")
  })

  it("提交载荷：时刻写坏了照原样发、乘数坏了回落 1（校验会先拦，这是兜底）", () => {
    const payload = formToPayload({
      ...合法,
      periods: [{ name: "坏的", start: " 25:00 ", end: "坏", multiplier: "免", days: [], workday: "rest" }],
    })
    // 落到这里的配置后端会拒并给出原文；关键是不要静默把它变成"免费"
    expect(payload.periods[0]).toEqual({
      name: "坏的",
      start: "25:00",
      end: "坏",
      multiplier: 1,
      workday: false,
    })
  })
})

describe("节假日覆盖", () => {
  it("覆盖表转行并按日期升序", () => {
    expect(
      holidayRowsFromOverrides({ "2026-10-08": "work", "2026-01-01": "rest", "2026-05-01": "rest" })
    ).toEqual([
      { date: "2026-01-01", kind: "rest" },
      { date: "2026-05-01", kind: "rest" },
      { date: "2026-10-08", kind: "work" },
    ])
  })

  it("行转覆盖表：空日期丢掉，同一天后者胜（与 Go 的 map 行为一致）", () => {
    expect(
      overridesFromHolidayRows([
        { date: "", kind: "rest" },
        { date: "2026-10-01", kind: "rest" },
        { date: " 2026-10-01 ", kind: "work" },
      ])
    ).toEqual({ "2026-10-01": "work" })
  })
})

describe("预览呈现", () => {
  it("按配置的时区渲染，而不是浏览器本地时区", () => {
    const point = {
      start: Date.UTC(2026, 9, 1, 0, 30),
      end: Date.UTC(2026, 9, 1, 1, 0),
      period: "标准时段",
      multiplier: 1,
      workday: true,
    }
    expect(formatSchedulePoint(point, "Asia/Shanghai")).toBe("10-01 08:30 → 10-01 09:00")
    expect(formatSchedulePoint(point, "UTC")).toBe("10-01 00:30 → 10-01 01:00")
  })

  it("午夜渲染成 00:00 而不是 24:00（同一根时间轴上不能有两种说法）", () => {
    const point = {
      start: Date.UTC(2026, 9, 1, 16, 0), // 东八区 10-02 00:00
      end: Date.UTC(2026, 9, 1, 16, 30),
      period: "夜间优惠",
      multiplier: 0.25,
      workday: true,
    }
    expect(formatSchedulePoint(point, "Asia/Shanghai")).toBe("10-02 00:00 → 10-02 00:30")
  })

  it("时区写错时退回本地时区渲染，不让整块预览炸掉", () => {
    const point = { start: 0, end: 60000, period: "", multiplier: 1, workday: false }
    expect(formatSchedulePoint(point, "Not/AZone")).toMatch(/^\d{2}-\d{2} \d{2}:\d{2} → \d{2}-\d{2} \d{2}:\d{2}$/)
  })

  it("乘数展示去掉浮点噪声", () => {
    expect(formatMultiplier(1)).toBe("×1")
    expect(formatMultiplier(0.25)).toBe("×0.25")
    expect(formatMultiplier(0.30000000000000004)).toBe("×0.3")
  })

  it("辅助常量与后端边界一致", () => {
    expect(PREVIEW_DAY_OPTIONS.every((d) => d >= 1 && d <= 31)).toBe(true)
    expect([...WEEKDAY_DISPLAY_ORDER]).toEqual([1, 2, 3, 4, 5, 6, 0])
    expect(weekdayKey(0)).toBe("weekday.0")
    expect(isCalendarDate("2026-10-01")).toBe(true)
    expect(isCalendarDate("2026-10-1")).toBe(false)
    expect(isValidTimezone(" ")).toBe(true)
    expect(parseMultiplier(undefined as unknown as string)).toBeNull()
    expect(isValidTimezone(undefined as unknown as string)).toBe(true)
  })
})
