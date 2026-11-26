import { Button, Markdown, Notice } from "@cicord/ui";
import { useState, type FormEvent } from "react";
import {
  Empty,
  Failure,
  Result,
  useCopy,
  useDate,
  useRead,
  useScope,
  useWrite,
} from "./shared.tsx";

interface Preview {
  content?: string;
  embeds?: { title?: string; description?: string }[];
  layout?: { type: string; content?: string; components?: { content?: string }[] }[];
}
interface Scheduled {
  timeZone: string;
  recurring: {
    name: string;
    channel: string;
    enabled: boolean;
    next: string | null;
    error: string | null;
  }[];
  scheduled: { id: number; channelId: string; at: string; title: string | null; preview: string }[];
}
function Agenda({ data }: { data: Scheduled }) {
  const c = useCopy(),
    date = useDate();
  return (
    <section className="feature-section">
      <h2>{c("Scheduled announcements", "Annonces programmées")}</h2>
      {data.scheduled.length || data.recurring.length ? (
        <ul className="feature-list">
          {data.scheduled.map((a) => (
            <li key={a.id}>
              <strong>{a.title ?? a.preview}</strong>
              <div className="feature-meta">
                <time dateTime={a.at}>{date(a.at)}</time>
                <span>#{a.channelId}</span>
              </div>
            </li>
          ))}
          {data.recurring.map((a) => (
            <li key={a.name}>
              <strong>{a.name}</strong>
              <div className="feature-meta">
                <span>#{a.channel}</span>
                <span>{a.enabled ? (a.next ? date(a.next) : "—") : c("Paused", "En pause")}</span>
              </div>
              {a.error ? <p>{a.error}</p> : null}
            </li>
          ))}
        </ul>
      ) : (
        <Empty />
      )}
    </section>
  );
}
export function Announcements() {
  const c = useCopy(),
    { plugin } = useScope(),
    preview = useWrite(),
    publish = useWrite();
  const [channel, setChannel] = useState(""),
    [title, setTitle] = useState(""),
    [text, setText] = useState(""),
    [at, setAt] = useState(""),
    [layout, setLayout] = useState(false);
  const [rendered, setRendered] = useState<{ key: string; message: Preview }>(),
    [sent, setSent] = useState<number>();
  const agenda = useRead<Scheduled>(
    plugin.routes.includes("GET /scheduled") ? "/scheduled" : undefined,
  );
  const key = JSON.stringify([channel, title, text, at, layout]);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!text.trim()) return;
    const req = {
      channel: channel.trim(),
      text: text.trim(),
      ...(title.trim() ? { title: title.trim() } : {}),
      ...(at ? { at: new Date(at).toISOString() } : {}),
      layout,
    };
    const button = (e.nativeEvent as SubmitEvent).submitter as HTMLButtonElement | null;
    if (button?.value === "preview") {
      await preview.run<{ message: Preview }>("POST", "/preview", req, (r) =>
        setRendered({ key, message: r.message }),
      );
    } else
      await publish.run<{ id: number }>("POST", "/compose", req, (r) => {
        setSent(r.id);
        setTitle("");
        setText("");
        setAt("");
        setRendered(undefined);
        agenda.reload();
      });
  }
  const m = rendered?.key === key ? rendered.message : undefined;
  return (
    <>
      <form className="feature-form" onSubmit={(e) => void submit(e)}>
        <label>
          {c("Channel ID", "Identifiant du salon")}
          <input
            required
            pattern="[0-9]{1,20}"
            inputMode="numeric"
            value={channel}
            onChange={(e) => setChannel(e.target.value)}
          />
        </label>
        <label>
          {c("Title (optional)", "Titre (facultatif)")}
          <input maxLength={256} value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label>
          {c("Message", "Message")}
          <textarea
            rows={6}
            required
            maxLength={4000}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </label>
        <label>
          {c("Schedule for (local time, optional)", "Programmer pour (heure locale, facultatif)")}
          <input type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} />
        </label>
        <label className="feature-choice">
          <input type="checkbox" checked={layout} onChange={(e) => setLayout(e.target.checked)} />
          {c("Use Discord Components V2", "Utiliser les composants Discord V2")}
        </label>
        <div className="feature-actions">
          {plugin.routes.includes("POST /preview") ? (
            <Button
              variant="secondary"
              type="submit"
              value="preview"
              disabled={preview.pending || publish.pending}
            >
              {c("Preview", "Aperçu")}
            </Button>
          ) : null}
          <Button
            type="submit"
            value="publish"
            disabled={!text.trim() || preview.pending || publish.pending}
          >
            {at
              ? c("Schedule announcement", "Programmer l’annonce")
              : c("Publish announcement", "Publier l’annonce")}
          </Button>
        </div>
        {preview.error || publish.error ? <Failure error={preview.error ?? publish.error} /> : null}
        {sent !== undefined ? (
          <Notice title={c("Announcement queued", "Annonce mise en file") + " #" + sent} />
        ) : null}
      </form>
      {m ? (
        <section className="feature-section" aria-label={c("Message preview", "Aperçu du message")}>
          <h2>{c("Message preview", "Aperçu du message")}</h2>
          {m.content ? <Markdown source={m.content} /> : null}
          {m.embeds?.map((e, i) => (
            <div key={i}>
              {e.title ? <h3>{e.title}</h3> : null}
              {e.description ? <Markdown source={e.description} /> : null}
            </div>
          ))}
          {m.layout
            ?.flatMap((l) => [l.content, ...(l.components ?? []).map((x) => x.content)])
            .filter((s): s is string => !!s)
            .map((s, i) => (
              <Markdown key={i} source={s} />
            ))}
        </section>
      ) : null}
      {plugin.routes.includes("GET /scheduled") ? (
        <Result read={agenda}>{(d) => <Agenda data={d} />}</Result>
      ) : null}
    </>
  );
}
