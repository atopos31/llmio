/**
 * 峰谷计费的纯逻辑层：时段窗口、本地校验、表单 ↔ 载荷映射、节假日覆盖的往返。
 *
 * ## 两半，各有归属
 *
 * 后端把峰谷拆成了两处，这一层照搬那条线（见 models/peak.go 与 handler/peak.go）：
 *
 *   - **条款**（开关 + 时段）挂在「模型 × 上游」的关联上：峰谷窗口是上游的
 *     商务条款，同一时间点上 A 家打折、B 家峰时是正常的。表单与校验是
 *     terms* 那几个函数，界面上住在关联编辑器里。
 *   - **日历**（时区 / 星期几 / 日期覆盖 / 节假日同步）是全局事实：哪天放假、
 *     按哪个时区算"今天"，对所有上游是同一个答案。表单与校验是 calendar*
 *     那几个函数，界面上是配置页的那张卡片。
 *
 * 判定要同时用到两者（"这一刻是不是夜间优惠"既看条款也看日历），但**存储与
 * 编辑完全分开**，这一层不做任何把两半合起来的东西。
 *
 * ## 真相来源与这一层的边界
 *
 * 权威在后端：`handler/peak.go` 的 validatePeakTerms / validatePeakCalendar 是
 * 最终裁决，`service/peak.go` 的 ParseClock / InClockWindow / PreviewSchedule
 * 是语义定义。这里做两件事，别的一概不做：
 *
 *   1. 把用户能犯的错在**提交之前**指出是哪一段、哪一条（省一次往返，
 *      也避免把后端那句英文 message 直接甩到脸上）；
 *   2. 把表单状态翻译成后端要的形状（"24:00"、空列表、布尔指针的省略）。
 *
 * 校验规则逐条对齐后端的两个 validate，下面每条都注明它对应哪一处。
 * 两边口径不一致的代价是"界面上说没问题、保存时才被拒"——这正是要避免的；
 * 但前端这一份只在提交前拦一道，**后端仍然是唯一权威**，它的 message
 * 要原样透出，不能被一句"保存失败"替掉。
 *
 * ## 刻意不做的事（每一件都是踩过才有理由）
 *
 *   - **不重算价格**。预览里每一段的区间与乘数由 POST /peak-calendar/preview
 *     下发（service.PreviewSchedule），这里只负责呈现。从 periods 再推一遍
 *     等于把"首个命中者胜出""跨零点""按日期覆盖工作日"整套判定抄第二遍，
 *     抄错的那一天没人看得出来。
 *   - **不给 periods 自动排序**。ResolvePeriod 的语义是**首个命中者胜出**，
 *     数组顺序就是优先级；按时间自动排一次序，会静默把"先特例后一般"的
 *     意图打乱，改的是每一笔请求的价格。顺序由用户在界面上用上移/下移控制。
 *   - **不把时段重叠当错误**。后端的 validatePeakTerms 里根本没有重叠检查——
 *     重叠是允许的，由顺序裁决。因此这里只给提示（findPeriodConflicts），
 *     不拦保存；拦了就等于前端凭空加了一条后端没有的规则。
 */

// ---------------------------------------------------------------------------
// 契约形状（与 models/peak.go 的 json tag 一一对应，改后端务必同步这里）
// ---------------------------------------------------------------------------

/** 一个计费时段。multiplier 是**相对基础价的乘数**，不是绝对价。 */
export interface PeakPeriod {
  name: string
  /** 一天中的 "HH:MM"，允许 "24:00"；start > end 表示跨零点。 */
  start: string
  end: string
  /** 1 表示不加价不减价。 */
  multiplier: number
  /** 限定星期几（0=周日 … 6=周六）。缺省表示不限制。 */
  days?: number[]
  /** 限定工作日/休息日。缺省表示不限制。对应 Go 的 *bool，故用可选而非三态枚举。 */
  workday?: boolean
}

/**
 * 某个上游的峰谷条款，落在 ModelWithProvider.Peak 上。
 *
 * 数组顺序即优先级（首个命中者胜出），因此**不要**在保存前按时间排序。
 */
export interface PeakTerms {
  enabled: boolean
  periods: PeakPeriod[]
}

/** 全局工作日日历（configs 表的 peak_calendar 键）。 */
export interface PeakCalendar {
  /** 判定时段所用的时区；空 = 服务器本地时区。 */
  timezone: string
  /** 工作日定义（0=周日 … 6=周六），空 = 周一至周五。 */
  weekdays?: number[]
  /** 按日期覆盖工作日判定，键 "YYYY-MM-DD"，值 "work" / "rest"。 */
  dateOverrides: Record<string, string>
  /** 最近一次节假日同步的 unix 秒，0/缺省 = 从未同步。 */
  holidaySyncedAt?: number
  holidaySource?: string
}

/** 预览时间轴上的一段（后端 service.SchedulePoint）。 */
export interface SchedulePoint {
  /** Unix 毫秒，闭区间起点。 */
  start: number
  /** Unix 毫秒，**开区间**终点：末段的结束时刻正好是下一个边界。 */
  end: number
  /** 命中的时段名，空串表示按基础价。 */
  period: string
  multiplier: number
  /** 该段起始日是否为工作日（已按日历的时区与日期覆盖判定）。 */
  workday: boolean
}

/**
 * POST /peak-calendar/preview 的数据体。
 *
 * timezone 由服务端回传：它才是判定"这段是不是夜间优惠"所用的那个时区。
 * 编辑器手上只有条款、拿不到全局日历，不回传它就只能按浏览器本地时区渲染，
 * 会出现"08:30 命中了夜间优惠"这种自相矛盾的画面。
 */
export interface PreviewResult {
  timezone: string
  points: SchedulePoint[]
}

/** POST /peak-calendar/holidays/sync 的数据体。 */
export interface PeakHolidaySyncResult {
  year: number
  count: number
  source: string
  syncedAt: number
  /** 同步后的完整日历：服务端已落盘，界面拿它直接替换本地状态即可。 */
  calendar: PeakCalendar
}

export type DateOverrideKind = "work" | "rest"

/** 预览天数上限，与 handler/peak.go 里 days 的 1..31 校验一致。 */
export const PREVIEW_DAY_OPTIONS = [1, 3, 7, 14, 31]

/** 星期在界面上的展示顺序：周一在前（与中文习惯一致，与 Go 的 0=周日 无关）。 */
export const WEEKDAY_DISPLAY_ORDER = [1, 2, 3, 4, 5, 6, 0] as const

/** 星期几的 i18n 键。取值 0=周日 … 6=周六。 */
export function weekdayKey(day: number): string {
  return `weekday.${day}`
}

// ---------------------------------------------------------------------------
// 时刻
// ---------------------------------------------------------------------------

const MINUTES_PER_DAY = 24 * 60

/**
 * "HH:MM" → 一天中的分钟数；不合法返回 null。
 *
 * 对应后端 service.ParseClock。几个必须一致的点：
 *   - 允许 "24:00"（=1440），这是把一整天写成 "00:00"-"24:00" 的唯一写法；
 *   - "24:30" 不合法；
 *   - 小时/分钟允许一位数（"8:5" 合法），后端用 strconv.Atoi 解析，
 *     这里用 \d{1,2} 对齐；
 *   - 只认十进制数字：Atoi("1e2") 会失败，Number("1e2") 不会，所以不能图省事用 Number()。
 *
 * 边界差异是刻意的：后端还接受 "+8:30"（Atoi 认符号），这里不接受。
 * 前端更严只会拦下一次手改配置文件的怪异写法，不会放过后端会拒的值。
 */
export function parseClock(raw: string): number | null {
  const s = (raw ?? "").trim()
  const parts = s.split(":")
  if (parts.length !== 2) return null
  const [hs, ms] = parts
  if (!/^\d{1,2}$/.test(hs.trim()) || !/^\d{1,2}$/.test(ms.trim())) return null
  const h = Number(hs.trim())
  const m = Number(ms.trim())
  if (h > 24 || m > 59) return null
  if (h === 24 && m !== 0) return null
  return h * 60 + m
}

/** 分钟数 → "HH:MM"。1440 输出 "24:00"（与后端的可读写法保持一致）。 */
export function formatClock(minutes: number): string {
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`
}

/**
 * 把用户填的时刻规范化成 "HH:MM"；填的不是时刻则返回 null。
 *
 * 用途是回填与提交前的归一："8:5" → "08:05"。后端两种都收，但界面上
 * 混着 "8:5" 与 "08:05" 会让"这两段是不是同一段"变得要靠眼睛数。
 */
export function normalizeClock(raw: string): string | null {
  const minutes = parseClock(raw)
  return minutes === null ? null : formatClock(minutes)
}

/** 比较两个时刻的先后；任一不合法返回 null。 */
export function compareClock(a: string, b: string): number | null {
  const ma = parseClock(a)
  const mb = parseClock(b)
  if (ma === null || mb === null) return null
  return ma - mb
}

/** start > end 即跨零点（如 22:00-06:00）。 */
export function isOvernight(start: string, end: string): boolean {
  const s = parseClock(start)
  const e = parseClock(end)
  return s !== null && e !== null && s > e
}

/**
 * 时段覆盖的分钟数。跨零点补一天，start == end 视为覆盖全天。
 *
 * start == end 那一支其实走不到：validateTermsForm 直接把它拒了
 * （"which would cover the whole day"）。这里保留与 InClockWindow 同样的
 * 语义，是为了不在两处对同一个输入给出不同答案。
 */
export function windowLength(start: string, end: string): number | null {
  const s = parseClock(start)
  const e = parseClock(end)
  if (s === null || e === null) return null
  if (s === e) return MINUTES_PER_DAY
  return e > s ? e - s : MINUTES_PER_DAY - s + e
}

/**
 * 某时刻是否落在 [start, end) 窗口内。对应后端 service.InClockWindow。
 * 用于重叠提示——预览时间轴上的命中判定不在这里，那是服务端算好的。
 */
export function clockContains(minutes: number, start: string, end: string): boolean {
  const s = parseClock(start)
  const e = parseClock(end)
  if (s === null || e === null) return false
  if (s === e) return true
  return s < e ? minutes >= s && minutes < e : minutes >= s || minutes < e
}

/**
 * 两个时刻窗口是否有交集。
 *
 * 判法：两个非空圆弧相交 ⇔ 其中一个包含另一个的起点。窗口都是 [start,end)
 * 的半开区间，所以 08:00-20:00 与 20:00-22:00 不算重叠（20:00 属于后者）。
 * 任一时刻不合法时返回 false：那种时段由 validateTermsForm 专门报错，
 * 不该在这里再报一次"重叠"。
 */
export function clocksOverlap(aStart: string, aEnd: string, bStart: string, bEnd: string): boolean {
  const bs = parseClock(bStart)
  const as = parseClock(aStart)
  if (bs === null || as === null) return false
  return clockContains(bs, aStart, aEnd) || clockContains(as, bStart, bEnd)
}

// ---------------------------------------------------------------------------
// 时段冲突（提示，不是错误）
// ---------------------------------------------------------------------------

/** 两组星期限制是否有交集。空数组 = 不限制 = 与任何一组都有交集。 */
function daysIntersect(a?: number[], b?: number[]): boolean {
  if (!a?.length || !b?.length) return true
  return a.some((d) => b.includes(d))
}

/** 两条 workday 限制能否同时成立。缺省 = 不限，故与任何值都能同时成立。 */
function workdayCompatible(a?: boolean, b?: boolean): boolean {
  if (a === undefined || b === undefined) return true
  return a === b
}

/** 两个时段是否存在"同一时刻都命中"的可能（不含谁优先的问题）。 */
export function periodsConflict(a: PeakPeriod, b: PeakPeriod): boolean {
  if (!daysIntersect(a.days, b.days)) return false
  if (!workdayCompatible(a.workday, b.workday)) return false
  return clocksOverlap(a.start, a.end, b.start, b.end)
}

/**
 * 排序键：时段的窗口起点（分钟）。
 *
 * 解析不出来的排到最后——它们由 validateTermsForm 单独报错，不该在这里
 * 替它们编一个靠前的名次。
 */
export function periodStartMinutes(p: PeakPeriod): number {
  return parseClock(p.start) ?? Number.MAX_SAFE_INTEGER
}

/**
 * 找出互相重叠的时段对，按窗口起点排序。
 *
 * 只用于**提示**：重叠并不被后端拒绝，先命中者胜出（数组顺序即优先级），
 * 所以界面上要说的是"这两段会在同一时刻生效，靠顺序决定谁算"，而不是
 * "请修改"。返回的下标是 0-based 数组下标，由调用方 +1 显示。
 */
export function findPeriodConflicts(periods: PeakPeriod[]): Array<[number, number]> {
  const pairs: Array<[number, number]> = []
  for (let i = 0; i < periods.length; i++) {
    for (let j = i + 1; j < periods.length; j++) {
      if (periodsConflict(periods[i], periods[j])) pairs.push([i, j])
    }
  }
  const startOf = (i: number) => periodStartMinutes(periods[i])
  return pairs.sort(([ai, bi], [aj, bj]) => startOf(ai) - startOf(aj) || startOf(bi) - startOf(bj))
}

// ---------------------------------------------------------------------------
// 值的校验（与 handler/peak.go 的两个 validate 逐条对齐）
// ---------------------------------------------------------------------------

export type PeakIssueKey =
  | "err_start_invalid"
  | "err_end_invalid"
  | "err_same_clock"
  | "err_days_range"
  | "err_weekday_range"
  | "err_timezone"
  | "err_override_date"
  | "err_override_value"
  | "err_multiplier"

/** 一条本地校验结论。文字由界面翻译，故这里只给键与参数。 */
export interface PeakIssue {
  key: PeakIssueKey
  /** 时段序号，1-based（与后端报错的 "period 2 (...)" 同一口径）。时段之外的规则不填。 */
  index?: number
  /** 时段名，供提示里点名是哪一段。 */
  name?: string
  /** 出问题的原值（日期、"work" 之外的值、填错的乘数……）。 */
  value?: string
}

/**
 * IANA 时区名是否可用。
 *
 * 对齐后端 validatePeakCalendar 的 time.LoadLocation 分支，两点差异要说清：
 *   - 空串合法：空 = 用服务器本地时区，后端对空串直接跳过校验；
 *   - 这里靠 Intl 试解析（浏览器没有 tzdata 接口），个别 Go 认、ICU 不认的
 *     别名可能被判错。判错的后果只是提前拦下一次保存，后端仍是权威。
 */
export function isValidTimezone(tz: string): boolean {
  const s = (tz ?? "").trim()
  if (s === "") return true
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: s })
    return true
  } catch {
    return false
  }
}

/**
 * 是否是后端认的 "YYYY-MM-DD"。
 *
 * 比正则多一步：后端用 time.Parse("2006-01-02")，它同时拒绝 "2026-2-3"
 * 与 "2026-02-30"。只查正则会放过后者，用户会看到"日期没错但存不进去"。
 */
export function isCalendarDate(raw: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(raw)) return false
  const [y, m, d] = raw.split("-").map(Number)
  const t = new Date(Date.UTC(y, m - 1, d))
  return t.getUTCFullYear() === y && t.getUTCMonth() === m - 1 && t.getUTCDate() === d
}

/**
 * 乘数输入 → 数字；不是有限数则返回 null。
 *
 * **后端完全不校验乘数**（validatePeakTerms 里没有这一项），这条是前端补的，
 * 理由是 JSON 载不动 NaN：JSON.stringify(NaN) 得到 null，Go 侧解出来是 0 ——
 * 一次"手滑清空了输入框"的保存会把那一档价格静默改成免费。
 * 负乘数则是语义上没人想要的东西（负价），一并拦掉。
 */
export function parseMultiplier(raw: string): number | null {
  const s = (raw ?? "").trim()
  if (s === "") return null
  if (!/^-?\d+(\.\d+)?$/.test(s)) return null
  const v = Number(s)
  // 正则已经排除了 NaN/Infinity，这里只剩"负数"这一种拒绝理由
  return v >= 0 ? v : null
}

/** 乘数的展示文本：×0.25、×1、×1.5。去掉浮点噪声（0.30000000000000004 → 0.3）。 */
export function formatMultiplier(m: number): string {
  const trimmed = Number(m.toFixed(4))
  return `×${trimmed}`
}

// ---------------------------------------------------------------------------
// 条款：表单形状与映射
// ---------------------------------------------------------------------------

/** 时段的 workday 在表单里是三态：不限 / 只在工作日 / 只在休息日。 */
export type WorkdayFilter = "any" | "work" | "rest"

export interface PeakPeriodForm {
  name: string
  start: string
  end: string
  /**
   * 乘数在表单里存**字符串**而不是 number。
   *
   * 存 number 的话，"清空输入框"会让它变成 NaN，提交时 JSON.stringify 把
   * NaN 写成 null，后端解成 0——那次保存静默把这一档改成免费。字符串能把
   * "没填/填错"一直保留到校验那一步。
   */
  multiplier: string
  /** 空数组 = 不限星期。 */
  days: number[]
  workday: WorkdayFilter
}

export interface PeakTermsForm {
  enabled: boolean
  periods: PeakPeriodForm[]
}

/**
 * 一份新的条款模板：**关闭**状态，两段示例时段。
 *
 * 与后端 models.DefaultPeakTerms 逐字对齐（关闭 + 08:30-00:30 标准价、
 * 00:30-08:30 半价的示例）。默认关闭是刻意的——分时段计费会改变成本数字的
 * 语义，在用户明确打开开关前不该静默生效；给两段示例则是因为打开开关的人
 * 马上要填的就是时段，照抄示例改数字比从零写起快得多。
 */
export function defaultTermsForm(): PeakTermsForm {
  return {
    enabled: false,
    periods: [
      { name: "标准时段", start: "08:30", end: "00:30", multiplier: "1", days: [], workday: "any" },
      { name: "夜间优惠", start: "00:30", end: "08:30", multiplier: "0.25", days: [], workday: "any" },
    ],
  }
}

/** 条款 → 表单。深拷贝，避免表单里的编辑改到父组件持有的那份配置。 */
export function termsToForm(terms: PeakTerms): PeakTermsForm {
  return {
    enabled: terms.enabled,
    periods: (terms.periods ?? []).map((p) => ({
      name: p.name ?? "",
      // 回填时归一：配置里手写的 "8:30" 在这里变成 "08:30"，否则输入框里
      // 会同时出现两种写法，而"它们其实是同一段"要用户自己看出来
      start: normalizeClock(p.start ?? "") ?? (p.start ?? ""),
      end: normalizeClock(p.end ?? "") ?? (p.end ?? ""),
      multiplier: String(p.multiplier ?? 1),
      days: [...(p.days ?? [])],
      workday: p.workday === undefined ? "any" : p.workday ? "work" : "rest",
    })),
  }
}

/**
 * 条款表单 → 提交载荷。
 *
 * 几处刻意的取舍：
 *   - 空 days **省略**：后端的"空"等价于不限，省掉后配置文件里也看得清
 *     "这里就是不限制"；
 *   - periods 为空照发 []（不是 null）：空列表是合法配置——启用后所有请求
 *     按基础价，ResolvePeriod 返回 nil、乘数落到 1；
 *   - 乘数解析不出来时回落 1（基础价）。调用方应当先跑 validateTermsForm；
 *     真有漏网的，落成基础价也比落成 0（免费）安全。
 */
export function termsFormToPayload(form: PeakTermsForm): PeakTerms {
  return {
    enabled: form.enabled,
    periods: form.periods.map((p) => {
      const period: PeakPeriod = {
        name: p.name.trim(),
        start: normalizeClock(p.start) ?? p.start.trim(),
        end: normalizeClock(p.end) ?? p.end.trim(),
        multiplier: parseMultiplier(p.multiplier) ?? 1,
      }
      if (p.days.length) period.days = [...p.days].sort((a, b) => a - b)
      if (p.workday !== "any") period.workday = p.workday === "work"
      return period
    }),
  }
}

// ---------------------------------------------------------------------------
// 日历：表单形状与映射
// ---------------------------------------------------------------------------

export interface HolidayRow {
  /** "YYYY-MM-DD" */
  date: string
  /**
   * "work" / "rest"；手工编辑过的配置里可能还有别的值。
   *
   * 这里**保留原值**并让校验报出来，而不是丢掉：丢掉等于替用户做了一次
   * 静默删除，而这份覆盖是花了一次外网同步换来的。
   */
  kind: string
}

export interface PeakCalendarForm {
  timezone: string
  weekdays: number[]
  holidays: HolidayRow[]
  /**
   * 同步元数据必须跟着表单走。
   *
   * PUT /peak-calendar 是**整份覆盖**，而这两项与覆盖表存在同一份配置里。
   * 表单不带它们回传，用户改一个时区就会顺手抹掉"最近同步于何时、来自哪里"，
   * 界面随之后退成"从未同步"——而节假日覆盖其实还在。
   */
  holidaySyncedAt?: number
  holidaySource?: string
}

/** 日历 → 表单。深拷贝，理由同 termsToForm。 */
export function calendarToForm(cal: PeakCalendar): PeakCalendarForm {
  return {
    timezone: cal.timezone ?? "",
    weekdays: [...(cal.weekdays ?? [])],
    holidays: holidayRowsFromOverrides(cal.dateOverrides ?? {}),
    holidaySyncedAt: cal.holidaySyncedAt,
    holidaySource: cal.holidaySource,
  }
}

/**
 * 日历表单 → 提交载荷。
 *
 * 取舍与条款那份一致：空 weekdays 省略（后端把空当周一~周五）；
 * dateOverrides 永远带对象，空也给 {}——对应 models.PeakCalendar 上那句
 * "刻意不加 omitempty"，否则配置文件里看不到这个字段，手工编辑时
 * 不知道有它可填。
 */
export function calendarFormToPayload(form: PeakCalendarForm): PeakCalendar {
  const out: PeakCalendar = {
    timezone: form.timezone.trim(),
    dateOverrides: overridesFromHolidayRows(form.holidays),
  }
  if (form.weekdays.length) out.weekdays = [...form.weekdays].sort((a, b) => a - b)
  if (form.holidaySyncedAt) out.holidaySyncedAt = form.holidaySyncedAt
  if (form.holidaySource) out.holidaySource = form.holidaySource
  return out
}

// ---------------------------------------------------------------------------
// 节假日覆盖
// ---------------------------------------------------------------------------

/** 覆盖表 → 行列表，按日期升序（"YYYY-MM-DD" 的字典序就是时间序）。 */
export function holidayRowsFromOverrides(overrides: Record<string, string>): HolidayRow[] {
  return Object.entries(overrides)
    .map(([date, kind]) => ({ date, kind }))
    .sort((a, b) => a.date.localeCompare(b.date))
}

/**
 * 行列表 → 覆盖表。同一天出现两次时**后者胜**。
 *
 * 与 Go 侧一致：map[string]string 解 JSON 时重复键就是后者覆盖前者。
 * 界面不阻止重复（用户可能是先加了一条、后来又加了一条），保存后的结果
 * 因此必须与后端对同一份输入的解读相同，否则界面上看到的和生效的不一样。
 */
export function overridesFromHolidayRows(rows: HolidayRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const row of rows) {
    const date = row.date.trim()
    if (!date) continue // 空行是"还没填完"，不该落成一条空键（后端会直接拒）
    out[date] = row.kind
  }
  return out
}

// ---------------------------------------------------------------------------
// 本地校验
// ---------------------------------------------------------------------------

/**
 * 校验条款，返回**全部**问题而不是第一个。
 *
 * 逐条对应 handler/peak.go validatePeakTerms：
 *   - start / end 解析          → err_start_invalid / err_end_invalid
 *   - start == end              → err_same_clock（后端："would cover the whole day"）
 *   - period.days ∈ 0..6        → err_days_range
 *   - 乘数是有限的非负数        → err_multiplier（**后端没有这条**，理由见 parseMultiplier）
 *
 * 不在列表里的两项都是刻意的：时段重叠（后端允许，见 findPeriodConflicts）
 * 与"启用但没有时段"（后端允许，等价于全部按基础价）。
 */
export function validateTermsForm(form: PeakTermsForm): PeakIssue[] {
  const issues: PeakIssue[] = []

  form.periods.forEach((p, i) => {
    const index = i + 1
    const name = p.name.trim()
    const start = parseClock(p.start)
    const end = parseClock(p.end)
    if (start === null) issues.push({ key: "err_start_invalid", index, name, value: p.start })
    if (end === null) issues.push({ key: "err_end_invalid", index, name, value: p.end })
    // start == end 只有两边都合法时才谈得上：否则前面两条已经报过了
    if (start !== null && end !== null && start === end) {
      issues.push({ key: "err_same_clock", index, name })
    }
    if (p.days.some((d) => d < 0 || d > 6 || !Number.isInteger(d))) {
      issues.push({ key: "err_days_range", index, name })
    }
    if (parseMultiplier(p.multiplier) === null) {
      issues.push({ key: "err_multiplier", index, name, value: p.multiplier })
    }
  })

  return issues
}

/**
 * 校验日历，返回**全部**问题而不是第一个。
 *
 * 逐条对应 handler/peak.go validatePeakCalendar：
 *   - weekdays ∈ 0..6           → err_weekday_range
 *   - timezone 可解析           → err_timezone
 *   - 覆盖键是 YYYY-MM-DD       → err_override_date
 *   - 覆盖值 ∈ {work, rest}     → err_override_value
 */
export function validateCalendarForm(form: PeakCalendarForm): PeakIssue[] {
  const issues: PeakIssue[] = []

  if (form.weekdays.some((d) => d < 0 || d > 6 || !Number.isInteger(d))) {
    issues.push({ key: "err_weekday_range" })
  }

  if (!isValidTimezone(form.timezone)) {
    issues.push({ key: "err_timezone", value: form.timezone })
  }

  for (const row of form.holidays) {
    const date = row.date.trim()
    if (!isCalendarDate(date)) {
      issues.push({ key: "err_override_date", value: row.date })
      continue // 日期都不对，值的对错没有意义，别一次报两条
    }
    if (row.kind !== "work" && row.kind !== "rest") {
      issues.push({ key: "err_override_value", value: `${date}=${row.kind}` })
    }
  }

  return issues
}

// ---------------------------------------------------------------------------
// 预览呈现
// ---------------------------------------------------------------------------

/**
 * 把预览时间轴上的一段排成 "MM-DD HH:mm → MM-DD HH:mm"。
 *
 * 自己拼字符串而不是让 Intl 输出整句：各语言的分隔符与顺序都不同，
 * 中文会给"10/01 08:30"，英文给"10/01, 08:30"，测试与界面都会跟着语言漂移。
 * 这里只要一个稳定的、能一眼比对的形状。
 *
 * 必须按**判定所用的时区**渲染：预览的毫秒是绝对时刻，而"这段是不是夜间优惠"
 * 是按日历的时区判定的；用浏览器本地时区显示，用户会看到"08:30 命中了夜间优惠"
 * 这种自相矛盾的画面。该时区由服务端在预览响应里回传。
 */
export function formatSchedulePoint(point: SchedulePoint, timezone: string): string {
  return `${instantText(point.start, timezone)} → ${instantText(point.end, timezone)}`
}

function instantText(ms: number, timezone: string): string {
  const base: Intl.DateTimeFormatOptions = {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    // 用 h23 而不是 hour12:false：后者在部分 ICU 版本里把午夜渲染成 24:00，
    // 于是同一条时间轴上出现"10-01 24:00"和"10-02 00:00"两个说法
    hourCycle: "h23",
  }
  let fmt: Intl.DateTimeFormat
  try {
    fmt = new Intl.DateTimeFormat("en-US", { ...base, timeZone: timezone })
  } catch {
    // 时区填错时后端本就会拒绝保存；但预览不该因此整块炸掉——
    // 退回浏览器本地时区，至少还能看出"时段大概长这样"
    fmt = new Intl.DateTimeFormat("en-US", base)
  }
  const parts = fmt.formatToParts(new Date(ms))
  // 这四种 part 在 en-US 下一定存在，缺了说明 Intl 出了问题；
  // 写 ?.value ?? "" 只会让"渲染成空串"代替一次明确的崩溃
  const get = (type: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === type)!.value
  return `${get("month")}-${get("day")} ${get("hour")}:${get("minute")}`
}
