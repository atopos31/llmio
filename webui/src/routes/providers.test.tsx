import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ProvidersPage from "@/routes/providers"
import {
  getProviders,
  getProviderTemplates,
  type Provider,
  type ProviderTemplate,
} from "@/lib/api"

// 这一页的数据全部经过 api.ts，切断它就断开了本页的全部 IO。
// 子组件（表单对话框、模型列表对话框、useProviderForm）也从这里取函数，
// 清单必须完整——漏一个会让它在运行时是 undefined 而不是"没被调用"。
vi.mock("@/lib/api", () => ({
  createProvider: vi.fn(),
  deleteProvider: vi.fn(),
  getProviderModels: vi.fn(),
  getProviderTemplates: vi.fn(),
  getProviders: vi.fn(),
  updateProvider: vi.fn(),
}))

const mocked = {
  getProviders: vi.mocked(getProviders),
  getProviderTemplates: vi.mocked(getProviderTemplates),
}

/**
 * 供应商页的四态测试（方案 §5.2 的 C 层）。
 *
 * 与模型路由页同一套理由：断言"什么条件下说什么话"。原先取数失败只弹一条
 * 提示、正文照旧说"暂无提供商数据"，也就是失败与空态长得一样——这一组把
 * 那条界线钉住。
 *
 * 这一页同样是桌面表格 + 手机卡片两套并行实现，名字在 DOM 里出现两次，
 * 因此凡按文字找的地方都限定在表内。
 */
function provider(over: Partial<Provider> = {}): Provider {
  return {
    ID: 11,
    Name: "prov-a",
    Type: "openai",
    Config: '{"base_url":"https://api.example.com"}',
    Console: "https://console.example.com",
    Proxy: "",
    ErrorMatcher: "",
    ...over,
  }
}

function template(over: Partial<ProviderTemplate> = {}): ProviderTemplate {
  return { type: "openai", name: "OpenAI", config: {}, ...over } as ProviderTemplate
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ProvidersPage />
    </MemoryRouter>
  )
}

/** 唯一的那张表（桌面表格；手机卡片不是 table） */
function table(): Promise<HTMLElement> {
  return screen.findByRole("table")
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getProviders.mockResolvedValue([provider()])
  mocked.getProviderTemplates.mockResolvedValue([template()])
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("供应商页 · 加载", () => {
  it("请求未落地时声明正在加载，不说没有数据", async () => {
    mocked.getProviders.mockReturnValue(new Promise(() => {}))

    renderPage()

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("加载提供商列表"))
    expect(screen.queryByText("暂无提供商数据")).not.toBeInTheDocument()
  })
})

describe("供应商页 · 空", () => {
  it("一开始就没有时给出空态与添加入口，而不是一条死路", async () => {
    mocked.getProviders.mockResolvedValue([])

    renderPage()

    expect(await screen.findByText("暂无提供商数据")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "添加提供商" })).toBeInTheDocument()
  })

  it("筛选筛空与本来就没有，说法不同", async () => {
    const user = userEvent.setup()
    renderPage()
    await within(await table()).findByText("prov-a")

    // 先让后续的筛选请求返回空，再输入——顺序反过来的话，
    // 防抖那次请求早已用旧值落地，改 mock 改的是已经过去的那个 Promise
    mocked.getProviders.mockResolvedValue([])
    await user.type(screen.getByPlaceholderText("输入名称"), "不存在的名字")

    // 防抖 300ms 之后才真的按名字去取
    await waitFor(() => expect(mocked.getProviders).toHaveBeenLastCalledWith({ name: "不存在的名字", type: undefined }))
    expect(await screen.findByText("未找到匹配的提供商")).toBeInTheDocument()
    expect(screen.queryByText("暂无提供商数据")).not.toBeInTheDocument()
  })
})

describe("供应商页 · 错误", () => {
  it("取数失败时给出原文与重试入口，而不是说没有数据", async () => {
    mocked.getProviders.mockRejectedValueOnce(new Error("网关超时"))

    renderPage()

    expect(await screen.findByText("提供商列表加载失败")).toBeInTheDocument()
    expect(screen.getByText("网关超时")).toBeInTheDocument()
    expect(screen.queryByText("暂无提供商数据")).not.toBeInTheDocument()
  })

  it("重试后就地恢复成列表", async () => {
    const user = userEvent.setup()
    mocked.getProviders.mockRejectedValueOnce(new Error("网关超时"))
    renderPage()
    await screen.findByText("提供商列表加载失败")

    mocked.getProviders.mockResolvedValue([provider()])
    await user.click(screen.getByRole("button", { name: /重试/ }))

    expect(await within(await table()).findByText("prov-a")).toBeInTheDocument()
    expect(screen.queryByText("提供商列表加载失败")).not.toBeInTheDocument()
  })
})

describe("供应商页 · 有数据", () => {
  it("列出提供商，并在面板上交代这是几条", async () => {
    renderPage()

    const list = await table()
    expect(within(list).getByText("prov-a")).toBeInTheDocument()
    expect(within(list).getByText("openai")).toBeInTheDocument()
    // 口径说明不是装饰：条数就是"看全了没有"的依据
    expect(screen.getByText("共 1 个")).toBeInTheDocument()
  })

  it("名称筛选有防抖：连打三个字符只按最终值取一次", async () => {
    const user = userEvent.setup()
    renderPage()
    await within(await table()).findByText("prov-a")
    mocked.getProviders.mockClear()

    await user.type(screen.getByPlaceholderText("输入名称"), "abc")

    await waitFor(() => expect(mocked.getProviders).toHaveBeenCalledWith({ name: "abc", type: undefined }))
    expect(mocked.getProviders).toHaveBeenCalledTimes(1)
  })
})
