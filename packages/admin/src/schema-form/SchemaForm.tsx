/**
 * Settings form generated from a plugin's JSON Schema (ADR-0007). Controlled: `value` is the
 * whole config object and `onChange` receives the next one.
 */
import type { Json, ServerResources } from "@codi/client";
import { createContext, useContext, useId, useState, type ReactNode } from "react";
import { Markdown } from "./markdown.tsx";
import { Picker } from "./Picker.tsx";
import { refLabel, refOptions } from "./refs.tsx";
import {
  enumOptions,
  fieldKind,
  fieldLabel,
  initialValue,
  resolve,
  type JsonSchema,
} from "./schema.ts";
import {
  deepEqual,
  getIn,
  isObject,
  jsonText,
  move,
  removeAt,
  setIn,
  toPointer,
  type JsonObject,
  type Segment,
} from "./values.ts";
import { i18n } from "../i18n/index.ts";

export interface SchemaFormProps {
  schema: JsonSchema;
  value: JsonObject;
  onChange: (next: JsonObject) => void;
  /** Top-level fields the user may edit (`editable` from the API); default: all. */
  editable?: readonly string[];
  /** Resources of the server, for `x-codi-ref` pickers. */
  resources?: ServerResources;
  /** Error messages per field pointer (client validation + runtime diagnostics). */
  errors?: Record<string, string[]>;
  /** Saved value, to mark modified fields. */
  baseline?: JsonObject;
  disabled?: boolean;
  /** Text of the read-only badge. */
  lockedLabel?: string;
}

interface Ctx {
  root: JsonSchema;
  resources: ServerResources | undefined;
  editable: ReadonlySet<string> | undefined;
  errors: Record<string, string[]>;
  baseline: JsonObject | undefined;
  disabled: boolean;
  lockedLabel: string;
  idBase: string;
  update: (path: Segment[], value: Json | undefined) => void;
}

const FormContext = createContext<Ctx | null>(null);
const useForm = (): Ctx => useContext(FormContext)!;

export function SchemaForm(props: SchemaFormProps) {
  const idBase = useId().replace(/:/g, "");
  const root = props.schema;
  const s = resolve(root, root);
  const ctx: Ctx = {
    root,
    resources: props.resources,
    editable: props.editable ? new Set(props.editable) : undefined,
    errors: props.errors ?? {},
    baseline: props.baseline,
    disabled: props.disabled ?? false,
    lockedLabel: props.lockedLabel ?? i18n.t("plugins.editor.adminsOnly"),
    idBase,
    update: (path, v) => props.onChange(setIn(props.value, path, v) as JsonObject),
  };
  const required = new Set(s.required ?? []);
  return (
    <FormContext.Provider value={ctx}>
      <div className="sf-form">
        {Object.entries(s.properties ?? {}).map(([key, prop]) => (
          <Field
            key={key}
            schema={prop}
            path={[key]}
            name={key}
            value={props.value[key]}
            required={required.has(key)}
          />
        ))}
      </div>
    </FormContext.Provider>
  );
}

interface FieldProps {
  schema: JsonSchema;
  path: Segment[];
  name: string | number;
  value: Json | undefined;
  required: boolean;
  /** Rendered inside an array row: no label block. */
  bare?: boolean;
}

function useFieldState(path: Segment[]) {
  const ctx = useForm();
  const pointer = toPointer(path);
  const top = String(path[0] ?? "");
  const locked = ctx.disabled || (ctx.editable ? !ctx.editable.has(top) : false);
  const errors = ctx.errors[pointer] ?? [];
  const id = `${ctx.idBase}${pointer.replace(/[^\w-]/g, "_")}`;
  return { ctx, pointer, locked, errors, id };
}

function Field(props: FieldProps) {
  const { ctx } = useFieldState(props.path);
  const s = resolve(props.schema, ctx.root);
  switch (fieldKind(s)) {
    case "object":
      return <ObjectField {...props} schema={s} />;
    case "array":
      return <ArrayField {...props} schema={s} />;
    case "boolean":
      return <BooleanField {...props} schema={s} />;
    default:
      return <ScalarField {...props} schema={s} />;
  }
}

/** Label, key, badges, description and errors around a control. */
function FieldShell({
  schema,
  path,
  name,
  required,
  children,
  id,
  inlineControl,
  value,
}: {
  schema: JsonSchema;
  path: Segment[];
  name: string | number;
  required: boolean;
  children: ReactNode;
  id: string;
  inlineControl?: boolean;
  value: Json | undefined;
}) {
  const { ctx, locked, errors } = useFieldState(path);
  const modified = ctx.baseline !== undefined && !deepEqual(getIn(ctx.baseline, path), value);
  const topLocked = path.length === 1 && locked && !ctx.disabled;
  return (
    <div
      className="sf-field"
      data-inline={inlineControl || undefined}
      data-modified={modified || undefined}
      data-invalid={errors.length > 0 || undefined}
    >
      <div className="sf-head">
        <label className="sf-label" htmlFor={id} id={`${id}-label`}>
          {fieldLabel(schema, name)}
        </label>
        {typeof name === "string" ? <code className="sf-key">{name}</code> : null}
        {required ? <span className="sf-badge">{i18n.t("form.required")}</span> : null}
        {topLocked ? (
          <span className="sf-badge sf-badge-lock" title={i18n.t("form.lockedHint")}>
            <LockIcon />
            {ctx.lockedLabel}
          </span>
        ) : null}
        {modified ? <span className="sf-badge sf-badge-mod">{i18n.t("form.modified")}</span> : null}
        {inlineControl ? <div className="sf-inline-control">{children}</div> : null}
      </div>
      {inlineControl ? null : children}
      {schema.description ? (
        <p className="sf-desc" id={`${id}-desc`}>
          {schema.description}
        </p>
      ) : null}
      {errors.map((e) => (
        <p key={e} className="sf-error" id={`${id}-err`} role="alert">
          {e}
        </p>
      ))}
    </div>
  );
}

function LockIcon() {
  return (
    <svg viewBox="0 0 16 16" width="11" height="11" aria-hidden="true">
      <rect x="3.5" y="7" width="9" height="6.5" rx="1.5" fill="currentColor" />
      <path d="M5.5 7V5a2.5 2.5 0 015 0v2" fill="none" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  );
}

function describedBy(id: string, schema: JsonSchema, errors: string[]): string | undefined {
  const ids = [schema.description ? `${id}-desc` : "", errors.length ? `${id}-err` : ""].filter(
    Boolean,
  );
  return ids.length ? ids.join(" ") : undefined;
}

// ───────────────────────────────────────────────────────────── scalars

function ScalarField(props: FieldProps) {
  const { schema, path, value, required, bare } = props;
  const { ctx, locked, errors, id } = useFieldState(path);
  const control = (
    <ScalarControl
      id={id}
      schema={schema}
      value={value}
      required={required}
      locked={locked}
      invalid={errors.length > 0}
      describedBy={describedBy(id, schema, errors)}
      label={bare ? fieldLabel(schema, props.name) : undefined}
      onChange={(v) => ctx.update(path, v)}
    />
  );
  if (bare)
    return (
      <div className="sf-bare" data-invalid={errors.length > 0 || undefined}>
        {control}
        {errors.map((e) => (
          <p key={e} className="sf-error" role="alert">
            {e}
          </p>
        ))}
      </div>
    );
  return (
    <FieldShell {...props} id={id}>
      {control}
    </FieldShell>
  );
}

interface ControlProps {
  id: string;
  schema: JsonSchema;
  value: Json | undefined;
  required: boolean;
  locked: boolean;
  invalid: boolean;
  describedBy: string | undefined;
  /** aria-label when there is no visible label (array rows). */
  label: string | undefined;
  onChange: (value: Json | undefined) => void;
}

/** Empty optional fields are removed (so the runtime applies the default / leaves it unset). */
function emptyValue(schema: JsonSchema, required: boolean): Json | undefined {
  return required || schema.default !== undefined ? "" : undefined;
}

function ScalarControl(p: ControlProps) {
  const ctx = useForm();
  const s = p.schema;
  const common = {
    id: p.id,
    disabled: p.locked,
    "aria-invalid": p.invalid || undefined,
    "aria-describedby": p.describedBy,
    "aria-label": p.label,
  };
  const kind = fieldKind(s);

  if (kind === "ref") {
    const ref = s["x-codi-ref"]!;
    const value = typeof p.value === "string" ? p.value : undefined;
    return (
      <Picker
        id={p.id}
        value={value || undefined}
        options={refOptions(ref, ctx.resources)}
        onChange={(v) => p.onChange(v ?? emptyValue(s, p.required))}
        clearable={!p.required}
        disabled={p.locked}
        invalid={p.invalid}
        placeholder={i18n.t("form.selectRef", { kind: i18n.t(`form.ref.${ref}`) })}
        aria-label={p.label}
        aria-labelledby={p.label ? undefined : `${p.id}-label`}
        aria-describedby={p.describedBy}
        unknownLabel={(v) => `${v} — no such ${ref} on this server`}
      />
    );
  }

  if (kind === "enum") {
    const options = enumOptions(s);
    const index = options.findIndex((o) => deepEqual(o.value, p.value));
    if (options.length <= 4 && options.every((o) => o.label.length <= 16)) {
      return (
        <div
          className="sf-segmented"
          role="radiogroup"
          aria-labelledby={p.label ? undefined : `${p.id}-label`}
          aria-label={p.label}
          aria-describedby={p.describedBy}
          id={p.id}
        >
          {options.map((o, i) => (
            <button
              key={o.label}
              type="button"
              role="radio"
              aria-checked={i === index}
              disabled={p.locked}
              onClick={() => p.onChange(o.value)}
              title={o.description}
            >
              {o.label}
            </button>
          ))}
        </div>
      );
    }
    return (
      <select
        {...common}
        className="sf-input sf-select"
        value={index >= 0 ? String(index) : ""}
        onChange={(e) =>
          p.onChange(e.target.value === "" ? undefined : options[Number(e.target.value)]!.value)
        }
      >
        {index < 0 ? (
          <option value="">{p.required ? i18n.t("form.select") : i18n.t("form.notSet")}</option>
        ) : null}
        {options.map((o, i) => (
          <option key={o.label} value={String(i)}>
            {o.label}
          </option>
        ))}
      </select>
    );
  }

  if (kind === "number" || kind === "integer") {
    const v = typeof p.value === "number" ? p.value : "";
    return (
      <input
        {...common}
        className="sf-input sf-number"
        type="number"
        inputMode={kind === "integer" ? "numeric" : "decimal"}
        step={kind === "integer" ? 1 : "any"}
        min={s.minimum}
        max={s.maximum}
        placeholder={s.default !== undefined ? jsonText(s.default) : undefined}
        value={v}
        onChange={(e) => {
          const raw = e.target.value;
          p.onChange(raw === "" ? undefined : Number(raw));
        }}
      />
    );
  }

  if (kind === "json") return <JsonControl {...p} />;

  // strings
  const text = jsonText(p.value);
  const set = (raw: string) => p.onChange(raw === "" ? emptyValue(s, p.required) : raw);
  const widget = s["x-codi-widget"];
  const counter =
    s.maxLength !== undefined && text.length > s.maxLength * 0.6 ? (
      <span className="sf-counter" data-over={text.length > s.maxLength || undefined}>
        {text.length}/{s.maxLength}
      </span>
    ) : null;

  if (widget === "color") {
    const valid = /^#[0-9a-fA-F]{6}$/.test(text);
    return (
      <div className="sf-color">
        <input
          type="color"
          className="sf-color-swatch"
          value={valid ? text : "#000000"}
          disabled={p.locked}
          aria-label={`${p.label ?? "Color"} picker`}
          onChange={(e) => set(e.target.value)}
        />
        <input
          {...common}
          className="sf-input sf-mono"
          value={text}
          placeholder="#5865f2"
          spellCheck={false}
          onChange={(e) => set(e.target.value.trim())}
        />
      </div>
    );
  }
  if (widget === "textarea" || widget === "markdown") {
    return (
      <TextArea
        common={common}
        value={text}
        markdown={widget === "markdown"}
        onChange={set}
        counter={counter}
        maxLength={s.maxLength}
      />
    );
  }
  return (
    <div className="sf-input-wrap">
      <input
        {...common}
        className={`sf-input${widget === "duration" || s.pattern ? " sf-mono" : ""}`}
        value={text}
        placeholder={
          widget === "duration"
            ? "e.g. 10m, 1h30m"
            : typeof s.default === "string" && s.default
              ? s.default
              : undefined
        }
        spellCheck={widget === "duration" ? false : undefined}
        onChange={(e) => set(e.target.value)}
      />
      {counter}
    </div>
  );
}

function TextArea({
  common,
  value,
  markdown,
  onChange,
  counter,
}: {
  common: Record<string, unknown>;
  value: string;
  markdown: boolean;
  onChange: (v: string) => void;
  counter: ReactNode;
  maxLength: number | undefined;
}) {
  const [preview, setPreview] = useState(false);
  const rows = Math.min(
    12,
    Math.max(3, value.split("\n").length + 1, Math.ceil(value.length / 70)),
  );
  return (
    <div className="sf-textarea">
      {markdown ? (
        <div className="sf-textarea-tabs" role="tablist" aria-label={i18n.t("form.editorMode")}>
          <button
            type="button"
            role="tab"
            aria-selected={!preview}
            onClick={() => setPreview(false)}
          >
            {i18n.t("form.write")}
          </button>
          <button type="button" role="tab" aria-selected={preview} onClick={() => setPreview(true)}>
            {i18n.t("form.preview")}
          </button>
        </div>
      ) : null}
      {preview ? (
        <div className="sf-preview">
          {value ? (
            <Markdown source={value} placeholders />
          ) : (
            <em>{i18n.t("form.nothingToPreview")}</em>
          )}
        </div>
      ) : (
        <textarea
          {...common}
          className="sf-input"
          rows={rows}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {counter}
    </div>
  );
}

function JsonControl(p: ControlProps) {
  const [draft, setDraft] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const shown = draft ?? (p.value === undefined ? "" : JSON.stringify(p.value, null, 2));
  return (
    <div>
      <textarea
        id={p.id}
        className="sf-input sf-mono"
        rows={Math.min(10, shown.split("\n").length + 1)}
        disabled={p.locked}
        aria-label={p.label}
        aria-describedby={p.describedBy}
        aria-invalid={p.invalid || error !== null || undefined}
        value={shown}
        spellCheck={false}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => {
          if (draft === null) return;
          try {
            p.onChange(draft.trim() === "" ? undefined : (JSON.parse(draft) as Json));
            setDraft(null);
            setError(null);
          } catch {
            setError(i18n.t("form.invalidJson"));
          }
        }}
      />
      {error ? <p className="sf-error">{error}</p> : null}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── boolean

function BooleanField(props: FieldProps) {
  const { schema, path, value, bare } = props;
  const { ctx, locked, errors, id } = useFieldState(path);
  const checked = value === true;
  const control = (
    <button
      type="button"
      role="switch"
      id={id}
      className="sf-switch"
      aria-checked={checked}
      aria-label={bare ? fieldLabel(schema, props.name) : undefined}
      aria-labelledby={bare ? undefined : `${id}-label`}
      aria-describedby={describedBy(id, schema, errors)}
      disabled={locked}
      onClick={() => ctx.update(path, !checked)}
    >
      <span className="sf-switch-thumb" />
    </button>
  );
  if (bare) return control;
  return (
    <FieldShell {...props} id={id} inlineControl>
      {control}
    </FieldShell>
  );
}

// ───────────────────────────────────────────────────────────── object

function ObjectField(props: FieldProps) {
  const { schema, path, value } = props;
  const { errors, id } = useFieldState(path);
  const obj = isObject(value) ? value : {};
  const required = new Set(schema.required ?? []);
  const fields = Object.entries(schema.properties ?? {}).map(([key, prop]) => (
    <Field
      key={key}
      schema={prop}
      path={[...path, key]}
      name={key}
      value={obj[key]}
      required={required.has(key)}
    />
  ));
  if (props.bare) return <div className="sf-object-body">{fields}</div>;
  return (
    <fieldset className="sf-object" aria-describedby={describedBy(id, schema, errors)}>
      <legend className="sf-legend" id={`${id}-label`}>
        {fieldLabel(schema, props.name)}
        {typeof props.name === "string" ? <code className="sf-key">{props.name}</code> : null}
      </legend>
      {schema.description ? (
        <p className="sf-desc" id={`${id}-desc`}>
          {schema.description}
        </p>
      ) : null}
      {errors.map((e) => (
        <p key={e} className="sf-error" id={`${id}-err`} role="alert">
          {e}
        </p>
      ))}
      <div className="sf-object-body">{fields}</div>
    </fieldset>
  );
}

// ───────────────────────────────────────────────────────────── array

function singular(name: string | number, schema: JsonSchema): string {
  if (schema.items?.title) return schema.items.title.toLowerCase();
  if (typeof name !== "string") return "item";
  const label = fieldLabel({}, name).toLowerCase();
  if (label.endsWith("ies")) return `${label.slice(0, -3)}y`;
  if (label.endsWith("s") && !label.endsWith("ss")) return label.slice(0, -1);
  return "item";
}

/** One-line summary of an object item for its collapsed header. */
function summarize(item: Json | undefined, schema: JsonSchema, ctx: Ctx): string {
  if (!isObject(item)) return "";
  const props = schema.properties ?? {};
  const emoji = typeof item["emoji"] === "string" ? `${item["emoji"]} ` : "";
  for (const key of ["title", "label", "name"]) {
    if (typeof item[key] === "string" && item[key]) return `${emoji}${item[key] as string}`;
  }
  for (const [key, raw] of Object.entries(props)) {
    const p = resolve(raw, ctx.root);
    const v = item[key];
    if (typeof v !== "string" || !v) continue;
    const ref = p["x-codi-ref"];
    if (ref) return `${emoji}${refLabel(ref, v, ctx.resources) ?? v}`;
    if (key !== "emoji") return `${emoji}${v}`;
  }
  return emoji.trim();
}

function ArrayField(props: FieldProps) {
  const { schema, path, value, name } = props;
  const { ctx, locked, errors, id } = useFieldState(path);
  const list = Array.isArray(value) ? value : [];
  const itemSchema = resolve(schema.items ?? {}, ctx.root);
  const itemKind = fieldKind(itemSchema);
  const canAdd = !locked && (schema.maxItems === undefined || list.length < schema.maxItems);
  const canRemove = !locked && list.length > (schema.minItems ?? 0);
  const noun = singular(name, schema);
  const set = (next: Json[]) => ctx.update(path, next);

  // Multi-select of enum values: checkbox chips.
  if (itemKind === "enum" && schema.uniqueItems) {
    return (
      <FieldShell {...props} id={id}>
        <div
          className="sf-chips"
          role="group"
          aria-labelledby={`${id}-label`}
          aria-describedby={describedBy(id, schema, errors)}
        >
          {enumOptions(itemSchema).map((o) => {
            const on = list.some((v) => deepEqual(v, o.value));
            return (
              <label key={o.label} className="sf-chip" data-on={on || undefined}>
                <input
                  type="checkbox"
                  checked={on}
                  disabled={locked}
                  onChange={() =>
                    set(on ? list.filter((v) => !deepEqual(v, o.value)) : [...list, o.value])
                  }
                />
                {o.label}
              </label>
            );
          })}
        </div>
      </FieldShell>
    );
  }

  const objects = itemKind === "object";
  const add = () => {
    const v = initialValue(itemSchema, ctx.root);
    set([...list, v === undefined ? null : v]);
  };

  return (
    <FieldShell {...props} id={id}>
      <div className={objects ? "sf-list sf-list-objects" : "sf-list"} id={id}>
        {list.length === 0 ? (
          <p className="sf-empty">{i18n.t("form.emptyList", { noun })}</p>
        ) : null}
        {list.map((item, i) =>
          objects ? (
            <ObjectItem
              key={i}
              index={i}
              count={list.length}
              item={item}
              schema={itemSchema}
              path={[...path, i]}
              noun={noun}
              locked={locked}
              canRemove={canRemove}
              onMove={(to) => set(move(list, i, to))}
              onRemove={() => set(removeAt(list, i))}
            />
          ) : (
            <div className="sf-row" key={i}>
              <span className="sf-row-index">{i + 1}</span>
              <div className="sf-row-control">
                <Field
                  schema={itemSchema}
                  path={[...path, i]}
                  name={`${fieldLabel(schema, name)} ${i + 1}`}
                  value={item}
                  required
                  bare
                />
              </div>
              <RowActions
                index={i}
                count={list.length}
                noun={noun}
                locked={locked}
                canRemove={canRemove}
                onMove={(to) => set(move(list, i, to))}
                onRemove={() => set(removeAt(list, i))}
              />
            </div>
          ),
        )}
        {canAdd ? (
          <button type="button" className="sf-add" onClick={add}>
            <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
              <path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="1.6" />
            </svg>
            {i18n.t("form.add", { noun })}
          </button>
        ) : null}
      </div>
    </FieldShell>
  );
}

function RowActions(p: {
  index: number;
  count: number;
  noun: string;
  locked: boolean;
  canRemove: boolean;
  onMove: (to: number) => void;
  onRemove: () => void;
}) {
  if (p.locked) return null;
  const n = p.index + 1;
  return (
    <div className="sf-row-actions">
      <button
        type="button"
        className="sf-icon-btn"
        aria-label={i18n.t("form.moveUp", { noun: p.noun, n })}
        disabled={p.index === 0}
        onClick={() => p.onMove(p.index - 1)}
      >
        <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
          <path d="M4 10l4-4 4 4" fill="none" stroke="currentColor" strokeWidth="1.6" />
        </svg>
      </button>
      <button
        type="button"
        className="sf-icon-btn"
        aria-label={i18n.t("form.moveDown", { noun: p.noun, n })}
        disabled={p.index === p.count - 1}
        onClick={() => p.onMove(p.index + 1)}
      >
        <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
          <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.6" />
        </svg>
      </button>
      <button
        type="button"
        className="sf-icon-btn sf-danger"
        aria-label={i18n.t("form.remove", { noun: p.noun, n })}
        disabled={!p.canRemove}
        onClick={p.onRemove}
      >
        <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
          <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.6" />
        </svg>
      </button>
    </div>
  );
}

function ObjectItem(p: {
  index: number;
  count: number;
  item: Json;
  schema: JsonSchema;
  path: Segment[];
  noun: string;
  locked: boolean;
  canRemove: boolean;
  onMove: (to: number) => void;
  onRemove: () => void;
}) {
  const ctx = useForm();
  const pointer = toPointer(p.path);
  const hasErrors = Object.keys(ctx.errors).some(
    (k) => k === pointer || k.startsWith(`${pointer}/`),
  );
  const [open, setOpen] = useState(() => p.count <= 1 || hasErrors);
  const isOpen = open || hasErrors;
  const summary = summarize(p.item, p.schema, ctx);
  const bodyId = `${ctx.idBase}${pointer.replace(/[^\w-]/g, "_")}-body`;
  return (
    <div className="sf-item" data-open={isOpen || undefined} data-invalid={hasErrors || undefined}>
      <div className="sf-item-head">
        <button
          type="button"
          className="sf-item-toggle"
          aria-expanded={isOpen}
          aria-controls={bodyId}
          onClick={() => setOpen(!isOpen)}
        >
          <svg
            className="sf-item-caret"
            viewBox="0 0 16 16"
            width="12"
            height="12"
            aria-hidden="true"
          >
            <path d="M6 4l4 4-4 4" fill="none" stroke="currentColor" strokeWidth="1.6" />
          </svg>
          <span className="sf-item-index">
            {p.noun.charAt(0).toUpperCase() + p.noun.slice(1)} {p.index + 1}
          </span>{" "}
          <span className="sf-item-summary">{summary}</span>
        </button>
        <RowActions {...p} />
      </div>
      {isOpen ? (
        <div className="sf-item-body" id={bodyId}>
          <ObjectField
            schema={p.schema}
            path={p.path}
            name={p.index}
            value={p.item}
            required
            bare
          />
        </div>
      ) : null}
    </div>
  );
}
