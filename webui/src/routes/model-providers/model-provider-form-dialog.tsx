import type { FieldArrayWithId, UseFormReturn } from "react-hook-form";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { RefreshCw } from "lucide-react";
import type { Model, Provider, ProviderModel } from "@/lib/api";
import type { ModelWithProvider } from "@/lib/api";
import type { ModelProviderFormValues } from "./use-model-provider-form";

type ModelProviderFormDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  form: UseFormReturn<ModelProviderFormValues>;
  onSubmit: (values: ModelProviderFormValues) => Promise<void>;
  editingAssociation: ModelWithProvider | null;
  models: Model[];
  providers: Provider[];
  headerFields: FieldArrayWithId<ModelProviderFormValues, "customer_headers", "id">[];
  appendHeader: (value: { key: string; value: string }) => void;
  removeHeader: (index: number) => void;
  showProviderModels: boolean;
  setShowProviderModels: (show: boolean) => void;
  selectedProviderId: number;
  providerModelsMap: Record<number, ProviderModel[]>;
  providerModelsLoading: Record<number, boolean>;
  sortProviderModels: (providerId: number, query: string) => ProviderModel[];
  loadProviderModels: (providerId: number, force?: boolean) => Promise<void>;
};

export function ModelProviderFormDialog({
  open,
  onOpenChange,
  form,
  onSubmit,
  editingAssociation,
  models,
  providers,
  headerFields,
  appendHeader,
  removeHeader,
  showProviderModels,
  setShowProviderModels,
  selectedProviderId,
  providerModelsMap,
  providerModelsLoading,
  sortProviderModels,
  loadProviderModels,
}: ModelProviderFormDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>
            {editingAssociation ? "编辑关联" : "添加关联"}
          </DialogTitle>
        </DialogHeader>

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className="flex flex-col gap-4 flex-1 min-h-0">
            <div className="space-y-4 overflow-y-auto pr-1 sm:pr-2 max-h-[60vh] flex-1 min-h-0">
              <div className="grid grid-cols-2 gap-3">
                <FormField
                  control={form.control}
                  name="model_id"
                  render={({ field }) => (
                    <FormItem className="min-w-0">
                      <FormLabel>模型</FormLabel>
                      <Select
                        value={field.value.toString()}
                        onValueChange={(value) => field.onChange(parseInt(value))}
                        disabled={!!editingAssociation}
                      >
                        <FormControl>
                          <SelectTrigger className="form-select w-full">
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
                    <FormItem className="min-w-0">
                      <FormLabel>提供商</FormLabel>
                      <Select
                        value={field.value ? field.value.toString() : ""}
                        onValueChange={(value) => {
                          const parsed = parseInt(value);
                          field.onChange(parsed);
                          form.setValue("provider_name", "");
                        }}
                      >
                        <FormControl>
                          <SelectTrigger className="form-select w-full">
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
              </div>

              <FormField
                control={form.control}
                name="provider_name"
                render={({ field }) => (
                  <FormItem className="space-y-2">
                    <FormLabel>提供商模型</FormLabel>
                    <FormControl>
                      <div className="relative">
                        <Input
                          {...field}
                          placeholder="输入或选择提供商模型"
                          onFocus={() => setShowProviderModels(true)}
                          onBlur={() => setTimeout(() => setShowProviderModels(false), 100)}
                          onChange={(e) => {
                            field.onChange(e.target.value);
                            setShowProviderModels(true);
                          }}
                        />
                        {showProviderModels && (providerModelsMap[selectedProviderId] || []).length > 0 && (
                          <div className="absolute z-10 mt-1 w-full rounded-md border bg-popover text-popover-foreground shadow-sm max-h-52 overflow-y-auto">
                            {sortProviderModels(selectedProviderId, field.value || "").map((model) => (
                              <button
                                key={model.id}
                                type="button"
                                className="w-full text-left px-3 py-2 text-sm hover:bg-accent hover:text-accent-foreground"
                                onMouseDown={(e) => {
                                  e.preventDefault();
                                  field.onChange(model.id);
                                  setShowProviderModels(false);
                                }}
                              >
                                {model.id}
                              </button>
                            ))}
                          </div>
                        )}
                      </div>
                    </FormControl>
                    {selectedProviderId ? (
                      <div className="flex items-center justify-between text-xs text-muted-foreground">
                        <p>可直接输入，或在下拉列表中选择</p>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => loadProviderModels(selectedProviderId, true)}
                          disabled={!!providerModelsLoading[selectedProviderId]}
                        >
                          {providerModelsLoading[selectedProviderId] ? (
                            <Spinner className="size-4" />
                          ) : (
                            <RefreshCw className="size-4" />
                          )}
                        </Button>
                      </div>
                    ) : (
                      <p className="text-xs text-muted-foreground">请选择提供商以加载模型列表</p>
                    )}
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
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
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
  );
}
