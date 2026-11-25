/**
 * Generated analytics of the mock runtime (GET /api/v1/servers/{server}/analytics/…, the
 * contract of internal/analytics/api.go): ~400 days of hourly aggregates computed on demand
 * from a seeded hash, never stored (sessionStorage could not hold them). A growing community
 * with weekly seasonality and evening peaks (UTC), a partnership that brings a wave of joins,
 * a raid that ends in bans, a few busy channels, custom emojis, invites, commands, voice and
 * presence, and the bots members use (Cicord's own, a music bot, a moderation bot).
 *
 * Variants: `full` (everything collected), `noPresence` (no privileged presence intent and
 * no ViewAuditLog permission: `status.missing`, collection started 45 days ago), `empty`
 * (enabled, no point yet) and `disabled` (`status.enabled: false`).
 */
import type {
  AnalyticsCatalogEntry,
  AnalyticsKpi,
  AnalyticsLabel,
  AnalyticsMissing,
  AnalyticsPeriod,
  AnalyticsPoint,
  AnalyticsRanked,
  AnalyticsSerie,
  AnalyticsStatus,
  AnalyticsStep,
} from "@cicord/client";

export type MockAnalyticsVariant = "full" | "noPresence" | "empty" | "disabled";

export const MOCK_ANALYTICS_VARIANTS: readonly MockAnalyticsVariant[] = [
  "full",
  "noPresence",
  "empty",
  "disabled",
];

const HOUR = 3600;
const DAY = 86400;
/** Days of history in the full variant (retention). */
const DAYS = 400;
const BASE_MEMBERS = 1180;

type Kind = "counter" | "max" | "last" | "daily";
type Profile = "chat" | "voice" | "flat" | "joins";

interface Dim {
  id: string;
  weight: number;
  /** First and last day index (0 = oldest day) the dimension has data. */
  from?: number;
  until?: number;
  label?: AnalyticsLabel;
}

interface MetricDef {
  name: string;
  kind: Kind;
  /** Catalog dimension name ("channel", "emoji"…). */
  dim?: string;
  unit: "count" | "members" | "seconds" | "level";
  source: string;
  presence?: boolean;
}

// ───────────────────────────────────────────────────────────── dimensions

const D = DAYS - 1;
/** A partnership with another server: a wave of joins through one invite. */
const PARTNER_DAY = D - 75;
/** A raid of fresh accounts, kicked and banned the next day. */
const RAID_DAY = D - 200;
/** #league-of-legends opened (and the LoL role and activity). */
const LOL_DAY = D - 180;

const ch = (id: string, name: string, weight: number, extra: Partial<Dim> = {}): Dim => ({
  id,
  weight,
  label: { name, kind: "channel", ...(extra.until !== undefined ? { deleted: true } : {}) },
  ...extra,
});

const TEXT: Dim[] = [
  ch("ch-general", "general", 30),
  ch("ch-memes", "memes", 14),
  ch("ch-mc", "minecraft", 11),
  ch("ch-valo", "valorant", 10),
  ch("ch-lfg", "looking-for-group", 8),
  ch("ch-offtopic", "off-topic", 7),
  ch("ch-lol", "league-of-legends", 6, { from: LOL_DAY }),
  ch("ch-clips", "clips", 6),
  ch("ch-help", "help", 5),
  ch("ch-staff", "staff", 4),
  ch("ch-events-2025", "events-2025", 5, { until: D - 150 }),
  ch("ch-announcements", "announcements", 0.6),
  ch("ch-rules", "rules", 0.1),
];

const vc = (id: string, name: string, weight: number): Dim => ({
  id,
  weight,
  label: { name, kind: "voice" },
});

const VOICE: Dim[] = [
  vc("ch-vgame1", "Gaming 1", 28),
  vc("ch-voice", "Lounge", 24),
  vc("ch-vchill", "Chill", 20),
  vc("ch-vgame2", "Gaming 2", 15),
  vc("ch-vmusic", "Music", 8),
  vc("ch-vstudy", "Focus", 5),
];
const STAGE: Dim[] = [{ id: "ch-stage", weight: 1, label: { name: "Stage", kind: "voice" } }];

function emojiImage(text: string, color: string): string {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="9" fill="${color}"/><text x="16" y="21" font-family="system-ui,sans-serif" font-size="13" font-weight="700" fill="#fff" text-anchor="middle">${text}</text></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

const custom = (name: string, id: string, weight: number, text: string, color: string): Dim => ({
  id: `${name}:${id}`,
  weight,
  label: { name, kind: "emoji", imageUrl: emojiImage(text, color) },
});

const EMOJIS: Dim[] = [
  { id: "😂", weight: 22 },
  { id: "👍", weight: 18 },
  custom("pepe_hype", "1180000000000000001", 14, "PH", "#2f9e44"),
  { id: "❤️", weight: 12 },
  { id: "🔥", weight: 10 },
  custom("gg", "1180000000000000002", 9, "GG", "#5865f2"),
  { id: "👀", weight: 7 },
  custom("pixelhaven", "1180000000000000003", 6, "PX", "#e8590c"),
  { id: "🎉", weight: 5 },
  custom("kekw", "1180000000000000004", 4, "KW", "#c2255c"),
  { id: "😭", weight: 3 },
  {
    id: "old_logo:1180000000000000005",
    weight: 2,
    until: D - 120,
    label: {
      name: "old_logo",
      kind: "emoji",
      deleted: true,
      imageUrl: emojiImage("OL", "#868e96"),
    },
  },
];

const invite = (code: string, weight: number, extra: Partial<Dim> = {}): Dim => ({
  id: code,
  weight,
  label: { name: code, kind: "invite" },
  ...extra,
});

const INVITES: Dim[] = [
  invite("pixelhaven", 40),
  invite("x7Kp2Qa", 22),
  invite("yt-desc", 12),
  invite("tiktok", 14, { from: D - 250 }),
  invite("partner-gg", 0.4, { from: PARTNER_DAY - 3 }),
  invite("zz9Rk1d", 0, { from: RAID_DAY, until: RAID_DAY }),
  invite("Fq83nLm", 4),
];

const role = (id: string, name: string, color: string, weight: number, from?: number): Dim => ({
  id,
  weight,
  label: { name, kind: "role", color },
  ...(from !== undefined ? { from } : {}),
});

const ROLES: Dim[] = [
  role("r-member", "Member", "#3498db", 90),
  role("r-pc", "PC", "#95a5a6", 48),
  role("r-ping", "Event Pings", "#f1c40f", 34),
  role("r-mc", "Minecraft", "#5b8731", 30),
  role("r-valo", "Valorant", "#ff4655", 26),
  role("r-mobile", "Mobile", "#1abc9c", 24),
  role("r-console", "Console", "#9b59b6", 19),
  role("r-lol", "League of Legends", "#c89b3c", 16, LOL_DAY),
];

const plain = (ids: [string, number][]): Dim[] => ids.map(([id, weight]) => ({ id, weight }));

const COMMANDS = plain([
  ["rank", 30],
  ["play", 21],
  ["onboarding", 14],
  ["ticket", 9],
  ["poll", 7],
  ["help", 5],
  ["giveaway", 4],
  ["leaderboard", 4],
  ["welcome-preview", 0.6],
  ["remind", 3],
  ["8ball", 2],
]);

/** Commands of every bot of the server, "<bot> /<command>" (Cicord and Warden run on Cicord). */
const BOT_COMMANDS = plain([
  ["Jockie Music /play", 28],
  ["Cicord /rank", 18],
  ["Cicord /ticket open", 12],
  ["Jockie Music /skip", 12],
  ["Cicord /poll", 8],
  ["Jockie Music /queue", 6],
  ["Warden /verify", 6],
  ["Cicord /onboarding", 5],
  ["Dyno /warn", 4],
  ["Cicord /giveaway", 3],
  ["Jockie Music /stop", 3],
  ["Dyno /timeout", 2],
  ["Dyno /ban", 0.6],
]);
/** Share of the bot commands that are Cicord's bots' (the only ones with a member tenure). */
const CICORD_SHARE =
  BOT_COMMANDS.filter((c) => /^(Cicord|Warden) \//.test(c.id)).reduce((a, c) => a + c.weight, 0) /
  BOT_COMMANDS.reduce((a, c) => a + c.weight, 0);

const BOT_CHANNELS: Dim[] = [
  ch("ch-bots", "bot-commands", 34),
  ch("ch-music", "music", 24),
  ch("ch-general", "general", 12),
  ch("ch-help", "help", 10),
  ch("ch-verify", "verify", 6),
  ch("ch-staff", "staff", 5),
  ch("ch-valo", "valorant", 3),
  ch("ch-mc", "minecraft", 2),
];

/** Time since the member joined, for Cicord's commands: newcomers use the bots most. */
const TENURES = plain([
  ["<1d", 15],
  ["<7d", 22],
  ["<30d", 25],
  ["<1y", 24],
  [">=1y", 14],
]);

/** Buttons and select menus, per Cicord bot. */
const COMPONENT_BOTS = plain([
  ["Cicord", 76],
  ["Warden", 24],
]);

const MOD_ACTIONS = plain([
  ["message_delete", 44],
  ["timeout", 20],
  ["role_update", 14],
  ["kick", 8],
  ["ban", 7],
  ["untimeout", 5],
  ["unban", 1],
  ["prune", 0.2],
]);

const rule = (id: string, name: string, weight: number): Dim => ({
  id,
  weight,
  label: { name, kind: "label" },
});
const AUTOMOD = [
  rule("1190000000000000001", "Spam links", 46),
  rule("1190000000000000002", "Blocked words", 34),
  rule("1190000000000000003", "Mention spam", 14),
  rule("1190000000000000004", "Invite links", 6),
];

const AGES = plain([
  ["<1d", 4],
  ["<7d", 6],
  ["<30d", 11],
  ["<1y", 24],
  [">=1y", 55],
]);

const STATUSES = plain([
  ["online", 57],
  ["idle", 28],
  ["dnd", 15],
]);
const PLATFORMS = plain([
  ["desktop", 56],
  ["mobile", 41],
  ["web", 8],
]);
const ACTIVITIES: Dim[] = [
  { id: "listening:Spotify", weight: 12 },
  { id: "playing:Minecraft", weight: 9 },
  { id: "playing:VALORANT", weight: 7 },
  { id: "playing:League of Legends", weight: 5, from: LOL_DAY },
  { id: "playing:Lethal Company", weight: 6, from: D - 130, until: D - 50 },
  { id: "watching:YouTube", weight: 3 },
  { id: "playing:Fortnite", weight: 2.5 },
  { id: "playing:Counter-Strike 2", weight: 2.2 },
  { id: "playing:Rocket League", weight: 1.4 },
  { id: "streaming:Twitch", weight: 0.8 },
  { id: "playing:Stardew Valley", weight: 1.1 },
  { id: "competing:Clash", weight: 0.4 },
];

const EVENTS: Dim[] = [
  {
    id: "1200000000000000001",
    weight: 1,
    from: D - 45,
    until: D - 30,
    label: { name: "Valorant Tournament", kind: "event" },
  },
  {
    id: "1200000000000000002",
    weight: 1,
    from: D - 10,
    label: { name: "Board Game Night", kind: "event" },
  },
];

const DIMS: Record<string, Dim[]> = {
  "members.joined.age": AGES,
  "members.roles.added": ROLES,
  "members.roles.removed": ROLES,
  "invites.uses": INVITES,
  "messages.sent": TEXT,
  "messages.sent.bots": TEXT,
  "messages.edited": TEXT,
  "messages.deleted": TEXT,
  "messages.attachments": TEXT,
  "messages.replies": TEXT,
  "reactions.added": EMOJIS,
  "threads.created": TEXT,
  "polls.created": TEXT,
  "voice.seconds": VOICE,
  "voice.sessions": VOICE,
  "voice.streaming": VOICE,
  "stage.seconds": STAGE,
  "events.interested": EVENTS,
  "moderation.actions": MOD_ACTIONS,
  "automod.triggers": AUTOMOD,
  "commands.used": COMMANDS,
  "components.used": COMPONENT_BOTS,
  "bots.commands": BOT_COMMANDS,
  "bots.commands.channels": BOT_CHANNELS,
  "bots.commands.tenure": TENURES,
  "presence.status": STATUSES,
  "presence.platform": PLATFORMS,
  "presence.activity": ACTIVITIES,
};

const LABELS: Record<string, AnalyticsLabel> = Object.fromEntries(
  Object.values(DIMS)
    .flat()
    .filter((d) => d.label)
    .map((d) => [d.id, d.label!]),
);

/** Per-channel ratios of the message sub-metrics (attachments, threads, polls). */
const ATTACHMENTS: Record<string, number> = { "ch-clips": 0.62, "ch-memes": 0.48 };
const THREADS: Record<string, number> = { "ch-help": 0.035, "ch-lfg": 0.012 };
const POLLS: Record<string, number> = { "ch-general": 0.0016, "ch-valo": 0.002, "ch-mc": 0.002 };
const BOTS: Record<string, number> = {
  "ch-announcements": 0.8,
  "ch-general": 0.06,
  "ch-staff": 0.3,
  "ch-lfg": 0.04,
};
const STREAMING: Record<string, number> = {
  "ch-vgame1": 0.2,
  "ch-vgame2": 0.16,
  "ch-vchill": 0.05,
};

// ───────────────────────────────────────────────────────────── catalog

const m = (
  name: string,
  kind: Kind,
  unit: MetricDef["unit"],
  source: string,
  dim?: string,
  presence?: boolean,
): MetricDef => ({
  name,
  kind,
  unit,
  source,
  ...(dim ? { dim } : {}),
  ...(presence ? { presence } : {}),
});

const PRESENCE_SOURCE = "presence events (privileged intent)";

/** internal/analytics/catalog.go, in dashboard order. */
const CATALOG: MetricDef[] = [
  m(
    "members.total",
    "last",
    "members",
    "Discord's approximate member count minus the bot members (humans), sampled",
  ),
  m("members.bots", "last", "members", "bot members of the server, sampled"),
  m(
    "members.online",
    "last",
    "members",
    "Discord's approximate online count (online, idle, do not disturb) minus online bots, sampled",
  ),
  m("members.joined", "counter", "count", "member join events"),
  m("members.left", "counter", "count", "member leave events (leaves, kicks and bans)"),
  m(
    "members.joined.age",
    "counter",
    "count",
    "member join events: age of the Discord account",
    "account age",
  ),
  m("members.joined.bots", "counter", "count", "bot accounts added"),
  m("members.roles.added", "counter", "count", "member update events", "role"),
  m("members.roles.removed", "counter", "count", "member update events", "role"),
  m("members.boosting", "last", "count", "server boosts, sampled"),
  m("server.boost.tier", "last", "level", "server boost level, sampled"),
  m(
    "members.active",
    "daily",
    "members",
    "distinct members active in the day (message, reaction, voice or command)",
  ),
  m("members.active.chat", "daily", "members", "distinct message authors in the day"),
  m("members.active.voice", "daily", "members", "distinct members in voice in the day"),
  m(
    "invites.uses",
    "counter",
    "count",
    "invite use counts compared before and after each join",
    "invite",
  ),
  m("invites.created", "counter", "count", "invite create events"),
  m("onboarding.completed", "counter", "count", "member updates: Discord onboarding completed"),
  m("messages.sent", "counter", "count", "message events (members, not bots)", "channel"),
  m("messages.sent.bots", "counter", "count", "message events from bots and webhooks", "channel"),
  m("messages.edited", "counter", "count", "message update events", "channel"),
  m("messages.deleted", "counter", "count", "message delete events", "channel"),
  m("messages.attachments", "counter", "count", "attachments of new messages", "channel"),
  m("messages.replies", "counter", "count", "new messages that reply to another", "channel"),
  m("messages.stickers", "counter", "count", "stickers of new messages"),
  m("reactions.added", "counter", "count", "reaction add events", "emoji"),
  m(
    "threads.created",
    "counter",
    "count",
    "thread create events (dimension: parent channel)",
    "channel",
  ),
  m("polls.created", "counter", "count", "new messages carrying a Discord poll", "channel"),
  m(
    "voice.seconds",
    "counter",
    "seconds",
    "voice state events: time members spend in each voice channel",
    "channel",
  ),
  m(
    "voice.sessions",
    "counter",
    "count",
    "voice state events: joins of a voice channel",
    "channel",
  ),
  m("voice.peak", "max", "members", "voice state events: most members in voice at once"),
  m(
    "voice.streaming",
    "counter",
    "seconds",
    "voice state events: time streaming or with the camera on",
    "channel",
  ),
  m("stage.seconds", "counter", "seconds", "voice state events in stage channels", "channel"),
  m(
    "events.interested",
    "last",
    "members",
    "scheduled events: interested members, sampled",
    "event",
  ),
  m("moderation.actions", "counter", "count", "audit log entry events", "action"),
  m("automod.triggers", "counter", "count", "AutoMod action execution events", "rule"),
  m(
    "automod.blocked",
    "counter",
    "count",
    "AutoMod action execution events that blocked a message",
  ),
  m(
    "commands.used",
    "counter",
    "count",
    "application command interactions handled by the runtime",
    "command",
  ),
  m(
    "components.used",
    "counter",
    "count",
    "button and select menu interactions of Cicord's bots",
    "bot",
  ),
  m(
    "bots.commands",
    "counter",
    "count",
    "commands of every bot: Cicord's from their interactions, other bots' from their replies (a command answered only privately, ephemeral, is not seen)",
    "bot command",
  ),
  m(
    "bots.commands.channels",
    "counter",
    "count",
    "channels where bot commands are used (every bot)",
    "channel",
  ),
  m(
    "bots.commands.tenure",
    "counter",
    "count",
    "commands of Cicord's bots by how long the member has been in the server",
    "member tenure",
  ),
  m(
    "members.active.bots",
    "daily",
    "members",
    "distinct members who used a bot command in the day (every bot)",
  ),
  m("presence.status", "last", "members", PRESENCE_SOURCE, "status", true),
  m("presence.platform", "last", "members", PRESENCE_SOURCE, "platform", true),
  m(
    "presence.activity",
    "last",
    "members",
    "presence events: what members are playing, listening to or watching (privileged intent)",
    "activity",
    true,
  ),
];

const BY_NAME = new Map(CATALOG.map((c) => [c.name, c]));

const MISSING_PRESENCE: AnalyticsMissing = {
  metrics: ["presence.status", "presence.platform", "presence.activity"],
  reason:
    "Detailed presence is off: spec.analytics.presence is not set and the bot does not have the Presence privileged intent.",
  fix: "Set spec.analytics.presence: true in the server's YAML, enable Presence Intent for the bot in the Discord Developer Portal (https://discord.com/developers/applications/1100000000000000001/bot → Privileged Gateway Intents) and add ServerPresences to the bot's intents.",
};
const MISSING_AUDIT: AnalyticsMissing = {
  metrics: ["moderation.actions"],
  reason: "The bot cannot read the audit log: it lacks the View Audit Log permission.",
  fix: "Give the bot's role the View Audit Log permission (spec.roles in the server's YAML, or Server Settings → Roles).",
};

// ───────────────────────────────────────────────────────────── noise

function hash(text: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 0x01000193);
  return h >>> 0;
}

/** Uniform [0, 1) from a seed and a counter. */
function rand(seed: number, n: number): number {
  let x = (seed ^ Math.imul(n + 0x6d2b79f5, 0x9e3779b1)) >>> 0;
  x = Math.imul(x ^ (x >>> 16), 0x85ebca6b);
  x = Math.imul(x ^ (x >>> 13), 0xc2b2ae35);
  x ^= x >>> 16;
  return (x >>> 0) / 4294967296;
}

/** An integer whose expectation is `x` (stochastic rounding). */
const roundish = (x: number, r: number) => Math.floor(x + r);

// Hour profiles (UTC; a European community peaks around 20:00 UTC). Each sums to 1.
function bell(h: number, mu: number, sigma: number): number {
  const d = Math.min(Math.abs(h - mu), 24 - Math.abs(h - mu));
  return Math.exp(-(d * d) / (2 * sigma * sigma));
}

function normalized(fn: (h: number) => number): number[] {
  const raw = Array.from({ length: 24 }, (_, h) => fn(h));
  const sum = raw.reduce((a, b) => a + b, 0);
  return raw.map((v) => v / sum);
}

/** PROFILES[profile][weekend ? 1 : 0][hour]. */
const PROFILES: Record<Profile, [number[], number[]]> = {
  chat: [
    normalized((h) => 0.12 + bell(h, 20, 2.6) + 0.35 * bell(h, 12.5, 1.8) - 0.1 * bell(h, 5, 2)),
    normalized((h) => 0.12 + bell(h, 20.5, 3) + 0.6 * bell(h, 15, 2.5) - 0.1 * bell(h, 6, 2)),
  ],
  voice: [
    normalized((h) => 0.03 + bell(h, 21, 2.1) + 0.12 * bell(h, 13, 1.5)),
    normalized((h) => 0.04 + bell(h, 21.5, 2.6) + 0.45 * bell(h, 16, 2.4)),
  ],
  joins: [normalized((h) => 0.3 + bell(h, 19, 3.5)), normalized((h) => 0.35 + bell(h, 17, 4))],
  flat: [normalized((h) => 0.6 + bell(h, 18, 4)), normalized((h) => 0.6 + bell(h, 18, 4))],
};

const SHAPE_MAX = Math.max(...PROFILES.chat[0]);

const WEEKLY: Record<Profile, number[]> = {
  chat: [0.93, 0.9, 0.94, 0.97, 1.04, 1.18, 1.12],
  voice: [0.84, 0.84, 0.9, 0.95, 1.16, 1.36, 1.2],
  joins: [1, 0.98, 1, 1, 1.05, 1.14, 1.1],
  flat: [1, 1, 1, 1, 1, 1, 1],
};

// ───────────────────────────────────────────────────────────── model

export interface MockAnalyticsOptions {
  variant?: MockAnalyticsVariant;
  /** Server key reported in the responses. */
  server?: string;
  /** Clock (ms), for deterministic tests. */
  now?: () => number;
}

interface Window {
  /** Unix seconds, [from, to). */
  from: number;
  to: number;
}

export function createMockAnalytics(options: MockAnalyticsOptions = {}) {
  const variant = options.variant ?? "full";
  const clock = options.now ?? Date.now;
  let cached: ReturnType<typeof buildModel> | undefined;
  let builtFor = -1;
  /** The model is anchored on today: rebuilt when the UTC day changes. */
  const current = () => {
    const nowSec = Math.floor(clock() / 1000);
    const today = Math.floor(nowSec / DAY);
    if (!cached || builtFor !== today) {
      cached = buildModel(variant, today, options.server ?? "");
      builtFor = today;
    }
    return { model: cached, now: nowSec };
  };

  return {
    summary(url: URL) {
      const { model, now } = current();
      return model.summary(url, now);
    },
    series(url: URL) {
      const { model, now } = current();
      return model.series(url, now);
    },
    catalog() {
      return current().model.catalog();
    },
  };
}

type Result = { status: 200; body: unknown } | { status: 400; message: string };

function buildModel(variant: MockAnalyticsVariant, todayEpochDay: number, server: string) {
  /** Unix seconds of day index 0 (the oldest day of the full history). */
  const base = (todayEpochDay - D) * DAY;
  const hasData = variant === "full" || variant === "noPresence";
  /** First day index with data. */
  const firstDay = variant === "noPresence" ? D - 44 : 0;
  const presence = variant === "full";
  const missing: AnalyticsMissing[] =
    variant === "noPresence" ? [MISSING_PRESENCE, MISSING_AUDIT] : [];
  const notCollected = new Set(missing.flatMap((x) => x.metrics));
  const collected = (metric: string) =>
    hasData && !notCollected.has(metric) && (presence || !BY_NAME.get(metric)?.presence);

  const status: AnalyticsStatus = {
    enabled: variant !== "disabled",
    presence,
    since: hasData
      ? new Date((base + firstDay * DAY) * 1000).toISOString().replace(".000Z", "Z")
      : null,
    retentionDays: DAYS,
    missing,
  };

  const growth = (d: number) => Math.max(0, Math.min(1, d / D));
  const weekday = (d: number) => (new Date((base + d * DAY) * 1000).getUTCDay() + 6) % 7;
  const isWeekend = (d: number) => weekday(d) >= 5;

  // Members: daily joins and leaves, hourly; the total is their running sum.
  const joinSeed = hash("members.joined");
  const leaveSeed = hash("members.left");
  function joinsExpected(d: number): number {
    let e = (5 + 19 * growth(d) ** 1.3) * WEEKLY.joins[weekday(d)]!;
    if (d === PARTNER_DAY) e *= 9;
    else if (d === PARTNER_DAY + 1) e *= 3.6;
    else if (d === PARTNER_DAY + 2) e *= 2;
    if (d === RAID_DAY) e += 140;
    return e;
  }
  function leavesExpected(d: number): number {
    let e = joinsExpected(d) * 0.42;
    if (d === RAID_DAY) e -= 140 * 0.42;
    if (d === RAID_DAY + 1) e += 128;
    if (d > PARTNER_DAY + 2 && d < PARTNER_DAY + 20) e += 3.5;
    return e;
  }
  const dayHours = new Map<string, number[]>();
  function spread(
    key: string,
    seed: number,
    d: number,
    daily: number,
    profile: Profile,
    burst?: number,
  ) {
    const cacheKey = `${key}|${d}`;
    const hit = dayHours.get(cacheKey);
    if (hit) return hit;
    const shape = PROFILES[profile][isWeekend(d) ? 1 : 0];
    const out = shape.map((s, h) => {
      const r1 = rand(seed, d * 48 + h * 2);
      const r2 = rand(seed, d * 48 + h * 2 + 1);
      const share = burst !== undefined ? (h >= burst && h < burst + 3 ? 1 / 3 : 0) : s;
      return Math.max(0, roundish(daily * share * (0.72 + 0.56 * r1), r2));
    });
    dayHours.set(cacheKey, out);
    return out;
  }
  const raidBurst = (d: number, extra: number) =>
    d === RAID_DAY || d === RAID_DAY + 1 ? extra : undefined;
  const joinHours = (d: number) =>
    d === RAID_DAY
      ? sumArrays(
          spread("j", joinSeed, d, joinsExpected(d) - 140, "joins"),
          spread("jr", joinSeed, d, 140, "joins", 2),
        )
      : spread("j", joinSeed, d, joinsExpected(d), "joins");
  const leaveHours = (d: number) =>
    d === RAID_DAY + 1
      ? sumArrays(
          spread("l", leaveSeed, d, leavesExpected(d) - 128, "joins"),
          spread("lr", leaveSeed, d, 128, "flat", 9),
        )
      : spread("l", leaveSeed, d, leavesExpected(d), "joins");

  /** Members at the end of each day. */
  const totals: number[] = [];
  let running = BASE_MEMBERS;
  for (let d = 0; d <= D; d++) {
    running += sum(joinHours(d)) - sum(leaveHours(d));
    totals.push(running);
  }
  const totalAt = (d: number, h: number) => {
    let t = d > 0 ? totals[d - 1]! : BASE_MEMBERS;
    const j = joinHours(d);
    const l = leaveHours(d);
    for (let i = 0; i <= h; i++) t += j[i]! - l[i]!;
    return t;
  };
  const scale = (d: number) => (d > 0 ? totals[d - 1]! : BASE_MEMBERS) / BASE_MEMBERS;
  const activeSeed = hash("members.active");
  const activeDay = (d: number) =>
    Math.round(
      (d > 0 ? totals[d - 1]! : BASE_MEMBERS) *
        0.16 *
        WEEKLY.chat[weekday(d)]! *
        (0.93 + 0.14 * rand(activeSeed, d)),
    );

  /** Expected daily total of a counter (all dimensions). */
  function counterDaily(metric: string, d: number): number {
    const msgs = 690 * scale(d) * WEEKLY.chat[weekday(d)]!;
    const joins = joinsExpected(d);
    const voiceMembers = activeDay(d) * 0.3;
    const raid = d === RAID_DAY || d === RAID_DAY + 1;
    switch (metric) {
      case "messages.sent":
      case "messages.replies":
      case "messages.edited":
      case "messages.deleted":
      case "messages.attachments":
      case "messages.sent.bots":
      case "threads.created":
      case "polls.created":
        return msgs;
      case "messages.stickers":
        return msgs * 0.012;
      case "reactions.added":
        return msgs * 0.34;
      case "commands.used":
        return msgs * 0.055;
      case "components.used":
        return msgs * 0.08;
      case "bots.commands":
      case "bots.commands.channels":
        return msgs * 0.11;
      case "bots.commands.tenure":
        return msgs * 0.11 * CICORD_SHARE;
      case "voice.seconds":
      case "voice.sessions":
      case "voice.streaming":
        return voiceMembers * 3900 * WEEKLY.voice[weekday(d)]!;
      case "stage.seconds":
        return weekday(d) === 4 ? 24 * scale(d) ** 0.5 * 2 * HOUR : 0;
      case "members.joined.age":
      case "invites.uses":
        return joins;
      case "members.roles.added":
        return joins * 2.9 + 4 * scale(d) ** 0.5;
      case "onboarding.completed":
        return joins * 0.71;
      case "members.joined.bots":
        return 0.04;
      case "members.roles.removed":
        return 3.2 * scale(d) ** 0.5;
      case "invites.created":
        return 1.3;
      case "moderation.actions":
        return 5.5 * scale(d) ** 0.5 + (d === RAID_DAY + 1 ? 132 : 0);
      case "automod.triggers":
        return 8.5 * scale(d) ** 0.5 * (raid ? 9 : 1);
      case "automod.blocked":
        return 6 * scale(d) ** 0.5 * (raid ? 9 : 1);
      default:
        return 0;
    }
  }

  const profileOf = (metric: string): Profile =>
    metric.startsWith("voice.") || metric === "stage.seconds"
      ? "voice"
      : metric.startsWith("members.") ||
          metric.startsWith("invites.") ||
          metric === "onboarding.completed"
        ? "joins"
        : metric.startsWith("moderation.") || metric.startsWith("automod.")
          ? "flat"
          : "chat";

  const live = (dim: Dim, d: number) =>
    (dim.from === undefined || d >= dim.from) && (dim.until === undefined || d <= dim.until);

  /** Weight of a dimension on a day (ratios of the message sub-metrics, raid and partner days). */
  function weightOf(metric: string, dim: Dim, d: number): number {
    if (!live(dim, d)) return 0;
    const w = dim.weight;
    switch (metric) {
      case "messages.replies":
        return w * 0.2;
      case "messages.edited":
        return w * 0.055;
      case "messages.deleted":
        return w * 0.024;
      case "messages.attachments":
        return w * (ATTACHMENTS[dim.id] ?? 0.05);
      case "messages.sent.bots":
        return w * (BOTS[dim.id] ?? 0.005) + (dim.id === "ch-announcements" ? 2.5 : 0);
      case "threads.created":
        return w * (THREADS[dim.id] ?? 0.0012);
      case "polls.created":
        return w * (POLLS[dim.id] ?? 0.0002);
      case "voice.sessions":
        return w / 2600;
      case "voice.streaming":
        return w * (STREAMING[dim.id] ?? 0.01);
      case "invites.uses":
        if (dim.id === "partner-gg" && d >= PARTNER_DAY && d <= PARTNER_DAY + 2) return 520;
        if (dim.id === "zz9Rk1d") return d === RAID_DAY ? 640 : 0;
        return w;
      case "members.joined.age":
        if (d === RAID_DAY && dim.id === "<1d") return w + 700;
        if (d >= PARTNER_DAY && d <= PARTNER_DAY + 2 && dim.id === "<1y") return w + 20;
        return w;
      case "moderation.actions":
        if (d === RAID_DAY + 1 && (dim.id === "ban" || dim.id === "kick"))
          return w + (dim.id === "ban" ? 600 : 240);
        return w;
      default:
        return w;
    }
  }

  /** Metrics whose dimensions split the total (the others are ratios of the messages or voice). */
  const PARTITIONS = new Set([
    "messages.sent",
    "reactions.added",
    "voice.seconds",
    "stage.seconds",
    "invites.uses",
    "members.joined.age",
    "moderation.actions",
    "automod.triggers",
    "commands.used",
    "components.used",
    "bots.commands",
    "bots.commands.channels",
    "bots.commands.tenure",
    "members.roles.added",
    "members.roles.removed",
  ]);

  /** Hours of one dimension of a counter on a day ("" = the total). */
  function counterHours(metric: string, dimId: string, d: number): number[] {
    const key = `${metric}|${dimId}`;
    const hit = dayHours.get(`${key}|${d}`);
    if (hit) return hit;
    const dims = DIMS[metric];
    if (metric === "members.joined") return joinHours(d);
    if (metric === "members.left") return leaveHours(d);
    if (!dims) return spread(key, hash(key), d, counterDaily(metric, d), profileOf(metric));
    if (dimId === "") {
      const total = dims.reduce(
        (acc, dim) => sumArrays(acc, counterHours(metric, dim.id, d)),
        zeros(),
      );
      dayHours.set(`${key}|${d}`, total);
      return total;
    }
    const dim = dims.find((x) => x.id === dimId);
    if (!dim) return zeros();
    // Partitions share the total; ratio metrics are relative to the plain (message) weights.
    const weightSum =
      dims.reduce(
        (a, x) => a + (PARTITIONS.has(metric) ? weightOf(metric, x, d) : live(x, d) ? x.weight : 0),
        0,
      ) || 1;
    const daily = (counterDaily(metric, d) * weightOf(metric, dim, d)) / weightSum;
    const burst =
      metric === "moderation.actions"
        ? raidBurst(d, 9)
        : metric === "stage.seconds"
          ? 19
          : undefined;
    return spread(key, hash(key), d, daily, profileOf(metric), burst);
  }

  /** A gauge's value at an hour (undefined: no sample). */
  function gauge(metric: string, dimId: string, d: number, h: number): number | undefined {
    const seed = hash(`${metric}|${dimId}`);
    const r = rand(seed, d * 24 + h);
    const online = () => {
      const shape = PROFILES.chat[isWeekend(d) ? 1 : 0][h]! / SHAPE_MAX;
      return Math.round(
        totalAt(d, h) * (0.075 + 0.13 * shape) * (0.95 + 0.1 * rand(hash("online"), d * 24 + h)),
      );
    };
    const share = (dims: Dim[]) => {
      const dim = dims.find((x) => x.id === dimId);
      if (!dim || !live(dim, d)) return undefined;
      const total = dims.reduce((a, x) => a + (live(x, d) ? x.weight : 0), 0);
      return Math.round(
        online() * (dim.weight / (dims === ACTIVITIES ? 100 : total)) * (0.9 + 0.2 * r),
      );
    };
    switch (metric) {
      case "members.total":
        return totalAt(d, h);
      case "members.bots":
        // A few bots, one more now and then.
        return 5 + Math.floor(5 * growth(d));
      case "members.online":
        return online();
      case "members.boosting":
        return 4 + Math.floor(15 * growth(d));
      case "server.boost.tier": {
        const boosts = 4 + Math.floor(15 * growth(d));
        return boosts >= 14 ? 3 : boosts >= 7 ? 2 : 1;
      }
      case "voice.peak": {
        const shape = PROFILES.voice[isWeekend(d) ? 1 : 0][h]! / Math.max(...PROFILES.voice[1]);
        return Math.max(
          0,
          Math.round(
            activeDay(d) * 0.3 * 0.42 * shape * WEEKLY.voice[weekday(d)]! * (0.85 + 0.3 * r),
          ),
        );
      }
      case "events.interested": {
        const ev = EVENTS.find((x) => x.id === dimId);
        if (!ev || !live(ev, d) || dimId === "") return undefined;
        return Math.round(12 + 9 * (d - ev.from!) + h * 0.2);
      }
      case "presence.status":
        return dimId ? share(STATUSES) : online();
      case "presence.platform":
        return dimId ? share(PLATFORMS) : online();
      case "presence.activity":
        return dimId ? share(ACTIVITIES) : undefined;
      default:
        return undefined;
    }
  }

  function dailyValue(metric: string, d: number, now: number): number {
    const elapsed = Math.min(1, (now - (base + d * DAY)) / DAY);
    const liveFactor = d === D ? 0.3 + 0.7 * elapsed : 1;
    const active = activeDay(d) * liveFactor;
    const r = rand(hash(metric), d);
    if (metric === "members.active.chat") return Math.round(active * (0.78 + 0.04 * r));
    if (metric === "members.active.voice") return Math.round(active * (0.28 + 0.04 * r));
    if (metric === "members.active.bots") return Math.round(active * (0.19 + 0.04 * r));
    return Math.round(active);
  }

  // ─────────────────────────────────────────────────────── hourly access

  const dayOf = (t: number) => Math.floor((t - base) / DAY);

  /** Value of a metric/dimension in the hour starting at t (undefined: no data). */
  function hourly(metric: string, dimId: string, t: number, now: number): number | undefined {
    if (!collected(metric) || t > now) return undefined;
    const d = dayOf(t);
    if (d < firstDay || d > D) return undefined;
    const h = Math.floor((t - base - d * DAY) / HOUR);
    const def = BY_NAME.get(metric)!;
    if (def.kind === "counter") return counterHours(metric, dimId, d)[h]!;
    if (def.kind === "daily") return h === 0 ? dailyValue(metric, d, now) : undefined;
    return gauge(metric, dimId, d, h);
  }

  /** Combines the hours of [from, to) the way the metric's kind does. */
  function aggregate(
    metric: string,
    dimId: string,
    w: Window,
    now: number,
    mode: "bucket" | "avg" | "peak",
  ): number | undefined {
    const kind = BY_NAME.get(metric)!.kind;
    let total = 0;
    if (kind === "counter") {
      // Fast path: whole days at once.
      if (!collected(metric)) return 0;
      for (let d = Math.max(firstDay, dayOf(w.from)); d <= Math.min(D, dayOf(w.to - 1)); d++) {
        const hours = counterHours(metric, dimId, d);
        const start = base + d * DAY;
        for (let h = 0; h < 24; h++) {
          const t = start + h * HOUR;
          if (t >= w.from && t < w.to && t <= now) total += hours[h]!;
        }
      }
      return total;
    }
    if (kind === "daily" && mode === "avg") {
      // Daily distinct metrics are read per day: the days the window touches.
      if (!collected(metric)) return undefined;
      let days = 0;
      for (let d = Math.max(firstDay, dayOf(w.from)); d <= Math.min(D, dayOf(w.to - 1)); d++) {
        if (base + d * DAY > now) break;
        total += dailyValue(metric, d, now);
        days++;
      }
      return days ? total / days : undefined;
    }
    let count = 0;
    let last: number | undefined;
    let max: number | undefined;
    for (let t = w.from; t < w.to; t += HOUR) {
      const v = hourly(metric, dimId, t, now);
      if (v === undefined) continue;
      total += v;
      count++;
      last = v;
      max = max === undefined ? v : Math.max(max, v);
    }
    if (count === 0) return undefined;
    if (mode === "avg" || kind === "daily") return total / count;
    return kind === "max" || mode === "peak" ? max : last;
  }

  const dimsOf = (metric: string) => DIMS[metric] ?? [];

  // ─────────────────────────────────────────────────────── summary

  const PERIOD_DAYS: Record<AnalyticsPeriod, number> = {
    "7d": 7,
    "30d": 30,
    "90d": 90,
    "365d": 365,
  };

  /** The summary's window: a preset period (calendar days) or a custom range (UTC hours). */
  function summaryWindow(
    q: URLSearchParams,
    now: number,
  ): { period: AnalyticsPeriod | "custom"; step: "hour" | "day"; cur: Window } | { error: string } {
    if (!q.has("from") && !q.has("to")) {
      const period = (q.get("period") ?? "30d") as AnalyticsPeriod;
      const days = PERIOD_DAYS[period];
      if (!days) return { error: `invalid period "${period}" (7d, 30d, 90d or 365d)` };
      const to = (todayEpochDay + 1) * DAY;
      return { period, step: "day", cur: { from: to - days * DAY, to } };
    }
    if (q.has("period")) return { error: "period and from/to are mutually exclusive" };
    const fromMs = Date.parse(q.get("from") ?? "");
    const toMs = Date.parse(q.get("to") ?? "");
    if (Number.isNaN(fromMs) || Number.isNaN(toMs))
      return { error: "from and to are both required, in RFC 3339" };
    const from = Math.floor(fromMs / 1000 / HOUR) * HOUR;
    const to = Math.ceil(toMs / 1000 / HOUR) * HOUR;
    if (to - from < HOUR) return { error: "from must be before to (at least 1 hour)" };
    if (to - from > DAYS * DAY) return { error: "at most 400 days per request" };
    if (from >= now) return { error: "from must be in the past" };
    return { period: "custom", step: to - from < 2 * DAY ? "hour" : "day", cur: { from, to } };
  }

  function summary(url: URL, now: number): Result {
    const win = summaryWindow(url.searchParams, now);
    if ("error" in win) return { status: 400, message: win.error };
    const { period, step, cur } = win;
    const length = cur.to - cur.from;
    const prev: Window = { from: cur.from - length, to: cur.from };
    const sinceSec = base + firstDay * DAY;
    const prevCovered = hasData && sinceSec <= prev.from + DAY;

    const value = (metric: string, w: Window, dimId = "") => {
      if (metric === "members.net")
        return (
          (aggregate("members.joined", "", w, now, "bucket") ?? 0) -
          (aggregate("members.left", "", w, now, "bucket") ?? 0)
        );
      const avg = metric === "members.online" || metric === "members.active";
      return aggregate(metric, dimId, w, now, avg ? "avg" : "bucket");
    };
    const spark = (metric: string): AnalyticsPoint[] => {
      const points: AnalyticsPoint[] = [];
      if (!collected(metric === "members.net" ? "members.joined" : metric)) return points;
      const size = step === "hour" ? HOUR : DAY;
      for (let t = alignDown(cur.from, step); t < cur.to; t += size) {
        const v = value(metric, { from: Math.max(t, cur.from), to: Math.min(t + size, cur.to) });
        if (v !== undefined) points.push([t, round(v)]);
      }
      return points;
    };
    const KPIS = [
      "members.total",
      "members.bots",
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
    ];
    const kpis: AnalyticsKpi[] = KPIS.map((metric) => {
      const v = value(metric, cur);
      const p =
        prevCovered && collected(metric === "members.net" ? "members.joined" : metric)
          ? value(metric, prev)
          : undefined;
      return {
        metric,
        value: round(v ?? 0),
        previous: p === undefined ? null : round(p),
        spark: spark(metric),
      };
    });

    const RANKINGS = [
      "messages.sent",
      "voice.seconds",
      "reactions.added",
      "invites.uses",
      "commands.used",
      "moderation.actions",
      "automod.triggers",
      "members.roles.added",
      "members.joined.age",
      "bots.commands",
      "bots.commands.channels",
      "bots.commands.tenure",
      "components.used",
      ...(presence ? ["presence.activity"] : []),
    ];
    const labels: Record<string, AnalyticsLabel> = {};
    const rankings: Record<string, AnalyticsRanked[]> = {};
    for (const metric of RANKINGS) {
      // Gauge rankings (activities) hold the period's peak, like the store's dimension totals.
      const mode = metric === "presence.activity" ? "peak" : "bucket";
      const entries = collected(metric)
        ? dimsOf(metric)
            .map((dim) => ({
              dim: dim.id,
              value: round(aggregate(metric, dim.id, cur, now, mode) ?? 0),
              previous: prevCovered ? round(aggregate(metric, dim.id, prev, now, mode) ?? 0) : null,
            }))
            .filter((e) => e.value > 0)
            .toSorted((a, b) => b.value - a.value)
            .slice(0, 10)
        : [];
      rankings[metric] = entries;
      for (const e of entries) if (LABELS[e.dim]) labels[e.dim] = LABELS[e.dim]!;
    }

    const heatmapOf = (metric: string) => {
      const grid = Array.from({ length: 7 }, () => Array.from({ length: 24 }, () => 0));
      for (let t = cur.from; t < cur.to; t += HOUR) {
        const v = hourly(metric, "", t, now);
        if (!v) continue;
        const date = new Date(t * 1000);
        grid[(date.getUTCDay() + 6) % 7]![date.getUTCHours()]! += v;
      }
      return grid;
    };

    return {
      status: 200,
      body: {
        server,
        period,
        from: iso(cur.from),
        to: iso(cur.to),
        step,
        kpis,
        rankings,
        heatmap: heatmapOf("messages.sent"),
        botsHeatmap: heatmapOf("bots.commands"),
        labels,
        status,
      },
    };
  }

  // ─────────────────────────────────────────────────────── series

  const STEP: Record<AnalyticsStep, number> = { hour: HOUR, day: DAY, week: 7 * DAY };

  function alignDown(t: number, step: AnalyticsStep): number {
    if (step === "hour") return Math.floor(t / HOUR) * HOUR;
    const day = Math.floor(t / DAY) * DAY;
    if (step === "day") return day;
    const wd = (new Date(day * 1000).getUTCDay() + 6) % 7;
    return day - wd * DAY;
  }
  const alignUp = (t: number, step: AnalyticsStep) => {
    const down = alignDown(t, step);
    return down === t ? t : down + STEP[step];
  };

  function series(url: URL, now: number): Result {
    const q = url.searchParams;
    const metrics = (q.get("metrics") ?? "").split(",").filter(Boolean);
    if (metrics.length === 0) return { status: 400, message: "metrics is required" };
    const unknown = metrics.find((x) => !BY_NAME.has(x));
    if (unknown) return { status: 400, message: `unknown metric "${unknown}"` };
    const step = (q.get("step") ?? "day") as AnalyticsStep;
    if (!STEP[step]) return { status: 400, message: `invalid step "${step}" (hour, day or week)` };
    const parse = (name: string) => {
      const raw = q.get(name);
      if (raw === null) return undefined;
      const ms = Date.parse(raw);
      return Number.isNaN(ms) ? Number.NaN : Math.floor(ms / 1000);
    };
    const toRaw = parse("to") ?? now;
    const fromRaw = parse("from") ?? toRaw - 7 * DAY;
    if (Number.isNaN(toRaw) || Number.isNaN(fromRaw))
      return { status: 400, message: "from and to must be RFC 3339" };
    if (fromRaw >= toRaw) return { status: 400, message: "from must be before to" };
    if (toRaw - fromRaw > DAYS * DAY)
      return { status: 400, message: "at most 400 days per request" };
    const top = q.has("top") ? Number(q.get("top")) : 8;
    if (!Number.isInteger(top) || top < 1 || top > 50)
      return { status: 400, message: "top must be between 1 and 50" };
    const from = alignDown(fromRaw, step);
    const to = alignUp(toRaw, step);
    const points = (to - from) / STEP[step];
    if (points > 2000)
      return { status: 400, message: `at most 2000 points per series (${points} requested)` };
    const dimParam = q.get("dim");

    const buckets: Window[] = [];
    for (let t = from; t < to; t += STEP[step]) buckets.push({ from: t, to: t + STEP[step] });
    const serie = (metric: string, dimId: string): AnalyticsSerie => {
      const kind = BY_NAME.get(metric)!.kind;
      const out: AnalyticsPoint[] = [];
      for (const b of buckets) {
        const v = aggregate(metric, dimId, b, now, "bucket");
        if (v !== undefined) out.push([b.from, round(v)]);
        else if (kind === "counter") out.push([b.from, 0]);
      }
      return { metric, dim: dimId, points: out };
    };

    const result: AnalyticsSerie[] = [];
    const labels: Record<string, AnalyticsLabel> = {};
    for (const metric of metrics) {
      const def = BY_NAME.get(metric)!;
      if (!def.dim) {
        result.push(serie(metric, ""));
        continue;
      }
      if (dimParam !== null) {
        result.push(serie(metric, dimParam));
        if (LABELS[dimParam]) labels[dimParam] = LABELS[dimParam]!;
        continue;
      }
      const window: Window = { from, to };
      const ranked = dimsOf(metric)
        .map((dim) => ({ dim, total: aggregate(metric, dim.id, window, now, "avg") ?? 0 }))
        .filter((x) => x.total > 0)
        .toSorted((a, b) => b.total - a.total);
      const total = serie(metric, "");
      result.push(total);
      const kept = ranked.slice(0, top);
      for (const { dim } of kept) {
        result.push(serie(metric, dim.id));
        if (dim.label) labels[dim.id] = dim.label;
      }
      if (ranked.length > top) {
        // "other" = total − the kept dimensions, bucket by bucket.
        const keptSeries = result.slice(-kept.length);
        result.push({
          metric,
          dim: "other",
          points: total.points.map(([t, v]) => {
            const rest = keptSeries.reduce(
              (a, s) => a + (s.points.find((p) => p[0] === t)?.[1] ?? 0),
              0,
            );
            return [t, round(Math.max(0, v - rest))];
          }),
        });
      }
    }
    return {
      status: 200,
      body: { server, from: iso(from), to: iso(to), step, series: result, labels, status },
    };
  }

  function catalog(): Result {
    const metrics: AnalyticsCatalogEntry[] = CATALOG.map((c) => ({
      name: c.name,
      kind: c.kind === "daily" ? "last" : c.kind,
      ...(c.dim ? { dim: c.dim } : {}),
      unit: c.unit,
      source: c.source,
      ...(c.presence ? { presence: true } : {}),
      collected: variant !== "disabled" && !notCollected.has(c.name) && (presence || !c.presence),
    }));
    return { status: 200, body: { metrics, status } };
  }

  return { summary, series, catalog };
}

// ───────────────────────────────────────────────────────────── helpers

const zeros = () => Array.from({ length: 24 }, () => 0);
const sum = (xs: number[]) => xs.reduce((a, b) => a + b, 0);
const sumArrays = (a: number[], b: number[]) => a.map((v, i) => v + b[i]!);
const round = (v: number) => Math.round(v * 100) / 100;
const iso = (t: number) => new Date(t * 1000).toISOString().replace(".000Z", "Z");
