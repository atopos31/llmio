import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { InfoTip } from "@/components/info-tip";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import {
  configAPI,
  getProviderGzipAssociations,
  probeProviderGzip,
  setProviderRequestGzip,
} from "@/lib/api";
import type {
  Provider,
  ProviderGzipAssociation,
  ProviderGzipProbeStatus,
} from "@/lib/api";
import { formatFull } from "@/lib/format";

const POLICY_KEY = "traffic_compression_policy";

type GzipStatus = ProviderGzipProbeStatus | "unknown";

const statusKey = {
  supported: "gzip.status.supported",
  unsupported: "gzip.status.unsupported",
  inconclusive: "gzip.status.inconclusive",
  unknown: "gzip.status.unknown",
} as const;

const statusHelpKey = {
  supported: "gzip.status.supported_help",
  unsupported: "gzip.status.unsupported_help",
  inconclusive: "gzip.status.inconclusive_help",
  unknown: "gzip.status.unknown_help",
} as const;

const statusVariant: Record<GzipStatus, "default" | "destructive" | "secondary" | "outline"> = {
  supported: "default",
  unsupported: "destructive",
  inconclusive: "outline",
  unknown: "secondary",
};

const statusToastKey = {
  supported: "gzip.toast.supported",
  unsupported: "gzip.toast.unsupported",
  inconclusive: "gzip.toast.inconclusive",
} as const;

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function formatTimestamp(value: string | null | undefined): string {
  if (!value) return "";
  const ts = new Date(value).getTime();
  return Number.isNaN(ts) ? value : formatFull(ts);
}

function resolveStatus(provider: Provider): GzipStatus {
  if (provider.RequestGzipSupported === true) return "supported";
  if (provider.RequestGzipSupported === false) return "unsupported";
  if (provider.RequestGzipProbedAt) return "inconclusive";
  return "unknown";
}

type ProviderGzipDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  provider: Provider;
  onUpdated: (provider: Provider) => void;
};

export function ProviderGzipDialog({
  open,
  onOpenChange,
  provider,
  onUpdated,
}: ProviderGzipDialogProps) {
  const { t } = useTranslation(["providers", "common"]);
  const [associations, setAssociations] = useState<ProviderGzipAssociation[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [associationsLoading, setAssociationsLoading] = useState(false);
  const [associationsError, setAssociationsError] = useState<string | null>(null);
  const [masterEnabled, setMasterEnabled] = useState<boolean | null>(null);
  const [probing, setProbing] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setAssociations([]);
    setSelectedID("");
    setAssociationsError(null);
    setAssociationsLoading(true);
    setMasterEnabled(null);

    getProviderGzipAssociations(provider.ID)
      .then((data) => {
        if (cancelled) return;
        setAssociations(data.items);
        setSelectedID(String(data.suggested_id || data.items[0]?.id || ""));
      })
      .catch((error) => {
        if (cancelled) return;
        setAssociationsError(errorMessage(error));
      })
      .finally(() => {
        if (!cancelled) setAssociationsLoading(false);
      });

    configAPI
      .getConfig(POLICY_KEY)
      .then((response) => {
        if (cancelled) return;
        const parsed = response.value
          ? (JSON.parse(response.value) as { enabled?: boolean })
          : {};
        setMasterEnabled(parsed.enabled === true);
      })
      .catch(() => {
        if (!cancelled) setMasterEnabled(null);
      });

    return () => {
      cancelled = true;
    };
  }, [open, provider.ID]);

  const status = resolveStatus(provider);
  const selectedAssociation = useMemo(
    () => associations.find((item) => String(item.id) === selectedID),
    [associations, selectedID]
  );
  const hasProbed = Boolean(provider.RequestGzipProbedAt);
  const canEnable = provider.RequestGzipSupported === true;
  const probeDisabled =
    probing || associationsLoading || Boolean(associationsError) || !selectedID;

  const handleProbe = async () => {
    if (!selectedID) return;
    setProbing(true);
    try {
      const result = await probeProviderGzip(provider.ID, Number(selectedID));
      onUpdated(result.provider);
      toast(t(statusToastKey[result.status]));
    } catch (error) {
      toast.error(t("gzip.probe_failed", { message: errorMessage(error) }));
    } finally {
      setProbing(false);
    }
  };

  const handleToggle = async (checked: boolean) => {
    setSaving(true);
    try {
      const updated = await setProviderRequestGzip(provider.ID, checked);
      onUpdated(updated);
      toast.success(
        t(checked ? "gzip.toast.enable_success" : "gzip.toast.disable_success")
      );
    } catch (error) {
      toast.error(t("gzip.toast.update_failed", { message: errorMessage(error) }));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("gzip.title")}</DialogTitle>
          <DialogDescription>{t("gzip.desc")}</DialogDescription>
          <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span>{t("gzip.target", { name: provider.Name })}</span>
            <InfoTip label={t("common:terms.provider_label")}>
              {t("common:terms.provider")}
            </InfoTip>
          </div>
        </DialogHeader>

        <div className="space-y-5">
          <div className="space-y-2">
            <div className="flex items-center gap-1.5">
              <Label>{t("gzip.association")}</Label>
              <InfoTip label={t("gzip.association_help_label")}>
                {t("gzip.association_help")}
              </InfoTip>
            </div>

            {associationsLoading ? (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Spinner className="size-3.5" />
                {t("gzip.associations_loading")}
              </div>
            ) : associationsError ? (
              <p className="text-xs text-destructive">
                {t("gzip.associations_load_failed", { message: associationsError })}
              </p>
            ) : associations.length === 0 ? (
              <p className="text-xs text-muted-foreground">{t("gzip.no_association")}</p>
            ) : (
              <>
                <Select value={selectedID} onValueChange={setSelectedID}>
                  <SelectTrigger className="w-full" aria-label={t("gzip.association")}>
                    <SelectValue placeholder={t("gzip.association_placeholder")} />
                  </SelectTrigger>
                  <SelectContent>
                    {associations.map((item) => (
                      <SelectItem key={item.id} value={String(item.id)}>
                        {t("gzip.association_item", {
                          model: item.model,
                          providerModel: item.provider_model,
                        })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {selectedAssociation?.last_success_at ? (
                  <p className="text-xs text-muted-foreground">
                    {t("gzip.last_success", {
                      time: formatTimestamp(selectedAssociation.last_success_at),
                    })}
                  </p>
                ) : null}
              </>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">{t("gzip.status_label")}</span>
              <Badge variant={statusVariant[status]}>{t(statusKey[status])}</Badge>
              <InfoTip label={t("gzip.status_help_label")}>
                {t(statusHelpKey[status])}
              </InfoTip>
            </div>
            {provider.RequestGzipProbedAt ? (
              <p className="text-xs text-muted-foreground">
                {t("gzip.probed_at", {
                  time: formatTimestamp(provider.RequestGzipProbedAt),
                })}
              </p>
            ) : null}
          </div>

          {provider.RequestGzipProbeDetail ? (
            <div className="space-y-1.5">
              <div className="flex items-center gap-1.5">
                <Label>{t("gzip.detail")}</Label>
                <InfoTip label={t("gzip.detail_help_label")}>
                  {t("gzip.detail_help")}
                </InfoTip>
              </div>
              <pre className="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-2 font-mono text-xs">
                {provider.RequestGzipProbeDetail}
              </pre>
            </div>
          ) : null}

          <div className="flex items-start justify-between gap-3 rounded-lg border p-4">
            <div className="space-y-1">
              <div className="flex items-center gap-1.5">
                <Label htmlFor="provider-request-gzip">{t("gzip.provider_switch")}</Label>
                <InfoTip label={t("gzip.provider_switch_help_label")}>
                  {t("gzip.provider_switch_help")}
                </InfoTip>
              </div>
              <p className="text-xs text-muted-foreground">
                {t("gzip.provider_switch_note")}
              </p>
            </div>
            <Switch
              id="provider-request-gzip"
              checked={provider.RequestGzipEnabled}
              disabled={!canEnable || saving}
              onCheckedChange={handleToggle}
            />
          </div>

          {masterEnabled === false ? (
            <p className="text-xs text-status-warning-ink">{t("gzip.master_off")}</p>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common:actions.close")}
          </Button>
          <Button onClick={handleProbe} disabled={probeDisabled}>
            {probing ? (
              <>
                <Spinner className="mr-2 size-4" />
                {t("gzip.probing")}
              </>
            ) : hasProbed ? (
              t("gzip.retry")
            ) : (
              t("gzip.probe")
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
