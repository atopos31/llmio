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
import { Textarea } from "@/components/ui/textarea";
import Loading from "@/components/loading";
import { 
  getProviders, 
  createProvider, 
  updateProvider, 
  deleteProvider
} from "@/lib/api";
import type { Provider } from "@/lib/api";

export default function ProvidersPage() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [formData, setFormData] = useState({
    name: "",
    type: "",
    config: "",
  });

  useEffect(() => {
    fetchProviders();
  }, []);

  const fetchProviders = async () => {
    try {
      setLoading(true);
      const data = await getProviders();
      setProviders(data);
    } catch (err) {
      setError("获取提供商列表失败");
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async () => {
    try {
      await createProvider(formData);
      setOpen(false);
      setFormData({ name: "", type: "", config: "" });
      fetchProviders();
    } catch (err) {
      setError("创建提供商失败");
      console.error(err);
    }
  };

  const handleUpdate = async () => {
    if (!editingProvider) return;
    try {
      await updateProvider(editingProvider.ID, formData);
      setOpen(false);
      setEditingProvider(null);
      setFormData({ name: "", type: "", config: "" });
      fetchProviders();
    } catch (err) {
      setError("更新提供商失败");
      console.error(err);
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm("确定要删除这个提供商吗？")) return;
    try {
      await deleteProvider(id);
      fetchProviders();
    } catch (err) {
      setError("删除提供商失败");
      console.error(err);
    }
  };

  const openEditDialog = (provider: Provider) => {
    setEditingProvider(provider);
    setFormData({
      name: provider.Name,
      type: provider.Type,
      config: provider.Config,
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    setEditingProvider(null);
    setFormData({ name: "", type: "", config: "" });
    setOpen(true);
  };

  if (loading) return <Loading message="加载提供商列表" />;
  if (error) return <div className="text-red-500">{error}</div>;

  return (
    <div className="space-y-6">
      <div className="flex justify-between items-center">
        <h2 className="text-2xl font-bold">提供商管理</h2>
        <Button onClick={openCreateDialog}>添加提供商</Button>
      </div>
      
      <div className="border rounded-lg">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>名称</TableHead>
              <TableHead>类型</TableHead>
              <TableHead>配置</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {providers.map((provider) => (
              <TableRow key={provider.ID}>
                <TableCell>{provider.ID}</TableCell>
                <TableCell>{provider.Name}</TableCell>
                <TableCell>{provider.Type}</TableCell>
                <TableCell>
                  <pre className="text-xs overflow-hidden max-w-md truncate">
                    {provider.Config}
                  </pre>
                </TableCell>
                <TableCell className="space-x-2">
                  <Button 
                    variant="outline" 
                    size="sm" 
                    onClick={() => openEditDialog(provider)}
                  >
                    编辑
                  </Button>
                  <Button 
                    variant="destructive" 
                    size="sm" 
                    onClick={() => handleDelete(provider.ID)}
                  >
                    删除
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingProvider ? "编辑提供商" : "添加提供商"}
            </DialogTitle>
            <DialogDescription>
              {editingProvider 
                ? "修改提供商信息" 
                : "添加一个新的提供商"}
            </DialogDescription>
          </DialogHeader>
          
          <div className="space-y-4 min-w-0">
            <div className="form-group">
              <Label htmlFor="name" className="form-label">名称</Label>
              <Input
                id="name"
                className="form-input"
                value={formData.name}
                onChange={(e) => setFormData({...formData, name: e.target.value})}
              />
            </div>
            
            <div className="form-group">
              <Label htmlFor="type" className="form-label">类型</Label>
              <Input
                id="type"
                className="form-input"
                value={formData.type}
                onChange={(e) => setFormData({...formData, type: e.target.value})}
              />
            </div>
            
            <div className="form-group">
              <Label htmlFor="config" className="form-label">配置</Label>
              <Textarea
                id="config"
                
                className="form-textarea resize-none whitespace-pre overflow-x-auto"
                value={formData.config}
                onChange={(e) => setFormData({...formData, config: e.target.value})}
              />
            </div>
          </div>
          
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button onClick={editingProvider ? handleUpdate : handleCreate}>
              {editingProvider ? "更新" : "创建"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}