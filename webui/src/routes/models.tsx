import { useState, useEffect } from "react";
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
import { RefreshCw } from "lucide-react";

// 定义表单验证模式
const formSchema = z.object({
  name: z.string().min(1, { message: "模型名称不能为空" }),
  remark: z.string(),
  max_retry: z.number().min(0, { message: "重试次数限制不能为负数" }),
  time_out: z.number().min(0, { message: "超时时间不能为负数" }),
  io_log: z.boolean(),
});

export default function ModelsPage() {
  const navigate = useNavigate();
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<Model | null>(null);
  const [deleteId, setDeleteId] = useState<number | null>(null);

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: "",
      remark: "",
      max_retry: 10,
      time_out: 60,
      io_log: false,
    },
  });

  useEffect(() => {
    console.log("Fetching models...");
    fetchModels();
  }, []);

  const fetchModels = async () => {
    try {
      setLoading(true);
      const data = await getModels();
      setModels(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取模型列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (values: z.infer<typeof formSchema>) => {
    try {
      await createModel(values);
      setOpen(false);
      toast.success(`模型: ${values.name} 创建成功`);
      form.reset({ name: "", remark: "", max_retry: 10, time_out: 60, io_log: false });
      fetchModels();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`创建模型失败: ${message}`);
    }
  };

  const handleUpdate = async (values: z.infer<typeof formSchema>) => {
    if (!editingModel) return;
    try {
      await updateModel(editingModel.ID, values);
      setOpen(false);
      toast.success(`模型: ${values.name} 更新成功`);
      setEditingModel(null);
      form.reset({ name: "", remark: "", max_retry: 10, time_out: 60, io_log: false });
      fetchModels();
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
      fetchModels();
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
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    setEditingModel(null);
    form.reset({ name: "", remark: "", max_retry: 10, time_out: 60, io_log: false });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const handleRefresh = () => {
    fetchModels();
  };

  return (
    <div className="h-full min-h-0 flex flex-col gap-4 p-1">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-2xl font-bold tracking-tight">模型管理</h2>
          <p className="text-sm text-muted-foreground">维护可用模型、限时和重试等参数</p>
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
          <Button onClick={openCreateDialog} className="flex-1 sm:flex-none">
            添加模型
          </Button>
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
            <div className="hidden sm:block w-full overflow-x-auto">
              <Table className="min-w-[900px]">
                <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>名称</TableHead>
                    <TableHead>备注</TableHead>
                    <TableHead>重试次数限制</TableHead>
                    <TableHead>超时时间(秒)</TableHead>
                    <TableHead>IO 记录</TableHead>
                    <TableHead className="w-[220px]">操作</TableHead>
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
                      <TableCell>
                        <span className={model.IOLog ? "text-green-500" : "text-red-500"}>
                          {model.IOLog ? "开启" : "关闭"}
                        </span>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-2">
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => navigate(`/model-providers?modelId=${model.ID}`)}
                          >
                            关联
                          </Button>
                          <Button variant="outline" size="sm" onClick={() => openEditDialog(model)}>
                            编辑
                          </Button>
                          <AlertDialog>
                            <AlertDialogTrigger asChild>
                              <Button variant="destructive" size="sm" onClick={() => openDeleteDialog(model.ID)}>
                                删除
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
            <div className="sm:hidden px-2 py-3 divide-y divide-border">
              {models.map((model) => (
                <div key={model.ID} className="py-3 space-y-3 my-1 px-1">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 flex-1">
                      <h3 className="font-semibold text-base truncate">{model.Name}</h3>
                      <p className="text-[11px] text-muted-foreground">ID: {model.ID}</p>
                    </div>
                    <div className="flex flex-wrap justify-end gap-1.5">
                      <Button variant="secondary" size="sm" className="h-7 px-2 text-xs" onClick={() => navigate(`/model-providers?modelId=${model.ID}`)}>
                        关联
                      </Button>
                      <Button variant="outline" size="sm" className="h-7 px-2 text-xs" onClick={() => openEditDialog(model)}>
                        编辑
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button variant="destructive" size="sm" className="h-7 px-2 text-xs" onClick={() => openDeleteDialog(model.ID)}>
                            删除
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
                    <p className="text-muted-foreground">备注</p>
                    <p className="break-words">{model.Remark || "-"}</p>
                  </div>
                  <div className="grid grid-cols-2 gap-3 text-xs">
                    <div>
                      <p className="text-muted-foreground text-[11px] uppercase tracking-wide">重试次数</p>
                      <p className="font-medium">{model.MaxRetry}</p>
                    </div>
                    <div>
                      <p className="text-muted-foreground text-[11px] uppercase tracking-wide">超时时间</p>
                      <p className="font-medium">{model.TimeOut} 秒</p>
                    </div>
                    <div>
                      <p className="text-muted-foreground text-[11px] uppercase tracking-wide">IO 记录</p>
                      <p className={model.IOLog ? "text-green-600" : "text-red-600"}>{model.IOLog ? "开启" : "关闭"}</p>
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
                      <div className="text-sm text-muted-foreground">
                        是否记录输入输出日志
                      </div>
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
