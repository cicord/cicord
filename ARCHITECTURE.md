# CODI — Architecture

> **CODI** — *CI/CD Oriented Discord Integration*.
> GitOps for Discord: servers and bots are declared in a git repository, a runtime
> reconciles them continuously, and behaviour is extended with TypeScript plugins.

Status: **draft v0.1** — decisions are recorded in [`docs/adr/`](docs/adr/README.md),
the contracts live in [`spec/`](spec/README.md).

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
     write-back     │ commits (GitHub App          │ webhook / poll
     (UI changes)   │ or embedded go-git)          ▼
┌───────────────────┴──────────────────────────────────────────────────────────────────┐
│                                 codi serve  (runtime)                                │
│                                                                                      │
│  ┌───────────┐   ┌────────────┐   ┌──────────────┐   ┌──────────────────────────┐    │
│  │  Source   │──▶│  Loader &  │──▶│   Builder    │──▶│ Revision (immutable)      │    │
│  │ controller│   │ validator  │   │ (esbuild-go) │   │ config + plugin bundles   │    │
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
│         │  State store (SQLite / Postgres): resource map · KV · audit · sessions  │  │
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

### 4.1 Source controller
- Watches the configured repo/branch: **webhook** (push events) with **polling** as a fallback.
- In "embedded git" mode (no GitHub), the repo lives inside the runtime (go-git) and the CLI /
  UI push to it. The pipeline is identical: everything is a commit.
- Produces a candidate revision per commit SHA.

### 4.2 Loader & validator
1. Parse YAML, validate each document against its JSON Schema (`spec/schemas`).
2. Semantic validation: references resolve (`category: community` exists), keys are unique,
   plugin config validates against the plugin's `config.schema.json`, every capability a
   plugin requests is granted in `bot.yaml`, the engine version is compatible.
3. The same code runs in `codi validate` (CLI, offline) and in the runtime.

### 4.3 Builder
- Resolves plugins from `codi.lock` (registry plugins) or the repo (local plugins).
- Bundles TypeScript with **esbuild used as a Go library** — no Node.js required anywhere.
- Loads each bundle once to extract its static definition (commands, handlers, API routes),
  checks it against the manifest (e.g. a `memberJoin` handler requires the `ServerMembers` intent).

### 4.4 Server reconciler
- **plan**: diff desired state (revision) vs. live state (Discord API + resource map).
- **apply**: ordered, rate-limit aware operations (roles → categories → channels → overwrites
  → ordering), resumable after a failure.
- **drift**: the bot host forwards gateway events (`CHANNEL_UPDATE`, `ROLE_DELETE`, …) so
  manual changes in Discord are detected in real time. Per server `syncPolicy` decides:
  report only, or `selfHeal` (revert).
- **ownership**: only resources present in the resource map are ever modified. Resources
  never declared are ignored. Resources created at runtime by plugins (e.g. ticket channels)
  are recorded as owned by that plugin instance and never pruned by the reconciler.

### 4.5 Bot host
- One gateway session per bot. Intents = union of the intents required by its plugin
  instances (+ what the reconciler needs).
- Syncs slash commands (per server) from the plugins' static definitions.
- Routes events and interactions to plugin instances.
- **Hot swap** on a new revision: new plugin VMs are started, validated, then traffic is
  switched atomically per bot; old VMs are drained. No gateway reconnect, no lost events.
  Storage persists across revisions.

### 4.6 Plugin host
- JavaScript engine: **Sobek** (pure Go, ESM, async/await), behind a language-agnostic
  `PluginRuntime` interface so a QuickJS-on-wasm or WASM backend can be added for stronger
  multi-tenant isolation. See [ADR-0005](docs/adr/0005-typescript-plugins.md).
- One isolated VM per plugin instance, a serialized event queue per VM.
- Capability enforcement on every host call ([ADR-0006](docs/adr/0006-capability-based-permissions.md)).
- CPU budget per handler (interrupt), wall-clock timeouts, no filesystem/network except
  through capabilities (`http.fetch` with an allow-list).
- **Interaction deadline**: Discord requires an answer within 3 s. If a handler has not
  replied after ~2 s, the host defers automatically; a later `reply()` transparently edits
  the deferred response.

### 4.7 State store
SQLite by default (single file, zero ops), Postgres optional. Holds:
- resource map: `(server key, kind, resource key) → Discord ID`, plus owner (git / plugin instance);
- plugin KV storage, scoped per plugin instance;
- audit log (moderation actions, UI actions), sessions, sync history.

**Rule: configuration lives in git, data lives in the DB.** XP, warnings, tickets, member
preferences never go to git. See [ADR-0004](docs/adr/0004-git-write-back.md).

### 4.8 HTTP API
Serves the admin UI, the community portal SDK and the CLI (`codi plan --remote`, `codi status`).
Auth via Discord OAuth2; authorization derived from Discord roles (`spec.access` in the Server).
Plugins can expose their own routes to the portal. *The API contract is the next spec to write.*

### 4.9 Write-back
When a change is made in the admin UI:

```
UI edit ─▶ API validates against schema ─▶ patch YAML node tree (yaml.v3, comments kept)
        ─▶ commit on top of the active SHA (author codi[bot], trailer Codi-Actor)
        ─▶ policy: direct commit  → runtime activates its own SHA immediately
                   pull request   → change waits for review/merge, then normal sync
        ─▶ conflict (branch moved): rebase if trivial, otherwise open a PR
```

The runtime never applies a change that is not committed first.

---

## 5. Sync lifecycle

```
commit ─▶ fetch ─▶ validate ─▶ build ─▶ plan ─▶ apply servers ─▶ activate bots ─▶ report
                     │            │                │                  │             │
                  rejected     rejected         failed            rolled back   commit status
                  (status)     (status)         (resumable)       to previous    + UI status
```

Each server and bot exposes a status, ArgoCD style:

| Sync | Health |
|---|---|
| `Synced` · `OutOfSync` · `Unknown` | `Healthy` · `Progressing` · `Degraded` · `Missing` |

The active revision only changes when validation and build succeed. A failed apply on one
server does not block others.

---

## 6. CI/CD flow

| Event | CI step | Runtime |
|---|---|---|
| Pull request | `codi validate` (offline) + `codi plan --remote` posted as a PR comment | answers the plan request, nothing applied |
| Merge to `main` | — (optional: `codi status --wait`) | webhook → sync → commit status on the SHA |
| UI change | — | commits, then activates (or opens a PR, by policy) |

`codi init` generates the GitHub Actions workflow.

---

## 7. CLI

| Command | Purpose |
|---|---|
| `codi init` | Scaffold a repo (`codi.yaml`, `servers/`, `bots/`, CI workflow). |
| `codi validate` | Schema + semantic validation, offline. |
| `codi plan [--remote]` | Diff with the live server(s). |
| `codi apply` | Reconcile directly (local/dev use; in production the runtime applies). |
| `codi import <server-id>` | Generate `servers/<key>.yaml` from an existing server. |
| `codi adopt` | Rebuild the resource map by matching names after state loss. |
| `codi mv <old-key> <new-key>` | Rename a resource key without delete + create (rewrites YAML and the resource map). |
| `codi dev` | Run the runtime locally against a test server (or the simulator) with hot reload. |
| `codi serve` | Run the runtime. |
| `codi status` | Sync/health of servers and bots. |
| `codi plugin new / build / add / publish / typegen` | Plugin lifecycle. |

---

## 8. User interfaces

### Admin UI (generic, embedded)
- Shipped prebuilt inside the binary. Not customized per deployment.
- Server overview, sync status, drift, history (git log of the config), audit log.
- **Plugin settings pages generated from each plugin's `config.schema.json`.** CODI schema
  extensions (`x-codi-ref: channel | role | category`) render channel/role pickers.
- Every save goes through write-back (§4.9).

### Community portal (SDK)
The portal is **not** a fixed app: it is a set of packages any React app can use.

| Package | Role |
|---|---|
| `@codi/client` | Typed, framework-agnostic client for the runtime API (auth, members, roles, plugin routes). |
| `@codi/react` | Hooks + headless components (`useMember`, `useRoles`, `<OnboardingFlow>`). |
| `@codi/ui` | Optional styled components driven by the server's theme. |
| `create-codi-portal` | The demo / reference portal, meant to be forked. |

Plugins can be full-stack: a server part (runs in the runtime) and a client part (React
components). Types for plugin API routes are generated.

Hosting: if the repo has a `portal/` directory, CI builds it and the runtime serves the
output; otherwise the runtime serves the default portal. An external host (Vercel, …) works
with CORS configured.

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
- **Write-back** uses a GitHub App with repo-scoped, short-lived tokens.
- **Destructive operations** (deleting roles/channels) require `prune: true` and are blocked by
  `deletionProtection` unless explicitly disabled.

---

## 10. Testing strategy

A **Discord simulator** (`internal/sim`) implements the subset of the REST API and gateway
CODI uses. It backs:
- unit/integration tests of the reconciler and the bot host;
- plugin tests (`codi plugin test`) without a real server;
- `codi dev --sim` offline;
- conformance tests that agents and contributors run against the spec.

See [ADR-0012](docs/adr/0012-discord-simulator.md).

---

## 11. Repository layout (CODI itself)

```
cmd/codi/               single binary: CLI + `codi serve`
internal/
  config/               loading, JSON Schema + semantic validation
  source/               git fetch, webhooks, embedded repo, revisions
  build/                esbuild bundling of plugins
  reconcile/            server plan / apply / drift
  bothost/              gateway sessions, command sync, interaction routing
  plugin/               PluginRuntime interface, Sobek backend, capabilities, scheduler
  state/                SQLite/Postgres store
  writeback/            YAML node patching, commits, PRs
  api/                  HTTP API
  discord/              adapter over the Discord library (only place saying "guild")
  sim/                  Discord simulator
packages/               JS workspace (Vite+)
  sdk/                  @codi/sdk — plugin authoring API (types = spec/sdk)
  client/  react/  ui/  community portal SDK
  admin/                admin UI (embedded into the binary)
  create-codi-portal/   reference portal
spec/                   JSON Schemas + SDK type contract
docs/adr/               architecture decision records
examples/               example CODI repositories
```

---

## 12. Roadmap

| Phase | Content |
|---|---|
| **0 — Spec** | This document, ADRs, JSON Schemas, SDK contract, example repo. ← *we are here* |
| **1 — Vertical slice** | `codi` binary, config loader/validator, simulator, bot host, Sobek plugin host, KV, slash commands, `codi dev`. |
| **2 — Server reconciler** | Resource map, plan/apply, drift + selfHeal, `import` / `adopt`, GitHub Actions template. |
| **3 — GitOps runtime** | Source controller, revisions + hot swap, commit statuses, `plan --remote`. |
| **4 — Admin UI + write-back** | HTTP API spec, OAuth, generated settings pages, GitHub App write-back. |
| **5 — Portal SDK** | `@codi/client`, `@codi/react`, `@codi/ui`, `create-codi-portal`, onboarding flow. |
| **6 — Registry** | Git-based index, `codi plugin publish`, verified publishers. |
| **7 — SaaS** | Multi-tenant runtime, stronger isolation backend, managed git, billing. |

Next specs to write: **runtime HTTP API**, **registry index format**, **revision/bundle format**.
