import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ConfigPage from "@/routes/config"
import {
  configAPI,
  getCleanupHistory,
  getPeakPricing,
  type AnthropicCountTokens,
  type LogCleanupPolicy,
  type LogCleanupRecord,
} from "@/lib/api"

// 这一页的数据全部经过 api.ts，切断它就断开了本页的全部 IO。
// configAPI 是个对象，mock 工厂里得整个换掉（测试里再取 getConfig/updateConfig）。
vi.mock("@/lib/api", () => ({
  configAPI: {
    getConfig: vi.fn(),
    updateConfig: vi.fn(),
  },
  getCleanupHistory: vi.fn(),
  testCountTokens: vi.fn(),
  // 峰谷计费卡片自带取数，挂在这一页上。不给它默认值的话，它会渲染成
  // 错误态并多出一个"重试"按钮，把这一页原有的重试断言搅成"找到多个"
  getPeakPricing: vi.fn(),
}))

const mocked = {
  getConfig: vi.mocked(configAPI.getConfig),
  updateConfig: vi.mocked(configAPI.updateConfig),
  getCleanupHistory: vi.mocked(getCleanupHistory),
  getPeakPricing: vi.mocked(getPeakPricing),
}

/**
 * 系统配置页的四态测试（方案 §5.2 的 C 层）。
 *
 * 这一页的失败态与另三个列表页不同：卡片上显示的是默认值，编辑与保存本身
 * 不依赖这次读取，因此失败不作整页替换，只在顶部说明"下面显示的可能不是
 * 已保存的值"。断言因此钉两件事——失败要说出来，且页面仍可用。
 *
 * 后端对"这个 key 不存在"返回的是 200 + 空 value，与请求失败是两回事：
 * 前者照旧说"未配置"是对的，后者不能说。
 */
function config(value: unknown) {
  return { key: "k", value: value === "" ? "" : JSON.stringify(value) }
}

function historyRecord(over: Partial<LogCleanupRecord> = {}): LogCleanupRecord {
  return {
    ID: 1,
    CreatedAt: "2026-09-01T03:00:00Z",
    Source: "scheduled",
    Type: "days",
    RetentionDays: 30,
    DeletedCount: 12,
    DurationMs: 42,
    ...over,
  }
}

function historyPage(records: LogCleanupRecord[]) {
  return { data: records, total: records.length, page: 1, page_size: 10, pages: 1 }
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ConfigPage />
    </MemoryRouter>
  )
}

/** 让两个 getConfig 分别返回给定值；不传即"未配置" */
function configReplies(
  anthropic: AnthropicCountTokens | "" = "",
  cleanup: Partial<LogCleanupPolicy> | "" = ""
) {
  mocked.getConfig.mockImplementation(async (key: string) =>
    key === "anthropic_count_tokens" ? config(anthropic) : config(cleanup)
  )
}

beforeEach(async () => {
  vi.clearAllMocks()
  configReplies()
  mocked.getCleanupHistory.mockResolvedValue(historyPage([]))
  // 峰谷计费卡片在本页每次渲染都会取一次数；这里给服务端的默认配置，
  // 卡片显示"未开启"，这一页的其它断言不受它影响
  mocked.getPeakPricing.mockResolvedValue({
    enabled: false,
    timezone: "Asia/Shanghai",
    weekdays: [1, 2, 3, 4, 5],
    periods: [],
    dateOverrides: {},
  })
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("系统配置页 · 加载", () => {
  it("读取未落地时声明正在加载，不说未配置", async () => {
    mocked.getConfig.mockReturnValue(new Promise(() => {}))

    renderPage()

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("加载中"))
    expect(screen.queryByText("未配置")).not.toBeInTheDocument()
  })
})

describe("系统配置页 · 空", () => {
  it("从未配置过时逐项说未配置，并用默认值填充另一张卡", async () => {
    renderPage()

    // base_url / api_key / version 三项都没有
    expect(await screen.findAllByText("未配置")).toHaveLength(3)
    expect(screen.getByText("未启用")).toBeInTheDocument()
    expect(screen.getByText("保留最近 30 天")).toBeInTheDocument()
  })
})

describe("系统配置页 · 错误", () => {
  it("读取失败时在顶部说明，并说清下面显示的可能不是已保存的值", async () => {
    mocked.getConfig.mockRejectedValueOnce(new Error("500 internal error"))

    renderPage()

    expect(await screen.findByText("读取现有配置失败")).toBeInTheDocument()
    expect(screen.getByText("下面显示的可能不是已保存的值，保存前请先重试读取。")).toBeInTheDocument()
    expect(screen.getByText("500 internal error")).toBeInTheDocument()
    // 不整页替换：卡片与编辑入口照旧可用
    expect(screen.getByText("Anthropic 令牌计数配置")).toBeInTheDocument()
  })

  it("重试后按已保存的值重新渲染，提示随之消失", async () => {
    const user = userEvent.setup()
    mocked.getConfig.mockRejectedValueOnce(new Error("500 internal error"))
    renderPage()
    await screen.findByText("读取现有配置失败")

    configReplies(
      { base_url: "https://saved.example.com", api_key: "sk-ant-api03-secret", version: "2023-06-01" },
      { enabled: true, retention_days: 7 }
    )
    await user.click(screen.getByRole("button", { name: "重试" }))

    expect(await screen.findByText("https://saved.example.com")).toBeInTheDocument()
    expect(screen.queryByText("读取现有配置失败")).not.toBeInTheDocument()
  })

  it("存进去的配置坏了（JSON 解析不出来）也算失败，不静默当成未配置", async () => {
    mocked.getConfig.mockImplementation(async (key: string) => ({
      key,
      value: key === "anthropic_count_tokens" ? "{ 这不是 JSON" : "",
    }))

    renderPage()

    expect(await screen.findByText("读取现有配置失败")).toBeInTheDocument()
  })

  it("清理历史取数失败时给出原文与重试，而不是说暂无数据", async () => {
    const user = userEvent.setup()
    mocked.getCleanupHistory.mockRejectedValueOnce(new Error("历史服务不可用"))
    configReplies()
    renderPage()
    await screen.findAllByText("未配置")

    await user.click(screen.getByRole("button", { name: "查看清理历史" }))

    const dialog = await screen.findByRole("dialog")
    expect(await within(dialog).findByText("清理历史加载失败")).toBeInTheDocument()
    expect(within(dialog).getByText("历史服务不可用")).toBeInTheDocument()
    expect(within(dialog).queryByText("暂无数据")).not.toBeInTheDocument()
  })

  it("清理历史重试后就地恢复成表", async () => {
    const user = userEvent.setup()
    mocked.getCleanupHistory.mockRejectedValueOnce(new Error("历史服务不可用"))
    renderPage()
    await screen.findAllByText("未配置")
    await user.click(screen.getByRole("button", { name: "查看清理历史" }))
    const dialog = await screen.findByRole("dialog")
    await within(dialog).findByText("清理历史加载失败")

    mocked.getCleanupHistory.mockResolvedValue(historyPage([historyRecord()]))
    await user.click(within(dialog).getByRole("button", { name: /重试/ }))

    expect(await within(dialog).findByText("12")).toBeInTheDocument()
    expect(within(dialog).queryByText("清理历史加载失败")).not.toBeInTheDocument()
  })
})

describe("系统配置页 · 有数据", () => {
  it("已保存的值逐项显示，密钥只露前八位", async () => {
    configReplies(
      { base_url: "https://saved.example.com", api_key: "sk-ant-api03-secret", version: "2023-06-01" },
      { enabled: true, retention_days: 7 }
    )

    renderPage()

    expect(await screen.findByText("https://saved.example.com")).toBeInTheDocument()
    expect(screen.getByText("sk-ant-a...")).toBeInTheDocument()
    // 整串不出现在页面上
    expect(screen.queryByText("sk-ant-api03-secret")).not.toBeInTheDocument()
    expect(screen.getByText("已启用")).toBeInTheDocument()
    expect(screen.getByText("保留最近 7 天")).toBeInTheDocument()
  })

  it("编辑后保存，提交的是对话框里的那一份", async () => {
    const user = userEvent.setup()
    configReplies(
      { base_url: "https://old.example.com", api_key: "sk-ant-api03-secret", version: "2023-06-01" }
    )
    mocked.updateConfig.mockResolvedValue({ key: "anthropic_count_tokens", value: "" })

    renderPage()
    await screen.findByText("https://old.example.com")
    await user.click(screen.getAllByRole("button", { name: "编辑配置" })[0])
    const dialog = await screen.findByRole("dialog")

    const baseUrl = within(dialog).getByDisplayValue("https://old.example.com")
    await user.clear(baseUrl)
    await user.type(baseUrl, "https://new.example.com")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() =>
      expect(mocked.updateConfig).toHaveBeenCalledWith("anthropic_count_tokens", {
        base_url: "https://new.example.com",
        api_key: "sk-ant-api03-secret",
        version: "2023-06-01",
      })
    )
  })

  it("清理历史按执行记录列出触发方式与删除数", async () => {
    const user = userEvent.setup()
    mocked.getCleanupHistory.mockResolvedValue(
      historyPage([historyRecord({ DeletedCount: 128, DurationMs: 0 })])
    )
    configReplies()

    renderPage()
    await screen.findAllByText("未配置")
    await user.click(screen.getByRole("button", { name: "查看清理历史" }))

    const dialog = await screen.findByRole("dialog")
    const row = (await within(dialog).findAllByRole("row")).at(-1) as HTMLElement
    expect(within(row).getByText("定时清理")).toBeInTheDocument()
    expect(within(row).getByText("128")).toBeInTheDocument()
    // 耗时为 0 时显式给一个占位，而不是"0ms"这种看起来像测出来的数
    expect(within(row).getByText("-")).toBeInTheDocument()
  })
})
