import { describe, expect, it } from "vitest"

import {
  activeFilterCount,
  ADMIN_KEY_ID,
  buildStatsQuery,
  customRangeError,
  DEFAULT_MODEL_SORT,
  DIMENSIONS,
  dimensionFilterValues,
  dimensionGroups,
  EMPTY_FILTER,
  ERROR_BAR_LIMIT,
  errorBarBase,
  errorBarWidth,
  errorViewMode,
  fromLocalInput,
  groupByModel,
  isMissingModelMetric,
  keyFilterOptions,
  logDetailPath,
  MODEL_DIMENSIONS,
  modelDimensionGroups,
  MODEL_SORT_KEYS,
  nextModelSort,
  observedOptions,
  presetRange,
  shortenLabel,
  sortModelGroups,
  toLocalInput,
  toggleDimensionValue,
  toggleValue,
  UA_DISPLAY_MAX,
  type AnalyticsFilter,
  type ModelSort,
} from "@/lib/analytics"
import type { AuthKeyItem, ErrorGroup, GroupStat, StatsResult } from "@/lib/api"

/**
 * 这里钉的是**口径**：时间范围的边界、筛选怎么变成查询串、什么情况下
 * 错误类别不该画条形图。它们写歪了页面照样渲染，只是数字的含义变了。
 */

/** 造一个分组，只覆盖用例关心的字段。 */
function group(over: Partial<GroupStat> = {}): GroupStat {
  return {
    name: "g",
    total: 1,
    success: 1,
    error: 0,
    running: 0,
    successRate: 100,
    prompt: 0,
    completion: 0,
    totalTokens: 0,
    cached: 0,
    cacheHitRate: 0,
    avgTps: 0,
    maxTps: 0,
    avgFirstChunkMs: 0,
    p95FirstChunkMs: 0,
    avgProxyMs: 0,
    retries: 0,
    cost: 0,
    ...over,
  }
}

function filter(over: Partial<AnalyticsFilter> = {}): AnalyticsFilter {
  return { ...EMPTY_FILTER, ...over }
}

/** 只有下钻五个数组的结果，够 dimensionGroups 用。 */
function statsWith(over: Partial<StatsResult> = {}): StatsResult {
  return {
    byModel: [],
    byProvider: [],
    byKey: [],
    byName: [],
    byUa: [],
    byModelProvider: [],
    ...over,
  } as StatsResult
}

describe("presetRange", () => {
  // 固定一个本地时刻，避免用例随运行时间漂移。取一个非零点的时刻，
  // 否则"今天"与"最近 24 小时"会算出同一个起点而掩盖口径差异。
  const now = new Date(2026, 8, 30, 14, 30, 0) // 2026-09-30 14:30 本地

  it("今天从本地零点起，右端是此刻", () => {
    const r = presetRange("today", now)
    expect(new Date(r.from * 1000).getHours()).toBe(0)
    expect(new Date(r.from * 1000).getDate()).toBe(30)
    expect(r.to).toBe(Math.floor(now.getTime() / 1000))
  })

  it("昨天是完整的自然日，右端落在今日零点而不是此刻", () => {
    // 这条是关键：右端若取 now，"昨天"会把今天的流量也算进来。
    const r = presetRange("yesterday", now)
    const from = new Date(r.from * 1000)
    const to = new Date(r.to * 1000)
    expect(from.getDate()).toBe(29)
    expect(from.getHours()).toBe(0)
    expect(to.getDate()).toBe(30)
    expect(to.getHours()).toBe(0)
  })

  it("近 N 小时是滚动窗口，右端是此刻", () => {
    const r = presetRange("last_24h", now)
    expect(r.to - r.from).toBe(24 * 3600)
    expect(r.to).toBe(Math.floor(now.getTime() / 1000))
  })

  it("近 7 天 / 近 30 天按整日数给窗口", () => {
    expect(presetRange("last_7d", now).to - presetRange("last_7d", now).from).toBe(7 * 24 * 3600)
    expect(presetRange("last_30d", now).to - presetRange("last_30d", now).from).toBe(30 * 24 * 3600)
  })
})

describe("自定义范围的输入往返", () => {
  const local = new Date(2026, 8, 30, 14, 30, 0).getTime() / 1000

  it("按本地时间拼，不走 UTC", () => {
    // 这条是本组的存在理由：用 toISOString() 会在东八区整整错 8 小时，
    // 用户选"14:30"却查到 06:30 的数据
    expect(toLocalInput(local)).toBe("2026-09-30T14:30")
  })

  it("个位数的月/日/时/分补零", () => {
    expect(toLocalInput(new Date(2026, 0, 5, 9, 7, 0).getTime() / 1000)).toBe("2026-01-05T09:07")
  })

  it("秒被丢弃（输入框只到分钟）", () => {
    expect(toLocalInput(new Date(2026, 8, 30, 14, 30, 59).getTime() / 1000)).toBe(
      "2026-09-30T14:30"
    )
  })

  it("往返不变", () => {
    const s = fromLocalInput(toLocalInput(local))
    expect(s).toBe(Math.floor(local))
  })

  it("空值与非法值返回 null", () => {
    expect(fromLocalInput("")).toBeNull()
    expect(fromLocalInput("不是时间")).toBeNull()
  })
})

describe("customRangeError", () => {
  it("未填全报 incomplete", () => {
    expect(customRangeError("", "2026-09-30T14:30")).toBe("incomplete")
    expect(customRangeError("2026-09-30T14:30", "")).toBe("incomplete")
  })

  it("前后颠倒报 order", () => {
    expect(customRangeError("2026-09-30T14:30", "2026-09-30T10:00")).toBe("order")
  })

  it("起止相同也算颠倒（窗口宽度为零，查不出东西）", () => {
    expect(customRangeError("2026-09-30T14:30", "2026-09-30T14:30")).toBe("order")
  })

  it("过去 → 未来是合法的", () => {
    // 查未来等于查到现在为止，后端如实返回空桶；不在客户端假装它非法
    expect(customRangeError("2026-09-29T00:00", "2030-01-01T00:00")).toBeNull()
  })

  it("合法窗口返回 null", () => {
    expect(customRangeError("2026-09-29T00:00", "2026-09-30T00:00")).toBeNull()
  })
})

describe("筛选状态", () => {
  it("空筛选计数为 0", () => {
    expect(activeFilterCount(EMPTY_FILTER)).toBe(0)
  })

  it("计数把六个字段上的已选项累加", () => {
    expect(
      activeFilterCount(
        filter({
          provider: ["a", "b"],
          model: ["m"],
          key: [ADMIN_KEY_ID],
          name: ["n"],
          ua: ["u"],
          status: ["error"],
        })
      )
    ).toBe(7)
  })

  it("切换：不在则加入，在则移除", () => {
    expect(toggleValue([], "a")).toEqual(["a"])
    expect(toggleValue(["a", "b"], "b")).toEqual(["a"])
  })

  it("切换用值相等而不是引用相等，重复加入不会产生重复项", () => {
    // 选项值来自服务端字符串，多次点击同一个值必须幂等。
    const once = toggleValue([], "x")
    expect(toggleValue(once, "x")).toEqual([])
  })
})

describe("buildStatsQuery", () => {
  const base = { from: 100, to: 200, granularity: "auto", filter: EMPTY_FILTER }

  it("总是带上时间范围，且以字符串下发", () => {
    // 后端接受数字与字符串两种，但 URLSearchParams 只接受字符串；
    // 这里显式转换，避免调用方各自记得转。
    expect(buildStatsQuery(base)).toEqual({ from: "100", to: "200" })
  })

  it("granularity 为 auto 或空时不发送", () => {
    // 后端对空值与 "auto" 是同一分支，发送只是把默认值明写一遍。
    expect(buildStatsQuery(base).granularity).toBeUndefined()
    expect(buildStatsQuery({ ...base, granularity: "" }).granularity).toBeUndefined()
  })

  it("显式档位原样下发", () => {
    expect(buildStatsQuery({ ...base, granularity: "1h" }).granularity).toBe("1h")
  })

  it("多值筛选用逗号连接", () => {
    const q = buildStatsQuery({
      ...base,
      filter: filter({ provider: ["p1", "p2"], model: ["m1"], name: ["n1", "n2"], ua: ["u1"] }),
    })
    expect(q.provider).toBe("p1,p2")
    expect(q.model).toBe("m1")
    expect(q.name).toBe("n1,n2")
    expect(q.ua).toBe("u1")
  })

  it("密钥筛选走 key_id 而不是 key", () => {
    // 后端只认 key_id；名字不唯一（resolveKeyGroups 会按名字合并，
    // 说明存在同名密钥），传名字会在同名时筛错。
    const q = buildStatsQuery({ ...base, filter: filter({ key: ["3", "0"] }) })
    expect(q.key_id).toBe("3,0")
  })

  it("未选中的维度不出现在查询里", () => {
    const q = buildStatsQuery({ ...base, filter: filter({ status: ["error"] }) })
    expect(q.provider).toBeUndefined()
    expect(q.model).toBeUndefined()
    expect(q.name).toBeUndefined()
    expect(q.ua).toBeUndefined()
    expect(q.key_id).toBeUndefined()
    expect(q.status).toBe("error")
  })
})

describe("下钻维度", () => {
  it("五个维度齐备且顺序稳定", () => {
    // 顺序即界面上的分段控件顺序，改它是一次可见的界面变更。
    expect(DIMENSIONS).toEqual(["model", "provider", "key", "name", "ua"])
  })

  it("每个维度取到对应的数组", () => {
    const s = statsWith({
      byModel: [group({ name: "m" })],
      byProvider: [group({ name: "p" })],
      byKey: [group({ name: "k" })],
      byName: [group({ name: "n" })],
      byUa: [group({ name: "u" })],
    })
    expect(dimensionGroups(s, "model")[0].name).toBe("m")
    expect(dimensionGroups(s, "provider")[0].name).toBe("p")
    expect(dimensionGroups(s, "key")[0].name).toBe("k")
    expect(dimensionGroups(s, "name")[0].name).toBe("n")
    expect(dimensionGroups(s, "ua")[0].name).toBe("u")
  })

  it("没有数据时是空数组而不是 undefined", () => {
    // 页面直接 .map 它；服务端对空结果给的是 [] 而不是 null，
    // 但类型上仍要保证这里可迭代。
    expect(dimensionGroups(statsWith(), "ua")).toEqual([])
  })

  it("每个维度都能读回自己在筛选里的值", () => {
    const f = filter({ model: ["m"], provider: ["p"], key: ["3"], name: ["n"], ua: ["u"] })
    expect(dimensionFilterValues(f, "model")).toEqual(["m"])
    expect(dimensionFilterValues(f, "provider")).toEqual(["p"])
    expect(dimensionFilterValues(f, "key")).toEqual(["3"])
    expect(dimensionFilterValues(f, "name")).toEqual(["n"])
    expect(dimensionFilterValues(f, "ua")).toEqual(["u"])
  })

  it("点一行切换的是该维度自己的字段，不碰其他维度", () => {
    // 下钻的语义就是"把这一行变成一条筛选条件"；串到别的字段上会
    // 筛出一个看似合理、实则另一回事的结果。
    const before = filter({ model: ["m"], status: ["error"] })
    const after = toggleDimensionValue(before, "provider", "p")
    expect(after.provider).toEqual(["p"])
    expect(after.model).toEqual(["m"])
    expect(after.status).toEqual(["error"])
  })

  it("再点同一行取消该筛选（幂等）", () => {
    const once = toggleDimensionValue(EMPTY_FILTER, "ua", "curl/8")
    expect(toggleDimensionValue(once, "ua", "curl/8").ua).toEqual([])
  })

  it("返回值是新对象，不改原状态", () => {
    // 页面把筛选放在 useState 里，原地改不会触发重渲染。
    const before = filter()
    const after = toggleDimensionValue(before, "name", "n")
    expect(before.name).toEqual([])
    expect(after).not.toBe(before)
  })
})

describe("shortenLabel", () => {
  it("不超长时原样返回", () => {
    expect(shortenLabel("short")).toBe("short")
    expect(shortenLabel("x".repeat(UA_DISPLAY_MAX))).toHaveLength(UA_DISPLAY_MAX)
  })

  it("超长时截断并加省略号", () => {
    const out = shortenLabel("x".repeat(UA_DISPLAY_MAX + 10))
    expect(out).toHaveLength(UA_DISPLAY_MAX + 1)
    expect(out.endsWith("…")).toBe(true)
  })

  it("按字符计数，多字节字符不会被切坏", () => {
    // 按字节截断会产出无效 UTF-8，中文 UA 注释里出现过。
    const out = shortenLabel("中".repeat(10), 4)
    expect(out).toBe("中中中中…")
  })
})

describe("observedOptions", () => {
  it("取分组名，保持服务端给的顺序（按请求数降序）", () => {
    expect(observedOptions([group({ name: "a" }), group({ name: "b" })])).toEqual(["a", "b"])
  })

  it("保留服务端填的未知值——它是一个真实分组，不是脏数据", () => {
    expect(observedOptions([group({ name: "未知" })])).toEqual(["未知"])
  })

  it("没有分组时为空", () => {
    expect(observedOptions([])).toEqual([])
  })
})

describe("keyFilterOptions", () => {
  const keys: AuthKeyItem[] = [
    { id: 3, name: "生产" },
    { id: 7, name: "测试" },
    { id: 9, name: "从未使用" },
  ]

  it("只保留窗口内出现过的密钥", () => {
    // 选了必然返回空结果的选项是把用户往坑里带。
    expect(keyFilterOptions([group({ name: "生产" })], keys)).toEqual([
      { value: "3", label: "生产" },
    ])
  })

  it("admin 出现时置前，并映射到 id 0", () => {
    // admin 是后端 keyLabel(0) 的固定值（管理台 TOKEN 直连），不在 AuthKey 表里。
    const out = keyFilterOptions([group({ name: "测试" }), group({ name: "admin" })], keys)
    expect(out).toEqual([
      { value: ADMIN_KEY_ID, label: "admin" },
      { value: "7", label: "测试" },
    ])
  })

  it("没有 admin 时不凭空造出这一项", () => {
    expect(keyFilterOptions([group({ name: "测试" })], keys).map((o) => o.value)).toEqual(["7"])
  })

  it("匹配不上 id 的观测名被丢掉——构造不出可用的筛选条件", () => {
    // 密钥删除后历史日志仍在，分组名会留下。给一个选了没反应的选项更糟。
    expect(keyFilterOptions([group({ name: "已删除的密钥" })], keys)).toEqual([])
  })

  it("没有任何观测数据时为空", () => {
    expect(keyFilterOptions([], keys)).toEqual([])
  })
})

describe("错误类别的呈现", () => {
  function err(count: number): ErrorGroup {
    return { type: "t", code: "c", count, providers: [], models: [], samples: [] }
  }

  it("不超过 7 类时画条形图", () => {
    expect(errorViewMode(1)).toBe("bars")
    expect(errorViewMode(ERROR_BAR_LIMIT)).toBe("bars")
  })

  it("超过 7 类时改上表格", () => {
    // 分类色板只有 8 槽；"折叠成其他"会把用户最需要看的少数大类淹掉。
    expect(errorViewMode(ERROR_BAR_LIMIT + 1)).toBe("table")
    expect(errorViewMode(20)).toBe("table")
  })

  it("也没有错误类别时仍画条形图（画出来是空态）", () => {
    expect(errorViewMode(0)).toBe("bars")
  })

  it("宽度基准取最大类别的计数", () => {
    expect(errorBarBase([err(3), err(11), err(5)])).toBe(11)
  })

  it("没有类别时基准为 1，避免除零", () => {
    expect(errorBarBase([])).toBe(1)
  })

  it("宽度按基准等比换算", () => {
    expect(errorBarWidth(5, 10)).toBe(50)
    expect(errorBarWidth(10, 10)).toBe(100)
  })

  it("基准为 0 或负时不给宽度，而不是给出 Infinity/NaN", () => {
    expect(errorBarWidth(5, 0)).toBe(0)
    expect(errorBarWidth(5, -1)).toBe(0)
  })

  it("超过基准时钳到 100%（防御：基准与类别来自两次不同的遍历）", () => {
    expect(errorBarWidth(20, 10)).toBe(100)
  })
})

describe("logDetailPath", () => {
  it("指向请求内容页而不是弹窗", () => {
    // 分析页需要"从聚合数字跳到那一条请求"的通路；内容页是排查终点。
    expect(logDetailPath(42)).toBe("/logs/42/chat-io")
  })
})

describe("模型性能排序", () => {
  /** 只取名称次序，断言读起来才像"谁在前谁在后"。 */
  function names(gs: GroupStat[], sort: ModelSort): string[] {
    return sortModelGroups(gs, sort).map((g) => g.name)
  }

  it("列顺序即表头顺序，且只含数值列", () => {
    expect(MODEL_SORT_KEYS).toEqual([
      "total",
      "successRate",
      "avgTps",
      "maxTps",
      "avgFirstChunkMs",
      "p95FirstChunkMs",
      "retries",
      "cost",
      "totalTokens",
    ])
  })

  it("缺值判定只对「0 表示没有样本」的列成立", () => {
    // 这几个只在成功请求上累加，0 就是没有样本
    expect(isMissingModelMetric("avgTps", 0)).toBe(true)
    expect(isMissingModelMetric("avgTps", 40)).toBe(false)
    // 而 0 次重试、0 成本是真实读数，不是缺值
    expect(isMissingModelMetric("total", 0)).toBe(false)
    expect(isMissingModelMetric("retries", 0)).toBe(false)
    expect(isMissingModelMetric("cost", 0)).toBe(false)
  })

  it("点同一列翻转方向，换一列从降序起步", () => {
    expect(DEFAULT_MODEL_SORT).toEqual({ key: "total", dir: "desc" })
    // 同列：desc → asc
    expect(nextModelSort(DEFAULT_MODEL_SORT, "total")).toEqual({ key: "total", dir: "asc" })
    // 同列：asc → desc
    expect(nextModelSort({ key: "total", dir: "asc" }, "total")).toEqual({
      key: "total",
      dir: "desc",
    })
    // 换列：一律从降序起步
    expect(nextModelSort({ key: "total", dir: "asc" }, "cost")).toEqual({ key: "cost", dir: "desc" })
  })

  it("降序把最大读数排在前", () => {
    const gs = [
      group({ name: "slow", avgTps: 10 }),
      group({ name: "fast", avgTps: 50 }),
      group({ name: "mid", avgTps: 30 }),
    ]
    expect(names(gs, { key: "avgTps", dir: "desc" })).toEqual(["fast", "mid", "slow"])
  })

  it("【关键】没有样本（0）的模型在升序与降序下都必须排在最后，不能被当成最快", () => {
    // 这是本表与普通数字表最要紧的区别：若按升序把小值放前面，
    // avgTps=0 会跑到第一行，读出来就是"这个模型最快"，与事实相反。
    const gs = [
      group({ name: "slow", avgTps: 10 }),
      group({ name: "fast", avgTps: 50 }),
      group({ name: "nosample", avgTps: 0 }),
    ]
    expect(names(gs, { key: "avgTps", dir: "desc" })).toEqual(["fast", "slow", "nosample"])
    expect(names(gs, { key: "avgTps", dir: "asc" })).toEqual(["slow", "fast", "nosample"])
  })

  it("缺值行之间的先后也稳定（按名称），不会随输入顺序跳", () => {
    const a = group({ name: "z", avgTps: 0 })
    const b = group({ name: "a", avgTps: 0 })
    expect(names([a, b], { key: "avgTps", dir: "asc" })).toEqual(["a", "z"])
    expect(names([b, a], { key: "avgTps", dir: "asc" })).toEqual(["a", "z"])
  })

  it("缺值与非缺值成对出现时两个方向都被判定（两种入参顺序）", () => {
    const none = group({ name: "none", avgTps: 0 })
    const some = group({ name: "some", avgTps: 5 })
    expect(names([none, some], { key: "avgTps", dir: "desc" })).toEqual(["some", "none"])
    expect(names([some, none], { key: "avgTps", dir: "desc" })).toEqual(["some", "none"])
  })

  it("同值时按名称定序，且不改动入参数组", () => {
    const input = [group({ name: "b", total: 5 }), group({ name: "a", total: 5 })]
    expect(names(input, { key: "total", dir: "desc" })).toEqual(["a", "b"])
    // 入参来自 props 里的 stats，就地排序会污染页面的数据
    expect(input.map((g) => g.name)).toEqual(["b", "a"])
  })

  it("同名时按 0 处理，不抛错", () => {
    const gs = [group({ name: "same", total: 1 }), group({ name: "same", total: 1 })]
    expect(names(gs, { key: "total", dir: "desc" })).toEqual(["same", "same"])
  })

  it("任何列都能排（以成本与重试为例）", () => {
    const gs = [
      group({ name: "cheap", cost: 0.1, retries: 5 }),
      group({ name: "pricey", cost: 9, retries: 1 }),
    ]
    expect(names(gs, { key: "cost", dir: "desc" })).toEqual(["pricey", "cheap"])
    expect(names(gs, { key: "retries", dir: "asc" })).toEqual(["pricey", "cheap"])
  })
})

describe("模型性能的呈现维度", () => {
  it("三个维度齐备且顺序稳定", () => {
    expect(MODEL_DIMENSIONS).toEqual(["model", "provider", "modelProvider"])
  })

  it("每个维度取到对应的数组", () => {
    const stats = statsWith({
      byModel: [group({ name: "m" })],
      byProvider: [group({ name: "p" })],
      byModelProvider: [group({ name: "m · p" })],
    })
    expect(modelDimensionGroups(stats, "model").map((g) => g.name)).toEqual(["m"])
    expect(modelDimensionGroups(stats, "provider").map((g) => g.name)).toEqual(["p"])
    expect(modelDimensionGroups(stats, "modelProvider").map((g) => g.name)).toEqual(["m · p"])
  })
})

describe("模型×上游按模型归组", () => {
  /** 一行「模型 · 上游」。分量字段是服务端另填的，这里一并给上。 */
  function joint(model: string, provider: string, total: number): GroupStat {
    return group({ name: `${model} · ${provider}`, model, provider, total })
  }

  it("把同一模型的上游收进一组，并给出组名", () => {
    const groups = groupByModel([joint("a", "x", 3), joint("a", "y", 2), joint("b", "x", 1)])
    expect(groups.map((g) => g.model)).toEqual(["a", "b"])
    expect(groups[0].rows.map((r) => r.provider)).toEqual(["x", "y"])
  })

  it("组的先后按各行合计现算，不跟着输入顺序走", () => {
    // b 单独一行就比 a 的任意一行大，但 a 有两行、合计更大——按分量比会排错
    const groups = groupByModel([joint("b", "x", 4), joint("a", "x", 3), joint("a", "y", 3)])
    expect(groups.map((g) => g.model)).toEqual(["a", "b"])
  })

  it("合计相同时按模型名的码点定序，不随输入顺序跳", () => {
    const input = [joint("b", "x", 1), joint("a", "x", 1)]
    expect(groupByModel(input).map((g) => g.model)).toEqual(["a", "b"])
    expect(groupByModel([...input].reverse()).map((g) => g.model)).toEqual(["a", "b"])
  })

  it("模型名自己带分隔符时不会被拆开", () => {
    // 行名是拼出来的，靠拆字符串认模型必然在这里翻车：
    // 「a · b」这个名字按分隔符拆会得到两个模型，实际是同一个
    const groups = groupByModel([joint("a · b", "x", 2), joint("a", "b · x", 1)])
    expect(groups.map((g) => g.model)).toEqual(["a · b", "a"])
    expect(groups[0].rows).toHaveLength(1)
  })

  it("分量字段缺失时回落到行名——老接口没有 model 字段也还能摆出来", () => {
    const groups = groupByModel([group({ name: "m · p" }), group({ name: "m · q" })])
    expect(groups).toHaveLength(2)
    expect(groups.map((g) => g.model)).toEqual(["m · p", "m · q"])
  })

  it("空输入给空数组", () => {
    expect(groupByModel([])).toEqual([])
  })
})
