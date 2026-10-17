import { describe, expect, it } from "vitest"

import { geminiCandidateDeltas, geminiNonStreamDeltas, geminiParseRequest, partToDelta } from "./gemini"
import { fixtures } from "./fixtures"

describe("partToDelta", () => {
  it("text part 落成 text 或 thinking（thought: true）", () => {
    expect(partToDelta({ text: "正文" })).toEqual({ kind: "text", text: "正文" })
    expect(partToDelta({ text: "思考", thought: true })).toEqual({ kind: "thinking", text: "思考" })
  })

  it("inlineData 的 camel 与 snake 两种写法都认，落成带 mime 的 unknown", () => {
    expect(partToDelta({ inlineData: { mimeType: "image/png", data: "D1" } })).toEqual({
      kind: "unknown", data: expect.any(Object), name: "inline_data", mimeType: "image/png", text: "D1",
    })
    const snake = partToDelta({ inline_data: { mime_type: "image/jpeg", data: "D2" } })
    expect(snake).toMatchObject({ kind: "unknown", name: "inline_data", mimeType: "image/jpeg", text: "D2" })
  })

  it("functionCall 落成 tool_call，args 转成 JSON 字符串", () => {
    const out = partToDelta({ functionCall: { name: "get_weather", args: { city: "上海" } } })
    expect(out).toEqual({ kind: "tool_call", name: "get_weather", arguments: "{\"city\":\"上海\"}" })
  })

  it("functionCall 没有 name 时给空名", () => {
    const out = partToDelta({ functionCall: { args: { city: "上海" } } })
    expect(out).toEqual({ kind: "tool_call", name: "", arguments: "{\"city\":\"上海\"}" })
  })

  it("functionResponse 落成 tool_result", () => {
    expect(partToDelta({ functionResponse: { name: "get_weather", response: { temp: 31 } } })).toEqual({
      kind: "tool_result", callId: "get_weather", data: { temp: 31 },
    })
  })

  it("functionResponse 没有 name 时给空 callId", () => {
    const out = partToDelta({ functionResponse: { response: { temp: 31 } } })
    expect(out).toEqual({ kind: "tool_result", callId: "", data: { temp: 31 } })
  })

  it("不认识的 part 落 unknown", () => {
    expect(partToDelta({ custom: 1 })).toEqual({ kind: "unknown", data: { custom: 1 }, name: "part" })
  })

  it("functionResponse 用 responseData 而不是 response 时也能读", () => {
    const out = partToDelta({ functionResponse: { name: "f", responseData: { ok: 1 } } })
    expect(out).toEqual({ kind: "tool_result", callId: "f", data: { ok: 1 } })
  })
})

describe("geminiCandidateDeltas", () => {
  it("candidates 不是数组时给空", () => {
    expect(geminiCandidateDeltas(null)).toEqual([])
    expect(geminiCandidateDeltas({})).toEqual([])
    expect(geminiCandidateDeltas([])).toEqual([])
  })

  it("多个 candidate 与 finishReason 都收进来", () => {
    const out = geminiCandidateDeltas([
      { content: { parts: [{ text: "A" }] }, finishReason: "STOP" },
      { content: { parts: [{ text: "B" }] } },
      { parts: [{ text: "C" }] },
    ])
    expect(out).toEqual([
      { kind: "text", text: "A" },
      { kind: "unknown", text: "", finish: "STOP" },
      { kind: "text", text: "B" },
      { kind: "text", text: "C" },
    ])
  })

  it("整条流式响应按片段顺序拼装", () => {
    const deltas = fixtures.geminiStream.OfStringArray!.map((c) => JSON.parse(c)).flatMap(geminiCandidateDeltas)
    expect(deltas.filter((d) => d.kind === "text").map((d) => d.text)).toEqual(["我来查", "多云"])
    expect(deltas.filter((d) => d.kind === "thinking").map((d) => d.text)).toEqual(["好的"])
    expect(deltas.filter((d) => d.kind === "tool_call").map((d) => d.name)).toEqual(["get_weather"])
    expect(deltas.filter((d) => d.kind === "tool_result").map((d) => d.callId)).toEqual(["get_weather"])
    expect(deltas.filter((d) => d.kind === "unknown")).toHaveLength(4)
  })
})

describe("geminiNonStreamDeltas", () => {
  it("非对象给空", () => {
    expect(geminiNonStreamDeltas(null)).toEqual([])
    expect(geminiNonStreamDeltas("x" as unknown as Record<string, unknown>)).toEqual([])
  })

  it("candidates 展开后追加 usageMetadata 收尾事件", () => {
    const out = geminiNonStreamDeltas(JSON.parse(fixtures.geminiNonStream.OfString!))
    expect(out[out.length - 1]).toMatchObject({ kind: "unknown", usage: { promptTokenCount: 1, candidatesTokenCount: 2 } })
    expect(out.filter((d) => d.kind === "thinking").map((d) => d.text)).toEqual(["整包思考"])
    expect(out.filter((d) => d.kind === "tool_call").map((d) => d.name)).toEqual(["get_weather"])
  })

  it("没有 usageMetadata 时不追加", () => {
    expect(geminiNonStreamDeltas({ candidates: [] })).toEqual([])
  })
})

describe("geminiParseRequest", () => {
  const body = JSON.parse(fixtures.geminiRequest.Input!)

  it("systemInstruction 拍平，generationConfig 归入参数", () => {
    const req = geminiParseRequest(body)
    expect(req.protocol).toBe("gemini")
    expect(req.system).toBe("你是助手\n\n第二段")
    expect(req.params).toEqual({ generationConfig: { temperature: 0.4, maxOutputTokens: 512 } })
    expect(req.tools.map((t) => t.name)).toEqual(["get_weather", "get_time"])
  })

  it("systemInstruction 是对象但 parts 为空时回退", () => {
    expect(geminiParseRequest({ systemInstruction: { text: "只有文本" } }).system).toBe("只有文本")
    expect(geminiParseRequest({ systemInstruction: { odd: true } }).system).toEqual({ odd: true })
  })

  it("systemInstruction 是普通字符串时原样保留", () => {
    expect(geminiParseRequest({ systemInstruction: "直接字符串" }).system).toBe("直接字符串")
  })

  it("systemInstruction 的 parts 里没有 text 的条目用 JSON 兜底", () => {
    expect(geminiParseRequest({ systemInstruction: { parts: [{ v: 1 }] } }).system).toBe('{"v":1}')
  })

  it("contents 是单个对象而非数组时也能解析", () => {
    const req = geminiParseRequest({ contents: { role: "user", parts: [{ text: "你好" }] } })
    expect(req.messages).toEqual([{ role: "user", parts: [{ type: "text", text: "你好" }], toolCalls: [] }])
  })

  it("没有 role 的 content 归为 unknown", () => {
    const req = geminiParseRequest({ contents: [{ parts: [{ text: "x" }] }] })
    expect(req.messages[0].role).toBe("unknown")
  })

  it("parts 为空或不是数组时给空 part 列表", () => {
    const req = geminiParseRequest({ contents: [{ role: "user" }, { role: "model", parts: "bad" }] })
    expect(req.messages.map((m) => m.parts)).toEqual([[], []])
  })

  it("thought: true 的 text 落 thinking，functionCall/functionResponse 各自归位", () => {
    const req = geminiParseRequest(body)
    expect(req.messages.map((m) => m.role)).toEqual(["user", "model", "user"])
    const model = req.messages[1]
    expect(model.parts.map((p) => p.type)).toEqual(["thinking", "tool_use", "unknown"])
    expect(model.toolCalls).toHaveLength(1)
    expect(model.toolCalls[0].name).toBe("get_weather")
  })

  it("functionResponse 有对应调用时按名回填 toolCall", () => {
    const req = geminiParseRequest(body)
    const result = req.messages[2].parts[0] as { type: string; toolCall?: string; callId: string }
    expect(result).toMatchObject({ type: "tool_result", callId: "get_weather", toolCall: "get_weather" })
  })

  it("functionResponse 找不到对应调用时 toolCall 留空", () => {
    const req = geminiParseRequest(body)
    const unknown = req.messages[2].parts[1] as { toolCall?: string; callId: string }
    expect(unknown.toolCall).toBeUndefined()
    expect(unknown.callId).toBe("unknown_tool")
  })

  it("inline_data 落成 image part，未识别 part 落 unknown", () => {
    const req = geminiParseRequest(body)
    const user = req.messages[0]
    expect(user.parts.map((p) => p.type)).toEqual(["text", "image", "image"])
    expect(user.parts[1]).toMatchObject({ type: "image", label: "inline_data", mimeType: "image/png", data: "PNGDATA" })
  })
})
