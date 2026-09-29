import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { SUPPORTED_LANGUAGES, type SupportedLanguage } from "@/i18n/index"
import { Link, Outlet, useNavigate, useLocation } from "react-router-dom"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  FaHome,
  FaRocket,
  FaCloud,
  FaRobot,
  FaFileAlt,
  FaSignOutAlt,
  FaChevronLeft,
  FaChevronRight,
  FaCog,
  FaKey,
  FaExternalLinkAlt,
  FaColumns,
} from "react-icons/fa"
import { toast } from "sonner"
import { ThemeToggle } from "@/components/theme-toggle"
import { getVersion, checkLatestRelease, type GitHubRelease } from "@/lib/api"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

// 显式声明键的联合类型：i18n 的类型增强把 t() 的入参收窄为字面量联合，
// 若这里写成 string 会丢掉字面量信息而无法通过类型检查。
type NavLabelKey =
  | "nav.home"
  | "nav.logs"
  | "nav.compare"
  | "nav.providers"
  | "nav.models"
  | "nav.auth_keys"
  | "nav.config"
  | "nav.quickstart"

type NavGroupLabelKey = "nav.group_observe" | "nav.group_config"

type NavItem = { to: string; labelKey: NavLabelKey; icon: React.ReactNode }

/**
 * 导航按"观察 / 配置"分两组。
 *
 * 分组的依据是使用动机，不是页面相似度：观察类是"看"（现在正在发生什么），
 * 配置类是"改"（把系统调成想要的样子）。这两件事在同一组平铺时，
 * 找东西要靠逐个读标签；分开后，用户先确定自己在做什么，再找具体页面。
 */
const NAV_GROUPS: { labelKey: NavGroupLabelKey; items: NavItem[] }[] = [
  {
    labelKey: "nav.group_observe",
    items: [
      { to: "/", labelKey: "nav.home", icon: <FaHome /> },
      { to: "/logs", labelKey: "nav.logs", icon: <FaFileAlt /> },
      { to: "/compare", labelKey: "nav.compare", icon: <FaColumns /> },
    ],
  },
  {
    labelKey: "nav.group_config",
    items: [
      { to: "/providers", labelKey: "nav.providers", icon: <FaCloud /> },
      { to: "/models", labelKey: "nav.models", icon: <FaRobot /> },
      { to: "/auth-keys", labelKey: "nav.auth_keys", icon: <FaKey /> },
      { to: "/config", labelKey: "nav.config", icon: <FaCog /> },
      { to: "/quickstart", labelKey: "nav.quickstart", icon: <FaRocket /> },
    ],
  },
]

/**
 * 判断某个导航项是否为当前页。
 *
 * 用前缀匹配而非精确匹配：旧实现是 `pathname === to`，导致进入
 * `/logs/5/chat-io` 这类子页面时**没有任何导航项高亮**，用户失去位置感。
 * 根路径仍用精确匹配（否则它会匹配一切）。
 */
function isNavActive(pathname: string, to: string): boolean {
  if (to === "/") return pathname === "/"
  return pathname === to || pathname.startsWith(to + "/")
}

/** 在两组里找出最长匹配项，避免父路径与子路径同时高亮。 */
function activeNavPath(pathname: string): string | null {
  const all = NAV_GROUPS.flatMap((g) => g.items)
  const matches = all.filter((i) => isNavActive(pathname, i.to))
  if (matches.length === 0) return null
  return matches.reduce((a, b) => (b.to.length > a.to.length ? b : a)).to
}

// 侧边栏宽度常量
const WIDTH_EXPANDED = "min-w-48"
const WIDTH_COLLAPSED = "min-w-14"

/**
 * 桌面端默认展开侧边栏。
 *
 * 旧实现默认收起。加入「观察 / 配置」分组后，收起态会连分组标题一起隐藏，
 * 分组的价值就没了——分组是为了让人一眼看出"我在看还是在改"，
 * 收起时只剩图标，这个信息传递不出去。窄屏（抽屉式导航）仍默认收起。
 */
function initialSidebarOpen(): boolean {
  if (typeof window.matchMedia !== "function") return false
  return window.matchMedia("(min-width: 768px)").matches
}

export default function Layout() {
  const { t, i18n } = useTranslation("layout")
  const [sidebarOpen, setSidebarOpen] = useState(initialSidebarOpen)
  const [version, setVersion] = useState("dev")
  const [latestRelease, setLatestRelease] = useState<GitHubRelease | null>(null)
  const [showUpdateDialog, setShowUpdateDialog] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()

  const handleLanguageChange = (code: SupportedLanguage) => {
    void i18n.changeLanguage(code)
  }

  useEffect(() => {
    let active = true

    const fetchVersion = async () => {
      try {
        const value = await getVersion()
        if (active && value) setVersion(value)
      } catch {
        // 拿不到版本就用默认值，不应影响界面
      }
    }

    void fetchVersion()
    return () => {
      active = false
    }
  }, [])

  // 检查更新。仅在总览页，且提示改为**非模态**：
  // 这是常驻挂机的自托管工具，进来就被弹窗拦住很打扰；
  // 改成角标 + 一次轻提示，用户想看点角标即可。
  useEffect(() => {
    if (location.pathname !== "/") return

    const checkForUpdates = async () => {
      try {
        if (version === "dev") return
        const release = await checkLatestRelease("atopos31", "llmio")
        if (!release || release.tag_name === version) return

        setLatestRelease(release)
        const snoozeUntil = localStorage.getItem("updateReminderSnoozeUntil")
        if (!snoozeUntil || Date.now() > parseInt(snoozeUntil, 10)) {
          toast(t("header.new_version_hint", { version: release.tag_name }), {
            action: { label: t("update_dialog.view_detail"), onClick: () => setShowUpdateDialog(true) },
          })
        }
      } catch {
        // 离线或限流时静默：更新提示是附属功能
      }
    }

    void checkForUpdates()
  }, [location.pathname, version, t])

  const handleLogout = () => {
    localStorage.removeItem("authToken")
    navigate("/login")
  }

  const handleSnoozeUpdate = () => {
    localStorage.setItem("updateReminderSnoozeUntil", (Date.now() + 24 * 60 * 60 * 1000).toString())
    setShowUpdateDialog(false)
  }

  const activePath = activeNavPath(location.pathname)

  return (
    <div className="flex h-screen w-full flex-col bg-background">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:left-3 focus:top-3 focus:z-50 focus:rounded-md focus:bg-primary focus:px-3 focus:py-2 focus:text-primary-foreground"
      >
        {t("header.skip_to_content")}
      </a>

      <header className="z-20 flex flex-shrink-0 items-center justify-between border-b border-border bg-background px-3 py-2">
        <div className="flex items-center gap-2">
          <span className="font-bold tracking-tight text-primary">LLMIO</span>
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="h-6 gap-1 px-2 text-xs font-normal text-muted-foreground"
            onClick={() => {
              if (!latestRelease) return
              localStorage.removeItem("updateReminderSnoozeUntil")
              setShowUpdateDialog(true)
            }}
            title={
              latestRelease
                ? t("header.new_version_hint", { version: latestRelease.tag_name })
                : t("header.current_version")
            }
          >
            <span className="reading">{version}</span>
            {latestRelease && (
              <>
                {/* 用图标而非纯色点：颜色不能是唯一的信息载体 */}
                <FaExternalLinkAlt className="size-2.5 text-status-warning-ink" aria-hidden="true" />
                <span className="sr-only">
                  {t("header.new_version_hint", { version: latestRelease.tag_name })}
                </span>
              </>
            )}
          </Button>

          <Select
            value={i18n.language}
            onValueChange={(v) => handleLanguageChange(v as SupportedLanguage)}
          >
            <SelectTrigger
              className="!h-7 w-[84px] px-2 text-xs"
              aria-label={t("header.language_label")}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SUPPORTED_LANGUAGES.map((lang) => (
                <SelectItem key={lang.code} value={lang.code} className="text-xs">
                  {lang.code}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <ThemeToggle />

          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            onClick={handleLogout}
            title={t("header.logout")}
          >
            <FaSignOutAlt className="size-3.5" aria-hidden="true" />
            <span className="sr-only">{t("header.logout")}</span>
          </Button>
        </div>
      </header>

      <div className="flex min-w-0 flex-1 overflow-y-hidden">
        <aside
          className={`flex flex-col border-r border-border bg-sidebar transition-all duration-200 ease-in-out ${
            sidebarOpen ? WIDTH_EXPANDED : WIDTH_COLLAPSED
          }`}
        >
          <nav className="flex-1 overflow-y-auto py-3" aria-label={t("nav.primary_label")}>
            {NAV_GROUPS.map((group) => (
              <div key={group.labelKey} className="mb-3 last:mb-0">
                {/* 分组标题在收起态下隐藏；收起时靠图标 + title 提示辨识 */}
                <div
                  className={`mb-1 overflow-hidden px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground transition-all duration-200 ${
                    sidebarOpen ? "h-4 opacity-100" : "h-0 opacity-0"
                  }`}
                  aria-hidden={!sidebarOpen}
                >
                  {t(group.labelKey)}
                </div>
                <ul className="space-y-0.5">
                  {group.items.map((item) => {
                    const isActive = activePath === item.to
                    const label = t(item.labelKey)
                    return (
                      <li key={item.to}>
                        <Link
                          to={item.to}
                          title={!sidebarOpen ? label : undefined}
                          aria-current={isActive ? "page" : undefined}
                          className={`group mx-2 flex h-9 items-center overflow-hidden whitespace-nowrap rounded-md transition-colors ${
                            isActive
                              ? "bg-primary text-primary-foreground"
                              : "text-muted-foreground hover:bg-sidebar-accent hover:text-accent-foreground"
                          }`}
                        >
                          {/* 图标容器固定宽度，保证收起/展开时图标位置不跳动 */}
                          <div
                            className={`flex h-full flex-shrink-0 items-center justify-center ${
                              sidebarOpen ? "w-9" : "w-full"
                            }`}
                          >
                            <span className="text-sm" aria-hidden="true">
                              {item.icon}
                            </span>
                          </div>
                          <span
                            className={`origin-left font-medium transition-all duration-200 ease-in-out ${
                              sidebarOpen
                                ? "ml-1 w-auto translate-x-0 opacity-100"
                                : "ml-0 w-0 -translate-x-4 opacity-0"
                            }`}
                          >
                            {label}
                          </span>
                        </Link>
                      </li>
                    )
                  })}
                </ul>
              </div>
            ))}
          </nav>

          <div className="mt-auto p-2">
            <Button
              variant="ghost"
              onClick={() => setSidebarOpen(!sidebarOpen)}
              aria-expanded={sidebarOpen}
              className="flex h-10 w-full items-center p-0"
              title={t("sidebar.collapse")}
            >
              <div
                className={`flex h-full flex-shrink-0 items-center justify-center ${
                  sidebarOpen ? "w-9" : "w-full"
                }`}
              >
                {sidebarOpen ? (
                  <FaChevronLeft aria-hidden="true" />
                ) : (
                  <FaChevronRight aria-hidden="true" />
                )}
              </div>
              <span
                className={`overflow-hidden whitespace-nowrap transition-all duration-200 ${
                  sidebarOpen
                    ? "ml-1 w-auto translate-x-0 opacity-100"
                    : "ml-0 w-0 -translate-x-4 opacity-0"
                }`}
              >
                {t("sidebar.collapse")}
              </span>
              <span className="sr-only">{t("sidebar.collapse")}</span>
            </Button>
          </div>
        </aside>

        <main
          id="main-content"
          className="min-w-0 flex-1 overflow-y-auto bg-muted/20 p-2 md:p-4"
        >
          <div className="mx-auto h-full min-w-0 max-w-full">
            <Outlet />
          </div>
        </main>
      </div>

      <Dialog open={showUpdateDialog} onOpenChange={setShowUpdateDialog}>
        <DialogContent className="max-h-[80vh] max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {t("update_dialog.title", { version: latestRelease?.tag_name })}
            </DialogTitle>
            <DialogDescription>
              {t("update_dialog.current")}: {version} → {t("update_dialog.latest")}:{" "}
              {latestRelease?.tag_name}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div>
              <h4 className="mb-2 font-semibold">{t("update_dialog.changelog")}</h4>
              <div className="max-h-96 overflow-y-auto whitespace-pre-wrap rounded-md bg-muted p-4 text-sm">
                {latestRelease?.body || t("update_dialog.no_changelog")}
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={handleSnoozeUpdate}>
                {t("update_dialog.snooze")}
              </Button>
              <Button
                onClick={() => {
                  window.open(latestRelease?.html_url, "_blank")
                  setShowUpdateDialog(false)
                }}
              >
                {t("update_dialog.view_detail")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
