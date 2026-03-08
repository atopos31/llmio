import { useState, useEffect, useCallback, type DragEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
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
import { Checkbox } from "@/components/ui/checkbox";
import Loading from "@/components/loading";
import {
  getModelProviders,
  getModelProviderStatus,
  updateModelProviderStatus,
  deleteModelProvider,
  deleteModel,
  createModel,
  getModelOptions,
  updateModel,
  getProviders,
  getProviderModels,
  updateModelOrder
} from "@/lib/api";
import type { ModelWithProvider, Model, Provider, ProviderModel } from "@/lib/api";
import { toast } from "sonner";
import { ArrowLeft, RefreshCw, Pencil, Trash2, Zap, Search, Link, ListCollapse } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { useModelProviderForm } from "@/routes/model-providers/use-model-provider-form";
import { useModelProviderTesting } from "@/routes/model-providers/use-model-provider-testing";
import { ModelProviderFormDialog } from "@/routes/model-providers/model-provider-form-dialog";
import { ModelProviderTestDialog } from "@/routes/model-providers/model-provider-test-dialog";

type MobileInfoItemProps = {
  label: string;
  value: ReactNode;
};

type StrategyFilter = "all" | "lottery" | "rotor";
type IOLogFilter = "all" | "true" | "false";

const MobileInfoItem = ({ label, value }: MobileInfoItemProps) => (
  <div className="space-y-1">
    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{label}</p>
    <div className="text-sm font-medium break-words">{value}</div>
  </div>
);

const renderStrategy = (strategy?: string) =>
  strategy === "rotor" ? "Rotor" : "Lottery";

const modelEditSchema = z.object({
  name: z.string().min(1, { message: "模型名称不能为空" }),
  remark: z.string(),
  max_retry: z.number().min(0, { message: "重试次数限制不能为负数" }),
  time_out: z.number().min(0, { message: "超时时间不能为负数" }),
  io_log: z.boolean(),
  strategy: z.enum(["lottery", "rotor"]),
  breaker: z.boolean(),
});

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
  const [providerSearchInput, setProviderSearchInput] = useState("");
  const [providerSearchTerm, setProviderSearchTerm] = useState("");
  const [weightSortOrder, setWeightSortOrder] = useState<"asc" | "desc" | "none">("none");
  const [statusUpdating, setStatusUpdating] = useState<Record<number, boolean>>({});
  const [statusError, setStatusError] = useState<string | null>(null);
  const [orderedCardModels, setOrderedCardModels] = useState<Model[]>([]);
  const [draggingModelId, setDraggingModelId] = useState<number | null>(null);
  const [dragOverModelId, setDragOverModelId] = useState<number | null>(null);
  const [cardOrderSaving, setCardOrderSaving] = useState(false);
  const [suppressCardClick, setSuppressCardClick] = useState(false);
  const [modelAssociationCountMap, setModelAssociationCountMap] = useState<Record<number, number>>({});
  const [modelAssociationCountLoading, setModelAssociationCountLoading] = useState(false);
  const [modelEditOpen, setModelEditOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<Model | null>(null);
  const [modelEditSaving, setModelEditSaving] = useState(false);
  const [modelDeleteId, setModelDeleteId] = useState<number | null>(null);
  const [modelDeleteLoading, setModelDeleteLoading] = useState(false);
  const [modelSearchInput, setModelSearchInput] = useState("");
  const [modelSearchTerm, setModelSearchTerm] = useState("");
  const [modelStrategyFilter, setModelStrategyFilter] = useState<StrategyFilter>("all");
  const [modelIOLogFilter, setModelIOLogFilter] = useState<IOLogFilter>("all");

  const modelEditForm = useForm<z.infer<typeof modelEditSchema>>({
    resolver: zodResolver(modelEditSchema),
    defaultValues: {
      name: "",
      remark: "",
      max_retry: 10,
      time_out: 60,
      io_log: false,
      strategy: "lottery",
      breaker: false,
    },
  });

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

  const sortCardModels = useCallback((modelList: Model[]) => {
    return [...modelList].sort((a, b) => {
      const orderA = a.DisplayOrder ?? 0;
      const orderB = b.DisplayOrder ?? 0;
      if (orderA !== orderB) return orderB - orderA;
      return b.ID - a.ID;
    });
  }, []);

  const reorderCardModels = useCallback((modelList: Model[], sourceId: number, targetId: number) => {
    if (sourceId === targetId) return modelList;
    const sourceIndex = modelList.findIndex((item) => item.ID === sourceId);
    const targetIndex = modelList.findIndex((item) => item.ID === targetId);
    if (sourceIndex === -1 || targetIndex === -1) return modelList;
    if (sourceIndex === targetIndex) return modelList;

    const next = [...modelList];
    const [moved] = next.splice(sourceIndex, 1);
    next.splice(targetIndex, 0, moved);
    return next;
  }, []);

  useEffect(() => {
    setOrderedCardModels(sortCardModels(models));
  }, [models, sortCardModels]);

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

  const loadProviderStatus = useCallback(async (providers: ModelWithProvider[], modelId: number) => {
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
  }, [models]);

  const fetchModelProviders = useCallback(async (modelId: number) => {
    try {
      setLoading(true);
      const data = await getModelProviders(modelId);
      setModelProviders(data.map(item => ({
        ...item,
        CustomerHeaders: item.CustomerHeaders || {}
      })));
      setModelAssociationCountMap((prev) => ({ ...prev, [modelId]: data.length }));
      // 异步加载状态数据
      loadProviderStatus(data, modelId);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取关联管理列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  }, [loadProviderStatus]);

  const refreshModelAssociationCount = useCallback(async (modelId: number) => {
    try {
      const associations = await getModelProviders(modelId);
      setModelAssociationCountMap((prev) => ({ ...prev, [modelId]: associations.length }));
    } catch (err) {
      console.error(`Failed to refresh association count for model ${modelId}:`, err);
      setModelAssociationCountMap((prev) => ({ ...prev, [modelId]: -1 }));
    }
  }, []);

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
    onReload: async (modelId) => {
      if (selectedModelId) {
        await fetchModelProviders(selectedModelId);
        return;
      }
      await refreshModelAssociationCount(modelId);
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
    if (!modelIdParam) {
      if (selectedModelId !== null) {
        setSelectedModelId(null);
        form.setValue("model_id", 0);
      }
      return;
    }

    const parsedParam = Number(modelIdParam);
    if (!Number.isNaN(parsedParam) && models.some((model) => model.ID === parsedParam)) {
      if (selectedModelId !== parsedParam) {
        setSelectedModelId(parsedParam);
        form.setValue("model_id", parsedParam);
      }
      return;
    }

    if (selectedModelId !== null) {
      setSelectedModelId(null);
      form.setValue("model_id", 0);
    }
    const nextParams = new URLSearchParams(searchParams);
    nextParams.delete("modelId");
    setSearchParams(nextParams, { replace: true });
  }, [models, searchParams, form, selectedModelId, setSearchParams]);

  useEffect(() => {
    if (selectedModelId) {
      fetchModelProviders(selectedModelId);
    }
  }, [selectedModelId, fetchModelProviders]);

  useEffect(() => {
    const timer = setTimeout(() => {
      setModelSearchTerm(modelSearchInput.trim());
    }, 300);
    return () => clearTimeout(timer);
  }, [modelSearchInput]);

  useEffect(() => {
    const timer = setTimeout(() => {
      setProviderSearchTerm(providerSearchInput.trim().toLowerCase());
    }, 300);
    return () => clearTimeout(timer);
  }, [providerSearchInput]);

  useEffect(() => {
    if (models.length === 0) {
      setModelAssociationCountMap({});
      setModelAssociationCountLoading(false);
      return;
    }
    if (selectedModelId !== null) return;

    let active = true;
    setModelAssociationCountLoading(true);

    const loadAssociationCounts = async () => {
      const entries = await Promise.all(
        models.map(async (model) => {
          try {
            const associations = await getModelProviders(model.ID);
            return [model.ID, associations.length] as const;
          } catch (err) {
            console.error(`Failed to load association count for model ${model.ID}:`, err);
            return [model.ID, -1] as const;
          }
        })
      );

      if (!active) return;
      const nextCountMap: Record<number, number> = {};
      entries.forEach(([modelId, count]) => {
        nextCountMap[modelId] = count;
      });
      setModelAssociationCountMap(nextCountMap);
      setModelAssociationCountLoading(false);
    };

    loadAssociationCounts().catch((err) => {
      if (!active) return;
      console.error("Failed to load model association counts:", err);
      setModelAssociationCountLoading(false);
    });

    return () => {
      active = false;
    };
  }, [models, selectedModelId]);

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

  const persistCardOrder = async (nextOrderedModels: Model[]) => {
    const nextModelIds = nextOrderedModels.map((model) => model.ID);
    const currentModelIds = sortCardModels(models).map((model) => model.ID);
    if (nextModelIds.join(",") === currentModelIds.join(",")) return;

    setCardOrderSaving(true);
    try {
      await updateModelOrder(nextModelIds);

      const total = nextOrderedModels.length;
      const nextOrderMap = new Map<number, number>();
      nextOrderedModels.forEach((model, index) => {
        nextOrderMap.set(model.ID, total - index);
      });

      setModels((prev) =>
        prev.map((model) => ({
          ...model,
          DisplayOrder: nextOrderMap.get(model.ID) ?? model.DisplayOrder ?? 0,
        }))
      );
      toast.success("模型排序已保存");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`保存模型排序失败: ${message}`);
      setOrderedCardModels(sortCardModels(models));
    } finally {
      setCardOrderSaving(false);
    }
  };

  const handleCardDragStart = (event: DragEvent<HTMLElement>, modelId: number) => {
    if (cardOrderSaving || hasModelOverviewFilter) {
      event.preventDefault();
      return;
    }
    setDraggingModelId(modelId);
    setDragOverModelId(null);
    setSuppressCardClick(true);
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", modelId.toString());
  };

  const handleCardDragOver = (event: DragEvent<HTMLElement>, targetModelId: number) => {
    if (hasModelOverviewFilter) return;
    event.preventDefault();
    if (draggingModelId === null || draggingModelId === targetModelId) return;
    setDragOverModelId(targetModelId);
    setOrderedCardModels((prev) => reorderCardModels(prev, draggingModelId, targetModelId));
  };

  const handleCardDrop = async (event: DragEvent<HTMLElement>) => {
    event.preventDefault();
    setDraggingModelId(null);
    setDragOverModelId(null);
    if (hasModelOverviewFilter) return;
    await persistCardOrder(orderedCardModels);
  };

  const handleCardDragEnd = () => {
    setDraggingModelId(null);
    setDragOverModelId(null);
    setTimeout(() => setSuppressCardClick(false), 0);
  };

  const openModelEditDialog = (model: Model) => {
    setEditingModel(model);
    modelEditForm.reset({
      name: model.Name,
      remark: model.Remark ?? "",
      max_retry: model.MaxRetry,
      time_out: model.TimeOut,
      io_log: !!model.IOLog,
      strategy: model.Strategy === "rotor" ? "rotor" : "lottery",
      breaker: model.Breaker ?? false,
    });
    setModelEditOpen(true);
  };

  const openModelCreateDialog = () => {
    setEditingModel(null);
    modelEditForm.reset({
      name: "",
      remark: "",
      max_retry: 10,
      time_out: 60,
      io_log: false,
      strategy: "lottery",
      breaker: false,
    });
    setModelEditOpen(true);
  };

  const closeModelEditDialog = () => {
    setModelEditOpen(false);
    setEditingModel(null);
    modelEditForm.reset({
      name: "",
      remark: "",
      max_retry: 10,
      time_out: 60,
      io_log: false,
      strategy: "lottery",
      breaker: false,
    });
    setModelEditSaving(false);
  };

  const handleModelSave = async (values: z.infer<typeof modelEditSchema>) => {
    setModelEditSaving(true);
    try {
      if (editingModel) {
        const updated = await updateModel(editingModel.ID, {
          name: values.name,
          remark: values.remark,
          max_retry: values.max_retry,
          time_out: values.time_out,
          strategy: values.strategy,
          io_log: values.io_log,
          breaker: values.breaker,
        });

        setModels((prev) =>
          prev.map((model) =>
            model.ID === editingModel.ID
              ? {
                ...model,
                Name: updated.Name,
                Remark: updated.Remark,
                MaxRetry: updated.MaxRetry,
                TimeOut: updated.TimeOut,
                Strategy: updated.Strategy,
                IOLog: updated.IOLog,
                Breaker: updated.Breaker,
              }
              : model
          )
        );
        toast.success(`模型: ${updated.Name} 更新成功`);
      } else {
        const created = await createModel({
          name: values.name,
          remark: values.remark,
          max_retry: values.max_retry,
          time_out: values.time_out,
          strategy: values.strategy,
          io_log: values.io_log,
          breaker: values.breaker,
        });

        setModels((prev) => sortCardModels([...prev, created]));
        setModelAssociationCountMap((prev) => ({ ...prev, [created.ID]: 0 }));
        toast.success(`模型: ${created.Name} 创建成功`);
      }
      closeModelEditDialog();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`${editingModel ? "更新" : "创建"}模型失败: ${message}`);
    } finally {
      setModelEditSaving(false);
    }
  };

  const handleModelSelect = (modelId: number) => {
    if (modelId === selectedModelId) return;
    setLoading(true);
    setSelectedModelId(modelId);
    setModelProviders([]);
    setProviderStatus({});
    setStatusError(null);
    const nextParams = new URLSearchParams(searchParams);
    nextParams.set("modelId", modelId.toString());
    setSearchParams(nextParams);
    form.setValue("model_id", modelId);
  };

  const handleBackToModelCards = () => {
    const nextParams = new URLSearchParams(searchParams);
    nextParams.delete("modelId");
    setSearchParams(nextParams);
    setSelectedModelId(null);
    setModelProviders([]);
    setProviderStatus({});
    setStatusError(null);
    form.setValue("model_id", 0);
  };

  // 获取唯一的提供商类型列表
  const providerTypes = Array.from(new Set(providers.map(p => p.Type).filter(Boolean)));

  // 根据筛选条件过滤关联管理，并按权重排序
  const filteredModelProviders = modelProviders.filter((association) => {
    const provider = providers.find((p) => p.ID === association.ProviderID);
    const matchesType = selectedProviderType === "all" || provider?.Type === selectedProviderType;
    if (!matchesType) return false;
    if (!providerSearchTerm) return true;

    const providerName = (provider?.Name ?? "").toLowerCase();
    const providerModel = (association.ProviderModel ?? "").toLowerCase();
    const providerType = (provider?.Type ?? "").toLowerCase();
    const providerId = association.ProviderID.toString();
    return (
      providerName.includes(providerSearchTerm) ||
      providerModel.includes(providerSearchTerm) ||
      providerType.includes(providerSearchTerm) ||
      providerId.includes(providerSearchTerm)
    );
  });

  // 按权重排序
  const sortedModelProviders = [...filteredModelProviders].sort((a, b) => {
    if (weightSortOrder === "none") return 0;
    return weightSortOrder === "asc" ? a.Weight - b.Weight : b.Weight - a.Weight;
  });

  const hasAssociationFilter = selectedProviderType !== "all" || providerSearchTerm.length > 0;
  const getAssociationCountNumberText = (modelId: number) => {
    const count = modelAssociationCountMap[modelId];
    if (count === undefined) return modelAssociationCountLoading ? "-" : "--";
    if (count < 0) return "--";
    return String(count);
  };

  const filteredOverviewModels = orderedCardModels.filter((model) => {
    const matchesSearch = modelSearchTerm.length === 0 || model.Name.toLowerCase().includes(modelSearchTerm.toLowerCase());
    const matchesStrategy = modelStrategyFilter === "all" || model.Strategy === modelStrategyFilter;
    const matchesIO =
      modelIOLogFilter === "all" ||
      (modelIOLogFilter === "true" && !!model.IOLog) ||
      (modelIOLogFilter === "false" && !model.IOLog);
    return matchesSearch && matchesStrategy && matchesIO;
  });

  const hasModelOverviewFilter =
    modelSearchTerm.length > 0 || modelStrategyFilter !== "all" || modelIOLogFilter !== "all";
  const modelPendingDelete = modelDeleteId ? models.find((model) => model.ID === modelDeleteId) : null;

  const handleDeleteModel = async () => {
    if (!modelDeleteId) return;
    setModelDeleteLoading(true);
    try {
      const targetModel = models.find((model) => model.ID === modelDeleteId);
      await deleteModel(modelDeleteId);
      setModels((prev) => prev.filter((model) => model.ID !== modelDeleteId));
      setModelAssociationCountMap((prev) => {
        const next = { ...prev };
        delete next[modelDeleteId];
        return next;
      });
      toast.success(`模型: ${targetModel?.Name ?? modelDeleteId} 删除成功`);
      setModelDeleteId(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除模型失败: ${message}`);
      console.error(err);
    } finally {
      setModelDeleteLoading(false);
    }
  };

  if (loading && models.length === 0 && providers.length === 0) return <Loading message="加载模型和提供商" />;

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            <h2 className="text-2xl font-bold tracking-tight">模型管理</h2>
          </div>
          {selectedModelId && (
            <div className="flex items-center gap-2">
              <Button variant="outline" className="h-8 text-xs" onClick={handleBackToModelCards}>
                <ArrowLeft className="size-3.5" />
                返回模型列表
              </Button>
              <Button onClick={() => openCreateDialog()} className="h-8 text-xs">
                添加关联
              </Button>
            </div>
          )}
        </div>

        {!selectedModelId && (
          <div className="flex flex-col gap-2">
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4 lg:gap-4">
              <div className="flex flex-col gap-1 text-xs lg:min-w-0 lg:col-span-2">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">搜索</Label>
                <div className="relative">
                  <Search className="size-4 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    placeholder="按名称搜索"
                    value={modelSearchInput}
                    onChange={(event) => setModelSearchInput(event.target.value)}
                    className="h-8 pl-8 text-xs"
                  />
                </div>
              </div>
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">负载策略</Label>
                <Select
                  value={modelStrategyFilter}
                  onValueChange={(value) => setModelStrategyFilter(value as StrategyFilter)}
                >
                  <SelectTrigger className="h-8 w-full text-xs px-2">
                    <SelectValue placeholder="负载策略" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">全部</SelectItem>
                    <SelectItem value="lottery">Lottery</SelectItem>
                    <SelectItem value="rotor">Rotor</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">IO 记录</Label>
                <div className="flex items-center gap-2">
                  <Select
                    value={modelIOLogFilter}
                    onValueChange={(value) => setModelIOLogFilter(value as IOLogFilter)}
                  >
                    <SelectTrigger className="h-8 w-full text-xs px-2">
                      <SelectValue placeholder="IO 记录" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">全部</SelectItem>
                      <SelectItem value="true">开启</SelectItem>
                      <SelectItem value="false">关闭</SelectItem>
                    </SelectContent>
                  </Select>
                  <Button onClick={openModelCreateDialog} className="h-8 text-xs shrink-0">
                    添加模型
                  </Button>
                </div>
              </div>
            </div>
          </div>
        )}

        {selectedModelId && (
          <div className="flex flex-col gap-2 flex-shrink-0">
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4 lg:gap-4">
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">搜索提供商</Label>
                <div className="relative">
                  <Search className="size-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    placeholder="按提供商/模型搜索"
                    value={providerSearchInput}
                    onChange={(event) => setProviderSearchInput(event.target.value)}
                    className="h-8 pl-7 text-xs"
                  />
                </div>
              </div>
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">快速切换模型</Label>
                <Select value={selectedModelId?.toString() || ""} onValueChange={(value) => handleModelSelect(Number(value))}>
                  <SelectTrigger className="h-8 w-full text-xs px-2">
                    <SelectValue placeholder="选择模型" />
                  </SelectTrigger>
                  <SelectContent>
                    {orderedCardModels.map((model) => (
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
              <div className="flex flex-col gap-1 text-xs">
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
            </div>
          </div>
        )}
      </div>

      {!selectedModelId && (
        <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
          {loading ? (
            <div className="flex h-full items-center justify-center">
              <Loading message="加载模型列表" />
            </div>
          ) : filteredOverviewModels.length === 0 ? (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              {hasModelOverviewFilter ? "没有符合筛选条件的模型" : "暂无可关联模型"}
            </div>
          ) : (
            <div className="h-full flex flex-col">
              <div className="px-3 pt-3 pb-1 text-xs text-muted-foreground">
                {hasModelOverviewFilter
                  ? "筛选状态下暂不支持拖拽排序，点击行可查看详情"
                  : "可拖拽调整列表顺序并自动保存，点击行可查看详情"}
              </div>
              <div className="hidden sm:block flex-1 overflow-y-auto">
                <div className="w-full">
                  <Table className="min-w-[1100px]">
                    <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                      <TableRow>
                        <TableHead>ID</TableHead>
                        <TableHead>名称</TableHead>
                        <TableHead>备注</TableHead>
                        <TableHead className="text-center">已关联模型</TableHead>
                        <TableHead className="text-center">重试次数限制</TableHead>
                        <TableHead className="text-center">超时时间(秒)</TableHead>
                        <TableHead className="text-center">负载策略</TableHead>
                        <TableHead className="text-center">IO 记录</TableHead>
                        <TableHead className="text-center">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {filteredOverviewModels.map((model) => (
                        <TableRow
                          key={model.ID}
                          draggable={!cardOrderSaving && !hasModelOverviewFilter}
                          onDragStart={(event) => handleCardDragStart(event, model.ID)}
                          onDragOver={(event) => handleCardDragOver(event, model.ID)}
                          onDrop={handleCardDrop}
                          onDragEnd={handleCardDragEnd}
                          className={`cursor-pointer transition-colors ${
                            draggingModelId === model.ID ? "opacity-60 ring-1 ring-primary/60" : ""
                          } ${
                            dragOverModelId === model.ID && draggingModelId !== model.ID ? "bg-accent/40" : ""
                          }`}
                          onClick={() => {
                            if (suppressCardClick || cardOrderSaving) return;
                            handleModelSelect(model.ID);
                          }}
                        >
                          <TableCell className="font-mono text-xs text-muted-foreground">{model.ID}</TableCell>
                          <TableCell className="font-medium">{model.Name}</TableCell>
                          <TableCell className="max-w-[240px] truncate text-sm" title={model.Remark || "-"}>
                            {model.Remark || "-"}
                          </TableCell>
                          <TableCell className="text-sm text-center">{getAssociationCountNumberText(model.ID)}</TableCell>
                          <TableCell className="text-center">{model.MaxRetry}</TableCell>
                          <TableCell className="text-center">{model.TimeOut}</TableCell>
                          <TableCell className="text-sm text-muted-foreground text-center">{renderStrategy(model.Strategy)}</TableCell>
                          <TableCell className="text-center">
                            <span className={model.IOLog ? "text-green-500" : "text-red-500"}>
                              {model.IOLog ? "✓" : "✗"}
                            </span>
                          </TableCell>
                          <TableCell>
                            <div className="flex gap-2 justify-center">
                              <Button
                                variant="outline"
                                size="icon"
                                className="h-8 w-8"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  openModelEditDialog(model);
                                }}
                                title="编辑模型"
                                aria-label="编辑模型"
                              >
                                <Pencil className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="outline"
                                size="icon"
                                className="h-8 w-8"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  openCreateDialog(model.ID);
                                }}
                                title="添加关联"
                                aria-label="添加关联"
                              >
                                <Link className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="outline"
                                size="icon"
                                className="h-8 w-8"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  handleModelSelect(model.ID);
                                }}
                                title="查看详情"
                                aria-label="查看详情"
                              >
                                <ListCollapse className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="destructive"
                                size="icon"
                                className="h-8 w-8"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  setModelDeleteId(model.ID);
                                }}
                                aria-label="删除模型"
                              >
                                <Trash2 className="h-4 w-4" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </div>
              <div className="sm:hidden flex-1 min-h-0 overflow-y-auto px-2 py-3 divide-y divide-border">
                {filteredOverviewModels.map((model) => (
                  <div
                    key={model.ID}
                    draggable={!cardOrderSaving && !hasModelOverviewFilter}
                    onDragStart={(event) => handleCardDragStart(event, model.ID)}
                    onDragOver={(event) => handleCardDragOver(event, model.ID)}
                    onDrop={handleCardDrop}
                    onDragEnd={handleCardDragEnd}
                    className={`py-3 space-y-3 transition-colors ${
                      draggingModelId === model.ID ? "opacity-60" : ""
                    } ${
                      dragOverModelId === model.ID && draggingModelId !== model.ID ? "bg-accent/20 rounded-md" : ""
                    }`}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0 flex-1">
                        <h3 className="font-semibold text-sm truncate">{model.Name}</h3>
                        <p className="text-[11px] text-muted-foreground">模型 ID: {model.ID}</p>
                      </div>
                      <div className="flex gap-1.5">
                        <Button
                          variant="outline"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => openModelEditDialog(model)}
                          title="编辑模型"
                          aria-label="编辑模型"
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="outline"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => openCreateDialog(model.ID)}
                          title="添加关联"
                          aria-label="添加关联"
                        >
                          <Link className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="outline"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => handleModelSelect(model.ID)}
                          title="查看详情"
                          aria-label="查看详情"
                        >
                          <ListCollapse className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="destructive"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => setModelDeleteId(model.ID)}
                          aria-label="删除模型"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                    <div className="text-xs space-y-1">
                      <p className="text-[11px] text-muted-foreground uppercase tracking-wide">备注</p>
                      <p className="break-words">{model.Remark || "-"}</p>
                    </div>
                    <div className="grid grid-cols-2 gap-3 text-xs">
                      <MobileInfoItem label="重试次数" value={model.MaxRetry} />
                      <MobileInfoItem label="超时时间" value={`${model.TimeOut} 秒`} />
                      <MobileInfoItem label="负载策略" value={renderStrategy(model.Strategy)} />
                      <MobileInfoItem label="已关联" value={getAssociationCountNumberText(model.ID)} />
                      <MobileInfoItem
                        label="IO 记录"
                        value={<span className={model.IOLog ? "text-green-500" : "text-red-500"}>{model.IOLog ? "✓" : "✗"}</span>}
                      />
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {selectedModelId && (
        <>
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
        ) : sortedModelProviders.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground text-sm text-center px-6">
            {hasAssociationFilter ? '当前筛选条件暂无关联' : '该模型还没有关联的提供商'}
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
        </>
      )}

      <AlertDialog open={modelDeleteId !== null} onOpenChange={(open) => !open && setModelDeleteId(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确定要删除这个模型吗？</AlertDialogTitle>
            <AlertDialogDescription>
              此操作无法撤销。这将永久删除模型
              {modelPendingDelete ? `「${modelPendingDelete.Name}」` : ""}。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={modelDeleteLoading}>取消</AlertDialogCancel>
            <AlertDialogAction onClick={handleDeleteModel} disabled={modelDeleteLoading}>
              {modelDeleteLoading ? "删除中..." : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Dialog open={modelEditOpen} onOpenChange={(open) => (open ? setModelEditOpen(true) : closeModelEditDialog())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editingModel ? "编辑模型" : "添加模型"}</DialogTitle>
            <DialogDescription>
              {editingModel ? "修改模型信息" : "添加一个新的模型"}
            </DialogDescription>
          </DialogHeader>
          <Form {...modelEditForm}>
            <form onSubmit={modelEditForm.handleSubmit(handleModelSave)} className="space-y-4">
              <FormField
                control={modelEditForm.control}
                name="name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>名称</FormLabel>
                    <FormControl>
                      <Input {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={modelEditForm.control}
                name="remark"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>备注</FormLabel>
                    <FormControl>
                      <Textarea {...field} rows={3} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className="grid grid-cols-2 gap-4">
                <FormField
                  control={modelEditForm.control}
                  name="max_retry"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>重试次数限制</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          {...field}
                          onChange={(e) => field.onChange(+e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={modelEditForm.control}
                  name="time_out"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>超时时间(秒)</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          {...field}
                          onChange={(e) => field.onChange(+e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={modelEditForm.control}
                name="io_log"
                render={({ field }) => (
                  <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                    <div className="space-y-0.5">
                      <FormLabel className="text-base">IO 记录</FormLabel>
                    </div>
                    <FormControl>
                      <Checkbox
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={modelEditForm.control}
                name="breaker"
                render={({ field }) => (
                  <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                    <div className="space-y-0.5">
                      <FormLabel className="text-base">熔断</FormLabel>
                    </div>
                    <FormControl>
                      <Checkbox checked={field.value} onCheckedChange={field.onChange} />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={modelEditForm.control}
                name="strategy"
                render={({ field }) => (
                  <FormItem className="rounded-lg border p-4 space-y-3">
                    <div className="flex flex-col gap-1">
                      <FormLabel className="text-base">负载均衡策略</FormLabel>
                    </div>
                    <div className="grid gap-2 sm:grid-cols-2">
                      {[
                        {
                          value: "lottery",
                          title: "Lottery",
                          desc: "按权重概率抽取, 适合随机分散流量.",
                        },
                        {
                          value: "rotor",
                          title: "Rotor",
                          desc: "按权重循环轮转, 适合需要缓存命中场景.",
                        },
                      ].map((option) => (
                        <label
                          key={option.value}
                          className="flex cursor-pointer items-start gap-3 rounded-md border p-3 hover:bg-accent"
                        >
                          <FormControl>
                            <Checkbox
                              checked={field.value === option.value}
                              onCheckedChange={(checked) => {
                                if (checked) field.onChange(option.value);
                              }}
                            />
                          </FormControl>
                          <div className="space-y-1">
                            <p className="font-medium leading-none">{option.title}</p>
                            <p className="text-[13px] text-muted-foreground">{option.desc}</p>
                          </div>
                        </label>
                      ))}
                    </div>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={closeModelEditDialog} disabled={modelEditSaving}>
                  取消
                </Button>
                <Button type="submit" disabled={modelEditSaving}>
                  {modelEditSaving ? "保存中..." : editingModel ? "更新" : "创建"}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

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
