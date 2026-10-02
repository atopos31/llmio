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
  reclaimStorage,
  rollbackCompression,
  runCompression,
  updateCompressionPolicy,
  type AnthropicCountTokens,
  type CompressionDBStats,
  type CompressionStatus,
  type LogCleanupPolicy,
  type LogCleanupRecord,
  type ReclaimState,
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
  reclaimStorage: vi.fn(),
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
  reclaimStorage: vi.mocked(reclaimStorage),
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
    policy: {
      enabled: false,
      batch_rows: 64,
      batch_bytes: 32 * 1024 * 1024,
      quiesce_sec: 60,
      batch_interval_ms: 0,
    },
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
      bytes_total: 0,
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
      bytes_total: 0,
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
      stats_at: 0,
      stale: false,
    },
    running: false,
    reclaiming: false,
    reclaim: {
      status: "idle",
      source: "",
      page_size: 0,
      freed_pages: 0,
      freed_bytes: 0,
      file_size_before: 0,
      file_size_after: 0,
      freelist_before: 0,
      freelist_after: 0,
      calls: 0,
      duration_ms: 0,
      started_at: "",
      finished_at: "",
      stop_reason: "",
      last_error: "",
    },
    ...over,
  }
}

/** 一份"回收过一轮、正常收工"的记录。 */
function reclaimState(over: Partial<ReclaimState> = {}): ReclaimState {
  return { ...compressionStatus().reclaim, ...over }
}

/**
 * 一份非空的库现状。`compressionStatus().db` 的类型是 `| null`（量不到时后端
 * 如实给 null），而绝大多数用例要的是"量到了"的那一份——用这个抽出来，
 * 免得每处都写一遍非空断言。
 */
function dbStats(over: Partial<CompressionDBStats> = {}): CompressionDBStats {
  const db = compressionStatus().db
  if (db === null) throw new Error("compressionStatus() 的 db 不该是 null")
  return { ...db, ...over }
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
 *  1. **压缩比的分子与分母必须盖同一批行。** `state.bytes_before` 只统计
 *     **扫过的行**，拿它去除以全库的落库字节会得出一个看起来很专业的错误数字
 *     （半程时恰好是真值的一半）。分子要用 `state.bytes_total`——全表原文合计。
 *     但**别拿"还有没有明文行"当门**：压不动的行会被有意留成明文，
 *     那个条件永远不成立，比值就一次都不会显示。
 *  2. **没备份时那道门。** 迁移原地改写历史行，出事时唯一不需要相信压缩代码
 *     的退路是"把备份盖回去"。所以没探到备份时不许直接开跑，必须过一次确认，
 *     而且确认之后要**如实**把"无备份"这件事传下去。
 */
describe("系统配置页 · 数据库压缩", () => {
  /** 归零的一份状态，各用例只改自己关心的那几个字段。不传即"全默认"。 */
  function withCompression(over: Partial<CompressionStatus> = {}) {
    mocked.getCompression.mockResolvedValue(compressionStatus(over))
  }

  it("迁移完成后给出压缩比：原始 ÷ 真正落库", async () => {
    // 1 GiB 原始，落库 = 8 MiB（input 列）+ 2 MiB（组表）+ 0 块表 = 10 MiB ⇒ 约 102.4×
    withCompression({
      state: {
        ...compressionStatus().state,
        status: "done",
        bytes_total: 1024 ** 3,
        bytes_before: 1024 ** 3,
        bytes_after: 0,
      },
      db: dbStats({
        pending_rows: 0,
        framed_rows: 100,
        input_column_bytes: 8 * 1024 ** 2,
        block_group_rows: 1,
        block_group_bytes: 2 * 1024 ** 2,
      }),
    })

    renderPage()

    expect(await screen.findByText("102.4×")).toBeInTheDocument()
    expect(screen.getByText("原始 ÷ 真正落库")).toBeInTheDocument()
  })

  /**
   * 这一条是 bug 回归。曾经的判据是 `pending_rows === 0`，而 pending_rows 数的
   * 是 `typeof(input)='text'`——**压不动的行**（帧比原文还大）会被迁移有意留成
   * 明文，于是它永远到不了 0（真机上有 15 行），压缩比跟着一次都没显示过。
   *
   * 所以这里刻意让 pending_rows 非 0 而状态是 done：这正是真机上迁移跑完的样子。
   */
  it("有压不动而留成明文的行时，压缩比照常显示", async () => {
    withCompression({
      state: {
        ...compressionStatus().state,
        status: "done",
        bytes_total: 1024 ** 3,
        bytes_before: 1024 ** 3,
        scanned: 12483,
        total_rows: 12483,
        skipped: 15,
      },
      db: dbStats({
        pending_rows: 15, // 压不动的那几行，永远还是明文
        framed_rows: 12468,
        input_column_bytes: 8 * 1024 ** 2,
        block_group_rows: 891,
        block_group_bytes: 2 * 1024 ** 2,
      }),
    })

    renderPage()

    expect(await screen.findByText("102.4×")).toBeInTheDocument()
    expect(screen.queryByText("还有未迁移的行，此比值不完整")).not.toBeInTheDocument()
  })

  /**
   * 中途报的也必须是个**真数**：分子（全表原文）与分母（全表落库）盖的是同一批
   * 行，所以"此刻全库比原文小 2 倍"这句话在半程就是成立的。用 bytes_before 当
   * 分子则会算出真值的一半——那不是保守，是错的。
   */
  it("迁移跑到一半就给出比值，并说明它还会涨", async () => {
    withCompression({
      state: {
        ...compressionStatus().state,
        status: "running",
        bytes_total: 2 * 1024 ** 3,
        bytes_before: 1024 ** 3, // 只扫了一半 —— 它**不是**分子
        scanned: 40,
        total_rows: 100,
      },
      db: dbStats({
        pending_rows: 60, // 还有 60 行是明文，占了分母的大半
        framed_rows: 40,
        input_column_bytes: 1024 ** 3,
      }),
    })

    renderPage()

    // 2 GiB ÷ 1 GiB = 2.0×（若误用 bytes_before 会得到 1.0×）
    expect(await screen.findByText("2.0×")).toBeInTheDocument()
    expect(screen.queryByText("1.0×")).not.toBeInTheDocument()
    expect(
      screen.getByText("迁移进行中：此值会随行改形态继续上升（起跑时约 1.0×）")
    ).toBeInTheDocument()
  })

  it("还没量过原文合计时如实说没量过，而不是显示一个空比值", async () => {
    withCompression({
      db: dbStats({ pending_rows: 12483, input_column_bytes: 5 * 1024 ** 3 }),
    })

    renderPage()

    expect(
      await screen.findByText("还没量过：点一次「开始迁移」即可量出（只读记录头，不改数据）")
    ).toBeInTheDocument()
    // 三个读数格里的比值格是"—"
    expect(screen.getAllByText("—").length).toBeGreaterThan(0)
  })

  /**
   * 这一条是**真机反馈的回归**：迁移正在写库的时候，状态接口那份"库的现状"
   * 会撞上 SQLITE_BUSY。原先它一路 500 上去，整张卡报错、用户得手动刷新；
   * 更要命的是「开始迁移」那个动作也要量一次库（只为一个文件大小），于是连
   * 迁移都起不来。
   *
   * 现在的约定：量不到就给 `db: null`，**不画 0**。`auto_vacuum=0` 是个有确切
   * 含义的值（文件永不缩），拿它顶替"不知道"会把警告画反。
   */
  it("量不到库的现状时如实说量不到，不拿 0 冒充（更不整卡报错）", async () => {
    withCompression({ db: null })

    renderPage()

    // 整块给一句短的；"真正落库"那一格另给一句说清原因（这里只该出现一次）
    expect(await screen.findByText("库的现状暂时量不到，会自动刷新")).toBeInTheDocument()
    expect(screen.getAllByText(/这一读撞上了锁/)).toHaveLength(1)
    // auto_vacuum 那条警告不能出现——我们并不知道它是多少
    expect(screen.queryByText(/当前 auto_vacuum=0/)).not.toBeInTheDocument()
    // 进度照常显示：它是另一条读法（读 Config），不该被库现状拖下水
    expect(screen.getByText("进度")).toBeInTheDocument()
  })

  it("库的现状是上一次量到的时说清是几点量的", async () => {
    const at = new Date("2026-10-02T22:30:00").getTime()
    withCompression({
      db: dbStats({ stats_at: at, stale: true }),
    })

    renderPage()

    const hint = await screen.findByText(/库的现状是 .* 量到的（此刻繁忙，量不动）/)
    expect(hint.textContent).toContain(new Date(at).toLocaleTimeString())
  })

  it("auto_vacuum=0 时说清省下的空间还在文件里，要 VACUUM 才能还给磁盘", async () => {
    withCompression({
      db: dbStats({
        auto_vacuum: 0,
        page_size: 4096,
        freelist_count: 256, // 1 MiB
      }),
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
      db: dbStats({ file_size: 1024 ** 3 }),
    })

    renderPage()
    await user.click(await screen.findByRole("button", { name: "开始迁移" }))

    const dialog = await screen.findByRole("dialog")
    expect(
      within(dialog).getByText(/多半是迁移前的旧备份——盖回去会丢数据/)
    ).toBeInTheDocument()
    expect(mocked.runCompression).not.toHaveBeenCalled()
  })

  /**
   * 批间停顿是这次新加的**占用率旋钮**（迁移每批持写锁约 185 ms，停多久决定
   * 库有多闲）。两件事必须钉住：它能被保存下去，以及**调过之后在卡片上看得见**
   * ——一个改完就看不见的旋钮会被忘在那儿，然后有人对着一个慢了三倍的迁移查半天。
   */
  it("调整策略时把批间停顿一起提交", async () => {
    const user = userEvent.setup()
    mocked.updateCompressionPolicy.mockResolvedValue(compressionStatus().policy)

    renderPage()
    await user.click(await screen.findByRole("button", { name: "调整策略" }))

    const dialog = await screen.findByRole("dialog")
    const interval = within(dialog).getByLabelText("批间停顿（毫秒）")
    await user.clear(interval)
    await user.type(interval, "200")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() =>
      expect(mocked.updateCompressionPolicy).toHaveBeenCalledWith(
        expect.objectContaining({ batch_interval_ms: 200 })
      )
    )
  })

  it("批间停顿非 0 时在卡片上露出来，为 0 时不占位置", async () => {
    withCompression({
      policy: { ...compressionStatus().policy, batch_interval_ms: 200 },
    })

    renderPage()

    expect(await screen.findByText("批间停 200 ms")).toBeInTheDocument()
  })

  it("批间停顿为 0（默认）时卡片上不出现这一项", async () => {
    withCompression()

    renderPage()

    // 先等这只卡片真的渲染完（策略行里有「调整策略」），否则"没找到"可能只是还没渲染
    await screen.findByRole("button", { name: "调整策略" })
    expect(screen.queryByText(/批间停/)).not.toBeInTheDocument()
  })

  it("一行都没压过时回滚按钮是禁用的", async () => {
    withCompression({ db: dbStats({ framed_rows: 0 }) })

    renderPage()

    expect(await screen.findByRole("button", { name: "回滚为明文" })).toBeDisabled()
  })

  it("已有压缩行时回滚要先过一道明确警告", async () => {
    const user = userEvent.setup()
    withCompression({
      state: { ...compressionStatus().state, status: "done", bytes_before: 1024 ** 3 },
      db: dbStats({ framed_rows: 12468, pending_rows: 0 }),
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

  /**
   * 空间回收那一块。它要回答三个问题，三条各有一个用例：
   *
   *  1. **上一轮干了什么**——放掉多少、为什么停的。`stop_reason` 不是日志字段，
   *     它是"这次回收到底有没有用"的结论（尤其 `no_auto_vacuum`：空了就是空了）。
   *  2. **按钮为什么点不动**——每种灰都得说得出理由。一个不写原因的灰按钮
   *     会被当成故障，而 `no_auto_vacuum` 那一种根本不是"暂时不行"，是这条路
   *     在这个库上走不通。
   *  3. **点下去之前要知道代价**——它全程持写锁，写请求会排队并失败。
   */
  describe("空间回收", () => {
    it("如实说出上一轮放掉多少、为什么停的、文件变了多少", async () => {
      withCompression({
        db: dbStats({
          auto_vacuum: 2,
          freelist_count: 336_000,
          file_size: 7.05 * 1024 ** 3,
        }),
        reclaim: reclaimState({
          status: "done",
          source: "startup",
          freed_pages: 1_374_982,
          freed_bytes: 5 * 1024 ** 3,
          file_size_before: 7.05 * 1024 ** 3,
          file_size_after: 1.46 * 1024 ** 3,
          calls: 336,
          duration_ms: 793_000,
          stop_reason: "empty",
        }),
      })

      renderPage()

      expect(await screen.findByText(/已放掉 5/)).toBeInTheDocument()
      // 为什么停的——这一栏是结论，不是日志。
      expect(screen.getByText(/放完了/)).toBeInTheDocument()
      expect(screen.getByText(/启动时做的/)).toBeInTheDocument()
      // 文件真的缩了，这一步在界面上看得见。
      // formatBytes 只保留一位小数（7.05 → 7.1），所以这里跟着它写。
      expect(screen.getByText(/文件 7\.1 GB → 1\.5 GB/)).toBeInTheDocument()
    })

    /**
     * `stalled` 是唯一一个 status=done 却带着 last_error 的收工。
     * 它的存在理由是：真机上那个 pragma 曾经被驱动**静默忽略参数**（一次只放一页），
     * 下一次可能变成一页都不放——那时"到点收工、剩余下次再放"和正常收工长得一模一样，
     * 于是报错必须**跟着它一起显示**，不能因为 status 是 done 就藏起来。
     */
    it("原地踏步时：既报出这个理由，也把具体数字显示出来", async () => {
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 1_000 }),
        reclaim: reclaimState({
          status: "done",
          freed_pages: 0,
          stop_reason: "stalled",
          last_error: "这批放完 freelist 反而没减少：1000 → 1000",
        }),
      })

      renderPage()

      expect(await screen.findByText(/没见 freelist 变少/)).toBeInTheDocument()
      // 关键：status 是 done，但这句话照样得看得见。
      expect(screen.getByText(/1000 → 1000/)).toBeInTheDocument()
    })

    it("库没开 auto_vacuum 时说明回收是空操作，不许人点一个注定没反应的按钮", async () => {
      withCompression({
        // 有空洞，但库是 auto_vacuum=0：这种情况下 `PRAGMA incremental_vacuum`
        // 实测 0.000 秒返回、文件一字节不缩。按钮亮着才是骗人。
        db: dbStats({ auto_vacuum: 0, freelist_count: 1_374_982 }),
      })

      renderPage()

      const button = await screen.findByRole("button", { name: /回收/ })
      expect(button).toBeDisabled()
      expect(screen.getByText(/增量回收在这里是空操作/)).toBeInTheDocument()
      expect(screen.getByText(/DB_AUTO_VACUUM_REBUILD=on/)).toBeInTheDocument()
    })

    it("没有可回收的页时按钮禁用，并说清是没空洞而不是坏了", async () => {
      withCompression({ db: dbStats({ auto_vacuum: 2, freelist_count: 0 }) })

      renderPage()

      const button = await screen.findByRole("button", { name: /回收/ })
      expect(button).toBeDisabled()
      expect(screen.getByText(/文件里已经没有空洞了/)).toBeInTheDocument()
    })

    it("迁移在跑时回收让路，并说明是同一把维护锁", async () => {
      withCompression({
        running: true,
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })

      renderPage()

      expect(await screen.findByText(/两者抢同一把维护锁/)).toBeInTheDocument()
    })

    it("点下去之前先过一次确认，把写锁的代价说在点之前", async () => {
      const user = userEvent.setup()
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })
      mocked.reclaimStorage.mockResolvedValue({ started: true })

      renderPage()
      // 按钮上带可回收的量，所以按名字前缀找。
      await user.click(await screen.findByRole("button", { name: /^回收 / }))

      const dialog = await screen.findByRole("dialog")
      expect(within(dialog).getByText(/写请求会排队/)).toBeInTheDocument()
      expect(mocked.reclaimStorage).not.toHaveBeenCalled()

      await user.click(within(dialog).getByRole("button", { name: "开始回收" }))
      await waitFor(() => expect(mocked.reclaimStorage).toHaveBeenCalledTimes(1))
    })

    it("回收在跑时按钮变成回收中并且不再可点", async () => {
      withCompression({
        reclaiming: true,
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })

      renderPage()

      // 两处「回收中」：读数行一处、按钮一处。
      const labels = await screen.findAllByText("回收中")
      expect(labels.length).toBeGreaterThanOrEqual(2)
      for (const button of screen.getAllByRole("button")) {
        if (button.textContent?.includes("回收中")) expect(button).toBeDisabled()
      }
    })
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
