import { ChevronDown } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { cn } from "@/lib/utils"

/** 一个筛选维度的可选项。 */
export interface FilterOption {
  value: string
  label: string
}

/**
 * 多选筛选器。
 *
 * 用 Popover + 复选框而不是 `<select multiple>`：原生多选在触屏上几乎不可用
 * （需要长按/ctrl 才能多选），而这两处用它的人（分析页、日志页）在手机上也要能用。
 *
 * 已选项计数放在触发器上而不是用颜色表示"已启用"：颜色不能是唯一的信息载体，
 * 而且计数本身（"筛了 2 个供应商"）就是用户需要知道的事。
 *
 * ## 为什么文案从外面传进来
 *
 * 这个组件被两个页面用，而它们分属不同的 i18n 命名空间（`analytics` / `logs`）。
 * 组件自己不翻译，只收 `label` / `emptyText` / `ariaLabel` 三个字符串——
 * 否则它就得知道"谁在用它"，而那正是共享组件最不该有的知识。
 */
export function MultiSelectFilter({
  label,
  options,
  selected,
  onToggle,
  emptyText,
  ariaLabel,
}: {
  label: string
  options: FilterOption[]
  selected: string[]
  onToggle: (value: string) => void
  /** 一个可选项都没有时的说明文字（"这个维度上还没有值"） */
  emptyText: string
  /** 触发器在无障碍树上的名字。省略时用 label；已选若干项时调用方会带上计数。 */
  ariaLabel?: string
}) {
  const active = selected.length > 0

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          size="sm"
          aria-label={ariaLabel ?? label}
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
          <p className="px-2 py-3 text-xs text-muted-foreground">{emptyText}</p>
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
