"use client"

import { TrendingUp } from "lucide-react"
import { Bar, BarChart, CartesianGrid, XAxis } from "recharts"

import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart"

// Mock data for model rankings
const chartData = [
  { model: "GPT-4", calls: 1240 },
  { model: "Claude-3", calls: 850 },
  { model: "Gemini Pro", calls: 620 },
  { model: "LLaMA-2", calls: 480 },
  { model: "Mistral", calls: 320 },
]

const chartConfig = {
  calls: {
    label: "调用次数",
    color: "var(--chart-1)",
  },
} satisfies ChartConfig

export function ModelRankingChart() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>模型调用排行</CardTitle>
      </CardHeader>
      <CardContent>
        <ChartContainer config={chartConfig} className="mx-auto aspect-square max-h-[390px] pb-0">
          <BarChart accessibilityLayer data={chartData}>
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="model"
              tickLine={false}
              tickMargin={10}
              axisLine={false}
              tickFormatter={(value) => value.slice(0, 6)}
            />
            <ChartTooltip
              cursor={false}
              content={<ChartTooltipContent hideLabel />}
            />
            <Bar dataKey="calls" fill="var(--color-calls)" radius={8} />
          </BarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}