import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type AnchorHTMLAttributes,
  type ButtonHTMLAttributes,
  type CSSProperties,
  type ElementType,
  type HTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { parseHex, readableOn } from "./color.ts";

const cx = (...parts: (string | false | null | undefined)[]): string =>
  parts.filter(Boolean).join(" ");

// ───────────────────────────────────────────────────────────── Spinner

export interface SpinnerProps {
  size?: "sm" | "md" | "lg";
  /** Accessible label. Default "Loading". */
  label?: string;
  className?: string;
}

export function Spinner({ size = "md", label = "Loading", className }: SpinnerProps) {
  return (
    <span role="status" className={cx("codi-spinner", `codi-spinner--${size}`, className)}>
      <span className="codi-spinner__ring" aria-hidden="true" />
      <span className="codi-visually-hidden">{label}</span>
    </span>
  );
}

// ───────────────────────────────────────────────────────────── Button

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ButtonSize = "sm" | "md" | "lg";

interface ButtonOwnProps {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Shows a spinner and disables the button. */
  loading?: boolean;
  /** Leading icon (decorative). */
  icon?: ReactNode;
  /** Stretch to the container width. */
  block?: boolean;
}

export type ButtonProps = ButtonOwnProps & ButtonHTMLAttributes<HTMLButtonElement>;
export type LinkButtonProps = ButtonOwnProps &
  AnchorHTMLAttributes<HTMLAnchorElement> & { href: string };

function buttonClass(
  { variant = "primary", size = "md", block, loading }: ButtonOwnProps,
  className?: string,
) {
  return cx(
    "codi-button",
    `codi-button--${variant}`,
    `codi-button--${size}`,
    block && "codi-button--block",
    loading && "is-loading",
    className,
  );
}

export function Button({
  variant,
  size,
  loading = false,
  icon,
  block,
  className,
  children,
  disabled,
  type = "button",
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      type={type}
      className={buttonClass({ variant, size, block, loading }, className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
    >
      {loading ? (
        <Spinner size="sm" label="" />
      ) : icon ? (
        <span className="codi-button__icon" aria-hidden="true">
          {icon}
        </span>
      ) : null}
      <span className="codi-button__label">{children}</span>
    </button>
  );
}

/** A link styled as a button (navigation, external URLs). */
export function LinkButton({
  variant,
  size,
  loading,
  icon,
  block,
  className,
  children,
  ...rest
}: LinkButtonProps) {
  return (
    <a {...rest} className={buttonClass({ variant, size, block, loading }, className)}>
      {icon ? (
        <span className="codi-button__icon" aria-hidden="true">
          {icon}
        </span>
      ) : null}
      <span className="codi-button__label">{children}</span>
    </a>
  );
}

// ───────────────────────────────────────────────────────────── Card

export interface CardProps extends HTMLAttributes<HTMLElement> {
  as?: ElementType;
  padding?: "none" | "sm" | "md" | "lg";
  /** Raised (default) or inset surface. */
  tone?: "raised" | "inset";
}

export function Card({
  as: Tag = "div",
  padding = "md",
  tone = "raised",
  className,
  ...rest
}: CardProps) {
  return (
    <Tag
      {...rest}
      className={cx("codi-card", `codi-card--${tone}`, `codi-card--pad-${padding}`, className)}
    />
  );
}

// ───────────────────────────────────────────────────────────── Avatar

export interface AvatarProps {
  src?: string | undefined;
  /** Used for initials and the alt text. */
  name: string;
  size?: number;
  /** Ring color (e.g. the server primary). */
  ring?: string;
  className?: string;
}

function hue(text: string): number {
  let h = 0;
  for (const ch of text) h = (h * 31 + ch.codePointAt(0)!) % 360;
  return h;
}

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  const letters =
    words.length > 1
      ? [words[0]!, words[1]!].map((w) => Array.from(w)[0])
      : Array.from(words[0] ?? "?").slice(0, 1);
  return letters.join("").toUpperCase();
}

export function Avatar({ src, name, size = 40, ring, className }: AvatarProps) {
  const [failed, setFailed] = useState(false);
  const style = {
    "--codi-avatar-size": `${size}px`,
    "--codi-avatar-hue": hue(name),
    ...(ring ? { "--codi-avatar-ring": ring } : {}),
  } as CSSProperties;
  return (
    <span className={cx("codi-avatar", ring && "codi-avatar--ring", className)} style={style}>
      {src && !failed ? (
        <img src={src} alt={name} width={size} height={size} onError={() => setFailed(true)} />
      ) : (
        <span role="img" aria-label={name} className="codi-avatar__initials">
          {initials(name)}
        </span>
      )}
    </span>
  );
}

// ───────────────────────────────────────────────────────────── RoleChip

export interface RoleChipProps {
  name: string;
  /** Role color (#RRGGBB). Discord's "no color" roles render neutral. */
  color?: string | undefined;
  size?: "sm" | "md";
  /** Highlight a chip that was just added. */
  fresh?: boolean;
  className?: string;
  style?: CSSProperties;
}

/** A Discord-style role pill with its color dot. */
export function RoleChip({ name, color, size = "md", fresh, className, style }: RoleChipProps) {
  return (
    <span
      className={cx("codi-role-chip", `codi-role-chip--${size}`, fresh && "is-fresh", className)}
      style={{ ...(color ? ({ "--codi-role": color } as CSSProperties) : {}), ...style }}
    >
      <span className="codi-role-chip__dot" aria-hidden="true" />
      {name}
    </span>
  );
}

// ───────────────────────────────────────────────────────────── ChoiceGroup / ChoiceTile

/** `--codi-role` plus a readable foreground for marks drawn on it. */
function roleStyle(color: string | undefined): CSSProperties | undefined {
  const rgb = parseHex(color);
  if (!rgb) return undefined;
  return { "--codi-role": color, "--codi-role-fg": readableOn(rgb) } as CSSProperties;
}

export interface ChoiceGroupProps {
  /** Accessible name of the group (usually the question). */
  label: string;
  /** Hidden label: pass when a visible heading already names the group (aria-labelledby). */
  labelledBy?: string;
  multiple?: boolean;
  description?: string;
  children: ReactNode;
  className?: string;
}

/** Group of ChoiceTiles: a `group` of checkboxes, or a `radiogroup`. */
export function ChoiceGroup({
  label,
  labelledBy,
  multiple = false,
  children,
  className,
}: ChoiceGroupProps) {
  const ref = useRef<HTMLDivElement>(null);
  const radios = () =>
    Array.from(
      ref.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]:not(:disabled)') ?? [],
    );

  // Radio pattern: a single tab stop (the checked radio, else the first one)…
  useLayoutEffect(() => {
    if (multiple) return;
    const all = radios();
    const checked = all.findIndex((r) => r.getAttribute("aria-checked") === "true");
    all.forEach((r, i) => (r.tabIndex = i === (checked >= 0 ? checked : 0) ? 0 : -1));
  });

  // …and arrow keys move the selection.
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (multiple) return;
    const delta = { ArrowDown: 1, ArrowRight: 1, ArrowUp: -1, ArrowLeft: -1 }[event.key];
    if (!delta) return;
    const all = radios();
    const current = all.indexOf(document.activeElement as HTMLButtonElement);
    if (current < 0) return;
    event.preventDefault();
    const next = all[(current + delta + all.length) % all.length];
    next?.focus();
    next?.click();
  };

  return (
    <div
      ref={ref}
      role={multiple ? "group" : "radiogroup"}
      {...(labelledBy ? { "aria-labelledby": labelledBy } : { "aria-label": label })}
      className={cx("codi-choice-group", className)}
      onKeyDown={onKeyDown}
    >
      {children}
    </div>
  );
}

export interface ChoiceTileProps {
  label: string;
  description?: string;
  emoji?: string;
  /** Role color, shown as the accent of the tile. */
  color?: string;
  selected: boolean;
  /** Checkbox (multiple) or radio semantics. */
  multiple?: boolean;
  disabled?: boolean;
  onToggle: () => void;
  className?: string;
}

/** Large selectable tile (checkbox or radio) for picking a role. */
export function ChoiceTile({
  label,
  description,
  emoji,
  color,
  selected,
  multiple = false,
  disabled,
  onToggle,
  className,
}: ChoiceTileProps) {
  const descriptionId = useId();
  const labelId = useId();
  return (
    <button
      type="button"
      role={multiple ? "checkbox" : "radio"}
      aria-checked={selected}
      aria-labelledby={labelId}
      aria-describedby={description ? descriptionId : undefined}
      disabled={disabled}
      onClick={onToggle}
      className={cx("codi-choice", selected && "is-selected", className)}
      style={roleStyle(color)}
    >
      <span className="codi-choice__emoji" aria-hidden="true">
        {emoji || <span className="codi-choice__dot" />}
      </span>
      <span className="codi-choice__text">
        <span id={labelId} className="codi-choice__label">
          {label}
        </span>
        {description ? (
          <span id={descriptionId} className="codi-choice__description">
            {description}
          </span>
        ) : null}
      </span>
      <span
        className={cx("codi-choice__mark", multiple ? "is-check" : "is-radio")}
        aria-hidden="true"
      >
        <svg viewBox="0 0 16 16" width="12" height="12">
          <path
            d="M3.5 8.5l3 3 6-7"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </span>
    </button>
  );
}

// ───────────────────────────────────────────────────────────── Stepper & ProgressBar

export interface StepperStep {
  id: string;
  label: string;
}

export interface StepperProps {
  steps: StepperStep[];
  /** Index of the current step. */
  current: number;
  /** Indexes of steps that are done (default: every step before `current`). */
  done?: number[];
  /** Makes visited steps clickable. */
  onSelect?: (index: number) => void;
  label?: string;
  className?: string;
}

export function Stepper({
  steps,
  current,
  done,
  onSelect,
  label = "Progress",
  className,
}: StepperProps) {
  const isDone = (i: number) => (done ? done.includes(i) : i < current);
  return (
    <nav aria-label={label} className={cx("codi-stepper", className)}>
      <ol>
        {steps.map((step, i) => {
          const state = i === current ? "current" : isDone(i) ? "done" : "todo";
          const clickable = onSelect && (state === "done" || i < current);
          const content = (
            <>
              <span className="codi-stepper__index" aria-hidden="true">
                {state === "done" ? (
                  <svg viewBox="0 0 16 16" width="11" height="11">
                    <path
                      d="M3.5 8.5l3 3 6-7"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2.4"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                ) : (
                  i + 1
                )}
              </span>
              <span className="codi-stepper__label">{step.label}</span>
            </>
          );
          return (
            <li
              key={step.id}
              className={`codi-stepper__step is-${state}`}
              aria-current={state === "current" ? "step" : undefined}
            >
              {clickable ? (
                <button type="button" onClick={() => onSelect(i)}>
                  {content}
                </button>
              ) : (
                <span>{content}</span>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

export interface ProgressBarProps {
  /** 0 → 1 */
  value: number;
  label: string;
  className?: string;
}

export function ProgressBar({ value, label, className }: ProgressBarProps) {
  const pct = Math.round(Math.max(0, Math.min(1, value)) * 100);
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={pct}
      className={cx("codi-progress", className)}
    >
      <span className="codi-progress__bar" style={{ width: `${pct}%` }} />
    </div>
  );
}

// ───────────────────────────────────────────────────────────── Notice

export interface NoticeProps {
  tone?: "info" | "success" | "error";
  title?: string;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
}

export function Notice({ tone = "info", title, children, action, className }: NoticeProps) {
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={cx("codi-notice", `codi-notice--${tone}`, className)}
    >
      <div className="codi-notice__body">
        {title ? <p className="codi-notice__title">{title}</p> : null}
        {children ? <div className="codi-notice__text">{children}</div> : null}
      </div>
      {action ? <div className="codi-notice__action">{action}</div> : null}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── MemberCard

export interface MemberCardProps {
  displayName: string;
  username?: string;
  avatarUrl?: string | undefined;
  roles: { key: string; name: string; color?: string | undefined }[];
  /** Role keys to highlight (e.g. just picked). */
  fresh?: string[];
  /** Banner color; defaults to the theme primary. */
  banner?: string;
  /** Text under the name (e.g. "Membre depuis…"). */
  caption?: ReactNode;
  /** Shown when there are no roles. */
  emptyRoles?: ReactNode;
  rolesLabel?: string;
  className?: string;
  children?: ReactNode;
}

/** Discord-like profile card: banner, avatar, name and role chips. */
export function MemberCard({
  displayName,
  username,
  avatarUrl,
  roles,
  fresh = [],
  banner,
  caption,
  emptyRoles,
  rolesLabel = "Roles",
  className,
  children,
}: MemberCardProps) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className={cx("codi-member-card", className)}>
      <div
        className="codi-member-card__banner"
        style={banner ? { background: banner } : undefined}
        aria-hidden="true"
      />
      <div className="codi-member-card__head">
        <Avatar src={avatarUrl} name={displayName} size={76} className="codi-member-card__avatar" />
      </div>
      <div className="codi-member-card__body">
        <h2 id={headingId} className="codi-member-card__name">
          {displayName}
        </h2>
        {username ? <p className="codi-member-card__username">@{username}</p> : null}
        {caption ? <p className="codi-member-card__caption">{caption}</p> : null}
        <div className="codi-member-card__roles">
          <h3 className="codi-member-card__roles-title">{rolesLabel}</h3>
          {roles.length > 0 ? (
            <ul>
              {roles.map((role) => (
                <li key={role.key || role.name}>
                  <RoleChip
                    name={role.name}
                    color={role.color}
                    size="sm"
                    fresh={fresh.includes(role.key)}
                  />
                </li>
              ))}
            </ul>
          ) : (
            <p className="codi-member-card__empty">{emptyRoles}</p>
          )}
        </div>
        {children}
      </div>
    </section>
  );
}
