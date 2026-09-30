import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import AnalyticsPage from "@/routes/analytics"
import {
  getAuthKeysList,
  getStats,
  getStatsGranularities,
  type GroupStat,
  type StatsResult,
} from "@/lib/api"
import type { StatsQuery } from "@/lib/api"

// 页面的三个数据来源全部来自这一个模块，在这里切断即断开了页面的全部 IO。
// api.ts 的其余导出（类型与别的端点）保持真实——类型在运行时不存在，
// 而别的端点是别的页面的依赖，与本页无关。
vi.mock("@/lib/api", () => ({
  getStats: vi.fn(),
  getStatsGranularities: vi.fn(),
  getAuthKeysList: vi.fn(),
}))

const mocked = {
  getStats: vi.mocked(getStats),
  getStatsGranularities: vi.mocked(getStatsGranularities),
  getAuthKeysList: vi.mocked(getAuthKeysList),
}

/**
 * 分析页的四态测试（方案 §5.2 的 C 层）。
 *
 * 断言的是**页面在什么条件下说什么话**，而不是排版：加载时不能说"没有数据"、
 * 失败时要给出原文而不是"加载失败"四个字、筛选之后选项不能跟着缩水。
 * 这些是这一页对外承诺的语义，用四个状态各钉一遍。
 */

function group(name: string, over: Partial<GroupStat> = {}): GroupStat {
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
    ...over,
  }
}

function fixture(over: Partial<StatsResult> = {}): StatsResult {
  return {
    generatedAt: 1_760_000_000_000,
    bucketMs: 30 * 60 * 1000,
    truncated: false,
    range: { from: 1_759_900_000, to: 1_760_000_000 },
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
      currency: "USD",
      totalRetries: 2,
      avgRetries: 0.2,
    },
    trend: [
      { ts: 1_759_980_000_000, total: 6, success: 6, error: 0, running: 0, tokens: 900, prompt: 600, completion: 300, cached: 150, avgTps: 40, avgFirstChunkMs: 1200 },
      { ts: 1_759_981_800_000, total: 4, success: 3, error: 1, running: 0, tokens: 600, prompt: 400, completion: 200, cached: 100, avgTps: 40, avgFirstChunkMs: 1200 },
    ],
    byModel: [group("gpt-test")],
    byProvider: [group("prov-a"), group("prov-b")],
    byKey: [group("admin"), group("dev-key")],
    byName: [group("dev")],
    byUa: [group("curl/8.0")],
    errors: [
      {
        type: "限流",
        code: "rate_limit",
        count: 1,
        providers: [{ name: "prov-b", count: 1 }],
        models: [{ name: "gpt-test", count: 1 }],
        samples: [{ id: 42, createdAt: 1_759_981_800_000, error: "429 too many requests" }],
      },
    ],
    errorTrend: [
      { ts: 1_759_980_000_000, total: 0, success: 0, error: 0, running: 0, tokens: 0, prompt: 0, completion: 0, cached: 0, avgTps: 0, avgFirstChunkMs: 0 },
      { ts: 1_759_981_800_000, total: 1, success: 0, error: 1, running: 0, tokens: 0, prompt: 0, completion: 0, cached: 0, avgTps: 0, avgFirstChunkMs: 0 },
    ],
    latency: {
      firstChunk: { p50: 1, p90: 2, p95: 3, p99: 4, avg: 1.5, max: 5, list: [1, 1.2, 2, 3, 5] },
      tps: { p50: 40, p90: 60, p95: 70, p99: 88, avg: 42, max: 88, list: [30, 40, 88] },
      proxyMs: { p50: 1400, p90: 1800, p95: 2000, p99: 2400, avg: 1500, max: 2500, list: [1200, 1500, 2500] },
    },
    topTps: [
      { id: 7, createdAt: 1_759_981_800_000, model: "gpt-test", provider: "prov-a", keyName: "admin", tps: 88, firstChunkMs: 900, completionTokens: 500, promptTokens: 1000, retry: 0 },
    ],
    slowest: [
      { id: 8, createdAt: 1_759_981_800_000, model: "gpt-test", provider: "prov-b", keyName: "admin", tps: 20, firstChunkMs: 5000, completionTokens: 100, promptTokens: 100, retry: 1 },
    ],
    recentErrors: [
      { id: 42, createdAt: 1_759_981_800_000, model: "gpt-test", provider: "prov-b", keyName: "admin", tps: 0, firstChunkMs: 0, completionTokens: 0, promptTokens: 0, error: "429 too many requests", retry: 0 },
    ],
    ...over,
  }
}

function renderPage() {
  return render(
    <MemoryRouter>
      <AnalyticsPage />
    </MemoryRouter>
  )
}

/**
 * 已发出的所有查询串。
 *
 * 按集合而不是"最后一次"断言：筛选生效时页面并行发两个请求（带筛选的与
 * 不带筛选的），完成顺序不保证，取"最后一次"会随版本漂移。
 */
function queries(): StatsQuery[] {
  return mocked.getStats.mock.calls.map((c) => c[0] ?? {})
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getStats.mockResolvedValue(fixture())
  mocked.getStatsGranularities.mockResolvedValue(["5m", "1h", "1d"])
  mocked.getAuthKeysList.mockResolvedValue([{ id: 3, name: "dev-key" }])
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("分析页 · 加载", () => {
  it("请求未落地时不宣称「没有数据」，并声明正在加载", async () => {
    mocked.getStats.mockReturnValue(new Promise(() => {}))
    renderPage()

    expect(screen.getByRole("status")).toHaveTextContent("加载中")
    expect(screen.queryByText("所选范围内没有请求")).not.toBeInTheDocument()
  })
})

describe("分析页 · 空", () => {
  it("切片为空时给出去向而不是空白页", async () => {
    mocked.getStats.mockResolvedValue(
      fixture({ kpi: { ...fixture().kpi, total: 0, success: 0, failed: 0 } })
    )
    renderPage()

    expect(await screen.findByText("所选范围内没有请求")).toBeInTheDocument()
    expect(screen.getByText("换个时间范围，或清除筛选条件")).toBeInTheDocument()
    // 没有数据就不该出现视图切换器——切了也无内容可看
    expect(screen.queryByRole("radiogroup", { name: "视图" })).not.toBeInTheDocument()
  })
})

describe("分析页 · 错误", () => {
  it("给出错误原文并可重试", async () => {
    mocked.getStats.mockRejectedValueOnce(new Error("网络不可达"))
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByText("数据加载失败")).toBeInTheDocument()
    expect(screen.getByText("网络不可达")).toBeInTheDocument()

    mocked.getStats.mockResolvedValue(fixture())
    mocked.getStats.mockClear()
    await user.click(screen.getByRole("button", { name: "重试" }))

    await waitFor(() => expect(mocked.getStats).toHaveBeenCalledTimes(1))
    expect(await screen.findByText("请求趋势")).toBeInTheDocument()
  })
})

describe("分析页 · 有数据", () => {
  it("默认给出趋势读数与两个视图入口", async () => {
    renderPage()

    expect(await screen.findByText("请求趋势")).toBeInTheDocument()
    expect(screen.getByText(/共 10 次，成功 9 · 失败 1 · 在途 0/)).toBeInTheDocument()
    expect(screen.getByText("Token 趋势")).toBeInTheDocument()
  })

  it("截断时明确告知数字可能不完整", async () => {
    mocked.getStats.mockResolvedValue(fixture({ truncated: true }))
    renderPage()

    expect(await screen.findByText(/掃描已達上限|扫描已达上限/)).toBeInTheDocument()
  })

  it("下钻：点名称即按该值筛选，再次点击取消", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("请求趋势")

    await user.click(screen.getByRole("radio", { name: "下钻" }))
    const cell = await screen.findByRole("button", { name: "gpt-test" })

    await user.click(cell)
    expect(cell).toHaveAttribute("aria-pressed", "true")
    await waitFor(() => expect(queries().some((q) => q.model === "gpt-test")).toBe(true))

    // 只看"取消之后"新发出的请求：不清空就一定会看到刚才那次带筛选的调用
    mocked.getStats.mockClear()
    await user.click(cell)
    expect(cell).toHaveAttribute("aria-pressed", "false")
    await waitFor(() => expect(mocked.getStats).toHaveBeenCalled())
    expect(queries().every((q) => q.model === undefined)).toBe(true)
  })

  it("筛选生效时另取一份未加筛选的切片，选项才不会自我缩水", async () => {
    mocked.getStats.mockResolvedValue(fixture())
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("请求趋势")
    // 无筛选时只发一次请求
    expect(mocked.getStats).toHaveBeenCalledTimes(1)

    await user.click(screen.getByRole("radio", { name: "下钻" }))
    await user.click(await screen.findByRole("button", { name: "gpt-test" }))

    await waitFor(() => expect(mocked.getStats).toHaveBeenCalledTimes(3))
    // 一次带筛选、一次不带；顺序不保证（并行发出），因此只看集合
    expect(queries().filter((q) => q.model === "gpt-test")).toHaveLength(1)
    expect(queries().filter((q) => q.model === undefined)).toHaveLength(2)
  })

  it("自定义范围：切过来时预填当前预设的窗口，改了就按新窗口取数", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("请求趋势")

    await user.click(screen.getByRole("radio", { name: "自定义" }))
    const from = screen.getByLabelText("起始时间") as HTMLInputElement
    const to = screen.getByLabelText("结束时间") as HTMLInputElement
    // 预填的是"近 24 小时"：两个端点都被截到分钟，差值仍是整 86400 秒
    expect(new Date(to.value).getTime() - new Date(from.value).getTime()).toBe(24 * 3600 * 1000)

    mocked.getStats.mockClear()
    fireEvent.change(from, { target: { value: "2026-09-28T08:00" } })
    fireEvent.change(to, { target: { value: "2026-09-29T08:00" } })

    await waitFor(() => expect(mocked.getStats).toHaveBeenCalled())
    const expected = String(Math.floor(new Date("2026-09-28T08:00").getTime() / 1000))
    expect(queries().every((q) => q.from === expected)).toBe(true)
  })

  it("自定义范围非法时不发请求，也不把已有视图清空", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("请求趋势")

    await user.click(screen.getByRole("radio", { name: "自定义" }))
    mocked.getStats.mockClear()
    // 把结束时间改到起始之前
    fireEvent.change(screen.getByLabelText("结束时间"), { target: { value: "2000-01-01T00:00" } })

    expect(await screen.findByText("起始必须早于结束")).toBeInTheDocument()
    expect(mocked.getStats).not.toHaveBeenCalled()
    // 正在编辑的半截输入不该把已有内容打成空白
    expect(screen.getByText("请求趋势")).toBeInTheDocument()
  })

  it("密钥筛选传 id 而不传展示名", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("请求趋势")

    await user.click(screen.getByRole("button", { name: "密钥" }))
    await user.click(await screen.findByRole("checkbox", { name: "dev-key" }))

    await waitFor(() => expect(queries().some((q) => q.key_id === "3")).toBe(true))
    expect(queries().every((q) => (q as { key?: string }).key === undefined)).toBe(true)
  })
})
