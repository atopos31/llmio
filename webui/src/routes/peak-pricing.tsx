import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { ArrowDown, ArrowUp, Loader2, Plus, RefreshCw, Trash2 } from "lucide-react"
import { toast } from "sonner"

import { ErrorState, ListSkeleton } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { getPeakPricing, previewPeakPricing, syncPeakHolidays, updatePeakPricing } from "@/lib/api"
import {
  PREVIEW_DAY_OPTIONS,
  WEEKDAY_DISPLAY_ORDER,
  findPeriodConflicts,
  formatMultiplier,
  formatSchedulePoint,
  formToPayload,
  holidayRowsFromOverrides,
  pricingToForm,
  validatePeakForm,
  weekdayKey,
  type PeakForm,
  type PeakIssue,
  type PeakPeriodForm,
  type PeakPricing,
  type SchedulePoint,
  type WorkdayFilter,
} from "@/lib/peak"

/**
 * 峰谷计费的界面：一张入口卡片 + 一个编辑对话框。
 *
 * 卡片自己取数（而不是让配置页把它的配置一起拉回来）：这一页的其余两张卡
 * 走的是通用 config 端点，而峰谷计费是独立的 /peak-pricing 端点，失败与
 * 加载的粒度也不同——混进配置页那一次 Promise.all，一处读失败就会把整页
 * 说成"读取现有配置失败"，而这页上的另外两张卡其实是好的。
 *
 * 四态因此落在卡片自己身上：加载中 / 未开启（后端返回的默认配置）/ 读取失败
 * / 有配置可编辑。
 */

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export function PeakPricingCard() {
  const { t } = useTranslation(["peak", "common"])
  const [config, setConfig] = useState<PeakPricing | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [open, setOpen] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      // 未配置过时后端返回默认配置（关闭状态）而不是空值，因此"没有配置"
      // 在界面上就是"未开启 + 一份默认时段"，这里不需要自己拼一份默认值
      setConfig(await getPeakPricing())
    } catch (err) {
      setLoadError(errorText(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium">{t("title")}</CardTitle>
        <CardDescription className="text-[11px]">{t("desc")}</CardDescription>
      </CardHeader>

      <CardContent className="space-y-4">
        {loading ? (
          <ListSkeleton label={t("common:loading")} rows={3} />
        ) : loadError ? (
          <ErrorState
            title={t("load_failed")}
            message={loadError}
            retryLabel={t("retry")}
            onRetry={() => void load()}
          />
        ) : config ? (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">{t("status")}</span>
              <p className="text-sm">{config.enabled ? t("status_on") : t("status_off")}</p>
            </div>
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">{t("timezone")}</span>
              <p className="text-sm">{config.timezone || t("timezone_server")}</p>
            </div>
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">{t("periods")}</span>
              <p className="text-sm">
                {config.periods.length === 0
                  ? t("periods_none")
                  : t("periods_value", { n: config.periods.length })}
              </p>
            </div>
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">{t("holiday")}</span>
              <p className="text-sm text-muted-foreground">
                {config.holidaySyncedAt
                  ? t("holiday_synced", {
                      time: new Date(config.holidaySyncedAt * 1000).toLocaleString(),
                      source: config.holidaySource || t("common:unknown"),
                    })
                  : t("holiday_never")}
              </p>
            </div>
            {!config.enabled && (
              <p className="text-xs text-muted-foreground md:col-span-2">{t("disabled_hint")}</p>
            )}
          </div>
        ) : null}
      </CardContent>

      <CardFooter>
        <Button disabled={loading || !config} onClick={() => setOpen(true)}>
          {t("edit")}
        </Button>
      </CardFooter>

      {config && (
        <PeakPricingDialog
          open={open}
          onOpenChange={setOpen}
          config={config}
          onConfigChange={setConfig}
        />
      )}
    </Card>
  )
}

/**
 * 峰谷计费编辑器。
 *
 * 表单用 useState 而不是 react-hook-form：这里的字段是**可增删的数组**
 * （时段、日期覆盖），而 zod + RHF 的 useFieldArray 会把"哪一段的第几项"
 * 编成路径字符串，校验信息也就跟着变成路径，报给用户时又要翻译回"第几段"。
 * 本地校验（lib/peak.ts 的 validatePeakForm）本来就返回"第几段、哪一处"，
 * 直接渲染它更直接。
 *
 * 保存与预览共用同一份校验：都先跑 validatePeakForm，问题就地列出，
 * 不去换一次往返再把后端那句英文抖出来。后端仍是权威——它返回的 message
 * 原文透出（见 saveError / syncError），不用"保存失败"四个字盖掉。
 */
export function PeakPricingDialog({
  open,
  onOpenChange,
  config,
  onConfigChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  config: PeakPricing
  /** 保存或同步成功后把**服务端返回的那一份**交给父组件，父页面的卡片随之更新 */
  onConfigChange: (next: PeakPricing) => void
}) {
  const { t } = useTranslation(["peak", "common"])

  const [form, setForm] = useState<PeakForm>(() => pricingToForm(config))
  const [issues, setIssues] = useState<PeakIssue[]>([])
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  const [previewDays, setPreviewDays] = useState(7)
  const [previewing, setPreviewing] = useState(false)
  const [preview, setPreview] = useState<SchedulePoint[] | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)

  const [syncYear, setSyncYear] = useState(() => String(new Date().getFullYear()))
  const [syncing, setSyncing] = useState(false)
  const [syncError, setSyncError] = useState<string | null>(null)

  // 每次**打开**都从服务端那份重新回填：对话框不是"草稿箱"，上一次没保存的
  // 编辑不该悄悄留在下一次打开里。
  //
  // 依赖里刻意只有 `open`，不带 `config`。`config` 会在两处被换掉：保存成功、
  // 以及同步节假日后把服务端那一份回传给父组件（卡片要立刻显示"最近同步于…"）。
  // 后一种情况带着的是**旧的服务端版本**，里面没有用户手上还没保存的改动——
  // 把它接进依赖，等于"点一下同步就把你刚改的开关和时段全部抹回服务端版本"，
  // 而且界面一声不响。同步结果里真正需要进表单的三项（覆盖表、同步时间、来源）
  // 由 doSync 自己合并，不走这条通路。
  useEffect(() => {
    if (!open) return
    setForm(pricingToForm(config))
    setIssues([])
    setSaveError(null)
    setPreview(null)
    setPreviewError(null)
    setSyncError(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 见上：config 换一份不等于"重新开草稿"
  }, [open])

  const payload = formToPayload(form)
  const conflicts = findPeriodConflicts(payload.periods)

  const patchPeriod = (index: number, patch: Partial<PeakPeriodForm>) =>
    setForm((f) => ({
      ...f,
      periods: f.periods.map((p, i) => (i === index ? { ...p, ...patch } : p)),
    }))

  const addPeriod = () =>
    setForm((f) => ({
      ...f,
      periods: [
        ...f.periods,
        { name: "", start: "00:00", end: "00:30", multiplier: "1", days: [], workday: "any" },
      ],
    }))

  const removePeriod = (index: number) =>
    setForm((f) => ({ ...f, periods: f.periods.filter((_, i) => i !== index) }))

  /**
   * 上移 / 下移一段。
   *
   * 动的不是"展示顺序"，而是**判定优先级**：后端 ResolvePeriod 首个命中者
   * 胜出，数组顺序就是优先级。因此这里换的是两条配置的位置，不是排序显示。
   */
  const movePeriod = (index: number, delta: number) =>
    setForm((f) => {
      const target = index + delta
      if (target < 0 || target >= f.periods.length) return f
      const next = [...f.periods]
      const tmp = next[index]
      next[index] = next[target]
      next[target] = tmp
      return { ...f, periods: next }
    })

  const patchHoliday = (index: number, patch: Partial<{ date: string; kind: string }>) =>
    setForm((f) => ({
      ...f,
      holidays: f.holidays.map((h, i) => (i === index ? { ...h, ...patch } : h)),
    }))

  const addHoliday = () =>
    setForm((f) => ({ ...f, holidays: [...f.holidays, { date: "", kind: "rest" }] }))

  const removeHoliday = (index: number) =>
    setForm((f) => ({ ...f, holidays: f.holidays.filter((_, i) => i !== index) }))

  /** 本地校验。有问题就摆出来并返回 false，调用方不再往下走。 */
  const check = (): boolean => {
    const found = validatePeakForm(form)
    setIssues(found)
    return found.length === 0
  }

  const doSave = async () => {
    setSaveError(null)
    if (!check()) return
    setSaving(true)
    try {
      const saved = await updatePeakPricing(payload)
      onConfigChange(saved)
      toast.success(t("toast_saved"))
      onOpenChange(false)
    } catch (err) {
      // 后端 message 是唯一能定位问题的信息，原样放出来
      setSaveError(errorText(err))
    } finally {
      setSaving(false)
    }
  }

  const doPreview = async () => {
    setPreviewError(null)
    if (!check()) return
    setPreviewing(true)
    try {
      setPreview(await previewPeakPricing(payload, previewDays))
    } catch (err) {
      setPreview(null)
      setPreviewError(errorText(err))
    } finally {
      setPreviewing(false)
    }
  }

  const doSync = async () => {
    setSyncError(null)
    setSyncing(true)
    try {
      const res = await syncPeakHolidays(Number(syncYear))
      // 同步在服务端就已经落盘了，这里把返回的那一份同时更新到表单与父页面。
      // 不依赖父组件的 state 更新回流到 props：对话框不该等一次 round-trip 才刷新
      setForm((f) => ({
        ...f,
        holidays: holidayRowsFromOverrides(res.config.dateOverrides ?? {}),
        holidaySyncedAt: res.config.holidaySyncedAt,
        holidaySource: res.config.holidaySource,
      }))
      onConfigChange(res.config)
      toast.success(t("toast_synced", { n: res.count, source: res.source }))
    } catch (err) {
      // 同步是外网请求，后端用 502 回，原因在 message 里；给原文 + 重试
      setSyncError(errorText(err))
    } finally {
      setSyncing(false)
    }
  }

  const issueText = (issue: PeakIssue) =>
    // i18next 的键是拼出来的，类型系统给不出返回值类型；String() 收下它，
    // 免得为了一个动态键把整段 JSX 都写成 unknown 转换
    String(
      t(`issue.${issue.key}` as never, {
        index: issue.index ?? 0,
        name: issue.name || t("period_unnamed"),
        value: issue.value ?? "",
      })
    )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("edit_title")}</DialogTitle>
          <DialogDescription>{t("edit_desc")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-5 py-2">
          {/* ---- 开关与时区 ---- */}
          <label className="flex items-start justify-between gap-3 rounded-lg border p-3">
            <span className="space-y-0.5">
              <span className="block text-sm font-medium">{t("enabled")}</span>
              <span className="block text-[11px] text-muted-foreground">{t("enabled_hint")}</span>
            </span>
            <Switch
              checked={form.enabled}
              onCheckedChange={(v) => setForm((f) => ({ ...f, enabled: v }))}
              aria-label={t("enabled")}
            />
          </label>

          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted-foreground">{t("timezone")}</span>
            <Input
              className="max-w-xs"
              value={form.timezone}
              placeholder={t("timezone_placeholder")}
              onChange={(e) => setForm((f) => ({ ...f, timezone: e.target.value }))}
            />
            <span className="text-[11px] text-muted-foreground">{t("timezone_hint")}</span>
          </div>

          {/* ---- 工作日定义 ---- */}
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted-foreground">{t("weekdays")}</span>
            <ToggleGroup
              type="multiple"
              variant="outline"
              size="sm"
              value={form.weekdays.map(String)}
              onValueChange={(v) =>
                setForm((f) => ({ ...f, weekdays: v.map(Number).sort((a, b) => a - b) }))
              }
              aria-label={t("weekdays")}
            >
              {WEEKDAY_DISPLAY_ORDER.map((d) => (
                <ToggleGroupItem key={d} value={String(d)} size="sm" className="px-2">
                  {t(weekdayKey(d) as never) as string}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <span className="text-[11px] text-muted-foreground">{t("weekdays_hint")}</span>
          </div>

          {/* ---- 时段列表 ---- */}
          <div className="flex flex-col gap-2">
            <span className="text-xs font-medium text-muted-foreground">{t("period_list")}</span>
            <ul aria-label={t("period_list")} className="flex flex-col gap-3">
              {form.periods.map((p, index) => (
                <li key={index} className="flex flex-col gap-3 rounded-lg border p-3">
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-medium text-muted-foreground">
                      {t("period_index", { index: index + 1 })}
                    </span>
                    <div className="ms-auto flex items-center gap-1">
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 px-0"
                        disabled={index === 0}
                        aria-label={`${t("period_index", { index: index + 1 })} ${t("period_up")}`}
                        onClick={() => movePeriod(index, -1)}
                      >
                        <ArrowUp className="size-3.5" aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 px-0"
                        disabled={index === form.periods.length - 1}
                        aria-label={`${t("period_index", { index: index + 1 })} ${t("period_down")}`}
                        onClick={() => movePeriod(index, 1)}
                      >
                        <ArrowDown className="size-3.5" aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 px-0"
                        aria-label={`${t("period_index", { index: index + 1 })} ${t("period_remove")}`}
                        onClick={() => removePeriod(index)}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                      </Button>
                    </div>
                  </div>

                  <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                    <label className="flex flex-col gap-1">
                      <span className="text-[11px] text-muted-foreground">{t("period_name")}</span>
                      <Input
                        value={p.name}
                        placeholder={t("period_name_placeholder")}
                        onChange={(e) => patchPeriod(index, { name: e.target.value })}
                      />
                    </label>
                    <label className="flex flex-col gap-1">
                      <span className="text-[11px] text-muted-foreground">{t("period_start")}</span>
                      {/*
                        用文本输入而不是 <input type="time">：后者的最大值是 23:59，
                        而"全天"这个时段只能写成 00:00 → 24:00（后端的 ParseClock
                        专门放了 24:00 这一支，见 service/peak.go）。选原生控件就等于
                        让一种后端明确支持的配置在界面上无法表达。跨零点（22:00 →
                        06:00）同理，靠规则而不是控件表达。触屏上退而求其次用
                        inputMode=numeric 唤起数字键盘。
                      */}
                      <Input
                        inputMode="numeric"
                        maxLength={5}
                        placeholder={t("period_time_placeholder")}
                        value={p.start}
                        onChange={(e) => patchPeriod(index, { start: e.target.value })}
                      />
                    </label>
                    <label className="flex flex-col gap-1">
                      <span className="text-[11px] text-muted-foreground">{t("period_end")}</span>
                      <Input
                        inputMode="numeric"
                        maxLength={5}
                        placeholder={t("period_time_placeholder")}
                        value={p.end}
                        onChange={(e) => patchPeriod(index, { end: e.target.value })}
                      />
                    </label>
                    <label className="flex flex-col gap-1">
                      <span className="text-[11px] text-muted-foreground">{t("multiplier")}</span>
                      <Input
                        type="number"
                        min={0}
                        step={0.05}
                        value={p.multiplier}
                        onChange={(e) => patchPeriod(index, { multiplier: e.target.value })}
                      />
                    </label>
                  </div>
                  <p className="text-[11px] text-muted-foreground">
                    {t("period_time_hint")} {t("multiplier_hint")}
                  </p>

                  <div className="flex flex-wrap items-end gap-4">
                    <div className="flex flex-col gap-1">
                      <span className="text-[11px] text-muted-foreground">{t("days")}</span>
                      <ToggleGroup
                        type="multiple"
                        variant="outline"
                        size="sm"
                        value={p.days.map(String)}
                        onValueChange={(v) =>
                          patchPeriod(index, { days: v.map(Number).sort((a, b) => a - b) })
                        }
                        aria-label={`${t("period_index", { index: index + 1 })} ${t("days")}`}
                      >
                        {WEEKDAY_DISPLAY_ORDER.map((d) => (
                          <ToggleGroupItem key={d} value={String(d)} size="sm" className="px-2">
                            {t(weekdayKey(d) as never) as string}
                          </ToggleGroupItem>
                        ))}
                      </ToggleGroup>
                    </div>

                    <div className="flex flex-col gap-1">
                      <span className="text-[11px] text-muted-foreground">
                        {t("workday")}
                      </span>
                      <ToggleGroup
                        type="single"
                        variant="outline"
                        size="sm"
                        value={p.workday}
                        // single 型允许再点一次清空，而"不限"本身就是那个空位，
                        // 因此空值要落回 any
                        onValueChange={(v) =>
                          patchPeriod(index, { workday: (v || "any") as WorkdayFilter })
                        }
                        aria-label={`${t("period_index", { index: index + 1 })} ${t("workday")}`}
                      >
                        <ToggleGroupItem value="any" size="sm" className="px-2">
                          {t("workday_any")}
                        </ToggleGroupItem>
                        <ToggleGroupItem value="work" size="sm" className="px-2">
                          {t("workday_work")}
                        </ToggleGroupItem>
                        <ToggleGroupItem value="rest" size="sm" className="px-2">
                          {t("workday_rest")}
                        </ToggleGroupItem>
                      </ToggleGroup>
                    </div>
                  </div>
                </li>
              ))}
            </ul>
            <span className="text-[11px] text-muted-foreground">{t("days_hint")}</span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="self-start gap-1.5"
              onClick={addPeriod}
            >
              <Plus className="size-3.5" aria-hidden="true" />
              {t("period_add")}
            </Button>
          </div>

          {/*
            重叠只提示不拦：后端没有重叠校验，先命中者胜出。把提示写成
            "谁先命中"而不是"请修改"，是因为这两段同时存在本身就是一种合法表达。
          */}
          {conflicts.length > 0 && (
            <ul className="flex flex-col gap-1 rounded-md border border-status-warning/40 bg-status-warning/5 p-3">
              {conflicts.map(([a, b]) => (
                <li key={`${a}-${b}`} className="text-xs text-status-warning-ink">
                  {t("warn_conflict", { a: a + 1, b: b + 1 })}
                </li>
              ))}
            </ul>
          )}

          {form.enabled && form.periods.length === 0 && (
            <p className="rounded-md border border-status-warning/40 bg-status-warning/5 p-3 text-xs text-status-warning-ink">
              {t("warn_no_periods")}
            </p>
          )}

          {/* ---- 本地校验结果 ---- */}
          {issues.length > 0 && (
            <div className="flex flex-col gap-1 rounded-md border border-status-critical/40 bg-status-critical/5 p-3">
              <p className="text-xs font-medium text-status-critical-ink">
                {t("errors_title", { n: issues.length })}
              </p>
              <ul className="flex flex-col gap-1">
                {issues.map((issue, i) => (
                  <li key={`${issue.key}-${issue.index ?? ""}-${i}`} className="reading text-xs text-status-critical-ink">
                    {issueText(issue)}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {/* ---- 节假日 ---- */}
          <div className="flex flex-col gap-2">
            <span className="text-xs font-medium text-muted-foreground">{t("holiday_rows")}</span>
            <div className="flex flex-wrap items-end gap-2">
              <label className="flex flex-col gap-1">
                <span className="text-[11px] text-muted-foreground">{t("holiday_year")}</span>
                <Input
                  className="w-24"
                  inputMode="numeric"
                  value={syncYear}
                  onChange={(e) => setSyncYear(e.target.value)}
                />
              </label>
              <Button
                type="button"
                variant="outline"
                className="gap-1.5"
                disabled={syncing}
                onClick={() => void doSync()}
              >
                {syncing ? (
                  <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                ) : (
                  <RefreshCw className="size-3.5" aria-hidden="true" />
                )}
                {syncing ? t("holiday_syncing") : t("holiday_sync")}
              </Button>
              {form.holidaySyncedAt ? (
                <span className="text-[11px] text-muted-foreground">
                  {t("holiday_synced", {
                    time: new Date(form.holidaySyncedAt * 1000).toLocaleString(),
                    source: form.holidaySource || t("common:unknown"),
                  })}
                </span>
              ) : null}
            </div>

            {syncError && (
              <div className="flex flex-wrap items-center gap-2 rounded-md border border-status-critical/40 bg-status-critical/5 p-3">
                <span className="text-xs font-medium text-status-critical-ink">
                  {t("holiday_sync_failed")}
                </span>
                <span className="reading min-w-0 flex-1 break-all text-xs text-status-critical-ink">
                  {syncError}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={syncing}
                  onClick={() => void doSync()}
                >
                  {t("holiday_sync_retry")}
                </Button>
              </div>
            )}

            {form.holidays.length === 0 ? (
              <p className="text-xs text-muted-foreground">{t("holiday_empty")}</p>
            ) : (
              <ul aria-label={t("holiday_rows")} className="flex flex-col gap-2">
                {form.holidays.map((h, index) => (
                  <li key={index} className="flex flex-wrap items-center gap-2">
                    {/* 日期用原生的 date 控件：它的值格式恰好就是后端要的 YYYY-MM-DD，
                        既不用自己拼也没有解析歧义 */}
                    <Input
                      type="date"
                      className="w-40"
                      aria-label={t("holiday_date")}
                      value={h.date}
                      onChange={(e) => patchHoliday(index, { date: e.target.value })}
                    />
                    <ToggleGroup
                      type="single"
                      variant="outline"
                      size="sm"
                      value={h.kind}
                      onValueChange={(v) => v && patchHoliday(index, { kind: v })}
                      aria-label={t("holiday_kind")}
                    >
                      <ToggleGroupItem value="rest" size="sm" className="px-2">
                        {t("holiday_kind_rest")}
                      </ToggleGroupItem>
                      <ToggleGroupItem value="work" size="sm" className="px-2">
                        {t("holiday_kind_work")}
                      </ToggleGroupItem>
                    </ToggleGroup>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 px-0"
                      aria-label={t("holiday_row_remove")}
                      onClick={() => removeHoliday(index)}
                    >
                      <Trash2 className="size-3.5" aria-hidden="true" />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="self-start gap-1.5"
              onClick={addHoliday}
            >
              <Plus className="size-3.5" aria-hidden="true" />
              {t("holiday_row_add")}
            </Button>
            <span className="text-[11px] text-muted-foreground">{t("holiday_hint")}</span>
          </div>

          {/* ---- 预览 ---- */}
          <div className="flex flex-col gap-2 rounded-lg border p-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs font-medium text-muted-foreground">{t("preview")}</span>
              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                value={String(previewDays)}
                onValueChange={(v) => v && setPreviewDays(Number(v))}
                aria-label={t("preview_days")}
              >
                {PREVIEW_DAY_OPTIONS.map((d) => (
                  <ToggleGroupItem key={d} value={String(d)} size="sm" className="px-2">
                    {d}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="gap-1.5"
                disabled={previewing}
                onClick={() => void doPreview()}
              >
                {previewing && <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />}
                {previewing ? t("preview_running") : t("preview_run")}
              </Button>
            </div>
            <span className="text-[11px] text-muted-foreground">{t("preview_hint")}</span>

            {previewError && (
              <div className="flex flex-wrap items-center gap-2 rounded-md border border-status-critical/40 bg-status-critical/5 p-3">
                <span className="text-xs font-medium text-status-critical-ink">
                  {t("preview_failed")}
                </span>
                <span className="reading min-w-0 flex-1 break-all text-xs text-status-critical-ink">
                  {previewError}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={previewing}
                  onClick={() => void doPreview()}
                >
                  {t("retry")}
                </Button>
              </div>
            )}

            {preview &&
              (preview.length === 0 ? (
                <p className="text-xs text-muted-foreground">{t("preview_empty")}</p>
              ) : (
                <ul className="flex flex-col gap-1">
                  {preview.map((point) => (
                    <li
                      key={point.start}
                      className="flex flex-wrap items-center gap-2 text-xs"
                    >
                      <span className="reading">
                        {formatSchedulePoint(point, form.timezone)}
                      </span>
                      <span>{point.period || t("preview_base")}</span>
                      <span className="reading text-muted-foreground">
                        {formatMultiplier(point.multiplier)}
                      </span>
                      <span className="text-muted-foreground">
                        {point.workday ? t("preview_workday") : t("preview_restday")}
                      </span>
                    </li>
                  ))}
                </ul>
              ))}
          </div>
        </div>

        <DialogFooter className="gap-2 sm:justify-end">
          {saveError && (
            <span className="reading min-w-0 flex-1 break-all text-xs text-status-critical-ink">
              {saveError}
            </span>
          )}
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common:actions.cancel")}
          </Button>
          <Button type="button" disabled={saving} onClick={() => void doSave()}>
            {saving && <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />}
            {saving ? t("common:actions.saving") : t("common:actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
