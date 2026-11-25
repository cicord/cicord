/**
 * Hand-rolled SVG charts of the analytics dashboard (no chart library): time charts (lines,
 * stacked or diverging bars), sparklines, ranked bars, a weekday × hour heatmap and the table
 * view every chart carries. Colours come from the --chart-* tokens (light and dark), text
 * always wears text tokens. Hover and keyboard focus show the same readout: arrow keys move
 * the crosshair, a live region announces the values.
 */
import type { AnalyticsStep } from "@cicord/client";
import { ChevronRight, Table2 } from "lucide-react";
import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { useTranslation } from "../i18n/index.ts";
import { formatBucket, formatTick, inProgressFrom } from "../lib/analytics.ts";
import { cn } from "../lib/utils.ts";
import { Button } from "./ui/button.tsx";
import { Card, CardAction, CardDescription, CardHeader, CardTitle } from "./ui/card.tsx";

/** Categorical slots, in their validated order. Assign by entity, never cycle past 8. */
export const SERIES_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
  "var(--chart-6)",
  "var(--chart-7)",
  "var(--chart-8)",
] as const;

/** "Other" and context series: de-emphasised grey. */
export const OTHER_COLOR = "var(--subtle-foreground)";

// ───────────────────────────────────────────────────────────── layout

/** Width of an element, kept in sync with ResizeObserver (a sensible default before layout). */
function useWidth<T extends HTMLElement>(fallback = 640) {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(fallback);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return undefined;
    const measure = () => {
      const w = el.getBoundingClientRect().width;
      if (w > 0) setWidth(Math.round(w));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

/**
 * Round tick values covering [min, max] (about `count` of them). `integer`: whole-number
 * ticks only (a step of at least 1, no 2.5 at magnitude 1), for counts and members.
 */
export function niceTicks(
  min: number,
  max: number,
  count = 4,
  { integer = false }: { integer?: boolean } = {},
): number[] {
  if (max <= min) max = min + 1;
  if (integer) {
    min = Math.floor(min);
    max = Math.ceil(max);
  }
  const raw = integer ? Math.max(1, (max - min) / count) : (max - min) / count;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step =
    ([1, 2, 2.5, 5, 10].find((s) => s * mag >= raw && (!integer || Number.isInteger(s * mag))) ??
      10) * mag;
  const start = Math.floor(min / step) * step;
  const ticks: number[] = [];
  for (let v = start; v <= max + step * 0.5; v += step) ticks.push(Math.round(v * 1e6) / 1e6);
  if (ticks[ticks.length - 1]! < max) ticks.push(ticks[ticks.length - 1]! + step);
  return ticks;
}

// ───────────────────────────────────────────────────────────── card & legend

export interface LegendItem {
  label: ReactNode;
  color: string;
  mark?: "bar" | "line";
}

export function Legend({ items, className }: { items: LegendItem[]; className?: string }) {
  return (
    <ul className={cn("flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs", className)}>
      {items.map((item, i) => (
        <li key={i} className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
          <Swatch color={item.color} mark={item.mark ?? "bar"} />
          <span className="truncate">{item.label}</span>
        </li>
      ))}
    </ul>
  );
}

function Swatch({ color, mark }: { color: string; mark: "bar" | "line" }) {
  return mark === "line" ? (
    <span
      aria-hidden="true"
      className="h-0.5 w-3 shrink-0 rounded-full"
      style={{ background: color }}
    />
  ) : (
    <span
      aria-hidden="true"
      className="size-2.5 shrink-0 rounded-[3px]"
      style={{ background: color }}
    />
  );
}

export interface TableView {
  caption: string;
  columns: string[];
  rows: { key: string | number; cells: ReactNode[] }[];
}

/** A chart's card: title, description, legend, the chart, and its table view behind a toggle. */
export function ChartCard({
  title,
  description,
  legend,
  actions,
  table,
  children,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  legend?: LegendItem[];
  actions?: ReactNode;
  table?: TableView;
  children: ReactNode;
  className?: string;
}) {
  const { t } = useTranslation();
  const id = useId();
  const [showTable, setShowTable] = useState(false);
  return (
    <Card aria-labelledby={`${id}-title`} className={cn("flex min-w-0 flex-col", className)}>
      <CardHeader className="flex-wrap items-center gap-y-1 py-2.5">
        <div className="min-w-0">
          <CardTitle id={`${id}-title`}>{title}</CardTitle>
          {description ? (
            <CardDescription className="text-xs text-pretty">{description}</CardDescription>
          ) : null}
        </div>
        <CardAction>
          {actions}
          {table ? (
            <Button
              variant="ghost"
              size="sm"
              className="text-muted-foreground"
              aria-expanded={showTable}
              aria-controls={`${id}-table`}
              onClick={() => setShowTable((v) => !v)}
            >
              <Table2 />
              {showTable ? t("analytics.chart.hideTable") : t("analytics.chart.showTable")}
            </Button>
          ) : null}
        </CardAction>
      </CardHeader>
      <div className="flex flex-1 flex-col gap-3 p-4">
        {legend && legend.length > 1 ? <Legend items={legend} /> : null}
        {children}
      </div>
      {table && showTable ? (
        <div id={`${id}-table`} className="max-h-80 overflow-auto border-t">
          <DataTable table={table} />
        </div>
      ) : null}
    </Card>
  );
}

function DataTable({ table }: { table: TableView }) {
  return (
    <table className="w-full text-left text-xs">
      <caption className="sr-only">{table.caption}</caption>
      <thead className="sticky top-0 bg-card">
        <tr className="border-b">
          {table.columns.map((c, i) => (
            <th
              key={i}
              scope="col"
              className={cn(
                "px-4 py-2 font-medium whitespace-nowrap text-muted-foreground",
                i > 0 && "text-right",
              )}
            >
              {c}
            </th>
          ))}
        </tr>
      </thead>
      <tbody className="tabular">
        {table.rows.map((r) => (
          <tr key={r.key} className="border-b last:border-b-0">
            {r.cells.map((cell, i) =>
              i === 0 ? (
                <th key={i} scope="row" className="px-4 py-1.5 font-normal whitespace-nowrap">
                  {cell}
                </th>
              ) : (
                <td key={i} className="px-4 py-1.5 text-right whitespace-nowrap">
                  {cell}
                </td>
              ),
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// ───────────────────────────────────────────────────────────── readout (hover + keyboard)

interface ReadoutRow {
  color: string;
  label: string;
  value: string;
  mark?: "bar" | "line";
}

function Readout({
  title,
  rows,
  x,
  width,
}: {
  title: string;
  rows: ReadoutRow[];
  x: number;
  width: number;
}) {
  const flip = x > width * 0.6;
  return (
    <div
      aria-hidden="true"
      className="pointer-events-none absolute top-0 z-10 w-max max-w-60 rounded-md border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-pop"
      style={flip ? { right: Math.max(0, width - x + 10) } : { left: x + 10 }}
    >
      <p className="mb-1 text-muted-foreground">{title}</p>
      <ul className="flex flex-col gap-0.5">
        {rows.map((r, i) => (
          <li key={i} className="flex items-center gap-2">
            <Swatch color={r.color} mark={r.mark ?? "line"} />
            <span className="font-semibold tabular text-foreground">{r.value}</span>
            <span className="truncate text-muted-foreground">{r.label}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Arrow-key navigation over n positions; returns the active index (null: none). */
function useCursor(n: number) {
  const [active, setActive] = useState<number | null>(null);
  const onKeyDown = (e: KeyboardEvent) => {
    const keys: Record<string, (i: number) => number> = {
      ArrowLeft: (i) => i - 1,
      ArrowRight: (i) => i + 1,
      Home: () => 0,
      End: () => n - 1,
      PageUp: (i) => i - 7,
      PageDown: (i) => i + 7,
    };
    const fn = keys[e.key];
    if (!fn || n === 0) return;
    e.preventDefault();
    setActive((i) => Math.max(0, Math.min(n - 1, fn(i ?? n - 1))));
  };
  return {
    active,
    setActive,
    handlers: {
      onKeyDown,
      onFocus: () => setActive((i) => i ?? n - 1),
      onBlur: () => setActive(null),
      onPointerLeave: () => setActive(null),
    },
  };
}

// ───────────────────────────────────────────────────────────── time chart

export interface TimeSeries {
  key: string;
  label: string;
  color: string;
  values: (number | null)[];
  /** Drawn below the baseline (leaves under joins); the readout shows the positive value. */
  negative?: boolean;
}

/**
 * Values over regular buckets. `bars`: positive series stack upwards, negative ones
 * downwards (2px surface gap between segments, 4px rounded data-end); `line`: one 2px line
 * per series (a 10 % wash under a single series), with an end dot. `overlay` lines (net
 * growth on its bars) share the same axis.
 */
export function TimeChart({
  buckets,
  step,
  series,
  kind,
  overlay = [],
  format,
  formatAxis = format,
  label,
  height = 180,
  minZero = true,
  integer = true,
  axisWidth,
}: {
  buckets: number[];
  step: AnalyticsStep;
  series: TimeSeries[];
  kind: "bars" | "line";
  overlay?: TimeSeries[];
  format: (v: number) => string;
  formatAxis?: (v: number) => string;
  /** Accessible name. */
  label: string;
  height?: number;
  /** Lines: the y axis starts at 0 (false: fits the data, e.g. member totals). */
  minZero?: boolean;
  /** Whole-number y ticks (default: every metric is a count, members or seconds). */
  integer?: boolean;
  /** Fixed width of the y axis labels (aligns stacked panels). */
  axisWidth?: number;
}) {
  const { t, i18n } = useTranslation();
  const [ref, width] = useWidth<HTMLDivElement>();
  const n = buckets.length;
  const cursor = useCursor(n);
  const all = [...series, ...overlay];

  // Y domain.
  let lo = 0;
  let hi = 0;
  if (kind === "bars") {
    for (let i = 0; i < n; i++) {
      let up = 0;
      let down = 0;
      for (const s of series) {
        const v = s.values[i] ?? 0;
        if (s.negative) down -= v;
        else up += v;
      }
      hi = Math.max(hi, up);
      lo = Math.min(lo, down);
    }
  } else {
    const vals = series.flatMap((s) => s.values.filter((v): v is number => v !== null));
    hi = vals.length ? Math.max(...vals) : 1;
    lo = minZero || !vals.length ? 0 : Math.min(...vals);
    if (!minZero && hi - lo < 1) lo -= 1;
  }
  for (const s of overlay)
    for (const v of s.values) {
      if (v === null) continue;
      hi = Math.max(hi, v);
      lo = Math.min(lo, v);
    }
  if (hi === lo) hi = lo + 1;
  const ticks = niceTicks(lo, hi, height < 140 ? 3 : 4, { integer });
  const y0 = ticks[0]!;
  const y1 = ticks[ticks.length - 1]!;

  const axisW = axisWidth ?? Math.max(...ticks.map((v) => formatAxis(v).length)) * 6.6 + 10;
  const m = { top: 8, right: 10, bottom: 22, left: axisW };
  const pw = Math.max(10, width - m.left - m.right);
  const ph = height - m.top - m.bottom;
  const band = pw / Math.max(1, n);
  const cx = (i: number) => m.left + band * (i + 0.5);
  const y = (v: number) => m.top + ph - ((v - y0) / (y1 - y0)) * ph;

  // X ticks: about one label per 90px; hourly buckets are labelled at midnight UTC (dates)
  // unless the range is shorter than two days.
  const indices = buckets.map((_, i) => i);
  const midnights = indices.filter((i) => buckets[i]! % 86400 === 0);
  const candidates = step === "hour" && midnights.length >= 2 ? midnights : indices;
  const slots = Math.max(1, Math.floor(pw / 90));
  const every = Math.max(1, Math.ceil(candidates.length / slots));
  const xTicks = candidates.filter((_, k) => (candidates.length - 1 - k) % every === 0);

  const pointerIndex = (clientX: number, el: Element) => {
    const rect = el.getBoundingClientRect();
    const i = Math.floor((clientX - rect.left - m.left) / band);
    return Math.max(0, Math.min(n - 1, i));
  };

  // The current day / week / hour is still being counted: drawn faded, labelled in progress.
  const partial = inProgressFrom(buckets, step);
  const inProgress = (i: number) => partial >= 0 && i >= partial;
  const active = cursor.active;
  const readout: { title: string; rows: ReadoutRow[] } | null =
    active !== null
      ? {
          title: `${formatBucket(buckets[active]!, step, i18n.language)}${inProgress(active) ? ` (${t("analytics.chart.inProgress")})` : ""}`,
          rows: all.map((s) => ({
            color: s.color,
            label: s.label,
            mark: kind === "bars" && !overlay.includes(s) ? "bar" : "line",
            value: s.values[active] === null ? "—" : format(s.values[active]!),
          })),
        }
      : null;

  return (
    <div className="relative" ref={ref}>
      <div
        role="group"
        aria-roledescription={t("analytics.chart.roledescription")}
        aria-label={`${label}. ${t("analytics.chart.keys")}`}
        tabIndex={0}
        className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onPointerMove={(e) => n && cursor.setActive(pointerIndex(e.clientX, e.currentTarget))}
        {...cursor.handlers}
      >
        <svg width={width} height={height} aria-hidden="true" className="block overflow-visible">
          {ticks.map((v) => (
            <g key={v}>
              <line
                x1={m.left}
                x2={m.left + pw}
                y1={y(v)}
                y2={y(v)}
                stroke={v === 0 ? "var(--chart-axis)" : "var(--chart-grid)"}
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text
                x={m.left - 8}
                y={y(v)}
                dy="0.32em"
                textAnchor="end"
                className="fill-muted-foreground tabular text-[11px]"
              >
                {formatAxis(v)}
              </text>
            </g>
          ))}
          {xTicks.map((i) => (
            <text
              key={i}
              x={cx(i)}
              y={height - 6}
              textAnchor={i === n - 1 && n > 1 ? "end" : i === 0 ? "start" : "middle"}
              className="fill-muted-foreground tabular text-[11px]"
            >
              {formatTick(buckets[i]!, step, i18n.language)}
            </text>
          ))}
          {active !== null && kind === "bars" ? (
            <rect
              x={m.left + band * active}
              y={m.top}
              width={band}
              height={ph}
              className="fill-surface-hover"
            />
          ) : null}
          {kind === "bars" ? (
            <Bars series={series} n={n} band={band} cx={cx} y={y} faded={inProgress} />
          ) : (
            series.map((s) => (
              <Line
                key={s.key}
                s={s}
                cx={cx}
                y={y}
                wash={series.length === 1}
                base={y(Math.max(y0, 0))}
                partial={partial}
              />
            ))
          )}
          {overlay.map((s) => (
            <Line key={s.key} s={s} cx={cx} y={y} partial={partial} />
          ))}
          {active !== null ? (
            <g>
              {kind === "line" ? (
                <line
                  x1={cx(active)}
                  x2={cx(active)}
                  y1={m.top}
                  y2={m.top + ph}
                  stroke="var(--chart-axis)"
                  shapeRendering="crispEdges"
                />
              ) : null}
              {(kind === "line" ? all : overlay).map((s) =>
                s.values[active] === null ? null : (
                  <circle
                    key={s.key}
                    cx={cx(active)}
                    cy={y(s.values[active]!)}
                    r={4}
                    fill={s.color}
                    stroke="var(--card)"
                    strokeWidth={2}
                  />
                ),
              )}
            </g>
          ) : null}
        </svg>
      </div>
      {readout && active !== null ? (
        <Readout title={readout.title} rows={readout.rows} x={cx(active)} width={width} />
      ) : null}
      <p className="sr-only" aria-live="polite">
        {readout
          ? `${readout.title}: ${readout.rows.map((r) => `${r.label} ${r.value}`).join(", ")}`
          : ""}
      </p>
    </div>
  );
}

/** A bar with a rounded data-end (4px) and a square base. */
function barPath(x: number, w: number, yBase: number, yEnd: number): string {
  const up = yEnd < yBase;
  const h = Math.abs(yBase - yEnd);
  const r = Math.min(4, w / 2, h);
  if (up)
    return `M${x},${yBase}V${yEnd + r}Q${x},${yEnd} ${x + r},${yEnd}H${x + w - r}Q${x + w},${yEnd} ${x + w},${yEnd + r}V${yBase}Z`;
  return `M${x},${yBase}V${yEnd - r}Q${x},${yEnd} ${x + r},${yEnd}H${x + w - r}Q${x + w},${yEnd} ${x + w},${yEnd - r}V${yBase}Z`;
}

function Bars({
  series,
  n,
  band,
  cx,
  y,
  faded,
}: {
  series: TimeSeries[];
  n: number;
  band: number;
  cx: (i: number) => number;
  y: (v: number) => number;
  faded: (i: number) => boolean;
}) {
  const w = Math.max(1, Math.min(24, band > 6 ? band * 0.7 : band - 1));
  const buckets: ReactNode[] = [];
  for (let i = 0; i < n; i++) {
    const x = cx(i) - w / 2;
    const marks: ReactNode[] = [];
    for (const dir of [1, -1] as const) {
      const stack = series.filter((s) => (dir === 1 ? !s.negative : s.negative));
      const present = stack.filter((s) => (s.values[i] ?? 0) > 0);
      let acc = 0;
      present.forEach((s, k) => {
        const v = s.values[i]!;
        const from = y(acc * dir);
        const to = y((acc + v) * dir);
        acc += v;
        // 2px surface gap between stacked segments (only when the segment can afford it).
        const gap = k > 0 && Math.abs(to - from) > 3 ? 2 : 0;
        const base = from - gap * dir;
        const last = k === present.length - 1;
        marks.push(
          last ? (
            <path key={s.key} d={barPath(x, w, base, to)} fill={s.color} />
          ) : (
            <rect
              key={s.key}
              x={x}
              width={w}
              y={Math.min(base, to)}
              height={Math.max(0, Math.abs(to - base))}
              fill={s.color}
            />
          ),
        );
      });
    }
    buckets.push(
      <g key={i} opacity={faded(i) ? 0.45 : undefined}>
        {marks}
      </g>,
    );
  }
  return <g>{buckets}</g>;
}

/** Runs of consecutive non-null points (a gap breaks the line). */
function runsOf(
  values: (number | null)[],
  cx: (i: number) => number,
  y: (v: number) => number,
): [number, number][][] {
  const runs: [number, number][][] = [];
  let run: [number, number][] = [];
  values.forEach((v, i) => {
    if (v === null) {
      if (run.length) runs.push(run);
      run = [];
    } else run.push([cx(i), y(v)]);
  });
  if (run.length) runs.push(run);
  return runs;
}

const pathOf = (pts: [number, number][]) =>
  pts.map(([px, py], i) => `${i ? "L" : "M"}${px},${py}`).join("");

function Line({
  s,
  cx,
  y,
  wash,
  base,
  partial = -1,
}: {
  s: TimeSeries;
  cx: (i: number) => number;
  y: (v: number) => number;
  wash?: boolean;
  base?: number;
  /** First in-progress bucket: the line into it is faded. */
  partial?: number;
}) {
  const solid = runsOf(
    s.values.map((v, i) => (partial >= 0 && i >= partial ? null : v)),
    cx,
    y,
  );
  const tail =
    partial > 0
      ? runsOf(
          s.values.map((v, i) => (i < partial - 1 ? null : v)),
          cx,
          y,
        )
      : [];
  const lastIndex = s.values.findLastIndex((v) => v !== null);
  const end = lastIndex >= 0 ? ([cx(lastIndex), y(s.values[lastIndex]!)] as const) : undefined;
  const stroke = (pts: [number, number][], key: string, opacity?: number) =>
    pts.length === 1 ? (
      <circle key={key} cx={pts[0]![0]} cy={pts[0]![1]} r={2} fill={s.color} opacity={opacity} />
    ) : (
      <path
        key={key}
        d={pathOf(pts)}
        fill="none"
        stroke={s.color}
        strokeWidth={2}
        strokeLinejoin="round"
        strokeLinecap="round"
        opacity={opacity}
      />
    );
  return (
    <g>
      {wash && base !== undefined
        ? solid.map((pts, i) => (
            <path
              key={`w${i}`}
              d={`${pathOf(pts)}L${pts[pts.length - 1]![0]},${base}L${pts[0]![0]},${base}Z`}
              fill={s.color}
              opacity={0.1}
            />
          ))
        : null}
      {solid.map((pts, i) => stroke(pts, `s${i}`))}
      {tail.map((pts, i) => (pts.length > 1 ? stroke(pts, `t${i}`, 0.45) : null))}
      {end ? (
        <circle
          cx={end[0]}
          cy={end[1]}
          r={4}
          fill={s.color}
          stroke="var(--card)"
          strokeWidth={2}
          opacity={partial >= 0 && lastIndex >= partial ? 0.45 : undefined}
        />
      ) : null}
    </g>
  );
}

// ───────────────────────────────────────────────────────────── sparkline

/** A KPI's trend: the period in the de-emphasis grey, the latest point in the accent. */
export function Sparkline({
  values,
  partial = false,
  className,
}: {
  values: number[];
  /** The last value is still being counted (today): faded, the accent on the day before. */
  partial?: boolean;
  className?: string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>(120);
  const h = 28;
  if (values.length < 2) return <div ref={ref} className={cn("h-7", className)} />;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const px = (i: number) => 2 + (i / (values.length - 1)) * (width - 4);
  const py = (v: number) => 3 + (1 - (v - min) / span) * (h - 6);
  const fade = partial && values.length > 2;
  const last = values.length - 1 - (fade ? 1 : 0);
  const d = values
    .slice(0, last + 1)
    .map((v, i) => `${i ? "L" : "M"}${px(i)},${py(v)}`)
    .join("");
  return (
    <div ref={ref} className={cn("h-7", className)} aria-hidden="true">
      <svg width={width} height={h} className="block overflow-visible">
        <path
          d={d}
          fill="none"
          stroke="var(--subtle-foreground)"
          strokeWidth={1.5}
          strokeLinejoin="round"
          strokeLinecap="round"
          opacity={0.7}
        />
        {fade ? (
          <path
            d={`M${px(last)},${py(values[last]!)}L${px(last + 1)},${py(values[last + 1]!)}`}
            fill="none"
            stroke="var(--subtle-foreground)"
            strokeWidth={1.5}
            strokeLinecap="round"
            opacity={0.3}
          />
        ) : null}
        <circle
          cx={px(last)}
          cy={py(values[last]!)}
          r={3}
          fill="var(--chart-1)"
          stroke="var(--card)"
          strokeWidth={1.5}
        />
      </svg>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── ranked bars

export interface RankItem {
  key: string;
  label: ReactNode;
  /** Plain-text name (accessible label of the row's bar). */
  name: string;
  value: number;
  display: string;
  /** Secondary text after the value (change vs the previous period, a share). */
  extra?: ReactNode;
  color?: string;
  /**
   * The entries this one sums (a group of the channels a plugin created): hidden behind a
   * toggle, their bars relative to this entry.
   */
  children?: RankItem[];
  /** Accessible name of the toggle that shows the children. */
  expandLabel?: string;
}

/** Ranked horizontal bars on one baseline, one hue (ordered buckets pass their own colours). */
export function RankBars({
  items,
  empty,
  label,
}: {
  items: RankItem[];
  empty?: ReactNode;
  label: string;
}) {
  if (items.length === 0)
    return <p className="py-6 text-center text-[13px] text-muted-foreground">{empty}</p>;
  const max = Math.max(...items.map((i) => i.value)) || 1;
  return (
    <ol className="flex flex-col gap-2" aria-label={label}>
      {items.map((item) => (
        <RankRow key={item.key} item={item} max={max} />
      ))}
    </ol>
  );
}

function RankRow({ item, max }: { item: RankItem; max: number }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const kids = item.children ?? [];
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1">
      <span className="flex min-w-0 items-center gap-2 text-[13px]">
        {kids.length > 0 ? (
          <button
            type="button"
            className="-ml-1 inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-surface-sunken hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
            aria-expanded={open}
            aria-controls={id}
            aria-label={item.expandLabel ?? item.name}
            onClick={() => setOpen((o) => !o)}
          >
            <ChevronRight
              className={cn("size-3.5 transition-transform", open && "rotate-90")}
              aria-hidden="true"
            />
          </button>
        ) : null}
        {item.label}
      </span>
      <span className="flex items-baseline gap-2 text-[13px]">
        <span className="font-medium tabular">{item.display}</span>
        {item.extra ? (
          <span className="text-xs text-muted-foreground tabular">{item.extra}</span>
        ) : null}
      </span>
      <span className="col-span-2 h-1.5 rounded-full bg-surface-sunken" aria-hidden="true">
        <span
          className="block h-full rounded-full"
          style={{
            width: `${Math.max(1.5, (item.value / max) * 100)}%`,
            background: item.color ?? "var(--chart-1)",
          }}
        />
      </span>
      {kids.length > 0 ? (
        <ol
          id={id}
          hidden={!open}
          aria-label={item.name}
          className={cn(
            "col-span-2 mt-1 mb-1 flex-col gap-2 border-l border-border pl-4",
            open ? "flex" : "hidden",
          )}
        >
          {kids.map((kid) => (
            <RankRow key={kid.key} item={kid} max={item.value || 1} />
          ))}
        </ol>
      ) : null}
    </li>
  );
}

/** Step k of n of the ordinal ramp (ordered buckets: account ages). */
export const ordinalColor = (k: number, n: number) =>
  `color-mix(in oklab, var(--chart-ord-hi) ${Math.round((n <= 1 ? 1 : k / (n - 1)) * 100)}%, var(--chart-ord-lo))`;

// ───────────────────────────────────────────────────────────── heatmap

/** Weekday × hour grid, one-hue sequential scale, roving cursor over the cells. */
export function Heatmap({
  grid,
  rows,
  columns,
  format,
  label,
  scaleLabel,
}: {
  grid: number[][];
  rows: string[];
  columns: string[];
  format: (v: number) => string;
  label: string;
  scaleLabel: [string, string];
}) {
  const { t } = useTranslation();
  const max = Math.max(1, ...grid.flat());
  const cols = columns.length;
  const n = rows.length * cols;
  const cursor = useCursor(n);
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      e.preventDefault();
      cursor.setActive((i) => {
        const cur = i ?? 0;
        const next = cur + (e.key === "ArrowUp" ? -cols : cols);
        return next < 0 || next >= n ? cur : next;
      });
      return;
    }
    cursor.handlers.onKeyDown(e);
  };
  const active = cursor.active;
  const ar = active === null ? -1 : Math.floor(active / cols);
  const ac = active === null ? -1 : active % cols;
  const text = active === null ? "" : `${rows[ar]} ${columns[ac]}: ${format(grid[ar]?.[ac] ?? 0)}`;
  return (
    <div className="flex flex-col gap-3">
      <div
        role="group"
        aria-roledescription={t("analytics.chart.roledescription")}
        aria-label={`${label}. ${t("analytics.chart.gridKeys")}`}
        tabIndex={0}
        className="relative rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring"
        {...cursor.handlers}
        onFocus={() => cursor.setActive((i) => i ?? 0)}
        onKeyDown={onKeyDown}
      >
        <div
          className="grid gap-[2px]"
          style={{ gridTemplateColumns: `2.25rem repeat(${cols}, minmax(0, 1fr))` }}
          aria-hidden="true"
        >
          {rows.map((r, ri) => (
            <div key={r} className="contents">
              <span className="self-center pr-1 text-[11px] text-muted-foreground">{r}</span>
              {columns.map((c, ci) => {
                const v = grid[ri]?.[ci] ?? 0;
                const idx = ri * cols + ci;
                return (
                  <span
                    key={c}
                    onPointerEnter={() => cursor.setActive(idx)}
                    className={cn(
                      "aspect-[1.6] min-h-3 rounded-[3px]",
                      idx === active && "ring-2 ring-foreground ring-offset-1 ring-offset-card",
                    )}
                    style={{
                      background:
                        v === 0
                          ? "var(--surface-sunken)"
                          : `color-mix(in oklab, var(--chart-seq-hi) ${Math.round(12 + (v / max) * 88)}%, var(--chart-seq-lo))`,
                    }}
                  />
                );
              })}
            </div>
          ))}
          <span />
          {columns.map((c, ci) => (
            <span key={c} className="text-center text-[11px] text-muted-foreground tabular">
              {ci % 3 === 0 ? c.slice(0, 2) : ""}
            </span>
          ))}
        </div>
        {active !== null ? (
          <div
            aria-hidden="true"
            className="pointer-events-none absolute z-10 w-max rounded-md border bg-popover px-2.5 py-1.5 text-xs shadow-pop"
            style={{
              top: `calc(${((ar + 1) / (rows.length + 1)) * 100}% + 4px)`,
              ...(ac > cols / 2
                ? { right: `${((cols - ac) / cols) * 100}%` }
                : { left: `calc(2.25rem + ${(ac / cols) * 100}%)` }),
            }}
          >
            <span className="font-semibold tabular">{format(grid[ar]?.[ac] ?? 0)}</span>{" "}
            <span className="text-muted-foreground">
              {rows[ar]} {columns[ac]}
            </span>
          </div>
        ) : null}
      </div>
      <div className="flex items-center gap-2 text-[11px] text-muted-foreground" aria-hidden="true">
        <span>{scaleLabel[0]}</span>
        <span
          className="h-2 w-28 rounded-full"
          style={{
            background:
              "linear-gradient(to right, color-mix(in oklab, var(--chart-seq-hi) 12%, var(--chart-seq-lo)), var(--chart-seq-hi))",
          }}
        />
        <span>{scaleLabel[1]}</span>
      </div>
      <p className="sr-only" aria-live="polite">
        {text}
      </p>
    </div>
  );
}
