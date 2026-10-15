import type { ContentPart, Delta, Message, ParsedRequest, ToolCall } from "./types";
import { argsOf, isObj, str } from "./util";
import { paramsOf, parseToolDeclarations } from "./tools";

/** Google Gemini：candidates[].content.parts。 */

const GEMINI_RESERVED = ["contents", "systemInstruction", "tools", "toolConfig", "safetySettings", "history", "cachedContent"];

/** 流式：chunks[].candidates；非流式：data.candidates，同一形状，所以共用一个函数。 */
export function geminiCandidateDeltas(data: unknown): Delta[] {
  const obj = Array.isArray(data) ? undefined : isObj(data) ? (data as Record<string, unknown>) : undefined;
  const candidates = Array.isArray(data) ? data : isObj(data) ? (data as Record<string, unknown>).candidates : undefined;
  const out: Delta[] = [];
  if (Array.isArray(candidates)) {
    for (const cand of candidates as Array<Record<string, unknown>>) {
      // 标准形状是 content.parts；有些网关（含 llmio 自家转发）会拍平到 cand.parts。
      const content = isObj(cand.content) ? (cand.content as Record<string, unknown>) : cand;
      for (const part of arrayFrom(content.parts)) out.push(partToDelta(part));
      const finish = str(cand.finishReason);
      if (finish) out.push({ kind: "unknown", text: "", finish });
    }
  }
  // usageMetadata 是独立于 candidates 的顶层字段，单独发一个 unknown 记账。
  // 流式里它会单独占一条片段（不含 candidates），所以要照收。
  const usage = isObj(obj?.usageMetadata) ? (obj?.usageMetadata as Record<string, unknown>) : undefined;
  if (usage) out.push({ kind: "unknown", text: "", usage });
  return out;
}

/**
 * Gemini 的 part 用 `thought: true` 标思考片段，用 functionCall / functionResponse
 * 区分工具动作与工具结果。functionResponse 没有调用 id，只有函数名，所以回填
 * tool_result 的调用名要按名字兜底（见 assemble.attachToolResultNames）。
 */
export function partToDelta(part: Record<string, unknown>): Delta {
  if (typeof part.text === "string") {
    return part.thought ? { kind: "thinking", text: part.text } : { kind: "text", text: part.text };
  }
  const inline = isObj(part.inlineData) ? part.inlineData : isObj(part.inline_data) ? (part.inline_data as Record<string, unknown>) : undefined;
  if (inline) return { kind: "unknown", data: part, name: "inline_data", mimeType: str(inline.mimeType ?? inline.mime_type), text: str(inline.data) };
  if (isObj(part.functionCall)) {
    const fc = part.functionCall as Record<string, unknown>;
    return { kind: "tool_call", name: str(fc.name) ?? "", arguments: argsOf(fc.args) };
  }
  if (isObj(part.functionResponse)) {
    const fr = part.functionResponse as Record<string, unknown>;
    return { kind: "tool_result", callId: str(fr.name) ?? "", data: fr.response ?? fr.responseData };
  }
  return { kind: "unknown", data: part, name: "part" };
}

/** 非流式：modelVersion / candidates / usageMetadata。 */
export function geminiNonStreamDeltas(data: Record<string, unknown> | null | undefined): Delta[] {
  if (!isObj(data)) return [];
  return geminiCandidateDeltas(data);
}

export function geminiParseRequest(body: Record<string, unknown>): ParsedRequest {
  const contents = geminiContents(body.contents);
  const tools = parseToolDeclarations(body.tools, "gemini");

  const messages: Message[] = contents.map((c) => {
    const parts = arrayFrom(c.parts).map((p) => contentPartOf(partToDelta(p)));
    return { role: str(c.role) ?? "unknown", parts, toolCalls: callsOf(parts) };
  });

  // Gemini 的工具结果不带调用 id，只有函数名。先扫全量消息建表，再按名回填。
  const allNames = new Set<string>();
  for (const m of messages) for (const c of m.toolCalls) if (c.name) allNames.add(c.name);
  for (const m of messages) {
    for (const p of m.parts) {
      if (p.type === "tool_result" && !p.toolCall && allNames.has(p.callId)) p.toolCall = p.callId;
    }
  }

  return {
    protocol: "gemini",
    system: isObj(body.systemInstruction) ? flattenGeminiSystem(body.systemInstruction) : body.systemInstruction,
    messages,
    tools,
    params: paramsOf(body, GEMINI_RESERVED),
    errors: [],
  };
}

function contentPartOf(delta: Delta): ContentPart {
  switch (delta.kind) {
    case "text":
      return { type: "text", text: delta.text! };
    case "thinking":
      return { type: "thinking", text: delta.text! };
    case "tool_call":
      return { type: "tool_use", call: { name: delta.name!, arguments: delta.arguments } };
    case "tool_result":
      return { type: "tool_result", callId: delta.callId!, content: delta.data };
    case "unknown":
      if (delta.name === "inline_data") {
        return { type: "image", label: "inline_data", mimeType: delta.mimeType, data: delta.text };
      }
      return { type: "unknown", data: delta.data, name: delta.name, text: delta.text };
  }
}

/** Gemini 的 contents 既可能是数组，也可能只有一个对象。 */
function geminiContents(v: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(v)) return arrayFrom(v);
  if (isObj(v)) return [v];
  return [];
}

function flattenGeminiSystem(sys: Record<string, unknown>): unknown {
  const parts = arrayFrom(sys.parts);
  if (parts.length > 0) return parts.map((p) => str(p.text) ?? JSON.stringify(p)).join("\n\n");
  return str(sys.text) ?? sys;
}

function callsOf(parts: ContentPart[]): ToolCall[] {
  return parts.filter((p): p is Extract<ContentPart, { type: "tool_use" }> => p.type === "tool_use").map((p) => p.call);
}

function arrayFrom(v: unknown): Array<Record<string, unknown>> {
  return Array.isArray(v) ? (v as Array<Record<string, unknown>>) : [];
}
