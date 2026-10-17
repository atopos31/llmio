// 结构化请求/响应内容的统一模型。
//
// 这一层刻意不依赖 React，也不接触 i18n 与路由：它只把「某个协议的一串 JSON」
// 变成一份与协议无关的结构，供 log-chat 页渲染。协议差异全部收敛在
// 各协议的 toDeltas / parseRequest 实现里，装配器只有这一个。

/** 协议风格标识，取值与后端 chat_io.style 字段一致。 */
export type Protocol = "openai" | "openai-res" | "anthropic" | "gemini";

export const KNOWN_PROTOCOLS: readonly Protocol[] = ["openai", "openai-res", "anthropic", "gemini"] as const;

/**
 * 响应侧的一个归一化增量。
 *
 * 四种协议被压平成同一个形状，供唯一的装配器 applyChunk 消费：
 * - kind 决定落到哪个桶（正文 / 思考 / 工具调用 / 工具结果 / 无法识别）；
 * - id 是"同一工具调用的哪个分片"（流式里按 index 累积，非流式里用 call id）；
 * - finish / usage 是非文本的信令，装配器只做最后写入（后到覆盖）。
 */
export interface Delta {
  kind: "text" | "thinking" | "tool_call" | "tool_result" | "unknown";
  /** 文本内容。null 表示"取过但没有"，与 undefined 的"没取"区分开。 */
  text?: string | null;
  id?: string;
  index?: number;
  name?: string;
  callId?: string;
  data?: unknown;
  finish?: string;
  usage?: unknown;
  /** 媒体类未知块的 MIME 类型（inlineData / image block）。 */
  mimeType?: string | null;
  /**
   * 工具参数分片。约定（见 assemble.accumulate）：字符串片段直接相接；
   * 非字符串整体替换；空值不覆盖已有内容。这样流式分片与非流式整包共用
   * 同一段装配代码，而 Gemini / Anthropic 那种"一次给完整对象"的写法不会
   * 被拆散。
   */
  arguments?: unknown;
  /**
   * true 表示这是一个"最终"的工具调用增量（Responses 的 output_item.added
   * 带完整 arguments、Anthropic 的 content_block_start 带完整 id/name），
   * 装配器要整体替换而不是分片相接。
   */
  final?: boolean;
}

/** 一条增量里没能识别的子项（未知块类型 / 未知事件类型）原样保留。 */
export interface UnknownPart {
  type: "unknown";
  data?: unknown;
  text?: string | null;
  name?: string;
  index?: number;
  mimeType?: string | null;
}

export interface ToolCall {
  /** 协议给的调用 id（OpenAI `tool_calls[].id`、Anthropic `tool_use.id`）。 */
  id?: string;
  /** 流式里区分同一段响应中多个并发工具调用的下标。 */
  index?: number;
  name: string;
  /** 原始参数：OpenAI 是 JSON 字符串，Anthropic / Gemini 已经是对象。 */
  arguments: unknown;
  /** 参数是字符串但 JSON 解析失败（流式尾部的半截片段常见）。 */
  parseError?: boolean;
}

export type ContentPart =
  | { type: "text"; text: string }
  | { type: "thinking"; text: string }
  | { type: "image"; label?: string; url?: string; data?: string | null; mimeType?: string | null }
  | { type: "audio"; label?: string; data?: string | null; mimeType?: string | null }
  | { type: "tool_use"; call: ToolCall }
  | { type: "tool_result"; callId: string; content: unknown; toolCall?: string }
  | UnknownPart;

export interface Message {
  role: string;
  parts: ContentPart[];
  /** 便捷视图：本条消息里的工具调用（渲染与 tool_result 回填都从这里读）。 */
  toolCalls: ToolCall[];
}

export interface ToolDeclaration {
  name: string;
  description?: string;
  parameters?: unknown;
}

export interface RequestParseError {
  protocol: Protocol;
  message: string;
}

export interface ParsedRequest {
  protocol: Protocol;
  /** 请求体里写的模型名。注意：这是"请求名 / llmio 别名"，不是上游模型 id。 */
  model?: string;
  system?: unknown;
  messages: Message[];
  tools: ToolDeclaration[];
  params: Record<string, unknown>;
  errors: RequestParseError[];
  /** 请求体不是 JSON 时的原始字符串。 */
  raw?: string;
}

export interface ParsedResponse {
  protocol: Protocol;
  stream: boolean;
  model?: string;
  /** 正文（不含思考与工具片段），按出现顺序拼接。 */
  text: string;
  /**
   * 思考内容，一次响应一整段。
   *
   * 不是 `string[]`：流式里思考是以几十上百个 delta 到达的（Anthropic 的
   * thinking_delta、OpenAI 的 reasoning_content、Responses 的
   * reasoning_summary_text.delta），逐 delta 存一项就是"按 SSE 展示"——
   * 读者看到的是几百张各写一两个字的卡片。分片是线路格式的产物，不是思考
   * 的边界，所以在装配器里就拼起来。
   *
   * 真要有多个思考块的协议（Anthropic 非流式的 thinking + redacted_thinking）
   * 在上游就已经都归一成 kind="thinking" 了，边界信息在到达装配器之前就丢了，
   * 这里保留 `string[]` 也换不回它。
   */
  thinking: string;
  toolCalls: ToolCall[];
  toolResults: Array<{ callId: string; content: unknown; toolCall?: string }>;
  usage?: unknown;
  finishReason?: string;
  /** 逐条原始片段：JSON 解析成功的是对象，失败的是原字符串。 */
  chunks: unknown[];
  /** 解析 JSON 失败、或形状完全无法归一的片段数（界面上显式告知，不静默丢）。 */
  malformed: number;
  /** 落进 unknown 块的子项数（未知块类型 / 未知事件类型）。 */
  unknown: number;
}

/** 原始片段：JSON 解析成功的是对象，失败的是原字符串（原样回显）。 */
export type RawChunk = Record<string, unknown> | string;

export interface ChatInput {
  Input?: string | null;
  OfString?: string | null;
  OfStringArray?: string[] | null;
  Style?: unknown;
}
