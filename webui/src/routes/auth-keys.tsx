import { useEffect, useMemo, useState, type ReactNode } from "react";
import { z } from "zod";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage
} from "@/components/ui/form";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from "@/components/ui/alert-dialog";
import {
  Copy,
  Plus,
  Search,
  Trash2,
  Pencil,
  ChevronLeft,
  ChevronRight,
  ChevronDownIcon,
  Eye,
  EyeOff
} from "lucide-react";
import Loading from "@/components/loading";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { Calendar } from "@/components/ui/calendar";
import {
  getAuthKeys,
  createAuthKey,
  updateAuthKey,
  deleteAuthKey,
  toggleAuthKeyStatus,
  getModelOptions,
  type AuthKey,
  type Model
} from "@/lib/api";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

const formSchema = z.object({
  name: z.string().min(1, { message: "项目名称不能为空" }),
  key: z.string().optional(),
  status: z.boolean(),
  allow_all: z.boolean(),
  models: z.array(z.string()),
  expires_at: z.string().nullable().optional(),
}).refine((value) => value.allow_all || value.models.length > 0, {
  message: "请选择至少一个允许的模型",
  path: ["models"],
});

type AuthKeyFormValues = z.infer<typeof formSchema>;

const defaultFormValues: AuthKeyFormValues = {
  name: "",
  status: true,
  allow_all: true,
  models: [],
  expires_at: null,
};

type MobileInfoItemProps = {
  label: string;
  value: ReactNode;
  mono?: boolean;
};

const MobileInfoItem = ({ label, value, mono = false }: MobileInfoItemProps) => (
  <div className="space-y-1">
    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{label}</p>
    <div className={cn("text-sm font-medium break-words", mono ? "font-mono text-xs" : "")}>
      {value}
    </div>
  </div>
);


export default function AuthKeysPage() {
  const [authKeys, setAuthKeys] = useState<AuthKey[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(0);
  const [statusFilter, setStatusFilter] = useState<"all" | "active" | "inactive">("all");
  const [allowAllFilter, setAllowAllFilter] = useState<"all" | "allow" | "restricted">("all");
  const [searchInput, setSearchInput] = useState("");
  const [searchTerm, setSearchTerm] = useState("");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<AuthKey | null>(null);
  const [modelSearch, setModelSearch] = useState("");
  const [pendingDelete, setPendingDelete] = useState<AuthKey | null>(null);
  const [toggleLoadingId, setToggleLoadingId] = useState<number | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [revealedKeys, setRevealedKeys] = useState<Record<number, boolean>>({});


  const form = useForm<AuthKeyFormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: defaultFormValues,
  });

  const allowAll = form.watch("allow_all");

  useEffect(() => {
    fetchModels();
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      setSearchTerm(searchInput.trim());
      setPage(1);
    }, 400);
    return () => clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    setPage(1);
  }, [statusFilter, allowAllFilter]);

  useEffect(() => {
    fetchAuthKeys();
  }, [page, pageSize, statusFilter, allowAllFilter, searchTerm]);


  const filteredModels = useMemo(() => {
    if (!modelSearch) return models;
    return models.filter((model) =>
      model.Name.toLowerCase().includes(modelSearch.toLowerCase())
    );
  }, [models, modelSearch]);

  const fetchModels = async () => {
    try {
    const list = await getModelOptions();
      setModels(list);
    } catch (error) {
      console.error(error);
      toast.error("获取模型列表失败");
    }
  };

  const fetchAuthKeys = async () => {
    setLoading(true);
    try {
      const response = await getAuthKeys({
        page,
        page_size: pageSize,
        status: statusFilter === "all" ? undefined : statusFilter,
        allow_all:
          allowAllFilter === "all"
            ? undefined
            : allowAllFilter === "allow"
              ? "true"
              : "false",
        search: searchTerm || undefined,
      });
      setAuthKeys(response.data);
      setTotal(response.total);
      setPages(response.pages);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "获取 API Key 列表失败");
    } finally {
      setLoading(false);
    }
  };

  const handleDialogOpenChange = (open: boolean) => {
    setDialogOpen(open);
    if (!open) {
      setEditingKey(null);
      setModelSearch("");
      form.reset(defaultFormValues);
    }
  };

  const handleCreate = () => {
    setEditingKey(null);
    form.reset(defaultFormValues);
    setDialogOpen(true);
  };

  const handleEdit = (key: AuthKey) => {
    setEditingKey(key);
    form.reset({
      name: key.Name,
      key: key.Key,
      status: key.Status,
      allow_all: key.AllowAll,
      models: key.Models ?? [],
      expires_at: key.ExpiresAt,
    });
    setDialogOpen(true);
  };

  const handleCopyKey = async (keyValue: string) => {
    try {
      await navigator.clipboard.writeText(keyValue);
      toast.success("已复制 API Key");
    } catch (error) {
      console.error(error);
      toast.error("复制失败，请手动复制");
    }
  };

  const onSubmit = async (values: AuthKeyFormValues) => {
    setSaving(true);
    try {
      const payload = {
        name: values.name,
        key: values.key?.trim() || undefined,
        status: values.status,
        allow_all: values.allow_all,
        models: values.allow_all ? [] : values.models,
        expires_at: values.expires_at ?? undefined,
      };
      if (editingKey) {
        await updateAuthKey(editingKey.ID, payload);
        toast.success("API Key 已更新");
      } else {
        await createAuthKey(payload);
        toast.success("API Key 已创建");
      }
      handleDialogOpenChange(false);
      fetchAuthKeys();
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const handleToggleStatus = async (item: AuthKey, next: boolean) => {
    const previousStatus = item.Status;
    setToggleLoadingId(item.ID);

    // 乐观更新：立即更新 UI
    setAuthKeys(prev =>
      prev.map(key =>
        key.ID === item.ID ? { ...key, Status: next } : key
      )
    );

    try {
      const updated = await toggleAuthKeyStatus(item.ID);
      // 用服务器返回的数据更新
      setAuthKeys(prev =>
        prev.map(key =>
          key.ID === item.ID ? updated : key
        )
      );
    } catch (error) {
      // 失败时回滚到之前的状态
      setAuthKeys(prev =>
        prev.map(key =>
          key.ID === item.ID ? { ...key, Status: previousStatus } : key
        )
      );
      console.error(error);
      toast.error(error instanceof Error ? error.message : "更新状态失败");
    } finally {
      setToggleLoadingId(null);
    }
  };

  const handleDeleteConfirmed = async () => {
    if (!pendingDelete) return;
    setDeleteLoading(true);
    try {
      await deleteAuthKey(pendingDelete.ID);
      toast.success("删除成功");
      fetchAuthKeys();
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "删除失败");
    } finally {
      setDeleteLoading(false);
      setPendingDelete(null);
    }
  };

  const handlePageChange = (nextPage: number) => {
    if (nextPage < 1 || nextPage > Math.max(pages, 1)) return;
    setPage(nextPage);
  };

  const handlePageSizeChange = (size: number) => {
    if (size === pageSize) return;
    setPage(1);
    setPageSize(size);
  };

  const toggleKeyVisibility = (id: number) => {
    setRevealedKeys((prev) => ({
      ...prev,
      [id]: !prev[id],
    }));
  };


  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-2xl font-bold tracking-tight">API Key 管理</h2>
        </div>
        <Button onClick={handleCreate} className="shrink-0">
          <Plus className="size-4 " />
        </Button>
      </div>

      <div className="flex flex-col gap-2">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5 lg:gap-4">
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">搜索</Label>
            <div className="relative">
              <Search className="size-4 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={searchInput}
                onChange={(event) => setSearchInput(event.target.value)}
                placeholder="名称或 Key"
                className="h-9 text-xs pl-8"
              />
            </div>
          </div>
          <div className="flex flex-col gap-1 text-xs lg:min-w-0">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">状态</Label>
            <Select value={statusFilter} onValueChange={(value: "all" | "active" | "inactive") => setStatusFilter(value)}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder="选择状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                <SelectItem value="active">启用</SelectItem>
                <SelectItem value="inactive">禁用</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="col-span-2 flex flex-col gap-1 text-xs lg:min-w-0 sm:col-span-1">
            <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">访问范围</Label>
            <Select value={allowAllFilter} onValueChange={(value: "all" | "allow" | "restricted") => setAllowAllFilter(value)}>
              <SelectTrigger className="h-8 text-xs w-full px-2">
                <SelectValue placeholder="访问范围" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                <SelectItem value="allow">允许全部模型</SelectItem>
                <SelectItem value="restricted">指定模型</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
      </div>

        <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
          {loading ? (
            <div className="flex h-full items-center justify-center">
              <Loading message="加载 API Key 列表" />
            </div>
          ) : authKeys.length === 0 ? (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              暂无 API Key
            </div>
          ) : (
            <div className="h-full flex flex-col">
              <div className="hidden sm:block flex-1 overflow-y-auto">
                <div className="w-full">
                  <Table className="min-w-[960px]">
                    <TableHeader className="z-10 sticky top-0 bg-secondary/90 backdrop-blur text-secondary-foreground">
                      <TableRow>
                        <TableHead>项目</TableHead>
                        <TableHead className="min-w-64">Key</TableHead>
                        <TableHead>使用范围</TableHead>
                        <TableHead>有效期至</TableHead>
                        <TableHead>使用次数</TableHead>
                        <TableHead>最后使用时间</TableHead>
                        <TableHead>状态</TableHead>
                        <TableHead>操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {authKeys.map((item) => {
                        const modelsToShow = item.Models ?? [];
                        const hasMoreModels = modelsToShow.length > 3;
                        const expired = item.ExpiresAt ? new Date(item.ExpiresAt) < new Date() : false;
                        const toggleDisabled = toggleLoadingId === item.ID;
                        const isKeyVisible = Boolean(revealedKeys[item.ID]);
                        const lastSix = item.Key.slice(-6);
                        const hiddenLength = Math.max(0, item.Key.length - 6);
                        const maskedKey = `${"*".repeat(hiddenLength)}${lastSix}`;
                        const displayKey = isKeyVisible ? item.Key : maskedKey;
                        return (
                          <TableRow key={item.ID}>
                            <TableCell>
                              <div className="flex flex-col gap-1">
                                <span className="font-medium">{item.Name}</span>
                              </div>
                            </TableCell>
                            <TableCell className="align-top">
                              <div className="flex items-center gap-2">
                                <span className="font-mono text-sm break-all">{displayKey}</span>
                                <Button
                                  size="icon"
                                  variant="ghost"
                                  className="size-8"
                                  onClick={() => toggleKeyVisibility(item.ID)}
                                  aria-label={isKeyVisible ? "隐藏 Key" : "显示 Key"}
                                >
                                  {isKeyVisible ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                                </Button>
                                <Button
                                  size="icon"
                                  variant="ghost"
                                  className="size-8"
                                  onClick={() => handleCopyKey(item.Key)}
                                >
                                  <Copy className="size-4" />
                                </Button>
                              </div>
                            </TableCell>
                            <TableCell>
                              {item.AllowAll ? (
                                <Badge>全部模型</Badge>
                              ) : (
                                <div>
                                  {modelsToShow.slice(0, 3).map((model) => (
                                    <Badge key={model} variant="outline">
                                      {model}
                                    </Badge>
                                  ))}
                                  {hasMoreModels && (
                                    <Badge variant="outline">+{modelsToShow.length - 3}</Badge>
                                  )}
                                </div>
                              )}
                            </TableCell>
                            <TableCell>
                              <span className={cn(
                                "text-sm",
                                expired ? "text-destructive font-medium" : ""
                              )}>
                                {item.ExpiresAt ? new Date(item.ExpiresAt).toLocaleDateString() : "永久有效"}
                              </span>
                            </TableCell>
                            <TableCell>
                              <div className="flex flex-col">
                                <span>{item.UsageCount}</span>
                              </div>
                            </TableCell>
                            <TableCell>
                              {item.LastUsedAt ? new Date(item.LastUsedAt).toLocaleString() : "未使用"}
                            </TableCell>
                            <TableCell>
                              <div className="flex items-center gap-2">
                                <Switch
                                  checked={item.Status}
                                  disabled={toggleDisabled}
                                  onCheckedChange={(checked) => handleToggleStatus(item, checked)}
                                />
                              </div>
                            </TableCell>
                            <TableCell>
                              <div className="flex gap-2">
                                <Button variant="outline" size="icon" onClick={() => handleEdit(item)}>
                                  <Pencil />
                                </Button>
                                <Button
                                  variant="outline"
                                  size="icon"
                                  className="text-destructive"
                                  onClick={() => setPendingDelete(item)}
                                >
                                  <Trash2 />
                                </Button>
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
                {authKeys.map((item) => {
                  const modelsToShow = item.Models ?? [];
                  const hasMoreModels = modelsToShow.length > 3;
                  const expired = item.ExpiresAt ? new Date(item.ExpiresAt) < new Date() : false;
                  const toggleDisabled = toggleLoadingId === item.ID;
                  const isKeyVisible = Boolean(revealedKeys[item.ID]);
                  const lastSix = item.Key.slice(-6);
                  const hiddenLength = Math.max(0, item.Key.length - 6);
                  const maskedKey = `${"*".repeat(hiddenLength)}${lastSix}`;
                  const displayKey = isKeyVisible ? item.Key : maskedKey;

                  return (
                    <div key={item.ID} className="py-3 space-y-3">
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0 flex-1">
                          <h3 className="font-semibold text-sm truncate">{item.Name}</h3>
                          <p className="text-[11px] text-muted-foreground">ID: {item.ID}</p>
                        </div>
                        <span
                          className={`text-[11px] font-medium px-2 py-0.5 rounded-full ${item.Status ? 'bg-emerald-100 text-emerald-700' : 'bg-red-100 text-red-700'}`}
                        >
                          {item.Status ? '启用' : '禁用'}
                        </span>
                      </div>
                      <div className="rounded-md border bg-muted/20 px-3 py-2 space-y-2">
                        <p className="text-[11px] text-muted-foreground uppercase tracking-wide">Key</p>
                        <div className="flex items-start gap-2">
                          <div className="flex-1 min-w-0">
                            <p className="font-mono text-xs break-all">{displayKey}</p>
                          </div>
                          <div className="flex items-center gap-1 shrink-0">
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-8"
                              onClick={() => toggleKeyVisibility(item.ID)}
                              aria-label={isKeyVisible ? "隐藏 Key" : "显示 Key"}
                            >
                              {isKeyVisible ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                            </Button>
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-8"
                              onClick={() => handleCopyKey(item.Key)}
                              aria-label="复制 Key"
                            >
                              <Copy className="size-4" />
                            </Button>
                          </div>
                        </div>
                      </div>
                      <div className="grid grid-cols-2 gap-3 text-xs">
                        <MobileInfoItem
                          label="使用范围"
                          value={item.AllowAll ? <Badge>全部模型</Badge> : <Badge variant="outline">指定模型</Badge>}
                        />
                        <MobileInfoItem
                          label="有效期至"
                          value={
                            <span className={expired ? "text-destructive font-medium" : ""}>
                              {item.ExpiresAt ? new Date(item.ExpiresAt).toLocaleDateString() : "永久有效"}
                            </span>
                          }
                        />
                        <MobileInfoItem label="使用次数" value={item.UsageCount} />
                        <MobileInfoItem label="最后使用" value={item.LastUsedAt ? new Date(item.LastUsedAt).toLocaleString() : "未使用"} />
                      </div>
                      {!item.AllowAll && modelsToShow.length > 0 && (
                        <div className="flex flex-wrap gap-1.5">
                          {modelsToShow.slice(0, 3).map((model) => (
                            <Badge key={model} variant="outline">
                              {model}
                            </Badge>
                          ))}
                          {hasMoreModels && <Badge variant="outline">+{modelsToShow.length - 3}</Badge>}
                        </div>
                      )}
                      <div className="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2">
                        <p className="text-xs text-muted-foreground">启用状态</p>
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-medium">{item.Status ? "启用" : "禁用"}</span>
                          <Switch
                            checked={item.Status}
                            disabled={toggleDisabled}
                            onCheckedChange={(checked) => handleToggleStatus(item, checked)}
                            aria-label="切换启用状态"
                          />
                        </div>
                      </div>
                      <div className="flex flex-wrap justify-end gap-1.5">
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          onClick={() => handleEdit(item)}
                        >
                          编辑
                        </Button>
                        <Button
                          variant="destructive"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          onClick={() => setPendingDelete(item)}
                        >
                          删除
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 flex-shrink-0 border-t pt-2">
          <div className="text-sm text-muted-foreground whitespace-nowrap">
            共 {total} 条，第 {page} / {Math.max(pages, 1)} 页
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
                disabled={page === pages || pages === 0}
                aria-label="下一页"
              >
                <ChevronRight className="size-4" />
              </Button>
          </div>
        </div>
      </div>

      <Dialog open={dialogOpen} onOpenChange={handleDialogOpenChange}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{editingKey ? "编辑 API Key" : "新建 API Key"}</DialogTitle>
          </DialogHeader>
          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <FormField
                control={form.control}
                name="name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>项目名称</FormLabel>
                    <FormControl>
                      <Input {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="models"
                render={({ field }) => (
                  <FormItem className="rounded-lg space-y-3">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <FormLabel >模型权限</FormLabel>
                      <FormField
                        control={form.control}
                        name="allow_all"
                        render={({ field: allowAllField }) => (
                          <FormControl>
                            <div className="flex items-center gap-2 text-sm">
                              <span className="text-muted-foreground">无限制</span>
                              <Checkbox
                                checked={allowAllField.value}
                                onCheckedChange={(checked) => allowAllField.onChange(checked === true)}
                              />
                            </div>
                          </FormControl>
                        )}
                      />
                    </div>
                    <FormControl>
                      <div className="space-y-3">
                        <Input
                          placeholder="搜索模型"
                          value={modelSearch}
                          onChange={(event) => setModelSearch(event.target.value)}
                          disabled={allowAll}
                        />
                        <div className={cn(
                          "border rounded-md p-3 h-48 overflow-y-auto space-y-2",
                          allowAll ? "opacity-50 pointer-events-none" : ""
                        )}>
                          {filteredModels.length === 0 ? (
                            <p className="text-sm text-muted-foreground">无匹配模型</p>
                          ) : (
                            filteredModels.map((model) => {
                              const checked = field.value.includes(model.Name);
                              return (
                                <label key={model.ID} className="flex items-center gap-2 text-sm cursor-pointer">
                                  <Checkbox
                                    checked={checked}
                                    onCheckedChange={(checkedState) => {
                                      const current = new Set(field.value);
                                      const nextChecked = checkedState === true;
                                      if (nextChecked) {
                                        current.add(model.Name);
                                      } else {
                                        current.delete(model.Name);
                                      }
                                      field.onChange(Array.from(current));
                                    }}
                                    className="border-muted-foreground/50"
                                  />
                                  <div className="flex flex-col">
                                    <span className="text-foreground">{model.Name}</span>
                                  </div>
                                </label>
                              );
                            })
                          )}
                        </div>
                      </div>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="expires_at"
                render={({ field }) => {
                  const selected = field.value ? new Date(field.value) : undefined;
                  const isValidDate = selected && !Number.isNaN(selected.getTime()) ? selected : undefined;
                  return (
                    <FormItem className="grid gap-2">
                      <FormLabel>有效期至（可选）</FormLabel>
                      <FormControl>
                        <div className="flex flex-col gap-3">
                          <div className="flex items-center gap-2">
                            <Popover open={open} onOpenChange={setOpen} modal={true}>
                              <PopoverTrigger asChild>
                                <Button
                                  variant="outline"
                                  id="date"
                                  className="w-48 justify-between font-normal"
                                >
                                  {isValidDate ? isValidDate.toLocaleDateString() : "Select date"}
                                  <ChevronDownIcon />
                                </Button>
                              </PopoverTrigger>
                              <PopoverContent className="w-auto overflow-hidden p-0" align="start">
                                <Calendar
                                  mode="single"
                                  selected={isValidDate}
                                  captionLayout="dropdown"
                                  startMonth={new Date()}
                                  disabled={{ before: new Date(new Date().setDate(new Date().getDate() + 1)) }}
                                  endMonth={new Date(new Date().getFullYear() + 10, 11, 31)}
                                  onSelect={(date) => {
                                    field.onChange(date ? date.toISOString() : null);
                                    setOpen(false);
                                  }}
                                />
                              </PopoverContent>
                            </Popover>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="ml-auto text-muted-foreground"
                              onClick={() => {
                                field.onChange(null);
                                setOpen(false);
                              }}
                              disabled={!field.value}
                            >
                              重置
                            </Button>
                          </div>
                        </div>
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  );
                }}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => handleDialogOpenChange(false)}>
                  取消
                </Button>
                <Button type="submit" disabled={saving}>
                  {saving ? "保存中..." : "保存"}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

      <AlertDialog open={Boolean(pendingDelete)} onOpenChange={(open) => { if (!open) setPendingDelete(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除这个 API Key？</AlertDialogTitle>
            <AlertDialogDescription>
              {pendingDelete ? `项目 ${pendingDelete.Name} 将被删除，该 Key 将立即失效。` : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel
              onClick={() => setPendingDelete(null)}
              disabled={deleteLoading}
            >
              取消
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDeleteConfirmed}
              className="bg-destructive hover:bg-destructive/90"
              disabled={deleteLoading}
            >
              {deleteLoading ? "删除中..." : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
