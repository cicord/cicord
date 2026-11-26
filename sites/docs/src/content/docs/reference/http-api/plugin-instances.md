---
title: "HTTP API: plugin instances"
description: Listing the plugin instances of a server, editing their configuration through write-back with field-level access, and uninstalling them.
---

Part of the [HTTP API reference](../). The underlying model is described in
[Bot → Plugin instances](../../../configuration/bot/#plugin-instances).

## Listing instances

`GET /api/v1/servers/{server}/plugins` (moderator, *[paged](../#data-tables)*: sort `name`
(default), `instance`, `bot`, `version`, `health`, `source`; filter `updates=true|false`;
without `limit` every instance is returned):

```json
{ "items": [ {
  "bot": "cicord", "instance": "polls", "server": "showcase",
  "plugin": { "name": "polls", "version": "0.1.1", "displayName": "Polls", "description": "Quick polls with buttons or a select menu, …",
              "capabilities": ["discord.messages.send", "discord.messages.read", "…", "portal.routes"], "intents": [] },
  "config": { "channels": [], "color": "#5865f2", "confirmVotes": true, "defaultHours": 24,
              "managerRoles": ["mod"], "maxHours": 168 },             // effective config (defaults applied)
  "schema": { … },                                                  // the plugin's config.schema.json as written
  "baseConfig": { "defaultHours": 24, "managerRoles": ["mod"] },    // spec.plugins[].config as written
  "override": null,                                                 // overrides.<server>.config, or null
  "editable": ["channels", "color", "confirmVotes", "defaultHours", "managerRoles", "maxHours"],
  "commands": [ { "name": "poll", "description": "Start a poll" }, { "name": "polls", "description": "Manage polls" } ],
  "routes": ["GET /polls/:id"],                                     // portal routes
  "health": "Healthy", "message": "",
  "source": "cicord",       // "cicord" (official catalog), a registry name, or "local" (written in the repo)
  "trust": "official"       // official | unofficial | local
  // "update": PluginUpdate, when a newer version is available (Store)
}, … ], "total": 7, "offset": 0, "limit": 2, "sort": "name", "order": "asc" }
```

- `schema` is the schema file's JSON with its key order preserved (`{"type": "object"}` when
  the plugin has none). `baseConfig` and `override` are written values, without defaults.
- `editable`: admins may edit every top-level property; moderators only those marked
  `x-cicord-access: moderator` (default `admin`). Sorted.
- `update` (a [`PluginUpdate`](../store/#shapes)) comes from the runtime's cache only (no
  fetch on this endpoint): registry versions are known once the background refresh or a Store
  page has looked them up.

`GET /api/v1/servers/{server}/plugins/{bot}/{instance}` (moderator) returns one item (the
admin console's per-plugin page); unknown: `404`.

## Editing the config

`PUT /api/v1/servers/{server}/plugins/{bot}/{instance}/config` (moderator, field-restricted).

Request: `{"config": {...}, "scope": "server" | "base", "revision": "<active revision id>"}`

- `config` must be a JSON object (`400`); `scope` must be `server` or `base` (`400`); an
  unknown bot or instance on that server answers `404`.
- `scope: "server"` (default) writes the server override (`overrides.<server>.config` in
  `bot.yaml`) as the minimal merge patch from the base config; `"base"` writes `config`.
- `config` is the full desired config **with the fields the user sees** (defaults included):
  a base field left out is removed for that server (`null` in the override).
  For `scope: "server"` the runtime diffs it against the base config with defaults applied and
  writes only the difference (`null` removes a base field; deletions of fields absent from the
  base are dropped); an empty difference removes the override. For `"base"`, fields that only
  restate a schema default and were not written before are dropped. Comments and key order are
  kept.
- **Authorization** is checked on the top-level fields whose effective value changes:
  moderators may only change fields listed in `editable`; any other change answers `403`
  (`"you may not change: a, b"`). `scope: "base"` changes every server the instance runs on:
  the caller must be at least moderator on each of them where the effective config changes,
  and the field restriction applies per server.
- The config is **validated** (schema and `x-cicord-ref`, the same rules as `cicord validate`)
  on every affected server before anything is written: `422` with
  `{"error": {"code": "invalid", …, "diagnostics": [Diagnostic]}}`.
- `revision` (optional) must be the active revision ID, otherwise `409 conflict` (reload and
  retry). A write-back conflict (the branch moved and the patch no longer applies) is also
  `409`.
- An unchanged config writes nothing and returns `{"mode": "commit", "revision": <active>}`.
- `mode` comes from the `cicord.yaml` `writeBack.policies` for the bot file.
  `writeBack.enabled: false` answers `403`; no writer configured, or a failed write-back,
  `503`. Write-backs are serialized.
- In `commit` mode the runtime is triggered at once and the response waits (at most 20
  seconds) for the new revision to be active; `revision` is then the active revision ID. When
  the new revision does not become active in time (or is rejected), `revision` is the ID
  reported by the writer, and the new revision may still be pending or rejected (see `/status`
  `lastError`).
- Every write is audited (`config.update`, target `<bot>/<instance>`, details
  `{scope, fields, mode, revision, pullRequestUrl}`). Commits carry the trailers
  `Cicord-Actor: discord:<id> (<name>)` and `Cicord-Source: admin-ui`.

Response `200`:

```sh
curl -X PUT http://127.0.0.1:7420/api/v1/servers/showcase/plugins/cicord/polls/config \
  -b cookies.txt -H 'X-Cicord-Request: 1' -H 'Content-Type: application/json' \
  -d '{"config": {"defaultHours": 72, "managerRoles": ["mod"]}, "scope": "server", "revision": "local-e43bf5a"}'
```

```json
{ "mode": "commit", "revision": "local-ea0ac66", "pullRequestUrl": "" }
// mode "pullRequest": revision is "" and pullRequestUrl is set; nothing is applied yet.
```

The override written to `bot.yaml`:

```yaml
      overrides:
        showcase:
          config:
            defaultHours: 72
```

## Uninstalling

`DELETE /api/v1/servers/{server}/plugins/{bot}/{instance}` (admin) uninstalls an instance
from the server:

- An instance that also runs on other servers only loses this one (its `servers` list and
  its `overrides.<server>` go): `removed: false`.
- Otherwise it is deleted from `bot.yaml` (`removed: true`). When no other instance uses the
  plugin, its `cicord.lock` entry (registry plugin) or its `plugins/<name>/` directory (copied
  by the Store or the wizard) goes too, listed in `cleaned`. Plugins written in the
  repository elsewhere are never deleted. Plugin data (KV) is kept.
- The change is validated on a copy of the active revision first (`422 invalid` when
  something depends on it). Mode, activation wait and errors are those of a
  [config `PUT`](#editing-the-config). Audit `plugin.uninstall`.
- A custom portal's route types are regenerated in the same change, as for a
  [Store install](../store/#installing); `warnings` says when they could not be.

```json
{ "removed": true, "cleaned": ["cicord.lock: acme/xp"], "mode": "commit", "revision"?: "…", "pullRequestUrl"?: "…", "warnings"?: ["…"] }
```


## Failed scheduled tasks

A server administrator can list up to 100 pending decisions with
`GET /api/v1/servers/{server}/dead-letters`. Each item includes the task ID, plugin scope,
handler name, due time, attempt and failure counts, and a failure category.
`POST /api/v1/servers/{server}/dead-letters/{id}/retry` enables another delivery.
`POST /api/v1/servers/{server}/dead-letters/{id}/discard` deletes the failed task.
Both actions return 204 on success and are audited. A missing or already resolved task
returns 404.
