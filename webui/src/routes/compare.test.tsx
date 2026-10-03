import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ComparePage from "@/routes/compare"
import { getChatIO, getLogById, type ChatIO, type ChatLog } from "@/lib/api"

vi.mock("@/lib/api", () => ({
  getLogById: vi.fn(),
  getChatIO: vi.fn(),
}))

const mocked = {
  getLogById: vi.mocked(getLogById),
  getChatIO: vi.mocked(getChatIO),
}

/**
 * 对比页的四态测试（方案 §5.2 的 C 层）。
 *
 * 这一页与别的页不同：它没有页面级的取数失败可言。每条请求各取一次，
 * 用的是 `allSettled`——一条取不到不影响其余几条，失败以**原文**留在那张
 * 卡片里。因此这里钉的是两件事：加载态对读屏可读（骨架不声明 role="status"
 * 就与"没有可对比的请求"无从区分），以及失败确实带着原文出现在页面上，
 * 而不是被吞掉。
 */

const INPUT = JSON.stringify({
  model: "deepseek-chat",
  messages: [{ role: "user", content: "你好" }],
})

function log(over: Partial<ChatLog> = {}): ChatLog {
  return {
    ID: 1,
    CreatedAt: "2026-09-30T10:00:00+08:00",
    Name: "deepseek-chat",
    TraceID: "trace-1",
    ProviderModel: "deepseek-chat-v3",
    ProviderName: "scnet",
    Status: "success",
    Style: "openai",
    UserAgent: "curl/8.0",
    Error: "",
    Retry: 0,
    ProxyTime: 1_000_000_000,
    FirstChunkTime: 200_000_000,
    ChunkTime: 3_000_000_000,
    Tps: 40,
    ChatIO: true,
    Size: 2048,
    prompt_tokens: 1000,
    completion_tokens: 500,
    total_tokens: 1500,
    prompt_tokens_details: { cached_tokens: 250 },
    key_name: "dev-key",
    input_price: 0,
    cache_read_price: 0,
    output_price: 0,
    currency: "CNY",
    ...over,
  }
}

function io(over: Partial<ChatIO> = {}): ChatIO {
  return { ID: 1, CreatedAt: "", UpdatedAt: "", LogId: 1, Input: INPUT, Style: "openai", ...over }
}

function renderPage(ids = "1") {
  return render(
    <MemoryRouter initialEntries={[`/compare?ids=${ids}`]}>
      <ComparePage />
    </MemoryRouter>
  )
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getLogById.mockResolvedValue(log())
  mocked.getChatIO.mockResolvedValue(io())
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("对比页 · 加载", () => {
  it("请求未落地时声明正在加载，而不是一片空白", () => {
    mocked.getLogById.mockReturnValue(new Promise(() => {}))
    mocked.getChatIO.mockReturnValue(new Promise(() => {}))
    renderPage()

    expect(screen.getByRole("status")).toHaveTextContent("加载中")
  })
})

describe("对比页 · 有数据", () => {
  it("给出缓存前缀结论与逐条分叉", async () => {
    renderPage()

    expect(await screen.findByText("缓存公共前缀")).toBeInTheDocument()
    // 分叉表与条目清单各出现一次，因此数量断言而不是唯一匹配
    expect(screen.getAllByText("#1").length).toBeGreaterThan(0)
  })
})

describe("对比页 · 逐条失败", () => {
  it("一条取不到时以原文留在页面上，其余几条照常显示", async () => {
    mocked.getLogById.mockRejectedValueOnce(new Error("网关超时"))
    renderPage("1,2")

    // 失败的那条：原文出现（不是"加载失败"四个字，也不是静默跳过）
    expect(await screen.findByText(/网关超时/)).toBeInTheDocument()
    // 没失败的那条：仍然画出来了（分叉表与条目清单里各出现一次）
    expect(screen.getAllByText("#2").length).toBeGreaterThan(0)
  })
})
