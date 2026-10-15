import { describe, expect, it } from "vitest"

import { anthropicNonStreamDeltas, anthropicParseRequest, anthropicToDeltas } from "./anthropic"
import { fixtures } from "./fixtures"

describe("anthropicToDeltas", () => {
  it("没有 type 的事件给空", () => {
    expect(anthropicToDeltas({})).toEqual([])
  })

  it("文本与思考增量分别成桶", () => {
    expect(anthropicToDeltas({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "结果" } })).toEqual([
      { kind: "text", text: "结果" },
    ])
    expect(anthropicToDeltas({ type: "content_block_delta", index: 0, delta: { type: "thinking_delta", thinking: "想想" } })).toEqual([
      { kind: "thinking", text: "想想" },
    ])
    expect(anthropicToDeltas({ type: "content_block_delta", index: 0, delta: { type: "thinking_delta", thinking: "" } })).toEqual([
      { kind: "thinking", text: "" },
    ])
  })

  it("参数增量按 index 累积", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", index: 1, delta: { type: "input_json_delta", partial_json: "{\"cit" } })
    expect(out).toEqual([{ kind: "tool_call", index: 1, id: "", name: "", arguments: "{\"cit", mimeType: undefined }])
  })

  it("index 不是数字时不算下标", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", index: "1", delta: { type: "input_json_delta", partial_json: "x" } })
    expect(out[0].index).toBe(undefined)
  })

  it("signature_delta 原样落 unknown，不把签名混进正文", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", delta: { type: "signature_delta", signature: "SIG" } })
    expect(out).toEqual([{ kind: "unknown", data: { signature: "SIG" }, text: "SIG" }])
  })

  it("不认识的 delta 类型落 unknown", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", delta: { type: "other_delta", text: "x" } })
    expect(out).toEqual([{ kind: "unknown", data: { type: "other_delta", text: "x" }, text: "x" }])
  })

  it("不认识的 delta 类型没有 text 时 text 为 undefined", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", delta: { type: "other_delta" } })
    expect(out).toEqual([{ kind: "unknown", data: { type: "other_delta" }, text: undefined }])
  })

  it("text_delta 没有 text 时给空串", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", delta: { type: "text_delta" } })
    expect(out).toEqual([{ kind: "text", text: "" }])
  })

  it("input_json_delta 没有 partial_json 时给 undefined", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", delta: { type: "input_json_delta" } })
    expect(out).toEqual([{ kind: "tool_call", index: undefined, id: "", name: "", arguments: undefined }])
  })

  it("signature_delta 没有 signature 时 text 为 undefined", () => {
    const out = anthropicToDeltas({ type: "content_block_delta", delta: { type: "signature_delta" } })
    expect(out).toEqual([{ kind: "unknown", data: { signature: undefined }, text: undefined }])
  })

  it("空 delta 给空数组", () => {
    expect(anthropicToDeltas({ type: "content_block_delta" })).toEqual([])
  })

  it("message_delta 带结束原因与用量", () => {
    const out = anthropicToDeltas({ type: "message_delta", delta: { stop_reason: "tool_use" }, usage: { output_tokens: 7 } })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "tool_use", usage: { output_tokens: 7 } }])
  })

  it("message_delta 只有 usage 也发一个收尾事件", () => {
    const out = anthropicToDeltas({ type: "message_delta", delta: null, usage: { output_tokens: 1 } })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "", usage: { output_tokens: 1 } }])
  })

  it("message_start / ping / content_block_stop 都落 unknown，不丢", () => {
    expect(anthropicToDeltas({ type: "message_start", message: { id: "m1" } })).toEqual([
      { kind: "unknown", data: { type: "message_start", message: { id: "m1" } } },
    ])
    expect(anthropicToDeltas({ type: "ping" })).toEqual([{ kind: "unknown", data: { type: "ping" } }])
    expect(anthropicToDeltas({ type: "content_block_stop", index: 0 })).toEqual([
      { kind: "unknown", data: { type: "content_block_stop", index: 0 } },
    ])
  })

  it("错误事件只保留 error 子对象", () => {
    const out = anthropicToDeltas({ type: "error", error: { type: "api_error", message: "boom" } })
    expect(out).toEqual([{ kind: "unknown", data: { type: "api_error", message: "boom" } }])
  })

  it("错误事件没有 error 字段时回退整条 chunk", () => {
    const out = anthropicToDeltas({ type: "error", message: "boom" })
    expect(out).toEqual([{ kind: "unknown", data: { type: "error", message: "boom" } }])
  })

  it("content_block_start 的 content_block 不是对象时给空", () => {
    const out = anthropicToDeltas({ type: "content_block_start", content_block: "bad" })
    expect(out).toEqual([])
  })

  it("content_block_start 的 tool_use 没有 id 和 name 时给空值", () => {
    const out = anthropicToDeltas({ type: "content_block_start", index: 0, content_block: { type: "tool_use" } })
    expect(out).toEqual([{ kind: "tool_call", index: 0, id: "", name: "", arguments: undefined }])
  })

  it("content_block_start 的 index 不是数字时不算下标", () => {
    const out = anthropicToDeltas({ type: "content_block_start", index: "bad", content_block: { type: "tool_use", id: "x", name: "f" } })
    expect(out[0].index).toBe(undefined)
  })

  it("message_delta 只有 finish 没有 usage 时 usage 为 undefined", () => {
    const out = anthropicToDeltas({ type: "message_delta", delta: { stop_reason: "stop" } })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "stop", usage: undefined }])
  })

  it("message_delta 既没有 finish 也没有 usage 时给空", () => {
    const out = anthropicToDeltas({ type: "message_delta" })
    expect(out).toEqual([])
  })

  it("message_delta 的 usage 不是对象时不算", () => {
    const out = anthropicToDeltas({ type: "message_delta", delta: { stop_reason: "stop" }, usage: "bad" })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "stop", usage: undefined }])
  })

  it("整条流式响应按事件顺序拼装", () => {
    const chunks = fixtures.anthropicStream.OfStringArray!.map((c) => JSON.parse(c))
    const deltas = chunks.flatMap(anthropicToDeltas)
    expect(deltas.filter((d) => d.kind === "text").map((d) => d.text)).toEqual(["结果", " 多云"])
    expect(deltas.filter((d) => d.kind === "thinking").map((d) => d.text)).toEqual(["让我", "想想"])
    expect(deltas.filter((d) => d.kind === "tool_call").map((d) => [d.index, d.id, d.name, d.arguments])).toEqual([
      [1, "tu_9", "get_weather", undefined],
      [1, "", "", "{\"cit"],
      [1, "", "", "y\":\"上海\"}"],
    ])
    expect(deltas.filter((d) => d.kind === "unknown")).toHaveLength(6)
  })
})

describe("anthropicNonStreamDeltas", () => {
  it("非对象给空", () => {
    expect(anthropicNonStreamDeltas(null)).toEqual([])
    expect(anthropicNonStreamDeltas("x" as unknown as Record<string, unknown>)).toEqual([])
  })

  it("非流式的整包 content 展开成增量，收尾事件最后", () => {
    const out = anthropicNonStreamDeltas(JSON.parse(fixtures.anthropicNonStream.OfString!))
    expect(out.map((d) => d.kind)).toEqual(["thinking", "thinking", "text", "tool_call", "unknown", "unknown"])
    expect(out.map((d) => d.text)).toEqual(["非流式思考", "RED2", "非流式正文", undefined, undefined, ""])
    expect(out[3]).toMatchObject({ id: "tu_2", name: "get_weather", arguments: "{\"city\":\"北京\"}" })
    expect(out[out.length - 1]).toMatchObject({ finish: "tool_use", usage: { input_tokens: 2, output_tokens: 8 } })
  })

  it("只有 usage 没有 stop_reason 时仍发收尾事件", () => {
    const out = anthropicNonStreamDeltas({ content: [], usage: { input_tokens: 1 } })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "", usage: { input_tokens: 1 } }])
  })

  it("空 content 且无 usage 时不产生任何 delta", () => {
    expect(anthropicNonStreamDeltas({ content: [] })).toEqual([])
  })
})

describe("anthropicParseRequest", () => {
  const body = JSON.parse(fixtures.anthropicRequest.Input!)

  it("system 数组拍平成字符串，模型名与参数各自归位", () => {
    const req = anthropicParseRequest(body)
    expect(req.protocol).toBe("anthropic")
    expect(req.model).toBe("claude-sonnet-4-20250514")
    expect(req.system).toBe("系统第一段\n\n系统第二段")
    expect(req.params).toEqual({ thinking: { type: "enabled", budget_tokens: 1024 }, max_tokens: 1000 })
    expect(req.tools).toEqual([{ name: "get_weather", description: "查天气", parameters: { type: "object", properties: { city: { type: "string" } } } }])
  })

  it("system 是普通对象时按 parts 拍平", () => {
    expect(anthropicParseRequest({ system: { parts: [{ text: "A" }, { v: 1 }] } }).system).toBe("A\n\n{\"v\":1}")
  })

  it("system 是对象但没有 parts 时回退到 text 或原对象", () => {
    expect(anthropicParseRequest({ system: { text: "只有文本" } }).system).toBe("只有文本")
    expect(anthropicParseRequest({ system: { odd: true } }).system).toEqual({ odd: true })
  })

  it("字符串 content 与块 content 都能解析", () => {
    const req = anthropicParseRequest(body)
    expect(req.messages[0]).toEqual({ role: "user", parts: [{ type: "text", text: "你好" }], toolCalls: [] })
  })

  it("thinking / redacted_thinking / text / tool_use / image 各自成 part", () => {
    const assistant = anthropicParseRequest(body).messages.find((m) => m.role === "assistant")!
    expect(assistant.parts.map((p) => p.type)).toEqual(["thinking", "thinking", "text", "tool_use", "image", "unknown"])
    expect(assistant.parts[0]).toMatchObject({ type: "thinking", text: "先想一下" })
    expect(assistant.parts[1]).toMatchObject({ type: "thinking", text: "REDACTED" })
    expect(assistant.parts[3]).toMatchObject({ type: "tool_use", call: { id: "tu_1", name: "get_weather", arguments: { city: "上海" } } })
    expect(assistant.parts[4]).toMatchObject({ type: "image", mimeType: "image/png", data: "BASE64" })
    expect(assistant.parts[5]).toMatchObject({ type: "unknown", name: "server_tool_use" })
    expect(assistant.toolCalls).toHaveLength(1)
  })

  it("tool_result 的混合格式 content 原样保留，回挂名留空", () => {
    const req = anthropicParseRequest(body)
    const withToolResult = req.messages.find((m) => m.parts.some((p) => p.type === "tool_result"))!
    expect(withToolResult.parts[0]).toMatchObject({ type: "tool_result", callId: "tu_1", toolCall: undefined })
  })

  it("缺 role 归为 unknown，content 为空给空 part 列表", () => {
    const req = anthropicParseRequest({ messages: [{ content: "" }] })
    expect(req.messages).toEqual([{ role: "unknown", parts: [], toolCalls: [] }])
  })

  it("tool_use 的 input 是坏 JSON 时打 parseError", () => {
    const req = anthropicParseRequest({ messages: [{ role: "assistant", content: [{ type: "tool_use", id: "t", name: "f", input: "{坏" }] }] })
    expect((req.messages[0].parts[0] as { call: { parseError?: boolean } }).call.parseError).toBe(true)
  })

  it("不认识的块类型落 unknown", () => {
    const req = anthropicParseRequest({ messages: [{ role: "assistant", content: [{ type: "mystery_block" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "unknown", data: { type: "mystery_block" }, name: "mystery_block" }])
  })

  it("没有 type 的块给空", () => {
    const req = anthropicParseRequest({ messages: [{ role: "assistant", content: [{ text: "x" }] }] })
    expect(req.messages[0].parts).toEqual([])
  })

  it("text 块没有 text 时给空串", () => {
    const req = anthropicParseRequest({ messages: [{ role: "user", content: [{ type: "text" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "text", text: "" }])
  })

  it("thinking 块没有 thinking 时回退到 data", () => {
    const req = anthropicParseRequest({ messages: [{ role: "assistant", content: [{ type: "thinking", data: "from data" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "thinking", text: "from data" }])
  })

  it("thinking 块既没有 thinking 也没有 data 时给空串", () => {
    const req = anthropicParseRequest({ messages: [{ role: "assistant", content: [{ type: "thinking" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "thinking", text: "" }])
  })

  it("tool_use 块没有 id 时 call.id 为 undefined", () => {
    const req = anthropicParseRequest({ messages: [{ role: "assistant", content: [{ type: "tool_use", name: "f", input: {} }] }] })
    expect((req.messages[0].parts[0] as { call: { id?: string } }).call).toEqual({ id: undefined, name: "f", arguments: {} })
  })

  it("tool_use 块没有 name 时给空名", () => {
    const req = anthropicParseRequest({ messages: [{ role: "assistant", content: [{ type: "tool_use", id: "t", input: {} }] }] })
    expect((req.messages[0].parts[0] as { call: { name: string } }).call.name).toBe("")
  })

  it("tool_result 块没有 tool_use_id 时给空 callId", () => {
    const req = anthropicParseRequest({ messages: [{ role: "user", content: [{ type: "tool_result" }] }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", callId: "" })
  })

  it("image 块没有 source 对象时 mimeType 与 data 为 undefined", () => {
    const req = anthropicParseRequest({ messages: [{ role: "user", content: [{ type: "image", source: null }] }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "image", mimeType: undefined, data: undefined })
  })

  it("tool_result 的 content 是数组时拍平成字符串", () => {
    const req = anthropicParseRequest({ messages: [{ role: "user", content: [{ type: "tool_result", tool_use_id: "t1", content: [{ type: "text", text: "结果" }, "纯文本"] }] }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", callId: "t1", content: [{ type: "text", text: "结果" }, "纯文本"] })
  })

  it("tool_result 的 content 是对象时原样保留", () => {
    const req = anthropicParseRequest({ messages: [{ role: "user", content: [{ type: "tool_result", tool_use_id: "t2", content: { error: "fail" } }] }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", callId: "t2", content: { error: "fail" } })
  })

  it("tool_result 的 content 是 undefined 时给空", () => {
    const req = anthropicParseRequest({ messages: [{ role: "user", content: [{ type: "tool_result", tool_use_id: "t3" }] }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", callId: "t3", content: undefined })
  })

  it("image 块落成 unknown 并带 mimeType 与 data", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "image", source: { media_type: "image/png", data: "B64" } }] })
    expect(out[0]).toMatchObject({ kind: "unknown", name: "image", mimeType: "image/png", text: "B64" })
  })

  it("image 块没有 source 对象时 mimeType 与 text 为 undefined", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "image", source: null }] })
    expect(out[0]).toMatchObject({ kind: "unknown", name: "image", mimeType: undefined, text: undefined })
  })

  it("没有 type 的块给空", () => {
    const out = anthropicNonStreamDeltas({ content: [{ text: "x" }] })
    expect(out).toEqual([])
  })

  it("text 块没有 text 时给空串", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "text" }] })
    expect(out).toEqual([{ kind: "text", text: "" }])
  })

  it("thinking 块没有 thinking 字段时回退到 data", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "thinking", data: "from data" }] })
    expect(out).toEqual([{ kind: "thinking", text: "from data" }])
  })

  it("thinking 块既没有 thinking 也没有 data 时给空串", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "thinking" }] })
    expect(out).toEqual([{ kind: "thinking", text: "" }])
  })

  it("tool_use 块没有 id 和 name 时给空值", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "tool_use" }] })
    expect(out[0]).toMatchObject({ kind: "tool_call", id: "", name: "", arguments: undefined })
  })

  it("tool_result 块没有 tool_use_id 时给空 callId", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "tool_result" }] })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "", data: undefined, text: undefined })
  })

  it("tool_result 的 content 数组里有对象没有 text 时用 JSON 兜底", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "tool_result", tool_use_id: "t", content: [{ v: 1 }] }] })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "t", text: '{"v":1}' })
  })

  it("tool_result 的 content 是数组时 textOf 拍平", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "tool_result", tool_use_id: "t1", content: [{ type: "text", text: "结果" }, "纯文本"] }] })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "t1", text: "结果\n纯文本" })
  })

  it("tool_result 的 content 是对象时 textOf 原样保留", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "tool_result", tool_use_id: "t2", content: { error: "fail" } }] })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "t2", text: "{\"error\":\"fail\"}" })
  })

  it("tool_result 的 content 是 undefined 时 textOf 给 undefined", () => {
    const out = anthropicNonStreamDeltas({ content: [{ type: "tool_result", tool_use_id: "t3" }] })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "t3", text: undefined })
  })
})
