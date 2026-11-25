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

const DAY = 86400;

/** Days covered by a summary (its normalized window). */
const daysOf = (summary: Pick<AnalyticsSummary, "from" | "to">): number =>
  (Date.parse(summary.to) - Date.parse(summary.from)) / 1000 / DAY;

/**
 * Bucket of the period's charts (at most 2000 points per series): hours under 2 days, days
 * under 120 days, else weeks (7d to 90d: days, a year: weeks).
 */
export function stepFor(days: number): AnalyticsStep {
  return days < 2 ? "hour" : days < 120 ? "day" : "week";
}

/** Bucket of the sampled gauges (online members, voice peak): hours up to 30 days. */
export function fineStepFor(days: number): AnalyticsStep {
  return days <= 30 ? "hour" : "day";
}

/** Metrics of the dashboard's main series request (one step for every chart). */
export function mainSeriesParams(summary: AnalyticsSummary): AnalyticsSeriesParams {
  return {
    metrics: [
      "members.total",
      "members.bots",
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
    step: stepFor(daysOf(summary)),
    top: 6,
  };
}

export function fineSeriesParams(summary: AnalyticsSummary): AnalyticsSeriesParams {
  return {
    metrics: ["members.online", "voice.peak"],
    from: summary.from,
    to: summary.to,
    step: fineStepFor(daysOf(summary)),
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
  "members.bots": "neutral",
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

// ───────────────────────────────────────────────────────────── custom range

/** Longest custom range the summary accepts. */
export const MAX_RANGE_DAYS = 400;

/** Why a custom range is refused before asking the runtime (null: valid). */
export type RangeProblem = "missing" | "order" | "tooLong" | "future";

/**
 * Checks a custom range the way the runtime does after rounding outward to UTC hours:
 * from before to (so at least one hour), at most 400 days, from before now.
 */
export function rangeProblem(
  from: Date | null,
  to: Date | null,
  now = Date.now(),
): RangeProblem | null {
  if (!from || !to || Number.isNaN(from.getTime()) || Number.isNaN(to.getTime())) return "missing";
  if (from.getTime() >= to.getTime()) return "order";
  if (from.getTime() >= now) return "future";
  const hour = 3600_000;
  const span = Math.ceil(to.getTime() / hour) * hour - Math.floor(from.getTime() / hour) * hour;
  if (span > MAX_RANGE_DAYS * DAY * 1000) return "tooLong";
  return null;
}

const pad = (n: number) => String(n).padStart(2, "0");

/** A time as the value of an `<input type="datetime-local">` (the browser's time zone). */
export function toLocalInput(time: Date | string | number): string {
  const d = new Date(time);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** The value of an `<input type="datetime-local">` as a Date (null when empty or invalid). */
export function fromLocalInput(value: string): Date | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(value)) return null;
  // Without an offset, a date-time string is parsed in the local time zone.
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** A Date as RFC 3339 UTC, whole seconds. */
export const toRfc3339 = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");

/** A summary window as a short label: "Nov 3, 14:00 – Nov 4, 10:00" (UTC, like the charts). */
export function formatRange(from: string, to: string, lng: string): string {
  const a = new Date(from);
  const b = new Date(to);
  const sameYear =
    a.getUTCFullYear() === b.getUTCFullYear() && a.getUTCFullYear() === new Date().getUTCFullYear();
  const wholeDays = a.getTime() % (DAY * 1000) === 0 && b.getTime() % (DAY * 1000) === 0;
  const fmt = new Intl.DateTimeFormat(lng, {
    month: "short",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
    ...(wholeDays ? {} : { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }),
    timeZone: "UTC",
  });
  // Whole days end at 00:00 of the next day: show the last day included.
  return fmt.formatRange(a, wholeDays ? new Date(b.getTime() - DAY * 1000) : b);
}

// ───────────────────────────────────────────────────────────── links in text

/** A part of a text: plain text, or an http(s) URL. */
export type TextPart = { text: string; href?: string };

/**
 * Splits a text around its http(s) URLs. Trailing punctuation (".", ",", ";", ":", "!", "?")
 * and a ")" without its "(" in the URL stay in the text.
 */
export function splitLinks(text: string): TextPart[] {
  const parts: TextPart[] = [];
  let last = 0;
  for (const m of text.matchAll(/https?:\/\/[^\s<>"]+/g)) {
    let url = m[0];
    for (;;) {
      if (/[.,;:!?'"]$/.test(url)) url = url.slice(0, -1);
      else if (url.endsWith(")") && count(url, "(") < count(url, ")")) url = url.slice(0, -1);
      else break;
    }
    if (!/^https?:\/\/[^/]/.test(url)) continue;
    if (m.index > last) parts.push({ text: text.slice(last, m.index) });
    parts.push({ text: url, href: url });
    last = m.index + url.length;
  }
  if (last < text.length) parts.push({ text: text.slice(last) });
  return parts;
}

const count = (s: string, c: string) => s.split(c).length - 1;

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
