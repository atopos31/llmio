import { render, screen, waitFor } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import LogsPage from "@/routes/logs"
import {
  getAuthKeysList,
  getLogs,
  getModelOptions,
  getProviderTemplates,
  getProviders,
  type ChatLog,
  type LogsResponse,
} from "@/lib/api"

vi.mock("@/lib/api", () => ({
  getLogs: vi.fn(),
  // 四个筛选下拉的选项来源。它们走 allSettled，失败只影响可选范围，
  // 但**不能**是 undefined——那样 setState 下去会让下拉自己崩掉。
  getProviders: vi.fn(),
  getModelOptions: vi.fn(),
  getAuthKeysList: vi.fn(),
  getProviderTemplates: vi.fn(),
  cleanLogs: vi.fn(),
}))

const mocked = {
  getLogs: vi.mocked(getLogs),
  getProviders: vi.mocked(getProviders),
  getModelOptions: vi.mocked(getModelOptions),
  getAuthKeysList: vi.mocked(getAuthKeysList),
  getProviderTemplates: vi.mocked(getProviderTemplates),
}

/**
 * 日志页的四态测试（方案 §5.2 的 C 层）。
 *
 * 这一页原先对取数失败只弹一条会自己消失的 toast，表体照旧显示"暂无请求
 * 日志"——与模型路由页、供应商页、密钥页是同一处缺陷。翻页失败更隐蔽：
 * 页码变了、行还是上一页的，而页面上没有任何东西说明这一点。
 *
 * 自动刷新（静默）失败**不**落成页面上的错误态——一次网络瞬断就把整页换成
 * 失败态，比保留上一次的结果更打扰人。但这一支**没有被测试钉住**（文件末尾
 * 留了一条 it.todo）：要驱动它得先在界面上选中一个自动刷新档位，而 Radix 的
 * Select 配上假定时器在这个环境里会卡住。那条 `if (!silent)` 是这次原样保留
 * 的既有行为，不是新增的未覆盖——把"改坏了也没有测试会红"写在这里，免得
 * 后来者以为它是被保护着的。
 */

function log(over: Partial<ChatLog> = {}): ChatLog {
  return {
    ID: 42,
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

function response(data: ChatLog[] = [log()]): LogsResponse {
  return { data, total: data.length, page: 1, page_size: 20, pages: data.length ? 1 : 0 }
}

/** 该时间范围内的空态文案 */
const NO_DATA = "暂无请求日志"

function renderPage() {
  return render(
    <MemoryRouter>
      <LogsPage />
    </MemoryRouter>
  )
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getLogs.mockResolvedValue(response())
  mocked.getProviders.mockResolvedValue([])
  mocked.getModelOptions.mockResolvedValue([])
  mocked.getAuthKeysList.mockResolvedValue([])
  mocked.getProviderTemplates.mockResolvedValue([])
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("日志页 · 加载", () => {
  it("请求未落地时说正在加载，不说没有日志", () => {
    mocked.getLogs.mockReturnValue(new Promise(() => {}))
    renderPage()

    expect(screen.getByRole("status")).toHaveTextContent("加载日志数据")
    expect(screen.queryByText(NO_DATA)).not.toBeInTheDocument()
  })
})

describe("日志页 · 空", () => {
  it("确实没有日志时才说没有，且不给「清空筛选」（本来就没有筛选）", async () => {
    mocked.getLogs.mockResolvedValue(response([]))
    renderPage()

    expect(await screen.findByText(NO_DATA)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "清空筛选" })).not.toBeInTheDocument()
  })
})

describe("日志页 · 错误", () => {
  it("取数失败给出原文与重试，而不是说没有日志", async () => {
    mocked.getLogs.mockRejectedValue(new Error("数据库忙"))
    renderPage()

    expect(await screen.findByText("读取日志失败")).toBeInTheDocument()
    expect(screen.getByText("数据库忙")).toBeInTheDocument()
    // "暂无请求日志"是关于服务端事实的断言，不该由一次失败的请求替它说
    expect(screen.queryByText(NO_DATA)).not.toBeInTheDocument()
  })

  it("重试后就地恢复成表", async () => {
    mocked.getLogs.mockRejectedValueOnce(new Error("数据库忙"))
    const user = userEvent.setup()
    renderPage()

    await screen.findByText("读取日志失败")
    mocked.getLogs.mockClear()
    await user.click(screen.getByRole("button", { name: "重试" }))

    await waitFor(() => expect(mocked.getLogs).toHaveBeenCalledTimes(1))
    expect(await screen.findByText("deepseek-chat")).toBeInTheDocument()
    expect(screen.queryByText("读取日志失败")).not.toBeInTheDocument()
  })

  it("已有行时刷新失败不清屏，但说清下面是上一次的结果", async () => {
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByText("deepseek-chat")).toBeInTheDocument()

    mocked.getLogs.mockRejectedValue(new Error("数据库忙"))
    await user.click(screen.getByRole("button", { name: "刷新" }))

    expect(await screen.findByText("这次没取到，下面是上一次的结果")).toBeInTheDocument()
    // 旧行仍在，但读者已经知道它不是这次取回来的
    expect(screen.getByText("deepseek-chat")).toBeInTheDocument()
    expect(screen.queryByText(NO_DATA)).not.toBeInTheDocument()
  })

  it.todo("自动刷新（静默）失败不落成页面上的错误态——未覆盖，见文件头说明")
})

describe("日志页 · 有数据", () => {
  it("一行一条，给出模型、供应商与状态", async () => {
    renderPage()

    expect(await screen.findByText("deepseek-chat")).toBeInTheDocument()
    expect(screen.getByText("deepseek-chat-v3")).toBeInTheDocument()
    expect(screen.getByText("scnet")).toBeInTheDocument()
    expect(screen.getByText("成功")).toBeInTheDocument()
    expect(screen.queryByText(NO_DATA)).not.toBeInTheDocument()
  })
})
