import {
  CodiApiError,
  type Json,
  type Me,
  type PortalMe,
  type PortalPlugin,
  type PortalRole,
  type PortalServer,
  type Query,
} from "@codi/client";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { hashKey, type QueryKey, type QueryState, type QueryStatus } from "./cache.ts";
import { useCodi, useServerKey } from "./provider.tsx";

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
export function useCodiQuery<T>(
  key: QueryKey | null,
  fn: (signal: AbortSignal) => Promise<T>,
  options: QueryOptions = {},
): QueryResult<T> {
  const { cache } = useCodi();
  const enabled = key !== null && (options.enabled ?? true);
  const hash = key ? hashKey(key) : "";
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const keyRef = useRef(key);
  keyRef.current = key;

  const subscribe = useCallback(
    (listener: () => void) =>
      keyRef.current ? cache.subscribe(keyRef.current, listener) : () => {},
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [cache, hash],
  );
  const getSnapshot = useCallback(
    (): QueryState<T> | undefined =>
      keyRef.current ? cache.getState<T>(keyRef.current) : undefined,
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

export const codiKeys = {
  me: () => ["codi", "me"] as const,
  portalServers: () => ["codi", "portal", "servers"] as const,
  portalServer: (server: string) => ["codi", "portal", server] as const,
  member: (server: string) => ["codi", "portal", server, "me"] as const,
  plugin: (server: string, bot: string, instance: string, path: string, query?: Query) =>
    ["codi", "plugin", server, bot, instance, path, query ? JSON.stringify(query) : null] as const,
};

const nullOn401 = async <T>(promise: Promise<T>): Promise<T | null> => {
  try {
    return await promise;
  } catch (error) {
    if (error instanceof CodiApiError && error.code === "unauthenticated") return null;
    throw error;
  }
};

// ───────────────────────────────────────────────────────────── auth

/** Current user (`GET /me`); `data` is `null` when logged out. */
export function useMe(): QueryResult<Me | null> {
  const { client } = useCodi();
  return useCodiQuery(codiKeys.me(), (signal) => client.meOrNull({ signal }));
}

export type LinkTokenLoginState =
  | { status: "none" }
  | { status: "pending" }
  | { status: "success"; me: Me }
  | { status: "error"; error: unknown };

// One exchange per token and client, whatever the number of mounts (StrictMode, remounts).
const exchanges = new WeakMap<object, Map<string, Promise<Me | null>>>();

/**
 * Exchanges the `codi_token` of the current URL (signed link sent by a bot) for a session,
 * once. The token is removed from the address bar immediately. On success, the cached user
 * and member queries are refreshed.
 */
export function useLinkTokenLogin(options: { param?: string } = {}): LinkTokenLoginState {
  const { client, cache } = useCodi();
  const param = options.param ?? "codi_token";
  const [state, setState] = useState<LinkTokenLoginState>(() => {
    if (typeof location === "undefined") return { status: "none" };
    const token = new URL(location.href).searchParams.get(param);
    const inflight = token ? exchanges.get(client)?.get(token) : undefined;
    return token || inflight ? { status: "pending" } : { status: "none" };
  });
  const tokenRef = useRef<string | null>(
    typeof location === "undefined" ? null : new URL(location.href).searchParams.get(param),
  );

  useEffect(() => {
    const token = tokenRef.current;
    if (!token) return;
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
    promise.then(
      (me) => {
        if (!active) return;
        if (me) {
          cache.setData(codiKeys.me(), me);
          void cache.invalidate(["codi", "portal"]);
          void cache.invalidate(["codi", "plugin"]);
          setState({ status: "success", me });
        } else setState({ status: "none" });
      },
      (error: unknown) => {
        if (active) setState({ status: "error", error });
      },
    );
    return () => {
      active = false;
    };
  }, [client, cache, param]);

  return state;
}

/** Logs out and clears every cached query. */
export function useLogout(): () => Promise<void> {
  const { client, cache } = useCodi();
  return useCallback(async () => {
    await client.auth.logout();
    cache.clear();
    cache.setData(codiKeys.me(), null);
  }, [client, cache]);
}

// ───────────────────────────────────────────────────────────── portal

export function usePortalServers(): QueryResult<PortalServer[]> {
  const { client } = useCodi();
  return useCodiQuery(codiKeys.portalServers(), (signal) => client.portal.servers({ signal }));
}

/** `GET /portal/{server}` for the given (or provider's) server. */
export function usePortalServer(server?: string): QueryResult<PortalServer> {
  const { client } = useCodi();
  const key = useServerKey(server);
  return useCodiQuery(key ? codiKeys.portalServer(key) : null, (signal) =>
    client.portal.server(key as string, { signal }),
  );
}

/** The viewer as a member of the server; `data` is `null` when logged out. */
export function useMember(server?: string): QueryResult<PortalMe | null> {
  const { client } = useCodi();
  const key = useServerKey(server);
  return useCodiQuery(key ? codiKeys.member(key) : null, (signal) =>
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
  const { client } = useCodi();
  const server = useServerKey(ref?.server);
  const key =
    ref && server ? codiKeys.plugin(server, ref.bot, ref.instance, path, options.query) : null;
  return useCodiQuery<T>(
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
  const { client, cache } = useCodi();
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
        throw new Error("@codi/react: usePluginMutation called before the plugin is known");
      setState({ status: "pending", data: undefined, error: undefined });
      try {
        const { method = "POST", invalidate = true, onSuccess } = optionsRef.current;
        const result = await client
          .plugin(server, ref.bot, ref.instance)
          .request<TResult>(method, path, body === undefined ? {} : { body });
        setState({ status: "success", data: result, error: undefined });
        if (invalidate) {
          void cache.invalidate(["codi", "plugin", server, ref.bot, ref.instance]);
          void cache.invalidate(codiKeys.member(server));
        }
        onSuccess?.(result);
        return result;
      } catch (error) {
        setState({ status: "error", data: undefined, error });
        throw error;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
