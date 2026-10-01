import { describe, expect, it } from "vitest"

/**
 * 三语键集的守门测试。
 *
 * 存在的理由是一处**真实发生过**的缺口：`card.hidden_items` 三个语种都缺，
 * 顶层却有个同名的孤儿键（从来没被读到），于是徽标只靠 `defaultValue` 兜底，
 * 三语都显示英文。缺键不会报错、不会让页面崩，只会让人看到另一种语言——
 * 而这在只跑一种语言时根本看不出来。
 *
 * 因此这里不检查译文质量（那要人读），只检查**结构**：同一命名空间下
 * 三个语种的键路径必须完全一致。zh-CN 是基准。
 *
 * 用 `import.meta.glob` 而不是手写 import 列表：手写的那一份会随着新增
 * 命名空间而漏掉新文件，而漏掉的正是没人提醒的那种。eager 是必须的，
 * 测试里没有 await 的余地（glob 返回的是惰性 loader）。
 */
const modules = import.meta.glob("./locales/*/*.json", { eager: true }) as Record<
  string,
  { default: Record<string, unknown> }
>

/** 把嵌套对象摊成 "a.b.c" 的键路径集合。 */
function keyPaths(value: unknown, prefix = ""): Set<string> {
  const out = new Set<string>()
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    out.add(prefix)
    return out
  }
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    for (const p of keyPaths(v, prefix ? `${prefix}.${k}` : k)) out.add(p)
  }
  return out
}

/** path 形如 "./locales/zh-CN/quota.json" */
function parsePath(path: string): { locale: string; ns: string } {
  const m = /^\.\/locales\/([^/]+)\/(.+)\.json$/.exec(path)
  if (!m) throw new Error(`意外的词条路径：${path}`)
  return { locale: m[1], ns: m[2] }
}

const LOCALES = ["zh-CN", "zh-TW", "en"] as const
const REFERENCE = "zh-CN"

/** 命名空间 → 语种 → 键路径集合 */
const byNs = new Map<string, Map<string, Set<string>>>()
for (const [path, mod] of Object.entries(modules)) {
  const { locale, ns } = parsePath(path)
  if (!byNs.has(ns)) byNs.set(ns, new Map())
  byNs.get(ns)!.set(locale, keyPaths(mod.default))
}

describe("词条键集", () => {
  it("三个语种的命名空间清单一致", () => {
    const localesOf = (locale: string) =>
      [...byNs.entries()]
        .filter(([, perLocale]) => perLocale.has(locale))
        .map(([ns]) => ns)
        .sort()

    expect(localesOf("zh-TW")).toEqual(localesOf(REFERENCE))
    expect(localesOf("en")).toEqual(localesOf(REFERENCE))
  })

  // 每个命名空间单独一例：失败时报出的是哪个文件缺哪个键，
  // 而不是一坨"两个大集合不相等"
  for (const [ns, perLocale] of [...byNs.entries()].sort()) {
    it(`${ns}：zh-TW 与 en 不缺不多`, () => {
      const base = perLocale.get(REFERENCE)
      expect(base, `${REFERENCE} 缺 ${ns}`).toBeDefined()

      for (const locale of LOCALES) {
        if (locale === REFERENCE) continue
        const other = perLocale.get(locale)
        expect(other, `${locale} 缺 ${ns}.json`).toBeDefined()

        const missing = [...base!].filter((k) => !other!.has(k)).sort()
        const extra = [...other!].filter((k) => !base!.has(k)).sort()
        expect({ locale, ns, missing, extra }).toEqual({ locale, ns, missing: [], extra: [] })
      }
    })
  }
})
