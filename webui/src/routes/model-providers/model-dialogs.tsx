import { useEffect } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import type { Model } from "@/lib/api"
import {
  emptyModelForm,
  modelEditSchema,
  toFormValues,
  type ModelFormValues,
} from "@/routes/model-providers/use-model-editor"

type ModelFormDialogProps = {
  open: boolean
  /** 有值即编辑，无值即新建——标题、说明与提交后的动作都由它决定 */
  editingModel: Model | null
  saving: boolean
  onClose: () => void
  onSubmit: (values: ModelFormValues) => void
}

/**
 * 模型的新建 / 编辑对话框。
 *
 * 表单实例放在对话框内部：它在页面里只被这个对话框使用（页面另一处
 * `setValue` 操作的是"关联"那张表单，与此无关），留在页面只会让页面多背一个
 * 与它无关的 useForm。初值在**打开时**铺，而不是在父组件里预先 reset——
 * 打开是唯一需要初值的时刻。
 */
export function ModelFormDialog({ open, editingModel, saving, onClose, onSubmit }: ModelFormDialogProps) {
  const { t } = useTranslation(["models", "common"])
  const form = useForm<ModelFormValues>({
    resolver: zodResolver(modelEditSchema),
    defaultValues: emptyModelForm,
  })

  useEffect(() => {
    if (open) form.reset(editingModel ? toFormValues(editingModel) : emptyModelForm)
  }, [open, editingModel, form])

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{editingModel ? t("model_form.edit_title") : t("model_form.add_title")}</DialogTitle>
          <DialogDescription>
            {editingModel ? t("model_form.edit_desc") : t("model_form.add_desc")}
          </DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
            <FormField
              control={form.control}
              name="name"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t("model_form.name")}</FormLabel>
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
                  <FormLabel>{t("model_form.remark")}</FormLabel>
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
                    <FormLabel>{t("model_form.max_retry")}</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        {...field}
                        onChange={(e) => field.onChange(+e.target.value)}
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
                    <FormLabel>{t("model_form.timeout")}</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        {...field}
                        onChange={(e) => field.onChange(+e.target.value)}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name="breaker"
              render={({ field }) => (
                <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                  <div className="space-y-0.5">
                    <FormLabel className="text-base">{t("model_form.breaker")}</FormLabel>
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
                    <FormLabel className="text-base">{t("model_form.strategy")}</FormLabel>
                  </div>
                  <div className="grid gap-2 sm:grid-cols-2">
                    {[
                      {
                        value: "lottery",
                        title: t("model_form.strategy_lottery_title"),
                        desc: t("model_form.strategy_lottery_desc"),
                      },
                      {
                        value: "rotor",
                        title: t("model_form.strategy_rotor_title"),
                        desc: t("model_form.strategy_rotor_desc"),
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
              <Button type="button" variant="outline" onClick={onClose} disabled={saving}>
                {t("model_form.cancel")}
              </Button>
              <Button type="submit" disabled={saving}>
                {saving ? t("model_form.saving") : editingModel ? t("common:actions.update") : t("common:actions.create")}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}

type ModelDeleteDialogProps = {
  open: boolean
  /** 待删除的模型：名字取自它，页面不必再回查 */
  model: Model | null
  deleting: boolean
  onClose: () => void
  onConfirm: () => void
}

export function ModelDeleteDialog({ open, model, deleting, onClose, onConfirm }: ModelDeleteDialogProps) {
  const { t } = useTranslation(["models", "common"])

  return (
    <AlertDialog open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("delete_model_dialog.title")}</AlertDialogTitle>
          <AlertDialogDescription>
            {t("delete_model_dialog.description", { name: model ? `「${model.Name}」` : "" })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleting}>{t("common:actions.cancel")}</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm} disabled={deleting}>
            {deleting ? t("common:actions.deleting") : t("common:actions.confirm_delete")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
