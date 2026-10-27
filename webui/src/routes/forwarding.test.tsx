import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ForwardingPage from "@/routes/forwarding"
import { FORWARDING_GUIDE_STORAGE_KEY } from "@/routes/forwarding/guide-storage"
import { getProviders, updateProviderForward, type Provider } from "@/lib/api"

// 本页只经过两个 API：列表与转发设置的独立保存端点。
// 子组件（设置弹窗）也从这里取函数，清单漏一个会让它运行时报 undefined。
vi.mock("@/lib/api", () => ({
  getProviders: vi.fn(),
  updateProviderForward: vi.fn(),
}))

const mocked = {
  getProviders: vi.mocked(getProviders),
  updateProviderForward: vi.mocked(updateProviderForward),
}

/**
 * 转发透传专页的四态与两个弹窗。
 *
 * 重点不是"渲染出几行"，而是几条承诺：
 * - 地址按协议给出（Anthropic 不带 /v1），因为给错形状的地址在客户端必然 404；
 * - 首次进入自动说明一次，之后靠「使用说明」重开，不要反复打扰；
 * - 保存走独立的 updateProviderForward，参数与页面展示的模式/改写一致。
 *
 * 这一页同样是桌面表格 + 手机卡片两套 DOM（后者只用 CSS 隐藏），
 * 所以凡按文字找的地方都限定在表内；弹窗里的说明表格另有 role="table"。
 */
function provider(over: Partial<Provider> = {}): Provider {
  return {
    ID: 11,
    Name: "prov-openai",
    Type: "openai",
    Config: '{"base_url":"https://api.example.com"}',
    Console: "",
    Proxy: "",
    ErrorMatcher: "",
    ForwardMode: null,
    ForwardSlug: "gw-a",
    ForwardRewrite: "known",
    ForwardTargetModel: "",
    ...over,
  }
}

const openaiProvider = provider()
const anthropicProvider = provider({ ID: 12, Name: "prov-ant", Type: "anthropic", ForwardSlug: "gw-ant" })
const geminiProvider = provider({
  ID: 13,
  Name: "prov-gem",
  Type: "gemini",
  ForwardSlug: "gw-gem",
  ForwardMode: true,
  ForwardRewrite: "all",
  ForwardTargetModel: "gemini-fixed",
})
const noSlugProvider = provider({
  ID: 14,
  Name: "prov-plain",
  ForwardSlug: null,
  ForwardMode: false,
  ForwardRewrite: "none",
})

function origin(): string {
  return window.location.origin
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ForwardingPage />
    </MemoryRouter>
  )
}

/** 主列表的桌子（弹窗里的说明表格不算） */
async function table(): Promise<HTMLElement> {
  return screen.findByRole("table")
}

/** 认为引导已读过，免得它盖住要点的按钮 */
function markGuideSeen() {
  window.localStorage.setItem(FORWARDING_GUIDE_STORAGE_KEY, "1")
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getProviders.mockResolvedValue([openaiProvider])
  mocked.updateProviderForward.mockResolvedValue(openaiProvider)
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("转发透传页 · 加载", () => {
  it("请求未落地时声明正在加载，不说没有数据", async () => {
    // 引导弹窗是模态的，打开时会把页面其余部分标成 aria-hidden，
    // 那样按 role 就找不到加载态了——本条只关心加载语义，先当成已读。
    markGuideSeen()
    mocked.getProviders.mockReturnValue(new Promise(() => {}))

    renderPage()

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("加载转发设置"))
    expect(screen.queryByText("还没有可配置的提供商")).not.toBeInTheDocument()
  })
})

describe("转发透传页 · 空", () => {
  it("没有提供商时给出空态与去处", async () => {
    markGuideSeen()
    mocked.getProviders.mockResolvedValue([])

    renderPage()

    expect(await screen.findByText("还没有可配置的提供商")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "前往提供商管理" })).toHaveAttribute("href", "/providers")
  })
})

describe("转发透传页 · 失败", () => {
  it("取数失败给出原文与重试，重试后恢复正常", async () => {
    markGuideSeen()
    const user = userEvent.setup()
    mocked.getProviders.mockRejectedValueOnce(new Error("网关超时"))

    renderPage()

    expect(await screen.findByText("转发设置加载失败")).toBeInTheDocument()
    expect(screen.getByText("网关超时")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "重试" }))

    const tableEl = await table()
    expect(await within(tableEl).findByText("prov-openai")).toBeInTheDocument()
  })
})

describe("转发透传页 · 有数据", () => {
  beforeEach(() => markGuideSeen())

  it("地址按协议生成：OpenAI 带 /v1、Anthropic 不带、Gemini 带 /v1beta", async () => {
    mocked.getProviders.mockResolvedValue([openaiProvider, anthropicProvider, geminiProvider])

    renderPage()

    const tableEl = await table()
    expect(await within(tableEl).findByText(origin() + "/p/gw-a/v1")).toBeInTheDocument()
    expect(within(tableEl).getByText(origin() + "/p/gw-ant")).toBeInTheDocument()
    expect(within(tableEl).getByText(origin() + "/p/gw-gem/v1beta")).toBeInTheDocument()
    // Anthropic 的地址绝不能以 /v1 结尾
    expect(within(tableEl).queryByText(origin() + "/p/gw-ant/v1")).not.toBeInTheDocument()
  })

  it("模式与改写按后端字段展示，all 带出目标模型", async () => {
    mocked.getProviders.mockResolvedValue([openaiProvider, geminiProvider, noSlugProvider])

    renderPage()

    const tableEl = await table()
    await within(tableEl).findByText("prov-openai")
    expect(within(tableEl).getByText("关闭")).toBeInTheDocument()
    expect(within(tableEl).getByText("认识的模型才改写")).toBeInTheDocument()
    expect(within(tableEl).getByText("纯转发")).toBeInTheDocument()
    expect(within(tableEl).getByText("完全改写 → gemini-fixed")).toBeInTheDocument()
    expect(within(tableEl).getByText("不改写")).toBeInTheDocument()
  })

  it("未设置标识的提供商明确说没有地址，而不是显示一个拼错的地址", async () => {
    mocked.getProviders.mockResolvedValue([noSlugProvider])

    renderPage()

    const tableEl = await table()
    expect(await within(tableEl).findByText("未设置专属地址")).toBeInTheDocument()
    expect(within(tableEl).queryByText(/\/p\/null/)).not.toBeInTheDocument()
  })
})

describe("转发透传页 · 首次引导", () => {
  it("第一次进入自动弹出说明，关闭后记住已读且不再次打扰", async () => {
    const user = userEvent.setup()

    const first = renderPage()

    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText("转发透传怎么用")).toBeInTheDocument()
    // 说明里必须讲清 Anthropic 基地址不带 /v1
    expect(within(dialog).getByText("/v1/messages")).toBeInTheDocument()
    expect(within(dialog).getByText(origin() + "/p/slug")).toBeInTheDocument()
    // 带 {{origin}} 的说明必须真的代入域名；漏传参数会原样渲染出花括号
    const dialogText = dialog.textContent ?? ""
    expect(dialogText).not.toContain("{{")
    expect(dialogText).toContain("本站当前域名是 " + origin())

    await user.click(within(dialog).getByRole("button", { name: "知道了" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(window.localStorage.getItem(FORWARDING_GUIDE_STORAGE_KEY)).toBe("1")

    first.unmount()
    renderPage()
    await table()
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  })

  it("已读过的用户不会被弹窗打断，仍可点「使用说明」重开", async () => {
    markGuideSeen()
    const user = userEvent.setup()

    renderPage()
    await table()
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "使用说明" }))
    expect(await screen.findByRole("dialog")).toBeInTheDocument()
  })
})

describe("转发透传页 · 设置弹窗", () => {
  beforeEach(() => markGuideSeen())

  it("保存时把标识、模式、改写与目标模型发给独立端点，并刷新列表", async () => {
    const user = userEvent.setup()
    renderPage()

    const tableEl = await table()
    await user.click(await within(tableEl).findByRole("button", { name: "设置 prov-openai 的转发" }))

    const dialog = await screen.findByRole("dialog")
    const slugInput = within(dialog).getByLabelText("专属地址标识")
    expect(slugInput).toHaveValue("gw-a")

    await user.clear(slugInput)
    await user.type(slugInput, "gw-new")
    await user.click(within(dialog).getByRole("radio", { name: /纯转发/ }))
    await user.click(within(dialog).getByRole("radio", { name: /完全改写/ }))
    await user.type(within(dialog).getByLabelText("改写目标模型"), "gpt-4o-mini")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() =>
      expect(mocked.updateProviderForward).toHaveBeenCalledWith(11, {
        slug: "gw-new",
        mode: "pure",
        rewrite: "all",
        target_model: "gpt-4o-mini",
      })
    )
    // 保存成功后关弹窗并重拉列表
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    await waitFor(() => expect(mocked.getProviders).toHaveBeenCalledTimes(2))
  })

  it("开启转发却没填标识时本地拦下，不发请求", async () => {
    const user = userEvent.setup()
    mocked.getProviders.mockResolvedValue([noSlugProvider])

    renderPage()

    const tableEl = await table()
    await user.click(await within(tableEl).findByRole("button", { name: "设置 prov-plain 的转发" }))

    const dialog = await screen.findByRole("dialog")
    await user.click(within(dialog).getByRole("radio", { name: /混合模式/ }))
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("开启转发时必须填写地址标识")
    expect(mocked.updateProviderForward).not.toHaveBeenCalled()
  })

  it("完全改写缺目标模型时本地拦下", async () => {
    const user = userEvent.setup()
    renderPage()

    const tableEl = await table()
    await user.click(await within(tableEl).findByRole("button", { name: "设置 prov-openai 的转发" }))

    const dialog = await screen.findByRole("dialog")
    await user.click(within(dialog).getByRole("radio", { name: /完全改写/ }))
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("完全改写需要填写目标模型")
    expect(mocked.updateProviderForward).not.toHaveBeenCalled()
  })
})
