import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'

// Check if user is authenticated
const isAuthenticated = () => {
  return !!localStorage.getItem('authToken');
};

// Redirect to login if not authenticated (except for login page)
const ProtectedApp = () => {
  const path = window.location.pathname;

  // Allow access to login page without authentication
  if (path === '/login') {
    return <App />;
  }

  // Redirect to login if not authenticated
  if (!isAuthenticated()) {
    window.location.href = '/login';
    return null;
  }

  return <App />;
};

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ProtectedApp />
  </StrictMode>,
)