---
title: "HTTP API: status and servers"
description: The viewer (/me), runtime status with drift and boost state, sync history, server detail, resources for pickers, plans, syncs, pausing enforcement, the audit log and server image uploads.
---

Part of the [HTTP API reference](../). Roles are described in
[Authentication](../authentication/#roles).

## Me

`GET /api/v1/me` answers `200 Me` or `401`. `servers` lists every server where the caller has a
role (servers whose role cannot be resolved are skipped).

```json
{
  "user": { "id": "1575125353398009856", "username": "owner", "displayName": "owner", "avatarUrl": "" },
  "servers": [ { "key": "showcase", "name": "Cicord Showcase", "role": "admin" } ]
}
```

`User = {"id", "username", "displayName", "avatarUrl", "bot"?}` (`bot: true` for bot users).
During setup, the setup session's `/me` is the user `setup`.

## Runtime status

`GET /api/v1/status` (admin or moderator) returns the visible servers only (where the caller
is admin or moderator), the bots on them and their plugin instances on those servers, with the
drift of each server; `403` if there is none. Before the first revision, `revision` is
`null`, the lists are empty and the status is returned to any authenticated caller, without
`lastError` and `bots` unless the caller uses the runtime token (`lastError` may quote the
configuration).

```json
{
  "version": "0.2.0",
  "mode": "dev",                         // "serve" | "dev"
  "simulator": true,                     // the Discord simulator is in use
  "revision": { "id": "local-55cc940", "sha": "55cc9403b806…", "message": "working tree", "author": "local",
                "time": "2026-11-25T12:27:46.428729Z", "source": "local" },
  "servers": [ {
    "key": "showcase", "name": "Cicord Showcase", "discordId": "223344556677889900",
    "sync": "Synced",                    // Synced | OutOfSync | Unknown
    "health": "Healthy",                 // Healthy | Progressing | Degraded | Missing
    "message": "1 change(s) wait for a higher boost level",
    "lastSync": "2026-11-25T12:27:46.457287Z",  // optional: absent until the first reconcile
    "drift": [],                         // e.g. { "kind": "channel", "key": "general", "field": "name", "expected": "general", "actual": "general-chat" }
    "bot": "cicord",                     // optional: the reconciling bot
    // live Discord state (runtime cache, kept current by server update events)
    "iconUrl": "", "bannerUrl": "", "splashUrl": "",   // CDN URLs; "" = none (or not cached yet)
    "boostLevel": 0,                     // premium tier 0-3
    "boostCount": 0,                     // number of boosts
    "features": ["COMMUNITY", "NEWS"],   // Discord feature flags, sorted; [] when unknown
    "community": true,                   // COMMUNITY feature enabled
    "paused": { "by": "discord:1575125353398009856", "byName": "owner",   // optional: enforcement paused
                "at": "2026-11-25T12:30:02Z", "reason": "raid" },
    "unconfirmedAudit": { "bot": "cicord", "count": 2,   // optional: audited actions by the bot that Cicord did not emit (last 24 h)
                          "first": "2026-11-25T12:31:00Z", "last": "2026-11-25T12:31:04Z",
                          "lastEntry": "1575125353398009999", "lastAction": "webhook.create" }
  } ],
  "bots": [ {
    "key": "cicord", "username": "Cicord", "userId": "1575125353398009857", "health": "Healthy", "message": "",
    "servers": ["showcase"],
    "plugins": [ { "instance": "tickets", "plugin": "tickets", "version": "0.4.0", "server": "showcase", "health": "Healthy", "message": "",
                   "advisories"?: [ { "feature": "STAFF_WEBHOOK", "reason": "optional plugin secret not configured" } ] }, … ]
  } ]
}
```

- `revision.id` is the short identifier (7-character SHA, or `local-` and a short content hash
  for a local directory), `sha` the full SHA or hash; `source` is `git` or `local`.
- `lastError` (optional) is the latest rejected revision or failed fetch.
- `drift` comes from the server's last plan, planned again after an apply, a relevant gateway
  event, or a minute. Blocked changes are drift (`OutOfSync`), except those that only wait for
  a boost level: the server stays `Synced` and `message` counts them, naming the first
  blocked one (`2 change(s) blocked (delete channel old: … and 1 more), 1 wait for a higher
  boost level`). `message` follows the last plan:
  changes it found that are not applied yet (drift without `selfHeal`, a boost level just
  reached) are counted too (`1 change(s) to apply (selfHeal is off: …)`), and a failed sync
  keeps its error while changes remain.
- Drift `field` `"exists"` means the resource is missing in Discord (or not created yet),
  `"order"` that a reorder is pending, `"managed"` that a mapping is about to be dropped.
- A bot whose session is not connected is `Missing`, with the reason in `message`; it is
  `Degraded` when one of its plugin instances is (`message` names it). An instance is
  `Degraded` when it failed to start, after a crash while it restarts, after three handler
  errors in a row, while it has [dead letters](../plugin-instances/#failed-scheduled-tasks),
  or when a required [plugin secret](../plugin-instances/#plugin-secrets) is missing. It is
  `Disabled` with `enabled: false`. `advisories` lists optional features that are off (a
  missing optional grant or secret) without degrading it.
- `unconfirmedAudit` is set when Discord recorded audited actions by a project bot that match
  no operation Cicord emitted: someone else may be using the bot token. It raises the "Bot
  token possibly compromised" alert, expires a day after the last such entry and never
  changes `health`.
- `paused` is set while [enforcement is paused](#pause-and-resume) on the server: nothing is
  applied to it. `sync` keeps reporting the drift from git (`Synced` or `OutOfSync`) and
  `health` the last plan or sync.
- `boostLevel` and `features` drive the plan: settings that need a higher boost level or the
  Community feature are reported as `blocked` with a reason instead of failing at apply time
  (see [Boost-gated settings](../../../configuration/server/#boost-gated-settings)).
- Servers and bots also carry the [error object](../#the-error-object) as `error`, and
  `lastErrorInfo` explains `lastError`.

## Sync history

`GET /api/v1/history?limit=50&target=<target>` returns `{"items": [SyncRecord]}`, newest
first:

```json
{ "id": 3, "time": "2026-11-25T12:27:46.457315Z", "revision": "local-55cc940", "target": "server:showcase",
  "status": "succeeded", "message": "0 change(s) applied",
  "details": { "applied": 0, "failed": 0, "plan": { "server": "showcase", "changes": [], "blocked": [ … ] } } }
```

- `target` is `revision` (activation of a revision), `server:<key>` (a sync or self-heal) or
  `bot:<key>`. `status` is `succeeded`, `failed` or `rejected` (`rejected` = an invalid
  revision), or for a server `paused`, `resumed` and `skipped` (a revision not applied to a
  paused server). Only `failed` and `rejected` records carry `error`. `details` is optional.
- Visibility: `server:` records need a visible server, `bot:` records a visible server of
  that bot, and project-level records (`revision`) are only returned to callers who see every
  server. `403` when the caller sees no server, `503` before the first revision.
- `limit` defaults to 50, at most 500. `target` filters the query; records the caller may not
  see are dropped afterwards, so a page can hold fewer than `limit` items.

## Servers

| Method | Path | Role | Description |
|---|---|---|---|
| GET | `/api/v1/servers/{server}` | moderator | `ServerDetail`: the server's [status entry](#runtime-status), with its drift, plus `spec` (the Server YAML spec as JSON). |
| GET | `/api/v1/servers/{server}/resources` | moderator | [Resources](#resources) for pickers. |
| GET | `/api/v1/servers/{server}/plan` | admin | [`Plan`](#plan) of the active revision against live Discord. |
| POST | `/api/v1/servers/{server}/sync` | admin | Reconcile now. Returns `{"plan": Plan, "applied": n}`. `409 paused` while [enforcement is paused](#pause-and-resume), unless the body is `{"resume": true}` (resume, then sync). |
| GET | `/api/v1/servers/{server}/pause` | moderator | `{"paused": PauseInfo \| null, "plan": Plan \| null, "planError"?}`: the pause and the plan that resuming lets apply (for moderators only while paused; `null` otherwise). |
| POST | `/api/v1/servers/{server}/pause` | moderator | Pause enforcement. Body `{"reason"?}` (at most 500 characters). Returns `{"paused": PauseInfo, "changed": bool}`. |
| POST | `/api/v1/servers/{server}/resume` | moderator | Resume enforcement. Returns `{"resumed": bool, "pause"?: PauseInfo}`. |
| GET | `/api/v1/servers/{server}/audit?before=<id>&limit=50` | moderator | [`{"items": [AuditEntry]}`](#audit-log), newest first. |
| PUT | `/api/v1/servers/{server}/assets/{kind}` | admin | Upload the server `icon`, `banner` or `splash` ([below](#server-images)). |
| GET | `/api/v1/servers/{server}/assets/{kind}` | moderator | The image file declared in `spec.profile.<kind>` of the active revision (`404` when none), e.g. as a preview when the Discord CDN URL is unavailable. |
| GET | `/api/v1/servers/{server}/diagnostics` | admin | The [verification checklist](../setup/#catalog-and-diagnostics). |
| GET | `/api/v1/servers/{server}/overlays` | admin | `{"items": [Overlay]}`: the active [incident overlays](#incident-overlays). |
| DELETE | `/api/v1/servers/{server}/overlays/{bot}/{instance}/{id}` | admin | Lift one overlay (`204`). Audited as `overlay.remove`. |
| GET | `/api/v1/servers/{server}/interop` | admin | `{"edges": [{kind, from, to, contract, status}], "features": {"<bot>/<instance>": {"<feature>": "<reason off>"}}}`: who subscribes to or calls whom, and why a link is inactive (Diagnostics shows it). |
| POST | `/api/v1/servers/{server}/privacy/{export\|erase}` | admin | [Member data](#member-data). |
| GET, POST | `/api/v1/servers/{server}/orphaned-data`, `…/orphaned-data/purge` | admin | [Data of uninstalled installations](#data-of-uninstalled-installations). |

`plan` and `sync` failures (no bot, a Discord or reconcile error) answer `503 unavailable`, with
the explained [error object](../#the-error-object) fields. Every sync is recorded in the audit
log (`sync.apply`, details `{changes}`).

### Pause and resume

The emergency switch of a server (see
[Emergency: pause enforcement](../../../concepts/reconciliation/#emergency-pause-enforcement)).
While a server is paused, nothing is applied to it: not a new revision, the periodic
resync, self-heal or a sync. Plans and drift are still computed.
`PauseInfo = {"by", "byName"?, "at", "reason"?}`, with `by` in the audit `actor` form.

- Pausing a paused server changes nothing (`changed: false`); resuming a server that is not
  paused answers `resumed: false`. Resume does not apply the plan by itself, except the
  temporary overlay changes held during the pause: overlays that expired are removed (their
  fields return to the git configuration) and overlays applied meanwhile take effect.
- A sync refused because of a pause answers `409`:
  `{"error": {"code": "paused", "message": "enforcement is paused on server showcase by owner since …", "hint": "Resume enforcement first …"}}`.
- Each pause and resume is recorded in the audit log (`enforcement.pause`,
  `enforcement.resume`) and the sync history (`paused`, `resumed`), and sent on the
  [events stream](../events/) (`status`, and `sync` with the server).
- The pause is kept in the runtime's database: it survives restarts and new revisions.

### Resources

`@everyone` is omitted; roles are sorted from the top of the hierarchy, channels by position.
`color` is `"#rrggbb"` (`""` = none). Channel `type` uses the Server YAML names (`text`,
`voice`, `announcement`, `stage`, `forum`, `media`, `thread`). `category` is the key of the
channel's managed category and is omitted when there is none. `managed: true` means declared
in YAML (it has a key); unmanaged resources have `key: ""`.

```json
{
  "roles":      [ { "key": "moderator", "id": "…", "name": "Moderator", "color": "#e67e22", "managed": true } ],
  "categories": [ { "key": "community", "id": "…", "name": "Community", "managed": true } ],
  "channels":   [ { "key": "general", "id": "…", "name": "general", "type": "text", "category": "community", "managed": true } ]
}
```

### Plan

Changes are in apply order; blocked operations are not applied (see
[Reconciliation](../../../concepts/reconciliation/#blocked-changes-and-their-explanations)):

```json
{ "server": "showcase",
  "changes": [
    { "action": "update", "kind": "role", "key": "mod", "id": "1575125354564026369",
      "summary": "update role \"Moderator\": color",
      "fields": { "color": { "from": "#e67e22", "to": "#f1c40f" } } },
    { "action": "create", "kind": "channel", "key": "help", "summary": "create forum channel #help in \"Community\"",
      "fields": { "category": { "from": null, "to": "community" }, "name": { "from": null, "to": "help" },
                  "type": { "from": null, "to": "forum" } } }
  ],
  "blocked": [
    { "action": "update", "kind": "server", "key": "showcase", "id": "223344556677889900",
      "reason": "boost: banner requires boost level 2; server is level 0 (0 boost(s))",
      "fields": { "banner": { "from": null, "to": "./assets/banner.png" } },
      "error": { "code": "boost_required", "message": "Banner requires boost level 2; server is level 0 (0 boost(s))",
                 "hint": "Boost the server to the required level (Server Settings → Server Boost), or remove the setting from the YAML. Cicord applies it as soon as the level is reached.",
                 "details": "boost: banner requires boost level 2; server is level 0 (0 boost(s))",
                 "resource": { "action": "update", "kind": "server", "key": "showcase" } } }
  ] }
```

- `action`: `create`, `update`, `delete`, `adopt`, `reorder` or `unmanage` (`adopt`: an
  existing resource with the same name becomes managed; `unmanage`: a removed key is dropped
  from the resource map without touching Discord).
- `kind`: `server`, `role`, `category`, `channel`, `everyone`, `memberRole` (a role given to
  a member listed in `roles.<key>.members`; key `<role key>/<user id>`, `id` the user ID,
  field `assigned`) or a [parity](../../discord-parity/) kind (`widget`, `automod`,
  `onboarding`, `welcomeScreen`, `emoji`, `sticker`, `sound`).
- `id` (optional): the Discord ID of an existing resource. `fields` (optional): `from` → `to`
  of each field (strings, numbers, booleans, permission name lists, overwrite maps, key
  lists).
- `reorder` keys: `hierarchy` (roles), `categories`, a category key (its channels) or `(none)`
  (channels without a category).
- `blocked[]` = `{action, kind, key, id?, reason, summary?, fields?, error?}`: `fields` are
  the differences that could not be applied, when known; `summary` describes the operation
  when the kind and key do not (`memberRole`); `error` is the explained
  [error object](../#the-error-object).
- `notes[]` (omitted when empty) = `{kind, key, message}`: information that needs no action,
  e.g. a user listed in a role's `members` who is not in the server yet. Notes are not drift.
- `overlays[]` (omitted when empty): the active [incident overlays](#incident-overlays) the plan
  applied on top of git.

### Audit log

`before` is the ID cursor of the next page; `limit` is at most 500.

```json
{ "id": 1, "time": "2026-11-25T12:29:10.470788Z", "server": "showcase", "actor": "discord:1575125353398009856",
  "action": "sync.apply", "target": "showcase", "details": { "changes": 0 } }
```

`actor` is `"discord:<user id>"`, `"runtime-token"` or, for actions a plugin requested,
`"plugin:<bot>/<instance>"`; `details` is always an object. Actions include `sync.apply`,
`enforcement.pause`, `enforcement.resume`, `server.asset`, `config.update`,
`plugin.uninstall`, `store.install`, `plugin.update`, `plugin.secret.set`,
`plugin.data.purge`, `privacy.export`, `privacy.erase`, `task.retry`, `task.discard`,
`overlay.apply`, `overlay.remove`, `server.incident`, `server.sync.request`, `registry.add`,
`registry.remove` and `registry.token`.

### Server images

`PUT /api/v1/servers/{server}/assets/{icon|banner|splash}?revision=<id>` takes the raw image
bytes as body (any `Content-Type`: the bytes decide).

- Accepted: PNG, JPEG, GIF (animated kept; not for `splash`) and WebP, at most 8 MiB. SVG and
  anything else answer `415 unsupported_media_type`, larger files `413 too_large`. The image
  is not re-encoded.
- One [write-back](../../../concepts/write-back/) (mode from the `cicord.yaml` policies over
  both paths) writes the file at `<server YAML dir>/assets/<server>-<kind>.<ext>` and sets
  `spec.profile.<kind>: ./assets/<server>-<kind>.<ext>`.
- `revision` (optional): `409 conflict` when the active revision differs.
- Uploading the declared file again is a no-op. Commit mode waits for the new revision like a
  [config `PUT`](../plugin-instances/#editing-the-config).
- Audit action `server.asset` (details `{kind, path, format, bytes, mode, revision,
  pullRequestUrl}`).

```json
{ "mode": "commit", "revision": "local-0be1ea1", "pullRequestUrl": "",
  "path": "servers/assets/showcase-icon.png", "value": "./assets/showcase-icon.png",
  "format": "png", "width": 256, "height": 256,
  "warnings": [] }   // e.g. "the image is 300×100: Discord recommends a 16:9 image of at least 960×540"
```

The banner needs boost level 2 and the splash level 1: uploading works at any level, and the
plan blocks the setting (`boost_required`) until the server reaches it.

### Incident overlays

A plugin with `server.overlays` applies temporary restrictions on top of git with
`ctx.server.overlays`; each installation manages only its own. The admin console's **Plan**
page lists the active ones (installation, reason, expiry, targets) and lifts one with
**Lift overlay** (`DELETE …/overlays/{bot}/{instance}/{id}`).

```json
{ "origin": "cicord/guard", "id": "lock-general", "until": "2026-11-25T13:30:00Z", "reason": "raid",
  "channels": { "general": { "overwrites": { "@everyone": { "deny": ["SendMessages"] } }, "slowmodeSeconds": 30 } },
  "server": { "verificationLevel": "high" },
  "members": { "1575125353398009999": { "suspendDeclaredRoles": true } } }
```

An overlay adds denies, removes existing allows (`neutral`), raises slowmode or
verification, or suspends roles assigned to named holders in YAML. Wildcard channels
use the active revision's managed channel list. Restriction composition preserves
other installations' locks. Reusing an ID replaces its targets and expiry. The
maximum lifetime is seven days. Check the runtime plan for targets removed from YAML.

Overwrite targets are `@everyone`, `@bot`, a role key, `member:<id>`, or `*`. The `*`
target takes `neutral` only: it removes those permissions from the `allow` of every
overwrite the channel already has in its effective state (after category sync), except
`@everyone`, `@bot` and the roles and users of `spec.access.admins` and
`spec.access.moderators`. It never creates an overwrite. A Discord role allow wins over
an `@everyone` deny, so a lock that must also stop `verified: allow [SendMessages]`
combines both, and staff keep posting:

```ts
await ctx.server.overlays.apply({ id: "lock-general", reason: "raid", until,
  channels: { general: { overwrites: {
    "@everyone": { deny: ["SendMessages"] },
    "*": { neutral: ["SendMessages"] },
  } } } });
```

Lifting or expiring the overlay recomputes the channel from YAML, so the allows come back.

An overlay is refused when it is applied if its overwrites would take View Channel,
Manage Channels or Manage Roles away from a bot that is not Administrator in a
managed channel (a deny on `@bot`, or an `@everyone` View Channel deny the bot has no
allow against). Such an overlay would otherwise make the preflight refuse every overlay
sync of the server until it expires. A server holds at most 256 active overlays, and
one installation at most 128. An installation with fewer than 8 active overlays can
still apply one when the server is full, so one plugin's quarantines or channel locks
cannot starve another's raid overlay.

A durable task recomputes affected fields at expiry, including with self-healing
disabled. While enforcement is paused the runtime persists changes and expirations.
Resuming applies only pending overlay fields against the current revision. Other
drift follows normal sync policy. A failed Discord update retains its pending
targets and is retried. A local plan cannot see overlays. Use `cicord plan --remote`
before reviewing incident-related structural changes.

`server.incident` authorizes the bot to disable invites or DMs for up to 24 hours.
The Discord bot needs Manage Server. `server.sync` allows an installation to request
a full reconciliation with an audit reason. Requests are throttled per installation:
`requestSync` fails with a retry delay when the same installation's previous accepted
request is less than 30 seconds old, or while another sync runs. [Discord incident actions](https://docs.discord.com/developers/resources/guild#modify-guild-incident-actions)

## Member data

Admins do the same from **Server settings → Member data** in the admin console, which also lists the data of uninstalled installations with **Purge data**.

![Member data export and erasure, and the purge of an uninstalled plugin's data](../../../../assets/screenshots/admin-member-data.webp)

`POST /api/v1/servers/{server}/privacy/export` and `POST /api/v1/servers/{server}/privacy/erase` accept `{"userId":"123456789012345678"}`. `userId` must be a Discord user snowflake (17 to 20 digits); anything else is `400 invalid`. Both operations require a server administrator and the normal mutation CSRF header for cookie sessions. Responses use `Cache-Control: private, no-store`. The report lists each plugin instance, inventory and result, along with the number of removed tasks and inter-plugin records. Check the `complete` field and per-instance statuses even after HTTP 200:

| Status | Meaning |
|---|---|
| `ok` | The hook ran. |
| `no-member-data` | The plugin declares that it keeps no member data. |
| `undeclared` | No privacy hooks or inventory: Cicord cannot verify the plugin. |
| `disabled`, `unavailable`, `failed` | The hook could not run. The installation runs the erasure again when it starts, and its queued work that mentions the member is not delivered until then. Retry the request once it is healthy. |
| `orphaned` | An uninstalled installation still holds data that mentions the member. Its hooks cannot run: purge its data (below). |

An erasure that fails part-way (a storage error after some hooks ran) answers `503 unavailable` with the partial report: `{"error": {...}, "report": {...}}`. It is audited as `privacy.erase` with `"complete": false` and the per-instance statuses. Retrying is safe. The audit trail records a pseudonymous target, never the member ID.

### Data of uninstalled installations

Removing an installation from git keeps its stored data. `GET /api/v1/servers/{server}/orphaned-data` (server administrator) lists the installations of the server that are no longer in the active revision but still hold data, with their row counts:

```json
{"items": [{"instance": "moderator/old-tickets", "scope": "plugin:moderator/old-tickets/my-community", "records": 14, "rankings": 3, "index": 0, "tasks": 1}]}
```

`POST /api/v1/servers/{server}/orphaned-data/purge` with `{"instance": "moderator/old-tickets", "confirm": "moderator/old-tickets"}` deletes that installation's records, rankings, search index documents, queued and dead tasks, event deliveries and DM receipts on this server, and answers the deleted counts. `confirm` must repeat the name (`400 invalid` otherwise). An installation that is still in the active revision, disabled ones included, is refused with `409 conflict`. The purge cannot be undone; it is audited as `plugin.data.purge`. It does not remove the installation's admin-set secret (in the encrypted secret store) or the stored results of interop actions it served, which expire after 30 days and are tombstoned by a member erasure. The CLI equivalent is [`cicord plugin purge-data`](../../cli/#cicord-plugin-purge-data).
