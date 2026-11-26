---
title: Official plugins
description: The 29 official plugins of Cicord 0.2.0, with their versions, commands, grants and settings.
---

The official registry, [cicord/cicord-plugins](https://github.com/cicord/cicord-plugins),
publishes 29 signed plugins. Install one with `cicord plugin add cicord/<name> --bot <bot>` or
from the admin console's Store; `cicord.lock` pins the reviewed release. Plugin versions are
independent of the engine version.

- **Engine:** every registry release declares `spec.engine: ">=0.2.0"`, so it needs a Cicord
  0.2.0 runtime or later.
- **Bundled copies:** the binary also bundles ten of them (welcome, onboarding, tickets,
  polls, levels, moderation, reaction-roles, audit-log, announcements and voice-rooms) for
  offline and first installs. A bundled copy can be older than the registry release; see
  [which copy wins](../publishing/#source-cicord-and-the-bundled-plugins).
- **Upgrading from 0.1:** Cicord 0.2.0 does not promise compatibility with data written by
  0.1.x plugins. Back up first and follow [Upgrading](../../self-hosting/upgrading/#from-0111-to-020).

## Catalogue

Each link opens the plugin's source directory: manifest, configuration schema, README and
tests. The README has the complete settings.

| Plugin | Version | What it does | Commands | Privileged intents |
|---|---|---|---|---|
| [ai-assistant](https://github.com/cicord/cicord-plugins/tree/main/plugins/ai-assistant) | 0.1.0 | Answers questions from reviewed sources with citations and a daily token budget. **Experimental.** | `/ask question`, `/ask optout` | none |
| [announcements](https://github.com/cicord/cicord-plugins/tree/main/plugins/announcements) | 0.2.0 | Scheduled and one-off announcements, countdowns and sticky messages. | `/announce`, `/announce-list`, `/announce-cancel`, `/countdown create` | none |
| [anti-nuke](https://github.com/cicord/cicord-plugins/tree/main/plugins/anti-nuke) | 0.2.0 | Watches administrative changes against audit evidence and alerts staff (monitor mode only). | `/antinuke status`, `/antinuke trust` | none |
| [audit-log](https://github.com/cicord/cicord-plugins/tree/main/plugins/audit-log) | 0.2.0 | Staff log of server changes with evidence-based attribution and 30-day search. | none | `ServerMembers` |
| [birthdays](https://github.com/cicord/cicord-plugins/tree/main/plugins/birthdays) | 0.1.0 | Opt-in birthday greetings and 24-hour roles in each member's local date. | `/birthday set`, `remove`, `upcoming` | `ServerMembers` |
| [channel-automations](https://github.com/cicord/cicord-plugins/tree/main/plugins/channel-automations) | 0.1.0 | Per-channel threads, reactions, crossposting, media-only rules and cleanup. | none | none |
| [community-events](https://github.com/cicord/cicord-plugins/tree/main/plugins/community-events) | 0.1.0 | Recurring Discord events, attendance, opt-in reminders and a calendar feed. | `/event create`, `/event reminders` | none |
| [economy](https://github.com/cicord/cicord-plugins/tree/main/plugins/economy) | 0.1.0 | Server points, daily claims, transfers and a rewards shop. **Experimental.** | `/balance`, `/daily`, `/weekly`, `/pay`, `/leaderboard`, `/shop`, `/buy`, `/inventory`, `/eco` | none |
| [feeds](https://github.com/cicord/cicord-plugins/tree/main/plugins/feeds) | 0.1.0 | Posts RSS, Atom and JSON Feed entries from approved hosts. | none | none |
| [github-releases](https://github.com/cicord/cicord-plugins/tree/main/plugins/github-releases) | 0.2.0 | Announces releases of public GitHub repositories. | `/changelog latest`, `/changelog check` | none |
| [giveaways](https://github.com/cicord/cicord-plugins/tree/main/plugins/giveaways) | 0.1.0 | Weighted draws with seed commitments, verifiable results and claims. **Experimental.** | `/giveaway start`, `list`, `end`, `cancel`, `reroll` | none |
| [guard](https://github.com/cicord/cicord-plugins/tree/main/plugins/guard) | 0.2.0 | Link, duplicate and newcomer checks, and join-surge (raid) response. | `/lockdown on`, `/lockdown off` | `MessageContent`, `ServerMembers` |
| [invite-tracker](https://github.com/cicord/cicord-plugins/tree/main/plugins/invite-tracker) | 0.1.0 | Attributes joins to invites when the evidence allows, and ranks inviters. | `/invites`, `/invite-leaderboard` | `ServerMembers` |
| [levels](https://github.com/cicord/cicord-plugins/tree/main/plugins/levels) | 0.2.0 | Chat and voice XP, role rewards, seasons and a portal leaderboard. | `/rank`, `/top`, `/xp` | `MessageContent` |
| [member-roles](https://github.com/cicord/cicord-plugins/tree/main/plugins/member-roles) | 0.1.0 | Temporary, sticky, tenure and booster roles. | `/temprole add`, `remove`, `list` | `ServerMembers` |
| [moderation](https://github.com/cicord/cicord-plugins/tree/main/plugins/moderation) | 0.3.0 | Cases, expiring sanctions, quarantine, locks, reviewed purges and appeals. | `/warn`, `/timeout`, `/kick`, `/ban`, `/unban`, `/case`, `/purge`… | `ServerMembers` |
| [onboarding](https://github.com/cicord/cicord-plugins/tree/main/plugins/onboarding) | 0.2.0 | Step-by-step role picking on the community portal. | `/onboarding` | none |
| [polls](https://github.com/cicord/cicord-plugins/tree/main/plugins/polls) | 0.2.0 | Button, menu or native Discord polls with versioned closing results. | `/poll`, `/polls close`, `/polls results` | none |
| [reaction-roles](https://github.com/cicord/cicord-plugins/tree/main/plugins/reaction-roles) | 0.2.0 | Self-service role panels with buttons, a menu or reactions. | `/role-panel` | none |
| [reminders](https://github.com/cicord/cicord-plugins/tree/main/plugins/reminders) | 0.1.0 | Private or channel reminders with time zones, repeats and snooze. | `/remind create`, `list`, `cancel`, `/timezone set` | none |
| [staff-shifts](https://github.com/cicord/cicord-plugins/tree/main/plugins/staff-shifts) | 0.1.0 | Duty roles, weekly shifts and coverage gaps. | `/duty on`, `/duty off` | `ServerMembers` |
| [starboard](https://github.com/cicord/cicord-plugins/tree/main/plugins/starboard) | 0.1.0 | Highlights starred messages within their original audience. | `/starboard top` | `MessageContent` |
| [stats-reports](https://github.com/cicord/cicord-plugins/tree/main/plugins/stats-reports) | 0.1.0 | Weekly and monthly activity reports, short-stay counts and counter channels. | `/stats-counter` | none |
| [suggestions](https://github.com/cicord/cicord-plugins/tree/main/plugins/suggestions) | 0.1.0 | Proposals, changeable votes and a moderated decision board. | `/suggest` | none |
| [tags](https://github.com/cicord/cicord-plugins/tree/main/plugins/tags) | 0.1.0 | Reviewed Markdown answers and staff-managed tags. | `/tag` | none |
| [tickets](https://github.com/cicord/cicord-plugins/tree/main/plugins/tickets) | 0.4.0 | Typed private queues, portal desk, transcripts, ratings and modmail. | `/ticket-panel` | `MessageContent`, `ServerMembers` |
| [verification](https://github.com/cicord/cicord-plugins/tree/main/plugins/verification) | 0.2.0 | Member-bound challenges, account-age checks and a staff queue. | `/verify-panel`, `/verify approve`, `deny`, `status` | `ServerMembers` |
| [voice-rooms](https://github.com/cicord/cicord-plugins/tree/main/plugins/voice-rooms) | 0.2.0 | Voice channels on demand ("join to create"), managed by their owner. | `/voice rename`, `limit`, `lock`, `unlock`, `permit`, `kick`, `transfer`, `claim` | none |
| [welcome](https://github.com/cicord/cicord-plugins/tree/main/plugins/welcome) | 0.2.0 | Greets members, gives a default role and DMs an onboarding link. | `/welcome-preview`, `/welcome-stats` | `ServerMembers` |

The privileged intents column lists required intents only. Some optional features add one:
`MessageContent` for audit-log's `cacheContent`, tags' automatic replies, the ai-assistant help
channels, suggestions' forum input and channel-automations' media rules, `ServerMembers` for
stats-reports' short-stay counts. Privileged intents must be enabled in the Discord developer
portal; installing a plugin that needs one is refused, with the toggle to turn on, until it
is.

### Conventions

- **Optional features.** A manifest's `spec.optional` lists features with their own grants
  and intents. Grant them to turn the feature on. A configured feature without its grant
  never fails the instance: it gets an instance status line in the admin console instead.
- **Moderator settings.** Settings marked *(moderator)* below can be edited by moderators in
  the admin console; the others need an admin.
- **Disabling.** `config: {enabled: false}` turns an instance off without its required
  settings. ai-assistant, economy, feeds, giveaways, member-roles and tags are off until you
  set `enabled: true`.
- **Language.** Built-in messages and command descriptions come in English and French; most
  plugins accept `locale: en` or `fr`. Your own texts keep their language.
- **Member data.** Each plugin declares its records in `spec.privacy`; the admin console's
  member data export and erasure cover them.

## welcome

Greets members in a channel on join, after verification (`trigger: verified`) or after
onboarding (`trigger: onboarded`). It can pick a random variant, add a rendered card and the
member's inviter, give a default role and DM a link to the portal's onboarding page.

- **Commands:** `/welcome-preview [member]` (Manage Server), `/welcome-stats`.
- **Grants:** `discord.messages.send`, `discord.channels.read`, `discord.members.read`,
  `storage.kv`. Optional: `defaultRole` (`discord.roles.assign`), `onboarding`
  (`discord.dm.send`, `portal.links`), `verified` (`events.subscribe`), `inviter`
  (`plugins.query:invite-tracker.inviterOf`), `cards` (`render`, experimental) and
  `templates` (`files.read`).
- **Intents:** `ServerMembers`.

| Setting | Default | Description |
|---|---|---|
| `channel` (required) | | Channel of the welcome messages. |
| `message` *(moderator)* | `Welcome {member} to {server}!` | Template; `{member}`, `{server}`, `{number}` (member number). |
| `variants` | `[]` | Up to 25 alternate templates; the pick stays stable on a replay. |
| `trigger` | `join` | `join`, `verified` or `onboarded`. |
| `autoRole` | | Role given to every new member (`defaultRole`). |
| `dmOnboarding` | `true` | DM a link to the portal's onboarding page (`onboarding`). |
| `card`, `showInviter` | `false` | Add the image card, the known inviter. |
| `templateFile`, `leaveMessage` | | Template from the instance files; optional farewell. |

## onboarding

Role picking on the community portal, step by step (games, platforms, notifications…).
`/onboarding` replies with a signed personal link. It works on any server, Community or not,
and complements Discord's [native onboarding](../../configuration/server/#onboarding-native).
The bot needs Manage Roles and a role above every role offered.

- **Grants:** `discord.roles.assign`, `storage.kv`, `scheduler`, `portal.routes`,
  `portal.links`. No intents.
- **Data:** the member's last choices and completion time, kept 365 days.

| Setting | Default | Description |
|---|---|---|
| `steps` (required) *(moderator)* | | Up to 10 questions: `id`, `title`, `description`, `multiple`, `required`, and `options` (a `role` key with an optional `label`, `emoji`, `description`). |
| `title`, `intro` *(moderator)* | `Welcome!` | Heading and text before the first step. |
| `completedRole` | | Role given to members who complete it. |
| `successMessage` *(moderator)* | `You're all set!` | Shown once complete. |
| `linkMessage`, `buttonLabel` *(moderator)* | `Pick your roles on the community portal:`, `Open onboarding` | The `/onboarding` reply and its button. |

## tickets

Members open a private ticket from a panel, a message (**Open ticket on message**), the
portal or a DM (modmail). Staff and the member talk in a private channel; staff run the
queue from the portal's [ticket desk](../../portal/overview/): assignment, priority, labels,
replies, private notes, close and reopen. Closing keeps a transcript (HTML and Markdown) and
asks for a 1–5 rating. Ticket channels are **plugin-owned**: the reconciler never touches or
prunes them.

- **Commands:** `/ticket-panel [channel]` (Manage Channels).
- **Grants:** `discord.messages.send`, `discord.messages.read`, `discord.channels.read`,
  `discord.channels.manage`, `discord.members.read`, `storage.kv`, `portal.routes`,
  `portal.links`, `events.publish`, `scheduler`.
- **Optional:** `modmail` (`discord.dm.send` and the `DirectMessages` intent),
  `ratingPrompts` (`discord.dm.send`; without it members rate on the portal),
  `managedMessages` (`discord.messages.manage`; without it log channels get a summary
  without transcript files, and erasure blanks the bot's copies instead of deleting them),
  `onDuty` (`plugins.query:staff-shifts.onDuty`), `caseContext`
  (`plugins.query:moderation.cases`), `appealUpdates` (`events.subscribe`).
- **Intents:** `ServerMessages`, `MessageContent` (transcripts, the conversation shown to
  staff, modmail relay), `ServerMembers` (the staff list).
- **Modmail:** the bot manifest picks the instance that receives DMs
  (`spec.directMessages: {instance: support}`). A member on several eligible servers picks
  one first.
- **Portal routes:** members get `GET /tickets/mine`, `GET /tickets/mine/:id`,
  `POST /tickets` (`409 limit` beyond `maxOpenPerMember`) and `POST /tickets/:id/rating`.
  Staff get the paged queue `GET /tickets` (newest first, at most 50 tickets and 500
  records read per page; follow `cursor` until it is absent), the ticket detail, assignment,
  `PATCH` (priority, label), replies, notes, close, reopen, transcript, `GET /staff` and
  `GET /tickets/stats`. Staff routes need the moderator access level and a staff role of
  the ticket's type.
- **Bounded work:** the minute maintenance handles at most 25 due tickets, one page of relays
  and one page of log copies per run.
- **Interop:** publishes `opened`, `closed` and `rated`; serves `openFor` and the idempotent
  `open` action (the same caller and key return the same ticket; policy refusals are
  `denied`).

| Setting | Default | Description |
|---|---|---|
| `panelChannel` (required) | | Default channel of the panel. |
| `types` | `[]` | Up to 5 ticket types: `key`, `name`, `staffRoles`, `category`, `logChannel`, `defaultLabels`, `fields` (the form), `retainSourceForTranscript` (`true`). Without types, one general queue (key `default`) uses the settings below. |
| `category`, `staffRoles`, `logChannel` | | Category, staff roles and summary channel of the general queue. |
| `modmailType`, `appealType` | first type | Type of tickets opened by DM, by moderation appeals. |
| `firstResponseMinutes`, `inactivityHours` | `0` | Staff reminder, inactivity warning and auto-close (0: off). |
| `transcriptRetentionDays` | `30` | 1–365. |
| `recordRetentionDays` | `365` | 1–730; closed tickets, notes and indexes. |
| `labels` *(moderator)* | question, bug, self-hosting, plugin, other | Up to 25 `{key, name, emoji?}`. |
| `panel`, `welcomeMessage` *(moderator)* | | Panel message; first message (`{member}`, `{staff}`, `{subject}`, `{number}`). |
| `channelName` | `ticket-{number}` | `{number}`, `{member}`. |
| `maxOpenPerMember` *(moderator)* | `1` | 1–10. |

## polls

Polls with buttons, a select menu or Discord's native polls, live results, automatic closing
and an optional discussion thread. Closing publishes `polls.closed` (v1 to the same bot, v2
to every bot of the server) for levels, economy and audit-log.

- **Commands:** `/poll question choices [hours] [multiple] [style] [ping] [thread] …`, with 2
  to 10 choices separated by `|`. `ping` notifies exactly the picked member or role, never
  `@everyone`. `/polls close poll` (its author, a manager or Manage Server) and `/polls
  results poll`; `poll` autocompletes.
- **Native polls** (`style: native`) take 1–768 whole hours and answers of at most 55
  characters, and refuse eligible roles, weights and anonymity.
- **Portal route:** `GET /polls/:id`, with a typed route contract. Private channels need a
  member who can read the source now.
- **Grants:** `discord.messages.send`, `discord.messages.read`, `discord.channels.read`,
  `discord.members.read`, `storage.kv`, `scheduler`, `events.publish`, `portal.routes`.
  Optional: `discussionThread` (`discord.threads.manage`; without it `/poll thread:true` is
  refused before anything is posted). No intents. Native polls need the bot's Send Polls
  permission.
- **Voters:** above 10,000 participants v2 omits the voter IDs (`votersTruncated`); a
  consumer granted `plugins.query:polls.voters` pages through them, 1,000 at a time.
  Anonymous polls delete their ballots before publishing.

| Setting | Default | Description |
|---|---|---|
| `defaultHours`, `maxHours` *(moderator)* | `24`, `168` | Default and longest duration (at most 768). |
| `channels` *(moderator)* | `[]` | Channels where `/poll` may be used (empty: everywhere). |
| `managerRoles` | `[]` | May close any poll and ping any role. |
| `eligibleRoles`, `roleWeights` | `[]` | Who may vote, and the highest weight that applies. |
| `confirmVotes` | `true` | Tell voters privately what they voted for. |
| `color` | `#5865f2` | Embed color. |

## levels

XP for chatting, receiving reactions, voice time and votes in polls, with level-up
announcements, role rewards, multipliers, seasons, a daily recap and a portal leaderboard.

- **Commands:** `/rank [member]`, `/top [limit] [role]`, `/xp give|remove|set|reset` (Manage
  Server). French names: `/rang`, `/classement`.
- **Portal routes:** `GET /leaderboard` (public), `GET /me` (members), `GET /seasons/:id`
  (top 100).
- **Grants:** `discord.messages.send`, `discord.reactions.manage`, `discord.channels.read`,
  `discord.members.read`, `discord.roles.assign`, `storage.kv`, `scheduler`,
  `portal.routes`, `portal.links`. Optional: `voiceXp` (`ServerVoiceStates`), `pollBonus`
  (`events.subscribe`), `pollPages` (`plugins.query:polls.voters`; without it a truncated poll
  credits nobody), `levelEvents` (`events.publish`), `rankCard` (`render`, experimental),
  `importCsv` (`files.read`), `privacyCopies` (`discord.messages.read`).
- **Intents:** `ServerMessages`, `MessageContent` (very short messages earn nothing),
  `ServerMessageReactions`.
- **Level crossing:** a member crosses each level once per season, so removing and re-adding a
  reaction or `/xp remove` then `/xp give` never repeats the level-up. A redelivered event
  finishes an interrupted level-up: rewards, then the event, then at most one announcement.
- **Interop:** serves `memberLevel`, `leaderboard` and `grantXp`; publishes `levelUp`.

| Setting | Default | Description |
|---|---|---|
| `xpPerMessage` *(moderator)* | `15` | XP per message, at most once per cooldown. |
| `cooldownSeconds`, `minLength` | `60`, `3` | Delay between two XP messages; shorter messages earn nothing. |
| `reactionXp`, `pollBonusXp` | `2`, `10` | XP per reaction received, per closed poll voted in. |
| `voice` | | `xpPerMinute` (0), `requireUndeafened` (true), `afkChannel`. |
| `ignoredChannels`, `noXpRoles` | `[]` | Where, and who, earns nothing. |
| `roleMultipliers`, `channelMultipliers`, `boosts` | `[]` | Multipliers; `boosts` are dated (UTC). |
| `announce` *(moderator)* | | `channel`, `message` (`{member}`, `{level}`), `react` (🎉). |
| `rewards`, `rewardMode` | `[]`, `configured` | Up to 25 `{level, role, keep}`; `configured`, `stack` or `replace`. |
| `recap` | | `enabled`, `channel`, `top` (1–10), daily at 09:00 UTC. |
| `season`, `rankCard`, `importFile` | | Season `id` and `end`; image rank card; CSV import (`userId,xp`). |

## moderation

Warnings, timeouts, kicks and bans with cases, expiring sanctions, quarantine, channel locks,
reviewed message purges, private notes and appeals. Every case is published as
`moderation.case` for audit-log and staff-shifts.

- **Commands:** `/warn`, `/warnings`, `/timeout`, `/kick`, `/ban [duration]`, `/unban`,
  `/case view|reason|void`, `/history`, `/note`, `/purge`, `/quarantine`, `/release`,
  `/lock`, `/slowmode`, and a **Warn** user menu. The plugin checks the moderator's
  hierarchy (Discord only checks the bot's): the owner may act on anyone, nobody on the
  owner, members with the moderator or admin access level only by an admin, and otherwise
  the moderator's highest role must be strictly above the member's.
- **Grants:** `discord.auditlog.read`, `discord.messages.send`, `discord.messages.manage`,
  `discord.messages.read`, `discord.channels.read`, `discord.members.read`,
  `discord.members.moderate`, `discord.dm.send`, `storage.kv`, `scheduler`, `http.fetch` (to
  `hooks.slack.com` and `discord.com`), `portal.routes`, `events.publish`. Optional:
  `quarantine` (`discord.roles.assign`), `lock` (`server.overlays`), `appeals`
  (`portal.links`), `appealsViaTicket` (`plugins.call:tickets.open`), `tempRoles`
  (`events.subscribe`).
- **Intents:** `ServerMembers`, `ServerMessages` (flood protection: who posts and when, never
  the content), `ServerModeration`.
- **Temporary bans** are lifted from the plugin's records: the case must still be the
  member's active one and the Discord ban must carry its marker. When the audit log is
  readable, a newer ban or unban by someone else means staff took it over, and the expiry
  leaves it. Banning a banned member makes the new case own the ban.
- **Purge:** `/purge` previews recent messages; only the requesting moderator can confirm,
  within two minutes. A purge that stops after a failed batch still records a case.
- **Quarantine and locks:** `/quarantine` (1–10080 minutes, default 7 days) uses
  `quarantineRole`; a member who leaves and returns gets it back. `/lock` and `/slowmode`
  apply a temporary [overlay](../../concepts/reconciliation/) to a managed channel; a lock
  denies Send Messages to `@everyone` and neutralizes other allows except the bot's and the
  staff roles'. An admin can lift each lock on the server's Plan page.
- **Appeals:** a sanction DM links to one case for 7 days; the member signs in with Discord
  even after a ban. Staff decide on `/appeals/staff`, never on their own case or one issued
  by a moderator ranked above them.
- **Interop:** serves `standing`, `cases` and the `sanction` action (warn, timeout, kick,
  ban, quarantine, release).

| Setting | Default | Description |
|---|---|---|
| `logChannel` (required) | | Staff channel of the log. |
| `warningDays` *(moderator)* | `30` | Warnings expire after this many days. |
| `escalation` *(moderator)* | 3 warnings → timeout | Up to 10 rules: `warnings`, `action` (`timeout`, `kick`, `ban`, `tempban`), `durationMinutes` (60). |
| `dmMembers` | `true` | Tell members by DM why. |
| `flood` | 6 messages in 5 s → 5 min timeout | `enabled`, `messages`, `seconds`, `timeoutMinutes`, `exemptRoles`. |
| `log` | all on | `joins`, `leaves`, `updates` (nicknames and roles). |
| `purge` | `maxCount: 500` | Limit of a purge. |
| `quarantineRole` | | Assignable role without declared holders. |
| `appeals` | off | `enabled`, `via` (`portal` or `ticket`), `channel`, `cooldownDays` (7). |
| `sanction` | | `allowStaffTargets`: installations allowed to act on staff. |

Mirror cases to Slack or Discord with the optional `WEBHOOK_URL`
[secret](../../configuration/bot/); `webhook.url` in the config still works but is
deprecated.

## reaction-roles

Self-assignable roles from **panels** defined in the config, with buttons, a select menu or
emoji reactions. On start and with `/role-panel`, an unchanged panel is left alone, a changed
one is edited in place and a deleted one is posted again.

- **Commands:** `/role-panel [panel] [new_message]` (Manage Roles).
- **Grants:** `discord.messages.send`, `discord.messages.read`, `discord.channels.read`,
  `discord.roles.assign`, `discord.members.read`, `discord.reactions.manage`, `storage.kv`.
- **Intents:** `ServerMessageReactions` (reaction panels).
- **Safety:** a click is checked against the panel as configured now, answers are
  ephemeral, and additions run before removals, so a failed swap keeps the current role. The
  bot's role must be above the roles it offers.

| Setting | Default | Description |
|---|---|---|
| `panels` | `[]` | Up to 20: `name` (stable key), `channel`, `title`, `description`, `color`, `style` (`buttons`, `select`, `reactions`), `mode` (`multiple`, `single`), `requiredRole`, `options` (`role`, `label`, `emoji`, `description`). |
| `syncOnStart` | `true` | Post missing panels and update changed ones on every new revision. |

:::caution
Every member who can see a panel can take its roles: never list a role with moderation or
admin permissions. A reaction panel offers at most 20 options (Discord's limit of distinct
reactions per message). A reaction cannot be answered privately, so a member without the
required role is silently ignored.
:::

## audit-log

Posts server observations to a private staff channel in batched embeds: joins, leaves,
nicknames, roles, messages, channels, threads, expressions, server settings, invites, bans,
voice, AutoMod and partner plugins' events. The actor comes from Discord's audit log: only a
confirmed host attribution earns the Cicord label, and an unresolved deletion reports an
unknown actor after 30 seconds. Without `cacheContent` it stores no message text.

- **Portal routes:** `GET /search` and `GET /export` (moderators; `q`, `member`, `channel`,
  `category`, `cursor`). Every result rechecks the viewer's access to the source.
- **Grants:** `discord.messages.send`, `discord.messages.read`, `discord.channels.read`,
  `discord.auditlog.read` (with View Audit Log), `storage.kv`, `scheduler`, `portal.routes`.
  Optional: `search` (`storage.index`), `partners` (`events.subscribe`: moderation, polls,
  levels, guard, anti-nuke, verification, tickets, giveaways, economy and member-roles
  topics), `cacheContent` (`MessageContent`).
- **Intents:** `ServerMembers`, `ServerMessages`, `ServerModeration`, `ServerInvites`,
  `ServerVoiceStates`, `ServerExpressions`, `AutoModerationExecution`.
- **Destinations:** a category channel must have an audience equal to or narrower than
  `channel`; an unknown or wider one disables its delivery.

| Setting | Default | Description |
|---|---|---|
| `channel` (required) | | Staff channel of the log. Keep it private. |
| `channels` | | `members`, `messages`, `plugins`, `structure`, `security`, `voice`: a category in its own channel. |
| `events` | all on but `messageEdits`, `polls` | One switch per category. |
| `ignoredChannels`, `ignoreBots` *(moderator)* | `[]`, `true` | |
| `batchSeconds` *(moderator)* | `10` | Collect events this long, then post them together (0: one by one). |
| `maxPerBatch` *(moderator)* | `60` | The rest are counted, not posted, so a raid cannot flood the log. |
| `retentionDays` | `30` | 1–30 days of searchable metadata and bot log copies. |
| `cacheContent` | `false` | Keep up to 2,000 characters per message for at most 7 days. |

## announcements

Recurring or one-off announcements from the config (cron, interval or date, in your time
zone), `/announce` for staff, countdowns and sticky messages. Mentions ping only who you say.

- **Commands:** `/announce channel [when] [mention] [everyone]` (Manage Messages) opens a
  form, then posts now or later (`in 2h`, `18:30`, `2026-12-24 18:00`, up to 90 days);
  `/announce-list`; `/announce-cancel id`; `/countdown create title date channel
  [milestones]` (default `7d,1d,1h`).
- **Portal routes** (staff): `POST /preview`, `POST /compose`, `GET /scheduled`.
- **Grants:** `discord.messages.send`, `discord.channels.read`, `storage.kv`, `scheduler`,
  `portal.routes`. Optional: `countdowns` and `recoverPosts` (`discord.messages.read`),
  `stickies` (`discord.messages.read`, `discord.messages.manage`, the `ServerMessages`
  intent), `crosspost`.
- **Mention safety:** only the configured mentions ping. `@everyone` and `@here` need
  `allowEveryone` and are neutralized otherwise.
- **Restarts:** a run is posted once, even across restarts and daylight-saving changes; runs
  missed less than `catchUpMinutes` ago are still posted.

| Setting | Default | Description |
|---|---|---|
| `timeZone` *(moderator)* | `UTC` | Time zone of the schedules and of `/announce` dates. |
| `announcements` *(moderator)* | `[]` | Up to 50: `name`, `channel`, `enabled`, exactly one of `cron`, `every` (≥ 5 min) or `at` (`YYYY-MM-DD HH:MM`), `timeZone`, `content`, `embed`, `mentions`. |
| `stickies` | `[]` | Up to 50 `{channel, message, afterMessages}` kept at the bottom. |
| `crosspost` | `false` | Crosspost in announcement channels. |
| `allowEveryone` | `false` | Allow `@everyone` and `@here`. |
| `channels` | `[]` | Channels `/announce` may post to (empty: any). |
| `color` *(moderator)* | `#5865f2` | Embed color of `/announce` messages. |
| `maxScheduled`, `catchUpMinutes` | `25`, `10` | Waiting `/announce` messages; catch-up window. |

## github-releases

Announces the releases of public GitHub repositories: one embed per release with the notes
(cut to 4,096 characters), the author, a pre-release badge and an optional role ping. Drafts
are never announced, and a release is never announced twice.

- **Commands:** `/changelog latest repo` (posts the latest release publicly) and `/changelog
  check` (checks every feed now); both need Manage Server.
- **Grants:** `discord.messages.send`, `discord.channels.read`, `storage.kv`, `scheduler`,
  `http.fetch` (to `api.github.com` only). No intents.

| Setting | Default | Description |
|---|---|---|
| `feeds` (required) *(moderator)* | | 1 to 20: `repo` (`owner/name`), `channel`, `includePrereleases` (`false`), `mentionRole`, `label`. A repository may be listed twice. |
| `pollMinutes` | `15` | How often each feed is checked (5–1440). |
| `announceExisting` | `false` | Announce a feed's recent releases (5 at most) on its first check. |
| `color` *(moderator)* | `#2da44e` | Embed color. |

GitHub allows 60 unauthenticated requests per hour per IP address, and each check costs one
request per feed: keep `feeds × 60 / pollMinutes` well under 60. The plugin's
[README](https://github.com/cicord/cicord-plugins/tree/main/plugins/github-releases#readme)
details the rate limits.

## voice-rooms

Voice channels on demand, often called "join to create": a member who joins a hub channel
such as "➕ Create a channel" gets a voice channel of their own, is moved into it, and the
channel is deleted once nobody (but bots) is left. A server can have several hubs, each with
its own name, category, limit and roles.

- **Commands** (`/voice …`, for the room's owner or the staff, answered privately): `rename`
  (two per room per 10 minutes, across restarts), `limit`, `lock`, `unlock`, `permit
  member`, `kick member`, `transfer member` and `claim` (take over a room whose owner left).
- **Grants:** `discord.channels.read`, `discord.channels.manage` (the rooms are
  plugin-owned), `discord.voice.move`, `discord.members.read`, `storage.kv`, `scheduler`.
  Optional: `roomEvents` (`events.publish`: `roomCreated`, `roomClosed`).
- **Intents:** `ServerVoiceStates`.
- **Reliability:** rooms are created with a `provisionKey`, so a redelivered join resumes the
  same channel. A hub whose bot lacks Manage Channels, Connect or Move Members gets a
  `permissions:<hub>` status line and creates nothing. A 5-minute sweep deletes empty rooms
  the events missed.

| Setting | Default | Description |
|---|---|---|
| `hubs` (required) | | 1 to 10 hubs (below). |
| `emptyDelaySeconds` | `0` | How long an empty room is kept. |
| `cooldownSeconds` | `5` | Minimum time between two rooms of the same member. |
| `onRestart` | `adopt` | Rooms found at start: `adopt` deletes them once empty, `delete` at once. |
| `staffRoles` | `[]` | Roles that may use `/voice` on any room. |

Each hub: `channel` (required), `name` (`🔊 {member}'s channel`; `{member}`, `{username}`,
`{n}`), `category` (the hub's), `userLimit` (0), `bitrate` (8000–384000), `allowedRoles`
(empty: whoever can join the hub) and `ownerPermissions` (`ViewChannel`, `Connect`, `Speak`,
`Stream`, `MoveMembers`). The bot needs every permission it writes. `transfer` and `claim`
only give a room to a member with one of its hub's `allowedRoles` (or to the staff).

```yaml
- name: voice-rooms
  source: cicord/voice-rooms
  grants: [discord.channels.read, discord.channels.manage, discord.voice.move,
           discord.members.read, storage.kv, scheduler]
  config:
    hubs:
      - channel: create-room              # "➕ Create a channel"
      - channel: create-guild-room        # "➕ Create a guild channel"
        name: "🛡️ Guild room #{n}"
        category: guilds
        userLimit: 10
        allowedRoles: [guild]
    staffRoles: [mod]
```

## Moderation and protection

### guard

Checks messages (links, invites, mentions, attachments, duplicates, image hashes) and
newcomers, and responds to join surges. `mode: monitor` (the default) records findings for
staff; `enforce` acts.

- **Exemptions:** every bot account, staff, configured exemptions and the project's own
  Cicord bots (`ctx.server.cicordBotIds`), which the sanction step refuses on every path.
- **Duplicates:** 4 copies within 30 s in at least 2 channels, with a link or at least 20
  characters, so "gg" never counts. By default guard removes the copies without sanctioning.
- **Sanction limit:** in `enforce` mode, at most 5 automatic sanctions per 60 s across all
  members (`sanctionLimit`); further findings are recorded as `needs_review`.
- **Invites:** `allowOwnInvites` needs the optional `discord.invites.read` grant; without it
  every Discord invite is blocked and the status says why.
- **Raids:** `/lockdown on|off` is a staff decision that applies the raid measures even in
  `monitor` mode. Measures use `server.overlays` (verification level, slowmode, channel
  locks) and optionally `server.incident` (invite pause). Lifting a raid resumes invites
  within a few seconds.
- **Grants:** see the [README](https://github.com/cicord/cicord-plugins/tree/main/plugins/guard#readme)
  for the optional features (`discord.messages.manage`, `discord.members.moderate`,
  `plugins.call:moderation.sanction`, `media.analyze`, `http.fetch` feeds…). Intents:
  `ServerMessages`, `MessageContent`, `ServerMembers`, `AutoModerationExecution`.

### verification

New members pass a challenge on the portal before they get `verifiedRole`; held accounts go
to a staff queue (`/verify approve|deny`, `GET /queue`).

- **Typed challenge (default):** the member types a six-character code shown in an image,
  drawn from a CSPRNG over 31 symbols (A–Z and 2–9 without 0/O and 1/I/L). A blind guess
  succeeds once in 31^6. The `visual` method (pick a four-digit code among twelve) is an
  option. Three failures lock the challenge for 15 minutes; six in a day send the member to
  staff review. These slow scripts down but are not anti-bot measures: use Turnstile or
  hCaptcha (`http.fetch` and a `TURNSTILE_SECRET` or `HCAPTCHA_SECRET` secret).
- **Rendering:** both image methods need the optional `render` grant (experimental). Without
  it, or when a render fails, `fallback: review` (default) sends members to staff and
  `fallback: button` offers a button, with a status line, never a silent pass.
- **Young accounts:** `youngAccount: hold`, `delay` or `kick`. Kicks need
  `plugins.call:moderation.sanction` when moderation is installed, otherwise
  `discord.members.moderate`; until granted, accounts are held for review.

:::caution[Experimental, opt-in]
Image rendering (the `render` capability behind the typed and visual challenges) is
experimental and opt-in in 0.2.0. It stays experimental until usage is confirmed with three
target communities.
:::

### anti-nuke

Correlates channel and role deletions, kicks, bans, webhook creations and dangerous
permission grants with Discord's audit log, and alerts staff (staff channel, optionally an owner DM and a
`WEBHOOK_URL` webhook). Version 0.2.0 has **monitor mode only**: it never acts. Each alert says
what enforce mode would do (`wouldRespond` on the `triggered` event). Alerts are grouped per
actor and window; unknown actors are never targeted. `/antinuke status` reports the audit-log
permission, protected roles and the host's protection snapshot.

### member-roles

Temporary roles (`/temprole add|remove|list`), sticky roles restored on return, tenure
milestones and booster roles. Off until `enabled: true`.

- **Grants from other plugins:** economy, giveaways and birthdays grant roles through
  `grantTemporary`. Only roles listed in `temp.roles` are allowed, and an empty list refuses
  every call from another plugin, e.g. `temp: {roles: [supporter, winner, birthday]}`.
- **Privileged roles:** a temporary grant refuses a role with Administrator, Manage Roles,
  Manage Server, Ban Members, Kick Members, Manage Channels, Manage Webhooks, Mention Everyone
  or Timeout Members, or whose permissions are not known yet, unless it is also listed in
  `temp.privilegedRoles`.
- **Role checks:** a role declared in YAML, owned by an integration, missing or above the bot
  gets its own status line; only the rules using it skip it.

## Community

### tags

Reviewed Markdown answers from the instance files, plus tags staff edit on the portal
(`editableOnPortal`; concurrent edits get `409`). Members use `/tag`, a mention or the **Reply
with a tag** message menu; automatic keyword replies need `discord.messages.read` and
`MessageContent`. Restricted tags are only shown to members who can read their source
channel. Serves `tags.search` to partners. Off until `enabled: true`.

### starboard

Highlights messages that reach a star count, after checking that the destination's audience
is equal to or narrower than the source's. It never stores the message text or image. A
deleted source or lost audience removes the copy. Publishes `starred`.

### birthdays

Members opt in with `/birthday set day month` or the portal form; greetings and a 24-hour
role follow the member's local date. A member is greeted at most once a year, even after
removing and setting their birthday again. The role goes through member-roles
(`plugins.call:member-roles.grantTemporary`) or, without it, `discord.roles.assign`.

### feeds

Posts new RSS, Atom and JSON Feed entries from hosts approved in the instance's
`network.allow`. The first check records existing entries without posting them (`backlog`, 0
to 8, posts that many). Each check posts at most 8 entries and parses at most 500 from a
1 MiB response; posts are retried with a nonce and never duplicated. Off until `enabled:
true`.

### reminders

`/remind create when message` delivers by DM or in the channel (the member must still be
able to view and post there, otherwise it goes by DM). Day and week repeats keep their local
time across daylight-saving changes. `/timezone set` saves the member's zone, which
birthdays can read. A **Remind me** message menu sets a reminder for one hour later.

### community-events

Recurring Discord scheduled events per series (`timeZone`, default UTC), opt-in reminders and
attendance from voice presence. Publishes `started` and `ended` (with members who attended at
least `minMinutes`). `GET /calendar` and `GET /calendar.ics` list upcoming events and those
that ended in the last 30 days. Needs `discord.events.manage` and Manage Events.

### invite-tracker

Attributes a join to an invite only when the observed use counters match it; ambiguous joins
keep an unknown inviter. `/invites` shows valid, fake and departed counts. Needs Manage Server
to read the counters; without it, joins are still recorded without an inviter. Serves
`inviterOf` and `count`, publishes `joined`.

### staff-shifts

`/duty on|off` gives a duty role with a deadline; staff plan weekly shifts on the portal and
see coverage gaps. Counts closed tickets and moderation cases by `moderator.id` (automatic
cases are not counted). Serves `onDuty` to tickets.

### stats-reports

Weekly and monthly aggregate reports in a private staff channel (withheld when the channel is
public), from server analytics. Optional: charts (`render`), short-stay counts (an HMAC of the
member ID, never the raw ID) and counter channels (`discord.channels.manage`).

### suggestions

`/suggest` opens a proposal with changeable votes; moderators set its status (`open`,
`considered`, `approved`, `denied`, `implemented`). Proposal text is stored only with
`retainText: true`. Publishes `created` and `statusChanged`.

### channel-automations

One rule per channel: automatic threads, reactions, crossposting, one post per member per
interval, bot-message cleanup, timed deletion (pins are kept) and media-only channels
(`MessageContent`). Each feature needs its own grant; message contents are never stored.

### Experimental plugins

:::caution[Experimental, opt-in]
economy, giveaways and ai-assistant are experimental and opt-in in 0.2.0. They stay
experimental until usage is confirmed with three target communities. An empty configuration
leaves them disabled.
:::

#### economy

Server points: `/daily`, `/weekly` (reset at midnight in `timezone`), `/pay` (with an optional
tax to a treasury), a leaderboard and a shop (`/shop`, `/buy`, `/inventory`); managers use
`/eco give|take|set`. Points can come from chat, voice, levels, polls and other plugins. A
replayed operation never pays twice: receipts are kept at least 400 days, separately from
journal rows (`journalDays`, 365). A perk is delivered in the buyer's own `/buy` or portal
checkout; a definitive refusal refunds at once, an ambiguous failure stays pending for a retry
and is refunded after 72 hours. Temporary-role products go through member-roles. `debit`
only accepts calls on behalf of the debited member.

#### giveaways

`/giveaway start` posts a draw with a SHA-256 commitment to a fresh seed; staff can end,
cancel, reroll and list. Role bonuses, eligibility conditions (levels, invites, moderation
standing) and rewards (XP, points, a temporary role) come from partner plugins. After the
draw, `GET /giveaways/:id` publishes the seed, winners and entrants hash; entrants come in
pages of 1,000 (`entriesCursor`). `node test/verify.mjs <draw URL>` in the plugin checks a
draw independently.

#### ai-assistant

`/ask question` answers from approved Markdown files (and readable tags), quoting a cited
section; an uncited or unsupported answer falls back to a human contact. It needs an Anthropic
key bound as the `API_KEY` secret, injected only on requests to `api.anthropic.com`, and stops
at `dailyTokenBudget`. The answer runs as a durable task (`interaction.deferTask`); a
request that may have been charged is never retried. `/ask optout` stops processing a member's
messages.

## Plugins working together

Plugins call each other through declared [interop contracts](../writing/#versioned-events-and-plugin-calls): events
(`polls.closed`, `moderation.case`, `levels.levelUp`…), queries and actions. Each link needs an
explicit grant such as `plugins.query:polls.voters` or `plugins.call:member-roles.grantTemporary`
on the caller, and a missing partner only disables the feature that uses it. A call made from
an interaction or a portal route carries the member as `onBehalfOf`; calls from tasks,
schedules and events carry none.

[`examples/showcase`](https://github.com/cicord/cicord/tree/main/examples/showcase) runs
tickets, polls, levels, moderation, reaction-roles, audit-log, announcements and voice-rooms
(the bundled copies) on one bot, and is the end-to-end test fixture of the runtime. Try it
offline:

```sh
cicord dev --sim examples/showcase
```

[SDK coverage](../coverage/) maps each SDK feature to the plugin and the test that exercise
it, and ends with a checklist to try the showcase on a real Discord server.
