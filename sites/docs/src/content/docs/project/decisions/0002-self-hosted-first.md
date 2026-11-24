---
title: "ADR-0002 — Self-hosted first, SaaS-ready design"
description: "ADR-0002: Self-hosted first, SaaS-ready design."
sidebar:
  label: "0002 · Self-hosted first, SaaS-ready design"
  order: 2
---

**Status:** Accepted

## Context
The long-term business model is a hosted SaaS (open-source core, paid hosting). Building
multi-tenancy first would slow down the core and the community adoption.

## Decision
- v1 targets self-hosting: a single binary or Docker image, SQLite by default.
- Design rules that keep the SaaS possible:
  - no global singletons: everything is scoped by project → server/bot → plugin instance;
  - storage behind an interface (SQLite / Postgres); both exist now (`internal/state/sqlite`,
    `internal/state/postgres`, selected with `--database-url`), held to the same contract by
    the conformance suite `internal/state/statetest`;
  - plugin execution behind `PluginRuntime` so a stronger isolation backend can be added;
  - secrets resolved through a `SecretResolver` (env vars now, secret store later);
  - git source abstracted (GitHub App, generic git, embedded go-git).

## Consequences
- Multi-tenant concerns (quotas, billing, tenant isolation) are deferred but not blocked.
- Some features are designed twice-light: e.g. secret resolution has a trivial self-hosted
  implementation and a richer one later.
