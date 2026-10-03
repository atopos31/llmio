/**
 * 主题的纯逻辑部分。
 *
 * 与 components/theme-provider.tsx 分开的理由有两个：
 *   - React Fast Refresh 要求组件文件只导出组件，混导出常量会让热更新失效
 *   - 这些函数是纯逻辑（无 React），放进 lib 后可以被覆盖率门禁单独盯住
 */

/** 三态主题。`system` 表示跟随操作系统，且必须能被切回来。 */
export type Theme = "light" | "dark" | "system"

/** 解析后的实际外观，只有两态。 */
export type ResolvedTheme = "light" | "dark"

/** localStorage 键。首屏内联脚本（index.html）也用它，两者必须一致。 */
export const THEME_STORAGE_KEY = "llmio-theme"

const DARK_QUERY = "(prefers-color-scheme: dark)"

/** 读取系统当前外观。 */
export function systemTheme(): ResolvedTheme {
  // 少数环境没有 matchMedia（极老的浏览器、部分嵌入场景）。
  // 主题是展示偏好，缺失时按浅色处理而不是抛错。
  // 这里不检查 `typeof window === "undefined"`：本项目是纯浏览器 SPA，
  // 该分支不可达，留着只会是永不被覆盖的死代码。
  if (typeof window.matchMedia !== "function") {
    return "light"
  }
  return window.matchMedia(DARK_QUERY).matches ? "dark" : "light"
}

/** 从 localStorage 读取用户选择，非法值或读不到时回落到默认值。 */
export function readStoredTheme(storageKey: string, fallback: Theme): Theme {
  try {
    const stored = window.localStorage.getItem(storageKey)
    if (stored === "light" || stored === "dark" || stored === "system") {
      return stored
    }
  } catch {
    // 隐私模式或站点数据被禁时 localStorage 会抛异常。
    // 主题是纯展示偏好，读不到就用默认值，绝不因此让界面崩溃。
  }
  return fallback
}

/** 持久化用户选择。写不进去不影响本次会话内的切换效果。 */
export function persistTheme(storageKey: string, theme: Theme): void {
  try {
    window.localStorage.setItem(storageKey, theme)
  } catch {
    // 同上：写失败只是下次打开丢偏好，不该打断交互
  }
}

/**
 * 把主题应用到 <html>。
 *
 * 只在 data-theme 上表达用户选择；`system` 时**移除**该属性，让 CSS 的
 * prefers-color-scheme 接管。若在 system 下也写死属性，系统切换外观时
 * 页面就不会跟随了。
 */
export function applyThemeToDocument(theme: Theme): void {
  const root = window.document.documentElement
  if (theme === "system") {
    root.removeAttribute("data-theme")
  } else {
    root.setAttribute("data-theme", theme)
  }
  // .dark 类已废弃（index.css 的 dark 变体以 data-theme 为键），
  // 这里清掉以免与遗留样式叠加
  root.classList.remove("light", "dark")
}

/**
 * 在 <html> 上内联执行的首屏主题初始化脚本。
 *
 * 必须内联且早于样式表：否则会先按默认外观绘制一帧再切换，形成闪烁。
 * 与 index.html 里的内联脚本是同一份逻辑，改一处必须同步另一处
 * （由 design-tokens.test.ts 校验两处用的是同一个 storageKey）。
 */
export const themeInitScript = `(function(){try{
var s=localStorage.getItem(${JSON.stringify(THEME_STORAGE_KEY)});
if(s==="dark"||s==="light"){document.documentElement.setAttribute("data-theme",s)}
}catch(e){}})();`
