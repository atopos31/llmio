import { useState, useEffect, type ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
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
import { ChevronLeft, ChevronRight, RefreshCw, Trash2, Eye, MessageSquare, Search } from "lucide-react";

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
const getStatusTextClass = (status: string) => {
  switch (status) {
    case "success":
      return "text-green-500";
    case "running":
      return "text-amber-500";
    default:
      return "text-red-500";
  }
};

const getStatusPillClass = (status: string) => {
  switch (status) {
    case "success":
      return "bg-green-100 text-green-700";
    case "running":
      return "bg-amber-100 text-amber-700";
    default:
      return "bg-red-100 text-red-700";
  }
};

const getStatusDetailClass = (status: string) => {
  switch (status) {
    case "success":
      return "text-green-600";
    case "running":
      return "text-amber-600";
    default:
      return "text-red-600";
  }
};

export default function LogsPage() {
  const { t } = useTranslation(['logs', 'common']);
  const [logs, setLogs] = useState<ChatLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(0);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [authKeys, setAuthKeys] = useState<AuthKeyItem[]>([]);
  const [availableStyles, setAvailableStyles] = useState<string[]>([]);
  const navigate = useNavigate();
  // URL 同步的筛选和分页状态（浏览器返回时自动恢复）
  const [searchParams, setSearchParams] = useSearchParams();
  const page = Math.max(1, Number(searchParams.get('page')) || 1);
  const pageSize = Math.max(1, Number(searchParams.get('pageSize')) || 20);
  const providerNameFilter = searchParams.get('providerName') ?? 'all';
  const modelFilter = searchParams.get('model') ?? 'all';
  const statusFilter = searchParams.get('status') ?? 'all';
  const styleFilter = searchParams.get('style') ?? 'all';
  const authKeyFilter = searchParams.get('authKey') ?? 'all';
  const traceIdFilter = searchParams.get('traceId') ?? '';

  const patchParams = (patch: Record<string, string | number>) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev);
      for (const [k, v] of Object.entries(patch)) {
        const s = String(v);
        const isDefault =
          s === '' ||
          (['providerName', 'model', 'status', 'style', 'authKey'].includes(k) && s === 'all') ||
          (k === 'page' && s === '1') ||
          (k === 'pageSize' && s === '20');
        if (isDefault) next.delete(k);
        else next.set(k, s);
      }
      return next;
    }, { replace: true });
  };
  const setProviderNameFilter = (v: string) => patchParams({ providerName: v, page: 1 });
  const setModelFilter = (v: string) => patchParams({ model: v, page: 1 });
  const setStatusFilter = (v: string) => patchParams({ status: v, page: 1 });
  const setStyleFilter = (v: string) => patchParams({ style: v, page: 1 });
  const setAuthKeyFilter = (v: string) => patchParams({ authKey: v, page: 1 });
  const setTraceIdFilter = (v: string) => patchParams({ traceId: v, page: 1 });
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
        authKeyId: authKeyFilter === "all" ? undefined : authKeyFilter,
        traceId: traceIdFilter.trim() || undefined,
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
  }, [page, pageSize, providerNameFilter, modelFilter, statusFilter, styleFilter, authKeyFilter, traceIdFilter]);
  const handlePageChange = (newPage: number) => {
    if (newPage >= 1 && newPage <= pages) patchParams({ page: newPage });
  };
  const handlePageSizeChange = (size: number) => {
    if (size === pageSize) return;
    patchParams({ pageSize: size, page: 1 });
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
      toast.success(t('clean.success', { count: result.deleted_count }));
      fetchLogs();
    } catch (error) {
      console.error("Error cleaning logs:", error);
      toast.error(t('clean.failed'));
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
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-3 min-w-0">
            <h2 className="text-2xl font-bold tracking-tight shrink-0">{t('title')}</h2>
            <div className="relative">
              <Search className="size-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder={t('trace_id_placeholder')}
                value={traceIdFilter}
                onChange={(e) => setTraceIdFilter(e.target.value)}
                className="h-8 text-xs w-44 lg:w-64 pl-7"
              />
            </div>
          </div>
          <div className="flex gap-2">
            <Button
              onClick={() => setIsCleanDialogOpen(true)}
              variant="outline"
              size="icon"
              className="shrink-0"
              aria-label={t('clean.tooltip')}
              title={t('clean.tooltip')}
            >
              <Trash2 className="size-4" />
            </Button>
            <Button
              onClick={handleRefresh}
              variant="outline"
              size="icon"
              className="shrink-0"
              aria-label={t('common:actions.refresh')}
              title={t('common:actions.refresh')}
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
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.model')}</Label>
            <Select value={modelFilter} onValueChange={setModelFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder={t('filters.model_placeholder')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t('common:status.all')}</SelectItem>
                {models.map((model) => (
                  <SelectItem key={model.ID} value={model.Name}>{model.Name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.project')}</Label>
            <Select value={authKeyFilter} onValueChange={setAuthKeyFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder={t('filters.project_placeholder')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t('common:status.all')}</SelectItem>
                {authKeys.map((key) => (
                  <SelectItem key={key.id} value={key.id.toString()}>{key.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.status')}</Label>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder={t('filters.status')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t('common:status.all')}</SelectItem>
                <SelectItem value="success">{t('filters.status_success')}</SelectItem>
                <SelectItem value="running">{t('filters.status_running')}</SelectItem>
                <SelectItem value="error">{t('filters.status_error')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.type')}</Label>
            <Select value={styleFilter} onValueChange={setStyleFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder={t('filters.type')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t('common:status.all')}</SelectItem>
                {availableStyles.map((s) => <SelectItem key={s} value={s}>{s}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="col-span-2 flex flex-col gap-1 text-xs lg:min-w-0 sm:col-span-1">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.provider')}</Label>
            <Select value={providerNameFilter} onValueChange={setProviderNameFilter}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder={t('filters.provider_placeholder')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t('common:status.all')}</SelectItem>
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
            <Loading message={t('loading')} />
          </div>
        ) : logs?.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            {t('no_data')}
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="flex-1 overflow-y-auto">
              <div className="hidden sm:block w-full">
                <Table className="min-w-[1200px]">
                  <TableHeader className="z-10 sticky top-0 bg-secondary/90 backdrop-blur text-secondary-foreground">
                    <TableRow className="hover:bg-secondary/90">
                      <TableHead>{t('table.time')}</TableHead>
                      <TableHead>{t('table.model')}</TableHead>
                      <TableHead>{t('table.project')}</TableHead>
                      <TableHead>{t('table.status')}</TableHead>
                      <TableHead>{t('table.tokens')}</TableHead>
                      <TableHead>{t('table.size')}</TableHead>
                      <TableHead>{t('table.duration')}</TableHead>
                      <TableHead>{t('table.provider_model')}</TableHead>
                      <TableHead>{t('table.type')}</TableHead>
                      <TableHead>{t('table.provider')}</TableHead>
                      <TableHead>{t('table.actions')}</TableHead>
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
                          <span className={`inline-flex items-center px-2 py-1 ${getStatusTextClass(log.Status)}`}>
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
                              <MessageSquare className="h-4 w-4" />
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
                          className={`text-[11px] font-medium px-2 py-0.5 rounded-full ${getStatusPillClass(log.Status)}`}
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
                            <MessageSquare className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </div>
                    </div>
                    <div className="grid grid-cols-2 gap-3 text-xs">
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">{t('mobile.tokens')}</p>
                        <p className="font-medium">{log.total_tokens}</p>
                      </div>
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">{t('mobile.duration')}</p>
                        <p className="font-medium">{formatTime(log.ChunkTime + log.FirstChunkTime + log.ProxyTime)}</p>
                      </div>
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">{t('mobile.provider')}</p>
                        <p className="truncate">{log.ProviderName}</p>
                      </div>
                      <div className="space-y-1">
                        <p className="text-muted-foreground text-[10px] uppercase tracking-wide">{t('mobile.type')}</p>
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
          {t('common:pagination.summary', { total, page, pages })}
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Select value={String(pageSize)} onValueChange={(value) => handlePageSizeChange(Number(value))}>
              <SelectTrigger className="h-8 text-xs">
                <SelectValue placeholder={t('common:pagination.per_page')} />
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
              aria-label={t('common:pagination.prev')}
            >
              <ChevronLeft className="size-4" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              onClick={() => handlePageChange(page + 1)}
              disabled={page === pages}
              aria-label={t('common:pagination.next')}
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
                <DialogTitle>{t('detail.title', { id: selectedLog.ID })}</DialogTitle>
              </DialogHeader>
            </div>
            <div className="overflow-y-auto p-3 flex-1">
              <div className="space-y-6 text-sm">
                <div className="space-y-3">
                  <div className="space-y-2">
                    <div className="text-sm">
                      <span className="text-muted-foreground">{t('detail.created_at')}</span>
                      <span>{new Date(selectedLog.CreatedAt).toLocaleString()}</span>
                    </div>
                    {selectedLog.TraceID && (
                      <div className="text-sm">
                        <span className="text-muted-foreground">{t('detail.trace_id')}</span>
                        <span className="font-mono text-xs break-all">{selectedLog.TraceID}</span>
                      </div>
                    )}
                    <div className="text-sm">
                      <span className="text-muted-foreground">{t('detail.status')}</span>
                      <span className={getStatusDetailClass(selectedLog.Status)}>
                        {selectedLog.Status}
                      </span>
                    </div>
                  </div>
                </div>
                {selectedLog.Error && (
                  <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3">
                    <p className="text-xs text-destructive uppercase tracking-wide mb-1">{t('detail.error_title')}</p>
                    <div className="text-destructive whitespace-pre-wrap break-words text-sm">
                      {selectedLog.Error}
                    </div>
                  </div>
                )}
                <div className="space-y-3">
                  <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{t('detail.basic_info')}</p>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <DetailCard label={t('detail.model_name')} value={selectedLog.Name} />
                    <DetailCard label={t('detail.provider')} value={selectedLog.ProviderName || '-'} />
                    <DetailCard label={t('detail.provider_model')} value={selectedLog.ProviderModel || '-'} mono />
                    <DetailCard label={t('detail.type')} value={selectedLog.Style || '-'} />
                    <DetailCard label={t('detail.size')} value={selectedLog.Size ? formatBytes(selectedLog.Size) : '-'} />
                    <DetailCard label={t('detail.remote_ip')} value={selectedLog.RemoteIP || '-'} mono />
                    <DetailCard label={t('detail.io_log')} value={selectedLog.ChatIO ? t('detail.io_yes') : t('detail.io_no')} />
                    <DetailCard label={t('detail.retry')} value={selectedLog.Retry ?? 0} />
                  </div>
                  <div className="grid grid-cols-1 gap-4">
                    <DetailCard label={t('detail.user_agent')} value={selectedLog.UserAgent || '-'} mono />
                  </div>
                </div>
                <div className="space-y-3">
                  <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{t('detail.performance')}</p>
                  <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                    <DetailCard label={t('detail.proxy_time')} value={formatDurationValue(selectedLog.ProxyTime)} />
                    <DetailCard label={t('detail.first_chunk_time')} value={formatDurationValue(selectedLog.FirstChunkTime)} />
                    <DetailCard label={t('detail.chunk_time')} value={formatDurationValue(selectedLog.ChunkTime)} />
                    <DetailCard label={t('detail.tps')} value={formatTpsValue(selectedLog.Tps)} />
                  </div>
                </div>
                <div className="space-y-3">
                  <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{t('detail.token_usage')}</p>
                  <div className="grid grid-cols-1 sm:grid-cols-4 gap-4">
                    <DetailCard label={t('detail.input')} value={formatTokenValue(selectedLog.prompt_tokens)} />
                    <DetailCard label={t('detail.output')} value={formatTokenValue(selectedLog.completion_tokens)} />
                    <DetailCard label={t('detail.total')} value={formatTokenValue(selectedLog.total_tokens)} />
                    <DetailCard label={t('detail.cached')} value={formatTokenValue(selectedLog.prompt_tokens_details.cached_tokens)} />
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
            <DialogTitle>{t('clean.title')}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="flex gap-2">
              <Button
                variant={cleanType === 'count' ? 'default' : 'outline'}
                size="sm"
                onClick={() => handleCleanTypeChange('count')}
                className="flex-1"
              >
                {t('clean.by_count')}
              </Button>
              <Button
                variant={cleanType === 'days' ? 'default' : 'outline'}
                size="sm"
                onClick={() => handleCleanTypeChange('days')}
                className="flex-1"
              >
                {t('clean.by_days')}
              </Button>
            </div>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min="1"
                value={cleanValue}
                onChange={(e) => setCleanValue(e.target.value)}
                placeholder={cleanType === 'count' ? t('clean.count_placeholder') : t('clean.days_placeholder')}
                className="h-10"
              />
              <span className="text-sm text-muted-foreground whitespace-nowrap">
                {cleanType === 'count' ? t('clean.count_unit') : t('clean.days_unit')}
              </span>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setIsCleanDialogOpen(false)}>
              {t('clean.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleCleanLogs}
              disabled={cleanLoading || !cleanValue || parseInt(cleanValue) <= 0}
            >
              {cleanLoading ? t('clean.confirming') : t('clean.confirm')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
