import { useState, useEffect } from "react";
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
import { RefreshCw } from "lucide-react";
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

// 定义表单验证模式
const formSchema = z.object({
  name: z.string().min(1, { message: "提供商名称不能为空" }),
  type: z.string().min(1, { message: "提供商类型不能为空" }),
  config: z.string().min(1, { message: "配置不能为空" }),
  console: z.string().optional(),
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

  // 筛选条件
  const [nameFilter, setNameFilter] = useState<string>("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [availableTypes, setAvailableTypes] = useState<string[]>([]);

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: "",
      type: "",
      config: "",
      console: "",
    },
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
      // 设置可用的提供商类型
      const types = data.map(template => template.type);
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
      setFilteredProviderModels(data);
    } catch (err) {
      console.error("获取提供商模型失败", err);
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

  const handleCreate = async (values: z.infer<typeof formSchema>) => {
    try {
      await createProvider({
        name: values.name,
        type: values.type,
        config: values.config,
        console: values.console || ""
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 创建成功`);
      form.reset({ name: "", type: "", config: "", console: "" });
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
        console: values.console || ""
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 更新成功`);
      setEditingProvider(null);
      form.reset({ name: "", type: "", config: "", console: "" });
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
    setEditingProvider(provider);
    form.reset({
      name: provider.Name,
      type: provider.Type,
      config: provider.Config,
      console: provider.Console || "",
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    setEditingProvider(null);
    form.reset({ name: "", type: "", config: "", console: "" });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const handleRefresh = () => {
    fetchProviders();
  };

  const hasFilter = nameFilter.trim() !== "" || typeFilter !== "all";

  return (
    <div className="h-full min-h-0 flex flex-col gap-4 p-1">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-2xl font-bold tracking-tight">提供商管理</h2>
          <p className="text-sm text-muted-foreground">维护上游供应商、类型与凭据</p>
        </div>
        <div className="flex w-full sm:w-auto items-center justify-end gap-2">
          <Button
            variant="outline"
            size="icon"
            className="shrink-0"
            aria-label="刷新列表"
            title="刷新列表"
            onClick={handleRefresh}
          >
            <RefreshCw className="size-4" />
          </Button>
        </div>
      </div>
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col gap-1 text-xs w-full sm:w-64">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">提供商名称</Label>
            <Input
              placeholder="输入名称"
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              className="h-9 text-sm"
            />
          </div>
          <div className="flex flex-col gap-1 text-xs w-full sm:w-48">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">类型</Label>
            <Select value={typeFilter} onValueChange={setTypeFilter}>
              <SelectTrigger className="h-9 text-sm">
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
          <Button onClick={openCreateDialog} className="w-full sm:w-auto sm:ml-auto">
            添加提供商
          </Button>
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
            <div className="hidden sm:block w-full overflow-x-auto">
              <Table className="min-w-[1100px]">
                <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>名称</TableHead>
                    <TableHead>类型</TableHead>
                    <TableHead>配置</TableHead>
                    <TableHead>控制台</TableHead>
                    <TableHead className="w-[260px]">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {providers.map((provider) => (
                    <TableRow key={provider.ID}>
                      <TableCell className="font-mono text-xs text-muted-foreground">{provider.ID}</TableCell>
                      <TableCell className="font-medium">{provider.Name}</TableCell>
                      <TableCell className="text-sm">{provider.Type}</TableCell>
                      <TableCell>
                        <pre className="text-xs overflow-hidden max-w-md truncate">
                          {provider.Config}
                        </pre>
                      </TableCell>
                      <TableCell>
                        {provider.Console ? (
                          <Button
                            title={provider.Console}
                            variant="outline"
                            size="sm"
                            onClick={() => window.open(provider.Console, '_blank')}
                          >
                            前往
                          </Button>
                        ) : (
                          <span className="text-muted-foreground">暂未设置</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-2">
                          <Button variant="outline" size="sm" onClick={() => openEditDialog(provider)}>
                            编辑
                          </Button>
                          <Button variant="secondary" size="sm" onClick={() => openModelsDialog(provider.ID)}>
                            模型列表
                          </Button>
                          <AlertDialog>
                            <AlertDialogTrigger asChild>
                              <Button variant="destructive" size="sm" onClick={() => openDeleteDialog(provider.ID)}>
                                删除
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
            <div className="sm:hidden px-2 py-3 divide-y divide-border">
              {providers.map((provider) => (
                <div key={provider.ID} className="py-3 space-y-3 my-1 px-1">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 flex-1">
                      <h3 className="font-semibold text-base truncate">{provider.Name}</h3>
                      <p className="text-[11px] text-muted-foreground">ID: {provider.ID}</p>
                      <p className="text-[11px] text-muted-foreground">类型: {provider.Type}</p>
                      {provider.Console && (
                        <Button
                          variant="link"
                          className="px-0 h-auto text-xs"
                          onClick={() => window.open(provider.Console, '_blank')}
                        >
                          打开控制台
                        </Button>
                      )}
                    </div>
                    <div className="flex flex-wrap justify-end gap-1.5">
                      <Button variant="outline" size="sm" className="h-7 px-2 text-xs" onClick={() => openEditDialog(provider)}>
                        编辑
                      </Button>
                      <Button variant="secondary" size="sm" className="h-7 px-2 text-xs" onClick={() => openModelsDialog(provider.ID)}>
                        模型
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button variant="destructive" size="sm" className="h-7 px-2 text-xs" onClick={() => openDeleteDialog(provider.ID)}>
                            删除
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
                  <div className="text-xs space-y-1">
                    <p className="text-muted-foreground">配置</p>
                    <pre className="font-mono text-[11px] whitespace-pre-wrap break-words bg-muted/40 p-2 rounded">
                      {provider.Config}
                    </pre>
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
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>类型</FormLabel>
                    <FormControl>
                      <select
                        {...field}
                        className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                        onChange={(e) => {
                          field.onChange(e);
                          // When type changes, populate config with template if available
                          const selectedTemplate = providerTemplates.find(t => t.type === e.target.value);
                          if (selectedTemplate) {
                            form.setValue("config", selectedTemplate.template);
                          }
                        }}
                      >
                        <option value="">请选择提供商类型</option>
                        {providerTemplates.map((template) => (
                          <option key={template.type} value={template.type}>
                            {template.type}
                          </option>
                        ))}
                      </select>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="config"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>配置</FormLabel>
                    <FormControl>
                      <Textarea
                        {...field}
                        className="resize-none whitespace-pre overflow-x-auto"
                      />
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
