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
  type Model,
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

describe("日志页 · 多选筛选", () => {
  /** 两个可选的模型名。刻意不与任何行里的模型同名：按名字取控件时不会撞车。 */
  function modelOptions(): Model[] {
    return [
      { ID: 1, Name: "alpha-model", Remark: "", MaxRetry: 2, TimeOut: 30, Strategy: "lottery" },
      { ID: 2, Name: "beta-model", Remark: "", MaxRetry: 2, TimeOut: 30, Strategy: "lottery" },
    ]
  }

  it("同一维度上勾两个取值，请求里带上逗号拼接的那串", async () => {
    mocked.getModelOptions.mockResolvedValue(modelOptions())
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("deepseek-chat")

    // 触发器在无障碍树上的名字就是维度名（未选时不含计数）
    await user.click(screen.getByRole("button", { name: "模型名称" }))
    await user.click(await screen.findByRole("checkbox", { name: "alpha-model" }))
    await user.click(screen.getByRole("checkbox", { name: "beta-model" }))

    // 两个取值一次带走，而不是"最后点的那一个"
    await waitFor(() =>
      expect(mocked.getLogs).toHaveBeenLastCalledWith(
        1,
        20,
        expect.objectContaining({ name: "alpha-model,beta-model" })
      )
    )
  })

  it("全部取消后这个维度不再发送（回到不过滤，而不是筛一个空值）", async () => {
    mocked.getModelOptions.mockResolvedValue(modelOptions())
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("deepseek-chat")

    await user.click(screen.getByRole("button", { name: "模型名称" }))
    const alpha = await screen.findByRole("checkbox", { name: "alpha-model" })
    await user.click(alpha)
    await waitFor(() => expect(mocked.getLogs).toHaveBeenLastCalledWith(1, 20, expect.objectContaining({ name: "alpha-model" })))

    await user.click(alpha)
    await waitFor(() =>
      expect(mocked.getLogs).toHaveBeenLastCalledWith(
        1,
        20,
        expect.objectContaining({ name: undefined })
      )
    )
    // 一个维度都没筛，就不该摆出"清空筛选"
    expect(screen.queryByRole("button", { name: "清空筛选" })).not.toBeInTheDocument()
  })

  it("旧链接里的 all 当作没筛，而不是筛一个叫 all 的值", async () => {
    // 单值时代的链接是 `?status=all`。若按字面理解，读者会得到一个空表，
    // 而筛选器上什么也看不出来——这是最难自查的一类错。
    render(
      <MemoryRouter initialEntries={["/?status=all&model=all"]}>
        <LogsPage />
      </MemoryRouter>
    )

    expect(await screen.findByText("deepseek-chat")).toBeInTheDocument()
    expect(mocked.getLogs).toHaveBeenCalledWith(
      1,
      20,
      expect.objectContaining({ status: undefined })
    )
    expect(mocked.getLogs).toHaveBeenCalledWith(1, 20, expect.objectContaining({ name: undefined }))
    expect(screen.queryByRole("button", { name: "清空筛选" })).not.toBeInTheDocument()
  })
})

/**
 * 协议转换（OpenAI 客户端 ↔ Anthropic 上游）在日志页的呈现。
 *
 * 这一块的信息在别处都拿不到：客户端看到的响应与直连无异（转换对它透明），
 * 后端也只是把这些短码写进了日志行。界面若把它藏起来或漏读字段，一次"答案
 * 变了但没人知道为什么"的排查就断了线。
 */
describe("日志页 · 协议转换", () => {
  /** 打开唯一一行的详情抽屉 */
  async function openDetail() {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "日志详情: 42" }))
    return user
  }

  it("客户端 openai、上游 anthropic：给出方向，并把每个改动码译成人话", async () => {
    mocked.getLogs.mockResolvedValue(
      response([
        log({ Style: "openai", upstream_style: "anthropic", bridge_notes: "defaulted_max_tokens,dropped_seed" }),
      ])
    )
    renderPage()
    await openDetail()

    expect(await screen.findByText("协议转换")).toBeInTheDocument()
    expect(screen.getByText("openai → anthropic")).toBeInTheDocument()
    // 短码本身留着（能拿去 grep 日志），旁边是它的释义
    expect(screen.getByText("defaulted_max_tokens")).toBeInTheDocument()
    expect(screen.getByText("请求没带最大输出长度，补了默认值（可能被截断）")).toBeInTheDocument()
    expect(screen.getByText("dropped_seed")).toBeInTheDocument()
    expect(screen.getByText("丢掉了 seed 参数")).toBeInTheDocument()
  })

  it("同协议直连：不摆出这一节（绝大多数日志都是这种，摆了就是噪声）", async () => {
    mocked.getLogs.mockResolvedValue(response([log({ Style: "openai", upstream_style: "openai" })]))
    renderPage()
    await openDetail()

    // 详情确实开了——否则下面的断言就是"什么都没渲染"的假通过
    expect(await screen.findByText("基本信息")).toBeInTheDocument()
    expect(screen.queryByText("协议转换")).not.toBeInTheDocument()
  })

  it("转了但没丢东西：说清是逐字段对应的，而不是留一片空白让人猜", async () => {
    mocked.getLogs.mockResolvedValue(response([log({ Style: "anthropic", upstream_style: "openai" })]))
    renderPage()
    await openDetail()

    expect(await screen.findByText("anthropic → openai")).toBeInTheDocument()
    expect(screen.getByText("逐字段一一对应，这次转换没有丢下什么")).toBeInTheDocument()
  })

  it("码表里没有的新码：显示原码与未知条目，而不是把 i18n 键路径漏到界面上", async () => {
    // 后端加了新码而三语词条还没跟上时会走到这里。i18next 认不出键时默认吐出
    // 键路径本身，"logs:bridge.notes.…" 直接给用户看是明显的缺陷
    mocked.getLogs.mockResolvedValue(
      response([log({ Style: "openai", upstream_style: "anthropic", bridge_notes: "brand_new_note" })])
    )
    renderPage()
    await openDetail()

    expect(await screen.findByText("brand_new_note")).toBeInTheDocument()
    expect(screen.getByText("未知条目")).toBeInTheDocument()
    expect(screen.queryByText(/logs:bridge\.notes/)).not.toBeInTheDocument()
  })
})
