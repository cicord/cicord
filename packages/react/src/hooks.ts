import {
  CicordApiError,
  resolveRoute,
  type OptionalArg,
  type RouteInput,
  type RouteKeyOf,
  type RouteMethod,
  type RouteResponseOf,
  type RoutesOf,
  type Json,
  type Me,
  type PortalMe,
  type PortalPlugin,
  type PortalRole,
  type PortalServer,
  type Query,
  type StoredSession,
} from "@cicord/client";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { hashKey, type QueryKey, type QueryState, type QueryStatus } from "./cache.ts";
import { useCicord, useServerKey } from "./provider.tsx";

// ───────────────────────────────────────────────────────────── generic query

export interface QueryResult<T> {
  data: T | undefined;
  error: unknown;
  status: QueryStatus;
  /** First load in progress (no data yet). */
  isLoading: boolean;
  /** Any request in progress, including background refreshes. */
  isFetching: boolean;
  refetch: () => Promise<T | undefined>;
}

export interface QueryOptions {
  /** Default true. A query with `enabled: false` (or a null key) stays idle. */
  enabled?: boolean;
}

/** Cached query bound to the provider's cache. `key: null` disables the query. */
export function useCicordQuery<T>(
  key: QueryKey | null,
  fn: (signal: AbortSignal) => Promise<T>,
  options: QueryOptions = {},
): QueryResult<T> {
  const { cache } = useCicord();
  const enabled = key !== null && (options.enabled ?? true);
  const hash = key ? hashKey(key) : "";
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const keyRef = useRef(key);
  keyRef.current = key;

  const subscribe = useCallback(
    (listener: () => void) =>
      keyRef.current ? cache.subscribe(keyRef.current, listener) : () => {},
    // eslint-disable-next-line react-hooks/exhaustive-deps -- hash is the stable serialization of the key; keyRef always holds the current key
    [cache, hash],
  );
  const getSnapshot = useCallback(
    (): QueryState<T> | undefined =>
      keyRef.current ? cache.getState<T>(keyRef.current) : undefined,
    // eslint-disable-next-line react-hooks/exhaustive-deps -- hash is the stable serialization of the key; keyRef always holds the current key
    [cache, hash],
  );
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

  useEffect(() => {
    if (!enabled || !keyRef.current) return;
    cache.fetch(keyRef.current, (signal) => fnRef.current(signal)).catch(() => undefined);
  }, [cache, hash, enabled]);

  const refetch = useCallback(async () => {
    if (!keyRef.current) return undefined;
    try {
      return await cache.fetch(keyRef.current, (signal) => fnRef.current(signal), { force: true });
    } catch {
      return undefined;
    }
  }, [cache]);

  const status: QueryStatus = state?.status ?? "idle";
  return {
    data: state?.data,
    error: state?.error,
    status: enabled && status === "idle" ? "loading" : status,
    isLoading: enabled && (status === "idle" || status === "loading"),
    isFetching: state?.fetching ?? false,
    refetch,
  };
}

// ───────────────────────────────────────────────────────────── keys

export const cicordKeys = {
  me: () => ["cicord", "me"] as const,
  portalServers: () => ["cicord", "portal", "servers"] as const,
  portalServer: (server: string) => ["cicord", "portal", server] as const,
  member: (server: string) => ["cicord", "portal", server, "me"] as const,
  plugin: (server: string, bot: string, instance: string, path: string, query?: Query) =>
    [
      "cicord",
      "plugin",
      server,
      bot,
      instance,
      path,
      query ? JSON.stringify(query) : null,
    ] as const,
};

const nullOn401 = async <T>(promise: Promise<T>): Promise<T | null> => {
  try {
    return await promise;
  } catch (error) {
    if (error instanceof CicordApiError && error.code === "unauthenticated") return null;
    throw error;
  }
};

// ───────────────────────────────────────────────────────────── auth

/** Current user (`GET /me`); `data` is `null` when logged out. */
export function useMe(): QueryResult<Me | null> {
  const { client } = useCicord();
  return useCicordQuery(cicordKeys.me(), (signal) => client.meOrNull({ signal }));
}

export type LinkTokenLoginState =
  | { status: "none" }
  | { status: "pending" }
  | { status: "success"; me: Me }
  | { status: "error"; error: unknown };

// One exchange per token and client, whatever the number of mounts (StrictMode, remounts).
const exchanges = new WeakMap<object, Map<string, Promise<Me | null>>>();

/** The login credential left in the current URL: `cicord_token` (bot link) or, in token mode, `cicord_code`. */
function urlCredential(param: string, tokenMode: boolean): string | null {
  if (typeof location === "undefined") return null;
  const params = new URL(location.href).searchParams;
  return params.get(param) ?? (tokenMode ? params.get("cicord_code") : null);
}

/**
 * Exchanges the `cicord_token` of the current URL (signed link sent by a bot) — or, in token
 * mode, the `cicord_code` of an OAuth login — for a session, once. The parameter is removed
 * from the address bar immediately. On success, the cached user and member queries are
 * refreshed.
 */
export function useLinkTokenLogin(options: { param?: string } = {}): LinkTokenLoginState {
  const { client, cache } = useCicord();
  const param = options.param ?? "cicord_token";
  const tokenMode = client.authMode === "token";
  const [state, setState] = useState<LinkTokenLoginState>(() => {
    const token = urlCredential(param, tokenMode);
    const inflight = token ? exchanges.get(client)?.get(token) : undefined;
    return token || inflight ? { status: "pending" } : { status: "none" };
  });
  const tokenRef = useRef<string | null>(urlCredential(param, tokenMode));

  useEffect(() => {
    const token = tokenRef.current;
    if (!token) return undefined;
    let perClient = exchanges.get(client);
    if (!perClient) {
      perClient = new Map();
      exchanges.set(client, perClient);
    }
    let promise = perClient.get(token);
    if (!promise) {
      promise = client.auth.exchangeLinkToken({ param });
      perClient.set(token, promise);
    }
    let active = true;
    const exchange = promise;
    const settle = async () => {
      let me: Awaited<typeof exchange>;
      try {
        me = await exchange;
      } catch (error: unknown) {
        if (active) setState({ status: "error", error });
        return;
      }
      if (!active) return;
      if (me) {
        cache.setData(cicordKeys.me(), me);
        void cache.invalidate(["cicord", "portal"]);
        void cache.invalidate(["cicord", "plugin"]);
        setState({ status: "success", me });
      } else setState({ status: "none" });
    };
    void settle();
    return () => {
      active = false;
    };
  }, [client, cache, param]);

  return state;
}

/**
 * The token-mode session (`null` in cookie mode or when logged out), updated on login,
 * logout and expiry.
 */
export function useSession(): StoredSession | null {
  const { client } = useCicord();
  const subscribe = useCallback((fn: () => void) => client.onSessionChange(fn), [client]);
  const token = useSyncExternalStore(
    subscribe,
    () => client.session()?.token ?? null,
    () => null,
  );
  return token ? client.session() : null;
}

/** Logs out and clears every cached query. */
export function useLogout(): () => Promise<void> {
  const { client, cache } = useCicord();
  return useCallback(async () => {
    await client.auth.logout();
    cache.clear();
    cache.setData(cicordKeys.me(), null);
  }, [client, cache]);
}

// ───────────────────────────────────────────────────────────── portal

export function usePortalServers(): QueryResult<PortalServer[]> {
  const { client } = useCicord();
  return useCicordQuery(cicordKeys.portalServers(), (signal) => client.portal.servers({ signal }));
}

/** `GET /portal/{server}` for the given (or provider's) server. */
export function usePortalServer(server?: string): QueryResult<PortalServer> {
  const { client } = useCicord();
  const key = useServerKey(server);
  return useCicordQuery(key ? cicordKeys.portalServer(key) : null, (signal) =>
    client.portal.server(key as string, { signal }),
  );
}

/** The viewer as a member of the server; `data` is `null` when logged out. */
export function useMember(server?: string): QueryResult<PortalMe | null> {
  const { client } = useCicord();
  const key = useServerKey(server);
  return useCicordQuery(key ? cicordKeys.member(key) : null, (signal) =>
    nullOn401(client.portal.me(key as string, { signal })),
  );
}

/** The viewer's roles on the server ([] when logged out or loading). */
export function useRoles(server?: string): PortalRole[] {
  return useMember(server).data?.member.roles ?? [];
}

/** Finds the instance of a plugin exposed on the server's portal (first match). */
export function usePortalPlugin(
  plugin: string,
  options: { server?: string; bot?: string; instance?: string } = {},
): { instance: PortalPlugin | undefined; status: QueryStatus; error: unknown } {
  const server = usePortalServer(options.server);
  const instance = server.data?.plugins.find(
    (p) =>
      (options.instance ? p.instance === options.instance : p.plugin === plugin) &&
      (options.bot === undefined || p.bot === options.bot),
  );
  return { instance, status: server.status, error: server.error };
}

// ───────────────────────────────────────────────────────────── plugin routes

export interface PluginRef {
  bot: string;
  instance: string;
  /** Defaults to the provider's server. */
  server?: string;
}

/** `GET` on a plugin route, cached. Pass `ref: undefined` to wait. */
export function usePluginQuery<T = Json>(
  ref: PluginRef | undefined,
  path: string,
  options: QueryOptions & { query?: Query } = {},
): QueryResult<T> {
  const { client } = useCicord();
  const server = useServerKey(ref?.server);
  const key =
    ref && server ? cicordKeys.plugin(server, ref.bot, ref.instance, path, options.query) : null;
  return useCicordQuery<T>(
    key,
    (signal) =>
      client
        .plugin(server as string, ref!.bot, ref!.instance)
        .get<T>(path, options.query, { signal }),
    options.enabled === undefined ? {} : { enabled: options.enabled },
  );
}

export type MutationStatus = "idle" | "pending" | "success" | "error";

export interface MutationResult<TBody, TResult> {
  status: MutationStatus;
  data: TResult | undefined;
  error: unknown;
  isPending: boolean;
  /** Rejects on error. */
  mutateAsync: (body: TBody) => Promise<TResult>;
  /** Fire and forget (errors land in `error`). */
  mutate: (body: TBody) => void;
  reset: () => void;
}

/**
 * `POST`/`PUT`/`PATCH`/`DELETE` on a plugin route. On success, the plugin's cached queries are
 * refetched (and the member query too, since plugins often change roles).
 */
export function usePluginMutation<TBody = Json, TResult = Json>(
  ref: PluginRef | undefined,
  path: string,
  options: {
    method?: "POST" | "PUT" | "PATCH" | "DELETE";
    onSuccess?: (result: TResult) => void;
    /** Default true. */
    invalidate?: boolean;
  } = {},
): MutationResult<TBody, TResult> {
  const { client, cache } = useCicord();
  const server = useServerKey(ref?.server);
  const [state, setState] = useState<{
    status: MutationStatus;
    data: TResult | undefined;
    error: unknown;
  }>({
    status: "idle",
    data: undefined,
    error: undefined,
  });
  const optionsRef = useRef(options);
  optionsRef.current = options;

  const mutateAsync = useCallback(
    async (body: TBody): Promise<TResult> => {
      if (!ref || !server)
        throw new Error("@cicord/react: usePluginMutation called before the plugin is known");
      setState({ status: "pending", data: undefined, error: undefined });
      try {
        const { method = "POST", invalidate = true, onSuccess } = optionsRef.current;
        const result = await client
          .plugin(server, ref.bot, ref.instance)
          .request<TResult>(method, path, body === undefined ? {} : { body });
        setState({ status: "success", data: result, error: undefined });
        if (invalidate) {
          void cache.invalidate(["cicord", "plugin", server, ref.bot, ref.instance]);
          void cache.invalidate(cicordKeys.member(server));
        }
        onSuccess?.(result);
        return result;
      } catch (error) {
        setState({ status: "error", data: undefined, error });
        throw error;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- depends on the identity of the plugin instance (bot, instance), not on the ref object callers recreate each render
    [client, cache, server, ref?.bot, ref?.instance, path],
  );

  return {
    ...state,
    isPending: state.status === "pending",
    mutateAsync,
    mutate: useCallback(
      (body: TBody) => void mutateAsync(body).catch(() => undefined),
      [mutateAsync],
    ),
    reset: useCallback(() => setState({ status: "idle", data: undefined, error: undefined }), []),
  };
}

// ───────────────────────────────────────────────────────────── typed plugin routes

/** Which instance of the plugin to call. Default: the first one of the provider's server. */
export interface RouteTarget {
  server?: string;
  bot?: string;
  instance?: string;
}

export type RouteQueryOptions<
  N extends string,
  K extends keyof RoutesOf<N> & string,
> = QueryOptions & RouteTarget & RouteInput<K, RoutesOf<N>[K]>;

type LooseInput = RouteTarget &
  QueryOptions & { params?: Record<string, string>; query?: object; body?: unknown };

function routeTarget(o: RouteTarget): RouteTarget {
  return {
    ...(o.server ? { server: o.server } : {}),
    ...(o.bot ? { bot: o.bot } : {}),
    ...(o.instance ? { instance: o.instance } : {}),
  };
}

/**
 * `GET` on a typed plugin route, cached: the instance is found by plugin name in
 * `PortalServer.plugins` and the types come from the generated `src/cicord-routes.d.ts`
 * (`PluginRouteRegistry`).
 *
 * ```ts
 * const board = useRouteQuery("levels", "GET /leaderboard", { query: { limit: 50 } });
 * const poll = useRouteQuery("polls", "GET /polls/:id", { params: { id } });
 * ```
 *
 * `instance` is the plugin instance: undefined while the server loads or when the plugin is
 * not installed (the query then stays idle).
 */
export function useRouteQuery<N extends string, K extends RouteKeyOf<N, "GET"> & string>(
  plugin: N,
  route: K,
  ...options: OptionalArg<RouteQueryOptions<N, K>>
): QueryResult<RouteResponseOf<RoutesOf<N>[K]>> & { instance: PortalPlugin | undefined } {
  type T = RouteResponseOf<RoutesOf<N>[K]>;
  const opts = (options[0] ?? {}) as LooseInput;
  const { client } = useCicord();
  const { instance } = usePortalPlugin(plugin, routeTarget(opts));
  const server = useServerKey(opts.server);
  // An invalid parameter (".." from the page URL…) is an error state, never a request.
  let path: string | undefined;
  let invalid: unknown;
  try {
    [, path] = resolveRoute(route, opts.params);
  } catch (error) {
    invalid = error;
  }
  const query = opts.query as Query | undefined;
  const key =
    instance && server
      ? cicordKeys.plugin(
          server,
          instance.bot,
          instance.instance,
          path ?? `${route} ${JSON.stringify(opts.params ?? {})}`,
          query,
        )
      : null;
  const result = useCicordQuery<T>(
    key,
    (signal) =>
      invalid !== undefined || path === undefined
        ? Promise.reject(invalid)
        : client
            .plugin(server as string, instance!.bot, instance!.instance)
            .get<T>(path, query, { signal }),
    opts.enabled === undefined ? {} : { enabled: opts.enabled },
  );
  return { ...result, instance };
}

/**
 * `POST`/`PUT`/`PATCH`/`DELETE` on a typed plugin route: `mutate({ params, body })`. On
 * success, the plugin's cached queries are refetched (and the member query too).
 */
export function useRouteMutation<
  N extends string,
  K extends RouteKeyOf<N, Exclude<RouteMethod, "GET">> & string,
>(
  plugin: N,
  route: K,
  options: RouteTarget & {
    onSuccess?: (result: RouteResponseOf<RoutesOf<N>[K]>) => void;
    /** Default true. */
    invalidate?: boolean;
  } = {},
): MutationResult<RouteInput<K, RoutesOf<N>[K]>, RouteResponseOf<RoutesOf<N>[K]>> & {
  instance: PortalPlugin | undefined;
} {
  type T = RouteResponseOf<RoutesOf<N>[K]>;
  type I = RouteInput<K, RoutesOf<N>[K]>;
  const { client, cache } = useCicord();
  const { instance } = usePortalPlugin(plugin, routeTarget(options));
  const server = useServerKey(options.server);
  const [state, setState] = useState<{
    status: MutationStatus;
    data: T | undefined;
    error: unknown;
  }>({ status: "idle", data: undefined, error: undefined });
  const optionsRef = useRef(options);
  optionsRef.current = options;

  const mutateAsync = useCallback(
    async (input: I): Promise<T> => {
      if (!instance || !server)
        throw new Error(`@cicord/react: the ${plugin} plugin is not installed on this server`);
      const { params, body } = input as LooseInput;
      setState({ status: "pending", data: undefined, error: undefined });
      try {
        const [method, path] = resolveRoute(route, params);
        const result = await client
          .plugin(server, instance.bot, instance.instance)
          .request<T>(method, path, body === undefined ? {} : { body });
        setState({ status: "success", data: result, error: undefined });
        const { invalidate = true, onSuccess } = optionsRef.current;
        if (invalidate) {
          void cache.invalidate(["cicord", "plugin", server, instance.bot, instance.instance]);
          void cache.invalidate(cicordKeys.member(server));
        }
        onSuccess?.(result);
        return result;
      } catch (error) {
        setState({ status: "error", data: undefined, error });
        throw error;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- depends on the identity of the plugin instance (bot, instance), not on the ref object callers recreate each render
    [client, cache, server, instance?.bot, instance?.instance, plugin, route],
  );

  return {
    ...state,
    instance,
    isPending: state.status === "pending",
    mutateAsync,
    mutate: useCallback(
      (input: I) => void mutateAsync(input).catch(() => undefined),
      [mutateAsync],
    ),
    reset: useCallback(() => setState({ status: "idle", data: undefined, error: undefined }), []),
  };
}
