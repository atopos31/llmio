import type { ContentPart, Delta, Message, ParsedRequest, ToolCall } from "./types";
import { argsOf, arrayFrom, isObj, parseArgs, str } from "./util";
import { paramsOf, parseToolDeclarations } from "./tools";

/** Anthropic Messages API（Claude）。 */

const ANTHROPIC_RESERVED = ["model", "messages", "system", "tools", "tool_choice", "metadata", "stream"];

/**
 * 流式事件只有两类信息：content_block_delta 的增量文本，与 message_delta 的
 * 结束原因/用量。其余事件（message_start、ping、content_block_stop…）一律落
 * unknown，不静默丢弃。
 */
export function anthropicToDeltas(chunk: Record<string, unknown>): Delta[] {
  const type = str(chunk.type);
  if (!type) return [];

  if (type === "message_start") return [{ kind: "unknown", data: chunk }];

  if (type === "content_block_start") {
    const block = isObj(chunk.content_block) ? (chunk.content_block as Record<string, unknown>) : undefined;
    if (isObj(block) && str(block.type) === "tool_use") {
      return [{ kind: "tool_call", index: isNum(chunk.index) ? chunk.index : undefined, id: str(block.id) ?? "", name: str(block.name) ?? "", arguments: undefined }];
    }
    return [];
  }

  if (type === "content_block_delta") {
    return deltasFromDelta(isObj(chunk.delta) ? (chunk.delta as Record<string, unknown>) : undefined, isNum(chunk.index) ? chunk.index : undefined);
  }

  if (type === "message_delta") {
    const delta = isObj(chunk.delta) ? (chunk.delta as Record<string, unknown>) : undefined;
    const finish = str(delta?.stop_reason);
    const usage = isObj(chunk.usage) ? (chunk.usage as Record<string, unknown>) : undefined;
    if (!finish && !usage) return [];
    return [{ kind: "unknown", text: "", finish: finish ?? "", usage }];
  }

  if (type === "error") return [{ kind: "unknown", data: chunk.error ?? chunk }];
  return [{ kind: "unknown", data: chunk }];
}

/** content_block_delta 与 message_delta 共用：认得哪些子字段。 */
function deltasFromDelta(delta: Record<string, unknown> | undefined, index?: number): Delta[] {
  if (!delta) return [];

  const t = str(delta.type);
  if (t === "text_delta") return [{ kind: "text", text: str(delta.text) ?? "" }];
  if (t === "thinking_delta") return [{ kind: "thinking", text: str(delta.thinking) ?? "" }];
  if (t === "input_json_delta") {
    return [{ kind: "tool_call", index, id: "", name: "", arguments: str(delta.partial_json) ?? undefined }];
  }
  if (t === "signature_delta") {
    return [{ kind: "unknown", data: { signature: delta.signature }, text: str(delta.signature) ?? undefined }];
  }
  return [{ kind: "unknown", data: delta, text: str(delta.text) ?? undefined, index }];
}

/** 非流式：content 数组里的每个块归一成增量，再走同一个装配器。 */
export function anthropicNonStreamDeltas(message: Record<string, unknown> | null | undefined): Delta[] {
  if (!isObj(message)) return [];
  const out: Delta[] = [];
  for (const block of arrayFrom(message.content)) out.push(...deltasFromBlock(block));

  const finish = str(message.stop_reason);
  const usage = isObj(message.usage) ? (message.usage as Record<string, unknown>) : undefined;
  if (finish || usage) out.push({ kind: "unknown", text: "", finish: finish ?? "", usage });
  return out;
}

/** 一个 Anthropic 内容块 → 一个增量（请求侧与响应侧共用）。 */
export function deltasFromBlock(block: Record<string, unknown>): Delta[] {
  const t = str(block.type);
  if (t === "text") return [{ kind: "text", text: str(block.text) ?? "" }];
  if (t === "thinking" || t === "redacted_thinking") {
    return [{ kind: "thinking", text: str(block.thinking) ?? str(block.data) ?? "" }];
  }
  if (t === "tool_use") {
    return [{ kind: "tool_call", id: str(block.id) ?? "", name: str(block.name) ?? "", arguments: argsOf(block.input) }];
  }
  if (t === "tool_result") {
    return [{ kind: "tool_result", callId: str(block.tool_use_id) ?? "", data: block.content, text: textOf(block.content) }];
  }
  if (t === "image") {
    const source = isObj(block.source) ? (block.source as Record<string, unknown>) : undefined;
    return [{ kind: "unknown", data: block, name: "image", mimeType: str(source?.media_type), text: str(source?.data) }];
  }
  if (t) return [{ kind: "unknown", data: block, name: t }];
  return [];
}

function textOf(content: unknown): string | undefined {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content.map((c) => (typeof c === "string" ? c : str((c as Record<string, unknown>)?.text) ?? JSON.stringify(c))).join("\n");
  }
  if (isObj(content)) return str(content.text) ?? JSON.stringify(content);
  return undefined;
}

export function anthropicParseRequest(body: Record<string, unknown>): ParsedRequest {
  const msgs = Array.isArray(body.messages) ? (body.messages as Array<Record<string, unknown>>) : [];

  const messages: Message[] = msgs.map((m) => {
    const parts = Array.isArray(m.content)
      ? (m.content as Array<Record<string, unknown>>).map(contentPartFrom).flat()
      : typeof m.content === "string" && m.content.length > 0
        ? ([{ type: "text", text: m.content }] as ContentPart[])
        : [];
    return { role: str(m.role) ?? "unknown", parts, toolCalls: callsOf(parts) };
  });

  return {
    protocol: "anthropic",
    model: str(body.model) ?? undefined,
    system: isObj(body.system) ? flattenSystem(body.system) : Array.isArray(body.system) ? flattenSystem({ parts: body.system }) : body.system,
    messages,
    tools: parseToolDeclarations(body.tools, "anthropic"),
    params: paramsOf(body, ANTHROPIC_RESERVED),
    errors: [],
  };
}

/** 请求侧的 Anthropic 内容块 → ContentPart。 */
function contentPartFrom(block: Record<string, unknown>): ContentPart[] {
  const t = str(block.type);
  if (t === "text") return [{ type: "text", text: str(block.text) ?? "" }];
  if (t === "thinking" || t === "redacted_thinking") {
    return [{ type: "thinking", text: str(block.thinking) ?? str(block.data) ?? "" }];
  }
  if (t === "tool_use") {
    const parsed = parseArgs(block.input);
    const call: ToolCall = { id: str(block.id) ?? undefined, name: str(block.name) ?? "", arguments: parsed.value };
    if (parsed.error) call.parseError = true;
    return [{ type: "tool_use", call }];
  }
  if (t === "tool_result") {
    return [{ type: "tool_result", callId: str(block.tool_use_id) ?? "", content: block.content, toolCall: undefined }];
  }
  if (t === "image") {
    const source = isObj(block.source) ? (block.source as Record<string, unknown>) : undefined;
    const image: ContentPart = { type: "image", mimeType: str(source?.media_type), data: str(source?.data) };
    return [image];
  }
  if (t) return [{ type: "unknown", data: block, name: t }];
  return [];
}

function flattenSystem(system: Record<string, unknown>): unknown {
  const parts = arrayFrom(system.parts);
  if (parts.length > 0) return parts.map((p) => str(p.text) ?? JSON.stringify(p)).join("\n\n");
  return str(system.text) ?? system;
}

function isNum(v: unknown): v is number {
  return typeof v === "number" && Number.isFinite(v);
}

function callsOf(parts: ContentPart[]): ToolCall[] {
  return parts.filter((p): p is Extract<ContentPart, { type: "tool_use" }> => p.type === "tool_use").map((p) => p.call);
}
