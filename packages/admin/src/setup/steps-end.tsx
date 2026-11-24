/** Wizard steps 4–8: repository + import, admin login, verify, first plugin, done. */
import { CicordApiError, type Plan } from "@cicord/client";
import {
  ArrowRight,
  CheckCircle2,
  Database,
  FolderGit2,
  FolderOpen,
  GitBranch,
  LogIn,
  Puzzle,
  Terminal,
} from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useResources } from "../api.ts";
import { Mono, Sha } from "../components/common.tsx";
import { Callout } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { href } from "../router.tsx";
import {
  SchemaForm,
  applyDefaults,
  collectPointers,
  deepEqual,
  errorsByField,
  mapDiagnostics,
  mergeErrors,
  validate,
  type JsonObject,
  type JsonSchema,
} from "../schema-form/index.ts";
import type { CatalogPlugin, ImportResult, InstallResult, Provider, Report } from "./api.ts";
import { useAction, usePoll } from "./hooks.ts";
import {
  Checklist,
  CopyField,
  ExternalButton,
  Instruction,
  Instructions,
  SetupError,
  SimButton,
  StepFooter,
  StepHeading,
} from "./kit.tsx";
import type { StepProps } from "./steps-start.tsx";

// ───────────────────────────────────────────────────────────── repository

const PROVIDER_ICON: Record<Provider, ReactNode> = {
  embedded: <Database className="size-5" />,
  github: <GitBranch className="size-5" />,
  local: <FolderOpen className="size-5" />,
};

function PlanProof({ plan }: { plan: Plan }) {
  const { t } = useTranslation();
  const n = plan.changes.length;
  if (n === 0)
    return (
      <p className="mt-1 flex items-center gap-1.5 text-[13px]">
        <CheckCircle2 className="size-4 text-success" aria-hidden="true" />
        {t("setup.repo.proof")}
      </p>
    );
  return (
    <div className="mt-2">
      <p className="text-[13px]">{t("setup.repo.proofChanges", { count: n })}</p>
      <ul className="mt-2 max-h-56 overflow-auto rounded-md border bg-surface-sunken p-2 font-mono text-[12px]">
        {plan.changes.map((c, i) => (
          <li key={`${c.kind}-${c.key}-${i}`}>
            {c.action} {c.kind} {c.key} — {c.summary}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function RepositoryStep({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const [provider, setProvider] = useState<Provider>(view.repo?.provider ?? view.providers[0]!);
  const [url, setUrl] = useState(view.repo?.url ?? "");
  const [branch, setBranch] = useState(view.repo?.branch ?? "main");
  const [token, setToken] = useState("");
  const [editing, setEditing] = useState(!view.repo);
  const [imported, setImported] = useState<ImportResult | null>(null);
  const choose = useAction();
  const imp = useAction();
  const nav = useAction();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const v = await choose.run(() =>
      api.repo(provider === "github" ? { provider, url, branch, token } : { provider }),
    );
    if (v) {
      setToken("");
      setEditing(false);
      onView(v);
    }
  };
  const runImport = async (confirm: boolean) => {
    const r = await imp.run(() => api.import(confirm));
    if (r) {
      setImported(r);
      if (!r.needsConfirm) onView(r.view);
    }
  };
  const label = (p: Provider) => t(`setup.repo.providers.${p}`);
  return (
    <>
      <StepHeading title={t("setup.repo.title")} lead={t("setup.repo.lead")} />
      {editing ? (
        <form onSubmit={submit}>
          <div className="flex flex-col gap-2" role="radiogroup" aria-label={t("setup.repo.title")}>
            {view.providers.map((p, i) => (
              <label
                key={p}
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-lg border bg-card p-4 hover:bg-surface-hover",
                  provider === p && "border-brand ring-1 ring-brand",
                )}
              >
                <input
                  type="radio"
                  name="provider"
                  value={p}
                  checked={provider === p}
                  onChange={() => setProvider(p)}
                  className="mt-1 accent-[var(--color-brand)]"
                />
                <span className="mt-0.5 text-brand">{PROVIDER_ICON[p]}</span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2 text-[13px] font-semibold">
                    {label(p)}
                    {i === 0 ? <Badge tone="brand">{t("setup.repo.recommended")}</Badge> : null}
                  </span>
                  <span className="mt-0.5 block text-[13px] text-muted-foreground">
                    {t(`setup.repo.providers.${p}Text`)}
                  </span>
                </span>
              </label>
            ))}
          </div>
          {provider === "github" ? (
            <div className="mt-4 grid gap-3 rounded-lg border bg-card p-4">
              <div className="grid gap-1.5">
                <Label htmlFor="repo-url">{t("setup.repo.url")}</Label>
                <Input
                  id="repo-url"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  placeholder={t("setup.repo.urlPlaceholder")}
                  spellCheck={false}
                  autoComplete="off"
                />
                <p className="text-xs text-muted-foreground">{t("setup.repo.urlHelp")}</p>
              </div>
              <div className="grid gap-1.5 sm:max-w-48">
                <Label htmlFor="repo-branch">{t("setup.repo.branch")}</Label>
                <Input
                  id="repo-branch"
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="repo-token">{t("setup.repo.token")}</Label>
                <Input
                  id="repo-token"
                  type="password"
                  autoComplete="off"
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  className="font-mono text-[13px]"
                />
                <p className="text-xs text-muted-foreground">{t("setup.repo.tokenHelp")}</p>
                <div>
                  <ExternalButton href="https://github.com/settings/personal-access-tokens/new">
                    {t("setup.repo.createToken")}
                  </ExternalButton>
                </div>
              </div>
            </div>
          ) : null}
          {choose.error ? <SetupError className="mt-4" error={choose.error} /> : null}
          <div className="mt-4">
            <Button type="submit" loading={choose.pending}>
              {t("setup.repo.choose")}
            </Button>
          </div>
        </form>
      ) : view.repo ? (
        <div className="flex items-center gap-3 rounded-lg border bg-card p-4">
          <span className="text-brand">{PROVIDER_ICON[view.repo.provider]}</span>
          <p className="min-w-0 flex-1 truncate text-[13px] font-medium">
            {t("setup.repo.chosen", { what: view.repo.url || label(view.repo.provider) })}
          </p>
          {!view.activated ? (
            <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
              {t("setup.repo.change")}
            </Button>
          ) : null}
        </div>
      ) : null}

      {view.repo && !editing ? (
        <section className="mt-6 rounded-lg border bg-card p-4" aria-labelledby="import-title">
          <h2 id="import-title" className="flex items-center gap-2 text-[13px] font-semibold">
            <FolderGit2 className="size-4 text-muted-foreground" aria-hidden="true" />
            {view.activated ? t("setup.repo.activated") : t("setup.repo.importTitle")}
          </h2>
          {!view.activated ? (
            <>
              <p className="mt-1 text-[13px] text-muted-foreground">
                {t("setup.repo.importLead", { name: view.server?.name ?? "" })}
              </p>
              {imported?.needsConfirm && imported.plan ? (
                <Callout tone="warning" className="mt-3" title={t("setup.repo.needsConfirm")}>
                  <p>{t("setup.repo.needsConfirmText")}</p>
                  <PlanProof plan={imported.plan} />
                  <div className="mt-3">
                    <Button loading={imp.pending} onClick={() => runImport(true)}>
                      {t("setup.repo.apply")}
                    </Button>
                  </div>
                </Callout>
              ) : (
                <div className="mt-3">
                  <Button loading={imp.pending} onClick={() => runImport(false)}>
                    {imp.pending ? t("setup.repo.importing") : t("setup.repo.import")}
                  </Button>
                </div>
              )}
            </>
          ) : (
            <>
              {(imported?.plan ?? view.firstPlan) ? (
                <PlanProof plan={(imported?.plan ?? view.firstPlan)!} />
              ) : null}
              {view.revision ? (
                <p className="mt-2 text-xs text-muted-foreground">
                  {t("setup.repo.revision")} <Sha id={view.revision} />
                </p>
              ) : null}
              {imported?.files?.length ? (
                <div className="mt-3">
                  <p className="text-xs font-medium text-muted-foreground">
                    {t("setup.repo.files")}
                  </p>
                  <ul className="mt-1 flex flex-wrap gap-1.5">
                    {imported.files.map((f) => (
                      <li key={f}>
                        <Mono className="text-[12px]">{f}</Mono>
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
              {imported?.warnings.length ? (
                <Callout tone="info" className="mt-3" title={t("setup.repo.warnings")}>
                  <ul className="list-disc pl-5">
                    {imported.warnings.map((w) => (
                      <li key={w}>{w}</li>
                    ))}
                  </ul>
                </Callout>
              ) : null}
            </>
          )}
          {imp.error ? <SetupError className="mt-3" error={imp.error} /> : null}
        </section>
      ) : null}
      {nav.error ? <SetupError className="mt-4" error={nav.error} /> : null}
      <StepFooter
        onBack={
          view.activated
            ? undefined
            : () => void nav.run(() => api.step("server")).then((v) => v && onView(v))
        }
      >
        {view.activated ? (
          <Button
            loading={nav.pending}
            onClick={() => void nav.run(() => api.step("login")).then((v) => v && onView(v))}
          >
            {t("setup.continue")}
          </Button>
        ) : null}
      </StepFooter>
    </>
  );
}

// ───────────────────────────────────────────────────────────── admin login

export function LoginStep({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const [secret, setSecret] = useState("");
  const [testing, setTesting] = useState(false);
  const save = useAction();
  const nav = useAction();
  const test = usePoll((signal) => api.testLogin(signal), 2000, testing);
  useEffect(() => {
    if (test.data?.ok) setTesting(false);
  }, [test.data]);
  const login = view.login;
  const testedAs = test.data?.ok
    ? test.data.user?.displayName || test.data.user?.username
    : login.testedAs;
  const next = () => void nav.run(() => api.step("verify")).then((v) => v && onView(v));
  if (view.simulator)
    return (
      <>
        <StepHeading title={t("setup.login.title")} lead={t("setup.login.lead")} />
        <Callout tone="info" title={t("setup.simulator")}>
          {t("setup.login.simText")}
        </Callout>
        <StepFooter>
          <Button
            loading={nav.pending}
            onClick={() => void nav.run(() => api.skipLogin()).then((v) => v && onView(v))}
          >
            {t("setup.continue")}
          </Button>
        </StepFooter>
      </>
    );
  return (
    <>
      <StepHeading title={t("setup.login.title")} lead={t("setup.login.lead")} />
      {login.fromEnv ? (
        <Callout tone="info" title={t("setup.login.saved")}>
          {t("setup.login.fromEnv")}
        </Callout>
      ) : (
        <Instructions>
          <Instruction n={1}>
            <p>{t("setup.login.step1")}</p>
            <div className="mt-2">
              <ExternalButton href={login.portalUrl}>{t("setup.login.openOAuth")}</ExternalButton>
            </div>
          </Instruction>
          <Instruction n={2}>
            <p>{t("setup.login.step2")}</p>
            <CopyField value={login.redirectUrl} label={t("setup.login.step2")} />
          </Instruction>
          <Instruction n={3}>
            <p>{t("setup.login.step3")}</p>
            <form
              className="mt-2 flex flex-col gap-2 sm:flex-row"
              onSubmit={async (e) => {
                e.preventDefault();
                const v = await save.run(() => api.saveOAuth(secret));
                if (v) {
                  setSecret("");
                  onView(v);
                }
              }}
            >
              <Label htmlFor="client-secret" className="sr-only">
                {t("setup.login.secret")}
              </Label>
              <Input
                id="client-secret"
                type="password"
                autoComplete="off"
                className="font-mono text-[13px]"
                placeholder={t("setup.login.secretPlaceholder")}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
              />
              <Button type="submit" loading={save.pending} disabled={!secret.trim()}>
                {t("setup.login.save")}
              </Button>
            </form>
          </Instruction>
        </Instructions>
      )}
      {save.error ? <SetupError className="mt-4" error={save.error} /> : null}
      {login.configured ? (
        <div className="mt-5 flex flex-wrap items-center gap-3 rounded-lg border bg-card p-4">
          <CheckCircle2 className="size-4 text-success" aria-hidden="true" />
          <p className="flex-1 text-[13px]">
            {testedAs ? t("setup.login.testedAs", { name: testedAs }) : t("setup.login.saved")}
          </p>
          <Button variant="outline" size="sm" asChild>
            <a
              href={`/auth/login?redirect=${encodeURIComponent(href("/setup?login=done"))}`}
              target="_blank"
              rel="noreferrer"
              onClick={() => setTesting(true)}
            >
              <LogIn />
              {t("setup.login.test")}
            </a>
          </Button>
          {testing ? (
            <p className="w-full text-xs text-muted-foreground" role="status">
              {t("setup.login.testing")}
            </p>
          ) : null}
        </div>
      ) : (
        <p className="mt-4 text-xs text-muted-foreground">{t("setup.login.skipWarning")}</p>
      )}
      {nav.error ? <SetupError className="mt-4" error={nav.error} /> : null}
      <StepFooter>
        {!login.configured ? (
          <Button
            variant="ghost"
            loading={nav.pending}
            onClick={() => void nav.run(() => api.skipLogin()).then((v) => v && onView(v))}
          >
            {t("setup.login.skip")}
          </Button>
        ) : null}
        <Button disabled={!login.configured} loading={nav.pending} onClick={next}>
          {t("setup.continue")}
        </Button>
      </StepFooter>
    </>
  );
}

// ───────────────────────────────────────────────────────────── verify

export function VerifyStep({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const [live, setLive] = useState(true);
  const rep = usePoll((signal) => api.verify(signal), 2000, live);
  const nav = useAction();
  const sim = useAction();
  const report: Report | undefined = rep.data;
  useEffect(() => {
    if (report?.ok) setLive(false);
  }, [report?.ok]);
  const pending = report?.checks.some((c) => c.status === "pending");
  return (
    <>
      <StepHeading title={t("setup.verify.title")} lead={t("setup.verify.lead")} />
      {rep.error ? <SetupError className="mb-4" error={rep.error} /> : null}
      {report ? (
        <Checklist
          checks={report.checks}
          label={t("setup.verify.title")}
          extra={{
            events:
              report.checks.find((c) => c.id === "events")?.status === "pending" ? (
                <div className="flex flex-col gap-2">
                  <p className="rounded-md border border-info/30 bg-info-soft px-3 py-2 text-[13px]">
                    {t("setup.verify.sendMessage", { name: view.server?.name ?? "" })}
                  </p>
                  {view.simulator ? (
                    <div>
                      <SimButton
                        loading={sim.pending}
                        onClick={() => void sim.run(() => api.sim.message())}
                      >
                        {t("setup.verify.simMessage")}
                      </SimButton>
                    </div>
                  ) : null}
                </div>
              ) : null,
          }}
        />
      ) : (
        <p className="text-[13px] text-muted-foreground" role="status">
          {t("setup.checking")}
        </p>
      )}
      {report?.ok ? (
        <p className="mt-4 flex items-center gap-1.5 text-[13px] font-medium" role="status">
          <CheckCircle2 className="size-4 text-success" aria-hidden="true" />
          {t("setup.verify.allGood")}
        </p>
      ) : null}
      {!live ? (
        <Button className="mt-3" variant="ghost" size="sm" onClick={() => setLive(true)}>
          {t("setup.verify.rerun")}
        </Button>
      ) : null}
      {nav.error ? <SetupError className="mt-4" error={nav.error} /> : null}
      <StepFooter>
        <Button
          variant={report?.ok ? "default" : "outline"}
          loading={nav.pending}
          onClick={() => void nav.run(() => api.step("plugin")).then((v) => v && onView(v))}
        >
          {report?.ok || !pending ? t("setup.continue") : t("setup.continueAnyway")}
        </Button>
      </StepFooter>
    </>
  );
}

// ───────────────────────────────────────────────────────────── first plugin

/** t() for keys built at runtime (capabilities, intents). */
export function dyn(t: ReturnType<typeof useTranslation>["t"]) {
  return t as unknown as (key: string, options?: Record<string, unknown>) => string;
}

function intentToggle(t: ReturnType<typeof useTranslation>["t"], name: string) {
  return dyn(t)(`setup.discord.toggle.${name}`, { defaultValue: name });
}

/** Only what differs from the schema defaults goes to bot.yaml. */
export function nonDefaults(schema: JsonSchema, draft: JsonObject): JsonObject {
  const defaults = applyDefaults(schema, {});
  return Object.fromEntries(Object.entries(draft).filter(([k, v]) => !deepEqual(v, defaults[k])));
}

function PluginReview({
  plugin,
  serverKey,
  serverName,
  api,
  onView,
  onBack,
}: {
  plugin: CatalogPlugin;
  serverKey: string;
  serverName: string;
  api: StepProps["api"];
  onView: StepProps["onView"];
  onBack: () => void;
}) {
  const { t } = useTranslation();
  const schema = plugin.schema as JsonSchema;
  const hasSettings = Object.keys((schema as { properties?: object }).properties ?? {}).length > 0;
  const [draft, setDraft] = useState<JsonObject>(() => applyDefaults(schema, {}));
  const [attempted, setAttempted] = useState(false);
  const [serverErrors, setServerErrors] = useState<Record<string, string[]>>({});
  const [done, setDone] = useState<InstallResult | null>(null);
  const resources = useResources(serverKey);
  const a = useAction();
  const clientErrors = useMemo(
    () => (hasSettings ? validate(schema, draft, resources.data) : []),
    [schema, draft, resources.data, hasSettings],
  );
  const errors = mergeErrors(attempted ? errorsByField(clientErrors) : {}, serverErrors);
  const install = async () => {
    setAttempted(true);
    if (clientErrors.length) return;
    const r = await a.run(() => api.install(plugin.name, nonDefaults(schema, draft)));
    if (r) {
      setDone(r.result);
      onView(r.view);
    }
  };
  useEffect(() => {
    const e = a.error;
    if (e instanceof CicordApiError && e.diagnostics.length)
      setServerErrors(mapDiagnostics(e.diagnostics, collectPointers(schema, draft)).byField);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- maps the diagnostics of a new error only; schema and draft are read at that moment, re-running on edits would resurrect fixed errors
  }, [a.error]);
  if (done)
    return (
      <section
        className="rounded-lg border border-success/30 bg-success-soft p-5"
        aria-live="polite"
      >
        <p className="flex items-center gap-2 text-[15px] font-semibold">
          <CheckCircle2 className="size-5 text-success" aria-hidden="true" />
          {t("setup.plugin.installed", { name: plugin.displayName })}
        </p>
        <p className="mt-2 text-[13px]">
          {done.mode !== "commit"
            ? t("setup.plugin.pr")
            : done.command
              ? t("setup.plugin.tryIt", { command: done.command, server: serverName })
              : t("setup.plugin.noCommand")}
        </p>
        {done.command ? <CopyField value={`/${done.command}`} /> : null}
        {done.message ? <p className="mt-2 text-xs text-muted-foreground">{done.message}</p> : null}
      </section>
    );
  return (
    <section aria-labelledby="review-title">
      <div className="rounded-lg border bg-card p-4">
        <h2 id="review-title" className="text-[13px] font-semibold">
          {t("setup.plugin.reviewTitle", { name: plugin.displayName })}
        </h2>
        <ul className="mt-2 grid gap-1 sm:grid-cols-2">
          {plugin.capabilities.map((c) => (
            <li key={c} className="flex items-start gap-2 text-[13px]">
              <CheckCircle2
                className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
              {dyn(t)(`setup.capabilities.${c.replaceAll(".", "_")}`, { defaultValue: c })}
            </li>
          ))}
        </ul>
        {plugin.intents.length ? (
          <p className="mt-3 text-xs text-muted-foreground">
            {t("setup.plugin.intents")}: {plugin.intents.join(", ")}
          </p>
        ) : null}
        {plugin.network.length ? (
          <p className="mt-1 text-xs text-muted-foreground">
            {t("setup.plugin.network")}: {plugin.network.join(", ")}
          </p>
        ) : null}
        {plugin.commands.length ? (
          <div className="mt-3">
            <p className="text-xs font-medium text-muted-foreground">
              {t("setup.plugin.commandsTitle")}
            </p>
            <ul className="mt-1 flex flex-wrap gap-1.5">
              {plugin.commands.map((c) => (
                <li key={c.name}>
                  <Badge tone="solid" title={c.description}>
                    /{c.name}
                  </Badge>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>
      <div className="mt-4 rounded-lg border bg-card p-4">
        <h3 className="mb-3 text-[13px] font-semibold">{t("setup.plugin.configure")}</h3>
        {hasSettings ? (
          <SchemaForm
            schema={schema}
            value={draft}
            onChange={(v) => {
              setDraft(v);
              setServerErrors({});
            }}
            {...(resources.data ? { resources: resources.data } : {})}
            errors={errors}
          />
        ) : (
          <p className="text-[13px] text-muted-foreground">{t("setup.plugin.noSettings")}</p>
        )}
      </div>
      {a.error ? <SetupError className="mt-4" error={a.error} /> : null}
      <div className="mt-4 flex flex-wrap gap-2">
        <Button loading={a.pending} onClick={install}>
          {a.pending
            ? t("setup.plugin.installing")
            : t("setup.plugin.install", { name: plugin.displayName })}
        </Button>
        <Button variant="ghost" onClick={onBack}>
          {t("setup.plugin.other")}
        </Button>
      </div>
    </section>
  );
}

export function PluginStep({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const [picked, setPicked] = useState<CatalogPlugin | null>(null);
  const cat = usePoll((signal) => api.catalog(signal), 3_600_000);
  const nav = useAction();
  const installed = view.plugin && !view.plugin.skipped ? view.plugin : undefined;
  return (
    <>
      <StepHeading title={t("setup.plugin.title")} lead={t("setup.plugin.lead")} />
      {cat.error ? <SetupError error={cat.error} /> : null}
      {picked ? (
        <PluginReview
          plugin={picked}
          serverKey={view.server!.key}
          serverName={view.server!.name}
          api={api}
          onView={onView}
          onBack={() => setPicked(null)}
        />
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2" aria-label={t("setup.plugin.title")}>
          {(cat.data ?? []).map((p) => (
            <li key={p.name} className="flex flex-col rounded-lg border bg-card p-4">
              <div className="flex items-center gap-2">
                <Puzzle className="size-4 text-brand" aria-hidden="true" />
                <h2 className="text-[14px] font-semibold">{p.displayName}</h2>
                <Badge tone="brand">{t("setup.plugin.official")}</Badge>
                {installed?.name === p.name ? (
                  <Badge tone="success">{t("setup.checks.status.ok")}</Badge>
                ) : null}
              </div>
              <p className="mt-1.5 line-clamp-3 flex-1 text-[13px] text-muted-foreground">
                {p.description}
              </p>
              <div className="mt-3 flex flex-wrap items-center gap-1.5">
                {p.commands.length ? (
                  <Badge>{t("setup.plugin.commands", { count: p.commands.length })}</Badge>
                ) : null}
                {p.privilegedIntents.length ? (
                  <Badge tone="warning">
                    {t("setup.plugin.needsIntent", {
                      intents: p.privilegedIntents.map((i) => intentToggle(t, i)).join(", "),
                    })}
                  </Badge>
                ) : null}
                <Button
                  size="sm"
                  variant="outline"
                  className="ml-auto"
                  disabled={installed?.name === p.name}
                  onClick={() => setPicked(p)}
                  aria-label={`${t("setup.plugin.choose")} ${p.displayName}`}
                >
                  {t("setup.plugin.choose")}
                  <ArrowRight />
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {nav.error ? <SetupError className="mt-4" error={nav.error} /> : null}
      <StepFooter onBack={() => void nav.run(() => api.step("verify")).then((v) => v && onView(v))}>
        <Button
          variant={installed ? "default" : "ghost"}
          loading={nav.pending}
          onClick={() => void nav.run(() => api.step("done")).then((v) => v && onView(v))}
        >
          {installed ? t("setup.continue") : t("setup.plugin.skip")}
        </Button>
      </StepFooter>
    </>
  );
}

// ───────────────────────────────────────────────────────────── done

export function DoneStep({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const a = useAction();
  const loginOn = view.login.configured || view.simulator;
  const finish = async () => {
    const v = await a.run(() => api.finish());
    if (v) {
      onView(v);
      window.location.assign(href("/"));
    }
  };
  const rows: [string, ReactNode][] = [
    [t("setup.done.bot"), view.bot?.username ?? "—"],
    [t("setup.done.server"), view.server?.name ?? "—"],
    [
      t("setup.done.repo"),
      view.repo ? view.repo.url || t(`setup.repo.providers.${view.repo.provider}`) : "—",
    ],
    [
      t("setup.done.login"),
      view.simulator
        ? t("setup.done.loginDev")
        : loginOn
          ? t("setup.done.loginOn")
          : t("setup.done.loginOff"),
    ],
    [
      t("setup.done.plugin"),
      view.plugin && !view.plugin.skipped ? view.plugin.name : t("setup.done.none"),
    ],
  ];
  return (
    <>
      <StepHeading
        title={t("setup.done.title")}
        lead={t("setup.done.lead", { server: view.server?.name ?? "" })}
      />
      <dl className="divide-y rounded-lg border bg-card">
        {rows.map(([k, v]) => (
          <div key={k} className="flex justify-between gap-4 px-4 py-2.5 text-[13px]">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="truncate font-medium">{v}</dd>
          </div>
        ))}
      </dl>
      <h2 className="mt-6 text-[13px] font-semibold">{t("setup.done.next")}</h2>
      <ul className="mt-2 grid gap-2 sm:grid-cols-2">
        <li className="rounded-lg border bg-card p-4">
          <p className="text-[13px] font-semibold">{t("setup.done.portal")}</p>
          <p className="mt-0.5 text-[13px] text-muted-foreground">{t("setup.done.portalText")}</p>
          <div className="mt-2">
            <ExternalButton href="/">{t("setup.done.portal")}</ExternalButton>
          </div>
        </li>
        <li className="rounded-lg border bg-card p-4">
          <p className="flex items-center gap-1.5 text-[13px] font-semibold">
            <Terminal className="size-4 text-muted-foreground" aria-hidden="true" />
            {t("setup.done.cli")}
          </p>
          <p className="mt-0.5 text-[13px] text-muted-foreground">{t("setup.done.cliText")}</p>
          <CopyField value="cicord plan --remote" />
        </li>
      </ul>
      {!loginOn ? (
        <Callout tone="warning" className="mt-4" title={t("setup.done.loginOff")}>
          {t("setup.done.loginWarning")}
        </Callout>
      ) : null}
      {a.error ? <SetupError className="mt-4" error={a.error} /> : null}
      <StepFooter
        onBack={() =>
          void a.run(() => api.step(loginOn ? "plugin" : "login")).then((v) => v && onView(v))
        }
      >
        <Button loading={a.pending} onClick={finish}>
          {t("setup.done.finish")}
          <ArrowRight />
        </Button>
      </StepFooter>
    </>
  );
}
