import { createCicordClient, type AnalyticsSummary, type FetchLike } from "@cicord/client";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { niceTicks } from "../src/components/charts.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import {
  bucketsOf,
  deltaOf,
  formatDuration,
  formatNumber,
  parseActivity,
} from "../src/lib/analytics.ts";
import type { MockAnalyticsVariant } from "../src/mock/analytics.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";

const NOW = Date.parse("2026-11-24T15:30:00Z");

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

async function open(analytics: MockAnalyticsVariant = "full", user = /Owner/, path = "") {
  const runtime = createMockRuntime({ latency: 0, persist: false, analytics, now: () => NOW });
  const fetch: FetchLike = (input, init) => runtime.fetch(input, init);
  const config: AdminConfig = {
    client: createCicordClient({ fetch: watch401(fetch) }),
    loginUrl: "/auth/login",
    mock: true,
  };
  window.history.replaceState(
    null,
    "",
    `${import.meta.env.BASE_URL.replace(/\/$/, "")}/servers/ma-commu/analytics${path}`,
  );
  render(<App config={config} />);
  const u = userEvent.setup();
  await u.click(await screen.findByRole("button", { name: user }, { timeout: 3000 }));
  await screen.findByRole("heading", { level: 1, name: "Analytics" });
  const summary = async (period: string) =>
    (await (
      await runtime.fetch(`/api/v1/servers/ma-commu/analytics/summary?period=${period}`, {})
    ).json()) as AnalyticsSummary;
  return { user: u, summary };
}

const kpis = async () => screen.findByRole("region", { name: "Headline numbers" });

/** The value shown under a KPI label. */
function kpiValue(region: HTMLElement, label: string): string {
  const term = within(region).getByText(label, { selector: "dt" });
  return term.nextElementSibling?.firstElementChild?.textContent ?? "";
}

describe("analytics page", () => {
  test("KPIs with their change against the previous period", async () => {
    const { summary } = await open();
    const s = await summary("30d");
    const region = await kpis();
    const value = (m: string) => s.kpis.find((k) => k.metric === m)!.value;
    expect(kpiValue(region, "Members")).toBe(formatNumber(value("members.total"), "en"));
    expect(kpiValue(region, "Joins")).toBe(formatNumber(value("members.joined"), "en"));
    expect(kpiValue(region, "Voice time")).toBe(formatDuration(value("voice.seconds"), "en"));
    expect(within(region).getAllByRole("term")).toHaveLength(11);
    expect(within(region).getAllByText(/^vs /).length).toBeGreaterThan(5);
    expect(screen.getByText(/Collected since Oct 21, 2025/)).toBeTruthy();
    // Sections, rankings with custom emoji images, presence.
    expect(screen.getByRole("heading", { level: 2, name: "Engagement" })).toBeTruthy();
    expect(screen.getByText(":pepe_hype:")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Statuses" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Top activities" })).toBeTruthy();
    expect(screen.queryByText(/not collected/)).toBeNull();
  });

  test("switching the period reloads every number", async () => {
    const { user, summary } = await open();
    const s7 = await summary("7d");
    await kpis();
    await user.click(screen.getByRole("radio", { name: "Last 7 days" }));
    expect(window.location.search).toBe("?period=7d");
    await waitFor(async () =>
      expect(kpiValue(await kpis(), "Joins")).toBe(
        formatNumber(s7.kpis.find((k) => k.metric === "members.joined")!.value, "en"),
      ),
    );
    expect(screen.getByRole("radio", { name: "Last 7 days" }).getAttribute("aria-checked")).toBe(
      "true",
    );
  });

  test("a year has no previous period to compare with", async () => {
    await open("full", /Owner/, "?period=365d");
    const region = await kpis();
    await waitFor(() => expect(within(region).getAllByText("No previous period")).toHaveLength(11));
  });

  test("presence off: the missing banner, explanation and uncollected KPI", async () => {
    await open("noPresence");
    const banner = await screen.findByText("2 groups of metrics are not collected");
    const callout = banner.closest("[role=status]") as HTMLElement;
    expect(within(callout).getByText("presence.activity")).toBeTruthy();
    expect(within(callout).getByText("moderation.actions")).toBeTruthy();
    expect(within(callout).getByText(/lacks the View Audit Log permission/)).toBeTruthy();
    expect(screen.getByText("Detailed presence is off")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Statuses" })).toBeNull();
    const region = await kpis();
    expect(within(region).getByText("Not collected")).toBeTruthy();
  });

  test("a server without data yet, and with analytics off", async () => {
    await open("empty");
    expect(await screen.findByText("No activity recorded yet")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Headline numbers" })).toBeNull();
  });

  test("analytics turned off", async () => {
    await open("disabled");
    expect(await screen.findByText("Analytics are off for this server")).toBeTruthy();
    expect(screen.getByText("spec.analytics.enabled: true")).toBeTruthy();
  });

  test("table view and keyboard readout of a chart", async () => {
    const { user } = await open();
    await kpis();
    const card = screen.getByRole("region", { name: "Online members" });
    await user.click(within(card).getByRole("button", { name: "Table" }));
    const table = within(card).getByRole("table");
    expect(within(table).getAllByRole("row").length).toBeGreaterThan(24 * 30);
    expect(
      within(card).getByRole("button", { name: "Hide table" }).getAttribute("aria-expanded"),
    ).toBe("true");
    const chart = within(card).getByRole("group", { name: /Online members/ });
    fireEvent.focus(chart);
    fireEvent.keyDown(chart, { key: "Home" });
    await waitFor(() =>
      expect(within(card).getByText(/^Mon, Oct 26, 00:00: Online \d/)).toBeTruthy(),
    );
  });

  test("moderators see the page too", async () => {
    await open("full", /Mina/);
    expect(await kpis()).toBeTruthy();
    const nav = screen.getByRole("complementary", { name: "Main navigation" });
    expect(within(nav).getByRole("link", { name: "Analytics" })).toBeTruthy();
  });
});

describe("analytics helpers", () => {
  test("delta polarity: leaves up is bad, moderation is neutral, no previous is none", () => {
    expect(deltaOf(120, 100)).toMatchObject({ kind: "change", tone: "good" });
    expect(deltaOf(120, 100, "down")).toMatchObject({ kind: "change", tone: "bad" });
    expect(deltaOf(80, 100, "down")).toMatchObject({ kind: "change", tone: "good" });
    expect(deltaOf(120, 100, "neutral")).toMatchObject({ tone: "neutral" });
    expect(deltaOf(5, null)).toEqual({ kind: "none" });
    expect(deltaOf(5, 0)).toEqual({ kind: "new" });
    expect(deltaOf(-10, -20)).toMatchObject({ ratio: 0.5, tone: "good" });
  });

  test("buckets, durations, ticks, activities", () => {
    expect(
      bucketsOf({ from: "2026-11-02T00:00:00Z", to: "2026-11-16T00:00:00Z", step: "week" }),
    ).toEqual([Date.UTC(2026, 10, 2) / 1000, Date.UTC(2026, 10, 9) / 1000]);
    expect(formatDuration(5_605_200, "en")).toBe("1,557 hr");
    expect(formatDuration(1800, "en")).toBe("30 min");
    expect(niceTicks(0, 417)).toEqual([0, 200, 400, 600]);
    expect(niceTicks(3800, 4384)).toEqual([3800, 4000, 4200, 4400]);
    expect(niceTicks(-23, 41, 3)).toEqual([-25, 0, 25, 50]);
    expect(parseActivity("listening:Spotify")).toEqual({ type: "listening", name: "Spotify" });
  });
});
