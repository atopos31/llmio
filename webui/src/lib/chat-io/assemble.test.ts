import { describe, expect, it } from "vitest"

import { applyChunk, attachToolResultNames, emptyAssembled } from "./assemble"
import type { ContentPart, Delta } from "./types"

type Msg = { parts: ContentPart[] }

const d = (patch: Partial<Delta> & { kind: Delta["kind"] }): Delta => patch

describe("applyChunk", () => {
  it("空装配器的初始形状", () => {
    expect(emptyAssembled()).toEqual({ text: "", thinking: "", toolCalls: [], toolResults: [], unknown: [] })
  })

  it("text 与 thinking 各自累加，互不占桶", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "text", text: "你好" }))
    applyChunk(out, d({ kind: "text", text: "世界" }))
    applyChunk(out, d({ kind: "thinking", text: "先想想" }))
    applyChunk(out, d({ kind: "thinking", text: "再想想" }))
    applyChunk(out, d({ kind: "text" }))
    expect(out.text).toBe("你好世界")
    expect(out.thinking).toBe("先想想再想想")
  })

  it("流式思考的分片拼成一段，不是每片一项", () => {
    // Anthropic 的思考是几十上百个 thinking_delta；逐 delta 存一项就是
    // "按 SSE 展示"——界面上会裂成几百张各写一两个字的卡片。
    const out = emptyAssembled()
    for (const frag of ["让", "我", "想想", "。"]) applyChunk(out, d({ kind: "thinking", text: frag }))
    expect(out.thinking).toBe("让我想想。")
  })

  it("空片不计入：空串接上去不改变结果", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "thinking", text: "" }))
    applyChunk(out, d({ kind: "text", text: "" }))
    expect(out.thinking).toBe("")
    expect(out.text).toBe("")
  })

  it("tool_call 按 id 累积 name 与 arguments 分片", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_call", id: "c1", name: "get_", arguments: "{\"cit" }))
    applyChunk(out, d({ kind: "tool_call", id: "c1", name: "weather", arguments: "y\":\"上海\"}" }))
    expect(out.toolCalls).toEqual([{ id: "c1", name: "get_weather", arguments: "{\"city\":\"上海\"}" }])
  })

  it("没有 id 时退回 index 累积", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_call", index: 0, name: "a", arguments: "1" }))
    applyChunk(out, d({ kind: "tool_call", index: 0, name: "b", arguments: "2" }))
    applyChunk(out, d({ kind: "tool_call", index: 1, name: "c", arguments: "3" }))
    expect(out.toolCalls.map((c) => `${c.name}:${c.arguments}:${c.index}`)).toEqual(["ab:12:0", "c:3:1"])
  })

  it("id 与 index 都没有时每次新建条目", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_call", name: "x", arguments: "1" }))
    applyChunk(out, d({ kind: "tool_call", name: "y", arguments: "2" }))
    expect(out.toolCalls.map((c) => c.name)).toEqual(["x", "y"])
  })

  it("非字符串参数整体替换，不被字符串拼接拆开", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_call", id: "c", name: "f", arguments: "{\"a\":1}" }))
    applyChunk(out, d({ kind: "tool_call", id: "c", name: "", arguments: { replaced: true } }))
    applyChunk(out, d({ kind: "tool_call", id: "c", name: "", arguments: null }))
    expect(out.toolCalls).toEqual([{ id: "c", name: "f", arguments: { replaced: true } }])
  })

  it("tool_result 单独成桶并带上调用名", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_result", callId: "c1", data: { temp: 31 }, name: "get_weather" }))
    expect(out.toolResults).toEqual([{ type: "tool_result", callId: "c1", content: { temp: 31 }, toolCall: "get_weather" }])
  })

  it("tool_result 没有 id 时回退到 id 字段", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_result", id: "id-only", data: "x" }))
    expect(out.toolResults[0]).toMatchObject({ callId: "id-only", content: "x", toolCall: undefined })
  })

  it("tool_result 既无 callId 也无 id 时 callId 为空串", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_result", data: "x" }))
    expect(out.toolResults[0]).toMatchObject({ callId: "", content: "x", toolCall: undefined })
  })

  it("tool_call 没有 name 时给空名", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "tool_call", id: "c", arguments: "{}" }))
    expect(out.toolCalls[0].name).toBe("")
  })

  it("unknown 事件原样保留在 unknown 桶里", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "unknown", data: { type: "ping" }, name: "ping", text: "hello", index: 2, mimeType: "image/png" }))
    expect(out.unknown).toEqual([
      { type: "unknown", data: { type: "ping" }, text: "hello", name: "ping", index: 2, mimeType: "image/png" },
    ])
  })

  it("usage 与 finish 后到覆盖，跨多个增量只留最后一个", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "unknown", text: "", usage: { total_tokens: 1 } }))
    applyChunk(out, d({ kind: "text", text: "x", finish: "stop" }))
    applyChunk(out, d({ kind: "unknown", text: "", usage: { total_tokens: 9 }, finish: "tool_calls" }))
    expect(out.usage).toEqual({ total_tokens: 9 })
    expect(out.finishReason).toBe("tool_calls")
  })

  it("finish 为空串不算有效值，保留之前的", () => {
    const out = emptyAssembled()
    applyChunk(out, d({ kind: "unknown", text: "", finish: "stop" }))
    applyChunk(out, d({ kind: "unknown", text: "", finish: "" }))
    expect(out.finishReason).toBe("stop")
  })
})

describe("attachToolResultNames", () => {
  it("tool_result 按 call id 回挂调用名", () => {
    const messages: Msg[] = [
      {
        parts: [
          { type: "tool_use", call: { id: "t1", name: "get_weather", arguments: {} } },
        ],
      },
      { parts: [{ type: "tool_result", callId: "t1", content: "31°" }] },
    ]
    attachToolResultNames(messages)
    expect(messages[1].parts[0]).toMatchObject({ type: "tool_result", toolCall: "get_weather" })
  })

  it("查不到的 id 保留 undefined，协议侧已给出的名字不被覆盖", () => {
    const messages: Msg[] = [
      { parts: [{ type: "tool_result", callId: "missing", content: "x" }] },
      { parts: [{ type: "tool_result", callId: "t2", content: "y", toolCall: "by_name" }] },
    ]
    attachToolResultNames(messages)
    expect(messages[0].parts[0]).toMatchObject({ toolCall: undefined })
    expect(messages[1].parts[0]).toMatchObject({ toolCall: "by_name" })
  })

  it("重复 id 只保留第一次出现的名字（先出现者为准）", () => {
    const messages: Msg[] = [
      { parts: [{ type: "tool_use", call: { id: "dup", name: "first", arguments: {} } }] },
      { parts: [{ type: "tool_use", call: { id: "dup", name: "second", arguments: {} } }] },
      { parts: [{ type: "tool_result", callId: "dup", content: "z" }] },
    ]
    attachToolResultNames(messages)
    expect(messages[2].parts[0]).toMatchObject({ toolCall: "first" })
  })

  it("没有 id 的 tool_use 不建表项", () => {
    const messages: Msg[] = [
      { parts: [{ type: "tool_use", call: { name: "no_id", arguments: {} } }] },
      { parts: [{ type: "tool_result", callId: "no_id", content: "n" }] },
    ]
    attachToolResultNames(messages)
    expect(messages[1].parts[0]).toMatchObject({ toolCall: undefined })
  })

  it("空消息列表不报错", () => {
    expect(() => attachToolResultNames([])).not.toThrow()
  })
})
