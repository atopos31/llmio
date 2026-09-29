/**
 * 请求对比与提示缓存前缀分析。
 *
 * ## 为什么顺序是 tools → system → messages
 *
 * 提示缓存是**前缀匹配**：前缀里任何一个字节变化，其后全部失效。API 渲染
 * 请求的顺序是 `tools` → `system` → `messages`，因此这三段里靠前的部分才
 * 是"公共前缀"的真正贡献者。
 *
 * 这一点直接决定本文件的算法：只比较 messages 是不够的。工具定义通常
 * 在最前面、体量最大，两个请求共享同一份 tools 时，即使消息完全不同，
 * tools + system 那一段依然是命中的。忽略它会把"前缀很长"误报成"前缀 0"，
 * 从而把缓存命中的原因判断错。
 *
 * 参考：Claude 提示缓存的渲染顺序为 tools → system → messages。
 *
 * ## 比较是"字节忠实"的
 *
 * 规范化只做一件事：`JSON.parse` 后重新序列化，以消除缩进/换行这类格式噪音。
 * **不排序键**——因为上游缓存看到的也是原始键序，键序不同确实会导致未命中。
 * 若在此处排序，就会把"其实没命中"报成"命中"，那是主动误导。
 */

/** 请求体里参与前缀比较的一段。 */
export type PromptEntry = {
  /** 在请求体中的位置。顺序即 tools → system → messages。 */
  kind: "tool" | "system" | "message"
  /** 展示用标签：工具名，或消息角色。 */
  label: string
  /** 参与比较的规范化文本。 */
  text: string
}

/** 一次请求的提示结构。顺序即缓存前缀的构造顺序。 */
export type PromptStructure = {
  tools: PromptEntry[]
  system: PromptEntry | null
  messages: PromptEntry[]
}

/** 参与分析的消息数上限。避免超长会话把比较变成不可接受的规模。 */
export const MAX_MESSAGES = 5000

/** 参与分析的工具数上限。 */
export const MAX_TOOLS = 256

const EMPTY: PromptStructure = { tools: [], system: null, messages: [] }

/**
 * 把三个段落按缓存前缀顺序拼成一条条目序列。
 * 顺序错误会让前缀长度完全失真，因此只在此处定义，不在调用方重复。
 */
export function toEntries(s: PromptStructure): PromptEntry[] {
  return [...s.tools, ...(s.system ? [s.system] : []), ...s.messages]
}

/**
 * 解析请求体，抽出缓存前缀的三个段落。
 *
 * 兼容多种协议的字段差异：
 *   - tools：OpenAI 的 `tools[].function`、Anthropic 的 `tools[]`、
 *     Gemini 的 `tools[].functionDeclarations[]`
 *   - system：Anthropic/OpenAI 的 `system`、OpenAI-Responses 的 `instructions`、
 *     Gemini 的 `systemInstruction`
 *   - messages：`messages[]`（OpenAI/Anthropic）或 `contents[]`（Gemini）
 *
 * 解析失败或形状不认识时返回空结构而不是抛错：对比页应当在"这段看不懂"
 * 的情况下仍展示其余可比信息，而不是整页失败。
 */
export function extractStructure(input: string, max = MAX_MESSAGES): PromptStructure {
  let body: unknown
  try {
    body = JSON.parse(input)
  } catch {
    return EMPTY
  }
  if (!isRecord(body)) return EMPTY

  const tools = extractTools(body.tools)
  const system = extractSystem(body)

  const out: PromptEntry[] = []
  const list = Array.isArray(body.messages)
    ? body.messages
    : Array.isArray(body.contents)
      ? body.contents
      : null

  if (list) {
    for (const raw of list) {
      if (out.length >= max) break
      if (!isRecord(raw)) continue
      const role = typeof raw.role === "string" ? raw.role : "unknown"
      const content = flattenContent(raw.content ?? raw.parts)
      out.push({ kind: "message", label: role, text: `${role}\u0000${content}` })
    }
  }

  return { tools, system, messages: out }
}

/**
 * 抽取工具定义。
 *
 * Gemini 把多个函数放在一个 `functionDeclarations` 数组里，展开成独立条目——
 * 它们在前缀里的地位与单个工具相同，合并成一个条目会掩盖"第 2 个工具变了"。
 */
function extractTools(raw: unknown): PromptEntry[] {
  if (!Array.isArray(raw)) return []
  const out: PromptEntry[] = []

  for (const t of raw) {
    if (out.length >= MAX_TOOLS) break
    if (!isRecord(t)) continue

    if (Array.isArray(t.functionDeclarations)) {
      for (const fn of t.functionDeclarations) {
        if (out.length >= MAX_TOOLS) break
        if (!isRecord(fn)) continue
        const name = typeof fn.name === "string" ? fn.name : "function"
        out.push({ kind: "tool", label: name, text: stableText(fn) })
      }
      continue
    }

    // OpenAI 包一层 function；Anthropic 直接是 name/input_schema
    const inner = isRecord(t.function) ? t.function : t
    const name =
      typeof inner.name === "string" ? inner.name : typeof t.type === "string" ? t.type : "tool"
    out.push({ kind: "tool", label: name, text: stableText(t) })
  }

  return out
}

/** 抽取系统提示。三个来源按优先级取第一个非空的。 */
function extractSystem(body: Record<string, unknown>): PromptEntry | null {
  const candidates: unknown[] = [body.system, body.systemInstruction, body.instructions]
  for (const c of candidates) {
    const text = systemText(c)
    if (text !== "") {
      return { kind: "system", label: "system", text: `system\u0000${text}` }
    }
  }
  return null
}

/** system 可能是字符串、块数组，或 Gemini 的 { parts: [...] }。 */
function systemText(v: unknown): string {
  if (typeof v === "string") return v.trim() === "" ? "" : v
  if (Array.isArray(v)) return v.length > 0 ? flattenContent(v) : ""
  if (isRecord(v) && Array.isArray(v.parts)) return v.parts.length > 0 ? flattenContent(v.parts) : ""
  return ""
}

/** 把 Anthropic 的 content 块数组或 Gemini 的 parts 数组压成文本。 */
function flattenContent(content: unknown): string {
  if (typeof content === "string") return content
  if (!Array.isArray(content)) {
    return content === undefined || content === null ? "" : JSON.stringify(content)
  }
  return content
    .map((part) => {
      if (typeof part === "string") return part
      if (!isRecord(part)) return ""
      if (typeof part.text === "string") return part.text
      // 图片等非文本块保留类型标记，让"这里差了一张图"可见而不是静默忽略
      if (typeof part.type === "string") return `<${part.type}>`
      return ""
    })
    .join("\n")
}

/**
 * 规范化文本：解析后重新序列化，消除缩进/换行噪音。
 *
 * 刻意**不排序键**：上游缓存看到的也是原始键序，键序变化确实会导致未命中。
 * 在此排序会把真实的未命中报成命中，是主动误导。
 */
function stableText(v: unknown): string {
  try {
    return JSON.stringify(v) ?? ""
  } catch {
    return String(v)
  }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

/**
 * 多份请求的公共前缀长度（按条目计）。
 *
 * 前缀长度只在**条目边界**上有意义：缓存也按块（工具 / 系统提示 / 消息）生效，
 * "前 137 个字符相同"这类字符级结论无法对应到任何真实的缓存单元。
 */
export function commonPrefixEntries(structures: PromptStructure[]): number {
  const lists = structures.map(toEntries).filter((l) => l.length > 0)
  if (lists.length === 0) return 0

  const shortest = Math.min(...lists.map((l) => l.length))
  let n = 0
  while (n < shortest) {
    const sig = lists[0][n].text
    if (!lists.every((l) => l[n].text === sig)) break
    n++
  }
  return n
}

/**
 * 某个请求相对公共前缀的分叉点。
 *
 * 返回三段式描述，直接对应"缓存从哪里开始失效"这个问题：
 *   prefixLen = 3，请求有 5 个条目 → 前 3 个（例如 tools + system）命中，
 *   从第 4 个（第一条消息）起失效。
 */
export type Divergence = {
  /** 公共前缀中包含的条目数 */
  prefixLen: number
  /** 该请求从第几个条目起偏离公共前缀；null 表示它完全包含公共前缀 */
  divergesAt: number | null
  /** 偏离处的条目；null 表示没有偏离 */
  entry: PromptEntry | null
  /** 该请求的条目总数 */
  total: number
}

export function divergenceOf(structure: PromptStructure, prefixLen: number): Divergence {
  const entries = toEntries(structure)
  if (entries.length <= prefixLen) {
    return { prefixLen, divergesAt: null, entry: null, total: entries.length }
  }
  return { prefixLen, divergesAt: prefixLen, entry: entries[prefixLen], total: entries.length }
}

/** 把条目序列按 kind 归纳成一句人读的说明，用于"前 N 项命中的是什么"。 */
export function describePrefix(entries: PromptEntry[]): { tools: number; system: number; messages: number } {
  const out = { tools: 0, system: 0, messages: 0 }
  for (const e of entries) {
    if (e.kind === "tool") out.tools++
    else if (e.kind === "system") out.system++
    else out.messages++
  }
  return out
}

/**
 * 缓存命中率（%）。输入为 0 时返回 null，表示"不适用"而非 0%——
 * 把不适用显示成 0% 会让人以为缓存完全没用上。
 */
export function cacheHitRate(promptTokens: number, cachedTokens: number): number | null {
  if (!Number.isFinite(promptTokens) || promptTokens <= 0) return null
  return (cachedTokens / promptTokens) * 100
}
