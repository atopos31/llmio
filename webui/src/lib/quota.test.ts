import { describe, expect, it } from "vitest"

import {
  applyOverride,
  CHART_STYLES,
  clearSourceOverrides,
  DEFAULT_QUOTA_VIEW,
  defaultFormat,
  editableSource,
  FORMAT_TOKENS,
  formatNum,
  itemOverrideKey,
  itemsOf,
  jsonToText,
  kvToText,
  loadQuotaView,
  pruneOverrides,
  QUOTA_VIEW_STORAGE_KEY,
  QUOTA_VIEW_VERSION,
  itemStyleOf,
  ringLayout,
  queryToText,
  quotaStatusTone,
  renderItemText,
  saveQuotaView,
  sourceTypeKey,
  statusForQuota,
  statusRank,
  styleOf,
  tightestItem,
  unitKindOf,
  windowLabel,
  worstLabel,
  type QuotaItem,
  type QuotaSource,
  type QuotaSourceResult,
  type QuotaViewPrefs,
} from "@/lib/quota"

/**
 * 这些用例钉的是**与控制台展示相关的语义决定**，不是实现细节。
 *
 * 其中格式化引擎是 Go `quota/contract.go` 那份的镜像。镜像本身不参与主渲染
 * （服务端下发的 text 才是真相），只服务于"尚未保存的自定义格式"的实时预览。
 * 但既然存在两份实现，就必须有测试对着 Go 的值固化，否则它们会悄悄分家——
 * 表现为"预览里是这个样子，一保存就变样了"。
 */

/** 造一条余量，只覆盖用例关心的字段。 */
function item(over: Partial<QuotaItem> = {}): QuotaItem {
  return {
    id: "i",
    label: "标签",
    used: null,
    total: null,
    remaining: null,
    percent: null,
    unit: "tokens",
    window: "",
    status: "ok",
    text: "",
    ...over,
  }
}

function prefs(over: Partial<QuotaViewPrefs> = {}): QuotaViewPrefs {
  return { ...DEFAULT_QUOTA_VIEW, overrides: {}, ...over }
}

// ---------------------------------------------------------------------------
// 单位与窗口
// ---------------------------------------------------------------------------

describe("unitKindOf", () => {
  it("按单位分四类，决定默认数值格式", () => {
    expect(unitKindOf("%")).toBe("percent")
    expect(unitKindOf("CNY")).toBe("money")
    expect(unitKindOf("USD")).toBe("money")
    expect(unitKindOf("tokens")).toBe("amount")
    expect(unitKindOf("CREDITS")).toBe("amount")
    expect(unitKindOf("次")).toBe("amount")
    expect(unitKindOf("")).toBe("unknown")
  })

  it("遇到表里没有的单位退回 unknown，而不是抛错", () => {
    // 单位来自服务端的归一结果，但 JSON 不受类型系统保护：
    // 上游接口换了个字段、或配置手改坏了，这里会拿到表外的值。
    // 抛错会让整页打不开，退回 unknown 只会让那一条显示得朴素些。
    expect(unitKindOf("EUR" as never)).toBe("unknown")
  })
})

describe("windowLabel", () => {
  it("窗口用中文展示，与服务端 WindowLabel 一致", () => {
    // 这里刻意不走 i18n：{window} 占位符在服务端就是用中文渲染的，
    // 前端若改成 t()，预览与保存后的结果会不一样。
    expect(windowLabel("5h")).toBe("5 小时")
    expect(windowLabel("day")).toBe("每日")
    expect(windowLabel("week")).toBe("每周")
    expect(windowLabel("month")).toBe("每月")
    expect(windowLabel("total")).toBe("总额")
  })

  it("没有窗口时给空串（模板里 {window} 应渲染成空而不是占位符残留）", () => {
    expect(windowLabel("")).toBe("")
  })
})

// ---------------------------------------------------------------------------
// 数值格式化
// ---------------------------------------------------------------------------

describe("formatNum", () => {
  it("null 表示上游没给，渲染成空串而不是 0", () => {
    // 把"没给"显示成 0 会让用户以为额度用光了，与真实情况相反。
    expect(formatNum(null, "tokens", -1)).toBe("")
  })

  it("非有限数同样按没给处理", () => {
    expect(formatNum(Number.NaN, "tokens", -1)).toBe("")
    expect(formatNum(Number.POSITIVE_INFINITY, "tokens", -1)).toBe("")
  })

  it("指定了精度就照办，不管单位", () => {
    expect(formatNum(1.23456, "tokens", 2)).toBe("1.23")
    expect(formatNum(1.23456, "CNY", 0)).toBe("1")
  })

  it("金额固定两位小数", () => {
    expect(formatNum(12.5, "CNY", -1)).toBe("12.50")
    expect(formatNum(12.5, "USD", -1)).toBe("12.50")
  })

  it("百分比一位小数", () => {
    expect(formatNum(83.456, "%", -1)).toBe("83.5")
  })

  it("额度类按中文计量习惯简写：万 / 亿", () => {
    // 中文是四位一进（万、亿），不是英文的三位一进（K/M/B），
    // 因此不能复用 format.ts 给英文界面用的 compactNumber。
    expect(formatNum(1e8, "tokens", -1)).toBe("1亿")
    expect(formatNum(2.5e8, "tokens", -1)).toBe("2.5亿")
    expect(formatNum(2e4, "CREDITS", -1)).toBe("2万")
    expect(formatNum(1234.5, "tokens", -1)).toBe("1234.5")
  })

  it("额度类的整数不带小数点", () => {
    expect(formatNum(1234, "tokens", -1)).toBe("1234")
  })

  it("单位未知时也走额度类格式（宁可朴素，也不要空白）", () => {
    expect(formatNum(9999, "", -1)).toBe("9999")
  })
})

describe("defaultFormat", () => {
  const at = (over: Partial<QuotaItem>) =>
    defaultFormat({ used: null, total: null, remaining: null, unit: "tokens", ...over })

  it("百分比恒为剩余，且不拼单位（否则会出 8% %）", () => {
    // 这是余量面板，百分比展示"还剩多少"而不是"用掉多��"
    expect(at({ unit: "%", used: 92, total: 100, remaining: 8 })).toBe("{remaining}%")
  })

  it("有总量时主体仍是剩余", () => {
    // 与百分比默认同一口径：面板回答"还剩多少"。剩余量由 used/total 推出。
    expect(at({ unit: "CNY", used: 3, total: 10 })).toBe("{remaining} / {total} {unit}")
    expect(at({ used: 3, total: 10 })).toBe("{remaining} / {total} {unit}")
  })

  it("只有剩余时金额要报出币种，其余单位不拼", () => {
    expect(at({ unit: "USD", remaining: 7 })).toBe("{remaining} {unit}")
    expect(at({ remaining: 7 })).toBe("{remaining}")
  })

  it("只有已用时同理", () => {
    expect(at({ unit: "CNY", used: 3 })).toBe("{used} {unit}")
    expect(at({ used: 3 })).toBe("{used}")
  })

  it("只有总量时只报总量", () => {
    expect(at({ total: 10 })).toBe("{total} {unit}")
  })

  it("单位为空时不留尾随占位符", () => {
    // 空单位会让 "{remaining} {unit}" 渲染出多余空格
    expect(at({ unit: "", used: 3, total: 10 })).toBe("{remaining} / {total}")
  })

  it("三个分量全无时仍给一个模板（渲染出空串，由界面隐藏该条）", () => {
    expect(at({ unit: "tokens" })).toBe("{remaining}")
  })
})

// ---------------------------------------------------------------------------
// 模板渲染
// ---------------------------------------------------------------------------

describe("renderItemText", () => {
  it("没有自定义、条目也没有自带格式时用默认模板", () => {
    const it = item({ remaining: 42, unit: "tokens" })
    expect(renderItemText(it)).toBe("42")
  })

  it("缺 total 时不留下悬空分隔符与双空格", () => {
    // 这是被修掉的一个真实瑕疵：额度类默认模板 {remaining} / {total} {unit}
    // 在缺 total 时会渲染成 "42 /  tokens"。deepseek 的余额接口只返回
    // total_balance，正是最常见的"只有剩余"场景，等于默认路径就是脏的。
    expect(renderItemText(item({ remaining: 42, unit: "tokens" }))).toBe("42")
    expect(renderItemText(item({ remaining: 42, unit: "CNY" }))).toBe("42.00 CNY")
  })

  it("有总量时渲染成 剩余 / 总量 单位", () => {
    // 注意 remaining 是**显式给**的：镜像不做契约的"任二补一"
    // （那条规则只允许有一处实现）。真实路径上条目来自服务端，
    // 到手时 remaining 已被推出，因此这里必须照那个形状构造。
    expect(
      renderItemText(item({ used: 3, total: 10, remaining: 7, unit: "tokens" }))
    ).toBe("7 / 10 tokens")
  })

  it("自定义格式优先于条目自带格式", () => {
    const it = item({ used: 3, total: 10, format: "{used}/{total}", unit: "tokens" })
    expect(renderItemText(it)).toBe("3/10")
    expect(renderItemText(it, "{remaining}")).toBe("")
  })

  it("精度后缀只作用于数值占位符", () => {
    const it = item({ used: 1.005, total: 10 })
    expect(renderItemText(it, "{used:2}")).toBe("1.00")
    expect(renderItemText(it, "{percent:2}")).toBe("")
  })

  it("percent 的精度单独处理（它不是 *float64 而是可空的百分比）", () => {
    expect(renderItemText(item({ percent: 12.345 }), "{percent:2}")).toBe("12.35")
    expect(renderItemText(item({ percent: 12.345 }), "{percent}")).toBe("12.3")
  })

  it("精度后缀非法时当作没写（:-1 / :02 / :x 都不生效）", () => {
    // 与 Go 一致：只有纯十进制且非负的后缀才算精度。
    // "02" 这种前导零 Go 也认（Atoi 接受），但 String(n)!=="02"，
    // 这里刻意与 Go 保持同一个判据，宁可两边都不认也不要各自解释。
    const it = item({ used: 1.25 })
    expect(renderItemText(it, "{used:-1}")).toBe("1.25")
    expect(renderItemText(it, "{used:02}")).toBe("1.25")
    expect(renderItemText(it, "{used:x}")).toBe("1.25")
  })

  it("非数值占位符忽略精度后缀", () => {
    expect(renderItemText(item({ label: "备用" }), "{label:2}")).toBe("备用")
  })

  it("未知占位符原样保留", () => {
    // 静默吞掉会让"模板写错了"看起来像"没数据"，两者排查方向完全不同。
    expect(renderItemText(item(), "{nope}")).toBe("{nope}")
  })

  it("缺失左括号闭合时原样保留剩余部分", () => {
    expect(renderItemText(item({ label: "X" }), "前缀{used")).toBe("前缀{used")
  })

  it("空占位符 {} 原样保留", () => {
    expect(renderItemText(item(), "{}")).toBe("{}")
  })

  it("unit / label / window 直接取值", () => {
    const it = item({ unit: "CNY", label: "主套餐", window: "week" })
    expect(renderItemText(it, "{label}@{window}({unit})")).toBe("主套餐@每周(CNY)")
  })

  it("有值时各分量占位符都取到值", () => {
    const it = item({ used: 3, total: 10, remaining: 7, unit: "tokens" })
    expect(renderItemText(it, "{used}")).toBe("3")
    expect(renderItemText(it, "{total}")).toBe("10")
  })

  it("缺分量的占位符渲染成空串（而不是 null / NaN / 0）", () => {
    // "没给"与"给了 0"是两回事：前者显示 0 会被读成"额度用光了"。
    const empty = item({})
    expect(renderItemText(empty, "{used}")).toBe("")
    expect(renderItemText(empty, "{total}")).toBe("")
    expect(renderItemText(empty, "{percent}")).toBe("")
    expect(renderItemText(empty, "{remaining}")).toBe("")
  })

  it("带精度的 total / remaining 各自走数值分支", () => {
    const it = item({ total: 10, remaining: 5, unit: "tokens" })
    expect(renderItemText(it, "{total:2}")).toBe("10.00")
    expect(renderItemText(it, "{remaining:3}")).toBe("5.000")
  })

  it("带精度但分量缺失时同样是空串", () => {
    expect(renderItemText(item({}), "{total:2}")).toBe("")
    expect(renderItemText(item({}), "{remaining:2}")).toBe("")
    expect(renderItemText(item({}), "{used:2}")).toBe("")
  })

  it("渲染结果两端去空白", () => {
    expect(renderItemText(item({ remaining: 5 }), "  {remaining}  ")).toBe("5")
  })
})

describe("FORMAT_TOKENS", () => {
  it("列出的每个 token 都真的能被渲染（否则芯片点了没反应）", () => {
    const it = item({
      used: 1,
      total: 2,
      remaining: 1,
      percent: 50,
      unit: "tokens",
      label: "L",
      window: "day",
    })
    for (const t of FORMAT_TOKENS) {
      const out = renderItemText(it, t.token)
      expect(out, `${t.token} 应被渲染`).not.toContain("{")
    }
  })
})

// ---------------------------------------------------------------------------
// 展示偏好
// ---------------------------------------------------------------------------

describe("loadQuotaView", () => {
  it("没有存过时给默认值", () => {
    expect(loadQuotaView(null)).toEqual({
      chartStyle: "progress",
      showMeta: true,
      overrides: {},
      version: QUOTA_VIEW_VERSION,
    })
  })

  it("空串也走默认（localStorage 里可能是空值）", () => {
    expect(loadQuotaView("").chartStyle).toBe("progress")
  })

  it("损坏的 JSON 回落到默认而不是抛错", () => {
    // 这段代码在首屏路径上：为一个纯装饰性偏好抛错会让整页打不开。
    expect(loadQuotaView("{不是 json")).toEqual({
      chartStyle: "progress",
      showMeta: true,
      overrides: {},
      version: QUOTA_VIEW_VERSION,
    })
  })

  it("读回存过的值", () => {
    const saved = saveQuotaView(prefs({ chartStyle: "bar", showMeta: false }))
    const got = loadQuotaView(saved)
    expect(got.chartStyle).toBe("bar")
    expect(got.showMeta).toBe(false)
  })

  it("非法的 chartStyle 回落默认", () => {
    expect(loadQuotaView(JSON.stringify({ chartStyle: "pie" })).chartStyle).toBe("progress")
    expect(loadQuotaView(JSON.stringify({})).chartStyle).toBe("progress")
  })

  it("showMeta 只有显式 false 才关（旧数据里它可能缺失）", () => {
    expect(loadQuotaView(JSON.stringify({})).showMeta).toBe(true)
  })

  it("v1 数据把条目上的 chartStyle 提升到数据源级，且条目上那份留着", () => {
    // v1 里样式是整张卡片生效的，早期版本却把它写在单条余量上。
    // 不做迁移的话，用户此前的设置会被静默忽略——表现为
    // "我明明改过样式，怎么没生效"，且无从排查。
    //
    // 提升是**两边都放**同一个值，而不是搬走：v2 起条目样式是条目级的，
    // 留着它渲染结果与 v1 一模一样；删掉则会让用户再打开这条时看到"没设置过"。
    const got = loadQuotaView(
      JSON.stringify({ overrides: { "s1::i1": { chartStyle: "ring" } } })
    )
    expect(got.overrides.s1).toEqual({ chartStyle: "ring" })
    expect(got.overrides["s1::i1"]).toEqual({ chartStyle: "ring" })
    // 读出来就是当前版本，否则每次加载都要重跑一遍提升
    expect(got.version).toBe(QUOTA_VIEW_VERSION)
  })

  it("v2 数据不再提升：条目样式就是条目自己的", () => {
    // 用户明确选了"这条画环、卡片默认进度条"。再提升一次会把整张卡片
    // 也改成环——那正是这次要修掉的毛病。
    const got = loadQuotaView(
      JSON.stringify({
        version: QUOTA_VIEW_VERSION,
        overrides: { "s1::i1": { chartStyle: "ring" } },
      })
    )
    expect(got.overrides.s1).toBeUndefined()
    expect(got.overrides["s1::i1"]).toEqual({ chartStyle: "ring" })
  })

  it("迁移时数据源已有样式则不覆盖它", () => {
    const got = loadQuotaView(
      JSON.stringify({
        overrides: { s1: { chartStyle: "bar" }, "s1::i1": { chartStyle: "ring" } },
      })
    )
    expect(got.overrides.s1).toEqual({ chartStyle: "bar" })
  })

  it("迁移时条目上的其它字段原样保留", () => {
    const got = loadQuotaView(
      JSON.stringify({ overrides: { "s1::i1": { chartStyle: "ring", label: "自定义" } } })
    )
    expect(got.overrides.s1).toEqual({ chartStyle: "ring" })
    expect(got.overrides["s1::i1"]).toEqual({ chartStyle: "ring", label: "自定义" })
  })

  it("非 :: 的键原样保留", () => {
    const got = loadQuotaView(JSON.stringify({ overrides: { s2: { name: "改名" } } }))
    expect(got.overrides.s2).toEqual({ name: "改名" })
  })

  it("overrides 不是对象时给空表", () => {
    expect(loadQuotaView(JSON.stringify({ overrides: null })).overrides).toEqual({})
    expect(loadQuotaView(JSON.stringify({ overrides: "x" })).overrides).toEqual({})
  })

  it("覆盖值不是对象时跳过该条", () => {
    expect(loadQuotaView(JSON.stringify({ overrides: { s: "x", n: 1 } })).overrides).toEqual({})
  })
})

describe("applyOverride", () => {
  it("写入字段", () => {
    const got = applyOverride(prefs(), "s", { name: "改名", format: "{used}" })
    expect(got.overrides.s).toEqual({ name: "改名", format: "{used}" })
  })

  it("空串 / null / undefined / false 都表示清除该字段", () => {
    // 清除而不是置假值：`hidden: false` 与"没设置过"语义等价，
    // 区分开只会让 overrides 里堆满无意义的空对象。
    const base = applyOverride(prefs(), "s", {
      name: "A",
      format: "{used}",
      chartStyle: "ring",
      hidden: true,
    })
    expect(applyOverride(base, "s", { name: "" }).overrides.s.name).toBeUndefined()
    expect(applyOverride(base, "s", { name: null }).overrides.s.name).toBeUndefined()
    expect(applyOverride(base, "s", { name: undefined }).overrides.s.name).toBeUndefined()
    expect(applyOverride(base, "s", { hidden: false }).overrides.s.hidden).toBeUndefined()
    expect(applyOverride(base, "s", { format: "" }).overrides.s.format).toBeUndefined()
    expect(applyOverride(base, "s", { chartStyle: undefined }).overrides.s.chartStyle).toBeUndefined()
  })

  it("清空最后一个字段后整条覆盖消失", () => {
    const one = applyOverride(prefs(), "s", { name: "A" })
    expect(applyOverride(one, "s", { name: "" }).overrides.s).toBeUndefined()
  })

  it("保留同一键上的其它字段", () => {
    const one = applyOverride(prefs(), "s", { name: "A", format: "{used}" })
    expect(applyOverride(one, "s", { name: "B" }).overrides.s).toEqual({
      name: "B",
      format: "{used}",
    })
  })

  it("不修改传入的偏好对象", () => {
    const base = prefs()
    applyOverride(base, "s", { name: "A" })
    expect(base.overrides).toEqual({})
  })
})

describe("styleOf / itemOverrideKey / pruneOverrides", () => {
  it("样式取源级覆盖，没有则回落全局", () => {
    const p = prefs({ chartStyle: "progress", overrides: { s: { chartStyle: "bar" } } })
    expect(styleOf(p, "s")).toBe("bar")
    expect(styleOf(p, "other")).toBe("progress")
  })

  it("条目覆盖键用 :: 分隔", () => {
    expect(itemOverrideKey("s1", "i1")).toBe("s1::i1")
  })

  it("清理已删除数据源的覆盖", () => {
    // 不清理的话，重建一个同 id 的源会"继承"早已无主的旧设置。
    const before = prefs({
      overrides: { keep: { name: "A" }, "keep::i": { label: "L" }, gone: { name: "B" } },
    })
    expect(pruneOverrides(before, ["keep"]).overrides).toEqual({
      keep: { name: "A" },
      "keep::i": { label: "L" },
    })
  })
})

describe("itemStyleOf / ringLayout", () => {
  const pct = (id: string, percent: number, status: QuotaItem["status"] = "ok") =>
    item({ id, percent, status })

  it("条目样式只认条目自己的键，没设置过就是 undefined", () => {
    const p = prefs({
      chartStyle: "progress",
      overrides: { s: { chartStyle: "ring" }, "s::a": { chartStyle: "text" } },
    })
    expect(itemStyleOf(p, "s", "a")).toBe("text")
    // 源级样式不是条目样式：它决定整张卡怎么摆
    expect(itemStyleOf(p, "s", "b")).toBeUndefined()
  })

  it("指定了环的条目就是画环的那几条，其余全进 rest", () => {
    const p = prefs({
      overrides: { "s::a": { chartStyle: "ring" }, "s::b": { chartStyle: "ring" } },
    })
    const items = [pct("a", 10), pct("b", 90), pct("c", 50)]
    const got = ringLayout(p, "s", items)
    expect(got.rings.map((i) => i.id)).toEqual(["a", "b"])
    expect(got.rest.map((i) => i.id)).toEqual(["c"])
  })

  it("一条都没指定时回落到最紧张的那条（与旧版一样）", () => {
    const got = ringLayout(prefs(), "s", [pct("lo", 10), pct("hi", 90)])
    expect(got.rings.map((i) => i.id)).toEqual(["hi"])
    expect(got.rest.map((i) => i.id)).toEqual(["lo"])
  })

  it("明确指定了别样式的条目不参与回落——它不该因为最紧张又被拎出来画环", () => {
    const p = prefs({ overrides: { "s::hi": { chartStyle: "progress" } } })
    const got = ringLayout(p, "s", [pct("hi", 90), pct("lo", 10)])
    expect(got.rings.map((i) => i.id)).toEqual(["lo"])
    expect(got.rest.map((i) => i.id)).toEqual(["hi"])
  })

  it("可回落的条目都挑不出来时给空环，由调用方说「没有可画的」", () => {
    // 两条都没有百分比：算不出比例就没有可比性
    expect(ringLayout(prefs(), "s", [item({ id: "a" }), item({ id: "b" })])).toEqual({
      rings: [],
      rest: [item({ id: "a" }), item({ id: "b" })].map((i) => i),
    })
    // 或者用户把每条都指定成了别的样式
    const p = prefs({ overrides: { "s::a": { chartStyle: "text" } } })
    expect(ringLayout(p, "s", [pct("a", 90)]).rings).toEqual([])
  })
})

describe("clearSourceOverrides", () => {
  it("连条目级覆盖一起清掉，不只是源级那四个字段", () => {
    // 只清源级键会留下"名称恢复了、条目自定义还在"的半截状态。
    const before = prefs({
      overrides: {
        s1: { name: "A", chartStyle: "bar" },
        "s1::i1": { label: "L", format: "{used}" },
        "s1::i2": { hidden: true },
        s2: { name: "B" },
        "s2::i1": { label: "keep me" },
      },
    })
    expect(clearSourceOverrides(before, "s1").overrides).toEqual({
      s2: { name: "B" },
      "s2::i1": { label: "keep me" },
    })
  })

  it("前缀相近的源不受牵连", () => {
    // id 是从配置来的自由字符串，`s1` 与 `s10` 必须分得开。
    const before = prefs({
      overrides: { s1: { name: "A" }, s10: { name: "B" }, "s10::i": { label: "L" } },
    })
    expect(clearSourceOverrides(before, "s1").overrides).toEqual({
      s10: { name: "B" },
      "s10::i": { label: "L" },
    })
  })

  it("没有覆盖时原样返回，不改动其余偏好", () => {
    const before = prefs({ chartStyle: "ring", showMeta: false, overrides: {} })
    expect(clearSourceOverrides(before, "nope")).toEqual(before)
  })
})

describe("CHART_STYLES / 存储键", () => {
  it("四种样式齐备，且 key 落在 quota 命名空间", () => {
    expect(CHART_STYLES.map((s) => s.value)).toEqual(["progress", "ring", "bar", "text"])
    expect(CHART_STYLES.every((s) => s.labelKey.startsWith("chartStyle."))).toBe(true)
    expect(QUOTA_VIEW_STORAGE_KEY).toBe("llmio-quota-view-v1")
  })
})

// ---------------------------------------------------------------------------
// 状态与排序
// ---------------------------------------------------------------------------

describe("statusForQuota", () => {
  it("四态各自映射到不同的语义色槽", () => {
    expect(statusForQuota("ok")).toBe("good")
    expect(statusForQuota("warning")).toBe("warning")
    expect(statusForQuota("exhausted")).toBe("critical")
  })

  it("unknown 走中性色而不是告警色", () => {
    // unknown 是"取数看不懂"，不是"余量紧张"。染成告警色会让真正紧张的
    // 条目淹没在同样的黄色里——那时面板就没法用来做判断了。
    expect(statusForQuota("unknown")).toBe("muted")
    expect(statusForQuota("weird" as never)).toBe("muted")
  })
})

describe("quotaStatusTone", () => {
  it("给出文字色类名；中性态用 muted-foreground", () => {
    expect(quotaStatusTone("ok")).toBe("text-status-good-ink")
    expect(quotaStatusTone("exhausted")).toBe("text-status-critical-ink")
    expect(quotaStatusTone("unknown")).toBe("text-muted-foreground")
  })
})

describe("statusRank", () => {
  it("ok < unknown < warning < exhausted", () => {
    expect(statusRank("ok")).toBeLessThan(statusRank("unknown"))
    expect(statusRank("unknown")).toBeLessThan(statusRank("warning"))
    expect(statusRank("warning")).toBeLessThan(statusRank("exhausted"))
  })

  it("无法识别的状态按 unknown 计，而不是排到最后", () => {
    // 排最后会让它盖过真实的 exhausted——"最差"就报错了。
    expect(statusRank("???" as never)).toBe(statusRank("unknown"))
  })
})

describe("itemsOf", () => {
  it("null / undefined 给空数组，而不是抛错", () => {
    // Go 把 nil 切片序列化成 null，取数失败的源正是这样下发的。
    // 实测过：不做归一的话卡片在 .filter 上抛错，React 卸载整棵树 -> 白屏。
    expect(itemsOf({ items: null })).toEqual([])
    expect(itemsOf(null)).toEqual([])
    expect(itemsOf(undefined)).toEqual([])
  })

  it("有值原样返回", () => {
    const one = item({ id: "x" })
    expect(itemsOf({ items: [one] })).toEqual([one])
  })

  it("空数组仍是空数组（与 null 同样安全）", () => {
    expect(itemsOf({ items: [] })).toEqual([])
  })
})

describe("tightestItem", () => {
  it("没有任何条目时给 null（不编造「一切正常」）", () => {
    expect(tightestItem([])).toBeNull()
  })

  it("全部没有总量时给 null——算不出百分比就没有可比性", () => {
    // 拿它当"最紧张"会得到一个恒为 null 的答案，不如明说没有可比条目。
    expect(tightestItem([item({ status: "exhausted" }), item({ status: "warning" })])).toBeNull()
  })

  it("先比状态：更差的胜出", () => {
    const worse = item({ id: "w", status: "exhausted", percent: 10 })
    const better = item({ id: "b", status: "ok", percent: 99 })
    expect(tightestItem([better, worse])?.id).toBe("w")
    expect(tightestItem([worse, better])?.id).toBe("w")
  })

  it("状态相同时比百分比，大的胜出", () => {
    const hi = item({ id: "hi", status: "warning", percent: 90 })
    const lo = item({ id: "lo", status: "warning", percent: 50 })
    expect(tightestItem([lo, hi])?.id).toBe("hi")
    expect(tightestItem([hi, lo])?.id).toBe("hi")
  })

  it("忽略没有百分比的条目，但保留有百分比的那条", () => {
    const noTotal = item({ id: "n", status: "exhausted" })
    const withPct = item({ id: "p", status: "ok", percent: 3 })
    expect(tightestItem([noTotal, withPct])?.id).toBe("p")
  })
})

describe("worstLabel", () => {
  it("没有最紧张条目时给 null", () => {
    expect(worstLabel(null)).toBeNull()
  })

  it("有百分比时带上百分比", () => {
    expect(
      worstLabel({
        id: "i",
        label: "主套餐",
        status: "warning",
        percent: 82.55,
        sourceId: "s",
        sourceName: "DeepSeek",
      })
    ).toBe("DeepSeek · 主套餐 82.6%")
  })

  it("没有百分比时只给来源与名称", () => {
    // 硬写 0% 会被读成"还剩很多"，而真相是"算不出来"。
    expect(
      worstLabel({
        id: "i",
        label: "余额",
        status: "unknown",
        percent: null,
        sourceId: "s",
        sourceName: "中转站",
      })
    ).toBe("中转站 · 余额")
  })
})

describe("sourceTypeKey", () => {
  it("三种类型各有 i18n 键", () => {
    expect(sourceTypeKey("builtin")).toBe("type.builtin")
    expect(sourceTypeKey("http")).toBe("type.http")
    expect(sourceTypeKey("script")).toBe("type.script")
  })
})

// ---------------------------------------------------------------------------
// 编辑器回填
// ---------------------------------------------------------------------------

function resultOf(over: Partial<QuotaSourceResult> = {}): QuotaSourceResult {
  return {
    id: "s1",
    name: "超算",
    type: "builtin",
    enabled: true,
    ok: true,
    items: null,
    status: "ok",
    latencyMs: 1,
    updatedAt: 0,
    cached: false,
    ...over,
  }
}

describe("editableSource", () => {
  it("用配置里那份完整源，而不是卡片上的展示形状", () => {
    // 展示形状只有 id / 名称 / 类型，拿它当表单初值会让保存把 path、headers、
    // 字段映射、env 一并清掉（保存是整份替换）。
    const cfg: QuotaSource = {
      id: "s1",
      name: "超算",
      enabled: true,
      type: "builtin",
      builtin: "scnet",
      baseUrl: "https://www.scnet.cn",
      path: "",
      env: { SCNET_USER: "alice", SCNET_PASS: "****4321" },
    }
    expect(editableSource(resultOf(), cfg)).toEqual(cfg)
  })

  it("配置里找不到这一条时退回展示形状，且不猜内置适配器", () => {
    // 猜错适配器会把超算源悄悄变成另一个数据源——宁可让用户自己选一次。
    const got = editableSource(resultOf({ note: "国家超算" }), undefined)
    expect(got).toEqual({
      id: "s1",
      name: "超算",
      enabled: true,
      type: "builtin",
      note: "国家超算",
    })
    expect(got.builtin).toBeUndefined()
  })
})

describe("kvToText", () => {
  it("每行一条 KEY=VALUE", () => {
    expect(kvToText({ SCNET_USER: "alice", SCNET_PASS: "pw" })).toBe(
      "SCNET_USER=alice\nSCNET_PASS=pw"
    )
  })

  it("没有值给空串，而不是 undefined 字样", () => {
    expect(kvToText(undefined)).toBe("")
    expect(kvToText({})).toBe("")
    expect(kvToText({ A: undefined, B: null, C: 0 })).toBe("A=\nB=\nC=0")
  })

  it("字面量的 = 标记原样带出：map 的 unit==CREDITS 就是这么写出来的", () => {
    // 后端约定「值以 = 开头即字面量」，标记存在值里，因此这里不需要额外规则
    expect(kvToText({ unit: "=CREDITS", used: "usage.used" })).toBe(
      "unit==CREDITS\nused=usage.used"
    )
  })
})

describe("jsonToText", () => {
  it("对象序列化成一行 JSON", () => {
    expect(jsonToText({ "x-foo": "bar" })).toBe('{"x-foo":"bar"}')
  })

  it("null / undefined 给空串", () => {
    // 写 "null" 会让回填的文本框看起来有内容，保存时又被当成 JSON 原样写回
    expect(jsonToText(null)).toBe("")
    expect(jsonToText(undefined)).toBe("")
  })

  it("标量与数组照样序列化", () => {
    expect(jsonToText(0)).toBe("0")
    expect(jsonToText([1, 2])).toBe("[1,2]")
  })
})

describe("queryToText", () => {
  it("拼成 k=v&k2=v2", () => {
    expect(queryToText({ page: "1", size: "20" })).toBe("page=1&size=20")
  })

  it("非对象一律给空串", () => {
    expect(queryToText(undefined)).toBe("")
    expect(queryToText(null)).toBe("")
    expect(queryToText("page=1")).toBe("")
    expect(queryToText(3)).toBe("")
  })

  it("跳过硬不出值的键，其余照拼", () => {
    expect(queryToText({ a: null, b: undefined, c: 1 })).toBe("c=1")
  })
})
