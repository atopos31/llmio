import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { PeakPricingCard } from "@/routes/peak-pricing"
import { getPeakPricing, previewPeakPricing, syncPeakHolidays, updatePeakPricing } from "@/lib/api"
import type { PeakPricing } from "@/lib/peak"

// 这一页只有四个端点，全部经 api.ts——切断它就断开了这一页的所有 IO
vi.mock("@/lib/api", () => ({
  getPeakPricing: vi.fn(),
  updatePeakPricing: vi.fn(),
  previewPeakPricing: vi.fn(),
  syncPeakHolidays: vi.fn(),
}))

const mocked = {
  get: vi.mocked(getPeakPricing),
  update: vi.mocked(updatePeakPricing),
  preview: vi.mocked(previewPeakPricing),
  sync: vi.mocked(syncPeakHolidays),
}

/**
 * 峰谷计费卡片的 C 层测试（四态 + 编辑器的关键路径）。
 *
 * 这一层钉的是"改坏了页面照样渲染"的几件事：
 *
 *   1. **后端 message 必须原样透出**。校验失败的定位信息只有后端那句
 *      "period 2 (夜间): start ..." 有，被"保存失败"四个字盖掉就等于让用户
 *      自己去猜是哪一段、哪一条。
 *   2. **保存后的数据要回流到父页面**。只更新弹窗里的本地 state，卡片会继续
 *      显示旧状态——用户会以为没保存成功，再存一次。
 *   3. **顺序动的是优先级**，不是展示顺序。后端首个命中者胜出，上移/下移
 *      写反了，谁贵谁便宜就跟着反，而界面上看起来都"排好了"。
 *   4. 同步失败（外网请求，后端用 502 回）要有原文与重试，不能一声不响。
 */

/** 与 models.DefaultPeakPricing 一致：未配置过的服务端就是这个 */
const 默认配置: PeakPricing = {
  enabled: false,
  timezone: "Asia/Shanghai",
  weekdays: [1, 2, 3, 4, 5],
  periods: [
    { name: "标准时段", start: "08:30", end: "00:30", multiplier: 1 },
    { name: "夜间优惠", start: "00:30", end: "08:30", multiplier: 0.25 },
  ],
  dateOverrides: {},
}

const 启用配置: PeakPricing = {
  ...默认配置,
  enabled: true,
  periods: [
    { name: "夜间优惠", start: "00:30", end: "08:30", multiplier: 0.25, days: [1, 2, 3, 4, 5] },
    { name: "周末全天", start: "00:00", end: "24:00", multiplier: 1.5, days: [0, 6] },
  ],
  dateOverrides: { "2026-10-01": "rest", "2026-10-08": "work" },
  holidaySyncedAt: 1759271400,
  holidaySource: "bundled:holidays/2026.json",
}

function renderCard() {
  return render(<PeakPricingCard />)
}

/** 打开编辑器并等它回填完成 */
async function openEditor() {
  const user = userEvent.setup()
  const button = await screen.findByRole("button", { name: "编辑" })
  await user.click(button)
  const dialog = await screen.findByRole("dialog")
  return { user, dialog }
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.get.mockResolvedValue(默认配置)
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("峰谷计费卡片 · 加载", () => {
  it("取数未落地时声明正在加载，不先说未开启", async () => {
    mocked.get.mockReturnValue(new Promise(() => {}))

    renderCard()

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("加载中"))
    expect(screen.queryByText("未开启")).not.toBeInTheDocument()
  })
})

describe("峰谷计费卡片 · 空（后端返回默认配置）", () => {
  it("未配置过时摆出服务端的默认配置，并说明关闭时代价是什么", async () => {
    renderCard()

    // 后端对未配置返回的是默认配置而不是空值，因此"空态"就是"未开启 + 默认时段"
    expect(await screen.findByText("未开启")).toBeInTheDocument()
    expect(screen.getByText("Asia/Shanghai")).toBeInTheDocument()
    expect(screen.getByText("2 段")).toBeInTheDocument()
    expect(screen.getByText("从未同步")).toBeInTheDocument()
    expect(
      screen.getByText("未开启时全部请求按基础价计费，下面的时段配置不会生效。")
    ).toBeInTheDocument()
  })
})

describe("峰谷计费卡片 · 错误", () => {
  it("取数失败给原文与重试，而不是继续显示「未配置」", async () => {
    mocked.get.mockRejectedValueOnce(new Error("502 Bad Gateway"))

    renderCard()

    expect(await screen.findByText("读取峰谷计费配置失败")).toBeInTheDocument()
    expect(screen.getByText("502 Bad Gateway")).toBeInTheDocument()
    expect(screen.queryByText("未开启")).not.toBeInTheDocument()
  })

  it("重试成功后按服务端的值渲染，错误提示随之消失", async () => {
    const user = userEvent.setup()
    mocked.get.mockRejectedValueOnce(new Error("网络不可达"))
    renderCard()
    await screen.findByText("读取峰谷计费配置失败")

    mocked.get.mockResolvedValue(启用配置)
    await user.click(screen.getByRole("button", { name: "重试" }))

    expect(await screen.findByText("已开启")).toBeInTheDocument()
    expect(screen.queryByText("读取峰谷计费配置失败")).not.toBeInTheDocument()
  })
})

describe("峰谷计费卡片 · 有数据", () => {
  it("已保存的配置逐项显示，含节假日同步的来源", async () => {
    mocked.get.mockResolvedValue(启用配置)

    renderCard()

    expect(await screen.findByText("已开启")).toBeInTheDocument()
    expect(screen.getByText("2 段")).toBeInTheDocument()
    expect(screen.getByText(/同步自 bundled:holidays\/2026\.json/)).toBeInTheDocument()
  })
})

describe("峰谷计费编辑器 · 回填", () => {
  it("把已保存的时段回填进表单（含跨零点与乘数）", async () => {
    mocked.get.mockResolvedValue(启用配置)
    renderCard()
    const { dialog } = await openEditor()

    const first = within(await within(dialog).findByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )[0]
    expect(within(first).getByLabelText("名称")).toHaveValue("夜间优惠")
    expect(within(first).getByLabelText("开始")).toHaveValue("00:30")
    expect(within(first).getByLabelText("结束")).toHaveValue("08:30")
    expect(within(first).getByLabelText("价格系数")).toHaveValue(0.25)
  })

  it("日期覆盖按日期列出，并标出是放假还是上班", async () => {
    mocked.get.mockResolvedValue(启用配置)
    renderCard()
    const { dialog } = await openEditor()

    const list = await within(dialog).findByRole("list", { name: "按日期覆盖" })
    const rows = within(list).getAllByRole("listitem")
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByLabelText("日期")).toHaveValue("2026-10-01")
    // 放假 / 上班 是分段控件上的两个状态，选中项带 aria-checked
    expect(within(rows[0]).getByRole("radio", { name: "放假" })).toBeChecked()
    expect(within(rows[1]).getByRole("radio", { name: "上班" })).toBeChecked()
  })
})

describe("峰谷计费编辑器 · 校验", () => {
  it("时刻写错时点名第几段、哪一项、原值是什么，并且不提交", async () => {
    mocked.get.mockResolvedValue(启用配置)
    renderCard()
    const { user, dialog } = await openEditor()

    const first = within(await within(dialog).findByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )[0]
    const start = within(first).getByLabelText("开始")
    await user.clear(start)
    await user.type(start, "25:00")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(
      await within(dialog).findByText("第 1 段「夜间优惠」的开始时间「25:00」不是 HH:MM")
    ).toBeInTheDocument()
    // 本地就拦下了：不该再跑一趟后端，也不该把对话框关掉
    expect(mocked.update).not.toHaveBeenCalled()
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("开始与结束相同会被拦下（后端会拒：那会覆盖全天）", async () => {
    renderCard()
    const { user, dialog } = await openEditor()

    const first = within(await within(dialog).findByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )[0]
    const end = within(first).getByLabelText("结束")
    await user.clear(end)
    await user.type(end, "08:30")
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(
      await within(dialog).findByText("第 1 段「标准时段」的开始与结束相同，这会覆盖全天")
    ).toBeInTheDocument()
    expect(mocked.update).not.toHaveBeenCalled()
  })

  it("时段重叠只提示、不拦保存（后端允许重叠，顺序决定谁生效）", async () => {
    // 默认配置的两段首尾相接，先改成真正重叠
    mocked.get.mockResolvedValue({
      ...默认配置,
      periods: [
        { name: "标准时段", start: "08:00", end: "20:00", multiplier: 1 },
        { name: "夜间优惠", start: "19:00", end: "08:00", multiplier: 0.25 },
      ],
    })
    mocked.update.mockResolvedValue(默认配置)
    renderCard()
    const { user, dialog } = await openEditor()

    expect(
      await within(dialog).findByText("第 1 段与第 2 段在时间上重叠；重叠的时刻由靠前的第 1 段生效。")
    ).toBeInTheDocument()

    await user.click(within(dialog).getByRole("button", { name: "保存" }))
    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
  })

  it("启用但一段时段都没有：给出说明，但仍然允许保存（等价于按基础价）", async () => {
    mocked.get.mockResolvedValue({ ...默认配置, enabled: true })
    mocked.update.mockResolvedValue({ ...默认配置, enabled: true })
    renderCard()
    const { user, dialog } = await openEditor()

    // 每删一段都重新取一次：li 用下标当 key，删完第一个后先取到的那个节点
    // 已经脱离文档，再点它等于什么都没点
    for (let i = 0; i < 2; i++) {
      const rows = within(
        within(dialog).getByRole("list", { name: "时段列表" })
      ).getAllByRole("listitem")
      await user.click(within(rows[0]).getByRole("button", { name: /删除该段/ }))
    }

    expect(
      await within(dialog).findByText("已启用但没有任何时段：全部请求都会按基础价计费。")
    ).toBeInTheDocument()

    await user.click(within(dialog).getByRole("button", { name: "保存" }))
    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].periods).toEqual([])
  })
})

describe("峰谷计费编辑器 · 保存", () => {
  it("保存提交的是归一后的整份配置，成功后父卡片跟着更新", async () => {
    mocked.get.mockResolvedValue(默认配置)
    mocked.update.mockResolvedValue(启用配置)
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("switch", { name: "启用峰谷计费" }))
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    // 整份覆盖：空数组省略、覆盖表永远带对象、时段顺序原样（顺序即优先级）
    expect(mocked.update.mock.calls[0][0]).toEqual({
      enabled: true,
      timezone: "Asia/Shanghai",
      weekdays: [1, 2, 3, 4, 5],
      periods: [
        { name: "标准时段", start: "08:30", end: "00:30", multiplier: 1 },
        { name: "夜间优惠", start: "00:30", end: "08:30", multiplier: 0.25 },
      ],
      dateOverrides: {},
    })

    // 卡片拿到的是**服务端返回的那一份**，不是表单
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(await screen.findByText("已开启")).toBeInTheDocument()
  })

  it("保存失败时把后端那句话原样摆出来，对话框留着可重试", async () => {
    mocked.get.mockResolvedValue(默认配置)
    mocked.update.mockRejectedValueOnce(
      new Error('period 2 (夜间优惠): start invalid clock "25:00": expected HH:MM')
    )
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    expect(
      await within(dialog).findByText(
        'period 2 (夜间优惠): start invalid clock "25:00": expected HH:MM'
      )
    ).toBeInTheDocument()
    // 没保存成功就不能当成功：对话框还在，按钮还能按
    expect(screen.getByRole("dialog")).toBeInTheDocument()
    expect(within(dialog).getByRole("button", { name: "保存" })).toBeEnabled()
    expect(screen.queryByText("已开启")).not.toBeInTheDocument()
  })
})

describe("峰谷计费编辑器 · 优先级", () => {
  it("上移一段换的是判定顺序，不是展示顺序", async () => {
    mocked.get.mockResolvedValue(启用配置)
    mocked.update.mockResolvedValue(启用配置)
    renderCard()
    const { user, dialog } = await openEditor()

    const rows = () =>
      within(within(dialog).getByRole("list", { name: "时段列表" })).getAllByRole("listitem")
    expect(within(rows()[0]).getByLabelText("名称")).toHaveValue("夜间优惠")

    // 第 2 段上移
    await user.click(within(rows()[1]).getByRole("button", { name: /上移/ }))
    expect(within(rows()[0]).getByLabelText("名称")).toHaveValue("周末全天")

    await user.click(within(dialog).getByRole("button", { name: "保存" }))
    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    // 顺序真的进了载荷——后端首个命中者胜出，这里写反了价格就反了
    expect(mocked.update.mock.calls[0][0].periods.map((p) => p.name)).toEqual([
      "周末全天",
      "夜间优惠",
    ])
  })

  it("新增时段排在最后（优先级最低），删除按段删", async () => {
    mocked.get.mockResolvedValue(默认配置)
    mocked.update.mockResolvedValue(默认配置)
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "新增时段" }))
    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    const sent = mocked.update.mock.calls[0][0]
    expect(sent.periods).toHaveLength(3)
    expect(sent.periods[2]).toEqual({
      name: "",
      start: "00:00",
      end: "00:30",
      multiplier: 1,
    })
  })
})

describe("峰谷计费编辑器 · 预览", () => {
  it("预览把服务端的时间轴画出来，不自己重算乘数", async () => {
    mocked.get.mockResolvedValue(默认配置)
    mocked.preview.mockResolvedValue([
      {
        start: Date.UTC(2026, 9, 1, 0, 30),
        end: Date.UTC(2026, 9, 1, 1, 0),
        period: "标准时段",
        multiplier: 1,
        workday: true,
      },
      {
        start: Date.UTC(2026, 9, 3, 0, 0),
        end: Date.UTC(2026, 9, 3, 1, 0),
        period: "",
        multiplier: 1,
        workday: false,
      },
    ])
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "预览时间轴" }))

    // 按配置时区渲染：UTC 00:30 在东八区就是 08:30
    expect(await within(dialog).findByText("10-01 08:30 → 10-01 09:00")).toBeInTheDocument()
    expect(within(dialog).getByText("标准时段")).toBeInTheDocument()
    // 两段都是基础价：乘数逐行展示，不是把整条时间轴汇总成一个数
    expect(within(dialog).getAllByText("×1")).toHaveLength(2)
    // 命中空名的那段是基础价，并标出当天是休息日
    expect(within(dialog).getByText("基础价")).toBeInTheDocument()
    expect(within(dialog).getByText("休息日")).toBeInTheDocument()
    expect(mocked.preview).toHaveBeenCalledWith(expect.anything(), 7)
  })

  it("预览失败给原文与重试，重试后恢复", async () => {
    mocked.get.mockResolvedValue(默认配置)
    mocked.preview.mockRejectedValueOnce(new Error("days must be between 1 and 31"))
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "预览时间轴" }))
    expect(await within(dialog).findByText("预览失败")).toBeInTheDocument()
    expect(within(dialog).getByText("days must be between 1 and 31")).toBeInTheDocument()

    mocked.preview.mockResolvedValue([])
    await user.click(within(dialog).getByRole("button", { name: "重试" }))

    expect(
      await within(dialog).findByText("这段时间内没有任何时段命中，全部按基础价。")
    ).toBeInTheDocument()
    expect(within(dialog).queryByText("预览失败")).not.toBeInTheDocument()
  })

  it("改了天数就按新的天数去问", async () => {
    mocked.get.mockResolvedValue(默认配置)
    mocked.preview.mockResolvedValue([])
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("radio", { name: "3" }))
    await user.click(within(dialog).getByRole("button", { name: "预览时间轴" }))

    await waitFor(() => expect(mocked.preview).toHaveBeenCalledWith(expect.anything(), 3))
  })
})

describe("峰谷计费编辑器 · 节假日同步", () => {
  it("同步失败（外网不通、后端 502）摆出原文与重试，配置保持原样", async () => {
    mocked.get.mockResolvedValue(默认配置)
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
      config: 启用配置,
    })
    await user.click(within(dialog).getByRole("button", { name: "重试同步" }))

    // 同步在服务端就已落盘：返回的那一份要同时进表单与父卡片
    const list = await within(dialog).findByRole("list", { name: "按日期覆盖" })
    expect(within(list).getAllByRole("listitem")).toHaveLength(2)
    expect(within(dialog).queryByText("节假日同步失败")).not.toBeInTheDocument()
    // 关掉对话框后再看卡片：拿到的是同步后的那一份，而不是打开时的旧值
    await user.click(within(dialog).getByRole("button", { name: "取消" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(screen.getByText(/同步自 bundled:holidays\/2026\.json/)).toBeInTheDocument()
  })

  it("同步回传服务端那一份时，不能把还没保存的编辑抹掉", async () => {
    // 同步成功会把服务端那份配置回传给父组件（卡片要立刻显示"最近同步于…"），
    // 而那一份是**旧的服务端版本**：它里面 enabled 还是 false，时段也还是默认两段。
    // 早先这里把 config 接进了回填 effect 的依赖，于是"开开关 → 同步 → 保存"
    // 会静默存回 enabled=false——用户只会以为"我明明开了"。
    mocked.get.mockResolvedValue(默认配置)
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("switch", { name: "启用峰谷计费" }))
    expect(within(dialog).getByRole("switch", { name: "启用峰谷计费" })).toBeChecked()

    mocked.sync.mockResolvedValue({
      year: 2026,
      count: 2,
      source: "bundled:holidays/2026.json",
      syncedAt: 1759271400,
      // 服务端那份：未开启、覆盖表已写进去
      config: {
        ...默认配置,
        dateOverrides: { "2026-10-01": "rest", "2026-10-08": "work" },
        holidaySyncedAt: 1759271400,
        holidaySource: "bundled:holidays/2026.json",
      },
    })
    mocked.update.mockResolvedValue(启用配置)
    await user.click(within(dialog).getByRole("button", { name: "从上游同步" }))
    await within(dialog).findByRole("list", { name: "按日期覆盖" })

    // 开关是用户改的，同步不该动它；同步新带回来的覆盖表则要进表单
    expect(within(dialog).getByRole("switch", { name: "启用峰谷计费" })).toBeChecked()
    expect(within(within(dialog).getByRole("list", { name: "按日期覆盖" })).getAllByRole("listitem")).toHaveLength(2)

    await user.click(within(dialog).getByRole("button", { name: "保存" }))
    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].enabled).toBe(true)
  })

  it("手动加一条日期覆盖，随保存提交（日期用原生 date 控件的 YYYY-MM-DD）", async () => {
    mocked.get.mockResolvedValue(默认配置)
    mocked.update.mockResolvedValue(默认配置)
    renderCard()
    const { user, dialog } = await openEditor()

    await user.click(within(dialog).getByRole("button", { name: "新增日期" }))
    const list = within(dialog).getByRole("list", { name: "按日期覆盖" })
    const row = within(list).getAllByRole("listitem")[0]
    fireEvent.change(within(row).getByLabelText("日期"), { target: { value: "2026-10-01" } })
    await user.click(within(row).getByRole("radio", { name: "上班" }))

    await user.click(within(dialog).getByRole("button", { name: "保存" }))

    await waitFor(() => expect(mocked.update).toHaveBeenCalledTimes(1))
    expect(mocked.update.mock.calls[0][0].dateOverrides).toEqual({ "2026-10-01": "work" })
  })
})
