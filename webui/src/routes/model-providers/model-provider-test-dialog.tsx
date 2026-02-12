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

type TestType = "connectivity" | "react";

type ModelProviderTestDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onClose: () => void;
  testType: TestType;
  setTestType: (type: TestType) => void;
  selectedTestId: number | null;
  testResults: Record<number, { loading: boolean; result: any }>;
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
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>模型测试</DialogTitle>
          <DialogDescription>
            选择要执行的测试类型
          </DialogDescription>
        </DialogHeader>

        <RadioGroup value={testType} onValueChange={(value: string) => setTestType(value as TestType)} className="space-y-4">
          <div className="flex items-center space-x-2">
            <RadioGroupItem value="connectivity" id="connectivity" />
            <Label htmlFor="connectivity">连通性测试</Label>
          </div>
          <p className="text-sm text-gray-500 ml-6">测试模型提供商的基本连通性</p>

          <div className="flex items-center space-x-2">
            <RadioGroupItem value="react" id="react" />
            <Label htmlFor="react">React Agent 能力测试</Label>
          </div>
          <p className="text-sm text-gray-500 ml-6">测试模型的工具调用和反应能力</p>
        </RadioGroup>

        {testType === "connectivity" && (
          <div className="mt-4">
            {selectedTestId && testResults[selectedTestId]?.loading ? (
              <div className="flex items-center justify-center py-4">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-gray-900"></div>
                <span className="ml-2">测试中...</span>
              </div>
            ) : selectedTestId && testResults[selectedTestId] ? (
              <div className={`p-4 rounded-md ${testResults[selectedTestId].result?.error ? "bg-red-100 text-red-800" : "bg-green-100 text-green-800"}`}>
                <p>{testResults[selectedTestId].result?.error ? testResults[selectedTestId].result?.error : "测试成功"}</p>
                {testResults[selectedTestId].result?.message && (
                  <p className="mt-2">{testResults[selectedTestId].result.message}</p>
                )}
              </div>
            ) : (
              <p className="text-gray-500">点击"执行测试"开始测试</p>
            )}
          </div>
        )}

        {testType === "react" && (
          <div className="mt-4 max-h-96 min-w-0">
            {reactTestResult.loading ? (
              <div className="flex items-center justify-center py-4">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-gray-900"></div>
                <span className="ml-2">测试中...</span>
              </div>
            ) : (
              <>
                {reactTestResult.error ? (
                  <div className="p-4 rounded-md bg-red-100 text-red-800">
                    <p>测试失败: {reactTestResult.error}</p>
                  </div>
                ) : reactTestResult.success !== null ? (
                  <div className={`p-4 rounded-md ${reactTestResult.success ? "bg-green-100 text-green-800" : "bg-red-100 text-red-800"}`}>
                    <p>{reactTestResult.success ? "测试成功！" : "测试失败"}</p>
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
            关闭
          </Button>
          <Button
            onClick={executeTest}
            disabled={testType === "connectivity"
              ? (selectedTestId ? testResults[selectedTestId]?.loading : false)
              : reactTestResult.loading}
          >
            {testType === "connectivity"
              ? (selectedTestId && testResults[selectedTestId]?.loading ? "测试中..." : "执行测试")
              : (reactTestResult.loading ? "测试中..." : "执行测试")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
