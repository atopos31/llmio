import { useState, useEffect, useRef } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import { z } from 'zod';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
  FormDescription,
} from '@/components/ui/form';
import Loading from '@/components/loading';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { Switch } from '@/components/ui/switch';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  configAPI,
  type AnthropicCountTokens,
  testCountTokens,
  exportBackup,
  importBackup,
  previewBackup,
  type BackupData,
  type BackupInfo,
  getWebDAVConfig,
  updateWebDAVConfig,
  getWebDAVAutoSyncConfig,
  updateWebDAVAutoSyncConfig,
  testWebDAVConnection,
  uploadWebDAVBackup,
  downloadWebDAVBackup,
  type WebDAVConfig,
  type WebDAVAutoSyncConfig,
} from '@/lib/api';
import { toast } from 'sonner';
import { FaDownload, FaUpload, FaFileImport, FaSync, FaCloud, FaEye, FaEyeSlash, FaLock, FaUnlock } from 'react-icons/fa';
import {
  encryptBackupContent,
  decryptBackupEnvelope,
  tryParseEncryptedBackupEnvelope,
} from '@/lib/backup-encryption';

const anthropicConfigSchema = z.object({
  base_url: z.string().min(1, { message: 'Base URL 不能为空' }),
  api_key: z.string().min(1, { message: 'API Key 不能为空' }),
  version: z.string().min(1, { message: 'Version 不能为空' }),
});

type AnthropicConfigForm = z.infer<typeof anthropicConfigSchema>;

const logRetentionSchema = z.object({
  days: z.number().min(0, { message: '天数不能为负数' }),
});

type LogRetentionForm = z.infer<typeof logRetentionSchema>;

export default function ConfigPage() {
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [config, setConfig] = useState<AnthropicCountTokens | null>(null);
  const [testing, setTesting] = useState(false);
  
  // Log retention states
  const [logRetentionDays, setLogRetentionDays] = useState<number>(0);
  const [logRetentionDialogOpen, setLogRetentionDialogOpen] = useState(false);
  const [savingLogRetention, setSavingLogRetention] = useState(false);
  
  // Backup states
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [importDialogOpen, setImportDialogOpen] = useState(false);
  const [backupPreview, setBackupPreview] = useState<BackupInfo | null>(null);
  const [pendingBackupData, setPendingBackupData] = useState<BackupData | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // WebDAV states
  const [webdavConfig, setWebdavConfig] = useState<WebDAVConfig>({
    url: '',
    username: '',
    password: '',
    encryption_enabled: false,
    encryption_password: ''
  });
  const [webdavAutoSyncConfig, setWebdavAutoSyncConfig] = useState<WebDAVAutoSyncConfig>({
    enabled: false,
    sync_interval: 3600,
    sync_strategy: 'merge'
  });
  const [showWebdavPassword, setShowWebdavPassword] = useState(false);
  const [showEncryptionPassword, setShowEncryptionPassword] = useState(false);
  const [savingWebdav, setSavingWebdav] = useState(false);
  const [testingWebdav, setTestingWebdav] = useState(false);
  const [uploadingWebdav, setUploadingWebdav] = useState(false);
  const [downloadingWebdav, setDownloadingWebdav] = useState(false);
  const [savingAutoSync, setSavingAutoSync] = useState(false);
  const [syncingNow, setSyncingNow] = useState(false);
  const [webdavDialogOpen, setWebdavDialogOpen] = useState(false);
  const [autoSyncDialogOpen, setAutoSyncDialogOpen] = useState(false);
  const [decryptDialogOpen, setDecryptDialogOpen] = useState(false);
  const [decryptPassword, setDecryptPassword] = useState('');
  const [encryptedRawContent, setEncryptedRawContent] = useState('');
  const [decrypting, setDecrypting] = useState(false);

  const form = useForm<AnthropicConfigForm>({
    resolver: zodResolver(anthropicConfigSchema),
    defaultValues: {
      base_url: '',
      api_key: '',
      version: '2023-06-01',
    },
  });

  const logRetentionForm = useForm<LogRetentionForm>({
    resolver: zodResolver(logRetentionSchema),
    defaultValues: {
      days: 0,
    },
  });

  useEffect(() => {
    const fetchConfig = async () => {
      try {
        setLoading(true);
        
        // 获取 Anthropic 配置
        const response = await configAPI.getConfig('anthropic_count_tokens');
        if (response.value) {
          const anthropicConfig = JSON.parse(response.value) as AnthropicCountTokens;
          setConfig(anthropicConfig);
        }
        
        // 获取日志保留天数配置
        const logRetentionResponse = await configAPI.getConfig('log_retention_days');
        if (logRetentionResponse.value) {
          const days = parseInt(logRetentionResponse.value, 10);
          setLogRetentionDays(isNaN(days) ? 0 : days);
        }

        // 获取 WebDAV 配置
        try {
          const webdavConfigData = await getWebDAVConfig();
          setWebdavConfig(webdavConfigData);
        } catch {
          // WebDAV 配置不存在是正常的
        }

        // 获取 WebDAV 自动同步配置
        try {
          const autoSyncConfigData = await getWebDAVAutoSyncConfig();
          setWebdavAutoSyncConfig(autoSyncConfigData);
        } catch {
          // 自动同步配置不存在是正常的
        }
      } catch (error) {
        console.error('Failed to load config:', error);
        // 配置不存在是正常的，不显示错误提示
      } finally {
        setLoading(false);
      }
    };

    fetchConfig();
  }, []);

  const openEditDialog = () => {
    form.reset({
      base_url: config?.base_url || 'https://api.anthropic.com/v1',
      api_key: config?.api_key || '',
      version: config?.version || '2023-06-01',
    });
    setOpen(true);
  };

  const closeDialog = () => {
    setOpen(false);
  };

  const testConfig = async () => {
    try {
      setTesting(true);
      await testCountTokens();
      toast.success('配置检测成功');
    } catch (error) {
      console.error('Config test failed:', error);
      const errorMessage = error instanceof Error ? error.message : '检测失败';
      toast.error(`配置检测失败: ${errorMessage}`);
    } finally {
      setTesting(false);
    }
  };

  const onSubmit = async (values: AnthropicConfigForm) => {
    try {
      await configAPI.updateConfig('anthropic_count_tokens', values);
      setConfig(values);
      toast.success('配置已保存');
      setOpen(false);
    } catch (error) {
      console.error('Failed to save config:', error);
      toast.error('保存配置失败');
    }
  };

  // 打开日志保留天数编辑对话框
  const openLogRetentionDialog = () => {
    logRetentionForm.reset({ days: logRetentionDays });
    setLogRetentionDialogOpen(true);
  };

  // 保存日志保留天数
  const onLogRetentionSubmit = async (values: LogRetentionForm) => {
    try {
      setSavingLogRetention(true);
      await configAPI.updateConfigRaw('log_retention_days', values.days.toString());
      setLogRetentionDays(values.days);
      toast.success('日志保留天数已保存');
      setLogRetentionDialogOpen(false);
    } catch (error) {
      console.error('Failed to save log retention config:', error);
      toast.error('保存日志保留天数失败');
    } finally {
      setSavingLogRetention(false);
    }
  };

  // 导出数据
  const handleExport = async () => {
    try {
      setExporting(true);
      const data = await exportBackup();
      
      // 创建下载链接
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `llmio_backup_${new Date().toISOString().slice(0, 19).replace(/[:-]/g, '')}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      
      toast.success('数据导出成功');
    } catch (error) {
      console.error('Export failed:', error);
      const errorMessage = error instanceof Error ? error.message : '导出失败';
      toast.error(`导出失败: ${errorMessage}`);
    } finally {
      setExporting(false);
    }
  };

  // 选择文件
  const handleFileSelect = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;

    try {
      const text = await file.text();
      const data = JSON.parse(text) as BackupData;
      
      // 预览备份信息
      const preview = await previewBackup(data);
      setBackupPreview(preview);
      setPendingBackupData(data);
      setImportDialogOpen(true);
    } catch (error) {
      console.error('Failed to parse backup file:', error);
      toast.error('无法解析备份文件，请确保文件格式正确');
    }
    
    // 重置文件输入
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  // 确认导入
  const handleConfirmImport = async () => {
    if (!pendingBackupData) return;

    try {
      setImporting(true);
      const result = await importBackup(pendingBackupData);
      
      toast.success(`导入成功！提供商: ${result.stats.providers}, 模型: ${result.stats.models}, 关联: ${result.stats.model_providers}, API Keys: ${result.stats.auth_keys}, 配置: ${result.stats.configs}`);
      setImportDialogOpen(false);
      setPendingBackupData(null);
      setBackupPreview(null);
    } catch (error) {
      console.error('Import failed:', error);
      const errorMessage = error instanceof Error ? error.message : '导入失败';
      toast.error(`导入失败: ${errorMessage}`);
    } finally {
      setImporting(false);
    }
  };

  // 取消导入
  const handleCancelImport = () => {
    setImportDialogOpen(false);
    setPendingBackupData(null);
    setBackupPreview(null);
  };

  // WebDAV 相关函数
  const openWebdavDialog = () => {
    setWebdavDialogOpen(true);
  };

  const handleSaveWebdavConfig = async () => {
    // 验证必填字段
    if (!webdavConfig.url.trim()) {
      toast.error('请填写服务器地址');
      return;
    }
    if (!webdavConfig.username.trim()) {
      toast.error('请填写用户名');
      return;
    }
    if (!webdavConfig.password.trim()) {
      toast.error('请填写密码');
      return;
    }

    try {
      setSavingWebdav(true);
      await updateWebDAVConfig(webdavConfig);
      toast.success('WebDAV 配置已保存');
      setWebdavDialogOpen(false);
    } catch (error) {
      console.error('Failed to save WebDAV config:', error);
      const errorMessage = error instanceof Error ? error.message : '保存失败';
      toast.error(`保存 WebDAV 配置失败: ${errorMessage}`);
    } finally {
      setSavingWebdav(false);
    }
  };

  const handleTestWebdav = async () => {
    try {
      setTestingWebdav(true);
      await testWebDAVConnection(webdavConfig);
      toast.success('WebDAV 连接测试成功');
    } catch (error) {
      console.error('WebDAV test failed:', error);
      const errorMessage = error instanceof Error ? error.message : '连接失败';
      toast.error(`WebDAV 连接测试失败: ${errorMessage}`);
    } finally {
      setTestingWebdav(false);
    }
  };

  const handleUploadWebdav = async () => {
    try {
      setUploadingWebdav(true);
      const data = await exportBackup();
      
      if (webdavConfig.encryption_enabled && webdavConfig.encryption_password) {
        // 加密备份数据
        const jsonContent = JSON.stringify(data, null, 2);
        const encrypted = await encryptBackupContent({
          content: jsonContent,
          password: webdavConfig.encryption_password,
        });
        const encryptedContent = JSON.stringify(encrypted, null, 2);
        await uploadWebDAVBackup(webdavConfig, data, true, encryptedContent);
        toast.success('加密备份已上传到 WebDAV');
      } else {
        await uploadWebDAVBackup(webdavConfig, data, false);
        toast.success('备份已上传到 WebDAV');
      }
    } catch (error) {
      console.error('WebDAV upload failed:', error);
      const errorMessage = error instanceof Error ? error.message : '上传失败';
      toast.error(`上传到 WebDAV 失败: ${errorMessage}`);
    } finally {
      setUploadingWebdav(false);
    }
  };

  const handleDownloadWebdav = async () => {
    try {
      setDownloadingWebdav(true);
      const response = await downloadWebDAVBackup(webdavConfig);
      
      if (response.is_encrypted) {
        // 备份是加密的，需要解密
        setEncryptedRawContent(response.raw_content || '');
        setDecryptPassword(webdavConfig.encryption_password || '');
        setDecryptDialogOpen(true);
      } else if (response.data) {
        // 普通备份，直接预览
        const preview = await previewBackup(response.data);
        setBackupPreview(preview);
        setPendingBackupData(response.data);
        setImportDialogOpen(true);
      }
    } catch (error) {
      console.error('WebDAV download failed:', error);
      const errorMessage = error instanceof Error ? error.message : '下载失败';
      toast.error(`从 WebDAV 下载失败: ${errorMessage}`);
    } finally {
      setDownloadingWebdav(false);
    }
  };

  const handleDecryptBackup = async () => {
    if (!decryptPassword) {
      toast.error('请输入解密密码');
      return;
    }

    try {
      setDecrypting(true);
      const envelope = tryParseEncryptedBackupEnvelope(encryptedRawContent);
      if (!envelope) {
        toast.error('无法解析加密备份');
        return;
      }

      const decryptedContent = await decryptBackupEnvelope({
        envelope,
        password: decryptPassword,
      });

      const data = JSON.parse(decryptedContent) as BackupData;
      const preview = await previewBackup(data);
      
      setDecryptDialogOpen(false);
      setDecryptPassword('');
      setEncryptedRawContent('');
      
      setBackupPreview(preview);
      setPendingBackupData(data);
      setImportDialogOpen(true);
    } catch (error) {
      console.error('Decrypt failed:', error);
      toast.error('解密失败，请检查密码是否正确');
    } finally {
      setDecrypting(false);
    }
  };

  const handleSyncNow = async () => {
    if (!webdavConfig.url) {
      toast.error('请先配置 WebDAV');
      return;
    }

    try {
      setSyncingNow(true);
      
      // 根据同步策略执行操作
      switch (webdavAutoSyncConfig.sync_strategy) {
        case 'upload_only':
          await handleUploadWebdav();
          break;
        case 'download_only':
          await handleDownloadWebdav();
          break;
        case 'merge':
          // 双向合并：先上传本地数据
          await handleUploadWebdav();
          toast.success('同步完成（已上传本地数据）');
          break;
        default:
          await handleUploadWebdav();
      }
    } catch (error) {
      console.error('Sync now failed:', error);
      const errorMessage = error instanceof Error ? error.message : '同步失败';
      toast.error(`立即同步失败: ${errorMessage}`);
    } finally {
      setSyncingNow(false);
    }
  };

  const openAutoSyncDialog = () => {
    setAutoSyncDialogOpen(true);
  };

  const handleSaveAutoSyncConfig = async () => {
    try {
      setSavingAutoSync(true);
      await updateWebDAVAutoSyncConfig(webdavAutoSyncConfig);
      toast.success('自动同步配置已保存');
      setAutoSyncDialogOpen(false);
    } catch (error) {
      console.error('Failed to save auto sync config:', error);
      const errorMessage = error instanceof Error ? error.message : '保存失败';
      toast.error(`保存自动同步配置失败: ${errorMessage}`);
    } finally {
      setSavingAutoSync(false);
    }
  };

  if (loading) {
    return <Loading />;
  }

  return (
    <div className="h-full min-h-0 flex flex-col gap-4 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">系统配置</h2>
          </div>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto space-y-4">
        {/* Anthropic 配置卡片 - 放在最上面 */}
        <Card>
          <CardHeader>
            <CardTitle>Anthropic 令牌计数配置</CardTitle>
            <CardDescription>
              配置 Anthropic API 用于令牌计数功能的连接信息
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div className="space-y-2">
                <Label>Base URL</Label>
                <p className="text-sm text-muted-foreground break-all">
                  {config?.base_url || '未配置'}
                </p>
              </div>
              <div className="space-y-2">
                <Label>API Key</Label>
                <p className="text-sm text-muted-foreground">
                  {config?.api_key ? (
                    <span className="font-mono">
                      {config.api_key.substring(0, 8)}...
                    </span>
                  ) : (
                    '未配置'
                  )}
                </p>
              </div>
              <div className="space-y-2">
                <Label>API Version</Label>
                <p className="text-sm text-muted-foreground">
                  {config?.version || '未配置'}
                </p>
              </div>
            </div>
          </CardContent>

          <CardFooter className="flex justify-between">
            <Button onClick={openEditDialog}>编辑配置</Button>
            <Button
              type="button"
              variant="outline"
              onClick={testConfig}
              disabled={!config?.api_key || testing}
            >
              {testing ? (
                <>
                  <span className="inline-block w-4 h-4 mr-2 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
                  检测中...
                </>
              ) : (
                '检测配置'
              )}
            </Button>
          </CardFooter>
        </Card>

        {/* 日志保留天数卡片 */}
        <Card>
          <CardHeader>
            <CardTitle>日志保留设置</CardTitle>
            <CardDescription>
              配置请求日志的自动清理策略，系统启动时会自动删除超过保留天数的日志
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div className="space-y-2">
                <Label>保留天数</Label>
                <p className="text-sm text-muted-foreground">
                  {logRetentionDays === 0 ? (
                    <span className="text-green-600 font-medium">永久保留</span>
                  ) : (
                    <span className="font-medium">{logRetentionDays} 天</span>
                  )}
                </p>
              </div>
              <div className="space-y-2">
                <Label>说明</Label>
                <p className="text-sm text-muted-foreground">
                  设置为 0 表示永久保留日志，不自动清理
                </p>
              </div>
            </div>
          </CardContent>
          <CardFooter>
            <Button onClick={openLogRetentionDialog}>编辑设置</Button>
          </CardFooter>
        </Card>

        {/* 数据备份与恢复卡片 - 放在最下面 */}
        <Card>
          <CardHeader>
            <CardTitle>数据备份与恢复</CardTitle>
            <CardDescription>
              导出或导入系统配置数据，包括提供商、模型、关联关系、API Keys 和系统配置
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-6">
            {/* 2x2 网格布局 */}
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              {/* 导出区域 */}
              <div className="space-y-3 p-4 rounded-lg border bg-card">
                <div className="flex items-center gap-2">
                  <FaDownload className="h-4 w-4 text-green-600" />
                  <Label className="text-base font-medium">导出数据</Label>
                </div>
                <p className="text-sm text-muted-foreground">
                  将所有配置数据导出为 JSON 文件，用于备份或迁移到其他实例。
                </p>
                <Button onClick={handleExport} disabled={exporting} variant="default" className="w-full">
                  {exporting ? (
                    <>
                      <span className="inline-block w-4 h-4 mr-2 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
                      导出中...
                    </>
                  ) : (
                    <>
                      <FaDownload className="mr-2" />
                      导出全部数据
                    </>
                  )}
                </Button>
              </div>

              {/* 导入区域 */}
              <div className="space-y-3 p-4 rounded-lg border bg-card">
                <div className="flex items-center gap-2">
                  <FaUpload className="h-4 w-4 text-blue-600" />
                  <Label className="text-base font-medium">导入数据</Label>
                </div>
                <p className="text-sm text-muted-foreground">
                  从 JSON 备份文件恢复配置数据，已存在的记录将被更新。
                </p>
                <div>
                  <input
                    type="file"
                    ref={fileInputRef}
                    onChange={handleFileSelect}
                    accept=".json"
                    className="hidden"
                  />
                  <Button
                    variant="outline"
                    onClick={() => fileInputRef.current?.click()}
                    className="w-full"
                  >
                    <FaUpload className="mr-2" />
                    选择备份文件
                  </Button>
                </div>
              </div>

              {/* WebDAV 备份区域 */}
              <div className="space-y-3 p-4 rounded-lg border bg-card">
                <div className="flex items-center gap-2">
                  <FaCloud className="h-4 w-4 text-indigo-600" />
                  <Label className="text-base font-medium">WebDAV 云端备份</Label>
                </div>
                <p className="text-sm text-muted-foreground">
                  将备份数据上传到 WebDAV 服务器，支持坚果云、NextCloud 等。
                </p>
                
                {/* WebDAV 配置显示 */}
                <div className="rounded-md bg-muted/50 p-2 space-y-1 text-xs">
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">服务器:</span>
                    <span className="font-mono truncate max-w-[120px]">{webdavConfig.url || '未配置'}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">状态:</span>
                    <span className={webdavConfig.url ? 'text-green-600' : 'text-muted-foreground'}>
                      {webdavConfig.url ? '已配置' : '未配置'}
                    </span>
                  </div>
                </div>

                <div className="flex flex-wrap gap-2">
                  <Button variant="outline" size="sm" onClick={openWebdavDialog}>
                    配置
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={handleTestWebdav}
                    disabled={!webdavConfig.url || testingWebdav}
                  >
                    {testingWebdav ? '测试中...' : '测试'}
                  </Button>
                  <Button
                    variant="default"
                    size="sm"
                    onClick={handleUploadWebdav}
                    disabled={!webdavConfig.url || uploadingWebdav}
                  >
                    {uploadingWebdav ? '上传中...' : '上传'}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={handleDownloadWebdav}
                    disabled={!webdavConfig.url || downloadingWebdav}
                  >
                    {downloadingWebdav ? '下载中...' : '下载'}
                  </Button>
                </div>
              </div>

              {/* WebDAV 自动同步区域 */}
              <div className="space-y-3 p-4 rounded-lg border bg-card">
                <div className="flex items-center gap-2">
                  <FaSync className="h-4 w-4 text-green-600" />
                  <Label className="text-base font-medium">WebDAV 自动同步</Label>
                </div>
                <p className="text-sm text-muted-foreground">
                  配置自动同步策略，定期将数据备份到 WebDAV 服务器。
                </p>
                
                {/* 自动同步配置显示 */}
                <div className="rounded-md bg-muted/50 p-2 space-y-1 text-xs">
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">自动同步:</span>
                    <span className={webdavAutoSyncConfig.enabled ? 'text-green-600 font-medium' : 'text-muted-foreground'}>
                      {webdavAutoSyncConfig.enabled ? '已启用' : '未启用'}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">间隔:</span>
                    <span>{Math.floor(webdavAutoSyncConfig.sync_interval / 60)} 分钟</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">策略:</span>
                    <span>
                      {webdavAutoSyncConfig.sync_strategy === 'merge' && '双向合并'}
                      {webdavAutoSyncConfig.sync_strategy === 'upload_only' && '仅上传'}
                      {webdavAutoSyncConfig.sync_strategy === 'download_only' && '仅下载'}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">加密:</span>
                    <span className={webdavConfig.encryption_enabled ? 'text-green-600 font-medium' : 'text-muted-foreground'}>
                      {webdavConfig.encryption_enabled ? (
                        <span className="flex items-center gap-1"><FaLock className="h-3 w-3" /> 已启用</span>
                      ) : (
                        <span className="flex items-center gap-1"><FaUnlock className="h-3 w-3" /> 未启用</span>
                      )}
                    </span>
                  </div>
                </div>

                <div className="flex gap-2">
                  <Button variant="outline" size="sm" onClick={openAutoSyncDialog} className="flex-1">
                    配置
                  </Button>
                  <Button
                    variant="default"
                    size="sm"
                    onClick={handleSyncNow}
                    disabled={!webdavConfig.url || syncingNow}
                    className="flex-1"
                  >
                    {syncingNow ? (
                      <>
                        <span className="inline-block w-3 h-3 mr-1 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
                        同步中...
                      </>
                    ) : (
                      <>
                        <FaSync className="mr-1 h-3 w-3" />
                        立即同步
                      </>
                    )}
                  </Button>
                </div>
              </div>
            </div>

            {/* 重要提示 */}
            <div className="rounded-md bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800 p-4">
              <p className="text-sm font-medium text-amber-800 dark:text-amber-200 mb-2">⚠️ 重要提示</p>
              <ul className="text-sm text-amber-700 dark:text-amber-300 space-y-1">
                <li>• 导入操作会更新已存在的同名记录</li>
                <li>• 建议在导入前先导出当前数据作为备份</li>
                <li>• 导入的 API Key 将保持原有的密钥值</li>
                <li>• 模型与提供商的关联关系会根据名称自动匹配</li>
                <li>• WebDAV 备份文件名为 llmio_backup.json</li>
              </ul>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 导入确认对话框 */}
      <Dialog open={importDialogOpen} onOpenChange={setImportDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>确认导入数据</DialogTitle>
            <DialogDescription>
              即将导入以下数据，已存在的记录将被更新
            </DialogDescription>
          </DialogHeader>

          {backupPreview && (
            <div className="space-y-3 py-4">
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">备份版本:</span>
                <span>{backupPreview.version}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">导出时间:</span>
                <span>{new Date(backupPreview.exported_at).toLocaleString()}</span>
              </div>
              <hr />
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">提供商数量:</span>
                <span className="font-medium">{backupPreview.providers_count}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">模型数量:</span>
                <span className="font-medium">{backupPreview.models_count}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">模型提供商关联:</span>
                <span className="font-medium">{backupPreview.model_providers_count}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">API Keys:</span>
                <span className="font-medium">{backupPreview.auth_keys_count}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">配置项:</span>
                <span className="font-medium">{backupPreview.configs_count}</span>
              </div>
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={handleCancelImport}>
              取消
            </Button>
            <Button onClick={handleConfirmImport} disabled={importing}>
              {importing ? (
                <>
                  <span className="inline-block w-4 h-4 mr-2 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
                  导入中...
                </>
              ) : (
                <>
                  <FaFileImport className="mr-2" />
                  确认导入
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 日志保留天数编辑对话框 */}
      <Dialog open={logRetentionDialogOpen} onOpenChange={setLogRetentionDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>编辑日志保留设置</DialogTitle>
            <DialogDescription>
              设置请求日志的保留天数，超过该天数的日志将在系统启动时自动删除
            </DialogDescription>
          </DialogHeader>

          <Form {...logRetentionForm}>
            <form onSubmit={logRetentionForm.handleSubmit(onLogRetentionSubmit)} className="space-y-4">
              <FormField
                control={logRetentionForm.control}
                name="days"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>保留天数</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        min={0}
                        placeholder="0"
                        {...field}
                        onChange={e => field.onChange(parseInt(e.target.value, 10) || 0)}
                      />
                    </FormControl>
                    <FormDescription>
                      设置为 0 表示永久保留日志，不自动清理
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setLogRetentionDialogOpen(false)}>
                  取消
                </Button>
                <Button type="submit" disabled={savingLogRetention}>
                  {savingLogRetention ? '保存中...' : '保存'}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

      {/* WebDAV 配置对话框 */}
      <Dialog open={webdavDialogOpen} onOpenChange={setWebdavDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>配置 WebDAV</DialogTitle>
            <DialogDescription>
              配置 WebDAV 服务器连接信息，支持坚果云、NextCloud 等服务
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label htmlFor="webdav-url">服务器地址</Label>
              <Input
                id="webdav-url"
                placeholder="https://dav.jianguoyun.com/dav/llmio/"
                value={webdavConfig.url}
                onChange={(e) => setWebdavConfig({ ...webdavConfig, url: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">
                例如坚果云: https://dav.jianguoyun.com/dav/文件夹名/
              </p>
            </div>

            <div className="space-y-2">
              <Label htmlFor="webdav-username">用户名</Label>
              <Input
                id="webdav-username"
                placeholder="your@email.com"
                value={webdavConfig.username}
                onChange={(e) => setWebdavConfig({ ...webdavConfig, username: e.target.value })}
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="webdav-password">密码 / 应用密码</Label>
              <div className="relative">
                <Input
                  id="webdav-password"
                  type={showWebdavPassword ? 'text' : 'password'}
                  placeholder="应用专用密码"
                  value={webdavConfig.password}
                  onChange={(e) => setWebdavConfig({ ...webdavConfig, password: e.target.value })}
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="absolute right-0 top-0 h-full px-3"
                  onClick={() => setShowWebdavPassword(!showWebdavPassword)}
                >
                  {showWebdavPassword ? <FaEyeSlash className="h-4 w-4" /> : <FaEye className="h-4 w-4" />}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                建议使用应用专用密码而非账户密码
              </p>
            </div>

            <hr className="my-4" />

            <div className="flex items-center justify-between">
              <div className="space-y-0.5">
                <Label className="flex items-center gap-2">
                  <FaLock className="h-4 w-4" />
                  启用备份加密
                </Label>
                <p className="text-xs text-muted-foreground">
                  使用 AES-256-GCM 加密备份数据
                </p>
              </div>
              <Switch
                checked={webdavConfig.encryption_enabled || false}
                onCheckedChange={(checked) =>
                  setWebdavConfig({ ...webdavConfig, encryption_enabled: checked })
                }
              />
            </div>

            {webdavConfig.encryption_enabled && (
              <div className="space-y-2">
                <Label htmlFor="encryption-password">加密密码</Label>
                <div className="relative">
                  <Input
                    id="encryption-password"
                    type={showEncryptionPassword ? 'text' : 'password'}
                    placeholder="输入加密密码"
                    value={webdavConfig.encryption_password || ''}
                    onChange={(e) => setWebdavConfig({ ...webdavConfig, encryption_password: e.target.value })}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="absolute right-0 top-0 h-full px-3"
                    onClick={() => setShowEncryptionPassword(!showEncryptionPassword)}
                  >
                    {showEncryptionPassword ? <FaEyeSlash className="h-4 w-4" /> : <FaEye className="h-4 w-4" />}
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground text-amber-600">
                  ⚠️ 请牢记此密码，丢失后将无法恢复加密的备份数据
                </p>
              </div>
            )}
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setWebdavDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={handleSaveWebdavConfig} disabled={savingWebdav}>
              {savingWebdav ? '保存中...' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* WebDAV 自动同步配置对话框 */}
      <Dialog open={autoSyncDialogOpen} onOpenChange={setAutoSyncDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>配置自动同步</DialogTitle>
            <DialogDescription>
              设置 WebDAV 自动同步策略
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            <div className="flex items-center justify-between">
              <div className="space-y-0.5">
                <Label>启用自动同步</Label>
                <p className="text-xs text-muted-foreground">
                  定期自动备份数据到 WebDAV
                </p>
              </div>
              <Switch
                checked={webdavAutoSyncConfig.enabled}
                onCheckedChange={(checked) =>
                  setWebdavAutoSyncConfig({ ...webdavAutoSyncConfig, enabled: checked })
                }
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="sync-interval">同步间隔（秒）</Label>
              <Input
                id="sync-interval"
                type="number"
                min={60}
                max={86400}
                step={60}
                value={webdavAutoSyncConfig.sync_interval}
                onChange={(e) =>
                  setWebdavAutoSyncConfig({
                    ...webdavAutoSyncConfig,
                    sync_interval: parseInt(e.target.value, 10) || 3600
                  })
                }
              />
              <p className="text-xs text-muted-foreground">
                当前设置: {Math.floor(webdavAutoSyncConfig.sync_interval / 60)} 分钟
              </p>
            </div>

            <div className="space-y-2">
              <Label htmlFor="sync-strategy">同步策略</Label>
              <Select
                value={webdavAutoSyncConfig.sync_strategy}
                onValueChange={(value: 'merge' | 'upload_only' | 'download_only') =>
                  setWebdavAutoSyncConfig({ ...webdavAutoSyncConfig, sync_strategy: value })
                }
              >
                <SelectTrigger>
                  <SelectValue placeholder="选择同步策略" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="merge">双向合并</SelectItem>
                  <SelectItem value="upload_only">仅上传（本地优先）</SelectItem>
                  <SelectItem value="download_only">仅下载（远程优先）</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                双向合并会智能合并本地和远程数据
              </p>
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setAutoSyncDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={handleSaveAutoSyncConfig} disabled={savingAutoSync}>
              {savingAutoSync ? '保存中...' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 解密对话框 */}
      <Dialog open={decryptDialogOpen} onOpenChange={setDecryptDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <FaLock className="h-4 w-4" />
              解密备份
            </DialogTitle>
            <DialogDescription>
              检测到加密的备份文件，请输入解密密码
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label htmlFor="decrypt-password">解密密码</Label>
              <div className="relative">
                <Input
                  id="decrypt-password"
                  type={showEncryptionPassword ? 'text' : 'password'}
                  placeholder="输入解密密码"
                  value={decryptPassword}
                  onChange={(e) => setDecryptPassword(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      handleDecryptBackup();
                    }
                  }}
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="absolute right-0 top-0 h-full px-3"
                  onClick={() => setShowEncryptionPassword(!showEncryptionPassword)}
                >
                  {showEncryptionPassword ? <FaEyeSlash className="h-4 w-4" /> : <FaEye className="h-4 w-4" />}
                </Button>
              </div>
            </div>
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setDecryptDialogOpen(false);
                setDecryptPassword('');
                setEncryptedRawContent('');
              }}
            >
              取消
            </Button>
            <Button onClick={handleDecryptBackup} disabled={decrypting || !decryptPassword}>
              {decrypting ? (
                <>
                  <span className="inline-block w-4 h-4 mr-2 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
                  解密中...
                </>
              ) : (
                <>
                  <FaUnlock className="mr-2" />
                  解密
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Anthropic 配置编辑对话框 */}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>编辑 Anthropic 配置</DialogTitle>
            <DialogDescription>
              修改 Anthropic API 连接信息
            </DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <FormField
                control={form.control}
                name="base_url"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Base URL</FormLabel>
                    <FormControl>
                      <Input placeholder="https://api.anthropic.com/v1" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="api_key"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>API Key</FormLabel>
                    <FormControl>
                      <Input placeholder="sk-ant-..." {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="version"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>API Version</FormLabel>
                    <FormControl>
                      <Input placeholder="2023-06-01" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={closeDialog}>
                  取消
                </Button>
                <Button type="submit">保存</Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
