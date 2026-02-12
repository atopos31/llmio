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
import { Input } from "@/components/ui/input";
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
  deleteProvider,
  getProviderTemplates,
  getProviderModels
} from "@/lib/api";
import type { Provider, ProviderTemplate, ProviderModel } from "@/lib/api";
import { toast } from "sonner";
import { ExternalLink, Pencil, Trash2, Boxes } from "lucide-react";
import { ProviderFormDialog } from "@/routes/providers/provider-form-dialog";
import { ProviderModelsDialog } from "@/routes/providers/provider-models-dialog";
import { useProviderForm } from "@/routes/providers/use-provider-form";
import { getConfigBaseUrl } from "@/routes/providers/provider-form-utils";

export default function ProvidersPage() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerTemplates, setProviderTemplates] = useState<ProviderTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [modelsOpen, setModelsOpen] = useState(false);
  const [modelsOpenId, setModelsOpenId] = useState<number | null>(null);
  const [providerModels, setProviderModels] = useState<ProviderModel[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);

  // 筛选条件
  const [nameFilter, setNameFilter] = useState<string>("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [availableTypes, setAvailableTypes] = useState<string[]>([]);

  const {
    form,
    open,
    setOpen,
    editingProvider,
    structuredConfigEnabled,
    configFields,
    openEditDialog,
    openCreateDialog,
    handleConfigFieldChange,
    submit,
  } = useProviderForm({
    providerTemplates,
    refreshProviders: () => fetchProviders(),
  });

  useEffect(() => {
    fetchProviders();
    fetchProviderTemplates();
  }, []);

  // 监听筛选条件变化
  useEffect(() => {
    fetchProviders();
  }, [nameFilter, typeFilter]);

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
    } catch (err) {
      console.error("获取提供商模板失败", err);
    }
  };

  const fetchProviderModels = async (providerId: number) => {
    try {
      setModelsLoading(true);
      const data = await getProviderModels(providerId);
      setProviderModels(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取提供商模型失败: ${message}`);
      setProviderModels([]);
    } finally {
      setModelsLoading(false);
    }
  };

  const openModelsDialog = async (providerId: number) => {
    setModelsOpen(true);
    setModelsOpenId(providerId);
    await fetchProviderModels(providerId);
  };

  const copyModelName = async (modelName: string) => {
    await navigator.clipboard.writeText(modelName);
    toast.success(`已复制模型名称: ${modelName}`);
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
          <div className="flex items-end col-span-2 sm:col-span-1 sm:justify-end">
            <Button
              onClick={openCreateDialog}
              className="h-8 w-full text-xs sm:w-auto sm:ml-auto"
              disabled={providerTemplates.length === 0}
            >
              添加提供商
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

      <ProviderFormDialog
        open={open}
        onOpenChange={setOpen}
        form={form}
        editingProvider={editingProvider}
        providerTemplates={providerTemplates}
        structuredConfigEnabled={structuredConfigEnabled}
        configFields={configFields}
        onConfigFieldChange={handleConfigFieldChange}
        onSubmit={submit}
      />

      <ProviderModelsDialog
        open={modelsOpen}
        onOpenChange={setModelsOpen}
        providerId={modelsOpenId ?? undefined}
        providerName={providers.find((v) => v.ID === modelsOpenId)?.Name}
        modelsLoading={modelsLoading}
        providerModels={providerModels}
        onCopyModelName={copyModelName}
      />
    </div>
  );
}
