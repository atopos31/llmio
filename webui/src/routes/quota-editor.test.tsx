import { render, screen, waitFor } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { QuotaEditorDialog } from "@/routes/quota-editor"
import {
  createQuotaSource,
  deleteQuotaSource,
  testQuotaSource,
  updateQuotaSource,
} from "@/lib/api"
import type { QuotaBuiltinInfo, QuotaSource } from "@/lib/quota"

vi.mock("@/lib/api", () => ({
  createQuotaSource: vi.fn(),
  updateQuotaSource: vi.fn(),
  deleteQuotaSource: vi.fn(),
  testQuotaSource: vi.fn(),
}))

const mocked = {
  create: vi.mocked(createQuotaSource),
  update: vi.mocked(updateQuotaSource),
  remove: vi.mocked(deleteQuotaSource),
  test: vi.mocked(testQuotaSource),
}

/**
 * 数据源编辑器的行为测试（方案 §5.2 的 C 层）。
 *
 * 这一层钉的是两件**改坏了页面照样渲染**的事：
 *
 *   1. 登录型内置适配器（超算 / opencode）的账号与会话要填在 env 里，而
 *      `quota.ValidateSource` 要求登录型的 env 非空——没有输入项就等于这个
 *      适配器选得出来、存不下去。键名是适配器读死的（SCNET_USER / SCNET_PASS），
 *      用户无从猜起，因此连占位符一起钉住。
 *   2. 编辑已有源时表单必须**回填完整配置**。保存走的是整份替换，没回填的
 *      字段不会被保留，而是被静默清掉——高级项（请求头 / 字段映射 / 固定字段）
 *      和脚本的 env 都在此列。这类丢失没有任何界面提示，只有测试拦得住。
 *
 * 适配器 id 不是猜的：卡片给的是取数结果，编辑器要的是配置里那一份，页面用
 * `editableSource` 合成（那一层在 lib/quota.test.ts 里另有一组断言）。
 */

const BUILTINS: QuotaBuiltinInfo[] = [
  { id: "deepseek", label: "DeepSeek 余额", doc: "GET /user/balance", verified: true },
  {
    id: "scnet",
    label: "国家超算 TokenPlan（登录型）",
    doc: "走控制台会话：RSA 加密 SSO 登录 + cookie jar。需填账号口令",
    verified: false,
    needsLogin: true,
    envKeys: ["SCNET_USER", "SCNET_PASS"],
  },
]

function renderDialog(source: QuotaSource | null) {
  const onSaved = vi.fn()
  render(
    <QuotaEditorDialog
      open
      onOpenChange={() => {}}
      source={source}
      builtins={BUILTINS}
      defaults={{ refresh: 60, warning: 80 }}
      onSaved={onSaved}
    />
  )
  return { onSaved }
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.create.mockResolvedValue({} as QuotaSource)
  mocked.update.mockResolvedValue({} as QuotaSource)
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("数据源编辑器 · 新增", () => {
  it("默认是 DeepSeek，不摆出环境变量字段（它不从 env 读凭据）", () => {
    renderDialog(null)

    expect(screen.getByText("新增数据源")).toBeInTheDocument()
    expect(screen.queryByText("环境变量")).not.toBeInTheDocument()
  })
})

describe("数据源编辑器 · 登录型内置适配器", () => {
  it("超算给出环境变量输入框，占位符直接写明要填哪两个键", () => {
    // 键名由适配器读死，界面不写出来用户只能靠猜或者翻源码
    renderDialog({ id: "s1", name: "超算", enabled: true, type: "builtin", builtin: "scnet" })

    expect(screen.getByText("环境变量")).toBeInTheDocument()
    // 多行文本用正则匹配：testing-library 只把 DOM 那一侧归一化（换行折成空格），
    // 字符串匹配器不会跟着折，于是带换行的字符串永远比不上
    expect(screen.getByPlaceholderText(/SCNET_USER=\s*SCNET_PASS=/)).toBeInTheDocument()
  })

  it("填好的账号口令随保存提交", async () => {
    const user = userEvent.setup()
    renderDialog({ id: "s1", name: "超算", enabled: true, type: "builtin", builtin: "scnet" })

    await user.type(
      screen.getByPlaceholderText(/SCNET_USER=\s*SCNET_PASS=/),
      "SCNET_USER=alice{enter}SCNET_PASS=hunter2"
    )
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].env).toEqual({
      SCNET_USER: "alice",
      SCNET_PASS: "hunter2",
    })
  })

  it("已保存的凭据以掩码回填，不动它就是原样回传（由服务端还原）", async () => {
    // 回填成空会让"保存一个改了超时的源"顺手把口令清掉；回填掩码则服务端
    // 认得出"没改过"，这正是 MaskSource / ResolveSourceSecrets 的约定
    const user = userEvent.setup()
    renderDialog({
      id: "s1",
      name: "超算",
      enabled: true,
      type: "builtin",
      builtin: "scnet",
      baseUrl: "https://www.scnet.cn",
      env: { SCNET_USER: "alice", SCNET_PASS: "****4321" },
    })

    expect(screen.getByDisplayValue("https://www.scnet.cn")).toBeInTheDocument()
    expect(screen.getByPlaceholderText(/SCNET_USER=\s*SCNET_PASS=/)).toHaveValue(
      "SCNET_USER=alice\nSCNET_PASS=****4321"
    )

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].env).toEqual({
      SCNET_USER: "alice",
      SCNET_PASS: "****4321",
    })
  })
})

describe("数据源编辑器 · 编辑已有源", () => {
  it("脚本内容与 env 都回填，保存后一个不少", async () => {
    const user = userEvent.setup()
    renderDialog({
      id: "s2",
      name: "自写脚本",
      enabled: true,
      type: "script",
      scriptSource: "output({ items: [] })",
      env: { API_KEY: "****9999" },
    })

    expect(screen.getByDisplayValue("output({ items: [] })")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    const sent = mocked.update.mock.calls[0][0]
    expect(sent.scriptSource).toBe("output({ items: [] })")
    // 这一条以前会丢：表单不回填 env，保存时的整份替换就把它抹掉了
    expect(sent.env).toEqual({ API_KEY: "****9999" })
  })

  it("高级项（请求头 / 查询参数 / 固定字段 / 字段映射）回填后仍在配置里", async () => {
    const user = userEvent.setup()
    renderDialog({
      id: "s3",
      name: "中转站",
      enabled: true,
      type: "http",
      url: "https://api.example.com/usage",
      headers: { "x-foo": "bar", "x-org": "acme" },
      query: { page: "1" },
      constants: { window: "month" },
      map: { label: "name", unit: "=CREDITS" },
    })

    expect(screen.getByDisplayValue("https://api.example.com/usage")).toBeInTheDocument()
    // 高级项默认折叠，展开才渲染
    await user.click(screen.getByRole("button", { name: "展开高级" }))

    // 请求头是"每行一条 KEY=VALUE"，与其他几个多行框同一套写法
    expect(screen.getByDisplayValue(/x-foo=bar\s*x-org=acme/)).toBeInTheDocument()
    expect(screen.getByDisplayValue("page=1")).toBeInTheDocument()
    expect(screen.getByDisplayValue("window=month")).toBeInTheDocument()
    // 字面量在文本里写成双等号，回填要能认回来
    expect(screen.getByDisplayValue(/label=name\s*unit==CREDITS/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    const sent = mocked.update.mock.calls[0][0]
    // 多个请求头要一个不少地带上：以前是 JSON 文本框，语法错一点就整份丢掉
    expect(sent.headers).toEqual({ "x-foo": "bar", "x-org": "acme" })
    expect(sent.query).toEqual({ page: "1" })
    expect(sent.constants).toEqual({ window: "month" })
    expect(sent.map).toEqual({ label: "name", unit: "=CREDITS" })
  })

  /**
   * HTTP 类型的"请求定制"。
   *
   * 原先有两处填了等于没填：请求头是 JSON 文本框（写错一个逗号就整份静默丢掉），
   * 查询参数则从编辑器一路存到配置、却被服务端丢在 toHTTPConfig 之外——
   * 界面上看不出任何差别，只有真正去拉一次才发现参数没发出去。
   * 两头都改过了，这一组钉的是"用户填的能存下来、能原样回传"。
   */
  it("多行填写的请求头与查询参数随保存提交", async () => {
    const user = userEvent.setup()
    renderDialog({
      id: "s7",
      name: "中转站",
      enabled: true,
      type: "http",
      url: "https://api.example.com/usage",
    })

    await user.click(screen.getByRole("button", { name: "展开高级" }))
    await user.type(screen.getByPlaceholderText("如 page=1&size=20"), "page=2&size=5")
    // 一行一条：换行即"再加一个头"，不必先学会 JSON
    await user.type(
      screen.getByPlaceholderText("如 x-foo=bar（一行一条）"),
      "x-foo=bar{enter}x-org=acme"
    )
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    const sent = mocked.update.mock.calls[0][0]
    expect(sent.headers).toEqual({ "x-foo": "bar", "x-org": "acme" })
    // 存成对象而不是那串文本：服务端 joinURL 认的是对象/字符串两种写法
    expect(sent.query).toEqual({ page: "2", size: "5" })
  })

  it("basic 鉴权给出用户名输入框，改过的用户名随保存提交", async () => {
    // 后端把 auth.user 拼进 Authorization: Basic，没有输入项就只能发出
    // "空用户名:口令"，而界面上完全看不出少了东西
    const user = userEvent.setup()
    renderDialog({
      id: "s8",
      name: "中转站",
      enabled: true,
      type: "http",
      url: "https://api.example.com/usage",
      auth: { type: "basic", user: "alice" },
    })

    const name = screen.getByPlaceholderText("用户名")
    expect(name).toHaveValue("alice")
    // basic 的头名由后端定死，摆一个改了不生效的框只会让人以为自己配错了
    expect(screen.queryByPlaceholderText("x-api-key")).not.toBeInTheDocument()

    await user.clear(name)
    await user.type(name, "bob")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    // type 也要跟着回来：只发 user 会被服务端当成"改了整套鉴权"
    expect(mocked.update.mock.calls[0][0].auth).toEqual({ type: "basic", user: "bob" })
  })

  it("bearer 鉴权不给用户名框，头名照旧可改", async () => {
    renderDialog({
      id: "s9",
      name: "中转站",
      enabled: true,
      type: "http",
      url: "https://api.example.com/usage",
      auth: { type: "bearer", header: "Authorization" },
    })

    expect(screen.queryByPlaceholderText("用户名")).not.toBeInTheDocument()
    expect(screen.getByDisplayValue("Authorization")).toBeInTheDocument()
  })

  it("改一处、存一次，别的字段不受牵连", async () => {
    const user = userEvent.setup()
    renderDialog({
      id: "s4",
      name: "超算",
      enabled: true,
      type: "builtin",
      builtin: "scnet",
      baseUrl: "https://www.scnet.cn",
      path: "/console/api/plan",
      timeout: 45,
      warningAt: 90,
      env: { SCNET_USER: "alice", SCNET_PASS: "****4321" },
    })

    const name = screen.getByDisplayValue("超算")
    await user.clear(name)
    await user.type(name, "超算（主）")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0]).toMatchObject({
      id: "s4",
      name: "超算（主）",
      builtin: "scnet",
      baseUrl: "https://www.scnet.cn",
      path: "/console/api/plan",
      timeout: 45,
      warningAt: 90,
    })
  })
})

describe("数据源编辑器 · 错误", () => {
  it("试跑失败把上游的原话摆出来，而不是只说失败", async () => {
    const user = userEvent.setup()
    mocked.test.mockResolvedValue({
      ok: false,
      items: null,
      status: "unknown",
      error: "登录失败：账号或口令不对",
      durationMs: 120,
    })
    renderDialog({ id: "s5", name: "超算", enabled: true, type: "builtin", builtin: "scnet" })

    await user.click(screen.getByRole("button", { name: "试跑（不保存）" }))

    expect(await screen.findByText("试跑失败")).toBeInTheDocument()
    expect(screen.getByText("登录失败：账号或口令不对")).toBeInTheDocument()
  })

  it("保存失败保留对话框，原文进 toast 之外还能重试", async () => {
    const user = userEvent.setup()
    mocked.update.mockRejectedValue(new Error("数据源 id 已存在：s1"))
    const { onSaved } = renderDialog({ id: "s1", name: "超算", enabled: true, type: "builtin", builtin: "scnet", env: { SCNET_USER: "a", SCNET_PASS: "b" } })

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    // 没保存成功就不能当成保存成功：对话框还在，重试按钮还能按
    expect(onSaved).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: "保存" })).toBeEnabled()
  })
})

describe("数据源编辑器 · 删除", () => {
  it("删除按 id 发，成功后把删除事件交给页面", async () => {
    const user = userEvent.setup()
    mocked.remove.mockResolvedValue(undefined)
    const { onSaved } = renderDialog({ id: "s6", name: "旧源", enabled: true, type: "builtin", builtin: "deepseek" })

    await user.click(screen.getByRole("button", { name: "删除" }))

    await waitFor(() => expect(mocked.remove).toHaveBeenCalledWith("s6"))
    expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: "s6" }), true)
  })
})
