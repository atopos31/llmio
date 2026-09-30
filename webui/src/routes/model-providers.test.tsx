import { render, screen, waitFor, within } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ModelProvidersPage from "@/routes/model-providers"
// 只导入要在断言里引用的那几个；其余端点由下面的 mock 工厂供给页面，
// 不导入是刻意的——导入了却不用会让 tsc 报未使用
import {
  createModel,
  deleteModel,
  getModelOptions,
  getModelProviderStatus,
  getModelProviders,
  getProviderModels,
  getProviders,
  type Model,
  type ModelWithProvider,
  type Provider,
  testModelProvider,
  updateModelOrder,
} from "@/lib/api"

// 这一页的数据全部经过 api.ts，切断它就断开了本页的全部 IO。
// 子组件（表单对话框、连通性测试、两个自定义 hook）也从这里取函数，
// 因此清单必须完整——漏一个会让它在运行时是 undefined 而不是"没被调用"。
vi.mock("@/lib/api", () => ({
  createModel: vi.fn(),
  createModelProvider: vi.fn(),
  deleteModel: vi.fn(),
  deleteModelProvider: vi.fn(),
  getModelOptions: vi.fn(),
  getModelProviderStatus: vi.fn(),
  getModelProviders: vi.fn(),
  getProviderModels: vi.fn(),
  getProviders: vi.fn(),
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
  deleteModel: vi.mocked(deleteModel),
  createModel: vi.mocked(createModel),
  getProviderModels: vi.mocked(getProviderModels),
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

beforeEach(async () => {
  vi.clearAllMocks()
  mocked.getModelOptions.mockResolvedValue([model()])
  mocked.getProviders.mockResolvedValue([provider()])
  mocked.getModelProviders.mockImplementation(async (id: number) => associationsOf(id))
  mocked.getModelProviderStatus.mockResolvedValue([true, false])
  mocked.getProviderModels.mockResolvedValue([])
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
