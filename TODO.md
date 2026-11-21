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
- [x] Live status: boost level + boost count, features (Community…) in contract/API/admin UI; boost-gated fields blocked in plans with a reason
- [x] Server icon + banner (cover): declarable in YAML and editable (upload) from the admin UI (`PUT /api/v1/servers/{server}/assets/{kind}`)
- [x] Community: enable + rules/public updates channels, explicit content filter, safety alerts channel (prerequisites in validate + plan)
- [x] Server: system message flags, AFK channel/timeout, widget, preferred locale, boost progress bar, banner, invite splash
- [x] AutoMod rules (keyed, resource map kind `automod`, adoption, drift, prune, rule caps)
- [x] Discord native onboarding + welcome screen
- [x] Roles: icon (image + emoji), gradient colors
- [x] Channels: voice region, video quality, thread archive/slowmode defaults, forum tags & settings
- [x] Emojis, stickers, soundboard from repository assets (slot limits per boost level)
- [x] Document "Discord UI only" settings in the admin UI
- [x] Verify the parity checklist on the real Discord test server: Community toggle, AFK/locale/flags/filter, icon upload, widget, welcome screen, AutoMod (+ default mention spam placeholder), emoji upload, native onboarding, boost gating (docs/discord-parity.md "Behaviour observed on real Discord")
- [ ] Real Discord, still untested: stickers (multipart), soundboard, role gradient colors / icons (need boosts)
- [x] Setup wizard on real Discord: token, intents, server, hierarchy, embedded repo, import (empty plan), OAuth secret, verify, welcome plugin (commands registered), finish
- [ ] Wizard on real Discord, by hand: the live events check (needs a member message) and a real "Log in with Discord" round trip
- [ ] Import: download the server icon and emojis into the repository instead of only warning
- [ ] Plugin privileged-intent warning is printed even when the intent is enabled in the portal: check the live application flags first
- [ ] Gateway events for the new kinds (AUTO_MODERATION_RULE_*, GUILD_EMOJIS_UPDATE, GUILD_STICKERS_UPDATE,
      GUILD_SOUNDBOARD_SOUND_*, widget/onboarding have none): today hand edits of AutoMod rules, expressions,
      widget, onboarding and welcome screen only show as drift at the next plan (status refresh / sync), and
      selfHeal does not trigger on them
- [ ] Invites (declared permanent invite links), webhooks (secrets)
- [ ] `codi import`: download icon/banner/role icons/emojis/stickers/sounds into the repository (today: warnings)
- [ ] Holographic role style preset (`colors: holographic`) instead of the three magic colors
- [ ] Admin UI: plan page refreshes on live server updates (boost level changes need a manual Refresh)

## Setup wizard (docs/onboarding-wizard.md)
- [x] Setup mode (no repo) + one-time setup token
- [x] Secret store (encrypted) for bot tokens / OAuth secret
- [x] Token validation, application ID + intents detection from the token
- [x] Invite URL + live detection of servers the bot joined + permission/hierarchy checks
- [x] Repository: embedded (default) or GitHub; automatic import + first commit
- [x] OAuth step, verification checklist, first plugin from a bundled catalog
- [x] Diagnostics page (same checks as the wizard's Verify step)
- [ ] Setup wizard: GitHub App instead of a personal access token; webhook setup step
- [ ] Setup wizard: manage several servers / bots; Store page on top of the catalog (registries)

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
