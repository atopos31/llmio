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

interface ProjectChartPieDonutTextProps {
  data: ProjectCount[]
}

export function ProjectChartPieDonutText({ data }: ProjectChartPieDonutTextProps) {
  const chartData = generateChartData(data)
  const chartConfig = generateChartConfig(data)

  return (
    <Card className="flex flex-col">
      <CardHeader className="items-center pb-0">
        <CardTitle>项目调用次数占比</CardTitle>
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
              nameKey="project"
              label
              labelLine={false}
              innerRadius={70}
              strokeWidth={1}
            />
            <ChartLegend
              content={<ChartLegendContent nameKey="project" payload={undefined} />}
              className="-translate-y-2 flex-wrap gap-2 min-h-12 *:basis-1/4 *:justify-center"
            />
          </PieChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
