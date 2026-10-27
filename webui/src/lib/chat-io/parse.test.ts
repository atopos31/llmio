import { describe, expect, it } from "vitest"

import { fixtures } from "./fixtures"
import { detectProtocol, parseRequest, parseResponse, sniffProtocol, sniffResponseProtocol } from "./parse"
import type { Protocol } from "./types"

describe("detectProtocol", () => {
  it("响应体形状优先：choices / output / candidates / content", () => {
    expect(detectProtocol({ choices: [] })).toBe("openai")
    expect(detectProtocol({ output: [] })).toBe("openai-res")
    expect(detectProtocol({ candidates: [] })).toBe("gemini")
    expect(detectProtocol({ content: [] })).toBe("anthropic")
  })

  it("响应体判不出来时按请求体形状退而求其次", () => {
    expect(detectProtocol({ messages: [] })).toBe("anthropic")
    expect(detectProtocol({ system: "sys" })).toBe("anthropic")
    expect(detectProtocol({ input: [] })).toBe("openai-res")
    expect(detectProtocol({ instructions: "ins" })).toBe("openai-res")
    expect(detectProtocol({ contents: [] })).toBe("gemini")
    expect(detectProtocol({ systemInstruction: { parts: [] } })).toBe("gemini")
  })

  it("全部判不出来时回落到 openai", () => {
    expect(detectProtocol({})).toBe("openai")
    expect(detectProtocol({ foo: 1 })).toBe("openai")
  })
})

describe("parseRequest", () => {

  it("空输入给空结果", () => {
    expect(parseRequest({}, "openai")).toEqual({ protocol: "openai", messages: [], tools: [], params: {}, errors: [] })
    expect(parseRequest({ Input: null }, "openai")).toEqual({ protocol: "openai", messages: [], tools: [], params: {}, errors: [] })
    expect(parseRequest({ Input: "" }, "openai")).toEqual({ protocol: "openai", messages: [], tools: [], params: {}, errors: [] })
  })

  it("不是 JSON 或不是对象时给错误并保留原文", () => {
    expect(parseRequest({ Input: "完全不是 json" }, "openai")).toEqual({
      protocol: "openai",
      messages: [],
      tools: [],
      params: {},
      errors: [{ protocol: "openai", message: "请求体不是合法的 JSON 对象" }],
      raw: "完全不是 json",
    })
    expect(parseRequest({ Input: "[1,2,3]" }, "openai").raw).toBe("[1,2,3]")
    expect(parseRequest({ Input: "{}" }, "openai").messages).toEqual([])
  })

  it("Style 可信时直接用它，不按形状嗅探", () => {
    const req = parseRequest({ Input: JSON.stringify({ model: "m", messages: [{ role: "user", content: "你好" }] }) }, "openai")
    expect(req.protocol).toBe("openai")
    expect(req.model).toBe("m")
    expect(req.messages).toHaveLength(1)
  })

  it("Style 缺失时按请求体嗅探出 anthropic / responses / gemini", () => {
    expect(
      parseRequest({ Input: JSON.stringify({ system: "s", messages: [{ role: "user", content: "你好" }] }) }, undefined).protocol,
    ).toBe("anthropic")
    expect(
      parseRequest({ Input: JSON.stringify({ instructions: "i", input: [{ type: "message", role: "user", content: [{ type: "input_text", text: "x" }] }] }) }, null).protocol,
    ).toBe("openai-res")
    expect(parseRequest({ Input: JSON.stringify({ contents: [{ role: "user", parts: [{ text: "x" }] }] }) }, "" as unknown as Protocol).protocol).toBe("gemini")
  })

  it("Style 是未知值时：请求侧按嗅探，响应侧回落到 openai", () => {
    const unknownStyle = "not-a-real-style" as unknown as Protocol
    const req = parseRequest({ Input: JSON.stringify({ model: "m", messages: [{ role: "user", content: "x" }] }) }, unknownStyle)
    expect(req.protocol).toBe("openai")
    expect(parseResponse({ OfString: JSON.stringify({ choices: [{ message: { content: "文本" } }] }) }, unknownStyle).protocol).toBe("openai")
  })

  it("四个协议的请求都能解析出消息与工具", () => {
    const openai = parseRequest(fixtures.openaiRequest, "openai")
    expect(openai.model).toBe("gpt-4o")
    expect(openai.messages).toHaveLength(4)
    expect(openai.tools).toHaveLength(1)

    const openaiRes = parseRequest(fixtures.openaiResRequest, "openai-res")
    expect(openaiRes.system).toBe("只回答问题本身")
    expect(openaiRes.messages).toHaveLength(6)

    const anthropic = parseRequest(fixtures.anthropicRequest, "anthropic")
    expect(anthropic.tools).toHaveLength(1)
    expect(anthropic.messages).toHaveLength(3)

    const gemini = parseRequest(fixtures.geminiRequest, "gemini")
    expect(gemini.tools.map((t) => t.name)).toEqual(["get_weather", "get_time"])
  })
})

describe("parseResponse", () => {
  it("没有响应内容时给空结果", () => {
    const empty = { protocol: "openai", stream: false, text: "", thinking: "", toolCalls: [], toolResults: [], chunks: [], malformed: 0, unknown: 0 }
    expect(parseResponse({}, "openai")).toEqual(empty)
    expect(parseResponse({ OfString: "" }, "openai")).toEqual(empty)
    expect(parseResponse({ OfStringArray: [] }, "openai")).toEqual(empty)
  })

  it("OpenAI 流式：正文、思考、工具分片、usage 与结束原因都到位", () => {
    const res = parseResponse(fixtures.openaiStream, "openai")
    expect(res.stream).toBe(true)
    expect(res.protocol).toBe("openai")
    expect(res.model).toBe("gpt-4o-2024")
    expect(res.text).toBe("上海今天多云 31°")
    expect(res.thinking).toBe("先查一下实时天气。")
    expect(res.toolCalls.map((c) => [c.id, c.name, c.arguments])).toEqual([
      ["call_a", "get_weather", "{\"city\":\"上海\"}"],
      ["call_b", "search", "{\"q\":\"天气\"}"],
    ])
    expect(res.finishReason).toBe("stop")
    expect(res.usage).toMatchObject({ total_tokens: 30 })
    expect(res.malformed).toBe(0)
    expect(res.unknown).toBe(0)
    expect(res.chunks).toHaveLength(7)
  })

  it("OpenAI 非流式：整包 message 被拆成增量，思考与工具调用都在", () => {
    const res = parseResponse(fixtures.openaiNonStream, "openai")
    expect(res.stream).toBe(false)
    expect(res.model).toBe("gpt-4o")
    expect(res.thinking).toBe("思考过程")
    expect(res.toolCalls.map((c) => c.name)).toEqual(["get_weather"])
    expect(res.finishReason).toBe("tool_calls")
    expect(res.chunks).toHaveLength(1)
  })

  it("Responses 流式：事件按顺序拼装成正文、思考与工具调用", () => {
    const res = parseResponse(fixtures.openaiResStream, "openai-res")
    expect(res.model).toBe("gpt-4o-responses")
    expect(res.text).toBe("第一段。第二段。拒绝内容")
    expect(res.thinking).toBe("思考 A思考 B")
    expect(res.toolCalls).toHaveLength(1)
    expect(res.toolCalls[0]).toMatchObject({ id: "fc_2", name: "get_weather", arguments: "{\"city\":\"杭州\"}" })
    expect(res.toolResults.map((r) => r.callId)).toEqual(["fc_2"])
    expect(res.finishReason).toBe("completed")
    expect(res.usage).toMatchObject({ input_tokens: 3, output_tokens: 9 })
  })

  it("Responses 非流式：output 数组展开，函数名与正文都拿到", () => {
    const res = parseResponse(fixtures.openaiResNonStream, "openai-res")
    expect(res.stream).toBe(false)
    expect(res.model).toBe("gpt-4o-responses")
    expect(res.text).toBe("整包文本。不能回答")
    expect(res.thinking).toBe("非流式的推理摘要")
    expect(res.toolCalls.map((c) => c.name)).toEqual(["get_weather"])
    expect(res.finishReason).toBe("completed")
    expect(res.toolResults).toHaveLength(0)
  })

  it("Anthropic 流式：thinking 分片拼接，signature 不计入思考", () => {
    const res = parseResponse(fixtures.anthropicStream, "anthropic")
    expect(res.model).toBe("claude-sonnet-4-20250514")
    expect(res.text).toBe("结果 多云")
    expect(res.thinking).toBe("让我想想")
    expect(res.toolCalls).toHaveLength(1)
    expect(res.toolCalls[0]).toMatchObject({ id: "tu_9", name: "get_weather", arguments: "{\"city\":\"上海\"}" })
    expect(res.finishReason).toBe("tool_use")
    expect(res.usage).toMatchObject({ output_tokens: 7 })
  })

  it("Anthropic 非流式：content 数组里的每类块都展开", () => {
    const res = parseResponse(fixtures.anthropicNonStream, "anthropic")
    expect(res.model).toBe("claude-sonnet-4-20250514")
    expect(res.thinking).toBe("非流式思考RED2")
    expect(res.text).toBe("非流式正文")
    expect(res.toolCalls).toHaveLength(1)
    expect(res.finishReason).toBe("tool_use")
  })

  it("Gemini 流式：thought part 落思考，functionCall 与 functionResponse 各自归位", () => {
    const res = parseResponse(fixtures.geminiStream, "gemini")
    expect(res.model).toBe("gemini-2.0-flash-001")
    expect(res.text).toBe("我来查多云")
    expect(res.thinking).toBe("好的")
    expect(res.toolCalls.map((c) => c.name)).toEqual(["get_weather"])
    expect(res.toolResults.map((r) => r.callId)).toEqual(["get_weather"])
    expect(res.finishReason).toBe("STOP")
    expect(res.usage).toMatchObject({ promptTokenCount: 5 })
  })

  it("Gemini 非流式：candidates 与 usageMetadata 都收进来", () => {
    const res = parseResponse(fixtures.geminiNonStream, "gemini")
    expect(res.stream).toBe(false)
    expect(res.text).toBe("整包正文")
    expect(res.thinking).toBe("整包思考")
    expect(res.toolResults).toHaveLength(1)
    expect(res.finishReason).toBe("STOP")
  })

  it("Style 缺失时按响应体形状嗅探协议", () => {
    expect(parseResponse(fixtures.sniffed.openai, null).text).toBe("嗅探到 openai")
    expect(parseResponse(fixtures.sniffed.openaiRes, null).text).toBe("嗅探到 responses")
    expect(parseResponse(fixtures.sniffed.anthropic, null).text).toBe("嗅探到 anthropic")
    expect(parseResponse(fixtures.sniffed.gemini, null).text).toBe("嗅探到 gemini")
  })

  it("响应不是 JSON 时按原文保留并计 malformed，不抛异常", () => {
    const res = parseResponse(fixtures.nonJsonResponse, "openai")
    expect(res.malformed).toBe(1)
    expect(res.chunks).toEqual(["这不是 JSON"])
    expect(res.text).toBe("")
  })

  it("流式里混入坏片段与非字符串：坏片段计数，非字符串直接忽略", () => {
    const res = parseResponse(fixtures.mixedMalformed, "openai")
    expect(res.stream).toBe(true)
    expect(res.malformed).toBe(1)
    expect(res.text).toBe("好的")
    expect(res.chunks).toHaveLength(2)
  })

  it("模型名在 response / message 子对象里也能取到", () => {
    expect(parseResponse({ OfString: JSON.stringify({ response: { model: "nested-res" } }) }, "openai-res").model).toBe("nested-res")
    expect(parseResponse({ OfString: JSON.stringify({ message: { model: "nested-ant" } }) }, "anthropic").model).toBe("nested-ant")
    expect(parseResponse({ OfString: JSON.stringify({ model_version: "v1" }) }, "openai").model).toBe("v1")
  })

  it("tool_result 回挂不到调用名时保留空值，不算错", () => {
    const res = parseResponse({ OfString: JSON.stringify({ content: [{ type: "tool_result", tool_use_id: "missing", content: "x" }] }) }, "anthropic")
    expect(res.toolResults).toHaveLength(1)
    expect(res.toolResults[0]).toMatchObject({ callId: "missing", content: "x", toolCall: undefined })
  })

  it("响应是 JSON 字符串（非对象）时忽略该段，不抛异常", () => {
    const res = parseResponse({ OfStringArray: ['"hello"', '{"choices":[{"delta":{"content":"好的"}}]}'] }, "openai")
    expect(res.text).toBe("好的")
    expect(res.chunks).toHaveLength(2)
    expect(res.malformed).toBe(0)
  })
})

describe("sniffProtocol", () => {
  it("认出四种聊天协议", () => {
    expect(sniffProtocol({ choices: [] })).toBe("openai")
    expect(sniffProtocol({ output: [] })).toBe("openai-res")
    expect(sniffProtocol({ candidates: [] })).toBe("gemini")
    expect(sniffProtocol({ content: [] })).toBe("anthropic")
    expect(sniffProtocol({ model: "m", messages: [] })).toBe("openai")
    expect(sniffProtocol({ system: "s" })).toBe("anthropic")
    expect(sniffProtocol({ input: [] })).toBe("openai-res")
    expect(sniffProtocol({ contents: [] })).toBe("gemini")
  })

  it("一条特征都没有时返回 null（非聊天协议）", () => {
    expect(sniffProtocol({})).toBeNull()
    expect(sniffProtocol({ output: "ok-from-mock", usage: { prompt_tokens: 1 } })).toBeNull()
    expect(sniffProtocol({ data: [{ embedding: [0.1, 0.2] }] })).toBeNull()
    // OpenAI 与 Anthropic 的区分特征都不完整时，messages 兜底成 anthropic。
    expect(sniffProtocol({ messages: [] })).toBe("anthropic")
  })
})

describe("sniffResponseProtocol", () => {
  it("Style 已知协议时直接采信，不再看响应体", () => {
    expect(sniffResponseProtocol({ OfString: JSON.stringify({ data: [] }) }, "anthropic")).toBe("anthropic")
  })

  it("Style 未知时按响应体嗅探，认不出来返回 null", () => {
    expect(sniffResponseProtocol({ OfString: JSON.stringify({ choices: [] }) }, "passthrough" as unknown as Protocol)).toBe("openai")
    expect(sniffResponseProtocol({ OfString: JSON.stringify({ output: "ok-from-mock" }) }, "passthrough" as unknown as Protocol)).toBeNull()
    expect(sniffResponseProtocol({}, undefined)).toBeNull()
  })

  it("流式响应里有一条认出来就算认识", () => {
    const io = { OfStringArray: ['{"output":"he"}', '{"choices":[{"delta":{"content":"llo"}}]}'] }
    expect(sniffResponseProtocol(io, "passthrough" as unknown as Protocol)).toBe("openai")
  })

  it("响应体不是 JSON 时不强行认协议", () => {
    expect(sniffResponseProtocol({ OfString: "这不是 JSON" }, undefined)).toBeNull()
    expect(sniffResponseProtocol({ OfString: "[1,2]" }, null)).toBeNull()
  })
})
