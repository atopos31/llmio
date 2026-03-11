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
import { configAPI, type AnthropicCountTokens, testCountTokens } from '@/lib/api';
import { toast } from 'sonner';

const anthropicConfigSchema = z.object({
  base_url: z.string().min(1),
  api_key: z.string().min(1),
  version: z.string().min(1),
});

type AnthropicConfigForm = z.infer<typeof anthropicConfigSchema>;

export default function ConfigPage() {
  const { t } = useTranslation(['config', 'common']);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [config, setConfig] = useState<AnthropicCountTokens | null>(null);
  const [testing, setTesting] = useState(false);

  const form = useForm<AnthropicConfigForm>({
    resolver: zodResolver(anthropicConfigSchema),
    defaultValues: {
      base_url: '',
      api_key: '',
      version: '2023-06-01',
    },
  });

  useEffect(() => {
    const fetchConfig = async () => {
      try {
        setLoading(true);
        const response = await configAPI.getConfig('anthropic_count_tokens');
        if (response.value) {
          const anthropicConfig = JSON.parse(response.value) as AnthropicCountTokens;
          setConfig(anthropicConfig);
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
      toast.success(t('toast.test_success'));
    } catch (error) {
      console.error('Config test failed:', error);
      const errorMessage = error instanceof Error ? error.message : String(error);
      toast.error(t('toast.test_failed', { message: errorMessage }));
    } finally {
      setTesting(false);
    }
  };

  const onSubmit = async (values: AnthropicConfigForm) => {
    try {
      await configAPI.updateConfig('anthropic_count_tokens', values);
      setConfig(values);
      toast.success(t('toast.save_success'));
      setOpen(false);
    } catch (error) {
      console.error('Failed to save config:', error);
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
                  {config?.base_url || t('anthropic.not_configured')}
                </p>
              </div>
              <div className="space-y-2">
                <Label>{t('anthropic.api_key')}</Label>
                <p className="text-sm text-muted-foreground">
                  {config?.api_key ? (
                    <span className="font-mono">
                      {config.api_key.substring(0, 8)}...
                    </span>
                  ) : (
                    t('anthropic.not_configured')
                  )}
                </p>
              </div>
              <div className="space-y-2">
                <Label>{t('anthropic.version')}</Label>
                <p className="text-sm text-muted-foreground">
                  {config?.version || t('anthropic.not_configured')}
                </p>
              </div>
            </div>
          </CardContent>

          <CardFooter className="flex justify-between">
            <Button onClick={openEditDialog}>{t('anthropic.edit')}</Button>
            <Button
              type="button"
              variant="outline"
              onClick={testConfig}
              disabled={!config?.api_key || testing}
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
      </div>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t('anthropic.edit_title')}</DialogTitle>
              <DialogDescription>
                {t('anthropic.edit_desc')}
              </DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <FormField
                control={form.control}
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
                control={form.control}
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
                control={form.control}
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
                <Button type="button" variant="outline" onClick={closeDialog}>
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
