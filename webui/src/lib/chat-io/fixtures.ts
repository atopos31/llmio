import type { ChatInput } from "./types"

/**
 * 真实形状的样例载荷。字段名与取值都照各协议官方文档写，不做简化——
 * 解析层最怕的就是"用了一个永远不出现的形状"。
 *
 * 夹具是测试用数据，不进覆盖率门禁（见 vitest.config.ts 的 include 清单）。
 *
 * 排布约定（刻意）：数组里嵌对象、对象里再嵌数组的样例一律「一行一条，
 * 收尾括号缩进独占一行」。tsc 5.8 在这种嵌套里存在解析歧义，把几个
 * 收尾括号挤在一行末尾会误报 `',' expected`；摊开就稳定。
 */

const io = (patch: Partial<ChatInput>): ChatInput => patch

export const fixtures = {
  // ── OpenAI Chat Completions ─────────────────────────────────────────────

  openaiRequest: io({
    Style: "openai",
    Input: JSON.stringify({
      model: "gpt-4o",
      messages: [
        { role: "system", content: "你是一个天气助手" },
        {
          role: "user",
          content: [
            { type: "text", text: "上海现在怎么样？" },
            { type: "image_url", image_url: { url: "https://example.com/a.png" } },
            { type: "input_audio", input_audio: { data: "AAAA", format: "wav" } },
            { type: "video_url", video_url: { url: "https://example.com/a.mp4" } },
          ],
        },
        {
          role: "assistant",
          content: "我先查一下天气",
          tool_calls: [
            { id: "call_1", index: 0, type: "function", function: { name: "get_weather", arguments: "{\"city\":\"上海\"}" } },
            { id: "call_2", index: 1, type: "function", function: { name: "broken", arguments: "{\"city\":" } },
          ],
        },
        { role: "tool", tool_call_id: "call_1", content: "{\"temp\":31,\"cond\":\"晴\"}" },
      ],
      tools: [
        { type: "function", function: { name: "get_weather", description: "查询天气", parameters: { type: "object", properties: { city: { type: "string" } } } } },
        { type: "web_search", search_context_size: "medium" },
      ],
      temperature: 0.7,
    }),
  }),

  // 请求名与上游模型 id 不同：这是"同名处理"最常见的形态。
  openaiStream: io({
    Style: "openai",
    OfStringArray: [
      JSON.stringify({
        id: "1",
        model: "gpt-4o-2024",
        choices: [{ index: 0, delta: { role: "assistant" } }],
      }),
      JSON.stringify({ choices: [{ index: 0, delta: { content: "上海" } }] }),
      JSON.stringify({ choices: [{ index: 0, delta: { content: "今天" } }] }),
      JSON.stringify({ choices: [{ index: 0, delta: { reasoning_content: "先查一下实时天气。" } }] }),
      JSON.stringify({
        choices: [
          {
            index: 0,
            delta: {
              tool_calls: [
                { index: 0, id: "call_a", function: { name: "get_", arguments: "{\"cit" } },
                { index: 1, id: "call_b", function: { name: "search", arguments: "{\"q\":\"天气\"}" } },
              ],
            },
          },
        ],
      }),
      JSON.stringify({
        choices: [
          {
            index: 0,
            delta: {
              tool_calls: [
                { index: 0, function: { name: "weather", arguments: "y\":\"上海\"}" } },
              ],
            },
          },
        ],
      }),
      JSON.stringify({
        choices: [
          {
            index: 0,
            delta: { content: "多云 31°" },
            finish_reason: "stop",
          },
        ],
        usage: {
          prompt_tokens: 10,
          completion_tokens: 20,
          total_tokens: 30,
        },
      }),
    ],
  }),

  openaiNonStream: io({
    Style: "openai",
    OfString: JSON.stringify({
      id: "3",
      model: "gpt-4o",
      choices: [
        {
          index: 0,
          finish_reason: "tool_calls",
          message: {
            role: "assistant",
            content: null,
            reasoning: "思考过程",
            tool_calls: [
              { id: "call_n", index: 0, type: "function", function: { name: "get_weather", arguments: "{\"city\":\"北京\"}" } },
            ],
          },
        },
      ],
      usage: {
        prompt_tokens: 5,
        completion_tokens: 6,
        total_tokens: 11,
      },
    }),
  }),

  // ── OpenAI Responses ────────────────────────────────────────────────────

  openaiResRequest: io({
    Style: "openai-res",
    Input: JSON.stringify({
      model: "gpt-4o-responses",
      instructions: "只回答问题本身",
      input: [
        { type: "reasoning", summary: [{ type: "summary_text", text: "推理摘要第一段。" }] },
        {
          type: "message",
          role: "user",
          content: [
            { type: "input_text", text: "帮我查一下" },
            { type: "input_image", image_url: "https://example.com/b.png", detail: "low" },
          ],
        },
        { type: "function_call", call_id: "fc_1", name: "get_weather", arguments: "{\"city\":\"深圳\"}" },
        { type: "function_call_output", call_id: "fc_1", output: "{\"temp\":29}" },
        { type: "custom_tool_call", call_id: "cc_1", name: "my_tool", input: "{\"x\":1}" },
        { type: "custom_tool_call_output", call_id: "cc_1", output: "ok" },
      ],
      tools: [
        { type: "function", name: "get_weather", parameters: { type: "object" } },
        { type: "web_search", search_context_size: "high" },
      ],
      max_output_tokens: 512,
      truncation: "auto",
    }),
  }),

  openaiResStream: io({
    Style: "openai-res",
    OfStringArray: [
      JSON.stringify({ type: "response.created", response: { id: "r1", model: "gpt-4o-responses", usage: { input_tokens: 3 } } }),
      JSON.stringify({ type: "response.output_item.added", item: { type: "message", status: "completed", content: [{ type: "output_text", text: "第一段。" }] } }),
      JSON.stringify({ type: "response.reasoning_summary_text.delta", delta: "思考 A" }),
      JSON.stringify({ type: "response.reasoning_summary_text.delta", delta: "思考 B" }),
      JSON.stringify({ type: "response.function_call_arguments.delta", item_id: "fc_2", delta: "{\"city\"" }),
      JSON.stringify({ type: "response.function_call_arguments.delta", item_id: "fc_2", delta: ":\"杭州\"}" }),
      JSON.stringify({ type: "response.output_item.added", item: { type: "function_call", call_id: "fc_2", name: "get_weather", arguments: "{\"city\":\"杭州\"}" } }),
      JSON.stringify({ type: "response.output_item.added", item: { type: "function_call_output", call_id: "fc_2", output: "{\"temp\":27}" } }),
      JSON.stringify({ type: "response.content_part.added", part: { type: "output_text", text: "第二段。" } }),
      JSON.stringify({ type: "response.refusal.delta", delta: "拒绝内容" }),
      JSON.stringify({ type: "response.completed", response: { id: "r1", status: "completed", usage: { input_tokens: 3, output_tokens: 9 } } }),
      JSON.stringify({ type: "response.error", error: { code: "x", message: "boom" } }),
      JSON.stringify({ type: "response.other.event", foo: 1 }),
    ],
  }),

  openaiResNonStream: io({
    Style: "openai-res",
    OfString: JSON.stringify({
      id: "r2",
      model: "gpt-4o-responses",
      status: "completed",
      output: [
        {
          type: "message",
          status: "completed",
          content: [
            { type: "output_text", text: "整包文本。" },
            { type: "refusal", refusal: "不能回答" },
            { type: "input_image", image_url: "https://example.com/c.png" },
            { type: "custom_part", value: 1 },
          ],
        },
        { type: "function_call", call_id: "fc_3", name: "get_weather", arguments: "{\"city\":\"成都\"}" },
        { type: "reasoning", summary: [{ type: "summary_text", text: "非流式的推理摘要" }] },
        { type: "custom_item", value: 2 },
      ],
      usage: { input_tokens: 1, output_tokens: 2 },
    }),
  }),

  // ── Anthropic ───────────────────────────────────────────────────────────

  anthropicRequest: io({
    Style: "anthropic",
    Input: JSON.stringify({
      model: "claude-sonnet-4-20250514",
      system: [
        { type: "text", text: "系统第一段" },
        { type: "text", text: "系统第二段" },
      ],
      messages: [
        { role: "user", content: "你好" },
        {
          role: "assistant",
          content: [
            { type: "thinking", thinking: "先想一下" },
            { type: "redacted_thinking", data: "REDACTED" },
            { type: "text", text: "让我查一下" },
            { type: "tool_use", id: "tu_1", name: "get_weather", input: { city: "上海" } },
            { type: "image", source: { type: "base64", media_type: "image/png", data: "BASE64" } },
            { type: "server_tool_use", id: "st_1", name: "web_search", input: { query: "x" } },
          ],
        },
        {
          role: "user",
          content: [
            {
              type: "tool_result",
              tool_use_id: "tu_1",
              content: [
                { type: "text", text: "晴 31°" },
                "裸字符串",
                { type: "image", source: { media_type: "image/png", data: "Z" } },
              ],
            },
          ],
        },
      ],
      tools: [
        { name: "get_weather", description: "查天气", input_schema: { type: "object", properties: { city: { type: "string" } } } },
      ],
      thinking: { type: "enabled", budget_tokens: 1024 },
      max_tokens: 1000,
    }),
  }),

  anthropicStream: io({
    Style: "anthropic",
    OfStringArray: [
      JSON.stringify({ type: "message_start", message: { id: "m1", model: "claude-sonnet-4-20250514", usage: { input_tokens: 4 } } }),
      JSON.stringify({ type: "content_block_start", index: 0, content_block: { type: "thinking", thinking: "" } }),
      JSON.stringify({ type: "content_block_delta", index: 0, delta: { type: "thinking_delta", thinking: "让我" } }),
      JSON.stringify({ type: "content_block_delta", index: 0, delta: { type: "thinking_delta", thinking: "想想" } }),
      JSON.stringify({ type: "content_block_delta", index: 0, delta: { type: "signature_delta", signature: "SIG" } }),
      JSON.stringify({ type: "content_block_start", index: 1, content_block: { type: "tool_use", id: "tu_9", name: "get_weather" } }),
      JSON.stringify({ type: "content_block_delta", index: 1, delta: { type: "input_json_delta", partial_json: "{\"cit" } }),
      JSON.stringify({ type: "content_block_delta", index: 1, delta: { type: "input_json_delta", partial_json: "y\":\"上海\"}" } }),
      JSON.stringify({ type: "content_block_delta", index: 2, delta: { type: "text_delta", text: "结果" } }),
      JSON.stringify({ type: "content_block_delta", index: 3, delta: { type: "text_delta", text: " 多云" } }),
      JSON.stringify({ type: "content_block_delta", index: 4, delta: { type: "other_delta", text: "x" } }),
      JSON.stringify({ type: "content_block_stop", index: 0 }),
      JSON.stringify({ type: "message_delta", delta: { stop_reason: "tool_use" }, usage: { output_tokens: 7 } }),
      JSON.stringify({ type: "error", error: { type: "api_error", message: "boom" } }),
    ],
  }),

  anthropicNonStream: io({
    Style: "anthropic",
    OfString: JSON.stringify({
      id: "m2",
      model: "claude-sonnet-4-20250514",
      content: [
        { type: "thinking", thinking: "非流式思考" },
        { type: "redacted_thinking", data: "RED2" },
        { type: "text", text: "非流式正文" },
        { type: "tool_use", id: "tu_2", name: "get_weather", input: { city: "北京" } },
        { type: "server_tool_use", id: "st_2", name: "web_search", input: { query: "b" } },
      ],
      stop_reason: "tool_use",
      usage: { input_tokens: 2, output_tokens: 8 },
    }),
  }),

  // ── Gemini ──────────────────────────────────────────────────────────────

  geminiRequest: io({
    Style: "gemini",
    Input: JSON.stringify({
      systemInstruction: { parts: [{ text: "你是助手" }, { text: "第二段" }] },
      contents: [
        {
          role: "user",
          parts: [
            { text: "帮我查天气" },
            { inlineData: { mimeType: "image/png", data: "PNGDATA" } },
            { inline_data: { mime_type: "image/jpeg", data: "JPGDATA" } },
          ],
        },
        {
          role: "model",
          parts: [
            { text: "思考中", thought: true },
            { functionCall: { name: "get_weather", args: { city: "上海" } } },
            { custom_part: { v: 1 } },
          ],
        },
        {
          role: "user",
          parts: [
            { functionResponse: { name: "get_weather", response: { temp: 31 } } },
            { functionResponse: { name: "unknown_tool", response: { ok: 1 } } },
          ],
        },
      ],
      tools: [
        {
          functionDeclarations: [
            { name: "get_weather", description: "查天气", parameters: { type: "object" } },
            { name: "get_time", parameters: { type: "object" } },
          ],
        },
        { google_search: {} },
      ],
      generationConfig: { temperature: 0.4, maxOutputTokens: 512 },
      safetySettings: [{ category: "HARM_CATEGORY_DEFAULT", threshold: "BLOCK_NONE" }],
    }),
  }),

  geminiStream: io({
    Style: "gemini",
    OfStringArray: [
      JSON.stringify({
        modelVersion: "gemini-2.0-flash-001",
        candidates: [
          {
            content: {
              role: "model",
              parts: [
                { text: "好的", thought: true },
                { text: "我来查" },
              ],
            },
          },
        ],
      }),
      JSON.stringify({
        candidates: [
          {
            content: {
              parts: [
                { functionCall: { name: "get_weather", args: { city: "上海" } } },
                { functionResponse: { name: "get_weather", response: { temp: 31 } } },
                { inlineData: { mimeType: "image/png", data: "D" } },
                { text: "多云" },
                { custom_part: { v: 1 } },
              ],
            },
            finishReason: "STOP",
          },
        ],
      }),
      JSON.stringify({ usageMetadata: { promptTokenCount: 5, candidatesTokenCount: 3 } }),
    ],
  }),

  geminiNonStream: io({
    Style: "gemini",
    OfString: JSON.stringify({
      modelVersion: "gemini-2.0-flash-001",
      candidates: [
        {
          content: {
            role: "model",
            parts: [
              { text: "整包正文" },
              { text: "整包思考", thought: true },
              { functionCall: { name: "get_weather", args: { city: "广州" } } },
              { functionResponse: { name: "get_weather", response: { temp: 28 } } },
            ],
          },
          finishReason: "STOP",
          index: 0,
        },
      ],
      usageMetadata: { promptTokenCount: 1, candidatesTokenCount: 2 },
    }),
  }),

  // ── 降级路径 ────────────────────────────────────────────────────────────

  /** Style 缺失：靠形状嗅探决定协议。 */
  sniffed: {
    openai: io({ Style: null, OfString: JSON.stringify({ choices: [{ message: { content: "嗅探到 openai" } }] }) }),
    openaiRes: io({
      Style: "weird-value",
      OfString: JSON.stringify({
        output: [
          {
            type: "message",
            content: [
              { type: "output_text", text: "嗅探到 responses" },
            ],
          },
        ],
        status: "completed",
      }),
    }),
    anthropic: io({ Style: "??? ", OfString: JSON.stringify({ content: [{ type: "text", text: "嗅探到 anthropic" }] }) }),
    gemini: io({ Style: 0, OfString: JSON.stringify({ candidates: [{ content: { parts: [{ text: "嗅探到 gemini" }] } }] }) }),
  },

  /** 响应体不是 JSON：按原文展示，不能崩。 */
  nonJsonResponse: io({
    Style: "openai",
    OfString: "这不是 JSON",
  }),

  /** 流式里混入坏片段：计 malformed，其余照常。 */
  mixedMalformed: io({
    Style: "openai",
    OfStringArray: [
      "{坏掉的片段",
      JSON.stringify({ choices: [{ index: 0, delta: { content: "好的" } }] }),
      "   ",
      "123",
    ],
  }),

  /** 请求体不是 JSON。 */
  badRequest: io({ Style: "openai", Input: "完全不是 json" }),

  /** 请求体是 JSON 但不是对象。 */
  requestArray: io({ Style: "openai", Input: "[1,2,3]" }),

  /** 什么都没有。 */
  empty: io({ Style: "openai" }),
}
