import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import Loading from "@/components/loading";
import { getProviderMetrics, getSystemConfig, getSystemStatus, updateSystemConfig } from "@/lib/api";
import type { ProviderMetric, SystemConfig, SystemStatus } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export default function SystemPage() {
  const [config, setConfig] = useState<SystemConfig>({
    enable_smart_routing: true,
    success_rate_weight: 0.7,
    response_time_weight: 0.3,
    decay_threshold_hours: 24,
    min_weight: 1,
  });
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [metrics, setMetrics] = useState<ProviderMetric[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([fetchConfig(), fetchStatus(), fetchMetrics()]);
  }, []);

  const fetchConfig = async () => {
    try {
      const data = await getSystemConfig();
      setConfig(data);
    } catch (err) {
      setError("获取系统配置失败");
      console.error(err);
    }
  };

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

  const handleSave = async () => {
    try {
      setSaving(true);
      await updateSystemConfig(config);
      setError(null);
    } catch (err) {
      setError("保存配置失败");
      console.error(err);
    } finally {
      setSaving(false);
    }
  };

  const handleChange = (field: keyof SystemConfig, value: any) => {
    setConfig(prev => ({
      ...prev,
      [field]: value
    }));
  };

  if (loading) return <Loading message="加载系统信息" />;
  if (error) return <div className="text-red-500">{error}</div>;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>系统状态</CardTitle>
          <CardDescription>当前系统运行状态</CardDescription>
        </CardHeader>
        <CardContent>
          {status && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <div className="border p-4 rounded-lg">
                <div className="text-2xl font-bold">{status.total_providers}</div>
                <div className="text-gray-500">提供商数量</div>
              </div>
              <div className="border p-4 rounded-lg">
                <div className="text-2xl font-bold">{status.total_models}</div>
                <div className="text-gray-500">模型数量</div>
              </div>
              <div className="border p-4 rounded-lg">
                <div className="text-2xl font-bold">{status.active_requests}</div>
                <div className="text-gray-500">活跃请求</div>
              </div>
              <div className="border p-4 rounded-lg">
                <div className="text-2xl font-bold">{status.version}</div>
                <div className="text-gray-500">系统版本</div>
              </div>
            </div>
          )}
        </CardContent>
      </Card>
      
      <Card>
        <CardHeader>
          <CardTitle>提供商性能指标</CardTitle>
          <CardDescription>各提供商的性能表现</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">提供商</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">成功率</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">平均响应时间(ms)</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">总请求数</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">成功数</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">失败数</th>
                </tr>
              </thead>
              <tbody className=" divide-y divide-gray-200">
                {metrics.map((metric) => (
                  <tr key={metric.provider_id}>
                    <td className="px-6 py-4 whitespace-nowrap">{metric.provider_name}</td>
                    <td className="px-6 py-4 whitespace-nowrap">
                      <span className={metric.success_rate >= 0.9 ? "text-green-600" : "text-red-600"}>
                        {(metric.success_rate * 100).toFixed(2)}%
                      </span>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap">{metric.avg_response_time}</td>
                    <td className="px-6 py-4 whitespace-nowrap">{metric.total_requests}</td>
                    <td className="px-6 py-4 whitespace-nowrap text-green-600">{metric.success_count}</td>
                    <td className="px-6 py-4 whitespace-nowrap text-red-600">{metric.failure_count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>
      
      <Card>
        <CardHeader>
          <CardTitle>智能路由配置</CardTitle>
          <CardDescription>配置智能路由算法参数</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          <div className="flex items-center justify-between form-group">
            <div>
              <Label htmlFor="enable-smart-routing" className="form-label">启用智能路由</Label>
              <p className="text-sm text-gray-500">根据性能指标自动选择最佳提供商</p>
            </div>
            <Switch
              id="enable-smart-routing"
              checked={config.enable_smart_routing}
              onCheckedChange={(checked) => handleChange('enable_smart_routing', checked)}
            />
          </div>
          
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div className="form-group">
              <Label htmlFor="success-rate-weight" className="form-label">成功率权重</Label>
              <Input
                id="success-rate-weight"
                className="form-input"
                type="number"
                step="0.01"
                min="0"
                max="1"
                value={config.success_rate_weight}
                onChange={(e) => handleChange('success_rate_weight', parseFloat(e.target.value))}
              />
              <p className="text-sm text-gray-500 mt-1">在智能路由中成功率的权重 (0-1)</p>
            </div>
            
            <div className="form-group">
              <Label htmlFor="response-time-weight" className="form-label">响应时间权重</Label>
              <Input
                id="response-time-weight"
                className="form-input"
                type="number"
                step="0.01"
                min="0"
                max="1"
                value={config.response_time_weight}
                onChange={(e) => handleChange('response_time_weight', parseFloat(e.target.value))}
              />
              <p className="text-sm text-gray-500 mt-1">在智能路由中响应时间的权重 (0-1)</p>
            </div>
            
            <div className="form-group">
              <Label htmlFor="decay-threshold" className="form-label">衰减阈值 (小时)</Label>
              <Input
                id="decay-threshold"
                className="form-input"
                type="number"
                min="1"
                value={config.decay_threshold_hours}
                onChange={(e) => handleChange('decay_threshold_hours', parseInt(e.target.value))}
              />
              <p className="text-sm text-gray-500 mt-1">性能指标衰减的时间阈值</p>
            </div>
            
            <div className="form-group">
              <Label htmlFor="min-weight" className="form-label">最小权重</Label>
              <Input
                id="min-weight"
                className="form-input"
                type="number"
                min="1"
                value={config.min_weight}
                onChange={(e) => handleChange('min_weight', parseInt(e.target.value))}
              />
              <p className="text-sm text-gray-500 mt-1">分配给提供商的最小权重值</p>
            </div>
          </div>
          
          <Button onClick={handleSave} disabled={saving}>
            {saving ? "保存中..." : "保存配置"}
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}