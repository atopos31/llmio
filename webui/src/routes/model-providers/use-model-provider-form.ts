import { useEffect, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useFieldArray, useForm } from "react-hook-form";
import { z } from "zod";
import { createModelProvider, updateModelProvider } from "@/lib/api";
import type { Model, ModelWithProvider, ProviderModel } from "@/lib/api";
import { termsFormToPayload, termsToForm, validateTermsForm } from "@/lib/peak";
import type { PeakTermsForm } from "@/lib/peak";
import { toast } from "sonner";

const headerPairSchema = z.object({
  key: z.string().min(1, { message: "请求头键不能为空" }),
  value: z.string().default(""),
});

export const modelProviderFormSchema = z.object({
  model_id: z.number().positive({ message: "模型ID必须大于0" }),
  provider_name: z.string().min(1, { message: "提供商模型名称不能为空" }),
  provider_id: z.number().positive({ message: "提供商ID必须大于0" }),
  tool_call: z.boolean(),
  structured_output: z.boolean(),
  image: z.boolean(),
  with_header: z.boolean(),
  weight: z.number().positive({ message: "权重必须大于0" }),
  customer_headers: z.array(headerPairSchema).default([]),
  extra_body: z.string().default(""),
  input_price: z.number().min(0).default(0),
  cache_read_price: z.number().min(0).default(0),
  output_price: z.number().min(0).default(0),
  currency: z.enum(["CNY", "USD"]).default("CNY"),
});

export type ModelProviderFormValues = z.input<typeof modelProviderFormSchema>;

type UseModelProviderFormParams = {
  selectedModelId: number | null;
  models: Model[];
  providerModelsMap: Record<number, ProviderModel[]>;
  loadProviderModels: (providerId: number, force?: boolean) => Promise<void>;
  onReload: (modelId: number) => Promise<void> | void;
};

export const useModelProviderForm = ({
  selectedModelId,
  models,
  providerModelsMap,
  loadProviderModels,
  onReload,
}: UseModelProviderFormParams) => {
  const [open, setOpen] = useState(false);
  const [editingAssociation, setEditingAssociation] = useState<ModelWithProvider | null>(null);
  const [showProviderModels, setShowProviderModels] = useState(false);
  /**
   * 峰谷条款，null = 这条关联没配（后端那一列落 NULL，按基础价计费）。
   *
   * 与 RHF 那份表单并列放在这里而不是塞进 modelProviderFormSchema：条款是
   * 可增删的时段数组，zod schema 给它只能编成一串嵌套字段，而校验要报的是
   * "第几段"（见 lib/peak.ts 的 validateTermsForm），渲染时又得从字段路径
   * 翻译回去。存成独立的 state，校验结论原样就是界面要的字。
   */
  const [peakTerms, setPeakTerms] = useState<PeakTermsForm | null>(null);
  /** 保存被条款校验拦下的次数。>0 时条款编辑器才开始显示红色（它据此判断"用户试过了"）。 */
  const [peakSubmitAttempt, setPeakSubmitAttempt] = useState(0);

  const getDefaultFormValues = (overrideModelId?: number): ModelProviderFormValues => {
    const fallbackModelId = overrideModelId ?? selectedModelId ?? models[0]?.ID ?? 0;
    return {
      model_id: fallbackModelId,
      provider_name: "",
      provider_id: 0,
      tool_call: false,
      structured_output: false,
      image: false,
      with_header: false,
      weight: 1,
      customer_headers: [],
      extra_body: "",
      input_price: 0,
      cache_read_price: 0,
      output_price: 0,
      currency: "CNY",
    };
  };

  const form = useForm<ModelProviderFormValues>({
    resolver: zodResolver(modelProviderFormSchema),
    defaultValues: getDefaultFormValues(),
  });

  const { fields: headerFields, append: appendHeader, remove: removeHeader } = useFieldArray({
    control: form.control,
    name: "customer_headers",
  });

  const selectedProviderId = form.watch("provider_id");

  useEffect(() => {
    if (selectedProviderId && selectedProviderId > 0) {
      loadProviderModels(selectedProviderId);
    }
    setShowProviderModels(false);
  }, [selectedProviderId, loadProviderModels]);

  const buildPayload = (values: ModelProviderFormValues, peak: PeakTermsForm | null) => {
    const headers: Record<string, string> = {};
    (values.customer_headers || []).forEach(({ key, value }) => {
      const trimmedKey = key.trim();
      if (trimmedKey) {
        headers[trimmedKey] = value ?? "";
      }
    });

    let extraBody: Record<string, unknown> = {};
    if (values.extra_body && values.extra_body.trim()) {
      try {
        extraBody = JSON.parse(values.extra_body);
      } catch {
        // ignore parse errors, send empty object
      }
    }

    return {
      model_id: values.model_id,
      provider_name: values.provider_name,
      provider_id: values.provider_id,
      tool_call: values.tool_call,
      structured_output: values.structured_output,
      image: values.image,
      with_header: values.with_header,
      customer_headers: headers,
      extra_body: extraBody,
      weight: values.weight,
      input_price: values.input_price ?? 0,
      cache_read_price: values.cache_read_price ?? 0,
      output_price: values.output_price ?? 0,
      currency: values.currency ?? "CNY",
      /**
       * 无条件带上 peak，null 也带。
       *
       * 更新接口把"省略 peak"和"peak: null"都当作清空（后端在结构体更新之外
       * 补了一次显式清空，否则 GORM 会跳过 nil 指针、旧条款一直留在行上），
       * 所以这两者在这里没有区别；但显式传 null 让"移除峰谷配置"这条路径在
       * 请求体里就看得见，而不是要读一遍后端代码才知道省略等于清空。
       */
      peak: peak ? termsFormToPayload(peak) : null,
    };
  };

  const openEditDialog = (association: ModelWithProvider) => {
    setEditingAssociation(association);
    const headerPairs = Object.entries(association.CustomerHeaders || {}).map(([key, value]) => ({
      key,
      value,
    }));
    let extraBodyStr = "";
    if (association.ExtraBody && Object.keys(association.ExtraBody).length > 0) {
      extraBodyStr = JSON.stringify(association.ExtraBody, null, 2);
    }
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
      extra_body: extraBodyStr,
      input_price: association.InputPrice ?? 0,
      cache_read_price: association.CacheReadPrice ?? 0,
      output_price: association.OutputPrice ?? 0,
      currency: (association.Currency as "CNY" | "USD") || "CNY",
    });
    // 条款跟着这条关联走：没配就是 null（三态里的"未配置"），配了才回填。
    // 用 termsToForm 而不是直接塞 association.Peak：乘数在表单里是字符串
    // （见 lib/peak.ts 的 PeakPeriodForm），"8:30" 也要顺手归一成 "08:30"。
    setPeakTerms(association.Peak ? termsToForm(association.Peak) : null);
    // 上一次保存被拦下的红色不能跟着飘到这一次打开：用户会以为新打开的这一条
    // 就有问题
    setPeakSubmitAttempt(0);
    setOpen(true);
  };

  const openCreateDialog = (modelId?: number) => {
    setEditingAssociation(null);
    form.reset(getDefaultFormValues(modelId));
    setPeakTerms(null);
    setPeakSubmitAttempt(0);
    setOpen(true);
  };

  const submit = async (values: ModelProviderFormValues) => {
    if (peakTerms) {
      const issues = validateTermsForm(peakTerms);
      if (issues.length > 0) {
        // 记一次尝试，条款编辑器据此把问题列出来并滚进视野；
        // 这里只负责拦，文案与呈现都在那边（它才知道是哪一段）
        setPeakSubmitAttempt((n) => n + 1);
        return;
      }
    }
    try {
      if (editingAssociation) {
        await updateModelProvider(editingAssociation.ID, buildPayload(values, peakTerms));
        toast.success("关联管理更新成功");
        setEditingAssociation(null);
      } else {
        await createModelProvider(buildPayload(values, peakTerms));
        toast.success("关联管理创建成功");
      }

      setOpen(false);
      form.reset(getDefaultFormValues());
      setPeakTerms(null);
      await onReload(values.model_id);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`${editingAssociation ? "更新" : "创建"}关联管理失败: ${message}`);
      console.error(err);
    }
  };

  const sortProviderModels = (providerId: number, query: string): ProviderModel[] => {
    const modelsForProvider = providerModelsMap[providerId] || [];
    if (!query) return modelsForProvider;

    const normalized = query.toLowerCase();
    const score = (id: string) => {
      const val = id.toLowerCase();
      if (val === normalized) return 1000;
      let s = 0;
      if (val.startsWith(normalized)) s += 500;
      if (val.includes(normalized)) s += 200;
      s -= Math.abs(val.length - normalized.length);
      return s;
    };

    return [...modelsForProvider].sort((a, b) => score(b.id) - score(a.id));
  };

  return {
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
  };
};
