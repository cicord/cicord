---
title: HTTP API
description: Overview of the runtime's HTTP API v1 - authentication, roles, CSRF, errors and the main endpoints for status, servers, plugins, the portal, CI and the Store.
---

The runtime serves a JSON HTTP API used by the admin console, the portal SDK and the CLI. This
page is an overview. The normative contract, with every shape, is
[`spec/api.md`](https://github.com/cicord/cicord/blob/main/spec/api.md);
`@cicord/client` mirrors it in TypeScript.

:::caution
The API is `v1` of a pre-release project: shapes may still change. Prefer `@cicord/client` over
hand-written calls.
:::

## Basics

- **Base path** `/api/v1`. Static UIs: `/admin/` (admin console) and `/` (portal). Health:
  `/healthz` and `/readyz`.
- **Errors**: an HTTP status and `{"error": {"code", "message"}}`. Validation errors add
  `diagnostics` (`severity`, `file`, `line`, `path`, `message`). Reconciler and Discord errors
  also carry an explained form (`hint`, `details`, `discordCode`, the failed `resource`…).
  Clients must accept unknown codes.

  | Code | Status |
  |---|---|
  | `unauthenticated` | 401 |
  | `forbidden` | 403 (and 421 for the dev-mode Host check) |
  | `not_found` | 404 |
  | `invalid` | 400, 405, 413, 415, 422 |
  | `conflict` | 409 (and `changed`, `confirmation_required`, `in_use` for the Store) |
  | `unavailable` | 503: no active revision, no connected bot, write-back failure, timeout |
  | `setup_required` | 503: setup mode |
  | `gone` | 410: setup endpoints after setup |
  | `internal` | 500 |

- Times are RFC 3339 strings; Discord IDs and keys are strings. Optional fields are omitted;
  lists are always present.
- **Data tables** (paged lists): `?q=&sort=&order=asc|desc&offset=&limit=` (limit ≤ 200); the
  response adds `total`, `offset`, `limit`, `sort`, `order`.
- **Limits**: JSON bodies ≤ 1 MiB, `POST /api/v1/plan` uploads ≤ 32 MiB; handlers time out
  after 60 seconds.

## Authentication

| Client | How |
|---|---|
| Browser, same origin | The `cicord_session` cookie (HttpOnly, `SameSite=Lax`), from Discord OAuth2 (`/auth/login`) or a portal link (`/auth/link`). |
| Portal on another origin | A session token, `Authorization: Bearer cicord_st_…` (token mode). |
| CLI, CI | `Authorization: Bearer <CICORD_RUNTIME_TOKEN>`: admin on every server. |

| Endpoint | Purpose |
|---|---|
| `GET /auth/login?redirect=<path>[&mode=token]` | Redirect to Discord OAuth2 (scope `identify`). |
| `GET /auth/callback` | OAuth2 callback. Register `<public-url>/auth/callback` in the Discord application. |
| `POST /auth/link` | Exchange a `ctx.portal.link()` token (`cicord_token`) for a session scoped to one server. |
| `POST /auth/token` | Token mode: redeem the one-time `cicord_code` for a 24-hour session token. |
| `POST /auth/dev-login` | Dev mode only: sign in as any user (needs `--sim` or the runtime token). |
| `POST /auth/logout` | End the session (or revoke the bearer session token). |

**CSRF**: every unsafe request (`POST`, `PUT`, `PATCH`, `DELETE`) needs `X-Cicord-Request: 1`
unless it has an `Authorization` header, and an `Origin` that is the public URL or an allowed
CORS origin. `/hooks/*` is exempt (HMAC).

**Roles**: per server, `admin` (owner or `spec.access.admins`), `moderator`, `member` or none,
resolved from the user's live Discord roles. See [Access and roles](../../concepts/access-and-roles/).

## Endpoints

### Viewer and runtime

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/me` | signed in | The user and the servers where they have a role. |
| GET | `/api/v1/status` | moderator | Revision, servers (sync, health, drift, boost level, features) and bots with their plugin instances. |
| GET | `/api/v1/history` | moderator | Sync history: revision activations and rejections, server syncs. |

### Servers

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/servers/{server}` | moderator | Status entry with live drift, plus the server's YAML `spec`. |
| GET | `/api/v1/servers/{server}/resources` | moderator | Roles, categories and channels, for pickers. |
| GET | `/api/v1/servers/{server}/plan` | admin | The plan of the active revision against live Discord. |
| POST | `/api/v1/servers/{server}/sync` | admin | Reconcile now: `{plan, applied}`. |
| GET | `/api/v1/servers/{server}/audit` | moderator | Audit log, newest first. |
| PUT | `/api/v1/servers/{server}/assets/{icon\|banner\|splash}` | admin | Upload the server icon, banner or splash (committed through write-back). |
| GET | `/api/v1/servers/{server}/diagnostics` | admin | The setup wizard's verification checklist, on demand. |

### Plugin instances

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/servers/{server}/plugins` | moderator | Instances (paged): plugin, effective config, schema, editable fields, commands, routes, health, trust, available update. |
| GET | `/api/v1/servers/{server}/plugins/{bot}/{instance}` | moderator | One instance. |
| PUT | `/api/v1/servers/{server}/plugins/{bot}/{instance}/config` | moderator (field-restricted) | Write the config, for this server (override) or the base, through write-back. |
| DELETE | `/api/v1/servers/{server}/plugins/{bot}/{instance}` | admin | Uninstall from this server. |

### Community portal

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/portal/servers` | none | Servers with a portal (`PortalServer`). |
| GET | `/api/v1/portal/{server}` | none | One `PortalServer`: theme, texts, links, pages, locale, installed plugins. |
| GET | `/api/v1/portal/{server}/logo`, `/banner` | none | The theme logo and banner images. |
| GET | `/api/v1/portal/{server}/me` | member | The viewer's member, roles and role on the server. |
| ANY | `/api/v1/portal/{server}/plugins/{bot}/{instance}/{path…}` | per route | Plugin routes; the route's `access` is enforced. |

### Machine endpoints (CLI and CI)

| Method | Path | Auth | Result |
|---|---|---|---|
| POST | `/api/v1/sync` | bearer | Fetch the source now and sync (`202`). Used by `cicord sync`. |
| POST | `/api/v1/plan` | bearer | Body: a `.tar.gz` of a repository. Validates and plans it against live Discord without applying (`cicord plan --remote`). |
| POST | `/hooks/github` | HMAC (`CICORD_WEBHOOK_SECRET`) | GitHub push webhook. |

### Store and registries

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/catalog` | admin | The official plugins bundled in the binary. |
| GET | `/api/v1/store` | admin | Every source (`cicord` first, then registries) with its plugins. |
| GET | `/api/v1/store/plugins` | admin | Plugins across sources (paged, filterable by source, trust, installed, updates). |
| POST | `/api/v1/store/refresh` | admin | Refetch every registry index. |
| GET | `/api/v1/store/{source}/{plugin}` | admin | What the latest version requests (capabilities, intents, network, schema, commands). |
| GET, POST | `/api/v1/store/{source}/{plugin}/update` | admin (POST: on every server running it) | Review, then apply, an update. |
| POST | `/api/v1/servers/{server}/store/install` | admin | Install exactly the reviewed version (`409 changed` otherwise). |
| POST, DELETE | `/api/v1/registries`, `/api/v1/registries/{name}` | admin of every server | Add or remove a registry (write-back to `cicord.yaml`). |
| PUT, DELETE | `/api/v1/registries/{name}/token` | admin of every server | Set or remove a private registry's token. |

### Setup wizard

In setup mode (no `cicord.yaml`), only the admin console, `/api/v1/setup/*`, `/api/v1/catalog`
and the health endpoints are served; everything else answers `503 setup_required`. The wizard
endpoints are under `/api/v1/setup` and need the setup session obtained from the one-time
token (`POST /api/v1/setup/session`). After **Finish** they answer `410 gone`.

### Simulator (`cicord dev --sim` only)

`/api/v1/sim/*` exposes the simulated world to the admin console's Simulator panel: state,
users, joins and leaves, boosts, messages, DMs, slash commands, components, modals, and a
Server-Sent Events stream of changes. These endpoints answer `404` without `--sim`.

## Headers and CORS

- Every response carries `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`;
  JSON responses are `Cache-Control: no-store`; the admin console is served with
  `X-Frame-Options: DENY`.
- CORS is same-origin by default. Operator origins (`--cors-origin`) are allowed **with**
  credentials; repository origins (`cicord.yaml` `spec.portal.allowedOrigins`) **without**
  credentials, for token mode only.
