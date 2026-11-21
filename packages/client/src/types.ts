/**
 * Types of the CODI runtime HTTP API v1 (spec/api.md). Field names and shapes follow the
 * JSON DTOs of the runtime (`internal/api`, `internal/runtime/status.go`,
 * `internal/reconcile/plan.go`); comments point at the spec section that defines them.
 * Optional (`?`) fields are omitted by the runtime when empty.
 */

export type Json = string | number | boolean | null | Json[] | { [key: string]: Json };

/** RFC 3339 timestamp. */
export type Timestamp = string;

// ───────────────────────────────────────────────────────────── errors (§ intro)

export type ApiErrorCode =
  | "unauthenticated"
  | "forbidden"
  | "not_found"
  | "invalid"
  | "conflict"
  | "unavailable"
  | "internal"
  /** Client side only: the request did not reach the runtime (`status: 0`). */
  | "network"
  // Codes are an open set: unknown codes are passed through.
  | (string & {});

/** Validation finding (`codi validate`), returned with `invalid` errors and by `POST /plan`. */
export interface Diagnostic {
  severity: "error" | "warning";
  /** Relative to the repository root. */
  file?: string;
  line?: number;
  /** JSON pointer inside the document. */
  path?: string;
  message: string;
}

export interface ApiErrorBody {
  error: { code: ApiErrorCode; message: string; diagnostics?: Diagnostic[] };
}

// ───────────────────────────────────────────────────────────── auth & me (§1, §2)

/** Role of a user on a server (§1 Roles). */
export type AccessRole = "admin" | "moderator" | "member";

export interface User {
  id: string;
  username: string;
  displayName: string;
  avatarUrl: string;
  /** Present (true) for bot users only. */
  bot?: boolean;
}

export interface MeServer {
  key: string;
  name: string;
  role: AccessRole;
}

export interface Me {
  user: User;
  servers: MeServer[];
}

// ───────────────────────────────────────────────────────────── status (§3)

export type RuntimeMode = "serve" | "dev";
export type SyncState = "Synced" | "OutOfSync" | "Unknown";
export type HealthState = "Healthy" | "Progressing" | "Degraded" | "Missing";

export interface Revision {
  /** Short identifier shown to users (7-char SHA, or content hash for local sources). */
  id: string;
  /** Full git SHA, or content hash for local sources. */
  sha: string;
  message: string;
  author: string;
  time: Timestamp;
  source: "local" | "git" | (string & {});
}

/**
 * One differing field. `field: "exists"` = the resource is missing in Discord (or not
 * created yet), `"order"` = a reorder, `"managed"` = a mapping about to be dropped.
 */
export interface Drift {
  kind: string;
  key: string;
  field: string;
  expected: Json;
  actual: Json;
}

export interface ServerStatus {
  key: string;
  name: string;
  discordId: string;
  sync: SyncState;
  health: HealthState;
  message: string;
  /** Absent until the server was reconciled once. */
  lastSync?: Timestamp;
  drift: Drift[];
  /** Key of the bot that reconciles this server. */
  bot?: string;
}

export interface PluginInstanceStatus {
  instance: string;
  plugin: string;
  version: string;
  server: string;
  health: HealthState;
  message: string;
}

export interface BotStatus {
  key: string;
  username: string;
  userId: string;
  health: HealthState;
  message: string;
  servers: string[];
  plugins: PluginInstanceStatus[];
}

export interface RuntimeStatus {
  version: string;
  mode: RuntimeMode;
  simulator: boolean;
  /** `null` before the first revision is active (the lists are then empty). */
  revision: Revision | null;
  /** Latest rejected revision / failed fetch or sync, if any. */
  lastError?: string;
  servers: ServerStatus[];
  bots: BotStatus[];
}

export type SyncRecordStatus = "succeeded" | "failed" | "rejected";

export interface SyncRecord {
  id: number;
  time: Timestamp;
  /** Revision id ("" for a self-heal before the first revision). */
  revision: string;
  /** `revision`, `server:<key>` or `bot:<key>`. */
  target: string;
  status: SyncRecordStatus;
  message: string;
  /** Free-form details, e.g. `{plan, applied, failed}` for server syncs. */
  details?: Json;
}

export interface HistoryQuery {
  /** Default 50, at most 500. */
  limit?: number;
  /** Only records of this target (`revision`, `server:<key>`, `bot:<key>`). */
  target?: string;
}

export interface Items<T> {
  items: T[];
}

// ───────────────────────────────────────────────────────────── servers (§4)

export interface ServerDetail extends ServerStatus {
  /** The Server YAML spec as JSON (spec/schemas/server.schema.json#/properties/spec). */
  spec: { [key: string]: Json };
}

export interface ResourceRole {
  /** "" for unmanaged resources. */
  key: string;
  id: string;
  name: string;
  /** "#rrggbb", "" when the role has no color. */
  color: string;
  /** Declared in YAML (has a key). */
  managed: boolean;
}

export interface ResourceCategory {
  key: string;
  id: string;
  name: string;
  managed: boolean;
}

export interface ResourceChannel {
  key: string;
  id: string;
  name: string;
  /** Server YAML channel type (`text`, `voice`, `announcement`, `stage`, `forum`, …). */
  type: string;
  /** Category key; absent when the channel has no managed category. */
  category?: string;
  managed: boolean;
}

export interface ServerResources {
  /** Sorted from the top of the hierarchy; `@everyone` is omitted. */
  roles: ResourceRole[];
  categories: ResourceCategory[];
  channels: ResourceChannel[];
}

export type PlanAction = "create" | "update" | "delete" | "adopt" | "reorder" | "unmanage";

export type PlanKind = "server" | "role" | "category" | "channel" | "everyone" | (string & {});

/** from → to of one field (strings, numbers, booleans, permission lists, overwrite maps, key lists). */
export interface PlanFieldChange {
  from: Json;
  to: Json;
}

export interface PlanChange {
  action: PlanAction;
  kind: PlanKind;
  /**
   * Resource key. For `reorder`: `hierarchy` (roles), `categories`, a category key or
   * `(none)` (channels without a category).
   */
  key: string;
  /** Discord ID of an existing resource. */
  id?: string;
  summary: string;
  fields?: Record<string, PlanFieldChange>;
}

/** An operation the reconciler refuses to perform. */
export interface PlanBlocked {
  action: PlanAction;
  kind: PlanKind;
  key: string;
  id?: string;
  /** e.g. `deletionProtection`, `ownership: …`, `type change: …`, `hierarchy: …`. */
  reason: string;
  /** The differences that could not be applied, when known. */
  fields?: Record<string, PlanFieldChange>;
}

/** Diff between the desired and the live state of one server; changes are in apply order. */
export interface Plan {
  server: string;
  changes: PlanChange[];
  blocked: PlanBlocked[];
}

export interface SyncResult {
  plan: Plan;
  applied: number;
}

export interface AuditEntry {
  id: number;
  time: Timestamp;
  server: string;
  /** `discord:<user id>` or `runtime-token`. */
  actor: string;
  /** e.g. `config.update`, `sync.apply`. */
  action: string;
  target: string;
  details: { [key: string]: Json };
}

export interface AuditQuery {
  /** Only entries with a smaller id (pagination cursor). */
  before?: number | string;
  /** Default 50, at most 500. */
  limit?: number;
}

// ───────────────────────────────────────────────────────────── plugins (§5)

export interface PluginManifestSummary {
  name: string;
  version: string;
  displayName: string;
  description: string;
  capabilities: string[];
  intents: string[];
}

export interface PluginCommandSummary {
  name: string;
  description: string;
}

export interface PluginInstance {
  bot: string;
  instance: string;
  server: string;
  plugin: PluginManifestSummary;
  /** Effective config (defaults applied). */
  config: { [key: string]: Json };
  /**
   * The plugin's config.schema.json as written (key order preserved);
   * `{"type": "object"}` when the plugin has none.
   */
  schema: { [key: string]: Json };
  /** `spec.plugins[].config` of bot.yaml as written (no defaults). */
  baseConfig: { [key: string]: Json };
  /** `overrides.<server>.config` as written, `null` when there is none. */
  override: { [key: string]: Json } | null;
  /** Top-level fields this user may edit (x-codi-access). */
  editable: string[];
  commands: PluginCommandSummary[];
  /** Portal route keys (§6), e.g. "GET /steps". */
  routes: string[];
  health: HealthState;
  message: string;
}

export type ConfigScope = "server" | "base";

export interface UpdatePluginConfigRequest {
  config: { [key: string]: Json };
  /** Default "server". */
  scope?: ConfigScope;
  /** Active revision id the edit is based on (`409 conflict` when it is not the active one). */
  revision?: string;
}

export type WriteBackResult =
  | { mode: "commit"; revision: string; pullRequestUrl: string }
  | { mode: "pullRequest"; revision: ""; pullRequestUrl: string };

// ───────────────────────────────────────────────────────────── portal (§6)

/** The runtime always sends every field; "" means unset. */
export interface PortalTheme {
  /** #RRGGBB */
  primary?: string;
  accent?: string;
  /** URL of the logo (`/api/v1/portal/{server}/logo` or an absolute URL). */
  logo?: string;
  mode?: "light" | "dark" | "system" | "";
}

export interface PortalPlugin {
  bot: string;
  instance: string;
  plugin: string;
  /** Route keys, e.g. "GET /steps". */
  routes: string[];
}

export interface PortalServer {
  key: string;
  name: string;
  iconUrl: string;
  theme: PortalTheme;
  plugins: PortalPlugin[];
}

export interface PortalRole {
  /** Role key ("" for roles not declared in YAML). */
  key: string;
  id: string;
  name: string;
  color: string;
}

export interface PortalMember {
  nick: string;
  joinedAt: Timestamp;
  roles: PortalRole[];
}

export interface PortalMe {
  user: User;
  member: PortalMember;
  role: AccessRole;
}

// ───────────────────────────────────────────────────────────── machine (§7)

export interface RemotePlanResult {
  /** false when the uploaded repository has validation errors (`plans` is then empty). */
  valid: boolean;
  diagnostics: Diagnostic[];
  plans: Plan[];
}

// ───────────────────────────────────────────────────────────── simulator (§8)

export interface SimRole {
  id: string;
  name: string;
  /** "#rrggbb", "" when none. */
  color: string;
  position: number;
  /** Integration-managed role (e.g. a bot's own role). */
  managed: boolean;
}

export interface SimChannel {
  id: string;
  name: string;
  /** Server YAML channel type (`text`, `voice`, `category`, …). */
  type: string;
  /** Category id. */
  parentId?: string;
  position: number;
}

export interface SimMember {
  user: User;
  nick: string;
  /** Role ids. */
  roles: string[];
}

export interface SimServer {
  /** Discord id of the server. */
  id: string;
  /** Repository key of the server. */
  key?: string;
  name: string;
  ownerId: string;
  channels: SimChannel[];
  /** Includes `@everyone` (its id is the server id). */
  roles: SimRole[];
  members: SimMember[];
}

export interface SimState {
  users: User[];
  bots: User[];
  servers: SimServer[];
}

export type ButtonStyle = "primary" | "secondary" | "success" | "danger" | "link";

export interface MessageButton {
  type: "button";
  /** Absent on link buttons. */
  customId?: string;
  label?: string;
  style: ButtonStyle;
  /** Link buttons only. */
  url?: string;
  emoji?: string;
  disabled?: boolean;
}

export interface MessageSelectOption {
  value: string;
  label: string;
  description?: string;
  emoji?: string;
  default?: boolean;
}

export interface MessageSelect {
  type: "select";
  customId: string;
  placeholder?: string;
  minValues?: number;
  maxValues?: number;
  disabled?: boolean;
  options: MessageSelectOption[];
}

export interface MessageRow {
  type: "row";
  components: (MessageButton | MessageSelect)[];
}

export interface MessageEmbed {
  title?: string;
  description?: string;
  url?: string;
  /** "#rrggbb" */
  color?: string;
  timestamp?: Timestamp;
  fields?: { name: string; value: string; inline: boolean }[];
  author?: { name: string; url: string; iconUrl: string };
  footer?: { text: string; iconUrl: string };
  imageUrl?: string;
  thumbnailUrl?: string;
}

export interface Message {
  id: string;
  channelId: string;
  /** Absent for DMs. */
  serverId?: string;
  author: User;
  content: string;
  embeds: MessageEmbed[];
  components: MessageRow[];
  ephemeral: boolean;
  createdAt: Timestamp;
  editedAt?: Timestamp;
}

export interface SimTextInput {
  type: "textInput";
  customId: string;
  label: string;
  style: "short" | "paragraph";
  required?: boolean;
  value?: string;
  placeholder?: string;
}

export interface SimModalRow {
  type: "row";
  components: SimTextInput[];
}

/** A modal shown by the bot (`InteractionResult.modal`). */
export interface SimModal {
  customId: string;
  title: string;
  components: SimModalRow[];
}

export type InteractionStatus = "replied" | "deferred" | "failed" | "modal";

export interface InteractionResult {
  status: InteractionStatus;
  response: Message | null;
  followups: Message[];
  /** Set when `status` is `"modal"`. */
  modal?: SimModal;
  error: string;
}

export interface SimSendMessageRequest {
  userId: string;
  channelId: string;
  content: string;
}

export interface SimCommandRequest {
  userId: string;
  serverId: string;
  channelId: string;
  /** Bot key (an application id is accepted too). */
  bot: string;
  name: string;
  options?: { [key: string]: Json };
}

export interface SimComponentRequest {
  userId: string;
  /** Optional check: the message must be in this channel. */
  channelId: string;
  messageId: string;
  customId: string;
  /** Selected values (select menus). */
  values?: string[];
}

export interface SimModalRequest {
  userId: string;
  serverId: string;
  channelId: string;
  /** Bot key (an application id is accepted too). */
  bot: string;
  /** `SimModal.customId`. */
  customId: string;
  /** Text input custom id → value. */
  fields: Record<string, string>;
}

export type SimCommandOptionType =
  | "string"
  | "integer"
  | "boolean"
  | "user"
  | "channel"
  | "role"
  | "mentionable"
  | "number"
  | "attachment";

export interface SimCommandOption {
  name: string;
  description: string;
  type: SimCommandOptionType;
  required: boolean;
  choices?: { name: string; value: string | number }[];
  minValue?: number;
  maxValue?: number;
}

export interface SimCommand {
  /** Bot key (application id when the bot is not in the repository). */
  bot: string;
  name: string;
  description: string;
  options: SimCommandOption[];
}
