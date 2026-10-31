import { render, screen, waitFor, within } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { TransportCompressionCard } from "@/routes/transport-compression-card";
import {
  configAPI,
  defaultTrafficCompressionPolicy,
  getTransportCompressionStats,
  type TrafficCompressionPolicy,
  type TransportCompressionStats,
} from "@/lib/api";

vi.mock("@/lib/api", () => ({
  configAPI: {
    getConfig: vi.fn(),
    updateConfig: vi.fn(),
  },
  defaultTrafficCompressionPolicy: {
    enabled: false,
    sse_response: false,
    upstream_request: false,
  },
  getTransportCompressionStats: vi.fn(),
}));

const mocked = {
  getConfig: vi.mocked(configAPI.getConfig),
  updateConfig: vi.mocked(configAPI.updateConfig),
  getStats: vi.mocked(getTransportCompressionStats),
};

const emptyStats: TransportCompressionStats = {
  started_at: "2026-10-31T12:00:00+08:00",
  upstream: { count: 0, before_bytes: 0, after_bytes: 0, saved_bytes: 0, ratio: 0 },
  sse: { count: 0, before_bytes: 0, after_bytes: 0, saved_bytes: 0, ratio: 0 },
};

beforeEach(async () => {
  vi.clearAllMocks();
  const i18n = (await import("@/i18n")).default;
  await i18n.changeLanguage("zh-CN");
  mocked.getConfig.mockResolvedValue({ key: "traffic_compression_policy", value: "" });
  mocked.updateConfig.mockResolvedValue({
    key: "traffic_compression_policy",
    value: JSON.stringify(defaultTrafficCompressionPolicy),
  });
  mocked.getStats.mockResolvedValue(emptyStats);
});

describe("传输压缩设置卡", () => {
  it("默认三项全部关闭，并给出概念说明", async () => {
    render(<TransportCompressionCard />);

    expect(await screen.findByText("传输压缩")).toBeInTheDocument();
    expect(screen.getAllByText("未启用")).toHaveLength(3);
    expect(screen.getByLabelText("传输压缩说明")).toBeInTheDocument();
    expect(screen.getByLabelText("压缩效果说明")).toBeInTheDocument();
    expect(await screen.findByText("尚无压缩流量")).toBeInTheDocument();
    expect(
      screen.getByText(
        "未声明支持压缩的客户端、检测结果为“不支持”或“无法判定”的提供商，均保持未压缩传输。"
      )
    ).toBeInTheDocument();
  });

  it("有数据时显示次数、字节与压缩比", async () => {
    mocked.getStats.mockResolvedValue({
      started_at: "2026-10-31T12:00:00+08:00",
      upstream: { count: 2, before_bytes: 2048, after_bytes: 1024, saved_bytes: 1024, ratio: 2 },
      sse: { count: 1, before_bytes: 4096, after_bytes: 1024, saved_bytes: 3072, ratio: 4 },
    });

    render(<TransportCompressionCard />);

    const table = await screen.findByRole("table");
    expect(within(table).getByText("2.00x")).toBeInTheDocument();
    expect(within(table).getByText("4.00x")).toBeInTheDocument();
    expect(within(table).getByText("2 KB")).toBeInTheDocument();
    expect(within(table).getByText("4 KB")).toBeInTheDocument();
    expect(mocked.getStats).toHaveBeenCalled();
  });

  it("保存时提交三个开关的完整策略", async () => {
    render(<TransportCompressionCard />);

    await userEvent.click(await screen.findByRole("button", { name: "编辑设置" }));

    expect(screen.getByLabelText("总开关说明")).toBeInTheDocument();
    expect(screen.getByLabelText("流式响应压缩说明")).toBeInTheDocument();
    expect(screen.getByLabelText("上游请求压缩说明")).toBeInTheDocument();

    const switches = screen.getAllByRole("switch");
    expect(switches).toHaveLength(3);
    await userEvent.click(switches[0]);
    await userEvent.click(switches[1]);
    await userEvent.click(screen.getByRole("button", { name: "保存设置" }));

    const expected: TrafficCompressionPolicy = {
      enabled: true,
      sse_response: true,
      upstream_request: false,
    };
    await waitFor(() =>
      expect(mocked.updateConfig).toHaveBeenCalledWith(
        "traffic_compression_policy",
        expected
      )
    );
  });

  it("读取失败时说明下面可能不是已保存的值", async () => {
    mocked.getConfig.mockRejectedValue(new Error("500 internal error"));

    render(<TransportCompressionCard />);

    expect(
      await screen.findByText("读取传输压缩设置失败：500 internal error")
    ).toBeInTheDocument();
  });
});
