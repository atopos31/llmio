import { useState, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
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
  getProviderModels,
  getProviderUsage
} from "@/lib/api";
import type { Provider, ProviderTemplate, ProviderModel, ProviderUsage, UsageWindow } from "@/lib/api";
import { copyToClipboard } from "@/lib/utils";
import { toast } from "sonner";
import { ExternalLink, Pencil, Trash2, Boxes, RefreshCw, TriangleAlert } from "lucide-react";
import { ProviderFormDialog } from "@/routes/providers/provider-form-dialog";
import { ProviderModelsDialog } from "@/routes/providers/provider-models-dialog";
import { useProviderForm } from "@/routes/providers/use-provider-form";
import { getConfigBaseUrl } from "@/routes/providers/provider-form-utils";

// 用量百分比保留一位小数，去掉无意义的 .0
const formatUsagePercent = (window: UsageWindow) => {
  const percent = Math.round(window.percent * 10) / 10;
  return `${percent}%`;
};

export default function ProvidersPage() {
  const { t } = useTranslation(['providers', 'common']);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerTemplates, setProviderTemplates] = useState<ProviderTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [modelsOpen, setModelsOpen] = useState(false);
  const [modelsOpenId, setModelsOpenId] = useState<number | null>(null);
  const [providerModels, setProviderModels] = useState<ProviderModel[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [usageMap, setUsageMap] = useState<Record<number, ProviderUsage>>({});
  const [usageLoading, setUsageLoading] = useState(false);
  const usageRequestedRef = useRef<Set<number>>(new Set());

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
      // 首屏/新增的供应商补拉用量；已在 ref 中记录过的会被跳过，避免筛选时重复打上游
      void fetchUsages(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('toast.fetch_failed', { message }));
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  // 并发拉取列表内每个供应商的用量；单个失败只影响该行，不拖垮整页。
  // usageRequestedRef 记录已发起过请求的 provider，避免筛选框每敲一次键都重打上游
  // （列表会被 name 筛选高频刷新，用量属于上游限额资源，只在首次与手动刷新时拉取）。
  const fetchUsages = async (list: Provider[], force = false) => {
    if (force) {
      usageRequestedRef.current.clear();
    }
    const targets = list.filter((provider) => !usageRequestedRef.current.has(provider.ID));
    if (targets.length === 0) {
      return;
    }
    targets.forEach((provider) => usageRequestedRef.current.add(provider.ID));

    setUsageLoading(true);
    try {
      const next: Record<number, ProviderUsage> = {};
      await Promise.all(
        targets.map(async (provider) => {
          try {
            next[provider.ID] = await getProviderUsage(provider.ID);
          } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            next[provider.ID] = { supported: true, error: message };
          }
        })
      );
      setUsageMap((prev) => ({ ...prev, ...next }));
    } finally {
      setUsageLoading(false);
    }
  };

  const renderUsageCell = (usage: ProviderUsage | undefined, key: "rolling" | "weekly" | "monthly") => {
    if (!usage) {
      return <span className="text-muted-foreground">{usageLoading ? "…" : "-"}</span>;
    }
    if (!usage.supported) {
      return (
        <span className="text-muted-foreground" title={t('usage.unsupported')}>
          -
        </span>
      );
    }
    if (usage.error) {
      return (
        <span className="inline-flex items-center text-destructive" title={usage.error}>
          <TriangleAlert className="h-3.5 w-3.5" />
        </span>
      );
    }
    const window = usage[key];
    if (!window) {
      return <span className="text-muted-foreground">-</span>;
    }
    const title = window.resets_at
      ? `${t('usage.resets_at')}: ${new Date(window.resets_at).toLocaleString()}`
      : undefined;
    return (
      <span className={window.status === "exceeded" ? "font-medium text-destructive" : "font-medium"} title={title}>
        {formatUsagePercent(window)}
      </span>
    );
  };

  const fetchProviderTemplates = async () => {
    try {
      const data = await getProviderTemplates();
      setProviderTemplates(data);
      const types = data.map((template) => template.type);
      setAvailableTypes(types);
    } catch (err) {
      console.error("fetch provider templates failed", err);
    }
  };

  const fetchProviderModels = async (providerId: number) => {
    try {
      setModelsLoading(true);
      const data = await getProviderModels(providerId);
      setProviderModels(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('toast.fetch_model_failed', { message }));
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
    try {
      await copyToClipboard(modelName);
      toast.success(t('toast.copy_model', { name: modelName }));
    } catch {
      toast.error(t('toast.copy_failed'));
    }
  };

  const handleDelete = async () => {
    if (!deleteId) return;
    try {
      const targetProvider = providers.find((provider) => provider.ID === deleteId);
      await deleteProvider(deleteId);
      setDeleteId(null);
      fetchProviders();
      toast.success(t('toast.delete_success', { name: targetProvider?.Name ?? deleteId }));
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(t('toast.delete_failed', { message }));
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
            <h2 className="text-2xl font-bold tracking-tight">{t('title')}</h2>
          </div>
          <div className="flex w-full sm:w-auto items-center justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => fetchUsages(providers, true)}
              disabled={usageLoading || providers.length === 0}
              className="h-8 gap-1.5 text-xs"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${usageLoading ? "animate-spin" : ""}`} />
              {t('actions.refresh')}
            </Button>
          </div>
        </div>
      </div>
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:gap-4">
          <div className="flex flex-col gap-1 text-xs">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.name')}</Label>
            <Input
              placeholder={t('filters.name_placeholder')}
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              className="h-8 w-full text-xs px-2"
            />
          </div>
          <div className="flex flex-col gap-1 text-xs">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">{t('filters.type')}</Label>
            <Select value={typeFilter} onValueChange={setTypeFilter}>
              <SelectTrigger className="h-8 w-full text-xs px-2">
                <SelectValue placeholder={t('filters.type_placeholder')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t('common:status.all')}</SelectItem>
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
              {t('actions.add')}
            </Button>
          </div>
        </div>
      </div>
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message={t('loading')} />
          </div>
        ) : providers.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground text-sm text-center px-6">
            {hasFilter ? t('no_match') : t('no_data')}
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="hidden sm:block flex-1 overflow-y-auto">
              <div className="w-full">
                <Table className="min-w-[1500px]">
                  <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                    <TableRow>
                      <TableHead>{t('table.id')}</TableHead>
                      <TableHead>{t('table.name')}</TableHead>
                      <TableHead>{t('table.type')}</TableHead>
                      <TableHead>{t('table.config')}</TableHead>
                      <TableHead>{t('table.console')}</TableHead>
                      <TableHead>{t('table.rolling')}</TableHead>
                      <TableHead>{t('table.weekly')}</TableHead>
                      <TableHead>{t('table.monthly')}</TableHead>
                      <TableHead>{t('table.actions')}</TableHead>
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
                        <TableCell className="text-xs">{renderUsageCell(usageMap[provider.ID], "rolling")}</TableCell>
                        <TableCell className="text-xs">{renderUsageCell(usageMap[provider.ID], "weekly")}</TableCell>
                        <TableCell className="text-xs">{renderUsageCell(usageMap[provider.ID], "monthly")}</TableCell>
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
                                  <AlertDialogTitle>{t('delete_dialog.title')}</AlertDialogTitle>
                                  <AlertDialogDescription>
                                    {t('delete_dialog.description')}
                                  </AlertDialogDescription>
                                </AlertDialogHeader>
                                <AlertDialogFooter>
                                  <AlertDialogCancel onClick={() => setDeleteId(null)}>{t('common:actions.cancel')}</AlertDialogCancel>
                                  <AlertDialogAction onClick={handleDelete}>{t('common:actions.confirm_delete')}</AlertDialogAction>
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
                        <p className="text-[11px] text-muted-foreground">{t('filters.type')}: {provider.Type || t('common:unknown')}</p>
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
                            <AlertDialogTitle>{t('delete_dialog.title')}</AlertDialogTitle>
                            <AlertDialogDescription>
                              {t('delete_dialog.description')}
                            </AlertDialogDescription>
                          </AlertDialogHeader>
                          <AlertDialogFooter>
                            <AlertDialogCancel onClick={() => setDeleteId(null)}>{t('common:actions.cancel')}</AlertDialogCancel>
                            <AlertDialogAction onClick={handleDelete}>{t('common:actions.confirm_delete')}</AlertDialogAction>
                          </AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    </div>
                  </div>
                  <div className="grid grid-cols-3 gap-2 border-t pt-2">
                    <div className="space-y-0.5">
                      <p className="text-[10px] text-muted-foreground uppercase tracking-wide">{t('table.rolling')}</p>
                      <p className="text-xs">{renderUsageCell(usageMap[provider.ID], "rolling")}</p>
                    </div>
                    <div className="space-y-0.5">
                      <p className="text-[10px] text-muted-foreground uppercase tracking-wide">{t('table.weekly')}</p>
                      <p className="text-xs">{renderUsageCell(usageMap[provider.ID], "weekly")}</p>
                    </div>
                    <div className="space-y-0.5">
                      <p className="text-[10px] text-muted-foreground uppercase tracking-wide">{t('table.monthly')}</p>
                      <p className="text-xs">{renderUsageCell(usageMap[provider.ID], "monthly")}</p>
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
