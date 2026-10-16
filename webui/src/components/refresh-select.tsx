import { useTranslation } from "react-i18next"
import { RefreshCw } from "lucide-react"

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { cn } from "@/lib/utils"

/**
 * 自动刷新档位选择器。
 *
 * 总览、日志、配额三页都要选刷新档位，原先各写一份，文案还各存一套
 * （logs 用 auto_refresh.*，quota 用 auto_refresh_off / auto_refresh_every），
 * 因此收敛成这一份：文案统一走 common 命名空间，档位列表由调用方传入——
 * 三页的档位刻意不同（日志页 5 秒起，总览与配额更保守）。
 *
 * `0` 表示关闭，也是唯一的关闭方式，所以 options 的第一个必须是 0：
 * 没有关闭档位的页面就没法停掉轮询。
 *
 * `pending` 由调用方传"有没有一次取数在飞"，只为旋转这个图标：轮询是
 * 静默的（不闪骨架），用户画面上看不出它在动，旋一下就是那个"它在跑"的
 * 证据。
 */
export function RefreshSelect({
  value,
  options,
  onChange,
  pending,
  className,
}: {
  value: number
  options: readonly number[]
  onChange: (sec: number) => void
  pending?: boolean
  className?: string
}) {
  const { t } = useTranslation("common")
  return (
    <div className="flex items-center gap-1.5">
      <RefreshCw className={cn("size-3.5 text-muted-foreground", pending && "animate-spin")} aria-hidden="true" />
      <Select value={String(value)} onValueChange={(v) => onChange(Number(v))}>
        <SelectTrigger
          className={cn("h-8 w-[104px] text-xs", className)}
          aria-label={t("auto_refresh.label")}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((s) => (
            <SelectItem key={s} value={String(s)}>
              {s === 0 ? t("auto_refresh.off") : t("auto_refresh.every", { sec: s })}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
