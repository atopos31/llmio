import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2, Plus, RefreshCw, Trash2 } from "lucide-react"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { getPeakCalendar, syncPeakHolidays, updatePeakCalendar } from "@/lib/api"
import {
  WEEKDAY_DISPLAY_ORDER,
  calendarFormToPayload,
  calendarToForm,
  holidayRowsFromOverrides,
  validateCalendarForm,
  weekdayKey,
  type PeakCalendar,
  type PeakCalendarForm,
  type PeakIssue,
} from "@/lib/peak"

/**
 * 工作日日历的界面：一张入口卡片 + 一个编辑对话框。
 *
 * **只有日历，没有时段**。时段与乘数是上游的商务条款，住在「模型 × 上游」的
 * 关联编辑器里（见 routes/model-providers/peak-terms-editor.tsx）；而"哪天算
 * 工作日、按哪个时区算今天"对所有上游是同一个答案，搁在这里由一处维护——
 * 每个关联各存一份的话，同步一次节假日要写 N 遍，N 份之间还会不一致。
 *
 * 卡片自己取数（而不是让配置页把它的配置一起拉回来）：这一页的其余两张卡
 * 走的是通用 config 端点，而峰谷日历是独立的 /peak-calendar 端点，失败与
 * 加载的粒度也不同——混进配置页那一次 Promise.all，一处读失败就会把整页
 * 说成"读取现有配置失败"，而这页上的另外两张卡其实是好的。
 *
 * 四态因此落在卡片自己身上：加载中 / 读取失败 / 有数据 / 空态（没有覆盖表、
 * 从未同步，但时区与工作日仍是有效配置）。
 */

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export function PeakCalendarCard() {
  const { t } = useTranslation(["peak", "common"])
  const [config, setConfig] = useState<PeakCalendar | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [open, setOpen] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      // 未配置过时后端返回默认日历而不是空值，因此"没有配置"在界面上
      // 就是"默认时区 + 默认工作日"，这里不需要自己拼一份默认值
      setConfig(await getPeakCalendar())
    } catch (err) {
      setLoadError(errorText(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const overrideCount = Object.keys(config?.dateOverrides ?? {}).length

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium">{t("calendar_title")}</CardTitle>
        <CardDescription className="text-[11px]">{t("calendar_desc")}</CardDescription>
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
              <span className="text-xs font-medium text-muted-foreground">{t("timezone")}</span>
              <p className="text-sm">{config.timezone || t("timezone_server")}</p>
            </div>
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">{t("weekdays")}</span>
              <p className="text-sm">
                {config.weekdays?.length
                  ? t("weekdays_value", { n: config.weekdays.length })
                  : t("weekdays_default")}
              </p>
            </div>
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">{t("overrides")}</span>
              <p className="text-sm">
                {overrideCount === 0
                  ? t("overrides_none")
                  : t("overrides_value", { n: overrideCount })}
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
            {overrideCount === 0 && (
              <p className="text-xs text-muted-foreground md:col-span-2">
                {t("overrides_empty_hint")}
              </p>
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
        <PeakCalendarDialog
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
 * 工作日日历编辑器。
 *
 * 表单用 useState 而不是 react-hook-form：这里的字段是**可增删的数组**
 * （日期覆盖），而 zod + RHF 的 useFieldArray 会把"第几项"编成路径字符串，
 * 校验信息也就跟着变成路径，报给用户时又要翻译回"第几条"。本地校验
 * （lib/peak.ts 的 validateCalendarForm）本来就返回"哪一处"，直接渲染它更直接。
 *
 * 保存与同步共用同一份校验：都先跑 validateCalendarForm，问题就地列出，
 * 不去换一次往返再把后端那句英文抖出来。后端仍是权威——它返回的 message
 * 原文透出（见 saveError / syncError），不用"保存失败"四个字盖掉。
 */
export function PeakCalendarDialog({
  open,
  onOpenChange,
  config,
  onConfigChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  config: PeakCalendar
  /** 保存或同步成功后把**服务端返回的那一份**交给父组件，卡片随之更新 */
  onConfigChange: (next: PeakCalendar) => void
}) {
  const { t } = useTranslation(["peak", "common"])

  const [form, setForm] = useState<PeakCalendarForm>(() => calendarToForm(config))
  /**
   * 是否已经按过一次保存。
   *
   * 输入过程中不弹红：正在把 "Asia/Shanghai" 改成别的时区的中间态天天不合法，
   * 边打边报红只会让人学会无视红色。按过保存之后改成实时——每改一个字结论都
   * 跟着刷新，改好了红色自己消失。（条款编辑器用的是同一套办法，那边由父级
   * 的 submitAttempt 计次，因为保存按钮在关联表单的底部。）
   */
  const [attempted, setAttempted] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  const [syncYear, setSyncYear] = useState(() => String(new Date().getFullYear()))
  const [syncing, setSyncing] = useState(false)
  const [syncError, setSyncError] = useState<string | null>(null)

  // 每次**打开**都从服务端那份重新回填：对话框不是"草稿箱"，上一次没保存的
  // 编辑不该悄悄留在下一次打开里。
  //
  // 依赖里刻意只有 `open`，不带 `config`。`config` 会在两处被换掉：保存成功、
  // 以及同步节假日后把服务端那一份回传给父组件（卡片要立刻显示"最近同步于…"）。
  // 后一种情况带着的是**旧的服务端版本**，里面没有用户手上还没保存的改动——
  // 把它接进依赖，等于"点一下同步就把你刚改的时区和覆盖全部抹回服务端版本"，
  // 而且界面一声不响。同步结果里真正需要进表单的三项（覆盖表、同步时间、来源）
  // 由 doSync 自己合并，不走这条通路。
  useEffect(() => {
    if (!open) return
    setForm(calendarToForm(config))
    setAttempted(false)
    setSaveError(null)
    setSyncError(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 见上：config 换一份不等于"重新开草稿"
  }, [open])

  const patchHoliday = (index: number, patch: Partial<{ date: string; kind: string }>) =>
    setForm((f) => ({
      ...f,
      holidays: f.holidays.map((h, i) => (i === index ? { ...h, ...patch } : h)),
    }))

  const addHoliday = () =>
    setForm((f) => ({ ...f, holidays: [...f.holidays, { date: "", kind: "rest" }] }))

  const removeHoliday = (index: number) =>
    setForm((f) => ({ ...f, holidays: f.holidays.filter((_, i) => i !== index) }))

  const issues = validateCalendarForm(form)
  const showIssues = attempted && issues.length > 0

  /** 本地校验。有问题就摆出来并返回 false，调用方不再往下走。 */
  const check = (): boolean => {
    setAttempted(true)
    return validateCalendarForm(form).length === 0
  }

  const doSave = async () => {
    setSaveError(null)
    if (!check()) return
    setSaving(true)
    try {
      const saved = await updatePeakCalendar(calendarFormToPayload(form))
      onConfigChange(saved)
      toast.success(t("toast_calendar_saved"))
      onOpenChange(false)
    } catch (err) {
      // 后端 message 是唯一能定位问题的信息，原样放出来
      setSaveError(errorText(err))
    } finally {
      setSaving(false)
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
        holidays: holidayRowsFromOverrides(res.calendar.dateOverrides ?? {}),
        holidaySyncedAt: res.calendar.holidaySyncedAt,
        holidaySource: res.calendar.holidaySource,
      }))
      onConfigChange(res.calendar)
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
    String(t(`issue.${issue.key}` as never, { value: issue.value ?? "" }))

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("edit_title")}</DialogTitle>
          <DialogDescription>{t("edit_desc")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-5 py-2">
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

          {/* ---- 本地校验结果 ---- */}
          {showIssues && (
            <div className="flex flex-col gap-1 rounded-md border border-status-critical/40 bg-status-critical/5 p-3">
              <p className="text-xs font-medium text-status-critical-ink">
                {t("errors_title", { n: issues.length })}
              </p>
              <ul className="flex flex-col gap-1">
                {issues.map((issue, i) => (
                  <li
                    key={`${issue.key}-${i}`}
                    className="reading text-xs text-status-critical-ink"
                  >
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
