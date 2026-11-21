# Discord ↔ CODI parity

Audit of the Discord server settings UI (test server "Ma Commu", 2026-11-21) against what a
CODI `Server` resource can declare and reconcile. Goal: everything a server owner can set in
Discord and that the bot API allows must be declarable in YAML (1:1), and the rest must be
listed explicitly as "Discord UI only".

Legend: ✅ declared + reconciled · 🟡 partial · ❌ missing (API allows it) · 🚫 not settable by a bot (Discord UI only)

## Server profile & overview
| Discord setting | CODI | Notes |
|---|---|---|
| Name | ✅ `profile.name` | |
| Icon | ✅ `profile.icon` | |
| Banner image (boost level 2) | ❌ | `banner` field (PATCH guild) |
| Invite splash / discovery splash | ❌ | boost / discovery only |
| Description | ✅ `profile.description` | shown for community/discovery servers |
| Profile banner color, traits ("particularités"), games played, private profile | 🚫 | server profile is not in the public API |
| Server tag | 🚫 | not in the public API |
| Preferred locale | ❌ | `preferred_locale` (community) |
| Boost progress bar | ❌ | `premium_progress_bar_enabled` |

## Engagement ("Participation")
| Discord setting | CODI | Notes |
|---|---|---|
| System messages channel | ✅ `profile.systemChannel` | |
| System message toggles (welcome message, sticker replies, boost message, setup tips) | ❌ | `system_channel_flags` |
| Activity feed | 🚫 | |
| Default notifications | ✅ `profile.defaultNotifications` | |
| Inactive (AFK) channel + timeout | ❌ | `afk_channel_id`, `afk_timeout` |
| Server widget (enabled + channel) | ❌ | `PATCH /guilds/{id}/widget` |

## Access & safety
| Discord setting | CODI | Notes |
|---|---|---|
| Join mode (invite only / apply / discovery) | 🚫 | join requests & discovery are not settable by bots |
| Age-restricted server | 🚫 | `nsfw_level` is read-only |
| Server rules (rules screening form) | 🚫 | member verification form not in the public API |
| Verification level | ✅ `profile.verificationLevel` | |
| 2FA required for moderation | 🚫 | `mfa_level` only by the owner |
| Explicit (sensitive) content filter | ❌ | `explicit_content_filter` |
| Safety alerts channel | ❌ | `safety_alerts_channel_id` (community) |
| Members in channel list | 🚫 | |

## Community
| Discord setting | CODI | Notes |
|---|---|---|
| Enable Community | ❌ | `features: [COMMUNITY]` + rules/public updates channels, verification ≥ low, explicit filter = all members |
| Rules channel | ✅ `profile.rulesChannel` | requires Community on real Discord |
| Community updates channel | ❌ | `public_updates_channel_id` |
| Welcome screen | ❌ | `PATCH /guilds/{id}/welcome-screen` |
| Onboarding (questions, default channels, mode) | ❌ | `PUT /guilds/{id}/onboarding` — complements the CODI onboarding plugin |
| Announcement channels | ✅ `type: announcement` | need Community (clear error since 78aea4a) |

## Roles
| Discord setting | CODI | Notes |
|---|---|---|
| Name, color, hoist, mentionable, permissions, hierarchy | ✅ | |
| Role style: gradient / holographic colors | ❌ | `colors {primary, secondary, tertiary}` (boost) |
| Role icon (image, boost level 2) | 🟡 | `roles.*.icon` declared but not applied (contract has no icon field) |
| Role icon emoji | ❌ | `unicode_emoji` |
| Linked roles (role connections) | ❌ | app-level metadata, later |
| @everyone permissions | ✅ `everyone.permissions` | |

## Channels & categories
| Discord setting | CODI | Notes |
|---|---|---|
| Name, type, category, position, topic, NSFW, slowmode, overwrites, sync with category | ✅ | |
| Voice bitrate, user limit | ✅ | |
| Voice region | ❌ | `rtc_region` |
| Video quality | ❌ | `video_quality_mode` |
| Hide after inactivity (thread auto-archive) | ❌ | `default_auto_archive_duration` |
| Thread slowmode default | ❌ | `default_thread_rate_limit_per_user` |
| Forum/media: tags, default reaction, sort order, layout, guidelines | ❌ | `available_tags`, `default_reaction_emoji`, `default_sort_order`, `default_forum_layout`, `topic` |
| Spoiler channel ("salon de spoiler") | ❌ | to verify in the API |
| Stage channels | ✅ `type: stage` | |
| Channel webhooks | ❌ | maybe later (secrets) |
| Invites | ❌ | permanent invite links could be declared |

## Expression
| Discord setting | CODI | Notes |
|---|---|---|
| Emojis | ❌ | `emojis: {key: {name, image}}` from repo files |
| Stickers | ❌ | `stickers` |
| Soundboard sounds | ❌ | `soundboard` |

## Moderation
| Discord setting | CODI | Notes |
|---|---|---|
| AutoMod rules (mention spam, spam, keyword presets, custom keywords, actions, exemptions) | ❌ | `/guilds/{id}/auto-moderation/rules` — high value |
| Bans | — | data, not configuration |
| Audit log | — | read-only |

## Other
| Discord setting | CODI | Notes |
|---|---|---|
| Scheduled events | — | runtime data (plugins), not configuration |
| Server template | 🚫 | |
| Boost perks | 🚫 | |

## Priorities
1. Community (enable + required channels + explicit filter), system message flags, AFK, widget,
   locale, progress bar, safety alerts channel.
2. AutoMod rules.
3. Discord native onboarding + welcome screen.
4. Role icons (image + emoji) and gradient colors; channel extras (region, video quality,
   archive duration, forum tags/settings).
5. Emojis, stickers, soundboard from repository assets.
6. Invites, webhooks.
