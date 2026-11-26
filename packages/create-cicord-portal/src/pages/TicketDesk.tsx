import type { PortalPlugin } from "@cicord/client";
import { useCicord, usePluginQuery } from "@cicord/react";
import { Button, Notice, Spinner } from "@cicord/ui";
import { useEffect, useState } from "react";
import { useT } from "../i18n/index.tsx";
import { useServer } from "../portal.tsx";

interface Item {
  id: string;
  subject: string;
  status: "open" | "closed";
  type: string;
  createdAt: string;
}
interface Person {
  id: string;
  displayName: string;
}
interface Detail extends Item {
  description: string;
  assignee: { id?: string; displayName: string } | null;
  rating: number | null;
  notes?: { by: Person; at: string; content: string }[];
  messages?:
    | { id: string; author: Person; content: string; embeds: { description: string | null }[] }[]
    | null;
}

export function TicketDesk({ plugin }: { plugin: PortalPlugin }) {
  const t = useT();
  const q = t.tickets;
  const staff = usePluginQuery<{ items: Item[] }>(plugin, "/tickets");
  const mine = usePluginQuery<{ items: Item[] }>(plugin, "/tickets/mine");
  const [tab, setTab] = useState<"mine" | "staff">("staff");
  const [selected, select] = useState(
    () => new URLSearchParams(location.search).get("ticket") ?? "",
  );
  const [creating, setCreating] = useState(false);
  const [filter, setFilter] = useState("all");
  const queue = tab === "staff" && !staff.error;
  const list = queue ? staff : mine;
  const stats = usePluginQuery<{ rated: number; mean: number | null }>(
    queue && staff.data ? plugin : undefined,
    "/tickets/stats",
  );
  const items = list.data?.items.filter((i) => filter === "all" || i.status === filter) ?? [];
  const activeStaff = queue && !!staff.data?.items.some((item) => item.id === selected);
  return (
    <div className="page tickets">
      <header className="page__header">
        <h1>{q.title}</h1>
      </header>
      <div className="ticket-desk__tools">
        {staff.data && !staff.error && (
          <div className="segmented">
            {(["staff", "mine"] as const).map((v) => (
              <button
                type="button"
                key={v}
                className="segmented__item"
                aria-pressed={tab === v}
                onClick={() => {
                  setTab(v);
                  select("");
                }}
              >
                {v === "staff" ? q.team : q.mine}
              </button>
            ))}
          </div>
        )}
        <label>
          {q.filter}{" "}
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            {(["all", "open", "closed"] as const).map((k) => (
              <option key={k} value={k}>
                {q[k]}
              </option>
            ))}
          </select>
        </label>
      </div>
      <div>
        <Button variant="secondary" aria-expanded={creating} onClick={() => setCreating(!creating)}>
          {q.create}
        </Button>
      </div>
      {creating && (
        <NewTicket
          plugin={plugin}
          onCreated={(id) => {
            setTab("mine");
            setFilter("all");
            select(id);
            setCreating(false);
            void mine.refetch();
            void staff.refetch();
          }}
        />
      )}
      {queue && stats.data?.mean != null && (
        <p>
          {q.rating} · {stats.data.mean.toFixed(1)}/5 ({stats.data.rated})
        </p>
      )}
      {list.error ? (
        <Notice tone="error">{q.failed}</Notice>
      ) : !list.data ? (
        <Spinner label={t.loading} />
      ) : items.length === 0 ? (
        <Notice>{q.empty}</Notice>
      ) : (
        <ul className="ticket-list">
          {items.map((i) => (
            <li key={i.id} className="ticket">
              <span className="ticket__id">#{i.id}</span>
              <button
                className="ticket__select"
                type="button"
                aria-expanded={selected === i.id}
                onClick={() => select(selected === i.id ? "" : i.id)}
              >
                {i.subject}
                <small>{i.type}</small>
              </button>
              <span className={`ticket__status ticket__status--${i.status}`}>
                {q.status[i.status]}
              </span>
            </li>
          ))}
        </ul>
      )}
      {selected && /^\d+$/.test(selected) && list.data && (
        <TicketDetail
          key={selected + activeStaff}
          id={selected}
          plugin={plugin}
          staff={activeStaff}
        />
      )}
    </div>
  );
}

function TicketDetail({ plugin, id, staff }: { plugin: PortalPlugin; id: string; staff: boolean }) {
  const t = useT(),
    q = t.tickets;
  const { client } = useCicord();
  const server = useServer();
  const api = () => client.plugin(server.key, plugin.bot, plugin.instance);
  const path = "/tickets/" + id;
  const [ticket, setTicket] = useState<Detail>();
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [revision, reload] = useState(0);
  const [message, setMessage] = useState("");
  const [mode, setMode] = useState("reply");
  const [history, setHistory] =
    useState<{ number: number; action: string; status: string; reason: string }[]>();
  const [historyBusy, setHistoryBusy] = useState(false);
  async function loadHistory() {
    setHistory(undefined);
    setHistoryBusy(true);
    try {
      const data = await api().get<{
        cases: { number: number; action: string; status: string; reason: string }[];
      }>(path + "/cases");
      setHistory(data.cases);
    } catch {
      setTicket(undefined);
      setError(true);
    } finally {
      setHistoryBusy(false);
    }
  }
  const assignees = usePluginQuery<{ items: Person[] }>(
    staff && ticket ? plugin : undefined,
    "/staff",
    { query: { type: ticket?.type } },
  );
  useEffect(() => {
    const controller = new AbortController();
    const ref = client.plugin(server.key, plugin.bot, plugin.instance);
    setTicket(undefined);
    setError(false);
    async function load() {
      try {
        const detail = await ref.get<Detail>(staff ? path : "/tickets/mine/" + id, undefined, {
          signal: controller.signal,
        });
        if (!controller.signal.aborted) setTicket(detail);
      } catch {
        if (!controller.signal.aborted) setError(true);
      }
    }
    void load();
    return () => controller.abort();
  }, [client, server.key, plugin.bot, plugin.instance, id, staff, path, revision]);

  async function post(suffix: string, body: Record<string, string | number | null>) {
    setBusy(true);
    setError(false);
    try {
      await api().request("POST", path + suffix, { body });
      setMessage("");
      reload((n) => n + 1);
    } catch {
      setTicket(undefined);
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  async function download(format: "html" | "markdown") {
    setBusy(true);
    setError(false);
    try {
      const data = await api().get<{ content: string }>(path + "/transcript", { format });
      const href = URL.createObjectURL(
        new Blob([data.content], { type: format === "html" ? "text/html" : "text/markdown" }),
      );
      const link = document.createElement("a");
      link.href = href;
      link.download = `ticket-${id}.${format === "html" ? "html" : "md"}`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(href), 1000);
    } catch {
      setTicket(undefined);
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  if (error)
    return (
      <Notice
        tone="error"
        action={<Button onClick={() => reload((n) => n + 1)}>{t.onboarding.retry}</Button>}
      >
        {q.failed}
      </Notice>
    );
  if (!ticket) return <Spinner label={t.loading} />;
  return (
    <section className="ticket-detail" aria-labelledby="ticket-title">
      <h2 id="ticket-title">
        #{id} · {ticket.subject}
      </h2>
      <p>{ticket.description}</p>
      {ticket.messages?.map((m) => (
        <p key={m.id}>
          <strong>{m.author.displayName}</strong>{" "}
          {m.content || m.embeds.map((e) => e.description).join(" ")}
        </p>
      ))}
      {staff && (
        <>
          <label>
            {q.assigned}{" "}
            <select
              disabled={busy}
              value={ticket.assignee?.id ?? ""}
              onChange={(e) => void post("/assign", { userId: e.target.value || null })}
            >
              <option value="">{q.unassigned}</option>
              {assignees.data?.items.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.displayName}
                </option>
              ))}
            </select>
          </label>
          {plugin.routes.includes("GET /tickets/:id/cases") && (
            <details
              onToggle={(e) => {
                if (e.currentTarget.open) void loadHistory();
                else setHistory(undefined);
              }}
            >
              <summary>{q.cases}</summary>
              {historyBusy ? (
                <Spinner label={t.loading} />
              ) : history?.length ? (
                <ul>
                  {history.map((c) => (
                    <li key={c.number}>
                      <strong>
                        #{c.number} · {c.action}
                      </strong>{" "}
                      · {c.status}
                      <p>{c.reason}</p>
                    </li>
                  ))}
                </ul>
              ) : (
                <p>{q.noCases}</p>
              )}
            </details>
          )}
          {ticket.notes?.length ? (
            <section>
              <h3>{q.notes}</h3>
              {ticket.notes.map((n, i) => (
                <p key={i}>
                  <strong>{n.by.displayName}</strong> {n.content}
                </p>
              ))}
            </section>
          ) : null}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void post(mode === "note" ? "/notes" : "/messages", { content: message });
            }}
          >
            <label>
              {q.write}{" "}
              <select value={mode} onChange={(e) => setMode(e.target.value)}>
                <option value="reply">{q.reply}</option>
                <option value="note">{q.note}</option>
              </select>
            </label>
            <textarea
              aria-label={mode === "note" ? q.note : q.reply}
              value={message}
              maxLength={2000}
              onChange={(e) => setMessage(e.target.value)}
              required
            />
            <Button
              type="submit"
              disabled={busy || !message.trim() || (mode === "reply" && ticket.status === "closed")}
            >
              {mode === "note" ? q.saveNote : q.send}
            </Button>
          </form>
        </>
      )}
      {ticket.status === "closed" && (
        <div className="ticket-desk__tools">
          <strong>{q.transcript}</strong>
          {(["html", "markdown"] as const).map((f) => (
            <Button key={f} variant="secondary" disabled={busy} onClick={() => void download(f)}>
              {f === "html" ? "HTML" : "Markdown"}
            </Button>
          ))}
        </div>
      )}
      {!staff && ticket.status === "closed" && (
        <label>
          {q.rating}{" "}
          <select
            disabled={busy || !!ticket.rating}
            value={ticket.rating ?? ""}
            onChange={(e) => void post("/rating", { rating: Number(e.target.value) })}
          >
            <option value="">—</option>
            {[1, 2, 3, 4, 5].map((n) => (
              <option key={n} value={n}>
                {n}/5
              </option>
            ))}
          </select>
        </label>
      )}
    </section>
  );
}

function NewTicket({
  plugin,
  onCreated,
}: {
  plugin: PortalPlugin;
  onCreated: (id: string) => void;
}) {
  const q = useT().tickets;
  const types = usePluginQuery<{ items: { key: string; name: string }[] }>(plugin, "/types");
  const [type, setType] = useState("");
  const [subject, setSubject] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const { client } = useCicord(),
    server = useServer();
  async function submit() {
    setBusy(true);
    setError(false);
    try {
      const result = await client
        .plugin(server.key, plugin.bot, plugin.instance)
        .request<Item>("POST", "/tickets", {
          body: { subject, description, type: type || types.data?.items[0]?.key },
        });
      onCreated(result.id);
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className="ticket-detail ticket-compose"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      {error && <Notice tone="error">{q.createFailed}</Notice>}
      <label>
        {q.type}{" "}
        <select
          value={type || types.data?.items[0]?.key || ""}
          onChange={(e) => setType(e.target.value)}
        >
          {types.data?.items.map((t) => (
            <option key={t.key} value={t.key}>
              {t.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        {q.subject}
        <input
          value={subject}
          onChange={(e) => setSubject(e.target.value)}
          minLength={3}
          maxLength={100}
          required
        />
      </label>
      <label>
        {q.details}
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          minLength={10}
          maxLength={1000}
          required
        />
      </label>
      <Button type="submit" disabled={busy || !types.data?.items.length}>
        {q.create}
      </Button>
    </form>
  );
}
