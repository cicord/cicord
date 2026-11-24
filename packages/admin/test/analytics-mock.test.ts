import type { AnalyticsCatalog, AnalyticsSeries, AnalyticsSummary } from "@cicord/client";
import { describe, expect, test } from "vite-plus/test";
import { createMockAnalytics, type MockAnalyticsVariant } from "../src/mock/analytics.ts";

const NOW = Date.parse("2026-11-24T15:30:00Z");

function mock(variant: MockAnalyticsVariant = "full") {
  const a = createMockAnalytics({ variant, server: "s", now: () => NOW });
  const get = (kind: "summary" | "series", query: string): unknown => {
    const r = a[kind](new URL(`http://x/?${query}`));
    if (r.status !== 200) throw new Error(r.message);
    return r.body;
  };
  return {
    a,
    summary: (query: string) => get("summary", query) as AnalyticsSummary,
    series: (query: string) => get("series", query) as AnalyticsSeries,
  };
}

describe("mock analytics", () => {
  test("summary: calendar window, KPIs in contract order, rankings, heatmap", () => {
    const { summary } = mock();
    const s = summary("period=30d");
    expect(s.server).toBe("s");
    expect(s.to).toBe("2026-11-25T00:00:00Z");
    expect(s.from).toBe("2026-10-26T00:00:00Z");
    expect(s.kpis.map((k) => k.metric)).toEqual([
      "members.total",
      "members.joined",
      "members.left",
      "members.net",
      "members.active",
      "messages.sent",
      "voice.seconds",
      "members.online",
      "reactions.added",
      "commands.used",
      "moderation.actions",
    ]);
    const kpi = (m: string) => s.kpis.find((k) => k.metric === m)!;
    expect(kpi("members.net").value).toBe(kpi("members.joined").value - kpi("members.left").value);
    expect(kpi("members.total").value).toBeGreaterThan(3000);
    expect(kpi("members.joined").spark).toHaveLength(30);
    expect(kpi("messages.sent").previous).not.toBeNull();
    expect(Object.keys(s.rankings)).toContain("presence.activity");
    expect(s.rankings["messages.sent"]![0]!.dim).toBe("ch-general");
    expect(s.labels["ch-general"]).toEqual({ name: "général", kind: "channel" });
    expect(s.heatmap).toHaveLength(7);
    expect(s.heatmap[0]).toHaveLength(24);
    // Evening peak (20:00 UTC) beats the night (05:00 UTC).
    expect(s.heatmap[2]![20]!).toBeGreaterThan(s.heatmap[2]![5]! * 3);
    expect(s.status).toMatchObject({ enabled: true, presence: true, missing: [] });
  });

  test("365d: the previous year is not covered", () => {
    const s = mock().summary("period=365d");
    expect(s.kpis.every((k) => k.previous === null)).toBe(true);
    expect(s.rankings["messages.sent"]!.every((r) => r.previous === null)).toBe(true);
  });

  test("series: total first, top dims, other; counters zero-filled, aligned outward", () => {
    const { series } = mock();
    const r = series(
      "metrics=messages.sent,members.total&from=2026-11-01T10:00:00Z&to=2026-11-24T12:00:00Z&step=day&top=3",
    );
    expect(r.from).toBe("2026-11-01T00:00:00Z");
    expect(r.to).toBe("2026-11-25T00:00:00Z");
    const msgs = r.series.filter((x) => x.metric === "messages.sent");
    expect(msgs.map((x) => x.dim)).toEqual(["", "ch-general", "ch-memes", "ch-mc", "other"]);
    expect(msgs[0]!.points).toHaveLength(24);
    // total = top dims + other
    const at = (i: number) => msgs.slice(1).reduce((a, x) => a + x.points[i]![1], 0);
    expect(at(3)).toBeCloseTo(msgs[0]!.points[3]![1], 5);
    expect(r.labels["ch-memes"]?.name).toBe("memes");
    expect(r.series.find((x) => x.metric === "members.total")!.points.length).toBe(24);
  });

  test("series validation: 2000 points, 400 days, unknown metric", () => {
    const { a } = mock();
    const q = (s: string) => a.series(new URL(`http://x/?${s}`));
    expect(
      q("metrics=messages.sent&from=2026-01-01T00:00:00Z&to=2026-11-01T00:00:00Z&step=hour"),
    ).toMatchObject({ status: 400 });
    expect(
      q("metrics=messages.sent&from=2025-01-01T00:00:00Z&to=2026-11-01T00:00:00Z"),
    ).toMatchObject({ status: 400 });
    expect(q("metrics=nope")).toMatchObject({ status: 400 });
    expect(q("metrics=messages.sent")).toMatchObject({ status: 200 });
  });

  test("noPresence: missing entries, no presence ranking, short history", () => {
    const { summary } = mock("noPresence");
    const s = summary("period=90d");
    expect(s.status.presence).toBe(false);
    expect(s.status.missing.flatMap((m) => m.metrics)).toEqual([
      "presence.status",
      "presence.platform",
      "presence.activity",
      "moderation.actions",
    ]);
    expect(s.status.since).toBe("2026-10-11T00:00:00Z");
    expect(s.rankings["presence.activity"]).toBeUndefined();
    expect(s.rankings["moderation.actions"]).toEqual([]);
    expect(s.kpis.find((k) => k.metric === "members.joined")!.previous).toBeNull();
  });

  test("empty and disabled servers", () => {
    const empty = mock("empty").summary("period=7d");
    expect(empty.status).toMatchObject({ enabled: true, since: null });
    expect(empty.kpis.every((k) => k.value === 0 && k.previous === null)).toBe(true);
    expect(empty.rankings["messages.sent"]).toEqual([]);
    const disabled = createMockAnalytics({ variant: "disabled" }).catalog();
    expect(disabled.status).toBe(200);
    const body = (disabled as { body: AnalyticsCatalog }).body;
    expect(body.status.enabled).toBe(false);
    expect(body.metrics.every((x) => !x.collected)).toBe(true);
  });
});
