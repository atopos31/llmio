import { describe, expect, it } from "vitest"

import { fixtures } from "./fixtures"
import { openaiResNonStreamDeltas, openaiResParseRequest, openaiResToDeltas } from "./openai-res"

describe("openaiResToDeltas", () => {
  it("没有 type 的事件给空", () => {
    expect(openaiResToDeltas({})).toEqual([])
  })

  it("response.created 落 unknown 并带 usage", () => {
    const out = openaiResToDeltas({ type: "response.created", response: { id: "r", usage: { input_tokens: 1 } } })
    expect(out).toEqual([{ kind: "unknown", data: expect.any(Object), usage: { input_tokens: 1 } }])
  })

  it("created 没有 usage 时仍然保留事件", () => {
    const out = openaiResToDeltas({ type: "response.created", response: null })
    expect(out).toEqual([{ kind: "unknown", data: { type: "response.created", response: null }, usage: undefined }])
  })

  it("created 的 response 不是对象时 usage 为 undefined", () => {
    const out = openaiResToDeltas({ type: "response.created", response: "bad" })
    expect(out[0].usage).toBeUndefined()
  })

  it("文本与思考增量分别成桶", () => {
    expect(openaiResToDeltas({ type: "response.output_text.delta", delta: "正文" })).toEqual([{ kind: "text", text: "正文" }])
    expect(openaiResToDeltas({ type: "response.refusal.delta", delta: "拒绝" })).toEqual([{ kind: "text", text: "拒绝" }])
    expect(openaiResToDeltas({ type: "response.reasoning_summary_text.delta", delta: "思考" })).toEqual([{ kind: "thinking", text: "思考" }])
    expect(openaiResToDeltas({ type: "response.output_text.delta", delta: "" })).toEqual([])
  })

  it("函数参数增量按 item_id 累积", () => {
    expect(openaiResToDeltas({ type: "response.function_call_arguments.delta", item_id: "f1", delta: "{\"c\"" })).toEqual([
      { kind: "tool_call", id: "f1", name: "", arguments: "{\"c\"", mimeType: undefined },
    ])
  })

  it("函数参数增量没有 item_id 时给空 id", () => {
    expect(openaiResToDeltas({ type: "response.function_call_arguments.delta", delta: "x" })).toEqual([
      { kind: "tool_call", id: "", name: "", arguments: "x" },
    ])
  })

  it("完成事件只带 status 或 usage 时才发一个 unknown", () => {
    expect(openaiResToDeltas({ type: "response.completed", response: { status: "completed" } })).toEqual([
      { kind: "unknown", text: "", finish: "completed", usage: undefined },
    ])
    expect(openaiResToDeltas({ type: "response.completed", response: null })).toEqual([])
    expect(openaiResToDeltas({ type: "response.incomplete", response: { usage: { t: 1 } } })).toEqual([
      { kind: "unknown", text: "", finish: "", usage: { t: 1 } },
    ])
  })

  it("错误事件与未知事件都落 unknown", () => {
    expect(openaiResToDeltas({ type: "response.error", error: { code: "x" } })).toEqual([
      { kind: "unknown", data: { code: "x" } },
    ])
    expect(openaiResToDeltas({ type: "response.something_new", foo: 1 })).toEqual([
      { kind: "unknown", data: { type: "response.something_new", foo: 1 } },
    ])
  })

  it("整条流式响应按事件顺序拼装", () => {
    const res = fixtures.openaiResStream
    const chunks = res.OfStringArray!.map((c) => JSON.parse(c))
    const deltas = chunks.flatMap(openaiResToDeltas)
    expect(deltas.filter((d) => d.kind === "text").map((d) => d.text)).toEqual(["第一段。", "第二段。", "拒绝内容"])
    expect(deltas.filter((d) => d.kind === "thinking").map((d) => d.text)).toEqual(["思考 A", "思考 B"])
    expect(deltas.filter((d) => d.kind === "tool_call").map((d) => [d.id, d.name, d.arguments])).toEqual([
      ["fc_2", "", "{\"city\""],
      ["fc_2", "", ":\"杭州\"}"],
      ["fc_2", "get_weather", "{\"city\":\"杭州\"}"],
    ])
    expect(deltas.filter((d) => d.kind === "tool_result")).toHaveLength(1)
    expect(deltas.filter((d) => d.kind === "unknown").length).toBe(5)
  })

  it("content_part.added 的 text / output_text / input_text / refusal 各自成 text", () => {
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "text", text: "A" } })).toEqual([{ kind: "text", text: "A" }])
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "output_text", text: "B" } })).toEqual([{ kind: "text", text: "B" }])
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "input_text", text: "C" } })).toEqual([{ kind: "text", text: "C" }])
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "refusal", refusal: "D" } })).toEqual([{ kind: "text", text: "D" }])
  })

  it("content_part.added 的 input_image 落 unknown，未知类型也落 unknown", () => {
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "input_image", image_url: "x" } })).toEqual([{ kind: "unknown", data: expect.any(Object), name: "image" }])
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "mystery" } })).toEqual([{ kind: "unknown", data: expect.any(Object), name: "mystery" }])
  })

  it("content_part.added 没有 part 或空文本时给空", () => {
    expect(openaiResToDeltas({ type: "response.content_part.added" })).toEqual([])
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "text" } })).toEqual([])
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { type: "refusal" } })).toEqual([])
  })

  it("content_part.added 的 part 没有 type 时给空", () => {
    expect(openaiResToDeltas({ type: "response.content_part.added", part: { text: "x" } })).toEqual([])
  })

  it("function_call_arguments.delta 空文本时给空", () => {
    expect(openaiResToDeltas({ type: "response.function_call_arguments.delta", delta: "" })).toEqual([])
  })

  it("reasoning_summary_text.delta 空文本时给空", () => {
    expect(openaiResToDeltas({ type: "response.reasoning_summary_text.delta" })).toEqual([])
  })

  it("error 事件没有 error 字段时回退整条 chunk", () => {
    expect(openaiResToDeltas({ type: "response.error", message: "boom" })).toEqual([
      { kind: "unknown", data: { type: "response.error", message: "boom" } },
    ])
  })

  it("output_item.added 的 item 不是对象时给空", () => {
    expect(openaiResToDeltas({ type: "response.output_item.added", item: "bad" })).toEqual([])
  })

  it("output_item.added 没有 item 时给空", () => {
    expect(openaiResToDeltas({ type: "response.output_item.added" })).toEqual([])
  })

  it("output_item.added 带 function_call 时 final: true", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "function_call", call_id: "c1", name: "f", arguments: "{}" } })
    expect(out).toEqual([{ kind: "tool_call", id: "c1", name: "f", arguments: "{}", final: true, mimeType: undefined }])
  })

  it("output_item.added 带 custom_tool_call 时 final: true", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "custom_tool_call", call_id: "c2", name: "g", input: { a: 1 } } })
    expect(out).toEqual([{ kind: "tool_call", id: "c2", name: "g", arguments: { a: 1 }, final: true, mimeType: undefined }])
  })

  it("output_item.added 带 function_call_output 时成 tool_result", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "function_call_output", call_id: "c1", output: "result" } })
    expect(out).toEqual([{ kind: "tool_result", callId: "c1", data: "result", text: "result" }])
  })

  it("output_item.added 带 custom_tool_call_output 时成 tool_result", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "custom_tool_call_output", call_id: "c2", output: "ok" } })
    expect(out).toEqual([{ kind: "tool_result", callId: "c2", data: "ok", text: "ok" }])
  })

  it("output_item.added 带 reasoning 时成 thinking", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "reasoning", summary: [{ text: "想" }] } })
    expect(out).toEqual([{ kind: "thinking", text: "想" }])
  })

  it("output_item.added 带未知类型时落 unknown", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "mystery", v: 1 } })
    expect(out).toEqual([{ kind: "unknown", data: { type: "mystery", v: 1 }, name: "mystery" }])
  })

  it("output_item.added 带没有 type 的 item 时给空", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { v: 1 } })
    expect(out).toEqual([])
  })

  it("output_item.added 带 message 且 content 没有 text 时给空串", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "message", content: [{ type: "text" }] } })
    expect(out).toEqual([{ kind: "text", text: "" }])
  })

  it("output_item.added 带 message 且 refusal 没有 refusal 字段时给空串", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "message", content: [{ type: "refusal" }] } })
    expect(out).toEqual([{ kind: "text", text: "" }])
  })

  it("output_item.added 带 message 且 usage 不是对象时不算", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "message", status: "failed", usage: "bad", content: [] } })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "failed", usage: undefined }])
  })

  it("output_item.added 带 message 只有 usage 没有 status 时 finish 为空串", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "message", usage: { t: 1 }, content: [] } })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "", usage: { t: 1 } }])
  })

  it("output_item.added 带 function_call_output 的 output 不是字符串时 text 为 undefined", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "function_call_output", call_id: "c", output: null } })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "c", text: undefined })
  })

  it("output_item.added 带 custom_tool_call_output 的 output 不是字符串时 text 为 undefined", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "custom_tool_call_output", call_id: "c", output: null } })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "c", text: undefined })
  })

  it("output_item.added 带 function_call 没有 call_id 和 id 时给空 callId", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "function_call", name: "f", arguments: "{}" } })
    expect(out[0]).toMatchObject({ kind: "tool_call", id: "", name: "f" })
  })

  it("output_item.added 带 function_call 没有 name 时给空名", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "function_call", call_id: "c", arguments: "{}" } })
    expect(out[0]).toMatchObject({ kind: "tool_call", name: "" })
  })

  it("output_item.added 带 function_call_output 没有 call_id 时给空 callId", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "function_call_output", output: "ok" } })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "", text: "ok" })
  })

  it("output_item.added 带 custom_tool_call 没有 call_id 和 name 时给空值", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "custom_tool_call", input: {} } })
    expect(out[0]).toMatchObject({ kind: "tool_call", id: "", name: "" })
  })

  it("output_item.added 带 custom_tool_call_output 没有 call_id 时给空 callId", () => {
    const out = openaiResToDeltas({ type: "response.output_item.added", item: { type: "custom_tool_call_output", output: "ok" } })
    expect(out[0]).toMatchObject({ kind: "tool_result", callId: "", text: "ok" })
  })
})

describe("openaiResNonStreamDeltas", () => {
  it("非对象给空", () => {
    expect(openaiResNonStreamDeltas(null)).toEqual([])
    expect(openaiResNonStreamDeltas("x" as unknown as Record<string, unknown>)).toEqual([])
  })

  it("output 条目逐个展开，顶层 status 与 usage 单独收尾", () => {
    const out = openaiResNonStreamDeltas(JSON.parse(fixtures.openaiResNonStream.OfString!))
    expect(out.filter((d) => d.kind === "text").map((d) => d.text)).toEqual(["整包文本。", "不能回答"])
    expect(out.filter((d) => d.kind === "thinking").map((d) => d.text)).toEqual(["非流式的推理摘要"])
    expect(out.filter((d) => d.kind === "tool_call").map((d) => d.name)).toEqual(["get_weather"])
    expect(out.filter((d) => d.kind === "unknown")).toHaveLength(5)
    expect(out[out.length - 1]).toMatchObject({ kind: "unknown", finish: "completed", usage: { input_tokens: 1, output_tokens: 2 } })
  })

  it("message 条目只带 status 不带 usage 也发收尾事件", () => {
    const out = openaiResNonStreamDeltas({ output: [{ type: "message", status: "failed", content: [] }] })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "failed", usage: undefined }])
  })

  it("空 output 且没有 status 时不产生任何 delta", () => {
    expect(openaiResNonStreamDeltas({ output: [] })).toEqual([])
  })

  it("只有 usage 没有 status 时 finish 为空串", () => {
    const out = openaiResNonStreamDeltas({ output: [], usage: { t: 1 } })
    expect(out).toEqual([{ kind: "unknown", text: "", finish: "", usage: { t: 1 } }])
  })
})

describe("openaiResParseRequest", () => {
  const body = JSON.parse(fixtures.openaiResRequest.Input!)

  it("instructions 落成 system，模型名与参数各自归位", () => {
    const req = openaiResParseRequest(body)
    expect(req.protocol).toBe("openai-res")
    expect(req.model).toBe("gpt-4o-responses")
    expect(req.system).toBe("只回答问题本身")
    expect(req.params).toEqual({ max_output_tokens: 512, truncation: "auto" })
    expect(req.tools.map((t) => t.name)).toEqual(["get_weather"])
  })

  it("input 数组按角色折叠：user 消息、assistant 思考+调用、tool 结果", () => {
    const req = openaiResParseRequest(body)
    expect(req.messages.map((m) => m.role)).toEqual(["assistant", "user", "assistant", "tool", "assistant", "tool"])
  })

  it("reasoning 单独成条 assistant，遇到 user 消息就切分", () => {
    const req = openaiResParseRequest(body)
    expect(req.messages[0].role).toBe("assistant")
    expect(req.messages[0].parts.map((p) => p.type)).toEqual(["thinking"])
    expect(req.messages[0].parts[0]).toMatchObject({ type: "thinking", text: "推理摘要第一段。" })
  })

  it("function_call_output 反查出它回应的函数名", () => {
    const req = openaiResParseRequest(body)
    const tool = req.messages.find((m) => m.role === "tool")!
    expect(tool.parts).toEqual([{ type: "tool_result", callId: "fc_1", content: "{\"temp\":29}", toolCall: "get_weather" }])
  })

  it("custom_tool_call / custom_tool_call_output 走同一条路", () => {
    const req = openaiResParseRequest(body)
    const custom = req.messages.filter((m) => m.role === "tool")
    expect(custom.map((m) => (m.parts[0] as { toolCall?: string }).toolCall)).toEqual(["get_weather", "my_tool"])
  })

  it("input 是单个对象而非数组时也能解析", () => {
    const req = openaiResParseRequest({ model: "m", input: { type: "message", role: "user", content: [{ type: "input_text", text: "你好" }] } })
    expect(req.messages[0].role).toBe("user")
    expect(req.messages[0].parts).toEqual([{ type: "text", text: "你好" }])
  })

  it("未知的 input 条目类型落 unknown 并单独成一条消息", () => {
    const req = openaiResParseRequest({ input: [{ type: "mystery", v: 1 }] })
    expect(req.messages).toEqual([{ role: "unknown", parts: [{ type: "unknown", data: { type: "mystery", v: 1 }, name: "mystery" }], toolCalls: [] }])
  })

  it("没有 type 的条目被跳过", () => {
    expect(openaiResParseRequest({ input: [{ v: 1 }] }).messages).toEqual([])
  })

  it("input 既不是数组也不是对象时给空消息列表", () => {
    expect(openaiResParseRequest({ input: "bad" }).messages).toEqual([])
  })

  it("函数调用参数是坏 JSON 时打 parseError", () => {
    const req = openaiResParseRequest({ input: [{ type: "function_call", call_id: "b", name: "f", arguments: "{\"a\":" }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_use" })
    expect((req.messages[0].parts[0] as { call: { parseError?: boolean } }).call.parseError).toBe(true)
  })

  it("function_call 没有 id 时 tool_use.call.id 为空", () => {
    const req = openaiResParseRequest({ input: [{ type: "function_call", name: "f", arguments: "{}" }] })
    expect((req.messages[0].parts[0] as { call: { id?: string; name: string } }).call).toEqual({ id: undefined, name: "f", arguments: {} })
  })

  it("message 条目里的 input_image 落成 image part", () => {
    const req = openaiResParseRequest({ input: [{ type: "message", role: "user", content: [{ type: "input_image", image_url: "https://x.com/a.png", detail: "high" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "image", url: "https://x.com/a.png", label: "image", mimeType: "high" }])
  })

  it("message 条目里的 refusal 落成 text part", () => {
    const req = openaiResParseRequest({ input: [{ type: "message", role: "assistant", content: [{ type: "refusal", refusal: "不能说" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "text", text: "不能说" }])
  })

  it("message 条目里的未知类型落成 unknown part", () => {
    const req = openaiResParseRequest({ input: [{ type: "message", role: "user", content: [{ type: "mystery", v: 1 }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "unknown", data: { type: "mystery", v: 1 }, name: "mystery" }])
  })

  it("reasoning 条目合成 assistant 消息", () => {
    const req = openaiResParseRequest({ input: [{ type: "reasoning", summary: [{ text: "想想" }] }] })
    expect(req.messages[0].role).toBe("assistant")
    expect(req.messages[0].parts).toEqual([{ type: "thinking", text: "想想" }])
  })

  it("function_call_output 找不到对应函数名时 toolCall 为 undefined", () => {
    const req = openaiResParseRequest({ input: [{ type: "function_call_output", call_id: "missing", output: "r" }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", callId: "missing", toolCall: undefined })
  })

  it("custom_tool_call 走同一条路", () => {
    const req = openaiResParseRequest({ input: [{ type: "custom_tool_call", call_id: "c1", name: "my_tool", input: { a: 1 } }] })
    expect(req.messages[0].role).toBe("assistant")
    expect((req.messages[0].parts[0] as { call: { name: string } }).call.name).toBe("my_tool")
  })

  it("custom_tool_call_output 走同一条路", () => {
    const req = openaiResParseRequest({ input: [{ type: "custom_tool_call_output", call_id: "c1", output: "ok" }] })
    expect(req.messages[0].role).toBe("tool")
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", callId: "c1" })
  })

  it("message 条目没有 role 时默认 assistant", () => {
    const req = openaiResParseRequest({ input: [{ type: "message", content: [{ type: "input_text", text: "hi" }] }] })
    expect(req.messages[0].role).toBe("assistant")
  })

  it("message 条目里 text 类型没有 text 字段时给空串", () => {
    const req = openaiResParseRequest({ input: [{ type: "message", role: "user", content: [{ type: "input_text" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "text", text: "" }])
  })

  it("message 条目里 refusal 类型没有 refusal 字段时给空串", () => {
    const req = openaiResParseRequest({ input: [{ type: "message", role: "user", content: [{ type: "refusal" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "text", text: "" }])
  })

  it("function_call 没有 name 时给空名", () => {
    const req = openaiResParseRequest({ input: [{ type: "function_call", call_id: "c", arguments: "{}" }] })
    expect((req.messages[0].parts[0] as { call: { name: string } }).call.name).toBe("")
  })

  it("function_call_output 没有 call_id 时给空 callId", () => {
    const req = openaiResParseRequest({ input: [{ type: "function_call_output", output: "ok" }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", callId: "" })
  })

  it("function_call_output 的 call_id 为空串时 toolCall 为 undefined", () => {
    const req = openaiResParseRequest({ input: [{ type: "function_call_output", call_id: "", output: "ok" }] })
    expect(req.messages[0].parts[0]).toMatchObject({ type: "tool_result", toolCall: undefined })
  })

  it("function_call_output 找到对应函数但函数没有 name 时 toolCall 为 undefined", () => {
    const req = openaiResParseRequest({
      input: [
        { type: "function_call", call_id: "c1", arguments: "{}" },
        { type: "function_call_output", call_id: "c1", output: "ok" },
      ],
    })
    expect((req.messages.find((m) => m.role === "tool")!.parts[0] as { toolCall?: string }).toolCall).toBeUndefined()
  })

  it("function_call 用 id 而不是 call_id 时也能反查", () => {
    const req = openaiResParseRequest({
      input: [
        { type: "function_call", id: "i1", name: "f", arguments: "{}" },
        { type: "function_call_output", call_id: "i1", output: "ok" },
      ],
    })
    expect((req.messages.find((m) => m.role === "tool")!.parts[0] as { toolCall?: string }).toolCall).toBe("f")
  })
})
