/**
 * 首次进入转发透传页的引导弹窗是否已读。
 *
 * 单独一个版本化键而不是塞进 UI 偏好：引导只关心"看过没有"，
 * 与刷新档位之类的偏好没有共同结构；将来引导改版（比如新增一页说明）
 * 只要换版本号就能重新弹一次，不影响用户其他设置。
 */
export const FORWARDING_GUIDE_STORAGE_KEY = "llmio-forwarding-guide-seen-v1"

/** 读取失败一律当作"没看过"：引导是可重开的，宁可多弹一次也不要把页面打不开。 */
export function hasSeenForwardingGuide(): boolean {
  try {
    return window.localStorage.getItem(FORWARDING_GUIDE_STORAGE_KEY) === "1"
  } catch {
    return false
  }
}

/** 写入失败静默：隐私模式下 localStorage 可能不可用，下次还会弹，但功能不受影响。 */
export function markForwardingGuideSeen(): void {
  try {
    window.localStorage.setItem(FORWARDING_GUIDE_STORAGE_KEY, "1")
  } catch {
    // 忽略
  }
}
