import {
  CodiApiError,
  type ConfigScope,
  type Json,
  type PluginInstance,
  type WriteBackResult,
} from "@codi/client";
import { useCodi } from "@codi/react";
import {
  ExternalLink,
  GitCommitHorizontal,
  Lock,
  Puzzle,
  Slash,
  TriangleAlert,
} from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { keys, usePlugins, useResources, useStatus } from "../api.ts";
import {
  EmptyState,
  LoadingRows,
  Mono,
  Page,
  PageHeader,
  PluginGlyph,
  Sha,
} from "../components/common.tsx";
import { Callout, ErrorState, toastError } from "../components/errors.tsx";
import { HealthBadge } from "../components/status.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card } from "../components/ui/card.tsx";
import { ToggleGroup, ToggleGroupItem } from "../components/ui/toggle-group.tsx";
import { Tip } from "../components/ui/tooltip.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { cleanMessage } from "../lib/errors.ts";
import { cn } from "../lib/utils.ts";
import {
  applyDefaults,
  changedTopLevel,
  collectPointers,
  diffConfig,
  errorsByField,
  mapDiagnostics,
  mergeErrors,
  SchemaForm,
  validate,
  type FieldChange,
  type JsonObject,
  type JsonSchema,
} from "../schema-form/index.ts";
import { ValueView } from "./Sync.tsx";

export function PluginsPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const plugins = usePlugins(serverKey);
  const resources = useResources(serverKey);
  const status = useStatus();

  useEffect(() => {
    if (!plugins.data || !window.location.hash) return;
    document.getElementById(window.location.hash.slice(1))?.scrollIntoView({ block: "start" });
  }, [plugins.data]);

  return (
    <Page>
      <PageHeader
        title={t("plugins.title")}
        description={t("plugins.description")}
        meta={
          plugins.data ? (
            <span className="text-xs text-muted-foreground">
              {t("plugins.count", { count: plugins.data.length })}
            </span>
          ) : null
        }
      />
      {plugins.isLoading ? (
        <Card>
          <LoadingRows rows={8} />
        </Card>
      ) : !plugins.data ? (
        <ErrorState error={plugins.error} onRetry={() => void plugins.refetch()} />
      ) : plugins.data.length === 0 ? (
        <Card>
          <EmptyState icon={<Puzzle />} title={t("plugins.empty.title")}>
            {t("plugins.empty.text")}
          </EmptyState>
        </Card>
      ) : (
        <div className="flex flex-col gap-6">
          {plugins.data.map((p) => (
            <PluginCard
              key={`${p.bot}/${p.instance}`}
              instance={p}
              serverKey={serverKey}
              role={ws.role}
              resources={resources.data}
              revision={status.data?.revision?.id ?? ""}
            />
          ))}
        </div>
      )}
    </Page>
  );
}

// ───────────────────────────────────────────────────────────── capabilities

const RISK: Record<string, "high" | "medium"> = {
  "discord.messages.manage": "medium",
  "discord.members.manage": "medium",
  "discord.members.moderate": "high",
  "discord.roles.assign": "medium",
  "discord.channels.manage": "medium",
  "http.fetch": "medium",
};

function CapabilityChip({ cap }: { cap: string }) {
  const { t } = useTranslation();
  const risk = RISK[cap];
  const label = t(`plugins.capabilities.${cap.replaceAll(".", "_") as "storage_kv"}`, {
    defaultValue: cap,
  });
  return (
    <Tip content={<Mono>{cap}</Mono>}>
      <span
        tabIndex={0}
        className={cn(
          "inline-flex h-6 items-center gap-1 rounded-md border px-2 text-xs",
          risk === "high"
            ? "border-danger/30 text-danger"
            : risk === "medium"
              ? "border-warning/30 text-warning"
              : "border-border-strong text-foreground/90",
        )}
      >
        {risk ? <TriangleAlert className="size-3" aria-hidden="true" /> : null}
        {label}
      </span>
    </Tip>
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
  instance: PluginInstance;
  serverKey: string;
  role: string | undefined;
  resources: Parameters<typeof SchemaForm>[0]["resources"];
  revision: string;
}) {
  const { t } = useTranslation();
  const p = instance.plugin;
  const [outcome, setOutcome] = useState<SaveOutcome | null>(null);
  const [scope, setScope] = useState<ConfigScope>("server");
  const anchor = `${instance.bot}-${instance.instance}`;
  const hasSettings = Object.keys((instance.schema as JsonSchema).properties ?? {}).length > 0;
  const name = p.displayName || p.name;
  return (
    <Card id={anchor} role="article" aria-labelledby={`${anchor}-title`} className="scroll-mt-20">
      <header className="flex items-start gap-3 border-b px-4 py-4">
        <PluginGlyph size={36} />
        <div className="min-w-0 flex-1">
          <h2
            className="flex flex-wrap items-baseline gap-x-2 text-[15px] font-semibold"
            id={`${anchor}-title`}
          >
            {name}
            <Mono className="text-[11px] font-normal">
              {p.name}@{p.version}
            </Mono>
          </h2>
          {p.description ? (
            <p className="mt-0.5 text-[13px] text-muted-foreground">{p.description}</p>
          ) : null}
          <p className="mt-1.5 text-xs text-muted-foreground">
            {t("plugins.where.instance")}{" "}
            <Mono className="text-foreground">{instance.instance}</Mono> {t("plugins.where.bot")}{" "}
            <Mono className="text-foreground">{instance.bot}</Mono>
          </p>
        </div>
        <HealthBadge health={instance.health} />
      </header>
      {instance.message ? (
        <div className="border-b px-4 py-3">
          <Callout
            tone={instance.health === "Degraded" ? "danger" : "info"}
            title={cleanMessage(instance.message)}
            details={instance.message}
          />
        </div>
      ) : null}

      <div className="grid gap-x-6 gap-y-4 border-b px-4 py-4 md:grid-cols-2">
        <MetaBlock title={t("plugins.meta.capabilities")}>
          {p.capabilities.length ? (
            <div className="flex flex-wrap gap-1.5">
              {p.capabilities.map((c) => (
                <CapabilityChip key={c} cap={c} />
              ))}
            </div>
          ) : (
            <None />
          )}
        </MetaBlock>
        <MetaBlock title={t("plugins.meta.intents")}>
          {p.intents.length ? (
            <div className="flex flex-wrap gap-1.5">
              {p.intents.map((i) => (
                <Badge key={i} tone="neutral" className="h-6 px-2 font-mono font-normal">
                  {i}
                </Badge>
              ))}
            </div>
          ) : (
            <None />
          )}
        </MetaBlock>
        <MetaBlock title={t("plugins.meta.commands")}>
          {instance.commands.length ? (
            <ul className="flex flex-col gap-1">
              {instance.commands.map((c) => (
                <li key={c.name} className="flex items-baseline gap-2 text-[13px]">
                  <code className="inline-flex items-center font-mono text-[12px] text-foreground">
                    <Slash className="size-3 text-subtle-foreground" aria-hidden="true" />
                    {c.name}
                  </code>
                  <span className="truncate text-muted-foreground">{c.description}</span>
                </li>
              ))}
            </ul>
          ) : (
            <None />
          )}
        </MetaBlock>
        {instance.routes && instance.routes.length ? (
          <MetaBlock title={t("plugins.meta.routes")}>
            <ul className="flex flex-col gap-1">
              {instance.routes.map((r) => (
                <li key={r}>
                  <Mono className="text-foreground">{r}</Mono>
                </li>
              ))}
            </ul>
          </MetaBlock>
        ) : null}
      </div>

      <section className="px-4 py-4" aria-label={t("plugins.settings.label", { name })}>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-[13px] font-semibold">{t("plugins.settings.title")}</h3>
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
          <p className="text-[13px] text-muted-foreground">{t("plugins.settings.none")}</p>
        )}
      </section>
    </Card>
  );
}

function MetaBlock({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <h3 className="mb-1.5 text-xs font-medium text-muted-foreground">{title}</h3>
      {children}
    </div>
  );
}

function None() {
  const { t } = useTranslation();
  return <p className="text-[13px] text-subtle-foreground">{t("common.none")}</p>;
}

function OutcomeNote({ outcome, onDismiss }: { outcome: SaveOutcome; onDismiss: () => void }) {
  const { t } = useTranslation();
  if (outcome.kind !== "saved") return null;
  const r = outcome.result;
  return (
    <div className="flex items-center gap-2 text-[13px]" role="status">
      {r.mode === "pullRequest" ? (
        <>
          <Badge tone="info">{t("plugins.outcome.review")}</Badge>
          <a
            href={r.pullRequestUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-brand hover:underline"
          >
            {t("plugins.outcome.prOpened")} <ExternalLink className="size-3" />
          </a>
        </>
      ) : (
        <>
          <Badge tone="success">{t("plugins.outcome.committed")}</Badge>
          <span className="inline-flex items-center gap-1">
            {r.revision ? (
              <>
                <Sha id={r.revision} /> {t("plugins.outcome.active")}
              </>
            ) : (
              t("plugins.outcome.committed")
            )}
          </span>
        </>
      )}
      <Button variant="link" size="sm" className="text-xs" onClick={onDismiss}>
        {t("common.dismiss")}
      </Button>
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
  instance: PluginInstance;
  serverKey: string;
  role: string | undefined;
  resources: Parameters<typeof SchemaForm>[0]["resources"];
  revision: string;
  onOutcome: (o: SaveOutcome | null) => void;
}) {
  const { t } = useTranslation();
  const { client, cache } = useCodi();
  const schema = instance.schema as JsonSchema;
  // "server" edits the effective config on this server; "base" edits spec.plugins[].config.
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
  const [problem, setProblem] = useState<
    SaveOutcome | { kind: "error"; message: string; details?: string | undefined } | null
  >(null);

  const editable = instance.editable;
  const readOnly = editable.length === 0;
  const changes = useMemo(() => diffConfig(baseline, draft), [baseline, draft]);
  const dirty = changes.length > 0;
  const switchScope = (next: ConfigScope) => {
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
      toast.error(t("plugins.editor.fixFirst"), {
        description: t("plugins.editor.problems", { count: clientErrors.length }),
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
      if (result.mode === "pullRequest")
        toast.info(t("plugins.outcome.prOpened"), { description: t("plugins.outcome.prText") });
      else
        toast.success(
          result.revision
            ? t("plugins.outcome.committedRev", { rev: shortRev(result.revision) })
            : t("plugins.outcome.committed"),
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
        const d = toastError(e, t("plugins.editor.saveFailed"));
        setProblem({ kind: "error", message: d.message, details: d.details });
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
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span
          className="text-xs text-muted-foreground"
          id={`${instance.bot}-${instance.instance}-scope`}
        >
          {t("plugins.editor.writeTo")}
        </span>
        <ToggleGroup
          type="single"
          value={scope}
          onValueChange={(v) => v && switchScope(v as ConfigScope)}
          aria-labelledby={`${instance.bot}-${instance.instance}-scope`}
        >
          <ToggleGroupItem
            value="server"
            disabled={readOnly || (dirty && scope !== "server")}
            title={dirty && scope !== "server" ? t("plugins.editor.saveFirst") : undefined}
          >
            {t("plugins.editor.scopeServer")}
          </ToggleGroupItem>
          <ToggleGroupItem
            value="base"
            disabled={readOnly || (dirty && scope !== "base")}
            title={dirty && scope !== "base" ? t("plugins.editor.saveFirst") : undefined}
          >
            {t("plugins.editor.scopeBase")}
          </ToggleGroupItem>
        </ToggleGroup>
        <span className="text-xs text-muted-foreground">
          {scope === "server" ? (
            <>
              {t("plugins.editor.savedAs")} <Mono>overrides.{serverKey}.config</Mono> ·{" "}
              {t("plugins.editor.onlyDiff")}
            </>
          ) : (
            <>
              {t("plugins.editor.savedAs")} <Mono>config</Mono> ·{" "}
              {t("plugins.editor.everyServer", { bot: instance.bot })}
            </>
          )}
        </span>
      </div>
      {scope === "base" && !baseConfig ? (
        <Callout tone="warning" title={t("plugins.editor.baseEffective", { server: serverKey })} />
      ) : scope === "base" && overridden.length ? (
        <Callout
          tone="info"
          title={t("plugins.editor.baseOverride", {
            server: serverKey,
            fields: overridden.join(", "),
          })}
        />
      ) : scope === "server" && overridden.length ? (
        <p className="editor-note text-xs text-muted-foreground">
          {t("plugins.editor.overrides")} <code className="font-mono">{overridden.join(", ")}</code>
          {t("plugins.editor.overridesTail")}
        </p>
      ) : null}
      {readOnly ? (
        <Callout tone="info" title={t("plugins.editor.readOnly.title")}>
          {t("plugins.editor.readOnly.text")}
        </Callout>
      ) : role === "moderator" && editable.length < Object.keys(schema.properties ?? {}).length ? (
        <p className="inline-flex items-center gap-1 text-xs text-muted-foreground">
          <Lock className="size-3" aria-hidden="true" /> {t("plugins.editor.someLocked")}
        </p>
      ) : null}

      {problem?.kind === "conflict" ? (
        <Callout
          tone="warning"
          title={t("plugins.editor.conflict.title")}
          hint={t("plugins.editor.conflict.hint")}
          details={problem.message}
          action={
            <Button size="sm" variant="outline" onClick={() => void reload()}>
              {t("plugins.editor.conflict.reload")}
            </Button>
          }
        />
      ) : problem?.kind === "forbidden" ? (
        <Callout
          tone="danger"
          title={t("plugins.editor.forbidden.title")}
          hint={t("plugins.editor.forbidden.hint")}
        >
          {problem.message}
        </Callout>
      ) : problem?.kind === "error" ? (
        <Callout tone="danger" title={t("plugins.editor.rejected")} details={problem.details}>
          <p>{problem.message}</p>
          {serverErrors.general.length ? (
            <ul className="mt-1 list-disc pl-5">
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
        lockedLabel={t("plugins.editor.adminsOnly")}
      />

      {changes.length > 0 ? (
        <div
          className="sticky bottom-3 z-[4] rounded-lg border border-border-strong bg-popover/95 shadow-pop backdrop-blur"
          role="region"
          aria-label={t("plugins.editor.unsavedRegion")}
        >
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
            <div className="flex flex-wrap items-center gap-3 px-4 py-2.5">
              <span className="text-[13px]">
                <strong className="tabular">
                  {t("plugins.editor.unsaved", { count: changes.length })}
                </strong>
                <span className="text-muted-foreground"> · {touched.join(", ")}</span>
              </span>
              <div className="ml-auto flex gap-2">
                <Button variant="ghost" onClick={discard}>
                  {t("plugins.editor.discard")}
                </Button>
                <Button onClick={review}>{t("plugins.editor.review")}</Button>
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
  const { t } = useTranslation();
  return (
    <div className="review flex flex-col gap-3 p-4">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h4 className="text-[13px] font-semibold">{t("plugins.editor.review")}</h4>
        <span className="text-xs text-muted-foreground">
          {scope === "server"
            ? t("plugins.editor.reviewServer", { server: serverKey })
            : t("plugins.editor.reviewBase")}
        </span>
      </div>
      <div className="max-h-80 overflow-auto rounded-md border">
        <table className="w-full text-[13px]">
          <thead className="border-b bg-surface-2 text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-3 py-1.5 text-left font-medium">
                {t("plugins.editor.field")}
              </th>
              <th scope="col" className="px-3 py-1.5 text-left font-medium">
                {t("plugins.editor.before")}
              </th>
              <th scope="col" className="px-3 py-1.5 text-left font-medium">
                {t("plugins.editor.after")}
              </th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {changes.map((c) => (
              <tr key={c.pointer} data-kind={c.kind} className="align-top">
                <th scope="row" className="px-3 py-1.5 text-left font-normal">
                  <code className="font-mono text-[12px]">{formatPath(c.path)}</code>
                  {c.kind !== "changed" ? (
                    <Badge tone={c.kind === "added" ? "success" : "danger"} className="ml-1.5">
                      {t(`plugins.editor.kinds.${c.kind}`)}
                    </Badge>
                  ) : null}
                </th>
                <td className="px-3 py-1.5 text-muted-foreground">
                  <DiffValue value={c.from} />
                </td>
                <td className="px-3 py-1.5">
                  <DiffValue value={c.to} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {denied.length ? (
        <Callout tone="warning" title={t("plugins.editor.denied", { fields: denied.join(", ") })} />
      ) : null}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
          <GitCommitHorizontal className="size-3.5" aria-hidden="true" />
          {t("plugins.editor.commitNote")}
        </span>
        <div className="flex gap-2">
          <Button variant="ghost" onClick={onCancel} disabled={saving}>
            {t("plugins.editor.back")}
          </Button>
          <Button onClick={onSave} loading={saving}>
            {t("plugins.editor.save")}
          </Button>
        </div>
      </div>
    </div>
  );
}

function DiffValue({ value }: { value: Json | undefined }) {
  if (value !== null && typeof value === "object")
    return (
      <pre className="font-mono text-[12px] whitespace-pre-wrap">
        {JSON.stringify(value, null, 2)}
      </pre>
    );
  return <ValueView value={value} />;
}
