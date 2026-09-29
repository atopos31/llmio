import { render, screen } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { describe, expect, it } from "vitest"

import { JsonTree } from "@/components/json-tree"

describe("JsonTree · 标量", () => {
  it("字符串带引号，数字/布尔/null 不带", () => {
    render(<JsonTree data={{ s: "abc", n: 42, b: true, z: null }} defaultDepth={9} />)
    expect(screen.getByText('"abc"')).toBeInTheDocument()
    expect(screen.getByText("42")).toBeInTheDocument()
    expect(screen.getByText("true")).toBeInTheDocument()
    expect(screen.getByText("null")).toBeInTheDocument()
  })

  it("null 与布尔不重复标注类型（字面量已说明类型）", () => {
    render(<JsonTree data={{ z: null, b: true }} defaultDepth={9} />)
    // 各只出现一次：值本身
    expect(screen.getAllByText("null")).toHaveLength(1)
    expect(screen.getAllByText("true")).toHaveLength(1)
  })

  it("每种类型都带文字提示（不依赖颜色）", () => {
    // 颜色在色盲下不可依赖，因此每种类型同时给一个可读的类型名
    render(<JsonTree data={{ s: "x", n: 1 }} defaultDepth={9} />)
    expect(screen.getAllByText("string").length).toBeGreaterThan(0)
    expect(screen.getAllByText("number").length).toBeGreaterThan(0)
  })

  it("键名可见", () => {
    render(<JsonTree data={{ hello: 1 }} defaultDepth={9} />)
    expect(screen.getByText("hello")).toBeInTheDocument()
  })
})

describe("JsonTree · 容器", () => {
  it("超出默认深度时折起，并显示元素个数", () => {
    render(<JsonTree data={{ a: { b: 1, c: 2 } }} defaultDepth={1} />)
    // 外层展开，内层折起
    expect(screen.getByText("2 个键")).toBeInTheDocument()
    expect(screen.queryByText("b")).not.toBeInTheDocument()
  })

  it("数组折起时显示项数", () => {
    render(<JsonTree data={{ list: [1, 2, 3] }} defaultDepth={1} />)
    expect(screen.getByText("3 项")).toBeInTheDocument()
  })

  it("点击可展开", async () => {
    const user = userEvent.setup()
    render(<JsonTree data={{ a: { b: "hidden" } }} defaultDepth={1} />)
    expect(screen.queryByText('"hidden"')).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: /展开 a/ }))
    expect(screen.getByText('"hidden"')).toBeInTheDocument()
  })

  it("展开后可再次折叠", async () => {
    const user = userEvent.setup()
    render(<JsonTree data={{ a: { b: 1 } }} defaultDepth={9} />)
    const btn = screen.getByRole("button", { name: /折叠 a/ })

    await user.click(btn)
    expect(screen.getByRole("button", { name: /展开 a/ })).toBeInTheDocument()
  })

  it("aria-expanded 反映折叠状态（键盘与读屏可用）", async () => {
    const user = userEvent.setup()
    render(<JsonTree data={{ a: { b: 1 } }} defaultDepth={1} />)
    const btn = screen.getByRole("button", { name: /展开 a/ })
    expect(btn).toHaveAttribute("aria-expanded", "false")

    await user.click(btn)
    expect(screen.getByRole("button", { name: /折叠 a/ })).toHaveAttribute("aria-expanded", "true")
  })

  it("空对象与空数组自身不带折叠按钮（无可折叠内容）", () => {
    render(<JsonTree data={{ o: {}, a: [] }} defaultDepth={9} />)
    // 根节点有 2 个键，因此它自己有一个按钮；空容器不应再各带一个
    expect(screen.getAllByRole("button")).toHaveLength(1)
  })
})

describe("JsonTree · 边界", () => {
  it("顶层标量也能渲染", () => {
    render(<JsonTree data={123} />)
    expect(screen.getByText("123")).toBeInTheDocument()
  })

  it("顶层 null 也能渲染", () => {
    render(<JsonTree data={null} />)
    expect(screen.getByText("null")).toBeInTheDocument()
  })

  it("深层嵌套不抛错", () => {
    let deep: unknown = 1
    for (let i = 0; i < 20; i++) deep = { [`k${i}`]: deep }
    expect(() => render(<JsonTree data={deep} defaultDepth={30} />)).not.toThrow()
  })

  it("数组下标作为键名显示", () => {
    render(<JsonTree data={["a", "b"]} defaultDepth={9} />)
    expect(screen.getByText("0")).toBeInTheDocument()
    expect(screen.getByText("1")).toBeInTheDocument()
  })
})
