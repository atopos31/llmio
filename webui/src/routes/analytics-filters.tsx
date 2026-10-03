import { useTranslation } from "react-i18next"
import { RefreshCw, X } from "lucide-react"

import { MultiSelectFilter, type FilterOption } from "@/components/multi-select-filter"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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

// 组件本身是共享的（日志页也用同一个），这里只是按分析页的命名空间转发一次。
export type { FilterOption }

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
 * 一个筛选维度：把共享组件的三处文案接到分析页的命名空间上。
 *
 * 取值与切换都由调用方给（`FilterRow` 从 `filter` / `onToggle` 里取），
 * 这里只负责"用哪套词"。
 */
function Dimension({
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
  return (
    <MultiSelectFilter
      label={label}
      options={options}
      selected={selected}
      onToggle={onToggle}
      emptyText={t("filter.empty")}
      ariaLabel={
        selected.length > 0
          ? t("filter.active", { label, count: selected.length })
          : label
      }
    />
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

      <Dimension
        label={t("analytics:filter.provider")}
        options={options.provider}
        selected={filter.provider}
        onToggle={(v) => onToggle("provider", v)}
      />
      <Dimension
        label={t("analytics:filter.model")}
        options={options.model}
        selected={filter.model}
        onToggle={(v) => onToggle("model", v)}
      />
      <Dimension
        label={t("analytics:filter.key")}
        options={options.key}
        selected={filter.key}
        onToggle={(v) => onToggle("key", v)}
      />
      <Dimension
        label={t("analytics:filter.name")}
        options={options.name}
        selected={filter.name}
        onToggle={(v) => onToggle("name", v)}
      />
      <Dimension
        label={t("analytics:filter.ua")}
        options={options.ua}
        selected={filter.ua}
        onToggle={(v) => onToggle("ua", v)}
      />
      <Dimension
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
