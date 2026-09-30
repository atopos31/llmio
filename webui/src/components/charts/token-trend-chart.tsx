import { useTranslation } from "react-i18next"
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  type ChartConfig,
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart"
import type { TimelinePoint, TimelineRange } from "@/lib/api"

// 时间维度 -> i18n key。对象键序即下拉展示顺序。
// 用字面量映射而不是拼接字符串：i18n 的 key 有编译期校验，拼出来的字符串过不了类型检查。
const RANGE_LABEL_KEYS = {
  today: "timeline.range_today",
  "24h": "timeline.range_24h",
  "7d": "timeline.range_7d",
  "30d": "timeline.range_30d",
  "90d": "timeline.range_90d",
} as const

const TIMELINE_RANGES = Object.keys(RANGE_LABEL_KEYS) as TimelineRange[]

// Radix Select 的 value 不接受空字符串，用哨兵值承载「全部模型」这一档
const ALL_MODELS = "__all__"

// 两条折线：dataKey 与下方 chartConfig 的 key 一致
const SERIES = [
  { key: "tokens", color: "var(--color-tokens)" },
  { key: "cached_tokens", color: "var(--color-cached_tokens)" },
] as const

// 桶数超过这个值就不画数据点，「今天」到 00:xx 只有 1 个桶时全靠点才看得见
const DOT_LIMIT = 32

// 后端桶标签：按小时 "2026-09-29 06:00"，按天 "2026-09-29"。
// 「今天」全在同一天，只显示时刻；跨天的维度保留月日，否则零点两侧无法区分。
const formatTick = (bucket: string, range: TimelineRange) =>
  range === "today" ? bucket.slice(11) : bucket.slice(5)

interface TokenTrendChartProps {
  data: TimelinePoint[]
  range: TimelineRange
  onRangeChange: (range: TimelineRange) => void
  models: string[]
  model: string // 空串表示全部模型
  onModelChange: (model: string) => void
}

export function TokenTrendChart({ data, range, onRangeChange, models, model, onModelChange }: TokenTrendChartProps) {
  const { t } = useTranslation('home')
  const showDots = data.length <= DOT_LIMIT

  // color 不能省：ChartStyle 只为带 color 的 config 项生成 --color-* 变量，
  // 缺了它 stroke="var(--color-x)" 会落到无效值（bar-chart 的柱子就是这么变黑的）。
  const chartConfig: ChartConfig = {
    tokens: { label: t('timeline.series_total'), color: "var(--chart-1)" },
    cached_tokens: { label: t('timeline.series_cached'), color: "var(--chart-2)" },
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <CardTitle>{t('timeline.title')}</CardTitle>
          <div className="flex flex-wrap items-center gap-3">
            <div className="flex items-center gap-2">
              <Label htmlFor="timeline-model" className="text-xs text-muted-foreground">
                {t('timeline.model_label')}
              </Label>
              <Select
                value={model || ALL_MODELS}
                onValueChange={(next) => onModelChange(next === ALL_MODELS ? "" : next)}
              >
                <SelectTrigger id="timeline-model" size="sm" className="w-[168px]">
                  <SelectValue placeholder={t('timeline.model_all')} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ALL_MODELS}>{t('timeline.model_all')}</SelectItem>
                  {models.map((name) => (
                    <SelectItem key={name} value={name}>{name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-center gap-2">
              <Label htmlFor="timeline-range" className="text-xs text-muted-foreground">
                {t('timeline.range_label')}
              </Label>
              <Select value={range} onValueChange={(next) => onRangeChange(next as TimelineRange)}>
                <SelectTrigger id="timeline-range" size="sm" className="w-[132px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {TIMELINE_RANGES.map((item) => (
                    <SelectItem key={item} value={item}>{t(RANGE_LABEL_KEYS[item])}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {data.length === 0 ? (
          <div className="flex h-[320px] items-center justify-center text-sm text-muted-foreground">
            {t('timeline.empty')}
          </div>
        ) : (
          <ChartContainer config={chartConfig} className="aspect-auto h-[320px] w-full">
            <LineChart accessibilityLayer data={data} margin={{ left: 4, right: 12, top: 8 }}>
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="bucket"
                tickLine={false}
                axisLine={false}
                tickMargin={12}
                minTickGap={24}
                tickFormatter={(value: string) => formatTick(value, range)}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={64}
                tickFormatter={(value) => Number(value).toLocaleString()}
              />
              <ChartTooltip
                cursor={false}
                content={<ChartTooltipContent indicator="line" />}
              />
              <ChartLegend content={<ChartLegendContent />} />
              {SERIES.map(({ key, color }) => (
                <Line
                  key={key}
                  dataKey={key}
                  type="monotone"
                  stroke={color}
                  strokeWidth={2}
                  dot={showDots ? { r: 3, fill: color, stroke: color } : false}
                  activeDot={{ r: 5, fill: color, stroke: color }}
                />
              ))}
            </LineChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  )
}
