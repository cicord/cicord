# CODI — TODO

Living tracker of what is left. Updated as work lands (`[x]` = done, with the commit or PR).
No public release until the product is complete.

## Now — plugins & community portal
- [x] Verify every SDK feature end to end on the simulator (events, commands with every option
      type, components, modals, selects, schedules, tasks, KV, http.fetch, cross-plugin events,
      routes per access level, plugin-owned channels, reactions, message content):
      examples/showcase (tickets, polls, levels, moderation) + docs/plugin-coverage.md
- [ ] Same on real Discord: checklist at the end of docs/plugin-coverage.md
- [x] Plugin developer loop: `codi plugin new` → `codi dev --sim` hot reload → `typegen` →
      `plugin test` → unit tests with `@codi/sdk/testing` (component/modal runners, schema
      defaults, richer fakes); friction fixed (init placeholders work with `--sim`, `plugin new`
      prints the bot.yaml snippet, array-item defaults, `plugin test` docs)
- [ ] Unit tests of a plugin outside the monorepo (`codi plugin new` scaffolds no test setup;
      needs `@codi/sdk` installable, e.g. the tarballs embedded for `codi portal init`)
- [ ] SDK: fetch a message by id (`channel.message(id)`) instead of the `messages({ before })`
      workaround (needs `GetMessage` in the Discord contract)
- [ ] Community portal customization (docs/portal.md):
  - [x] theme from YAML end to end (colors, mode, logo served; banner too)
  - [x] no-code customization from YAML (title, tagline, Markdown description, banner, links, pages, locale)
  - [x] custom portal in the repo (`portal/`, committed build), served by the runtime from the active revision
  - [x] `codi portal init` (scaffold from the template) + `codi portal build` + dev loop against `codi dev`
  - [x] `@codi/*` usable outside the monorepo before npm publication (tarballs embedded in `codi`, vendored by `portal init`)
  - [x] embeddable in third-party sites (token mode: portal-scoped bearer sessions, `spec.portal.allowedOrigins`, examples/embed) — decision: SDK-first, plugin UI = plugin routes via the SDK (ADR-0008)
  - [x] reference portal = complete showcase of everything the SDK can do (leaderboard, tickets, developers page, en/fr)
  - [ ] generated route types for plugin routes (`definePlugin({routes})` → typed client)
  - [ ] publish `@codi/*` on npm, then make `codi portal init` use registry versions
- [ ] Real Discord: test member join (needs a second account)
- [x] Admin console redesign: shadcn/ui + Tailwind v4, collapsible sidebar with server switcher,
      ⌘K palette, true-black dark + light themes, i18n (`src/i18n/en.json`, typed keys), one
      status taxonomy with legend, humanized errors end to end (`internal/reconcile/humanize.go`
      → `error` objects in status / history / plan / sync failures), grouped sync history

## Discord parity (docs/discord-parity.md)
- [ ] Live status: boost level + boost count, features (Community…) in contract/API/admin UI; boost-gated fields blocked in plans with a reason
- [ ] Server icon + banner (cover): declarable in YAML and editable (upload) from the admin UI
- [ ] Community: enable + rules/public updates channels, explicit content filter, safety alerts channel
- [ ] Server: system message flags, AFK channel/timeout, widget, preferred locale, boost progress bar, banner
- [ ] AutoMod rules
- [ ] Discord native onboarding + welcome screen
- [ ] Roles: icon (image + emoji), gradient colors
- [ ] Channels: voice region, video quality, thread archive/slowmode defaults, forum tags & settings
- [ ] Emojis, stickers, soundboard from repository assets
- [ ] Invites, webhooks
- [ ] Document "Discord UI only" settings in the admin UI

## Setup wizard (docs/onboarding-wizard.md)
- [ ] Setup mode (no repo) + one-time setup token
- [ ] Secret store (encrypted) for bot tokens / OAuth secret
- [ ] Token validation, application ID + intents detection from the token
- [ ] Invite URL + live detection of servers the bot joined + permission/hierarchy checks
- [ ] Repository: embedded (default) or GitHub; automatic import + first commit
- [ ] OAuth step, verification checklist, first plugin from a bundled catalog

## Store (core of the vision) — apt/Homebrew-style sources (ADR-0011 update)
- [ ] Create the official plugin registry repository (`codi-dev/registry`, index.yaml) with the official plugins
- [ ] Registry trust levels (`official` / `unofficial`) in `codi.yaml spec.registries`; unofficial clearly labelled + explicit confirmation
- [ ] `codi registry add|remove|list|update`; index refresh in the runtime
- [ ] Admin UI: manage sources (add/remove unofficial repos, write-back to codi.yaml), search across sources, install
- [ ] `codi plugin publish`; verified publishers; signing
- [ ] Official plugins: moderation (warn/timeout/automod), XP & levels (+ portal leaderboard),
      tickets, reaction roles, audit logging, polls, scheduled announcements
- [ ] Store page in the admin UI (browse, permissions review, install via commit/PR)

## Production hardening
- [ ] GitHub App authentication (instead of a token) + commit statuses on synced SHAs
- [ ] Observability: `/metrics` (Prometheus), plugin error history, alerting hooks
- [ ] Postgres state backend; backup/restore guide
- [ ] Discord contract gaps: subcommands, autocomplete, `GetMessage`, server join/leave events,
      fatal disconnect event, `CommandOption.MinLength/MaxLength`, select menu types 5–8
- [ ] Simulator: reseed when hot reload adds servers/bots
- [ ] Security review: plugin sandbox escapes, Sobek memory limits, YAML fuzzing, link token reuse
- [ ] `codi mv` against a remote runtime (mapping move endpoint) — rename detection already in the reconciler
- [ ] Export runtime manifest checks / writeback helpers reused by the CLI (dedupe)
- [ ] `@codi/sdk/testing`: enforce route access levels and `messages.manage` in fakes

## Grand public & SaaS
- [ ] Onboarding without git: "Add to Discord" → embedded repo → automatic `codi import` → admin UI only
- [ ] Multi-tenant runtime, stronger plugin isolation (QuickJS/WASM), quotas, managed git, billing
- [ ] Portal as a Discord Activity (ADR-0016, verify constraints first)
- [ ] Admin UI i18n

## Release (later)
- [ ] Runtime license decision (ADR-0013) — SDKs are Apache-2.0
- [ ] GoReleaser binaries, Docker image (ghcr.io), npm publication of `@codi/*`
- [ ] `go install` with embedded UIs (commit built assets or release-only builds)

## Done (recent)
- [x] Runtime, reconciler, plugin host, simulator, API, admin UI, portal SDK, CLI (see git log)
- [x] Real Discord validation: reconcile, slash commands, onboarding roles, drift + sync, OAuth (78aea4a)
