/** Building blocks of the setup wizard (and of the Diagnostics page). */
import { CodiApiError } from "@codi/client";
import {
  AlertTriangle,
  Check as CheckIcon,
  CircleDashed,
  CircleSlash,
  ExternalLink,
  FlaskConical,
  Loader2,
  OctagonAlert,
} from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { CopyButton } from "../components/common.tsx";
import { Callout } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { useTranslation } from "../i18n/index.ts";
import { describeError } from "../lib/errors.ts";
import { cn } from "../lib/utils.ts";
import type { Check, CheckStatus } from "./api.ts";

export function StepHeading({ title, lead }: { title: string; lead?: ReactNode }) {
  return (
    <header className="mb-6">
      <h1 className="text-xl font-semibold tracking-[-0.01em] text-balance">{title}</h1>
      {lead ? (
        <p className="mt-2 max-w-[62ch] text-[14px] leading-relaxed text-muted-foreground">
          {lead}
        </p>
      ) : null}
    </header>
  );
}

/** Numbered instructions ("1. Open the portal…"). */
export function Instructions({ children }: { children: ReactNode }) {
  return <ol className="flex flex-col gap-3">{children}</ol>;
}

export function Instruction({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span
        className="mt-px inline-grid size-5 shrink-0 place-items-center rounded-full bg-brand-soft text-[11px] font-semibold text-brand"
        aria-hidden="true"
      >
        {n}
      </span>
      <div className="min-w-0 flex-1 text-[13px] leading-relaxed">{children}</div>
    </li>
  );
}

export function ExternalButton({
  href,
  children,
  variant = "outline",
  size = "sm",
}: {
  href: string;
  children: ReactNode;
  variant?: ComponentProps<typeof Button>["variant"];
  size?: ComponentProps<typeof Button>["size"];
}) {
  return (
    <Button asChild variant={variant} size={size}>
      <a href={href} target="_blank" rel="noreferrer noopener">
        {children}
        <ExternalLink className="size-3.5" aria-hidden="true" />
      </a>
    </Button>
  );
}

/** A value to copy (redirect URL, command). */
export function CopyField({ value, label }: { value: string; label?: string }) {
  return (
    <div className="mt-2 flex min-w-0 items-center gap-2 rounded-md border bg-surface-sunken py-1 pr-1 pl-3">
      <code className="min-w-0 flex-1 truncate font-mono text-[12.5px]" aria-label={label}>
        {value}
      </code>
      <CopyButton text={value} />
    </div>
  );
}

/** Buttons that stand in for discord.com in `codi dev --sim --setup`. */
export function SimButton(props: ComponentProps<typeof Button>) {
  return (
    <Button variant="outline" size="sm" {...props} className={cn("border-dashed", props.className)}>
      <FlaskConical className="text-violet" />
      {props.children}
    </Button>
  );
}

const STATUS_ICON: Record<CheckStatus, { Icon: typeof CheckIcon; cls: string; spin?: boolean }> = {
  ok: { Icon: CheckIcon, cls: "bg-success-soft text-success" },
  warn: { Icon: AlertTriangle, cls: "bg-warning-soft text-warning" },
  fail: { Icon: OctagonAlert, cls: "bg-danger-soft text-danger" },
  pending: { Icon: Loader2, cls: "bg-info-soft text-info", spin: true },
  skip: { Icon: CircleSlash, cls: "bg-surface-2 text-subtle-foreground" },
};

export function StatusDot({ status }: { status: CheckStatus }) {
  const { t } = useTranslation();
  const s = STATUS_ICON[status] ?? { Icon: CircleDashed, cls: "bg-surface-2" };
  return (
    <span
      className={cn("inline-grid size-6 shrink-0 place-items-center rounded-full", s.cls)}
      role="img"
      aria-label={t(`setup.checks.status.${status}`)}
    >
      <s.Icon className={cn("size-3.5", s.spin && "animate-spin")} aria-hidden="true" />
    </span>
  );
}

const TONE: Record<CheckStatus, "success" | "warning" | "danger" | "info" | "neutral"> = {
  ok: "success",
  warn: "warning",
  fail: "danger",
  pending: "info",
  skip: "neutral",
};

/** A live checklist: one row per check with its explanation and fix. */
export function Checklist({
  checks,
  extra,
  label,
}: {
  checks: Check[];
  extra?: Partial<Record<string, ReactNode>>;
  label?: string;
}) {
  const { t } = useTranslation();
  return (
    <ul className="divide-y rounded-lg border bg-card" aria-label={label} aria-live="polite">
      {checks.map((c) => (
        <li key={c.id} className="flex gap-3 px-4 py-3" data-check={c.id} data-status={c.status}>
          <StatusDot status={c.status} />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <p className="text-[13px] font-semibold">
                {t(`setup.checks.${c.id}` as "setup.checks.token", { defaultValue: c.id })}
              </p>
              {c.status !== "ok" ? (
                <Badge tone={TONE[c.status]}>{t(`setup.checks.status.${c.status}`)}</Badge>
              ) : null}
            </div>
            <p className="mt-0.5 text-[13px] text-foreground/90">{c.message}</p>
            {c.hint && c.status !== "ok" ? (
              <p className="mt-1 text-[13px] text-muted-foreground">{c.hint}</p>
            ) : null}
            {c.link && (c.status === "fail" || c.status === "warn") ? (
              <div className="mt-2">
                <ExternalButton href={c.link}>{t("setup.checks.fix")}</ExternalButton>
              </div>
            ) : null}
            {extra?.[c.id] ? <div className="mt-2">{extra[c.id]}</div> : null}
          </div>
        </li>
      ))}
    </ul>
  );
}

/** An API error explained: message, hint, link and technical details. */
export function SetupError({ error, className }: { error: unknown; className?: string }) {
  const { t } = useTranslation();
  let d = describeError(error);
  // The setup endpoints explain every error themselves (message + hint): keep their words
  // even for codes the console words generically (unauthenticated, conflict…).
  if (error instanceof CodiApiError && error.hint && error.code !== "network") {
    d = {
      ...d,
      message: error.message,
      hint: error.hint,
      ...(error.details ? { details: error.details } : {}),
    };
  }
  let link: string | undefined;
  if (error instanceof CodiApiError && error.body && typeof error.body === "object") {
    const e = (error.body as { error?: { link?: unknown } }).error;
    if (e && typeof e.link === "string") link = e.link;
  }
  return (
    <Callout
      tone={d.kind === "network" || d.kind === "unavailable" ? "warning" : "danger"}
      title={d.message || d.title}
      hint={d.hint}
      details={d.details}
      className={className}
      action={
        link ? <ExternalButton href={link}>{t("setup.checks.fix")}</ExternalButton> : undefined
      }
    />
  );
}

/** Footer of a step: back on the left, primary action on the right. */
export function StepFooter({
  onBack,
  children,
}: {
  onBack?: (() => void) | undefined;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="mt-8 flex items-center justify-between gap-3 border-t pt-5">
      {onBack ? (
        <Button variant="ghost" onClick={onBack}>
          {t("setup.back")}
        </Button>
      ) : (
        <span />
      )}
      <div className="flex items-center gap-2">{children}</div>
    </div>
  );
}
