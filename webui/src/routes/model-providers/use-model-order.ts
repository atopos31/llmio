import { useEffect, useState, type DragEvent, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { updateModelOrder, type Model } from "@/lib/api"

/**
 * 模型展示顺序：DisplayOrder 大的在前，同序按 ID 倒序。
 * 顺序由页面自己定，不依赖后端返回的次序；拖拽排序保存的也是这个值。
 */
export function sortModelsByOrder(modelList: Model[]): Model[] {
  return [...modelList].sort((a, b) => {
    const orderA = a.DisplayOrder ?? 0
    const orderB = b.DisplayOrder ?? 0
    if (orderA !== orderB) return orderB - orderA
    return b.ID - a.ID
  })
}

function moveWithin(modelList: Model[], sourceId: number, targetId: number): Model[] {
  if (sourceId === targetId) return modelList
  const sourceIndex = modelList.findIndex((item) => item.ID === sourceId)
  const targetIndex = modelList.findIndex((item) => item.ID === targetId)
  if (sourceIndex === -1 || targetIndex === -1) return modelList
  if (sourceIndex === targetIndex) return modelList

  const next = [...modelList]
  const [moved] = next.splice(sourceIndex, 1)
  next.splice(targetIndex, 0, moved)
  return next
}

export type ModelOrder = {
  /** 当前展示顺序（拖拽期间就是拖动后的中间态） */
  orderedModels: Model[]
  /** 保存中：此时不接受新的拖拽，也不响应行点击 */
  orderSaving: boolean
  /** 可以拖：既不在保存中，也没有筛选——筛选态下拖拽会把局部顺序当成全量顺序保存 */
  dragEnabled: boolean
  draggingModelId: number | null
  dragOverModelId: number | null
  /** 刚拖完：抑制随之而来的那次点击，否则拖拽会顺带进入该模型的关联列表 */
  suppressClick: boolean
  onDragStart: (event: DragEvent<HTMLElement>, modelId: number) => void
  onDragOver: (event: DragEvent<HTMLElement>, modelId: number) => void
  onDrop: (event: DragEvent<HTMLElement>) => void
  onDragEnd: () => void
  /** 键盘路径：Alt+↑/↓ 上下移一位。返回是否由它处理了这个按键 */
  onKeyDown: (event: KeyboardEvent<HTMLElement>, modelId: number) => boolean
  /** 给读屏软件的一句话：刚移到第几位、为什么不能移。视觉上不显示 */
  announcement: string
}

/**
 * 模型列表的拖拽排序。
 *
 * 顺序状态之所以单独放在这里而不是留给页面：拖拽过程中的三种中间态
 * （正在拖谁、拖到谁头上、刚拖完要抑制点击）只有和顺序本身放在一起才讲得通，
 * 拆开就要在页面里互相同步四个 state。
 *
 * `onOrderSaved` 由页面提供：保存成功后按新顺序回写 DisplayOrder，
 * 顺序的唯一真相仍在页面的 models 上。
 */
export function useModelOrder(
  models: Model[],
  { filtering, onOrderSaved }: { filtering: boolean; onOrderSaved: (orderedModels: Model[]) => void }
): ModelOrder {
  const { t } = useTranslation(["models", "common"])
  const [orderedModels, setOrderedModels] = useState<Model[]>([])
  const [orderSaving, setOrderSaving] = useState(false)
  const [draggingModelId, setDraggingModelId] = useState<number | null>(null)
  const [dragOverModelId, setDragOverModelId] = useState<number | null>(null)
  const [suppressClick, setSuppressClick] = useState(false)
  const [announcement, setAnnouncement] = useState("")

  useEffect(() => {
    setOrderedModels(sortModelsByOrder(models))
  }, [models])

  const persistOrder = async (nextOrderedModels: Model[]) => {
    const nextModelIds = nextOrderedModels.map((model) => model.ID)
    const currentModelIds = sortModelsByOrder(models).map((model) => model.ID)
    if (nextModelIds.join(",") === currentModelIds.join(",")) return

    setOrderSaving(true)
    try {
      await updateModelOrder(nextModelIds)
      onOrderSaved(nextOrderedModels)
      toast.success("模型排序已保存")
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      toast.error(t("toast.order_save_failed", { message }))
      setOrderedModels(sortModelsByOrder(models))
    } finally {
      setOrderSaving(false)
    }
  }

  const handleDragStart = (event: DragEvent<HTMLElement>, modelId: number) => {
    if (orderSaving || filtering) {
      event.preventDefault()
      return
    }
    setDraggingModelId(modelId)
    setDragOverModelId(null)
    setSuppressClick(true)
    event.dataTransfer.effectAllowed = "move"
    event.dataTransfer.setData("text/plain", modelId.toString())
  }

  const handleDragOver = (event: DragEvent<HTMLElement>, targetModelId: number) => {
    if (filtering) return
    event.preventDefault()
    if (draggingModelId === null || draggingModelId === targetModelId) return
    setDragOverModelId(targetModelId)
    setOrderedModels((prev) => moveWithin(prev, draggingModelId, targetModelId))
  }

  const handleDrop = async (event: DragEvent<HTMLElement>) => {
    event.preventDefault()
    setDraggingModelId(null)
    setDragOverModelId(null)
    if (filtering) return
    await persistOrder(orderedModels)
  }

  const handleDragEnd = () => {
    setDraggingModelId(null)
    setDragOverModelId(null)
    setTimeout(() => setSuppressClick(false), 0)
  }

  /**
   * 键盘路径：焦点落在某一行上时 Alt+↑/↓ 把它上下移一位，就地保存。
   *
   * 用 Alt 而不是裸方向键：表格里方向键是行内浏览，抢掉会毁掉别处的键盘习惯。
   * 与拖拽共用同一条保存路径，因此"移一位"和"拖到某处"落库的方式完全一致；
   * 拖拽在筛选态下被禁用，这里也必须禁用——带着筛选移，移出来的局部顺序
   * 会被当成全量顺序保存，而用户看不到被筛掉的那些。
   *
   * 返回值是给调用方的：true 表示这个按键归它管，调用方可以据此把焦点找回原位。
   */
  const handleKeyDown = (event: KeyboardEvent<HTMLElement>, modelId: number): boolean => {
    if (!event.altKey) return false
    if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return false
    event.preventDefault()

    if (filtering) {
      setAnnouncement(t("order.blocked_by_filter"))
      return true
    }
    if (orderSaving) return true

    const index = orderedModels.findIndex((model) => model.ID === modelId)
    if (index === -1) return true

    const target = index + (event.key === "ArrowUp" ? -1 : 1)
    if (target < 0 || target >= orderedModels.length) {
      setAnnouncement(t("order.at_edge"))
      return true
    }

    const next = [...orderedModels]
    const [moved] = next.splice(index, 1)
    next.splice(target, 0, moved)
    setOrderedModels(next)
    setAnnouncement(t("order.moved", { name: moved.Name, position: target + 1 }))
    void persistOrder(next)
    return true
  }

  return {
    orderedModels,
    orderSaving,
    dragEnabled: !orderSaving && !filtering,
    draggingModelId,
    dragOverModelId,
    suppressClick,
    onDragStart: handleDragStart,
    onDragOver: handleDragOver,
    onDrop: handleDrop,
    onDragEnd: handleDragEnd,
    onKeyDown: handleKeyDown,
    announcement,
  }
}
