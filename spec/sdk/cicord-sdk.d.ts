/**
 * @cicord/sdk — plugin authoring contract (v1alpha1).
 *
 * This file is the normative API of Cicord plugins. The runtime implements it on top of the
 * plugin host (QuickJS-ng on WebAssembly); the published `@cicord/sdk` package re-exports these types.
 *
 * Conventions:
 * - Server resources (channels, roles, categories) are referenced by their stable **key**
 *   (as declared in servers/<server>.yaml), or by `{ id }` for resources Cicord does not manage.
 * - Every call that touches Discord or the runtime requires a capability, noted as
 *   `@capability`. Calling it without the grant throws `CapabilityError`.
 * - Handlers that need gateway data note `@intent`. Declaring a handler without its intent in
 *   plugin.yaml is a build error.
 */

// ───────────────────────────────────────────────────────────── basics

export type Snowflake = string;
export type Awaitable<T> = T | Promise<T>;
export type Json = string | number | boolean | null | Json[] | { [key: string]: Json };

/**
 * Typed plugin config. Empty by default; `cicord plugin typegen` generates a module
 * augmentation from config.schema.json:
 *
 * ```ts
 * declare module "@cicord/sdk" {
 *   interface PluginConfig { channel: string; message: string }
 * }
 * ```
 */
export interface PluginConfig {}

/** Key of a managed resource, or an explicit Discord ID for unmanaged ones. */
export type ResourceRef = string | { id: Snowflake };
export type ChannelRef = ResourceRef;
export type RoleRef = ResourceRef;

export type Permission =
  | "CreateInstantInvite" | "KickMembers" | "BanMembers" | "Administrator" | "ManageChannels"
  | "ManageServer" | "AddReactions" | "ViewAuditLog" | "PrioritySpeaker" | "Stream" | "ViewChannel"
  | "SendMessages" | "SendTTSMessages" | "ManageMessages" | "EmbedLinks" | "AttachFiles"
  | "ReadMessageHistory" | "MentionEveryone" | "UseExternalEmojis" | "ViewServerInsights"
  | "Connect" | "Speak" | "MuteMembers" | "DeafenMembers" | "MoveMembers" | "UseVAD"
  | "ChangeNickname" | "ManageNicknames" | "ManageRoles" | "ManageWebhooks" | "ManageExpressions"
  | "UseApplicationCommands" | "RequestToSpeak" | "ManageEvents" | "ManageThreads"
  | "CreatePublicThreads" | "CreatePrivateThreads" | "UseExternalStickers"
  | "SendMessagesInThreads" | "UseEmbeddedActivities" | "ModerateMembers"
  | "ViewCreatorMonetizationAnalytics" | "UseSoundboard" | "CreateExpressions" | "CreateEvents"
  | "UseExternalSounds" | "SendVoiceMessages" | "SendPolls" | "UseExternalApps";

/** Thrown when a call needs a capability that is not granted in bot.yaml. */
export declare class CapabilityError extends Error {
  readonly capability: string;
}

// ───────────────────────────────────────────────────────────── messages

export interface Embed {
  title?: string;
  description?: string;
  url?: string;
  /** #RRGGBB */
  color?: string;
  timestamp?: Date | string;
  fields?: { name: string; value: string; inline?: boolean }[];
  author?: { name: string; url?: string; iconUrl?: string };
  footer?: { text: string; iconUrl?: string };
  image?: { url: string };
  thumbnail?: { url: string };
}

/** `id` values are local to the plugin instance; the runtime namespaces them. */
export type Button =
  | { type: "button"; id: string; label?: string; emoji?: string; style?: "primary" | "secondary" | "success" | "danger"; disabled?: boolean }
  | { type: "link"; url: string; label?: string; emoji?: string; disabled?: boolean };

export interface SelectMenu {
  type: "select";
  id: string;
  placeholder?: string;
  minValues?: number;
  maxValues?: number;
  options: { value: string; label: string; description?: string; emoji?: string; default?: boolean }[];
}

export interface ActionRow {
  type: "row";
  components: (Button | SelectMenu)[];
}

export interface MessagePayload {
  content?: string;
  embeds?: Embed[];
  components?: ActionRow[];
  allowedMentions?: { users?: boolean; roles?: boolean; everyone?: boolean };
}

export type MessageInput = string | MessagePayload;

/** Interaction replies can additionally be ephemeral (visible only to the invoking user). */
export type ReplyInput = string | (MessagePayload & { ephemeral?: boolean });

export interface Attachment {
  id: Snowflake;
  filename: string;
  url: string;
  contentType?: string;
  size: number;
}

export interface Message {
  readonly id: Snowflake;
  /**
   * Empty unless the plugin declares the MessageContent intent in plugin.yaml, or the
   * message is the bot's own, mentions the bot or is a DM. The gating is per plugin (other
   * plugins of the same bot declaring the intent do not lift it) and also applies to
   * `channel.messages()`; `attachments` are emptied the same way.
   */
  readonly content: string;
  readonly author: User;
  readonly member?: Member;
  readonly channel: TextChannel;
  readonly attachments: readonly Attachment[];
  readonly createdAt: Date;
  /** @capability discord.messages.send */
  reply(message: MessageInput): Promise<Message>;
  /** @capability discord.messages.manage (when not authored by this bot) */
  edit(message: MessageInput): Promise<Message>;
  /** @capability discord.messages.manage */
  delete(reason?: string): Promise<void>;
  /** @capability discord.reactions.manage */
  react(emoji: string): Promise<void>;
}

// ───────────────────────────────────────────────────────────── server entities

export interface User {
  readonly id: Snowflake;
  readonly username: string;
  readonly displayName: string;
  readonly avatarUrl?: string;
  readonly bot: boolean;
  /** `<@id>` */
  readonly mention: string;
}

export interface Role {
  readonly id: Snowflake;
  /** Key in servers/<server>.yaml when the role is managed by Cicord. */
  readonly key?: string;
  readonly name: string;
  readonly color?: string;
  readonly mention: string;
}

export interface Channel {
  readonly id: Snowflake;
  /** Key in servers/<server>.yaml when the channel is managed by Cicord. */
  readonly key?: string;
  readonly name: string;
  readonly type: "text" | "voice" | "announcement" | "stage" | "forum" | "media" | "thread" | "category" | "dm";
  readonly mention: string;
}

export interface TextChannel extends Channel {
  /** @capability discord.messages.send */
  send(message: MessageInput): Promise<Message>;
  /** @capability discord.messages.read */
  messages(options?: { limit?: number; before?: Snowflake; after?: Snowflake }): Promise<Message[]>;
  /** @capability discord.threads.manage */
  createThread(name: string, options?: { private?: boolean; fromMessage?: Snowflake }): Promise<TextChannel>;
}

export interface Member {
  readonly user: User;
  readonly id: Snowflake;
  readonly nickname?: string;
  readonly displayName: string;
  readonly mention: string;
  readonly roles: readonly Role[];
  readonly joinedAt: Date;
  hasRole(role: RoleRef): boolean;
  /** @capability discord.roles.assign */
  addRole(role: RoleRef, reason?: string): Promise<void>;
  /** @capability discord.roles.assign */
  removeRole(role: RoleRef, reason?: string): Promise<void>;
  /** @capability discord.members.manage */
  setNickname(nickname: string | null, reason?: string): Promise<void>;
  /** @capability discord.members.moderate */
  timeout(durationMs: number, reason?: string): Promise<void>;
  /** @capability discord.members.moderate */
  kick(reason?: string): Promise<void>;
  /** @capability discord.members.moderate */
  ban(options?: { reason?: string; deleteMessagesSeconds?: number }): Promise<void>;
  /** Direct message. @capability discord.dm.send */
  send(message: MessageInput): Promise<Message>;
}

export interface CreateChannelOptions {
  name: string;
  type?: "text" | "voice" | "forum";
  category?: ResourceRef;
  topic?: string;
  /**
   * Permission overwrites keyed by target: `"@everyone"`, a role key, `"role:<id>"` or
   * `"member:<id>"` (e.g. the ticket opener, or `member:${ctx.bot.user.id}` so the bot keeps
   * access without Administrator).
   */
  overwrites?: Record<string, { allow?: Permission[]; deny?: Permission[] }>;
}

export interface Server {
  readonly id: Snowflake;
  /** metadata.name of the Server resource. */
  readonly key: string;
  readonly name: string;
  readonly iconUrl?: string;
  /** Resolve a channel by key or id. Throws if it does not exist. @capability discord.channels.read */
  channel(ref: ChannelRef): TextChannel;
  /** Resolve a role by key or id. Throws if it does not exist. */
  role(ref: RoleRef): Role;
  /** @capability discord.members.read */
  member(userId: Snowflake): Promise<Member | undefined>;
  /**
   * Create a channel owned by this plugin instance (e.g. a ticket). The reconciler never
   * prunes plugin-owned channels. @capability discord.channels.manage
   */
  createChannel(options: CreateChannelOptions): Promise<TextChannel>;
  /** Delete a channel owned by this plugin instance. @capability discord.channels.manage */
  deleteChannel(ref: ChannelRef, reason?: string): Promise<void>;
}

// ───────────────────────────────────────────────────────────── interactions

/**
 * Discord requires an answer within 3 seconds. If a handler has not replied after ~2 s, the
 * runtime defers automatically (ephemeral if the command/component says so) and a later
 * `reply()` edits the deferred response transparently.
 */
export interface Interaction {
  readonly id: Snowflake;
  readonly user: User;
  readonly member?: Member;
  readonly channel?: TextChannel;
  readonly replied: boolean;
  readonly deferred: boolean;
  reply(message: ReplyInput): Promise<void>;
  defer(options?: { ephemeral?: boolean }): Promise<void>;
  followUp(message: ReplyInput): Promise<Message>;
  showModal(modal: Modal): Promise<void>;
}

export type CommandOptionType =
  | "string" | "integer" | "number" | "boolean" | "user" | "channel" | "role" | "mentionable" | "attachment";

export interface CommandOption {
  type: CommandOptionType;
  description: string;
  required?: boolean;
  choices?: readonly { name: string; value: string | number }[];
  min?: number;
  max?: number;
}

interface OptionTypeMap {
  string: string;
  integer: number;
  number: number;
  boolean: boolean;
  user: User;
  channel: Channel;
  role: Role;
  mentionable: User | Role;
  attachment: Attachment;
}

export type OptionValues<O extends Record<string, CommandOption>> = {
  [K in keyof O as O[K]["required"] extends true ? K : never]: OptionTypeMap[O[K]["type"]];
} & {
  [K in keyof O as O[K]["required"] extends true ? never : K]?: OptionTypeMap[O[K]["type"]];
};

export interface CommandInteraction<O extends Record<string, CommandOption> = Record<string, CommandOption>>
  extends Interaction {
  readonly command: string;
  readonly options: OptionValues<O>;
}

export interface ComponentInteraction extends Interaction {
  /** Local component id (as given in Button/SelectMenu). */
  readonly componentId: string;
  /** Selected values for select menus. */
  readonly values: readonly string[];
  readonly message: Message;
  /** Edit the message the component belongs to. */
  update(message: MessageInput): Promise<void>;
}

export interface Modal {
  id: string;
  title: string;
  fields: {
    id: string;
    label: string;
    style?: "short" | "paragraph";
    required?: boolean;
    placeholder?: string;
    value?: string;
    minLength?: number;
    maxLength?: number;
  }[];
}

export interface ModalInteraction extends Interaction {
  readonly modalId: string;
  readonly fields: Readonly<Record<string, string>>;
}

export interface CommandDefinition<O extends Record<string, CommandOption> = Record<string, CommandOption>> {
  description: string;
  options?: O;
  /** Ephemeral replies and auto-defer by default. */
  ephemeral?: boolean;
  /** Default member permissions required to see/use the command. */
  permissions?: Permission[];
  run(ctx: Context, interaction: CommandInteraction<O>): Awaitable<void>;
}

/** Helper that infers option types for `interaction.options`. */
export declare function command<const O extends Record<string, CommandOption> = {}>(
  definition: CommandDefinition<O>,
): CommandDefinition<O>;

// ───────────────────────────────────────────────────────────── runtime services

export interface Logger {
  debug(message: string, fields?: Record<string, Json>): void;
  info(message: string, fields?: Record<string, Json>): void;
  warn(message: string, fields?: Record<string, Json>): void;
  error(message: string, fields?: Record<string, Json>): void;
}

/** Key-value storage scoped to the plugin instance. Values are JSON. @capability storage.kv */
export interface KV {
  get<T extends Json = Json>(key: string): Promise<T | undefined>;
  set(key: string, value: Json, options?: { ttlSeconds?: number }): Promise<void>;
  delete(key: string): Promise<void>;
  /** Atomic increment; creates the key at 0 if missing. Returns the new value. */
  increment(key: string, by?: number): Promise<number>;
  list<T extends Json = Json>(options?: { prefix?: string; limit?: number; cursor?: string }):
    Promise<{ items: { key: string; value: T }[]; cursor?: string }>;
}

/** @capability scheduler */
export interface Scheduler {
  /** Run the task declared under `tasks[name]` once after `delayMs`. Survives restarts. */
  after(delayMs: number, task: string, payload?: Json): Promise<{ id: string }>;
  cancel(id: string): Promise<void>;
}

/** Subset of WHATWG fetch, limited to hosts allowed in plugin.yaml. @capability http.fetch */
export interface Http {
  fetch(url: string, init?: {
    method?: string;
    headers?: Record<string, string>;
    body?: string;
    timeoutMs?: number;
  }): Promise<{
    status: number;
    headers: Record<string, string>;
    text(): Promise<string>;
    json<T = unknown>(): Promise<T>;
  }>;
}

/** Cross-plugin events within the same (bot, server). */
export interface Events {
  /**
   * Topic is namespaced by the publisher plugin name: `publish("levelUp", …)` from plugin
   * `xp` is delivered to `subscriptions["xp.levelUp"]`. @capability events.publish
   */
  publish(topic: string, payload: Json): Promise<void>;
}

export interface Portal {
  /**
   * Signed, short-lived URL to a page of the community portal. When `member` is given, the
   * portal session is pre-authenticated for that member. @capability portal.links
   */
  link(path: string, options?: { member?: Member | User; expiresInSeconds?: number }): Promise<string>;
}

export interface Context {
  readonly plugin: { readonly name: string; readonly version: string; readonly instance: string };
  readonly bot: { readonly key: string; readonly user: User };
  readonly server: Server;
  /** Effective config: base config merged with the server override. */
  readonly config: Readonly<PluginConfig>;
  readonly log: Logger;
  readonly kv: KV;
  readonly schedule: Scheduler;
  readonly http: Http;
  readonly events: Events;
  readonly portal: Portal;
}

// ───────────────────────────────────────────────────────────── portal routes

export type Access = "public" | "member" | "moderator" | "admin";

export interface RouteRequest {
  readonly params: Readonly<Record<string, string>>;
  readonly query: Readonly<Record<string, string>>;
  readonly body: Json | undefined;
  /** Authenticated viewer, present unless access is "public" and the viewer is anonymous. */
  readonly viewer?: { readonly user: User; readonly member?: Member };
}

export type RouteResponse = Json | { status: number; body?: Json; headers?: Record<string, string> };

export interface RouteDefinition {
  access: Access;
  handler(ctx: Context, request: RouteRequest): Awaitable<RouteResponse>;
}

/** Keys are "<METHOD> <path>", e.g. "GET /leaderboard" or "POST /answers/:id". */
export type RouteKey = `${"GET" | "POST" | "PUT" | "PATCH" | "DELETE"} /${string}`;

/**
 * Declared types of one route: what the portal sends (`query`, `body`) and receives
 * (`response`, the JSON of a `200`; omit it for routes that answer `204`). A plugin lists
 * them in a type-only contract file (plugin.yaml `spec.routes.types`, e.g.
 * `src/routes.ts`: `export interface Routes { "GET /polls/:id": { response: Poll } }`),
 * checks its handlers against it with `defineRoutes<Routes>()`, and `cicord portal typegen`
 * copies it into the portals, which get a typed client for the routes.
 */
export interface RouteTypes {
  /** Query parameters sent by the portal. They arrive as strings: see `TypedRouteRequest`. */
  query?: object;
  /** JSON body sent by the portal (POST, PUT, PATCH). */
  body?: unknown;
  /** JSON answered with status 200. */
  response?: unknown;
}

/** `{ id: string }` for "GET /polls/:id": the `:name` segments of a route key. */
export type RouteParams<K extends string> = K extends `${string}/:${infer Rest}`
  ? Rest extends `${infer Name}/${infer Tail}`
    ? { [P in Name]: string } & RouteParams<`/${Tail}`>
    : { [P in Rest]: string }
  : {};

/** An explicit response: `{ status: 404, body: { error: … } }`. */
export interface RouteEnvelope {
  status: number;
  body?: unknown;
  headers?: Record<string, string>;
}

/**
 * The request of a route declared with `defineRoutes<Routes>()`. `query` values are the
 * strings of the URL and `body` is the JSON sent: the runtime does not validate them
 * against the declared types, so check them like any input.
 */
export interface TypedRouteRequest<K extends string, T extends RouteTypes> {
  readonly params: Readonly<RouteParams<K>>;
  readonly query: { readonly [Q in keyof T["query"]]?: string } & Readonly<Record<string, string>>;
  readonly body: (T extends { body?: infer B } ? B : Json) | undefined;
  /** Authenticated viewer, present unless access is "public" and the viewer is anonymous. */
  readonly viewer?: { readonly user: User; readonly member?: Member };
}

/** A route whose handler is checked against its declared types (`defineRoutes`). */
export interface TypedRouteDefinition<K extends string, T extends RouteTypes> {
  access: Access;
  handler(
    ctx: Context,
    request: TypedRouteRequest<K, T>,
  ): Awaitable<(T extends { response: infer R } ? R : void) | RouteEnvelope>;
}

/**
 * Routes checked against a contract: every route of `Routes` must be implemented, with the
 * declared parameters, query, body and response. Identity at run time.
 *
 * ```ts
 * import type { Routes } from "./routes.ts";
 * routes: defineRoutes<Routes>({
 *   "GET /polls/:id": { access: "public", async handler(ctx, request) { … request.params.id … } },
 * }),
 * ```
 */
export declare function defineRoutes<Routes extends { [K in keyof Routes]: RouteTypes }>(routes: {
  [K in keyof Routes & RouteKey]: TypedRouteDefinition<K, Routes[K]>;
}): { [K in RouteKey]?: RouteDefinition };

// ───────────────────────────────────────────────────────────── plugin definition

export interface EventHandlers {
  /** @intent ServerMembers */
  memberJoin?(ctx: Context, member: Member): Awaitable<void>;
  /** @intent ServerMembers */
  memberLeave?(ctx: Context, user: User): Awaitable<void>;
  /** @intent ServerMembers */
  memberUpdate?(ctx: Context, member: Member, previous: Member | undefined): Awaitable<void>;
  /** @intent ServerMessages (+ MessageContent to read message.content) */
  messageCreate?(ctx: Context, message: Message): Awaitable<void>;
  /** @intent ServerMessages */
  messageUpdate?(ctx: Context, message: Message): Awaitable<void>;
  /** @intent ServerMessages */
  messageDelete?(ctx: Context, deleted: { id: Snowflake; channel: TextChannel }): Awaitable<void>;
  /**
   * @intent ServerMessageReactions
   * `message` is partial: only `id`, `channel` and the methods are guaranteed (author/content
   * may be missing); fetch it with `channel.messages()` if you need more.
   */
  reactionAdd?(ctx: Context, reaction: { message: Message; user: User; emoji: string }): Awaitable<void>;
  /** @intent ServerMessageReactions — same partial `message` as reactionAdd. */
  reactionRemove?(ctx: Context, reaction: { message: Message; user: User; emoji: string }): Awaitable<void>;
}

export interface PluginDefinition {
  /** Called when the instance starts (new revision, restart, plugin enabled). */
  onStart?(ctx: Context): Awaitable<void>;
  /** Called before the instance stops; must finish quickly. */
  onStop?(ctx: Context): Awaitable<void>;
  /** Gateway event handlers. */
  events?: EventHandlers;
  /** Slash commands, keyed by command name (lowercase, 1–32 chars). Synced per server. */
  commands?: Record<string, CommandDefinition<any>>;
  /**
   * Component handlers keyed by local component id. A component id may carry data after a
   * colon: `"vote:42"` is routed to `components.vote` (`componentId` stays `"vote:42"`).
   */
  components?: Record<string, (ctx: Context, interaction: ComponentInteraction) => Awaitable<void>>;
  /** Modal submit handlers keyed by modal id. */
  modals?: Record<string, (ctx: Context, interaction: ModalInteraction) => Awaitable<void>>;
  /** Recurring jobs (cron syntax, server time zone UTC). @capability scheduler */
  schedules?: Record<string, { cron: string; run(ctx: Context): Awaitable<void> }>;
  /** One-shot tasks started with ctx.schedule.after(). @capability scheduler */
  tasks?: Record<string, (ctx: Context, payload: Json | undefined) => Awaitable<void>>;
  /** Cross-plugin event subscriptions, keyed by "<plugin>.<topic>". @capability events.subscribe */
  subscriptions?: Record<string, (ctx: Context, payload: Json) => Awaitable<void>>;
  /** HTTP routes exposed to the community portal. @capability portal.routes */
  routes?: { [K in RouteKey]?: RouteDefinition };
}

/** Entry point of a plugin: `export default definePlugin({ ... })`. */
export declare function definePlugin(definition: PluginDefinition): PluginDefinition;

// ───────────────────────────────────────────────────────────── globals

/**
 * Globals provided by the plugin host (no Node.js / DOM APIs). Timers are bound to the
 * handler that created them and cancelled when it times out.
 */
declare global {
  function setTimeout(callback: (...args: any[]) => void, ms?: number, ...args: any[]): number;
  function clearTimeout(id: number | undefined): void;
  function setInterval(callback: (...args: any[]) => void, ms?: number, ...args: any[]): number;
  function clearInterval(id: number | undefined): void;
  function queueMicrotask(callback: () => void): void;
  /** Mapped to the instance logger. Declared by interface merging so it composes with lib.dom. */
  interface Console {
    log(...args: any[]): void;
    info(...args: any[]): void;
    warn(...args: any[]): void;
    error(...args: any[]): void;
    debug(...args: any[]): void;
  }
  var console: Console;
}
