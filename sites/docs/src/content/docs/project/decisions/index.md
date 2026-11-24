---
title: Architecture decision records
description: The architecture decision records (ADRs) of Cicord - one decision each, with its status, context and consequences.
sidebar:
  label: Overview
  order: 0
---

Each ADR records one decision: **Status · Context · Decision · Consequences**.
Statuses: `Proposed` (not decided yet), `Accepted`, `Superseded by ADR-XXXX`.

| # | Decision | Status |
|---|---|---|
| [0001](0001-single-runtime-controller/) | Single Go binary; the runtime is a GitOps controller hosting N bots × M servers | Accepted |
| [0002](0002-self-hosted-first/) | Self-hosted first, SaaS-ready design | Accepted |
| [0003](0003-state-in-runtime-db/) | Reconciliation state in the runtime DB, stable keys, drift via gateway | Accepted |
| [0004](0004-git-write-back/) | UI changes become git commits; config in git, data in DB | Accepted |
| [0005](0005-typescript-plugins/) | Plugins in TypeScript, bundled by esbuild-in-Go (engine: superseded by 0018) | Accepted, engine superseded by ADR-0018 |
| [0006](0006-capability-based-permissions/) | Capability-based plugin permissions, granted explicitly | Accepted |
| [0007](0007-generic-admin-ui/) | Generic admin UI generated from JSON Schemas | Accepted |
| [0008](0008-community-portal-sdk/) | Community portal as layered SDKs + forkable template | Accepted |
| [0009](0009-server-vocabulary/) | "Server", never "guild", outside the Discord adapter | Accepted |
| [0010](0010-plugin-instance-per-bot-server/) | One plugin instance per (bot, server) | Accepted |
| [0011](0011-git-registry-and-lockfile/) | Git-based plugin registry + `cicord.lock` | Accepted |
| [0012](0012-discord-simulator/) | In-process Discord simulator behind the `discord` interfaces | Accepted |
| [0013](0013-licensing/) | Apache-2.0 for everything | Accepted |
| [0014](0014-discord-library/) | `disgo` (`rest` + `gateway` only) as the Discord library | Accepted |
| [0015](0015-yaml-k8s-style/) | Kubernetes-style YAML resources | Accepted |
| [0016](0016-portal-as-discord-activity/) | Community portal runnable as a Discord Activity | Proposed |
| [0017](0017-dev-mode-with-simulator/) | `cicord dev --sim`: local runtime on the simulator, loopback-only | Accepted |
| [0018](0018-quickjs-wasm-engine/) | Plugins run on QuickJS-ng compiled to WebAssembly (wazero): hard memory cap, killable CPU | Accepted |
