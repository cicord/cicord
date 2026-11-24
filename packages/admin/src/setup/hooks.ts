import { useCallback, useEffect, useRef, useState } from "react";

/** Pending / error state of a user action. */
export function useAction() {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const run = useCallback(async <T>(fn: () => Promise<T>): Promise<T | undefined> => {
    setPending(true);
    setError(null);
    try {
      return await fn();
    } catch (e) {
      setError(e);
      return undefined;
    } finally {
      setPending(false);
    }
  }, []);
  return { pending, error, run, setError };
}

/** Calls fn now and every `ms` while enabled (live detection, live checks). */
export function usePoll<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  ms: number,
  enabled = true,
): { data: T | undefined; error: unknown; refresh: () => void } {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<unknown>(null);
  const [tick, setTick] = useState(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;
  useEffect(() => {
    if (!enabled) return undefined;
    const ctrl = new AbortController();
    let timer: number | undefined;
    const once = async () => {
      try {
        const v = await fnRef.current(ctrl.signal);
        if (!ctrl.signal.aborted) {
          setData(v);
          setError(null);
        }
      } catch (e) {
        if (!ctrl.signal.aborted) setError(e);
      }
      if (!ctrl.signal.aborted) timer = window.setTimeout(() => void once(), ms);
    };
    void once();
    return () => {
      ctrl.abort();
      if (timer) window.clearTimeout(timer);
    };
  }, [ms, enabled, tick]);
  const refresh = useCallback(() => setTick((n) => n + 1), []);
  return { data, error, refresh };
}
