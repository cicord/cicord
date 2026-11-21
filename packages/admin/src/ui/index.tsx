/** Admin console primitives. Styling lives in src/styles/app.css. */
import { CodiApiError } from "@codi/client";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ReactNode,
} from "react";
import { IconAlert, IconCheck, IconInfo, IconX } from "./icons.tsx";

// ───────────────────────────────────────────────────────────── buttons

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: "sm" | "md";
  loading?: boolean;
  icon?: ReactNode;
}

export function Button({
  variant = "secondary",
  size = "md",
  loading,
  icon,
  children,
  disabled,
  className,
  type = "button",
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      type={type}
      className={`btn btn-${variant} btn-${size}${className ? ` ${className}` : ""}`}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
    >
      {loading ? <Spinner size={12} /> : icon}
      {children ? <span>{children}</span> : null}
    </button>
  );
}

// ───────────────────────────────────────────────────────────── status

export type Tone = "ok" | "warn" | "bad" | "info" | "neutral" | "violet" | "accent";

export function Badge({
  tone = "neutral",
  children,
  dot,
  title,
}: {
  tone?: Tone;
  children: ReactNode;
  dot?: boolean;
  title?: string;
}) {
  return (
    <span className={`badge badge-${tone}`} title={title}>
      {dot ? <span className="badge-dot" aria-hidden="true" /> : null}
      {children}
    </span>
  );
}

const HEALTH_TONE: Record<string, Tone> = {
  Healthy: "ok",
  Progressing: "info",
  Degraded: "bad",
  Missing: "warn",
};

export function HealthBadge({ health, title }: { health: string; title?: string }) {
  return (
    <Badge tone={HEALTH_TONE[health] ?? "neutral"} dot title={title}>
      {health || "Unknown"}
    </Badge>
  );
}

const SYNC_TONE: Record<string, Tone> = { Synced: "ok", OutOfSync: "warn", Unknown: "neutral" };
const SYNC_LABEL: Record<string, string> = {
  Synced: "Synced",
  OutOfSync: "Out of sync",
  Unknown: "Unknown",
};

export function SyncBadge({ sync }: { sync: string }) {
  return (
    <Badge tone={SYNC_TONE[sync] ?? "neutral"} dot>
      {SYNC_LABEL[sync] ?? sync}
    </Badge>
  );
}

const ACTION_TONE: Record<string, Tone> = {
  create: "ok",
  update: "info",
  delete: "bad",
  adopt: "violet",
  reorder: "neutral",
  unmanage: "warn",
};

export function ActionBadge({ action }: { action: string }) {
  return (
    <span className={`action action-${ACTION_TONE[action] ?? "neutral"}`}>
      {action === "create" ? "+" : action === "delete" ? "−" : action === "update" ? "~" : "•"}{" "}
      {action}
    </span>
  );
}

// ───────────────────────────────────────────────────────────── layout

export function Panel({
  title,
  actions,
  children,
  description,
  flush,
  id,
}: {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  description?: ReactNode;
  flush?: boolean;
  id?: string;
}) {
  return (
    <section className="panel" data-flush={flush || undefined} id={id}>
      {title || actions ? (
        <header className="panel-head">
          <div className="panel-titles">
            {title ? <h2 className="panel-title">{title}</h2> : null}
            {description ? <p className="panel-desc">{description}</p> : null}
          </div>
          {actions ? <div className="panel-actions">{actions}</div> : null}
        </header>
      ) : null}
      <div className="panel-body">{children}</div>
    </section>
  );
}

export function PageHeader({
  title,
  meta,
  actions,
  eyebrow,
}: {
  title: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  eyebrow?: ReactNode;
}) {
  return (
    <header className="page-head">
      <div className="page-titles">
        {eyebrow ? <div className="page-eyebrow">{eyebrow}</div> : null}
        <h1 className="page-title">{title}</h1>
        {meta ? <div className="page-meta">{meta}</div> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  );
}

// ───────────────────────────────────────────────────────────── states

export function Spinner({ size = 16, label }: { size?: number; label?: string }) {
  return (
    <span
      className="spinner"
      style={{ width: size, height: size }}
      role={label ? "status" : undefined}
      aria-label={label}
    />
  );
}

export function Skeleton({ lines = 3, height }: { lines?: number; height?: number }) {
  return (
    <div className="skeleton" aria-busy="true" aria-label="Loading">
      {Array.from({ length: lines }, (_, i) => (
        <span
          key={i}
          className="skeleton-line"
          style={{ width: `${92 - ((i * 17) % 40)}%`, height }}
        />
      ))}
    </div>
  );
}

export function EmptyState({
  icon,
  title,
  children,
  action,
}: {
  icon?: ReactNode;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      {icon ? <div className="empty-icon">{icon}</div> : null}
      <p className="empty-title">{title}</p>
      {children ? <div className="empty-text">{children}</div> : null}
      {action ? <div className="empty-action">{action}</div> : null}
    </div>
  );
}

export function errorMessage(error: unknown): string {
  if (error instanceof CodiApiError) {
    if (error.code === "network") return "The runtime is unreachable. Is `codi dev` running?";
    if (error.code === "forbidden") return error.message || "You do not have access to this.";
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

export function ErrorState({
  error,
  onRetry,
  title = "Could not load this",
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
}) {
  const forbidden = error instanceof CodiApiError && error.code === "forbidden";
  return (
    <div className="error-state" role="alert">
      <IconAlert size={18} />
      <div>
        <p className="error-title">{forbidden ? "Not available with your role" : title}</p>
        <p className="error-text">{errorMessage(error)}</p>
      </div>
      {onRetry && !forbidden ? (
        <Button size="sm" variant="ghost" onClick={onRetry}>
          Retry
        </Button>
      ) : null}
    </div>
  );
}

export function Callout({
  tone = "info",
  title,
  children,
  action,
}: {
  tone?: "info" | "warn" | "bad" | "ok";
  title?: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
}) {
  const Icon = tone === "ok" ? IconCheck : tone === "info" ? IconInfo : IconAlert;
  return (
    <div className={`callout callout-${tone}`} role={tone === "bad" ? "alert" : undefined}>
      <Icon size={16} />
      <div className="callout-body">
        {title ? <p className="callout-title">{title}</p> : null}
        {children ? <div className="callout-text">{children}</div> : null}
      </div>
      {action ? <div className="callout-action">{action}</div> : null}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── time & ids

const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
const STEPS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 31536000],
  ["month", 2592000],
  ["week", 604800],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
  ["second", 1],
];

export function relativeTime(iso: string, now = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const diff = Math.round((t - now) / 1000);
  if (Math.abs(diff) < 10) return "just now";
  for (const [unit, secs] of STEPS) {
    if (Math.abs(diff) >= secs || unit === "second")
      return rtf.format(Math.round(diff / secs), unit);
  }
  return "—";
}

function useNow(interval = 30000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(id);
  }, [interval]);
  return now;
}

export function Time({ iso }: { iso: string }) {
  const now = useNow();
  if (!iso || iso.startsWith("0001-")) return <span className="muted">never</span>;
  const date = new Date(iso);
  return (
    <time dateTime={iso} title={Number.isNaN(date.getTime()) ? iso : date.toLocaleString()}>
      {relativeTime(iso, now)}
    </time>
  );
}

export function Sha({ id, long }: { id: string; long?: boolean }) {
  if (!id) return <span className="muted">—</span>;
  return (
    <code className="sha" title={id}>
      {long || !/^[0-9a-f]{40}$/i.test(id) ? id : id.slice(0, 7)}
    </code>
  );
}

export function Avatar({
  name,
  url,
  size = 24,
  bot,
}: {
  name: string;
  url?: string;
  size?: number;
  bot?: boolean;
}) {
  const initials = (name || "?").trim().slice(0, 1).toUpperCase();
  let hash = 0;
  for (const ch of name) hash = (hash * 31 + ch.charCodeAt(0)) >>> 0;
  const hue = hash % 360;
  return (
    <span
      className="avatar"
      data-bot={bot || undefined}
      style={{
        width: size,
        height: size,
        fontSize: Math.round(size * 0.44),
        background: url ? undefined : `oklch(0.62 0.12 ${hue})`,
      }}
      aria-hidden="true"
    >
      {url ? <img src={url} alt="" /> : initials}
    </span>
  );
}

// ───────────────────────────────────────────────────────────── menu

export function Menu({
  trigger,
  children,
  align = "end",
  label,
}: {
  trigger: (props: {
    onClick: () => void;
    "aria-expanded": boolean;
    "aria-haspopup": "menu";
  }) => ReactNode;
  children: (close: () => void) => ReactNode;
  align?: "start" | "end";
  label: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div className="menu" ref={ref}>
      {trigger({
        onClick: () => setOpen((o) => !o),
        "aria-expanded": open,
        "aria-haspopup": "menu",
      })}
      {open ? (
        <div className={`menu-pop menu-${align}`} role="menu" aria-label={label}>
          {children(() => setOpen(false))}
        </div>
      ) : null}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── toasts

export interface Toast {
  id: number;
  tone: "ok" | "bad" | "info";
  title: string;
  text?: ReactNode;
}

interface ToastApi {
  push: (toast: Omit<Toast, "id">) => void;
}

const ToastContext = createContext<ToastApi>({ push: () => undefined });

export function useToast(): ToastApi {
  return useContext(ToastContext);
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const next = useRef(1);
  const dismiss = useCallback((id: number) => setToasts((t) => t.filter((x) => x.id !== id)), []);
  const push = useCallback(
    (toast: Omit<Toast, "id">) => {
      const id = next.current++;
      setToasts((t) => [...t.slice(-3), { ...toast, id }]);
      window.setTimeout(() => dismiss(id), toast.tone === "bad" ? 9000 : 5000);
    },
    [dismiss],
  );
  const api = useMemo(() => ({ push }), [push]);
  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="toasts" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast toast-${t.tone}`} role="status">
            {t.tone === "ok" ? <IconCheck /> : t.tone === "bad" ? <IconAlert /> : <IconInfo />}
            <div className="toast-body">
              <p className="toast-title">{t.title}</p>
              {t.text ? <div className="toast-text">{t.text}</div> : null}
            </div>
            <button className="toast-close" aria-label="Dismiss" onClick={() => dismiss(t.id)}>
              <IconX size={12} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

// ───────────────────────────────────────────────────────────── tabs

export function Tabs<T extends string>({
  tabs,
  value,
  onChange,
  label,
}: {
  tabs: { id: T; label: ReactNode; count?: number }[];
  value: T;
  onChange: (id: T) => void;
  label: string;
}) {
  return (
    <div className="tabs" role="tablist" aria-label={label}>
      {tabs.map((t) => (
        <button
          key={t.id}
          role="tab"
          type="button"
          aria-selected={t.id === value}
          className="tab"
          onClick={() => onChange(t.id)}
        >
          {t.label}
          {t.count !== undefined ? <span className="tab-count">{t.count}</span> : null}
        </button>
      ))}
    </div>
  );
}
