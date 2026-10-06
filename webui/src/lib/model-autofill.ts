import type { ModelAutofillPolicy, ModelMetadataCandidate, ModelMetadataSuggestion } from "./api"

/**
 * 自动填写的取值逻辑。
 *
 * 抽出来做成纯函数，是因为这里的判定全是"什么情况下**不**写"，而那些条件
 * 在界面上看不出来：源没给（null）、用户刚改过、已有值、总开关关着。埋进
 * 组件的 effect 里就只能靠手点验证，而"少了哪一种保护"恰恰是点不出来的
 * ——看起来都是"这次没填上"。
 */

/** 会被自动填写的六个字段，顺序即界面顺序。 */
export const AUTOFILL_FIELDS = [
  "tool_call",
  "structured_output",
  "image",
  "input_price",
  "cache_read_price",
  "output_price",
] as const

export type AutofillField = (typeof AUTOFILL_FIELDS)[number]

/** 三个能力字段：判断"有没有值"看布尔；其余三档价格看是否为 0。 */
const CAPABILITY_FIELDS: readonly AutofillField[] = ["tool_call", "structured_output", "image"]

const PRICE_FIELDS: readonly AutofillField[] = ["input_price", "cache_read_price", "output_price"]

/** 表单里这六个字段当前的样子（外加币种，因为它跟着价格一起改）。 */
export type AutofillSnapshot = Record<AutofillField, boolean | number> & { currency: string }

export type AutofillFill = {
  field: AutofillField
  value: boolean | number
}

/** 没被填的原因。界面上两种说法不同，所以这里不能只记一个"跳过"。 */
export type AutofillSkipReason =
  /** 表单里已经有值了（能力为 true / 价格为非 0），不覆盖 */
  | "existing"
  /** 用户在这次弹窗里手工改过这个字段 */
  | "edited"

export type AutofillResult = {
  /** 要写进表单的字段。只含真正要改的（值没变的不进这里） */
  fill: AutofillFill[]
  /** 源没有提供，因此不动 */
  missing: AutofillField[]
  /** 有值或用户改过，因此不动；以及原因 */
  skipped: { field: AutofillField; reason: AutofillSkipReason }[]
  /**
   * 要一并写进表单的币种；没写任何价格时为 null。
   *
   * 只有确实写了价格才切币种：源的价格是 USD / 每百万 token，把价格按
   * USD 写进去却留着 CNY，会拿美元数字当人民币计价（§3.6）。
   */
  currency: string | null
}

/**
 * 一个字段当前算不算"已经有值"。
 *
 * 能力的 false 与价格的 0 都当作**没配过**，而不是"用户确认了不支持/免费"：
 * 这两条是同一个判断的两面——
 *
 * - 表单新建时六个字段就是 false / 0，那显然不是"有值"，否则自动填写在
 *   新建时一次都不会生效；
 * - 库里 NULL 的能力在路由上等同 false（见 `service/chat.go` 的过滤），价格
 *   的 NULL 则被启动迁移抹成了 0，所以 0 与 false 都与"没配过"不可区分。
 *
 * 反方向（已有 true / 非 0 价）则一律不动，除非策略里打开 overwrite——把
 * 用户确认过的 true 改写成 false 会让这个上游从工具调用的候选池里直接消失，
 * 那正是 Overwrite 默认 false 的理由（§3.5.2）。
 */
function hasValue(field: AutofillField, current: boolean | number): boolean {
  return CAPABILITY_FIELDS.includes(field) ? current === true : current !== 0
}

function valueOf(suggestion: ModelMetadataSuggestion, field: AutofillField): boolean | number | null {
  const raw: boolean | number | null | undefined = suggestion[field]
  return raw ?? null
}

/**
 * 算出这次预填要动哪几格。
 *
 * `trigger` 是唯一区分自动与手动的开关：
 *
 * - `auto` 受三道门控制——策略的 overwrite、字段已有值、用户这次改过；
 * - `manual` 是用户点了「重新填写」（或「采用」某条候选），是他自己的显式
 *   动作，所以不再看 overwrite 与 edited。
 *
 * 两种都**不看**源没给的字段：null 是"不知道"，把它写成 false / 0 等于替
 * 用户断言"这个模型不支持工具调用"。这条没有例外，手动也一样。
 */
export function computeAutofill(args: {
  suggestion: ModelMetadataSuggestion
  current: AutofillSnapshot
  /** 用户在这次弹窗里手工改过的字段 */
  edited: ReadonlySet<AutofillField>
  policy: Pick<ModelAutofillPolicy, "overwrite">
  trigger: "auto" | "manual"
}): AutofillResult {
  const { suggestion, current, edited, policy, trigger } = args
  const result: AutofillResult = { fill: [], missing: [], skipped: [], currency: null }

  for (const field of AUTOFILL_FIELDS) {
    const value = valueOf(suggestion, field)
    if (value === null) {
      result.missing.push(field)
      continue
    }
    if (trigger === "auto") {
      if (edited.has(field)) {
        result.skipped.push({ field, reason: "edited" })
        continue
      }
      if (!policy.overwrite && hasValue(field, current[field])) {
        result.skipped.push({ field, reason: "existing" })
        continue
      }
    }
    // 值没变就不进 fill：它不该出现在"已填写 N 项"里，那会让人以为
    // 刚才动过什么
    if (current[field] !== value) {
      result.fill.push({ field, value })
    }
  }

  if (result.fill.some((f) => PRICE_FIELDS.includes(f.field)) && suggestion.currency) {
    result.currency = suggestion.currency
  }

  return result
}

/**
 * 一次预填在界面上的结论：填了什么、为什么没填。
 *
 * 与 `computeAutofill` 分开，是因为候选「采用」与自动预填共用同一份算出来的
 * 结果，但提示语不同——"已跳过 N 项（已有值）"在用户主动点「采用」之后再说
 * 就成了推诿：那时他明确要求覆盖。
 */
export type AutofillReport = {
  /** 命中的源里那条记录 */
  suggestion: ModelMetadataSuggestion
  result: AutofillResult
  /** 本次是按数据源里的哪个上游查的（对齐可能靠协议兜底，所以要让用户看见） */
  providerLabel: string
  /** 源里那个模型 id，与用户填的可能不同（归一化过的） */
  modelLabel: string
}

/** 未命中时给前端的三种原因，界面上是三句不同的话（§5.2「原因要分开」）。 */
export type AutofillFailureReason =
  | "no_provider_match"
  | "no_model_match"
  | "catalog_unavailable"
  | "deprecated_model"
  | "unknown"

export function classifyFailure(suggestion: ModelMetadataSuggestion): AutofillFailureReason {
  switch (suggestion.reason) {
    case "no_provider_match":
    case "no_model_match":
    case "catalog_unavailable":
    case "deprecated_model":
      return suggestion.reason
    default:
      return "unknown"
  }
}

/** 源里有没有这个模型这件事，只有 matched 才算数。 */
export function isUsable(suggestion: ModelMetadataSuggestion): boolean {
  return suggestion.matched === true
}

/**
 * 把一条跨上游候选当成建议来用。
 *
 * 候选与建议的取值字段是同构的，但**语义不同**：候选是"别家有这个同名模型"，
 * 在用户点「采用」之前不许写入。所以这条换算只该在「采用」那一个地方调用——
 * 让候选有机会走上自动预填那条路，正是 §4.1.1 要防的事。
 */
export function suggestionFromCandidate(candidate: ModelMetadataCandidate): ModelMetadataSuggestion {
  return {
    matched: true,
    source: candidate.source,
    provider: candidate.provider,
    provider_name: candidate.provider_name,
    model: candidate.model,
    tool_call: candidate.tool_call,
    structured_output: candidate.structured_output,
    image: candidate.image,
    input_price: candidate.input_price,
    cache_read_price: candidate.cache_read_price,
    output_price: candidate.output_price,
    currency: candidate.currency,
    context_limit: candidate.context_limit,
    output_limit: candidate.output_limit,
  }
}
