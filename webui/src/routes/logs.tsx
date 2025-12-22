import { useState, useEffect, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
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
import { Input } from "@/components/ui/input";
import Loading from "@/components/loading";
import { getLogs, getProviders, getModelOptions, getAuthKeysList, type ChatLog, type Provider, type Model, type AuthKeyItem, getProviderTemplates, cleanLogs } from "@/lib/api";
import { ChevronLeft, ChevronRight, RefreshCw, Trash2, Eye, EyeOff } from "lucide-react";

// 格式化时间显示
const formatTime = (nanoseconds: number): string => {
  if (nanoseconds < 1000) return `${nanoseconds.toFixed(2)} ns`;
  if (nanoseconds < 1000000) return `${(nanoseconds / 1000).toFixed(2)} μs`;
  if (nanoseconds < 1000000000) return `${(nanoseconds / 1000000).toFixed(2)} ms`;
  return `${(nanoseconds / 1000000000).toFixed(2)} s`;
};

// 格式化字节大小显示
const formatBytes = (bytes: number): string => {
  if (bytes === 0) return '0 B';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(2)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
};

type DetailCardProps = {
  label: string;
  value: ReactNode;
  mono?: boolean;
};

const DetailCard = ({ label, value, mono = false }: DetailCardProps) => (
  <div className="rounded-md border bg-muted/20 p-3 space-y-1">
    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{label}</p>
    <div className={`text-sm break-words ${mono ? 'font-mono text-xs' : ''}`}>
      {value ?? '-'}
    </div>
  </div>
);

const formatDurationValue = (value?: number) => (typeof value === "number" ? formatTime(value) : "-");
const formatTokenValue = (value?: number) => (typeof value === "number" ? value.toLocaleString() : "-");
const formatTpsValue = (value?: number) => (typeof value === "number" ? value.toFixed(2) : "-");

export default function LogsPage() {
  const [logs, setLogs] = useState<ChatLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(0);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [authKeys, setAuthKeys] = useState<AuthKeyItem[]>([]);
  // 筛选条件
  const [providerNameFilter, setProviderNameFilter] = useState<string>("all");
  const [modelFilter, setModelFilter] = useState<string>("all");
  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [styleFilter, setStyleFilter] = useState<string>("all");
  const [authKeyFilter, setAuthKeyFilter] = useState<string>("all");
  const [availableStyles, setAvailableStyles] = useState<string[]>([]);
  const navigate = useNavigate();
  // 详情弹窗
  const [selectedLog, setSelectedLog] = useState<ChatLog | null>(null);
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  // 清理弹窗
  const [cleanType, setCleanType] = useState<'count' | 'days'>('count');
  const [cleanValue, setCleanValue] = useState<string>('1000');
  const [isCleanDialogOpen, setIsCleanDialogOpen] = useState(false);
  const [cleanLoading, setCleanLoading] = useState(false);
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
      const modelList = await getModelOptions();
      setModels(modelList);
    } catch (error) {
      console.error("Error fetching models:", error);
    }
  };
  const fetchAuthKeys = async () => {
    try {
      const authKeyList = await getAuthKeysList();
      setAuthKeys(authKeyList);
    } catch (error) {
      console.error("Error fetching auth keys:", error);
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
        authKeyId: authKeyFilter === "all" ? undefined : authKeyFilter
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
    fetchAuthKeys();
    fetchLogs();
  }, [page, pageSize, providerNameFilter, modelFilter, statusFilter, styleFilter, authKeyFilter]);
  const handleFilterChange = () => {
    setPage(1);
  };
  useEffect(() => {
    handleFilterChange();
  }, [providerNameFilter, modelFilter, statusFilter, styleFilter, authKeyFilter]);
  const handlePageChange = (newPage: number) => {
    if (newPage >= 1 && newPage <= pages) setPage(newPage);
  };
  const handlePageSizeChange = (size: number) => {
    if (size === pageSize) return;
    setPage(1);
    setPageSize(size);
  };
  const handleRefresh = () => {
    fetchLogs();
  };
  const handleCleanTypeChange = (type: 'count' | 'days') => {
    setCleanType(type);
    setCleanValue(type === 'count' ? '1000' : '30');
  };
  const handleCleanLogs = async () => {
    const value = parseInt(cleanValue);
    if (isNaN(value) || value <= 0) return;

    setCleanLoading(true);
    try {
      const result = await cleanLogs({ type: cleanType, value });
      toast.success(`已清理 ${result.deleted_count} 条日志`);
      fetchLogs();
    } catch (error) {
      console.error("Error cleaning logs:", error);
      toast.error('清理失败');
    } finally {
      setCleanLoading(false);
      setIsCleanDialogOpen(false);
    }
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
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      {/* 顶部标题和刷新 */}
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">请求日志</h2>
          </div>
          <div className="flex gap-2">
            <Button
              onClick={() => setIsCleanDialogOpen(true)}
              variant="outline"
              size="icon"
              className="shrink-0"
              aria-label="清理日志"
              title="清理日志"
            >
              <Trash2 className="size-4" />
            </Button>
            <Button
              onClick={handleRefresh}
              variant="outline"
              size="icon"
              className="shrink-0"
              aria-label="刷新列表"
              title="刷新列表"
            >
              <RefreshCw className="size-4" />
            </Button>
          </div>
        </div>
      </div>
      {/* 筛选区域 */}
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5 lg:gap-4">
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">模型名称</Label>
            <Select value={modelFilter} onValueChange={setModelFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
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
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">项目</Label>
            <Select value={authKeyFilter} onValueChange={setAuthKeyFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder="选择项目" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {authKeys.map((key) => (
                  <SelectItem key={key.id} value={key.id.toString()}>{key.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">状态</Label>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder="状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                <SelectItem value="success">成功</SelectItem>
                <SelectItem value="error">错误</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">类型</Label>
            <Select value={styleFilter} onValueChange={setStyleFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder="类型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {availableStyles.map((s) => <SelectItem key={s} value={s}>{s}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="col-span-2 flex flex-col gap-1 text-xs lg:min-w-0 sm:col-span-1">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">提供商</Label>
            <Select value={providerNameFilter} onValueChange={setProviderNameFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
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
        </div>
      </div>
      {/* 列表区域 */}
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载日志数据" />
          </div>
        ) : logs?.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            暂无请求日志
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="flex-1 overflow-y-auto">
              <div className="hidden sm:block w-full">
                <Table className="min-w-[1200px]">
                  <TableHeader className="z-10 sticky top-0 bg-secondary/90 backdrop-blur text-secondary-foreground">
                    <TableRow className="hover:bg-secondary/90">
                      <TableHead>时间</TableHead>
                      <TableHead>模型</TableHead>
                      <TableHead>项目</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>Tokens</TableHead>
                      <TableHead>响应大小</TableHead>
                      <TableHead>耗时</TableHead>
                      <TableHead>提供商模型</TableHead>
                      <TableHead>类型</TableHead>
                      <TableHead>提供商</TableHead>
                      <TableHead>操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {logs?.map((log) => (
                      <TableRow key={log.ID}>
                        <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                          {new Date(log.CreatedAt).toLocaleString()}
                        </TableCell>
                        <TableCell className="font-medium">{log.Name}</TableCell>
                        <TableCell className="text-xs">{log.key_name || '-'}</TableCell>
                        <TableCell>
                          <span className={`inline-flex items-center px-2 py-1 ${log.Status === 'success' ? 'text-green-500' : 'text-red-500 '
                            }`}>
                            {log.Status}
                          </span>
                        </TableCell>
                        <TableCell>{log.total_tokens}</TableCell>
                        <TableCell className="text-xs">
                          {log.Size ? formatBytes(log.Size) : '-'}
                        </TableCell>
                        <TableCell>{formatTime(log.ChunkTime + log.FirstChunkTime + log.ProxyTime)}</TableCell>
                        <TableCell className="max-w-[120px] truncate text-xs" title={log.ProviderModel}>{log.ProviderModel}</TableCell>
                        <TableCell className="text-xs">{log.Style}</TableCell>
                        <TableCell className="text-xs">{log.ProviderName}</TableCell>
                        <TableCell>
                          <div className="flex gap-2">
                            <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => openDetailDialog(log)}>
                              <Eye className="h-4 w-4" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-8 w-8"
                              onClick={() => handleViewChatIO(log)}
                              disabled={!canViewChatIO(log)}
                            >
                              <EyeOff className="h-4 w-4" />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <div className="sm:hidden px-2 py-3 divide-y divide-border">
                {logs?.map((log) => (
                  <div key={log.ID} className="py-3 space-y-2 my-1 px-1">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0 flex-1">
                        <h3 className="font-semibold text-sm truncate">{log.Name}</h3>
                        <p className="text-[11px] text-muted-foreground">{new Date(log.CreatedAt).toLocaleString()}</p>
                      </div>
                      <div className="flex items-center gap-2">
                        <span
                          className={`text-[11px] font-medium px-2 py-0.5 rounded-full ${log.Status === 'success' ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'
                            }`}
                        >
                          {log.Status}
                        </span>
                        <div className="flex gap-1.5">
                          <Button variant="outline" size="icon" className="h-7 w-7" onClick={() => openDetailDialog(log)}>
                            <Eye className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            size="icon"
                            className="h-7 w-7"
                            onClick={() => handleViewChatIO(log)}
                            disabled={!canViewChatIO(log)}
                          >
                            <EyeOff className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </div>
                    </div>
                    <div className="grid grid-cols-2 gap-3 text-xs">
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">Tokens</p>
                        <p className="font-medium">{log.total_tokens}</p>
                      </div>
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">耗时</p>
                        <p className="font-medium">{formatTime(log.ChunkTime)}</p>
                      </div>
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">提供商</p>
                        <p className="truncate">{log.ProviderName}</p>
                      </div>
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">类型</p>
                        <p>{log.Style || '-'}</p>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
      {/* 分页区域 */}

      <div className="flex flex-wrap items-center justify-between gap-3 flex-shrink-0 border-t pt-2">
        <div className="text-sm text-muted-foreground whitespace-nowrap">
          共 {total} 条，第 {page} / {pages} 页
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Select value={String(pageSize)} onValueChange={(value) => handlePageSizeChange(Number(value))}>
              <SelectTrigger className="h-8 text-xs">
                <SelectValue placeholder="条数" />
              </SelectTrigger>
              <SelectContent>
                {[10, 20, 50].map((size) => (
                  <SelectItem key={size} value={String(size)}>
                    {size}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="icon"
              onClick={() => handlePageChange(page - 1)}
              disabled={page === 1}
              aria-label="上一页"
            >
              <ChevronLeft className="size-4" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              onClick={() => handlePageChange(page + 1)}
              disabled={page === pages}
              aria-label="下一页"
            >
              <ChevronRight className="size-4" />
            </Button>
          </div>
        </div>
      </div>
      {/* 详情弹窗 */}
      {selectedLog && (
        <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
          <DialogContent className="p-0 w-[92vw] sm:w-auto sm:max-w-2xl max-h-[95vh] flex flex-col">
            <div className="p-4 border-b flex-shrink-0">
              <DialogHeader className="p-0">
                <DialogTitle>日志详情: {selectedLog.ID}</DialogTitle>
              </DialogHeader>
            </div>
            <div className="overflow-y-auto p-3 flex-1">
              <div className="space-y-6 text-sm">
                <div className="space-y-3">
                  <div className="space-y-2">
                    <div className="text-sm">
                      <span className="text-muted-foreground">创建时间：</span>
                      <span>{new Date(selectedLog.CreatedAt).toLocaleString()}</span>
                    </div>
                    <div className="text-sm">
                      <span className="text-muted-foreground">状态：</span>
                      <span className={selectedLog.Status === 'success' ? 'text-green-600' : 'text-red-600'}>
                        {selectedLog.Status}
                      </span>
                    </div>
                  </div>
                </div>
                {selectedLog.Error && (
                  <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3">
                    <p className="text-xs text-destructive uppercase tracking-wide mb-1">错误信息</p>
                    <div className="text-destructive whitespace-pre-wrap break-words text-sm">
                      {selectedLog.Error}
                    </div>
                  </div>
                )}
                <div className="space-y-3">
                  <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">基本信息</p>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <DetailCard label="模型名称" value={selectedLog.Name} />
                    <DetailCard label="提供商" value={selectedLog.ProviderName || '-'} />
                    <DetailCard label="提供商模型" value={selectedLog.ProviderModel || '-'} mono />
                    <DetailCard label="类型" value={selectedLog.Style || '-'} />
                    <DetailCard label="响应大小" value={selectedLog.Size ? formatBytes(selectedLog.Size) : '-'} />
                    <DetailCard label="远端 IP" value={selectedLog.RemoteIP || '-'} mono />
                    <DetailCard label="记录 IO" value={selectedLog.ChatIO ? '是' : '否'} />
                    <DetailCard label="重试次数" value={selectedLog.Retry ?? 0} />
                  </div>
                  <div className="grid grid-cols-1 gap-4">
                    <DetailCard label="用户代理" value={selectedLog.UserAgent || '-'} mono />
                  </div>
                </div>
                <div className="space-y-3">
                  <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">性能指标</p>
                  <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                    <DetailCard label="代理耗时" value={formatDurationValue(selectedLog.ProxyTime)} />
                    <DetailCard label="首包耗时" value={formatDurationValue(selectedLog.FirstChunkTime)} />
                    <DetailCard label="完成耗时" value={formatDurationValue(selectedLog.ChunkTime)} />
                    <DetailCard label="TPS" value={formatTpsValue(selectedLog.Tps)} />
                  </div>
                </div>
                <div className="space-y-3">
                  <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Token 使用</p>
                  <div className="grid grid-cols-1 sm:grid-cols-4 gap-4">
                    <DetailCard label="输入" value={formatTokenValue(selectedLog.prompt_tokens)} />
                    <DetailCard label="输出" value={formatTokenValue(selectedLog.completion_tokens)} />
                    <DetailCard label="总计" value={formatTokenValue(selectedLog.total_tokens)} />
                    <DetailCard label="缓存" value={formatTokenValue(selectedLog.prompt_tokens_details.cached_tokens)} />
                  </div>
                </div>
              </div>
            </div>
          </DialogContent>
        </Dialog>
      )}
      {/* 清理日志弹窗 */}
      <Dialog open={isCleanDialogOpen} onOpenChange={setIsCleanDialogOpen}>
        <DialogContent className="w-[92vw] sm:max-w-md">
          <DialogHeader>
            <DialogTitle>清理日志</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="flex gap-2">
              <Button
                variant={cleanType === 'count' ? 'default' : 'outline'}
                size="sm"
                onClick={() => handleCleanTypeChange('count')}
                className="flex-1"
              >
                保留条数
              </Button>
              <Button
                variant={cleanType === 'days' ? 'default' : 'outline'}
                size="sm"
                onClick={() => handleCleanTypeChange('days')}
                className="flex-1"
              >
                保留天数
              </Button>
            </div>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min="1"
                value={cleanValue}
                onChange={(e) => setCleanValue(e.target.value)}
                placeholder={cleanType === 'count' ? '输入保留条数' : '输入保留天数'}
                className="h-10"
              />
              <span className="text-sm text-muted-foreground whitespace-nowrap">
                {cleanType === 'count' ? '条' : '天'}
              </span>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setIsCleanDialogOpen(false)}>
              取消
            </Button>
            <Button
              variant="destructive"
              onClick={handleCleanLogs}
              disabled={cleanLoading || !cleanValue || parseInt(cleanValue) <= 0}
            >
              {cleanLoading ? '清理中...' : '确定清理'}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
