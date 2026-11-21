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
| Icon | ✅ `profile.icon` | upload from Server settings; animated icons need boost level 1 |
| Banner image (boost level 2) | ✅ `profile.banner` | boost-gated in the plan; upload from Server settings |
| Invite splash / discovery splash | ✅ `profile.splash` / 🚫 | invite splash (boost level 1); discovery splash needs Discovery (not settable) |
| Description | ✅ `profile.description` | shown for community/discovery servers |
| Profile banner color, traits ("particularités"), games played, private profile | 🚫 | server profile is not in the public API |
| Server tag | 🚫 | not in the public API |
| Preferred locale | ✅ `profile.preferredLocale` | |
| Boost progress bar | ✅ `profile.premiumProgressBar` | |

## Engagement ("Participation")
| Discord setting | CODI | Notes |
|---|---|---|
| System messages channel | ✅ `profile.systemChannel` | |
| System message toggles (welcome message, sticker replies, boost message, setup tips) | ✅ `profile.systemChannelFlags` | readable booleans (`welcomeMessages`, `welcomeStickerReplies`, `boostMessages`, `setupTips`, `roleSubscriptionMessages`, `roleSubscriptionStickerReplies`) |
| Activity feed | 🚫 | |
| Default notifications | ✅ `profile.defaultNotifications` | |
| Inactive (AFK) channel + timeout | ✅ `afk: {channel, timeout}` | |
| Server widget (enabled + channel) | ✅ `widget: {enabled, channel}` | |

## Access & safety
| Discord setting | CODI | Notes |
|---|---|---|
| Join mode (invite only / apply / discovery) | 🚫 | join requests & discovery are not settable by bots |
| Age-restricted server | 🚫 | `nsfw_level` is read-only |
| Server rules (rules screening form) | 🚫 | member verification form not in the public API |
| Verification level | ✅ `profile.verificationLevel` | |
| 2FA required for moderation | 🚫 | `mfa_level` only by the owner |
| Explicit (sensitive) content filter | ✅ `profile.explicitContentFilter` | |
| Safety alerts channel | ✅ `community.safetyAlertsChannel` | |
| Members in channel list | 🚫 | |

## Community
| Discord setting | CODI | Notes |
|---|---|---|
| Enable Community | ✅ `community.enabled` | prerequisites (rules/updates channels, verification ≥ low, filter = all members) checked by `codi validate` and blocked in the plan with the missing list |
| Rules channel | ✅ `community.rulesChannel` | `profile.rulesChannel` still accepted (deprecated alias); blocked without Community |
| Community updates channel | ✅ `community.updatesChannel` | |
| Welcome screen | ✅ `welcomeScreen` | |
| Onboarding (questions, default channels, mode) | ✅ `onboarding` | see "Native onboarding and the CODI onboarding plugin" below |
| Announcement channels | ✅ `type: announcement` | need Community: blocked in the plan, created right after Community is enabled |

## Roles
| Discord setting | CODI | Notes |
|---|---|---|
| Name, color, hoist, mentionable, permissions, hierarchy | ✅ | |
| Role style: gradient / holographic colors | ✅ `roles.*.colors` | boost level 2; `color` stays the solid shorthand |
| Role icon (image, boost level 2) | ✅ `roles.*.icon` | |
| Role icon emoji | ✅ `roles.*.emoji` | exclusive with `icon` |
| Linked roles (role connections) | ❌ | app-level metadata, later |
| @everyone permissions | ✅ `everyone.permissions` | |

## Channels & categories
| Discord setting | CODI | Notes |
|---|---|---|
| Name, type, category, position, topic, NSFW, slowmode, overwrites, sync with category | ✅ | |
| Voice bitrate, user limit | ✅ | |
| Voice region | ✅ `voiceRegion` | `automatic` or a region id |
| Video quality | ✅ `videoQuality` | `auto` / `full` |
| Hide after inactivity (thread auto-archive) | ✅ `threadArchiveAfter` | `1h` / `24h` / `3d` / `1w` |
| Thread slowmode default | ✅ `threadSlowmode` | |
| Forum/media: tags, default reaction, sort order, layout, guidelines | ✅ `tags` (keyed), `defaultReaction`, `sortOrder`, `layout`, `guidelines` | tag IDs kept on rename |
| Spoiler channel ("salon de spoiler") | ❌ | to verify in the API |
| Stage channels | ✅ `type: stage` | |
| Channel webhooks | ❌ | later (secrets) — TODO |
| Invites | ❌ | permanent invite links could be declared — TODO |

## Expression
| Discord setting | CODI | Notes |
|---|---|---|
| Emojis | ✅ `emojis: {key: {name, image, roles}}` | slots per boost level blocked in the plan; a changed file needs a new key |
| Stickers | ✅ `stickers: {key: {name, description, emoji, file}}` | |
| Soundboard sounds | ✅ `soundboard: {key: {name, sound, volume, emoji}}` | |

## Moderation
| Discord setting | CODI | Notes |
|---|---|---|
| AutoMod rules (mention spam, spam, keyword presets, custom keywords, actions, exemptions) | ✅ `automod: {key: {...}}` | keyed, adopted by name + trigger, rule caps blocked in the plan |
| Bans | — | data, not configuration |
| Audit log | — | read-only |

## Other
| Discord setting | CODI | Notes |
|---|---|---|
| Scheduled events | — | runtime data (plugins), not configuration |
| Server template | 🚫 | |
| Boost perks | 🚫 | |

## Live server status (read-only, shown in the admin UI and used by the planner)
| Information | CODI | Notes |
|---|---|---|
| Boost level and boost count | ✅ | `premium_tier`, `premium_subscription_count`: features locked by boost level (banner, role icons, gradient role colors, splash, upload limits…) must be **blocked in the plan with a clear reason** ("banner requires boost level 2, server is level 0") instead of failing on apply |
| Community enabled | ✅ | `features` contains `COMMUNITY` (also `DISCOVERABLE`, `WELCOME_SCREEN_ENABLED`, `ROLE_ICONS`…) |
| Icon / banner currently set | ✅ | `iconUrl`, `bannerUrl`, `splashUrl` in the status API; the admin switcher shows the icon |

## Example

```yaml
spec:
  profile:
    banner: ./assets/ma-commu-banner.png   # boost level 2
    explicitContentFilter: allMembers
    preferredLocale: fr
    premiumProgressBar: true
    systemChannel: general
    systemChannelFlags: {welcomeMessages: true, setupTips: false}
  community: {enabled: true, rulesChannel: rules, updatesChannel: mod-news}
  afk: {channel: lounge, timeout: 900}
  widget: {enabled: true, channel: general}
  roles:
    mod: {name: Mod, colors: {primary: "#5865f2", secondary: "#eb459e"}, emoji: "🛡️"}
  channels:
    help:
      name: help
      type: forum
      guidelines: Search before asking.
      tags: {question: {name: Question, emoji: "❓"}, solved: {name: Solved, moderated: true}}
      defaultReaction: "👍"
      sortOrder: creationDate
      layout: list
  automod:
    bad-words:
      name: Bad words
      trigger: keyword
      keywords: [darn, "heck*"]
      actions: {block: {message: "Language!"}, alert: mod-log, timeout: 10m}
      exemptRoles: [mod]
  welcomeScreen:
    description: Welcome!
    channels: [{channel: rules, description: Read the rules, emoji: "📜"}]
  onboarding:
    enabled: true
    defaultChannels: [rules, general, news, help, memes, events, lounge]
    prompts:
      - key: games
        title: What do you play?
        options: [{title: Minecraft, emoji: "⛏️", roles: [minecraft], channels: [minecraft]}]
  emojis: {party: {name: party, image: ./assets/party.png}}
  stickers: {wave: {name: Wave, emoji: wave, file: ./assets/wave.png}}
  soundboard: {horn: {name: Horn, sound: ./assets/horn.ogg, volume: 0.6}}
```

## Behaviour observed on real Discord

Verified on the test server (Community enabled by CODI, 2026-11-21):

- **Default mention spam rule.** A community server lists a "Block Mention Spam" rule
  created by Discord's AutoMod system user (`1008776202191634432`). It is a placeholder:
  `PATCH`/`DELETE` answer 404 and it does not count toward the caps. The adapter ignores
  it; creating a real `mentionSpam` rule replaces it.
- **Once-per-server triggers** (`mentionSpam`, `spam`, `keywordPreset`, `memberProfile`):
  an existing rule with that trigger is adopted whatever its name (and renamed), also when
  the mapped rule was deleted and recreated by hand.
- **The mention spam rule cannot be deleted on a community server** (403, code 200006):
  removing its key with prune is blocked in the plan (`undeletable`); set `enabled: false`.
- **Onboarding default channels must be open to @everyone** (`ViewChannel`, plus
  `Connect` for voice and stage; `DEFAULT_CHANNEL_REQUIRES_EVERYONE_ACCESS`), and 5 of the
  counted channels must let @everyone send messages. `codi validate` reports both when
  `spec.everyone` is declared.
- Boost gating (banner, role icon) is blocked in the plan at level 0, as designed.

## Native onboarding and the CODI onboarding plugin

They are complementary and can run together:
- **Discord onboarding** (`spec.onboarding`) runs inside the Discord client when a member
  joins a Community server: default channels and a few questions that grant roles and
  channels. It needs Community and at least 7 default channels (5 where @everyone can send
  messages) in default mode.
- **The CODI onboarding plugin** runs on the community portal (and with slash commands):
  richer steps, custom logic, KV state, links to the portal profile. It works on any server.

A typical setup keeps the essential interest roles in Discord onboarding (members see them
right away) and uses the plugin for everything else; both can grant the same role keys.

## Priorities
1. Live status (boost level/count, features incl. Community) in the contract, API and admin UI,
   with boost-gated fields blocked in plans; icon + banner (cover) declarable and editable from
   the admin UI. Then Community (enable + required channels + explicit filter), system message
   flags, AFK, widget, locale, progress bar, safety alerts channel.
2. AutoMod rules.
3. Discord native onboarding + welcome screen.
4. Role icons (image + emoji) and gradient colors; channel extras (region, video quality,
   archive duration, forum tags/settings).
5. Emojis, stickers, soundboard from repository assets.
6. Invites, webhooks.
