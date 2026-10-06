import { describe, expect, it } from "vitest"

import type { ModelAutofillPolicy, ModelMetadataCandidate, ModelMetadataSuggestion } from "@/lib/api"
import {
  AUTOFILL_FIELDS,
  classifyFailure,
  computeAutofill,
  isUsable,
  suggestionFromCandidate,
  type AutofillField,
  type AutofillResult,
  type AutofillSnapshot,
} from "@/lib/model-autofill"

/**
 * 这里测的全是"什么情况下**不**写"。
 *
 * 这一层抽成纯函数就是为了能这样测：保护失效在界面上的表现只是"这次没填上"，
 * 手点分不出来，而少一道门就可能把用户确认过的 `true` 覆盖成 `false`，
 * 让那个上游从工具调用的候选池里直接消失。
 */

/** 表单刚打开时的样子：能力全 false、价格全 0——新建时就是这样。 */
function emptySnapshot(): AutofillSnapshot {
  return {
    tool_call: false,
    structured_output: false,
    image: false,
    input_price: 0,
    cache_read_price: 0,
    output_price: 0,
    currency: "CNY",
  }
}

/** 源什么都没给的命中结果：每个字段都得显式写 null，不写就成了"没定义"。 */
function suggestion(overrides: Partial<ModelMetadataSuggestion> = {}): ModelMetadataSuggestion {
  return {
    matched: true,
    tool_call: null,
    structured_output: null,
    image: null,
    input_price: null,
    cache_read_price: null,
    output_price: null,
    ...overrides,
  }
}

const NO_EDIT: ReadonlySet<AutofillField> = new Set()
const KEEP_ONLY: Pick<ModelAutofillPolicy, "overwrite"> = { overwrite: false }
const OVERWRITE: Pick<ModelAutofillPolicy, "overwrite"> = { overwrite: true }

const filledFields = (result: AutofillResult) => result.fill.map((item) => item.field)

describe("自动填写 · 源没给就不写", () => {
  it("六个字段全是 null 时一个字都不写，逐项记成「没提供」", () => {
    const result = computeAutofill({
      suggestion: suggestion(),
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(result.fill).toEqual([])
    expect(result.missing).toEqual([...AUTOFILL_FIELDS])
    expect(result.skipped).toEqual([])
    expect(result.currency).toBeNull()
  })

  it("手动「重新填写」也不替源断言：null 依然不写", () => {
    // 手动只免掉 overwrite 与 edited 两道门，免不掉"源不知道"这一条——
    // 把 null 写成 false 等于替用户断言"这个模型不支持工具调用"。
    const result = computeAutofill({
      suggestion: suggestion(),
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: OVERWRITE,
      trigger: "manual",
    })

    expect(result.fill).toEqual([])
    expect(result.missing).toEqual([...AUTOFILL_FIELDS])
  })

  it("null 与 false 不是一回事：false 是源说他不行，不是源没说", () => {
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: false, structured_output: true }),
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    // tool_call 的 false 与表格里的 false 相同，所以不进 fill；但它绝不在
    // missing 里——"没提供"与"提供了 false"在提示语里是两句不同的话。
    expect(result.fill).toEqual([{ field: "structured_output", value: true }])
    expect(result.missing).not.toContain("tool_call")
    expect(result.missing).toContain("image")

    // 当前值不是 false 时，这条 false 就是一次真实改写。
    const overwritten = computeAutofill({
      suggestion: suggestion({ tool_call: false }),
      current: { ...emptySnapshot(), tool_call: true },
      edited: NO_EDIT,
      policy: OVERWRITE,
      trigger: "auto",
    })
    expect(overwritten.fill).toEqual([{ field: "tool_call", value: false }])
  })
})

describe("自动填写 · 不打断输入", () => {
  it("用户刚改过的字段一律不动，并说清是「改过」而不是「已有值」", () => {
    const current = emptySnapshot()
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: true }),
      current,
      edited: new Set<AutofillField>(["tool_call"]),
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(result.fill).toEqual([])
    expect(result.skipped).toEqual([{ field: "tool_call", reason: "edited" }])
  })

  it("正在输入中被跳过的是 input_price，不是别的格子", () => {
    // 输入框里有半截数字时最忌讳被后台的预填覆盖：那个数字是用户敲的。
    const current = { ...emptySnapshot(), input_price: 1.5 }
    const result = computeAutofill({
      suggestion: suggestion({ input_price: 3, output_price: 15 }),
      current,
      edited: new Set<AutofillField>(["input_price"]),
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(filledFields(result)).toEqual(["output_price"])
    expect(result.skipped).toEqual([{ field: "input_price", reason: "edited" }])
  })

  it("「改过」优先于「已有值」：用户刚碰过的那格不该被说成早就有值", () => {
    const current = { ...emptySnapshot(), tool_call: true }
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: true }),
      current,
      edited: new Set<AutofillField>(["tool_call"]),
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(result.skipped).toEqual([{ field: "tool_call", reason: "edited" }])
  })

  it("改过的那格此时是 false 也一样不动：判据是动过，不是当前有没有值", () => {
    // 这条是「不打断输入」的关键：用户把 tool_call 勾上又取消，停在 false。
    // 若保护只看"当前有没有值"，这一格会被立刻填回 true。
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: true }),
      current: emptySnapshot(),
      edited: new Set<AutofillField>(["tool_call"]),
      policy: OVERWRITE,
      trigger: "auto",
    })

    expect(result.fill).toEqual([])
    expect(result.skipped).toEqual([{ field: "tool_call", reason: "edited" }])
  })
})

describe("自动填写 · 已有值只补空", () => {
  it("已有的 true 不动：把它改写成 false 会让这个上游退出工具调用候选池", () => {
    const current = { ...emptySnapshot(), tool_call: true }
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: false, image: true }),
      current,
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(filledFields(result)).toEqual(["image"])
    expect(result.skipped).toEqual([{ field: "tool_call", reason: "existing" }])
  })

  it("已有非 0 价格不动", () => {
    const current = { ...emptySnapshot(), input_price: 2.5 }
    const result = computeAutofill({
      suggestion: suggestion({ input_price: 3, output_price: 15 }),
      current,
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(filledFields(result)).toEqual(["output_price"])
    expect(result.skipped).toEqual([{ field: "input_price", reason: "existing" }])
  })

  it("0 价与 false 都算没配过，所以老关联上照样填得进去", () => {
    // 库里 NULL 的能力在路由上等同 false，价格的 NULL 被启动迁移抹成 0，
    // 所以这两个值都与"没配过"不可区分；当成"有值"的话这个功能就只在
    // 全新关联上生效了。
    const result = computeAutofill({
      suggestion: suggestion({
        tool_call: true,
        structured_output: true,
        image: true,
        input_price: 1,
        cache_read_price: 0.1,
        output_price: 5,
        currency: "USD",
      }),
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(filledFields(result)).toEqual([...AUTOFILL_FIELDS])
  })

  it("只有打开 Overwrite 才会改已有值", () => {
    const current = { ...emptySnapshot(), tool_call: true, input_price: 2.5 }
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: false, input_price: 3, currency: "USD" }),
      current,
      edited: NO_EDIT,
      policy: OVERWRITE,
      trigger: "auto",
    })

    expect(result.fill).toEqual([
      { field: "tool_call", value: false },
      { field: "input_price", value: 3 },
    ])
    expect(result.skipped).toEqual([])
    expect(result.currency).toBe("USD")
  })
})

describe("自动填写 · 手动重新填写", () => {
  it("不看 Overwrite：策略是「只补空」也照样覆盖", () => {
    const current = { ...emptySnapshot(), tool_call: true }
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: false }),
      current,
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "manual",
    })

    expect(result.fill).toEqual([{ field: "tool_call", value: false }])
    expect(result.skipped).toEqual([])
  })

  it("不看「改过」：用户点重新填写就是要覆盖他刚改的那格", () => {
    const current = { ...emptySnapshot(), tool_call: true }
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: true }),
      current,
      edited: new Set<AutofillField>(["tool_call"]),
      policy: KEEP_ONLY,
      trigger: "manual",
    })

    // 值没变，所以也不进 fill——"已填写"清单里不该出现没动过的格子。
    expect(result.fill).toEqual([])
    expect(result.skipped).toEqual([])
  })
})

describe("自动填写 · 币种只在真的写了价格时跟着改", () => {
  it("只写了能力就不动币种", () => {
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: true, currency: "USD" }),
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(filledFields(result)).toEqual(["tool_call"])
    expect(result.currency).toBeNull()
  })

  it("写了价格就带上币种：源的价格是 USD，留着 CNY 等于拿美元数字当人民币计价", () => {
    const result = computeAutofill({
      suggestion: suggestion({ input_price: 3, currency: "USD" }),
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(result.currency).toBe("USD")
  })

  it("价格被跳过时也不写币种（没写价格就没有币种的事）", () => {
    const current = { ...emptySnapshot(), input_price: 2.5 }
    const result = computeAutofill({
      suggestion: suggestion({ input_price: 3, currency: "USD" }),
      current,
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(result.fill).toEqual([])
    expect(result.currency).toBeNull()
  })

  it("源没给币种时不写，价格照样填", () => {
    const result = computeAutofill({
      suggestion: suggestion({ input_price: 3 }),
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    expect(filledFields(result)).toEqual(["input_price"])
    expect(result.currency).toBeNull()
  })
})

describe("自动填写 · fill 只装真正要改的", () => {
  it("值没变的不进 fill，也就不会出现在「已填写 N 项」里", () => {
    const current = { ...emptySnapshot(), tool_call: true, output_price: 15 }
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: true, output_price: 15, image: true }),
      current,
      edited: NO_EDIT,
      policy: OVERWRITE,
      trigger: "auto",
    })

    expect(result.fill).toEqual([{ field: "image", value: true }])
  })

  it("六个字段在 fill / missing / skipped 里恰好各出现一次，一个不漏也不重", () => {
    const result = computeAutofill({
      suggestion: suggestion({ tool_call: true, input_price: 3 }),
      current: { ...emptySnapshot(), image: true },
      edited: new Set<AutofillField>(["output_price"]),
      policy: KEEP_ONLY,
      trigger: "auto",
    })

    const seen = [...filledFields(result), ...result.missing, ...result.skipped.map((item) => item.field)]
    expect([...seen].sort()).toEqual([...AUTOFILL_FIELDS].sort())
  })
})

describe("自动填写 · 未命中原因", () => {
  it("源自己报的原因原样透传，别的一律落到 unknown", () => {
    expect(classifyFailure(suggestion({ matched: false, reason: "no_provider_match" }))).toBe("no_provider_match")
    expect(classifyFailure(suggestion({ matched: false, reason: "no_model_match" }))).toBe("no_model_match")
    expect(classifyFailure(suggestion({ matched: false, reason: "catalog_unavailable" }))).toBe("catalog_unavailable")
    expect(classifyFailure(suggestion({ matched: false, reason: "deprecated_model" }))).toBe("deprecated_model")
    // 数据还没准备好与确实没这个模型要分得开：前者让人白等，后者让人去改模型名。
    expect(classifyFailure(suggestion({ matched: false, reason: "something_new" }))).toBe("unknown")
    expect(classifyFailure(suggestion({ matched: false }))).toBe("unknown")
  })

  it("只有 matched 才算命中：字段填得再满也不算", () => {
    expect(isUsable(suggestion({ matched: true }))).toBe(true)
    expect(isUsable(suggestion({ matched: false, tool_call: true }))).toBe(false)
  })
})

describe("自动填写 · 采用候选", () => {
  const candidate: ModelMetadataCandidate = {
    source: "models.dev",
    provider: "openai",
    provider_name: "OpenAI",
    model: "gpt-4o",
    tool_call: true,
    structured_output: true,
    image: true,
    input_price: 2.5,
    cache_read_price: 1.25,
    output_price: 10,
    currency: "USD",
    context_limit: 128000,
    output_limit: 16384,
    same_protocol: true,
  }

  it("候选换算成建议后照样只是建议：matched 为真，字段照搬", () => {
    const adopted = suggestionFromCandidate(candidate)

    expect(isUsable(adopted)).toBe(true)
    expect(adopted.source).toBe("models.dev")
    expect(adopted.model).toBe("gpt-4o")
    expect(adopted.currency).toBe("USD")

    const result = computeAutofill({
      suggestion: adopted,
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "manual",
    })
    expect(filledFields(result)).toEqual([...AUTOFILL_FIELDS])
  })

  it("候选没给的字段保持 null，不因为「采用了」就凭空补上", () => {
    const adopted = suggestionFromCandidate({
      ...candidate,
      tool_call: null,
      cache_read_price: null,
      output_price: null,
    })

    const result = computeAutofill({
      suggestion: adopted,
      current: emptySnapshot(),
      edited: NO_EDIT,
      policy: KEEP_ONLY,
      trigger: "manual",
    })

    expect(result.missing).toEqual(["tool_call", "cache_read_price", "output_price"])
  })
})
