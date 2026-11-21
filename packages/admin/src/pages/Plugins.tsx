import { CodiApiError, type ConfigScope, type Json, type WriteBackResult } from "@codi/client";
import { useCodi } from "@codi/react";
import { useEffect, useMemo, useState } from "react";
import { keys, usePlugins, useResources, useRole, useStatus } from "../api.ts";
import { RoleBadge, useViewer } from "../context.tsx";
import {
  changedTopLevel,
  collectPointers,
  diffConfig,
  errorsByField,
  mapDiagnostics,
  mergeErrors,
  SchemaForm,
  applyDefaults,
  validate,
  type FieldChange,
  type JsonObject,
  type JsonSchema,
} from "../schema-form/index.ts";
import type { AdminPluginInstance } from "../types.ts";
import { IconExternal, IconLock, IconPlug, IconSlash } from "../ui/icons.tsx";
import {
  Badge,
  Button,
  Callout,
  EmptyState,
  ErrorState,
  errorMessage,
  HealthBadge,
  PageHeader,
  Sha,
  Skeleton,
  useToast,
} from "../ui/index.tsx";
import { ValueView } from "./Server.tsx";

export function PluginsPage({ serverKey }: { serverKey: string }) {
  const me = useViewer();
  const role = useRole(me, serverKey);
  const plugins = usePlugins(serverKey);
  const resources = useResources(serverKey);
  const status = useStatus();
  const name = status.data?.servers.find((s) => s.key === serverKey)?.name ?? serverKey;

  useEffect(() => {
    if (!plugins.data || !window.location.hash) return;
    document.getElementById(window.location.hash.slice(1))?.scrollIntoView({ block: "start" });
  }, [plugins.data]);

  return (
    <div className="page">
      <PageHeader
        eyebrow={<code>bots/*/bot.yaml</code>}
        title={`Plugins on ${name}`}
        meta={
          <>
            <RoleBadge role={role} />
            <span className="muted small">
              Saving settings commits to git; the runtime then activates the new revision.
            </span>
          </>
        }
      />
      {plugins.isLoading ? (
        <Skeleton lines={8} />
      ) : !plugins.data ? (
        <ErrorState error={plugins.error} onRetry={() => void plugins.refetch()} />
      ) : plugins.data.length === 0 ? (
        <EmptyState icon={<IconPlug />} title="No plugins on this server">
          Add a plugin to a bot's <code>spec.plugins</code> and list this server in{" "}
          <code>spec.servers</code>.
        </EmptyState>
      ) : (
        <div className="plugin-list">
          {plugins.data.map((p) => (
            <PluginCard
              key={`${p.bot}/${p.instance}`}
              instance={p}
              serverKey={serverKey}
              role={role}
              resources={resources.data}
              revision={status.data?.revision?.id ?? ""}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── capabilities

const CAPABILITIES: Record<string, { label: string; risk?: "high" | "medium" }> = {
  "discord.messages.read": { label: "Read messages" },
  "discord.messages.send": { label: "Send messages" },
  "discord.messages.manage": { label: "Manage messages", risk: "medium" },
  "discord.reactions.manage": { label: "Manage reactions" },
  "discord.members.read": { label: "Read members" },
  "discord.members.manage": { label: "Change nicknames", risk: "medium" },
  "discord.members.moderate": { label: "Timeout, kick, ban", risk: "high" },
  "discord.roles.assign": { label: "Assign roles", risk: "medium" },
  "discord.channels.read": { label: "Read channels" },
  "discord.channels.manage": { label: "Manage channels", risk: "medium" },
  "discord.threads.manage": { label: "Manage threads" },
  "discord.dm.send": { label: "Send DMs" },
  "storage.kv": { label: "Key-value storage" },
  scheduler: { label: "Scheduled tasks" },
  "http.fetch": { label: "Outbound HTTP", risk: "medium" },
  "portal.routes": { label: "Portal routes" },
  "portal.links": { label: "Portal links" },
  "events.publish": { label: "Publish events" },
  "events.subscribe": { label: "Subscribe to events" },
};

function CapabilityChip({ cap }: { cap: string }) {
  const info = CAPABILITIES[cap];
  return (
    <span className="cap" data-risk={info?.risk} title={cap}>
      {info?.label ?? cap}
      <code>{cap}</code>
    </span>
  );
}

// ───────────────────────────────────────────────────────────── card

type SaveOutcome =
  | { kind: "saved"; result: WriteBackResult; changed: string[] }
  | { kind: "conflict"; message: string }
  | { kind: "forbidden"; message: string };

function PluginCard({
  instance,
  serverKey,
  role,
  resources,
  revision,
}: {
  instance: AdminPluginInstance;
  serverKey: string;
  role: string | undefined;
  resources: Parameters<typeof SchemaForm>[0]["resources"];
  revision: string;
}) {
  const p = instance.plugin;
  const [outcome, setOutcome] = useState<SaveOutcome | null>(null);
  const [scope, setScope] = useState<ConfigScope>("server");
  const anchor = `${instance.bot}-${instance.instance}`;
  const hasSettings = Object.keys((instance.schema as JsonSchema).properties ?? {}).length > 0;
  return (
    <article className="plugin-card" id={anchor} aria-labelledby={`${anchor}-title`}>
      <header className="plugin-head">
        <div className="plugin-icon" aria-hidden="true">
          {(p.displayName || p.name).slice(0, 1).toUpperCase()}
        </div>
        <div className="plugin-titles">
          <h2 className="plugin-title" id={`${anchor}-title`}>
            {p.displayName || p.name}
            <span className="plugin-version">
              {p.name}@{p.version}
            </span>
          </h2>
          <p className="plugin-desc">{p.description}</p>
          <p className="plugin-where muted small">
            Instance <code>{instance.instance}</code> of bot <code>{instance.bot}</code> on{" "}
            <code>{serverKey}</code>
          </p>
        </div>
        <HealthBadge health={instance.health} title={instance.message} />
      </header>
      {instance.message ? <p className="plugin-msg">{instance.message}</p> : null}

      <div className="plugin-meta">
        <div className="plugin-meta-block">
          <h3>Capabilities</h3>
          {p.capabilities.length ? (
            <div className="caps">
              {p.capabilities.map((c) => (
                <CapabilityChip key={c} cap={c} />
              ))}
            </div>
          ) : (
            <p className="muted small">None</p>
          )}
        </div>
        <div className="plugin-meta-block">
          <h3>Gateway intents</h3>
          {p.intents.length ? (
            <div className="caps">
              {p.intents.map((i) => (
                <span key={i} className="cap cap-plain">
                  {i}
                </span>
              ))}
            </div>
          ) : (
            <p className="muted small">None</p>
          )}
        </div>
        <div className="plugin-meta-block">
          <h3>Commands</h3>
          {instance.commands.length ? (
            <ul className="cmd-list">
              {instance.commands.map((c) => (
                <li key={c.name}>
                  <code className="cmd-name">
                    <IconSlash size={11} />
                    {c.name}
                  </code>
                  <span className="muted">{c.description}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted small">None</p>
          )}
        </div>
        {instance.routes && instance.routes.length ? (
          <div className="plugin-meta-block">
            <h3>Portal routes</h3>
            <ul className="cmd-list">
              {instance.routes.map((r) => (
                <li key={r}>
                  <code>{r}</code>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>

      <section className="settings" aria-label={`${p.displayName || p.name} settings`}>
        <div className="settings-head">
          <h3>Settings</h3>
          {outcome ? <OutcomeNote outcome={outcome} onDismiss={() => setOutcome(null)} /> : null}
        </div>
        {hasSettings ? (
          <SettingsEditor
            key={JSON.stringify([scope, instance.config, instance.baseConfig ?? null])}
            scope={scope}
            onScope={setScope}
            instance={instance}
            serverKey={serverKey}
            role={role}
            resources={resources}
            revision={revision}
            onOutcome={setOutcome}
          />
        ) : (
          <p className="muted small">This plugin has no settings.</p>
        )}
      </section>
    </article>
  );
}

function OutcomeNote({ outcome, onDismiss }: { outcome: SaveOutcome; onDismiss: () => void }) {
  if (outcome.kind !== "saved") return null;
  const r = outcome.result;
  return (
    <div className="outcome" role="status">
      {r.mode === "pullRequest" ? (
        <>
          <Badge tone="info">Review required</Badge>
          <a href={r.pullRequestUrl} target="_blank" rel="noreferrer" className="outcome-link">
            Pull request opened <IconExternal size={12} />
          </a>
        </>
      ) : (
        <>
          <Badge tone="ok">Committed</Badge>
          <span>
            {r.revision ? (
              <>
                <Sha id={r.revision} /> is now the active revision
              </>
            ) : (
              "Committed"
            )}
          </span>
        </>
      )}
      <button className="link-btn small" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── editor

/** 40-char git SHAs are shortened; other revision ids (local-…) are shown as is. */
export const shortRev = (id: string): string => (/^[0-9a-f]{40}$/i.test(id) ? id.slice(0, 7) : id);

export function formatPath(path: readonly (string | number)[]): string {
  return path.map((s, i) => (typeof s === "number" ? `[${s}]` : i === 0 ? s : `.${s}`)).join("");
}

function SettingsEditor({
  instance,
  serverKey,
  role,
  resources,
  revision,
  onOutcome,
  scope,
  onScope,
}: {
  scope: ConfigScope;
  onScope: (scope: ConfigScope) => void;
  instance: AdminPluginInstance;
  serverKey: string;
  role: string | undefined;
  resources: Parameters<typeof SchemaForm>[0]["resources"];
  revision: string;
  onOutcome: (o: SaveOutcome | null) => void;
}) {
  const { client, cache } = useCodi();
  const toast = useToast();
  const schema = instance.schema as JsonSchema;
  // "server" edits the effective config on this server; "base" edits spec.plugins[].config
  // (with defaults shown) when the runtime exposes it.
  const baseConfig = instance.baseConfig ? applyDefaults(schema, instance.baseConfig) : undefined;
  const baseline = (scope === "base" && baseConfig ? baseConfig : instance.config) as JsonObject;
  const overridden = Object.keys(instance.override ?? {}).sort();
  const [draft, setDraft] = useState<JsonObject>(baseline);
  const [reviewing, setReviewing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [serverErrors, setServerErrors] = useState<{
    byField: Record<string, string[]>;
    general: string[];
  }>({
    byField: {},
    general: [],
  });
  const [problem, setProblem] = useState<SaveOutcome | { kind: "error"; message: string } | null>(
    null,
  );

  const editable = instance.editable;
  const readOnly = editable.length === 0;
  const changes = useMemo(() => diffConfig(baseline, draft), [baseline, draft]);
  const dirty = changes.length > 0;
  const switchScope = (next: ConfigScope) => {
    // The editor remounts with the new scope's values (keyed by scope).
    if (next !== scope && !dirty) onScope(next);
  };
  const touched = changedTopLevel(changes);
  const clientErrors = useMemo(
    () => validate(schema, draft, resources),
    [schema, draft, resources],
  );
  const visibleClientErrors = attempted
    ? clientErrors
    : clientErrors.filter((e) =>
        changes.some(
          (c) =>
            e.pointer === c.pointer ||
            e.pointer.startsWith(`${c.pointer}/`) ||
            c.pointer.startsWith(`${e.pointer}/`),
        ),
      );
  const errors = mergeErrors(errorsByField(visibleClientErrors), serverErrors.byField);
  const denied = touched.filter((k) => !editable.includes(k));

  const change = (next: JsonObject) => {
    setDraft(next);
    setServerErrors({ byField: {}, general: [] });
    if (problem?.kind === "error") setProblem(null);
  };

  const discard = () => {
    setDraft(baseline);
    setReviewing(false);
    setAttempted(false);
    setServerErrors({ byField: {}, general: [] });
    setProblem(null);
  };

  const review = () => {
    setAttempted(true);
    if (clientErrors.length > 0) {
      toast.push({
        tone: "bad",
        title: "Fix the highlighted fields first",
        text: `${clientErrors.length} problem${clientErrors.length > 1 ? "s" : ""} found`,
      });
      return;
    }
    setReviewing(true);
  };

  const save = async () => {
    setSaving(true);
    setProblem(null);
    try {
      const result = await client.admin.updatePluginConfig(
        serverKey,
        instance.bot,
        instance.instance,
        {
          config: draft,
          scope,
          revision,
        },
      );
      onOutcome({ kind: "saved", result, changed: touched });
      toast.push(
        result.mode === "pullRequest"
          ? {
              tone: "info",
              title: "Pull request opened",
              text: "The change applies once it is merged.",
            }
          : {
              tone: "ok",
              title: result.revision ? `Committed ${shortRev(result.revision)}` : "Committed",
            },
      );
      setReviewing(false);
      await Promise.all([
        cache.invalidate(keys.plugins(serverKey)),
        cache.invalidate(["admin", "status"]),
        cache.invalidate(["admin", "history"]),
        cache.invalidate(["admin", "server", serverKey, "audit"]),
      ]);
    } catch (e) {
      setReviewing(false);
      if (e instanceof CodiApiError && e.code === "invalid" && e.diagnostics.length) {
        const mapped = mapDiagnostics(e.diagnostics, collectPointers(schema, draft));
        setServerErrors(mapped);
        setAttempted(true);
        setProblem({ kind: "error", message: e.message });
      } else if (e instanceof CodiApiError && e.code === "forbidden") {
        setProblem({ kind: "forbidden", message: e.message });
      } else if (e instanceof CodiApiError && e.code === "conflict") {
        setProblem({ kind: "conflict", message: e.message });
      } else {
        setProblem({ kind: "error", message: errorMessage(e) });
      }
    } finally {
      setSaving(false);
    }
  };

  const reload = async () => {
    setProblem(null);
    await Promise.all([
      cache.invalidate(keys.plugins(serverKey)),
      cache.invalidate(["admin", "status"]),
    ]);
    discard();
  };

  return (
    <div className="editor">
      <div className="editor-bar">
        <div className="scope" role="radiogroup" aria-label="Write to">
          <span className="scope-label">Write to</span>
          <div className="segmented">
            <button
              role="radio"
              aria-checked={scope === "server"}
              onClick={() => switchScope("server")}
              disabled={readOnly || (dirty && scope !== "server")}
              title={dirty && scope !== "server" ? "Save or discard your changes first" : undefined}
            >
              This server's override
            </button>
            <button
              role="radio"
              aria-checked={scope === "base"}
              onClick={() => switchScope("base")}
              disabled={readOnly || (dirty && scope !== "base")}
              title={dirty && scope !== "base" ? "Save or discard your changes first" : undefined}
            >
              Base config
            </button>
          </div>
        </div>
        <span className="muted small scope-help">
          {scope === "server" ? (
            <>
              Saved as <code>overrides.{serverKey}.config</code>: only what differs from the base.
            </>
          ) : (
            <>
              Saved as <code>config</code>: applies to every server of bot{" "}
              <code>{instance.bot}</code>.
            </>
          )}
        </span>
      </div>
      {scope === "base" && !baseConfig ? (
        <Callout tone="warn">
          The form shows the effective values on <strong>{serverKey}</strong> (base plus this
          server's override). Saving to the base writes them all as the shared base config.
        </Callout>
      ) : scope === "base" && overridden.length ? (
        <Callout tone="info">
          Editing the base config. On <strong>{serverKey}</strong> the override still wins for{" "}
          <code>{overridden.join(", ")}</code>.
        </Callout>
      ) : scope === "server" && overridden.length ? (
        <p className="muted small editor-note">
          This server overrides <code>{overridden.join(", ")}</code>; other fields come from the
          base config.
        </p>
      ) : null}
      {readOnly ? (
        <Callout tone="info" title="Read only">
          Your role on this server cannot change these settings. Fields marked{" "}
          <code>x-codi-access: moderator</code> are editable by moderators.
        </Callout>
      ) : role === "moderator" && editable.length < Object.keys(schema.properties ?? {}).length ? (
        <p className="muted small editor-note">
          <IconLock size={12} /> Some fields are for admins only; you can view them.
        </p>
      ) : null}

      {problem?.kind === "conflict" ? (
        <Callout
          tone="warn"
          title="The configuration changed since you opened it"
          action={
            <Button size="sm" onClick={() => void reload()}>
              Reload settings
            </Button>
          }
        >
          {problem.message} Reloading discards your unsaved edits.
        </Callout>
      ) : problem?.kind === "forbidden" ? (
        <Callout tone="bad" title="Your role cannot save this change">
          {problem.message}. Moderators can only change fields marked{" "}
          <code>x-codi-access: moderator</code>; ask an admin for the others.
        </Callout>
      ) : problem?.kind === "error" ? (
        <Callout tone="bad" title="The runtime rejected the configuration">
          <p>{problem.message}</p>
          {serverErrors.general.length ? (
            <ul className="general-errors">
              {serverErrors.general.map((g) => (
                <li key={g}>{g}</li>
              ))}
            </ul>
          ) : null}
        </Callout>
      ) : null}

      <SchemaForm
        schema={schema}
        value={draft}
        onChange={change}
        editable={editable}
        resources={resources}
        errors={errors}
        baseline={baseline}
        lockedLabel="Admins only"
      />

      {changes.length > 0 ? (
        <div className="savebar" role="region" aria-label="Unsaved changes">
          {reviewing ? (
            <ReviewPanel
              changes={changes}
              scope={scope}
              serverKey={serverKey}
              denied={denied}
              saving={saving}
              onCancel={() => setReviewing(false)}
              onSave={() => void save()}
            />
          ) : (
            <div className="savebar-row">
              <span className="savebar-count">
                <strong>{changes.length}</strong> unsaved change{changes.length > 1 ? "s" : ""}
                <span className="muted"> in {touched.join(", ")}</span>
              </span>
              <div className="savebar-actions">
                <Button variant="ghost" onClick={discard}>
                  Discard
                </Button>
                <Button variant="primary" onClick={review}>
                  Review changes
                </Button>
              </div>
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}

function ReviewPanel({
  changes,
  scope,
  serverKey,
  denied,
  saving,
  onCancel,
  onSave,
}: {
  changes: FieldChange[];
  scope: ConfigScope;
  serverKey: string;
  denied: string[];
  saving: boolean;
  onCancel: () => void;
  onSave: () => void;
}) {
  return (
    <div className="review">
      <div className="review-head">
        <h4>Review changes</h4>
        <span className="muted small">
          {scope === "server" ? `Override for ${serverKey}` : "Base config"} · committed to git,
          then applied
        </span>
      </div>
      <table className="review-table">
        <thead>
          <tr>
            <th scope="col">Field</th>
            <th scope="col">Before</th>
            <th scope="col">After</th>
          </tr>
        </thead>
        <tbody>
          {changes.map((c) => (
            <tr key={c.pointer} data-kind={c.kind}>
              <th scope="row">
                <code>{formatPath(c.path)}</code>
                {c.kind !== "changed" ? (
                  <span className={`review-kind review-${c.kind}`}>{c.kind}</span>
                ) : null}
              </th>
              <td className="diff-from">
                <DiffValue value={c.from} />
              </td>
              <td className="diff-to">
                <DiffValue value={c.to} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {denied.length ? (
        <Callout tone="warn">
          Your role may not change <code>{denied.join(", ")}</code>; the runtime will refuse this
          save.
        </Callout>
      ) : null}
      <div className="review-actions">
        <Button variant="ghost" onClick={onCancel} disabled={saving}>
          Back to editing
        </Button>
        <Button variant="primary" onClick={onSave} loading={saving}>
          Save to git
        </Button>
      </div>
    </div>
  );
}

function DiffValue({ value }: { value: Json | undefined }) {
  if (value !== null && typeof value === "object")
    return <pre className="diff-json">{JSON.stringify(value, null, 2)}</pre>;
  return <ValueView value={value} />;
}
