---
title: Discord parity
description: Which Discord server settings CODI can declare and reconcile, which are not supported yet, and which only the Discord client can change.
---

Goal: everything a server owner can set in Discord **and** that the bot API allows is
declarable in YAML, one to one; the rest is listed here as "Discord only". This table comes
from an audit of the Discord server settings against the `Server` resource.

**Legend.** **Yes**: declared in YAML and reconciled. **Not yet**: the API allows it, CODI does
not support it yet. **Discord only**: not settable by a bot. **Data**: runtime data, not
configuration.

## Server profile and overview

| Discord setting | CODI | Field and notes |
|---|---|---|
| Name | Yes | `profile.name` |
| Icon | Yes | `profile.icon`; animated icons need boost level 1. Also uploadable from the admin console. |
| Banner image | Yes | `profile.banner`; boost level 2, blocked in the plan below it. |
| Invite splash | Yes | `profile.splash`; boost level 1. |
| Discovery splash | Discord only | Needs Discovery. |
| Description | Yes | `profile.description`; shown for Community and Discovery servers. |
| Preferred locale | Yes | `profile.preferredLocale` |
| Boost progress bar | Yes | `profile.premiumProgressBar` |
| Profile banner color, traits, games played, private profile, server tag | Discord only | Not in the public API. |

## Engagement

| Discord setting | CODI | Field and notes |
|---|---|---|
| System messages channel | Yes | `profile.systemChannel` |
| System message toggles | Yes | `profile.systemChannelFlags`: `welcomeMessages`, `welcomeStickerReplies`, `boostMessages`, `setupTips`, `roleSubscriptionMessages`, `roleSubscriptionStickerReplies` |
| Default notifications | Yes | `profile.defaultNotifications` |
| Inactive (AFK) channel and timeout | Yes | `afk: { channel, timeout }` |
| Server widget | Yes | `widget: { enabled, channel }` |
| Activity feed | Discord only | |

## Access and safety

| Discord setting | CODI | Field and notes |
|---|---|---|
| Verification level | Yes | `profile.verificationLevel` |
| Explicit (sensitive) content filter | Yes | `profile.explicitContentFilter` |
| Safety alerts channel | Yes | `community.safetyAlertsChannel` |
| Join mode (invite only, apply, discovery), age-restricted server, rules screening form, 2FA requirement for moderation, members in channel list | Discord only | |

## Community

| Discord setting | CODI | Field and notes |
|---|---|---|
| Enable Community | Yes | `community.enabled`; prerequisites (rules and updates channels, verification at least low, content filter on all members) are checked by `codi validate` and blocked in the plan with the missing list. |
| Rules channel | Yes | `community.rulesChannel` (`profile.rulesChannel` is a deprecated alias) |
| Community updates channel | Yes | `community.updatesChannel` |
| Welcome screen | Yes | `welcomeScreen` |
| Onboarding (questions, default channels, mode) | Yes | `onboarding` |
| Announcement channels | Yes | `type: announcement`; created right after Community is enabled. |

## Roles

| Discord setting | CODI | Field and notes |
|---|---|---|
| Name, color, hoist, mentionable, permissions, hierarchy | Yes | `roles`, `hierarchy` |
| Gradient and holographic colors | Yes | `roles.*.colors`; boost level 2. `color` stays the solid shorthand. |
| Role icon image | Yes | `roles.*.icon`; boost level 2. |
| Role icon emoji | Yes | `roles.*.emoji`; exclusive with `icon`. |
| `@everyone` permissions | Yes | `everyone.permissions` |
| Linked roles (role connections) | Not yet | Application-level metadata. |

## Channels and categories

| Discord setting | CODI | Field and notes |
|---|---|---|
| Name, type, category, position, topic, NSFW, slowmode, overwrites, sync with category | Yes | |
| Voice bitrate, user limit, region, video quality | Yes | `bitrate`, `userLimit`, `voiceRegion`, `videoQuality` |
| Thread "hide after inactivity", thread slowmode | Yes | `threadArchiveAfter`, `threadSlowmode` |
| Forum and media: tags, default reaction, sort order, layout, guidelines | Yes | `tags` (keyed; IDs kept on rename), `defaultReaction`, `sortOrder`, `layout`, `guidelines` |
| Stage channels | Yes | `type: stage` |
| Channel webhooks | Not yet | Planned (they involve secrets). |
| Invites | Not yet | Declared permanent invite links are planned. |

## Expressions and moderation

| Discord setting | CODI | Field and notes |
|---|---|---|
| Emojis | Yes | `emojis: { key: { name, image, roles } }`; slots per boost level blocked in the plan. |
| Stickers | Yes | `stickers: { key: { name, description, emoji, file } }` |
| Soundboard sounds | Yes | `soundboard: { key: { name, sound, volume, emoji } }` |
| AutoMod rules | Yes | `automod: { key: {...} }`; adopted by name and trigger, rule caps blocked in the plan. |
| Bans, scheduled events | Data | Handled by plugins, not configuration. |
| Audit log | Data | Read-only. |
| Server template, boost perks | Discord only | |

## Live server status

The admin console and the planner read the live boost level and boost count, the feature flags
(`COMMUNITY`, `ROLE_ICONS`…) and the current icon, banner and splash. Features locked by the
boost level are **blocked in the plan with a clear reason** ("banner requires boost level 2,
server is level 0") instead of failing on apply.

`codi import` (and the setup wizard's import) downloads the server icon, banner and invite
splash, role icons, emojis (GIF when animated), stickers and soundboard sounds into
`servers/assets/<server>/`, so the first plan is empty. Files over Discord's upload limits and
managed emojis are reported and skipped.

## Behavior observed on real Discord

- **Default mention spam rule.** A Community server lists a "Block Mention Spam" rule created
  by Discord's AutoMod system user. It is a placeholder that does not count toward the caps;
  CODI ignores it, and declaring a real `mentionSpam` rule replaces it.
- **Once-per-server triggers** (`mentionSpam`, `spam`, `keywordPreset`, `memberProfile`): an
  existing rule with that trigger is adopted whatever its name, and renamed.
- **The mention spam rule cannot be deleted on a Community server**: removing its key with
  prune is blocked in the plan (`undeletable`); set `enabled: false` instead.
- **Onboarding default channels must be open to `@everyone`** (`ViewChannel`, plus `Connect`
  for voice and stage), and 5 of them must let `@everyone` send messages. `codi validate`
  reports both when `spec.everyone` is declared.
- **Roles can share a position** after a drag in the Discord client. CODI ranks tied roles the
  way Discord does (the older role is higher), so hierarchy checks and reordering see the real
  order.

## Native onboarding and the onboarding plugin

They are complementary and can run together:

- **Discord onboarding** (`spec.onboarding`) runs inside the Discord client when a member joins
  a Community server: default channels and a few questions that grant roles and channels. It
  needs Community, and at least 7 default channels (5 where `@everyone` can send messages) in
  default mode.
- **The CODI [onboarding plugin](../../plugins/official/#onboarding)** runs on the community
  portal (and with `/onboarding`): richer steps, custom logic, links to the portal profile. It
  works on any server.

A typical setup keeps the essential interest roles in Discord onboarding, so members see them
right away, and uses the plugin for everything else; both can grant the same role keys.

## Not verified on real Discord yet

Stickers, soundboard sounds, gradient role colors and role icons are implemented and tested on
the simulator, but still untested on a real server (they need boosts). The widget, onboarding
and welcome screen have no Discord events, so manual changes to them are only seen at the next
plan.
