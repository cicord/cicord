---
title: "HTTP API: setup wizard"
description: Setup mode, the one-time setup session, the wizard endpoints and their explained errors, the simulator stand-ins for discord.com, the plugin catalog and on-demand diagnostics.
---

Part of the [HTTP API reference](../). How the wizard works and why it is built this way:
[Setup wizard](../../../getting-started/setup-wizard/).

## Setup mode

`cicord serve` and `cicord dev` started in a directory without `cicord.yaml` (and without a
repository already created by the wizard) serve only:

- the admin console;
- the endpoints of this page;
- `/healthz`, and `/readyz` (`503 setup mode`).

Every other `/api/*` and `/auth/*` path answers `503 setup_required`; `/` redirects (`302`) to
`/admin/setup`.

```json
{"error": {"code": "setup_required", "message": "Cicord is not set up yet.",
           "hint": "Open the setup link printed in the Cicord logs (/admin/setup?token=…)."}}
```

 The runtime prints a one-time link
`<public url>/admin/setup?token=<token>` (`<admin url>/admin/setup?token=…` with
`--admin-url`; the public host then only answers `/healthz` and `/readyz`, see
[Admin host](../authentication/#admin-host---admin-url)).

## Session

- `GET /api/v1/setup/status` (anonymous) returns
  `{"active", "completed", "authenticated", "simulator"?, "view"?: SetupView}`. `view` is set
  only for a valid setup session. In setup mode, before the session:
  `{"active": true, "authenticated": false, "completed": false, "simulator": true}` (with
  `--sim`); without the wizard (normal runtime):
  `{"active": false, "completed": true, "authenticated": false}`.
- `POST /api/v1/setup/session` `{"token"}` returns the same shape and sets the cookie
  `cicord_setup` (HttpOnly, `SameSite=Strict`, `Secure` behind HTTPS, 24 hours).
  - The token is consumed: a reused or wrong token answers `401 unauthenticated`. After
    completion: `410 gone`.
  - The usual [CSRF rule](../authentication/#csrf) applies (`X-Cicord-Request`).
- Every other `/api/v1/setup/*` endpoint needs the setup session, otherwise
  `401 unauthenticated` with a `hint`. After completion: `410 gone`.
- Until completion, the setup session is an **admin** on every server for the rest of the API,
  like the runtime bearer token; `/me` returns the user `setup`.

## Errors

Wizard errors use the error envelope with explanations:

```json
{"error": {"code", "message", "hint"?, "details"?, "link"?, "diagnostics"?}}
```

`link` is the page where the fix happens (the developer portal…).

| Code | Status | Meaning |
|---|---|---|
| `invalid_token` | 422 | Discord rejected the bot token. |
| `intents` | 409 | A plugin needs a privileged intent that is off. |
| `invalid` | 400 / 422 | Bad input, or a plugin config that does not validate (with `diagnostics`). |
| `conflict` | 409 | Step prerequisites not met, configuration already active, plugin already installed. |
| `unavailable` | 503 | Discord, git or the runtime could not be reached. |
| `gone` | 410 | Setup already complete. |

## Wizard endpoints

All under `/api/v1/setup`. [`SetupView`](#shapes) is described below.

| Endpoint | Body | Result |
|---|---|---|
| `POST /intent` | `{"intent": "existing" \| "new"}` | `SetupView` (moves to `discord`) |
| `POST /step` | `{"step"}` | `SetupView` |
| `POST /discord/token` | `{"token"}` | `SetupView` |
| `POST /discord/recheck` | | `SetupView` |
| `GET /discord/invite` | | `{"url", "scopes": ["bot", "applications.commands"], "permissions": "Administrator" \| "ViewChannel", "access", "adminUrl", "inspectUrl"}` |
| `GET /discord/servers` | | `{"servers": [{"id", "name", "iconUrl"?}]}` |
| `POST /server` | `{"serverId"}` | `{"view", "checks": [Check], "ok"}` |
| `POST /server/check` | | Same as `POST /server` |
| `POST /access` | `{"access": "admin" \| "inspect"}` | `SetupView` |
| `POST /inspect` | | `{"report": InspectReport, "plan": Plan}` |
| `POST /repo` | `{"provider": "embedded" \| "github" \| "local", "url"?, "branch"?, "token"?}` | `SetupView` |
| `POST /import` | `{"confirm"?: bool}` | `{"view", "plan": Plan \| null, "needsConfirm", "files", "warnings"}` |
| `POST /oauth` | `{"clientSecret"}` | `SetupView` |
| `POST /oauth/skip` | | `SetupView` |
| `GET /oauth/test` | | `{"ok", "user"?, "servers"?}` |
| `GET /verify` | | `Report` |
| `GET /plugins` | | The catalog (as `/api/v1/catalog`) |
| `POST /plugins/install` | `{"plugin", "config"}` | `{"result": {"instance", "health", "message"?, "command"?, "revision"?, "mode", "pullRequestUrl"?}, "view"}` |
| `POST /finish` | | `SetupView` (`completed: true`) |

- `POST /step` moves to a step whose prerequisites are met, else `409 conflict` with a `hint`.
  `repository` re-runs the server checks and refuses while one fails. Steps up to
  `repository` cannot be revisited once the configuration is active.
- `POST /discord/token` validates the token with Discord (`/users/@me`,
  `/oauth2/applications/@me`), derives the bot key, the application ID and the privileged
  intents from the application flags, and stores the token encrypted under
  `DISCORD_TOKEN_<BOT>`.
- `POST /discord/recheck` re-reads the application (privileged intents) with the stored token.
  The `intents` check of a new simulated application:

  ```json
  { "id": "intents", "status": "warn",
    "message": "“Message Content Intent” and “Server Members Intent” are turned off. Some official plugins need it (welcome messages need Server Members, levels need Message Content).",
    "hint": "In the Discord Developer Portal, open your application → Bot → Privileged Gateway Intents, turn on “Message Content Intent” and “Server Members Intent”, click Save Changes, then re-check.",
    "link": "https://discord.com/developers/applications/1575128033587625984/bot",
    "data": { "enabled": [], "missing": ["MessageContent", "ServerMembers"] } }
  ```
- `GET /discord/servers` lists the servers the bot is in (`GET /users/@me/guilds`). The UI
  polls it every 2.5 seconds while the invite is open.
- `POST /server` picks the server (it must be in that list) and checks it: `member`,
  `permissions`, `hierarchy`.
- `POST /access` chooses the invite of the Server step: `admin` (default, `inviteUrl`,
  Administrator) or `inspect` (`inspectInviteUrl`, View Channels only). `POST /sim/invite`
  authorizes the chosen one.
- `POST /inspect` imports the chosen server read-only into a temporary directory and plans it
  through a client that refuses every change. It needs only membership. The wizard state and
  the resource map are not touched. The report has `categories` and `channels` (each with
  `visible` and `private`), `unlisted` (hidden channels the server refers to), `unreadable`
  (`part`, `name`, `needs`), `needs` (`permission`, `sources`, `why`), `hierarchy` (a Check),
  `ready`, `upgradeInviteUrl`, `adminInviteUrl`, `reads` and `writes` (always 0). `POST /step`
  to `repository` still requires the server checks to pass.
- `POST /repo`: `providers` in `SetupView` lists the choices of this runtime (`serve`:
  embedded, github; `dev`: local, embedded). For `github`, the remote is checked with the
  token, which is stored encrypted as `CICORD_GIT_TOKEN`.
- `POST /import` imports the server and computes a preflight plan before anything changes. If
  the plan is not empty, it answers `needsConfirm: true` with the plan; call again with
  `confirm: true` to go on. Otherwise it writes the first commit and activates the runtime
  **without restart**, then answers with the runtime's first plan (`changes: []` when
  everything was adopted). Repeating the call after activation returns the same result.
- `POST /oauth` checks the OAuth2 client secret with Discord and stores it
  (`CICORD_DISCORD_CLIENT_SECRET`). `POST /oauth/skip` skips admin login.
- `GET /oauth/test` reports whether this browser has a Discord login session after "Test
  login" (`/auth/login` in another tab).
- `GET /verify` runs the checklist on the wizard's server. The `events` check counts messages
  since the Verify step started.
- `POST /plugins/install` installs a catalog plugin. Privileged intents are checked against the
  application. The config is validated on a copy of the repository; then the sources are
  written to `plugins/<name>/` and the instance (every capability granted) to
  `bots/<bot>/bot.yaml`.
- `POST /finish` completes the setup and revokes the setup sessions.

### Simulator stand-ins

With `cicord dev --sim --setup` only (otherwise `404`), these endpoints stand in for
discord.com:

| Endpoint | Body | Result |
|---|---|---|
| `GET /sim` | | `{"servers", "owner"}` |
| `POST /sim/bot` | `{"name"?}` | `{"token"}` (a simulated application, intents off) |
| `POST /sim/intents` | `{"enabled"}` | `204` |
| `POST /sim/invite` | `{"serverId"?, "low"?}` | `204` (`low` puts the bot role at the bottom) |
| `POST /sim/raise-role` | | `204` |
| `POST /sim/grant-admin` | | `204` (the bot's role gets Administrator, as after authorizing that invite) |
| `POST /sim/message` | | `204` (the owner sends a message) |

## Shapes

```json
// SetupView (never contains secret values)
{ "step": "welcome|discord|server|repository|login|verify|plugin|done", "steps": [...],
  "completed": false, "mode": "serve|dev", "simulator": false, "version": "0.1.10",
  "intent"?: "existing|new", "providers": ["embedded", "github"], "publicUrl": "…",
  "bot"?: { "key", "secretRef", "applicationId", "applicationName", "userId", "username",
            "avatarUrl"?, "flags", "tokenStored", "portalUrl", "intents": Check },
  "access": "admin|inspect",
  "inviteUrl"?: "https://discord.com/oauth2/authorize?client_id=…&permissions=8&scope=bot+applications.commands…",
  "inspectInviteUrl"?: "…&permissions=1024&…",
  "server"?: { "id", "name", "iconUrl"?, "key" },
  "repo"?: { "provider", "url"?, "branch", "tokenStored"? },
  "activated": false, "revision"?: "…", "firstPlan"?: Plan,
  "login": { "configured", "skipped", "testedAs"?, "clientId"?, "redirectUrl", "redirectUrls", "portalUrl", "fromEnv" },
  // redirectUrl: the callback of the host the wizard runs on (the admin URL with --admin-url);
  // redirectUrls: every callback the Discord application must list (public, then admin)
  "plugin"?: { "name", "instance", "command"?, "skipped"? } }
// Check
{ "id": "token|intents|member|permissions|hierarchy|commands|events|plan|writeback",
  "status": "ok|warn|fail|pending|skip", "message": "…", "hint"?: "…", "link"?: "…", "data"?: {…} }
// Report (/setup/verify, /servers/{server}/diagnostics)
{ "server", "bot", "checks": [Check], "ranAt", "ok", "plan"?: Plan }
```

`ok` is true when no check is `fail` or `pending`. `fail` blocks the step. The UI titles checks
by `id`; `message` and `hint` are written for people.

## Catalog and diagnostics

These endpoints answer in any mode.

- `GET /api/v1/catalog` (admin of a server, the setup session or the bearer token) returns
  `{"plugins": [CatalogPlugin]}`: the [official plugins](../../../plugins/official/) bundled in
  the binary (welcome, onboarding, tickets, polls, levels, moderation, reaction-roles,
  audit-log, announcements, voice-rooms).

  ```json
  { "name", "displayName", "description", "version", "keywords": [], "source": "official",
    "capabilities": [], "intents": [], "privilegedIntents": [], "network": [],
    "schema": {…}, "commands": [{"name", "description"}] }
  ```

- `GET /api/v1/servers/{server}/diagnostics[?since=<RFC 3339>]` (admin) returns a `Report`:
  the Verify checklist on demand. `since` bounds the `events` check; by default any message
  since the runtime started counts.
