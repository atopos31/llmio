"use client"

import { useState, useEffect, Suspense, lazy, memo, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import Loading from "@/components/loading";
import {
  getMetrics,
  getModelCounts,
  getProjectCounts
} from "@/lib/api";
import type { MetricsData, ModelCount, ProjectCount } from "@/lib/api";
import { toast } from "sonner";
import { RefreshCw } from "lucide-react";

// 懒加载图表组件
const ChartPieDonutText = lazy(() => import("@/components/charts/pie-chart").then(module => ({ default: module.ChartPieDonutText })));
const ModelRankingChart = lazy(() => import("@/components/charts/bar-chart").then(module => ({ default: module.ModelRankingChart })));
const ProjectChartPieDonutText = lazy(() => import("@/components/charts/project-pie-chart").then(module => ({ default: module.ProjectChartPieDonutText })));
const ProjectRankingChart = lazy(() => import("@/components/charts/project-bar-chart").then(module => ({ default: module.ProjectRankingChart })));

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

  // Real data from APIs
  const [todayMetrics, setTodayMetrics] = useState<MetricsData>({ reqs: 0, tokens: 0 });
  const [totalMetrics, setTotalMetrics] = useState<MetricsData>({ reqs: 0, tokens: 0 });
  const [modelCounts, setModelCounts] = useState<ModelCount[]>([]);
  const [projectCounts, setProjectCounts] = useState<ProjectCount[]>([]);

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

  const fetchModelCounts = useCallback(async () => {
    try {
      const data = await getModelCounts();
      setModelCounts(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.model_counts', { message }));
      console.error(err);
    }
  }, [t]);

  const fetchProjectCounts = useCallback(async () => {
    try {
      const data = await getProjectCounts();
      setProjectCounts(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('errors.project_counts', { message }));
      console.error(err);
    }
  }, [t]);

  const load = useCallback(async () => {
    setLoading(true);
    await Promise.all([fetchTodayMetrics(), fetchTotalMetrics(), fetchModelCounts(), fetchProjectCounts()]);
    setLoading(false);
  }, [fetchModelCounts, fetchProjectCounts, fetchTodayMetrics, fetchTotalMetrics]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <HomeHeader onRefresh={() => void load()} />

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
                </CardContent>
              </Card>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ChartPieDonutText data={modelCounts} />
              </Suspense>

              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ProjectChartPieDonutText data={projectCounts} />
              </Suspense>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ModelRankingChart data={modelCounts} />
              </Suspense>

              <Suspense fallback={<div className="h-64 flex items-center justify-center">
                <Loading message={t('loading_chart')} />
              </div>}>
                <ProjectRankingChart data={projectCounts} />
              </Suspense>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
