import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { PeakCalendarCard } from "@/routes/peak-calendar"
import { getPeakCalendar, syncPeakHolidays, updatePeakCalendar } from "@/lib/api"
import type { PeakCalendar } from "@/lib/peak"

// 这张卡片只有三个端点，全部经 api.ts——切断它就断开了这一页的所有 IO
vi.mock("@/lib/api", () => ({
  getPeakCalendar: vi.fn(),
  updatePeakCalendar: vi.fn(),
  syncPeakHolidays: vi.fn(),
}))

const mocked = {
  get: vi.mocked(getPeakCalendar),
  update: vi.mocked(updatePeakCalendar),
  sync: vi.mocked(syncPeakHolidays),
}

/**
 * 工作日日历卡片与编辑器的 C 层测试（四态 + 关键路径）。
 *
 * 这一层钉的是"改坏了页面照样渲染"的几件事：
 *
 *   1. **后端 message 必须原样透出**。校验失败的定位信息（是时区还是哪一条
 *      覆盖）只有后端那句有，被"保存失败"四个字盖掉就等于让用户自己去猜。
 *   2. **保存/同步后的那一份要回流到卡片**。只更新弹窗里的本地 state，卡片
 *      会继续显示旧值——用户会以为没保存成功，再存一次。
 *   3. **同步不能抹掉手上还没保存的编辑**。同步会把服务端那份回传给卡片，
 *      那是**旧版本**；把它当成回填源，"改时区 → 点同步 → 保存"就会静默
 *      存回时区旧值。
 *   4. **保存是整份覆盖**：漏掉 dateOverrides 就等于把节假日覆盖清空，
 *      漏掉 holidaySyncedAt/holidaySource 界面就退回"从未同步"。
 *
 * 条款（时段、乘数、优先级）不在这里：它挂在「模型 × 上游」关联上，
 * 由 routes/model-providers.test.tsx 覆盖。
 */

/** 与 models.DefaultPeakCalendar 一致：未配置过的服务端就是这个 */
const 默认日历: PeakCalendar = {
  timezone: "Asia/Shanghai",
  weekdays: [1, 2, 3, 4, 5],
  dateOverrides: {},
}

const 已同步日历: PeakCalendar = {
  timezone: "Asia/Shanghai",
  weekdays: [1, 2, 3, 4, 5],
  dateOverrides: { "2026-10-01": "rest", "2026-10-08": "work" },
  holidaySyncedAt: 1759271400,
  holidaySource: "bundled:holidays/2026.json",
}

function renderCard() {
  return render(<PeakCalendarCard />)
}

/** 打开编辑器并等它回填完成 */
async function openEditor() {
  const user = userEvent.setup()
  const button = await screen.findByRole("button", { name: "编辑日历" })
  await user.click(button)
  const dialog = await screen.findByRole("dialog")
  return { user, dialog }
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.get.mockResolvedValue(默认日历)
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("工作日日历卡片 · 加载", () => {
  it("取数未落地时声明正在加载，不先摆一份默认日历", async () => {
    mocked.get.mockReturnValue(new Promise(() => {}))

    renderCard()

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("加载中"))
    expect(screen.queryByText("Asia/Shanghai")).not.toBeInTheDocument()
  })
})

describe("工作日日历卡片 · 空（后端返回默认日历）", () => {
  it("未配置过时摆出服务端的默认日历，并说清没有覆盖意味着什么", async () => {
    renderCard()

    // 后端对未配置返回的是默认日历而不是空值，因此"空态"就是"默认时区 + 默认工作日"
    expect(await screen.findByText("Asia/Shanghai")).toBeInTheDocument()
    expect(screen.getByText("5 天")).toBeInTheDocument()
    expect(screen.getByText("无")).toBeInTheDocument()
    expect(screen.getByText("从未同步")).toBeInTheDocument()
    expect(
      screen.getByText(/还没有按日期的覆盖：法定节假日与调休无法由星期几推出/)
    ).toBeInTheDocument()
  })
})

describe("工作日日历卡片 · 错误", () => {
  it("取数失败给原文与重试，而不是继续显示默认日历", async () => {
    mocked.get.mockRejectedValueOnce(new Error("502 Bad Gateway"))

    renderCard()

    expect(await screen.findByText("读取工作日日历失败")).toBeInTheDocument()
    expect(screen.getByText("502 Bad Gateway")).toBeInTheDocument()
    expect(screen.queryByText("Asia/Shanghai")).not.toBeInTheDocument()
  })

  it("重试成功后按服务端的值渲染，错误提示随之消失", async () => {
    const user = userEvent.setup()
    mocked.get.mockRejectedValueOnce(new Error("网络不可达"))
    renderCard()
    await screen.findByText("读取工作日日历失败")

    mocked.get.mockResolvedValue(已同步日历)
    await user.click(screen.getByRole("button", { name: "重试" }))

    expect(await screen.findByText(/同步自 bundled:holidays\/2026\.json/)).toBeInTheDocument()
    expect(screen.queryByText("读取工作日日历失败")).not.toBeInTheDocument()
  })

  it("读取失败时不给编辑入口（拿一份假日历进去改，保存会把它整份覆盖掉）", async () => {
    mocked.get.mockRejectedValueOnce(new Error("502 Bad Gateway"))

    renderCard()

    await screen.findByText("读取工作日日历失败")
    expect(screen.getByRole("button", { name: "编辑日历" })).toBeDisabled()
  })
})

describe("工作日日历卡片 · 有数据", () => {
  it("已保存的日历逐项显示，含覆盖条数与节假日同步的来源", async () => {
    mocked.get.mockResolvedValue(已同步日历)

    renderCard()

    expect(await screen.findByText("2 天")).toBeInTheDocument()
    expect(screen.getByText(/同步自 bundled:holidays\/2026\.json/)).toBeInTheDocument()
    // 有覆盖了，就不该再说"没有覆盖"
    expect(screen.queryByText(/还没有按日期的覆盖/)).not.toBeInTheDocument()
  })
})

describe("工作日日历编辑器 · 回填", () => {
  it("把已保存的日历回填进表单（时区、工作日、覆盖表、同步元数据）", async () => {
    mocked.get.mockResolvedValue(已同步日历)
    renderCard()
    const { dialog } = await openEditor()

    expect(within(dialog).getByPlaceholderText("Asia/Shanghai")).toHaveValue("Asia/Shanghai")
    // 周一至周五在分段控件上是按下状态（multiple 型给的是 aria-pressed）
    for (const day of ["周一", "周二", "周三", "周四", "周五"]) {
      expect(within(dialog).getByRole("button", { name: day })).toHaveAttribute(
        "aria-pressed",
        "true"
      )
    }
    expect(within(dialog).getByRole("button", { name: "周六" })).toHaveAttribute(
      "aria-pressed",
      "false"
    )

    const list = within(dialog).getByRole("list", { name: "按日期覆盖" })
    const rows = within(list).getAllByRole("listitem")
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByLabelText("日期")).toHaveValue("2026-10-01")
    // 放假 / 上班 是分段控件上的两个状态，选中项带 aria-checked
    expect(within(rows[0]).getByRole("radio", { name: "放假" })).toBeChecked()
    expect(within(rows[1]).getByRole("radio", { name: "上班" })).toBeChecked()

    // 同步元数据跟着回填：界面上要说得出"最近同步于哪一刻、来自哪里"
    expect(within(dialog).getByText(/同步自 bundled:holidays\/2026\.json/)).toBeInTheDocument()
  })

  it("没有覆盖时摆一句说明而不是一张空表", async () => {
    renderCard()
    const { dialog } = await openEditor()

    expect(within(dialog).getByText("还没有任何按日期的覆盖。")).toBeInTheDocument()
    expect(within(dialog).queryByRole("list", { name: "按日期覆盖" })).not.toBeInTheDocument()
  })

  it("关掉再打开是重新回填，不是留着上次没保存的改动", async () => {
    mocked.get.mockResolvedValue(默认日历)
    renderCard()
    const { user, dialog } = await openEditor()

    const tz = within(dialog).getByPlaceholderText("Asia/Shanghai")
    await user.clear(tz)
    await user.type(tz, "Not/AZone")
    await user.click(within(dialog).getByRole("button", { name: "取消" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

    await user.click(screen.getByRole("button", { name: "编辑日历" }))
    const reopened = await screen.findByRole("dialog")
    expect(within(reopened).getByPlaceholderText("Asia/Shanghai")).toHaveValue("Asia/Shanghai")
  })
})

describe("工作日日历编辑器 · 校验", () => {
  it("时区认不出来时点名原值，并且不提交", async () => {
    mocked.get.mockResolvedValue(默认日历)
    renderCard()
    const { user, dialog } = await openEditor()

    const tz = within(dialog).getByPlaceholderText("Asia/Shanghai")
    await user.clear(tz)
    await user.type(tz, "Not/AZone")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(await within(dialog).findByText("时区「Not/AZone」无法识别")).toBeInTheDocument()
    // 本地就拦下了：不该再跑一趟后端，也不该把对话框关掉
    expect(mocked.update).not.toHaveBeenCalled()
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("没填完的日期覆盖拦下保存（空日期落到后端会被整份拒掉）", async () => {
    mocked.get.mockResolvedValue(默认日历)
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "新增日期" }))
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(await within(dialog).findByText("日期覆盖「」不是 YYYY-MM-DD")).toBeInTheDocument()
    expect(mocked.update).not.toHaveBeenCalled()
  })

  it("改好之后红色自己消失（不是一直挂着上次的结论）", async () => {
    mocked.get.mockResolvedValue(默认日历)
    renderCard()
    const { user, dialog } = await openEditor()

    const tz = within(dialog).getByPlaceholderText("Asia/Shanghai")
    await user.type(tz, "X")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))
    expect(await within(dialog).findByText(/无法识别/)).toBeInTheDocument()

    await user.clear(tz)
    await user.type(tz, "Asia/Shanghai")
    expect(within(dialog).queryByText(/无法识别/)).not.toBeInTheDocument()
    expect(within(dialog).queryByText(/条）/)).not.toBeInTheDocument()
  })

  it("工作日一个都不选是合法的：留空表示按后端默认（周一至周五）", async () => {
    mocked.get.mockResolvedValue(默认日历)
    mocked.update.mockResolvedValue(默认日历)
    renderCard()
    const { user, dialog } = await openEditor()

    for (const day of ["周一", "周二", "周三", "周四", "周五"]) {
      await user.click(within(dialog).getByRole("button", { name: day }))
    }
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    // 空数组要**省略**（不是发 []）：后端把空当周一至周五，发 [] 是同一个意思，
    // 但配置文件里就多了一个看不出差别的字段
    expect(mocked.update.mock.calls[0][0].weekdays).toBeUndefined()
  })
})

describe("工作日日历编辑器 · 保存", () => {
  it("保存提交的是归一后的整份日历，成功后卡片跟着更新", async () => {
    mocked.get.mockResolvedValue(默认日历)
    mocked.update.mockResolvedValue(已同步日历)
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "新增日期" }))
    const row = within(within(dialog).getByRole("list", { name: "按日期覆盖" })).getAllByRole(
      "listitem"
    )[0]
    fireEvent.change(within(row).getByLabelText("日期"), { target: { value: "2026-10-01" } })
    await user.click(within(row).getByRole("radio", { name: "上班" }))
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    // 整份覆盖：星期原样、覆盖表永远带对象（不带 omitempty）
    expect(mocked.update.mock.calls[0][0]).toEqual({
      timezone: "Asia/Shanghai",
      weekdays: [1, 2, 3, 4, 5],
      dateOverrides: { "2026-10-01": "work" },
    })

    // 卡片拿到的是**服务端返回的那一份**，不是表单
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(await screen.findByText("2 天")).toBeInTheDocument()
    expect(screen.getByText(/同步自 bundled:holidays\/2026\.json/)).toBeInTheDocument()
  })

  it("保存失败时把后端那句话原样摆出来，对话框留着可重试", async () => {
    mocked.get.mockResolvedValue(默认日历)
    mocked.update.mockRejectedValueOnce(
      new Error('invalid date override key "2026-9-1": expected YYYY-MM-DD')
    )
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(
      await within(dialog).findByText('invalid date override key "2026-9-1": expected YYYY-MM-DD')
    ).toBeInTheDocument()
    // 没保存成功就不能当成功：对话框还在，按钮还能按
    expect(screen.getByRole("dialog")).toBeInTheDocument()
    expect(within(dialog).getByRole("button", { name: "保存" })).toBeEnabled()
    expect(screen.queryByText(/同步自/)).not.toBeInTheDocument()
  })
})

describe("工作日日历编辑器 · 节假日同步", () => {
  it("同步失败（外网不通、后端 502）摆出原文与重试，配置保持原样", async () => {
    mocked.get.mockResolvedValue(默认日历)
    mocked.sync.mockRejectedValueOnce(
      new Error("Failed to sync holidays: no holiday data for 2026: remote failed")
    )
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "从上游同步" }))

    expect(await within(dialog).findByText("节假日同步失败")).toBeInTheDocument()
    expect(
      within(dialog).getByText("Failed to sync holidays: no holiday data for 2026: remote failed")
    ).toBeInTheDocument()
    // 同步没成功，配置不该被改动
    expect(mocked.update).not.toHaveBeenCalled()
    expect(within(dialog).getByRole("button", { name: "重试同步" })).toBeEnabled()

    mocked.sync.mockResolvedValue({
      year: 2026,
      count: 2,
      source: "bundled:holidays/2026.json",
      syncedAt: 1759271400,
      calendar: 已同步日历,
    })
    await user.click(within(dialog).getByRole("button", { name: "重试同步" }))

    // 同步在服务端就已落盘：返回的那一份要同时进表单与卡片
    const list = await within(dialog).findByRole("list", { name: "按日期覆盖" })
    expect(within(list).getAllByRole("listitem")).toHaveLength(2)
    expect(within(dialog).queryByText("节假日同步失败")).not.toBeInTheDocument()
    // 关掉对话框后再看卡片：拿到的是同步后的那一份，而不是打开时的旧值
    await user.click(within(dialog).getByRole("button", { name: "取消" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(screen.getByText(/同步自 bundled:holidays\/2026\.json/)).toBeInTheDocument()
  })

  it("同步与保存共用同一条路径：同步后保存提交的是同步回来的那份覆盖表", async () => {
    mocked.get.mockResolvedValue(默认日历)
    mocked.update.mockResolvedValue(已同步日历)
    renderCard()
    const { user, dialog } = await openEditor()

    mocked.sync.mockResolvedValue({
      year: 2026,
      count: 2,
      source: "bundled:holidays/2026.json",
      syncedAt: 1759271400,
      calendar: 已同步日历,
    })
    await user.click(within(dialog).getByRole("button", { name: "从上游同步" }))
    await within(dialog).findByRole("list", { name: "按日期覆盖" })

    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0]).toEqual(已同步日历)
  })

  it("同步回传服务端那一份时，不能把还没保存的编辑抹掉", async () => {
    // 同步成功会把服务端那份日历回传给卡片（卡片要立刻显示"最近同步于…"），
    // 而那一份是**旧的服务端版本**：它里面时区还是服务端那个值。
    // 早先这里把 config 接进了回填 effect 的依赖，于是"改时区 → 同步 → 保存"
    // 会静默存回旧时区——用户只会以为"我明明改了"。
    mocked.get.mockResolvedValue(默认日历)
    mocked.update.mockResolvedValue(已同步日历)
    renderCard()
    const { user, dialog } = await openEditor()

    const tz = within(dialog).getByPlaceholderText("Asia/Shanghai")
    await user.clear(tz)
    await user.type(tz, "UTC")

    mocked.sync.mockResolvedValue({
      year: 2026,
      count: 2,
      source: "bundled:holidays/2026.json",
      syncedAt: 1759271400,
      // 服务端那份：时区仍是 Asia/Shanghai，覆盖表已写进去
      calendar: 已同步日历,
    })
    await user.click(within(dialog).getByRole("button", { name: "从上游同步" }))
    await within(dialog).findByRole("list", { name: "按日期覆盖" })

    // 时区是用户改的，同步不该动它；同步新带回来的覆盖表则要进表单
    expect(within(dialog).getByPlaceholderText("Asia/Shanghai")).toHaveValue("UTC")
    expect(
      within(within(dialog).getByRole("list", { name: "按日期覆盖" })).getAllByRole("listitem")
    ).toHaveLength(2)

    await user.click(within(dialog).getByRole("button", { name: "保存" }))
    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].timezone).toBe("UTC")
    expect(mocked.update.mock.calls[0][0].dateOverrides).toEqual({
      "2026-10-01": "rest",
      "2026-10-08": "work",
    })
  })

  it("手动加一条日期覆盖，随保存提交（日期用原生 date 控件的 YYYY-MM-DD）", async () => {
    mocked.get.mockResolvedValue(默认日历)
    mocked.update.mockResolvedValue(默认日历)
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "新增日期" }))
    const row = within(within(dialog).getByRole("list", { name: "按日期覆盖" })).getAllByRole(
      "listitem"
    )[0]
    fireEvent.change(within(row).getByLabelText("日期"), { target: { value: "2026-10-01" } })
    await user.click(within(row).getByRole("radio", { name: "上班" }))

    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].dateOverrides).toEqual({ "2026-10-01": "work" })
  })

  it("删掉一条日期覆盖，不再提交它", async () => {
    mocked.get.mockResolvedValue(已同步日历)
    mocked.update.mockResolvedValue(默认日历)
    renderCard()
    const { user, dialog } = await openEditor()

    const list = within(dialog).getByRole("list", { name: "按日期覆盖" })
    await user.click(
      within(within(list).getAllByRole("listitem")[0]).getByRole("button", { name: "删除该日期" })
    )
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].dateOverrides).toEqual({ "2026-10-08": "work" })
  })
})
