# Cicord — TODO

Living tracker of what is left. Updated as work lands (`[x]` = done, with the commit or PR).
No public release until the product is complete.

## Now — plugins & community portal
- [x] Verify every SDK feature end to end on the simulator (events, commands with every option
      type, components, modals, selects, schedules, tasks, KV, http.fetch, cross-plugin events,
      routes per access level, plugin-owned channels, reactions, message content):
      examples/showcase (tickets, polls, levels, moderation) + docs/plugin-coverage.md
- [ ] Same on real Discord: checklist at the end of docs/plugin-coverage.md
- [x] Plugin developer loop: `cicord plugin new` → `cicord dev --sim` hot reload → `typegen` →
      `plugin test` → unit tests with `@cicord/sdk/testing` (component/modal runners, schema
      defaults, richer fakes); friction fixed (init placeholders work with `--sim`, `plugin new`
      prints the bot.yaml snippet, array-item defaults, `plugin test` docs)
- [x] Unit tests of a plugin outside the monorepo: `cicord plugin new` scaffolds `node --test` with the vendored @cicord/sdk
- [ ] SDK: fetch a message by id (`channel.message(id)`) instead of the `messages({ before })`
      workaround (needs `GetMessage` in the Discord contract)
- [ ] Community portal customization (docs/portal.md):
  - [x] theme from YAML end to end (colors, mode, logo served; banner too)
  - [x] no-code customization from YAML (title, tagline, Markdown description, banner, links, pages, locale)
  - [x] custom portal in the repo (`portal/`, committed build), served by the runtime from the active revision
  - [x] `cicord portal init` (scaffold from the template) + `cicord portal build` + dev loop against `cicord dev`
  - [x] `@cicord/*` usable outside the monorepo before npm publication (tarballs embedded in `cicord`, vendored by `portal init`)
  - [x] embeddable in third-party sites (token mode: portal-scoped bearer sessions, `spec.portal.allowedOrigins`, examples/embed) — decision: SDK-first, plugin UI = plugin routes via the SDK (ADR-0008)
  - [x] reference portal = complete showcase of everything the SDK can do (leaderboard, tickets, developers page, en/fr)
  - [x] typed plugin routes: route contracts (`spec.routes.types`, `defineRoutes`), `cicord portal typegen`, `client.routes`, `useRouteQuery` (levels, polls)
  - [ ] route contracts for tickets, moderation, announcements
  - [x] npm publication prepared (package metadata, release workflow, `--from-npm`), RELEASING.md
  - [ ] **Owner:** create the npm org `cicord`, NPM_TOKEN secret, public repo, then run the release workflow (RELEASING.md)
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
- [x] Wizard on real Discord, by hand: live events check (member message received) and a real "Log in with Discord" round trip; all 9 diagnostics green
- [x] Import downloads icon, banner, splash, role icons, emojis, stickers, sounds (empty first plan)
- [x] Privileged intents checked against the live application flags (bot not connected + clear status when missing)
- [x] Gateway events for AutoMod rules, emojis, stickers, soundboard sounds: hand edits of managed ones trigger a resync (intents requested only when managed)
- [ ] Widget, onboarding and welcome screen still drift until the next plan (Discord has no events for them)
- [ ] Invites (declared permanent invite links), webhooks (secrets)
- [ ] `cicord import --force` leaves stale files in servers/assets/<key>/
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
- [x] Sources: bundled official plugins (`cicord`, reserved) + `cicord.yaml spec.registries`; trust computed from the URL (unofficial unless official URL), `verified` only honoured for official
- [x] Store API (§11): browse, detail from plugin.yaml at the resolved version (no unofficial code run before consent), install with reviewed version/commit/integrity (`409 changed`), explicit confirmation for unofficial, registries add/remove (project admins, https only, `in_use`)
- [x] `cicord registry list|add|remove|update|search` (file:// and local paths for dev registries)
- [x] Admin UI Store page: browse/search by source, trust badges, permissions review, install (bot, instance, config, unofficial confirmation, `changed` re-review), sources add/remove
- [x] Signed official index (ed25519 envelope bound to URL, serial, expiry; anti-rollback serial), implicit official registry merged into source `cicord`, `cicord registry keygen|sign|verify`
- [ ] **Owner:** create the official registry repository + production signing key, set `registry.OfficialURLs` / `OfficialKeys`, schedule the re-signing CI job (docs/plugins.md §9)
- [x] Background refresh of registry indexes (10 s after start, then every 30 min)
- [x] Uninstall (API: instance, lock entry or copied plugin dir; per-server for multi-server instances)
- [x] Plugin updates: detection (registry tags, bundled copies with modified-copy detection), review with permission diff, update in one write-back, `cicord plugin update`, admin badges/filters/review page
- [x] Private registries: `secretRef` per registry, token in the secret store, sent to the registry host only; `cicord registry add --private`
- [x] `cicord plugin publish` (checks, tag, index entry); verified publishers honoured only from the verified official index
- [ ] `cicord plugin publish`: build from the tagged tree (today: the working tree)
- [x] Official plugins: reaction-roles, audit-log, announcements (9 in the catalog); moderation publishes `moderation.case`
- [ ] SDK gaps found while writing them: subcommands, channel/role/ban events and actor, message content before edit/delete, allowedMentions id lists, Intl/time zones in the plugin host, removing others' reactions, role hierarchy info

- [ ] Admin login loses the deep link (redirects to /admin/ instead of the page that asked for login)

## Production hardening
- [ ] GitHub App authentication (instead of a token) + commit statuses on synced SHAs
- [ ] Observability: `/metrics` (Prometheus), plugin error history, alerting hooks
- [ ] Postgres state backend; backup/restore guide
- [ ] Discord contract gaps: subcommands, autocomplete, `GetMessage`, server join/leave events,
      fatal disconnect event, `CommandOption.MinLength/MaxLength`, select menu types 5–8
- [ ] Simulator: reseed when hot reload adds servers/bots
- [ ] Security review: plugin sandbox escapes, Sobek memory limits, YAML fuzzing, link token reuse
- [ ] `cicord mv` against a remote runtime (mapping move endpoint) — rename detection already in the reconciler
- [ ] Export runtime manifest checks / writeback helpers reused by the CLI (dedupe)
- [ ] `@cicord/sdk/testing`: enforce route access levels and `messages.manage` in fakes

## Grand public & SaaS
- [ ] Onboarding without git: "Add to Discord" → embedded repo → automatic `cicord import` → admin UI only
- [ ] Multi-tenant runtime, stronger plugin isolation (QuickJS/WASM), quotas, managed git, billing
- [ ] Portal as a Discord Activity (ADR-0016, verify constraints first)
- [ ] Admin UI i18n

## Release (later)
- [ ] GoReleaser binaries, Docker image (ghcr.io), npm publication of `@cicord/*`
- [ ] `go install` with embedded UIs (commit built assets or release-only builds)

## Done (recent)
- [x] License: Apache-2.0 for everything (ADR-0013, 2026-11-24)
- [x] Runtime, reconciler, plugin host, simulator, API, admin UI, portal SDK, CLI (see git log)
- [x] Real Discord validation: reconcile, slash commands, onboarding roles, drift + sync, OAuth (ce2a51c)
