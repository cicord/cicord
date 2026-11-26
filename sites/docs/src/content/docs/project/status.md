---
title: Status and roadmap
description: What works in Cicord 0.2.0, what is new in recent releases, what is next, and what is further out.
---

Cicord **0.2.0** is a pre-release of the API `cicord.dev/v1alpha1`: `0.x` releases
(binaries, the container image `ghcr.io/cicord/cicord` and the `@cicord/*` npm packages, see
[Installation](../../getting-started/installation/)) may still change formats and APIs. The
[changelog](https://github.com/cicord/cicord/blob/main/CHANGELOG.md) lists what each release
changed. This page is updated as work lands; no dates are promised.

## What works today

- **Configuration**: loader and validator (JSON Schemas plus semantic checks), local and git
  sources (GitHub, any git remote, an embedded repository) with revisions and hot swap.
- **Server reconciler**: plan, apply, drift and self-heal, adoption by name, deletion
  protection, key renames (`cicord mv`), role holders declared in git (`roles.<key>.members`),
  a permission preflight for bots without Administrator, and parity with most Discord server
  settings: Community, AutoMod, native onboarding and welcome screen, role icons and gradients,
  forum settings, emojis, stickers and soundboard sounds. Changes that need a higher boost
  level wait in the plan, with the reason, without counting as drift. See
  [Discord parity](../../reference/discord-parity/).
- **Bots and plugins**: one runtime for N bots across M servers; a TypeScript plugin host on
  QuickJS-ng compiled to WebAssembly (one VM per instance, 64 MiB of memory, CPU and
  wall-clock budgets with a hard stop, host-side quotas, crashed instances restarted with
  backoff) with capabilities, KV storage, slash commands (subcommands and groups,
  autocomplete), components, modals, schedules, tasks, server join and leave events,
  messages with their embeds, the members of a role, versioned interop (events, queries and
  actions between plugins), durable tasks with dead letters, temporary incident overlays,
  member data export and erasure, bound secrets, plugin-owned channels and typed portal
  routes. See [Testing and limits](../../plugins/testing/).
- **Official plugins**: 29 in [cicord/cicord-plugins](https://github.com/cicord/cicord-plugins)
  for 0.2 (engine `>=0.2.0`); [Official plugins](../../plugins/official/) says which the
  signed registry publishes and which the binary bundles. `economy`, `giveaways`,
  `ai-assistant` and image rendering are experimental and opt-in.
- **Git write-back**: commits and GitHub pull requests, with per-path policies.
- **GitHub access** with a token or a **GitHub App** (short-lived installation tokens scoped to
  the configuration repository), and a `cicord/sync` commit status on every evaluated commit.
- **State store**: SQLite in the data directory or PostgreSQL, held to one conformance suite,
  with a [backup and restore guide](../../self-hosting/backups-and-secrets/) for both.
- **Admin console**: status, plans (with incident overlays), history, audit log, server
  analytics (bot usage included), generated plugin settings and secrets, the Store,
  diagnostics (failed tasks, interop), member data, the simulator panel; live refresh through
  `GET /api/v1/events`; light and dark themes.
- **Server analytics**: hourly counts per server (members, joins, messages, voice, reactions,
  invites, moderation, commands, opt-in presence), no content and no member IDs; optional
  server-wide stats on the community portal. See [Server analytics](../../self-hosting/analytics/).
- **Setup wizard**: from nothing to a synced server, with live checks, an encrypted secret
  store, a token or GitHub App for the repository, and an import that produces an empty
  first plan.
- **Plugin Store**: the official plugins (bundled, and from the signed official registry), git
  registries (including private ones and monorepos with one directory and tag series per
  plugin), computed trust, permission review, install, update and uninstall,
  `cicord plugin publish`, and `cicord registry keygen|sign|verify` for signed indexes.
- **Community portal**: the SDK (`@cicord/client`, `@cicord/react`, `@cicord/ui`), the reference
  portal, no-code customization, custom portals served from the repository (with
  the npm packages at the version of cicord, `cicord portal upgrade` to follow it), embedding
  in token mode.
- **Self-hosting**: a separate, private admin URL (the admin console and admin API only answer
  there), `cicord healthcheck` for containers, Prometheus metrics on a dedicated listener
  (`--metrics-listen`, see [Monitoring](../../self-hosting/monitoring/)), and a
  [hardened single-server deployment](../../self-hosting/production-vps/) (Docker, Caddy,
  Tailscale, automatic updates checked against the signed build provenance, daily backups)
  that runs the official community server.
- **CLI**: `init`, `validate`, `plan` (local and remote, with pull request comments), `apply`,
  `status`, `sync`, `healthcheck`, `import`, `inspect`, `adopt`, `mv`, `pause`, `resume`,
  `dev`, `serve`, `secret`, `alert`, `registry`, `plugin`, `portal`, `version`.
- **Releases**: a tag `v<version>` builds reproducible binaries for Linux, macOS and Windows
  (with SBOMs and build provenance attestations), the multi-arch image and the npm packages
  with provenance; release candidates (`x.y.z-rc.N`) go to the npm dist-tag `next`. See
  [Releasing](../releasing/).
- **Validated on a real Discord server**: reconciliation, slash commands, onboarding roles,
  drift and sync, OAuth login, the setup wizard end to end, and the parity checklist
  (Community, AutoMod, native onboarding, icon upload, widget, welcome screen, emoji upload,
  boost gating).

## New in 0.2.0

The plugin platform release ([changelog](https://github.com/cicord/cicord/blob/main/CHANGELOG.md),
[upgrading from 0.1.11](../../self-hosting/upgrading/#from-0111-to-020)):

- **Plugin SDK**: versioned interop between plugins, durable tasks retried at least once with
  dead letters to retry or discard in Diagnostics, temporary
  [incident overlays](../../reference/http-api/servers/#incident-overlays) (the only way a plugin
  changes managed resources), [member data](../../reference/http-api/servers/#member-data)
  export and erasure, bound credentials, scoped case links, storage quotas, and the moderation
  APIs (bans, purges, timeouts).
- **Official plugins**: 29 for 0.2, from guard, verification and anti-nuke to tickets with
  modmail. They need engine `>=0.2.0`. Plugin data of 0.1.x is not migrated.
- **Admin console**: overlays on the Plan page, plugin secrets, member data and purges in
  Server settings, paged dead letters and the interop graph in Diagnostics, and a warning
  when Discord's audit log shows bot actions Cicord did not make.
- **Portal**: the ticket desk, the typed-code verification challenge, appeals through case
  links and the Tools pages of the community plugins.
- **Experimental, opt-in**: `economy`, `giveaways`, `ai-assistant` and image rendering. They
  stay experimental until usage is confirmed with three target communities.

## New in 0.1.11

- Analytics keep the names of deleted channels, and group the channels a plugin creates
  (voice-rooms rooms…) into one row per plugin instance; the portal never names them.

## New in 0.1.10

- Official plugin **voice-rooms**: voice channels on demand ("join to create"), with owner
  commands (`/voice rename`, `limit`, `lock`, `permit`, `kick`, `transfer`, `claim`).
- Plugin SDK voice APIs: the `voiceStateUpdate` event, `voiceMembers()`, `member.moveTo()` /
  `disconnect()` (`discord.voice.move`) and `editChannel()` for plugin-owned channels.

## New in 0.1.9

- Custom portals use the `@cicord/*` packages from npm, pinned to the cicord version, instead
  of vendored tarballs; `cicord portal upgrade` bumps them (`--to-npm` converts a vendored
  portal, `--vendor` keeps tarballs for offline builds).

## New in 0.1.8

- The Store and Plugins pages offer the same actions: **Update to X** when an update exists,
  otherwise **Open**; installing another instance is a secondary action.
- A Store install, update or uninstall, and `cicord plugin add` / `update`, regenerate a custom
  portal's route types in the same change, so the pull request passes the portal CI.
- Install with `curl -fsSL https://cicord.dev/install.sh | sh`, Homebrew or npm (`@cicord/cli`).

## New in 0.1.7

Highlights (the [changelog](https://github.com/cicord/cicord/blob/main/CHANGELOG.md) has every
change):

- **Emergency pause**: `cicord pause` / `cicord resume`, the admin console and
  `POST /api/v1/servers/{server}/pause|resume` stop every apply to a server, for moderators
  and admins. See [Emergency: pause enforcement](../../concepts/reconciliation/#emergency-pause-enforcement).
- **Inspect first**: `cicord inspect` and the wizard's **Inspect first** evaluate a live server
  with a View Channels bot, read-only. See [Evaluate an existing server](../../getting-started/evaluate-a-server/).
- **Built-in alerts** to a Discord or JSON webhook, `cicord alert test|send`. See
  [Alerts](../../self-hosting/monitoring/#alerts).
- **Stricter health gates**: `cicord status --wait`, `cicord healthcheck --service` and the
  `cicord/sync` commit status fail on a degraded plugin, an offline bot or a paused server.
  `selfHeal: false` now only reports drift: the periodic resync no longer applies.
- **Operations**: VPS updates kept only when the service check passes (rollback otherwise),
  backups that fail loudly and copy off-host, `07-restore.sh` and a timed recovery drill, and
  [Handing over an instance](../../self-hosting/handoff/).
- **Install script** `https://cicord.dev/install.sh` (live now); Homebrew
  (`cicord/tap/cicord`) from this release on, npm (`@cicord/cli`) from a later one.
- JSON schemas published at `https://cicord.dev/schemas/v1alpha1/`.

## Waiting on the project owner

- A live migration drill on a real Discord test server with a second person: an ordinary
  member joins, staff channels stay private, a ticket is opened and answered by staff, and a
  reviewed configuration change is applied then reverted with the channel keeping its ID.
- The commercial terms for handing an instance over to a customer (outside these docs; the
  technical side is in [Handing over an instance](../../self-hosting/handoff/)).
- Updating the `cicord-portal.yml` workflow of
  [cicord/cicord-community](https://github.com/cicord/cicord-community) to the generated one
  (verified release binary).

## Next

- Verify every SDK feature on real Discord (the simulator already covers them all), including
  member joins (this needs a second account).
- Real Discord, still untested: stickers (multipart uploads), soundboard sounds, role gradient
  colors and role icons (they need boosts).
- A typed route contract (`spec.routes.types`) for the moderation plugin.
- SDK gaps found while writing the official plugins: message content before edits and
  deletions, time zones (`Intl`) in the plugin host.
- Discord parity: invites and webhooks, a holographic role style preset, drift detection for
  widget, onboarding and welcome screen.
- Setup wizard: a webhook step, creating the GitHub App from a manifest, several servers and
  bots, a Store step with registries on top of the catalog.
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
- Plugins depending on the npm `@cicord/sdk` by default instead of the vendored tarball, like
  portals (`cicord plugin new --from-npm` today, see
  [Releasing](../releasing/#vendored-or-npm-packages)).
- `go install` with the embedded web UIs; the CI workflow written by `cicord init`
  downloading the verified release binary (like `cicord-portal.yml`) instead of `go install`
  (its actions are already pinned by SHA and cicord by version).

## Production hardening

- Observability: plugin error history.
- A security review: plugin sandbox escapes, YAML fuzzing, link token reuse.

## Further out

- Onboarding without git: "Add to Discord", an embedded repository, an automatic import and
  the admin console only.
- A hosted, multi-tenant offering with stronger plugin isolation, quotas and managed git.
- The portal as a Discord Activity (proposed, to be verified).
- Translations of the admin console.
