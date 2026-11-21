# CODI runtime HTTP API — v1

Served by `codi serve` / `codi dev`. JSON over HTTP, UTF-8. This document is the contract
between the runtime (`internal/api`), the admin UI (`packages/admin`), the portal SDK
(`@codi/client`, `@codi/react`) and the CLI.

- Base path: `/api/v1`. Static UIs: `/admin/` (admin UI), `/` (community portal).
- Errors: HTTP status + `{"error": {"code": "<snake_case>", "message": "<human text>"}}`.
  Codes: `unauthenticated` (401), `forbidden` (403), `not_found` (404), `invalid` (400/422),
  `conflict` (409), `unavailable` (503), `internal` (500).
- Time values: RFC 3339 strings. IDs: strings.
- CORS: same-origin by default; `--cors-origin` allows an external portal origin
  (credentials allowed). Preflight allows the headers `Content-Type`, `Authorization`,
  `X-Codi-Request` and `Last-Event-ID`.
- Limits: JSON bodies ≤ 1 MiB (`413`), `POST /api/v1/plan` uploads ≤ 32 MiB; handlers time
  out after 60 s (`503 unavailable`), except the SSE stream.

## 1. Authentication

Browser clients use a session cookie `codi_session` (HttpOnly, SameSite=Lax, Secure when
served over https). Machine clients (CLI, CI) use `Authorization: Bearer <token>` where the
token is the runtime token (`CODI_RUNTIME_TOKEN`); a bearer token acts as a project admin.

| Method | Path | Description |
|---|---|---|
| GET | `/auth/login?redirect=<path>` | Redirects to Discord OAuth2 (scope `identify`). |
| GET | `/auth/callback` | OAuth2 callback; creates the session, redirects to `redirect`. |
| POST | `/auth/link` | Body `{"token": "<signed link token>"}`. Exchanges a token produced by `ctx.portal.link()` (query parameter `codi_token` on the portal URL) for a session. Returns `Me`. |
| POST | `/auth/dev-login` | **Dev mode only** (`codi dev`). Body `{"userId": "<id>"}`. Returns `Me`. |
| POST | `/auth/logout` | Deletes the session. `204`. |

- **OAuth2**: `redirect` must be a local path (`/x`, not `//x`) or a URL on an allowed CORS
  origin; anything else falls back to `/`. The `state` parameter is random, single-use
  (10 min) and bound to the browser by the `codi_oauth_state` cookie (HttpOnly, Lax,
  path `/auth/`); a mismatch → `400 invalid`. The OAuth2 client ID is
  `CODI_DISCORD_CLIENT_ID`, by default the application ID of the first bot (by key); without
  `CODI_DISCORD_CLIENT_SECRET`, `/auth/login` answers `503 unavailable`.
- **Sessions** last 7 days. Session IDs are random (256 bits) and stored in the runtime DB.
- **Link sessions** (`/auth/link`) last until the token's expiry, at least 1 h and at
  most 24 h. They are **limited to the token's server**, and to the `member` role outside
  the portal endpoints (§6), so a forwarded DM link never grants admin UI access.
  `401 unauthenticated` for an invalid/expired token; `400 invalid` for an anonymous link
  (`ctx.portal.link(path)` without a member), which cannot sign anyone in. Tokens are not
  single-use.
- `/auth/dev-login` → `404` outside dev mode; with the simulator the user must exist.

### CSRF
Every request with an unsafe method (`POST`, `PUT`, `PATCH`, `DELETE`), the `/auth/*`
endpoints included, must carry the header **`X-Codi-Request: 1`** unless it has an
`Authorization` header. `@codi/client` always sends it. Otherwise:
`403 {"error": {"code": "forbidden", "message": "missing X-Codi-Request header (CSRF protection)"}}`.
A cross-site form cannot set custom headers, and cross-origin scripts need a CORS
preflight, which only `--cors-origin` origins pass. In addition, an unsafe request whose
`Origin` is neither the runtime's own origin nor an allowed CORS origin → `403`.
`/hooks/github` is exempt (it is HMAC-authenticated).

### Roles
For each server, a user is `admin` (server owner, or `spec.access.admins`), `moderator`
(`spec.access.moderators`), `member` (member of the Discord server) or nothing.
Bearer-token clients are `admin` everywhere.

Resolution: the user's live member roles are fetched from Discord (through the server's
reconciling bot, cached ~30 s) and matched with `spec.access.<admins|moderators>.roles`
(keys resolved through the resource map) and `.users` (Discord user IDs). Admin wins over
moderator. Not in the server → no role.

## 2. Me

`GET /api/v1/me` → `200 Me` or `401`.

```json
{
  "user": { "id": "1", "username": "alice", "displayName": "Alice", "avatarUrl": "" },
  "servers": [ { "key": "ma-commu", "name": "Ma Commu", "role": "admin" } ]
}
```

## 3. Runtime status (admin, moderator)

`GET /api/v1/status` → visible servers only (where the caller is admin or moderator), the
bots on them and their plugin instances on those servers; `403` if there is none. Before the
first revision, `revision` is `null` and the lists are empty. Optional extra fields:
`lastError` (latest rejected revision / failed fetch) and `servers[].bot` (reconciling bot).

```json
{
  "version": "0.1.0",
  "mode": "dev",                         // "serve" | "dev"
  "simulator": true,                     // the Discord simulator is in use
  "revision": { "id": "4f0c2d9", "message": "…", "author": "…", "time": "…", "source": "git" },
  "servers": [ {
    "key": "ma-commu", "name": "Ma Commu", "discordId": "1122…",
    "sync": "Synced",                    // Synced | OutOfSync | Unknown
    "health": "Healthy",                 // Healthy | Progressing | Degraded | Missing
    "message": "",
    "lastSync": "2026-11-21T10:00:00Z",
    "drift": [ { "kind": "channel", "key": "general", "field": "name", "expected": "général", "actual": "general" } ]
  } ],
  "bots": [ {
    "key": "moderator", "username": "Gardien", "userId": "…", "health": "Healthy", "message": "",
    "servers": ["ma-commu"],
    "plugins": [ { "instance": "welcome", "plugin": "welcome", "version": "0.1.0", "server": "ma-commu", "health": "Healthy", "message": "" } ]
  } ]
}
```

`GET /api/v1/history?limit=50&target=<target>` → `{"items": [SyncRecord]}` newest first, where
`SyncRecord = {id, time, revision, target, status, message, details?}`; `target` is
`revision`, `server:<key>` or `bot:<key>`; `status` is `succeeded | failed | rejected`.
Entries for servers the caller cannot see are omitted. `limit` ≤ 500.

## 4. Servers

| Method | Path | Role | Description |
|---|---|---|---|
| GET | `/api/v1/servers/{server}` | moderator | `ServerDetail`: the status entry above + `spec` (the Server YAML spec as JSON). |
| GET | `/api/v1/servers/{server}/resources` | moderator | Resources for pickers (below). |
| GET | `/api/v1/servers/{server}/plan` | admin | `Plan` of the active revision vs. live Discord. |
| POST | `/api/v1/servers/{server}/sync` | admin | Reconcile now. Returns `{"plan": Plan, "applied": n}`. |
| GET | `/api/v1/servers/{server}/audit?before=<id>&limit=50` | moderator | `{"items": [AuditEntry]}` newest first. |

`sync` failures (no bot, reconcile error) → `503 unavailable`; a sync is recorded in the
audit log (`sync.apply`). Resources: `@everyone` is omitted, roles are sorted from the top
of the hierarchy, `color` is `"#rrggbb"` (`""` = none), channel `type` uses the Server
YAML names and `category` is the category key (`""` when unmanaged/none).

```json
// resources
{
  "roles":      [ { "key": "modo", "id": "…", "name": "Modo", "color": "#e67e22", "managed": true } ],
  "categories": [ { "key": "community", "id": "…", "name": "Communauté", "managed": true } ],
  "channels":   [ { "key": "general", "id": "…", "name": "général", "type": "text", "category": "community", "managed": true } ]
}
// managed: true = declared in YAML (has a key); unmanaged resources have key "".

// Plan
{ "server": "ma-commu", "changes": [
  { "action": "create", "kind": "channel", "key": "general", "summary": "create text channel #général",
    "fields": { "name": { "from": null, "to": "général" } } }
  // action: create | update | delete | adopt | reorder
], "blocked": [ { "action": "delete", "kind": "role", "key": "old", "reason": "deletionProtection" } ] }

// AuditEntry
{ "id": 12, "time": "…", "server": "ma-commu", "actor": "discord:1", "action": "config.update", "target": "moderator/welcome", "details": {} }
```

## 5. Plugin instances & configuration

`GET /api/v1/servers/{server}/plugins` (moderator) →

```json
{ "items": [ {
  "bot": "moderator", "instance": "welcome", "server": "ma-commu",
  "plugin": { "name": "welcome", "version": "0.1.0", "displayName": "Welcome", "description": "…",
              "capabilities": ["discord.messages.send"], "intents": ["ServerMembers"] },
  "config": { "channel": "general", "message": "…" },     // effective config (defaults applied)
  "schema": { /* the plugin's config.schema.json */ },
  "editable": ["message"],                                  // fields this user may edit (x-codi-access)
  "commands": [ { "name": "welcome-preview", "description": "…" } ],
  "routes": ["GET /steps", "POST /complete"],                // portal routes (§6)
  "health": "Healthy", "message": ""
} ] }
```

`PUT /api/v1/servers/{server}/plugins/{bot}/{instance}/config` (moderator, field-restricted)

Request: `{"config": {...}, "scope": "server" | "base", "revision": "<active revision id>"}`
- `scope: "server"` (default) writes the server override (`overrides.<server>.config` in
  `bot.yaml`) as the minimal merge patch from the base config; `"base"` writes `config`.
- Moderators may only change fields listed in `editable`; any other change → `403`.
- The config is validated (schema + `x-codi-ref`) before anything is written → `422` with
  `{"error": {..., "diagnostics": [Diagnostic]}}`.
- `revision` (optional) must be the active revision ID, otherwise `409 conflict`
  (reload and retry). A write-back conflict (the branch moved and the patch no longer
  applies) is also `409`.
- `config` is the full desired config **with the fields the user sees** (defaults
  included). For `scope: "server"` the runtime diffs it against the base config with
  defaults applied and writes only the difference (`null` = remove a base field); an
  empty difference removes the override. For `"base"`, fields that only restate a schema
  default and were not written before are dropped. Comments and key order are kept.
- `scope: "base"` changes every server the instance runs on: the caller must be at least
  moderator on each, and the field restriction applies per server.
- Moderators are checked on the top-level fields whose effective value changes.
- An unchanged config writes nothing and returns the active revision.
- `mode` comes from `codi.yaml` `writeBack.policies` for the bot file. In `commit` mode
  the runtime is triggered at once and the response waits (≤ 20 s) for the new revision to
  be active; `revision` is then the active revision ID (empty if the activation failed or
  is still pending). `writeBack.enabled: false` → `403`; no writer configured → `503`.
- Every write is audited (`config.update`, target `<bot>/<instance>`, details
  `{scope, fields, mode, revision, pullRequestUrl}`). Commits carry the trailers
  `Codi-Actor: discord:<id> (<name>)` and `Codi-Source: admin-ui`.

Response `200`:
```json
{ "mode": "commit", "revision": "9a1b2c3", "pullRequestUrl": "" }
// mode "pullRequest": revision is empty and pullRequestUrl is set; nothing is applied yet.
```

## 6. Community portal

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/portal/servers` | none | `{"items": [PortalServer]}` — servers with a portal. |
| GET | `/api/v1/portal/{server}` | none | `PortalServer`. |
| GET | `/api/v1/portal/{server}/logo` | none | The `portal.theme.logo` image when it is a repository file (path relative to the server YAML); a URL logo redirects. |
| GET | `/api/v1/portal/{server}/me` | member | `{"user": User, "member": {"nick", "joinedAt", "roles": [{"key","name","color"}]}, "role": "member"}` |
| ANY | `/api/v1/portal/{server}/plugins/{bot}/{instance}/{path…}` | per route | Plugin routes (`definePlugin({routes})`). The route's `access` is enforced; the JSON body/response are passed through. Unknown route → 404. |

"Servers with a portal" are servers that declare `spec.portal` or run a plugin with routes.
`theme.logo` is the logo endpoint above or an absolute URL.

Plugin routes:
- `access` (default `member`): `public` (anyone; the viewer is still filled when logged in),
  `member`, `moderator`, `admin` with the roles of §1. Anonymous → `401`, insufficient
  role → `403`. The bearer token passes every check (no viewer).
- The handler receives `request.viewer = {user, member}` (member fetched from Discord),
  the query parameters (first value of each) and the JSON body (non-JSON → `400`).
- Response: the handler's status (default `200`), JSON body, and its headers except
  `Set-Cookie`, `Content-Type`, `Location`, CORS, CSP/HSTS and hop-by-hop headers. No body →
  `204` (or the handler's status). A thrown error → `500 internal`.
- `/portal/{server}/me` → `403` when the caller is not a member. Roles also carry `id`.

```json
// PortalServer
{ "key": "ma-commu", "name": "Ma Commu", "iconUrl": "",
  "theme": { "primary": "#5865f2", "accent": "#eb459e", "logo": "/api/v1/portal/ma-commu/logo", "mode": "dark" },
  "plugins": [ { "bot": "moderator", "instance": "onboarding", "plugin": "onboarding", "routes": ["GET /options", "POST /choose"] } ] }
```

## 7. Machine endpoints (bearer token)

| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/sync` | Fetch the source now and sync (used by CI / webhooks without GitHub). `202 {"status": "accepted"}`. |
| POST | `/api/v1/plan` | Body: a `.tar.gz` of a repository (`Content-Type: application/gzip`). Validates it and returns `{"valid": bool, "diagnostics": [...], "plans": [Plan]}` against live Discord, without applying (`codi plan --remote`). |
| POST | `/hooks/github` | GitHub webhook (`X-Hub-Signature-256` verified with `CODI_WEBHOOK_SECRET`). `push` on the tracked branch triggers a sync. |
| GET | `/healthz` | `200 ok` when the process is up. |
| GET | `/readyz` | `200` once a revision is active, `503` otherwise. |

- Without `CODI_RUNTIME_TOKEN` the bearer endpoints answer `401`.
- Plan archive: `codi.yaml` at the root or inside a single top-level directory. Only
  regular files and directories; absolute paths, `..`, symlinks, hard links and special
  files → `400 invalid`. At most 5000 files, 16 MiB per file and 64 MiB extracted. An invalid
  repository is `200` with `valid: false`, its diagnostics and `plans: []`.
- Webhook: no secret configured or a bad signature → `401`; `ping` → `200`; `push` on
  `refs/heads/<tracked branch>` → `202`; other branches and events → `200 {"status": "ignored"}`.

## 8. Simulator (dev mode with `--sim` only)

Lets the admin UI's **Simulator** panel and tests play the role of Discord users. These
endpoints need no authentication (dev only) but follow the CSRF rule of §1. Without the
simulator they answer `404`. `codi dev --sim` seeds the world from the repository: every
server (its `discordId`) owned by a user `owner`, every bot (its `applicationId`) invited
with `Administrator`, and the users `alice`, `bob` and `charlie`, who are not members.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/sim/state` | `{"users": [User], "bots": [User], "servers": [{"id","name","channels":[...],"roles":[...],"members":[{"user","nick","roles"}]}]}` |
| POST | `/api/v1/sim/users` | `{"username"}` → `User` |
| POST | `/api/v1/sim/servers/{id}/members` | `{"userId"}` — the user joins the server (`memberJoin`). |
| DELETE | `/api/v1/sim/servers/{id}/members/{userId}` | The user leaves. |
| GET | `/api/v1/sim/channels/{id}/messages?limit=50` | `{"items": [Message]}` oldest first. |
| GET | `/api/v1/sim/users/{id}/dms` | `{"items": [Message]}` DMs received by the user from bots. |
| POST | `/api/v1/sim/messages` | `{"userId","channelId","content"}` → `Message` |
| POST | `/api/v1/sim/commands` | `{"userId","serverId","channelId","bot","name","options":{}}` → `InteractionResult` |
| POST | `/api/v1/sim/components` | `{"userId","channelId","messageId","customId","values":[]}` → `InteractionResult` |
| POST | `/api/v1/sim/modals` | `{"userId","serverId","channelId","bot","customId","fields":{"<input id>":"value"}}` → `InteractionResult` (submits a modal shown by a previous interaction). |
| GET | `/api/v1/sim/commands?serverId=` | `{"items": [{"bot","name","description","options":[...]}]}` |
| GET | `/api/v1/sim/events` | Server-Sent Events: `event: change` whenever the simulated world changes (UI refresh hint). |

- `bot` is a bot key (an application ID is accepted too).
- `state.servers[]` = `{"id","key","name","ownerId","channels":[{"id","name","type","parentId","position"}],"roles":[{"id","name","color","position","managed"}],"members":[{"user","nick","roles":["<role id>"]}]}`.
- Command options: `{"name","description","type","required","choices"?,"minValue"?,"maxValue"?}`.
  `type` is one of `string | integer | boolean | user | channel | role | mentionable | number | attachment`.
- Joining and leaving answer `204`. Creating a user or a message answers `201`.
- Validation errors of an interaction (unknown command, missing option, disabled
  button…) come back as `200` with `status: "failed"` and `error` set. `status: "modal"`
  carries `modal: {"customId","title","components"}`. `deferred` means the bot deferred
  and had not followed up after 5 s.
- SSE: an initial `change` event is sent, then bursts are coalesced (≤ 1 event / 100 ms),
  with a `: ping` comment every 25 s.

```json
// Message
{ "id": "…", "channelId": "…", "author": User, "content": "…", "embeds": [...],
  "components": [ { "type": "row", "components": [ { "type": "button", "customId": "c:welcome:go", "label": "Go", "style": "primary", "url": "" } ] } ],
  "ephemeral": false, "createdAt": "…" }
// component types: row | button | select | textInput
// button style: primary | secondary | success | danger | link
// select: {"type":"select","customId","placeholder","minValues","maxValues","options":[{"label","value","description","emoji","default"}]}
// textInput (modals): {"type":"textInput","customId","label","style":"short|paragraph","required","value","placeholder"}
// embeds: [{"title","description","url","color":"#rrggbb","timestamp","fields":[{"name","value","inline"}],"author","footer","imageUrl","thumbnailUrl"}]
// User = {"id","username","displayName","avatarUrl","bot"?}
// InteractionResult — waits up to 5 s for the bot's answer
{ "status": "replied",            // replied | deferred | failed | modal
  "response": Message | null, "followups": [Message], "error": "" }
```
