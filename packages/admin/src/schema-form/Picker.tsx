/**
 * Accessible single-select combobox with a filterable listbox, used for resource references
 * (roles with their color, #channels grouped by category) where a native <select> cannot show
 * the information an operator needs.
 */
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { i18n } from "../i18n/index.ts";

export interface PickerOption {
  value: string;
  label: string;
  /** Secondary text (e.g. the resource key). */
  hint?: string;
  /** Group heading (e.g. the channel category). */
  group?: string;
  /** Leading visual (color swatch, # glyph). */
  icon?: ReactNode;
  disabled?: boolean;
}

export interface PickerProps {
  id?: string;
  value: string | undefined;
  options: PickerOption[];
  onChange: (value: string | undefined) => void;
  placeholder?: string;
  /** Show a "None" entry that clears the value. */
  clearable?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  /** Rendered when the value is not among the options. */
  unknownLabel?: (value: string) => string;
}

export function Picker(props: PickerProps) {
  const {
    value,
    options,
    onChange,
    placeholder = i18n.t("form.select"),
    clearable,
    disabled,
    invalid,
    unknownLabel = (v) => `${v} (not found)`,
  } = props;
  const autoId = useId();
  const id = props.id ?? autoId;
  const listId = `${id}-list`;
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  const entries = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = options.filter(
      (o) =>
        !q ||
        o.label.toLowerCase().includes(q) ||
        o.value.toLowerCase().includes(q) ||
        (o.group ?? "").toLowerCase().includes(q),
    );
    return clearable && !q ? [{ value: "", label: "None", hint: "" }, ...list] : list;
  }, [options, query, clearable]);

  const selected = options.find((o) => o.value === value);

  useEffect(() => {
    if (!open) return undefined;
    const onDoc = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  useEffect(() => {
    if (open) {
      const index = entries.findIndex((o) => o.value === (value ?? ""));
      setActive(index >= 0 ? index : 0);
      searchRef.current?.focus();
    } else {
      setQuery("");
    }
    // only when opening
    // eslint-disable-next-line react-hooks/exhaustive-deps -- resets the query only when the picker opens, not when the value or options change
  }, [open]);

  const choose = (option: PickerOption | undefined) => {
    if (!option || option.disabled) return;
    onChange(option.value === "" ? undefined : option.value);
    setOpen(false);
    buttonRef.current?.focus();
  };

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (!open) setOpen(true);
      else setActive((a) => Math.min(a + 1, entries.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter" && open) {
      e.preventDefault();
      choose(entries[active]);
    } else if (e.key === "Escape" && open) {
      e.preventDefault();
      setOpen(false);
      buttonRef.current?.focus();
    }
  };

  let lastGroup: string | undefined;
  return (
    <div className="sf-picker" ref={rootRef} onKeyDown={onKey}>
      <button
        ref={buttonRef}
        id={id}
        type="button"
        role="combobox"
        className="sf-picker-button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listId}
        aria-invalid={invalid || undefined}
        aria-label={props["aria-label"]}
        aria-labelledby={props["aria-labelledby"]}
        aria-describedby={props["aria-describedby"]}
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
      >
        {selected ? (
          <span className="sf-picker-value">
            {selected.icon}
            <span className="sf-picker-label">{selected.label}</span>
            {selected.hint ? <code className="sf-picker-hint">{selected.hint}</code> : null}
          </span>
        ) : value ? (
          <span className="sf-picker-value sf-picker-unknown">{unknownLabel(value)}</span>
        ) : (
          <span className="sf-picker-placeholder">{placeholder}</span>
        )}
        <svg className="sf-picker-caret" viewBox="0 0 16 16" aria-hidden="true">
          <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      </button>
      {open ? (
        <div className="sf-picker-pop">
          <input
            ref={searchRef}
            className="sf-picker-search"
            placeholder={i18n.t("form.filter")}
            value={query}
            aria-label={i18n.t("form.filterOptions")}
            aria-controls={listId}
            aria-activedescendant={entries[active] ? `${id}-opt-${active}` : undefined}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
            }}
          />
          <ul className="sf-picker-list" role="listbox" id={listId}>
            {entries.length === 0 ? (
              <li className="sf-picker-empty">{i18n.t("form.noMatch")}</li>
            ) : null}
            {entries.map((o, i) => {
              const heading = o.group !== lastGroup && o.group ? o.group : undefined;
              lastGroup = o.group;
              return (
                <li key={`${o.value}-${i}`} role="presentation">
                  {heading ? <div className="sf-picker-group">{heading}</div> : null}
                  <div
                    id={`${id}-opt-${i}`}
                    role="option"
                    aria-selected={(value ?? "") === o.value}
                    aria-disabled={o.disabled || undefined}
                    className="sf-picker-option"
                    data-active={i === active || undefined}
                    onMouseEnter={() => setActive(i)}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => choose(o)}
                  >
                    {o.icon}
                    <span className="sf-picker-label">{o.label}</span>
                    {o.hint ? <code className="sf-picker-hint">{o.hint}</code> : null}
                  </div>
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
