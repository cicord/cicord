# ADR-0006 — Capability-based plugin permissions

**Status:** Accepted

## Context
A community store means running third-party code with access to a Discord server. Users
must see and approve what a plugin can do, and updates must not silently widen access.

## Decision
- `plugin.yaml` declares `spec.capabilities` (e.g. `discord.messages.send`, `storage.kv`,
  `http.fetch`) and, for `http.fetch`, an allow-list of hosts.
- `bot.yaml` must list the same capabilities under `grants` for each plugin instance.
  Validation fails if a requested capability is not granted.
- `codi.lock` records the capabilities of each locked version, so a diff in the lockfile
  shows a permission change in code review.
- Every host call is checked against the instance's granted capabilities at runtime.
- Gateway intents are declared in the manifest and aggregated per bot; a handler that needs an
  undeclared intent is a build error.

## Consequences
- Plugin authors must keep the manifest accurate; the builder enforces it.
- The admin UI shows capabilities at install time (store UX similar to mobile app permissions).
- Finer scoping (e.g. "may only assign roles listed in config") is a later extension.
