import { render, screen } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import LogChatPage from "@/routes/log-chat"
import { getChatIO, type ChatIO } from "@/lib/api"

// 这一页只调 getChatIO；Style 由后端原样给出，不属于 API 客户端职责。
vi.mock("@/lib/api", () => ({ getChatIO: vi.fn() }))
// 加载失败会往 toast 里塞一条消息，测试里不需要真的渲染 toast 容器。
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const mocked = { getChatIO: vi.mocked(getChatIO) }

function io(over: Partial<ChatIO> = {}): ChatIO {
  return {
    ID: 1,
    CreatedAt: "",
    UpdatedAt: "",
    LogId: 7,
    Input: "{}",
    ...over,
  }
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/logs/7/chat-io"]}>
      <Routes>
        <Route path="/logs/:logId/chat-io" element={<LogChatPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

beforeEach(async () => {
  vi.clearAllMocks()
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("日志会话页 · 通用 JSON", () => {
  it("透传接口的请求与响应都按通用 JSON 展示，不套聊天面板", async () => {
    mocked.getChatIO.mockResolvedValue(
      io({
        Style: "passthrough",
        Input: '{"input":"hello embed"}',
        OfString: '{"embedding":[0.1,0.2]}',
      }),
    )

    renderPage()

    // 请求与响应各一块，两块都带"通用 JSON"标记
    expect(await screen.findAllByText("通用 JSON")).toHaveLength(2)
    expect(screen.getAllByText(/这里没嗅探出聊天协议/)).toHaveLength(2)
    expect(screen.getByText(/hello embed/)).toBeInTheDocument()
    expect(screen.getByText(/embedding/)).toBeInTheDocument()
    // 没有消息面板/文本面板，说明没有硬套聊天视图
    expect(screen.queryByText("消息")).not.toBeInTheDocument()
  })

  it("gb18030 一侧挂「GB18030 已解码」徽章与说明，另一侧 utf-8 不受影响", async () => {
    mocked.getChatIO.mockResolvedValue(
      io({
        Style: "passthrough",
        Input: '{"input":"中文请求"}',
        InputEncoding: "gb18030",
        OfString: '{"embedding":[0.1,0.2]}',
        OutputEncoding: "utf-8",
      }),
    )

    renderPage()

    expect(await screen.findByText("GB18030 已解码")).toBeInTheDocument()
    // 说明行讲清"原文不可读、已解码"
    expect(screen.getByText(/已按 GB18030 解码/)).toBeInTheDocument()
    // 只有请求侧挂编码徽章：响应侧是 utf-8，不该跟着出现
    expect(screen.getAllByText("GB18030 已解码")).toHaveLength(1)
    expect(screen.queryByText("非 UTF-8")).not.toBeInTheDocument()
    expect(screen.queryByText(/非法字节已替换/)).not.toBeInTheDocument()
  })

  it("binary 一侧挂「非 UTF-8」徽章并说明非法字节已被替换", async () => {
    mocked.getChatIO.mockResolvedValue(
      io({
        Style: "passthrough",
        Input: '{"input":"\uFFFD\uFFFD"}',
        InputEncoding: "binary",
        OfString: '{"embedding":[0.1,0.2]}',
        OutputEncoding: "binary",
      }),
    )

    renderPage()

    // 两侧都判为 binary → 两个徽章、两段说明
    expect(await screen.findAllByText("非 UTF-8")).toHaveLength(2)
    expect(screen.getAllByText(/非法字节已替换/)).toHaveLength(2)
    expect(screen.queryByText("GB18030 已解码")).not.toBeInTheDocument()
  })

  it("请求 JSON 与响应 SSE 各自标注格式，互不影响", async () => {
    mocked.getChatIO.mockResolvedValue(
      io({
        Style: "passthrough",
        Input: '{"input":"hello embed"}',
        OfString: 'data: {"output":"ok"}\n\ndata: [DONE]',
      }),
    )

    renderPage()

    // 等两块都渲染出来，再断言格式徽章各一个
    expect(await screen.findAllByText("通用 JSON")).toHaveLength(2)
    expect(screen.getAllByText("JSON")).toHaveLength(1)
    expect(screen.getAllByText("SSE")).toHaveLength(1)
    expect(screen.queryByText("纯文本")).not.toBeInTheDocument()
  })

  it("Encoding 字段缺失时不挂任何编码徽章，也不加说明", async () => {
    mocked.getChatIO.mockResolvedValue(
      io({ Style: "passthrough", Input: '{"input":"hello embed"}', OfString: '{"embedding":[0.1,0.2]}' }),
    )

    renderPage()

    expect(await screen.findAllByText("通用 JSON")).toHaveLength(2)
    expect(screen.queryByText("GB18030 已解码")).not.toBeInTheDocument()
    expect(screen.queryByText("非 UTF-8")).not.toBeInTheDocument()
    expect(screen.queryByText(/已按 GB18030 解码/)).not.toBeInTheDocument()
    expect(screen.queryByText(/非法字节已替换/)).not.toBeInTheDocument()
  })

  it("两侧都没有 body 时格式标「空」而不是纯文本", async () => {
    mocked.getChatIO.mockResolvedValue(io({ Style: "passthrough", Input: "", OfString: null }))

    renderPage()

    expect(await screen.findAllByText("空")).toHaveLength(2)
    expect(screen.getAllByText("(空)")).toHaveLength(2)
    expect(screen.queryByText("纯文本")).not.toBeInTheDocument()
  })

  it("流式响应标出「流式 · N 个片段」，避免把片段数组当成普通 JSON 响应体", async () => {
    mocked.getChatIO.mockResolvedValue(
      io({
        Style: "passthrough",
        Input: '{"input":"hello embed"}',
        OfStringArray: ['{"embedding":[0.1]}', '{"embedding":[0.2]}'],
      }),
    )

    renderPage()

    expect(await screen.findByText("流式 · 2 个片段")).toBeInTheDocument()
    // 单段响应的用例不挂这个徽章：只在真是 SSE 分片时出现
    expect(screen.getAllByText(/流式 ·/)).toHaveLength(1)
  })

  it("响应嗅探不出协议、但请求体是聊天请求时，仍按聊天面板渲染", async () => {
    mocked.getChatIO.mockResolvedValue(
      io({
        Style: "passthrough",
        Input: '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}',
        OfString: "PNG binary dump",
      }),
    )

    renderPage()

    // 兜底嗅探认出请求体是 OpenAI 聊天请求 → 走 InputSection/OutputSection
    expect(await screen.findByText("消息")).toBeInTheDocument()
    expect(screen.queryByText("通用 JSON")).not.toBeInTheDocument()
  })
})
