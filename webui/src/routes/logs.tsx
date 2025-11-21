import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import Loading from "@/components/loading";
import { getLogs, getProviders, getModels, getUserAgents, type ChatLog, type Provider, type Model, getProviderTemplates } from "@/lib/api";

// 格式化时间显示
const formatTime = (nanoseconds: number): string => {
  if (nanoseconds < 1000) return `${nanoseconds.toFixed(2)} ns`;
  if (nanoseconds < 1000000) return `${(nanoseconds / 1000).toFixed(2)} μs`;
  if (nanoseconds < 1000000000) return `${(nanoseconds / 1000000).toFixed(2)} ms`;
  return `${(nanoseconds / 1000000000).toFixed(2)} s`;
};

export default function LogsPage() {
  const [logs, setLogs] = useState<ChatLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(10);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(0);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [userAgents, setUserAgents] = useState<string[]>([]);
  // 筛选条件
  const [providerNameFilter, setProviderNameFilter] = useState<string>("all");
  const [modelFilter, setModelFilter] = useState<string>("all");
  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [styleFilter, setStyleFilter] = useState<string>("all");
  const [userAgentFilter, setUserAgentFilter] = useState<string>("all");
  const [availableStyles, setAvailableStyles] = useState<string[]>([]);
  const navigate = useNavigate();
  // 详情弹窗
  const [selectedLog, setSelectedLog] = useState<ChatLog | null>(null);
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  // 获取数据
  const fetchProviders = async () => {
    try {
      const providerList = await getProviders();
      setProviders(providerList);
      const templates = await getProviderTemplates();
      const styleTypes = templates.map(template => template.type);
      setAvailableStyles(styleTypes);
    } catch (error) {
      console.error("Error fetching providers:", error);
    }
  };
  const fetchModels = async () => {
    try {
      const modelList = await getModels();
      setModels(modelList);
    } catch (error) {
      console.error("Error fetching models:", error);
    }
  };
  const fetchUserAgents = async () => {
    try {
      const userAgentList = await getUserAgents();
      setUserAgents(userAgentList);
    } catch (error) {
      console.error("Error fetching user agents:", error);
    }
  };
  const fetchLogs = async () => {
    setLoading(true);
    try {
      const result = await getLogs(page, pageSize, {
        providerName: providerNameFilter === "all" ? undefined : providerNameFilter,
        name: modelFilter === "all" ? undefined : modelFilter,
        status: statusFilter === "all" ? undefined : statusFilter,
        style: styleFilter === "all" ? undefined : styleFilter,
        userAgent: userAgentFilter === "all" ? undefined : userAgentFilter
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
  useEffect(() => {
    fetchProviders();
    fetchModels();
    fetchUserAgents();
    fetchLogs();
  }, [page, pageSize, providerNameFilter, modelFilter, statusFilter, styleFilter, userAgentFilter]);
  const handleFilterChange = () => {
    setPage(1);
  };
  useEffect(() => {
    handleFilterChange();
  }, [providerNameFilter, modelFilter, statusFilter, styleFilter, userAgentFilter]);
  const handlePageChange = (newPage: number) => {
    if (newPage >= 1 && newPage <= pages) setPage(newPage);
  };
  const handleRefresh = () => {
    fetchLogs();
  };
  const openDetailDialog = (log: ChatLog) => {
    setSelectedLog(log);
    setIsDialogOpen(true);
  };
  const canViewChatIO = (log: ChatLog) => log.Status === 'success' && log.ChatIO;
  const handleViewChatIO = (log: ChatLog) => {
    if (!canViewChatIO(log)) return;
    navigate(`/logs/${log.ID}/chat-io`);
  };
  // 布局开始
  return (
    <div className="h-full min-h-0 flex flex-col gap-4 p-1">
      {/* 顶部标题和刷新 */}
      <div className="flex flex-col sm:flex-row sm:justify-between sm:items-center gap-4 flex-shrink-0">
        <div>
          <h2 className="text-2xl font-bold tracking-tight">请求日志</h2>
          <p className="text-sm text-muted-foreground">系统处理的请求日志，支持分页和筛选</p>
        </div>
        <Button onClick={handleRefresh} className="w-full sm:w-auto">刷新</Button>
      </div>
      {/* 筛选区域 */}
      <div className="flex flex-col lg:flex-row gap-4 flex-shrink-0">
        <div className="flex flex-wrap gap-4 items-end">
          <div className="flex flex-col gap-2 w-full sm:w-auto">
            <Label className="text-xs text-muted-foreground">模型名称</Label>
            <Select value={modelFilter} onValueChange={setModelFilter}>
              <SelectTrigger className="w-full sm:w-[160px] h-9">
                <SelectValue placeholder="选择模型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {models.map((model) => (
                  <SelectItem key={model.ID} value={model.Name}>{model.Name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-full sm:w-auto">
            <Label className="text-xs text-muted-foreground">提供商</Label>
            <Select value={providerNameFilter} onValueChange={setProviderNameFilter}>
              <SelectTrigger className="w-full sm:w-[160px] h-9">
                <SelectValue placeholder="选择提供商" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {providers.map((p) => (
                  <SelectItem key={p.ID} value={p.Name}>{p.Name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-full sm:w-auto">
            <Label className="text-xs text-muted-foreground">状态</Label>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-full sm:w-[130px] h-9">
                <SelectValue placeholder="状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                <SelectItem value="success">成功</SelectItem>
                <SelectItem value="error">错误</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-full sm:w-auto">
            <Label className="text-xs text-muted-foreground">类型</Label>
            <Select value={styleFilter} onValueChange={setStyleFilter}>
              <SelectTrigger className="w-full sm:w-[130px] h-9">
                <SelectValue placeholder="类型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {availableStyles.map((s) => <SelectItem key={s} value={s}>{s}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-full sm:w-auto">
            <Label className="text-xs text-muted-foreground">用户代理</Label>
            <Select value={userAgentFilter} onValueChange={setUserAgentFilter}>
              <SelectTrigger className="w-full sm:w-[160px] h-9">
                <SelectValue placeholder="User Agent" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {userAgents.map((ua) => (
                  <SelectItem key={ua} value={ua}>
                    <span className="truncate max-w-[140px] block">{ua.length > 20 ? ua.substring(0, 20) + '...' : ua}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      </div>
      {/* 列表区域 */}
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载日志数据" />
          </div>
        ) : logs.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            暂无请求日志
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="flex-1 overflow-y-auto">
              <div className="hidden sm:block ">
                <Table>
                  <TableHeader className="z-10 sticky top-0 bg-secondary/90 backdrop-blur text-secondary-foreground">
                    <TableRow className="hover:bg-secondary/90">
                      <TableHead>时间</TableHead>
                      <TableHead>模型名称</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>Tokens</TableHead>
                      <TableHead>耗时</TableHead>
                      <TableHead>提供商模型</TableHead>
                      <TableHead>类型</TableHead>
                      <TableHead>提供商</TableHead>
                      <TableHead>UA</TableHead>
                      <TableHead className="w-[140px]">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {logs.map((log) => (
                      <TableRow key={log.ID}>
                        <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                          {new Date(log.CreatedAt).toLocaleString()}
                        </TableCell>
                        <TableCell className="font-medium">{log.Name}</TableCell>
                        <TableCell>
                          <span className={`inline-flex items-center px-2 py-1 ${
                            log.Status === 'success' ? 'text-green-500' : 'text-red-500 '
                          }`}>
                            {log.Status}
                          </span>
                        </TableCell>
                        <TableCell>{log.total_tokens}</TableCell>
                        <TableCell>{formatTime(log.ChunkTime)}</TableCell>
                        <TableCell className="max-w-[120px] truncate text-xs" title={log.ProviderModel}>{log.ProviderModel}</TableCell>
                        <TableCell className="text-xs">{log.Style}</TableCell>
                        <TableCell className="text-xs">{log.ProviderName}</TableCell>
                        <TableCell className="max-w-[100px] truncate text-xs" title={log.UserAgent}>
                          {log.UserAgent || '-'}
                        </TableCell>
                        <TableCell>
                          <div className="flex gap-2">
                            <Button variant="ghost" size="sm" className="h-8 px-2" onClick={() => openDetailDialog(log)}>
                              详情
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              className="h-8 px-2"
                              onClick={() => handleViewChatIO(log)}
                              disabled={!canViewChatIO(log)}
                            >
                              会话
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <div className="sm:hidden p-4 space-y-4">
                {logs.map((log) => (
                  <div key={log.ID} className="border rounded-lg p-4 bg-card shadow-sm space-y-3">
                    <div className="flex justify-between items-start">
                      <div>
                        <h3 className="font-bold">{log.Name}</h3>
                        <p className="text-xs text-muted-foreground">{new Date(log.CreatedAt).toLocaleString()}</p>
                      </div>
                      <div className="flex gap-1">
                        <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => openDetailDialog(log)}>
                          详情
                        </Button>
                        <Button
                          size="sm"
                          className="h-7 text-xs"
                          onClick={() => handleViewChatIO(log)}
                          disabled={!canViewChatIO(log)}
                        >
                          会话
                        </Button>
                      </div>
                    </div>
                    <div className="grid grid-cols-2 gap-2 text-sm">
                      <span className="text-muted-foreground text-xs">状态:</span>
                      <span className={log.Status === 'success' ? 'text-green-600' : 'text-red-600'}>
                        {log.Status}
                      </span>
                      <span className="text-muted-foreground text-xs">Tokens:</span>
                      <span>{log.total_tokens}</span>
                      <span className="text-muted-foreground text-xs">耗时:</span>
                      <span>{formatTime(log.ChunkTime)}</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
      {/* 分页区域 */}
      {!loading && pages > 1 && (
        <div className="flex flex-col sm:flex-row sm:justify-between sm:items-center gap-4 flex-shrink-0 border-t pt-2">
          <div className="text-sm text-muted-foreground">
            共 {total} 条记录，第 {page} / {pages} 页
          </div>
          <div className="flex space-x-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => handlePageChange(page - 1)}
              disabled={page === 1}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => handlePageChange(page + 1)}
              disabled={page === pages}
            >
              下一页
            </Button>
          </div>
        </div>
      )}
      {/* 详情弹窗 */}
      {selectedLog && (
        <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
          <DialogContent className="p-0 max-w-2xl max-h-[90vh] flex flex-col">
            <div className="p-6 border-b flex-shrink-0">
              <DialogHeader className="p-0">
                <DialogTitle>日志详情</DialogTitle>
              </DialogHeader>
            </div>
            <div className="overflow-y-auto p-6 flex-1">
              <div className="grid gap-3 text-sm">
                <div className="grid grid-cols-4 items-center gap-4">
                  <Label className="text-right text-muted-foreground">ID</Label>
                  <div className="col-span-3 font-mono text-xs select-all">{selectedLog.ID}</div>
                </div>
                <div className="grid grid-cols-4 items-center gap-4">
                  <Label className="text-right text-muted-foreground">时间</Label>
                  <div className="col-span-3">{new Date(selectedLog.CreatedAt).toLocaleString()}</div>
                </div>
                <div className="grid grid-cols-4 items-center gap-4">
                  <Label className="text-right text-muted-foreground">完整状态</Label>
                  <div className="col-span-3">
                    <span className={selectedLog.Status === 'success' ? 'text-green-600' : 'text-red-600'}>
                      {selectedLog.Status}
                    </span>
                  </div>
                </div>
                {selectedLog.Error && (
                  <div className="grid grid-cols-4 items-start gap-4 bg-destructive/10 p-2 rounded">
                    <Label className="text-right text-destructive pt-1">错误信息</Label>
                    <div className="col-span-3 text-destructive whitespace-pre-wrap break-words">
                      {selectedLog.Error}
                    </div>
                  </div>
                )}
                {/* 其他详情字段可按需补充 */}
              </div>
            </div>
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}
