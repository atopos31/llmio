import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { 
  Table, 
  TableBody, 
  TableCell, 
  TableHead, 
  TableHeader, 
  TableRow 
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import Loading from "@/components/loading";
import { 
  getModelProviders, 
  createModelProvider, 
  updateModelProvider, 
  deleteModelProvider,
  getModels,
  getProviders,
  testModelProvider
} from "@/lib/api";
import type { ModelWithProvider, Model, Provider } from "@/lib/api";

export default function ModelProvidersPage() {
  const [modelProviders, setModelProviders] = useState<ModelWithProvider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [editingAssociation, setEditingAssociation] = useState<ModelWithProvider | null>(null);
  const [selectedModelId, setSelectedModelId] = useState<number | null>(null);
  const [formData, setFormData] = useState({
    model_id: 0,
    provider_name: "",
    provider_id: 0,
    weight: 1, // 默认权重为1
  });
  const [testResults, setTestResults] = useState<Record<number, { loading: boolean; result: any }>>({});

  useEffect(() => {
    Promise.all([fetchModels(), fetchProviders()]);
  }, []);

  useEffect(() => {
    if (selectedModelId) {
      fetchModelProviders(selectedModelId);
    }
  }, [selectedModelId]);

  const fetchModels = async () => {
    try {
      const data = await getModels();
      setModels(data);
      if (data.length > 0 && !selectedModelId) {
        setSelectedModelId(data[0].ID);
      }
    } catch (err) {
      setError("获取模型列表失败");
      console.error(err);
    }
  };

  const fetchProviders = async () => {
    try {
      const data = await getProviders();
      setProviders(data);
    } catch (err) {
      setError("获取提供商列表失败");
      console.error(err);
    }
  };

  const fetchModelProviders = async (modelId: number) => {
    try {
      setLoading(true);
      const data = await getModelProviders(modelId);
      setModelProviders(data);
    } catch (err) {
      setError("获取模型提供商关联列表失败");
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async () => {
    // 验证权重必须大于0
    if (formData.weight <= 0) {
      setError("权重必须大于0");
      return;
    }
    
    try {
      await createModelProvider({
        ...formData,
        model_id: selectedModelId || 0
      });
      setOpen(false);
      setFormData({ model_id: 0, provider_name: "", provider_id: 0, weight: 1 });
      if (selectedModelId) {
        fetchModelProviders(selectedModelId);
      }
    } catch (err) {
      setError("创建模型提供商关联失败");
      console.error(err);
    }
  };

  const handleUpdate = async () => {
    if (!editingAssociation) return;
    
    // 验证权重必须大于0
    if (formData.weight <= 0) {
      setError("权重必须大于0");
      return;
    }
    
    try {
      await updateModelProvider(editingAssociation.ID, {
        model_id: formData.model_id,
        provider_name: formData.provider_name,
        provider_id: formData.provider_id,
        weight: formData.weight,
      });
      setOpen(false);
      setEditingAssociation(null);
      setFormData({ model_id: 0, provider_name: "", provider_id: 0, weight: 1 });
      if (selectedModelId) {
        fetchModelProviders(selectedModelId);
      }
    } catch (err) {
      setError("更新模型提供商关联失败");
      console.error(err);
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm("确定要删除这个关联吗？")) return;
    try {
      await deleteModelProvider(id);
      if (selectedModelId) {
        fetchModelProviders(selectedModelId);
      }
    } catch (err) {
      setError("删除模型提供商关联失败");
      console.error(err);
    }
  };

  const handleTest = async (id: number) => {
    try {
      setTestResults(prev => ({
        ...prev,
        [id]: { loading: true, result: null }
      }));
      
      const result = await testModelProvider(id);
      setTestResults(prev => ({
        ...prev,
        [id]: { loading: false, result }
      }));
    } catch (err) {
      setTestResults(prev => ({
        ...prev,
        [id]: { loading: false, result: { error: "测试失败" } }
      }));
      console.error(err);
    }
  };

  const openEditDialog = (association: ModelWithProvider) => {
    setEditingAssociation(association);
    setFormData({
      model_id: association.ModelID,
      provider_name: association.ProviderModel,
      provider_id: association.ProviderID,
      weight: association.Weight,
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    setEditingAssociation(null);
    setFormData({ 
      model_id: selectedModelId || 0, 
      provider_name: "", 
      provider_id: 0, 
      weight: 1 // 默认权重为1
    });
    setOpen(true);
  };

  const handleModelChange = (modelId: string) => {
    const id = parseInt(modelId);
    setSelectedModelId(id);
  };

  const handleProviderChange = (providerId: string) => {
    const id = parseInt(providerId);
    const provider = providers.find(p => p.ID === id);
    if (provider) {
      setFormData({
        ...formData,
        provider_id: id,
      });
    }
  };

  const handleProviderModelChange = (name: string) => {
    setFormData({
      ...formData,
      provider_name: name
    });
  };

  const handleWeightChange = (weight: string) => {
    const weightValue = parseInt(weight) || 0;
    setFormData({
      ...formData,
      weight: weightValue
    });
  };

  if (loading && modelProviders.length === 0) return <Loading message="加载模型提供商关联" />;
  if (error) return <div className="text-red-500">{error}</div>;

  return (
    <div className="space-y-6">
      <div className="flex justify-between items-center">
        <h2 className="text-2xl font-bold">模型提供商关联</h2>
        <div className="flex space-x-4">
          <Select value={selectedModelId?.toString() || ""} onValueChange={handleModelChange}>
            <SelectTrigger className="w-64">
              <SelectValue placeholder="选择模型" />
            </SelectTrigger>
            <SelectContent>
              {models.map((model) => (
                <SelectItem key={model.ID} value={model.ID.toString()}>
                  {model.Name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button onClick={openCreateDialog} disabled={!selectedModelId}>添加关联</Button>
        </div>
      </div>
      
      {!selectedModelId ? (
        <div>请选择一个模型来查看其提供商关联</div>
      ) : (
        <div className="border rounded-lg">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>提供商模型</TableHead>
                <TableHead>提供商</TableHead>
                <TableHead>权重</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {modelProviders.map((association) => {
                const provider = providers.find(p => p.ID === association.ProviderID);
                return (
                  <TableRow key={association.ID}>
                    <TableCell>{association.ID}</TableCell>
                    <TableCell>{association.ProviderModel}</TableCell>
                    <TableCell>{provider ? provider.Name : '未知'}</TableCell>
                    <TableCell>{association.Weight}</TableCell>
                    <TableCell className="space-x-2 text-right">
                      <Button 
                        variant="outline" 
                        size="sm" 
                        onClick={() => openEditDialog(association)}
                      >
                        编辑
                      </Button>
                      <Button 
                        variant="destructive" 
                        size="sm" 
                        onClick={() => handleDelete(association.ID)}
                      >
                        删除
                      </Button>
                      <Button 
                        variant="outline" 
                        size="sm" 
                        onClick={() => handleTest(association.ID)}
                        disabled={testResults[association.ID]?.loading}
                      >
                        {testResults[association.ID]?.loading ? "测试中..." : "测试"}
                      </Button>
                      {testResults[association.ID] && !testResults[association.ID].loading && (
                        <span className={testResults[association.ID].result?.error ? "text-red-500" : "text-green-500"}>
                          {testResults[association.ID].result?.error ? "失败" : "成功"}
                        </span>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingAssociation ? "编辑关联" : "添加关联"}
            </DialogTitle>
            <DialogDescription>
              {editingAssociation 
                ? "修改模型提供商关联" 
                : "添加一个新的模型提供商关联"}
            </DialogDescription>
          </DialogHeader>
          
          <div className="space-y-4">
            <div className="form-group">
              <Label htmlFor="model" className="form-label">模型</Label>
              <Select 
                value={selectedModelId?.toString() || ""} 
                onValueChange={handleModelChange}
                disabled={!!editingAssociation}
              >
                <SelectTrigger id="model" className="form-select">
                  <SelectValue placeholder="选择模型" />
                </SelectTrigger>
                <SelectContent>
                  {models.map((model) => (
                    <SelectItem key={model.ID} value={model.ID.toString()}>
                      {model.Name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            
            <div className="form-group">
              <Label htmlFor="provider" className="form-label">提供商</Label>
              <Select 
                value={formData.provider_id.toString() || ""} 
                onValueChange={handleProviderChange}
              >
                <SelectTrigger id="provider" className="form-select">
                  <SelectValue placeholder="选择提供商" />
                </SelectTrigger>
                <SelectContent>
                  {providers.map((provider) => (
                    <SelectItem key={provider.ID} value={provider.ID.toString()}>
                      {provider.Name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            
            <div className="form-group">
              <Label htmlFor="provider-name" className="form-label">提供商模型</Label>
              <Input
                id="provider-name"
                className="form-input"
                value={formData.provider_name}
                onChange={(e) => handleProviderModelChange(e.target.value)}
                placeholder="输入提供商模型名称"
              />
            </div>
            
            <div className="form-group">
              <Label htmlFor="weight" className="form-label">权重 (必须大于0)</Label>
              <Input
                id="weight"
                className="form-input"
                type="number"
                min="1"
                value={formData.weight}
                onChange={(e) => handleWeightChange(e.target.value)}
              />
            </div>
          </div>
          
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button onClick={editingAssociation ? handleUpdate : handleCreate}>
              {editingAssociation ? "更新" : "创建"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
