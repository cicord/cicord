import { Button } from "@cicord/ui";
import { useRef, useState } from "react";
import { useT } from "../i18n/index.tsx";
import { Outcome, Result, useCopy, useDate, useRead, useScope, useWrite } from "./shared.tsx";
interface Shift {
  day: number;
  start: number;
  minutes: number;
}
interface Plan {
  zone: string;
  shifts: Shift[];
}
interface StaffOverview {
  zones?: string[];
  plan?: Plan;
  duty?: { start: number; end: number };
  duration?: number;
  ticket?: number;
  case?: number;
}
function PlanEditor({ data, refresh }: { data: StaffOverview; refresh: () => void }) {
  const c = useCopy(),
    date = useDate(),
    locale = useT().locale,
    write = useWrite(),
    { plugin } = useScope();
  const initialZone = data.plan?.zone ?? Intl.DateTimeFormat().resolvedOptions().timeZone ?? "UTC";
  const [zone, setZone] = useState(
    data.zones?.length && !data.zones.includes(initialZone) ? "UTC" : initialZone,
  );
  const [rows, set] = useState((data.plan?.shifts ?? []).map((s, i) => ({ ...s, key: i })));
  const seq = useRef(rows.length);
  function update(key: number, patch: Partial<Shift>) {
    set((r) => r.map((x) => (x.key === key ? { ...x, ...patch } : x)));
  }
  const weekdays = Array.from({ length: 7 }, (_, i) =>
    new Intl.DateTimeFormat(locale, { weekday: "long", timeZone: "UTC" }).format(
      Date.UTC(2023, 0, 1 + i),
    ),
  );
  return (
    <>
      <dl className="feature-facts">
        {data.duty ? (
          <>
            <dt>{c("On duty until", "De garde jusqu’au")}</dt>
            <dd>{date(data.duty.end)}</dd>
          </>
        ) : null}
        <dt>{c("Hours on duty", "Heures de garde")}</dt>
        <dd>
          {new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(
            (data.duration ?? 0) / 3600000,
          )}
        </dd>
        <dt>{c("Tickets / cases", "Tickets / dossiers")}</dt>
        <dd>
          {data.ticket ?? 0} / {data.case ?? 0}
        </dd>
      </dl>
      <form
        className="feature-form"
        onSubmit={(e) => {
          e.preventDefault();
          void write.run(
            "PUT",
            "/me/plan",
            { zone, shifts: rows.map(({ day, start, minutes }) => ({ day, start, minutes })) },
            refresh,
          );
        }}
      >
        <label>
          {c("Time zone", "Fuseau horaire")}
          {data.zones?.length ? (
            <select value={zone} onChange={(e) => setZone(e.target.value)}>
              {data.zones.map((z) => (
                <option key={z}>{z}</option>
              ))}
            </select>
          ) : (
            <input
              required
              value={zone}
              onChange={(e) => setZone(e.target.value)}
              placeholder="Europe/Paris"
            />
          )}
        </label>
        {rows.map((s, i) => (
          <fieldset key={s.key}>
            <legend>
              {c("Shift", "Créneau")} {i + 1}
            </legend>
            <div className="feature-filters feature-form">
              <label>
                {c("Weekday", "Jour")}
                <select
                  value={s.day}
                  onChange={(e) => update(s.key, { day: Number(e.target.value) })}
                >
                  {weekdays.map((d, n) => (
                    <option value={n} key={n}>
                      {d}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                {c("Starts at", "Début")}
                <input
                  type="time"
                  required
                  value={
                    String(Math.floor(s.start / 60)).padStart(2, "0") +
                    ":" +
                    String(s.start % 60).padStart(2, "0")
                  }
                  onChange={(e) => {
                    const [h, m] = e.target.value.split(":").map(Number);
                    if (h !== undefined && m !== undefined) update(s.key, { start: h * 60 + m });
                  }}
                />
              </label>
              <label>
                {c("Duration (minutes)", "Durée (minutes)")}
                <input
                  type="number"
                  required
                  min={15}
                  max={1440}
                  value={s.minutes || ""}
                  onChange={(e) => update(s.key, { minutes: Number(e.target.value) })}
                />
              </label>
            </div>
            <Button
              variant="ghost"
              type="button"
              onClick={() => set((r) => r.filter((x) => x.key !== s.key))}
            >
              {c("Remove shift", "Retirer le créneau")}
            </Button>
          </fieldset>
        ))}
        {plugin.routes.includes("PUT /me/plan") ? (
          <div className="feature-actions">
            <Button
              variant="secondary"
              type="button"
              disabled={rows.length >= 28}
              onClick={() => {
                const k = seq.current++;
                set((r) => [...r, { key: k, day: 1, start: 540, minutes: 60 }]);
              }}
            >
              {c("Add shift", "Ajouter un créneau")}
            </Button>
            <Button type="submit" disabled={write.pending}>
              {c("Save weekly schedule", "Enregistrer le planning")}
            </Button>
          </div>
        ) : null}
        <Outcome write={write} />
      </form>
    </>
  );
}
function Coverage() {
  const c = useCopy(),
    date = useDate();
  const [from, setFrom] = useState("");
  const read = useRead<{ from: number; to: number; gaps: { from: number; to: number }[] }>(
    "/coverage",
    from ? { from: new Date(from + "T00:00:00").toISOString() } : undefined,
  );
  return (
    <section className="feature-section">
      <h2>{c("Uncovered shifts", "Créneaux sans couverture")}</h2>
      <label className="feature-form">
        {c("Week starting", "Semaine à partir du")}
        <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
      </label>
      <Result read={read}>
        {(d) => (
          <>
            <p>
              {date(d.from)} – {date(d.to)}
            </p>
            {d.gaps.length ? (
              <ul className="feature-list">
                {d.gaps.map((g) => (
                  <li key={g.from}>
                    {date(g.from)} – {date(g.to)}
                  </li>
                ))}
              </ul>
            ) : (
              <p>{c("The whole week is covered.", "Toute la semaine est couverte.")}</p>
            )}
          </>
        )}
      </Result>
    </section>
  );
}
export function Shifts() {
  const read = useRead<StaffOverview>("/me"),
    { plugin } = useScope(),
    c = useCopy();
  const [saved, setSaved] = useState(false);
  return (
    <Result read={read}>
      {(d) => (
        <>
          <PlanEditor
            data={d}
            refresh={() => {
              setSaved(true);
              read.reload();
            }}
          />
          {saved ? <p role="status">{c("Schedule saved.", "Planning enregistré.")}</p> : null}
          {plugin.routes.includes("GET /coverage") ? <Coverage /> : null}
        </>
      )}
    </Result>
  );
}
