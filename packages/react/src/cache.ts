/**
 * Tiny query cache (dedupes requests, shares results between components, invalidation by key
 * prefix). Deliberately small: portals that want more can use TanStack Query with the
 * framework-agnostic @cicord/client directly.
 */

export type QueryKey = readonly (string | number | boolean | null | undefined)[];

export type QueryStatus = "idle" | "loading" | "success" | "error";

export interface QueryState<T> {
  status: QueryStatus;
  data: T | undefined;
  error: unknown;
  /** A request is in flight (also true while refreshing existing data). */
  fetching: boolean;
  updatedAt: number;
}

interface Entry {
  state: QueryState<unknown>;
  promise: Promise<unknown> | undefined;
  controller: AbortController | undefined;
  listeners: Set<() => void>;
  fn: ((signal: AbortSignal) => Promise<unknown>) | undefined;
}

const IDLE: QueryState<never> = Object.freeze({
  status: "idle",
  data: undefined,
  error: undefined,
  fetching: false,
  updatedAt: 0,
}) as QueryState<never>;

export const hashKey = (key: QueryKey): string => JSON.stringify(key);

export class QueryCache {
  readonly #entries = new Map<string, Entry>();

  #entry(hash: string): Entry {
    let entry = this.#entries.get(hash);
    if (!entry) {
      entry = {
        state: IDLE,
        promise: undefined,
        controller: undefined,
        listeners: new Set(),
        fn: undefined,
      };
      this.#entries.set(hash, entry);
    }
    return entry;
  }

  #set(entry: Entry, state: QueryState<unknown>): void {
    entry.state = state;
    // A snapshot: listeners may subscribe or unsubscribe while being notified.
    for (const listener of Array.from(entry.listeners)) listener();
  }

  getState<T>(key: QueryKey): QueryState<T> {
    return (this.#entries.get(hashKey(key))?.state ?? IDLE) as QueryState<T>;
  }

  subscribe(key: QueryKey, listener: () => void): () => void {
    const entry = this.#entry(hashKey(key));
    entry.listeners.add(listener);
    return () => entry.listeners.delete(listener);
  }

  /** Fetches unless a request is in flight (or data exists and `force` is false). */
  fetch<T>(
    key: QueryKey,
    fn: (signal: AbortSignal) => Promise<T>,
    options: { force?: boolean } = {},
  ): Promise<T> {
    const entry = this.#entry(hashKey(key));
    entry.fn = fn as Entry["fn"];
    if (entry.promise) return entry.promise as Promise<T>;
    if (!options.force && entry.state.status === "success")
      return Promise.resolve(entry.state.data as T);

    const controller = new AbortController();
    entry.controller = controller;
    this.#set(entry, {
      ...entry.state,
      status: entry.state.status === "success" ? "success" : "loading",
      fetching: true,
    });
    const promise = fn(controller.signal).then(
      (data) => {
        if (entry.promise === promise) {
          entry.promise = undefined;
          this.#set(entry, {
            status: "success",
            data,
            error: undefined,
            fetching: false,
            updatedAt: Date.now(),
          });
        }
        return data;
      },
      (error: unknown) => {
        if (entry.promise === promise) {
          entry.promise = undefined;
          this.#set(entry, {
            ...entry.state,
            status: "error",
            error,
            fetching: false,
            updatedAt: Date.now(),
          });
        }
        throw error;
      },
    );
    entry.promise = promise;
    return promise;
  }

  setData<T>(key: QueryKey, data: T | ((previous: T | undefined) => T)): void {
    const entry = this.#entry(hashKey(key));
    const next =
      typeof data === "function"
        ? (data as (p: T | undefined) => T)(entry.state.data as T | undefined)
        : data;
    entry.controller?.abort();
    entry.promise = undefined;
    this.#set(entry, {
      status: "success",
      data: next,
      error: undefined,
      fetching: false,
      updatedAt: Date.now(),
    });
  }

  /**
   * Marks queries whose key starts with `prefix` as stale and refetches the ones that are
   * currently observed. Without a prefix, every query is invalidated.
   */
  invalidate(prefix: QueryKey = []): Promise<void> {
    const pending: Promise<unknown>[] = [];
    for (const [hash, entry] of this.#entries) {
      const key = JSON.parse(hash) as QueryKey;
      if (!prefix.every((part, i) => key[i] === part)) continue;
      entry.controller?.abort();
      entry.promise = undefined;
      if (entry.listeners.size > 0 && entry.fn) {
        pending.push(this.fetch(key, entry.fn, { force: true }).catch(() => undefined));
      } else {
        this.#entries.delete(hash);
      }
    }
    return Promise.all(pending).then(() => undefined);
  }

  clear(): void {
    for (const entry of this.#entries.values()) entry.controller?.abort();
    this.#entries.clear();
  }
}
