---
title: Official plugins
description: The ten plugins of the signed official registry, with their versions, commands, capabilities and settings.
---

The official registry [cicord/cicord-plugins](https://github.com/cicord/cicord-plugins)
(source `cicord`, tags `<name>/vX.Y.Z`) publishes ten plugins. Install one with
`cicord plugin add cicord/<name> --bot <bot>`, or from the admin console's Store. Their
sources are in that repository, under `plugins/<name>/`, and are complete examples of the
SDK.

| Plugin | Version | In one line | Commands | Privileged intents |
|---|---|---|---|---|
| [welcome](#welcome) | 0.1.1 | Greets new members, gives a default role, DMs an onboarding link. | `/welcome-preview`, `/welcome-stats` | `ServerMembers` |
| [onboarding](#onboarding) | 0.1.0 | Step-by-step role picking on the community portal. | `/onboarding` | none |
| [tickets](#tickets) | 0.2.1 | Support tickets in private channels, with a member and staff portal. | `/ticket-panel` | `MessageContent`, `ServerMembers` |
| [polls](#polls) | 0.1.1 | Polls with buttons or a select menu, live results, auto-close. | `/poll`, `/polls close`, `/polls results` | none |
| [levels](#levels) | 0.1.0 | XP, levels, role rewards, leaderboard on the portal. | `/rank`, `/top` | `MessageContent` |
| [moderation](#moderation) | 0.1.1 | Warnings, timeouts, kicks, bans, escalation, flood protection. | `/warn`, `/warnings`, `/timeout`, `/kick`, `/ban` | `ServerMembers` |
| [reaction-roles](#reaction-roles) | 0.1.1 | Self-assignable role panels (buttons, select menu or reactions). | `/role-panel` | none |
| [audit-log](#audit-log) | 0.1.0 | Server events in a staff channel, in batched embeds. | none | `ServerMembers` |
| [announcements](#announcements) | 0.1.0 | Scheduled and one-off announcements, `/announce` for staff. | `/announce`, `/announce-list`, `/announce-cancel` | none |
| [github-releases](#github-releases) | 0.1.0 | Announces GitHub releases in your channels. | `/changelog latest`, `/changelog check` | none |

The `cicord` binary also bundles copies of the first nine, for offline and first installs
(how the two are merged: [source `cicord`](../publishing/#source-cicord-and-the-bundled-plugins)).
The bundled copies are the same versions as the registry; github-releases is only in the
registry.

Privileged intents must be enabled in the Discord developer portal; installing a plugin that
needs one is refused, with the toggle to turn on, until it is.

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
| `linkMessage`, `buttonLabel` *(moderator)* | `Pick your roles on the community portal:`, `Open onboarding` | The `/onboarding` reply and its link button. |

## tickets

Members open a ticket from a panel button and a short form, or from the community portal;
staff and the member talk in a private channel only they can see; closing it posts a
summary to the staff log. Staff run the queue from the portal too: assignment, priority,
labels, replies, close and reopen. Ticket channels are **plugin-owned**: the reconciler
never touches or prunes them.

- **Commands**: `/ticket-panel [channel]` (Manage Channels) posts the panel.
- **Portal routes** (typed route contract):
  - members: `GET /tickets/mine`, `GET /tickets/mine/:id` (their own tickets, with the
    timeline), `POST /tickets` (open one: subject, description, optional label; `409 limit`
    beyond `maxOpenPerMember`);
  - staff: `GET /tickets?status=&assignee=&priority=&label=`, `GET /tickets/:id` (with the
    last 50 messages of the ticket channel, read on demand, never stored), `GET /staff`
    (every holder of a staff role), `POST /tickets/:id/assign` (`me`, a staff member or
    `null`), `PATCH /tickets/:id` (priority, label), `POST /tickets/:id/close` (optional
    reason), `POST /tickets/:id/reopen` (recreates the channel when needed),
    `POST /tickets/:id/messages` (the bot posts the reply, signed "— name (via portal)").
  Staff are the members of `staffRoles`, the server owner and Cicord admins. The staff
  routes need both: the moderator access level (checked by the runtime) and staff (checked
  by the plugin), so a Cicord moderator without a staff role gets `403`.
- **Requires** Cicord 0.1.5 or later (`spec.engine: ">=0.1.5"`).
- **Capabilities**: `discord.messages.send`, `discord.messages.read`,
  `discord.channels.read`, `discord.channels.manage`, `discord.members.read`, `storage.kv`,
  `portal.routes`.
- **Intents**: `ServerMessages` (last activity), `MessageContent` (the conversation shown to
  staff in the portal, read on demand), `ServerMembers` (listing the staff for assignment).
  The last two are privileged: enable them in the Discord developer portal.

| Setting | Default | Description |
|---|---|---|
| `panelChannel` (required) | | Default channel of the panel. |
| `category` | | Category ticket channels are created in. |
| `staffRoles` | `[]` | Roles that see every ticket, can close them and run the queue from the portal. |
| `logChannel` | | Receives a summary of each closed ticket. |
| `labels` *(moderator)* | question, bug, self-hosting, plugin, other | Up to 25 `{key, name, emoji?}` to sort tickets (set from the portal). |
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
- `/polls close poll` (its author or a poll manager) and `/polls results poll`. `poll`
  autocompletes: the open polls of the author (all of them for a manager) for `close`, every
  poll for `results`.
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

- **Styles**: `buttons` (one button per role, which toggles it), `select` (a select menu: the
  member's roles of the panel become exactly the selection) or `reactions` (react with the
  option's emoji; removing the reaction takes the role back).
- **Modes**: `multiple`, or `single` (taking a role removes the panel's other roles).
- **Panels in place**: each panel's message is remembered in KV (`panel:<name>`). Moving a
  panel to another channel posts a new message; delete the old one by hand (deleting needs
  `discord.messages.manage`, which the plugin does not request).
- **Safety**: a click is checked against the panel as configured now, so a stale button never
  grants a role that left the config; answers are ephemeral; a role change Discord refuses
  (403) is explained (the bot's highest role must be above it, with Manage Roles). Additions
  run before removals, so a failed swap keeps the member's current role.
- **Reaction panels**: a reaction cannot be answered privately, so a member without the
  required role is silently ignored (the panel footer states the requirement). In `single`
  mode the member's previous reaction stays on the message (the SDK cannot remove another
  user's reaction).

:::caution
Every member who can see a panel can take its roles: never list a role with moderation or
admin permissions. A reaction panel offers at most 20 options (Discord's limit of distinct
reactions per message); `/role-panel` reports the rest, and they are logged.
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

- **What it logs**: one line per event, with `<t:…>` timestamps and no pings. Joins show the
  account age, with ⚠️ under 7 days. `messageEdits` is off by default because Discord reports
  link previews as edits. A deleted message shows its author when the plugin saw the message
  since its last start.
- **Batches**: events are queued in KV and posted together `batchSeconds` after the first one,
  by a single task per batch; past `maxPerBatch`, the post ends with "…and N more events not
  posted". A `@every 1m` sweep posts a batch whose task was lost.
- **`moderation.case`**: the payload is the moderation plugin's case: `number`, `action`,
  `member { id, name }`, `moderator { id, name }` or `null`, `reason`, `durationMinutes`. The
  moderation plugin publishes one for every case.

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

- **Configured announcements**: `cron` takes 5 fields or a descriptor (`@daily`…); `mentions`
  takes `roles`, `everyone` and `here`. A `* * * * *` schedule evaluates them in their time
  zone and posts each due one once: the last run is kept in KV, so restarts, a second tick
  and the repeated hour of a daylight-saving change never post twice. Runs missed while the
  bot was down are posted (once) if they are less than `catchUpMinutes` old.
- **`/announce`** asks for a title and a message in the form; `when` is read in the plugin's
  time zone. `/announce-list` shows the scheduled and the configured announcements (next run,
  invalid schedules).
- **Mention safety**: only the configured mentions ping (`allowedMentions`). `@everyone` and
  `@here` need `allowEveryone` (an admin setting) and are neutralized in the text otherwise;
  other role mentions in the text become plain names. `/announce` bodies go in an embed, which
  never pings.

The plugin host has no `Intl`, so the plugin carries the offset and daylight-saving rules
(EU, US, Australia, New Zealand) of the time zones listed in its schema.

## github-releases

Announces the releases of public GitHub repositories: one embed per release with the notes
(cut to 4096 characters with a link), the author, a pre-release badge and an optional role
ping. Drafts are never announced, and a release is never announced twice.

- **Commands**: `/changelog latest repo` (autocompletes from the configured feeds; posts the
  latest release publicly) and `/changelog check` (checks every feed now); both need Manage
  Server.
- **Schedule**: a tick every 5 minutes checks the feeds whose `pollMinutes` have elapsed.
- **Capabilities**: `discord.messages.send`, `discord.channels.read`, `storage.kv`,
  `scheduler`, `http.fetch` (to `api.github.com` only). No intents.

| Setting | Default | Description |
|---|---|---|
| `feeds` (required) *(moderator)* | | 1 to 20: `repo` (`owner/name`), `channel`, `includePrereleases` (`false`), `mentionRole`, `label` (display name). A repository may be listed twice, for example pre-releases in another channel. |
| `pollMinutes` | `15` | How often each feed is checked (5–1440). |
| `announceExisting` | `false` | Announce a feed's recent releases on its first check (5 at most); off, only later releases are announced. |
| `color` *(moderator)* | `#2da44e` | Embed color. |

The GitHub API is called without a token: GitHub allows 60 requests per hour per IP address,
and each check costs one request per feed, so keep `feeds × 60 / pollMinutes` well under 60.
The plugin's [README](https://github.com/cicord/cicord-plugins/tree/main/plugins/github-releases#readme)
details the rate limits and the retry behavior.

## Using them together

[`examples/showcase`](https://github.com/cicord/cicord/tree/main/examples/showcase) runs
tickets, polls, levels, moderation, reaction-roles, audit-log and announcements (the bundled
copies) on one bot, and is the end-to-end test fixture of the runtime. Try it offline:

```sh
cicord dev --sim examples/showcase
```

[SDK coverage](../coverage/) maps each SDK feature to the plugin and the test that exercise it,
and ends with a checklist to try the showcase on a real Discord server.
