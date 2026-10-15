import type { ContentPart, Delta, ToolCall } from "./types";

export interface AssembledResponse {
  text: string;
  thinking: string[];
  toolCalls: ToolCall[];
  toolResults: ContentPart[];
  unknown: ContentPart[];
  usage?: unknown;
  finishReason?: string;
}

export function emptyAssembled(): AssembledResponse {
  return { text: "", thinking: [], toolCalls: [], toolResults: [], unknown: [] };
}

/**
 * 唯一的装配器。
 *
 * 流式（逐条 SSE 增量）与非流式（整包被拆成 delta 后再进来）都走这里，所以
 * 两条路径不会出现"流式有 thinking 卡片、非流式没有"这类分叉。参照项目只
 * 覆盖 OpenAI 的 choices 形状；这里按 kind 分发，四协议共用一套。
 *
 * 不变式：text 只收 kind=text 的正文，thinking / tool_call / tool_result 各自
 * 单独成桶。旧实现把流式 delta.tool_calls 直接 JSON.stringify 塞进正文字符串，
 * 读者看到的是正文中间夹着一坨 JSON——那条路径已经不存在了。
 */
export function applyChunk(out: AssembledResponse, d: Delta): AssembledResponse {
  switch (d.kind) {
    case "text":
      if (d.text) out.text += d.text;
      break;
    case "thinking":
      if (d.text) out.thinking.push(d.text);
      break;
    case "tool_call": {
      // 按 id 建索引，找不到再退回 index：id 跨分片稳定，index 只在"同一响应
      // 的多个并发调用"里有意义。两者都没有时才新建条目。
      const incoming: ToolCall = { name: d.name ?? "", arguments: d.arguments };
      if (d.id) incoming.id = d.id;
      if (d.index !== undefined) incoming.index = d.index;

      let target = d.id ? out.toolCalls.find((c) => c.id === d.id) : undefined;
      if (!target && d.index !== undefined) target = out.toolCalls.find((c) => c.index === d.index);
      if (!target) {
        out.toolCalls.push(incoming);
      } else if (d.final) {
        // "最终"增量（Responses 的 output_item.added 带完整 arguments、Anthropic
        // 的 content_block_start 带完整 id/name）：整体替换，不跟前面的分片拼。
        if (d.name) target.name = d.name;
        if (d.arguments !== undefined) target.arguments = d.arguments;
      } else {
        if (d.name) target.name += d.name;
        target.arguments = accumulate(target.arguments, d.arguments);
      }
      break;
    }
    case "tool_result":
      out.toolResults.push({ type: "tool_result", callId: d.callId ?? d.id ?? "", content: d.data, toolCall: d.name });
      break;
    case "unknown":
      out.unknown.push({ type: "unknown", data: d.data, text: d.text, name: d.name, index: d.index, mimeType: d.mimeType });
      break;
  }

  if (d.usage !== undefined) out.usage = d.usage;
  if (d.finish) out.finishReason = d.finish;
  return out;
}

/**
 * 分片累积：两个字符串相接（流式 tool call 的参数逐片到达）；一方是非字符串
 * 就整体替换（非流式的完整对象）；空值不覆盖已有内容。
 */
function accumulate(prev: unknown, next: unknown): unknown {
  if (next === undefined || next === null) return prev;
  if (prev === undefined || prev === null) return next;
  return typeof prev === "string" && typeof next === "string" ? prev + next : next;
}

/**
 * 把 tool_result 回挂到它回应的调用名上。
 *
 * 参照实现里这一步是每次遍历历史消息做一次 filter，O(n²)；这里先建一次
 * id → 调用名 的表再回填，整条消息序列只走一遍。协议侧已经给出调用名的
 * （Gemini 按名匹配、Responses 按 call_id 反查）保留原值不动。
 */
export function attachToolResultNames(messages: Array<{ parts: ContentPart[] }>): void {
  const byId = new Map<string, string>();
  for (const m of messages) {
    for (const p of m.parts) {
      if (p.type === "tool_use" && p.call.id && !byId.has(p.call.id)) byId.set(p.call.id, p.call.name);
    }
  }

  for (const m of messages) {
    for (const p of m.parts) {
      if (p.type === "tool_result" && !p.toolCall) p.toolCall = byId.get(p.callId);
    }
  }
}
