/** Shaping and formatting of the analytics API responses for the dashboard. */
import type {
  AnalyticsLabel,
  AnalyticsMissing,
  AnalyticsPeriod,
  AnalyticsSeries,
  AnalyticsSeriesParams,
  AnalyticsStatus,
  AnalyticsStep,
  AnalyticsSummary,
} from "@cicord/client";

export const PERIODS: readonly AnalyticsPeriod[] = ["7d", "30d", "90d", "365d"];

/** Account age buckets of `members.joined.age`, youngest first. */
export const ACCOUNT_AGES = ["<1d", "<7d", "<30d", "<1y", ">=1y"] as const;

const STEP_SECONDS: Record<AnalyticsStep, number> = { hour: 3600, day: 86400, week: 604800 };

/** Bucket of the period's charts: days, weeks for a year (at most 2000 points per series). */
const stepFor = (period: AnalyticsPeriod): AnalyticsStep => (period === "365d" ? "week" : "day");

/** Bucket of the sampled gauges (online members, voice peak): hours up to 30 days. */
const fineStepFor = (period: AnalyticsPeriod): AnalyticsStep =>
  period === "7d" || period === "30d" ? "hour" : "day";

/** Metrics of the dashboard's main series request (one step for every chart). */
export function mainSeriesParams(summary: AnalyticsSummary): AnalyticsSeriesParams {
  return {
    metrics: [
      "members.total",
      "members.joined",
      "members.left",
      "members.active",
      "members.active.chat",
      "members.active.voice",
      "messages.sent",
      "messages.replies",
      "messages.attachments",
      "threads.created",
      "polls.created",
      "voice.seconds",
      "voice.sessions",
      "voice.streaming",
      "stage.seconds",
      ...(summary.status.presence ? ["presence.status", "presence.platform"] : []),
    ],
    from: summary.from,
    to: summary.to,
    step: stepFor(summary.period),
    top: 6,
  };
}

export function fineSeriesParams(summary: AnalyticsSummary): AnalyticsSeriesParams {
  return {
    metrics: ["members.online", "voice.peak"],
    from: summary.from,
    to: summary.to,
    step: fineStepFor(summary.period),
  };
}

/** Bucket starts (unix seconds) of a series response. */
export function bucketsOf(series: Pick<AnalyticsSeries, "from" | "to" | "step">): number[] {
  const step = STEP_SECONDS[series.step] ?? 86400;
  const from = Math.floor(Date.parse(series.from) / 1000);
  const to = Math.floor(Date.parse(series.to) / 1000);
  const out: number[] = [];
  for (let t = from; t < to && out.length < 2000; t += step) out.push(t);
  return out;
}

/** Index of the first bucket still in progress (it ends after now), -1 when none. */
export function inProgressFrom(buckets: number[], step: AnalyticsStep, now = Date.now()): number {
  const size = STEP_SECONDS[step] ?? 86400;
  return buckets.findIndex((t) => (t + size) * 1000 > now);
}

/** Values of one serie on the buckets (null: no point, e.g. a sparse gauge). */
export function valuesOf(
  series: AnalyticsSeries,
  buckets: number[],
  metric: string,
  dim = "",
): (number | null)[] {
  const serie = series.series.find((s) => s.metric === metric && s.dim === dim);
  if (!serie) return buckets.map(() => null);
  const byTime = new Map(serie.points.map(([t, v]) => [t, v]));
  return buckets.map((t) => byTime.get(t) ?? null);
}

/** Dimensions returned for a metric, in rank order, "other" last (the total excluded). */
export function dimsOf(series: AnalyticsSeries, metric: string): string[] {
  return series.series.filter((s) => s.metric === metric && s.dim !== "").map((s) => s.dim);
}

export const sumOf = (values: readonly (number | null)[]): number =>
  values.reduce<number>((a, v) => a + (v ?? 0), 0);

/** Average of the non-null values (null when there is none). */
export function averageOf(values: readonly (number | null)[]): number | null {
  const present = values.filter((v): v is number => v !== null);
  return present.length ? present.reduce((a, v) => a + v, 0) / present.length : null;
}

/** The missing entry that covers a metric, if any. */
export function missingFor(status: AnalyticsStatus, metric: string): AnalyticsMissing | undefined {
  return status.missing.find((m) => m.metrics.includes(metric));
}

// ───────────────────────────────────────────────────────────── KPIs

/** Whether an increase is good news: leaves up is bad; moderation is neither. */
export type Polarity = "up" | "down" | "neutral";

export const KPI_POLARITY: Record<string, Polarity> = {
  "members.left": "down",
  "moderation.actions": "neutral",
};

export type Delta =
  | { kind: "none" }
  | { kind: "new" }
  | { kind: "change"; ratio: number; diff: number; tone: "good" | "bad" | "neutral" };

/** Change against the previous period (none: no comparison, new: up from 0). */
export function deltaOf(value: number, previous: number | null, polarity: Polarity = "up"): Delta {
  if (previous === null) return { kind: "none" };
  if (previous === 0) return value === 0 ? change(0, 0, polarity) : { kind: "new" };
  return change((value - previous) / Math.abs(previous), value - previous, polarity);
}

function change(ratio: number, diff: number, polarity: Polarity): Delta {
  const flat = Math.abs(ratio) < 0.005;
  const up = ratio > 0;
  const tone =
    flat || polarity === "neutral" ? "neutral" : up === (polarity === "up") ? "good" : "bad";
  return { kind: "change", ratio, diff, tone };
}

// ───────────────────────────────────────────────────────────── labels

/** Display name of a dimension: its label, else the raw value. */
export function labelOf(labels: Record<string, AnalyticsLabel>, dim: string): string {
  return labels[dim]?.name || dim;
}

/** "playing:Minecraft" → { type: "playing", name: "Minecraft" }. */
export function parseActivity(dim: string): { type: string; name: string } {
  const i = dim.indexOf(":");
  return i < 0 ? { type: "", name: dim } : { type: dim.slice(0, i), name: dim.slice(i + 1) };
}

/** Custom emoji dims are "name:id"; unicode emojis are the emoji itself. */
export const isCustomEmoji = (dim: string) => /^(a:)?[\w~-]+:\d+$/.test(dim);

// ───────────────────────────────────────────────────────────── formatting

export function formatNumber(value: number, lng: string, compact = false): string {
  const abs = Math.abs(value);
  return new Intl.NumberFormat(lng, {
    maximumFractionDigits: abs >= 100 || Number.isInteger(value) ? 0 : 1,
    ...(compact && abs >= 10000 ? { notation: "compact" } : {}),
  }).format(value);
}

/** Voice time: seconds → hours ("1,557 hr"), minutes under an hour. */
export function formatDuration(seconds: number, lng: string, compact = false): string {
  const hours = seconds / 3600;
  if (hours > 0 && hours < 1)
    return new Intl.NumberFormat(lng, {
      style: "unit",
      unit: "minute",
      unitDisplay: "short",
      maximumFractionDigits: 0,
    }).format(seconds / 60);
  return new Intl.NumberFormat(lng, {
    style: "unit",
    unit: "hour",
    unitDisplay: compact ? "narrow" : "short",
    maximumFractionDigits: hours >= 100 ? 0 : 1,
    ...(compact && hours >= 10000 ? { notation: "compact" } : {}),
  }).format(hours);
}

export function formatPercent(ratio: number, lng: string, signed = false): string {
  return new Intl.NumberFormat(lng, {
    style: "percent",
    maximumFractionDigits: Math.abs(ratio) < 0.1 ? 1 : 0,
    ...(signed ? { signDisplay: "exceptZero" } : {}),
  }).format(ratio);
}

/** A bucket start as a tooltip / table label (UTC). */
export function formatBucket(t: number, step: AnalyticsStep, lng: string): string {
  const d = new Date(t * 1000);
  if (step === "hour")
    return new Intl.DateTimeFormat(lng, {
      weekday: "short",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
      timeZone: "UTC",
    }).format(d);
  return new Intl.DateTimeFormat(lng, {
    ...(step === "day" ? { weekday: "short" } : {}),
    month: "short",
    day: "numeric",
    year: "numeric",
    timeZone: "UTC",
  }).format(d);
}

/** A short axis label: "Nov 3", or "Nov 3" at midnight and "14:00" otherwise for hours. */
export function formatTick(t: number, step: AnalyticsStep, lng: string): string {
  const d = new Date(t * 1000);
  if (step === "hour" && d.getUTCHours() !== 0)
    return new Intl.DateTimeFormat(lng, {
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
      timeZone: "UTC",
    }).format(d);
  return new Intl.DateTimeFormat(lng, { month: "short", day: "numeric", timeZone: "UTC" }).format(
    d,
  );
}

export function formatDay(iso: string, lng: string): string {
  return new Intl.DateTimeFormat(lng, { dateStyle: "medium", timeZone: "UTC" }).format(
    new Date(iso),
  );
}

/** Weekday names, Monday first (the heatmap's row order). */
export function weekdays(lng: string, width: "short" | "long" = "short"): string[] {
  // 2024-01-01 was a Monday.
  return Array.from({ length: 7 }, (_, i) =>
    new Intl.DateTimeFormat(lng, { weekday: width, timeZone: "UTC" }).format(
      new Date(Date.UTC(2024, 0, 1 + i)),
    ),
  );
}

/** "00:00" … "23:00". */
export function hourLabel(h: number, lng: string): string {
  return new Intl.DateTimeFormat(lng, {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
    timeZone: "UTC",
  }).format(new Date(Date.UTC(2024, 0, 1, h)));
}
