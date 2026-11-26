import type { Me } from "@cicord/client";
import { CicordProvider, cicordKeys, useCicord, useLogout, useMe } from "@cicord/react";
import { Loader2, LogOut, Moon, Sun } from "lucide-react";
import { toast } from "sonner";
import { Fragment, useCallback, useEffect, useMemo, type ReactNode } from "react";
import { usePlugin, useStatus, useStoreDetail } from "./api.ts";
import { EmptyState, Sha } from "./components/common.tsx";
import { ErrorState } from "./components/errors.tsx";
import { PauseBanner, PauseProvider } from "./components/pause.tsx";
import { CicordApiError } from "@cicord/client";
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
import { Skeleton } from "./components/ui/skeleton.tsx";
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
import { AnalyticsPage } from "./pages/Analytics.tsx";
import { AuditPage } from "./pages/Audit.tsx";
import { LoginPage } from "./pages/Login.tsx";
import { OverviewPage } from "./pages/Overview.tsx";
import { PluginInstancePage } from "./pages/PluginInstance.tsx";
import { PluginsPage } from "./pages/Plugins.tsx";
import { ResourcesPage } from "./pages/Resources.tsx";
import { ServerSettingsPage } from "./pages/ServerSettings.tsx";
import { SettingsPage } from "./pages/Settings.tsx";
import { StorePage, StorePluginPage } from "./pages/Store.tsx";
import { StoreUpdatesPage } from "./pages/StoreUpdates.tsx";
import { DriftPage, HistoryPage, PlanPage } from "./pages/Sync.tsx";
import { DiagnosticsPage } from "./pages/Diagnostics.tsx";
import { SetupPage } from "./setup/SetupPage.tsx";
import { Link, navigate, useLocation } from "./router.tsx";
import { AppSidebar } from "./shell/AppSidebar.tsx";
import { useAdminLive, type AdminLive } from "./shell/live.ts";
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
      <CicordProvider client={config.client}>
        <ThemeProvider>
          <TooltipProvider delayDuration={300}>
            <Root />
            <ThemedToaster />
          </TooltipProvider>
        </ThemeProvider>
      </CicordProvider>
    </ConfigContext.Provider>
  );
}

function ThemedToaster() {
  const { theme } = useTheme();
  return <Toaster theme={theme} />;
}

function Root() {
  const { t } = useTranslation();
  const { cache } = useCicord();
  const me = useMe();
  const { path } = useLocation();

  useEffect(() => {
    const onExpired = () => void cache.invalidate(cicordKeys.me());
    window.addEventListener(UNAUTHENTICATED_EVENT, onExpired);
    return () => window.removeEventListener(UNAUTHENTICATED_EVENT, onExpired);
  }, [cache]);

  // The setup wizard is a full page outside the shell (like Login); a runtime in setup
  // mode answers every other API call with setup_required.
  if (
    path === "/setup" ||
    (me.error instanceof CicordApiError && me.error.code === "setup_required")
  )
    return <SetupPage />;
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

const SERVER_KEY = "cicord-admin:server";

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

  const mine = serverKey ? me.servers.find((s) => s.key === serverKey) : undefined;
  const role = mine?.role;
  const loading = status.isLoading && status.data === undefined;
  return useMemo(() => {
    const server = status.data?.servers.find((s) => s.key === serverKey);
    return {
      status: status.data,
      loading,
      serverKey,
      server,
      serverName: server?.name ?? mine?.name,
      role,
      isAdmin: role === "admin",
      simulator: status.data?.simulator ?? false,
      selectServer,
    };
  }, [status.data, loading, serverKey, mine?.name, role, selectServer]);
}

// ───────────────────────────────────────────────────────────── shell

function Shell() {
  const me = useViewer();
  const { path } = useLocation();
  const route = useMemo(() => parseRoute(path), [path]);
  const ws = useWorkspaceState(route);
  const [paletteOpen, setPaletteOpen] = useCommandPalette();
  const staff = me.servers.filter((s) => s.role === "admin" || s.role === "moderator");
  const config = useAdminConfig();
  const live = useAdminLive(!config.mock && staff.length > 0);
  useUpdateNotice(live.updatedTo);

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
    case "analytics":
      page = <AnalyticsPage serverKey={key} key={key} />;
      break;
    case "resources":
      page = <ResourcesPage serverKey={key} key={key} />;
      break;
    case "serverSettings":
      page = <ServerSettingsPage serverKey={key} key={key} />;
      break;
    case "plugins":
      page = route.instance ? (
        <PluginInstancePage
          serverKey={key}
          bot={route.instance.bot}
          instance={route.instance.instance}
          key={`${key}/${route.instance.bot}/${route.instance.instance}`}
        />
      ) : (
        <PluginsPage serverKey={key} key={key} />
      );
      break;
    case "store":
      page = route.storeUpdates ? (
        <StoreUpdatesPage serverKey={key} key={`${key}/updates`} />
      ) : route.storePlugin ? (
        <StorePluginPage
          serverKey={key}
          source={route.storePlugin.source}
          plugin={route.storePlugin.plugin}
          key={`${key}/${route.storePlugin.source}/${route.storePlugin.plugin}`}
        />
      ) : (
        <StorePage serverKey={key} key={key} />
      );
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
    case "diagnostics":
      page = <DiagnosticsPage serverKey={key} key={key} />;
      break;
    default:
      page = <NotFound path={path} />;
  }

  // Every page of a paused server shows who paused enforcement, when and why.
  const serverPage = navItem(route.page)?.segment !== undefined;
  return (
    <WorkspaceContext.Provider value={ws}>
      <PauseProvider>
        <SidebarProvider>
          <AppSidebar page={route.page} />
          <SidebarInset>
            <Header route={route} live={live} onSearch={() => setPaletteOpen(true)} />
            <main id="main" className="flex-1">
              {serverPage ? <PauseBanner /> : null}
              {page}
            </main>
          </SidebarInset>
          <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
        </SidebarProvider>
      </PauseProvider>
    </WorkspaceContext.Provider>
  );
}

/** Label of a detail page's last crumb (the plugin's display name, the raw id meanwhile). */
function useDetailLabel(route: Route): string | undefined {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const key = route.serverKey ?? ws.serverKey ?? "";
  const instance = usePlugin(key, route.instance?.bot, route.instance?.instance);
  const store = useStoreDetail(
    ws.isAdmin ? route.storePlugin?.source : undefined,
    ws.isAdmin ? route.storePlugin?.plugin : undefined,
  );
  if (route.instance) {
    const p = instance.data?.plugin;
    return p?.displayName || p?.name || route.instance.instance;
  }
  if (route.storePlugin) {
    const d = store.data;
    return d?.displayName || d?.name || route.storePlugin.plugin;
  }
  if (route.storeUpdates) return t("store.updates.crumb");
  return undefined;
}

/** Tells once that the runtime was updated, with a button that loads the new console. */
function useUpdateNotice(version: string | undefined) {
  const { t } = useTranslation();
  useEffect(() => {
    if (!version) return;
    toast(t("live.updated", { version }), {
      id: "cicord-update",
      description: t("live.updatedHint"),
      duration: Number.POSITIVE_INFINITY,
      action: { label: t("live.reload"), onClick: () => window.location.reload() },
    });
  }, [version, t]);
}

function Header({
  route,
  live,
  onSearch,
}: {
  route: Route;
  live: AdminLive;
  onSearch: () => void;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const config = useAdminConfig();
  const { theme, toggle } = useTheme();
  const page = route.page;
  const item = navItem(page);
  const detail = useDetailLabel(route);
  const crumbs: { label: string; to?: string; loading?: boolean }[] = [];
  // The server crumb: its name as soon as it is known, a placeholder while the status loads.
  const crumbKey = ws.server || ws.serverName ? ws.serverKey : undefined;
  if (item?.segment !== undefined && crumbKey && ws.serverName)
    crumbs.push({ label: ws.serverName, to: `/servers/${crumbKey}` });
  else if (item?.segment !== undefined && ws.serverKey && ws.loading)
    crumbs.push({ label: t("switcher.loading"), loading: true });
  if (item?.section && detail === undefined)
    crumbs.push({ label: t(`nav.sections.${item.section}`) });
  if (item && page !== "home" && page !== "notFound")
    crumbs.push({
      label: t(`nav.pages.${item.id as Exclude<PageId, "home" | "notFound">}`),
      ...(detail !== undefined && crumbKey ? { to: pathFor(item, crumbKey) } : {}),
    });
  if (detail !== undefined) crumbs.push({ label: detail });
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
                {c.loading ? (
                  <span role="status" className="inline-flex items-center">
                    <Skeleton className="h-3.5 w-24" aria-hidden="true" />
                    <span className="sr-only">{c.label}</span>
                  </span>
                ) : i === crumbs.length - 1 ? (
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
      {live.connection === "reconnecting" ? (
        <Tip content={t("live.reconnectingHint")}>
          <span tabIndex={0} role="status" className="inline-flex">
            <Badge tone="warning">
              <Loader2 className="animate-spin" aria-hidden="true" />
              {t("live.reconnecting")}
            </Badge>
          </span>
        </Tip>
      ) : null}
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
