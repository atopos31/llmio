import type { Protocol, ToolDeclaration } from "./types";
import { arrayFrom, isObj, str } from "./util";

/**
 * 内置工具没有可展示的函数名与参数（Google 的内置检索、OpenAI 的 web_search），
 * 各协议写法不同，这里列一下以免界面上多出一行空名字。
 */
const BUILTIN_TOOLS: Record<Protocol, readonly string[]> = {
  openai: ["web_search", "file_search", "computer_use", "code_interpreter"],
  "openai-res": ["web_search", "file_search", "computer_use", "image_gen"],
  anthropic: [],
  gemini: ["google_search", "google_maps", "code_execution"],
};

// Gemini 的内置工具不是靠 type 字段标记，而是靠一个同名的顶层键。
// 官方文档用 camelCase，网关转发常见 snake_case，两种都认。
const GEMINI_BUILTIN_KEYS = ["googleSearch", "google_search", "googleMaps", "google_maps", "codeExecution", "code_execution"] as const;

/**
 * 工具声明归一。四种协议的工具表形状各不相同，但界面只需要
 * name / description / parameters 三样，所以在这里一次性压平。
 *
 * 识别顺序有讲究：OpenAI 的 function 包裹式优先，其次才是顶层 name——
 * 后者在 OpenAI Responses 与 Anthropic 里就是原生形状。
 */
export function parseToolDeclarations(tools: unknown, style: Protocol): ToolDeclaration[] {
  if (!Array.isArray(tools)) return [];
  const builtins = new Set(BUILTIN_TOOLS[style]);
  const out: ToolDeclaration[] = [];

  for (const tool of arrayFrom(tools)) {
    if (!isObj(tool)) continue;

    if (builtins.has(str(tool.type) ?? "")) continue;
    if (style === "gemini" && GEMINI_BUILTIN_KEYS.some((k) => tool[k] !== undefined)) continue;

    const fn = isObj(tool.function) ? (tool.function as Record<string, unknown>) : undefined;

    if (fn) {
      out.push(declFrom(fn));
      continue;
    }
    if (str(tool.name)) {
      out.push({
        name: str(tool.name)!,
        description: str(tool.description) ?? undefined,
        parameters: tool.parameters ?? tool.input_schema ?? undefined,
      });
      continue;
    }
    const decls = arrayFrom(tool.functionDeclarations);
    if (decls.length > 0) {
      for (const d of decls) out.push(declFrom(d));
      continue;
    }
    out.push({ name: "" });
  }
  return out;
}

function declFrom(d: Record<string, unknown>): ToolDeclaration {
  return {
    name: str(d.name) ?? "",
    description: str(d.description) ?? undefined,
    parameters: d.parameters ?? d.input_schema ?? undefined,
  };
}

/**
 * 剩余参数：请求体里除已知字段之外的部分。
 *
 * 各协议的"已知字段"不同（OpenAI 的 messages 与 Anthropic 的 messages 同名但
 * 形状不同），所以每个解析器传自己的保留集。generationConfig 这类复合配置由
 * gemini 的保留集单独处理，避免和参数混在一栏里看不清。
 */
export function paramsOf(body: Record<string, unknown>, reserved: string[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const key of Object.keys(body)) {
    if (reserved.includes(key)) continue;
    out[key] = body[key];
  }
  return out;
}
