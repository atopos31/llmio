// 解析层的小工具。全部纯函数、无副作用，便于单测。

/** 非 null 的普通对象。数组会被判为 false——协议里的数组字段有各自的分支。 */
export function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** 有值才转字符串；空串与 undefined 都当"没有"（返回 undefined）。这样调用方写 `?? 回退` 就能真正生效。 */
export function str(v: unknown): string | undefined {
  return typeof v === "string" && v.length > 0 ? v : undefined;
}

/** 宽松取数字。 */
export function isNum(v: unknown): v is number {
  return typeof v === "number" && Number.isFinite(v);
}

/** 字符串数组里的对象。 */
export function arrayFrom(v: unknown): Array<Record<string, unknown>> {
  return Array.isArray(v) ? (v as Array<Record<string, unknown>>) : [];
}

/**
 * JSON 解析 + 失败标记。
 *
 * 工具参数在流式里是逐片字符串，最后一刀可能落在半个括号上，这时必须保留
 * 原文让人看得见，而不是抛异常把整条日志吞掉。返回值保留原文，标记留给界面。
 */
export function parseArgs(raw: unknown): { value: unknown; error?: boolean } {
  if (typeof raw !== "string") return { value: raw };
  if (raw.length === 0) return { value: "" };
  try {
    return { value: JSON.parse(raw) };
  } catch {
    return { value: raw, error: true };
  }
}

/**
 * 装配器约定 tool_call.arguments 是字符串才会按分片拼接；非流式整包往往是
 * 完整对象，统一转成 JSON 字符串免得被拆开或误拼接。
 */
export function argsOf(args: unknown): unknown {
  if (args === undefined || args === null || typeof args === "string") return args;
  try {
    return JSON.stringify(args);
  } catch {
    return String(args);
  }
}

/** JSON 片段：能解析就给对象，解析不了就原样回显（原始视图依赖这一点）。 */
export function toChunk(raw: unknown): Record<string, unknown> | string | null {
  if (isObj(raw)) return raw;
  if (typeof raw !== "string") return null;
  if (raw.length === 0) return null;
  try {
    const value = JSON.parse(raw);
    return isObj(value) ? value : typeof value === "string" ? value : null;
  } catch {
    return null;
  }
}
