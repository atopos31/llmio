import { useState, useEffect, useMemo } from "react";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Checkbox } from "@/components/ui/checkbox";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    getProviderModels,
    getModelOptions,
    getModelProviders,
    createModelProvider,
    updateModel,
    type Provider,
    type ProviderModel,
    type Model,
    type ModelWithProvider,
} from "@/lib/api";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

interface AutoSyncDialogProps {
    provider: Provider | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSuccess?: () => void;
}

interface MappingItem {
    providerModelId: string;
    internalModelId: number | null;
    isSelected: boolean;
    isExisting?: boolean;
}

// Common patterns to try matching if exact match fails
const MATCH_PATTERNS = [
    // OpenAI
    { pattern: /^gpt-4/i, target: "gpt-4" },
    { pattern: /^gpt-3.5/i, target: "gpt-3.5-turbo" },
    // Anthropic
    { pattern: /^claude-3-opus/i, target: "claude-3-opus" },
    { pattern: /^claude-3-sonnet/i, target: "claude-3-sonnet" },
    { pattern: /^claude-3-haiku/i, target: "claude-3-haiku" },
    // Google
    { pattern: /^gemini-pro/i, target: "gemini-pro" },
];

export function AutoSyncDialog({
    provider,
    open,
    onOpenChange,
    onSuccess,
}: AutoSyncDialogProps) {
    const [loading, setLoading] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [providerModels, setProviderModels] = useState<ProviderModel[]>([]);
    const [internalModels, setInternalModels] = useState<Model[]>([]);
    const [mappings, setMappings] = useState<Record<string, MappingItem>>({});

    useEffect(() => {
        if (open && provider) {
            fetchData();
        } else {
            // Reset state on close
            setMappings({});
            setProviderModels([]);
        }
    }, [open, provider]);

    const fetchData = async () => {
        if (!provider) return;
        setLoading(true);
        try {
            const [pModels, iModels] = await Promise.all([
                getProviderModels(provider.ID),
                getModelOptions(),
            ]);

            setProviderModels(pModels);
            setInternalModels(iModels);

            // Fetch existing mappings from DB for all internal models
            const existingMap = new Map<string, number>();
            await Promise.all(iModels.map(async (model) => {
                try {
                    const mappingsFromDB = await getModelProviders(model.ID);
                    mappingsFromDB.forEach(m => {
                        if (m.ProviderID === provider.ID) {
                            existingMap.set(m.ProviderModel, model.ID);
                        }
                    });
                } catch {
                    // Ignore errors
                }
            }));

            // Initialize mappings: DB matches are pre-filled but NOT selected (since they already exist)
            const initialMappings: Record<string, MappingItem> = {};
            pModels.forEach((pm) => {
                const existingModelId = existingMap.get(pm.id);
                initialMappings[pm.id] = {
                    providerModelId: pm.id,
                    internalModelId: existingModelId || null,
                    isSelected: false,
                };
            });
            setMappings(initialMappings);

        } catch (err) {
            toast.error("获取模型失败: " + (err instanceof Error ? err.message : String(err)));
            onOpenChange(false);
        } finally {
            setLoading(false);
        }
    };

    const findBestMatch = (providerModelId: string, models: Model[]): Model | undefined => {
        // 1. Exact Match (Case insensitive)
        const exact = models.find(
            (m) => m.Name.toLowerCase() === providerModelId.toLowerCase()
        );
        if (exact) return exact;

        // 2. Match using Model.MatchPattern (multiple regex patterns, one per line)
        for (const model of models) {
            if (model.MatchPattern) {
                const patterns = model.MatchPattern.split('\n').map(p => p.trim()).filter(p => p);
                for (const pattern of patterns) {
                    try {
                        const re = new RegExp(pattern, 'i');
                        if (re.test(providerModelId)) {
                            return model;
                        }
                    } catch {
                        // Invalid regex, skip
                    }
                }
            }
        }

        // 3. Known Patterns Fallback
        for (const p of MATCH_PATTERNS) {
            if (p.pattern.test(providerModelId)) {
                const target = models.find((m) => m.Name.toLowerCase().includes(p.target.toLowerCase()));
                if (target) return target;
            }
        }

        return undefined;
    };



    const toggleSelection = (id: string) => {
        setMappings((prev) => ({
            ...prev,
            [id]: { ...prev[id], isSelected: !prev[id].isSelected },
        }));
    };

    const updateMapping = (id: string, internalModelId: number) => {
        setMappings((prev) => ({
            ...prev,
            [id]: { ...prev[id], internalModelId, isSelected: true },
        }));
    };

    const handleSelectAll = (checked: boolean) => {
        setMappings(prev => {
            const next = { ...prev };
            Object.keys(next).forEach(key => {
                next[key].isSelected = checked;
            });
            return next;
        });
    };

    // Build existing mappings map from DB (provider_model -> internal_model_id)
    const buildExistingMappingsMap = async (): Promise<Map<string, number>> => {
        const existingMap = new Map<string, number>();
        if (!provider) return existingMap;

        // Fetch mappings for all internal models in parallel
        await Promise.all(internalModels.map(async (model) => {
            try {
                const mappingsFromDB = await getModelProviders(model.ID);
                mappingsFromDB.forEach(m => {
                    if (m.ProviderID === provider.ID) {
                        existingMap.set(m.ProviderModel, model.ID);
                    }
                });
            } catch {
                // Ignore errors for individual models
            }
        }));

        return existingMap;
    };

    // Auto-match a single provider model (DB first, then regex/name match)
    const handleAutoMatchSingle = async (providerModelId: string) => {
        // First, check if there's an existing mapping in DB
        for (const model of internalModels) {
            try {
                const existingMappings = await getModelProviders(model.ID);
                const existing = existingMappings.find(
                    m => m.ProviderID === provider?.ID && m.ProviderModel === providerModelId
                );
                if (existing) {
                    setMappings(prev => ({
                        ...prev,
                        [providerModelId]: {
                            ...prev[providerModelId],
                            internalModelId: model.ID,
                            isSelected: true,
                        }
                    }));
                    toast.success(`已从数据库匹配: ${providerModelId} → ${model.Name}`);
                    return;
                }
            } catch {
                // Continue to next model
            }
        }

        // Fallback to regex/name match
        const matched = findBestMatch(providerModelId, internalModels);
        if (matched) {
            setMappings(prev => ({
                ...prev,
                [providerModelId]: {
                    ...prev[providerModelId],
                    internalModelId: matched.ID,
                    isSelected: true,
                }
            }));
            toast.success(`已匹配: ${providerModelId} → ${matched.Name}`);
        } else {
            toast.info(`未找到匹配: ${providerModelId}`);
        }
    };

    // Auto-match all provider models (DB first, then regex/name match)
    const handleAutoMatchAll = async () => {
        setSubmitting(true);
        try {
            // Build existing mappings from DB
            const existingMap = await buildExistingMappingsMap();

            const newMappings = { ...mappings };
            let dbMatchedCount = 0;
            let regexMatchedCount = 0;
            let unmatchedCount = 0;

            Object.keys(newMappings).forEach(providerModelId => {
                // First check DB
                const existingModelId = existingMap.get(providerModelId);
                if (existingModelId) {
                    newMappings[providerModelId] = {
                        ...newMappings[providerModelId],
                        internalModelId: existingModelId,
                        isSelected: true,
                    };
                    dbMatchedCount++;
                    return;
                }

                // Fallback to regex/name match
                const matched = findBestMatch(providerModelId, internalModels);
                if (matched) {
                    newMappings[providerModelId] = {
                        ...newMappings[providerModelId],
                        internalModelId: matched.ID,
                        isSelected: true,
                    };
                    regexMatchedCount++;
                } else {
                    unmatchedCount++;
                }
            });

            setMappings(newMappings);
            toast.success(`匹配完成: 数据库 ${dbMatchedCount} 个，智能匹配 ${regexMatchedCount} 个，未匹配 ${unmatchedCount} 个`);
        } finally {
            setSubmitting(false);
        }
    };

    const handleConfirm = async () => {
        if (!provider) return;
        const toCreate = Object.values(mappings).filter(
            (m) => m.isSelected && m.internalModelId
        );

        if (toCreate.length === 0) {
            toast.warning("未选择有效的映射");
            return;
        }

        setSubmitting(true);
        let successCount = 0;
        let failCount = 0;
        let skippedCount = 0;

        try {
            // Fetch existing mappings for deduplication
            const modelIds = [...new Set(toCreate.map(m => m.internalModelId!))];
            const existingMappingsMap = new Map<number, ModelWithProvider[]>();

            await Promise.all(modelIds.map(async (modelId) => {
                try {
                    const existing = await getModelProviders(modelId);
                    existingMappingsMap.set(modelId, existing);
                } catch {
                    existingMappingsMap.set(modelId, []);
                }
            }));

            // Filter out already existing mappings
            const toCreateFiltered = toCreate.filter(m => {
                const existing = existingMappingsMap.get(m.internalModelId!) || [];
                const isDuplicate = existing.some(
                    e => e.ProviderID === provider.ID && e.ProviderModel === m.providerModelId
                );
                if (isDuplicate) {
                    skippedCount++;
                    return false;
                }
                return true;
            });

            const batchSize = 5;
            for (let i = 0; i < toCreateFiltered.length; i += batchSize) {
                const batch = toCreateFiltered.slice(i, i + batchSize);
                await Promise.all(batch.map(async (m) => {
                    try {
                        await createModelProvider({
                            model_id: m.internalModelId!,
                            provider_id: provider.ID,
                            provider_name: m.providerModelId,
                            tool_call: true,
                            structured_output: true,
                            image: false,
                            with_header: true,
                            customer_headers: {},
                            weight: 1,
                        });
                        successCount++;
                    } catch (e) {
                        console.error(e);
                        failCount++;
                    }
                }));
            }

            if (successCount > 0) {
                toast.success("成功创建 " + successCount + " 个模型映射");
                if (onSuccess) onSuccess();
                onOpenChange(false);
            }
            if (skippedCount > 0) {
                toast.info("跳过 " + skippedCount + " 个已存在的映射");
            }
            if (failCount > 0) {
                toast.error("创建 " + failCount + " 个映射失败");
            }

            // Auto-update regex patterns for manual mappings
            let patternUpdateCount = 0;
            const distinctModelsToUpdate = new Map<number, Model>();

            for (const m of toCreate) {
                const internalModel = internalModels.find(im => im.ID === m.internalModelId);
                if (internalModel) {
                    // Check if this provider model would have been auto-matched
                    const autoMatch = findBestMatch(m.providerModelId, [internalModel]);
                    if (!autoMatch) {
                        // Not auto-matched, so we should add a pattern
                        distinctModelsToUpdate.set(internalModel.ID, internalModel);
                    }
                }
            }

            for (const [_, model] of distinctModelsToUpdate) {
                // Determine new pattern to add
                // We need to find which provider model triggered this. 
                // Since multiple provider models might map to the same internal one, 
                // and none of them matched, we should add patterns for ALL of them.
                // Let's iterate the mappings again for this model.

                const relatedMappings = toCreate.filter(m => m.internalModelId === model.ID);
                let newPatterns: string[] = [];

                for (const m of relatedMappings) {
                    const autoMatch = findBestMatch(m.providerModelId, [model]);
                    if (!autoMatch) {
                        newPatterns.push(`^${m.providerModelId.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}.*`);
                    }
                }

                if (newPatterns.length > 0) {
                    // Filter out duplicate patterns that might already exist or be duplicates in newPatterns
                    const existingPatterns = (model.MatchPattern || "").split('\n').map(p => p.trim()).filter(p => p);
                    const uniqueNewPatterns = [...new Set(newPatterns)].filter(p => !existingPatterns.includes(p));

                    if (uniqueNewPatterns.length > 0) {
                        const updatedPattern = [...existingPatterns, ...uniqueNewPatterns].join('\n');
                        try {
                            await updateModel(model.ID, {
                                match_pattern: updatedPattern
                            });
                            patternUpdateCount++;
                        } catch (e) {
                            console.error("更新模型正则失败", model.Name, e);
                        }
                    }
                }
            }

            if (patternUpdateCount > 0) {
                toast.success(`已为 ${patternUpdateCount} 个模型更新正则模板`);
            }

        } catch (err) {
            toast.error("创建映射失败: " + (err instanceof Error ? err.message : String(err)));
        } finally {
            setSubmitting(false);
        }
    };

    const stats = useMemo(() => {
        const all = Object.values(mappings);
        const selected = all.filter(m => m.isSelected);
        return { total: all.length, selected: selected.length };
    }, [mappings]);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col p-[20px]">
                <DialogHeader>
                    <DialogTitle>自动同步模型 - {provider?.Name}</DialogTitle>
                    <DialogDescription>
                        将提供商模型映射到内部模型。
                    </DialogDescription>
                </DialogHeader>

                <div className="flex items-center gap-2 py-2">
                    <div className="flex-1"></div>
                    <div className="text-xs text-muted-foreground whitespace-nowrap">
                        已选 {stats.selected} / {stats.total}
                    </div>
                </div>

                {loading ? (
                    <div className="flex-1 flex items-center justify-center min-h-[300px]">
                        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                    </div>
                ) : (
                    <div className="flex-1 min-h-0 border rounded-md overflow-hidden flex flex-col">
                        <Table>
                            <TableHeader className="bg-secondary/50">
                                <TableRow>
                                    <TableHead className="w-[60px]">
                                        <Checkbox
                                            checked={stats.selected === stats.total && stats.total > 0}
                                            onCheckedChange={(c) => handleSelectAll(!!c)}
                                        />
                                    </TableHead>
                                    <TableHead>提供商模型 ID</TableHead>
                                    <TableHead>映射到内部模型</TableHead>
                                    <TableHead className="w-[80px]">操作</TableHead>
                                </TableRow>
                            </TableHeader>
                        </Table>
                        <div className="flex-1 overflow-y-auto">
                            <Table>
                                <TableBody>
                                    {providerModels.map((pm) => {
                                        const m = mappings[pm.id];
                                        if (!m) return null;
                                        return (
                                            <TableRow key={pm.id} className={m.isSelected ? "bg-accent/30" : ""}>
                                                <TableCell className="w-[50px]">
                                                    <Checkbox
                                                        checked={m.isSelected}
                                                        onCheckedChange={() => toggleSelection(pm.id)}
                                                    />
                                                </TableCell>
                                                <TableCell className="font-mono text-xs">{pm.id}</TableCell>
                                                <TableCell>
                                                    <Select
                                                        value={m.internalModelId?.toString() || "0"}
                                                        onValueChange={(val) => updateMapping(pm.id, parseInt(val))}
                                                    >
                                                        <SelectTrigger className="h-7 text-xs w-full max-w-[250px]">
                                                            <SelectValue placeholder="选择模型" />
                                                        </SelectTrigger>
                                                        <SelectContent>
                                                            <SelectItem value="0">未映射</SelectItem>
                                                            {internalModels.map((im) => (
                                                                <SelectItem key={im.ID} value={im.ID.toString()}>
                                                                    {im.Name}
                                                                </SelectItem>
                                                            ))}
                                                        </SelectContent>
                                                    </Select>
                                                </TableCell>
                                                <TableCell>
                                                    <Button
                                                        variant="outline"
                                                        size="sm"
                                                        className="h-6 px-3 text-xs text-blue-600 border-blue-300 hover:bg-blue-50 hover:text-blue-700"
                                                        onClick={() => handleAutoMatchSingle(pm.id)}
                                                    >
                                                        匹配
                                                    </Button>
                                                </TableCell>
                                            </TableRow>
                                        );
                                    })}
                                    {providerModels.length === 0 && (
                                        <TableRow>
                                            <TableCell colSpan={4} className="text-center py-8 text-muted-foreground">
                                                未从提供商获取到模型。
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </Table>
                        </div>
                    </div>
                )}

                <DialogFooter className="mt-4">
                    <Button variant="secondary" onClick={handleAutoMatchAll} disabled={submitting}>
                        自动匹配
                    </Button>
                    <Button variant="outline" onClick={() => onOpenChange(false)}>
                        取消
                    </Button>
                    <Button onClick={handleConfirm} disabled={submitting || stats.selected === 0}>
                        {submitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        同步选中项 ({stats.selected})
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
