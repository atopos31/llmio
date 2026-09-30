import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import { Suspense, lazy } from 'react';
import { ThemeProvider } from "@/components/theme-provider"
import Loading from "@/components/loading"
import { Toaster } from './components/ui/sonner';

// 懒加载路由组件
const Layout = lazy(() => import('./routes/layout'));
const Home = lazy(() => import('./routes/home'));
const Quickstart = lazy(() => import('./routes/quickstart'));
const ProvidersPage = lazy(() => import('./routes/providers'));
const ModelProvidersPage = lazy(() => import('./routes/model-providers'));
const LogsPage = lazy(() => import('./routes/logs'));
const LogChatPage = lazy(() => import('./routes/log-chat'));
const ComparePage = lazy(() => import('./routes/compare'));
const QuotaPage = lazy(() => import('./routes/quota'));
const LoginPage = lazy(() => import('./routes/login'));
const ConfigPage = lazy(() => import('./routes/config'));
const AuthKeysPage = lazy(() => import('./routes/auth-keys'));

// 简单的加载组件。高度与外壳一致用 dvh 而不是 vh：
// 移动端 100vh 是"地址栏收起时"的高度，比可见区域高，会撑出滚动条。
const PageLoader = () => (
  <div className="flex h-dvh items-center justify-center">
    <Loading message="加载中..." />
  </div>
);

function App() {
  // 不显式传 storageKey：让 ThemeProvider 用 @/lib/theme 里的默认键，
  // 与 index.html 中首屏内联脚本读取的键保持同一真相来源。
  //
  // 原先这里硬编码了另一个 storageKey，与首屏脚本读的键不一致，
  // 会让首屏脚本写入的主题 Provider 读不到，表现为刷新后主题闪回、
  // 或切换后下次打开又变回去。这类错误不报错，由
  // src/lib/design-tokens.test.ts 的断言守住。
  return (
    <ThemeProvider defaultTheme="system">
      <Router>
        <Suspense fallback={<PageLoader />}>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/" element={<Layout />}>
              <Route index element={<Home />} />
              <Route path="quickstart" element={<Quickstart />} />
              <Route path="providers" element={<ProvidersPage />} />
              <Route path="models" element={<ModelProvidersPage />} />
              <Route path="model-providers" element={<ModelProvidersPage />} />
              <Route path="logs" element={<LogsPage />} />
              <Route path="logs/:logId/chat-io" element={<LogChatPage />} />
              <Route path="compare" element={<ComparePage />} />
              <Route path="quota" element={<QuotaPage />} />
              <Route path="config" element={<ConfigPage />} />
              <Route path="auth-keys" element={<AuthKeysPage />} />
            </Route>
          </Routes>
        </Suspense>
      </Router>
      <Toaster richColors position='top-center' />
    </ThemeProvider>
  );
}

export default App;
