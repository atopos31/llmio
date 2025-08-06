import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import Layout from './routes/layout';
import Home from './routes/home';
import ProvidersPage from './routes/providers';
import ModelsPage from './routes/models';
import ModelProvidersPage from './routes/model-providers';
import LogsPage from './routes/logs';
import LoginPage from './routes/login';

import { ThemeProvider } from "@/components/theme-provider"

function App() {
  return (
    <ThemeProvider defaultTheme="dark" storageKey="vite-ui-theme">
      <Router>
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
      </Router>
    </ThemeProvider>
  );
}

export default App;