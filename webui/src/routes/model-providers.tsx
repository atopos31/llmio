import { useState, useEffect, useRef } from "react";
import { useSearchParams } from "react-router-dom";
import { zodResolver } from "@hookform/resolvers/zod";
import { useFieldArray, useForm } from "react-hook-form";
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
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Checkbox } from "@/components/ui/checkbox";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import Loading from "@/components/loading";
import {
  getModelProviders,
  getModelProviderStatus,
  createModelProvider,
  updateModelProvider,
  updateModelProviderStatus,
  deleteModelProvider,
  getModels,
  getProviders,
  testModelProvider
} from "@/lib/api";
import type { ModelWithProvider, Model, Provider } from "@/lib/api";
import { fetchEventSource } from "@microsoft/fetch-event-source";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "sonner";

// 定义表单验证模式
const headerPairSchema = z.object({
  key: z.string().min(1, { message: "请求头键不能为空" }),
  value: z.string().default(""),
});

const formSchema = z.object({
  model_id: z.number().positive({ message: "模型ID必须大于0" }),
  provider_name: z.string().min(1, { message: "提供商模型名称不能为空" }),
  provider_id: z.number().positive({ message: "提供商ID必须大于0" }),
  tool_call: z.boolean(),
  structured_output: z.boolean(),
  image: z.boolean(),
  with_header: z.boolean(),
  weight: z.number().positive({ message: "权重必须大于0" }),
  customer_headers: z.array(headerPairSchema).default([]),
});

type FormValues = z.input<typeof formSchema>;

export default function ModelProvidersPage() {
  const [modelProviders, setModelProviders] = useState<ModelWithProvider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [searchParams, setSearchParams] = useSearchParams();
  const [providerStatus, setProviderStatus] = useState<Record<number, boolean[]>>({});
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editingAssociation, setEditingAssociation] = useState<ModelWithProvider | null>(null);
  const [selectedModelId, setSelectedModelId] = useState<number | null>(null);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [testResults, setTestResults] = useState<Record<number, { loading: boolean; result: any }>>({});
  const [testDialogOpen, setTestDialogOpen] = useState(false);
  const [selectedTestId, setSelectedTestId] = useState<number | null>(null);
  const [testType, setTestType] = useState<"connectivity" | "react">("connectivity");
  const [selectedProviderType, setSelectedProviderType] = useState<string>("all");
  const [reactTestResult, setReactTestResult] = useState<{
    loading: boolean;
    messages: string;
    success: boolean | null;
    error: string | null;
  }>({
    loading: false,
    messages: "",
    success: null,
    error: null
  });
  const [statusUpdating, setStatusUpdating] = useState<Record<number, boolean>>({});
  const [statusError, setStatusError] = useState<string | null>(null);

  const dialogClose = () => {
    setTestDialogOpen(false)
  };

  // 初始化表单
  const form = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      model_id: 0,
      provider_name: "",
      provider_id: 0,
      tool_call: false,
      structured_output: false,
      image: false,
      with_header: false,
      weight: 1,
      customer_headers: [],
    },
  });
  const { fields: headerFields, append: appendHeader, remove: removeHeader } = useFieldArray({
    control: form.control,
    name: "customer_headers",
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
    const parsedParam = modelIdParam ? Number(modelIdParam) : NaN;

    if (!Number.isNaN(parsedParam) && models.some(model => model.ID === parsedParam)) {
      if (selectedModelId !== parsedParam) {
        setSelectedModelId(parsedParam);
        form.setValue("model_id", parsedParam);
      }
      return;
    }

    const fallbackId = models[0].ID;
    if (selectedModelId !== fallbackId) {
      setSelectedModelId(fallbackId);
      form.setValue("model_id", fallbackId);
    }
    if (modelIdParam !== fallbackId.toString()) {
      const nextParams = new URLSearchParams(searchParams);
      nextParams.set("modelId", fallbackId.toString());
      setSearchParams(nextParams, { replace: true });
    }
  }, [models, searchParams, selectedModelId, form, setSearchParams]);

  useEffect(() => {
    if (selectedModelId) {
      fetchModelProviders(selectedModelId);
    }
  }, [selectedModelId]);

  const buildPayload = (values: FormValues) => {
    const headers: Record<string, string> = {};
    (values.customer_headers || []).forEach(({ key, value }) => {
      const trimmedKey = key.trim();
      if (trimmedKey) {
        headers[trimmedKey] = value ?? "";
      }
    });

    return {
      model_id: values.model_id,
      provider_name: values.provider_name,
      provider_id: values.provider_id,
      tool_call: values.tool_call,
      structured_output: values.structured_output,
      image: values.image,
      with_header: values.with_header,
      customer_headers: headers,
      weight: values.weight
    };
  };

  const fetchModels = async () => {
    try {
      const data = await getModels();
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

  const fetchModelProviders = async (modelId: number) => {
    try {
      setLoading(true);
      const data = await getModelProviders(modelId);
      setModelProviders(data.map(item => ({
        ...item,
        CustomerHeaders: item.CustomerHeaders || {}
      })));
      // 异步加载状态数据
      loadProviderStatus(data, modelId);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取模型提供商关联列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const loadProviderStatus = async (providers: ModelWithProvider[], modelId: number) => {
    const selectedModel = models.find(m => m.ID === modelId);
    if (!selectedModel) return;

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
  };

  const handleCreate = async (values: FormValues) => {
    try {
      await createModelProvider(buildPayload(values));
      setOpen(false);
      toast.success("模型提供商关联创建成功");
      form.reset({
        model_id: selectedModelId || 0,
        provider_name: "",
        provider_id: 0,
        tool_call: false,
        structured_output: false,
        image: false,
        with_header: false,
        weight: 1,
        customer_headers: []
      });
      if (selectedModelId) {
        fetchModelProviders(selectedModelId);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`创建模型提供商关联失败: ${message}`);
      console.error(err);
    }
  };

  const handleUpdate = async (values: FormValues) => {
    if (!editingAssociation) return;

    try {
      await updateModelProvider(editingAssociation.ID, buildPayload(values));
      setOpen(false);
      toast.success("模型提供商关联更新成功");
      setEditingAssociation(null);
      form.reset({
        model_id: 0,
        provider_name: "",
        provider_id: 0,
        tool_call: false,
        structured_output: false,
        image: false,
        with_header: false,
        weight: 1,
        customer_headers: []
      });
      if (selectedModelId) {
        fetchModelProviders(selectedModelId);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`更新模型提供商关联失败: ${message}`);
      console.error(err);
    }
  };

  const handleDelete = async () => {
    if (!deleteId) return;
    try {
      await deleteModelProvider(deleteId);
      setDeleteId(null);
      if (selectedModelId) {
        fetchModelProviders(selectedModelId);
      }
      toast.success("模型提供商关联删除成功");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除模型提供商关联失败: ${message}`);
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

  const handleTest = (id: number) => {
    currentControllerRef.current?.abort(); // 取消之前的请求
    setSelectedTestId(id);
    setTestType("connectivity");
    setTestDialogOpen(true);
    setReactTestResult({
      loading: false,
      messages: "",
      success: null,
      error: null
    });
  };

  const handleConnectivityTest = async (id: number) => {
    try {
      setTestResults(prev => ({
        ...prev,
        [id]: { loading: true, result: null }
      }));

      const result = await testModelProvider(id);
      setTestResults(prev => ({
        ...prev,
        [id]: { loading: false, result }
      }));
      return result;
    } catch (err) {
      setTestResults(prev => ({
        ...prev,
        [id]: { loading: false, result: { error: "测试失败" + err } }
      }));
      console.error(err);
      return { error: "测试失败" + err };
    }
  };


  const currentControllerRef = useRef<AbortController | null>(null);
  const handleReactTest = async (id: number) => {
    setReactTestResult(prev => ({
      ...prev,
      messages: "",
      loading: true,
    }));
    try {
      const token = localStorage.getItem("authToken");
      const controller = new AbortController();
      currentControllerRef.current = controller;
      await fetchEventSource(`/api/test/react/${id}`, {
        method: "GET",
        headers: {
          "Authorization": `Bearer ${token}`,
        },
        signal: controller.signal,
        onmessage(event) {
          setReactTestResult(prev => {
            if (event.event === "start") {
              return {
                ...prev,
                messages: prev.messages + `[开始测试] ${event.data}\n`
              };
            } else if (event.event === "toolcall") {
              return {
                ...prev,
                messages: prev.messages + `\n[调用工具] ${event.data}\n`
              };
            } else if (event.event === "toolres") {
              return {
                ...prev,
                messages: prev.messages + `\n[工具输出] ${event.data}\n`
              };
            }
            else if (event.event === "message") {
              if (event.data.trim()) {
                return {
                  ...prev,
                  messages: prev.messages + `${event.data}`
                };
              }
            } else if (event.event === "error") {
              return {
                ...prev,
                success: false,
                messages: prev.messages + `\n[错误] ${event.data}\n`
              };
            } else if (event.event === "success") {
              return {
                ...prev,
                success: true,
                messages: prev.messages + `\n[成功] ${event.data}`
              };
            }
            return prev;
          });
        },
        onclose() {
          setReactTestResult(prev => {
            return {
              ...prev,
              loading: false,
            };
          });
        },
        onerror(err) {
          setReactTestResult(prev => {
            return {
              ...prev,
              loading: false,
              error: err.message || "测试过程中发生错误",
              success: false
            };
          });
          throw err;
        }
      });
    } catch (err) {
      setReactTestResult(prev => ({
        ...prev,
        loading: false,
        error: "测试失败",
        success: false
      }));
      console.error(err);
    }
  };

  const executeTest = async () => {
    if (!selectedTestId) return;

    if (testType === "connectivity") {
      await handleConnectivityTest(selectedTestId);
    } else {
      await handleReactTest(selectedTestId);
    }
  };

  const openEditDialog = (association: ModelWithProvider) => {
    setEditingAssociation(association);
    const headerPairs = Object.entries(association.CustomerHeaders || {}).map(([key, value]) => ({
      key,
      value,
    }));
    form.reset({
      model_id: association.ModelID,
      provider_name: association.ProviderModel,
      provider_id: association.ProviderID,
      tool_call: association.ToolCall,
      structured_output: association.StructuredOutput,
      image: association.Image,
      with_header: association.WithHeader,
      weight: association.Weight,
      customer_headers: headerPairs.length ? headerPairs : [],
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    setEditingAssociation(null);
    form.reset({
      model_id: selectedModelId || 0,
      provider_name: "",
      provider_id: 0,
      tool_call: false,
      structured_output: false,
      image: false,
      with_header: false,
      weight: 1,
      customer_headers: []
    });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const handleModelChange = (modelId: string) => {
    const id = parseInt(modelId);
    setSelectedModelId(id);
    const nextParams = new URLSearchParams(searchParams);
    nextParams.set("modelId", id.toString());
    setSearchParams(nextParams);
    form.setValue("model_id", id);
  };

  // 获取唯一的提供商类型列表
  const providerTypes = Array.from(new Set(providers.map(p => p.Type).filter(Boolean)));

  // 根据选择的提供商类型过滤模型提供商关联
  const filteredModelProviders = selectedProviderType && selectedProviderType !== "all"
    ? modelProviders.filter(association => {
        const provider = providers.find(p => p.ID === association.ProviderID);
        return provider?.Type === selectedProviderType;
      })
    : modelProviders;




  if (loading && models.length === 0 && providers.length === 0) return <Loading message="加载模型和提供商" />;

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:justify-between sm:items-center gap-4">
        <h2 className="text-2xl font-bold">模型提供商关联</h2>
        <div className="flex flex-col sm:flex-row gap-2 w-full sm:w-auto">
          <Select value={selectedProviderType} onValueChange={setSelectedProviderType}>
            <SelectTrigger className="w-full sm:w-48">
              <SelectValue placeholder="按类型筛选" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部类型</SelectItem>
              {providerTypes.map((type) => (
                <SelectItem key={type} value={type}>
                  {type}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={selectedModelId?.toString() || ""} onValueChange={handleModelChange}>
            <SelectTrigger className="w-full sm:w-64">
              <SelectValue placeholder="选择模型" />
            </SelectTrigger>
            <SelectContent>
              {models.map((model) => (
                <SelectItem key={model.ID} value={model.ID.toString()}>
                  {model.Name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button onClick={openCreateDialog} disabled={!selectedModelId} className="w-full sm:w-auto">
            添加关联
          </Button>
        </div>
      </div>

      {statusError && (
        <div className="text-sm text-red-500">
          {statusError}
        </div>
      )}

      {!selectedModelId ? (
        <div>请选择一个模型来查看其提供商关联</div>
      ) : (
        <>
          {/* 桌面端表格 */}
          <div className="border rounded-lg hidden sm:block">
            <Table>
              <TableHeader>
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
                  <TableHead>状态</TableHead>
                  <TableHead>操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredModelProviders.map((association) => {
                  const provider = providers.find(p => p.ID === association.ProviderID);
                  const isAssociationEnabled = association.Status ?? false;
                  return (
                    <TableRow key={association.ID}>
                      <TableCell>{association.ID}</TableCell>
                      <TableCell>{association.ProviderModel}</TableCell>
                      <TableCell>{provider?.Type}</TableCell>
                      <TableCell>{provider ? provider.Name : '未知'}</TableCell>
                      <TableCell>
                        <span className={association.ToolCall ? "text-green-500" : "text-red-500"}>
                          {association.ToolCall ? '✓' : '✗'}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className={association.StructuredOutput ? "text-green-500" : "text-red-500"}>
                          {association.StructuredOutput ? '✓' : '✗'}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className={association.Image ? "text-green-500" : "text-red-500"}>
                          {association.Image ? '✓' : '✗'}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className={association.WithHeader ? "text-green-500" : "text-red-500"}>
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
                          <span className="text-sm text-gray-500">
                            {isAssociationEnabled ? "已启用" : "已停用"}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center space-x-4">
                          {providerStatus[association.ID] ? (
                            providerStatus[association.ID].length > 0 ? (
                              <div className="flex space-x-1 items-end h-6">
                                {providerStatus[association.ID].map((isSuccess, index) => (
                                  <div
                                    key={index}
                                    className={`w-1 h-6  ${isSuccess ? 'bg-green-500' : 'bg-red-500'
                                      }`}
                                    title={isSuccess ? '成功' : '失败'}
                                  />
                                ))}
                              </div>
                            ) : (
                              <div className="text-xs text-gray-400">无数据</div>
                            )
                          ) : (
                            <div className="text-xs text-gray-400">加载中...</div>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="space-x-2 text-right">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => openEditDialog(association)}
                        >
                          编辑
                        </Button>
                        <AlertDialog open={deleteId === association.ID} onOpenChange={(open) => !open && setDeleteId(null)}>
                          <Button
                            variant="destructive"
                            size="sm"
                            onClick={() => openDeleteDialog(association.ID)}
                          >
                            删除
                          </Button>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>确定要删除这个关联吗？</AlertDialogTitle>
                              <AlertDialogDescription>
                                此操作无法撤销。这将永久删除该模型提供商关联。
                              </AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel onClick={() => setDeleteId(null)}>取消</AlertDialogCancel>
                              <AlertDialogAction onClick={handleDelete}>确认删除</AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => handleTest(association.ID)}
                        >
                          测试
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          {/* 移动端卡片布局 */}
          <div className="sm:hidden space-y-4">
            {filteredModelProviders.map((association) => {
              const provider = providers.find(p => p.ID === association.ProviderID);
              const isAssociationEnabled = association.Status ?? true;
              return (
                <div key={association.ID} className="border rounded-lg p-4 space-y-3">
                  <div className="flex justify-between items-start">
                    <div>
                      <h3 className="font-bold text-lg">{provider ? provider.Name : '未知'}</h3>
                      <p className="text-sm text-gray-500">ID: {provider?.ID}</p>
                      <p className="text-sm text-gray-500">类型: {provider?.Type}</p>
                      <p className="text-sm text-gray-500">提供商模型: {association.ProviderModel}</p>
                      <p className="text-sm text-gray-500">
                        工具调用:
                        <span className={association.ToolCall ? "text-green-500" : "text-red-500"}>
                          {association.ToolCall ? '✓' : '✗'}
                        </span>
                      </p>
                      <p className="text-sm text-gray-500">
                        结构化输出:
                        <span className={association.StructuredOutput ? "text-green-500" : "text-red-500"}>
                          {association.StructuredOutput ? '✓' : '✗'}
                        </span>
                      </p>
                      <p className="text-sm text-gray-500">
                        视觉:
                        <span className={association.Image ? "text-green-500" : "text-red-500"}>
                          {association.Image ? '✓' : '✗'}
                        </span>
                      </p>
                      <p className="text-sm text-gray-500">
                        请求头透传:
                        <span className={association.WithHeader ? "text-green-500" : "text-red-500"}>
                          {association.WithHeader ? '✓' : '✗'}
                        </span>
                      </p>
                      <p className="text-sm text-gray-500">权重: {association.Weight}</p>
                      <div className="text-sm text-gray-500 flex items-center gap-2">
                        <span>状态:</span>
                        <div className="flex space-x-1 items-end h-4">
                          {providerStatus[association.ID] ? (
                            providerStatus[association.ID].length > 0 ? (
                              providerStatus[association.ID].map((isSuccess, index) => (
                                <div
                                  key={index}
                                  className={`w-0.75 h-4  ${isSuccess ? 'bg-green-500' : 'bg-red-500'
                                    }`}
                                  title={isSuccess ? '成功' : '失败'}
                                />
                              ))
                            ) : (
                              <span className="text-xs">无数据</span>
                            )
                          ) : (
                            <span className="text-xs">加载中...</span>
                          )}
                        </div>
                      </div>
                    </div>
                    <div className="flex flex-col space-y-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => openEditDialog(association)}
                      >
                        编辑
                      </Button>
                      <AlertDialog open={deleteId === association.ID} onOpenChange={(open) => !open && setDeleteId(null)}>
                        <Button
                          variant="destructive"
                          size="sm"
                          onClick={() => openDeleteDialog(association.ID)}
                        >
                          删除
                        </Button>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>确定要删除这个关联吗？</AlertDialogTitle>
                            <AlertDialogDescription>
                              此操作无法撤销。这将永久删除该模型提供商关联。
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
                  <div className="flex items-center justify-between text-sm text-gray-500">
                    <span>{isAssociationEnabled ? "已启用" : "已停用"}</span>
                    <Switch
                      checked={isAssociationEnabled}
                      disabled={!!statusUpdating[association.ID]}
                      onCheckedChange={(value) => handleStatusToggle(association, value)}
                      aria-label="切换启用状态"
                    />
                  </div>
                  <div className="flex justify-between items-center">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handleTest(association.ID)}
                    >
                      测试
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        </>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[85vh] flex flex-col">
          <DialogHeader>
            <DialogTitle>
              {editingAssociation ? "编辑关联" : "添加关联"}
            </DialogTitle>
            <DialogDescription>
              {editingAssociation
                ? "修改模型提供商关联"
                : "添加一个新的模型提供商关联"}
            </DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(editingAssociation ? handleUpdate : handleCreate)} className="flex flex-col gap-4 flex-1 min-h-0">
              <div className="space-y-4 overflow-y-auto pr-1 sm:pr-2 max-h-[60vh] flex-1 min-h-0">
                <FormField
                  control={form.control}
                  name="model_id"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>模型</FormLabel>
                      <Select
                        value={field.value.toString()}
                        onValueChange={(value) => field.onChange(parseInt(value))}
                        disabled={!!editingAssociation}
                      >
                        <FormControl>
                          <SelectTrigger className="form-select">
                            <SelectValue placeholder="选择模型" />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          {models.map((model) => (
                            <SelectItem key={model.ID} value={model.ID.toString()}>
                              {model.Name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="provider_id"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>提供商</FormLabel>
                      <Select
                        value={field.value.toString()}
                        onValueChange={(value) => field.onChange(parseInt(value))}
                      >
                        <FormControl>
                          <SelectTrigger className="form-select">
                            <SelectValue placeholder="选择提供商" />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          {providers.map((provider) => (
                            <SelectItem key={provider.ID} value={provider.ID.toString()}>
                              {provider.Name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="provider_name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>提供商模型</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          placeholder="输入提供商模型名称"
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormLabel>模型能力</FormLabel>
                <FormField
                  control={form.control}
                  name="tool_call"
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
                          工具调用
                        </FormLabel>
                      </div>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="structured_output"
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
                          结构化输出
                        </FormLabel>
                      </div>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="image"
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
                          视觉
                        </FormLabel>
                      </div>
                    </FormItem>
                  )}
                />
                <FormLabel>参数配置</FormLabel>
                <FormField
                  control={form.control}
                  name="with_header"
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
                          请求头透传
                        </FormLabel>
                      </div>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="customer_headers"
                  render={({ field }) => {
                    const headerValues = field.value ?? [];
                    return (
                      <FormItem>
                        <div className="flex items-center justify-between">
                          <FormLabel>自定义请求头</FormLabel>
                          <Button type="button" variant="outline" size="sm" onClick={() => appendHeader({ key: "", value: "" })}>
                            添加
                          </Button>
                        </div>
                        <div className="space-y-2">
                          {headerFields.map((header, index) => {
                            const errorMsg = form.formState.errors.customer_headers?.[index]?.key?.message;
                            return (
                              <div key={header.id} className="space-y-1">
                                <div className="flex gap-2 items-center">
                                  <div className="flex-1">
                                    <Input
                                      placeholder="Header Key"
                                      value={headerValues[index]?.key ?? ""}
                                      onChange={(e) => {
                                        const next = [...headerValues];
                                        next[index] = { ...next[index], key: e.target.value };
                                        field.onChange(next);
                                      }}
                                    />
                                  </div>
                                  <div className="flex-1">
                                    <Input
                                      placeholder="Header Value"
                                      value={headerValues[index]?.value ?? ""}
                                      onChange={(e) => {
                                        const next = [...headerValues];
                                        next[index] = { ...next[index], value: e.target.value };
                                        field.onChange(next);
                                      }}
                                    />
                                  </div>
                                  <Button type="button" size="sm" variant="destructive" onClick={() => removeHeader(index)}>
                                    删除
                                  </Button>
                                </div>
                                {errorMsg && (
                                  <p className="text-sm text-red-500">
                                    {errorMsg}
                                  </p>
                                )}
                              </div>
                            );
                          })}
                          <p className="text-sm text-muted-foreground">
                              {"优先级: 提供商配置 > 自定义请求头 > 透传请求头"}
                            </p>
                        </div>
                      </FormItem>
                    );
                  }}
                />

                <FormField
                  control={form.control}
                  name="weight"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>权重 (必须大于0)</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type="number"
                          min="1"
                          onChange={(e) => field.onChange(parseInt(e.target.value) || 0)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                  取消
                </Button>
                <Button type="submit">
                  {editingAssociation ? "更新" : "创建"}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

      {/* Test Dialog */}
      <Dialog open={testDialogOpen} onOpenChange={setTestDialogOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>模型测试</DialogTitle>
            <DialogDescription>
              选择要执行的测试类型
            </DialogDescription>
          </DialogHeader>

          <RadioGroup value={testType} onValueChange={(value: string) => setTestType(value as "connectivity" | "react")} className="space-y-4">
            <div className="flex items-center space-x-2">
              <RadioGroupItem value="connectivity" id="connectivity" />
              <Label htmlFor="connectivity">连通性测试</Label>
            </div>
            <p className="text-sm text-gray-500 ml-6">测试模型提供商的基本连通性</p>

            <div className="flex items-center space-x-2">
              <RadioGroupItem value="react" id="react" />
              <Label htmlFor="react">React Agent 能力测试</Label>
            </div>
            <p className="text-sm text-gray-500 ml-6">测试模型的工具调用和反应能力</p>
          </RadioGroup>

          {testType === "connectivity" && (
            <div className="mt-4">
              {selectedTestId && testResults[selectedTestId]?.loading ? (
                <div className="flex items-center justify-center py-4">
                  <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-gray-900"></div>
                  <span className="ml-2">测试中...</span>
                </div>
              ) : selectedTestId && testResults[selectedTestId] ? (
                <div className={`p-4 rounded-md ${testResults[selectedTestId].result?.error ? "bg-red-100 text-red-800" : "bg-green-100 text-green-800"}`}>
                  <p>{testResults[selectedTestId].result?.error ? testResults[selectedTestId].result?.error : "测试成功"}</p>
                  {testResults[selectedTestId].result?.message && (
                    <p className="mt-2">{testResults[selectedTestId].result.message}</p>
                  )}
                </div>
              ) : (
                <p className="text-gray-500">点击"执行测试"开始测试</p>
              )}
            </div>
          )}

          {testType === "react" && (
            <div className="mt-4 max-h-96 min-w-0">
              {reactTestResult.loading ? (
                <div className="flex items-center justify-center py-4">
                  <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-gray-900"></div>
                  <span className="ml-2">测试中...</span>
                </div>
              ) : (
                <>
                  {reactTestResult.error ? (
                    <div className="p-4 rounded-md bg-red-100 text-red-800">
                      <p>测试失败: {reactTestResult.error}</p>
                    </div>
                  ) : reactTestResult.success !== null ? (
                    <div className={`p-4 rounded-md ${reactTestResult.success ? "bg-green-100 text-green-800" : "bg-red-100 text-red-800"}`}>
                      <p>{reactTestResult.success ? "测试成功！" : "测试失败"}</p>
                    </div>
                  ) : null}


                </>
              )}

              {reactTestResult.messages && <Textarea name="logs" className="mt-4 max-h-50 resize-none whitespace-pre overflow-x-auto" readOnly value={reactTestResult.messages}>
              </Textarea>}
            </div>
          )}

          <DialogFooter>
            <Button variant="outline" onClick={dialogClose}>
              关闭
            </Button>
            <Button onClick={executeTest} disabled={testType === "connectivity" ?
              (selectedTestId ? testResults[selectedTestId]?.loading : false) :
              reactTestResult.loading}>
              {testType === "connectivity" ?
                (selectedTestId && testResults[selectedTestId]?.loading ? "测试中..." : "执行测试") :
                (reactTestResult.loading ? "测试中..." : "执行测试")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
