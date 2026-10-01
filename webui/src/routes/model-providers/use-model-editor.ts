import { useState } from "react"
import { toast } from "sonner"
import { z } from "zod"

import { createModel, updateModel, type Model } from "@/lib/api"

export const modelEditSchema = z.object({
  name: z.string().min(1, { message: "模型名称不能为空" }),
  remark: z.string(),
  max_retry: z.number().min(0, { message: "重试次数限制不能为负数" }),
  time_out: z.number().min(0, { message: "超时时间不能为负数" }),
  strategy: z.enum(["lottery", "rotor"]),
  breaker: z.boolean(),
  prefer_direct: z.boolean(),
})

export type ModelFormValues = z.infer<typeof modelEditSchema>

/** 新建时的初值，也是关闭对话框后的复位值 */
export const emptyModelForm: ModelFormValues = {
  name: "",
  remark: "",
  max_retry: 10,
  time_out: 60,
  strategy: "lottery",
  breaker: false,
  prefer_direct: true,
}

/** 编辑时把模型摊成表单值：strategy 只认这两个，其余一律回落到 lottery */
export function toFormValues(model: Model): ModelFormValues {
  return {
    name: model.Name,
    remark: model.Remark ?? "",
    max_retry: model.MaxRetry,
    time_out: model.TimeOut,
    strategy: model.Strategy === "rotor" ? "rotor" : "lottery",
    breaker: model.Breaker ?? false,
    // 没配过（老模型）按"开"显示：勾子若默认关，用户只是改个备注再保存，
    // 就会把候选池从"只挑本协议"悄悄换成"整池按权重摇"
    prefer_direct: model.PreferDirect ?? true,
  }
}

type Options = {
  onUpdated: (model: Model) => void
  onCreated: (model: Model) => void
}

/**
 * 模型（不是"模型-提供商关联"）的新建 / 编辑 / 保存。
 *
 * 只管对话框的开合与提交；新建出来的模型怎么并进列表由页面决定，
 * 因为模型列表的顺序与关联数都归页面所有。
 */
export function useModelEditor({ onUpdated, onCreated }: Options) {
  const [open, setOpen] = useState(false)
  const [editingModel, setEditingModel] = useState<Model | null>(null)
  const [saving, setSaving] = useState(false)

  const openEdit = (model: Model) => {
    setEditingModel(model)
    setOpen(true)
  }

  const openCreate = () => {
    setEditingModel(null)
    setOpen(true)
  }

  const close = () => {
    setOpen(false)
    setEditingModel(null)
    setSaving(false)
  }

  const submit = async (values: ModelFormValues) => {
    setSaving(true)
    try {
      if (editingModel) {
        const updated = await updateModel(editingModel.ID, {
          name: values.name,
          remark: values.remark,
          max_retry: values.max_retry,
          time_out: values.time_out,
          strategy: values.strategy,
          breaker: values.breaker,
          prefer_direct: values.prefer_direct,
        })
        onUpdated(updated)
        toast.success(`模型: ${updated.Name} 更新成功`)
      } else {
        const created = await createModel({
          name: values.name,
          remark: values.remark,
          max_retry: values.max_retry,
          time_out: values.time_out,
          strategy: values.strategy,
          breaker: values.breaker,
          prefer_direct: values.prefer_direct,
        })
        onCreated(created)
        toast.success(`模型: ${created.Name} 创建成功`)
      }
      close()
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      toast.error(`${editingModel ? "更新" : "创建"}模型失败: ${message}`)
    } finally {
      setSaving(false)
    }
  }

  return { open, editingModel, saving, openEdit, openCreate, close, submit }
}
