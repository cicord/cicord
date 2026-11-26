import { Button, Markdown } from "@cicord/ui";
import { useState } from "react";
import {
  Empty,
  Outcome,
  Paging,
  Result,
  useCopy,
  useCursor,
  useRead,
  useScope,
  useWrite,
} from "./shared.tsx";

interface Tag {
  editable?: boolean;
  name: string;
  text: string;
  aliases: string[];
  keywords: string[];
  channel?: string;
  url?: string;
}
const blank: Tag = { name: "", text: "", aliases: [], keywords: [] };
function TagEditor({
  initial,
  saved,
  cancel,
}: {
  initial: Tag;
  saved: () => void;
  cancel: () => void;
}) {
  const c = useCopy(),
    write = useWrite(),
    { plugin } = useScope();
  const [name, setName] = useState(initial.name),
    [text, setText] = useState(initial.text),
    [aliases, setAliases] = useState(initial.aliases.join(", ")),
    [keywords, setKeywords] = useState(initial.keywords.join(", ")),
    [channel, setChannel] = useState(initial.channel ?? ""),
    [link, setLink] = useState(initial.url ?? ""),
    [removing, setRemoving] = useState(false);
  const split = (v: string) =>
    v
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean);
  return (
    <form
      className="feature-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!text.trim()) return;
        void write.run(
          "PUT",
          "/tags/" + encodeURIComponent(name),
          {
            name,
            text: text.trim(),
            aliases: split(aliases),
            keywords: split(keywords),
            ...(channel ? { channel } : {}),
            ...(link ? { url: link } : {}),
          },
          saved,
        );
      }}
    >
      <h2>{initial.name ? c("Edit tag", "Modifier le tag") : c("Create tag", "Créer un tag")}</h2>
      <label>
        {c("Name", "Nom")}
        <input
          readOnly={!!initial.name}
          required
          maxLength={64}
          pattern="[A-Za-z0-9][A-Za-z0-9 _\-]*"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </label>
      <label>
        {c("Content", "Contenu")}
        <textarea
          required
          rows={5}
          maxLength={1800}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      </label>
      <label>
        {c("Aliases (comma-separated)", "Alias (séparés par des virgules)")}
        <input value={aliases} onChange={(e) => setAliases(e.target.value)} />
      </label>
      <label>
        {c("Keywords (comma-separated)", "Mots-clés (séparés par des virgules)")}
        <input value={keywords} onChange={(e) => setKeywords(e.target.value)} />
      </label>
      <label>
        {c("Channel key (optional)", "Clé du salon (facultatif)")}
        <input
          pattern="[a-z][a-z0-9\-]{0,62}"
          value={channel}
          onChange={(e) => setChannel(e.target.value)}
        />
      </label>
      <label>
        {c("HTTPS source (optional)", "Source HTTPS (facultatif)")}
        <input
          type="url"
          pattern="https://.+"
          maxLength={1024}
          value={link}
          onChange={(e) => setLink(e.target.value)}
        />
      </label>
      <div className="feature-actions">
        <Button type="submit" disabled={write.pending || !text.trim()}>
          {c("Save tag", "Enregistrer le tag")}
        </Button>
        <Button type="button" variant="secondary" onClick={cancel}>
          {c("Cancel", "Annuler")}
        </Button>
        {initial.name && plugin.routes.includes("DELETE /tags/:name") ? (
          <Button type="button" variant="ghost" onClick={() => setRemoving(true)}>
            {c("Delete tag", "Supprimer le tag")}
          </Button>
        ) : null}
      </div>
      {removing ? (
        <Button
          variant="danger"
          type="button"
          disabled={write.pending}
          onClick={() =>
            void write.run("DELETE", "/tags/" + encodeURIComponent(initial.name), undefined, saved)
          }
        >
          {c("Confirm deletion", "Confirmer la suppression")} · {initial.name}
        </Button>
      ) : null}
      <Outcome write={write} />
    </form>
  );
}
export function Tags() {
  const c = useCopy(),
    { staff, plugin } = useScope(),
    [input, setInput] = useState(""),
    [q, search] = useState(""),
    [editor, setEditor] = useState<Tag | null>(null),
    [updated, setUpdated] = useState(false);
  const read = useRead<{ canEdit?: boolean; entries: Tag[] }>("/tags", q ? { q } : undefined);
  const edit =
    staff &&
    plugin.routes.includes("PUT /tags/:name") &&
    !!read.snapshot &&
    "data" in read.snapshot &&
    read.snapshot.data.canEdit !== false;
  return (
    <>
      <form
        className="feature-form feature-filters"
        onSubmit={(e) => {
          e.preventDefault();
          search(input.trim());
        }}
      >
        <label>
          {c("Find a tag", "Rechercher un tag")}
          <input type="search" value={input} onChange={(e) => setInput(e.target.value)} />
        </label>
        <Button type="submit">{c("Search", "Rechercher")}</Button>
      </form>
      {edit ? (
        <Button
          onClick={() => {
            setUpdated(false);
            setEditor(blank);
          }}
        >
          {c("New tag", "Nouveau tag")}
        </Button>
      ) : null}
      {edit && editor ? (
        <TagEditor
          key={editor.name}
          initial={editor}
          cancel={() => setEditor(null)}
          saved={() => {
            setEditor(null);
            setUpdated(true);
            read.reload();
          }}
        />
      ) : null}
      {updated ? <p role="status">{c("Tags updated.", "Tags mis à jour.")}</p> : null}
      <Result read={read}>
        {(d) =>
          d.entries.length ? (
            <ul className="feature-list">
              {d.entries.map((tag) => (
                <li key={tag.name}>
                  <h2>{tag.name}</h2>
                  <Markdown source={tag.text} baseLevel={3} />
                  {tag.aliases.length ? <p>{tag.aliases.join(", ")}</p> : null}
                  {tag.url && /^https:\/\//i.test(tag.url) ? (
                    <p>
                      <a href={tag.url} target="_blank" rel="noopener noreferrer">
                        {c("Open source", "Ouvrir la source")}
                      </a>
                    </p>
                  ) : null}
                  {edit && tag.editable !== false ? (
                    <Button
                      variant="secondary"
                      onClick={() => {
                        setUpdated(false);
                        setEditor(tag);
                      }}
                    >
                      {c("Edit", "Modifier")} {tag.name}
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : (
            <Empty />
          )
        }
      </Result>
    </>
  );
}
export function Unanswered() {
  const c = useCopy(),
    page = useCursor(),
    write = useWrite(),
    { plugin } = useScope(),
    [channel, setChannel] = useState(""),
    [message, setMessage] = useState(""),
    [thread, setThread] = useState("");
  const read = useRead<{ items: { question: string; channel: string }[]; cursor?: string }>(
    "/unanswered",
    { cursor: page.value },
  );
  return (
    <>
      <Result read={read}>
        {(d) => (
          <>
            {d.items.length ? (
              <ul className="feature-list">
                {d.items.map((row, i) => (
                  <li key={i}>
                    <p>{row.question}</p>
                    <span>#{row.channel}</span>
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
      {plugin.routes.includes("POST /solved") ? (
        <details>
          <summary>
            {c("Add a solved Discord answer", "Ajouter une réponse Discord résolue")}
          </summary>
          <form
            className="feature-form"
            onSubmit={(e) => {
              e.preventDefault();
              void write.run("POST", "/solved", {
                channel,
                message,
                ...(thread ? { thread } : {}),
              });
            }}
          >
            <label>
              {c("Forum channel key", "Clé du salon forum")}
              <input
                required
                pattern="[a-z][a-z0-9\-]{0,62}"
                value={channel}
                onChange={(e) => setChannel(e.target.value)}
              />
            </label>
            <label>
              {c("Message ID", "Identifiant du message")}
              <input
                required
                pattern="[0-9]{1,20}"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
              />
            </label>
            <label>
              {c("Thread ID (optional)", "Identifiant du fil (facultatif)")}
              <input
                pattern="[0-9]{1,20}"
                value={thread}
                onChange={(e) => setThread(e.target.value)}
              />
            </label>
            <Button type="submit" disabled={write.pending}>
              {c("Save answer", "Enregistrer la réponse")}
            </Button>
            <Outcome write={write} />
          </form>
        </details>
      ) : null}
    </>
  );
}
