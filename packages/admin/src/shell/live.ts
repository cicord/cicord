/**
 * Live refresh of the admin console: GET /api/v1/events change hints invalidate the
 * queries they concern, a new runtime version is reported, and the console falls back to
 * refreshing on focus and every 30 s when the stream is unavailable.
 */
import type { AdminEvent, EventStream } from "@cicord/client";
import { useCicord, type QueryKey } from "@cicord/react";
import { useEffect, useRef, useState } from "react";
import { keys } from "../api.ts";

/** live: connected; reconnecting: the stream dropped (runtime restarting, network). */
export type LiveConnection = "connecting" | "live" | "reconnecting";

export interface AdminLive {
  connection: LiveConnection;
  /** The runtime version, when it changed since the console was loaded. */
  updatedTo?: string;
}

/** How long a dropped stream may take to come back before it is shown. */
const RECONNECT_GRACE_MS = 4000;
/** Fallback refresh while the stream is down. */
const FALLBACK_REFRESH_MS = 30_000;
/** A tab hidden longer than this refreshes everything when it is shown again. */
const STALE_AFTER_HIDDEN_MS = 60_000;
/** Delay before reconnecting after the runtime said it was stopping. */
const RESTART_DELAY_MS = 2000;
/** Hints are coalesced: one refetch per query prefix every this many ms at most. */
const COALESCE_MS = 300;

/** The query prefixes a hint invalidates. */
export function prefixesFor(event: AdminEvent): QueryKey[] {
  switch (event.type) {
    case "hello":
      return [["admin"]]; // (re)connected: changes may have been missed meanwhile
    case "status":
      return [keys.status()];
    case "sync":
      return event.server
        ? [keys.status(), ["admin", "history"], keys.server(event.server)]
        : [keys.status(), ["admin", "history"]];
    case "plugins":
      return event.server ? [keys.status(), keys.plugins(event.server)] : [keys.status()];
    case "analytics":
      return event.server ? [keys.analytics(event.server)] : [];
    case "stopping":
    default:
      return [];
  }
}

export function useAdminLive(enabled = true): AdminLive {
  const { client, cache } = useCicord();
  const [connection, setConnection] = useState<LiveConnection>("connecting");
  const [updatedTo, setUpdatedTo] = useState<string>();
  const loadedVersion = useRef<string>(undefined);

  useEffect(() => {
    if (!enabled) return undefined;
    const pending = new Map<string, QueryKey>();
    let flushTimer: number | undefined;
    let graceTimer: number | undefined;
    let fallback: number | undefined;
    let live = false;
    let hiddenAt: number | undefined;

    const invalidate = (prefixes: QueryKey[]) => {
      for (const p of prefixes) pending.set(JSON.stringify(p), p);
      if (flushTimer !== undefined) return;
      flushTimer = window.setTimeout(() => {
        flushTimer = undefined;
        const all = [...pending.values()];
        pending.clear();
        // A prefix covers the longer ones: ["admin"] alone refetches everything.
        const kept = all.filter(
          (p) => !all.some((q) => q !== p && q.length < p.length && q.every((x, i) => p[i] === x)),
        );
        for (const p of kept) void cache.invalidate([...p]);
      }, COALESCE_MS);
    };

    const startFallback = () => {
      if (fallback !== undefined) return;
      fallback = window.setInterval(() => invalidate([keys.status()]), FALLBACK_REFRESH_MS);
    };
    const stopFallback = () => {
      if (fallback !== undefined) window.clearInterval(fallback);
      fallback = undefined;
    };

    let stream: EventStream | undefined;
    let restartTimer: number | undefined;
    let stopped = false;

    const lost = () => {
      live = false;
      startFallback();
      if (graceTimer !== undefined) return;
      graceTimer = window.setTimeout(() => {
        graceTimer = undefined;
        if (!live) setConnection("reconnecting");
      }, RECONNECT_GRACE_MS);
    };

    const connect = () => {
      stream = client.admin.subscribe(
        (event) => {
          if (event.type === "stopping") {
            // The runtime restarts (an update): reconnect on a fresh connection, as a
            // proxy may keep this one open.
            stream?.close();
            lost();
            restartTimer = window.setTimeout(() => {
              restartTimer = undefined;
              if (!stopped) connect();
            }, RESTART_DELAY_MS);
            return;
          }
          if (event.type === "hello") {
            if (loadedVersion.current === undefined) {
              loadedVersion.current = event.version;
              return; // first connection: the page just loaded its data
            }
            if (event.version && event.version !== loadedVersion.current)
              setUpdatedTo(event.version);
          }
          invalidate(prefixesFor(event));
        },
        {
          retryMs: 3000,
          // The runtime sends a keep-alive every 25 s: silence means a dead connection.
          idleTimeoutMs: 60_000,
          onOpen: () => {
            live = true;
            if (graceTimer !== undefined) window.clearTimeout(graceTimer);
            graceTimer = undefined;
            stopFallback();
            setConnection("live");
          },
          onError: (error) => {
            // Signed out or no access any more: stop (the pages report it).
            if (/HTTP 40[13]\b/.test(String(error))) {
              stream?.close();
              live = false;
              setConnection("reconnecting");
              startFallback();
              return;
            }
            lost();
          },
        },
      );
    };
    connect();

    const onVisibility = () => {
      if (document.visibilityState === "hidden") {
        hiddenAt = Date.now();
        return;
      }
      if (hiddenAt !== undefined && Date.now() - hiddenAt > STALE_AFTER_HIDDEN_MS && !live)
        invalidate([["admin"]]);
      hiddenAt = undefined;
    };
    const onOnline = () => invalidate([["admin"]]);
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("online", onOnline);

    return () => {
      stopped = true;
      stream?.close();
      if (restartTimer !== undefined) window.clearTimeout(restartTimer);
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("online", onOnline);
      if (flushTimer !== undefined) window.clearTimeout(flushTimer);
      if (graceTimer !== undefined) window.clearTimeout(graceTimer);
      stopFallback();
    };
  }, [client, cache, enabled]);

  return updatedTo === undefined ? { connection } : { connection, updatedTo };
}
