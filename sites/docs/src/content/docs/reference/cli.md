---
title: CLI reference
description: Every cicord command and subcommand with its real flags, generated from cicord --help.
tableOfContents:
  maxHeadingLevel: 3
---

`cicord` is both the command line tool and the runtime. This page lists every command with
the flags of `cicord <command> --help` (version 0.1.0); `--help` stays authoritative. Every
command also accepts `-h, --help`.

| Command | Purpose |
|---|---|
| [`cicord init`](#cicord-init) | Scaffold a repository. |
| [`cicord validate`](#cicord-validate) | Schema and semantic validation, offline. |
| [`cicord import`](#cicord-import) | Generate `servers/<key>.yaml` from an existing Discord server. |
| [`cicord mv`](#cicord-mv) | Rename a resource key without recreating the resource. |
| [`cicord plan`](#cicord-plan) | Show what a sync would change, locally or through the runtime. |
| [`cicord apply`](#cicord-apply) | Reconcile directly (development, bootstrap). |
| [`cicord adopt`](#cicord-adopt) | Rebuild the local resource map by display name. |
| [`cicord serve`](#cicord-serve) | Run the runtime (production). |
| [`cicord dev`](#cicord-dev) | Run the runtime locally with hot reload, on real Discord or the simulator. |
| [`cicord status`](#cicord-status) | Revision, sync and health of a runtime; wait for a revision. |
| [`cicord sync`](#cicord-sync) | Ask the runtime to fetch and sync now. |
| [`cicord healthcheck`](#cicord-healthcheck) | Exit 0 when a runtime answers `/healthz` (container health checks). |
| [`cicord version`](#cicord-version) | Print the version. |
| [`cicord secret`](#secrets) | `ls`, `set`, `rm`, `rotate-key`: the encrypted secret store. |
| [`cicord plugin`](#plugins) | `new`, `sdk-types`, `build`, `typegen`, `test`, `add`, `update`, `publish`. |
| [`cicord registry`](#registries) | `list`, `add`, `remove`, `update`, `search`, `keygen`, `sign`, `verify`. |
| [`cicord portal`](#portal) | `init`, `build`, `typegen`: a custom community portal. |
| `cicord completion` | Generate the shell completion script (`bash`, `zsh`, `fish`, `powershell`). |

Commands that talk to a runtime (`plan --remote`, `status`, `sync`) read its URL from
`--remote=URL` or `CICORD_RUNTIME_URL` (https, or http on localhost) and the bearer token from
`CICORD_RUNTIME_TOKEN`; never from `cicord.yaml`. Commands that work on a repository take the
directory as an optional argument (default `.`).

## Repository

### `cicord init`

Scaffold a new Cicord repository: `cicord.yaml`, `servers/<server>.yaml`, `bots/<bot>/bot.yaml`, `.github/workflows/cicord.yml`, `README.md` and `.gitignore`. Without IDs, the placeholders work with `cicord dev --sim`; replace them with your real IDs before `cicord plan`.

```text
cicord init [dir] [flags]
```

| Flag | Description |
|---|---|
| `--application-id` `string` | Discord application ID of the bot (default: the placeholder "100000000000000002", which works with cicord dev --sim) |
| `--server-id` `string` | Discord server ID (default: the placeholder "100000000000000001", which works with cicord dev --sim) |
| `--bot` `string` | bot key (default "bot") |
| `--force` | overwrite existing files |
| `--project` `string` | project key (default: directory name) |
| `--provider` `string` | source provider: github \| git \| embedded (default: inferred from --repo, none for local-only repos) |
| `--repo` `string` | repository URL (github / git providers) |
| `--server` `string` | server key (default: project key) |

```sh
cicord init my-community --server-id 112233445566778899 --application-id 998877665544332211
cicord init . --server my-server --bot helper
```

### `cicord validate`

Validate a repository offline: JSON Schemas, then semantic checks (references, unique keys, plugin configs against their schemas after overrides, grants, intents, `cicord.lock`, engine range, command names). Exits 1 on errors; warnings (privileged intents, probable renames, `prune` without deletion protection…) are printed.

```text
cicord validate [dir] [flags]
```

| Flag | Description |
|---|---|
| `-o, --output` `string` | output format: text \| json (default "text") |

### `cicord import`

Connect to Discord with a bot token, read the live server (roles with their hierarchy and permissions, `@everyone`, categories, channels with their overwrites, positions, topics and slowmode, the profile, AutoMod, onboarding, widget, welcome screen) and write `servers/<key>.yaml`. The server icon, banner and invite splash, role icons, emojis, stickers and soundboard sounds are downloaded into `servers/assets/<key>/`. Keys are derived from the display names; integration roles are skipped. The resource map is written to the local state database, so a later `cicord apply` adopts the existing resources. The bot must be a member of the server; add the server key to a bot's `spec.servers` afterwards.

```text
cicord import <server-id> [dir] [flags]
```

| Flag | Description |
|---|---|
| `--force` | overwrite an existing servers/&lt;key&gt;.yaml and its resource map |
| `--key` `string` | server key (default: derived from the server name) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.cicord/state.db) |
| `--token-env` `string` | environment variable holding the bot token (default "DISCORD_TOKEN") |

```sh
DISCORD_TOKEN=… cicord import 112233445566778899
cicord import 112233445566778899 ./infra --key ma-commu --token-env DISCORD_TOKEN_MODERATOR
```

### `cicord mv`

Rename the key of a role, category or channel: the server YAML (the key and every reference: channel categories, hierarchy, access roles, overwrite targets, profile channels), the plugin configurations that reference it through `x-cicord-ref`, and the local resource map. Comments and formatting are kept. A runtime moves its own mapping when the renamed file reaches it (plan: `adopt` + `move … mapping`), so the Discord resource is kept. Rename the key first, then the display name, in separate commits. When a plugin instance runs on several servers and its base config references the key, the command stops (add an override first, or `--force`).

```text
cicord mv <server> <role|category|channel> <old-key> <new-key> [dir] [flags]
```

| Flag | Description |
|---|---|
| `--dry-run` | show the changes without writing anything |
| `--force` | rename even when a shared plugin base config references the key (it is left unchanged) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.cicord/state.db) |

```sh
cicord mv ma-commu channel general discussions
cicord mv ma-commu role modo moderators --dry-run
```

## Plan and apply

### `cicord plan`

Compute the diff between the repository and the live servers; nothing is applied.

- **Remote** (CI): `--remote` uploads the repository (`.tar.gz`, without `.git`, `node_modules`, `.cicord`, `dist`, `.env` files and the root `.gitignore` patterns) to the runtime, which validates and plans it with its own state and bots (`POST /api/v1/plan`). The URL is `--remote=URL`, else `CICORD_RUNTIME_URL` (https, or http on localhost); the token is `CICORD_RUNTIME_TOKEN`. `cicord.yaml` `spec.runtime.url` is never used to send the token.
- **Local**: without `--remote`, the repository is planned against Discord directly with the bot tokens from the environment and the local state database (read only).

`--comment` posts the plan on the pull request in GitHub Actions (`GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `GITHUB_EVENT_PATH`; `GITHUB_API_URL` for GitHub Enterprise), updating the previous Cicord comment. Symbols: `+` create, `~` update, `-` delete, `>` reorder, `=` adopt, `#` stop managing, `!` blocked. Exit codes: 0 success, 1 error or invalid configuration, 2 changes pending (only with `--detailed-exitcode`).

```text
cicord plan [dir] [flags]
```

| Flag | Description |
|---|---|
| `--comment` | post/update the plan as a pull-request comment (GitHub Actions) |
| `--detailed-exitcode` | exit 2 when changes are pending |
| `-o, --output` `string` | output format: text \| json (default "text") |
| `--pr` `int` | pull request number for --comment (default: from GITHUB_EVENT_PATH) |
| `--remote` `string[="auto"]` | runtime URL (--remote=URL); without a value: CICORD_RUNTIME_URL (https, or http on localhost) |
| `--server` `strings` | only these server keys (repeatable) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.cicord/state.db) |

```sh
cicord plan                                   # local, bot tokens from the environment
cicord plan --remote                          # runtime from CICORD_RUNTIME_URL
cicord plan --remote=https://cicord.example.com --comment
cicord plan --remote -o json
cicord plan --detailed-exitcode; echo $?      # 0 no changes, 1 error, 2 changes pending
```

### `cicord apply`

Validate, plan every server against Discord with the bot tokens from the environment, show the plan, ask for confirmation, apply it and record the created and adopted resources in the local state database. For development and bootstrapping: in production, the runtime applies merged changes with its own state. Without a terminal, `--yes` is required.

```text
cicord apply [dir] [flags]
```

| Flag | Description |
|---|---|
| `-o, --output` `string` | output format of the result: text \| json (default "text") |
| `--server` `strings` | only these server keys (repeatable) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.cicord/state.db) |
| `-y, --yes` | apply without asking for confirmation |

```sh
DISCORD_TOKEN_MODERATOR=… cicord apply
cicord apply --server ma-commu --yes
cicord apply ./infra --state /var/lib/cicord/state.db
```

### `cicord adopt`

Rebuild the local resource map after it was lost or became stale, by matching each declared role, category and channel with an existing Discord resource of the same kind and display name. Valid mappings are kept, mappings to deleted resources dropped. The matches are shown and must be confirmed. Nothing is changed in Discord; keys without a match are created by the next `cicord apply`.

```text
cicord adopt [dir] [flags]
```

| Flag | Description |
|---|---|
| `--reset` | ignore existing mappings and match every key by name |
| `--server` `strings` | only these server keys (repeatable) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.cicord/state.db) |
| `-y, --yes` | write the mappings without asking for confirmation |

```sh
cicord adopt
cicord adopt --server ma-commu --reset --yes
```

## Runtime

### `cicord serve`

Run the runtime: follow the configured source (`cicord.yaml` `spec.source`), reconcile servers, run the bots and their plugins, and serve the HTTP API, the admin console (`/admin/`) and the portal (`/`). In a directory without `cicord.yaml`, it starts the setup wizard. `--data-dir` defaults to `.cicord-data`. The state store is SQLite in the data directory, or PostgreSQL with `--database-url` / `CICORD_DATABASE_URL` (the data directory is still needed for its keys; see [Deploy](../../self-hosting/deploy/#state-store-sqlite-or-postgresql)). Environment variables: see [Deploy](../../self-hosting/deploy/#environment-variables).

```text
cicord serve [dir] [flags]
```

| Flag | Description |
|---|---|
| `--admin-url` `string` | URL of the admin console on a separate (private) hostname, e.g. a VPN; the admin UI and admin API then only answer on that host (env CICORD_ADMIN_URL) |
| `--cors-origin` `strings` | external portal origin allowed with credentials (repeatable) |
| `--data-dir` `string` | runtime state directory (database, caches, keys) |
| `--database-url` `string` | PostgreSQL connection URL of the state store (default: $CICORD_DATABASE_URL; unset: SQLite in the data dir) |
| `--github-app-key-file` `string` | GitHub App private key file (PEM), instead of the CICORD_GITHUB_APP_PRIVATE_KEY secret (env CICORD_GITHUB_APP_PRIVATE_KEY_FILE) |
| `--listen` `string` | HTTP listen address (default ":7420") |
| `--local` | serve the directory as-is, ignoring spec.source (no git) |
| `--log-level` `string` | log level: debug \| info \| warn \| error (default "info") |
| `--metrics-listen` `string` | serve Prometheus metrics on GET /metrics of this address, without authentication, e.g. 127.0.0.1:9420 (default: disabled; env CICORD_METRICS_LISTEN) |
| `--public-url` `string` | public URL of the runtime (OAuth redirects, portal links); default http://&lt;listen&gt; |

`--public-url` (env `CICORD_PUBLIC_URL`) is the URL people reach: OAuth redirects, portal links sent by the bots, the CSRF origin check. Without it, `serve` uses `spec.runtime.url` of `cicord.yaml`, then the URL the setup wizard ran on, then `http://localhost:<port>`. `--admin-url` (env `CICORD_ADMIN_URL`, `https://host[:port]` without a path; `http` only on a loopback host) serves the admin console on a second hostname: a request whose `Host` is that hostname logs in with `<admin-url>/auth/callback`, every other host with `<public-url>/auth/callback`, and the admin UI, the admin API and the setup wizard answer 404 on every host but the admin one. The setup link, the startup banner and the GitHub commit status link use the admin URL; portal links keep the public URL. See [Admin on a private network, portal public](../../self-hosting/security/#admin-on-a-private-network-portal-public).

`--metrics-listen` (env `CICORD_METRICS_LISTEN`) opens a second listener that only answers `GET /metrics` (Prometheus text format), without authentication: bind it to a loopback address such as `127.0.0.1:9420`. It is off by default, and the API listener answers `404` on `/metrics`. See [Monitoring](../../self-hosting/monitoring/).

### `cicord dev`

Run the runtime on a local working directory: every saved change becomes a new revision, admin console edits are written back to the files, and `POST /auth/dev-login` signs in as any user. With `--sim`, Discord is replaced by the in-memory simulator, seeded from the repository (servers, bots invited with Administrator, an `owner` and a few users). Without `--sim`, bot tokens come from the environment, then the secret store. In a directory without `cicord.yaml`, it starts the setup wizard; `--setup` makes that explicit. The data directory defaults to `<dir>/.cicord/dev`; `--database-url` / `CICORD_DATABASE_URL` use PostgreSQL for the state, except with `--sim`, whose state always stays in memory. **Never expose it on a network.**

```text
cicord dev [dir] [flags]
```

| Flag | Description |
|---|---|
| `--cors-origin` `strings` | external portal origin allowed with credentials (repeatable) |
| `--data-dir` `string` | runtime state directory (database, caches, keys) |
| `--database-url` `string` | PostgreSQL connection URL of the state store (default: $CICORD_DATABASE_URL; unset: SQLite in the data dir) |
| `--listen` `string` | HTTP listen address (default "127.0.0.1:7420") |
| `--log-level` `string` | log level: debug \| info \| warn \| error (default "info") |
| `--public-url` `string` | public URL of the runtime (OAuth redirects, portal links); default http://&lt;listen&gt; |
| `--setup` | start the setup wizard (the directory must not contain cicord.yaml) |
| `--sim` | use the Discord simulator instead of real Discord |

### `cicord status`

Show the status of a runtime (`GET /api/v1/status`): the active revision, every server (sync, health, drift) and every bot with its plugin instances. Runtime URL and token as for `cicord plan --remote`. `--wait` polls until `--revision` (a SHA or prefix; default: the active one) is active and every server is `Synced` and `Healthy`; it fails when the revision is rejected, a server is `Degraded` or the timeout expires. A server with blocked operations stays `OutOfSync`, so `--wait` times out on it and prints the drift.

```text
cicord status [dir] [flags]
```

| Flag | Description |
|---|---|
| `--interval` `duration` | polling interval (with --wait; default 4s) |
| `-o, --output` `string` | output format: text \| json (default "text") |
| `--remote` `string[="auto"]` | runtime URL (default: CICORD_RUNTIME_URL; https, or http on localhost) |
| `--revision` `string` | revision to wait for: a git SHA or a prefix of it (with --wait) |
| `--timeout` `duration` | maximum time to wait (with --wait) (default 5m0s) |
| `--wait` | wait until the revision is active and every server is Synced and Healthy |

```sh
cicord status
cicord status --remote=https://cicord.example.com -o json
cicord status --wait --revision $GITHUB_SHA --timeout 10m
```

### `cicord sync`

Ask the runtime to fetch its source and activate the new revision, if any (`POST /api/v1/sync`). Use it after a push when no webhook is configured. `--wait` then waits like `cicord status --wait`; pass `--revision` for an exact wait.

```text
cicord sync [dir] [flags]
```

| Flag | Description |
|---|---|
| `--interval` `duration` | polling interval (with --wait; default 4s) |
| `-o, --output` `string` | output format of the final status: text \| json (default "text") |
| `--remote` `string[="auto"]` | runtime URL (default: CICORD_RUNTIME_URL; https, or http on localhost) |
| `--revision` `string` | revision to wait for: a git SHA or a prefix of it (with --wait) |
| `--timeout` `duration` | maximum time to wait (with --wait) (default 5m0s) |
| `--wait` | wait until the revision is active and every server is Synced and Healthy |

```sh
cicord sync
cicord sync --wait --revision $(git rev-parse HEAD)
```

### `cicord healthcheck`

Send `GET` to the health URL of a running runtime and exit 0 on HTTP 200, 1 otherwise (connection error, timeout or any other status, with a one-line message on stderr). Made for container health checks: the distroless image has no `curl`.

```text
cicord healthcheck [flags]
```

| Flag | Description |
|---|---|
| `--timeout` `duration` | maximum time for the request (default 3s) |
| `--url` `string` | health URL to check (default "http://127.0.0.1:7420/healthz") |

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s CMD ["/usr/local/bin/cicord", "healthcheck"]
```

### `cicord version`

Print the version and the commit the binary was built from, e.g. `cicord 0.1.0 (dee0768)`.

```text
cicord version [flags]
```

## Secrets

Manage the encrypted secret store of a data directory (bot tokens, `CICORD_DISCORD_CLIENT_SECRET`, `CICORD_GIT_TOKEN`, the `CICORD_GITHUB_APP_*` secrets, `CICORD_WEBHOOK_SECRET`, `CICORD_RUNTIME_TOKEN`, registry tokens). Environment variables always take precedence over stored secrets. Values are read from stdin and never printed; the runtime picks a changed bot token, webhook secret or runtime token up on its next restart. Every `cicord secret` command takes `--data-dir` (default `.cicord-data`, where `secret.key` lives) and, for a runtime on PostgreSQL, `--database-url` (default `CICORD_DATABASE_URL`). See [Backups and secrets](../../self-hosting/backups-and-secrets/#the-secret-store).

### `cicord secret ls`

List the stored secrets (names only).

```text
cicord secret ls [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (secret.key; cicord.db unless PostgreSQL is used) (default ".cicord-data") (inherited) |
| `--database-url` `string` | PostgreSQL connection URL of the state store (default: $CICORD_DATABASE_URL; unset: SQLite in the data dir) (inherited) |

### `cicord secret set`

Store or rotate a secret; the value is read from stdin: one line, or a whole PEM block (a GitHub App private key, from its `-----BEGIN` line through its `-----END` line).

```text
cicord secret set NAME [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (secret.key; cicord.db unless PostgreSQL is used) (default ".cicord-data") (inherited) |
| `--database-url` `string` | PostgreSQL connection URL of the state store (default: $CICORD_DATABASE_URL; unset: SQLite in the data dir) (inherited) |

```sh
cicord secret set DISCORD_TOKEN_CICORD --data-dir /data   # paste the token, then Enter
cicord secret set CICORD_GITHUB_APP_PRIVATE_KEY --data-dir /data < app.private-key.pem
```

### `cicord secret rm`

Delete a stored secret.

```text
cicord secret rm NAME [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (secret.key; cicord.db unless PostgreSQL is used) (default ".cicord-data") (inherited) |
| `--database-url` `string` | PostgreSQL connection URL of the state store (default: $CICORD_DATABASE_URL; unset: SQLite in the data dir) (inherited) |

### `cicord secret rotate-key`

Re-encrypt every secret with a new key (key file only, not `CICORD_SECRET_KEY`).

```text
cicord secret rotate-key [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (secret.key; cicord.db unless PostgreSQL is used) (default ".cicord-data") (inherited) |
| `--database-url` `string` | PostgreSQL connection URL of the state store (default: $CICORD_DATABASE_URL; unset: SQLite in the data dir) (inherited) |

## Plugins

### `cicord plugin new`

Scaffold a TypeScript plugin: `plugin.yaml`, `config.schema.json`, `tsconfig.json`, `src/`, the SDK types, and a unit-test setup with the vendored `@cicord/sdk`. Prints the `bot.yaml` snippet to enable it. See [Writing a plugin](../../plugins/writing/).

```text
cicord plugin new <dir> [flags]
```

| Flag | Description |
|---|---|
| `--force` | overwrite existing files |
| `--from-npm` | depend on @cicord/sdk from the npm registry instead of vendoring the embedded tarball |

### `cicord plugin sdk-types`

Write the `@cicord/sdk` type definitions into `<plugin-dir>/.cicord` for editors and `tsc`.

```text
cicord plugin sdk-types <plugin-dir> [flags]
```

### `cicord plugin build`

Bundle the plugin with esbuild (no Node.js needed), load it once to extract its static definition (commands, events, routes…) and check it against `plugin.yaml` like the runtime does: handlers that need an intent missing from `spec.intents`, schedules, routes or subscriptions without their capability, `http.fetch` without `spec.network.allow`. Exit 1 when the plugin would be rejected.

```text
cicord plugin build <plugin-dir> [flags]
```

| Flag | Description |
|---|---|
| `--out` `string` | also write the bundle to this file |
| `-o, --output` `string` | output format: text \| json (default "text") |

```sh
cicord plugin build plugins/welcome
cicord plugin build plugins/welcome -o json
cicord plugin build plugins/welcome --out dist/welcome.js
```

### `cicord plugin typegen`

Generate a module augmentation of `@cicord/sdk`'s `PluginConfig` from the plugin's config schema, so `ctx.config` is typed. Fields that are required or have a default the runtime applies are non-optional; `x-cicord-ref` fields are keys (strings); enums become literal unions.

```text
cicord plugin typegen <plugin-dir> [flags]
```

| Flag | Description |
|---|---|
| `--check` | fail when the file is out of date instead of writing it |
| `--out` `string` | output file (default &lt;plugin-dir&gt;/src/cicord-env.d.ts) |

```sh
cicord plugin typegen plugins/welcome
cicord plugin typegen plugins/welcome --check
```

### `cicord plugin test`

Build the plugin and deploy it in a throw-away repository on the Discord simulator with the real runtime (a test server with the resources its config references, a bot granted every requested capability, one instance). Then, unless `--script=false`, a member joins (when the plugin handles `memberJoin`) and every slash command without required options is invoked once. Fails when the plugin does not build, its config does not validate, the instance does not start or an interaction fails. Required `x-cicord-ref` fields without a value are filled with test resources.

```text
cicord plugin test <plugin-dir> [flags]
```

| Flag | Description |
|---|---|
| `--config` `string` | plugin config file (YAML or JSON) |
| `--script` | run the scripted simulation after the start (default true) |
| `--timeout` `duration` | overall timeout (default 1m0s) |
| `-v, --verbose` | show the runtime and plugin logs |

```sh
cicord plugin test plugins/welcome
cicord plugin test plugins/onboarding --config plugins/onboarding/test/config.yaml
```

### `cicord plugin add`

Add a registry plugin to a bot and lock it in `cicord.lock`. The registry is one declared in `cicord.yaml` `spec.registries`, or `cicord`, the [official registry](../../plugins/publishing/#the-official-registry). The bot gets `source`, a `version` range (`^<version>` without `@<range>`) and `grants` set to the capabilities the plugin requests; review them before committing.

```text
cicord plugin add <registry>/<plugin>[@<range>] [flags]
```

| Flag | Description |
|---|---|
| `--bot` `string` | bot key |
| `--dir` `string` | repository root (default ".") |
| `--name` `string` | instance name (default: plugin name) |

```sh
cicord plugin add acme/hello --bot moderator
cicord plugin add acme/xp@^1.2.0 --bot moderator --name xp
cicord plugin add cicord/welcome --bot moderator     # from github.com/cicord/cicord-plugins
```

### `cicord plugin update`

Without argument, list the plugins with a newer version: registry plugins (`cicord.lock`) and copies of the official plugins (`plugins/<name>/`). With a plugin, update every instance using it: the lock entry, each instance's version range when the new version is outside it, and the grants (set to what the new version requests). Copies of official plugins (`cicord/<name>` instances using `plugins/<name>/`) are rewritten from the plugins bundled in this binary; a locally modified copy only with `--force`. An official plugin locked in `cicord.lock` (installed from the official registry) updates from the registry like any registry plugin. Review the printed capability changes before committing.

```text
cicord plugin update [<registry>/<plugin>] [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") |
| `--force` | overwrite a locally modified copy of an official plugin |
| `--to` `string` | version range to update to (default: the latest version, any major); written to every instance |

```sh
cicord plugin update
cicord plugin update acme/xp
cicord plugin update acme/xp --to ^2.0.0
cicord plugin update cicord/welcome
```

### `cicord plugin publish`

Release a plugin from its git repository: validate it like `cicord plugin build`, check the working tree is clean and that `metadata.version` is a semver greater than every earlier release tag, create the annotated tag `v<version>` (pushed with `--push`), then print the registry index entry or, with `--registry <dir>`, write it to a local registry checkout. Never sets `verified`. In a repository with several plugins (the plugin directory is not the repository root, or `--path` names it), the tag is `<name>/v<version>`, only the plugin directory must be clean, and the entry gets `path`. See [Publishing](../../plugins/publishing/).

```text
cicord plugin publish [plugin-dir] [flags]
```

| Flag | Description |
|---|---|
| `--dry-run` | validate and print what would be done, change nothing |
| `--path` `string` | plugin directory relative to the repository root, for repositories with several plugins (default: [plugin-dir] relative to the root) |
| `--publisher` `string` | publisher name for the index entry (default: the existing entry's) |
| `--push` | push the tag to --remote |
| `--registry` `string` | local checkout of a registry to update (an https URL only prints the steps) |
| `--remote` `string` | git remote to push to and to read the repo URL from (default "origin") |
| `--replace-repo` | allow changing the repo or path of an existing index entry |
| `--repo` `string` | repository URL for the index entry (default: the --remote URL) |

```sh
cicord plugin publish --dry-run
cicord plugin publish --push --registry ../cicord-registry --publisher acme
cicord plugin publish ./my-plugin --repo https://github.com/acme/cicord-xp.git
cicord plugin publish plugins/polls --registry . --push   # monorepo: tag polls/v<version>
```

## Registries

Plugin sources work like apt sources or Homebrew taps. The official plugins (source `cicord`: the copies bundled with the binary plus the signed index of [github.com/cicord/cicord-plugins](https://github.com/cicord/cicord-plugins)) are always available; other registries are git repositories with an `index.yaml`, declared in `cicord.yaml` `spec.registries`, and are unofficial. Every `cicord registry` command takes `--dir` (the repository root, default `.`).

### `cicord registry list`

List the plugin sources.

```text
cicord registry list [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `cicord registry add`

Add a registry to `cicord.yaml` (`https://`, `file://` or a local path). With `--private`, its token is read from `CICORD_REGISTRY_<NAME>_TOKEN` (an environment variable here; on the runtime, an environment variable or `cicord secret set`), and only that name is written to `cicord.yaml`. The token is sent to the registry's host only.

```text
cicord registry add <name> <url> [flags]
```

| Flag | Description |
|---|---|
| `--no-check` | do not fetch the index before adding |
| `--private` | private registry: its token is CICORD_REGISTRY_&lt;NAME&gt;_TOKEN |
| `--dir` `string` | repository root (default ".") (inherited) |

```sh
cicord registry add acme https://github.com/acme/cicord-plugins.git
CICORD_REGISTRY_ACME_TOKEN=… cicord registry add acme https://github.com/acme/private-plugins.git --private
cicord registry add dev ../my-registry
```

### `cicord registry remove`

Remove an unused registry from `cicord.yaml`.

```text
cicord registry remove <name> [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `cicord registry update`

Fetch the index of every registry and report what it lists.

```text
cicord registry update [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `cicord registry search`

Search the plugins of every source.

```text
cicord registry search [query] [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `cicord registry keygen`

Create an ed25519 key pair to sign a registry index. The private key is written to `--out` (mode 0600, never overwritten); keep it out of any repository. The public key is printed.

```text
cicord registry keygen --out <file> [flags]
```

| Flag | Description |
|---|---|
| `--out` `string` | file to write the private key to |
| `--dir` `string` | repository root (default ".") (inherited) |

```sh
cicord registry keygen --out ~/.config/cicord/registry.key
```

### `cicord registry sign`

Sign `<registry-dir>/index.yaml` and write `index.yaml.sig`: the registry URL, a serial that increases with every signature (default: the current Unix time, or the previous serial + 1), an expiry (default 30 days, at most 180) and the SHA-256 of `index.yaml`, signed by each `--key`. The index is checked first (plugin names, `https://` repositories without credentials). Sign again after every change and on a schedule.

```text
cicord registry sign <registry-dir> --key <file|env:NAME> [flags]
```

| Flag | Description |
|---|---|
| `--expires-in` `duration` | validity of the signature (default 720h0m0s) |
| `--key` `stringArray` | private key: a file, or env:NAME for an environment variable (repeatable) |
| `--registry-url` `string` | URL of the registry the index is for (default: the official registry) |
| `--serial` `uint` | serial of this signature (default: current Unix time, always above the previous one) |
| `--dir` `string` | repository root (default ".") (inherited) |

```sh
cicord registry sign . --key ~/.config/cicord/registry.key --registry-url https://github.com/cicord/cicord-plugins
CICORD_REGISTRY_SIGNING_KEY=… cicord registry sign . --key env:CICORD_REGISTRY_SIGNING_KEY
```

### `cicord registry verify`

Check `index.yaml.sig` against `index.yaml` with the official keys of this build or `--public-key`: a signature by a trusted key, the hash, the registry URL and the expiry. Exit 1 otherwise, e.g. in the registry's CI.

```text
cicord registry verify <registry-dir> [flags]
```

| Flag | Description |
|---|---|
| `--public-key` `stringArray` | trusted public key (base64; default: the official keys of this build) |
| `--registry-url` `string` | registry URL the signature must name |
| `--dir` `string` | repository root (default ".") (inherited) |

## Portal

A custom portal is a React app (a fork of the reference portal) committed in the repository, with its build output, and served by the runtime at `/` instead of the default portal (`cicord.yaml` `spec.portal.path`). See [Custom portal](../../portal/custom-portal/).

### `cicord portal init`

Copy the reference portal into `[dir]` (default `portal`), with `@cicord/client`, `@cicord/react` and `@cicord/ui` vendored in `<dir>/vendor`. Inside a Cicord repository, also write `.github/workflows/cicord-portal.yml` and `src/cicord-routes.d.ts`. `--from-npm` (or `CICORD_FROM_NPM=true`) depends on npm instead, once the packages are published. Then build it, set `spec.portal.path: ./<dir>/dist` and commit it, `dist/` included.

```text
cicord portal init [dir] [flags]
```

| Flag | Description |
|---|---|
| `--force` | write into a non-empty directory / overwrite files |
| `--from-npm` | depend on the @cicord/* packages of the npm registry instead of vendoring the embedded tarballs |

### `cicord portal build`

Run the portal's package manager (pnpm when it has `pnpm-lock.yaml` or pnpm is installed, else npm): install when `node_modules` is missing, then the build script. The sources are the nearest directory above `spec.portal.path` with a `package.json`. Needs Node and pnpm or npm. `--check` installs from the lockfile, rebuilds and fails when the output differs from what was committed.

```text
cicord portal build [repo-dir] [flags]
```

| Flag | Description |
|---|---|
| `--check` | fail when the build output changes (CI) |

### `cicord portal typegen`

Write the types of the portal routes of every plugin installed in the repository (route keys, access levels, path parameters, and the query, body and response types of each route contract) to `<portal>/src/cicord-routes.d.ts`, `<portal>` being the directory of `spec.portal.path` (or `./portal`). `cicord portal build` runs it before building.

```text
cicord portal typegen [repo-dir] [flags]
```

| Flag | Description |
|---|---|
| `--check` | fail when the file is out of date instead of writing it |
| `--out` `string` | output file (default &lt;portal&gt;/src/cicord-routes.d.ts) |

```sh
cicord portal typegen
cicord portal typegen --check
cicord portal typegen examples/showcase --out packages/create-cicord-portal/src/cicord-routes.d.ts
```
