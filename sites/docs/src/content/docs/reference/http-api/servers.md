---
title: "HTTP API: status and servers"
description: The viewer (/me), runtime status with live drift and boost state, sync history, server detail, resources for pickers, plans, syncs, the audit log and server image uploads.
---

Part of the [HTTP API reference](../). Roles are described in
[Authentication](../authentication/#roles).

## Me

`GET /api/v1/me` answers `200 Me` or `401`. `servers` lists every server where the caller has a
role (servers whose role cannot be resolved are skipped).

```json
{
  "user": { "id": "1", "username": "alice", "displayName": "Alice", "avatarUrl": "" },
  "servers": [ { "key": "ma-commu", "name": "Ma Commu", "role": "admin" } ]
}
```

`User = {"id", "username", "displayName", "avatarUrl", "bot"?}` (`bot: true` for bot users).
During setup, the setup session's `/me` is the user `setup`.

## Runtime status

`GET /api/v1/status` (admin or moderator) returns the visible servers only (where the caller
is admin or moderator), the bots on them and their plugin instances on those servers, with the
live drift of each server; `403` if there is none. Before the first revision, `revision` is
`null`, the lists are empty and the status is returned to any authenticated caller.

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
    "drift": [ { "kind": "channel", "key": "general", "field": "name", "expected": "général", "actual": "general" } ],
    // live Discord state (runtime cache, kept current by server update events)
    "iconUrl": "https://cdn.discordapp.com/icons/…/….png",   // "" = no icon (or not cached yet)
    "bannerUrl": "", "splashUrl": "",
    "boostLevel": 1,                     // premium tier 0-3
    "boostCount": 3,                     // number of boosts
    "features": ["ANIMATED_ICON", "INVITE_SPLASH"],  // Discord feature flags, sorted; [] when unknown
    "community": false                   // COMMUNITY feature enabled
  } ],
  "bots": [ {
    "key": "moderator", "username": "Gardien", "userId": "…", "health": "Healthy", "message": "",
    "servers": ["ma-commu"],
    "plugins": [ { "instance": "welcome", "plugin": "welcome", "version": "0.1.0", "server": "ma-commu", "health": "Healthy", "message": "" } ]
  } ]
}
```

- `revision.id` is the short identifier (7-character SHA, or a content hash for local
  sources), `sha` the full SHA or hash; `source` is `git` or `local`.
- Drift `field` `"exists"` means the resource is missing in Discord (or not created yet),
  `"order"` that a reorder is pending, `"managed"` that a mapping is about to be dropped.
- A bot whose session is not connected is `Missing`, with the reason in `message`; it is
  `Degraded` when one of its plugin instances is.
- `boostLevel` and `features` drive the plan: settings that need a higher boost level or the
  Community feature are reported as `blocked` with a reason instead of failing at apply time
  (see [Boost-gated settings](../../../configuration/server/#boost-gated-settings)).
- Servers and bots also carry the [error object](../#the-error-object) as `error`, and
  `lastErrorInfo` explains `lastError`.

## Sync history

`GET /api/v1/history?limit=50&target=<target>` returns `{"items": [SyncRecord]}`, newest
first:

```json
{ "id": 42, "time": "…", "revision": "4f0c2d9", "target": "server:ma-commu",
  "status": "succeeded", "message": "3 change(s) applied", "details": { "plan": {}, "applied": 3, "failed": 0 } }
```

- `target` is `revision` (activation of a revision), `server:<key>` (a sync or self-heal) or
  `bot:<key>`. `status` is `succeeded`, `failed` or `rejected` (`rejected` = an invalid
  revision). `details` is optional.
- Visibility: `server:` records need a visible server, `bot:` records a visible server of
  that bot, and project-level records (`revision`) are only returned to callers who see every
  server. `403` when the caller sees no server, `503` before the first revision.
- `limit` defaults to 50, at most 500 (the `target` filter applies after the limit).

## Servers

| Method | Path | Role | Description |
|---|---|---|---|
| GET | `/api/v1/servers/{server}` | moderator | `ServerDetail`: the server's [status entry](#runtime-status), with live drift, plus `spec` (the Server YAML spec as JSON). |
| GET | `/api/v1/servers/{server}/resources` | moderator | [Resources](#resources) for pickers. |
| GET | `/api/v1/servers/{server}/plan` | admin | [`Plan`](#plan) of the active revision against live Discord. |
| POST | `/api/v1/servers/{server}/sync` | admin | Reconcile now. Returns `{"plan": Plan, "applied": n}`. |
| GET | `/api/v1/servers/{server}/audit?before=<id>&limit=50` | moderator | [`{"items": [AuditEntry]}`](#audit-log), newest first. |
| PUT | `/api/v1/servers/{server}/assets/{kind}` | admin | Upload the server `icon`, `banner` or `splash` ([below](#server-images)). |
| GET | `/api/v1/servers/{server}/assets/{kind}` | moderator | The image file declared in `spec.profile.<kind>` of the active revision (`404` when none), e.g. as a preview when the Discord CDN URL is unavailable. |
| GET | `/api/v1/servers/{server}/diagnostics` | admin | The [verification checklist](../setup/#catalog-and-diagnostics). |

`plan` and `sync` failures (no bot, a Discord or reconcile error) answer `503 unavailable`, with
the explained [error object](../#the-error-object) fields. Every sync is recorded in the audit
log (`sync.apply`, details `{changes}`).

### Resources

`@everyone` is omitted; roles are sorted from the top of the hierarchy, channels by position.
`color` is `"#rrggbb"` (`""` = none). Channel `type` uses the Server YAML names (`text`,
`voice`, `announcement`, `stage`, `forum`, `media`, `thread`). `category` is the key of the
channel's managed category and is omitted when there is none. `managed: true` means declared
in YAML (it has a key); unmanaged resources have `key: ""`.

```json
{
  "roles":      [ { "key": "modo", "id": "…", "name": "Modo", "color": "#e67e22", "managed": true } ],
  "categories": [ { "key": "community", "id": "…", "name": "Communauté", "managed": true } ],
  "channels":   [ { "key": "general", "id": "…", "name": "général", "type": "text", "category": "community", "managed": true } ]
}
```

### Plan

Changes are in apply order; blocked operations are not applied (see
[Reconciliation](../../../concepts/reconciliation/#blocked-changes-and-their-explanations)):

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

### Audit log

`before` is the ID cursor of the next page; `limit` is at most 500.

```json
{ "id": 12, "time": "…", "server": "ma-commu", "actor": "discord:1", "action": "config.update", "target": "moderator/welcome", "details": {} }
```

`actor` is `"discord:<user id>"` or `"runtime-token"`; `details` is always an object. Actions
include `sync.apply`, `server.asset`, `config.update`, `plugin.uninstall`, `store.install`,
`plugin.update`, `registry.add`, `registry.remove` and `registry.token`.

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
{ "mode": "commit", "revision": "9a1b2c3", "pullRequestUrl": "",
  "path": "servers/assets/ma-commu-banner.png", "value": "./assets/ma-commu-banner.png",
  "format": "png", "width": 1920, "height": 1080,
  "warnings": [] }   // e.g. "the image is 300×100: Discord recommends a 16:9 image of at least 960×540"
```

The banner needs boost level 2 and the splash level 1: uploading works at any level, and the
plan blocks the setting (`boost_required`) until the server reaches it.
