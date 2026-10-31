import { useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProviderGzipDialog } from "@/routes/providers/provider-gzip-dialog";
import {
  configAPI,
  getProviderGzipAssociations,
  probeProviderGzip,
  setProviderRequestGzip,
  type Provider,
} from "@/lib/api";

vi.mock("@/lib/api", () => ({
  configAPI: {
    getConfig: vi.fn(),
    updateConfig: vi.fn(),
  },
  getProviderGzipAssociations: vi.fn(),
  probeProviderGzip: vi.fn(),
  setProviderRequestGzip: vi.fn(),
}));

const mocked = {
  getConfig: vi.mocked(configAPI.getConfig),
  getAssociations: vi.mocked(getProviderGzipAssociations),
  probe: vi.mocked(probeProviderGzip),
  setEnabled: vi.mocked(setProviderRequestGzip),
};

function provider(over: Partial<Provider> = {}): Provider {
  return {
    ID: 7,
    Name: "prov-a",
    Type: "openai",
    Config: "{}",
    Console: "",
    Proxy: "",
    ErrorMatcher: "",
    RequestGzipEnabled: false,
    RequestGzipSupported: null,
    RequestGzipProbedAt: null,
    RequestGzipProbeDetail: "",
    RequestGzipProbeHash: "",
    ...over,
  };
}

function Harness({ initial }: { initial: Provider }) {
  const [current, setCurrent] = useState(initial);
  return (
    <ProviderGzipDialog
      open
      onOpenChange={() => {}}
      provider={current}
      onUpdated={setCurrent}
    />
  );
}

beforeEach(async () => {
  vi.clearAllMocks();
  const i18n = (await import("@/i18n")).default;
  await i18n.changeLanguage("zh-CN");
  mocked.getConfig.mockResolvedValue({ key: "traffic_compression_policy", value: "" });
  mocked.getAssociations.mockResolvedValue({
    suggested_id: 12,
    items: [
      {
        id: 12,
        model: "gpt-test",
        provider_model: "gpt-upstream",
        last_success_at: "2026-10-31T10:00:00Z",
      },
    ],
  });
});

describe("上游请求压缩探测对话框", () => {
  it("未检测时显示尚未检测，并给出概念说明与关联选择", async () => {
    render(<Harness initial={provider()} />);

    expect(await screen.findByText("尚未检测")).toBeInTheDocument();
    expect(await screen.findByText("gpt-test（上游模型：gpt-upstream）")).toBeInTheDocument();
    expect(screen.getByLabelText("模型关联说明")).toBeInTheDocument();
    expect(screen.getByLabelText("检测结果说明")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "开始检测" })).toBeEnabled();
  });

  it("探测成功后显示支持，并允许开启提供商开关", async () => {
    const updated = provider({
      RequestGzipSupported: true,
      RequestGzipProbedAt: "2026-10-31T10:00:00Z",
      RequestGzipProbeDetail: "gzip HTTP 200",
      RequestGzipProbeHash: "hash",
    });
    mocked.probe.mockResolvedValue({
      status: "supported",
      http_status: 200,
      detail: "gzip HTTP 200",
      probed_at: "2026-10-31T10:00:00Z",
      model_provider_id: 12,
      provider: updated,
    });
    mocked.setEnabled.mockResolvedValue({ ...updated, RequestGzipEnabled: true });

    render(<Harness initial={provider()} />);
    await userEvent.click(await screen.findByRole("button", { name: "开始检测" }));

    expect(await screen.findByText("支持")).toBeInTheDocument();
    expect(mocked.probe).toHaveBeenCalledWith(7, 12);

    const toggle = screen.getByRole("switch");
    expect(toggle).toBeEnabled();
    await userEvent.click(toggle);
    await waitFor(() => expect(mocked.setEnabled).toHaveBeenCalledWith(7, true));
  });

  it("没有模型关联时禁用探测，并说明下一步", async () => {
    mocked.getAssociations.mockResolvedValue({ suggested_id: 0, items: [] });

    render(<Harness initial={provider()} />);

    expect(
      await screen.findByText("该提供商没有可用的模型关联，无法检测。请先在模型关联页面添加一条关联。")
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "开始检测" })).toBeDisabled();
  });

  it("总开关关闭时提示压缩不会生效", async () => {
    mocked.getConfig.mockResolvedValue({
      key: "traffic_compression_policy",
      value: JSON.stringify({ enabled: false, sse_response: true, upstream_request: true }),
    });

    render(<Harness initial={provider()} />);

    expect(
      await screen.findByText("设置页的总开关已关闭。检测仍可执行，但压缩不会生效。")
    ).toBeInTheDocument();
  });
});
