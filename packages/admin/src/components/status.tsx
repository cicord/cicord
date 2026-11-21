/**
 * The status taxonomy: one visual language for every state the console shows.
 *
 *   tone     success | warning | danger | info | neutral | violet   (same meaning everywhere)
 *   sync     Synced · Out of sync · Unknown                 (icon + label: git vs Discord)
 *   health   Healthy · Progressing · Degraded · Missing     (dot + label: is it working)
 *   history  Succeeded · Failed · Rejected                  (icon + label: what happened)
 *   action   create · update · delete · adopt · reorder · unmanage · blocked (glyph + label)
 *
 * Every badge carries a tooltip with its definition; <StatusLegend> lists them all.
 */
import {
  ArrowDownUp,
  Ban,
  CircleCheck,
  CircleDashed,
  CircleHelp,
  CircleX,
  Link2,
  Link2Off,
  Lock,
  Minus,
  PenLine,
  Plus,
  RefreshCw,
  type LucideIcon,
} from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { Badge, type BadgeTone } from "./ui/badge.tsx";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover.tsx";
import { Tip } from "./ui/tooltip.tsx";

export type Tone = "success" | "warning" | "danger" | "info" | "neutral" | "violet";

const TEXT: Record<Tone, string> = {
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
  info: "text-info",
  neutral: "text-subtle-foreground",
  violet: "text-violet",
};
const BG: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
  info: "bg-info",
  neutral: "bg-subtle-foreground",
  violet: "bg-violet",
};

export const SYNC_STATES = ["Synced", "OutOfSync", "Unknown"] as const;
export const HEALTH_STATES = ["Healthy", "Progressing", "Degraded", "Missing"] as const;
export const HISTORY_STATES = ["succeeded", "failed", "rejected"] as const;
export const ACTIONS = [
  "create",
  "update",
  "delete",
  "adopt",
  "reorder",
  "unmanage",
  "blocked",
] as const;

type SyncState = (typeof SYNC_STATES)[number];
type HealthState = (typeof HEALTH_STATES)[number];
type HistoryState = (typeof HISTORY_STATES)[number];
type ActionName = (typeof ACTIONS)[number];

export const syncMeta: Record<SyncState, { tone: Tone; icon: LucideIcon }> = {
  Synced: { tone: "success", icon: CircleCheck },
  OutOfSync: { tone: "warning", icon: RefreshCw },
  Unknown: { tone: "neutral", icon: CircleDashed },
};

export const healthMeta: Record<HealthState, { tone: Tone }> = {
  Healthy: { tone: "success" },
  Progressing: { tone: "info" },
  Degraded: { tone: "danger" },
  Missing: { tone: "warning" },
};

export const historyMeta: Record<HistoryState, { tone: Tone; icon: LucideIcon }> = {
  succeeded: { tone: "success", icon: CircleCheck },
  failed: { tone: "danger", icon: CircleX },
  rejected: { tone: "danger", icon: Ban },
};

export const actionMeta: Record<ActionName, { tone: Tone; icon: LucideIcon }> = {
  create: { tone: "success", icon: Plus },
  update: { tone: "info", icon: PenLine },
  delete: { tone: "danger", icon: Minus },
  adopt: { tone: "violet", icon: Link2 },
  reorder: { tone: "neutral", icon: ArrowDownUp },
  unmanage: { tone: "warning", icon: Link2Off },
  blocked: { tone: "danger", icon: Lock },
};

function isKey<T extends string>(list: readonly T[], v: string): v is T {
  return (list as readonly string[]).includes(v);
}

export function healthTone(health: string): Tone {
  return isKey(HEALTH_STATES, health) ? healthMeta[health].tone : "neutral";
}
export function syncTone(sync: string): Tone {
  return isKey(SYNC_STATES, sync) ? syncMeta[sync].tone : "neutral";
}

/** A 6px status dot (the health glyph, also used in the server switcher). */
export function StatusDot({
  tone,
  className,
  label,
  pulse,
}: {
  tone: Tone;
  className?: string;
  label?: string;
  pulse?: boolean;
}) {
  return (
    <span
      className={cn("relative inline-flex size-1.5 shrink-0 rounded-full", BG[tone], className)}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      {pulse ? (
        <span
          className={cn("absolute inset-0 animate-ping rounded-full opacity-60", BG[tone])}
          aria-hidden="true"
        />
      ) : null}
    </span>
  );
}

function StateBadge({
  tone,
  icon: Icon,
  dot,
  label,
  description,
  className,
}: {
  tone: Tone;
  icon?: LucideIcon;
  dot?: boolean;
  label: ReactNode;
  description?: string;
  className?: string;
}) {
  return (
    <Tip content={description}>
      <Badge tone="neutral" className={cn("gap-1.5 text-foreground/90", className)} tabIndex={0}>
        {Icon ? <Icon className={TEXT[tone]} aria-hidden="true" /> : null}
        {dot ? <StatusDot tone={tone} /> : null}
        {label}
      </Badge>
    </Tip>
  );
}

export function SyncBadge({ sync }: { sync: string }) {
  const { t } = useTranslation();
  if (!isKey(SYNC_STATES, sync))
    return <StateBadge tone="neutral" icon={CircleHelp} label={sync} />;
  const m = syncMeta[sync];
  return (
    <StateBadge
      tone={m.tone}
      icon={m.icon}
      label={t(`status.sync.${sync}.label`)}
      description={t(`status.sync.${sync}.description`)}
    />
  );
}

export function HealthBadge({ health }: { health: string }) {
  const { t } = useTranslation();
  if (!isKey(HEALTH_STATES, health))
    return <StateBadge tone="neutral" dot label={health || t("status.unknown")} />;
  return (
    <StateBadge
      tone={healthMeta[health].tone}
      dot
      label={t(`status.health.${health}.label`)}
      description={t(`status.health.${health}.description`)}
    />
  );
}

export function HistoryBadge({ status }: { status: string }) {
  const { t } = useTranslation();
  if (!isKey(HISTORY_STATES, status))
    return <StateBadge tone="neutral" icon={CircleHelp} label={status} />;
  const m = historyMeta[status];
  return (
    <StateBadge
      tone={m.tone}
      icon={m.icon}
      label={t(`status.history.${status}.label`)}
      description={t(`status.history.${status}.description`)}
    />
  );
}

const ACTION_BADGE: Record<Tone, BadgeTone> = {
  success: "success",
  warning: "warning",
  danger: "danger",
  info: "info",
  neutral: "solid",
  violet: "violet",
};

/** Plan action: tinted glyph + verb (the one place where the tone fills the badge). */
export function ActionBadge({ action, className }: { action: string; className?: string }) {
  const { t } = useTranslation();
  if (!isKey(ACTIONS, action))
    return (
      <Badge tone="solid" className={className}>
        {action}
      </Badge>
    );
  const m = actionMeta[action];
  const Icon = m.icon;
  return (
    <Tip content={t(`status.action.${action}.description`)}>
      <Badge
        tone={ACTION_BADGE[m.tone]}
        className={cn("w-[5.5rem] justify-start", className)}
        tabIndex={0}
      >
        <Icon aria-hidden="true" />
        {t(`status.action.${action}.label`)}
      </Badge>
    </Tip>
  );
}

/** "What do these statuses mean?" — every state with its definition. */
export function StatusLegend({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const row = (glyph: ReactNode, label: string, description: string) => (
    <li key={label} className="grid grid-cols-[7.5rem_1fr] items-start gap-3 py-1">
      <span className="flex items-center gap-1.5 text-[13px] font-medium">
        {glyph}
        {label}
      </span>
      <span className="text-[13px] text-muted-foreground">{description}</span>
    </li>
  );
  return (
    <Popover>
      <PopoverTrigger asChild>{children}</PopoverTrigger>
      <PopoverContent align="end" className="w-[min(30rem,calc(100vw-2rem))] p-0">
        <div className="max-h-[70vh] overflow-y-auto p-4">
          <h3 className="text-sm font-semibold">{t("legend.title")}</h3>
          <p className="mt-1 text-[13px] text-muted-foreground">{t("legend.intro")}</p>
          <LegendSection title={t("legend.sync")}>
            {SYNC_STATES.map((s) => {
              const Icon = syncMeta[s].icon;
              return row(
                <Icon className={cn("size-3.5", TEXT[syncMeta[s].tone])} />,
                t(`status.sync.${s}.label`),
                t(`status.sync.${s}.description`),
              );
            })}
          </LegendSection>
          <LegendSection title={t("legend.health")}>
            {HEALTH_STATES.map((s) =>
              row(
                <StatusDot tone={healthMeta[s].tone} className="mx-1" />,
                t(`status.health.${s}.label`),
                t(`status.health.${s}.description`),
              ),
            )}
          </LegendSection>
          <LegendSection title={t("legend.history")}>
            {HISTORY_STATES.map((s) => {
              const Icon = historyMeta[s].icon;
              return row(
                <Icon className={cn("size-3.5", TEXT[historyMeta[s].tone])} />,
                t(`status.history.${s}.label`),
                t(`status.history.${s}.description`),
              );
            })}
          </LegendSection>
          <LegendSection title={t("legend.actions")}>
            {ACTIONS.map((a) => {
              const Icon = actionMeta[a].icon;
              return row(
                <Icon className={cn("size-3.5", TEXT[actionMeta[a].tone])} />,
                t(`status.action.${a}.label`),
                t(`status.action.${a}.description`),
              );
            })}
          </LegendSection>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function LegendSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mt-4 border-t pt-3">
      <h4 className="mb-1 text-xs font-medium text-subtle-foreground">{title}</h4>
      <ul>{children}</ul>
    </section>
  );
}
