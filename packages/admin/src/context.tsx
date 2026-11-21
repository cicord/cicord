/** App-wide contexts and small brand pieces shared by the shell and the pages. */
import type { AccessRole, Me, RuntimeStatus, ServerStatus } from "@codi/client";
import { createContext, useContext } from "react";
import { Badge } from "./components/ui/badge.tsx";
import type { AdminConfig } from "./config.ts";
import { useTranslation } from "./i18n/index.ts";

export const ConfigContext = createContext<AdminConfig | null>(null);

export function useAdminConfig(): AdminConfig {
  const value = useContext(ConfigContext);
  if (!value) throw new Error("missing <App config>");
  return value;
}

export const MeContext = createContext<Me | null>(null);

/** The logged-in user (inside the authenticated shell). */
export function useViewer(): Me {
  const me = useContext(MeContext);
  if (!me) throw new Error("useViewer outside the authenticated shell");
  return me;
}

/** The server selected in the switcher and the runtime status (inside the shell). */
export interface Workspace {
  status: RuntimeStatus | undefined;
  serverKey: string | undefined;
  server: ServerStatus | undefined;
  role: AccessRole | undefined;
  isAdmin: boolean;
  simulator: boolean;
  selectServer: (key: string) => void;
}

export const WorkspaceContext = createContext<Workspace | null>(null);

export function useWorkspace(): Workspace {
  const ws = useContext(WorkspaceContext);
  if (!ws) throw new Error("useWorkspace outside the shell");
  return ws;
}

export function BrandMark({ size = 20 }: { size?: number }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true" className="shrink-0">
      <circle cx="11" cy="16" r="4.5" fill="none" stroke="currentColor" strokeWidth="2.5" />
      <circle cx="24" cy="8.5" r="3" fill="var(--success)" />
      <circle cx="24" cy="23.5" r="3" fill="var(--discord)" />
      <path
        d="M15 14l6-4M15 18l6 4"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function RoleBadge({ role }: { role: string | undefined }) {
  const { t } = useTranslation();
  if (role === "admin") return <Badge tone="brand">{t("roles.admin")}</Badge>;
  if (role === "moderator") return <Badge tone="info">{t("roles.moderator")}</Badge>;
  if (role === "member") return <Badge>{t("roles.member")}</Badge>;
  return <Badge>{t("roles.none")}</Badge>;
}
