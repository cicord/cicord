/**
 * @codi/sdk/testing — in-memory fakes to unit-test CODI plugins in Node (Vitest, node:test…).
 *
 * ```ts
 * import plugin from "../src/index.ts";
 * import { createTestContext, callRoute } from "@codi/sdk/testing";
 *
 * const t = createTestContext({ config: {...}, roles: { pc: { name: "PC" } }, members: [{ id: "1" }] });
 * const res = await callRoute(plugin, t, "GET /steps", { viewer: "1" });
 * ```
 *
 * The fakes follow spec/sdk/codi-sdk.d.ts. Capabilities are enforced only when `grants` is
 * given; then a call without its grant throws `CapabilityError`, like the real host.
 */
import type {
  Channel,
  CommandInteraction,
  Context,
  Json,
  KV,
  Member,
  Message,
  MessageInput,
  PluginConfig,
  PluginDefinition,
  ReplyInput,
  Role,
  RoleRef,
  RouteResponse,
  Server,
  TextChannel,
  User,
} from "@codi/sdk";
import { CapabilityError } from "./index.js";

// ───────────────────────────────────────────────────────────── options

export interface TestRoleInit {
  name: string;
  color?: string;
  id?: string;
}

export interface TestChannelInit {
  name: string;
  type?: Channel["type"];
  id?: string;
}

export interface TestMemberInit {
  id: string;
  username?: string;
  displayName?: string;
  nickname?: string;
  avatarUrl?: string;
  bot?: boolean;
  /** Role keys (as in `roles`). */
  roles?: string[];
  joinedAt?: Date;
}

export interface TestContextOptions {
  /** Effective plugin config (defaults are *not* applied here: pass them explicitly). */
  config?: Record<string, unknown>;
  /** Granted capabilities. Omit to allow everything. */
  grants?: string[];
  plugin?: { name?: string; version?: string; instance?: string };
  bot?: { key?: string; username?: string };
  server?: { id?: string; key?: string; name?: string; iconUrl?: string };
  /** Managed roles keyed by role key. */
  roles?: Record<string, TestRoleInit>;
  /** Managed channels keyed by channel key. */
  channels?: Record<string, TestChannelInit>;
  members?: TestMemberInit[];
  /** Base URL used by the fake `ctx.portal.link()`. */
  portalUrl?: string;
}

// ───────────────────────────────────────────────────────────── records

export interface LogEntry {
  level: "debug" | "info" | "warn" | "error";
  message: string;
  fields?: Record<string, Json>;
}

export interface SentMessage {
  /** Channel key/id, or `dm:<userId>` for direct messages. */
  to: string;
  message: MessageInput;
}

export interface RoleChange {
  member: string;
  role: string;
  action: "add" | "remove";
  reason?: string;
}

export interface TestHarness {
  readonly ctx: Context;
  readonly logs: LogEntry[];
  readonly sent: SentMessage[];
  readonly roleChanges: RoleChange[];
  readonly links: { path: string; member?: string; url: string }[];
  readonly published: { topic: string; payload: Json }[];
  readonly scheduled: { id: string; delayMs: number; task: string; payload?: Json }[];
  /** Raw KV contents (values are JSON clones). */
  readonly kv: Map<string, Json>;
  /** Live member (roles reflect role changes). */
  member(userId: string): Member | undefined;
  addMember(init: TestMemberInit): Member;
}

// ───────────────────────────────────────────────────────────── helpers

/** Builds the error the host throws when `capability` is not granted. */
export function capabilityError(capability: string): CapabilityError {
  return new CapabilityError(`missing capability "${capability}" (add it to grants in bot.yaml)`, {
    capability,
  });
}

function clone<T extends Json>(value: T): T {
  return structuredClone(value);
}

function makeUser(init: {
  id: string;
  username?: string;
  displayName?: string;
  avatarUrl?: string;
  bot?: boolean;
}): User {
  const username = init.username ?? `user${init.id}`;
  return {
    id: init.id,
    username,
    displayName: init.displayName ?? username,
    ...(init.avatarUrl ? { avatarUrl: init.avatarUrl } : {}),
    bot: init.bot ?? false,
    mention: `<@${init.id}>`,
  };
}

// ───────────────────────────────────────────────────────────── context

export function createTestContext(options: TestContextOptions = {}): TestHarness {
  const grants = options.grants ? new Set(options.grants) : undefined;
  const need = (capability: string): void => {
    if (grants && !grants.has(capability)) throw capabilityError(capability);
  };

  const logs: LogEntry[] = [];
  const sent: SentMessage[] = [];
  const roleChanges: RoleChange[] = [];
  const links: TestHarness["links"] = [];
  const published: TestHarness["published"] = [];
  const scheduled: TestHarness["scheduled"] = [];
  const kvData = new Map<string, Json>();

  const roles = new Map<string, Role>();
  for (const [key, r] of Object.entries(options.roles ?? {})) {
    const id = r.id ?? `role-${key}`;
    roles.set(key, {
      id,
      key,
      name: r.name,
      ...(r.color ? { color: r.color } : {}),
      mention: `<@&${id}>`,
    });
  }
  const resolveRole = (ref: RoleRef): Role | undefined => {
    if (typeof ref === "string") return roles.get(ref);
    for (const r of roles.values()) if (r.id === ref.id) return r;
    return undefined;
  };

  let messageSeq = 0;
  const makeMessage = (channel: TextChannel, author: User, input: MessageInput): Message => {
    const message: Message = {
      id: `msg-${++messageSeq}`,
      content: typeof input === "string" ? input : (input.content ?? ""),
      author,
      channel,
      attachments: [],
      createdAt: new Date(),
      async reply(reply) {
        need("discord.messages.send");
        sent.push({ to: channel.key ?? channel.id, message: reply });
        return makeMessage(channel, botUser, reply);
      },
      async edit(next) {
        sent.push({ to: channel.key ?? channel.id, message: next });
        return makeMessage(channel, author, next);
      },
      async delete() {
        need("discord.messages.manage");
      },
      async react() {
        need("discord.reactions.manage");
      },
    };
    return message;
  };

  const channels = new Map<string, TextChannel>();
  const makeChannel = (key: string | undefined, init: TestChannelInit): TextChannel => {
    const id = init.id ?? `channel-${key}`;
    const channel: TextChannel = {
      id,
      ...(key ? { key } : {}),
      name: init.name,
      type: init.type ?? "text",
      mention: `<#${id}>`,
      async send(input) {
        need("discord.messages.send");
        sent.push({ to: key ?? id, message: input });
        return makeMessage(channel, botUser, input);
      },
      async messages() {
        need("discord.messages.read");
        return [];
      },
      async createThread(name) {
        need("discord.threads.manage");
        return makeChannel(undefined, { name, type: "thread" });
      },
    };
    return channel;
  };
  for (const [key, c] of Object.entries(options.channels ?? {}))
    channels.set(key, makeChannel(key, c));

  const botUser = makeUser({ id: "bot", username: options.bot?.username ?? "TestBot", bot: true });

  const members = new Map<string, Member>();
  const addMember = (init: TestMemberInit): Member => {
    const user = makeUser(init);
    const owned = new Set<string>();
    for (const key of init.roles ?? []) {
      const role = roles.get(key);
      if (!role) throw new Error(`createTestContext: member ${init.id} has unknown role "${key}"`);
      owned.add(role.id);
    }
    const change = (ref: RoleRef, action: "add" | "remove", reason?: string): void => {
      need("discord.roles.assign");
      const role = resolveRole(ref);
      if (!role) throw new Error(`unknown role ${JSON.stringify(ref)}`);
      if (action === "add") owned.add(role.id);
      else owned.delete(role.id);
      roleChanges.push({
        member: init.id,
        role: role.key ?? role.id,
        action,
        ...(reason ? { reason } : {}),
      });
    };
    const member: Member = {
      user,
      id: user.id,
      ...(init.nickname ? { nickname: init.nickname } : {}),
      displayName: init.nickname ?? user.displayName,
      mention: user.mention,
      get roles() {
        return [...roles.values()].filter((r) => owned.has(r.id));
      },
      joinedAt: init.joinedAt ?? new Date(0),
      hasRole(ref) {
        const role = resolveRole(ref);
        return role ? owned.has(role.id) : false;
      },
      async addRole(ref, reason) {
        change(ref, "add", reason);
      },
      async removeRole(ref, reason) {
        change(ref, "remove", reason);
      },
      async setNickname() {
        need("discord.members.manage");
      },
      async timeout() {
        need("discord.members.moderate");
      },
      async kick() {
        need("discord.members.moderate");
      },
      async ban() {
        need("discord.members.moderate");
      },
      async send(input) {
        need("discord.dm.send");
        sent.push({ to: `dm:${user.id}`, message: input });
        return makeMessage(
          makeChannel(undefined, { name: "dm", id: `dm-${user.id}` }),
          botUser,
          input,
        );
      },
    };
    members.set(user.id, member);
    return member;
  };
  for (const m of options.members ?? []) addMember(m);

  const serverKey = options.server?.key ?? "test-server";
  const server: Server = {
    id: options.server?.id ?? "server-1",
    key: serverKey,
    name: options.server?.name ?? "Test Server",
    ...(options.server?.iconUrl ? { iconUrl: options.server.iconUrl } : {}),
    channel(ref) {
      need("discord.channels.read");
      const found =
        typeof ref === "string"
          ? channels.get(ref)
          : [...channels.values()].find((c) => c.id === ref.id);
      if (!found) throw new Error(`unknown channel ${JSON.stringify(ref)}`);
      return found;
    },
    role(ref) {
      const role = resolveRole(ref);
      if (!role) throw new Error(`unknown role ${JSON.stringify(ref)}`);
      return role;
    },
    async member(userId) {
      need("discord.members.read");
      return members.get(userId);
    },
    async createChannel(opts) {
      need("discord.channels.manage");
      const channel = makeChannel(undefined, { name: opts.name, type: opts.type ?? "text" });
      return channel;
    },
    async deleteChannel() {
      need("discord.channels.manage");
    },
  };

  const kv: KV = {
    async get<T extends Json = Json>(key: string) {
      need("storage.kv");
      const v = kvData.get(key);
      return (v === undefined ? undefined : clone(v)) as T | undefined;
    },
    async set(key, value) {
      need("storage.kv");
      kvData.set(key, clone(value));
    },
    async delete(key) {
      need("storage.kv");
      kvData.delete(key);
    },
    async increment(key, by = 1) {
      need("storage.kv");
      const current = kvData.get(key);
      const next = (typeof current === "number" ? current : 0) + by;
      kvData.set(key, next);
      return next;
    },
    async list<T extends Json = Json>(
      opts: { prefix?: string; limit?: number; cursor?: string } = {},
    ) {
      need("storage.kv");
      const keys = [...kvData.keys()].filter((k) => k.startsWith(opts.prefix ?? "")).sort();
      const start = opts.cursor ? Number(opts.cursor) : 0;
      const limit = opts.limit ?? 100;
      const page = keys.slice(start, start + limit);
      const items = page.map((key) => ({ key, value: clone(kvData.get(key) as Json) as T }));
      const next = start + limit < keys.length ? String(start + limit) : undefined;
      return next ? { items, cursor: next } : { items };
    },
  };

  const log = (level: LogEntry["level"]) => (message: string, fields?: Record<string, Json>) => {
    logs.push({ level, message, ...(fields ? { fields } : {}) });
  };

  let scheduleSeq = 0;
  const portalUrl = (options.portalUrl ?? "https://portal.test").replace(/\/$/, "");

  const ctx: Context = {
    plugin: {
      name: options.plugin?.name ?? "test-plugin",
      version: options.plugin?.version ?? "0.0.0",
      instance: options.plugin?.instance ?? options.plugin?.name ?? "test-plugin",
    },
    bot: { key: options.bot?.key ?? "test-bot", user: botUser },
    server,
    config: (options.config ?? {}) as Readonly<PluginConfig>,
    log: { debug: log("debug"), info: log("info"), warn: log("warn"), error: log("error") },
    kv,
    schedule: {
      async after(delayMs, task, payload) {
        need("scheduler");
        const id = `task-${++scheduleSeq}`;
        scheduled.push({ id, delayMs, task, ...(payload === undefined ? {} : { payload }) });
        return { id };
      },
      async cancel(id) {
        need("scheduler");
        const i = scheduled.findIndex((s) => s.id === id);
        if (i >= 0) scheduled.splice(i, 1);
      },
    },
    http: {
      async fetch() {
        need("http.fetch");
        throw new Error("createTestContext: ctx.http.fetch is not faked; stub it in your test");
      },
    },
    events: {
      async publish(topic, payload) {
        need("events.publish");
        published.push({ topic, payload });
      },
    },
    portal: {
      async link(path, opts) {
        need("portal.links");
        const memberId = opts?.member?.id;
        const token = `test-${memberId ?? "anonymous"}`;
        const url = `${portalUrl}${path.startsWith("/") ? path : `/${path}`}?codi_token=${token}`;
        links.push({ path, ...(memberId ? { member: memberId } : {}), url });
        return url;
      },
    },
  };

  return {
    ctx,
    logs,
    sent,
    roleChanges,
    links,
    published,
    scheduled,
    kv: kvData,
    member: (id) => members.get(id),
    addMember,
  };
}

// ───────────────────────────────────────────────────────────── routes

export interface RouteCall {
  /** User id of the viewer (must be a member of the test context), or undefined (anonymous). */
  viewer?: string;
  body?: Json;
  query?: Record<string, string>;
}

export interface RouteResult {
  status: number;
  body: Json | undefined;
  headers: Record<string, string>;
}

/**
 * The runtime's reading of `RouteResponse`: an object whose only keys are `status` (number),
 * `body` and `headers` is a response envelope; anything else is a 200 JSON body.
 */
export function normalizeRouteResponse(response: RouteResponse): RouteResult {
  if (
    response !== null &&
    typeof response === "object" &&
    !Array.isArray(response) &&
    typeof (response as { status?: unknown }).status === "number" &&
    Object.keys(response).every((k) => k === "status" || k === "body" || k === "headers")
  ) {
    const env = response as { status: number; body?: Json; headers?: Record<string, string> };
    return { status: env.status, body: env.body, headers: env.headers ?? {} };
  }
  return { status: 200, body: response as Json, headers: {} };
}

/**
 * Calls a plugin route like the runtime does. `route` is `"<METHOD> <path>"`; the path may
 * be concrete (`"POST /answers/42"`) and is matched against declared patterns (`/answers/:id`).
 * Route `access` is checked: anonymous viewers get 401 unless the route is public.
 */
export async function callRoute(
  plugin: PluginDefinition,
  harness: TestHarness,
  route: string,
  call: RouteCall = {},
): Promise<RouteResult> {
  const [method = "", rawPath = "/"] = route.trim().split(/\s+/, 2);
  const [path = "/", search = ""] = rawPath.split("?", 2);
  const query = { ...Object.fromEntries(new URLSearchParams(search)), ...call.query };

  for (const [key, definition] of Object.entries(plugin.routes ?? {})) {
    if (!definition) continue;
    const [m = "", pattern = ""] = key.split(" ", 2);
    if (m !== method.toUpperCase()) continue;
    const params = matchPath(pattern, path);
    if (!params) continue;

    let viewer: { user: User; member?: Member } | undefined;
    if (call.viewer !== undefined) {
      const member = harness.member(call.viewer);
      viewer = member ? { user: member.user, member } : { user: makeUser({ id: call.viewer }) };
    }
    if (definition.access !== "public" && !viewer) {
      return {
        status: 401,
        body: { error: { code: "unauthenticated", message: "login required" } },
        headers: {},
      };
    }
    if (definition.access === "member" && !viewer?.member) {
      return {
        status: 403,
        body: { error: { code: "forbidden", message: "members only" } },
        headers: {},
      };
    }
    const response = await definition.handler(harness.ctx, {
      params,
      query,
      body: call.body,
      ...(viewer ? { viewer } : {}),
    });
    return normalizeRouteResponse(response);
  }
  return {
    status: 404,
    body: { error: { code: "not_found", message: `no route ${method} ${path}` } },
    headers: {},
  };
}

function matchPath(pattern: string, path: string): Record<string, string> | undefined {
  const p = pattern.split("/").filter(Boolean);
  const s = path.split("/").filter(Boolean);
  if (p.length !== s.length) return undefined;
  const params: Record<string, string> = {};
  for (let i = 0; i < p.length; i++) {
    const seg = p[i] as string;
    const val = s[i] as string;
    if (seg.startsWith(":")) params[seg.slice(1)] = decodeURIComponent(val);
    else if (seg !== val) return undefined;
  }
  return params;
}

// ───────────────────────────────────────────────────────────── commands

export interface CommandCall {
  /** Invoking user id. When it is a member of the test context, `interaction.member` is set. */
  user: string;
  options?: Record<string, unknown>;
}

export interface CommandResult {
  replies: ReplyInput[];
  followUps: ReplyInput[];
  deferred: { ephemeral?: boolean } | undefined;
}

/** Runs a slash command handler with a recording fake interaction. */
export async function runCommand(
  plugin: PluginDefinition,
  harness: TestHarness,
  name: string,
  call: CommandCall,
): Promise<CommandResult> {
  const definition = plugin.commands?.[name];
  if (!definition) throw new Error(`runCommand: no command "${name}"`);
  const result: CommandResult = { replies: [], followUps: [], deferred: undefined };
  const member = harness.member(call.user);
  const user = member?.user ?? makeUser({ id: call.user });
  let replied = false;
  let deferred = false;
  const interaction: CommandInteraction = {
    id: `interaction-${name}`,
    user,
    ...(member ? { member } : {}),
    get replied() {
      return replied;
    },
    get deferred() {
      return deferred;
    },
    command: name,
    options: (call.options ?? {}) as CommandInteraction["options"],
    async reply(message) {
      if (replied) throw new Error("interaction already replied");
      replied = true;
      result.replies.push(message);
    },
    async defer(opts) {
      deferred = true;
      result.deferred = opts ?? {};
    },
    async followUp(message) {
      result.followUps.push(message);
      const channel: TextChannel = {
        id: "interaction-channel",
        name: "interaction",
        type: "text",
        mention: "<#interaction-channel>",
        send: async () => {
          throw new Error("not supported in tests");
        },
        messages: async () => [],
        createThread: async () => {
          throw new Error("not supported in tests");
        },
      };
      const content = typeof message === "string" ? message : (message.content ?? "");
      return {
        id: "followup",
        content,
        author: harness.ctx.bot.user,
        channel,
        attachments: [],
        createdAt: new Date(),
        reply: async () => {
          throw new Error("not supported in tests");
        },
        edit: async () => {
          throw new Error("not supported in tests");
        },
        delete: async () => {},
        react: async () => {},
      };
    },
    async showModal() {
      replied = true;
    },
  };
  await definition.run(harness.ctx, interaction);
  return result;
}
