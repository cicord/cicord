---
title: CLI reference
description: Every codi command and subcommand with its real flags, generated from codi --help.
tableOfContents:
  maxHeadingLevel: 3
---

`codi` is both the command line tool and the runtime. This page lists every command with
the flags of `codi <command> --help` (version 0.1.0); `--help` stays authoritative. Every
command also accepts `-h, --help`.

| Command | Purpose |
|---|---|
| [`codi init`](#codi-init) | Scaffold a repository. |
| [`codi validate`](#codi-validate) | Schema and semantic validation, offline. |
| [`codi import`](#codi-import) | Generate `servers/<key>.yaml` from an existing Discord server. |
| [`codi mv`](#codi-mv) | Rename a resource key without recreating the resource. |
| [`codi plan`](#codi-plan) | Show what a sync would change, locally or through the runtime. |
| [`codi apply`](#codi-apply) | Reconcile directly (development, bootstrap). |
| [`codi adopt`](#codi-adopt) | Rebuild the local resource map by display name. |
| [`codi serve`](#codi-serve) | Run the runtime (production). |
| [`codi dev`](#codi-dev) | Run the runtime locally with hot reload, on real Discord or the simulator. |
| [`codi status`](#codi-status) | Revision, sync and health of a runtime; wait for a revision. |
| [`codi sync`](#codi-sync) | Ask the runtime to fetch and sync now. |
| [`codi version`](#codi-version) | Print the version. |
| [`codi secret`](#secrets) | `ls`, `set`, `rm`, `rotate-key`: the encrypted secret store. |
| [`codi plugin`](#plugins) | `new`, `sdk-types`, `build`, `typegen`, `test`, `add`, `update`, `publish`. |
| [`codi registry`](#registries) | `list`, `add`, `remove`, `update`, `search`, `keygen`, `sign`, `verify`. |
| [`codi portal`](#portal) | `init`, `build`, `typegen`: a custom community portal. |
| `codi completion` | Generate the shell completion script (`bash`, `zsh`, `fish`, `powershell`). |

Commands that talk to a runtime (`plan --remote`, `status`, `sync`) read its URL from
`--remote=URL` or `CODI_RUNTIME_URL` (https, or http on localhost) and the bearer token from
`CODI_RUNTIME_TOKEN`; never from `codi.yaml`. Commands that work on a repository take the
directory as an optional argument (default `.`).

## Repository

### `codi init`

Scaffold a new CODI repository: `codi.yaml`, `servers/<server>.yaml`, `bots/<bot>/bot.yaml`, `.github/workflows/codi.yml`, `README.md` and `.gitignore`. Without IDs, the placeholders work with `codi dev --sim`; replace them with your real IDs before `codi plan`.

```text
codi init [dir] [flags]
```

| Flag | Description |
|---|---|
| `--application-id` `string` | Discord application ID of the bot (default: the placeholder "100000000000000002", which works with codi dev --sim) |
| `--server-id` `string` | Discord server ID (default: the placeholder "100000000000000001", which works with codi dev --sim) |
| `--bot` `string` | bot key (default "bot") |
| `--force` | overwrite existing files |
| `--project` `string` | project key (default: directory name) |
| `--provider` `string` | source provider: github \| git \| embedded (default: inferred from --repo, none for local-only repos) |
| `--repo` `string` | repository URL (github / git providers) |
| `--server` `string` | server key (default: project key) |

```sh
codi init my-community --server-id 112233445566778899 --application-id 998877665544332211
codi init . --server my-server --bot helper
```

### `codi validate`

Validate a repository offline: JSON Schemas, then semantic checks (references, unique keys, plugin configs against their schemas after overrides, grants, intents, `codi.lock`, engine range, command names). Exits 1 on errors; warnings (privileged intents, probable renames, `prune` without deletion protection…) are printed.

```text
codi validate [dir] [flags]
```

| Flag | Description |
|---|---|
| `-o, --output` `string` | output format: text \| json (default "text") |

### `codi import`

Connect to Discord with a bot token, read the live server (roles with their hierarchy and permissions, `@everyone`, categories, channels with their overwrites, positions, topics and slowmode, the profile, AutoMod, onboarding, widget, welcome screen) and write `servers/<key>.yaml`. The server icon, banner and invite splash, role icons, emojis, stickers and soundboard sounds are downloaded into `servers/assets/<key>/`. Keys are derived from the display names; integration roles are skipped. The resource map is written to the local state database, so a later `codi apply` adopts the existing resources. The bot must be a member of the server; add the server key to a bot's `spec.servers` afterwards.

```text
codi import <server-id> [dir] [flags]
```

| Flag | Description |
|---|---|
| `--force` | overwrite an existing servers/&lt;key&gt;.yaml and its resource map |
| `--key` `string` | server key (default: derived from the server name) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.codi/state.db) |
| `--token-env` `string` | environment variable holding the bot token (default "DISCORD_TOKEN") |

```sh
DISCORD_TOKEN=… codi import 112233445566778899
codi import 112233445566778899 ./infra --key ma-commu --token-env DISCORD_TOKEN_MODERATOR
```

### `codi mv`

Rename the key of a role, category or channel: the server YAML (the key and every reference: channel categories, hierarchy, access roles, overwrite targets, profile channels), the plugin configurations that reference it through `x-codi-ref`, and the local resource map. Comments and formatting are kept. A runtime moves its own mapping when the renamed file reaches it (plan: `adopt` + `move … mapping`), so the Discord resource is kept. Rename the key first, then the display name, in separate commits. When a plugin instance runs on several servers and its base config references the key, the command stops (add an override first, or `--force`).

```text
codi mv <server> <role|category|channel> <old-key> <new-key> [dir] [flags]
```

| Flag | Description |
|---|---|
| `--dry-run` | show the changes without writing anything |
| `--force` | rename even when a shared plugin base config references the key (it is left unchanged) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.codi/state.db) |

```sh
codi mv ma-commu channel general discussions
codi mv ma-commu role modo moderators --dry-run
```

## Plan and apply

### `codi plan`

Compute the diff between the repository and the live servers; nothing is applied.

- **Remote** (CI): `--remote` uploads the repository (`.tar.gz`, without `.git`, `node_modules`, `.codi`, `dist`, `.env` files and the root `.gitignore` patterns) to the runtime, which validates and plans it with its own state and bots (`POST /api/v1/plan`). The URL is `--remote=URL`, else `CODI_RUNTIME_URL` (https, or http on localhost); the token is `CODI_RUNTIME_TOKEN`. `codi.yaml` `spec.runtime.url` is never used to send the token.
- **Local**: without `--remote`, the repository is planned against Discord directly with the bot tokens from the environment and the local state database (read only).

`--comment` posts the plan on the pull request in GitHub Actions (`GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `GITHUB_EVENT_PATH`; `GITHUB_API_URL` for GitHub Enterprise), updating the previous CODI comment. Symbols: `+` create, `~` update, `-` delete, `>` reorder, `=` adopt, `#` stop managing, `!` blocked. Exit codes: 0 success, 1 error or invalid configuration, 2 changes pending (only with `--detailed-exitcode`).

```text
codi plan [dir] [flags]
```

| Flag | Description |
|---|---|
| `--comment` | post/update the plan as a pull-request comment (GitHub Actions) |
| `--detailed-exitcode` | exit 2 when changes are pending |
| `-o, --output` `string` | output format: text \| json (default "text") |
| `--pr` `int` | pull request number for --comment (default: from GITHUB_EVENT_PATH) |
| `--remote` `string[="auto"]` | runtime URL (--remote=URL); without a value: CODI_RUNTIME_URL (https, or http on localhost) |
| `--server` `strings` | only these server keys (repeatable) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.codi/state.db) |

```sh
codi plan                                   # local, bot tokens from the environment
codi plan --remote                          # runtime from CODI_RUNTIME_URL
codi plan --remote=https://codi.example.com --comment
codi plan --remote -o json
codi plan --detailed-exitcode; echo $?      # 0 no changes, 1 error, 2 changes pending
```

### `codi apply`

Validate, plan every server against Discord with the bot tokens from the environment, show the plan, ask for confirmation, apply it and record the created and adopted resources in the local state database. For development and bootstrapping: in production, the runtime applies merged changes with its own state. Without a terminal, `--yes` is required.

```text
codi apply [dir] [flags]
```

| Flag | Description |
|---|---|
| `-o, --output` `string` | output format of the result: text \| json (default "text") |
| `--server` `strings` | only these server keys (repeatable) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.codi/state.db) |
| `-y, --yes` | apply without asking for confirmation |

```sh
DISCORD_TOKEN_MODERATOR=… codi apply
codi apply --server ma-commu --yes
codi apply ./infra --state /var/lib/codi/state.db
```

### `codi adopt`

Rebuild the local resource map after it was lost or became stale, by matching each declared role, category and channel with an existing Discord resource of the same kind and display name. Valid mappings are kept, mappings to deleted resources dropped. The matches are shown and must be confirmed. Nothing is changed in Discord; keys without a match are created by the next `codi apply`.

```text
codi adopt [dir] [flags]
```

| Flag | Description |
|---|---|
| `--reset` | ignore existing mappings and match every key by name |
| `--server` `strings` | only these server keys (repeatable) |
| `--state` `string` | local state database (resource map) (default &lt;dir&gt;/.codi/state.db) |
| `-y, --yes` | write the mappings without asking for confirmation |

```sh
codi adopt
codi adopt --server ma-commu --reset --yes
```

## Runtime

### `codi serve`

Run the runtime: follow the configured source (`codi.yaml` `spec.source`), reconcile servers, run the bots and their plugins, and serve the HTTP API, the admin console (`/admin/`) and the portal (`/`). In a directory without `codi.yaml`, it starts the setup wizard. `--data-dir` defaults to `.codi-data`. Environment variables: see [Deploy](../../self-hosting/deploy/#environment-variables).

```text
codi serve [dir] [flags]
```

| Flag | Description |
|---|---|
| `--cors-origin` `strings` | external portal origin allowed with credentials (repeatable) |
| `--data-dir` `string` | runtime state directory (database, caches, keys) |
| `--listen` `string` | HTTP listen address (default ":7420") |
| `--local` | serve the directory as-is, ignoring spec.source (no git) |
| `--log-level` `string` | log level: debug \| info \| warn \| error (default "info") |
| `--public-url` `string` | public URL of the runtime (OAuth redirects, portal links); default http://&lt;listen&gt; |

### `codi dev`

Run the runtime on a local working directory: every saved change becomes a new revision, admin console edits are written back to the files, and `POST /auth/dev-login` signs in as any user. With `--sim`, Discord is replaced by the in-memory simulator, seeded from the repository (servers, bots invited with Administrator, an `owner` and a few users). Without `--sim`, bot tokens come from the environment, then the secret store. In a directory without `codi.yaml`, it starts the setup wizard; `--setup` makes that explicit. The data directory defaults to `<dir>/.codi/dev`. **Never expose it on a network.**

```text
codi dev [dir] [flags]
```

| Flag | Description |
|---|---|
| `--cors-origin` `strings` | external portal origin allowed with credentials (repeatable) |
| `--data-dir` `string` | runtime state directory (database, caches, keys) |
| `--listen` `string` | HTTP listen address (default "127.0.0.1:7420") |
| `--log-level` `string` | log level: debug \| info \| warn \| error (default "info") |
| `--public-url` `string` | public URL of the runtime (OAuth redirects, portal links); default http://&lt;listen&gt; |
| `--setup` | start the setup wizard (the directory must not contain codi.yaml) |
| `--sim` | use the Discord simulator instead of real Discord |

### `codi status`

Show the status of a runtime (`GET /api/v1/status`): the active revision, every server (sync, health, drift) and every bot with its plugin instances. Runtime URL and token as for `codi plan --remote`. `--wait` polls until `--revision` (a SHA or prefix; default: the active one) is active and every server is `Synced` and `Healthy`; it fails when the revision is rejected, a server is `Degraded` or the timeout expires. A server with blocked operations stays `OutOfSync`, so `--wait` times out on it and prints the drift.

```text
codi status [dir] [flags]
```

| Flag | Description |
|---|---|
| `--interval` `duration` | polling interval (with --wait; default 4s) |
| `-o, --output` `string` | output format: text \| json (default "text") |
| `--remote` `string[="auto"]` | runtime URL (default: CODI_RUNTIME_URL; https, or http on localhost) |
| `--revision` `string` | revision to wait for: a git SHA or a prefix of it (with --wait) |
| `--timeout` `duration` | maximum time to wait (with --wait) (default 5m0s) |
| `--wait` | wait until the revision is active and every server is Synced and Healthy |

```sh
codi status
codi status --remote=https://codi.example.com -o json
codi status --wait --revision $GITHUB_SHA --timeout 10m
```

### `codi sync`

Ask the runtime to fetch its source and activate the new revision, if any (`POST /api/v1/sync`). Use it after a push when no webhook is configured. `--wait` then waits like `codi status --wait`; pass `--revision` for an exact wait.

```text
codi sync [dir] [flags]
```

| Flag | Description |
|---|---|
| `--interval` `duration` | polling interval (with --wait; default 4s) |
| `-o, --output` `string` | output format of the final status: text \| json (default "text") |
| `--remote` `string[="auto"]` | runtime URL (default: CODI_RUNTIME_URL; https, or http on localhost) |
| `--revision` `string` | revision to wait for: a git SHA or a prefix of it (with --wait) |
| `--timeout` `duration` | maximum time to wait (with --wait) (default 5m0s) |
| `--wait` | wait until the revision is active and every server is Synced and Healthy |

```sh
codi sync
codi sync --wait --revision $(git rev-parse HEAD)
```

### `codi version`

Print the version and the commit the binary was built from, e.g. `codi 0.1.0 (dee0768)`.

```text
codi version [flags]
```

## Secrets

Manage the encrypted secret store of a data directory (bot tokens, `CODI_DISCORD_CLIENT_SECRET`, `CODI_GIT_TOKEN`, registry tokens). Environment variables always take precedence over stored secrets. Values are read from stdin and never printed; the runtime picks a changed bot token up on its next restart. Every `codi secret` command takes `--data-dir` (default `.codi-data`). See [Backups and secrets](../../self-hosting/backups-and-secrets/#the-secret-store).

### `codi secret ls`

List the stored secrets (names only).

```text
codi secret ls [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (default ".codi-data") (inherited) |

### `codi secret set`

Store or rotate a secret; the value is read from stdin.

```text
codi secret set NAME [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (default ".codi-data") (inherited) |

```sh
codi secret set DISCORD_TOKEN_CODI --data-dir /data   # paste the token, then Enter
```

### `codi secret rm`

Delete a stored secret.

```text
codi secret rm NAME [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (default ".codi-data") (inherited) |

### `codi secret rotate-key`

Re-encrypt every secret with a new key (key file only, not `CODI_SECRET_KEY`).

```text
codi secret rotate-key [flags]
```

| Flag | Description |
|---|---|
| `--data-dir` `string` | runtime state directory (default ".codi-data") (inherited) |

## Plugins

### `codi plugin new`

Scaffold a TypeScript plugin: `plugin.yaml`, `config.schema.json`, `tsconfig.json`, `src/`, the SDK types, and a unit-test setup with the vendored `@codi/sdk`. Prints the `bot.yaml` snippet to enable it. See [Writing a plugin](../../plugins/writing/).

```text
codi plugin new <dir> [flags]
```

| Flag | Description |
|---|---|
| `--force` | overwrite existing files |
| `--from-npm` | depend on @codi/sdk from the npm registry instead of vendoring the embedded tarball |

### `codi plugin sdk-types`

Write the `@codi/sdk` type definitions into `<plugin-dir>/.codi` for editors and `tsc`.

```text
codi plugin sdk-types <plugin-dir> [flags]
```

### `codi plugin build`

Bundle the plugin with esbuild (no Node.js needed), load it once to extract its static definition (commands, events, routes…) and check it against `plugin.yaml` like the runtime does: handlers that need an intent missing from `spec.intents`, schedules, routes or subscriptions without their capability, `http.fetch` without `spec.network.allow`. Exit 1 when the plugin would be rejected.

```text
codi plugin build <plugin-dir> [flags]
```

| Flag | Description |
|---|---|
| `--out` `string` | also write the bundle to this file |
| `-o, --output` `string` | output format: text \| json (default "text") |

```sh
codi plugin build plugins/welcome
codi plugin build plugins/welcome -o json
codi plugin build plugins/welcome --out dist/welcome.js
```

### `codi plugin typegen`

Generate a module augmentation of `@codi/sdk`'s `PluginConfig` from the plugin's config schema, so `ctx.config` is typed. Fields that are required or have a default the runtime applies are non-optional; `x-codi-ref` fields are keys (strings); enums become literal unions.

```text
codi plugin typegen <plugin-dir> [flags]
```

| Flag | Description |
|---|---|
| `--check` | fail when the file is out of date instead of writing it |
| `--out` `string` | output file (default &lt;plugin-dir&gt;/src/codi-env.d.ts) |

```sh
codi plugin typegen plugins/welcome
codi plugin typegen plugins/welcome --check
```

### `codi plugin test`

Build the plugin and deploy it in a throw-away repository on the Discord simulator with the real runtime (a test server with the resources its config references, a bot granted every requested capability, one instance). Then, unless `--script=false`, a member joins (when the plugin handles `memberJoin`) and every slash command without required options is invoked once. Fails when the plugin does not build, its config does not validate, the instance does not start or an interaction fails. Required `x-codi-ref` fields without a value are filled with test resources.

```text
codi plugin test <plugin-dir> [flags]
```

| Flag | Description |
|---|---|
| `--config` `string` | plugin config file (YAML or JSON) |
| `--script` | run the scripted simulation after the start (default true) |
| `--timeout` `duration` | overall timeout (default 1m0s) |
| `-v, --verbose` | show the runtime and plugin logs |

```sh
codi plugin test plugins/welcome
codi plugin test plugins/onboarding --config plugins/onboarding/test/config.yaml
```

### `codi plugin add`

Add a registry plugin to a bot and lock it in `codi.lock`.

```text
codi plugin add <registry>/<plugin>[@<range>] [flags]
```

| Flag | Description |
|---|---|
| `--bot` `string` | bot key |
| `--dir` `string` | repository root (default ".") |
| `--name` `string` | instance name (default: plugin name) |

```sh
codi plugin add acme/xp --bot moderator
codi plugin add acme/xp@^1.2.0 --bot moderator --name xp
```

The `--help` examples use the `codi/` prefix, which only resolves once the official registry
exists; today the bundled official plugins are installed from the setup wizard or the Store.

### `codi plugin update`

Without argument, list the plugins with a newer version: registry plugins (`codi.lock`) and copies of the official plugins (`plugins/<name>/`). With a plugin, update every instance using it: the lock entry, each instance's version range when the new version is outside it, and the grants (set to what the new version requests). Copies of official plugins (`codi/<name>`) are rewritten from the plugins bundled in this binary. Review the printed capability changes before committing.

```text
codi plugin update [<registry>/<plugin>] [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") |
| `--force` | overwrite a locally modified copy of an official plugin |
| `--to` `string` | version range to update to (default: the latest version, any major); written to every instance |

```sh
codi plugin update
codi plugin update acme/xp
codi plugin update acme/xp --to ^2.0.0
codi plugin update codi/welcome
```

### `codi plugin publish`

Release a plugin from its repository (`plugin.yaml` at the root of a git repository): validate it like `codi plugin build`, check the working tree is clean and that `metadata.version` is a semver greater than every `v*` tag, create the annotated tag `v<version>` (pushed with `--push`), then print the registry index entry or, with `--registry <dir>`, write it to a local registry checkout. Never sets `verified`. See [Publishing](../../plugins/publishing/).

```text
codi plugin publish [plugin-repo] [flags]
```

| Flag | Description |
|---|---|
| `--dry-run` | validate and print what would be done, change nothing |
| `--publisher` `string` | publisher name for the index entry (default: the existing entry's) |
| `--push` | push the tag to --remote |
| `--registry` `string` | local checkout of a registry to update (an https URL only prints the steps) |
| `--remote` `string` | git remote to push to and to read the repo URL from (default "origin") |
| `--replace-repo` | allow changing the repo of an existing index entry |
| `--repo` `string` | repository URL for the index entry (default: the --remote URL) |

```sh
codi plugin publish --dry-run
codi plugin publish --push --registry ../codi-registry --publisher acme
codi plugin publish ./my-plugin --repo https://github.com/acme/codi-xp.git
```

## Registries

Plugin sources work like apt sources or Homebrew taps. The official plugins (source `codi`) are always available; other registries are git repositories with an `index.yaml`, declared in `codi.yaml` `spec.registries`, and are unofficial. Every `codi registry` command takes `--dir` (the repository root, default `.`).

### `codi registry list`

List the plugin sources.

```text
codi registry list [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `codi registry add`

Add a registry to `codi.yaml` (`https://`, `file://` or a local path). With `--private`, its token is read from `CODI_REGISTRY_<NAME>_TOKEN` (an environment variable here; on the runtime, an environment variable or `codi secret set`), and only that name is written to `codi.yaml`. The token is sent to the registry's host only.

```text
codi registry add <name> <url> [flags]
```

| Flag | Description |
|---|---|
| `--no-check` | do not fetch the index before adding |
| `--private` | private registry: its token is CODI_REGISTRY_&lt;NAME&gt;_TOKEN |
| `--dir` `string` | repository root (default ".") (inherited) |

```sh
codi registry add acme https://github.com/acme/codi-plugins.git
CODI_REGISTRY_ACME_TOKEN=… codi registry add acme https://github.com/acme/private-plugins.git --private
codi registry add dev ../my-registry
```

### `codi registry remove`

Remove an unused registry from `codi.yaml`.

```text
codi registry remove <name> [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `codi registry update`

Fetch the index of every registry and report what it lists.

```text
codi registry update [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `codi registry search`

Search the plugins of every source.

```text
codi registry search [query] [flags]
```

| Flag | Description |
|---|---|
| `--dir` `string` | repository root (default ".") (inherited) |

### `codi registry keygen`

Create an ed25519 key pair to sign a registry index. The private key is written to `--out` (mode 0600, never overwritten); keep it out of any repository. The public key is printed.

```text
codi registry keygen --out <file> [flags]
```

| Flag | Description |
|---|---|
| `--out` `string` | file to write the private key to |
| `--dir` `string` | repository root (default ".") (inherited) |

```sh
codi registry keygen --out ~/.config/codi/registry.key
```

### `codi registry sign`

Sign `<registry-dir>/index.yaml` and write `index.yaml.sig`: the registry URL, a serial that increases with every signature (default: the current Unix time, or the previous serial + 1), an expiry (default 30 days, at most 180) and the SHA-256 of `index.yaml`, signed by each `--key`. The index is checked first (plugin names, `https://` repositories without credentials). Sign again after every change and on a schedule.

```text
codi registry sign <registry-dir> --key <file|env:NAME> [flags]
```

| Flag | Description |
|---|---|
| `--expires-in` `duration` | validity of the signature (default 720h0m0s) |
| `--key` `stringArray` | private key: a file, or env:NAME for an environment variable (repeatable) |
| `--registry-url` `string` | URL of the registry the index is for (default: the official registry) |
| `--serial` `uint` | serial of this signature (default: current Unix time, always above the previous one) |
| `--dir` `string` | repository root (default ".") (inherited) |

```sh
codi registry sign . --key ~/.config/codi/registry.key --registry-url https://github.com/codi-dev/registry
CODI_REGISTRY_SIGNING_KEY=… codi registry sign . --key env:CODI_REGISTRY_SIGNING_KEY
```

### `codi registry verify`

Check `index.yaml.sig` against `index.yaml` with the official keys of this build or `--public-key`: a signature by a trusted key, the hash, the registry URL and the expiry. Exit 1 otherwise, e.g. in the registry's CI.

```text
codi registry verify <registry-dir> [flags]
```

| Flag | Description |
|---|---|
| `--public-key` `stringArray` | trusted public key (base64; default: the official keys of this build) |
| `--registry-url` `string` | registry URL the signature must name |
| `--dir` `string` | repository root (default ".") (inherited) |

## Portal

A custom portal is a React app (a fork of the reference portal) committed in the repository, with its build output, and served by the runtime at `/` instead of the default portal (`codi.yaml` `spec.portal.path`). See [Custom portal](../../portal/custom-portal/).

### `codi portal init`

Copy the reference portal into `[dir]` (default `portal`), with `@codi/client`, `@codi/react` and `@codi/ui` vendored in `<dir>/vendor`. Inside a CODI repository, also write `.github/workflows/codi-portal.yml` and `src/codi-routes.d.ts`. `--from-npm` (or `CODI_FROM_NPM=true`) depends on npm instead, once the packages are published. Then build it, set `spec.portal.path: ./<dir>/dist` and commit it, `dist/` included.

```text
codi portal init [dir] [flags]
```

| Flag | Description |
|---|---|
| `--force` | write into a non-empty directory / overwrite files |
| `--from-npm` | depend on the @codi/* packages of the npm registry instead of vendoring the embedded tarballs |

### `codi portal build`

Run the portal's package manager (pnpm when it has `pnpm-lock.yaml` or pnpm is installed, else npm): install when `node_modules` is missing, then the build script. The sources are the nearest directory above `spec.portal.path` with a `package.json`. Needs Node and pnpm or npm. `--check` installs from the lockfile, rebuilds and fails when the output differs from what was committed.

```text
codi portal build [repo-dir] [flags]
```

| Flag | Description |
|---|---|
| `--check` | fail when the build output changes (CI) |

### `codi portal typegen`

Write the types of the portal routes of every plugin installed in the repository (route keys, access levels, path parameters, and the query, body and response types of each route contract) to `<portal>/src/codi-routes.d.ts`, `<portal>` being the directory of `spec.portal.path` (or `./portal`). `codi portal build` runs it before building.

```text
codi portal typegen [repo-dir] [flags]
```

| Flag | Description |
|---|---|
| `--check` | fail when the file is out of date instead of writing it |
| `--out` `string` | output file (default &lt;portal&gt;/src/codi-routes.d.ts) |

```sh
codi portal typegen
codi portal typegen --check
codi portal typegen examples/showcase --out packages/create-codi-portal/src/codi-routes.d.ts
```
