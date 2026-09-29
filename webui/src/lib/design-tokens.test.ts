import { readFileSync, existsSync, readdirSync, statSync } from "node:fs"
import { describe, expect, it } from "vitest"

import { THEME_STORAGE_KEY } from "@/lib/theme"

/**
 * 设计系统的回归测试。
 *
 * 这里守的不是"代码能跑"，而是"设计契约没被悄悄改掉"。这些规则如果只写在
 * 文档和注释里，迟早会在某次改动中被无声破坏——而破坏方式通常不报错、
 * 只让界面慢慢变得平庸（或不可访问）。所以把它们固化成测试。
 */

// 用 process.cwd() 而非 new URL(..., import.meta.url)：Vite 会把后者当作
// 构建期资源引用去解析，传入目录会直接报"找不到模块"。
// vitest 的工作目录即 webui/。
const root = process.cwd() + "/"
const read = (rel: string) => readFileSync(root + rel, "utf-8")

const css = read("src/index.css")
const impeccable = read("../.impeccable.md")
const html = read("index.html")

/** 从一段 CSS 文本里抽取 `--name: value;` 形式的自定义属性。 */
function tokensIn(block: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const m of block.matchAll(/--([\w-]+)\s*:\s*([^;]+);/g)) {
    out[m[1]] = m[2].trim()
  }
  return out
}

/** 取出大括号配对的一段（用于从 CSS 中切出单个规则块）。 */
function blockAfter(source: string, marker: string): string {
  const start = source.indexOf(marker)
  if (start < 0) throw new Error(`未找到标记：${marker}`)
  const open = source.indexOf("{", start)
  let depth = 0
  for (let i = open; i < source.length; i++) {
    if (source[i] === "{") depth++
    else if (source[i] === "}") {
      depth--
      if (depth === 0) return source.slice(open + 1, i)
    }
  }
  throw new Error(`标记 ${marker} 的块未闭合`)
}

describe("主题机制", () => {
  it("dark 变体以 data-theme 为键，而不是已废弃的 .dark 类", () => {
    // 这条守的是一个真实存在过的缺陷：@custom-variant 声明匹配 [data-theme=dark]，
    // 而 provider 加的是 .dark 类，两者不匹配，声明的变体从未生效。
    const m = css.match(/@custom-variant\s+dark\s*\(([^)]*)\)/)
    expect(m, "index.css 应声明 dark 自定义变体").not.toBeNull()
    expect(m![1]).toContain("data-theme=dark")
    expect(m![1]).not.toContain(".dark")
  })

  it("深浅两套 token 块涵盖同一组键", () => {
    const light = tokensIn(blockAfter(css, ":root {"))
    const darkMedia = tokensIn(blockAfter(css, ':root:not([data-theme="light"]) {'))
    const darkAttr = tokensIn(blockAfter(css, ':root[data-theme="dark"] {'))

    const lightKeys = new Set(Object.keys(light))
    const darkKeys = new Set([...Object.keys(darkMedia), ...Object.keys(darkAttr)])

    // 深色块只需要覆盖"会变的" token；凡是深色覆写了的键，浅色必须有对应项
    for (const key of darkMedia ? Object.keys(darkMedia) : []) {
      expect(lightKeys.has(key), `深色覆盖了 --${key}，浅色块却缺少同名 token`).toBe(true)
    }
    expect(darkKeys.size).toBeGreaterThan(20)
  })
})

describe("深色 token 的两块必须完全一致", () => {
  // 深色值写了两次：一次给 @media (prefers-color-scheme: dark)，
  // 一次给 :root[data-theme="dark"]。CSS 没有 mixin，重复不可避免——
  // 那就用测试保证改一处必须改两处，否则会出现"手动选深色"与"系统深色"
  // 两种路径下外观不一致这种极难排查的问题。
  it("媒体查询块与属性选择器块逐键一致", () => {
    const media = tokensIn(blockAfter(css, ':root:not([data-theme="light"]) {'))
    const attr = tokensIn(blockAfter(css, ':root[data-theme="dark"] {'))

    expect(Object.keys(media).sort()).toEqual(Object.keys(attr).sort())
    for (const [key, value] of Object.entries(media)) {
      expect(attr[key], `--${key} 在两块深色定义中不一致`).toBe(value)
    }
  })
})

describe("分类色板", () => {
  // 这些是跑过 dataviz 验证器的确切 hex。改色必须重跑验证器并同步更新此处，
  // 否则要么测试失败、要么验证结果与实际渲染不符。
  const LIGHT = [
    "#3b5fdc",
    "#c2631c",
    "#0d9488",
    "#b5830c",
    "#cf4585",
    "#167a37",
    "#6b48c2",
    "#cf4444",
  ]
  const DARK = [
    "#6789f0",
    "#d9743a",
    "#16a99b",
    "#b87f00",
    "#d4529b",
    "#2aa85e",
    "#8d76e0",
    "#e26a6a",
  ]

  function seriesOf(block: string) {
    const t = tokensIn(block)
    return Array.from({ length: 8 }, (_, i) => t[`series-${i + 1}`])
  }

  it("浅色序列色与验证值一致", () => {
    expect(seriesOf(blockAfter(css, ":root {"))).toEqual(LIGHT)
  })

  it("深色序列色与验证值一致（两块都要）", () => {
    expect(seriesOf(blockAfter(css, ':root:not([data-theme="light"]) {'))).toEqual(DARK)
    expect(seriesOf(blockAfter(css, ':root[data-theme="dark"] {'))).toEqual(DARK)
  })

  it("恰好 8 槽，没有第 9 槽", () => {
    // 分类色板的上限是硬约束：第 9 个色相在色盲模拟下与已有槽位无法区分
    expect(css).not.toMatch(/--series-9\s*:/)
    expect(css).not.toMatch(/--color-series-9\s*:/)
  })

  it("相邻槽位颜色各不相同", () => {
    for (const list of [LIGHT, DARK]) {
      expect(new Set(list).size, "色板内不应有重复色值").toBe(list.length)
    }
  })
})

describe("顺序色阶", () => {
  it("5 级且两端明度单调递减", () => {
    const light = tokensIn(blockAfter(css, ":root {"))
    const steps = Array.from({ length: 5 }, (_, i) => light[`seq-${i + 1}`])
    expect(steps.every((s) => /^#[0-9a-f]{6}$/.test(s))).toBe(true)
    expect(new Set(steps).size).toBe(5)
  })
})

describe("状态色", () => {
  it("固定不随主题走：深浅两块取同一组填充色", () => {
    const light = tokensIn(blockAfter(css, ":root {"))
    const dark = tokensIn(blockAfter(css, ':root[data-theme="dark"] {'))
    for (const key of ["status-good", "status-warning", "status-serious", "status-critical"]) {
      expect(dark[key], `--${key} 不应随主题变化`).toBe(light[key])
    }
  })

  it("填充色与文字色是两套 token", () => {
    // 填充色用于标记、文字色用于正文，对比度要求不同（3:1 vs 4.5:1），
    // 合成一个会让某一种用法必然不达标
    const light = tokensIn(blockAfter(css, ":root {"))
    for (const key of ["good", "warning", "serious", "critical"]) {
      expect(light[`status-${key}`]).toBeTruthy()
      expect(light[`status-${key}-ink`]).toBeTruthy()
      expect(light[`status-${key}-ink`]).not.toBe(light[`status-${key}`])
    }
  })
})

describe("禁止的设计模式", () => {
  // 这些不是风格偏好，是 dataviz 规范里的硬性禁项
  const banned: { name: string; re: RegExp }[] = [
    {
      name: "侧边条纹（border-left/right 宽度 > 1px 的强调条）",
      // 允许 border-left-width: 1px 的发丝线与 0，禁止 2px 及以上的色条
      re: /border-(?:left|right)(?:-width)?\s*:\s*(?!1px|0|thin|medium|var\(--border-width\))\d+px/g,
    },
    { name: "渐变文字（background-clip: text 配渐变）", re: /background-clip\s*:\s*text/g },
    { name: "虚线网格线或轴线", re: /stroke-dasharray\s*:\s*(?!0)\d/g },
  ]

  it.each(banned)("index.css 中不出现：$name", ({ re }) => {
    expect(css).not.toMatch(re)
  })

  it("全仓组件源码中不出现侧边条纹与渐变文字", () => {
    const files = collectSourceFiles(root + "src")
    const offenders: string[] = []
    for (const file of files) {
      const text = readFileSync(file, "utf-8")
      // 只查 style 相关文本，避免把注释里的说明也算作违规
      for (const { re, name } of banned.slice(0, 2)) {
        if (re.test(text)) offenders.push(`${file}: ${name}`)
      }
    }
    expect(offenders).toEqual([])
  })
})

describe("字体自托管", () => {
  it("index.html 不引第三方字体 CDN", () => {
    // 离线单二进制工具不应依赖外网字体；外链还会阻塞首屏渲染
    expect(html).not.toMatch(/fonts\.googleapis\.com/)
    expect(html).not.toMatch(/fonts\.gstatic\.com/)
  })

  it("index.css 引入本地字体样式表", () => {
    expect(css).toContain('./assets/fonts/fonts.css')
  })

  it("每个 @font-face 指向的字体文件都存在", () => {
    const fontsCss = read("src/assets/fonts/fonts.css")
    const urls = [...fontsCss.matchAll(/url\('\.\/([^']+)'\)/g)].map((m) => m[1])
    expect(urls.length).toBeGreaterThan(0)
    for (const file of urls) {
      expect(existsSync(root + "src/assets/fonts/" + file), `缺少字体文件 ${file}`).toBe(true)
    }
  })

  it("中文字体走系统栈而非打包字体", () => {
    // 中文子集化不现实且体积收益为负，系统 CJK 字体的渲染质量也更好
    const m = css.match(/--font-sans\s*:\s*([^;]+);/)
    expect(m).not.toBeNull()
    expect(m![1]).toContain("PingFang SC")
    expect(m![1]).toContain("Microsoft YaHei")
  })
})

describe("无障碍基线", () => {
  it("尊重 prefers-reduced-motion", () => {
    expect(css).toContain("prefers-reduced-motion: reduce")
  })

  it("焦点环始终可见且用 token", () => {
    expect(css).toMatch(/:focus-visible\s*\{[^}]*outline\s*:/)
    expect(css).toMatch(/:focus-visible\s*\{[^}]*var\(--ring\)/)
  })

  it("未使用纯黑或纯白作为面/墨色", () => {
    // 纯黑纯白在自然界不存在，且会与所有带色调的相邻元素打架
    expect(css).not.toMatch(/--background:\s*(#000|#000000|#fff|#ffffff)\b/)
    expect(css).not.toMatch(/--foreground:\s*(#000|#000000)\b/)
  })

  it("首屏脚本与 ThemeProvider 用同一个 storageKey", () => {
    // 这两处曾经不一致：index.html 的内联脚本读 'llmio-theme'，
    // 而 App.tsx 给 ThemeProvider 传了 'vite-ui-theme'。
    // 后果是首屏脚本写的主题 Provider 读不到，表现为刷新后主题闪回/切换不生效。
    // 这类错误不会报错，只能靠断言守住。
    expect(html).toContain(THEME_STORAGE_KEY)
    expect(html).not.toContain("vite-ui-theme")

    // 断言前先剥掉注释：说明性文字里会提到被禁的写法，
    // 不剥离的话会把注释本身判为违规
    const app = stripComments(read("src/App.tsx"))
    expect(app).not.toMatch(/storageKey\s*=/)
  })

  it("首屏主题初始化脚本内联在 html 中（避免闪烁）", () => {
    expect(html).toContain("llmio-theme")
    expect(html).toMatch(/<script>[\s\S]*data-theme[\s\S]*<\/script>/)
  })
})

describe("文档与代码一致", () => {
  it(".impeccable.md 记录的分类色板与 index.css 相同", () => {
    // 文档是设计意图的出处，代码是实现。两者漂移会让后来者按文档改却改不动测试。
    const light = tokensIn(blockAfter(css, ":root {"))
    for (let i = 1; i <= 8; i++) {
      const hex = light[`series-${i}`]
      expect(impeccable, `文档未记录序列色 ${hex}`).toContain(hex)
    }
  })

  it(".impeccable.md 记录了无障碍要求", () => {
    expect(impeccable).toContain("WCAG AA")
    expect(impeccable).toContain("键盘")
  })
})

// 剥掉行注释与块注释，用于在断言里排除说明性文字
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "")
}

/** 递归收集源码文件（跳过测试与夹具）。 */
function collectSourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    const full = dir + "/" + entry
    if (statSync(full).isDirectory()) {
      if (entry === "test") continue
      out.push(...collectSourceFiles(full))
    } else if (/\.(tsx?|css)$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(full)
    }
  }
  return out
}
