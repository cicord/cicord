import type { Me } from "@codi/client";
import { CodiProvider, codiKeys, useCodi, useLogout, useMe } from "@codi/react";
import { Loader2, LogOut, Moon, Sun } from "lucide-react";
import { Fragment, useCallback, useEffect, useMemo, type ReactNode } from "react";
import { useStatus } from "./api.ts";
import { EmptyState, Sha } from "./components/common.tsx";
import { ErrorState } from "./components/errors.tsx";
import { Badge } from "./components/ui/badge.tsx";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "./components/ui/breadcrumb.tsx";
import { Button } from "./components/ui/button.tsx";
import { Separator } from "./components/ui/separator.tsx";
import { SidebarInset, SidebarProvider, SidebarTrigger } from "./components/ui/sidebar.tsx";
import { Toaster } from "./components/ui/sonner.tsx";
import { Tip, TooltipProvider } from "./components/ui/tooltip.tsx";
import { UNAUTHENTICATED_EVENT, type AdminConfig } from "./config.ts";
import {
  BrandMark,
  ConfigContext,
  MeContext,
  useAdminConfig,
  useViewer,
  useWorkspace,
  WorkspaceContext,
  type Workspace,
} from "./context.tsx";
import { useTranslation } from "./i18n/index.ts";
import { AuditPage } from "./pages/Audit.tsx";
import { LoginPage } from "./pages/Login.tsx";
import { OverviewPage } from "./pages/Overview.tsx";
import { PluginsPage } from "./pages/Plugins.tsx";
import { ResourcesPage } from "./pages/Resources.tsx";
import { ServerSettingsPage } from "./pages/ServerSettings.tsx";
import { SettingsPage } from "./pages/Settings.tsx";
import { DriftPage, HistoryPage, PlanPage } from "./pages/Sync.tsx";
import { Link, navigate, useLocation } from "./router.tsx";
import { AppSidebar } from "./shell/AppSidebar.tsx";
import {
  CommandPalette,
  CommandPaletteButton,
  useCommandPalette,
} from "./shell/CommandPalette.tsx";
import { navItem, parseRoute, pathFor, type PageId, type Route } from "./shell/nav.ts";
import { ThemeProvider, useTheme } from "./shell/theme.tsx";
import { SimulatorPage } from "./sim/Simulator.tsx";

export function App({ config }: { config: AdminConfig }) {
  return (
    <ConfigContext.Provider value={config}>
      <CodiProvider client={config.client}>
        <ThemeProvider>
          <TooltipProvider delayDuration={300}>
            <Root />
            <ThemedToaster />
          </TooltipProvider>
        </ThemeProvider>
      </CodiProvider>
    </ConfigContext.Provider>
  );
}

function ThemedToaster() {
  const { theme } = useTheme();
  return <Toaster theme={theme} />;
}

function Root() {
  const { t } = useTranslation();
  const { cache } = useCodi();
  const me = useMe();

  useEffect(() => {
    const onExpired = () => void cache.invalidate(codiKeys.me());
    window.addEventListener(UNAUTHENTICATED_EVENT, onExpired);
    return () => window.removeEventListener(UNAUTHENTICATED_EVENT, onExpired);
  }, [cache]);

  if (me.isLoading)
    return (
      <div className="grid min-h-svh place-items-center">
        <Loader2
          className="size-5 animate-spin text-muted-foreground"
          aria-label={t("common.loading")}
          role="status"
        />
      </div>
    );
  if (me.status === "error")
    return (
      <div className="grid min-h-svh place-items-center p-4">
        <ErrorState
          className="w-full max-w-md"
          error={me.error}
          title={t("boot.unreachable")}
          onRetry={() => void me.refetch()}
        />
      </div>
    );
  if (!me.data) return <LoginPage />;
  return (
    <MeContext.Provider value={me.data}>
      <Shell />
    </MeContext.Provider>
  );
}

// ───────────────────────────────────────────────────────────── workspace

const SERVER_KEY = "codi-admin:server";

function rememberServer(key: string) {
  try {
    localStorage.setItem(SERVER_KEY, key);
  } catch {
    // ignore
  }
}

function rememberedServer(): string | null {
  try {
    return localStorage.getItem(SERVER_KEY);
  } catch {
    return null;
  }
}

function useWorkspaceState(route: Route): Workspace {
  const me = useViewer();
  const status = useStatus();
  const staff = me.servers.filter((s) => s.role === "admin" || s.role === "moderator");
  const known = status.data?.servers.map((s) => s.key) ?? staff.map((s) => s.key);
  const remembered = rememberedServer();
  const serverKey =
    route.serverKey ??
    (remembered && known.includes(remembered) ? remembered : undefined) ??
    known[0];

  useEffect(() => {
    if (route.serverKey) rememberServer(route.serverKey);
  }, [route.serverKey]);

  const selectServer = useCallback(
    (key: string) => {
      rememberServer(key);
      // Stay on the same page of the other server.
      const item = navItem(route.page);
      navigate(item && item.segment !== undefined ? pathFor(item, key) : `/servers/${key}`);
    },
    [route.page],
  );

  const role = serverKey ? me.servers.find((s) => s.key === serverKey)?.role : undefined;
  return useMemo(
    () => ({
      status: status.data,
      serverKey,
      server: status.data?.servers.find((s) => s.key === serverKey),
      role,
      isAdmin: role === "admin",
      simulator: status.data?.simulator ?? false,
      selectServer,
    }),
    [status.data, serverKey, role, selectServer],
  );
}

// ───────────────────────────────────────────────────────────── shell

function Shell() {
  const me = useViewer();
  const { path } = useLocation();
  const route = useMemo(() => parseRoute(path), [path]);
  const ws = useWorkspaceState(route);
  const [paletteOpen, setPaletteOpen] = useCommandPalette();
  const staff = me.servers.filter((s) => s.role === "admin" || s.role === "moderator");

  // "/" (and "/login" after signing in) lands on the current server's overview.
  useEffect(() => {
    if (route.page === "home" && ws.serverKey)
      navigate(`/servers/${ws.serverKey}`, { replace: true });
  }, [route.page, ws.serverKey]);

  if (staff.length === 0) return <NoAccess me={me} />;

  const key = route.serverKey ?? ws.serverKey ?? "";
  let page: ReactNode;
  switch (route.page) {
    case "home":
    case "overview":
      page = key ? <OverviewPage serverKey={key} key={key} /> : null;
      break;
    case "resources":
      page = <ResourcesPage serverKey={key} key={key} />;
      break;
    case "serverSettings":
      page = <ServerSettingsPage serverKey={key} key={key} />;
      break;
    case "plugins":
      page = <PluginsPage serverKey={key} key={key} />;
      break;
    case "plan":
      page = <PlanPage serverKey={key} key={key} />;
      break;
    case "history":
      page = <HistoryPage serverKey={key} key={key} />;
      break;
    case "drift":
      page = <DriftPage serverKey={key} key={key} />;
      break;
    case "audit":
      page = <AuditPage serverKey={key} key={key} />;
      break;
    case "simulator":
      page = <SimulatorPage />;
      break;
    case "settings":
      page = <SettingsPage />;
      break;
    default:
      page = <NotFound path={path} />;
  }

  return (
    <WorkspaceContext.Provider value={ws}>
      <SidebarProvider>
        <AppSidebar page={route.page} />
        <SidebarInset>
          <Header page={route.page} onSearch={() => setPaletteOpen(true)} />
          <main id="main" className="flex-1">
            {page}
          </main>
        </SidebarInset>
        <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
      </SidebarProvider>
    </WorkspaceContext.Provider>
  );
}

function Header({ page, onSearch }: { page: PageId; onSearch: () => void }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const config = useAdminConfig();
  const { theme, toggle } = useTheme();
  const item = navItem(page);
  const crumbs: { label: string; to?: string }[] = [];
  if (item?.segment !== undefined && ws.server)
    crumbs.push({ label: ws.server.name, to: `/servers/${ws.server.key}` });
  if (item?.section) crumbs.push({ label: t(`nav.sections.${item.section}`) });
  if (item && page !== "home" && page !== "notFound")
    crumbs.push({ label: t(`nav.pages.${item.id as Exclude<PageId, "home" | "notFound">}`) });
  const rev = ws.status?.revision;
  return (
    <header className="sticky top-0 z-[5] flex h-(--header-h) shrink-0 items-center gap-2 border-b bg-background/85 px-3 backdrop-blur-md md:px-4">
      <SidebarTrigger label={t("nav.toggleSidebar")} className="-ml-1" />
      <Separator orientation="vertical" className="mx-1 !h-4" />
      <Breadcrumb aria-label={t("nav.breadcrumb")} className="min-w-0 flex-1">
        <BreadcrumbList>
          {crumbs.map((c, i) => (
            <Fragment key={`${c.label}-${i}`}>
              {i > 0 ? <BreadcrumbSeparator className="hidden sm:block" /> : null}
              <BreadcrumbItem
                className={i < crumbs.length - 1 ? "hidden sm:inline-flex" : undefined}
              >
                {i === crumbs.length - 1 ? (
                  <BreadcrumbPage>{c.label}</BreadcrumbPage>
                ) : c.to ? (
                  <Link to={c.to} className="truncate hover:text-foreground">
                    {c.label}
                  </Link>
                ) : (
                  <span className="truncate">{c.label}</span>
                )}
              </BreadcrumbItem>
            </Fragment>
          ))}
        </BreadcrumbList>
      </Breadcrumb>
      {config.mock ? <Badge tone="violet">{t("app.mock")}</Badge> : null}
      {rev ? (
        <Tip content={t("header.revision", { message: rev.message.split("\n")[0] ?? "" })}>
          <span className="hidden items-center lg:inline-flex" tabIndex={0}>
            <Sha id={rev.id} icon />
          </span>
        </Tip>
      ) : null}
      <CommandPaletteButton onOpen={onSearch} />
      <Button
        variant="ghost"
        size="icon-sm"
        onClick={toggle}
        aria-label={theme === "dark" ? t("header.lightTheme") : t("header.darkTheme")}
      >
        {theme === "dark" ? <Sun /> : <Moon />}
      </Button>
    </header>
  );
}

function NotFound({ path }: { path: string }) {
  const { t } = useTranslation();
  return (
    <EmptyState
      className="py-24"
      title={t("notFound.title")}
      action={
        <Button asChild variant="outline" size="sm">
          <Link to="/">{t("notFound.back")}</Link>
        </Button>
      }
    >
      {t("notFound.text")} <code className="font-mono text-xs">{path}</code>
    </EmptyState>
  );
}

function NoAccess({ me }: { me: Me }) {
  const { t } = useTranslation();
  const logout = useLogout();
  return (
    <div className="grid min-h-svh place-items-center p-4">
      <div className="w-full max-w-md rounded-xl border bg-card p-6">
        <BrandMark size={28} />
        <h1 className="mt-4 text-lg font-semibold">{t("noAccess.title")}</h1>
        <p className="mt-2 text-[13px] text-muted-foreground">
          {t("noAccess.text", { name: me.user.displayName || me.user.username })}
        </p>
        <Button className="mt-5" variant="outline" onClick={() => void logout()}>
          <LogOut />
          {t("user.logout")}
        </Button>
      </div>
    </div>
  );
}
