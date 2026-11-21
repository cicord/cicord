# CODI — TODO

Living tracker of what is left. Updated as work lands (`[x]` = done, with the commit or PR).
No public release until the product is complete.

## Now — plugins & community portal
- [ ] Verify every SDK feature end to end (events, commands with options, components, modals,
      selects, schedules, tasks, KV, http.fetch, cross-plugin events, routes, plugin-owned
      channels, reactions, message content) on the simulator and on real Discord
- [ ] Plugin developer loop: `codi plugin new` → `codi dev --sim` hot reload → `typegen` →
      `plugin test` → unit tests with `@codi/sdk/testing`; fix friction found
- [ ] Community portal customization:
  - [ ] theme from YAML end to end (colors, mode, logo served)
  - [ ] no-code customization from YAML (texts, links, pages)
  - [ ] custom portal in the repo (`portal/`), served by the runtime from the active revision
  - [ ] `codi portal init` (scaffold from the template) + dev loop against `codi dev`
  - [ ] `@codi/*` usable outside the monorepo before npm publication
  - [ ] embeddable in third-party sites (cross-origin auth with bearer sessions) — decision: SDK-first, plugin UI = plugin routes via the SDK
  - [ ] reference portal = complete showcase of everything the SDK can do
- [ ] Real Discord: test member join (needs a second account)

## Discord parity (docs/discord-parity.md)
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

## Store (core of the vision)
- [ ] Create the plugin registry repository (`codi-dev/registry`, index.yaml)
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
