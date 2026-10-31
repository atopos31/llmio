import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
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
import { configAPI, defaultTrafficCompressionPolicy } from "@/lib/api";
import type { TrafficCompressionPolicy } from "@/lib/api";

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
