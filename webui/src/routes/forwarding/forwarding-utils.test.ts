import { describe, expect, it } from "vitest"

import {
  FORWARD_OFF_KNOWN_PATHS,
  forwardBaseUrl,
  forwardOffKnownUrls,
  forwardProtocol,
  forwardProviderMode,
  forwardProviderRewrite,
  isValidForwardSlug,
} from "@/routes/forwarding/forwarding-utils"

/**
 * 专属地址与展示状态的纯逻辑。
 *
 * 这里每一条都对应一个会让用户拿到"看起来对、粘进去却 404"的地址：
 * 协议归类错、给 Anthropic 补了 /v1、把升级前的空串改写当成 none。
 * 页面测试无法覆盖全部协议组合，所以规则本身单独钉住。
 */

describe("forwardProtocol", () => {
  it("按类型归到三种协议", () => {
    expect(forwardProtocol("openai")).toBe("openai")
    expect(forwardProtocol("openai_res")).toBe("openai")
    expect(forwardProtocol("anthropic")).toBe("anthropic")
    expect(forwardProtocol("gemini")).toBe("gemini")
  })

  it("大小写与首尾空白不影响判断", () => {
    expect(forwardProtocol(" Anthropic ")).toBe("anthropic")
    expect(forwardProtocol("GEMINI")).toBe("gemini")
  })

  it("未知或空类型按 OpenAI 兼容处理", () => {
    expect(forwardProtocol("")).toBe("openai")
    expect(forwardProtocol("bedrock")).toBe("openai")
  })
})

describe("forwardProviderMode", () => {
  it("null / false / true 分别映射 off / mixed / pure", () => {
    expect(forwardProviderMode({ ForwardMode: null })).toBe("off")
    expect(forwardProviderMode({ ForwardMode: false })).toBe("mixed")
    expect(forwardProviderMode({ ForwardMode: true })).toBe("pure")
  })
})

describe("forwardProviderRewrite", () => {
  it("none / all 原样返回", () => {
    expect(forwardProviderRewrite({ ForwardRewrite: "none" })).toBe("none")
    expect(forwardProviderRewrite({ ForwardRewrite: "all" })).toBe("all")
  })

  it("known 与升级前的空串都归一成 known", () => {
    expect(forwardProviderRewrite({ ForwardRewrite: "known" })).toBe("known")
    expect(forwardProviderRewrite({ ForwardRewrite: "" })).toBe("known")
    expect(forwardProviderRewrite({ ForwardRewrite: "unknown-value" })).toBe("known")
  })
})

describe("isValidForwardSlug", () => {
  it("接受字母数字开头、含 - _ . 的 1-64 位标识", () => {
    expect(isValidForwardSlug("gw")).toBe(true)
    expect(isValidForwardSlug("gw-a_1.2")).toBe(true)
    expect(isValidForwardSlug("0abc")).toBe(true)
    expect(isValidForwardSlug("a".repeat(64))).toBe(true)
  })

  it("拒绝空串、过长、非法首字符与路径字符", () => {
    expect(isValidForwardSlug("")).toBe(false)
    expect(isValidForwardSlug("a".repeat(65))).toBe(false)
    expect(isValidForwardSlug("-gw")).toBe(false)
    expect(isValidForwardSlug("_gw")).toBe(false)
    expect(isValidForwardSlug(".gw")).toBe(false)
    expect(isValidForwardSlug("gw/a")).toBe(false)
    expect(isValidForwardSlug("gw a")).toBe(false)
    expect(isValidForwardSlug("网关")).toBe(false)
    // 首字符限制同时挡掉了 "." 与 ".." 这类会被当成相对路径的段
    expect(isValidForwardSlug(".")).toBe(false)
    expect(isValidForwardSlug("..")).toBe(false)
  })
})

describe("forwardOffKnownUrls", () => {
  it("没有地址标识时返回空数组，绝不拼出 /p//v1 这种假地址", () => {
    expect(forwardOffKnownUrls("https://llm.example.com", "")).toEqual([])
  })

  it("列出的正是关闭模式下仍被接管的 5 条已知端点", () => {
    expect(FORWARD_OFF_KNOWN_PATHS).toHaveLength(5)
    expect(forwardOffKnownUrls("https://llm.example.com", "gw")).toEqual([
      "https://llm.example.com/p/gw/v1/chat/completions",
      "https://llm.example.com/p/gw/v1/responses",
      "https://llm.example.com/p/gw/v1/messages",
      "https://llm.example.com/p/gw/v1/messages/count_tokens",
      "https://llm.example.com/p/gw/v1/models",
    ])
  })

  it("同样去掉 origin 末尾的斜杠", () => {
    expect(forwardOffKnownUrls("https://llm.example.com//", "gw")[0]).toBe(
      "https://llm.example.com/p/gw/v1/chat/completions",
    )
  })
})

describe("forwardBaseUrl", () => {
  it("OpenAI 兼容带 /v1", () => {
    expect(forwardBaseUrl("https://llm.example.com", "openai", "gw")).toBe(
      "https://llm.example.com/p/gw/v1",
    )
  })

  it("Anthropic 不带 /v1：SDK 自己会拼 /v1/messages", () => {
    expect(forwardBaseUrl("https://llm.example.com", "anthropic", "gw")).toBe(
      "https://llm.example.com/p/gw",
    )
  })

  it("Gemini 带 /v1beta", () => {
    expect(forwardBaseUrl("https://llm.example.com", "gemini", "gw")).toBe(
      "https://llm.example.com/p/gw/v1beta",
    )
  })

  it("未知类型回落到 OpenAI 兼容形状", () => {
    expect(forwardBaseUrl("https://llm.example.com", "", "gw")).toBe(
      "https://llm.example.com/p/gw/v1",
    )
  })

  it("去掉 origin 末尾的斜杠，避免拼出 //p", () => {
    expect(forwardBaseUrl("https://llm.example.com/", "openai", "gw")).toBe(
      "https://llm.example.com/p/gw/v1",
    )
  })
})
