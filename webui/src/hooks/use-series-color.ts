import { useMemo } from "react"

import { entitySeriesVar, seriesVar } from "@/lib/palette"

/**
 * 按**实体名**取色的记忆化钩子。
 *
 * 关键用法约定：universe 必须是**稳定的全集**（例如配置里的全部模型名），
 * 而不是当前时间窗/筛选下出现的子集。传子集会退化成"按排名取色"——
 * 筛掉一个系列后其余系列被改色，而读者已经学会"某个模型是蓝色"，
 * 颜色一变先前的认知就成了误导。见 palette.ts 的 entitySeriesVar。
 *
 * 返回一个 name → CSS 变量 的查表函数，避免在渲染循环里反复排序。
 */
export function useEntityColors(universe: readonly string[]) {
  return useMemo(() => {
    const sorted = [...universe].sort((a, b) => a.localeCompare(b))
    const map = new Map<string, string>()
    sorted.forEach((name, i) => map.set(name, seriesVar(i)))
    return (name: string) => map.get(name) ?? entitySeriesVar(name, universe)
  }, [universe])
}

/** 按索引取色的查表函数（用于"成功/失败/在途"这类固定顺序的序列）。 */
export function useIndexColors() {
  return useMemo(() => (index: number) => seriesVar(index), [])
}
