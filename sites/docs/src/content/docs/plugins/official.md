---
title: Official plugins
description: The nine official plugins, published in the signed cicord-plugins registry - welcome, onboarding, tickets, polls, levels, moderation, reaction-roles, audit-log and announcements - with their commands, capabilities and settings.
---

Nine official plugins are published in the signed official registry
[cicord/cicord-plugins](https://github.com/cicord/cicord-plugins) (source `cicord`, tags
`<name>/vX.Y.Z`). Install one with `cicord plugin add cicord/<name> --bot <bot>`, which locks
its exact commit and integrity in `cicord.lock`. Copies are also bundled in the `cicord` binary
for offline and first installs: the setup wizard always installs the bundled copy into
`plugins/<name>/`, and the admin console's Store does too unless the registry has a newer
version.
They are also complete examples of the SDK: their sources live in
[`examples/basic/plugins`](https://github.com/cicord/cicord/tree/main/examples/basic/plugins)
and [`examples/showcase/plugins`](https://github.com/cicord/cicord/tree/main/examples/showcase/plugins).

| Plugin | In one line | Commands | Privileged intents |
|---|---|---|---|
| [welcome](#welcome) | Greets new members, gives a default role, DMs an onboarding link. | `/welcome-preview`, `/welcome-stats` | `ServerMembers` |
| [onboarding](#onboarding) | Step-by-step role picking on the community portal. | `/onboarding` | none |
| [tickets](#tickets) | Support tickets in private, plugin-owned channels. | `/ticket-panel` | none |
| [polls](#polls) | Polls with buttons or a select menu, live results, auto-close. | `/poll` | none |
| [levels](#levels) | XP, levels, role rewards, leaderboard on the portal. | `/rank`, `/top` | `MessageContent` |
| [moderation](#moderation) | Warnings, timeouts, kicks, bans, escalation, flood protection. | `/warn`, `/warnings`, `/timeout`, `/kick`, `/ban` | `ServerMembers` |
| [reaction-roles](#reaction-roles) | Self-assignable role panels (buttons, select menu or reactions). | `/role-panel` | none |
| [audit-log](#audit-log) | Server events in a staff channel, in batched embeds. | none | `ServerMembers` |
| [announcements](#announcements) | Scheduled and one-off announcements, `/announce` for staff. | `/announce`, `/announce-list`, `/announce-cancel` | none |

Privileged intents must be enabled in the Discord developer portal; installing a plugin that
needs one is refused, with the toggle to turn on, until it is. Every plugin is version
`0.1.0` and follows the version of the binary.

Settings marked *(moderator)* below can be edited by moderators in the admin console; the
others need an admin.

## welcome

Greets new members in a channel, gives them a default role and sends them a DM with a link to
the onboarding page of the portal.

- **Commands**: `/welcome-preview [member]` (Manage Server) previews the message;
  `/welcome-stats` tells how many members were welcomed.
- **Capabilities**: `discord.messages.send`, `discord.channels.read`, `discord.members.read`,
  `discord.roles.assign`, `discord.dm.send`, `portal.links`, `storage.kv`.
- **Intents**: `ServerMembers` (privileged).

| Setting | Default | Description |
|---|---|---|
| `channel` (required) | | Channel where welcome messages are posted. |
| `message` *(moderator)* | `Welcome {member} to {server}!` | Template; placeholders `{member}`, `{server}`. |
| `autoRole` | | Role given to every new member. |
| `dmOnboarding` | `true` | DM a link to the portal's onboarding page. |

## onboarding

Role picking on the community portal, step by step (games, platforms, notifications…).
`/onboarding` replies with a link to the portal page. It works on any server, Community or
not, and complements Discord's [native onboarding](../../configuration/server/#onboarding-native).

- **Portal routes**: `GET /steps`, `POST /complete` (members).
- **Capabilities**: `discord.roles.assign`, `storage.kv`, `portal.routes`, `portal.links`.
  No intents.

| Setting | Default | Description |
|---|---|---|
| `steps` (required) *(moderator)* | | Questions, in order: `id`, `title`, `description`, `multiple`, `required`, and `options` (a `role` key with an optional `label`, `emoji`, `description`). |
| `title` *(moderator)* | `Welcome!` | Heading of the page. |
| `intro` *(moderator)* | | Text before the first step. |
| `completedRole` | | Role given to members who complete it. |
| `successMessage` *(moderator)* | `You're all set!` | Shown once complete. |
| `linkMessage`, `buttonLabel` *(moderator)* | | The `/onboarding` reply and its link button. |

## tickets

Members open a ticket from a panel button and a short form; staff and the member talk in a
private channel only they can see; closing it posts a summary to the staff log. Ticket
channels are **plugin-owned**: the reconciler never touches or prunes them.

- **Commands**: `/ticket-panel [channel]` (Manage Channels) posts the panel.
- **Portal routes**: `GET /tickets`, `GET /tickets/:id` (moderators).
- **Capabilities**: `discord.messages.send`, `discord.messages.read`,
  `discord.channels.read`, `discord.channels.manage`, `storage.kv`, `portal.routes`.
- **Intents**: `ServerMessages` (last activity only, never the content).

| Setting | Default | Description |
|---|---|---|
| `panelChannel` (required) | | Default channel of the panel. |
| `category` | | Category ticket channels are created in. |
| `staffRoles` | `[]` | Roles that see every ticket and can close them. |
| `logChannel` | | Receives a summary of each closed ticket. |
| `panel` *(moderator)* | | `title`, `description`, `buttonLabel`, `color` of the panel message. |
| `welcomeMessage` *(moderator)* | `Hi {member}, thanks for reaching out! {staff} will be with you shortly.` | Placeholders `{member}`, `{staff}`, `{subject}`, `{number}`. |
| `channelName` | `ticket-{number}` | Placeholders `{number}`, `{member}`. |
| `maxOpenPerMember` *(moderator)* | `1` | Open tickets per member (1–10). |

## polls

Quick polls with buttons or a select menu, live results, automatic closing and an optional
discussion thread. Publishes `polls.closed` for other plugins (levels gives voters bonus XP,
audit-log can log it).

- **Commands**: `/poll question choices [hours] [multiple] [style] [ping] [thread]`; choices
  are separated by `|` (2 to 10). `ping` notifies exactly the picked member or role (never
  the author, never `@everyone`); a role that is not mentionable can only be pinged by a poll
  manager.
- **Portal routes**: `GET /polls/:id` (public), with a typed route contract.
- **Capabilities**: `discord.messages.send`, `discord.messages.read`,
  `discord.channels.read`, `discord.threads.manage`, `storage.kv`, `scheduler`,
  `events.publish`, `portal.routes`. No intents.

| Setting | Default | Description |
|---|---|---|
| `defaultHours` *(moderator)* | `24` | How long a poll stays open when `/poll` does not say. |
| `maxHours` *(moderator)* | `168` | Longest poll allowed. |
| `channels` *(moderator)* | `[]` | Channels where `/poll` may be used (empty: everywhere). |
| `managerRoles` | `[]` | Poll managers: they may close any poll (its author always can) and ping any role with `/poll ping` (others only mentionable roles). Members with Administrator or Manage Server are managers too. |
| `confirmVotes` | `true` | Tell voters privately what they voted for. |
| `color` | `#5865f2` | Embed color. |

## levels

XP for chatting, receiving reactions and voting in polls. Level-up announcements, role
rewards, `/rank`, `/top`, a daily recap and a public leaderboard on the portal.

- **Commands**: `/rank [member]`; `/top [limit] [role]`.
- **Portal routes**: `GET /leaderboard` (public), `GET /me` (members), with a typed route
  contract. The reference portal's Leaderboard page uses it.
- **Schedule**: daily recap at 09:00 UTC.
- **Capabilities**: `discord.messages.send`, `discord.reactions.manage`,
  `discord.channels.read`, `discord.members.read`, `discord.roles.assign`, `storage.kv`,
  `scheduler`, `events.subscribe`, `portal.routes`, `portal.links`.
- **Intents**: `ServerMessages`, `MessageContent` (privileged: very short messages earn no
  XP), `ServerMessageReactions`.

| Setting | Default | Description |
|---|---|---|
| `xpPerMessage` *(moderator)* | `15` | XP per message, at most once per cooldown. |
| `cooldownSeconds` | `60` | Minimum delay between two messages that earn XP. |
| `minLength` | `3` | Shorter messages earn nothing. |
| `reactionXp` | `2` | XP for the author each time another member reacts. |
| `pollBonusXp` | `10` | XP for each voter when a poll closes. |
| `ignoredChannels` | `[]` | Channels where messages earn no XP. |
| `announce` *(moderator)* | | `channel`, `message` (placeholders `{member}`, `{level}`), `react` (🎉). |
| `rewards` | `[]` | `level`, `role`, `keep` (keep it when a higher reward is reached). |
| `recap` | | `enabled`, `channel`, `top` (1–10). |

## moderation

`/warn`, `/timeout`, `/kick` and `/ban` with an audit log, expiring warnings, automatic
escalation, flood protection, DMs to members and an optional Slack or Discord webhook. Every
case is published as `moderation.case` for the audit-log plugin.

- **Commands**: `/warn member reason [evidence]`, `/warnings member` (Moderate Members);
  `/timeout member duration [reason]` (Moderate Members); `/kick member [reason]` (Kick
  Members); `/ban member [reason] [delete_messages]` (Ban Members). Discord only checks the
  bot's role hierarchy, so the plugin checks the moderator's: the owner may act on anyone,
  nobody on the owner; members with the moderator or admin access level (`spec.access`) can
  only be sanctioned by an admin; and the moderator's highest role must be strictly above
  the member's. What cannot be verified is refused (ephemeral answer).
- **Portal routes**: `GET /warnings/:userId` (moderators), `DELETE /warnings/:userId`
  (admins).
- **Capabilities**: `discord.messages.send`, `discord.messages.manage`,
  `discord.channels.read`, `discord.members.read`, `discord.members.moderate`,
  `discord.dm.send`, `storage.kv`, `http.fetch` (to `hooks.slack.com` and `discord.com`
  only), `portal.routes`, `events.publish`.
- **Intents**: `ServerMembers` (privileged), `ServerMessages` (flood protection needs who
  posts and when, never the content).

| Setting | Default | Description |
|---|---|---|
| `logChannel` (required) | | Staff channel of the log. |
| `warningDays` *(moderator)* | `30` | Warnings expire after this many days. |
| `escalation` *(moderator)* | 3 warnings → timeout | `warnings`, `action` (`timeout`, `kick`, `ban`), `durationMinutes` (60). |
| `dmMembers` | `true` | Tell members by DM why. |
| `flood` | on: 6 messages in 5 s → 5 min timeout | `enabled`, `messages`, `seconds`, `timeoutMinutes`, `exemptRoles`. |
| `log` | all on | `joins`, `leaves`, `updates` (nicknames and roles). |
| `webhook` | | `url`, `format` (`slack` or `discord`): mirror every case. |

## reaction-roles

Self-assignable roles from **panels** defined in the config, with buttons, a select menu or
emoji reactions. Panels are kept up to date in place: on start and with `/role-panel`, an
unchanged panel is left alone, a changed one is edited, a deleted one is posted again.

- **Commands**: `/role-panel [panel] [new_message]` (Manage Roles).
- **Capabilities**: `discord.messages.send`, `discord.messages.read`,
  `discord.channels.read`, `discord.roles.assign`, `discord.members.read`,
  `discord.reactions.manage`, `storage.kv`.
- **Intents**: `ServerMessageReactions` (reaction panels only).

| Setting | Default | Description |
|---|---|---|
| `panels` | `[]` | Each panel: `name` (stable key), `channel`, `title`, `description`, `color`, `style` (`buttons`, `select`, `reactions`), `mode` (`multiple`, `single`), `requiredRole`, `options` (`role`, `label`, `emoji`, `description`). |
| `syncOnStart` | `true` | Post missing panels and update changed ones on every new revision. |

:::caution
Every member who can see a panel can take its roles: never list a role with moderation or
admin permissions. A reaction panel offers at most 20 options (Discord's limit of distinct
reactions per message).
:::

## audit-log

Posts server events to a staff channel in compact batched embeds: joins (with the account
age), leaves, role and nickname changes, message edits and deletions, moderation cases and
closed polls. It never shows what people wrote.

- **Subscriptions**: `moderation.case`, `polls.closed`.
- **Capabilities**: `discord.messages.send`, `discord.channels.read`, `storage.kv`,
  `scheduler`, `events.subscribe`.
- **Intents**: `ServerMembers` (privileged), `ServerMessages` (no `MessageContent`).

| Setting | Default | Description |
|---|---|---|
| `channel` (required) | | Staff channel of the log. Keep it private. |
| `channels` | | `members`, `messages`, `plugins`: send a category to its own channel. |
| `events` | | `joins`, `leaves`, `roles`, `nicknames`, `messageDeletes`, `moderation` on; `messageEdits` and `polls` off. |
| `ignoredChannels` *(moderator)* | `[]` | Message events there are not logged. |
| `ignoreBots` *(moderator)* | `true` | |
| `batchSeconds` *(moderator)* | `10` | Events are collected this long, then posted together (`0`: one by one). |
| `maxPerBatch` *(moderator)* | `60` | The rest are counted, not posted, so a raid cannot flood the log. |

## announcements

Recurring or one-off announcements from the config (cron, interval or date, in your time
zone), and `/announce` for moderators to schedule, list and cancel their own. Mentions ping
only who you say.

- **Commands**: `/announce channel [when] [mention] [everyone]` (Manage Messages) opens a
  form, then posts now or later (`in 2h`, `18:30`, `2026-12-24 18:00`, up to 90 days);
  `/announce-list`; `/announce-cancel id`.
- **Portal routes**: `GET /scheduled` (moderators).
- **Capabilities**: `discord.messages.send`, `discord.channels.read`, `storage.kv`,
  `scheduler`, `portal.routes`. No intents.

| Setting | Default | Description |
|---|---|---|
| `timeZone` *(moderator)* | `UTC` | Time zone of the schedules and of `/announce` dates. |
| `announcements` *(moderator)* | `[]` | Each: `name`, `channel`, `enabled`, exactly one of `cron`, `every` (`30m`, `6h`, `1d`, `2w`, ≥ 5 min, from `start`) or `at` (`YYYY-MM-DD HH:MM`), `timeZone`, `content`, `embed`, `mentions`. |
| `allowEveryone` | `false` | Allow `@everyone` and `@here`; otherwise they never ping. |
| `channels` | `[]` | Channels `/announce` may post to (empty: any). |
| `color` *(moderator)* | `#5865f2` | Embed color of `/announce` messages. |
| `maxScheduled` | `25` | Most `/announce` messages waiting at once. |
| `catchUpMinutes` | `10` | After a restart, recent missed runs are still posted (once). |

The plugin host has no `Intl`, so the plugin carries the offset and daylight-saving rules of
the time zones listed in its schema.

## Using them together

[`examples/showcase`](https://github.com/cicord/cicord/tree/main/examples/showcase) runs
tickets, polls, levels, moderation, reaction-roles, audit-log and announcements on one bot,
and is the end-to-end test fixture of the runtime. Try it offline:

```sh
cicord dev --sim examples/showcase
```
