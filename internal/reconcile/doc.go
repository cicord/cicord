// Package reconcile plans and applies the desired state of a Server resource
// (servers/<key>.yaml) onto a live Discord server, and detects drift.
//
// # API
//
//	snap, _ := reconcile.Fetch(ctx, client, serverID, botUserID) // live state (bot member for the hierarchy)
//	r := reconcile.New(reconcile.Options{Server: srv, RepoRoot: repo.Root, Client: c,
//	        Resources: store.Resources(), KV: store.KV("reconcile:" + srv.Metadata.Name),
//	        BotUserID: botID})
//	plan, _ := r.Plan(ctx)          // fetch + compute, nothing applied
//	res, _ := r.Apply(ctx, plan)    // ordered, resumable
//	plan, res, _ = r.Sync(ctx)      // plan + apply
//	drift, _ := r.Drift(ctx)        // desired vs live, for status
//	r.HandleEvent(ev)               // gateway events → drift flag / debounced selfHeal
//
// The diff itself is the pure function computePlan(planInput) so it can be tested
// without any client.
//
// # Identity and ownership
//
// YAML keys are mapped to Discord IDs through state.ResourceMap (Owner state.OwnerGit).
//   - A key mapped to an ID that no longer exists in Discord is created again (and the
//     mapping is replaced).
//   - A display name change is an update (rename), never a recreate.
//   - Adoption: a desired key without mapping adopts an unmapped live resource of the
//     same kind (role, category, or channel of the same type) with exactly the same
//     display name (action "adopt"; the mapping is recorded, differing fields are
//     updated by the same change). Candidates exclude every ID present in the map under
//     any owner, managed (integration) roles and @everyone; each live resource is adopted
//     at most once; desired keys are processed in key order and candidates in ID order.
//   - Resources that are not mapped, or mapped with a "plugin:" owner, are never
//     modified or deleted. A desired key whose mapping is owned by a plugin is reported
//     as Blocked{reason: "ownership: ..."}.
//   - Managed roles (bot/integration roles) are never touched. @everyone is only touched
//     through spec.everyone (Change kind "everyone").
//
// # Removed keys (prune)
//
// A git-owned mapping whose key is no longer in the YAML produces:
//   - "delete" when syncPolicy.prune && !deletionProtection;
//   - Blocked{reason: "deletionProtection"} when prune && deletionProtection (the
//     mapping is kept, so disabling the protection later deletes it);
//   - "unmanage" when !prune: the mapping is dropped and the Discord resource is left
//     as is. "unmanage" is an addition to the action list of spec/api.md §4, so the
//     user sees that the resource stops being managed. It is also used (whatever the
//     policy) when the removed resource is already gone from Discord.
//
// Deletions run last: channels, then categories, then roles. A delete only removes the
// mapping when it still points to the deleted ID.
//
// # Compared fields and zero values
//
// Text-like channel names (text, announcement, forum, media) are compared after the
// normalization Discord applies (lowercase, whitespace runs → "-"), so "Général Chat"
// in YAML matches #général-chat.
//
// Always managed (the YAML zero value is applied): role name, color (0 = none), hoist,
// mentionable, permissions; category/channel name and overwrites; channel topic (text,
// announcement, forum, media), nsfw, slowmode (text, voice, stage, forum, media) and
// userLimit (voice, stage). Managed only when set: channel bitrate (> 0), every server
// profile field (non-empty), the icon, everyone (when spec.everyone is present), and
// every Discord-parity field (parity.go): afk, widget, community, system message
// toggles, role colors/icon/emoji, channel extras (voice region, video quality, thread
// defaults, forum tags/settings), welcome screen, onboarding, AutoMod rules, emojis,
// stickers and soundboard sounds.
//
// Images (server icon, banner, splash, role icons) are paths relative to the server
// YAML file. Discord only exposes URLs, so the SHA-256 of the last uploaded file and the
// URL observed after the upload are kept in the KV ("icon", "image:banner",
// "image:splash", "image:role:<key>"); an image is uploaded again when the file hash or
// the live URL differs. Expressions keep only the hash ("image:emoji:<key>"…): Discord
// cannot replace their file, so a changed file is blocked ("recreate: …").
//
// # Gating (parity.go)
//
// Boost-gated settings (banner, splash, animated icon, role icons, gradient role colors)
// are checked against the live boost level and feature flags, and Community-only
// settings (announcement/stage channels, rules/updates/safety channels, welcome screen,
// onboarding) against the Community state after the plan; expression slots and AutoMod
// rule caps are counted. What Discord would refuse is Blocked with a reason prefixed
// "boost: ", "community: " or "limit: " (ExplainBlocked) instead of failing at apply.
// Enabling Community is planned once with its prerequisites (rules and updates channels,
// verification ≥ low, explicit filter allMembers) in phaseServerRefs, after the channels
// exist; channels that need Community are created in phaseCommunityChannels right after.
//
// # Overwrites
//
// Targets: "@everyone" (the server ID), a role key (its mapped ID, resolved at apply
// time when the role is created by the same plan) or "member:<id>". Entries with empty
// allow and deny are ignored on both sides; comparison is order-insensitive. Overwrites
// of a managed category/channel are owned as a whole: overwrites for unknown targets
// are removed. A channel with syncWithCategory (config.Channel.SyncsWithCategory) gets
// exactly its category's desired overwrites, written explicitly (LockPermissions is not
// used); without a category, its desired overwrites are empty.
//
// # Channel types
//
// The type of a channel cannot be changed in place. When the live type differs from
// the YAML: with prune && !deletionProtection the channel is replaced (create, then
// the old one is deleted at the end); otherwise the change is Blocked{reason:
// "type change: ..."}. Threads are ignored.
//
// # Ordering
//
// Roles: the desired order of managed roles is spec.hierarchy (highest first), then
// the other declared roles in key order. Managed roles permute among the slots they
// occupy in the live order (sorted by position desc, then ID); unmanaged roles keep
// their place. Roles created by the plan appear at the bottom (Discord creates roles at
// position 1). Only roles below the bot's highest role are ordered; positions 1..k are
// rewritten for them (only the changed ones are sent). A managed role at or above the
// bot's highest role cannot be edited, moved or deleted: Blocked{reason: "hierarchy:
// ..."}. When no bot user is configured, there is no hierarchy constraint.
//
// Categories are ordered among categories, channels within their parent (no parent
// first): position ascending, a missing position after explicit ones, ties by key.
// Like Discord, text-like and voice-like (voice, stage) channels of a parent are two
// separate position groups (voice channels are always shown below text channels).
// Same slot algorithm, positions 0..n-1 per group (only the changed ones are sent). A "reorder" change is emitted only
// when the relative order of managed items differs (as predicted after the plan's
// creates and moves). Apply recomputes positions from a fresh fetch, so the final order
// is right even if Discord placed new items differently than predicted.
//
// # Apply
//
// Order: server basics and @everyone → roles (adopt/create/update) → role positions →
// categories → channels (create/update with parent and overwrites) → server channel
// references and the Community toggle → channels that need Community → channel
// positions → widget → welcome screen → onboarding → AutoMod → expressions → deletions
// (AutoMod, expressions, channels, categories, roles) and unmanage. IDs are
// resolved by key at apply time from a working map seeded with the resource map and
// updated after every create/adopt; mappings are written as soon as a create succeeds,
// so a crash mid-apply re-plans correctly (and a resource created but not recorded is
// adopted by name). Apply continues past failures; changes depending on a failed one
// fail with an "unresolved" error. Result lists applied and failed changes; the
// returned error joins the underlying errors (errors.Is works with discord.ErrForbidden
// and friends). The position phases recompute the order from a fresh ListRoles /
// ListChannels whenever the plan touches roles (resp. categories/channels).
//
// Apply needs the unexported data attached by Plan: it accepts Plans produced by the
// same Reconciler (a Plan decoded from JSON cannot be applied).
//
// # Drift and selfHeal
//
// Drift returns one entry per differing field of the current plan (spec/api.md §3).
// HandleEvent marks drift for role/channel events on git-owned resources, @everyone and
// server updates; with syncPolicy.selfHeal it schedules a debounced Sync.
package reconcile
