import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { JsonTree } from "@/components/json-tree"
import {
  createQuotaSource,
  deleteQuotaSource,
  testQuotaSource,
  updateQuotaSource,
} from "@/lib/api"
import {
  itemsOf,
  jsonToText,
  kvToText,
  queryToText,
  renderItemText,
  unitKindOf,
  type QuotaBuiltinInfo,
  type QuotaSource,
  type QuotaSourceType,
  type QuotaTestResult,
} from "@/lib/quota"

/** 脚本模板：一上来就有个能跑通的骨架，比空白框友好得多。 */
const SCRIPT_SAMPLE = `// 取数契约：
//   - 把结果交给 output()，或直接 return 一个值
//   - text 由服务端按 format 渲染，这里不用管
//   - 字段别名很宽松（used/已用、total/总量、remaining/剩余、unit/单位…，任二补一）
//   - console.log 会作为调试日志回传，不影响取数
const res = await fetch("https://example.com/api/usage", {
  headers: { Authorization: "Bearer " + env.API_KEY },
});
const data = await res.json();

output({
  items: [{ label: "套餐余量", used: data.used, total: data.total, unit: "CREDITS" }],
});
`

/** 标签 + 说明的字段壳。 */
function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      {children}
      {hint && <span className="text-[11px] text-muted-foreground">{hint}</span>}
    </div>
  )
}

/**
 * 环境变量输入框。
 *
 * 脚本与登录型内置共用：两边都是"往 env 白名单里塞凭据"，只是键名的来路
 * 不同——脚本由用户自定，登录型由适配器定死（因此那边把键名写进占位符）。
 */
function EnvField({
  value,
  onChange,
  hint,
  placeholder,
}: {
  value: string
  onChange: (v: string) => void
  hint: string
  placeholder?: string
}) {
  const { t } = useTranslation(["quota", "common"])
  return (
    <Field label={t("editor.env")} hint={hint}>
      <Textarea
        rows={4}
        className="reading text-xs"
        value={value}
        placeholder={placeholder ?? t("editor.env_placeholder")}
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  )
}

/** 每行一条的文本 → 字符串数组。 */
function lines(text: string): string[] {
  return text
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean)
}

/**
 * 「目标字段=路径」的映射文本 → 对象。
 *
 * 值以 `=` 开头表示字面量（`unit==CREDITS` 里的第二个 `=`）。
 * **标记要原样带回后端**：`quota.MapRow` 判的就是"值以 `=` 开头"，
 * 只留值的话 `unit==CREDITS` 会变成一条字段路径 `CREDITS`——上游没有这个
 * 字段，这一条就被静默丢掉，界面与配置里都看不出差别。
 */
function parseMapText(text: string): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const line of lines(text)) {
    const i = line.indexOf("=")
    if (i < 0) continue
    const key = line.slice(0, i).trim()
    let rest = line.slice(i + 1)
    if (rest.startsWith("=")) {
      out[key] = "=" + rest.slice(1).trim()
      continue
    }
    rest = rest.trim()
    // 纯数字当数字传，其余当字符串（后端两种都认，这里只是少一层转换）
    out[key] = rest !== "" && !Number.isNaN(Number(rest)) ? Number(rest) : rest
  }
  return out
}

function parseKV(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of lines(text)) {
    const i = line.indexOf("=")
    if (i < 0) continue
    out[line.slice(0, i).trim()] = line.slice(i + 1)
  }
  return out
}

function parseJSON(text: string): unknown {
  const s = text.trim()
  if (!s) return undefined
  try {
    return JSON.parse(s)
  } catch {
    return undefined
  }
}

/** 编辑器的表单初值。src 为 null 表示新增。 */
function initial(src: QuotaSource | null): QuotaSource {
  if (!src) {
    return { id: "", name: "", enabled: true, type: "builtin", builtin: "deepseek" }
  }
  return { ...src }
}

/**
 * 六个多行文本框的初值。它们与结构化字段一一对应，回填规则集中在
 * `lib/quota.ts`（那一层有测试钉住，也有"值以 = 开头即字面量"这类约定）。
 */
function textsOf(src: QuotaSource | null) {
  return {
    // 请求头用"每行一条 KEY=VALUE"，与 constants / map / env 同一套写法。
    // 原来是 JSON 文本框：写错一个逗号就被静默丢掉（build 里判不出对象就不写），
    // 而且"加一个头"要先学会 JSON 的语法——这层语法不是这一步要说的事。
    headers: kvToText(src?.headers),
    map: kvToText(src?.map),
    query: queryToText(src?.query),
    body: jsonToText(src?.body),
    constants: kvToText(src?.constants),
    env: kvToText(src?.env),
  }
}

/**
 * 数据源编辑器。
 *
 * 三种类型分派到不同的表单，但**共用**名称 / 启用 / 备注 / 超时 / 告警阈值
 * 这几个横切字段——它们的含义与类型无关，放进各自表单会重复三遍。
 *
 * 高级项（请求定制与字段映射）默认折叠：只有适配器预期与实际接口不符时才要动，
 * 把八个输入框常年摊在主要路径上，会淹没真正需要填的那两���个。
 */
export function QuotaEditorDialog({
  open,
  onOpenChange,
  source,
  builtins,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /**
   * 表单初值：编辑时是配置里那份**完整且已脱敏**的源（密钥是掩码，保存时
   * 由服务端还原成真值），null 表示新增。
   */
  source: QuotaSource | null
  builtins: QuotaBuiltinInfo[]
  defaults: { refresh: number; warning: number }
  onSaved: (saved: QuotaSource, deleted?: boolean) => void
}) {
  const { t } = useTranslation(["quota", "common"])
  const isNew = source === null

  const [form, setForm] = useState<QuotaSource>(() => initial(source))
  const [texts, setTexts] = useState(() => textsOf(source))
  const [advanced, setAdvanced] = useState(false)

  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<QuotaTestResult | null>(null)
  const [saving, setSaving] = useState(false)
  const [removing, setRemoving] = useState(false)

  useEffect(() => {
    if (!open) return
    setForm(initial(source))
    setTexts(textsOf(source))
    setTest(null)
    setAdvanced(false)
  }, [open, source])

  const setText = (key: keyof ReturnType<typeof textsOf>, value: string) =>
    setTexts((t) => ({ ...t, [key]: value }))

  const curBuiltin = builtins.find((b) => b.id === form.builtin)

  /**
   * 登录型内置适配器（超算 / opencode）的账号、口令、会话 Cookie 都从 env 读，
   * 与脚本类型共用同一套字段。这里以前只在脚本分支渲染，于是这两个适配器
   * 「选得出来、存不下去」——服务端的 ValidateSource 要求登录型的 env 非空。
   */
  const envKeys = curBuiltin?.envKeys ?? []

  const patch = (p: Partial<QuotaSource>) => setForm((f) => ({ ...f, ...p }))

  /** 界面上是"每行一条"的文本框，这里拼回结构化配置。 */
  const build = (): QuotaSource => {
    const out: QuotaSource = { ...form }
    const headers = parseKV(texts.headers)
    if (Object.keys(headers).length) out.headers = headers
    const query = texts.query.trim()
      ? Object.fromEntries(new URLSearchParams(texts.query))
      : undefined
    if (query) out.query = query
    if (texts.body.trim()) out.body = parseJSON(texts.body)
    if (texts.constants.trim()) out.constants = parseKV(texts.constants)
    if (texts.map.trim()) out.map = parseMapText(texts.map)
    if (texts.env.trim()) out.env = parseKV(texts.env)
    // 只保留当前类型用得到的字段，避免把上一次的类型残留写进配置
    if (out.type === "builtin") {
      delete out.url
      delete out.auth
      delete out.constants
      delete out.body
    }
    if (out.type === "script") {
      delete out.url
      delete out.apiKey
      delete out.auth
    }
    return out
  }

  const doTest = async () => {
    setTesting(true)
    setTest(null)
    try {
      const res = await testQuotaSource(build())
      setTest(res)
      if (!res.ok) toast.error(t("test.fail"), { description: res.error })
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setTest({ ok: false, items: [], status: "unknown", error: msg, durationMs: 0 })
      toast.error(t("error.test"), { description: msg })
    } finally {
      setTesting(false)
    }
  }

  const doSave = async () => {
    setSaving(true)
    try {
      const payload = build()
      const saved = isNew
        ? await createQuotaSource(payload)
        : await updateQuotaSource(payload)
      onSaved(saved)
    } catch (err) {
      toast.error(t("error.save"), {
        description: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setSaving(false)
    }
  }

  const doDelete = async () => {
    if (!source) return
    setRemoving(true)
    try {
      await deleteQuotaSource(source.id)
      onSaved({ ...form, id: source.id }, true)
    } catch (err) {
      toast.error(t("error.delete"), {
        description: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setRemoving(false)
    }
  }

  const typeHint = t(
    (form.type === "builtin"
      ? "editor.type_builtin_hint"
      : form.type === "http"
        ? "editor.type_http_hint"
        : "editor.type_script_hint") as never,
    { defaultValue: "" }
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{isNew ? t("editor.new_title") : t("editor.edit_title")}</DialogTitle>
          <DialogDescription>{typeHint}</DialogDescription>
        </DialogHeader>

        {/* min-w-0 不是装饰：DialogContent 是 grid，这一层是它的网格项，网格项默认
            min-width:auto，会把所在轨道撑到内容的 min-content 宽。而 Textarea 带
            field-sizing:content（见 components/ui/textarea.tsx），粘贴一条长凭据
            （会话 Cookie / API Key）时它的 min-content 就是那一整行的宽度——实测把
            622px 的输入框撑成 3722px，直接顶出对话框。允许这一层收缩后，轨道回到
            可用宽度，输入框仍是 100% 宽，长内容改为换行、由高度自己长。 */}
        <div className="flex min-w-0 flex-col gap-4 py-2">
          {/* ---- 横切字段 ---- */}
          <Field label={t("editor.name")}>
            <div className="flex flex-wrap items-center gap-3">
              <Input
                value={form.name}
                placeholder={t("editor.name_placeholder")}
                onChange={(e) => patch({ name: e.target.value })}
                className="max-w-xs"
              />
              <label className="flex items-center gap-2 text-sm">
                <Switch checked={form.enabled} onCheckedChange={(v) => patch({ enabled: v })} />
                {t("editor.enabled")}
              </label>
            </div>
          </Field>

          <Field label={t("editor.type")}>
            <Select value={form.type} onValueChange={(v) => patch({ type: v as QuotaSourceType })}>
              <SelectTrigger className="w-52">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="builtin">{t("type.builtin")}</SelectItem>
                <SelectItem value="http">{t("type.http")}</SelectItem>
                <SelectItem value="script">{t("type.script")}</SelectItem>
              </SelectContent>
            </Select>
          </Field>

          <Field label={t("editor.id")} hint={t("editor.id_hint")}>
            <Input
              value={form.id}
              disabled={!isNew}
              placeholder={t("editor.id_placeholder")}
              onChange={(e) => patch({ id: e.target.value })}
              className="max-w-xs"
            />
          </Field>

          {/* ---- 按类型分派 ---- */}
          {form.type === "builtin" && (
            <>
              <Field label={t("editor.builtin")} hint={curBuiltin?.doc}>
                <Select
                  value={form.builtin ?? ""}
                  onValueChange={(v) => patch({ builtin: v })}
                >
                  <SelectTrigger className="w-72">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {builtins.map((b) => (
                      <SelectItem key={b.id} value={b.id}>
                        {b.label}
                        {!b.verified && ` · ${t("editor.builtin_unverified")}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label={t("editor.base_url")}>
                <Input
                  value={form.baseUrl ?? ""}
                  placeholder={curBuiltin?.defaultBaseUrl}
                  onChange={(e) => patch({ baseUrl: e.target.value })}
                />
              </Field>
              <Field label={t("editor.path")} hint={t("editor.path_hint")}>
                <Input
                  value={form.path ?? ""}
                  placeholder={curBuiltin?.defaultPath ?? "/v1/usage"}
                  onChange={(e) => patch({ path: e.target.value })}
                />
              </Field>
              <Field label={t("editor.api_key")}>
                <Input
                  type="password"
                  value={form.apiKey ?? ""}
                  placeholder={t("editor.api_key_placeholder")}
                  onChange={(e) => patch({ apiKey: e.target.value })}
                />
              </Field>
              {envKeys.length > 0 && (
                <EnvField
                  value={texts.env}
                  onChange={(v) => setText("env", v)}
                  hint={t("editor.env_hint_builtin")}
                  // 提示该填哪些键：这些名字是适配器读死的，用户无从猜起
                  placeholder={envKeys.map((k) => `${k}=`).join("\n")}
                />
              )}
            </>
          )}

          {form.type === "http" && (
            <>
              <Field label={t("editor.url")}>
                <Input
                  value={form.url ?? ""}
                  placeholder={t("editor.url_placeholder")}
                  onChange={(e) => patch({ url: e.target.value })}
                />
              </Field>
              <Field label={t("editor.auth")}>
                <div className="flex flex-wrap items-center gap-2">
                  <Select
                    value={form.auth?.type ?? "none"}
                    onValueChange={(v) => patch({ auth: v === "none" ? undefined : { type: v } })}
                  >
                    <SelectTrigger className="w-36">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">{t("editor.auth_none")}</SelectItem>
                      <SelectItem value="bearer">Bearer</SelectItem>
                      <SelectItem value="header">Header</SelectItem>
                      <SelectItem value="basic">Basic</SelectItem>
                    </SelectContent>
                  </Select>
                  {form.auth && form.auth.type !== "none" && (
                    <>
                      {/* basic 的用户名没有默认值可回落，缺了它 basic 就是"空用户名+口令"，
                          服务端只会把 user 拼成空串发出去，谁也看不出哪里不对 */}
                      {form.auth.type === "basic" && (
                        <Input
                          className="max-w-44"
                          value={form.auth.user ?? ""}
                          placeholder={t("editor.auth_user_placeholder")}
                          onChange={(e) =>
                            patch({ auth: { ...(form.auth ?? { type: "basic" }), user: e.target.value } })
                          }
                        />
                      )}
                      {/* basic 的头名不由这里决定（后端固定发 Authorization），
                          摆一个改了不生效的输入框比不摆更费解 */}
                      {form.auth.type !== "basic" && (
                        <Input
                          className="max-w-44"
                          value={form.auth.header ?? ""}
                          placeholder={t("editor.auth_header_placeholder")}
                          onChange={(e) =>
                            patch({ auth: { ...(form.auth ?? { type: "bearer" }), header: e.target.value } })
                          }
                        />
                      )}
                      <Input
                        type="password"
                        className="max-w-56"
                        value={form.apiKey ?? ""}
                        placeholder={t("editor.auth_secret_placeholder")}
                        onChange={(e) => patch({ apiKey: e.target.value })}
                      />
                    </>
                  )}
                </div>
              </Field>
              <Field label={t("editor.body")}>
                <Textarea
                  rows={3}
                  value={texts.body}
                  placeholder={t("editor.body_placeholder")}
                  onChange={(e) => setText("body", e.target.value)}
                />
              </Field>
            </>
          )}

          {form.type === "script" && (
            <>
              <Field label={t("editor.script_source")} hint={t("editor.script_hint")}>
                <div className="flex flex-col gap-2">
                  <Textarea
                    rows={14}
                    spellCheck={false}
                    className="reading text-xs"
                    value={form.scriptSource ?? ""}
                    onChange={(e) => patch({ scriptSource: e.target.value })}
                  />
                  <Button
                    variant="ghost"
                    size="sm"
                    className="self-start px-0 text-xs"
                    onClick={() => patch({ scriptSource: SCRIPT_SAMPLE })}
                  >
                    {t("editor.script_sample")}
                  </Button>
                </div>
              </Field>
              <EnvField
                value={texts.env}
                onChange={(v) => setText("env", v)}
                hint={t("editor.env_hint")}
              />
              <label className="flex items-start gap-2 text-sm">
                <Switch
                  checked={!!form.allowFetch}
                  onCheckedChange={(v) => patch({ allowFetch: v })}
                />
                <span>
                  {t("editor.allow_fetch")}
                  <span className="block text-[11px] text-muted-foreground">
                    {t("editor.allow_fetch_hint")}
                  </span>
                </span>
              </label>
            </>
          )}

          {/* ---- 高级（请求定制与字段映射）：内置与 HTTP 都需要 ---- */}
          {form.type !== "script" && (
            <Collapsible open={advanced} onOpenChange={setAdvanced}>
              <CollapsibleTrigger asChild>
                <Button variant="ghost" size="sm" className="justify-start px-0 text-xs">
                  {advanced ? t("editor.advanced_hide") : t("editor.advanced_show")}
                </Button>
              </CollapsibleTrigger>
              <CollapsibleContent className="flex flex-col gap-4 pt-3">
                <Field label={t("editor.method")}>
                  <Select value={form.method || "GET"} onValueChange={(v) => patch({ method: v })}>
                    <SelectTrigger className="w-28">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="GET">GET</SelectItem>
                      <SelectItem value="POST">POST</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
                <Field label={t("editor.query")} hint={t("editor.query_hint")}>
                  <Input
                    value={texts.query}
                    placeholder={t("editor.query_placeholder")}
                    onChange={(e) => setText("query", e.target.value)}
                  />
                </Field>
                <Field label={t("editor.headers")} hint={t("editor.headers_hint")}>
                  <Textarea
                    rows={3}
                    className="reading text-xs"
                    value={texts.headers}
                    placeholder={t("editor.headers_placeholder")}
                    onChange={(e) => setText("headers", e.target.value)}
                  />
                </Field>
                <Field label={t("editor.items_path")}>
                  <Input
                    value={form.itemsPath ?? ""}
                    placeholder={t("editor.items_path_placeholder")}
                    onChange={(e) => patch({ itemsPath: e.target.value })}
                  />
                </Field>
                <Field label={t("editor.map")} hint={t("editor.map_hint")}>
                  <Textarea
                    rows={5}
                    className="reading text-xs"
                    value={texts.map}
                    placeholder={t("editor.map_placeholder")}
                    onChange={(e) => setText("map", e.target.value)}
                  />
                </Field>
                {form.type === "http" && (
                  <Field label={t("editor.constants")}>
                    <Textarea
                      rows={2}
                      className="reading text-xs"
                      value={texts.constants}
                      placeholder={t("editor.constants_placeholder")}
                      onChange={(e) => setText("constants", e.target.value)}
                    />
                  </Field>
                )}
              </CollapsibleContent>
            </Collapsible>
          )}

          <div className="flex flex-wrap gap-4">
            <Field label={t("editor.timeout")} hint={t("editor.timeout_hint")}>
              <Input
                type="number"
                min={0}
                className="w-24"
                value={form.timeout ?? 0}
                onChange={(e) => patch({ timeout: Number(e.target.value) || 0 })}
              />
            </Field>
            <Field label={t("editor.warning_at")} hint={t("editor.warning_at_hint")}>
              <Input
                type="number"
                min={0}
                max={100}
                className="w-24"
                value={form.warningAt ?? 0}
                onChange={(e) => patch({ warningAt: Number(e.target.value) || 0 })}
              />
            </Field>
          </div>

          <Field label={t("editor.note")}>
            <Input
              value={form.note ?? ""}
              placeholder={t("editor.note_placeholder")}
              onChange={(e) => patch({ note: e.target.value })}
            />
          </Field>

          {test && <TestPanel result={test} />}
        </div>

        <DialogFooter className="gap-2 sm:justify-end">
          {!isNew && (
            <Button
              variant="destructive"
              className="sm:mr-auto"
              disabled={removing || saving}
              onClick={() => void doDelete()}
            >
              {removing && <Loader2 className="size-3.5 animate-spin" />}
              {t("common:actions.delete")}
            </Button>
          )}
          <Button variant="outline" disabled={testing || saving} onClick={() => void doTest()}>
            {testing && <Loader2 className="size-3.5 animate-spin" />}
            {testing ? t("test.running") : t("test.run")}
          </Button>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common:actions.cancel")}
          </Button>
          <Button disabled={saving || removing} onClick={() => void doSave()}>
            {saving && <Loader2 className="size-3.5 animate-spin" />}
            {saving ? t("common:actions.saving") : t("common:actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * 试跑结果面板。
 *
 * 这是配额功能里最有价值的作者循环：改脚本/映射 → 立刻看到解析结果与原始产出。
 * 因此它**同时**给三样东西：
 *   1. 归一后的条目（用户实际会看到的样子）
 *   2. 适配器返回的原始值（排查"为什么认不出"）
 *   3. 原始 stdout / stderr（排查脚本本身）
 *
 * 后两者默认折叠：绝大多数时候看的是第 1 项。
 */
function TestPanel({ result }: { result: QuotaTestResult }) {
  const { t } = useTranslation(["quota", "common"])
  const [rawOpen, setRawOpen] = useState(false)

  return (
    <div className="flex flex-col gap-2 rounded-md border border-border bg-muted/30 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span
          className={
            result.ok
              ? "rounded-sm bg-status-good/10 px-1.5 py-0.5 text-xs font-medium text-status-good-ink"
              : "rounded-sm bg-status-critical/10 px-1.5 py-0.5 text-xs font-medium text-status-critical-ink"
          }
        >
          {result.ok ? t("test.ok") : t("test.fail")}
        </span>
        <span className="reading text-xs text-muted-foreground">
          {t("test.meta", {
            ms: result.durationMs,
            count: itemsOf(result).length,
            defaultValue: "{{ms}}ms",
          })}
        </span>
      </div>

      {result.error && (
        <pre className="reading max-h-40 overflow-auto text-xs break-all whitespace-pre-wrap text-status-critical-ink">
          {result.error}
        </pre>
      )}

      {result.warning && (
        <p className="text-xs text-status-warning-ink">{result.warning}</p>
      )}

      {itemsOf(result).length ? (
        <div className="flex flex-col gap-1">
          {itemsOf(result).map((it) => (
            <div key={it.id} className="flex items-center gap-2 text-xs">
              <span className="min-w-0 flex-1 truncate">{it.label}</span>
              <span className="reading shrink-0">
                {it.text || renderItemText(it)}
              </span>
              {it.percent !== null && unitKindOf(it.unit) !== "unknown" && (
                <span className="reading w-14 shrink-0 text-right text-muted-foreground">
                  {Math.round(it.percent * 10) / 10}%
                </span>
              )}
            </div>
          ))}
        </div>
      ) : (
        result.ok && <p className="text-xs text-muted-foreground">{t("test.no_items")}</p>
      )}

      {(result.rawValue !== undefined || result.error) && (
        <Collapsible open={rawOpen} onOpenChange={setRawOpen}>
          <CollapsibleTrigger asChild>
            <Button variant="ghost" size="sm" className="justify-start px-0 text-xs">
              {t("test.raw")}
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="pt-2">
            {result.rawValue !== undefined && (
              <div className="max-h-56 overflow-auto rounded-sm border border-border bg-background p-2">
                <JsonTree data={result.rawValue} />
              </div>
            )}
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  )
}
