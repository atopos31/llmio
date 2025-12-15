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
import type { ProjectCount } from "@/lib/api"

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

const generateChartConfig = (data: ProjectCount[]) => {
  const config: ChartConfig = {
    calls: {
      label: "调用次数",
    },
  }

  data.forEach((item, index) => {
    config[item.project] = {
      label: item.project,
      color: predefinedColors[index % predefinedColors.length],
    }
  })

  return config
}

const generateChartData = (data: ProjectCount[]) => {
  return data.map((item, index) => ({
    project: item.project,
    calls: item.calls,
    fill: predefinedColors[index % predefinedColors.length],
  }))
}

interface ProjectRankingChartProps {
  data: ProjectCount[]
}

export function ProjectRankingChart({ data }: ProjectRankingChartProps) {
  const chartData = generateChartData(data)
  const chartConfig = generateChartConfig(data)

  return (
    <Card>
      <CardHeader>
        <CardTitle>项目调用排行</CardTitle>
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
              dataKey="project"
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
                dataKey="project"
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

