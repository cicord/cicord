/**
 * In-browser fake of the CODI runtime for `vp dev --mode mock` and the tests: auth (dev
 * login), admin endpoints (spec/api.md §2–§5) and the simulator (§8), including the SSE
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
  Revision,
  SyncRecord,
  User,
} from "@codi/client";
import { validate } from "../schema-form/validate.ts";
import { deepEqual, isObject, jsonText } from "../schema-form/values.ts";
import {
  bot,
  categories,
  channels,
  onboardingBase,
  plugins,
  roles,
  schemas,
  SERVER_ID,
  SERVER_KEY,
  serverSpec,
  unmanagedChannels,
  unmanagedRoles,
  users,
  welcomeBase,
  welcomeOverride,
} from "./data.ts";

type Obj = { [key: string]: Json };

interface Member {
  userId: string;
  nick: string;
  roles: string[];
}

interface Instance {
  bot: string;
  instance: string;
  base: Obj;
  override: Obj;
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
}

const STORAGE = "codi-admin:mock";

const firstLine = (s: string) => s.split("\n")[0]!;

/** What the runtime reports for an announcement channel on a server without Community. */
const COMMUNITY_RAW =
  'reconcile ma-commu: 1 change(s) failed: create channel news: discord CreateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {"type":{"_errors":[{"code":"BASE_TYPE_CHOICES","message":"Value must be one of {0, 2, 4, 6, 13, 14, 15}."}]}}) (announcement channels require the Community feature: Server Settings → Enable Community, then sync again)';
const COMMUNITY_ERROR: HumanError = {
  code: "community_required",
  message: "Announcement and stage channels need the Community feature",
  hint: "Enable Community in Discord (Server Settings → Enable Community), then sync again. Or change the channel type in the YAML.",
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
const hex = () =>
  Array.from({ length: 7 }, () => "0123456789abcdef"[Math.floor(Math.random() * 16)]).join("");

function seed(): MockState {
  const rev: Revision = {
    id: "4f0c2d9",
    sha: "4f0c2d9e8b1a7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
    message: "feat(onboarding): add the notifications step",
    author: "Léa",
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
      { userId: users.mina.id, nick: "", roles: ["r-modo", "r-member"] },
      { userId: bot.id, nick: "", roles: ["r-gardien"] },
    ],
    messages: [
      message(
        bot,
        "ch-rules",
        "**Bienvenue sur Ma Commu !**\n\n1. Sois bienveillant\n2. Pas de spam\n3. Les pings `@everyone` sont réservés au staff",
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
                  label: "J'accepte le règlement",
                  style: "success",
                  url: "",
                  emoji: "✅",
                },
                {
                  type: "button",
                  customId: "rules:more",
                  label: "En savoir plus",
                  style: "secondary",
                  url: "",
                },
              ],
            },
          ],
        },
      ),
      message(users.mina, "ch-general", "Hello tout le monde, soirée Valorant ce soir à 21h ?", {
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
        actor: "codi",
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
        actor: "github:lea",
        action: "revision.reject",
        target: "a91e7c3",
        details: { reason: "invalid config" },
      },
      {
        id: 4,
        time: ago(60 * 26),
        server: SERVER_KEY,
        actor: "codi",
        action: "sync.apply",
        target: SERVER_KEY,
        details: { applied: 1 },
      },
      {
        id: 3,
        time: ago(60 * 50),
        server: SERVER_KEY,
        actor: "codi",
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
        actor: "codi",
        action: "bot.start",
        target: "moderator",
        details: {},
      },
    ],
    welcomed: 0,
    seq: 1,
  };
}

function message(
  author: User,
  channelId: string,
  content: string,
  extra: Partial<Message> = {},
): Message {
  return {
    id: extra.id ?? `m-${Math.random().toString(36).slice(2, 10)}`,
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

function withDefaults(schemaName: string, cfg: Obj): Obj {
  const out: Obj = { ...cfg };
  for (const [k, prop] of Object.entries(schemas[schemaName]?.properties ?? {})) {
    if (out[k] === undefined && prop.default !== undefined) out[k] = prop.default;
  }
  return out;
}

export interface MockRuntimeOptions {
  /** Artificial latency in ms (default 180; 0 in tests). */
  latency?: number;
  /** Persist state in sessionStorage (default true). */
  persist?: boolean;
}

export function createMockRuntime(options: MockRuntimeOptions = {}) {
  const latency = options.latency ?? 180;
  const persist = options.persist ?? true;
  let state: MockState = load();
  const streams = new Set<ReadableStreamDefaultController<Uint8Array>>();
  const encoder = new TextEncoder();

  function load(): MockState {
    if (persist) {
      try {
        const raw = sessionStorage.getItem(STORAGE);
        if (raw) return JSON.parse(raw) as MockState;
      } catch {
        // fresh state
      }
    }
    return seed();
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
    if (m.roles.includes("r-modo")) return "moderator";
    return "member";
  }

  const me = () => {
    const user = state.session ? userById(state.session) : undefined;
    if (!user) return null;
    const role = accessRole(user.id);
    return {
      user,
      servers: role ? [{ key: SERVER_KEY, name: "Ma Commu", role }] : [],
    };
  };

  function effective(inst: Instance): Obj {
    return withDefaults(inst.instance, merge(inst.base, inst.override));
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
            expected: "Discussions générales",
            actual: "discussions random",
          },
          { kind: "role", key: "valorant", field: "color", expected: "#ff4655", actual: "#e91e63" },
        ]
      : [];

  const serverStatus = () => ({
    key: SERVER_KEY,
    name: "Ma Commu",
    discordId: SERVER_ID,
    sync: state.drift ? "OutOfSync" : "Synced",
    health: state.failing ? "Degraded" : "Healthy",
    message: state.failing ? COMMUNITY_RAW : "",
    ...(state.failing ? { error: COMMUNITY_ERROR } : {}),
    lastSync: state.lastSync,
    drift: drift(),
  });

  const plan = () => ({
    server: SERVER_KEY,
    changes: state.drift
      ? [
          {
            action: "update",
            kind: "channel",
            key: "general",
            summary: "update text channel #général",
            fields: { topic: { from: "discussions random", to: "Discussions générales" } },
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
            summary: "reorder 2 channels in Communauté",
          },
          {
            action: "adopt",
            kind: "role",
            key: "ping-events",
            summary: "adopt existing role Notifs événements",
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
          hint: "Set syncPolicy.deletionProtection: false to let CODI delete it, or add it back to the YAML.",
          details: "deletionProtection",
          resource: { action: "delete", kind: "role", key: "VIP 2023" },
        },
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
        username: "Gardien",
        userId: bot.id,
        health: "Healthy",
        message: "",
        servers: [SERVER_KEY],
        plugins: state.instances.map((i) => ({
          instance: i.instance,
          plugin: i.instance,
          version: "0.1.0",
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
      const info = plugins[inst.instance as keyof typeof plugins];
      const schema = schemas[inst.instance]!;
      const editable = Object.entries(schema.properties ?? {})
        .filter(
          ([, p]) =>
            role === "admin" || (role === "moderator" && p["x-codi-access"] === "moderator"),
        )
        .map(([k]) => k)
        .sort();
      return {
        bot: inst.bot,
        instance: inst.instance,
        server: SERVER_KEY,
        plugin: { ...info, commands: undefined },
        config: effective(inst),
        baseConfig: inst.base,
        override: Object.keys(inst.override).length ? inst.override : null,
        schema,
        editable,
        commands: info.commands,
        routes: inst.instance === "onboarding" ? ["GET /steps", "POST /complete"] : [],
        health: "Healthy",
        message: "",
      };
    });
  };

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

  function addAudit(actor: string, action: string, target: string, details: Obj) {
    const id = Math.max(0, ...state.audit.map((a) => Number(a.id))) + 1;
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
    const after = withDefaults(inst.instance, scope === "server" ? cfg : merge(cfg, inst.override));
    const changedKeys = [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(
      (k) => !deepEqual(before[k], after[k]),
    );
    const schema = schemas[inst.instance]!;
    const editable = new Set(
      Object.entries(schema.properties ?? {})
        .filter(([, p]) => role === "admin" || p["x-codi-access"] === "moderator")
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
    template.replaceAll("{member}", memberMention).replaceAll("{server}", "Ma Commu");

  function portalLink(path: string, userId: string) {
    return `${window.location.origin}${path}?codi_token=mock-${userId}-${Date.now().toString(36)}`;
  }

  function join(userId: string): Response {
    if (!userById(userId)) return fail(404, "not_found", "unknown user");
    if (member(userId)) return fail(400, "invalid", "already a member");
    state.members.push({ userId, nick: "", roles: [] });
    const welcome = state.instances.find((i) => i.instance === "welcome")!;
    const cfg = effective(welcome);
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
        "Bienvenue sur **Ma Commu** ! Choisis tes rôles en 30 secondes :",
        {
          components: [
            {
              type: "row",
              components: [
                {
                  type: "button",
                  customId: "",
                  label: "Commencer",
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
        const cfg = effective(state.instances.find((i) => i.instance === "welcome")!);
        const target =
          typeof opts["member"] === "string" && opts["member"] ? opts["member"] : userId;
        return json(result(reply(render(jsonText(cfg["message"]), `<@${target}>`))));
      }
      case "welcome-stats":
        return json(result(reply(`${state.welcomed} membres accueillis.`)));
      case "onboarding": {
        const cfg = effective(state.instances.find((i) => i.instance === "onboarding")!);
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
      const m = message(bot, channelId, "Merci, bonne visite sur **Ma Commu** ! 🎉", {
        ephemeral: true,
      });
      state.messages.push(m);
      changed();
      return json(result(m));
    }
    if (customId === "rules:more") {
      const m = message(bot, channelId, "Le règlement complet est épinglé dans <#ch-rules>.", {
        ephemeral: true,
      });
      const follow = message(bot, channelId, "Une question ? Ping un <@&r-modo>.", {
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
        name: "Ma Commu",
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

    const server = /^\/api\/v1\/servers\/([^/]+)(\/.*)?$/.exec(path);
    if (server) {
      if (decodeURIComponent(server[1]!) !== SERVER_KEY)
        return fail(404, "not_found", "unknown server");
      const rest = server[2] ?? "";
      if (method === "GET" && rest === "")
        return staff(() => json({ ...serverStatus(), spec: serverSpec }));
      if (method === "GET" && rest === "/resources") return staff(() => json(resources()));
      if (method === "GET" && rest === "/plan") return staff(() => json(plan()), true);
      if (method === "POST" && rest === "/sync")
        return staff((v) => {
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
          const items = state.audit.filter((a) => !before || Number(a.id) < before).slice(0, limit);
          return json({ items });
        });
      if (method === "GET" && rest === "/plugins")
        return staff((v) => json({ items: pluginItems(v) }));
      const put = /^\/plugins\/([^/]+)\/([^/]+)\/config$/.exec(rest);
      if (method === "PUT" && put) {
        const inst = state.instances.find((i) => i.bot === put[1] && i.instance === put[2]);
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
    if (method !== "GET" && new Headers(init.headers).get("X-Codi-Request") !== "1")
      return fail(403, "forbidden", "missing X-Codi-Request header (CSRF protection)");
    return route(method, url, body);
  };

  return {
    fetch,
    reset(): void {
      state = seed();
      changed();
    },
    /** Test helper: current state. */
    get state(): MockState {
      return state;
    },
  };
}
