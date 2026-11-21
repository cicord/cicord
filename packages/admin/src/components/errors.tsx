import type { HumanError } from "@codi/client";
import { AlertTriangle, ChevronRight, Info, OctagonAlert, RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { useTranslation } from "../i18n/index.ts";
import { describeError, type DescribedError } from "../lib/errors.ts";
import { cn } from "../lib/utils.ts";
import { CopyButton } from "./common.tsx";
import { Button } from "./ui/button.tsx";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./ui/collapsible.tsx";

/** Collapsed raw text with a copy button. */
export function TechnicalDetails({ text, extra }: { text: string; extra?: ReactNode }) {
  const { t } = useTranslation();
  return (
    <Collapsible className="group/details">
      <div className="flex items-center gap-2">
        <CollapsibleTrigger className="inline-flex items-center gap-1 rounded text-xs font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
          <ChevronRight
            className="size-3.5 transition-transform group-data-[state=open]/details:rotate-90"
            aria-hidden="true"
          />
          {t("errors.details")}
        </CollapsibleTrigger>
        {extra}
      </div>
      <CollapsibleContent>
        <div className="mt-2 rounded-md border bg-surface-sunken">
          <div className="flex justify-end border-b px-1 py-0.5">
            <CopyButton text={text} />
          </div>
          <pre className="max-h-64 overflow-auto p-3 font-mono text-[12px] leading-relaxed break-words whitespace-pre-wrap text-muted-foreground">
            {text}
          </pre>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

const TONE = {
  danger: { box: "border-danger/30 bg-danger-soft", icon: "text-danger", Icon: OctagonAlert },
  warning: { box: "border-warning/30 bg-warning-soft", icon: "text-warning", Icon: AlertTriangle },
  info: { box: "border-border bg-surface-2", icon: "text-info", Icon: Info },
} as const;

/** Inline explanation of a problem: message, hint, collapsed technical details. */
export function Callout({
  tone = "danger",
  title,
  children,
  hint,
  details,
  meta,
  action,
  className,
}: {
  tone?: keyof typeof TONE;
  title: ReactNode;
  children?: ReactNode;
  hint?: ReactNode;
  details?: string | undefined;
  meta?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  const s = TONE[tone];
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      className={cn("flex gap-3 rounded-lg border px-4 py-3", s.box, className)}
    >
      <s.Icon className={cn("mt-0.5 size-4 shrink-0", s.icon)} aria-hidden="true" />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
          <p className="text-[13px] font-semibold text-foreground">{title}</p>
          {meta ? <div className="text-xs text-muted-foreground">{meta}</div> : null}
        </div>
        {children ? <div className="text-[13px] text-foreground/90">{children}</div> : null}
        {hint ? <p className="text-[13px] text-muted-foreground">{hint}</p> : null}
        {details ? <TechnicalDetails text={details} /> : null}
      </div>
      {action ? <div className="shrink-0 self-center">{action}</div> : null}
    </div>
  );
}

/** A HumanError from the runtime (status, history, plan) as a callout. */
export function HumanErrorCallout({
  error,
  title,
  meta,
  action,
  tone = "danger",
  className,
}: {
  error: HumanError;
  title?: ReactNode;
  meta?: ReactNode;
  action?: ReactNode;
  tone?: "danger" | "warning";
  className?: string;
}) {
  const { t } = useTranslation();
  const failures = error.failures && error.failures > 1 ? error.failures : 0;
  return (
    <Callout
      tone={tone}
      title={title ?? error.message}
      hint={error.hint}
      details={error.details}
      meta={meta}
      action={action}
      {...(className ? { className } : {})}
    >
      {title ? <p>{error.message}</p> : null}
      {failures ? (
        <p className="text-muted-foreground">
          {t("errors.otherFailures", { count: failures - 1 })}
        </p>
      ) : null}
    </Callout>
  );
}

/** A failed query: friendly text by error kind + retry. */
export function ErrorState({
  error,
  onRetry,
  title,
  className,
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const d = describeError(error);
  const canRetry = onRetry && d.kind !== "forbidden" && d.kind !== "not_found";
  return (
    <Callout
      tone={d.kind === "forbidden" || d.kind === "not_found" ? "info" : "danger"}
      title={d.kind === "forbidden" ? d.title : (title ?? d.title)}
      hint={d.hint}
      details={d.details}
      className={className}
      action={
        canRetry ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RotateCw />
            {t("common.retry")}
          </Button>
        ) : undefined
      }
    >
      {d.message}
    </Callout>
  );
}

/** Toast body: the message, then the hint on its own line. */
export function ToastText({ message, hint }: { message: string; hint?: string | undefined }) {
  return (
    <span className="flex flex-col gap-1">
      <span className="text-foreground/90">{message}</span>
      {hint ? <span>{hint}</span> : null}
    </span>
  );
}

/** Toast for a failed action (`title` describes the action: "Sync failed"). */
export function toastError(error: unknown, title?: string): DescribedError {
  const d = describeError(error);
  toast.error(title ?? d.title, {
    description: <ToastText message={d.message} hint={d.hint} />,
    duration: 9000,
  });
  return d;
}
