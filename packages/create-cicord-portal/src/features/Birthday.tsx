import { Button } from "@cicord/ui";
import { useState } from "react";
import { useT } from "../i18n/index.tsx";
import { Outcome, Result, useCopy, useRead, useScope, useWrite } from "./shared.tsx";
interface Birthday {
  month: number;
  day: number;
}
function BirthdayForm({ birthday, refresh }: { birthday: Birthday | null; refresh: () => void }) {
  const c = useCopy(),
    locale = useT().locale,
    { plugin } = useScope(),
    write = useWrite();
  const [month, setMonth] = useState(String(birthday?.month ?? "")),
    [day, setDay] = useState(String(birthday?.day ?? "")),
    [removing, setRemoving] = useState(false);
  const max = month ? new Date(Date.UTC(2000, Number(month), 0)).getUTCDate() : 31;
  return (
    <form
      className="feature-form"
      onSubmit={(e) => {
        e.preventDefault();
        void write.run("PUT", "/birthday", { month: Number(month), day: Number(day) }, refresh);
      }}
    >
      <label>
        {c("Month", "Mois")}
        <select required value={month} onChange={(e) => setMonth(e.target.value)}>
          <option value="">{c("Select a month", "Choisir un mois")}</option>
          {Array.from({ length: 12 }, (_, i) => (
            <option key={i} value={i + 1}>
              {new Intl.DateTimeFormat(locale, { month: "long", timeZone: "UTC" }).format(
                Date.UTC(2000, i, 1),
              )}
            </option>
          ))}
        </select>
      </label>
      <label>
        {c("Day", "Jour")}
        <input
          type="number"
          min={1}
          max={max}
          required
          value={day}
          onChange={(e) => setDay(e.target.value)}
        />
      </label>
      <div className="feature-actions">
        {plugin.routes.includes("PUT /birthday") ? (
          <Button type="submit" disabled={write.pending}>
            {c("Save birthday", "Enregistrer mon anniversaire")}
          </Button>
        ) : null}
        {birthday && plugin.routes.includes("DELETE /birthday") ? (
          <Button
            type="button"
            variant="secondary"
            disabled={write.pending}
            onClick={() => setRemoving(true)}
          >
            {c("Remove birthday", "Retirer mon anniversaire")}
          </Button>
        ) : null}
      </div>
      {removing ? (
        <fieldset>
          <legend>
            {c(
              "Remove your birthday from celebrations?",
              "Retirer votre anniversaire des célébrations ?",
            )}
          </legend>
          <div className="feature-actions">
            <Button
              variant="danger"
              type="button"
              disabled={write.pending}
              onClick={() => void write.run("DELETE", "/birthday", undefined, refresh)}
            >
              {c("Confirm removal", "Confirmer le retrait")}
            </Button>
            <Button variant="secondary" type="button" onClick={() => setRemoving(false)}>
              {c("Cancel", "Annuler")}
            </Button>
          </div>
        </fieldset>
      ) : null}
      <Outcome write={write} />
    </form>
  );
}
export function Birthdays() {
  const read = useRead<{ birthday: Birthday | null }>("/birthday");
  const c = useCopy(),
    [saved, setSaved] = useState(false);
  return (
    <>
      <Result read={read}>
        {(d) => (
          <BirthdayForm
            birthday={d.birthday}
            refresh={() => {
              setSaved(true);
              read.reload();
            }}
          />
        )}
      </Result>
      {saved ? (
        <p role="status">
          {c("Birthday preference updated.", "Préférence d’anniversaire mise à jour.")}
        </p>
      ) : null}
    </>
  );
}
