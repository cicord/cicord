---
title: Server
description: Reference of servers/<key>.yaml - profile, roles, categories, channels, permissions, Community, AutoMod, onboarding, welcome screen, emojis, stickers, soundboard, access and portal.
---

A **Server** file, `servers/<key>.yaml`, is the desired state of one Discord server. Schema:
[`spec/schemas/server.schema.json`](https://github.com/cicord/cicord/blob/main/spec/schemas/server.schema.json).
How it is applied is described in [Reconciliation](../../concepts/reconciliation/).

```yaml title="servers/ma-commu.yaml"
apiVersion: cicord.dev/v1alpha1
kind: Server
metadata:
  name: ma-commu
spec:
  discordId: "112233445566778899"
  profile:
    name: Ma Commu
    verificationLevel: medium
    explicitContentFilter: allMembers
    systemChannel: general
  syncPolicy: { selfHeal: false, prune: false, deletionProtection: true }
  everyone:
    permissions: [ViewChannel, ReadMessageHistory, AddReactions, UseApplicationCommands]
  roles:
    admin: { name: Admin, color: "#e74c3c", hoist: true, permissions: [Administrator] }
    modo: { name: Modo, color: "#e67e22", hoist: true, permissions: [KickMembers, ModerateMembers, ManageMessages] }
    member: { name: Membre, color: "#3498db", permissions: [SendMessages, EmbedLinks, AttachFiles, Connect, Speak] }
  hierarchy: [admin, modo, member]
  categories:
    community: { name: Communauté, position: 1 }
    staff:
      name: Staff
      position: 2
      overwrites:
        "@everyone": { deny: [ViewChannel] }
        modo: { allow: [ViewChannel] }
  channels:
    general: { name: général, type: text, category: community, topic: Discussions générales }
    voice: { name: Vocal, type: voice, category: community, userLimit: 10 }
    staff-chat: { name: staff, type: text, category: staff }
  access:
    admins: { roles: [admin] }
    moderators: { roles: [modo] }
```

Keys (`admin`, `general`…) match `^[a-z][a-z0-9-]{0,62}$` and never change; display names
(`name:`) can. All references below are **keys** of the same server.

## Top level

| Field | Type | Description |
|---|---|---|
| `discordId` | Discord ID, **required** | The server's ID (17 to 20 digits, as a string). |
| `profile` | object | Name, icon, banner, verification, notifications, system channel… |
| `syncPolicy` | object | Drift, pruning and deletion protection. |
| `everyone` | `{ permissions }` | Permissions of the `@everyone` role. Managed only when present. |
| `roles` | map of key → role | Roles. |
| `hierarchy` | list of role keys | Role order, highest first. Managed roles not listed follow, in key order. |
| `categories` | map of key → category | Categories. |
| `channels` | map of key → channel | Channels. |
| `afk` | object | Inactive voice channel. |
| `widget` | object | Server widget. |
| `community` | object | The Community feature and its channels. |
| `automod` | map of key → rule | AutoMod rules. |
| `onboarding` | object | Discord's native onboarding. |
| `welcomeScreen` | object | Welcome screen of Community servers. |
| `emojis`, `stickers`, `soundboard` | maps | Expressions, from files in the repository. |
| `access` | object | Who can use the admin console for this server. |
| `portal` | object | The server's community portal. |

## References

Every reference is a **key**, checked by `cicord validate`:

| Field | Refers to |
|---|---|
| `channels.<key>.category` | `categories.<key>` of the same server |
| `profile.systemChannel`, `profile.rulesChannel` (deprecated), `community.*Channel`, `afk.channel`, `widget.channel`, `automod.*.actions.alert`, `automod.*.exemptChannels[]`, `onboarding.defaultChannels[]`, `onboarding.prompts[].options[].channels[]`, `welcomeScreen.channels[].channel` | `channels.<key>` |
| `hierarchy[]`, `access.*.roles[]`, `automod.*.exemptRoles[]`, `onboarding.prompts[].options[].roles[]`, `emojis.*.roles[]` | `roles.<key>` |
| [overwrite](#permission-overwrites) targets | `@everyone`, `@bot`, `roles.<key>`, or `member:<discord id>` |
| a bot's `spec.servers[]`, `plugins[].servers[]`, `plugins[].overrides.<key>` | `servers/<key>.yaml` |
| plugin config fields with `x-cicord-ref` | a key of the given kind on **every** server the instance runs on |

## `syncPolicy`

| Field | Default | Effect |
|---|---|---|
| `selfHeal` | `false` | `true`: revert manual changes made in Discord instead of only reporting drift. |
| `prune` | `false` | Delete resources that were managed and have been removed from the YAML. |
| `deletionProtection` | `true` | Refuse every deletion, even with `prune: true`. |

## `profile`

Each field is managed only when set.

| Field | Type | Description |
|---|---|---|
| `name` | string (2–100) | Server name. |
| `description` | string (≤ 120) | Shown for Community and Discovery servers. |
| `icon` | relative path | Server icon. An animated (GIF) icon needs boost level 1. |
| `banner` | relative path | Banner image (png, jpg, gif, webp). Needs boost level 2. |
| `splash` | relative path | Invite background (png, jpg, webp). Needs boost level 1. |
| `verificationLevel` | `none` \| `low` \| `medium` \| `high` \| `highest` | |
| `defaultNotifications` | `allMessages` \| `onlyMentions` | |
| `explicitContentFilter` | `disabled` \| `membersWithoutRoles` \| `allMembers` | Sensitive media scanning. Community servers require `allMembers`. |
| `preferredLocale` | Discord locale (`fr`, `en-US`, `es-419`…) | Community language. |
| `premiumProgressBar` | boolean | Show the boost progress bar. |
| `systemChannel` | channel key | Where system messages are sent. |
| `systemChannelFlags` | object of booleans | `welcomeMessages`, `welcomeStickerReplies`, `boostMessages`, `setupTips`, `roleSubscriptionMessages`, `roleSubscriptionStickerReplies` (`true` = sent). Unset toggles keep their live value. |
| `rulesChannel` | channel key | **Deprecated**: use `community.rulesChannel`. |

Image paths are relative to the server file. They are re-uploaded when the file's hash or the
live image changes. Admins can also upload the icon, banner and splash from the admin
console's server settings, which commits the file.

## Roles

```yaml
roles:
  mod:
    name: Mod
    colors: { primary: "#5865f2", secondary: "#eb459e" }   # gradient (boost level 2)
    emoji: "🛡️"                                            # role icon (boost level 2)
    hoist: true
    mentionable: false
    permissions: [KickMembers, ModerateMembers, ManageMessages]
```

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string (1–100), **required** | | Display name. |
| `color` | `#rrggbb` | none | Solid color (shorthand of `colors.primary`). |
| `colors` | `{ primary, secondary?, tertiary? }` | | Gradient role style; `secondary` needs boost level 2. |
| `hoist` | boolean | `false` | Display members separately. |
| `mentionable` | boolean | `false` | |
| `icon` | relative path | | Role icon image (png, jpg, webp, ≤ 256 KiB). Boost level 2. |
| `emoji` | string | | Unicode emoji role icon. Boost level 2. Exclusive with `icon`. |
| `permissions` | list of permissions | `[]` | See [permission names](#permission-names). |

Name, color, hoist, mentionable and permissions are always managed: the YAML value is applied
even when it is the zero value. A role without `colors` is solid: a live gradient is drift.

## Categories

| Field | Type | Description |
|---|---|---|
| `name` | string (1–100), **required** | |
| `position` | integer ≥ 0 | Order among categories (ascending; missing positions after explicit ones; ties by key). |
| `overwrites` | [overwrites](#permission-overwrites) | |

## Channels

```yaml
channels:
  help:
    name: help
    type: forum
    category: community
    guidelines: Search before asking.
    tags:
      question: { name: Question, emoji: "❓" }
      solved: { name: Solved, moderated: true }
    defaultReaction: "👍"
    sortOrder: creationDate
    layout: list
```

| Field | Type | Applies to | Description |
|---|---|---|---|
| `name` | string (1–100), **required** | all | Text-like names are compared after Discord's normalization. |
| `type` | `text` \| `voice` \| `announcement` \| `stage` \| `forum` \| `media`, **required** | | Cannot change in place (see [blocked changes](../../concepts/reconciliation/#blocked-changes-and-their-explanations)). Announcement and stage channels need Community. |
| `category` | category key | all | |
| `position` | integer ≥ 0 | all | Order inside the category. |
| `topic` | string (≤ 1024) | text, announcement, forum, media | |
| `nsfw` | boolean (`false`) | all | |
| `slowmode` | seconds (0–21600) | text, voice, stage, forum, media | Seconds between messages per member. |
| `bitrate` | integer ≥ 8000 | voice, stage | Managed when > 0. |
| `userLimit` | 0–99 | voice, stage | |
| `syncWithCategory` | boolean | all | Inherit the category's overwrites. Defaults to `true` when the channel has no `overwrites`. |
| `overwrites` | [overwrites](#permission-overwrites) | all | |
| `voiceRegion` | `automatic` or a region id | voice, stage | E.g. `rotterdam`, `us-east`. |
| `videoQuality` | `auto` \| `full` | voice, stage | Camera quality. |
| `threadArchiveAfter` | `1h` \| `24h` \| `3d` \| `1w` | channels with threads | Default "hide after inactivity" of new threads. |
| `threadSlowmode` | seconds (0–21600) | channels with threads | Default slowmode of new threads. |
| `tags` | map of key → `{ name, emoji?, moderated? }` (≤ 20) | forum, media | Post tags. Live tag IDs are kept on rename. |
| `defaultReaction` | emoji | forum, media | A unicode emoji, or a custom emoji as `name:id`. |
| `sortOrder` | `latestActivity` \| `creationDate` | forum, media | |
| `layout` | `list` \| `gallery` | forum | |
| `guidelines` | string (≤ 4096) | forum, media | Post guidelines (alias of `topic`). |

Threads are ignored by the reconciler.

## Permission overwrites

`overwrites` on a category or channel are keyed by **target**: `@everyone`, `@bot`, a role
key, or `member:<discord id>`. Each target has `allow` and `deny` lists; a permission in
neither is inherited.

```yaml
overwrites:
  "@everyone": { deny: [ViewChannel] }
  modo: { allow: [ViewChannel, ManageMessages] }
  "@bot": { allow: [ViewChannel] }
  "member:112233445566778899": { allow: [ViewChannel] }
```

`@bot` is the managing bot's own role, the managed role Discord created when the bot was
invited. A bot that is not Administrator needs it in every private category or channel, or
it locks itself out. See [bot permissions](../../self-hosting/bot-permissions/#private-channels-the-bot-target).

Overwrites of a managed category or channel are owned as a whole: targets that are not in the
YAML are removed. A channel with `syncWithCategory` gets exactly its category's overwrites.

### Permission names

Cicord names permissions in PascalCase, saying **server** where Discord says guild:
`CreateInstantInvite`, `KickMembers`, `BanMembers`, `Administrator`, `ManageChannels`,
`ManageServer`, `AddReactions`, `ViewAuditLog`, `PrioritySpeaker`, `Stream`, `ViewChannel`,
`SendMessages`, `SendTTSMessages`, `ManageMessages`, `EmbedLinks`, `AttachFiles`,
`ReadMessageHistory`, `MentionEveryone`, `UseExternalEmojis`, `ViewServerInsights`,
`Connect`, `Speak`, `MuteMembers`, `DeafenMembers`, `MoveMembers`, `UseVAD`,
`ChangeNickname`, `ManageNicknames`, `ManageRoles`, `ManageWebhooks`, `ManageExpressions`,
`UseApplicationCommands`, `RequestToSpeak`, `ManageEvents`, `ManageThreads`,
`CreatePublicThreads`, `CreatePrivateThreads`, `UseExternalStickers`,
`SendMessagesInThreads`, `UseEmbeddedActivities`, `ModerateMembers`,
`ViewCreatorMonetizationAnalytics`, `UseSoundboard`, `CreateExpressions`, `CreateEvents`,
`UseExternalSounds`, `SendVoiceMessages`, `SendPolls`, `UseExternalApps`.

The names that differ from Discord's API are `ManageServer` (`MANAGE_GUILD`),
`ViewServerInsights` (`VIEW_GUILD_INSIGHTS`), `ManageExpressions`
(`MANAGE_GUILD_EXPRESSIONS`), `CreateExpressions` (`CREATE_GUILD_EXPRESSIONS`),
`SendTTSMessages` and `UseVAD`; the others map 1:1 to SCREAMING_SNAKE_CASE.

## `afk` and `widget`

| Field | Type | Description |
|---|---|---|
| `afk.channel` | channel key | Voice channel inactive members are moved to. |
| `afk.timeout` | `60` \| `300` \| `900` \| `1800` \| `3600` | Seconds of inactivity. |
| `widget.enabled` | boolean, **required** | Server widget (Server Settings → Widget). |
| `widget.channel` | channel key | Invite channel of the widget. |

## `community`

```yaml
profile:
  verificationLevel: low          # at least low
  explicitContentFilter: allMembers
community:
  enabled: true
  rulesChannel: rules
  updatesChannel: mod-news
  safetyAlertsChannel: mod-news
```

| Field | Type | Description |
|---|---|---|
| `enabled` | boolean, **required** | Toggles the `COMMUNITY` feature. |
| `rulesChannel` | text channel key | Required to enable Community. |
| `updatesChannel` | text channel key | Where Discord posts moderator news. Required to enable Community. |
| `safetyAlertsChannel` | text channel key | Receives safety alerts (raids, spam). |

Enabling Community also needs `profile.verificationLevel` at least `low` and
`profile.explicitContentFilter: allMembers`, and text channels for the rules and updates
(created by the same plan if needed). `cicord validate` reports a missing prerequisite and
the plan blocks the change with the full list. The toggle, the channels and the verification
and filter changes are sent in one request, once the channels exist; channels that need
Community are created right after it is enabled, in the same sync. Disabling Community sends
the live mutable features without `COMMUNITY`.

`profile.rulesChannel` is a deprecated alias of `community.rulesChannel`: validation warns,
and setting both to different keys is an error.

## `automod`

```yaml
automod:
  bad-words:
    name: Bad words
    trigger: keyword
    keywords: [darn, "heck*"]
    actions: { block: { message: "Language!" }, alert: mod-log, timeout: 10m }
    exemptRoles: [mod]
```

| Field | Type | Description |
|---|---|---|
| `name` | string (1–100), **required** | |
| `trigger` | `keyword` \| `keywordPreset` \| `mentionSpam` \| `spam` \| `memberProfile`, **required** | Up to 6 `keyword` rules, 1 of each other trigger. Cannot change in place: use a new key. |
| `enabled` | boolean (`true`) | |
| `keywords` | list (≤ 1000, each ≤ 60) | `keyword`, `memberProfile`: words or phrases (`*` wildcards). |
| `regex` | list (≤ 10, each ≤ 260) | `keyword`, `memberProfile`: Rust-flavoured regular expressions. |
| `allowList` | list (≤ 1000) | Exceptions (`keyword`, `keywordPreset`, `memberProfile`). |
| `presets` | list of `profanity` \| `sexualContent` \| `slurs` | `keywordPreset`. |
| `mentionLimit` | 1–50 | `mentionSpam`: unique role and user mentions allowed per message. |
| `mentionRaidProtection` | boolean | `mentionSpam`: also detect mention raids. |
| `actions.block.message` | string (≤ 150) | Block the message (for `memberProfile`: block the member's interactions); the text is shown to the member. |
| `actions.alert` | channel key | Channel that receives an alert. |
| `actions.timeout` | duration (`60s`, `10m`, `1h`, `1d`, `1w`, ≤ 4 weeks) | Time the member out (`keyword`, `mentionSpam`). |
| `exemptRoles` | role keys (≤ 20) | |
| `exemptChannels` | channel keys (≤ 50) | |

Rules are keyed like roles and adopted by name and trigger. A once-per-server trigger
(`mentionSpam`, `spam`, `keywordPreset`, `memberProfile`) adopts the existing rule whatever
its name. On a Community server the mention spam rule cannot be deleted: set
`enabled: false` instead.

## `onboarding` (native)

Discord's own onboarding, shown in the Discord client to new members of a Community server.
It complements the Cicord [onboarding plugin](../../plugins/official/#onboarding), which runs on
the portal.

```yaml
onboarding:
  enabled: true
  defaultChannels: [rules, general, news, help, memes, events, lounge]
  prompts:
    - key: games
      title: What do you play?
      options:
        - { title: Minecraft, emoji: "⛏️", roles: [minecraft], channels: [minecraft] }
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | boolean, **required** | | |
| `mode` | `default` \| `advanced` | `default` | `default`: only default channels count toward Discord's requirements; `advanced`: questions count too. |
| `defaultChannels` | channel keys | | Channels every new member sees. In default mode Discord needs at least 7, 5 of which let `@everyone` send messages, all open to `@everyone`. |
| `prompts[]` | list (≤ 15) | | Questions. |
| `prompts[].key` | key, **required** | | Stable key of the question (answers stay attached). |
| `prompts[].title` | string (1–100), **required** | | |
| `prompts[].type` | `multipleChoice` \| `dropdown` | `multipleChoice` | |
| `prompts[].singleSelect` | boolean | `false` | |
| `prompts[].required` | boolean | `false` | |
| `prompts[].inOnboarding` | boolean | `true` | `false`: only in Channels & Roles. |
| `prompts[].options[]` | list (1–50), **required** | | `title` (required, ≤ 50), `description` (≤ 100), `emoji`, `roles` (role keys granted), `channels` (channel keys shown). |

## `welcomeScreen`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | boolean | `true` | |
| `description` | string (≤ 140) | | |
| `channels[]` | list (≤ 5) | | `channel` (key, required), `description` (1–42, required), `emoji`. |

The welcome screen and onboarding are compared as a whole. Both need Community.

## Emojis, stickers and soundboard

Expressions are declared from files of the repository and keyed like roles.

```yaml
emojis:
  party: { name: party, image: ./assets/party.png, roles: [member] }
stickers:
  wave: { name: Wave, emoji: wave, file: ./assets/wave.png }
soundboard:
  horn: { name: Horn, sound: ./assets/horn.ogg, volume: 0.6 }
```

| Kind | Fields |
|---|---|
| `emojis.<key>` | `name` (required, `[A-Za-z0-9_]{2,32}`), `image` (required: png, jpg, gif or webp, ≤ 256 KiB), `roles` (only these role keys can use it). |
| `stickers.<key>` | `name` (required, 2–30), `description` (≤ 100), `emoji` (required: related emoji name for suggestions), `file` (required: png, apng, gif or Lottie json, ≤ 512 KiB). |
| `soundboard.<key>` | `name` (required, 2–32), `sound` (required: mp3 or ogg, ≤ 512 KiB, 5.2 s), `volume` (0–1, default `1`), `emoji`. |

Slots depend on the boost level (emojis 50/100/150/250 static plus as many animated,
stickers 5/15/30/60, sounds 8/24/36/48); extra items are blocked in the plan. A changed file
cannot be updated in place: declare a new key. `cicord import` downloads existing expressions
into `servers/assets/<server>/`.

## Boost-gated settings

The planner reads the live boost level and features. Instead of failing on apply, settings
Discord would refuse are **blocked in the plan with a reason**:

| Setting | Needs | Discord feature |
|---|---|---|
| `profile.banner` | Boost level 2 (animated: level 3) | `BANNER` |
| `profile.splash` | Boost level 1 | `INVITE_SPLASH` |
| animated `profile.icon` | Boost level 1 | `ANIMATED_ICON` |
| `roles.*.icon`, `roles.*.emoji` | Boost level 2 | `ROLE_ICONS` |
| `roles.*.colors.secondary` (gradient) | Boost level 2 (the primary color is still applied) | `ENHANCED_ROLE_COLORS` |
| Announcement and stage channels, Community channels, welcome screen, native onboarding | Community (live, or enabled by the same plan) | `COMMUNITY` |
| Emojis, stickers, sounds beyond the slots | A higher boost level | |

A feature flag is enough (partnered or verified servers get some without boosts). Blocked
items are applied by the first sync after the condition is met.

## `access`

| Field | Type | Description |
|---|---|---|
| `admins.roles`, `admins.users` | role keys, Discord user IDs | Admins of this server in the admin console. The owner is always admin. |
| `moderators.roles`, `moderators.users` | role keys, Discord user IDs | Moderators. |

See [Access and roles](../../concepts/access-and-roles/).

## `portal`

Declaring `portal` publishes the server on the community portal.

| Field | Type | Default | Description |
|---|---|---|---|
| `theme.primary` | `#rrggbb` | | Buttons, links, focus. |
| `theme.accent` | `#rrggbb` | | Highlights. |
| `theme.logo` | relative path | | png, jpg, gif, webp, avif or svg, inside the repository. |
| `theme.mode` | `light` \| `dark` \| `system` | `system` | |
| `title` | string (1–100) | server name | Home page headline. |
| `tagline` | string (≤ 200) | | One sentence under the title. |
| `description` | string (≤ 4000) | | Markdown subset. Raw HTML is shown as text. |
| `banner` | relative path | | Wide header image, same rules as the logo. |
| `links[]` | list (≤ 12) | | `label` (1–60), `url` (http(s), ≤ 500), `icon` (default `link`). |
| `pages` | `{ onboarding, profile, leaderboard, tickets }` booleans | all `true` | Pages of the default portal. |
| `locale` | `en` \| `fr` | browser language | Interface language of the default portal. |

Details and examples: [Customize from YAML](../../portal/customize/).

## Annotations for the simulator

With `cicord dev --sim`, the server annotations `sim.cicord.dev/boosts: "<n>"` and
`sim.cicord.dev/features: "A,B"` seed the simulated boost count and feature flags:

```yaml
metadata:
  name: ma-commu
  annotations:
    sim.cicord.dev/boosts: "7"
```

## Validation

Beyond the [JSON Schema](https://github.com/cicord/cicord/blob/main/spec/schemas/server.schema.json),
`cicord validate` fails when `metadata.name` does not match the file name or is duplicated,
when a [reference](#references) does not resolve, or when enabled Community lacks a rules or
updates channel, has a verification level below `low` or a content filter other than
`allMembers`. It warns when Community is enabled without setting those two profile fields,
when a setting needs Community but `community.enabled` is not `true`, when
`profile.rulesChannel` is used, and when `prune: true` is combined with
`deletionProtection: false`.
