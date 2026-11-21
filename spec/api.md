# CODI runtime HTTP API — v1

Served by `codi serve` / `codi dev`. JSON over HTTP, UTF-8. This document is the contract
between the runtime (`internal/api`), the admin UI (`packages/admin`), the portal SDK
(`@codi/client`, `@codi/react`) and the CLI. `@codi/client` (`packages/client/src/types.ts`)
mirrors every shape below.

- Base path: `/api/v1`. Static UIs: `/admin/` (admin UI), `/` (community portal).
- Errors: HTTP status + `{"error": {"code": "<snake_case>", "message": "<human text>"}}`
  (validation errors add `"diagnostics": [Diagnostic]`). Codes:

  | Code | Status |
  |---|---|
  | `unauthenticated` | 401 |
  | `forbidden` | 403, and 421 (dev-mode Host check, below) |
  | `not_found` | 404 (unknown routes under `/api/`, `/auth/`, `/hooks/` included) |
  | `invalid` | 400, 405 (non-GET on a static UI), 413 (body too large), 415 (plan upload content type), 422 (validation, with `diagnostics`) |
  | `conflict` | 409 |
  | `unavailable` | 503 (no active revision yet, no bot connected, write-back failure, timeout) |
  | `internal` | 500 (also a panic in a handler) |

  `Diagnostic = {"severity": "error" | "warning", "file"?, "line"?, "path"? (JSON pointer), "message"}`.
- **Error object** (humanized errors, `internal/reconcile/humanize.go`). The runtime keeps
  errors as text; the API adds an explained form next to the raw field, which is unchanged:

  ```json
  { "code": "community_required",
    "message": "Announcement and stage channels need the Community feature",
    "hint": "Enable Community in Discord (Server Settings → Enable Community), then sync again. …",
    "details": "reconcile ma-commu: 1 change(s) failed: create channel news: discord CreateChannel: … (HTTP 400 50035: Invalid Form Body {…})",
    "discordCode": 50035, "httpStatus": 400,
    "resource": { "action": "create", "kind": "channel", "key": "news" },
    "fields": [ { "path": "type", "code": "BASE_TYPE_CHOICES", "message": "Value must be one of {0, 2}." } ],
    "failures": 3 }
  ```

  `code`, `message` always; `hint`, `details` (the raw text), `discordCode`, `httpStatus`,
  `resource` (the failed plan change), `fields` (Discord's "Invalid Form Body" tree,
  flattened) and `failures` (changes that failed in the same sync, when > 1) are optional.
  Codes: `community_required`, `boost_required`, `missing_permissions`, `missing_access`,
  `role_hierarchy`, `unknown_resource`, `invalid_form_body`, `limit_reached`,
  `invalid_token`, `intents_disallowed`, `rate_limited`, `discord_unavailable`, `timeout`,
  `bot_not_connected`, `no_bot`, `unresolved_reference`, `revision_rejected`,
  `fetch_failed`, `bad_request`, `unknown`; for blocked plan operations also
  `deletion_protection`, `ownership`, `managed_role`, `type_change` (and `role_hierarchy`,
  `no_bot`). Clients must accept unknown codes. It appears as:
  - `error` on a server of `/status` and `/servers/{server}` (Degraded or Missing with a
    `message`), and on a bot of `/status`; `lastErrorInfo` explains `lastError`;
  - `error` on `/history` records whose status is `failed` or `rejected`;
  - `error` on every `blocked[]` entry of a `Plan` (`/plan`, `/sync`);
  - in the error envelope of `plan` / `sync` failures (`503`): `message` is the explained
    message, plus `hint`, `details` (raw text) and `reason` (the error object `code`).
- Time values: RFC 3339 strings. Discord IDs and keys are strings; the IDs of history and
  audit records are integers.
- Optional fields are omitted when empty (marked `?` below); lists are always present
  (`[]`, never `null`).
- Every response carries `X-Content-Type-Options: nosniff` and
  `Referrer-Policy: same-origin` (portal URLs may carry `codi_token`); JSON responses are
  `Cache-Control: no-store`; the admin UI is served with `X-Frame-Options: DENY`.
- CORS: same-origin by default (every response carries `Vary: Origin`). Two lists of
  external portal origins are allowed:
  - operator origins: `--cors-origin` (`CODI_CORS_ORIGINS`) — with
    credentials (`Access-Control-Allow-Credentials: true`), for cookie sessions;
  - repository origins: codi.yaml `spec.portal.allowedOrigins` of the active revision —
    **without** credentials, for token mode only (§1), so that a merged pull request can
    never open cookie sessions to a new site.

  Preflights (`OPTIONS` with `Access-Control-Request-Method`) answer `204`; for an allowed
  origin they allow the methods `GET, POST, PUT, PATCH, DELETE, OPTIONS`, the headers
  `Content-Type`, `Authorization`, `X-Codi-Request` and `Last-Event-ID`, max age 600 s.
- Limits: JSON bodies ≤ 1 MiB (`413 invalid`), `POST /api/v1/plan` uploads ≤ 32 MiB,
  GitHub webhook payloads ≤ 25 MiB; handlers time out after 60 s (`503 unavailable`),
  except the SSE stream.
- **Dev-mode Host check** (`codi dev` only): to defeat DNS rebinding, the runtime only
  answers requests whose `Host` is `localhost`, `*.localhost`, `127.0.0.1`, `::1` or the host
  of `--public-url`; anything else → `421 forbidden` ("unexpected Host header"). `codi serve`
  does not restrict `Host`.

## 1. Authentication

Browser clients use a session cookie `codi_session` (HttpOnly, SameSite=Lax, path `/`,
Secure when the request is TLS or `--public-url` is `https://`), or — for sites embedding
the portal SDK on another origin — a **session token** (token mode, below). Machine clients
(CLI, CI) use `Authorization: Bearer <token>` where the token is the runtime token
(`CODI_RUNTIME_TOKEN`, compared in constant time); a bearer token acts as `admin` on every
server. `Authorization: Bearer codi_st_…` is a session token. Any other `Authorization`
value, or an unknown / expired session token → `401 unauthenticated`, even when a valid
cookie is present. An unknown or expired cookie is simply anonymous.

| Method | Path | Description |
|---|---|---|
| GET | `/auth/login?redirect=<path>[&mode=token]` | Redirects (`302`) to Discord OAuth2 (scope `identify`, `prompt=none`). |
| GET | `/auth/callback` | OAuth2 callback; creates the session, redirects (`302`) to `redirect`. |
| POST | `/auth/link` | Body `{"token": "<signed link token>"}`. Exchanges a token produced by `ctx.portal.link()` (query parameter `codi_token` on the portal URL) for a session. Returns `Me`. |
| POST | `/auth/dev-login` | **Dev mode only.** Body `{"userId": "<id>"}`. Creates a session for that user. Returns `Me`. |
| POST | `/auth/token` | Token mode. Body `{"code": "<one-time code>"}`. Redeems the `codi_code` of a token-mode login for a `TokenSession`. |
| POST | `/auth/logout` | Deletes the session and clears the cookie; with `Authorization: Bearer codi_st_…`, revokes that token instead. `204`. |

- **OAuth2**: `redirect` must be a local path (`/x`, not `//x` nor `/\x`) or an absolute URL
  on an allowed CORS origin; anything else falls back to `/`. The redirect URI sent to
  Discord is `<public URL>/auth/callback` (the public URL is `--public-url`, otherwise
  derived from the request's scheme and `Host`) and must be registered in the Discord
  application. The `state` parameter is random, single-use (10 min, stored in the runtime
  DB) and bound to the browser by the `codi_oauth_state` cookie (HttpOnly, Lax, path
  `/auth/`); a missing or mismatching cookie, or an expired state → `400 invalid`. A
  Discord error or a failed code exchange → `401 unauthenticated`. The OAuth2 client ID is
  `CODI_DISCORD_CLIENT_ID`, by default the application ID of the first bot (by key);
  without a client ID or `CODI_DISCORD_CLIENT_SECRET`, `/auth/login` answers
  `503 unavailable`.
- **Sessions** last 7 days. Session IDs are random (256 bits) and stored in the runtime DB.
  A new login replaces the browser's previous session.
- **Link sessions** (`/auth/link`) last until the token's expiry, at least 1 h and at
  most 24 h. They are **scoped to the token's server**: the caller has no role on any other
  server, and on the token's server it is at most `member` outside the portal endpoints
  (§6), so a forwarded DM link never grants admin UI access. `401 unauthenticated` for an
  invalid or expired token; `400 invalid` for an anonymous link (`ctx.portal.link(path)`
  without a member), which cannot sign anyone in; `404` when the token's server is no
  longer in the repository; `503` before the first revision. Tokens are not single-use.
- **Dev login**: `404` outside dev mode (`codi dev`). Impersonating arbitrary users is only
  acceptable against the simulator, so it also requires **`--sim` or the runtime token**
  (`Authorization: Bearer …`), otherwise `403 forbidden`. With the simulator the user must
  exist in the simulated world (`404` otherwise); without it the profile is looked up through
  a connected bot. `userId` is required (`400`).

### Token mode (embedded portals)
A site on another origin (a fan site with its own design) cannot use the SameSite=Lax
cookie. It authenticates with a bearer **session token** instead; `@codi/client` does it
when created with `auth: "token"`.

- **Allowed origins**: the embedding site's origin must be in codi.yaml
  `spec.portal.allowedOrigins` (token mode only) or `--cors-origin`.
- **OAuth**: `/auth/login?mode=token&redirect=<URL>` where `redirect` is a local path or an
  absolute URL on an allowed origin (otherwise `400 invalid`: unlike cookie mode, there is
  no silent fallback to `/`). After Discord, `/auth/callback` sets no session cookie and
  redirects to `redirect` with `codi_code=<code>` added to its query. The code is single-use,
  valid 60 s, and bound to the redirect's origin: `POST /auth/token {"code"}` must come from
  that origin (`Origin` header; a local redirect binds it to the runtime's own origin),
  otherwise `403 forbidden` (the code is burnt). Unknown or expired code → `401`.
- **Portal links** and **dev login**: `POST /auth/link {"token", "mode": "token"}` and
  `POST /auth/dev-login {"userId", "mode": "token"}` return a `TokenSession` instead of
  setting a cookie. `mode` is `"cookie"` (default) or `"token"` (anything else → `400`).
- `TokenSession = {"token": "codi_st_…", "expiresAt": "<RFC 3339>", "me": Me}`. Tokens
  last 24 h (link tokens: as long as the link session, at most 24 h) and are stored by the
  runtime like sessions (revoked by `/auth/logout`).
- **Scope**: on the portal endpoints (§6, plugin routes included) a token session has the
  user's full role on the server; everywhere else it is **at most `member`** (like link
  sessions), so a token kept by a third-party site never reaches the admin endpoints. A
  token is only accepted in the `Authorization` header, never as the `codi_session`
  cookie; a cookie session ID is never accepted as a bearer token.
- Requests carrying `Authorization` need no `X-Codi-Request` header (CSRF, below), but the
  `Origin` check still applies: unsafe requests must come from an allowed origin.

### CSRF
Every request with an unsafe method (anything but `GET`, `HEAD`, `OPTIONS`), the `/auth/*`
endpoints included, must carry the header **`X-Codi-Request: 1`** (any non-empty value)
unless it has an `Authorization` header (browsers never add one on their own). `@codi/client`
always sends it. Otherwise:
`403 {"error": {"code": "forbidden", "message": "missing X-Codi-Request header (CSRF protection)"}}`.

In addition, an unsafe request carrying an `Origin` header must come from the runtime's
own origin or an allowed CORS origin, otherwise `403 forbidden`. The runtime's own origin
is the origin of `--public-url` when it is set (the `Host` header is then never trusted),
otherwise `Origin` must match the request's `Host`. A cross-site form cannot set custom
headers, and cross-origin scripts need a CORS preflight, which only `--cors-origin` origins
pass. `/hooks/*` is exempt (HMAC-authenticated).

### Roles
For each server, a user is `admin` (server owner, or `spec.access.admins`), `moderator`
(`spec.access.moderators`), `member` (member of the Discord server) or nothing.
Bearer-token clients are `admin` everywhere.

Resolution: the user's live member roles are fetched from Discord (through the server's
reconciling bot, cached 30 s; no connected bot → `503`) and matched with
`spec.access.<admins|moderators>.roles` (keys resolved through the resource map) and
`.users` (Discord user IDs). Admin wins over moderator. Not in the server → no role. Link
sessions are restricted as described above.

Endpoints that need a role on a server answer `503` before the first revision, `401`
without a session, `404` for an unknown server key and `403` when the role is insufficient
(`"requires the <role> role on server <key>"`).

## 2. Me

`GET /api/v1/me` → `200 Me` or `401`. `servers` lists every server where the caller has a
role (servers whose role cannot be resolved are skipped).

```json
{
  "user": { "id": "1", "username": "alice", "displayName": "Alice", "avatarUrl": "" },
  "servers": [ { "key": "ma-commu", "name": "Ma Commu", "role": "admin" } ]
}
```

`User = {"id", "username", "displayName", "avatarUrl", "bot"?}` (`bot: true` for bot users).

## 3. Runtime status (admin, moderator)

`GET /api/v1/status` → the visible servers only (where the caller is admin or moderator),
the bots on them and their plugin instances on those servers, with the live drift of each
server; `403` if there is none. Before the first revision, `revision` is `null`, the lists
are empty and the status is returned to any authenticated caller.

```json
{
  "version": "0.1.0",
  "mode": "dev",                         // "serve" | "dev"
  "simulator": true,                     // the Discord simulator is in use
  "revision": { "id": "4f0c2d9", "sha": "4f0c2d9…", "message": "…", "author": "…", "time": "…", "source": "git" },
  "lastError": "…",                      // optional: latest rejected revision / failed fetch
  "servers": [ {
    "key": "ma-commu", "name": "Ma Commu", "discordId": "1122…",
    "sync": "Synced",                    // Synced | OutOfSync | Unknown
    "health": "Healthy",                 // Healthy | Progressing | Degraded | Missing
    "message": "",
    "lastSync": "2026-11-21T10:00:00Z",  // optional: absent until the first reconcile
    "bot": "moderator",                  // optional: the reconciling bot
    "drift": [ { "kind": "channel", "key": "general", "field": "name", "expected": "général", "actual": "general" } ]
  } ],
  "bots": [ {
    "key": "moderator", "username": "Gardien", "userId": "…", "health": "Healthy", "message": "",
    "servers": ["ma-commu"],
    "plugins": [ { "instance": "welcome", "plugin": "welcome", "version": "0.1.0", "server": "ma-commu", "health": "Healthy", "message": "" } ]
  } ]
}
```

- `revision.id` is the short identifier (7-char SHA, or a content hash for local sources),
  `sha` the full SHA / hash, `source` is `git` or `local`.
- Drift `field` `"exists"` = the resource is missing in Discord (or not created yet),
  `"order"` = a reorder is pending, `"managed"` = a mapping is about to be dropped.
- A bot whose session is not connected is `Missing` with the reason in `message`; it is
  `Degraded` when one of its plugin instances is.

`GET /api/v1/history?limit=50&target=<target>` → `{"items": [SyncRecord]}` newest first:

```json
{ "id": 42, "time": "…", "revision": "4f0c2d9", "target": "server:ma-commu",
  "status": "succeeded", "message": "3 change(s) applied", "details": { "plan": {}, "applied": 3, "failed": 0 } }
```

- `target` is `revision` (activation of a revision), `server:<key>` (a sync or self-heal) or
  `bot:<key>`; `status` is `succeeded | failed | rejected` (`rejected` = an invalid revision);
  `details` is optional.
- Visibility: `server:` records need a visible server, `bot:` records a visible server of
  that bot, and project-level records (`revision`) are only returned to callers who see
  every server. `403` when the caller sees no server, `503` before the first revision.
  `limit` defaults to 50, at most 500 (the filter applies after the limit).

## 4. Servers

| Method | Path | Role | Description |
|---|---|---|---|
| GET | `/api/v1/servers/{server}` | moderator | `ServerDetail`: the server's status entry (§3, with live drift) + `spec` (the Server YAML spec as JSON). |
| GET | `/api/v1/servers/{server}/resources` | moderator | Resources for pickers (below). |
| GET | `/api/v1/servers/{server}/plan` | admin | `Plan` of the active revision vs. live Discord. |
| POST | `/api/v1/servers/{server}/sync` | admin | Reconcile now. Returns `{"plan": Plan, "applied": n}`. |
| GET | `/api/v1/servers/{server}/audit?before=<id>&limit=50` | moderator | `{"items": [AuditEntry]}` newest first; `before` is the id cursor of the next page; `limit` ≤ 500. |

`plan` / `sync` failures (no bot, Discord or reconcile error) → `503 unavailable`. Every
sync is recorded in the audit log (`sync.apply`, details `{changes}`).

Resources: `@everyone` is omitted, roles are sorted from the top of the hierarchy, channels
by position; `color` is `"#rrggbb"` (`""` = none); channel `type` uses the Server YAML names
(`text`, `voice`, `announcement`, `stage`, `forum`, `media`, `thread`); `category` is the
key of the channel's managed category and is omitted when there is none. `managed: true` =
declared in YAML (has a key); unmanaged resources have `key: ""`.

```json
// resources
{
  "roles":      [ { "key": "modo", "id": "…", "name": "Modo", "color": "#e67e22", "managed": true } ],
  "categories": [ { "key": "community", "id": "…", "name": "Communauté", "managed": true } ],
  "channels":   [ { "key": "general", "id": "…", "name": "général", "type": "text", "category": "community", "managed": true } ]
}
```

**Plan** — changes are in apply order; blocked operations are not applied (spec/README.md §3):

```json
{ "server": "ma-commu",
  "changes": [
    { "action": "create", "kind": "channel", "key": "general", "summary": "create text channel #général",
      "fields": { "name": { "from": null, "to": "général" } } },
    { "action": "update", "kind": "role", "key": "modo", "id": "…", "summary": "update role Modo",
      "fields": { "color": { "from": "#000000", "to": "#e67e22" } } }
  ],
  "blocked": [
    { "action": "delete", "kind": "role", "key": "old", "id": "…", "reason": "deletionProtection" },
    { "action": "update", "kind": "channel", "key": "news", "id": "…", "reason": "type change: …",
      "fields": { "type": { "from": "text", "to": "forum" } } }
  ] }
```

- `action`: `create | update | delete | adopt | reorder | unmanage` (`adopt` = an existing
  resource with the same name becomes managed; `unmanage` = a removed key is dropped from
  the resource map without touching Discord).
- `kind`: `server | role | category | channel | everyone`.
- `id` (optional): the Discord ID of an existing resource. `fields` (optional): `from` → `to`
  of each field (strings, numbers, booleans, permission name lists, overwrite maps, key
  lists).
- `reorder` keys: `hierarchy` (roles), `categories`, a category key (its channels) or
  `(none)` (channels without a category).
- `blocked[]` = `{action, kind, key, id?, reason, fields?}`; `fields` are the differences
  that could not be applied, when known.

```json
// AuditEntry
{ "id": 12, "time": "…", "server": "ma-commu", "actor": "discord:1", "action": "config.update", "target": "moderator/welcome", "details": {} }
// actor: "discord:<user id>" or "runtime-token"; details is always an object.
```

## 5. Plugin instances & configuration

`GET /api/v1/servers/{server}/plugins` (moderator) →

```json
{ "items": [ {
  "bot": "moderator", "instance": "welcome", "server": "ma-commu",
  "plugin": { "name": "welcome", "version": "0.1.0", "displayName": "Welcome", "description": "…",
              "capabilities": ["discord.messages.send"], "intents": ["ServerMembers"] },
  "config": { "channel": "general", "message": "…" },     // effective config (defaults applied)
  "schema": { /* the plugin's config.schema.json as written */ },
  "baseConfig": { "channel": "general" },                   // spec.plugins[].config as written
  "override": { "message": "…" },                           // overrides.<server>.config, or null
  "editable": ["message"],                                  // fields this user may edit (x-codi-access)
  "commands": [ { "name": "welcome-preview", "description": "…" } ],
  "routes": ["GET /steps", "POST /complete"],                // portal routes (§6)
  "health": "Healthy", "message": ""
} ] }
```

- `schema` is the schema file's JSON with its key order preserved (`{"type": "object"}` when
  the plugin has none). `baseConfig` and `override` are written values without defaults.
- `editable`: admins may edit every top-level property; moderators only those marked
  `x-codi-access: moderator` (default `admin`). Sorted.

`PUT /api/v1/servers/{server}/plugins/{bot}/{instance}/config` (moderator, field-restricted)

Request: `{"config": {...}, "scope": "server" | "base", "revision": "<active revision id>"}`
- `config` must be a JSON object (`400`); `scope` must be `server` or `base` (`400`); an
  unknown bot or instance on that server → `404`.
- `scope: "server"` (default) writes the server override (`overrides.<server>.config` in
  `bot.yaml`) as the minimal merge patch from the base config; `"base"` writes `config`.
- `config` is the full desired config **with the fields the user sees** (defaults
  included). For `scope: "server"` the runtime diffs it against the base config with
  defaults applied and writes only the difference (`null` = remove a base field; deletions
  of fields absent from the base are dropped); an empty difference removes the override.
  For `"base"`, fields that only restate a schema default and were not written before are
  dropped. Comments and key order are kept.
- Authorization is checked on the top-level fields whose effective value changes:
  moderators may only change fields listed in `editable`, any other change → `403`
  (`"you may not change: a, b"`). `scope: "base"` changes every server the instance runs
  on: the caller must be at least moderator on each of them where the effective config
  changes, and the field restriction applies per server.
- The config is validated (schema + `x-codi-ref`, the same rules as `codi validate`) on
  every affected server before anything is written → `422` with
  `{"error": {"code": "invalid", ..., "diagnostics": [Diagnostic]}}`.
- `revision` (optional) must be the active revision ID, otherwise `409 conflict`
  (reload and retry). A write-back conflict (the branch moved and the patch no longer
  applies) is also `409`.
- An unchanged config writes nothing and returns `{"mode": "commit", "revision": <active>}`.
- `mode` comes from `codi.yaml` `writeBack.policies` for the bot file.
  `writeBack.enabled: false` → `403`; no writer configured → `503`; a failed write-back →
  `503`. Write-backs are serialized.
- In `commit` mode the runtime is triggered at once and the response waits (≤ 20 s) for
  the new revision to be active; `revision` is then the active revision ID. When the new
  revision does not become active in time (or is rejected), `revision` is the ID reported
  by the writer and the new revision may still be pending or rejected (see `/status`
  `lastError`).
- Every write is audited (`config.update`, target `<bot>/<instance>`, details
  `{scope, fields, mode, revision, pullRequestUrl}`). Commits carry the trailers
  `Codi-Actor: discord:<id> (<name>)` and `Codi-Source: admin-ui`.

Response `200`:
```json
{ "mode": "commit", "revision": "9a1b2c3", "pullRequestUrl": "" }
// mode "pullRequest": revision is "" and pullRequestUrl is set; nothing is applied yet.
```

## 6. Community portal

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/portal/servers` | none | `{"items": [PortalServer]}` — servers with a portal. |
| GET | `/api/v1/portal/{server}` | none | `PortalServer` (`404` when the server has no portal). |
| GET | `/api/v1/portal/{server}/logo` | none | The `portal.theme.logo` image when it is a repository file (path relative to the server YAML; must stay inside the repository, be reached without symbolic links, be at most 10 MiB and be a png, jpg, gif, webp, avif or svg); a URL logo redirects (`302`). Headers: `Cache-Control: public, max-age=300`, `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox` (a repository SVG never runs script), `Cross-Origin-Resource-Policy: cross-origin` (embedding sites may display it). Unset or invalid → `404`. |
| GET | `/api/v1/portal/{server}/banner` | none | The `portal.banner` image, same rules as the logo. |
| GET | `/api/v1/portal/{server}/me` | member | `{"user": User, "member": {"nick", "joinedAt", "roles": [{"key","id","name","color"}]}, "role": "member" \| "moderator" \| "admin"}` |
| ANY | `/api/v1/portal/{server}/plugins/{bot}/{instance}/{path…}` | per route | Plugin routes (`definePlugin({routes})`). The route's `access` is enforced; the JSON body/response are passed through. Unknown route → `404`. |

All portal endpoints answer `503` before the first revision.

"Servers with a portal" are servers that declare `spec.portal` or run a plugin with routes.
`theme` always has the four fields (`""` = unset): `theme.logo` is the logo endpoint above
(with `?v=<revision id>`, so caches follow new commits) or an absolute URL, `mode` is
`light | dark | system | ""`. The no-code fields of `spec.portal` (server.schema.json) are
always present: `title` (default: the server name), `tagline`, `description` (Markdown
subset source, rendered by the portal — never HTML), `banner` (like `theme.logo`), `links`
(`[{label, url, icon}]`, `url` is http(s) by schema, `icon` defaults to `"link"`), `pages`
(`{onboarding, profile, leaderboard, tickets}`, unset = `true`) and `locale`
(`"en" | "fr" | ""`). `plugins` lists the instances on that server that declare routes. Member roles have `key: ""` when they are not declared in
YAML. `/portal/{server}/me` → `401` anonymous, `403` when the caller is not a member.

Plugin routes:
- `access` (default `member`): `public` (anyone; the viewer is still filled when logged in),
  `member`, `moderator`, `admin` with the roles of §1 (portal endpoints count the full role
  of link sessions on their server). Anonymous → `401`, insufficient role → `403`. The
  bearer token passes every check (no viewer).
- The handler receives `request.viewer = {user, member}` (member fetched from Discord;
  after a route with an unsafe method the viewer's cached member is dropped, so the next
  `/portal/{server}/me` shows roles the plugin just changed),
  the query parameters (first value of each) and the JSON body (≤ 1 MiB; non-JSON →
  `400`; an empty body is no body).
- Response: the handler's status (default `200`) and its JSON body as
  `application/json; charset=utf-8` with `Cache-Control: no-store`, plus its headers except
  `Set-Cookie`, `Content-Type`, `Content-Length`, `Transfer-Encoding`, `Connection`,
  `Location`, `Content-Security-Policy`, `Strict-Transport-Security` and
  `Access-Control-*`. No body → `204` (or the handler's status when it is not 200). A thrown
  error → `500 internal` ("the plugin route failed").

```json
// PortalServer
{ "key": "ma-commu", "name": "Ma Commu", "iconUrl": "",
  "theme": { "primary": "#5865f2", "accent": "#eb459e", "logo": "/api/v1/portal/ma-commu/logo?v=3f2a9c1", "mode": "dark" },
  "title": "Bienvenue sur Ma Commu", "tagline": "La commu francophone des soirées jeux…",
  "description": "Ici, on joue **ensemble**…", "banner": "/api/v1/portal/ma-commu/banner?v=3f2a9c1",
  "links": [ { "label": "Site web", "url": "https://ma-commu.example", "icon": "website" } ],
  "pages": { "onboarding": true, "profile": true, "leaderboard": true, "tickets": true },
  "locale": "fr",
  "plugins": [ { "bot": "moderator", "instance": "onboarding", "plugin": "onboarding", "routes": ["GET /steps", "POST /complete"] } ] }
```

## 7. Machine endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/api/v1/sync` | bearer | Fetch the source now and sync (used by CI / webhooks without GitHub). `202 {"status": "accepted"}`. |
| POST | `/api/v1/plan` | bearer | Body: a `.tar.gz` of a repository. Validates it and returns `{"valid": bool, "diagnostics": [Diagnostic], "plans": [Plan]}` against live Discord, without applying (`codi plan --remote`). |
| POST | `/hooks/github` | HMAC | GitHub webhook (`X-Hub-Signature-256` verified with `CODI_WEBHOOK_SECRET`). `push` on the tracked branch triggers a sync. |
| GET | `/healthz` | none | `200 ok` (text) when the process is up. |
| GET | `/readyz` | none | `200 ready` once a revision is active, `503 no active revision` otherwise (text). |

- Without `CODI_RUNTIME_TOKEN` the bearer endpoints answer `401` ("machine endpoints are
  disabled"); a missing or wrong token → `401`.
- Plan archive: `Content-Type` `application/gzip`, `application/x-gzip`,
  `application/x-tar+gzip`, `application/octet-stream` or none, otherwise
  `415 invalid`. `codi.yaml` at the root or inside a single top-level directory. Only regular
  files and directories; absolute paths, `..`, backslashes, symlinks, hard links and special
  files → `400 invalid`. At most 5000 files, 16 MiB per file and 64 MiB extracted; a body over
  32 MiB → `413`. An invalid repository is `200` with `valid: false`, its diagnostics and
  `plans: []`; planning errors (Discord unreachable…) → `503`.
- Webhook: no secret configured or a bad signature → `401`; `ping` → `200 {"status": "pong"}`;
  `push` on `refs/heads/<tracked branch>` → `202 {"status": "accepted"}`; other branches and
  events → `200 {"status": "ignored", "reason": "…"}`; an unparsable push payload → `400`.
  The tracked branch is `codi.yaml` `spec.source.branch`, default `main`.

## 8. Simulator (`codi dev --sim` only)

Lets the admin UI's **Simulator** panel and tests play the role of Discord users. These
endpoints need no authentication (dev only, behind the dev-mode Host check) but follow
the CSRF rule of §1. Without the simulator they answer `404` ("the simulator is not
enabled"). `codi dev --sim` seeds the world from the repository: every server (its
`discordId`, named after `profile.name` or its key) owned by a user `owner`, every bot (its
`applicationId`, named after `profile.username` or its key) invited with `Administrator`,
and the users `alice`, `bob` and `charlie`, who are not members.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/sim/state` | `{"users": [User], "bots": [User], "servers": [SimServer]}` |
| POST | `/api/v1/sim/users` | `{"username"}` (1–32 characters) → `201 User` |
| POST | `/api/v1/sim/servers/{id}/members` | `{"userId"}` — the user joins the server (`memberJoin`). `204`. |
| DELETE | `/api/v1/sim/servers/{id}/members/{userId}` | The user leaves. `204`. |
| GET | `/api/v1/sim/channels/{id}/messages?limit=50` | `{"items": [Message]}` the last `limit` (≤ 500) messages, oldest first. |
| GET | `/api/v1/sim/users/{id}/dms?limit=50` | `{"items": [Message]}` DMs received by the user from bots, oldest first. |
| POST | `/api/v1/sim/messages` | `{"userId","channelId","content"}` → `201 Message` |
| POST | `/api/v1/sim/commands` | `{"userId","serverId","channelId","bot","name","options":{}}` → `InteractionResult` |
| POST | `/api/v1/sim/components` | `{"userId","channelId"?,"messageId","customId","values":[]}` → `InteractionResult` (a select when `customId` is a select menu of the message, a button click otherwise). |
| POST | `/api/v1/sim/modals` | `{"userId","serverId","channelId","bot","customId","fields":{"<input id>":"value"}}` → `InteractionResult` (submits a modal shown by a previous interaction). |
| GET | `/api/v1/sim/commands?serverId=` | `{"items": [{"bot","name","description","options":[...]}]}` registered commands, of every simulated server by default. |
| GET | `/api/v1/sim/events` | Server-Sent Events: `event: change` whenever the simulated world changes (UI refresh hint). |

- `bot` is a bot key (an application ID or a bot user ID is accepted too); unknown → `404`.
  In command listings `bot` is the bot key, or the application ID for bots outside the
  repository.
- `SimServer = {"id","key"?,"name","ownerId","channels":[{"id","name","type","parentId"?,"position"}],"roles":[{"id","name","color","position","managed"}],"members":[{"user","nick","roles":["<role id>"]}]}`.
  Only repository servers present in the world are listed; `key` is the repository key;
  `roles` include `@everyone` (its ID is the server ID); `managed` marks integration roles.
- Command options: `{"name","description","type","required","choices"?:[{"name","value"}],"minValue"?,"maxValue"?}`.
  `type` is one of `string | integer | boolean | user | channel | role | mentionable | number | attachment`.
- Simulated Discord errors map to `404 not_found`, `403 forbidden` and `400 invalid`
  (unknown user or server, missing permission…); an unknown channel / user in the message
  and DM listings → `404`; an unknown `messageId` (or one not in `channelId`) → `404`.
- Validation errors of an interaction (unknown command, missing option, disabled
  button…) come back as `200` with `status: "failed"` and `error` set; so does a bot that
  never acknowledges (`"the application did not respond"`). `status: "modal"` carries
  `modal: {"customId","title","components"}` (rows of `textInput`). `deferred` means the bot
  deferred (or had not answered yet) and had not followed up after 5 s; the UI refreshes on
  SSE events.
- SSE: `Content-Type: text/event-stream`, events `id: <n>`, `event: change`, `data: {}`. An
  initial `change` event is sent, then bursts are coalesced (≤ 1 event / 100 ms), with a
  `: ping` comment every 25 s. The stream ends when the runtime shuts down.

```json
// Message
{ "id": "…", "channelId": "…", "serverId": "…", "author": User, "content": "…", "embeds": [...],
  "components": [ { "type": "row", "components": [ { "type": "button", "customId": "c:welcome:go", "label": "Go", "style": "primary" } ] } ],
  "ephemeral": false, "createdAt": "…", "editedAt": "…" }
// serverId is omitted for DMs, editedAt until the message is edited.
// Components omit empty fields. Types: row | button | select | textInput.
// button: {"type":"button","customId"?,"label"?,"style","emoji"?,"url"? (link buttons),"disabled"?}
//   style: primary | secondary | success | danger | link
// select: {"type":"select","customId","placeholder"?,"minValues"?,"maxValues"?,"disabled"?,"options":[{"label","value","description"?,"emoji"?,"default"?}]}
// textInput (modals): {"type":"textInput","customId","label","style":"short"|"paragraph","required"?,"value"?,"placeholder"?}
// embeds: [{"title"?,"description"?,"url"?,"color"?:"#rrggbb","timestamp"?,"fields"?:[{"name","value","inline"}],
//           "author"?:{"name","url","iconUrl"},"footer"?:{"text","iconUrl"},"imageUrl"?,"thumbnailUrl"?}]
// InteractionResult — waits up to 5 s for the bot's answer
{ "status": "replied",            // replied | deferred | failed | modal
  "response": Message | null, "followups": [Message], "modal"?: {...}, "error": "" }
```

## 9. Static UIs

- `/admin` redirects (`301`) to `/admin/`; `/admin/*` serves the admin UI and `/*` the
  portal, both with an SPA fallback to `index.html` (`Cache-Control: no-cache`). Hashed files
  under `assets/` are `Cache-Control: public, max-age=31536000, immutable`, other files
  `no-cache` (revalidated with `Last-Modified`); a missing file with an extension (other than
  `.html`) is a plain `404`. Content types come from the file extension.
- **Custom portal**: when codi.yaml `spec.portal.path` of the **active revision** points to a
  directory of that revision containing `index.html` (a committed build, docs/portal.md), it
  is served at `/` instead of the embedded default portal, with the same rules and an
  `X-Codi-Portal: repository` header. The directory is resolved on every request, so a new
  revision swaps the files without a restart; when it is missing (not built yet) the
  default portal is served. Files are opened through an `os.Root` confined to that
  directory: `..` and symbolic links leading outside it are refused, and a portal path
  reached through a symbolic link is ignored (`codi validate` reports it).
- `/api/*`, `/auth/*`, `/hooks/*`, `/admin/*`, `/healthz` and `/readyz` are never shadowed
  by the portal: unknown paths there are JSON `404 not_found`.
- Methods other than `GET` / `HEAD` on a static UI → `405 invalid`. A UI that was not built
  into the binary answers `404`.
