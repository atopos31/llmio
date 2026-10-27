import type { ForwardModeSetting, ForwardRewriteSetting, Provider } from "@/lib/api"

/**
 * 专属地址的拼接规则由**协议**决定，而不是由提供商名称或固定 /v1 决定。
 *
 * 这里说的"地址"是客户端配置的 base_url：客户端会自己往后拼具体路径，
 * 所以三种协议需要的基地址不同——
 * - OpenAI 兼容：客户端拼 /chat/completions，基地址要带 /v1；
 * - Anthropic：SDK 自己拼 /v1/messages，基地址**不能**再带 /v1；
 * - Gemini：客户端拼 /models/{model}:generateContent，基地址要带 /v1beta。
 *
 * 判断错协议，界面会给出一个看起来合理、粘进客户端却必然 404 的地址，
 * 而且用户很难从地址文本上看出错在哪里——因此这段映射是纯逻辑并有单测。
 */
export type ForwardProtocol = "openai" | "anthropic" | "gemini"

/** 协议归类。未知/空类型按 OpenAI 兼容处理（与服务端转发假设一致）。 */
export function forwardProtocol(type: string): ForwardProtocol {
  const normalized = type.trim().toLowerCase()
  if (normalized === "anthropic") return "anthropic"
  if (normalized === "gemini") return "gemini"
  return "openai"
}

/** 后端 ForwardMode（null/false/true）→ 设置弹窗的三态。 */
export function forwardProviderMode(provider: Pick<Provider, "ForwardMode">): ForwardModeSetting {
  if (provider.ForwardMode === true) return "pure"
  if (provider.ForwardMode === false) return "mixed"
  return "off"
}

/**
 * 后端 ForwardRewrite → 设置弹窗三态。
 *
 * 空串表示升级前的旧行：那时的语义是"认识且绑定过才改写"，等价于 known，
 * 因此这里把空串与未知值都归一成 known，而不是当成 none。接收 string 而不是
 * api.ts 的三态 union，是因为这里是"读后端原始字段"的边界：旧库里真有空串，
 * 用 union 只会逼调用方写 as。
 */
export function forwardProviderRewrite(provider: { ForwardRewrite: string }): ForwardRewriteSetting {
  if (provider.ForwardRewrite === "none" || provider.ForwardRewrite === "all") {
    return provider.ForwardRewrite
  }
  return "known"
}

/** 与服务端 common.IsSafeForwardSlug 同一规则：1-64 位，首字符字母/数字，其余字母数字与 - _ .。 */
export function isValidForwardSlug(slug: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(slug)
}

/**
 * 转发关闭（mode=off）时，专属地址上只有这几条已知端点会被接管，其余路径
 * 一律 404（见 handler/provider_route.go 的 switch）。界面上必须把它们明写
 * 出来：用户最容易默认"地址能用"，然后拿 Gemini 原生路径或 /v1/embeddings
 * 去试，得到的 404 看起来像配错了地址。
 */
export const FORWARD_OFF_KNOWN_PATHS = [
  "/v1/chat/completions",
  "/v1/responses",
  "/v1/messages",
  "/v1/messages/count_tokens",
  "/v1/models",
] as const

/**
 * 关闭转发模式下确实可用的完整地址（协议无关：已知端点都挂在 /v1 下，
 * 与提供商类型拼出的 base_url 后缀无关）。没有地址标识时返回空数组。
 */
export function forwardOffKnownUrls(origin: string, slug: string): string[] {
  if (!slug) return []
  const root = `${origin.replace(/\/+$/, "")}/p/${slug}`
  return FORWARD_OFF_KNOWN_PATHS.map((path) => `${root}${path}`)
}

/** 由站点 origin、提供商类型与地址标识拼出专属地址（即客户端要填的 base_url）。 */
export function forwardBaseUrl(origin: string, type: string, slug: string): string {
  const base = origin.replace(/\/+$/, "")
  const root = `${base}/p/${slug}`
  switch (forwardProtocol(type)) {
    case "anthropic":
      return root
    case "gemini":
      return `${root}/v1beta`
    default:
      return `${root}/v1`
  }
}
