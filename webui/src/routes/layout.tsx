import { useState } from "react";
import { Link, Outlet } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { FaBars, FaTimes, FaHome, FaCloud, FaRobot, FaLink, FaCogs, FaFileAlt } from "react-icons/fa";
import { useTheme } from "@/components/theme-provider";

export default function Layout() {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const { theme, setTheme } = useTheme()

  const toggleSidebar = () => {
    setSidebarOpen(!sidebarOpen);
  };

  const navItems = [
    { to: "/", label: "首页", icon: <FaHome /> },
    { to: "/providers", label: "提供商管理", icon: <FaCloud /> },
    { to: "/models", label: "模型管理", icon: <FaRobot /> },
    { to: "/model-providers", label: "模型提供商关联", icon: <FaLink /> },
    { to: "/logs", label: "请求日志", icon: <FaFileAlt /> },
  ];

  return (
    <div className="flex min-h-screen dark:bg-gray-900">
      {/* 侧边栏 - 固定定位 */}
      <div 
        className={`fixed h-full shadow-md transition-all duration-300 ${
          sidebarOpen ? "w-45" : "w-15"
        }`}
      >
        <div className="flex items-center justify-between p-4 border-b">
          {sidebarOpen && (
            <h1 className="text-xl font-bold">LLMIO</h1>
          )}
          <Button variant="ghost" size="icon" onClick={toggleSidebar}>
            {sidebarOpen ? <FaTimes /> : <FaBars />}
          </Button>
        </div>
        
        <nav className="mt-5">
          <ul>
            {navItems.map((item) => (
              <li key={item.to}>
                <Link to={item.to}>
                  <div className={`flex items-center p-4 ${sidebarOpen ? "" : "justify-center"}`}>
                    <span className="text-lg">{item.icon}</span>
                    {sidebarOpen && <span className="ml-2">{item.label}</span>}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        </nav>
      </div>

      {/* 主内容区域 */}
      <div 
        className="flex-1 flex flex-col"
        style={{ marginLeft: sidebarOpen ? "10rem" : "3rem" }}
      >
        {/* 顶部栏 - 固定定位 */}
        <header className="fixed top-0 right-0 shadow-sm bg-background z-10"
                style={{ left: sidebarOpen ? "10rem" : "3rem" }}>
          <div className="mx-auto max-w-7xl px-4 py-4 sm:px-6 lg:px-8 flex justify-between">
            <h1 className="text-2xl font-bold tracking-tight">管理面板</h1>
            <Button 
              variant="ghost" 
              className="hover:bg-accent hover:text-accent-foreground dark:hover:bg-accent/50" 
              onClick={() => setTheme(theme === "light" ? "dark" : "light")}
            >
              <svg 
                xmlns="http://www.w3.org/2000/svg" 
                width="24" 
                height="24" 
                viewBox="0 0 24 24" 
                fill="none" 
                stroke="currentColor" 
                strokeWidth="2" 
                strokeLinecap="round" 
                strokeLinejoin="round" 
                className="size-4.5"
              >
                <path stroke="none" d="M0 0h24v24H0z" fill="none"></path>
                <path d="M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0 -18 0"></path>
                <path d="M12 3l0 18"></path>
                <path d="M12 9l4.65 -4.65"></path>
                <path d="M12 14.3l7.37 -7.37"></path>
                <path d="M12 19.6l8.85 -8.85"></path>
              </svg>
            </Button>
          </div>
        </header>
        
        {/* 主要内容区域 - 添加顶部边距以避免被顶部栏遮挡 */}
        <main className="flex-1 overflow-x-hidden overflow-y-auto mt-16 ml-2 mr-2">
          <div className="mx-auto max-w-7xl py-6 sm:px-6 lg:px-8">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}