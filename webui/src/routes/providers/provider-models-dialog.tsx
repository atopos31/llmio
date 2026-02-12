import { useEffect, useMemo, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import Loading from "@/components/loading";
import type { ProviderModel } from "@/lib/api";

type ProviderModelsDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  providerId?: number;
  providerName?: string;
  modelsLoading: boolean;
  providerModels: ProviderModel[];
  onCopyModelName: (name: string) => Promise<void>;
};

export function ProviderModelsDialog({
  open,
  onOpenChange,
  providerId,
  providerName,
  modelsLoading,
  providerModels,
  onCopyModelName,
}: ProviderModelsDialogProps) {
  const [searchTerm, setSearchTerm] = useState("");

  useEffect(() => {
    if (open) {
      setSearchTerm("");
    }
  }, [open, providerId]);

  const filteredProviderModels = useMemo(() => {
    const normalized = searchTerm.trim().toLowerCase();
    if (!normalized) {
      return providerModels;
    }

    return providerModels.filter((model) =>
      model.id.toLowerCase().includes(normalized)
    );
  }, [providerModels, searchTerm]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{providerName}模型列表</DialogTitle>
          <DialogDescription>
            当前提供商的所有可用模型
          </DialogDescription>
        </DialogHeader>

        {!modelsLoading && providerModels.length > 0 && (
          <div className="mb-4">
            <Input
              placeholder="搜索模型 ID"
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="w-full"
            />
          </div>
        )}

        {modelsLoading ? (
          <Loading message="加载模型列表" />
        ) : (
          <div className="max-h-96 overflow-y-auto">
            {filteredProviderModels.length === 0 ? (
              <div className="text-center text-gray-500 py-8">
                {providerModels.length === 0 ? "暂无模型数据" : "未找到匹配的模型"}
              </div>
            ) : (
              <div className="space-y-2">
                {filteredProviderModels.map((model, index) => (
                  <div
                    key={index}
                    className="flex items-center justify-between p-2 border rounded-lg"
                  >
                    <div className="flex-1">
                      <div className="font-medium">{model.id}</div>
                    </div>
                    <TooltipProvider>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => onCopyModelName(model.id)}
                            className="min-w-12"
                          >
                            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth="2" stroke="currentColor" aria-hidden="true" className="h-4 w-4"><path strokeLinecap="round" strokeLinejoin="round" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3m2 4H10m0 0l3-3m-3 3l3 3"></path></svg>
                          </Button>
                        </TooltipTrigger>
                      </Tooltip>
                    </TooltipProvider>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          <Button onClick={() => onOpenChange(false)}>关闭</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
