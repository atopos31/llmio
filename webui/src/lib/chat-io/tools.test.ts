import { describe, expect, it } from "vitest"

import { paramsOf, parseToolDeclarations } from "./tools"

describe("parseToolDeclarations", () => {
  it("非数组给空列表", () => {
    for (const value of [null, undefined, {}, "x", 3]) expect(parseToolDeclarations(value, "openai")).toEqual([])
  })

  it("OpenAI：function 包裹式取 name / description / parameters", () => {
    const out = parseToolDeclarations(
      [{ type: "function", function: { name: "get_weather", description: "查天气", parameters: { type: "object" } } }],
      "openai",
    )
    expect(out).toEqual([{ name: "get_weather", description: "查天气", parameters: { type: "object" } }])
  })

  it("OpenAI：内置工具没有函数名，直接过滤掉", () => {
    const out = parseToolDeclarations(
      [{ type: "web_search", search_context_size: "medium" }, { type: "computer_use" }],
      "openai",
    )
    expect(out).toEqual([])
  })

  it("Responses：顶层 name 形状 + web_search 过滤", () => {
    const out = parseToolDeclarations(
      [
        { type: "function", name: "get_weather", input_schema: { type: "object" } },
        { type: "web_search" },
        { type: "computer_use" },
        { name: "another", description: "d", parameters: { a: 1 } },
      ],
      "openai-res",
    )
    expect(out).toEqual([
      { name: "get_weather", description: undefined, parameters: { type: "object" } },
      { name: "another", description: "d", parameters: { a: 1 } },
    ])
  })

  it("Anthropic：name + input_schema", () => {
    const out = parseToolDeclarations([{ name: "get_weather", input_schema: { type: "object" } }], "anthropic")
    expect(out).toEqual([{ name: "get_weather", description: undefined, parameters: { type: "object" } }])
  })

  it("Gemini：functionDeclarations 展开成多条", () => {
    const out = parseToolDeclarations(
      [
        { functionDeclarations: [{ name: "a", parameters: { p: 1 } }, { name: "b", description: "dd" }] },
        { googleSearch: {} },
        { name: "loose", parameters: { q: 1 } },
      ],
      "gemini",
    )
    expect(out).toEqual([
      { name: "a", description: undefined, parameters: { p: 1 } },
      { name: "b", description: "dd", parameters: undefined },
      { name: "loose", description: undefined, parameters: { q: 1 } },
    ])
  })

  it("既没有 function 也没有 name 也没有 declarations 的项落成空名，不算丢", () => {
    const out = parseToolDeclarations([{ type: "mystery_tool", extra: 1 }], "anthropic")
    expect(out).toEqual([{ name: "" }])
  })

  it("非对象的条目被跳过", () => {
    expect(parseToolDeclarations(["str", null, 3, { name: "ok" }], "anthropic")).toEqual([{ name: "ok", description: undefined, parameters: undefined }])
  })

  it("OpenAI：function 包裹没有 name 时给空名", () => {
    const out = parseToolDeclarations(
      [{ type: "function", function: { description: "d" } }],
      "openai",
    )
    expect(out).toEqual([{ name: "", description: "d", parameters: undefined }])
  })

  it("Gemini：functionDeclarations 条目没有 name 时给空名", () => {
    const out = parseToolDeclarations(
      [{ functionDeclarations: [{ description: "no name" }] }],
      "gemini",
    )
    expect(out).toEqual([{ name: "", description: "no name", parameters: undefined }])
  })
})

describe("paramsOf", () => {
  it("保留集之外的字段全部收进 params", () => {
    expect(paramsOf({ model: "m", messages: [], temperature: 0.3, seed: 7 }, ["model", "messages"])).toEqual({
      temperature: 0.3,
      seed: 7,
    })
  })

  it("空保留集收全部", () => {
    expect(paramsOf({ a: 1 }, [])).toEqual({ a: 1 })
  })

  it("body 为空给空对象", () => {
    expect(paramsOf({}, ["model"])).toEqual({})
  })
})
