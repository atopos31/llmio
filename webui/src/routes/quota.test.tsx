import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import QuotaPage from "@/routes/quota"
import { getQuotaConfig, runQuotaSources } from "@/lib/api"
import type {
  QuotaConfigResponse,
  QuotaItem,
  QuotaRunResult,
  QuotaSourceResult,
} from "@/lib/quota"

// 列表里既有本页自己用的两个取数，也有子对话框（编辑器 / 导入 / 全局设置）
// 在模块加载时就绑定走的写接口：这里少写一个，那些子模块拿到的就是 undefined。
vi.mock("@/lib/api", () => ({
  getQuotaConfig: vi.fn(),
  runQuotaSources: vi.fn(),
  refreshQuotaSource: vi.fn(),
  updateQuotaConfig: vi.fn(),
  createQuotaSource: vi.fn(),
  updateQuotaSource: vi.fn(),
  deleteQuotaSource: vi.fn(),
  testQuotaSource: vi.fn(),
  discoverQuotaSources: vi.fn(),
  importQuotaSource: vi.fn(),
}))

const mocked = {
  getQuotaConfig: vi.mocked(getQuotaConfig),
  runQuotaSources: vi.mocked(runQuotaSources),
}

/**
 * 额度页的四态测试（方案 §5.2 的 C 层）。
 *
 * 这一页原先有两处"两态长得一样"的毛病，都由四态断言钉住：加载中的骨架在
 * 无障碍树上没有 role="status"，与"没有配置数据源"无从区分；读取失败被塞进
 * 空态里，于是"没取到"被读成了"这里本来就没有"。断言的是页面在什么条件下
 * 说什么话，不是排版。
 */

function item(over: Partial<QuotaItem> = {}): QuotaItem {
  return {
    id: "plan",
    label: "套餐",
    used: 80,
    total: 100,
    remaining: 20,
    percent: 80,
    unit: "CREDITS",
    window: "month",
    status: "ok",
    text: "80 / 100",
    ...over,
  }
}

function source(over: Partial<QuotaSourceResult> = {}): QuotaSourceResult {
  return {
    id: "s1",
    name: "测试源",
    type: "http",
    enabled: true,
    ok: true,
    items: [item()],
    status: "ok",
    latencyMs: 12,
    updatedAt: 1_700_000_000_000,
    cached: false,
    ...over,
  }
}

function config(over: Partial<QuotaConfigResponse> = {}): QuotaConfigResponse {
  return {
    config: { refreshInterval: 60, warningAt: 80, sources: [] },
    builtins: [],
    configPath: "/data/quota.config.json",
    writeEnabled: true,
    defaultRefresh: 60,
    defaultWarning: 80,
    ...over,
  }
}

function runResult(sources: QuotaSourceResult[]): QuotaRunResult {
  const ok = sources.filter((s) => s.ok)
  return {
    generatedAt: 1_700_000_000_000,
    refreshInterval: 60,
    warningAt: 80,
    sources,
    summary: {
      totalSources: sources.length,
      okSources: ok.length,
      failSources: sources.length - ok.length,
      totalItems: ok.reduce((n, s) => n + (s.items?.length ?? 0), 0),
      worst: null,
    },
  }
}

beforeEach(async () => {
  vi.clearAllMocks()
  // 展示偏好存在 localStorage 里，用例之间不清会互相串味
  localStorage.clear()
  mocked.getQuotaConfig.mockResolvedValue(config())
  mocked.runQuotaSources.mockResolvedValue(runResult([source()]))
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("额度页 · 加载", () => {
  it("余量还没落地时不宣称「没有配置数据源」，并声明正在读取", () => {
    mocked.runQuotaSources.mockReturnValue(new Promise(() => {}))
    render(<QuotaPage />)

    expect(screen.getByRole("status")).toHaveTextContent("正在读取余量")
    expect(screen.queryByText("还没有配置数据源")).not.toBeInTheDocument()
  })
})

describe("额度页 · 空", () => {
  it("没有数据源时给出去处，而不是一片空白", async () => {
    mocked.runQuotaSources.mockResolvedValue(runResult([]))
    render(<QuotaPage />)

    expect(await screen.findByText("还没有配置数据源")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "手动添加" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "从 llmio 导入" })).toBeInTheDocument()
    // 一条余量都没有，摘要条就没有可说的
    expect(screen.queryByText("最紧张")).not.toBeInTheDocument()
  })
})

describe("额度页 · 错误", () => {
  it("取数失败给出原文，而不是装成空态", async () => {
    mocked.runQuotaSources.mockRejectedValue(new Error("上游 502"))
    render(<QuotaPage />)

    expect(await screen.findByText("读取余量失败")).toBeInTheDocument()
    expect(screen.getByText("上游 502")).toBeInTheDocument()
    // 失败与空态必须分得开：文案一样时，"没取到"会被读成"没有配置"
    expect(screen.queryByText("还没有配置数据源")).not.toBeInTheDocument()
  })

  it("重试按钮再跑一轮，且是强制取数（不走缓存）", async () => {
    mocked.runQuotaSources.mockRejectedValue(new Error("网络不可达"))
    const user = userEvent.setup()
    render(<QuotaPage />)

    // 工具栏上也有一个"刷新"，这里要的是失败卡片里的那一个
    const card = (await screen.findByText("读取余量失败")).parentElement!
    mocked.runQuotaSources.mockResolvedValue(runResult([source()]))
    mocked.runQuotaSources.mockClear()

    await user.click(within(card).getByRole("button", { name: "刷新" }))

    await waitFor(() => expect(mocked.runQuotaSources).toHaveBeenCalledWith({ force: true }))
    expect(await screen.findByText("最紧张")).toBeInTheDocument()
  })
})

describe("额度页 · 有数据", () => {
  it("摘要条把最紧张那条的名字与状态都写出来，状态不只靠颜色", async () => {
    mocked.runQuotaSources.mockResolvedValue(
      runResult([
        source({
          items: [
            item({ id: "plan", label: "套餐", status: "ok", percent: 10 }),
            item({ id: "card", label: "信用卡", status: "warning", percent: 85 }),
          ],
        }),
      ])
    )
    render(<QuotaPage />)

    const bar = (await screen.findByText("最紧张")).parentElement!
    // 徽标里必须有一个可读的字：只换颜色的小圆点对色盲用户不可用
    expect(within(bar).getByText("告警")).toBeInTheDocument()
    expect(bar).toHaveTextContent("测试源 · 信用卡")
    expect(bar).toHaveTextContent("85%")
  })

  it("数据源照常渲染，条数与正常源数写进摘要", async () => {
    render(<QuotaPage />)

    // 这两句前面都带一个"· "分隔符，因此按子串匹配
    expect(await screen.findByText(/共 1 条余量/)).toBeInTheDocument()
    expect(screen.getByText(/1\/1 个数据源正常/)).toBeInTheDocument()
    // 卡片自己的读数：摘要条上没有它，能证明卡片确实渲染了
    expect(screen.getByText("12ms")).toBeInTheDocument()
  })
})

describe("额度页 · 编辑入口", () => {
  it("交给编辑器的是配置里那份完整源，而不是卡片上的取数结果", async () => {
    // 这一条钉的是**接线**：卡片手里只有取数结果（id / 名称 / 类型），
    // 配置那一份才有 url、超时、请求头。保存是整份替换，接错了线就等于
    // 让用户每次编辑都把这些字段重填一遍，不填就没了——而且没有任何提示。
    // 类型系统拦不住这种错：QuotaSourceResult 在结构上满足 QuotaSource。
    mocked.getQuotaConfig.mockResolvedValue(
      config({
        config: {
          refreshInterval: 60,
          warningAt: 80,
          sources: [
            {
              id: "s1",
              name: "测试源",
              enabled: true,
              type: "http",
              url: "https://api.example.com/usage",
              timeout: 45,
            },
          ],
        },
      })
    )
    const user = userEvent.setup()
    render(<QuotaPage />)

    await user.click(
      await screen.findByRole("button", { name: "数据源配置（接口 / 脚本 / 密钥）" })
    )

    expect(screen.getByDisplayValue("https://api.example.com/usage")).toBeInTheDocument()
    expect(screen.getByDisplayValue("45")).toBeInTheDocument()
  })
})
