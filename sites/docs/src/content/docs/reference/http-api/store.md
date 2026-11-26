---
title: "HTTP API: Store"
description: Plugin sources and computed trust, the signed official registry, private registries, browsing, installing exactly the reviewed version, reviewing and applying updates, managing registries, and the registry index format.
---

Part of the [HTTP API reference](../). For the user's view, see the
[Plugins overview](../../../plugins/overview/); for publishing, see
[Publishing](../../../plugins/publishing/).

## Sources

The Store lists the plugins of every **source** and installs them through
[write-back](../../../concepts/write-back/) (apt/Homebrew-style sources,
[ADR-0011](../../../project/decisions/0011-git-registry-and-lockfile/)). There are two kinds of
sources:

- **`cicord`**: the official plugins bundled in the binary (the
  [catalog](../setup/#catalog-and-diagnostics)), merged with the signed official registry
  (below). Trust `official`. Installing a bundled plugin copies its sources to
  `plugins/<name>/` (a local plugin).
- **Registries** declared in `cicord.yaml` `spec.registries` (`{name, url}`): git repositories
  with an `index.yaml`. Trust is **computed, never declared**: `official` only for the official
  registry URL, otherwise `unofficial`. The name `cicord` is reserved. Installing writes a
  `cicord.lock` entry and a `bot.yaml` instance with `source: <registry>/<plugin>`, in one
  write-back.

**The official registry**, `https://github.com/cicord/cicord-plugins`, is implicit: source
`cicord` lists the union of the bundled plugins and its plugins (a plugin in both comes from
the registry only when its index `version` is a semver greater than the bundled one), with
`url` and `signature` set. Its `index.yaml` must carry a valid, current signature by an
official key (`index.yaml.sig`, bound to the registry URL, a serial and an expiry). An
official URL whose signature is missing, invalid, expired, for another registry or older than
one already seen has `signature: "invalid"`, an `error`, and no plugins from it (`cicord` keeps
its bundled plugins). `verified` is only `true` from a verified official index. A `cicord`
plugin from the registry has a `repo`, and its `StoreDetail` a `commit` and `integrity`;
installing it locks `cicord/<name>` in `cicord.lock`. Once `cicord/<name>` is locked, its `StoreDetail` (and an
install of another instance) always comes from the registry, at its latest release, even when
the index does not list a version newer than the bundled one: the page then shows the same
`update` as the table, which compares the lock with the release tags.

**Who**: browsing and installing need the `admin` role on a server (bearer: everywhere).
Adding or removing a source changes `cicord.yaml` (the whole project): bearer, or `admin` on
**every** server the runtime manages.

**Refresh**: the runtime caches registry indexes and refreshes them in the background (10
seconds after start, then every 30 minutes) and on demand. A registry that cannot be fetched
is listed with an `error` and no plugins; the others still work.

**Private registries**: a registry may have a `secretRef` (`cicord.yaml`), always
`CICORD_REGISTRY_<NAME>_TOKEN` for its own name, naming the secret that holds an access token
(an environment variable or the runtime's secret store). The token is sent, as HTTP basic auth
over https, to the registry's host only (its index and the plugin repositories on the same
host), never to other hosts. It is never returned by the API.

## Shapes

```json
// Source
{ "name": "cicord", "kind": "bundled|git", "url"?: "https://…", "trust": "official|unofficial",
  "fetchedAt"?: "…", "error"?: Error, "private": false, "credentials"?: "ok|missing",
  "signature"?: "verified|invalid",                // official URLs only (see above)
  "pluginCount": 6, "plugins": [StorePlugin] }     // plugins omitted with ?summary=1
// StorePlugin (browse; from the catalog or the registry index)
{ "source": "cicord", "name": "welcome", "displayName": "Welcome", "description": "…",
  "keywords": [], "version"?: "0.1.0", "publisher"?: "…", "verified": false, "repo"?: "https://…",
  "trust": "official|unofficial",
  "installed": [{ "server", "bot", "instance" }],         // instances using this source/plugin
  "update"?: PluginUpdate }                               // installed plugins with a newer version
// PluginUpdate
{ "kind": "registry|bundled", "current": "1.0.0", "latest": "1.1.0",
  "level": "major|minor|patch", "modified"?: true }
// StoreDetail = StorePlugin + what the plugin requests, read from its plugin.yaml at the
// resolved version (not from the index):
{ …StorePlugin, "version": "1.2.0", "commit"?: "<sha>", "integrity"?: "sha256-…",
  "capabilities": [], "intents": [], "privilegedIntents": [], "network": [],
  "schema": {…}, "commands": [{"name", "description"}] | null }
```

`commands` is `null` when unknown: listing commands evaluates the plugin's code, which is only
done for `official` sources before installation.

**Updates.** A plugin version is shared by every instance using it, so an update changes all
of them at once:

- `kind: "registry"`: the `cicord.lock` version of `<registry>/<plugin>` (including
  `cicord/<plugin>` locked from the official registry) is below the version an update
  resolves: the highest release of the plugin repository, **any major version** (prereleases
  ignored, like resolving `*`), whose `plugin.yaml` `spec.engine` supports the running Cicord,
  and never above the version the signed official index publishes. The lookup lists the
  repository's tags and reads `plugin.yaml` at the newest candidates (shallow, no package
  files), with the registry's token and the public-only rule like every Store fetch, and is
  cached with the index TTL: the background refresh and `POST /store/refresh` refetch it,
  browsing the Store fetches what is missing or stale, instance items never fetch. A badge
  therefore never offers a release this Cicord would refuse.
- `kind: "bundled"`: a copy of an official plugin in `plugins/<name>/` whose `plugin.yaml`
  version is older than the version bundled in the binary. `modified: true` when the copy is
  neither the content of a released version of that plugin (the binary records the integrity
  of every bundled version) nor the current version with only another `metadata.version`:
  updating would overwrite local changes and must be forced.

## Endpoints

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/store[?summary=1]` | admin of a server | `{"sources": [Source]}`, `cicord` first, then the registries in `cicord.yaml` order. |
| GET | `/api/v1/store/plugins` | admin of a server | *[Paged](../#data-tables)* `{"items": [StorePlugin], …}` across sources; sort `name` (default), `source`, `trust`, `version`, `publisher`, `installed`; filters `source`, `trust`, `installed=true\|false`, `updates=true\|false`; default limit 50. |
| POST | `/api/v1/store/refresh` | admin of a server | Refetches every registry index; answers as `GET /store`. |
| GET | `/api/v1/store/{source}/{plugin}` | admin of a server | `StoreDetail` for the latest version (`404` unknown source or plugin, `502 registry_unavailable`). |
| GET | `/api/v1/store/{source}/{plugin}/update` | admin of a server | [`UpdateReview`](#updating-a-plugin) (`404 not_installed`, `502 registry_unavailable`). |
| POST | `/api/v1/store/{source}/{plugin}/update` | admin of **every** server running it | [`UpdateResult`](#updating-a-plugin). |
| POST | `/api/v1/servers/{server}/store/install` | admin of `{server}` | [`InstallResult`](#installing). |
| POST | `/api/v1/registries` | project (see above) | [`WriteResult`](#registries). |
| DELETE | `/api/v1/registries/{name}` | project | `WriteResult`. |
| PUT | `/api/v1/registries/{name}/token` | project | `WriteResult` (body `{"token"}`). |
| DELETE | `/api/v1/registries/{name}/token` | project | `WriteResult`. |

## Installing

`POST /api/v1/servers/{server}/store/install` request:

```json
{ "source": "acme", "plugin": "xp", "bot"?: "moderator", "instance"?: "xp", "config"?: {…},
  "version"?: "1.2.0", "commit"?: "<sha>", "integrity"?: "sha256-…", "confirmUnofficial"?: false,
  "confirmPrivileged"?: false }
```

- `bot` defaults to the only bot managing the server (`400` when several do). `instance`
  defaults to the plugin name (`409 conflict` when it is taken on that bot).
- Registry plugins: `version`, `commit` and `integrity` are the values of the reviewed
  `StoreDetail` and are required. If the latest version now resolves differently:
  `409 changed` (review again). The installed code is always the reviewed code.
- `cicord` plugins: send `version`, `commit` and `integrity` when the reviewed `StoreDetail`
  has a `commit` (it comes from the official registry), none for a bundled plugin. When the
  origin changed since the review (bundled ↔ registry): `409 changed`. No `confirmUnofficial`
  is needed.
- An `unofficial` source without `confirmUnofficial: true`: `409 confirmation_required`.
- A plugin that requests `server.overlays`, `server.incident` or `server.sync` needs
  `confirmPrivileged: true` (`409 confirmation_required`), for every source.
- Every capability the plugin requests is granted (`grants`), as reviewed. Privileged intents
  are checked against the application, as in the [setup wizard](../setup/#wizard-endpoints)
  (`409 intents`). The configuration is validated on a copy of the active revision before
  anything is written (`422 invalid` with diagnostics).
- Mode, activation wait and response are those of the wizard's `POST /plugins/install`
  (`InstallResult = {"instance", "health", "message"?, "command"?, "revision"?, "mode",
  "pullRequestUrl"?, "warnings"?}`). Audit `store.install` (target `<bot>/<instance>`, details
  `{source, plugin, version, commit, trust, mode}`).
- When the repository's custom portal has typed routes (`<portal>/src/cicord-routes.d.ts`),
  the same commit or pull request regenerates them with the code of `cicord portal typegen`
  (its path counts for the [write-back mode](../../../concepts/write-back/#policies)). When
  they cannot be generated, the change is still written and `warnings` says so (also in the
  commit message and the pull request body). Install, update and uninstall alike.

## Updating a plugin

`GET /store/{source}/{plugin}/update` reviews the latest version against what is installed. It
never evaluates the new version's code (its config schema and `plugin.yaml` only):

```json
// UpdateReview
{ "available": true, "update": PluginUpdate | null,
  "detail": StoreDetail,                     // the new version: send its version/commit/integrity back
  "current": { "version": "1.0.0", "commit"?: "…", "integrity"?: "…", "repo"?: "…",
               "capabilities": [], "intents": [], "privilegedIntents": [], "network": [] },
  "diff": { "capabilities": {"added": [], "removed": []},
            "madeOptional": [{ "name": "discord.roles.assign", "features": ["defaultRole"] }],
            "intents": {…}, "intentsMadeOptional": [], "privilegedIntents": {…}, "network": {…} },
  "instances": [{ "server", "bot", "instance", "range"?: "^1.0.0", "newRange"?: "^2.0.0",
                  "grantsAdded": [], "grantsRemoved": [],
                  "grantsKept": [{ "name": "discord.roles.assign", "features": ["defaultRole"] }],
                  "diagnostics": [Diagnostic] }],
  "files"?: { "changed": [], "added": [], "removed": [] },   // bundled copies (plugin-relative)
  "modified": false, "repoChanged": false, "servers": ["my-community"], "configValid": true,
  "missingAdmin": [], "restricted": [{ "server", "instances": 2 }] }
```

- `instances` has one entry per instance and server. `diagnostics`: the instance's current
  config (base ⊕ server override, with the defaults of the **new** schema) validated against
  the new config schema and `x-cicord-ref`. An incompatible settings change shows up here
  before anything is written (`configValid: false`).
- `newRange` is written when the new version is outside the instance's `version` range
  (`^<new>`). `grantsAdded` are the capabilities the new version requires that the instance
  does not grant yet. `grantsRemoved` are the grants the new version no longer declares at all.
  `grantsKept` are the grants kept only because an optional feature of the new version declares
  them. Every other grant stays.
- `diff.capabilities.added` is what the new version requires and the installed one did not;
  `removed` is what it no longer declares, required or optional. `madeOptional` lists what the
  installed version required and the new one only declares in optional features. `intents`
  and `intentsMadeOptional` work the same way for intents.
- `repoChanged`: the registry index now points to another repository than `cicord.lock`.
- `missingAdmin`: servers running the plugin where the caller is not admin. Their instances
  are **not** detailed (no config diagnostics, grants or names, and they are left out of
  `detail.installed`): `restricted` only gives how many instances run there.

`POST /store/{source}/{plugin}/update` request:

```json
{ "version": "1.1.0", "commit"?: "<sha>", "integrity"?: "sha256-…",
  "confirmUnofficial"?: false, "confirmPrivileged"?: false, "confirmSecretChanges"?: "<review digest>",
  "force"?: false }
```

- Needs the `admin` role on **every** server where an instance of the plugin runs (bearer:
  everywhere), otherwise `403 forbidden` naming the missing servers. Write-back disabled:
  `403`.
- Registry plugins (`cicord/<plugin>` locked from the official registry included): `version`,
  `commit` and `integrity` of the reviewed `detail` are required (`400`); when the latest
  version now resolves differently: `409 changed`. Bundled copies: `version` (and optionally
  `integrity`) of the reviewed `detail`.
- The review lists each bound credential whose destination hosts or daily provider budget change under `instances[].secretChanges`. If an update expands that access, GET also returns `secretReview`. Supply the exact digest in `confirmSecretChanges` when approving the update. A missing or stale digest returns `409 confirmation_required`. The CLI prints the corresponding `--approve-secret-changes` argument.
- An update that adds `server.overlays`, `server.incident` or `server.sync` needs
  `confirmPrivileged: true` (`409 confirmation_required`).
- `unofficial` sources need `confirmUnofficial: true` (`409 confirmation_required`). Already
  the latest: `409 up_to_date`. A modified bundled copy without `force: true`:
  `409 modified` (`details` lists the changed and deleted files).
- One write-back: the `cicord.lock` entry (refused when it changed meanwhile), every
  instance's `version` range when needed and `grants`; for bundled copies, the files of
  `plugins/<name>/` (files the new version no longer ships are deleted). Privileged intents
  are checked for every bot running it (`409 intents`); the change is validated on a copy of
  the active revision, plugins built (`422 invalid` with `diagnostics`).
- Mode and activation wait as an install. Audit `plugin.update` on each server (target
  `<source>/<plugin>`, details `{from, to, commit, trust, capabilitiesAdded,
  capabilitiesRemoved, forced, mode, revision, pullRequestUrl}`).

```json
// UpdateResult
{ "version": "1.1.0", "mode": "commit|pullRequest", "revision"?: "…", "pullRequestUrl"?: "…",
  "message"?: "…", "instances": [{ "server", "bot", "instance", "health", "message"? }],
  "warnings"?: ["…"] }
```

The CLI equivalent is `cicord plugin update [<registry>/<plugin>] [--to <range>] [--force]`:
without an argument it lists the available updates; with a plugin it updates `cicord.lock`,
the ranges and grants of `bot.yaml` (or `plugins/<name>/`) locally and prints the capability
diff.

## Registries

`POST /registries` request:
`{"name": "acme", "url": "https://github.com/acme/cicord-plugins.git", "token"?: "…"}`

- `token` makes it a private registry: stored in the secret store as
  `CICORD_REGISTRY_<NAME>_TOKEN`, with `secretRef` written to `cicord.yaml` (`503` when the
  runtime has no secret store). URLs with credentials (`https://user:pass@…`): `400`.
- `name` matches `^[a-z0-9][a-z0-9-]{0,31}$`, is not `cicord`, and is unique; `url` is
  `https://` only and unique (the CLI also accepts `file://` and local paths). Otherwise
  `400 invalid`.
- The index is fetched first; unreachable or invalid: `422 invalid` (nothing written).
- Writes `cicord.yaml` (`spec.registries`); audit `registry.add`.

`DELETE /registries/{name}`: unknown: `404`; used by `cicord.lock` or a bot plugin:
`409 in_use` (`details` lists them). Its stored token is deleted. Audit `registry.remove`.

`PUT /registries/{name}/token` `{"token"}` checks the index with the new token first
(`422 invalid` when it cannot be read), stores it, and writes `secretRef` to `cicord.yaml` when
it was not there (otherwise nothing is committed: `{"mode": "commit"}`).
`DELETE /registries/{name}/token` removes `secretRef` from `cicord.yaml` and deletes the token.
Both only ever touch `CICORD_REGISTRY_<NAME>_TOKEN` of that registry. Audit `registry.token`.

```json
// WriteResult
{ "mode": "commit|pullRequest", "revision"?: "…", "pullRequestUrl"?: "…" }
```

## Registry index

`index.yaml` at the root of the registry repository:

```yaml
plugins:
  xp:
    repo: https://github.com/acme/cicord-xp.git    # plugin repository, semver tags v1.2.0
    # path: plugins/xp                           # monorepo: plugin directory, tags xp/v1.2.0
    displayName: XP & levels                     # optional
    description: Levels and a portal leaderboard
    keywords: [xp, levels]
    publisher: acme
    verified: false                              # only honoured from the signed official index
    version: 1.2.0                               # optional: latest release (cicord plugin publish)
    commit: 3f1c…                                # optional: its tag's commit (a moved tag is refused)
```

The official registry also has `index.yaml.sig`, the signature envelope (`format`,
`registry`, `serial`, `expires`, `index-sha256`, then `ed25519 <key-id> <base64>` lines),
written by `cicord registry sign`. See
[Publishing → the official registry](../../../plugins/publishing/#the-official-registry) and
[Registries](../../../plugins/publishing/) for the rules on `path`, tags and signatures.
