import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';

// zh-CN
import zhCNCommon from './locales/zh-CN/common.json';
import zhCNLayout from './locales/zh-CN/layout.json';
import zhCNHome from './locales/zh-CN/home.json';
import zhCNQuickstart from './locales/zh-CN/quickstart.json';
import zhCNLogin from './locales/zh-CN/login.json';
import zhCNProviders from './locales/zh-CN/providers.json';
import zhCNModels from './locales/zh-CN/models.json';
import zhCNLogs from './locales/zh-CN/logs.json';
import zhCNAuthKeys from './locales/zh-CN/auth-keys.json';
import zhCNConfig from './locales/zh-CN/config.json';
import zhCNCompare from './locales/zh-CN/compare.json';
import zhCNQuota from './locales/zh-CN/quota.json';
import zhCNAnalytics from './locales/zh-CN/analytics.json';
import zhCNPeak from './locales/zh-CN/peak.json';

// zh-TW
import zhTWCommon from './locales/zh-TW/common.json';
import zhTWLayout from './locales/zh-TW/layout.json';
import zhTWHome from './locales/zh-TW/home.json';
import zhTWQuickstart from './locales/zh-TW/quickstart.json';
import zhTWLogin from './locales/zh-TW/login.json';
import zhTWProviders from './locales/zh-TW/providers.json';
import zhTWModels from './locales/zh-TW/models.json';
import zhTWLogs from './locales/zh-TW/logs.json';
import zhTWAuthKeys from './locales/zh-TW/auth-keys.json';
import zhTWConfig from './locales/zh-TW/config.json';
import zhTWCompare from './locales/zh-TW/compare.json';
import zhTWQuota from './locales/zh-TW/quota.json';
import zhTWAnalytics from './locales/zh-TW/analytics.json';
import zhTWPeak from './locales/zh-TW/peak.json';

// en
import enCommon from './locales/en/common.json';
import enLayout from './locales/en/layout.json';
import enHome from './locales/en/home.json';
import enQuickstart from './locales/en/quickstart.json';
import enLogin from './locales/en/login.json';
import enProviders from './locales/en/providers.json';
import enModels from './locales/en/models.json';
import enLogs from './locales/en/logs.json';
import enAuthKeys from './locales/en/auth-keys.json';
import enConfig from './locales/en/config.json';
import enCompare from './locales/en/compare.json';
import enQuota from './locales/en/quota.json';
import enAnalytics from './locales/en/analytics.json';
import enPeak from './locales/en/peak.json';

export const SUPPORTED_LANGUAGES = [
  { code: 'zh-CN', label: '简体中文' },
  { code: 'zh-TW', label: '繁體中文' },
  { code: 'en-US', label: 'English' },
] as const;

export type SupportedLanguage = typeof SUPPORTED_LANGUAGES[number]['code'];

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      'zh-CN': {
        common: zhCNCommon,
        layout: zhCNLayout,
        home: zhCNHome,
        quickstart: zhCNQuickstart,
        login: zhCNLogin,
        providers: zhCNProviders,
        models: zhCNModels,
        logs: zhCNLogs,
        'auth-keys': zhCNAuthKeys,
        config: zhCNConfig,
        compare: zhCNCompare,
        quota: zhCNQuota,
        analytics: zhCNAnalytics,
        peak: zhCNPeak,
      },
      'zh-TW': {
        common: zhTWCommon,
        layout: zhTWLayout,
        home: zhTWHome,
        quickstart: zhTWQuickstart,
        login: zhTWLogin,
        providers: zhTWProviders,
        models: zhTWModels,
        logs: zhTWLogs,
        'auth-keys': zhTWAuthKeys,
        config: zhTWConfig,
        compare: zhTWCompare,
        quota: zhTWQuota,
        analytics: zhTWAnalytics,
        peak: zhTWPeak,
      },
      'en-US': {
        common: enCommon,
        layout: enLayout,
        home: enHome,
        quickstart: enQuickstart,
        login: enLogin,
        providers: enProviders,
        models: enModels,
        logs: enLogs,
        'auth-keys': enAuthKeys,
        config: enConfig,
        compare: enCompare,
        quota: enQuota,
        analytics: enAnalytics,
        peak: enPeak,
      },
    },
    fallbackLng: 'zh-CN',
    defaultNS: 'common',
    ns: ['common', 'layout', 'home', 'quickstart', 'login', 'providers', 'models', 'logs', 'auth-keys', 'config', 'compare', 'quota', 'analytics', 'peak'],
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: 'llmio-language',
      caches: ['localStorage'],
    },
    interpolation: {
      escapeValue: false,
    },
  });

export default i18n;

// ── TypeScript type augmentation ─────────────────────────────────────────────
// Pointing all namespaces at zh-CN as the source of truth gives full
// key-autocomplete and compile-time typo detection via useTranslation().
declare module 'i18next' {
  interface CustomTypeOptions {
    defaultNS: 'common';
    resources: {
      common: typeof zhCNCommon;
      layout: typeof zhCNLayout;
      home: typeof zhCNHome;
      quickstart: typeof zhCNQuickstart;
      login: typeof zhCNLogin;
      providers: typeof zhCNProviders;
      models: typeof zhCNModels;
      logs: typeof zhCNLogs;
      'auth-keys': typeof zhCNAuthKeys;
      config: typeof zhCNConfig;
      compare: typeof zhCNCompare;
      quota: typeof zhCNQuota;
      analytics: typeof zhCNAnalytics;
      peak: typeof zhCNPeak;
    };
  }
}
