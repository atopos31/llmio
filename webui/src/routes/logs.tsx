import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { 
  Table, 
  TableBody, 
  TableCell, 
  TableHead, 
  TableHeader, 
  TableRow 
} from "@/components/ui/table";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import Loading from "@/components/loading";

export default function LogsPage() {
  const [logs, setLogs] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [limit, setLimit] = useState(50);

  // 生成假数据
  const generateFakeLogs = (count: number) => {
    const models = ['gpt-4', 'gpt-3.5-turbo', 'claude-3-opus', 'llama-3-70b'];
    const providers = ['OpenAI', 'Anthropic', 'Azure', 'HuggingFace'];
    const statuses = ['success', 'error', 'timeout'];
    
    return Array.from({ length: count }, (_, i) => ({
      id: i + 1,
      timestamp: new Date(Date.now() - Math.floor(Math.random() * 10000000)).toISOString(),
      model: models[Math.floor(Math.random() * models.length)],
      provider: providers[Math.floor(Math.random() * providers.length)],
      status: statuses[Math.floor(Math.random() * statuses.length)],
      response_time: Math.floor(Math.random() * 5000) + 100,
      input_tokens: Math.floor(Math.random() * 1000) + 100,
      output_tokens: Math.floor(Math.random() * 2000) + 50
    }));
  };

  useEffect(() => {
    // 模拟API调用延迟
    const timer = setTimeout(() => {
      const fakeLogs = generateFakeLogs(limit);
      setLogs(fakeLogs);
      setLoading(false);
    }, 500);
    
    return () => clearTimeout(timer);
  }, [limit]);

  const handleLimitChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setLimit(parseInt(e.target.value) || 0) 
  };

  const handleRefresh = () => {
    setLoading(true);
    const timer = setTimeout(() => {
      const fakeLogs = generateFakeLogs(limit);
      setLogs(fakeLogs);
      setLoading(false);
    }, 500);
    
    return () => clearTimeout(timer);
  };

  if (loading) return <Loading message="加载请求日志" />;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex justify-between items-center">
            <div>
              <CardTitle>最近请求</CardTitle>
              <CardDescription>系统最近处理的请求日志</CardDescription>
            </div>
            <div className="flex items-center space-x-2">
              <Label htmlFor="limit" className="form-label">显示条数:</Label>
              <Input
                id="limit"
                className="form-input w-20"
                min="1"
                value={limit}
                onChange={handleLimitChange}
              />
              <Button onClick={handleRefresh}>刷新</Button>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {logs.length === 0 ? (
            <div className="text-center py-8 text-gray-500">
              暂无请求日志
            </div>
          ) : (
            <div className="border rounded-lg overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>时间</TableHead>
                    <TableHead>模型</TableHead>
                    <TableHead>提供商</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>响应时间(ms)</TableHead>
                    <TableHead>输入Token</TableHead>
                    <TableHead>输出Token</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {logs.map((log) => (
                    <TableRow key={log.id}>
                      <TableCell>{new Date(log.timestamp).toLocaleString()}</TableCell>
                      <TableCell>{log.model}</TableCell>
                      <TableCell>{log.provider}</TableCell>
                      <TableCell>
                        <span className={log.status === 'success' ? 'text-green-600' : 'text-red-600'}>
                          {log.status}
                        </span>
                      </TableCell>
                      <TableCell>{log.response_time}</TableCell>
                      <TableCell>{log.input_tokens}</TableCell>
                      <TableCell>{log.output_tokens}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}