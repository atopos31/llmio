import { render, screen, waitFor } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import HomePage from "@/routes/home"
import { getStats, type GroupStat, type StatsResult } from "@/lib/api"

// 本页只有一个数据来源，在这里切断即断开了它的全部 IO。
vi.mock("@/lib/api", () => ({ getStats: vi.fn() }))

const mocked = { getStats: vi.mocked(getStats) }

/**
 * 总览页的四态测试（方案 §5.2 的 C 层）。
 *
 * 这一页的缺陷比别的页更难看出来：取数失败只弹一条会自己消失的 toast，
 * `stats` 保持 null，于是 `hasData` 为假，页面显示"该时间范围内没有请求"——
 * 把"没取到"写成了"确实没有"。而这是打开控制台的第一屏，那句话是对服务端
 * 事实的断言。另一处是切时间范围时失败：旧范围的数字留在页面上，没有任何
 * 迹象表明它不是刚切过去的那个范围。
 */

function stats(over: Partial<StatsResult> = {}): StatsResult {
  return {
    generatedAt: 1_700_000_000_000,
    bucketMs: 3_600_000,
    truncated: false,
    range: { from: 1_699_000_000, to: 1_700_000_000 },
    kpi: {
      total: 10,
      success: 9,
      failed: 1,
      running: 0,
      finished: 10,
      successRate: 90,
      promptTokens: 1000,
      completionTokens: 500,
      totalTokens: 1500,
      cachedTokens: 250,
      cacheHitRate: 25,
      cost: 0.12,
      currency: "CNY",
      totalRetries: 2,
      avgRetries: 0.2,
    },
    trend: [
      { ts: 1_699_000_000_000, total: 10, success: 9, error: 1, running: 0, tokens: 1500, prompt: 1000, completion: 500, cached: 250, avgTps: 40, avgFirstChunkMs: 1200 },
    ],
    byModel: [group("deepseek-chat")],
    byProvider: [group("scnet")],
    byKey: [group("dev-key")],
    byName: [group("claude-code")],
    byUa: [group("curl/8.0")],
    errors: [],
    errorTrend: [],
    latency: {
      firstChunk: latency([900, 1200, 3000]),
      tps: latency([40]),
      proxyMs: latency([1500]),
    },
    topTps: [],
    slowest: [],
    recentErrors: [],
    ...over,
  }
}

function group(name: string): GroupStat {
  return {
    name,
    total: 10,
    success: 9,
    error: 1,
    running: 0,
    successRate: 90,
    prompt: 1000,
    completion: 500,
    totalTokens: 1500,
    cached: 250,
    cacheHitRate: 25,
    avgTps: 40,
    maxTps: 88,
    avgFirstChunkMs: 1200,
    p95FirstChunkMs: 3000,
    avgProxyMs: 1500,
    retries: 2,
    cost: 0.12,
  }
}

function latency(list: number[]) {
  const last = list[list.length - 1] ?? 0
  return { p50: list[0] ?? 0, p90: last, p95: last, p99: last, avg: last, max: last, list }
}

/** 该时间范围内没有请求 */
const EMPTY = "该时间范围内没有请求"

function renderPage() {
  return render(
    <MemoryRouter>
      <HomePage />
    </MemoryRouter>
  )
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getStats.mockResolvedValue(stats())
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("总览页 · 加载", () => {
  it("请求未落地时说正在加载，不说这里没有请求", () => {
    mocked.getStats.mockReturnValue(new Promise(() => {}))
    renderPage()

    expect(screen.getByRole("status")).toHaveTextContent("正在加载系统概览")
    expect(screen.queryByText(EMPTY)).not.toBeInTheDocument()
  })
})

describe("总览页 · 空", () => {
  it("确实没有流量时才说没有请求，并给出去处", async () => {
    mocked.getStats.mockResolvedValue(
      stats({ kpi: { ...stats().kpi, total: 0, success: 0, failed: 0 } })
    )
    renderPage()

    expect(await screen.findByText(EMPTY)).toBeInTheDocument()
    expect(screen.getByText("换一个时间范围，或确认网关是否已有流量")).toBeInTheDocument()
  })
})

describe("总览页 · 错误", () => {
  it("取数失败给出原文与重试，而不是说这里没有请求", async () => {
    mocked.getStats.mockRejectedValue(new Error("聚合服务不可用"))
    renderPage()

    expect(await screen.findByText("读取统计失败")).toBeInTheDocument()
    expect(screen.getByText("聚合服务不可用")).toBeInTheDocument()
    // 空态那句话是关于服务端事实的断言，一次失败的请求没资格替它说
    expect(screen.queryByText(EMPTY)).not.toBeInTheDocument()
  })

  it("重试后按取到的数据重新渲染", async () => {
    mocked.getStats.mockRejectedValueOnce(new Error("聚合服务不可用"))
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("读取统计失败")
    mocked.getStats.mockClear()
    await user.click(screen.getByRole("button", { name: "重试" }))

    await waitFor(() => expect(mocked.getStats).toHaveBeenCalledTimes(1))
    expect(await screen.findByText("请求数")).toBeInTheDocument()
    expect(screen.queryByText("读取统计失败")).not.toBeInTheDocument()
  })

  it("已有数据时刷新失败不清屏，但说清下面是上一次的结果", async () => {
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByText("请求数")).toBeInTheDocument()

    // 切时间范围：这一次失败
    mocked.getStats.mockRejectedValue(new Error("聚合服务不可用"))
    await user.click(screen.getByRole("radio", { name: "近 7 天" }))

    expect(await screen.findByText("这次没取到，下面是上一次的结果")).toBeInTheDocument()
    expect(screen.getByText("聚合服务不可用")).toBeInTheDocument()
    // 旧数据仍在，但读者已经知道它不是这次切过去的那个范围
    expect(screen.getByText("请求数")).toBeInTheDocument()
  })
})

describe("总览页 · 有数据", () => {
  it("给出主视觉在途数、KPI 与两张趋势图", async () => {
    renderPage()

    expect(await screen.findByText("请求数")).toBeInTheDocument()
    expect(screen.getByText("在途请求")).toBeInTheDocument()
    expect(screen.getByText("成功率")).toBeInTheDocument()
    expect(screen.getByText("请求趋势")).toBeInTheDocument()
    expect(screen.queryByText(EMPTY)).not.toBeInTheDocument()
  })

  it("截断时明确告知数字可能不完整", async () => {
    mocked.getStats.mockResolvedValue(stats({ truncated: true }))
    renderPage()

    expect(await screen.findByText(/统计可能不完整/)).toBeInTheDocument()
  })
})
