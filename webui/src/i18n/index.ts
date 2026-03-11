import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';

// zh-CN
import zhCNCommon from './locales/zh-CN/common.json';
import zhCNLayout from './locales/zh-CN/layout.json';
import zhCNHome from './locales/zh-CN/home.json';
import zhCNLogin from './locales/zh-CN/login.json';
import zhCNProviders from './locales/zh-CN/providers.json';
import zhCNModels from './locales/zh-CN/models.json';
import zhCNLogs from './locales/zh-CN/logs.json';
import zhCNAuthKeys from './locales/zh-CN/auth-keys.json';
import zhCNConfig from './locales/zh-CN/config.json';

// zh-TW
import zhTWCommon from './locales/zh-TW/common.json';
import zhTWLayout from './locales/zh-TW/layout.json';
import zhTWHome from './locales/zh-TW/home.json';
import zhTWLogin from './locales/zh-TW/login.json';
import zhTWProviders from './locales/zh-TW/providers.json';
import zhTWModels from './locales/zh-TW/models.json';
import zhTWLogs from './locales/zh-TW/logs.json';
import zhTWAuthKeys from './locales/zh-TW/auth-keys.json';
import zhTWConfig from './locales/zh-TW/config.json';

// en
import enCommon from './locales/en/common.json';
import enLayout from './locales/en/layout.json';
import enHome from './locales/en/home.json';
import enLogin from './locales/en/login.json';
import enProviders from './locales/en/providers.json';
import enModels from './locales/en/models.json';
import enLogs from './locales/en/logs.json';
import enAuthKeys from './locales/en/auth-keys.json';
import enConfig from './locales/en/config.json';

export const SUPPORTED_LANGUAGES = [
  { code: 'zh-CN', label: '简体中文' },
  { code: 'zh-TW', label: '繁體中文' },
  { code: 'en',    label: 'English' },
] as const;

export type SupportedLanguage = typeof SUPPORTED_LANGUAGES[number]['code'];

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      'zh-CN': {
        common:      zhCNCommon,
        layout:      zhCNLayout,
        home:        zhCNHome,
        login:       zhCNLogin,
        providers:   zhCNProviders,
        models:      zhCNModels,
        logs:        zhCNLogs,
        'auth-keys': zhCNAuthKeys,
        config:      zhCNConfig,
      },
      'zh-TW': {
        common:      zhTWCommon,
        layout:      zhTWLayout,
        home:        zhTWHome,
        login:       zhTWLogin,
        providers:   zhTWProviders,
        models:      zhTWModels,
        logs:        zhTWLogs,
        'auth-keys': zhTWAuthKeys,
        config:      zhTWConfig,
      },
      en: {
        common:      enCommon,
        layout:      enLayout,
        home:        enHome,
        login:       enLogin,
        providers:   enProviders,
        models:      enModels,
        logs:        enLogs,
        'auth-keys': enAuthKeys,
        config:      enConfig,
      },
    },
    fallbackLng: 'zh-CN',
    defaultNS: 'common',
    ns: ['common', 'layout', 'home', 'login', 'providers', 'models', 'logs', 'auth-keys', 'config'],
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
      common:      typeof zhCNCommon;
      layout:      typeof zhCNLayout;
      home:        typeof zhCNHome;
      login:       typeof zhCNLogin;
      providers:   typeof zhCNProviders;
      models:      typeof zhCNModels;
      logs:        typeof zhCNLogs;
      'auth-keys': typeof zhCNAuthKeys;
      config:      typeof zhCNConfig;
    };
  }
}