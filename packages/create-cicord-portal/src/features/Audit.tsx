import { CicordApiError } from "@cicord/client";
import { Button } from "@cicord/ui";
import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  Empty,
  Failure,
  Paging,
  Result,
  useCopy,
  useCursor,
  useDate,
  useRead,
  useScope,
} from "./shared.tsx";

const categories = [
  "joins",
  "leaves",
  "roles",
  "nicknames",
  "messageEdits",
  "messageDeletes",
  "channels",
  "threads",
  "expressions",
  "server",
  "bans",
  "invites",
  "voice",
  "automod",
  "moderation",
  "polls",
  "plugins",
];
type Filters = { q?: string; member?: string; channel?: string; category?: string };
interface Entry {
  id: string;
  at: number;
  category: string;
  text: string;
  excerpt?: string;
}
interface Page {
  items: Entry[];
  cursor?: string;
}
// The portal allows 120 requests a minute per member, in bursts of 60: two pages a second
// stay under it. A 429 or 503 waits for Retry-After and asks for the same page again.
const EXPORT_PAGE_GAP_MS = 500;
const EXPORT_RETRIES = 5;
const MAX_RETRY_WAIT_MS = 60_000;
function pause(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    const timer = window.setTimeout(() => {
      signal.removeEventListener("abort", stop);
      resolve();
    }, ms);
    const stop = () => {
      window.clearTimeout(timer);
      reject(signal.reason);
    };
    signal.addEventListener("abort", stop, { once: true });
  });
}
const retryable = (e: unknown): e is CicordApiError =>
  e instanceof CicordApiError && (e.status === 429 || e.status === 503);
function AuditExport({ filters }: { filters: Filters }) {
  const c = useCopy(),
    { api } = useScope(),
    active = useRef<AbortController | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(),
    [done, setDone] = useState(false),
    [progress, setProgress] = useState<{ entries: number; waiting: boolean }>();
  useEffect(
    () => () => {
      active.current?.abort();
    },
    [],
  );
  async function save() {
    if (active.current) return;
    const abort = new AbortController();
    active.current = abort;
    setBusy(true);
    setError(undefined);
    setDone(false);
    setProgress({ entries: 0, waiting: false });
    try {
      const chunks: BlobPart[] = ["["];
      const seen = new Set<string>();
      let cursor: string | undefined,
        first = true,
        entries = 0;
      do {
        let page: Page | undefined;
        for (let attempt = 0; !page; attempt++) {
          try {
            page = await api.get<Page>("/export", { ...filters, cursor }, { signal: abort.signal });
          } catch (e) {
            if (!retryable(e) || attempt >= EXPORT_RETRIES) throw e;
            setProgress({ entries, waiting: true });
            await pause(Math.min((e.retryAfter ?? 2) * 1000, MAX_RETRY_WAIT_MS), abort.signal);
            setProgress({ entries, waiting: false });
          }
        }
        if (abort.signal.aborted) return;
        for (const item of page.items) {
          chunks.push(
            (first ? "" : ",") +
              JSON.stringify({
                id: item.id,
                at: item.at,
                category: item.category,
                text: item.text,
                ...(item.excerpt ? { excerpt: item.excerpt } : {}),
              }),
          );
          first = false;
        }
        entries += page.items.length;
        setProgress({ entries, waiting: false });
        cursor = page.cursor;
        if (cursor) {
          if (seen.has(cursor)) throw new Error("invalid export cursor");
          seen.add(cursor);
          await pause(EXPORT_PAGE_GAP_MS, abort.signal);
        }
      } while (cursor);
      chunks.push("]");
      const url = URL.createObjectURL(new Blob(chunks, { type: "application/json" }));
      const link = document.createElement("a");
      link.href = url;
      link.download = "audit-log.json";
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
      setDone(true);
    } catch (e) {
      if (!abort.signal.aborted) setError(e);
    } finally {
      if (!abort.signal.aborted) setBusy(false);
      active.current = null;
    }
  }
  return (
    <>
      <Button variant="secondary" disabled={busy} onClick={() => void save()}>
        {busy
          ? c("Preparing export…", "Préparation de l’export…")
          : c("Export matching entries", "Exporter les entrées filtrées")}
      </Button>
      {busy && progress ? (
        <span role="status">
          {progress.waiting
            ? c(
                `${progress.entries} entries so far. The server asked to slow down: retrying shortly…`,
                `${progress.entries} entrées pour l’instant. Le serveur demande de ralentir : nouvel essai sous peu…`,
              )
            : c(
                `${progress.entries} entries so far…`,
                `${progress.entries} entrées pour l’instant…`,
              )}
        </span>
      ) : null}
      {done ? <span role="status">{c("Export ready.", "Export prêt.")}</span> : null}
      {error ? <Failure error={error} /> : null}
    </>
  );
}
export function Audit() {
  const c = useCopy(),
    date = useDate(),
    { plugin } = useScope(),
    page = useCursor();
  const [filters, setFilters] = useState<Filters>({});
  const read = useRead<Page>("/search", { ...filters, cursor: page.value });
  function filter(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget),
      next: Filters = {};
    for (const key of ["q", "member", "category", "channel"] as const) {
      const v = f.get(key);
      if (typeof v === "string" && v.trim()) next[key] = v.trim();
    }
    page.reset();
    setFilters(next);
  }
  return (
    <>
      <form className="feature-form" onSubmit={filter}>
        <label>
          {c("Search audit entries", "Rechercher dans le journal")}
          <input type="search" name="q" />
        </label>
        <div className="feature-form feature-filters">
          <label>
            {c("Member ID", "Identifiant du membre")}
            <input name="member" pattern="[0-9]{1,20}" />
          </label>
          <label>
            {c("Channel ID", "Identifiant du salon")}
            <input name="channel" pattern="[0-9]{1,20}" />
          </label>
          <label>
            {c("Event type", "Type d’événement")}
            <select name="category">
              <option value="">{c("All events", "Tous les événements")}</option>
              {categories.map((k) => (
                <option key={k}>{k}</option>
              ))}
            </select>
          </label>
        </div>
        <Button type="submit">{c("Search", "Rechercher")}</Button>
      </form>
      <Result read={read}>
        {(d) => (
          <>
            {plugin.routes.includes("GET /export") ? (
              <AuditExport key={JSON.stringify(filters)} filters={filters} />
            ) : null}
            {d.items.length ? (
              <ul className="feature-list">
                {d.items.map((e) => (
                  <li key={e.id}>
                    <h2>{e.category}</h2>
                    <time>{date(e.at)}</time>
                    <p>{e.text}</p>
                    {e.excerpt ? <blockquote>{e.excerpt}</blockquote> : null}
                  </li>
                ))}
              </ul>
            ) : (
              <Empty />
            )}
            <Paging previous={page.back} next={d.cursor ? () => page.next(d.cursor!) : undefined} />
          </>
        )}
      </Result>
    </>
  );
}
