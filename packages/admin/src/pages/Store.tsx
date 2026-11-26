/**
 * Store (https://cicord.dev/docs/reference/http-api/store/): browse the plugins of every source, review what a plugin requests,
 * install it on the current server, and manage the registries of cicord.yaml.
 * Trust is shown everywhere a plugin or a source appears: `official` vs `unofficial`.
 */
import {
  CicordApiError,
  type InstallResult,
  type StoreDetail,
  type StoreInstallRequest,
  type StorePlugin,
  type StorePluginParams,
  type StorePluginSortKey,
  type StoreSourceSummary,
  type StoreTrust,
  type WriteResult,
} from "@cicord/client";
import { useCicord } from "@cicord/react";
import {
  ArrowLeft,
  ArrowUpCircle,
  CheckCircle2,
  ExternalLink,
  Globe,
  KeyRound,
  Lock,
  Plus,
  Puzzle,
  RefreshCw,
  SlidersHorizontal,
  Slash,
  Store as StoreIcon,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";
import { keys, useResources, useStatus, useStoreDetail, useStoreSources } from "../api.ts";
import {
  EmptyState,
  Mono,
  Page,
  PageHeader,
  PluginGlyph,
  Sha,
  Time,
} from "../components/common.tsx";
import { DataTable, type DataTableColumn, type TableQuery } from "../components/data-table.tsx";
import { Callout, ErrorState, HumanErrorCallout, toastError } from "../components/errors.tsx";
import { HealthBadge } from "../components/status.tsx";
import { RISK, TrustBadge } from "../components/trust.tsx";
import { UpdateBadge, WriteWarnings } from "../components/update.tsx";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../components/ui/tabs.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { describeError } from "../lib/errors.ts";
import { listUrl } from "../lib/flash.ts";
import { cn } from "../lib/utils.ts";
import { Link, navigate, useLocation } from "../router.tsx";
import {
  applyDefaults,
  collectPointers,
  errorsByField,
  mapDiagnostics,
  mergeErrors,
  SchemaForm,
  installationSchema,
  validate,
  type JsonObject,
  type JsonSchema,
  type MappedDiagnostics,
} from "../schema-form/index.ts";
import { CopyField, ExternalButton } from "../setup/kit.tsx";
import { dyn, nonDefaults } from "../setup/steps-end.tsx";
import { instancePath, storePluginPath, storeUpdatePath } from "../shell/nav.ts";
import { StoreUpdate } from "./StoreUpdate.tsx";

type T = ReturnType<typeof useTranslation>["t"];

export const DEVELOPER_PORTAL = "https://discord.com/developers/applications";

/** Human label of a capability ("Send messages"), the raw name when unknown. */
export const capabilityLabel = (t: T, cap: string) =>
  dyn(t)(`setup.capabilities.${cap.replaceAll(".", "_")}`, { defaultValue: cap });

/** "Message Content Intent" for MessageContent, the raw name when unknown. */
export const intentLabel = (t: T, intent: string) =>
  dyn(t)(`setup.discord.toggle.${intent}`, { defaultValue: intent });

const REGISTRY_NAME = /^[a-z0-9][a-z0-9-]{0,31}$/;

export type RegistryFieldError =
  | "nameRequired"
  | "namePattern"
  | "nameReserved"
  | "nameTaken"
  | "urlRequired"
  | "urlInvalid"
  | "urlHttps"
  | "urlTaken";

/** What `validateRegistry` needs of the existing sources. */
export type RegistryRef = Pick<StoreSourceSummary, "name" | "url">;

/** Client-side checks of `POST /registries` (the runtime checks them again). */
export function validateRegistry(
  name: string,
  url: string,
  sources: readonly RegistryRef[],
): { name?: RegistryFieldError; url?: RegistryFieldError } {
  const out: { name?: RegistryFieldError; url?: RegistryFieldError } = {};
  const n = name.trim();
  if (!n) out.name = "nameRequired";
  else if (n === "cicord") out.name = "nameReserved";
  else if (!REGISTRY_NAME.test(n)) out.name = "namePattern";
  else if (sources.some((s) => s.name === n)) out.name = "nameTaken";
  const u = url.trim();
  if (!u) out.url = "urlRequired";
  else {
    let parsed: URL | null = null;
    try {
      parsed = new URL(u);
    } catch {
      parsed = null;
    }
    if (!parsed) out.url = "urlInvalid";
    else if (parsed.protocol !== "https:" || !u.toLowerCase().startsWith("https://"))
      out.url = "urlHttps";
    else if (sources.some((s) => s.url === u)) out.url = "urlTaken";
  }
  return out;
}

/** `error.link` of an explained error (developer portal…), when the runtime sent one. */
export function errorLink(error: unknown): string | undefined {
  if (!(error instanceof CicordApiError) || !error.body || typeof error.body !== "object")
    return undefined;
  const e = (error.body as { error?: { link?: unknown } }).error;
  return e && typeof e.link === "string" && e.link ? e.link : undefined;
}

/** The instances listed by an `in_use` error (`details`: text lines or a list). */
export function inUseItems(error: unknown): string[] {
  if (!(error instanceof CicordApiError)) return [];
  const body = error.body;
  const raw =
    body && typeof body === "object" && !Array.isArray(body)
      ? (body as { error?: { details?: unknown } }).error?.details
      : undefined;
  if (Array.isArray(raw))
    return raw.map((item) => {
      if (typeof item === "string") return item;
      if (item && typeof item === "object") {
        const o = item as Record<string, unknown>;
        if (typeof o["bot"] === "string" && typeof o["instance"] === "string")
          return `${o["bot"]}/${o["instance"]}${typeof o["server"] === "string" ? ` (${o["server"]})` : ""}`;
      }
      return JSON.stringify(item);
    });
  if (typeof raw === "string")
    return raw
      .split(/\n|;\s*/)
      .map((s) => s.trim())
      .filter(Boolean);
  return [];
}

// ───────────────────────────────────────────────────────────── page

function AdminOnly() {
  const { t } = useTranslation();
  return (
    <Page>
      <PageHeader title={t("store.title")} description={t("store.description")} />
      <Callout tone="info" title={t("store.adminOnly.title")}>
        {t("store.adminOnly.text")}
      </Callout>
    </Page>
  );
}

export function StorePage({ serverKey }: { serverKey: string }) {
  const ws = useWorkspace();
  if (!ws.isAdmin) return <AdminOnly />;
  return <Store serverKey={serverKey} />;
}

type Tab = "browse" | "sources";

function Store({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const sources = useStoreSources();
  const { path, search } = useLocation();
  const tab: Tab = search.get("tab") === "sources" ? "sources" : "browse";
  const [refreshing, setRefreshing] = useState(false);

  const setTab = (next: string) => {
    const params = new URLSearchParams(search);
    if (next === "sources") params.set("tab", "sources");
    else params.delete("tab");
    const qs = params.toString();
    navigate(qs ? `${path}?${qs}` : path, { keepScroll: true });
  };

  const refresh = async () => {
    setRefreshing(true);
    try {
      await client.store.refresh();
      // Sources, the plugin table, the plugin pages and update reviews ("the latest
      // version": review them again), and the update badges of the Plugins table.
      await Promise.all([
        cache.invalidate(keys.store()),
        cache.invalidate(keys.plugins(serverKey)),
      ]);
      toast.success(t("store.refreshed"));
    } catch (e) {
      toastError(e, t("store.refreshFailed"));
    } finally {
      setRefreshing(false);
    }
  };

  const total = sources.data?.reduce((n, s) => n + s.pluginCount, 0) ?? 0;
  return (
    <Page>
      <PageHeader
        title={t("store.title")}
        description={t("store.description")}
        meta={
          sources.data ? (
            <span className="text-xs text-muted-foreground">
              {t("store.search.results", { count: total })} ·{" "}
              {t("store.sourcesCount", { count: sources.data.length })}
            </span>
          ) : null
        }
        actions={
          <Button
            variant="outline"
            loading={refreshing}
            onClick={() => void refresh()}
            disabled={!sources.data}
          >
            {refreshing ? null : <RefreshCw />}
            {t("store.refresh")}
          </Button>
        }
      />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList aria-label={t("store.tabs.label")} className="w-full justify-start">
          <TabsTrigger value="browse">
            <StoreIcon className="size-3.5" aria-hidden="true" />
            {t("store.tabs.browse")}
          </TabsTrigger>
          <TabsTrigger value="sources">
            <Globe className="size-3.5" aria-hidden="true" />
            {t("store.tabs.sources")}
            {sources.data ? (
              <span className="tabular text-xs text-subtle-foreground">{sources.data.length}</span>
            ) : null}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="browse" className="pt-5">
          <Browse sources={sources.data ?? []} serverKey={serverKey} />
        </TabsContent>
        <TabsContent value="sources" className="pt-5">
          {sources.isLoading ? (
            <Card>
              <div
                className="flex flex-col gap-3 p-4"
                aria-busy="true"
                aria-label={t("common.loading")}
              >
                {[0, 1, 2].map((i) => (
                  <Skeleton key={i} className="h-10" />
                ))}
              </div>
            </Card>
          ) : !sources.data ? (
            <ErrorState error={sources.error} onRetry={() => void sources.refetch()} />
          ) : (
            <Sources sources={sources.data} />
          )}
        </TabsContent>
      </Tabs>
    </Page>
  );
}

// ───────────────────────────────────────────────────────────── browse

const storeParams = (q: TableQuery): StorePluginParams => ({
  ...(q.q ? { q: q.q } : {}),
  sort: q.sort as StorePluginSortKey,
  order: q.order,
  offset: q.offset,
  limit: q.limit,
  ...(q.filters["source"] ? { source: q.filters["source"] } : {}),
  ...(q.filters["trust"] ? { trust: q.filters["trust"] as StoreTrust } : {}),
  ...(q.filters["installed"] ? { installed: q.filters["installed"] === "true" } : {}),
  ...(q.filters["updates"] ? { updates: q.filters["updates"] === "true" } : {}),
});

function Browse({ sources, serverKey }: { sources: StoreSourceSummary[]; serverKey: string }) {
  const { t } = useTranslation();
  const { client } = useCicord();
  const failing = sources.filter((s) => s.error);

  const columns: DataTableColumn<StorePlugin>[] = [
    {
      id: "plugin",
      header: t("store.columns.plugin"),
      sortKey: "name",
      primary: true,
      className: "min-w-64",
      cell: (p) => (
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-medium text-foreground">{p.displayName || p.name}</span>
            <Mono className="text-[11px]">
              {p.source}/{p.name}
            </Mono>
          </span>
          {p.description ? (
            <span className="line-clamp-1 max-w-[52ch] text-xs text-muted-foreground">
              {p.description}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      id: "source",
      header: t("store.columns.source"),
      sortKey: "source",
      skeleton: "w-14",
      cell: (p) => <Mono>{p.source}</Mono>,
    },
    {
      id: "trust",
      header: t("store.columns.trust"),
      sortKey: "trust",
      skeleton: "w-20",
      cell: (p) => <TrustBadge trust={p.trust} />,
    },
    {
      id: "version",
      header: t("store.columns.version"),
      sortKey: "version",
      skeleton: "w-12",
      cell: (p) => (
        <span className="inline-flex flex-wrap items-center gap-1.5">
          {p.update ? (
            <Mono>{p.update.current}</Mono>
          ) : p.version ? (
            <Mono>{p.version}</Mono>
          ) : (
            <span className="text-subtle-foreground">—</span>
          )}
          {p.update ? (
            <UpdateBadge update={p.update} to={storeUpdatePath(serverKey, p.source, p.name)} />
          ) : null}
        </span>
      ),
    },
    {
      id: "publisher",
      header: t("store.columns.publisher"),
      sortKey: "publisher",
      skeleton: "w-16",
      cell: (p) => (
        <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
          {p.publisher ?? <span className="text-subtle-foreground">—</span>}
          {p.verified ? (
            <CheckCircle2
              className="size-3.5 text-success"
              aria-label={t("store.card.verified")}
              role="img"
            />
          ) : null}
        </span>
      ),
    },
    {
      id: "installed",
      header: t("store.columns.installed"),
      sortKey: "installed",
      descFirst: true,
      className: "text-right",
      skeleton: "ml-auto w-8",
      cell: (p) => (
        <span className="inline-flex items-center justify-end gap-1.5 whitespace-nowrap">
          {p.installed.some((i) => i.server === serverKey) ? (
            <Badge tone="info" title={t("store.card.installedTip")}>
              {t("store.columns.here")}
            </Badge>
          ) : null}
          {p.installed.length ? (
            <span className="tabular">{p.installed.length}</span>
          ) : (
            <span className="text-subtle-foreground">—</span>
          )}
        </span>
      ),
    },
  ];

  return (
    <div className="flex flex-col gap-4">
      {failing.map((s) => (
        <HumanErrorCallout
          key={s.name}
          error={{ ...s.error!, hint: s.error!.hint ?? t("store.source.fetchHint") }}
          title={t("store.source.fetchFailed", { name: s.name })}
        />
      ))}
      <DataTable
        label={t("store.table")}
        columns={columns}
        queryKey={(q) => keys.storePlugins(storeParams(q))}
        fetch={(q, signal) => client.store.plugins(storeParams(q), { signal })}
        rowKey={(p) => `${p.source}/${p.name}`}
        rowHref={(p) => storePluginPath(serverKey, p.source, p.name)}
        defaultSort="name"
        rememberAs={`store:${serverKey}`}
        search={{ label: t("store.search.label"), placeholder: t("store.search.placeholder") }}
        filters={[
          {
            id: "source",
            label: t("store.filters.source"),
            allLabel: t("store.filters.allSources"),
            options: sources.map((s) => ({ value: s.name, label: s.name })),
          },
          {
            id: "trust",
            label: t("store.filters.trust"),
            allLabel: t("store.filters.anyTrust"),
            options: [
              { value: "official", label: t("store.trust.official") },
              { value: "unofficial", label: t("store.trust.unofficial") },
            ],
          },
          {
            id: "installed",
            label: t("store.filters.installed"),
            allLabel: t("store.filters.anyInstalled"),
            options: [
              { value: "true", label: t("store.filters.installedYes") },
              { value: "false", label: t("store.filters.installedNo") },
            ],
          },
          {
            id: "updates",
            label: t("store.filters.updates"),
            allLabel: t("store.filters.anyUpdates"),
            options: [
              { value: "true", label: t("store.filters.updatesYes") },
              { value: "false", label: t("store.filters.updatesNo") },
            ],
          },
        ]}
        empty={
          <EmptyState icon={<Puzzle />} title={t("store.empty.title")}>
            {t("store.empty.text")}
          </EmptyState>
        }
      />
    </div>
  );
}

// ───────────────────────────────────────────────────────────── plugin page & install

/** The page of a Store plugin (/servers/:key/store/:source/:plugin): review, then install. */
export function StorePluginPage({
  serverKey,
  source,
  plugin,
}: {
  serverKey: string;
  source: string;
  plugin: string;
}) {
  const ws = useWorkspace();
  const { search } = useLocation();
  if (!ws.isAdmin) return <AdminOnly />;
  if (search.get("update") === "1")
    return <StoreUpdate source={source} name={plugin} serverKey={serverKey} />;
  return <PluginDetail source={source} name={plugin} serverKey={serverKey} />;
}

type Step = "review" | "install";

function PluginDetail({
  source,
  name,
  serverKey,
}: {
  source: string;
  name: string;
  serverKey: string;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { cache } = useCicord();
  const detail = useStoreDetail(source, name);
  const [step, setStep] = useState<Step>("review");
  const [done, setDone] = useState<{ result: InstallResult; bot: string | undefined } | null>(null);
  const [reloaded, setReloaded] = useState(false);
  const serverName = ws.server?.name ?? serverKey;
  const d = detail.data;
  const storePath = listUrl(
    `store:${serverKey}`,
    `/servers/${encodeURIComponent(serverKey)}/store`,
  );

  const reviewAgain = async () => {
    await cache.invalidate(keys.storeDetail(source, name));
    await detail.refetch();
    setReloaded(true);
    setStep("review");
  };

  const notFound = detail.error instanceof CicordApiError && detail.error.code === "not_found";
  return (
    <Page className="max-w-4xl">
      {step === "install" && !done && d ? (
        <Button variant="ghost" size="sm" className="-ml-2 w-fit" onClick={() => setStep("review")}>
          <ArrowLeft />
          {t("store.install.back")}
        </Button>
      ) : (
        <Link
          to={storePath}
          className="inline-flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          {t("store.detail.back")}
        </Link>
      )}
      {detail.isLoading ? (
        <div className="flex flex-col gap-3" aria-busy="true" aria-label={t("common.loading")}>
          <Skeleton className="h-8 w-2/3" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-5/6" />
          <Skeleton className="mt-4 h-40" />
        </div>
      ) : !d ? (
        notFound ? (
          <Card>
            <EmptyState
              icon={<Puzzle />}
              title={t("store.detail.notFound.title")}
              action={
                <Button asChild variant="outline" size="sm">
                  <Link to={storePath}>{t("store.detail.back")}</Link>
                </Button>
              }
            >
              {t("store.detail.notFound.text", { plugin: `${source}/${name}` })}
            </EmptyState>
          </Card>
        ) : (
          <>
            <h1 className="text-xl font-semibold">{t("store.detail.title", { name })}</h1>
            <ErrorState
              error={detail.error}
              title={t("store.detail.loadFailed")}
              onRetry={() => void detail.refetch()}
            />
          </>
        )
      ) : (
        <>
          <DetailHeader detail={d} />
          {done ? (
            <InstallDone
              result={done.result}
              bot={done.bot}
              detail={d}
              serverKey={serverKey}
              serverName={serverName}
            />
          ) : step === "review" ? (
            <>
              {reloaded ? (
                <Callout tone="info" title={t("store.install.changed.title")}>
                  {t("store.install.changed.hint")}
                </Callout>
              ) : null}
              {d.update ? <UpdateCallout update={d.update} inBar /> : null}
              <Card className="px-4 py-4">
                <Review detail={d} serverKey={serverKey} />
              </Card>
              <DetailActions
                detail={d}
                serverKey={serverKey}
                serverName={serverName}
                onInstall={() => setStep("install")}
              />
            </>
          ) : (
            <InstallForm
              key={`${d.version}:${d.commit ?? ""}:${d.integrity ?? ""}`}
              detail={d}
              serverKey={serverKey}
              serverName={serverName}
              onDone={(result, bot) => setDone({ result, bot })}
              onChanged={() => void reviewAgain()}
            />
          )}
        </>
      )}
    </Page>
  );
}

/** At most this many instances get their own "Open" button in the action bar. */
const OPEN_IN_BAR = 3;

/**
 * The action bar of a Store page. Installed on this server: the primary action is the
 * update ("Update to X", the update review) when there is one, else opening the instance
 * (each of them, when there are several). Installing another instance (another name,
 * settings and data) is a rare, advanced action: secondary, and explained.
 */
function DetailActions({
  detail: d,
  serverKey,
  serverName,
  onInstall,
}: {
  detail: StoreDetail;
  serverKey: string;
  serverName: string;
  onInstall: () => void;
}) {
  const { t } = useTranslation();
  const hintId = useId();
  const here = d.installed.filter((i) => i.server === serverKey);
  const installLabel = here.length
    ? t("store.detail.installAnother")
    : t("store.detail.install", { server: serverName });
  if (!d.update && !here.length)
    return (
      <StickyBar>
        <Button onClick={onInstall}>{installLabel}</Button>
      </StickyBar>
    );
  const open = here.slice(0, OPEN_IN_BAR);
  return (
    <StickyBar>
      <div className="mr-auto flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        {/* With an update pending, a new instance would need the new version, which the
            others do not run yet: the version is shared, so the update comes first. */}
        <Button
          variant="outline"
          size="sm"
          onClick={onInstall}
          disabled={!!d.update}
          aria-describedby={here.length || d.update ? hintId : undefined}
        >
          <Plus />
          {installLabel}
        </Button>
        {here.length || d.update ? (
          <p id={hintId} className="text-xs text-muted-foreground">
            {d.update
              ? t("store.detail.updateFirst", { version: d.update.latest })
              : t("store.detail.installAnotherHint")}
          </p>
        ) : null}
      </div>
      {d.update ? (
        <Button asChild>
          <Link to={storeUpdatePath(serverKey, d.source, d.name)}>
            <ArrowUpCircle />
            {t("store.update.available.review", { version: d.update.latest })}
          </Link>
        </Button>
      ) : here.length === 1 ? (
        <Button asChild>
          <Link to={instancePath(serverKey, here[0]!.bot, here[0]!.instance)}>
            <SlidersHorizontal />
            {t("store.detail.open")}
          </Link>
        </Button>
      ) : (
        <>
          {here.length > OPEN_IN_BAR ? (
            <span className="text-xs text-muted-foreground">
              {t("store.detail.openMore", { count: here.length - OPEN_IN_BAR })}
            </span>
          ) : null}
          {open.map((i) => (
            <Button key={`${i.bot}/${i.instance}`} asChild variant="secondary">
              <Link to={instancePath(serverKey, i.bot, i.instance)}>
                <SlidersHorizontal />
                {t("store.detail.openInstance", { instance: i.instance })}
              </Link>
            </Button>
          ))}
        </>
      )}
    </StickyBar>
  );
}

/** "Update available" on the page of an installed plugin (Store page, instance page). */
export function UpdateCallout({
  update,
  to,
  inBar = false,
}: {
  update: NonNullable<StoreDetail["update"]>;
  /** The update review; absent when the viewer cannot update (not admin). */
  to?: string;
  /** The page's action bar offers the update (the Store page): no button of its own. */
  inBar?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <Callout
      tone={update.modified || update.level === "major" ? "warning" : "info"}
      title={t("store.update.available.title", { current: update.current, latest: update.latest })}
      action={
        to && !inBar ? (
          <Button asChild size="sm">
            <Link to={to}>
              <ArrowUpCircle />
              {t("store.update.available.review", { version: update.latest })}
            </Link>
          </Button>
        ) : undefined
      }
    >
      <p>{to || inBar ? t("store.update.available.text") : t("store.update.available.askAdmin")}</p>
      {update.level === "major" ? <p>{t("store.update.available.major")}</p> : null}
      {update.modified ? <p>{t("store.update.available.modified")}</p> : null}
    </Callout>
  );
}

/** Action bar that stays at the bottom of the viewport while the page scrolls. */
export function StickyBar({ children }: { children: ReactNode }) {
  return (
    <div className="sticky bottom-3 z-[4] flex flex-wrap items-center justify-end gap-2 rounded-lg border border-border-strong bg-popover/95 px-4 py-2.5 shadow-pop backdrop-blur">
      {children}
    </div>
  );
}

export function DetailHeader({ detail }: { detail: StoreDetail }) {
  const { t } = useTranslation();
  const name = detail.displayName || detail.name;
  return (
    <header className="flex flex-col gap-3">
      <div className="flex items-start gap-3">
        <PluginGlyph size={40} />
        <div className="min-w-0 flex-1">
          <h1 className="text-xl font-semibold tracking-[-0.01em]">{name}</h1>
          <Mono className="text-[11px]">
            {detail.source}/{detail.name}@{detail.version}
          </Mono>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <TrustBadge trust={detail.trust} />
        {detail.verified ? (
          <Badge tone="success">
            <CheckCircle2 aria-hidden="true" />
            {t("store.card.verified")}
          </Badge>
        ) : null}
        {detail.keywords.map((k) => (
          <Badge key={k}>{k}</Badge>
        ))}
      </div>
      <p className="max-w-[70ch] text-[13px] text-foreground/90">{detail.description}</p>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
        <dt className="text-muted-foreground">{t("store.detail.version")}</dt>
        <dd className="font-mono text-[12px]">{detail.version}</dd>
        {detail.publisher ? (
          <>
            <dt className="text-muted-foreground">{t("store.detail.publisher")}</dt>
            <dd>{detail.publisher}</dd>
          </>
        ) : null}
        {detail.commit ? (
          <>
            <dt className="text-muted-foreground">{t("store.detail.commit")}</dt>
            <dd>
              <Sha id={detail.commit} />
            </dd>
          </>
        ) : null}
        {detail.repo ? (
          <>
            <dt className="text-muted-foreground">{t("store.detail.repo")}</dt>
            <dd className="min-w-0">
              <a
                href={detail.repo}
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex max-w-full items-center gap-1 text-brand hover:underline"
              >
                <span className="truncate">{detail.repo}</span>
                <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
              </a>
            </dd>
          </>
        ) : null}
      </dl>
      {detail.trust === "unofficial" ? (
        <Callout
          tone="warning"
          title={t("store.detail.unofficial.title", { source: detail.source })}
        >
          {t("store.detail.unofficial.text", {
            publisher: detail.publisher || detail.source,
          })}
        </Callout>
      ) : null}
    </header>
  );
}

export function Block({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <h4 className="text-xs font-medium text-muted-foreground">{title}</h4>
      {children}
      {hint ? <p className="text-xs text-subtle-foreground">{hint}</p> : null}
    </div>
  );
}

export function Nothing({ children }: { children: ReactNode }) {
  return <p className="text-[13px] text-subtle-foreground">{children}</p>;
}

/** Permissions review: what the plugin requests and where it already runs. */
function Review({ detail, serverKey }: { detail: StoreDetail; serverKey: string }) {
  const { t } = useTranslation();
  const privileged = new Set(detail.privilegedIntents);
  return (
    <section aria-labelledby="store-review" className="flex flex-col gap-5">
      <div>
        <h3 id="store-review" className="text-[14px] font-semibold">
          {t("store.detail.review")}
        </h3>
        <p className="text-xs text-muted-foreground">{t("store.detail.reviewHint")}</p>
      </div>
      {detail.interop && (
        <Block title={t("store.detail.events")}>
          <ul>
            {detail.interop.publishes?.map((p) => (
              <li key={p.topic + (p.version ?? 1)}>
                {t("store.detail.publishes")} {detail.name}.{p.topic} v{p.version ?? 1} ·{" "}
                {p.scope ?? "bot"}
              </li>
            ))}
            {detail.interop.subscribes?.map((p) => (
              <li key={p.topic + (p.version ?? 1)}>
                {t("store.detail.subscribes")} {p.topic} v{p.version ?? 1}
              </li>
            ))}
          </ul>
        </Block>
      )}
      {detail.optional?.length ? (
        <Block title={t("store.detail.optional")}>
          <ul>
            {detail.optional.map((f) => (
              <li key={f.feature}>
                {f.feature}
                <ul>
                  {f.capabilities?.map((c) => (
                    <CapabilityRow key={c} cap={c} />
                  ))}
                  {f.intents?.map((intent) => (
                    <li key={intent}>{intent}</li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        </Block>
      ) : null}
      <Block title={t("store.detail.capabilities")}>
        {detail.capabilities.length ? (
          <ul className="flex flex-col gap-1" aria-label={t("store.detail.capabilities")}>
            {detail.capabilities.map((c) => (
              <CapabilityRow key={c} cap={c} />
            ))}
          </ul>
        ) : (
          <Nothing>{t("store.detail.noCapabilities")}</Nothing>
        )}
      </Block>
      <Block
        title={t("store.detail.intents")}
        hint={privileged.size ? t("store.detail.privilegedHint") : undefined}
      >
        {detail.intents.length ? (
          <ul className="flex flex-wrap gap-1.5" aria-label={t("store.detail.intents")}>
            {detail.intents.map((i) => (
              <li key={i}>
                {privileged.has(i) ? (
                  <Badge tone="warning" className="h-6 px-2" title={intentLabel(t, i)}>
                    <TriangleAlert aria-hidden="true" />
                    <span className="font-mono font-normal">{i}</span>
                    <span>· {t("store.detail.privileged")}</span>
                  </Badge>
                ) : (
                  <Badge className="h-6 px-2 font-mono font-normal">{i}</Badge>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <Nothing>{t("store.detail.noIntents")}</Nothing>
        )}
      </Block>
      <Block
        title={t("store.detail.network")}
        hint={detail.network.length ? t("store.detail.networkHint") : undefined}
      >
        {detail.network.length ? (
          <ul className="flex flex-wrap gap-1.5" aria-label={t("store.detail.network")}>
            {detail.network.map((h) => (
              <li key={h}>
                <Badge tone="warning" className="h-6 px-2 font-mono font-normal">
                  <Globe aria-hidden="true" />
                  {h}
                </Badge>
              </li>
            ))}
          </ul>
        ) : (
          <Nothing>{t("store.detail.noNetwork")}</Nothing>
        )}
      </Block>
      <Block
        title={t("store.detail.commands")}
        hint={detail.commands === null ? t("store.detail.commandsUnknownHint") : undefined}
      >
        {detail.commands === null ? (
          <p className="text-[13px]">{t("store.detail.commandsUnknown")}</p>
        ) : detail.commands.length ? (
          <ul className="flex flex-col gap-1">
            {detail.commands.map((c) => (
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
          <Nothing>{t("store.detail.noCommands")}</Nothing>
        )}
      </Block>
      <Block
        title={t("store.detail.installed")}
        hint={detail.installed.length ? t("store.detail.installedHint") : undefined}
      >
        {detail.installed.length ? (
          <ul className="flex flex-col gap-1" aria-label={t("store.detail.installed")}>
            {detail.installed.map((i) => (
              <li key={`${i.server}/${i.bot}/${i.instance}`} className="text-[13px]">
                <Link
                  to={instancePath(i.server, i.bot, i.instance)}
                  className={cn(
                    "rounded-sm font-mono text-[12px] outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring",
                    i.server === serverKey ? "text-brand" : "text-muted-foreground",
                  )}
                >
                  {t("store.detail.instanceOn", {
                    bot: i.bot,
                    instance: i.instance,
                    server: i.server,
                  })}
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <Nothing>{t("store.detail.notInstalled")}</Nothing>
        )}
      </Block>
    </section>
  );
}

export function serverGrants(capabilities: string[]) {
  return capabilities.filter((c) =>
    ["server.overlays", "server.incident", "server.sync"].includes(c),
  );
}
export function ServerGrantConsent({
  grants,
  checked,
  change,
}: {
  grants: string[];
  checked: boolean;
  change: (v: boolean) => void;
}) {
  const { t } = useTranslation();
  return (
    <label className="flex items-start gap-2 rounded-md border border-danger p-3 text-danger">
      <input type="checkbox" checked={checked} onChange={(e) => change(e.target.checked)} />
      {t("store.detail.serverGrants", { grants: grants.join(", ") })}
    </label>
  );
}

export function CapabilityRow({ cap }: { cap: string }) {
  const { t } = useTranslation();
  const wildcard = cap.startsWith("plugins.") && cap.endsWith(".*");
  const risk = wildcard || serverGrants([cap]).length ? "high" : RISK[cap];
  return (
    <li className="flex items-start gap-2 text-[13px]">
      {risk ? (
        <TriangleAlert
          className={cn(
            "mt-0.5 size-3.5 shrink-0",
            risk === "high" ? "text-danger" : "text-warning",
          )}
          aria-hidden="true"
        />
      ) : (
        <CheckCircle2
          className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
      )}
      <span className="min-w-0">
        {wildcard ? t("store.detail.allMethods") : capabilityLabel(t, cap)}{" "}
        <Mono className="text-[11px] text-subtle-foreground">{cap}</Mono>
      </span>
    </li>
  );
}

function InstallForm({
  detail,
  serverKey,
  serverName,
  onDone,
  onChanged,
}: {
  detail: StoreDetail;
  serverKey: string;
  serverName: string;
  onDone: (result: InstallResult, bot: string | undefined) => void;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const status = useStatus();
  const resources = useResources(serverKey);
  const ids = useId();
  const schema = useMemo(() => installationSchema(detail.schema as JsonSchema), [detail.schema]);
  const hasSettings = Object.keys(schema.properties ?? {}).length > 0;
  const bots = (status.data?.bots ?? []).filter((b) => b.servers.includes(serverKey));
  const multiBot = bots.length > 1;
  const unofficial = detail.trust === "unofficial";
  const name = detail.displayName || detail.name;

  const [bot, setBot] = useState("");
  const [instance, setInstance] = useState(detail.name);
  const [draft, setDraft] = useState<JsonObject>(() => applyDefaults(schema, {}));
  const [confirmed, setConfirmed] = useState(false);
  const [serverConfirmed, setServerConfirmed] = useState(false);
  const privilegedGrants = serverGrants(detail.capabilities);
  const [attempted, setAttempted] = useState(false);
  const [serverErrors, setServerErrors] = useState<MappedDiagnostics>({ byField: {}, general: [] });
  const [problem, setProblem] = useState<unknown>(null);
  const [pending, setPending] = useState(false);
  const problemRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (problem) problemRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [problem]);

  const clientErrors = useMemo(
    () => (hasSettings ? validate(schema, draft, resources.data) : []),
    [schema, draft, resources.data, hasSettings],
  );
  const errors = mergeErrors(attempted ? errorsByField(clientErrors) : {}, serverErrors.byField);
  const instanceName = instance.trim();
  const effectiveBot = multiBot ? bot : bots[0]?.key;
  const instanceError = !instanceName
    ? t("store.install.instanceRequired")
    : detail.installed.some(
          (i) => i.server === serverKey && i.instance === instanceName && i.bot === effectiveBot,
        )
      ? t("store.install.conflict.title")
      : undefined;
  const ready =
    (!privilegedGrants.length || serverConfirmed) &&
    (!unofficial || confirmed) &&
    (!multiBot || !!bot);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setAttempted(true);
    if (!ready) return;
    if (clientErrors.length || instanceError) {
      toast.error(t("store.install.fixFirst"));
      return;
    }
    // A cicord plugin from the official registry has a reviewed commit like a registry one.
    const registry = detail.source !== "cicord" || detail.commit !== undefined;
    const request: StoreInstallRequest = {
      source: detail.source,
      plugin: detail.name,
      instance: instanceName,
      config: nonDefaults(schema, draft),
      ...(multiBot && bot ? { bot } : {}),
      ...(registry ? { version: detail.version } : {}),
      ...(registry && detail.commit ? { commit: detail.commit } : {}),
      ...(registry && detail.integrity ? { integrity: detail.integrity } : {}),
      ...(unofficial ? { confirmUnofficial: true } : {}),
      ...(privilegedGrants.length ? { confirmPrivileged: true } : {}),
    };
    setPending(true);
    setProblem(null);
    setServerErrors({ byField: {}, general: [] });
    try {
      const result = await client.store.install(serverKey, request);
      onDone(result, effectiveBot);
      if (result.mode === "pullRequest") toast.info(t("store.install.done.pr"));
      else toast.success(t("store.install.done.title", { name }));
      await Promise.all([
        cache.invalidate(keys.store()),
        cache.invalidate(keys.plugins(serverKey)),
        cache.invalidate(["admin", "status"]),
        cache.invalidate(["admin", "history"]),
        cache.invalidate(["admin", "server", serverKey, "audit"]),
      ]);
    } catch (err) {
      if (err instanceof CicordApiError && err.code === "invalid" && err.diagnostics.length)
        setServerErrors(mapDiagnostics(err.diagnostics, collectPointers(schema, draft)));
      if (err instanceof CicordApiError && err.code === "changed") setConfirmed(false);
      setProblem(err);
    } finally {
      setPending(false);
    }
  };

  const privileged = detail.privilegedIntents;
  return (
    <form
      className="flex flex-col gap-5 rounded-lg border bg-card px-4 py-4"
      onSubmit={(e) => void submit(e)}
      noValidate
    >
      <div>
        <h3 className="flex flex-wrap items-center gap-2 text-[14px] font-semibold">
          {t("store.install.title", { name })}
          <TrustBadge trust={detail.trust} />
        </h3>
        <p className="text-xs text-muted-foreground">
          {t("store.install.lead", { server: serverName })}
        </p>
      </div>

      {multiBot ? (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`${ids}-bot`}>{t("store.install.bot")}</Label>
          <Select value={bot} onValueChange={setBot}>
            <SelectTrigger
              id={`${ids}-bot`}
              aria-invalid={attempted && !bot ? true : undefined}
              className="max-w-xs"
            >
              <SelectValue placeholder={t("store.install.bot")} />
            </SelectTrigger>
            <SelectContent>
              {bots.map((b) => (
                <SelectItem key={b.key} value={b.key}>
                  {b.username ? `${b.username} (${b.key})` : b.key}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-subtle-foreground">{t("store.install.botHint")}</p>
        </div>
      ) : null}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${ids}-instance`}>{t("store.install.instance")}</Label>
        <Input
          id={`${ids}-instance`}
          value={instance}
          onChange={(e) => setInstance(e.target.value)}
          aria-invalid={attempted && instanceError ? true : undefined}
          aria-describedby={`${ids}-instance-hint`}
          className="max-w-xs font-mono"
          autoComplete="off"
          spellCheck={false}
        />
        <p id={`${ids}-instance-hint`} className="text-xs text-subtle-foreground">
          {attempted && instanceError ? (
            <span className="text-danger">{instanceError}</span>
          ) : (
            t("store.install.instanceHint")
          )}
        </p>
      </div>

      <div className="flex flex-col gap-2">
        <h4 className="text-[13px] font-semibold">{t("store.install.settings")}</h4>
        {hasSettings ? (
          <SchemaForm
            schema={schema}
            value={draft}
            onChange={(v) => {
              setDraft(v);
              setServerErrors({ byField: {}, general: [] });
            }}
            {...(resources.data ? { resources: resources.data } : {})}
            errors={errors}
          />
        ) : (
          <p className="text-[13px] text-muted-foreground">{t("store.install.noSettings")}</p>
        )}
      </div>

      {privilegedGrants.length > 0 && (
        <ServerGrantConsent
          grants={privilegedGrants}
          checked={serverConfirmed}
          change={setServerConfirmed}
        />
      )}
      {privileged.length ? (
        <Callout
          tone="warning"
          title={t("store.install.intentsWarning", {
            intents: privileged.map((i) => intentLabel(t, i)).join(", "),
          })}
          action={
            <ExternalButton href={DEVELOPER_PORTAL}>{t("store.detail.portal")}</ExternalButton>
          }
        />
      ) : null}

      {unofficial ? (
        <UnofficialConfirm
          detail={detail}
          checked={confirmed}
          onChange={setConfirmed}
          invalid={attempted && !confirmed}
        />
      ) : null}

      {problem ? (
        <div ref={problemRef} className="scroll-mb-20">
          <InstallProblem
            error={problem}
            general={serverErrors.general}
            instance={instanceName}
            onChanged={onChanged}
          />
        </div>
      ) : null}

      <StickyBar>
        {unofficial && !confirmed ? (
          <span className="mr-auto text-xs text-muted-foreground">
            {t("store.install.confirmRequired")}
          </span>
        ) : null}
        <Button type="submit" loading={pending} disabled={!ready}>
          {pending ? t("store.install.installing") : t("store.install.submit")}
        </Button>
      </StickyBar>
    </form>
  );
}

function UnofficialConfirm({
  detail,
  checked,
  onChange,
  invalid,
}: {
  detail: StoreDetail;
  checked: boolean;
  onChange: (v: boolean) => void;
  invalid: boolean;
}) {
  const { t } = useTranslation();
  const id = useId();
  const caps = detail.capabilities.map((c) => capabilityLabel(t, c));
  return (
    <div
      className={cn(
        "rounded-lg border border-warning/40 bg-warning-soft px-4 py-3",
        invalid && "ring-2 ring-warning/40",
      )}
    >
      <div className="flex items-start gap-3">
        <input
          id={id}
          type="checkbox"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          aria-describedby={`${id}-grants`}
          className="mt-0.5 size-4 shrink-0 accent-[var(--warning)]"
        />
        <div className="min-w-0 text-[13px]">
          <label htmlFor={id} className="font-medium">
            {t("store.install.confirm", {
              publisher: detail.publisher || detail.source,
              name: detail.displayName || detail.name,
              source: detail.source,
            })}
          </label>
          <ul id={`${id}-grants`} className="mt-1.5 list-disc pl-4 text-foreground/90">
            {caps.length ? (
              caps.map((c) => <li key={c}>{c}</li>)
            ) : (
              <li>{t("store.install.confirmNothing")}</li>
            )}
            <li>
              {detail.network.length
                ? t("store.install.confirmNetwork", { hosts: detail.network.join(", ") })
                : t("store.install.confirmNoNetwork")}
            </li>
          </ul>
        </div>
      </div>
    </div>
  );
}

function InstallProblem({
  error,
  general,
  instance,
  onChanged,
}: {
  error: unknown;
  general: string[];
  instance: string;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const code = error instanceof CicordApiError ? error.code : "";
  const message = error instanceof CicordApiError ? error.message : "";
  switch (code) {
    case "changed":
      return (
        <Callout
          tone="warning"
          title={t("store.install.changed.title")}
          hint={t("store.install.changed.hint")}
          details={message}
          action={
            <Button size="sm" variant="outline" onClick={onChanged}>
              <RefreshCw />
              {t("store.install.changed.reload")}
            </Button>
          }
        />
      );
    case "confirmation_required":
      return (
        <Callout
          tone="warning"
          title={t("store.install.confirmation.title")}
          hint={t("store.install.confirmation.hint")}
        >
          {message}
        </Callout>
      );
    case "intents": {
      const hint = error instanceof CicordApiError ? error.hint : undefined;
      return (
        <Callout
          tone="danger"
          title={t("store.install.intents.title")}
          hint={hint ?? t("store.install.intents.hint")}
          action={
            <ExternalButton href={errorLink(error) ?? DEVELOPER_PORTAL}>
              {t("store.detail.portal")}
            </ExternalButton>
          }
        >
          {message}
        </Callout>
      );
    }
    case "conflict":
      return (
        <Callout
          tone="warning"
          title={t("store.install.conflict.title")}
          hint={t("store.install.conflict.hint")}
        >
          {message || instance}
        </Callout>
      );
    case "invalid":
      return (
        <Callout tone="danger" title={t("store.install.rejected")}>
          <p>{message}</p>
          {general.length ? (
            <ul className="mt-1 list-disc pl-5">
              {general.map((g) => (
                <li key={g}>{g}</li>
              ))}
            </ul>
          ) : null}
        </Callout>
      );
    default: {
      const d = describeError(error);
      return (
        <Callout tone="danger" title={t("store.install.failed")} hint={d.hint} details={d.details}>
          {d.message}
        </Callout>
      );
    }
  }
}

function InstallDone({
  result,
  bot,
  detail,
  serverKey,
  serverName,
}: {
  result: InstallResult;
  bot: string | undefined;
  detail: StoreDetail;
  serverKey: string;
  serverName: string;
}) {
  const { t } = useTranslation();
  const name = detail.displayName || detail.name;
  const pr = result.mode === "pullRequest";
  return (
    <section
      className={cn(
        "flex flex-col gap-3 rounded-lg border p-4",
        pr ? "border-info/30 bg-info-soft" : "border-success/30 bg-success-soft",
      )}
      aria-live="polite"
    >
      <p className="flex items-center gap-2 text-[15px] font-semibold">
        <CheckCircle2
          className={cn("size-5", pr ? "text-info" : "text-success")}
          aria-hidden="true"
        />
        {pr ? t("store.install.done.pr") : t("store.install.done.title", { name })}
      </p>
      <WriteWarnings warnings={result.warnings} />
      {pr ? (
        <>
          <p className="text-[13px]">{t("store.install.done.prText")}</p>
          {result.pullRequestUrl ? (
            <div>
              <ExternalButton href={result.pullRequestUrl}>
                {t("store.install.done.openPr")}
              </ExternalButton>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 text-[13px]">
            <HealthBadge health={result.health} />
            <span className="inline-flex items-center gap-1">
              {result.revision ? (
                <>
                  <Sha id={result.revision} />
                  <span>{t("store.install.done.active")}</span>
                </>
              ) : (
                t("store.install.done.committedNoRev")
              )}
            </span>
          </div>
          {result.message ? (
            <p className="text-xs text-muted-foreground">{result.message}</p>
          ) : null}
          {result.command ? (
            <div>
              <p className="text-[13px]">{t("store.install.done.tryIt", { server: serverName })}</p>
              <CopyField value={`/${result.command}`} />
            </div>
          ) : (
            <p className="text-[13px] text-muted-foreground">{t("store.install.done.noCommand")}</p>
          )}
        </>
      )}
      <div className="flex flex-wrap gap-2">
        {!pr && bot ? (
          <Button asChild size="sm">
            <Link to={instancePath(serverKey, bot, result.instance)}>
              <SlidersHorizontal />
              {t("store.install.done.open")}
            </Link>
          </Button>
        ) : !pr ? (
          <Button asChild size="sm">
            <Link to={`/servers/${encodeURIComponent(serverKey)}/plugins`}>
              <Puzzle />
              {t("store.install.done.plugins")}
            </Link>
          </Button>
        ) : null}
        <Button asChild variant="outline" size="sm">
          <Link to={`/servers/${encodeURIComponent(serverKey)}/store`}>
            {t("store.install.done.back")}
          </Link>
        </Button>
      </div>
    </section>
  );
}

// ───────────────────────────────────────────────────────────── sources

type SourcesOutcome =
  | {
      kind: "written";
      action: "added" | "removed" | "tokenSet" | "tokenRemoved";
      name: string;
      result: WriteResult;
    }
  | { kind: "inUse"; name: string; message: string; items: string[] }
  | { kind: "forbidden"; message: string };

function Sources({ sources }: { sources: StoreSourceSummary[] }) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const [removing, setRemoving] = useState<string | null>(null);
  const [tokenFor, setTokenFor] = useState<StoreSourceSummary | null>(null);
  const [clearingToken, setClearingToken] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<SourcesOutcome | null>(null);

  const afterWrite = () =>
    Promise.all([
      cache.invalidate(keys.store()),
      cache.invalidate(["admin", "status"]),
      cache.invalidate(["admin", "history"]),
    ]);

  const failed = (e: unknown, fallback: string) => {
    if (e instanceof CicordApiError && e.code === "forbidden")
      setOutcome({ kind: "forbidden", message: e.message });
    else toastError(e, fallback);
  };

  const remove = async (name: string) => {
    setRemoving(null);
    setBusy(name);
    setOutcome(null);
    try {
      const result = await client.registries.remove(name);
      setOutcome({ kind: "written", action: "removed", name, result });
      toast.success(t("store.sources.removed", { name }));
      await afterWrite();
    } catch (e) {
      if (e instanceof CicordApiError && e.code === "in_use")
        setOutcome({ kind: "inUse", name, message: e.message, items: inUseItems(e) });
      else failed(e, t("store.sources.removeFailed"));
    } finally {
      setBusy(null);
    }
  };

  const removeToken = async (name: string) => {
    setClearingToken(null);
    setBusy(name);
    setOutcome(null);
    try {
      const result = await client.registries.removeToken(name);
      setOutcome({ kind: "written", action: "tokenRemoved", name, result });
      toast.success(t("store.sources.token.removed", { name }));
      await afterWrite();
    } catch (e) {
      failed(e, t("store.sources.token.removeFailed"));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="text-[15px] font-semibold">{t("store.sources.title")}</h2>
        <p className="mt-0.5 max-w-[70ch] text-[13px] text-muted-foreground">
          {t("store.sources.description")}
        </p>
      </div>
      {outcome ? <SourcesNote outcome={outcome} onDismiss={() => setOutcome(null)} /> : null}
      <Card>
        <ul className="divide-y" aria-label={t("store.sources.title")}>
          {sources.map((s) => (
            <li key={s.name} className="flex flex-col gap-3 px-4 py-3">
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-[13px] font-medium">{s.name}</span>
                    <TrustBadge trust={s.trust} />
                    <Badge>{t(`store.kind.${s.kind}`)}</Badge>
                    {s.private ? (
                      <Badge tone="violet" title={t("store.sources.privateTip")}>
                        <Lock aria-hidden="true" />
                        {t("store.sources.private")}
                      </Badge>
                    ) : null}
                    {s.signature === "verified" ? (
                      <Badge tone="success" title={t("store.sources.signedTip")}>
                        {t("store.sources.signed")}
                      </Badge>
                    ) : s.signature === "invalid" ? (
                      <Badge tone="danger" title={t("store.sources.badSignatureTip")}>
                        {t("store.sources.badSignature")}
                      </Badge>
                    ) : null}
                    {s.error ? (
                      <Badge tone="danger" title={s.error.message}>
                        {t("store.sources.error")}
                      </Badge>
                    ) : null}
                  </div>
                  {s.url ? (
                    <Mono className="break-all">{s.url}</Mono>
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      {t("store.sources.builtin")}
                    </span>
                  )}
                  {s.error ? <p className="text-xs text-danger">{s.error.message}</p> : null}
                </div>
                <span className="text-xs text-muted-foreground tabular">
                  {t("store.source.plugins", { count: s.pluginCount })}
                  {s.fetchedAt && !s.error ? (
                    <>
                      {" · "}
                      <Time iso={s.fetchedAt} />
                    </>
                  ) : null}
                </span>
                {s.kind === "git" && s.name !== "cicord" ? (
                  <div className="flex flex-wrap items-center gap-1">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setTokenFor(s)}
                      aria-label={t(
                        s.private
                          ? "store.sources.token.replaceLabel"
                          : "store.sources.token.setLabel",
                        { name: s.name },
                      )}
                    >
                      <KeyRound />
                      {s.private ? t("store.sources.token.replace") : t("store.sources.token.set")}
                    </Button>
                    {s.private ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setClearingToken(s.name)}
                        aria-label={t("store.sources.token.removeLabel", { name: s.name })}
                      >
                        {t("store.sources.token.remove")}
                      </Button>
                    ) : null}
                    <Button
                      variant="ghost"
                      size="sm"
                      loading={busy === s.name}
                      onClick={() => setRemoving(s.name)}
                      aria-label={t("store.sources.removeLabel", { name: s.name })}
                    >
                      {busy === s.name ? null : <Trash2 />}
                      {t("store.sources.remove")}
                    </Button>
                  </div>
                ) : (
                  <span className="sr-only">{t("store.sources.cannotRemove")}</span>
                )}
              </div>
              {s.credentials === "missing" ? (
                <Callout
                  tone="warning"
                  title={t("store.sources.token.missing.title")}
                  hint={t("store.sources.token.missing.hint")}
                  action={
                    <Button size="sm" variant="outline" onClick={() => setTokenFor(s)}>
                      <KeyRound />
                      {t("store.sources.token.set")}
                    </Button>
                  }
                />
              ) : null}
            </li>
          ))}
        </ul>
      </Card>

      <AddRegistry
        sources={sources}
        onWritten={(name, result) => {
          setOutcome({ kind: "written", action: "added", name, result });
          void afterWrite();
        }}
        onForbidden={(message) => setOutcome({ kind: "forbidden", message })}
      />

      <TokenDialog
        source={tokenFor}
        onClose={() => setTokenFor(null)}
        onSaved={(name, result) => {
          setTokenFor(null);
          setOutcome({ kind: "written", action: "tokenSet", name, result });
          void afterWrite();
        }}
        onForbidden={(message) => {
          setTokenFor(null);
          setOutcome({ kind: "forbidden", message });
        }}
      />

      <AlertDialog open={removing !== null} onOpenChange={(o) => !o && setRemoving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("store.sources.confirmRemove.title", { name: removing ?? "" })}
            </AlertDialogTitle>
            <AlertDialogDescription>{t("store.sources.confirmRemove.text")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={(e) => {
                e.preventDefault();
                if (removing) void remove(removing);
              }}
            >
              {t("store.sources.confirmRemove.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={clearingToken !== null} onOpenChange={(o) => !o && setClearingToken(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("store.sources.token.confirmRemove.title", { name: clearingToken ?? "" })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("store.sources.token.confirmRemove.text")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={(e) => {
                e.preventDefault();
                if (clearingToken) void removeToken(clearingToken);
              }}
            >
              {t("store.sources.token.confirmRemove.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

/** Sets or replaces the access token of a registry (never shown, only sent). */
function TokenDialog({
  source,
  onClose,
  onSaved,
  onForbidden,
}: {
  source: StoreSourceSummary | null;
  onClose: () => void;
  onSaved: (name: string, result: WriteResult) => void;
  onForbidden: (message: string) => void;
}) {
  const { t } = useTranslation();
  const { client } = useCicord();
  const ids = useId();
  const [token, setToken] = useState("");
  const [attempted, setAttempted] = useState(false);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<unknown>(null);
  const name = source?.name ?? "";
  const host = (() => {
    try {
      return source?.url ? new URL(source.url).host : "";
    } catch {
      return "";
    }
  })();

  const reset = () => {
    setToken("");
    setAttempted(false);
    setProblem(null);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setAttempted(true);
    if (!token.trim() || !source) return;
    setPending(true);
    setProblem(null);
    try {
      const result = await client.registries.setToken(source.name, token.trim());
      toast.success(t("store.sources.token.saved", { name: source.name }));
      reset();
      onSaved(source.name, result);
    } catch (err) {
      if (err instanceof CicordApiError && err.code === "forbidden") {
        reset();
        onForbidden(err.message);
      } else setProblem(err);
    } finally {
      setPending(false);
    }
  };

  const d = problem ? describeError(problem) : null;
  return (
    <Dialog
      open={source !== null}
      onOpenChange={(o) => {
        if (!o && !pending) {
          reset();
          onClose();
        }
      }}
    >
      <DialogContent>
        <form className="flex flex-col gap-4" onSubmit={(e) => void submit(e)} noValidate>
          <DialogHeader>
            <DialogTitle>
              {source?.private
                ? t("store.sources.token.replaceTitle", { name })
                : t("store.sources.token.setTitle", { name })}
            </DialogTitle>
            <DialogDescription>{t("store.sources.token.dialogText", { host })}</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`${ids}-token`}>{t("store.sources.token.label")}</Label>
            <Input
              id={`${ids}-token`}
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              aria-invalid={attempted && !token.trim() ? true : undefined}
              aria-describedby={`${ids}-token-hint`}
              className="font-mono"
              autoComplete="new-password"
              spellCheck={false}
            />
            <p
              id={`${ids}-token-hint`}
              className={cn(
                "text-xs",
                attempted && !token.trim() ? "text-danger" : "text-subtle-foreground",
              )}
            >
              {attempted && !token.trim()
                ? t("store.sources.token.required")
                : t("store.sources.token.hint", { host })}
            </p>
          </div>
          {d ? (
            <Callout
              tone="danger"
              title={t("store.sources.token.failed")}
              hint={d.hint}
              details={d.details}
            >
              {d.message}
            </Callout>
          ) : null}
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={pending}
              onClick={() => {
                reset();
                onClose();
              }}
            >
              {t("common.cancel")}
            </Button>
            <Button type="submit" loading={pending}>
              {pending ? t("store.sources.token.saving") : t("store.sources.token.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function WriteNote({ result }: { result: WriteResult }) {
  const { t } = useTranslation();
  if (result.mode === "pullRequest")
    return (
      <span className="inline-flex flex-wrap items-center gap-2">
        <Badge tone="info">{t("store.sources.result.pr")}</Badge>
        <span className="text-muted-foreground">{t("store.sources.result.prText")}</span>
        {result.pullRequestUrl ? (
          <a
            href={result.pullRequestUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 text-brand hover:underline"
          >
            {t("store.install.done.openPr")} <ExternalLink className="size-3" aria-hidden="true" />
          </a>
        ) : null}
      </span>
    );
  return (
    <span className="inline-flex items-center gap-2">
      <Badge tone="success">{t("store.sources.result.committed")}</Badge>
      {result.revision ? <Sha id={result.revision} icon /> : null}
    </span>
  );
}

function SourcesNote({ outcome, onDismiss }: { outcome: SourcesOutcome; onDismiss: () => void }) {
  const { t } = useTranslation();
  const dismiss = (
    <Button variant="ghost" size="sm" onClick={onDismiss}>
      {t("common.dismiss")}
    </Button>
  );
  if (outcome.kind === "forbidden")
    return (
      <Callout tone="danger" title={t("store.sources.forbidden.title")} action={dismiss}>
        <p>{t("store.sources.forbidden.text")}</p>
        {outcome.message ? (
          <p className="mt-1 text-xs text-muted-foreground">{outcome.message}</p>
        ) : null}
      </Callout>
    );
  if (outcome.kind === "inUse")
    return (
      <Callout
        tone="warning"
        title={t("store.sources.inUse.title", { name: outcome.name })}
        action={dismiss}
      >
        <p>{outcome.message}</p>
        {outcome.items.length ? (
          <>
            <p className="mt-1">{t("store.sources.inUse.text")}</p>
            <ul className="mt-1 list-disc pl-5" aria-label={t("store.sources.inUse.text")}>
              {outcome.items.map((i) => (
                <li key={i}>
                  <Mono className="text-foreground">{i}</Mono>
                </li>
              ))}
            </ul>
          </>
        ) : null}
      </Callout>
    );
  const title =
    outcome.action === "added"
      ? t("store.sources.add.added", { name: outcome.name })
      : outcome.action === "removed"
        ? t("store.sources.removed", { name: outcome.name })
        : outcome.action === "tokenSet"
          ? t("store.sources.token.saved", { name: outcome.name })
          : t("store.sources.token.removed", { name: outcome.name });
  // A replaced token changes no file: nothing was committed.
  const unchanged =
    outcome.action === "tokenSet" && outcome.result.mode === "commit" && !outcome.result.revision;
  return (
    <div
      className="flex flex-wrap items-center gap-3 rounded-lg border bg-surface-2 px-4 py-2.5 text-[13px]"
      role="status"
    >
      <span className="font-medium">{title}</span>
      {unchanged ? (
        <span className="text-muted-foreground">{t("store.sources.token.secretOnly")}</span>
      ) : (
        <WriteNote result={outcome.result} />
      )}
      <span className="ml-auto">{dismiss}</span>
    </div>
  );
}

function AddRegistry({
  sources,
  onWritten,
  onForbidden,
}: {
  sources: readonly RegistryRef[];
  onWritten: (name: string, result: WriteResult) => void;
  onForbidden: (message: string) => void;
}) {
  const { t } = useTranslation();
  const { client } = useCicord();
  const ids = useId();
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [touched, setTouched] = useState({ name: false, url: false });
  const [attempted, setAttempted] = useState(false);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<unknown>(null);
  const v = validateRegistry(name, url, sources);
  const showName = (attempted || touched.name) && v.name;
  const showUrl = (attempted || touched.url) && v.url;
  const host = (() => {
    try {
      return url.trim() ? new URL(url.trim()).host : "";
    } catch {
      return "";
    }
  })();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setAttempted(true);
    if (v.name || v.url) return;
    setPending(true);
    setProblem(null);
    const n = name.trim();
    try {
      const result = await client.registries.add({
        name: n,
        url: url.trim(),
        ...(token.trim() ? { token: token.trim() } : {}),
      });
      toast.success(t("store.sources.add.added", { name: n }));
      setName("");
      setUrl("");
      setToken("");
      setTouched({ name: false, url: false });
      setAttempted(false);
      onWritten(n, result);
    } catch (err) {
      if (err instanceof CicordApiError && err.code === "forbidden") onForbidden(err.message);
      else setProblem(err);
    } finally {
      setPending(false);
    }
  };

  const d = problem ? describeError(problem) : null;
  return (
    <Card>
      <form className="flex flex-col gap-4 p-4" onSubmit={(e) => void submit(e)} noValidate>
        <div>
          <h3 className="flex items-center gap-2 text-[14px] font-semibold">
            <Plus className="size-4" aria-hidden="true" />
            {t("store.sources.add.title")}
          </h3>
          <p className="mt-0.5 text-[13px] text-muted-foreground">{t("store.sources.add.text")}</p>
        </div>
        <Callout tone="warning" title={t("store.trust.unofficial")}>
          {t("store.sources.add.risk")}
        </Callout>
        <div className="grid gap-4 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)]">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`${ids}-name`}>{t("store.sources.add.name")}</Label>
            <Input
              id={`${ids}-name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              onBlur={() => setTouched((s) => ({ ...s, name: true }))}
              aria-invalid={showName ? true : undefined}
              aria-describedby={`${ids}-name-msg`}
              placeholder="acme"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              maxLength={32}
            />
            <p
              id={`${ids}-name-msg`}
              className={cn("text-xs", showName ? "text-danger" : "text-subtle-foreground")}
            >
              {showName && v.name
                ? t(`store.sources.add.errors.${v.name}`)
                : t("store.sources.add.nameHint")}
            </p>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`${ids}-url`}>{t("store.sources.add.url")}</Label>
            <Input
              id={`${ids}-url`}
              type="url"
              inputMode="url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onBlur={() => setTouched((s) => ({ ...s, url: true }))}
              aria-invalid={showUrl ? true : undefined}
              aria-describedby={`${ids}-url-msg`}
              placeholder={t("store.sources.add.urlPlaceholder")}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
            <p
              id={`${ids}-url-msg`}
              className={cn("min-h-4 text-xs", showUrl ? "text-danger" : "text-subtle-foreground")}
            >
              {showUrl && v.url ? t(`store.sources.add.errors.${v.url}`) : null}
            </p>
          </div>
        </div>
        <div className="flex max-w-md flex-col gap-1.5">
          <Label htmlFor={`${ids}-token`}>
            {t("store.sources.add.token")}{" "}
            <span className="font-normal text-subtle-foreground">
              {t("store.sources.add.optional")}
            </span>
          </Label>
          <Input
            id={`${ids}-token`}
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            aria-describedby={`${ids}-token-msg`}
            className="font-mono"
            autoComplete="new-password"
            spellCheck={false}
          />
          <p id={`${ids}-token-msg`} className="text-xs text-subtle-foreground">
            {host ? t("store.sources.token.hint", { host }) : t("store.sources.add.tokenHint")}
          </p>
        </div>
        {d ? (
          <Callout
            tone="danger"
            title={t("store.sources.add.failed")}
            hint={d.hint}
            details={d.details}
          >
            {d.message}
          </Callout>
        ) : null}
        <div className="flex justify-end">
          <Button type="submit" loading={pending}>
            {pending ? t("store.sources.add.adding") : t("store.sources.add.submit")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
