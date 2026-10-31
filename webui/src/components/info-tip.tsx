import { Info } from "lucide-react";
import type { ReactNode } from "react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

type InfoTipProps = {
  /** 供读屏软件使用；视觉上只显示信息图标。 */
  label: string;
  children: ReactNode;
  className?: string;
};

/**
 * 术语说明。标签旁的信息图标必须可以用键盘聚焦，
 * 不能只在鼠标悬停时出现。
 */
export function InfoTip({ label, children, className }: InfoTipProps) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          className={cn(
            "inline-flex size-4 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            className
          )}
        >
          <Info className="size-3.5" aria-hidden="true" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-sm text-xs leading-relaxed">
        {children}
      </TooltipContent>
    </Tooltip>
  );
}
