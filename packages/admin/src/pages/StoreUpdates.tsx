/**
 * Review of every available update (https://cicord.dev/docs/reference/http-api/store/,
 * /servers/:key/store/updates): one section per plugin with what its new version requests,
 * then the update of the selected plugins in one write-back (one commit or one pull request).
 * Each plugin keeps the checks of its own review; the request carries what was reviewed.
 */
import {
  CicordApiError,
  type StoreUpdateResult,
  type StoreUpdateReview,
  type StoreUpdatesItem,
  type StoreUpdatesReview,
} from "@cicord/client";
import { useCicord } from "@cicord/react";
import { ArrowLeft, CheckCircle2, ChevronDown, TriangleAlert } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { invalidateAfterStoreWrite, keys, useStoreUpdatesReview } from "../api.ts";
import { EmptyState, Mono, Page, PageHeader } from "../components/common.tsx";
import { Callout, ErrorState } from "../components/errors.tsx";
import { TrustBadge } from "../components/trust.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card } from "../components/ui/card.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { Link } from "../router.tsx";
import { storeUpdatePath } from "../shell/nav.ts";
import {
  capabilityLabel,
  intentLabel,
  ServerGrantConsent,
  serverGrants,
  StickyBar,
} from "./Store.tsx";
import {
  Changes,
  Checkbox,
  Files,
  Instances,
  NewlyGranted,
  PrivilegedIntentsWarning,
  UpdateOutcome,
  UpdateProblem,
} from "./StoreUpdate.tsx";

const refOf = (r: StoreUpdateReview) => `${r.source}/${r.plugin}`;

/** Why a plugin cannot be part of the change ("" when it can). */
function blockReason(r: StoreUpdateReview): "admin" | "config" | "" {
  if (r.missingAdmin.length) return "admin";
  if (!r.configValid) return "config";
  return "";
}

export function StoreUpdatesPage({ serverKey }: { serverKey: string }) {
  const ws = useWorkspace();
  const { t } = useTranslation();
  if (!ws.isAdmin)
    return (
      <Page>
        <PageHeader title={t("store.updates.title")} />
        <Callout tone="info" title={t("store.adminOnly.title")}>
          {t("store.adminOnly.text")}
        </Callout>
      </Page>
    );
  return <StoreUpdates serverKey={serverKey} />;
}

function StoreUpdates({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const { cache } = useCicord();
  const review = useStoreUpdatesReview();
  const [done, setDone] = useState<StoreUpdateResult | null>(null);
  const r = review.data;
  const reload = async () => {
    await cache.invalidate(keys.storeUpdates());
    await review.refetch();
  };
  const back = (
    <Link
      to={`/servers/${encodeURIComponent(serverKey)}/store`}
      className="inline-flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="size-3.5" aria-hidden="true" />
      {t("store.updates.back")}
    </Link>
  );
  const header = (
    <PageHeader title={t("store.updates.title")} description={t("store.updates.description")} />
  );

  if (review.isLoading)
    return (
      <Page className="max-w-4xl">
        {back}
        {header}
        <div className="flex flex-col gap-3" aria-busy="true" aria-label={t("common.loading")}>
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-16" />
          ))}
        </div>
      </Page>
    );
  if (!r)
    return (
      <Page className="max-w-4xl">
        {back}
        {header}
        <ErrorState
          error={review.error}
          title={t("store.updates.loadFailed")}
          onRetry={() => void review.refetch()}
        />
      </Page>
    );
  return (
    <Page className="max-w-4xl">
      {back}
      {header}
      {r.unavailable.length && !done ? (
        <Callout tone="warning" title={t("store.updates.unavailable.title")}>
          <p>{t("store.updates.unavailable.hint")}</p>
          <ul className="mt-1 list-disc pl-5">
            {r.unavailable.map((u) => (
              <li key={`${u.source}/${u.plugin}`}>
                <Mono className="text-foreground">
                  {u.source}/{u.plugin}
                </Mono>
                : {u.message}
              </li>
            ))}
          </ul>
        </Callout>
      ) : null}
      {done ? (
        <UpdatesDone result={done} serverKey={serverKey} />
      ) : !r.items.length ? (
        <Card>
          <EmptyState icon={<CheckCircle2 />} title={t("store.updates.empty.title")}>
            {t("store.updates.empty.text")}
          </EmptyState>
        </Card>
      ) : (
        <UpdatesForm
          key={r.items
            .map((i) => `${refOf(i)}@${i.detail.version}:${i.detail.commit ?? ""}`)
            .join()}
          review={r}
          serverKey={serverKey}
          onDone={setDone}
          onChanged={() => void reload()}
        />
      )}
    </Page>
  );
}

function UpdatesForm({
  review,
  serverKey,
  onDone,
  onChanged,
}: {
  review: StoreUpdatesReview;
  serverKey: string;
  onDone: (r: StoreUpdateResult) => void;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const items = review.items;
  const [selected, setSelected] = useState<Set<string>>(
    () => new Set(items.filter((i) => !blockReason(i)).map(refOf)),
  );
  const [approved, setApproved] = useState<Record<string, string>>({});
  const [forced, setForced] = useState<Record<string, boolean>>({});
  const [trusted, setTrusted] = useState(false);
  const [serverConfirmed, setServerConfirmed] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<unknown>(null);
  const problemRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (problem) problemRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [problem]);

  const chosen = useMemo(() => items.filter((i) => selected.has(refOf(i))), [items, selected]);
  const unofficial = chosen.filter((i) => i.detail.trust === "unofficial");
  const privilegedGrants = [
    ...new Set(chosen.flatMap((i) => serverGrants(i.diff.capabilities.added))),
  ];
  const privilegedIntents = [...new Set(chosen.flatMap((i) => i.diff.privilegedIntents.added))];
  const secretsMissing = chosen.some(
    (i) => i.secretReview && approved[refOf(i)] !== i.secretReview,
  );
  const forceMissing = chosen.some((i) => i.modified && !forced[refOf(i)]);
  const ready =
    chosen.length > 0 &&
    !secretsMissing &&
    !forceMissing &&
    (!unofficial.length || trusted) &&
    (!privilegedGrants.length || serverConfirmed);

  const toggle = (ref: string, on: boolean) =>
    setSelected((cur) => {
      const next = new Set(cur);
      if (on) next.add(ref);
      else next.delete(ref);
      return next;
    });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setAttempted(true);
    if (!ready) return;
    const updates: StoreUpdatesItem[] = chosen.map((i) => {
      const d = i.detail;
      const ref = refOf(i);
      return {
        source: i.source,
        plugin: i.plugin,
        version: d.version,
        ...(d.commit ? { commit: d.commit } : {}),
        ...(d.integrity ? { integrity: d.integrity } : {}),
        ...(i.secretReview ? { confirmSecretChanges: approved[ref] ?? "" } : {}),
        ...(i.modified && forced[ref] ? { force: true } : {}),
      };
    });
    setPending(true);
    setProblem(null);
    try {
      const result = await client.store.updateAll({
        updates,
        ...(unofficial.length ? { confirmUnofficial: true } : {}),
        ...(privilegedGrants.length ? { confirmPrivileged: true } : {}),
      });
      onDone(result);
      if (result.mode === "pullRequest") toast.info(t("store.update.done.pr"));
      else toast.success(t("store.updates.done.title", { count: result.updated.length }));
      await invalidateAfterStoreWrite(cache, serverKey);
    } catch (err) {
      if (err instanceof CicordApiError && err.code === "changed") setTrusted(false);
      setProblem(err);
    } finally {
      setPending(false);
    }
  };

  const hint = !chosen.length
    ? t("store.updates.noneSelected")
    : forceMissing
      ? t("store.update.forceRequired")
      : secretsMissing
        ? t("store.updates.secretRequired")
        : unofficial.length && !trusted
          ? t("store.update.confirmRequired")
          : "";

  return (
    <form className="flex flex-col gap-4" onSubmit={(e) => void submit(e)} noValidate>
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {t("store.updates.selected", { count: chosen.length, total: items.length })}
      </p>
      <ul className="flex flex-col gap-3">
        {items.map((i) => {
          const ref = refOf(i);
          return (
            <PluginUpdate
              key={ref}
              review={i}
              serverKey={serverKey}
              selected={selected.has(ref)}
              onSelect={(on) => toggle(ref, on)}
              approved={approved[ref] ?? ""}
              onApprove={(token) => setApproved((cur) => ({ ...cur, [ref]: token }))}
              forced={!!forced[ref]}
              onForce={(on) => setForced((cur) => ({ ...cur, [ref]: on }))}
              attempted={attempted}
            />
          );
        })}
      </ul>
      {privilegedGrants.length > 0 && (
        <ServerGrantConsent
          grants={privilegedGrants}
          checked={serverConfirmed}
          change={setServerConfirmed}
        />
      )}
      <PrivilegedIntentsWarning intents={privilegedIntents} />
      {unofficial.length ? (
        <Checkbox
          tone="warning"
          checked={trusted}
          onChange={setTrusted}
          invalid={attempted && !trusted}
          label={t("store.updates.confirmUnofficial")}
        >
          <ul className="mt-1.5 flex flex-col gap-1.5">
            {unofficial.map((i) => (
              <li key={refOf(i)}>
                <span className="font-medium">
                  {i.detail.displayName || i.detail.name} {i.detail.version}
                </span>{" "}
                <span className="text-muted-foreground">
                  (
                  {i.detail.publisher && i.detail.publisher !== i.source
                    ? `${i.detail.publisher}, ${i.source}`
                    : i.source}
                  )
                </span>
                <NewlyGranted review={i} />
              </li>
            ))}
          </ul>
        </Checkbox>
      ) : null}
      {problem ? (
        <div ref={problemRef} className="scroll-mb-20">
          <UpdateProblem error={problem} onChanged={onChanged} />
        </div>
      ) : null}
      <StickyBar>
        {hint ? <span className="mr-auto text-xs text-muted-foreground">{hint}</span> : null}
        <Button type="submit" loading={pending} disabled={!ready}>
          {pending
            ? t("store.update.updating")
            : t("store.updates.submit", { count: chosen.length })}
        </Button>
      </StickyBar>
    </form>
  );
}

/** One plugin of the change: include it, what it newly requests, and its own confirmations. */
function PluginUpdate({
  review,
  serverKey,
  selected,
  onSelect,
  approved,
  onApprove,
  forced,
  onForce,
  attempted,
}: {
  review: StoreUpdateReview;
  serverKey: string;
  selected: boolean;
  onSelect: (on: boolean) => void;
  approved: string;
  onApprove: (token: string) => void;
  forced: boolean;
  onForce: (on: boolean) => void;
  attempted: boolean;
}) {
  const { t } = useTranslation();
  const id = useId();
  const [open, setOpen] = useState(false);
  const d = review.detail;
  const name = d.displayName || d.name;
  const blocked = blockReason(review);
  const added = review.diff.capabilities.added;
  const intents = review.diff.privilegedIntents.added;
  const level = review.update?.level;
  return (
    <li>
      <Card
        className={cn(
          "flex flex-col gap-3 px-4 py-3",
          !selected && "opacity-75",
          blocked && "border-danger/40",
        )}
      >
        <div className="flex items-start gap-3">
          <input
            id={`${id}-include`}
            type="checkbox"
            checked={selected}
            disabled={!!blocked}
            onChange={(e) => onSelect(e.target.checked)}
            aria-describedby={`${id}-summary`}
            className="mt-1 size-4 shrink-0 accent-[var(--brand)]"
          />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <label
              htmlFor={`${id}-include`}
              className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[14px] font-semibold"
            >
              <span>
                <span className="sr-only">{t("store.updates.include", { name })}: </span>
                <span aria-hidden="true">{name}</span>
              </span>
              <Mono className="text-[12px] font-normal">
                {t("store.updates.version", {
                  current: review.current.version,
                  latest: d.version,
                })}
              </Mono>
              {level ? (
                <Badge tone={level === "major" ? "warning" : "info"}>
                  {t(`store.update.level.${level}`)}
                </Badge>
              ) : null}
              <TrustBadge trust={d.trust} />
            </label>
            <div id={`${id}-summary`} className="flex flex-col gap-0.5 text-xs">
              {blocked ? (
                <span className="inline-flex items-center gap-1 text-danger">
                  <TriangleAlert className="size-3.5" aria-hidden="true" />
                  {blocked === "admin"
                    ? t("store.updates.blocked.admin", { servers: review.missingAdmin.join(", ") })
                    : t("store.updates.blocked.config", { version: d.version })}{" "}
                  <Link
                    to={storeUpdatePath(serverKey, review.source, review.plugin)}
                    className="text-brand hover:underline"
                  >
                    {t("store.updates.openReview")}
                  </Link>
                </span>
              ) : null}
              {added.length ? (
                <span className="text-warning-foreground">
                  {t("store.updates.newPermissions", {
                    caps: added.map((c) => capabilityLabel(t, c)).join(", "),
                  })}
                </span>
              ) : (
                <span className="text-muted-foreground">{t("store.update.nothingNew")}</span>
              )}
              {intents.length ? (
                <span className="text-danger">
                  {t("store.updates.privilegedIntents", {
                    intents: intents.map((x) => intentLabel(t, x)).join(", "),
                  })}
                </span>
              ) : null}
              {review.repoChanged ? (
                <span className="text-danger">{t("store.update.repoChanged.title")}</span>
              ) : null}
            </div>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-expanded={open}
            aria-controls={`${id}-details`}
            onClick={() => setOpen((o) => !o)}
          >
            <ChevronDown
              className={cn("transition-transform", open && "rotate-180")}
              aria-hidden="true"
            />
            <span className="max-sm:sr-only">
              {open ? t("store.updates.details.hide") : t("store.updates.details.show")}
            </span>
          </Button>
        </div>
        {selected && review.secretReview ? (
          <Checkbox
            tone="warning"
            checked={approved === review.secretReview}
            onChange={(on) => onApprove(on ? (review.secretReview ?? "") : "")}
            invalid={attempted && approved !== review.secretReview}
            label={t("store.update.confirmSecretChanges")}
          >
            <p>{t("store.update.secretChangesHint")}</p>
          </Checkbox>
        ) : null}
        {selected && review.modified ? (
          <Checkbox
            tone="danger"
            checked={forced}
            onChange={onForce}
            invalid={attempted && !forced}
            label={t("store.update.modified.force", { name: d.name })}
          >
            <p className="text-foreground/90">
              {t("store.update.modified.text", { name: d.name })}
            </p>
          </Checkbox>
        ) : null}
        <div id={`${id}-details`} hidden={!open} className="flex flex-col gap-6 border-t pt-4">
          {open ? (
            <>
              <Changes review={review} />
              <Instances review={review} serverKey={serverKey} />
              <Files review={review} />
            </>
          ) : null}
        </div>
      </Card>
    </li>
  );
}

function UpdatesDone({ result, serverKey }: { result: StoreUpdateResult; serverKey: string }) {
  const { t } = useTranslation();
  return (
    <UpdateOutcome
      result={result}
      serverKey={serverKey}
      title={t("store.updates.done.title", { count: result.updated.length })}
    >
      <ul className="flex flex-col gap-0.5 text-[13px]">
        {result.updated.map((u) => (
          <li key={u.source}>
            <Mono className="text-foreground">{u.source}</Mono>{" "}
            <Mono>{t("store.updates.version", { current: u.from, latest: u.to })}</Mono>
          </li>
        ))}
      </ul>
    </UpdateOutcome>
  );
}
