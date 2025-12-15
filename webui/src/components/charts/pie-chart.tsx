import { Pie, PieChart } from "recharts"
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  type ChartConfig,
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart"
import type { ModelCount } from "@/lib/api"

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
const generateChartConfig = (data: ModelCount[]) => {
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
const generateChartData = (data: ModelCount[]) => {
  return data.map((item, index) => ({
    model: item.model,
    calls: item.calls,
    fill: predefinedColors[index % predefinedColors.length],
  }))
}

interface ChartPieDonutTextProps {
  data: ModelCount[]
}

export function ChartPieDonutText({ data }: ChartPieDonutTextProps) {
  const chartData = generateChartData(data)
  const chartConfig = generateChartConfig(data)
  
  return (
    <Card className="flex flex-col">
      <CardHeader className="items-center pb-0">
        <CardTitle>模型调用次数占比</CardTitle>
      </CardHeader>
      <CardContent className="flex-1 pb-0">
        <ChartContainer
          config={chartConfig}
          className="mx-auto aspect-square max-h-[500px] sm:max-h-[390px] pb-0"
        >
          <PieChart>
            <ChartTooltip
              cursor={false}
              content={<ChartTooltipContent hideLabel />}
            />
            <Pie
              data={chartData}
              dataKey="calls"
              nameKey="model"
              label
              labelLine={false}
              innerRadius={70}
              strokeWidth={1}
            />
            <ChartLegend
              content={<ChartLegendContent nameKey="model" payload={undefined} />}
              className="-translate-y-2 flex-wrap gap-2 min-h-12 *:basis-1/4 *:justify-center"
            />
          </PieChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
