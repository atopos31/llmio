import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import Loading from "@/components/loading";
import { useTheme } from "@/components/theme-provider";
import { getChatIO, type ChatIO } from "@/lib/api";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { duotoneDark } from "react-syntax-highlighter/dist/esm/styles/prism";
import { duotoneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import { toast } from "sonner";
import { Copy, Check } from "lucide-react";

type SyntaxStyle = typeof duotoneLight;

interface CopyButtonProps {
  text: string;
}

function CopyButton({ text }: CopyButtonProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success("已复制到剪贴板");
      setTimeout(() => setCopied(false), 2000);
    } catch (error) {
      toast.error("复制失败");
    }
  };

  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={handleCopy}
      className="h-8 w-8 p-0"
    >
      {copied ? (
        <Check className="h-4 w-4" />
      ) : (
        <Copy className="h-4 w-4" />
      )}
    </Button>
  );
}

interface JsonBlockProps {
  title: string;
  raw: string;
  syntaxStyle: SyntaxStyle;
}

interface FormattedJson {
  text: string;
  parsed: boolean;
  empty: boolean;
}

function formatJson(raw: string): FormattedJson {
  if (!raw || raw.trim().length === 0) {
    return { text: "(无内容)", parsed: false, empty: true };
  }

  try {
    const parsedJson = JSON.parse(raw);
    return {
      text: JSON.stringify(parsedJson, null, 2),
      parsed: true,
      empty: false
    };
  } catch {
    return {
      text: raw,
      parsed: false,
      empty: false
    };
  }
}

function JsonBlock({ title, raw, syntaxStyle }: JsonBlockProps) {
  const { text, parsed, empty } = formatJson(raw);

  return (
    <Card>
      <CardHeader className="space-y-2">
        <CardTitle className="text-base font-semibold">{title}</CardTitle>
        <CardDescription>
          {empty ? "暂无数据" : parsed ? "格式化 JSON 预览" : "原始数据（非 JSON 或解析失败）"}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <JsonContent text={text} parsed={parsed} empty={empty} syntaxStyle={syntaxStyle} raw={raw} />
      </CardContent>
    </Card>
  );
}

interface OutputPreviewProps {
  index: number;
  raw: string;
  syntaxStyle: SyntaxStyle;
}

function OutputPreview({ index, raw, syntaxStyle }: OutputPreviewProps) {
  const { text, parsed, empty } = formatJson(raw);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <p className="text-sm font-medium text-muted-foreground">响应片段 {index + 1}</p>
          {!parsed && !empty && <span className="text-xs text-muted-foreground">原始字符串</span>}
          {empty && <span className="text-xs text-muted-foreground">暂无数据</span>}
        </div>
      </div>
      <JsonContent text={text} parsed={parsed} empty={empty} syntaxStyle={syntaxStyle} raw={raw} />
    </div>
  );
}

interface DefaultOutputProps {
  raw: string;
  syntaxStyle: SyntaxStyle;
}

function DefaultOutput({ raw, syntaxStyle }: DefaultOutputProps) {
  const { text, parsed, empty } = formatJson(raw);

  return (
    <div className="space-y-2">
      <div>
        {!parsed && !empty && <span className="text-xs text-muted-foreground">原始字符串</span>}
        {empty && <span className="text-xs text-muted-foreground">暂无数据</span>}
      </div>
      <JsonContent text={text} parsed={parsed} empty={empty} syntaxStyle={syntaxStyle} raw={raw} />
    </div>
  );
}

interface JsonContentProps {
  text: string;
  parsed: boolean;
  empty: boolean;
  syntaxStyle: SyntaxStyle;
  raw: string;
}

function JsonContent({ text, parsed, empty, syntaxStyle, raw }: JsonContentProps) {
  if (parsed && !empty) {
    return (
      <div className="relative w-full max-w-full min-w-0 overflow-x-auto rounded-md border bg-muted/70 font-mono text-sm leading-6">
        <div className="absolute top-2 right-2 z-10">
          <CopyButton text={raw} />
        </div>
        <SyntaxHighlighter
          language="json"
          style={syntaxStyle}
          customStyle={{
            margin: 0,
            background: "transparent",
            padding: "1rem",
            fontSize: "0.875rem",
            lineHeight: "1.5rem",
            whiteSpace: "pre",
            minWidth: "100%",
            maxWidth: "100%"
          }}
        >
          {text}
        </SyntaxHighlighter>
      </div>
    );
  }

  return (
    <div className="relative w-full max-w-full min-w-0">
      <div className="absolute top-2 right-2 z-10">
        <CopyButton text={raw} />
      </div>
      <pre className="whitespace-pre font-mono text-sm leading-6 bg-muted/70 border rounded-md p-4 overflow-x-auto w-full max-w-full min-w-0">{text}</pre>
    </div>
  );
}

function useSyntaxStyle(): SyntaxStyle {
  const { theme } = useTheme();

  const defaultPrefersDark = useMemo(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  }, []);

  const [isDark, setIsDark] = useState<boolean>(() => {
    if (theme === "system") {
      return defaultPrefersDark;
    }
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
      if (media.addEventListener) {
        media.addEventListener("change", listener);
        return () => media.removeEventListener("change", listener);
      }
      // 兼容旧浏览器
      media.addListener(listener);
      return () => media.removeListener(listener);
    }
    setIsDark(theme === "dark");
    return undefined;
  }, [theme]);

  return isDark ? duotoneDark : duotoneLight;
}

export default function LogChatPage() {
  const { logId } = useParams<{ logId: string }>();
  const navigate = useNavigate();
  const [chatIO, setChatIO] = useState<ChatIO | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadErrorMessage, setLoadErrorMessage] = useState<string | null>(null);
  const outputList = chatIO?.OfStringArray ?? [];
  const hasArrayOutput = outputList.length > 0;
  const singleOutput = chatIO?.OfString ?? "";
  const syntaxStyle = useSyntaxStyle();

  useEffect(() => {
    if (!logId) {
      const message = "缺少日志 ID";
      toast.error(message);
      setLoadErrorMessage(message);
      setLoading(false);
      return;
    }

    const parsedId = Number(logId);
    if (Number.isNaN(parsedId)) {
      const message = "日志 ID 无效";
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
        let message = "获取会话日志失败";
        if (fetchError instanceof Error) {
          if (fetchError.message.includes("chat io not found")) {
            message = "暂无会话记录，可能未开启 IO 记录";
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
  }, [logId]);

  if (loading) {
    return <Loading message="加载会话详情" />;
  }

  return (
    <div className="space-y-6 h-full overflow-y-auto overflow-x-hidden">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">会话详情</h1>
          <p className="text-sm text-muted-foreground">日志 ID：{logId}</p>
        </div>
        <Button variant="outline" onClick={() => navigate(-1)}>
          返回
        </Button>
      </div>

      {loadErrorMessage && (
        <Card>
          <CardHeader>
            <CardTitle>加载失败</CardTitle>
            <CardDescription>{loadErrorMessage}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => navigate(-1)}>回到日志列表</Button>
          </CardContent>
        </Card>
      )}

      {!loadErrorMessage && chatIO && (
        <div className="space-y-6">
          <JsonBlock title="请求输入" raw={chatIO.Input} syntaxStyle={syntaxStyle} />

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-semibold">响应输出</CardTitle>
              <CardDescription>
                {hasArrayOutput
                  ? "列表中的每一项都会尝试以 JSON 格式展示"
                  : "如果数据无法解析为 JSON，将保留原始内容"}
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {hasArrayOutput ? (
                outputList.map((entry, index) => (
                  <OutputPreview key={`chat-io-${index}`} index={index} raw={entry} syntaxStyle={syntaxStyle} />
                ))
              ) : (
                <DefaultOutput raw={singleOutput} syntaxStyle={syntaxStyle} />
              )}
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}
