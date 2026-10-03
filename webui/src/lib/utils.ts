import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * 在多选列表里切换一个取值（有就去掉，没有就补上）。
 *
 * 放在这里而不是某个页面自己的 lib 里：分析页与日志页都用它，
 * 而它对"筛选的是什么"一无所知——那正是通用工具与业务逻辑的分界。
 */
export function toggleValue(list: string[], value: string): string[] {
  return list.includes(value) ? list.filter((v) => v !== value) : [...list, value]
}

// 复制文本到剪贴板，非安全上下文（HTTP）下降级为 execCommand
export async function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // 降级到 execCommand
    }
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  try {
    if (!document.execCommand("copy")) {
      throw new Error("execCommand copy failed");
    }
  } finally {
    document.body.removeChild(textarea);
  }
}
