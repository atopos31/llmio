import { afterEach, describe, expect, it, vi } from "vitest"

import {
  DEFAULT_UI_PREFS,
  HOME_RANGES,
  UI_PREFS_STORAGE_KEY,
  UI_PREFS_VERSION,
  loadUiPrefs,
  refreshSecFor,
  readUiPrefs,
  withHomeRange,
  withLogsColumns,
  withRefreshSec,
  writeUiPrefs,
} from "./prefs"

afterEach(() => {
  vi.restoreAllMocks()
})

describe("loadUiPrefs", () => {
  it("空载荷给默认值", () => {
    for (const raw of [null, ""]) {
      expect(loadUiPrefs(raw)).toEqual(DEFAULT_UI_PREFS)
    }
  })

  it("非法 JSON 与不是对象的顶层值都给默认值", () => {
    for (const raw of ['{不是 json', "30", "null", "[]", "true"]) {
      expect(loadUiPrefs(raw)).toEqual(DEFAULT_UI_PREFS)
    }
  })

  it("每个字段各自独立校验：坏字段回落，好字段保留", () => {
    const prefs = loadUiPrefs(
      JSON.stringify({
        refresh: { home: 30, logs: "5", quota: -1 },
        homeRange: "yesterday",
        logsColumns: "id",
      })
    )

    expect(prefs.refresh).toEqual({ home: 30, logs: 0, quota: 0 })
    expect(prefs.homeRange).toBe("last_24h")
    expect(prefs.logsColumns).toEqual([])
  })

  it("refresh 不是对象时整体回落", () => {
    for (const raw of [JSON.stringify({ refresh: null }), JSON.stringify({ refresh: 30 })]) {
      expect(loadUiPrefs(raw).refresh).toEqual(DEFAULT_UI_PREFS.refresh)
    }
  })

  it("非整数秒与字符串秒不算档位", () => {
    expect(loadUiPrefs(JSON.stringify({ refresh: { home: 5.5 } })).refresh).toEqual({
      ...DEFAULT_UI_PREFS.refresh,
      logs: 0,
      quota: 0,
    })
    expect(loadUiPrefs(JSON.stringify({ refresh: { logs: "30" } })).refresh.logs).toBe(0)
  })

  it("缺的页面补默认，缺的字段整份补齐", () => {
    expect(loadUiPrefs(JSON.stringify({ refresh: { home: 30 } })).refresh).toEqual({
      home: 30,
      logs: 0,
      quota: 0,
    })
    expect(loadUiPrefs(JSON.stringify({}))).toEqual(DEFAULT_UI_PREFS)
  })

  it("homeRange 接受全部预设", () => {
    for (const range of HOME_RANGES) {
      expect(loadUiPrefs(JSON.stringify({ homeRange: range })).homeRange).toBe(range)
    }
  })

  it("非法 homeRange 回落默认", () => {
    for (const bad of ["yesterday", 24, null]) {
      expect(loadUiPrefs(JSON.stringify({ homeRange: bad })).homeRange).toBe("last_24h")
    }
  })

  it("logsColumns 只留字符串，非数组回落空列表", () => {
    expect(loadUiPrefs(JSON.stringify({ logsColumns: ["id", 3, null] })).logsColumns).toEqual([
      "id",
    ])
    expect(loadUiPrefs(JSON.stringify({ logsColumns: "id" })).logsColumns).toEqual([])
  })

  it("读出来的 version 是当前版本", () => {
    expect(loadUiPrefs(JSON.stringify({ version: 999 })).version).toBe(UI_PREFS_VERSION)
  })
})

describe("refreshSecFor", () => {
  const stored = { ...DEFAULT_UI_PREFS, refresh: { home: 30, logs: 5, quota: 0 } }
  const PRESETS = [0, 10, 30, 60, 300]

  it("返回存储的档位", () => {
    expect(refreshSecFor(stored, "home", PRESETS)).toBe(30)
    expect(refreshSecFor(stored, "quota", PRESETS)).toBe(0)
  })

  it("存储的档位不在当前选项里时回落关闭，而不是照发请求", () => {
    // 旧版本存过 5 秒，而这一版已经不提供 5 秒
    expect(refreshSecFor(stored, "logs", PRESETS)).toBe(0)
  })

  it("关闭档位的约定：选项列表的第一个档位是 0", () => {
    expect(PRESETS[0]).toBe(0)
  })
})

describe("写偏好的不可变更新", () => {
  it("withRefreshSec 只改目标页，且不改动原对象", () => {
    const base = DEFAULT_UI_PREFS
    const next = withRefreshSec(base, "logs", 30)

    expect(next.refresh.logs).toBe(30)
    expect(next.refresh.home).toBe(0)
    expect(next).not.toBe(base)
    expect(base.refresh.logs).toBe(0)
  })

  it("withHomeRange 只改时间范围", () => {
    const next = withHomeRange(DEFAULT_UI_PREFS, "last_7d")
    expect(next.homeRange).toBe("last_7d")
    expect(DEFAULT_UI_PREFS.homeRange).toBe("last_24h")
  })

  it("withLogsColumns 丢掉注册表里不存在的 id、去重、保序", () => {
    const next = withLogsColumns(
      DEFAULT_UI_PREFS,
      ["time", "gone", "id", "time"],
      ["id", "time", "status"]
    )
    expect(next.logsColumns).toEqual(["time", "id"])
  })
})

describe("readUiPrefs / writeUiPrefs", () => {
  it("写入后能读回，且 version 被刷成当前值", () => {
    writeUiPrefs(withRefreshSec(DEFAULT_UI_PREFS, "logs", 30))
    expect(readUiPrefs().refresh.logs).toBe(30)

    const stored = JSON.parse(localStorage.getItem(UI_PREFS_STORAGE_KEY) ?? "{}") as {
      version: number
    }
    expect(stored.version).toBe(UI_PREFS_VERSION)
  })

  it("localStorage 里没有东西时给默认值", () => {
    expect(readUiPrefs()).toEqual(DEFAULT_UI_PREFS)
  })

  it("读取抛错时回落默认值，不让首屏崩溃", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("site data is disabled")
    })
    expect(readUiPrefs()).toEqual(DEFAULT_UI_PREFS)
  })

  it("写入抛错时静默，不打断交互", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded")
    })
    expect(() => writeUiPrefs(DEFAULT_UI_PREFS)).not.toThrow()
  })
})
