import "@testing-library/jest-dom/vitest"

// 安装 matchMedia 夹具（jsdom 未实现），并让测试间状态互相隔离
import { resetSystemTheme } from "./theme-harness"

import { afterEach } from "vitest"
import { cleanup } from "@testing-library/react"

afterEach(() => {
  cleanup()
  try {
    localStorage.clear()
  } catch {
    // 某些环境下 localStorage 不可用，主题是���示偏好，清不掉不影响测试
  }
  document.documentElement.removeAttribute("data-theme")
  document.documentElement.classList.remove("light", "dark")
  resetSystemTheme()
})
