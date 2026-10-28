import { forwardProtocol, type ForwardProtocol } from "./forwarding-utils"

/**
 * 专属地址上"这个地址现在能用哪些端点"的唯一数据源。
 *
 * 页面与设置弹窗都要回答同一个问题，而答案取决于两件事：地址协议（路径前缀
 * 不同）和转发模式（有些端点没有本地实现，必须转发给本提供商）。把它们写成
 * 两份各自维护的清单，迟早会出现"页面上说能用、真正请求却 404"的矛盾，所以
 * 这里只留一份数据，展示层各自渲染。
 *
 * handling 是后端处理方式的直译（见 handler/provider_route.go）：
 * - pool：本地端点，请求在全部提供商之间负载均衡；
 * - global：本地端点，但走系统级配置而不是模型绑定（目前只有 token 计数）；
 * - forward：本地没有实现，只有转发模式为混合或纯转发时才转发给本提供商。
 */
export type EndpointHandling = "pool" | "global" | "forward"

/** 需要额外解释的端点；键对应 i18n 的 endpoint_note.*。 */
export type EndpointNoteKey = "models_anthropic" | "bridge" | "count_tokens" | "gemini_native"

export interface ForwardEndpoint {
  method: "GET" | "POST"
  /** 相对专属地址根（origin + /p/ + 标识）的路径；{model} 是占位符。 */
  path: string
  handling: EndpointHandling
  noteKey?: EndpointNoteKey
}

/**
 * /v1 下的本地端点：与地址协议无关，任何专属地址都接管。后端在查表前不看
 * 协议，所以 Anthropic 型地址上这几条同样可用。
 */
const V1_ENDPOINTS: readonly ForwardEndpoint[] = [
  { method: "POST", path: "/v1/chat/completions", handling: "pool" },
  { method: "POST", path: "/v1/responses", handling: "pool", noteKey: "bridge" },
  { method: "POST", path: "/v1/messages", handling: "pool", noteKey: "bridge" },
  {
    method: "POST",
    path: "/v1/messages/count_tokens",
    handling: "global",
    noteKey: "count_tokens",
  },
  { method: "GET", path: "/v1/models", handling: "pool", noteKey: "models_anthropic" },
]

/**
 * 只有 Gemini 型地址才有的本地端点：模型列表与两种原生对话方法。它们以前在
 * 关闭模式下是 404、在混合模式下是透传，现在统一走本地流水线。
 */
const GEMINI_NATIVE_ENDPOINTS: readonly ForwardEndpoint[] = [
  { method: "GET", path: "/v1beta/models", handling: "pool", noteKey: "gemini_native" },
  {
    method: "POST",
    path: "/v1beta/models/{model}:generateContent",
    handling: "pool",
    noteKey: "gemini_native",
  },
  {
    method: "POST",
    path: "/v1beta/models/{model}:streamGenerateContent",
    handling: "pool",
    noteKey: "gemini_native",
  },
]

/** 需要转发给本提供商才能用的端点：关闭模式下它们返回 404。 */
const FORWARD_ENDPOINTS: Record<ForwardProtocol, readonly ForwardEndpoint[]> = {
  openai: [
    { method: "POST", path: "/v1/embeddings", handling: "forward" },
    { method: "POST", path: "/v1/completions", handling: "forward" },
    { method: "POST", path: "/v1/moderations", handling: "forward" },
    { method: "POST", path: "/v1/rerank", handling: "forward" },
    { method: "POST", path: "/v1/images/generations", handling: "forward" },
    { method: "POST", path: "/v1/audio/transcriptions", handling: "forward" },
  ],
  // Anthropic 型地址的基地址不带 /v1，但转发仍然按路径走，所以路径与 OpenAI 相同。
  anthropic: [
    { method: "POST", path: "/v1/embeddings", handling: "forward" },
    { method: "POST", path: "/v1/completions", handling: "forward" },
    { method: "POST", path: "/v1/moderations", handling: "forward" },
    { method: "POST", path: "/v1/rerank", handling: "forward" },
    { method: "POST", path: "/v1/images/generations", handling: "forward" },
    { method: "POST", path: "/v1/audio/transcriptions", handling: "forward" },
  ],
  gemini: [
    { method: "POST", path: "/v1beta/models/{model}:embedContent", handling: "forward" },
    { method: "POST", path: "/v1beta/models/{model}:batchEmbedContents", handling: "forward" },
    { method: "POST", path: "/v1beta/models/{model}:countTokens", handling: "forward" },
  ],
}

/** 按地址协议列出该地址上的端点：本地端点在前，需开启转发的在后。 */
export function forwardingEndpoints(type: string): ForwardEndpoint[] {
  const protocol = forwardProtocol(type)
  const local = protocol === "gemini" ? [...GEMINI_NATIVE_ENDPOINTS, ...V1_ENDPOINTS] : [...V1_ENDPOINTS]
  return [...local, ...FORWARD_ENDPOINTS[protocol]]
}

/**
 * 端点完整地址。没有标识时返回空数组——绝不拼出 /p//v1 这种看起来能用、
 * 粘进去必然 404 的假地址。
 */
export function forwardEndpointUrls(origin: string, slug: string, type: string): string[] {
  if (!slug) return []
  const root = `${origin.replace(/\/+$/, "")}/p/${slug}`
  return forwardingEndpoints(type).map((endpoint) => `${root}${endpoint.path}`)
}
