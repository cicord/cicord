/**
 * In-browser fake of the Cicord runtime for `vp dev --mode mock` and the tests: auth (dev
 * login), admin endpoints (https://cicord.dev/docs/reference/http-api/servers/–§5) and the simulator (§8), including the SSE
 * change stream and the behaviour of the example plugins (welcome, onboarding). State lives in
 * sessionStorage so it survives reloads.
 */
import type {
  AuditEntry,
  Diagnostic,
  FetchLike,
  HumanError,
  InteractionResult,
  Json,
  Message,
  MessageRow,
  PauseInfo,
  Revision,
  SyncRecord,
  User,
} from "@cicord/client";
import { applyDefaults, type JsonSchema } from "../schema-form/schema.ts";
import { validate } from "../schema-form/validate.ts";
import { deepEqual, isObject, jsonText } from "../schema-form/values.ts";
import {
  bot,
  bundledPlugins,
  categories,
  ENABLED_INTENTS,
  PRIVILEGED_INTENTS,
  seedRegistries,
  type MockRegistry,
  type MockStorePlugin,
  channels,
  onboardingBase,
  plugins,
  roles,
  schemas,
  SERVER_ID,
  SERVER_KEY,
  SERVER_NAME,
  serverSpec,
  unmanagedChannels,
  unmanagedRoles,
  users,
  welcomeBase,
  welcomeOverride,
} from "./data.ts";
import { createMockAnalytics, type MockAnalyticsVariant } from "./analytics.ts";

// Discord user IDs of the mock plan's role holders (built here: literal "role/<id>" keys
// look like secrets to the gitleaks pre-commit hook).
const leahId = "112233445566778900";
const absentId = "998877665544332211";

type Obj = { [key: string]: Json };

interface Member {
  userId: string;
  nick: string;
  roles: string[];
}

interface Instance {
  bot: string;
  instance: string;
  /** Plugin name (default: the instance name). */
  plugin?: string;
  /** `cicord`, a registry name or `local` (default: by plugin, see pluginInfo). */
  source?: string;
  base: Obj;
  override: Obj;
}

/** What the runtime knows about the plugin of an instance (plugin.yaml + origin). */
interface PluginInfo {
  name: string;
  version: string;
  displayName: string;
  description: string;
  capabilities: string[];
  intents: string[];
  commands: { name: string; description: string }[];
  schema: JsonSchema;
  source: string;
  trust: "official" | "unofficial" | "local";
}

interface MockState {
  session: string | null;
  revision: Revision;
  drift: boolean;
  /** The last syncs failed (announcement channel without Community). */
  failing?: boolean;
  lastSync: string;
  users: User[];
  members: Member[];
  messages: Message[];
  dms: Record<string, Message[]>;
  instances: Instance[];
  history: SyncRecord[];
  audit: AuditEntry[];
  welcomed: number;
  seq: number;
  /** Store (§11): cicord.yaml spec.registries and the instances installed from the Store. */
  registries: MockRegistry[];
  storeInstalls: StoreInstall[];
  storeFetchedAt: string;
  /** Names (never values) of the secrets in the runtime's secret store (registry tokens). */
  secretNames: string[];
  /**
   * Installed versions of Store plugins by "<source>/<plugin>" (cicord.lock entries and copies
   * of official plugins); the latest version is the one of the source. Absent: the latest.
   */
  versions?: Record<string, MockVersion>;
  /** Enforcement paused on the server (POST /servers/{server}/pause), absent otherwise. */
  paused?: PauseInfo;
}

interface MockVersion {
  version: string;
  capabilities: string[];
  intents: string[];
  network: string[];
  commit?: string;
  /** Bundled copies: modified in the repository (the update must be forced). */
  modified?: boolean;
}

interface StoreInstall {
  source: string;
  plugin: string;
  server: string;
  bot: string;
  instance: string;
}

const STORAGE = "cicord-admin:mock:v3";

const firstLine = (s: string) => s.split("\n")[0]!;

/** What the runtime reports for an announcement channel on a server without Community. */
const COMMUNITY_RAW = `reconcile ${SERVER_KEY}: 1 change(s) failed: create channel news: discord CreateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {"type":{"_errors":[{"code":"BASE_TYPE_CHOICES","message":"Value must be one of {0, 2, 4, 6, 13, 14, 15}."}]}}) (announcement channels require the Community feature: set community.enabled: true in the server YAML and Cicord enables it; if it is set, enabling it failed or Community was turned off outside Cicord)`;
const COMMUNITY_ERROR: HumanError = {
  code: "community_required",
  message: "Announcement and stage channels need the Community feature",
  hint: "Declare community: {enabled: true, rulesChannel, updatesChannel} in the server YAML and Cicord enables Community at the next sync. If it is already declared, enabling it failed or it was turned off outside Cicord: check the sync history, or enable it in Discord (Server Settings → Enable Community), then sync again. Or change the channel type in the YAML.",
  details: COMMUNITY_RAW,
  discordCode: 50035,
  httpStatus: 400,
  resource: { action: "create", kind: "channel", key: "news" },
  fields: [
    {
      path: "type",
      code: "BASE_TYPE_CHOICES",
      message: "Value must be one of {0, 2, 4, 6, 13, 14, 15}.",
    },
  ],
};
const API = "/api/v1";

const ago = (minutes: number) => new Date(Date.now() - minutes * 60000).toISOString();
/** n random hex digits (crypto.getRandomValues: no Math.random, even for mock ids). */
const randomHex = (n: number) =>
  Array.from(crypto.getRandomValues(new Uint8Array(Math.ceil(n / 2))), (b) =>
    b.toString(16).padStart(2, "0"),
  )
    .join("")
    .slice(0, n);
const hex = () => randomHex(7);

function seed(): MockState {
  const rev: Revision = {
    id: "4f0c2d9",
    sha: "4f0c2d9e8b1a7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
    message: "feat(onboarding): add the notifications step",
    author: "Leah",
    time: ago(42),
    source: "git",
  };
  const owner = users.owner;
  return {
    session: null,
    revision: rev,
    drift: true,
    failing: true,
    lastSync: ago(1),
    users: Object.values(users),
    members: [
      { userId: owner.id, nick: "", roles: ["r-admin"] },
      { userId: users.mina.id, nick: "", roles: ["r-mod", "r-member"] },
      { userId: bot.id, nick: "", roles: ["r-warden"] },
    ],
    messages: [
      message(
        bot,
        "ch-rules",
        `**Welcome to ${SERVER_NAME}!**\n\n1. Be kind\n2. No spam\n3. \`@everyone\` pings are for staff only`,
        {
          createdAt: ago(60 * 24 * 3),
          id: "m-rules",
          components: [
            {
              type: "row",
              components: [
                {
                  type: "button",
                  customId: "rules:accept",
                  label: "I accept the rules",
                  style: "success",
                  url: "",
                  emoji: "✅",
                },
                {
                  type: "button",
                  customId: "rules:more",
                  label: "Learn more",
                  style: "secondary",
                  url: "",
                },
              ],
            },
          ],
        },
      ),
      message(users.mina, "ch-general", "Hey all, Valorant night tonight at 9pm? Who's in?", {
        createdAt: ago(30),
        id: "m-1",
      }),
    ],
    dms: {},
    instances: [
      { bot: "moderator", instance: "welcome", base: welcomeBase, override: welcomeOverride },
      { bot: "moderator", instance: "onboarding", base: onboardingBase, override: {} },
    ],
    history: [
      ...[1, 6, 11, 16].map((minutes, i) => ({
        id: 10 - i,
        time: ago(minutes),
        revision: "4f0c2d9",
        target: `server:${SERVER_KEY}`,
        status: "failed" as const,
        message: firstLine(COMMUNITY_RAW),
        error: COMMUNITY_ERROR,
      })),
      {
        id: 6,
        time: ago(3),
        revision: "4f0c2d9",
        target: `server:${SERVER_KEY}`,
        status: "succeeded",
        message: "3 changes applied",
      },
      {
        id: 5,
        time: ago(42),
        revision: "4f0c2d9",
        target: "bot:moderator",
        status: "succeeded",
        message: "bot reloaded (2 plugins)",
      },
      {
        id: 4,
        time: ago(60 * 5),
        revision: "a91e7c3",
        target: `server:${SERVER_KEY}`,
        status: "rejected",
        message: 'rejected: steps[1].options[0].role: unknown role "xbox"',
      },
      {
        id: 3,
        time: ago(60 * 26),
        revision: "c27d0b1",
        target: `server:${SERVER_KEY}`,
        status: "succeeded",
        message: "1 change applied",
      },
      {
        id: 2,
        time: ago(60 * 50),
        revision: "e04b9fa",
        target: `server:${SERVER_KEY}`,
        status: "succeeded",
        message: "12 changes applied (adopted 4)",
      },
      {
        id: 1,
        time: ago(60 * 51),
        revision: "e04b9fa",
        target: "bot:moderator",
        status: "succeeded",
        message: "bot started",
      },
    ],
    audit: [
      {
        id: 7,
        time: ago(3),
        server: SERVER_KEY,
        actor: "cicord",
        action: "sync.apply",
        target: SERVER_KEY,
        details: { applied: 3 },
      },
      {
        id: 6,
        time: ago(42),
        server: SERVER_KEY,
        actor: "discord:104 (Mina)",
        action: "config.update",
        target: "moderator/onboarding",
        details: { scope: "server", fields: ["steps"], mode: "commit", revision: "4f0c2d9" },
      },
      {
        id: 5,
        time: ago(60 * 5),
        server: SERVER_KEY,
        actor: "github:leah",
        action: "revision.reject",
        target: "a91e7c3",
        details: { reason: "invalid config" },
      },
      {
        id: 4,
        time: ago(60 * 26),
        server: SERVER_KEY,
        actor: "cicord",
        action: "sync.apply",
        target: SERVER_KEY,
        details: { applied: 1 },
      },
      {
        id: 3,
        time: ago(60 * 50),
        server: SERVER_KEY,
        actor: "cicord",
        action: "sync.apply",
        target: SERVER_KEY,
        details: { applied: 12 },
      },
      {
        id: 2,
        time: ago(60 * 50),
        server: SERVER_KEY,
        actor: "discord:100 (Owner)",
        action: "auth.login",
        target: "admin",
        details: {},
      },
      {
        id: 1,
        time: ago(60 * 51),
        server: SERVER_KEY,
        actor: "cicord",
        action: "bot.start",
        target: "moderator",
        details: {},
      },
    ],
    welcomed: 0,
    seq: 1,
    registries: seedRegistries(),
    storeInstalls: [],
    storeFetchedAt: ago(12),
    // The hub's token is configured; community's secretRef points at a missing secret.
    secretNames: [registrySecret("hub")],
  };
}

/** Secret holding the token of a private registry (`CICORD_REGISTRY_<NAME>_TOKEN`). */
const registrySecret = (name: string) =>
  `CICORD_REGISTRY_${name.toUpperCase().replaceAll("-", "_")}_TOKEN`;

// ───────────────────────────────────────────── data tables (https://cicord.dev/docs/reference/http-api/ "Data tables")

interface PageQuery {
  q: string[];
  sort: string;
  order: "asc" | "desc";
  offset: number;
  limit: number | undefined;
}

/** Parses ?q&sort&order&offset&limit like internal/api/paging.go; a string is a 400 message. */
function parsePage(
  url: URL,
  sortKeys: string[],
  defaultSort: string,
  defaultLimit?: number,
): PageQuery | string {
  const v = url.searchParams;
  const sort = v.get("sort") || defaultSort;
  if (!sortKeys.includes(sort)) return `sort must be one of: ${sortKeys.join(", ")}`;
  const order = v.get("order") || "asc";
  if (order !== "asc" && order !== "desc") return "order must be asc or desc";
  const offset = Number(v.get("offset") ?? 0);
  if (!Number.isInteger(offset) || offset < 0) return "offset must be a non-negative integer";
  let limit: number | undefined = defaultLimit;
  if (v.get("limit") !== null) {
    limit = Number(v.get("limit"));
    if (!Number.isInteger(limit) || limit < 1) return "limit must be a positive integer";
  }
  return {
    q: (v.get("q") ?? "").toLowerCase().split(/\s+/).filter(Boolean),
    sort,
    order,
    offset,
    limit: limit === undefined ? undefined : Math.min(limit, 200),
  };
}

/** Case-insensitive comparison, numbers by value ("2" < "10"). */
function cmpNatural(a: string, b: string): number {
  const x = Number(a);
  const y = Number(b);
  if (a.trim() !== "" && b.trim() !== "" && !Number.isNaN(x) && !Number.isNaN(y))
    return x === y ? 0 : x < y ? -1 : 1;
  const la = a.toLowerCase();
  const lb = b.toLowerCase();
  return la === lb ? 0 : la < lb ? -1 : 1;
}

/** Filters on every query word, sorts by key (ties by tie) and returns the window. */
function pageOf<T>(
  items: T[],
  p: PageQuery,
  text: (item: T) => string,
  key: (item: T, sort: string) => string,
  tie: (item: T) => string,
) {
  const out = items.filter((it) => {
    const hay = text(it).toLowerCase();
    return p.q.every((w) => hay.includes(w));
  });
  out.sort((a, b) => {
    let c = cmpNatural(key(a, p.sort), key(b, p.sort));
    if (c === 0) c = tie(a) < tie(b) ? -1 : tie(a) > tie(b) ? 1 : 0;
    return p.order === "desc" ? -c : c;
  });
  const start = Math.min(p.offset, out.length);
  const end = p.limit === undefined ? out.length : Math.min(start + p.limit, out.length);
  return {
    items: out.slice(start, end),
    total: out.length,
    offset: p.offset,
    limit: p.limit ?? out.length,
    sort: p.sort,
    order: p.order,
  };
}

function message(
  author: User,
  channelId: string,
  content: string,
  extra: Partial<Message> = {},
): Message {
  return {
    id: extra.id ?? `m-${randomHex(8)}`,
    channelId,
    author,
    content,
    embeds: [],
    components: [],
    ephemeral: false,
    createdAt: new Date().toISOString(),
    ...extra,
  };
}

const json = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const fail = (status: number, code: string, msg: string, diagnostics?: Diagnostic[]): Response =>
  json(
    { error: diagnostics ? { code, message: msg, diagnostics } : { code, message: msg } },
    status,
  );

function merge(base: Obj, patch: Obj): Obj {
  const out: Obj = { ...base };
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) delete out[k];
    else out[k] = v;
  }
  return out;
}

function withDefaults(schema: JsonSchema, cfg: Obj): Obj {
  const out: Obj = { ...cfg };
  for (const [k, prop] of Object.entries(schema.properties ?? {})) {
    if (out[k] === undefined && prop.default !== undefined) out[k] = prop.default;
  }
  return out;
}

export interface MockRuntimeOptions {
  /** Artificial latency in ms (default 180; 0 in tests). */
  latency?: number;
  /** Persist state in sessionStorage (default true). */
  persist?: boolean;
  /**
   * Extra plugin instances (from the generated `hub` registry) on the server, to exercise the
   * Plugins table in `vp dev --mode mock` (default 0).
   */
  demoInstances?: number;
  /**
   * Installed plugins with an available update: the welcome copy is older than the bundled
   * version and acme/giveaways is installed at an older version (default false).
   */
  demoUpdates?: boolean;
  /** Analytics of the server (src/mock/analytics.ts; default "full"). */
  analytics?: MockAnalyticsVariant;
  /** Clock of the generated analytics (ms; default Date.now). */
  now?: () => number;
}

export function createMockRuntime(options: MockRuntimeOptions = {}) {
  const latency = options.latency ?? 180;
  const persist = options.persist ?? true;
  const fresh = (): MockState => {
    const s = seed();
    const hub = s.registries.find((r) => r.name === "hub");
    for (const p of hub?.plugins.slice(0, options.demoInstances ?? 0) ?? []) {
      s.instances.push({
        bot: "moderator",
        instance: p.name,
        plugin: p.name,
        source: "hub",
        base: {},
        override: {},
      });
      s.storeInstalls.push({
        source: "hub",
        plugin: p.name,
        server: SERVER_KEY,
        bot: "moderator",
        instance: p.name,
      });
    }
    if (options.demoUpdates) {
      s.versions = {};
      const welcome = bundledPlugins.find((p) => p.name === "welcome")!;
      s.versions["cicord/welcome"] = {
        version: "0.0.9",
        capabilities: welcome.capabilities.slice(0, -1),
        intents: welcome.intents,
        network: [],
      };
      const giveaways = s.registries
        .find((r) => r.name === "acme")!
        .plugins.find((p) => p.name === "giveaways")!;
      s.instances.push({
        bot: "moderator",
        instance: "giveaways",
        plugin: "giveaways",
        source: "acme",
        base: {},
        override: {},
      });
      s.storeInstalls.push({
        source: "acme",
        plugin: "giveaways",
        server: SERVER_KEY,
        bot: "moderator",
        instance: "giveaways",
      });
      s.versions["acme/giveaways"] = {
        version: "0.3.2",
        capabilities: giveaways.capabilities.filter((c) => c !== "scheduler"),
        intents: [],
        network: [],
        commit: "5b2e9c1d0a8f7e6d5c4b3a29180716f5e4d3c2b1",
      };
    }
    return s;
  };
  let state: MockState = load();
  const analytics = createMockAnalytics({
    variant: options.analytics ?? "full",
    server: SERVER_KEY,
    ...(options.now ? { now: options.now } : {}),
  });
  const streams = new Set<ReadableStreamDefaultController<Uint8Array>>();
  const encoder = new TextEncoder();

  function load(): MockState {
    if (persist) {
      try {
        const raw = sessionStorage.getItem(STORAGE);
        if (raw) {
          const saved = JSON.parse(raw) as MockState;
          // State saved before the Store existed.
          saved.registries ??= seedRegistries();
          saved.storeInstalls ??= [];
          saved.storeFetchedAt ??= new Date().toISOString();
          saved.secretNames ??= [];
          saved.versions ??= {};
          return saved;
        }
      } catch {
        // fresh state
      }
    }
    return fresh();
  }

  function changed() {
    if (persist) {
      try {
        sessionStorage.setItem(STORAGE, JSON.stringify(state));
      } catch {
        // ignore
      }
    }
    for (const c of streams) {
      try {
        c.enqueue(encoder.encode("event: change\ndata: {}\n\n"));
      } catch {
        streams.delete(c);
      }
    }
  }

  const userById = (id: string) =>
    id === bot.id ? { ...bot } : state.users.find((u) => u.id === id);
  const member = (id: string) => state.members.find((m) => m.userId === id);
  const roleIdOf = (key: string) => roles.find((r) => r.key === key)?.id;

  function accessRole(userId: string): "admin" | "moderator" | "member" | null {
    if (userId === users.owner.id) return "admin";
    const m = member(userId);
    if (!m) return null;
    if (m.roles.includes("r-admin")) return "admin";
    if (m.roles.includes("r-mod")) return "moderator";
    return "member";
  }

  const me = () => {
    const user = state.session ? userById(state.session) : undefined;
    if (!user) return null;
    const role = accessRole(user.id);
    return {
      user,
      servers: role ? [{ key: SERVER_KEY, name: SERVER_NAME, role }] : [],
    };
  };

  /** The plugin of an instance, at its installed version (state.versions). */
  function pluginInfo(inst: Instance): PluginInfo {
    const info = latestPluginInfo(inst);
    const v = state.versions?.[`${info.source}/${info.name}`];
    return v
      ? { ...info, version: v.version, capabilities: v.capabilities, intents: v.intents }
      : info;
  }

  /** The plugin of an instance: the two example plugins, a Store plugin, or a generated one. */
  function latestPluginInfo(inst: Instance): PluginInfo {
    const name = inst.plugin ?? inst.instance;
    const example = plugins[name as keyof typeof plugins];
    if (example && !inst.source)
      return {
        ...example,
        schema: schemas[name]!,
        // welcome comes from the official catalog (plugins/welcome/ copied by the Store),
        // onboarding is written in the repository.
        source: name === "welcome" ? "cicord" : "local",
        trust: name === "welcome" ? "official" : "local",
      };
    const source = inst.source ?? "local";
    const store =
      source === "cicord"
        ? bundledPlugins.find((p) => p.name === name)
        : state.registries.find((r) => r.name === source)?.plugins.find((p) => p.name === name);
    if (store)
      return {
        name,
        version: store.version,
        displayName: store.displayName,
        description: store.description,
        capabilities: store.capabilities,
        intents: store.intents,
        commands: store.commands ?? [],
        schema: store.schema,
        source,
        trust: source === "cicord" ? "official" : source === "local" ? "local" : "unofficial",
      };
    return {
      name,
      version: "0.1.0",
      displayName: "",
      description: "",
      capabilities: [],
      intents: [],
      commands: [],
      schema: { type: "object" },
      source,
      trust: source === "cicord" ? "official" : source === "local" ? "local" : "unofficial",
    };
  }

  function effective(inst: Instance): Obj {
    return withDefaults(pluginInfo(inst).schema, merge(inst.base, inst.override));
  }

  /** Effective config of the instance named `name`, `{}` once it is uninstalled. */
  function configOf(name: string): Obj {
    const inst = state.instances.find((i) => i.instance === name);
    return inst ? effective(inst) : {};
  }

  const resources = () => ({
    roles: [
      ...roles.map((r) => ({ ...r, managed: true })),
      ...unmanagedRoles.map((r) => ({ ...r, managed: false })),
    ],
    categories: categories.map((c) => ({ ...c, managed: true })),
    channels: [
      ...channels.map((c) => ({ ...c, managed: true })),
      ...unmanagedChannels.map((c) => ({ ...c, managed: false })),
    ],
  });

  const drift = () =>
    state.drift
      ? [
          {
            kind: "channel",
            key: "general",
            field: "topic",
            expected: "Chat about anything",
            actual: "random stuff",
          },
          { kind: "role", key: "valorant", field: "color", expected: "#ff4655", actual: "#e91e63" },
        ]
      : [];

  const serverStatus = () => ({
    key: SERVER_KEY,
    name: SERVER_NAME,
    discordId: SERVER_ID,
    sync: state.drift ? "OutOfSync" : "Synced",
    health: state.failing ? "Degraded" : "Healthy",
    message: state.failing ? COMMUNITY_RAW : "",
    ...(state.failing ? { error: COMMUNITY_ERROR } : {}),
    lastSync: state.lastSync,
    drift: drift(),
    iconUrl: "",
    bannerUrl: "",
    splashUrl: "",
    boostLevel: 1,
    boostCount: 3,
    features: ["ANIMATED_ICON", "INVITE_SPLASH"],
    community: false,
    ...(state.paused ? { paused: state.paused } : {}),
  });

  const plan = () => ({
    server: SERVER_KEY,
    changes: state.drift
      ? [
          {
            action: "update",
            kind: "channel",
            key: "general",
            summary: "update text channel #general",
            fields: { topic: { from: "random stuff", to: "Chat about anything" } },
          },
          {
            action: "update",
            kind: "role",
            key: "valorant",
            summary: "update role Valorant",
            fields: { color: { from: "#e91e63", to: "#ff4655" } },
          },
          {
            action: "reorder",
            kind: "channel",
            key: "community",
            summary: "reorder 2 channels in Community",
          },
          {
            action: "adopt",
            kind: "role",
            key: "ping-events",
            summary: "adopt existing role Event Pings",
            fields: { mentionable: { from: false, to: true } },
          },
        ]
      : [],
    blocked: [
      {
        action: "delete",
        kind: "role",
        key: "VIP 2023",
        reason: "deletionProtection",
        error: {
          code: "deletion_protection",
          message: "The role “VIP 2023” was removed from the YAML but deletion protection is on",
          hint: "Set syncPolicy.deletionProtection: false to let Cicord delete it, or add it back to the YAML.",
          details: "deletionProtection",
          resource: { action: "delete", kind: "role", key: "VIP 2023" },
        },
      },
      {
        action: "update",
        kind: "memberRole",
        key: `admin/${leahId}`,
        id: leahId,
        reason: 'hierarchy: role "Admin" is at or above the bot\'s highest role',
        summary: `give role "Admin" to @Leah (${leahId})`,
        error: {
          code: "hierarchy",
          message:
            "Cannot give role “Admin” to @Leah: the role is at or above the bot's highest role",
          hint: "Move the bot's role above Admin in Server settings → Roles.",
          details: 'hierarchy: role "Admin" is at or above the bot\'s highest role',
        },
      },
      {
        action: "update",
        kind: "server",
        key: SERVER_KEY,
        reason: "boost: banner requires boost level 2; server is level 0 (0 boost(s))",
        fields: { banner: { from: null, to: "../assets/banner.png" } },
        error: {
          code: "boost_required",
          message: "Banner requires boost level 2; server is level 0 (0 boost(s))",
          hint: "Boost the server to the required level (Server Settings → Server Boost), or remove the setting from the YAML. Once the level is reached, Cicord applies it right away with syncPolicy.selfHeal; otherwise it shows as a pending change until the next revision or Sync now.",
          details: "boost: banner requires boost level 2; server is level 0 (0 boost(s))",
        },
      },
    ],
    notes: [
      {
        kind: "memberRole",
        key: `mod/${absentId}`,
        message: `${absentId} is listed in roles.mod.members but is not a member of the server: they get the role when they join.`,
      },
    ],
  });

  const status = () => ({
    version: "0.1.0-dev",
    mode: "dev",
    simulator: true,
    revision: state.revision,
    lastError:
      'revision a91e7c3 rejected: bots/moderator/bot.yaml: /spec/plugins/1/config/steps/1/options/0/role: unknown role "xbox"',
    servers: [serverStatus()],
    bots: [
      {
        key: "moderator",
        username: bot.username,
        userId: bot.id,
        health: "Healthy",
        message: "",
        servers: [SERVER_KEY],
        plugins: state.instances.map((i) => ({
          instance: i.instance,
          plugin: pluginInfo(i).name,
          version: pluginInfo(i).version,
          server: SERVER_KEY,
          health: "Healthy",
          message: "",
        })),
      },
    ],
  });

  const pluginItems = (viewer: string) => {
    const role = accessRole(viewer);
    return state.instances.map((inst) => {
      const { schema, commands, source, trust, ...info } = pluginInfo(inst);
      const editable = Object.entries(schema.properties ?? {})
        .filter(
          ([, p]) =>
            role === "admin" || (role === "moderator" && p["x-cicord-access"] === "moderator"),
        )
        .map(([k]) => k)
        .toSorted();
      return {
        bot: inst.bot,
        instance: inst.instance,
        server: SERVER_KEY,
        plugin: info,
        config: effective(inst),
        baseConfig: inst.base,
        override: Object.keys(inst.override).length ? inst.override : null,
        schema,
        editable,
        commands,
        routes: info.name === "onboarding" ? ["GET /steps", "POST /complete"] : [],
        health: degraded(inst) ? "Degraded" : "Healthy",
        message: degraded(inst)
          ? "plugin crashed 3 times in 10 minutes: TypeError: cannot read properties of undefined"
          : "",
        source,
        trust,
        ...withUpdate(updateOf(source, info.name)),
      };
    });
  };

  type PluginItem = ReturnType<typeof pluginItems>[number];

  /** Some generated demo instances are unhealthy, to show it in the table. */
  const degraded = (inst: Instance) => inst.source === "hub" && /[37]$/.test(inst.instance);

  function pluginsPage(viewer: string, url: URL): Response {
    const p = parsePage(url, ["name", "instance", "bot", "version", "health", "source"], "name");
    if (typeof p === "string") return fail(400, "invalid", p);
    const updates = url.searchParams.get("updates");
    return json(
      pageOf(
        pluginItems(viewer).filter(
          (it) => !updates || (updates === "true") === ("update" in it && !!it.update),
        ),
        p,
        (it: PluginItem) =>
          [
            it.plugin.name,
            it.plugin.displayName,
            it.plugin.description,
            it.instance,
            it.bot,
            it.source,
          ].join(" "),
        (it, k) =>
          k === "instance"
            ? it.instance
            : k === "bot"
              ? it.bot
              : k === "version"
                ? it.plugin.version
                : k === "health"
                  ? it.health
                  : k === "source"
                    ? it.source
                    : it.plugin.displayName || it.plugin.name,
        (it) => `${it.bot}/${it.instance}`,
      ),
    );
  }

  /** DELETE /servers/{server}/plugins/{bot}/{instance} (https://cicord.dev/docs/reference/http-api/plugin-instances/). */
  function uninstall(viewer: string, inst: Instance): Response {
    const info = pluginInfo(inst);
    // onboarding's portal is what welcome's DM links to: removing it breaks the config.
    if (info.name === "onboarding" && configOf("welcome")["dmOnboarding"] === true) {
      const idx = state.instances.findIndex((i) => i.instance === "welcome");
      const detail = `bots/moderator/bot.yaml: /spec/plugins/${idx}/config/dmOnboarding: [server ${SERVER_KEY}] dmOnboarding links to the onboarding portal, which would no longer exist`;
      return json(
        {
          error: {
            code: "invalid",
            message: "The runtime rejected the new configuration.",
            hint: "Another plugin or setting may depend on it.",
            details: detail,
            diagnostics: [
              {
                severity: "error",
                file: "bots/moderator/bot.yaml",
                path: `/spec/plugins/${idx}/config/dmOnboarding`,
                message: `[server ${SERVER_KEY}] dmOnboarding links to the onboarding portal, which would no longer exist`,
              },
            ],
          },
        },
        422,
      );
    }
    state.instances = state.instances.filter((i) => i !== inst);
    state.storeInstalls = state.storeInstalls.filter(
      (i) => !(i.bot === inst.bot && i.instance === inst.instance),
    );
    const stillUsed = state.instances.some(
      (i) => pluginInfo(i).name === info.name && pluginInfo(i).source === info.source,
    );
    const cleaned = stillUsed
      ? []
      : info.source === "cicord"
        ? [`plugins/${info.name}/`]
        : info.source === "local"
          ? []
          : [`cicord.lock: ${info.source}/${info.name}`];
    const user = userById(viewer)!;
    newRevision(
      `chore(plugins): uninstall ${inst.bot}/${inst.instance} from ${SERVER_KEY}`,
      user.displayName,
    );
    addAudit(
      `discord:${viewer} (${user.displayName})`,
      "plugin.uninstall",
      `${inst.bot}/${inst.instance}`,
      {
        removed: true,
        cleaned,
        mode: "commit",
        revision: state.revision.id,
      },
    );
    changed();
    return json({ removed: true, cleaned, mode: "commit", revision: state.revision.id });
  }

  function newRevision(msg: string, author: string) {
    const id = hex();
    state.revision = {
      id,
      sha: id.padEnd(40, "0"),
      message: msg,
      author,
      time: new Date().toISOString(),
      source: "git",
    };
    state.history.unshift({
      id: state.history.length + 1,
      time: new Date().toISOString(),
      revision: state.revision.id,
      target: "bot:moderator",
      status: "succeeded",
      message: "bot reloaded (config change)",
    });
  }

  function serverRecord(recordStatus: SyncRecord["status"], text: string) {
    state.history.unshift({
      id: state.history.length + 1,
      time: new Date().toISOString(),
      revision: state.revision.id,
      target: `server:${SERVER_KEY}`,
      status: recordStatus,
      message: text,
    });
  }

  /** POST /servers/{server}/pause: nothing is applied until it is resumed. */
  function pause(viewer: string, reason: string): Response {
    if (state.paused) return json({ paused: state.paused, changed: false });
    if (reason.length > 500)
      return fail(400, "invalid", "the reason is longer than 500 characters");
    const user = userById(viewer);
    const info: PauseInfo = {
      by: `discord:${viewer}`,
      at: new Date().toISOString(),
      ...(user ? { byName: user.displayName || user.username } : {}),
      ...(reason.trim() ? { reason: reason.trim() } : {}),
    };
    state.paused = info;
    serverRecord(
      "paused",
      `enforcement paused by ${info.byName ?? info.by}${info.reason ? `: ${info.reason}` : ""}`,
    );
    addAudit(info.by, "enforcement.pause", SERVER_KEY, { reason: info.reason ?? "" });
    changed();
    return json({ paused: info, changed: true });
  }

  /** Ends the pause; returns it (undefined when the server was not paused). */
  function resume(viewer: string): PauseInfo | undefined {
    const prev = state.paused;
    if (!prev) return undefined;
    delete state.paused;
    const user = userById(viewer);
    serverRecord(
      "resumed",
      `enforcement resumed by ${user?.displayName || user?.username || viewer} (paused by ${prev.byName ?? prev.by})`,
    );
    addAudit(`discord:${viewer}`, "enforcement.resume", SERVER_KEY, {
      pausedBy: prev.by,
      pausedAt: prev.at,
      reason: prev.reason ?? "",
    });
    changed();
    return prev;
  }

  function pausedError(): Response {
    const p = state.paused!;
    return json(
      {
        error: {
          code: "paused",
          message: `enforcement is paused on server ${SERVER_KEY} by ${p.byName ?? p.by} since ${p.at}${p.reason ? ` (${p.reason})` : ""}: nothing is applied until it is resumed`,
          hint: `Resume enforcement first (POST /api/v1/servers/${SERVER_KEY}/resume, cicord resume ${SERVER_KEY}), or send {"resume": true} with this sync.`,
        },
      },
      409,
    );
  }

  function addAudit(actor: string, action: string, target: string, details: Obj) {
    const id = Math.max(0, ...state.audit.map((a) => a.id)) + 1;
    state.audit.unshift({
      id,
      time: new Date().toISOString(),
      server: SERVER_KEY,
      actor,
      action,
      target,
      details,
    });
  }

  function putConfig(viewer: string, inst: Instance, body: Obj): Response {
    const cfg = body["config"];
    const scope = (body["scope"] as string | undefined) ?? "server";
    if (!isObject(cfg)) return fail(400, "invalid", "config must be a JSON object");
    if (body["revision"] && body["revision"] !== state.revision.id)
      return fail(
        409,
        "conflict",
        `the configuration changed since revision ${jsonText(body["revision"])} (active: ${state.revision.id}); reload and retry`,
      );
    const role = accessRole(viewer);
    if (role !== "admin" && role !== "moderator")
      return fail(403, "forbidden", "moderator role required");
    const before = effective(inst);
    const after = withDefaults(
      pluginInfo(inst).schema,
      scope === "server" ? cfg : merge(cfg, inst.override),
    );
    const changedKeys = [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(
      (k) => !deepEqual(before[k], after[k]),
    );
    const schema = pluginInfo(inst).schema;
    const editable = new Set(
      Object.entries(schema.properties ?? {})
        .filter(([, p]) => role === "admin" || p["x-cicord-access"] === "moderator")
        .map(([k]) => k),
    );
    const denied = changedKeys.filter((k) => !editable.has(k));
    if (denied.length) return fail(403, "forbidden", `you may not change: ${denied.join(", ")}`);
    const idx = state.instances.indexOf(inst);
    const prefix =
      scope === "server"
        ? `/spec/plugins/${idx}/overrides/${SERVER_KEY}/config`
        : `/spec/plugins/${idx}/config`;
    const errors = validate(schema, after, resources());
    if (errors.length)
      return fail(
        422,
        "invalid",
        "the configuration is invalid",
        errors.map((e) => ({
          severity: "error",
          file: "bots/moderator/bot.yaml",
          path: prefix + e.pointer,
          message: `[server ${SERVER_KEY}] ${e.message}`,
        })),
      );
    if (changedKeys.length === 0)
      return json({ mode: "commit", revision: state.revision.id, pullRequestUrl: "" });
    if (scope === "server") {
      const patch: Obj = {};
      for (const k of Object.keys(cfg)) if (!deepEqual(cfg[k], inst.base[k])) patch[k] = cfg[k]!;
      inst.override = patch;
    } else {
      inst.base = cfg;
    }
    const user = userById(viewer)!;
    newRevision(
      `config(${inst.bot}/${inst.instance}): update ${scope === "server" ? `override for ${SERVER_KEY}` : "base config"}`,
      user.displayName,
    );
    addAudit(
      `discord:${viewer} (${user.displayName})`,
      "config.update",
      `${inst.bot}/${inst.instance}`,
      {
        scope,
        fields: changedKeys,
        mode: "commit",
        revision: state.revision.id,
      },
    );
    changed();
    return json({ mode: "commit", revision: state.revision.id, pullRequestUrl: "" });
  }

  // ───────────────────────────────────────────── simulated plugins

  const render = (template: string, memberMention: string) =>
    template.replaceAll("{member}", memberMention).replaceAll("{server}", SERVER_NAME);

  function portalLink(path: string, userId: string) {
    return `${window.location.origin}${path}?cicord_token=mock-${userId}-${Date.now().toString(36)}`;
  }

  function join(userId: string): Response {
    if (!userById(userId)) return fail(404, "not_found", "unknown user");
    if (member(userId)) return fail(400, "invalid", "already a member");
    state.members.push({ userId, nick: "", roles: [] });
    const cfg = configOf("welcome");
    const channel = channels.find((c) => c.key === cfg["channel"]);
    if (channel)
      state.messages.push(
        message(bot, channel.id, render(jsonText(cfg["message"]), `<@${userId}>`)),
      );
    const autoRole = typeof cfg["autoRole"] === "string" ? roleIdOf(cfg["autoRole"]) : undefined;
    if (autoRole) member(userId)!.roles.push(autoRole);
    if (cfg["dmOnboarding"] === true) {
      const dm = message(
        bot,
        `dm-${userId}`,
        `Welcome to **${SERVER_NAME}**! Pick your roles in 30 seconds:`,
        {
          components: [
            {
              type: "row",
              components: [
                {
                  type: "button",
                  customId: "",
                  label: "Get started",
                  style: "link",
                  url: portalLink("/onboarding", userId),
                },
              ],
            },
          ],
        },
      );
      (state.dms[userId] ??= []).push(dm);
    }
    state.welcomed++;
    changed();
    return new Response(null, { status: 204 });
  }

  function leave(userId: string): Response {
    if (!member(userId)) return fail(404, "not_found", "not a member");
    state.members = state.members.filter((m) => m.userId !== userId);
    changed();
    return new Response(null, { status: 204 });
  }

  const commands = () => [
    {
      bot: "moderator",
      name: "welcome-preview",
      description: "Preview the welcome message",
      options: [
        {
          name: "member",
          description: "Member to greet (defaults to you)",
          type: "user",
          required: false,
        },
      ],
    },
    {
      bot: "moderator",
      name: "welcome-stats",
      description: "How many members were welcomed",
      options: [],
    },
    {
      bot: "moderator",
      name: "onboarding",
      description: "Pick your roles on the community portal",
      options: [],
    },
  ];

  const result = (
    response: Message | null,
    extra: Partial<InteractionResult> = {},
  ): InteractionResult => ({
    status: "replied",
    response,
    followups: [],
    error: "",
    ...extra,
  });

  function runCommand(body: Obj): Response {
    const userId = jsonText(body["userId"]);
    const channelId = jsonText(body["channelId"]);
    const name = jsonText(body["name"]);
    const opts = isObject(body["options"]) ? body["options"] : {};
    if (!member(userId))
      return json(
        result(null, { status: "failed", error: "the user is not a member of this server" }),
      );
    const reply = (content: string, extra: Partial<Message> = {}) => {
      const m = message(bot, channelId, content, { ephemeral: true, ...extra });
      state.messages.push(m);
      changed();
      return m;
    };
    switch (name) {
      case "welcome-preview": {
        if (accessRole(userId) !== "admin")
          return json(result(null, { status: "failed", error: "missing permission ManageServer" }));
        const cfg = configOf("welcome");
        const target =
          typeof opts["member"] === "string" && opts["member"] ? opts["member"] : userId;
        return json(result(reply(render(jsonText(cfg["message"]), `<@${target}>`))));
      }
      case "welcome-stats":
        return json(result(reply(`${state.welcomed} members welcomed.`)));
      case "onboarding": {
        const cfg = configOf("onboarding");
        const row: MessageRow = {
          type: "row",
          components: [
            {
              type: "button",
              customId: "",
              label: jsonText(cfg["buttonLabel"]),
              emoji: "✨",
              style: "link",
              url: portalLink("/onboarding", userId),
            },
          ],
        };
        return json(result(reply(jsonText(cfg["linkMessage"]), { components: [row] })));
      }
      default:
        return json(result(null, { status: "failed", error: `unknown command "${name}"` }));
    }
  }

  function clickComponent(body: Obj): Response {
    const userId = jsonText(body["userId"]);
    const customId = jsonText(body["customId"]);
    const channelId = jsonText(body["channelId"]);
    if (customId === "rules:accept") {
      const m = message(bot, channelId, `Thanks, enjoy your stay at **${SERVER_NAME}**! 🎉`, {
        ephemeral: true,
      });
      state.messages.push(m);
      changed();
      return json(result(m));
    }
    if (customId === "rules:more") {
      const m = message(bot, channelId, "The full rules are pinned in <#ch-rules>.", {
        ephemeral: true,
      });
      const follow = message(bot, channelId, "Got a question? Ping a <@&r-mod>.", {
        ephemeral: true,
      });
      state.messages.push(m, follow);
      changed();
      return json(result(m, { followups: [follow] }));
    }
    void userId;
    return json(
      result(null, {
        status: "failed",
        error: "This interaction failed (no handler answered within 3 s)",
      }),
    );
  }

  const simState = () => ({
    users: state.users,
    bots: [{ ...bot, bot: true }],
    servers: [
      {
        id: SERVER_ID,
        key: SERVER_KEY,
        name: SERVER_NAME,
        ownerId: users.owner.id,
        channels: [
          ...categories.map((c, i) => ({ id: c.id, name: c.name, type: "category", position: i })),
          ...[...channels, ...unmanagedChannels].map((c, i) => ({
            id: c.id,
            name: c.name,
            type: c.type,
            parentId: categories.find((k) => k.key === c.category)?.id ?? "",
            position: i,
          })),
        ],
        roles: [...roles, ...unmanagedRoles].map((r, i) => ({
          id: r.id,
          name: r.name,
          color: r.color,
          position: 20 - i,
        })),
        members: state.members.map((m) => ({
          user: userById(m.userId)!,
          nick: m.nick,
          roles: m.roles,
        })),
      },
    ],
  });

  // ───────────────────────────────────────────── store (§11)

  const registryName = /^[a-z0-9][a-z0-9-]{0,31}$/;

  function storeInstalled(source: string, name: string) {
    const out = state.instances
      .filter((i) => {
        const info = pluginInfo(i);
        return info.source === source && info.name === name;
      })
      .map((i) => ({ server: SERVER_KEY, bot: i.bot, instance: i.instance }));
    for (const i of state.storeInstalls)
      if (
        i.source === source &&
        i.plugin === name &&
        !out.some((o) => o.bot === i.bot && o.instance === i.instance)
      )
        out.push({ server: i.server, bot: i.bot, instance: i.instance });
    return out;
  }

  function storePlugin(source: string, p: MockStorePlugin) {
    return {
      source,
      name: p.name,
      displayName: p.displayName,
      description: p.description,
      keywords: p.keywords,
      version: p.version,
      ...(p.publisher ? { publisher: p.publisher } : {}),
      verified: source === "cicord" ? p.verified : false,
      ...(p.repo ? { repo: p.repo } : {}),
      trust: source === "cicord" ? "official" : "unofficial",
      installed: storeInstalled(source, p.name),
      ...withUpdate(updateOf(source, p.name)),
    };
  }

  // ───────────────────────────── updates

  const semver = (v: string) => v.split(/[.+-]/).slice(0, 3).map(Number);

  function updateLevel(current: string, latest: string): "major" | "minor" | "patch" | "" {
    const [a, b] = [semver(current), semver(latest)];
    for (let i = 0; i < 3; i++)
      if (b[i]! !== a[i]!) return b[i]! > a[i]! ? (["major", "minor", "patch"] as const)[i]! : "";
    return "";
  }

  const withUpdate = <U>(u: U | undefined) => (u ? { update: u } : {});

  /** The available update of an installed Store plugin. */
  function updateOf(source: string, name: string) {
    const v = state.versions?.[`${source}/${name}`];
    if (!v || source === "local") return undefined;
    const latest =
      source === "cicord"
        ? bundledPlugins.find((x) => x.name === name)
        : state.registries.find((r) => r.name === source)?.plugins.find((x) => x.name === name);
    const level = latest ? updateLevel(v.version, latest.version) : "";
    if (!latest || !level) return undefined;
    return {
      kind: source === "cicord" ? ("bundled" as const) : ("registry" as const),
      current: v.version,
      latest: latest.version,
      level,
      ...(source === "cicord" && v.modified ? { modified: true } : {}),
    };
  }

  function listDiff(a: string[], b: string[]) {
    return {
      added: b.filter((x) => !a.includes(x)).toSorted(),
      removed: a.filter((x) => !b.includes(x)).toSorted(),
    };
  }

  function updatePlan(source: string, name: string) {
    const found = findStorePlugin(source, name)!;
    const p = found.plugin;
    const v: MockVersion = state.versions?.[`${source}/${name}`] ?? {
      version: p.version,
      capabilities: p.capabilities,
      intents: p.intents,
      network: p.network,
    };
    const privileged = (l: string[]) => l.filter((i) => PRIVILEGED_INTENTS.includes(i));
    const major = updateLevel(v.version, p.version) === "major";
    const instances = state.instances
      .filter((i) => {
        const info = pluginInfo(i);
        return info.source === source && info.name === name;
      })
      .map((inst) => {
        const idx = state.instances.indexOf(inst);
        const grants = listDiff(v.capabilities, p.capabilities);
        const diagnostics = validate(
          p.schema,
          applyDefaults(p.schema, merge(inst.base, inst.override)),
          resources(),
        ).map((e) => ({
          severity: "error" as const,
          file: "bots/moderator/bot.yaml",
          path: `/spec/plugins/${idx}/config${e.pointer}`,
          message: `[server ${SERVER_KEY}] ${e.message}`,
        }));
        return {
          server: SERVER_KEY,
          bot: inst.bot,
          instance: inst.instance,
          ...(found.registry
            ? { range: `^${v.version}`, ...(major ? { newRange: `^${p.version}` } : {}) }
            : {}),
          grantsAdded: grants.added,
          grantsRemoved: grants.removed,
          diagnostics,
        };
      });
    return {
      p,
      found,
      v,
      current: {
        version: v.version,
        ...(v.commit ? { commit: v.commit } : {}),
        capabilities: v.capabilities,
        intents: v.intents,
        privilegedIntents: privileged(v.intents),
        network: v.network,
      },
      diff: {
        capabilities: listDiff(v.capabilities, p.capabilities),
        intents: listDiff(v.intents, p.intents),
        privilegedIntents: listDiff(privileged(v.intents), privileged(p.intents)),
        network: listDiff(v.network, p.network),
      },
      instances,
    };
  }

  /** GET /store/{source}/{plugin}/update (https://cicord.dev/docs/reference/http-api/store/). */
  function storeUpdateReview(source: string, name: string): Response {
    const detail = storeDetail(source, name);
    if (!detail.ok) return detail;
    if (!storeInstalled(source, name).length)
      return fail(404, "not_installed", `${source}/${name} is not installed`);
    const planned = updatePlan(source, name);
    const bundled = source === "cicord";
    return json({
      available: !!updateOf(source, name),
      update: updateOf(source, name) ?? null,
      detail: storeDetailBody(source, name),
      current: planned.current,
      diff: planned.diff,
      instances: planned.instances,
      ...(bundled
        ? {
            files: {
              changed: ["plugin.yaml", "src/index.ts"],
              added: [],
              removed: planned.v.modified ? ["src/local-tweaks.ts"] : [],
            },
          }
        : {}),
      modified: !!planned.v.modified,
      repoChanged: false,
      servers: [SERVER_KEY],
      configValid: planned.instances.every((i) => !i.diagnostics.length),
      missingAdmin: [],
      restricted: [],
    });
  }

  /** POST /store/{source}/{plugin}/update (https://cicord.dev/docs/reference/http-api/store/). */
  function storeUpdate(viewer: string, source: string, name: string, body: Obj): Response {
    const detail = storeDetail(source, name);
    if (!detail.ok) return detail;
    if (!storeInstalled(source, name).length)
      return fail(404, "not_installed", `${source}/${name} is not installed`);
    const planned = updatePlan(source, name);
    const { p, found, v } = planned;
    if (found.registry) {
      const reg = p as MockRegistry["plugins"][number];
      if (body["confirmUnofficial"] !== true)
        return explained(409, {
          code: "confirmation_required",
          message: `${name} comes from an unofficial source.`,
          hint: "Review what the new version requests and confirm that you trust this source.",
        });
      if (!body["version"] || !body["commit"] || !body["integrity"])
        return fail(
          400,
          "invalid",
          "version, commit and integrity of the reviewed version are required",
        );
      if (
        body["version"] !== reg.version ||
        body["commit"] !== reg.commit ||
        body["integrity"] !== reg.integrity
      )
        return explained(409, {
          code: "changed",
          message: `${p.displayName} changed since you reviewed it (now ${reg.version}).`,
          hint: "Review the update again.",
        });
    } else if (body["version"] !== p.version)
      return explained(409, {
        code: "changed",
        message: `${p.displayName} changed since you reviewed it (now ${p.version}).`,
        hint: "Review the update again.",
      });
    if (!updateOf(source, name))
      return explained(409, {
        code: "up_to_date",
        message: `${p.displayName} ${p.version} is already installed.`,
      });
    if (v.modified && body["force"] !== true)
      return explained(409, {
        code: "modified",
        message: `The copy of ${p.displayName} in plugins/${name}/ was modified in the repository.`,
        hint: "Updating overwrites the local changes: keep them elsewhere, then force the update.",
        details: "changed: src/index.ts\ndeleted by the update: src/local-tweaks.ts",
      });
    const missing = planned.diff.privilegedIntents.added.filter(
      (i) => !ENABLED_INTENTS.includes(i),
    );
    if (missing.length)
      return explained(409, {
        code: "intents",
        message: `${p.displayName} needs a privileged intent that is turned off. ${missing.join(", ")} is off for ${bot.username}`,
        hint: "Turn it on in the Discord developer portal (Bot → Privileged Gateway Intents), save, then update again.",
        link: "https://discord.com/developers/applications/900/bot",
      });
    const diagnostics = planned.instances.flatMap((i) => i.diagnostics);
    if (diagnostics.length)
      return explained(422, {
        code: "invalid",
        message: `The configuration is not valid with ${p.displayName} ${p.version}: ${diagnostics[0]!.message}`,
        hint: "Fix the configuration of the instances (see the details), then update again.",
        diagnostics,
      });
    const user = userById(viewer)!;
    const key = `${source}/${name}`;
    state.versions = {
      ...state.versions,
      [key]: {
        version: p.version,
        capabilities: p.capabilities,
        intents: p.intents,
        network: p.network,
        ...(found.registry ? { commit: (p as MockRegistry["plugins"][number]).commit } : {}),
      },
    };
    newRevision(`plugin: update ${key} ${v.version} → ${p.version}`, user.displayName);
    addAudit(`discord:${viewer} (${user.displayName})`, "plugin.update", key, {
      source,
      plugin: name,
      from: v.version,
      to: p.version,
      capabilitiesAdded: planned.diff.capabilities.added,
      capabilitiesRemoved: planned.diff.capabilities.removed,
      forced: body["force"] === true && !!v.modified,
      trust: found.registry ? "unofficial" : "official",
      mode: "commit",
    });
    changed();
    return json({
      version: p.version,
      mode: "commit",
      revision: state.revision.id,
      instances: planned.instances.map((i) => ({
        server: i.server,
        bot: i.bot,
        instance: i.instance,
        health: "Healthy",
      })),
    });
  }

  /** A registry whose token is missing cannot be read. */
  const registryError = (r: MockRegistry) =>
    r.error ??
    (r.secretRef && !state.secretNames.includes(r.secretRef)
      ? {
          code: "fetch_failed",
          message: "Could not fetch the registry index",
          hint: `The access token ${r.secretRef} is missing on this runtime: set it again in Store → Sources.`,
          details: `git ls-remote ${r.url}: authentication required`,
        }
      : undefined);

  const sourceRows = () => [
    {
      name: "cicord",
      kind: "bundled",
      trust: "official",
      private: false,
      plugins: bundledPlugins.map((p) => storePlugin("cicord", p)),
    },
    ...state.registries.map((r) => {
      const error = registryError(r);
      return {
        name: r.name,
        kind: "git",
        url: r.url,
        trust: "unofficial",
        fetchedAt: state.storeFetchedAt,
        ...(error ? { error } : {}),
        private: !!r.secretRef,
        ...(r.secretRef
          ? { credentials: state.secretNames.includes(r.secretRef) ? "ok" : "missing" }
          : {}),
        plugins: error ? [] : r.plugins.map((p) => storePlugin(r.name, p)),
      };
    }),
  ];

  const storeSources = (summary = false) =>
    sourceRows().map(({ plugins: list, ...s }) =>
      summary
        ? { ...s, pluginCount: list.length }
        : { ...s, pluginCount: list.length, plugins: list },
    );

  type StoreItem = ReturnType<typeof storePlugin>;

  function storePlugins(url: URL): Response {
    const p = parsePage(
      url,
      ["name", "source", "trust", "version", "publisher", "installed"],
      "name",
      50,
    );
    if (typeof p === "string") return fail(400, "invalid", p);
    const v = url.searchParams;
    const all: StoreItem[] = [];
    for (const src of sourceRows())
      if (!v.get("source") || v.get("source") === src.name)
        for (const plugin of src.plugins) {
          if (v.get("trust") && v.get("trust") !== plugin.trust) continue;
          if (v.get("installed") && (v.get("installed") === "true") !== plugin.installed.length > 0)
            continue;
          if (v.get("updates") && (v.get("updates") === "true") !== "update" in plugin) continue;
          all.push(plugin);
        }
    return json(
      pageOf(
        all,
        p,
        (it) =>
          [
            it.name,
            it.displayName,
            it.description,
            it.source,
            it.publisher ?? "",
            ...it.keywords,
          ].join(" "),
        (it, k) =>
          k === "source"
            ? it.source
            : k === "trust"
              ? it.trust
              : k === "version"
                ? it.version
                : k === "publisher"
                  ? (it.publisher ?? "")
                  : k === "installed"
                    ? String(it.installed.length)
                    : it.displayName,
        (it) => `${it.source}/${it.name}`,
      ),
    );
  }

  function findStorePlugin(source: string, name: string) {
    if (source === "cicord") {
      const p = bundledPlugins.find((x) => x.name === name);
      return p ? { plugin: p, registry: undefined } : undefined;
    }
    const registry = state.registries.find((r) => r.name === source);
    const p =
      registry && !registryError(registry)
        ? registry.plugins.find((x) => x.name === name)
        : undefined;
    return registry && p ? { plugin: p, registry } : undefined;
  }

  function storeDetail(source: string, name: string): Response {
    const found = findStorePlugin(source, name);
    if (!found) {
      const registry = state.registries.find((r) => r.name === source);
      const error = registry && registryError(registry);
      if (error) return fail(502, "registry_unavailable", `registry ${source}: ${error.message}`);
      return fail(404, "not_found", `unknown plugin ${source}/${name}`);
    }
    return json(storeDetailBody(source, name));
  }

  function storeDetailBody(source: string, name: string) {
    const found = findStorePlugin(source, name)!;
    const p = found.plugin;
    const extra = found.registry
      ? {
          commit: (p as MockRegistry["plugins"][number]).commit,
          integrity: (p as MockRegistry["plugins"][number]).integrity,
        }
      : {};
    return {
      ...storePlugin(source, p),
      version: p.version,
      ...extra,
      capabilities: p.capabilities,
      intents: p.intents,
      privilegedIntents: p.intents.filter((i) => PRIVILEGED_INTENTS.includes(i)),
      network: p.network,
      schema: p.schema,
      commands: found.registry ? null : p.commands,
    };
  }

  const explained = (code: number, error: Obj): Response => json({ error }, code);

  function storeInstall(viewer: string, body: Obj): Response {
    const source = jsonText(body["source"]);
    const name = jsonText(body["plugin"]);
    const found = findStorePlugin(source, name);
    if (!found) return fail(404, "not_found", `unknown plugin ${source}/${name}`);
    const p = found.plugin;
    if (found.registry) {
      const reg = p as MockRegistry["plugins"][number];
      if (!body["version"] || !body["commit"] || !body["integrity"])
        return fail(
          400,
          "invalid",
          "version, commit and integrity are required for registry plugins",
        );
      if (
        body["version"] !== reg.version ||
        body["commit"] !== reg.commit ||
        body["integrity"] !== reg.integrity
      )
        return explained(409, {
          code: "changed",
          message: `${source}/${name} now resolves to ${reg.version} (${reg.commit.slice(0, 7)}), not the version you reviewed`,
          hint: "Review the plugin again before installing it.",
        });
      if (body["confirmUnofficial"] !== true)
        return explained(409, {
          code: "confirmation_required",
          message: `${source} is an unofficial source: confirm that you trust it`,
          hint: "Send confirmUnofficial: true after reviewing what the plugin requests.",
        });
    }
    const botKey = jsonText(body["bot"]) || "moderator";
    if (botKey !== "moderator") return fail(404, "not_found", `unknown bot ${botKey}`);
    const instance = jsonText(body["instance"]) || name;
    if (state.instances.some((i) => i.bot === botKey && i.instance === instance))
      return fail(409, "conflict", `bot ${botKey} already has an instance named "${instance}"`);
    const missing = p.intents.filter(
      (i) => PRIVILEGED_INTENTS.includes(i) && !ENABLED_INTENTS.includes(i),
    );
    if (missing.length)
      return explained(409, {
        code: "intents",
        message: `${p.displayName} needs the ${missing.join(", ")} privileged intent, which is off for ${bot.username}`,
        hint: "Turn it on in the Discord developer portal (Bot → Privileged Gateway Intents), save, then install again.",
        link: "https://discord.com/developers/applications/900/bot",
      });
    const cfg = isObject(body["config"]) ? body["config"] : {};
    const errors = validate(p.schema, applyDefaults(p.schema, cfg), resources());
    if (errors.length)
      return fail(
        422,
        "invalid",
        "the configuration is invalid",
        errors.map((e) => ({
          severity: "error",
          file: "bots/moderator/bot.yaml",
          path: `/spec/plugins/${state.instances.length}/config${e.pointer}`,
          message: `[server ${SERVER_KEY}] ${e.message}`,
        })),
      );
    const user = userById(viewer)!;
    state.storeInstalls.push({ source, plugin: name, server: SERVER_KEY, bot: botKey, instance });
    state.versions = {
      ...state.versions,
      [`${source}/${name}`]: state.versions?.[`${source}/${name}`] ?? {
        version: p.version,
        capabilities: p.capabilities,
        intents: p.intents,
        network: p.network,
      },
    };
    state.instances.push({ bot: botKey, instance, plugin: name, source, base: cfg, override: {} });
    newRevision(
      `feat(store): install ${source}/${name} as ${botKey}/${instance}`,
      user.displayName,
    );
    addAudit(`discord:${viewer} (${user.displayName})`, "store.install", `${botKey}/${instance}`, {
      source,
      plugin: name,
      version: p.version,
      commit: found.registry ? (p as MockRegistry["plugins"][number]).commit : "",
      trust: found.registry ? "unofficial" : "official",
      mode: "commit",
    });
    changed();
    const command = found.registry ? undefined : p.commands?.[0]?.name;
    return json({
      instance,
      health: "Healthy",
      ...(command ? { command } : {}),
      revision: state.revision.id,
      mode: "commit",
    });
  }

  function addRegistry(viewer: string, body: Obj): Response {
    const name = jsonText(body["name"]).trim();
    const url = jsonText(body["url"]).trim();
    if (!registryName.test(name))
      return fail(400, "invalid", `invalid registry name "${name}": use a-z, 0-9 and -`);
    if (name === "cicord") return fail(400, "invalid", "the name cicord is reserved");
    if (state.registries.some((r) => r.name === name))
      return fail(400, "invalid", `a registry named ${name} already exists`);
    if (!url.startsWith("https://"))
      return fail(400, "invalid", "the registry URL must be https://");
    if (state.registries.some((r) => r.url === url))
      return fail(400, "invalid", `${url} is already declared`);
    if (/^https:\/\/[^/]*@/.test(url))
      return fail(
        400,
        "invalid",
        "registry URLs must not contain credentials: give the access token separately",
      );
    if (url.includes(".invalid"))
      return fail(422, "invalid", `could not fetch ${url}/index.yaml: repository not found`);
    const token = jsonText(body["token"]);
    if (token === "bad")
      return fail(
        422,
        "invalid",
        `could not read ${url}/index.yaml with this token: authentication failed`,
      );
    const secretRef = token ? registrySecret(name) : undefined;
    if (secretRef && !state.secretNames.includes(secretRef)) state.secretNames.push(secretRef);
    state.registries.push({ name, url, plugins: [], ...(secretRef ? { secretRef } : {}) });
    const user = userById(viewer)!;
    newRevision(`chore(store): add registry ${name}`, user.displayName);
    addAudit(`discord:${viewer} (${user.displayName})`, "registry.add", name, { url });
    changed();
    return json({ mode: "commit", revision: state.revision.id });
  }

  function removeRegistry(viewer: string, name: string): Response {
    const registry = state.registries.find((r) => r.name === name);
    if (!registry) return fail(404, "not_found", `unknown registry ${name}`);
    const dependents = state.storeInstalls.filter((i) => i.source === name);
    if (dependents.length)
      return explained(409, {
        code: "in_use",
        message: `registry ${name} is used by ${dependents.length} plugin instance(s)`,
        hint: "Remove these instances from bot.yaml first.",
        details: dependents
          .map((i) => `bots/${i.bot}/bot.yaml: ${i.instance} (${i.source}/${i.plugin})`)
          .join("\n"),
      });
    state.registries = state.registries.filter((r) => r !== registry);
    state.secretNames = state.secretNames.filter((x) => x !== registrySecret(name));
    const user = userById(viewer)!;
    newRevision(`chore(store): remove registry ${name}`, user.displayName);
    addAudit(`discord:${viewer} (${user.displayName})`, "registry.remove", name, {});
    changed();
    return json({ mode: "commit", revision: state.revision.id });
  }

  /** PUT/DELETE /registries/{name}/token (https://cicord.dev/docs/reference/http-api/store/). */
  function registryToken(viewer: string, name: string, method: string, body: Obj): Response {
    const registry = state.registries.find((r) => r.name === name);
    if (!registry) return fail(404, "not_found", `unknown registry "${name}"`);
    const user = userById(viewer)!;
    const own = registrySecret(name);
    if (method === "DELETE") {
      if (!registry.secretRef) return json({ mode: "commit" });
      // Only the secret the admin UI created is deleted; an operator's secret stays.
      if (registry.secretRef === own)
        state.secretNames = state.secretNames.filter((x) => x !== own);
      delete registry.secretRef;
      newRevision(`registry: ${name} is public (token removed)`, user.displayName);
      addAudit(`discord:${viewer} (${user.displayName})`, "registry.token", name, {
        removed: true,
      });
      changed();
      return json({ mode: "commit", revision: state.revision.id });
    }
    const token = jsonText(body["token"]);
    if (!token) return fail(400, "invalid", "token is required");
    if (token === "bad")
      return fail(
        422,
        "invalid",
        `could not read ${registry.url}/index.yaml with this token: authentication failed`,
      );
    const ref = registry.secretRef ?? own;
    // The index can be read again with the token.
    delete registry.error;
    if (!state.secretNames.includes(ref)) state.secretNames.push(ref);
    addAudit(`discord:${viewer} (${user.displayName})`, "registry.token", name, { set: true });
    if (registry.secretRef) {
      changed();
      return json({ mode: "commit" });
    }
    registry.secretRef = ref;
    newRevision(`registry: ${name} is private (token set)`, user.displayName);
    changed();
    return json({ mode: "commit", revision: state.revision.id });
  }

  // ───────────────────────────────────────────── routing

  function route(method: string, url: URL, body: Obj): Response {
    const path = url.pathname;
    const viewer = state.session;
    const authed = (fn: (viewer: string) => Response): Response =>
      viewer ? fn(viewer) : fail(401, "unauthenticated", "login required");
    const staff = (fn: (viewer: string) => Response, admin = false): Response =>
      authed((v) => {
        const role = accessRole(v);
        if (role === "admin" || (!admin && role === "moderator")) return fn(v);
        return fail(403, "forbidden", admin ? "admin role required" : "moderator role required");
      });

    if (method === "POST" && path === "/auth/dev-login") {
      const id = jsonText(body["userId"]);
      if (!userById(id) || id === bot.id) return fail(404, "not_found", `unknown user ${id}`);
      state.session = id;
      changed();
      return json(me());
    }
    if (method === "POST" && path === "/auth/logout") {
      state.session = null;
      changed();
      return new Response(null, { status: 204 });
    }
    if (method === "GET" && path === `${API}/me`) return authed(() => json(me()));
    if (method === "GET" && path === `${API}/status`) return staff(() => json(status()));
    if (method === "GET" && path === `${API}/history`) {
      const limit = Number(url.searchParams.get("limit") ?? 50);
      return staff(() => json({ items: state.history.slice(0, limit) }));
    }

    if (method === "GET" && path === `${API}/store`)
      return staff(
        () => json({ sources: storeSources(url.searchParams.get("summary") === "1") }),
        true,
      );
    if (method === "GET" && path === `${API}/store/plugins`)
      return staff(() => storePlugins(url), true);
    if (method === "POST" && path === `${API}/store/refresh`)
      return staff(() => {
        state.storeFetchedAt = new Date().toISOString();
        changed();
        return json({ sources: storeSources() });
      }, true);
    const upd = /^\/api\/v1\/store\/([^/]+)\/([^/]+)\/update$/.exec(path);
    if (upd && method === "GET")
      return staff(
        () => storeUpdateReview(decodeURIComponent(upd[1]!), decodeURIComponent(upd[2]!)),
        true,
      );
    if (upd && method === "POST")
      return staff(
        (v) => storeUpdate(v, decodeURIComponent(upd[1]!), decodeURIComponent(upd[2]!), body),
        true,
      );
    const detail = /^\/api\/v1\/store\/([^/]+)\/([^/]+)$/.exec(path);
    if (method === "GET" && detail && detail[1] !== "plugins")
      return staff(
        () => storeDetail(decodeURIComponent(detail[1]!), decodeURIComponent(detail[2]!)),
        true,
      );
    if (method === "POST" && path === `${API}/registries`)
      return staff((v) => addRegistry(v, body), true);
    const registry = /^\/api\/v1\/registries\/([^/]+)$/.exec(path);
    if (method === "DELETE" && registry)
      return staff((v) => removeRegistry(v, decodeURIComponent(registry[1]!)), true);
    const token = /^\/api\/v1\/registries\/([^/]+)\/token$/.exec(path);
    if ((method === "PUT" || method === "DELETE") && token)
      return staff((v) => registryToken(v, decodeURIComponent(token[1]!), method, body), true);

    const server = /^\/api\/v1\/servers\/([^/]+)(\/.*)?$/.exec(path);
    if (server) {
      if (decodeURIComponent(server[1]!) !== SERVER_KEY)
        return fail(404, "not_found", "unknown server");
      const rest = server[2] ?? "";
      if (method === "GET" && rest === "")
        return staff(() => json({ ...serverStatus(), spec: serverSpec }));
      if (method === "GET" && rest === "/resources") return staff(() => json(resources()));
      const insight = /^\/analytics\/(summary|series|catalog)$/.exec(rest);
      if (method === "GET" && insight)
        return staff(() => {
          const r =
            insight[1] === "summary"
              ? analytics.summary(url)
              : insight[1] === "series"
                ? analytics.series(url)
                : analytics.catalog();
          return r.status === 200 ? json(r.body) : fail(400, "invalid", r.message);
        });
      if (method === "GET" && rest === "/plan") return staff(() => json(plan()), true);
      if (method === "GET" && rest === "/diagnostics")
        return staff(
          () =>
            json({
              server: SERVER_KEY,
              bot: "moderator",
              ranAt: new Date().toISOString(),
              ok: false,
              checks: [
                { id: "token", status: "ok", message: `Connected as ${bot.username}.` },
                {
                  id: "intents",
                  status: "ok",
                  message: "The privileged intents Cicord uses are enabled.",
                },
                {
                  id: "member",
                  status: "ok",
                  message: `${bot.username} is a member of ${SERVER_NAME}.`,
                },
                {
                  id: "permissions",
                  status: "ok",
                  message: "The bot has the Administrator permission.",
                },
                {
                  id: "hierarchy",
                  status: "ok",
                  message: `The role “${bot.username}” is above every role Cicord manages.`,
                },
                { id: "commands", status: "ok", message: "2 slash command(s) registered." },
                {
                  id: "events",
                  status: "pending",
                  message:
                    "Waiting for a message: send any message in a text channel the bot can see.",
                },
                {
                  id: "plan",
                  status: "warn",
                  message: "Discord differs from the configuration: 3 change(s) pending.",
                  hint: "Open Sync → Plan in the admin to review them; Cicord applies them on the next sync.",
                },
                {
                  id: "writeback",
                  status: "ok",
                  message: "Cicord can read and update the configuration repository.",
                },
              ],
            }),
          true,
        );
      if (method === "GET" && rest === "/pause")
        // moderators get the plan only while paused (the plan endpoint is admin-only)
        return staff((v) =>
          json({
            paused: state.paused ?? null,
            plan: state.paused || accessRole(v) === "admin" ? plan() : null,
          }),
        );
      if (method === "POST" && rest === "/pause")
        return staff((v) => pause(v, jsonText(body["reason"])));
      if (method === "POST" && rest === "/resume")
        return staff((v) => {
          const prev = resume(v);
          return json(prev ? { resumed: true, pause: prev } : { resumed: false });
        });
      if (method === "POST" && rest === "/sync")
        return staff((v) => {
          if (state.paused && body["resume"] !== true) return pausedError();
          if (body["resume"] === true) resume(v);
          const p = plan();
          const applied = p.changes.length;
          state.drift = false;
          state.failing = false;
          state.lastSync = new Date().toISOString();
          state.history.unshift({
            id: state.history.length + 1,
            time: state.lastSync,
            revision: state.revision.id,
            target: `server:${SERVER_KEY}`,
            status: "succeeded",
            message: `${applied} changes applied`,
          });
          addAudit(`discord:${v}`, "sync.apply", SERVER_KEY, { applied });
          changed();
          return json({ plan: p, applied });
        }, true);
      if (method === "GET" && rest === "/audit")
        return staff(() => {
          const before = Number(url.searchParams.get("before") ?? 0);
          const limit = Number(url.searchParams.get("limit") ?? 50);
          const items = state.audit.filter((a) => !before || a.id < before).slice(0, limit);
          return json({ items });
        });
      if (method === "GET" && rest === "/plugins") return staff((v) => pluginsPage(v, url));
      const one = /^\/plugins\/([^/]+)\/([^/]+)$/.exec(rest);
      if (one) {
        const botKey = decodeURIComponent(one[1]!);
        const name = decodeURIComponent(one[2]!);
        const inst = state.instances.find((i) => i.bot === botKey && i.instance === name);
        const missing = () =>
          fail(404, "not_found", `no plugin instance ${botKey}/${name} on ${SERVER_KEY}`);
        if (method === "GET")
          return staff((v) => {
            const item = pluginItems(v).find((i) => i.bot === botKey && i.instance === name);
            return item ? json(item) : missing();
          });
        if (method === "DELETE") return staff((v) => (inst ? uninstall(v, inst) : missing()), true);
      }
      if (method === "POST" && rest === "/store/install")
        return staff((v) => storeInstall(v, body), true);
      const put = /^\/plugins\/([^/]+)\/([^/]+)\/config$/.exec(rest);
      if (method === "PUT" && put) {
        const inst = state.instances.find(
          (i) =>
            i.bot === decodeURIComponent(put[1]!) && i.instance === decodeURIComponent(put[2]!),
        );
        if (!inst) return fail(404, "not_found", "unknown plugin instance");
        return authed((v) => putConfig(v, inst, body));
      }
    }

    if (path.startsWith(`${API}/sim/`)) {
      const sim = path.slice(`${API}/sim`.length);
      if (method === "GET" && sim === "/state") return json(simState());
      if (method === "POST" && sim === "/users") {
        const username = jsonText(body["username"]).trim();
        if (!username) return fail(400, "invalid", "username is required");
        const user = {
          id: String(200 + state.seq++),
          username,
          displayName: username,
          avatarUrl: "",
        };
        state.users.push(user);
        changed();
        return json(user);
      }
      const joinMatch = /^\/servers\/([^/]+)\/members(?:\/([^/]+))?$/.exec(sim);
      if (joinMatch) {
        if (method === "POST") return join(jsonText(body["userId"]));
        if (method === "DELETE" && joinMatch[2]) return leave(decodeURIComponent(joinMatch[2]));
      }
      const msgs = /^\/channels\/([^/]+)\/messages$/.exec(sim);
      if (method === "GET" && msgs) {
        const id = decodeURIComponent(msgs[1]!);
        return json({ items: state.messages.filter((m) => m.channelId === id).slice(-100) });
      }
      const dms = /^\/users\/([^/]+)\/dms$/.exec(sim);
      if (method === "GET" && dms)
        return json({ items: state.dms[decodeURIComponent(dms[1]!)] ?? [] });
      if (method === "POST" && sim === "/messages") {
        const author = userById(jsonText(body["userId"]));
        if (!author) return fail(404, "not_found", "unknown user");
        if (!member(author.id))
          return fail(403, "forbidden", `${author.username} is not a member of the server`);
        const m = message(author, jsonText(body["channelId"]), jsonText(body["content"]));
        state.messages.push(m);
        changed();
        return json(m);
      }
      if (method === "GET" && sim === "/commands") return json({ items: commands() });
      if (method === "POST" && sim === "/commands") return runCommand(body);
      if (method === "POST" && sim === "/components") return clickComponent(body);
    }
    return fail(404, "not_found", `mock: no route ${method} ${path}`);
  }

  function events(signal: AbortSignal | null | undefined): Response {
    let ctrl: ReadableStreamDefaultController<Uint8Array> | undefined;
    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        ctrl = c;
        streams.add(c);
        c.enqueue(encoder.encode("retry: 2000\nevent: change\ndata: {}\n\n"));
      },
      cancel() {
        if (ctrl) streams.delete(ctrl);
      },
    });
    signal?.addEventListener("abort", () => {
      if (!ctrl) return;
      streams.delete(ctrl);
      try {
        ctrl.close();
      } catch {
        // already closed
      }
    });
    return new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream" } });
  }

  const fetch: FetchLike = async (input, init) => {
    const url = new URL(input, window.location.origin);
    const method = (init.method ?? "GET").toUpperCase();
    if (url.pathname === `${API}/sim/events`) return events(init.signal);
    let body: Obj = {};
    if (typeof init.body === "string" && init.body) {
      const parsed = JSON.parse(init.body) as Json;
      if (isObject(parsed)) body = parsed;
    }
    if (latency > 0) await new Promise((r) => setTimeout(r, latency));
    if (method !== "GET" && new Headers(init.headers).get("X-Cicord-Request") !== "1")
      return fail(403, "forbidden", "missing X-Cicord-Request header (CSRF protection)");
    return route(method, url, body);
  };

  return {
    fetch,
    reset(): void {
      state = fresh();
      changed();
    },
    /** Test helper: current state. */
    get state(): MockState {
      return state;
    },
  };
}
