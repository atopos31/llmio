"use client"

import { useState, useEffect } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import Loading from "@/components/loading";
import { 
  getSystemStatus,
  getProviderMetrics,
  getMetrics,
  getModelCounts
} from "@/lib/api";
import type { SystemStatus, ProviderMetric, MetricsData, ModelCount } from "@/lib/api";
import { ChartPieDonutText } from "@/components/charts/pie-chart";
import { ModelRankingChart } from "@/components/charts/bar-chart";

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

export default function Home() {
  const [, setStatus] = useState<SystemStatus | null>(null);
  const [, setMetrics] = useState<ProviderMetric[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeChart, setActiveChart] = useState<"distribution" | "ranking">("distribution");
  
  // Real data from APIs
  const [todayMetrics, setTodayMetrics] = useState<MetricsData>({ reqs: 0, tokens: 0 });
  const [totalMetrics, setTotalMetrics] = useState<MetricsData>({ reqs: 0, tokens: 0 });
  const [modelCounts, setModelCounts] = useState<ModelCount[]>([]);

  useEffect(() => {
    Promise.all([fetchStatus(), fetchMetrics(), fetchTodayMetrics(), fetchTotalMetrics(), fetchModelCounts()]);
  }, []);

  const fetchStatus = async () => {
    try {
      const data = await getSystemStatus();
      setStatus(data);
    } catch (err) {
      setError("获取系统状态失败");
      console.error(err);
    }
  };

  const fetchMetrics = async () => {
    try {
      const data = await getProviderMetrics();
      setMetrics(data);
    } catch (err) {
      setError("获取提供商指标失败");
      console.error(err);
    }
  };
  
  const fetchTodayMetrics = async () => {
    try {
      const data = await getMetrics(1);
      setTodayMetrics(data);
    } catch (err) {
      setError("获取今日指标失败");
      console.error(err);
    }
  };
  
  const fetchTotalMetrics = async () => {
    try {
      const data = await getMetrics(30); // Get last 30 days for "total" metrics
      setTotalMetrics(data);
    } catch (err) {
      setError("获取总计指标失败");
      console.error(err);
    }
  };
  
  const fetchModelCounts = async () => {
    try {
      const data = await getModelCounts();
      setModelCounts(data);
    } catch (err) {
      setError("获取模型调用统计失败");
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  if (loading) return <Loading message="加载系统概览" />;
  if (error) return <div className="text-red-500">{error}</div>;

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        <Card>
          <CardHeader>
            <CardTitle>今日请求</CardTitle>
            <CardDescription>今日处理的请求总数</CardDescription>
          </CardHeader>
          <CardContent>
            <AnimatedCounter value={todayMetrics.reqs} />
          </CardContent>
        </Card>
        
        <Card>
          <CardHeader>
            <CardTitle>今日Tokens</CardTitle>
            <CardDescription>今日处理的Tokens总数</CardDescription>
          </CardHeader>
          <CardContent>
            <AnimatedCounter value={todayMetrics.tokens} />
          </CardContent>
        </Card>
        
        <Card>
          <CardHeader>
            <CardTitle>本月请求</CardTitle>
            <CardDescription>最近30天处理的请求总数</CardDescription>
          </CardHeader>
          <CardContent>
            <AnimatedCounter value={totalMetrics.reqs} />
          </CardContent>
        </Card>
        
        <Card>
          <CardHeader>
            <CardTitle>本月Tokens</CardTitle>
            <CardDescription>最近30天处理的Tokens总数</CardDescription>
          </CardHeader>
          <CardContent>
            <AnimatedCounter value={totalMetrics.tokens} />
          </CardContent>
        </Card>
      </div>
      
      <Card>
        <CardHeader>
          <CardTitle>模型数据分析</CardTitle>
          <CardDescription>模型调用统计分析</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex gap-2 mb-4">
            <Button 
              variant={activeChart === "distribution" ? "default" : "outline"} 
              onClick={() => setActiveChart("distribution")}
            >
              调用次数分布
            </Button>
            <Button 
              variant={activeChart === "ranking" ? "default" : "outline"} 
              onClick={() => setActiveChart("ranking")}
            >
              调用次数排行
            </Button>
          </div>
          <div className="mt-4">
            {activeChart === "distribution" ? <ChartPieDonutText data={modelCounts} /> : <ModelRankingChart data={modelCounts} />}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}