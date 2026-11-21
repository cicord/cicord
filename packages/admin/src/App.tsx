import type { Me } from "@codi/client";
import { CodiProvider, codiKeys, useCodi, useLogout, useMe } from "@codi/react";
import { useEffect, useState, type ReactNode } from "react";
import { useStatus } from "./api.ts";
import {
  BrandMark,
  ConfigContext,
  MeContext,
  RoleBadge,
  useAdminConfig,
  useViewer,
} from "./context.tsx";
import { UNAUTHENTICATED_EVENT, type AdminConfig } from "./config.ts";
import { LoginPage } from "./pages/Login.tsx";
import { OverviewPage } from "./pages/Overview.tsx";
import { PluginsPage } from "./pages/Plugins.tsx";
import { ServerPage } from "./pages/Server.tsx";
import { SimulatorPage } from "./sim/Simulator.tsx";
import { Link, match, navigate, useLocation } from "./router.tsx";
import {
  IconChevronDown,
  IconLogout,
  IconMoon,
  IconPlay,
  IconServer,
  IconSun,
} from "./ui/icons.tsx";
import {
  Avatar,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Menu,
  Sha,
  Spinner,
  ToastProvider,
} from "./ui/index.tsx";

export function App({ config }: { config: AdminConfig }) {
  return (
    <ConfigContext.Provider value={config}>
      <CodiProvider client={config.client}>
        <ToastProvider>
          <Root />
        </ToastProvider>
      </CodiProvider>
    </ConfigContext.Provider>
  );
}

function Root() {
  const { cache } = useCodi();
  const me = useMe();

  useEffect(() => {
    const onExpired = () => void cache.invalidate(codiKeys.me());
    window.addEventListener(UNAUTHENTICATED_EVENT, onExpired);
    return () => window.removeEventListener(UNAUTHENTICATED_EVENT, onExpired);
  }, [cache]);

  if (me.isLoading)
    return (
      <div className="boot">
        <Spinner size={20} label="Loading" />
      </div>
    );
  if (me.status === "error")
    return (
      <div className="boot">
        <ErrorState
          error={me.error}
          title="Cannot reach the runtime"
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

// ───────────────────────────────────────────────────────────── theme

type Theme = "dark" | "light";
const THEME_KEY = "codi-admin:theme";

export function initialTheme(): Theme {
  try {
    const saved = localStorage.getItem(THEME_KEY);
    if (saved === "light" || saved === "dark") return saved;
  } catch {
    // storage unavailable
  }
  return "dark";
}

function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(() => initialTheme());
  useEffect(() => {
    document.documentElement.dataset["theme"] = theme;
    try {
      localStorage.setItem(THEME_KEY, theme);
    } catch {
      // ignore
    }
  }, [theme]);
  return [theme, () => setTheme((t) => (t === "dark" ? "light" : "dark"))];
}

// ───────────────────────────────────────────────────────────── shell

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

function Shell() {
  const me = useViewer();
  const { path } = useLocation();
  const status = useStatus();
  const [theme, toggleTheme] = useTheme();
  const config = useAdminConfig();

  const staffServers = me.servers.filter((s) => s.role === "admin" || s.role === "moderator");
  const routeServer =
    match("/servers/:key", path)?.["key"] ?? match("/servers/:key/:tab", path)?.["key"];
  const known = status.data?.servers.map((s) => s.key) ?? staffServers.map((s) => s.key);
  const remembered = rememberedServer();
  const current =
    routeServer ??
    (remembered && known.includes(remembered) ? remembered : undefined) ??
    known[0] ??
    undefined;

  useEffect(() => {
    if (routeServer) rememberServer(routeServer);
  }, [routeServer]);

  useEffect(() => {
    if (path === "/login") navigate("/", { replace: true });
  }, [path]);

  if (staffServers.length === 0) {
    return (
      <div className="boot">
        <NoAccess me={me} />
      </div>
    );
  }

  let page: ReactNode;
  let params: Record<string, string> | null;
  if (path === "/" || path === "/login") page = <OverviewPage />;
  else if ((params = match("/servers/:key/plugins", path)))
    page = <PluginsPage serverKey={params["key"]!} key={params["key"]} />;
  else if ((params = match("/servers/:key", path)))
    page = <ServerPage serverKey={params["key"]!} tab="status" key={params["key"]} />;
  else if ((params = match("/servers/:key/:tab", path)))
    page = <ServerPage serverKey={params["key"]!} tab={params["tab"]!} key={params["key"]} />;
  else if (path === "/simulator") page = <SimulatorPage />;
  else
    page = (
      <EmptyState title="Page not found" action={<Link to="/">Back to overview</Link>}>
        Nothing lives at <code>{path}</code>.
      </EmptyState>
    );

  const section = path.startsWith("/simulator")
    ? "simulator"
    : path.endsWith("/plugins")
      ? "plugins"
      : path.startsWith("/servers/")
        ? "server"
        : "overview";
  const sim = status.data?.simulator ?? false;
  const servers = status.data?.servers ?? [];
  const currentName = servers.find((s) => s.key === current)?.name ?? current;

  return (
    <div className="shell">
      <header className="topbar">
        <div className="topbar-row">
          <Link to="/" className="brand" aria-label="CODI admin, overview">
            <BrandMark />
            <span className="brand-name">codi</span>
            <span className="brand-sub">admin</span>
          </Link>
          <span className="topbar-sep" aria-hidden="true">
            /
          </span>
          <Menu
            label="Servers"
            align="start"
            trigger={(t) => (
              <button className="switcher" {...t} aria-label={`Server: ${currentName ?? "none"}`}>
                <IconServer size={14} />
                <span className="switcher-name">{currentName ?? "No server"}</span>
                <IconChevronDown size={12} />
              </button>
            )}
          >
            {(close) => (
              <>
                <div className="menu-label">Servers</div>
                {servers.map((s) => (
                  <button
                    key={s.key}
                    role="menuitemradio"
                    aria-checked={s.key === current}
                    className="menu-item"
                    onClick={() => {
                      rememberServer(s.key);
                      close();
                      navigate(
                        section === "plugins" ? `/servers/${s.key}/plugins` : `/servers/${s.key}`,
                      );
                    }}
                  >
                    <span
                      className={`dot dot-${s.sync === "Synced" ? "ok" : s.sync === "OutOfSync" ? "warn" : "neutral"}`}
                    />
                    <span className="menu-item-main">{s.name}</span>
                    <code className="menu-item-hint">{s.key}</code>
                  </button>
                ))}
                {servers.length === 0 ? <div className="menu-empty">No servers</div> : null}
              </>
            )}
          </Menu>
          <div className="topbar-spacer" />
          {status.data?.revision ? (
            <span className="topbar-rev" title={status.data.revision.message}>
              <span className="muted">rev</span> <Sha id={status.data.revision.id} />
            </span>
          ) : null}
          {config.mock ? <Badge tone="violet">Mock runtime</Badge> : null}
          <button
            className="icon-btn"
            onClick={toggleTheme}
            aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            title={theme === "dark" ? "Light theme" : "Dark theme"}
          >
            {theme === "dark" ? <IconSun /> : <IconMoon />}
          </button>
          <UserMenu me={me} />
        </div>
        <nav className="nav" aria-label="Main">
          <NavLink to="/" active={section === "overview"}>
            Overview
          </NavLink>
          {current ? (
            <>
              <NavLink to={`/servers/${current}`} active={section === "server"}>
                Server
              </NavLink>
              <NavLink to={`/servers/${current}/plugins`} active={section === "plugins"}>
                Plugins
              </NavLink>
            </>
          ) : null}
          {sim ? (
            <NavLink to="/simulator" active={section === "simulator"}>
              <IconPlay size={12} /> Simulator
            </NavLink>
          ) : null}
        </nav>
      </header>
      <main className="main" id="main">
        {page}
      </main>
    </div>
  );
}

function NavLink({ to, active, children }: { to: string; active: boolean; children: ReactNode }) {
  return (
    <Link to={to} className="nav-link" aria-current={active ? "page" : undefined}>
      {children}
    </Link>
  );
}

function UserMenu({ me }: { me: Me }) {
  const logout = useLogout();
  const name = me.user.displayName || me.user.username;
  return (
    <Menu
      label="Account"
      trigger={(t) => (
        <button className="user-btn" {...t} aria-label={`Account: ${name}`}>
          <Avatar name={name} url={me.user.avatarUrl} size={24} />
        </button>
      )}
    >
      {() => (
        <>
          <div className="menu-user">
            <Avatar name={name} url={me.user.avatarUrl} size={32} />
            <div>
              <div className="menu-user-name">{name}</div>
              <div className="menu-user-id">
                @{me.user.username} · <code>{me.user.id}</code>
              </div>
            </div>
          </div>
          <div className="menu-label">Access</div>
          {me.servers.map((s) => (
            <div key={s.key} className="menu-row">
              <span>{s.name}</span>
              <RoleBadge role={s.role} />
            </div>
          ))}
          <div className="menu-divider" />
          <button
            role="menuitem"
            className="menu-item"
            onClick={() => void logout().then(() => navigate("/", { replace: true }))}
          >
            <IconLogout size={14} />
            <span className="menu-item-main">Log out</span>
          </button>
        </>
      )}
    </Menu>
  );
}

function NoAccess({ me }: { me: Me }) {
  const logout = useLogout();
  return (
    <div className="login-card">
      <BrandMark size={28} />
      <h1 className="login-title">No server to manage</h1>
      <p className="login-text">
        You are signed in as <strong>{me.user.displayName || me.user.username}</strong>, but you are
        not an admin or moderator of any server managed by this runtime. Ask an owner to add you to{" "}
        <code>spec.access</code> in the server's YAML.
      </p>
      <Button onClick={() => void logout()} icon={<IconLogout size={14} />}>
        Log out
      </Button>
    </div>
  );
}
