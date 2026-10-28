import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"

import type { ForwardModeSetting } from "@/lib/api"
import { cn } from "@/lib/utils"
import {
  forwardEndpointUrls,
  forwardingEndpoints,
  type EndpointHandling,
  type EndpointNoteKey,
  type ForwardEndpoint,
} from "@/routes/forwarding/forwarding-endpoints"

/**
 * 一个专属地址的端点清单。
 *
 * 分两段而不是一张平表：上面是"地址建好就能用"的本地端点，下面是"必须把转发
 * 模式切成混合或纯转发"才可用的端点。旧版的关键词是"关闭时只有 5 条已知端点"，
 * 那既不准确（Gemini 地址还有原生端点）也不说清各端点怎么处理，于是用户拿
 * /v1/embeddings 去试，收到 404 会以为地址配错了。
 */

const HANDLING_KEY = {
  pool: "endpoint_handling.pool",
  global: "endpoint_handling.global",
  forward: "endpoint_handling.forward",
} as const satisfies Record<EndpointHandling, string>

const NOTE_KEY = {
  models_anthropic: "endpoint_note.models_anthropic",
  bridge: "endpoint_note.bridge",
  count_tokens: "endpoint_note.count_tokens",
  gemini_native: "endpoint_note.gemini_native",
} as const satisfies Record<EndpointNoteKey, string>

type Item = { endpoint: ForwardEndpoint; url: string }

function EndpointRow({ item, t }: { item: Item; t: TFunction<"forwarding"> }) {
  const { endpoint, url } = item
  return (
    <li className="flex flex-wrap items-baseline gap-x-1.5">
      <span className="rounded bg-muted px-1 font-mono text-[10px] text-muted-foreground">
        {endpoint.method}
      </span>
      <span className="break-all font-mono">{url}</span>
      <span className="text-[10px] text-muted-foreground">{t(HANDLING_KEY[endpoint.handling])}</span>
      {endpoint.noteKey ? (
        <span className="w-full text-[10px] text-muted-foreground">{t(NOTE_KEY[endpoint.noteKey])}</span>
      ) : null}
    </li>
  )
}

export function ForwardEndpointList({
  origin,
  slug,
  type,
  mode,
  className,
}: {
  origin: string
  slug: string
  type: string
  mode: ForwardModeSetting
  className?: string
}) {
  const { t } = useTranslation("forwarding")
  if (!slug) return null

  const endpoints = forwardingEndpoints(type)
  const urls = forwardEndpointUrls(origin, slug, type)
  const local: Item[] = []
  const forwarded: Item[] = []
  endpoints.forEach((endpoint, index) => {
    const item = { endpoint, url: urls[index] }
    if (endpoint.handling === "forward") forwarded.push(item)
    else local.push(item)
  })

  return (
    <div className={cn("space-y-2 rounded border border-border bg-background/60 p-2 text-xs", className)}>
      <div className="space-y-1">
        <p className="font-medium text-muted-foreground">{t("endpoint_notice.local_title")}</p>
        <ul className="space-y-1">
          {local.map((item) => (
            <EndpointRow key={item.url} item={item} t={t} />
          ))}
        </ul>
      </div>
      <div className="space-y-1">
        <p className="font-medium text-muted-foreground">{t("endpoint_notice.forward_title")}</p>
        <p className="text-muted-foreground">{t("endpoint_notice.forward_hint")}</p>
        <ul className="space-y-1">
          {forwarded.map((item) => (
            <EndpointRow key={item.url} item={item} t={t} />
          ))}
        </ul>
        {mode === "off" ? (
          <p className="text-[11px] text-status-warning-ink">{t("endpoint_notice.off_hint")}</p>
        ) : null}
      </div>
    </div>
  )
}
