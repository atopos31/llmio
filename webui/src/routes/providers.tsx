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
  getProviderModels
} from "@/lib/api";
import type { Provider, ProviderTemplate, ProviderModel } from "@/lib/api";
import { toast } from "sonner";
import { ExternalLink, Pencil, Trash2, Boxes } from "lucide-react";

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
  proxy: z.string().optional(),
});

export default function ProvidersPage() {
  const [providers, setProviders] = useState<Provider[]>([]);
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
  const [configFields, setConfigFields] = useState<ConfigFieldMap>({});
  const [structuredConfigEnabled, setStructuredConfigEnabled] = useState(false);
  const configCacheRef = useRef<Record<string, ConfigFieldMap>>({});

  // 筛选条件
  const [nameFilter, setNameFilter] = useState<string>("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [availableTypes, setAvailableTypes] = useState<string[]>([]);

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: { name: "", type: "", config: "", console: "", proxy: "" },
  });
  const selectedProviderType = form.watch("type");

  useEffect(() => {
    fetchProviders();
    fetchProviderTemplates();
  }, []);

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
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取提供商模型失败: ${message}`);
      setProviderModels([]);
      setFilteredProviderModels([]);
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
      await createProvider({
        name: values.name,
        type: values.type,
        config: values.config,
        console: values.console || "",
        proxy: values.proxy || "",
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 创建成功`);
      form.reset({ name: "", type: "", config: "", console: "", proxy: "" });
      fetchProviders();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`创建提供商失败: ${message}`);
      console.error(err);
    }
  };

  const handleUpdate = async (values: z.infer<typeof formSchema>) => {
    if (!editingProvider) return;
    try {
      await updateProvider(editingProvider.ID, {
        name: values.name,
        type: values.type,
        config: values.config,
        console: values.console || "",
        proxy: values.proxy || "",
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 更新成功`);
      setEditingProvider(null);
      form.reset({ name: "", type: "", config: "", console: "", proxy: "" });
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
      proxy: provider.Proxy || "",
    });
    setOpen(true);
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
      proxy: "",
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
                name="proxy"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>HTTP 代理</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="http://192.168.1.2:1234" />
                    </FormControl>
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

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                  取消
                </Button>
                <Button type="submit">
                  {editingProvider ? "更新" : "创建"}
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
                <div className="space-y-2">
                  {filteredProviderModels.map((model, index) => (
                    <div
                      key={index}
                      className="flex items-center justify-between p-2 border rounded-lg"
                    >
                      <div className="flex-1">
                        <div className="font-medium">{model.id}</div>
                      </div>
                      <TooltipProvider>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => copyModelName(model.id)}
                              className="min-w-12"
                            >
                              <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" aria-hidden="true" className="h-4 w-4"><path stroke-linecap="round" stroke-linejoin="round" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3m2 4H10m0 0l3-3m-3 3l3 3"></path></svg>
                            </Button>
                          </TooltipTrigger>
                        </Tooltip>
                      </TooltipProvider>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          <DialogFooter>
            <Button onClick={() => setModelsOpen(false)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
