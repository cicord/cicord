import { useT } from "../i18n/index.tsx";
import { Empty, Result, useCopy, useDate, useRead, useScope } from "./shared.tsx";
export function Calendar() {
  const c = useCopy(),
    date = useDate(),
    { plugin, api } = useScope();
  const read = useRead<{ id: string; name: string; start: string; end?: string }[]>("/calendar");
  return (
    <>
      {plugin.routes.includes("GET /calendar.ics") ? (
        <p>
          <a className="feature-link" href={api.url("/calendar.ics")}>
            {c("Download calendar (.ics)", "Télécharger le calendrier (.ics)")}
          </a>
        </p>
      ) : null}
      <Result read={read}>
        {(d) =>
          d.length ? (
            <ol className="feature-list">
              {d
                .toSorted((a, b) => Date.parse(a.start) - Date.parse(b.start))
                .map((e) => (
                  <li key={e.id}>
                    <h2>{e.name}</h2>
                    <time dateTime={e.start}>{date(e.start)}</time>
                    {e.end ? (
                      <span>
                        {" "}
                        – <time dateTime={e.end}>{date(e.end)}</time>
                      </span>
                    ) : null}
                  </li>
                ))}
            </ol>
          ) : (
            <Empty />
          )
        }
      </Result>
    </>
  );
}
export function Invites() {
  const c = useCopy(),
    fmt = new Intl.NumberFormat(useT().locale),
    read = useRead<{ userId: string; valid: number; fake: number; left: number }[]>("/leaderboard");
  return (
    <Result read={read}>
      {(d) =>
        d.length ? (
          <div className="feature-table">
            <table>
              <thead>
                <tr>
                  <th>{c("Member", "Membre")}</th>
                  <th>{c("Valid", "Valides")}</th>
                  <th>{c("Suspected fakes", "Suspectes")}</th>
                  <th>{c("Left", "Départs")}</th>
                </tr>
              </thead>
              <tbody>
                {d.map((r) => (
                  <tr key={r.userId}>
                    <td>{r.userId}</td>
                    <td>{fmt.format(r.valid)}</td>
                    <td>{fmt.format(r.fake)}</td>
                    <td>{fmt.format(r.left)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty />
        )
      }
    </Result>
  );
}

export function Poll({ id }: { id: string }) {
  const c = useCopy(),
    date = useDate();
  const read = useRead<{
    question: string;
    status: string;
    closesAt: string;
    voters: number;
    choices: { label: string; votes: number }[];
  }>("/polls/" + encodeURIComponent(id));
  return (
    <Result read={read}>
      {(p) => (
        <>
          <h2>{p.question}</h2>
          <p>
            {p.status} · {p.voters} {c("voters", "votants")} · {date(p.closesAt)}
          </p>
          <div className="feature-table">
            <table>
              <thead>
                <tr>
                  <th>{c("Option", "Choix")}</th>
                  <th>{c("Votes", "Votes")}</th>
                </tr>
              </thead>
              <tbody>
                {p.choices.map((x, i) => (
                  <tr key={i}>
                    <td>{x.label}</td>
                    <td>{x.votes}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </Result>
  );
}
