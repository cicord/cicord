# Changelog

All notable changes to Cicord. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and versions follow [Semantic Versioning](https://semver.org/) (`0.x` while the API is
`v1alpha1`: minor versions may break formats and APIs). Release process: [RELEASING.md](RELEASING.md).

## [Unreleased]

### Fixed

- `cicord plugin publish` rewrote the repository URL of an existing index entry in its own
  spelling (e.g. with `.git`), adding noise to the index diff; an equivalent URL now keeps
  the index's spelling.

## [0.1.5] - 2026-11-25

### Fixed

- `cicord portal init`: with pnpm 10 and later, the `@cicord/*` overrides written to
  `package.json` were ignored, so `@cicord/react` pulled `@cicord/client` from the npm
  registry next to the vendored tarball. They are now written to `pnpm-workspace.yaml`.
- The reference portal's dev proxy presented every request as same-origin to the runtime,
  defeating its CSRF origin check during `pnpm dev:runtime`; it now only does so for the dev
  page's own origin, and the dev server no longer moves to another port when 5174 is busy.
- The generated `cicord-portal.yml` used pnpm 10 and installed `cicord@latest`; it now uses
  pnpm 12 and the version of `cicord` that generated it.

### Added

- Plugin SDK: `message.embeds` on received messages (events, `channel.messages()`), in the
  shape plugins send them, gated by the MessageContent intent like `content`.

- Plugin SDK: `role.members({ limit?, after? })` lists the members holding a role
  (`ctx.server.role("staff").members()`), in user id order, at most 1000 per call (default
  100). It needs `discord.members.read` and the `ServerMembers` intent in plugin.yaml
  (checked at call time; `cicord plugin build` no longer reports that intent as unused for a
  plugin with `discord.members.read`). Discord has no such endpoint: the runtime lists the
  server's members (at most 100,000) and filters them; the listing is shared by the
  server's plugins, reused for 60 s and kept current by member events. The simulator lists
  members too (refused without the ServerMembers intent, like Discord) and the testing
  harness supports it, with a new `intents` option.
- `roles.<key>.members`: Discord user IDs who must hold a role, so staff roles are declared
  in git. Additive: listed members who lack the role get it (plan change kind `memberRole`,
  `give role "Maintainer" to @Alice (…)`), unlisted holders keep it, and removing a user from
  the list never removes the role. A listed user who is not in the server is a plan note
  (new `notes[]` in plans) and gets the role once they join; a role at or above the bot is
  blocked (`hierarchy:`). With the `ServerMembers` intent and `selfHeal`, joins and removed
  roles are fixed right away.
- `spec.analytics.portal` (`off`, `members`, `public`) and
  `GET /api/v1/portal/{server}/analytics`: server-wide activity for the community portal
  (members, joins, active members, messages, voice, reactions, bot commands, the busiest
  public channels and hours; nothing per member, private channels never named).
  `@cicord/client`: `client.portal.analytics`; `@cicord/react`: `usePortalAnalytics`.

## [0.1.4] - 2026-11-25

### Added

- Analytics of how members use the bots, every bot of the server: commands by bot and
  command (`bots.commands`; other bots are counted from their public replies to commands),
  the channels where they are used, distinct members using bots per day
  (`members.active.bots`), how long the members using Cicord's commands have been in the
  server, buttons and menus per bot, and a weekday × hour heatmap of bot commands. The
  admin's Analytics page has a Bot usage section.

- The admin console is live: `GET /api/v1/events` (Server-Sent Events, moderators) sends
  change hints (status, sync, plugin crashes and restarts, new analytics points) and the
  console refetches what it shows. A runtime update is noticed (a "Reload" notice with the
  new version), a restart shows "Reconnecting…", and without the stream the console
  refreshes every 30 s and when the tab comes back. `@cicord/client`:
  `client.admin.subscribe`, and `idleTimeoutMs` for event streams.

## [0.1.3] - 2026-11-25

### Added

- Analytics: bot members are counted apart (`members.bots`, from the member list read once
  a day and kept current by join and leave events); `members.total` and `members.online`
  now count humans only, and bots are left out of the presence aggregates. The summary
  takes a custom range (`?from=&to=`, whole UTC hours, hourly sparklines under two days)
  and the admin's Analytics page a Custom period with dates and times.

### Changed

- Analytics charts use whole-number axes, and the links in the "not collected" notice are
  clickable.

### Fixed

- A plugin handler that failed once its timeout had fired could report its own error
  instead of the timeout.

## [0.1.2] - 2026-11-25

### Added

- Server activity analytics: the runtime keeps hourly aggregates per server (members,
  joins and leaves, active members, messages per channel, voice time, reactions, invites,
  moderation actions, AutoMod, commands, and opt-in presence) in the state database, SQLite
  or PostgreSQL. Only counts are stored: no message content, no member IDs, and distinct
  members are counted with daily tokens that are deleted after the day. Configure it with
  `spec.analytics` (`enabled`, `retention`, default `400d`, `presence`). Moderators and admins
  read it through `GET /api/v1/servers/{server}/analytics/summary`, `…/series` and
  `…/catalog`. See [Server analytics](https://cicord.dev/docs/self-hosting/analytics/).
- Prometheus metrics: `cicord serve --metrics-listen 127.0.0.1:9420` (or
  `CICORD_METRICS_LISTEN`) serves `GET /metrics` on a dedicated listener, off by default and
  without authentication (bind it to a loopback address). They cover the build, the active
  revision, the last sync of each server, the changes applied to Discord, the gateway
  connection of each bot, plugin instances (running, crashes, restarts, handler errors and
  durations), HTTP requests by route, webhook deliveries, and the Go runtime and process. The
  API listener answers `404` on `/metrics`. See
  [Monitoring](https://cicord.dev/docs/self-hosting/monitoring/).

### Fixed

- A plugin running a long loop of native builtins (string searches, for example) could
  freeze the whole process: a garbage collection that started meanwhile waited for the
  plugin, with every goroutine stopped, the CPU-limit kill included. The engine now calls
  Go every 65536 function entries, which lets the collection and the kill run (about 4%
  more CPU on plugin code).
- A pre-release engine (`0.2.0-rc.1`) refused every repository and plugin: `spec.engine`
  ranges are now checked by semver precedence, pre-releases included, so `0.2.0-rc.1`
  satisfies `>=0.1.0` and `>=0.1.0 <0.2.0` but not `>=0.2.0` (use `>=0.2.0-0`). Repositories
  and plugins created by a pre-release binary get a range that admits it.
- `make release` accepts `x.y.z-rc.N` and orders versions by semver precedence
  (`0.2.0-rc.10` after `0.2.0-rc.2`, `0.2.0` after both); a pre-release leaves the
  CHANGELOG's `[Unreleased]` section for the release.
- The automatic VPS updates never moved a server running a pre-release to its release:
  `sort -V` ranked `0.2.0-rc.1` above `0.2.0`.

## [0.1.1] - 2026-11-24

### Added

- `"@bot"` overwrite target: the managing bot's own managed role, so a bot without
  Administrator keeps access to private channels. `cicord import` writes it for the bot
  role's overwrites instead of dropping them.
- Permission preflight: with a bot that is not Administrator, plan and sync check the
  permissions the changes need, and that the bot keeps access to the channels it manages.
  If a check fails, they apply nothing and name the missing permissions with the YAML
  paths that need them.
- The Verify / Diagnostics permission check uses what the server configuration and the
  plugin grants need, instead of a fixed list.
- Automatic runtime updates for the single-server install (`deploy/vps/scripts/06-auto-update.sh`):
  patch releases by default, each one checked against its signed build provenance, with a
  backup before and a rollback if the new version is not healthy.
- The release notes give the container image's digest.
- `make release VERSION=x.y.z` prepares a release commit (versions and changelog).

### Fixed

- Buttons and select menus failed with "The application did not respond": Discord now sends
  a numeric component id in button interactions, which made the whole event undecodable.
- Code scanning findings: uploaded archives are extracted through an `os.Root` (no write
  outside the destination), one strict check for local login redirects, 24-bit color
  parsing, no backtracking regexes in `@cicord/client` and the `@cicord/ui` Markdown, and
  the embed example no longer shows errors to the browser.
- The default portal's page shell is in English (it said "Portail communautaire" until the
  app loaded).
- `deploy/vps/.env.example` was missing from the repository (ignored by `.gitignore`).

### Changed

- The documentation has a single home, [cicord.dev/docs](https://cicord.dev/docs/): the
  Markdown guides, the HTTP API and configuration specs, the architecture document, the
  design decisions and the release process moved there from the repository.
- The single-server install runs `CICORD_IMAGE`, a release pinned by digest, instead of a
  locally tagged image (`CICORD_IMAGE_TAG`).

## [0.1.0] - 2026-11-24

First public release (pre-release, API `cicord.dev/v1alpha1`).

### Runtime

- One static `cicord` binary: command line tool and runtime (`cicord serve`), with the admin
  console, the default community portal and the portal kit embedded.
- Configuration from a git repository (GitHub, any git remote, or an embedded repository)
  or a local directory, validated by JSON Schemas and semantic checks; revisions with hot
  swap.
- Server reconciler: plan, apply, drift detection and self-heal, adoption, deletion
  protection, key renames (`cicord mv`), and parity with most Discord server settings
  (Community, AutoMod, native onboarding, welcome screen, role icons and gradients, forums,
  emojis, stickers, soundboard), with boost and Community gating explained in plans.
- N bots across M servers on one engine; privileged intents checked against the live
  application.
- Git write-back (commits or GitHub pull requests), GitHub App authentication and
  `cicord/sync` commit statuses.
- SQLite or PostgreSQL state, held to one conformance suite.
- Discord simulator (`cicord dev --sim`) for offline development.
- CLI: `init`, `validate`, `plan` (local and remote, pull request comments), `apply`,
  `status`, `sync`, `import` (with the server's files), `adopt`, `mv`, `dev`, `serve`,
  `secret`, `registry`, `plugin`, `portal`, `healthcheck`.

### Plugins and SDK

- TypeScript plugins in a sandbox (QuickJS-ng on WebAssembly) with hard memory and CPU
  limits, capabilities, KV storage, crash restarts with backoff.
- `@cicord/sdk`: slash commands (subcommands, groups, autocomplete, string length limits),
  components, modals, schedules, tasks, cross-plugin events, `channel.message(id)`,
  `serverJoin`/`serverLeave`, plugin-owned channels, typed portal routes; unit testing with
  `@cicord/sdk/testing`.
- Developer loop: `cicord plugin new`, `build`, `typegen`, `test`, hot reload on the
  simulator.
- Plugin Store: bundled official plugins (9), git registries (private ones with tokens),
  computed trust, permission review, install, update and uninstall, signed official index,
  `cicord plugin publish`.

### Admin console

- Status, plans, history, audit log, generated plugin settings, per-plugin pages, the
  Store, diagnostics and the simulator panel; light and dark themes.
- Setup wizard: from an empty directory to a synced server (token, intents, invite,
  repository, import with an empty first plan, OAuth, verification, first plugin).
- Served on its own admin URL when configured (the admin console only on the admin host).

### Community portal

- `@cicord/client`, `@cicord/react`, `@cicord/ui`: runtime API client, React bindings and
  themed components; typed plugin routes (`client.routes`, `useRouteQuery`).
- Reference portal (`create-cicord-portal`, forked with `cicord portal init`), no-code
  customization from YAML, custom portals served from the repository, embedding in
  third-party sites (token mode).

### Security

- Plugin network allow-list hardened against SSRF; registry and plugin downloads only reach
  public addresses (rebinding, redirects and special-purpose ranges refused).
- Signed official plugin index bound to its URL, serial and expiry, with anti-rollback.
- Encrypted secret store; registry credentials scoped to their host and redacted.
- Dev login restricted to the simulator or the runtime token; DNS rebinding checks.
- CI: gitleaks, golangci-lint, govulncheck, semgrep, oxlint, Knip; actions pinned to
  commit SHAs.

### Deploy and release

- Distroless, non-root container image with a `HEALTHCHECK`; single-server deployment
  guide (Docker, Caddy, Tailscale).
- Release pipeline: reproducible binaries for linux/darwin (amd64, arm64) and
  windows/amd64 with SHA-256 checksums, SPDX SBOMs and build provenance attestations; the
  multi-arch image `ghcr.io/cicord/cicord` with SBOM, provenance and an attestation in the
  registry; `@cicord/*` on npm through trusted publishing with provenance.

[Unreleased]: https://github.com/cicord/cicord/compare/v0.1.5...HEAD
[0.1.5]: https://github.com/cicord/cicord/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/cicord/cicord/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/cicord/cicord/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/cicord/cicord/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/cicord/cicord/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/cicord/cicord/releases/tag/v0.1.0
