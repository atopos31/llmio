import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"

import {
  THEME_STORAGE_KEY,
  applyThemeToDocument,
  persistTheme,
  readStoredTheme,
  systemTheme,
  type ResolvedTheme,
  type Theme,
} from "@/lib/theme"

// 此处刻意不再导出类型与常量：react-refresh 要求组件文件只导出组件，
// 混导出会让热更新失效。需要 Theme/THEME_STORAGE_KEY 请直接从 @/lib/theme 取。

type ThemeProviderState = {
  /** 用户的选择，含 system */
  theme: Theme
  /** 实际生效的外观（system 已解析为 light/dark） */
  resolvedTheme: ResolvedTheme
  setTheme: (theme: Theme) => void
}

type ThemeProviderProps = {
  children: React.ReactNode
  defaultTheme?: Theme
  storageKey?: string
}

const ThemeProviderContext = createContext<ThemeProviderState | undefined>(undefined)

/**
 * 主题容器。
 *
 * 与前一版的区别（都是缺陷修复，不是风格调整）：
 *
 * 1. **写入 `data-theme` 属性而非 `.dark` 类**。index.css 的 `@custom-variant dark`
 *    声明匹配的是 `[data-theme=dark]`，而旧实现加的是 `.dark` 类 —— 两者不匹配，
 *    声明的变体从未生效，深色样式实际靠变量覆盖块在兜。现在两侧口径一致。
 * 2. **`system` 可切回**。旧实现的切换按钮是 light↔dark 二值，一旦切过就
 *    再也回不到"跟随系统"。
 * 3. **system 模式下监听系统切换**。旧实现只在 theme 变化时读一次媒体查询，
 *    用户在系统里切换外观时页面不会跟随。
 *
 * 首屏闪烁由 index.html 里的内联脚本消除：它在样式加载前就把 data-theme 写到
 * <html> 上，因此不会出现"先浅色再变深色"的闪动。此处只负责后续同步。
 */
export function ThemeProvider({
  children,
  defaultTheme = "system",
  storageKey = THEME_STORAGE_KEY,
}: ThemeProviderProps) {
  const [theme, setThemeState] = useState<Theme>(() => readStoredTheme(storageKey, defaultTheme))
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() =>
    theme === "system" ? systemTheme() : theme
  )

  // 应用外观到 <html>
  useEffect(() => {
    setResolvedTheme(theme === "system" ? systemTheme() : theme)
    applyThemeToDocument(theme)
  }, [theme])

  // system 模式下跟随系统切换
  useEffect(() => {
    if (theme !== "system") return
    if (typeof window.matchMedia !== "function") return

    const mql = window.matchMedia("(prefers-color-scheme: dark)")
    const onChange = () => setResolvedTheme(mql.matches ? "dark" : "light")

    mql.addEventListener("change", onChange)
    return () => mql.removeEventListener("change", onChange)
  }, [theme])

  const setTheme = useCallback(
    (next: Theme) => {
      setThemeState(next)
      persistTheme(storageKey, next)
    },
    [storageKey]
  )

  const value = useMemo(
    () => ({ theme, resolvedTheme, setTheme }),
    [theme, resolvedTheme, setTheme]
  )

  return <ThemeProviderContext.Provider value={value}>{children}</ThemeProviderContext.Provider>
}

// useTheme 必须与 ThemeProvider 同处一个模块：两者共享同一个 context 实例，
// 拆开只会让 Fast Refresh 与现在一样不生效，却多一个文件。
// 这里定点豁免该规则，而不是放宽整个仓库的配置。
// eslint-disable-next-line react-refresh/only-export-components
export function useTheme(): ThemeProviderState {
  const context = useContext(ThemeProviderContext)
  if (context === undefined) {
    throw new Error("useTheme must be used within a ThemeProvider")
  }
  return context
}
