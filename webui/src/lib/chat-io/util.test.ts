import { describe, expect, it } from "vitest"

import { arrayFrom, argsOf, isNum, isObj, parseArgs, str, toChunk } from "./util"

describe("isObj", () => {
  it("只认普通对象：数组、null、标量都算没有", () => {
    expect(isObj({ a: 1 })).toBe(true)
    for (const value of [[1], null, undefined, "s", 3, true]) expect(isObj(value)).toBe(false)
  })
})

describe("str", () => {
  it("空串、非字符串都归一成 undefined，好让 ?? 回退生效", () => {
    expect(str("x")).toBe("x")
    for (const value of ["", null, undefined, 3, [], {}, false]) expect(str(value)).toBeUndefined()
  })
})

describe("isNum", () => {
  it("只认有限数字", () => {
    expect(isNum(0)).toBe(true)
    expect(isNum(-1.5)).toBe(true)
    for (const value of [NaN, Infinity, -Infinity, "1", null]) expect(isNum(value)).toBe(false)
  })
})

describe("arrayFrom", () => {
  it("非数组归一成空数组", () => {
    expect(arrayFrom([{ a: 1 }] as unknown[])).toHaveLength(1)
    expect(arrayFrom({ not: "array" })).toEqual([])
    expect(arrayFrom(null)).toEqual([])
  })
})

describe("parseArgs", () => {
  it("非字符串原样返回", () => {
    expect(parseArgs({ a: 1 })).toEqual({ value: { a: 1 } })
    expect(parseArgs(null)).toEqual({ value: null })
  })

  it("空字符串当作「没有参数」", () => {
    expect(parseArgs("")).toEqual({ value: "" })
  })

  it("合法 JSON 解析成对象", () => {
    expect(parseArgs("{\"a\":1}")).toEqual({ value: { a: 1 } })
  })

  it("半截 JSON 保留原文并打标记：流式尾部常见，不能丢信息", () => {
    expect(parseArgs("{\"a\":")).toEqual({ value: "{\"a\":", error: true })
  })
})

describe("argsOf", () => {
  it("字符串与空值原样返回", () => {
    expect(argsOf("{\"a\":1}")).toBe("{\"a\":1}")
    expect(argsOf(undefined)).toBe(undefined)
    expect(argsOf(null)).toBe(null)
  })

  it("对象统一转成 JSON 字符串，装配器才不会把它拆开", () => {
    expect(argsOf({ a: 1 })).toBe("{\"a\":1}")
  })

  it("循环引用回退成 String()，不让 stringify 抛异常", () => {
    const circular: Record<string, unknown> = {}
    circular.self = circular
    expect(argsOf(circular)).toBe("[object Object]")
  })
})

describe("toChunk", () => {
  it("对象直接给，数组与标量不算 JSON 对象", () => {
    expect(toChunk({ a: 1 })).toEqual({ a: 1 })
    expect(toChunk([1, 2])).toBe(null)
    expect(toChunk(null)).toBe(null)
    expect(toChunk(3)).toBe(null)
  })

  it("JSON 对象字符串解析成对象", () => {
    expect(toChunk("{\"a\":1}")).toEqual({ a: 1 })
  })

  it("JSON 字符串解析成字符串：Responses 的 arguments 之类不是对象的取值", () => {
    expect(toChunk("{\"a\":1}")).toEqual({ a: 1 })
    expect(toChunk(JSON.stringify("x"))).toBe("x")
  })

  it("坏 JSON 与空字符串返回 null", () => {
    expect(toChunk("{坏的")).toBe(null)
    expect(toChunk("")).toBe(null)
    expect(toChunk("[]")).toBe(null)
    expect(toChunk("3")).toBe(null)
  })
})
