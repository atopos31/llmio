import { applyChunk, attachToolResultNames, emptyAssembled } from "./assemble";
import { anthropicNonStreamDeltas, anthropicParseRequest, anthropicToDeltas } from "./anthropic";
import { geminiCandidateDeltas, geminiNonStreamDeltas, geminiParseRequest } from "./gemini";
import { openaiParseRequest, openaiToDeltas } from "./openai";
import { openaiResNonStreamDeltas, openaiResParseRequest, openaiResToDeltas } from "./openai-res";
import {
  KNOWN_PROTOCOLS,
  type ChatInput,
  type Delta,
  type Message,
  type ParsedRequest,
  type ParsedResponse,
  type Protocol,
} from "./types";
import { isObj, str, toChunk } from "./util";

/**
 * 协议嗅探。
 *
 * Style 缺失或被上游写成别的值时不能直接退化成"原文展示"——先按响应体形状
 * 判断（choices / content 数组 / candidates），再按请求体形状退而求其次，
 * 都判不出来才真的按原始文本处理。判错一整个面板都会变成不可读。
 */
export function sniffProtocol(body: Record<string, unknown>): Protocol | null {
  if (Array.isArray(body.choices)) return "openai";
  if (Array.isArray(body.output)) return "openai-res";
  if (Array.isArray(body.candidates)) return "gemini";
  if (Array.isArray(body.content)) return "anthropic";
  // OpenAI 与 Anthropic 都有 messages；OpenAI 一定带 model，Anthropic 一定带
  // system。先按这两个特征区分，再按 messages 兜底成 anthropic。
  if (body.model !== undefined && Array.isArray(body.messages)) return "openai";
  if (body.system !== undefined) return "anthropic";
  if (Array.isArray(body.messages)) return "anthropic";
  if (Array.isArray(body.input) || body.instructions !== undefined) return "openai-res";
  if (Array.isArray(body.contents) || body.systemInstruction !== undefined) return "gemini";
  // 一条特征都认不出来：这不是聊天协议（典型是透传的 embeddings / rerank /
  // 图片接口）。调用方要靠这个 null 决定"按通用 JSON 展示"。
  return null;
}

export function detectProtocol(body: Record<string, unknown>): Protocol {
  return sniffProtocol(body) ?? "openai";
}

function isKnown(style: unknown): style is Protocol {
  return typeof style === "string" && (KNOWN_PROTOCOLS as readonly string[]).includes(style);
}

/** 模型名在四种协议里位置不同：OpenAI 顶层，Responses 在 response 里，其余各一处。 */
function modelOf(chunk: Record<string, unknown>): string | undefined {
  for (const key of ["model", "modelVersion", "model_version"]) {
    const v = str(chunk[key]);
    if (v) return v;
  }
  for (const key of ["response", "message"]) {
    const nested = isObj(chunk[key]) ? (chunk[key] as Record<string, unknown>) : undefined;
    const v = str(nested?.model);
    if (v) return v;
  }
  return undefined;
}

/**
 * 把一条原始片段归一成 Delta[]。流式与非流式都走这里：非流式整包被拆成增量
 * 之后，下游只剩 applyChunk 一套逻辑，两条路径不会出现行为分叉。
 */
function toDeltas(chunk: Record<string, unknown>, protocol: Protocol, whole: boolean): Delta[] {
  switch (protocol) {
    case "openai": {
      if (!whole) return openaiToDeltas(chunk);
      // 非流式的 message 直接包成 delta，让流式/非流式共用同一段装配代码。
      const choices = Array.isArray(chunk.choices) ? (chunk.choices as Array<Record<string, unknown>>) : [];
      return openaiToDeltas({
        choices: choices.map((c) => ({ index: c.index, finish_reason: c.finish_reason, delta: c.message })),
      });
    }
    case "openai-res":
      return whole ? openaiResNonStreamDeltas(chunk) : openaiResToDeltas(chunk);
    case "anthropic":
      return whole ? anthropicNonStreamDeltas(chunk) : anthropicToDeltas(chunk);
    case "gemini":
      return whole ? geminiNonStreamDeltas(chunk) : geminiCandidateDeltas(chunk);
  }
}

export function parseRequest(io: ChatInput, style: Protocol | null | undefined): ParsedRequest {
  const protocol: Protocol = isKnown(style) ? style : "openai";
  const raw = io.Input;

  if (typeof raw !== "string" || raw.length === 0) {
    return { protocol, messages: [], tools: [], params: {}, errors: [] };
  }
  const parsed = toChunk(raw);
  if (typeof parsed === "string" || parsed === null) {
    return { protocol, messages: [], tools: [], params: {}, errors: [{ protocol, message: "请求体不是合法的 JSON 对象" }], raw };
  }

  const actual = isKnown(style) ? style : detectProtocol(parsed);
  const body =
    actual === "openai"
      ? openaiParseRequest(parsed)
      : actual === "openai-res"
        ? openaiResParseRequest(parsed)
        : actual === "anthropic"
          ? anthropicParseRequest(parsed)
          : geminiParseRequest(parsed);

  return body;
}

export function parseResponse(io: ChatInput, style: Protocol | null | undefined): ParsedResponse {
  const stream = Array.isArray(io.OfStringArray) && io.OfStringArray.length > 0;
  const rawChunks: string[] = stream
    ? io.OfStringArray!.filter((c): c is string => typeof c === "string" && c.length > 0)
    : typeof io.OfString === "string" && io.OfString.length > 0
      ? [io.OfString!]
      : [];

  // Style 缺失时按响应体形状嗅探协议；一条都认不出来说明不是聊天协议，
  // 这里仍回落到 openai 保持既有解析行为（页面另用 sniffResponseProtocol
  // 判断是否整体改成"通用 JSON"展示）。
  const protocol: Protocol = sniffResponseProtocol(io, style) ?? "openai";

  const result: ParsedResponse = {
    protocol,
    stream,
    text: "",
    thinking: "",
    toolCalls: [],
    toolResults: [],
    chunks: [],
    malformed: 0,
    unknown: 0,
  };
  if (rawChunks.length === 0) return result;

  const out = emptyAssembled();
  let model: string | undefined;
  const whole = !stream;

  for (const rawChunk of rawChunks) {
    const chunk = toChunk(rawChunk);
    if (chunk === null) {
      // toChunk 返回 null 有三种情况：JSON 真的坏掉、纯空白、或 JSON 合法但不是
      // 对象（纯数字、数组等）。只有第一种算"没能解析"——空白和纯标量是协议外的
      // 噪声，直接忽略，不占 malformed 名额也不进 chunks。
      const trimmed = rawChunk.trim();
      if (trimmed.length === 0) continue;
      try {
        JSON.parse(rawChunk);
        // 合法 JSON，不是对象——直接忽略。
      } catch {
        result.malformed += 1;
        result.chunks.push(rawChunk);
      }
      continue;
    }
    result.chunks.push(chunk);
    if (!model) model = typeof chunk === "string" ? undefined : modelOf(chunk);

    for (const d of toDeltas(typeof chunk === "string" ? {} : chunk, protocol, whole)) {
      applyChunk(out, d);
      // 只有"既无文本也无信号"的 unknown 才算真正没解析出来的块：带 usage /
      // finish 的事件已经在上面被消费了，带文本的（signature、inlineData）
      // 在界面上是可见内容，都不计入门槛。
      if (d.kind === "unknown" && !d.text && d.usage === undefined && !d.finish) result.unknown += 1;
    }
  }

  result.model = model;
  result.text = out.text;
  result.thinking = out.thinking;
  result.toolCalls = out.toolCalls;
  result.usage = out.usage;
  result.finishReason = out.finishReason;

  // tool_result 单独成桶，这里只做一件事：把它回挂到调用名上。
  const toolResults = out.toolResults as Array<Extract<Message["parts"][number], { type: "tool_result" }>>;
  attachToolResultNames([{ parts: toolResults }]);
  result.toolResults = toolResults;
  return result;
}

/**
 * 这份 IO 是不是"能认出来的聊天协议"。
 *
 * Style 已知协议时直接返回它；Style 缺失或未知时逐条响应片段嗅探，一条认出来
 * 就算认识。返回 null 表示不是聊天协议——页面据此把请求与响应整体按通用 JSON
 * 展示，而不是硬套一套解析不出来的聊天面板。
 */
export function sniffResponseProtocol(io: ChatInput, style: Protocol | null | undefined): Protocol | null {
  if (isKnown(style)) return style;
  const chunks: string[] =
    Array.isArray(io.OfStringArray) && io.OfStringArray.length > 0
      ? io.OfStringArray.filter((c): c is string => typeof c === "string" && c.length > 0)
      : typeof io.OfString === "string" && io.OfString.length > 0
        ? [io.OfString]
        : [];
  for (const raw of chunks) {
    const parsed = toChunk(raw);
    if (isObj(parsed)) {
      const sniffed = sniffProtocol(parsed);
      if (sniffed) return sniffed;
    }
  }
  return null;
}
