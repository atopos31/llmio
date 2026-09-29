"use client"

import { useTranslation } from "react-i18next"
import { Bar, BarChart, CartesianGrid, LabelList, XAxis, YAxis } from "recharts"

import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart"
import type { ModelCount, StatMetric } from "@/lib/api"

// 预定义颜色数组，按顺序生成颜色
const predefinedColors = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
  "var(--chart-6)",
  "var(--chart-7)",
  "var(--chart-8)",
  "var(--chart-9)",
  "var(--chart-10)",
]

// 根据模型数据生成图表配置
const generateChartConfig = (data: ModelCount[], metricLabel: string) => {
  const config: ChartConfig = {
    value: {
      label: metricLabel,
    },
  }

  data.forEach((item, index) => {
    config[item.model] = {
      label: item.model,
      color: predefinedColors[index % predefinedColors.length],
    }
  })

  return config
}

// 根据模型数据生成图表数据
const generateChartData = (data: ModelCount[]) => {
  return data.map((item, index) => ({
    model: item.model,
    value: item.value,
    fill: predefinedColors[index % predefinedColors.length],
  }))
}

interface ModelRankingChartProps {
  data: ModelCount[]
  metric: StatMetric
}

export function ModelRankingChart({ data, metric }: ModelRankingChartProps) {
  const { t } = useTranslation('home')
  const chartData = generateChartData(data)
  const chartConfig = generateChartConfig(data, t(metric === 'tokens' ? 'charts.metric_tokens' : 'charts.metric_count'))

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t(metric === 'tokens' ? 'charts.model_bar_tokens' : 'charts.model_bar_count')}</CardTitle>
      </CardHeader>
      <CardContent>
        <ChartContainer config={chartConfig} className="aspect-auto h-[320px] w-full">
          <BarChart
            accessibilityLayer
            data={chartData}
            barSize={32}
          >
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="model"
              tickLine={false}
              axisLine={false}
              tickMargin={16}
              interval={0}
              tickFormatter={(value) => String(value)}
            />
            <YAxis
              dataKey="value"
              tickLine={false}
              axisLine={false}
              tickFormatter={(value) => Number(value).toLocaleString()}
              width={60}
            />
            <ChartTooltip
              cursor={false}
              content={<ChartTooltipContent indicator="line" hideLabel />}
            />
            <Bar
              dataKey="value"
              fill="var(--color-value)"
              radius={[8, 8, 0, 0]}
            >
              <LabelList
                dataKey="value"
                position="top"
                offset={12}
                className="fill-foreground font-medium"
                fontSize={12}
              />
            </Bar>
          </BarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
