import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ConfigPage from "@/routes/config"
import {
  estimateReclaim,
  estimateVacuumSec,
  hasRoomForVacuum,
  RECLAIM_BUDGET_SEC,
  RECLAIM_PAGES_PER_BATCH,
  RECLAIM_PAGES_PER_SEC,
  vacuumNeedBytes,
  VACUUM_BYTES_PER_SEC,
  VACUUM_DISK_MARGIN,
} from "@/lib/compression"
import {
  configAPI,
  getCleanupHistory,
  getCompression,
  getPeakCalendar,
  pauseCompression,
  reclaimStorage,
  rollbackCompression,
  runCompression,
  stopReclaim,
  updateCompressionPolicy,
  updateReclaimPolicy,
  vacuumStorage,
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
  // 模型自动填写卡片要用默认策略。它是 api.ts 里的一个常量，不是函数——
  // mock 工厂整个换掉这个模块，不给的话导入处就是 undefined，而它在
  // useState 的初值里被读到，会当场炸。
  defaultModelAutofillPolicy: {
    enabled: true,
    overwrite: false,
    allow_deprecated: false,
    sources: ["models.dev", "litellm"],
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
  stopReclaim: vi.fn(),
  updateReclaimPolicy: vi.fn(),
  vacuumStorage: vi.fn(),
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
  stopReclaim: vi.mocked(stopReclaim),
  updateReclaimPolicy: vi.mocked(updateReclaimPolicy),
  vacuumStorage: vi.mocked(vacuumStorage),
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
      // 默认给一个"空间充足"的空闲量。用 0 的话，所有拿默认库现状的用例都会
      // 撞上「重整」的磁盘预判（0 表示量不到，一律算不够），于是那条路上的
      // 用例测的就不是它们想测的东西了。
      disk_free_bytes: 1 << 40,
      auto_vacuum: 0,
      rows: 0,
      pending_rows: 0,
      framed_rows: 0,
      block_rows: 0,
      block_group_rows: 0,
      block_group_bytes: 0,
      stats_at: 0,
      stale: false,
    },
    running: false,
    reclaiming: false,
    reclaiming_kind: "",
    reclaim_stopping: false,
    // 默认关着。定时回收会自己占写锁，所以"默认"这一档必须是关——
    // 用例要测自动回收时显式打开，免得某天默认值被改反了还没人发现。
    reclaim_policy: { enabled: false, min_bytes: 256 * 1024 ** 2, check_interval_sec: 3600 },
    reclaim: {
      status: "idle",
      kind: "",
      source: "",
      continuous: false,
      rounds: 0,
      page_size: 0,
      freed_pages: 0,
      freed_bytes: 0,
      file_size_before: 0,
      file_size_after: 0,
      freelist_before: 0,
      freelist_after: 0,
      calls: 0,
      batch_size: 0,
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

  it("迁移完成后给出压缩比：原始 ÷ 实际占用", async () => {
    // 1 GiB 原始，库文件 10 MiB ⇒ 约 102.4×
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
        file_size: 10 * 1024 ** 2,
      }),
    })

    renderPage()

    expect(await screen.findByText("102.4×")).toBeInTheDocument()
    expect(screen.getByText("原始大小 ÷ 实际占用")).toBeInTheDocument()
  })

  /**
   * 这一条是两轮 bug 的回归，方向相反：
   *
   *  - 第一轮："实际占用"当时只算请求体列 + 分块组表 + 块表索引，漏掉了体量最大的
   *    响应体两列（真机上 1.47 GiB 的库，这两列明文占 1.36 GiB，而读数只报 62 MB），
   *    提示语还把漏掉的那半写成了定义。用户据此得出"压缩效果很好"，而实际几乎没有省。
   *  - 第二轮：补齐的方式是**逐列把载荷读出来求和**——`length(CAST(列 AS BLOB))`。
   *    这一句在真机 7.6 GiB 的库上要 4.24 秒，而 `journal_mode=delete` 下读事务挡写，
   *    聊天写请求等满 5 秒 busy_timeout 后整站 500。
   *
   * 现在的口径是**库文件本身**：`file_size` 一次 stat 就拿到，比逐列求和更便宜，
   * 也比逐列求和更准（页头、空闲页、索引都在里面）。所以这条用例钉的是：
   * 分母取 `file_size`，而**不**是任何按列相加出来的数。
   */
  it("实际占用取库文件大小，不按列相加", async () => {
    withCompression({
      state: {
        ...compressionStatus().state,
        status: "done",
        bytes_total: 1024 ** 3,
        bytes_before: 1024 ** 3,
      },
      // 库文件 512 MiB ⇒ 2.0×。若哪天有人改回"逐列相加"（这里没有任何列字节
      // 字段可加，值会退化成 —／不显示），这条断言就会红。
      db: dbStats({
        pending_rows: 0,
        framed_rows: 100,
        file_size: 512 * 1024 ** 2,
      }),
    })

    renderPage()

    expect(await screen.findByText("2.0×")).toBeInTheDocument()
    expect(screen.queryByText("102.4×")).not.toBeInTheDocument()
    expect(screen.getByText(/库文件在磁盘上实际占用的大小/)).toBeInTheDocument()
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
        file_size: 10 * 1024 ** 2,
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
        file_size: 1024 ** 3,
      }),
    })

    renderPage()

    // 2 GiB ÷ 1 GiB = 2.0×（若误用 bytes_before 会得到 1.0×）
    expect(await screen.findByText("2.0×")).toBeInTheDocument()
    expect(screen.queryByText("1.0×")).not.toBeInTheDocument()
    expect(
      screen.getByText("迁移进行中：随着更多数据被压缩，此值会继续上升（刚开始时约为 1.0×）")
    ).toBeInTheDocument()
  })

  it("还没量过原文合计时如实说没量过，而不是显示一个空比值", async () => {
    withCompression({
      db: dbStats({ pending_rows: 12483, file_size: 5 * 1024 ** 3 }),
    })

    renderPage()

    expect(
      await screen.findByText("尚未测量：点击一次「开始迁移」即可测出（只读取记录头部，不修改数据）")
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
    expect(await screen.findByText("数据库状态暂时无法读取，系统会自动重试")).toBeInTheDocument()
    expect(screen.getAllByText(/本次读取被阻塞/)).toHaveLength(1)
    // auto_vacuum 那条警告不能出现——我们并不知道它是多少
    expect(screen.queryByText(/当前数据库的 auto_vacuum 为 0/)).not.toBeInTheDocument()
    // 进度照常显示：它是另一条读法（读 Config），不该被库现状拖下水
    expect(screen.getByText("进度")).toBeInTheDocument()
  })

  it("库的现状是上一次量到的时说清是几点量的", async () => {
    const at = new Date("2026-10-02T22:30:00").getTime()
    withCompression({
      db: dbStats({ stats_at: at, stale: true }),
    })

    renderPage()

    const hint = await screen.findByText(/数据库状态为 .* 的读数（当前数据库繁忙，暂时无法重新读取）/)
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
      await screen.findByText(
        /当前数据库的 auto_vacuum 为 0，文件不会自动缩小，需要执行 VACUUM 才能把空间真正归还磁盘/
      )
    ).toBeInTheDocument()
  })

  /**
   * 有备份也照样先弹一次确认。"备份存在"与"用户知道这一下会发生什么"
   * 是两件事，拿前者当后者用，等于把一段没有备份时才显眼的警告
   * 也一并省掉了。这个窗在没有备份时会多出警告、按钮变红、并要求把
   * "无备份"记入存证——所以两种情形都走同一个入口，区别只在窗里的内容。
   */
  it("探到可用备份时也先弹确认，但窗里不带「无备份」的警告", async () => {
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

    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).queryByText("未检测到可用备份")).not.toBeInTheDocument()
    expect(mocked.runCompression).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole("button", { name: "开始迁移" }))
    await waitFor(() =>
      expect(mocked.runCompression).toHaveBeenCalledWith({
        full: false,
        acknowledge_no_backup: false,
      })
    )
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
    expect(within(dialog).getByText("未检测到可用备份")).toBeInTheDocument()
    // 探测结论与它找过的路径一起显示：只写"未找到备份"，
    // 用户不知道该去哪儿把备份放对。
    expect(within(dialog).getByText(/未找到备份 · \/tmp\/llmio\.db\.bak/)).toBeInTheDocument()
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
      within(dialog).getByText(/可能是迁移前的旧备份——用它还原会丢失数据/)
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
    // `selector: "input"`：标题旁那个「？」也是个带同名 aria-label 的按钮，
    // 不限定的话这里会同时命中两个。
    const interval = within(dialog).getByLabelText("批间暂停（毫秒）", { selector: "input" })
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

    expect(await screen.findByText("批间暂停 200 毫秒")).toBeInTheDocument()
  })

  it("批间停顿为 0（默认）时卡片上不出现这一项", async () => {
    withCompression()

    renderPage()

    // 先等这只卡片真的渲染完（策略行里有「调整策略」），否则"没找到"可能只是还没渲染
    await screen.findByRole("button", { name: "调整策略" })
    expect(screen.queryByText(/批间停/)).not.toBeInTheDocument()
  })

  /**
   * 标题旁那个「？」是这个表单唯一的说明出口。
   *
   * 每一个数字输入都有两件必须说、又不适合常显的事：**允许填多少**，以及
   * **填超了会怎样**（后端是夹回，不报错——也就是"你填的值和你实际跑的值
   * 可能不是一回事"）。这两句话不能只写在源码和文档里：用户看的是界面，
   * 而"填了 99999 会被悄悄改成 5000"这件事必须在他下手之前说。
   */
  it("输入项标题旁的「？」悬停时展开取值范围与越界行为", async () => {
    const user = userEvent.setup()

    renderPage()
    await user.click(await screen.findByRole("button", { name: "调整策略" }))

    const dialog = await screen.findByRole("dialog")
    await user.hover(within(dialog).getByRole("button", { name: "批间暂停（毫秒）" }))

    const tip = await screen.findByRole("tooltip")
    expect(tip.textContent).toContain("0–5000")
    expect(tip.textContent).toContain("自动调整")
  })

  /**
   * 打开弹窗**本身**不该带出任何帮助文案。
   *
   * 这条是修出来的：Radix 的 Tooltip 在悬停与聚焦时都展开，而弹窗打开时浏览器
   * 会把焦点自动放到第一个可聚焦元素上——「数据库压缩策略」窗里那个位置正好
   * 就是「后台自动推进」旁边的「？」。于是点开弹窗，那段说明自己就冒出来了。
   * 修法是只在「键盘走过来的聚焦」时才认这次展开，判据是 `:focus-visible`。
   *
   * 判据在浏览器里成立，但 jsdom 判断不了聚焦来源——它把「已聚焦」一律算成
   * `:focus-visible` 为真。所以这里把这一条选择器单独打桩成假，模拟鼠标点开
   * 弹窗时程序聚焦的样子；其余选择器照旧走真实实现，不干扰 Radix 自己的判断。
   */
  it("打开策略弹窗时不自带帮助文案（焦点是被弹窗放进去的，不是键盘走过来的）", async () => {
    const realMatches = Element.prototype.matches
    const spy = vi
      .spyOn(Element.prototype, "matches")
      .mockImplementation(function (this: Element, selector: string) {
        if (selector === ":focus-visible") return false
        return realMatches.call(this, selector)
      })

    try {
      const user = userEvent.setup()

      renderPage()
      await user.click(await screen.findByRole("button", { name: "调整策略" }))
      await screen.findByRole("dialog")

      expect(screen.queryByRole("tooltip")).not.toBeInTheDocument()
    } finally {
      spy.mockRestore()
    }
  })

  /**
   * 上一条的配对用例：被否掉的那次聚焦**不能把「不许展开」一直留着**。
   *
   * 拦一次聚焦要立一个标记，而标记若只由「下一次该展开时」消费，就会留下一个
   * 空档：鼠标点开弹窗（聚焦被否、标记立起），用户接着把鼠标移到「？」上打算看
   * 说明——这时标记还在，悬停也会被一起否掉，等于把帮助文案彻底弄没了。
   * 所以悬停（指针进入）要能作废那个标记。
   */
  it("弹窗收回帮助之后，鼠标再移上去仍然展得开", async () => {
    const realMatches = Element.prototype.matches
    const spy = vi
      .spyOn(Element.prototype, "matches")
      .mockImplementation(function (this: Element, selector: string) {
        if (selector === ":focus-visible") return false
        return realMatches.call(this, selector)
      })

    try {
      const user = userEvent.setup()

      renderPage()
      await user.click(await screen.findByRole("button", { name: "调整策略" }))
      const dialog = await screen.findByRole("dialog")
      expect(screen.queryByRole("tooltip")).not.toBeInTheDocument()

      await user.hover(within(dialog).getByRole("button", { name: "每批行数" }))
      const tip = await screen.findByRole("tooltip")
      expect(tip.textContent).toContain("1–4096")
    } finally {
      spy.mockRestore()
    }
  })

  it("回收策略弹窗里「检查周期」的「？」说清范围与默认值", async () => {
    const user = userEvent.setup()

    renderPage()
    await user.click(await screen.findByRole("button", { name: "回收策略" }))

    const dialog = await screen.findByRole("dialog")
    await user.hover(within(dialog).getByRole("button", { name: "检查周期（秒）" }))

    const tip = await screen.findByRole("tooltip")
    expect(tip.textContent).toContain("60–86400")
    expect(tip.textContent).toContain("3600")
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
    expect(within(dialog).getByText(/库文件会明显变大/)).toBeInTheDocument()
    // 回滚完会重置迁移进度——不说的话，用户回来看到"从未运行"会以为白跑了。
    expect(within(dialog).getByText(/迁移进度会被重置/)).toBeInTheDocument()
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

      expect(await screen.findByText(/已释放 5/)).toBeInTheDocument()
      // 为什么停的——这一栏是结论，不是日志。
      expect(screen.getByText(/空闲空间已全部回收/)).toBeInTheDocument()
      expect(screen.getByText(/服务启动时执行/)).toBeInTheDocument()
      // 文件真的缩了，这一步在界面上看得见。
      // formatBytes 只保留一位小数（7.05 → 7.1），所以这里跟着它写。
      expect(screen.getByText(/文件大小 7\.1 GB → 1\.5 GB/)).toBeInTheDocument()
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

      expect(await screen.findByText(/本批未释放任何空闲页/)).toBeInTheDocument()
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

      // 「回收 <量>」与旁边那个「回收策略」链接都含"回收"，按前缀取前者。
      const button = await screen.findByRole("button", { name: /^回收 / })
      expect(button).toBeDisabled()
      expect(screen.getByText(/回收不会产生任何效果/)).toBeInTheDocument()
      expect(screen.getByText(/DB_AUTO_VACUUM_REBUILD=on/)).toBeInTheDocument()
    })

    it("没有可回收的页时按钮禁用，并说清是没空洞而不是坏了", async () => {
      withCompression({ db: dbStats({ auto_vacuum: 2, freelist_count: 0 }) })

      renderPage()

      // 没东西可放时不报量，按钮退回"回收空间"——与旁边那个
      // 「回收策略」链接、以及有量可放时的「回收 <量>」都区分开。
      const button = await screen.findByRole("button", { name: /^回收空间$/ })
      expect(button).toBeDisabled()
      expect(screen.getByText(/数据库中已不存在空闲页/)).toBeInTheDocument()
    })

    it("迁移在跑时回收让路，并说明是同一把维护锁", async () => {
      withCompression({
        running: true,
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })

      renderPage()

      expect(await screen.findByText(/两者使用同一把维护锁/)).toBeInTheDocument()
    })

    it("点下去之前先过一次确认，把写锁的代价说在点之前", async () => {
      const user = userEvent.setup()
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })
      mocked.reclaimStorage.mockResolvedValue({ started: true, continuous: true })

      renderPage()
      // 按钮上带可回收的量，所以按名字前缀找。
      await user.click(await screen.findByRole("button", { name: /^回收 / }))

      const dialog = await screen.findByRole("dialog")
      // 默认是持续那一档，所以这里说的是"排队变慢但不会失败"——
      // 两档的代价不一样，说错了这个窗就白弹了。
      expect(within(dialog).getByText(/写入请求会排队并变慢，而不会失败/)).toBeInTheDocument()
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

    /**
     * 持续回收与单轮回收的账**不是一回事**，估算法因此分成两支：
     *
     *   - **单轮**：一轮封顶 90 秒，一个 5 GiB 的洞要十轮。这个"轮数"必须说出来——
     *     不说的话，用户点一次、看到"到点收工、还剩一大截"，只会以为它坏了。
     *   - **持续**：一轮恒等于一批（512 条语句恰好放 512 页），所以度量单位是**批**，
     *     而耗时里必须算上每批之间松手的那 0.1 秒。真机那趟 2,628 批，光松手就 4 分多钟——
     *     把它当零头漏掉，估算会比实际快一整截。
     */
    it("单轮按轮算、持续按批算，松手的等待也要计进去", () => {
      const single = estimateReclaim(RECLAIM_PAGES_PER_SEC * RECLAIM_BUDGET_SEC * 3, false)
      expect(single).toEqual({ rounds: 3, seconds: 3 * RECLAIM_BUDGET_SEC })

      // 不足一轮的零头也占一整轮：一轮跑不满 90 秒就收工，剩下的得下次再点。
      expect(estimateReclaim(1, false)).toEqual({ rounds: 1, seconds: RECLAIM_BUDGET_SEC })

      const pages = RECLAIM_PAGES_PER_BATCH * 10
      const continuous = estimateReclaim(pages, true)
      expect(continuous.rounds).toBe(10)
      // 10 批的搬运时间 + 10 次松手（各 0.1 秒）⇒ 严格大于纯搬运的折算值。
      expect(continuous.seconds).toBeGreaterThan(pages / RECLAIM_PAGES_PER_SEC)
      expect(continuous.seconds).toBe(Math.round(pages / RECLAIM_PAGES_PER_SEC + 10 * 0.1))
    })

    it("没东西可放时不做估算，给 0 而不是给一个假的时间", () => {
      for (const continuous of [false, true]) {
        expect(estimateReclaim(0, continuous)).toEqual({ rounds: 0, seconds: 0 })
        expect(estimateReclaim(Number.NaN, continuous)).toEqual({ rounds: 0, seconds: 0 })
      }
    })

    it("默认选「一路放到放完」，确认时把这个选择带进请求", async () => {
      const user = userEvent.setup()
      withCompression({ db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }) })
      mocked.reclaimStorage.mockResolvedValue({ started: true, continuous: true })

      renderPage()
      await user.click(await screen.findByRole("button", { name: /^回收 / }))

      const dialog = await screen.findByRole("dialog")
      // 默认开着：「点一次把活干完」才是常态，关掉它是在主动选"只跑一轮、
      // 剩下的下次再点"——那是个更费事的选项，不该是默认。
      expect(within(dialog).getByRole("switch")).toBeChecked()
      // 两档的说明文字不一样，得换：只写"持续到放完"会让人以为它更激进。
      expect(within(dialog).getByText(/批與批之間暫停 0\.1 秒|批与批之间暂停 0\.1 秒/)).toBeInTheDocument()

      await user.click(within(dialog).getByRole("button", { name: "开始回收" }))
      await waitFor(() => expect(mocked.reclaimStorage).toHaveBeenCalledWith(true))
    })

    it("关掉持续开关就按单轮提交，文案也跟着换成单轮那一档", async () => {
      const user = userEvent.setup()
      withCompression({ db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }) })
      mocked.reclaimStorage.mockResolvedValue({ started: true, continuous: false })

      renderPage()
      await user.click(await screen.findByRole("button", { name: /^回收 / }))

      const dialog = await screen.findByRole("dialog")
      await user.click(within(dialog).getByRole("switch"))
      expect(within(dialog).getByRole("switch")).not.toBeChecked()
      expect(within(dialog).getByText(/超过 5 秒仍未获得写入权限的请求会失败/)).toBeInTheDocument()

      await user.click(within(dialog).getByRole("button", { name: "开始回收" }))
      await waitFor(() => expect(mocked.reclaimStorage).toHaveBeenCalledWith(false))
    })

    /**
     * 持续回收可能跑十几分钟，没有这个按钮，用户唯一能做的就是等——
     * 而"我现在不想让它继续占库了"恰恰是最常见的念头。
     *
     * 这里只钉"按下去了、而且是**一次**"：停止在批间生效，后端那一趟
     * 还会再跑最多一批，所以界面此刻不该假装已经停了（那是下一条用例）。
     * 这条不重复点，也就顺带钉住了"点第二下会被后端挡掉"这个坑不出现。
     */
    it("回收跑着时给一个「停止」，按一下就发一次请求", async () => {
      const user = userEvent.setup()
      withCompression({
        reclaiming: true,
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })
      mocked.stopReclaim.mockResolvedValue({ stopping: true })

      renderPage()
      await user.click(await screen.findByRole("button", { name: "停止" }))

      await waitFor(() => expect(mocked.stopReclaim).toHaveBeenCalledTimes(1))
    })

    it("按了停止但那一批还没跑完时，如实说自己正在停", async () => {
      withCompression({
        reclaiming: true,
        reclaim_stopping: true,
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })

      renderPage()

      // 那一刻后端还在跑（reclaiming 仍为真），界面若继续显示"回收中"，
      // 用户会以为按钮没生效、再按一下——后端会把第二下挡掉，
      // 于是"按了没反应"。
      expect(await screen.findByRole("button", { name: /正在停/ })).toBeDisabled()
    })

    it("持续那一趟的回执带上「跑了几批」——1 批和 2,628 批是两次不同的动作", async () => {
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 1_000 }),
        reclaim: reclaimState({
          status: "done",
          source: "scheduled",
          continuous: true,
          rounds: 2_628,
          freed_pages: 1_345_736,
          stop_reason: "empty",
        }),
      })

      renderPage()

      expect(await screen.findByText(/按计划自动触发/)).toBeInTheDocument()
      expect(screen.getByText(/2,628 批/)).toBeInTheDocument()
    })

    it("单轮那一趟不提批数：它恒为一批，说了反而是噪音", async () => {
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 1_000 }),
        reclaim: reclaimState({
          status: "done",
          continuous: false,
          rounds: 1,
          freed_pages: 512,
          stop_reason: "budget",
        }),
      })

      renderPage()

      expect(await screen.findByText(/本轮时间已到/)).toBeInTheDocument()
      expect(screen.queryByText(/1 批/)).not.toBeInTheDocument()
    })

    it("被叫停的那一趟与跑完的那一趟在回执里分得开", async () => {
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 1_000 }),
        reclaim: reclaimState({
          status: "done",
          continuous: true,
          rounds: 12,
          stop_reason: "stopped",
        }),
      })

      renderPage()

      // 都是 status=done，但"你按的停止"和"放完了"是两件事。
      expect(await screen.findByText(/已手动停止/)).toBeInTheDocument()
    })

    it("迁移抢走维护锁时如实说是让位，而不是说自己跑完了", async () => {
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 1_000 }),
        reclaim: reclaimState({
          status: "done",
          continuous: true,
          rounds: 3,
          stop_reason: "busy",
        }),
      })

      renderPage()

      expect(await screen.findByText(/回收已讓出資料庫維護鎖|回收已让出数据库维护锁/)).toBeInTheDocument()
    })

    it("定时回收开着时在读数行上挂一个小标，并提交改过的门槛与周期", async () => {
      const user = userEvent.setup()
      withCompression({
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
        reclaim_policy: { enabled: true, min_bytes: 512 * 1024 ** 2, check_interval_sec: 1800 },
      })
      mocked.updateReclaimPolicy.mockResolvedValue({
        enabled: false,
        min_bytes: 0,
        check_interval_sec: 60,
      })

      renderPage()

      // 后台自己会动这件事必须显示出来：不说的话，用户只能从
      // "我没点过，它怎么跑过了"里反推。
      expect(
        await screen.findByText(/可回收空間超過 512 MB 時，每 30 分鐘檢查一次|可回收空间超过 512 MB 时，每 30 分钟检查一次/)
      ).toBeInTheDocument()

      await user.click(screen.getByRole("button", { name: "回收策略" }))
      const dialog = await screen.findByRole("dialog")
      // 关掉开关后保存：门槛与周期照旧带上去，不被开关连坐清零。
      await user.click(within(dialog).getByRole("switch"))
      await user.click(within(dialog).getByRole("button", { name: "保存" }))

      await waitFor(() =>
        expect(mocked.updateReclaimPolicy).toHaveBeenCalledWith({
          enabled: false,
          min_bytes: 512 * 1024 ** 2,
          check_interval_sec: 1800,
        })
      )
    })
  })

  describe("重整（VACUUM）", () => {
    /**
     * 重整与回收是同一个目标的**两条路**，判据完全独立：
     *
     *   - 回收要库开 auto_vacuum（否则是一条 0.000 秒的空操作），重整**不要求**；
     *   - 重整要 2 倍库大小的可用磁盘，回收不需要。
     *
     * 所以这张卡上不能只有一个"能不能点"的结论——最常见的现场恰好是
     * "回收点不动（no_auto_vacuum）但重整可以"。这条用例钉的就是两句话各说各的。
     */
    it("库没开 auto_vacuum 时回收点不动，但重整照常可点", async () => {
      withCompression({
        db: dbStats({
          auto_vacuum: 0,
          freelist_count: 336_000,
          file_size: 1024 ** 3,
          disk_free_bytes: 8 * 1024 ** 3,
        }),
      })

      renderPage()

      // 回收那条路断在 auto_vacuum 上。
      expect(await screen.findByText(/回收不會產生任何效果|回收不会产生任何效果/)).toBeInTheDocument()
      expect(screen.getByRole("button", { name: /^回收 / })).toBeDisabled()
      // 重整这条路与它无关。空闲 8 GiB 对 1 GiB 的库绰绰有余（要 2 GiB + 余量）。
      expect(screen.getByRole("button", { name: "立即重整" })).toBeEnabled()
    })

    it("磁盘不够时说清差多少，并把按钮禁掉而不是让人白点", async () => {
      withCompression({
        db: dbStats({
          auto_vacuum: 2,
          freelist_count: 336_000,
          file_size: 6 * 1024 ** 3,
          // 6 GiB 的库要 12 GiB + 余量，只给 8 GiB。
          disk_free_bytes: 8 * 1024 ** 3,
        }),
      })

      renderPage()

      // 理由要写出来：一个灰着的按钮不写原因，只会被当成故障。
      expect(await screen.findByText(/可用磁碟空間不夠這一次重整|可用磁盘空间不够这一次重整/)).toBeInTheDocument()
      expect(screen.getByRole("button", { name: "立即重整" })).toBeDisabled()
      // 同一条路走不通不该连坐另一条：回收不需要额外磁盘，照样可点。
      expect(screen.getByRole("button", { name: /^回收 / })).toBeEnabled()
    })

    it("状态量不到时不猜空间够不够，一律禁掉这条重路", async () => {
      // db 为 null 是后端如实说的"量不到"（迁移正在写库时这一读会撞锁）。
      withCompression({ db: null })

      renderPage()

      // db 为 null 是后端如实说的"量不到"（迁移正在写库时这一读会撞锁）。
      // 判据取重整自己那一句：库里另有两处"量不到"的说明，混在一起会找到多个。
      expect(await screen.findByText(/無法確認磁碟空間是否夠用|无法确认磁盘空间是否够用/)).toBeInTheDocument()
      expect(screen.getByRole("button", { name: "立即重整" })).toBeDisabled()
    })

    it("点重整要过一次确认，代价与回收分开说清", async () => {
      const user = userEvent.setup()
      withCompression({
        db: dbStats({
          auto_vacuum: 2,
          freelist_count: 336_000,
          file_size: 7 * 1024 ** 3,
          disk_free_bytes: 32 * 1024 ** 3,
        }),
      })
      mocked.vacuumStorage.mockResolvedValue({
        started: true,
        plan: { file_size: 7 * 1024 ** 3, free_bytes: 32 * 1024 ** 3, need_bytes: 14 * 1024 ** 3 },
      })

      renderPage()
      await user.click(await screen.findByRole("button", { name: "立即重整" }))

      const dialog = await screen.findByRole("dialog")
      // 它的代价与回收**不是一回事**：回收期间写入排队但多半成功，重整期间读写
      // 都进不来。说成"变慢"就是把一次真实的不可用轻描淡写了。
      expect(within(dialog).getByText(/不是「變慢」|不是「变慢」/)).toBeInTheDocument()
      // 停不了这件事必须在按下之前说：它没有"批次之间"这个时机。
      expect(within(dialog).getByText(/無法中途停止|无法中途停止/)).toBeInTheDocument()
      expect(mocked.vacuumStorage).not.toHaveBeenCalled()

      await user.click(within(dialog).getByRole("button", { name: "开始重整" }))
      await waitFor(() => expect(mocked.vacuumStorage).toHaveBeenCalledTimes(1))
    })

    /**
     * 重整在跑时的界面与回收**必须分得开**：
     *
     *   - 文案是"重整中"（用户点了重整，看到"回收中"会以为点错了）；
     *   - **不画「停止」**——VACUUM 是一条语句，没有批间这个时机，后端也会明确
     *     拒绝。画一个按下去必然报错的按钮，不如不画。
     *
     * 判据取 `reclaiming_kind` 而**不是**回执里的 `reclaim.kind`：后者写的是上一次
     * 跑完的那趟（running 记录要等收工才落库），正在跑的时候它是张冠李戴的旧值。
     * 这条用例特意把两者**设成相反的**，谁用错了立刻就会红。
     */
    it("重整在跑时说的是重整中，而且不画「停止」", async () => {
      withCompression({
        reclaiming: true,
        reclaiming_kind: "vacuum",
        // 回执是上一次**回收**留下的：拿它当"正在干什么"是错的。
        reclaim: reclaimState({ status: "done", kind: "reclaim", stop_reason: "empty" }),
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })

      renderPage()

      // 两处「重整中」：读数行一处、按钮一处。用 findAllByText 是因为它们说的是
      // 同一句话——这正是想要的形状（按钮自己报"我在重整中"，而不是回到"立即重整"）。
      expect((await screen.findAllByText("重整中")).length).toBeGreaterThan(0)
      expect(screen.queryByRole("button", { name: "停止" })).not.toBeInTheDocument()
      // 重整期间两条路都不该能再点：库被整个独占着。
      // 重整那颗按钮的文案此刻是「重整中」，所以判据取它而不是「立即重整」。
      expect(screen.getByRole("button", { name: "重整中" })).toBeDisabled()
      expect(screen.getByRole("button", { name: /^回收 / })).toBeDisabled()
    })

    it("回收在跑时照旧给「停止」，且不把上一次的重整回执说成正在重整", async () => {
      withCompression({
        reclaiming: true,
        reclaiming_kind: "reclaim",
        reclaim: reclaimState({ status: "done", kind: "vacuum", stop_reason: "vacuumed" }),
        db: dbStats({ auto_vacuum: 2, freelist_count: 336_000 }),
      })

      renderPage()

      // 同上：读数行与按钮都写着「回收中」。
      expect((await screen.findAllByText("回收中")).length).toBeGreaterThan(0)
      expect(screen.getByRole("button", { name: "停止" })).toBeInTheDocument()
    })

    // 回执那一行按 kind 分两套说法：重整没有"执行了几条语句"这回事（它是整库重写），
    // 而回收那套里的批量读数正是它的诊断价值所在。混着说的话，一次重整会显示成
    // "执行 0 条语句"，看起来像什么事都没干。
    it("重整的回执不说「执行了几条语句」", async () => {
      withCompression({
        reclaim: reclaimState({
          status: "done",
          kind: "vacuum",
          source: "manual",
          stop_reason: "vacuumed",
          file_size_before: 7 * 1024 ** 3,
          file_size_after: 128 * 1024 ** 2,
          duration_ms: 57_000,
          calls: 0,
        }),
        db: dbStats({ auto_vacuum: 2, freelist_count: 0 }),
      })

      renderPage()

      // 回执行要出现（说明这一趟确实被读出来了）……
      // 耗时由 formatDurationMs 给，单位是**拉丁 s**（`57s` 而非「57 秒」）——
      // 那句 "实测 7 GiB 的库约 57 秒" 是估算行的说明文案，不是这里要判的读数。
      expect(await screen.findByText(/耗[時时] 57s/)).toBeInTheDocument()
      // ……但它说的是重整那一套：不写"执行 0 条语句"（整库重写没有这个数），
      // 也不写"末批几条语句"。
      expect(screen.queryByText(/執行 0 條語句|执行 0 条语句/)).not.toBeInTheDocument()
      expect(screen.queryByText(/條語句$|条语句$/)).not.toBeInTheDocument()
    })
  })

  // ── 重整那几个纯函数 ──
  //
  // 它们是"能被算错而不会报错"的那一类：算错了界面照样渲染，只是给出的
  // "约多久 / 够不够"是错的。所以判据单独钉一遍。
  describe("重整的估算", () => {
    it("耗时按库文件大小折算，实测基准是 7.06 GiB / 57 秒", () => {
      // 基准点自己：7.06 GiB 应当落在 55–59 秒那一档。
      const sevenGiB = 7.06 * 1024 ** 3
      const sec = estimateVacuumSec(sevenGiB)
      expect(sec).toBeGreaterThanOrEqual(55)
      expect(sec).toBeLessThanOrEqual(59)
      // 常量本身要贴着这个基准：取整到秒会差几百字节，所以用相对误差判，
      // 钉的是"它就是 7.06 GiB / 57 秒"这件事，不是某一位小数。
      expect(VACUUM_BYTES_PER_SEC).toBeCloseTo(sevenGiB / 57, -6)

      // 空库与坏值不给假时间。
      expect(estimateVacuumSec(0)).toBe(0)
      expect(estimateVacuumSec(Number.NaN)).toBe(0)
      // 再小也至少 1 秒：界面上给一个 0 秒的"预计"和没给一样。
      expect(estimateVacuumSec(1)).toBe(1)
    })

    it("要的磁盘是 2 倍库大小加余量，与后端 enoughDisk 同一套算术", () => {
      const oneGiB = 1024 ** 3
      expect(vacuumNeedBytes(oneGiB)).toBe(2 * oneGiB + VACUUM_DISK_MARGIN)
      expect(vacuumNeedBytes(0)).toBe(0)

      expect(hasRoomForVacuum(oneGiB, 2 * oneGiB + VACUUM_DISK_MARGIN)).toBe(true)
      // 差一个字节也算不够：这条判据错了的后果是 VACUUM 中途磁盘满，
      // 而那是唯一能把库留在说不清状态的做法。
      expect(hasRoomForVacuum(oneGiB, 2 * oneGiB + VACUUM_DISK_MARGIN - 1)).toBe(false)
    })

    it("空闲磁盘量不到（0）时一律算不够，不把未知当充足", () => {
      // 后端量不到时给 0。把它当"空间无限"的代价是让用户白点一次、
      // 甚至写盘写到一半满；反过来误禁的代价只是这条路暂时不能用。
      expect(hasRoomForVacuum(1024 ** 2, 0)).toBe(false)
      expect(hasRoomForVacuum(1024 ** 2, -1)).toBe(false)
      expect(hasRoomForVacuum(1024 ** 2, Number.NaN)).toBe(false)
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
