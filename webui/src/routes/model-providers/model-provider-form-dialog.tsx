import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation(['models', 'common']);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>
            {editingAssociation ? t('association_form.edit_title') : t('association_form.add_title')}
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
                        <FormLabel>{t('association_form.model_label')}</FormLabel>
                      <Select
                        value={field.value.toString()}
                        onValueChange={(value) => field.onChange(parseInt(value))}
                        disabled={!!editingAssociation}
                      >
                        <FormControl>
                          <SelectTrigger className="form-select w-full">
                              <SelectValue placeholder={t('association_form.model_placeholder')} />
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
                        <FormLabel>{t('association_form.provider_label')}</FormLabel>
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
                              <SelectValue placeholder={t('association_form.provider_placeholder')} />
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
                    <FormLabel>{t('association_form.provider_model_label')}</FormLabel>
                    <FormControl>
                      <div className="relative">
                        <Input
                          {...field}
                          placeholder={t('association_form.provider_model_placeholder')}
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
                        <p>{t('association_form.provider_model_hint')}</p>
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
                      <p className="text-xs text-muted-foreground">{t('association_form.select_provider_first')}</p>
                    )}
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormLabel>{t('association_form.capabilities')}</FormLabel>
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
                        {t('association_form.tool_call')}
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
                        {t('association_form.structured_output')}
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
                        {t('association_form.vision')}
                      </FormLabel>
                    </div>
                  </FormItem>
                )}
              />
              <FormLabel>{t('association_form.params')}</FormLabel>
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
                        {t('association_form.with_header')}
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
                        <FormLabel>{t('association_form.custom_headers')}</FormLabel>
                        <Button type="button" variant="outline" size="sm" onClick={() => appendHeader({ key: "", value: "" })}>
                          {t('association_form.add_header')}
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
                                      placeholder={t('association_form.header_key_placeholder')}
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
                                      placeholder={t('association_form.header_value_placeholder')}
                                    value={headerValues[index]?.value ?? ""}
                                    onChange={(e) => {
                                      const next = [...headerValues];
                                      next[index] = { ...next[index], value: e.target.value };
                                      field.onChange(next);
                                    }}
                                  />
                                </div>
                                <Button type="button" size="sm" variant="destructive" onClick={() => removeHeader(index)}>
                                  {t('association_form.remove_header')}
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
                          {t('association_form.header_priority')}
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
                    <FormLabel>{t('association_form.weight')}</FormLabel>
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
                {t('association_form.cancel')}
              </Button>
              <Button type="submit">
                {editingAssociation ? t('common:actions.update') : t('common:actions.create')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
