import type { PortalServer } from "@codi/client";
import { useMe, useLinkTokenLogin, usePortalServers } from "@codi/react";
import { Avatar, Button, Spinner, ThemeProvider } from "@codi/ui";
import { createContext, useContext, useEffect, type ReactNode } from "react";
import { forgetServer, requestedServer, type PortalConfig } from "./config.ts";
import { t } from "./i18n/fr.ts";
import { Link, usePathname } from "./router.tsx";

// ───────────────────────────────────────────────────────────── contexts

const ConfigContext = createContext<PortalConfig | null>(null);
const ServerContext = createContext<PortalServer | null>(null);

export const ConfigProvider = ConfigContext.Provider;

export function usePortalConfig(): PortalConfig {
  const value = useContext(ConfigContext);
  if (!value) throw new Error("missing <ConfigProvider>");
  return value;
}

export function useServer(): PortalServer {
  const value = useContext(ServerContext);
  if (!value) throw new Error("missing server");
  return value;
}

/** Starts a login that comes back to the current page. */
export function useLogin(): () => void {
  const config = usePortalConfig();
  return () => config.login(`${window.location.pathname}${window.location.search}`);
}

// ───────────────────────────────────────────────────────────── server selection

/**
 * Picks the server (`?server=`, remembered choice, VITE_CODI_SERVER or the first portal
 * server), then themes the whole page with its colors.
 */
export function ServerGate({
  onServer,
  children,
}: {
  onServer: (key: string) => void;
  children: ReactNode;
}) {
  const servers = usePortalServers();
  const wanted = requestedServer();
  const list = servers.data ?? [];
  const server = list.find((s) => s.key === wanted) ?? list[0];

  useEffect(() => {
    if (wanted && list.length > 0 && !list.some((s) => s.key === wanted)) forgetServer();
  }, [wanted, list]);
  useEffect(() => {
    if (server) onServer(server.key);
  }, [server, onServer]);

  useEffect(() => {
    if (server) document.title = t.portalOf(server.name);
  }, [server]);

  if (servers.status === "error") {
    return (
      <ThemeProvider global>
        <CenteredMessage title={t.apiDown}>
          <Button variant="secondary" onClick={() => void servers.refetch()}>
            {t.onboarding.retry}
          </Button>
        </CenteredMessage>
      </ThemeProvider>
    );
  }
  if (!servers.data) {
    return (
      <ThemeProvider global>
        <div className="portal-loading">
          <Spinner size="lg" label={t.loading} />
        </div>
      </ThemeProvider>
    );
  }
  if (!server) {
    return (
      <ThemeProvider global>
        <CenteredMessage title={t.noServer.title}>
          <p>{t.noServer.text}</p>
        </CenteredMessage>
      </ThemeProvider>
    );
  }
  return (
    <ServerContext.Provider value={server}>
      <ThemeProvider theme={server.theme} global>
        {children}
      </ThemeProvider>
    </ServerContext.Provider>
  );
}

// ───────────────────────────────────────────────────────────── chrome

export function ServerMark({ size = 40, className }: { size?: number; className?: string }) {
  const server = useServer();
  const src = server.theme.logo || server.iconUrl;
  const initials = server.name
    .split(/\s+/)
    .map((w) => Array.from(w)[0] ?? "")
    .join("")
    .slice(0, 2)
    .toUpperCase();
  return (
    <span
      className={["server-mark", className].filter(Boolean).join(" ")}
      style={{ width: size, height: size }}
    >
      {src ? (
        <img src={src} alt="" width={size} height={size} />
      ) : (
        <span aria-hidden="true">{initials}</span>
      )}
    </span>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const server = useServer();
  const config = usePortalConfig();
  const me = useMe();
  const login = useLogin();
  const linkLogin = useLinkTokenLogin();
  const path = usePathname();
  const user = me.data?.user;

  return (
    <div className="portal">
      <a className="skip-link" href="#main">
        Aller au contenu
      </a>
      <header className="portal-header">
        <Link to="/" className="portal-brand" aria-current={path === "/" ? "page" : undefined}>
          <ServerMark size={34} />
          <span>{server.name}</span>
        </Link>
        <div className="portal-header__end">
          {user ? (
            <Link
              to="/me"
              className="portal-user"
              aria-current={path === "/me" ? "page" : undefined}
            >
              <Avatar src={user.avatarUrl || undefined} name={user.displayName} size={30} />
              <span className="portal-user__name">{user.displayName}</span>
            </Link>
          ) : me.status === "success" && linkLogin.status !== "pending" ? (
            <Button size="sm" variant="secondary" onClick={login}>
              {t.loginShort}
            </Button>
          ) : null}
        </div>
      </header>
      <main id="main" className="portal-main">
        {children}
      </main>
      <footer className="portal-footer">
        <span>
          Portail de {server.name}, propulsé par <a href="https://github.com/codi-dev">CODI</a>
        </span>
      </footer>
      {config.mock ? <p className="portal-mock-badge">{t.demoMode}</p> : null}
    </div>
  );
}

export function CenteredMessage({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="centered-message">
      <h1>{title}</h1>
      {children}
    </div>
  );
}
