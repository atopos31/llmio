/**
 * 主题测试夹具。
 *
 * jsdom 未实现 matchMedia，需要自行提供。放在独立模块而非 setup 文件里，
 * 是因为 setup 文件的导出无法被测试文件可靠地共享到同一模块实例——
 * setup 与测试各自 import 本模块时才会拿到同一个状态。
 */

type Listener = (e: MediaQueryListEvent) => void

const listeners = new Set<Listener>()
let systemDark = false

/** 切换模拟的系统外观，并通知所有监听者（触发 change 事件）。 */
export function setSystemDark(dark: boolean) {
  systemDark = dark
  for (const l of listeners) {
    l({ matches: dark } as MediaQueryListEvent)
  }
}

/** 读取当前模拟值，供断言使用。 */
export function isSystemDark() {
  return systemDark
}

/** 清空状态与监听者，测试间互不影响。 */
export function resetSystemTheme() {
  systemDark = false
  listeners.clear()
}

/**
 * jsdom 未实现 ResizeObserver，而 Radix 的 use-size 依赖它。
 * 提供一个空实现即可——测试不关心尺寸变化，只要求不抛错。
 */
function installResizeObserver() {
  if (typeof globalThis.ResizeObserver !== "undefined") return
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
}

function install() {
  installResizeObserver()
  if (typeof window === "undefined") return
  window.matchMedia = ((query: string) => {
    const isDarkQuery = query.includes("prefers-color-scheme: dark")
    return {
      get matches() {
        return isDarkQuery ? systemDark : !systemDark
      },
      media: query,
      onchange: null,
      addEventListener: (_type: string, cb: Listener) => {
        listeners.add(cb)
      },
      removeEventListener: (_type: string, cb: Listener) => {
        listeners.delete(cb)
      },
      addListener: (cb: Listener) => listeners.add(cb),
      removeListener: (cb: Listener) => listeners.delete(cb),
      dispatchEvent: () => false,
    } as unknown as MediaQueryList
  }) as typeof window.matchMedia
}

install()
