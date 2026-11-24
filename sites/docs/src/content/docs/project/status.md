---
title: Status and roadmap
description: What works today, what is next, and what is waiting on the project owner before a first public release.
---

Cicord is **v1alpha1, pre-release**. There is no public release yet: the project's rule is no
release until the product is complete. This page summarizes the project's living tracker,
[`TODO.md`](https://github.com/cicord/cicord/blob/main/TODO.md). No dates are promised.

## What works today

- **Configuration**: loader and validator (JSON Schemas plus semantic checks), local and git
  sources (GitHub, any git remote, an embedded repository) with revisions and hot swap.
- **Server reconciler**: plan, apply, drift and self-heal, adoption by name, deletion
  protection, and parity with most Discord server settings: Community, AutoMod, native
  onboarding and welcome screen, role icons and gradients, forum settings, emojis, stickers and
  soundboard sounds, with boost and Community gating explained in the plan. See
  [Discord parity](../../reference/discord-parity/).
- **Bots and plugins**: one runtime for N bots across M servers; a sandboxed TypeScript plugin
  host with capabilities, KV storage, slash commands, components, modals, schedules, tasks,
  cross-plugin events, plugin-owned channels and typed portal routes.
- **Nine official plugins** bundled in the binary, all exercised end to end on the simulator.
- **Git write-back**: commits and GitHub pull requests, with per-path policies.
- **Admin console**: status, plans, history, audit log, generated plugin settings, the Store,
  diagnostics, the simulator panel; light and dark themes.
- **Setup wizard**: from nothing to a synced server, with live checks, an encrypted secret
  store and an import that produces an empty first plan.
- **Plugin Store**: bundled official plugins, git registries (including private ones), computed
  trust, permission review, install, update and uninstall, `cicord plugin publish`, and the
  signed official index mechanism.
- **Community portal**: the SDK (`@cicord/client`, `@cicord/react`, `@cicord/ui`), the reference
  portal, no-code customization, custom portals served from the repository, embedding in token
  mode.
- **CLI**: `init`, `validate`, `plan` (local and remote, with pull request comments), `apply`,
  `status`, `sync`, `import`, `adopt`, `mv`, `dev`, `serve`, `secret`, `registry`, `plugin`,
  `portal`.
- **Validated on a real Discord server**: reconciliation, slash commands, onboarding roles,
  drift and sync, OAuth login, the setup wizard end to end, and the parity checklist
  (Community, AutoMod, native onboarding, icon upload, widget, welcome screen, emoji upload,
  boost gating).

## Next

- Verify every SDK feature on real Discord (the simulator already covers them all), including
  member joins.
- Route contracts for the tickets, moderation and announcements plugins.
- SDK gaps found while writing the official plugins: fetching a message by ID, subcommands,
  autocomplete, more events (channel, role, ban), message content before edits and deletions,
  time zones in the plugin host, removing other users' reactions, role hierarchy information.
- Discord parity: invites and webhooks, a holographic role style preset, live updates of the
  plan page, drift detection for widget, onboarding and welcome screen.
- Setup wizard: GitHub App instead of a personal access token, a webhook step, several servers
  and bots.
- `cicord plugin publish` building from the tagged tree; `cicord mv` against a remote runtime.

## Production hardening

- GitHub App authentication and commit statuses on synced commits.
- Observability: a Prometheus `/metrics` endpoint, plugin error history, alerting hooks.
- A Postgres state backend, and a backup and restore guide.
- A security review: plugin sandbox escapes, memory limits, YAML fuzzing, link token reuse.

## Waiting on the owner

These steps need decisions or accounts from the project owner:

- **npm publication** of `@cicord/sdk`, `@cicord/client`, `@cicord/react` and `@cicord/ui`: create the
  npm organization, the publish token and a public repository, then run the prepared release
  workflow ([`RELEASING.md`](https://github.com/cicord/cicord/blob/main/RELEASING.md)).
- **The official plugin registry**: create its repository and production signing key, embed
  them in the binary and schedule the re-signing job.
- **The runtime license** (see [License](../license/)).

## Release, later

- Release binaries (GoReleaser) and a published Docker image.
- `go install` with the embedded web UIs.
- npm packages for the SDKs.

## Further out

- Onboarding without git: "Add to Discord", an embedded repository, an automatic import and
  the admin console only.
- A hosted, multi-tenant offering with stronger plugin isolation, quotas and managed git.
- The portal as a Discord Activity (proposed, to be verified).
- Translations of the admin console.
