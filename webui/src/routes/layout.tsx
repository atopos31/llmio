import { useEffect, useState } from "react";
import { Link, Outlet, useNavigate, useLocation } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  FaHome,
  FaCloud,
  FaRobot,
  FaLink,
  FaFileAlt,
  FaSignOutAlt,
  FaChevronLeft,
  FaChevronRight,
  FaCog,
  FaKey
} from "react-icons/fa";
import { useTheme } from "@/components/theme-provider";
import { getVersion, checkLatestRelease, type GitHubRelease } from "@/lib/api";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

export default function Layout() {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [version, setVersion] = useState("dev");
  const [latestRelease, setLatestRelease] = useState<GitHubRelease | null>(null);
  const [showUpdateDialog, setShowUpdateDialog] = useState(false);
  const { theme, setTheme } = useTheme();
  const navigate = useNavigate();
  const location = useLocation(); // 用于高亮当前选中的菜单

  const toggleSidebar = () => {
    setSidebarOpen(!sidebarOpen);
  };

  useEffect(() => {
    let active = true;

    const fetchVersion = async () => {
      try {
        const value = await getVersion();
        if (active && value) {
          setVersion(value);
        }
      } catch {
        // Keep default version when API is unreachable or unauthorized.
      }
    };

    void fetchVersion();

    return () => {
      active = false;
    };
  }, []);

  // Check for updates when on home page
  useEffect(() => {
    if (location.pathname === '/') {
      const checkForUpdates = async () => {
        try {
          const release = await checkLatestRelease('atopos31', 'llmio');
          if (release && release.tag_name !== version) {
            setLatestRelease(release);
            setShowUpdateDialog(true);
          }
        } catch (error) {
          console.error('Failed to check for updates:', error);
        }
      };

      void checkForUpdates();
    }
  }, [location.pathname, version]);

  const handleLogout = () => {
    localStorage.removeItem("authToken");
    navigate("/login");
  };

  const navItems = [
    { to: "/", label: "首页", icon: <FaHome /> },
    { to: "/providers", label: "提供商管理", icon: <FaCloud /> },
    { to: "/models", label: "模型管理", icon: <FaRobot /> },
    { to: "/model-providers", label: "模型提供商关联", icon: <FaLink /> },
    { to: "/logs", label: "请求日志", icon: <FaFileAlt /> },
    { to: "/auth-keys", label: "API Key 管理", icon: <FaKey /> },
    { to: "/config", label: "系统配置", icon: <FaCog /> },
  ];

  // 侧边栏宽度常量，方便统一管理
  const WIDTH_EXPANDED = "min-w-48";
  const WIDTH_COLLAPSED = "min-w-14";

  return (
    <div className="flex flex-col h-screen w-full dark:bg-gray-900 transition-colors duration-300">
      
      {/* 1. 顶部栏 Header */}
      <header className="border-b bg-background flex items-center justify-between p-3 flex-shrink-0 shadow-sm z-20">
        <div className="font-bold text-xl flex items-center gap-2">
          <span className="text-primary text-2xl">LLMIO</span> 
        </div>

        <div className="flex items-center gap-2">
          <Badge
            variant="outline"
            className="text-muted-foreground cursor-pointer hover:bg-accent transition-colors"
            onClick={() => latestRelease && setShowUpdateDialog(true)}
            title={latestRelease ? `有新版本 ${latestRelease.tag_name} 可用` : '当前版本'}
          >
            {version}
            {latestRelease && (
              <span className="ml-1 text-xs text-red-500">●</span>
            )}
          </Badge>
          <Button 
            variant="ghost" 
            size="icon"
            className="hover:bg-accent hover:text-accent-foreground" 
            onClick={() => setTheme(theme === "light" ? "dark" : "light")}
          >
            <svg 
              xmlns="http://www.w3.org/2000/svg" 
              width="24" height="24" viewBox="0 0 24 24" 
              fill="none" stroke="currentColor" strokeWidth="2" 
              strokeLinecap="round" strokeLinejoin="round" 
              className="size-5"
            >
              <path stroke="none" d="M0 0h24v24H0z" fill="none"></path>
              <path d="M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0 -18 0"></path>
              <path d="M12 3l0 18"></path>
              <path d="M12 9l4.65 -4.65"></path>
              <path d="M12 14.3l7.37 -7.37"></path>
              <path d="M12 19.6l8.85 -8.85"></path>
            </svg>
          </Button>
          
          <Button 
            variant="ghost" 
            onClick={handleLogout}
            className="gap-2"
          >
            <FaSignOutAlt />
          </Button>
        </div>
      </header>

      {/* 2. 下方主体区域 */}
      <div className="flex overflow-y-hidden flex-1 min-w-0">
        
        {/* 左侧侧边栏 Sidebar */}
        <aside 
          className={`
            flex flex-col border-r bg-background/95 transition-all duration-200 ease-in-out
            ${sidebarOpen ? WIDTH_EXPANDED : WIDTH_COLLAPSED}
          `}
        >
          <nav className="flex-1 overflow-y-auto py-4">
            <ul className="space-y-1">
              {navItems.map((item) => {
                const isActive = location.pathname === item.to;
                return (
                  <li key={item.to}>
                    <Link to={item.to}>
                      <div 
                        className={`
                          group flex items-center h-10 mx-2 rounded-md transition-colors relative overflow-hidden whitespace-nowrap
                          ${isActive 
                            ? "bg-primary text-primary-foreground shadow-sm" // 选中状态
                            : "hover:bg-accent hover:text-accent-foreground text-muted-foreground" // 默认状态
                          }
                        `}
                        title={!sidebarOpen ? item.label : ""}
                      >
                        {/* 
                          关键点：图标容器
                          永远固定为 w-12 (48px) 或 w-16 (相当于收起时的宽度)，
                          并且 flex-shrink-0 防止被挤压。
                          这样无论侧边栏多宽，图标相对于左侧的位置永远不变。
                        */}
                        <div className={`
                           flex items-center justify-center flex-shrink-0 h-full
                           ${sidebarOpen ? "w-10" : "w-full"} 
                           transition-all duration-300
                        `}>
                          <span className="text-lg">{item.icon}</span>
                        </div>
                        
                        {/* 
                           关键点：文字容器
                           通过 width, opacity, translate 组合实现平滑过渡
                        */}
                        <span 
                          className={`
                            font-medium transition-all duration-300 ease-in-out origin-left
                            ${sidebarOpen 
                              ? "w-auto opacity-100 translate-x-0 ml-2" 
                              : "w-0 opacity-0 -translate-x-4 ml-0"
                            }
                          `}
                        >
                          {item.label}
                        </span>
                      </div>
                    </Link>
                  </li>
                );
              })}
            </ul>
          </nav>

          {/* 底部切换按钮 */}
          <div className="p-2 mt-auto">
            <Button
              variant="ghost"
              onClick={toggleSidebar}
              className={`
                w-full h-12 flex items-center p-0 hover:bg-accent transition-all duration-300
              `}
            >
              {/* 同样的逻辑：图标容器固定宽度 */}
              <div className={`
                 flex items-center justify-center flex-shrink-0 h-full
                 ${sidebarOpen ? "w-12" : "w-full"}
                 transition-all duration-300
              `}>
                 {sidebarOpen ? <FaChevronLeft /> : <FaChevronRight />}
              </div>
              
              <span className={`
                whitespace-nowrap transition-all duration-300 ease-in-out overflow-hidden
                ${sidebarOpen 
                  ? "w-auto opacity-100 translate-x-0 ml-2" 
                  : "w-0 opacity-0 -translate-x-4 ml-0"
                }
              `}>
                收起菜单
              </span>
            </Button>
          </div>

        </aside>

        {/* 右侧主内容区域 */}
        <main className="flex-1 min-w-0 bg-muted/20 p-2 md:p-4 transition-all duration-300">
          <div className="mx-auto max-w-full h-full min-w-0 overflow-x-hidden">
             <Outlet />
          </div>
        </main>
      </div>

      {/* Update Dialog */}
      <Dialog open={showUpdateDialog} onOpenChange={setShowUpdateDialog}>
        <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>发现新版本 {latestRelease?.tag_name}</DialogTitle>
            <DialogDescription>
              当前版本: {version} → 最新版本: {latestRelease?.tag_name}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div>
              <h4 className="font-semibold mb-2">更新内容：</h4>
              <div className="bg-muted p-4 rounded-md text-sm whitespace-pre-wrap max-h-96 overflow-y-auto">
                {latestRelease?.body || '暂无更新说明'}
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                onClick={() => setShowUpdateDialog(false)}
              >
                稍后提醒
              </Button>
              <Button
                onClick={() => {
                  window.open(latestRelease?.html_url, '_blank');
                  setShowUpdateDialog(false);
                }}
              >
                查看详情
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
