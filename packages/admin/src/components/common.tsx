/** Small shared pieces: page header, empty state, time, ids, entity avatars, copy. */
import { Bot, Check, Copy, GitCommitHorizontal, Puzzle } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { formatDateTime, formatRelative, isNever, useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { Button } from "./ui/button.tsx";
import { Skeleton } from "./ui/skeleton.tsx";
import { Tip } from "./ui/tooltip.tsx";

export function PageHeader({
  title,
  description,
  actions,
  leading,
  meta,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  leading?: ReactNode;
  meta?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex min-w-0 items-start gap-3">
        {leading}
        <div className="min-w-0">
          <h1 className="truncate text-xl font-semibold tracking-[-0.01em]">{title}</h1>
          {description ? (
            <p className="mt-1 max-w-[70ch] text-[13px] text-muted-foreground">{description}</p>
          ) : null}
          {meta ? <div className="mt-2 flex flex-wrap items-center gap-2">{meta}</div> : null}
        </div>
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}

export function Page({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={cn(
        "mx-auto flex w-full max-w-6xl flex-col gap-6 px-4 py-6 md:px-8 md:py-8",
        className,
      )}
    >
      {children}
    </div>
  );
}

export function EmptyState({
  icon,
  title,
  children,
  action,
  className,
}: {
  icon?: ReactNode;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center px-6 py-10 text-center", className)}>
      {icon ? (
        <div className="mb-3 grid size-9 place-items-center rounded-lg border bg-surface-2 text-muted-foreground [&_svg]:size-4">
          {icon}
        </div>
      ) : null}
      <p className="text-sm font-medium">{title}</p>
      {children ? (
        <div className="mt-1 max-w-[52ch] text-[13px] text-muted-foreground">{children}</div>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}

export function LoadingRows({ rows = 4, className }: { rows?: number; className?: string }) {
  const { t } = useTranslation();
  return (
    <div
      className={cn("flex flex-col gap-2.5 p-4", className)}
      aria-busy="true"
      aria-label={t("common.loading")}
    >
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-4" style={{ width: `${92 - ((i * 17) % 40)}%` }} />
      ))}
    </div>
  );
}

function useNow(interval = 30000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(id);
  }, [interval]);
  return now;
}

/** Relative time with the absolute date in a tooltip. */
export function Time({ iso, className }: { iso: string | undefined; className?: string }) {
  const { t } = useTranslation();
  const now = useNow();
  if (isNever(iso))
    return <span className={cn("text-muted-foreground", className)}>{t("time.never")}</span>;
  return (
    <Tip content={formatDateTime(iso!)}>
      <time dateTime={iso} className={cn("tabular", className)}>
        {formatRelative(iso!, Math.max(now, Date.now()))}
      </time>
    </Tip>
  );
}

/** A revision id (git SHA shortened to 7). */
export function Sha({ id, className, icon }: { id: string; className?: string; icon?: boolean }) {
  if (!id) return <span className="text-muted-foreground">—</span>;
  const short = /^[0-9a-f]{40}$/i.test(id) ? id.slice(0, 7) : id;
  return (
    <code
      className={cn(
        "inline-flex items-center gap-1 font-mono text-[12px] text-muted-foreground",
        className,
      )}
      title={id}
    >
      {icon ? <GitCommitHorizontal className="size-3.5" aria-hidden="true" /> : null}
      {short}
    </code>
  );
}

/** A YAML key / Discord id: monospace, muted. */
export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <code className={cn("font-mono text-[12px] text-muted-foreground", className)}>{children}</code>
  );
}

function hue(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return h % 360;
}

/** People: round avatar (image or initial). */
export function UserAvatar({
  name,
  url,
  size = 24,
  className,
}: {
  name: string;
  url?: string;
  size?: number;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-grid shrink-0 place-items-center overflow-hidden rounded-full font-semibold text-white",
        className,
      )}
      style={{
        width: size,
        height: size,
        fontSize: Math.round(size * 0.42),
        background: url ? undefined : `oklch(0.55 0.09 ${hue(name)})`,
      }}
      aria-hidden="true"
    >
      {url ? (
        <img src={url} alt="" className="size-full object-cover" />
      ) : (
        (name || "?").trim().slice(0, 1).toUpperCase()
      )}
    </span>
  );
}

/** Servers: rounded square with the server initial (Discord's server icon shape). */
export function ServerAvatar({
  name,
  url,
  size = 28,
  className,
}: {
  name: string;
  url?: string;
  size?: number;
  className?: string;
}) {
  const initials = (name || "?")
    .split(/\s+/)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
  return (
    <span
      className={cn(
        "inline-grid shrink-0 place-items-center overflow-hidden rounded-[30%] border border-border-strong bg-surface-2 font-semibold text-foreground",
        className,
      )}
      style={{ width: size, height: size, fontSize: Math.round(size * 0.36) }}
      aria-hidden="true"
    >
      {url ? <img src={url} alt="" className="size-full object-cover" /> : initials}
    </span>
  );
}

/** Bots: circle with a bot glyph. */
export function BotAvatar({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <span
      className={cn(
        "inline-grid shrink-0 place-items-center rounded-full border border-border-strong bg-surface-2 text-muted-foreground",
        className,
      )}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <Bot style={{ width: size * 0.5, height: size * 0.5 }} />
    </span>
  );
}

/** Plugin instances: small square tile with the plugin glyph. */
export function PluginGlyph({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <span
      className={cn(
        "inline-grid shrink-0 place-items-center rounded-md border border-dashed border-border-strong text-muted-foreground",
        className,
      )}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <Puzzle style={{ width: size * 0.5, height: size * 0.5 }} />
    </span>
  );
}

export function CopyButton({
  text,
  label,
  className,
}: {
  text: string;
  label?: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (insecure context)
    }
  };
  return (
    <Button
      variant="ghost"
      size="sm"
      className={cn("h-6 px-1.5 text-xs", className)}
      onClick={() => void copy()}
    >
      {copied ? <Check className="text-success" /> : <Copy />}
      {copied ? t("common.copied") : (label ?? t("common.copy"))}
    </Button>
  );
}

/** A labelled fact in a horizontal strip ("Last sync · 3 min ago"). */
export function Fact({
  label,
  children,
  className,
}: {
  label: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-1 px-4 py-3", className)}>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate text-[13px] font-medium">{children}</dd>
    </div>
  );
}
