import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { getPeakPricing, previewPeakPricing, syncPeakHolidays, updatePeakPricing } from "@/lib/api"
import type { PeakHolidaySyncResult, PeakPricing } from "@/lib/peak"

/**
 * 峰谷计费四个端点的传输层测试。
 *
 * 这一层只钉一件事，但它是整页最关键的一条：**后端那句话必须活着到达界面**。
 *
 * 保存时后端用 HTTP 200 + code 400 回校验失败，message 里写着是哪一段的
 * 哪一项不合法；同步时要出外网，失败用 HTTP 502 回，原因也只在 message 里。
 * 后者尤其容易丢：本项目通用的 apiRequest 在 `!response.ok` 分支先抛
 * "API request failed: 502 Bad Gateway"，连 body 都不读，用户就只能看到
 * 一句毫无信息量的话。所以 syncPeakHolidays 自己读 body——这段逻辑没有
 * 测试守着，某天被"顺手统一成 apiRequest"就会静默退化。
 */

const 配置: PeakPricing = {
  enabled: true,
  timezone: "Asia/Shanghai",
  weekdays: [1, 2, 3, 4, 5],
  periods: [{ name: "夜间", start: "22:00", end: "06:00", multiplier: 0.25 }],
  dateOverrides: {},
}

const 同步结果: PeakHolidaySyncResult = {
  year: 2026,
  count: 2,
  source: "bundled:holidays/2026.json",
  syncedAt: 1759271400,
  config: 配置,
}

/** 造一个响应；`json` 抛错用来模拟网关返回的非 JSON 响应体。 */
function 响应(status: number, body: unknown, statusText = "OK") {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText,
    json: async () => {
      if (body instanceof Error) throw body
      return body
    },
  } as unknown as Response
}

const fetchMock = vi.fn()

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock)
  fetchMock.mockClear()
  localStorage.setItem("authToken", "t-1")
})

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe("syncPeakHolidays", () => {
  it("成功时按年份请求并返回服务端已落盘的那份配置", async () => {
    fetchMock.mockResolvedValue(响应(200, { code: 200, message: "ok", data: 同步结果 }))

    await expect(syncPeakHolidays(2026)).resolves.toEqual(同步结果)

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe("/api/peak-pricing/holidays/sync?year=2026")
    expect(init.method).toBe("POST")
    expect(init.headers.Authorization).toBe("Bearer t-1")
  })

  it("HTTP 502 时抛出的是 body 里的原因，不是「502 Bad Gateway」", async () => {
    fetchMock.mockResolvedValue(
      响应(
        502,
        { code: 502, message: "Failed to sync holidays: no holiday data for 2026: remote failed" },
        "Bad Gateway"
      )
    )

    // 这句里的 "no holiday data for 2026" 是用户唯一能据以行动的线索：
    // 换年份、或去查上游。丢掉它就只剩一句 502。
    await expect(syncPeakHolidays(2026)).rejects.toThrow(
      "Failed to sync holidays: no holiday data for 2026: remote failed"
    )
  })

  it("响应体不是 JSON（网关的 HTML 错误页）时退回带状态码的兜底文案", async () => {
    fetchMock.mockResolvedValue(响应(502, new SyntaxError("Unexpected token <"), "Bad Gateway"))

    await expect(syncPeakHolidays(2026)).rejects.toThrow("API request failed: 502 Bad Gateway")
  })

  it("code 200 但没带 data 也算失败，不能把 undefined 当成配置传下去", async () => {
    fetchMock.mockResolvedValue(响应(200, { code: 200, message: "ok" }))

    await expect(syncPeakHolidays(2026)).rejects.toThrow("API request failed: 200 OK")
  })

  it("401 跳登录页，不把未授权说成同步失败", async () => {
    // jsdom 不允许直接赋值 location.href，用 defineProperty 替换整个 location
    Object.defineProperty(window, "location", {
      value: { href: "" },
      configurable: true,
      writable: true,
    })
    fetchMock.mockResolvedValue(响应(401, { code: 401, message: "unauthorized" }))

    await expect(syncPeakHolidays(2026)).rejects.toThrow("Unauthorized")
    expect(window.location.href).toBe("/login")
  })
})

describe("峰谷计费其余三个端点", () => {
  it("读配置走 GET，且不带 body", async () => {
    fetchMock.mockResolvedValue(响应(200, { code: 200, message: "ok", data: 配置 }))

    await expect(getPeakPricing()).resolves.toEqual(配置)
    expect(fetchMock.mock.calls[0][0]).toBe("/api/peak-pricing")
  })

  it("保存失败时把后端的定位信息原样抛出（HTTP 200 + code 400）", async () => {
    fetchMock.mockResolvedValue(
      响应(200, {
        code: 400,
        message: 'period 2 (夜间优惠): start invalid clock "25:00": expected HH:MM',
      })
    )

    await expect(updatePeakPricing(配置)).rejects.toThrow(
      'period 2 (夜间优惠): start invalid clock "25:00": expected HH:MM'
    )
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe("/api/peak-pricing")
    expect(init.method).toBe("PUT")
    // 整份覆盖：漏了 dateOverrides 就等于把节假日覆盖清空
    expect(JSON.parse(init.body)).toEqual(配置)
  })

  it("预览把未保存的表单配置与天数一起发出去", async () => {
    fetchMock.mockResolvedValue(响应(200, { code: 200, message: "ok", data: [] }))

    await expect(previewPeakPricing(配置, 3)).resolves.toEqual([])
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe("/api/peak-pricing/preview?days=3")
    expect(init.method).toBe("POST")
    expect(JSON.parse(init.body)).toEqual(配置)
  })
})
