import { act, render, screen, waitFor } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import HomePage from "@/routes/home"
import {
  getLogById,
  getStats,
  type ChatLog,
  type ErrorGroup,
  type GroupStat,
  type StatsLogRow,
  type StatsResult,
} from "@/lib/api"
import { UI_PREFS_STORAGE_KEY } from "@/lib/prefs"

// 本页只有两个数据来源：概览聚合，以及错误样本点开时按 id 取的那一条日志。
vi.mock("@/lib/api", () => ({ getStats: vi.fn(), getLogById: vi.fn() }))

const mocked = { getStats: vi.mocked(getStats), getLogById: vi.mocked(getLogById) }

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
      { ts: 1_699_000_000_000, total: 10, success: 9, error: 1, running: 0, tokens: 1500, prompt: 1000, completion: 500, cached: 250, cost: 0.05, avgTps: 40, avgFirstChunkMs: 1200 },
    ],
    byModel: [group("deepseek-chat")],
    byModelProvider: [],
    byProviderModel: [],
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

/** 错误类别。样本是后端的 Truncate 截断后的片段，正文比它长。 */
function errorGroup(over: Partial<ErrorGroup> = {}): ErrorGroup {
  return {
    type: "上游 5xx",
    code: "5xx",
    count: 3,
    providers: [{ name: "openai", count: 3 }],
    models: [{ name: "claude-sonnet-4", count: 3 }],
    samples: [{ id: 21, createdAt: 1_700_000_000_000, error: "upstream unavailable" }],
    ...over,
  }
}

/** 排行榜一行。`requestName` 与 `model` 是同一个东西的两个名字，可分别给。 */
function leaderRow(over: Partial<StatsLogRow> = {}): StatsLogRow {
  return {
    id: 7,
    createdAt: 1_700_000_000_000,
    model: "claude-sonnet-4",
    requestName: "claude-code",
    provider: "openai",
    keyName: "dev-key",
    tps: 88.5,
    firstChunkMs: 3200,
    completionTokens: 512,
    promptTokens: 128,
    error: "upstream unavailable: connection reset by peer",
    retry: 0,
    ...over,
  }
}

/** 失败请求：有错误原文，没有请求体。 */
function failedLog(over: Partial<ChatLog> = {}): ChatLog {
  return {
    ID: 21,
    CreatedAt: "2026-10-15T10:00:00+08:00",
    Name: "claude-code",
    TraceID: "trace-err",
    ProviderModel: "claude-sonnet-4",
    ProviderName: "openai",
    Status: "error",
    Style: "openai",
    UserAgent: "claude-cli/2.1",
    Error: "upstream unavailable: connection reset by peer",
    Retry: 2,
    ProxyTime: 1_000_000_000,
    FirstChunkTime: 0,
    ChunkTime: 2_000_000_000,
    Tps: 0,
    ChatIO: false,
    Size: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    prompt_tokens_details: { cached_tokens: 0 },
    key_name: "dev-key",
    input_price: 0,
    cache_read_price: 0,
    output_price: 0,
    currency: "CNY",
    ...over,
  }
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

/**
 * 刷新反馈。
 *
 * 轮询是静默的，画面上没有任何东西在动，于是读者只能得出"点了刷新按钮才刷"
 * 的结论——计时器确实在跑，但**没有证据**。这两条测试钉住证据本身：
 * 时间戳要跟着每一轮落地往前走（静默轮询也算），在飞的那一轮要让图标转起来。
 */
describe("总览页 · 刷新反馈", () => {
  it("取到数据后给出更新时间，精确到秒", async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 16, 9, 5, 3))
    try {
      renderPage()
      await act(async () => {})

      // 秒级而不是分钟级：stats 是全量聚合，10 秒档下取回同一份数据很常见，
      // 分钟粒度下这一轮到底取没取成根本看不出来
      expect(screen.getByText("更新于 09:05:03")).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it("自动刷新落地后时间戳跟着走，这就是它在跑的证据", async () => {
    localStorage.setItem(
      UI_PREFS_STORAGE_KEY,
      JSON.stringify({ refresh: { home: 10 } })
    )
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 16, 9, 5, 3))
    try {
      renderPage()
      await act(async () => {})
      expect(screen.getByText("更新于 09:05:03")).toBeInTheDocument()

      // 从 09:05:03 起拨 10 秒，落地的那次取数记的就是 09:05:13
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_000)
      })
      // 计时器只负责"发起"，静默取数的落地还差一个微任务回合：
      // 多冲一次才轮到 setState 重渲染
      await act(async () => {})

      expect(mocked.getStats).toHaveBeenCalledTimes(2)
      // 静默取数不落错误条、不闪骨架，但时间戳必须动
      expect(screen.getByText("更新于 09:05:13")).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it("取数在飞时刷新图标转起来，落地后停下", async () => {
    let settle: (v: StatsResult) => void = () => {}
    mocked.getStats.mockReturnValue(
      new Promise<StatsResult>((resolve) => {
        settle = resolve
      })
    )
    renderPage()

    const button = screen.getByRole("button", { name: "刷新概览" })
    await waitFor(() => expect(button).toBeDisabled())
    expect(button.querySelector("svg")).toHaveClass("animate-spin")
    expect(screen.queryByText(/^更新于 /)).not.toBeInTheDocument()

    await act(async () => {
      settle(stats())
    })

    expect(button).toBeEnabled()
    expect(button.querySelector("svg")).not.toHaveClass("animate-spin")
    expect(screen.getByText(/^更新于 /)).toBeInTheDocument()
  })
})

describe("总览页 · 自动刷新与偏好", () => {
  it("存档里的时间范围与刷新档位被沿用，静默刷新不闪骨架", async () => {
    // 用户上一回选过"近 30 天 + 每 10 秒"：刷新页面后不该丢
    localStorage.setItem(
      UI_PREFS_STORAGE_KEY,
      JSON.stringify({ homeRange: "last_30d", refresh: { home: 10 } })
    )
    vi.useFakeTimers()
    try {
      renderPage()
      await act(async () => {})

      expect(screen.getByRole("radio", { name: "近 30 天" })).toHaveAttribute(
        "aria-checked",
        "true"
      )
      expect(screen.getByText("请求数")).toBeInTheDocument()
      expect(mocked.getStats).toHaveBeenCalledTimes(1)

      // 到点静默取一次：数字还在，骨架没有闪回来
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_000)
      })
      expect(mocked.getStats).toHaveBeenCalledTimes(2)
      expect(screen.getByText("请求数")).toBeInTheDocument()
      expect(screen.queryByText("正在加载系统概览")).not.toBeInTheDocument()

      // 静默刷新失败不打扰人：不留错误条，也不用空态那句断言
      mocked.getStats.mockRejectedValue(new Error("聚合服务不可用"))
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_000)
      })
      expect(mocked.getStats).toHaveBeenCalledTimes(3)
      expect(screen.queryByText("读取统计失败")).not.toBeInTheDocument()
      expect(screen.getByText("请求数")).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it("改了时间范围会落 localStorage，重新挂载后仍在", async () => {
    const user = userEvent.setup()
    const first = renderPage()
    expect(await screen.findByText("请求数")).toBeInTheDocument()

    await user.click(screen.getByRole("radio", { name: "近 7 天" }))
    const stored = JSON.parse(
      localStorage.getItem(UI_PREFS_STORAGE_KEY) ?? "{}"
    ) as { homeRange: string }
    expect(stored.homeRange).toBe("last_7d")

    // 重新挂载 = 用户刷新了页面
    first.unmount()
    renderPage()
    expect(screen.getByRole("radio", { name: "近 7 天" })).toHaveAttribute(
      "aria-checked",
      "true"
    )
  })

  it("改了刷新档位会落 localStorage", async () => {
    const user = userEvent.setup()
    renderPage()
    expect(await screen.findByText("请求数")).toBeInTheDocument()

    await user.click(screen.getByRole("combobox", { name: "自动刷新" }))
    await user.click(screen.getByRole("option", { name: "每 30 秒" }))

    const stored = JSON.parse(
      localStorage.getItem(UI_PREFS_STORAGE_KEY) ?? "{}"
    ) as { refresh: Record<string, number> }
    expect(stored.refresh.home).toBe(30)
  })
})

/**
 * 排行榜的详情抽屉。
 *
 * 首页显示失败请求的地方有两处：错误分析与"最近失败"。后者原先跳去
 * /logs/:id/chat-io，而失败请求没有请求内容，进去是张空表。三张榜共用
 * 一份实现，所以交互也必须共用——否则同一张卡片点哪张去哪，全看它装的是
 * 哪类数据。能不能看请求内容由抽屉按这条日志自己判断。
 */
describe("总览页 · 排行榜抽屉", () => {
  it("最近失败点开抽屉，不跳去没有请求内容的 chat-io", async () => {
    mocked.getStats.mockResolvedValue(
      stats({ recentErrors: [leaderRow({ id: 21 })] })
    )
    mocked.getLogById.mockResolvedValue(failedLog())
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("最近失败")
    await user.click(screen.getByRole("button", { name: /claude-code/ }))

    expect(mocked.getLogById).toHaveBeenCalledWith(21)
    expect(await screen.findByText("日志详情: 21")).toBeInTheDocument()
    expect(screen.getByText("upstream unavailable: connection reset by peer")).toBeInTheDocument()
    // 失败请求给的是"回日志页"，不是那条通往空表的 chat-io
    expect(screen.getByRole("button", { name: "在日志页查看" })).toBeInTheDocument()
    expect(screen.queryByText("查看请求内容")).not.toBeInTheDocument()
    expect(document.querySelector('a[href^="/logs/"]')).toBeNull()
  })

  it("TPS 榜首与最慢首包同样点开抽屉，交互不随数据而变", async () => {
    mocked.getStats.mockResolvedValue(stats({ topTps: [leaderRow()] }))
    mocked.getLogById.mockResolvedValue(failedLog())
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("TPS 榜首")
    await user.click(screen.getByRole("button", { name: /claude-code/ }))

    expect(mocked.getLogById).toHaveBeenCalledWith(7)
  })

  it("标签请求名优先，上游模型名退到括号里", async () => {
    mocked.getStats.mockResolvedValue(
      stats({ topTps: [leaderRow({ requestName: "claude-code", model: "claude-sonnet-4" })] })
    )
    renderPage()

    await screen.findByText("TPS 榜首")
    const row = screen.getByRole("button", { name: /claude-code/ })
    expect(row).toHaveTextContent("claude-code（claude-sonnet-4） · openai")
  })

  it("别名与上游模型名相同时只写一个名字", async () => {
    mocked.getStats.mockResolvedValue(
      stats({ topTps: [leaderRow({ requestName: undefined, model: "gpt-4o" })] })
    )
    renderPage()

    await screen.findByText("TPS 榜首")
    expect(screen.getByRole("button", { name: /gpt-4o/ })).toHaveTextContent(
      "gpt-4o · openai"
    )
  })
})

/**
 * 错误样本的详情抽屉。
 *
 * 原来这一块的唯一入口是跳到 /logs/:id/chat-io，而失败请求没有 ChatIO，
 * 那条链接进去是空表——也就是"点了没反应"。现在改成原地弹抽屉，
 * 读者不用离开总览就能看清是哪一次、哪个模型、错误原文是什么。
 */
describe("总览页 · 错误样本抽屉", () => {
  it("点开样本按那条日志的 id 取数，抽屉给出错误原文", async () => {
    mocked.getStats.mockResolvedValue(stats({ errors: [errorGroup()] }))
    mocked.getLogById.mockResolvedValue(failedLog())
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("错误分析")
    await user.click(screen.getByRole("button", { name: /upstream unavailable/ }))

    expect(mocked.getLogById).toHaveBeenCalledWith(21)
    expect(await screen.findByText("日志详情: 21")).toBeInTheDocument()
    // 抽屉里有被截断掉的那半句
    expect(screen.getByText("upstream unavailable: connection reset by peer")).toBeInTheDocument()
  })

  it("失败请求没有请求体：不给「查看请求内容」，改给「在日志页查看」", async () => {
    mocked.getStats.mockResolvedValue(stats({ errors: [errorGroup()] }))
    mocked.getLogById.mockResolvedValue(failedLog())
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("错误分析")
    await user.click(screen.getByRole("button", { name: /upstream unavailable/ }))

    expect(await screen.findByRole("button", { name: "在日志页查看" })).toBeInTheDocument()
    expect(screen.queryByText("查看请求内容")).not.toBeInTheDocument()
  })

  it("样本对应的日志已被清理时，说明不存在，而不是留一块空详情", async () => {
    mocked.getStats.mockResolvedValue(stats({ errors: [errorGroup()] }))
    mocked.getLogById.mockResolvedValue(null)
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("错误分析")
    await user.click(screen.getByRole("button", { name: /upstream unavailable/ }))

    expect(await screen.findByText("这条记录不存在，或已被清理。")).toBeInTheDocument()
    expect(screen.queryByText("错误信息")).not.toBeInTheDocument()
  })

  it("取这条日志失败时给原文与哪一步失败了", async () => {
    mocked.getStats.mockResolvedValue(stats({ errors: [errorGroup()] }))
    mocked.getLogById.mockRejectedValue(new Error("数据库不可用"))
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("错误分析")
    await user.click(screen.getByRole("button", { name: /upstream unavailable/ }))

    expect(await screen.findByText("读取这条记录失败。")).toBeInTheDocument()
    expect(screen.getByText("数据库不可用")).toBeInTheDocument()
  })

  it("没有样本的类别不给点开入口", async () => {
    mocked.getStats.mockResolvedValue(stats({ errors: [errorGroup({ samples: [] })] }))
    renderPage()

    await screen.findByText("上游 5xx")
    expect(screen.queryByRole("button", { name: /upstream/ })).not.toBeInTheDocument()
    expect(mocked.getLogById).not.toHaveBeenCalled()
  })
})

