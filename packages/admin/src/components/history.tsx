import type { SyncRecord } from "@cicord/client";
import { Bot, GitCommitHorizontal, Server } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "../i18n/index.ts";
import { groupHistory, parseTarget, type HistoryGroup } from "../lib/history.ts";
import { cn } from "../lib/utils.ts";
import { Sha, Time } from "./common.tsx";
import { TechnicalDetails } from "./errors.tsx";
import { historyMeta, HISTORY_STATES, isFailure } from "./status.tsx";
import { Tip } from "./ui/tooltip.tsx";

const TONE_TEXT = {
  success: "text-success",
  danger: "text-danger",
  warning: "text-warning",
} as const;

function statusIcon(status: string) {
  const known = (HISTORY_STATES as readonly string[]).includes(status);
  return known ? historyMeta[status as (typeof HISTORY_STATES)[number]] : historyMeta.failed;
}

/** Sync history, consecutive identical records grouped ("Failed 4 times · last 12 min ago"). */
export function HistoryList({
  records,
  limit,
  showTarget = true,
  className,
}: {
  records: readonly SyncRecord[];
  limit?: number;
  showTarget?: boolean;
  className?: string;
}) {
  const groups = useMemo(() => groupHistory(records), [records]);
  const shown = limit ? groups.slice(0, limit) : groups;
  return (
    <ol className={cn("divide-y", className)} aria-label={undefined}>
      {shown.map((g) => (
        <HistoryRow key={g.key} group={g} showTarget={showTarget} />
      ))}
    </ol>
  );
}

function TargetLabel({ target }: { target: string }) {
  const { t } = useTranslation();
  const { kind, key } = parseTarget(target);
  const Icon = kind === "bot" ? Bot : kind === "revision" ? GitCommitHorizontal : Server;
  const label =
    kind === "server"
      ? t("history.target.server", { key })
      : kind === "bot"
        ? t("history.target.bot", { key })
        : kind === "revision"
          ? t("history.target.revision")
          : target;
  return (
    <span className="inline-flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
      <Icon className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="truncate">{label}</span>
    </span>
  );
}

function HistoryRow({ group: g, showTarget }: { group: HistoryGroup; showTarget: boolean }) {
  const { t } = useTranslation();
  const meta = statusIcon(g.status);
  const Icon = meta.icon;
  const failed = isFailure(g.status);
  const title = failed
    ? (g.error?.message ?? g.latest.message)
    : g.latest.message || t("status.history.succeeded.label");
  const statusLabel = t(`status.history.${g.status}.label`, { defaultValue: g.status });
  return (
    <li className="group/row flex gap-3 px-4 py-3" data-status={g.status}>
      <Tip content={t(`status.history.${g.status}.description`, { defaultValue: "" })}>
        <span className="mt-0.5 shrink-0" tabIndex={0} aria-label={statusLabel}>
          <Icon
            className={cn("size-4", TONE_TEXT[meta.tone as keyof typeof TONE_TEXT])}
            aria-hidden="true"
          />
        </span>
      </Tip>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span
            className={cn("text-xs font-medium", failed ? "text-danger" : "text-muted-foreground")}
          >
            {g.count > 1
              ? t("history.repeated", { status: statusLabel, count: g.count })
              : statusLabel}
          </span>
          {showTarget ? <TargetLabel target={g.target} /> : null}
          <span className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
            {g.count > 1 ? <span>{t("history.last")}</span> : null}
            <Time iso={g.latest.time} />
          </span>
        </div>
        <p
          className={cn(
            "text-[13px] break-words",
            failed ? "font-medium text-foreground" : "text-foreground/90",
          )}
        >
          {title}
        </p>
        {failed && g.error?.hint ? (
          <p className="text-[13px] text-muted-foreground">{g.error.hint}</p>
        ) : null}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          {failed && g.error?.details ? (
            <div className="min-w-0 flex-1 basis-full">
              <TechnicalDetails
                text={g.error.details}
                extra={<RevisionInfo group={g} className="ml-auto" />}
              />
            </div>
          ) : (
            <RevisionInfo group={g} />
          )}
        </div>
      </div>
    </li>
  );
}

function RevisionInfo({ group: g, className }: { group: HistoryGroup; className?: string }) {
  const { t } = useTranslation();
  if (!g.revisions.length) return null;
  return (
    <span
      className={cn("inline-flex items-center gap-1.5 text-xs text-muted-foreground", className)}
    >
      <Sha id={g.revisions[0]!} icon />
      {g.revisions.length > 1 ? (
        <span>{t("history.moreRevisions", { count: g.revisions.length - 1 })}</span>
      ) : null}
      {g.count > 1 ? (
        <span className="hidden sm:inline">
          · {t("history.since")} <Time iso={g.oldest.time} />
        </span>
      ) : null}
    </span>
  );
}
