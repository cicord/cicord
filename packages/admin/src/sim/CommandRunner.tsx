/** Slash command runner: pick a command, fill its options from their types, run it. */
import type { Json, SimCommand, SimCommandOption, SimServer } from "@cicord/client";
import { useMemo, useState, type FormEvent } from "react";
import { jsonText } from "../schema-form/values.ts";
import { IconPlay, IconSlash } from "./kit.tsx";
import { Button, EmptyState } from "./kit.tsx";
import { i18n } from "../i18n/index.ts";

export interface CommandOption {
  name: string;
  description?: string;
  type: string;
  required?: boolean;
  choices?: { name: string; value: Json }[];
  minValue?: number;
  maxValue?: number;
  minLength?: number;
  maxLength?: number;
  /** The bot suggests values while the user types. */
  autocomplete?: boolean;
}

export type AutocompleteChoice = { name: string; value: string | number };

/** Discord's numeric option types, in case a runtime sends those. */
const NUMERIC_TYPES: Record<number, string> = {
  1: "subcommand",
  2: "subcommandgroup",
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

export function normalizeOption(raw: SimCommandOption | Json): CommandOption | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const o = raw as unknown as { [key: string]: Json };
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
    ...(typeof o["minLength"] === "number" ? { minLength: o["minLength"] } : {}),
    ...(typeof o["maxLength"] === "number" ? { maxLength: o["maxLength"] } : {}),
    ...(o["autocomplete"] === true ? { autocomplete: true } : {}),
  };
}

const isSubcommand = (o: SimCommandOption): boolean => {
  const t = (o as { type: unknown }).type;
  return t === 1 || t === 2 || (typeof t === "string" && /^subcommand(group)?$/i.test(t));
};

/**
 * One entry per invocable command, like Discord's command picker: a command with
 * subcommands becomes "polls close", "polls results", "polls admin purge" (`name` is the path
 * the simulator API takes, `options` those of the subcommand).
 */
export function expandCommands(commands: SimCommand[]): SimCommand[] {
  const out: SimCommand[] = [];
  const walk = (c: SimCommand, path: string, description: string, options: SimCommandOption[]) => {
    if (options.length > 0 && options.every(isSubcommand)) {
      for (const sub of options) walk(c, `${path} ${sub.name}`, sub.description, sub.options ?? []);
      return;
    }
    out.push({ ...c, name: path, description, options });
  };
  for (const c of commands) walk(c, c.name, c.description, c.options ?? []);
  return out;
}

export function CommandRunner({
  commands: registered,
  server,
  disabled,
  disabledReason,
  onRun,
  onAutocomplete,
  initial,
}: {
  commands: SimCommand[];
  server: SimServer;
  disabled?: boolean;
  disabledReason?: string;
  /** `command.name` is the command path ("poll", "polls close"). */
  onRun: (command: SimCommand, options: { [key: string]: Json }) => Promise<void>;
  /** Asks the bot for suggestions while an autocomplete option is typed. */
  onAutocomplete?: (
    command: SimCommand,
    option: string,
    value: string,
  ) => Promise<AutocompleteChoice[]>;
  initial?: string;
}) {
  const commands = useMemo(() => expandCommands(registered), [registered]);
  const [suggestions, setSuggestions] = useState<Record<string, AutocompleteChoice[]>>({});
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
      <EmptyState icon={<IconSlash />} title={i18n.t("sim.commands.none")}>
        {i18n.t("sim.commands.noneText")}
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
          {i18n.t("sim.commands.command")}
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
            setSuggestions({});
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
                  <option value="">
                    {o.required ? i18n.t("sim.commands.choose") : i18n.t("sim.commands.notSet")}
                  </option>
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
                  <option value="">
                    {o.required ? i18n.t("sim.commands.choose") : i18n.t("sim.commands.notSet")}
                  </option>
                  <option value="true">{i18n.t("sim.commands.true")}</option>
                  <option value="false">{i18n.t("sim.commands.false")}</option>
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
                  <option value="">
                    {o.required
                      ? i18n.t("sim.commands.chooseMember")
                      : i18n.t("sim.commands.notSet")}
                  </option>
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
                  <option value="">
                    {o.required
                      ? i18n.t("sim.commands.chooseChannel")
                      : i18n.t("sim.commands.notSet")}
                  </option>
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
                  <option value="">
                    {o.required ? i18n.t("sim.commands.chooseRole") : i18n.t("sim.commands.notSet")}
                  </option>
                  {server.roles.map((r) => (
                    <option key={r.id} value={r.id}>
                      @{r.name}
                    </option>
                  ))}
                </select>
              );
            } else if ((o.type === "integer" || o.type === "number") && !o.autocomplete) {
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
              const suggest = o.autocomplete && onAutocomplete && command;
              const ask = (v: string) => {
                if (!suggest) return;
                onAutocomplete(command, o.name, v).then(
                  (choices) => setSuggestions((s) => ({ ...s, [o.name]: choices })),
                  () => setSuggestions((s) => ({ ...s, [o.name]: [] })),
                );
              };
              input = (
                <>
                  <input
                    id={id}
                    className="input"
                    value={value}
                    disabled={o.type === "attachment"}
                    minLength={o.minLength}
                    maxLength={o.maxLength}
                    list={suggest ? `${id}-suggestions` : undefined}
                    placeholder={
                      o.type === "attachment"
                        ? i18n.t("sim.commands.noAttachments")
                        : suggest
                          ? i18n.t("sim.commands.autocomplete")
                          : undefined
                    }
                    onFocus={() => ask(value)}
                    onChange={(e) => {
                      set(e.target.value);
                      ask(e.target.value);
                    }}
                  />
                  {suggest ? (
                    <datalist id={`${id}-suggestions`} data-testid={`${id}-suggestions`}>
                      {(suggestions[o.name] ?? []).map((c) => (
                        <option key={String(c.value)} value={String(c.value)}>
                          {c.name}
                        </option>
                      ))}
                    </datalist>
                  ) : null}
                </>
              );
            }
            return (
              <div key={o.name} className="cmd-option">
                <label htmlFor={id} className="cmd-option-label">
                  <code>{o.name}</code>
                  <span className="muted small">{o.type}</span>
                  {o.required ? (
                    <span className="req">{i18n.t("sim.commands.required")}</span>
                  ) : null}
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
          {i18n.t("sim.commands.run", { name: command?.name ?? "" })}
        </Button>
      </div>
    </form>
  );
}
