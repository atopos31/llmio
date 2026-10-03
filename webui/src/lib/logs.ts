/**
 * 日志页的纯逻辑：多选筛选在 URL 与查询串之间的往返。
 *
 * ## 为什么这些事值得单独一个模块
 *
 * 筛选状态存在 URL 里（这一页的既有约定：可分享、可后退）。多选之后，
 * 一个问题从"点一下下拉"变成了三个必须一致的口径——
 * URL 里怎么编码、请求里怎么编码、以及"没有选"和"选了一个叫 all 的值"
 * 怎么区分。这三处只要有一处不一致，页面照样渲染，只是筛出来的东西不对，
 * 而"列表少了几行"是没人会注意到的那种错。因此这些都放在这里，逐条测。
 *
 * 后端的口径：`handler/api.go` 的 GetRequestLogs 对这五个维度做
 * `splitCSV` + `IN`，也就是说**逗号就是多值的编码**，不需要重复参数。
 */

/** 可以多选的筛选维度。键名与 URL 参数名一致，也和后端的查询参数名一致。 */
export const LOGS_MULTI_KEYS = ["providerName", "model", "status", "style", "authKey"] as const

export type LogsMultiKey = (typeof LOGS_MULTI_KEYS)[number]

/** 全部维度都为空的一份筛选值。 */
export type LogsMultiFilter = Record<LogsMultiKey, string[]>

/**
 * 单值时代的哨兵值。旧链接里 `status=all`、`model=all` 表示"不过滤"，
 * 新版的"不过滤"是**参数缺席**。留着它是为了不让一条旧书签变成
 * "筛选 status ∈ {all}" —— 那会得到一个空表，而且看不出哪里不对。
 */
const LEGACY_ALL = "all"

/**
 * 查询串里的一个取值 → 多选值列表。
 *
 * 去空白、丢空项、按出现顺序去重（同一维度里出现两次是用户或链接的问题，
 * 但重复传下去会让后端白白多比一次，也会让触发器上的计数虚高）。
 */
export function parseMulti(raw: string | null | undefined): string[] {
  const parts = (raw ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s !== "")
  // 只有"整个参数恰好等于 all"才算旧的哨兵值；`all,foo` 是按字面意思筛这两项
  if (parts.length === 1 && parts[0] === LEGACY_ALL) return []
  return [...new Set(parts)]
}

/** 多选值列表 → 查询串里的一个取值。空列表得到空串，调用方据此删掉该参数。 */
export function joinMulti(values: string[]): string {
  return values.join(",")
}

/** 从查询参数里读出五个多选维度。 */
export function readMultiFilter(get: (key: string) => string | null): LogsMultiFilter {
  const out = {} as LogsMultiFilter
  for (const key of LOGS_MULTI_KEYS) {
    out[key] = parseMulti(get(key))
  }
  return out
}

/** 已选中的筛选项总数（用于决定是否显示"清空筛选"）。 */
export function activeMultiCount(filter: LogsMultiFilter): number {
  return LOGS_MULTI_KEYS.reduce((n, key) => n + filter[key].length, 0)
}

/** 在一个维度上切换一个取值。 */
export { toggleValue as toggleMulti } from "@/lib/utils"
