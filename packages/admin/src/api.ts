/** Data hooks of the admin console, on top of @cicord/react's query cache. */
import type {
  AnalyticsSeries,
  AnalyticsSeriesParams,
  AnalyticsSummary,
  AnalyticsSummaryQuery,
  AuditEntry,
  PluginInstance,
  Message,
  PauseState,
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
  StoreUpdateReview,
  StoreUpdatesReview,
  SyncRecord,
} from "@cicord/client";
import { useCicord, useCicordQuery, type QueryResult } from "@cicord/react";

export const keys = {
  status: () => ["admin", "status"] as const,
  history: (limit: number, target = "") => ["admin", "history", limit, target] as const,
  server: (key: string) => ["admin", "server", key] as const,
  resources: (key: string) => ["admin", "server", key, "resources"] as const,
  plan: (key: string) => ["admin", "server", key, "plan"] as const,
  pause: (key: string) => ["admin", "server", key, "pause"] as const,
  audit: (key: string, before: string) => ["admin", "server", key, "audit", before] as const,
  /** Prefix of every analytics query of a server (refresh). */
  analytics: (key: string) => ["admin", "server", key, "analytics"] as const,
  analyticsSummary: (key: string, query: AnalyticsSummaryQuery) =>
    ["admin", "server", key, "analytics", "summary", JSON.stringify(query)] as const,
  analyticsSeries: (key: string, params: AnalyticsSeriesParams) =>
    ["admin", "server", key, "analytics", "series", JSON.stringify(params)] as const,
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
  storeUpdate: (source: string, plugin: string) =>
    ["admin", "store", "update", source, plugin] as const,
  storeUpdates: () => ["admin", "store", "updates"] as const,
  sim: () => ["sim"] as const,
  simState: (userId = "") => ["sim", "state", userId] as const,
  simCommands: (serverId: string, channelId: string, userId: string) =>
    ["sim", "commands", serverId, channelId, userId] as const,
  simMessages: (channelId: string, userId: string) =>
    ["sim", "messages", channelId, userId] as const,
  simDms: (userId: string) => ["sim", "dms", userId] as const,
};

export function useStatus(): QueryResult<RuntimeStatus> {
  const { client } = useCicord();
  return useCicordQuery(keys.status(), (signal) => client.admin.status({ signal }));
}

export function useHistory(limit = 30, target?: string): QueryResult<SyncRecord[]> {
  const { client } = useCicord();
  return useCicordQuery(keys.history(limit, target ?? ""), (signal) =>
    client.admin.history(target ? { limit, target } : { limit }, { signal }),
  );
}

export function useServer(key: string): QueryResult<ServerDetail> {
  const { client } = useCicord();
  return useCicordQuery(keys.server(key), (signal) => client.admin.server(key, { signal }));
}

export function useResources(key: string | undefined): QueryResult<ServerResources> {
  const { client } = useCicord();
  return useCicordQuery(key ? keys.resources(key) : null, (signal) =>
    client.admin.resources(key!, { signal }),
  );
}

export function usePlan(key: string, enabled = true): QueryResult<Plan> {
  // (enabled = false for moderators: the plan endpoint is admin-only)
  const { client } = useCicord();
  return useCicordQuery(keys.plan(key), (signal) => client.admin.plan(key, { signal }), {
    enabled,
  });
}

/** `GET /servers/{server}/pause` (moderator): the pause and the plan resuming lets apply. */
export function usePauseState(key: string, enabled = true): QueryResult<PauseState> {
  const { client } = useCicord();
  return useCicordQuery(keys.pause(key), (signal) => client.admin.pauseState(key, { signal }), {
    enabled,
  });
}

export const AUDIT_PAGE = 25;

export function useAudit(key: string, before: string): QueryResult<AuditEntry[]> {
  const { client } = useCicord();
  return useCicordQuery(keys.audit(key, before), (signal) =>
    client.admin.audit(key, before ? { before, limit: AUDIT_PAGE } : { limit: AUDIT_PAGE }, {
      signal,
    }),
  );
}

/** `GET /servers/{server}/analytics/summary?period=` or `?from=&to=` (moderator). */
export function useAnalyticsSummary(
  key: string,
  query: AnalyticsSummaryQuery,
): QueryResult<AnalyticsSummary> {
  const { client } = useCicord();
  return useCicordQuery(keys.analyticsSummary(key, query), (signal) =>
    client.analytics.summary(key, query, { signal }),
  );
}

/** `GET /servers/{server}/analytics/series` (moderator); null params: not yet. */
export function useAnalyticsSeries(
  key: string,
  params: AnalyticsSeriesParams | null,
): QueryResult<AnalyticsSeries> {
  const { client } = useCicord();
  return useCicordQuery(params ? keys.analyticsSeries(key, params) : null, (signal) =>
    client.analytics.series(key, params!, { signal }),
  );
}

/** Every plugin instance of a server (⌘K palette). */
export function usePlugins(key: string | undefined): QueryResult<PluginInstance[]> {
  const { client } = useCicord();
  return useCicordQuery(key ? keys.pluginsAll(key) : null, (signal) =>
    client.admin.plugins(key!, { signal }),
  );
}

/** One plugin instance (`GET /servers/{server}/plugins/{bot}/{instance}`). */
export function usePlugin(
  key: string,
  bot: string | undefined,
  instance: string | undefined,
): QueryResult<PluginInstance> {
  const { client } = useCicord();
  return useCicordQuery(bot && instance ? keys.plugin(key, bot, instance) : null, (signal) =>
    client.admin.plugin(key, bot!, instance!, { signal }),
  );
}

/** Store sources with their plugin counts (`GET /store?summary=1`). */
export function useStoreSources(enabled = true): QueryResult<StoreSourceSummary[]> {
  const { client } = useCicord();
  return useCicordQuery(keys.storeSources(), (signal) => client.store.sources({}, { signal }), {
    enabled,
  });
}

/** What a Store plugin requests at its latest version (`GET /store/{source}/{plugin}`). */
export function useStoreDetail(
  source: string | undefined,
  plugin: string | undefined,
): QueryResult<StoreDetail> {
  const { client } = useCicord();
  return useCicordQuery(source && plugin ? keys.storeDetail(source, plugin) : null, (signal) =>
    client.store.detail(source!, plugin!, { signal }),
  );
}

/** The update review of an installed Store plugin (`GET /store/{source}/{plugin}/update`). */
export function useStoreUpdateReview(
  source: string,
  plugin: string,
): QueryResult<StoreUpdateReview> {
  const { client } = useCicord();
  return useCicordQuery(keys.storeUpdate(source, plugin), (signal) =>
    client.store.updateReview(source, plugin, { signal }),
  );
}

/**
 * After a Store write (install, update): the Store, the server's plugins, the runtime
 * status, the sync history and the audit log change.
 */
export function invalidateAfterStoreWrite(
  cache: ReturnType<typeof useCicord>["cache"],
  serverKey: string,
): Promise<unknown> {
  return Promise.all([
    cache.invalidate(keys.store()),
    cache.invalidate(keys.plugins(serverKey)),
    cache.invalidate(["admin", "status"]),
    cache.invalidate(["admin", "history"]),
    cache.invalidate(["admin", "server", serverKey, "audit"]),
  ]);
}

/** The review of every available update (`GET /store/updates`). */
export function useStoreUpdatesReview(): QueryResult<StoreUpdatesReview> {
  const { client } = useCicord();
  return useCicordQuery(keys.storeUpdates(), (signal) => client.store.updatesReview({ signal }));
}

export function useSimState(enabled = true, userId?: string): QueryResult<SimState> {
  const { client } = useCicord();
  return useCicordQuery(keys.simState(userId), (signal) => client.sim.state({ signal, userId }), {
    enabled,
  });
}

export function useSimCommands(
  serverId: string,
  channelId: string | undefined,
  userId: string | undefined,
): QueryResult<SimCommand[]> {
  const { client } = useCicord();
  return useCicordQuery(
    channelId && userId ? keys.simCommands(serverId, channelId, userId) : null,
    (signal) => client.sim.commands(serverId, { signal, channelId, userId }),
  );
}

export function useSimMessages(
  channelId: string,
  userId: string | undefined,
): QueryResult<Message[]> {
  const { client } = useCicord();
  return useCicordQuery(userId ? keys.simMessages(channelId, userId) : null, (signal) =>
    client.sim.messages(channelId, { limit: 100, userId }, { signal }),
  );
}

export function useSimDms(userId: string | undefined): QueryResult<Message[]> {
  const { client } = useCicord();
  return useCicordQuery(userId ? keys.simDms(userId) : null, (signal) =>
    client.sim.dms(userId!, { signal }),
  );
}
