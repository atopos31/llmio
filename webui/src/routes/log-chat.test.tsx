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
