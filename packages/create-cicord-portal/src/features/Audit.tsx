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
function AuditExport({ filters }: { filters: Filters }) {
  const c = useCopy(),
    { api } = useScope(),
    active = useRef<AbortController | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(),
    [done, setDone] = useState(false);
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
    try {
      const chunks: BlobPart[] = ["["];
      const seen = new Set<string>();
      let cursor: string | undefined,
        first = true;
      do {
        const page: Page = await api.get<Page>(
          "/export",
          { ...filters, cursor },
          { signal: abort.signal },
        );
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
        cursor = page.cursor;
        if (cursor) {
          if (seen.has(cursor)) throw new Error("invalid export cursor");
          seen.add(cursor);
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
