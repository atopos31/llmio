import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ModelProvidersPage from "@/routes/model-providers"
// 只导入要在断言里引用的那几个；其余端点由下面的 mock 工厂供给页面，
// 不导入是刻意的——导入了却不用会让 tsc 报未使用
import {
  configAPI,
  createModel,
  createModelProvider,
  deleteModel,
  getModelMetadata,
  getModelOptions,
  getModelProviderStatus,
  getModelProviders,
  getProviderModels,
  getProviders,
  previewPeakTerms,
  testModelProvider,
  updateModel,
  updateModelOrder,
  updateModelProvider,
  type Model,
  type ModelMetadataSuggestion,
  type ModelWithProvider,
  type Provider,
} from "@/lib/api"
// 峰谷的契约形状住在 lib/peak（api.ts 只是把它用在 ModelWithProvider 上，
// 不转手导出）
import type { PeakTerms } from "@/lib/peak"

// 这一页的数据全部经过 api.ts，切断它就断开了本页的全部 IO。
// 子组件（表单对话框、连通性测试、两个自定义 hook、峰谷条款编辑器）也从这里
// 取函数，因此清单必须完整——漏一个会让它在运行时是 undefined 而不是"没被调用"。
vi.mock("@/lib/api", () => ({
  // 自动填写：策略是个常量（不是函数），mock 工厂整个换掉这个模块，不给的话
  // 导入处就是 undefined，而它在 useState 的初值里被读到，会当场炸。
  defaultModelAutofillPolicy: {
    enabled: true,
    overwrite: false,
    allow_deprecated: false,
    sources: ["models.dev", "litellm"],
  },
  configAPI: {
    getConfig: vi.fn(),
    updateConfig: vi.fn(),
  },
  getModelMetadata: vi.fn(),
  createModel: vi.fn(),
  createModelProvider: vi.fn(),
  deleteModel: vi.fn(),
  deleteModelProvider: vi.fn(),
  getModelOptions: vi.fn(),
  getModelProviderStatus: vi.fn(),
  getModelProviders: vi.fn(),
  getProviderModels: vi.fn(),
  getProviders: vi.fn(),
  previewPeakTerms: vi.fn(),
  testModelProvider: vi.fn(),
  updateModel: vi.fn(),
  updateModelOrder: vi.fn(),
  updateModelProvider: vi.fn(),
  updateModelProviderStatus: vi.fn(),
}))

const mocked = {
  getModelOptions: vi.mocked(getModelOptions),
  getProviders: vi.mocked(getProviders),
  getModelProviders: vi.mocked(getModelProviders),
  getModelProviderStatus: vi.mocked(getModelProviderStatus),
  updateModelOrder: vi.mocked(updateModelOrder),
  updateModel: vi.mocked(updateModel),
  deleteModel: vi.mocked(deleteModel),
  createModel: vi.mocked(createModel),
  getProviderModels: vi.mocked(getProviderModels),
  createModelProvider: vi.mocked(createModelProvider),
  updateModelProvider: vi.mocked(updateModelProvider),
  previewPeakTerms: vi.mocked(previewPeakTerms),
  getConfig: vi.mocked(configAPI.getConfig),
  getModelMetadata: vi.mocked(getModelMetadata),
}

/**
 * 模型路由页的行为基线（方案 §5.2 的 C 层四态）。
 *
 * 这组断言先于重构写下，用途有两个：一是钉住这一页对外承诺的语义
 * （什么条件下说什么话、点一行之后发生什么），让随后的文件拆分有网可依；
 * 二是把"失败"这一态显式留空——页面目前对取数失败只弹 toast，正文照旧
 * 显示"暂无可关联模型"，也就是把失败说成了空。那条修正随后单独提交。
 *
 * 贯穿本文件的一条限制：**这一页目前有两套并行实现**——桌面用表格、手机用
 * 卡片列表，同一个模型名/提供商名在 DOM 里出现两次。断言因此一律限定在
 * `table()` 之内，否则 `getByText` 必然报"找到多个"。这不是测试的将就，
 * 而是这一页的真实状态：两套实现要各自维护一遍字段与操作。
 */

function model(over: Partial<Model> = {}): Model {
  return {
    ID: 1,
    Name: "gpt-test",
    Remark: "测试模型",
    MaxRetry: 10,
    TimeOut: 60,
    Strategy: "lottery",
    Breaker: false,
    DisplayOrder: 1,
    ...over,
  }
}

function provider(over: Partial<Provider> = {}): Provider {
  return {
    ID: 11,
    Name: "prov-a",
    Type: "openai",
    Config: "{}",
    Console: "",
    Proxy: "",
    ErrorMatcher: "",
    ...over,
  }
}

function association(over: Partial<ModelWithProvider> = {}): ModelWithProvider {
  return {
    ID: 101,
    ModelID: 1,
    ProviderModel: "gpt-4o",
    ProviderID: 11,
    ToolCall: true,
    StructuredOutput: false,
    Image: false,
    WithHeader: false,
    CustomerHeaders: {},
    ExtraBody: null,
    Status: true,
    Weight: 5,
    InputPrice: 0,
    CacheReadPrice: 0,
    OutputPrice: 0,
    Currency: "CNY",
    // 未配置峰谷条款：这条关联按基础价计费（后端那一列是 NULL）
    Peak: null,
    ...over,
  }
}

/** 按模型 ID 给关联：模型 1 有两条、模型 2 没有——"每个模型各自的关联数"才可断言 */
function associationsOf(id: number): ModelWithProvider[] {
  if (id === 1) {
    return [
      association(),
      association({ ID: 102, ProviderModel: "gpt-4o-mini", ProviderID: 12, Weight: 8 }),
    ]
  }
  return []
}

function renderPage(path = "/model-providers") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ModelProvidersPage />
    </MemoryRouter>
  )
}

/** 表格行的第 4 列是"已关联模型"——列序即这一页的语义契约，索引读的是它 */
function cellText(row: HTMLElement, index: number): string {
  return within(row).getAllByRole("cell")[index].textContent ?? ""
}

/** 唯一的那张表（桌面表格；手机卡片不是 table）——避开同名元素出现两次的干扰 */
function table(): Promise<HTMLElement> {
  return screen.findByRole("table")
}

/** 打开某条关联的编辑对话框（表格那一套实现；卡片列表是同一份逻辑的另一处渲染） */
async function openAssociationDialog(providerModel = "gpt-4o") {
  const user = userEvent.setup()
  renderPage("/model-providers?modelId=1")
  const t = await table()
  await within(t).findByText(providerModel)
  const row = within(t)
    .getAllByRole("row")
    .find((r) => r.textContent?.includes(providerModel))
  if (!row) throw new Error(`关联表里没有 ${providerModel} 这一行`)
  await user.click(within(row).getByRole("button", { name: "编辑" }))
  const dialog = await screen.findByRole("dialog")
  return { user, dialog }
}

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getModelOptions.mockResolvedValue([model()])
  mocked.getProviders.mockResolvedValue([provider()])
  mocked.getModelProviders.mockImplementation(async (id: number) => associationsOf(id))
  mocked.getModelProviderStatus.mockResolvedValue([true, false])
  mocked.getProviderModels.mockResolvedValue([])
  // 弹窗打开时会读一次自动填写策略。给一个"没配过"的回执（新装状态），
  // 预填按默认策略（开）走；不给的话 getConfig 返回 undefined，.then 会抛。
  mocked.getConfig.mockResolvedValue({ key: "model_autofill_policy", value: "" })
  const i18n = (await import("@/i18n")).default
  await i18n.changeLanguage("zh-CN")
})

describe("模型路由页 · 加载", () => {
  it("两个来源都没落地时说正在加载，不说没有模型", () => {
    mocked.getModelOptions.mockReturnValue(new Promise(() => {}))
    mocked.getProviders.mockReturnValue(new Promise(() => {}))

    renderPage()

    // 声明"正在加载"而不是只转个圈：转圈在无障碍树上等于空白
    expect(screen.getByRole("status")).toHaveTextContent("加载模型和提供商")
    expect(screen.getByText(/加载模型和提供商/)).toBeInTheDocument()
    expect(screen.queryByText("暂无可关联模型")).not.toBeInTheDocument()
  })
})

describe("模型路由页 · 空", () => {
  it("没有任何模型时给出新建入口，而不是一条死路", async () => {
    mocked.getModelOptions.mockResolvedValue([])

    renderPage()

    expect(await screen.findByText("暂无可关联模型")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "添加模型" })).toBeInTheDocument()
  })

  it("筛选筛空与本来就没有，说法不同", async () => {
    const user = userEvent.setup()
    mocked.getModelOptions.mockResolvedValue([model()])

    renderPage()
    await within(await table()).findByText("gpt-test")

    await user.type(screen.getByPlaceholderText("按名称搜索"), "不存在的名字")

    // 防抖 300ms 后才生效，断言最终态即可
    expect(await screen.findByText("没有符合筛选条件的模型")).toBeInTheDocument()
    expect(screen.queryByText("暂无可关联模型")).not.toBeInTheDocument()
  })
})

describe("模型路由页 · 有数据", () => {
  it("每个模型各自显示自己的关联数，不是一个总数", async () => {
    mocked.getModelOptions.mockResolvedValue([
      model({ ID: 1, Name: "gpt-test" }),
      model({ ID: 2, Name: "claude-test" }),
    ])

    renderPage()

    const first = await screen.findByRole("row", { name: /gpt-test/ })
    const second = screen.getByRole("row", { name: /claude-test/ })
    // 关联数是另一批请求，行先渲染出来不等于数字已经到了——加载中那一列显示
    // "-"。这里原本同步断言，全量并行跑时偶尔读到 "-"（单跑必过，CI 上假红）。
    // 等第一行落定即可：两个数字来自同一次响应、同一次渲染。
    await waitFor(() => expect(cellText(first, 3)).toBe("2"))
    expect(cellText(second, 3)).toBe("0")
  })

  it("点一行进入该模型的关联列表，并把模型 ID 带进地址栏", async () => {
    const user = userEvent.setup()
    renderPage()
    const list = await table()

    await user.click(within(list).getByText("gpt-test"))

    // 进入关联视图：出现返回入口，且按该模型取关联
    expect(await screen.findByRole("button", { name: /返回模型列表/ })).toBeInTheDocument()
    await waitFor(() => expect(mocked.getModelProviders).toHaveBeenCalledWith(1))
  })

  it("带 modelId 直接打开时直接进关联视图", async () => {
    renderPage("/model-providers?modelId=1")
    // 先等关联视图的返回入口，再取表：模型列表那张表会先出现再被替换掉
    await screen.findByRole("button", { name: /返回模型列表/ })
    const associations = await table()

    expect(await within(associations).findByText("gpt-4o")).toBeInTheDocument()
    expect(within(associations).getByText("prov-a")).toBeInTheDocument()
  })

  it("返回列表时不再显示关联表", async () => {
    const user = userEvent.setup()
    renderPage("/model-providers?modelId=1")
    await screen.findByRole("button", { name: /返回模型列表/ })
    await within(await table()).findByText("gpt-4o")

    await user.click(screen.getByRole("button", { name: /返回模型列表/ }))

    // 回到模型列表：模型表回来了，关联视图整个卸载
    expect(await within(await table()).findByText("gpt-test")).toBeInTheDocument()
    expect(screen.queryByText("gpt-4o")).not.toBeInTheDocument()
  })
})

describe("模型路由页 · 关联列表的空", () => {
  it("该模型确实没有关联时说明这一点", async () => {
    mocked.getModelOptions.mockResolvedValue([model({ ID: 2, Name: "claude-test" })])

    renderPage("/model-providers?modelId=2")

    expect(await screen.findByText("该模型还没有关联的提供商")).toBeInTheDocument()
  })

  it("筛选筛空与确实没有，说法不同", async () => {
    const user = userEvent.setup()
    renderPage("/model-providers?modelId=1")
    await screen.findByRole("button", { name: /返回模型列表/ })
    await within(await table()).findByText("gpt-4o")

    await user.type(screen.getByPlaceholderText("按提供商/模型搜索"), "不存在的提供商")

    expect(await screen.findByText("当前筛选条件暂无关联")).toBeInTheDocument()
    expect(screen.queryByText("该模型还没有关联的提供商")).not.toBeInTheDocument()
  })
})

/**
 * 模型路由页 · 错误。
 *
 * 这一组是 S6 修掉的那处缺陷的钉子：取数失败原先只弹一条会自己消失的提示，
 * 正文照旧显示"暂无可关联模型"／"该模型还没有关联的提供商"——把失败说成了空，
 * 于是"没有模型"和"没取到"在页面上长得一模一样。
 */
describe("模型路由页 · 错误", () => {
  it("模型取数失败时给出原文与重试入口，而不是说没有模型", async () => {
    mocked.getModelOptions.mockRejectedValueOnce(new Error("连接被拒绝"))
    renderPage()

    expect(await screen.findByText("模型与提供商加载失败")).toBeInTheDocument()
    expect(screen.getByText("连接被拒绝")).toBeInTheDocument()
    expect(screen.queryByText("暂无可关联模型")).not.toBeInTheDocument()
  })

  it("提供商取数失败同样算失败——两个来源缺一不可", async () => {
    mocked.getProviders.mockRejectedValueOnce(new Error("网关超时"))
    renderPage()

    expect(await screen.findByText("模型与提供商加载失败")).toBeInTheDocument()
    expect(screen.getByText("网关超时")).toBeInTheDocument()
  })

  it("重试把两个来源一起重取，成功后就地恢复成列表", async () => {
    const user = userEvent.setup()
    mocked.getModelOptions.mockRejectedValueOnce(new Error("连接被拒绝"))
    renderPage()
    await screen.findByText("模型与提供商加载失败")

    mocked.getModelOptions.mockResolvedValue([model()])
    await user.click(screen.getByRole("button", { name: /重试/ }))

    expect(await within(await table()).findByText("gpt-test")).toBeInTheDocument()
    expect(screen.queryByText("模型与提供商加载失败")).not.toBeInTheDocument()
  })

  it("关联取数失败时给出原文，而不是说这个模型没有关联", async () => {
    mocked.getModelProviders.mockRejectedValue(new Error("500 internal error"))
    renderPage("/model-providers?modelId=1")

    expect(await screen.findByText("关联列表加载失败")).toBeInTheDocument()
    expect(screen.getByText("500 internal error")).toBeInTheDocument()
    expect(screen.queryByText("该模型还没有关联的提供商")).not.toBeInTheDocument()
  })

  it("关联取数失败后重试，恢复成关联表", async () => {
    const user = userEvent.setup()
    mocked.getModelProviders.mockRejectedValue(new Error("500 internal error"))
    renderPage("/model-providers?modelId=1")
    await screen.findByText("关联列表加载失败")

    mocked.getModelProviders.mockImplementation(async (id: number) => associationsOf(id))
    await user.click(screen.getByRole("button", { name: /重试/ }))

    expect(await within(await table()).findByText("gpt-4o")).toBeInTheDocument()
    expect(screen.queryByText("关联列表加载失败")).not.toBeInTheDocument()
  })
})

/**
 * 模型路由页 · 近期成败与能力列的读法。
 *
 * 一排绿/红小格是纯颜色信息：色盲用户分不出来，读屏用户更是读不到
 * （原先小格只挂了一个 hover 才出现的 title，键盘与读屏都够不着）。
 * 因此小格标为装饰，含义交给旁边的计数文字；能力列的对勾/叉号同理，
 * 两个符号本身没有可读的名字，得带上一句"支持/不支持"。
 * 这一组钉的就是"含义必须有文字承载"，而不是只钉颜色对不对。
 */
describe("模型路由页 · 近期成败的读法", () => {
  it("近期成败给出计数文字，小格本身对读屏隐藏", async () => {
    renderPage("/model-providers?modelId=1")
    const t = await table()
    await within(t).findByText("gpt-4o")

    // mock 给的是 [true, false]：两条关联各一排，都该读成"成功 1/2"
    expect(within(t).getAllByText("成功 1/2")).toHaveLength(2)
    // 两套实现（桌面表格 / 手机卡片）都要说这句话，不是只改一处
    expect(screen.getAllByText("成功 1/2")).toHaveLength(4)

    for (const bar of t.querySelectorAll(".bg-status-good, .bg-status-critical")) {
      expect(bar.closest('[aria-hidden="true"]')).not.toBeNull()
    }
  })

  it("能力列的对勾/叉号带得出名字，而不是两个孤零零的符号", async () => {
    renderPage("/model-providers?modelId=1")
    const t = await table()
    await within(t).findByText("gpt-4o")

    // 第 0 行是表头；第 4 列工具调用（true）、第 6 列视觉（false）
    const cells = within(within(t).getAllByRole("row")[1]).getAllByRole("cell")
    expect(within(cells[4]).getByRole("img", { name: "支持" })).toBeInTheDocument()
    expect(within(cells[6]).getByRole("img", { name: "不支持" })).toBeInTheDocument()
  })
})

/**
 * 模型路由页 · 测试对话框。
 *
 * 对话框里的"测试中"原先是一个自己写的转圈 div（border-gray-900），既没说
 * role="status" 也没跟主题走：深色下那个圈几乎看不见，读屏用户听到的还是
 * "什么都没发生"。这里钉住"测试进行中"这句话在无障碍树上存在。
 */
describe("模型路由页 · 测试对话框", () => {
  it("连通性测试进行中声明正在测试，而不是一片空白", async () => {
    const user = userEvent.setup()
    // 永不落地的 Promise：把界面钉在"进行中"这一刻
    vi.mocked(testModelProvider).mockReturnValue(new Promise(() => {}))
    renderPage("/model-providers?modelId=1")
    const t = await table()
    await within(t).findByText("gpt-4o")

    // 行内的 ⚡（aria-label 测试）；两行各一个，取第一行
    await user.click(within(t).getAllByRole("button", { name: "测试" })[0])

    const dialog = await screen.findByRole("dialog")
    await user.click(within(dialog).getByRole("button", { name: "执行测试" }))

    expect(within(dialog).getByRole("status")).toHaveTextContent("测试中")
  })
})

/**
 * 键盘的排序路径（方案 D9：模型排序拖拽要能全程用键盘完成）。
 *
 * 拖拽只有鼠标能做，但"调顺序"这件事本身是键盘可做的：焦点落在某一行上时
 * Alt+↑/↓ 上下移一位，走的是与拖拽完全相同的那条保存路径。
 * 断言三件事：移了没有（保存的参数）、移到头了说什么、筛选时为什么不动。
 */
describe("模型路由页 · 键盘排序", () => {
  /** 两个模型：gpt-test 在前（DisplayOrder 大者在前） */
  function twoModels() {
    mocked.getModelOptions.mockResolvedValue([
      model({ ID: 1, Name: "gpt-test", DisplayOrder: 2 }),
      model({ ID: 2, Name: "claude-test", DisplayOrder: 1 }),
    ])
  }

  /** 表格里的模型行，按显示顺序（第 0 行是表头） */
  async function modelRows(): Promise<HTMLElement[]> {
    return within(await table()).getAllByRole("row").slice(1)
  }

  async function rowOf(name: string): Promise<HTMLElement> {
    return screen.findByRole("row", { name: new RegExp(name) })
  }

  /**
   * 排序结果的实时播报区。
   *
   * 不按文字找元素：这些句子在页面上别处也出现（比如工具栏会说明为什么
   * 筛选中不能排序），按文字找会撞上，而撞上之后断言就不再是它以为的那件事。
   */
  function liveRegion(): HTMLElement {
    const region = document.querySelector('[aria-live="polite"]')
    if (!region) throw new Error("页面上没有实时播报区")
    return region as HTMLElement
  }

  it("Alt+↓ 把焦点所在的那一行下移一位，并就地保存新顺序", async () => {
    const user = userEvent.setup()
    twoModels()
    renderPage()
    const first = await rowOf("gpt-test")
    expect((await modelRows()).map((row) => cellText(row, 1))).toEqual(["gpt-test", "claude-test"])

    first.focus()
    await user.keyboard("{Alt>}{ArrowDown}{/Alt}")

    // 保存的是移过之后的完整顺序，不只是动了的那一条
    await waitFor(() => expect(mocked.updateModelOrder).toHaveBeenCalledWith([2, 1]))
    expect((await modelRows()).map((row) => cellText(row, 1))).toEqual(["claude-test", "gpt-test"])
    // 移动要说给读屏软件听：移的是谁、现在在第几位
    await waitFor(() => expect(liveRegion()).toHaveTextContent("「gpt-test」已移到第 2 位"))
  })

  it("移到列表尽头时只说明到了尽头，不做多余的保存", async () => {
    const user = userEvent.setup()
    mocked.getModelOptions.mockResolvedValue([model({ ID: 1, Name: "gpt-test" })])
    renderPage()

    const row = await rowOf("gpt-test")
    row.focus()
    await user.keyboard("{Alt>}{ArrowUp}{/Alt}")

    await waitFor(() => expect(liveRegion()).toHaveTextContent("已经在列表的开头或结尾"))
    expect(mocked.updateModelOrder).not.toHaveBeenCalled()
  })

  it("筛选中拒绝排序，并说明是筛选挡着而不是没反应", async () => {
    const user = userEvent.setup()
    twoModels()
    renderPage()
    await screen.findByRole("row", { name: /gpt-test/ })

    // 筛选有 300ms 防抖，没等它生效就按键，测的会是"没筛"的那条路径
    await user.type(screen.getByPlaceholderText("按名称搜索"), "gpt")
    await waitFor(() =>
      expect(screen.getByText("筛选状态下不能调整顺序，请先清空筛选")).toBeInTheDocument()
    )

    const row = await rowOf("gpt-test")
    row.focus()
    await user.keyboard("{Alt>}{ArrowDown}{/Alt}")

    await waitFor(() => expect(liveRegion()).toHaveTextContent("筛选状态下不能调整顺序"))
    expect(mocked.updateModelOrder).not.toHaveBeenCalled()
  })

  it("移完之后焦点仍在同一行，可以接着往下移", async () => {
    const user = userEvent.setup()
    twoModels()
    mocked.getModelOptions.mockResolvedValue([
      model({ ID: 1, Name: "gpt-test", DisplayOrder: 3 }),
      model({ ID: 2, Name: "claude-test", DisplayOrder: 2 }),
      model({ ID: 3, Name: "gemini-test", DisplayOrder: 1 }),
    ])
    renderPage()
    const first = await rowOf("gpt-test")

    first.focus()
    await user.keyboard("{Alt>}{ArrowDown}{/Alt}")
    // 行在 DOM 里被挪了位置，焦点跟着回来才谈得上"连着按"
    await waitFor(() => expect(document.activeElement).toHaveAttribute("data-model-id", "1"))

    await user.keyboard("{Alt>}{ArrowDown}{/Alt}")
    await waitFor(() => expect(mocked.updateModelOrder).toHaveBeenLastCalledWith([2, 3, 1]))
  })
})

/**
 * 模型路由页 · 关联表单里的峰谷条款。
 *
 * 峰谷计费的第一半（时段与乘数）挂在「模型 × 上游」的关联上：同一时刻 A 家
 * 打折、B 家峰时是正常的，所以它是**这条上游的商务条款**，不是全局配置。
 * 全局只剩工作日日历（时区、节假日），在配置页维护、由 peak-calendar.test.tsx 覆盖。
 *
 * 这一组钉四件事：
 *
 *   1. **三态都要能走到**：未配置（peak 为 null）→ 配置 → 移除回 null。
 *      最后那一步尤其要紧：后端专门在结构体更新之外补了一次显式清空
 *      （GORM 会跳过 nil 指针），界面走不到那条路径的话，那处修复就没人验证。
 *   2. **顺序即优先级**：上移/下移换的是数组位置，后端首个命中者胜出，
 *      写反了谁贵谁便宜就跟着反，而界面上看起来都"排好了"。
 *   3. **本地校验拦在提交之前**，且点名第几段、哪一项、原值是什么——
 *      校验通过之前一次网络请求都不该发出去。
 *   4. **预览按服务端回传的时区渲染**：编辑器手上没有全局日历，用浏览器本地
 *      时区显示会出现"08:30 命中了夜间优惠"这种自相矛盾的画面。
 */
describe("模型表单 · 优先同协议", () => {
  const SWITCH = "优先匹配相同协议"

  /** 打开某一行（表格里那一个按钮）的编辑对话框 */
  async function openEdit() {
    const user = userEvent.setup()
    await within(await table()).findByText("gpt-test")
    await user.click(within(await table()).getByRole("button", { name: "编辑模型" }))
    return user
  }

  it("新建时默认勾上，并随请求一起提交", async () => {
    const user = userEvent.setup()
    mocked.createModel.mockResolvedValue(model())
    renderPage()
    await table()

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    const box = await screen.findByRole("checkbox", { name: SWITCH })
    expect(box).toBeChecked()

    await user.type(screen.getByLabelText("名称"), "gpt-new")
    await user.click(screen.getByRole("button", { name: "创建" }))

    await waitFor(() =>
      expect(mocked.createModel).toHaveBeenCalledWith(expect.objectContaining({ prefer_direct: true }))
    )
  })

  it("库里没有这一列的老模型：打开时是勾着的，保存也不会把它改成关", async () => {
    // 这一列是后加的，老行的值是 NULL。若把"缺失"读成"关"，用户只是进来看一眼、
    // 保存一下，候选池就从"先挑本协议"变成"整池按权重摇"——一次静默的流量改道
    mocked.updateModel.mockResolvedValue(model())
    renderPage()
    const user = await openEdit()

    expect(await screen.findByRole("checkbox", { name: SWITCH })).toBeChecked()
    await user.click(screen.getByRole("button", { name: "更新" }))

    await waitFor(() =>
      expect(mocked.updateModel).toHaveBeenCalledWith(1, expect.objectContaining({ prefer_direct: true }))
    )
  })

  it("明确关掉过的模型：打开时没勾，原样跟着 false 提交", async () => {
    mocked.getModelOptions.mockResolvedValue([model({ PreferDirect: false })])
    mocked.updateModel.mockResolvedValue(model())
    renderPage()
    const user = await openEdit()

    expect(await screen.findByRole("checkbox", { name: SWITCH })).not.toBeChecked()
    await user.click(screen.getByRole("button", { name: "更新" }))

    await waitFor(() =>
      expect(mocked.updateModel).toHaveBeenCalledWith(1, expect.objectContaining({ prefer_direct: false }))
    )
  })
})

describe("关联表单 · 峰谷条款", () => {
  const 已配条款: PeakTerms = {
    enabled: true,
    periods: [
      { name: "夜间优惠", start: "00:30", end: "08:30", multiplier: 0.25 },
      { name: "周末加价", start: "00:00", end: "24:00", multiplier: 1.5, days: [0, 6] },
    ],
  }

  it("未配置时只给一个入口，不摆一份看起来已生效的空条款", async () => {
    const { dialog } = await openAssociationDialog()

    expect(within(dialog).getByRole("button", { name: "配置峰谷时段" })).toBeInTheDocument()
    // 没配就是没配：不该先渲染出开关和时段列表让人以为已经在用了
    expect(within(dialog).queryByRole("switch", { name: "启用峰谷计费" })).not.toBeInTheDocument()
    expect(within(dialog).queryByRole("list", { name: "时段列表" })).not.toBeInTheDocument()
  })

  it("点入口给一份关闭状态的示例条款（与后端默认值一致）", async () => {
    mocked.updateModelProvider.mockResolvedValue(association())
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("button", { name: "配置峰谷时段" }))

    expect(within(dialog).getByRole("switch", { name: "启用峰谷计费" })).not.toBeChecked()
    const rows = within(within(dialog).getByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )
    expect(rows).toHaveLength(2)
    expect(within(rows[1]).getByLabelText("名称")).toHaveValue("夜间优惠")
    expect(within(rows[1]).getByLabelText("价格系数")).toHaveValue(0.25)
  })

  it("已配置的关联回填条款，开关、时刻、乘数、星期都在", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    const { dialog } = await openAssociationDialog()

    expect(within(dialog).getByRole("switch", { name: "启用峰谷计费" })).toBeChecked()
    const rows = within(within(dialog).getByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )
    expect(within(rows[0]).getByLabelText("名称")).toHaveValue("夜间优惠")
    expect(within(rows[0]).getByLabelText("开始")).toHaveValue("00:30")
    expect(within(rows[0]).getByLabelText("价格系数")).toHaveValue(0.25)
    // 第二条只在周末生效：周日与周六两个按钮是按下状态
    expect(within(rows[1]).getByRole("button", { name: "周日" })).toHaveAttribute(
      "aria-pressed",
      "true"
    )
    expect(within(rows[1]).getByRole("button", { name: "周六" })).toHaveAttribute(
      "aria-pressed",
      "true"
    )
    expect(within(rows[1]).getByRole("button", { name: "周一" })).toHaveAttribute(
      "aria-pressed",
      "false"
    )
  })

  it("保存时把条款随关联一起提交（归一后的形状）", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.updateModelProvider.mockResolvedValue(association({ Peak: 已配条款 }))
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("button", { name: "更新" }))

    await waitFor(() => expect(mocked.updateModelProvider).toHaveBeenCalledTimes(1))
    expect(mocked.updateModelProvider.mock.calls[0][1].peak).toEqual(已配条款)
  })

  it("关掉开关只是停用：时段原样留着，不必抄下来备份", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.updateModelProvider.mockResolvedValue(association({ Peak: 已配条款 }))
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("switch", { name: "启用峰谷计费" }))
    await user.click(within(dialog).getByRole("button", { name: "更新" }))

    await waitFor(() => expect(mocked.updateModelProvider).toHaveBeenCalledTimes(1))
    const sent = mocked.updateModelProvider.mock.calls[0][1].peak
    expect(sent?.enabled).toBe(false)
    expect(sent?.periods).toEqual(已配条款.periods)
  })

  it("移除峰谷配置提交的是 null（后端就靠这一次显式清空把旧条款抹掉）", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.updateModelProvider.mockResolvedValue(association())
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("button", { name: "移除峰谷配置" }))

    // 移除之后就回到"未配置"那一态：入口重新出现，条款编辑器整个收起来
    expect(await within(dialog).findByRole("button", { name: "配置峰谷时段" })).toBeInTheDocument()
    expect(within(dialog).queryByRole("list", { name: "时段列表" })).not.toBeInTheDocument()

    await user.click(within(dialog).getByRole("button", { name: "更新" }))
    await waitFor(() => expect(mocked.updateModelProvider).toHaveBeenCalledTimes(1))
    // 必须是 null 而不是省略：后端把两者都当清空，但显式的 null 在请求体里
    // 就看得见"这一步是要清掉"，不用去读后端代码才知道
    expect(mocked.updateModelProvider.mock.calls[0][1].peak).toBeNull()
  })

  it("新建关联默认不带条款（未配置就是按基础价）", async () => {
    const user = userEvent.setup()
    renderPage("/model-providers?modelId=1")
    await within(await table()).findByText("gpt-4o")

    await user.click(screen.getByRole("button", { name: "添加关联" }))
    const dialog = await screen.findByRole("dialog")

    // 新建时条款是"未配置"那一态，而不是先塞一份关闭的默认条款：
    // 后端把 null 落成 NULL（按基础价），空着比塞一份更诚实。
    // 提交时这个 null 怎么进请求体，由上面"移除峰谷配置提交的是 null"钉住
    // （两条路径共用同一个 buildPayload）
    expect(within(dialog).getByRole("button", { name: "配置峰谷时段" })).toBeInTheDocument()
    expect(within(dialog).queryByRole("switch", { name: "启用峰谷计费" })).not.toBeInTheDocument()
  })

  it("时刻写坏时点名第几段、哪一项、原值是什么，并且一次请求都不发", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    const { user, dialog } = await openAssociationDialog()

    const first = within(within(dialog).getByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )[0]
    const start = within(first).getByLabelText("开始")
    await user.clear(start)
    await user.type(start, "25:00")
    await user.click(within(dialog).getByRole("button", { name: "更新" }))

    expect(
      await within(dialog).findByText("第 1 段「夜间优惠」的开始时间「25:00」不是 HH:MM")
    ).toBeInTheDocument()
    expect(mocked.updateModelProvider).not.toHaveBeenCalled()
    // 对话框留着，改完可以接着按
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("改好之后红色自己消失，不再拦着保存", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.updateModelProvider.mockResolvedValue(association({ Peak: 已配条款 }))
    const { user, dialog } = await openAssociationDialog()

    const first = within(within(dialog).getByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )[0]
    const end = within(first).getByLabelText("结束")
    await user.clear(end)
    // 与开始时间（00:30）相同：后端会拒——那等于覆盖全天
    await user.type(end, "00:30")
    await user.click(within(dialog).getByRole("button", { name: "更新" }))
    await within(dialog).findByText(/开始与结束相同/)

    await user.clear(end)
    await user.type(end, "09:00")
    expect(within(dialog).queryByText(/开始与结束相同/)).not.toBeInTheDocument()

    await user.click(within(dialog).getByRole("button", { name: "更新" }))
    await waitFor(() => expect(mocked.updateModelProvider).toHaveBeenCalledTimes(1))
  })

  it("上移一段换的是判定顺序，不是展示顺序", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.updateModelProvider.mockResolvedValue(association({ Peak: 已配条款 }))
    const { user, dialog } = await openAssociationDialog()

    const rows = () =>
      within(within(dialog).getByRole("list", { name: "时段列表" })).getAllByRole("listitem")
    expect(within(rows()[0]).getByLabelText("名称")).toHaveValue("夜间优惠")

    await user.click(within(rows()[1]).getByRole("button", { name: /上移/ }))
    expect(within(rows()[0]).getByLabelText("名称")).toHaveValue("周末加价")

    await user.click(within(dialog).getByRole("button", { name: "更新" }))
    await waitFor(() => expect(mocked.updateModelProvider).toHaveBeenCalledTimes(1))
    // 顺序真的进了载荷——后端首个命中者胜出，这里写反了价格就反了
    expect(mocked.updateModelProvider.mock.calls[0][1].peak?.periods.map((p) => p.name)).toEqual([
      "周末加价",
      "夜间优惠",
    ])
  })

  it("两段重叠只提示不拦（后端允许重叠，靠顺序裁决）", async () => {
    mocked.getModelProviders.mockResolvedValue([
      association({
        Peak: {
          enabled: true,
          periods: [
            { name: "标准时段", start: "08:00", end: "20:00", multiplier: 1 },
            { name: "夜间优惠", start: "19:00", end: "08:00", multiplier: 0.25 },
          ],
        },
      }),
    ])
    mocked.updateModelProvider.mockResolvedValue(association())
    const { user, dialog } = await openAssociationDialog()

    expect(
      await within(dialog).findByText("第 1 段与第 2 段在时间上重叠；重叠的时刻由靠前的第 1 段生效。")
    ).toBeInTheDocument()

    await user.click(within(dialog).getByRole("button", { name: "更新" }))
    await waitFor(() => expect(mocked.updateModelProvider).toHaveBeenCalledTimes(1))
  })

  it("预览把服务端的时间轴按服务端回传的时区画出来，并说明日历是全局的", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.previewPeakTerms.mockResolvedValue({
      // 服务端判定用的时区。编辑器手上没有全局日历，只能靠回传
      timezone: "Asia/Shanghai",
      points: [
        {
          start: Date.UTC(2026, 9, 1, 0, 30),
          end: Date.UTC(2026, 9, 1, 1, 0),
          period: "夜间优惠",
          multiplier: 0.25,
          workday: true,
        },
        {
          start: Date.UTC(2026, 9, 3, 0, 0),
          end: Date.UTC(2026, 9, 3, 1, 0),
          period: "",
          multiplier: 1,
          workday: false,
        },
      ],
    })
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("button", { name: "预览时间轴" }))

    // UTC 00:30 在东八区就是 08:30：按回传的时区渲染，不是浏览器本地时区
    expect(await within(dialog).findByText("10-01 08:30 → 10-01 09:00")).toBeInTheDocument()
    expect(within(dialog).getByText("×0.25")).toBeInTheDocument()
    // 命中空名的那段是基础价，并标出当天是休息日
    expect(within(dialog).getByText("基础价")).toBeInTheDocument()
    expect(within(dialog).getByText("休息日")).toBeInTheDocument()
    expect(mocked.previewPeakTerms).toHaveBeenCalledWith(已配条款, 7)
    // 日历不在这一页：得说清预览是按哪一份日历判定的，否则用户会以为
    // 时区和节假日也在这一条关联里
    expect(
      within(dialog).getByText("预览按已保存的工作日日历判定（时区与节假日来自「配置」页）。")
    ).toBeInTheDocument()
  })

  it("改了条款就作废上一次的预览（留着旧时间轴等于给出改之前的答案）", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.previewPeakTerms.mockResolvedValue({
      timezone: "UTC",
      points: [
        {
          start: Date.UTC(2026, 9, 1, 0, 0),
          end: Date.UTC(2026, 9, 1, 1, 0),
          period: "夜间优惠",
          multiplier: 0.25,
          workday: true,
        },
      ],
    })
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("button", { name: "预览时间轴" }))
    expect(await within(dialog).findByText("10-01 00:00 → 10-01 01:00")).toBeInTheDocument()

    const first = within(within(dialog).getByRole("list", { name: "时段列表" })).getAllByRole(
      "listitem"
    )[0]
    await user.type(within(first).getByLabelText("名称"), "改")

    expect(within(dialog).queryByText("10-01 00:00 → 10-01 01:00")).not.toBeInTheDocument()
  })

  it("预览失败给原文与重试，不是一声不响", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.previewPeakTerms.mockRejectedValueOnce(new Error("days must be between 1 and 31"))
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("button", { name: "预览时间轴" }))

    expect(await within(dialog).findByText("预览失败")).toBeInTheDocument()
    expect(within(dialog).getByText("days must be between 1 and 31")).toBeInTheDocument()

    mocked.previewPeakTerms.mockResolvedValue({ timezone: "UTC", points: [] })
    await user.click(within(dialog).getByRole("button", { name: "重试" }))

    expect(
      await within(dialog).findByText("这段时间内没有任何时段命中，全部按基础价。")
    ).toBeInTheDocument()
    expect(within(dialog).queryByText("预览失败")).not.toBeInTheDocument()
  })

  it("改了预览天数就按新的天数去问", async () => {
    mocked.getModelProviders.mockResolvedValue([association({ Peak: 已配条款 })])
    mocked.previewPeakTerms.mockResolvedValue({ timezone: "UTC", points: [] })
    const { user, dialog } = await openAssociationDialog()

    await user.click(within(dialog).getByRole("radio", { name: "3" }))
    await user.click(within(dialog).getByRole("button", { name: "预览时间轴" }))

    await waitFor(() => expect(mocked.previewPeakTerms).toHaveBeenCalledWith(已配条款, 3))
  })
})

/**
 * 换目标的预填（用户实测报的那条 bug）。
 *
 * 表单里那些值是预填自己写进去的，但它长得和"用户确认过的值"一模一样——
 * `setValue` 不标脏，dirtyFields 认不出来。于是换了个上游模型名之后，新目标
 * 的值全被"已有值"挡在外面：勾没取消、价也没覆盖，面板还写着"已有值，未覆盖"。
 *
 * 这条只有走完整弹窗才测得到：判定的两半分别在纯函数（lib/model-autofill）与
 * 这一页的 effect 里，前者测不到"痕迹要撤回"，后者测不到"撤回之后还能不能填"。
 */
describe("关联表单 · 自动填写换目标", () => {
  /** 源里那条记录；默认什么都不给，用例只写关心的那几项。 */
  function 建议(model: string, over: Partial<ModelMetadataSuggestion> = {}): ModelMetadataSuggestion {
    return {
      matched: true,
      source: "models.dev",
      provider: "opencode",
      provider_name: "OpenCode Zen",
      model,
      tool_call: null,
      structured_output: null,
      image: null,
      input_price: null,
      cache_read_price: null,
      output_price: null,
      ...over,
    }
  }

  /**
   * 两档价格为 0 时输入框是空的（PriceInput 的 0 与"没填"共用同一个显示），
   * 所以这里读的是 name 而不是 value——`input[name]` 就是表单字段名。
   */
  function 价格(dialog: HTMLElement, name: "input_price" | "cache_read_price" | "output_price") {
    const input = dialog.querySelector<HTMLInputElement>(`input[name="${name}"]`)
    if (!input) throw new Error(`表单里没有 ${name} 这个输入框`)
    return input.value
  }

  const 视觉 = (dialog: HTMLElement) => within(dialog).getByRole("checkbox", { name: "视觉" })

  it("改了上游模型名：新目标给的值要覆盖旧的，旧目标留下的勾也要取消", async () => {
    // 用编辑对话框而不是新建：编辑态上游是定死的，不必去点 Radix 的
    // Select（jsdom 里没有 pointer capture，点不动），而这一条要测的是
    // "换了模型名之后"，与上游怎么选上的无关。
    mocked.getModelProviders.mockResolvedValue([
      association({ ProviderModel: "claude-sonnet-4-5" }),
    ])
    mocked.getModelMetadata.mockImplementation(async (_id, model) =>
      model === "claude-sonnet-4-5"
        ? 建议("claude-sonnet-4-5", {
            tool_call: true,
            structured_output: true,
            image: true,
            input_price: 3,
            cache_read_price: 0.3,
            output_price: 15,
            currency: "USD",
          })
        : 建议("glm-5", {
            tool_call: true,
            // 源没给 structured_output：它必须回到"没勾"，而不是留着 A 的勾
            image: false,
            input_price: 1,
            cache_read_price: 0.2,
            output_price: 3.2,
            currency: "USD",
          })
    )

    const { user, dialog } = await openAssociationDialog("claude-sonnet-4-5")

    // A 填完了：能力三项都勾上，三档价 3 / 0.3 / 15
    await waitFor(() => expect(价格(dialog, "output_price")).toBe("15"))
    expect(视觉(dialog)).toBeChecked()
    expect(within(dialog).getByRole("checkbox", { name: "结构化输出" })).toBeChecked()

    const 模型名 = within(dialog).getByPlaceholderText("输入或选择提供商模型")
    await user.clear(模型名)
    await user.type(模型名, "glm-5")

    // B 在源里不支持视觉、价格也不同：两样都得跟着换
    await waitFor(() => expect(价格(dialog, "output_price")).toBe("3.2"))
    expect(价格(dialog, "input_price")).toBe("1")
    expect(价格(dialog, "cache_read_price")).toBe("0.2")
    expect(视觉(dialog)).not.toBeChecked()
    // 源没给 structured_output，所以它退回没勾——不是留着 A 的 true。
    // 留着的话等于替用户断言"这个上游支持结构化输出"，而路由会按它挑候选。
    expect(within(dialog).getByRole("checkbox", { name: "结构化输出" })).not.toBeChecked()
    // 用户看到的必须是"已填写"，不能再是那句把他劝退的话
    expect(within(dialog).queryByText(/已有值，未覆盖/)).not.toBeInTheDocument()
  })

  it("库里带过来的旧价也照新目标覆盖（换目标就是不再按旧值算）", async () => {
    // 编辑态那六格是从库里读出来的，预填第一次跑时它们受"只补空"保护（这是
    // 对的，打开弹窗不该动已保存的值）；但用户既然换了上游模型，那些值就
    // 不再描述这条关联了——留在原地会让 B 带着 A 的价格被保存。
    mocked.getModelProviders.mockResolvedValue([
      association({ ProviderModel: "gpt-4o", Image: true, InputPrice: 9, OutputPrice: 99 }),
    ])
    mocked.getModelMetadata.mockImplementation(async (_id, model) =>
      model === "glm-5"
        ? 建议("glm-5", {
            image: false,
            input_price: 1,
            cache_read_price: 0.2,
            output_price: 3.2,
            currency: "USD",
          })
        : { ...建议(model), matched: false, reason: "no_model_match" }
    )

    const { user, dialog } = await openAssociationDialog("gpt-4o")
    // 打开时按库里那条查（查不到），已保存的值一动不动
    expect(价格(dialog, "input_price")).toBe("9")
    expect(视觉(dialog)).toBeChecked()

    await user.clear(within(dialog).getByPlaceholderText("输入或选择提供商模型"))
    await user.type(within(dialog).getByPlaceholderText("输入或选择提供商模型"), "glm-5")

    await waitFor(() => expect(价格(dialog, "output_price")).toBe("3.2"))
    expect(价格(dialog, "input_price")).toBe("1")
    expect(视觉(dialog)).not.toBeChecked()
  })
})
