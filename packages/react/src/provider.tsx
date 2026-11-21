import type { CodiClient } from "@codi/client";
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { QueryCache } from "./cache.ts";

export interface CodiContextValue {
  client: CodiClient;
  /** Default server key used by hooks when none is passed. */
  server: string | undefined;
  cache: QueryCache;
}

const CodiContext = createContext<CodiContextValue | null>(null);

export interface CodiProviderProps {
  client: CodiClient;
  /** Server key (servers/<key>.yaml) used by hooks by default. */
  server?: string | undefined;
  /** Share a cache between providers (tests, micro-frontends). */
  cache?: QueryCache;
  children?: ReactNode;
}

export function CodiProvider({ client, server, cache, children }: CodiProviderProps) {
  const [ownCache] = useState(() => new QueryCache());
  const store = cache ?? ownCache;
  // Token mode: a token that expires or is revoked (401) logs the viewer out everywhere.
  useEffect(
    () =>
      client.onSessionChange(() => {
        if (!client.session()) {
          store.setData(["codi", "me"], null);
          void store.invalidate(["codi", "portal"]);
          void store.invalidate(["codi", "plugin"]);
        }
      }),
    [client, store],
  );
  const value = useMemo<CodiContextValue>(
    () => ({ client, server, cache: store }),
    [client, server, store],
  );
  return <CodiContext.Provider value={value}>{children}</CodiContext.Provider>;
}

export function useCodi(): CodiContextValue {
  const value = useContext(CodiContext);
  if (!value) throw new Error("@codi/react: wrap your app in <CodiProvider client={…}>");
  return value;
}

export function useCodiClient(): CodiClient {
  return useCodi().client;
}

/** The given server key, or the provider's (undefined while unknown: hooks then wait). */
export function useServerKey(server?: string): string | undefined {
  const fallback = useCodi().server;
  return server ?? fallback;
}
