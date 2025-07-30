import { useState, useEffect } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import Loading from "@/components/loading";
import { 
  getSystemStatus,
  getProviderMetrics
} from "@/lib/api";
import type { SystemStatus, ProviderMetric } from "@/lib/api";

export default function Home() {
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [metrics, setMetrics] = useState<ProviderMetric[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

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
      {status && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
          <Card>
            <CardHeader>
              <CardTitle>提供商数量</CardTitle>
              <CardDescription>系统中配置的AI提供商</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="text-3xl font-bold">{status.total_providers}</div>
            </CardContent>
          </Card>
          
          <Card>
            <CardHeader>
              <CardTitle>模型数量</CardTitle>
              <CardDescription>系统中配置的AI模型</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="text-3xl font-bold">{status.total_models}</div>
            </CardContent>
          </Card>
          
          <Card>
            <CardHeader>
              <CardTitle>活跃请求</CardTitle>
              <CardDescription>当前正在处理的请求数</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="text-3xl font-bold">{status.active_requests}</div>
            </CardContent>
          </Card>
          
          <Card>
            <CardHeader>
              <CardTitle>系统版本</CardTitle>
              <CardDescription>当前运行的系统版本</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="text-3xl font-bold">{status.version}</div>
            </CardContent>
          </Card>
        </div>
      )}
      
      <Card>
        <CardHeader>
          <CardTitle>提供商性能概览</CardTitle>
          <CardDescription>各提供商的关键性能指标</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">提供商</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">成功率</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">平均响应时间</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">总请求数</th>
                </tr>
              </thead>
              <tbody className=" divide-y divide-gray-200">
                {metrics.map((metric) => (
                  <tr key={metric.provider_id}>
                    <td className="px-6 py-4 whitespace-nowrap font-medium">{metric.provider_name}</td>
                    <td className="px-6 py-4 whitespace-nowrap">
                      <span className={metric.success_rate >= 0.9 ? "text-green-600" : "text-red-600"}>
                        {(metric.success_rate * 100).toFixed(2)}%
                      </span>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap">{metric.avg_response_time}ms</td>
                    <td className="px-6 py-4 whitespace-nowrap">{metric.total_requests}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}