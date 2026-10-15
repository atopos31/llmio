import type { ContentPart, Delta, Message, ParsedRequest, ToolCall } from "./types";
import { argsOf, isObj, parseArgs, str } from "./util";
import { paramsOf, parseToolDeclarations } from "./tools";

/** OpenAI Responses API：流式是一串事件，非流式是 {output: [...]}。 */

const OPENAI_RES_RESERVED = [
  "model",
  "input",
  "instructions",
  "tools",
  "tool_choice",
  "stream",
  "previous_response_id",
  "user",
  "metadata",
];

export function openaiResToDeltas(chunk: Record<string, unknown>): Delta[] {
  const type = str(chunk.type);
  if (!type) return [];

  if (type === "response.created" || type === "response.in_progress") {
    const response = isObj(chunk.response) ? (chunk.response as Record<string, unknown>) : undefined;
    return [{ kind: "unknown", data: chunk, usage: isObj(response?.usage) ? (response?.usage as Record<string, unknown>) : undefined }];
  }
  if (type === "response.output_item.added") return outputDeltas((chunk.item as Record<string, unknown>) ?? null);
  if (type === "response.content_part.added") {
    const part = isObj(chunk.part) ? (chunk.part as Record<string, unknown>) : undefined;
    if (!part) return [];
    const pt = str(part.type);
    if (pt === "text" || pt === "output_text" || pt === "input_text") {
      const text = str(part.text);
      return text ? [{ kind: "text", text }] : [];
    }
    if (pt === "refusal") {
      const text = str(part.refusal);
      return text ? [{ kind: "text", text }] : [];
    }
    if (pt === "input_image") return [{ kind: "unknown", data: part, name: "image" }];
    if (pt) return [{ kind: "unknown", data: part, name: pt }];
    return [];
  }

  if (type === "response.output_text.delta" || type === "response.refusal.delta") {
    const text = str(chunk.delta);
    return text ? [{ kind: "text", text }] : [];
  }
  if (type === "response.reasoning_summary_text.delta") {
    const text = str(chunk.delta);
    return text ? [{ kind: "thinking", text }] : [];
  }
  if (type === "response.function_call_arguments.delta") {
    const text = str(chunk.delta);
    return text ? [{ kind: "tool_call", id: str(chunk.item_id) ?? "", name: "", arguments: text }] : [];
  }

  if (type === "response.completed" || type === "response.incomplete" || type === "response.failed") {
    const response = isObj(chunk.response) ? (chunk.response as Record<string, unknown>) : undefined;
    const status = str(response?.status);
    const usage = isObj(response?.usage) ? (response?.usage as Record<string, unknown>) : undefined;
    if (!status && !usage) return [];
    return [{ kind: "unknown", text: "", finish: status ?? "", usage }];
  }

  if (type === "response.error") return [{ kind: "unknown", data: chunk.error ?? chunk }];
  return [{ kind: "unknown", data: chunk }];
}

/** output 数组里的一个条目：message / function_call / function_call_output / reasoning。 */
function outputDeltas(item: Record<string, unknown> | null | undefined): Delta[] {
  if (!isObj(item)) return [];
  const t = str(item.type);
  if (!t) return [];

  if (t === "message") {
    const out: Delta[] = [];
    for (const part of arrayFrom(item.content)) {
      const pt = str(part.type);
      if (pt === "text" || pt === "output_text" || pt === "input_text") out.push({ kind: "text", text: str(part.text) ?? "" });
      else if (pt === "refusal") out.push({ kind: "text", text: str(part.refusal) ?? "" });
      else if (pt === "input_image") out.push({ kind: "unknown", data: part, name: "image" });
      else if (pt) out.push({ kind: "unknown", data: part, name: pt });
    }
    const status = str(item.status);
    const usage = isObj(item.usage) ? (item.usage as Record<string, unknown>) : undefined;
    if (status || usage) out.push({ kind: "unknown", text: "", finish: status ?? "", usage });
    return out;
  }

  if (t === "function_call") {
    return [{ kind: "tool_call", id: str(item.call_id) ?? str(item.id) ?? "", name: str(item.name) ?? "", arguments: argsOf(item.arguments), final: true }];
  }
  if (t === "function_call_output") {
    return [{ kind: "tool_result", callId: str(item.call_id) ?? "", data: item.output, text: str(item.output) ?? undefined }];
  }
  if (t === "reasoning") {
    const out: Delta[] = [];
    for (const summary of arrayFrom(item.summary)) {
      const text = str(summary.text);
      if (text) out.push({ kind: "thinking", text });
    }
    return out;
  }
  if (t === "custom_tool_call") {
    return [{ kind: "tool_call", id: str(item.call_id) ?? str(item.id) ?? "", name: str(item.name) ?? "", arguments: item.input, final: true }];
  }
  if (t === "custom_tool_call_output") {
    return [{ kind: "tool_result", callId: str(item.call_id) ?? "", data: item.output, text: str(item.output) ?? undefined }];
  }
  return [{ kind: "unknown", data: item, name: t }];
}

/** 非流式：output 数组整体过一遍，finish 与 usage 取顶层。 */
export function openaiResNonStreamDeltas(data: Record<string, unknown> | null | undefined): Delta[] {
  if (!isObj(data)) return [];
  const out: Delta[] = [];
  for (const item of arrayFrom(data.output)) out.push(...outputDeltas(item));

  const status = str(data.status);
  const usage = isObj(data.usage) ? (data.usage as Record<string, unknown>) : undefined;
  if (status || usage) out.push({ kind: "unknown", text: "", finish: status ?? "", usage });
  return out;
}

export function openaiResParseRequest(body: Record<string, unknown>): ParsedRequest {
  const input = body.input;
  const items = Array.isArray(input) ? (input as Array<Record<string, unknown>>) : isObj(input) ? [input] : [];
  const tools = parseToolDeclarations(body.tools, "openai-res");

  const messages: Message[] = [];
  let role = "system";
  let parts: ContentPart[] = [];

  // 累积而不逐条 flush：一条 Responses 请求里 assistant 的 reasoning 与
  // message 分属两个条目，应当合成同一条对话消息，而不是拆成两段。
  const flush = () => {
    if (parts.length === 0 && role === "system") return;
    messages.push({ role, parts: parts.splice(0), toolCalls: callsOf(parts) });
  };

  for (const item of items) {
    const t = str(item.type);
    if (!t) continue;

    if (t === "message") {
      flush();
      role = str(item.role) ?? "assistant";
      for (const part of arrayFrom(item.content)) {
        const pt = str(part.type);
        if (pt === "input_text" || pt === "text" || pt === "output_text") parts.push({ type: "text", text: str(part.text) ?? "" });
        else if (pt === "input_image") parts.push({ type: "image", url: str(part.image_url), label: "image", mimeType: str(part.detail) });
        else if (pt === "refusal") parts.push({ type: "text", text: str(part.refusal) ?? "" });
        else if (pt) parts.push({ type: "unknown", data: part, name: pt });
      }
      continue;
    }

    if (t === "function_call" || t === "custom_tool_call") {
      if (role !== "assistant") {
        flush();
        role = "assistant";
      }
      const callId = str(item.call_id) ?? str(item.id) ?? "";
      const parsed = parseArgs(item.arguments ?? item.input);
      const call: ToolCall = { id: callId || undefined, name: str(item.name) ?? "", arguments: parsed.value };
      if (parsed.error) call.parseError = true;
      parts.push({ type: "tool_use", call });
      continue;
    }

    if (t === "function_call_output" || t === "custom_tool_call_output") {
      const callId = str(item.call_id) ?? "";
      flush();
      role = "tool";
      parts.push({ type: "tool_result", callId, content: item.output, toolCall: callName(items, callId) });
      continue;
    }

    if (t === "reasoning") {
      if (role !== "assistant") {
        flush();
        role = "assistant";
      }
      for (const summary of arrayFrom(item.summary)) {
        const text = str(summary.text);
        if (text) parts.push({ type: "thinking", text });
      }
      continue;
    }

    flush();
    role = "unknown";
    parts.push({ type: "unknown", data: item, name: t });
  }
  flush();

  return {
    protocol: "openai-res",
    model: str(body.model) ?? undefined,
    system: str(body.instructions) ?? undefined,
    messages,
    tools,
    params: paramsOf(body, OPENAI_RES_RESERVED),
    errors: [],
  };
}

/** function_call_output 只有 call_id 没有函数名，所以往前扫 input 找回名字。 */
function callName(items: Array<Record<string, unknown>>, callId: string): string | undefined {
  if (!callId) return undefined;
  for (const item of items) {
    if (str(item.type) !== "function_call" && str(item.type) !== "custom_tool_call") continue;
    const id = str(item.call_id) ?? str(item.id);
    if (id === callId) return str(item.name);
  }
  return undefined;
}

function callsOf(parts: ContentPart[]): ToolCall[] {
  return parts.filter((p): p is Extract<ContentPart, { type: "tool_use" }> => p.type === "tool_use").map((p) => p.call);
}

function arrayFrom(v: unknown): Array<Record<string, unknown>> {
  return Array.isArray(v) ? (v as Array<Record<string, unknown>>) : [];
}
