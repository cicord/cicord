# CODI — Architecture

> **CODI** — *CI/CD Oriented Discord Integration*.
> GitOps for Discord: servers and bots are declared in a git repository, a runtime
> reconciles them continuously, and behaviour is extended with TypeScript plugins.

Status: **v0.1, implemented (pre-release)** — decisions are recorded in
[`docs/adr/`](docs/adr/README.md), the contracts live in [`spec/`](spec/README.md) and
[`spec/api.md`](spec/api.md). Items not built yet are marked *(planned)*; see the
[roadmap](#12-roadmap).

---

## 1. Goals

1. **Declarative.** A Discord server (roles, categories, channels, permissions) and the bots
   that live on it are described in YAML files, Kubernetes style (`apiVersion` / `kind` /
   `metadata` / `spec`).
2. **GitOps.** Git is the single source of truth for configuration. Every change —
   whether it comes from a developer's PR or from a click in the admin UI — is a commit.
3. **One engine, many bots.** Every bot deployed with CODI runs on the same engine. A single
   runtime hosts *N* bot identities across *M* servers.
4. **Extensible.** Features are plugins written in TypeScript, sandboxed, with explicit
   capabilities, installable from a community registry.
5. **Two UIs.** A generic admin UI (generated from schemas) for owners and moderators, and a
   community portal that is an SDK any React app can embed.
6. **Self-hosted first, SaaS later.** Nothing in the design assumes a single tenant
   or a specific host.

Non-goals for v1: sharding (CODI bots are mostly single- or few-server), voice/audio
processing, a visual plugin builder.

---

## 2. Big picture

```
            ┌──────────────────────────── git repository ─────────────────────────────┐
            │ codi.yaml   servers/*.yaml   bots/<name>/bot.yaml   plugins/   codi.lock │
            └───────▲──────────────────────────────┬──────────────────────────────────┘
     write-back     │ commits (go-git push,        │ webhook / poll
     (UI changes)   │ or embedded go-git)          ▼
┌───────────────────┴──────────────────────────────────────────────────────────────────┐
│                                 codi serve  (runtime)                                │
│                                                                                      │
│  ┌───────────┐   ┌────────────┐   ┌──────────────┐   ┌──────────────────────────┐    │
│  │  Source   │──▶│  Loader &  │──▶│   Builder    │──▶│ Revision (immutable)      │    │
│  │ (source)  │   │ (config)   │   │ (esbuild-go) │   │ config + plugin bundles   │    │
│  └───────────┘   └────────────┘   └──────────────┘   └──────┬───────────────┬────┘    │
│                                                             │               │         │
│                                        ┌────────────────────▼───┐   ┌───────▼──────┐  │
│                                        │   Server reconciler    │   │   Bot host   │  │
│                                        │ plan · apply · drift   │   │ N gateway    │  │
│                                        └───────────┬────────────┘   │ sessions     │  │
│                                                    │                │  ┌─────────┐ │  │
│  ┌──────────────┐   ┌────────────────────────┐     │                │  │ Plugin  │ │  │
│  │ Write-back   │◀──│        HTTP API        │     │                │  │ host    │ │  │
│  │ yaml patch + │   │ admin · portal · CLI   │     │                │  │ (Sobek) │ │  │
│  │ commit       │   └───────▲────────▲───────┘     │                │  └─────────┘ │  │
│  └──────────────┘           │        │             │                └──────┬───────┘  │
│         ┌─────────────────────────────────────────────────────────────────▼───────┐  │
│         │  State store (SQLite):   resource map · KV · audit · sessions · history │  │
│         └─────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────┼────────┼─────────────┼────────────────────────────────┘
                               │        │             │ Discord REST + Gateway
                     ┌─────────┴──┐  ┌──┴──────────┐  ▼
                     │  Admin UI  │  │  Community  │  Discord
                     │ (embedded) │  │   portal    │
                     └────────────┘  │ (SDK-based) │
                                     └─────────────┘
```

Everything ships as **one Go binary, `codi`**: the CLI *and* the runtime (`codi serve`).
The admin UI is embedded in the binary. See [ADR-0001](docs/adr/0001-single-runtime-controller.md).

---

## 3. Concepts

| Concept | File | What it is |
|---|---|---|
| **Project** | `codi.yaml` | Repository-level settings: engine version, source, registries, write-back policy, portal. |
| **Server** | `servers/<key>.yaml` | The desired state of one Discord server: roles, categories, channels, permissions, sync policy, UI access, portal theme. |
| **Bot** | `bots/<key>/bot.yaml` | One Discord application (one token, one identity), the servers it joins and the plugins it runs. |
| **Plugin** | `plugin.yaml` + `src/` | A TypeScript module with a static manifest (capabilities, intents, config schema). Local or from a registry. |
| **Plugin instance** | — | A plugin running for one *(bot, server)* pair, with its own config and storage. |
| **Revision** | — | An immutable, validated, built snapshot of the repo at a git commit. The runtime always runs exactly one active revision. |

Vocabulary: CODI says **server**, never *guild*. The word *guild* only appears inside the
Discord adapter (`internal/discord`). See [ADR-0009](docs/adr/0009-server-vocabulary.md).

### Stable keys

Every resource is identified by a **key** that never changes, separate from the display name
shown in Discord:

```yaml
channels:
  general:            # ← key (stable, used for references and state)
    name: général     # ← display name (can change freely: a rename, not a recreate)
    category: community
```

References between resources, and from plugin config to server resources, always use keys.
See [ADR-0003](docs/adr/0003-state-in-runtime-db.md).

---

## 4. Runtime components

### 4.1 Source controller (`internal/source`)
- Providers (`codi.yaml` `spec.source.provider`): `github`, `git` (any HTTPS remote,
  `CODI_GIT_TOKEN`) and `embedded` (a bare repository in the data dir, seeded from the
  served directory); `codi serve --local` and `codi dev` use the working directory itself.
- Git sources are fetched with go-git (pure Go, no `git` binary): **polling** (default 1 min,
  `spec.source.pollInterval`) plus **webhooks** (`/hooks/github`) and `POST /api/v1/sync`
  for immediate fetches. The local source watches files and hashes the content.
- Produces an immutable snapshot directory per commit SHA (or content hash): a revision.

### 4.2 Loader & validator
1. Parse YAML, validate each document against its JSON Schema (`spec/schemas`).
2. Semantic validation: references resolve (`category: community` exists), keys are unique,
   plugin config validates against the plugin's `config.schema.json`, every capability a
   plugin requests is granted in `bot.yaml`, the engine version is compatible.
3. The same code runs in `codi validate` (CLI, offline), in the runtime and in the config
   write-back (`PUT …/config`, spec/api.md §5).

### 4.3 Builder
- Resolves plugins from `codi.lock` (registry plugins, `internal/registry`) or the repo
  (local plugins).
- Bundles TypeScript with **esbuild used as a Go library** (`internal/build`; ES2022, inline
  source maps, `@codi/sdk` as the only external) — no Node.js required anywhere.
- Loads each bundle once to extract its static definition (commands, handlers, API routes),
  checks it against the manifest (e.g. a `memberJoin` handler requires the `ServerMembers` intent).

### 4.4 Server reconciler
- **plan**: diff desired state (revision) vs. live state (Discord API + resource map).
- **apply**: ordered, rate-limit aware operations (roles → categories → channels → overwrites
  → ordering), resumable after a failure.
- **drift**: the bot host forwards gateway events (`CHANNEL_UPDATE`, `ROLE_DELETE`, …) so
  manual changes in Discord are detected in real time. Per server `syncPolicy` decides:
  report only, or `selfHeal` (revert).
- **adoption**: a declared resource with no mapping adopts an existing Discord resource with
  the same name (`adopt`), so pointing CODI at an existing server does not duplicate it.
- **ownership**: only resources present in the resource map are ever modified. Resources
  never declared are ignored. Resources created at runtime by plugins (e.g. ticket channels)
  are recorded as owned by that plugin instance and never pruned by the reconciler. A key
  removed from YAML is deleted (`prune`), blocked (`deletionProtection`) or just dropped
  from the map (`unmanage`). The exact rules are in [spec/README.md §3](spec/README.md).

### 4.5 Bot host
- One gateway session per bot. Intents = union of the intents required by its plugin
  instances (+ what the reconciler needs).
- Syncs slash commands (per server) from the plugins' static definitions.
- Routes events and interactions to plugin instances.
- **Hot swap** on a new revision: new plugin VMs are started, validated, then traffic is
  switched atomically per bot; old VMs are drained. No gateway reconnect, no lost events.
  Storage persists across revisions.

### 4.6 Plugin host
- JavaScript engine: **Sobek** (pure Go, async/await) running the esbuild bundle
  (`internal/plugin`). The package boundary is the seam for another backend
  (QuickJS-on-wasm, WASM) for stronger multi-tenant isolation; an interface will be
  extracted when a second backend exists. See [ADR-0005](docs/adr/0005-typescript-plugins.md).
- One isolated VM per plugin instance, a serialized event queue per VM.
- Capability enforcement on every host call ([ADR-0006](docs/adr/0006-capability-based-permissions.md)).
- CPU budget per handler (interrupt), wall-clock timeouts, no filesystem/network except
  through capabilities (`http.fetch` with an allow-list). Budgets and limits:
  [spec/README.md §8](spec/README.md), [docs/plugins.md](docs/plugins.md).
- **Interaction deadline**: Discord requires an answer within 3 s. If a handler has not
  replied after ~2 s, the host defers automatically; a later `reply()` transparently edits
  the deferred response.

### 4.7 State store
SQLite (`internal/state/sqlite`, pure Go driver; a single file in the data dir, in memory
for `codi dev --sim`) behind the `internal/state` interfaces; Postgres *(planned)*. Holds:
- resource map: `(server key, kind, resource key) → Discord ID`, plus owner (git / plugin instance);
- plugin KV storage, scoped per plugin instance;
- audit log (moderation actions, UI actions), sessions, sync history.

**Rule: configuration lives in git, data lives in the DB.** XP, warnings, tickets, member
preferences never go to git. See [ADR-0004](docs/adr/0004-git-write-back.md).

### 4.8 HTTP API
`internal/api`, contract in [spec/api.md](spec/api.md). Serves the admin UI, the community
portal SDK and the CLI (`codi plan --remote`, `codi status`), plus the simulator endpoints
of `codi dev --sim`. Auth via Discord OAuth2, portal links (`ctx.portal.link()`, scoped to one
server) and the runtime bearer token; authorization derived from Discord roles
(`spec.access` in the Server). Plugins can expose their own routes to the portal. CSRF
protection (`X-Codi-Request` + Origin check) and, in dev mode, a loopback-only Host check.

### 4.9 Write-back
When a change is made in the admin UI:

```
UI edit ─▶ API validates against schema ─▶ patch YAML node tree (yaml.v3, comments kept)
        ─▶ commit on the latest branch head (author codi[bot], trailers Codi-Actor / Codi-Source)
        ─▶ policy: direct commit  → runtime activates its own SHA immediately
                   pull request   → change waits for review/merge, then normal sync
        ─▶ conflict: the edit is re-applied on the latest head; if it no longer applies → 409
```

`internal/writeback` pushes with go-git and `CODI_GIT_TOKEN`; pull requests use the GitHub
API (GitHub App credentials *(planned)*). `codi dev` writes to the working directory. The
runtime never applies a change that is not committed first.

---

## 5. Sync lifecycle

```
commit ─▶ fetch ─▶ validate ─▶ build ─▶ plan ─▶ apply servers ─▶ activate bots ─▶ report
                     │            │                │                  │             │
                  rejected     rejected         failed            rolled back   history
                  (status)     (status)         (resumable)       to previous    + UI status
```

Each server and bot exposes a status, ArgoCD style:

| Sync | Health |
|---|---|
| `Synced` · `OutOfSync` · `Unknown` | `Healthy` · `Progressing` · `Degraded` · `Missing` |

The active revision only changes when validation and build succeed. A failed apply on one
server does not block others. Every activation, rejection and server sync is recorded in
the sync history (`GET /api/v1/history`); GitHub commit statuses are *(planned)*.

---

## 6. CI/CD flow

| Event | CI step | Runtime |
|---|---|---|
| Pull request | `codi validate` (offline) + `codi plan --remote` posted as a PR comment | answers the plan request, nothing applied |
| Merge to `main` | — (optional: `codi status --wait`) | webhook → sync (commit status on the SHA: *planned*) |
| UI change | — | commits, then activates (or opens a PR, by policy) |

A GitHub Actions template generated by `codi init` is *(planned)*.

---

## 7. CLI

| Command | Purpose |
|---|---|
| `codi init` | Scaffold a repo (`codi.yaml`, `servers/`, `bots/`). |
| `codi validate` | Schema + semantic validation, offline (`-o json`). |
| `codi plan [--remote]` | Diff with the live server(s); `--remote` asks the runtime (`POST /api/v1/plan`). |
| `codi apply` | Reconcile directly (local/dev use; in production the runtime applies). |
| `codi status [--wait]` | Sync/health of servers and bots (`GET /api/v1/status`). |
| `codi sync` | Ask the runtime to fetch and sync now (`POST /api/v1/sync`). |
| `codi import <server-id>` | Generate `servers/<key>.yaml` from an existing server. |
| `codi adopt` | Rebuild the resource map by matching names after state loss. |
| `codi mv <server> <kind> <old> <new>` | Rename a resource key and every reference to it (YAML rewritten in place, local resource map moved; the runtime moves its mapping by name). |
| `codi dev [--sim]` | Run the runtime locally with hot reload, against real Discord or the simulator. |
| `codi serve [--local]` | Run the runtime. |
| `codi plugin new / sdk-types` | Scaffold a plugin; write the SDK types for editors. |
| `codi plugin add` | Add a registry plugin to a bot and lock it in `codi.lock`. |
| `codi plugin build / typegen / test` | Bundle a plugin, generate config types from its schema, run its tests. |
| `codi plugin publish` | *(planned)* |

`codi <command> --help` is authoritative.

---

## 8. User interfaces

### Admin UI (generic, embedded)
- Shipped prebuilt inside the binary. Not customized per deployment.
- Server overview, sync status, drift, history (git log of the config), audit log.
- **Plugin settings pages generated from each plugin's `config.schema.json`.** CODI schema
  extensions (`x-codi-ref: channel | role | category`, `x-codi-widget`, `x-codi-access`)
  render pickers and restrict moderator edits.
- Every save goes through write-back (§4.9).
- In `codi dev --sim`, a **Simulator** panel plays Discord users (join, messages, slash
  commands, buttons, selects, modals, DMs) against the in-memory world
  ([ADR-0017](docs/adr/0017-dev-mode-with-simulator.md)).

### Community portal (SDK)
The portal is **not** a fixed app: it is a set of packages any React app can use.

| Package | Role |
|---|---|
| `@codi/client` | Typed, framework-agnostic client for the runtime API (auth, members, roles, plugin routes). |
| `@codi/react` | Hooks + headless components (`useMember`, `useRoles`, `<OnboardingFlow>`). |
| `@codi/ui` | Optional styled components driven by the server's theme. |
| `create-codi-portal` | The demo / reference portal, meant to be forked. |

Plugins expose portal routes (`definePlugin({routes})`, served under
`/api/v1/portal/{server}/plugins/{bot}/{instance}/…`); portals consume them through the SDK
(`client.plugin(...)`) and render them as they like — no server-driven UI, never plugin JS
injected at runtime. Generated route types are *(planned)*.

Hosting: the runtime serves the default portal (`create-codi-portal`) at `/`, or the
committed build of the repository's own portal (`codi portal init`, `codi.yaml
spec.portal.path`) from the active revision. Sites on other origins embed the SDK in token
mode (`spec.portal.allowedOrigins`, portal-scoped bearer tokens); `--cors-origin` also allows
cookie sessions. No-code options (`spec.portal`: texts, banner, links, pages, locale) need no
fork. See [docs/portal.md](docs/portal.md).

Typical onboarding: a member joins → a plugin sends a DM with a button →
`ctx.portal.link("/onboarding")` produces a signed, short-lived URL → the themed portal opens,
the member picks roles/answers questions → the plugin applies roles.
Running the portal *inside* Discord (Embedded App SDK / Activities) is a proposed option
([ADR-0016](docs/adr/0016-portal-as-discord-activity.md)).

---

## 9. Security model

- **Secrets** never appear in YAML: `secretRef: DISCORD_TOKEN_MODERATOR` resolves from the
  environment (self-hosted) or a secret store (SaaS).
- **Plugin capabilities** are declared in the manifest and must be granted explicitly in
  `bot.yaml`. A plugin update that requests a new capability fails validation until granted.
- **Supply chain**: `codi.lock` pins plugin commits and content hashes.
- **Write-back** uses `CODI_GIT_TOKEN` (a repo-scoped token); GitHub App credentials are
  *(planned)*.
- **HTTP API**: session cookies (HttpOnly, SameSite=Lax, Secure over https), CSRF header +
  Origin check, link sessions scoped to one server, dev mode bound to loopback hosts and
  dev login only with `--sim` or the runtime token. Checklist: [docs/deploy.md](docs/deploy.md).
- **Destructive operations** (deleting roles/channels) require `prune: true` and are blocked by
  `deletionProtection` unless explicitly disabled.

---

## 10. Testing strategy

A **Discord simulator** (`internal/sim`) is an in-process fake that implements the
`internal/discord` interfaces (REST operations and gateway events), not an HTTP-level fake.
It backs:
- unit/integration tests of the reconciler, the runtime and the API;
- `codi dev --sim` offline, with the admin UI's Simulator panel;
- end-to-end checks of plugins against a seeded world.

The disgo adapter is tested separately against an HTTP test server. Plugin unit tests use
`@codi/sdk/testing` (a fake context, no runtime needed). See
[ADR-0012](docs/adr/0012-discord-simulator.md).

---

## 11. Repository layout (CODI itself)

```
cmd/codi/                  single binary: CLI + `codi serve`
internal/
  api/                     HTTP API v1 (spec/api.md), auth, simulator endpoints, static UIs
  auth/                    signed portal link tokens, random IDs
  build/                   esbuild bundling of plugins
  cli/                     cobra commands (init, validate, dev, serve, plugin …)
  config/                  loading, JSON Schema + semantic validation, plugin config
  discord/                 Discord interfaces and vocabulary (only place saying "guild")
  discord/disgoadapter/    implementation over disgo rest + gateway (ADR-0014)
  plugin/                  Sobek plugin host: definitions, capabilities, budgets, scheduler
  reconcile/               server plan / apply / drift
  registry/                git registries, codi.lock resolution
  runtime/                 controller: revisions, hot swap, bot host, status
  sim/                     in-process Discord simulator
  source/                  local and git sources, revisions
  state/                   store interfaces; state/sqlite/ the SQLite store
  version/                 build version
  webui/                   embedded admin UI and default portal (copied by scripts/sync-ui.sh)
  writeback/               YAML node patching, commits, pull requests
packages/                  JS workspace (pnpm + Vite+)
  sdk/                     @codi/sdk — plugin authoring API (types = spec/sdk) + testing helpers
  client/                  @codi/client — typed API client
  react/                   @codi/react — hooks and headless components
  ui/                      @codi/ui — themed components
  admin/                   admin UI (embedded into the binary)
  create-codi-portal/      reference portal (embedded as the default portal)
spec/                      API contract, semantics, JSON Schemas, SDK type contract
docs/                      plugin guide, deployment guide, ADRs
examples/                  example CODI repositories
```

---

## 12. Roadmap

| Phase | Content | Status |
|---|---|---|
| **0 — Spec** | This document, ADRs, JSON Schemas, SDK contract, example repo. | Done |
| **1 — Vertical slice** | `codi` binary, config loader/validator, simulator, bot host, Sobek plugin host, KV, slash commands, `codi dev`. | Done |
| **2 — Server reconciler** | Resource map, plan/apply, drift + selfHeal, adoption, `import` / `adopt` / `mv`, GitHub Actions template. | Reconciler done; CLI commands in progress; template planned |
| **3 — GitOps runtime** | Source controller, revisions + hot swap, webhooks, `plan --remote`, commit statuses. | Done except commit statuses; `codi plan --remote` client in progress |
| **4 — Admin UI + write-back** | HTTP API spec, OAuth, generated settings pages, write-back (commit / PR), simulator panel. | Done (GitHub App credentials planned) |
| **5 — Portal SDK** | `@codi/client`, `@codi/react`, `@codi/ui`, `create-codi-portal`, onboarding flow. | Done |
| **6 — Registry** | Git-based index, `codi plugin add` + `codi.lock`, `codi plugin publish`, verified publishers. | `add` done; publishing planned |
| **7 — SaaS** | Multi-tenant runtime, stronger isolation backend, Postgres, managed git, billing. | Not started |

Next specs to write: **registry index format**, **revision/bundle format**.
