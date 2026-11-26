# Changelog

All notable changes to Cicord. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and versions follow [Semantic Versioning](https://semver.org/) (`0.x` while the API is
`v1alpha1`: minor versions may break formats and APIs). Release process: [Releasing](https://cicord.dev/docs/project/releasing/).

## [Unreleased]

Cicord 0.2.0 is a large plugin platform release.

- **Plugins get new SDK APIs.** Plugins can moderate, read the audit log and react to
  structure events. They can store data atomically, with rankings and full-text search, and
  translate their messages and commands. They can talk to each other through versioned
  contracts, apply temporary incident overlays and hold write-only credentials. Every
  plugin that stores member data must declare it, and admins can export or erase a
  member's data.
- **Work is durable.** Tasks and events are leased, retried and kept as dead letters until
  an admin decides.
- **The reference portal gains staff and member pages.** The admin console gains Diagnostics,
  Member data, overlay and plugin secret screens.
- **The official registry grows from 11 plugins to 29.** Every official plugin release in
  the registry now requires Cicord 0.2.0.
- **Image rendering is experimental and opt-in** (`ctx.render.image`). So are the economy,
  giveaways and ai-assistant plugins. The three-community usage gate still applies to them.

### Upgrading from 0.1.11

Read this before you upgrade. **The database upgrade is one-way.** The same steps are in
[Upgrading](https://cicord.dev/docs/self-hosting/upgrading/#from-0111-to-020).

1. **Back up the state first.** Back up the data directory (`cicord.db`, `secret.key`,
   `link.key`) or dump PostgreSQL.
   - On first start, 0.2.0 applies migrations 0004, 0005, 0006, 0010 and 0011
     automatically, on SQLite and on PostgreSQL. 7 to 9 are not used. They add task
     leases, versioned storage and rankings, the full-text index, interop receipts and
     the storage byte counters.
   - Tests upgrade a populated 0.1.11 database on both backends.
   - **There is no rollback.** A 0.1.11 binary refuses a migrated database ("database
     schema version 11 is newer than this binary supports (3)"). To go back, restore the
     backup.
   - The runtime also refuses to start when a recorded migration does not match this
     binary's migration of the same number.
2. **Widen `spec.engine` before you upgrade.** Repositories created by 0.1.x declare
   `spec.engine: ">=0.1.0 <0.2.0"` in `cicord.yaml`, and 0.2.0 refuses that revision.
   - Commit `">=0.1.0 <0.3.0"` first: 0.1.11 accepts it too.
   - Upgrade the runtime.
   - Then narrow it to `">=0.2.0 <0.3.0"`, the range `cicord init` now writes.
3. **VPS auto-update.** The default `CICORD_AUTO_UPDATE=patch` does not install 0.2.0.
   With `minor`, the update takes a backup first and rolls back if the service check fails.
   Widen `spec.engine` (step 2) before 0.2.0 is published, or 0.2.0 fails that check.
4. **Update the pinned CLI.** The CI workflow that `cicord init` generated
   (`.github/workflows/cicord.yml`) installs `cicord@v0.1.11` three times: change it to
   `@v0.2.0`.
5. **Update official plugins after the runtime, never before.**
   - Every official plugin release in the registry requires engine `>=0.2.0`.
   - On 0.2.0, `cicord plugin add`, `cicord plugin update` and the Store pick only releases
     whose `spec.engine` accepts the running engine and that the signed index publishes.
   - A 0.1.x CLI does neither. While you are still on 0.1.x:
     - do not `plugin add` official plugins, and do not install them from the Store: both
       resolve to the newest release;
     - pass `--to` with the instance's current range to `plugin update`.
   - Copies of official plugins in `plugins/<name>/` keep working on 0.2.0.
   - Review each update before you commit it: updated plugins request new grants,
     intents and Discord permissions. For example, audit-log and moderation need
     `discord.auditlog.read`, the `ServerModeration` intent and View Audit Log.
6. **Approve widened credentials.** When a new plugin version widens what a bound credential
   can reach (new hosts, another use, a larger or different provider budget), the update
   stops and prints a review digest.
   - CLI: repeat `cicord plugin update <plugin> --approve-secret-changes <digest>`.
   - Store: send the digest as `confirmSecretChanges`.
7. **Plugin `secretRef` names must start with `CICORD_`.** This applies to the
   `secrets.<NAME>.secretRef` of a plugin installation in `bot.yaml`.
   - Variables the runtime reads itself (`CICORD_SECRET_KEY`, `CICORD_PRIVACY_KEY`,
     `CICORD_PUBLIC_URL`, registry tokens…) are reserved.
8. **Upgrade custom portals.** The built-in portal updates with the binary. For a portal
   created with `cicord portal init`:
   - run `cicord portal upgrade`, then `pnpm install` and `cicord portal build`, and commit;
   - `portal upgrade` only changes the `@cicord/*` packages. A portal forked from the
     reference portal gets none of the new pages. To get them, run `cicord portal init`
     into a scratch directory and merge by hand:
     - Verification (typed-code challenge);
     - TicketDesk (paged tickets);
     - Rewards (paged giveaway entrants);
     - Audit (paced export);
     - the Appeals page and the `src/features/` community pages.
9. **Plugin storage now has a quota.** Each installation may hold 1,000,000 KV keys and
   ranking members, and 256 MiB of keys, values and members. An installation already above
   it keeps its data, but further writes fail until it shrinks.
10. **For plugin authors.** Details are under Changed.
    - Scheduled tasks are now at-least-once.
    - `Message.member` is set on received messages only.
    - `member.addRole()` and `member.removeRole()` refuse roles whose membership is declared
      in YAML.
    - Plugin route responses keep only allowlisted headers.
    - `enabled: false` in an installation's config belongs to the host.
    - Builds can no longer read files outside the plugin package.
    - `@cicord/sdk/testing` enforces the runtime's limits.
11. **0.2.0 source snapshots and plugin data.** Plugin data written by a 0.2.0-rc.1
    source snapshot, or by 0.1.x official plugins, has no compatibility guarantee. Several
    official plugins migrate their own 0.1.x data; their READMEs say what is kept.

### Added

#### Runtime and delivery

- **Durable scheduled tasks and events.** A task is leased while its handler runs and
  acknowledged when the handler succeeds. After a crash the lease expires and the same
  task ID runs again. The handler's third argument carries `id` and `attempt`.
  - **Who pays for a crash:** only the handler that crashed the instance. The work that
    waited or ran beside it is delivered again after the restart, and nothing is claimed
    while the instance is down.
  - **Retries:** five deliveries over about six minutes (1 s, 7 s, 45 s, 5 min, with
    jitter).
  - **Streams:** the next event of a stream is delivered as soon as the previous one is
    acknowledged.
  - **Dropped subscriptions:** an event whose subscription a new revision dropped waits,
    and is parked after 24 hours.
- **Dead letters.** Failed work stays as a dead letter. It records a failure category
  (timeout, crash, budget, built-in error name or interop code), never the plugin's error
  text.
  - Admins list, retry and discard dead letters in **Diagnostics**, or with
    `GET /servers/{server}/dead-letters`. The list pages with `cursor`/`nextCursor` and
    `limit` (1–500), and reports `total` and `truncated`.
  - The other endpoints are `POST …/dead-letters/{id}/retry|discard` and
    `GET …/dead-letters/{id}/event`.
- **Private command replies on durable tasks.** A guild command's `deferTask(name, payload)`
  defers an ephemeral reply and hands it to a task. The host keeps an encrypted reply
  credential for at most 14 minutes.
- **Disabling an installation.** `enabled: false` in an installation's config (or the admin
  **Enabled** switch, also in a server override) disables it.
  - The installation stays listed as Disabled. Its commands, routes, subscriptions and
    schedules stop, and its queued deliveries keep their payloads and deadlines.
  - Re-enabling validates the whole config again.
  - `cicord status --wait` and `healthcheck --service` accept a disabled installation.
- **Direct-message routing.**
  - **Which server:** a DM to a bot that is on several servers goes to the server the
    member picks from a menu. The choice is kept per bot across reconnects.
  - **Which plugin:** `spec.directMessages.instance` in `bot.yaml` names the installation
    that receives the DMs. Plugins handle them with the `directMessage` event, which carries
    the validated `member` of that server.
  - **Queueing:** work is queued per user shard, and member lookups run in parallel with a
    5 s bound.
  - **Rate limit:** each user gets a burst of 10 DMs, then one per second. A rate-limited
    DM gets a localized "try again" reply.
- **Per-server bot appearance.** In `bot.yaml`, a `servers` entry can be
  `{key, profile: {nickname, bio, avatar, banner}}`, applied to the bot member on that
  server. It is restored when the bot is invited again.
- **Status lines and feature diagnostics.** `ctx.status.set(key, message)` adds status lines
  to the instance status. `ctx.features.enabled()` and `ctx.features.report()` report which
  optional features are on, and Diagnostics shows them.
- **Own-action attribution.**
  - **Journaling:** audited Discord calls are journaled before they are sent. A gateway
    audit entry is bound to at most one emitted operation with the same actor, action and
    target within 30 seconds.
  - **Coverage:** overwrite changes sent in a channel update, webhooks, scheduled events,
    soundboard sounds, onboarding, the bot's nickname, and member updates that set roles and
    a nickname are matched with Discord's own entries.
  - **Unmatched entries:** an audited action by a project bot that Cicord did not emit
    raises an alert ("Bot token possibly compromised"). It also adds a line to
    `cicord status` and sets `unconfirmedAudit` in the server status, which the admin server
    page shows. This is advisory and never changes health.
  - **Bursts and failures:** gateway entries wait in a queue that holds a large first sync,
    and a raid by one account costs one actor lookup. An entry that was not checked (the
    queue overflowed, or the state store failed) never raises the alert. Subscribers get it
    as `unknown`, and the runtime logs one warning per burst with the count.
- **`CICORD_COMPILER_CACHE_DIR`:** a private native-code cache for the embedded compiler.

#### Plugin SDK

- **Moderation.**
  - `ctx.server.ban()` (also for users who left), `unban()` and paged `bans()`, with the
    `banAdd` and `banRemove` events.
  - `member.removeTimeout()` and `member.setRoles()`.
  - `channel.bulkDelete(ids)`, and a bounded `channel.purge(filter)` (1–1000 messages). Use
    `channel.previewPurge(filter)` to confirm the exact message IDs before deleting them.
- **Audit log and structure events.**
  - `discord.auditlog.read` gives `ctx.server.auditLog()` and the `auditLogEntry` event,
    with the attribution fields.
  - New events:
    - channels: `channelCreate|Update|Delete`;
    - roles: `roleCreate|Update|Delete`;
    - threads: `threadCreate|Delete`;
    - the server: `serverUpdate`, `serverAvailable`;
    - expressions, webhooks and invites: `expressionsUpdate`, `webhooksUpdate`,
      `inviteCreate|Delete`;
    - AutoMod: `automodAction`.

    A structure event carries the previous state when it is available, plus `managed` and
    `willHeal`.
  - `ctx.server.invites()` (`discord.invites.read`) reads the current invites.
- **Channel audiences.**
  - `member.canView(channel)` and `member.permissionsIn(channel)` decide from the live
    guild permissions.
  - `ctx.server.compareAudience(source, destination)` returns `same`, `narrower`, `wider`,
    `disjoint` or `unknown` before content is copied. `ctx.server.audience()` summarizes an audience
    for display, and `server.verifyReaders()` checks a reader ceiling.
  - `channel.memberOverwrite(userId)` reads a member-specific overwrite, and
    `ctx.server.protection()` reports the guild protection settings.
  - An access check denies the read while the gateway is disconnected. It also denies
    age-restricted channels without verified eligibility, and private threads without
    verified membership.
- **Members and server.**
  - `role.permissions` is the role's bitfield.
  - `ctx.server.cicordBotIds` lists the project's own bots, so plugins can exempt them.
  - New member fields: `premiumSince`, `timedOutUntil` and `flags` (rejoined, completed
    onboarding, bypasses verification). Interactions carry the user's `locale`, and the
    server its `preferredLocale`.
  - `ctx.server.members()` pages server membership (up to 1,000,000 members).
- **Messaging.**
  - Pins, `typing()`, reactions (`reactionUsers`, `removeReaction`, `discord.reactions.manage`)
    and crossposts.
  - Webhook sends under another name (`sendAs`, `discord.webhooks.manage`).
  - DMs the bot can retract or edit later (`retractDM`, `editRetractableDM`), also after a
    reconnect.
  - Binary `files` on messages, DMs and interaction replies (up to ten).
  - `attachment.bytes()`, with downloads up to 8 MiB.
- **Interactions.**
  - Guild context menus (`contextMenus`, USER and MESSAGE), with per-bot, per-server quotas.
  - Components V2 layouts.
  - User, role and channel select menus.
  - Labeled modal fields.
- **Scheduled events and polls.**
  - `scheduledEvents()` and `createScheduledEvent()` (`discord.events.read|manage`), with
    the `scheduledEvent*` events.
  - Native polls: `message.poll`, `endPoll()`, paged `pollVoters()`, and the
    `pollVoteAdd|Remove` events.
  - `ctx.analytics.summary()` (`analytics.read`) gives aggregate server analytics.
- **Storage.**
  - `kv.getEntry()` returns a value with its version, and `kv.getMany()` reads several keys.
  - `kv.atomic()` commits checks and mutations in one transaction (128 operations).
  - `kv.ranking(name)` gives exact rankings, with `rank`, `top`, `around` and `count`. A
    write changes one row, and ranks are counted on read. A board holds at most 250,000
    members.
  - Scoped full-text search with `ctx.index(name)` (`storage.index`), on SQLite FTS5 and
    on PostgreSQL.
  - `crypto.getRandomValues()` and `crypto.randomUUID()`.
- **Translations.**
  - `spec.locales` holds flat JSON catalogues, and `ctx.i18n.t(key, vars)` translates with
    locale fallback.
  - Catalogue keys also translate the Discord command and context-menu UI, option
    descriptions and fixed choices (`commands.<command>.options.<option>.choices.<value>`).
  - Number variables are formatted like JavaScript.
- **Interop.** Plugins integrate through versioned contracts declared in `spec.interop` and
  `contracts/`:
  - **Events:** server- or bot-scoped publications, `meta.id` for deduplication, 30-day
    publication keys, and ordered, durable subscriber queues.
  - **Queries:** `ctx.plugins.query()` (`plugins.query:<plugin>.<name>`) on a read-only
    context.
  - **Actions:** transactional actions with idempotency receipts (`ctx.plugins.call()`,
    `plugins.call:<plugin>.<name>`).
  - **Callers:** each caller is authenticated by the host, and member calls carry a
    delegated principal. An installation restricts its callers with `interop.allowCallers`,
    and a consumer picks a provider with `interop.targets`.
  - **Optional features:** `spec.optional` features have their own grants
    (`ctx.features`, `ctx.plugins.hasFeature`).
  - **Admin:** the interop graph of a server is at `GET /servers/{server}/interop`.
- **Member privacy.**
  - `spec.privacy.records` declares every member-bearing record.
  - The `privacy.export` and `privacy.erase` hooks are idempotent. `ctx.privacy.pseudonym()`
    works only inside them.
  - Admins export or erase a member in **Server settings → Member data**, or with
    `POST /servers/{server}/privacy/export|erase`. The report is per installation.
  - Erasures are recorded in an HMAC journal (`member-erasures/`, `privacy.key` or
    `CICORD_PRIVACY_KEY`) and reapplied after a restore of an older backup.
- **Temporary incident overlays.** `ctx.server.overlays` (`server.overlays`) applies
  time-limited restrictions. Overlays last at most seven days, and a durable task converges
  the affected fields at expiry, also while enforcement is paused.
  - **What an overlay can do:** add denies, take allows back (`neutral`, also on the `"*"`
    target), raise slowmode or the verification level, or suspend roles declared in YAML.
  - **Seeing and lifting overlays:** they are listed on the server Plan page and through
    `GET /servers/{server}/overlays`, and lifted one by one.
  - **Other incident actions:** `ctx.server.incident()` (`server.incident`) pauses invites
    or DMs for up to 24 hours. `ctx.server.requestSync()` (`server.sync`) asks for a full
    sync.
- **Credentials and files.**
  - **Plugin secrets:** a manifest declares `spec.secrets`, each with an allowed use and
    hosts, and optionally an Anthropic daily token budget.
    - `bot.yaml` binds a name to a `secretRef`.
    - The plugin passes `ctx.secrets.ref(name)` to `http.fetch` and never sees the value.
    - Admins set the value write-only with `PUT …/plugins/{bot}/{instance}/secrets/{name}`
      or in the admin console.
  - **Per-instance hosts:** an installation in `bot.yaml` can allow extra hosts with
    `network.allow`.
  - **Repository files:** an installation can mount repository files with `files`. The
    plugin reads them with `ctx.files.read` (`files.read`).
  - **Assets:** `spec.assets` bundles up to 5 MiB of read-only files, read with
    `ctx.assets.read`.
- **Media.** `ctx.media.hash()` (`sha256`, `phash`, `dhash`) and `ctx.media.distance()`
  need `media.analyze`.
  - Image decoding runs in a confined WebAssembly worker, with 96 MiB of memory and a
    3-second deadline.
  - Input is limited to 8 MiB and four megapixels.
- **Image rendering (experimental, opt-in).** `ctx.render.image()` (`render`) draws PNG
  cards in the confined worker. The layer types are text, image, `rect`, `progress` and
  `chart`.
  - **Sizes:** sides up to 2048 px with an area of at most 524,288 pixels. Source images are
    limited to 524,288 decoded pixels in total.
  - **Budget:** 24 MiB of memory and a one-second deadline. The deadline starts once a
    worker slot is free.
  - **Errors:** a larger spec gets a validation error.
  - **Fonts:** Inter, Noto, emoji or a packaged font. Their OFL notices ship in `NOTICE`,
    in the archives, the image and the npm packages.
- **Portal case links.**
  - `ctx.portal.link(path, {scope})` signs a link to one case, bound to the recipient's
    Discord identity and to the issuing installation.
  - Routes with `access: "scoped"` read `request.scope`.
  - `ctx.portal.revoke()` invalidates the links of a case.
  - An expired link can be renewed through OAuth for up to 30 days.
- **Plugin route answers.** Routes can return text answers, with the full 2 MiB allowance.
- **`@cicord/sdk/testing`.** The harness behaves like the host.
  - **Provider calls:** `callProvider` runs a provider query or action in a fresh realm,
    with a read-only context for queries and the contract checked. `fakeProviders` checks a
    consumer's input against its providers' contracts.
  - **New helpers:** `readOnlyContext`, `contractViolation`, `replayActionEffects`,
    `harness.status`, `harness.attachment()`, `server.otherCicordBotIds`.
  - **Fakes:** the harness takes asset, binary `fetch`, `renderImage` and `hashImage`
    fakes.
  - **Channels:** `createChannel({provisionKey})` returns the channel created earlier with
    the same key, and `recoverOwnedChannel({provisionKey})` finds it.
- **Idempotent channel creation.** `createChannel({provisionKey})` and
  `recoverOwnedChannel()` make channel creation idempotent per installation, across
  restarts, voice channels included. The keys are kept as a digest for 30 days.

#### Admin console and HTTP API

- **Diagnostics:** dead letters with "Load more", feature diagnostics and status lines.
- **Server settings → Member data:** export, erase and the per-plugin report.
- **Orphaned data:** **Server settings** lists the data uninstalled installations left
  behind (records, rankings, index, tasks, DM receipts) and deletes it once the installation
  name is typed. The API is `GET /servers/{server}/orphaned-data` and
  `POST /servers/{server}/orphaned-data/purge`.
- **Plan page:** active overlays, each with a lift action
  (`DELETE /servers/{server}/overlays/{bot}/{instance}/{id}`).
- **Plugin instance page:** declared secrets, which can be set write-only, and the
  Disabled state.
- **Server page:** the interop graph, and the `unconfirmedAudit` warning.
- **Admin dev mock:** serves the dead-letter, overlay and plugin secret endpoints.
- **`@cicord/client`:** methods for all of the above. `admin.deadLetters()` returns a page,
  and `CicordApiError.retryAfter` gives the `Retry-After` delay.

#### Portal

- **Reference portal community pages** (`/community`), routed per plugin instance under the
  viewer's session:
  - **Everyone:** an event calendar with an ICS feed, suggestions, poll totals, giveaway
    draw proofs and season standings.
  - **Members:** verification, their own moderation history, birthday preferences, invite
    standings, tag search, and a wallet with its shop.
  - **Staff:**
    - moderation: case filters, appeals, the verification queue, guard rules and incidents;
    - community: announcements with preview and scheduling, shift coverage and suggestion
      decisions;
    - records: the ledger, tag editing, unanswered questions, and audit search and export.
- **Verification:** a typed-code challenge. The plugin renders a code in an image, the
  member types it in, and a rejected answer gets a new challenge.
- **Ticket desk:** typed queues, transcript downloads and "Load more".
- **Giveaway Draw view:** reads every entrant page before it draws, and fails instead of
  mixing pages from a redraw.
- **Audit export:** reads two pages a second, and waits for `Retry-After` on a 429 or 503.
- **Appeals:** an appeal page for signed case links, which works after a ban too.

#### CLI

- **`cicord plugin purge-data <server> <bot>/<instance>`** deletes the data of an
  uninstalled installation.
- **`cicord plugin update --approve-secret-changes <digest>`** approves a widened
  credential review.
- **`cicord plugin test`** reads an optional `test/server.yaml`:
  - `syncPolicy`;
  - `privateChannels`;
  - `botPermissions`, the test bot's permissions after start, to run the plugin in degraded
    mode.

  `--config` with `enabled: false` checks that a disabled installation registers no
  handlers.
- **`cicord plugin build`** checks literal event names and versions against the contracts,
  and warns when a manifest declares no `spec.interop`.

#### Simulator

- **Members can act:** join, move or leave voice, react, vote in native polls, and change
  channel overwrites as the operator.
- **Member views:** `?userId=` on state, history and commands shows what one member sees,
  timeouts included.
- **Audit entries:** written like Discord's, with kick and timeout reasons and voice moves.
- **Bot permissions:** `World.SetBotPermissions` changes a bot's managed role, to test
  degraded mode.

### Changed

- **Tasks are at-least-once.** `ctx.schedule` tasks are leased and retried, as described
  under Added. A handler can run again after a crash. It must recheck state and be
  idempotent.
- **`Message.member`** is set on received server messages only, from the gateway payload.
  Fetched messages and the results of `send`, `reply` and `edit` never carry it, as on
  Discord, and the simulator no longer adds it.
- **`member.addRole()` and `member.removeRole()`** refuse roles whose members are declared in
  YAML. Use a `suspendDeclaredRoles` overlay instead of fighting the reconciler.
- **`enabled` in an installation's config** is the host's switch, read before the plugin's
  schema.
- **Plugin builds are confined.** They see only a read-only view of the plugin package:
  imports, TypeScript configuration, source maps and schema references that leave the
  package fail.
- **`kv.list` pages** stop at 4 MiB of values.
- **`@cicord/sdk/testing` fails where production would:**
  - **Limits:** storage quotas, ranking size, `kv.list` pages, `i18n.t` caps, render limits
    and `status.set` bounds.
  - **`callRoute`:** answers `403` below a route's access level and filters headers like the
    portal. Tests that compared `headers` with `{}` now see the core's headers.
  - **Messages:** only received messages carry `member`.
  - **`ctx.privacy.pseudonym`:** throws outside a privacy hook. A test that calls a privacy
    hook directly with `t.ctx` must use `callProvider` or `readOnlyContext`.
- **Engine-aware plugin resolution.** `cicord plugin add`, `cicord plugin update`, the Store
  and its update badge pick the newest release whose `spec.engine` accepts the running
  Cicord. They never pick one above the version the signed official index publishes.
  `plugin update` lists the version it would actually install.
- **New repositories and plugins** created by `cicord init` and `cicord plugin new` declare
  `spec.engine: ">=0.2.0 <0.3.0"`.
- **Generated CI workflows** pin the release tag of a published binary.
  - An unpublished build pins its own source commit, and refuses to scaffold when it does
    not know it.
  - `cicord portal init` from an unpublished build vendors the embedded `@cicord/*` packages.
- **Container image:** the base and frontend images are pinned by digest.

### Fixed

- **Portal Home:** says "not a member" only for the runtime's 403. A 429, a 5xx or a network
  error shows a retry message.

### Security

#### Portal and plugin routes

- **Plugin route responses keep only allowlisted headers:** `Content-Disposition`, `ETag`,
  `Last-Modified`, `X-*` headers outside the reserved prefixes below, `Cache-Control` on
  public routes, and a few others.
  - **Refused headers:** a plugin can no longer set `Refresh`, `Clear-Site-Data`, `Link`,
    `Upgrade` or hop-by-hop headers. It can no longer set the headers reverse proxies act
    on either: `X-Accel-*`, `X-Sendfile`, `X-Lighttpd-*`, `X-LiteSpeed-*`, `X-Reproxy-*`, and
    forwarding and rewrite headers. Before, a route behind nginx could trigger an internal
    redirect or a sendfile.
  - **Status codes:** a status outside 200–599 answers `500`.
  - **Caching:** every answer has `Vary: Cookie, Authorization`. A public route called by a
    signed-in viewer is `Cache-Control: no-store`.
- **Portal requests:**
  - each signed-in user gets at most 120 a minute (bursts of 60), then
    `429 rate_limited` with `Retry-After`;
  - `/portal/{server}/me` and the member analytics use the 30-second member cache, and
    concurrent reads of one member share one Discord call;
  - plugin routes re-read the member on every request.
- **Guild ownership and channel reads** are checked against the current gateway session and
  the live guild permissions, not cached state.

#### Plugin registry

- **Official registry:**
  - the runtime rereads the signed index when its cached copy is older than an hour or does
    not cover the locked release, so a revoked publisher loses verified status within the
    hour;
  - an older cached index never replaces a newer one;
  - a tag pushed to the registry is not installed until the signed index publishes it.
- **Registry packages:**
  - Git transfers are bounded: 64 MiB of pack, 32,768 objects, a 45 s deadline;
  - a cached package is checked against the integrity in `cicord.lock` before it is used;
  - packages whose paths differ only by case or Unicode normalization are refused.

#### Runtime resources

- **Host memory per installation is bounded,** private VMs included:
  - SDK results waiting for the VM: 96 MiB per installation and 48 MiB per handler.
    `files.read`, `kv.get`, `kv.list`, `kv.getMany` and `index.search` reserve their result
    before they read it;
  - staged message files: 100 MiB per installation and 512 MiB per process. Staging a
    file needs the grant of the send it is for;
  - `ctx.i18n.t`: results and variables of at most 64 KiB.
- **Media downloads** use the instance's own slots, never a process-wide one: one slot per
  processor, between 2 and 4.
- **Image decoding:** the WebP decoder used by the new media features is
  `golang.org/x/image` v0.45.0, which includes the fix for GO-2026-6222.

#### Privacy, interop and overlays

- **Member erasure:**
  - it waits for the member's active handlers;
  - it gates only work that may predate it;
  - it matches member IDs only as 17–20 digit snowflakes;
  - a failure part-way is audited (`privacy.erase` with `failed: true`) and answered `503`
    with the partial report;
  - the member data report lists the data of uninstalled installations as `orphaned`
    (incomplete).
- **Interop:**
  - action receipts are keyed by the caller installation and bind the principal's user and
    resource scope;
  - interop calls and isolated member requests have per-server admission budgets;
  - `requestSync()` accepts one request per installation every 30 seconds.
- **Overlays:**
  - an overlay that would take View Channel, Manage Channels or Manage Roles from the bot
    is refused;
  - an installation holds at most 128 of a server's 256 overlays, with a reserve of 8.
- **SQLite full-text search:** a query matches only its own index, and scores are rounded so
  that they do not depend on other installations' documents.

### Official plugins

The official registry ([cicord-plugins](https://github.com/cicord/cicord-plugins)) adds 18
plugins and updates the existing 11.

- **Engine:** every release requires engine `>=0.2.0`.
- **Locales:** every plugin has English and French messages.
- **Experimental:** the plugins marked experimental below are opt-in, and the
  three-community usage gate applies to them.
- **Bundled copies:** the copies bundled with the binary stay on their 0.1.x versions.

New plugins:

- **ai-assistant** 0.1.0 (**experimental**, off by default): `/ask` answers from mounted
  Markdown and quotes its sources. It calls Anthropic with a host-bound API key, under a
  daily token budget per server, and falls back to a human contact.
- **anti-nuke** 0.2.0: monitor only. It correlates mass deletions, kicks and other admin
  actions with audit-log evidence against per-actor thresholds, alerts staff, and publishes
  `anti-nuke.triggered`. It never sanctions.
- **birthdays** 0.1.0: opt-in birthdays and greetings in the member's local time, with an
  optional 24-hour role through member-roles.
- **channel-automations** 0.1.0: per-channel auto-thread, auto-react, auto-crosspost,
  media-only, posting intervals and timed auto-delete. Each rule has its own grant.
- **community-events** 0.1.0: Discord scheduled events and weekly series in a per-series
  `timeZone`. Reminder DMs, a public calendar and ICS feed (bounded), and attendance events.
- **economy** 0.1.0 (**experimental**, disabled until configured): server points, daily and
  weekly claims, transfers, a shop and an atomic ledger. It provides `balance`, `credit`
  and `debit` to other plugins.
- **feeds** 0.1.0 (off by default): posts RSS, Atom and JSON Feed entries from allowed
  hosts, with filters and an optional role ping.
- **giveaways** 0.1.0 (**experimental**, disabled until configured): weighted draws with a
  seed commitment, conditions, claims and rerolls. Entry routes are paged with
  `entriesCursor`, and the plugin's `test/verify.mjs` follows them.
- **guard** 0.2.0: link, invite, mention, attachment, duplicate, new-account and raid rules.
  It is monitor-only unless `mode: enforce`, caps automatic sanctions, and always exempts
  the project's own bots.
- **invite-tracker** 0.1.0: attributes a join only when the invite-use delta is
  unambiguous, flags fake joins, and has leaderboards and an `inviterOf` query.
- **member-roles** 0.1.0: temporary, sticky, tenure and booster roles. A temporary grant of a
  role with dangerous or unknown permissions is refused unless the role is listed in
  `temp.privilegedRoles`, and interop grants need a non-empty `temp.roles`.
- **reminders** 0.1.0: `/remind` in English and French time phrases, repeats that keep
  their local time, a "Remind me" message menu and `/timezone`.
- **staff-shifts** 0.1.0: `/duty on|off` with an on-duty role, weekly plans and a coverage
  view on the portal.
- **starboard** 0.1.0: copies a message once it reaches a reaction threshold, only to a
  board whose audience is the same or narrower. The copy is removed when the source changes.
- **stats-reports** 0.1.0: weekly and monthly aggregate reports, with optional channel
  counters. Its charts are **experimental** (image rendering).
- **suggestions** 0.1.0: `/suggest` or a forum, with votes, statuses, optional anonymity
  and threads. It keeps the text only with `retainText`.
- **tags** 0.1.0 (off by default): staff-curated answers from Markdown or the portal,
  through `/tag` and a message menu.
- **verification** 0.2.0: a portal challenge bound to the member.
  - **Default challenge:** `typed`, a 6-character code drawn with image rendering.
  - **Fallback:** when rendering is unavailable, the new `fallback` setting decides:
    `review` (the default) sends the member to staff review, and `button` gives them a
    button. It never passes silently.
  - **Also:** Turnstile and hCaptcha, account-age policies and a staff queue.

Updated plugins:

- **announcements** 0.2.0 (from 0.1.0): sticky messages, `/countdown`, optional
  crossposting, and portal compose, preview and scheduling.
- **audit-log** 0.2.0 (from 0.1.0): evidence-based logs of channels, roles, threads,
  expressions, settings, invites, bans, voice and AutoMod, with actors taken from the audit
  log. Staff search and export. It needs new grants and intents (`discord.auditlog.read`,
  `ServerModeration`…). Posts made by 0.1 are not tracked.
- **github-releases** 0.2.0 (from 0.1.0): locales and `enabled`. Its 0.1 state is still
  read.
- **levels** 0.2.0 (from 0.1.0): voice XP, multipliers, boosts, reward modes, seasons, CSV
  import, `/xp` admin commands and bonuses from other plugins. A member's export contains
  only their own data. The image rank card is **experimental**.
- **moderation** 0.3.0 (from 0.1.1):
  - **New commands:** timed bans, `/unban`, reviewed `/purge`, `/case`, `/history`, `/note`,
    quarantine, and `/lock` and `/slowmode` overlays. `/lock` neutralizes role and member
    allows through the `"*"` target.
  - **Bans:**
    - a temporary ban is lifted from the active case and the recorded Discord ban;
    - a later ban takes over an earlier one;
    - the optional `tempRoles` feature drops expired temporary roles from a quarantine.
  - **Appeals:** portal appeals. Staff cannot decide their own case.
  - **Interop:** the `sanction` action.
  - **Locales:** fully localized output.
  - **Upgrade:** needs new grants. `webhook.url` is deprecated in favour of the
    `WEBHOOK_URL` secret.
- **onboarding** 0.2.0 (from 0.1.0): locales and `enabled`. Choices are kept for 365 days.
  It needs `scheduler` for its migration.
- **polls** 0.2.0 (from 0.1.1): native Discord polls, scheduled starts, role eligibility
  and weights, `polls.closed` v2 and a paged `polls.voters` query. It needs
  `discord.members.read`.
- **reaction-roles** 0.2.0 (from 0.1.1): locales and `enabled`.
- **tickets** 0.4.0 (from 0.2.1):
  - **Queues:** typed queues, and opening a ticket from a message menu.
  - **Optional features:** modmail, rating prompts and managed messages.
  - **Staff:** first-response reminders, auto-close, HTML and Markdown transcripts, and
    moderation appeals.
  - **API:** `GET /tickets` is paged with a cursor.
  - **Upgrade:** existing tickets move to the `default` type.
- **voice-rooms** 0.2.0 (from 0.1.0; the bundle has 0.1.1):
  - **Permissions:** a status line when Discord refuses a permission.
  - **Creation:** crash-safe, with `provisionKey` and a sweep of interrupted creations.
  - **Rename limit:** two renames per 10 minutes, kept across restarts.
  - **Events:** optional `roomCreated` and `roomClosed` events.
- **welcome** 0.2.0 (from 0.1.1): triggers on join, verification or onboarding, with
  variants, a leave message, templates and the inviter. The image card is
  **experimental**. Role, DM and portal link grants became optional.

## [0.1.11] - 2026-11-25

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

[Unreleased]: https://github.com/cicord/cicord/compare/v0.1.11...HEAD
[0.1.11]: https://github.com/cicord/cicord/compare/v0.1.10...v0.1.11
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
