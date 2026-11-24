---
title: "ADR-0001 — Single Go binary; the runtime is a GitOps controller"
description: "ADR-0001: Single Go binary; the runtime is a GitOps controller hosting N bots × M servers."
sidebar:
  label: "0001 · Single Go binary; the runtime is a GitOps cont…"
  order: 1
---

**Status:** Accepted

## Context
Cicord must deploy and keep in sync one or more Discord servers and one or more bot identities.
Two deployment models exist: *push* (CI builds and redeploys the bot) and *pull* (a controller
watches git and converges, like ArgoCD). Restarting a bot drops its gateway connection and
loses events.

## Decision
- One Go binary, `cicord`, contains the CLI and the runtime (`cicord serve`).
- The runtime is a **pull-based controller**: it watches the git repo, builds immutable
  revisions, reconciles servers and hot-swaps plugin code without reconnecting to the gateway.
- A single runtime hosts **N bots × M servers** (many-to-many). The standard case is one
  server and one bot; multiple personas (distinct Discord applications) are first-class.

## Consequences
- CI only validates and plans; it never needs Discord tokens for applying.
- The runtime is long-lived and stateful (see ADR-0003); it must handle revision switching
  atomically and roll back on failure.
- A separate binary for the CLI can be split out later if size matters.
