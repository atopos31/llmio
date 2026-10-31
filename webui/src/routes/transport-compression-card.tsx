import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { InfoTip } from "@/components/info-tip";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { useAutoRefresh } from "@/hooks/use-auto-refresh";
import {
  configAPI,
  defaultTrafficCompressionPolicy,
  getTransportCompressionStats,
} from "@/lib/api";
import type { TrafficCompressionPolicy, TransportCompressionStats } from "@/lib/api";
import { formatBytes, formatFull } from "@/lib/format";

const CONFIG_KEY = "traffic_compression_policy";

function parsePolicy(value: string | undefined): TrafficCompressionPolicy {
  if (!value) return defaultTrafficCompressionPolicy;
  const parsed = JSON.parse(value) as Partial<TrafficCompressionPolicy>;
  return {
    enabled: parsed.enabled === true,
    sse_response: parsed.sse_response === true,
    upstream_request: parsed.upstream_request === true,
  };
}

/**
 * 出站传输压缩设置。
 *
 * 三项默认关闭。卡片上的状态同时有文字与颜色，避免只靠颜色区分；
 * 每个概念旁的信息图标说明它是什么、何时生效、关闭时怎样。
 */
export function TransportCompressionCard() {
  const { t } = useTranslation(["config", "common"]);
  const [policy, setPolicy] = useState<TrafficCompressionPolicy>(defaultTrafficCompressionPolicy);
  const [draft, setDraft] = useState<TrafficCompressionPolicy>(defaultTrafficCompressionPolicy);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [stats, setStats] = useState<TransportCompressionStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(true);
  const [statsError, setStatsError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setLoadError(null);
      const response = await configAPI.getConfig(CONFIG_KEY);
      setPolicy(parsePolicy(response.value));
    } catch (error) {
      console.error("读取传输压缩设置失败：", error);
      setLoadError(error instanceof Error ? error.message : String(error));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const loadStats = useCallback(async () => {
    try {
      setStatsError(null);
      setStats(await getTransportCompressionStats());
    } catch (error) {
      console.error("读取压缩效果失败：", error);
      setStatsError(error instanceof Error ? error.message : String(error));
    } finally {
      setStatsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadStats();
  }, [loadStats]);

  useAutoRefresh(10, loadStats);

  const openDialog = () => {
    setDraft(policy);
    setOpen(true);
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      await configAPI.updateConfig(CONFIG_KEY, draft);
      setPolicy(draft);
      toast.success(t("toast.save_success"));
      setOpen(false);
    } catch (error) {
      toast.error(
        t("transport.save_failed", {
          message: error instanceof Error ? error.message : String(error),
        })
      );
    } finally {
      setSaving(false);
    }
  };

  const statusText = (enabled: boolean) =>
    enabled ? t("transport.enabled") : t("transport.disabled");
  const statusVariant = (enabled: boolean) => (enabled ? "default" : "secondary");

  const statsRows = stats
    ? [
        { key: "upstream", label: t("transport.stats_upstream"), metric: stats.upstream },
        { key: "sse", label: t("transport.stats_sse"), metric: stats.sse },
      ]
    : [];
  const hasStats = statsRows.some((row) => row.metric.count > 0);
  const startedAtMs = stats ? new Date(stats.started_at).getTime() : Number.NaN;
  const startedAt =
    stats && !Number.isNaN(startedAtMs) ? formatFull(startedAtMs) : stats?.started_at ?? "";

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-1.5">
          <CardTitle className="text-sm font-medium">{t("transport.title")}</CardTitle>
          <InfoTip label={t("transport.title_help_label")}>
            {t("transport.title_help")}
          </InfoTip>
        </div>
        <CardDescription className="text-[11px]">{t("transport.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {loadError ? (
          <p className="text-xs text-status-warning-ink">
            {t("transport.load_failed", { message: loadError })}
          </p>
        ) : null}

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div className="space-y-1.5">
            <Label>{t("transport.master")}</Label>
            <div>
              <Badge variant={statusVariant(policy.enabled)}>{statusText(policy.enabled)}</Badge>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>{t("transport.sse")}</Label>
            <div>
              <Badge variant={statusVariant(policy.sse_response)}>
                {statusText(policy.sse_response)}
              </Badge>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>{t("transport.upstream")}</Label>
            <div>
              <Badge variant={statusVariant(policy.upstream_request)}>
                {statusText(policy.upstream_request)}
              </Badge>
            </div>
          </div>
        </div>

        <p className="text-xs text-muted-foreground">{t("transport.note")}</p>

        <div className="space-y-2 border-t pt-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-1.5">
              <Label>{t("transport.stats_title")}</Label>
              <InfoTip label={t("transport.stats_help_label")}>
                {t("transport.stats_help")}
              </InfoTip>
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void loadStats()}
              disabled={statsLoading}
            >
              <RefreshCw className="mr-1 size-3.5" aria-hidden="true" />
              {t("transport.stats_refresh")}
            </Button>
          </div>

          {statsError ? (
            <p className="text-xs text-status-warning-ink">
              {t("transport.stats_load_failed", { message: statsError })}
            </p>
          ) : null}

          {statsLoading && !stats ? (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <RefreshCw className="size-3.5 animate-spin" aria-hidden="true" />
              {t("transport.stats_loading")}
            </div>
          ) : stats && hasStats ? (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[520px] text-xs">
                <thead>
                  <tr className="text-left text-muted-foreground">
                    <th className="pb-2 pr-3 font-medium">{t("transport.stats_link")}</th>
                    <th className="pb-2 pr-3 text-right font-medium">{t("transport.stats_count")}</th>
                    <th className="pb-2 pr-3 text-right font-medium">{t("transport.stats_before")}</th>
                    <th className="pb-2 pr-3 text-right font-medium">{t("transport.stats_after")}</th>
                    <th className="pb-2 pr-3 text-right font-medium">{t("transport.stats_saved")}</th>
                    <th className="pb-2 text-right font-medium">{t("transport.stats_ratio")}</th>
                  </tr>
                </thead>
                <tbody>
                  {statsRows.map((row) => (
                    <tr key={row.key} className="border-t">
                      <td className="py-2 pr-3 font-medium">{row.label}</td>
                      <td className="py-2 pr-3 text-right tabular-nums">{row.metric.count}</td>
                      <td className="py-2 pr-3 text-right tabular-nums">
                        {formatBytes(row.metric.before_bytes)}
                      </td>
                      <td className="py-2 pr-3 text-right tabular-nums">
                        {formatBytes(row.metric.after_bytes)}
                      </td>
                      <td className="py-2 pr-3 text-right tabular-nums">
                        {formatBytes(row.metric.saved_bytes)}
                      </td>
                      <td className="py-2 text-right tabular-nums">
                        {row.metric.ratio > 0 ? `${row.metric.ratio.toFixed(2)}x` : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : statsError && !stats ? null : (
            <p className="text-xs text-muted-foreground">{t("transport.stats_empty")}</p>
          )}

          {stats ? (
            <p className="text-[11px] text-muted-foreground">
              {t("transport.stats_started", { time: startedAt })}
            </p>
          ) : null}
        </div>
      </CardContent>
      <CardFooter>
        <Button onClick={openDialog}>{t("transport.edit")}</Button>
      </CardFooter>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("transport.edit_title")}</DialogTitle>
            <DialogDescription>{t("transport.edit_desc")}</DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            <div className="flex items-start justify-between gap-3 rounded-lg border p-4">
              <div className="space-y-1">
                <div className="flex items-center gap-1.5">
                  <Label htmlFor="transport-master">{t("transport.master")}</Label>
                  <InfoTip label={t("transport.master_help_label")}>
                    {t("transport.master_help")}
                  </InfoTip>
                </div>
              </div>
              <Switch
                id="transport-master"
                checked={draft.enabled}
                disabled={saving}
                onCheckedChange={(checked) => setDraft((prev) => ({ ...prev, enabled: checked }))}
              />
            </div>

            <div className="flex items-start justify-between gap-3 rounded-lg border p-4">
              <div className="space-y-1">
                <div className="flex items-center gap-1.5">
                  <Label htmlFor="transport-sse">{t("transport.sse")}</Label>
                  <InfoTip label={t("transport.sse_help_label")}>
                    {t("transport.sse_help")}
                  </InfoTip>
                </div>
              </div>
              <Switch
                id="transport-sse"
                checked={draft.sse_response}
                disabled={saving}
                onCheckedChange={(checked) =>
                  setDraft((prev) => ({ ...prev, sse_response: checked }))
                }
              />
            </div>

            <div className="flex items-start justify-between gap-3 rounded-lg border p-4">
              <div className="space-y-1">
                <div className="flex items-center gap-1.5">
                  <Label htmlFor="transport-upstream">{t("transport.upstream")}</Label>
                  <InfoTip label={t("transport.upstream_help_label")}>
                    {t("transport.upstream_help")}
                  </InfoTip>
                </div>
              </div>
              <Switch
                id="transport-upstream"
                checked={draft.upstream_request}
                disabled={saving}
                onCheckedChange={(checked) =>
                  setDraft((prev) => ({ ...prev, upstream_request: checked }))
                }
              />
            </div>

            <p className="text-xs text-muted-foreground">{t("transport.master_off_note")}</p>
            <p className="text-xs text-muted-foreground">{t("transport.note")}</p>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
              {t("common:actions.cancel")}
            </Button>
            <Button onClick={handleSave} disabled={saving}>
              {saving ? (
                <>
                  <Spinner className="mr-2 size-4" />
                  {t("common:actions.saving")}
                </>
              ) : (
                t("transport.save")
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
