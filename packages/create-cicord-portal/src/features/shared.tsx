import { CicordApiError, type PluginRoutes, type PortalPlugin, type Query } from "@cicord/client";
import { Button, Notice, Spinner } from "@cicord/ui";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useT } from "../i18n/index.tsx";
import { Link } from "../router.tsx";
import { featureUrl } from "./catalog.ts";

export const Scope = createContext<{
  api: PluginRoutes;
  plugin: PortalPlugin;
  viewer: string;
  staff: boolean;
} | null>(null);
export function useScope() {
  const s = useContext(Scope);
  if (!s) throw new Error("missing plugin scope");
  return s;
}
export function useCopy() {
  const fr = useT().locale.startsWith("fr");
  return (en: string, french: string) => (fr ? french : en);
}
export function FeatureLink({ path, children }: { path: string; children: ReactNode }) {
  const { api, plugin } = useScope();
  return <Link to={featureUrl(path, api.server, plugin)}>{children}</Link>;
}
export function useDate() {
  const locale = useT().locale;
  return (value: string | number | null | undefined) => {
    if (value == null) return "—";
    const d = new Date(value);
    return Number.isNaN(d.getTime())
      ? "—"
      : new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(d);
  };
}
export function Failure({ error, retry }: { error: unknown; retry?: () => void }) {
  const c = useCopy();
  const status = error instanceof CicordApiError ? error.status : undefined;
  const msg =
    status === 403
      ? c(
          "You no longer have access to this information.",
          "Vous n’avez pas accès à ces informations.",
        )
      : status === 401
        ? c("Your session has expired. Sign in again.", "Votre session a expiré. Reconnectez-vous.")
        : status === 404
          ? c("This item is unavailable.", "Cet élément est indisponible.")
          : status === 400 || status === 422
            ? c("Check the values and try again.", "Vérifiez les valeurs et réessayez.")
            : status === 429
              ? c("Too many attempts. Try again later.", "Trop de tentatives. Réessayez plus tard.")
              : c("The request could not be completed.", "La requête n’a pas abouti.");
  return (
    <Notice
      tone="error"
      title={msg}
      action={
        retry ? (
          <Button size="sm" variant="secondary" onClick={retry}>
            {c("Retry", "Réessayer")}
          </Button>
        ) : undefined
      }
    />
  );
}
type Snapshot<T> = { key: string; data: T } | { key: string; error: unknown };
// The caller supplies the response contract of a discovered endpoint.
// eslint-disable-next-line typescript/no-unnecessary-type-parameters
/**
 * Reads every page before showing anything: `cursor` gives the next page's `?cursor=`
 * (undefined on the last one) and `merge` folds a page into what was read so far.
 */
export interface AllPages<T> {
  cursor: (page: T) => string | undefined;
  merge: (all: T, page: T) => T;
}
const MAX_PAGES = 1000;
export function useRead<T>(path: string | undefined, query?: Query, pages?: AllPages<T>) {
  const { api } = useScope();
  const key = JSON.stringify([path, query]);
  const [revision, refresh] = useState(0);
  const identity = key + "#" + revision;
  const [snapshot, set] = useState<Snapshot<T>>();
  const paging = useRef(pages);
  paging.current = pages;
  useEffect(() => {
    if (!path) return undefined;
    const abort = new AbortController();
    const [, params] = JSON.parse(key) as [string, Query | undefined];
    const load = async () => {
      try {
        let data: T = await api.get<T>(path, params, { signal: abort.signal });
        const all = paging.current;
        const seen = new Set<string>();
        if (all) {
          let cursor = all.cursor(data);
          while (cursor !== undefined) {
            // A cursor that repeats, or an endless list, is an error rather than a hang.
            if (seen.has(cursor) || seen.size >= MAX_PAGES)
              throw new Error("pagination did not end");
            seen.add(cursor);
            const page: T = await api.get<T>(path, { ...params, cursor }, { signal: abort.signal });
            data = all.merge(data, page);
            cursor = all.cursor(page);
          }
        }
        if (!abort.signal.aborted) set({ key: identity, data });
      } catch (error) {
        if (!abort.signal.aborted) set({ key: identity, error });
      }
    };
    void load();
    return () => abort.abort();
  }, [api, path, key, identity]);
  const reload = useCallback(() => refresh((n) => n + 1), []);
  return { snapshot: path && snapshot?.key === identity ? snapshot : undefined, reload };
}
export function Result<T>({
  read,
  children,
}: {
  read: ReturnType<typeof useRead<T>>;
  children: (data: T) => ReactNode;
}) {
  const t = useT();
  if (!read.snapshot) return <Spinner label={t.loading} />;
  if ("error" in read.snapshot) return <Failure error={read.snapshot.error} retry={read.reload} />;
  return <>{children(read.snapshot.data)}</>;
}
export function useWrite() {
  const { api } = useScope();
  const controller = useRef<AbortController | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [done, setDone] = useState(false);
  useEffect(
    () => () => {
      controller.current?.abort();
    },
    [],
  );
  async function run<T>(
    method: string,
    path: string,
    body: unknown,
    onSuccess?: (result: T) => void,
  ): Promise<T | undefined> {
    if (controller.current) return undefined;
    const abort = new AbortController();
    controller.current = abort;
    setPending(true);
    setDone(false);
    setError(undefined);
    try {
      const val = await api.request<T>(method, path, {
        ...(body === undefined ? {} : { body }),
        signal: abort.signal,
      });
      if (!abort.signal.aborted) {
        setDone(true);
        onSuccess?.(val);
        return val;
      }
    } catch (e) {
      if (!abort.signal.aborted) setError(e);
    } finally {
      if (!abort.signal.aborted) setPending(false);
      controller.current = null;
    }
    return undefined;
  }
  return { run, pending, error, done };
}
export function Outcome({
  write,
  message,
}: {
  write: ReturnType<typeof useWrite>;
  message?: string;
}) {
  const c = useCopy();
  if (write.error) return <Failure error={write.error} />;
  return (
    <span role="status">
      {write.pending
        ? c("Saving…", "Enregistrement…")
        : write.done
          ? (message ?? c("Saved.", "Enregistré."))
          : ""}
    </span>
  );
}
export function Empty() {
  const c = useCopy();
  return <p className="feature-muted">{c("No entries.", "Aucune entrée.")}</p>;
}
export function useCursor() {
  const [stack, set] = useState<(string | undefined)[]>([undefined]);
  return {
    value: stack.at(-1),
    back: stack.length > 1 ? () => set(stack.slice(0, -1)) : undefined,
    next: (v: string) => set([...stack, v]),
    reset: () => set([undefined]),
  };
}
export function Paging({ previous, next }: { previous?: () => void; next?: () => void }) {
  const c = useCopy();
  return previous || next ? (
    <div className="feature-actions">
      {previous ? (
        <Button variant="secondary" onClick={previous}>
          {c("Previous", "Précédent")}
        </Button>
      ) : null}
      {next ? (
        <Button variant="secondary" onClick={next}>
          {c("Next", "Suivant")}
        </Button>
      ) : null}
    </div>
  ) : null;
}
