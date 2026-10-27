import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router-dom";
import { JsonTree } from "@/components/json-tree";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import Loading from "@/components/loading";
import { getChatIO, type ChatIO } from "@/lib/api";
import { parseRequest, parseResponse, sniffProtocol, sniffRawFormat, sniffResponseProtocol, type RawFormat } from "@/lib/chat-io/parse";
import type { ContentPart, Message, ParsedRequest, ParsedResponse, Protocol, ToolCall, ToolDeclaration } from "@/lib/chat-io/types";
import { KNOWN_PROTOCOLS } from "@/lib/chat-io/types";
import { parseArgs } from "@/lib/chat-io/util";
import { copyToClipboard, cn } from "@/lib/utils";
import { toast } from "sonner";
import { Copy, Check, ChevronRight, ChevronDown, Code } from "lucide-react";
import { seriesVar } from "@/lib/palette";

// ── Style coercion ──

/** 后端 Style 字段是自由字符串；只认真正的四种协议，其余按嗅探处理。 */
function protocolOf(style: unknown): Protocol | undefined {
  return typeof style === "string" && (KNOWN_PROTOCOLS as readonly string[]).includes(style)
    ? (style as Protocol)
    : undefined;
}

/**
 * 响应嗅探失败时的兜底：请求体本身能认出聊天协议，就仍按聊天面板渲染。
 * 典型场景是透传接口（图片/音频/SSE 转储），响应没有可识别的 JSON 形状，
 * 但请求明明是 chat/completions —— 整页退化成两段原始 JSON 反而更难读。
 */
function inputLooksLikeChat(raw: string | null | undefined): boolean {
  if (!raw) return false;
  try {
    const body: unknown = JSON.parse(raw);
    if (typeof body !== "object" || body === null || Array.isArray(body)) return false;
    return sniffProtocol(body as Record<string, unknown>) !== null;
  } catch {
    return false;
  }
}

function truncate(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max) + "...";
}

// ── Copy Button ──

function CopyButton({ text }: { text: string }) {
  const { t } = useTranslation("logs");
  const [copied, setCopied] = useState(false);
  const handleCopy = async () => {
    try {
      await copyToClipboard(text);
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

// ── Role / kind badge ──

/**
 * 角色 / 块类型徽章。
 *
 * 用分类色板的固定槽位着色，并通过行内 style 取 CSS 变量——深浅两套主题
 * 自动切换，无需为每个角色各写一份 dark: 类。
 *
 * 颜色只是辅助：徽章上始终有文字（角色名 / tool_call / function），
 * 身份不依赖颜色传达，色盲读者同样可读。
 */
function KindBadge({ name, slot, variant = "solid" }: { name: string; slot: number; variant?: "solid" | "muted" }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full border px-2 py-0.5 text-xs font-medium",
        variant === "muted" && "bg-transparent"
      )}
      style={{
        color: seriesVar(slot),
        borderColor: `color-mix(in oklch, ${seriesVar(slot)} 40%, transparent)`,
        backgroundColor:
          variant === "solid" ? `color-mix(in oklch, ${seriesVar(slot)} 14%, transparent)` : undefined,
      }}
    >
      {name}
    </span>
  );
}

const ROLE_SLOT: Record<string, number> = {
  system: 6,
  user: 0,
  assistant: 5,
  tool: 1,
  developer: 3,
};

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

/**
 * JSON 视图。
 *
 * 改用自绘的 JsonTree 替代语法高亮库：本页内容几乎全是 JSON，而为了染色
 * 要额外加载约 640KB 的样式表。JsonTree 体积归零，且多了可折叠与按节点
 * 定位的能力——大请求体不必一次性铺开。
 */
function JsonViewer({ data }: { data: unknown }) {
  return <JsonTree data={data} />;
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

function RawJsonPanel({ raw }: { raw: string }) {
  const formatted = useMemo(() => {
    try {
      return JSON.stringify(JSON.parse(raw), null, 2);
    } catch {
      return raw;
    }
  }, [raw]);

  // 原始面板刻意**不折叠**：它的用途就是"整段可框选复制"，
  // 树形视图会让选择跨层级变得困难。格式化失败时原样展示。
  return (
    <div className="relative w-full overflow-x-auto rounded-md border border-border bg-muted/30">
      <div className="absolute top-2 right-2 z-10">
        <CopyButton text={raw} />
      </div>
      <pre className="p-3 font-mono text-xs leading-relaxed whitespace-pre">{formatted}</pre>
    </div>
  );
}

// ── Info row ──

function InfoRow({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("ml-1.5 break-all", mono && "font-mono")}>{value}</span>
    </div>
  );
}

// ── Tool call card ──

/**
 * 一个工具调用。名字 + id + 美化参数，parseError 时保留原文并显式标记。
 *
 * 老实现把 tool_calls 直接 JSON.stringify 塞进正文字符串，读者看到的是正文
 * 中间夹着一坨 JSON——那种行为在结构化模型里已经没有了。
 */
function ToolCallCard({ call }: { call: ToolCall }) {
  const { t } = useTranslation("logs");
  const parsedArgs = useMemo(() => parseArgs(call.arguments), [call.arguments]);
  const value = parsedArgs.value;
  const parseError = call.parseError || parsedArgs.error;

  return (
    <div className="border rounded-md p-2 space-y-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <KindBadge name={t("chat_io.function")} slot={7} variant="muted" />
        <span className="text-sm font-mono font-medium">{call.name || t("chat_io.unknown")}</span>
        {call.id && (
          <span className="text-xs text-muted-foreground font-mono" title={t("chat_io.call_id")}>
            {call.id}
          </span>
        )}
      </div>
      {parseError && (
        <p className="text-xs text-amber-600 dark:text-amber-400">{t("chat_io.parse_error")}</p>
      )}
      {value != null && value !== "" ? (
        <JsonViewer data={value} />
      ) : null}
    </div>
  );
}

// ── Tool result card ──

function ToolResultCard({ result }: { result: { callId: string; content: unknown; toolCall?: string } }) {
  const { t } = useTranslation("logs");
  return (
    <div className="border rounded-md p-2 space-y-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <KindBadge name={t("chat_io.tool_result")} slot={4} variant="muted" />
        {result.toolCall && (
          <span className="text-sm font-mono font-medium">{result.toolCall}</span>
        )}
        {result.callId && (
          <span className="text-xs text-muted-foreground font-mono" title={t("chat_io.call_id")}>
            {result.callId}
          </span>
        )}
        {!result.toolCall && !result.callId && (
          <span className="text-xs text-muted-foreground">{t("chat_io.unknown")}</span>
        )}
      </div>
      {result.content != null && result.content !== "" ? (
        <JsonViewer data={result.content} />
      ) : null}
    </div>
  );
}

// ── Thinking card ──

/**
 * 一段思考。
 *
 * `index` 只在请求侧传入：那里的思考块是消息里的一个内容块，一条消息可能有多
 * 段（Anthropic 的 thinking + redacted_thinking），序号按"第几段思考"数。响应
 * 侧不传——一次响应只有一段思考，分片在装配器里就拼好了（见 chat-io 的
 * ParsedResponse.thinking），给一个恒为 #1 的编号只会让人以为还有别的段。
 */
function ThinkingCard({ text, index }: { text: string; index?: number }) {
  const { t } = useTranslation("logs");
  return (
    <div className="border rounded-md">
      <div className="flex items-center gap-2 px-2 py-1.5 border-b bg-muted/30">
        <KindBadge name={t("chat_io.thinking")} slot={2} variant="muted" />
        {index !== undefined && <span className="text-xs text-muted-foreground">#{index + 1}</span>}
        <span className="text-xs text-muted-foreground ml-auto truncate max-w-[240px]">
          {truncate(text.replace(/\s+/g, " ").trim(), 80)}
        </span>
      </div>
      <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words px-2 py-1.5">{text}</pre>
    </div>
  );
}

// ── Content part view ──

/**
 * 一条消息里的一个内容块。四种协议被压平成同一个形状，渲染层只用这一份分发。
 *
 * `thinkingNo` 是这块思考在本条消息里是第几段（从 1 数起），只数思考块：
 * 按 parts 的下标算的话，消息里每多一段正文或一张图，思考的编号就跟着跳，
 * 而读者要的是"第几段思考"。
 */
function ContentPartView({ part, thinkingNo }: { part: ContentPart; thinkingNo?: number }) {
  const { t } = useTranslation("logs");

  switch (part.type) {
    case "text":
      return <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words">{part.text}</pre>;
    case "thinking":
      return <ThinkingCard text={part.text} index={thinkingNo === undefined ? undefined : thinkingNo - 1} />;
    case "image": {
      const url = part.url ?? part.data;
      return (
        <div className="border rounded-md p-2 space-y-1">
          <div className="flex items-center gap-2">
            <KindBadge name={part.label ?? t("chat_io.image")} slot={3} variant="muted" />
            {part.mimeType && (
              <span className="text-xs text-muted-foreground font-mono">{part.mimeType}</span>
            )}
          </div>
          {url ? (
            <img src={url} alt="" className="max-h-48 rounded border" />
          ) : (
            <p className="text-xs text-muted-foreground">{t("chat_io.empty")}</p>
          )}
        </div>
      );
    }
    case "audio": {
      return (
        <div className="border rounded-md p-2 space-y-1">
          <div className="flex items-center gap-2">
            <KindBadge name={part.label ?? t("chat_io.audio")} slot={3} variant="muted" />
            {part.mimeType && (
              <span className="text-xs text-muted-foreground font-mono">{part.mimeType}</span>
            )}
          </div>
          <p className="text-xs text-muted-foreground">{t("chat_io.empty")}</p>
        </div>
      );
    }
    case "tool_use":
      return <ToolCallCard call={part.call} />;
    case "tool_result":
      return <ToolResultCard result={{ callId: part.callId, content: part.content, toolCall: part.toolCall }} />;
    default: {
      const u = part as Extract<ContentPart, { type: "unknown" }>;
      return (
        <div className="border rounded-md p-2 space-y-1">
          <div className="flex items-center gap-2">
            <KindBadge name={u.name ?? t("chat_io.unknown")} slot={8} variant="muted" />
            {u.mimeType && (
              <span className="text-xs text-muted-foreground font-mono">{u.mimeType}</span>
            )}
          </div>
          {u.text ? (
            <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words">{u.text}</pre>
          ) : u.data != null ? (
            <JsonViewer data={u.data} />
          ) : null}
        </div>
      );
    }
  }
}

// ── Message view ──

function MessageView({ msg }: { msg: Message }) {
  const [open, setOpen] = useState(false);
  const role = msg.role;
  const textPreview = msg.parts
    .filter((p): p is Extract<ContentPart, { type: "text" }> => p.type === "text")
    .map((p) => p.text)
    .join(" ")
    .replace(/\s+/g, " ")
    .trim();
  const callNames = msg.toolCalls.map((c) => c.name || "?");
  const preview = textPreview || (callNames.length > 0 ? callNames.join(", ") : "");
  // 每个块的"第几段思考"：不是思考的块留 undefined，卡片就不显示编号。
  let thinkingSeen = 0;
  const thinkingNos = msg.parts.map((p) => (p.type === "thinking" ? ++thinkingSeen : undefined));

  return (
    <div className="border rounded-lg overflow-hidden">
      <button
        type="button"
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/50 transition-colors"
        onClick={() => setOpen(!open)}
      >
        {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
        <KindBadge name={role} slot={ROLE_SLOT[role] ?? 2} />
        {callNames.length > 0 && (
          <KindBadge name="tool_call" slot={7} variant="muted" />
        )}
        {preview && <span className="text-xs text-muted-foreground truncate">{truncate(preview, 100)}</span>}
      </button>
      {open && (
        <div className="border-t px-3 py-2 space-y-2">
          {msg.parts.length === 0 ? (
            <span className="text-xs text-muted-foreground">(empty)</span>
          ) : (
            msg.parts.map((part, i) => <ContentPartView key={i} part={part} thinkingNo={thinkingNos[i]} />)
          )}
        </div>
      )}
    </div>
  );
}

// ── Tool declaration view ──

function ToolDeclarationItem({ tool }: { tool: ToolDeclaration }) {
  const { t } = useTranslation("logs");
  const [open, setOpen] = useState(false);

  return (
    <div className="border rounded-lg overflow-hidden">
      <button
        type="button"
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/50 transition-colors"
        onClick={() => setOpen(!open)}
      >
        {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
        <KindBadge name="tool" slot={1} variant="muted" />
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
              <p className="text-xs font-medium text-muted-foreground mb-1">{t("chat_io.parameters")}</p>
              <JsonViewer data={tool.parameters} />
            </div>
          ) : null}
          {!tool.description && tool.parameters == null ? (
            <p className="text-xs text-muted-foreground">{t("chat_io.no_details")}</p>
          ) : null}
        </div>
      )}
    </div>
  );
}

// ── System / instructions view ──

/** system / instructions / systemInstruction 三种来源统一成字符串或结构化 JSON。 */
function SystemView({ value }: { value: unknown }) {
  if (typeof value === "string") {
    return <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words">{value}</pre>;
  }
  return <JsonViewer data={value} />;
}

// ── Usage table ──

/**
 * usage 各协议形状不同（OpenAI 的 prompt_tokens、Anthropic 的 input_tokens、
 * Gemini 的 promptTokenCount），这里不做统一——把对象当作有序键值对展示，
 * 原字段名就是最准确的解释。
 */
function UsageTable({ usage }: { usage: unknown }) {
  if (!usage || typeof usage !== "object" || Array.isArray(usage)) {
    return <JsonViewer data={usage} />;
  }
  const entries = Object.entries(usage as Record<string, unknown>).filter(
    ([, v]) => typeof v === "number" || typeof v === "string" || v === null
  );
  if (entries.length === 0) return <JsonViewer data={usage} />;

  return (
    <div className="border rounded-md overflow-hidden">
      <table className="w-full text-sm">
        <tbody>
          {entries.map(([k, v]) => (
            <tr key={k} className="border-b last:border-b-0">
              <td className="px-2 py-1 font-mono text-xs text-muted-foreground">{k}</td>
              <td className="px-2 py-1 font-mono text-right">{String(v)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ── Generic JSON Section ──

/** 优先用单段响应；流式响应是片段数组，整体序列化后交给 RawJsonPanel 美化。 */
function rawResponseText(ofString?: string | null, ofStringArray?: string[] | null): string {
  if (ofString) return ofString;
  if (ofStringArray && ofStringArray.length > 0) return JSON.stringify(ofStringArray, null, 2);
  return "";
}

interface GenericJsonSectionProps {
  title: string;
  hint: string;
  raw: string;
  /** 该侧原文的形态，由调用方各自嗅探——请求与响应互不影响。 */
  format: RawFormat;
  /** 该侧原始字节的编码判定（InputEncoding / OutputEncoding）。 */
  encoding?: string;
  /**
   * 响应是 SSE 分片时传片段数。分片在库里的形状是 []string，展示时被整体
   * 序列化成 JSON 数组，光看格式徽章会以为是一份普通 JSON 响应体；这个徽章
   * 把"这是流式抓下来的 N 段原始事件"讲明白。
   */
  streamingChunks?: number;
}

/**
 * 嗅探不出聊天协议时（典型是透传 embeddings / rerank / 图片等接口）的区块：
 * 不套聊天面板，直接展示原始 JSON，并且默认就是展开的——这类请求本来就没有
 * 消息、文本可言，再让用户先看一个"(空)"再去点"查看原始 JSON"只是添堵。
 *
 * 编码标注只对两种情况出声：gb18030（后端已解码，文本可读）与 binary（解不
 * 出来，非法字节已替换）。utf-8、空串和字段缺失都是常态，不挂徽章也不解释，
 * 否则每块都多一行噪音。
 */
function GenericJsonSection({ title, hint, raw, format, encoding, streamingChunks }: GenericJsonSectionProps) {
  const { t } = useTranslation("logs");
  const formatLabel: Record<RawFormat, string> = {
    json: t("chat_io.format_json"),
    sse: t("chat_io.format_sse"),
    text: t("chat_io.format_text"),
    empty: t("chat_io.format_empty"),
  };
  const encodingBadge =
    encoding === "gb18030"
      ? t("chat_io.encoding_gb18030")
      : encoding === "binary"
        ? t("chat_io.encoding_binary")
        : null;
  const encodingHint =
    encoding === "gb18030"
      ? t("chat_io.encoding_hint_gb18030")
      : encoding === "binary"
        ? t("chat_io.encoding_hint_binary")
        : null;
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-3">
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-base">{title}</CardTitle>
          <Badge variant="secondary" className="text-xs">{t("chat_io.generic_json")}</Badge>
          <Badge variant="outline" className="text-xs">{formatLabel[format]}</Badge>
          {(streamingChunks ?? 0) > 0 && (
            <Badge variant="secondary" className="text-xs">
              {t("chat_io.streaming")} · {t("chat_io.chunks_count", { count: streamingChunks })}
            </Badge>
          )}
          {encodingBadge && (
            <Badge variant={encoding === "binary" ? "destructive" : "secondary"} className="text-xs">
              {encodingBadge}
            </Badge>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-2">
        <p className="text-xs text-muted-foreground">{hint}</p>
        {encodingHint && (
          <p
            className={cn(
              "text-xs rounded-md border px-2 py-1.5",
              encoding === "binary"
                ? "text-destructive bg-destructive/10 border-destructive/30"
                : "text-muted-foreground bg-muted/40 border-border"
            )}
          >
            {encodingHint}
          </p>
        )}
        {format === "empty" ? (
          <p className="text-sm text-muted-foreground">{t("chat_io.empty")}</p>
        ) : (
          <RawJsonPanel raw={raw} />
        )}
      </CardContent>
    </Card>
  );
}

// ── Input Section ──

function InputSection({ chatIO, style }: { chatIO: ChatIO; style: Protocol | undefined }) {
  const { t } = useTranslation("logs");
  const raw = chatIO.Input ?? "";
  const rawToggle = useRawJsonToggle(raw);
  const parsed = useMemo<ParsedRequest | null>(() => {
    if (!raw) return null;
    return parseRequest({ Input: raw, Style: style }, style);
  }, [raw, style]);

  const hasBody = parsed !== null && (parsed.messages.length > 0 || parsed.tools.length > 0 || parsed.system !== undefined || parsed.params && Object.keys(parsed.params).length > 0);
  const hasRaw = raw.length > 0;

  if (!hasRaw && !hasBody) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-3">
          <CardTitle className="text-base">{t("chat_io.request_input")}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">{t("chat_io.empty")}</p>
        </CardContent>
      </Card>
    );
  }

  const errors = parsed?.errors ?? [];
  const params = parsed?.params ?? {};
  const paramCount = Object.keys(params).length;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-3">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">{t("chat_io.request_input")}</CardTitle>
          {parsed && <Badge variant="outline" className="text-xs font-mono">{parsed.protocol}</Badge>}
          {parsed?.model && (
            <span className="text-xs text-muted-foreground">
              {t("chat_io.model")}: <span className="font-mono">{parsed.model}</span>
            </span>
          )}
          {errors.length > 0 && (
            <Badge variant="destructive" className="text-xs">{errors.length}</Badge>
          )}
        </div>
        {hasRaw && <RawJsonButton show={rawToggle.show} toggle={rawToggle.toggle} />}
      </CardHeader>
      <CardContent className="space-y-2">
        {rawToggle.show && hasRaw ? (
          <RawJsonPanel raw={raw} />
        ) : (
          <>
            {errors.map((err, i) => (
              <p key={i} className="text-xs text-destructive bg-destructive/10 border border-destructive/30 rounded-md px-2 py-1.5">
                {err.message}
              </p>
            ))}
            {parsed?.system !== undefined && (
              <CollapsibleSection title={t("chat_io.system")} subtitle={typeof parsed.system === "string" ? truncate(parsed.system, 60) : undefined}>
                <SystemView value={parsed.system} />
              </CollapsibleSection>
            )}
            {parsed && parsed.messages.length > 0 && (
              <CollapsibleSection title={t("chat_io.messages")} badge={`${parsed.messages.length}`} defaultOpen={true}>
                <div className="space-y-2">
                  {parsed.messages.map((msg, i) => (
                    <MessageView key={i} msg={msg} />
                  ))}
                </div>
              </CollapsibleSection>
            )}
            {parsed && parsed.tools.length > 0 && (
              <CollapsibleSection title={t("chat_io.tools")} badge={`${parsed.tools.length}`}>
                <div className="space-y-2">
                  {parsed.tools.map((tool, i) => (
                    <ToolDeclarationItem key={i} tool={tool} />
                  ))}
                </div>
              </CollapsibleSection>
            )}
            {parsed && paramCount > 0 && (
              <CollapsibleSection title={t("chat_io.parameters")} badge={`${paramCount}`}>
                <JsonViewer data={params} />
              </CollapsibleSection>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// ── Output Section ──

function OutputSection({ chatIO, style }: { chatIO: ChatIO; style: Protocol | undefined }) {
  const { t } = useTranslation("logs");
  const parsed = useMemo<ParsedResponse>(() => {
    return parseResponse({ OfString: chatIO.OfString, OfStringArray: chatIO.OfStringArray, Style: style }, style);
  }, [chatIO.OfString, chatIO.OfStringArray, style]);

  const rawOutput = useMemo(
    () => rawResponseText(chatIO.OfString, chatIO.OfStringArray),
    [chatIO.OfString, chatIO.OfStringArray],
  );

  const rawToggle = useRawJsonToggle(rawOutput);

  const thinking = parsed.thinking;
  const toolCalls = parsed.toolCalls;
  const toolResults = parsed.toolResults;
  const hasMeta = parsed.model || parsed.finishReason;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-3">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">{t("chat_io.response_output")}</CardTitle>
          <Badge variant={parsed.stream ? "default" : "secondary"} className="text-xs">
            {parsed.stream ? t("chat_io.streaming") : t("chat_io.complete")}
          </Badge>
          <Badge variant="outline" className="text-xs font-mono">{parsed.protocol}</Badge>
          {parsed.stream && chatIO.OfStringArray && (
            <span className="text-xs text-muted-foreground">
              {t("chat_io.chunks_count", { count: chatIO.OfStringArray.length })}
            </span>
          )}
          {parsed.malformed > 0 && (
            <Badge variant="destructive" className="text-xs">
              {t("chat_io.malformed", { count: parsed.malformed })}
            </Badge>
          )}
          {parsed.unknown > 0 && (
            <Badge variant="outline" className="text-xs">
              {t("chat_io.unknown_blocks", { count: parsed.unknown })}
            </Badge>
          )}
        </div>
        {rawOutput && <RawJsonButton show={rawToggle.show} toggle={rawToggle.toggle} />}
      </CardHeader>
      <CardContent className="space-y-3">
        {rawToggle.show ? (
          <RawJsonPanel raw={rawOutput} />
        ) : (
          <>
            {hasMeta && (
              <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                {parsed.model && <InfoRow label={t("chat_io.model") + ":"} value={<span className="font-mono">{parsed.model}</span>} />}
                {parsed.finishReason && (
                  <InfoRow label={t("chat_io.finish_reason") + ":"} value={<Badge variant="outline" className="text-xs">{parsed.finishReason}</Badge>} />
                )}
              </div>
            )}

            {thinking && (
              <CollapsibleSection title={t("chat_io.thinking")} defaultOpen={true}>
                <ThinkingCard text={thinking} />
              </CollapsibleSection>
            )}

            {toolCalls.length > 0 && (
              <CollapsibleSection title={t("chat_io.tool_call")} badge={`${toolCalls.length}`} defaultOpen={true}>
                <div className="space-y-1.5">
                  {toolCalls.map((call, i) => <ToolCallCard key={i} call={call} />)}
                </div>
              </CollapsibleSection>
            )}

            {toolResults.length > 0 && (
              <CollapsibleSection title={t("chat_io.tool_result")} badge={`${toolResults.length}`}>
                <div className="space-y-1.5">
                  {toolResults.map((r, i) => <ToolResultCard key={i} result={r} />)}
                </div>
              </CollapsibleSection>
            )}

            {parsed.text && (
              <CollapsibleSection title={t("chat_io.content")} defaultOpen={true} subtitle={truncate(parsed.text.replace(/\s+/g, " ").trim(), 80)} badge={parsed.text.length > 0 ? String(parsed.text.length) : undefined}>
                <pre className="whitespace-pre-wrap text-sm leading-relaxed break-words">{parsed.text}</pre>
              </CollapsibleSection>
            )}

            {!parsed.text && !thinking && toolCalls.length === 0 && toolResults.length === 0 && (
              <p className="text-xs text-muted-foreground">{t("chat_io.empty")}</p>
            )}

            {parsed.usage && (
              <CollapsibleSection title={t("chat_io.usage")}>
                <UsageTable usage={parsed.usage} />
              </CollapsibleSection>
            )}

            {!parsed.stream && parsed.chunks.length > 0 && parsed.chunks.every((c) => typeof c === "object" && c !== null) && (
              <CollapsibleSection title={t("chat_io.full_response")}>
                <JsonViewer data={parsed.chunks[0]} />
              </CollapsibleSection>
            )}

            {parsed.stream && parsed.chunks.length > 0 && (
              <CollapsibleSection title={t("chat_io.stream_chunks")} badge={`${parsed.chunks.length}`}>
                <div className="space-y-1 max-h-96 overflow-y-auto">
                  {parsed.chunks.map((chunk, i) => (
                    <div key={i} className="text-xs font-mono bg-muted/30 rounded px-2 py-1 border break-all">
                      <span className="text-muted-foreground mr-2">#{i + 1}</span>
                      {typeof chunk === "string" ? chunk : JSON.stringify(chunk)}
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

// ── Page ──

export default function LogChatPage() {
  const { t } = useTranslation(["logs", "common"]);
  const { logId } = useParams<{ logId: string }>();
  const navigate = useNavigate();
  const [chatIO, setChatIO] = useState<ChatIO | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadErrorMessage, setLoadErrorMessage] = useState<string | null>(null);
  const style = protocolOf(chatIO?.Style);
  // Style 不是四种聊天协议、响应体又一条特征都嗅不出来：整页按通用 JSON 展示。
  // 之前这两项判定与 rawResponseText 都在渲染期直接算，整份 IO 每渲染一次就被
  // 重新解析一遍（输入框/主题/父级任何一次重渲染都算）；结果只随 chatIO 变化，
  // 因此收进 useMemo。
  const generic = useMemo(() => {
    if (chatIO === null || style) return false;
    if (sniffResponseProtocol(chatIO, style) !== null) return false;
    return !inputLooksLikeChat(chatIO.Input);
  }, [chatIO, style]);
  const rawOutput = useMemo(
    () => (chatIO ? rawResponseText(chatIO.OfString, chatIO.OfStringArray) : ""),
    [chatIO],
  );
  // 请求与响应各判各的格式：一次调用里两边形态经常不同（请求 JSON、响应 SSE
  // 转储），合并判断必然标错一侧。与 rawOutput 同理收进 useMemo——sniffRawFormat
  // 要对整段文本做 JSON.parse，不该每次重渲染都跑一遍。
  const inputFormat = useMemo(() => sniffRawFormat(chatIO?.Input), [chatIO]);
  const outputFormat = useMemo(() => sniffRawFormat(rawOutput), [rawOutput]);

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
          // 后端返回的是 "ChatIO not found"，大小写敏感比较会把"没找到"
          // 误报成通用加载失败，用户看不到真正的 404 语义。
          if (fetchError.message.toLowerCase().includes("chat io not found")) {
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
          {style && <Badge variant="outline" className="text-xs font-mono">{style}</Badge>}
          {chatIO && !style && chatIO.Style && (
            <Badge variant="secondary" className="text-xs font-mono">{chatIO.Style}</Badge>
          )}
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
          {generic ? (
            <>
              <GenericJsonSection
                title={t("chat_io.request_input")}
                hint={t("chat_io.generic_hint")}
                raw={chatIO.Input ?? ""}
                format={inputFormat}
                encoding={chatIO.InputEncoding}
              />
              <GenericJsonSection
                title={t("chat_io.response_output")}
                hint={t("chat_io.generic_hint")}
                raw={rawOutput}
                format={outputFormat}
                encoding={chatIO.OutputEncoding}
                streamingChunks={chatIO.OfStringArray?.length ?? 0}
              />
            </>
          ) : (
            <>
              <InputSection chatIO={chatIO} style={style} />
              <OutputSection chatIO={chatIO} style={style} />
            </>
          )}
        </div>
      )}
    </div>
  );
}
