---
title: HTTP API
description: The runtime's HTTP API v1 - conventions, errors and the explained error object, data tables, limits, headers and CORS, and the index of every endpoint.
---

The runtime (`cicord serve`, `cicord dev`) serves a JSON HTTP API, UTF-8, used by the admin
console, the portal SDK and the CLI. These pages are its reference: the contract between the
runtime (`internal/api`), the admin console (`packages/admin`), the portal SDK
(`@cicord/client`, `@cicord/react`) and the CLI. `@cicord/client`
(`packages/client/src/types.ts`) mirrors every shape.

:::caution
The API is `v1` of a pre-release project: shapes may still change. Prefer `@cicord/client` over
hand-written calls.
:::

| Page | Covers |
|---|---|
| [Authentication](authentication/) | Discord login, portal links, token mode, the runtime token, CSRF, the admin host, roles. |
| [Status and servers](servers/) | `/me`, runtime status, sync history, server detail, resources, plans, syncs, audit log, server images. |
| [Plugin instances](plugin-instances/) | Listing instances, editing their config through write-back, uninstalling. |
| [Community portal](portal/) | The public portal endpoints, plugin routes, and how the static UIs are served. |
| [Machine endpoints](machine/) | `POST /api/v1/sync`, `POST /api/v1/plan`, the GitHub webhook, health checks. |
| [Simulator](simulator/) | `/api/v1/sim/*`, only with `cicord dev --sim`. |
| [Setup wizard](setup/) | Setup mode, the wizard endpoints, the catalog and diagnostics. |
| [Store](store/) | Plugin sources, browsing, installing, updating, registries, the registry index. |
| [Analytics](analytics/) | Server activity analytics: the dashboard summary, time series, the metric catalog. |

## Conventions

- **Base path** `/api/v1`. Static UIs: `/admin/` (admin console) and `/` (portal). Health:
  `/healthz` and `/readyz`.
- Times are RFC 3339 strings. Discord IDs and keys are strings; the IDs of history and audit
  records are integers.
- Optional fields are omitted when empty (marked `?` in the shapes of these pages); lists are
  always present (`[]`, never `null`).

## Errors

An error is an HTTP status and `{"error": {"code": "<snake_case>", "message": "<human text>"}}`.
Validation errors add `"diagnostics": [Diagnostic]`:

```json
{ "severity": "error", "file": "servers/my-community.yaml", "line": 12, "path": "/spec/channels/general", "message": "…" }
```

`severity` is `error` or `warning`; `file`, `line` and `path` (a JSON pointer) are optional.
Clients must accept unknown codes.

| Code | Status |
|---|---|
| `unauthenticated` | 401 |
| `forbidden` | 403, and 421 for the [dev-mode Host check](#dev-mode-host-check) |
| `not_found` | 404 (unknown routes under `/api/`, `/auth/`, `/hooks/` included) |
| `invalid` | 400; 405 (non-GET on a static UI); 413 (body too large); 415 (plan upload content type); 422 (validation, with `diagnostics`) |
| `conflict` | 409 |
| `unavailable` | 503: no active revision yet, no connected bot, write-back failure, timeout |
| `internal` | 500 (also a panic in a handler) |
| `setup_required` | 503: [setup mode](setup/#setup-mode), no configured repository yet |
| `gone` | 410: setup endpoints after the setup completed |
| `invalid_token`, `intents` | 422 / 409: [setup wizard](setup/#errors) explanations (`intents` also on Store installs and updates) |
| `too_large`, `unsupported_media_type` | 413 / 415: [server image uploads](servers/#server-images) |
| `changed`, `confirmation_required`, `in_use`, `up_to_date`, `modified` | 409: [Store](store/) |
| `not_installed` | 404: [Store update review](store/#updating-a-plugin) of a plugin that is not installed |
| `analytics_disabled` | 404: [analytics](analytics/#errors) on a runtime without an analytics store |
| `registry_unavailable` | 502: [Store](store/), a registry could not be fetched |

### The error object

Reconciler and Discord errors are kept as text by the runtime; the API adds an **explained
form** next to the raw field, which is unchanged (`internal/reconcile/humanize.go`):

```json
{ "code": "community_required",
  "message": "Announcement and stage channels need the Community feature",
  "hint": "Declare community: {enabled: true, rulesChannel, updatesChannel} in the server YAML and Cicord enables Community at the next sync. …",
  "details": "reconcile my-community: 1 change(s) failed: create channel news: discord CreateChannel: … (HTTP 400 50035: Invalid Form Body {…})",
  "discordCode": 50035, "httpStatus": 400,
  "resource": { "action": "create", "kind": "channel", "key": "news" },
  "fields": [ { "path": "type", "code": "BASE_TYPE_CHOICES", "message": "Value must be one of {0, 2}." } ],
  "failures": 3 }
```

`code` and `message` are always set. `hint`, `details` (the raw text), `discordCode`,
`httpStatus`, `resource` (the failed plan change), `fields` (Discord's "Invalid Form Body"
tree, flattened) and `failures` (changes that failed in the same sync, when more than one) are
optional.

Codes: `community_required`, `boost_required`, `missing_permissions`, `missing_access`,
`role_hierarchy`, `unknown_resource`, `invalid_form_body`, `limit_reached`, `invalid_token`,
`intents_disallowed`, `rate_limited`, `discord_unavailable`, `timeout`, `bot_not_connected`,
`no_bot`, `unresolved_reference`, `revision_rejected`, `fetch_failed`, `bad_request`,
`unknown`. For [blocked plan operations](../../concepts/reconciliation/#blocked-changes-and-their-explanations)
also `deletion_protection`, `ownership`, `managed_role`, `type_change`, `recreate_required`,
`undeletable`, and `role_hierarchy`, `no_bot`, `boost_required`, `community_required`,
`limit_reached` (for the blocked reasons prefixed `boost: `, `community: `, `limit: `). Clients
must accept unknown codes.

The error object appears as:

- `error` on a server of `/status` and `/servers/{server}` (Degraded or Missing with a
  `message`), and on a bot of `/status`; `lastErrorInfo` explains `lastError`;
- `error` on `/history` records whose status is `failed` or `rejected`;
- `error` on every `blocked[]` entry of a [server plan](servers/#plan) (`GET …/plan`, `POST …/sync`; not the machine `POST /api/v1/plan`);
- in the error envelope of `plan` and `sync` failures (`503`): `message` is the explained
  message, plus `hint`, `details` (raw text) and `reason` (the error object's `code`).

## Data tables

Lists marked *paged* accept `?q=<words>&sort=<key>&order=asc|desc&offset=<n>&limit=<n>`:

- `q` keeps the items containing every word (case-insensitive);
- `sort` must be one of the keys listed for the endpoint (`400 invalid` otherwise);
- `limit` is capped at 200.

The response adds `{"total", "offset", "limit", "sort", "order"}` next to `items`; `total`
counts the filtered items before the window.

## Limits

- JSON bodies ≤ 1 MiB (`413 invalid`).
- `POST /api/v1/plan` uploads ≤ 32 MiB; GitHub webhook payloads ≤ 25 MiB.
- Handlers time out after 60 seconds (`503 unavailable`), except the simulator's
  Server-Sent Events stream.

## Headers and CORS

- Every response carries `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`
  (portal URLs may carry `cicord_token`); JSON responses are `Cache-Control: no-store`; the
  admin console is served with `X-Frame-Options: DENY`.
- CORS is same-origin by default, and every response carries `Vary: Origin`. Two lists of
  external portal origins are allowed:
  - **operator origins**, `--cors-origin` (`CICORD_CORS_ORIGINS`): **with** credentials
    (`Access-Control-Allow-Credentials: true`), for cookie sessions;
  - **repository origins**, `cicord.yaml` `spec.portal.allowedOrigins` of the active revision:
    **without** credentials, for [token mode](authentication/#token-mode-embedded-portals)
    only, so that a merged pull request can never open cookie sessions to a new site.
- Preflights (`OPTIONS` with `Access-Control-Request-Method`) answer `204`. For an allowed
  origin they allow the methods `GET, POST, PUT, PATCH, DELETE, OPTIONS`, the headers
  `Content-Type`, `Authorization`, `X-Cicord-Request` and `Last-Event-ID`, with a max age of
  600 seconds.

## Dev-mode Host check

With `cicord dev` only, to defeat DNS rebinding, the runtime only answers requests whose `Host`
is `localhost`, `*.localhost`, `127.0.0.1`, `::1` or the host of `--public-url`; anything else
answers `421 forbidden` ("unexpected Host header"). `cicord serve` does not restrict `Host`
(see the [admin host](authentication/#admin-host---admin-url) instead).

## Endpoints

Roles are per server: see [Roles](authentication/#roles).

### Viewer and runtime

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/me` | signed in | [The user and the servers where they have a role](servers/#me). |
| GET | `/api/v1/status` | moderator | [Revision, servers and bots](servers/#runtime-status). |
| GET | `/api/v1/history` | moderator | [Sync history](servers/#sync-history). |
| GET | `/api/v1/events` | moderator | [Live change hints (Server-Sent Events)](events/). |

### Servers

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/servers/{server}` | moderator | [Status entry with its drift, plus the server's YAML `spec`](servers/#servers). |
| GET | `/api/v1/servers/{server}/resources` | moderator | [Roles, categories and channels](servers/#resources), for pickers. |
| GET | `/api/v1/servers/{server}/plan` | admin | [The plan](servers/#plan) of the active revision against live Discord. |
| POST | `/api/v1/servers/{server}/sync` | admin | Reconcile now: `{plan, applied}`. |
| GET | `/api/v1/servers/{server}/audit` | moderator | [Audit log](servers/#audit-log), newest first. |
| PUT, GET | `/api/v1/servers/{server}/assets/{icon\|banner\|splash}` | admin (GET: moderator) | [Upload or read the server icon, banner or splash](servers/#server-images). |
| GET | `/api/v1/servers/{server}/diagnostics` | admin | [The setup wizard's checklist](setup/#catalog-and-diagnostics), on demand. |
| GET | `/api/v1/servers/{server}/analytics/summary` | moderator | [KPIs, rankings and heatmap of a period](analytics/#summary). |
| GET | `/api/v1/servers/{server}/analytics/series` | moderator | [Time series of metrics](analytics/#series) by hour, day or week. |
| GET | `/api/v1/servers/{server}/analytics/catalog` | moderator | [Every metric and whether it is collected](analytics/#catalog). |

### Plugin instances

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/servers/{server}/plugins` | moderator | [Instances](plugin-instances/) (paged). |
| GET | `/api/v1/servers/{server}/plugins/{bot}/{instance}` | moderator | One instance. |
| PUT | `/api/v1/servers/{server}/plugins/{bot}/{instance}/config` | moderator (field-restricted) | [Write the config](plugin-instances/#editing-the-config) through write-back. |
| DELETE | `/api/v1/servers/{server}/plugins/{bot}/{instance}` | admin | [Uninstall from this server](plugin-instances/#uninstalling). |

### Community portal

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/portal/servers` | none | [Servers with a portal](portal/). |
| GET | `/api/v1/portal/{server}` | none | One `PortalServer`. |
| GET | `/api/v1/portal/{server}/logo`, `/banner` | none | The theme logo and banner images. |
| GET | `/api/v1/portal/{server}/me` | member | The viewer's member, roles and role on the server. |
| GET | `/api/v1/portal/{server}/analytics` | per server | [Server-wide activity, when shared](portal/#activity). |
| ANY | `/api/v1/portal/{server}/plugins/{bot}/{instance}/{path…}` | per route | [Plugin routes](portal/#plugin-routes). |

### Machine endpoints (CLI and CI)

| Method | Path | Auth | Result |
|---|---|---|---|
| POST | `/api/v1/sync` | bearer | [Fetch the source now and sync](machine/) (`202`). |
| POST | `/api/v1/plan` | bearer | [Validate and plan a `.tar.gz` of a repository](machine/#plan-archive). |
| POST | `/hooks/github` | HMAC | [GitHub push webhook](machine/#github-webhook). |
| GET | `/healthz`, `/readyz` | none | Liveness and readiness. |

### Store and registries

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/catalog` | admin | [The official plugins bundled in the binary](setup/#catalog-and-diagnostics). |
| GET | `/api/v1/store` | admin | [Every source with its plugins](store/#endpoints). |
| GET | `/api/v1/store/plugins` | admin | Plugins across sources (paged). |
| POST | `/api/v1/store/refresh` | admin | Refetch every registry index. |
| GET | `/api/v1/store/{source}/{plugin}` | admin | What the latest version requests. |
| GET, POST | `/api/v1/store/{source}/{plugin}/update` | admin (POST: on every server running it) | [Review, then apply, an update](store/#updating-a-plugin). |
| POST | `/api/v1/servers/{server}/store/install` | admin | [Install exactly the reviewed version](store/#installing). |
| POST, DELETE | `/api/v1/registries`, `/api/v1/registries/{name}` | admin of every server | [Add or remove a registry](store/#registries). |
| PUT, DELETE | `/api/v1/registries/{name}/token` | admin of every server | Set or remove a private registry's token. |

### Setup wizard and simulator

`/api/v1/setup/*` serves the [setup wizard](setup/) and `/api/v1/sim/*` the
[simulator](simulator/) of `cicord dev --sim`.
