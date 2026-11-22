/**
 * Store (spec/api.md §11): browse the plugins of every source, review what a plugin requests,
 * install it on the current server, and manage the registries of codi.yaml.
 * Trust is shown everywhere a plugin or a source appears: `official` vs `unofficial`.
 */
import {
  CodiApiError,
  type InstallResult,
  type StoreDetail,
  type StoreInstallRequest,
  type StorePlugin,
  type StoreSource,
  type StoreTrust,
  type WriteResult,
} from "@codi/client";
import { useCodi } from "@codi/react";
import {
  ArrowLeft,
  CheckCircle2,
  ExternalLink,
  Globe,
  Plus,
  Puzzle,
  RefreshCw,
  Search,
  ShieldAlert,
  ShieldCheck,
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
import { Callout, ErrorState, HumanErrorCallout, toastError } from "../components/errors.tsx";
import { HealthBadge } from "../components/status.tsx";
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
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select.tsx";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "../components/ui/sheet.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../components/ui/tabs.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { describeError } from "../lib/errors.ts";
import { cn } from "../lib/utils.ts";
import { Link } from "../router.tsx";
import {
  applyDefaults,
  collectPointers,
  errorsByField,
  mapDiagnostics,
  mergeErrors,
  SchemaForm,
  validate,
  type JsonObject,
  type JsonSchema,
  type MappedDiagnostics,
} from "../schema-form/index.ts";
import { CopyField, ExternalButton } from "../setup/kit.tsx";
import { dyn, nonDefaults } from "../setup/steps-end.tsx";
import { RISK } from "./Plugins.tsx";

type T = ReturnType<typeof useTranslation>["t"];

const DEVELOPER_PORTAL = "https://discord.com/developers/applications";

/** Human label of a capability ("Send messages"), the raw name when unknown. */
const capabilityLabel = (t: T, cap: string) =>
  dyn(t)(`setup.capabilities.${cap.replaceAll(".", "_")}`, { defaultValue: cap });

/** "Message Content Intent" for MessageContent, the raw name when unknown. */
const intentLabel = (t: T, intent: string) =>
  dyn(t)(`setup.discord.toggle.${intent}`, { defaultValue: intent });

/** Case-insensitive match of every word of `query` on name, display name, description, keywords. */
export function matchesQuery(plugin: StorePlugin, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const text = [plugin.name, plugin.displayName, plugin.description, ...plugin.keywords]
    .join(" ")
    .toLowerCase();
  return words.every((w) => text.includes(w));
}

export const REGISTRY_NAME = /^[a-z0-9][a-z0-9-]{0,31}$/;

export type RegistryFieldError =
  | "nameRequired"
  | "namePattern"
  | "nameReserved"
  | "nameTaken"
  | "urlRequired"
  | "urlInvalid"
  | "urlHttps"
  | "urlTaken";

/** Client-side checks of `POST /registries` (the runtime checks them again). */
export function validateRegistry(
  name: string,
  url: string,
  sources: readonly StoreSource[],
): { name?: RegistryFieldError; url?: RegistryFieldError } {
  const out: { name?: RegistryFieldError; url?: RegistryFieldError } = {};
  const n = name.trim();
  if (!n) out.name = "nameRequired";
  else if (n === "codi") out.name = "nameReserved";
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
function errorLink(error: unknown): string | undefined {
  if (!(error instanceof CodiApiError) || !error.body || typeof error.body !== "object")
    return undefined;
  const e = (error.body as { error?: { link?: unknown } }).error;
  return e && typeof e.link === "string" && e.link ? e.link : undefined;
}

/** The instances listed by an `in_use` error (`details`: text lines or a list). */
export function inUseItems(error: unknown): string[] {
  if (!(error instanceof CodiApiError)) return [];
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

export function StorePage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  if (!ws.isAdmin)
    return (
      <Page>
        <PageHeader title={t("store.title")} description={t("store.description")} />
        <Callout tone="info" title={t("store.adminOnly.title")}>
          {t("store.adminOnly.text")}
        </Callout>
      </Page>
    );
  return <Store serverKey={serverKey} />;
}

interface PluginRef {
  source: string;
  name: string;
}

function Store({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const { client, cache } = useCodi();
  const sources = useStoreSources();
  const [tab, setTab] = useState("browse");
  const [open, setOpen] = useState<PluginRef | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const refresh = async () => {
    setRefreshing(true);
    try {
      const next = await client.store.refresh();
      cache.setData(keys.storeSources(), next);
      // Details are "the latest version": review them again after a refresh.
      await cache.invalidate(["admin", "store", "detail"]);
      toast.success(t("store.refreshed"));
    } catch (e) {
      toastError(e, t("store.refreshFailed"));
    } finally {
      setRefreshing(false);
    }
  };

  const total = sources.data?.reduce((n, s) => n + s.plugins.length, 0) ?? 0;
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
          {sources.isLoading ? (
            <BrowseSkeleton />
          ) : !sources.data ? (
            <ErrorState error={sources.error} onRetry={() => void sources.refetch()} />
          ) : (
            <Browse sources={sources.data} serverKey={serverKey} onOpen={setOpen} />
          )}
        </TabsContent>
        <TabsContent value="sources" className="pt-5">
          {sources.isLoading ? (
            <Card>
              <BrowseSkeleton rows />
            </Card>
          ) : !sources.data ? (
            <ErrorState error={sources.error} onRetry={() => void sources.refetch()} />
          ) : (
            <Sources sources={sources.data} />
          )}
        </TabsContent>
      </Tabs>
      <PluginSheet plugin={open} serverKey={serverKey} onClose={() => setOpen(null)} />
    </Page>
  );
}

function BrowseSkeleton({ rows }: { rows?: boolean }) {
  const { t } = useTranslation();
  if (rows)
    return (
      <div className="flex flex-col gap-3 p-4" aria-busy="true" aria-label={t("common.loading")}>
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-10" />
        ))}
      </div>
    );
  return (
    <div className="flex flex-col gap-8" aria-busy="true" aria-label={t("common.loading")}>
      {[0, 1].map((s) => (
        <div key={s} className="flex flex-col gap-3">
          <Skeleton className="h-5 w-48" />
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-32" />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── trust

export function TrustBadge({ trust, className }: { trust: StoreTrust; className?: string }) {
  const { t } = useTranslation();
  if (trust === "official")
    return (
      <Badge tone="brand" className={className} title={t("store.trust.officialTip")}>
        <ShieldCheck aria-hidden="true" />
        {t("store.trust.official")}
      </Badge>
    );
  return (
    <Badge
      tone="warning"
      className={cn("border-warning/40", className)}
      title={t("store.trust.unofficialTip")}
    >
      <ShieldAlert aria-hidden="true" />
      {t("store.trust.unofficial")}
    </Badge>
  );
}

// ───────────────────────────────────────────────────────────── browse

function Browse({
  sources,
  serverKey,
  onOpen,
}: {
  sources: StoreSource[];
  serverKey: string;
  onOpen: (p: PluginRef) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const searchId = useId();
  const q = query.trim();
  const filtered = useMemo(
    () => sources.map((s) => ({ ...s, plugins: s.plugins.filter((p) => matchesQuery(p, q)) })),
    [sources, q],
  );
  const matches = filtered.reduce((n, s) => n + s.plugins.length, 0);
  const total = sources.reduce((n, s) => n + s.plugins.length, 0);
  const visible = q ? filtered.filter((s) => s.plugins.length > 0) : filtered;

  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full max-w-md">
          <Search
            className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-subtle-foreground"
            aria-hidden="true"
          />
          <label htmlFor={searchId} className="sr-only">
            {t("store.search.label")}
          </label>
          <Input
            id={searchId}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape" && query) {
                e.preventDefault();
                setQuery("");
              }
            }}
            placeholder={t("store.search.placeholder")}
            className="pl-8"
          />
        </div>
        {q ? (
          <span className="text-xs text-muted-foreground" role="status">
            {t("store.search.results", { count: matches })}
          </span>
        ) : null}
      </div>

      {q && matches === 0 ? (
        <Card>
          <EmptyState
            icon={<Search />}
            title={t("store.search.noMatch", { query: q })}
            action={
              <Button variant="outline" size="sm" onClick={() => setQuery("")}>
                {t("store.search.clear")}
              </Button>
            }
          >
            {t("store.search.noMatchText")}
          </EmptyState>
        </Card>
      ) : (
        <>
          {!q && total === 0 && !sources.some((s) => s.error) ? (
            <Card>
              <EmptyState icon={<Puzzle />} title={t("store.empty.title")}>
                {t("store.empty.text")}
              </EmptyState>
            </Card>
          ) : null}
          {visible.map((s) => (
            <SourceSection key={s.name} source={s} serverKey={serverKey} onOpen={onOpen} />
          ))}
        </>
      )}
    </div>
  );
}

function SourceSection({
  source,
  serverKey,
  onOpen,
}: {
  source: StoreSource;
  serverKey: string;
  onOpen: (p: PluginRef) => void;
}) {
  const { t } = useTranslation();
  return (
    <section
      aria-label={t("store.source.label", { name: source.name })}
      className="flex flex-col gap-3"
    >
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b pb-2.5">
        <h2 className="text-[15px] font-semibold">{source.name}</h2>
        <TrustBadge trust={source.trust} />
        {source.url ? (
          <Mono className="max-w-full min-w-0 truncate">{source.url}</Mono>
        ) : (
          <span className="text-xs text-muted-foreground">{t("store.kind.bundled")}</span>
        )}
        <span className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
          {source.fetchedAt && !source.error ? (
            <span>
              {t("store.source.fetched")} <Time iso={source.fetchedAt} />
            </span>
          ) : null}
          <span className="tabular">
            {t("store.source.plugins", { count: source.plugins.length })}
          </span>
        </span>
      </header>
      {source.error ? (
        <HumanErrorCallout
          error={{ ...source.error, hint: source.error.hint ?? t("store.source.fetchHint") }}
          title={t("store.source.fetchFailed")}
        />
      ) : source.plugins.length === 0 ? (
        <p className="text-[13px] text-subtle-foreground">{t("store.source.none")}</p>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {source.plugins.map((p) => (
            <li key={p.name} className="flex">
              <PluginCard
                plugin={p}
                serverKey={serverKey}
                onOpen={() => onOpen({ source: p.source, name: p.name })}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function PluginCard({
  plugin,
  serverKey,
  onOpen,
}: {
  plugin: StorePlugin;
  serverKey: string;
  onOpen: () => void;
}) {
  const { t } = useTranslation();
  const name = plugin.displayName || plugin.name;
  const here = plugin.installed.some((i) => i.server === serverKey);
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={t("store.card.open", { name })}
      className={cn(
        "flex w-full flex-col gap-2 rounded-lg border bg-card p-4 text-left transition-colors outline-none hover:border-border-strong hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-ring",
        plugin.trust === "unofficial" && "border-warning/25",
      )}
    >
      <span className="flex w-full items-start gap-3">
        <PluginGlyph size={32} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[14px] font-semibold">{name}</span>
          <Mono className="block truncate text-[11px]">
            {plugin.source}/{plugin.name}
            {plugin.version ? `@${plugin.version}` : ""}
          </Mono>
        </span>
      </span>
      <span className="line-clamp-3 flex-1 text-[13px] text-muted-foreground">
        {plugin.description}
      </span>
      <span className="flex flex-wrap items-center gap-1.5">
        <TrustBadge trust={plugin.trust} />
        {plugin.verified ? (
          <Badge tone="success">
            <CheckCircle2 aria-hidden="true" />
            {t("store.card.verified")}
          </Badge>
        ) : null}
        {here ? (
          <Badge tone="info" title={t("store.card.installedTip")}>
            {t("store.card.installedHere")}
          </Badge>
        ) : null}
        {plugin.publisher ? (
          <span className="ml-auto truncate text-xs text-subtle-foreground">
            {t("store.card.by", { publisher: plugin.publisher })}
          </span>
        ) : null}
      </span>
    </button>
  );
}

// ───────────────────────────────────────────────────────────── detail & install

function PluginSheet({
  plugin,
  serverKey,
  onClose,
}: {
  plugin: PluginRef | null;
  serverKey: string;
  onClose: () => void;
}) {
  return (
    <Sheet open={plugin !== null} onOpenChange={(o) => !o && onClose()}>
      <SheetContent
        side="right"
        className="w-full overflow-y-auto bg-popover sm:w-[38rem] sm:max-w-[calc(100vw-2rem)]"
      >
        {plugin ? (
          <PluginDetail
            key={`${plugin.source}/${plugin.name}`}
            source={plugin.source}
            name={plugin.name}
            serverKey={serverKey}
            onClose={onClose}
          />
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

type Step = "review" | "install";

function PluginDetail({
  source,
  name,
  serverKey,
  onClose,
}: {
  source: string;
  name: string;
  serverKey: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { cache } = useCodi();
  const detail = useStoreDetail(source, name);
  const [step, setStep] = useState<Step>("review");
  const [done, setDone] = useState<InstallResult | null>(null);
  const [reloaded, setReloaded] = useState(false);
  const serverName = ws.server?.name ?? serverKey;
  const d = detail.data;

  const reviewAgain = async () => {
    await cache.invalidate(keys.storeDetail(source, name));
    await detail.refetch();
    setReloaded(true);
    setStep("review");
  };

  return (
    <div className="flex min-h-full flex-col">
      <div className="flex items-center justify-between gap-2 border-b px-5 py-3">
        {step === "install" && !done ? (
          <Button variant="ghost" size="sm" onClick={() => setStep("review")}>
            <ArrowLeft />
            {t("store.install.back")}
          </Button>
        ) : (
          <span className="text-xs text-muted-foreground">
            {t("store.detail.source")} <Mono className="text-foreground">{source}</Mono>
          </span>
        )}
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t("store.detail.close")}
        </Button>
      </div>
      <div className="flex flex-1 flex-col gap-5 px-5 py-5">
        {detail.isLoading ? (
          <>
            <SheetTitle className="sr-only">{t("store.detail.title", { name })}</SheetTitle>
            <SheetDescription className="sr-only">{t("common.loading")}</SheetDescription>
            <div className="flex flex-col gap-3" aria-busy="true" aria-label={t("common.loading")}>
              <Skeleton className="h-8 w-2/3" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-5/6" />
              <Skeleton className="mt-4 h-40" />
            </div>
          </>
        ) : !d ? (
          <>
            <SheetTitle className="text-base font-semibold">
              {t("store.detail.title", { name })}
            </SheetTitle>
            <SheetDescription className="sr-only">{t("store.detail.loadFailed")}</SheetDescription>
            <ErrorState
              error={detail.error}
              title={t("store.detail.loadFailed")}
              onRetry={() => void detail.refetch()}
            />
          </>
        ) : (
          <>
            <DetailHeader detail={d} />
            {done ? (
              <InstallDone
                result={done}
                detail={d}
                serverKey={serverKey}
                serverName={serverName}
                onClose={onClose}
              />
            ) : step === "review" ? (
              <>
                {reloaded ? (
                  <Callout tone="info" title={t("store.install.changed.title")}>
                    {t("store.install.changed.hint")}
                  </Callout>
                ) : null}
                <Review detail={d} serverKey={serverKey} />
                <div className="sticky bottom-0 -mx-5 mt-auto flex flex-wrap items-center justify-end gap-2 border-t bg-popover/95 px-5 py-3 backdrop-blur">
                  <Button onClick={() => setStep("install")}>
                    {d.installed.some((i) => i.server === serverKey)
                      ? t("store.detail.installAgain")
                      : t("store.detail.install", { server: serverName })}
                  </Button>
                </div>
              </>
            ) : (
              <InstallForm
                key={`${d.version}:${d.commit ?? ""}:${d.integrity ?? ""}`}
                detail={d}
                serverKey={serverKey}
                serverName={serverName}
                onDone={(r) => setDone(r)}
                onChanged={() => void reviewAgain()}
              />
            )}
          </>
        )}
      </div>
    </div>
  );
}

function DetailHeader({ detail }: { detail: StoreDetail }) {
  const { t } = useTranslation();
  const name = detail.displayName || detail.name;
  return (
    <header className="flex flex-col gap-3">
      <div className="flex items-start gap-3">
        <PluginGlyph size={40} />
        <div className="min-w-0 flex-1">
          <SheetTitle className="text-[17px] font-semibold tracking-[-0.01em]">{name}</SheetTitle>
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
      <SheetDescription className="text-[13px] text-foreground/90">
        {detail.description}
      </SheetDescription>
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

function Block({
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

function Nothing({ children }: { children: ReactNode }) {
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
      <Block title={t("store.detail.installed")}>
        {detail.installed.length ? (
          <ul className="flex flex-col gap-1">
            {detail.installed.map((i) => (
              <li
                key={`${i.server}/${i.bot}/${i.instance}`}
                className={cn(
                  "text-[13px]",
                  i.server === serverKey ? "text-foreground" : "text-muted-foreground",
                )}
              >
                <Mono className="text-inherit">
                  {t("store.detail.instanceOn", {
                    bot: i.bot,
                    instance: i.instance,
                    server: i.server,
                  })}
                </Mono>
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

function CapabilityRow({ cap }: { cap: string }) {
  const { t } = useTranslation();
  const risk = RISK[cap];
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
        {capabilityLabel(t, cap)} <Mono className="text-[11px] text-subtle-foreground">{cap}</Mono>
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
  onDone: (result: InstallResult) => void;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { client, cache } = useCodi();
  const status = useStatus();
  const resources = useResources(serverKey);
  const ids = useId();
  const schema = detail.schema as JsonSchema;
  const hasSettings = Object.keys(schema.properties ?? {}).length > 0;
  const bots = (status.data?.bots ?? []).filter((b) => b.servers.includes(serverKey));
  const multiBot = bots.length > 1;
  const unofficial = detail.trust === "unofficial";
  const name = detail.displayName || detail.name;

  const [bot, setBot] = useState("");
  const [instance, setInstance] = useState(detail.name);
  const [draft, setDraft] = useState<JsonObject>(() => applyDefaults(schema, {}) as JsonObject);
  const [confirmed, setConfirmed] = useState(false);
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
  const ready = (!unofficial || confirmed) && (!multiBot || !!bot);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setAttempted(true);
    if (!ready) return;
    if (clientErrors.length || instanceError) {
      toast.error(t("store.install.fixFirst"));
      return;
    }
    const registry = detail.source !== "codi";
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
    };
    setPending(true);
    setProblem(null);
    setServerErrors({ byField: {}, general: [] });
    try {
      const result = await client.store.install(serverKey, request);
      onDone(result);
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
      if (err instanceof CodiApiError && err.code === "invalid" && err.diagnostics.length)
        setServerErrors(mapDiagnostics(err.diagnostics, collectPointers(schema, draft)));
      if (err instanceof CodiApiError && err.code === "changed") setConfirmed(false);
      setProblem(err);
    } finally {
      setPending(false);
    }
  };

  const privileged = detail.privilegedIntents;
  return (
    <form className="flex flex-col gap-5" onSubmit={(e) => void submit(e)} noValidate>
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

      <div className="sticky bottom-0 -mx-5 flex flex-wrap items-center justify-end gap-2 border-t bg-popover/95 px-5 py-3 backdrop-blur">
        {unofficial && !confirmed ? (
          <span className="mr-auto text-xs text-muted-foreground">
            {t("store.install.confirmRequired")}
          </span>
        ) : null}
        <Button type="submit" loading={pending} disabled={!ready}>
          {pending ? t("store.install.installing") : t("store.install.submit")}
        </Button>
      </div>
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
  const code = error instanceof CodiApiError ? error.code : "";
  const message = error instanceof CodiApiError ? error.message : "";
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
      const hint = error instanceof CodiApiError ? error.hint : undefined;
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
  detail,
  serverKey,
  serverName,
  onClose,
}: {
  result: InstallResult;
  detail: StoreDetail;
  serverKey: string;
  serverName: string;
  onClose: () => void;
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
        {!pr ? (
          <Button asChild variant="outline" size="sm">
            <Link to={`/servers/${encodeURIComponent(serverKey)}/plugins`} onClick={onClose}>
              <Puzzle />
              {t("store.install.done.plugins")}
            </Link>
          </Button>
        ) : null}
        <Button size="sm" onClick={onClose}>
          {t("store.install.done.close")}
        </Button>
      </div>
    </section>
  );
}

// ───────────────────────────────────────────────────────────── sources

type SourcesOutcome =
  | { kind: "written"; action: "added" | "removed"; name: string; result: WriteResult }
  | { kind: "inUse"; name: string; message: string; items: string[] }
  | { kind: "forbidden"; message: string };

function Sources({ sources }: { sources: StoreSource[] }) {
  const { t } = useTranslation();
  const { client, cache } = useCodi();
  const [removing, setRemoving] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<SourcesOutcome | null>(null);

  const afterWrite = () =>
    Promise.all([
      cache.invalidate(keys.store()),
      cache.invalidate(["admin", "status"]),
      cache.invalidate(["admin", "history"]),
    ]);

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
      if (e instanceof CodiApiError && e.code === "in_use")
        setOutcome({ kind: "inUse", name, message: e.message, items: inUseItems(e) });
      else if (e instanceof CodiApiError && e.code === "forbidden")
        setOutcome({ kind: "forbidden", message: e.message });
      else toastError(e, t("store.sources.removeFailed"));
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
            <li key={s.name} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-[13px] font-medium">{s.name}</span>
                  <TrustBadge trust={s.trust} />
                  <Badge>{t(`store.kind.${s.kind}`)}</Badge>
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
                {t("store.source.plugins", { count: s.plugins.length })}
                {s.fetchedAt && !s.error ? (
                  <>
                    {" · "}
                    <Time iso={s.fetchedAt} />
                  </>
                ) : null}
              </span>
              {s.kind === "git" && s.name !== "codi" ? (
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
              ) : (
                <span className="sr-only">{t("store.sources.cannotRemove")}</span>
              )}
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
    </div>
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
  return (
    <div
      className="flex flex-wrap items-center gap-3 rounded-lg border bg-surface-2 px-4 py-2.5 text-[13px]"
      role="status"
    >
      <span className="font-medium">
        {outcome.action === "added"
          ? t("store.sources.add.added", { name: outcome.name })
          : t("store.sources.removed", { name: outcome.name })}
      </span>
      <WriteNote result={outcome.result} />
      <span className="ml-auto">{dismiss}</span>
    </div>
  );
}

function AddRegistry({
  sources,
  onWritten,
  onForbidden,
}: {
  sources: StoreSource[];
  onWritten: (name: string, result: WriteResult) => void;
  onForbidden: (message: string) => void;
}) {
  const { t } = useTranslation();
  const { client } = useCodi();
  const ids = useId();
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [touched, setTouched] = useState({ name: false, url: false });
  const [attempted, setAttempted] = useState(false);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<unknown>(null);
  const v = validateRegistry(name, url, sources);
  const showName = (attempted || touched.name) && v.name;
  const showUrl = (attempted || touched.url) && v.url;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setAttempted(true);
    if (v.name || v.url) return;
    setPending(true);
    setProblem(null);
    const n = name.trim();
    try {
      const result = await client.registries.add({ name: n, url: url.trim() });
      toast.success(t("store.sources.add.added", { name: n }));
      setName("");
      setUrl("");
      setTouched({ name: false, url: false });
      setAttempted(false);
      onWritten(n, result);
    } catch (err) {
      if (err instanceof CodiApiError && err.code === "forbidden") onForbidden(err.message);
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
