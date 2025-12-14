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
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
    getProviderModels,
    getModelOptions,
    createModelProvider,
    updateModel, // Import updateModel
    type Provider,
    type ProviderModel,
    type Model,
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
    const [regexPattern, setRegexPattern] = useState<string>("");

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

            // Initial Auto Match
            const initialMappings: Record<string, MappingItem> = {};
            pModels.forEach((pm) => {
                const matchedModel = findBestMatch(pm.id, iModels);
                initialMappings[pm.id] = {
                    providerModelId: pm.id,
                    internalModelId: matchedModel?.ID || null,
                    isSelected: !!matchedModel,
                };
            });
            setMappings(initialMappings);

        } catch (err) {
            toast.error("Failed to fetch models: " + (err instanceof Error ? err.message : String(err)));
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

    const handleRegexApply = () => {
        if (!regexPattern) return;
        try {
            const re = new RegExp(regexPattern, 'i');
            const newMappings = { ...mappings };
            let matchCount = 0;

            Object.values(newMappings).forEach(item => {
                if (re.test(item.providerModelId)) {
                    item.isSelected = true;
                    matchCount++;
                }
            });

            setMappings(newMappings);
            toast.success("Selected " + matchCount + " models matching pattern");
        } catch {
            toast.error("Invalid Regular Expression");
        }
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

    const handleConfirm = async () => {
        if (!provider) return;
        const toCreate = Object.values(mappings).filter(
            (m) => m.isSelected && m.internalModelId
        );

        if (toCreate.length === 0) {
            toast.warning("No valid mappings selected");
            return;
        }

        setSubmitting(true);
        let successCount = 0;
        let failCount = 0;

        try {
            const batchSize = 5;
            for (let i = 0; i < toCreate.length; i += batchSize) {
                const batch = toCreate.slice(i, i + batchSize);
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
                toast.success("Successfully created " + successCount + " mappings");
                if (onSuccess) onSuccess();
                onOpenChange(false);
            }
            if (failCount > 0) {
                toast.error("Failed to create " + failCount + " mappings");
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
                            console.error("Failed to update pattern for model", model.Name, e);
                        }
                    }
                }
            }

            if (patternUpdateCount > 0) {
                toast.success(`Updated regex patterns for ${patternUpdateCount} models`);
            }

        } catch (err) {
            toast.error("Failed to create mappings: " + (err instanceof Error ? err.message : String(err)));
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
            <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>Auto Sync Models - {provider?.Name}</DialogTitle>
                    <DialogDescription>
                        Match provider models to internal models.
                    </DialogDescription>
                </DialogHeader>

                <div className="flex items-center gap-2 py-2">
                    <div className="flex-1 flex items-center gap-2">
                        <Input
                            placeholder="Filter/Select by Regex (e.g. ^gpt-4.*)"
                            value={regexPattern}
                            onChange={(e) => setRegexPattern(e.target.value)}
                            className="h-8 text-sm"
                        />
                        <Button variant="secondary" size="sm" onClick={handleRegexApply} disabled={!regexPattern}>
                            Select matches
                        </Button>
                    </div>
                    <div className="text-xs text-muted-foreground whitespace-nowrap">
                        {stats.selected} / {stats.total} selected
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
                                    <TableHead className="w-[50px]">
                                        <Checkbox
                                            checked={stats.selected === stats.total && stats.total > 0}
                                            onCheckedChange={(c) => handleSelectAll(!!c)}
                                        />
                                    </TableHead>
                                    <TableHead>Provider Model ID</TableHead>
                                    <TableHead>Map To Internal Model</TableHead>
                                    <TableHead className="w-[100px]">Status</TableHead>
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
                                                            <SelectValue placeholder="Select Model" />
                                                        </SelectTrigger>
                                                        <SelectContent>
                                                            <SelectItem value="0">Unmapped</SelectItem>
                                                            {internalModels.map((im) => (
                                                                <SelectItem key={im.ID} value={im.ID.toString()}>
                                                                    {im.Name}
                                                                </SelectItem>
                                                            ))}
                                                        </SelectContent>
                                                    </Select>
                                                </TableCell>
                                                <TableCell>
                                                    {m.internalModelId ? (
                                                        <Badge variant="outline" className="text-green-600 bg-green-50 border-green-200">Mapped</Badge>
                                                    ) : (
                                                        <Badge variant="outline" className="text-yellow-600 bg-yellow-50 border-yellow-200">Unmapped</Badge>
                                                    )}
                                                </TableCell>
                                            </TableRow>
                                        );
                                    })}
                                    {providerModels.length === 0 && (
                                        <TableRow>
                                            <TableCell colSpan={4} className="text-center py-8 text-muted-foreground">
                                                No models found from provider.
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </Table>
                        </div>
                    </div>
                )}

                <DialogFooter className="mt-4">
                    <Button variant="outline" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button onClick={handleConfirm} disabled={submitting || stats.selected === 0}>
                        {submitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        Sync Selected ({stats.selected})
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
