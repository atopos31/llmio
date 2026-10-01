import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { getPeakCalendar, previewPeakTerms, syncPeakHolidays, updatePeakCalendar } from "@/lib/api"
import type { PeakCalendar, PeakHolidaySyncResult, PeakTerms } from "@/lib/peak"

/**
 * 峰谷端点里"全局日历 + 预览"这几个的传输层测试。
 *
 * 这一层只钉一件事，但它是整页最关键的一条：**后端那句话必须活着到达界面**。
 *
 * 保存时后端用 HTTP 200 + code 400 回校验失败，message 里写着是时区还是哪一条
 * 覆盖不合法；同步时要出外网，失败用 HTTP 502 回，原因也只在 message 里。
 * 后者尤其容易丢：本项目通用的 apiRequest 在 `!response.ok` 分支先抛
 * "API request failed: 502 Bad Gateway"，连 body 都不读，用户就只能看到
 * 一句毫无信息量的话。所以 syncPeakHolidays 自己读 body——这段逻辑没有
 * 测试守着，某天被"顺手统一成 apiRequest"就会静默退化。
 *
 * 条款不在这里：它挂在「模型 × 上游」关联上，随 createModelProvider /
 * updateModelProvider 的 peak 字段走（那两个端点已有自己的测试）。
 */

const 日历: PeakCalendar = {
  timezone: "Asia/Shanghai",
  weekdays: [1, 2, 3, 4, 5],
  dateOverrides: { "2026-10-01": "rest", "2026-10-10": "work" },
  holidaySyncedAt: 1759271400,
  holidaySource: "bundled:holidays/2026.json",
}

const 同步结果: PeakHolidaySyncResult = {
  year: 2026,
  count: 2,
  source: "bundled:holidays/2026.json",
  syncedAt: 1759271400,
  calendar: 日历,
}

const 条款: PeakTerms = {
  enabled: true,
  periods: [{ name: "夜间", start: "22:00", end: "06:00", multiplier: 0.25 }],
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
  it("成功时按年份请求并返回服务端已落盘的那份日历", async () => {
    fetchMock.mockResolvedValue(响应(200, { code: 200, message: "ok", data: 同步结果 }))

    await expect(syncPeakHolidays(2026)).resolves.toEqual(同步结果)

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe("/api/peak-calendar/holidays/sync?year=2026")
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

  it("code 200 但没带 data 也算失败，不能把 undefined 当成日历传下去", async () => {
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

describe("日历与预览三个端点", () => {
  it("读日历走 GET，且不带 body", async () => {
    fetchMock.mockResolvedValue(响应(200, { code: 200, message: "ok", data: 日历 }))

    await expect(getPeakCalendar()).resolves.toEqual(日历)
    expect(fetchMock.mock.calls[0][0]).toBe("/api/peak-calendar")
  })

  it("保存失败时把后端的定位信息原样抛出（HTTP 200 + code 400）", async () => {
    fetchMock.mockResolvedValue(
      响应(200, {
        code: 400,
        message: 'invalid date override key "2026-9-1": expected YYYY-MM-DD',
      })
    )

    await expect(updatePeakCalendar(日历)).rejects.toThrow(
      'invalid date override key "2026-9-1": expected YYYY-MM-DD'
    )
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe("/api/peak-calendar")
    expect(init.method).toBe("PUT")
    // 整份覆盖：漏了 dateOverrides 就等于把节假日覆盖清空，
    // 漏了 holidaySyncedAt/holidaySource 就会退化成"从未同步"
    expect(JSON.parse(init.body)).toEqual(日历)
  })

  it("预览把未保存的条款与天数一起发出去", async () => {
    const 预览 = { timezone: "Asia/Shanghai", points: [] }
    fetchMock.mockResolvedValue(响应(200, { code: 200, message: "ok", data: 预览 }))

    await expect(previewPeakTerms(条款, 3)).resolves.toEqual(预览)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe("/api/peak-calendar/preview?days=3")
    expect(init.method).toBe("POST")
    expect(JSON.parse(init.body)).toEqual(条款)
  })

  it("预览的载荷只有条款，没有日历字段", async () => {
    fetchMock.mockResolvedValue(
      响应(200, { code: 200, message: "ok", data: { timezone: "UTC", points: [] } })
    )

    await previewPeakTerms(条款, 1)

    // 后端 PreviewPeakTerms 绑的是 models.PeakTerms：多发的字段会被静默忽略，
    // 于是"预览里明明带上了时区"这种误解会一直留着。这里钉死形状。
    expect(Object.keys(JSON.parse(fetchMock.mock.calls[0][1].body))).toEqual([
      "enabled",
      "periods",
    ])
  })
})
