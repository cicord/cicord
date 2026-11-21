/**
 * Types of the CODI runtime HTTP API v1 (spec/api.md). Field names and shapes follow the
 * spec exactly; comments point at the section that defines them.
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
  id: string;
  message: string;
  author: string;
  time: Timestamp;
  source: string;
}

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
  lastSync: Timestamp;
  drift: Drift[];
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
  revision: Revision;
  servers: ServerStatus[];
  bots: BotStatus[];
}

export interface SyncRecord {
  id: string;
  time: Timestamp;
  revision: string;
  target: string;
  status: string;
  message: string;
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
  color: string;
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
  type: string;
  /** Category key ("" when none / unmanaged). */
  category: string;
  managed: boolean;
}

export interface ServerResources {
  roles: ResourceRole[];
  categories: ResourceCategory[];
  channels: ResourceChannel[];
}

export type PlanAction = "create" | "update" | "delete" | "adopt" | "reorder";

export interface PlanChange {
  action: PlanAction;
  kind: string;
  key: string;
  summary: string;
  fields?: Record<string, { from: Json; to: Json }>;
}

export interface PlanBlocked {
  action: PlanAction;
  kind: string;
  key: string;
  reason: string;
}

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
  /** Numeric in the spec example; IDs are strings elsewhere — both are accepted. */
  id: number | string;
  time: Timestamp;
  server: string;
  actor: string;
  action: string;
  target: string;
  details: { [key: string]: Json };
}

export interface AuditQuery {
  before?: number | string;
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
  /** The plugin's config.schema.json. */
  schema: { [key: string]: Json };
  /** Top-level fields this user may edit (x-codi-access). */
  editable: string[];
  commands: PluginCommandSummary[];
  health: HealthState;
  message: string;
}

export type ConfigScope = "server" | "base";

export interface UpdatePluginConfigRequest {
  config: { [key: string]: Json };
  /** Default "server". */
  scope?: ConfigScope;
  /** Active revision id the edit is based on (conflict detection). */
  revision: string;
}

export type WriteBackResult =
  | { mode: "commit"; revision: string; pullRequestUrl: string }
  | { mode: "pullRequest"; revision: ""; pullRequestUrl: string };

// ───────────────────────────────────────────────────────────── portal (§6)

export interface PortalTheme {
  /** #RRGGBB */
  primary?: string;
  accent?: string;
  /** URL of the logo. */
  logo?: string;
  mode?: "light" | "dark" | "system";
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
  key: string;
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
  diagnostics: Diagnostic[];
  plans: Plan[];
}

// ───────────────────────────────────────────────────────────── simulator (§8)

export interface SimRole {
  id: string;
  name: string;
  color?: string;
  [key: string]: Json | undefined;
}

export interface SimChannel {
  id: string;
  name: string;
  type?: string;
  [key: string]: Json | undefined;
}

export interface SimMember {
  user: User;
  nick: string;
  /** Role ids. */
  roles: string[];
}

export interface SimServer {
  id: string;
  name: string;
  channels: SimChannel[];
  roles: SimRole[];
  members: SimMember[];
}

export interface SimState {
  users: User[];
  bots: User[];
  servers: SimServer[];
}

export interface MessageButton {
  type: "button";
  customId: string;
  label: string;
  style: string;
  url: string;
  emoji?: string;
  disabled?: boolean;
}

export interface MessageSelect {
  type: "select";
  customId: string;
  placeholder?: string;
  minValues?: number;
  maxValues?: number;
  options: {
    value: string;
    label: string;
    description?: string;
    emoji?: string;
    default?: boolean;
  }[];
}

export interface MessageRow {
  type: "row";
  components: (MessageButton | MessageSelect)[];
}

export interface MessageEmbed {
  title?: string;
  description?: string;
  url?: string;
  color?: string;
  timestamp?: string;
  fields?: { name: string; value: string; inline?: boolean }[];
  author?: { name: string; url?: string; iconUrl?: string };
  footer?: { text: string; iconUrl?: string };
  image?: { url: string };
  thumbnail?: { url: string };
}

export interface Message {
  id: string;
  channelId: string;
  author: User;
  content: string;
  embeds: MessageEmbed[];
  components: MessageRow[];
  ephemeral: boolean;
  createdAt: Timestamp;
}

export type InteractionStatus = "replied" | "deferred" | "failed" | "modal";

export interface InteractionResult {
  status: InteractionStatus;
  response: Message | null;
  followups: Message[];
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
  bot: string;
  name: string;
  options?: { [key: string]: Json };
}

export interface SimComponentRequest {
  userId: string;
  channelId: string;
  messageId: string;
  customId: string;
  values?: string[];
}

export interface SimCommand {
  bot: string;
  name: string;
  description: string;
  options: Json[];
}
