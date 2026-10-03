import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { ArrowDown, ArrowUp, Loader2, Plus, Trash2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { previewPeakTerms } from "@/lib/api"
import {
  PREVIEW_DAY_OPTIONS,
  WEEKDAY_DISPLAY_ORDER,
  defaultTermsForm,
  findPeriodConflicts,
  formatMultiplier,
  formatSchedulePoint,
  termsFormToPayload,
  validateTermsForm,
  weekdayKey,
  type PeakPeriodForm,
  type PreviewResult,
  type WorkdayFilter,
} from "@/lib/peak"
import type { PeakTermsForm } from "@/lib/peak"

/**
 * 「模型 × 上游」关联表单里的峰谷条款小节。
 *
 * ## 为什么住在关联里
 *
 * 峰谷窗口是**这条上游的商务条款**：同一时刻 A 家打折、B 家峰时是正常的。
 * 全局只留一份日历（时区 / 星期几 / 节假日，见 routes/peak-calendar.tsx），
 * 因为它对所有上游是同一个答案。
 *
 * ## 三态
 *
 * 关联上的 Peak 是可空的，界面因此有三态，各自有明确的含义：
 *
 *   1. **未配置**（`value === null`）：按基础价计费，后端那一列是 NULL。
 *      不是"配了一份关闭的条款"——后者会随配置一起被谁看见就以为生效了。
 *   2. **已配置**：开关可停用而不删条款（`enabled=false` 留着时段），
 *      这样"这个月先按基础价结算"不必把时段抄下来备份。
 *   3. **移除配置**：回到 1。后端为此专门补了一次显式清空（见 lib/api.ts），
 *      否则 GORM 会跳过 nil 指针、旧条款一直留在行上——所以这条路径必须能被
 *      界面走到，否则那条后端修复就永远没人验证。
 *
 * ## 为什么不用 react-hook-form
 *
 * 时段是可增删的数组，用 RHF 的 useFieldArray 会把"第几段"编成路径字符串，
 * 校验信息也跟着变成路径，报给用户时又要翻译回"第几段"。本地校验
 * （validateTermsForm）本来就返回"第几段、哪一处"，直接渲染它更直接。
 * 表单状态因此由父级（useModelProviderForm）持有着，与 RHF 那份并列。
 *
 * ## 校验什么时候显示
 *
 * 输入过程中不弹红：正在把 "08:30" 敲成 "08:3" 的中间态天天不合法，边打边
 * 报红只会让人学会无视红色。父级在**按下保存**时校验（它才是决定拦不拦的
 * 那一方），并把尝试次数传下来；这里据此开始实时显示——第一次拦下之后，
 * 每改一个字结论都跟着刷新，改好了红色自己消失。
 */
export function PeakTermsEditor({
  value,
  onChange,
  submitAttempt,
  disabled,
}: {
  value: PeakTermsForm | null
  onChange: (next: PeakTermsForm | null) => void
  /** 父级按下保存的次数。>0 表示用户已经被拦过一次，可以开始报错了。 */
  submitAttempt: number
  disabled?: boolean
}) {
  const { t } = useTranslation(["peak", "models", "common"])
  const rootRef = useRef<HTMLDivElement>(null)
  const [previewDays, setPreviewDays] = useState(7)
  const [previewing, setPreviewing] = useState(false)
  const [preview, setPreview] = useState<PreviewResult | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)

  const issues = value ? validateTermsForm(value) : []
  const showIssues = submitAttempt > 0 && issues.length > 0
  const payload = value ? termsFormToPayload(value) : null
  const conflicts = payload ? findPeriodConflicts(payload.periods) : []

  // 被拦下时把这一节滚到眼前：保存按钮在对话框底部，而这一节在上面，
  // 用户按下去只会觉得"按钮没反应"。
  //
  // scrollIntoView 用可选调用：jsdom 里没有实现它，硬调会在测试环境抛异常
  // （真实浏览器里一定有），而"滚动失败"不该让整块界面崩掉。
  useEffect(() => {
    if (submitAttempt > 0) rootRef.current?.scrollIntoView?.({ block: "nearest" })
  }, [submitAttempt])

  /** 改条款 = 预览结果作废。留着旧时间轴会让"我改完再看"看到改之前的画面。 */
  const change = (next: PeakTermsForm | null) => {
    setPreview(null)
    setPreviewError(null)
    onChange(next)
  }

  const patchPeriod = (index: number, patch: Partial<PeakPeriodForm>) =>
    value &&
    change({
      ...value,
      periods: value.periods.map((p, i) => (i === index ? { ...p, ...patch } : p)),
    })

  const addPeriod = () =>
    value &&
    change({
      ...value,
      periods: [
        ...value.periods,
        { name: "", start: "00:00", end: "00:30", multiplier: "1", days: [], workday: "any" },
      ],
    })

  const removePeriod = (index: number) =>
    value && change({ ...value, periods: value.periods.filter((_, i) => i !== index) })

  /**
   * 上移 / 下移一段。
   *
   * 动的不是"展示顺序"，而是**判定优先级**：后端 ResolvePeriod 首个命中者
   * 胜出，数组顺序就是优先级。因此这里换的是两条配置的位置，不是排序显示。
   */
  const movePeriod = (index: number, delta: number) => {
    if (!value) return
    const target = index + delta
    if (target < 0 || target >= value.periods.length) return
    const next = [...value.periods]
    const tmp = next[index]
    next[index] = next[target]
    next[target] = tmp
    change({ ...value, periods: next })
  }

  const doPreview = async () => {
    setPreviewError(null)
    if (!payload) return
    setPreviewing(true)
    try {
      // 天数上限由后端把关（1..31），这里只发 PREVIEW_DAY_OPTIONS 里的值
      setPreview(await previewPeakTerms(payload, previewDays))
    } catch (err) {
      setPreview(null)
      setPreviewError(err instanceof Error ? err.message : String(err))
    } finally {
      setPreviewing(false)
    }
  }

  const issueText = (issue: (typeof issues)[number]) =>
    String(
      t(`issue.${issue.key}` as never, {
        index: issue.index ?? 0,
        name: issue.name || t("period_unnamed"),
        value: issue.value ?? "",
      })
    )

  // ---- 未配置：只给一句说明和一个入口 ----
  if (!value) {
    return (
      <div ref={rootRef} className="space-y-3">
        <div>
          <p className="text-sm font-medium">{t("terms_section")}</p>
          <p className="text-xs text-muted-foreground">{t("terms_section_desc")}</p>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="gap-1.5"
          disabled={disabled}
          onClick={() => change(defaultTermsForm())}
        >
          <Plus className="size-3.5" aria-hidden="true" />
          {t("terms_add")}
        </Button>
      </div>
    )
  }

  // ---- 已配置 ----
  return (
    <div ref={rootRef} className="space-y-3">
      <div>
        <p className="text-sm font-medium">{t("terms_section")}</p>
        <p className="text-xs text-muted-foreground">{t("terms_section_desc")}</p>
      </div>

      <label className="flex items-start justify-between gap-3 rounded-lg border p-3">
        <span className="space-y-0.5">
          <span className="block text-sm font-medium">{t("enabled")}</span>
          <span className="block text-[11px] text-muted-foreground">{t("enabled_hint")}</span>
        </span>
        <Switch
          checked={value.enabled}
          disabled={disabled}
          onCheckedChange={(v) => change({ ...value, enabled: v })}
          aria-label={t("enabled")}
        />
      </label>

      <div className="flex flex-col gap-2">
        <span className="text-xs font-medium text-muted-foreground">{t("period_list")}</span>
        <ul aria-label={t("period_list")} className="flex flex-col gap-3">
          {value.periods.map((p, index) => (
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
                    disabled={disabled || index === 0}
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
                    disabled={disabled || index === value.periods.length - 1}
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
                    disabled={disabled}
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
                    disabled={disabled}
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
                    disabled={disabled}
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
                    disabled={disabled}
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
                    disabled={disabled}
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
                  <span className="text-[11px] text-muted-foreground">{t("workday")}</span>
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
          disabled={disabled}
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

      {value.enabled && value.periods.length === 0 && (
        <p className="rounded-md border border-status-warning/40 bg-status-warning/5 p-3 text-xs text-status-warning-ink">
          {t("warn_no_periods")}
        </p>
      )}

      {showIssues && (
        <div className="flex flex-col gap-1 rounded-md border border-status-critical/40 bg-status-critical/5 p-3">
          <p className="text-xs font-medium text-status-critical-ink">
            {t("errors_title", { n: issues.length })}
          </p>
          <ul className="flex flex-col gap-1">
            {issues.map((issue, i) => (
              <li
                key={`${issue.key}-${issue.index ?? ""}-${i}`}
                className="reading text-xs text-status-critical-ink"
              >
                {issueText(issue)}
              </li>
            ))}
          </ul>
        </div>
      )}

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
            disabled={disabled || previewing}
            onClick={() => void doPreview()}
          >
            {previewing && <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />}
            {previewing ? t("preview_running") : t("preview_run")}
          </Button>
        </div>
        {/* 预览拿的是**已保存**的全局日历（时区、节假日都在那一份里），
            因此这里说清楚它按谁算，并指向改它的地方 */}
        <span className="text-[11px] text-muted-foreground">{t("preview_hint")}</span>
        <span className="text-[11px] text-muted-foreground">{t("calendar_note")}</span>

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
          (preview.points.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t("preview_empty")}</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {preview.points.map((point) => (
                <li key={point.start} className="flex flex-wrap items-center gap-2 text-xs">
                  {/*
                    按**服务端回传的时区**渲染，不是浏览器本地时区。
                    时间轴上的毫秒是绝对时刻，而"这段是不是夜间优惠"是按日历的
                    时区判定的：用本地时区显示，海外用户会看到"08:30 命中了
                    夜间优惠"这种自相矛盾的画面。
                  */}
                  <span className="reading">
                    {formatSchedulePoint(point, preview.timezone)}
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

      {/*
        移除是显式的一步，不是"把开关关了"。关开关保留条款（停用），
        移除则把这一列落回 NULL（按基础价）——两条路后端走的是不同代码，
        界面也得给出两个入口，否则漏掉的那条永远没人验证。
      */}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="gap-1.5 text-status-critical-ink"
        disabled={disabled}
        onClick={() => change(null)}
      >
        <Trash2 className="size-3.5" aria-hidden="true" />
        {t("terms_remove")}
      </Button>
    </div>
  )
}
