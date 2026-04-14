import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import Loading from "@/components/loading";
import { useTheme } from "@/components/theme-provider";
import { getChatIO, type ChatIO } from "@/lib/api";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { duotoneDark } from "react-syntax-highlighter/dist/esm/styles/prism";
import { duotoneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import { toast } from "sonner";
import { Copy, Check, ChevronRight, ChevronDown, Code } from "lucide-react";

type SyntaxStyle = typeof duotoneLight;

// i18n key mapping for field labels
const fieldLabelKeys: Record<string, string> = {
  model: "chat_io.model",
  messages: "chat_io.messages",
  tools: "chat_io.tools",
  params: "chat_io.parameters",
  instructions: "chat_io.instructions",
  input: "chat_io.input_messages",
  system: "chat_io.system",
  systemInstruction: "chat_io.system_instruction",
  contents: "chat_io.contents",
  generationConfig: "chat_io.generation_config",
};

// ── Copy Button ──

function CopyButton({ text }: { text: string }) {
  const { t } = useTranslation("logs");
  const [copied, setCopied] = useState(false);
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success(t("chat_io.copied"));
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error(t("chat_io.copy_failed"));
    }
  };
  return (
    <Button variant="ghost" size="sm" onClick={handleCopy} className="h-7 w-7 p-0">
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  );
}

// ── Collapsible Section ──

interface CollapsibleSectionProps {
  title: string;
  subtitle?: string;
  badge?: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}

function CollapsibleSection({ title, subtitle, badge, defaultOpen = false, children }: CollapsibleSectionProps) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="border rounded-lg overflow-hidden">
      <button
        type="button"
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/50 transition-colors"
        onClick={() => setOpen(!open)}
      >
        {open ? <ChevronDown className="h-4 w-4 shrink-0" /> : <ChevronRight className="h-4 w-4 shrink-0" />}
        <span className="font-medium text-sm">{title}</span>
        {badge && <Badge variant="secondary" className="text-xs">{badge}</Badge>}
        {subtitle && <span className="text-xs text-muted-foreground truncate ml-auto">{subtitle}</span>}
      </button>
      {open && <div className="border-t px-3 py-2">{children}</div>}
    </div>
  );
}

// ── JSON Viewer ──

function JsonViewer({ data, syntaxStyle }: { data: unknown; syntaxStyle: SyntaxStyle }) {
  const text = typeof data === "string" ? data : JSON.stringify(data, null, 2);
  return (
    <div className="relative w-full overflow-x-auto rounded-md bg-muted/50 font-mono text-sm leading-6">
      <div className="absolute top-1 right-1 z-10">
        <CopyButton text={text} />
      </div>
      <SyntaxHighlighter
        language="json"
        style={syntaxStyle}
        customStyle={{
          margin: 0,
          background: "transparent",
          padding: "0.75rem",
          fontSize: "0.8rem",
          lineHeight: "1.4rem",
          whiteSpace: "pre",
        }}
      >
        {text}
      </SyntaxHighlighter>
    </div>
  );
}

// ── Raw JSON Toggle ──

function useRawJsonToggle(raw: string) {
  const [show, setShow] = useState(false);
  const toggle = () => setShow(v => !v);
  return { show, toggle, raw };
}

function RawJsonButton({ show, toggle }: { show: boolean; toggle: () => void }) {
  const { t } = useTranslation("logs");
  return (
    <Button variant="ghost" size="sm" className="gap-1.5 text-xs h-7" onClick={toggle}>
      <Code className="h-3.5 w-3.5" />
      {show ? t("chat_io.hide_raw_json") : t("chat_io.view_raw_json")}
    </Button>
  );
}

function RawJsonPanel({ raw, syntaxStyle }: { raw: string; syntaxStyle: SyntaxStyle }) {
  const formatted = useMemo(() => {
    try {
      return JSON.stringify(JSON.parse(raw), null, 2);
    } catch {
      return raw;
    }
  }, [raw]);

  return (
    <div className="relative w-full overflow-x-auto rounded-md border bg-muted/50 font-mono text-sm leading-6">
      <div className="absolute top-1 right-1 z-10">
        <CopyButton text={raw} />
      </div>
      <SyntaxHighlighter
        language="json"
        style={syntaxStyle}
        customStyle={{
          margin: 0,
          background: "transparent",
          padding: "0.75rem",
          fontSize: "0.8rem",
          lineHeight: "1.4rem",
          whiteSpace: "pre",
        }}
      >
        {formatted}
      </SyntaxHighlighter>
    </div>
  );
}

// ── Message Bubble ──

function MessageBubble({ role, content, syntaxStyle }: { role: string; content: unknown; syntaxStyle: SyntaxStyle }) {
  const [open, setOpen] = useState(false);
  const roleBadgeColor: Record<string, string> = {
    system: "bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200",
    user: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
    assistant: "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
    tool: "bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200",
    developer: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200",
  };

  const textContent = extractTextContent(content);
  const isComplexContent = typeof content !== "string" || content.startsWith("[") || content.startsWith("{");

  return (
    <div className="border rounded-lg overflow-hidden">
      <button
        type="button"
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/50 transition-colors"
        onClick={() => setOpen(!open)}
      >
        {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
        <span className={`text-xs font-medium px-2 py-0.5 rounded-full shrink-0 ${roleBadgeColor[role] ?? "bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200"}`}>
          {role}
        </span>
        {textContent && <span className="text-xs text-muted-foreground truncate">{truncate(textContent, 100)}</span>}
      </button>
      {open && (
        <div className="border-t px-3 py-2">
          {isComplexContent ? (
            <JsonViewer data={content} syntaxStyle={syntaxStyle} />
          ) : (
            <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words">{textContent}</pre>
          )}
        </div>
      )}
    </div>
  );
}

function extractTextContent(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((c) => {
        if (typeof c === "string") return c;
        if (c?.type === "text" && c?.text) return c.text;
        if (c?.type === "tool_use") return `[tool_use: ${c.name ?? ""}]`;
        if (c?.type === "tool_result") return `[tool_result: ${c.tool_use_id ?? ""}]`;
        if (c?.type === "image_url" || c?.type === "image") return `[image]`;
        return JSON.stringify(c);
      })
      .join("\n");
  }
  if (content && typeof content === "object") return JSON.stringify(content);
  return String(content ?? "");
}

// ── Input Parsers ──

interface InputField {
  key: string;
  labelKey: string;
  badge?: string;
  subtitle?: string;
  value: unknown;
}

function parseInputFields(style: string, input: Record<string, unknown>): InputField[] {
  const fields: InputField[] = [];

  switch (style) {
    case "openai": {
      if (input.model) fields.push({ key: "model", labelKey: "model", value: input.model });
      const messages = input.messages as Array<Record<string, unknown>> | undefined;
      if (messages) {
        fields.push({ key: "messages", labelKey: "messages", badge: `${messages.length}`, value: messages });
      }
      if (input.tools) {
        const tools = input.tools as Array<Record<string, unknown>>;
        fields.push({ key: "tools", labelKey: "tools", badge: `${tools.length}`, value: input.tools });
      }
      const paramKeys = Object.keys(input).filter(k => !["model", "messages", "tools"].includes(k));
      if (paramKeys.length > 0) {
        const params = Object.fromEntries(paramKeys.map(k => [k, input[k]]));
        fields.push({ key: "params", labelKey: "params", badge: `${paramKeys.length}`, value: params });
      }
      break;
    }
    case "openai-res": {
      if (input.model) fields.push({ key: "model", labelKey: "model", value: input.model });
      if (input.instructions) fields.push({ key: "instructions", labelKey: "instructions", subtitle: truncate(String(input.instructions), 60), value: input.instructions });
      const resInput = input.input as Array<Record<string, unknown>> | undefined;
      if (resInput) {
        fields.push({ key: "input", labelKey: "input", badge: `${Array.isArray(resInput) ? resInput.length : 1}`, value: resInput });
      }
      if (input.tools) {
        const tools = input.tools as Array<Record<string, unknown>>;
        fields.push({ key: "tools", labelKey: "tools", badge: `${tools.length}`, value: input.tools });
      }
      const resParamKeys = Object.keys(input).filter(k => !["model", "instructions", "input", "tools"].includes(k));
      if (resParamKeys.length > 0) {
        const params = Object.fromEntries(resParamKeys.map(k => [k, input[k]]));
        fields.push({ key: "params", labelKey: "params", badge: `${resParamKeys.length}`, value: params });
      }
      break;
    }
    case "anthropic": {
      if (input.model) fields.push({ key: "model", labelKey: "model", value: input.model });
      if (input.system) {
        const sysText = typeof input.system === "string" ? input.system : extractTextContent(input.system);
        fields.push({ key: "system", labelKey: "system", subtitle: truncate(sysText, 60), value: input.system });
      }
      const antMessages = input.messages as Array<Record<string, unknown>> | undefined;
      if (antMessages) {
        fields.push({ key: "messages", labelKey: "messages", badge: `${antMessages.length}`, value: antMessages });
      }
      if (input.tools) {
        const tools = input.tools as Array<Record<string, unknown>>;
        fields.push({ key: "tools", labelKey: "tools", badge: `${tools.length}`, value: input.tools });
      }
      const antParamKeys = Object.keys(input).filter(k => !["model", "system", "messages", "tools"].includes(k));
      if (antParamKeys.length > 0) {
        const params = Object.fromEntries(antParamKeys.map(k => [k, input[k]]));
        fields.push({ key: "params", labelKey: "params", badge: `${antParamKeys.length}`, value: params });
      }
      break;
    }
    case "gemini": {
      if (input.systemInstruction) {
        const sysText = extractGeminiSystemText(input.systemInstruction);
        fields.push({ key: "systemInstruction", labelKey: "systemInstruction", subtitle: truncate(sysText, 60), value: input.systemInstruction });
      }
      const contents = input.contents as Array<Record<string, unknown>> | undefined;
      if (contents) {
        fields.push({ key: "contents", labelKey: "contents", badge: `${contents.length}`, value: contents });
      }
      if (input.tools) {
        const tools = input.tools as Array<Record<string, unknown>>;
        fields.push({ key: "tools", labelKey: "tools", badge: `${tools.length}`, value: input.tools });
      }
      if (input.generationConfig) {
        fields.push({ key: "generationConfig", labelKey: "generationConfig", value: input.generationConfig });
      }
      const gemParamKeys = Object.keys(input).filter(k => !["systemInstruction", "contents", "tools", "generationConfig"].includes(k));
      if (gemParamKeys.length > 0) {
        const params = Object.fromEntries(gemParamKeys.map(k => [k, input[k]]));
        fields.push({ key: "params", labelKey: "params", badge: `${gemParamKeys.length}`, value: params });
      }
      break;
    }
    default: {
      for (const [k, v] of Object.entries(input)) {
        fields.push({ key: k, labelKey: k, value: v });
      }
    }
  }
  return fields;
}

function extractGeminiSystemText(sys: unknown): string {
  if (typeof sys === "string") return sys;
  if (sys && typeof sys === "object" && "parts" in (sys as Record<string, unknown>)) {
    const parts = (sys as Record<string, unknown>).parts;
    if (Array.isArray(parts)) {
      return parts.map(p => p?.text ?? JSON.stringify(p)).join("\n");
    }
  }
  return JSON.stringify(sys);
}

function truncate(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max) + "...";
}

// ── Output Parsers ──

interface ParsedOutput {
  type: "streaming" | "complete";
  content: string;
  model?: string;
  usage?: Record<string, unknown>;
  finishReason?: string;
  raw: unknown;
}

function parseOutput(style: string, ofString: string | null | undefined, ofStringArray: string[] | null | undefined): ParsedOutput {
  if (ofString && (!ofStringArray || ofStringArray.length === 0)) {
    try {
      const parsed = JSON.parse(ofString);
      return {
        type: "complete",
        content: extractResponseContent(style, parsed),
        model: parsed.model || parsed.modelVersion,
        usage: extractUsage(style, parsed),
        finishReason: extractFinishReason(style, parsed),
        raw: parsed,
      };
    } catch {
      return { type: "complete", content: ofString, raw: ofString };
    }
  }

  if (ofStringArray && ofStringArray.length > 0) {
    const merged = mergeStreamChunks(style, ofStringArray);
    return { type: "streaming", ...merged };
  }

  return { type: "complete", content: "", raw: null };
}

function extractResponseContent(style: string, data: Record<string, unknown>): string {
  switch (style) {
    case "openai": {
      const choices = data.choices as Array<Record<string, unknown>> | undefined;
      if (choices?.[0]) {
        const msg = choices[0].message as Record<string, unknown> | undefined;
        if (msg) return extractTextContent(msg.content ?? msg.tool_calls ?? "");
      }
      return "";
    }
    case "openai-res": {
      const output = data.output as Array<Record<string, unknown>> | undefined;
      if (output) {
        return output
          .map(item => {
            if (item.type === "message" && item.content) {
              return (item.content as Array<Record<string, unknown>>)
                .map(c => c.text ?? JSON.stringify(c))
                .join("\n");
            }
            return JSON.stringify(item);
          })
          .join("\n");
      }
      return "";
    }
    case "anthropic": {
      const content = data.content as Array<Record<string, unknown>> | undefined;
      if (content) {
        return content.map(c => c.text ?? JSON.stringify(c)).join("\n");
      }
      return "";
    }
    case "gemini": {
      const candidates = data.candidates as Array<Record<string, unknown>> | undefined;
      if (candidates?.[0]) {
        const gemContent = candidates[0].content as Record<string, unknown> | undefined;
        if (gemContent?.parts) {
          return (gemContent.parts as Array<Record<string, unknown>>).map(p => p.text ?? JSON.stringify(p)).join("\n");
        }
      }
      return "";
    }
    default:
      return JSON.stringify(data);
  }
}

function extractUsage(style: string, data: Record<string, unknown>): Record<string, unknown> | undefined {
  switch (style) {
    case "openai":
    case "openai-res":
    case "anthropic":
      return data.usage as Record<string, unknown> | undefined;
    case "gemini":
      return data.usageMetadata as Record<string, unknown> | undefined;
    default:
      return undefined;
  }
}

function extractFinishReason(style: string, data: Record<string, unknown>): string | undefined {
  switch (style) {
    case "openai": {
      const choices = data.choices as Array<Record<string, unknown>> | undefined;
      return choices?.[0]?.finish_reason as string | undefined;
    }
    case "openai-res":
      return data.status as string | undefined;
    case "anthropic":
      return data.stop_reason as string | undefined;
    case "gemini": {
      const candidates = data.candidates as Array<Record<string, unknown>> | undefined;
      return candidates?.[0]?.finishReason as string | undefined;
    }
    default:
      return undefined;
  }
}

function mergeStreamChunks(style: string, chunks: string[]): { content: string; model?: string; usage?: Record<string, unknown>; finishReason?: string; raw: unknown } {
  const parsedChunks: Record<string, unknown>[] = [];
  let content = "";
  let model: string | undefined;
  let usage: Record<string, unknown> | undefined;
  let finishReason: string | undefined;

  for (const chunk of chunks) {
    try {
      const parsed = JSON.parse(chunk);
      parsedChunks.push(parsed);

      switch (style) {
        case "openai": {
          if (!model && parsed.model) model = parsed.model;
          const delta = parsed.choices?.[0]?.delta;
          if (delta?.content) content += delta.content;
          if (delta?.tool_calls) content += JSON.stringify(delta.tool_calls);
          if (parsed.choices?.[0]?.finish_reason) finishReason = parsed.choices[0].finish_reason;
          if (parsed.usage?.total_tokens) usage = parsed.usage;
          break;
        }
        case "openai-res": {
          if (!model && parsed.response?.model) model = parsed.response.model;
          if (parsed.type === "response.output_text.delta" && parsed.delta) content += parsed.delta;
          if (parsed.type === "response.completed") {
            finishReason = parsed.response?.status;
            usage = parsed.response?.usage;
          }
          break;
        }
        case "anthropic": {
          if (!model && parsed.message?.model) model = parsed.message.model;
          if (parsed.type === "content_block_delta" && parsed.delta?.text) content += parsed.delta.text;
          if (parsed.type === "message_delta") {
            finishReason = parsed.delta?.stop_reason;
            if (parsed.usage) usage = parsed.usage;
          }
          break;
        }
        case "gemini": {
          if (!model && parsed.modelVersion) model = parsed.modelVersion;
          const parts = parsed.candidates?.[0]?.content?.parts;
          if (parts) {
            for (const p of parts) {
              if (p.text) content += p.text;
            }
          }
          if (parsed.candidates?.[0]?.finishReason) finishReason = parsed.candidates[0].finishReason;
          if (parsed.usageMetadata?.totalTokenCount) usage = parsed.usageMetadata;
          break;
        }
        default: {
          content += chunk + "\n";
        }
      }
    } catch {
      content += chunk + "\n";
      parsedChunks.push({ _raw: chunk });
    }
  }

  return { content, model, usage, finishReason, raw: parsedChunks };
}

// ── Input Display ──

function InputSection({ raw, style, syntaxStyle }: { raw: string; style: string; syntaxStyle: SyntaxStyle }) {
  const { t } = useTranslation("logs");
  const rawToggle = useRawJsonToggle(raw);
  const parsed = useMemo(() => {
    try {
      return JSON.parse(raw) as Record<string, unknown>;
    } catch {
      return null;
    }
  }, [raw]);

  const fields = useMemo(() => {
    if (!parsed) return [];
    return parseInputFields(style, parsed);
  }, [parsed, style]);

  if (!parsed) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-3">
          <CardTitle className="text-base">{t("chat_io.request_input")}</CardTitle>
          <RawJsonButton show={rawToggle.show} toggle={rawToggle.toggle} />
        </CardHeader>
        <CardContent className="space-y-2">
          {rawToggle.show ? (
            <RawJsonPanel raw={raw} syntaxStyle={syntaxStyle} />
          ) : (
            <pre className="whitespace-pre-wrap text-sm bg-muted/50 rounded-md p-3 border">{raw}</pre>
          )}
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-3">
        <CardTitle className="text-base">{t("chat_io.request_input")}</CardTitle>
        <RawJsonButton show={rawToggle.show} toggle={rawToggle.toggle} />
      </CardHeader>
      <CardContent className="space-y-2">
        {rawToggle.show ? (
          <RawJsonPanel raw={raw} syntaxStyle={syntaxStyle} />
        ) : (
          fields.map(field => (
            <CollapsibleSection
              key={field.key}
              title={fieldLabelKeys[field.labelKey] ? t(fieldLabelKeys[field.labelKey] as never) : field.labelKey}
              badge={field.badge}
              subtitle={field.subtitle}
              defaultOpen={false}
            >
              {field.key === "messages" || field.key === "input" ? (
                <MessagesView messages={field.value as Array<Record<string, unknown>>} syntaxStyle={syntaxStyle} />
              ) : field.key === "tools" ? (
                <ToolsView tools={field.value as unknown[]} style={style} syntaxStyle={syntaxStyle} />
              ) : field.key === "model" ? (
                <span className="text-sm font-mono">{String(field.value)}</span>
              ) : field.key === "system" || field.key === "instructions" || field.key === "systemInstruction" ? (
                typeof field.value === "string" ? (
                  <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words">{field.value}</pre>
                ) : (
                  <JsonViewer data={field.value} syntaxStyle={syntaxStyle} />
                )
              ) : (
                <JsonViewer data={field.value} syntaxStyle={syntaxStyle} />
              )}
            </CollapsibleSection>
          ))
        )}
      </CardContent>
    </Card>
  );
}

function MessagesView({ messages, syntaxStyle }: { messages: Array<Record<string, unknown>>; syntaxStyle: SyntaxStyle }) {
  if (!Array.isArray(messages)) {
    return <JsonViewer data={messages} syntaxStyle={syntaxStyle} />;
  }
  return (
    <div className="space-y-2">
      {messages.map((msg, i) => (
        <MessageBubble key={i} role={String(msg.role ?? msg.type ?? "unknown")} content={msg.content ?? msg} syntaxStyle={syntaxStyle} />
      ))}
    </div>
  );
}

// ── Tools Display ──

interface ToolInfo {
  name: string;
  description?: string;
  parameters?: unknown;
}

function extractTools(style: string, tools: unknown[]): ToolInfo[] {
  const result: ToolInfo[] = [];
  for (const tool of tools) {
    if (typeof tool !== "object" || tool === null) continue;
    const t = tool as Record<string, unknown>;

    switch (style) {
      case "openai":
      case "openai-res": {
        // {type: "function", function: {name, description, parameters}}
        const fn = t.function as Record<string, unknown> | undefined;
        if (fn) {
          result.push({
            name: String(fn.name ?? ""),
            description: fn.description ? String(fn.description) : undefined,
            parameters: fn.parameters,
          });
        } else if (t.name) {
          result.push({
            name: String(t.name),
            description: t.description ? String(t.description) : undefined,
            parameters: t.parameters ?? t.input_schema,
          });
        }
        break;
      }
      case "anthropic": {
        // {name, description, input_schema}
        result.push({
          name: String(t.name ?? ""),
          description: t.description ? String(t.description) : undefined,
          parameters: t.input_schema,
        });
        break;
      }
      case "gemini": {
        // {functionDeclarations: [{name, description, parameters}]}
        const decls = t.functionDeclarations as Array<Record<string, unknown>> | undefined;
        if (decls) {
          for (const d of decls) {
            result.push({
              name: String(d.name ?? ""),
              description: d.description ? String(d.description) : undefined,
              parameters: d.parameters,
            });
          }
        } else if (t.name) {
          result.push({
            name: String(t.name),
            description: t.description ? String(t.description) : undefined,
            parameters: t.parameters,
          });
        }
        break;
      }
      default: {
        if (t.name) {
          result.push({
            name: String(t.name),
            description: t.description ? String(t.description) : undefined,
            parameters: t.parameters ?? t.input_schema,
          });
        }
      }
    }
  }
  return result;
}

function ToolsView({ tools, style, syntaxStyle }: { tools: unknown[]; style: string; syntaxStyle: SyntaxStyle }) {
  const parsed = useMemo(() => extractTools(style, tools), [style, tools]);

  if (parsed.length === 0) {
    return <JsonViewer data={tools} syntaxStyle={syntaxStyle} />;
  }

  return (
    <div className="space-y-2">
      {parsed.map((tool, i) => (
        <ToolItem key={i} tool={tool} syntaxStyle={syntaxStyle} />
      ))}
    </div>
  );
}

function ToolItem({ tool, syntaxStyle }: { tool: ToolInfo; syntaxStyle: SyntaxStyle }) {
  const [open, setOpen] = useState(false);

  return (
    <div className="border rounded-lg overflow-hidden">
      <button
        type="button"
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/50 transition-colors"
        onClick={() => setOpen(!open)}
      >
        {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
        <span className="text-xs font-medium px-2 py-0.5 rounded-full shrink-0 bg-indigo-100 text-indigo-800 dark:bg-indigo-900 dark:text-indigo-200">
          function
        </span>
        <span className="text-sm font-mono font-medium">{tool.name}</span>
        {tool.description && (
          <span className="text-xs text-muted-foreground truncate ml-auto">{truncate(tool.description, 60)}</span>
        )}
      </button>
      {open && (
        <div className="border-t px-3 py-2 space-y-2">
          {tool.description && (
            <p className="text-sm text-muted-foreground">{tool.description}</p>
          )}
          {tool.parameters != null ? (
            <div>
              <p className="text-xs font-medium text-muted-foreground mb-1">Parameters</p>
              <JsonViewer data={tool.parameters} syntaxStyle={syntaxStyle} />
            </div>
          ) : null}
          {!tool.description && tool.parameters == null ? (
            <p className="text-xs text-muted-foreground">No additional details</p>
          ) : null}
        </div>
      )}
    </div>
  );
}

// ── Output Display ──

function OutputSection({ chatIO, style, syntaxStyle }: { chatIO: ChatIO; style: string; syntaxStyle: SyntaxStyle }) {
  const { t } = useTranslation("logs");
  const parsed = useMemo(
    () => parseOutput(style, chatIO.OfString, chatIO.OfStringArray),
    [style, chatIO.OfString, chatIO.OfStringArray]
  );

  const rawOutput = useMemo(() => {
    if (chatIO.OfString) return chatIO.OfString;
    if (chatIO.OfStringArray && chatIO.OfStringArray.length > 0) return JSON.stringify(chatIO.OfStringArray, null, 2);
    return "";
  }, [chatIO.OfString, chatIO.OfStringArray]);

  const rawToggle = useRawJsonToggle(rawOutput);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-3">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">{t("chat_io.response_output")}</CardTitle>
          <Badge variant={parsed.type === "streaming" ? "default" : "secondary"} className="text-xs">
            {parsed.type === "streaming" ? t("chat_io.streaming") : t("chat_io.complete")}
          </Badge>
          {parsed.type === "streaming" && chatIO.OfStringArray && (
            <span className="text-xs text-muted-foreground">{t("chat_io.chunks_count", { count: chatIO.OfStringArray.length })}</span>
          )}
        </div>
        {rawOutput && <RawJsonButton show={rawToggle.show} toggle={rawToggle.toggle} />}
      </CardHeader>
      <CardContent className="space-y-3">
        {rawToggle.show ? (
          <RawJsonPanel raw={rawOutput} syntaxStyle={syntaxStyle} />
        ) : (
          <>
            {(parsed.model || parsed.finishReason) && (
              <div className="flex items-center gap-3 text-sm">
                {parsed.model && (
                  <span className="text-muted-foreground">
                    {t("chat_io.model")}: <span className="font-mono">{parsed.model}</span>
                  </span>
                )}
                {parsed.finishReason && (
                  <span className="text-muted-foreground">
                    {t("chat_io.finish")}: <Badge variant="outline" className="text-xs">{parsed.finishReason}</Badge>
                  </span>
                )}
              </div>
            )}

            <CollapsibleSection title={t("chat_io.content")} defaultOpen={false} subtitle={truncate(parsed.content, 80)}>
              <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words">{parsed.content || t("chat_io.empty")}</pre>
            </CollapsibleSection>

            {parsed.usage && (
              <CollapsibleSection title={t("chat_io.usage")}>
                <JsonViewer data={parsed.usage} syntaxStyle={syntaxStyle} />
              </CollapsibleSection>
            )}

            {parsed.type === "complete" && parsed.raw != null && typeof parsed.raw === "object" ? (
              <CollapsibleSection title={t("chat_io.full_response")}>
                <JsonViewer data={parsed.raw} syntaxStyle={syntaxStyle} />
              </CollapsibleSection>
            ) : null}

            {parsed.type === "streaming" && chatIO.OfStringArray && chatIO.OfStringArray.length > 0 && (
              <CollapsibleSection title={t("chat_io.stream_chunks")} badge={`${chatIO.OfStringArray.length}`}>
                <div className="space-y-1 max-h-96 overflow-y-auto">
                  {chatIO.OfStringArray.map((chunk, i) => (
                    <div key={i} className="text-xs font-mono bg-muted/30 rounded px-2 py-1 border break-all">
                      <span className="text-muted-foreground mr-2">#{i + 1}</span>
                      {chunk}
                    </div>
                  ))}
                </div>
              </CollapsibleSection>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// ── Theme hook ──

function useSyntaxStyle(): SyntaxStyle {
  const { theme } = useTheme();

  const defaultPrefersDark = useMemo(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  }, []);

  const [isDark, setIsDark] = useState<boolean>(() => {
    if (theme === "system") return defaultPrefersDark;
    return theme === "dark";
  });

  useEffect(() => {
    if (theme === "system") {
      if (typeof window === "undefined") {
        setIsDark(false);
        return;
      }
      const media = window.matchMedia("(prefers-color-scheme: dark)");
      const listener = (event: MediaQueryListEvent) => setIsDark(event.matches);
      setIsDark(media.matches);
      media.addEventListener("change", listener);
      return () => media.removeEventListener("change", listener);
    }
    setIsDark(theme === "dark");
    return undefined;
  }, [theme]);

  return isDark ? duotoneDark : duotoneLight;
}

// ── Page ──

export default function LogChatPage() {
  const { t } = useTranslation(["logs", "common"]);
  const { logId } = useParams<{ logId: string }>();
  const navigate = useNavigate();
  const [chatIO, setChatIO] = useState<ChatIO | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadErrorMessage, setLoadErrorMessage] = useState<string | null>(null);
  const syntaxStyle = useSyntaxStyle();
  const style = chatIO?.Style ?? "openai";

  useEffect(() => {
    if (!logId) {
      const message = t("chat_io.missing_log_id");
      toast.error(message);
      setLoadErrorMessage(message);
      setLoading(false);
      return;
    }

    const parsedId = Number(logId);
    if (Number.isNaN(parsedId)) {
      const message = t("chat_io.invalid_log_id");
      toast.error(message);
      setLoadErrorMessage(message);
      setLoading(false);
      return;
    }

    const fetchChatIO = async () => {
      try {
        const data = await getChatIO(parsedId);
        setChatIO(data);
        setLoadErrorMessage(null);
      } catch (fetchError) {
        let message = t("chat_io.fetch_failed");
        if (fetchError instanceof Error) {
          if (fetchError.message.includes("chat io not found")) {
            message = t("chat_io.io_not_found");
          } else {
            message = fetchError.message;
          }
        }
        toast.error(message);
        setLoadErrorMessage(message);
      } finally {
        setLoading(false);
      }
    };

    fetchChatIO();
  }, [logId, t]);

  if (loading) {
    return <Loading message={t("chat_io.loading")} />;
  }

  return (
    <div className="space-y-6 h-full overflow-y-auto overflow-x-hidden">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-bold">{t("detail.title", { id: logId })}</h1>
          <Badge variant="outline" className="text-xs font-mono">{style}</Badge>
        </div>
        <Button variant="outline" onClick={() => navigate(-1)}>
          {t("common:actions.back")}
        </Button>
      </div>

      {loadErrorMessage && (
        <Card>
          <CardHeader>
            <CardTitle>{t("chat_io.load_failed")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            <p className="text-sm text-muted-foreground">{loadErrorMessage}</p>
            <Button onClick={() => navigate(-1)}>{t("common:actions.back")}</Button>
          </CardContent>
        </Card>
      )}

      {!loadErrorMessage && chatIO && (
        <div className="space-y-6">
          <InputSection raw={chatIO.Input} style={style} syntaxStyle={syntaxStyle} />
          <OutputSection chatIO={chatIO} style={style} syntaxStyle={syntaxStyle} />
        </div>
      )}
    </div>
  );
}
