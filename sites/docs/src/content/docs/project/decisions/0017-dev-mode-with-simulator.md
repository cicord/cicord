---
title: "ADR-0017 — Dev mode with simulator"
description: "ADR-0017: cicord dev --sim: local runtime on the simulator, loopback-only."
sidebar:
  label: "0017 · Dev mode with simulator"
  order: 17
---

**Status:** Accepted

## Context
Writing a plugin or a server configuration should not require creating a Discord
application, a test server and a bot token, nor waiting on Discord's rate limits. The
simulator (ADR-0012) already implements the `internal/discord` interfaces in memory; the
question is how a developer uses it interactively, and how to keep a local dev runtime,
which lets anyone sign in as anyone, from becoming an attack surface.

## Decision
- **`cicord dev [dir] --sim`** runs the full runtime (reconciler, bot hosts, plugins, HTTP
  API, admin UI and portal) on a local working directory, with Discord replaced by an
  in-process `sim.World`. Without `--sim`, `cicord dev` uses real Discord with the tokens read
  from the environment (`token.secretRef`). Both modes hot reload: every saved file is a new
  revision, and admin UI edits are written back to the files (`writeback.DirWriter`).
- **Seeding from YAML** (`api.SeedSimWorld`, run once at startup from the loaded
  repository):
  - a user `owner` who owns every server; each server is created with its `discordId`
    (and `profile.name`);
  - every bot is created with its `applicationId` (and `profile.username`) and invited with
    `Administrator` to its servers; its simulated token is served to the runtime under the
    bot's `token.secretRef`, so the repository needs no change;
  - extra users `alice`, `bob` and `charlie`, not members of any server, to test joins;
  - servers start empty (only @everyone): the first sync creates roles, categories and
    channels from the YAML, like on a fresh real server.
- The simulator's IDs are new on every run, so the state DB of `cicord dev --sim` is in memory
  (`:memory:` SQLite); `cicord dev` without `--sim` keeps `<repo>/.cicord/dev/cicord.db`.
- **Admin Simulator panel**: the HTTP API exposes the world under `/api/v1/sim/*`
  ([Simulator API](../../../reference/http-api/simulator/): state, users, join/leave, messages, DMs, slash commands, components,
  modals, and an SSE `change` stream). The admin UI's Simulator panel uses it to pick an
  actor, chat in channels, run commands with typed options, click buttons, pick select
  values, fill modals and read DMs, rendering messages, embeds and components like Discord.
  The endpoints answer `404` without `--sim`.
- **Dev login**: `POST /auth/dev-login {"userId"}` creates a session for any user, so the
  admin UI and portal can be tried as the owner, a moderator or a plain member.

### Security
- **Loopback-only Host**: in dev mode every request whose `Host` is not `localhost`,
  `*.localhost`, `127.0.0.1`, `::1` or the host of `--public-url` is refused with
  `421 forbidden`. This defeats DNS rebinding: a malicious page cannot reach the dev runtime
  through a name that resolves to `127.0.0.1`. The default listen address is
  `127.0.0.1:7420`.
- **Dev login restrictions**: `/auth/dev-login` is `404` outside dev mode. In dev mode it is
  allowed only with the simulator (the user must exist in the simulated world) or with the
  runtime token (`Authorization: Bearer`); against real Discord without the token it
  answers `403`, because impersonating real users is not acceptable.
- The general rules still apply: CSRF header `X-Cicord-Request` and Origin check on unsafe
  methods, simulator endpoints included (they need no authentication otherwise).
- `cicord dev` prints the runtime token (generated per run unless `CICORD_RUNTIME_TOKEN` is
  set) and relaxes `http.fetch` address checks so plugins can call local services
  ([network egress](../../../plugins/writing/#network-egress)); `cicord serve` never does either.

## Consequences
- A new contributor can run `cicord init` then `cicord dev --sim` and exercise a bot end to end
  with no Discord account; the same code paths run in production.
- The simulator is only as faithful as ADR-0012's implementation: behaviour it does not
  model (some error cases, rate limits, privileged intent approval) must still be checked
  against a real test server before going live.
- Seeding happens once: a server or bot added to the YAML while `cicord dev --sim` runs is not
  in the world until the process is restarted.
- The dev runtime must not be exposed beyond loopback; the Host check makes an accidental
  `--listen 0.0.0.0` answer only to local names (or the configured public URL).
