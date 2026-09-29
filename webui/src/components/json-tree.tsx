import { useState } from "react"
import { ChevronDown, ChevronRight, Copy, Check } from "lucide-react"

import { seriesVar } from "@/lib/palette"
import { cn } from "@/lib/utils"

/**
 * 结构化数据的查看器。
 *
 * 为什么不复用语法高亮库：这一页 99% 的内容是 JSON（请求体、工具参数、
 * usage），而语法高亮库为了把 JSON 染色要加载约 640KB 的样式表。这里用一棵
 * 自绘的树替代——体积归零，同时拿到高亮库给不了的东西：可折叠、按节点复制、
 * 直接看到数组长度与对象键数。
 *
 * 类型用**分类色板的固定槽位**着色（字符串/数字/布尔/null/键名五种身份），
 * 并且**每类都同时带文字提示**（右下角的类型名）——颜色在色盲下不可依赖，
 * 而"这是字符串还是数字"恰恰是读 JSON 时最需要分辨的。
 */

/** 五种类型的固定槽位。身份固定，不随数据变化。 */
const TYPE_SLOT = {
  key: 6, // 紫罗兰
  string: 5, // 绿
  number: 0, // 靛蓝
  boolean: 3, // 琥珀
  null: 7, // 红
} as const

type JsonType = keyof typeof TYPE_SLOT

function typeOf(v: unknown): JsonType {
  if (v === null) return "null"
  const t = typeof v
  if (t === "string") return "string"
  if (t === "number") return "number"
  if (t === "boolean") return "boolean"
  return "null"
}

function isContainer(v: unknown): v is Record<string, unknown> | unknown[] {
  return typeof v === "object" && v !== null
}

type Props = {
  data: unknown
  /** 展开深度。超过则默认折起，避免大请求体一上来就滚不到头。 */
  defaultDepth?: number
  className?: string
}

export function JsonTree({ data, defaultDepth = 2, className }: Props) {
  return (
    <div
      className={cn(
        "w-full overflow-x-auto rounded-md border border-border bg-muted/30 p-3",
        "font-mono text-xs leading-relaxed",
        className
      )}
    >
      <Node value={data} depth={0} defaultDepth={defaultDepth} name={null} isLast />
    </div>
  )
}

function Node({
  value,
  name,
  depth,
  defaultDepth,
  isLast,
}: {
  value: unknown
  name: string | null
  depth: number
  defaultDepth: number
  isLast: boolean
}) {
  const [open, setOpen] = useState(depth < defaultDepth)

  if (!isContainer(value)) {
    return (
      <Row isLast={isLast}>
        {name !== null && <KeyName name={name} />}
        <Scalar value={value} />
      </Row>
    )
  }

  const entries: [string, unknown][] = Array.isArray(value)
    ? value.map((v, i) => [String(i), v])
    : Object.entries(value)
  const isArray = Array.isArray(value)
  const empty = entries.length === 0

  return (
    <div>
      <Row isLast={isLast && !open}>
        {!empty ? (
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            className="mr-0.5 inline-flex size-3.5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:text-foreground"
            aria-expanded={open}
            aria-label={open ? `折叠 ${name ?? "根"}` : `展开 ${name ?? "根"}`}
          >
            {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
          </button>
        ) : (
          <span className="mr-0.5 inline-block size-3.5 shrink-0" />
        )}
        {name !== null && <KeyName name={name} />}
        <span className="text-muted-foreground">
          {isArray ? "[" : "{"}
          {!open && (
            <span>
              {" "}
              <span className="text-foreground/70">
                {isArray ? `${entries.length} 项` : `${entries.length} 个键`}
              </span>{" "}
              {isArray ? "]" : "}"}
            </span>
          )}
        </span>
        {empty && <span className="text-muted-foreground">{isArray ? "]" : "}"}</span>}
        <TypeHint label={isArray ? "array" : "object"} />
      </Row>

      {open && !empty && (
        <div className="border-l border-border/60 pl-3">
          {entries.map(([k, v], i) => (
            <Node
              key={k}
              name={k}
              value={v}
              depth={depth + 1}
              defaultDepth={defaultDepth}
              isLast={i === entries.length - 1}
            />
          ))}
        </div>
      )}

      {open && !empty && (
        <div className="pl-4 text-muted-foreground">{isArray ? "]" : "}"}</div>
      )}
    </div>
  )
}

function Row({ children, isLast }: { children: React.ReactNode; isLast: boolean }) {
  return <div className={cn("flex items-start gap-1 whitespace-pre-wrap", !isLast && "mb-0.5")}>{children}</div>
}

function KeyName({ name }: { name: string }) {
  return (
    <>
      <span style={{ color: seriesVar(TYPE_SLOT.key) }} className="shrink-0">
        {name}
      </span>
      <span className="mr-1 shrink-0 text-muted-foreground">:</span>
    </>
  )
}

function Scalar({ value }: { value: unknown }) {
  const type = typeOf(value)
  const text =
    type === "string"
      ? JSON.stringify(value)
      : type === "null"
        ? "null"
        : String(value)

  // null 与布尔不加类型提示：它们的字面量本身就说明了类型，
  // 再标一次"null"只是重复。提示留给字符串与数字——这两者才看不出区别。
  const needsHint = type === "string" || type === "number"
  return (
    <>
      <span style={{ color: seriesVar(TYPE_SLOT[type]) }} className="break-all">
        {text}
      </span>
      {needsHint && <TypeHint label={type} />}
    </>
  )
}

/**
 * 类型文字提示。
 *
 * 颜色之外的第二条通道：色盲读者、以及把颜色调成灰度打印时，
 * 仍能分辨"这是字符串还是数字"。
 */
function TypeHint({ label }: { label: string }) {
  return (
    <>
      <span className="sr-only">{`（${label}）`}</span>
      <span
        aria-hidden="true"
        className="ml-auto shrink-0 pl-2 text-[10px] text-muted-foreground/70"
      >
        {label}
      </span>
    </>
  )
}

/** 带复制按钮的 JSON 视图。复制的是格式化后的完整 JSON 文本。 */
export function JsonViewerWithCopy({ data }: { data: unknown }) {
  const [copied, setCopied] = useState(false)
  const text = typeof data === "string" ? data : JSON.stringify(data, null, 2)

  return (
    <div className="relative w-full">
      <button
        type="button"
        className="absolute top-2 right-2 z-10 inline-flex items-center gap-1 rounded-sm border border-border bg-background px-1.5 py-0.5 text-[11px] text-muted-foreground hover:text-foreground"
        onClick={() => {
          void navigator.clipboard?.writeText(text).then(
            () => {
              setCopied(true)
              window.setTimeout(() => setCopied(false), 1500)
            },
            () => {
              /* 剪贴板不可用时静默：用户仍可手动选中复制 */
            }
          )
        }}
        aria-label="复制 JSON"
      >
        {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
        {copied ? "已复制" : "复制"}
      </button>
      <JsonTree data={data} />
    </div>
  )
}
