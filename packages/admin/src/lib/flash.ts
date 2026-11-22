/**
 * State handed from a page to the next one, in memory (a reload drops it): one-shot messages
 * (an uninstall result shown on the table after leaving the deleted instance's page) and the
 * last URL of a table (the detail pages' back links return to the same page and search).
 */
import { useEffect, useState } from "react";

const store = new Map<string, unknown>();

export function setFlash<T>(key: string, value: T): void {
  store.set(key, value);
}

/** The message left for `key` (shown once: it is forgotten after the first render). */
export function useFlash<T>(key: string): [T | undefined, () => void] {
  const [value, setValue] = useState(() => store.get(key) as T | undefined);
  // Forget it after mounting (not in the initializer, which StrictMode calls twice).
  useEffect(() => {
    store.delete(key);
  }, [key]);
  return [value, () => setValue(undefined)];
}

const lists = new Map<string, string>();

/** Remembers where a list (table) was left: page, sort, search… */
export function rememberList(key: string, url: string): void {
  lists.set(key, url);
}

/** The list's last URL, to go back to it from a detail page. */
export function listUrl(key: string, fallback: string): string {
  return lists.get(key) ?? fallback;
}
