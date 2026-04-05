import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
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
} from '@/components/ui/form';
import Loading from '@/components/loading';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { Switch } from '@/components/ui/switch';
import { configAPI, type AnthropicCountTokens, type LogCleanupPolicy, testCountTokens } from '@/lib/api';
import { toast } from 'sonner';

const anthropicConfigSchema = z.object({
  base_url: z.string().min(1),
  api_key: z.string().min(1),
  version: z.string().min(1),
});

type AnthropicConfigForm = z.infer<typeof anthropicConfigSchema>;

const logCleanupConfigSchema = z.object({
  enabled: z.boolean(),
  retention_days: z.number().min(1),
});

type LogCleanupConfigForm = z.infer<typeof logCleanupConfigSchema>;

const defaultLogCleanupConfig: LogCleanupPolicy = {
  enabled: false,
  retention_days: 30,
};

export default function ConfigPage() {
  const { t } = useTranslation(['config', 'common']);
  const [loading, setLoading] = useState(true);
  const [anthropicOpen, setAnthropicOpen] = useState(false);
  const [logCleanupOpen, setLogCleanupOpen] = useState(false);
  const [anthropicConfig, setAnthropicConfig] = useState<AnthropicCountTokens | null>(null);
  const [logCleanupConfig, setLogCleanupConfig] = useState<LogCleanupPolicy>(defaultLogCleanupConfig);
  const [testing, setTesting] = useState(false);

  const anthropicForm = useForm<AnthropicConfigForm>({
    resolver: zodResolver(anthropicConfigSchema),
    defaultValues: {
      base_url: '',
      api_key: '',
      version: '2023-06-01',
    },
  });

  const logCleanupForm = useForm<LogCleanupConfigForm>({
    resolver: zodResolver(logCleanupConfigSchema),
    defaultValues: defaultLogCleanupConfig,
  });

  useEffect(() => {
    const fetchConfig = async () => {
      try {
        setLoading(true);
        const [anthropicResponse, logCleanupResponse] = await Promise.all([
          configAPI.getConfig('anthropic_count_tokens'),
          configAPI.getConfig('log_cleanup_policy'),
        ]);

        if (anthropicResponse.value) {
          const nextAnthropicConfig = JSON.parse(anthropicResponse.value) as AnthropicCountTokens;
          setAnthropicConfig(nextAnthropicConfig);
        }

        if (logCleanupResponse.value) {
          const nextLogCleanupConfig = {
            ...defaultLogCleanupConfig,
            ...(JSON.parse(logCleanupResponse.value) as Partial<LogCleanupPolicy>),
          };
          setLogCleanupConfig(nextLogCleanupConfig);
        } else {
          setLogCleanupConfig(defaultLogCleanupConfig);
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

  const openAnthropicDialog = () => {
    anthropicForm.reset({
      base_url: anthropicConfig?.base_url || 'https://api.anthropic.com/v1',
      api_key: anthropicConfig?.api_key || '',
      version: anthropicConfig?.version || '2023-06-01',
    });
    setAnthropicOpen(true);
  };

  const openLogCleanupDialog = () => {
    logCleanupForm.reset(logCleanupConfig);
    setLogCleanupOpen(true);
  };

  const testConfig = async () => {
    try {
      setTesting(true);
      await testCountTokens();
      toast.success(t('toast.test_success'));
    } catch (error) {
      console.error('Config test failed:', error);
      const errorMessage = error instanceof Error ? error.message : String(error);
      toast.error(t('toast.test_failed', { message: errorMessage }));
    } finally {
      setTesting(false);
    }
  };

  const onSubmitAnthropic = async (values: AnthropicConfigForm) => {
    try {
      await configAPI.updateConfig('anthropic_count_tokens', values);
      setAnthropicConfig(values);
      toast.success(t('toast.save_success'));
      setAnthropicOpen(false);
    } catch (error) {
      console.error('Failed to save config:', error);
      toast.error(t('toast.save_failed'));
    }
  };

  const onSubmitLogCleanup = async (values: LogCleanupConfigForm) => {
    try {
      await configAPI.updateConfig('log_cleanup_policy', values);
      setLogCleanupConfig(values);
      toast.success(t('toast.save_success'));
      setLogCleanupOpen(false);
    } catch (error) {
      console.error('Failed to save log cleanup config:', error);
      toast.error(t('toast.save_failed'));
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
            <h2 className="text-2xl font-bold tracking-tight">{t('title')}</h2>
          </div>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto">
        <Card>
          <CardHeader>
            <CardTitle>{t('anthropic.title')}</CardTitle>
              <CardDescription>
                {t('anthropic.desc')}
              </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div className="space-y-2">
                <Label>{t('anthropic.base_url')}</Label>
                <p className="text-sm text-muted-foreground break-all">
                  {anthropicConfig?.base_url || t('anthropic.not_configured')}
                </p>
              </div>
              <div className="space-y-2">
                <Label>{t('anthropic.api_key')}</Label>
                <p className="text-sm text-muted-foreground">
                  {anthropicConfig?.api_key ? (
                    <span className="font-mono">
                      {anthropicConfig.api_key.substring(0, 8)}...
                    </span>
                  ) : (
                    t('anthropic.not_configured')
                  )}
                </p>
              </div>
              <div className="space-y-2">
                <Label>{t('anthropic.version')}</Label>
                <p className="text-sm text-muted-foreground">
                  {anthropicConfig?.version || t('anthropic.not_configured')}
                </p>
              </div>
            </div>
          </CardContent>

          <CardFooter className="flex justify-between">
            <Button onClick={openAnthropicDialog}>{t('anthropic.edit')}</Button>
            <Button
              type="button"
              variant="outline"
              onClick={testConfig}
              disabled={!anthropicConfig?.api_key || testing}
            >
              {testing ? (
                <>
                  <span className="inline-block w-4 h-4 mr-2 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
                  {t('anthropic.testing')}
                </>
              ) : (
                t('anthropic.test')
              )}
            </Button>
          </CardFooter>
        </Card>

        <Card className="mt-4">
          <CardHeader>
            <CardTitle>{t('log_cleanup.title')}</CardTitle>
            <CardDescription>{t('log_cleanup.desc')}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div className="space-y-2">
                <Label>{t('log_cleanup.enabled')}</Label>
                <p className="text-sm text-muted-foreground">
                  {logCleanupConfig.enabled ? t('log_cleanup.enabled_on') : t('log_cleanup.enabled_off')}
                </p>
              </div>
              <div className="space-y-2">
                <Label>{t('log_cleanup.retention_days')}</Label>
                <p className="text-sm text-muted-foreground">
                  {t('log_cleanup.retention_days_value', { days: logCleanupConfig.retention_days })}
                </p>
              </div>
            </div>
          </CardContent>
          <CardFooter>
            <Button onClick={openLogCleanupDialog}>{t('log_cleanup.edit')}</Button>
          </CardFooter>
        </Card>
      </div>

      <Dialog open={anthropicOpen} onOpenChange={setAnthropicOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t('anthropic.edit_title')}</DialogTitle>
              <DialogDescription>
                {t('anthropic.edit_desc')}
              </DialogDescription>
          </DialogHeader>

          <Form {...anthropicForm}>
            <form onSubmit={anthropicForm.handleSubmit(onSubmitAnthropic)} className="space-y-4">
              <FormField
                control={anthropicForm.control}
                name="base_url"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('anthropic.base_url')}</FormLabel>
                    <FormControl>
                      <Input placeholder="https://api.anthropic.com/v1" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={anthropicForm.control}
                name="api_key"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('anthropic.api_key')}</FormLabel>
                    <FormControl>
                      <Input placeholder="sk-ant-..." {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={anthropicForm.control}
                name="version"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('anthropic.version')}</FormLabel>
                    <FormControl>
                      <Input placeholder="2023-06-01" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setAnthropicOpen(false)}>
                  {t('common:actions.cancel')}
                </Button>
                <Button type="submit">{t('common:actions.save')}</Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

      <Dialog open={logCleanupOpen} onOpenChange={setLogCleanupOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t('log_cleanup.edit_title')}</DialogTitle>
            <DialogDescription>{t('log_cleanup.edit_desc')}</DialogDescription>
          </DialogHeader>

          <Form {...logCleanupForm}>
            <form onSubmit={logCleanupForm.handleSubmit(onSubmitLogCleanup)} className="space-y-4">
              <FormField
                control={logCleanupForm.control}
                name="enabled"
                render={({ field }) => (
                  <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                    <div className="space-y-0.5">
                      <FormLabel>{t('log_cleanup.enabled')}</FormLabel>
                    </div>
                    <FormControl>
                      <Switch checked={field.value} onCheckedChange={field.onChange} />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={logCleanupForm.control}
                name="retention_days"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('log_cleanup.retention_days')}</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        min={1}
                        {...field}
                        onChange={(event) => field.onChange(Number(event.target.value))}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setLogCleanupOpen(false)}>
                  {t('common:actions.cancel')}
                </Button>
                <Button type="submit">{t('common:actions.save')}</Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
