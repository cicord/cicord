/**
 * Update review (https://cicord.dev/docs/reference/http-api/store/, /servers/:key/store/:source/:plugin?update=1): what the
 * latest version of an installed plugin requests compared with the installed one, what it
 * changes for each instance, then the update of every instance in one write-back.
 * Review = update: the request carries the reviewed version, commit and integrity.
 */
import {
  CicordApiError,
  type ListDiff,
  type StoreUpdateRequest,
  type StoreUpdateResult,
  type StoreUpdateReview,
} from "@cicord/client";
import { useCicord } from "@cicord/react";
import {
  ArrowLeft,
  CheckCircle2,
  FileCode2,
  Globe,
  MinusCircle,
  Puzzle,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";
import { keys, useStoreUpdateReview } from "../api.ts";
import { EmptyState, Mono, Page, Sha } from "../components/common.tsx";
import { Callout, ErrorState } from "../components/errors.tsx";
import { HealthBadge } from "../components/status.tsx";
import { TrustBadge } from "../components/trust.tsx";
import { WriteWarnings } from "../components/update.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card } from "../components/ui/card.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import { useTranslation } from "../i18n/index.ts";
import { describeError } from "../lib/errors.ts";
import { cn } from "../lib/utils.ts";
import { Link } from "../router.tsx";
import { ExternalButton } from "../setup/kit.tsx";
import { instancePath, storePluginPath } from "../shell/nav.ts";
import {
  Block,
  CapabilityRow,
  ServerGrantConsent,
  serverGrants,
  capabilityLabel,
  DetailHeader,
  DEVELOPER_PORTAL,
  errorLink,
  intentLabel,
  Nothing,
  StickyBar,
} from "./Store.tsx";

export function StoreUpdate({
  source,
  name,
  serverKey,
}: {
  source: string;
  name: string;
  serverKey: string;
}) {
  const { t } = useTranslation();
  const { cache } = useCicord();
  const review = useStoreUpdateReview(source, name);
  const [done, setDone] = useState<StoreUpdateResult | null>(null);
  const r = review.data;
  const back = (
    <Link
      to={storePluginPath(serverKey, source, name)}
      className="inline-flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="size-3.5" aria-hidden="true" />
      {t("store.update.back", { name: r?.detail.displayName || name })}
    </Link>
  );
  const reload = async () => {
    await cache.invalidate(keys.storeUpdate(source, name));
    await review.refetch();
  };

  if (review.isLoading)
    return (
      <Page className="max-w-4xl">
        {back}
        <div className="flex flex-col gap-3" aria-busy="true" aria-label={t("common.loading")}>
          <Skeleton className="h-8 w-2/3" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="mt-4 h-40" />
        </div>
      </Page>
    );
  if (!r) {
    const code = review.error instanceof CicordApiError ? review.error.code : "";
    return (
      <Page className="max-w-4xl">
        {back}
        {code === "not_installed" || code === "not_found" ? (
          <Card>
            <EmptyState icon={<Puzzle />} title={t("store.update.notInstalled.title")}>
              {t("store.update.notInstalled.text", { plugin: `${source}/${name}` })}
            </EmptyState>
          </Card>
        ) : (
          <ErrorState
            error={review.error}
            title={t("store.update.loadFailed")}
            onRetry={() => void review.refetch()}
          />
        )}
      </Page>
    );
  }
  const d = r.detail;
  const display = d.displayName || d.name;
  return (
    <Page className="max-w-4xl">
      {back}
      <DetailHeader detail={d} />
      {done ? (
        <UpdateDone result={done} review={r} serverKey={serverKey} />
      ) : !r.available ? (
        <Card>
          <EmptyState
            icon={<CheckCircle2 />}
            title={t("store.update.upToDate.title", { name: display })}
          >
            {t("store.update.upToDate.text", { version: r.current.version })}
          </EmptyState>
        </Card>
      ) : (
        <UpdateForm
          key={`${d.version}:${d.commit ?? ""}:${d.integrity ?? ""}`}
          review={r}
          serverKey={serverKey}
          onDone={setDone}
          onChanged={() => void reload()}
        />
      )}
    </Page>
  );
}

/** Items of a diff list: added ones highlighted, removed ones struck through. */
function DiffList({
  diff,
  render,
  label,
}: {
  diff: ListDiff;
  render: (item: string) => ReactNode;
  label: string;
}) {
  const { t } = useTranslation();
  return (
    <ul className="flex flex-col gap-1" aria-label={label}>
      {diff.added.map((x) => (
        <li
          key={`+${x}`}
          className="flex items-center gap-2 rounded-md border border-warning/40 bg-warning-soft px-2 py-1 text-[13px]"
        >
          <Badge tone="warning">{t("store.update.new")}</Badge>
          {render(x)}
        </li>
      ))}
      {diff.removed.map((x) => (
        <li
          key={`-${x}`}
          className="flex items-center gap-2 px-2 text-[13px] text-muted-foreground"
        >
          <MinusCircle className="size-3.5 shrink-0" aria-hidden="true" />
          <span className="line-through">{render(x)}</span>
          <span className="sr-only">{t("store.update.gone")}</span>
        </li>
      ))}
    </ul>
  );
}

/** The permission diff between the installed version and the new one. */
function Changes({ review }: { review: StoreUpdateReview }) {
  const { t } = useTranslation();
  const { diff } = review;
  const privileged = new Set(review.detail.privilegedIntents);
  const nothing =
    !diff.capabilities.added.length &&
    !diff.capabilities.removed.length &&
    !diff.intents.added.length &&
    !diff.intents.removed.length &&
    !diff.network.added.length &&
    !diff.network.removed.length;
  const unchanged = review.detail.capabilities.filter((c) =>
    review.current.capabilities.includes(c),
  );
  return (
    <section aria-labelledby="update-changes" className="flex flex-col gap-5">
      <div>
        <h3 id="update-changes" className="text-[14px] font-semibold">
          {t("store.update.changes")}
        </h3>
        <p className="text-xs text-muted-foreground">
          {t("store.update.changesHint", { version: review.current.version })}
        </p>
      </div>
      {nothing ? <Nothing>{t("store.update.nothingNew")}</Nothing> : null}
      {diff.capabilities.added.length || diff.capabilities.removed.length ? (
        <Block
          title={
            diff.capabilities.added.length
              ? t("store.update.capabilitiesAdded")
              : t("store.update.capabilitiesRemoved")
          }
          hint={
            unchanged.length ? t("store.update.unchanged", { count: unchanged.length }) : undefined
          }
        >
          <DiffList
            diff={diff.capabilities}
            label={t("store.detail.capabilities")}
            render={(c) => (
              <span>
                {capabilityLabel(t, c)}{" "}
                <Mono className="text-[11px] text-subtle-foreground">{c}</Mono>
              </span>
            )}
          />
        </Block>
      ) : null}
      {diff.intents.added.length || diff.intents.removed.length ? (
        <Block
          title={
            diff.intents.added.length
              ? t("store.update.intentsAdded")
              : t("store.update.intentsRemoved")
          }
          hint={diff.privilegedIntents.added.length ? t("store.detail.privilegedHint") : undefined}
        >
          <DiffList
            diff={diff.intents}
            label={t("store.detail.intents")}
            render={(i) => (
              <span className="inline-flex items-center gap-1.5">
                <span className="font-mono text-[12px]">{i}</span>
                {privileged.has(i) && diff.intents.added.includes(i) ? (
                  <Badge tone="danger">
                    <TriangleAlert aria-hidden="true" />
                    {t("store.detail.privileged")}
                  </Badge>
                ) : null}
              </span>
            )}
          />
        </Block>
      ) : null}
      {diff.network.added.length || diff.network.removed.length ? (
        <Block
          title={
            diff.network.added.length
              ? t("store.update.networkAdded")
              : t("store.update.networkRemoved")
          }
          hint={diff.network.added.length ? t("store.detail.networkHint") : undefined}
        >
          <DiffList
            diff={diff.network}
            label={t("store.detail.network")}
            render={(h) => (
              <span className="inline-flex items-center gap-1 font-mono text-[12px]">
                <Globe className="size-3" aria-hidden="true" />
                {h}
              </span>
            )}
          />
        </Block>
      ) : null}
      {nothing && unchanged.length ? (
        <Block title={t("store.detail.capabilities")}>
          <ul className="flex flex-col gap-1">
            {unchanged.map((c) => (
              <CapabilityRow key={c} cap={c} />
            ))}
          </ul>
        </Block>
      ) : null}
    </section>
  );
}

/** What the update does to each instance (only those of servers where the viewer is admin). */
function Instances({ review, serverKey }: { review: StoreUpdateReview; serverKey: string }) {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="update-instances" className="flex flex-col gap-2">
      <div>
        <h3 id="update-instances" className="text-[14px] font-semibold">
          {t("store.update.instances")}
        </h3>
        <p className="text-xs text-muted-foreground">{t("store.update.instancesHint")}</p>
      </div>
      <ul className="flex flex-col gap-2">
        {review.instances.map((i) => {
          const invalid = i.diagnostics.length > 0;
          return (
            <li
              key={`${i.server}/${i.bot}/${i.instance}`}
              className={cn(
                "flex flex-col gap-1 rounded-md border px-3 py-2 text-[13px]",
                invalid && "border-danger/40 bg-danger-soft",
              )}
            >
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <Mono className="text-foreground">
                  {t("store.detail.instanceOn", {
                    bot: i.bot,
                    instance: i.instance,
                    server: i.server,
                  })}
                </Mono>
                {i.newRange ? (
                  <Badge tone="warning">
                    {t("store.update.rangeChange", { from: i.range || "*", to: i.newRange })}
                  </Badge>
                ) : null}
                {i.grantsAdded.length ? (
                  <Badge tone="warning">
                    {t("store.update.grantsAdded", { caps: i.grantsAdded.join(", ") })}
                  </Badge>
                ) : null}
                {i.grantsRemoved.length ? (
                  <Badge>
                    {t("store.update.grantsRemoved", { caps: i.grantsRemoved.join(", ") })}
                  </Badge>
                ) : null}
                {!invalid ? (
                  <span className="inline-flex items-center gap-1 text-xs text-success">
                    <CheckCircle2 className="size-3.5" aria-hidden="true" />
                    {t("store.update.configOk")}
                  </span>
                ) : null}
              </div>
              {invalid ? (
                <>
                  <ul className="list-disc pl-5 text-danger">
                    {i.diagnostics.map((dg) => (
                      <li key={`${dg.path ?? ""}${dg.message}`}>{dg.message}</li>
                    ))}
                  </ul>
                  {i.server === serverKey ? (
                    <Link
                      to={instancePath(serverKey, i.bot, i.instance)}
                      className="w-fit text-xs text-brand hover:underline"
                    >
                      {t("store.update.openInstance")}
                    </Link>
                  ) : null}
                </>
              ) : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function Files({ review }: { review: StoreUpdateReview }) {
  const { t } = useTranslation();
  const f = review.files;
  if (!f) return null;
  const groups = [
    { key: "changed", label: t("store.update.files.changed"), items: f.changed },
    { key: "added", label: t("store.update.files.added"), items: f.added },
    { key: "removed", label: t("store.update.files.removed"), items: f.removed },
  ].filter((g) => g.items.length);
  if (!groups.length) return null;
  return (
    <Block title={t("store.update.files.title", { name: review.detail.name })}>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
        {groups.map((g) => (
          <div key={g.key} className="contents">
            <dt className="text-muted-foreground">{g.label}</dt>
            <dd className="flex flex-wrap gap-x-3 gap-y-0.5">
              {g.items.map((p) => (
                <span key={p} className="inline-flex items-center gap-1">
                  <FileCode2 className="size-3 text-subtle-foreground" aria-hidden="true" />
                  <Mono className={cn(g.key === "removed" && "line-through")}>{p}</Mono>
                </span>
              ))}
            </dd>
          </div>
        ))}
      </dl>
    </Block>
  );
}

function UpdateForm({
  review,
  serverKey,
  onDone,
  onChanged,
}: {
  review: StoreUpdateReview;
  serverKey: string;
  onDone: (r: StoreUpdateResult) => void;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const d = review.detail;
  const name = d.displayName || d.name;
  const unofficial = d.trust === "unofficial";
  const blocked = review.missingAdmin.length > 0 || !review.configValid;
  const [confirmed, setConfirmed] = useState(false);
  const [serverConfirmed, setServerConfirmed] = useState(false);
  const privilegedGrants = serverGrants(review.diff.capabilities.added);
  const [force, setForce] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<unknown>(null);
  const problemRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (problem) problemRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [problem]);
  const ready =
    (!privilegedGrants.length || serverConfirmed) &&
    !blocked &&
    (!unofficial || confirmed) &&
    (!review.modified || force);
  const invalidCount = review.instances.filter((i) => i.diagnostics.length).length;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setAttempted(true);
    if (!ready) return;
    const request: StoreUpdateRequest = {
      version: d.version,
      ...(d.commit ? { commit: d.commit } : {}),
      ...(d.integrity ? { integrity: d.integrity } : {}),
      ...(unofficial ? { confirmUnofficial: true } : {}),
      ...(privilegedGrants.length ? { confirmPrivileged: true } : {}),
      ...(review.modified && force ? { force: true } : {}),
    };
    setPending(true);
    setProblem(null);
    try {
      const result = await client.store.update(d.source, d.name, request);
      onDone(result);
      if (result.mode === "pullRequest") toast.info(t("store.update.done.pr"));
      else toast.success(t("store.update.done.title", { name, version: result.version }));
      await Promise.all([
        cache.invalidate(keys.store()),
        cache.invalidate(keys.plugins(serverKey)),
        cache.invalidate(["admin", "status"]),
        cache.invalidate(["admin", "history"]),
        cache.invalidate(["admin", "server", serverKey, "audit"]),
      ]);
    } catch (err) {
      if (err instanceof CicordApiError && err.code === "changed") setConfirmed(false);
      setProblem(err);
    } finally {
      setPending(false);
    }
  };

  return (
    <form className="flex flex-col gap-5" onSubmit={(e) => void submit(e)} noValidate>
      <div>
        <h2 className="flex flex-wrap items-center gap-2 text-[15px] font-semibold">
          {t("store.update.title", { name })}
          <TrustBadge trust={d.trust} />
        </h2>
        <p className="text-xs text-muted-foreground">
          {t("store.update.lead", { current: review.current.version, latest: d.version })}
        </p>
      </div>
      {review.repoChanged ? (
        <Callout tone="danger" title={t("store.update.repoChanged.title")}>
          {t("store.update.repoChanged.text")}
        </Callout>
      ) : null}
      {review.restricted.length ? (
        <Callout
          tone="danger"
          title={t("store.update.restricted.title")}
          hint={t("store.update.restricted.text", { servers: review.missingAdmin.join(", ") })}
        >
          <ul className="list-disc pl-5">
            {review.restricted.map((r) => (
              <li key={r.server}>
                {t("store.update.restricted.server", { server: r.server, count: r.instances })}
              </li>
            ))}
          </ul>
        </Callout>
      ) : null}
      {invalidCount ? (
        <Callout
          tone="danger"
          title={t("store.update.configInvalid.title", { count: invalidCount, version: d.version })}
        >
          {t("store.update.configInvalid.hint")}
        </Callout>
      ) : null}
      <Card className="flex flex-col gap-6 px-4 py-4">
        <Changes review={review} />
        <Instances review={review} serverKey={serverKey} />
        <Files review={review} />
      </Card>
      {privilegedGrants.length > 0 && (
        <ServerGrantConsent
          grants={privilegedGrants}
          checked={serverConfirmed}
          change={setServerConfirmed}
        />
      )}
      {d.privilegedIntents.length && review.diff.privilegedIntents.added.length ? (
        <Callout
          tone="warning"
          title={t("store.install.intentsWarning", {
            intents: review.diff.privilegedIntents.added.map((i) => intentLabel(t, i)).join(", "),
          })}
          action={
            <ExternalButton href={DEVELOPER_PORTAL}>{t("store.detail.portal")}</ExternalButton>
          }
        />
      ) : null}
      {review.modified ? (
        <Checkbox
          tone="danger"
          checked={force}
          onChange={setForce}
          invalid={attempted && !force}
          label={t("store.update.modified.force", { name: d.name })}
        >
          <p className="font-medium">{t("store.update.modified.title")}</p>
          <p className="text-foreground/90">{t("store.update.modified.text", { name: d.name })}</p>
        </Checkbox>
      ) : null}
      {unofficial ? (
        <Checkbox
          tone="warning"
          checked={confirmed}
          onChange={setConfirmed}
          invalid={attempted && !confirmed}
          label={t("store.update.confirm", {
            publisher: d.publisher || d.source,
            name,
            source: d.source,
            version: d.version,
          })}
        >
          <NewlyGranted review={review} />
        </Checkbox>
      ) : null}
      {problem ? (
        <div ref={problemRef} className="scroll-mb-20">
          <UpdateProblem error={problem} onChanged={onChanged} />
        </div>
      ) : null}
      <StickyBar>
        {!blocked && unofficial && !confirmed ? (
          <span className="mr-auto text-xs text-muted-foreground">
            {t("store.update.confirmRequired")}
          </span>
        ) : !blocked && review.modified && !force ? (
          <span className="mr-auto text-xs text-muted-foreground">
            {t("store.update.forceRequired")}
          </span>
        ) : null}
        <Button type="submit" loading={pending} disabled={!ready}>
          {pending ? t("store.update.updating") : t("store.update.submit", { version: d.version })}
        </Button>
      </StickyBar>
    </form>
  );
}

/** What the new version is newly granted (restated in the unofficial confirmation). */
function NewlyGranted({ review }: { review: StoreUpdateReview }) {
  const { t } = useTranslation();
  const { diff } = review;
  const items = [
    ...diff.capabilities.added.map((c) =>
      t("store.update.confirmCapability", { capability: capabilityLabel(t, c) }),
    ),
    ...diff.intents.added.map((i) =>
      t("store.update.confirmIntent", { intent: intentLabel(t, i) }),
    ),
    ...(diff.network.added.length
      ? [t("store.update.confirmNetwork", { hosts: diff.network.added.join(", ") })]
      : []),
  ];
  return (
    <ul className="mt-1.5 list-disc pl-4 text-foreground/90">
      {items.length ? (
        items.map((x) => <li key={x}>{x}</li>)
      ) : (
        <li>{t("store.update.confirmNothingNew")}</li>
      )}
    </ul>
  );
}

function Checkbox({
  tone,
  checked,
  onChange,
  invalid,
  label,
  children,
}: {
  tone: "warning" | "danger";
  checked: boolean;
  onChange: (v: boolean) => void;
  invalid: boolean;
  label: string;
  children?: ReactNode;
}) {
  const id = useId();
  return (
    <div
      className={cn(
        "rounded-lg border px-4 py-3",
        tone === "warning"
          ? "border-warning/40 bg-warning-soft"
          : "border-danger/40 bg-danger-soft",
        invalid && (tone === "warning" ? "ring-2 ring-warning/40" : "ring-2 ring-danger/40"),
      )}
    >
      <div className="flex items-start gap-3">
        <input
          id={id}
          type="checkbox"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          aria-describedby={`${id}-more`}
          className="mt-0.5 size-4 shrink-0 accent-[var(--warning)]"
        />
        <div className="min-w-0 text-[13px]">
          <label htmlFor={id} className="font-medium">
            {label}
          </label>
          <div id={`${id}-more`}>{children}</div>
        </div>
      </div>
    </div>
  );
}

function UpdateProblem({ error, onChanged }: { error: unknown; onChanged: () => void }) {
  const { t } = useTranslation();
  const e = error instanceof CicordApiError ? error : null;
  const message = e?.message ?? "";
  switch (e?.code) {
    case "changed":
      return (
        <Callout
          tone="warning"
          title={t("store.update.changed.title")}
          hint={t("store.update.changed.hint")}
          details={message}
          action={
            <Button size="sm" variant="outline" onClick={onChanged}>
              <RefreshCw />
              {t("store.update.changed.reload")}
            </Button>
          }
        />
      );
    case "modified":
      return (
        <Callout
          tone="danger"
          title={t("store.update.modifiedError.title")}
          hint={e.hint ?? t("store.update.modifiedError.hint")}
          details={e.details}
        >
          {message}
        </Callout>
      );
    case "confirmation_required":
      return (
        <Callout tone="warning" title={t("store.install.confirmation.title")} hint={e.hint}>
          {message}
        </Callout>
      );
    case "intents":
      return (
        <Callout
          tone="danger"
          title={t("store.update.intents.title")}
          hint={e.hint ?? t("store.update.intents.hint")}
          action={
            <ExternalButton href={errorLink(error) ?? DEVELOPER_PORTAL}>
              {t("store.detail.portal")}
            </ExternalButton>
          }
        >
          {message}
        </Callout>
      );
    case "forbidden":
      return (
        <Callout
          tone="danger"
          title={t("store.update.forbidden.title")}
          hint={t("store.update.forbidden.hint")}
        >
          {message}
        </Callout>
      );
    case "up_to_date":
      return (
        <Callout tone="info" title={t("store.update.upToDateError")}>
          {message}
        </Callout>
      );
    case "invalid":
      return (
        <Callout tone="danger" title={t("store.update.rejected")} hint={e.hint} details={e.details}>
          <p>{message}</p>
          {e.diagnostics.length ? (
            <ul className="mt-1 list-disc pl-5">
              {e.diagnostics.map((dg) => (
                <li key={`${dg.file ?? ""}${dg.path ?? ""}${dg.message}`}>{dg.message}</li>
              ))}
            </ul>
          ) : null}
        </Callout>
      );
    default: {
      const dsc = describeError(error);
      return (
        <Callout
          tone="danger"
          title={t("store.update.failed")}
          hint={dsc.hint}
          details={dsc.details}
        >
          {dsc.message}
        </Callout>
      );
    }
  }
}

function UpdateDone({
  result,
  review,
  serverKey,
}: {
  result: StoreUpdateResult;
  review: StoreUpdateReview;
  serverKey: string;
}) {
  const { t } = useTranslation();
  const name = review.detail.displayName || review.detail.name;
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
        {pr
          ? t("store.update.done.pr")
          : t("store.update.done.title", { name, version: result.version })}
      </p>
      <WriteWarnings warnings={result.warnings} />
      {pr ? (
        <>
          <p className="text-[13px]">{t("store.update.done.prText")}</p>
          {result.pullRequestUrl ? (
            <div>
              <ExternalButton href={result.pullRequestUrl}>
                {t("store.update.done.openPr")}
              </ExternalButton>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <p className="inline-flex items-center gap-1 text-[13px]">
            {result.revision ? (
              <>
                <Sha id={result.revision} />
                <span>{t("store.update.done.active")}</span>
              </>
            ) : (
              t("store.update.done.committedNoRev")
            )}
          </p>
          {result.message ? (
            <p className="text-xs text-muted-foreground">{result.message}</p>
          ) : null}
          <ul className="flex flex-col gap-1">
            {result.instances.map((i) => (
              <li
                key={`${i.server}/${i.bot}/${i.instance}`}
                className="flex flex-wrap items-center gap-2 text-[13px]"
              >
                <Mono className="text-foreground">
                  {t("store.detail.instanceOn", {
                    bot: i.bot,
                    instance: i.instance,
                    server: i.server,
                  })}
                </Mono>
                <HealthBadge health={i.health} />
                {i.message ? (
                  <span className="text-xs text-muted-foreground">{i.message}</span>
                ) : null}
              </li>
            ))}
          </ul>
        </>
      )}
      <div className="flex flex-wrap gap-2">
        <Button asChild size="sm">
          <Link to={`/servers/${encodeURIComponent(serverKey)}/plugins`}>
            <Puzzle />
            {t("store.update.done.plugins")}
          </Link>
        </Button>
        <Button asChild variant="outline" size="sm">
          <Link to={`/servers/${encodeURIComponent(serverKey)}/store`}>
            {t("store.update.done.back")}
          </Link>
        </Button>
      </div>
    </section>
  );
}
