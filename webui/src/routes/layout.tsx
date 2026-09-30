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
  FaBatteryThreeQuarters,
  FaBars,
  FaTimes,
} from "react-icons/fa"
import { toast } from "sonner"
import { ThemeToggle } from "@/components/theme-toggle"
import { getVersion, checkLatestRelease, type GitHubRelease } from "@/lib/api"
import { cn } from "@/lib/utils"
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
  | "nav.quota"
  | "nav.providers"
  | "nav.models"
  | "nav.auth_keys"
  | "nav.config"
  | "nav.quickstart"

type NavGroupLabelKey = "nav.group_observe" | "nav.group_config"

type NavItem = { to: string; labelKey: NavLabelKey; icon: React.ReactNode }

/** 窄屏断点。必须与样式里用的 `md:` 一致（Tailwind 的 md 是 min-width 768px）。 */
const MOBILE_QUERY = "(max-width: 767px)"

/**
 * 是否窄屏。
 *
 * 用 JS 而不是纯 CSS 类的原因：这个值决定的是**结构**而不是样式——
 * 窄屏下侧边栏是浮层抽屉（要遮罩、要 Esc 关闭、要能整体隐藏），
 * 宽屏下它是常驻的图标轨道（可折叠成 56px）。两者共用同一个 <aside>，
 * 但"是否显示文字标签"这类决定没法只靠类名表达（抽屉里必须显示标签，
 * 而折叠轨道里必须不显示）。
 *
 * 监听 change 事件而不是只在挂载时读一次：原先只在挂载时读，
 * 于是把窗口从宽拖到窄、或手机横竖屏切换后，外壳仍然是旧形态。
 */
function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(
    () => typeof window.matchMedia === "function" && window.matchMedia(MOBILE_QUERY).matches
  )

  useEffect(() => {
    if (typeof window.matchMedia !== "function") return
    const mql = window.matchMedia(MOBILE_QUERY)
    const onChange = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    mql.addEventListener("change", onChange)
    // 挂载与订阅之间可能已经变过，补读一次
    setIsMobile(mql.matches)
    return () => mql.removeEventListener("change", onChange)
  }, [])

  return isMobile
}

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
      // 余量归"观察"组而不是"配置"：它回答"现在还剩多少"，
      // 是看的东西。加/改数据源虽然也在这一页，但那是次要动作。
      { to: "/quota", labelKey: "nav.quota", icon: <FaBatteryThreeQuarters /> },
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

// 侧边栏宽度常量。带 `md:` 前缀：窄屏下侧边栏是固定宽度的抽屉（WIDTH_DRAWER），
// 这两个只描述宽屏上那条图标轨道的两种形态。
const WIDTH_EXPANDED = "md:min-w-48"
const WIDTH_COLLAPSED = "md:min-w-14"

/** 窄屏抽屉宽度。固定值而非百分比：抽屉是全高的浮层，读起来才像浮层。
 *  另加 85vw 上限——280px 这类极窄外屏上，256px 会盖住几乎整个界面。 */
const WIDTH_DRAWER = "w-64 max-w-[85vw]"

export default function Layout() {
  const { t, i18n } = useTranslation("layout")
  const isMobile = useIsMobile()
  // 宽屏：图标轨道是否展开。默认展开——收起态会把分组标题一起藏掉，
  // 而分组是为了让人一眼看出"我在看还是在改"，只剩图标时这层信息就没了。
  const [sidebarOpen, setSidebarOpen] = useState(true)
  // 窄屏：抽屉是否打开。默认关闭，因为它是浮层。
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [version, setVersion] = useState("dev")
  const [latestRelease, setLatestRelease] = useState<GitHubRelease | null>(null)
  const [showUpdateDialog, setShowUpdateDialog] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()

  // 抽屉里必须显示文字标签——图标轨道在浮层里没有意义，用户是主动打开来找东西的。
  const labelsVisible = isMobile || sidebarOpen

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

  // 切到宽屏后抽屉不该继续"开着"——否则从窄屏拖宽再拖回来，
  // 抽屉会带着一个用户没主动做的状态重新出现。
  useEffect(() => {
    if (!isMobile) setDrawerOpen(false)
  }, [isMobile])

  // 点导航后自动收起抽屉：抽屉盖住了内容，不收起就得再点一次遮罩。
  useEffect(() => {
    setDrawerOpen(false)
  }, [location.pathname])

  // Esc 关闭抽屉。遮罩点击不是键盘可达的，所以必须有这条。
  useEffect(() => {
    if (!drawerOpen) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setDrawerOpen(false)
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [drawerOpen])

  const handleLogout = () => {
    localStorage.removeItem("authToken")
    navigate("/login")
  }

  const handleSnoozeUpdate = () => {
    localStorage.setItem("updateReminderSnoozeUntil", (Date.now() + 24 * 60 * 60 * 1000).toString())
    setShowUpdateDialog(false)
  }

  const activePath = activeNavPath(location.pathname)

  /**
   * 版本角标与语言选择器**只写一份、挂两处**：宽屏在头部，窄屏在抽屉底部。
   *
   * 头部在 280px 宽的设备（如折叠屏外屏）上放不下六组控件，而这两个都是
   * 低频的"设置类"动作，挪进抽屉比把头部挤到溢出更合适。用同一个元素描述符
   * 渲染两处是合法的——它只是描述，不是实例。
   */
  const versionChip = (
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
  )

  const langSelect = (
    <Select value={i18n.language} onValueChange={(v) => handleLanguageChange(v as SupportedLanguage)}>
      <SelectTrigger className="!h-7 w-[84px] px-2 text-xs" aria-label={t("header.language_label")}>
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
  )

  return (
    <div className="flex h-dvh w-full flex-col overflow-hidden bg-background">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:left-3 focus:top-3 focus:z-50 focus:rounded-md focus:bg-primary focus:px-3 focus:py-2 focus:text-primary-foreground"
      >
        {t("header.skip_to_content")}
      </a>

      <header className="z-20 flex flex-shrink-0 items-center gap-1.5 border-b border-border bg-background px-2 py-2 sm:gap-2 md:px-3">
        <Button
          variant="ghost"
          size="icon"
          className="size-8 md:hidden"
          onClick={() => setDrawerOpen(true)}
          aria-label={t("sidebar.open")}
          aria-expanded={drawerOpen}
          aria-controls="app-sidebar"
        >
          <FaBars className="size-4" aria-hidden="true" />
        </Button>

        <Link to="/" className="font-bold tracking-tight text-primary">
          LLMIO
        </Link>

        <div className="ml-auto flex items-center gap-1.5 sm:gap-2">
          {/* 窄屏下这两项在抽屉底部，见下方 drawerPrefs */}
          <span className="hidden sm:inline-flex">{versionChip}</span>
          <span className="hidden sm:block">{langSelect}</span>
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

      {/* 外壳只负责"外面这一层"：中间区不滚动，滚动交给各页面自己的内容区。
          若这里也留一个滚动条，右边缘就会出现两条并排的滚动条。 */}
      <div className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden">
        {/* 遮罩。抽屉开着时盖住包括头部在内的整个界面——它是模态的。
            点击关闭，但遮罩本身不可聚焦（键盘有 Esc 与抽屉里的关闭按钮）。 */}
        {drawerOpen && (
          <div
            className="fixed inset-0 z-30 bg-black/50 md:hidden"
            onClick={() => setDrawerOpen(false)}
            aria-hidden="true"
          />
        )}

        <aside
          id="app-sidebar"
          className={cn(
            "flex flex-col border-r border-border bg-sidebar transition-all duration-200 ease-in-out",
            // 窄屏：浮层抽屉。默认移出左侧，并且用 visibility 而不是只靠位移——
            // 仅移出屏幕的元素仍留在 tab 顺序里，键盘用户会 tab 进一个看不见的菜单。
            "fixed inset-y-0 left-0 z-40",
            WIDTH_DRAWER,
            drawerOpen ? "translate-x-0" : "invisible -translate-x-full",
            // 宽屏：回到普通流，位置、层级、可见性全部复位
            "md:visible md:static md:z-auto md:w-auto md:translate-x-0",
            sidebarOpen ? WIDTH_EXPANDED : WIDTH_COLLAPSED
          )}
        >
          {/* 抽屉标题栏。窄屏才有——宽屏的侧边栏没有"关闭"这个概念。 */}
          <div className="flex items-center justify-between gap-2 px-3 pt-2 md:hidden">
            <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              {t("nav.primary_label")}
            </span>
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              onClick={() => setDrawerOpen(false)}
              aria-label={t("sidebar.close")}
            >
              <FaTimes className="size-4" aria-hidden="true" />
            </Button>
          </div>

          <nav className="min-h-0 flex-1 overflow-y-auto py-3" aria-label={t("nav.primary_label")}>
            {NAV_GROUPS.map((group) => (
              <div key={group.labelKey} className="mb-3 last:mb-0">
                {/* 分组标题在收起态下隐藏；收起时靠图标 + title 提示辨识 */}
                <div
                  className={`mb-1 overflow-hidden px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground transition-all duration-200 ${
                    labelsVisible ? "h-4 opacity-100" : "h-0 opacity-0"
                  }`}
                  aria-hidden={!labelsVisible}
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
                          title={!labelsVisible ? label : undefined}
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
                              labelsVisible ? "w-9" : "w-full"
                            }`}
                          >
                            <span className="text-sm" aria-hidden="true">
                              {item.icon}
                            </span>
                          </div>
                          <span
                            className={`origin-left font-medium transition-all duration-200 ease-in-out ${
                              labelsVisible
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

          <div className="mt-auto flex flex-col gap-2 p-2">
            {/* 窄屏的"设置类"控件：头部放不下时挪到这里，见上方 versionChip 的说明 */}
            <div className="flex items-center justify-between gap-2 border-t border-border pt-2 sm:hidden">
              {versionChip}
              {langSelect}
            </div>

            <Button
              variant="ghost"
              onClick={() => setSidebarOpen(!sidebarOpen)}
              aria-expanded={sidebarOpen}
              className="hidden h-10 w-full items-center p-0 md:flex"
              title={t("sidebar.collapse")}
            >
              <div
                className={`flex h-full flex-shrink-0 items-center justify-center ${
                  labelsVisible ? "w-9" : "w-full"
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

        {/* main 不再自己滚动：各页面已经有一个内容滚动区，
            两层同向滚动容器叠在一起就是"两个滚动条"的来源。 */}
        <main
          id="main-content"
          tabIndex={-1}
          className="min-h-0 min-w-0 flex-1 overflow-hidden bg-muted/20 p-2 md:p-4"
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
