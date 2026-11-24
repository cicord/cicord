import type { CicordClient } from "@cicord/client";
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { QueryCache } from "./cache.ts";

export interface CicordContextValue {
  client: CicordClient;
  /** Default server key used by hooks when none is passed. */
  server: string | undefined;
  cache: QueryCache;
}

const CicordContext = createContext<CicordContextValue | null>(null);

export interface CicordProviderProps {
  client: CicordClient;
  /** Server key (servers/<key>.yaml) used by hooks by default. */
  server?: string | undefined;
  /** Share a cache between providers (tests, micro-frontends). */
  cache?: QueryCache;
  children?: ReactNode;
}

export function CicordProvider({ client, server, cache, children }: CicordProviderProps) {
  const [ownCache] = useState(() => new QueryCache());
  const store = cache ?? ownCache;
  // Token mode: a token that expires or is revoked (401) logs the viewer out everywhere.
  useEffect(
    () =>
      client.onSessionChange(() => {
        if (!client.session()) {
          store.setData(["cicord", "me"], null);
          void store.invalidate(["cicord", "portal"]);
          void store.invalidate(["cicord", "plugin"]);
        }
      }),
    [client, store],
  );
  const value = useMemo<CicordContextValue>(
    () => ({ client, server, cache: store }),
    [client, server, store],
  );
  return <CicordContext.Provider value={value}>{children}</CicordContext.Provider>;
}

export function useCicord(): CicordContextValue {
  const value = useContext(CicordContext);
  if (!value) throw new Error("@cicord/react: wrap your app in <CicordProvider client={…}>");
  return value;
}

export function useCicordClient(): CicordClient {
  return useCicord().client;
}

/** The given server key, or the provider's (undefined while unknown: hooks then wait). */
export function useServerKey(server?: string): string | undefined {
  const fallback = useCicord().server;
  return server ?? fallback;
}
