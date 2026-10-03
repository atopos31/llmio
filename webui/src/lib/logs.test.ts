import { describe, expect, it } from "vitest"

import {
  activeMultiCount,
  joinMulti,
  LOGS_MULTI_KEYS,
  parseMulti,
  readMultiFilter,
  toggleMulti,
} from "@/lib/logs"

/**
 * 日志页多选筛选的纯逻辑。
 *
 * 这些断言钉的都是"页面照常渲染、只是筛出来的东西不对"的那类决定：
 * 空值与旧哨兵值的区分、逗号作为多值编码的口径、以及"没有筛选"的判定。
 */

describe("parseMulti", () => {
  it("按逗号拆开并去掉空白", () => {
    expect(parseMulti("a,b")).toEqual(["a", "b"])
    expect(parseMulti(" a , b ")).toEqual(["a", "b"])
  })

  it("空值、空串、全是分隔符都得到空列表", () => {
    expect(parseMulti(null)).toEqual([])
    expect(parseMulti(undefined)).toEqual([])
    expect(parseMulti("")).toEqual([])
    expect(parseMulti("  ")).toEqual([])
    expect(parseMulti(",,,")).toEqual([])
  })

  it("丢掉空项（逗号写多了不该筛出一个空串取值）", () => {
    expect(parseMulti("a,,b")).toEqual(["a", "b"])
    expect(parseMulti("a,")).toEqual(["a"])
    expect(parseMulti(",a")).toEqual(["a"])
  })

  it("按出现顺序去重", () => {
    // 重复传下去会让后端白比一次，也会让触发器上的计数虚高
    expect(parseMulti("a,b,a")).toEqual(["a", "b"])
  })

  it("单独的 all 是旧版哨兵值，等于没筛", () => {
    // 旧链接里 status=all 表示"不过滤"。若不认它，一条旧书签会变成
    // "筛 status ∈ {all}"——得到一个空表，而且看不出哪里不对。
    expect(parseMulti("all")).toEqual([])
    expect(parseMulti(" all ")).toEqual([])
  })

  it("与别的取值混在一起时，all 按字面意思筛", () => {
    // 只有"整个参数恰好是 all"才算哨兵；否则用户是真的想筛这个名字
    expect(parseMulti("all,foo")).toEqual(["all", "foo"])
    expect(parseMulti("foo,all")).toEqual(["foo", "all"])
  })
})

describe("joinMulti", () => {
  it("用逗号拼回去，空列表得到空串", () => {
    expect(joinMulti(["a", "b"])).toBe("a,b")
    expect(joinMulti([])).toBe("")
  })

  it("与 parseMulti 互为往返", () => {
    for (const raw of ["a,b", "a", "", "a,b,c"]) {
      expect(joinMulti(parseMulti(raw))).toBe(raw)
    }
  })
})

describe("readMultiFilter", () => {
  it("五个维度都读，缺的参数是空数组", () => {
    const params = new URLSearchParams("model=a,b&providerName=p1")
    const f = readMultiFilter((k) => params.get(k))

    expect(f).toEqual({
      providerName: ["p1"],
      model: ["a", "b"],
      status: [],
      style: [],
      authKey: [],
    })
  })

  it("读的是查询参数名本身，不是一个别名", () => {
    // 参数名同时是发给后端的名字。改错一个字母不会报错，只会静默不筛。
    const seen: string[] = []
    readMultiFilter((k) => {
      seen.push(k)
      return null
    })
    expect(seen).toEqual([...LOGS_MULTI_KEYS])
  })
})

describe("activeMultiCount", () => {
  it("数的是选中的取值总数，不是被筛的维度数", () => {
    expect(activeMultiCount(readMultiFilter(() => null))).toBe(0)
    expect(
      activeMultiCount({ providerName: ["a", "b"], model: ["c"], status: [], style: [], authKey: [] })
    ).toBe(3)
  })
})

describe("toggleMulti", () => {
  it("没有就补上，有就去掉", () => {
    expect(toggleMulti([], "a")).toEqual(["a"])
    expect(toggleMulti(["a", "b"], "b")).toEqual(["a"])
  })

  it("切两次回到原处", () => {
    expect(toggleMulti(toggleMulti([], "x"), "x")).toEqual([])
  })

  it("不改原数组（调用方可能把它当作 state 的旧值）", () => {
    const before = ["a"]
    const after = toggleMulti(before, "b")
    expect(before).toEqual(["a"])
    expect(after).not.toBe(before)
  })
})
