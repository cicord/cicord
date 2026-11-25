import {
  createCicordClient,
  type AnalyticsSeries,
  type AnalyticsSummary,
  type FetchLike,
} from "@cicord/client";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { niceTicks } from "../src/components/charts.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import {
  bucketsOf,
  deltaOf,
  fineStepFor,
  formatDuration,
  formatNumber,
  formatRange,
  fromLocalInput,
  groupByBot,
  parseActivity,
  parseBotCommand,
  rangeProblem,
  splitLinks,
  stepFor,
  toLocalInput,
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
    `${import.meta.env.BASE_URL.replace(/\/$/, "")}/servers/pixel-haven/analytics${path}`,
  );
  render(<App config={config} />);
  const u = userEvent.setup();
  await u.click(await screen.findByRole("button", { name: user }, { timeout: 3000 }));
  await screen.findByRole("heading", { level: 1, name: "Analytics" });
  /** The mock's summary for a period ("7d") or a query ("from=…&to=…"). */
  const summary = async (query: string) =>
    (await (
      await runtime.fetch(
        `/api/v1/servers/pixel-haven/analytics/summary?${query.includes("=") ? query : `period=${query}`}`,
        {},
      )
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
    expect(kpiValue(region, "Human members")).toBe(formatNumber(value("members.total"), "en"));
    expect(kpiValue(region, "Bots")).toBe(formatNumber(value("members.bots"), "en"));
    expect(kpiValue(region, "Joins")).toBe(formatNumber(value("members.joined"), "en"));
    expect(kpiValue(region, "Voice time")).toBe(formatDuration(value("voice.seconds"), "en"));
    expect(within(region).getAllByRole("term")).toHaveLength(12);
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

  test("custom range: a date and time picker, kept in the URL, sent as UTC", async () => {
    const { user, summary } = await open();
    await kpis();
    await user.click(screen.getByRole("button", { name: "Custom" }));
    const dialog = await screen.findByRole("dialog", { name: "Custom range" });
    const from = within(dialog).getByLabelText("From");
    const to = within(dialog).getByLabelText("To");
    expect(from.getAttribute("type")).toBe("datetime-local");
    // Starts from the window on screen (30 days).
    expect((from as HTMLInputElement).value).toBe(toLocalInput("2026-10-26T00:00:00Z"));
    fireEvent.change(from, { target: { value: "2026-11-23T10:00" } });
    fireEvent.change(to, { target: { value: "2026-11-22T10:00" } });
    await user.click(within(dialog).getByRole("button", { name: "Apply" }));
    expect(within(dialog).getByRole("alert").textContent).toBe("The end must be after the start.");
    expect(to.getAttribute("aria-invalid")).toBe("true");
    expect(from.getAttribute("aria-invalid")).toBeNull();
    expect(window.location.search).toBe("");

    fireEvent.change(to, { target: { value: "2026-11-24T08:00" } });
    await user.click(within(dialog).getByRole("button", { name: "Apply" }));
    const utc = (local: string) => new Date(local).toISOString().replace(".000Z", "Z");
    const query = `from=${utc("2026-11-23T10:00")}&to=${utc("2026-11-24T08:00")}`;
    expect(window.location.search).toBe(`?${query}`);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const s = await summary(query);
    expect(s).toMatchObject({ period: "custom", step: "hour" });
    await waitFor(async () =>
      expect(kpiValue(await kpis(), "Joins")).toBe(
        formatNumber(s.kpis.find((k) => k.metric === "members.joined")!.value, "en"),
      ),
    );
    const radios = within(screen.getByRole("radiogroup", { name: "Period" })).getAllByRole("radio");
    expect(radios.every((r) => r.getAttribute("aria-checked") === "false")).toBe(true);
    expect(
      screen.getByRole("button", {
        name: `Custom range: ${formatRange(s.from, s.to, "en")} (UTC)`,
      }),
    ).toBeTruthy();
    expect(within(await kpis()).getAllByText(/^vs /).length).toBeGreaterThan(5);

    // Back to a preset.
    await user.click(screen.getByRole("radio", { name: "Last 7 days" }));
    expect(window.location.search).toBe("?period=7d");
    expect(screen.getByRole("button", { name: "Custom" })).toBeTruthy();
  });

  test("custom range: too long, and the runtime's refusal", async () => {
    const { user } = await open(
      "full",
      /Owner/,
      "?from=2026-11-25T00:00:00Z&to=2026-11-26T00:00:00Z",
    );
    expect(await screen.findByText("from must be in the past")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: /^Custom range: / }));
    const dialog = await screen.findByRole("dialog", { name: "Custom range" });
    expect(within(dialog).getByRole("alert").textContent).toBe(
      "The runtime refused this range: from must be in the past",
    );
    // The form starts from the range in the URL.
    expect(within(dialog).getByLabelText<HTMLInputElement>("From").value).toBe(
      toLocalInput("2026-11-25T00:00:00Z"),
    );
    expect(within(dialog).getByLabelText<HTMLInputElement>("To").value).toBe(
      toLocalInput("2026-11-26T00:00:00Z"),
    );
    fireEvent.change(within(dialog).getByLabelText("From"), {
      target: { value: "2025-01-01T00:00" },
    });
    fireEvent.change(within(dialog).getByLabelText("To"), {
      target: { value: "2026-11-01T00:00" },
    });
    await user.click(within(dialog).getByRole("button", { name: "Apply" }));
    expect(within(dialog).getByRole("alert").textContent).toBe("A range covers at most 400 days.");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  test("members over time: humans, or bots on their own axis", async () => {
    const { user } = await open();
    await kpis();
    const card = screen.getByRole("region", { name: "Members over time" });
    expect(
      within(card).getByRole("group", { name: /^Members over time: Human members/ }),
    ).toBeTruthy();
    await user.click(within(card).getByRole("radio", { name: "Bots" }));
    expect(within(card).getByRole("group", { name: /^Members over time: Bots/ })).toBeTruthy();
    await user.click(within(card).getByRole("button", { name: "Table" }));
    const header = within(within(card).getByRole("table")).getAllByRole("columnheader");
    expect(header.map((h) => h.textContent)).toEqual([
      "Date",
      "Human members",
      "Bots",
      "Joins",
      "Leaves",
      "Net growth",
    ]);
  });

  test("bot usage: commands by bot, members per day, rankings, tenure, when", async () => {
    const { user, summary } = await open();
    const s = await summary("30d");
    await kpis();
    const section = screen.getByRole("region", { name: "Bot usage" });
    const over = within(section).getByRole("region", { name: "Bot commands over time" });
    expect(within(over).getByText(/only privately is not seen/)).toBeTruthy();
    await user.click(within(over).getByRole("button", { name: "Table" }));
    const header = within(within(over).getByRole("table")).getAllByRole("columnheader");
    // Grouped by bot, busiest first.
    expect(header.map((h) => h.textContent)).toEqual([
      "Date",
      "Jockie Music",
      "Cicord",
      "Dyno",
      "Warden",
    ]);

    const users = within(section).getByRole("region", { name: "Members using bots per day" });
    expect(within(users).getByText(/a day on average/)).toBeTruthy();

    const top = within(section).getByRole("region", { name: "Top bot commands" });
    const first = within(top).getAllByRole("listitem")[0]!;
    expect(s.rankings["bots.commands"]![0]!.dim).toBe("Jockie Music /play");
    expect(within(first).getByText("/play", { selector: "code" })).toBeTruthy();
    expect(within(first).getByText("Jockie Music")).toBeTruthy();
    const where = within(section).getByRole("region", { name: "Where bots are used" });
    expect(within(where).getAllByRole("listitem")[0]!.textContent).toContain("#bot-commands");
    const components = within(section).getByRole("region", { name: "Buttons and menus by bot" });
    expect(within(components).getAllByRole("listitem")[0]!.textContent).toContain("Cicord");

    const tenure = within(section).getByRole("region", { name: "Tenure of members using Cicord" });
    const buckets = within(tenure).getAllByRole("listitem");
    expect(buckets.map((b) => b.firstElementChild!.textContent)).toEqual([
      "Joined less than a day ago",
      "Joined less than a week ago",
      "Joined less than a month ago",
      "Joined less than a year ago",
      "Joined a year ago or more",
    ]);
    expect(buckets[0]!.textContent).toMatch(/%/);

    const when = within(section).getByRole("region", { name: "When members use bots" });
    expect(within(when).getByRole("group", { name: /^When members use bots/ })).toBeTruthy();
    expect(screen.queryByText("No bot command used in this period")).toBeNull();
  });

  test("bot usage: nothing used in the period", async () => {
    // A week before collection started: every number is zero.
    await open("full", /Owner/, "?from=2025-01-01T00:00:00Z&to=2025-01-08T00:00:00Z");
    await kpis();
    const section = await screen.findByRole("region", { name: "Bot usage" });
    expect(within(section).getByText("No bot command used in this period")).toBeTruthy();
    expect(within(section).queryByRole("region", { name: "Top bot commands" })).toBeNull();
  });

  test("a year has no previous period to compare with", async () => {
    await open("full", /Owner/, "?period=365d");
    const region = await kpis();
    await waitFor(() => expect(within(region).getAllByText("No previous period")).toHaveLength(12));
  });

  test("presence off: the missing banner, explanation and uncollected KPI", async () => {
    await open("noPresence");
    const banner = await screen.findByText("2 groups of metrics are not collected");
    const callout = banner.closest("[role=status]") as HTMLElement;
    expect(within(callout).getByText("presence.activity")).toBeTruthy();
    expect(within(callout).getByText("moderation.actions")).toBeTruthy();
    expect(within(callout).getByText(/lacks the View Audit Log permission/)).toBeTruthy();
    // URLs in the fix are links; the text around them stays.
    const url = "https://discord.com/developers/applications/1100000000000000001/bot";
    const link = within(callout).getByRole("link", { name: url });
    expect(link.getAttribute("href")).toBe(url);
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(callout.textContent).toContain(`Portal (${url} → Privileged Gateway Intents) and`);
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
      expect(within(card).getByText(/^Mon, Oct 26, 00:00: Online humans \d/)).toBeTruthy(),
    );
  });

  test("voice rooms a plugin created: one row, expandable to the rooms", async () => {
    const { user, summary } = await open();
    const s = await summary("30d");
    const group = s.rankings["voice.seconds"]!.find((r) => r.dim === "plugin:main/voice-rooms")!;
    expect(group.count).toBeGreaterThan(1);
    await kpis();
    const card = screen.getByRole("region", { name: "Voice time by channel" });
    const list = within(card).getByRole("list", { name: "Voice time by channel" });
    const rows = within(list).getAllByRole("listitem");
    // Only the busy channels and the group: no row per temporary room.
    expect(rows.length).toBeLessThanOrEqual(10);
    const label = (li: HTMLElement) => li.firstElementChild!.textContent ?? "";
    expect(rows.some((r) => /'s channel/.test(label(r)))).toBe(false);
    const row = rows.find((r) => label(r).startsWith("voice-rooms"))!;
    expect(label(row)).toBe(`voice-rooms${group.count} channels created`);
    const toggle = within(row).getByRole("button", {
      name: "Show the channels created by voice-rooms",
    });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    await user.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const rooms = within(row).getByRole("list", { name: "Channels created by voice-rooms" });
    const names = within(rooms).getAllByRole("listitem").map(label);
    expect(names).toHaveLength(group.children!.length);
    // Deleted rooms keep their names; the rooms still open are not marked.
    expect(names.some((n) => /^.+'s channel \(deleted\)$/.test(n ?? ""))).toBe(true);
    await user.click(toggle);
    expect(within(row).queryByRole("list")).toBeNull();
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

  test("bot commands: split into bot and command, grouped per bot", () => {
    expect(parseBotCommand("Jockie Music /play")).toEqual({
      bot: "Jockie Music",
      command: "/play",
    });
    expect(parseBotCommand("Cicord /ticket open")).toEqual({
      bot: "Cicord",
      command: "/ticket open",
    });
    expect(parseBotCommand("AC/DC /rock")).toEqual({ bot: "AC/DC", command: "/rock" });
    expect(parseBotCommand("Mystery")).toEqual({ bot: "Mystery", command: "" });

    const t0 = Date.UTC(2026, 10, 1) / 1000;
    const buckets = [t0, t0 + 86400, t0 + 2 * 86400];
    const serie = (dim: string, values: (number | null)[]) => ({
      metric: "bots.commands",
      dim,
      points: values.flatMap((v, i) => (v === null ? [] : [[buckets[i]!, v] as [number, number]])),
    });
    const series: AnalyticsSeries = {
      server: "s",
      from: "2026-11-01T00:00:00Z",
      to: "2026-11-04T00:00:00Z",
      step: "day",
      series: [
        serie("", [20, 31, 8]),
        serie("Jockie Music /play", [5, 10, 2]),
        serie("Cicord /ticket open", [4, 6, 1]),
        serie("Jockie Music /skip", [3, 4, 0]),
        serie("Cicord /poll", [2, 2, 1]),
        serie("Dyno /warn", [1, 1, 0]),
        serie("other", [5, 8, 4]),
        // Another metric's dims are ignored.
        { metric: "members.active.bots", dim: "", points: [[t0, 9]] },
      ],
      labels: {},
      status: { enabled: true, presence: true, since: null, retentionDays: 400, missing: [] },
    };
    expect(groupByBot(series, buckets, "bots.commands")).toEqual([
      { bot: "Jockie Music", values: [8, 14, 2] },
      { bot: "Cicord", values: [6, 8, 2] },
      { bot: "Dyno", values: [1, 1, 0] },
      { bot: "other", values: [5, 8, 4] },
    ]);
    // Bots beyond the limit join "other".
    expect(groupByBot(series, buckets, "bots.commands", 1)).toEqual([
      { bot: "Jockie Music", values: [8, 14, 2] },
      { bot: "other", values: [12, 17, 6] },
    ]);
    expect(groupByBot(series, buckets, "messages.sent")).toEqual([]);
  });

  test("links in text: http(s) URLs, trailing punctuation and unbalanced parentheses excluded", () => {
    expect(splitLinks("no link here")).toEqual([{ text: "no link here" }]);
    expect(
      splitLinks("Portal (https://discord.com/developers/applications/1/bot → Intents)."),
    ).toEqual([
      { text: "Portal (" },
      {
        text: "https://discord.com/developers/applications/1/bot",
        href: "https://discord.com/developers/applications/1/bot",
      },
      { text: " → Intents)." },
    ]);
    expect(splitLinks("See https://cicord.dev/docs/x. Then (http://a.b/c).")).toEqual([
      { text: "See " },
      { text: "https://cicord.dev/docs/x", href: "https://cicord.dev/docs/x" },
      { text: ". Then (" },
      { text: "http://a.b/c", href: "http://a.b/c" },
      { text: ")." },
    ]);
    expect(splitLinks("https://en.wikipedia.org/wiki/Foo_(bar), ok")).toEqual([
      {
        text: "https://en.wikipedia.org/wiki/Foo_(bar)",
        href: "https://en.wikipedia.org/wiki/Foo_(bar)",
      },
      { text: ", ok" },
    ]);
    expect(splitLinks("ftp://x.y and http://.")).toEqual([{ text: "ftp://x.y and http://." }]);
  });

  test("custom range: checks, local inputs, steps", () => {
    const now = Date.parse("2026-11-24T15:30:00Z");
    const d = (iso: string) => new Date(iso);
    expect(rangeProblem(null, d("2026-11-02T00:00:00Z"), now)).toBe("missing");
    expect(rangeProblem(d("2026-11-02T00:00:00Z"), d("2026-11-01T00:00:00Z"), now)).toBe("order");
    expect(rangeProblem(d("2026-11-25T00:00:00Z"), d("2026-11-26T00:00:00Z"), now)).toBe("future");
    expect(rangeProblem(d("2025-01-01T00:00:00Z"), d("2026-11-01T00:00:00Z"), now)).toBe("tooLong");
    // 400 days exactly, and a few minutes (one hour once rounded), are fine.
    expect(rangeProblem(d("2025-10-20T00:00:00Z"), d("2026-11-24T00:00:00Z"), now)).toBeNull();
    expect(rangeProblem(d("2026-11-20T10:10:00Z"), d("2026-11-20T10:11:00Z"), now)).toBeNull();
    expect(fromLocalInput("")).toBeNull();
    expect(fromLocalInput("2026-11-20T10:30")?.getTime()).toBe(
      new Date(2026, 10, 20, 10, 30).getTime(),
    );
    expect(toLocalInput(new Date(2026, 10, 20, 9, 5))).toBe("2026-11-20T09:05");
    // Presets keep their steps: 7d and 30d daily (gauges hourly), 90d daily, a year weekly.
    expect([7, 30, 90, 365].map(stepFor)).toEqual(["day", "day", "day", "week"]);
    expect([7, 30, 90, 365].map(fineStepFor)).toEqual(["hour", "hour", "day", "day"]);
    expect([1, 1.99, 2, 119, 120].map(stepFor)).toEqual(["hour", "hour", "day", "day", "week"]);
    expect(formatRange("2026-11-03T00:00:00Z", "2026-11-05T00:00:00Z", "en")).toMatch(
      /^Nov 3\s–\s4/,
    );
    expect(formatRange("2026-11-03T14:00:00Z", "2026-11-04T10:00:00Z", "en")).toMatch(
      /14:00.*10:00/,
    );
  });

  test("integer ticks: whole numbers, no duplicates, no 2.5 at magnitude 1", () => {
    const int = (min: number, max: number, count = 4) =>
      niceTicks(min, max, count, { integer: true });
    expect(niceTicks(1, 2)).toEqual([1, 1.25, 1.5, 1.75, 2]);
    expect(int(1, 2)).toEqual([1, 2]);
    expect(int(0, 2)).toEqual([0, 1, 2]);
    expect(int(0, 1)).toEqual([0, 1]);
    expect(int(0, 0.4)).toEqual([0, 1]);
    expect(int(0, 10)).toEqual([0, 5, 10]);
    expect(int(0, 7)).toEqual([0, 2, 4, 6, 8]);
    expect(int(1180, 1182)).toEqual([1180, 1181, 1182]);
    expect(int(-3, 2, 3)).toEqual([-4, -2, 0, 2]);
    // Larger ranges keep the usual round steps (25 is whole at magnitude 10).
    expect(int(0, 100)).toEqual([0, 25, 50, 75, 100]);
    expect(int(0, 417)).toEqual([0, 200, 400, 600]);
    expect(int(-23, 41, 3)).toEqual([-25, 0, 25, 50]);
    for (const [a, b] of [
      [0, 3],
      [2, 9],
      [0, 13],
      [40, 47],
      [0, 0.2],
    ] as const) {
      const ticks = int(a, b);
      expect(ticks.every(Number.isInteger)).toBe(true);
      expect(new Set(ticks).size).toBe(ticks.length);
      expect(ticks[0]!).toBeLessThanOrEqual(a);
      expect(ticks.at(-1)!).toBeGreaterThanOrEqual(b);
    }
  });
});
