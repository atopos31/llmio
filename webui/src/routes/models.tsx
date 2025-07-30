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
  getModels, 
  createModel, 
  updateModel, 
  deleteModel,
} from "@/lib/api";
import type { Model,  } from "@/lib/api";

export default function ModelsPage() {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<Model | null>(null);
  const [formData, setFormData] = useState({
    name: "",
    remark: "",
  });

  useEffect(() => {
    console.log("Fetching models...");
    fetchModels();
  }, []);

  const fetchModels = async () => {
    try {
      setLoading(true);
      const data = await getModels();
      setModels(data);
    } catch (err) {
      setError("获取模型列表失败");
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async () => {
    try {
      await createModel(formData);
      setOpen(false);
      setFormData({ name: "", remark: "" });
      fetchModels();
    } catch (err) {
      setError("创建模型失败");
      console.error(err);
    }
  };

  const handleUpdate = async () => {
    if (!editingModel) return;
    try {
      await updateModel(editingModel.ID, formData);
      setOpen(false);
      setEditingModel(null);
      setFormData({ name: "", remark: "" });
      fetchModels();
    } catch (err) {
      setError("更新模型失败");
      console.error(err);
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm("确定要删除这个模型吗？")) return;
    try {
      await deleteModel(id);
      fetchModels();
    } catch (err) {
      setError("删除模型失败");
      console.error(err);
    }
  };

  const openEditDialog = (model: Model) => {
    setEditingModel(model);
    setFormData({
      name: model.Name,
      remark: model.Remark,
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    setEditingModel(null);
    setFormData({ name: "", remark: "" });
    setOpen(true);
  };

  if (loading) return <Loading message="加载模型列表" />;
  if (error) return <div className="text-red-500">{error}</div>;

  return (
    <div className="space-y-6">
      <div className="flex justify-between items-center">
        <h2 className="text-2xl font-bold">模型管理</h2>
        <Button onClick={openCreateDialog}>添加模型</Button>
      </div>
      
      <div className="border rounded-lg">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>名称</TableHead>
              <TableHead>备注</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {models.map((model) => (
              <TableRow key={model.ID}>
                <TableCell>{model.ID}</TableCell>
                <TableCell>{model.Name}</TableCell>
                <TableCell>{model.Remark}</TableCell>
                <TableCell className="space-x-2">
                  <Button 
                    variant="outline" 
                    size="sm" 
                    onClick={() => openEditDialog(model)}
                  >
                    编辑
                  </Button>
                  <Button 
                    variant="destructive" 
                    size="sm" 
                    onClick={() => handleDelete(model.ID)}
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
              {editingModel ? "编辑模型" : "添加模型"}
            </DialogTitle>
            <DialogDescription>
              {editingModel 
                ? "修改模型信息" 
                : "添加一个新的模型"}
            </DialogDescription>
          </DialogHeader>
          
          <div className="space-y-4">
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
              <Label htmlFor="remark" className="form-label">备注</Label>
              <Textarea
                id="remark"
                className="form-textarea"
                value={formData.remark}
                onChange={(e) => setFormData({...formData, remark: e.target.value})}
                rows={3}
              />
            </div>
          </div>
          
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button onClick={editingModel ? handleUpdate : handleCreate}>
              {editingModel ? "更新" : "创建"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}