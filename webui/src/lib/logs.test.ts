import { describe, expect, it } from "vitest"

import type { ChatLog } from "@/lib/api"
import {
  activeMultiCount,
  applyMultiFilter,
  canViewChatIO,
  joinMulti,
  LEGACY_MULTI_KEYS,
  LOGS_MULTI_KEYS,
  parseMulti,
  readMultiFilter,
  readMultiValue,
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
  it("六个维度都读，缺的参数是空数组", () => {
    const params = new URLSearchParams("requestName=a,b&providerModel=gpt-4o&providerName=p1")
    const f = readMultiFilter((k) => params.get(k))

    expect(f).toEqual({
      providerName: ["p1"],
      requestName: ["a", "b"],
      providerModel: ["gpt-4o"],
      status: [],
      style: [],
      authKey: [],
    })
  })

  it("旧链接的 model 按请求名解释（只读别名）", () => {
    // 旧版 `?model=gpt` 装的是请求名。改成新键名后，一条老书签仍得按原意
    // 生效，而不是变成"什么都没筛"——那是静默的行为回归。
    const params = new URLSearchParams("model=gpt-4o")
    expect(readMultiFilter((k) => params.get(k)).requestName).toEqual(["gpt-4o"])
  })

  it("两个键都在时以规范键名为准", () => {
    // 否则一次界面操作写回 requestName 后，残留的 model 又会在下一次刷新时
    // 抢回控制权。
    const params = new URLSearchParams("requestName=beta&model=alpha")
    expect(readMultiValue((k) => params.get(k), "requestName")).toEqual(["beta"])
  })

  it("别名上的旧哨兵值 all 同样是没筛", () => {
    const params = new URLSearchParams("model=all")
    expect(readMultiValue((k) => params.get(k), "requestName")).toEqual([])
  })

  it("读的是规范键名，唯一例外是请求名的只读别名", () => {
    // 参数名同时是发给后端的名字。改错一个字母不会报错，只会静默不筛。
    // 顺序也钉住：先全部规范键，再一次旧别名兜底——别名在中间出现说明
    // 某个规范键被改名了而没人发现。
    const seen: string[] = []
    readMultiFilter((k) => {
      seen.push(k)
      return null
    })
    expect(seen).toEqual([
      "providerName",
      "requestName",
      ...LEGACY_MULTI_KEYS,
      "providerModel",
      "status",
      "style",
      "authKey",
    ])
    expect(seen.length).toBe(LOGS_MULTI_KEYS.length + LEGACY_MULTI_KEYS.length)
  })
})

describe("applyMultiFilter", () => {
  it("非空写规范键名，并翻回第 1 页", () => {
    const params = new URLSearchParams("page=3")
    applyMultiFilter(params, "providerModel", ["gpt-4o", "gpt-4o-mini"])

    expect(params.get("providerModel")).toBe("gpt-4o,gpt-4o-mini")
    expect(params.get("page")).toBeNull()
  })

  it("全取消时删掉参数而不是写空串", () => {
    const params = new URLSearchParams("requestName=a")
    applyMultiFilter(params, "requestName", [])

    expect(params.get("requestName")).toBeNull()
  })

  it("写入时清掉所有只读别名，旧链接上的取值不残留", () => {
    for (const legacy of LEGACY_MULTI_KEYS) {
      const params = new URLSearchParams(`${legacy}=alpha&providerName=p1`)
      applyMultiFilter(params, "requestName", ["beta"])

      expect(params.get("requestName")).toBe("beta")
      expect(params.get(legacy)).toBeNull()
      // 无关维度不动
      expect(params.get("providerName")).toBe("p1")
    }
  })

  it("返回的是同一个实例（调用方就地用它去 setSearchParams）", () => {
    const params = new URLSearchParams()
    expect(applyMultiFilter(params, "status", ["error"])).toBe(params)
  })

  it("旧链接上的已选项先经 readMultiValue 读出再回写，不会被点选丢掉", () => {
    const params = new URLSearchParams("model=alpha")
    const next = applyMultiFilter(
      params,
      "requestName",
      toggleMulti(readMultiValue((k) => params.get(k), "requestName"), "beta")
    )

    expect(next.get("requestName")).toBe("alpha,beta")
  })
})

describe("activeMultiCount", () => {
  it("数的是选中的取值总数，不是被筛的维度数", () => {
    expect(activeMultiCount(readMultiFilter(() => null))).toBe(0)
    expect(
      activeMultiCount({
        providerName: ["a", "b"],
        requestName: ["c"],
        providerModel: [],
        status: [],
        style: [],
        authKey: [],
      })
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

/** canViewChatIO 只读两个字段，其余取值与判定无关，给一份能过类型检查的填充即可。 */
function logOf(over: Partial<ChatLog> = {}): ChatLog {
  return {
    ID: 1,
    CreatedAt: "2026-09-30T10:00:00+08:00",
    Name: "gpt-4o",
    TraceID: "trace-1",
    ProviderModel: "gpt-4o",
    ProviderName: "openai",
    Status: "success",
    Style: "openai",
    UserAgent: "curl/8.0",
    Error: "",
    Retry: 0,
    ProxyTime: 0,
    FirstChunkTime: 0,
    ChunkTime: 0,
    Tps: 0,
    ChatIO: true,
    Size: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    prompt_tokens_details: { cached_tokens: 0 },
    key_name: "",
    input_price: 0,
    cache_read_price: 0,
    output_price: 0,
    currency: "CNY",
    ...over,
  }
}

describe("canViewChatIO", () => {
  it("成功且有 IO 记录：可看", () => {
    expect(canViewChatIO(logOf())).toBe(true)
  })

  it("失败请求：后端不落 ChatIO 行，不可看", () => {
    expect(canViewChatIO(logOf({ Status: "error", ChatIO: false }))).toBe(false)
  })

  it("失败请求但 ioLog 开启、落了 ChatIO：可看", () => {
    expect(canViewChatIO(logOf({ Status: "error", ChatIO: true }))).toBe(true)
  })

  it("进行中的请求：不可看", () => {
    expect(canViewChatIO(logOf({ Status: "running", ChatIO: true }))).toBe(false)
  })

  it("成功但未开启 IO 记录：同样不可看", () => {
    expect(canViewChatIO(logOf({ Status: "success", ChatIO: false }))).toBe(false)
  })
})
