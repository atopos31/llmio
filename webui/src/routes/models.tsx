import { useState, useEffect, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
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
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Label } from "@/components/ui/label";
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
import {
  getModels,
  createModel,
  updateModel,
  deleteModel,
} from "@/lib/api";
import type { Model } from "@/lib/api";
import { Checkbox } from "@/components/ui/checkbox";
import { toast } from "sonner";
import { ChevronLeft, ChevronRight, Search, Pencil, Trash2, Link } from "lucide-react";

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

const renderStrategy = (strategy?: string) =>
  strategy === "rotor" ? "Rotor" : "Lottery";

type StrategyFilter = "all" | "lottery" | "rotor";
type IOLogFilter = "all" | "true" | "false";

// 定义表单验证模式
const formSchema = z.object({
  name: z.string().min(1, { message: "模型名称不能为空" }),
  remark: z.string(),
  max_retry: z.number().min(0, { message: "重试次数限制不能为负数" }),
  time_out: z.number().min(0, { message: "超时时间不能为负数" }),
  io_log: z.boolean(),
  strategy: z.enum(["lottery", "rotor"]),
  breaker: z.boolean(),
});

export default function ModelsPage() {
  const navigate = useNavigate();
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<Model | null>(null);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(0);
  const [searchInput, setSearchInput] = useState("");
  const [searchTerm, setSearchTerm] = useState("");
  const [strategyFilter, setStrategyFilter] = useState<StrategyFilter>("all");
  const [ioLogFilter, setIoLogFilter] = useState<IOLogFilter>("all");

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
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

  useEffect(() => {
    const timer = setTimeout(() => {
      setSearchTerm(searchInput.trim());
      setPage(1);
    }, 400);
    return () => clearTimeout(timer);
  }, [searchInput]);

  const fetchModels = async () => {
    try {
      setLoading(true);
      const response = await getModels({
        page,
        page_size: pageSize,
        search: searchTerm || undefined,
        strategy: strategyFilter === "all" ? undefined : strategyFilter,
        io_log: ioLogFilter === "all" ? undefined : (ioLogFilter as "true" | "false"),
      });
      setModels(response.data);
      setTotal(response.total);
      setPages(response.pages);
      const totalPages = response.pages || 0;
      if (totalPages > 0 && page > totalPages) {
        setPage(totalPages);
      } else if (totalPages === 0 && page !== 1) {
        setPage(1);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取模型列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchModels();
  }, [page, pageSize, searchTerm, strategyFilter, ioLogFilter]);

  const handleCreate = async (values: z.infer<typeof formSchema>) => {
    try {
      await createModel({
        name: values.name,
        remark: values.remark,
        max_retry: values.max_retry,
        time_out: values.time_out,
        io_log: values.io_log,
        strategy: values.strategy,
        breaker: values.breaker,
      });
      setOpen(false);
      toast.success(`模型: ${values.name} 创建成功`);
      form.reset({ name: "", remark: "", max_retry: 10, time_out: 60, io_log: false, strategy: "lottery", breaker: false });
      await fetchModels();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`创建模型失败: ${message}`);
    }
  };

  const handleUpdate = async (values: z.infer<typeof formSchema>) => {
    if (!editingModel) return;
    try {
      await updateModel(editingModel.ID, {
        name: values.name,
        remark: values.remark,
        max_retry: values.max_retry,
        time_out: values.time_out,
        io_log: values.io_log,
        strategy: values.strategy,
        breaker: values.breaker,
      });
      setOpen(false);
      toast.success(`模型: ${values.name} 更新成功`);
      setEditingModel(null);
      form.reset({ name: "", remark: "", max_retry: 10, time_out: 60, io_log: false, strategy: "lottery", breaker: false });
      await fetchModels();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`更新模型失败: ${message}`);
      console.error(err);
    }
  };

  const handleDelete = async () => {
    if (!deleteId) return;
    try {
      const targetModel = models.find((model) => model.ID === deleteId);
      await deleteModel(deleteId);
      setDeleteId(null);
      await fetchModels();
      toast.success(`模型: ${targetModel?.Name ?? deleteId} 删除成功`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除模型失败: ${message}`);
      console.error(err);
    }
  };

  const openEditDialog = (model: Model) => {
    setEditingModel(model);
    form.reset({
      name: model.Name,
      remark: model.Remark,
      max_retry: model.MaxRetry,
      time_out: model.TimeOut,
      io_log: model.IOLog,
      strategy: model.Strategy === "rotor" ? "rotor" : "lottery",
      breaker: model.Breaker ?? false,
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    setEditingModel(null);
    form.reset({ name: "", remark: "", max_retry: 10, time_out: 60, io_log: false, strategy: "lottery", breaker: false });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const handlePageChange = (nextPage: number) => {
    const maxPage = Math.max(pages, 1);
    if (nextPage < 1 || nextPage > maxPage) return;
    setPage(nextPage);
  };

  const handlePageSizeChange = (size: number) => {
    setPageSize(size);
    setPage(1);
  };

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex items-start justify-between">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">模型管理</h2>
          </div>
        </div>
        <div className="flex flex-col gap-2 flex-shrink-0">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-2 lg:grid-cols-3 lg:gap-4">
            <div className="flex flex-col gap-1 text-xs lg:min-w-0">
              <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">搜索</Label>
              <div className="relative">
                <Search className="size-4 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
                <Input
                  placeholder="按名称搜索"
                  value={searchInput}
                  onChange={(event) => setSearchInput(event.target.value)}
                  className="h-9 pl-8 text-sm"
                />
              </div>
            </div>
            <div className="flex flex-col gap-1 text-xs lg:min-w-0">
              <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">负载策略</Label>
              <Select
                value={strategyFilter}
                onValueChange={(value) => {
                  setStrategyFilter(value as StrategyFilter);
                  setPage(1);
                }}
              >
                <SelectTrigger className="h-8 text-sm px-2 w-full">
                  <SelectValue placeholder="负载策略" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部</SelectItem>
                  <SelectItem value="lottery">Lottery</SelectItem>
                  <SelectItem value="rotor">Rotor</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-end col-span-2 sm:col-span-2 lg:col-span-1 gap-2">
              <div className="flex-1">
                <Label className="text-[11px] text-muted-foreground uppercase tracking-wide">IO 记录</Label>
                <Select
                  value={ioLogFilter}
                  onValueChange={(value) => {
                    setIoLogFilter(value as IOLogFilter);
                    setPage(1);
                  }}
                >
                  <SelectTrigger className="h-8 text-sm px-2 w-full">
                    <SelectValue placeholder="IO 记录" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">全部</SelectItem>
                    <SelectItem value="true">开启</SelectItem>
                    <SelectItem value="false">关闭</SelectItem>
                  </SelectContent>
                </Select>

              </div>
              <Button onClick={openCreateDialog} className="h-8 text-xs">
                添加模型
              </Button>
            </div>

          </div>

        </div>
      </div>
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载模型列表" />
          </div>
        ) : models.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            暂无模型数据
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="hidden sm:block flex-1 overflow-y-auto">
              <div className="w-full">
                <Table className="min-w-[1100px]">
                  <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                    <TableRow>
                      <TableHead>ID</TableHead>
                      <TableHead>名称</TableHead>
                      <TableHead>备注</TableHead>
                      <TableHead>重试次数限制</TableHead>
                      <TableHead>超时时间(秒)</TableHead>
                      <TableHead>负载策略</TableHead>
                      <TableHead>IO 记录</TableHead>
                      <TableHead>操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {models.map((model) => (
                      <TableRow key={model.ID}>
                        <TableCell className="font-mono text-xs text-muted-foreground">{model.ID}</TableCell>
                        <TableCell className="font-medium">{model.Name}</TableCell>
                        <TableCell className="max-w-[240px] truncate text-sm" title={model.Remark}>
                          {model.Remark || "-"}
                        </TableCell>
                        <TableCell>{model.MaxRetry}</TableCell>
                        <TableCell>{model.TimeOut}</TableCell>
                        <TableCell className="text-sm text-muted-foreground">{renderStrategy(model.Strategy)}</TableCell>
                        <TableCell>
                          <span className={model.IOLog ? "text-green-500" : "text-red-500"}>
                            {model.IOLog ? '✓' : '✗'}
                          </span>
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-2">
                            <Button
                              variant="secondary"
                              size="icon"
                              onClick={() => navigate(`/model-providers?modelId=${model.ID}`)}
                            >
                              <Link className="h-4 w-4" />
                            </Button>
                            <Button variant="outline" size="icon" onClick={() => openEditDialog(model)}>
                              <Pencil className="h-4 w-4" />
                            </Button>
                            <AlertDialog>
                              <AlertDialogTrigger asChild>
                                <Button variant="destructive" size="icon" onClick={() => openDeleteDialog(model.ID)}>
                                  <Trash2 className="h-4 w-4" />
                                </Button>
                              </AlertDialogTrigger>
                              <AlertDialogContent>
                                <AlertDialogHeader>
                                  <AlertDialogTitle>确定要删除这个模型吗？</AlertDialogTitle>
                                  <AlertDialogDescription>此操作无法撤销。这将永久删除该模型。</AlertDialogDescription>
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
              {models.map((model) => (
                <div key={model.ID} className="py-3 space-y-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 flex-1">
                      <h3 className="font-semibold text-sm truncate">{model.Name}</h3>
                      <p className="text-[11px] text-muted-foreground">ID: {model.ID}</p>
                    </div>
                    <div className="flex flex-wrap justify-end gap-1.5">
                      <Button variant="secondary" size="icon" className="h-7 w-7" onClick={() => navigate(`/model-providers?modelId=${model.ID}`)}>
                        <Link className="h-3.5 w-3.5" />
                      </Button>
                      <Button variant="outline" size="icon" className="h-7 w-7" onClick={() => openEditDialog(model)}>
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button variant="destructive" size="icon" className="h-7 w-7" onClick={() => openDeleteDialog(model.ID)}>
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>确定要删除这个模型吗？</AlertDialogTitle>
                            <AlertDialogDescription>此操作无法撤销。这将永久删除该模型。</AlertDialogDescription>
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
                    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">备注</p>
                    <p className="break-words">{model.Remark || "-"}</p>
                  </div>
                  <div className="grid grid-cols-2 gap-3 text-xs">
                    <MobileInfoItem label="重试次数" value={model.MaxRetry} />
                    <MobileInfoItem label="超时时间" value={`${model.TimeOut} 秒`} />
                    <MobileInfoItem label="负载策略" value={renderStrategy(model.Strategy)} />
                    <MobileInfoItem
                      label="IO 记录"
                      value={<span className={model.IOLog ? "text-green-600" : "text-red-600"}>{model.IOLog ? '✓' : '✗'}</span>}
                    />
                  </div>
                </div>
              ))}
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
              <SelectTrigger className="h-8 w-[100px] text-xs">
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
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingModel ? "编辑模型" : "添加模型"}
            </DialogTitle>
            <DialogDescription>
              {editingModel
                ? "修改模型信息"
                : "添加一个新的模型"}
            </DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(editingModel ? handleUpdate : handleCreate)} className="space-y-4">
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
                  control={form.control}
                  name="max_retry"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>重试次数限制</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          {...field}
                          onChange={e => field.onChange(+e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="time_out"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>超时时间(秒)</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          {...field}
                          onChange={e => field.onChange(+e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={form.control}
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
                control={form.control}
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
                control={form.control}
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
                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                  取消
                </Button>
                <Button type="submit">
                  {editingModel ? "更新" : "创建"}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
