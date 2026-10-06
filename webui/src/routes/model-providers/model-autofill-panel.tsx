import { useTranslation } from "react-i18next";
import { RefreshCw } from "lucide-react";
import type { TFunction } from "i18next";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { formatNumber } from "@/lib/format";
import type { ModelMetadataCandidate } from "@/lib/api";
import type { AutofillFailureReason, AutofillField } from "@/lib/model-autofill";
import type { ModelAutofillState } from "./use-model-provider-form";

/**
 * 关联弹窗里的自动填写区（§5.1）。
 *
 * 三条交互约定都体现在这个组件里：
 *
 * - **失败不阻塞保存**：这里出现的任何一句话都不改提交按钮的可用性，也不
 *   拦截 onSubmit。数据源不可用时弹窗照常可填可存。
 * - **原因要分开**：`catalog_unavailable`（数据还没准备好，稍后自己会重试）
 *   与 `no_model_match`（确实没有这个模型）不能都写成「未找到」——前者让人
 *   白等，后者让人去改模型名，改错方向。
 * - **候选不自动填**：候选列表只提供「采用」按钮，不进预填路径。提示里必须
 *   同时写出被匹配的上游名与模型名，那是用户判断"这是不是我要的那家"的
 *   唯一依据。
 */

type Props = {
  state: ModelAutofillState;
  /** 上游与上游模型都填好了才让点「重新填写」——否则这次查询无从发起 */
  canRefresh: boolean;
  onRefresh: () => void;
  onAdopt: (candidate: ModelMetadataCandidate) => void;
};

/** 字段名按界面上的顺序列出来，用顿号连成一句。 */
function candidateKey(candidate: ModelMetadataCandidate): string {
  return `${candidate.source}\u0000${candidate.provider}\u0000${candidate.model}`;
}

/**
 * 未命中原因对应的那句话。
 *
 * 用一个 switch 而不是 `t(\`...reason.${failure}\`)`：词条是按 zh-CN 做了
 * 类型增强的，拼出来的键过不了类型检查。而这里恰好是最不该拼错的地方
 * ——五种原因对应五句给用户看的话，拼错一处就会退化成键路径本身，
 * 而它在界面上看起来只是"一句奇怪的英文"。
 */
function reasonText(t: TFunction<"models">, failure: AutofillFailureReason): string {
  switch (failure) {
    case "no_provider_match":
      return t("association_form.autofill.reason.no_provider_match");
    case "no_model_match":
      return t("association_form.autofill.reason.no_model_match");
    case "catalog_unavailable":
      return t("association_form.autofill.reason.catalog_unavailable");
    case "deprecated_model":
      return t("association_form.autofill.reason.deprecated_model");
    default:
      return t("association_form.autofill.reason.unknown");
  }
}

export function ModelAutofillPanel({ state, canRefresh, onRefresh, onAdopt }: Props) {
  const { t } = useTranslation(["models"]);

  /**
   * 字段名到界面标签。用界面上的说法（`image` 在界面上叫「视觉」），
   * 不然提示里会冒出一个用户没见过的词。
   */
  const fieldLabels: Record<AutofillField, string> = {
    tool_call: t("association_form.tool_call"),
    structured_output: t("association_form.structured_output"),
    image: t("association_form.vision"),
    input_price: t("association_form.input_price"),
    cache_read_price: t("association_form.cache_read_price"),
    output_price: t("association_form.output_price"),
  };
  const listFields = (fields: AutofillField[]) => fields.map((field) => fieldLabels[field]).join("、");

  const report = state.report;
  const candidates = state.suggestion?.candidates ?? [];
  const providerLabel =
    state.suggestion?.provider_name || state.suggestion?.provider || t("association_form.autofill.this_provider");

  return (
    <div className="space-y-2 rounded-md border bg-muted/30 p-3">
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className="text-sm font-medium">{t("association_form.autofill.title")}</span>
          {state.loading ? <Spinner className="size-3.5" /> : null}
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onRefresh}
          disabled={!canRefresh || state.loading}
          title={t("association_form.autofill.refill_hint")}
        >
          <RefreshCw className="size-3.5" aria-hidden="true" />
          {t("association_form.autofill.refill")}
        </Button>
      </div>

      {!state.enabled ? (
        <p className="text-xs text-muted-foreground">{t("association_form.autofill.disabled")}</p>
      ) : state.error ? (
        // 端点本身没答上来（网络 / 服务端错误）。与"源里没有这个模型"不同，
        // 所以不带任何关于模型的判断，只报这次没查成。
        <p className="text-xs text-status-warning-ink">
          {t("association_form.autofill.query_failed", { message: state.error })}
        </p>
      ) : state.failure ? (
        <div className="space-y-2">
          <p className="text-xs text-muted-foreground">
            {state.failure === "no_model_match" && candidates.length > 0
              ? t("association_form.autofill.candidates.title", { provider: providerLabel })
              : reasonText(t, state.failure)}
          </p>
          {candidates.length > 0 ? (
            <ul className="space-y-1.5">
              {candidates.map((candidate) => (
                <li
                  key={candidateKey(candidate)}
                  className="flex items-start justify-between gap-2 rounded border bg-background px-2 py-1.5"
                >
                  <div className="min-w-0 space-y-0.5">
                    <p className="truncate text-xs">
                      <span className="font-medium">{candidate.provider_name || candidate.provider}</span>
                      <span className="text-muted-foreground"> · </span>
                      <code className="text-[11px]">{candidate.model}</code>
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      {[
                        candidate.same_protocol ? t("association_form.autofill.candidates.same_protocol") : null,
                        candidate.context_limit
                          ? t("association_form.autofill.candidates.context", {
                              value: formatNumber(candidate.context_limit),
                            })
                          : null,
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </p>
                  </div>
                  <Button type="button" size="sm" variant="outline" onClick={() => onAdopt(candidate)}>
                    {t("association_form.autofill.candidates.adopt")}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : report ? (
        <div className="space-y-0.5 text-xs text-muted-foreground">
          <p>
            {report.result.fill.length > 0
              ? t("association_form.autofill.filled", {
                  count: report.result.fill.length,
                  fields: listFields(report.result.fill.map((f) => f.field)),
                })
              : t("association_form.autofill.nothing_filled")}
          </p>
          {report.result.missing.length > 0 ? (
            <p>
              {t("association_form.autofill.missing", {
                count: report.result.missing.length,
                fields: listFields(report.result.missing),
              })}
            </p>
          ) : null}
          {report.result.skipped.length > 0 ? (
            <p>
              {t("association_form.autofill.skipped", {
                count: report.result.skipped.length,
                fields: listFields(report.result.skipped.map((s) => s.field)),
              })}
              {state.overwrite ? null : ` ${t("association_form.autofill.skipped_hint")}`}
            </p>
          ) : null}
          {/* 本次是按哪个上游查的、命中了源里哪个模型 id：
              上游对齐有可能靠协议类型兜底，对齐错了模型名再准也会取到别家的价格。 */}
          {report.providerLabel ? (
            <p className="text-[11px]">
              {t("association_form.autofill.source_line", {
                provider: report.providerLabel,
                model: report.modelLabel,
              })}
            </p>
          ) : null}
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">{t("association_form.autofill.idle")}</p>
      )}

      <p className="text-[11px] text-muted-foreground">{t("association_form.autofill.note")}</p>
    </div>
  );
}
