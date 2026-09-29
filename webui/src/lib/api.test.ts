import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { checkLatestRelease, getVersion } from "@/lib/api"

/**
 * api 客户端核心行为测试。
 *
 * 只覆盖 apiRequest 这条公共通路（所有 38 个端点函数都走它）与唯一绕过它的
 * checkLatestRelease。端点函数本身是同构的 URL/查询串拼接，逐条枚举测试的
 * 收益低于成本；它们的 URL 正确性由页面集成测试与后端联调保证。
 * 这一取舍记录在 vitest.config.ts 的覆盖率说明里。
 */

type FetchArgs = [input: string, init?: RequestInit]

const fetchMock = vi.fn<(...args: FetchArgs) => Promise<Response>>()

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

let originalLocation: Location

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal("fetch", fetchMock)
  localStorage.clear()
  originalLocation = window.location
})

afterEach(() => {
  vi.unstubAllGlobals()
  // 某些用例会替换 window.location（jsdom 的 location 不可直接赋值），
  // 这里确保后续测试拿到的是原始对象
  Object.defineProperty(window, "location", { value: originalLocation, configurable: true, writable: true })
})

describe("apiRequest · 请求构造", () => {
  it("拼接 /api 前缀", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ code: 200, data: "dev" }))
    await getVersion()
    expect(fetchMock.mock.calls[0][0]).toBe("/api/version")
  })

  it("默认带 JSON 内容类型", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ code: 200, data: "dev" }))
    await getVersion()
    const init = fetchMock.mock.calls[0][1]!
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json")
  })

  it("localStorage 有 token 时附上 Bearer 头", async () => {
    localStorage.setItem("authToken", "secret-token")
    fetchMock.mockResolvedValue(jsonResponse({ code: 200, data: "dev" }))

    await getVersion()

    const init = fetchMock.mock.calls[0][1]!
    expect((init.headers as Record<string, string>)["Authorization"]).toBe("Bearer secret-token")
  })

  it("无 token 时不附 Authorization", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ code: 200, data: "dev" }))

    await getVersion()

    const init = fetchMock.mock.calls[0][1]!
    expect((init.headers as Record<string, string>)["Authorization"]).toBeUndefined()
  })
})

describe("apiRequest · 响应处理", () => {
  it("返回信封里的 data，而不是整个响应体", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ code: 200, message: "success", data: "v1.2.3" }))
    await expect(getVersion()).resolves.toBe("v1.2.3")
  })

  it("业务码非 200 时抛出信封里的 message", async () => {
    // 后端的 BadRequest / NotFound 以 HTTP 200 + 业务码返回，
    // 因此只判断 HTTP 状态会漏掉这些错误
    fetchMock.mockResolvedValue(jsonResponse({ code: 400, message: "参数不合法" }))
    await expect(getVersion()).rejects.toThrow("参数不合法")
  })

  it("HTTP 非 2xx 时抛出状态信息", async () => {
    fetchMock.mockResolvedValue(new Response("boom", { status: 500, statusText: "Server Error" }))
    await expect(getVersion()).rejects.toThrow(/500/)
  })

  it("401 时跳转登录页并抛 Unauthorized", async () => {
    // jsdom 不允许直接赋值 location.href，用 defineProperty 替换整个 location
    const replace = vi.fn()
    Object.defineProperty(window, "location", {
      value: { href: "", replace },
      configurable: true,
      writable: true,
    })

    fetchMock.mockResolvedValue(new Response("", { status: 401 }))

    await expect(getVersion()).rejects.toThrow("Unauthorized")
    // 后端必须返回真实 HTTP 401 才会走到这里，这也是它用 ErrorWithHttpStatus
    // 而非统一信封的原因
    expect(window.location.href).toBe("/login")
  })

  it("401 优先于业务码判断", async () => {
    Object.defineProperty(window, "location", {
      value: { href: "" },
      configurable: true,
      writable: true,
    })
    fetchMock.mockResolvedValue(jsonResponse({ code: 401, message: "expired" }, 401))

    await expect(getVersion()).rejects.toThrow("Unauthorized")
  })
})

describe("checkLatestRelease", () => {
  it("走 GitHub 公共 API，不带本站 token", async () => {
    localStorage.setItem("authToken", "secret")
    fetchMock.mockResolvedValue(
      jsonResponse({ tag_name: "v1.0.0", html_url: "https://example.com", name: "v1.0.0", published_at: "2026-01-01" })
    )

    await checkLatestRelease("atopos31", "llmio")

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe("https://api.github.com/repos/atopos31/llmio/releases/latest")
    const headers = (init?.headers ?? {}) as Record<string, string>
    expect(headers["Accept"]).toBe("application/vnd.github+json")
    // 绝不能把本站 token 发给第三方
    expect(headers["Authorization"]).toBeUndefined()
  })

  it("失败时返回 null 而不是抛错", async () => {
    // 版本提示是附属功能，离线或限流时不应影响界面
    fetchMock.mockRejectedValue(new Error("network"))
    await expect(checkLatestRelease("a", "b")).resolves.toBeNull()
  })

  it("非 2xx 时返回 null", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 403 }))
    await expect(checkLatestRelease("a", "b")).resolves.toBeNull()
  })
})
