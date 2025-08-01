"use client"

import { useState, useEffect } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import Loading from "@/components/loading";
import { 
  getSystemStatus,
  getProviderMetrics
} from "@/lib/api";
import type { SystemStatus, ProviderMetric } from "@/lib/api";
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
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [metrics, setMetrics] = useState<ProviderMetric[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeChart, setActiveChart] = useState<"distribution" | "ranking">("distribution");

  // Mock data for the new metrics
  const [todayRequests] = useState(1250);
  const [todayTokens] = useState(250000);
  const [totalRequests] = useState(18560);
  const [totalTokens] = useState(3875000);

  useEffect(() => {
    Promise.all([fetchStatus(), fetchMetrics()]);
  }, []);

  const fetchStatus = async () => {
    try {
      const data = await getSystemStatus();
      setStatus(data);
    } catch (err) {
      setError("获取系统状态失败");
      console.error(err);
    } finally {
      setLoading(false);
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
            <AnimatedCounter value={todayRequests} />
          </CardContent>
        </Card>
        
        <Card>
          <CardHeader>
            <CardTitle>今日Tokens</CardTitle>
            <CardDescription>今日处理的Tokens总数</CardDescription>
          </CardHeader>
          <CardContent>
            <AnimatedCounter value={todayTokens} />
          </CardContent>
        </Card>
        
        <Card>
          <CardHeader>
            <CardTitle>总计请求</CardTitle>
            <CardDescription>历史处理的请求总数</CardDescription>
          </CardHeader>
          <CardContent>
            <AnimatedCounter value={totalRequests} />
          </CardContent>
        </Card>
        
        <Card>
          <CardHeader>
            <CardTitle>总计Tokens</CardTitle>
            <CardDescription>历史处理的Tokens总数</CardDescription>
          </CardHeader>
          <CardContent>
            <AnimatedCounter value={totalTokens} />
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
            {activeChart === "distribution" ? <ChartPieDonutText /> : <ModelRankingChart />}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}