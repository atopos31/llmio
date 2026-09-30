import { Pencil, RefreshCw, Trash2, Zap } from "lucide-react"
import { useTranslation } from "react-i18next"

import Loading from "@/components/loading"
import { ErrorState } from "@/components/state-views"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Spinner } from "@/components/ui/spinner"
import type { ModelWithProvider, Provider } from "@/lib/api"
import { MobileInfoItem } from "@/routes/model-providers/mobile-info-item"

const Mark = ({ ok }: { ok: boolean }) => (
  <span className={ok ? "text-green-600" : "text-red-600"}>{ok ? "✓" : "✗"}</span>
)

type Props = {
  /** 已筛选、已排序的关联；筛选与排序都在页面里做完 */
  associations: ModelWithProvider[]
  providers: Provider[]
  /** 每个关联的近期成败条：缺键即"还没取到"，取到空数组即"没有数据"——两者说法不同 */
  providerStatus: Record<number, boolean[]>
  statusUpdating: Record<number, boolean>
  /** 正在确认删除的那条关联 ID */
  deleteId: number | null
  loading: boolean
  /** 取数失败的原文：有值就先说失败，绝不说"这个模型没有关联" */
  error: string | null
  onRetry: () => void
  emptyText: string
  onRefreshStatus: () => void
  onEdit: (association: ModelWithProvider) => void
  onTest: (associationId: number) => void
  onOpenDelete: (associationId: number) => void
  onCloseDelete: () => void
  onConfirmDelete: () => void
  onStatusToggle: (association: ModelWithProvider, nextStatus: boolean) => void
}

/**
 * 某个模型下的关联列表（面板整体：加载 / 空 / 有数据）。
 *
 * 与模型列表同样是桌面表格 + 手机卡片两套并行实现，改动要成对改。
 * 状态条有两种"没有"：`providerStatus` 里没有这个键 = 还在取，取到空数组 =
 * 取到了但近期没有记录，所以一处转圈、一处写"无数据"。
 */
export function AssociationList({
  associations,
  providers,
  providerStatus,
  statusUpdating,
  deleteId,
  loading,
  error,
  onRetry,
  emptyText,
  onRefreshStatus,
  onEdit,
  onTest,
  onOpenDelete,
  onCloseDelete,
  onConfirmDelete,
  onStatusToggle,
}: Props) {
  const { t } = useTranslation(["models", "common"])

  return (
    <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
      {error ? (
        <div className="p-3">
          <ErrorState
            title={t("load_failed_associations")}
            message={error}
            retryLabel={t("retry")}
            onRetry={onRetry}
          />
        </div>
      ) : loading ? (
        <div className="flex h-full items-center justify-center">
          <Loading message={t("loading_associations")} />
        </div>
      ) : associations.length === 0 ? (
        <div className="flex h-full items-center justify-center text-muted-foreground text-sm text-center px-6">
          {emptyText}
        </div>
      ) : (
        <div className="h-full flex flex-col">
          <div className="hidden sm:block flex-1 overflow-y-auto">
            <div className="w-full">
              <Table className="min-w-[1200px]">
                <TableHeader className="z-10 sticky top-0 bg-secondary/80 text-secondary-foreground">
                  <TableRow>
                    <TableHead>{t("association_table.id")}</TableHead>
                    <TableHead>{t("association_table.provider_model")}</TableHead>
                    <TableHead>{t("association_table.type")}</TableHead>
                    <TableHead>{t("association_table.provider")}</TableHead>
                    <TableHead>{t("association_table.tool_call")}</TableHead>
                    <TableHead>{t("association_table.structured_output")}</TableHead>
                    <TableHead>{t("association_table.vision")}</TableHead>
                    <TableHead>{t("association_table.with_header")}</TableHead>
                    <TableHead>{t("association_table.weight")}</TableHead>
                    <TableHead>{t("association_table.enabled")}</TableHead>
                    <TableHead>
                      <div className="flex items-center gap-1">{t("association_table.status")}
                        <Button
                          onClick={onRefreshStatus}
                          variant="ghost"
                          size="icon"
                          aria-label={t("actions.refresh_status")}
                          title={t("actions.refresh_status")}
                          className="rounded-full"
                        >
                          <RefreshCw className="size-4" />
                        </Button>
                      </div>
                    </TableHead>
                    <TableHead>{t("association_table.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {associations.map((association) => {
                    const provider = providers.find(p => p.ID === association.ProviderID);
                    const isAssociationEnabled = association.Status ?? false;
                    const statusBars = providerStatus[association.ID];
                    return (
                      <TableRow key={association.ID}>
                        <TableCell className="font-mono text-xs text-muted-foreground">{association.ID}</TableCell>
                        <TableCell className="max-w-[200px] truncate" title={association.ProviderModel}>
                          {association.ProviderModel}
                        </TableCell>
                        <TableCell>{provider?.Type ?? t("common:unknown")}</TableCell>
                        <TableCell>{provider?.Name ?? t("common:unknown")}</TableCell>
                        <TableCell>
                          <Mark ok={association.ToolCall} />
                        </TableCell>
                        <TableCell>
                          <Mark ok={association.StructuredOutput} />
                        </TableCell>
                        <TableCell>
                          <Mark ok={association.Image} />
                        </TableCell>
                        <TableCell>
                          <Mark ok={association.WithHeader} />
                        </TableCell>
                        <TableCell>{association.Weight}</TableCell>
                        <TableCell>
                          <div className="flex items-center gap-2">
                            <Switch
                              checked={isAssociationEnabled}
                              disabled={!!statusUpdating[association.ID]}
                              onCheckedChange={(value) => onStatusToggle(association, value)}
                              aria-label="切换启用状态"
                            />
                            <span className="text-xs text-muted-foreground">
                              {isAssociationEnabled ? t("association_table.active") : t("association_table.inactive")}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center space-x-4 w-20">
                            {statusBars ? (
                              statusBars.length > 0 ? (
                                <div className="flex space-x-1 items-end h-6">
                                  {statusBars.map((isSuccess, index) => (
                                    <div
                                      key={index}
                                      className={`w-1 h-6 ${isSuccess ? "bg-green-500" : "bg-red-500"}`}
                                      title={isSuccess ? t("association_table.success") : t("association_table.failed")}
                                    />
                                  ))}
                                </div>
                              ) : (
                                <div className="text-xs text-gray-400">{t("association_table.no_data")}</div>
                              )
                            ) : (
                              <Spinner />
                            )}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-2">
                            <Button variant="outline" size="icon" onClick={() => onEdit(association)}>
                              <Pencil className="h-4 w-4" />
                            </Button>
                            <Button variant="outline" size="icon" onClick={() => onTest(association.ID)}>
                              <Zap className="h-4 w-4" />
                            </Button>
                            <AlertDialog open={deleteId === association.ID} onOpenChange={(open) => !open && onCloseDelete()}>
                              <AlertDialogTrigger asChild>
                                <Button variant="destructive" size="icon" onClick={() => onOpenDelete(association.ID)}>
                                  <Trash2 className="h-4 w-4" />
                                </Button>
                              </AlertDialogTrigger>
                              <AlertDialogContent>
                                <AlertDialogHeader>
                                  <AlertDialogTitle>确定要删除这个关联吗？</AlertDialogTitle>
                                  <AlertDialogDescription>
                                    此操作无法撤销。这将永久删除该关联管理。
                                  </AlertDialogDescription>
                                </AlertDialogHeader>
                                <AlertDialogFooter>
                                  <AlertDialogCancel onClick={onCloseDelete}>取消</AlertDialogCancel>
                                  <AlertDialogAction onClick={onConfirmDelete}>确认删除</AlertDialogAction>
                                </AlertDialogFooter>
                              </AlertDialogContent>
                            </AlertDialog>
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          </div>
          <div className="sm:hidden flex-1 min-h-0 overflow-y-auto px-2 py-3 divide-y divide-border">
            {associations.map((association) => {
              const provider = providers.find(p => p.ID === association.ProviderID);
              const isAssociationEnabled = association.Status ?? true;
              const statusBars = providerStatus[association.ID];
              return (
                <div key={association.ID} className="py-3 space-y-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 flex-1">
                      <h3 className="font-semibold text-sm truncate">{provider?.Name ?? t("association_table.unknown_provider")}</h3>
                      <p className="text-[11px] text-muted-foreground">{t("association_table.mobile.provider_type")}: {association.ProviderModel}</p>
                    </div>
                    <span
                      className={`text-[11px] font-medium px-2 py-0.5 rounded-full ${isAssociationEnabled ? "bg-emerald-100 text-emerald-700" : "bg-red-100 text-red-700"}`}
                    >
                      {isAssociationEnabled ? t("association_table.active") : t("association_table.inactive")}
                    </span>
                  </div>
                  <div className="grid grid-cols-2 gap-3 text-xs">
                    <MobileInfoItem label={t("association_table.mobile.provider_type")} value={provider?.Type ?? t("common:unknown")} />
                    <MobileInfoItem label={t("association_table.mobile.provider_id")} value={<span className="font-mono text-xs">{provider?.ID ?? "-"}</span>} />
                    <MobileInfoItem label={t("association_table.mobile.weight")} value={association.Weight} />
                    <MobileInfoItem
                      label={t("association_table.mobile.with_header")}
                      value={<Mark ok={association.WithHeader} />}
                    />
                  </div>
                  <div className="grid grid-cols-2 gap-3 text-xs">
                    <MobileInfoItem
                      label={t("association_table.mobile.tool_call")}
                      value={<Mark ok={association.ToolCall} />}
                    />
                    <MobileInfoItem
                      label={t("association_table.mobile.structured_output")}
                      value={<Mark ok={association.StructuredOutput} />}
                    />
                    <MobileInfoItem
                      label={t("association_table.mobile.vision")}
                      value={<Mark ok={association.Image} />}
                    />
                    <MobileInfoItem
                      label={t("association_table.mobile.recent_status")}
                      value={
                        <div className="flex items-center gap-1">
                          {statusBars ? (
                            statusBars.length > 0 ? (
                              statusBars.map((isSuccess, index) => (
                                <div
                                  key={index}
                                  className={`w-1 h-4 rounded ${isSuccess ? "bg-green-500" : "bg-red-500"}`}
                                />
                              ))
                            ) : (
                              <span className="text-muted-foreground text-[11px]">{t("association_table.no_data")}</span>
                            )
                          ) : (
                            <Spinner />
                          )}
                        </div>
                      }
                    />
                  </div>
                  <div className="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2">
                    <p className="text-xs text-muted-foreground">{t("association_table.mobile.enable_status")}</p>
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium">{isAssociationEnabled ? t("association_table.active") : t("association_table.inactive")}</span>
                      <Switch
                        checked={isAssociationEnabled}
                        disabled={!!statusUpdating[association.ID]}
                        onCheckedChange={(value) => onStatusToggle(association, value)}
                        aria-label="切换启用状态"
                      />
                    </div>
                  </div>
                  <div className="flex flex-wrap justify-end gap-1.5">
                    <Button
                      variant="outline"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => onEdit(association)}
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="outline"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => onTest(association.ID)}
                    >
                      <Zap className="h-3.5 w-3.5" />
                    </Button>
                    <AlertDialog open={deleteId === association.ID} onOpenChange={(open) => !open && onCloseDelete()}>
                      <Button
                        variant="destructive"
                        size="icon"
                        className="h-7 w-7"
                        onClick={() => onOpenDelete(association.ID)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>确定要删除这个关联吗？</AlertDialogTitle>
                          <AlertDialogDescription>
                            此操作无法撤销。这将永久删除该关联管理。
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel onClick={onCloseDelete}>取消</AlertDialogCancel>
                          <AlertDialogAction onClick={onConfirmDelete}>确认删除</AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  )
}
