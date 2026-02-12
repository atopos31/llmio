import { useEffect, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useFieldArray, useForm } from "react-hook-form";
import { z } from "zod";
import { createModelProvider, updateModelProvider } from "@/lib/api";
import type { Model, ModelWithProvider, ProviderModel } from "@/lib/api";
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
});

export type ModelProviderFormValues = z.input<typeof modelProviderFormSchema>;

type UseModelProviderFormParams = {
  selectedModelId: number | null;
  models: Model[];
  providerModelsMap: Record<number, ProviderModel[]>;
  loadProviderModels: (providerId: number, force?: boolean) => Promise<void>;
  onReload: () => Promise<void> | void;
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

  const buildPayload = (values: ModelProviderFormValues) => {
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
      weight: values.weight,
    };
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
    form.reset(getDefaultFormValues());
    setOpen(true);
  };

  const submit = async (values: ModelProviderFormValues) => {
    try {
      if (editingAssociation) {
        await updateModelProvider(editingAssociation.ID, buildPayload(values));
        toast.success("关联管理更新成功");
        setEditingAssociation(null);
      } else {
        await createModelProvider(buildPayload(values));
        toast.success("关联管理创建成功");
      }

      setOpen(false);
      form.reset(getDefaultFormValues());
      await onReload();
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
    openEditDialog,
    openCreateDialog,
    submit,
    sortProviderModels,
  };
};
