import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  XAxis,
  YAxis,
} from "recharts"

import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import { compactNumber, formatBucketLabel } from "@/lib/format"
import { seriesVar } from "@/lib/palette"
import type { TrendPoint } from "@/lib/api"

/**
 * 请求趋势。
 *
 * **单轴**。请求数与 Token 量级完全不同，原实现把两者放在一张图上用两条
 * 轴（yAxisIndex），那会让缩放的对齐关系变成任意的，凭空造出数据里不存在
 * 的相关性——这是图表规范里的头号禁项。此处拆成两张图，各自单轴。
 *
 * 叠加方式用**堆叠**：成功/失败/在途是互斥的完备划分，堆叠后柱高就是总请求数，
 * 一张图同时给出总量与构成。段之间留 2px 表面色间隙来分隔——不用描边
 * （描边是数据之外的墨，而间隙是留白）。
 */
export function RequestTrendChart({ data }: { data: TrendPoint[] }) {
  const { t } = useTranslation("home")
  const bucketMs = useMemo(() => inferBucket(data), [data])

  const config = {
    success: { label: t("trend.success"), color: seriesVar(5) },
    error: { label: t("trend.error"), color: seriesVar(7) },
    running: { label: t("trend.running"), color: seriesVar(3) },
  } satisfies ChartConfig

  return (
    <ChartContainer config={config} className="aspect-auto h-[220px] w-full">
      <BarChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" strokeWidth={1} />
        <XAxis
          dataKey="ts"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={24}
          tickFormatter={(v: number) => formatBucketLabel(v, bucketMs)}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={44}
          tickFormatter={(v: number) => compactNumber(v)}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(v) => formatBucketLabel(Number(v), bucketMs)}
            />
          }
        />
        <ChartLegend content={<ChartLegendContent />} />
        <Bar dataKey="success" stackId="req" fill="var(--color-success)" maxBarSize={24} />
        {/* 段间 2px 表面色间隙：靠留白分隔，不靠描边 */}
        <Bar dataKey="error" stackId="req" fill="var(--color-error)" maxBarSize={24} />
        <Bar
          dataKey="running"
          stackId="req"
          fill="var(--color-running)"
          maxBarSize={24}
          radius={[4, 4, 0, 0]}
        />
      </BarChart>
    </ChartContainer>
  )
}

/**
 * Token 趋势。
 *
 * 与请求趋势分开的第二张图（原因见上）。输入/输出/缓存是 Token 的构成，
 * 同样用堆叠：柱高即总 Token。用**面积**而非柱是因为 Token 的量级连续性强，
 * 面积更能读出"消耗节奏"。
 */
export function TokenTrendChart({ data }: { data: TrendPoint[] }) {
  const { t } = useTranslation("home")
  const bucketMs = useMemo(() => inferBucket(data), [data])

  // prompt 含缓存读，因此画"非缓存输入 + 缓存读 + 输出"三者，
  // 避免缓存部分被重复计入而让总量虚高（三者之和恰为 totalTokens）
  const series = useMemo(
    () =>
      data.map((p) => ({
        ...p,
        freshPrompt: Math.max(0, p.prompt - p.cached),
      })),
    [data]
  )

  const config = {
    freshPrompt: { label: t("trend.input"), color: seriesVar(0) },
    cached: { label: t("trend.cached"), color: seriesVar(2) },
    completion: { label: t("trend.output"), color: seriesVar(1) },
  } satisfies ChartConfig

  return (
    <ChartContainer config={config} className="aspect-auto h-[220px] w-full">
      <AreaChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" strokeWidth={1} />
        <XAxis
          dataKey="ts"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={24}
          tickFormatter={(v: number) => formatBucketLabel(v, bucketMs)}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={44}
          tickFormatter={(v: number) => compactNumber(v)}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(v) => formatBucketLabel(Number(v), bucketMs)}
            />
          }
        />
        <ChartLegend content={<ChartLegendContent />} />
        <Area
          type="monotone"
          dataKey="freshPrompt"
          stackId="tok"
          stroke="var(--color-freshPrompt)"
          fill="var(--color-freshPrompt)"
          fillOpacity={0.1}
          strokeWidth={2}
        />
        <Area
          type="monotone"
          dataKey="cached"
          stackId="tok"
          stroke="var(--color-cached)"
          fill="var(--color-cached)"
          fillOpacity={0.1}
          strokeWidth={2}
        />
        <Area
          type="monotone"
          dataKey="completion"
          stackId="tok"
          stroke="var(--color-completion)"
          fill="var(--color-completion)"
          fillOpacity={0.1}
          strokeWidth={2}
        />
      </AreaChart>
    </ChartContainer>
  )
}

/**
 * 首包耗时分布直方图。
 *
 * 桶是**固定的**（而不是按分位数动态分箱）：固定的桶让相邻两天的图可直接比较，
 * 动态分箱会让同一个桶在不同时间代表不同区间，读者无法建立稳定认知。
 *
 * 着色用**单一顺序色阶**而非红/绿阈值：把连续量按阈值染成对立色，等于把
 * "多少"伪装成"好坏"，且红绿对立对色盲用户失效。真正的阈值告警应另外呈现。
 */
export function FirstChunkHistogram({ samples }: { samples: number[] }) {
  const { t } = useTranslation("home")

  const bins = useMemo(() => {
    const edges = [0, 0.5, 1, 2, 3, 5, 8, 10, 15, 20, 30, 60]
    const counts = new Array(edges.length).fill(0)
    for (const s of samples) {
      let i = edges.length - 1
      for (let j = 0; j < edges.length - 1; j++) {
        if (s >= edges[j] && s < edges[j + 1]) {
          i = j
          break
        }
      }
      counts[i]++
    }
    const labels = edges.map((e, i) =>
      i === edges.length - 1 ? `≥${e}s` : `${e}–${edges[i + 1]}s`
    )
    // 丢弃尾部连续为空的高耗时桶，避免空桶把横轴拉长
    let last = counts.length - 1
    while (last > 0 && counts[last] === 0) last--
    return labels.slice(0, last + 1).map((label, i) => ({ label, count: counts[i], i }))
  }, [samples])

  const config = { count: { label: t("latency.count") } } satisfies ChartConfig

  return (
    <ChartContainer config={config} className="aspect-auto h-[200px] w-full">
      <BarChart data={bins} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" strokeWidth={1} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={36}
          allowDecimals={false}
          tickFormatter={(v: number) => compactNumber(v)}
        />
        <ChartTooltip content={<ChartTooltipContent hideLabel />} />
        {/* 顺序色阶：桶越靠后（越慢）颜色越深，用单一色相表达量级 */}
        <Bar dataKey="count" maxBarSize={24} radius={[4, 4, 0, 0]}>
          {bins.map((b) => (
            <Cell key={b.label} fill={seqFor(b.i, bins.length)} />
          ))}
        </Bar>
      </BarChart>
    </ChartContainer>
  )
}

/** 桶索引 → 顺序色阶的一级（1-5）。 */
function seqFor(index: number, total: number): string {
  if (total <= 1) return "var(--seq-3)"
  const step = 1 + Math.round((index / (total - 1)) * 4)
  return `var(--seq-${Math.min(5, Math.max(1, step))})`
}

/**
 * 从趋势数据推断桶宽，供 X 轴标签选择格式（时刻 vs 日期）。
 * 取相邻两点的时间差；不足两点时回落到 1 小时。
 */
function inferBucket(data: TrendPoint[]): number {
  if (data.length < 2) return 60 * 60 * 1000
  return data[1].ts - data[0].ts
}
