---
title: Architecture
description: How the runtime is put together, and a summary of the architecture decision records (ADRs) behind it.
---

Everything ships as **one Go binary, `cicord`**: the command line tool and the runtime. Inside
the runtime:

```text
source (github · git · embedded · local) ─▶ loader & validator ─▶ builder (esbuild in Go)
       ─▶ revision (immutable: config + plugin bundles)
             ├─▶ server reconciler   plan · apply · drift          ─┐
             └─▶ bot host            one gateway session per bot    ├─▶ Discord REST + gateway
                   └─ plugin host    one Sobek VM per instance     ─┘
HTTP API ─ admin console · portal SDK · CLI ─▶ write-back (YAML patch + commit / pull request)
state store (SQLite): resource map · plugin KV · audit · sessions · history
```

| Component | Role |
|---|---|
| **Source controller** | Fetches the repository (go-git, no `git` binary): polling, GitHub webhooks and `POST /api/v1/sync`. Produces one immutable snapshot per commit. |
| **Loader and validator** | JSON Schemas, then semantic checks. The same code runs in `cicord validate`, in the runtime and before every admin console edit. |
| **Builder** | Resolves plugins (repository or `cicord.lock`), bundles TypeScript with esbuild used as a Go library, checks each bundle against its manifest. |
| **Server reconciler** | Plans and applies, in a rate-limit-aware order, adopts by name, detects drift from gateway events. |
| **Bot host** | One gateway session per bot with the union of its intents; syncs slash commands; routes events and interactions; hot-swaps plugin VMs on a new revision without reconnecting. |
| **Plugin host** | Sobek (pure-Go JavaScript with async/await): one VM and event loop per instance, capability checks on every host call, CPU and wall-clock budgets, auto-defer of interactions. |
| **State store** | SQLite (pure Go) in the data directory; in memory for `cicord dev --sim`. |
| **HTTP API** | Serves the admin console, the portal SDK and the CLI; OAuth, portal links, bearer tokens; CSRF protection. |
| **Write-back** | Patches YAML at the node level (comments kept), commits as `cicord[bot]`, pushes or opens a pull request. |

The Discord library is wrapped behind Cicord's own interfaces, so the rest of the code never
depends on it; the in-process **simulator** implements the same interfaces and backs
`cicord dev --sim`, `cicord plugin test` and the test suite.

## Decisions

The [ADRs](https://github.com/npjonath/cicord/tree/main/docs/adr) record each decision. In short:

| ADR | Decision | Status |
|---|---|---|
| 0001 | **Single Go binary; the runtime is a pull-based GitOps controller** hosting N bots × M servers, hot-swapping plugin code without dropping the gateway. CI only validates and plans. | Accepted |
| 0002 | **Self-hosted first, SaaS-ready**: one binary or Docker image with SQLite; everything scoped by project → server/bot → plugin instance so a hosted offering stays possible. | Accepted |
| 0003 | **Reconciliation state in the runtime database**: stable keys, a resource map (no state file in git), drift from gateway events, per-server `syncPolicy`, ownership rules; `cicord adopt` and `cicord import` for recovery. | Accepted |
| 0004 | **Admin console changes become git commits** (or pull requests by path policy), YAML patched with comments kept; configuration in git, data in the database. | Accepted |
| 0005 | **Plugins in TypeScript**, bundled by esbuild in Go and run by Sobek: no Node.js anywhere, Node built-ins refused, inline source maps. | Accepted |
| 0006 | **Capability-based plugin permissions**, granted explicitly in `bot.yaml`, recorded in `cicord.lock`, checked on every host call. | Accepted |
| 0007 | **Generic admin console generated from JSON Schemas**, with `x-cicord-ref`, `x-cicord-widget` and `x-cicord-access`; no per-deployment build. | Accepted |
| 0008 | **Community portal as layered SDKs plus a forkable reference portal**; custom builds committed and served from the revision; token mode for other origins; plugin UI through routes, never injected JavaScript. | Accepted |
| 0009 | **"Server", never "guild"**, outside the Discord adapter. | Accepted |
| 0010 | **One plugin instance per (bot, server)**, with base config and per-server overrides; storage and schedules scoped to the instance. | Accepted |
| 0011 | **Git-based plugin registries and `cicord.lock`**, apt/Homebrew-style sources with computed trust, and a signed official index with anti-rollback serials. | Accepted |
| 0012 | **In-process Discord simulator** behind Cicord's Discord interfaces, following Discord's observable behavior (permissions, hierarchy, intents, interaction deadlines). | Accepted |
| 0013 | **Licensing**: Apache-2.0 for everything (runtime, CLI, admin console, SDKs, official plugins, sites). | Accepted |
| 0014 | **disgo** (its `rest` and `gateway` packages only) as the Discord library, wrapped by an adapter; static `CGO_ENABLED=0` build, no sharding. | Accepted |
| 0015 | **Kubernetes-style YAML resources** (`apiVersion`, `kind`, `metadata`, `spec`), maps keyed by stable keys, JSON Schemas for editors. | Accepted |
| 0016 | **Portal runnable as a Discord Activity**, keeping signed links as the default. | Proposed |
| 0017 | **`cicord dev --sim`**: the full runtime on the simulator, seeded from YAML, loopback-only, with a Simulator panel and dev login. | Accepted |

Non-goals for v1: sharding, voice and audio processing, a visual plugin builder.
