import type { ContentPart, Delta, Message, ParsedRequest, ToolCall } from "./types";
import { arrayFrom, argsOf, isObj, isNum, parseArgs, str } from "./util";
import { paramsOf, parseToolDeclarations } from "./tools";

/** OpenAI Chat Completions（含一切兼容该接口的网关与厂商转发）。 */

const OPENAI_RESERVED = ["model", "messages", "tools", "stream", "stream_options", "tool_choice", "response_format"];

// 各家的推理字段命名不同：OpenAI 官方是 reasoning，若干网关用 reasoning_content，
// 还有 reasoning_details。这里统一取一遍，谁写进来都能被认成"思考"。
const THINKING_KEYS = ["reasoning_content", "reasoning", "reasoning_details"] as const;

/**
 * 流式与非流式共用一个入口：非流式在 parseResponse 里把 message 包装成一个
 * 合成 delta，所以下游只需要认 choices[].delta 这一个形状。
 */
export function openaiToDeltas(chunk: Record<string, unknown>): Delta[] {
  // usage 是独立于 choices 的顶层字段（stream_options.include_usage 场景），
  // 单独发一个 unknown 让下游记账。
  const usage = isObj(chunk.usage) ? (chunk.usage as Record<string, unknown>) : undefined;
  const out: Delta[] = [];
  if (usage) out.push({ kind: "unknown", text: "", usage });
  if (!Array.isArray(chunk.choices)) return out;

  for (const choice of chunk.choices as Array<Record<string, unknown>>) {
    const delta = isObj(choice.delta) ? (choice.delta as Record<string, unknown>) : undefined;
    const finish = str(choice.finish_reason);
    if (!delta && !finish) continue;

    if (delta) {
      if (typeof delta.content === "string") out.push({ kind: "text", text: delta.content });
      for (const key of THINKING_KEYS) {
        const v = delta[key];
        if (typeof v === "string") out.push({ kind: "thinking", text: v });
      }
      for (const tc of arrayFrom(delta.tool_calls)) {
        const fn = isObj(tc.function) ? (tc.function as Record<string, unknown>) : undefined;
        const args = argsOf(fn && "arguments" in fn ? fn.arguments : undefined);
        out.push({
          kind: "tool_call",
          index: isNum(tc.index) ? tc.index : undefined,
          id: str(tc.id) ?? undefined,
          name: str(fn?.name) ?? "",
          arguments: args,
        });
      }
    }
    if (finish) out.push({ kind: "unknown", text: "", finish });
  }
  return out;
}

export function openaiParseRequest(body: Record<string, unknown>): ParsedRequest {
  const msgs = arrayFrom(body.messages);

  const messages: Message[] = msgs.map((m) => {
    const parts = contentToParts(m.content, str(m.tool_call_id) ?? str(m.toolCallId));
    for (const key of THINKING_KEYS) {
      const v = m[key];
      if (typeof v === "string") parts.push({ type: "thinking", text: v });
    }
    const toolCalls = arrayFrom(m.tool_calls).map(toolCallFrom);
    return { role: str(m.role) ?? "unknown", parts, toolCalls };
  });

  return {
    protocol: "openai",
    model: str(body.model) ?? undefined,
    messages,
    tools: parseToolDeclarations(body.tools, "openai"),
    params: paramsOf(body, OPENAI_RESERVED),
    errors: [],
  };
}

/** content 可能是字符串、多模态数组，也可能整个就是一个对象。 */
function contentToParts(content: unknown, callId?: string): ContentPart[] {
  if (typeof content === "string") {
    if (content.length === 0) return [];
    return callId ? [{ type: "tool_result", callId, content }] : [{ type: "text", text: content }];
  }

  if (Array.isArray(content)) {
    return (content as Array<Record<string, unknown>>).map((part) => {
      const t = str(part.type);
      if (t === "text") return { type: "text" as const, text: str(part.text) ?? "" };
      if (t === "image_url") {
        const url = isObj(part.image_url) ? (part.image_url as Record<string, unknown>).url : undefined;
        return { type: "image" as const, url: str(url) ?? undefined, label: "image" };
      }
      if (t === "input_audio") {
        const audio = isObj(part.input_audio) ? (part.input_audio as Record<string, unknown>) : undefined;
        return { type: "audio" as const, label: "audio", data: str(audio?.data), mimeType: str(audio?.format) };
      }
      return { type: "unknown" as const, data: part, name: t };
    });
  }

  if (content === undefined || content === null) return [];
  if (callId) return [{ type: "tool_result", callId, content }];
  return [{ type: "unknown", data: content, name: "content" }];
}

/** 请求体里 assistant 消息的 tool_calls 与响应侧 delta 走同一个形状，共用一个解析函数。 */
function toolCallFrom(tc: Record<string, unknown>): ToolCall {
  const fn = isObj(tc.function) ? (tc.function as Record<string, unknown>) : undefined;
  const rawArgs = fn?.arguments;
  const call: ToolCall = { id: str(tc.id) ?? undefined, index: isNum(tc.index) ? tc.index : undefined, name: str(fn?.name) ?? "", arguments: rawArgs };
  if (typeof rawArgs === "string" && parseArgs(rawArgs).error) call.parseError = true;
  return call;
}
