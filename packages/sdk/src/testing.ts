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
 *
 * Interop: `callProvider` runs a provider query or action like the host, in a fresh module
 * realm without `onStart`, read-only for queries, with contract validation;
 * `fakeProviders` gives a consumer peers that validate its input. `ctx.privacy.pseudonym`
 * only works inside `eraseMember` / `exportMember`, for that member.
 */
import { createHash } from "node:crypto";
import type {
  Attachment,
  UploadFile,
  Action,
  InteropCall,
  AutocompleteChoice,
  AutocompleteInteraction,
  Channel,
  CommandInteraction,
  CommandWithSubcommandsDefinition,
  ComponentInteraction,
  Audience,
  AudienceRelation,
  AuditLogEntry,
  ServerInvite,
  ChannelRef,
  Overlay,
  CaseScope,
  Context,
  CreateChannelOptions,
  EditChannelOptions,
  Interaction,
  Json,
  Member,
  Message,
  MessageInput,
  Modal,
  ModalInteraction,
  Permission,
  PluginDefinition,
  RawRouteResponse,
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
import { createHmac } from "node:crypto";
import { checkRenderSpec } from "./testing-render.js";
import { testStorage } from "./testing-storage.js";
import { CapabilityError, PluginUnavailableError } from "./index.js";
export {
  callProvider,
  contractViolation,
  fakeProviders,
  readOnlyContext,
  type ContractSchema,
  type FakeProvider,
  type ProviderCall,
  type ProviderLoader,
} from "./testing-interop.js";

/** The host's error for `ctx.privacy.pseudonym` outside a privacy hook for that member. */
const PSEUDONYM_SCOPE = "privacy.pseudonym is scoped to the member of the current privacy hook";

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
  managed?: boolean;
  holders?: string[];
  /** Server-level permissions the role grants (`member.permissions`). Default none. */
  permissions?: Permission[];
}

/** Who holds an access level (servers/<server>.yaml spec.access): user ids and role keys. */
export interface TestPrincipals {
  users?: string[];
  roles?: string[];
}

export interface TestChannelInit {
  overwrites?: Record<string, { allow?: Permission[]; deny?: Permission[] }>;
  nsfw?: boolean;
  /** A thread is unreadable without a known public parent. */
  publicThreadParent?: string;
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
  pending?: boolean;
  memberFlags?: Member["flags"];
  publicFlags?: User["flags"];
  premiumSince?: Date;
  timedOutUntil?: Date;
  serverAvatarUrl?: string;
}

function fileMatches(pattern: string[], path: string[]): boolean {
  if (!pattern.length) return !path.length;
  if (pattern[0] === "**")
    return (
      fileMatches(pattern.slice(1), path) ||
      (path.length > 0 && fileMatches(pattern, path.slice(1)))
    );
  const expr = Array.from(pattern[0], (c) =>
    c === "*" ? ".*" : c === "?" ? "." : `\\u{${c.codePointAt(0)?.toString(16)}}`,
  ).join("");
  return (
    path.length > 0 &&
    new RegExp("^" + expr + "$", "u").test(path[0]) &&
    fileMatches(pattern.slice(1), path.slice(1))
  );
}

export interface TestFetchResponse {
  status?: number;
  headers?: Record<string, string>;
  /** Response body (string, or a JSON value that is serialized). */
  body?: Uint8Array | string | Json;
}

export interface TestContextOptions {
  /** Logical names only. The fake fetch sees opaque handles, never real credentials. */
  secrets?: string[];
  files?: Record<string, string | Uint8Array>;
  /** Exact bytes of the plugin's packaged files, relative to spec.assets. */
  assets?: Record<string, Uint8Array>;
  /** Inject a renderer or image hasher for Node unit tests. cicord plugin test uses the real Go implementation. */
  renderImage?: Context["render"]["image"];
  hashImage?: Context["media"]["hash"];
  /** Per-file upload limit. Defaults to an unboosted server's 10 MiB. */
  uploadLimit?: number;

  analytics?: import("@cicord/sdk").AnalyticsSummary;
  features?: string[];
  plugins?: Context["plugins"];
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
  locales?: Record<string, Record<string, string>>;
  /** Interaction locale for unit handlers. */
  locale?: string;
  /** Audit fixtures, newest first. */
  auditEntries?: AuditLogEntry[];
  invites?: ServerInvite[];
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
    preferredLocale?: string;
    /** `ctx.server.ownerId`; the owner has every permission and the admin access level. */
    ownerId?: string;
    afkChannelId?: string;
    protection?: Awaited<ReturnType<Server["protection"]>>;
    /** Permissions of `@everyone` (part of every `member.permissions`). Default none. */
    everyonePermissions?: Permission[];
    /**
     * `ctx.server.cicordBotIds`: the user ids of the project's other bots on this server.
     * The test bot's own id is always included.
     */
    otherCicordBotIds?: string[];
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
  /** Create a simulated Discord CDN attachment for an incoming slash option or message. */
  attachment(file: UploadFile): Attachment;
  /** Validate files using the simulated server limit. */
  validateFiles(input: MessageInput): void;

  readonly ctx: Context;
  /** Run the administrative erase hook, including scoped removal of member-tagged index data. */
  eraseMember(plugin: PluginDefinition, userId: string): Promise<{ erased: number }>;
  /** Run the administrative export hook. Like erase, it may pseudonymize only `userId`. */
  exportMember(plugin: PluginDefinition, userId: string): Promise<Json>;
  /** Seed a received message, including interaction follow-ups. */
  /**
   * A message received from `author`, as `messageCreate` or `directMessage` get it: it has
   * `member` when the author is a member of the test server. The stored copy that
   * `channel.messages()` and `channel.message(id)` return has no `member`, like Discord.
   */
  receive(channel: string | undefined, author: User, input: MessageInput): Message;
  setReaction(messageId: string, emoji: string, userId: string, present?: boolean): void;
  readonly crossposts: Set<string>;
  readonly typing: string[];
  readonly onceFor: ReturnType<typeof onceFor>;
  readonly logs: LogEntry[];
  readonly sent: SentMessage[];
  readonly roleChanges: RoleChange[];
  readonly links: {
    path: string;
    member?: string;
    scope?: CaseScope;
    expiresAt?: number;
    revoked?: boolean;
    url: string;
  }[];
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
  /**
   * The instance status lines set with `ctx.status.set(key, message)`, by key, as the admin
   * shows them. `null` or `""` removes a line; like the runtime, at most 32 lines are kept.
   */
  readonly status: ReadonlyMap<string, string>;
  /** Live member (roles reflect role changes). */
  member(userId: string): Member | undefined;
  addMember(init: TestMemberInit): Member;
  /** A message sent through the fakes (channel.send, reply, member.send, followUp). */
  votePoll(messageId: string, answerId: number, userId: string, add?: boolean): void;
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
  "PinMessages",
] as const satisfies readonly Permission[];
// Fails type-checking when the spec adds a permission that is missing above.
type MissingPermissions = Exclude<Permission, (typeof ALL_PERMISSIONS)[number]>;
const allPermissionsListed: [MissingPermissions] extends [never] ? true : never = true;
void allPermissionsListed;

/**
 * Discord's permission bitfield as a decimal string, like `Role.permissions`. Bits follow
 * internal/discord/names.go:16-67: ALL_PERMISSIONS is in bit order, except that bits 47 and
 * 48 are unused (SendPolls is 49).
 */
function permissionBitfield(permissions: readonly Permission[]): string {
  let bits = 0n;
  for (const p of permissions) {
    const i = (ALL_PERMISSIONS as readonly Permission[]).indexOf(p);
    if (i >= 0) bits |= 1n << BigInt(i < 47 ? i : i + 2);
  }
  return bits.toString();
}

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
  publicFlags?: User["flags"];
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
    createdAt:
      /^\d{1,20}$/.test(init.id) && BigInt(init.id) <= 0xffffffffffffffffn
        ? new Date(Number(BigInt(init.id) >> 22n) + 1420070400000)
        : new Date(NaN),
    flags: Object.freeze([...(init.publicFlags ?? [])]),
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

const testTranslators = new WeakMap<Context, (locale?: string) => Context["i18n"]>();
function localizedContext(ctx: Context, locale?: string): Context {
  const make = testTranslators.get(ctx);
  return locale && make ? { ...ctx, i18n: make(locale) } : ctx;
}
export function createTestContext(options: TestContextOptions = {}): TestHarness {
  const grants = options.grants ? new Set(options.grants) : undefined;
  const need = (capability: string): void => {
    if (grants && !grants.has(capability)) throw capabilityError(capability);
  };

  let attachmentSeq = 0;
  const validateFiles = (input: MessageInput) => {
    if (typeof input === "string" || input.files === undefined) return;
    if (!Array.isArray(input.files) || input.files.length > 10)
      throw new TypeError("expected at most 10 files");
    const names = new Set<string>();
    for (const f of input.files) {
      if (
        !f.name ||
        f.name === "." ||
        f.name === ".." ||
        Buffer.byteLength(f.name) > 255 ||
        /[/\\]/u.test(f.name) ||
        Array.from(f.name).some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127) ||
        names.has(f.name)
      )
        throw new TypeError("invalid or duplicate upload filename");
      names.add(f.name);
      if (
        !(f.data instanceof Uint8Array) ||
        f.data.byteLength > (options.uploadLimit ?? 10 * 1024 ** 2)
      )
        throw new TypeError("invalid bytes or attachment exceeds upload limit");
      if (Array.from(f.description ?? "").length > 1024)
        throw new TypeError("description exceeds 1024 characters");
      if (f.contentType && (/[\r\n]/u.test(f.contentType) || !f.contentType.includes("/")))
        throw new TypeError("invalid attachment content type");
    }
  };
  const attachmentBytes = new Map<string, Uint8Array>();
  const attachment = (f: UploadFile): Attachment => {
    validateFiles({ files: [f] });
    const data = new Uint8Array(f.data);
    const id = String(++attachmentSeq);
    const url = `https://cdn.discordapp.com/attachments/test/${id}/${encodeURIComponent(f.name)}`;
    // Render sources can be these CDN URLs: the size check reads their bytes.
    attachmentBytes.set(url, data);
    return {
      id,
      filename: f.name,
      url,
      contentType: f.contentType,
      description: f.description,
      size: data.length,
      async bytes() {
        need("discord.messages.read");
        if (data.length > 8 * 1024 ** 2) throw new Error("attachment exceeds 8 MiB");
        return data.slice();
      },
    };
  };
  const assets = new Map(
    Object.entries(options.assets ?? {}).map(([path, data]) => [path, new Uint8Array(data)]),
  );
  if (
    assets.size > 1024 ||
    [...assets.values()].reduce((sum, v) => sum + v.length, 0) > 5 * 1024 ** 2
  )
    throw new Error("plugin assets exceed package quota");
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

  const roles = new Map<string, Role>();
  const rolePermissions = new Map<string, readonly Permission[]>(); // role id → permissions
  // Methods on a prototype, like the host: a Role's own properties are its data (toEqual and
  // JSON.stringify see only them).
  function memberList(opts: { limit?: number; after?: string }, role?: Role): Member[] {
    const method = role ? "role.members()" : "server.members()";
    need("discord.members.read");
    if (options.intents && !options.intents.includes("ServerMembers"))
      throw new Error(
        method + " needs the ServerMembers intent: declare it in plugin.yaml (spec.intents)",
      );
    const limit = opts.limit ?? 100;
    if (!Number.isInteger(limit) || limit < 1 || limit > 1000)
      throw new TypeError(method + ": limit must be an integer between 1 and 1000");
    const after = opts.after;
    const held = [...members.values()]
      .filter(
        (m) =>
          (!role || m.hasRole({ id: role.id })) && (after === undefined || idLess(after, m.id)),
      )
      .toSorted((a, b) => (idLess(a.id, b.id) ? -1 : idLess(b.id, a.id) ? 1 : 0));
    return Object.freeze(held.slice(0, limit)) as Member[];
  }
  const roleProto: Pick<Role, "members"> = {
    async members(this: Role, opts = {}) {
      return memberList(opts, this);
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
      managed: r.managed ?? false,
      declaredMembers: r.declaredMembers ?? r.holders !== undefined,
      permissions: permissionBitfield(r.permissions ?? []),
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
  const dmReceipts = new Set<string>();
  const history = new Map<string, Message[]>(); // channel id → messages, oldest first
  const ballots = new Map<string, Map<number, Set<string>>>();
  function votePoll(id: string, answer: number, user: string, add = true) {
    const poll = messages.get(id)?.poll;
    if (!poll || poll.finalized || !poll.answers.some((a) => a.id === answer))
      throw new Error("unknown answer or closed poll");
    const votes = ballots.get(id) ?? new Map<number, Set<string>>();
    if (add && !poll.multiselect) for (const set of votes.values()) set.delete(user);
    const users = votes.get(answer) ?? new Set<string>();
    if (add) users.add(user);
    else users.delete(user);
    votes.set(answer, users);
    ballots.set(id, votes);
    for (const a of poll.answers) a.votes = votes.get(a.id)?.size ?? 0;
  }
  let messageSeq = 0;
  const pinned = new Set<string>();
  const crossposts = new Set<string>();
  const typing: string[] = [];
  const nonces = new Map<string, Message>();
  const reactionUsers = new Map<string, Map<string, Set<string>>>();
  function setReaction(messageId: string, emoji: string, userId: string, present = true) {
    if (!messages.has(messageId)) throw new Error("unknown message");
    const byEmoji = reactionUsers.get(messageId) ?? new Map<string, Set<string>>();
    const users = byEmoji.get(emoji) ?? new Set<string>();
    if (present) users.add(userId);
    else users.delete(userId);
    if (users.size) byEmoji.set(emoji, users);
    else byEmoji.delete(emoji);
    reactionUsers.set(messageId, byEmoji);
  }
  const makeMessage = (
    channel: TextChannel,
    author: User,
    input: MessageInput,
    reference?: Message["reference"],
    existing?: Message,
  ): Message => {
    validateFiles(input);
    validateLayoutInput(input);
    const id = existing?.id ?? String(1_000_000_000_000_000n + BigInt(++messageSeq));
    const poll = typeof input === "string" ? undefined : input.poll;
    if (
      poll &&
      (!poll.question ||
        poll.question.length > 300 ||
        poll.answers.length < 2 ||
        poll.answers.length > 10 ||
        poll.answers.some((a) => !a.text || Array.from(a.text).length > 55) ||
        !Number.isInteger(poll.durationHours) ||
        poll.durationHours < 1 ||
        poll.durationHours > 768)
    )
      throw new Error("invalid native poll");
    const message: Message = {
      // Snowflake-like ids (numeric strings growing with time), like Discord.
      id,
      url: `https://discord.com/channels/${channel.type === "dm" ? "@me" : (options.server?.id ?? "server-1")}/${channel.id}/${id}`,
      ...(reference ? { reference } : {}),
      get reactions() {
        return [...(reactionUsers.get(id) ?? [])].map(([emoji, users]) => ({
          emoji,
          count: users.size,
          me: users.has(botUser.id),
        }));
      },
      async pin() {
        need("discord.messages.manage");
        pinned.add(id);
      },
      async unpin() {
        need("discord.messages.manage");
        pinned.delete(id);
      },
      async crosspost() {
        need("discord.messages.send");
        if (channel.type !== "announcement" || crossposts.has(id))
          throw new Error("cannot crosspost message");
        crossposts.add(id);
      },
      async reactionUsers(emoji, opts = {}) {
        need("discord.messages.read");
        const limit = opts.limit ?? 25;
        if (!Number.isInteger(limit) || limit < 1 || limit > 100)
          throw new TypeError("limit must be 1–100");
        return [...(reactionUsers.get(id)?.get(emoji) ?? [])]
          .filter((uid) => !opts.after || idLess(opts.after, uid))
          .toSorted((a, b) => (idLess(a, b) ? -1 : idLess(b, a) ? 1 : 0))
          .slice(0, limit)
          .map(
            (uid) =>
              members.get(uid)?.user ?? (uid === botUser.id ? botUser : makeUser({ id: uid })),
          );
      },
      async removeReaction(emoji, userId) {
        need("discord.messages.manage");
        setReaction(id, emoji, userId ?? botUser.id, false);
      },
      content: typeof input === "string" ? input : (input.content ?? ""),
      author,
      channel,
      attachments:
        typeof input !== "string" && input.files
          ? input.files.map(attachment)
          : (existing?.attachments ?? []),
      embeds: typeof input === "string" ? [] : (input.embeds ?? []).map((e) => ({ ...e })),
      createdAt: existing?.createdAt ?? new Date(),
      async reply(reply) {
        need(channel.type === "dm" ? "discord.dm.send" : "discord.messages.send");
        return post(channel, reply, undefined, {
          messageId: id,
          channelId: channel.id,
          ...(channel.type === "dm" ? {} : { serverId: options.server?.id ?? "server-1" }),
        });
      },
      async edit(next) {
        if (author.id !== botUser.id) need("discord.messages.manage");
        const edited = makeMessage(channel, author, next, reference, message);
        edits.push({ id: message.id, message: next });
        return edited;
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
      ...(typeof input !== "string" && input.poll
        ? {
            poll: {
              question: input.poll.question,
              answers: input.poll.answers.map((a, index) => ({ ...a, id: index + 1, votes: 0 })),
              multiselect: !!input.poll.multiselect,
              finalized: false,
            },
          }
        : {}),
      async endPoll() {
        need("discord.messages.send");
        if (!message.poll || author.id !== botUser.id) throw new Error("expected own poll");
        message.poll.finalized = true;
        return message;
      },
      async pollVoters(answer, after) {
        need("discord.messages.read");
        return [...(ballots.get(message.id)?.get(answer) ?? [])]
          .toSorted((a, b) => a.length - b.length || a.localeCompare(b))
          .filter(
            (voterId) =>
              !after ||
              voterId.length > after.length ||
              (voterId.length === after.length && voterId > after),
          )
          .slice(0, 100);
      },
      async react(emoji) {
        need("discord.reactions.manage");
        setReaction(id, emoji, botUser.id);
        reactions.push({ message: message.id, emoji });
      },
    };
    messages.set(message.id, message);
    const list = history.get(channel.id) ?? [];
    const index = existing ? list.findIndex((item) => item.id === existing.id) : -1;
    if (index < 0) list.push(message);
    else list[index] = message;
    history.set(channel.id, list);
    return message;
  };
  /** Records a message sent by the bot. */
  const knownId = (id: string): boolean =>
    members.has(id) || [...roles.values()].some((r) => r.id === id);
  const post = (
    channel: TextChannel,
    input: MessageInput,
    to?: string,
    reference?: Message["reference"],
    author = botUser,
  ): Message => {
    validateLayoutInput(input);
    validateFiles(input);
    const allowedMentions = normalizeAllowedMentions(input, knownId);
    const key =
      typeof input !== "string" && input.nonce ? channel.id + "/" + input.nonce : undefined;
    if (
      typeof input !== "string" &&
      ((input.nonce?.length ?? 0) > 25 || (input.enforceNonce && !input.nonce))
    )
      throw new TypeError("invalid message nonce");
    const previous = key ? nonces.get(key) : undefined;
    if (
      typeof input !== "string" &&
      input.enforceNonce &&
      previous &&
      Date.now() - previous.createdAt.getTime() < 300000
    )
      return previous;
    const message = makeMessage(channel, author, input, reference);
    if (key) nonces.set(key, message);
    sent.push({
      to: to ?? channel.key ?? channel.id,
      message: input,
      id: message.id,
      ...(allowedMentions ? { allowedMentions } : {}),
    });
    return message;
  };

  const channelPolicy = new Map<string, TestChannelInit>();
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
      async pins() {
        need("discord.messages.read");
        return [...pinned]
          .toReversed()
          .map((messageID) => messages.get(messageID))
          .filter((m): m is Message => m?.channel.id === id);
      },
      async typing() {
        need("discord.messages.send");
        if (channel.type === "dm") need("discord.dm.send");
        typing.push(id);
      },
      async sendAs(identity, input) {
        need("discord.webhooks.manage");
        if (channel.type === "dm" || !identity.username || identity.username.length > 80)
          throw new Error("invalid webhook identity or channel");
        return post(
          channel,
          input,
          undefined,
          undefined,
          makeUser({
            id: "webhook-" + id,
            username: identity.username,
            avatarUrl: identity.avatarUrl,
            bot: true,
          }),
        );
      },
      async memberOverwrite(userId) {
        need("discord.channels.read");
        if (!channels.has(id) || channel.type === "dm")
          throw new Error("memberOverwrite requires a current guild channel");
        if (!userId) throw new Error("expected a member ID");
        const row = channelOverwrites.get(id)?.get("member:" + userId);
        return row
          ? { allow: [...row.allow].toSorted(), deny: [...row.deny].toSorted() }
          : undefined;
      },
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
    channelPolicy.set(channel.id, init);
    if (init.overwrites) {
      channelOverwrites.set(
        channel.id,
        new Map(
          Object.entries(init.overwrites).map(([target, patch]) => [
            target,
            { allow: patch.allow ?? [], deny: patch.deny ?? [] },
          ]),
        ),
      );
    }
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
      declaredRoles: Object.entries(options.roles ?? {})
        .filter(([, r]) => r.holders?.includes(user.id))
        .map(([key]) => key),
      user,
      id: user.id,
      ...(init.nickname ? { nickname: init.nickname } : {}),
      displayName: init.nickname ?? user.displayName,
      mention: user.mention,
      get roles() {
        return [...roles.values()].filter((r) => owned.has(r.id));
      },
      ...(init.pending === undefined ? {} : { pending: init.pending }),
      flags: Object.freeze([...(init.memberFlags ?? [])]),
      ...(init.premiumSince ? { premiumSince: init.premiumSince } : {}),
      ...(init.timedOutUntil ? { timedOutUntil: init.timedOutUntil } : {}),
      ...(init.serverAvatarUrl ? { avatarUrl: init.serverAvatarUrl } : {}),
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
      permissionsIn(ref) {
        need("discord.channels.read");
        const channel = policyChannel(ref);
        if (!members.has(user.id) || !channel || channelPolicy.get(channel.id)?.nsfw) return [];
        return [...effective(channel, user.id, owned, user.id === ownerId)];
      },
      canView(ref) {
        const p = this.permissionsIn(ref);
        return p.includes("ViewChannel") && p.includes("ReadMessageHistory");
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
      async send(input, sendOptions) {
        need("discord.dm.send");
        const dm =
          channels.get(`dm-${user.id}`) ??
          register(undefined, { name: "dm", type: "dm", id: `dm-${user.id}` });
        const m = post(dm, input, `dm:${user.id}`);
        if (sendOptions?.retractable) dmReceipts.add(m.id);
        return m;
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
  function policyChannel(ref: ChannelRef | Channel) {
    const c = findChannel(typeof ref === "string" ? ref : { id: ref.id });
    if (!c) return undefined;
    const parent = channelPolicy.get(c.id)?.publicThreadParent;
    const channel = c.type === "thread" ? (parent ? findChannel(parent) : undefined) : c;
    switch (channel?.type) {
      case "text":
      case "voice":
      case "category":
      case "announcement":
      case "stage":
      case "forum":
      case "media":
        return channel;
      default:
        return undefined;
    }
  }
  function effective(
    channel: TextChannel | undefined,
    user: string,
    owned: Set<string>,
    owner = false,
  ): Set<Permission> {
    if (!channel) return new Set();
    const bits = new Set<Permission>(options.server?.everyonePermissions ?? []);
    for (const id of owned) for (const p of rolePermissions.get(id) ?? []) bits.add(p);
    if (owner || bits.has("Administrator")) return new Set(ALL_PERMISSIONS);
    function apply(allow: Iterable<Permission>, deny: Iterable<Permission>) {
      for (const p of deny) bits.delete(p);
      for (const p of allow) bits.add(p);
    }
    const overwrites = channelOverwrites.get(channel.id);
    const everyone = overwrites?.get("@everyone");
    if (everyone) apply(everyone.allow, everyone.deny);
    const allow = new Set<Permission>(),
      deny = new Set<Permission>();
    for (const [target, ow] of overwrites ?? []) {
      const role =
        roles.get(target)?.id ?? (target.startsWith("role:") ? target.slice(5) : undefined);
      if (role && owned.has(role)) {
        for (const p of ow.allow) allow.add(p);
        for (const p of ow.deny) deny.add(p);
      }
    }
    apply(allow, deny);
    const member = overwrites?.get("member:" + user);
    if (member) apply(member.allow, member.deny);
    return bits.has("ViewChannel") ? bits : new Set();
  }
  function reads(bits: Set<Permission>) {
    return bits.has("ViewChannel") && bits.has("ReadMessageHistory");
  }
  function audienceOf(channel: TextChannel | undefined, withoutMember?: string): Audience {
    const out = {
      known: !!channel,
      ...(channel
        ? {
            fingerprint: createHash("sha256")
              .update(
                JSON.stringify({
                  channel: channel.id,
                  ownerId,
                  nsfw: channelPolicy.get(channel.id)?.nsfw,
                  permissions: options.server?.everyonePermissions,
                  roles: [...rolePermissions],
                  overwrites: [...(channelOverwrites.get(channel.id) ?? [])]
                    .filter(([id]) => !withoutMember || id !== "member:" + withoutMember)
                    .map(([id, o]) => [
                      id,
                      [...o.allow]
                        .filter((p) => p === "ViewChannel" || p === "ReadMessageHistory")
                        .toSorted(),
                      [...o.deny]
                        .filter((p) => p === "ViewChannel" || p === "ReadMessageHistory")
                        .toSorted(),
                    ]),
                }),
              )
              .digest("hex"),
          }
        : {}),
      public: false,
      allowRoles: [] as string[],
      allowMembers: [] as string[],
      denyMembers: [] as string[],
    };
    if (!channel) return out;
    out.public = reads(effective(channel, "", new Set())) && !channelPolicy.get(channel.id)?.nsfw;
    for (const [target, ow] of channelOverwrites.get(channel.id) ?? []) {
      const role = resolveRole(target.startsWith("role:") ? { id: target.slice(5) } : target);
      if (
        target !== "@everyone" &&
        !(role && rolePermissions.get(role.id)?.includes("Administrator")) &&
        ow.deny.some(
          (p) => (p === "ViewChannel" || p === "ReadMessageHistory") && !ow.allow.includes(p),
        )
      )
        out.public = false;
    }
    for (const r of roles.values())
      if (!channelPolicy.get(channel.id)?.nsfw && reads(effective(channel, "", new Set([r.id]))))
        out.allowRoles.push(r.id);
    for (const [key, ow] of channelOverwrites.get(channel.id) ?? []) {
      if (!key.startsWith("member:")) continue;
      const user = key.slice(7);
      if (!channelPolicy.get(channel.id)?.nsfw && reads(effective(channel, user, new Set())))
        out.allowMembers.push(user);
      if (ow.deny.includes("ViewChannel") || ow.deny.includes("ReadMessageHistory"))
        out.denyMembers.push(user);
    }
    return out;
  }
  function comparePolicies(
    source: TextChannel | undefined,
    destination: TextChannel | undefined,
  ): AudienceRelation {
    if (
      !source ||
      !destination ||
      (channelPolicy.get(source.id)?.nsfw && !channelPolicy.get(destination.id)?.nsfw)
    )
      return "unknown";
    if (source.id === destination.id) return "same";
    const roleIds = [...roles.values()]
      .filter((r) => !rolePermissions.get(r.id)?.includes("Administrator"))
      .map((r) => r.id);
    if (roleIds.length > 10) return "unknown"; // bounded reference oracle for unit fixtures
    const users = new Set([""]);
    for (const c of [source, destination])
      for (const key of channelOverwrites.get(c.id)?.keys() ?? []) {
        if (key.startsWith("member:")) users.add(key.slice(7));
      }
    let left = false,
      right = false,
      both = false;
    for (let mask = 0; mask < 2 ** roleIds.length; mask++) {
      const assigned = new Set(roleIds.filter((_, i) => (mask & (1 << i)) !== 0));
      for (const user of users) {
        const a = reads(effective(source, user, assigned)),
          b = reads(effective(destination, user, assigned));
        left ||= a && !b;
        right ||= b && !a;
        both ||= a && b;
      }
    }
    if (!left && !right) return "same";
    if (!right) return "narrower";
    if (!left) return "wider";
    return both ? "unknown" : "disjoint";
  }
  const overlayRecords = new Map<string, Overlay & { origin: string }>();
  const scheduledEvents = new Map<string, import("@cicord/sdk").ScheduledEvent>();
  let eventSeq = 0;
  const server: Server = {
    async protection() {
      need("discord.channels.read");
      return structuredClone(
        options.server?.protection ?? {
          known: false,
          selfHeal: false,
          deletionProtection: false,
          paused: false,
          totalResources: 0,
          unmanaged: [],
        },
      );
    },
    ...(options.server?.preferredLocale ? { preferredLocale: options.server.preferredLocale } : {}),
    cicordBotIds: Object.freeze([
      botUser.id,
      ...(options.server?.otherCicordBotIds ?? []).filter((id) => id !== botUser.id),
    ]),
    async members(opts = {}) {
      return memberList(opts);
    },
    async sendDM(userId, input) {
      need("discord.dm.send");
      const dm =
        channels.get(`dm-${userId}`) ??
        register(undefined, { name: "dm", type: "dm", id: `dm-${userId}` });
      return post(dm, input, `dm:${userId}`);
    },
    overlays: {
      async apply(o) {
        need("server.overlays");
        if (
          !o.reason.trim() ||
          !Number.isFinite(+o.until) ||
          +o.until <= Date.now() ||
          +o.until > Date.now() + 7 * 86400000
        )
          throw new Error("invalid overlay");
        for (const channel of Object.values(o.channels ?? {}))
          for (const patch of Object.values(channel.overwrites ?? {}))
            if ("allow" in patch) throw new Error("overlay allows are forbidden");
        overlayRecords.set(o.id, structuredClone({ ...o, origin: "test/test" }));
      },
      async list() {
        need("server.overlays");
        return structuredClone([...overlayRecords.values()].filter((o) => +o.until > Date.now()));
      },
      async remove(id) {
        need("server.overlays");
        overlayRecords.delete(id);
      },
    },
    async incident(a) {
      need("server.incident");
      if (!a.invitesDisabledUntil && !a.dmsDisabledUntil)
        throw new Error("an incident end time is required");
      for (const until of Object.values(a))
        if (+until <= Date.now() || +until > Date.now() + 86400000)
          throw new Error("invalid incident end time");
    },
    async requestSync(reason) {
      need("server.sync");
      if (!reason.trim()) throw new Error("a sync reason is required");
    },
    async scheduledEvents() {
      need("discord.events.read");
      return [...scheduledEvents.values()];
    },
    async createScheduledEvent(input) {
      need("discord.events.manage");
      if (
        !input.name ||
        !Number.isFinite(Date.parse(input.start)) ||
        !!input.channel === !!input.location ||
        (input.location && !input.end)
      )
        throw new Error("invalid event");
      const event: import("@cicord/sdk").ScheduledEvent = {
        ...input,
        id: String(++eventSeq),
        status: "scheduled",
        async edit(changes) {
          need("discord.events.manage");
          if (!scheduledEvents.has(event.id)) throw new Error("unknown event");
          Object.assign(event, changes);
          return event;
        },
        async delete() {
          need("discord.events.manage");
          scheduledEvents.delete(event.id);
        },
        async users() {
          need("discord.events.read");
          return [];
        },
      };
      scheduledEvents.set(event.id, event);
      return event;
    },
    async invites() {
      need("discord.invites.read");
      return [...(options.invites ?? [])];
    },
    async auditLog(query = {}) {
      need("discord.auditlog.read");
      if (options.intents && !options.intents.includes("ServerModeration"))
        throw new Error("auditLog requires ServerModeration");
      const limit = query.limit ?? 50;
      if (!Number.isInteger(limit) || limit < 1 || limit > 100)
        throw new Error("expected 1–100 audit entries");
      return (options.auditEntries ?? [])
        .filter(
          (e) =>
            (!query.action || query.action === e.action) &&
            (!query.actorId || query.actorId === e.actorId) &&
            (!query.before || idLess(e.id, query.before)),
        )
        .slice(0, limit);
    },
    audience(ref) {
      need("discord.channels.read");
      return audienceOf(policyChannel(ref));
    },
    async verifyReaders(ref, readers) {
      need("discord.channels.read");
      const source = policyChannel(ref);
      const ceilingId = ":reader-ceiling";
      const ceiling = source && { ...source, id: ceilingId };
      const overwrites = new Map<string, TestOverwrite>();
      overwrites.set("@everyone", { allow: [], deny: ["ViewChannel", "ReadMessageHistory"] });
      for (const id of readers.roles)
        overwrites.set("role:" + id, { allow: ["ViewChannel", "ReadMessageHistory"], deny: [] });
      for (const id of readers.members)
        overwrites.set("member:" + id, { allow: ["ViewChannel", "ReadMessageHistory"], deny: [] });
      channelOverwrites.set(ceilingId, overwrites);
      try {
        const rel = comparePolicies(ceiling, source);
        const audience = audienceOf(source);
        if (readers.viewer) need("discord.members.read");
        return {
          known: audience.known,
          withoutMemberFingerprint: readers.withoutMember
            ? audienceOf(source, readers.withoutMember).fingerprint
            : undefined,
          confined: rel === "same" || rel === "narrower",
          fingerprint: audience.fingerprint ?? "",
          viewerCanRead:
            !!source &&
            !channelPolicy.get(source.id)?.nsfw &&
            !!readers.viewer &&
            !!members.get(readers.viewer)?.canView(source),
          destination: readers.destination
            ? comparePolicies(source, policyChannel(readers.destination))
            : "unknown",
        };
      } finally {
        channelOverwrites.delete(ceilingId);
      }
    },
    compareAudience(source, destination) {
      need("discord.channels.read");
      return comparePolicies(policyChannel(source), policyChannel(destination));
    },
    id: options.server?.id ?? "server-1",
    key: serverKey,
    name: options.server?.name ?? "Test Server",
    ...(options.server?.iconUrl ? { iconUrl: options.server.iconUrl } : {}),
    ...(ownerId !== undefined ? { ownerId } : {}),
    async editRetractableDM(id, input) {
      need("discord.dm.send");
      const message = messages.get(id);
      if (!dmReceipts.has(id) || !message) throw new Error("Unknown DM receipt");
      await message.edit(input);
    },
    async retractDM(id) {
      need("discord.dm.send");
      if (!dmReceipts.delete(id)) return;
      const m = messages.get(id);
      if (m) {
        messages.delete(id);
        history.set(
          m.channel.id,
          (history.get(m.channel.id) ?? []).filter((v) => v.id !== id),
        );
        deletedMessages.push({ id });
      }
    },
    ...(options.server?.afkChannelId ? { afkChannelId: options.server.afkChannelId } : {}),
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
    async recoverOwnedChannel(request) {
      need("discord.channels.manage");
      // internal/plugin/ops.go:1540-1600: by provisionKey ("owned" or "missing") or by topic.
      if (typeof request !== "string" && request.provisionKey !== "") {
        const id = provisioned(request.provisionKey);
        return id ? { status: "owned", id } : { status: "missing" };
      }
      const topic = typeof request === "string" ? request : "";
      if (!topic || Array.from(topic).length > 1024)
        throw new TypeError("provide the original provisioning topic");
      const found = createdChannels.filter(
        (c) => c.options.topic === topic && channels.has(c.channel.id),
      );
      if (found.length === 1) return { status: "owned", id: found[0].channel.id };
      return { status: found.length ? "conflict" : "missing" };
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
      // internal/plugin/ops.go:1680-1720: a provisionKey whose channel still exists returns
      // that channel instead of creating another one.
      const key = opts.provisionKey;
      if (key) {
        const existing = provisioned(key);
        if (existing) return channels.get(existing)!;
      }
      const channel = register(undefined, {
        name: opts.name,
        type: opts.type ?? "text",
        ...(categoryId ? { categoryId } : {}),
      });
      createdChannels.push({ channel, options: opts });
      const inherited =
        categoryId && !opts.overwrites ? channelOverwrites.get(categoryId) : undefined;
      const ows = new Map<string, TestOverwrite>(
        Array.from(inherited ?? [], ([target, row]) => [
          target,
          { allow: [...row.allow], deny: [...row.deny] },
        ]),
      );
      for (const [target, o] of Object.entries(opts.overwrites ?? {}))
        ows.set(target, { allow: [...(o.allow ?? [])], deny: [...(o.deny ?? [])] });
      channelOverwrites.set(channel.id, ows);
      if (key) provisions.set(key, channel.id);
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

  const kv = testStorage(kvData, () => need("storage.kv"));

  const log = (level: LogEntry["level"]) => (message: string, fields?: Record<string, Json>) => {
    logs.push({ level, message, ...(fields ? { fields } : {}) });
  };

  let scheduleSeq = 0;
  const portalUrl = (options.portalUrl ?? "https://portal.test").replace(/\/$/, "");
  const config =
    options.schema !== undefined
      ? applyDefaults(options.schema, options.config ?? {})
      : (options.config ?? {});

  const indices = new Map<string, Map<string, import("@cicord/sdk").IndexDocument>>();
  function translator(interactionLocale?: string): Context["i18n"] {
    return {
      t(key, vars = {}, opts = {}) {
        const configuredLocale = options.config?.["locale"];
        const names = [
          opts.locale,
          typeof configuredLocale === "string" ? configuredLocale : undefined,
          interactionLocale ?? options.locale,
          options.server?.preferredLocale,
          "en",
        ];
        let template: string | undefined;
        for (const name of names) {
          if (!name) continue;
          template =
            options.locales?.[name]?.[key] ?? options.locales?.[name.split("-")[0] ?? name]?.[key];
          if (template !== undefined) break;
        }
        // internal/plugin/locales.go:16-31 and internal/locales/catalog.go:134-161.
        if (utf8Length(key) > 256 || Object.keys(vars).length > 50)
          throw new TypeError("invalid translation arguments");
        for (const v of Object.values(vars))
          if (typeof v === "string" && utf8Length(v) > MAX_TEXT)
            throw new TypeError(`i18n.t: a variable exceeds ${MAX_TEXT} bytes`);
        const value = template ?? key;
        if (utf8Length(value) > MAX_TEXT) throw textTooLong(key);
        let delta = 0;
        const out = value.replace(
          /\{([a-zA-Z][a-zA-Z0-9_]*)\}/g,
          (original, k: string, at: number) => {
            const v: unknown = Object.hasOwn(vars, k) ? vars[k] : undefined;
            // Strings, numbers and booleans are substituted; anything else keeps the placeholder.
            const sub =
              typeof v === "string" || typeof v === "number" || typeof v === "boolean"
                ? String(v)
                : original;
            // Checked while building, so a repeated placeholder cannot build a huge string.
            delta += utf8Length(sub) - utf8Length(original);
            if (utf8Length(value.slice(0, at + original.length)) + delta > MAX_TEXT)
              throw textTooLong(key);
            return sub;
          },
        );
        if (utf8Length(out) > MAX_TEXT) throw textTooLong(key);
        return out;
      },
    };
  }
  // createChannel provisionKey → channel id, per installation. A key whose channel is gone
  // is forgotten, like the host (which also forgets keys after 30 days).
  const provisions = new Map<string, string>();
  const provisioned = (key: unknown): string | undefined => {
    if (typeof key !== "string" || new TextEncoder().encode(key).length > 200)
      throw new TypeError("provisionKey must be a string of at most 200 bytes");
    const id = provisions.get(key);
    if (id !== undefined && !channels.has(id)) provisions.delete(key);
    return provisions.get(key);
  };
  const featureReports = new Map<string, boolean>();
  const statusLines = new Map<string, string>();
  const ctx: Context = {
    i18n: translator(),

    assets: {
      async read(path) {
        const file = assets.get(path);
        if (
          !file ||
          path.length > 512 ||
          path.includes("\\") ||
          path.includes(":") ||
          path.split("/").some((s) => !s || s === "." || s === "..")
        )
          throw new Error("unknown plugin asset");
        return file.slice();
      },
    },
    render: {
      async image(spec) {
        need("render");
        checkRenderSpec(spec, (key) => {
          if (key.startsWith("url:")) return attachmentBytes.get(key.slice(4));
          const file = assets.get(key.slice("asset:".length));
          if (!file) throw new Error("unknown plugin asset");
          return file;
        });
        if (!options.renderImage)
          throw new Error("createTestContext: pass renderImage to test card rendering");
        return options.renderImage(spec);
      },
    },
    media: {
      async hash(source, opts) {
        need("media.analyze");
        if (!options.hashImage)
          throw new Error("createTestContext: pass hashImage to test media analysis");
        return options.hashImage(source, opts);
      },
      distance(a, b) {
        need("media.analyze");
        if (
          a.length !== b.length ||
          a.length % 2 !== 0 ||
          !/^[0-9a-f]*$/iu.test(a) ||
          !/^[0-9a-f]*$/iu.test(b)
        )
          throw new TypeError("expected equal-length hexadecimal digests");
        let d = 0;
        for (let i = 0; i < a.length; i++) {
          let v = Number.parseInt(a.charAt(i), 16) ^ Number.parseInt(b.charAt(i), 16);
          while (v) {
            d++;
            v &= v - 1;
          }
        }
        return d;
      },
    },
    analytics: {
      async summary(q) {
        need("analytics.read");
        if (
          !options.analytics ||
          !["day", "week"].includes(q.granularity) ||
          !(Date.parse(q.from) < Date.parse(q.to))
        )
          throw new Error("analytics unavailable or invalid range");
        return structuredClone(options.analytics);
      },
    },
    features: {
      enabled: (name) => options.features?.includes(name) ?? false,
      report: (name, active) => {
        featureReports.set(name, active);
      },
    },
    plugins: options.plugins ?? {
      has: () => false,
      hasFeature: (name, feature) =>
        name === ctx.plugin.name && featureReports.get(feature) === true,
      async query(plugin) {
        throw new PluginUnavailableError(plugin);
      },
      async call(plugin) {
        throw new PluginUnavailableError(plugin);
      },
    },
    plugin: {
      name: options.plugin?.name ?? "test-plugin",
      version: options.plugin?.version ?? "0.0.0",
      instance: options.plugin?.instance ?? options.plugin?.name ?? "test-plugin",
    },
    bot: { key: options.bot?.key ?? "test-bot", user: botUser },
    server,
    config,
    log: { debug: log("debug"), info: log("info"), warn: log("warn"), error: log("error") },
    privacy: {
      // Like the host: a pseudonym exists only for the subject of the current privacy hook
      // (eraseMember / exportMember), so anonymized records cannot be linked back to a
      // live member from ordinary handlers.
      async pseudonym() {
        throw new Error(PSEUDONYM_SCOPE);
      },
    },
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
    status: {
      // internal/plugin/status.go and internal/runtime/runtime.go:188 (featureStatus).
      set(key, message) {
        if (
          typeof key !== "string" ||
          key.length === 0 ||
          utf8Length(key) > 96 ||
          (message !== null && message !== undefined && typeof message !== "string") ||
          utf8Length(message ?? "") > 320
        )
          throw new TypeError("invalid feature diagnostic");
        if (!message) {
          statusLines.delete(key);
          return;
        }
        log("warn")(key + ": " + message);
        // At most 32 lines: a new key beyond them is dropped, an existing one is updated.
        if (statusLines.has(key) || statusLines.size < 32) statusLines.set(key, message);
      },
    },
    secrets: {
      has(name) {
        return options.secrets?.includes(name) === true;
      },
      ref(name) {
        if (!this.has(name)) throw new Error("plugin secret is unavailable");
        return ("cicord-secret:" + name) as ReturnType<Context["secrets"]["ref"]>;
      },
    },
    index(name, indexOptions = {}) {
      const key = name + "|" + (indexOptions.language ?? "en");
      const values = indices.get(key) ?? new Map<string, import("@cicord/sdk").IndexDocument>();
      indices.set(key, values);
      return {
        async upsert(id, doc) {
          need("storage.index");
          values.set(id, structuredClone(doc));
        },
        async remove(id) {
          need("storage.index");
          values.delete(id);
        },
        async clear() {
          need("storage.index");
          values.clear();
        },
        async search(query, { limit = 10, tags = [] } = {}) {
          need("storage.index");
          if (limit < 1 || limit > 50) throw new Error("index limit must be 1 to 50");
          const terms = query.toLowerCase().match(/[\p{L}\p{N}]+/gu) ?? [];
          return [...values]
            .filter(([, doc]) => tags.every((tag) => doc.tags?.includes(tag)))
            .map(([id, doc]) => {
              const haystack = ((doc.title ?? "") + " " + doc.text).toLowerCase();
              const score =
                terms.filter((t) => haystack.includes(t)).length / Math.max(terms.length, 1);
              return {
                id,
                score,
                snippet: doc.text.slice(0, 250),
                meta: structuredClone(doc.meta ?? null),
              };
            })
            .filter((r) => r.score > 0)
            .toSorted((a, b) => b.score - a.score || a.id.localeCompare(b.id))
            .slice(0, limit);
        },
      };
    },
    files: {
      async list(glob) {
        need("files.read");
        const files = Object.keys(options.files ?? {}).toSorted();
        return glob ? files.filter((path) => fileMatches(glob.split("/"), path.split("/"))) : files;
      },
      async bytes(path) {
        need("files.read");
        const value = options.files?.[path];
        if (value === undefined) throw new Error("undeclared instance file");
        return typeof value === "string" ? new TextEncoder().encode(value) : value.slice();
      },
      async read(path) {
        return new TextDecoder("utf-8", { fatal: true }).decode(await this.bytes(path));
      },
    },
    http: {
      async fetch(url, init) {
        need("http.fetch");
        if (!options.fetch)
          throw new Error(
            "createTestContext: ctx.http.fetch is not faked; pass `fetch` in the options",
          );
        const sendOnly = url.startsWith("cicord-secret:");
        if (
          sendOnly &&
          (init?.method?.toUpperCase() !== "POST" || init.form || (!init.json && !init.body))
        )
          throw new Error("invalid webhook send");
        requests.push({ url, init });
        const raw = await options.fetch(url, init);
        const r = sendOnly ? { ...raw, body: undefined, headers: {} } : raw;
        const body =
          typeof r.body === "string"
            ? r.body
            : r.body instanceof Uint8Array
              ? new TextDecoder().decode(r.body)
              : r.body === undefined
                ? ""
                : JSON.stringify(r.body);
        const bytes =
          r.body instanceof Uint8Array ? new Uint8Array(r.body) : new TextEncoder().encode(body);
        return {
          status: r.status ?? 200,
          headers: r.headers ?? {},
          text: async () => body,
          bytes: async () => bytes.slice(),
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
      async revoke(scope) {
        need("portal.links");
        for (const link of links)
          if (link.scope?.id === scope.id && link.scope.kind === scope.kind) link.revoked = true;
      },
      async link(
        path,
        opts?: {
          member?: Member | User;
          user?: User | Member | string;
          scope?: CaseScope;
          expiresInSeconds?: number;
        },
      ) {
        need("portal.links");
        const memberId =
          typeof opts?.user === "string" ? opts.user : (opts?.user?.id ?? opts?.member?.id);
        if (opts?.scope) {
          if (
            !memberId ||
            opts.scope.kind !== "case" ||
            !/^[A-Za-z0-9_-]{1,128}$/.test(opts.scope.id) ||
            (opts.expiresInSeconds ?? 0) > 604800 ||
            (opts.expiresInSeconds ?? 0) < 0
          )
            throw new Error("invalid case link");
          const url = new URL(path, portalUrl);
          url.searchParams.set("cicord_scope", String(links.length + 1));
          links.push({
            path,
            member: memberId,
            scope: { ...opts.scope },
            expiresAt: Date.now() + (opts.expiresInSeconds || 900) * 1000,
            url: url.toString(),
          });
          return url.toString();
        }
        const token = `test-${memberId ?? "anonymous"}`;
        const url = `${portalUrl}${path.startsWith("/") ? path : `/${path}`}?cicord_token=${token}`;
        links.push({ path, ...(memberId ? { member: memberId } : {}), url });
        return url;
      },
    },
  };

  testTranslators.set(ctx, translator);

  function privacyHook(subject: string): Context["privacy"] {
    return {
      async pseudonym(member) {
        if (member !== subject) throw new Error(PSEUDONYM_SCOPE);
        const hash = createHmac("sha256", "cicord-test-privacy")
          .update(server.id + "\0" + member)
          .digest("hex");
        return "deleted-user-" + hash.slice(0, 16);
      },
    };
  }

  return {
    ctx,
    async eraseMember(plugin, userId) {
      if (!plugin.privacy) throw new Error("plugin has no privacy hooks");
      const tag = "member-" + userId;
      const scoped: Context = {
        ...ctx,
        privacy: privacyHook(userId),
        index(name, opts) {
          const api = ctx.index(name, opts);
          if (!grants || grants.has("storage.index")) return api;
          return {
            ...api,
            async remove(id) {
              for (const [key, docs] of indices) {
                if (!key.startsWith(name + "|")) continue;
                const doc = docs.get(id);
                if (doc && !doc.tags?.includes(tag))
                  throw new Error("index document has no binding to the member being erased");
                docs.delete(id);
              }
            },
          };
        },
      };
      const result = await plugin.privacy.erase(scoped, userId);
      for (const docs of indices.values())
        for (const [id, doc] of docs) if (doc.tags?.includes(tag)) docs.delete(id);
      return result;
    },
    async exportMember(plugin, userId) {
      if (!plugin.privacy) throw new Error("plugin has no privacy hooks");
      return plugin.privacy.export({ ...ctx, privacy: privacyHook(userId) }, userId);
    },
    attachment,
    validateFiles,
    onceFor: onceFor(),
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
    status: statusLines,
    member: (id) => members.get(id),
    addMember,
    receive(ref, author, input) {
      const channel = ref
        ? channels.get(ref)
        : (channels.get("interaction-channel") ??
          register(undefined, { id: "interaction-channel", name: "interaction" }));
      if (!channel) throw new Error("unknown channel");
      const message = makeMessage(channel, author, input);
      // Like the gateway payload: a received message carries its author's member (in a DM,
      // the member of this server, as `directMessage` gets it). Fetched and sent messages
      // (history, channel.message(), send/reply results) have none, like Discord's REST API.
      const member = members.get(author.id);
      if (!member) return message;
      const view = Object.defineProperties({}, Object.getOwnPropertyDescriptors(message));
      return Object.defineProperty(view, "member", { value: member, enumerable: true }) as Message;
    },
    setReaction,
    crossposts,
    typing,
    votePoll,
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

// internal/locales/catalog.go:104: a translated text, and each string variable, is at most 64 KiB.
const MAX_TEXT = 64 << 10;
const utf8Length = (s: string) => new TextEncoder().encode(s).length;
const textTooLong = (key: string) =>
  new TypeError(`i18n.t ${JSON.stringify(key)}: translated text exceeds ${MAX_TEXT} bytes`);

// ───────────────────────────────────────────────────────────── routes

export interface RouteCall {
  /** Signed link issued by this harness to the OAuth viewer. */
  scopedLink?: string;
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
const isRawRouteResponse = (response: RouteResponse): response is RawRouteResponse =>
  !!response &&
  typeof response === "object" &&
  !Array.isArray(response) &&
  typeof response.status === "number" &&
  "raw" in response &&
  "contentType" in response;

export function normalizeRouteResponse(response: RouteResponse): RouteResult {
  if (isRawRouteResponse(response)) {
    if (
      typeof response.raw !== "string" ||
      typeof response.contentType !== "string" ||
      !["text/calendar", "text/csv", "text/plain", "text/markdown"].includes(
        response.contentType,
      ) ||
      new TextEncoder().encode(response.raw).length > 2 * 1024 * 1024
    )
      throw new Error("invalid raw route response");
    return {
      status: response.status,
      body: response.raw,
      headers: {
        "content-type": response.contentType + "; charset=utf-8",
        "x-content-type-options": "nosniff",
      },
    };
  }
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

// internal/api/auth.go:155-187: a route requires a role at least this high.
const ROUTE_ROLES = { public: 0, scoped: 0, member: 1, moderator: 2, admin: 3 } as const;
const routeFailure = (status: number, code: string, message: string): RouteResult => ({
  status,
  body: { error: { code, message } },
  headers: { "content-type": "application/json; charset=utf-8", "cache-control": "no-store" },
});

/**
 * internal/api/portal.go:302 (routeHeader): the response headers a plugin route may set.
 * Cookies, redirects, CORS, security and hop-by-hop headers are dropped.
 */
function routeHeader(name: string, isPublic: boolean): boolean {
  const lk = name.toLowerCase();
  switch (lk) {
    case "content-disposition":
    case "content-language":
    case "etag":
    case "last-modified":
    case "expires":
    case "retry-after":
      return true;
    case "cache-control":
      return isPublic;
  }
  return (
    lk.startsWith("x-") &&
    !lk.startsWith("x-cicord-") &&
    ![
      "x-content-type-options",
      "x-frame-options",
      "x-forwarded-for",
      "x-forwarded-host",
      "x-forwarded-proto",
    ].includes(lk)
  );
}

/**
 * What the runtime accepts from a handler (internal/plugin/sdk.js routeOut and
 * internal/plugin/instance.go:1380-1414): an integer status of 200 to 599, string header
 * values, at most 4 MiB serialized. Anything else fails the route.
 */
function checkRouteResult(response: RouteResponse, result: RouteResult): void {
  if (!Number.isInteger(result.status) || result.status < 200 || result.status > 599)
    throw new Error(`invalid route status ${String(result.status)}`);
  if (Object.values(result.headers).some((v) => typeof v !== "string"))
    throw new Error("route header values must be strings");
  const serialized = isRawRouteResponse(response)
    ? `raw\n${result.status}\n${response.contentType}\n${response.raw}`
    : (JSON.stringify({ status: result.status, body: result.body, headers: result.headers }) ?? "");
  if (new TextEncoder().encode(serialized).length > 4 << 20)
    throw new Error("route response exceeds 4 MiB");
}

/**
 * Calls a plugin route like the runtime does. `route` is `"<METHOD> <path>"`; the path may
 * be concrete (`"POST /answers/42"`) and is matched against declared patterns (`/answers/:id`).
 * Like internal/api/portal.go: route `access` is checked (`401` for anonymous viewers of
 * non-public routes, `403` below the route's role: member, moderator or admin), an invalid
 * response is a `500`, plugin headers are filtered and the core's own are added.
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

    const access: keyof typeof ROUTE_ROLES = definition.access ?? "member";
    const need = ROUTE_ROLES[access];
    let viewer: { user: User; member?: Member } | undefined;
    if (call.viewer !== undefined) {
      const member = harness.member(call.viewer);
      viewer = member ? { user: member.user, member } : { user: makeUser({ id: call.viewer }) };
    }
    const scoped = call.scopedLink
      ? harness.links.find((l) => l.url === call.scopedLink && l.scope)
      : undefined;
    const scope = scoped?.scope;
    if (access === "scoped") {
      if (!viewer) return routeFailure(401, "unauthenticated", "case sign-in required");
      if (!scoped || scoped.member !== viewer.user.id)
        return routeFailure(403, "forbidden", "a matching case link is required");
      if (scoped.revoked || (scoped.expiresAt ?? 0) <= Date.now())
        return routeFailure(403, "forbidden", "expired or revoked case link");
      // instance.go:1352: a scoped route's :id must be the link's resource.
      if (params.id !== undefined && params.id !== scope?.id)
        return serveRoute({ status: 403, body: undefined, headers: {} }, false, true, false);
    } else if (call.scopedLink) {
      return routeFailure(403, "forbidden", "this session is restricted to its case");
    } else if (viewer) {
      const role = viewer.member ? ROUTE_ROLES[viewer.member.access ?? "member"] : 0;
      if (role < need)
        return routeFailure(403, "forbidden", `this route requires the ${access} role`);
    } else if (need > 0) {
      return routeFailure(401, "unauthenticated", "authentication required");
    }
    const response = await definition.handler(harness.ctx, {
      params,
      query,
      body: call.body,
      ...(viewer ? { viewer } : {}),
      ...(scope ? { scope } : {}),
    });
    let result: RouteResult;
    try {
      result = normalizeRouteResponse(response);
      checkRouteResult(response, result);
    } catch {
      return routeFailure(500, "internal", "the plugin route failed");
    }
    return serveRoute(
      result,
      access === "public",
      viewer !== undefined,
      isRawRouteResponse(response),
    );
  }
  return routeFailure(404, "not_found", "not found");
}

/** internal/api/portal.go:419-457: the HTTP answer the portal gets for a valid response. */
function serveRoute(
  result: RouteResult,
  isPublic: boolean,
  signedIn: boolean,
  raw: boolean,
): RouteResult {
  const headers: Record<string, string> = {};
  if (!raw)
    for (const [k, v] of Object.entries(result.headers))
      if (routeHeader(k, isPublic)) headers[k.toLowerCase()] = v;
  headers["x-content-type-options"] = "nosniff";
  headers["vary"] = "Cookie, Authorization";
  if (!isPublic || signedIn) headers["cache-control"] = "no-store";
  if (raw) {
    headers["content-type"] = result.headers["content-type"]!;
    return { status: result.status, body: result.body, headers };
  }
  if (result.body === undefined)
    return { status: result.status === 200 ? 204 : result.status, body: undefined, headers };
  headers["content-type"] = "application/json; charset=utf-8";
  headers["cache-control"] = "no-store";
  // The portal receives JSON: what does not serialize (undefined fields, Dates…) is converted.
  return { status: result.status, body: JSON.parse(JSON.stringify(result.body)), headers };
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
  locale?: string;
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
  selections?: Record<string, string[]>;
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

/** A command that handed off its private reply exposes its queued task to the test. */
export interface CommandResult extends InteractionResult {
  runDeferredTask?: () => Promise<void>;
}

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
    validateLayoutInput(message);
    harness.validateFiles(message);
    normalizeAllowedMentions(message, known);
  };
  const followUp = async (message: ReplyInput): Promise<Message> => {
    if (phase === "none") throw new Error("followUp(): the interaction has not been answered yet");
    checkMentions(message);
    result.followUps.push(message);
    // Follow-ups need no capability: the message is not recorded in `sent`.
    const sent = harness.receive(channel?.id, harness.ctx.bot.user, message);
    // A sent message has no `member`, like Discord's REST answer: return the stored copy.
    return harness.message(sent.id) ?? sent;
  };
  return {
    id: `interaction-${kind}-${id}`,
    ...(call.locale ? { locale: call.locale } : {}),
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
  const result: CommandResult = emptyResult();
  const base = fakeInteraction(harness, "command", path, call, result);
  const { update: _update, ...rest } = base;
  const interaction = Object.defineProperties(
    {
      ...rest,
      async deferTask(name: string, payload?: Json) {
        const handle = plugin.tasks?.[name];
        const channel = base.channel;
        if (!handle || !base.member || !channel || result.runDeferredTask)
          throw new Error("private command task unavailable");
        if (!result.deferred) await base.defer({ ephemeral: true });
        if (!result.deferred?.ephemeral) throw new Error("private deferral required");
        const { id } = await harness.ctx.schedule.after(1000, name, payload);
        const expiresAt = new Date(Date.now() + 14 * 60_000);
        result.runDeferredTask = async () => {
          if (!harness.scheduled.some((task) => task.id === id)) return;
          await harness.ctx.schedule.cancel(id);
          if (Date.now() >= expiresAt.getTime()) return;
          await handle(harness.ctx, payload, {
            id,
            attempt: 1,
            interaction: {
              id: base.id,
              userId: base.user.id,
              channelId: channel.id,
              expiresAt,
              async reply(message) {
                if (
                  !harness.member(base.user.id)?.canView(channel) ||
                  Date.now() >= expiresAt.getTime()
                )
                  throw new Error("private command task unavailable");
                await base.reply(message);
              },
            },
          });
        };
      },
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
  await leaf.definition.run(localizedContext(harness.ctx, interaction.locale), interaction);
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
    ...(call.locale ? { locale: call.locale } : {}),
    user: member?.user ?? makeUser({ id: call.user }),
    ...(member ? { member } : {}),
    ...(channel ? { channel } : {}),
    command: leaf.command,
    ...(leaf.group ? { group: leaf.group } : {}),
    ...(leaf.subcommand ? { subcommand: leaf.subcommand } : {}),
    options: { ...call.options },
  };
  const choices = await option.autocomplete(
    localizedContext(harness.ctx, interaction.locale),
    interaction,
    {
      name: call.focused,
      value: call.value ?? "",
    },
  );
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
  await handler(localizedContext(harness.ctx, interaction.locale), interaction);
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
    { ...rest, modalId, fields: { ...call.fields }, selections: { ...call.selections } },
    {
      replied: { get: () => base.replied },
      deferred: { get: () => base.deferred },
    },
  ) as ModalInteraction;
  await handler(localizedContext(harness.ctx, interaction.locale), interaction);
  return result;
}

/** Invoke a guild context menu with a resolved message or member. */
export async function runContextMenu(
  plugin: PluginDefinition,
  harness: TestHarness,
  name: string,
  call: InteractionCall & { targetId: string },
): Promise<InteractionResult> {
  const menu = plugin.contextMenus?.[name];
  if (!menu) throw new Error(`unknown context menu ${name}`);
  const member = harness.member(call.user);
  if (menu.permissions?.some((p) => !member?.permissions?.includes(p)))
    throw new Error("missing context menu permissions");
  const result = emptyResult();
  const base = fakeInteraction(harness, "command", name, call, result);
  if (menu.type === "message") {
    const target = harness.message(call.targetId);
    if (!target || (base.channel && target.channel.id !== base.channel.id))
      throw new Error("unknown target message");
    await menu.run(localizedContext(harness.ctx, base.locale), Object.assign(base, { target }));
  } else {
    const target = harness.member(call.targetId);
    if (!target) throw new Error("unknown target member");
    await menu.run(localizedContext(harness.ctx, base.locale), Object.assign(base, { target }));
  }
  return result;
}

function validateLayoutInput(input: MessageInput) {
  if (typeof input === "string" || input.layout === undefined) return;
  if (
    input.content !== undefined ||
    input.embeds !== undefined ||
    input.components !== undefined ||
    !input.layout.length
  )
    throw new TypeError("layout requires components and excludes content, embeds and components");
  let count = 0;
  function visit(
    node: NonNullable<Exclude<MessageInput, string>["layout"]>[number],
    parent: string,
  ) {
    count++;
    switch (node.type) {
      case "row":
        if (!node.components.length || node.components.length > 5)
          throw new TypeError("invalid row");
        count += node.components.length;
        break;
      case "container":
        if (parent !== "message" || !node.components.length)
          throw new TypeError("invalid container");
        for (const c of node.components) visit(c, "container");
        break;
      case "section":
        if (!node.accessory || node.components.length < 1 || node.components.length > 3)
          throw new TypeError("invalid section");
        count++;
        for (const c of node.components) visit(c, "section");
        break;
      case "text":
        if (!node.content || codePoints(node.content) > 4000) throw new TypeError("invalid text");
        break;
      case "file":
        if (!node.file.url.startsWith("attachment://")) throw new TypeError("invalid file");
        break;
      case "mediaGallery":
        if (!node.items.length || node.items.length > 10) throw new TypeError("invalid gallery");
        break;
      case "separator":
        break;
      default:
        throw new TypeError("unknown layout component");
    }
    if (count > 40) throw new TypeError("layout exceeds 40 components");
  }
  for (const n of input.layout) visit(n, "message");
}

/** Test helper for idempotent handlers. Concurrent calls share the same promise;
 * a failed effect can be retried. Keep the returned closure across redeliveries. */
export function onceFor() {
  const seen = new Map<string, Promise<void>>();
  return (id: string, effect: () => Promise<void>): Promise<void> => {
    const previous = seen.get(id);
    if (previous) return previous;
    const promise = Promise.resolve()
      .then(effect)
      .catch((error) => {
        seen.delete(id);
        throw error;
      });
    seen.set(id, promise);
    return promise;
  };
}

const externalMethods = new Set([
  "send",
  "reply",
  "edit",
  "delete",
  "react",
  "bulkDelete",
  "purge",
  "createThread",
  "createChannel",
  "editChannel",
  "deleteChannel",
  "addRole",
  "removeRole",
  "setRoles",
  "setNickname",
  "timeout",
  "removeTimeout",
  "kick",
  "ban",
  "unban",
  "moveTo",
  "disconnect",
  "fetch",
  "open",
  "close",
  "pin",
  "unpin",
  "publish",
  "add",
  "remove",
  "respond",
  "setSlowmode",
]);

/** Exercise an action once, then interrupt a fresh copy of its world after each
 * successful Discord/HTTP effect and retry with the same key. The returned trials
 * expose the recording fakes for assertions about external effects. */
export async function replayActionEffects<I, O>(
  handler: Action<I, O>,
  input: I,
  call: InteropCall & { idempotencyKey: string },
  setup: () => TestHarness,
  isExternal: (method: string) => boolean = (method) => externalMethods.has(method),
) {
  const runTrial = async (failAt: number) => {
    const h = setup();
    let effects = 0;
    const lost = Error("simulated response loss after external effect");
    let crashed = false;
    const wrap = (value: unknown): unknown => {
      if (value === null || typeof value !== "object" || value instanceof Date) return value;
      if (Array.isArray(value)) return value.map(wrap);
      const obj = value as Record<PropertyKey, unknown>;
      return new Proxy(
        {},
        {
          get(_target, key) {
            const fn = Reflect.get(obj, key);
            if (typeof fn !== "function") return wrap(fn);
            return (...args: unknown[]) => {
              if (crashed) throw lost;
              const result = Reflect.apply(fn, obj, args);
              const after = (r: unknown) => {
                if (isExternal(String(key)) && ++effects === failAt) {
                  crashed = true;
                  throw lost;
                }
                return wrap(r);
              };
              return result instanceof Promise ? result.then(after) : after(result);
            };
          },
        },
      );
    };
    // The provider's KV writes from an interrupted run disappear. Discord/HTTP
    // recording fakes remain live, so a second external effect is observable.
    const initial = [...h.kv];
    const freshStorage = () => {
      const store = createTestContext();
      for (const [k, v] of initial) store.kv.set(k, JSON.parse(JSON.stringify(v)) as Json);
      return store;
    };
    const commit = (store: TestHarness) => {
      h.kv.clear();
      for (const [key, value] of store.kv) h.kv.set(key, value);
    };
    const staged = freshStorage();
    const request = {
      ...h.ctx,
      kv: staged.ctx.kv,
      server: wrap(h.ctx.server) as Server,
      http: wrap(h.ctx.http) as Context["http"],
    };
    try {
      const result = await handler.run(request, input, call);
      if (!crashed) {
        commit(staged);
        return { harness: h, result, effects };
      }
    } catch (error) {
      if (!crashed) throw error;
    }
    const retry = freshStorage();
    const result = await handler.run({ ...h.ctx, kv: retry.ctx.kv }, input, call);
    commit(retry);
    return { harness: h, result, effects };
  };
  const first = await runTrial(-1);
  const trials = [first];
  for (let n = 1; n <= first.effects; n++) trials.push(await runTrial(n));
  return trials;
}
