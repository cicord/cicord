/**
 * Types of the Cicord runtime HTTP API v1 (https://cicord.dev/docs/reference/http-api/). Field names and shapes follow the
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

/** Validation finding (`cicord validate`), returned with `invalid` errors and by `POST /plan`. */
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
  error: {
    code: ApiErrorCode;
    message: string;
    diagnostics?: Diagnostic[];
    /** How to fix it (plan / sync failures). */
    hint?: string;
    /** Raw technical text (plan / sync failures). */
    details?: string;
    /** Humanized error code ({@link HumanErrorCode}) of plan / sync failures. */
    reason?: string;
  };
}

/** Stable codes of humanized errors (https://cicord.dev/docs/reference/http-api/ "Error object"); unknown codes may appear. */
export type HumanErrorCode =
  | "community_required"
  | "boost_required"
  | "missing_permissions"
  | "missing_access"
  | "role_hierarchy"
  | "unknown_resource"
  | "invalid_form_body"
  | "limit_reached"
  | "invalid_token"
  | "intents_disallowed"
  | "rate_limited"
  | "discord_unavailable"
  | "timeout"
  | "bot_not_connected"
  | "no_bot"
  | "unresolved_reference"
  | "revision_rejected"
  | "fetch_failed"
  | "bad_request"
  | "deletion_protection"
  | "ownership"
  | "managed_role"
  | "type_change"
  | "recreate_required"
  | "unknown"
  | (string & {});

/** An error explained for people (https://cicord.dev/docs/reference/http-api/ "Error object"). */
export interface HumanError {
  code: HumanErrorCode;
  /** One sentence: what went wrong. */
  message: string;
  /** How to fix it. */
  hint?: string;
  /** The raw technical error text. */
  details?: string;
  /** Discord JSON error code (e.g. 50035) and HTTP status, for Discord REST errors. */
  discordCode?: number;
  httpStatus?: number;
  /** The planned change that failed. */
  resource?: { action: string; kind: string; key?: string };
  /** Discord "Invalid Form Body" field errors, flattened. */
  fields?: { path: string; code?: string; message: string }[];
  /** Number of changes that failed in the same sync (when more than one). */
  failures?: number;
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
  /** `message` explained, when the server is Degraded or Missing. */
  error?: HumanError;
  // Live Discord state (runtime cache). Optional: absent on older runtimes.
  /** Server icon URL, `""` = none (or not cached yet). */
  iconUrl?: string;
  /** Server banner URL (boost level 2), `""` = none. */
  bannerUrl?: string;
  /** Invite splash URL (boost level 1), `""` = none. */
  splashUrl?: string;
  /** Boost level (Discord premium tier), 0–3. */
  boostLevel?: number;
  /** Number of boosts. */
  boostCount?: number;
  /** Discord feature flags (`COMMUNITY`, `BANNER`, `ROLE_ICONS`…), sorted. */
  features?: string[];
  /** The `COMMUNITY` feature is enabled. */
  community?: boolean;
}

/** Server images editable from the admin UI (`PUT /servers/{server}/assets/{kind}`). */
export type ServerAssetKind = "icon" | "banner" | "splash";

/** Result of a server image upload (https://cicord.dev/docs/reference/http-api/servers/, "Server assets"). */
export interface ServerAssetResult {
  /** `commit` (applied, `revision` set) or `pullRequest` (`pullRequestUrl` set). */
  mode: "commit" | "pullRequest" | (string & {});
  revision: string;
  pullRequestUrl: string;
  /** Repository path of the written file, e.g. `servers/assets/ma-commu-icon.png`. */
  path: string;
  /** Value set in `spec.profile.<kind>`, e.g. `./assets/ma-commu-icon.png`. */
  value: string;
  format: "png" | "jpeg" | "gif" | "webp" | (string & {});
  width: number;
  height: number;
  /** Non-blocking remarks (size, aspect ratio). */
  warnings: string[];
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
  /** `message` explained, when the bot is Degraded or Missing. */
  error?: HumanError;
}

export interface RuntimeStatus {
  version: string;
  mode: RuntimeMode;
  simulator: boolean;
  /** `null` before the first revision is active (the lists are then empty). */
  revision: Revision | null;
  /** Latest rejected revision / failed fetch or sync, if any. */
  lastError?: string;
  /** `lastError` explained. */
  lastErrorInfo?: HumanError;
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
  /** `message` explained, for `failed` and `rejected` records. */
  error?: HumanError;
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

/** Sort direction of a paged list. */
export type SortOrder = "asc" | "desc";

/**
 * Query of a *paged* list (https://cicord.dev/docs/reference/http-api/ "Data tables"): `q` keeps items containing every word
 * (case-insensitive), `sort` is one of the endpoint's keys, `limit` is capped at 200.
 */
export interface PageParams<S extends string = string> {
  q?: string;
  sort?: S;
  order?: SortOrder;
  offset?: number;
  limit?: number;
}

/** A window of a paged list; `total` counts the filtered items before the window. */
export interface Page<T> {
  items: T[];
  total: number;
  offset: number;
  limit: number;
  sort: string;
  order: SortOrder;
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
  /** `reason` explained. */
  error?: HumanError;
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

// ───────────────────────────────────────────────────────────── analytics

/**
 * Window of `GET /servers/{server}/analytics/summary?period=`. KPIs and rankings are
 * compared with the previous period of the same length.
 */
export type AnalyticsPeriod = "7d" | "30d" | "90d" | "365d";

/** Bucket size of `GET /servers/{server}/analytics/series` (UTC hours, days, ISO weeks). */
export type AnalyticsStep = "hour" | "day" | "week";

/**
 * How points of a metric combine inside a bucket: `counter` values are summed, `max`
 * keeps the highest value, `last` the most recent one (gauges and daily distinct counts).
 */
export type AnalyticsKind = "counter" | "max" | "last" | (string & {});

/** Unit of a metric's values. */
export type AnalyticsUnit = "count" | "members" | "seconds" | "level" | (string & {});

/** Metric names of the analytics catalog. */
export type AnalyticsMetric =
  | "members.total"
  | "members.online"
  | "members.joined"
  | "members.left"
  | "members.joined.age"
  | "members.joined.bots"
  | "members.roles.added"
  | "members.roles.removed"
  | "members.boosting"
  | "server.boost.tier"
  | "members.active"
  | "members.active.chat"
  | "members.active.voice"
  | "invites.uses"
  | "invites.created"
  | "onboarding.completed"
  | "messages.sent"
  | "messages.sent.bots"
  | "messages.edited"
  | "messages.deleted"
  | "messages.attachments"
  | "messages.replies"
  | "messages.stickers"
  | "reactions.added"
  | "threads.created"
  | "polls.created"
  | "voice.seconds"
  | "voice.sessions"
  | "voice.peak"
  | "voice.streaming"
  | "stage.seconds"
  | "events.interested"
  | "moderation.actions"
  | "automod.triggers"
  | "automod.blocked"
  | "commands.used"
  | "components.used"
  | "presence.status"
  | "presence.platform"
  | "presence.activity";

/** A metric group that is not collected, with the reason and the fix. */
export interface AnalyticsMissing {
  metrics: string[];
  reason: string;
  fix: string;
}

/** Collection status of a server's analytics. */
export interface AnalyticsStatus {
  enabled: boolean;
  /**
   * Detailed presence is collected (`spec.analytics.presence` and the privileged
   * ServerPresences intent); false: only Discord's approximate online count.
   */
  presence: boolean;
  /** Oldest bucket recorded; null before the first point. */
  since: Timestamp | null;
  /** How long points are kept. */
  retentionDays: number;
  /** What cannot be collected now and why (empty when everything is collected). */
  missing: AnalyticsMissing[];
}

/** Kind of entity a dimension resolves to. */
export type AnalyticsLabelKind =
  | "channel"
  | "voice"
  | "thread"
  | "role"
  | "emoji"
  | "event"
  | "invite"
  | "label"
  | (string & {});

/** Display name of a dimension (a channel or role ID, a custom emoji…). */
export interface AnalyticsLabel {
  name: string;
  kind: AnalyticsLabelKind;
  /** Set for custom emojis. */
  imageUrl?: string;
  /** Set for roles (`#rrggbb`). */
  color?: string;
  /** The channel, role or emoji no longer exists. */
  deleted?: boolean;
}

/** One value of a series: `[unix seconds of the bucket start, value]`. */
export type AnalyticsPoint = [time: number, value: number];

/** One metric (and dimension) over time. */
export interface AnalyticsSerie {
  metric: string;
  /** `""` for the metric's total, `"other"` for the dimensions beyond `top`. */
  dim: string;
  points: AnalyticsPoint[];
}

/**
 * `GET /servers/{server}/analytics/series`. `from` / `to` are aligned outward to the step.
 * Counters are zero-filled (one point per step); gauges and daily distinct metrics only
 * have the steps with data.
 */
export interface AnalyticsSeries {
  server: string;
  from: Timestamp;
  to: Timestamp;
  step: AnalyticsStep;
  series: AnalyticsSerie[];
  labels: Record<string, AnalyticsLabel>;
  status: AnalyticsStatus;
}

/** Query of `GET /servers/{server}/analytics/series`. */
export interface AnalyticsSeriesParams {
  /** One or more metric names. */
  metrics: readonly (AnalyticsMetric | (string & {}))[];
  /** Start (RFC 3339 or a Date). Default: `to` minus 7 days; at most 400 days per request. */
  from?: Date | Timestamp;
  /** End (RFC 3339 or a Date). Default: now. */
  to?: Date | Timestamp;
  /** Default `day`. At most 2000 points per series. */
  step?: AnalyticsStep;
  /**
   * One dimension only (a channel ID, an emoji…). Without it, a dimensioned metric returns
   * its total (`dim: ""`) first, then the top dimensions in rank order, then `"other"` when
   * more dimensions exist than `top`.
   */
  dim?: string;
  /** How many dimensions before `"other"` (default 8, at most 50). */
  top?: number;
}

/** A headline number with the same number over the previous period. */
export interface AnalyticsKpi {
  metric: AnalyticsMetric | "members.net" | (string & {});
  value: number;
  /** Value over the previous period of the same length; null when not covered by the data. */
  previous: number | null;
  /** The KPI per day over the period, for a sparkline. */
  spark: AnalyticsPoint[];
}

/** One entry of a ranking (top channels, emojis, invites…). */
export interface AnalyticsRanked {
  dim: string;
  value: number;
  /** The entry's value over the previous period; null when not covered. */
  previous: number | null;
}

/**
 * `GET /servers/{server}/analytics/summary`. The window is calendar days including today
 * (`to` = tomorrow 00:00 UTC, `from` = `to` − N days); the previous period is the N days before.
 */
export interface AnalyticsSummary {
  server: string;
  period: AnalyticsPeriod;
  from: Timestamp;
  to: Timestamp;
  /**
   * In this order: members.total (last value), members.joined, members.left, members.net
   * (joined − left), members.active (average per day), messages.sent, voice.seconds,
   * members.online (average of the samples), reactions.added, commands.used,
   * moderation.actions.
   */
  kpis: AnalyticsKpi[];
  /** Top 10 per metric name over the period. */
  rankings: Record<string, AnalyticsRanked[]>;
  /** Human messages per weekday and hour (UTC): `heatmap[weekday][hour]`, weekday 0 = Monday. */
  heatmap: number[][];
  labels: Record<string, AnalyticsLabel>;
  status: AnalyticsStatus;
}

/** A metric as `GET /servers/{server}/analytics/catalog` reports it. */
export interface AnalyticsCatalogEntry {
  name: AnalyticsMetric | (string & {});
  kind: AnalyticsKind;
  /** What the dimension holds ("channel", "emoji"…); absent when the metric has none. */
  dim?: string;
  unit: AnalyticsUnit;
  /** Where the data comes from. */
  source: string;
  /** Needs `spec.analytics.presence` and the privileged ServerPresences intent. */
  presence?: boolean;
  collected: boolean;
}

/** `GET /servers/{server}/analytics/catalog`. */
export interface AnalyticsCatalog {
  metrics: AnalyticsCatalogEntry[];
  status: AnalyticsStatus;
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
  /** Top-level fields this user may edit (x-cicord-access). */
  editable: string[];
  commands: PluginCommandSummary[];
  /** Portal route keys (§6), e.g. "GET /steps". */
  routes: string[];
  health: HealthState;
  message: string;
  /** `cicord` (official catalog), a registry name, or `local` (written in the repository). */
  source: string;
  trust: PluginTrust;
  /**
   * Set when a newer version of the plugin is available (§11). Registry versions come from
   * the runtime's cache (filled by the background refresh and by browsing the Store).
   */
  update?: PluginUpdate;
}

/** Where a plugin instance comes from: Store trust, or `local` for plugins written in the repo. */
export type PluginTrust = "official" | "unofficial" | "local";

/** Sort keys of `GET /servers/{server}/plugins`. */
export type PluginSortKey = "name" | "instance" | "bot" | "version" | "health" | "source";

export interface PluginListParams extends PageParams<PluginSortKey> {
  /** Only instances with (`true`) or without (`false`) an available update. */
  updates?: boolean;
}

/** `DELETE /servers/{server}/plugins/{bot}/{instance}`. */
export interface UninstallResult {
  /**
   * `true` when the instance was deleted from bot.yaml; `false` when it also runs on other
   * servers and only this server was removed from it.
   */
  removed: boolean;
  /** What went with the last instance of the plugin (`cicord.lock: acme/xp`, `plugins/xp/`). */
  cleaned: string[];
  mode: "commit" | "pullRequest";
  revision?: string;
  pullRequestUrl?: string;
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

// ───────────────────────────────────────────────────────────── token mode (§1)

/**
 * Response of the token-mode logins (`POST /auth/token`, and `/auth/link` or
 * `/auth/dev-login` with `mode: "token"`): a bearer session token for embedding sites.
 */
export interface TokenSession {
  /** `cicord_st_…`, sent as `Authorization: Bearer <token>`. */
  token: string;
  expiresAt: Timestamp;
  me: Me;
}

// ───────────────────────────────────────────────────────────── portal (§6)

/** The runtime always sends every field; "" means unset. */
export interface PortalTheme {
  /** #RRGGBB */
  primary?: string;
  accent?: string;
  /**
   * URL of the logo: `/api/v1/portal/{server}/logo?v=<revision>` or an absolute URL.
   * `@cicord/client` makes runtime paths absolute when `baseUrl` is set (embedded portals).
   */
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

/** Icons of `spec.portal.links[].icon` (server.schema.json). */
export type PortalLinkIcon =
  | "link"
  | "website"
  | "discord"
  | "youtube"
  | "twitch"
  | "x"
  | "bluesky"
  | "mastodon"
  | "instagram"
  | "tiktok"
  | "github"
  | "reddit"
  | "steam"
  | "rules"
  | "calendar"
  | "shop"
  // Future icons are passed through.
  | (string & {});

export interface PortalLink {
  label: string;
  /** http(s) URL (validated by the schema; check it again with `isSafeUrl` before rendering). */
  url: string;
  /** Always set ("link" by default). */
  icon: PortalLinkIcon;
}

/** Page toggles of `spec.portal.pages`, resolved (unset = true). */
export interface PortalPages {
  onboarding: boolean;
  profile: boolean;
  leaderboard: boolean;
  tickets: boolean;
}

export type PortalLocale = "en" | "fr" | "";

export interface PortalServer {
  key: string;
  name: string;
  iconUrl: string;
  theme: PortalTheme;
  /** `spec.portal.title`, or the server name. */
  title: string;
  /** "" when unset. */
  tagline: string;
  /** Markdown subset ("" when unset); render it with `<Markdown>` from @cicord/ui. */
  description: string;
  /** URL of the banner image ("" when unset); like `theme.logo`. */
  banner: string;
  links: PortalLink[];
  pages: PortalPages;
  /** "" = let the portal choose (browser language). */
  locale: PortalLocale;
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
  /** Autocomplete results, set for a `SimCommandRequest` with `autocomplete`. */
  choices?: { name: string; value: string | number }[];
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
  /** Command path: `"poll"`, `"polls close"`, `"polls admin purge"`. */
  name: string;
  options?: { [key: string]: Json };
  /**
   * Type `value` in the autocomplete option `option` instead of running the command
   * (`options` are the other options already filled in); the result carries `choices`.
   */
  autocomplete?: { option: string; value: string };
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
  | "subcommand"
  | "subcommandGroup"
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
  minLength?: number;
  maxLength?: number;
  /** The bot suggests values while the user types (see `SimCommandRequest.autocomplete`). */
  autocomplete?: boolean;
  /** Options of a subcommand (plain options) or of a subcommand group (subcommands). */
  options?: SimCommandOption[];
}

export interface SimCommand {
  /** Bot key (application id when the bot is not in the repository). */
  bot: string;
  name: string;
  description: string;
  options: SimCommandOption[];
}

// ───────────────────────────────────────────────────────────── store (§10, §11)

/**
 * Trust of a Store source. Computed by the runtime, never declared: `official` only for the
 * `cicord` source (bundled plugins, plus the official registry once it exists) and the official
 * registry URL, whose index must be signed by an official key; every other registry is
 * `unofficial`.
 */
export type StoreTrust = "official" | "unofficial";

/** `bundled`: the official plugins in the binary (source `cicord`); `git`: a registry. */
export type StoreSourceKind = "bundled" | "git";

/** An instance created from a Store plugin. */
export interface StoreInstallation {
  server: string;
  bot: string;
  instance: string;
}

/** A plugin as listed by the Store (from the catalog or the registry index). */
export interface StorePlugin {
  /** Source name (`cicord` or a registry of `cicord.yaml spec.registries`). */
  source: string;
  name: string;
  displayName: string;
  description: string;
  keywords: string[];
  version?: string;
  publisher?: string;
  /** Only meaningful in official sources. */
  verified: boolean;
  /** Plugin repository. */
  repo?: string;
  trust: StoreTrust;
  /** Instances using this source/plugin. */
  installed: StoreInstallation[];
  /** Set when the installed version is not the latest (installed plugins only). */
  update?: PluginUpdate;
}

/**
 * `registry`: the cicord.lock version is not the highest release tag of the plugin repository
 * (any major version). `bundled`: a copy of an official plugin (`plugins/<name>/`) is older
 * than the version bundled in the runtime.
 */
export type PluginUpdateKind = "registry" | "bundled";

/** The highest part of the version that changes. */
export type PluginUpdateLevel = "major" | "minor" | "patch";

/** An available update of an installed plugin. */
export interface PluginUpdate {
  kind: PluginUpdateKind;
  current: string;
  latest: string;
  level: PluginUpdateLevel;
  /**
   * Bundled copies: the copy was changed in the repository (or cannot be told apart from a
   * local change). Updating overwrites it and must be forced.
   */
  modified?: boolean;
}

/**
 * A plugin source as listed by `GET /store?summary=1` (no plugins). A registry that cannot be
 * fetched has an `error`.
 */
export interface StoreSourceSummary {
  name: string;
  kind: StoreSourceKind;
  url?: string;
  trust: StoreTrust;
  fetchedAt?: Timestamp;
  error?: HumanError;
  /** A registry with an access token (`secretRef` in cicord.yaml). */
  private: boolean;
  /** Private registries: whether this runtime has the token. */
  credentials?: "ok" | "missing";
  /**
   * Official registry index signature: `verified` (signed by an official key) or `invalid`
   * (missing or wrong: the source has an `error` and lists no remote plugins). Absent for
   * unofficial registries and for a `cicord` source with only bundled plugins.
   */
  signature?: StoreSignature;
  pluginCount: number;
}

/** Signature state of an official registry index. */
export type StoreSignature = "verified" | "invalid";

/** A plugin source (`GET /store`). A registry that cannot be fetched has an `error` and no plugins. */
export interface StoreSource extends StoreSourceSummary {
  plugins: StorePlugin[];
}

/** Sort keys of `GET /store/plugins`. */
export type StorePluginSortKey =
  | "name"
  | "source"
  | "trust"
  | "version"
  | "publisher"
  | "installed";

/** `GET /store/plugins` (default limit 50). */
export interface StorePluginParams extends PageParams<StorePluginSortKey> {
  /** Only this source. */
  source?: string;
  trust?: StoreTrust;
  /** Only plugins with (`true`) or without (`false`) an instance. */
  installed?: boolean;
  /** Only installed plugins with (`true`) or without (`false`) an available update. */
  updates?: boolean;
}

/**
 * `GET /store/{source}/{plugin}`: the plugin at its latest resolved version, with what it
 * requests (read from its plugin.yaml at that version, not from the index).
 */
export interface StoreDetail extends StorePlugin {
  version: string;
  /** Resolved commit (registry plugins); send it back with the install. */
  commit?: string;
  /** `sha256-…` of the reviewed sources (registry plugins); send it back with the install. */
  integrity?: string;
  capabilities: string[];
  intents: string[];
  /** The subset of `intents` that must be enabled in the Discord developer portal. */
  privilegedIntents: string[];
  /** Hosts the plugin may call (network egress). */
  network: string[];
  /** config.schema.json (`{"type": "object"}` when the plugin has none). */
  schema: { [key: string]: Json };
  /**
   * Slash commands, or `null` when unknown: listing them evaluates the plugin's code, which
   * the runtime only does for `official` sources before installation.
   */
  commands: PluginCommandSummary[] | null;
}

/** `POST /servers/{server}/store/install`. */
export interface StoreInstallRequest {
  source: string;
  plugin: string;
  /** Default: the only bot managing the server (`400` when several do). */
  bot?: string;
  /** Default: the plugin name (`409 conflict` when taken on that bot). */
  instance?: string;
  config?: { [key: string]: Json };
  /**
   * Registry plugins: the `version`, `commit` and `integrity` of the reviewed `StoreDetail`
   * (required). `409 changed` when the latest version now resolves differently.
   */
  version?: string;
  commit?: string;
  integrity?: string;
  /** Required (`true`) for `unofficial` sources, otherwise `409 confirmation_required`. */
  confirmUnofficial?: boolean;
}

/** What a new version adds to and removes from a list (sorted). */
export interface ListDiff {
  added: string[];
  removed: string[];
}

/** What the new version requests compared with the installed one. */
export interface PermissionDiff {
  capabilities: ListDiff;
  intents: ListDiff;
  /** Intents that must be enabled in the Discord developer portal. */
  privilegedIntents: ListDiff;
  /** Hosts the plugin may call. */
  network: ListDiff;
}

/** The installed version of a plugin (read from the active configuration). */
export interface InstalledPluginVersion {
  version: string;
  /** Registry plugins: the cicord.lock entry. */
  commit?: string;
  integrity?: string;
  repo?: string;
  capabilities: string[];
  intents: string[];
  privilegedIntents: string[];
  network: string[];
}

/** What an update changes for one instance on one server. */
export interface PluginUpdateInstance {
  server: string;
  bot: string;
  instance: string;
  /** Registry plugins: the version range of the instance (bot.yaml). */
  range?: string;
  /** The range the update writes, when the new version is outside `range`. */
  newRange?: string;
  /** Grants are set to what the new version requests. */
  grantsAdded: string[];
  grantsRemoved: string[];
  /** The instance's current config is not valid for the new version's config schema. */
  diagnostics: Diagnostic[];
}

/** What a bundled update does to `plugins/<name>/` (paths relative to it). */
export interface PluginUpdateFiles {
  changed: string[];
  added: string[];
  removed: string[];
}

/**
 * `GET /store/{source}/{plugin}/update` (admin of a server): the latest version against what
 * is installed. Computed without evaluating the new version's code.
 */
export interface StoreUpdateReview {
  /** `false` when the installed version is the latest. */
  available: boolean;
  update: PluginUpdate | null;
  /** The new version, as `GET /store/{source}/{plugin}`: send its version/commit/integrity back. */
  detail: StoreDetail;
  current: InstalledPluginVersion;
  diff: PermissionDiff;
  instances: PluginUpdateInstance[];
  /** Bundled copies only. */
  files?: PluginUpdateFiles;
  /** Bundled copies: modified locally (the update needs `force`). */
  modified: boolean;
  /** Registry plugins: the registry index now points to another repository than cicord.lock. */
  repoChanged: boolean;
  /** Servers where an instance runs: the update needs the admin role on each of them. */
  servers: string[];
  /** `false` when an instance's config is invalid for the new version (`instances[].diagnostics`). */
  configValid: boolean;
  /** Servers among `servers` where the caller is not admin (the update is refused). */
  missingAdmin: string[];
  /**
   * For each server of `missingAdmin`, how many instances run there. Their details (config
   * diagnostics, grants) are not returned: `instances` and `detail.installed` only cover the
   * servers where the caller is admin.
   */
  restricted: { server: string; instances: number }[];
}

/** `POST /store/{source}/{plugin}/update`. */
export interface StoreUpdateRequest {
  /** `detail.version` of the review (required). */
  version: string;
  /** `detail.commit` and `detail.integrity` (required for registry plugins). */
  commit?: string;
  integrity?: string;
  /** Required (`true`) for `unofficial` sources. */
  confirmUnofficial?: boolean;
  /** Overwrite a locally modified copy of an official plugin. */
  force?: boolean;
}

/** An instance once the update is active. */
export interface UpdatedInstance {
  server: string;
  bot: string;
  instance: string;
  health: HealthState | (string & {});
  message?: string;
}

/** Result of `POST /store/{source}/{plugin}/update`. */
export interface StoreUpdateResult {
  version: string;
  mode: "commit" | "pullRequest";
  revision?: string;
  pullRequestUrl?: string;
  message?: string;
  /** Health of every instance (commit mode, once the new revision is active). */
  instances: UpdatedInstance[];
}

/** Result of a plugin install (setup wizard §10 and Store §11). */
export interface InstallResult {
  instance: string;
  health: HealthState | (string & {});
  message?: string;
  /** First slash command of the plugin, to try it. */
  command?: string;
  revision?: string;
  mode: "commit" | "pullRequest";
  pullRequestUrl?: string;
}

/** `POST /registries` request. */
export interface AddRegistryRequest {
  /** `^[a-z0-9][a-z0-9-]{0,31}$`, not `cicord`, unique. */
  name: string;
  /** `https://` only, unique. */
  url: string;
  /**
   * Access token of a private registry: kept in the runtime's secret store, sent to the
   * registry's host only, never returned.
   */
  token?: string;
}

/** Result of a `cicord.yaml` write-back (`POST /registries`, `DELETE /registries/{name}`). */
export interface WriteResult {
  mode: "commit" | "pullRequest";
  revision?: string;
  pullRequestUrl?: string;
}
