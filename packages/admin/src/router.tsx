/** Minimal History API router aware of the /admin/ base path. */
import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";

const EVENT = "cicord-admin:navigate";
const BASE = import.meta.env.BASE_URL.replace(/\/$/, "");

function subscribe(listener: () => void): () => void {
  window.addEventListener("popstate", listener);
  window.addEventListener(EVENT, listener);
  return () => {
    window.removeEventListener("popstate", listener);
    window.removeEventListener(EVENT, listener);
  };
}

/** App path without the base ("/admin/servers/x" → "/servers/x"). */
function appPath(pathname: string): string {
  const rest = BASE && pathname.startsWith(BASE) ? pathname.slice(BASE.length) : pathname;
  return rest === "" ? "/" : rest;
}

export function href(to: string): string {
  return `${BASE}${to.startsWith("/") ? to : `/${to}`}`;
}

const snapshot = () => `${window.location.pathname}${window.location.search}`;

export function useLocation(): { path: string; search: URLSearchParams } {
  const raw = useSyncExternalStore(subscribe, snapshot, () => "/");
  const url = new URL(raw, "http://x");
  return { path: appPath(url.pathname), search: url.searchParams };
}

export function navigate(to: string, options: { replace?: boolean; keepScroll?: boolean } = {}) {
  const target = href(to);
  if (target === snapshot()) return;
  if (options.replace) window.history.replaceState(null, "", target);
  else window.history.pushState(null, "", target);
  window.dispatchEvent(new Event(EVENT));
  if (!options.keepScroll) window.scrollTo({ top: 0 });
}

/** Matches "/servers/:key/plan" against a path; returns the params or null. */
export function match(pattern: string, path: string): Record<string, string> | null {
  const p = pattern.split("/").filter(Boolean);
  const s = path.split("/").filter(Boolean);
  if (p.length !== s.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < p.length; i++) {
    const part = p[i]!;
    if (part.startsWith(":")) params[part.slice(1)] = decodeURIComponent(s[i]!);
    else if (part !== s[i]) return null;
  }
  return params;
}

export interface LinkProps extends AnchorHTMLAttributes<HTMLAnchorElement> {
  to: string;
  keepScroll?: boolean;
}

export function Link({ to, onClick, keepScroll, ...rest }: LinkProps) {
  const handle = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event);
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    navigate(to, { keepScroll: keepScroll ?? false });
  };
  return <a {...rest} href={href(to)} onClick={handle} />;
}
