import { Monitor, Moon, Sun } from "lucide-react"
import { useTranslation } from "react-i18next"

import { useTheme } from "@/components/theme-provider"
import type { Theme } from "@/lib/theme"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

// 显式声明键的联合类型：i18n 的类型增强把 t() 的入参收窄为字面量联合，
// 若这里声明成 string 会丢掉字面量信息而无法通过类型检查。
type ThemeLabelKey = "header.theme_light" | "header.theme_dark" | "header.theme_system"

const OPTIONS: { value: Theme; icon: typeof Sun; labelKey: ThemeLabelKey }[] = [
  { value: "light", icon: Sun, labelKey: "header.theme_light" },
  { value: "dark", icon: Moon, labelKey: "header.theme_dark" },
  { value: "system", icon: Monitor, labelKey: "header.theme_system" },
]

/**
 * 三态主题切换。
 *
 * 用分段控件而非单按钮循环：单按钮无法表达"当前是哪一态"，
 * 用户切到 system 后也看不出还在不在跟随系统（旧实现甚至切不回去）。
 * 这是单选组语义，因此用 radiogroup + aria-checked，键盘左右键可切换。
 */
export function ThemeToggle() {
  const { theme, setTheme } = useTheme()
  const { t } = useTranslation("layout")

  return (
    <div
      role="radiogroup"
      aria-label={t("header.theme_label")}
      className="flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5"
    >
      {OPTIONS.map(({ value, icon: Icon, labelKey }, index) => {
        const active = theme === value
        return (
          <Tooltip key={value}>
            <TooltipTrigger asChild>
              <Button
                type="button"
                role="radio"
                aria-checked={active}
                // 单一可聚焦点 + 方向键切换，避免三个 tab 停靠点
                tabIndex={active ? 0 : -1}
                variant="ghost"
                size="icon"
                className={cn(
                  "size-7 rounded-sm",
                  active
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:text-foreground"
                )}
                onClick={() => setTheme(value)}
                onKeyDown={(e) => {
                  // 方向键相对**当前聚焦**的选项移动，而不是相对当前选中项——
                  // 这是 radiogroup 的标准行为；按选中项算会导致
                  // "聚焦 A、按右键却跳到 C"这类错位。
                  if (e.key === "ArrowRight" || e.key === "ArrowDown") {
                    e.preventDefault()
                    setTheme(OPTIONS[(index + 1) % OPTIONS.length].value)
                  } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
                    e.preventDefault()
                    setTheme(OPTIONS[(index - 1 + OPTIONS.length) % OPTIONS.length].value)
                  }
                }}
              >
                <Icon className="size-3.5" aria-hidden="true" />
                <span className="sr-only">{t(labelKey)}</span>
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{t(labelKey)}</TooltipContent>
          </Tooltip>
        )
      })}
    </div>
  )
}
