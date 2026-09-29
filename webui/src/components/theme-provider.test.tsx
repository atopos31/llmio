import { act, render, renderHook, screen } from "@testing-library/react"
import { userEvent } from "@testing-library/user-event"
import type { ReactNode } from "react"
import { describe, expect, it } from "vitest"

import { ThemeProvider, useTheme } from "@/components/theme-provider"
import { THEME_STORAGE_KEY, themeInitScript } from "@/lib/theme"
import { isSystemDark, setSystemDark } from "@/test/theme-harness"

const wrapper = ({ children }: { children: ReactNode }) => (
  <ThemeProvider>{children}</ThemeProvider>
)

const htmlTheme = () => document.documentElement.getAttribute("data-theme")

describe("ThemeProvider · 三态", () => {
  it("默认跟随系统，且不写 data-theme 属性", () => {
    const { result } = renderHook(() => useTheme(), { wrapper })

    expect(result.current.theme).toBe("system")
    expect(result.current.resolvedTheme).toBe("light")
    // system 时刻意不写属性：写了就等于把当前系统外观固化下来，
    // 之后系统再切换页面就不会跟随了
    expect(htmlTheme()).toBeNull()
  })

  it("显式选深色时写入 data-theme 属性", () => {
    const { result } = renderHook(() => useTheme(), { wrapper })

    act(() => result.current.setTheme("dark"))

    expect(result.current.theme).toBe("dark")
    expect(result.current.resolvedTheme).toBe("dark")
    expect(htmlTheme()).toBe("dark")
  })

  it("能从深色切回「跟随系统」——旧实现切不回去", () => {
    const { result } = renderHook(() => useTheme(), { wrapper })

    act(() => result.current.setTheme("dark"))
    expect(htmlTheme()).toBe("dark")

    act(() => result.current.setTheme("system"))

    // 属性被移除，交还给 CSS 的 prefers-color-scheme
    expect(result.current.theme).toBe("system")
    expect(htmlTheme()).toBeNull()
  })

  it("系统为深色时，system 态解析为 dark", () => {
    setSystemDark(true)
    const { result } = renderHook(() => useTheme(), { wrapper })

    expect(result.current.theme).toBe("system")
    expect(result.current.resolvedTheme).toBe("dark")
    expect(htmlTheme()).toBeNull()
  })

  it("system 态下跟随系统外观变化——旧实现只在 theme 变化时读一次", () => {
    const { result } = renderHook(() => useTheme(), { wrapper })
    expect(result.current.resolvedTheme).toBe("light")

    act(() => setSystemDark(true))
    expect(result.current.resolvedTheme).toBe("dark")

    act(() => setSystemDark(false))
    expect(result.current.resolvedTheme).toBe("light")
  })

  it("显式选定后不再跟随系统变化", () => {
    const { result } = renderHook(() => useTheme(), { wrapper })

    act(() => result.current.setTheme("light"))
    act(() => setSystemDark(true))

    expect(result.current.resolvedTheme).toBe("light")
    expect(htmlTheme()).toBe("light")
  })

  it("不再使用已废弃的 .dark 类", () => {
    const { result } = renderHook(() => useTheme(), { wrapper })

    act(() => result.current.setTheme("dark"))

    // 类名与 index.css 的 @custom-variant 声明曾经不匹配，是本次修正的缺陷
    expect(document.documentElement.classList.contains("dark")).toBe(false)
    expect(document.documentElement.classList.contains("light")).toBe(false)
  })

  it("清掉可能残留的 .dark/.light 类", () => {
    document.documentElement.classList.add("dark")
    const { result } = renderHook(() => useTheme(), { wrapper })

    act(() => result.current.setTheme("light"))

    expect(document.documentElement.classList.contains("dark")).toBe(false)
  })
})

describe("ThemeProvider · 环境缺失", () => {
  it("matchMedia 不可用时回落浅色而不崩溃", () => {
    const original = window.matchMedia
    // @ts-expect-error 模拟不支持 matchMedia 的环境
    delete window.matchMedia
    try {
      const { result } = renderHook(() => useTheme(), { wrapper })
      expect(result.current.theme).toBe("system")
      expect(result.current.resolvedTheme).toBe("light")
      expect(htmlTheme()).toBeNull()
    } finally {
      window.matchMedia = original
    }
  })

  it("matchMedia 缺失时不影响显式选择", () => {
    const original = window.matchMedia
    // @ts-expect-error 模拟不支持 matchMedia 的环境
    delete window.matchMedia
    try {
      const { result } = renderHook(() => useTheme(), { wrapper })
      act(() => result.current.setTheme("dark"))
      expect(result.current.resolvedTheme).toBe("dark")
      expect(htmlTheme()).toBe("dark")
    } finally {
      window.matchMedia = original
    }
  })
})

describe("ThemeProvider · 持久化", () => {
  it("写入 localStorage", () => {
    const { result } = renderHook(() => useTheme(), { wrapper })

    act(() => result.current.setTheme("dark"))

    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark")
  })

  it("从 localStorage 恢复选择", () => {
    localStorage.setItem(THEME_STORAGE_KEY, "dark")

    const { result } = renderHook(() => useTheme(), { wrapper })

    expect(result.current.theme).toBe("dark")
    expect(htmlTheme()).toBe("dark")
  })

  it("忽略 localStorage 中的非法值", () => {
    localStorage.setItem(THEME_STORAGE_KEY, "neon")

    const { result } = renderHook(() => useTheme(), { wrapper })

    expect(result.current.theme).toBe("system")
    expect(htmlTheme()).toBeNull()
  })

  it("localStorage 抛异常时回落到默认值而不崩溃", () => {
    // 隐私模式或站点数据被禁时 localStorage 会直接抛错。
    // 主题是纯展示偏好，绝不能因此让整个界面挂掉。
    const original = Storage.prototype.getItem
    Storage.prototype.getItem = () => {
      throw new Error("access denied")
    }
    try {
      const { result } = renderHook(() => useTheme(), { wrapper })
      expect(result.current.theme).toBe("system")
    } finally {
      Storage.prototype.getItem = original
    }
  })

  it("写入失败时当前会话内仍生效", () => {
    const original = Storage.prototype.setItem
    Storage.prototype.setItem = () => {
      throw new Error("quota exceeded")
    }
    try {
      const { result } = renderHook(() => useTheme(), { wrapper })
      act(() => result.current.setTheme("dark"))
      expect(result.current.theme).toBe("dark")
    } finally {
      Storage.prototype.setItem = original
    }
  })

  it("支持自定义 storageKey", () => {
    const custom = ({ children }: { children: ReactNode }) => (
      <ThemeProvider storageKey="custom-theme">{children}</ThemeProvider>
    )
    const { result } = renderHook(() => useTheme(), { wrapper: custom })

    act(() => result.current.setTheme("dark"))

    expect(localStorage.getItem("custom-theme")).toBe("dark")
  })

  it("默认主题可配置", () => {
    const custom = ({ children }: { children: ReactNode }) => (
      <ThemeProvider defaultTheme="dark">{children}</ThemeProvider>
    )
    const { result } = renderHook(() => useTheme(), { wrapper: custom })

    expect(result.current.theme).toBe("dark")
  })
})

describe("useTheme", () => {
  it("在 Provider 之外使用会抛错", () => {
    expect(() => renderHook(() => useTheme())).toThrow(/ThemeProvider/)
  })

  it("setTheme 引用稳定，不会导致无谓重渲染", () => {
    const { result, rerender } = renderHook(() => useTheme(), { wrapper })
    const first = result.current.setTheme
    rerender()
    expect(result.current.setTheme).toBe(first)
  })
})

describe("首屏初始化脚本", () => {
  it("内联脚本只处理显式选择，system 时不动属性", () => {
    // 脚本在 <html> 上直接跑。这里用 Function 构造执行它，验证真实行为。
    const run = new Function(themeInitScript + ";")

    localStorage.removeItem(THEME_STORAGE_KEY)
    document.documentElement.removeAttribute("data-theme")
    run()
    expect(document.documentElement.getAttribute("data-theme")).toBeNull()

    localStorage.setItem(THEME_STORAGE_KEY, "dark")
    document.documentElement.removeAttribute("data-theme")
    run()
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark")

    localStorage.setItem(THEME_STORAGE_KEY, "light")
    document.documentElement.removeAttribute("data-theme")
    run()
    expect(document.documentElement.getAttribute("data-theme")).toBe("light")

    // system 不该写属性：写死会让系统切换失去作用
    localStorage.setItem(THEME_STORAGE_KEY, "system")
    document.documentElement.removeAttribute("data-theme")
    run()
    expect(document.documentElement.getAttribute("data-theme")).toBeNull()
  })

  it("与 ThemeProvider 使用同一个 storageKey", () => {
    expect(themeInitScript).toContain(THEME_STORAGE_KEY)
  })

  it("localStorage 不可用时静默失败", () => {
    const run = new Function(themeInitScript + ";")
    const original = Storage.prototype.getItem
    Storage.prototype.getItem = () => {
      throw new Error("denied")
    }
    try {
      expect(() => run()).not.toThrow()
    } finally {
      Storage.prototype.getItem = original
    }
  })
})

describe("ThemeToggle", () => {
  it("渲染三个选项并标出当前态", async () => {
    const { ThemeToggle } = await import("@/components/theme-toggle")
    render(
      <ThemeProvider>
        <ThemeToggle />
      </ThemeProvider>
    )

    const radios = screen.getAllByRole("radio")
    expect(radios).toHaveLength(3)
    // 默认 system
    expect(radios[2]).toHaveAttribute("aria-checked", "true")
    expect(radios[0]).toHaveAttribute("aria-checked", "false")
  })

  it("点击切换到深色", async () => {
    const user = userEvent.setup()
    const { ThemeToggle } = await import("@/components/theme-toggle")
    render(
      <ThemeProvider>
        <ThemeToggle />
      </ThemeProvider>
    )

    await user.click(screen.getAllByRole("radio")[1])

    expect(document.documentElement.getAttribute("data-theme")).toBe("dark")
  })

  it("方向键可在选项间移动（键盘可用性）", async () => {
    const user = userEvent.setup()
    const { ThemeToggle } = await import("@/components/theme-toggle")
    render(
      <ThemeProvider>
        <ThemeToggle />
      </ThemeProvider>
    )

    const radios = screen.getAllByRole("radio")
    radios[2].focus()
    await user.keyboard("{ArrowRight}")

    // system 是最后一个，右移回到第一个 light
    expect(document.documentElement.getAttribute("data-theme")).toBe("light")
  })

  it("从 light 左移回到 system", async () => {
    const user = userEvent.setup()
    const { ThemeToggle } = await import("@/components/theme-toggle")
    render(
      <ThemeProvider>
        <ThemeToggle />
      </ThemeProvider>
    )

    const radios = screen.getAllByRole("radio")
    radios[0].focus()
    await user.keyboard("{ArrowLeft}")

    expect(document.documentElement.getAttribute("data-theme")).toBeNull()
  })
})

describe("系统主题夹具自检", () => {
  it("setSystemDark 会影响 matchMedia 的返回值", () => {
    expect(isSystemDark()).toBe(false)
    setSystemDark(true)
    expect(isSystemDark()).toBe(true)
    expect(window.matchMedia("(prefers-color-scheme: dark)").matches).toBe(true)
  })
})
