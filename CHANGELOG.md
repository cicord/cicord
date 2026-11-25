# Changelog

All notable changes to Cicord. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and versions follow [Semantic Versioning](https://semver.org/) (`0.x` while the API is
`v1alpha1`: minor versions may break formats and APIs). Release process: [Releasing](https://cicord.dev/docs/project/releasing/).

## [Unreleased]

### Changed

- Analytics: the channels a plugin created (the voice-rooms plugin's temporary rooms,
  tickets…) are grouped into one row per plugin instance in every per-channel ranking and
  chart of the dashboard ("Channels created by voice-rooms", with how many channels), instead
  of one row per ephemeral channel; expand the row to see the channels. The analytics API
  answers the group as the dimension `plugin:<bot>/<instance>`, with `count` and `children`
  in the summary's rankings (`dim=plugin:<bot>/<instance>` in a series).
- Analytics, community portal: the channels a plugin created, often named after a member,
  are never named in `topChannels`: those `@everyone` can see count in one entry per plugin
  instance (`group: true`), named after the instance. Private and deleted channels are still
  left out.

### Fixed

- Analytics: deleted channels and roles keep their names in the dashboard
  (`NPjonath's channel (deleted)` instead of `#1575242066328551536 (deleted)`), and scheduled
  events are shown by name instead of their ID. The collector remembers the last name it saw
  (on activity, once a day, on channel and role create, update and delete events, and from
  the scheduled event samples, with no extra Discord API call) in the new state table
  `analytics_labels` (migration 3, SQLite and PostgreSQL), pruned with the analytics
  retention and when no data is left for the channel.

## [0.1.10] - 2026-11-25

### Added

- Official plugin **voice-rooms** (bundled, and in the official registry): voice channels on
  demand ("join to create"). Joining a hub channel such as "➕ Create a channel" gives the
  member a voice channel of their own, moves them into it and deletes it once only bots are
  left in it. Several hubs, each with its name template (`{member}`, `{username}`, `{n}`),
  category (default: the hub's), user limit, bitrate, allowed roles and owner permissions;
  an empty-room grace delay, a creation cooldown, and rooms adopted or deleted when the plugin
  starts. Owners (and the staff) manage their room with `/voice rename`, `limit`, `lock`,
  `unlock`, `permit`, `kick`, `transfer` and `claim`. The showcase example has a regular hub
  and a guild hub.
- Plugin SDK, voice:
  - `voiceStateUpdate` event (intent `ServerVoiceStates`): `user`, `member`, `channel` (now),
    `previous` and the mute, deafen, stream and camera flags. Discord does not send the
    previous channel: the runtime keeps the voice states of its servers per bot session,
    rebuilt from each `GUILD_CREATE` and kept current by the voice and member events.
  - `ctx.server.voiceMembers(channel)` (`discord.members.read`, intent `ServerVoiceStates`):
    the members connected to a voice channel. It throws until the gateway has sent the
    server's voice states, never answering "nobody" when it does not know.
  - `member.moveTo(channel)` and `member.disconnect()`, with the new capability
    `discord.voice.move` (View Channel, Connect, Move Members).
  - `ctx.server.editChannel(channel, options)` (`discord.channels.manage`): rename, topic,
    user limit, bitrate and overwrites of a plugin-owned channel. Overwrites are patched
    (`allow` / `deny` / `clear` per target, `null` removes one), so what the channel got from
    its category is kept.
  - `createChannel()` takes `userLimit` and `bitrate` for voice channels; `channel.categoryId`.
  - `@cicord/sdk/testing`: `setVoice()`, `voiceChannel()`, `overwrites()`, `voiceMoves`,
    `editedChannels`, and fakes of the calls above.
- Simulator: `UpdateMember` moves and disconnects members (Move Members, Connect in the
  destination, its user limit ignored); joining a voice channel needs Connect and respects
  the user limit; deleting a voice channel disconnects its members; voice states carry the
  member.

## [0.1.9] - 2026-11-25

### Changed

- Custom portals use the npm packages by default. `cicord portal init` writes
  `"@cicord/client": "<version of cicord>"` (and the same for `@cicord/react` and
  `@cicord/ui`): the exact version, with no range, no `vendor/` and no `@cicord/*` overrides.
  `pnpm install` resolves them from the registry, where every release publishes them with
  provenance. `--vendor` keeps the former layout for offline or air-gapped builds
  (`vendor/`, `file:` dependencies, pnpm overrides). A development build, whose version is not
  on npm, always vendors and says so. `--from-npm` is still accepted and does nothing.
- `cicord portal upgrade [dir]` sets a portal's `@cicord/*` packages to the version of the
  running cicord, in the mode the portal uses. On npm it rewrites the versions in
  `package.json`; vendored, it swaps the tarballs, as `portal vendor` did. It then prints the
  next steps: `pnpm install`, `cicord portal build`, commit, and bumping `CICORD_TAG` in
  `cicord-portal.yml` when it lags behind. `--check` fails when the versions differ, in either
  mode (`^` ranges included). `--to-npm` moves a vendored portal to npm in one command: it
  rewrites the three dependencies, deletes their overrides from `pnpm-workspace.yaml` and
  `package.json` and deletes `vendor/`, and the rest of both files is kept as is. The
  `cicord-portal.yml` written by `portal init` runs `cicord portal upgrade --check <dir>`.
- `create-cicord-portal` writes the exact version of the `@cicord/*` packages, not a `^` range.

### Deprecated

- `cicord portal vendor`: use `cicord portal upgrade`. It is kept as an alias with the same
  arguments and flags, hidden from `--help`, and prints a deprecation notice on stderr. The
  `cicord portal vendor --check portal` step of existing `cicord-portal.yml` workflows keeps
  passing, vendored or on npm.

## [0.1.8] - 2026-11-25

### Changed

- Store and Plugins pages offer the same actions with the same words. An update reads
  **Update to X** everywhere (Store table, Plugins table, the plugin's Store page, the
  instance's page) and leads to the same update review. On the Store page of an installed
  plugin, the main action is **Update to X** when there is an update, else **Open** (its
  instance, or each of them). **Install another instance** (the plugin a second time, with its
  own name, settings and data) is now a secondary action, explained, and disabled while an
  update is pending. An instance links to its Store page (**View in Store**), and the Store
  page's Installed list links to the instances.
- `GET /api/v1/events` has a `store` event: what the Store lists changed without a new
  revision (a registry refresh found new releases). The admin console then refetches the Store
  listings, plugin pages, update reviews and update badges; a new revision (`sync` without
  server) also refreshes the Store. `@cicord/client`: `AdminEvent` `{type: "store"}`.
- On a repository whose custom portal has typed routes (`portal/src/cicord-routes.d.ts`), a
  Store install, update or uninstall also changes that file, which counts for the write-back
  mode: with the community policies (`bots/**` and `plugins/**` commit, default
  `pullRequest`), installing a bundled plugin now opens a pull request.
- `cicord plugin add` and `cicord plugin update` regenerate the custom portal's route types
  too (`Regenerated portal/src/cicord-routes.d.ts`), with a warning when they cannot.
- A pull request opened by write-back carries the commit message body (what was regenerated,
  warnings). Install, update and uninstall results have `warnings`.

### Fixed

- The Store page of an official plugin locked from the official registry (`cicord/<name>` in
  `cicord.lock`, e.g. `cicord/tickets`) showed no update while the Store table showed one: it
  was described from the bundled copy when the registry index did not list a newer version.
  It now comes from the registry, like its update review and its installs. Store pages cached
  in the admin console are refetched on Refresh, on `store` events and on a new revision.
- A Store action (install, update, uninstall) no longer leaves the custom portal's route types
  stale: `<portal>/src/cicord-routes.d.ts` is regenerated with the code of `cicord portal
  typegen` in the same commit or pull request, so admin pull requests pass the portal's CI
  (`cicord portal typegen --check`). When it cannot be generated, the change is still written
  and the admin console, the commit message and the pull request say so.

## [0.1.7] - 2026-11-25

### Added

- Emergency pause: **Pause enforcement** in the admin console, `cicord pause <server>
  --reason …` or `POST /api/v1/servers/{server}/pause` stops every apply to a server, whatever
  the trigger (a new revision, Sync now, the periodic resync, self-heal), until it is resumed.
  Changes made in Discord stay, drift is still computed and shown, and plugins keep running.
  The pause is runtime state: it survives restarts and new revisions, and a banner on every
  page of the server says who paused, when and why. **Resume…** (`cicord resume`,
  `POST …/resume`) shows the pending plan first and applies nothing by itself; `cicord resume
  --sync` (or `POST …/sync` with `{"resume": true}`) resumes and syncs. Moderators and admins of
  the server can pause and resume. Pauses and resumes are in the audit log, the sync history
  and the alerts; a sync of a paused server answers `409 paused`. `@cicord/client`:
  `client.admin.pause`, `resume` and `pauseState`.
- `cicord inspect <server-id> [dir]` and the setup wizard's **Inspect first**: evaluate a live
  server with a bot invited with View Channels only (`permissions=1024`). A read-only report
  lists what the bot sees, the hidden channels it knows of (Discord hides private channels
  from bots, so they are counted, not named), what it could not read and why, the
  permissions applying would need with the YAML paths that need each one, and the plan of the
  imported configuration, which must be empty. Every Discord call goes through a read-only
  client: zero writes. See [Evaluate an existing server](https://cicord.dev/docs/getting-started/evaluate-a-server/).
- Built-in alerts: with `CICORD_ALERT_WEBHOOK` (secret store or environment, or
  `--alert-webhook`), `cicord serve` posts to a Discord webhook (embeds) or any JSON webhook
  when a plugin handler fails repeatedly, a plugin instance or a bot is degraded, a server sync
  fails, a revision is rejected or a server is paused, and once when each recovers. Alerts name
  the runtime, server, bot, plugin instance and workflow, link to the admin console, and never
  carry a secret, a plugin's error text or Discord content; a problem fires once, however
  often it flaps. `cicord alert test` runs a drill; `cicord alert send` posts from scripts.
- `cicord healthcheck --service`: `cicord status --wait` from inside the container, with the
  runtime token of the secret store and the admin host.
- Install script: `curl -fsSL https://cicord.dev/install.sh | sh` installs the latest
  release on Linux and macOS (or `CICORD_VERSION`, into `CICORD_INSTALL_DIR`), after checking
  the SHA-256 of the archive and, when the GitHub CLI is logged in, its build provenance.
- Homebrew (from this release on): `brew install cicord/tap/cicord`, a cask (macOS and
  Linux) that the release workflow commits to `cicord/homebrew-tap` for every release.
- npm (from a later release, once its packages are set up; see
  [Releasing](https://cicord.dev/docs/project/releasing/#new-npm-packages)): `npm i -g
  @cicord/cli` or `npx @cicord/cli`, the release binary in one package per platform
  (`@cicord/cli-<os>-<arch>`, Windows included), published with provenance from the verified
  release archives. Until the repository variable `CICORD_NPM_CLI` is `true`, a release
  publishes the four SDK packages only.
- The JSON schemas are published at their `$id` URLs, `https://cicord.dev/schemas/v1alpha1/`
  (`project`, `server`, `bot`, `plugin`), for editors (`# yaml-language-server: $schema=…`).
- VPS: automatic updates are accepted only when the service check passes (before the update,
  so a version is never rolled back for a problem it did not cause, and after it), with a copy
  of the database taken before the update; otherwise they roll back to the previous image
  digest, restoring that copy when the new version migrated the database, and send an alert.
  Backups fail loudly (missing or damaged `cicord.db`, a failed off-host copy) with an alert,
  can be copied off the server (`CICORD_BACKUP_REMOTE`: rclone or rsync), keep the encryption
  key apart and write a `MANIFEST`; `07-restore.sh` restores one onto a fresh machine and
  verifies it. The runtime gets `stop_grace_period: 40s`, the time it needs to stop gracefully.
- Docs: [Evaluate an existing server](https://cicord.dev/docs/getting-started/evaluate-a-server/),
  a timed recovery drill, what a backup covers, and
  [Handing over an instance](https://cicord.dev/docs/self-hosting/handoff/) (a dedicated
  instance per customer, an application and a repository the customer owns, credential
  rotation).
- Official plugins: tickets 0.2.1 runs the support queue from the community portal (members
  open and follow their tickets; staff assign, prioritize, label, reply, close and reopen),
  needs Cicord 0.1.5 and the `MessageContent` and `ServerMembers` intents; welcome 0.1.1 and
  reaction-roles 0.1.1. The bundled copies match the official registry's.

### Changed

- **Behaviour change: `syncPolicy.selfHeal: false` only reports drift.** The periodic resync
  (every 5 minutes) now only plans, and a change made in Discord stays until the next revision
  or **Sync now**; before, the resync applied the configuration and reverted it. A failed
  apply of a new revision is not retried on its own either, only by the next revision or Sync
  now. With `selfHeal: true`, drift is reverted within seconds and at the resync, as before.
- **Behaviour change: `cicord status --wait` and the `cicord/sync` commit status are
  stricter.** `--wait` fails at once, naming the cause, when a plugin instance failed to
  start or keeps crashing (`Degraded`), a bot is offline or degraded, or a server is paused;
  it still times out on a blocked change and prints it. The commit status fails when a server
  is paused, a bot is not connected or a plugin instance failed to start, names the first
  problem and counts the others, and turns `success` once they are gone. Changes that only wait
  for a boost level keep the server `Synced` and pass both.
- `cicord plan` lists the changes that wait for a boost level apart (`^ waits for boost: …`),
  and never says "Discord matches the configuration" next to a blocked operation (`No changes
  can be applied: 1 operation(s) blocked.`). The server status message names the first
  blocked change.
- `cicord plan --remote` always uploads the committed portal build at `spec.portal.path`,
  even under `dist/` or an ignored path, and leaves out macOS `._*` and `.DS_Store` files.
- `cicord import` reads AutoMod, the widget and the welcome screen separately: what the bot
  cannot read (without Manage Server) is left out with a warning, and the rest is imported.
- The `cicord-portal.yml` workflow written by `cicord portal init` installs the release binary
  of its cicord version after verifying its checksum and build provenance, instead of
  `go install`. The `cicord.yml` workflow of `cicord init` pins its actions by commit SHA and
  cicord by the version that generated it.
- `spec.portal.theme.mode` defaults to `dark` (it was `system`).
- The examples are in English; `examples/basic`'s server is now `my-community`.

### Fixed

- A bot's `profile.banner` was never applied to Discord.
- Saving plugin settings in the admin console wrote numeric settings equal to their default
  into the bot's YAML (`overrides` or the base config).
- The error for an announcement channel on a server without Community now points to
  `community.enabled: true`.
- Diagnostics reported changes that wait for a boost level as a problem; they are OK.
- The admin console's Plan, Resources and Drift pages now refresh live when drift is seen in a
  gateway event, not only after syncs.
- The admin console shows forum and media channels with their own icons.
- `@cicord/react`: invalidating a query that was observed but not fetched yet (a disabled
  query) removed it from the cache with its subscribers: components kept the stale data and
  did not fetch again once enabled.

## [0.1.6] - 2026-11-25

### Added

- `cicord portal vendor [dir]` updates the vendored `@cicord/*` packages of a custom portal
  to the running cicord (`--check` for CI; the generated `cicord-portal.yml` runs it).

### Fixed

- Right after a new revision was applied, the previous revision's self-heal could revert one
  of its changes (the gateway events of the new changes reached it before it stopped), and
  the new revision healed it forward again. The previous reconcilers now stop before the
  new revision changes anything.
- A server whose only blocked changes wait for a boost level (a banner, an invite splash, an
  animated icon, role icons or gradient colors below their level, and emojis, stickers or
  sounds beyond the slots of a level below 3) stayed `OutOfSync` forever; they are still
  listed in the plan but are no longer drift, the status message says they wait for a higher
  boost level, and the admin's Plan page shows them as a "Waiting for boosts" warning
  instead of "Blocked".
- The runtime status and the server detail read the whole server from Discord on every
  request (a plan each, about 2 s, up to 10 s when Discord throttled), which the live admin
  console multiplied; they now reuse the last plan's drift until an apply, a relevant event
  or a minute passes. The admin's sync dialog no longer waits for every page to refresh
  before closing.

- The `cicord-portal.yml` workflow written by `cicord portal init` referenced actions by tag,
  which organisations requiring SHA pinning refuse (the workflow does not start); they are
  now pinned by commit SHA.

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

[Unreleased]: https://github.com/cicord/cicord/compare/v0.1.10...HEAD
[0.1.10]: https://github.com/cicord/cicord/compare/v0.1.9...v0.1.10
[0.1.9]: https://github.com/cicord/cicord/compare/v0.1.8...v0.1.9
[0.1.8]: https://github.com/cicord/cicord/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/cicord/cicord/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/cicord/cicord/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/cicord/cicord/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/cicord/cicord/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/cicord/cicord/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/cicord/cicord/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/cicord/cicord/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/cicord/cicord/releases/tag/v0.1.0
