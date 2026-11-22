/** Data hooks of the admin console, on top of @codi/react's query cache. */
import type {
  AccessRole,
  AuditEntry,
  Me,
  PluginInstance,
  Message,
  Plan,
  ServerDetail,
  RuntimeStatus,
  ServerResources,
  SimCommand,
  SimState,
  PluginListParams,
  StoreDetail,
  StorePluginParams,
  StoreSourceSummary,
  SyncRecord,
} from "@codi/client";
import { useCodi, useCodiQuery, type QueryResult } from "@codi/react";
import { useMemo } from "react";

export const keys = {
  status: () => ["admin", "status"] as const,
  history: (limit: number, target = "") => ["admin", "history", limit, target] as const,
  server: (key: string) => ["admin", "server", key] as const,
  resources: (key: string) => ["admin", "server", key, "resources"] as const,
  plan: (key: string) => ["admin", "server", key, "plan"] as const,
  audit: (key: string, before: string) => ["admin", "server", key, "audit", before] as const,
  /** Prefix of every plugin query of a server (list, pages, single instances). */
  plugins: (key: string) => ["admin", "server", key, "plugins"] as const,
  pluginsAll: (key: string) => ["admin", "server", key, "plugins", "all"] as const,
  pluginsPage: (key: string, params: PluginListParams) =>
    ["admin", "server", key, "plugins", "page", JSON.stringify(params)] as const,
  plugin: (key: string, bot: string, instance: string) =>
    ["admin", "server", key, "plugins", "one", bot, instance] as const,
  store: () => ["admin", "store"] as const,
  storeSources: () => ["admin", "store", "sources"] as const,
  storePlugins: (params: StorePluginParams) =>
    ["admin", "store", "plugins", JSON.stringify(params)] as const,
  storeDetail: (source: string, plugin: string) =>
    ["admin", "store", "detail", source, plugin] as const,
  sim: () => ["sim"] as const,
  simState: () => ["sim", "state"] as const,
  simCommands: (serverId: string) => ["sim", "commands", serverId] as const,
  simMessages: (channelId: string) => ["sim", "messages", channelId] as const,
  simDms: (userId: string) => ["sim", "dms", userId] as const,
};

export function useStatus(): QueryResult<RuntimeStatus> {
  const { client } = useCodi();
  return useCodiQuery(keys.status(), (signal) => client.admin.status({ signal }));
}

export function useHistory(limit = 30, target?: string): QueryResult<SyncRecord[]> {
  const { client } = useCodi();
  return useCodiQuery(keys.history(limit, target ?? ""), (signal) =>
    client.admin.history(target ? { limit, target } : { limit }, { signal }),
  );
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
  // (enabled = false for moderators: the plan endpoint is admin-only)
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

/** Every plugin instance of a server (⌘K palette). */
export function usePlugins(key: string | undefined): QueryResult<PluginInstance[]> {
  const { client } = useCodi();
  return useCodiQuery(key ? keys.pluginsAll(key) : null, (signal) =>
    client.admin.plugins(key!, { signal }),
  );
}

/** One plugin instance (`GET /servers/{server}/plugins/{bot}/{instance}`). */
export function usePlugin(
  key: string,
  bot: string | undefined,
  instance: string | undefined,
): QueryResult<PluginInstance> {
  const { client } = useCodi();
  return useCodiQuery(bot && instance ? keys.plugin(key, bot, instance) : null, (signal) =>
    client.admin.plugin(key, bot!, instance!, { signal }),
  );
}

/** Store sources with their plugin counts (`GET /store?summary=1`). */
export function useStoreSources(enabled = true): QueryResult<StoreSourceSummary[]> {
  const { client } = useCodi();
  return useCodiQuery(keys.storeSources(), (signal) => client.store.sources({}, { signal }), {
    enabled,
  });
}

/** What a Store plugin requests at its latest version (`GET /store/{source}/{plugin}`). */
export function useStoreDetail(
  source: string | undefined,
  plugin: string | undefined,
): QueryResult<StoreDetail> {
  const { client } = useCodi();
  return useCodiQuery(source && plugin ? keys.storeDetail(source, plugin) : null, (signal) =>
    client.store.detail(source!, plugin!, { signal }),
  );
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
