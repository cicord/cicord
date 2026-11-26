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
  | "UseExternalSounds" | "SendVoiceMessages" | "SendPolls" | "UseExternalApps" | "PinMessages";

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

export interface EntitySelect {
  type: "userSelect" | "roleSelect" | "channelSelect" | "mentionableSelect";
  id: string;
  placeholder?: string;
  minValues?: number;
  maxValues?: number;
  disabled?: boolean;
  defaultValues?: {id: Snowflake; type:"user" | "role" | "channel"}[];
  channelTypes?: Channel["type"][];
}
export type LayoutMedia = { url: string };
export type TextDisplay = {type:"text"; content:string};
export type Thumbnail = {type:"thumbnail"; media:LayoutMedia; description?:string; spoiler?:boolean};
export type Component = ActionRow | TextDisplay
 | {type:"section"; components:TextDisplay[]; accessory:Button | Thumbnail}
 | {type:"mediaGallery"; items:{media:LayoutMedia;description?:string;spoiler?:boolean}[]}
 | {type:"separator"; divider?:boolean; spacing?:1|2}
 | {type:"file"; file:LayoutMedia; spoiler?:boolean}
 | {type:"container"; components:Component[];accentColor?:string;spoiler?:boolean};
export interface ActionRow {
  type: "row";
  components: (Button | SelectMenu | EntitySelect)[];
}

/**
 * Who a message may notify (Discord's `allowed_mentions`). Without it, every mention in the
 * content notifies. `true` allows every mention of that type, `false` none, a list only
 * those users or roles: ids (snowflakes, not keys) or objects with an `id` (a User, Member
 * or Role), at most 100 per list. Even when allowed, a role that is not mentionable (and
 * `@everyone` / `@here`) only notifies if the bot has MentionEveryone in the channel.
 *
 * ```ts
 * // Pings exactly this role, nobody else, whatever else the content mentions.
 * await channel.send({ content: `${role.mention} new poll`, allowedMentions: { roles: [role.id], users: [] } });
 * ```
 */
export interface AllowedMentions {
  users?: boolean | readonly (Snowflake | { readonly id: Snowflake })[];
  roles?: boolean | readonly (Snowflake | { readonly id: Snowflake })[];
  everyone?: boolean;
  /** Notify the author of the message replied to (`message.reply`). Default false once allowedMentions is set. */
  repliedUser?: boolean;
}

export interface UploadFile {
  name: string;
  data: Uint8Array;
  contentType?: string;
  description?: string;
}

export interface NativePoll {
  question: string;
  answers: { text:string; emoji?:string }[];
  durationHours: number;
  multiselect?: boolean;
}
export interface PollResults {
  question:string; answers:{id:number;text:string;emoji?:string;votes:number}[];
  multiselect:boolean; finalized:boolean;
}
export interface MessagePayload {
  /** Components V2. Exclusive with content, embeds and components. */
  layout?: Component[];
  /** Channel sends only. Discord deduplicates enforced nonces for the past few minutes. */
  nonce?: string;
  enforceNonce?: boolean;
  /** Up to ten files. Embeds can use attachment://filename. */
  files?: UploadFile[];
  poll?: NativePoll;
  content?: string;
  embeds?: Embed[];
  components?: ActionRow[];
  allowedMentions?: AllowedMentions;
}

export type MessageInput = string | MessagePayload;

/** Interaction replies can additionally be ephemeral (visible only to the invoking user). */
export type ReplyInput = string | (MessagePayload & { ephemeral?: boolean });

export interface Attachment {
  description?: string;
  /** Read up to 8 MiB from the Discord CDN. @capability discord.messages.read */
  bytes(): Promise<Uint8Array>;
  id: Snowflake;
  filename: string;
  url: string;
  contentType?: string;
  size: number;
}

export interface Message {
  readonly url: string;
  readonly reference?: { readonly messageId: Snowflake; readonly channelId?: Snowflake; readonly serverId?: Snowflake };
  readonly reactions: readonly { readonly emoji: string; readonly count: number; readonly me: boolean }[];
  /** @capability discord.messages.manage */
  pin(): Promise<void>;
  /** @capability discord.messages.manage */
  unpin(): Promise<void>;
  /** @capability discord.messages.send */
  crosspost(): Promise<void>;
  /** Normal reaction users, ascending by ID. Default 25, maximum 100. @capability discord.messages.read */
  reactionUsers(emoji: string, options?: {limit?: number; after?: Snowflake}): Promise<User[]>;
  /** Omitting userId removes the bot's reaction. @capability discord.messages.manage */
  removeReaction(emoji: string, userId?: Snowflake): Promise<void>;
  readonly poll?: PollResults;
  endPoll():Promise<Message>;
  /** Discord returns at most 100 voters for one answer. Pass the last ID as after. */
  pollVoters(answerId:number,after?:Snowflake):Promise<readonly Snowflake[]>;
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
  /**
   * The message's embeds (what a bot posted as rich content), in the shape a plugin sends
   * them. Emptied like `content` without the MessageContent intent (the bot's own messages
   * excepted).
   */
  readonly embeds: readonly Readonly<Embed>[];
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

export type UserFlag = "staff" | "partner" | "hypesquad" | "bugHunterLevel1" | "hypesquadOnlineHouse1" | "hypesquadOnlineHouse2" | "hypesquadOnlineHouse3" | "premiumEarlySupporter" | "teamPseudoUser" | "bugHunterLevel2" | "verifiedBot" | "verifiedDeveloper" | "certifiedModerator" | "botHTTPInteractions";
export interface User {
  readonly createdAt: Date;
  readonly flags: readonly UserFlag[];
  readonly id: Snowflake;
  readonly username: string;
  readonly displayName: string;
  readonly avatarUrl?: string;
  readonly bot: boolean;
  /** `<@id>` */
  readonly mention: string;
}

export interface Role {
  /** Discord integration owns the role. Unknown when the role is unavailable. */
  readonly managed?: boolean;
  /** Git declares the member list on this role. */
  readonly declaredMembers?: boolean;
  readonly id: Snowflake;
  /** Key in servers/<server>.yaml when the role is managed by Cicord. */
  readonly key?: string;
  readonly name: string;
  readonly color?: string;
  readonly mention: string;
  /**
   * Position in the role hierarchy: higher is higher, `@everyone` is 0. Two roles may share
   * a position (Discord then orders them by id). `undefined` when the role is not known to
   * the runtime (not cached yet, or an older engine): treat it as unknown, never as 0.
   */
  readonly position?: number;
  /** Whether anyone may mention the role. `undefined` when the role is not known. */
  readonly mentionable?: boolean;
  /**
   * The members holding the role, in ascending user id order: at most `limit` (default 100,
   * at most 1000), those after the user id `after` (to page past `limit`). Discord has no
   * "members of a role" endpoint: the runtime lists the server's members (1000 per request,
   * at most 100,000 members; the listing is shared by the server's plugins, reused for up to
   * 60 s and kept current by member events) and filters them. Throws for `@everyone`.
   * plugin.yaml must declare the ServerMembers intent (checked at call time).
   * @capability discord.members.read
   * @intent ServerMembers
   */
  members(options?: { limit?: number; after?: Snowflake }): Promise<Member[]>;
}

export interface Channel {
  /** Current member-specific allow/deny row on this guild channel. Synchronized children
   * carry their category's rows. For the category itself, look it up by categoryId and
   * call memberOverwrite there. Absent rows resolve to undefined. DMs are unsupported.
   * @capability discord.channels.read */
  memberOverwrite(userId: Snowflake): Promise<{allow: Permission[]; deny: Permission[]} | undefined>;
  readonly id: Snowflake;
  /** Key in servers/<server>.yaml when the channel is managed by Cicord. */
  readonly key?: string;
  readonly name: string;
  readonly type: "text" | "voice" | "announcement" | "stage" | "forum" | "media" | "thread" | "category" | "dm";
  /** Id of the channel's category; `undefined` when it has none (or it is not known). */
  readonly categoryId?: Snowflake;
  readonly mention: string;
}

export interface PurgeFilter {
  limit: number;
  author?: Snowflake;
  contains?: string;
  bots?: boolean;
  attachments?: boolean;
  before?: Snowflake;
  after?: Snowflake;
  reason?: string;
}

export interface TextChannel extends Channel {
  /** @capability discord.messages.read */
  pins(): Promise<Message[]>;
  /** @capability discord.messages.send */
  typing(): Promise<void>;
  /** @capability discord.webhooks.manage */
  sendAs(identity: {username: string; avatarUrl?: string}, message: MessageInput): Promise<Message>;
  /** Deletes up to 100 recent guild messages. Requires discord.messages.manage and discord.messages.read. */
  bulkDelete(ids: Snowflake[], reason?: string): Promise<number>;
  /** Scans up to limit messages, newest first. Old or nonmatching messages are counted as skipped. */
  previewPurge(filter: PurgeFilter): Promise<{ ids: Snowflake[]; skipped: number }>;
  purge(filter: PurgeFilter): Promise<{ deleted: number; skipped: number }>;
  /** @capability discord.messages.send */
  send(message: MessageInput): Promise<Message>;
  /** @capability discord.messages.read */
  messages(options?: { limit?: number; before?: Snowflake; after?: Snowflake }): Promise<Message[]>;
  /**
   * One message by id, `undefined` when it does not exist (or was deleted). Same content
   * gating as `messages()`. @capability discord.messages.read
   */
  message(id: Snowflake): Promise<Message | undefined>;
  /** @capability discord.threads.manage */
  createThread(name: string, options?: { private?: boolean; fromMessage?: Snowflake }): Promise<TextChannel>;
}

export interface Member {
  readonly pending?: boolean;
  readonly flags: readonly ("didRejoin" | "completedOnboarding" | "bypassesVerification")[];
  readonly premiumSince?: Date;
  readonly timedOutUntil?: Date;
  readonly avatarUrl?: string;
  /** Role keys assigned to this user by roles.*.members on the active revision. */
  readonly declaredRoles?: readonly string[];
  /** Effective permissions for the current invocation. Missing information returns an empty list. */
  permissionsIn(channel: ChannelRef | Channel): readonly Permission[];
  /** Both ViewChannel and ReadMessageHistory are required to expose past content. */
  canView(channel: ChannelRef | Channel): boolean;
  readonly user: User;
  readonly id: Snowflake;
  readonly nickname?: string;
  readonly displayName: string;
  readonly mention: string;
  readonly roles: readonly Role[];
  readonly joinedAt: Date;
  /**
   * Server-level permissions: those of `@everyone` and of every role of the member; all of
   * them for the server owner and for Administrator. Channel overwrites (and timeouts) are
   * NOT applied: this is not what the member may do in a given channel. Roles the runtime
   * does not know count for nothing. `undefined` on an older engine.
   */
  readonly permissions?: readonly Permission[];
  /**
   * Cicord access level, as for portal routes: `"admin"` for the server owner and
   * `spec.access.admins` of servers/<server>.yaml, `"moderator"` for
   * `spec.access.moderators`, else `"member"`. `undefined` on an older engine.
   */
  readonly access?: "member" | "moderator" | "admin";
  hasRole(role: RoleRef): boolean;
  /** @capability discord.roles.assign */
  setRoles(roles: RoleRef[], reason?: string): Promise<void>;
  /** @capability discord.roles.assign */
  addRole(role: RoleRef, reason?: string): Promise<void>;
  /** @capability discord.roles.assign */
  removeRole(role: RoleRef, reason?: string): Promise<void>;
  /** @capability discord.members.manage */
  setNickname(nickname: string | null, reason?: string): Promise<void>;
  /** @capability discord.members.moderate */
  removeTimeout(reason?: string): Promise<void>;
  timeout(durationMs: number, reason?: string): Promise<void>;
  /** @capability discord.members.moderate */
  kick(reason?: string): Promise<void>;
  /** @capability discord.members.moderate */
  ban(options?: { reason?: string; deleteMessagesSeconds?: number }): Promise<void>;
  /** Direct message. An optional receipt permits later retraction by this instance. @capability discord.dm.send */
  send(message: MessageInput, options?: {retractable?:boolean}): Promise<Message>;
  /**
   * Move the member, who must be connected to voice, to a voice channel. The bot needs Move
   * Members, and Connect in the destination (whose user limit does not apply to moves).
   * @capability discord.voice.move
   */
  moveTo(channel: ChannelRef | Channel, reason?: string): Promise<void>;
  /** Disconnect the member from voice. @capability discord.voice.move */
  disconnect(reason?: string): Promise<void>;
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
  /** Voice channels: maximum number of connected members, 0–99 (0, the default, is no limit). */
  userLimit?: number;
  /** Voice channels: bits per second, 8000–384000 (above 96000 needs server boosts). */
  bitrate?: number;
}

/**
 * A change to one overwrite target of `server.editChannel()`: `allow` and `deny` set these
 * permissions (taking them out of the other side), `clear` makes them neutral again
 * (inherited). The target's other permissions are kept.
 */
export interface OverwritePatch {
  allow?: Permission[];
  deny?: Permission[];
  clear?: Permission[];
}

export interface EditChannelOptions {
  name?: string;
  topic?: string;
  /** Voice channels: 0–99 (0 is no limit). */
  userLimit?: number;
  /** Voice channels: 8000–384000. */
  bitrate?: number;
  /**
   * Overwrites to change, keyed by target like `CreateChannelOptions.overwrites`; `null`
   * removes a target's overwrite. Targets not listed are kept, including what the channel
   * got from its category:
   *
   * ```ts
   * // Lock a room without touching who may see it.
   * await ctx.server.editChannel(room, { overwrites: { "@everyone": { deny: ["Connect"] } } });
   * ```
   */
  overwrites?: Record<string, OverwritePatch | null>;
  /** Audit log reason. */
  reason?: string;
}

export type AudienceRelation = "same" | "narrower" | "wider" | "disjoint" | "unknown";
export interface Audience {
  /** Opaque version of all read-policy inputs. Available only for a known policy. */
  readonly fingerprint?:string;
  readonly known: boolean;
  readonly public: boolean;
  readonly allowRoles: readonly Snowflake[];
  readonly allowMembers: readonly Snowflake[];
  readonly denyMembers: readonly Snowflake[];
}

export interface AuditLogEntry {
  id: Snowflake; at: Date; action: string; actorId?: Snowflake; actor?: User;
  targetId?: Snowflake; targetType?:string; reason?: string; mergeable: boolean;
  changes: {key:string;old?:Json;new?:Json;addedPermissions?:Permission[];removedPermissions?:Permission[]}[]; options?: Record<string,Json>;
  attribution:{kind:"cicord"|"human"|"bot"|"unknown"; certainty:"confirmed"|"probable"|"unknown";
    plugin?:{plugin:string;bot:string;instance:string};revision?:string};
}

export interface Overlay {
  id: string;
  reason: string;
  until: Date;
  channels?: Record<string, {slowmodeSeconds?: number; overwrites?: Record<string, { deny?: Permission[]; neutral?: Permission[] }> }>;
  server?: {verificationLevel: "low" | "medium" | "high" | "highest"};
  members?: Record<Snowflake, {suspendDeclaredRoles: true}>;
}

export interface ScheduledEventInput {
  name:string; description?:string; start:string; end?:string; channel?:Snowflake; location?:string; image?:string; recurrence?:Json;
}
export interface ScheduledEvent extends ScheduledEventInput {
  readonly id:Snowflake; readonly status:string;
  edit(changes:Partial<ScheduledEventInput>):Promise<ScheduledEvent>;
  delete():Promise<void>;
  /** Interested user IDs, 100 per page. */
  users(after?:Snowflake):Promise<readonly Snowflake[]>;
}
export interface ServerProtection {
 readonly known:boolean;
 readonly selfHeal:boolean;
 readonly deletionProtection:boolean;
 readonly paused:boolean;
 readonly totalResources:number;
 readonly unmanaged:readonly {readonly id:Snowflake;readonly name:string;readonly kind:"role"|"channel"}[];
 readonly botTopPosition?:number;
}
export interface Server {
 /** Current guild policy and eligible resources visible to this bot. A failed or incomplete read returns known:false. @capability discord.channels.read */
 protection():Promise<ServerProtection>;
  readonly preferredLocale?: string;
  /** Paginated by ascending user id. Shares the role.members() cache, grant and intent. */
  members(options?: { limit?: number; after?: Snowflake }): Promise<Member[]>;
  /** Send a DM even after the recipient left or was banned. @capability discord.dm.send */
  sendDM(userId: Snowflake, message: MessageInput):Promise<Message>;
  /** Temporary restrictions owned by this installation. @capability server.overlays */
  readonly overlays: {
    apply(overlay: Overlay): Promise<void>;
    remove(id: string): Promise<void>;
    list(): Promise<(Overlay & {origin:string})[]>;
  };
  /** Restrict invites or DMs for up to 24 hours. @capability server.incident */
  incident(actions: {invitesDisabledUntil?:Date; dmsDisabledUntil?:Date}): Promise<void>;
  /** Request a full sync of the active YAML revision. @capability server.sync */
  requestSync(reason: string): Promise<void>;
  /** Read current Discord policies. confined checks every possible role combination against
   * the supplied role/member ceiling. Viewer access also checks current membership and age gates. */
  verifyReaders(channel: ChannelRef|Channel, readers:{roles:readonly Snowflake[];members:readonly Snowflake[];viewer?:Snowflake;withoutMember?:Snowflake;destination?:ChannelRef|Channel}):Promise<{known:boolean;confined:boolean;fingerprint:string;viewerCanRead:boolean;withoutMemberFingerprint?:string;destination:AudienceRelation}>;
  /** Remove a DM previously sent as retractable by this instance. @capability discord.dm.send */
  retractDM(messageId:Snowflake): Promise<void>;
  scheduledEvents():Promise<readonly ScheduledEvent[]>;
  createScheduledEvent(event:ScheduledEventInput):Promise<ScheduledEvent>;
  invites():Promise<ServerInvite[]>;
  /** Up to 100 recent entries. Requires discord.auditlog.read and ServerModeration. */
  auditLog(options?:{action?: string;actorId?:Snowflake;before?:Snowflake;limit?:number}):Promise<AuditLogEntry[]>;
  /** Read policy summary. Use compareAudience for copies, which also checks combinations of roles. */
  audience(channel: ChannelRef | Channel): Audience;
  compareAudience(source: ChannelRef | Channel, destination: ChannelRef | Channel): AudienceRelation;
  /** Ban a user, including one outside the server. @capability discord.members.moderate */
  ban(userId: Snowflake, options?: { reason?: string; deleteMessagesSeconds?: number }): Promise<void>;
  /** Lift a ban. Already unbanned is success. @capability discord.members.moderate */
  unban(userId: Snowflake, reason?: string): Promise<void>;
  /** Sorted by user ID, up to 1000 results. @capability discord.members.moderate */
  bans(options?: { limit?: number; after?: Snowflake }): Promise<{user: User;reason?: string}[]>;
  readonly id: Snowflake;
  /** metadata.name of the Server resource. */
  readonly key: string;
  readonly name: string;
  readonly iconUrl?: string;
  /** User id of the server owner, read live; `undefined` when not known (or an older engine). */
  readonly ownerId?: Snowflake;
  /** Discord inactive voice channel, when configured. */
  readonly afkChannelId?: Snowflake;
  /** Resolve a channel by key or id. Throws if it does not exist. @capability discord.channels.read */
  channel(ref: ChannelRef): TextChannel;
  /** Resolve a role by key or id. Throws if it does not exist. */
  role(ref: RoleRef): Role;
  /** @capability discord.members.read */
  member(userId: Snowflake): Promise<Member | undefined>;
  /**
   * Create a channel owned by this plugin instance (e.g. a ticket). The reconciler never
   * prunes plugin-owned channels. A channel created in a category without `overwrites` gets
   * the category's (Discord syncs it). @capability discord.channels.manage
   */
  createChannel(options: CreateChannelOptions): Promise<TextChannel>;
  /**
   * Edit a channel owned by this plugin instance (created by `createChannel`); overwrites
   * are patched, see `EditChannelOptions`. @capability discord.channels.manage
   */
  editChannel(ref: ChannelRef | Channel, options: EditChannelOptions): Promise<TextChannel>;
  /** Delete a channel owned by this plugin instance. @capability discord.channels.manage */
  deleteChannel(ref: ChannelRef | Channel, reason?: string): Promise<void>;
  /**
   * The members connected to a voice channel, in ascending user id order (bots included).
   * Read from the runtime's voice states, which the gateway sends when the server becomes
   * available: until then (just after a start or a reconnect) it throws rather than answer
   * "nobody". plugin.yaml must declare the ServerVoiceStates intent (checked at call time).
   * @capability discord.members.read
   * @intent ServerVoiceStates
   */
  voiceMembers(channel: ChannelRef | Channel): Promise<Member[]>;
}

// ───────────────────────────────────────────────────────────── interactions

/**
 * Discord requires an answer within 3 seconds. If a handler has not replied after ~2 s, the
 * runtime defers automatically (ephemeral if the command/component says so) and a later
 * `reply()` edits the deferred response transparently.
 */
export interface Interaction {
  readonly locale?: string;
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

/** A suggestion returned by an autocomplete handler (value: string for string options). */
export interface AutocompleteChoice {
  /** 1–100 characters, shown to the user. */
  name: string;
  /** What the option receives when picked (strings: at most 100 characters). */
  value: string | number;
}

/** The option being typed: `value` is the raw text so far (a string for every option type). */
export interface FocusedOption {
  readonly name: string;
  readonly value: string;
}

/**
 * Sent while the user types an option that has `autocomplete`. It cannot reply: the handler
 * returns the choices.
 */
export interface AutocompleteInteraction {
  readonly locale?: string;
  readonly id: Snowflake;
  readonly user: User;
  readonly member?: Member;
  readonly channel?: TextChannel;
  readonly command: string;
  readonly group?: string;
  readonly subcommand?: string;
  /** Other options already filled in: raw values (ids for users, roles and channels). */
  readonly options: Readonly<Record<string, string | number | boolean>>;
}

export interface CommandOption {
  type: CommandOptionType;
  description: string;
  required?: boolean;
  choices?: readonly { name: string; value: string | number }[];
  /** integer / number options. */
  min?: number;
  max?: number;
  /** string options: length bounds (0–6000, maxLength ≥ 1). */
  minLength?: number;
  maxLength?: number;
  /**
   * Suggestions while the user types (string, integer and number options; not with
   * `choices`). Answer within ~2.5 s with at most 25 choices: an autocomplete cannot be
   * deferred, and a handler that fails or is late shows an empty list.
   */
  autocomplete?(
    ctx: Context,
    interaction: AutocompleteInteraction,
    focused: FocusedOption,
  ): Awaitable<readonly AutocompleteChoice[]>;
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
  /** Top-level command name. */
  readonly command: string;
  /** Group of the invoked subcommand (`/polls admin purge` → `"admin"`). */
  readonly group?: string;
  /** Invoked subcommand (`/polls close` → `"close"`). */
  readonly subcommand?: string;
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
  fields: ({
    id: string;
    label: string;
    style?: "short" | "paragraph";
    required?: boolean;
    placeholder?: string;
    value?: string;
    minLength?: number;
    maxLength?: number;
  } | {type:"label"; label:string; description?:string; component: ((SelectMenu|EntitySelect) & {required?:boolean}) | {type:"textInput"; id:string; style?:"short"|"paragraph"; required?:boolean;placeholder?:string;value?:string;minLength?:number;maxLength?:number}})[];
}

export interface ModalInteraction extends Interaction {
  readonly modalId: string;
  readonly selections: Readonly<Record<string, readonly string[]>>;
  readonly fields: Readonly<Record<string, string>>;
}

export interface CommandDefinition<O extends Record<string, CommandOption> = Record<string, CommandOption>> {
  description: string;
  /** Mapped so that `autocomplete` handlers get typed parameters. */
  options?: { [K in keyof O]: O[K] & CommandOption };
  /** Ephemeral replies and auto-defer by default. */
  ephemeral?: boolean;
  /** Default member permissions required to see/use the command. */
  permissions?: Permission[];
  run(ctx: Context, interaction: CommandInteraction<O>): Awaitable<void>;
}

/** A subcommand: `/polls close poll:…`. Its options are typed like a command's. */
export interface SubcommandDefinition<O extends Record<string, CommandOption> = Record<string, CommandOption>> {
  description: string;
  /** Mapped so that `autocomplete` handlers get typed parameters. */
  options?: { [K in keyof O]: O[K] & CommandOption };
  run(ctx: Context, interaction: CommandInteraction<O>): Awaitable<void>;
}

/** A group of subcommands: `/polls admin purge`. A group holds only subcommands. */
export interface SubcommandGroupDefinition {
  description: string;
  subcommands: Record<string, SubcommandDefinition<any>>;
}

/**
 * A command made of subcommands (and groups of subcommands): at most 25 of them in all. The
 * command itself cannot be invoked: it has no `run` and no `options`. `ephemeral` and
 * `permissions` apply to every subcommand.
 */
export interface CommandWithSubcommandsDefinition {
  description: string;
  ephemeral?: boolean;
  permissions?: Permission[];
  subcommands?: Record<string, SubcommandDefinition<any>>;
  groups?: Record<string, SubcommandGroupDefinition>;
  options?: never;
  run?: never;
}

/**
 * Helper that infers option types for `interaction.options`.
 *
 * ```ts
 * command({ description: "Ping", run: (ctx, i) => i.reply("pong") });
 * command({
 *   description: "Manage polls",
 *   subcommands: {
 *     close: subcommand({
 *       description: "Close a poll",
 *       options: { poll: { type: "string", description: "Poll", required: true, autocomplete: suggestPolls } },
 *       async run(ctx, interaction) { … interaction.options.poll … },
 *     }),
 *   },
 * });
 * ```
 */
export declare function command<const O extends Record<string, CommandOption> = {}>(
  definition: CommandDefinition<O>,
): CommandDefinition<O>;
export declare function command(definition: CommandWithSubcommandsDefinition): CommandWithSubcommandsDefinition;

/** Helper that infers option types for a subcommand (`command({ subcommands: { … } })`). */
export declare function subcommand<const O extends Record<string, CommandOption> = {}>(
  definition: SubcommandDefinition<O>,
): SubcommandDefinition<O>;

// ───────────────────────────────────────────────────────────── runtime services

export interface Logger {
  debug(message: string, fields?: Record<string, Json>): void;
  info(message: string, fields?: Record<string, Json>): void;
  warn(message: string, fields?: Record<string, Json>): void;
  error(message: string, fields?: Record<string, Json>): void;
}

export interface KVEntry<T extends Json = Json> { value: T; version: string }
export interface KVAtomic {
  check(key: string, version: string | null | undefined): this;
  set(key: string, value: Json, options?: { ttlSeconds?: number }): this;
  delete(key: string): this;
  increment(key: string, by?: number): this;
  rankSet(name: string, member: string, score: number): this;
  rankRemove(name: string, member: string): this;
  commit(): Promise<{ok: boolean}>;
}
export interface RankedMember { member: string; score: number; rank: number }
export interface Ranking {
  add(member: string, delta: number): Promise<number>;
  set(member: string, score: number): Promise<number>;
  score(member: string): Promise<number | undefined>;
  rank(member: string): Promise<number | undefined>;
  top(options?: {limit?: number; offset?: number}): Promise<RankedMember[]>;
  around(member: string, radius: number): Promise<RankedMember[]>;
  remove(member: string): Promise<void>;
  count(): Promise<number>;
}

/** Key-value storage scoped to the plugin instance. Values are JSON. @capability storage.kv */
export interface KV {
  getEntry<T extends Json = Json>(key: string): Promise<KVEntry<T> | undefined>;
  getMany<T extends Json = Json>(keys: readonly string[]): Promise<(T | undefined)[]>;
  atomic(): KVAtomic;
  ranking(name: string): Ranking;
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

/** Opaque logical reference, resolved only by the host at an approved destination. */
export type SecretRef = string & { readonly __secretRef: unique symbol };

/** Attach an exact `member-${userId}` tag for each represented member to bind privacy cleanup, including after grant withdrawal. */
export interface IndexDocument { title?: string; text: string; tags?: string[]; meta?: Json }
export interface IndexHit { id: string; score: number; snippet: string; meta: Json }
export interface FullTextIndex {
  upsert(id: string, document: IndexDocument): Promise<void>;
  remove(id: string): Promise<void>;
  clear(): Promise<void>;
  search(query: string, options?: {limit?: number;tags?: string[]}): Promise<IndexHit[]>;
}

export interface InstanceFiles {
  list(glob?: string): Promise<string[]>;
  read(path: string): Promise<string>;
  bytes(path: string): Promise<Uint8Array>;
}

/** Subset of WHATWG fetch, limited to hosts allowed in plugin.yaml. @capability http.fetch */
export interface Http {
  /** A whole-URL SecretRef requires use: webhook.send, POST and a JSON message. It returns status only. */
  fetch(url: string | SecretRef, init?: {
    method?: string;
    headers?: Record<string, string | SecretRef>;
    form?: Record<string, string | SecretRef>;
    json?: Record<string, Json | SecretRef>;
    body?: string;
    timeoutMs?: number;
  }): Promise<{
    status: number;
    headers: Record<string, string>;
    bytes(): Promise<Uint8Array>;
    text(): Promise<string>;
    json<T = unknown>(): Promise<T>;
  }>;
}

/** Cross-plugin events scoped to the bot or the server, as declared by the publisher. */
export interface Events {
  /**
   * Topic is namespaced by the publisher plugin name: `publish("levelUp", …)` from plugin
   * `xp` is delivered to `subscriptions["xp.levelUp"]`. @capability events.publish
   */
  publish<N extends string, V extends number = 1>(
    topic: N,
    payload: `${N}.v${V}` extends keyof PublishedEvents ? PublishedEvents[`${N}.v${V}`] : Json,
    options?: { version?: V; idempotencyKey?: string },
  ): Promise<void>;
}

export interface CaseScope { readonly kind: "case"; readonly id: string }

export interface Portal {
  /** Requires a matching Discord OAuth identity. A scoped session can only visit scoped routes of its issuer. */
  link(path: string, options: { user: Snowflake | User | Member; scope: CaseScope; expiresInSeconds?: number }): Promise<string>;
  /** Invalidates existing links and sessions for this installation and case. */
  revoke(scope: CaseScope): Promise<void>;
  /**
   * Signed, short-lived URL to a page of the community portal. When `member` is given, the
   * portal session is pre-authenticated for that member. @capability portal.links
   */
  link(
    path: string,
    options?: { member?: Member | User; expiresInSeconds?: number },
  ): Promise<string>;
}

export type ImageSource = {asset: string} | {url: string};
export type ImageFont = "Inter" | "Noto" | "emoji" | {asset: string};
export type ImageLayer =
  | {type: "text"; x: number; y: number; text: string; size: number; color: string; font?: ImageFont; align?: "left" | "center" | "right"; maxWidth?: number; ellipsis?: boolean}
  | {type: "image"; x: number; y: number; width: number; height: number; source: ImageSource; mask?: "circle" | "rounded"; radius?: number}
  | {type: "rect"; x: number; y: number; width: number; height: number; color: string}
  | {type: "progress"; x: number; y: number; width: number; height: number; color: string; value: number; trackColor?: string}
  | {type: "chart"; x: number; y: number; width: number; height: number; color: string; kind: "bar" | "line"; values: number[]};

export interface ImageSpec {
  width: number;
  height: number;
  background?: string | {gradient: [string,string]} | ImageSource;
  layers?: ImageLayer[];
}

export interface AnalyticsSummary {
  from:string; to:string; granularity:"day"|"week";
  kpis:{metric:string;value:number}[];
  series:{metric:string;dim:string;points:[number,number][]}[];
  channels:{id:string;name:string;value:number}[];
  heatmap:number[][];
}
export interface EventMeta {
  readonly id: string;
  readonly topic: string;
  readonly version: number;
  readonly at: string;
  readonly publisher: { readonly plugin: string; readonly bot: string; readonly instance: string };
}
export interface InteropCall {
  readonly caller: {
    readonly plugin: string;
    readonly bot: string;
    readonly instance: string;
    readonly source: string;
    readonly verified: boolean;
  };
  readonly onBehalfOf?: {
    readonly userId: string;
    readonly access: "member" | "moderator" | "admin" | "scoped";
    readonly resourceScope?: { readonly kind: string; readonly id: string };
  };
}
export type InteropCode =
  | "not_found"
  | "ambiguous"
  | "denied"
  | "timeout"
  | "invalid_input"
  | "handler_error"
  | "cycle";
export declare class InteropError extends Error {
  readonly code: InteropCode;
  constructor(code: InteropCode, message: string);
}
export declare class PluginUnavailableError extends InteropError {
  constructor(message?: string);
}
/** Augmented by cicord plugin typegen from the installed providers' JSON Schemas. */
export interface QueryContracts {}
export interface ActionContracts {}
export interface EventContracts {}
export interface PublishedEvents {}
export interface SubscribedEvents {}
type InputOf<C, K> = K extends keyof C ? (C[K] extends { input: infer I } ? I : Json) : Json;
type OutputOf<C, K> = K extends keyof C ? (C[K] extends { output: infer O } ? O : Json) : Json;
export interface Plugins {
  has(plugin: string): boolean;
  hasFeature(plugin: string, feature: string): boolean;
  query<K extends string, P extends string = string, M extends string = string>(
    plugin: P,
    method: M,
    input: InputOf<QueryContracts, string extends K ? `${P}.${M}` : K>,
    opts?: { timeoutMs?: number },
  ): Promise<OutputOf<QueryContracts, string extends K ? `${P}.${M}` : K>>;
  call<K extends string, P extends string = string, M extends string = string>(
    plugin: P,
    method: M,
    input: InputOf<ActionContracts, string extends K ? `${P}.${M}` : K>,
    opts: { idempotencyKey: string; timeoutMs?: number },
  ): Promise<OutputOf<ActionContracts, string extends K ? `${P}.${M}` : K>>;
}
export interface Query<I = Json, O = Json> {
  run(ctx: Context, input: I, call: InteropCall): Awaitable<O>;
}
export interface Action<I = Json, O = Json> {
  run(
    ctx: Context,
    input: I,
    call: InteropCall & { readonly idempotencyKey: string },
  ): Awaitable<O>;
}
export declare function query<I = Json, O = Json>(handler: Query<I, O>): Query<I, O>;
export declare function action<I = Json, O = Json>(handler: Action<I, O>): Action<I, O>;

export interface Context {
  readonly i18n: {t(key: string, vars?: Record<string,string|number|boolean>, options?: {locale?:string}): string};
  readonly assets: {read(path: string): Promise<Uint8Array>};
  /** Declarative PNG rendering, at most 2048 × 2048. @capability render */
  readonly render: {image(spec: ImageSpec): Promise<{data: Uint8Array; contentType: "image/png"}>};
  /** Image digests and bitwise Hamming distance of equal-length hexadecimal digests. @capability media.analyze */
  readonly media: {
    hash(source: Attachment | string, options: {algo: "sha256" | "phash" | "dhash"}): Promise<string>;
    distance(a: string, b: string): number;
  };
  readonly analytics:{summary(query:{from:string;to:string;granularity:"day"|"week"}):Promise<AnalyticsSummary>};
  readonly features: {
    enabled(feature: string): boolean;
    report(feature: string, active: boolean, reason?: string): void;
  };
  readonly plugins: Plugins;
  readonly plugin: { readonly name: string; readonly version: string; readonly instance: string };
  readonly bot: { readonly key: string; readonly user: User };
  readonly server: Server;
  /** Effective config: base config merged with the server override. */
  readonly config: Readonly<PluginConfig>;
  readonly log: Logger;
  /** Pseudonym for the subject of the current privacy hook. */
  readonly privacy: { pseudonym(userId: string): Promise<string> };
  readonly kv: KV;
  readonly schedule: Scheduler;
  readonly http: Http;
  readonly status: {set(key:string, message:string|null):void};
  readonly files: InstanceFiles;
  index(name: string, options?: {language?: "en" | "fr"}): FullTextIndex;
  readonly secrets: {has(name: string): boolean; ref(name: string): SecretRef};
  readonly events: Events;
  readonly portal: Portal;
}

// ───────────────────────────────────────────────────────────── portal routes

export type Access = "scoped" | "public" | "member" | "moderator" | "admin";

export interface RouteRequest {
  readonly params: Readonly<Record<string, string>>;
  readonly query: Readonly<Record<string, string>>;
  readonly body: Json | undefined;
  readonly scope?: CaseScope;
  /** Authenticated viewer, present unless access is "public" and the viewer is anonymous. */
  readonly viewer?: { readonly user: User; readonly member?: Member };
}

export type RawRouteResponse = { status: number; raw: string; contentType: "text/calendar" | "text/csv" | "text/plain" | "text/markdown" };
export type RouteResponse = RawRouteResponse | Json | { status: number; body?: Json; headers?: Record<string, string> };

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
  readonly scope?: CaseScope;
  /** Authenticated viewer, present unless access is "public" and the viewer is anonymous. */
  readonly viewer?: { readonly user: User; readonly member?: Member };
}

/** A route whose handler is checked against its declared types (`defineRoutes`). */
export interface TypedRouteDefinition<K extends string, T extends RouteTypes> {
  access: Access;
  handler(
    ctx: Context,
    request: TypedRouteRequest<K, T>,
  ): Awaitable<(T extends { response: infer R } ? R : void) | RouteEnvelope | RawRouteResponse>;
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

/**
 * A member joined, left or moved between voice channels, or changed their voice state
 * (mute, deafen, stream, camera). Compare `channel` and `previous` to tell them apart.
 */
export interface VoiceStateUpdate {
  readonly user: User;
  /** The member (Discord sends it with every voice state of a server). */
  readonly member?: Member;
  /** The voice channel the member is in now; `undefined` when they left voice. */
  readonly channel?: Channel;
  /**
   * The voice channel they were in before this update, from the runtime's voice states;
   * `undefined` when they were not in voice, or when the runtime does not know (an update
   * that arrives just after a start or a reconnect).
   */
  readonly previous?: Channel;
  readonly selfMute: boolean;
  readonly selfDeaf: boolean;
  /** Streaming (Go Live). */
  readonly streaming: boolean;
  /** Camera on. */
  readonly video: boolean;
}

export interface StructureChange<T> {
  managed: boolean; willHeal: boolean; previous?: T;
}
export interface ServerInvite {
  code: string; channelId?:Snowflake; inviter?:User; maxUses?:number; uses?:number;
  expiresAt?:Date; vanity?:boolean;
}
export interface EventHandlers {
  scheduledEventCreate?(ctx:Context,event:ScheduledEvent):Awaitable<void>;
  scheduledEventUpdate?(ctx:Context,event:ScheduledEvent):Awaitable<void>;
  scheduledEventDelete?(ctx:Context,event:ScheduledEvent):Awaitable<void>;
  scheduledEventUserAdd?(ctx:Context,event:{eventId:Snowflake;userId:Snowflake}):Awaitable<void>;
  scheduledEventUserRemove?(ctx:Context,event:{eventId:Snowflake;userId:Snowflake}):Awaitable<void>;
  pollVoteAdd?(ctx:Context,event:{channelId:Snowflake;messageId:Snowflake;userId:Snowflake;answerId:number}):Awaitable<void>;
  pollVoteRemove?(ctx:Context,event:{channelId:Snowflake;messageId:Snowflake;userId:Snowflake;answerId:number}):Awaitable<void>;

  /** Guild availability or resumed gateway after live permissions are refreshed. */
  serverAvailable?(ctx:Context):Awaitable<void>;
  channelCreate?(ctx:Context,event:StructureChange<Channel> & {channel:Channel}):Awaitable<void>;
  channelUpdate?(ctx:Context,event:StructureChange<Channel> & {channel:Channel}):Awaitable<void>;
  channelDelete?(ctx:Context,event:StructureChange<Channel> & {channel:Channel}):Awaitable<void>;
  roleCreate?(ctx:Context,event:StructureChange<Role> & {role:Role}):Awaitable<void>;
  roleUpdate?(ctx:Context,event:StructureChange<Role> & {role:Role}):Awaitable<void>;
  roleDelete?(ctx:Context,event:StructureChange<Role> & {role:Role}):Awaitable<void>;
  threadCreate?(ctx:Context,event:{thread:Channel;parent?:Channel}):Awaitable<void>;
  threadDelete?(ctx:Context,event:{thread:Channel;parent?:Channel}):Awaitable<void>;
  serverUpdate?(ctx:Context,event:{changes:Record<string,{old:Json;new:Json}>}):Awaitable<void>;
  expressionsUpdate?(ctx:Context,event:{emojis?:{id:string;name:string}[];stickers?:{id:string;name:string}[]}):Awaitable<void>;
  webhooksUpdate?(ctx:Context,event:{channelId:Snowflake}):Awaitable<void>;
  inviteCreate?(ctx:Context,event:ServerInvite):Awaitable<void>;
  inviteDelete?(ctx:Context,event:ServerInvite):Awaitable<void>;
  automodAction?(ctx:Context,event:{rule:string;userId:Snowflake;channelId?:Snowflake;action:string;matched?:string}):Awaitable<void>;
 auditLogEntry?(ctx:Context,entry:AuditLogEntry):Awaitable<void>;
  /** @intent ServerMembers */
  /** Both ban notifications need the ServerModeration intent. */
  banAdd?(ctx: Context, event: {user: User;reason?: string}): Awaitable<void>;
  banRemove?(ctx: Context, event: {user: User}): Awaitable<void>;
  memberJoin?(ctx: Context, member: Member): Awaitable<void>;
  /** @intent ServerMembers */
  memberLeave?(ctx: Context, user: User, previous?: Member): Awaitable<void>;
  /** @intent ServerMembers */
  memberUpdate?(ctx: Context, member: Member, previous: Member | undefined): Awaitable<void>;
  /** Delivered only after the bot host selects a server and validates membership. @intent DirectMessages */
  directMessage?(ctx: Context, message: Message): Awaitable<void>;
  /** @intent ServerMessages (+ MessageContent to read message.content) */
  messageCreate?(ctx: Context, message: Message): Awaitable<void>;
  /** @intent ServerMessages */
  messageUpdate?(ctx: Context, message: Message): Awaitable<void>;
  /** @intent ServerMessages */
  messageDelete?(ctx: Context, deleted: { id: Snowflake; channel: TextChannel }): Awaitable<void>;
  /**
   * @intent ServerMessageReactions
   * `message` is partial: only `id`, `channel` and the methods are guaranteed (author/content
   * may be missing); fetch it with `message.channel.message(message.id)` if you need more.
   */
  reactionAdd?(ctx: Context, reaction: { message: Message; user: User; emoji: string }): Awaitable<void>;
  /** @intent ServerMessageReactions — same partial `message` as reactionAdd. */
  reactionRemove?(ctx: Context, reaction: { message: Message; user: User; emoji: string }): Awaitable<void>;
  /** @intent ServerVoiceStates */
  voiceStateUpdate?(ctx: Context, update: VoiceStateUpdate): Awaitable<void>;
  /**
   * The bot joined the server (invited again after a removal, for instance) while
   * connected; not sent for servers loading after a connection or an outage. Like every
   * event, it only reaches instances of servers declared for the bot. No intent to declare.
   */
  serverJoin?(ctx: Context, server: { id: Snowflake; name: string; iconUrl?: string }): Awaitable<void>;
  /**
   * The bot left the server: kicked, banned, removed or the server was deleted (not when
   * Discord has an outage). Discord API calls for the server fail from now on. No intent to
   * declare.
   */
  serverLeave?(ctx: Context, server: { id: Snowflake }): Awaitable<void>;
}

export declare const sdkFeatures: Readonly<{contextMenus: true}>;
export type ContextMenuDefinition =
 | { type:"message"; permissions?: Permission[]; ephemeral?: boolean; run(ctx:Context,i:Interaction & {readonly target:Message}):Awaitable<void> }
 | { type:"user"; permissions?: Permission[]; ephemeral?: boolean; run(ctx:Context,i:Interaction & {readonly target:Member}):Awaitable<void> };
export interface PluginDefinition {
  contextMenus?: Record<string,ContextMenuDefinition>;
  privacy?: {
    export(ctx: Context, userId: string): Promise<Json>;
    erase(ctx: Context, userId: string): Promise<{ erased: number }>;
  };
  /** Called when the instance starts (new revision, restart, plugin enabled). */
  onStart?(ctx: Context): Awaitable<void>;
  /** Called before the instance stops; must finish quickly. */
  onStop?(ctx: Context): Awaitable<void>;
  /** Gateway event handlers. */
  events?: EventHandlers;
  /** Slash commands, keyed by command name (lowercase, 1–32 chars). Synced per server. */
  commands?: Record<string, CommandDefinition<any> | CommandWithSubcommandsDefinition>;
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
  /** A task may be delivered more than once. Its ID is stable across attempts. */
  tasks?: Record<string, (ctx: Context, payload: Json | undefined, task?: { readonly id: string; readonly attempt: number }) => Awaitable<void>>;
  /** Cross-plugin event subscriptions, keyed by "<plugin>.<topic>". @capability events.subscribe */
  subscriptions?: {
    [K in keyof SubscribedEvents]?: (ctx: Context, payload: SubscribedEvents[K], meta: EventMeta) => Awaitable<void>;
  } & Record<string, {run(ctx: Context, payload: Json, meta: EventMeta): Awaitable<void>}["run"]>;
  queries?: Record<string, Query<any, any>>;
  actions?: Record<string, Action<any, any>>;
  /** HTTP routes exposed to the community portal. @capability portal.routes */
  routes?: { [K in RouteKey]?: RouteDefinition };
}

/** Entry point of a plugin: `export default definePlugin({ ... })`. */
export declare function definePlugin<S extends NonNullable<PluginDefinition["subscriptions"]>>(
  definition: PluginDefinition & {subscriptions: S},
): PluginDefinition & {subscriptions: S};
export declare function definePlugin(definition: PluginDefinition): PluginDefinition;

// ───────────────────────────────────────────────────────────── globals

/**
 * Globals provided by the plugin host (no Node.js / DOM APIs). Timers are bound to the
 * handler that created them and cancelled when it times out.
 */
declare global {
  interface Crypto {
    getRandomValues<T extends Uint8Array>(view: T): T;
    randomUUID(): `${string}-${string}-${string}-${string}-${string}`;
  }
  var crypto: Crypto;
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
