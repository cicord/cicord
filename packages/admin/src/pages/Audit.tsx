/** Audit log: who changed what on this server (runtime database, not git). */
import type { AuditEntry } from "@codi/client";
import { useCodi } from "@codi/react";
import { ExternalLink, RotateCw, ScrollText } from "lucide-react";
import { useState, type ReactNode } from "react";
import { AUDIT_PAGE, keys, useAudit } from "../api.ts";
import { EmptyState, LoadingRows, Mono, Page, PageHeader, Time } from "../components/common.tsx";
import { ErrorState } from "../components/errors.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card } from "../components/ui/card.tsx";
import { TableCell, TableHead, TableRow } from "../components/ui/table.tsx";
import { i18n, useTranslation } from "../i18n/index.ts";
import { jsonText } from "../schema-form/index.ts";

export function AuditPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const [cursors, setCursors] = useState<string[]>([""]);
  const { cache } = useCodi();
  return (
    <Page>
      <PageHeader
        title={t("audit.title")}
        description={t("audit.description")}
        actions={
          <Button
            variant="outline"
            onClick={() => {
              setCursors([""]);
              void cache.invalidate(keys.audit(serverKey, "").slice(0, 4));
            }}
          >
            <RotateCw />
            {t("common.refresh")}
          </Button>
        }
      />
      <Card className="overflow-hidden">
        <div className="relative w-full overflow-x-auto">
          <table className="w-full text-[13px]">
            <thead className="border-b">
              <TableRow className="hover:bg-transparent">
                <TableHead>{t("audit.columns.when")}</TableHead>
                <TableHead>{t("audit.columns.actor")}</TableHead>
                <TableHead>{t("audit.columns.action")}</TableHead>
                <TableHead>{t("audit.columns.target")}</TableHead>
                <TableHead>{t("audit.columns.details")}</TableHead>
              </TableRow>
            </thead>
            {cursors.map((before, i) => (
              <AuditChunk
                key={before || "first"}
                serverKey={serverKey}
                before={before}
                last={i === cursors.length - 1}
                onMore={(id) => setCursors((c) => [...c, id])}
              />
            ))}
          </table>
        </div>
      </Card>
    </Page>
  );
}

function AuditChunk({
  serverKey,
  before,
  last,
  onMore,
}: {
  serverKey: string;
  before: string;
  last: boolean;
  onMore: (before: string) => void;
}) {
  const { t } = useTranslation();
  const audit = useAudit(serverKey, before);
  const full = (node: ReactNode) => (
    <tbody>
      <tr>
        <td colSpan={5}>{node}</td>
      </tr>
    </tbody>
  );
  if (audit.isLoading) return full(<LoadingRows rows={4} />);
  if (!audit.data)
    return full(
      <div className="p-4">
        <ErrorState error={audit.error} onRetry={() => void audit.refetch()} />
      </div>,
    );
  const items = audit.data;
  return (
    <tbody className="[&_tr:last-child]:border-0">
      {items.length === 0 && before === "" ? (
        <tr>
          <td colSpan={5}>
            <EmptyState icon={<ScrollText />} title={t("audit.empty")} />
          </td>
        </tr>
      ) : null}
      {items.map((e) => (
        <AuditRow key={String(e.id)} entry={e} />
      ))}
      {last && items.length >= AUDIT_PAGE ? (
        <tr>
          <td colSpan={5} className="p-2 text-center">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => onMore(String(items[items.length - 1]!.id))}
            >
              {t("audit.more")}
            </Button>
          </td>
        </tr>
      ) : last && before !== "" ? (
        <tr>
          <td colSpan={5} className="p-3 text-center text-xs text-muted-foreground">
            {t("audit.end")}
          </td>
        </tr>
      ) : null}
    </tbody>
  );
}

function actorLabel(actor: string): { label: string; id?: string } {
  if (actor.startsWith("discord:"))
    return { label: i18n.t("audit.discordUser"), id: actor.slice(8) };
  return { label: actor };
}

function AuditRow({ entry }: { entry: AuditEntry }) {
  const { t } = useTranslation();
  const details = Object.entries(entry.details ?? {}).filter(
    ([, v]) => v !== "" && v !== null && !(Array.isArray(v) && v.length === 0),
  );
  const actor = actorLabel(entry.actor);
  return (
    <TableRow className="align-top">
      <TableCell className="whitespace-nowrap text-muted-foreground">
        <Time iso={entry.time} />
      </TableCell>
      <TableCell className="whitespace-nowrap">
        {actor.id ? (
          <span>
            {actor.label} <Mono className="tabular">{actor.id}</Mono>
          </span>
        ) : (
          <Mono className="text-foreground">{actor.label}</Mono>
        )}
      </TableCell>
      <TableCell>
        <span className="font-medium">
          {t(`audit.actions.${entry.action as "sync.apply"}`, { defaultValue: entry.action })}
        </span>
        <Mono className="block text-[11px]">{entry.action}</Mono>
      </TableCell>
      <TableCell>
        <Mono className="text-foreground">{entry.target}</Mono>
      </TableCell>
      <TableCell className="min-w-56">
        {details.length === 0 ? (
          <span className="text-subtle-foreground">—</span>
        ) : (
          <dl className="flex flex-wrap gap-1.5">
            {details.map(([k, v]) => (
              <div
                key={k}
                className="inline-flex max-w-full items-center gap-1 rounded-md border bg-surface-2 px-1.5 py-0.5 text-xs"
              >
                <dt className="text-muted-foreground">{k}</dt>
                <dd className="min-w-0 truncate">
                  {k === "pullRequestUrl" && typeof v === "string" && v ? (
                    <a
                      href={v}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-0.5 text-brand hover:underline"
                    >
                      {t("audit.pullRequest")}
                      <ExternalLink className="size-3" />
                    </a>
                  ) : (
                    <span>{Array.isArray(v) ? v.map(jsonText).join(", ") : jsonText(v)}</span>
                  )}
                </dd>
              </div>
            ))}
          </dl>
        )}
      </TableCell>
    </TableRow>
  );
}
