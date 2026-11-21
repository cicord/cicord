/** Slash command runner: pick a command, fill its options from their types, run it. */
import type { Json, SimCommand, SimServer } from "@codi/client";
import { useMemo, useState, type FormEvent } from "react";
import { jsonText } from "../schema-form/values.ts";
import { IconPlay, IconSlash } from "../ui/icons.tsx";
import { Button, EmptyState } from "../ui/index.tsx";

export interface CommandOption {
  name: string;
  description?: string;
  type: string;
  required?: boolean;
  choices?: { name: string; value: Json }[];
  minValue?: number;
  maxValue?: number;
}

/** Discord's numeric option types, in case a runtime sends those. */
const NUMERIC_TYPES: Record<number, string> = {
  3: "string",
  4: "integer",
  5: "boolean",
  6: "user",
  7: "channel",
  8: "role",
  9: "mentionable",
  10: "number",
  11: "attachment",
};

export function normalizeOption(raw: Json): CommandOption | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const o = raw as { [key: string]: Json };
  if (typeof o["name"] !== "string") return null;
  const t = o["type"];
  const type =
    typeof t === "number"
      ? (NUMERIC_TYPES[t] ?? "string")
      : typeof t === "string"
        ? t.toLowerCase()
        : "string";
  const choices = Array.isArray(o["choices"])
    ? (o["choices"] as { [key: string]: Json }[])
        .filter((c) => c && typeof c === "object")
        .map((c) => ({ name: jsonText(c["name"] ?? c["value"]), value: c["value"] ?? null }))
    : undefined;
  return {
    name: o["name"],
    type,
    ...(typeof o["description"] === "string" ? { description: o["description"] } : {}),
    required: o["required"] === true,
    ...(choices && choices.length ? { choices } : {}),
    ...(typeof o["minValue"] === "number" ? { minValue: o["minValue"] } : {}),
    ...(typeof o["maxValue"] === "number" ? { maxValue: o["maxValue"] } : {}),
  };
}

export function CommandRunner({
  commands,
  server,
  disabled,
  disabledReason,
  onRun,
  initial,
}: {
  commands: SimCommand[];
  server: SimServer;
  disabled?: boolean;
  disabledReason?: string;
  onRun: (command: SimCommand, options: { [key: string]: Json }) => Promise<void>;
  initial?: string;
}) {
  const [selected, setSelected] = useState<string>(() => {
    const found = commands.find((c) => c.name === initial);
    return found
      ? `${found.bot}/${found.name}`
      : commands[0]
        ? `${commands[0].bot}/${commands[0].name}`
        : "";
  });
  const [values, setValues] = useState<Record<string, string>>({});
  const [running, setRunning] = useState(false);
  const command = commands.find((c) => `${c.bot}/${c.name}` === selected);
  const options = useMemo(
    () =>
      (command?.options ?? []).map(normalizeOption).filter((o): o is CommandOption => o !== null),
    [command],
  );
  const bots = new Set(commands.map((c) => c.bot));

  if (commands.length === 0)
    return (
      <EmptyState icon={<IconSlash />} title="No slash commands on this server">
        Plugins register commands with <code>command()</code> in <code>definePlugin</code>.
      </EmptyState>
    );

  const missing = options.filter((o) => o.required && !values[o.name]);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!command || missing.length) return;
    const out: { [key: string]: Json } = {};
    for (const o of options) {
      const raw = values[o.name];
      if (raw === undefined || raw === "") continue;
      if (o.type === "integer" || o.type === "number") out[o.name] = Number(raw);
      else if (o.type === "boolean") out[o.name] = raw === "true";
      else if (o.choices)
        out[o.name] = o.choices.find((c) => jsonText(c.value) === raw)?.value ?? raw;
      else out[o.name] = raw;
    }
    setRunning(true);
    try {
      await onRun(command, out);
    } finally {
      setRunning(false);
    }
  };

  const channels = server.channels.filter((c) => c.type !== "category" && c.type !== "voice");
  return (
    <form className="cmd-runner" onSubmit={(e) => void submit(e)}>
      <div className="cmd-pick">
        <label htmlFor="sim-command" className="sr-only">
          Command
        </label>
        <span className="cmd-slash" aria-hidden="true">
          /
        </span>
        <select
          id="sim-command"
          className="input cmd-select"
          value={selected}
          onChange={(e) => {
            setSelected(e.target.value);
            setValues({});
          }}
        >
          {commands.map((c) => (
            <option key={`${c.bot}/${c.name}`} value={`${c.bot}/${c.name}`}>
              {c.name}
              {bots.size > 1 ? ` (${c.bot})` : ""}
            </option>
          ))}
        </select>
        <span className="cmd-desc muted small">{command?.description}</span>
      </div>
      {options.length ? (
        <div className="cmd-options">
          {options.map((o) => {
            const id = `opt-${o.name}`;
            const set = (v: string) => setValues((s) => ({ ...s, [o.name]: v }));
            const value = values[o.name] ?? "";
            let input;
            if (o.choices) {
              input = (
                <select
                  id={id}
                  className="input"
                  value={value}
                  onChange={(e) => set(e.target.value)}
                >
                  <option value="">{o.required ? "Choose…" : "Not set"}</option>
                  {o.choices.map((c) => (
                    <option key={jsonText(c.value)} value={jsonText(c.value)}>
                      {c.name}
                    </option>
                  ))}
                </select>
              );
            } else if (o.type === "boolean") {
              input = (
                <select
                  id={id}
                  className="input"
                  value={value}
                  onChange={(e) => set(e.target.value)}
                >
                  <option value="">{o.required ? "Choose…" : "Not set"}</option>
                  <option value="true">True</option>
                  <option value="false">False</option>
                </select>
              );
            } else if (o.type === "user" || o.type === "mentionable") {
              input = (
                <select
                  id={id}
                  className="input"
                  value={value}
                  onChange={(e) => set(e.target.value)}
                >
                  <option value="">{o.required ? "Choose a member…" : "Not set"}</option>
                  {server.members.map((m) => (
                    <option key={m.user.id} value={m.user.id}>
                      @{m.nick || m.user.displayName || m.user.username}
                    </option>
                  ))}
                  {o.type === "mentionable"
                    ? server.roles.map((r) => (
                        <option key={r.id} value={r.id}>
                          @{r.name} (role)
                        </option>
                      ))
                    : null}
                </select>
              );
            } else if (o.type === "channel") {
              input = (
                <select
                  id={id}
                  className="input"
                  value={value}
                  onChange={(e) => set(e.target.value)}
                >
                  <option value="">{o.required ? "Choose a channel…" : "Not set"}</option>
                  {channels.map((c) => (
                    <option key={c.id} value={c.id}>
                      #{c.name}
                    </option>
                  ))}
                </select>
              );
            } else if (o.type === "role") {
              input = (
                <select
                  id={id}
                  className="input"
                  value={value}
                  onChange={(e) => set(e.target.value)}
                >
                  <option value="">{o.required ? "Choose a role…" : "Not set"}</option>
                  {server.roles.map((r) => (
                    <option key={r.id} value={r.id}>
                      @{r.name}
                    </option>
                  ))}
                </select>
              );
            } else if (o.type === "integer" || o.type === "number") {
              input = (
                <input
                  id={id}
                  className="input"
                  type="number"
                  step={o.type === "integer" ? 1 : "any"}
                  min={o.minValue}
                  max={o.maxValue}
                  value={value}
                  onChange={(e) => set(e.target.value)}
                />
              );
            } else {
              input = (
                <input
                  id={id}
                  className="input"
                  value={value}
                  disabled={o.type === "attachment"}
                  placeholder={
                    o.type === "attachment" ? "Attachments are not simulated" : undefined
                  }
                  onChange={(e) => set(e.target.value)}
                />
              );
            }
            return (
              <div key={o.name} className="cmd-option">
                <label htmlFor={id} className="cmd-option-label">
                  <code>{o.name}</code>
                  <span className="muted small">{o.type}</span>
                  {o.required ? <span className="req">required</span> : null}
                </label>
                {input}
                {o.description ? <span className="muted small">{o.description}</span> : null}
              </div>
            );
          })}
        </div>
      ) : null}
      <div className="cmd-actions">
        {disabledReason ? <span className="muted small">{disabledReason}</span> : null}
        <Button
          type="submit"
          variant="primary"
          icon={<IconPlay size={12} />}
          loading={running}
          disabled={disabled || !command || missing.length > 0}
        >
          Run /{command?.name}
        </Button>
      </div>
    </form>
  );
}
