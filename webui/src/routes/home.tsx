"use client"

import { useState, useEffect, Suspense, lazy, memo, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import Loading from "@/components/loading";
import {
  getMetrics,
  getModelCounts,
  getProjectCounts,
  getTimeline,
  getTimelineModels
} from "@/lib/api";
import type { MetricsData, ModelCount, ProjectCount, StatMetric, TimelinePoint, TimelineRange } from "@/lib/api";
import { toast } from "sonner";
import { RefreshCw } from "lucide-react";

// 懒加载图表组件
const ChartPieDonutText = lazy(() => import("@/components/charts/pie-chart").then(module => ({ default: module.ChartPieDonutText })));
const ModelRankingChart = lazy(() => import("@/components/charts/bar-chart").then(module => ({ default: module.ModelRankingChart })));
const ProjectChartPieDonutText = lazy(() => import("@/components/charts/project-pie-chart").then(module => ({ default: module.ProjectChartPieDonutText })));
const ProjectRankingChart = lazy(() => import("@/components/charts/project-bar-chart").then(module => ({ default: module.ProjectRankingChart })));
const TokenTrendChart = lazy(() => import("@/components/charts/token-trend-chart").then(module => ({ default: module.TokenTrendChart })));

const emptyMetrics = (): MetricsData => ({ reqs: 0, tokens: 0, prompt_tokens: 0, cached_tokens: 0 });

const DEFAULT_METRIC: StatMetric = "count";
const DEFAULT_RANGE: TimelineRange = "today";
const DEFAULT_MODEL = ""; // 空串 = 全部模型

// 缓存率 = 缓存 tokens / 输入 tokens
const formatCacheRate = (cached: number, promptTokens: number) =>
  promptTokens > 0 ? `${((cached / promptTokens) * 100).toFixed(2)}%` : "-";

// Animated counter component
const AnimatedCounter = ({ value, duration = 1000 }: { value: number; duration?: number }) => {
  const [count, setCount] = useState(0);

  useEffect(() => {
    let startTime: number | null = null;
    const animateCount = (timestamp: number) => {
      if (!startTime) startTime = timestamp;
      const progress = timestamp - startTime;
      const progressRatio = Math.min(progress / duration, 1);
      const currentValue = Math.floor(progressRatio * value);

      setCount(currentValue);

      if (progress < duration) {
        requestAnimationFrame(animateCount);
      }
    };

    requestAnimationFrame(animateCount);
  }, [value, duration]);

  return <div className="text-3xl font-bold">{count.toLocaleString()}</div>;
};

// 缓存 tokens 与缓存率，用于 Tokens 卡片
const CacheUsage = memo(({ cached, promptTokens }: { cached: number; promptTokens: number }) => {
  const { t } = useTranslation('home');
  return (
    <>
      <p className="mt-2 text-sm text-muted-foreground">
        {t('cards.cached_tokens')}: {cached.toLocaleString()}
      </p>
      <p className="text-xs text-muted-foreground">
        {t('cards.cache_rate')}: {formatCacheRate(cached, promptTokens)}
      </p>
    </>
  );
});

// 统计口径切换开关
const MetricToggle = memo(({ value, onChange }: { value: StatMetric; onChange: (value: StatMetric) => void }) => {
  const { t } = useTranslation('home');
  return (
    <div className="flex flex-wrap items-center justify-end gap-3">
      <span className="text-xs text-muted-foreground">{t('charts.metric_label')}</span>
      <RadioGroup
        value={value}
        onValueChange={(next) => onChange(next as StatMetric)}
        className="flex items-center gap-4"
      >
        <div className="flex items-center gap-2">
          <RadioGroupItem value="count" id="stat-metric-count" />
          <Label htmlFor="stat-metric-count" className="cursor-pointer text-sm font-normal">
            {t('charts.by_count')}
          </Label>
        </div>
        <div className="flex items-center gap-2">
          <RadioGroupItem value="tokens" id="stat-metric-tokens" />
          <Label htmlFor="stat-metric-tokens" className="cursor-pointer text-sm font-normal">
            {t('charts.by_tokens')}
          </Label>
        </div>
      </RadioGroup>
    </div>
  );
});

type HomeHeaderProps = {
  onRefresh: () => void;
};

const HomeHeader = memo(({ onRefresh }: HomeHeaderProps) => {
  const { t } = useTranslation('home');
  return (
    <div className="flex flex-col gap-2 flex-shrink-0">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-2xl font-bold tracking-tight">{t('title')}</h2>
        </div>
        <Button
          onClick={onRefresh}
          variant="outline"
          size="icon"
          className="ml-auto shrink-0"
          aria-label={t('refresh')}
          title={t('refresh')}
        >
          <RefreshCw className="size-4" />
        </Button>
      </div>
    </div>
  );
});

export default function Home() {
  const [loading, setLoading] = useState(true);
  const [metric, setMetric] = useState<StatMetric>(DEFAULT_METRIC);
  const [range, setRange] = useState<TimelineRange>(DEFAULT_RANGE);
  const [model, setModel] = useState<string>(DEFAULT_MODEL);

  // Real data from APIs
  const [todayMetrics, setTodayMetrics] = useState<MetricsData>(emptyMetrics);
  const [totalMetrics, setTotalMetrics] = useState<MetricsData>(emptyMetrics);
  const [modelCounts, setModelCounts] = useState<ModelCount[]>([]);
  const [projectCounts, setProjectCounts] = useState<ProjectCount[]>([]);
  const [timeline, setTimeline] = useState<TimelinePoint[]>([]);
  const [timelineModels, setTimelineModels] = useState<string[]>([]);

  const { t } = useTranslation('home');

  const fetchTodayMetrics = useCallback(async () => {
    try {
      const data = await getMetrics(0);
      setTodayMetrics(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.today_metrics', { message }));
      console.error(err);
    }
  }, [t]);

  const fetchTotalMetrics = useCallback(async () => {
    try {
      const data = await getMetrics(30);
      setTotalMetrics(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.total_metrics', { message }));
      console.error(err);
    }
  }, [t]);

  const fetchModelCounts = useCallback(async (metricBy: StatMetric, timelineRange: TimelineRange) => {
    try {
      const data = await getModelCounts(metricBy, timelineRange);
      setModelCounts(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.model_counts', { message }));
      console.error(err);
    }
  }, [t]);

  const fetchProjectCounts = useCallback(async (metricBy: StatMetric, timelineRange: TimelineRange) => {
    try {
      const data = await getProjectCounts(metricBy, timelineRange);
      setProjectCounts(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.project_counts', { message }));
      console.error(err);
    }
  }, [t]);

  const fetchTimeline = useCallback(async (timelineRange: TimelineRange, modelName: string) => {
    try {
      const data = await getTimeline(timelineRange, modelName || undefined);
      setTimeline(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.timeline', { message }));
      console.error(err);
    }
  }, [t]);

  const fetchTimelineModels = useCallback(async () => {
    try {
      const data = await getTimelineModels();
      setTimelineModels(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.timeline_models', { message }));
      console.error(err);
    }
  }, [t]);

  // 概要指标（今日/本月）与统计口径无关
  const loadSummary = useCallback(
    () => Promise.all([fetchTodayMetrics(), fetchTotalMetrics()]),
    [fetchTodayMetrics, fetchTotalMetrics]
  );

  // 图表统计随统计口径与时间维度变化（时间维度与趋势折线图共用）
  const loadStats = useCallback(
    (metricBy: StatMetric, timelineRange: TimelineRange) =>
      Promise.all([fetchModelCounts(metricBy, timelineRange), fetchProjectCounts(metricBy, timelineRange)]),
    [fetchModelCounts, fetchProjectCounts]
  );

  // 趋势图随时间维度与模型筛选变化
  const loadTimeline = useCallback(
    (timelineRange: TimelineRange, modelName: string) => fetchTimeline(timelineRange, modelName),
    [fetchTimeline]
  );

  // 首次加载，后续由刷新按钮、统计口径、时间维度与模型筛选切换触发
  useEffect(() => {
    void (async () => {
      await Promise.all([
        loadSummary(),
        loadStats(DEFAULT_METRIC, DEFAULT_RANGE),
        loadTimeline(DEFAULT_RANGE, DEFAULT_MODEL),
        fetchTimelineModels(),
      ]);
      setLoading(false);
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleRefresh = useCallback(async () => {
    setLoading(true);
    await Promise.all([
      loadSummary(),
      loadStats(metric, range),
      loadTimeline(range, model),
      fetchTimelineModels(),
    ]);
    setLoading(false);
  }, [loadSummary, loadStats, loadTimeline, fetchTimelineModels, metric, range, model]);

  const handleMetricChange = useCallback((next: StatMetric) => {
    if (next === metric) return;
    setMetric(next);
    void loadStats(next, range);
  }, [metric, range, loadStats]);

  // 时间维度联动：趋势折线图与模型/项目图表同步刷新同一 range
  const handleRangeChange = useCallback((next: TimelineRange) => {
    if (next === range) return;
    setRange(next);
    void Promise.all([loadTimeline(next, model), loadStats(metric, next)]);
  }, [range, model, metric, loadStats, loadTimeline]);

  // 模型筛选只影响趋势折线图，不改变占比/排行图表的统计口径
  const handleModelChange = useCallback((next: string) => {
    if (next === model) return;
    setModel(next);
    void loadTimeline(range, next);
  }, [model, range, loadTimeline]);

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <HomeHeader onRefresh={() => void handleRefresh()} />

      <div className="flex-1 min-h-0 overflow-y-auto">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message={t('loading')} />
          </div>
        ) : (
          <div className="space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
              <Card>
                <CardHeader>
                  <CardTitle>{t('cards.today_requests')}</CardTitle>
                  <CardDescription>{t('cards.today_requests_desc')}</CardDescription>
                </CardHeader>
                <CardContent>
                  <AnimatedCounter value={todayMetrics.reqs} />
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>{t('cards.today_tokens')}</CardTitle>
                  <CardDescription>{t('cards.today_tokens_desc')}</CardDescription>
                </CardHeader>
                <CardContent>
                  <AnimatedCounter value={todayMetrics.tokens} />
                  <CacheUsage cached={todayMetrics.cached_tokens} promptTokens={todayMetrics.prompt_tokens} />
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>{t('cards.monthly_requests')}</CardTitle>
                  <CardDescription>{t('cards.monthly_requests_desc')}</CardDescription>
                </CardHeader>
                <CardContent>
                  <AnimatedCounter value={totalMetrics.reqs} />
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>{t('cards.monthly_tokens')}</CardTitle>
                  <CardDescription>{t('cards.monthly_tokens_desc')}</CardDescription>
                </CardHeader>
                <CardContent>
                  <AnimatedCounter value={totalMetrics.tokens} />
                  <CacheUsage cached={totalMetrics.cached_tokens} promptTokens={totalMetrics.prompt_tokens} />
                </CardContent>
              </Card>
            </div>

            <Suspense fallback={<div className="h-64 flex items-center justify-center">
              <Loading message={t('loading_chart')} />
            </div>}>
              <TokenTrendChart
                data={timeline}
                range={range}
                onRangeChange={handleRangeChange}
                models={timelineModels}
                model={model}
                onModelChange={handleModelChange}
              />
            </Suspense>

            <MetricToggle value={metric} onChange={handleMetricChange} />

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ChartPieDonutText data={modelCounts} metric={metric} />
              </Suspense>

              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ProjectChartPieDonutText data={projectCounts} metric={metric} />
              </Suspense>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ModelRankingChart data={modelCounts} metric={metric} />
              </Suspense>

              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ProjectRankingChart data={projectCounts} metric={metric} />
              </Suspense>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
