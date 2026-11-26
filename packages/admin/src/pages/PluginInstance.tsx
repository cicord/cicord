/**
 * The page of one plugin instance (/servers/:key/plugins/:bot/:instance): header, then a
 * sub-navigation between its settings, an overview of what it does and may do, and (admins)
 * the danger zone to uninstall it from the server.
 */
import { CicordApiError, type PluginInstance, type UninstallResult } from "@cicord/client";
import { useCicord } from "@cicord/react";
import {
  ArrowLeft,
  CheckCircle2,
  ExternalLink,
  Info,
  Puzzle,
  SlidersHorizontal,
  Slash,
  Store as StoreIcon,
  Trash2,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { toast } from "sonner";
import { keys, usePlugin } from "../api.ts";
import { EmptyState, Mono, Page, PageHeader, PluginGlyph, Sha } from "../components/common.tsx";
import { Callout, ErrorState } from "../components/errors.tsx";
import { HealthBadge } from "../components/status.tsx";
import { CapabilityChip, TrustBadge } from "../components/trust.tsx";
import { WriteWarnings } from "../components/update.tsx";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../components/ui/alert-dialog.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card } from "../components/ui/card.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { cleanMessage, describeError } from "../lib/errors.ts";
import { listUrl, setFlash } from "../lib/flash.ts";
import { cn } from "../lib/utils.ts";
import { Link, navigate, useLocation } from "../router.tsx";
import { PluginSettings } from "./PluginSettings.tsx";
import { UpdateCallout } from "./Store.tsx";
import { storePluginPath, storeUpdatePath } from "../shell/nav.ts";

type Section = "settings" | "overview" | "danger";

const pluginsPath = (serverKey: string) => `/servers/${encodeURIComponent(serverKey)}/plugins`;

export function PluginInstancePage({
  serverKey,
  bot,
  instance,
}: {
  serverKey: string;
  bot: string;
  instance: string;
}) {
  const { t } = useTranslation();
  const query = usePlugin(serverKey, bot, instance);
  const back = (
    <Link
      to={listUrl(`plugins:${serverKey}`, pluginsPath(serverKey))}
      className="inline-flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="size-3.5" aria-hidden="true" />
      {t("plugins.instance.back")}
    </Link>
  );

  if (query.isLoading)
    return (
      <Page>
        {back}
        <div className="flex flex-col gap-3" aria-busy="true" aria-label={t("common.loading")}>
          <div className="flex items-center gap-3">
            <Skeleton className="size-10" />
            <div className="flex flex-1 flex-col gap-2">
              <Skeleton className="h-5 w-48" />
              <Skeleton className="h-3.5 w-72 max-w-full" />
            </div>
          </div>
          <Skeleton className="mt-4 h-64" />
        </div>
      </Page>
    );
  if (!query.data) {
    const notFound = query.error instanceof CicordApiError && query.error.code === "not_found";
    return (
      <Page>
        {back}
        {notFound ? (
          <Card>
            <EmptyState
              icon={<Puzzle />}
              title={t("plugins.instance.notFound.title")}
              action={
                <Button asChild variant="outline" size="sm">
                  <Link to={pluginsPath(serverKey)}>{t("plugins.instance.back")}</Link>
                </Button>
              }
            >
              {t("plugins.instance.notFound.text", { bot, instance })}
            </EmptyState>
          </Card>
        ) : (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        )}
      </Page>
    );
  }
  return <InstanceView instance={query.data} serverKey={serverKey} back={back} />;
}

function InstanceView({
  instance,
  serverKey,
  back,
}: {
  instance: PluginInstance;
  serverKey: string;
  back: ReactNode;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { path, search } = useLocation();
  const p = instance.plugin;
  const name = p.displayName || p.name;
  const sections: { id: Section; label: string; icon: ReactNode }[] = [
    {
      id: "settings",
      label: t("plugins.instance.sections.settings"),
      icon: <SlidersHorizontal aria-hidden="true" />,
    },
    {
      id: "overview",
      label: t("plugins.instance.sections.overview"),
      icon: <Info aria-hidden="true" />,
    },
    ...(ws.isAdmin
      ? [
          {
            id: "danger" as const,
            label: t("plugins.instance.sections.danger"),
            icon: <Trash2 aria-hidden="true" />,
          },
        ]
      : []),
  ];
  const requested = search.get("section");
  const section: Section = sections.some((s) => s.id === requested)
    ? (requested as Section)
    : "settings";
  const sectionHref = (id: Section) => (id === "settings" ? path : `${path}?section=${id}`);

  return (
    <Page>
      {back}
      <PageHeader
        leading={<PluginGlyph size={40} />}
        title={name}
        description={p.description || undefined}
        actions={
          // The plugin's Store page (versions, what it requests, its other instances).
          ws.isAdmin && instance.source && instance.source !== "local" ? (
            <Button asChild variant="outline" size="sm">
              <Link to={storePluginPath(serverKey, instance.source, p.name)}>
                <StoreIcon />
                {t("plugins.instance.viewInStore")}
              </Link>
            </Button>
          ) : undefined
        }
        meta={
          <>
            <Mono className="text-[11px]">
              {p.name}@{p.version}
            </Mono>
            <span className="text-xs text-muted-foreground">
              {t("plugins.where.instance")}{" "}
              <Mono className="text-foreground">{instance.instance}</Mono> {t("plugins.where.bot")}{" "}
              <Mono className="text-foreground">{instance.bot}</Mono>
            </span>
            <span className="inline-flex items-center gap-1.5">
              {instance.source && instance.source !== "local" ? (
                <Mono className="text-[11px]">{instance.source}</Mono>
              ) : null}
              <TrustBadge trust={instance.trust} />
            </span>
            <HealthBadge health={instance.health} />
          </>
        }
      />
      {instance.update ? (
        <UpdateCallout
          update={instance.update}
          {...(ws.isAdmin
            ? { to: storeUpdatePath(serverKey, instance.source, instance.plugin.name) }
            : {})}
        />
      ) : null}
      {instance.message ? (
        <Callout
          tone={instance.health === "Degraded" ? "danger" : "info"}
          title={cleanMessage(instance.message)}
          details={instance.message}
        />
      ) : null}

      <div className="grid grid-cols-[minmax(0,1fr)] gap-6 md:grid-cols-[11rem_minmax(0,1fr)]">
        <nav aria-label={t("plugins.instance.sections.label", { name })}>
          <ul className="-mx-1 flex gap-1 overflow-x-auto md:sticky md:top-[calc(var(--header-h)+1.5rem)] md:flex-col md:overflow-visible">
            {sections.map((s) => (
              <li key={s.id}>
                <Link
                  to={sectionHref(s.id)}
                  keepScroll
                  aria-current={s.id === section ? "true" : undefined}
                  className={cn(
                    "flex h-8 items-center gap-2 rounded-md px-2.5 text-[13px] whitespace-nowrap text-muted-foreground outline-none hover:bg-surface-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring [&_svg]:size-4",
                    s.id === section && "bg-surface-2 font-medium text-foreground",
                    s.id === "danger" && "text-danger hover:text-danger",
                  )}
                >
                  {s.icon}
                  {s.label}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
        <div className="min-w-0">
          {section === "overview" ? (
            <Overview instance={instance} serverKey={serverKey} />
          ) : section === "danger" ? (
            <DangerZone instance={instance} serverKey={serverKey} />
          ) : (
            <>
              <PluginSettings instance={instance} serverKey={serverKey} />
              {ws.isAdmin &&
                instance.secrets?.map((secret) => (
                  <SecretField key={secret.name} instance={instance} secret={secret} />
                ))}
            </>
          )}
        </div>
      </div>
    </Page>
  );
}

// ───────────────────────────────────────────────────────────── overview

function Overview({ instance, serverKey }: { instance: PluginInstance; serverKey: string }) {
  const { t } = useTranslation();
  const p = instance.plugin;
  return (
    <section className="flex flex-col gap-4" aria-labelledby="instance-overview">
      <h2 id="instance-overview" className="text-[15px] font-semibold">
        {t("plugins.instance.sections.overview")}
      </h2>
      <Card>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 px-4 py-4 text-[13px]">
          <dt className="text-muted-foreground">{t("plugins.instance.facts.plugin")}</dt>
          <dd>
            <Mono className="text-foreground">{p.name}</Mono>
          </dd>
          <dt className="text-muted-foreground">{t("plugins.instance.facts.version")}</dt>
          <dd>
            <Mono className="text-foreground">{p.version}</Mono>
          </dd>
          <dt className="text-muted-foreground">{t("plugins.instance.facts.instance")}</dt>
          <dd>
            <Mono className="text-foreground">
              {instance.bot}/{instance.instance}
            </Mono>
          </dd>
          <dt className="text-muted-foreground">{t("plugins.instance.facts.server")}</dt>
          <dd>
            <Mono className="text-foreground">{serverKey}</Mono>
          </dd>
          <dt className="text-muted-foreground">{t("plugins.instance.facts.source")}</dt>
          <dd className="flex flex-wrap items-center gap-1.5">
            <Mono className="text-foreground">{instance.source}</Mono>
            <TrustBadge trust={instance.trust} />
          </dd>
        </dl>
      </Card>
      <Card>
        <div className="grid gap-x-6 gap-y-5 px-4 py-4 md:grid-cols-2">
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
          <MetaBlock title={t("plugins.meta.routes")}>
            {instance.routes?.length ? (
              <ul className="flex flex-col gap-1">
                {instance.routes.map((r) => (
                  <li key={r}>
                    <Mono className="text-foreground">{r}</Mono>
                  </li>
                ))}
              </ul>
            ) : (
              <None />
            )}
          </MetaBlock>
        </div>
      </Card>
    </section>
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

// ───────────────────────────────────────────────────────────── uninstall

/** What the Plugins table shows after an uninstall made on this page. */
export interface UninstallFlash {
  name: string;
  bot: string;
  instance: string;
  server: string;
  result: UninstallResult;
}

function SecretField({
  instance,
  secret,
}: {
  instance: PluginInstance;
  secret: NonNullable<PluginInstance["secrets"]>[number];
}) {
  const { t } = useTranslation(),
    { client, cache } = useCicord();
  const [value, setValue] = useState(""),
    [busy, setBusy] = useState(false),
    [state, setState] = useState("");
  return (
    <form
      className="mt-4 space-y-2 rounded-md border p-4"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setState("");
        try {
          await client.admin.setPluginSecret(
            instance.server,
            instance.bot,
            instance.instance,
            secret.name,
            value,
          );
          setValue("");
          setState(t("plugins.instance.secretSaved"));
          await cache.invalidate(keys.plugins(instance.server));
        } catch {
          setState(t("plugins.instance.secretFailed"));
        } finally {
          setBusy(false);
        }
      }}
    >
      <label className="block font-medium" htmlFor={"secret-" + secret.name}>
        {secret.name}
      </label>
      <p className="text-sm text-muted-foreground">
        {secret.description} · {secret.hosts.join(", ")}
      </p>
      {secret.bound && !secret.external ? (
        <>
          <input
            id={"secret-" + secret.name}
            className="rounded border bg-background px-2 py-1"
            type="password"
            autoComplete="new-password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            maxLength={8192}
            required
          />
          <Button type="submit" disabled={busy || !value}>
            {t("plugins.instance.saveSecret")}
          </Button>
        </>
      ) : (
        <p>
          {t(
            secret.external ? "plugins.instance.secretExternal" : "plugins.instance.secretUnbound",
          )}
        </p>
      )}
      <span role="status">{state}</span>
    </form>
  );
}

function DangerZone({ instance, serverKey }: { instance: PluginInstance; serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { client, cache } = useCicord();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<unknown>(null);
  const p = instance.plugin;
  const name = p.displayName || p.name;
  const server = ws.server?.name ?? serverKey;

  const uninstall = async () => {
    setPending(true);
    setProblem(null);
    try {
      const result = await client.admin.uninstallPlugin(serverKey, instance.bot, instance.instance);
      setOpen(false);
      setFlash(`uninstall:${serverKey}`, {
        name,
        bot: instance.bot,
        instance: instance.instance,
        server,
        result,
      } satisfies UninstallFlash);
      if (result.mode === "pullRequest") toast.info(t("plugins.uninstall.done.pr"));
      else toast.success(t("plugins.uninstall.done.title", { name, server }));
      // Leave first: refetching this instance now would 404 under our feet.
      navigate(pluginsPath(serverKey));
      await Promise.all([
        cache.invalidate(keys.plugins(serverKey)),
        cache.invalidate(keys.store()),
        cache.invalidate(["admin", "status"]),
        cache.invalidate(["admin", "history"]),
        cache.invalidate(["admin", "server", serverKey, "audit"]),
      ]);
    } catch (e) {
      setOpen(false);
      setProblem(e);
    } finally {
      setPending(false);
    }
  };

  return (
    <section className="flex flex-col gap-4" aria-labelledby="instance-danger">
      <h2 id="instance-danger" className="text-[15px] font-semibold">
        {t("plugins.instance.sections.danger")}
      </h2>
      {problem ? <UninstallProblem error={problem} /> : null}
      <Card className="border-danger/30">
        <div className="flex flex-wrap items-start gap-4 px-4 py-4">
          <div className="min-w-0 flex-1">
            <h3 className="text-[14px] font-semibold">
              {t("plugins.uninstall.title", { server })}
            </h3>
            <p className="mt-1 max-w-[70ch] text-[13px] text-muted-foreground">
              {t("plugins.uninstall.lead", { name })}
            </p>
          </div>
          <Button variant="destructive" onClick={() => setOpen(true)} loading={pending}>
            {pending ? null : <Trash2 />}
            {t("plugins.uninstall.open")}
          </Button>
        </div>
      </Card>

      <AlertDialog open={open} onOpenChange={(o) => !pending && setOpen(o)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("plugins.uninstall.confirm.title", { name, server })}
            </AlertDialogTitle>
            <AlertDialogDescription>{t("plugins.uninstall.confirm.lead")}</AlertDialogDescription>
          </AlertDialogHeader>
          <ul className="flex list-disc flex-col gap-1.5 pl-5 text-[13px] text-foreground/90">
            <li>
              {t("plugins.uninstall.confirm.removed", {
                instance: `${instance.bot}/${instance.instance}`,
                server,
              })}
            </li>
            <li>{t("plugins.uninstall.confirm.otherServers", { server })}</li>
            <li>
              {t("plugins.uninstall.confirm.last", { name: p.name })}{" "}
              <span className="text-muted-foreground">{t("plugins.uninstall.confirm.repo")}</span>
            </li>
            <li>{t("plugins.uninstall.confirm.data")}</li>
          </ul>
          <p className="text-xs text-muted-foreground">{t("plugins.uninstall.confirm.git")}</p>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={pending}>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={pending}
              onClick={(e) => {
                e.preventDefault();
                void uninstall();
              }}
            >
              {pending
                ? t("plugins.uninstall.confirm.pending")
                : t("plugins.uninstall.confirm.submit")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function UninstallProblem({ error }: { error: unknown }) {
  const { t } = useTranslation();
  const api = error instanceof CicordApiError ? error : undefined;
  switch (api?.code) {
    case "invalid":
      return (
        <Callout
          tone="danger"
          title={t("plugins.uninstall.invalid.title")}
          hint={api.hint ?? t("plugins.uninstall.invalid.hint")}
          details={api.details}
        >
          <p>{api.message}</p>
          {api.diagnostics.length ? (
            <ul className="mt-1 list-disc pl-5" aria-label={t("plugins.uninstall.invalid.list")}>
              {api.diagnostics.map((d, i) => (
                <li key={`${d.path ?? ""}-${i}`}>
                  {cleanMessage(d.message)}
                  {d.path ? <Mono className="ml-1.5 text-[11px]">{d.path}</Mono> : null}
                </li>
              ))}
            </ul>
          ) : null}
        </Callout>
      );
    case "conflict":
      return (
        <Callout
          tone="warning"
          title={t("plugins.uninstall.conflict.title")}
          hint={api.hint ?? t("plugins.uninstall.conflict.hint")}
          details={api.details}
        >
          {api.message}
        </Callout>
      );
    case "forbidden":
      return (
        <Callout
          tone="danger"
          title={t("plugins.uninstall.forbidden.title")}
          hint={t("plugins.uninstall.forbidden.hint")}
        >
          {api.message}
        </Callout>
      );
    default: {
      const d = describeError(error);
      return (
        <Callout
          tone="danger"
          title={t("plugins.uninstall.failed")}
          hint={d.hint}
          details={d.details}
        >
          {d.message}
        </Callout>
      );
    }
  }
}

/** The result of an uninstall, on the Plugins table. */
export function UninstallNotice({
  flash,
  onDismiss,
}: {
  flash: UninstallFlash;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  const r = flash.result;
  const pr = r.mode === "pullRequest";
  return (
    <div
      role="status"
      className={cn(
        "flex flex-wrap items-start gap-3 rounded-lg border px-4 py-3 text-[13px]",
        pr ? "border-info/30 bg-info-soft" : "border-success/30 bg-success-soft",
      )}
    >
      {pr ? (
        <Info className="mt-0.5 size-4 shrink-0 text-info" aria-hidden="true" />
      ) : (
        <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" aria-hidden="true" />
      )}
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="font-semibold">
          {pr
            ? t("plugins.uninstall.done.pr")
            : t("plugins.uninstall.done.title", { name: flash.name, server: flash.server })}
        </p>
        <p className="text-foreground/90">
          {pr
            ? t("plugins.uninstall.done.prText")
            : r.removed
              ? t("plugins.uninstall.done.removed", {
                  instance: `${flash.bot}/${flash.instance}`,
                })
              : t("plugins.uninstall.done.serverOnly", {
                  instance: `${flash.bot}/${flash.instance}`,
                  server: flash.server,
                })}
        </p>
        {r.cleaned.length ? (
          <div>
            <p>{t("plugins.uninstall.done.cleaned")}</p>
            <ul className="mt-0.5 list-disc pl-5" aria-label={t("plugins.uninstall.done.cleaned")}>
              {r.cleaned.map((c) => (
                <li key={c}>
                  <Mono className="text-foreground">{c}</Mono>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        <p className="text-xs text-muted-foreground">{t("plugins.uninstall.done.dataKept")}</p>
        <WriteWarnings warnings={r.warnings} />
        <div className="flex flex-wrap items-center gap-2">
          {pr && r.pullRequestUrl ? (
            <a
              href={r.pullRequestUrl}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1 text-brand hover:underline"
            >
              {t("plugins.uninstall.done.openPr")}
              <ExternalLink className="size-3" aria-hidden="true" />
            </a>
          ) : r.revision ? (
            <span className="inline-flex items-center gap-1">
              <Sha id={r.revision} icon /> {t("plugins.outcome.active")}
            </span>
          ) : null}
        </div>
      </div>
      <Button variant="ghost" size="sm" onClick={onDismiss}>
        {t("common.dismiss")}
      </Button>
    </div>
  );
}
