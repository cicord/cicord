/**
 * Settings of a plugin instance (its dedicated page): scope switch (this server's override /
 * base config), the schema form, the diff review and the save to git.
 */
import {
  CicordApiError,
  type ConfigScope,
  type Json,
  type PluginInstance,
  type WriteBackResult,
} from "@cicord/client";
import { useCicord } from "@cicord/react";
import { ExternalLink, GitCommitHorizontal, Lock } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { keys, useResources, useStatus } from "../api.ts";
import { Mono, Sha } from "../components/common.tsx";
import { Callout, toastError } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { ToggleGroup, ToggleGroupItem } from "../components/ui/toggle-group.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import {
  applyDefaults,
  changedTopLevel,
  collectPointers,
  diffConfig,
  errorsByField,
  mapDiagnostics,
  mergeErrors,
  SchemaForm,
  installationSchema,
  validate,
  type FieldChange,
  type JsonObject,
  type JsonSchema,
} from "../schema-form/index.ts";
import { ValueView } from "./Sync.tsx";

type SaveOutcome =
  | { kind: "saved"; result: WriteBackResult; changed: string[] }
  | { kind: "conflict"; message: string }
  | { kind: "forbidden"; message: string };

/** The settings section of an instance's page. */
export function PluginSettings({
  instance,
  serverKey,
}: {
  instance: PluginInstance;
  serverKey: string;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const resources = useResources(serverKey);
  const status = useStatus();
  const [outcome, setOutcome] = useState<SaveOutcome | null>(null);
  const [scope, setScope] = useState<ConfigScope>("server");
  const hasSettings =
    instance.editable.includes("enabled") ||
    Object.keys((instance.schema as JsonSchema).properties ?? {}).length > 0;
  const name = instance.plugin.displayName || instance.plugin.name;
  return (
    <section className="flex flex-col gap-3" aria-label={t("plugins.settings.label", { name })}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-[15px] font-semibold">{t("plugins.settings.title")}</h2>
        {outcome ? <OutcomeNote outcome={outcome} onDismiss={() => setOutcome(null)} /> : null}
      </div>
      {hasSettings ? (
        <SettingsEditor
          key={JSON.stringify([scope, instance.config, instance.baseConfig ?? null])}
          scope={scope}
          onScope={setScope}
          instance={instance}
          serverKey={serverKey}
          role={ws.role}
          resources={resources.data}
          revision={status.data?.revision?.id ?? ""}
          onOutcome={setOutcome}
        />
      ) : (
        <p className="text-[13px] text-muted-foreground">{t("plugins.settings.none")}</p>
      )}
    </section>
  );
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
const shortRev = (id: string): string => (/^[0-9a-f]{40}$/i.test(id) ? id.slice(0, 7) : id);

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
  const { client, cache } = useCicord();
  const schema = useMemo(
    () => installationSchema(instance.schema as JsonSchema),
    [instance.schema],
  );
  // "server" edits the effective config on this server; "base" edits spec.plugins[].config.
  const baseConfig = instance.baseConfig
    ? applyDefaults(instance.schema as JsonSchema, instance.baseConfig)
    : undefined;
  const baseline = scope === "base" && baseConfig ? baseConfig : instance.config;
  const overridden = Object.keys(instance.override ?? {}).toSorted();
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
      if (e instanceof CicordApiError && e.code === "invalid" && e.diagnostics.length) {
        const mapped = mapDiagnostics(e.diagnostics, collectPointers(schema, draft));
        setServerErrors(mapped);
        setAttempted(true);
        setProblem({ kind: "error", message: e.message });
      } else if (e instanceof CicordApiError && e.code === "forbidden") {
        setProblem({ kind: "forbidden", message: e.message });
      } else if (e instanceof CicordApiError && e.code === "conflict") {
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
