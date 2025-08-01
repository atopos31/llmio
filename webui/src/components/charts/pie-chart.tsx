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
export const description = "A donut chart with text"
const chartData = [
  { model: "gpt4", calls: 275, fill: "var(--color-gpt4)" },
  { model: "deepseekv3", calls: 200, fill: "var(--color-deepseekv3)" },
  { model: "qwen", calls: 287, fill: "var(--color-qwen)" },
  { model: "gemini", calls: 173, fill: "var(--color-gemini)" },
  { model: "other", calls: 190, fill: "var(--color-other)" },
]
const chartConfig = {
  calls: {
    label: "calls",
  },
  gpt4: {
    label: "gpt4",
    color: "var(--chart-1)",
  },
  deepseekv3: {
    label: "deepseekv3",
    color: "var(--chart-2)",
  },
  qwen: {
    label: "qwen",
    color: "var(--chart-3)",
  },
  gemini: {
    label: "gemini",
    color: "var(--chart-4)",
  },
  other: {
    label: "Other",
    color: "var(--chart-5)",
  },
} satisfies ChartConfig
export function ChartPieDonutText() {
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
            >
              
            </Pie>
            <ChartLegend
              content={<ChartLegendContent nameKey="model" payload={undefined} />}
              className="-translate-y-2 flex-wrap gap-2 *:basis-1/4 *:justify-center"
            />
          </PieChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}