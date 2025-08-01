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
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import Loading from "@/components/loading";
import { getLogs, getProviders, type ChatLog, type Provider } from "@/lib/api";

// 格式化时间显示，自动选择合适的单位
// 假设后端返回的时间单位是纳秒
const formatTime = (nanoseconds: number): string => {
  if (nanoseconds < 1000) {
    // 纳秒级别
    return `${nanoseconds.toFixed(2)} ns`;
  } else if (nanoseconds < 1000000) {
    // 微秒级别
    return `${(nanoseconds / 1000).toFixed(2)} μs`;
  } else if (nanoseconds < 1000000000) {
    // 毫秒级别
    return `${(nanoseconds / 1000000).toFixed(2)} ms`;
  } else {
    // 秒级别
    return `${(nanoseconds / 1000000000).toFixed(2)} s`;
  }
};

// 格式化TPS显示
const formatTPS = (tps: number): string => {
  return `${tps.toFixed(2)} tokens/s`;
};

export default function LogsPage() {
  const [logs, setLogs] = useState<ChatLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(10);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(0);
  const [providers, setProviders] = useState<Provider[]>([]);

  // 筛选条件
  const [providerNameFilter, setProviderNameFilter] = useState<string>("all");
  const [statusFilter, setStatusFilter] = useState<string>("all");

  // 详情弹窗
  const [selectedLog, setSelectedLog] = useState<ChatLog | null>(null);
  const [isDialogOpen, setIsDialogOpen] = useState(false);

  // 获取提供商列表
  const fetchProviders = async () => {
    try {
      const providerList = await getProviders();
      setProviders(providerList);
    } catch (error) {
      console.error("Error fetching providers:", error);
    }
  };

  // 获取日志数据
  const fetchLogs = async () => {
    setLoading(true);
    try {
      // 处理筛选条件，"all"表示不过滤
      const providerName = providerNameFilter === "all" ? undefined : providerNameFilter;
      const status = statusFilter === "all" ? undefined : statusFilter;

      const result = await getLogs(page, pageSize, {
        providerName: providerName,
        status: status
      });

      setLogs(result.data);
      setTotal(result.total);
      setPages(result.pages);
    } catch (error) {
      console.error("Error fetching logs:", error);
    } finally {
      setLoading(false);
    }
  };

  // 初始加载
  useEffect(() => {
    fetchProviders();
    fetchLogs();
  }, [page, pageSize, providerNameFilter, statusFilter]);

  // 处理筛选条件变化
  const handleFilterChange = () => {
    setPage(1); // 重置到第一页
  };
  useEffect(() => {
    handleFilterChange();
  }, [providerNameFilter, statusFilter]);

  // 处理分页变化
  const handlePageChange = (newPage: number) => {
    if (newPage >= 1 && newPage <= pages) {
      setPage(newPage);
    }
  };

  // 刷新数据
  const handleRefresh = () => {
    fetchLogs();
  };

  // 打开详情弹窗
  const openDetailDialog = (log: ChatLog) => {
    setSelectedLog(log);
    setIsDialogOpen(true);
  };

  if (loading && logs.length === 0) return <Loading message="加载请求日志" />;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex justify-between items-center">
            <div>
              <CardTitle>请求日志</CardTitle>
              <CardDescription>系统处理的请求日志，支持分页和筛选</CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {/* 筛选区域 */}
          <div className="flex flex-wrap gap-4 mb-6 justify-between items-end">
            <div className="flex flex-wrap gap-4">
              <div className="flex items-center gap-2">
                <Label htmlFor="provider-name-filter" className="whitespace-nowrap">提供商名称</Label>
                <Select value={providerNameFilter} onValueChange={setProviderNameFilter}>
                  <SelectTrigger className="w-[180px]">
                    <SelectValue placeholder="选择提供商" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">全部</SelectItem>
                    {providers.map((provider) => (
                      <SelectItem key={provider.ID} value={provider.Name}>
                        {provider.Name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex items-center gap-2">
                <Label htmlFor="status-filter" className="whitespace-nowrap">状态</Label>
                <Select value={statusFilter} onValueChange={setStatusFilter}>
                  <SelectTrigger className="w-[180px]">
                    <SelectValue placeholder="选择状态" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">全部</SelectItem>
                    <SelectItem value="success">成功</SelectItem>
                    <SelectItem value="error">错误</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
            <Button onClick={handleRefresh}>刷新</Button>
          </div>

          {/* 日志表格 */}
          {loading ? (
            <Loading message="加载日志数据" />
          ) : logs.length == 0 ? <div className="text-center py-8 text-gray-500">暂无请求日志</div> : (
            <div className="border rounded-lg overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>时间</TableHead>
                    <TableHead>模型名称</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>Tokens</TableHead>
                    <TableHead>耗时</TableHead>
                    <TableHead>提供商模型</TableHead>
                    <TableHead>提供商名称</TableHead>

                    <TableHead>操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {logs.map((log) => (
                    <TableRow key={log.ID}>
                      <TableCell>{new Date(log.CreatedAt).toLocaleString()}</TableCell>
                      <TableCell>{log.Name}</TableCell>
                      <TableCell>
                        <span className={log.Status === 'success' ? 'text-green-600' : 'text-red-600'}>
                          {log.Status}
                        </span>
                      </TableCell>
                      <TableCell>{log.total_tokens}</TableCell>
                      <TableCell><div className="col-span-3">{formatTime(log.ChunkTime)}</div></TableCell>
                      <TableCell>{log.ProviderModel}</TableCell>
                      <TableCell>{log.ProviderName}</TableCell>


                      <TableCell>
                        <Button variant="outline" size="sm" onClick={() => openDetailDialog(log)}>
                          详情
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          {/* 分页控件 */}
          {!loading && pages > 1 && (
            <div className="flex justify-between items-center mt-6">
              <div className="text-sm text-gray-500">
                共 {total} 条记录，第 {page} 页，共 {pages} 页
              </div>
              <div className="flex space-x-2">
                <Button
                  variant="outline"
                  onClick={() => handlePageChange(page - 1)}
                  disabled={page === 1}
                >
                  上一页
                </Button>
                <Button
                  variant="outline"
                  onClick={() => handlePageChange(page + 1)}
                  disabled={page === pages}
                >
                  下一页
                </Button>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 详情弹窗 */}
      {selectedLog && (
        <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
          <DialogContent className="max-w-2xl">
            <DialogHeader>
              <DialogTitle>日志详情</DialogTitle>
              <DialogDescription>请求日志的详细信息</DialogDescription>
            </DialogHeader>
            <div className="grid gap-4 py-4">
              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">ID:</Label>
                <div className="col-span-3">{selectedLog.ID}</div>
              </div>
              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">时间:</Label>
                <div className="col-span-3">{new Date(selectedLog.CreatedAt).toLocaleString()}</div>
              </div>
              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">名称:</Label>
                <div className="col-span-3">{selectedLog.Name}</div>
              </div>
              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">提供商模型:</Label>
                <div className="col-span-3">{selectedLog.ProviderModel}</div>
              </div>
              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">提供商名称:</Label>
                <div className="col-span-3">{selectedLog.ProviderName}</div>
              </div>
              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">状态:</Label>
                <div className="col-span-3">
                  <span className={selectedLog.Status === 'success' ? 'text-green-600' : 'text-red-600'}>
                    {selectedLog.Status}
                  </span>
                </div>
              </div>
              {selectedLog.Status === 'error' && selectedLog.Error && (
                <div className="grid grid-cols-4 items-center gap-4">
                  <Label className="text-right">错误信息:</Label>
                  <div className="col-span-3 text-red-600">{selectedLog.Error}</div>
                </div>
              )}
              {selectedLog.FirstChunkTime !== undefined && (
                <div className="grid grid-cols-4 items-center gap-4">
                  <Label className="text-right">首字耗时:</Label>
                  <div className="col-span-3">{formatTime(selectedLog.FirstChunkTime)}</div>
                </div>
              )}
              {selectedLog.ChunkTime !== undefined && (
                <div className="grid grid-cols-4 items-center gap-4">
                  <Label className="text-right">耗时:</Label>
                  <div className="col-span-3">{formatTime(selectedLog.ChunkTime)}</div>
                </div>
              )}
              {selectedLog.Tps !== undefined && (
                <div className="grid grid-cols-4 items-center gap-4">
                  <Label className="text-right">TPS:</Label>
                  <div className="col-span-3">{formatTPS(selectedLog.Tps)}</div>
                </div>
              )}

              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">输入:</Label>
                <div className="col-span-3">
                  {selectedLog.prompt_tokens} tokens
                </div>
              </div>
              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">输出:</Label>
                <div className="col-span-3">
                  {selectedLog.completion_tokens} tokens
                </div>
              </div>

              <div className="grid grid-cols-4 items-center gap-4">
                <Label className="text-right">总计:</Label>
                <div className="col-span-3">
                  {selectedLog.total_tokens} tokens
                </div>
              </div>

            </div>
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}