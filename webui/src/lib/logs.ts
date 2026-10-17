import type { ChatLog } from "@/lib/api"

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

/**
 * 可以多选的筛选维度。键名与 URL 参数名一致，和后端查询参数的**语义**一致：
 *
 * - `providerModel` → `provider_model`，上游模型名（真正发给提供商的模型 id）
 * - `requestName`   → `name`，请求名（客户端请求体里写的名字 = llmio 别名）
 *
 * 两个名字以前混成一个，于是「按模型下钻」用的是请求名、而分组显示的是上游
 * 模型名，两边对不上——点分组名筛出来是空表。
 */
export const LOGS_MULTI_KEYS = [
  "providerName",
  "requestName",
  "providerModel",
  "status",
  "style",
  "authKey",
] as const

export type LogsMultiKey = (typeof LOGS_MULTI_KEYS)[number]

/** 全部维度都为空的一份筛选值。 */
export type LogsMultiFilter = Record<LogsMultiKey, string[]>

/**
 * 只读的历史键名：读到就按新语义解释，写回时改用规范键名。
 *
 * 旧链接的 `model` 装的是请求名（后端 `name`）。它是**只读别名**——新版永不
 * 再写它，所以不会和新版的同名参数相撞；而一旦用户在界面上碰过一次请求名
 * 筛选，就该写成 `requestName` 并把残留的 `model` 删掉，否则同两个语义会在
 * URL 上各占一个键，刷新后谁先谁后就得猜。
 */
export const LEGACY_MULTI_KEYS = ["model"] as const

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

/**
 * 读一个多选维度当前的取值。规范键名优先；`requestName` 还有一个只读别名
 * （旧链接的 `model`），只在 `requestName` 缺席时兜底——两个键都在时以规范
 * 键为准，否则一次界面操作就会把旧值又带回来。
 *
 * 这个函数读写共用：写入前先取这里算出来的值再去勾选，旧链接上的已选项
 * 才不会被一次点选悄悄丢掉。
 */
export function readMultiValue(get: (key: string) => string | null, key: LogsMultiKey): string[] {
  if (key === "requestName") {
    const current = parseMulti(get("requestName"))
    return current.length > 0 ? current : parseMulti(get("model"))
  }
  return parseMulti(get(key))
}

/** 从查询参数里读出全部多选维度（含只读别名的兜底）。 */
export function readMultiFilter(get: (key: string) => string | null): LogsMultiFilter {
  const out = {} as LogsMultiFilter
  for (const key of LOGS_MULTI_KEYS) {
    out[key] = readMultiValue(get, key)
  }
  return out
}

/**
 * 把一次多选勾选写进 URL 参数：非空就写规范键名，空就删掉参数，同时清掉
 * 所有只读别名并回到第 1 页。
 *
 * 勾选前必须先用 `readMultiValue` 取当前值（它认别名），再在这里回写规范键名：
 * 这样旧书签上的已选项会被**迁移**到新键名，而不是被一次点选静默丢掉。
 */
export function applyMultiFilter(params: URLSearchParams, key: LogsMultiKey, values: string[]): URLSearchParams {
  const csv = joinMulti(values)
  if (csv) params.set(key, csv)
  else params.delete(key)
  for (const legacy of LEGACY_MULTI_KEYS) params.delete(legacy)
  params.delete("page")
  return params
}

/** 已选中的筛选项总数（用于决定是否显示"清空筛选"）。 */
export function activeMultiCount(filter: LogsMultiFilter): number {
  return LOGS_MULTI_KEYS.reduce((n, key) => n + filter[key].length, 0)
}

/**
 * 这一条日志有没有可看的请求内容。
 *
 * 判据是后端写入的口径而不是前端猜测：ioLog 开关打开时，成功路径落
 * ChatIO（请求体 + 响应体），失败路径也落 ChatIO（仅请求体，无响应）。
 * 因此"这行有 ChatIO 文件"且状态为 success 或 error 时才有内容可读。
 * 详情抽屉与日志列表共用这一条判据——各写一份的话，很快就会出现
 * "按钮可点但页面空白"或反过来的不一致。
 */
export function canViewChatIO(log: ChatLog): boolean {
  return !!log.ChatIO && (log.Status === "success" || log.Status === "error")
}

/** 在一个维度上切换一个取值。 */
export { toggleValue as toggleMulti } from "@/lib/utils"
