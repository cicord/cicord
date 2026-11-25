---
title: Server
description: Reference of servers/<key>.yaml - profile, roles, categories, channels, permissions, Community, AutoMod, onboarding, welcome screen, emojis, stickers, soundboard, access, portal and analytics.
---

A **Server** file, `servers/<key>.yaml`, is the desired state of one Discord server. Schema:
[`spec/schemas/server.schema.json`](https://github.com/cicord/cicord/blob/main/spec/schemas/server.schema.json).
How it is applied: [Reconciliation](../../concepts/reconciliation/). Complete files:
[`examples/basic`](https://github.com/cicord/cicord/blob/main/examples/basic/servers/my-community.yaml),
[`examples/showcase`](https://github.com/cicord/cicord/blob/main/examples/showcase/servers/showcase.yaml)
and the [community server](https://github.com/cicord/cicord-community/blob/main/servers/cicord.yaml),
which the examples below come from.

```yaml title="servers/my-community.yaml (examples/basic, shortened)"
apiVersion: cicord.dev/v1alpha1
kind: Server
metadata:
  name: my-community
spec:
  discordId: "112233445566778899"
  profile:
    name: My Community
    verificationLevel: medium
    explicitContentFilter: allMembers
    systemChannel: general
  syncPolicy: { selfHeal: false, prune: false, deletionProtection: true }
  everyone:
    permissions: [ViewChannel, ReadMessageHistory, AddReactions, UseApplicationCommands]
  roles:
    admin: { name: Admin, color: "#e74c3c", hoist: true, permissions: [Administrator] }
    moderator: { name: Moderator, color: "#e67e22", hoist: true, permissions: [KickMembers, ModerateMembers, ManageMessages, ManageThreads] }
    member: { name: Member, color: "#3498db", permissions: [SendMessages, EmbedLinks, AttachFiles, Connect, Speak] }
  hierarchy: [admin, moderator, member]
  categories:
    community: { name: Community, position: 1 }
    staff:
      name: Staff
      position: 2
      overwrites:
        "@everyone": { deny: [ViewChannel] }
        moderator: { allow: [ViewChannel] }
  channels:
    general: { name: general, type: text, category: community, topic: General chat, position: 0 }
    voice: { name: Voice, type: voice, category: community, userLimit: 10, position: 1 }
    staff-chat: { name: staff, type: text, category: staff }
  access:
    admins: { roles: [admin] }
    moderators: { roles: [moderator] }
```

Keys (`admin`, `general`…) match `^[a-z][a-z0-9-]{0,62}$` and never change; display names
(`name:`) can. Every reference is a **key** of the same server. Each block is managed only
when present: what is compared is listed in
[Reconciliation](../../concepts/reconciliation/#what-is-compared).

## Top level

| Field                              | Description                                                                   |
| ---------------------------------- | ----------------------------------------------------------------------------- |
| `discordId`                        | **Required.** The server's ID (17 to 20 digits, as a string).                 |
| [`profile`](#profile)              | Name, images, verification, notifications, system channel.                     |
| [`syncPolicy`](#syncpolicy)        | Self-heal, pruning and deletion protection.                                   |
| `everyone`                         | `{ permissions }` of the `@everyone` role.                                    |
| [`roles`](#roles)                  | Map of key → role.                                                            |
| `hierarchy`                        | Role keys, highest first. Declared roles not listed follow, in key order.     |
| [`categories`](#categories)        | Map of key → category.                                                        |
| [`channels`](#channels)            | Map of key → channel.                                                         |
| [`afk`, `widget`](#afk-and-widget) | Inactive voice channel, server widget.                                        |
| [`community`](#community)          | The Community feature and its channels.                                       |
| [`automod`](#automod)              | Map of key → AutoMod rule.                                                    |
| [`onboarding`](#onboarding-native) | Discord's native onboarding.                                                  |
| [`welcomeScreen`](#welcomescreen)  | Welcome screen of Community servers.                                          |
| [`emojis`, `stickers`, `soundboard`](#emojis-stickers-and-soundboard) | Expressions, from files in the repository. |
| [`access`](#access)                | Admins and moderators of this server in the admin console.                    |
| [`portal`](#portal)                | The server's page on the community portal.                                    |
| [`analytics`](#analytics)          | Activity analytics (on when absent).                                          |

## References

`cicord validate` checks every reference:

| Field                                                                                                                                                  | Refers to                                                    |
| ------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------ |
| `channels.*.category`                                                                                                                                  | a category                                                   |
| `profile.systemChannel`, `community.rulesChannel`, `community.updatesChannel`, `community.safetyAlertsChannel`                                         | a text channel                                               |
| `afk.channel`                                                                                                                                          | a voice channel                                              |
| `automod.*.actions.alert`                                                                                                                              | a text or announcement channel                               |
| `widget.channel`, `automod.*.exemptChannels[]`, `onboarding.defaultChannels[]`, `onboarding.prompts[].options[].channels[]`, `welcomeScreen.channels[].channel` | a channel                                                    |
| `hierarchy[]`, `access.*.roles[]`, `automod.*.exemptRoles[]`, `onboarding.prompts[].options[].roles[]`, `emojis.*.roles[]`                             | a role                                                       |
| [overwrite](#permission-overwrites) targets                                                                                                            | `@everyone`, `@bot`, a role, or `member:<discord id>`        |
| a bot's `spec.servers[]`, `plugins[].servers[]`, `plugins[].overrides.<key>`                                                                           | `servers/<key>.yaml`                                         |
| plugin config fields with `x-cicord-ref`                                                                                                               | a key of the given kind on **every** server the instance runs on |

## `syncPolicy`

| Field                | Default | Effect                                                                                                    |
| -------------------- | ------- | --------------------------------------------------------------------------------------------------------- |
| `selfHeal`           | `false` | Drift (a change made in Discord): `true` reverts it within seconds and at the periodic sync (every 5 minutes); `false` only reports it, the server stays `OutOfSync` until the next revision or **Sync now**. Either way a new revision and **Sync now** apply the whole configuration. |
| `prune`              | `false` | Delete resources that were managed and have been removed from the YAML (otherwise they are unmanaged).   |
| `deletionProtection` | `true`  | Refuse every deletion, even with `prune: true`.                                                           |

To keep a manual change during an emergency (a channel locked during a raid), pause
enforcement on the server instead: nothing is applied until it is resumed. See
[Drift and self-heal](../../concepts/reconciliation/#drift-and-self-heal),
[Emergency: pause enforcement](../../concepts/reconciliation/#emergency-pause-enforcement) and
[Removed keys](../../concepts/reconciliation/#removed-keys-prune-and-deletion-protection).

## `profile`

```yaml title="servers/cicord.yaml (community server)"
profile:
  name: Cicord
  icon: ../assets/icon.png
  banner: ../assets/banner.png # boost level 2: waits in the plan until the server reaches it
  description: The community of Cicord, GitOps for Discord.
  verificationLevel: medium
  explicitContentFilter: allMembers
  defaultNotifications: onlyMentions
  preferredLocale: en-US
  systemChannel: general
  systemChannelFlags:
    welcomeMessages: false # the welcome plugin greets new members
    boostMessages: true
    setupTips: false
```

| Field                   | Type                                                  | Description                                                                                                                                                             |
| ----------------------- | ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`                  | string (2–100)                                        | Server name.                                                                                                                                                            |
| `description`           | string (≤ 120)                                        | Shown for Community and Discovery servers.                                                                                                                              |
| `icon`                  | relative path (≤ 10 MiB)                              | Server icon. An animated (GIF) icon needs boost level 1.                                                                                                                |
| `banner`                | relative path (≤ 10 MiB)                              | Banner image (png, jpg, gif, webp). Boost level 2; animated: level 3.                                                                                                  |
| `splash`                | relative path (≤ 10 MiB)                              | Invite background (png, jpg, webp). Boost level 1.                                                                                                                     |
| `verificationLevel`     | `none` \| `low` \| `medium` \| `high` \| `highest`    |                                                                                                                                                                         |
| `defaultNotifications`  | `allMessages` \| `onlyMentions`                       |                                                                                                                                                                         |
| `explicitContentFilter` | `disabled` \| `membersWithoutRoles` \| `allMembers`   | Sensitive media scanning. Community servers require `allMembers`.                                                                                                       |
| `preferredLocale`       | Discord locale (`fr`, `en-US`, `es-419`…)             | Community language.                                                                                                                                                     |
| `premiumProgressBar`    | boolean                                               | Show the boost progress bar.                                                                                                                                            |
| `systemChannel`         | text channel key                                      | Where system messages are sent.                                                                                                                                         |
| `systemChannelFlags`    | booleans (`true` = sent)                              | `welcomeMessages`, `welcomeStickerReplies`, `boostMessages`, `setupTips`, `roleSubscriptionMessages`, `roleSubscriptionStickerReplies`. Unset toggles keep their live value. |
| `rulesChannel`          | channel key                                           | **Deprecated** alias of `community.rulesChannel`.                                                                                                                       |

Each field is managed only when set. Image paths are relative to the server file and are
uploaded again when the file or the live image changes. Admins can also upload the icon,
banner and splash from the admin console's server settings, which commits the file.

## Roles

```yaml title="servers/cicord.yaml (community server)"
roles:
  moderator:
    name: Moderator
    color: "#eb459e"
    hoist: true
    permissions: [KickMembers, BanMembers, ModerateMembers, ManageMessages, ManageThreads,
                  ManageNicknames, ViewAuditLog, MuteMembers, DeafenMembers, MoveMembers]
  changelog-ping:
    name: Changelog ping
    mentionable: false
```

| Field         | Type                                 | Default | Description                                                                           |
| ------------- | ------------------------------------ | ------- | ------------------------------------------------------------------------------------- |
| `name`        | string (1–100), **required**         |         | Display name.                                                                         |
| `color`       | `#rrggbb`                            | none    | Solid color (shorthand of `colors.primary`).                                          |
| `colors`      | `{ primary, secondary?, tertiary? }` |         | Gradient role style. `secondary` (boost level 2) is needed for `tertiary`.            |
| `hoist`       | boolean                              | `false` | Display members separately.                                                           |
| `mentionable` | boolean                              | `false` |                                                                                       |
| `icon`        | relative path (≤ 256 KiB)            |         | Role icon image (png, jpg, webp). Boost level 2.                                      |
| `emoji`       | unicode emoji                        |         | Emoji role icon. Boost level 2. Exclusive with `icon`.                                |
| `permissions` | list of [permissions](#permission-names) | `[]` |                                                                                       |
| `members`     | list of Discord user IDs, unique     |         | Users who must hold the role: see [Role holders](#role-holders).                      |

Name, color, hoist, mentionable and permissions are always managed: the YAML value is applied
even when it is the zero value. A role without `colors` is solid, and setting both `color` and
`colors.primary` to different values is an error.

### Role holders

`members` gives a role to the users you list, so staff roles are reviewed in git:

```yaml
roles:
  maintainer:
    name: Maintainer
    permissions: [ManageServer, ManageRoles, ManageChannels, ManageMessages]
    members:
      - "112233445566778899" # Alice
      - "998877665544332211" # Bob, not in the server yet
```

- **Additive**: each listed user who is in the server and lacks the role gets it (plan:
  `give role "Maintainer" to @Alice (112233445566778899)`). Holders who are not listed keep it,
  and **removing a user from the list never removes the role**: take it away in Discord.
- A listed user who is not in the server is a plan **note**, not an error or drift. They get
  the role at the first sync after they join: right away with the bot's `ServerMembers`
  intent ([`spec.intents`](../bot/#gateway-intents)) and `syncPolicy.selfHeal: true`,
  otherwise at the next sync that applies (the periodic sync with `selfHeal: true`, else the
  next revision or **Sync now**). The same goes for a listed member whose role was taken away.
- The bot needs `ManageRoles` and can only give roles below its highest role; a role at or
  above it is blocked (`hierarchy:`).
- Each listed user costs one Discord API read per plan: list staff, not the whole community
  (self-assigned roles belong to the onboarding or a plugin).

## Categories

| Field        | Type                                  | Description                                                                              |
| ------------ | ------------------------------------- | ---------------------------------------------------------------------------------------- |
| `name`       | string (1–100), **required**          |                                                                                          |
| `position`   | integer ≥ 0                           | Order among categories (ascending; missing positions after explicit ones; ties by key). |
| `overwrites` | [overwrites](#permission-overwrites)  |                                                                                          |

A key cannot be both a category and a channel.

## Channels

```yaml title="servers/cicord.yaml (community server)"
channels:
  help:
    name: help
    type: forum
    category: support
    position: 0
    guidelines: One post per question. Search first, then tell us your Cicord version.
    tags:
      question: { name: Question, emoji: "❓" }
      bug: { name: Bug, emoji: "🐛" }
      solved: { name: Solved, emoji: "✅" }
    sortOrder: latestActivity
    layout: list
    threadArchiveAfter: 1w
```

| Field                | Type                                                                               | Applies to                       | Description                                                                                                         |
| -------------------- | ---------------------------------------------------------------------------------- | -------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `name`               | string (1–100), **required**                                                       | all                              | Text-like names are compared after Discord's normalization (lowercase, spaces become `-`).                          |
| `type`               | `text` \| `voice` \| `announcement` \| `stage` \| `forum` \| `media`, **required** | all                              | Cannot change in place (see [blocked changes](../../concepts/reconciliation/#blocked-changes-and-their-explanations)). Announcement and stage need Community. |
| `category`           | category key                                                                       | all                              |                                                                                                                     |
| `position`           | integer ≥ 0                                                                        | all                              | Order inside the category; text-like and voice-like channels are ordered separately.                                |
| `topic`              | string (≤ 1024)                                                                    | text, announcement, forum, media |                                                                                                                     |
| `nsfw`               | boolean (`false`)                                                                  | all                              |                                                                                                                     |
| `slowmode`           | seconds (0–21600)                                                                  | text, voice, stage, forum, media | Seconds between messages per member.                                                                                |
| `bitrate`            | integer ≥ 8000                                                                     | voice, stage                     | Managed when set.                                                                                                   |
| `userLimit`          | 0–99                                                                               | voice, stage                     |                                                                                                                     |
| `syncWithCategory`   | boolean                                                                            | all                              | Use the category's overwrites. Defaults to `true` when the channel has no `overwrites`; `true` needs a category and no `overwrites`. |
| `overwrites`         | [overwrites](#permission-overwrites)                                               | all                              |                                                                                                                     |
| `voiceRegion`        | `automatic` or a region id (`rotterdam`, `us-east`…)                                | voice, stage                     |                                                                                                                     |
| `videoQuality`       | `auto` \| `full`                                                                   | voice, stage                     | Camera quality.                                                                                                     |
| `threadArchiveAfter` | `1h` \| `24h` \| `3d` \| `1w`                                                      | text, announcement, forum, media | Default "hide after inactivity" of new threads.                                                                     |
| `threadSlowmode`     | seconds (0–21600)                                                                  | text, announcement, forum, media | Default slowmode of new threads.                                                                                    |
| `tags`               | map of key → `{ name (≤ 20), emoji?, moderated? }` (≤ 20 tags)                     | forum, media                     | Post tags, with unique names. Live tag IDs are kept on rename.                                                      |
| `defaultReaction`    | emoji                                                                              | forum, media                     | A unicode emoji, or a custom emoji as `name:id`.                                                                    |
| `sortOrder`          | `latestActivity` \| `creationDate`                                                 | forum, media                     |                                                                                                                     |
| `layout`             | `list` \| `gallery`                                                                | forum                            |                                                                                                                     |
| `guidelines`         | string (≤ 4096)                                                                    | forum, media                     | Post guidelines: an alias of `topic` (set one or the other).                                                        |

Setting a field on a channel type it does not apply to is a validation error. Threads are
ignored by the reconciler.

## Permission overwrites

`overwrites` on a category or channel are keyed by **target**: `@everyone`, `@bot`, a role
key, or `member:<discord id>`. Each target has `allow` and `deny` lists; a permission in
neither is inherited.

```yaml title="servers/cicord.yaml (community server)"
categories:
  team:
    name: Staff
    position: 5
    overwrites:
      "@everyone": { deny: [ViewChannel] }
      maintainer: { allow: [ViewChannel, SendMessages, EmbedLinks, AttachFiles] }
      moderator: { allow: [ViewChannel, SendMessages, EmbedLinks, AttachFiles] }
      "@bot": { allow: [ViewChannel, SendMessages, EmbedLinks, ReadMessageHistory] }
```

`@bot` is the managing bot's own role, the managed role Discord created when the bot was
invited. A bot that is not Administrator needs it in every private category or channel, or
it locks itself out: see [bot permissions](../../self-hosting/bot-permissions/#private-channels-the-bot-target).
Overwrites are owned as a whole: targets that are not in the YAML are removed.

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

They map 1:1 to Discord's SCREAMING_SNAKE_CASE names, except `ManageServer`
(`MANAGE_GUILD`), `ViewServerInsights` (`VIEW_GUILD_INSIGHTS`), `ManageExpressions`
(`MANAGE_GUILD_EXPRESSIONS`), `CreateExpressions` (`CREATE_GUILD_EXPRESSIONS`),
`SendTTSMessages` (`SEND_TTS_MESSAGES`) and `UseVAD` (`USE_VAD`).

## `afk` and `widget`

| Field            | Type                                      | Description                                 |
| ---------------- | ----------------------------------------- | ------------------------------------------- |
| `afk.channel`    | voice channel key                         | Where inactive members are moved.           |
| `afk.timeout`    | `60` \| `300` \| `900` \| `1800` \| `3600` | Seconds of inactivity.                      |
| `widget.enabled` | boolean, **required**                     | Server widget (Server Settings → Widget).   |
| `widget.channel` | channel key                               | Invite channel of the widget.               |

## `community`

```yaml title="servers/cicord.yaml (community server)"
profile:
  verificationLevel: medium # at least low
  explicitContentFilter: allMembers
community:
  enabled: true
  rulesChannel: rules
  updatesChannel: discord-updates
  safetyAlertsChannel: mod-log
```

| Field                 | Type                  | Description                                                     |
| --------------------- | --------------------- | --------------------------------------------------------------- |
| `enabled`             | boolean, **required** | Toggles the `COMMUNITY` feature.                                |
| `rulesChannel`        | text channel key      | Required to enable Community.                                   |
| `updatesChannel`      | text channel key      | Where Discord posts moderator news. Required to enable Community. |
| `safetyAlertsChannel` | text channel key      | Receives safety alerts (raids, spam).                           |

Enabling Community also needs `profile.verificationLevel` at least `low` and
`profile.explicitContentFilter: allMembers`; the plan blocks the change with the list of what
is missing. The toggle, its channels and those profile fields are sent in one request once the
channels exist, and the channels that need Community are created right after, in the same
sync. `profile.rulesChannel` is a deprecated alias of `community.rulesChannel`.

## `automod`

```yaml title="servers/cicord.yaml (community server)"
automod:
  mention-spam:
    name: Mention spam
    trigger: mentionSpam
    mentionLimit: 5
    mentionRaidProtection: true
    actions:
      block: { message: Too many mentions in one message. }
      alert: mod-log
      timeout: 1h
    exemptRoles: [maintainer, moderator]
  scams:
    name: Scam phrases
    trigger: keyword
    keywords: [free nitro, nitro giveaway, steam gift card]
    actions:
      block: { message: This message looks like a known scam. }
      alert: mod-log
      timeout: 10m
```

| Field                   | Type                                                                                       | Description                                                                                                  |
| ----------------------- | ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------ |
| `name`                  | string (1–100), **required**                                                               |                                                                                                              |
| `trigger`               | `keyword` \| `keywordPreset` \| `mentionSpam` \| `spam` \| `memberProfile`, **required**   | Up to 6 `keyword` rules, 1 of each other trigger. Cannot change in place: use a new key.                    |
| `enabled`               | boolean (`true`)                                                                           |                                                                                                              |
| `keywords`              | list (≤ 1000, each ≤ 60)                                                                   | `keyword`, `memberProfile`: words or phrases (`*` wildcards). These rules need `keywords` or `regex`.        |
| `regex`                 | list (≤ 10, each ≤ 260)                                                                    | `keyword`, `memberProfile`: Rust-flavoured regular expressions.                                              |
| `allowList`             | list (≤ 1000, each ≤ 60)                                                                   | Exceptions (`keyword`, `keywordPreset`, `memberProfile`).                                                    |
| `presets`               | list of `profanity` \| `sexualContent` \| `slurs`                                          | `keywordPreset`, required.                                                                                   |
| `mentionLimit`          | 1–50                                                                                       | `mentionSpam`: unique role and user mentions allowed per message.                                           |
| `mentionRaidProtection` | boolean                                                                                    | `mentionSpam`: also detect mention raids. A `mentionSpam` rule needs this or `mentionLimit`.                 |
| `actions`               | object, **required** (at least one action)                                                 | `block.message` (≤ 150): block the message (for `memberProfile`, the member's interactions) and show the text; `alert`: channel key that receives an alert; `timeout`: duration (`60s`, `10m`, `1h`, `1d`, `1w`, ≤ 4 weeks), `keyword` and `mentionSpam` only. |
| `exemptRoles`           | role keys (≤ 20)                                                                           |                                                                                                              |
| `exemptChannels`        | channel keys (≤ 50)                                                                        |                                                                                                              |

Rules are keyed like roles and [adopted](../../concepts/reconciliation/#adoption-by-name) by
name and trigger. On a Community server the mention spam rule cannot be deleted: set
`enabled: false` instead.

## `onboarding` (native)

Discord's own onboarding, shown in the Discord client to new members of a Community server.
It complements the Cicord [onboarding plugin](../../plugins/official/#onboarding), which runs on
the portal.

```yaml title="servers/cicord.yaml (community server)"
onboarding:
  enabled: true
  defaultChannels: [welcome, rules, announcements, changelog, general, showcase, off-topic, help,
                    development, plugins]
  prompts:
    - key: interests
      title: What brings you here?
      options:
        - { title: Writing plugins, description: Get the Plugin author role, emoji: "🧩", roles: [plugin-author] }
        - { title: Hanging out, description: The voice lounge, emoji: "🎧", channels: [lounge] }
```

| Field                    | Type                           | Default          | Description                                                                                                           |
| ------------------------ | ------------------------------ | ---------------- | --------------------------------------------------------------------------------------------------------------------- |
| `enabled`                | boolean, **required**          |                  | Needs Community.                                                                                                      |
| `mode`                   | `default` \| `advanced`        | `default`        | `default`: only default channels count toward Discord's requirements; `advanced`: answers' channels count too.       |
| `defaultChannels`        | channel keys                   |                  | Channels every new member sees.                                                                                       |
| `prompts[]`              | list (≤ 15)                    |                  | Questions.                                                                                                            |
| `prompts[].key`          | key, **required**, unique      |                  | Stable key of the question (answers stay attached).                                                                   |
| `prompts[].title`        | string (1–100), **required**   |                  |                                                                                                                       |
| `prompts[].type`         | `multipleChoice` \| `dropdown` | `multipleChoice` |                                                                                                                       |
| `prompts[].singleSelect` | boolean                        | `false`          |                                                                                                                       |
| `prompts[].required`     | boolean                        | `false`          |                                                                                                                       |
| `prompts[].inOnboarding` | boolean                        | `true`           | `false`: only in Channels & Roles.                                                                                    |
| `prompts[].options[]`    | list (1–50), **required**      |                  | `title` (required, ≤ 50), `description` (≤ 100), `emoji`, `roles` (role keys granted), `channels` (channel keys shown); at least one role or channel. |

`cicord validate` applies Discord's rules to an enabled onboarding: in `default` mode, at
least 7 default channels; when `spec.everyone` is declared, every default channel open to
`@everyone` (voice and stage: with Connect), and 5 of the counted channels where `@everyone`
can send messages.

## `welcomeScreen`

| Field         | Type           | Default | Description                                                          |
| ------------- | -------------- | ------- | -------------------------------------------------------------------- |
| `enabled`     | boolean        | `true`  | Needs Community.                                                     |
| `description` | string (≤ 140) |         |                                                                      |
| `channels[]`  | list (≤ 5)     |         | `channel` (key, required), `description` (1–42, required), `emoji`. |

```yaml title="servers/cicord.yaml (community server)"
welcomeScreen:
  enabled: true
  description: GitOps for Discord - declare your server in git, review changes, let Cicord apply them.
  channels:
    - { channel: rules, description: Read the rules, emoji: "📜" }
    - { channel: help, description: Ask a question, emoji: "❓" }
```

## Emojis, stickers and soundboard

Expressions are declared from files of the repository, relative to the server file, and keyed
like roles.

```yaml
emojis:
  party: { name: party, image: ./assets/party.png, roles: [member] }
stickers:
  wave: { name: Wave, emoji: wave, file: ./assets/wave.png }
soundboard:
  horn: { name: Horn, sound: ./assets/horn.ogg, volume: 0.6 }
```

| Kind               | Fields                                                                                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `emojis.<key>`     | `name` (required, `[A-Za-z0-9_]{2,32}`, unique), `image` (required: png, jpg, gif or webp, ≤ 256 KiB), `roles` (only these role keys can use it).            |
| `stickers.<key>`   | `name` (required, 2–30), `description` (≤ 100), `emoji` (required: related emoji name for suggestions), `file` (required: png, apng, gif or Lottie json, ≤ 512 KiB). |
| `soundboard.<key>` | `name` (required, 2–32), `sound` (required: mp3 or ogg, ≤ 512 KiB; Discord accepts at most 5.2 s), `volume` (0–1, default `1`), `emoji`.                    |

Slots depend on the boost level: emojis 50/100/150/250 static plus as many animated,
stickers 5/15/30/60, sounds 8/24/36/48. A changed file cannot be updated in place: declare a
new key. `cicord import` downloads existing expressions into `servers/assets/<server>/`.

## Boost-gated settings

The planner reads the live boost level and features. What Discord would refuse is
[blocked in the plan](../../concepts/reconciliation/#blocked-changes-and-their-explanations)
instead of failing on apply:

| Setting                                                                               | Needs                                                  | Discord feature        |
| ------------------------------------------------------------------------------------- | ------------------------------------------------------ | ---------------------- |
| animated `profile.icon`                                                               | Boost level 1                                          | `ANIMATED_ICON`        |
| `profile.splash`                                                                      | Boost level 1                                          | `INVITE_SPLASH`        |
| `profile.banner`                                                                      | Boost level 2 (animated: level 3)                      | `BANNER`               |
| `roles.*.icon`, `roles.*.emoji`                                                       | Boost level 2                                          | `ROLE_ICONS`           |
| `roles.*.colors.secondary` (gradient)                                                 | Boost level 2; the primary color is applied meanwhile  | `ENHANCED_ROLE_COLORS` |
| Emojis, stickers, sounds beyond the slots                                             | A higher boost level                                   |                        |
| Announcement and stage channels, Community channels, welcome screen, native onboarding | Community (live, or enabled by the same plan)          | `COMMUNITY`            |

A feature flag is enough (partnered or verified servers get some without boosts). Settings
that wait for a boost level keep the server `Synced` and are applied by the first sync after
the level is reached.

## `access`

| Field                                  | Type                        | Description                                                            |
| -------------------------------------- | --------------------------- | ---------------------------------------------------------------------- |
| `admins.roles`, `admins.users`         | role keys, Discord user IDs | Admins of this server in the admin console. The owner is always admin. |
| `moderators.roles`, `moderators.users` | role keys, Discord user IDs | Moderators.                                                            |

What each role can do: [Access and roles](../../concepts/access-and-roles/).

## `portal`

Declaring `portal` publishes the server on the community portal. Details and examples:
[Customize from YAML](../../portal/customize/).

| Field           | Type                                                      | Default          | Description                                                                                                                            |
| --------------- | --------------------------------------------------------- | ---------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| `theme.primary` | `#rrggbb`                                                 |                  | Buttons, links, focus.                                                                                                                 |
| `theme.accent`  | `#rrggbb`                                                 |                  | Highlights.                                                                                                                            |
| `theme.logo`    | relative path                                             |                  | png, jpg, gif, webp, avif or svg, inside the repository.                                                                               |
| `theme.mode`    | `light` \| `dark` \| `system`                             | `dark`           | `system` follows the visitor's light or dark preference.                                                                               |
| `title`         | string (1–100)                                            | server name      | Home page headline.                                                                                                                    |
| `tagline`       | string (≤ 200)                                            |                  | One sentence under the title.                                                                                                          |
| `description`   | string (≤ 4000)                                           |                  | Markdown subset. Raw HTML is shown as text.                                                                                            |
| `banner`        | relative path                                             |                  | Wide header image, same rules as the logo.                                                                                             |
| `links[]`       | list (≤ 12)                                               |                  | `label` (1–60), `url` (http(s), ≤ 500), `icon`: `link` (default), `website`, `discord`, `youtube`, `twitch`, `x`, `bluesky`, `mastodon`, `instagram`, `tiktok`, `github`, `reddit`, `steam`, `rules`, `calendar` or `shop`. |
| `pages`         | `{ onboarding, profile, leaderboard, tickets }` booleans  | all `true`       | Pages of the default portal.                                                                                                           |
| `locale`        | `en` \| `fr`                                              | browser language | Interface language of the default portal.                                                                                              |

## `analytics`

Aggregated activity counts (members, messages, voice, reactions, invites, moderation,
commands) shown on the admin console's Analytics page. They are collected when the block is
absent. What is collected, the privacy guarantees and the intents each group needs:
[Server analytics](../../self-hosting/analytics/).

```yaml title="servers/cicord.yaml (community server)"
analytics:
  presence: true # needs the ServerPresences intent
  portal: members
```

| Field       | Type                             | Default | Description                                                                                                                                                                                                                   |
| ----------- | -------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enabled`   | boolean                          | `true`  | `false` stops collection for this server. Points already recorded stay until retention removes them.                                                                                                                         |
| `retention` | `<n>d` (1–9999)                  | `400d`  | How long points are kept, in days.                                                                                                                                                                                            |
| `presence`  | boolean                          | `false` | Also aggregate member statuses, platforms and activities. Needs the privileged `ServerPresences` intent; without it, only Discord's approximate online count is sampled.                                                     |
| `portal`    | `off` \| `members` \| `public`   | `off`   | Share server-wide activity on the community portal ([`GET /api/v1/portal/{server}/analytics`](../../reference/http-api/portal/#activity)), to the signed-in members of the server or to anyone. Only channels everyone can see are named; nothing per member. |

## Annotations for the simulator

With `cicord dev --sim`, the annotations `sim.cicord.dev/boosts: "<n>"` and
`sim.cicord.dev/features: "A,B"` seed the simulated boost count and feature flags:

```yaml
metadata:
  name: my-community
  annotations:
    sim.cicord.dev/boosts: "7"
```

## Validation

Beyond the [JSON Schema](https://github.com/cicord/cicord/blob/main/spec/schemas/server.schema.json),
`cicord validate` fails when:

- `metadata.name` does not match the file name, or two servers share a `discordId`;
- a [reference](#references) does not resolve or points to the wrong channel type, or a key is
  both a category and a channel;
- a channel sets a field of another channel type, `syncWithCategory: true` without a category
  or with `overwrites`, `topic` and `guidelines` together, or two tags with the same name;
- a role sets both `icon` and `emoji`, a `color` that differs from `colors.primary`, or
  `colors.tertiary` without `colors.secondary`;
- an AutoMod rule exceeds the per-trigger limit, uses a field or action its trigger does not
  support, has no action, or has an invalid regular expression;
- enabled Community lacks a rules or updates channel, or sets a verification level below
  `low` or a content filter other than `allMembers`; a setting needs Community while
  `community.enabled` is `false`;
- enabled onboarding breaks Discord's [default channel rules](#onboarding-native), repeats a
  question key, or has an answer without role or channel;
- a referenced file is missing or above its size limit, or two emojis share a name.

It warns when Community is enabled without those two profile fields, when a setting needs
Community but `community` is not declared, when `profile.rulesChannel` is used, and when
`prune: true` is combined with `deletionProtection: false`.
