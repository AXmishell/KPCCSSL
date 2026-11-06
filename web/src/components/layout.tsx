import * as React from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Activity,
  BookOpenCheck,
  ClipboardList,
  FileCheck2,
  GitBranch,
  Globe2,
  KeyRound,
  LayoutDashboard,
  Menu,
  Moon,
  Server,
  Settings,
  ShieldCheck,
  Sun,
  UserRound,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { readStoredTheme, useTheme, type Theme } from "@/lib/theme";
import { useInfo } from "@/api/queries";
import { useAuth } from "@/auth";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";

const navItems = [
  { to: "/", key: "dashboard", icon: LayoutDashboard, end: true },
  { to: "/certificates", key: "certificates", icon: FileCheck2 },
  { to: "/jobs", key: "jobs", icon: ClipboardList },
  { to: "/ca-accounts", key: "ca", icon: Server },
  { to: "/dns-credentials", key: "dns", icon: Globe2 },
  { to: "/deployments", key: "deployments", icon: Activity },
  { to: "/deployment-credentials", key: "deploymentCredentials", icon: KeyRound },
  { to: "/workflows", key: "workflows", icon: GitBranch },
  { to: "/notifications", key: "notifications", icon: BookOpenCheck, admin: true },
  { to: "/audit", key: "audit", icon: ShieldCheck, admin: true },
  { to: "/settings", key: "settings", icon: Settings, admin: true },
];

export function AppLayout() {
  const { t, i18n } = useTranslation();
  const { user, logout } = useAuth();
  const { theme, setTheme } = useTheme();
  const { data: info } = useInfo();
  const [mobileOpen, setMobileOpen] = React.useState(false);
  const [confirmLogout, setConfirmLogout] = React.useState(false);
  const location = useLocation();
  const instanceName = info?.instance_name || "KPCCSSL";

  React.useEffect(() => setMobileOpen(false), [location.pathname]);

  // Apply the administrator's default only when this browser has no explicit
  // preference yet.
  React.useEffect(() => {
    if (!info?.default_theme || readStoredTheme()) return;
    if (info.default_theme === "light" || info.default_theme === "dark") {
      setTheme(info.default_theme as Theme);
    }
  }, [info?.default_theme, setTheme]);

  const toggleLang = () => {
    const next = i18n.language.startsWith("zh") ? "en" : "zh-CN";
    void i18n.changeLanguage(next);
    localStorage.setItem("kpcc_lang", next);
  };

  const items = navItems.filter((item) => !item.admin || user?.role === "admin");

  const sidebar = (
    <div className="flex h-full flex-col">
      <div className="flex h-16 items-center gap-2 border-b border-border px-5">
        <ShieldCheck className="h-6 w-6 text-primary" />
        <div>
          <div className="text-sm font-semibold leading-tight">{instanceName}</div>
          <div className="text-[11px] text-muted-foreground">{t("tagline")}</div>
        </div>
      </div>
      <nav className="flex-1 space-y-1 overflow-y-auto p-3 scrollbar-thin">
        {items.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "bg-primary/10 text-primary"
                  : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
              )
            }
          >
            <item.icon className="h-4 w-4" />
            {t(`nav.${item.key}`)}
          </NavLink>
        ))}
      </nav>
      <div className="border-t border-border p-3">
        <NavLink
          to="/profile"
          className={({ isActive }) =>
            cn(
              "flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
              isActive ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )
          }
        >
          <UserRound className="h-4 w-4" />
          <span className="truncate">{user?.email}</span>
        </NavLink>
        <div className="mt-1 flex items-center justify-between px-3 pb-1">
          <Badge variant="muted">{user?.role}</Badge>
          <Button variant="link" size="sm" className="h-auto p-0 text-xs" onClick={() => setConfirmLogout(true)}>
            {t("common.logout")}
          </Button>
        </div>
      </div>
    </div>
  );

  return (
    <div className="flex min-h-full">
      <aside className="hidden w-64 shrink-0 border-r border-border bg-card/40 lg:block">{sidebar}</aside>

      <Dialog open={mobileOpen} onOpenChange={setMobileOpen}>
        <DialogContent className="left-0 top-0 h-full max-w-[18rem] translate-x-0 translate-y-0 rounded-none border-r p-0">
          <DialogTitle className="sr-only">Navigation</DialogTitle>
          {sidebar}
        </DialogContent>
      </Dialog>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-16 items-center justify-between gap-2 border-b border-border bg-background/80 px-4 backdrop-blur lg:px-6">
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="icon" className="lg:hidden" onClick={() => setMobileOpen(true)}>
              {mobileOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
            </Button>
            <span className="text-sm text-muted-foreground">{t(`nav.${navItems.find((n) => n.to === location.pathname)?.key ?? "dashboard"}`, { defaultValue: "" })}</span>
          </div>
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="sm" onClick={toggleLang} title={t("common.language")}>
              {i18n.language.startsWith("zh") ? "EN" : "中文"}
            </Button>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
              title={t("common.theme")}
            >
              {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
          </div>
        </header>
        <main className="flex-1 p-4 lg:p-8">
          <div className="mx-auto max-w-6xl animate-fade-in">
            <Outlet />
          </div>
        </main>
      </div>

      <Dialog open={confirmLogout} onOpenChange={setConfirmLogout}>
        <DialogContent>
          <DialogTitle>{t("common.logout")}</DialogTitle>
          <p className="text-sm text-muted-foreground">{t("common.confirm")}?</p>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setConfirmLogout(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              onClick={async () => {
                await logout();
                setConfirmLogout(false);
              }}
            >
              {t("common.logout")}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
