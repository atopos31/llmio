import { describe, expect, it } from "vitest"

import {
  forwardEndpointUrls,
  forwardingEndpoints,
} from "@/routes/forwarding/forwarding-endpoints"

/**
 * 端点清单是页面与设置弹窗共用的唯一数据源。
 *
 * 这里钉住的几条都属于"界面上说能用、真正请求却 404"的事故来源：
 * 协议判错导致路径前缀不对、把只有转发才可用的端点混进"本地端点"、
 * 以及没有标识时拼出 /p//v1 这种假地址。
 */

const localPaths = (type: string) =>
  forwardingEndpoints(type)
    .filter((endpoint) => endpoint.handling !== "forward")
    .map((endpoint) => endpoint.path)

const forwardPaths = (type: string) =>
  forwardingEndpoints(type)
    .filter((endpoint) => endpoint.handling === "forward")
    .map((endpoint) => endpoint.path)

describe("forwardingEndpoints", () => {
  it("本地端点排在需开启转发的端点之前", () => {
    const handling = forwardingEndpoints("openai").map((endpoint) => endpoint.handling)
    const firstForward = handling.indexOf("forward")
    expect(firstForward).toBeGreaterThan(0)
    expect(handling.slice(firstForward).every((item) => item === "forward")).toBe(true)
  })

  it("OpenAI 型与 Anthropic 型地址给出同样的 /v1 本地端点", () => {
    const expected = [
      "/v1/chat/completions",
      "/v1/responses",
      "/v1/messages",
      "/v1/messages/count_tokens",
      "/v1/models",
    ]
    expect(localPaths("openai")).toEqual(expected)
    expect(localPaths("anthropic")).toEqual(expected)
    // 未知类型按 OpenAI 兼容处理，与服务端转发假设一致
    expect(localPaths("bedrock")).toEqual(expected)
  })

  it("Gemini 型地址额外接管三条原生端点", () => {
    expect(localPaths("gemini")).toEqual([
      "/v1beta/models",
      "/v1beta/models/{model}:generateContent",
      "/v1beta/models/{model}:streamGenerateContent",
      "/v1/chat/completions",
      "/v1/responses",
      "/v1/messages",
      "/v1/messages/count_tokens",
      "/v1/models",
    ])
  })

  it("token 计数走系统级配置，其余本地端点走全池负载均衡", () => {
    const byPath = new Map(
      forwardingEndpoints("openai").map((endpoint) => [endpoint.path, endpoint.handling]),
    )
    expect(byPath.get("/v1/messages/count_tokens")).toBe("global")
    expect(byPath.get("/v1/chat/completions")).toBe("pool")
    expect(byPath.get("/v1/models")).toBe("pool")
  })

  it("需开启转发的端点按协议区分：Gemini 用原生 embedding 路径", () => {
    expect(forwardPaths("openai")).toContain("/v1/embeddings")
    expect(forwardPaths("gemini")).toEqual([
      "/v1beta/models/{model}:embedContent",
      "/v1beta/models/{model}:batchEmbedContents",
      "/v1beta/models/{model}:countTokens",
    ])
    // 本地没有实现的端点不能混进本地段，否则用户会以为关闭模式下也能用
    expect(localPaths("gemini")).not.toContain("/v1beta/models/{model}:embedContent")
  })
})

describe("forwardEndpointUrls", () => {
  it("没有地址标识时返回空数组，绝不拼出 /p//v1 这种假地址", () => {
    expect(forwardEndpointUrls("https://llm.example.com", "", "openai")).toEqual([])
  })

  it("按协议拼出完整地址，数量与端点条目一致", () => {
    const urls = forwardEndpointUrls("https://llm.example.com", "gw", "openai")
    expect(urls).toHaveLength(forwardingEndpoints("openai").length)
    expect(urls[0]).toBe("https://llm.example.com/p/gw/v1/chat/completions")
    expect(urls).toContain("https://llm.example.com/p/gw/v1/embeddings")
  })

  it("Gemini 型地址的本地端点落在 /v1beta 下", () => {
    const urls = forwardEndpointUrls("https://llm.example.com", "gw", "gemini")
    expect(urls).toContain("https://llm.example.com/p/gw/v1beta/models")
    expect(urls).toContain(
      "https://llm.example.com/p/gw/v1beta/models/{model}:generateContent",
    )
  })

  it("去掉 origin 末尾的斜杠，避免拼出 //p", () => {
    expect(forwardEndpointUrls("https://llm.example.com//", "gw", "openai")[0]).toBe(
      "https://llm.example.com/p/gw/v1/chat/completions",
    )
  })
})
