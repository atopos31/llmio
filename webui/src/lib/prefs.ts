/**
 * 跨页面 UI 偏好的纯逻辑：自动刷新档位、总览时间范围、日志页可见列。
 *
 * 与 components/ 分开，理由同 lib/theme.ts：React Fast Refresh 要求组件文件
 * 只导出组件，而纯逻辑放进 lib 才能被覆盖率门禁单独盯住。
 *
 * ## 偏好要守住三条约束
 *
 * 1. **任何读取都不抛错。** 偏好是纯展示性的，读不到就回落默认。反之为一个
 *    5 秒的刷新档位抛错，会让整页打不开——隐私模式、站点数据被禁、旧版本
 *    写下的异构结构都会走到这里。`readUiPrefs` / `writeUiPrefs` 因此各带一个
 *    catch，与 theme.ts 的 readStoredTheme / persistTheme 同一处理方式。
 * 2. **存储格式带版本**，见 UI_PREFS_VERSION。将来若改动载荷结构，必须在这里
 *    加一个显式的迁移分支（参照 quota.ts 的 v1→v2 提升），而不是靠 fallback
 *    静默丢掉用户已经设好的偏好。
 * 3. **值必须与"当前提供的选项"一致。** 档位列表是会变的（日志页哪天把 5 秒
 *    从选项里删掉），而旧值还留在 localStorage 里。若不核对，界面会显示
 *    "关闭"却在后台按 5 秒发请求——这个矛盾无处可查，因为界面上根本没有任何
 *    按钮对应 5 秒。
 *
 * 第 3 条只能在**读取侧**核对（`refreshSecFor`）：它依赖页面运行时才知道的
 * 选项列表，加载时无法知道。而 `homeRange` 的选项是库内的封闭联合类型，
 * 所以 `loadUiPrefs` 里归一化就足以完备。
 */

/** localStorage 键。 */
export const UI_PREFS_STORAGE_KEY = "llmio-ui-prefs-v1"

/** 存储格式版本。当前只有 1；见文件头第 2 条约束。 */
export const UI_PREFS_VERSION = 1

/** 有自动刷新档位的页面。 */
export type RefreshPage = "home" | "logs" | "quota"

/**
 * 总览页的时间范围。值与后端 `from` / `to` 的换算口径一致（home.tsx 的
 * RANGE_HOURS），是**封闭**集合，故可以在加载时归一化。
 */
export type HomeRange = "today" | "last_24h" | "last_7d" | "last_30d"

export interface UiPrefs {
  /** 存储格式版本，写回时总是被刷成当前值。 */
  version: number
  /** 每个页面各自独立的刷新档位，单位秒；0 表示关闭。 */
  refresh: Record<RefreshPage, number>
  homeRange: HomeRange
  /** 日志页当前可见的列 id。空数组表示用默认集合。 */
  logsColumns: string[]
}

export const HOME_RANGES: readonly HomeRange[] = ["today", "last_24h", "last_7d", "last_30d"]

export const DEFAULT_UI_PREFS: UiPrefs = {
  version: UI_PREFS_VERSION,
  refresh: { home: 0, logs: 0, quota: 0 },
  homeRange: "last_24h",
  logsColumns: [],
}

const REFRESH_PAGES: readonly RefreshPage[] = ["home", "logs", "quota"]

/** 深拷贝默认值：`refresh` 与 `logsColumns` 是可变集合，不能共享引用。 */
function cloneDefaults(): UiPrefs {
  return { ...DEFAULT_UI_PREFS, refresh: { ...DEFAULT_UI_PREFS.refresh }, logsColumns: [] }
}

function isHomeRange(value: unknown): value is HomeRange {
  return (HOME_RANGES as readonly string[]).includes(value as string)
}

function normalizeHomeRange(value: unknown): HomeRange {
  return isHomeRange(value) ? value : DEFAULT_UI_PREFS.homeRange
}

function normalizeRefresh(value: unknown): Record<RefreshPage, number> {
  const out = { ...DEFAULT_UI_PREFS.refresh }
  if (typeof value !== "object" || value === null) return out
  const src = value as Record<string, unknown>
  for (const page of REFRESH_PAGES) {
    const seconds = src[page]
    // 只认非负整数秒：负数会让 setTimeout 立刻触发，小数不是任何页面提供的选项
    if (typeof seconds === "number" && Number.isInteger(seconds) && seconds >= 0) {
      out[page] = seconds
    }
  }
  return out
}

function normalizeColumns(value: unknown): string[] {
  return Array.isArray(value)
    ? (value.filter((id) => typeof id === "string") as string[])
    : []
}

/**
 * 解析存储里的偏好载荷。
 *
 * 与 loadQuotaView 同一形状（收 `raw: string | null`，纯函数无 IO）：
 * 存储的读写由 readUiPrefs / writeUiPrefs 负责，本函数只关心"这段字符串
 * 能不能变出一份合法的偏好"。任何一步不合预期都回落默认值。
 */
export function loadUiPrefs(raw: string | null): UiPrefs {
  if (!raw) return cloneDefaults()
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return cloneDefaults()
  }
  if (typeof parsed !== "object" || parsed === null) return cloneDefaults()
  const src = parsed as Partial<UiPrefs>
  return {
    version: UI_PREFS_VERSION,
    refresh: normalizeRefresh(src.refresh),
    homeRange: normalizeHomeRange(src.homeRange),
    logsColumns: normalizeColumns(src.logsColumns),
  }
}

/**
 * 某页当前生效的刷新档位。
 *
 * stored 值不在 `presets` 里就回落到 0：0 是"界面上确实存在的选项"，
 * 而不是一个用户已经无法选择的值。约定每个页面的 presets 第一个档位是 0
 * （关闭）——不创建选项的页面就没法关掉轮询，所以这条约定不可破。
 */
export function refreshSecFor(prefs: UiPrefs, page: RefreshPage, presets: readonly number[]): number {
  const stored = prefs.refresh[page]
  return presets.includes(stored) ? stored : 0
}

export function withRefreshSec(prefs: UiPrefs, page: RefreshPage, sec: number): UiPrefs {
  return { ...prefs, refresh: { ...prefs.refresh, [page]: sec } }
}

export function withHomeRange(prefs: UiPrefs, range: HomeRange): UiPrefs {
  return { ...prefs, homeRange: range }
}

/**
 * 保留列 id 列表里"仍然存在的"部分。
 *
 * `known` 是当前版本的列注册表全集：某个列被删掉后它的 id 还留在
 * localStorage 里，若不修剪，下一版就会带着一个指向不存在列的选择启动。
 * 顺序与去重都保留——顺序就是用户在"列"菜单里看到的顺序。
 */
export function keepKnownColumns(columns: string[], known: readonly string[]): string[] {
  return [...new Set(columns.filter((id) => known.includes(id)))]
}

export function withLogsColumns(
  prefs: UiPrefs,
  visible: string[],
  known: readonly string[],
): UiPrefs {
  return { ...prefs, logsColumns: keepKnownColumns(visible, known) }
}

function serializeUiPrefs(prefs: UiPrefs): string {
  return JSON.stringify({ ...prefs, version: UI_PREFS_VERSION })
}

/**
 * 从 localStorage 读取偏好。
 *
 * 只有 `getItem` 本身抛错才会走到 catch（JSON 解析失败已由 loadUiPrefs 兜住），
 * 那时同样回落默认值而不是让首屏崩溃。
 */
export function readUiPrefs(): UiPrefs {
  try {
    return loadUiPrefs(window.localStorage.getItem(UI_PREFS_STORAGE_KEY))
  } catch {
    return cloneDefaults()
  }
}

/** 持久化偏好。写不进去只是下次打开丢偏好，不该打断交互。 */
export function writeUiPrefs(prefs: UiPrefs): void {
  try {
    window.localStorage.setItem(UI_PREFS_STORAGE_KEY, serializeUiPrefs(prefs))
  } catch {
    // 与 theme.ts 的 persistTheme 同一条理由
  }
}
