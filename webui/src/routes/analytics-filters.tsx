import { useTranslation } from "react-i18next"
import { ChevronDown, RefreshCw, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  activeFilterCount,
  RANGE_PRESETS,
  type AnalyticsFilter,
  type CustomRangeError,
  type RangePreset,
} from "@/lib/analytics"
import { cn } from "@/lib/utils"

/** 一个筛选维度的可选项。 */
export interface FilterOption {
  value: string
  label: string
}

/** 各筛选维度的可选项集合。键名与 `AnalyticsFilter` 的字段一一对应。 */
export interface FilterOptions {
  provider: FilterOption[]
  model: FilterOption[]
  key: FilterOption[]
  name: FilterOption[]
  ua: FilterOption[]
  status: FilterOption[]
}

export type RangeKey = RangePreset

/**
 * 多选筛选器。
 *
 * 用 Popover + 复选框而不是 `<select multiple>`：原生多选在触屏上几乎不可用
 * （需要长按/ctrl 才能多选），而这一页在手机上也要能用。
 *
 * 已选项计数放在触发器上而不是用颜色表示"已启用"：颜色不能是唯一的信息载体，
 * 而且计数本身（"筛了 2 个供应商"）就是用户需要知道的事。
 */
export function MultiSelectFilter({
  label,
  options,
  selected,
  onToggle,
}: {
  label: string
  options: FilterOption[]
  selected: string[]
  onToggle: (value: string) => void
}) {
  const { t } = useTranslation("analytics")
  const active = selected.length > 0

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          size="sm"
          aria-label={active ? t("filter.active", { label, count: selected.length }) : label}
          className={cn(
            "h-8 gap-1 px-2 text-xs font-normal",
            active && "border-primary text-foreground"
          )}
        >
          {label}
          {active && (
            <span className="reading rounded-sm bg-primary px-1 text-[10px] text-primary-foreground">
              {selected.length}
            </span>
          )}
          <ChevronDown className="size-3 opacity-60" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-60 p-1">
        {options.length === 0 ? (
          <p className="px-2 py-3 text-xs text-muted-foreground">{t("filter.empty")}</p>
        ) : (
          // 选项可能上百条（UA、请求名），因此列表自己滚动并限高
          <ul className="max-h-64 overflow-y-auto">
            {options.map((o) => (
              <li key={o.value}>
                <label className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-xs hover:bg-accent">
                  <Checkbox
                    checked={selected.includes(o.value)}
                    onCheckedChange={() => onToggle(o.value)}
                  />
                  <span className="min-w-0 flex-1 truncate" title={o.label}>
                    {o.label}
                  </span>
                </label>
              </li>
            ))}
          </ul>
        )}
      </PopoverContent>
    </Popover>
  )
}

/**
 * 筛选行。
 *
 * 位于所有视图之上并作用于其下全部内容——这是这一页的核心约定（方案 §4.3）。
 * 因此它不放进任何卡片里：塞进卡片会让人以为它只影响那张卡。
 *
 * 窄屏上控件换行而不是横向滚动：横向滚动的筛选行在触屏上很难被发现
 * （没有滚动条提示，且与页面的纵向手势冲突）。
 */
export function FilterRow({
  preset,
  onPreset,
  granularities,
  granularity,
  onGranularity,
  customFrom,
  customTo,
  customError,
  onCustomFrom,
  onCustomTo,
  filter,
  options,
  onToggle,
  onClear,
  onRefresh,
  loading,
}: {
  preset: RangeKey
  onPreset: (p: RangeKey) => void
  /** 服务端给出的档位；为空则不显示这个控件（见页面里的说明） */
  granularities: string[]
  granularity: string
  onGranularity: (g: string) => void
  /** 自定义范围的起止，`datetime-local` 的本地时间字符串 */
  customFrom: string
  customTo: string
  customError: CustomRangeError | null
  onCustomFrom: (v: string) => void
  onCustomTo: (v: string) => void
  filter: AnalyticsFilter
  options: FilterOptions
  onToggle: (field: keyof AnalyticsFilter, value: string) => void
  onClear: () => void
  onRefresh: () => void
  loading: boolean
}) {
  const { t } = useTranslation(["analytics", "common"])
  const activeCount = activeFilterCount(filter)

  return (
    <div className="flex flex-wrap items-center gap-2">
      <h2 className="mr-auto text-xl font-semibold tracking-tight">{t("analytics:title")}</h2>

      <ToggleGroup
        type="single"
        variant="outline"
        value={preset}
        // Radix 允许再次点击把值清空；时间范围没有"不选"这一态，忽略空值
        onValueChange={(v) => {
          if (v) onPreset(v as RangeKey)
        }}
        aria-label={t("analytics:range.label")}
        // 允许换行：五个档位在 280px（Galaxy Fold 外屏）上比容器宽十几像素，
        // 而外壳是 overflow-hidden——不换行就会被静默裁掉，"近 30 天"够不着
        className="flex-wrap"
      >
        {RANGE_PRESETS.map((p) => (
          <ToggleGroupItem key={p} value={p} size="sm" className="px-2">
            {t(`analytics:range.${p}` as never)}
          </ToggleGroupItem>
        ))}
        <ToggleGroupItem value="custom" size="sm" className="px-2">
          {t("analytics:range.custom")}
        </ToggleGroupItem>
      </ToggleGroup>

      {/* 自定义范围只在选中时占位：常驻会白占掉两个控件的位置，而它多数时候是空的 */}
      {preset === "custom" && (
        <div className="flex flex-wrap items-center gap-1.5">
          {/* 用原生 datetime-local 而不是再装一个日期选择器：它自带键盘与触屏
              交互、跟系统时区一致，且不必为两个输入框引入新的依赖与焦点管理。
              宽度交给 `auto`（原生控件的固有宽度按语言/字号变），并 `shrink-0`
              不参与压缩——写死宽度会切掉右端的日历图标，而压缩它同样会切掉 */}
          <Input
            type="datetime-local"
            value={customFrom}
            onChange={(e) => onCustomFrom(e.target.value)}
            aria-label={t("analytics:range.custom_from")}
            aria-invalid={customError !== null}
            className="h-8 w-auto shrink-0 px-2 text-xs"
          />
          <span className="text-xs text-muted-foreground" aria-hidden="true">
            –
          </span>
          <Input
            type="datetime-local"
            value={customTo}
            onChange={(e) => onCustomTo(e.target.value)}
            aria-label={t("analytics:range.custom_to")}
            aria-invalid={customError !== null}
            className="h-8 w-auto shrink-0 px-2 text-xs"
          />
          {/* 非法时说明**不动数据**：正在编辑的半截输入不该把已有视图清空 */}
          {customError && (
            <span className="text-[11px] text-status-warning-ink">
              {t(`analytics:range.custom_${customError}` as never)}
            </span>
          )}
        </div>
      )}

      {granularities.length > 0 && (
        <Select value={granularity} onValueChange={onGranularity}>
          <SelectTrigger className="h-8 w-[104px] px-2 text-xs" aria-label={t("analytics:granularity.label")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="auto" className="text-xs">
              {t("analytics:granularity.auto")}
            </SelectItem>
            {granularities.map((g) => (
              <SelectItem key={g} value={g} className="text-xs">
                {g}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}

      <MultiSelectFilter
        label={t("analytics:filter.provider")}
        options={options.provider}
        selected={filter.provider}
        onToggle={(v) => onToggle("provider", v)}
      />
      <MultiSelectFilter
        label={t("analytics:filter.model")}
        options={options.model}
        selected={filter.model}
        onToggle={(v) => onToggle("model", v)}
      />
      <MultiSelectFilter
        label={t("analytics:filter.key")}
        options={options.key}
        selected={filter.key}
        onToggle={(v) => onToggle("key", v)}
      />
      <MultiSelectFilter
        label={t("analytics:filter.name")}
        options={options.name}
        selected={filter.name}
        onToggle={(v) => onToggle("name", v)}
      />
      <MultiSelectFilter
        label={t("analytics:filter.ua")}
        options={options.ua}
        selected={filter.ua}
        onToggle={(v) => onToggle("ua", v)}
      />
      <MultiSelectFilter
        label={t("analytics:filter.status")}
        options={options.status}
        selected={filter.status}
        onToggle={(v) => onToggle("status", v)}
      />

      {activeCount > 0 && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-8 gap-1 px-2 text-xs font-normal text-muted-foreground"
          onClick={onClear}
        >
          <X className="size-3" aria-hidden="true" />
          {t("analytics:filter.clear", { count: activeCount })}
        </Button>
      )}

      <Button
        type="button"
        variant="outline"
        size="icon"
        className="size-8"
        onClick={onRefresh}
        disabled={loading}
        aria-label={t("analytics:refresh")}
        title={t("analytics:refresh")}
      >
        <RefreshCw className={cn("size-4", loading && "animate-spin")} aria-hidden="true" />
      </Button>
    </div>
  )
}
