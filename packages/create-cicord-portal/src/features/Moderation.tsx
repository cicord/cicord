import { useState } from "react";
import { Button } from "@cicord/ui";
import {
  Empty,
  FeatureLink,
  Paging,
  Result,
  useCopy,
  useCursor,
  useDate,
  useRead,
  useScope,
} from "./shared.tsx";

interface Case {
  number: number;
  action: string;
  at: string;
  reason: string;
  status?: string;
  expiresAt?: string;
  member?: { id: string; name: string } | null;
  moderator?: { id: string; name: string } | null;
  evidence?: string;
}
const actions = [
  "warn",
  "timeout",
  "untimeout",
  "kick",
  "ban",
  "tempban",
  "unban",
  "note",
  "purge",
  "clear",
  "quarantine",
  "release",
];
const statuses = ["active", "expired", "lifted", "void", "superseded", "appealed"];
function CaseRows({ items }: { items: Case[] }) {
  const { staff } = useScope();
  const date = useDate(),
    c = useCopy();
  return items.length ? (
    <ul className="feature-list">
      {items
        .filter((row) => staff || row.action !== "note")
        .map((row) => (
          <li key={row.number}>
            <h2>
              #{row.number} · {row.action}
            </h2>
            <p>{row.reason}</p>
            <div className="feature-meta">
              <time dateTime={row.at}>{date(row.at)}</time>
              <span>{row.status ?? "active"}</span>
              {row.expiresAt ? (
                <span>
                  {c("Expires", "Expire le")} {date(row.expiresAt)}
                </span>
              ) : null}
            </div>
            {staff && row.member ? (
              <FeatureLink path={"/moderation/members/" + encodeURIComponent(row.member.id)}>
                {row.member.name}
              </FeatureLink>
            ) : null}
            {staff && row.moderator ? (
              <p>
                {c("Moderator", "Modérateur")} · {row.moderator.name}
              </p>
            ) : null}
            {staff && row.evidence ? (
              <details>
                <summary>{c("Evidence", "Éléments de preuve")}</summary>
                <p>{row.evidence}</p>
              </details>
            ) : null}
          </li>
        ))}
    </ul>
  ) : (
    <Empty />
  );
}
export function MemberCases({ id }: { id: string }) {
  const c = useCopy(),
    date = useDate(),
    { staff } = useScope();
  const read = useRead<{
    items: Case[];
    appeals?: {
      case: number;
      status: string;
      text: string;
      submittedAt: string;
      resolution?: string;
    }[];
  }>("/members/" + encodeURIComponent(id));
  return (
    <Result read={read}>
      {(d) => (
        <>
          <CaseRows items={d.items} />
          {d.appeals?.length ? (
            <section className="feature-section">
              <h2>{c("Appeals", "Appels")}</h2>
              <ul className="feature-list">
                {d.appeals.map((a) => (
                  <li key={a.case}>
                    <strong>
                      #{a.case} · {a.status}
                    </strong>
                    <p>{a.text}</p>
                    <time dateTime={a.submittedAt}>{date(a.submittedAt)}</time>
                    {staff && a.resolution ? <p>{a.resolution}</p> : null}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
        </>
      )}
    </Result>
  );
}
export function Cases() {
  const c = useCopy();
  const { plugin } = useScope();
  const page = useCursor();
  const [filter, setFilter] = useState<Record<string, string>>({});
  const read = useRead<{ items: Case[]; next: string | null }>("/cases", {
    ...filter,
    cursor: page.value,
  });
  return (
    <>
      {plugin.routes.includes("GET /appeals") ? (
        <p>
          <FeatureLink path="/appeals/staff">
            {c("Review appeals", "Examiner les appels")}
          </FeatureLink>
        </p>
      ) : null}
      <form
        className="feature-form feature-filters"
        onSubmit={(e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          setFilter(
            Object.fromEntries(
              ["action", "status", "moderator"]
                .map((k) => [k, typeof f.get(k) === "string" ? (f.get(k) as string).trim() : ""])
                .filter(([, v]) => v),
            ),
          );
          page.reset();
        }}
      >
        <label>
          {c("Action", "Action")}
          <select name="action">
            <option value="">{c("All actions", "Toutes les actions")}</option>
            {actions.map((v) => (
              <option key={v}>{v}</option>
            ))}
          </select>
        </label>
        <label>
          {c("Status", "Statut")}
          <select name="status">
            <option value="">{c("All statuses", "Tous les statuts")}</option>
            {statuses.map((v) => (
              <option key={v}>{v}</option>
            ))}
          </select>
        </label>
        <label>
          {c("Moderator ID", "Identifiant du modérateur")}
          <input name="moderator" inputMode="numeric" pattern="[0-9]{1,20}" />
        </label>
        <Button type="submit">{c("Filter", "Filtrer")}</Button>
      </form>
      <Result read={read}>
        {(d) => (
          <>
            <CaseRows items={d.items} />
            <Paging previous={page.back} next={d.next ? () => page.next(d.next!) : undefined} />
          </>
        )}
      </Result>
    </>
  );
}
