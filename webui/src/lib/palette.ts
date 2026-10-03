/**
 * 图表配色契约。
 *
 * 颜色的**真值在 index.css 的 CSS 变量里**（--series-1..8 等），本文件不复制
 * 色值，只声明槽位数量与取用规则。这样做的原因：
 *
 *   - 色值必须能被 dataviz 验证器直接核验，hex 留在 CSS 里是唯一可审计的位置
 *   - 深浅两套主题由 CSS 的变量覆盖负责切换，JS 侧不该知道当前是哪个主题
 *
 * 硬约束（由 design-tokens.test.ts 强制）：分类色板只有 8 槽，固定顺序，
 * **永不循环**。超出的系列必须折叠为"其他"或分面，绝不能生成第 9 个色相——
 * 在色盲模拟下生成色与已有槽位无法区分，会使图表彻底失去辨识力。
 */

/** 分类色板槽位数。 */
export const SERIES_COUNT = 8

/**
 * 散点 / 气泡 / 小倍数等"任意两色都可能相邻"的图形，可安全使用的槽位数。
 *
 * 数值来自验证器的全配对检查：只有前 3 槽在两种模式下都能通过全配对门槛
 * （Light 最差对 CVD ΔE 13.7 / 常视觉 21.9，Dark 13.4 / 18.5）。
 * 第 4 槽会把琥珀与铜金同时放上屏，这一对在全配对下不达标。
 */
export const SERIES_COUNT_ALL_PAIRS = 3

/**
 * 取第 index 个分类槽位的 CSS 变量名。
 *
 * 越界时**钳到最后一槽**而不是回绕：回绕会让第 9 个系列与第 1 个同色，
 * 在读者眼里它们是两个不同的实体却看着一样；钳位至少不会伪造出错误的对应关系，
 * 而且会立刻在视觉上暴露"系列太多了"这个问题。
 */
export function seriesVar(index: number): string {
  if (!Number.isFinite(index) || index < 0) return "var(--series-1)"
  const clamped = Math.min(Math.floor(index), SERIES_COUNT - 1)
  return `var(--series-${clamped + 1})`
}

/** 顺序色阶（量级/热力）的 CSS 变量名。step 从 1 开始，越界钳位。 */
export function seqVar(step: number): string {
  if (!Number.isFinite(step) || step < 1) return "var(--seq-1)"
  const clamped = Math.min(Math.floor(step), 5)
  return `var(--seq-${clamped})`
}

/**
 * 按**实体**分配槽位，保证同一实体在任何筛选下都是同一个颜色。
 *
 * `universe` 必须是**稳定的全集**（例如配置里全部模型名），而**不是**
 * 当前时间窗/筛选条件下出现的子集。传子集会退化成"按排名取色"：
 * 筛掉一个系列后其余系列被改色，而读者已经学会"某个模型是蓝色"——
 * 颜色一变，先前的认知就成了误导。这是本函数唯一的、也是最容易用错的地方。
 *
 * 排序用 localeCompare（字典序），因此 "e10" 排在 "e2" 之前。对模型名、
 * 供应商名这类人读的标识是合适的；若实体集合是按数字编号的，需要调用方
 * 先自行归一化命名。
 */
export function entitySeriesVar(entity: string, universe: readonly string[]): string {
  const sorted = [...universe].sort((a, b) => a.localeCompare(b))
  const idx = sorted.indexOf(entity)
  return seriesVar(idx < 0 ? 0 : idx)
}

/**
 * 状态令牌。**固定不随主题走**，且必须与图标 + 文字一同出现：
 * 浅色面上 warning 与 serious 的填充色低于 3:1 是刻意设计，
 * 靠配对编码补偿，绝不单靠颜色传达状态。
 */
export const STATUS = {
  good: { fill: "var(--status-good)", ink: "var(--status-good-ink)" },
  warning: { fill: "var(--status-warning)", ink: "var(--status-warning-ink)" },
  serious: { fill: "var(--status-serious)", ink: "var(--status-serious-ink)" },
  critical: { fill: "var(--status-critical)", ink: "var(--status-critical-ink)" },
} as const

export type StatusKey = keyof typeof STATUS

/** 请求三态到语义状态的映射。running 是"在途"而非"成功"，故用中性色而非 good。 */
export function statusForRequest(status: string): StatusKey {
  switch (status) {
    case "success":
      return "good"
    case "error":
      return "critical"
    default:
      return "warning"
  }
}
