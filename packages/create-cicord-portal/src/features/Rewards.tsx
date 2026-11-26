import { CicordApiError } from "@cicord/client";
import { Button, Markdown } from "@cicord/ui";
import { useRef, useState } from "react";
import { useT } from "../i18n/index.tsx";
import { navigate } from "../router.tsx";
import { featureUrl } from "./catalog.ts";
import {
  type AllPages,
  Empty,
  Failure,
  FeatureLink,
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

interface Proposal {
  id: string;
  status: string;
  score: number;
  body?: string;
  authorId?: string;
  reason?: string;
}
const statuses = ["open", "considered", "approved", "denied", "implemented"];
function ProposalDecision({ item, reload }: { item: Proposal; reload: () => void }) {
  const c = useCopy(),
    write = useWrite(),
    [status, setStatus] = useState(item.status),
    [reason, setReason] = useState("");
  return (
    <details>
      <summary>{c("Change status", "Changer le statut")}</summary>
      <form
        className="feature-form"
        onSubmit={(e) => {
          e.preventDefault();
          if (reason.trim())
            void write.run(
              "POST",
              "/suggestions/" + encodeURIComponent(item.id) + "/status",
              { status, reason: reason.trim() },
              reload,
            );
        }}
      >
        <label>
          {c("New status", "Nouveau statut")}
          <select value={status} onChange={(e) => setStatus(e.target.value)}>
            {statuses.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        </label>
        <label>
          {c("Decision reason", "Motif de la décision")}
          <textarea
            required
            maxLength={500}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>
        <Button type="submit" disabled={write.pending || !reason.trim()}>
          {c("Save decision", "Enregistrer la décision")}
        </Button>
        <Outcome write={write} />
      </form>
    </details>
  );
}
export function Suggestions({ moderation }: { moderation: boolean }) {
  const c = useCopy(),
    { plugin } = useScope(),
    [filter, setFilter] = useState(""),
    [offset, setOffset] = useState(0);
  const path = moderation ? "/suggestions/moderation" : "/suggestions";
  const read = useRead<{ items: Proposal[]; nextOffset?: number }>(path, {
    offset,
    ...(filter ? { status: filter } : {}),
  });
  return (
    <>
      {!moderation ? (
        <label className="feature-form">
          {c("Filter by status", "Filtrer par statut")}
          <select
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value);
              setOffset(0);
            }}
          >
            <option value="">{c("All statuses", "Tous les statuts")}</option>
            {statuses.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        </label>
      ) : null}
      <Result read={read}>
        {(p) => (
          <>
            {p.items.length ? (
              <ul className="feature-list">
                {p.items.map((s) => (
                  <li key={s.id}>
                    <h2>
                      {c("Suggestion", "Suggestion")} #{s.id}
                    </h2>
                    <div className="feature-meta">
                      <span>{s.status}</span>
                      <span>
                        {c("Score", "Score")} · {s.score}
                      </span>
                      {s.authorId ? (
                        <span>
                          {c("Author", "Auteur")} · {s.authorId}
                        </span>
                      ) : null}
                    </div>
                    {s.body ? <Markdown source={s.body} baseLevel={3} /> : null}
                    {s.reason ? <p>{s.reason}</p> : null}
                    {moderation && plugin.routes.includes("POST /suggestions/:id/status") ? (
                      <ProposalDecision item={s} reload={read.reload} />
                    ) : null}
                  </li>
                ))}
              </ul>
            ) : (
              <Empty />
            )}
            <Paging
              previous={offset ? () => setOffset(Math.max(0, offset - 100)) : undefined}
              next={
                p.nextOffset !== undefined || p.items.length === 100
                  ? () => setOffset(p.nextOffset ?? offset + 100)
                  : undefined
              }
            />
          </>
        )}
      </Result>
    </>
  );
}
interface Entry {
  userId: string;
  weight: number;
}
interface DrawView {
  id: string;
  prize: string;
  state: string;
  closesAt: number;
  commitment: string;
  entrants: number;
  winners: string[];
  seed?: string;
  entrantsHash?: string;
  round?: number;
  history?: string[];
  entries?: Entry[];
  /** Next page of `entries` (`?cursor=`), absent on the last page. */
  entriesCursor?: string;
  redacted?: boolean;
}
// The giveaway routes return entrants in pages of 1,000, in storage order.
const byUserId = (a: Entry, b: Entry) => {
  const x = BigInt(/^\d+$/.test(a.userId) ? a.userId : 0),
    y = BigInt(/^\d+$/.test(b.userId) ? b.userId : 0);
  return x < y ? -1 : x > y ? 1 : a.userId < b.userId ? -1 : a.userId > b.userId ? 1 : 0;
};
/**
 * The draw's public facts must be read from one state: every page repeats them, and a page
 * that disagrees (a redraw while loading) fails the read instead of mixing two draws.
 */
const drawPages: AllPages<DrawView> = {
  cursor: (d) => d.entriesCursor,
  merge(all, page) {
    for (const k of ["state", "commitment", "seed", "entrantsHash", "round", "entrants"] as const)
      if (all[k] !== page[k]) throw new Error("the draw changed while it was loading");
    const entries = [...(all.entries ?? []), ...(page.entries ?? [])].toSorted(byUserId);
    const merged: DrawView = { ...all, entries };
    if (page.entriesCursor) merged.entriesCursor = page.entriesCursor;
    else delete merged.entriesCursor;
    return merged;
  },
};
function Entries({ entries }: { entries: Entry[] }) {
  const c = useCopy();
  return entries.length ? (
    <div className="feature-table">
      <table>
        <thead>
          <tr>
            <th>{c("Participant", "Participant")}</th>
            <th>{c("Weight", "Poids")}</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((e) => (
            <tr key={e.userId}>
              <td>{e.userId}</td>
              <td>{e.weight}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  ) : (
    <Empty />
  );
}
interface EntryPage {
  entries: Entry[];
  entriesCursor?: string;
}
function StaffEntries({ id }: { id: string }) {
  const c = useCopy(),
    [opened, set] = useState(false);
  const path = "/giveaways/" + encodeURIComponent(id) + "/entries";
  const read = useRead<EntryPage>(opened ? path : undefined);
  return (
    <details onToggle={(e) => set(e.currentTarget.open)}>
      <summary>{c("Staff participant list", "Liste des participants pour l’équipe")}</summary>
      {opened ? (
        <Result read={read}>
          {(first) => <MoreEntries key={read.snapshot?.key} first={first} path={path} />}
        </Result>
      ) : null}
    </details>
  );
}
/** The first page of entrants, then one more page per "Load more" (`entriesCursor`). */
function MoreEntries({ first, path }: { first: EntryPage; path: string }) {
  const c = useCopy(),
    { api } = useScope();
  const [loaded, setLoaded] = useState<EntryPage>(first);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const next = loaded.entriesCursor;
  async function more() {
    if (!next || pending) return;
    setPending(true);
    setError(undefined);
    try {
      const page = await api.get<EntryPage>(path, { cursor: next });
      setLoaded((all) => ({
        entries: [...all.entries, ...page.entries],
        ...(page.entriesCursor ? { entriesCursor: page.entriesCursor } : {}),
      }));
    } catch (e) {
      setError(e);
    } finally {
      setPending(false);
    }
  }
  return (
    <>
      <Entries entries={loaded.entries} />
      {next ? (
        <Button variant="secondary" disabled={pending} onClick={() => void more()}>
          {c("Load more", "Afficher plus")}
        </Button>
      ) : null}
      {error ? <Failure error={error} retry={() => void more()} /> : null}
    </>
  );
}
export function Draw({ id }: { id: string }) {
  const c = useCopy(),
    date = useDate(),
    { staff, plugin } = useScope();
  // Every page of entrants is read before anything is shown, so the published list is whole.
  const read = useRead<DrawView>("/giveaways/" + encodeURIComponent(id), undefined, drawPages);
  return (
    <Result read={read}>
      {(d) => (
        <>
          <h2>{d.prize}</h2>
          <dl className="feature-facts">
            <dt>{c("Status", "Statut")}</dt>
            <dd>{d.state}</dd>
            <dt>{c("Closes", "Clôture")}</dt>
            <dd>{date(d.closesAt)}</dd>
            <dt>{c("Participants", "Participants")}</dt>
            <dd>{d.entrants}</dd>
            <dt>{c("Commitment", "Engagement")}</dt>
            <dd>
              <code>{d.commitment}</code>
            </dd>
            {d.entrantsHash ? (
              <>
                <dt>{c("Entrants hash", "Empreinte des participants")}</dt>
                <dd>
                  <code>{d.entrantsHash}</code>
                </dd>
              </>
            ) : null}
            {d.seed ? (
              <>
                <dt>{c("Revealed seed", "Graine révélée")}</dt>
                <dd>
                  <code>{d.seed}</code>
                </dd>
              </>
            ) : null}
            {d.round !== undefined ? (
              <>
                <dt>{c("Round", "Tour")}</dt>
                <dd>{d.round}</dd>
              </>
            ) : null}
          </dl>
          {d.winners.length ? (
            <section className="feature-section">
              <h2>{c("Winners", "Gagnants")}</h2>
              <ol>
                {d.winners.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ol>
            </section>
          ) : null}
          {d.history?.length ? (
            <details>
              <summary>{c("Draw history", "Historique du tirage")}</summary>
              <ol>
                {d.history.map((h, i) => (
                  <li key={i}>{h}</li>
                ))}
              </ol>
            </details>
          ) : null}
          {d.entries ? (
            <details>
              <summary>
                {c("Published participants and weights", "Participants et poids publiés")}
              </summary>
              <Entries entries={d.entries} />
            </details>
          ) : null}
          {staff && plugin.routes.includes("GET /giveaways/:id/entries") ? (
            <StaffEntries id={id} />
          ) : null}
        </>
      )}
    </Result>
  );
}
export function Draws() {
  const c = useCopy(),
    date = useDate(),
    page = useCursor(),
    read = useRead<{ items: DrawView[]; cursor?: string }>("/giveaways", { cursor: page.value });
  return (
    <Result read={read}>
      {(d) => (
        <>
          {d.items.length ? (
            <ul className="feature-list">
              {d.items.map((g) => (
                <li key={g.id}>
                  <h2>
                    <FeatureLink path={"/giveaways/" + encodeURIComponent(g.id)}>
                      {g.prize}
                    </FeatureLink>
                  </h2>
                  <p>
                    {g.state} · {g.entrants} {c("participants", "participants")} ·{" "}
                    {date(g.closesAt)}
                  </p>
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
export function Lookup({ kind }: { kind: "season" | "poll" | "giveaway" }) {
  const c = useCopy(),
    { api, plugin } = useScope(),
    [id, set] = useState("");
  return (
    <form
      className="feature-form"
      onSubmit={(e) => {
        e.preventDefault();
        navigate(
          featureUrl(
            (kind === "season" ? "/levels/seasons/" : kind === "poll" ? "/polls/" : "/giveaways/") +
              encodeURIComponent(id.trim()),
            api.server,
            plugin,
          ),
        );
      }}
    >
      <label>
        {kind === "season"
          ? c("Season ID", "Identifiant de saison")
          : kind === "poll"
            ? c("Poll ID", "Identifiant du sondage")
            : c("Giveaway ID", "Identifiant du tirage")}
        <input
          required
          maxLength={128}
          pattern="[A-Za-z0-9_\-]+"
          value={id}
          onChange={(e) => set(e.target.value)}
        />
      </label>
      <Button type="submit">{c("View results", "Voir les résultats")}</Button>
    </form>
  );
}
interface LedgerRow {
  id: string;
  userId: string;
  amount: number;
  balance: number;
  reason: string;
  at: number;
}
export function Ledger({ staff = false }: { staff?: boolean }) {
  const c = useCopy(),
    date = useDate(),
    locale = useT().locale,
    page = useCursor();
  const read = useRead<{ items: LedgerRow[]; cursor?: string }>(staff ? "/ledger" : "/history", {
    cursor: page.value,
  });
  return (
    <Result read={read}>
      {(d) => (
        <>
          {d.items.length ? (
            <div className="feature-table">
              <table>
                <thead>
                  <tr>
                    <th>{c("Date", "Date")}</th>
                    {staff ? <th>{c("Member", "Membre")}</th> : null}
                    <th>{c("Reason", "Motif")}</th>
                    <th>{c("Change", "Variation")}</th>
                    <th>{c("Balance", "Solde")}</th>
                  </tr>
                </thead>
                <tbody>
                  {d.items.map((row) => (
                    <tr key={row.id + ":" + row.userId}>
                      <td>{date(row.at)}</td>
                      {staff ? <td>{row.userId}</td> : null}
                      <td>{row.reason}</td>
                      <td>
                        {new Intl.NumberFormat(locale, { signDisplay: "always" }).format(
                          row.amount,
                        )}
                      </td>
                      <td>{row.balance}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty />
          )}
          <Paging previous={page.back} next={d.cursor ? () => page.next(d.cursor!) : undefined} />
        </>
      )}
    </Result>
  );
}
interface Order {
  orderId: string;
  itemId: string;
  state: "pending" | "delivered";
}
interface Product {
  id: string;
  label: string;
  price: number;
}
function Purchase({
  product,
  balance,
  saved,
}: {
  product: Product;
  balance: number;
  saved: (o: Order) => void;
}) {
  const c = useCopy(),
    write = useWrite(),
    { api, viewer } = useScope();
  const key =
    "cicord:order:" + JSON.stringify([api.server, api.bot, api.instance, viewer, product.id]);
  const [stored, setStored] = useState(() => {
    try {
      return sessionStorage.getItem(key) || "";
    } catch {
      return "";
    }
  });
  const active = useRef(stored);
  const [confirm, setConfirm] = useState(false);
  function clearOrder() {
    active.current = "";
    setStored("");
    try {
      sessionStorage.removeItem(key);
    } catch {
      /* The in-memory receipt was cleared. */
    }
  }
  function buy() {
    if (!active.current) {
      active.current = crypto.randomUUID();
      setStored(active.current);
      try {
        sessionStorage.setItem(key, active.current);
      } catch {
        /* In-memory receipt for restricted storage. */
      }
    }
    void write.run<Order>(
      "POST",
      "/shop/" + encodeURIComponent(product.id) + "/buy",
      { purchaseId: active.current },
      (o) => {
        if (o.state === "delivered") {
          clearOrder();
        }
        setConfirm(false);
        saved(o);
      },
    );
  }
  if (confirm || stored)
    return (
      <div className="feature-actions">
        <Button disabled={write.pending} onClick={buy}>
          {stored
            ? c("Check purchase", "Vérifier l’achat")
            : c("Confirm purchase", "Confirmer l’achat")}{" "}
          · {product.price}
        </Button>
        {!stored ? (
          <Button variant="ghost" onClick={() => setConfirm(false)}>
            {c("Cancel", "Annuler")}
          </Button>
        ) : null}
        {stored &&
        write.error instanceof CicordApiError &&
        [400, 404, 409].includes(write.error.status) ? (
          <Button
            variant="ghost"
            onClick={() => {
              clearOrder();
              setConfirm(false);
            }}
          >
            {c("Dismiss failed order", "Fermer la commande refusée")}
          </Button>
        ) : null}
        <Outcome write={write} />
      </div>
    );
  return (
    <Button variant="secondary" disabled={balance < product.price} onClick={() => setConfirm(true)}>
      {c("Buy", "Acheter")} {product.label}
    </Button>
  );
}
function Shop({ balance, saved }: { balance: number; saved: (o: Order) => void }) {
  const c = useCopy(),
    { plugin } = useScope(),
    canBuy = plugin.routes.includes("POST /shop/:id/buy"),
    read = useRead<{ items: Product[] }>("/shop");
  return (
    <section className="feature-section">
      <h2>{c("Reward shop", "Boutique de récompenses")}</h2>
      <Result read={read}>
        {(d) =>
          d.items.length ? (
            <>
              {!canBuy ? (
                <p>
                  {c(
                    "Redeem a reward with /buy in Discord.",
                    "Utilisez /buy sur Discord pour obtenir une récompense.",
                  )}
                </p>
              ) : null}
              <ul className="feature-list">
                {d.items.map((x) => (
                  <li key={x.id}>
                    <strong>{x.label}</strong> · {x.price} {c("points", "points")}
                    {canBuy ? (
                      <Purchase product={x} balance={balance} saved={saved} />
                    ) : (
                      <code> · {x.id}</code>
                    )}
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <Empty />
          )
        }
      </Result>
    </section>
  );
}
export function Wallet() {
  const c = useCopy(),
    { plugin } = useScope(),
    read = useRead<{ balance: number }>("/wallet"),
    [receipt, setReceipt] = useState<Order>();
  return (
    <Result read={read}>
      {(d) => (
        <>
          <p>
            <strong>
              {d.balance} {c("points", "points")}
            </strong>
          </p>
          {receipt ? (
            <p role="status">
              {receipt.state === "delivered"
                ? c("Reward delivered.", "Récompense remise.")
                : c("Purchase pending.", "Achat en attente.")}{" "}
              · {receipt.orderId}
            </p>
          ) : null}
          {plugin.routes.includes("GET /history") ? (
            <section className="feature-section">
              <h2>{c("Transaction history", "Historique des transactions")}</h2>
              <Ledger />
            </section>
          ) : null}
          {plugin.routes.includes("GET /shop") ? (
            <Shop
              balance={d.balance}
              saved={(o) => {
                setReceipt(o);
                read.reload();
              }}
            />
          ) : null}
        </>
      )}
    </Result>
  );
}
export function Season({ id }: { id: string }) {
  const c = useCopy();
  const read = useRead<{ entries: { userId: string; xp: number; rank: number }[] }>(
    "/seasons/" + encodeURIComponent(id),
  );
  return (
    <Result read={read}>
      {(d) => (
        <>
          <h2>{id}</h2>
          {d.entries.length ? (
            <div className="feature-table">
              <table>
                <thead>
                  <tr>
                    <th>{c("Rank", "Rang")}</th>
                    <th>{c("Member", "Membre")}</th>
                    <th>XP</th>
                  </tr>
                </thead>
                <tbody>
                  {d.entries.map((e) => (
                    <tr key={e.userId}>
                      <td>{e.rank}</td>
                      <td>{e.userId}</td>
                      <td>{e.xp}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty />
          )}
        </>
      )}
    </Result>
  );
}
