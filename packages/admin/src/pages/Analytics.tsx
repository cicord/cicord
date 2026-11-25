/**
 * Analytics of a server: headline numbers against the previous period, then members,
 * messages, voice, engagement rankings and presence. One period selector (a preset or a
 * custom range, kept in the URL) scopes everything; while a new period loads, the previous
 * render stays at reduced opacity.
 */
import {
  CicordApiError,
  type AnalyticsLabel,
  type AnalyticsPeriod,
  type AnalyticsRanked,
  type AnalyticsSeries,
  type AnalyticsStatus,
  type AnalyticsStep,
  type AnalyticsSummary,
} from "@cicord/client";
import { useCicord } from "@cicord/react";
import {
  ArrowDownRight,
  ArrowUpRight,
  CalendarRange,
  ChartColumn,
  EyeOff,
  Minus,
  RefreshCw,
} from "lucide-react";
import { useEffect, useId, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { keys, useAnalyticsSeries, useAnalyticsSummary } from "../api.ts";
import {
  ChartCard,
  Heatmap,
  Legend,
  OTHER_COLOR,
  RankBars,
  SERIES_COLORS,
  Sparkline,
  TimeChart,
  ordinalColor,
  type RankItem,
  type TableView,
  type TimeSeries,
} from "../components/charts.tsx";
import { EmptyState, Mono, Page, PageHeader } from "../components/common.tsx";
import { Callout, ErrorState } from "../components/errors.tsx";
import { Card } from "../components/ui/card.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import { Popover, PopoverContent, PopoverTrigger } from "../components/ui/popover.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import { ToggleGroup, ToggleGroupItem } from "../components/ui/toggle-group.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import {
  ACCOUNT_AGES,
  KPI_POLARITY,
  MAX_RANGE_DAYS,
  PERIODS,
  averageOf,
  bucketsOf,
  deltaOf,
  dimsOf,
  fineSeriesParams,
  formatBucket,
  formatDay,
  formatDuration,
  formatNumber,
  formatPercent,
  formatRange,
  fromLocalInput,
  hourLabel,
  inProgressFrom,
  isCustomEmoji,
  labelOf,
  mainSeriesParams,
  missingFor,
  parseActivity,
  rangeProblem,
  splitLinks,
  sumOf,
  toLocalInput,
  toRfc3339,
  valuesOf,
  weekdays,
  type RangeProblem,
} from "../lib/analytics.ts";
import { cn } from "../lib/utils.ts";
import { navigate, useLocation } from "../router.tsx";

const DOCS = "https://cicord.dev/docs/self-hosting/analytics/";

/** i18n keys of the KPIs (metric names contain dots, i18next's separator). */
const KPI_KEYS = {
  "members.total": "membersTotal",
  "members.bots": "membersBots",
  "members.joined": "membersJoined",
  "members.left": "membersLeft",
  "members.net": "membersNet",
  "members.active": "membersActive",
  "messages.sent": "messagesSent",
  "voice.seconds": "voiceSeconds",
  "members.online": "membersOnline",
  "reactions.added": "reactionsAdded",
  "commands.used": "commandsUsed",
  "moderation.actions": "moderationActions",
} as const;

type KpiMetric = keyof typeof KPI_KEYS;

interface Data {
  summary: AnalyticsSummary;
  main: AnalyticsSeries;
  fine: AnalyticsSeries;
}

/** What the page shows: a preset period, or a custom range (RFC 3339 UTC). */
type Range = { period: AnalyticsPeriod } | { from: string; to: string };

const isTime = (v: string | null): v is string => v !== null && !Number.isNaN(Date.parse(v));

/** The range in the URL: `?period=7d`, `?from=…&to=…`, or nothing (30 days). */
function useRange(): [Range, (r: Range) => void] {
  const { path, search } = useLocation();
  const from = search.get("from");
  const to = search.get("to");
  const raw = search.get("period");
  const range: Range =
    isTime(from) && isTime(to)
      ? { from, to }
      : { period: PERIODS.includes(raw as AnalyticsPeriod) ? (raw as AnalyticsPeriod) : "30d" };
  const set = (r: Range) => {
    // RFC 3339 times need no escaping in a query string (digits, "-", ":", "T", "Z").
    const query =
      "from" in r ? `from=${r.from}&to=${r.to}` : r.period === "30d" ? "" : `period=${r.period}`;
    navigate(query ? `${path}?${query}` : path, { replace: true, keepScroll: true });
  };
  return [range, set];
}

export function AnalyticsPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { cache } = useCicord();
  const [range, setRange] = useRange();
  const summary = useAnalyticsSummary(serverKey, range);
  const mainParams = useMemo(
    () => (summary.data ? mainSeriesParams(summary.data) : null),
    [summary.data],
  );
  const fineParams = useMemo(
    () => (summary.data ? fineSeriesParams(summary.data) : null),
    [summary.data],
  );
  const main = useAnalyticsSeries(serverKey, mainParams);
  const fine = useAnalyticsSeries(serverKey, fineParams);

  // The last complete render: kept (dimmed) while another period loads.
  const [shown, setShown] = useState<Data | null>(null);
  useEffect(() => {
    if (summary.data && main.data && fine.data)
      setShown({ summary: summary.data, main: main.data, fine: fine.data });
  }, [summary.data, main.data, fine.data]);
  const current =
    summary.data && main.data && fine.data
      ? { summary: summary.data, main: main.data, fine: fine.data }
      : null;
  const data = current ?? shown;
  const stale = !current || summary.isFetching || main.isFetching || fine.isFetching;
  const error = summary.error ?? main.error ?? fine.error;
  const refresh = () => void cache.invalidate(keys.analytics(serverKey));

  const header = (
    <PageHeader
      title={t("analytics.title")}
      description={t("analytics.description", { server: ws.serverName ?? serverKey })}
      meta={data ? <CollectionMeta status={data.summary.status} /> : undefined}
      actions={
        <>
          <ToggleGroup
            type="single"
            value={"period" in range ? range.period : ""}
            onValueChange={(v) => v && setRange({ period: v as AnalyticsPeriod })}
            aria-label={t("analytics.period.label")}
          >
            {PERIODS.map((p) => (
              <ToggleGroupItem key={p} value={p} aria-label={t(`analytics.period.long.${p}`)}>
                {t(`analytics.period.short.${p}`)}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <CustomRange
            range={range}
            shown={data?.summary}
            error={"from" in range ? summary.error : null}
            onApply={setRange}
          />
          <Button
            variant="outline"
            size="icon-sm"
            onClick={refresh}
            aria-label={t("common.refresh")}
            disabled={stale && !!current}
          >
            <RefreshCw className={cn(stale && current && "animate-spin")} />
          </Button>
        </>
      }
    />
  );

  if (!data) {
    if (error instanceof CicordApiError && error.code === "analytics_disabled")
      return (
        <Page>
          {header}
          <Card>
            <EmptyState icon={<ChartColumn />} title={t("analytics.noStore.title")}>
              {t("analytics.noStore.text")}
            </EmptyState>
          </Card>
        </Page>
      );
    return (
      <Page>
        {header}
        {error ? <ErrorState error={error} onRetry={refresh} /> : <DashboardSkeleton />}
      </Page>
    );
  }

  const status = data.summary.status;
  if (!status.since)
    return (
      <Page>
        {header}
        <MissingCallout status={status} />
        <Card>
          {status.enabled ? (
            <EmptyState icon={<ChartColumn />} title={t("analytics.empty.title")}>
              {t("analytics.empty.text")}
            </EmptyState>
          ) : (
            <EmptyState icon={<EyeOff />} title={t("analytics.disabled.title")}>
              {t("analytics.disabled.text")} <Mono>spec.analytics.enabled: true</Mono>
            </EmptyState>
          )}
        </Card>
      </Page>
    );

  return (
    <Page>
      {header}
      {error ? <ErrorState error={error} onRetry={refresh} /> : null}
      {!status.enabled ? (
        <Callout tone="info" title={t("analytics.paused.title")}>
          {t("analytics.paused.text")} <Mono>spec.analytics.enabled: true</Mono>
        </Callout>
      ) : null}
      <MissingCallout status={status} />
      <div
        className={cn("flex flex-col gap-8 transition-opacity duration-200", stale && "opacity-60")}
        aria-busy={stale}
      >
        <KpiGrid summary={data.summary} />
        <MembersSection data={data} />
        <MessagesSection data={data} />
        <VoiceSection data={data} />
        <EngagementSection summary={data.summary} />
        <PresenceSection data={data} />
      </div>
    </Page>
  );
}

// ───────────────────────────────────────────────────────────── custom range

const HOUR_MS = 3600_000;

/**
 * The custom range button and its popover: two local date-time inputs, checked like the
 * runtime checks them, sent as RFC 3339 UTC. The runtime's 400 for the current range is
 * shown in the form too.
 */
function CustomRange({
  range,
  shown,
  error,
  onApply,
}: {
  range: Range;
  /** The summary on screen (the form starts from its window). */
  shown: AnalyticsSummary | undefined;
  /** Error of the current custom range's summary. */
  error: unknown;
  onApply: (r: Range) => void;
}) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const custom = "from" in range;
  // The window the runtime shows: rounded outward to UTC hours.
  const label = custom
    ? formatRange(
        new Date(Math.floor(Date.parse(range.from) / HOUR_MS) * HOUR_MS).toISOString(),
        new Date(Math.ceil(Date.parse(range.to) / HOUR_MS) * HOUR_MS).toISOString(),
        i18n.language,
      )
    : null;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className={cn("h-[30px] text-xs", !custom && "text-muted-foreground")}
          aria-label={label ? t("analytics.custom.active", { range: label }) : undefined}
        >
          <CalendarRange className="size-3.5" />
          <span className="max-w-44 truncate sm:max-w-none">
            {label ?? t("analytics.custom.button")}
          </span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        className="w-[min(20rem,calc(100vw-2rem))]"
        aria-label={t("analytics.custom.title")}
      >
        <RangeForm
          initial={
            custom
              ? range
              : shown
                ? // A preset ends tomorrow 00:00 UTC: start from now instead.
                  { from: shown.from, to: new Date(Math.min(Date.parse(shown.to), Date.now())) }
                : null
          }
          serverError={
            error instanceof CicordApiError && error.status === 400 ? error.message : null
          }
          onCancel={() => setOpen(false)}
          onApply={(r) => {
            setOpen(false);
            onApply(r);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

function RangeForm({
  initial,
  serverError,
  onApply,
  onCancel,
}: {
  initial: { from: string | Date; to: string | Date } | null;
  serverError: string | null;
  onApply: (r: { from: string; to: string }) => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const [from, setFrom] = useState(() => toLocalInput(initial?.from ?? Date.now() - 7 * 86400_000));
  const [to, setTo] = useState(() => toLocalInput(initial?.to ?? Date.now()));
  const [problem, setProblem] = useState<RangeProblem | null>(null);
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const a = fromLocalInput(from);
    const b = fromLocalInput(to);
    const p = rangeProblem(a, b);
    setProblem(p);
    if (!p) onApply({ from: toRfc3339(a!), to: toRfc3339(b!) });
  };

  const message = problem
    ? t(`analytics.custom.errors.${problem}`, { count: MAX_RANGE_DAYS })
    : serverError
      ? t("analytics.custom.rejected", { message: serverError })
      : null;
  const fromInvalid =
    problem === "future" ||
    problem === "tooLong" ||
    (problem === "missing" && !fromLocalInput(from));
  const toInvalid =
    problem === "order" || problem === "tooLong" || (problem === "missing" && !fromLocalInput(to));
  const describedBy = `${id}-zone${message ? ` ${id}-error` : ""}`;

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3">
      <h3 className="text-sm font-semibold">{t("analytics.custom.title")}</h3>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${id}-from`}>{t("analytics.custom.from")}</Label>
        <Input
          id={`${id}-from`}
          type="datetime-local"
          value={from}
          onChange={(e) => setFrom(e.target.value)}
          aria-invalid={fromInvalid || undefined}
          aria-describedby={describedBy}
          required
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${id}-to`}>{t("analytics.custom.to")}</Label>
        <Input
          id={`${id}-to`}
          type="datetime-local"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          aria-invalid={toInvalid || undefined}
          aria-describedby={describedBy}
          required
        />
      </div>
      <p id={`${id}-zone`} className="text-xs text-muted-foreground">
        {t("analytics.custom.zone", { zone })}
      </p>
      {message ? (
        <p id={`${id}-error`} role="alert" className="text-xs text-danger">
          {message}
        </p>
      ) : null}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          {t("analytics.custom.cancel")}
        </Button>
        <Button type="submit" size="sm">
          {t("analytics.custom.apply")}
        </Button>
      </div>
    </form>
  );
}

// ───────────────────────────────────────────────────────────── status

/** Text with its http(s) URLs as links (new tab). */
function LinkText({ text }: { text: string }) {
  return (
    <>
      {splitLinks(text).map((part, i) =>
        part.href ? (
          <a
            key={i}
            href={part.href}
            target="_blank"
            rel="noopener noreferrer"
            className="text-brand underline-offset-4 [overflow-wrap:anywhere] hover:underline"
          >
            {part.text}
          </a>
        ) : (
          part.text
        ),
      )}
    </>
  );
}

function CollectionMeta({ status }: { status: AnalyticsStatus }) {
  const { t, i18n } = useTranslation();
  const parts = [
    status.since
      ? t("analytics.meta.since", { date: formatDay(status.since, i18n.language) })
      : null,
    t("analytics.meta.retention", { count: status.retentionDays }),
    t("analytics.meta.utc"),
  ].filter(Boolean);
  return <p className="text-xs text-muted-foreground">{parts.join(" · ")}</p>;
}

function MissingCallout({ status }: { status: AnalyticsStatus }) {
  const { t } = useTranslation();
  if (status.missing.length === 0) return null;
  return (
    <Callout tone="warning" title={t("analytics.missing.title", { count: status.missing.length })}>
      <ul className="mt-1 flex flex-col gap-3">
        {status.missing.map((m, i) => (
          <li key={i} className="flex flex-col gap-1">
            <p className="flex flex-wrap gap-1">
              {m.metrics.map((metric) => (
                <code
                  key={metric}
                  className="rounded-[4px] border bg-background/60 px-1 font-mono text-[11px] text-muted-foreground"
                >
                  {metric}
                </code>
              ))}
            </p>
            <p>
              <LinkText text={m.reason} />
            </p>
            <p className="text-muted-foreground">
              <span className="font-medium text-foreground">{t("analytics.missing.fix")}</span>{" "}
              <LinkText text={m.fix} />
            </p>
          </li>
        ))}
      </ul>
    </Callout>
  );
}

function DashboardSkeleton() {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-6" aria-busy="true" aria-label={t("common.loading")}>
      <Card className="grid grid-cols-2 gap-px overflow-hidden sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6">
        {Array.from({ length: 12 }, (_, i) => (
          <div key={i} className="flex flex-col gap-2 p-4">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="h-6 w-16" />
            <Skeleton className="h-7 w-full" />
          </div>
        ))}
      </Card>
      <div className="grid gap-6 lg:grid-cols-2">
        <Skeleton className="h-64" />
        <Skeleton className="h-64" />
      </div>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── KPIs

const STEP_SECONDS = { hour: 3600, day: 86400 } as const;

function KpiGrid({ summary }: { summary: AnalyticsSummary }) {
  const { t } = useTranslation();
  // Older runtimes send no step: their sparklines are per day.
  const sparkStep = STEP_SECONDS[summary.step] ?? 86400;
  return (
    <section aria-label={t("analytics.kpi.label")}>
      <Card className="overflow-hidden">
        <dl className="-mr-px -mb-px grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6">
          {summary.kpis.map((kpi) => (
            <KpiTile
              key={kpi.metric}
              metric={kpi.metric as KpiMetric}
              value={kpi.value}
              previous={kpi.previous}
              spark={kpi.spark.map((p) => p[1])}
              sparkPartial={(kpi.spark.at(-1)?.[0] ?? 0) + sparkStep > Date.now() / 1000}
              period={summary.period}
              missing={missingFor(summary.status, kpi.metric) !== undefined}
            />
          ))}
        </dl>
      </Card>
    </section>
  );
}

function formatKpi(metric: string, v: number, lng: string, compact = false) {
  return metric === "voice.seconds"
    ? formatDuration(v, lng, compact)
    : formatNumber(v, lng, compact);
}

function KpiTile({
  metric,
  value,
  previous,
  spark,
  sparkPartial,
  period,
  missing,
}: {
  metric: KpiMetric;
  value: number;
  previous: number | null;
  spark: number[];
  sparkPartial: boolean;
  period: AnalyticsSummary["period"];
  missing: boolean;
}) {
  const { t, i18n } = useTranslation();
  const lng = i18n.language;
  const name = KPI_KEYS[metric] ?? metric;
  const delta = deltaOf(value, previous, KPI_POLARITY[metric] ?? "up");
  const vs = t(`analytics.period.previous.${period}`);
  return (
    <div className="flex min-w-0 flex-col gap-1 border-r border-b px-4 pt-3 pb-2">
      <dt className="truncate text-xs text-muted-foreground">
        {t(`analytics.kpi.${name}`, { defaultValue: metric })}
      </dt>
      <dd className="flex min-w-0 flex-col gap-1">
        {missing ? (
          <>
            <span className="text-xl font-semibold text-muted-foreground">—</span>
            <span className="text-xs text-muted-foreground">{t("analytics.notCollected")}</span>
          </>
        ) : (
          <>
            <span className="truncate text-xl font-semibold tracking-[-0.01em]">
              {metric === "members.net" && value > 0 ? "+" : ""}
              {formatKpi(metric, value, lng)}
            </span>
            <DeltaText
              delta={delta}
              vs={vs}
              previous={previous === null ? null : formatKpi(metric, previous, lng)}
            />
            <Sparkline values={spark} partial={sparkPartial} className="mt-1" />
          </>
        )}
      </dd>
    </div>
  );
}

function DeltaText({
  delta,
  vs,
  previous,
}: {
  delta: ReturnType<typeof deltaOf>;
  vs: string;
  previous: string | null;
}) {
  const { t, i18n } = useTranslation();
  if (delta.kind === "none")
    return (
      <span className="flex items-center gap-1 text-xs text-muted-foreground">
        <Minus className="size-3" aria-hidden="true" />
        {t("analytics.delta.none")}
      </span>
    );
  if (delta.kind === "new")
    return (
      <span className="text-xs text-muted-foreground">{t("analytics.delta.new", { vs })}</span>
    );
  const Icon = delta.ratio > 0 ? ArrowUpRight : delta.ratio < 0 ? ArrowDownRight : Minus;
  return (
    <span className="flex min-w-0 items-center gap-1 text-xs">
      <span
        className={cn(
          "inline-flex shrink-0 items-center gap-0.5 font-medium tabular",
          delta.tone === "good" && "text-success",
          delta.tone === "bad" && "text-danger",
          delta.tone === "neutral" && "text-muted-foreground",
        )}
      >
        <Icon className="size-3.5" aria-hidden="true" />
        {formatPercent(delta.ratio, i18n.language, true)}
      </span>
      <span className="truncate text-muted-foreground" title={t("analytics.delta.label", { vs })}>
        {t("analytics.delta.vs", { value: previous })}
        <span className="sr-only">, {t("analytics.delta.label", { vs })}</span>
      </span>
    </span>
  );
}

// ───────────────────────────────────────────────────────────── shared helpers

function Section({ title, children }: { title: string; children: ReactNode }) {
  const id = `analytics-${title.toLowerCase().replace(/\W+/g, "-")}`;
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <h2 id={id} className="text-[15px] font-semibold tracking-[-0.01em]">
        {title}
      </h2>
      {children}
    </section>
  );
}

/** Table view of time series: one row per bucket, one column per series. */
function timeTable(
  caption: string,
  buckets: number[],
  step: AnalyticsStep,
  series: TimeSeries[],
  format: (v: number) => string,
  text: { lng: string; date: string; inProgress: string },
): TableView {
  const partial = inProgressFrom(buckets, step);
  return {
    caption,
    columns: [text.date, ...series.map((s) => s.label)],
    rows: buckets.map((b, i) => ({
      key: b,
      cells: [
        `${formatBucket(b, step, text.lng)}${partial >= 0 && i >= partial ? ` (${text.inProgress})` : ""}`,
        ...series.map((s) => (s.values[i] === null ? "—" : format(s.values[i]!))),
      ],
    })),
  };
}

/** Props of a TimeChart in a ChartCard: the series, the table view and the legend. */
function useTimeCard(
  title: string,
  series: AnalyticsSeries,
  lines: TimeSeries[],
  format: (v: number) => string,
  mark: "bar" | "line",
) {
  const { t, i18n } = useTranslation();
  const buckets = bucketsOf(series);
  return {
    buckets,
    step: series.step,
    table: timeTable(title, buckets, series.step, lines, format, {
      lng: i18n.language,
      date: t("analytics.chart.date"),
      inProgress: t("analytics.chart.inProgress"),
    }),
    legend: lines.map((s) => ({ label: s.label, color: s.color, mark })),
  };
}

const serie = (
  series: AnalyticsSeries,
  buckets: number[],
  metric: string,
  label: string,
  color: string,
  dim = "",
): TimeSeries => ({
  key: `${metric}:${dim}`,
  label,
  color,
  values: valuesOf(series, buckets, metric, dim),
});

/** Display name of a dimension: "#channel", "(deleted)" suffix, "Other". */
function useDimLabel(labels: Record<string, AnalyticsLabel>): (dim: string) => string {
  const { t } = useTranslation();
  return (dim) => {
    if (dim === "other") return t("analytics.other");
    const l = labels[dim];
    const name = labelOf(labels, dim);
    const shown = l?.kind === "channel" || l?.kind === "thread" ? `#${name}` : name;
    return l?.deleted ? `${shown} (${t("analytics.deleted")})` : shown;
  };
}

// ───────────────────────────────────────────────────────────── members

function MembersSection({ data }: { data: Data }) {
  const { t, i18n } = useTranslation();
  const lng = i18n.language;
  const { main, fine } = data;
  const b = bucketsOf(main);
  const num = (v: number) => formatNumber(v, lng);
  const axis = (v: number) => formatNumber(v, lng, true);

  const [shownMembers, setShownMembers] = useState<"humans" | "bots">("humans");
  const total = serie(main, b, "members.total", t("analytics.members.total"), "var(--chart-1)");
  const bots = serie(main, b, "members.bots", t("analytics.members.bots"), "var(--chart-2)");
  // Bots are a handful next to thousands of humans: one at a time, each on its own axis.
  const hasBots = main.series.some((s) => s.metric === "members.bots");
  const members = hasBots && shownMembers === "bots" ? bots : total;
  const joined = serie(main, b, "members.joined", t("analytics.members.joined"), "var(--chart-1)");
  const leftValues = valuesOf(main, b, "members.left");
  const left: TimeSeries = {
    key: "left",
    label: t("analytics.members.left"),
    color: "var(--chart-2)",
    values: leftValues,
    negative: true,
  };
  const net: TimeSeries = {
    key: "net",
    label: t("analytics.members.net"),
    color: "var(--foreground)",
    values: joined.values.map((v, i) =>
      v === null && leftValues[i] === null ? null : (v ?? 0) - (leftValues[i] ?? 0),
    ),
  };
  const flow = useTimeCard(t("analytics.members.flow"), main, [joined, left, net], num, "bar");
  const totalCard = useTimeCard(
    t("analytics.members.title"),
    main,
    hasBots ? [total, bots] : [total],
    num,
    "line",
  );

  const fb = bucketsOf(fine);
  const online = serie(fine, fb, "members.online", t("analytics.members.online"), "var(--chart-1)");
  const onlineCard = useTimeCard(t("analytics.members.onlineTitle"), fine, [online], num, "line");

  const active = [
    serie(main, b, "members.active", t("analytics.active.all"), "var(--chart-1)"),
    serie(main, b, "members.active.chat", t("analytics.active.chat"), "var(--chart-2)"),
    serie(main, b, "members.active.voice", t("analytics.active.voice"), "var(--chart-3)"),
  ];
  const activeCard = useTimeCard(t("analytics.active.title"), main, active, num, "line");

  return (
    <Section title={t("analytics.sections.members")}>
      <ChartCard
        title={t("analytics.members.title")}
        description={t("analytics.members.description")}
        actions={
          hasBots ? (
            <ToggleGroup
              type="single"
              value={shownMembers}
              onValueChange={(v) => v && setShownMembers(v as "humans" | "bots")}
              aria-label={t("analytics.members.show")}
            >
              <ToggleGroupItem value="humans">{t("analytics.members.humans")}</ToggleGroupItem>
              <ToggleGroupItem value="bots">{t("analytics.members.bots")}</ToggleGroupItem>
            </ToggleGroup>
          ) : undefined
        }
        table={{
          ...totalCard.table,
          columns: [...totalCard.table.columns, joined.label, left.label, net.label],
          rows: totalCard.table.rows.map((r, i) => ({
            ...r,
            cells: [
              ...r.cells,
              ...[joined, left, net].map((s) => (s.values[i] === null ? "—" : num(s.values[i]!))),
            ],
          })),
        }}
      >
        <TimeChart
          kind="line"
          buckets={b}
          step={main.step}
          series={[members]}
          format={num}
          formatAxis={axis}
          minZero={false}
          axisWidth={48}
          label={`${t("analytics.members.title")}: ${members.label}`}
          height={170}
        />
        <div className="flex flex-col gap-2 border-t pt-3">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h3 className="text-xs font-medium text-muted-foreground">
              {t("analytics.members.flow")}
            </h3>
            <Legend
              items={flow.legend.map((l, i) => (i === 2 ? { ...l, mark: "line" as const } : l))}
            />
          </div>
          <TimeChart
            kind="bars"
            buckets={b}
            step={main.step}
            series={[joined, left]}
            overlay={[net]}
            format={(v) => num(Math.abs(v))}
            formatAxis={(v) => axis(Math.abs(v))}
            label={t("analytics.members.flow")}
            height={140}
            axisWidth={48}
          />
        </div>
      </ChartCard>
      <div className="grid gap-6 lg:grid-cols-2">
        <ChartCard
          title={t("analytics.members.onlineTitle")}
          description={t("analytics.members.onlineDescription")}
          table={onlineCard.table}
        >
          <TimeChart
            kind="line"
            buckets={fb}
            step={fine.step}
            series={[online]}
            format={num}
            formatAxis={axis}
            label={t("analytics.members.onlineTitle")}
          />
        </ChartCard>
        <ChartCard
          title={t("analytics.active.title")}
          description={t("analytics.active.description")}
          legend={activeCard.legend}
          table={activeCard.table}
        >
          <TimeChart
            kind="line"
            buckets={b}
            step={main.step}
            series={active}
            format={num}
            formatAxis={axis}
            label={t("analytics.active.title")}
          />
        </ChartCard>
      </div>
    </Section>
  );
}

// ───────────────────────────────────────────────────────────── messages

function MessagesSection({ data }: { data: Data }) {
  const { t, i18n } = useTranslation();
  const lng = i18n.language;
  const { main, summary } = data;
  const b = bucketsOf(main);
  const num = (v: number) => formatNumber(v, lng);
  const dimLabel = useDimLabel({ ...summary.labels, ...main.labels });
  const dims = dimsOf(main, "messages.sent");
  const stack = dims.map((dim, i) =>
    serie(
      main,
      b,
      "messages.sent",
      dimLabel(dim),
      dim === "other" ? OTHER_COLOR : SERIES_COLORS[i]!,
      dim,
    ),
  );
  const byChannel = useTimeCard(t("analytics.messages.title"), main, stack, num, "bar");
  const days = weekdays(lng);
  const longDays = weekdays(lng, "long");
  const hours = Array.from({ length: 24 }, (_, h) => hourLabel(h, lng));
  const heat = summary.heatmap.length === 7 ? summary.heatmap : days.map(() => hours.map(() => 0));

  const detailsPartial = inProgressFrom(b, main.step) === b.length - 1;
  const details = (
    [
      ["messages.replies", "replies"],
      ["messages.attachments", "attachments"],
      ["threads.created", "threads"],
      ["polls.created", "polls"],
    ] as const
  ).map(([metric, key]) => {
    const values = valuesOf(main, b, metric);
    return { metric, key, total: sumOf(values), values: values.map((v) => v ?? 0) };
  });

  return (
    <Section title={t("analytics.sections.messages")}>
      <ChartCard
        title={t("analytics.messages.title")}
        description={t("analytics.messages.description")}
        legend={byChannel.legend}
        table={byChannel.table}
      >
        <TimeChart
          kind="bars"
          buckets={b}
          step={main.step}
          series={stack}
          format={num}
          formatAxis={(v) => formatNumber(v, lng, true)}
          label={t("analytics.messages.title")}
          height={220}
        />
      </ChartCard>
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <ChartCard
          title={t("analytics.heatmap.title")}
          description={t("analytics.heatmap.description")}
          table={{
            caption: t("analytics.heatmap.title"),
            columns: [t("analytics.heatmap.day"), ...hours],
            rows: heat.map((row, i) => ({ key: i, cells: [longDays[i]!, ...row.map(num)] })),
          }}
        >
          <Heatmap
            grid={heat}
            rows={days}
            columns={hours}
            format={(v) => t("analytics.heatmap.value", { value: num(v) })}
            label={t("analytics.heatmap.title")}
            scaleLabel={[t("analytics.heatmap.fewer"), t("analytics.heatmap.more")]}
          />
        </ChartCard>
        <Card aria-label={t("analytics.messages.details")} className="flex flex-col">
          <dl className="grid flex-1 grid-cols-2 divide-border [&>*:nth-child(-n+2)]:border-b [&>*:nth-child(odd)]:border-r">
            {details.map((d) => (
              <div key={d.metric} className="flex min-w-0 flex-col gap-1 p-4">
                <dt className="text-xs text-muted-foreground">
                  {t(`analytics.messages.${d.key}`)}
                </dt>
                <dd className="flex flex-col gap-1">
                  <span className="text-lg font-semibold">{num(d.total)}</span>
                  <Sparkline values={d.values} partial={detailsPartial} />
                </dd>
              </div>
            ))}
          </dl>
        </Card>
      </div>
    </Section>
  );
}

// ───────────────────────────────────────────────────────────── voice

function VoiceSection({ data }: { data: Data }) {
  const { t, i18n } = useTranslation();
  const lng = i18n.language;
  const { main, fine, summary } = data;
  const b = bucketsOf(main);
  const fb = bucketsOf(fine);
  const hrs = (v: number) => formatDuration(v, lng);
  const num = (v: number) => formatNumber(v, lng);
  const totalSeconds = sumOf(valuesOf(main, b, "voice.seconds"));
  // Charted in hours so the axis ticks are round hours.
  const hours = serie(main, b, "voice.seconds", t("analytics.voice.hours"), "var(--chart-1)");
  const seconds = { ...hours, values: hours.values.map((v) => (v === null ? null : v / 3600)) };
  const inHours = (v: number) => formatDuration(v * 3600, lng);
  const sessions = serie(
    main,
    b,
    "voice.sessions",
    t("analytics.voice.sessions"),
    "var(--chart-1)",
  );
  const peak = serie(fine, fb, "voice.peak", t("analytics.voice.peak"), "var(--chart-1)");
  const streaming = sumOf(valuesOf(main, b, "voice.streaming"));
  const stage = sumOf(valuesOf(main, b, "stage.seconds"));
  const timeCard = useTimeCard(t("analytics.voice.title"), main, [seconds], inHours, "bar");
  const sessionsCard = useTimeCard(t("analytics.voice.sessions"), main, [sessions], num, "bar");
  const peakCard = useTimeCard(t("analytics.voice.peak"), fine, [peak], num, "line");
  const dimLabel = useDimLabel(summary.labels);
  const byChannel = rankItems(summary.rankings["voice.seconds"] ?? [], dimLabel, hrs, lng);

  return (
    <Section title={t("analytics.sections.voice")}>
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <ChartCard
          title={t("analytics.voice.title")}
          description={[
            t("analytics.voice.total", { value: hrs(totalSeconds) }),
            streaming > 0 ? t("analytics.voice.streaming", { value: hrs(streaming) }) : null,
            stage > 0 ? t("analytics.voice.stage", { value: hrs(stage) }) : null,
          ]
            .filter(Boolean)
            .join(" · ")}
          table={timeCard.table}
        >
          <TimeChart
            kind="bars"
            buckets={b}
            step={main.step}
            series={[seconds]}
            format={inHours}
            formatAxis={(v) => formatDuration(v * 3600, lng, true)}
            integer={false}
            label={t("analytics.voice.title")}
          />
        </ChartCard>
        <ChartCard
          title={t("analytics.voice.byChannel")}
          description={t("analytics.voice.byChannelDescription")}
        >
          <RankBars
            items={byChannel}
            label={t("analytics.voice.byChannel")}
            empty={t("analytics.rankings.empty")}
          />
        </ChartCard>
      </div>
      <div className="grid gap-6 lg:grid-cols-2">
        <ChartCard
          title={t("analytics.voice.peak")}
          description={t("analytics.voice.peakDescription")}
          table={peakCard.table}
        >
          <TimeChart
            kind="line"
            buckets={fb}
            step={fine.step}
            series={[peak]}
            format={num}
            label={t("analytics.voice.peak")}
            height={160}
          />
        </ChartCard>
        <ChartCard
          title={t("analytics.voice.sessions")}
          description={t("analytics.voice.sessionsDescription", {
            value: num(sumOf(sessions.values)),
          })}
          table={sessionsCard.table}
        >
          <TimeChart
            kind="bars"
            buckets={b}
            step={main.step}
            series={[sessions]}
            format={num}
            label={t("analytics.voice.sessions")}
            height={160}
          />
        </ChartCard>
      </div>
    </Section>
  );
}

// ───────────────────────────────────────────────────────────── engagement

function rankItems(
  ranked: AnalyticsRanked[],
  name: (dim: string) => string,
  format: (v: number) => string,
  lng: string,
  label?: (dim: string) => ReactNode,
): RankItem[] {
  return ranked.map((r) => {
    const delta = deltaOf(r.value, r.previous, "neutral");
    return {
      key: r.dim,
      name: name(r.dim),
      label: label ? label(r.dim) : <span className="truncate">{name(r.dim)}</span>,
      value: r.value,
      display: format(r.value),
      ...(delta.kind === "change" ? { extra: formatPercent(delta.ratio, lng, true) } : {}),
    };
  });
}

function EmojiLabel({ dim, labels }: { dim: string; labels: Record<string, AnalyticsLabel> }) {
  const label = labels[dim];
  if (!isCustomEmoji(dim)) return <span className="text-base leading-none">{dim}</span>;
  const name = label?.name ?? dim.replace(/^a:/, "").split(":")[0]!;
  return (
    <span className="flex min-w-0 items-center gap-2">
      {label?.imageUrl ? (
        <img
          src={label.imageUrl}
          alt=""
          className="size-5 shrink-0 rounded-[4px] object-contain"
          loading="lazy"
        />
      ) : null}
      <span className="truncate font-mono text-xs">:{name}:</span>
    </span>
  );
}

function EngagementSection({ summary }: { summary: AnalyticsSummary }) {
  const { t, i18n } = useTranslation();
  const lng = i18n.language;
  const labels = summary.labels;
  const num = (v: number) => formatNumber(v, lng);
  const r = (metric: string) => summary.rankings[metric] ?? [];
  const plain = (dim: string) => labelOf(labels, dim);
  const dimLabel = useDimLabel(labels);

  const ages = r("members.joined.age");
  const ageTotal = ages.reduce((a, x) => a + x.value, 0) || 1;
  const ageItems: RankItem[] = (ages.length ? ACCOUNT_AGES : []).map((age, k) => {
    const value = ages.find((x) => x.dim === age)?.value ?? 0;
    return {
      key: age,
      name: t(`analytics.ages.${ageKey(age)}`),
      label: <span>{t(`analytics.ages.${ageKey(age)}`)}</span>,
      value,
      display: num(value),
      extra: formatPercent(value / ageTotal, lng),
      color: ordinalColor(k, ACCOUNT_AGES.length),
    };
  });

  const cards: { metric: string; title: string; description?: string; items: RankItem[] }[] = [
    {
      metric: "messages.sent",
      title: t("analytics.rankings.channels"),
      items: rankItems(r("messages.sent"), dimLabel, num, lng),
    },
    {
      metric: "reactions.added",
      title: t("analytics.rankings.emojis"),
      items: rankItems(
        r("reactions.added"),
        (d) => labelOf(labels, d),
        num,
        lng,
        (d) => <EmojiLabel dim={d} labels={labels} />,
      ),
    },
    {
      metric: "invites.uses",
      title: t("analytics.rankings.invites"),
      description: t("analytics.rankings.invitesDescription"),
      items: rankItems(r("invites.uses"), plain, num, lng, (d) => (
        <Mono className="truncate text-foreground">{d}</Mono>
      )),
    },
    {
      metric: "commands.used",
      title: t("analytics.rankings.commands"),
      items: rankItems(
        r("commands.used"),
        (d) => `/${d}`,
        num,
        lng,
        (d) => <Mono className="truncate text-foreground">/{d}</Mono>,
      ),
    },
    {
      metric: "members.roles.added",
      title: t("analytics.rankings.roles"),
      items: rankItems(r("members.roles.added"), dimLabel, num, lng, (d) => (
        <span className="flex min-w-0 items-center gap-2">
          <span
            aria-hidden="true"
            className="size-2.5 shrink-0 rounded-full border border-border-strong"
            style={{ background: labels[d]?.color ?? "transparent" }}
          />
          <span className="truncate">{dimLabel(d)}</span>
        </span>
      )),
    },
    {
      metric: "members.joined.age",
      title: t("analytics.rankings.ages"),
      description: t("analytics.rankings.agesDescription"),
      items: ageItems,
    },
    {
      metric: "moderation.actions",
      title: t("analytics.rankings.moderation"),
      items: rankItems(
        r("moderation.actions"),
        (d) => t(`analytics.actions.${d}`, { defaultValue: d.replaceAll("_", " ") }),
        num,
        lng,
      ),
    },
    {
      metric: "automod.triggers",
      title: t("analytics.rankings.automod"),
      items: rankItems(r("automod.triggers"), plain, num, lng),
    },
  ];

  return (
    <Section title={t("analytics.sections.engagement")}>
      <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-3">
        {cards.map((c) => {
          const missing = missingFor(summary.status, c.metric);
          return (
            <ChartCard key={c.metric} title={c.title} description={c.description}>
              {missing ? (
                <NotCollected reason={missing.reason} />
              ) : (
                <RankBars items={c.items} label={c.title} empty={t("analytics.rankings.empty")} />
              )}
            </ChartCard>
          );
        })}
      </div>
    </Section>
  );
}

function ageKey(age: string): "day" | "week" | "month" | "year" | "older" {
  return (
    ({ "<1d": "day", "<7d": "week", "<30d": "month", "<1y": "year" } as const)[age as "<1d"] ??
    "older"
  );
}

function NotCollected({ reason }: { reason: string }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1 py-4 text-center text-[13px]">
      <p className="font-medium text-muted-foreground">{t("analytics.notCollected")}</p>
      <p className="text-xs text-muted-foreground">
        <LinkText text={reason} />
      </p>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── presence

function PresenceSection({ data }: { data: Data }) {
  const { t, i18n } = useTranslation();
  const lng = i18n.language;
  const { summary, main } = data;
  const status = summary.status;
  if (!status.presence) {
    const missing = missingFor(status, "presence.status");
    return (
      <Section title={t("analytics.sections.presence")}>
        <Card>
          <EmptyState icon={<EyeOff />} title={t("analytics.presence.off.title")}>
            <p>
              <LinkText text={missing?.reason ?? t("analytics.presence.off.text")} />
            </p>
            <p className="mt-2">
              <LinkText text={missing?.fix ?? t("analytics.presence.off.fix")} />{" "}
              <a
                href={DOCS}
                target="_blank"
                rel="noreferrer"
                className="text-brand underline-offset-4 hover:underline"
              >
                {t("analytics.presence.off.docs")}
              </a>
            </p>
          </EmptyState>
        </Card>
      </Section>
    );
  }
  const b = bucketsOf(main);
  const num = (v: number) => formatNumber(v, lng);
  const shares = (metric: string, label: (dim: string) => string): RankItem[] => {
    const avgs = dimsOf(main, metric)
      .filter((d) => d !== "other")
      .map((dim) => ({ dim, avg: averageOf(valuesOf(main, b, metric, dim)) ?? 0 }));
    const total = avgs.reduce((a, x) => a + x.avg, 0) || 1;
    return avgs.map(({ dim, avg }) => ({
      key: dim,
      name: label(dim),
      label: <span>{label(dim)}</span>,
      value: avg,
      display: formatPercent(avg / total, lng),
    }));
  };
  const statuses = shares("presence.status", (d) =>
    t(`analytics.presence.statuses.${d}`, { defaultValue: d }),
  );
  const platforms = shares("presence.platform", (d) =>
    t(`analytics.presence.platforms.${d}`, { defaultValue: d }),
  );
  const activities = rankItems(
    summary.rankings["presence.activity"] ?? [],
    (d) => parseActivity(d).name,
    num,
    lng,
    (d) => {
      const a = parseActivity(d);
      return (
        <span className="flex min-w-0 items-baseline gap-1.5">
          <span className="truncate">{a.name}</span>
          {a.type ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {t(`analytics.presence.activityTypes.${a.type}`, { defaultValue: a.type })}
            </span>
          ) : null}
        </span>
      );
    },
  );
  const empty = t("analytics.rankings.empty");
  return (
    <Section title={t("analytics.sections.presence")}>
      <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-3">
        <ChartCard
          title={t("analytics.presence.status")}
          description={t("analytics.presence.shareDescription")}
        >
          <RankBars items={statuses} label={t("analytics.presence.status")} empty={empty} />
        </ChartCard>
        <ChartCard
          title={t("analytics.presence.platform")}
          description={t("analytics.presence.shareDescription")}
        >
          <RankBars items={platforms} label={t("analytics.presence.platform")} empty={empty} />
        </ChartCard>
        <ChartCard
          title={t("analytics.presence.activities")}
          description={t("analytics.presence.activitiesDescription")}
        >
          <RankBars items={activities} label={t("analytics.presence.activities")} empty={empty} />
        </ChartCard>
      </div>
    </Section>
  );
}
