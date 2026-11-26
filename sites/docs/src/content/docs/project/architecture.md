---
title: Architecture
description: The goals of Cicord, how the runtime is put together component by component, the sync lifecycle, the CI/CD flow, the security model, the testing strategy and the source layout.
---

> **Cicord**: *CI/CD for Discord*. GitOps for Discord: servers and bots are declared in a git
> repository, a runtime reconciles them continuously, and behavior is extended with TypeScript
> plugins.

## Goals

1. **Declarative.** A Discord server (roles, categories, channels, permissions) and the bots
   that live on it are described in YAML files, Kubernetes style (`apiVersion`, `kind`,
   `metadata`, `spec`).
2. **GitOps.** Git is the single source of truth for configuration. Every change, whether it
   comes from a developer's pull request or from a click in the admin console, is a commit.
3. **One engine, many bots.** Every bot deployed with Cicord runs on the same engine. A single
   runtime hosts *N* bot identities across *M* servers.
4. **Extensible.** Features are plugins written in TypeScript, sandboxed, with explicit
   capabilities, installable from registries.
5. **Two UIs.** A generic admin console (generated from schemas) for owners and moderators,
   and a community portal that is an SDK any React app can embed.
6. **Self-hosted first, SaaS later.** Nothing in the design assumes a single tenant or a
   specific host.

Non-goals for v1: sharding (Cicord bots are mostly single- or few-server), voice and audio
processing, a visual plugin builder.

## The runtime

Everything ships as **one Go binary, `cicord`**: the command line tool and the runtime
(`cicord serve`), with the admin console embedded
([ADR-0001](../decisions/0001-single-runtime-controller/)). Inside the runtime:

```text
source (github · git · embedded · local) ─▶ loader & validator ─▶ builder (esbuild in Go)
       ─▶ revision (immutable: config + plugin bundles)
             ├─▶ server reconciler   plan · apply · drift          ─┐
             └─▶ bot host            one gateway session per bot    ├─▶ Discord REST + gateway
                   └─ plugin host    one QuickJS VM per instance   ─┘
HTTP API ─ admin console · portal SDK · CLI ─▶ write-back (YAML patch + commit / pull request)
state store (SQLite or PostgreSQL): resource map · plugin KV · audit · sessions · history · analytics
```

| Component | Role |
|---|---|
| **Source controller** | Fetches the repository (go-git, no `git` binary): polling, GitHub webhooks and `POST /api/v1/sync`. Produces one immutable snapshot per commit. |
| **Loader and validator** | JSON Schemas, then semantic checks. The same code runs in `cicord validate`, in the runtime and before every admin console edit. |
| **Builder** | Resolves plugins (repository or `cicord.lock`), bundles TypeScript with esbuild used as a Go library, checks each bundle against its manifest. |
| **Server reconciler** | Plans and applies, in a rate-limit-aware order, adopts by name, detects drift from gateway events. |
| **Analytics collector** | Turns gateway events and periodic samples into hourly counts per server (no content, no member IDs). |
| **Bot host** | One gateway session per bot with the union of its intents; syncs slash commands; routes events and interactions; hot-swaps plugin VMs on a new revision without reconnecting. |
| **Plugin host** | QuickJS-ng compiled to WebAssembly and run by wazero (no CGO): one VM and event loop per instance with a hard memory limit, capability checks on every host call, CPU budgets with a hard stop, wall-clock budgets, automatic restart of crashed instances, auto-defer of interactions. |
| **State store** | SQLite (pure Go) in the data directory by default, or PostgreSQL (`--database-url`); in memory for `cicord dev --sim`. |
| **HTTP API** | Serves the admin console, the portal SDK and the CLI; OAuth, portal links, bearer tokens; CSRF protection; a live event stream. |
| **Write-back** | Patches YAML at the node level (comments kept), commits as `cicord[bot]`, pushes or opens a pull request. |

The Discord library is wrapped behind Cicord's own interfaces, so the rest of the code never
depends on it; the in-process **simulator** implements the same interfaces and backs
`cicord dev --sim`, `cicord plugin test` and the test suite.

### Source controller

- Providers (`cicord.yaml` `spec.source.provider`): `github`, `git` (any HTTPS remote, with
  `CICORD_GIT_TOKEN` or a GitHub App) and `embedded` (a bare repository in the data directory,
  seeded from the served directory). `cicord serve --local` and `cicord dev` use the working
  directory itself.
- Git sources are fetched with go-git: **polling** (default every minute,
  `spec.source.pollInterval`) plus **webhooks** (`/hooks/github`) and `POST /api/v1/sync` for
  immediate fetches. The local source watches files and hashes their content.
- Each commit SHA (or content hash) becomes an immutable snapshot directory: a **revision**.

See [Project → source](../../configuration/project/#specsource).

### Loader and validator

1. Parse the YAML and validate each document against its JSON Schema (`spec/schemas`).
2. Semantic validation: references resolve (`category: community` exists), keys are unique,
   plugin configs validate against their `config.schema.json`, every capability a plugin
   requests is granted in `bot.yaml`, the engine version is compatible.
3. The same code runs in `cicord validate` (offline), in the runtime, and in the config
   write-back ([`PUT …/config`](../../reference/http-api/plugin-instances/#editing-the-config)).

### Builder

- Resolves plugins from `cicord.lock` (registry plugins, `internal/registry`) or the
  repository (local plugins).
- Bundles TypeScript with **esbuild used as a Go library** (`internal/build`: ES2023, inline
  source maps, `@cicord/sdk` as the only external). No Node.js is required anywhere
  ([ADR-0005](../decisions/0005-typescript-plugins/)).
- Loads each bundle once to extract its static definition (commands, handlers, routes) and
  checks it against the manifest: a `memberJoin` handler requires the `ServerMembers` intent,
  for example.

### Server reconciler

- **Plan**: the diff between the desired state (the revision) and the live state (the Discord
  API plus the resource map).
- **Apply**: ordered, rate-limit-aware operations, resumable after a failure.
- **Drift**: the bot host forwards gateway events (`CHANNEL_UPDATE`, `ROLE_DELETE`…), so manual
  changes in Discord are detected in real time. Per server, `syncPolicy.selfHeal` decides
  when they are reverted: right away (and by the periodic sync every 5 minutes), or only by
  the next revision or an explicit sync. Drift reads (runtime status, server detail) reuse the last plan until an apply, a
  relevant gateway event or a minute passes, instead of reading the whole server from Discord
  on each request. Changes that only wait for a higher boost level stay in the plan but are
  not drift.
- **Adoption** and **ownership**: a declared resource without a mapping adopts an existing
  Discord resource with the same name; only resources in the resource map are ever modified,
  and plugin-owned resources are never pruned.

The exact rules: [Reconciliation](../../concepts/reconciliation/)
([ADR-0003](../decisions/0003-state-in-runtime-db/)).

### Bot host

- One gateway session per bot. Its intents are the union of those its plugin instances need,
  plus what the reconciler needs.
- Syncs slash commands per server from the plugins' static definitions.
- Routes events and interactions to plugin instances.
- **Hot swap** on a new revision: new plugin VMs are started and validated, then traffic is
  switched atomically per bot and the old VMs are drained. No gateway reconnect, no lost
  events; storage persists across revisions.

### Plugin host

- JavaScript engine: **QuickJS-ng compiled to WebAssembly** (`internal/plugin/qjs`, a
  reproducible build of QuickJS-ng plus a small shim), run by **wazero** in the Go process (no
  CGO). The bot host consumes the engine-neutral `plugin.Runtime` interface
  ([ADR-0018](../decisions/0018-quickjs-wasm-engine/), superseding the engine part of
  [ADR-0005](../decisions/0005-typescript-plugins/)).
- One isolated VM per plugin instance (its own linear memory, a hard memory cap) and a
  serialized event queue per VM ([ADR-0010](../decisions/0010-plugin-instance-per-bot-server/)).
  A crash (memory limit, hard stop, trap) discards that instance only; the bot host restarts
  it with exponential backoff.
- Capability enforcement on every host call
  ([ADR-0006](../decisions/0006-capability-based-permissions/)).
- A CPU budget per event-loop turn (an interrupt, then a hard stop for code that ignores it),
  wall-clock timeouts, host-side quotas (pending host calls, queue, payload sizes), and no
  filesystem or network access except through capabilities (`http.fetch` with an allow-list).
  The budgets: [Testing and limits](../../plugins/testing/).
- Images (card renders, image hashes) are decoded and drawn in a separate, confined
  WebAssembly worker (`internal/media`), never in the runtime process.
- **Interaction deadline**: Discord requires an answer within 3 seconds. If a handler has not
  replied after about 2 seconds, the host defers automatically; a later `reply()` transparently
  edits the deferred response.

### State store

SQLite (`internal/state/sqlite`, a pure Go driver; a single file in the data directory, in
memory for `cicord dev --sim`) by default, or PostgreSQL (`internal/state/postgres`, pgx;
`--database-url` or `CICORD_DATABASE_URL`), behind the `internal/state` interfaces. Both
backends pass the same conformance suite (`internal/state/statetest`). It holds:

- the resource map: `(server key, kind, resource key) → Discord ID`, plus the owner (git or a
  plugin instance);
- plugin storage, scoped per plugin instance: KV entries, rankings and a search index, within
  per-installation quotas;
- durable plugin tasks and event deliveries (leased, retried, then parked as dead letters),
  interop records, and the active incident overlays of each server;
- the audit log (moderation and admin console actions), sessions and the sync history;
- the analytics aggregates (hourly counts, `spec.analytics.retention`, default 400 days).

**Configuration lives in git, data lives in the database**: XP, warnings, tickets and member
preferences never go to git ([ADR-0004](../decisions/0004-git-write-back/)).

### HTTP API

`internal/api`, documented in the [HTTP API reference](../../reference/http-api/). It serves the
admin console, the community portal SDK and the CLI (`cicord plan --remote`, `cicord status`,
`cicord sync`), plus the simulator endpoints of `cicord dev --sim`.
[`GET /api/v1/events`](../../reference/http-api/events/) is a Server-Sent Events stream of
change hints (status, sync, plugins, analytics) that carries no content: the admin console
refetches what it shows. Prometheus metrics are served on a separate listener
(`cicord serve --metrics-listen`), never on the API port. Authentication uses Discord
OAuth2, portal links (`ctx.portal.link()`, scoped to one server), session tokens for embedded
portals and the runtime bearer token; authorization is derived from Discord roles
(`spec.access` of each server). Plugins can expose their own routes to the portal. CSRF
protection (`X-Cicord-Request` plus an Origin check) and, in dev mode, a loopback-only Host
check.

### Write-back

```text
UI edit ─▶ API validates against the schemas ─▶ YAML node tree patched (yaml.v3, comments kept)
        ─▶ commit on the latest branch head (author cicord[bot], trailers Cicord-Actor / Cicord-Source)
        ─▶ policy: commit       → the runtime activates its own SHA immediately
                   pullRequest  → the change waits for review and merge, then syncs normally
        ─▶ conflict: the edit is re-applied on the latest head; if it no longer applies → 409
```

`internal/writeback` pushes with go-git and the repository credentials of `internal/gitauth`
(`CICORD_GIT_TOKEN`, or a GitHub App whose installation tokens are minted and renewed on
demand); pull requests use the GitHub API with the same credentials. `cicord dev` writes to the
working directory. The runtime never applies a change that is not committed first. See
[Write-back](../../concepts/write-back/).

## Sync lifecycle

```text
commit ─▶ fetch ─▶ validate ─▶ build ─▶ plan ─▶ apply servers ─▶ activate bots ─▶ report
                     │            │                │                  │             │
                  rejected     rejected         failed            rolled back   history, status,
                  (status)     (status)         (resumable)       to previous   commit status
```

Each server and bot exposes a status, ArgoCD style:

| Sync | Health |
|---|---|
| `Synced`, `OutOfSync`, `Unknown` | `Healthy`, `Progressing`, `Degraded`, `Missing` |

The active revision only changes when validation and build succeed. A failed apply on one
server does not block the others. Every activation, rejection and server sync is recorded in
the sync history (`GET /api/v1/history`), and on GitHub as a `cicord/sync` commit status.

## CI/CD flow

| Event | CI step | Runtime |
|---|---|---|
| Pull request | `cicord validate` (offline) and `cicord plan --remote`, posted as a pull request comment | Answers the plan request; nothing is applied. |
| Merge to `main` | Optionally `cicord status --wait` | Webhook, then sync; a `cicord/sync` commit status on the SHA. |
| Admin console change | | Commits, then activates (or opens a pull request, by policy). |

`cicord init` writes this workflow as `.github/workflows/cicord.yml`.

## User interfaces

### Admin console

- Shipped prebuilt inside the binary, not customized per deployment
  ([ADR-0007](../decisions/0007-generic-admin-ui/)).
- Server overview, sync status, the plan, drift, sync history, audit log, analytics, the
  Store, diagnostics; refreshed live from `GET /api/v1/events` (every 30 s without it).
- **Plugin settings pages are generated from each plugin's `config.schema.json`.** The Cicord
  schema extensions (`x-cicord-ref: channel | role | category`, `x-cicord-widget`,
  `x-cicord-access`) render pickers and restrict moderator edits.
- Every save goes through write-back.
- With `cicord dev --sim`, a **Simulator** panel plays Discord users (joins, messages, slash
  commands, buttons, selects, modals, DMs) against the in-memory world
  ([ADR-0017](../decisions/0017-dev-mode-with-simulator/)).

### Community portal (SDK)

The portal is **not** a fixed app: it is a set of packages any React app can use
([ADR-0008](../decisions/0008-community-portal-sdk/)).

| Package | Role |
|---|---|
| `@cicord/client` | Typed, framework-agnostic client for the runtime API (auth, members, roles, plugin routes). |
| `@cicord/react` | Hooks and headless components (`useMember`, `useRoles`, `<OnboardingFlow>`). |
| `@cicord/ui` | Optional styled components driven by the server's theme. |
| `create-cicord-portal` | The reference portal, meant to be forked. |

Plugins expose portal routes (`definePlugin({routes})`, served under
`/api/v1/portal/{server}/plugins/{bot}/{instance}/…`), with typed route contracts; portals
consume them through the SDK and render them as they like. There is no server-driven UI, and
plugin JavaScript is never injected at run time.

The runtime serves the default portal at `/`, or the committed build of the repository's own
portal (`cicord portal init`, `cicord.yaml` `spec.portal.path`) from the active revision. Sites
on other origins embed the SDK in token mode (`spec.portal.allowedOrigins`, portal-scoped bearer
tokens); `--cors-origin` also allows cookie sessions. No-code options (`spec.portal`: texts,
banner, links, pages, locale) need no fork. See [Community portal](../../portal/overview/).

A typical onboarding: a member joins, a plugin sends a DM with a button,
`ctx.portal.link("/onboarding")` produces a signed, short-lived URL, the themed portal opens,
the member picks roles and answers questions, and the plugin applies the roles. Running the
portal *inside* Discord (Embedded App SDK, Activities) is a proposed option
([ADR-0016](../decisions/0016-portal-as-discord-activity/)).

## Security model

- **Secrets** never appear in YAML: `secretRef: DISCORD_TOKEN_MODERATOR` resolves from the
  environment, then from the runtime's encrypted secret store.
- **Plugin capabilities** are declared in the manifest and must be granted explicitly in
  `bot.yaml`. A plugin update that requests a new capability fails validation until granted.
- **Supply chain**: `cicord.lock` pins plugin commits and content hashes; the official
  registry's index is signed.
- **Write-back** uses `CICORD_GIT_TOKEN` (a repository-scoped token) or a GitHub App
  (`CICORD_GITHUB_APP_*`: installation tokens restricted to the configuration repository,
  renewed before they expire; the App takes precedence).
- **HTTP API**: session cookies (HttpOnly, `SameSite=Lax`, `Secure` over https), the CSRF
  header plus an Origin check, link sessions scoped to one server, an optional private admin
  host, dev mode bound to loopback hosts and dev login only with `--sim` or the runtime token.
  Checklist: [Security checklist](../../self-hosting/security/).
- **Destructive operations** (deleting roles or channels) require `prune: true` and are blocked
  by `deletionProtection` unless it is explicitly disabled.

## Testing strategy

A **Discord simulator** (`internal/sim`) is an in-process fake that implements the
`internal/discord` interfaces (REST operations and gateway events), not an HTTP-level fake
([ADR-0012](../decisions/0012-discord-simulator/)). It backs:

- the unit and integration tests of the reconciler, the runtime and the API;
- `cicord dev --sim` offline, with the admin console's Simulator panel;
- end-to-end checks of plugins against a seeded world.

The disgo adapter is tested separately against an HTTP test server
([ADR-0014](../decisions/0014-discord-library/)). Plugin unit tests use `@cicord/sdk/testing`
(a fake context, no runtime needed). See also [Contributing](../contributing/#how-things-are-tested).

## Source layout

```text
cmd/cicord/                  single binary: CLI + `cicord serve`
internal/
  analytics/                 server activity collector, queries and summaries
  api/                       HTTP API v1, auth, event stream, simulator endpoints, static UIs
  auth/                      signed portal link tokens, random IDs
  build/                     esbuild bundling of plugins
  catalog/                   official plugins bundled in the binary
  cli/                       cobra commands (init, validate, dev, serve, plugin…)
  config/                    loading, JSON Schema + semantic validation, plugin config
  discord/                   Discord interfaces and vocabulary (the only place saying "guild")
  discord/disgoadapter/      implementation over disgo rest + gateway
  gitauth/                   repository credentials: token or GitHub App, commit statuses
  importer/                  live server → servers/<key>.yaml (`cicord import`)
  metrics/                   Prometheus instrumentation (`--metrics-listen`)
  planfmt/                   plan rendering for the CLI and pull request comments
  plugin/                    plugin host: definitions, capabilities, budgets (qjs/: QuickJS-ng wasm on wazero)
  reconcile/                 server plan / apply / drift
  registry/                  git registries, cicord.lock resolution, signed indexes
  runtime/                   controller: revisions, hot swap, bot host, status
  secrets/                   encrypted secret store
  setup/                     setup wizard
  sim/                       in-process Discord simulator
  source/                    local and git sources, revisions
  state/                     store interfaces; sqlite/ and postgres/ backends, statetest/ conformance suite
  store/                     the plugin Store: sources, installs, updates
  typegen/                   types generated from plugin config schemas and route contracts
  version/                   build version
  webui/                     embedded admin console and default portal (copied by scripts/sync-ui.sh)
  writeback/                 YAML node patching, commits, pull requests
packages/                    JS workspace (pnpm + Vite+)
  sdk/                       @cicord/sdk: plugin authoring API (types = spec/sdk) + testing helpers
  client/                    @cicord/client: typed API client
  react/                     @cicord/react: hooks and headless components
  ui/                        @cicord/ui: themed components
  admin/                     admin console (embedded into the binary)
  create-cicord-portal/      reference portal (embedded as the default portal)
spec/                        JSON Schemas and the SDK type contract
examples/                    example Cicord repositories (basic, showcase, embed)
scripts/                     release, UI sync, QuickJS wasm build, official registry tooling
deploy/vps/                  hardened single-server install
sites/                       the landing page and this documentation
```

## Decisions

Each design choice above is recorded, with its context and trade-offs, in the
[architecture decision records](../decisions/).
