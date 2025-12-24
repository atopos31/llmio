import { useState, useEffect, useRef } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Tooltip,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Checkbox } from "@/components/ui/checkbox";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
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
import Loading from "@/components/loading";
import { Label } from "@/components/ui/label";
import {
  getProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  getProviderTemplates,
  getProviderModels,
  refreshProviderModels,
  refreshAllProviderModels,
  getModelProvidersByProvider,
  deleteModelProvider,
  getModelOptions
} from "@/lib/api";
import type { Provider, ProviderTemplate, ProviderModel, ModelWithProvider, Model } from "@/lib/api";
import { toast } from "sonner";
import { ExternalLink, Pencil, Trash2, Boxes, RefreshCw, Eye, Wrench, FileJson, ChevronRight } from "lucide-react";

// 将模型列表按分组整理（使用后端返回的 group 字段）
const groupModels = (models: ProviderModel[]): Map<string, ProviderModel[]> => {
  const groups = new Map<string, ProviderModel[]>();
  
  for (const model of models) {
    // 使用后端返回的 group 字段，如果不存在则使用 "Other" 作为默认分组
    const groupName = model.group || 'Other';
    if (!groups.has(groupName)) {
      groups.set(groupName, []);
    }
    groups.get(groupName)!.push(model);
  }
  
  // 按分组名称排序
  return new Map([...groups.entries()].sort((a, b) => a[0].localeCompare(b[0])));
};

type ConfigFieldMap = Record<string, string>;

const parseConfigJson = (raw?: string | null): ConfigFieldMap | null => {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return null;
    }
    const entries: [string, string][] = [];
    for (const [key, value] of Object.entries(parsed)) {
      if (["string", "number", "boolean"].includes(typeof value)) {
        entries.push([key, String(value)]);
        continue;
      }
      return null;
    }
    return Object.fromEntries(entries);
  } catch {
    return null;
  }
};

const stringifyConfigFields = (fields: ConfigFieldMap) =>
  JSON.stringify(fields, null, 2);

const mergeTemplateWithConfig = (
  templateFields: ConfigFieldMap,
  existingConfig: ConfigFieldMap | null,
  preserveUnknownKeys: boolean
): ConfigFieldMap => {
  if (!existingConfig) {
    return templateFields;
  }

  if (preserveUnknownKeys) {
    return { ...templateFields, ...existingConfig };
  }

  const merged: ConfigFieldMap = {};
  for (const [key, value] of Object.entries(templateFields)) {
    merged[key] = Object.prototype.hasOwnProperty.call(existingConfig, key)
      ? existingConfig[key]
      : value;
  }
  return merged;
};

const getConfigBaseUrl = (config: string): string => {
  const parsed = parseConfigJson(config);
  return parsed?.base_url ?? "未设置";
};

// 定义表单验证模式
const formSchema = z.object({
  name: z.string().min(1, { message: "提供商名称不能为空" }),
  type: z.string().min(1, { message: "提供商类型不能为空" }),
  config: z.string().min(1, { message: "配置不能为空" }),
  console: z.string().optional(),
  sync_models: z.boolean(),
});

export default function ProvidersPage() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [providerTemplates, setProviderTemplates] = useState<ProviderTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [modelsOpen, setModelsOpen] = useState(false);
  const [modelsOpenId, setModelsOpenId] = useState<number | null>(null);
  const [providerModels, setProviderModels] = useState<ProviderModel[]>([]);
  const [filteredProviderModels, setFilteredProviderModels] = useState<ProviderModel[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsRefreshing, setModelsRefreshing] = useState(false);
  const [creating, setCreating] = useState(false);
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [configFields, setConfigFields] = useState<ConfigFieldMap>({});
  const [structuredConfigEnabled, setStructuredConfigEnabled] = useState(false);
  const configCacheRef = useRef<Record<string, ConfigFieldMap>>({});
  const [orphanedAssociations, setOrphanedAssociations] = useState<ModelWithProvider[]>([]);
  const [orphanedDialogOpen, setOrphanedDialogOpen] = useState(false);
  const [deletingOrphaned, setDeletingOrphaned] = useState(false);
  const [selectedOrphanedIds, setSelectedOrphanedIds] = useState<Set<number>>(new Set());

  // 筛选条件
  const [nameFilter, setNameFilter] = useState<string>("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [availableTypes, setAvailableTypes] = useState<string[]>([]);

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: { name: "", type: "", config: "", console: "", sync_models: true },
  });
  const selectedProviderType = form.watch("type");

  useEffect(() => {
    fetchProviders();
    fetchProviderTemplates();
    fetchModels();
  }, []);

  const fetchModels = async () => {
    try {
      const data = await getModelOptions();
      setModels(data);
    } catch (err) {
      console.error("获取模型列表失败", err);
    }
  };

  // 监听筛选条件变化
  useEffect(() => {
    fetchProviders();
  }, [nameFilter, typeFilter]);

  useEffect(() => {
    if (!open) {
      setStructuredConfigEnabled(false);
      setConfigFields({});
      configCacheRef.current = {};
      return;
    }

    if (!selectedProviderType) {
      setStructuredConfigEnabled(false);
      setConfigFields({});
      return;
    }

    const template = providerTemplates.find(
      (item) => item.type === selectedProviderType
    );

    if (!template) {
      setStructuredConfigEnabled(false);
      setConfigFields({});
      return;
    }

    const templateFields = parseConfigJson(template.template);
    if (!templateFields) {
      setStructuredConfigEnabled(false);
      setConfigFields({});
      return;
    }

    let nextFields = configCacheRef.current[selectedProviderType];

    if (!nextFields && editingProvider && editingProvider.Type === selectedProviderType) {
      const editingConfig = parseConfigJson(editingProvider.Config);
      if (editingConfig) {
        nextFields = mergeTemplateWithConfig(templateFields, editingConfig, true);
      }
    }

    if (!nextFields) {
      nextFields = templateFields;
    }

    configCacheRef.current[selectedProviderType] = nextFields;
    setConfigFields(nextFields);
    setStructuredConfigEnabled(true);
    form.setValue("config", stringifyConfigFields(nextFields));
  }, [
    open,
    selectedProviderType,
    providerTemplates,
    editingProvider,
    form,
  ]);

  const fetchProviders = async () => {
    try {
      setLoading(true);
      // 处理筛选条件，"all"表示不过滤，空字符串表示不过滤
      const name = nameFilter.trim() || undefined;
      const type = typeFilter === "all" ? undefined : typeFilter;

      const data = await getProviders({ name, type });
      setProviders(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取提供商列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const fetchProviderTemplates = async () => {
    try {
      const data = await getProviderTemplates();
      setProviderTemplates(data);
      const types = data.map((template) => template.type);
      setAvailableTypes(types);

      if (!form.getValues("type") && types.length > 0) {
        const firstType = types[0];
        form.setValue("type", firstType);
        const firstTemplate = data.find((item) => item.type === firstType);
        if (firstTemplate) {
          const parsed = parseConfigJson(firstTemplate.template);
          if (parsed) {
            form.setValue("config", stringifyConfigFields(parsed));
          } else {
            form.setValue("config", firstTemplate.template);
          }
        }
      }
    } catch (err) {
      console.error("获取提供商模板失败", err);
    }
  };

  const fetchProviderModels = async (providerId: number) => {
    try {
      setModelsLoading(true);
      const data = await getProviderModels(providerId);
      setProviderModels(data);
      setFilteredProviderModels(data);
      // 默认展开所有分组
      const groups = groupModels(data);
      setExpandedGroups(new Set(groups.keys()));
    } catch (err) {
      console.error("获取提供商模型失败", err);
      setProviderModels([]);
      setFilteredProviderModels([]);
    } finally {
      setModelsLoading(false);
    }
  };

  const toggleGroup = (groupName: string) => {
    setExpandedGroups(prev => {
      const next = new Set(prev);
      if (next.has(groupName)) {
        next.delete(groupName);
      } else {
        next.add(groupName);
      }
      return next;
    });
  };

  const toggleAllGroups = (expand: boolean) => {
    if (expand) {
      const groups = groupModels(filteredProviderModels);
      setExpandedGroups(new Set(groups.keys()));
    } else {
      setExpandedGroups(new Set());
    }
  };

  const openModelsDialog = async (providerId: number) => {
    setModelsOpen(true);
    setModelsOpenId(providerId);
    await fetchProviderModels(providerId);
  };

  const handleRefreshModels = async () => {
    if (!modelsOpenId) return;
    try {
      setModelsRefreshing(true);
      
      // 先获取该提供商的所有模型关联
      const associations = await getModelProvidersByProvider(modelsOpenId);
      
      // 刷新模型列表
      const data = await refreshProviderModels(modelsOpenId);
      
      // 检查是否有已关联的模型在新列表中不存在
      const newModelIds = new Set(data.map(m => m.id));
      const orphaned = associations.filter(assoc => !newModelIds.has(assoc.ProviderModel));
      
      if (orphaned.length > 0) {
        // 有失效的关联，显示确认对话框
        setOrphanedAssociations(orphaned);
        // 默认全选
        setSelectedOrphanedIds(new Set(orphaned.map(a => a.ID)));
        setOrphanedDialogOpen(true);
      } else {
        toast.success("模型列表刷新成功");
      }
      
      setProviderModels(data);
      setFilteredProviderModels(data);
      
      // 刷新提供商列表以更新缓存的模型数量
      fetchProviders();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`刷新模型列表失败: ${message}`);
    } finally {
      setModelsRefreshing(false);
    }
  };

  const handleDeleteOrphanedAssociations = async () => {
    if (selectedOrphanedIds.size === 0) {
      toast.warning("请至少选择一个要删除的关联");
      return;
    }

    try {
      setDeletingOrphaned(true);
      let successCount = 0;
      let failCount = 0;
      
      const toDelete = orphanedAssociations.filter(a => selectedOrphanedIds.has(a.ID));
      
      for (const assoc of toDelete) {
        try {
          await deleteModelProvider(assoc.ID);
          successCount++;
        } catch (err) {
          console.error(`删除关联失败 ${assoc.ID}:`, err);
          failCount++;
        }
      }
      
      if (failCount === 0) {
        toast.success(`成功删除 ${successCount} 个失效的模型关联`);
      } else {
        toast.warning(`删除完成: ${successCount} 成功, ${failCount} 失败`);
      }
      
      setOrphanedDialogOpen(false);
      setOrphanedAssociations([]);
      setSelectedOrphanedIds(new Set());
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除失效关联失败: ${message}`);
    } finally {
      setDeletingOrphaned(false);
    }
  };

  const toggleOrphanedSelection = (id: number) => {
    setSelectedOrphanedIds(prev => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const toggleAllOrphanedSelection = () => {
    if (selectedOrphanedIds.size === orphanedAssociations.length) {
      setSelectedOrphanedIds(new Set());
    } else {
      setSelectedOrphanedIds(new Set(orphanedAssociations.map(a => a.ID)));
    }
  };

  const copyModelName = async (modelName: string) => {
    await navigator.clipboard.writeText(modelName);
    toast.success(`已复制模型名称: ${modelName}`);
  };

  const handleConfigFieldChange = (key: string, value: string) => {
    setConfigFields((prev) => {
      const updatedFields = { ...prev, [key]: value };
      if (selectedProviderType) {
        configCacheRef.current[selectedProviderType] = updatedFields;
      }
      form.setValue("config", stringifyConfigFields(updatedFields), {
        shouldDirty: true,
        shouldValidate: true,
      });
      return updatedFields;
    });
  };

  const handleCreate = async (values: z.infer<typeof formSchema>) => {
    try {
      setCreating(true);
      await createProvider({
        name: values.name,
        type: values.type,
        config: values.config,
        console: values.console || "",
        sync_models: values.sync_models
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 创建成功`);
      form.reset({ name: "", type: "", config: "", console: "", sync_models: true });
      fetchProviders();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`创建提供商失败: ${message}`);
      console.error(err);
    } finally {
      setCreating(false);
    }
  };

  const handleUpdate = async (values: z.infer<typeof formSchema>) => {
    if (!editingProvider) return;
    try {
      await updateProvider(editingProvider.ID, {
        name: values.name,
        type: values.type,
        config: values.config,
        console: values.console || ""
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 更新成功`);
      setEditingProvider(null);
      form.reset({ name: "", type: "", config: "", console: "", sync_models: true });
      fetchProviders();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`更新提供商失败: ${message}`);
      console.error(err);
    }
  };

  const handleDelete = async () => {
    if (!deleteId) return;
    try {
      const targetProvider = providers.find((provider) => provider.ID === deleteId);
      await deleteProvider(deleteId);
      setDeleteId(null);
      fetchProviders();
      toast.success(`提供商 ${targetProvider?.Name ?? deleteId} 删除成功`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除提供商失败: ${message}`);
      console.error(err);
    }
  };

  const openEditDialog = (provider: Provider) => {
    configCacheRef.current = {};
    setEditingProvider(provider);
    form.reset({
      name: provider.Name,
      type: provider.Type,
      config: provider.Config,
      console: provider.Console || "",
      sync_models: true,
    });
    setOpen(true);
  };

  const handleRefreshAllModels = async () => {
    try {
      setRefreshingAll(true);
      const result = await refreshAllProviderModels();
      if (result.failed === 0) {
        toast.success(`成功刷新 ${result.success} 个提供商的模型列表`);
      } else {
        toast.warning(`刷新完成: ${result.success} 成功, ${result.failed} 失败`);
        // 显示失败详情
        result.details
          .filter(d => !d.success)
          .forEach(d => {
            toast.error(`${d.provider_name}: ${d.error}`);
          });
      }
      // 刷新提供商列表以更新缓存的模型数量
      fetchProviders();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`刷新所有提供商模型失败: ${message}`);
    } finally {
      setRefreshingAll(false);
    }
  };

  const openCreateDialog = () => {
    configCacheRef.current = {};
    if (providerTemplates.length === 0) {
      toast.error("暂无可用的提供商模板");
      return;
    }
    setEditingProvider(null);
    const firstTemplate = providerTemplates[0];
    const defaultType = firstTemplate?.type ?? "";
    const defaultConfig = firstTemplate
      ? (() => {
        const parsed = parseConfigJson(firstTemplate.template);
        return parsed ? stringifyConfigFields(parsed) : firstTemplate.template;
      })()
      : "";
    form.reset({
      name: "",
      type: defaultType,
      config: defaultConfig,
      console: "",
      sync_models: true,
    });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const hasFilter = nameFilter.trim() !== "" || typeFilter !== "all";

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">提供商管理</h2>
          </div>
          <div className="flex w-full sm:w-auto items-center justify-end gap-2">
          </div>
        </div>
      </div>
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:gap-4">
          <div className="flex flex-col gap-1 text-xs">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">提供商名称</Label>
            <Input
              placeholder="输入名称"
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              className="h-8 w-full text-xs px-2"
            />
          </div>
          <div className="flex flex-col gap-1 text-xs">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">类型</Label>
            <Select value={typeFilter} onValueChange={setTypeFilter}>
              <SelectTrigger className="h-8 w-full text-xs px-2">
                <SelectValue placeholder="选择类型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {availableTypes.map((type) => (
                  <SelectItem key={type} value={type}>
                    {type}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex items-end col-span-2 sm:col-span-1 sm:justify-end gap-2">
            <Button
              onClick={openCreateDialog}
              className="h-8 text-xs"
              disabled={providerTemplates.length === 0}
            >
              添加提供商
            </Button>
            <Button
              variant="outline"
              onClick={handleRefreshAllModels}
              className="h-8 text-xs"
              disabled={refreshingAll || providers.length === 0}
            >
              {refreshingAll ? (
                <>
                  <RefreshCw className="mr-1 h-3 w-3 animate-spin" />
                  刷新中...
                </>
              ) : (
                <>
                  <RefreshCw className="mr-1 h-3 w-3" />
                  刷新所有模型
                </>
              )}
            </Button>
          </div>
        </div>
      </div>
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载提供商列表" />
          </div>
        ) : providers.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground text-sm text-center px-6">
            {hasFilter ? '未找到匹配的提供商' : '暂无提供商数据'}
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="hidden sm:block flex-1 overflow-y-auto">
              <div className="w-full">
                <Table className="min-w-[1200px]">
                  <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                    <TableRow>
                      <TableHead>ID</TableHead>
                      <TableHead>名称</TableHead>
                      <TableHead>类型</TableHead>
                      <TableHead>配置</TableHead>
                      <TableHead>控制台</TableHead>
                      <TableHead>操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {providers.map((provider) => (
                      <TableRow key={provider.ID}>
                        <TableCell className="font-mono text-xs text-muted-foreground">{provider.ID}</TableCell>
                        <TableCell className="font-medium">{provider.Name}</TableCell>
                        <TableCell className="text-sm">{provider.Type}</TableCell>
                        <TableCell className="text-xs text-muted-foreground font-mono">
                          {getConfigBaseUrl(provider.Config)}
                        </TableCell>
                        <TableCell>
                          {provider.Console ? (
                            <Button
                              title={provider.Console}
                              variant="ghost"
                              size="icon"
                              onClick={() => window.open(provider.Console, '_blank')}
                            >
                              <ExternalLink className="h-2 w-2" />
                            </Button>
                          ) : (
                            <Button variant="ghost" size="icon" disabled>
                              <ExternalLink className="h-2 w-2 opacity-50" />
                            </Button>
                          )}
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-2">
                            <Button variant="outline" size="icon" onClick={() => openEditDialog(provider)}>
                              <Pencil className="h-4 w-4" />
                            </Button>
                            <Button variant="secondary" size="icon" onClick={() => openModelsDialog(provider.ID)}>
                              <Boxes className="h-4 w-4" />
                            </Button>
                            <AlertDialog>
                              <AlertDialogTrigger asChild>
                                <Button variant="destructive" size="icon" onClick={() => openDeleteDialog(provider.ID)}>
                                  <Trash2 className="h-4 w-4" />
                                </Button>
                              </AlertDialogTrigger>
                              <AlertDialogContent>
                                <AlertDialogHeader>
                                  <AlertDialogTitle>确定要删除这个提供商吗？</AlertDialogTitle>
                                  <AlertDialogDescription>
                                    此操作无法撤销。这将永久删除该提供商。
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
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
            <div className="sm:hidden flex-1 min-h-0 overflow-y-auto px-2 py-3 divide-y divide-border">
              {providers.map((provider) => (
                <div key={provider.ID} className="py-3 space-y-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1">
                        <h3 className="font-semibold text-sm truncate">{provider.Name}</h3>
                        {provider.Console ? (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-5 w-5"
                            onClick={() => window.open(provider.Console, '_blank')}
                          >
                            <ExternalLink className="h-2.5 w-2.5" />
                          </Button>
                        ) : (
                          <Button variant="ghost" size="icon" disabled className="h-5 w-5">
                            <ExternalLink className="h-2.5 w-2.5 opacity-50" />
                          </Button>
                        )}
                      </div>
                      <div className="flex items-center gap-2">
                        <p className="text-[11px] text-muted-foreground">ID: {provider.ID}</p>
                        <p className="text-[11px] text-muted-foreground">类型: {provider.Type || "未知"}</p>
                      </div>
                    </div>
                    <div className="flex flex-wrap justify-end gap-1.5">
                      <Button variant="outline" size="icon" className="h-7 w-7" onClick={() => openEditDialog(provider)}>
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button variant="secondary" size="icon" className="h-7 w-7" onClick={() => openModelsDialog(provider.ID)}>
                        <Boxes className="h-3.5 w-3.5" />
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button variant="destructive" size="icon" className="h-7 w-7" onClick={() => openDeleteDialog(provider.ID)}>
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>确定要删除这个提供商吗？</AlertDialogTitle>
                            <AlertDialogDescription>
                              此操作无法撤销。这将永久删除该提供商。
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
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingProvider ? "编辑提供商" : "添加提供商"}
            </DialogTitle>
            <DialogDescription>
              {editingProvider
                ? "修改提供商信息"
                : "添加一个新的提供商"}
            </DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(editingProvider ? handleUpdate : handleCreate)} className="space-y-4 min-w-0">
              <FormField
                control={form.control}
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
                control={form.control}
                name="type"
                render={({ field }) => {
                  const currentValue = field.value ?? "";
                  const hasCurrentValue = providerTemplates.some(
                    (template) => template.type === currentValue
                  );
                  const templateOptions =
                    !hasCurrentValue && currentValue
                      ? [
                        ...providerTemplates,
                        {
                          type: currentValue,
                          template: "",
                        } as ProviderTemplate,
                      ]
                      : providerTemplates;

                  return (
                    <FormItem>
                      <FormLabel>类型</FormLabel>
                      <FormControl>
                        {providerTemplates.length === 0 ? (
                          <p className="text-sm text-muted-foreground">
                            暂无可用类型，请先配置模板。
                          </p>
                        ) : (
                          <RadioGroup
                            value={currentValue}
                            onValueChange={(value) => field.onChange(value)}
                            className="flex flex-wrap gap-2"
                          >
                            {templateOptions.map((template) => {
                              const radioId = `provider-type-${template.type}`;
                              const selected = currentValue === template.type;
                              return (
                                <label
                                  key={template.type}
                                  htmlFor={radioId}
                                  className={`flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm ${selected
                                      ? "border-primary bg-primary/10"
                                      : "border-border"
                                    }`}
                                >
                                  <RadioGroupItem
                                    id={radioId}
                                    value={template.type}
                                    className="sr-only"
                                  />
                                  <Checkbox
                                    checked={selected}
                                    aria-hidden="true"
                                    tabIndex={-1}
                                    className="pointer-events-none"
                                  />
                                  <span className="select-none">{template.type}</span>
                                </label>
                              );
                            })}
                          </RadioGroup>
                        )}
                      </FormControl>
                      {!hasCurrentValue && currentValue && (
                        <p className="text-xs text-muted-foreground">
                          当前提供商类型{" "}
                          <span className="font-mono">{currentValue}</span>{" "}
                          不在模板列表中，可继续使用或选择其他类型。
                        </p>
                      )}
                      <FormMessage />
                    </FormItem>
                  );
                }}
              />

              <FormField
                control={form.control}
                name="config"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>配置</FormLabel>
                    {structuredConfigEnabled ? (
                      Object.keys(configFields).length === 0 ? (
                        <p className="text-sm text-muted-foreground">
                          当前提供商类型暂无额外配置。
                        </p>
                      ) : (
                        <div className="space-y-3">
                          {Object.entries(configFields).map(([key, value]) => (
                            <div key={key} className="space-y-1">
                              <Label className="text-xs font-medium text-muted-foreground">
                                {key}
                              </Label>
                              <Input
                                value={value}
                                onChange={(event) =>
                                  handleConfigFieldChange(key, event.target.value)
                                }
                                placeholder={`请输入 ${key}`}
                              />
                            </div>
                          ))}
                        </div>
                      )
                    ) : (
                      <FormControl>
                        <Textarea
                          {...field}
                          className="resize-none whitespace-pre overflow-x-auto"
                        />
                      </FormControl>
                    )}
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="console"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>控制台地址</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="https://example.com/console" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {!editingProvider && (
                <FormField
                  control={form.control}
                  name="sync_models"
                  render={({ field }) => (
                    <FormItem className="flex flex-row items-start space-x-3 space-y-0 rounded-md border p-4">
                      <FormControl>
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                      <div className="space-y-1 leading-none">
                        <FormLabel>
                          同步拉取模型列表
                        </FormLabel>
                        <p className="text-sm text-muted-foreground">
                          创建时自动从提供商拉取可用模型列表
                        </p>
                      </div>
                    </FormItem>
                  )}
                />
              )}

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setOpen(false)} disabled={creating}>
                  取消
                </Button>
                <Button type="submit" disabled={creating}>
                  {creating && form.watch("sync_models") ? (
                    <>
                      <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
                      正在拉取模型列表...
                    </>
                  ) : (
                    editingProvider ? "更新" : "创建"
                  )}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

      {/* 模型列表对话框 */}
      <Dialog open={modelsOpen} onOpenChange={setModelsOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{providers.find(v => v.ID === modelsOpenId)?.Name}模型列表</DialogTitle>
            <DialogDescription>
              当前提供商的所有可用模型
            </DialogDescription>
          </DialogHeader>

          {/* 搜索框 */}
          {!modelsLoading && providerModels.length > 0 && (
            <div className="mb-4">
              <Input
                placeholder="搜索模型 ID"
                onChange={(e) => {
                  const searchTerm = e.target.value.toLowerCase();
                  if (searchTerm === '') {
                    setFilteredProviderModels(providerModels);
                  } else {
                    const filteredModels = providerModels.filter(model =>
                      model.id.toLowerCase().includes(searchTerm)
                    );
                    setFilteredProviderModels(filteredModels);
                  }
                }}
                className="w-full"
              />
            </div>
          )}

          {modelsLoading ? (
            <Loading message="加载模型列表" />
          ) : (
            <div className="max-h-96 overflow-y-auto">
              {filteredProviderModels.length === 0 ? (
                <div className="text-center text-gray-500 py-8">
                  {providerModels.length === 0 ? '暂无模型数据' : '未找到匹配的模型'}
                </div>
              ) : (
                <div className="space-y-1 pb-2">
                  {/* 展开/折叠全部按钮 */}
                  <div className="flex justify-end gap-2 mb-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 text-xs"
                      onClick={() => toggleAllGroups(true)}
                    >
                      展开全部
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 text-xs"
                      onClick={() => toggleAllGroups(false)}
                    >
                      折叠全部
                    </Button>
                  </div>
                  {Array.from(groupModels(filteredProviderModels).entries()).map(([groupName, models]) => {
                    const isCollapsed = !expandedGroups.has(groupName);
                    return (
                      <div key={groupName} className={`border rounded-lg overflow-hidden ${isCollapsed ? 'mb-2' : 'mb-2'}`}>
                        {/* 分组标题 */}
                        <button
                          type="button"
                          className={`w-full flex items-center justify-between px-3 py-2 bg-muted/50 hover:bg-muted transition-colors ${isCollapsed ? 'rounded-lg' : 'rounded-t-lg'}`}
                          onClick={() => toggleGroup(groupName)}
                        >
                          <div className="flex items-center gap-2">
                            <ChevronRight
                              className={`h-4 w-4 text-muted-foreground transition-transform ${expandedGroups.has(groupName) ? 'rotate-90' : ''}`}
                            />
                            <span className="font-bold text-sm">{groupName}</span>
                            <span className="inline-flex items-center justify-center px-2 py-0.5 text-xs font-medium rounded-full bg-green-100 text-green-700">
                              {models.length}
                            </span>
                          </div>
                        </button>
                        {/* 分组内容 */}
                        {expandedGroups.has(groupName) && (
                          <div className="border-t">
                            {models.map((model, index) => (
                              <div
                                key={index}
                                className={`flex items-center justify-between pl-8 pr-3 py-2 gap-2 hover:bg-muted/30 ${index === models.length - 1 ? '' : 'border-b'}`}
                              >
                                <div className="flex-1 min-w-0 flex items-center gap-2">
                                  <div className="font-medium text-sm truncate">{model.id}</div>
                                  {model.capabilities && (
                                    <div className="flex gap-1 flex-shrink-0">
                                      {model.capabilities.vision && (
                                        <span className="inline-flex items-center justify-center h-5 w-5 rounded bg-blue-100 text-blue-600" title="视觉">
                                          <Eye className="h-3 w-3" />
                                        </span>
                                      )}
                                      {model.capabilities.function_calling && (
                                        <span className="inline-flex items-center justify-center h-5 w-5 rounded bg-amber-100 text-amber-600" title="工具调用">
                                          <Wrench className="h-3 w-3" />
                                        </span>
                                      )}
                                      {model.capabilities.structured_output && (
                                        <span className="inline-flex items-center justify-center h-5 w-5 rounded bg-green-100 text-green-600" title="结构化输出">
                                          <FileJson className="h-3 w-3" />
                                        </span>
                                      )}
                                    </div>
                                  )}
                                </div>
                                <TooltipProvider>
                                  <Tooltip>
                                    <TooltipTrigger asChild>
                                      <Button
                                        variant="outline"
                                        size="sm"
                                        onClick={() => copyModelName(model.id)}
                                        className="min-w-10 h-7 flex-shrink-0"
                                      >
                                        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth="2" stroke="currentColor" aria-hidden="true" className="h-3.5 w-3.5"><path strokeLinecap="round" strokeLinejoin="round" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3m2 4H10m0 0l3-3m-3 3l3 3"></path></svg>
                                      </Button>
                                    </TooltipTrigger>
                                  </Tooltip>
                                </TooltipProvider>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          )}

          <DialogFooter className="flex justify-between sm:justify-between">
            <Button
              variant="outline"
              onClick={handleRefreshModels}
              disabled={modelsRefreshing}
            >
              {modelsRefreshing ? (
                <>
                  <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
                  刷新中...
                </>
              ) : (
                <>
                  <RefreshCw className="mr-2 h-4 w-4" />
                  刷新模型列表
                </>
              )}
            </Button>
            <Button onClick={() => setModelsOpen(false)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 失效模型关联确认对话框 */}
      <AlertDialog open={orphanedDialogOpen} onOpenChange={(open) => {
        setOrphanedDialogOpen(open);
        if (!open) {
          setSelectedOrphanedIds(new Set());
        }
      }}>
        <AlertDialogContent className="max-w-2xl">
          <AlertDialogHeader>
            <AlertDialogTitle>发现失效的模型关联</AlertDialogTitle>
            <AlertDialogDescription>
              刷新后，以下 {orphanedAssociations.length} 个已关联的模型在提供商中已不存在。请选择要删除的关联（某些供应商可能临时移除模型后又会添加回来）。
            </AlertDialogDescription>
          </AlertDialogHeader>
          
          <div className="space-y-2">
            <div className="flex items-center justify-between px-3 py-2 bg-muted/30 rounded">
              <div className="flex items-center gap-2">
                <Checkbox
                  checked={selectedOrphanedIds.size === orphanedAssociations.length && orphanedAssociations.length > 0}
                  onCheckedChange={toggleAllOrphanedSelection}
                  ref={(el) => {
                    if (el) {
                      // @ts-ignore
                      el.indeterminate = selectedOrphanedIds.size > 0 && selectedOrphanedIds.size < orphanedAssociations.length;
                    }
                  }}
                />
                <span className="text-sm font-medium">全选 / 取消全选</span>
              </div>
              <span className="text-xs text-muted-foreground">
                已选择 {selectedOrphanedIds.size} / {orphanedAssociations.length}
              </span>
            </div>

            <div className="max-h-[300px] overflow-y-auto border rounded-md p-2">
              <div className="space-y-1">
                {orphanedAssociations.map((assoc) => {
                  const provider = providers.find(p => p.ID === assoc.ProviderID);
                  const model = models.find(m => m.ID === assoc.ModelID);
                  const isSelected = selectedOrphanedIds.has(assoc.ID);
                  
                  return (
                    <div
                      key={assoc.ID}
                      className={`flex items-center gap-2 p-2 rounded text-sm cursor-pointer hover:bg-muted/50 ${isSelected ? 'bg-muted/30' : ''}`}
                      onClick={() => toggleOrphanedSelection(assoc.ID)}
                    >
                      <Checkbox
                        checked={isSelected}
                        onCheckedChange={() => toggleOrphanedSelection(assoc.ID)}
                        onClick={(e) => e.stopPropagation()}
                      />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium truncate">
                          {model?.Name || `模型ID: ${assoc.ModelID}`}
                        </div>
                        <div className="text-xs text-muted-foreground truncate">
                          {provider?.Name || `提供商ID: ${assoc.ProviderID}`} → {assoc.ProviderModel}
                        </div>
                      </div>
                      <span className="ml-2 text-xs text-red-600 flex-shrink-0">不存在</span>
                    </div>
                  );
                })}
              </div>
            </div>
          </div>

          <AlertDialogFooter>
            <AlertDialogCancel disabled={deletingOrphaned}>
              关闭
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDeleteOrphanedAssociations}
              disabled={deletingOrphaned || selectedOrphanedIds.size === 0}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deletingOrphaned ? (
                <>
                  <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
                  删除中...
                </>
              ) : (
                `删除选中的 ${selectedOrphanedIds.size} 个关联`
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
