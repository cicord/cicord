import { Button } from "@cicord/ui";
import { useState } from "react";
import {
  Empty,
  Outcome,
  Paging,
  Result,
  useCopy,
  useCursor,
  useDate,
  useRead,
  useScope,
  useWrite,
} from "./shared.tsx";
interface Violation {
  at: number;
  rule: string;
  action: string;
  status?: string;
  channelId: string;
}
export function Violations() {
  const c = useCopy(),
    date = useDate(),
    page = useCursor();
  const read = useRead<{
    items: { userId: string; violations: Violation[] }[];
    cursor: string | null;
  }>("/violations", { cursor: page.value });
  return (
    <Result read={read}>
      {(d) => (
        <>
          {d.items.length ? (
            <ul className="feature-list">
              {d.items.map((r) => (
                <li key={r.userId}>
                  <h2>{r.userId}</h2>
                  {r.violations.map((v, i) => (
                    <p key={i}>
                      {date(v.at)} · {v.rule} · {v.action} · #{v.channelId}
                      {v.status === "needs_review" ? c(" · Needs review", " · À examiner") : ""}
                    </p>
                  ))}
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
  );
}
interface Raid {
  started: number;
  until: number;
  rate: number;
  enforced: boolean;
}
export function Raids() {
  const c = useCopy(),
    date = useDate(),
    read = useRead<{
      active: Raid | null;
      history: { started: number; ended: number; rate: number; enforced: boolean }[];
    }>("/raids");
  return (
    <Result read={read}>
      {(d) => (
        <>
          <h2>{c("Current raid", "Raid en cours")}</h2>
          {d.active ? (
            <p>
              {date(d.active.started)} – {date(d.active.until)} ·{" "}
              {d.active.enforced
                ? c("Protection active", "Protection active")
                : c("Monitoring", "Observation")}
            </p>
          ) : (
            <p>{c("No active raid.", "Aucun raid en cours.")}</p>
          )}
          <h2>{c("Previous raids", "Raids précédents")}</h2>
          {d.history.length ? (
            <ul className="feature-list">
              {d.history.map((r) => (
                <li key={r.started}>
                  {date(r.started)} – {date(r.ended)} ·{" "}
                  {r.enforced
                    ? c("Protection active", "Protection active")
                    : c("Monitoring", "Observation")}
                </li>
              ))}
            </ul>
          ) : (
            <Empty />
          )}
        </>
      )}
    </Result>
  );
}
export function Blocklist() {
  const c = useCopy(),
    { plugin } = useScope(),
    write = useWrite(),
    [kind, setKind] = useState<"domain" | "hash">("domain"),
    [value, setValue] = useState("");
  const read = useRead<{ domains: string[]; hashes: string[] }>("/blocklist");
  const editable = plugin.routes.includes("POST /blocklist");
  return (
    <>
      {editable ? (
        <form
          className="feature-form"
          onSubmit={(e) => {
            e.preventDefault();
            void write.run(
              "POST",
              "/blocklist",
              { kind, action: "add", value: value.trim() },
              () => {
                setValue("");
                read.reload();
              },
            );
          }}
        >
          <label>
            {c("Type", "Type")}
            <select
              value={kind}
              onChange={(e) => {
                setKind(e.target.value === "hash" ? "hash" : "domain");
                setValue("");
              }}
            >
              <option value="domain">{c("Domain", "Domaine")}</option>
              <option value="hash">
                {c("Perceptual image hash", "Empreinte perceptuelle d’image")}
              </option>
            </select>
          </label>
          <label>
            {kind === "hash"
              ? c("Image hash", "Empreinte d’image")
              : c("Domain name", "Nom de domaine")}
            <input
              required
              maxLength={kind === "hash" ? 16 : 253}
              pattern={kind === "hash" ? "[0-9A-Fa-f]{16}" : undefined}
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
          </label>
          <Button type="submit" disabled={write.pending || !value.trim()}>
            {c("Add to blocklist", "Ajouter à la liste de blocage")}
          </Button>
        </form>
      ) : null}
      <Outcome write={write} />
      <Result read={read}>
        {(d) =>
          (["domain", "hash"] as const).map((type) => (
            <section className="feature-section" key={type}>
              <h2>
                {type === "domain"
                  ? c("Blocked domains", "Domaines bloqués")
                  : c("Blocked images", "Images bloquées")}
              </h2>
              {(type === "domain" ? d.domains : d.hashes).length ? (
                <ul className="feature-list">
                  {(type === "domain" ? d.domains : d.hashes).map((v) => (
                    <li key={v}>
                      <code>{v}</code>
                      {editable ? (
                        <Button
                          variant="ghost"
                          aria-label={c("Remove ", "Retirer ") + v}
                          disabled={write.pending}
                          onClick={() =>
                            void write.run(
                              "POST",
                              "/blocklist",
                              { kind: type, action: "remove", value: v },
                              read.reload,
                            )
                          }
                        >
                          {c("Remove", "Retirer")}
                        </Button>
                      ) : null}
                    </li>
                  ))}
                </ul>
              ) : (
                <Empty />
              )}
            </section>
          ))
        }
      </Result>
    </>
  );
}
interface Incident {
  at: number;
  trigger: string;
  count: number;
  actorType: string;
  actorId?: string;
  certainty: string;
  why: string;
}
export function Incidents() {
  const c = useCopy(),
    date = useDate(),
    { plugin } = useScope(),
    page = useCursor();
  const info = useRead<{ mode: string; warnings: string[] }>(
    plugin.routes.includes("GET /status") ? "/status" : undefined,
  );
  const read = useRead<{ items: Incident[]; cursor: string | null }>("/incidents", {
    cursor: page.value,
  });
  return (
    <>
      {plugin.routes.includes("GET /status") ? (
        <Result read={info}>
          {(d) => (
            <>
              <p>
                {c("Protection mode", "Mode de protection")} · {d.mode}
              </p>
              {d.warnings.map((w, i) => (
                <p key={i}>{w}</p>
              ))}
            </>
          )}
        </Result>
      ) : null}
      <Result read={read}>
        {(d) => (
          <>
            {d.items.length ? (
              <ul className="feature-list">
                {d.items.map((i, n) => (
                  <li key={n}>
                    <h2>
                      {i.trigger} · {i.count}
                    </h2>
                    <p>{i.why}</p>
                    <div className="feature-meta">
                      <span>{date(i.at)}</span>
                      <span>
                        {i.actorType}
                        {i.actorId ? " · " + i.actorId : ""}
                      </span>
                      <span>{i.certainty}</span>
                    </div>
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
export function ModeratorStats() {
  const c = useCopy(),
    page = useCursor(),
    read = useRead<{ items: { moderatorId: string; count: number }[]; next: string | null }>(
      "/stats/moderators",
      { cursor: page.value },
    );
  return (
    <Result read={read}>
      {(d) => (
        <>
          {d.items.length ? (
            <div className="feature-table">
              <table>
                <thead>
                  <tr>
                    <th>{c("Moderator", "Modérateur")}</th>
                    <th>{c("Cases", "Dossiers")}</th>
                  </tr>
                </thead>
                <tbody>
                  {d.items.map((m) => (
                    <tr key={m.moderatorId}>
                      <td>{m.moderatorId}</td>
                      <td>{m.count}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty />
          )}
          <Paging previous={page.back} next={d.next ? () => page.next(d.next!) : undefined} />
        </>
      )}
    </Result>
  );
}
