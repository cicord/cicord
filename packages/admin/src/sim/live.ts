/** Keeps simulator queries fresh: SSE change hints, with polling as a fallback. */
import { useCicord } from "@cicord/react";
import { useEffect, useRef, useState } from "react";
import { keys } from "../api.ts";

export type LiveMode = "connecting" | "live" | "polling";

export function useSimLive(enabled = true): LiveMode {
  const { client, cache } = useCicord();
  const [mode, setMode] = useState<LiveMode>("connecting");
  const failures = useRef(0);

  useEffect(() => {
    if (!enabled) return;
    let timer: number | undefined;
    let poll: number | undefined;
    let opened = false;
    const refresh = () => {
      if (timer !== undefined) return;
      // Coalesce bursts of change events into one refetch.
      timer = window.setTimeout(() => {
        timer = undefined;
        void cache.invalidate(keys.sim());
      }, 60);
    };
    const startPolling = () => {
      if (poll !== undefined) return;
      setMode("polling");
      poll = window.setInterval(refresh, 2500);
    };
    const stream = client.sim.subscribe(refresh, {
      onOpen: () => {
        opened = true;
        failures.current = 0;
        setMode("live");
        if (poll !== undefined) {
          window.clearInterval(poll);
          poll = undefined;
        }
      },
      onError: () => {
        failures.current++;
        if (failures.current >= 2) startPolling();
      },
      retryMs: 3000,
    });
    // No SSE after a few seconds (proxies that buffer): poll meanwhile.
    const watchdog = window.setTimeout(() => {
      if (!opened) startPolling();
    }, 4000);
    return () => {
      stream.close();
      window.clearTimeout(watchdog);
      if (timer !== undefined) window.clearTimeout(timer);
      if (poll !== undefined) window.clearInterval(poll);
    };
  }, [client, cache, enabled]);

  return mode;
}
