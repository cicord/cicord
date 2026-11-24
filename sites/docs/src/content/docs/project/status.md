---
title: Status and roadmap
description: What works today, what is next, and what is waiting on the project owner.
---

Cicord is **v1alpha1, pre-release**: `0.x` releases (binaries, the container image
`ghcr.io/cicord/cicord` and the `@cicord/*` npm packages, see
[Installation](../../getting-started/installation/) and the
[changelog](https://github.com/cicord/cicord/blob/main/CHANGELOG.md)) may still change formats
and APIs. This page is the project's living tracker: it is updated as work lands. No dates are
promised.

## What works today

- **Configuration**: loader and validator (JSON Schemas plus semantic checks), local and git
  sources (GitHub, any git remote, an embedded repository) with revisions and hot swap.
- **Server reconciler**: plan, apply, drift and self-heal, adoption by name, deletion
  protection, and parity with most Discord server settings: Community, AutoMod, native
  onboarding and welcome screen, role icons and gradients, forum settings, emojis, stickers and
  soundboard sounds, with boost and Community gating explained in the plan. See
  [Discord parity](../../reference/discord-parity/).
- **Bots and plugins**: one runtime for N bots across M servers; a TypeScript plugin host on
  QuickJS-ng compiled to WebAssembly (one VM per instance, 64 MiB of memory, CPU and
  wall-clock budgets with a hard stop, host-side quotas, crashed instances restarted with
  backoff) with capabilities, KV storage, slash commands (subcommands and groups,
  autocomplete, string length limits), components, modals, schedules, tasks, server join and
  leave events, fetching a message by ID, cross-plugin events, plugin-owned channels and typed
  portal routes. See [Testing and limits](../../plugins/testing/).
- **Nine official plugins**, published in the signed official registry
  [cicord/cicord-plugins](https://github.com/cicord/cicord-plugins) and bundled in the binary,
  all exercised end to end on the simulator.
- **Git write-back**: commits and GitHub pull requests, with per-path policies.
- **GitHub access** with a token or a **GitHub App** (short-lived installation tokens scoped to
  the configuration repository), and a `cicord/sync` commit status on every evaluated commit.
- **State store**: SQLite in the data directory or PostgreSQL, held to one conformance suite,
  with a [backup and restore guide](../../self-hosting/backups-and-secrets/) for both.
- **Admin console**: status, plans, history, audit log, generated plugin settings, the Store,
  diagnostics, the simulator panel; light and dark themes.
- **Setup wizard**: from nothing to a synced server, with live checks, an encrypted secret
  store and an import that produces an empty first plan.
- **Plugin Store**: the official plugins (bundled, and from the signed official registry), git
  registries (including private ones and monorepos with one directory and tag series per
  plugin), computed trust, permission review, install, update and uninstall,
  `cicord plugin publish`, and `cicord registry keygen|sign|verify` for signed indexes.
- **Community portal**: the SDK (`@cicord/client`, `@cicord/react`, `@cicord/ui`), the reference
  portal, no-code customization, custom portals served from the repository, embedding in token
  mode.
- **Self-hosting**: a separate, private admin URL (the admin console and admin API only answer
  there), `cicord healthcheck` for containers, and a
  [hardened single-server deployment](../../self-hosting/production-vps/) (Docker, Caddy,
  Tailscale, automatic updates, daily backups) that runs the official community server.
- **CLI**: `init`, `validate`, `plan` (local and remote, with pull request comments), `apply`,
  `status`, `sync`, `healthcheck`, `import`, `adopt`, `mv`, `dev`, `serve`, `secret`,
  `registry`, `plugin`, `portal`.
- **Validated on a real Discord server**: reconciliation, slash commands, onboarding roles,
  drift and sync, OAuth login, the setup wizard end to end, and the parity checklist
  (Community, AutoMod, native onboarding, icon upload, widget, welcome screen, emoji upload,
  boost gating).

## Next

- Verify every SDK feature on real Discord (the simulator already covers them all), including
  member joins (this needs a second account).
- Real Discord, still untested: stickers (multipart uploads), soundboard sounds, role gradient
  colors and role icons (they need boosts).
- Route contracts for the tickets, moderation and announcements plugins.
- SDK gaps found while writing the official plugins: more events (channel, role, ban, with
  their actor), message content before edits and deletions, time zones (`Intl`) in the plugin
  host, removing other users' reactions, user/role/mentionable/channel select menus (Discord
  types 5 to 8, with default values, channel types and resolved values). (Fetching a message by ID, subcommands,
  autocomplete, server join/leave events, role positions, member permissions and
  allowedMentions id lists are done.)
- Discord parity: invites and webhooks, a holographic role style preset, live updates of the
  plan page, drift detection for widget, onboarding and welcome screen.
- Setup wizard: a webhook step, the GitHub App instead of a personal access token (created from
  a manifest), several servers and bots, a Store step with registries on top of the catalog.
- `cicord plugin publish` building from the tagged tree; `cicord mv` against a remote runtime
  (a mapping move endpoint; the reconciler already detects renames).
- Fixes: the admin console login loses the deep link (it returns to `/admin/` instead of the
  page that asked for login); `cicord import --force` leaves stale files in
  `servers/assets/<key>/`; the simulator does not reseed when a hot reload adds servers or
  bots.
- Discord contract: a fatal disconnect event.
- `@cicord/sdk/testing`: enforce route access levels and `discord.messages.manage` in the
  fakes.
- Internal: reuse the runtime's manifest checks and write-back helpers in the CLI instead of
  duplicating them.

## Production hardening

- Observability: a Prometheus `/metrics` endpoint, plugin error history, alerting hooks.
- A security review: plugin sandbox escapes, YAML fuzzing, link token reuse.

## Waiting on the owner

These steps need decisions or accounts from the project owner:

- **The first release, v0.1.0**: the [one-time setup](../releasing/#one-time-setup-owner)
  (a tag ruleset for `v*`, the public container package, the npm organization `cicord` with a
  first publication of each package, trusted publishing and "disallow tokens" for
  `@cicord/sdk`, `@cicord/client`, `@cicord/react` and `@cicord/ui`, the `npm` environment),
  then the tag.

## Releases

- Done: the release pipeline. A tag `v<version>` builds the binaries for Linux, macOS and
  Windows (GoReleaser, reproducible, with SBOMs and build provenance attestations), the
  multi-arch image `ghcr.io/cicord/cicord` and the npm packages with provenance (trusted
  publishing). See [Releasing](../releasing/)
  and [Installation](../../getting-started/installation/).
- Later: `go install` with the embedded web UIs; the CI workflow written by `cicord init`
  downloading the release binary instead of `go install`.

## Further out

- Onboarding without git: "Add to Discord", an embedded repository, an automatic import and
  the admin console only.
- A hosted, multi-tenant offering with stronger plugin isolation, quotas and managed git.
- The portal as a Discord Activity (proposed, to be verified).
- Translations of the admin console.
