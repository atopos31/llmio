import { useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { ConnectivityTestResult } from "@/lib/api";

type TestType = "connectivity" | "react";

/**
 * "测试中"。连通性测试与能力测试各用一次，原先各写一遍写死的转圈 div。
 *
 * 转动的圈在无障碍树上等于空白：不声明 role="status" 的话，读屏用户听到的
 * 是"什么都没有"，与"测试没反应"是同一件事。颜色也从写死的 gray-900 换成
 * 语义 token，免得在深色主题下要么刺眼要么看不见。
 */
function TestingIndicator({ label }: { label: string }) {
  return (
    <div
      role="status"
      aria-busy="true"
      className="flex items-center justify-center gap-2 py-4 text-muted-foreground"
    >
      <div className="size-6 animate-spin rounded-full border-b-2 border-current" aria-hidden="true" />
      <span>{label}</span>
    </div>
  );
}

type ModelProviderTestDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onClose: () => void;
  testType: TestType;
  setTestType: (type: TestType) => void;
  selectedTestId: number | null;
  testResults: Record<number, { loading: boolean; result: ConnectivityTestResult | null }>;
  reactTestResult: {
    loading: boolean;
    messages: string;
    success: boolean | null;
    error: string | null;
  };
  executeTest: () => Promise<void>;
};

export function ModelProviderTestDialog({
  open,
  onOpenChange,
  onClose,
  testType,
  setTestType,
  selectedTestId,
  testResults,
  reactTestResult,
  executeTest,
}: ModelProviderTestDialogProps) {
  const { t } = useTranslation('models');

  const formatErrorText = (error: unknown) => {
    if (!error) return "";
    if (typeof error === "string") {
      return error;
    }
    if (error instanceof Error) {
      return error.message || String(error);
    }
    try {
      return JSON.stringify(error, null, 2);
    } catch {
      return String(error);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('test_dialog.title')}</DialogTitle>
          <DialogDescription>
            {t('test_dialog.desc')}
          </DialogDescription>
        </DialogHeader>

        <RadioGroup value={testType} onValueChange={(value: string) => setTestType(value as TestType)} className="space-y-4">
          <div className="flex items-center space-x-2">
            <RadioGroupItem value="connectivity" id="connectivity" />
              <Label htmlFor="connectivity">{t('test_dialog.connectivity')}</Label>
            </div>
            <p className="text-sm text-muted-foreground ml-6">{t('test_dialog.connectivity_desc')}</p>

            <div className="flex items-center space-x-2">
              <RadioGroupItem value="react" id="react" />
              <Label htmlFor="react">{t('test_dialog.react')}</Label>
            </div>
            <p className="text-sm text-muted-foreground ml-6">{t('test_dialog.react_desc')}</p>
        </RadioGroup>

        {testType === "connectivity" && (
          <div className="mt-4">
            {selectedTestId && testResults[selectedTestId]?.loading ? (
              <TestingIndicator label={t('test_dialog.testing')} />
            ) : selectedTestId && testResults[selectedTestId] ? (
              testResults[selectedTestId].result?.error ? (
                <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 max-w-full overflow-hidden">
                  <p className="text-xs text-destructive uppercase tracking-wide mb-1">{t('test_dialog.error_title')}</p>
                  <div className="text-destructive whitespace-pre-wrap break-all text-sm max-w-full">
                    {formatErrorText(testResults[selectedTestId].result?.error)}
                  </div>
                </div>
              ) : (
                <div className="rounded-md border border-status-good/40 bg-status-good/5 p-4 text-status-good-ink">
                  <p>{t('test_dialog.test_success')}</p>
                  {testResults[selectedTestId].result?.message && (
                    <p className="mt-2 whitespace-pre-wrap break-words">{testResults[selectedTestId].result.message}</p>
                  )}
                </div>
              )
            ) : (
              <p className="text-muted-foreground">{t('test_dialog.click_to_start')}</p>
            )}
          </div>
        )}

        {testType === "react" && (
          <div className="mt-4 max-h-96 min-w-0">
            {reactTestResult.loading ? (
              <TestingIndicator label={t('test_dialog.testing')} />
            ) : (
              <>
                {reactTestResult.error ? (
                  <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 max-w-full overflow-hidden">
                    <p className="text-xs text-destructive uppercase tracking-wide mb-1">{t('test_dialog.error_title')}</p>
                    <div className="text-destructive whitespace-pre-wrap break-all text-sm max-w-full">
                      {formatErrorText(reactTestResult.error)}
                    </div>
                  </div>
                ) : reactTestResult.success !== null ? (
                  <div
                    className={`rounded-md border p-4 ${
                      reactTestResult.success
                        ? "border-status-good/40 bg-status-good/5 text-status-good-ink"
                        : "border-status-critical/40 bg-status-critical/5 text-status-critical-ink"
                    }`}
                  >
                    <p>{reactTestResult.success ? t('test_dialog.test_success_excl') : t('test_dialog.test_failed')}</p>
                  </div>
                ) : null}
              </>
            )}

            {reactTestResult.messages && (
              <Textarea
                name="logs"
                className="mt-4 max-h-50 resize-none whitespace-pre overflow-x-auto"
                readOnly
                value={reactTestResult.messages}
              />
            )}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('test_dialog.close')}
          </Button>
          <Button
            onClick={executeTest}
            disabled={testType === "connectivity"
              ? (selectedTestId ? testResults[selectedTestId]?.loading : false)
              : reactTestResult.loading}
          >
            {testType === "connectivity"
              ? (selectedTestId && testResults[selectedTestId]?.loading ? t('test_dialog.testing') : t('test_dialog.execute'))
              : (reactTestResult.loading ? t('test_dialog.testing') : t('test_dialog.execute'))}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
