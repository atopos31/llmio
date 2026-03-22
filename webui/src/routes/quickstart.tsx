import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { getAuthKeys, getModelOptions } from "@/lib/api";
import { toast } from "sonner";
import { Copy, Check } from "lucide-react";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { duotoneDark } from "react-syntax-highlighter/dist/esm/styles/prism";

type ApiFormat = "openai-completions" | "openai-responses" | "anthropic-messages" | "google-generative";
type CodeLanguage = "curl" | "typescript" | "python";

type SnippetInput = {
  apiFormat: ApiFormat;
  language: CodeLanguage;
  model: string;
  baseUrl: string;
  apiKey: string;
};

type ApiKeyOption = {
  id: number;
  name: string;
  key: string;
  allowAll: boolean;
  models: string[] | null;
};

const FALLBACK_MODELS = [
  "gpt-4o-mini",
  "gpt-4.1-mini",
  "claude-3-5-sonnet-20241022",
  "claude-3-5-haiku-20241022",
  "gemini-1.5-flash",
  "gemini-1.5-pro",
];

function pickDefaultModel(models: string[]): string {
  return models.length ? models[0] : FALLBACK_MODELS[0];
}

function buildSnippet({ apiFormat, language, model, baseUrl, apiKey }: SnippetInput): string {
  const storyPrompt = "Write a short bedtime story about a unicorn.";
  const resolvedKey = apiKey || "YOUR_API_KEY";
  const openaiRuntimeKey = `"${resolvedKey}"`;
  const anthropicRuntimeKey = `"${resolvedKey}"`;
  const geminiRuntimeKey = `"${resolvedKey}"`;

  if (apiFormat === "openai-completions") {
    if (language === "curl") {
      return [
        `curl ${baseUrl}/chat/completions \\\n  -H "Content-Type: application/json" \\\n  -H "Authorization: Bearer ${resolvedKey}" \\\n  -d '{`,
        `  "model": "${model}",`,
        `  "messages": [`,
        `    { "role": "system", "content": "You are a helpful assistant." },`,
        `    { "role": "user", "content": "${storyPrompt}" }`,
        `  ]`,
        `}'`,
      ].join("\n");
    }

    if (language === "typescript") {
      return [
        `import OpenAI from "openai";`,
        ``,
        `const client = new OpenAI({`,
        `  apiKey: ${openaiRuntimeKey},`,
        `  baseURL: "${baseUrl}",`,
        `});`,
        ``,
        `const response = await client.chat.completions.create({`,
        `  model: "${model}",`,
        `  messages: [`,
        `    { role: "system", content: "You are a helpful assistant." },`,
        `    { role: "user", content: "${storyPrompt}" },`,
        `  ],`,
        `});`,
        ``,
        `console.log(response);`,
      ].join("\n");
    }

    return [
      `from openai import OpenAI`,
      ``,
      `api_key = "${resolvedKey}"`,
      `client = OpenAI(api_key=api_key, base_url="${baseUrl}")`,
      ``,
      `response = client.chat.completions.create(`,
      `    model="${model}",`,
      `    messages=[`,
      `        {"role": "system", "content": "You are a helpful assistant."},`,
      `        {"role": "user", "content": "${storyPrompt}"},`,
      `    ],`,
      `)`,
      ``,
      `print(response)`,
    ].join("\n");
  }

  if (apiFormat === "openai-responses") {
    if (language === "curl") {
      return [
        `curl ${baseUrl}/responses \\\n  -H "Content-Type: application/json" \\\n  -H "Authorization: Bearer ${resolvedKey}" \\\n  -d '{`,
        `  "model": "${model}",`,
        `  "input": "${storyPrompt}"`,
        `}'`,
      ].join("\n");
    }

    if (language === "typescript") {
      return [
        `import OpenAI from "openai";`,
        ``,
        `const client = new OpenAI({`,
        `  apiKey: ${openaiRuntimeKey},`,
        `  baseURL: "${baseUrl}",`,
        `});`,
        ``,
        `const response = await client.responses.create({`,
        `  model: "${model}",`,
        `  input: "${storyPrompt}",`,
        `});`,
        ``,
        `console.log(response);`,
      ].join("\n");
    }

    return [
      `from openai import OpenAI`,
      ``,
      `api_key = "${resolvedKey}"`,
      `client = OpenAI(api_key=api_key, base_url="${baseUrl}")`,
      ``,
      `response = client.responses.create(`,
      `    model="${model}",`,
      `    input="${storyPrompt}",`,
      `)`,
      ``,
      `print(response)`,
    ].join("\n");
  }

  if (apiFormat === "anthropic-messages") {
    if (language === "curl") {
      return [
        `curl ${baseUrl}/messages \\\n  -H "Content-Type: application/json" \\\n  -H "x-api-key: ${resolvedKey}" \\\n  -H "anthropic-version: 2023-06-01" \\\n  -d '{`,
        `  "model": "${model}",`,
        `  "max_tokens": 256,`,
        `  "messages": [`,
        `    { "role": "user", "content": "${storyPrompt}" }`,
        `  ]`,
        `}'`,
      ].join("\n");
    }

    if (language === "typescript") {
      return [
        `import Anthropic from "@anthropic-ai/sdk";`,
        ``,
        `const client = new Anthropic({`,
        `  apiKey: ${anthropicRuntimeKey},`,
        `  baseURL: "${baseUrl}",`,
        `});`,
        ``,
        `const response = await client.messages.create({`,
        `  model: "${model}",`,
        `  max_tokens: 256,`,
        `  messages: [{ role: "user", content: "${storyPrompt}" }],`,
        `});`,
        ``,
        `console.log(response);`,
      ].join("\n");
    }

    return [
      `import anthropic`,
      ``,
      `api_key = "${resolvedKey}"`,
      `client = anthropic.Anthropic(api_key=api_key, base_url="${baseUrl}")`,
      ``,
      `response = client.messages.create(`,
      `    model="${model}",`,
      `    max_tokens=256,`,
      `    messages=[{"role": "user", "content": "${storyPrompt}"}],`,
      `)`,
      ``,
      `print(response)`,
    ].join("\n");
  }

  if (language === "curl") {
    return [
      `curl ${baseUrl}/models/${model}:generateContent \\\n  -H "Content-Type: application/json" \\\n  -H "x-goog-api-key: ${resolvedKey}" \\\n  -d '{`,
      `  "contents": [`,
      `    {`,
      `      "role": "user",`,
      `      "parts": [`,
      `        { "text": "${storyPrompt}" }`,
      `      ]`,
      `    }`,
      `  ]`,
      `}'`,
    ].join("\n");
  }

  if (language === "typescript") {
    return [
      `import { GoogleGenerativeAI } from "@google/generative-ai";`,
      ``,
      `const genAI = new GoogleGenerativeAI(${geminiRuntimeKey}, {`,
      `  apiEndpoint: "${baseUrl}",`,
      `});`,
      ``,
      `const model = genAI.getGenerativeModel({ model: "${model}" });`,
      `const response = await model.generateContent("${storyPrompt}");`,
      ``,
      `console.log(response);`,
    ].join("\n");
  }

  return [
    `import google.generativeai as genai`,
    ``,
    `api_key = "${resolvedKey}"`,
    `genai.configure(api_key=api_key, api_endpoint="${baseUrl}")`,
    ``,
    `model = genai.GenerativeModel("${model}")`,
    `response = model.generate_content("${storyPrompt}")`,
    ``,
    `print(response)`,
  ].join("\n");
}

function CodeCopyButton({
  text,
  successLabel,
  errorLabel,
}: {
  text: string;
  successLabel: string;
  errorLabel: string;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success(successLabel);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error(errorLabel);
    }
  };

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={handleCopy}
      className="h-8 w-8 text-white/70 hover:text-white hover:bg-white/10"
      aria-label={successLabel}
      title={successLabel}
    >
      {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
    </Button>
  );
}

function InlineCopyButton({
  text,
  successLabel,
  errorLabel,
}: {
  text: string;
  successLabel: string;
  errorLabel: string;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success(successLabel);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error(errorLabel);
    }
  };

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={handleCopy}
      className="h-7 w-7 text-white/70 hover:text-white hover:bg-white/10"
      aria-label={successLabel}
      title={successLabel}
    >
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  );
}

export default function Quickstart() {
  const { t } = useTranslation("quickstart");
  const [apiFormat, setApiFormat] = useState<ApiFormat>("openai-completions");
  const [language, setLanguage] = useState<CodeLanguage>("curl");
  const [selectedModel, setSelectedModel] = useState("");
  const [apiKeyChoice, setApiKeyChoice] = useState("");
  const [availableKeys, setAvailableKeys] = useState<ApiKeyOption[]>([]);
  const [remoteModels, setRemoteModels] = useState<string[]>([]);
  const defaultOrigin = useMemo(() => {
    return typeof window === "undefined" ? "http://localhost:7070" : window.location.origin;
  }, []);

  const didAutoSelectRef = useRef(false);

  useEffect(() => {
    let active = true;
    const loadModels = async () => {
      try {
        const models = await getModelOptions();
        if (!active) return;
        const names = models
          .map((model) => model.Name)
          .filter((name): name is string => Boolean(name));
        setRemoteModels(names);
        if (!didAutoSelectRef.current) {
          const defaultModel = names.length ? names[0] : pickDefaultModel(FALLBACK_MODELS);
          setSelectedModel(defaultModel);
          didAutoSelectRef.current = true;
        }
      } catch (error) {
        if (!active) return;
        console.error(error);
        toast.error(t("toast.load_models_failed"));
        setRemoteModels([]);
        if (!didAutoSelectRef.current) {
          setSelectedModel(pickDefaultModel(FALLBACK_MODELS));
          didAutoSelectRef.current = true;
        }
      }
    };

    void loadModels();

    return () => {
      active = false;
    };
  }, [t]);

  useEffect(() => {
    let active = true;
    const loadKeys = async () => {
      try {
        const data = await getAuthKeys({ page: 1, page_size: 100 });
        if (!active) return;
        const keys = data.data
          .filter((item) => item.Key)
          .map((item) => ({
            id: item.ID,
            name: item.Name,
            key: item.Key,
            allowAll: item.AllowAll,
            models: item.Models ?? null,
          }));
        setAvailableKeys(keys);
        if (keys.length && !apiKeyChoice) {
          setApiKeyChoice(keys[0].key);
        }
      } catch (error) {
        if (!active) return;
        console.error(error);
        toast.error(t("toast.load_keys_failed"));
      }
    };

    void loadKeys();

    return () => {
      active = false;
    };
  }, [apiKeyChoice, t]);

  const availableModels = useMemo(() => {
    return remoteModels.length ? remoteModels : FALLBACK_MODELS;
  }, [remoteModels]);

  const selectedKey = useMemo(() => {
    return availableKeys.find((item) => item.key === apiKeyChoice) ?? null;
  }, [availableKeys, apiKeyChoice]);

  const filteredModels = useMemo(() => {
    if (!selectedKey || selectedKey.allowAll || !selectedKey.models?.length) {
      return availableModels;
    }
    return availableModels.filter((model) => selectedKey.models?.includes(model));
  }, [availableModels, selectedKey]);

  useEffect(() => {
    if (!filteredModels.length) return;
    if (!filteredModels.includes(selectedModel)) {
      setSelectedModel(pickDefaultModel(filteredModels));
    }
  }, [filteredModels, selectedModel]);

  const baseUrl = useMemo(() => {
    if (apiFormat.startsWith("openai")) {
      return `${defaultOrigin}/openai/v1`;
    }
    if (apiFormat === "anthropic-messages") {
      return `${defaultOrigin}/anthropic`;
    }
    return `${defaultOrigin}/gemini/v1beta`;
  }, [apiFormat, defaultOrigin]);
  const currentModel = selectedModel || pickDefaultModel(filteredModels);

  const resolvedApiKey = apiKeyChoice;
  const baseUrlDisplay = baseUrl;
  const apiKeyDisplay = apiKeyChoice || t("controls.api_key_empty");
  const apiKeyCopyValue = apiKeyChoice;

  const snippet = useMemo(() => {
    return buildSnippet({
      apiFormat,
      language,
      model: currentModel,
      baseUrl,
      apiKey: resolvedApiKey,
    });
  }, [apiFormat, language, currentModel, baseUrl, resolvedApiKey]);

  const formatOptions: Array<{ value: ApiFormat; label: string }> = [
    { value: "openai-completions", label: t("formats.openai_completions") },
    { value: "openai-responses", label: t("formats.openai_responses") },
    { value: "anthropic-messages", label: t("formats.anthropic_messages") },
    { value: "google-generative", label: t("formats.google_generative") },
  ];

  const languageOptions: Array<{ value: CodeLanguage; label: string }> = [
    { value: "curl", label: t("languages.curl") },
    { value: "typescript", label: t("languages.typescript") },
    { value: "python", label: t("languages.python") },
  ];

  return (
    <div className="h-full min-h-0 flex flex-col gap-4 p-1">
      <div className="flex flex-col gap-2">
        <h2 className="text-2xl font-bold tracking-tight">{t("title")}</h2>
        <p className="text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto">
        <div className="relative overflow-hidden rounded-xl sm:rounded-2xl lg:rounded-3xl border border-white/10 bg-[radial-gradient(circle_at_top,#1c1f24_0%,#0f1114_42%,#0b0c0f_100%)] p-2 sm:p-4 md:p-6 shadow-[0_30px_60px_-30px_rgba(0,0,0,0.75)]">
          <div className="absolute -top-12 -right-8 h-32 w-32 rounded-full bg-white/10 blur-3xl" />
          <div className="absolute -bottom-16 left-10 h-40 w-40 rounded-full bg-white/5 blur-3xl" />
          <div className="pointer-events-none absolute inset-0 opacity-40 [background:linear-gradient(110deg,transparent_45%,rgba(255,255,255,0.06)_50%,transparent_55%)]" />

          <div className="relative grid min-w-0 gap-3 sm:gap-5 lg:gap-6 lg:grid-cols-[minmax(260px,1fr)_minmax(360px,1.6fr)]">
            <div className="order-2 lg:order-1 flex min-w-0 flex-col gap-3 sm:gap-4 text-white">
              <div className="hidden lg:block">
                <h3 className="text-lg md:text-xl font-semibold">{t("left.title")}</h3>
                <p className="mt-2 text-sm text-white/60 leading-relaxed">{t("left.subtitle")}</p>
              </div>

              <div className="space-y-2 sm:space-y-4">
                <div className="flex flex-col gap-1.5 sm:gap-2">
                  <Label className="text-[10px] sm:text-xs uppercase tracking-wide text-white/60">
                    {t("controls.base_url")}
                  </Label>
                  <div className="flex items-center justify-between rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-[11px] sm:text-xs text-white/80">
                    <span className="truncate">{baseUrlDisplay}</span>
                    <InlineCopyButton
                      text={baseUrlDisplay}
                      successLabel={t("toast.copy_success")}
                      errorLabel={t("toast.copy_failed")}
                    />
                  </div>
                </div>

                <div className="flex flex-col gap-1.5 sm:gap-2">
                  <div className="flex items-center justify-between gap-3">
                    <Label className="text-[10px] sm:text-xs uppercase tracking-wide text-white/60">
                      {t("controls.api_key")}
                    </Label>
                    <Select value={apiKeyChoice} onValueChange={setApiKeyChoice}>
                      <SelectTrigger className="h-7 sm:h-8 w-[160px] sm:w-[190px] border-white/10 bg-white/5 text-[10px] sm:text-[11px] text-white">
                        <SelectValue placeholder={t("controls.api_key_placeholder")} />
                      </SelectTrigger>
                      <SelectContent>
                        {availableKeys.map((item) => (
                          <SelectItem key={item.id} value={item.key}>
                            {item.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="flex items-center justify-between rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-[11px] sm:text-xs text-white/80">
                    <span className="truncate">{apiKeyDisplay}</span>
                    <InlineCopyButton
                      text={apiKeyCopyValue}
                      successLabel={t("toast.copy_success")}
                      errorLabel={t("toast.copy_failed")}
                    />
                  </div>
                </div>
              </div>

              <div className="hidden lg:block mt-auto text-xs text-white/50">
                {t("left.note")}
              </div>
            </div>

            <div className="order-1 lg:order-2 min-w-0 rounded-xl sm:rounded-2xl border border-white/10 bg-black/40 p-2 sm:p-4 text-white backdrop-blur">
              <div className="flex flex-col gap-2 sm:gap-4">
                <div className="grid gap-2 sm:gap-4 grid-cols-2">
                  <div className="flex flex-col gap-1.5 sm:gap-2">
                    <Label className="text-[10px] sm:text-[11px] uppercase tracking-[0.2em] text-white/50">
                      {t("controls.api_format")}
                    </Label>
                    <Select value={apiFormat} onValueChange={(value) => setApiFormat(value as ApiFormat)}>
                      <SelectTrigger className="h-8 sm:h-9 border-white/10 bg-black/40 text-[10px] sm:text-xs text-white">
                        <SelectValue placeholder={t("controls.api_format_placeholder")} />
                      </SelectTrigger>
                      <SelectContent>
                        {formatOptions.map((option) => (
                          <SelectItem key={option.value} value={option.value}>
                            {option.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>

                  <div className="flex flex-col gap-1.5 sm:gap-2">
                    <Label className="text-[10px] sm:text-[11px] uppercase tracking-[0.2em] text-white/50">
                      {t("controls.model")}
                    </Label>
                    <Select
                      value={currentModel}
                      onValueChange={(value) => {
                        setSelectedModel(value);
                        didAutoSelectRef.current = true;
                      }}
                    >
                      <SelectTrigger className="h-8 sm:h-9 border-white/10 bg-black/40 text-[10px] sm:text-xs text-white">
                        <SelectValue placeholder={t("controls.model_placeholder")} />
                      </SelectTrigger>
                      <SelectContent>
                        {filteredModels.map((model) => (
                          <SelectItem key={model} value={model}>
                            {model}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </div>

                <div className="min-w-0 rounded-lg sm:rounded-xl border border-white/10 bg-black/70 p-2 sm:p-3">
                  <div className="mb-2 sm:mb-3 flex items-center justify-between">
                    <div className="flex flex-wrap gap-1.5">
                      {languageOptions.map((option) => (
                        <button
                          key={option.value}
                          type="button"
                          onClick={() => setLanguage(option.value)}
                          className={`rounded-full px-2.5 py-1 text-[10px] sm:text-[11px] font-semibold transition ${
                            language === option.value
                              ? "bg-white/20 text-white shadow-[0_0_0_1px_rgba(255,255,255,0.2)]"
                              : "text-white/60 hover:bg-white/10 hover:text-white"
                          }`}
                        >
                          {option.label}
                        </button>
                      ))}
                    </div>
                    <CodeCopyButton
                      text={snippet}
                      successLabel={t("toast.copy_success")}
                      errorLabel={t("toast.copy_failed")}
                    />
                  </div>

                  <div className="max-h-64 min-w-0 overflow-auto pr-1 sm:max-h-[360px]">
                    <SyntaxHighlighter
                      language={language === "curl" ? "bash" : language}
                      style={duotoneDark}
                      customStyle={{
                        background: "transparent",
                        margin: 0,
                        padding: 0,
                        fontSize: "clamp(0.68rem, 1.8vw, 0.78rem)",
                        lineHeight: "1.5",
                      }}
                      wrapLongLines
                    >
                      {snippet}
                    </SyntaxHighlighter>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
