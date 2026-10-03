import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Label } from "@/components/ui/label";
import Loading from "@/components/loading";
import {
  getModelProviders,
  getModelProviderStatus,
  updateModelProviderStatus,
  deleteModelProvider,
  deleteModel,
  getModelOptions,
  getProviders,
  getProviderModels
} from "@/lib/api";
import type { ModelWithProvider, Model, Provider, ProviderModel } from "@/lib/api";
import { toast } from "sonner";
import { ArrowLeft, Search } from "lucide-react";
import { useModelProviderForm } from "@/routes/model-providers/use-model-provider-form";
import { useModelProviderTesting } from "@/routes/model-providers/use-model-provider-testing";
import { sortModelsByOrder, useModelOrder } from "@/routes/model-providers/use-model-order";
import { useModelEditor } from "@/routes/model-providers/use-model-editor";
import { ModelProviderFormDialog } from "@/routes/model-providers/model-provider-form-dialog";
import { ModelProviderTestDialog } from "@/routes/model-providers/model-provider-test-dialog";
import { ModelList } from "@/routes/model-providers/model-list";
import { AssociationList } from "@/routes/model-providers/association-list";
import { ModelDeleteDialog, ModelFormDialog } from "@/routes/model-providers/model-dialogs";

type StrategyFilter = "all" | "lottery" | "rotor";

/** 失败信息一律取原文：只有原文可能指向原因 */
const messageOf = (err: unknown) => (err instanceof Error ? err.message : String(err));

/**
 * 模型路由页（模型 → 提供商关联）。
 *
 * 这个文件是页面壳：谁拥有数据、什么时候去取、筛选与地址栏怎么同步，
 * 以及两个视图之间的切换。画的部分在 model-providers/ 下按职责分开——
 * 模型列表、关联列表、模型对话框、模型顺序（拖拽）、连通性测试。
 *
 * 页面有两种形态，由地址栏的 `modelId` 决定：没有它时是模型总览，
 * 有它时是那个模型的关联列表。切换即改地址栏，因此刷新和分享都停在原地。
 */
export default function ModelProvidersPage() {
  const { t } = useTranslation(['models', 'common']);
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
  const [weightSortOrder, setWeightSortOrder] = useState<"asc" | "desc" | "none">("desc");
  const [statusUpdating, setStatusUpdating] = useState<Record<number, boolean>>({});
  const [statusError, setStatusError] = useState<string | null>(null);
  const [modelAssociationCountMap, setModelAssociationCountMap] = useState<Record<number, number>>({});
  const [modelAssociationCountLoading, setModelAssociationCountLoading] = useState(false);
  const [modelDeleteId, setModelDeleteId] = useState<number | null>(null);
  const [modelDeleteLoading, setModelDeleteLoading] = useState(false);
  const [modelSearchInput, setModelSearchInput] = useState("");
  const [modelSearchTerm, setModelSearchTerm] = useState("");
  const [modelStrategyFilter, setModelStrategyFilter] = useState<StrategyFilter>("all");
  /** 取模型/提供商失败的原文；有值即整页显示失败态而不是空态 */
  const [overviewError, setOverviewError] = useState<string | null>(null);
  /** 取该模型关联失败的原文，同上 */
  const [associationsError, setAssociationsError] = useState<string | null>(null);

  // 总览上的筛选：既决定列表里剩哪些模型，也决定能不能拖拽排序
  // （带着筛选拖，拖出来的局部顺序会被当成全量顺序保存）。
  // 它只依赖两个筛选条件，因此在用到它的 hook 之前就能算出来。
  const hasModelOverviewFilter =
    modelSearchTerm.length > 0 || modelStrategyFilter !== "all";

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

  /** 拖拽排序保存成功后，把新顺序折算成 DisplayOrder 回写——顺序的真相只在 models 上 */
  const applyOrder = useCallback((nextOrderedModels: Model[]) => {
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
  }, []);

  const order = useModelOrder(models, { filtering: hasModelOverviewFilter, onOrderSaved: applyOrder });

  const modelEditor = useModelEditor({
    onUpdated: (updated) => {
      setModels((prev) =>
        prev.map((model) =>
          model.ID === updated.ID
            ? {
              ...model,
              Name: updated.Name,
              Remark: updated.Remark,
              MaxRetry: updated.MaxRetry,
              TimeOut: updated.TimeOut,
              Strategy: updated.Strategy,
              Breaker: updated.Breaker,
            }
            : model
        )
      );
    },
    onCreated: (created) => {
      // 新建的模型也要按展示顺序落进 models：列表画的是排序后的那份，
      // 但"关联"对话框的模型下拉直接读 models，不排序它就会掉在末尾
      setModels((prev) => sortModelsByOrder([...prev, created]));
      setModelAssociationCountMap((prev) => ({ ...prev, [created.ID]: 0 }));
    },
  });

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

  /**
   * 模型与提供商一起取：这一页没有它们就没有内容可看，因此失败也只有一个出口——
   * 面板上的失败态（给原文 + 重试），而不是一条会自己消失的提示加上一页
   * 假装"暂无可关联模型"的空态。
   */
  const loadOverview = useCallback(async () => {
    setLoading(true);
    setOverviewError(null);
    const [modelsResult, providersResult] = await Promise.allSettled([getModelOptions(), getProviders()]);
    if (modelsResult.status === "fulfilled") setModels(modelsResult.value);
    if (providersResult.status === "fulfilled") setProviders(providersResult.value);

    // 两处一起取，只留一条原文：先说模型那条——没有模型，这一页连列表都没有
    const failure = modelsResult.status === "rejected"
      ? modelsResult.reason
      : providersResult.status === "rejected" ? providersResult.reason : null;
    if (failure !== null) {
      console.error(failure);
      setOverviewError(messageOf(failure));
    }
    setLoading(false);
  }, []);


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
      setAssociationsError(null);
      const data = await getModelProviders(modelId);
      setModelProviders(data.map(item => ({
        ...item,
        CustomerHeaders: item.CustomerHeaders || {}
      })));
      setModelAssociationCountMap((prev) => ({ ...prev, [modelId]: data.length }));
      // 异步加载状态数据
      loadProviderStatus(data, modelId);
    } catch (err) {
      // 失败要留在面板上：只弹一条提示的话，紧随其后的就是"该模型还没有关联的提供商"，
      // 等于把取数失败说成了这个模型确实没有关联
      console.error(err);
      setAssociationsError(messageOf(err));
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
    peakTerms,
    setPeakTerms,
    peakSubmitAttempt,
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
    void loadOverview();
  }, [loadOverview]);

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
      toast.error(t('toast.association_delete_failed', { message }));
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
      toast.error(t('toast.model_save_failed', { message }));
      console.error(err);
    } finally {
      setModelDeleteLoading(false);
    }
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

  const filteredOverviewModels = order.orderedModels.filter((model) => {
    const matchesSearch = modelSearchTerm.length === 0 || model.Name.toLowerCase().includes(modelSearchTerm.toLowerCase());
    const matchesStrategy = modelStrategyFilter === "all" || model.Strategy === modelStrategyFilter;
    return matchesSearch && matchesStrategy;
  });

  const modelPendingDelete = modelDeleteId ? models.find((model) => model.ID === modelDeleteId) ?? null : null;

  if (loading && models.length === 0 && providers.length === 0) return <Loading message="加载模型和提供商" />;

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            <h2 className="text-2xl font-bold tracking-tight">{t('title')}</h2>
          </div>
          {selectedModelId && (
            <div className="flex items-center gap-2">
              <Button variant="outline" className="h-8 text-xs" onClick={handleBackToModelCards}>
                <ArrowLeft className="size-3.5" />
                {t('actions.back_to_list')}
              </Button>
              <Button onClick={() => openCreateDialog()} className="h-8 text-xs">
                {t('actions.add_association')}
              </Button>
            </div>
          )}
        </div>

        {!selectedModelId && (
          <div className="flex flex-col gap-2">
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4 lg:gap-4">
              <div className="flex flex-col gap-1 text-xs lg:min-w-0 lg:col-span-2">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.search')}</Label>
                <div className="relative">
                  <Search className="size-4 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    placeholder={t('filters.search_placeholder')}
                    value={modelSearchInput}
                    onChange={(event) => setModelSearchInput(event.target.value)}
                    className="h-8 pl-8 text-xs"
                  />
                </div>
              </div>
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.strategy')}</Label>
                <Select
                  value={modelStrategyFilter}
                  onValueChange={(value) => setModelStrategyFilter(value as StrategyFilter)}
                >
                  <SelectTrigger className="h-8 w-full text-xs px-2">
                    <SelectValue placeholder={t('filters.strategy')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">{t('common:status.all')}</SelectItem>
                    <SelectItem value="lottery">Lottery</SelectItem>
                    <SelectItem value="rotor">Rotor</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex items-end">
                <Button onClick={modelEditor.openCreate} className="h-8 text-xs shrink-0">
                  {t('actions.add_model')}
                </Button>
              </div>
            </div>
            {/* 排序怎么操作；筛着的时候同一行位置改说为什么不能排序 */}
            <p className="text-[11px] text-muted-foreground">
              {hasModelOverviewFilter ? t('order.blocked_by_filter') : t('order.hint')}
            </p>
          </div>
        )}

        {selectedModelId && (
          <div className="flex flex-col gap-2 flex-shrink-0">
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4 lg:gap-4">
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.provider_search')}</Label>
                <div className="relative">
                  <Search className="size-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    placeholder={t('filters.provider_search_placeholder')}
                    value={providerSearchInput}
                    onChange={(event) => setProviderSearchInput(event.target.value)}
                    className="h-8 pl-7 text-xs"
                  />
                </div>
              </div>
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.quick_switch')}</Label>
                <Select value={selectedModelId?.toString() || ""} onValueChange={(value) => handleModelSelect(Number(value))}>
                  <SelectTrigger className="h-8 w-full text-xs px-2">
                    <SelectValue placeholder={t('filters.model_placeholder')} />
                  </SelectTrigger>
                  <SelectContent>
                    {order.orderedModels.map((model) => (
                      <SelectItem key={model.ID} value={model.ID.toString()}>
                        {model.Name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.provider_type')}</Label>
                <Select value={selectedProviderType} onValueChange={setSelectedProviderType}>
                  <SelectTrigger className="h-8 w-full text-xs px-2">
                    <SelectValue placeholder={t('filters.provider_type_placeholder')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">{t('common:status.all')}</SelectItem>
                    {providerTypes.map((type) => (
                      <SelectItem key={type} value={type}>
                        {type}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1 text-xs">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.weight_sort')}</Label>
                <Select value={weightSortOrder} onValueChange={(value) => setWeightSortOrder(value as "asc" | "desc" | "none")}>
                  <SelectTrigger className="h-8 w-full text-xs px-2">
                    <SelectValue placeholder={t('filters.weight_sort')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">{t('filters.weight_sort_none')}</SelectItem>
                    <SelectItem value="asc">{t('filters.weight_sort_asc')}</SelectItem>
                    <SelectItem value="desc">{t('filters.weight_sort_desc')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </div>
        )}
      </div>

      {!selectedModelId && (
        <ModelList
          models={filteredOverviewModels}
          loading={loading}
          error={overviewError}
          onRetry={loadOverview}
          emptyText={hasModelOverviewFilter ? t('no_models_filtered') : t('no_models')}
          associationCountText={getAssociationCountNumberText}
          order={order}
          onSelect={handleModelSelect}
          onEdit={modelEditor.openEdit}
          onAddAssociation={openCreateDialog}
          onDelete={setModelDeleteId}
        />
      )}

      {selectedModelId && (
        <>
          {statusError && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {statusError}
            </div>
          )}
          <AssociationList
            associations={sortedModelProviders}
            providers={providers}
            providerStatus={providerStatus}
            statusUpdating={statusUpdating}
            deleteId={deleteId}
            loading={loading}
            error={associationsError}
            onRetry={() => fetchModelProviders(selectedModelId)}
            emptyText={hasAssociationFilter ? t('no_associations_filtered') : t('no_associations')}
            onRefreshStatus={() => loadProviderStatus(modelProviders, selectedModelId)}
            onEdit={openEditDialog}
            onTest={openTestDialog}
            onOpenDelete={openDeleteDialog}
            onCloseDelete={() => setDeleteId(null)}
            onConfirmDelete={handleDelete}
            onStatusToggle={handleStatusToggle}
          />
        </>
      )}

      <ModelDeleteDialog
        open={modelDeleteId !== null}
        model={modelPendingDelete}
        deleting={modelDeleteLoading}
        onClose={() => setModelDeleteId(null)}
        onConfirm={handleDeleteModel}
      />

      <ModelFormDialog
        open={modelEditor.open}
        editingModel={modelEditor.editingModel}
        saving={modelEditor.saving}
        onClose={modelEditor.close}
        onSubmit={modelEditor.submit}
      />

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
        peakTerms={peakTerms}
        setPeakTerms={setPeakTerms}
        peakSubmitAttempt={peakSubmitAttempt}
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
