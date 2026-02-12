import { useState, useEffect, useCallback, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from "@/components/ui/table";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import Loading from "@/components/loading";
import {
  getModelProviders,
  getModelProviderStatus,
  updateModelProviderStatus,
  deleteModelProvider,
  getModelOptions,
  getProviders,
  getProviderModels
} from "@/lib/api";
import type { ModelWithProvider, Model, Provider, ProviderModel } from "@/lib/api";
import { toast } from "sonner";
import { RefreshCw, Pencil, Trash2, Zap } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { useModelProviderForm } from "@/routes/model-providers/use-model-provider-form";
import { useModelProviderTesting } from "@/routes/model-providers/use-model-provider-testing";
import { ModelProviderFormDialog } from "@/routes/model-providers/model-provider-form-dialog";
import { ModelProviderTestDialog } from "@/routes/model-providers/model-provider-test-dialog";

type MobileInfoItemProps = {
  label: string;
  value: ReactNode;
};

const MobileInfoItem = ({ label, value }: MobileInfoItemProps) => (
  <div className="space-y-1">
    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{label}</p>
    <div className="text-sm font-medium break-words">{value}</div>
  </div>
);

export default function ModelProvidersPage() {
  const [modelProviders, setModelProviders] = useState<ModelWithProvider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerModelsMap, setProviderModelsMap] = useState<Record<number, ProviderModel[]>>({});
  const [providerModelsLoading, setProviderModelsLoading] = useState<Record<number, boolean>>({});
  const [searchParams, setSearchParams] = useSearchParams();
  const [providerStatus, setProviderStatus] = useState<Record<number, boolean[]>>({});
  const [loading, setLoading] = useState(true);
  const [selectedModelId, setSelectedModelId] = useState<number | null>(null);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [selectedProviderType, setSelectedProviderType] = useState<string>("all");
  const [weightSortOrder, setWeightSortOrder] = useState<"asc" | "desc" | "none">("none");
  const [statusUpdating, setStatusUpdating] = useState<Record<number, boolean>>({});
  const [statusError, setStatusError] = useState<string | null>(null);

  const loadProviderModels = useCallback(async (providerId: number, force = false) => {
    if (!providerId) return;
    if (!force && providerModelsMap[providerId]) return;

    setProviderModelsLoading((prev) => ({ ...prev, [providerId]: true }));
    try {
      const data = await getProviderModels(providerId);
      setProviderModelsMap((prev) => ({ ...prev, [providerId]: data }));
    } catch (err) {
      toast.warning(`获取提供商: ${providers.find((e) => e.ID === providerId)?.Name} 模型列表失败, 请手动填写提供商模型\n${err}`);
      setProviderModelsMap((prev) => ({ ...prev, [providerId]: [] }));
    } finally {
      setProviderModelsLoading((prev) => {
        const next = { ...prev };
        delete next[providerId];
        return next;
      });
    }
  }, [providerModelsMap, providers]);

  const {
    testResults,
    testDialogOpen,
    setTestDialogOpen,
    selectedTestId,
    testType,
    setTestType,
    reactTestResult,
    openTestDialog,
    closeTestDialog,
    executeTest,
  } = useModelProviderTesting();

  const fetchModels = async () => {
    try {
      const data = await getModelOptions();
      setModels(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取模型列表失败: ${message}`);
      console.error(err);
    }
  };

  const fetchProviders = async () => {
    try {
      const data = await getProviders();
      setProviders(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取提供商列表失败: ${message}`);
      console.error(err);
    }
  };

  const fetchModelProviders = async (modelId: number) => {
    try {
      setLoading(true);
      const data = await getModelProviders(modelId);
      setModelProviders(data.map(item => ({
        ...item,
        CustomerHeaders: item.CustomerHeaders || {}
      })));
      // 异步加载状态数据
      loadProviderStatus(data, modelId);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取关联管理列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const loadProviderStatus = async (providers: ModelWithProvider[], modelId: number) => {
    const selectedModel = models.find(m => m.ID === modelId);
    if (!selectedModel) return;
    setProviderStatus({})

    const newStatus: Record<number, boolean[]> = {};

    // 并行加载所有状态数据
    await Promise.all(
      providers.map(async (provider) => {
        try {
          const status = await getModelProviderStatus(
            provider.ProviderID,
            selectedModel.Name,
            provider.ProviderModel
          );
          newStatus[provider.ID] = status;
        } catch (error) {
          console.error(`Failed to load status for provider ${provider.ID}:`, error);
          newStatus[provider.ID] = [];
        }
      })
    );

    setProviderStatus(newStatus);
  };

  const {
    form,
    open,
    setOpen,
    editingAssociation,
    showProviderModels,
    setShowProviderModels,
    headerFields,
    appendHeader,
    removeHeader,
    selectedProviderId,
    openEditDialog,
    openCreateDialog,
    submit,
    sortProviderModels,
  } = useModelProviderForm({
    selectedModelId,
    models,
    providerModelsMap,
    loadProviderModels,
    onReload: async () => {
      if (selectedModelId) {
        await fetchModelProviders(selectedModelId);
      }
    },
  });

  useEffect(() => {
    Promise.all([fetchModels(), fetchProviders()]).finally(() => {
      setLoading(false);
    });
  }, []);

  useEffect(() => {
    if (models.length === 0) {
      if (selectedModelId !== null) {
        setSelectedModelId(null);
        form.setValue("model_id", 0);
      }
      return;
    }

    const modelIdParam = searchParams.get("modelId");
    const parsedParam = modelIdParam ? Number(modelIdParam) : NaN;

    if (!Number.isNaN(parsedParam) && models.some((model) => model.ID === parsedParam)) {
      if (selectedModelId !== parsedParam) {
        setSelectedModelId(parsedParam);
        form.setValue("model_id", parsedParam);
      }
      return;
    }

    const fallbackId = models[0].ID;
    if (selectedModelId !== fallbackId) {
      setSelectedModelId(fallbackId);
      form.setValue("model_id", fallbackId);
    }
    if (modelIdParam !== fallbackId.toString()) {
      const nextParams = new URLSearchParams(searchParams);
      nextParams.set("modelId", fallbackId.toString());
      setSearchParams(nextParams, { replace: true });
    }
  }, [models, searchParams, form, selectedModelId, setSearchParams]);

  useEffect(() => {
    if (selectedModelId) {
      fetchModelProviders(selectedModelId);
    }
  }, [selectedModelId]);

  const handleDelete = async () => {
    if (!deleteId) return;
    try {
      await deleteModelProvider(deleteId);
      setDeleteId(null);
      if (selectedModelId) {
        fetchModelProviders(selectedModelId);
      }
      toast.success("关联管理删除成功");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除关联管理失败: ${message}`);
      console.error(err);
    }
  };

  const handleStatusToggle = async (association: ModelWithProvider, nextStatus: boolean) => {
    const previousStatus = association.Status ?? true;
    setStatusError(null);
    setStatusUpdating(prev => ({ ...prev, [association.ID]: true }));
    setModelProviders(prev =>
      prev.map(item =>
        item.ID === association.ID ? { ...item, Status: nextStatus } : item
      )
    );

    try {
      const updated = await updateModelProviderStatus(association.ID, nextStatus);
      const normalized = { ...updated, CustomerHeaders: updated.CustomerHeaders || {} };
      setModelProviders(prev =>
        prev.map(item =>
          item.ID === association.ID ? normalized : item
        )
      );
    } catch (err) {
      setModelProviders(prev =>
        prev.map(item =>
          item.ID === association.ID ? { ...item, Status: previousStatus } : item
        )
      );
      setStatusError("更新启用状态失败");
      console.error(err);
    } finally {
      setStatusUpdating(prev => {
        const next = { ...prev };
        delete next[association.ID];
        return next;
      });
    }
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const handleModelChange = (modelId: string) => {
    const id = parseInt(modelId);
    setSelectedModelId(id);
    const nextParams = new URLSearchParams(searchParams);
    nextParams.set("modelId", id.toString());
    setSearchParams(nextParams);
    form.setValue("model_id", id);
  };

  // 获取唯一的提供商类型列表
  const providerTypes = Array.from(new Set(providers.map(p => p.Type).filter(Boolean)));

  // 根据选择的提供商类型过滤关联管理，并按权重排序
  const filteredModelProviders = selectedProviderType && selectedProviderType !== "all"
    ? modelProviders.filter(association => {
      const provider = providers.find(p => p.ID === association.ProviderID);
      return provider?.Type === selectedProviderType;
    })
    : modelProviders;

  // 按权重排序
  const sortedModelProviders = [...filteredModelProviders].sort((a, b) => {
    if (weightSortOrder === "none") return 0;
    return weightSortOrder === "asc" ? a.Weight - b.Weight : b.Weight - a.Weight;
  });

  const hasAssociationFilter = selectedProviderType !== "all";

  if (loading && models.length === 0 && providers.length === 0) return <Loading message="加载模型和提供商" />;

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">关联管理</h2>
          </div>
        </div>
        <div className="flex flex-col gap-2 flex-shrink-0">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-2 lg:grid-cols-3 lg:gap-4">
            <div className="flex flex-col gap-1 text-xs">
              <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">关联模型</Label>
              <Select value={selectedModelId?.toString() || ""} onValueChange={handleModelChange}>
                <SelectTrigger className="h-8 w-full text-xs px-2">
                  <SelectValue placeholder="选择模型" />
                </SelectTrigger>
                <SelectContent>
                  {models.map((model) => (
                    <SelectItem key={model.ID} value={model.ID.toString()}>
                      {model.Name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1 text-xs">
              <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">提供商类型</Label>
              <Select value={selectedProviderType} onValueChange={setSelectedProviderType}>
                <SelectTrigger className="h-8 w-full text-xs px-2">
                  <SelectValue placeholder="按类型筛选" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部</SelectItem>
                  {providerTypes.map((type) => (
                    <SelectItem key={type} value={type}>
                      {type}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-end col-span-2 sm:col-span-2 lg:col-span-1 gap-2">
              <div className="flex-1">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">权重排序</Label>
                <Select value={weightSortOrder} onValueChange={(value) => setWeightSortOrder(value as "asc" | "desc" | "none")}>
                  <SelectTrigger className="h-8 w-full text-xs px-2">
                    <SelectValue placeholder="选择排序方式" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">默认顺序</SelectItem>
                    <SelectItem value="asc">权重升序</SelectItem>
                    <SelectItem value="desc">权重降序</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <Button
                onClick={openCreateDialog}
                disabled={!selectedModelId}
                className="h-8 text-xs"
              >
                添加关联
              </Button>
            </div>
          </div>
        </div>
      </div>

      {statusError && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {statusError}
        </div>
      )}
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载关联数据" />
          </div>
        ) : !selectedModelId ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            请选择一个模型来查看其提供商关联
          </div>
        ) : sortedModelProviders.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground text-sm text-center px-6">
            {hasAssociationFilter ? '当前类型暂无关联' : '该模型还没有关联的提供商'}
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="hidden sm:block flex-1 overflow-y-auto">
              <div className="w-full">
                <Table className="min-w-[1200px]">
                  <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                    <TableRow>
                      <TableHead>ID</TableHead>
                      <TableHead>提供商模型</TableHead>
                      <TableHead>类型</TableHead>
                      <TableHead>提供商</TableHead>
                      <TableHead>工具调用</TableHead>
                      <TableHead>结构化输出</TableHead>
                      <TableHead>视觉</TableHead>
                      <TableHead>请求头透传</TableHead>
                      <TableHead>权重</TableHead>
                      <TableHead>启用</TableHead>
                      <TableHead>
                        <div className="flex items-center gap-1">状态
                          <Button
                            onClick={() => loadProviderStatus(modelProviders, selectedModelId)}
                            variant="ghost"
                            size="icon"
                            aria-label="刷新状态"
                            title="刷新状态"
                            className="rounded-full"
                          >
                            <RefreshCw className="size-4" />
                          </Button>
                        </div>
                      </TableHead>
                      <TableHead>操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {sortedModelProviders.map((association) => {
                      const provider = providers.find(p => p.ID === association.ProviderID);
                      const isAssociationEnabled = association.Status ?? false;
                      const statusBars = providerStatus[association.ID];
                      return (
                        <TableRow key={association.ID}>
                          <TableCell className="font-mono text-xs text-muted-foreground">{association.ID}</TableCell>
                          <TableCell className="max-w-[200px] truncate" title={association.ProviderModel}>
                            {association.ProviderModel}
                          </TableCell>
                          <TableCell>{provider?.Type ?? '未知'}</TableCell>
                          <TableCell>{provider?.Name ?? '未知'}</TableCell>
                          <TableCell>
                            <span className={association.ToolCall ? "text-green-600" : "text-red-600"}>
                              {association.ToolCall ? '✓' : '✗'}
                            </span>
                          </TableCell>
                          <TableCell>
                            <span className={association.StructuredOutput ? "text-green-600" : "text-red-600"}>
                              {association.StructuredOutput ? '✓' : '✗'}
                            </span>
                          </TableCell>
                          <TableCell>
                            <span className={association.Image ? "text-green-600" : "text-red-600"}>
                              {association.Image ? '✓' : '✗'}
                            </span>
                          </TableCell>
                          <TableCell>
                            <span className={association.WithHeader ? "text-green-600" : "text-red-600"}>
                              {association.WithHeader ? '✓' : '✗'}
                            </span>
                          </TableCell>
                          <TableCell>{association.Weight}</TableCell>
                          <TableCell>
                            <div className="flex items-center gap-2">
                              <Switch
                                checked={isAssociationEnabled}
                                disabled={!!statusUpdating[association.ID]}
                                onCheckedChange={(value) => handleStatusToggle(association, value)}
                                aria-label="切换启用状态"
                              />
                              <span className="text-xs text-muted-foreground">
                                {isAssociationEnabled ? '已启用' : '已停用'}
                              </span>
                            </div>
                          </TableCell>
                          <TableCell>
                            <div className="flex items-center space-x-4 w-20">
                              {statusBars ? (
                                statusBars.length > 0 ? (
                                  <div className="flex space-x-1 items-end h-6">
                                    {statusBars.map((isSuccess, index) => (
                                      <div
                                        key={index}
                                        className={`w-1 h-6 ${isSuccess ? 'bg-green-500' : 'bg-red-500'}`}
                                        title={isSuccess ? '成功' : '失败'}
                                      />
                                    ))}
                                  </div>
                                ) : (
                                  <div className="text-xs text-gray-400">无数据</div>
                                )
                              ) : (
                                <Spinner />
                              )}
                            </div>
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-wrap gap-2">
                              <Button variant="outline" size="icon" onClick={() => openEditDialog(association)}>
                                <Pencil className="h-4 w-4" />
                              </Button>
                              <Button variant="outline" size="icon" onClick={() => openTestDialog(association.ID)}>
                                <Zap className="h-4 w-4" />
                              </Button>
                              <AlertDialog open={deleteId === association.ID} onOpenChange={(open) => !open && setDeleteId(null)}>
                                <AlertDialogTrigger asChild>
                                  <Button variant="destructive" size="icon" onClick={() => openDeleteDialog(association.ID)}>
                                    <Trash2 className="h-4 w-4" />
                                  </Button>
                                </AlertDialogTrigger>
                                <AlertDialogContent>
                                  <AlertDialogHeader>
                                    <AlertDialogTitle>确定要删除这个关联吗？</AlertDialogTitle>
                                    <AlertDialogDescription>
                                      此操作无法撤销。这将永久删除该关联管理。
                                    </AlertDialogDescription>
                                  </AlertDialogHeader>
                                  <AlertDialogFooter>
                                    <AlertDialogCancel onClick={() => setDeleteId(null)}>取消</AlertDialogCancel>
                                    <AlertDialogAction onClick={handleDelete}>确认删除</AlertDialogAction>
                                  </AlertDialogFooter>
                                </AlertDialogContent>
                              </AlertDialog>
                            </div>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>
            </div>
            <div className="sm:hidden flex-1 min-h-0 overflow-y-auto px-2 py-3 divide-y divide-border">
              {sortedModelProviders.map((association) => {
                const provider = providers.find(p => p.ID === association.ProviderID);
                const isAssociationEnabled = association.Status ?? true;
                const statusBars = providerStatus[association.ID];
                return (
                  <div key={association.ID} className="py-3 space-y-3">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0 flex-1">
                        <h3 className="font-semibold text-sm truncate">{provider?.Name ?? '未知提供商'}</h3>
                        <p className="text-[11px] text-muted-foreground">提供商模型: {association.ProviderModel}</p>
                      </div>
                      <span
                        className={`text-[11px] font-medium px-2 py-0.5 rounded-full ${isAssociationEnabled ? 'bg-emerald-100 text-emerald-700' : 'bg-red-100 text-red-700'}`}
                      >
                        {isAssociationEnabled ? '已启用' : '已停用'}
                      </span>
                    </div>
                    <div className="grid grid-cols-2 gap-3 text-xs">
                      <MobileInfoItem label="提供商类型" value={provider?.Type ?? '未知'} />
                      <MobileInfoItem label="提供商 ID" value={<span className="font-mono text-xs">{provider?.ID ?? '-'}</span>} />
                      <MobileInfoItem label="权重" value={association.Weight} />
                      <MobileInfoItem
                        label="请求头透传"
                        value={<span className={association.WithHeader ? "text-green-600" : "text-red-600"}>{association.WithHeader ? '✓' : '✗'}</span>}
                      />
                    </div>
                    <div className="grid grid-cols-2 gap-3 text-xs">
                      <MobileInfoItem
                        label="工具调用"
                        value={<span className={association.ToolCall ? "text-green-600" : "text-red-600"}>{association.ToolCall ? '✓' : '✗'}</span>}
                      />
                      <MobileInfoItem
                        label="结构化输出"
                        value={<span className={association.StructuredOutput ? "text-green-600" : "text-red-600"}>{association.StructuredOutput ? '✓' : '✗'}</span>}
                      />
                      <MobileInfoItem
                        label="视觉能力"
                        value={<span className={association.Image ? "text-green-600" : "text-red-600"}>{association.Image ? '✓' : '✗'}</span>}
                      />
                      <MobileInfoItem
                        label="最近状态"
                        value={
                          <div className="flex items-center gap-1">
                            {statusBars ? (
                              statusBars.length > 0 ? (
                                statusBars.map((isSuccess, index) => (
                                  <div
                                    key={index}
                                    className={`w-1 h-4 rounded ${isSuccess ? 'bg-green-500' : 'bg-red-500'}`}
                                  />
                                ))
                              ) : (
                                <span className="text-muted-foreground text-[11px]">无数据</span>
                              )
                            ) : (
                              <Spinner />
                            )}
                          </div>
                        }
                      />
                    </div>
                    <div className="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2">
                      <p className="text-xs text-muted-foreground">启用状态</p>
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">{isAssociationEnabled ? "启用" : "停用"}</span>
                        <Switch
                          checked={isAssociationEnabled}
                          disabled={!!statusUpdating[association.ID]}
                          onCheckedChange={(value) => handleStatusToggle(association, value)}
                          aria-label="切换启用状态"
                        />
                      </div>
                    </div>
                    <div className="flex flex-wrap justify-end gap-1.5">
                      <Button
                        variant="outline"
                        size="icon"
                        className="h-7 w-7"
                        onClick={() => openEditDialog(association)}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="outline"
                        size="icon"
                        className="h-7 w-7"
                        onClick={() => openTestDialog(association.ID)}
                      >
                        <Zap className="h-3.5 w-3.5" />
                      </Button>
                      <AlertDialog open={deleteId === association.ID} onOpenChange={(open) => !open && setDeleteId(null)}>
                        <Button
                          variant="destructive"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => openDeleteDialog(association.ID)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>确定要删除这个关联吗？</AlertDialogTitle>
                            <AlertDialogDescription>
                              此操作无法撤销。这将永久删除该关联管理。
                            </AlertDialogDescription>
                          </AlertDialogHeader>
                          <AlertDialogFooter>
                            <AlertDialogCancel onClick={() => setDeleteId(null)}>取消</AlertDialogCancel>
                            <AlertDialogAction onClick={handleDelete}>确认删除</AlertDialogAction>
                          </AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>

      <ModelProviderFormDialog
        open={open}
        onOpenChange={setOpen}
        form={form}
        onSubmit={submit}
        editingAssociation={editingAssociation}
        models={models}
        providers={providers}
        headerFields={headerFields}
        appendHeader={appendHeader}
        removeHeader={removeHeader}
        showProviderModels={showProviderModels}
        setShowProviderModels={setShowProviderModels}
        selectedProviderId={selectedProviderId}
        providerModelsMap={providerModelsMap}
        providerModelsLoading={providerModelsLoading}
        sortProviderModels={sortProviderModels}
        loadProviderModels={loadProviderModels}
      />

      <ModelProviderTestDialog
        open={testDialogOpen}
        onOpenChange={setTestDialogOpen}
        onClose={closeTestDialog}
        testType={testType}
        setTestType={setTestType}
        selectedTestId={selectedTestId}
        testResults={testResults}
        reactTestResult={reactTestResult}
        executeTest={executeTest}
      />
    </div>
  );
}
