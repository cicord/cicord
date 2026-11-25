# Changelog

All notable changes to Cicord. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and versions follow [Semantic Versioning](https://semver.org/) (`0.x` while the API is
`v1alpha1`: minor versions may break formats and APIs). Release process: [RELEASING.md](RELEASING.md).

## [Unreleased]

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

[Unreleased]: https://github.com/cicord/cicord/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/cicord/cicord/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/cicord/cicord/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/cicord/cicord/releases/tag/v0.1.0
