import { afterEach, describe, expect, it, vi } from "vitest"

import { cn, copyToClipboard } from "@/lib/utils"

describe("cn", () => {
  it("合并类名", () => {
    expect(cn("a", "b")).toBe("a b")
  })

  it("忽略假值", () => {
    const off = false
    expect(cn("a", off && "b", undefined, null, "c")).toBe("a c")
  })

  it("支持条件对象", () => {
    expect(cn("base", { on: true, off: false })).toBe("base on")
  })

  it("后写的同类工具类覆盖先写的", () => {
    // 这正是用 tailwind-merge 而非单纯拼接的原因：
    // 拼接会让两个冲突的类同时存在，最终生效的取决于 CSS 源码顺序而非调用顺序
    expect(cn("p-2", "p-4")).toBe("p-4")
    expect(cn("text-red-500", "text-blue-500")).toBe("text-blue-500")
  })

  it("保留不冲突的类", () => {
    expect(cn("p-2", "m-4")).toBe("p-2 m-4")
  })
})

describe("copyToClipboard", () => {
  const originalClipboard = navigator.clipboard
  const originalSecure = Object.getOwnPropertyDescriptor(window, "isSecureContext")

  afterEach(() => {
    Object.defineProperty(navigator, "clipboard", {
      value: originalClipboard,
      configurable: true,
      writable: true,
    })
    if (originalSecure) {
      Object.defineProperty(window, "isSecureContext", originalSecure)
    }
    vi.restoreAllMocks()
  })

  function setClipboard(value: unknown, secure = true) {
    Object.defineProperty(navigator, "clipboard", { value, configurable: true, writable: true })
    Object.defineProperty(window, "isSecureContext", {
      value: secure,
      configurable: true,
      writable: true,
    })
  }

  it("安全上下文下走 Clipboard API", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    setClipboard({ writeText })

    await copyToClipboard("hello")

    expect(writeText).toHaveBeenCalledWith("hello")
  })

  it("非安全上下文（HTTP）降级为 execCommand", async () => {
    // 这是为本项目实际存在过的缺陷加的：HTTP 部署下 Clipboard API 不可用，
    // 复制按钮会整体失效。
    const writeText = vi.fn().mockResolvedValue(undefined)
    setClipboard({ writeText }, false)
    const exec = vi.fn().mockReturnValue(true)
    document.execCommand = exec

    await copyToClipboard("hello")

    expect(writeText).not.toHaveBeenCalled()
    expect(exec).toHaveBeenCalledWith("copy")
  })

  it("Clipboard API 抛错时降级", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("denied"))
    setClipboard({ writeText })
    const exec = vi.fn().mockReturnValue(true)
    document.execCommand = exec

    await copyToClipboard("hello")

    expect(exec).toHaveBeenCalledWith("copy")
  })

  it("navigator.clipboard 不存在时降级", async () => {
    setClipboard(undefined)
    const exec = vi.fn().mockReturnValue(true)
    document.execCommand = exec

    await copyToClipboard("hello")

    expect(exec).toHaveBeenCalledWith("copy")
  })

  it("降级路径使用临时 textarea 且用后清理", async () => {
    setClipboard(undefined)
    document.execCommand = vi.fn().mockReturnValue(true)

    const before = document.body.children.length
    await copyToClipboard("payload")

    // 临时节点必须移除，否则每复制一次就往 DOM 里留一个
    expect(document.body.children.length).toBe(before)
    expect(document.querySelector("textarea")).toBeNull()
  })

  it("execCommand 返回 false 时抛错", async () => {
    setClipboard(undefined)
    document.execCommand = vi.fn().mockReturnValue(false)

    await expect(copyToClipboard("x")).rejects.toThrow(/copy failed/)
  })

  it("execCommand 抛错时仍清理临时节点", async () => {
    setClipboard(undefined)
    document.execCommand = vi.fn().mockImplementation(() => {
      throw new Error("boom")
    })

    const before = document.body.children.length
    await expect(copyToClipboard("x")).rejects.toThrow("boom")
    // finally 块保证清理，不会因异常泄漏节点
    expect(document.body.children.length).toBe(before)
  })
})
