import { describe, expect, it } from "vitest"

import { openaiParseRequest, openaiToDeltas } from "./openai"
import { fixtures } from "./fixtures"

describe("openaiToDeltas", () => {
  it("choices 不是数组时给空", () => {
    for (const chunk of [{}, { choices: null }, { choices: { a: 1 } }, { choices: [] }]) {
      expect(openaiToDeltas(chunk)).toEqual([])
    }
  })

  it("delta 与 finish_reason 都没有的 choice 被跳过", () => {
    expect(openaiToDeltas({ choices: [{ index: 0 }, { delta: null, finish_reason: "" }] })).toEqual([])
  })

  it("正文与思考分片各自成桶，三家推理字段名都认", () => {
    const deltas = openaiToDeltas({ choices: [{ index: 0, delta: {
      content: "正文",
      reasoning_content: "思考A",
      reasoning: "思考B",
      reasoning_details: "思考C",
    }}]} as Record<string, unknown>)
    expect(deltas.map((d) => [d.kind, d.text])).toEqual([
      ["text", "正文"],
      ["thinking", "思考A"],
      ["thinking", "思考B"],
      ["thinking", "思考C"],
    ])
  })

  it("finish_reason 单独发一个 unknown 事件，不带文本", () => {
    const deltas = openaiToDeltas({ choices: [{ index: 0, finish_reason: "stop" }] })
    expect(deltas).toEqual([{ kind: "unknown", text: "", finish: "stop" }])
  })

  it("tool_calls 分片带 index / id / name / arguments", () => {
    const argWithCity = "{\"city\"}"
    const chunk = {
      choices: [
        {
          index: 0,
          delta: {
            tool_calls: [
              {
                index: 0,
                id: "c1",
                function: {
                  name: "get_weather",
                  arguments: argWithCity,
                },
              },
              { function: { arguments: "x" } },
            ],
          },
        },
      ],
    }
    const deltas = openaiToDeltas(chunk)
    expect(deltas).toEqual([
      { kind: "tool_call", index: 0, id: "c1", name: "get_weather", arguments: argWithCity, mimeType: undefined },
      { kind: "tool_call", index: undefined, id: undefined, name: "", arguments: "x", mimeType: undefined },
    ])
  })

  it("function 不是对象的 tool_call 也能读", () => {
    const deltas = openaiToDeltas({ choices: [{ index: 0, delta: { tool_calls: [{ index: 1, function: null }] }}]})
    expect(deltas).toEqual([{ kind: "tool_call", index: 1, id: undefined, name: "", arguments: undefined, mimeType: undefined }])
  })

  it("没有 function 字段的 tool_call 也能读", () => {
    const deltas = openaiToDeltas({ choices: [{ index: 0, delta: { tool_calls: [{ index: 2, id: "c2" }] }}]})
    expect(deltas).toEqual([{ kind: "tool_call", index: 2, id: "c2", name: "", arguments: undefined, mimeType: undefined }])
  })
})

describe("openaiParseRequest", () => {
  const body = JSON.parse(fixtures.openaiRequest.Input!)

  it("请求名照原样取出，参数收在 params 里", () => {
    const req = openaiParseRequest(body)
    expect(req.protocol).toBe("openai")
    expect(req.model).toBe("gpt-4o")
    expect(req.params).toEqual({ temperature: 0.7 })
    expect(req.errors).toEqual([])
  })

  it("多模态 content 数组：text / image_url / input_audio 各自成 part，未识别的落 unknown", () => {
    const user = openaiParseRequest(body).messages.find((m) => m.role === "user")!
    expect(user.parts).toEqual([
      { type: "text", text: "上海现在怎么样？" },
      { type: "image", url: "https://example.com/a.png", label: "image" },
      { type: "audio", label: "audio", data: "AAAA", mimeType: "wav" },
      { type: "unknown", data: { type: "video_url", video_url: { url: "https://example.com/a.mp4" } }, name: "video_url" },
    ])
  })

  it("assistant 的 tool_calls 落成 tool_use，坏 JSON 参数打 parseError", () => {
    const assistant = openaiParseRequest(body).messages.find((m) => m.role === "assistant")!
    expect(assistant.toolCalls).toHaveLength(2)
    expect(assistant.toolCalls[0]).toMatchObject({ id: "call_1", index: 0, name: "get_weather" })
    expect(assistant.toolCalls[1]).toMatchObject({ id: "call_2", index: 1, name: "broken", parseError: true })
    expect(assistant.toolCalls[1].arguments).toBe("{\"city\":")
  })

  it("tool 消息落成 tool_result，tool_call_id 是回挂依据", () => {
    const tool = openaiParseRequest(body).messages.find((m) => m.role === "tool")!
    expect(tool.parts).toEqual([
      { type: "tool_result", callId: "call_1", content: "{\"temp\":31,\"cond\":\"晴\"}" },
    ])
  })

  it("tool 消息带 camelCase toolCallId 也能认", () => {
    const req = openaiParseRequest({ messages: [{ role: "tool", toolCallId: "camel", content: "ok" }] })
    expect(req.messages[0].parts).toEqual([{ type: "tool_result", callId: "camel", content: "ok" }])
  })

  it("空字符串 content 不产生 part", () => {
    const req = openaiParseRequest({ messages: [{ role: "user", content: "" }] })
    expect(req.messages[0].parts).toEqual([])
  })

  it("content 是对象（非数组非字符串）时落 unknown", () => {
    const req = openaiParseRequest({ messages: [{ role: "user", content: { weird: true } }] })
    expect(req.messages[0].parts).toEqual([{ type: "unknown", data: { weird: true }, name: "content" }])
  })

  it("content 是对象且带 tool_call_id 时当 tool_result", () => {
    const req = openaiParseRequest({ messages: [{ role: "tool", tool_call_id: "c9", content: { refusal: "no" } }] })
    expect(req.messages[0].parts).toEqual([{ type: "tool_result", callId: "c9", content: { refusal: "no" } }])
  })

  it("assistant 消息上的三种推理字段名都落成 thinking part", () => {
    const req = openaiParseRequest({ messages: [{ role: "assistant", reasoning: "R1", reasoning_content: "R2", reasoning_details: "R3" }] })
    expect(req.messages[0].parts.map((p) => p.type)).toEqual(["thinking", "thinking", "thinking"])
    expect(req.messages[0].parts.map((p) => (p as { text: string }).text)).toEqual(["R2", "R1", "R3"])
  })

  it("缺失 role 归为 unknown，tools 过滤内置工具", () => {
    const req = openaiParseRequest({ messages: [{ content: "x" }], tools: [{ type: "web_search" }] })
    expect(req.messages[0].role).toBe("unknown")
    expect(req.tools).toEqual([])
  })

  it("tool_calls 没有 function / id / index / name 时各自给默认值", () => {
    const req = openaiParseRequest({ messages: [{ role: "assistant", tool_calls: [{ arguments: "{}" }] }] })
    expect(req.messages[0].toolCalls).toEqual([{ id: undefined, index: undefined, name: "", arguments: undefined }])
  })

  it("image_url 不是对象时不给 url", () => {
    const req = openaiParseRequest({ messages: [{ role: "user", content: [{ type: "image_url" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "image", url: undefined, label: "image" }])
  })

  it("text 类型没有 text 字段时给空串", () => {
    const req = openaiParseRequest({ messages: [{ role: "user", content: [{ type: "text" }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "text", text: "" }])
  })

  it("input_audio 不是对象时不给 data 与 mimeType", () => {
    const req = openaiParseRequest({ messages: [{ role: "user", content: [{ type: "input_audio", input_audio: null }] }] })
    expect(req.messages[0].parts).toEqual([{ type: "audio", label: "audio", data: undefined, mimeType: undefined }])
  })
})
