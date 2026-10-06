import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Form, FormControl, FormField, FormItem, FormLabel } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { configAPI, defaultModelAutofillPolicy } from "@/lib/api";
import type { ModelAutofillPolicy } from "@/lib/api";

/**
 * 「模型能力与价格自动填写」配置卡（§5.3）。
 *
 * 自带取数，与峰谷日历、数据库压缩两张卡同样处理：并进配置页那次
 * Promise.all 的话，它读失败会把整页说成"读取现有配置失败"，而另外几张卡
 * 其实好着。
 *
 * 卡片上必须有一句说明**当前行为**：Enabled 默认是**开**，所以"没配过"
 * 在界面上看起来应该是"已启用"，否则用户会以为默认是关的、去找开关。
 */

const CONFIG_KEY = "model_autofill_policy";

const schema = z.object({
  enabled: z.boolean(),
  overwrite: z.boolean(),
  allow_deprecated: z.boolean(),
  /** 逗号分隔；留空即用默认。做成文本而不是列表，是因为这里几乎不用改 */
  sources: z.string(),
});

type FormValues = z.infer<typeof schema>;

const toForm = (policy: ModelAutofillPolicy): FormValues => ({
  enabled: policy.enabled,
  overwrite: policy.overwrite,
  allow_deprecated: policy.allow_deprecated,
  sources: (policy.sources ?? []).join(", "),
});

const toPolicy = (values: FormValues): ModelAutofillPolicy => ({
  enabled: values.enabled,
  overwrite: values.overwrite,
  allow_deprecated: values.allow_deprecated,
  sources: values.sources
    .split(",")
    .map((name) => name.trim())
    .filter(Boolean),
});

export function ModelAutofillCard() {
  const { t } = useTranslation(["config", "common"]);
  const [policy, setPolicy] = useState<ModelAutofillPolicy>(defaultModelAutofillPolicy);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: toForm(defaultModelAutofillPolicy),
  });

  const load = useCallback(async () => {
    try {
      setLoadError(null);
      const response = await configAPI.getConfig(CONFIG_KEY);
      if (!response.value) {
        // 这一行是新装时还没有：默认策略就是"开"，不是"没配过"。
        setPolicy(defaultModelAutofillPolicy);
        return;
      }
      const parsed = JSON.parse(response.value) as Partial<ModelAutofillPolicy>;
      setPolicy({
        ...defaultModelAutofillPolicy,
        ...parsed,
        sources: parsed.sources?.length ? parsed.sources : defaultModelAutofillPolicy.sources,
      });
    } catch (error) {
      // 失败不能静默：卡片上那时显示的是默认值，而默认值是"已启用"——等于
      // 替一个未知状态作了断言。说清楚"下面显示的可能不是已保存的值"。
      console.error("读取自动填写策略失败：", error);
      setLoadError(error instanceof Error ? error.message : String(error));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const openDialog = () => {
    form.reset(toForm(policy));
    setOpen(true);
  };

  const onSubmit = async (values: FormValues) => {
    const next = toPolicy(values);
    try {
      await configAPI.updateConfig(CONFIG_KEY, next);
      setPolicy(next);
      toast.success(t("toast.save_success"));
      setOpen(false);
    } catch (error) {
      console.error("保存自动填写策略失败：", error);
      toast.error(t("toast.save_failed"));
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium">{t("model_autofill.title")}</CardTitle>
        <CardDescription className="text-[11px]">{t("model_autofill.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {loadError ? (
          <p className="text-xs text-status-warning-ink">
            {t("model_autofill.load_failed", { message: loadError })}
          </p>
        ) : null}
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div className="space-y-2">
            <Label>{t("model_autofill.enabled")}</Label>
            <p className="text-sm text-muted-foreground">
              {policy.enabled ? t("model_autofill.enabled_on") : t("model_autofill.enabled_off")}
            </p>
          </div>
          <div className="space-y-2">
            <Label>{t("model_autofill.overwrite")}</Label>
            <p className="text-sm text-muted-foreground">
              {policy.overwrite ? t("model_autofill.overwrite_on") : t("model_autofill.overwrite_off")}
            </p>
          </div>
        </div>
        {/* 当前行为的陈述句。Enabled 默认是开，"没配过"看起来就是"已启用"。 */}
        <p className="text-xs text-muted-foreground">
          {policy.enabled ? t("model_autofill.status_on") : t("model_autofill.status_off")}
        </p>
      </CardContent>
      <CardFooter>
        <Button onClick={openDialog}>{t("model_autofill.edit")}</Button>
      </CardFooter>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("model_autofill.edit_title")}</DialogTitle>
            <DialogDescription>{t("model_autofill.edit_desc")}</DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <FormField
                control={form.control}
                name="enabled"
                render={({ field }) => (
                  <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                    <div className="space-y-0.5">
                      <FormLabel>{t("model_autofill.enabled")}</FormLabel>
                      <p className="text-xs text-muted-foreground">{t("model_autofill.enabled_help")}</p>
                    </div>
                    <FormControl>
                      <Switch checked={field.value} onCheckedChange={field.onChange} />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="overwrite"
                render={({ field }) => (
                  <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                    <div className="space-y-0.5">
                      <FormLabel>{t("model_autofill.overwrite")}</FormLabel>
                      <p className="text-xs text-muted-foreground">{t("model_autofill.overwrite_help")}</p>
                    </div>
                    <FormControl>
                      <Switch checked={field.value} onCheckedChange={field.onChange} />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="allow_deprecated"
                render={({ field }) => (
                  <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                    <div className="space-y-0.5">
                      <FormLabel>{t("model_autofill.allow_deprecated")}</FormLabel>
                      <p className="text-xs text-muted-foreground">{t("model_autofill.allow_deprecated_help")}</p>
                    </div>
                    <FormControl>
                      <Switch checked={field.value} onCheckedChange={field.onChange} />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="sources"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("model_autofill.sources")}</FormLabel>
                    <FormControl>
                      <Input placeholder="models.dev, litellm" {...field} />
                    </FormControl>
                    <p className="text-xs text-muted-foreground">{t("model_autofill.sources_help")}</p>
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                  {t("common:actions.cancel")}
                </Button>
                <Button type="submit">{t("common:actions.save")}</Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
