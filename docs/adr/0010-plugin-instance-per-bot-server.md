# ADR-0010 — One plugin instance per (bot, server)

**Status:** Accepted

## Context
A bot can join several servers, and several bots can run the same plugin. Config and data
must not leak between servers.

## Decision
- A **plugin instance** is a plugin running for exactly one *(bot, server)* pair.
- In `bot.yaml`, a plugin entry has a base `config` and optional per-server `overrides`
  (JSON merge patch). `servers` restricts which of the bot's servers the plugin runs on.
- Storage (KV), schedules and runtime-owned resources are scoped to the instance.
- Each instance gets its own VM (ADR-0005).

## Consequences
- Memory grows with bots × servers × plugins; acceptable for the target scale, to be
  optimized (VM pooling) if needed.
- Cross-server features (a network of servers) need an explicit future capability.
