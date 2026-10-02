import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ConfigPage from "@/routes/config"
import {
  configAPI,
  getCleanupHistory,
  getCompression,
  getPeakCalendar,
  pauseCompression,
  rollbackCompression,
  runCompression,
  updateCompressionPolicy,
  type AnthropicCountTokens,
  type CompressionStatus,
  type LogCleanupPolicy,
  type LogCleanupRecord,
} from "@/lib/api"

// 这一页的数据全部经过 api.ts，切断它就断开了本页的全部 IO。
// configAPI 是个对象，mock 工厂里得整个换掉（测试里再取 getConfig/updateConfig）。
vi.mock("@/lib/api", () => ({
  configAPI: {
    getConfig: vi.fn(),
    updateConfig: vi.fn(),
  },
  getCleanupHistory: vi.fn(),
  testCountTokens: vi.fn(),
  // 峰谷计费卡片与数据库压缩卡片都自带取数，挂在这一页上。不给它们默认值的话，
  // 它们会渲染成错误态并各多出一个"重试"按钮，把这一页原有的重试断言搅成"找到多个"。
  getPeakCalendar: vi.fn(),
  getCompression: vi.fn(),
  runCompression: vi.fn(),
  pauseCompression: vi.fn(),
  rollbackCompression: vi.fn(),
  updateCompressionPolicy: vi.fn(),
}))

const mocked = {
  getConfig: vi.mocked(configAPI.getConfig),
  updateConfig: vi.mocked(configAPI.updateConfig),
  getCleanupHistory: vi.mocked(getCleanupHistory),
  getPeakCalendar: vi.mocked(getPeakCalendar),
  getCompression: vi.mocked(getCompression),
  runCompression: vi.mocked(runCompression),
  pauseCompression: vi.mocked(pauseCompression),
  rollbackCompression: vi.mocked(rollbackCompression),
  updateCompressionPolicy: vi.mocked(updateCompressionPolicy),
}

/**
 * 系统配置页的四态测试（方案 §5.2 的 C 层）。
 *
 * 这一页的失败态与另三个列表页不同：卡片上显示的是默认值，编辑与保存本身
 * 不依赖这次读取，因此失败不作整页替换，只在顶部说明"下面显示的可能不是
 * 已保存的值"。断言因此钉两件事——失败要说出来，且页面仍可用。
 *
 * 后端对"这个 key 不存在"返回的是 200 + 空 value，与请求失败是两回事：
 * 前者照旧说"未配置"是对的，后者不能说。
 */
function config(value: unknown) {
  return { key: "k", value: value === "" ? "" : JSON.stringify(value) }
}

function historyRecord(over: Partial<LogCleanupRecord> = {}): LogCleanupRecord {
  return {
    ID: 1,
    CreatedAt: "2026-09-01T03:00:00Z",
    Source: "scheduled",
    Type: "days",
    RetentionDays: 30,
    DeletedCount: 12,
    DurationMs: 42,
    ...over,
  }
}

function historyPage(records: LogCleanupRecord[]) {
  return { data: records, total: records.length, page: 1, page_size: 10, pages: 1 }
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ConfigPage />
    </MemoryRouter>
  )
}

/**
 * 一份"从未运行过"的压缩状态。
 *
 * 默认值刻意选**空库**：它的读数是这张卡最不显眼的一组（全 0、未找到备份），
 * 于是这一页其它断言不会被压缩卡的数字碰巧撞上。
 */
function compressionStatus(over: Partial<CompressionStatus> = {}): CompressionStatus {
  return {
    policy: { enabled: false, batch_rows: 64, batch_bytes: 32 * 1024 * 1024, quiesce_sec: 60 },
    state: {
      status: "idle",
      last_id: 0,
      max_id: 0,
      total_rows: 0,
      scanned: 0,
      packed: 0,
      skipped: 0,
      bytes_before: 0,
      bytes_after: 0,
      attempts: 0,
      last_error: "",
      started_at: "",
      finished_at: "",
    },
    decompress_state: {
      status: "idle",
      last_id: 0,
      max_id: 0,
      total_rows: 0,
      scanned: 0,
      packed: 0,
      skipped: 0,
      bytes_before: 0,
      bytes_after: 0,
      attempts: 0,
      last_error: "",
      started_at: "",
      finished_at: "",
    },
    backup: { path: "", size: 0, mtime: "", at: "", source: "missing" },
    db: {
      path: "/tmp/llmio.db",
      file_size: 0,
      page_size: 4096,
      page_count: 0,
      freelist_count: 0,
      auto_vacuum: 0,
      rows: 0,
      pending_rows: 0,
      framed_rows: 0,
      block_rows: 0,
      block_group_rows: 0,
      block_group_bytes: 0,
      input_column_bytes: 0,
    },
    running: false,
    ...over,
  }
}

/** 让两个 getConfig 分别返回给定值；不传即"未配置" */
function configReplies(
  anthropic: AnthropicCountTokens | "" = "",
  cleanup: Partial<LogCleanupPolicy> | "" = ""
) {
  mocked.getConfig.mockImplementation(async (key: string) =>
    key === "anthropic_count_tokens" ? config(anthropic) : config(cleanup)
  )
}

beforeEach(async () => {
  vi.clearAllMocks()
  configReplies()
  mocked.getCleanupHistory.mockResolvedValue(historyPage([]))
  // 峰谷的工作日日历卡片在本页每次渲染都会取一次数；这里给服务端的默认日历，
  // 卡片显示"默认时区 + 默认工作日"，这一页的其它断言不受它影响
  mocked.getPeakCalendar.mockResolvedValue({
    timezone: "Asia/Shanghai",
    weekdays: [1, 2, 3, 4, 5],
    dateOverrides: {},
  })
  mocked.getCompression.mockResolvedValue(compressionStatus())
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("系统配置页 · 加载", () => {
  it("读取未落地时声明正在加载，不说未配置", async () => {
    mocked.getConfig.mockReturnValue(new Promise(() => {}))

    renderPage()

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("加载中"))
    expect(screen.queryByText("未配置")).not.toBeInTheDocument()
  })
})

describe("系统配置页 · 空", () => {
  it("从未配置过时逐项说未配置，并用默认值填充另一张卡", async () => {
    renderPage()

    // base_url / api_key / version 三项都没有
    expect(await screen.findAllByText("未配置")).toHaveLength(3)
    expect(screen.getByText("未启用")).toBeInTheDocument()
    expect(screen.getByText("保留最近 30 天")).toBeInTheDocument()
  })
})

describe("系统配置页 · 错误", () => {
  it("读取失败时在顶部说明，并说清下面显示的可能不是已保存的值", async () => {
    mocked.getConfig.mockRejectedValueOnce(new Error("500 internal error"))

    renderPage()

    expect(await screen.findByText("读取现有配置失败")).toBeInTheDocument()
    expect(screen.getByText("下面显示的可能不是已保存的值，保存前请先重试读取。")).toBeInTheDocument()
    expect(screen.getByText("500 internal error")).toBeInTheDocument()
    // 不整页替换：卡片与编辑入口照旧可用
    expect(screen.getByText("Anthropic 令牌计数配置")).toBeInTheDocument()
  })

  it("重试后按已保存的值重新渲染，提示随之消失", async () => {
    const user = userEvent.setup()
    mocked.getConfig.mockRejectedValueOnce(new Error("500 internal error"))
    renderPage()
    await screen.findByText("读取现有配置失败")

    configReplies(
      { base_url: "https://saved.example.com", api_key: "sk-ant-api03-secret", version: "2023-06-01" },
      { enabled: true, retention_days: 7 }
    )
    await user.click(screen.getByRole("button", { name: "重试" }))

    expect(await screen.findByText("https://saved.example.com")).toBeInTheDocument()
    expect(screen.queryByText("读取现有配置失败")).not.toBeInTheDocument()
  })

  it("存进去的配置坏了（JSON 解析不出来）也算失败，不静默当成未配置", async () => {
    mocked.getConfig.mockImplementation(async (key: string) => ({
      key,
      value: key === "anthropic_count_tokens" ? "{ 这不是 JSON" : "",
    }))

    renderPage()

    expect(await screen.findByText("读取现有配置失败")).toBeInTheDocument()
  })

  it("清理历史取数失败时给出原文与重试，而不是说暂无数据", async () => {
    const user = userEvent.setup()
    mocked.getCleanupHistory.mockRejectedValueOnce(new Error("历史服务不可用"))
    configReplies()
    renderPage()
    await screen.findAllByText("未配置")

    await user.click(screen.getByRole("button", { name: "查看清理历史" }))

    const dialog = await screen.findByRole("dialog")
    expect(await within(dialog).findByText("清理历史加载失败")).toBeInTheDocument()
    expect(within(dialog).getByText("历史服务不可用")).toBeInTheDocument()
    expect(within(dialog).queryByText("暂无数据")).not.toBeInTheDocument()
  })

  it("清理历史重试后就地恢复成表", async () => {
    const user = userEvent.setup()
    mocked.getCleanupHistory.mockRejectedValueOnce(new Error("历史服务不可用"))
    renderPage()
    await screen.findAllByText("未配置")
    await user.click(screen.getByRole("button", { name: "查看清理历史" }))
    const dialog = await screen.findByRole("dialog")
    await within(dialog).findByText("清理历史加载失败")

    mocked.getCleanupHistory.mockResolvedValue(historyPage([historyRecord()]))
    await user.click(within(dialog).getByRole("button", { name: /重试/ }))

    expect(await within(dialog).findByText("12")).toBeInTheDocument()
    expect(within(dialog).queryByText("清理历史加载失败")).not.toBeInTheDocument()
  })
})

/**
 * 数据库压缩卡片。
 *
 * 这张卡上最容易做错的两件事，测试就钉这两件：
 *
 *  1. **压缩比什么时候才该显示。** `state.bytes_before` 只统计**扫过的行**，
 *     库里还有没迁的行时它是个不完整的分子。拿它去除以全库的落库字节，
 *     会得出一个看起来很专业的错误数字——比不显示更糟。
 *  2. **没备份时那道门。** 迁移原地改写历史行，出事时唯一不需要相信压缩代码
 *     的退路是"把备份盖回去"。所以没探到备份时不许直接开跑，必须过一次确认，
 *     而且确认之后要**如实**把"无备份"这件事传下去。
 */
describe("系统配置页 · 数据库压缩", () => {
  /** 归零的一份状态，各用例只改自己关心的那几个字段。 */
  function withCompression(over: Partial<CompressionStatus>) {
    mocked.getCompression.mockResolvedValue(compressionStatus(over))
  }

  it("迁移完成后给出压缩比：原始 ÷ 真正落库", async () => {
    // 1 GiB 原始，落库 = 8 MiB（input 列）+ 2 MiB（组表）+ 0 块表 = 10 MiB ⇒ 约 102.4×
    withCompression({
      state: {
        ...compressionStatus().state,
        status: "done",
        bytes_before: 1024 ** 3,
        bytes_after: 0,
      },
      db: {
        ...compressionStatus().db,
        pending_rows: 0,
        framed_rows: 100,
        input_column_bytes: 8 * 1024 ** 2,
        block_group_rows: 1,
        block_group_bytes: 2 * 1024 ** 2,
      },
    })

    renderPage()

    expect(await screen.findByText("102.4×")).toBeInTheDocument()
    expect(screen.getByText("原始 ÷ 真正落库")).toBeInTheDocument()
  })

  it("还有未迁移的行时不报压缩比，改说这个比值不完整", async () => {
    withCompression({
      state: {
        ...compressionStatus().state,
        status: "paused",
        bytes_before: 1024 ** 3,
        scanned: 40,
        total_rows: 100,
      },
      db: {
        ...compressionStatus().db,
        pending_rows: 60, // 还有 60 行是明文
        framed_rows: 40,
        input_column_bytes: 8 * 1024 ** 2,
        block_group_bytes: 2 * 1024 ** 2,
      },
    })

    renderPage()

    expect(await screen.findByText("还有未迁移的行，此比值不完整")).toBeInTheDocument()
    expect(screen.queryByText(/×$/)).not.toBeInTheDocument()
    expect(screen.getByText("已暂停")).toBeInTheDocument()
  })

  it("auto_vacuum=0 时说清省下的空间还在文件里，要 VACUUM 才能还给磁盘", async () => {
    withCompression({
      db: {
        ...compressionStatus().db,
        auto_vacuum: 0,
        page_size: 4096,
        freelist_count: 256, // 1 MiB
      },
    })

    renderPage()

    expect(
      await screen.findByText(/当前 auto_vacuum=0，文件只会涨不会缩，要 VACUUM 才能真正还给磁盘/)
    ).toBeInTheDocument()
  })

  it("探到可用备份时直接开跑，不带「无备份」确认", async () => {
    const user = userEvent.setup()
    withCompression({
      backup: { path: "/tmp/llmio.db.bak", size: 999, mtime: "", at: "", source: "manual" },
    })
    mocked.runCompression.mockResolvedValue({
      started: true,
      full: false,
      backup: { path: "/tmp/llmio.db.bak", size: 999, mtime: "", at: "", source: "manual" },
    })

    renderPage()
    await user.click(await screen.findByRole("button", { name: "开始迁移" }))

    expect(mocked.runCompression).toHaveBeenCalledWith({
      full: false,
      acknowledge_no_backup: false,
    })
  })

  it("没探到备份时先弹确认，确认后如实带上 acknowledge_no_backup", async () => {
    const user = userEvent.setup()
    withCompression({
      backup: { path: "/tmp/llmio.db.bak", size: 0, mtime: "", at: "", source: "missing" },
    })
    mocked.runCompression.mockResolvedValue({
      started: true,
      full: false,
      backup: { path: "", size: 0, mtime: "", at: "", source: "forced" },
    })

    renderPage()
    await user.click(await screen.findByRole("button", { name: "开始迁移" }))

    // 先弹框，**没有**已经开跑
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText("没有探测到可用备份")).toBeInTheDocument()
    expect(within(dialog).getByText("未找到备份")).toBeInTheDocument()
    expect(mocked.runCompression).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole("button", { name: "确认无备份并继续" }))

    // 确认之后才提交，且这个确认必须传到后端——后端那道门靠的就是它
    await waitFor(() =>
      expect(mocked.runCompression).toHaveBeenCalledWith({
        full: false,
        acknowledge_no_backup: true,
      })
    )
  })

  it("备份比库还小时不许直接开跑（旧备份盖回去会丢数据）", async () => {
    const user = userEvent.setup()
    withCompression({
      backup: { path: "/tmp/llmio.db.bak", size: 1, mtime: "", at: "", source: "stale" },
      db: { ...compressionStatus().db, file_size: 1024 ** 3 },
    })

    renderPage()
    await user.click(await screen.findByRole("button", { name: "开始迁移" }))

    const dialog = await screen.findByRole("dialog")
    expect(
      within(dialog).getByText(/多半是迁移前的旧备份——盖回去会丢数据/)
    ).toBeInTheDocument()
    expect(mocked.runCompression).not.toHaveBeenCalled()
  })

  it("一行都没压过时回滚按钮是禁用的", async () => {
    withCompression({ db: { ...compressionStatus().db, framed_rows: 0 } })

    renderPage()

    expect(await screen.findByRole("button", { name: "回滚为明文" })).toBeDisabled()
  })

  it("已有压缩行时回滚要先过一道明确警告", async () => {
    const user = userEvent.setup()
    withCompression({
      state: { ...compressionStatus().state, status: "done", bytes_before: 1024 ** 3 },
      db: { ...compressionStatus().db, framed_rows: 12468, pending_rows: 0 },
    })
    mocked.rollbackCompression.mockResolvedValue({ started: true })

    renderPage()
    await user.click(await screen.findByRole("button", { name: "回滚为明文" }))

    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText(/回滚会让数据库明显变大/)).toBeInTheDocument()
    expect(mocked.rollbackCompression).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole("button", { name: "确认回滚" }))
    await waitFor(() => expect(mocked.rollbackCompression).toHaveBeenCalled())
  })
})

describe("系统配置页 · 有数据", () => {
  it("已保存的值逐项显示，密钥只露前八位", async () => {
    configReplies(
      { base_url: "https://saved.example.com", api_key: "sk-ant-api03-secret", version: "2023-06-01" },
      { enabled: true, retention_days: 7 }
    )

    renderPage()

    expect(await screen.findByText("https://saved.example.com")).toBeInTheDocument()
    expect(screen.getByText("sk-ant-a...")).toBeInTheDocument()
    // 整串不出现在页面上
    expect(screen.queryByText("sk-ant-api03-secret")).not.toBeInTheDocument()
    expect(screen.getByText("已启用")).toBeInTheDocument()
    expect(screen.getByText("保留最近 7 天")).toBeInTheDocument()
  })

  it("编辑后保存，提交的是对话框里的那一份", async () => {
    const user = userEvent.setup()
    configReplies(
      { base_url: "https://old.example.com", api_key: "sk-ant-api03-secret", version: "2023-06-01" }
    )
    mocked.updateConfig.mockResolvedValue({ key: "anthropic_count_tokens", value: "" })

    renderPage()
    await screen.findByText("https://old.example.com")
    await user.click(screen.getAllByRole("button", { name: "编辑配置" })[0])
    const dialog = await screen.findByRole("dialog")

    const baseUrl = within(dialog).getByDisplayValue("https://old.example.com")
    await user.clear(baseUrl)
    await user.type(baseUrl, "https://new.example.com")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() =>
      expect(mocked.updateConfig).toHaveBeenCalledWith("anthropic_count_tokens", {
        base_url: "https://new.example.com",
        api_key: "sk-ant-api03-secret",
        version: "2023-06-01",
      })
    )
  })

  it("清理历史按执行记录列出触发方式与删除数", async () => {
    const user = userEvent.setup()
    mocked.getCleanupHistory.mockResolvedValue(
      historyPage([historyRecord({ DeletedCount: 128, DurationMs: 0 })])
    )
    configReplies()

    renderPage()
    await screen.findAllByText("未配置")
    await user.click(screen.getByRole("button", { name: "查看清理历史" }))

    const dialog = await screen.findByRole("dialog")
    const row = (await within(dialog).findAllByRole("row")).at(-1) as HTMLElement
    expect(within(row).getByText("定时清理")).toBeInTheDocument()
    expect(within(row).getByText("128")).toBeInTheDocument()
    // 耗时为 0 时显式给一个占位，而不是"0ms"这种看起来像测出来的数
    expect(within(row).getByText("-")).toBeInTheDocument()
  })
})
