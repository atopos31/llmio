import "@testing-library/jest-dom/vitest"

// jsdom 没有 Pointer Events，而 Radix 的 Select / Popover 在 pointerup 时会问
// hasPointerCapture，没有实现就抛 TypeError。返回 false（本来就不存在捕获）
// 让它跳过释放分支即可，不必实现完整的 Pointer Events 捕获语义。
if (typeof HTMLElement.prototype.hasPointerCapture !== "function") {
  HTMLElement.prototype.hasPointerCapture = () => false
}
// 同一家组件库选中项后会调用 scrollIntoView 把该项滚进视野，jsdom 也没有。
// 测试里不校验滚动位置，空实现即可。
if (typeof Element.prototype.scrollIntoView !== "function") {
  Element.prototype.scrollIntoView = () => {}
}

// 安装 matchMedia 夹具（jsdom 未实现），并让测试间状态互相隔离
import { resetSystemTheme } from "./theme-harness"

import { afterEach } from "vitest"
import { cleanup } from "@testing-library/react"

afterEach(() => {
  cleanup()
  try {
    localStorage.clear()
  } catch {
    // 某些环境下 localStorage 不可用，主题是展示偏好，清不掉不影响测试
  }
  document.documentElement.removeAttribute("data-theme")
  document.documentElement.classList.remove("light", "dark")
  resetSystemTheme()
})
