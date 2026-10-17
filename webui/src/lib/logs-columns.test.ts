import { describe, expect, it } from "vitest"

import type { ChatLog } from "@/lib/api"
import {
  LOG_COLUMNS,
  LOG_COLUMN_IDS,
  LOG_DEFAULT_VISIBLE_IDS,
  LOG_TOGGLEABLE_IDS,
  cacheRate,
  computeCost,
  visibleColumns,
} from "./logs-columns"

/** 一条最小可用的日志行：缺省字段都是零或空串。 */
function makeLog(overrides: Partial<ChatLog> = {}): ChatLog {
  return {
    ID: 1,
    CreatedAt: "2024-01-01T00:00:00Z",
    Name: "gpt-4",
    TraceID: "trace-123",
    SessionID: "",
    ProviderModel: "gpt-4",
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
    ChatIO: false,
    Size: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    prompt_tokens_details: { cached_tokens: 0 },
    key_name: "",
    input_price: 0,
    cache_read_price: 0,
    output_price: 0,
    currency: "USD",
    ...overrides,
  }
}

describe("computeCost", () => {
  it("零价格得到零成本", () => {
    expect(computeCost(makeLog())).toBe(0)
  })

  it("输入 + 输出成本", () => {
    const log = makeLog({
      prompt_tokens: 1_000_000,
      completion_tokens: 500_000,
      input_price: 3,
      output_price: 15,
    })
    // 1M input * $3 + 0.5M output * $15 = $3 + $7.5 = $10.5
    expect(computeCost(log)).toBeCloseTo(10.5, 6)
  })

  it("缓存 token 按缓存价计费，不占输入价", () => {
    const log = makeLog({
      prompt_tokens: 1_000_000,
      completion_tokens: 0,
      prompt_tokens_details: { cached_tokens: 500_000 },
      input_price: 3,
      cache_read_price: 0.3,
    })
    // 0.5M uncached * $3 + 0.5M cached * $0.3 = $1.5 + $0.15 = $1.65
    expect(computeCost(log)).toBeCloseTo(1.65, 6)
  })

  it("全部命中缓存时输入价不参与", () => {
    const log = makeLog({
      prompt_tokens: 1_000_000,
      completion_tokens: 0,
      prompt_tokens_details: { cached_tokens: 1_000_000 },
      input_price: 3,
      cache_read_price: 0.3,
    })
    expect(computeCost(log)).toBeCloseTo(0.3, 6)
  })

  it("缓存超过输入时按输入价算零（Math.max 保护）", () => {
    const log = makeLog({
      prompt_tokens: 100_000,
      prompt_tokens_details: { cached_tokens: 200_000 },
      input_price: 3,
      cache_read_price: 0.3,
    })
    // uncached = max(0, 100k - 200k) = 0; cached = 200k * $0.3 = $0.06
    expect(computeCost(log)).toBeCloseTo(0.06, 6)
  })

  it("缺省 prompt_tokens_details 时不崩", () => {
    const log = makeLog({
      prompt_tokens: 1_000_000,
      completion_tokens: 1_000_000,
      input_price: 3,
      output_price: 15,
      prompt_tokens_details: undefined as unknown as ChatLog["prompt_tokens_details"],
    })
    expect(computeCost(log)).toBeCloseTo(18, 6)
  })

  it("小数 token 数正常工作", () => {
    const log = makeLog({
      prompt_tokens: 1500,
      completion_tokens: 250,
      input_price: 2,
      output_price: 8,
    })
    // 1500/1e6 * 2 + 250/1e6 * 8 = 0.003 + 0.002 = 0.005
    expect(computeCost(log)).toBeCloseTo(0.005, 8)
  })

  it("所有字段缺省时不崩", () => {
    const log = makeLog({
      prompt_tokens: undefined as unknown as number,
      completion_tokens: undefined as unknown as number,
      input_price: undefined as unknown as number,
      cache_read_price: undefined as unknown as number,
      output_price: undefined as unknown as number,
      prompt_tokens_details: undefined as unknown as ChatLog["prompt_tokens_details"],
    })
    expect(computeCost(log)).toBe(0)
  })
})

describe("cacheRate", () => {
  it("正常命中率", () => {
    const log = makeLog({
      prompt_tokens: 1000,
      prompt_tokens_details: { cached_tokens: 750 },
    })
    expect(cacheRate(log)).toBe(75)
  })

  it("零输入返回 null（不适用而非 0%）", () => {
    const log = makeLog({ prompt_tokens: 0 })
    expect(cacheRate(log)).toBeNull()
  })

  it("负数输入返回 null", () => {
    const log = makeLog({ prompt_tokens: -1 })
    expect(cacheRate(log)).toBeNull()
  })

  it("缺省 prompt_tokens_details 时命中率为 0%", () => {
    const log = makeLog({
      prompt_tokens: 1000,
      prompt_tokens_details: undefined as unknown as ChatLog["prompt_tokens_details"],
    })
    expect(cacheRate(log)).toBe(0)
  })

  it("缺省 prompt_tokens 时返回 null（不崩）", () => {
    const log = makeLog({ prompt_tokens: undefined as unknown as number })
    expect(cacheRate(log)).toBeNull()
  })

  it("全部命中返回 100%", () => {
    const log = makeLog({
      prompt_tokens: 1000,
      prompt_tokens_details: { cached_tokens: 1000 },
    })
    expect(cacheRate(log)).toBe(100)
  })

  it("零 prompt_tokens 时 cached_tokens 不影响判定", () => {
    const log = makeLog({
      prompt_tokens: 0,
      prompt_tokens_details: { cached_tokens: 500 },
    })
    expect(cacheRate(log)).toBeNull()
  })
})

describe("visibleColumns", () => {
  it("空列表返回默认可见列（always + defaultVisible）", () => {
    const cols = visibleColumns([])
    const ids = cols.map((c) => c.id)
    // 必须包含 always 列
    expect(ids).toContain("select")
    expect(ids).toContain("id")
    expect(ids).toContain("actions")
    // 必须包含 defaultVisible 列
    expect(ids).toContain("time")
    expect(ids).toContain("request_name")
    // 默认不包含候选列
    expect(ids).not.toContain("trace_id")
    expect(ids).not.toContain("cost")
  })

  it("勾选列可见，未勾选的 defaultVisible 列隐藏", () => {
    const cols = visibleColumns(["time", "tokens", "trace_id"])
    const ids = cols.map((c) => c.id)
    // always 列始终在
    expect(ids).toContain("select")
    expect(ids).toContain("id")
    expect(ids).toContain("actions")
    // 勾选的列在
    expect(ids).toContain("time")
    expect(ids).toContain("tokens")
    expect(ids).toContain("trace_id")
    // 未勾选的 defaultVisible 列不在
    expect(ids).not.toContain("request_name")
    expect(ids).not.toContain("status")
  })

  it("顺序与注册表一致", () => {
    const cols = visibleColumns([])
    for (let i = 0; i < cols.length - 1; i++) {
      const a = LOG_COLUMNS.findIndex((c) => c.id === cols[i].id)
      const b = LOG_COLUMNS.findIndex((c) => c.id === cols[i + 1].id)
      expect(a).toBeLessThan(b)
    }
  })

  it("全部勾选时所有列都可见", () => {
    const cols = visibleColumns([...LOG_TOGGLEABLE_IDS])
    const ids = cols.map((c) => c.id)
    expect(ids).toContain("trace_id")
    expect(ids).toContain("cost")
    expect(ids).toContain("bridge_notes")
    expect(cols.length).toBe(LOG_COLUMNS.length)
  })
})

describe("column value functions", () => {
  it("每列的 value 函数都能跑通（正常数据）", () => {
    const log = makeLog({
      CreatedAt: "2024-06-15T10:30:45Z",
      Name: "gpt-4",
      key_name: "my-key",
      TraceID: "trace-abc",
      SessionID: "sess-123",
      Size: 2048,
      ChunkTime: 500_000_000,
      FirstChunkTime: 100_000_000,
      ProxyTime: 200_000_000,
      Tps: 42.5,
      Retry: 2,
      RemoteIP: "192.168.1.1",
      UserAgent: "Mozilla/5.0",
      upstream_style: "openai",
      bridge_notes: "dropped_seed",
      prompt_tokens: 1000,
      completion_tokens: 500,
      total_tokens: 1500,
      prompt_tokens_details: { cached_tokens: 200 },
      input_price: 3,
      cache_read_price: 0.3,
      output_price: 15,
      currency: "USD",
    })
    for (const col of LOG_COLUMNS) {
      if (col.value) {
        const fn = col.value
        expect(() => fn(log)).not.toThrow()
      }
    }
  })

  it("project 列空 key_name 时给短横线", () => {
    const projectCol = LOG_COLUMNS.find((c) => c.id === "project")!
    expect(projectCol.value!(makeLog({ key_name: "" }))).toBe("-")
  })

  it("size 列零时给短横线", () => {
    const sizeCol = LOG_COLUMNS.find((c) => c.id === "size")!
    expect(sizeCol.value!(makeLog({ Size: 0 }))).toBe("-")
  })

  it("size 列非零时给字节格式", () => {
    const sizeCol = LOG_COLUMNS.find((c) => c.id === "size")!
    expect(sizeCol.value!(makeLog({ Size: 2048 }))).toContain("KB")
  })

  it("trace_id 列空时给短横线", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "trace_id")!
    expect(col.value!(makeLog({ TraceID: "" }))).toBe("-")
  })

  it("session_id 列空时给短横线", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "session_id")!
    expect(col.value!(makeLog({ SessionID: "" }))).toBe("-")
  })

  it("retry 列零时给短横线", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "retry")!
    expect(col.value!(makeLog({ Retry: 0 }))).toBe("-")
  })

  it("retry 列非零时给数字", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "retry")!
    expect(col.value!(makeLog({ Retry: 3 }))).toBe("3")
  })

  it("remote_ip 列空时给短横线", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "remote_ip")!
    expect(col.value!(makeLog({ RemoteIP: "" }))).toBe("-")
  })

  it("user_agent 列空时给短横线", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "user_agent")!
    expect(col.value!(makeLog({ UserAgent: "" }))).toBe("-")
  })

  it("upstream_style 列空时给短横线", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "upstream_style")!
    expect(col.value!(makeLog({ upstream_style: "" }))).toBe("-")
  })

  it("bridge_notes 列空时给短横线", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "bridge_notes")!
    expect(col.value!(makeLog({ bridge_notes: "" }))).toBe("-")
  })

  it("cost 列用 formatCost 格式化", () => {
    const col = LOG_COLUMNS.find((c) => c.id === "cost")!
    const result = col.value!(makeLog({
      prompt_tokens: 1_000_000,
      completion_tokens: 0,
      input_price: 3,
      currency: "USD",
    }))
    expect(result).toContain("$")
  })
})

describe("column registry", () => {
  it("每列有唯一 id", () => {
    const ids = LOG_COLUMNS.map((c) => c.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it("LOG_COLUMN_IDS 包含所有列", () => {
    expect(LOG_COLUMN_IDS.length).toBe(LOG_COLUMNS.length)
    for (const c of LOG_COLUMNS) {
      expect(LOG_COLUMN_IDS).toContain(c.id)
    }
  })

  it("LOG_TOGGLEABLE_IDS 不含 always 列", () => {
    for (const id of LOG_TOGGLEABLE_IDS) {
      const col = LOG_COLUMNS.find((c) => c.id === id)
      expect(col?.always).toBeFalsy()
    }
  })

  it("LOG_DEFAULT_VISIBLE_IDS 包含 always 与 defaultVisible 列", () => {
    for (const c of LOG_COLUMNS) {
      if (c.always || c.defaultVisible) {
        expect(LOG_DEFAULT_VISIBLE_IDS).toContain(c.id)
      }
    }
  })

  it("always 列不可隐藏（visibleColumns 总包含它们）", () => {
    const alwaysIds = LOG_COLUMNS.filter((c) => c.always).map((c) => c.id)
    for (const id of alwaysIds) {
      // 即使勾选列表里不包含它，它也应该在 visibleColumns 里
      const cols = visibleColumns(["time"])
      expect(cols.map((c) => c.id)).toContain(id)
    }
  })

  it("所有可切换列都有 value 或 kind", () => {
    for (const c of LOG_COLUMNS.filter((c) => !c.always)) {
      if (c.id === "select" || c.id === "actions") continue
      // 正常列要么有 value 要么有 kind
      expect(c.value || c.kind).toBeTruthy()
    }
  })

  it("候选列有默认隐藏标记", () => {
    const candidates = ["trace_id", "session_id", "tps", "cost", "retry", "remote_ip", "user_agent", "upstream_style", "bridge_notes"]
    for (const id of candidates) {
      const col = LOG_COLUMNS.find((c) => c.id === id)
      expect(col).toBeDefined()
      expect(col!.defaultVisible).toBeFalsy()
    }
  })
})
