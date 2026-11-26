/**
 * @cicord/sdk/testing — in-memory fakes to unit-test Cicord plugins in Node (Vitest, node:test…).
 *
 * ```ts
 * import plugin from "../src/index.ts";
 * import { createTestContext, callRoute } from "@cicord/sdk/testing";
 *
 * const t = createTestContext({ config: {...}, roles: { pc: { name: "PC" } }, members: [{ id: "1" }] });
 * const res = await callRoute(plugin, t, "GET /steps", { viewer: "1" });
 * ```
 *
 * The fakes follow spec/sdk/cicord-sdk.d.ts. Capabilities are enforced only when `grants` is
 * given; then a call without its grant throws `CapabilityError`, like the real host. Pass the
 * plugin's `schema` (config.schema.json) to get the config defaults the runtime applies.
 *
 * Interactions: `runCommand` (a command or a subcommand path such as `"polls close"`),
 * `runComponent` and `submitModal` call the handler with a recording fake interaction that
 * follows the reply/defer/update/followUp/showModal rules of the runtime; `runAutocomplete`
 * returns the choices of an option's autocomplete handler. Other handlers (events, tasks, schedules, subscriptions) are plain functions:
 * call them with `harness.ctx`, e.g. `await plugin.tasks!.close!(t.ctx, { id: "1" })`.
 *
 * Voice: `harness.setVoice(userId, channel)` connects, moves or (with `null`) disconnects a
 * member and returns the `VoiceStateUpdate` to pass to a `voiceStateUpdate` handler;
 * `member.moveTo()` / `disconnect()` update the same voice states (`voiceMoves` records them).
 */
import type {
  AutocompleteChoice,
  AutocompleteInteraction,
  Channel,
  CommandInteraction,
  CommandWithSubcommandsDefinition,
  ComponentInteraction,
  Context,
  CreateChannelOptions,
  EditChannelOptions,
  Interaction,
  Json,
  KV,
  Member,
  Message,
  MessageInput,
  Modal,
  ModalInteraction,
  Permission,
  PluginDefinition,
  ReplyInput,
  Role,
  RoleRef,
  RouteResponse,
  Server,
  SubcommandDefinition,
  TextChannel,
  User,
  VoiceStateUpdate,
} from "@cicord/sdk";
import { CapabilityError } from "./index.js";

// ───────────────────────────────────────────────────────────── options

export interface TestRoleInit {
  name: string;
  color?: string;
  id?: string;
  /** Hierarchy position (higher is higher). Default: 1, 2, 3… in declaration order. */
  position?: number;
  /** Default false. */
  mentionable?: boolean;
  /** Git declares role membership; bulk replacement must preserve it. */
  declaredMembers?: boolean;
  /** Server-level permissions the role grants (`member.permissions`). Default none. */
  permissions?: Permission[];
}

/** Who holds an access level (servers/<server>.yaml spec.access): user ids and role keys. */
export interface TestPrincipals {
  users?: string[];
  roles?: string[];
}

export interface TestChannelInit {
  name: string;
  type?: Channel["type"];
  id?: string;
  /** Category id (`channel.categoryId`). */
  categoryId?: string;
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

export interface TestFetchResponse {
  status?: number;
  headers?: Record<string, string>;
  /** Response body (string, or a JSON value that is serialized). */
  body?: string | Json;
}

export interface TestContextOptions {
  /**
   * Plugin config. Defaults are applied only when `schema` is given (like the runtime does
   * with config.schema.json); otherwise pass every field the plugin reads.
   */
  config?: Record<string, unknown>;
  /** The plugin's config.schema.json: its `default`s are applied to `config` (see `applyDefaults`). */
  schema?: object;
  /** Fake `ctx.http.fetch`. Without it, fetch throws. Requests are recorded in `requests`. */
  fetch?: (
    url: string,
    init: Parameters<Context["http"]["fetch"]>[1],
  ) => TestFetchResponse | Promise<TestFetchResponse>;
  /** Granted capabilities. Omit to allow everything. */
  grants?: string[];
  /**
   * Intents declared in plugin.yaml (`spec.intents`). Omit to allow everything; when given,
   * `role.members()` throws without ServerMembers, like the host.
   */
  intents?: string[];
  plugin?: { name?: string; version?: string; instance?: string };
  bot?: { key?: string; username?: string };
  server?: {
    id?: string;
    key?: string;
    name?: string;
    iconUrl?: string;
    /** `ctx.server.ownerId`; the owner has every permission and the admin access level. */
    ownerId?: string;
    /** Permissions of `@everyone` (part of every `member.permissions`). Default none. */
    everyonePermissions?: Permission[];
  };
  /** spec.access of the server: `member.access` is "admin" / "moderator" for these. */
  access?: { admins?: TestPrincipals; moderators?: TestPrincipals };
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

/** `allowedMentions` as the host sends it: lists are ids (objects replaced by their id). */
export interface NormalizedAllowedMentions {
  users?: boolean | string[];
  roles?: boolean | string[];
  everyone?: boolean;
  repliedUser?: boolean;
}

export interface SentMessage {
  /** Channel key/id, or `dm:<userId>` for direct messages. */
  to: string;
  message: MessageInput;
  /** The message's `allowedMentions`, normalized; absent when the message has none. */
  allowedMentions?: NormalizedAllowedMentions;
  /** Id of the created message (see `harness.message(id)`). */
  id: string;
}

export interface ModerationAction {
  member: string;
  action: "timeout" | "kick" | "unban" | "ban" | "nickname";
  durationMs?: number;
  reason?: string;
  deleteMessagesSeconds?: number;
  nickname?: string | null;
}

/** A permission overwrite of a channel created (and edited) by the plugin. */
export interface TestOverwrite {
  allow: Permission[];
  deny: Permission[];
}

export interface VoiceMove {
  member: string;
  /** Destination channel id; `null` for `disconnect()`. */
  channel: string | null;
  reason?: string;
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
  /** Message edits (`message.edit()`), in order. */
  readonly edits: { id: string; message: MessageInput }[];
  /** Deleted message ids (`message.delete()`). */
  readonly deletedMessages: { id: string; reason?: string }[];
  readonly reactions: { message: string; emoji: string }[];
  readonly moderation: ModerationAction[];
  /** Channels created with `ctx.server.createChannel()` (resolvable by `{ id }`). */
  readonly createdChannels: { channel: TextChannel; options: CreateChannelOptions }[];
  readonly deletedChannels: { id: string; reason?: string }[];
  /** `ctx.server.editChannel()` calls, in order. */
  readonly editedChannels: { id: string; options: EditChannelOptions }[];
  /** `member.moveTo()` / `member.disconnect()` calls, in order. */
  readonly voiceMoves: VoiceMove[];
  readonly threads: { parent: string; name: string; private: boolean; fromMessage?: string }[];
  /** `ctx.http.fetch()` requests. */
  readonly requests: { url: string; init: Parameters<Context["http"]["fetch"]>[1] }[];
  /** Raw KV contents (values are JSON clones; expired keys are dropped on access). */
  readonly kv: Map<string, Json>;
  /** Live member (roles reflect role changes). */
  member(userId: string): Member | undefined;
  addMember(init: TestMemberInit): Member;
  /** A message sent through the fakes (channel.send, reply, member.send, followUp). */
  message(id: string): Message | undefined;
  /** A channel by key or id (managed, created or DM). */
  channel(ref: string): TextChannel | undefined;
  /** Removes a member (e.g. before calling a memberLeave handler). */
  removeMember(userId: string): void;
  /**
   * Connects a member to a voice channel (key or id), moves them, or disconnects them
   * (`null`), and returns the update a `voiceStateUpdate` handler receives (`previous` is
   * where they were).
   */
  setVoice(userId: string, channel: string | null): VoiceStateUpdate;
  /** The voice channel id a member is connected to. */
  voiceChannel(userId: string): string | undefined;
  /**
   * The overwrites of a channel created by the plugin, after its edits, keyed by target
   * (`"@everyone"`, a role key, `"role:<id>"`, `"member:<id>"`). Category overwrites are not
   * modelled.
   */
  overwrites(channelId: string): Record<string, TestOverwrite>;
}

// ───────────────────────────────────────────────────────────── helpers

/** Builds the error the host throws when `capability` is not granted. */
export function capabilityError(capability: string): CapabilityError {
  return new CapabilityError(`missing capability "${capability}" (add it to grants in bot.yaml)`, {
    capability,
  });
}

/** Every permission name (Administrator and the owner get all of them). */
const ALL_PERMISSIONS = [
  "CreateInstantInvite",
  "KickMembers",
  "BanMembers",
  "Administrator",
  "ManageChannels",
  "ManageServer",
  "AddReactions",
  "ViewAuditLog",
  "PrioritySpeaker",
  "Stream",
  "ViewChannel",
  "SendMessages",
  "SendTTSMessages",
  "ManageMessages",
  "EmbedLinks",
  "AttachFiles",
  "ReadMessageHistory",
  "MentionEveryone",
  "UseExternalEmojis",
  "ViewServerInsights",
  "Connect",
  "Speak",
  "MuteMembers",
  "DeafenMembers",
  "MoveMembers",
  "UseVAD",
  "ChangeNickname",
  "ManageNicknames",
  "ManageRoles",
  "ManageWebhooks",
  "ManageExpressions",
  "UseApplicationCommands",
  "RequestToSpeak",
  "ManageEvents",
  "ManageThreads",
  "CreatePublicThreads",
  "CreatePrivateThreads",
  "UseExternalStickers",
  "SendMessagesInThreads",
  "UseEmbeddedActivities",
  "ModerateMembers",
  "ViewCreatorMonetizationAnalytics",
  "UseSoundboard",
  "CreateExpressions",
  "CreateEvents",
  "UseExternalSounds",
  "SendVoiceMessages",
  "SendPolls",
  "UseExternalApps",
] as const satisfies readonly Permission[];
// Fails type-checking when the spec adds a permission that is missing above.
type MissingPermissions = Exclude<Permission, (typeof ALL_PERMISSIONS)[number]>;
const allPermissionsListed: [MissingPermissions] extends [never] ? true : never = true;
void allPermissionsListed;

const SNOWFLAKE = /^\d{1,20}$/;
const MAX_MENTION_IDS = 100;

/**
 * Validates and normalizes `allowedMentions` like the host: `users` / `roles` are a boolean
 * or at most 100 ids (or objects with an id). Ids must be snowflakes, or ids known to the
 * fakes (`known`), since test ids such as `role-staff` are not snowflakes. Throws a
 * TypeError like the host.
 */
export function normalizeAllowedMentions(
  input: MessageInput | ReplyInput,
  known: (id: string) => boolean = () => false,
): NormalizedAllowedMentions | undefined {
  if (typeof input === "string" || input.allowedMentions === undefined) return undefined;
  const am = input.allowedMentions as Record<string, unknown>;
  const list = (name: "users" | "roles"): boolean | string[] | undefined => {
    const v = am[name];
    if (v === undefined || v === null) return undefined;
    if (typeof v === "boolean") return v;
    if (!Array.isArray(v))
      throw new TypeError(`allowedMentions.${name} must be a boolean or an array of ids`);
    if (v.length > MAX_MENTION_IDS)
      throw new TypeError(`allowedMentions: at most ${MAX_MENTION_IDS} ${name} ids`);
    return v.map((x: unknown) => {
      const id = x !== null && typeof x === "object" ? (x as { id?: unknown }).id : x;
      if (typeof id !== "string" || !(SNOWFLAKE.test(id) || known(id)))
        throw new TypeError(`allowedMentions: ${name} id ${JSON.stringify(id)} is not a snowflake`);
      return id;
    });
  };
  const users = list("users");
  const roles = list("roles");
  return {
    ...(users !== undefined ? { users } : {}),
    ...(roles !== undefined ? { roles } : {}),
    ...(am["everyone"] !== undefined ? { everyone: Boolean(am["everyone"]) } : {}),
    ...(am["repliedUser"] !== undefined ? { repliedUser: Boolean(am["repliedUser"]) } : {}),
  };
}

function clone<T extends Json>(value: T): T {
  return structuredClone(value);
}

type SchemaNode = { [key: string]: unknown };
const isObject = (v: unknown): v is SchemaNode =>
  typeof v === "object" && v !== null && !Array.isArray(v);

function resolveRef(root: SchemaNode, s: SchemaNode): SchemaNode {
  for (let i = 0; i < 8; i++) {
    const ref = s["$ref"];
    if (typeof ref !== "string") return s;
    if (!ref.startsWith("#")) return {};
    let cur: unknown = root;
    for (const raw of ref.slice(1).split("/")) {
      if (raw === "") continue;
      const part = raw.replaceAll("~1", "/").replaceAll("~0", "~");
      cur = isObject(cur) ? cur[part] : undefined;
    }
    if (!isObject(cur)) return {};
    const { $ref: _ignored, ...siblings } = s;
    s = { ...cur, ...siblings };
  }
  return s;
}

function defaultsInObject(root: SchemaNode, schema: SchemaNode, v: SchemaNode, depth: number) {
  if (depth > 32) return;
  schema = resolveRef(root, schema);
  const props = isObject(schema["properties"]) ? schema["properties"] : {};
  for (const [k, raw] of Object.entries(props)) {
    if (!isObject(raw)) continue;
    const sub = resolveRef(root, raw);
    if (!(k in v) && "default" in sub) v[k] = structuredClone(sub["default"]);
    if (k in v) defaultsInValue(root, sub, v[k], depth + 1);
    else if (sub["type"] === "object" && isObject(sub["properties"])) {
      const obj: SchemaNode = {};
      defaultsInObject(root, sub, obj, depth + 1);
      const required = Array.isArray(sub["required"]) ? (sub["required"] as unknown[]) : [];
      // like the runtime: only when the defaults cover the object's required properties
      if (Object.keys(obj).length > 0 && required.every((r) => typeof r !== "string" || r in obj))
        v[k] = obj;
    }
  }
}

function defaultsInValue(root: SchemaNode, schema: SchemaNode, v: unknown, depth: number) {
  if (isObject(v)) defaultsInObject(root, schema, v, depth);
  else if (Array.isArray(v) && depth <= 32) {
    const items = resolveRef(root, schema)["items"];
    if (isObject(items)) for (const e of v) defaultsInValue(root, items, e, depth + 1);
  }
}

/**
 * Returns a copy of `config` with the `default`s of a JSON Schema applied, with the runtime's
 * rules (config.ApplyDefaults): nested objects, object elements of arrays and local `$ref`s;
 * not `additionalProperties` values nor `oneOf`/`anyOf` alternatives.
 */
export function applyDefaults<T extends Record<string, unknown>>(schema: object, config: T): T {
  const out = structuredClone(config) as SchemaNode;
  defaultsInObject(schema as SchemaNode, schema as SchemaNode, out, 0);
  return out as T;
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

/** User id order of `role.members()`: numeric for snowflakes, else by string. */
function idLess(a: string, b: string): boolean {
  if (/^\d+$/.test(a) && /^\d+$/.test(b)) return BigInt(a) < BigInt(b);
  return a < b;
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
  const edits: TestHarness["edits"] = [];
  const deletedMessages: TestHarness["deletedMessages"] = [];
  const reactions: TestHarness["reactions"] = [];
  const moderation: ModerationAction[] = [];
  const bans = new Map<string, { user: User; reason?: string }>();
  const createdChannels: TestHarness["createdChannels"] = [];
  const deletedChannels: TestHarness["deletedChannels"] = [];
  const editedChannels: TestHarness["editedChannels"] = [];
  const voiceMoves: VoiceMove[] = [];
  const voice = new Map<string, string>(); // user id → voice channel id
  const channelOverwrites = new Map<string, Map<string, TestOverwrite>>(); // created channel id → target → overwrite
  const threads: TestHarness["threads"] = [];
  const requests: TestHarness["requests"] = [];
  const kvData = new Map<string, Json>();
  const kvExpiry = new Map<string, number>();

  const roles = new Map<string, Role>();
  const rolePermissions = new Map<string, readonly Permission[]>(); // role id → permissions
  // Methods on a prototype, like the host: a Role's own properties are its data (toEqual and
  // JSON.stringify see only them).
  const roleProto: Pick<Role, "members"> = {
    async members(this: Role, opts = {}) {
      need("discord.members.read");
      if (options.intents && !options.intents.includes("ServerMembers"))
        throw new Error(
          "role.members() needs the ServerMembers intent: declare it in plugin.yaml (spec.intents)",
        );
      const limit = opts.limit ?? 100;
      if (!Number.isInteger(limit) || limit < 1 || limit > 1000)
        throw new TypeError("role.members(): limit must be an integer between 1 and 1000");
      const after = opts.after;
      const held = [...members.values()]
        .filter((m) => m.hasRole({ id: this.id }) && (after === undefined || idLess(after, m.id)))
        .toSorted((a, b) => (idLess(a.id, b.id) ? -1 : idLess(b.id, a.id) ? 1 : 0));
      return Object.freeze(held.slice(0, limit)) as Member[];
    },
  };
  for (const [key, r] of Object.entries(options.roles ?? {})) {
    const id = r.id ?? `role-${key}`;
    const role: Role = Object.assign(Object.create(roleProto) as Pick<Role, "members">, {
      id,
      key,
      name: r.name,
      ...(r.color ? { color: r.color } : {}),
      mention: `<@&${id}>`,
      position: r.position ?? roles.size + 1,
      mentionable: r.mentionable ?? false,
    });
    roles.set(key, role);
    rolePermissions.set(id, r.permissions ?? []);
  }
  const ownerId = options.server?.ownerId;
  const inPrincipals = (p: TestPrincipals | undefined, userId: string, owned: Set<string>) =>
    !!p &&
    ((p.users ?? []).includes(userId) ||
      (p.roles ?? []).some((key) => {
        const role = roles.get(key);
        return role !== undefined && owned.has(role.id);
      }));
  const resolveRole = (ref: RoleRef): Role | undefined => {
    if (typeof ref === "string") return roles.get(ref);
    for (const r of roles.values()) if (r.id === ref.id) return r;
    return undefined;
  };

  const botUser = makeUser({ id: "bot", username: options.bot?.username ?? "TestBot", bot: true });

  const messages = new Map<string, Message>();
  const history = new Map<string, Message[]>(); // channel id → messages, oldest first
  let messageSeq = 0;
  const makeMessage = (channel: TextChannel, author: User, input: MessageInput): Message => {
    const message: Message = {
      // Snowflake-like ids (numeric strings growing with time), like Discord.
      id: String(1_000_000_000_000_000n + BigInt(++messageSeq)),
      content: typeof input === "string" ? input : (input.content ?? ""),
      author,
      channel,
      attachments: [],
      embeds: typeof input === "string" ? [] : (input.embeds ?? []).map((e) => ({ ...e })),
      createdAt: new Date(),
      async reply(reply) {
        need(channel.type === "dm" ? "discord.dm.send" : "discord.messages.send");
        return post(channel, reply);
      },
      async edit(next) {
        if (author.id !== botUser.id) need("discord.messages.manage");
        edits.push({ id: message.id, message: next });
        return makeMessage(channel, author, next);
      },
      async delete(reason) {
        need("discord.messages.manage");
        deletedMessages.push({ id: message.id, ...(reason ? { reason } : {}) });
        messages.delete(message.id);
        history.set(
          channel.id,
          (history.get(channel.id) ?? []).filter((m) => m.id !== message.id),
        );
      },
      async react(emoji) {
        need("discord.reactions.manage");
        reactions.push({ message: message.id, emoji });
      },
    };
    messages.set(message.id, message);
    const list = history.get(channel.id) ?? [];
    list.push(message);
    history.set(channel.id, list);
    return message;
  };
  /** Records a message sent by the bot. */
  const knownId = (id: string): boolean =>
    members.has(id) || [...roles.values()].some((r) => r.id === id);
  const post = (channel: TextChannel, input: MessageInput, to?: string): Message => {
    const allowedMentions = normalizeAllowedMentions(input, knownId);
    const message = makeMessage(channel, botUser, input);
    sent.push({
      to: to ?? channel.key ?? channel.id,
      message: input,
      id: message.id,
      ...(allowedMentions ? { allowedMentions } : {}),
    });
    return message;
  };

  const channels = new Map<string, TextChannel>(); // by key and by id
  const makeChannel = (key: string | undefined, init: TestChannelInit): TextChannel => {
    const id = init.id ?? (key ? `channel-${key}` : `channel-${channels.size + 1}`);
    const channel: TextChannel = {
      id,
      ...(key ? { key } : {}),
      name: init.name,
      type: init.type ?? "text",
      ...(init.categoryId ? { categoryId: init.categoryId } : {}),
      mention: `<#${id}>`,
      async send(input) {
        need(channel.type === "dm" ? "discord.dm.send" : "discord.messages.send");
        return post(channel, input);
      },
      async bulkDelete(ids, reason) {
        need("discord.messages.read");
        need("discord.messages.manage");
        if (
          channel.type === "dm" ||
          ids.length === 0 ||
          ids.length > 100 ||
          new Set(ids).size !== ids.length
        )
          throw new Error("invalid bulk delete");
        const selected = ids
          .map((messageId) => messages.get(messageId))
          .filter((m): m is Message => m?.channel.id === id);
        const cutoff = Date.now() - 14 * 24 * 3600_000;
        if (selected.some((m) => m.createdAt.getTime() <= cutoff))
          throw new Error("message older than 14 days");
        for (const m of selected) await m.delete(reason);
        return selected.length;
      },
      async previewPurge(filter) {
        need("discord.messages.read");
        need("discord.messages.manage");
        if (
          channel.type === "dm" ||
          !Number.isInteger(filter.limit) ||
          filter.limit < 1 ||
          filter.limit > 1000
        )
          throw new Error("invalid purge");
        if (
          (filter.contains || filter.attachments !== undefined) &&
          options.intents &&
          !options.intents.includes("MessageContent")
        )
          throw new Error("purge requires MessageContent");
        const candidates = (history.get(id) ?? [])
          .toReversed()
          .filter(
            (m) =>
              (!filter.before || idLess(m.id, filter.before)) &&
              (!filter.after || idLess(filter.after, m.id)),
          )
          .slice(0, filter.limit);
        const cutoff = Date.now() - 14 * 24 * 3600_000;
        const selected = candidates.filter(
          (m) =>
            m.createdAt.getTime() > cutoff &&
            (!filter.author || m.author.id === filter.author) &&
            (filter.bots === undefined || m.author.bot === filter.bots) &&
            (filter.attachments === undefined || m.attachments.length > 0 === filter.attachments) &&
            (!filter.contains || m.content.includes(filter.contains)),
        );
        return { ids: selected.map((m) => m.id), skipped: candidates.length - selected.length };
      },
      async purge(filter) {
        const { ids, skipped } = await channel.previewPurge(filter);
        let deleted = 0;
        for (let start = 0; start < ids.length; start += 100)
          deleted += await channel.bulkDelete(ids.slice(start, start + 100), filter.reason);
        return { deleted, skipped };
      },
      async messages(opts = {}) {
        need("discord.messages.read");
        // Newest first; `before`/`after` compare snowflakes numerically, like Discord.
        let list = (history.get(id) ?? []).toReversed();
        const { before, after } = opts;
        if (before) list = list.filter((m) => BigInt(m.id) < BigInt(before));
        if (after) list = list.filter((m) => BigInt(m.id) > BigInt(after)).toReversed();
        return list.slice(0, opts.limit ?? 50);
      },
      async message(messageId) {
        need("discord.messages.read");
        return (history.get(id) ?? []).find((m) => m.id === messageId);
      },
      async createThread(name, opts = {}) {
        need("discord.threads.manage");
        threads.push({
          parent: id,
          name,
          private: opts.private ?? false,
          ...(opts.fromMessage ? { fromMessage: opts.fromMessage } : {}),
        });
        return register(undefined, { name, type: "thread" });
      },
    };
    return channel;
  };
  const register = (key: string | undefined, init: TestChannelInit): TextChannel => {
    const channel = makeChannel(key, init);
    if (key) channels.set(key, channel);
    channels.set(channel.id, channel);
    return channel;
  };
  for (const [key, c] of Object.entries(options.channels ?? {})) register(key, c);

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
    const moderate = (entry: ModerationAction): void => {
      need(entry.action === "nickname" ? "discord.members.manage" : "discord.members.moderate");
      moderation.push(entry);
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
      // Server-level, like the host: @everyone + roles; all for the owner and Administrator.
      get permissions() {
        if (user.id === ownerId) return ALL_PERMISSIONS;
        const set = new Set<Permission>(options.server?.everyonePermissions ?? []);
        for (const id of owned) for (const p of rolePermissions.get(id) ?? []) set.add(p);
        if (set.has("Administrator")) return ALL_PERMISSIONS;
        return ALL_PERMISSIONS.filter((p) => set.has(p));
      },
      get access() {
        if (user.id === ownerId || inPrincipals(options.access?.admins, user.id, owned))
          return "admin" as const;
        if (inPrincipals(options.access?.moderators, user.id, owned)) return "moderator" as const;
        return "member" as const;
      },
      hasRole(ref) {
        const role = resolveRole(ref);
        return role ? owned.has(role.id) : false;
      },
      async setRoles(refs, reason) {
        need("discord.roles.assign");
        const wanted = refs.map((ref) => {
          const role = resolveRole(ref);
          if (!role || role.id === server.id) throw new Error("unknown role or @everyone");
          return role.id;
        });
        if (new Set(wanted).size !== wanted.length) throw new Error("duplicate role");
        for (const [key, def] of Object.entries(options.roles ?? {})) {
          const role = roles.get(key);
          if (role && def.declaredMembers && owned.has(role.id) !== wanted.includes(role.id))
            throw new Error("role membership is declared in git");
        }
        for (const id of owned) if (!wanted.includes(id)) change({ id }, "remove", reason);
        for (const id of wanted) if (!owned.has(id)) change({ id }, "add", reason);
      },
      async addRole(ref, reason) {
        change(ref, "add", reason);
      },
      async removeRole(ref, reason) {
        change(ref, "remove", reason);
      },
      async setNickname(nickname, reason) {
        moderate({ member: user.id, action: "nickname", nickname, ...(reason ? { reason } : {}) });
      },
      async removeTimeout(reason) {
        await this.timeout(0, reason);
      },
      async timeout(durationMs, reason) {
        moderate({ member: user.id, action: "timeout", durationMs, ...(reason ? { reason } : {}) });
      },
      async kick(reason) {
        moderate({ member: user.id, action: "kick", ...(reason ? { reason } : {}) });
      },
      async ban(opts = {}) {
        moderate({
          member: user.id,
          action: "ban",
          ...(opts.reason ? { reason: opts.reason } : {}),
          ...(opts.deleteMessagesSeconds !== undefined
            ? { deleteMessagesSeconds: opts.deleteMessagesSeconds }
            : {}),
        });
        bans.set(user.id, { user, ...(opts.reason ? { reason: opts.reason } : {}) });
      },
      async send(input) {
        need("discord.dm.send");
        const dm =
          channels.get(`dm-${user.id}`) ??
          register(undefined, { name: "dm", type: "dm", id: `dm-${user.id}` });
        return post(dm, input, `dm:${user.id}`);
      },
      async moveTo(ref, reason) {
        need("discord.voice.move");
        if (ref === null || ref === undefined)
          throw new TypeError(
            "moveTo: a voice channel is required (use disconnect() to disconnect)",
          );
        const target = findChannel(typeof ref === "string" ? ref : { id: ref.id });
        if (!target) throw new Error(`unknown channel ${JSON.stringify(ref)}`);
        if (target.type !== "voice" && target.type !== "stage")
          throw new Error(`channel ${target.id} is not a voice channel`);
        if (!voice.has(user.id)) throw new Error("target user is not connected to voice");
        voice.set(user.id, target.id);
        voiceMoves.push({ member: user.id, channel: target.id, ...(reason ? { reason } : {}) });
      },
      async disconnect(reason) {
        need("discord.voice.move");
        if (!voice.has(user.id)) throw new Error("target user is not connected to voice");
        voice.delete(user.id);
        voiceMoves.push({ member: user.id, channel: null, ...(reason ? { reason } : {}) });
      },
    };
    members.set(user.id, member);
    return member;
  };
  for (const m of options.members ?? []) addMember(m);

  const findChannel = (ref: string | { id: string }): TextChannel | undefined =>
    typeof ref === "string" ? channels.get(ref) : channels.get(ref.id);

  const voiceSettings = (
    fn: string,
    isVoice: boolean,
    opts: { userLimit?: number; bitrate?: number },
  ) => {
    if (opts.userLimit !== undefined) {
      if (!isVoice) throw new TypeError(`${fn}(): userLimit is only for voice channels`);
      if (!Number.isInteger(opts.userLimit) || opts.userLimit < 0 || opts.userLimit > 99)
        throw new TypeError(`${fn}(): userLimit must be an integer between 0 (no limit) and 99`);
    }
    if (opts.bitrate !== undefined) {
      if (!isVoice) throw new TypeError(`${fn}(): bitrate is only for voice channels`);
      if (!Number.isInteger(opts.bitrate) || opts.bitrate < 8000 || opts.bitrate > 384000)
        throw new TypeError(`${fn}(): bitrate must be an integer between 8000 and 384000`);
    }
  };
  const checkTarget = (fn: string, target: string): void => {
    if (target === "@everyone" || /^(role|member):/.test(target)) return;
    if (!roles.has(target)) throw new Error(`${fn}(): overwrite target "${target}": unknown role`);
  };
  const ownedChannel = (fn: string, ref: string | { id: string }): TextChannel => {
    const channel = findChannel(ref);
    if (!channel || !createdChannels.some((c) => c.channel.id === channel.id))
      throw new Error(
        `${fn}(): channel ${JSON.stringify(ref)} is not owned by this plugin instance`,
      );
    return channel;
  };
  const refOf = (ref: string | { id: string }) => (typeof ref === "string" ? ref : { id: ref.id });
  const voiceUpdate = (
    userId: string,
    channelId: string | undefined,
    previousId: string | undefined,
  ): VoiceStateUpdate => {
    const member = members.get(userId);
    const user = member?.user ?? makeUser({ id: userId });
    const channel = channelId ? channels.get(channelId) : undefined;
    const previous = previousId ? channels.get(previousId) : undefined;
    return Object.freeze({
      user,
      ...(member ? { member } : {}),
      ...(channel ? { channel } : {}),
      ...(previous ? { previous } : {}),
      selfMute: false,
      selfDeaf: false,
      streaming: false,
      video: false,
    });
  };

  const serverKey = options.server?.key ?? "test-server";
  const server: Server = {
    id: options.server?.id ?? "server-1",
    key: serverKey,
    name: options.server?.name ?? "Test Server",
    ...(options.server?.iconUrl ? { iconUrl: options.server.iconUrl } : {}),
    ...(ownerId !== undefined ? { ownerId } : {}),
    async ban(id, opts = {}) {
      need("discord.members.moderate");
      if (
        opts.deleteMessagesSeconds !== undefined &&
        (!Number.isInteger(opts.deleteMessagesSeconds) ||
          opts.deleteMessagesSeconds < 0 ||
          opts.deleteMessagesSeconds > 604800)
      )
        throw new Error("invalid deleteMessagesSeconds");
      const member = members.get(id);
      if (member) {
        await member.ban(opts);
        return;
      }
      bans.set(id, { user: makeUser({ id }), ...(opts.reason ? { reason: opts.reason } : {}) });
      moderation.push({ member: id, action: "ban", ...opts });
    },
    async unban(id, reason) {
      need("discord.members.moderate");
      if (bans.delete(id))
        moderation.push({ member: id, action: "unban", ...(reason ? { reason } : {}) });
    },
    async bans({ limit = 100, after }: { limit?: number; after?: string } = {}) {
      need("discord.members.moderate");
      if (!Number.isInteger(limit) || limit < 1 || limit > 1000)
        throw new Error("bans(): limit must be between 1 and 1000");
      return [...bans.entries()]
        .filter(([id]) => !after || idLess(after, id))
        .toSorted(([a], [b]) => (a === b ? 0 : idLess(a, b) ? -1 : 1))
        .slice(0, limit)
        .map(([, entry]) => entry);
    },
    channel(ref) {
      need("discord.channels.read");
      const found = findChannel(ref);
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
      let categoryId: string | undefined;
      if (opts.category !== undefined) {
        const category = findChannel(opts.category);
        // A category id that is not declared is accepted, like the host ({ id } of an unmanaged one).
        if (!category && typeof opts.category === "string")
          throw new Error(`unknown category ${JSON.stringify(opts.category)}`);
        categoryId = category?.id ?? (opts.category as { id: string }).id;
      }
      for (const target of Object.keys(opts.overwrites ?? {})) checkTarget("createChannel", target);
      voiceSettings("createChannel", opts.type === "voice", opts);
      const channel = register(undefined, {
        name: opts.name,
        type: opts.type ?? "text",
        ...(categoryId ? { categoryId } : {}),
      });
      createdChannels.push({ channel, options: opts });
      const ows = new Map<string, TestOverwrite>();
      for (const [target, o] of Object.entries(opts.overwrites ?? {}))
        ows.set(target, { allow: [...(o.allow ?? [])], deny: [...(o.deny ?? [])] });
      channelOverwrites.set(channel.id, ows);
      return channel;
    },
    async editChannel(ref, opts) {
      need("discord.channels.manage");
      const channel = ownedChannel("editChannel", refOf(ref));
      if (opts.name !== undefined && (opts.name === "" || Array.from(opts.name).length > 100))
        throw new TypeError("editChannel(): name must be 1-100 characters");
      voiceSettings("editChannel", channel.type === "voice" || channel.type === "stage", opts);
      const ows = channelOverwrites.get(channel.id) ?? new Map<string, TestOverwrite>();
      for (const [target, patch] of Object.entries(opts.overwrites ?? {})) {
        checkTarget("editChannel", target);
        if (patch === null) {
          ows.delete(target);
          continue;
        }
        const allow = patch.allow ?? [],
          deny = patch.deny ?? [],
          clear = patch.clear ?? [];
        if (
          allow.some((p) => deny.includes(p) || clear.includes(p)) ||
          deny.some((p) => clear.includes(p))
        )
          throw new TypeError(
            `editChannel(): overwrite "${target}": a permission is in more than one of allow, deny and clear`,
          );
        const cur = ows.get(target) ?? { allow: [], deny: [] };
        const next: TestOverwrite = {
          allow: [
            ...cur.allow.filter((p) => !deny.includes(p) && !clear.includes(p)),
            ...allow.filter((p) => !cur.allow.includes(p)),
          ],
          deny: [
            ...cur.deny.filter((p) => !allow.includes(p) && !clear.includes(p)),
            ...deny.filter((p) => !cur.deny.includes(p)),
          ],
        };
        if (target.startsWith("member:") && next.allow.length === 0 && next.deny.length === 0)
          ows.delete(target);
        else ows.set(target, next);
      }
      channelOverwrites.set(channel.id, ows);
      if (opts.name !== undefined) (channel as { name: string }).name = opts.name;
      editedChannels.push({ id: channel.id, options: opts });
      return channel;
    },
    async deleteChannel(ref, reason) {
      need("discord.channels.manage");
      const channel = ownedChannel("deleteChannel", refOf(ref));
      channels.delete(channel.id);
      for (const [userId, at] of voice) if (at === channel.id) voice.delete(userId); // disconnected
      deletedChannels.push({ id: channel.id, ...(reason ? { reason } : {}) });
    },
    async voiceMembers(ref) {
      need("discord.members.read");
      if (options.intents && !options.intents.includes("ServerVoiceStates"))
        throw new Error(
          "server.voiceMembers() needs the ServerVoiceStates intent: declare it in plugin.yaml (spec.intents)",
        );
      const channel = findChannel(refOf(ref));
      if (!channel) throw new Error(`unknown channel ${JSON.stringify(ref)}`);
      const ids = [...voice].filter(([, at]) => at === channel.id).map(([id]) => id);
      const list = ids
        .map((id) => members.get(id))
        .filter((m): m is Member => m !== undefined)
        .toSorted((a, b) => (idLess(a.id, b.id) ? -1 : idLess(b.id, a.id) ? 1 : 0));
      return Object.freeze(list) as Member[];
    },
  };

  const live = (key: string): boolean => {
    const exp = kvExpiry.get(key);
    if (exp !== undefined && exp <= Date.now()) {
      kvData.delete(key);
      kvExpiry.delete(key);
      return false;
    }
    return kvData.has(key);
  };
  const kv: KV = {
    async get<T extends Json = Json>(key: string) {
      need("storage.kv");
      if (!live(key)) return undefined;
      return clone(kvData.get(key) as Json) as T;
    },
    async set(key, value, opts) {
      need("storage.kv");
      kvData.set(key, clone(value));
      if (opts?.ttlSeconds) kvExpiry.set(key, Date.now() + opts.ttlSeconds * 1000);
      else kvExpiry.delete(key);
    },
    async delete(key) {
      need("storage.kv");
      kvData.delete(key);
      kvExpiry.delete(key);
    },
    async increment(key, by = 1) {
      need("storage.kv");
      const current = live(key) ? kvData.get(key) : undefined;
      const next = (typeof current === "number" ? current : 0) + by;
      kvData.set(key, next);
      return next;
    },
    async list<T extends Json = Json>(
      opts: { prefix?: string; limit?: number; cursor?: string } = {},
    ) {
      need("storage.kv");
      const keys = [...kvData.keys()]
        .filter((k) => k.startsWith(opts.prefix ?? "") && live(k))
        .toSorted();
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
  const config =
    options.schema !== undefined
      ? applyDefaults(options.schema, options.config ?? {})
      : (options.config ?? {});

  const ctx: Context = {
    plugin: {
      name: options.plugin?.name ?? "test-plugin",
      version: options.plugin?.version ?? "0.0.0",
      instance: options.plugin?.instance ?? options.plugin?.name ?? "test-plugin",
    },
    bot: { key: options.bot?.key ?? "test-bot", user: botUser },
    server,
    config,
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
      async fetch(url, init) {
        need("http.fetch");
        if (!options.fetch)
          throw new Error(
            "createTestContext: ctx.http.fetch is not faked; pass `fetch` in the options",
          );
        requests.push({ url, init });
        const r = await options.fetch(url, init);
        const body =
          typeof r.body === "string" ? r.body : r.body === undefined ? "" : JSON.stringify(r.body);
        return {
          status: r.status ?? 200,
          headers: r.headers ?? {},
          text: async () => body,
          json: async <T>() => JSON.parse(body) as T,
        };
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
        const url = `${portalUrl}${path.startsWith("/") ? path : `/${path}`}?cicord_token=${token}`;
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
    edits,
    deletedMessages,
    reactions,
    moderation,
    createdChannels,
    deletedChannels,
    editedChannels,
    voiceMoves,
    threads,
    requests,
    kv: kvData,
    member: (id) => members.get(id),
    addMember,
    message: (id) => messages.get(id),
    channel: (ref) => channels.get(ref),
    removeMember: (id) => {
      members.delete(id);
    },
    setVoice(userId, ref) {
      const previous = voice.get(userId);
      let next: string | undefined;
      if (ref !== null) {
        const channel = channels.get(ref);
        if (!channel) throw new Error(`setVoice: unknown channel ${JSON.stringify(ref)}`);
        if (channel.type !== "voice" && channel.type !== "stage")
          throw new Error(`setVoice: channel ${channel.id} is not a voice channel`);
        next = channel.id;
        voice.set(userId, next);
      } else {
        voice.delete(userId);
      }
      return voiceUpdate(userId, next, previous);
    },
    voiceChannel: (userId) => voice.get(userId),
    overwrites(channelId) {
      const ows = channelOverwrites.get(channelId);
      if (!ows) throw new Error(`overwrites: channel ${channelId} was not created by the plugin`);
      return Object.fromEntries(
        [...ows].map(([k, o]) => [k, { allow: [...o.allow], deny: [...o.deny] }]),
      );
    },
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
  // Like the runtime: a handler returning nothing answers 204.
  if (response === undefined) return { status: 204, body: undefined, headers: {} };
  return { status: 200, body: response, headers: {} };
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
    const seg = p[i];
    const val = s[i];
    if (seg.startsWith(":")) params[seg.slice(1)] = decodeURIComponent(val);
    else if (seg !== val) return undefined;
  }
  return params;
}

// ───────────────────────────────────────────────────────────── interactions

export interface InteractionCall {
  /** Invoking user id. When it is a member of the test context, `interaction.member` is set. */
  user: string;
  /** Channel key or id of `interaction.channel` (a managed or created channel). */
  channel?: string;
}

export interface CommandCall extends InteractionCall {
  options?: Record<string, unknown>;
}

export interface ComponentCall extends InteractionCall {
  /** Selected values (select menus). */
  values?: string[];
  /** The message carrying the component: a message sent through the fakes, or its id. */
  message?: Message | string;
}

export interface ModalCall extends InteractionCall {
  fields?: Record<string, string>;
}

export interface InteractionResult {
  /** `reply()` calls (initial reply, or the edit of a deferred reply). */
  replies: ReplyInput[];
  followUps: ReplyInput[];
  /** `update()` calls (components only). */
  updates: MessageInput[];
  deferred: { ephemeral?: boolean } | undefined;
  /** The modal opened with `showModal()`. */
  modal: Modal | undefined;
}

/** Result of `runCommand` (same shape as the other interaction helpers). */
export type CommandResult = InteractionResult;

type Phase = "none" | "deferred" | "replied" | "updated" | "modal";

/**
 * A recording interaction following the runtime's rules (https://cicord.dev/docs/plugins/writing/, "Interactions"):
 * `reply()` once (or after a deferral), then `followUp()`; `showModal()` first; `update()`
 * for components only.
 */
function fakeInteraction(
  harness: TestHarness,
  kind: "command" | "component" | "modal",
  id: string,
  call: InteractionCall,
  result: InteractionResult,
): Interaction & { update: (message: MessageInput) => Promise<void> } {
  const member = harness.member(call.user);
  const user = member?.user ?? makeUser({ id: call.user });
  const channel = call.channel !== undefined ? harness.channel(call.channel) : undefined;
  if (call.channel !== undefined && !channel)
    throw new Error(`unknown channel ${JSON.stringify(call.channel)} for the interaction`);
  let phase: Phase = "none";
  let deferredUpdate = false;
  const known = (candidate: string): boolean => {
    if (harness.member(candidate)) return true;
    try {
      harness.ctx.server.role({ id: candidate });
      return true;
    } catch {
      return false;
    }
  };
  // Invalid allowedMentions throw, like the host.
  const checkMentions = (message: MessageInput | ReplyInput): void => {
    normalizeAllowedMentions(message, known);
  };
  const followUp = async (message: ReplyInput): Promise<Message> => {
    if (phase === "none") throw new Error("followUp(): the interaction has not been answered yet");
    checkMentions(message);
    result.followUps.push(message);
    // Follow-ups need no capability: the message is not recorded in `sent`.
    return {
      id: `followup-${result.followUps.length}`,
      content: typeof message === "string" ? message : (message.content ?? ""),
      author: harness.ctx.bot.user,
      channel: channel ?? {
        id: "interaction-channel",
        name: "interaction",
        type: "text",
        mention: "<#interaction-channel>",
        send: async () => {
          throw new Error("not supported in tests: pass `channel` in the call");
        },
        previewPurge: async () => {
          throw new Error("pass a channel to test previewPurge");
        },
        bulkDelete: async () => {
          throw new Error("pass a channel to test bulkDelete");
        },
        purge: async () => {
          throw new Error("pass a channel to test purge");
        },
        messages: async () => [],
        message: async () => undefined,
        createThread: async () => {
          throw new Error("not supported in tests: pass `channel` in the call");
        },
      },
      attachments: [],
      embeds: [],
      createdAt: new Date(),
      reply: async () => {
        throw new Error("not supported in tests: pass `channel` in the call");
      },
      edit: async () => {
        throw new Error("not supported in tests: pass `channel` in the call");
      },
      delete: async () => {},
      react: async () => {},
    };
  };
  return {
    id: `interaction-${kind}-${id}`,
    user,
    ...(member ? { member } : {}),
    ...(channel ? { channel } : {}),
    get replied() {
      return phase === "replied" || phase === "updated" || phase === "modal";
    },
    get deferred() {
      return phase === "deferred";
    },
    async reply(message) {
      if (phase === "replied" || phase === "modal")
        throw new Error("reply(): the interaction was already answered: use followUp()");
      if (phase === "updated" || (phase === "deferred" && deferredUpdate)) {
        await followUp(message);
        return;
      }
      checkMentions(message);
      phase = "replied";
      result.replies.push(message);
    },
    async defer(opts) {
      if (phase === "deferred") return;
      if (phase !== "none") throw new Error("defer(): the interaction was already answered");
      phase = "deferred";
      deferredUpdate = kind === "component" && opts?.ephemeral === undefined;
      result.deferred = opts ?? {};
    },
    followUp,
    async showModal(modal) {
      if (kind === "modal") throw new Error("showModal(): a modal cannot open another modal");
      if (phase !== "none") throw new Error("showModal(): must be the first response");
      phase = "modal";
      result.modal = modal;
    },
    async update(message) {
      if (kind !== "component") throw new Error("update(): only for component interactions");
      if (phase === "replied" || phase === "modal" || (phase === "deferred" && !deferredUpdate))
        throw new Error("update(): the interaction was already answered");
      checkMentions(message);
      phase = "updated";
      result.updates.push(message);
    },
  };
}

const emptyResult = (): InteractionResult => ({
  replies: [],
  followUps: [],
  updates: [],
  deferred: undefined,
  modal: undefined,
});

/** Like the runtime: the full local id, then the part before the first ":" (`"vote:42"` → `vote`). */
function lookup<F>(table: Record<string, F> | undefined, id: string): F | undefined {
  if (!table) return undefined;
  if (Object.hasOwn(table, id)) return table[id];
  const i = id.indexOf(":");
  return i > 0 && Object.hasOwn(table, id.slice(0, i)) ? table[id.slice(0, i)] : undefined;
}

type Leaf = {
  command: string;
  group?: string;
  subcommand?: string;
  definition: SubcommandDefinition;
};

/**
 * Like the runtime: resolves a command path ("poll", "polls close", "polls admin purge") to
 * the command or subcommand that runs.
 */
function resolveCommand(plugin: PluginDefinition, path: string, caller: string): Leaf {
  const parts = path.trim().split(/\s+/);
  const [command = "", ...rest] = parts;
  const c =
    plugin.commands && Object.hasOwn(plugin.commands, command)
      ? plugin.commands[command]
      : undefined;
  if (!c) throw new Error(`${caller}: no command "${command}"`);
  const withSubs = c as CommandWithSubcommandsDefinition;
  const hasSubs = withSubs.subcommands !== undefined || withSubs.groups !== undefined;
  if (rest.length === 0) {
    if (hasSubs)
      throw new Error(
        `${caller}: command "${command}" needs a subcommand ("${command} <subcommand>")`,
      );
    return { command, definition: c as SubcommandDefinition };
  }
  const own = <T>(table: Record<string, T> | undefined, key: string): T | undefined =>
    table && Object.hasOwn(table, key) ? table[key] : undefined;
  if (rest.length === 1) {
    const sub = own(withSubs.subcommands, rest[0]);
    if (!sub) throw new Error(`${caller}: command "${command}" has no subcommand "${rest[0]}"`);
    return { command, subcommand: rest[0], definition: sub };
  }
  const group = own(withSubs.groups, rest[0]);
  const sub = rest.length === 2 ? own(group?.subcommands, rest[1]) : undefined;
  if (!sub) throw new Error(`${caller}: no subcommand "${path}"`);
  return { command, group: rest[0], subcommand: rest[1], definition: sub };
}

/**
 * Runs a slash command handler with a recording fake interaction. `path` is the command
 * name, or the subcommand path for commands with subcommands: `"polls close"`,
 * `"polls admin purge"` (`interaction.subcommand` / `interaction.group` are set).
 */
export async function runCommand(
  plugin: PluginDefinition,
  harness: TestHarness,
  path: string,
  call: CommandCall,
): Promise<CommandResult> {
  const leaf = resolveCommand(plugin, path, "runCommand");
  const result = emptyResult();
  const base = fakeInteraction(harness, "command", path, call, result);
  const { update: _update, ...rest } = base;
  const interaction = Object.defineProperties(
    {
      ...rest,
      command: leaf.command,
      ...(leaf.group ? { group: leaf.group } : {}),
      ...(leaf.subcommand ? { subcommand: leaf.subcommand } : {}),
      options: (call.options ?? {}) as CommandInteraction["options"],
    },
    {
      replied: { get: () => base.replied },
      deferred: { get: () => base.deferred },
    },
  ) as CommandInteraction;
  await leaf.definition.run(harness.ctx, interaction);
  return result;
}

/** Length in Unicode code points, like Discord (and the runtime) count characters. */
function codePoints(s: string): number {
  let n = 0;
  for (const _ of s) n++;
  return n;
}

export interface AutocompleteCall extends InteractionCall {
  /** Name of the option being typed (it must have `autocomplete`). */
  focused: string;
  /** Text typed so far (default ""). */
  value?: string;
  /** Other options already filled in (raw values: ids for users, roles, channels). */
  options?: Record<string, string | number | boolean>;
}

/**
 * Runs the `autocomplete` handler of an option like the runtime does and returns the
 * choices. The result is checked like the runtime checks it (at most 25 choices, names of
 * 1–100 characters, values of the option's type): an invalid result throws.
 */
export async function runAutocomplete(
  plugin: PluginDefinition,
  harness: TestHarness,
  path: string,
  call: AutocompleteCall,
): Promise<AutocompleteChoice[]> {
  const leaf = resolveCommand(plugin, path, "runAutocomplete");
  const option = leaf.definition.options?.[call.focused];
  if (!option?.autocomplete)
    throw new Error(`runAutocomplete: option "${call.focused}" of /${path} has no autocomplete`);
  const member = harness.member(call.user);
  const channel = call.channel !== undefined ? harness.channel(call.channel) : undefined;
  if (call.channel !== undefined && !channel)
    throw new Error(`unknown channel ${JSON.stringify(call.channel)} for the interaction`);
  const interaction: AutocompleteInteraction = {
    id: `interaction-autocomplete-${path}`,
    user: member?.user ?? makeUser({ id: call.user }),
    ...(member ? { member } : {}),
    ...(channel ? { channel } : {}),
    command: leaf.command,
    ...(leaf.group ? { group: leaf.group } : {}),
    ...(leaf.subcommand ? { subcommand: leaf.subcommand } : {}),
    options: { ...call.options },
  };
  const choices = await option.autocomplete(harness.ctx, interaction, {
    name: call.focused,
    value: call.value ?? "",
  });
  const out = [...(choices ?? [])];
  if (out.length > 25) throw new Error(`autocomplete returned ${out.length} choices (at most 25)`);
  for (const c of out) {
    if (typeof c?.name !== "string" || c.name.length < 1 || codePoints(c.name) > 100)
      throw new Error("autocomplete choice: name must be 1-100 characters");
    const ok =
      option.type === "string"
        ? typeof c.value === "string" && codePoints(c.value) <= 100
        : typeof c.value === "number" && (option.type !== "integer" || Number.isInteger(c.value));
    if (!ok) throw new Error(`autocomplete choice "${c.name}": value must be a ${option.type}`);
  }
  return out;
}

/**
 * Runs the component handler for a local component id (`"vote:42"` is routed to
 * `components.vote`, `componentId` stays `"vote:42"`).
 */
export async function runComponent(
  plugin: PluginDefinition,
  harness: TestHarness,
  componentId: string,
  call: ComponentCall,
): Promise<InteractionResult> {
  const handler = lookup(plugin.components, componentId);
  if (!handler) throw new Error(`runComponent: no component handler for "${componentId}"`);
  const message = typeof call.message === "string" ? harness.message(call.message) : call.message;
  if (call.message !== undefined && !message)
    throw new Error(`runComponent: unknown message ${JSON.stringify(call.message)}`);
  const result = emptyResult();
  const base = fakeInteraction(
    harness,
    "component",
    componentId,
    {
      ...call,
      ...(call.channel === undefined && message ? { channel: message.channel.id } : {}),
    },
    result,
  );
  const interaction = Object.defineProperties(
    { ...base, componentId, values: [...(call.values ?? [])], ...(message ? { message } : {}) },
    {
      replied: { get: () => base.replied },
      deferred: { get: () => base.deferred },
    },
  ) as ComponentInteraction;
  await handler(harness.ctx, interaction);
  return result;
}

/** Runs the modal submit handler for a modal id (same id fallback as components). */
export async function submitModal(
  plugin: PluginDefinition,
  harness: TestHarness,
  modalId: string,
  call: ModalCall,
): Promise<InteractionResult> {
  const handler = lookup(plugin.modals, modalId);
  if (!handler) throw new Error(`submitModal: no modal handler for "${modalId}"`);
  const result = emptyResult();
  const base = fakeInteraction(harness, "modal", modalId, call, result);
  const { update: _update, ...rest } = base;
  const interaction = Object.defineProperties(
    { ...rest, modalId, fields: { ...call.fields } },
    {
      replied: { get: () => base.replied },
      deferred: { get: () => base.deferred },
    },
  ) as ModalInteraction;
  await handler(harness.ctx, interaction);
  return result;
}
