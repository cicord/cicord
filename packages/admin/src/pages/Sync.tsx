/** Sync section: Plan, History and Drift of a server, plus the "Sync now" action. */
import type {
  Drift,
  HumanError,
  Json,
  Plan,
  PlanBlocked,
  PlanChange,
  SyncRecord,
} from "@cicord/client";
import { useCicord } from "@cicord/react";
import {
  CircleCheck,
  CircleHelp,
  GitCompareArrows,
  Gem,
  History,
  Info,
  Lock,
  RefreshCw,
  RotateCw,
} from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { usePlan, useHistory, useServer, useStatus } from "../api.ts";
import { EmptyState, LoadingRows, Mono, Page, PageHeader } from "../components/common.tsx";
import {
  Callout,
  ErrorState,
  HumanErrorCallout,
  TechnicalDetails,
  ToastText,
  toastError,
} from "../components/errors.tsx";
import { HistoryList } from "../components/history.tsx";
import { ActionBadge, StatusLegend } from "../components/status.tsx";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "../components/ui/alert-dialog.tsx";
import { Button } from "../components/ui/button.tsx";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table.tsx";
import { ToggleGroup, ToggleGroupItem } from "../components/ui/toggle-group.tsx";
import { useWorkspace } from "../context.tsx";
import { i18n, useTranslation } from "../i18n/index.ts";
import { errorOf } from "../lib/errors.ts";
import { parseTarget } from "../lib/history.ts";
import { Link } from "../router.tsx";
import { Swatch } from "../schema-form/index.ts";

// ───────────────────────────────────────────────────────────── sync action

export function SyncButton({ serverKey, size }: { serverKey: string; size?: "sm" | "default" }) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      const res = await client.admin.sync(serverKey);
      // The runtime answers 200 even when Discord refused some changes: the outcome is in
      // the server status (Degraded + error) and the history.
      const after = await client.admin.server(serverKey).catch(() => null);
      const failure = after && after.health === "Degraded" ? errorOf(after) : null;
      if (failure) {
        toast.error(t("sync.toast.failed"), {
          description: <ToastText message={failure.message} hint={failure.hint} />,
          duration: 9000,
        });
      } else {
        const waiting = res.plan.blocked.filter(waitsForBoost).length;
        const blocked = res.plan.blocked.length - waiting;
        const notes = [
          blocked > 0 ? t("sync.toast.blocked", { count: blocked }) : "",
          waiting > 0 ? t("sync.toast.waiting", { count: waiting }) : "",
        ].filter(Boolean);
        toast.success(
          res.applied === 0
            ? t("sync.toast.upToDate")
            : t("sync.toast.applied", { count: res.applied }),
          notes.length > 0 ? { description: notes.join(" ") } : undefined,
        );
      }
    } catch (e) {
      toastError(e, t("sync.toast.failed"));
    } finally {
      // Close at once: the pages refresh in the background (a slow query, e.g. a server
      // plan read from Discord, must not hold the dialog open).
      void cache.invalidate(["admin"]);
      setBusy(false);
      setOpen(false);
    }
  };
  return (
    <AlertDialog open={open} onOpenChange={(o) => !busy && setOpen(o)}>
      <AlertDialogTrigger asChild>
        <Button size={size}>
          <RefreshCw />
          {t("sync.now")}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("sync.confirm.title")}</AlertDialogTitle>
          <AlertDialogDescription>{t("sync.confirm.text")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            disabled={busy}
            onClick={(e) => {
              e.preventDefault();
              void run();
            }}
          >
            {busy ? <RotateCw className="animate-spin" /> : null}
            {t("sync.now")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// ───────────────────────────────────────────────────────────── values

export function ValueView({ value }: { value: Json | undefined }) {
  const { t } = useTranslation();
  if (value === undefined || value === null)
    return <span className="text-subtle-foreground">—</span>;
  if (typeof value === "string") {
    if (/^#[0-9a-f]{6}$/i.test(value))
      return (
        <span className="inline-flex items-center gap-1.5">
          <Swatch color={value} />
          <code className="font-mono text-[12px]">{value}</code>
        </span>
      );
    return value === "" ? (
      <em className="text-subtle-foreground">{t("common.empty")}</em>
    ) : (
      <span className="break-words">{value}</span>
    );
  }
  if (typeof value === "boolean") return <span>{value ? t("common.on") : t("common.off")}</span>;
  return <code className="font-mono text-[12px] break-all">{JSON.stringify(value)}</code>;
}

// ───────────────────────────────────────────────────────────── plan

const KIND_ORDER = ["server", "everyone", "profile", "role", "category", "channel"];

export function groupChanges(changes: readonly PlanChange[]): [string, PlanChange[]][] {
  const groups = new Map<string, PlanChange[]>();
  for (const c of changes) {
    const list = groups.get(c.kind) ?? [];
    list.push(c);
    groups.set(c.kind, list);
  }
  const rank = (k: string) => {
    const i = KIND_ORDER.indexOf(k);
    return i < 0 ? KIND_ORDER.length : i;
  };
  return [...groups.entries()].toSorted(
    (a, b) => rank(a[0]) - rank(b[0]) || a[0].localeCompare(b[0]),
  );
}

function kindLabel(kind: string): string {
  return i18n.t(`plan.kinds.${kind}`, { defaultValue: kind });
}

/** Explanation of a blocked operation: the runtime's error object, else a local fallback. */
function blockedError(b: PlanBlocked): HumanError {
  if (b.error) return b.error;
  const known = ["deletionProtection", "hierarchy", "ownership", "managed", "type change"].find(
    (r) => b.reason.startsWith(r),
  );
  return {
    code: "unknown",
    message: known
      ? i18n.t(`plan.blockedReason.${known.replace(" ", "") as "deletionProtection"}`)
      : b.reason,
    details: b.reason,
  };
}

export function PlanPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const plan = usePlan(serverKey, ws.isAdmin);
  return (
    <Page>
      <PageHeader
        title={t("plan.title")}
        description={t("plan.description")}
        actions={
          ws.isAdmin ? (
            <>
              <StatusLegend>
                <Button variant="ghost" size="icon-sm" aria-label={t("legend.open")}>
                  <CircleHelp />
                </Button>
              </StatusLegend>
              <Button
                variant="outline"
                loading={plan.isFetching}
                onClick={() => void plan.refetch()}
              >
                {plan.isFetching ? null : <RotateCw />}
                {t("common.refresh")}
              </Button>
              <SyncButton serverKey={serverKey} />
            </>
          ) : null
        }
      />
      {ws.server && ws.server.health === "Degraded" && errorOf(ws.server) ? (
        <HumanErrorCallout
          tone="warning"
          title={t("plan.lastSyncFailed")}
          error={errorOf(ws.server)!}
        />
      ) : null}
      {!ws.isAdmin ? (
        <Callout tone="info" title={t("plan.adminOnly.title")}>
          {t("plan.adminOnly.text")}
        </Callout>
      ) : plan.isLoading ? (
        <Card>
          <LoadingRows rows={6} />
        </Card>
      ) : !plan.data ? (
        <ErrorState
          error={plan.error}
          onRetry={() => void plan.refetch()}
          title={t("plan.loadFailed")}
        />
      ) : (
        <PlanView plan={plan.data} />
      )}
    </Page>
  );
}

function PlanView({ plan }: { plan: Plan }) {
  const { t } = useTranslation();
  const waiting = plan.blocked.filter(waitsForBoost);
  const blocked = plan.blocked.filter((b) => !waitsForBoost(b));
  const groups = useMemo(() => groupChanges(plan.changes), [plan.changes]);
  const counts = useMemo(() => {
    const out: Record<string, number> = {};
    for (const c of plan.changes) out[c.action] = (out[c.action] ?? 0) + 1;
    return out;
  }, [plan.changes]);
  return (
    <div className="flex flex-col gap-4">
      {plan.changes.length === 0 ? (
        <Card>
          <EmptyState icon={<CircleCheck />} title={t("plan.empty.title")}>
            {t("plan.empty.text")}
          </EmptyState>
        </Card>
      ) : (
        <>
          <div
            className="flex flex-wrap items-center gap-x-4 gap-y-2"
            aria-label={t("plan.summary")}
          >
            <span className="text-[13px] text-muted-foreground">
              {t("plan.changeCount", { count: plan.changes.length })}
            </span>
            {Object.entries(counts).map(([action, n]) => (
              <span key={action} className="inline-flex items-center gap-1.5">
                <ActionBadge action={action} className="w-auto" />
                <span className="tabular text-[13px] font-medium">{n}</span>
              </span>
            ))}
          </div>
          {groups.map(([kind, changes]) => (
            <Card key={kind} role="region" aria-label={kindLabel(kind)}>
              <CardHeader className="py-2.5">
                <CardTitle>{kindLabel(kind)}</CardTitle>
                <span className="tabular text-[13px] leading-6 text-muted-foreground">
                  {changes.length}
                </span>
              </CardHeader>
              <ul className="divide-y">
                {changes.map((c, i) => (
                  <li key={`${c.key}-${i}`} className="flex flex-col gap-2 px-4 py-3">
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                      <ActionBadge action={c.action} />
                      <Mono className="text-foreground">{c.key}</Mono>
                      <span className="text-[13px] text-muted-foreground">{c.summary}</span>
                    </div>
                    {c.fields && Object.keys(c.fields).length > 0 ? (
                      <FieldDiff fields={c.fields} />
                    ) : null}
                  </li>
                ))}
              </ul>
            </Card>
          ))}
        </>
      )}
      {blocked.length > 0 ? (
        <Card role="region" aria-label={t("plan.blocked.title")} className="border-danger/25">
          <CardHeader className="py-2.5">
            <Lock className="mt-1 size-4 text-danger" aria-hidden="true" />
            <div>
              <CardTitle>{t("plan.blocked.title")}</CardTitle>
              <CardDescription>{t("plan.blocked.text")}</CardDescription>
            </div>
            <span className="ml-auto tabular text-[13px] leading-6 text-muted-foreground">
              {blocked.length}
            </span>
          </CardHeader>
          <BlockedList items={blocked} />
        </Card>
      ) : null}
      {waiting.length > 0 ? (
        <Card role="region" aria-label={t("plan.boost.title")} className="border-warning/30">
          <CardHeader className="py-2.5">
            <Gem className="mt-1 size-4 text-warning" aria-hidden="true" />
            <div>
              <CardTitle>{t("plan.boost.title")}</CardTitle>
              <CardDescription>{t("plan.boost.text")}</CardDescription>
            </div>
            <span className="ml-auto tabular text-[13px] leading-6 text-muted-foreground">
              {waiting.length}
            </span>
          </CardHeader>
          <BlockedList items={waiting} />
        </Card>
      ) : null}
      {plan.notes && plan.notes.length > 0 ? (
        <Card role="region" aria-label={t("plan.notes.title")}>
          <CardHeader className="py-2.5">
            <Info className="mt-1 size-4 text-info" aria-hidden="true" />
            <div>
              <CardTitle>{t("plan.notes.title")}</CardTitle>
              <CardDescription>{t("plan.notes.text")}</CardDescription>
            </div>
            <span className="ml-auto tabular text-[13px] leading-6 text-muted-foreground">
              {plan.notes.length}
            </span>
          </CardHeader>
          <ul className="divide-y">
            {plan.notes.map((n, i) => (
              <li key={`${n.key}-${i}`} className="flex flex-col gap-1 px-4 py-3">
                <span className="text-xs text-muted-foreground">{kindLabel(n.kind)}</span>
                <p className="text-[13px]">{n.message}</p>
              </li>
            ))}
          </ul>
        </Card>
      ) : null}
    </div>
  );
}

/** The items of a blocked or waiting-for-boosts card. */
function BlockedList({ items }: { items: readonly PlanBlocked[] }) {
  return (
    <ul className="divide-y">
      {items.map((b, i) => {
        const e = blockedError(b);
        return (
          <li key={`${b.key}-${i}`} className="flex flex-col gap-1.5 px-4 py-3">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <ActionBadge action={b.action} />
              <span className="text-xs text-muted-foreground">{kindLabel(b.kind)}</span>
              {b.summary ? (
                <span className="text-[13px] text-foreground">{b.summary}</span>
              ) : (
                <Mono className="text-foreground">{b.key}</Mono>
              )}
            </div>
            <p className="text-[13px] font-medium">{e.message}</p>
            {e.hint ? <p className="text-[13px] text-muted-foreground">{e.hint}</p> : null}
            {b.fields && Object.keys(b.fields).length > 0 ? <FieldDiff fields={b.fields} /> : null}
            {e.details && e.details !== e.message ? <TechnicalDetails text={e.details} /> : null}
          </li>
        );
      })}
    </ul>
  );
}

/** A blocked operation that only needs a higher server boost level: not drift. */
function waitsForBoost(b: PlanBlocked): boolean {
  return b.reason.startsWith("boost:");
}

function FieldDiff({ fields }: { fields: NonNullable<PlanChange["fields"]> }) {
  const { t } = useTranslation();
  return (
    <div className="overflow-x-auto rounded-md border bg-surface-sunken">
      <table className="w-full table-fixed text-[13px]">
        <colgroup>
          <col className="w-24 sm:w-40" />
          <col />
          <col className="w-5" />
          <col />
        </colgroup>
        <tbody className="divide-y">
          {Object.entries(fields).map(([field, d]) => (
            <tr key={field}>
              <th scope="row" className="px-3 py-1.5 text-left align-top font-normal">
                <Mono>{field}</Mono>
              </th>
              <td className="px-3 py-1.5 align-top text-muted-foreground line-through decoration-danger/50">
                <ValueView value={d.from} />
              </td>
              <td
                className="py-1.5 text-center align-top text-subtle-foreground"
                aria-label={t("plan.becomes")}
              >
                →
              </td>
              <td className="px-3 py-1.5 align-top">
                <ValueView value={d.to} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── history

type HistoryFilter = "all" | "server" | "revision" | "bot";

/** History records relevant to a server: its syncs, its bots and the project revisions. */
export function relevantHistory(
  records: readonly SyncRecord[],
  serverKey: string,
  bots: readonly string[],
): SyncRecord[] {
  return records.filter((r) => {
    const { kind, key } = parseTarget(r.target);
    if (kind === "server") return key === serverKey;
    if (kind === "bot") return bots.includes(key);
    return true;
  });
}

function useServerBots(serverKey: string): string[] {
  const status = useStatus();
  return useMemo(
    () => (status.data?.bots ?? []).filter((b) => b.servers.includes(serverKey)).map((b) => b.key),
    [status.data, serverKey],
  );
}

export function HistoryPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const history = useHistory(200);
  const bots = useServerBots(serverKey);
  const [filter, setFilter] = useState<HistoryFilter>("all");
  const records = useMemo(() => {
    const all = relevantHistory(history.data ?? [], serverKey, bots);
    return filter === "all" ? all : all.filter((r) => parseTarget(r.target).kind === filter);
  }, [history.data, serverKey, bots, filter]);
  return (
    <Page>
      <PageHeader
        title={t("history.title")}
        description={t("history.description")}
        actions={
          <StatusLegend>
            <Button variant="ghost" size="icon-sm" aria-label={t("legend.open")}>
              <CircleHelp />
            </Button>
          </StatusLegend>
        }
      />
      <Card>
        <CardHeader className="flex-wrap items-center py-2.5">
          <ToggleGroup
            type="single"
            value={filter}
            onValueChange={(v) => v && setFilter(v as HistoryFilter)}
            aria-label={t("history.filter.label")}
          >
            {(["all", "server", "revision", "bot"] as const).map((f) => (
              <ToggleGroupItem key={f} value={f}>
                {t(`history.filter.${f}`)}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <CardAction>
            <Button
              variant="ghost"
              size="sm"
              loading={history.isFetching}
              onClick={() => void history.refetch()}
            >
              {history.isFetching ? null : <RotateCw />}
              {t("common.refresh")}
            </Button>
          </CardAction>
        </CardHeader>
        {history.isLoading ? (
          <LoadingRows rows={6} />
        ) : !history.data ? (
          <CardContent>
            <ErrorState error={history.error} onRetry={() => void history.refetch()} />
          </CardContent>
        ) : records.length === 0 ? (
          <EmptyState icon={<History />} title={t("history.empty.title")}>
            {t("history.empty.text")}
          </EmptyState>
        ) : (
          <HistoryList records={records} />
        )}
      </Card>
    </Page>
  );
}

// ───────────────────────────────────────────────────────────── drift

function DriftTable({ drift }: { drift: readonly Drift[] }) {
  const { t } = useTranslation();
  return (
    <Table>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead>{t("drift.columns.resource")}</TableHead>
          <TableHead>{t("drift.columns.field")}</TableHead>
          <TableHead>{t("drift.columns.expected")}</TableHead>
          <TableHead>{t("drift.columns.actual")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {drift.map((d, i) => (
          <TableRow key={`${d.kind}-${d.key}-${d.field}-${i}`}>
            <TableCell>
              <span className="text-xs text-muted-foreground">{kindLabel(d.kind)}</span>{" "}
              <Mono className="text-foreground">{d.key}</Mono>
            </TableCell>
            <TableCell>
              {["exists", "order", "managed"].includes(d.field) ? (
                <span className="text-[13px]">{t(`drift.fields.${d.field as "exists"}`)}</span>
              ) : (
                <Mono>{d.field}</Mono>
              )}
            </TableCell>
            <TableCell>
              <ValueView value={d.expected} />
            </TableCell>
            <TableCell className="text-warning">
              <ValueView value={d.actual} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function DriftPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const server = useServer(serverKey);
  const drift = server.data?.drift ?? [];
  return (
    <Page>
      <PageHeader
        title={t("drift.title")}
        description={t("drift.description")}
        actions={
          <>
            {ws.isAdmin ? (
              <Button variant="outline" asChild>
                <Link to={`/servers/${serverKey}/plan`}>{t("drift.viewPlan")}</Link>
              </Button>
            ) : null}
            {ws.isAdmin && drift.length ? <SyncButton serverKey={serverKey} /> : null}
          </>
        }
      />
      <Card>
        {server.isLoading ? (
          <LoadingRows rows={4} />
        ) : !server.data ? (
          <CardContent>
            <ErrorState error={server.error} onRetry={() => void server.refetch()} />
          </CardContent>
        ) : drift.length === 0 ? (
          <EmptyState icon={<GitCompareArrows />} title={t("drift.empty.title")}>
            {t("drift.empty.text")}
          </EmptyState>
        ) : (
          <DriftTable drift={drift} />
        )}
      </Card>
    </Page>
  );
}
