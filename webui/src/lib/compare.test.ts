import { describe, expect, it } from "vitest"

import {
  cacheHitRate,
  commonPrefixEntries,
  describePrefix,
  divergenceOf,
  extractStructure,
  toEntries,
} from "@/lib/compare"

const parse = (o: unknown) => JSON.stringify(o)

describe("extractStructure · tools", () => {
  it("解析 Anthropic 风格的工具（name/input_schema 直接挂在顶层）", () => {
    const s = extractStructure(
      parse({
        tools: [
          { name: "get_weather", description: "查天气", input_schema: { type: "object" } },
          { name: "search", description: "搜索", input_schema: { type: "object" } },
        ],
      })
    )
    expect(s.tools.map((t) => t.label)).toEqual(["get_weather", "search"])
    expect(s.tools[0].kind).toBe("tool")
  })

  it("解析 OpenAI 风格的工具（包一层 function）", () => {
    const s = extractStructure(
      parse({
        tools: [{ type: "function", function: { name: "list_files", description: "列文件" } }],
      })
    )
    expect(s.tools.map((t) => t.label)).toEqual(["list_files"])
  })

  it("展开 Gemini 的 functionDeclarations 为独立条目", () => {
    // 合并成一个条目会掩盖"第 2 个工具变了"
    const s = extractStructure(
      parse({
        tools: [
          { functionDeclarations: [{ name: "a" }, { name: "b" }] },
        ],
      })
    )
    expect(s.tools.map((t) => t.label)).toEqual(["a", "b"])
  })

  it("尊重工具数上限", () => {
    const tools = Array.from({ length: 10 }, (_, i) => ({ name: `t${i}` }))
    expect(extractStructure(parse({ tools })).tools).toHaveLength(10)
  })

  it("无 tools 时为空数组", () => {
    expect(extractStructure(parse({ messages: [] })).tools).toEqual([])
    expect(extractStructure(parse({ tools: "not an array" })).tools).toEqual([])
  })

  it("跳过非对象的工具项", () => {
    const s = extractStructure(parse({ tools: ["bad", { name: "ok" }, 42] }))
    expect(s.tools.map((t) => t.label)).toEqual(["ok"])
  })

  it("规范化会消除缩进噪音但不重排键", () => {
    // 缩进不同不应算作差异；键序不同应当算作差异（缓存确实会因此未命中）
    const a = extractStructure('{\n  "tools": [ { "name": "x", "b": 1, "a": 2 } ]\n}')
    const b = extractStructure('{"tools":[{"name":"x","b":1,"a":2}]}')
    expect(a.tools[0].text).toBe(b.tools[0].text)

    const c = extractStructure('{"tools":[{"a":2,"b":1,"name":"x"}]}')
    expect(c.tools[0].text).not.toBe(a.tools[0].text)
  })
})

describe("extractStructure · system", () => {
  it("字符串 system", () => {
    expect(extractStructure(parse({ system: "你是助手" })).system?.text).toBe("system\u0000你是助手")
  })

  it("块数组 system", () => {
    const s = extractStructure(parse({ system: [{ type: "text", text: "规则" }] }))
    expect(s.system?.text).toBe("system\u0000规则")
  })

  it("Gemini 的 systemInstruction.parts", () => {
    const s = extractStructure(parse({ systemInstruction: { parts: [{ text: "指令" }] } }))
    expect(s.system?.text).toBe("system\u0000指令")
  })

  it("OpenAI-Responses 的 instructions 作为回落", () => {
    const s = extractStructure(parse({ instructions: "只回答是或否" }))
    expect(s.system?.text).toBe("system\u0000只回答是或否")
  })

  it("system 优先于 instructions", () => {
    const s = extractStructure(parse({ system: "A", instructions: "B" }))
    expect(s.system?.text).toBe("system\u0000A")
  })

  it("空白 system 视为不存在", () => {
    expect(extractStructure(parse({ system: "   " })).system).toBeNull()
    expect(extractStructure(parse({ system: [] })).system).toBeNull()
    expect(extractStructure(parse({ systemInstruction: { parts: [] } })).system).toBeNull()
  })
})

describe("extractStructure · messages", () => {
  it("解析 OpenAI 风格 messages", () => {
    const s = extractStructure(
      parse({ messages: [
        { role: "system", content: "S" },
        { role: "user", content: "U" },
      ] })
    )
    expect(s.messages.map((m) => m.label)).toEqual(["system", "user"])
    expect(s.messages[0].text).toBe("system\u0000S")
  })

  it("解析 Gemini 的 contents/parts", () => {
    const s = extractStructure(parse({ contents: [{ role: "user", parts: [{ text: "你好" }] }] }))
    expect(s.messages.map((m) => m.text)).toEqual(["user\u0000你好"])
  })

  it("非文本块保留类型标记", () => {
    const s = extractStructure(
      parse({ messages: [{ role: "user", content: [{ type: "image" }, { type: "text", text: "看图" }] }] })
    )
    expect(s.messages[0].text).toBe("user\u0000<image>\n看图")
  })

  it("缺 role 记为 unknown", () => {
    expect(extractStructure(parse({ messages: [{ content: "x" }] })).messages[0].label).toBe("unknown")
  })

  it("content 为对象时序列化", () => {
    const s = extractStructure(parse({ messages: [{ role: "user", content: { a: 1 } }] }))
    expect(s.messages[0].text).toBe('user\u0000{"a":1}')
  })

  it("尊重消息数上限", () => {
    const messages = Array.from({ length: 20 }, (_, i) => ({ role: "user", content: `m${i}` }))
    expect(extractStructure(parse({ messages }), 5).messages).toHaveLength(5)
  })
})

describe("extractStructure · 容错", () => {
  it("非法 JSON 返回空结构", () => {
    expect(extractStructure("{not json")).toEqual({ tools: [], system: null, messages: [] })
  })

  it("非对象体返回空结构", () => {
    expect(extractStructure(parse([1, 2, 3]))).toEqual({ tools: [], system: null, messages: [] })
    expect(extractStructure(parse("str"))).toEqual({ tools: [], system: null, messages: [] })
    expect(extractStructure(parse(42))).toEqual({ tools: [], system: null, messages: [] })
  })

  it("空对象得到空结构", () => {
    expect(extractStructure(parse({}))).toEqual({ tools: [], system: null, messages: [] })
  })
})

describe("toEntries · 缓存前缀顺序", () => {
  it("顺序必须是 tools → system → messages", () => {
    // 这是缓存的实际渲染顺序。顺序写错会让前缀长度完全失真。
    const s = extractStructure(
      parse({
        tools: [{ name: "t1" }, { name: "t2" }],
        system: "S",
        messages: [{ role: "user", content: "U" }],
      })
    )
    expect(toEntries(s).map((e) => e.kind)).toEqual(["tool", "tool", "system", "message"])
    expect(toEntries(s).map((e) => e.label)).toEqual(["t1", "t2", "system", "user"])
  })

  it("无 system 时不留空位", () => {
    const s = extractStructure(parse({ tools: [{ name: "t" }], messages: [{ role: "user", content: "U" }] }))
    expect(toEntries(s).map((e) => e.kind)).toEqual(["tool", "message"])
  })

  it("空结构得到空序列", () => {
    expect(toEntries(extractStructure(parse({})))).toEqual([])
  })
})

describe("commonPrefixEntries", () => {
  const withTools = (toolNames: string[], sys: string, msgs: string[]) =>
    extractStructure(
      parse({
        tools: toolNames.map((name) => ({ name })),
        system: sys,
        messages: msgs.map((c) => ({ role: "user", content: c })),
      })
    )

  it("**工具相同、消息不同时，前缀包含工具**", () => {
    // 这是本文件存在的核心理由：只比较 messages 会报 0，而实际
    // tools + system 那一段是命中的。
    const a = withTools(["t1", "t2"], "S", ["A"])
    const b = withTools(["t1", "t2"], "S", ["B"])
    expect(commonPrefixEntries([a, b])).toBe(3) // 2 tools + system
  })

  it("工具不同则前缀止于第一个工具", () => {
    const a = withTools(["t1", "t2"], "S", ["A"])
    const b = withTools(["t9", "t2"], "S", ["A"])
    expect(commonPrefixEntries([a, b])).toBe(0)
  })

  it("工具数不同时前缀止于较短者之后", () => {
    const a = withTools(["t1", "t2"], "S", ["A"])
    const b = withTools(["t1"], "S", ["A"])
    expect(commonPrefixEntries([a, b])).toBe(1)
  })

  it("system 不同则前缀止于 tools 之后", () => {
    const a = withTools(["t1"], "S1", ["A"])
    const b = withTools(["t1"], "S2", ["A"])
    expect(commonPrefixEntries([a, b])).toBe(1)
  })

  it("三方对比取所有请求的公共部分", () => {
    const a = withTools(["t1"], "S", ["A"])
    const b = withTools(["t1"], "S", ["B"])
    const c = withTools(["t1"], "S", ["A"])
    // 前 2 项（tool + system）三者一致；消息处 A/B 不同
    expect(commonPrefixEntries([a, b, c])).toBe(2)
  })

  it("角色不同视为不同消息", () => {
    const a = extractStructure(parse({ messages: [{ role: "user", content: "x" }] }))
    const b = extractStructure(parse({ messages: [{ role: "assistant", content: "x" }] }))
    expect(commonPrefixEntries([a, b])).toBe(0)
  })

  it("忽略空结构（它不限制前缀）", () => {
    const a = withTools(["t1"], "S", ["A"])
    const empty = extractStructure(parse({}))
    expect(commonPrefixEntries([a, empty])).toBe(3)
  })

  it("全为空则 0", () => {
    expect(commonPrefixEntries([extractStructure(parse({})), extractStructure(parse({}))])).toBe(0)
    expect(commonPrefixEntries([])).toBe(0)
  })

  it("单个结构取其全长", () => {
    expect(commonPrefixEntries([withTools(["t1"], "S", ["A"])])).toBe(3)
  })

  it("完全相同则取最短者长度", () => {
    const a = withTools(["t1"], "S", ["A"])
    const b = withTools(["t1"], "S", ["A", "B", "C"])
    expect(commonPrefixEntries([a, b])).toBe(3)
  })
})

describe("divergenceOf", () => {
  const s = extractStructure(
    parse({
      tools: [{ name: "t1" }],
      system: "S",
      messages: [{ role: "user", content: "A" }, { role: "user", content: "B" }],
    })
  )

  it("报告分叉处是第几个条目及其内容", () => {
    const d = divergenceOf(s, 2)
    expect(d.prefixLen).toBe(2)
    expect(d.divergesAt).toBe(2)
    expect(d.entry?.kind).toBe("message")
    expect(d.entry?.label).toBe("user")
    expect(d.total).toBe(4)
  })

  it("完全包含公共前缀时无分叉", () => {
    const d = divergenceOf(s, 4)
    expect(d.divergesAt).toBeNull()
    expect(d.entry).toBeNull()
    expect(d.divergesAt).toBeNull()
  })

  it("prefixLen 为 0 时从第一个条目起分叉", () => {
    const d = divergenceOf(s, 0)
    expect(d.divergesAt).toBe(0)
    expect(d.entry?.kind).toBe("tool")
  })

  it("空结构不报分叉", () => {
    const d = divergenceOf(extractStructure(parse({})), 3)
    expect(d.divergesAt).toBeNull()
    expect(d.total).toBe(0)
  })
})

describe("describePrefix", () => {
  it("按 kind 归纳前三段的构成", () => {
    const s = extractStructure(
      parse({
        tools: [{ name: "t1" }, { name: "t2" }],
        system: "S",
        messages: [{ role: "user", content: "U" }],
      })
    )
    expect(describePrefix(toEntries(s).slice(0, 4))).toEqual({ tools: 2, system: 1, messages: 1 })
  })

  it("空序列全为 0", () => {
    expect(describePrefix([])).toEqual({ tools: 0, system: 0, messages: 0 })
  })
})

describe("cacheHitRate", () => {
  it("正常计算", () => {
    expect(cacheHitRate(1000, 250)).toBe(25)
  })

  it("输入为 0 时返回 null 而非 0", () => {
    expect(cacheHitRate(0, 0)).toBeNull()
    expect(cacheHitRate(-1, 5)).toBeNull()
  })

  it("命中超过输入（数据异常）时如实返回", () => {
    expect(cacheHitRate(100, 150)).toBe(150)
  })

  it("非法值返回 null", () => {
    expect(cacheHitRate(Number.NaN, 1)).toBeNull()
  })
})
