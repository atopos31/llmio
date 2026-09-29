import { useTranslation } from "react-i18next"
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
import type { ProjectCount, StatMetric } from "@/lib/api"

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

const generateChartConfig = (data: ProjectCount[], metricLabel: string) => {
  const config: ChartConfig = {
    value: {
      label: metricLabel,
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
    value: item.value,
    fill: predefinedColors[index % predefinedColors.length],
  }))
}

interface ProjectChartPieDonutTextProps {
  data: ProjectCount[]
  metric: StatMetric
}

export function ProjectChartPieDonutText({ data, metric }: ProjectChartPieDonutTextProps) {
  const { t } = useTranslation('home')
  const chartData = generateChartData(data)
  const chartConfig = generateChartConfig(data, t(metric === 'tokens' ? 'charts.metric_tokens' : 'charts.metric_count'))

  return (
    <Card className="flex flex-col">
      <CardHeader className="items-center pb-0">
        <CardTitle>{t(metric === 'tokens' ? 'charts.project_pie_tokens' : 'charts.project_pie_count')}</CardTitle>
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
              dataKey="value"
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
