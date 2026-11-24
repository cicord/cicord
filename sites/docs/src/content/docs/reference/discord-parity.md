---
title: Discord parity
description: Which Discord server settings Cicord can declare and reconcile, which are not supported yet, and which only the Discord client can change.
---

Goal: everything a server owner can set in Discord **and** that the bot API allows is
declarable in YAML, one to one; the rest is listed here as "Discord only". This table comes
from an audit of the Discord server settings UI against the `Server` resource, on the test
server on 2026-11-21.

**Legend.** **Yes**: declared in YAML and reconciled. **Not yet**: the API allows it, Cicord does
not support it yet. **Discord only**: not settable by a bot. **Data**: runtime data, not
configuration.

## Server profile and overview

| Discord setting | Cicord | Field and notes |
|---|---|---|
| Name | Yes | `profile.name` |
| Icon | Yes | `profile.icon`; animated icons need boost level 1. Also uploadable from the admin console's Server settings. |
| Banner image | Yes | `profile.banner`; boost level 2, blocked in the plan below it. Also uploadable from the admin console's Server settings. |
| Invite splash | Yes | `profile.splash`; boost level 1. |
| Discovery splash | Discord only | Needs Discovery. |
| Description | Yes | `profile.description`; shown for Community and Discovery servers. |
| Preferred locale | Yes | `profile.preferredLocale` |
| Boost progress bar | Yes | `profile.premiumProgressBar` |
| Profile banner color, traits, games played, private profile, server tag | Discord only | Not in the public API. |

## Engagement

| Discord setting | Cicord | Field and notes |
|---|---|---|
| System messages channel | Yes | `profile.systemChannel` |
| System message toggles | Yes | `profile.systemChannelFlags`: `welcomeMessages`, `welcomeStickerReplies`, `boostMessages`, `setupTips`, `roleSubscriptionMessages`, `roleSubscriptionStickerReplies` |
| Default notifications | Yes | `profile.defaultNotifications` |
| Inactive (AFK) channel and timeout | Yes | `afk: { channel, timeout }` |
| Server widget | Yes | `widget: { enabled, channel }` |
| Activity feed | Discord only | |

## Access and safety

| Discord setting | Cicord | Field and notes |
|---|---|---|
| Verification level | Yes | `profile.verificationLevel` |
| Explicit (sensitive) content filter | Yes | `profile.explicitContentFilter` |
| Safety alerts channel | Yes | `community.safetyAlertsChannel` |
| Join mode (invite only, apply, discovery), age-restricted server, rules screening form, 2FA requirement for moderation, members in channel list | Discord only | |

## Community

| Discord setting | Cicord | Field and notes |
|---|---|---|
| Enable Community | Yes | `community.enabled`; prerequisites (rules and updates channels, verification at least low, content filter on all members) are checked by `cicord validate` and blocked in the plan with the missing list. |
| Rules channel | Yes | `community.rulesChannel` (`profile.rulesChannel` is a deprecated alias); blocked in the plan without Community. |
| Community updates channel | Yes | `community.updatesChannel` |
| Welcome screen | Yes | `welcomeScreen` |
| Onboarding (questions, default channels, mode) | Yes | `onboarding`; see [Native onboarding and the onboarding plugin](#native-onboarding-and-the-onboarding-plugin). |
| Announcement channels | Yes | `type: announcement`; they need Community: blocked in the plan without it, created right after Community is enabled. |

## Roles

| Discord setting | Cicord | Field and notes |
|---|---|---|
| Name, color, hoist, mentionable, permissions, hierarchy | Yes | `roles`, `hierarchy` |
| Gradient and holographic colors | Yes | `roles.*.colors`; boost level 2. `color` stays the solid shorthand. |
| Role icon image | Yes | `roles.*.icon`; boost level 2. |
| Role icon emoji | Yes | `roles.*.emoji`; exclusive with `icon`. |
| `@everyone` permissions | Yes | `everyone.permissions` |
| Linked roles (role connections) | Not yet | Application-level metadata. |

## Channels and categories

| Discord setting | Cicord | Field and notes |
|---|---|---|
| Name, type, category, position, topic, NSFW, slowmode, overwrites, sync with category | Yes | |
| Voice bitrate, user limit, region, video quality | Yes | `bitrate`, `userLimit`, `voiceRegion`, `videoQuality` |
| Thread "hide after inactivity", thread slowmode | Yes | `threadArchiveAfter`, `threadSlowmode` |
| Forum and media: tags, default reaction, sort order, layout, guidelines | Yes | `tags` (keyed; IDs kept on rename), `defaultReaction`, `sortOrder`, `layout`, `guidelines` |
| Stage channels | Yes | `type: stage` |
| Spoiler channel | Not yet | To verify in the API. |
| Channel webhooks | Not yet | Planned (they involve secrets). |
| Invites | Not yet | Declared permanent invite links are planned. |

## Expressions and moderation

| Discord setting | Cicord | Field and notes |
|---|---|---|
| Emojis | Yes | `emojis: { key: { name, image, roles } }`; slots per boost level blocked in the plan. A changed image file needs a new key. |
| Stickers | Yes | `stickers: { key: { name, description, emoji, file } }` |
| Soundboard sounds | Yes | `soundboard: { key: { name, sound, volume, emoji } }` |
| AutoMod rules (mention spam, spam, keyword presets, custom keywords, actions, exemptions) | Yes | `automod: { key: {...} }`; adopted by name and trigger, rule caps blocked in the plan. |
| Bans, scheduled events | Data | Handled by plugins, not configuration. |
| Audit log | Data | Read-only. |
| Server template, boost perks | Discord only | |

## Live server status

The admin console and the planner read, without changing anything:

| Information | Source | Use |
|---|---|---|
| Boost level and boost count | `premium_tier`, `premium_subscription_count` | Features locked by the boost level (banner, role icons, gradient role colors, splash, upload limits…) are **blocked in the plan with a clear reason** ("banner requires boost level 2, server is level 0") instead of failing on apply. |
| Community and other features | `features`: `COMMUNITY`, `DISCOVERABLE`, `WELCOME_SCREEN_ENABLED`, `ROLE_ICONS`… | Community-only settings are blocked in the plan while Community is off. |
| Current icon, banner and splash | `iconUrl`, `bannerUrl`, `splashUrl` in the status API | Shown in the admin console; the server switcher shows the icon. |

`cicord import` (and the setup wizard's import) downloads the server icon, banner and invite
splash, role icons, emojis (GIF when animated), stickers and soundboard sounds from the
Discord CDN into `servers/assets/<server>/` and records them as uploaded, so the first plan is
empty. Files over Discord's upload limits and managed emojis are reported and skipped.

Hand edits of managed AutoMod rules, emojis, stickers and soundboard sounds reach the
reconciler through the gateway (`AUTO_MODERATION_RULE_*`, `GUILD_EMOJIS_UPDATE`,
`GUILD_STICKERS_UPDATE`, `GUILD_SOUNDBOARD_SOUND_*`) and trigger [self-heal](../../concepts/reconciliation/#drift-and-self-heal).

## Example

```yaml title="servers/ma-commu.yaml (excerpt)"
spec:
  profile:
    banner: ./assets/ma-commu-banner.png # boost level 2
    explicitContentFilter: allMembers
    preferredLocale: fr
    premiumProgressBar: true
    systemChannel: general
    systemChannelFlags: { welcomeMessages: true, setupTips: false }
  community: { enabled: true, rulesChannel: rules, updatesChannel: mod-news }
  afk: { channel: lounge, timeout: 900 }
  widget: { enabled: true, channel: general }
  roles:
    mod: { name: Mod, colors: { primary: "#5865f2", secondary: "#eb459e" }, emoji: "🛡️" }
  channels:
    help:
      name: help
      type: forum
      guidelines: Search before asking.
      tags: { question: { name: Question, emoji: "❓" }, solved: { name: Solved, moderated: true } }
      defaultReaction: "👍"
      sortOrder: creationDate
      layout: list
  automod:
    bad-words:
      name: Bad words
      trigger: keyword
      keywords: [darn, "heck*"]
      actions: { block: { message: "Language!" }, alert: mod-log, timeout: 10m }
      exemptRoles: [mod]
  welcomeScreen:
    description: Welcome!
    channels: [{ channel: rules, description: Read the rules, emoji: "📜" }]
  onboarding:
    enabled: true
    defaultChannels: [rules, general, news, help, memes, events, lounge]
    prompts:
      - key: games
        title: What do you play?
        options: [{ title: Minecraft, emoji: "⛏️", roles: [minecraft], channels: [minecraft] }]
  emojis: { party: { name: party, image: ./assets/party.png } }
  stickers: { wave: { name: Wave, emoji: wave, file: ./assets/wave.png } }
  soundboard: { horn: { name: Horn, sound: ./assets/horn.ogg, volume: 0.6 } }
```

Every field is described in [Server](../../configuration/server/).

## Behavior observed on real Discord

Verified on the test server, with Community enabled by Cicord, on 2026-11-21:

- **Default mention spam rule.** A Community server lists a "Block Mention Spam" rule created
  by Discord's AutoMod system user (`1008776202191634432`). It is a placeholder: `PATCH` and
  `DELETE` answer 404, and it does not count toward the caps. Cicord ignores it, and declaring
  a real `mentionSpam` rule replaces it.
- **Once-per-server triggers** (`mentionSpam`, `spam`, `keywordPreset`, `memberProfile`): an
  existing rule with that trigger is adopted whatever its name, and renamed, also when the
  mapped rule was deleted and recreated by hand.
- **The mention spam rule cannot be deleted on a Community server** (403, code 200006):
  removing its key with prune is blocked in the plan (`undeletable`); set `enabled: false`
  instead.
- **Onboarding default channels must be open to `@everyone`** (`ViewChannel`, plus `Connect`
  for voice and stage; Discord's `DEFAULT_CHANNEL_REQUIRES_EVERYONE_ACCESS`), and 5 of the
  counted channels must let `@everyone` send messages. `cicord validate` reports both when
  `spec.everyone` is declared.
- **Boost gating** (banner, role icon) is blocked in the plan at boost level 0, as designed.
- **Roles can share a position.** After a drag in the Discord client, the API listed every
  role at position 1; Discord then ranks the older role (lower ID) higher, and the bot could
  edit the tied roles. Cicord turns positions into distinct ranks in that order, so hierarchy
  checks and the reorder planner see the real hierarchy. Any bulk `PATCH /roles` (even a no-op
  on one role) makes Discord renumber every role 1..n in that same order, so the positions
  Cicord writes from those ranks stay consistent.

## Native onboarding and the onboarding plugin

They are complementary and can run together:

- **Discord onboarding** (`spec.onboarding`) runs inside the Discord client when a member joins
  a Community server: default channels and a few questions that grant roles and channels. It
  needs Community, and at least 7 default channels (5 where `@everyone` can send messages) in
  default mode.
- **The Cicord [onboarding plugin](../../plugins/official/#onboarding)** runs on the community
  portal (and with `/onboarding`): richer steps, custom logic, state in its KV storage, links
  to the portal profile. It works on any server.

A typical setup keeps the essential interest roles in Discord onboarding, so members see them
right away, and uses the plugin for everything else; both can grant the same role keys.

## Not verified on real Discord yet

Stickers, soundboard sounds, gradient role colors and role icons are implemented and tested on
the simulator, but still untested on a real server (they need boosts). The widget, onboarding
and welcome screen have no Discord events, so manual changes to them are only seen at the next
plan.

## What's next

The parity work went in this order: live status and boost gating, then Community and the
server profile and engagement settings, AutoMod, native onboarding and the welcome screen,
role icons and gradient colors with the channel extras, and emojis, stickers and sounds from
repository assets. Invites and channel webhooks come next; the other **Not yet** rows above
(linked roles, spoiler channels) are open.
