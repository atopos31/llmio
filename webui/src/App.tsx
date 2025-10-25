import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import { Suspense, lazy } from 'react';
import { ThemeProvider } from "@/components/theme-provider"
import Loading from "@/components/loading"

// 懒加载路由组件
const Layout = lazy(() => import('./routes/layout'));
const Home = lazy(() => import('./routes/home'));
const ProvidersPage = lazy(() => import('./routes/providers'));
const ModelsPage = lazy(() => import('./routes/models'));
const ModelProvidersPage = lazy(() => import('./routes/model-providers'));
const LogsPage = lazy(() => import('./routes/logs'));
const LoginPage = lazy(() => import('./routes/login'));

// 简单的加载组件
const PageLoader = () => (
  <div className="flex items-center justify-center min-h-screen">
    <Loading message="加载中..." />
  </div>
);

function App() {
  return (
    <ThemeProvider defaultTheme="system" storageKey="vite-ui-theme">
      <Router>
        <Suspense fallback={<PageLoader />}>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/" element={<Layout />}>
              <Route index element={<Home />} />
              <Route path="providers" element={<ProvidersPage />} />
              <Route path="models" element={<ModelsPage />} />
              <Route path="model-providers" element={<ModelProvidersPage />} />
              <Route path="logs" element={<LogsPage />} />
            </Route>
          </Routes>
        </Suspense>
      </Router>
    </ThemeProvider>
  );
}

export default App;