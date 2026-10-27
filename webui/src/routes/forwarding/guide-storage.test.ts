import { afterEach, describe, expect, it, vi } from "vitest"

import {
  FORWARDING_GUIDE_STORAGE_KEY,
  hasSeenForwardingGuide,
  markForwardingGuideSeen,
} from "@/routes/forwarding/guide-storage"

/**
 * 引导只关心"看过没有"，因此两个方向都不能让存储故障冒到页面上：
 * 读取抛错当作没看过（再弹一次），写入抛错静默（功能照常）。
 */
afterEach(() => {
  vi.restoreAllMocks()
})

describe("转发引导已读标记", () => {
  it("没写过就是没看过；写过之后读得到，键是带版本号的", () => {
    expect(hasSeenForwardingGuide()).toBe(false)

    markForwardingGuideSeen()

    expect(hasSeenForwardingGuide()).toBe(true)
    expect(window.localStorage.getItem(FORWARDING_GUIDE_STORAGE_KEY)).toBe("1")
  })

  it("只有存成 \"1\" 才算已读，别的值不认", () => {
    window.localStorage.setItem(FORWARDING_GUIDE_STORAGE_KEY, "true")

    expect(hasSeenForwardingGuide()).toBe(false)
  })

  it("存储读取抛错时当作没看过，页面不会因此打不开", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage denied")
    })

    expect(hasSeenForwardingGuide()).toBe(false)
  })

  it("存储写入抛错时静默失败，下次还会弹但功能不受影响", () => {
    window.localStorage.setItem(FORWARDING_GUIDE_STORAGE_KEY, "old")
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("storage denied")
    })

    expect(() => markForwardingGuideSeen()).not.toThrow()
    // 写入没成功，标记就还停在旧值上
    expect(window.localStorage.getItem(FORWARDING_GUIDE_STORAGE_KEY)).toBe("old")
  })
})
