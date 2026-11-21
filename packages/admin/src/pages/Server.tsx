import type { AuditEntry, Drift, Json, Plan, PlanChange, ServerResources } from "@codi/client";
import { useCodi } from "@codi/react";
import { useMemo, useState, type ReactNode } from "react";
import { AUDIT_PAGE, keys, useAudit, usePlan, useResources, useRole, useServer } from "../api.ts";
import { RoleBadge, useViewer } from "../context.tsx";
import { navigate } from "../router.tsx";
import { ChannelGlyph, jsonText, Swatch } from "../schema-form/index.ts";
import { IconCheck, IconDiff, IconFolder, IconShield, IconSync } from "../ui/icons.tsx";
import {
  ActionBadge,
  Badge,
  Button,
  Callout,
  EmptyState,
  ErrorState,
  errorMessage,
  HealthBadge,
  PageHeader,
  Panel,
  Skeleton,
  SyncBadge,
  Tabs,
  Time,
  useToast,
} from "../ui/index.tsx";

type Tab = "status" | "plan" | "resources" | "audit";
const TABS: Tab[] = ["status", "plan", "resources", "audit"];

export function ServerPage({ serverKey, tab }: { serverKey: string; tab: string }) {
  const me = useViewer();
  const role = useRole(me, serverKey);
  const server = useServer(serverKey);
  const current: Tab = (TABS as string[]).includes(tab) ? (tab as Tab) : "status";
  const isAdmin = role === "admin";

  if (server.isLoading)
    return (
      <div className="page">
        <PageHeader title={serverKey} />
        <Skeleton lines={5} />
      </div>
    );
  if (!server.data)
    return (
      <div className="page">
        <PageHeader title={serverKey} />
        <ErrorState error={server.error} onRetry={() => void server.refetch()} />
      </div>
    );
  const s = server.data;
  const drift = s.drift ?? [];
  return (
    <div className="page">
      <PageHeader
        eyebrow={<code>servers/{s.key}.yaml</code>}
        title={s.name}
        meta={
          <>
            <SyncBadge sync={s.sync} />
            <HealthBadge health={s.health} title={s.message} />
            <RoleBadge role={role} />
            <span className="muted small">
              Discord ID <code>{s.discordId}</code>
            </span>
          </>
        }
        actions={isAdmin ? <SyncButton serverKey={s.key} /> : null}
      />
      <Tabs
        label="Server sections"
        value={current}
        onChange={(t) =>
          navigate(t === "status" ? `/servers/${s.key}` : `/servers/${s.key}/${t}`, {
            keepScroll: true,
          })
        }
        tabs={[
          { id: "status", label: "Status", ...(drift.length ? { count: drift.length } : {}) },
          { id: "plan", label: "Plan" },
          { id: "resources", label: "Resources" },
          { id: "audit", label: "Audit log" },
        ]}
      />
      <div className="tab-panel" role="tabpanel">
        {current === "status" ? (
          <StatusTab
            drift={drift}
            message={s.message}
            lastSync={s.lastSync}
            bot={s.bot}
            spec={s.spec}
          />
        ) : current === "plan" ? (
          isAdmin ? (
            <PlanTab serverKey={s.key} />
          ) : (
            <Callout tone="info" title="The plan is for admins">
              Only server admins can preview and apply the reconciliation plan. You can still see
              drift on the Status tab.
            </Callout>
          )
        ) : current === "resources" ? (
          <ResourcesTab serverKey={s.key} />
        ) : (
          <AuditTab serverKey={s.key} />
        )}
      </div>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── sync

function SyncButton({ serverKey }: { serverKey: string }) {
  const { client, cache } = useCodi();
  const toast = useToast();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      const res = await client.admin.sync(serverKey);
      toast.push({
        tone: "ok",
        title:
          res.applied === 0
            ? "Already in sync"
            : `Applied ${res.applied} change${res.applied > 1 ? "s" : ""}`,
        text:
          res.plan.blocked.length > 0
            ? `${res.plan.blocked.length} blocked by the sync policy`
            : undefined,
      });
      await cache.invalidate(["admin"]);
    } catch (e) {
      toast.push({ tone: "bad", title: "Sync failed", text: errorMessage(e) });
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  };
  if (confirming)
    return (
      <div className="confirm-inline" role="group" aria-label="Confirm sync">
        <span className="small">Apply the plan to Discord now?</span>
        <Button variant="ghost" size="sm" onClick={() => setConfirming(false)} disabled={busy}>
          Cancel
        </Button>
        <Button variant="primary" size="sm" onClick={() => void run()} loading={busy}>
          Sync now
        </Button>
      </div>
    );
  return (
    <Button variant="primary" icon={<IconSync size={14} />} onClick={() => setConfirming(true)}>
      Sync now
    </Button>
  );
}

// ───────────────────────────────────────────────────────────── status

export function ValueView({ value }: { value: Json | undefined }) {
  if (value === undefined || value === null) return <span className="muted">—</span>;
  if (typeof value === "string") {
    if (/^#[0-9a-f]{6}$/i.test(value))
      return (
        <span className="value-color">
          <Swatch color={value} />
          <code>{value}</code>
        </span>
      );
    return (
      <span className="value-str">{value === "" ? <em className="muted">empty</em> : value}</span>
    );
  }
  return <code className="value-json">{JSON.stringify(value)}</code>;
}

function StatusTab({
  drift,
  message,
  lastSync,
  bot,
  spec,
}: {
  drift: Drift[];
  message: string;
  lastSync: string | undefined;
  bot: string | undefined;
  spec: { [key: string]: Json };
}) {
  const policy = (spec["syncPolicy"] ?? {}) as { [key: string]: Json };
  return (
    <div className="stack">
      {message ? <Callout tone="info">{message}</Callout> : null}
      <div className="facts">
        <Fact label="Last sync">{lastSync ? <Time iso={lastSync} /> : "never"}</Fact>
        {bot ? (
          <Fact label="Reconciled by">
            <code>{bot}</code>
          </Fact>
        ) : null}
        <Fact label="Self-heal">{policy["selfHeal"] ? "on" : "off"}</Fact>
        <Fact label="Prune">{policy["prune"] ? "on" : "off"}</Fact>
        <Fact label="Deletion protection">
          {policy["deletionProtection"] === false ? "off" : "on"}
        </Fact>
      </div>
      <Panel
        title="Drift"
        description="Fields changed on Discord that differ from the active revision"
        flush
      >
        {drift.length === 0 ? (
          <EmptyState icon={<IconCheck />} title="No drift">
            Discord matches the YAML of the active revision.
          </EmptyState>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Resource</th>
                  <th scope="col">Field</th>
                  <th scope="col">Expected (git)</th>
                  <th scope="col">Actual (Discord)</th>
                </tr>
              </thead>
              <tbody>
                {drift.map((d, i) => (
                  <tr key={`${d.kind}-${d.key}-${d.field}-${i}`}>
                    <td>
                      <span className="muted">{d.kind}</span> <code>{d.key}</code>
                    </td>
                    <td>
                      <code>{d.field}</code>
                    </td>
                    <td className="cell-expected">
                      <ValueView value={d.expected} />
                    </td>
                    <td className="cell-actual">
                      <ValueView value={d.actual} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="fact">
      <div className="fact-label">{label}</div>
      <div className="fact-value">{children}</div>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── plan

const KIND_ORDER = ["server", "profile", "role", "category", "channel"];
const KIND_LABEL: Record<string, string> = {
  server: "Server",
  profile: "Profile",
  role: "Roles",
  category: "Categories",
  channel: "Channels",
  overwrite: "Permission overwrites",
};

const BLOCK_REASON: Record<string, string> = {
  deletionProtection: "Deletion protection is on (syncPolicy.deletionProtection).",
  prune: "Pruning is off (syncPolicy.prune): unmanaged resources are kept.",
  hierarchy: "The bot's role is below this role in the hierarchy.",
  permissions: "The bot is missing the permission to do this.",
};

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
  return [...groups.entries()].sort((a, b) => rank(a[0]) - rank(b[0]) || a[0].localeCompare(b[0]));
}

function PlanTab({ serverKey }: { serverKey: string }) {
  const plan = usePlan(serverKey);
  return (
    <Panel
      title="Plan"
      description="What a sync would change on Discord to match the active revision"
      actions={
        <Button
          size="sm"
          variant="ghost"
          loading={plan.isFetching}
          onClick={() => void plan.refetch()}
        >
          Refresh
        </Button>
      }
    >
      {plan.isLoading ? (
        <Skeleton lines={5} />
      ) : !plan.data ? (
        <ErrorState error={plan.error} onRetry={() => void plan.refetch()} />
      ) : (
        <PlanView plan={plan.data} />
      )}
    </Panel>
  );
}

export function PlanView({ plan }: { plan: Plan }) {
  const groups = useMemo(() => groupChanges(plan.changes), [plan.changes]);
  const counts = useMemo(() => {
    const out: Record<string, number> = {};
    for (const c of plan.changes) out[c.action] = (out[c.action] ?? 0) + 1;
    return out;
  }, [plan.changes]);
  return (
    <div className="plan">
      {plan.changes.length === 0 ? (
        <EmptyState icon={<IconCheck />} title="Nothing to change">
          Discord already matches the active revision.
        </EmptyState>
      ) : (
        <>
          <div className="plan-summary" aria-label="Summary">
            {Object.entries(counts).map(([action, n]) => (
              <span key={action} className="plan-count">
                <ActionBadge action={action} /> <strong>{n}</strong>
              </span>
            ))}
          </div>
          {groups.map(([kind, changes]) => (
            <section key={kind} className="plan-group" aria-label={KIND_LABEL[kind] ?? kind}>
              <h3 className="plan-group-title">
                {KIND_LABEL[kind] ?? kind}
                <span className="muted">{changes.length}</span>
              </h3>
              <ul className="plan-list">
                {changes.map((c, i) => (
                  <li key={`${c.key}-${i}`} className="plan-change">
                    <div className="plan-change-head">
                      <ActionBadge action={c.action} />
                      <code className="plan-key">{c.key}</code>
                      <span className="plan-summary-text">{c.summary}</span>
                    </div>
                    {c.fields && Object.keys(c.fields).length > 0 ? (
                      <table className="field-diff">
                        <tbody>
                          {Object.entries(c.fields).map(([field, d]) => (
                            <tr key={field}>
                              <th scope="row">
                                <code>{field}</code>
                              </th>
                              <td className="diff-from">
                                <ValueView value={d.from} />
                              </td>
                              <td className="diff-arrow" aria-label="becomes">
                                →
                              </td>
                              <td className="diff-to">
                                <ValueView value={d.to} />
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    ) : null}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </>
      )}
      {plan.blocked.length > 0 ? (
        <section className="plan-group plan-blocked" aria-label="Blocked">
          <h3 className="plan-group-title">
            <IconShield size={14} /> Blocked
            <span className="muted">{plan.blocked.length}</span>
          </h3>
          <ul className="plan-list">
            {plan.blocked.map((b, i) => (
              <li key={`${b.key}-${i}`} className="plan-change">
                <div className="plan-change-head">
                  <ActionBadge action={b.action} />
                  <span className="muted">{b.kind}</span>
                  <code className="plan-key">{b.key}</code>
                </div>
                <p className="plan-reason">{BLOCK_REASON[b.reason] ?? b.reason}</p>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── resources

type ResFilter = "all" | "managed" | "unmanaged";

function ResourcesTab({ serverKey }: { serverKey: string }) {
  const res = useResources(serverKey);
  const [filter, setFilter] = useState<ResFilter>("all");
  if (res.isLoading) return <Skeleton lines={8} />;
  if (!res.data) return <ErrorState error={res.error} onRetry={() => void res.refetch()} />;
  return (
    <div className="stack">
      <div className="toolbar">
        <div className="segmented" role="radiogroup" aria-label="Show">
          {(["all", "managed", "unmanaged"] as const).map((f) => (
            <button key={f} role="radio" aria-checked={filter === f} onClick={() => setFilter(f)}>
              {f === "all" ? "All" : f === "managed" ? "Managed" : "Unmanaged"}
            </button>
          ))}
        </div>
        <span className="muted small">
          Managed resources are declared in YAML and have a key; unmanaged ones exist only on
          Discord.
        </span>
      </div>
      <div className="resources-grid">
        <Panel title="Roles" flush>
          <RoleList resources={res.data} filter={filter} />
        </Panel>
        <Panel title="Categories & channels" flush>
          <ChannelTree resources={res.data} filter={filter} />
        </Panel>
      </div>
    </div>
  );
}

const keep = (managed: boolean, filter: ResFilter) =>
  filter === "all" || (filter === "managed" ? managed : !managed);

function ManagedTag({ managed, k }: { managed: boolean; k: string }) {
  return managed ? <code className="res-key">{k}</code> : <Badge tone="warn">unmanaged</Badge>;
}

function RoleList({ resources, filter }: { resources: ServerResources; filter: ResFilter }) {
  const roles = resources.roles.filter((r) => keep(r.managed, filter));
  if (roles.length === 0) return <EmptyState title="No roles" />;
  return (
    <ul className="res-list">
      {roles.map((r) => (
        <li key={r.id || r.key} className="res-item" data-unmanaged={!r.managed || undefined}>
          <Swatch color={r.color} />
          <span
            className="res-name"
            style={{ color: r.color && r.color !== "#000000" ? r.color : undefined }}
          >
            {r.name}
          </span>
          <ManagedTag managed={r.managed} k={r.key} />
          <code className="res-id muted">{r.id}</code>
        </li>
      ))}
    </ul>
  );
}

function ChannelTree({ resources, filter }: { resources: ServerResources; filter: ResFilter }) {
  const channels = resources.channels.filter(
    (c) => keep(c.managed, filter) && c.type !== "category",
  );
  const known = new Set(resources.categories.map((c) => c.key).filter(Boolean));
  const loose = channels.filter((c) => !c.category || !known.has(c.category));
  const cats = resources.categories.filter(
    (cat) => keep(cat.managed, filter) || channels.some((c) => c.category === cat.key && cat.key),
  );
  if (cats.length === 0 && loose.length === 0) return <EmptyState title="No channels" />;
  return (
    <ul className="res-tree">
      {loose.map((c) => (
        <ChannelItem key={c.id || c.key} c={c} />
      ))}
      {cats.map((cat) => {
        const children = channels.filter((c) => cat.key && c.category === cat.key);
        return (
          <li key={cat.id || cat.key} className="res-cat">
            <div className="res-item res-cat-head" data-unmanaged={!cat.managed || undefined}>
              <IconFolder size={14} />
              <span className="res-name res-cat-name">{cat.name}</span>
              <ManagedTag managed={cat.managed} k={cat.key} />
            </div>
            {children.length ? (
              <ul>
                {children.map((c) => (
                  <ChannelItem key={c.id || c.key} c={c} />
                ))}
              </ul>
            ) : (
              <p className="muted small res-empty">No channels</p>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function ChannelItem({ c }: { c: ServerResources["channels"][number] }) {
  return (
    <li className="res-item" data-unmanaged={!c.managed || undefined}>
      <ChannelGlyph type={c.type} />
      <span className="res-name">{c.name}</span>
      {c.type && c.type !== "text" ? <span className="muted small">{c.type}</span> : null}
      <ManagedTag managed={c.managed} k={c.key} />
    </li>
  );
}

// ───────────────────────────────────────────────────────────── audit

function AuditTab({ serverKey }: { serverKey: string }) {
  const [cursors, setCursors] = useState<string[]>([""]);
  const { cache } = useCodi();
  return (
    <Panel
      title="Audit log"
      description="Configuration changes, syncs and logins on this server (stored in the runtime database, not in git)"
      flush
      actions={
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setCursors([""]);
            void cache.invalidate(keys.audit(serverKey, "").slice(0, 4));
          }}
        >
          Refresh
        </Button>
      }
    >
      <div className="table-wrap">
        <table className="table audit-table">
          <thead>
            <tr>
              <th scope="col">When</th>
              <th scope="col">Actor</th>
              <th scope="col">Action</th>
              <th scope="col">Target</th>
              <th scope="col">Details</th>
            </tr>
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
    </Panel>
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
  const audit = useAudit(serverKey, before);
  if (audit.isLoading)
    return (
      <tbody>
        <tr>
          <td colSpan={5}>
            <Skeleton lines={3} />
          </td>
        </tr>
      </tbody>
    );
  if (!audit.data)
    return (
      <tbody>
        <tr>
          <td colSpan={5}>
            <ErrorState error={audit.error} onRetry={() => void audit.refetch()} />
          </td>
        </tr>
      </tbody>
    );
  const items = audit.data;
  return (
    <tbody>
      {items.length === 0 && before === "" ? (
        <tr>
          <td colSpan={5}>
            <EmptyState icon={<IconDiff />} title="No audit entries yet" />
          </td>
        </tr>
      ) : null}
      {items.map((e) => (
        <AuditRow key={String(e.id)} entry={e} />
      ))}
      {last && items.length >= AUDIT_PAGE ? (
        <tr>
          <td colSpan={5} className="table-more">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => onMore(String(items[items.length - 1]!.id))}
            >
              Load older entries
            </Button>
          </td>
        </tr>
      ) : last && before !== "" ? (
        <tr>
          <td colSpan={5} className="table-more muted small">
            End of the log
          </td>
        </tr>
      ) : null}
    </tbody>
  );
}

function AuditRow({ entry }: { entry: AuditEntry }) {
  const details = Object.entries(entry.details ?? {}).filter(
    ([, v]) => v !== "" && v !== null && !(Array.isArray(v) && v.length === 0),
  );
  return (
    <tr>
      <td className="nowrap">
        <Time iso={entry.time} />
      </td>
      <td>
        <span className="audit-actor">{entry.actor}</span>
      </td>
      <td>
        <code className="audit-action">{entry.action}</code>
      </td>
      <td>
        <code>{entry.target}</code>
      </td>
      <td className="audit-details">
        {details.length === 0 ? (
          <span className="muted">—</span>
        ) : (
          details.map(([k, v]) => (
            <span key={k} className="kv-chip">
              <span className="muted">{k}</span>{" "}
              {k === "pullRequestUrl" && typeof v === "string" && v ? (
                <a href={v} target="_blank" rel="noreferrer">
                  pull request
                </a>
              ) : (
                <span>{Array.isArray(v) ? v.map(jsonText).join(", ") : jsonText(v)}</span>
              )}
            </span>
          ))
        )}
      </td>
    </tr>
  );
}
