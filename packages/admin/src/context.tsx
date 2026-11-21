/** App-wide contexts and small brand pieces shared by the shell and the pages. */
import type { Me } from "@codi/client";
import { createContext, useContext } from "react";
import type { AdminConfig } from "./config.ts";
import { Badge } from "./ui/index.tsx";

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

export function BrandMark({ size = 20 }: { size?: number }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true" className="brand-mark">
      <circle cx="11" cy="16" r="4.5" fill="none" stroke="currentColor" strokeWidth="2.5" />
      <circle cx="24" cy="8.5" r="3" fill="var(--ok)" />
      <circle cx="24" cy="23.5" r="3" fill="var(--accent)" />
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
  if (role === "admin") return <Badge tone="accent">Admin</Badge>;
  if (role === "moderator") return <Badge tone="info">Moderator</Badge>;
  if (role === "member") return <Badge>Member</Badge>;
  return <Badge>No access</Badge>;
}
