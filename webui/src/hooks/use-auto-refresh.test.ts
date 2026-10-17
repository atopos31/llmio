import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { useAutoRefresh } from "./use-auto-refresh"

beforeEach(() => {
  vi.useFakeTimers()
  Object.defineProperty(document, "hidden", { value: false, configurable: true })
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

/** 让 jsdom 的只读 document.hidden 变成可控值，并派发事件触发监听。 */
function setHidden(hidden: boolean) {
  Object.defineProperty(document, "hidden", { value: hidden, configurable: true })
  document.dispatchEvent(new Event("visibilitychange"))
}

describe("useAutoRefresh", () => {
  it("关闭档位移不创建定时器，也不监听可见性", () => {
    const tick = vi.fn()
    const onVis = vi.fn()

    renderHook(() => useAutoRefresh(0, tick))
    document.addEventListener("visibilitychange", onVis)
    try {
      expect(onVis).not.toHaveBeenCalled()
    } finally {
      document.removeEventListener("visibilitychange", onVis)
    }

    act(() => {
      vi.advanceTimersByTime(3600_000)
    })
    expect(tick).not.toHaveBeenCalled()
  })

  it("负数与 NaN 同样按关闭处理", () => {
    const tick = vi.fn()
    renderHook(() => useAutoRefresh(NaN, tick))
    act(() => {
      vi.advanceTimersByTime(3600_000)
    })
    expect(tick).not.toHaveBeenCalled()
  })

  it("按档位周期反复触发，节奏稳定", () => {
    const tick = vi.fn()
    renderHook(() => useAutoRefresh(5, tick))

    act(() => {
      vi.advanceTimersByTime(10_000)
    })
    expect(tick).toHaveBeenCalledTimes(2)

    // 继续走 100 秒：每 5 秒一次，即 20 次，累计 22 次
    act(() => {
      vi.advanceTimersByTime(100_000)
    })
    expect(tick).toHaveBeenCalledTimes(22)

    // 不足一个间隔不再触发
    act(() => {
      vi.advanceTimersByTime(2_500)
    })
    expect(tick).toHaveBeenCalledTimes(22)
  })

  it("标签页隐藏时停止，回到前台先补一次再重新排期", () => {
    const tick = vi.fn()
    renderHook(() => useAutoRefresh(5, tick))

    act(() => setHidden(true))
    act(() => {
      vi.advanceTimersByTime(60_000)
    })
    expect(tick).not.toHaveBeenCalled()

    act(() => setHidden(false))
    expect(tick).toHaveBeenCalledTimes(1)

    act(() => {
      vi.advanceTimersByTime(5_000)
    })
    expect(tick).toHaveBeenCalledTimes(2)
  })

  it("挂载时就处于隐藏状态，等回到前台才第一次触发", () => {
    Object.defineProperty(document, "hidden", { value: true, configurable: true })
    const tick = vi.fn()

    renderHook(() => useAutoRefresh(5, tick))
    act(() => {
      vi.advanceTimersByTime(60_000)
    })
    expect(tick).not.toHaveBeenCalled()

    act(() => setHidden(false))
    expect(tick).toHaveBeenCalledTimes(1)
  })

  it("回调走 ref：组件重渲染不重置计时，也不拿到过期闭包", () => {
    const tick = vi.fn()
    const { result, rerender } = renderHook(
      ({ s }) => useAutoRefresh(5, () => tick(s)),
      { initialProps: { s: 1 } }
    )

    act(() => {
      vi.advanceTimersByTime(2_500)
    })
    rerender({ s: 2 })
    act(() => {
      vi.advanceTimersByTime(2_500)
    })
    expect(tick).toHaveBeenCalledTimes(1)
    expect(tick).toHaveBeenCalledWith(2)
    expect(result.current).toBeUndefined()
  })

  it("卸载后不再触发", () => {
    const tick = vi.fn()
    const { unmount } = renderHook(() => useAutoRefresh(5, tick))

    act(() => {
      vi.advanceTimersByTime(6_000)
    })
    expect(tick).toHaveBeenCalledTimes(1)

    unmount()
    act(() => {
      vi.advanceTimersByTime(60_000)
    })
    expect(tick).toHaveBeenCalledTimes(1)
  })

  it("档位变化时重建计时器，立即开始按新周期走", () => {
    const tick = vi.fn()
    const { rerender } = renderHook(({ s }) => useAutoRefresh(s, tick), { initialProps: { s: 60 } })

    act(() => {
      vi.advanceTimersByTime(60_000)
    })
    expect(tick).toHaveBeenCalledTimes(1)

    rerender({ s: 5 })
    act(() => {
      vi.advanceTimersByTime(5_000)
    })
    expect(tick).toHaveBeenCalledTimes(2)
  })
})
