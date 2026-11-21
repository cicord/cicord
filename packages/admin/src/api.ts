/** Data hooks of the admin console, on top of @codi/react's query cache. */
import type {
  AccessRole,
  AuditEntry,
  Me,
  Message,
  Plan,
  ServerDetail,
  ServerResources,
  SimCommand,
  SimState,
  SyncRecord,
} from "@codi/client";
import { useCodi, useCodiQuery, type QueryResult } from "@codi/react";
import { useMemo } from "react";
import type { AdminPluginInstance, AdminStatus } from "./types.ts";

export const keys = {
  status: () => ["admin", "status"] as const,
  history: (limit: number) => ["admin", "history", limit] as const,
  server: (key: string) => ["admin", "server", key] as const,
  resources: (key: string) => ["admin", "server", key, "resources"] as const,
  plan: (key: string) => ["admin", "server", key, "plan"] as const,
  audit: (key: string, before: string) => ["admin", "server", key, "audit", before] as const,
  plugins: (key: string) => ["admin", "server", key, "plugins"] as const,
  sim: () => ["sim"] as const,
  simState: () => ["sim", "state"] as const,
  simCommands: (serverId: string) => ["sim", "commands", serverId] as const,
  simMessages: (channelId: string) => ["sim", "messages", channelId] as const,
  simDms: (userId: string) => ["sim", "dms", userId] as const,
};

export function useStatus(): QueryResult<AdminStatus> {
  const { client } = useCodi();
  return useCodiQuery(
    keys.status(),
    (signal) => client.admin.status({ signal }) as unknown as Promise<AdminStatus>,
  );
}

export function useHistory(limit = 30): QueryResult<SyncRecord[]> {
  const { client } = useCodi();
  return useCodiQuery(keys.history(limit), (signal) => client.admin.history({ limit }, { signal }));
}

export function useServer(key: string): QueryResult<ServerDetail> {
  const { client } = useCodi();
  return useCodiQuery(keys.server(key), (signal) => client.admin.server(key, { signal }));
}

export function useResources(key: string | undefined): QueryResult<ServerResources> {
  const { client } = useCodi();
  return useCodiQuery(key ? keys.resources(key) : null, (signal) =>
    client.admin.resources(key!, { signal }),
  );
}

export function usePlan(key: string, enabled = true): QueryResult<Plan> {
  const { client } = useCodi();
  return useCodiQuery(keys.plan(key), (signal) => client.admin.plan(key, { signal }), { enabled });
}

export const AUDIT_PAGE = 25;

export function useAudit(key: string, before: string): QueryResult<AuditEntry[]> {
  const { client } = useCodi();
  return useCodiQuery(keys.audit(key, before), (signal) =>
    client.admin.audit(key, before ? { before, limit: AUDIT_PAGE } : { limit: AUDIT_PAGE }, {
      signal,
    }),
  );
}

export function usePlugins(key: string): QueryResult<AdminPluginInstance[]> {
  const { client } = useCodi();
  return useCodiQuery(keys.plugins(key), (signal) => client.admin.plugins(key, { signal }));
}

export function useSimState(enabled = true): QueryResult<SimState> {
  const { client } = useCodi();
  return useCodiQuery(keys.simState(), (signal) => client.sim.state({ signal }), { enabled });
}

export function useSimCommands(serverId: string | undefined): QueryResult<SimCommand[]> {
  const { client } = useCodi();
  return useCodiQuery(serverId ? keys.simCommands(serverId) : null, (signal) =>
    client.sim.commands(serverId, { signal }),
  );
}

export function useSimMessages(channelId: string | undefined): QueryResult<Message[]> {
  const { client } = useCodi();
  return useCodiQuery(channelId ? keys.simMessages(channelId) : null, (signal) =>
    client.sim.messages(channelId!, { limit: 100 }, { signal }),
  );
}

export function useSimDms(userId: string | undefined): QueryResult<Message[]> {
  const { client } = useCodi();
  return useCodiQuery(userId ? keys.simDms(userId) : null, (signal) =>
    client.sim.dms(userId!, { signal }),
  );
}

/** The viewer's role on a server ("admin" | "moderator" | "member" | undefined). */
export function useRole(me: Me, server: string | undefined): AccessRole | undefined {
  return useMemo(
    () => (server ? me.servers.find((s) => s.key === server)?.role : undefined),
    [me, server],
  );
}
