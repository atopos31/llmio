import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

/**
 * 首次进入时自动弹出的使用说明。
 *
 * 内容分五节，顺序按"读完就能用"排：先讲地址怎么读（最容易出错的地方），
 * 再讲三种转发模式、三种改写策略，最后是鉴权与常见误区。地址表里三种协议
 * 的基地址形状不同，这正是旧版提供商页"一律 /v1"出错的地方。
 */
export function ForwardingGuideDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation("forwarding")
  const origin = window.location.origin

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("guide.title")}</DialogTitle>
          <DialogDescription>{t("guide.intro")}</DialogDescription>
        </DialogHeader>

        <div className="max-h-[65vh] space-y-5 overflow-y-auto pr-1 text-sm">
          <section className="space-y-2">
            <h3 className="text-sm font-semibold">{t("guide.address_title")}</h3>
            <p className="text-xs text-muted-foreground">
              {t("guide.address_desc", { origin })}
            </p>
            <div className="overflow-x-auto rounded-md border">
              <table className="w-full text-left text-xs">
                <caption className="border-b bg-muted/30 px-3 py-2 text-left text-xs font-medium">
                  {t("guide.address_table_caption")}
                </caption>
                <thead className="bg-muted/50 text-muted-foreground">
                  <tr>
                    <th scope="col" className="px-3 py-2 font-medium">{t("guide.address_protocol")}</th>
                    <th scope="col" className="px-3 py-2 font-medium">{t("guide.address_base")}</th>
                    <th scope="col" className="px-3 py-2 font-medium">{t("guide.address_client_path")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y">
                  <tr>
                    <td className="px-3 py-2">{t("protocol.openai")}</td>
                    <td className="px-3 py-2 font-mono">{`${origin}/p/slug/v1`}</td>
                    <td className="px-3 py-2 font-mono text-muted-foreground">/chat/completions</td>
                  </tr>
                  <tr>
                    <td className="px-3 py-2">{t("protocol.anthropic")}</td>
                    <td className="px-3 py-2 font-mono">{`${origin}/p/slug`}</td>
                    <td className="px-3 py-2 font-mono text-muted-foreground">/v1/messages</td>
                  </tr>
                  <tr>
                    <td className="px-3 py-2">{t("protocol.gemini")}</td>
                    <td className="px-3 py-2 font-mono">{`${origin}/p/slug/v1beta`}</td>
                    <td className="px-3 py-2 font-mono text-muted-foreground">
                      /models/{"{model}"}:generateContent
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p className="text-xs text-muted-foreground">
              {t("guide.address_note", { origin })}
            </p>
          </section>

          <section className="space-y-2">
            <h3 className="text-sm font-semibold">{t("guide.mode_title")}</h3>
            <ul className="space-y-1.5 text-xs">
              <li>
                <span className="font-medium">{t("mode.off")}</span>
                <span className="text-muted-foreground"> · {t("guide.mode_off")}</span>
              </li>
              <li>
                <span className="font-medium">{t("mode.mixed")}</span>
                <span className="text-muted-foreground"> · {t("guide.mode_mixed")}</span>
              </li>
              <li>
                <span className="font-medium">{t("mode.pure")}</span>
                <span className="text-muted-foreground"> · {t("guide.mode_pure")}</span>
              </li>
            </ul>
          </section>

          <section className="space-y-2">
            <h3 className="text-sm font-semibold">{t("guide.rewrite_title")}</h3>
            <ul className="space-y-1.5 text-xs">
              <li>
                <span className="font-medium">{t("rewrite.none")}</span>
                <span className="text-muted-foreground"> · {t("guide.rewrite_none")}</span>
              </li>
              <li>
                <span className="font-medium">{t("rewrite.known")}</span>
                <span className="text-muted-foreground"> · {t("guide.rewrite_known")}</span>
              </li>
              <li>
                <span className="font-medium">{t("rewrite.all")}</span>
                <span className="text-muted-foreground"> · {t("guide.rewrite_all")}</span>
              </li>
            </ul>
          </section>

          <section className="space-y-2">
            <h3 className="text-sm font-semibold">{t("guide.auth_title")}</h3>
            <p className="text-xs text-muted-foreground">{t("guide.auth_desc")}</p>
          </section>

          <section className="space-y-2">
            <h3 className="text-sm font-semibold">{t("guide.pitfalls_title")}</h3>
            <ul className="list-disc space-y-1 pl-4 text-xs text-muted-foreground">
              <li>{t("guide.pitfall_1")}</li>
              <li>{t("guide.pitfall_2")}</li>
              <li>{t("guide.pitfall_3")}</li>
            </ul>
          </section>
        </div>

        <DialogFooter>
          <Button onClick={() => onOpenChange(false)}>{t("guide.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
