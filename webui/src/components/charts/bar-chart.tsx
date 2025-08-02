"use client"

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

// 模拟后端数据
const mockModelData = [
  { model: "GPT-4", calls: 1240 },
  { model: "Claude-3", calls: 850 },
  { model: "Geminidd", calls: 620 },
  { model: "LLaMA-2", calls: 480 },
  { model: "Mistral", calls: 320 },
  { model: "Yi-34B", calls: 275 },
  { model: "DeepSeek", calls: 200 },
]

// 根据模型数据生成图表配置
const generateChartConfig = (data: typeof mockModelData) => {
  const config: ChartConfig = {
    calls: {
      label: "调用次数",
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
const generateChartData = (data: typeof mockModelData) => {
  return data.map((item, index) => ({
    model: item.model,
    calls: item.calls,
    fill: predefinedColors[index % predefinedColors.length],
  }))
}

export function ModelRankingChart() {
  const chartData = generateChartData(mockModelData)
  const chartConfig = generateChartConfig(mockModelData)

  return (
    <Card>
      <CardHeader>
        <CardTitle>模型调用排行</CardTitle>
      </CardHeader>
      <CardContent>
        <ChartContainer config={chartConfig} className="max-h-[500px] sm:max-h-[390px]">
          <BarChart
            accessibilityLayer
            data={chartData}
            layout="vertical"
            margin={{
              right: 16,
            }}
          >
            <CartesianGrid horizontal={false} />
            <YAxis
              dataKey="model"
              type="category"
              tickLine={false}
              tickMargin={10}
              axisLine={false}
              tickFormatter={(value) => value.slice(0, 10)}
              hide
            />
            <XAxis dataKey="calls" type="number" hide />
            <ChartTooltip
              cursor={false}
              content={<ChartTooltipContent indicator="line" hideLabel />}
            />
            <Bar
              dataKey="calls"
              layout="vertical"
              fill="var(--color-calls)"
              radius={4}
            >
              <LabelList
                dataKey="model"
                position="insideLeft"
                offset={8}
                className="fill-white font-medium"
                fontSize={12}
              />
              <LabelList
                dataKey="calls"
                position="right"
                offset={8}
                className="fill-foreground"
                fontSize={12}
              />
            </Bar>
          </BarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}