import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import AuthKeysPage from "@/routes/auth-keys"
import { getAuthKeys, getModelOptions, type AuthKey, type Model } from "@/lib/api"

// 这一页的数据全部经过 api.ts，切断它就断开了本页的全部 IO。
// 清单要完整（含对话框与表单用到的写入端点）——漏一个会让它在运行时是
// undefined，而不是"没被调用"。
vi.mock("@/lib/api", () => ({
  createAuthKey: vi.fn(),
  deleteAuthKey: vi.fn(),
  getAuthKeys: vi.fn(),
  getModelOptions: vi.fn(),
  toggleAuthKeyStatus: vi.fn(),
  updateAuthKey: vi.fn(),
}))

const mocked = {
  getAuthKeys: vi.mocked(getAuthKeys),
  getModelOptions: vi.mocked(getModelOptions),
}

/**
 * 密钥页的四态测试（方案 §5.2 的 C 层）。
 *
 * 与模型路由页、供应商页同一处缺陷、同一组钉子：`fetchAuthKeys` 原先失败
 * 只 console.error，正文照旧说"暂无 API Key"。这一组里"取数失败不说没有数据"
 * 是核心断言，其余三态顺带覆盖。
 *
 * 与另两页一样，这一页是桌面表格 + 手机卡片两套并行实现，同一个名字在 DOM
 * 里出现两次，按文字找的地方一律限定在 `within(table())` 内。
 */
function key(over: Partial<AuthKey> = {}): AuthKey {
  return {
    ID: 7,
    CreatedAt: "2026-01-01T00:00:00Z",
    UpdatedAt: "2026-01-01T00:00:00Z",
    Name: "team-a",
    Key: "sk-abcdef123456",
    Status: true,
    IOLog: false,
    AllowAll: true,
    Models: null,
    ExpiresAt: null,
    UsageCount: 3,
    LastUsedAt: null,
    ...over,
  }
}

function page(items: AuthKey[], over: Partial<{ total: number; pages: number }> = {}) {
  return {
    data: items,
    total: over.total ?? items.length,
    page: 1,
    page_size: 20,
    pages: over.pages ?? 1,
  }
}

function model(over: Partial<Model> = {}): Model {
  return {
    ID: 1,
    Name: "gpt-test",
    Remark: "",
    MaxRetry: 10,
    TimeOut: 60,
    Strategy: "lottery",
    Breaker: false,
    DisplayOrder: 1,
    ...over,
  }
}

function renderPage() {
  return render(
    <MemoryRouter>
      <AuthKeysPage />
    </MemoryRouter>
  )
}

/** 唯一的那张表（桌面表格；手机卡片不是 table） */
function table(): Promise<HTMLElement> {
  return screen.findByRole("table")
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getAuthKeys.mockResolvedValue(page([key()]))
  mocked.getModelOptions.mockResolvedValue([model()])
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("密钥页 · 加载", () => {
  it("请求未落地时声明正在加载，不说没有数据", async () => {
    mocked.getAuthKeys.mockReturnValue(new Promise(() => {}))

    renderPage()

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("加载 API Key 列表"))
    expect(screen.queryByText("暂无 API Key")).not.toBeInTheDocument()
  })
})

describe("密钥页 · 空", () => {
  it("一个 Key 都没有时给出新建入口，而不是一条死路", async () => {
    mocked.getAuthKeys.mockResolvedValue(page([]))

    renderPage()

    expect(await screen.findByText("暂无 API Key")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "新建 API Key" })).toBeInTheDocument()
  })

  it("筛选筛空与本来就没有，说法不同", async () => {
    const user = userEvent.setup()
    renderPage()
    await within(await table()).findByText("team-a")

    mocked.getAuthKeys.mockResolvedValue(page([]))
    await user.type(screen.getByPlaceholderText("名称或 Key"), "没有这个")

    // 搜索有 400ms 防抖，没等它落地就断言，测的是"没筛"的那条路径
    expect(await screen.findByText("没有符合筛选条件的 API Key", {}, { timeout: 2000 })).toBeInTheDocument()
    expect(screen.queryByText("暂无 API Key")).not.toBeInTheDocument()
  })
})

describe("密钥页 · 错误", () => {
  it("取数失败时给出原文与重试入口，而不是说没有数据", async () => {
    mocked.getAuthKeys.mockRejectedValueOnce(new Error("500 internal error"))

    renderPage()

    expect(await screen.findByText("API Key 列表加载失败")).toBeInTheDocument()
    expect(screen.getByText("500 internal error")).toBeInTheDocument()
    expect(screen.queryByText("暂无 API Key")).not.toBeInTheDocument()
  })

  it("重试后就地恢复成列表，并按当前分页参数重取", async () => {
    const user = userEvent.setup()
    mocked.getAuthKeys.mockRejectedValueOnce(new Error("500 internal error"))
    renderPage()
    await screen.findByText("API Key 列表加载失败")

    mocked.getAuthKeys.mockResolvedValue(page([key()]))
    await user.click(screen.getByRole("button", { name: /重试/ }))

    expect(await within(await table()).findByText("team-a")).toBeInTheDocument()
    expect(mocked.getAuthKeys).toHaveBeenLastCalledWith({
      page: 1,
      page_size: 20,
      status: undefined,
      allow_all: undefined,
      search: undefined,
    })
  })

  it("模型列表取数失败时，模型选择区说失败，而不是说无匹配模型", async () => {
    const user = userEvent.setup()
    mocked.getModelOptions.mockRejectedValueOnce(new Error("模型服务不可用"))
    renderPage()
    await within(await table()).findByText("team-a")

    await user.click(screen.getByRole("button", { name: "新建 API Key" }))
    const dialog = await screen.findByRole("dialog")

    expect(await within(dialog).findByText("模型列表加载失败")).toBeInTheDocument()
    expect(within(dialog).getByText("模型服务不可用")).toBeInTheDocument()
    expect(within(dialog).queryByText("无匹配模型")).not.toBeInTheDocument()
  })

  it("模型选择区就地重试，恢复后列出模型", async () => {
    const user = userEvent.setup()
    mocked.getModelOptions.mockRejectedValueOnce(new Error("模型服务不可用"))
    renderPage()
    await within(await table()).findByText("team-a")
    await user.click(screen.getByRole("button", { name: "新建 API Key" }))
    const dialog = await screen.findByRole("dialog")
    await within(dialog).findByText("模型列表加载失败")

    // 新 Key 默认"无限制"，那一区是 pointer-events-none，先解开再点重试
    await user.click(within(dialog).getByRole("checkbox"))
    mocked.getModelOptions.mockResolvedValue([model()])
    await user.click(within(dialog).getByRole("button", { name: /重试/ }))

    expect(await within(dialog).findByText("gpt-test")).toBeInTheDocument()
    expect(within(dialog).queryByText("模型列表加载失败")).not.toBeInTheDocument()
  })
})

describe("密钥页 · 有数据", () => {
  it("列表里只出现 Key 的尾部，整串要显式点开", async () => {
    renderPage()

    const list = await table()
    // 整串不在列表里：肩窥一眼就是一把可用的钥匙
    expect(within(list).getByText("...123456")).toBeInTheDocument()
    expect(screen.queryByText("sk-abcdef123456")).not.toBeInTheDocument()
  })

  it("面板交代条数口径，分页另说页码", async () => {
    mocked.getAuthKeys.mockResolvedValue(page([key(), key({ ID: 8, Name: "team-b" })]))

    renderPage()

    await table()
    expect(screen.getByText("共 2 个")).toBeInTheDocument()
    expect(screen.getByText("共 2 条，第 1 / 1 页")).toBeInTheDocument()
  })

  it("过期的 Key 除了变色还写明已过期", async () => {
    mocked.getAuthKeys.mockResolvedValue(
      page([key({ ExpiresAt: "2020-01-01T00:00:00Z" })])
    )

    renderPage()

    // 颜色单独承载含义时色盲用户读不到，因此"已过期"这三个字是必须的
    // 桌面与手机两套实现都要有，故用 getAllByText
    expect(await within(await table()).findByText("已过期")).toBeInTheDocument()
    expect(screen.getAllByText("已过期")).toHaveLength(2)
  })

  it("永久有效的 Key 不显示已过期", async () => {
    renderPage()

    await table()
    expect(screen.getAllByText("永久有效").length).toBeGreaterThan(0)
    expect(screen.queryByText("已过期")).not.toBeInTheDocument()
  })

  it("搜索按防抖后的词取数，连打三个字符只发一次请求", async () => {
    const user = userEvent.setup()
    renderPage()
    await within(await table()).findByText("team-a")
    mocked.getAuthKeys.mockClear()

    await user.type(screen.getByPlaceholderText("名称或 Key"), "abc")

    await waitFor(
      () =>
        expect(mocked.getAuthKeys).toHaveBeenLastCalledWith({
          page: 1,
          page_size: 20,
          status: undefined,
          allow_all: undefined,
          search: "abc",
        }),
      { timeout: 2000 }
    )
    expect(mocked.getAuthKeys).toHaveBeenCalledTimes(1)
  })
})
