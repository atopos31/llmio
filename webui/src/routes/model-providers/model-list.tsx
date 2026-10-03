import { Link, ListCollapse, Pencil, Trash2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import Loading from "@/components/loading"
import { ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type { Model } from "@/lib/api"
import { MobileInfoItem } from "@/components/mobile-info-item"
import type { ModelOrder } from "@/routes/model-providers/use-model-order"

const renderStrategy = (strategy?: string) => (strategy === "rotor" ? "Rotor" : "Lottery")

/**
 * 顺序一变，React 就按 key 把这些行在 DOM 里挪位置，而挪动节点在部分浏览器里
 * 会把焦点丢掉。按模型 ID 把焦点找回来——键盘操作得能连着按，
 * 不能每移一位就要重新 Tab 进来一次。
 */
function restoreFocus(row: HTMLElement, modelId: number) {
  requestAnimationFrame(() => {
    row
      .closest("[data-model-rows]")
      ?.querySelector<HTMLElement>(`[data-model-id="${modelId}"]`)
      ?.focus()
  })
}

type Props = {
  /** 已筛选、已排序的模型；筛选在页面里做完，这里只管画 */
  models: Model[]
  loading: boolean
  /** 取数失败的原文：有值就先说失败，绝不说"没有模型" */
  error: string | null
  onRetry: () => void
  /** 空态文案由页面给：筛空与本来就没有是两种说法 */
  emptyText: string
  associationCountText: (modelId: number) => string
  order: ModelOrder
  onSelect: (modelId: number) => void
  onEdit: (model: Model) => void
  onAddAssociation: (modelId: number) => void
  onDelete: (modelId: number) => void
}

/**
 * 模型列表（面板整体：加载 / 空 / 有数据）。
 *
 * 桌面是表格、手机是卡片，两套并行实现——同一份数据画两遍是既有的选择
 * （表格在手机上横不起来），因此这个文件里每一处字段都出现两次，
 * 改动要成对改。拖拽排序的行、点击进关联列表也都在这两套里各接一遍。
 */
export function ModelList({
  models,
  loading,
  error,
  onRetry,
  emptyText,
  associationCountText,
  order,
  onSelect,
  onEdit,
  onAddAssociation,
  onDelete,
}: Props) {
  const { t } = useTranslation(["models", "common"])

  return (
    <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
      {/* 排序结果的播报：读屏软件念这一句，视觉上不占位置 */}
      <div aria-live="polite" aria-atomic="true" className="sr-only">{order.announcement}</div>
      {error ? (
        <div className="p-3">
          <ErrorState
            title={t("load_failed_models")}
            message={error}
            retryLabel={t("retry")}
            onRetry={onRetry}
          />
        </div>
      ) : loading ? (
        <div className="flex h-full items-center justify-center">
          <Loading message={t("loading_models")} />
        </div>
      ) : models.length === 0 ? (
        <div className="flex h-full items-center justify-center text-muted-foreground">
          {emptyText}
        </div>
      ) : (
        <div className="h-full flex flex-col">
          <div className="hidden sm:block flex-1 overflow-y-auto" data-model-rows>
            <div className="w-full">
              <Table className="min-w-[1100px]">
                <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                  <TableRow>
                    <TableHead>{t("model_table.id")}</TableHead>
                    <TableHead>{t("model_table.name")}</TableHead>
                    <TableHead>{t("model_table.remark")}</TableHead>
                    <TableHead className="text-center">{t("model_table.associations")}</TableHead>
                    <TableHead className="text-center">{t("model_table.max_retry")}</TableHead>
                    <TableHead className="text-center">{t("model_table.timeout")}</TableHead>
                    <TableHead className="text-center">{t("model_table.strategy")}</TableHead>
                    <TableHead className="text-center">{t("model_table.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {models.map((model) => (
                    <TableRow
                      key={model.ID}
                      data-model-id={model.ID}
                      tabIndex={0}
                      aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
                      draggable={order.dragEnabled}
                      onDragStart={(event) => order.onDragStart(event, model.ID)}
                      onDragOver={(event) => order.onDragOver(event, model.ID)}
                      onDrop={order.onDrop}
                      onDragEnd={order.onDragEnd}
                      onKeyDown={(event) => {
                        const row = event.currentTarget
                        if (order.onKeyDown(event, model.ID)) restoreFocus(row, model.ID)
                      }}
                      className={`cursor-pointer transition-colors ${
                        order.draggingModelId === model.ID ? "opacity-60 ring-1 ring-primary/60" : ""
                      } ${
                        order.dragOverModelId === model.ID && order.draggingModelId !== model.ID ? "bg-accent/40" : ""
                      }`}
                      onClick={() => {
                        if (order.suppressClick || order.orderSaving) return;
                        onSelect(model.ID);
                      }}
                    >
                      <TableCell className="font-mono text-xs text-muted-foreground">{model.ID}</TableCell>
                      <TableCell className="font-medium">{model.Name}</TableCell>
                      <TableCell className="max-w-[240px] truncate text-sm" title={model.Remark || "-"}>
                        {model.Remark || "-"}
                      </TableCell>
                      <TableCell className="text-sm text-center">{associationCountText(model.ID)}</TableCell>
                      <TableCell className="text-center">{model.MaxRetry}</TableCell>
                      <TableCell className="text-center">{model.TimeOut}</TableCell>
                      <TableCell className="text-sm text-muted-foreground text-center">{renderStrategy(model.Strategy)}</TableCell>
                      <TableCell>
                        <div className="flex gap-2 justify-center">
                          <Button
                            variant="outline"
                            size="icon"
                            className="h-8 w-8"
                            onClick={(event) => {
                              event.stopPropagation();
                              onEdit(model);
                            }}
                            title={t("model_form.edit_title")}
                            aria-label={t("model_form.edit_title")}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="outline"
                            size="icon"
                            className="h-8 w-8"
                            onClick={(event) => {
                              event.stopPropagation();
                              onAddAssociation(model.ID);
                            }}
                            title={t("actions.add_association")}
                            aria-label={t("actions.add_association")}
                          >
                            <Link className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="outline"
                            size="icon"
                            className="h-8 w-8"
                            onClick={(event) => {
                              event.stopPropagation();
                              onSelect(model.ID);
                            }}
                            title={t("common:actions.search")}
                            aria-label={t("common:actions.search")}
                          >
                            <ListCollapse className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="destructive"
                            size="icon"
                            className="h-8 w-8"
                            onClick={(event) => {
                              event.stopPropagation();
                              onDelete(model.ID);
                            }}
                            aria-label={t("actions.delete_model")}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
          <div className="sm:hidden flex-1 min-h-0 overflow-y-auto px-2 py-3 divide-y divide-border" data-model-rows>
            {models.map((model) => (
              <div
                key={model.ID}
                data-model-id={model.ID}
                tabIndex={0}
                aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
                draggable={order.dragEnabled}
                onDragStart={(event) => order.onDragStart(event, model.ID)}
                onDragOver={(event) => order.onDragOver(event, model.ID)}
                onDrop={order.onDrop}
                onDragEnd={order.onDragEnd}
                onKeyDown={(event) => {
                  const row = event.currentTarget
                  if (order.onKeyDown(event, model.ID)) restoreFocus(row, model.ID)
                }}
                className={`py-3 space-y-3 transition-colors ${
                  order.draggingModelId === model.ID ? "opacity-60" : ""
                } ${
                  order.dragOverModelId === model.ID && order.draggingModelId !== model.ID ? "bg-accent/20 rounded-md" : ""
                }`}
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <h3 className="font-semibold text-sm truncate">{model.Name}</h3>
                    <p className="text-[11px] text-muted-foreground">{t("model_table.model_id", { id: model.ID })}</p>
                  </div>
                  <div className="flex gap-1.5">
                    <Button
                      variant="outline"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => onEdit(model)}
                      title={t("model_form.edit_title")}
                      aria-label={t("model_form.edit_title")}
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="outline"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => onAddAssociation(model.ID)}
                      title={t("actions.add_association")}
                      aria-label={t("actions.add_association")}
                    >
                      <Link className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="outline"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => onSelect(model.ID)}
                      title={t("common:actions.search")}
                      aria-label={t("common:actions.search")}
                    >
                      <ListCollapse className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="destructive"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => onDelete(model.ID)}
                      aria-label={t("actions.delete_model")}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </div>
                <div className="text-xs space-y-1">
                  <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{t("mobile.remark")}</p>
                  <p className="break-words">{model.Remark || "-"}</p>
                </div>
                <div className="grid grid-cols-2 gap-3 text-xs">
                  <MobileInfoItem label={t("mobile.max_retry")} value={model.MaxRetry} />
                  <MobileInfoItem label={t("mobile.timeout")} value={t("mobile.timeout_unit", { value: model.TimeOut })} />
                  <MobileInfoItem label={t("mobile.strategy")} value={renderStrategy(model.Strategy)} />
                  <MobileInfoItem label={t("mobile.associations")} value={associationCountText(model.ID)} />
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
