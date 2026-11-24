# Plugin SDK coverage

Every surface of [`spec/sdk/cicord-sdk.d.ts`](../spec/sdk/cicord-sdk.d.ts), the plugin of
[`examples/showcase`](../examples/showcase/) that uses it, and the end-to-end test that drives
it through the real runtime on the Discord simulator (reconciler, bot host, plugin host,
scheduler, SQLite store). The last column tracks the manual check on real Discord.

Tests (Go, run by `go test ./...`):

- `rt/…`: `internal/runtime/showcase_test.go` and `showcase_more_test.go` (`TestShowcase*`;
  `rt/ReactionRoles`, `rt/AuditLog`, `rt/Announcements` cover the last three plugins). The probe fixture
  (`internal/runtime/testdata/probe`) covers error paths a real plugin never hits on purpose.
- `api/…`: `internal/api/showcase_test.go` (`TestShowcaseRouteAccess`): portal routes
  through the HTTP API, which enforces the access levels.
- `cli/…`: `internal/cli/plugin_test.go` (`TestShowcasePlugins`: `cicord plugin build`,
  `typegen --check` and `cicord plugin test` for every showcase plugin).
- Unit tests of each plugin with `@cicord/sdk/testing` live in
  `examples/showcase/plugins/*/test` (`pnpm test`).

Real Discord status: **to verify on real Discord** unless noted. The checklist at the end
lists the precise steps.

## Events

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `memberJoin` | moderation (audit log) | rt/Moderation ("joined") | to verify on real Discord |
| `memberLeave` | moderation | rt/Moderation (after `/kick`) | to verify on real Discord |
| `memberUpdate` (with `previous`) | moderation (nickname, roles) | rt/Moderation (nickname change) | to verify on real Discord |
| `messageCreate` **with** MessageContent | levels (short messages earn nothing) | rt/Levels | to verify on real Discord |
| `messageCreate` **without** MessageContent (content blanked per plugin, even when another plugin of the bot declares it; mention exemption) | tickets (activity), moderation (flood), probe | rt/ProbeErrorsAndGating, rt/Tickets, rt/Moderation; `internal/plugin` TestMessageContentGating | to verify on real Discord |
| `reactionAdd` / `reactionRemove` (partial message: id + channel only) | levels (author XP via `msg:<id>` KV) | rt/Levels | to verify on real Discord |
| `messageDelete` (id + channel only) | audit-log (author remembered from `messageCreate`) | rt/AuditLog | to verify on real Discord |
| `messageUpdate` | audit-log (`events.messageEdits`, off by default) | audit-log unit tests, `internal/plugin` unit tests | to verify on real Discord |
| `reactionAdd` / `reactionRemove` on a plugin's own message | reaction-roles (reaction panels) | rt/ReactionRoles | to verify on real Discord |
| `serverJoin` / `serverLeave` (real join vs lazy GUILD_CREATE after READY or after an outage; leave = GUILD_DELETE without `unavailable`) | — | `internal/plugin` TestServerEvents, `disgoadapter` TestTranslateServerJoinLeave, `internal/sim` TestServerJoinLeaveEvents | to verify on real Discord |
| Slash commands registered again after a re-invite (`serverJoin`) | all | `internal/runtime` TestCommandsReRegisteredOnServerJoin | to verify on real Discord |

## Commands

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `string` option (+ `choices`) | polls `question`, `choices`, `style` | rt/Polls | to verify on real Discord |
| `integer` option (`min`/`max`) | levels `/top limit` | rt/Levels | to verify on real Discord |
| `integer` option with `choices` | moderation `/timeout duration` (0 = remove) | rt/Moderation | to verify on real Discord |
| `number` option | polls `hours` (2.5) | rt/Polls | to verify on real Discord |
| `boolean` option | polls `multiple`, `thread`; moderation `/ban delete_messages` | rt/Polls, rt/Moderation | to verify on real Discord |
| `user` option | levels `/rank member`, moderation `/warn` `/kick` … | rt/Levels, rt/Moderation | to verify on real Discord |
| `channel` option | tickets `/ticket-panel channel` | rt/Tickets | to verify on real Discord |
| `role` option | levels `/top role` | rt/Levels | to verify on real Discord |
| `mentionable` option | polls `ping` (role) | rt/Polls | to verify on real Discord |
| `attachment` option | moderation `/warn evidence` (image in the case embed) | rt/Moderation | to verify on real Discord |
| `permissions` (default member permissions) | tickets `/ticket-panel` (ManageChannels), moderation (ModerateMembers, KickMembers, BanMembers) | rt/Tickets, rt/Moderation (a member is refused) | to verify on real Discord |
| `ephemeral: true` default (reply without flag is ephemeral) | levels `/rank`, tickets, polls, moderation | rt/Levels, rt/AutoDefer, rt/Tickets | to verify on real Discord |
| Subcommands (`subcommand()`, `interaction.subcommand`, ephemeral on the command) | polls `/polls close`, `/polls results` | rt/Polls; polls unit tests; `internal/plugin` TestSubcommandDispatch | to verify on real Discord |
| Subcommand groups (`interaction.group`) | — | `internal/plugin` TestSubcommandDispatch, `internal/sim` TestInvokeSubcommand, sdk tests | to verify on real Discord |
| `autocomplete` (string option, filtered by the typed text and the member) | polls `/polls close poll`, `/polls results poll` | rt/Polls; polls unit tests; `internal/plugin` TestAutocompleteDispatch (invalid, failing and slow handlers → empty list) | to verify on real Discord |
| `minLength` / `maxLength` (string option) | polls `poll` (`maxLength: 100`) | `internal/sim` TestInvokeSubcommand, `internal/plugin` TestInspectSubcommands | to verify on real Discord |

## Components, modals, interaction API

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| Buttons | tickets `open`, `close:<n>`, polls `vote:<poll>:<choice>`, `end:<poll>` | rt/Tickets, rt/Polls | to verify on real Discord |
| Ids with data (`vote:42` → `components.vote`) | polls, tickets | rt/Polls, rt/Tickets | to verify on real Discord |
| Select menu (multi-values) | polls `pick:<poll>` | rt/Polls | to verify on real Discord |
| Link button | levels `/rank` (portal link) | rt/Levels | to verify on real Discord |
| Buttons on an ephemeral message | tickets close confirmation | rt/Tickets | to verify on real Discord |
| `showModal` + modal submit (`fields`) | tickets `open` | rt/Tickets | to verify on real Discord |
| `interaction.update()` | polls (live results), tickets (confirm/cancel) | rt/Polls, rt/Tickets | to verify on real Discord |
| `interaction.defer({ ephemeral })` then `reply()` edits it | tickets modal submit | rt/Tickets | to verify on real Discord |
| `interaction.followUp()` (ephemeral, after `update()`) | polls vote confirmation, moderation (DM failed, escalation) | rt/Polls | to verify on real Discord |
| Auto-defer of a slow command (reply edits the deferral) | probe `/probe-slow` | rt/AutoDefer (AutoDeferAfter 100 ms) | to verify on real Discord |
| Auto-defer of a slow component (silent deferred update) | probe `slow` button | rt/AutoDefer | to verify on real Discord |

## Runtime services

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `schedules` (cron, UTC) | levels daily recap `0 9 * * *` | rt/Levels (`sched.runCron` two days ahead) | to verify on real Discord (wait for 09:00 UTC or use a `@every 1m` copy) |
| `ctx.schedule.after` + `tasks` | polls auto-close | rt/Polls (`sched.runTasks` in the future) | to verify on real Discord |
| `ctx.schedule.cancel` | polls closed manually | rt/Polls (task gone from the store) | to verify on real Discord |
| Task scheduled before a hot reload fires after it | polls | rt/Polls | to verify on real Discord |
| KV `get` / `set` / `delete` / `increment` / `list` | all | rt/* | n/a (runtime only) |
| KV `ttlSeconds` (expiry) | moderation warnings, levels cooldown | rt/Moderation (warnings expire after ≈2 s) | n/a |
| KV persists across a hot reload (new revision) | polls (poll #2 closed after the reload) | rt/Polls | n/a |
| `http.fetch` to an allow-listed host | moderation webhook (`hooks.slack.com`) | rt/Moderation (TLS httptest server via `Options.PluginHTTPClient`) | to verify with a real Slack/Discord webhook |
| `http.fetch` egress refusals (allow-list, private addresses, redirects) | — | `internal/plugin` fetch tests | n/a |
| `events.publish` + `subscriptions` (`polls.closed`) | polls → levels bonus XP | rt/Polls | n/a |
| `ctx.portal.link` | levels `/rank` | rt/Levels (signed `cicord_token`) | to verify on real Discord (open the link) |
| Portal route `public` | levels `GET /leaderboard`, polls `GET /polls/:id` | api/RouteAccess, rt/Levels, rt/Polls | to verify with the portal |
| Portal route `member` | levels `GET /me` | api/RouteAccess (401 anonymous, 403 non-member) | to verify with the portal |
| Portal route `moderator` | tickets `GET /tickets`, moderation `GET /warnings/:userId` | api/RouteAccess (403 member), rt/Tickets | to verify with the portal |
| Portal route `admin` | moderation `DELETE /warnings/:userId` | api/RouteAccess (403 moderator) | to verify with the portal |
| `ctx.log`, `console.*` → runtime log | all, probe | rt/AutoDefer (`console.log` captured) | n/a |
| `setTimeout` / timers | probe | rt/AutoDefer | n/a |

## Server, channels, members, messages

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `server.channel(key / {id})`, `server.role()` | all | rt/* | to verify on real Discord |
| `server.member()` | moderation, levels | rt/Moderation, rt/Levels | to verify on real Discord |
| `server.createChannel` (category, topic, overwrites `@everyone` / role key / `member:<id>`) | tickets | rt/Tickets (visibility per member) | to verify on real Discord |
| Plugin-owned channel never pruned (`prune: true`) and overwrites kept by `SyncServer` | tickets | rt/Tickets | to verify on real Discord |
| `server.deleteChannel` (ownership record removed) | tickets close | rt/Tickets | to verify on real Discord |
| `channel.send` | all | rt/* | to verify on real Discord |
| `channel.messages()` (history) | tickets summary | rt/Tickets | to verify on real Discord |
| `channel.message(id)` (one message; `undefined` when deleted) | polls task and `/polls close`, reaction-roles panels | rt/Polls, rt/ReactionRoles; `internal/plugin` TestChannelMessageOp | to verify on real Discord |
| `channel.createThread({ fromMessage })` | polls `thread` | rt/Polls | to verify on real Discord |
| `message.reply` | probe (bot mentioned), levels (no announce channel) | rt/ProbeErrorsAndGating | to verify on real Discord |
| `message.edit` (own message, from a task) | polls auto-close | rt/Polls | to verify on real Discord |
| `message.delete` | moderation flood | rt/Moderation | to verify on real Discord |
| `message.react` | levels 🎉 | rt/Levels | to verify on real Discord |
| `member.addRole` / `removeRole` / `hasRole` | levels rewards (`keep: false` replaced) | rt/Levels | to verify on real Discord |
| `member.timeout` (incl. 0 = remove) | moderation | rt/Moderation | to verify on real Discord |
| `member.kick` / `member.ban` | moderation | rt/Moderation | to verify on real Discord |
| `member.send` (DM, sent before a kick) | moderation | rt/Moderation | to verify on real Discord |
| `member.setNickname` | — | `internal/plugin` unit tests | to verify on real Discord |

## Errors and limits

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `CapabilityError` (instanceof, `.capability`) | probe `/probe-cap` | rt/ProbeErrorsAndGating | n/a |
| Handler error → generic ephemeral reply | probe `/probe-throw` | rt/ProbeErrorsAndGating | to verify on real Discord |
| Sync budget: runaway loop aborted, VM keeps serving | probe `/probe-spin` then `/probe-ping` | rt/ProbeErrorsAndGating | n/a |
| Startup budget, handler timeout, `onStop` bound | — | `internal/plugin` unit tests | n/a |
| Channel isolation (other servers) | — | `internal/plugin` unit tests | n/a |
| Config defaults incl. array items and `$ref` (and typegen agrees) | moderation `escalation[].durationMinutes`, levels `rewards[].keep`, onboarding `steps[].multiple` | rt/Moderation, rt/Levels; `internal/config` TestApplyDefaultsArraysAndRefs; `internal/typegen` TestDefaultsConformance | n/a |

## Official plugins: reaction-roles, audit-log, announcements

Three more plugins of the official catalog (`examples/catalog.go`), configured in the
showcase. Their unit tests are in `examples/showcase/plugins/<name>/test`.

### reaction-roles

Self-assignable roles from **panels** defined in the config (`panels[]`: `name`, `channel`,
`title`, `description`, `color`, `style`, `mode`, `requiredRole`, `options[]` of `role`,
`label`, `emoji`, `description`).

- **Styles**: `buttons` (one button per role, toggles it), `select` (a select menu: the
  member's roles of the panel become exactly the selection) or `reactions` (react with the
  option's emoji; removing the reaction takes the role back).
- **Modes**: `multiple`, or `single` (taking a role removes the panel's other roles).
- **Idempotent panels**: a panel's message is remembered in KV (`panel:<name>`). On start
  (`syncOnStart`, default on) and with `/role-panel [panel] [new_message]` (ManageRoles), an
  unchanged panel is left alone, a changed one is edited in place, a deleted one is posted
  again. Moving a panel to another channel posts a new message; the old one must be deleted
  by hand (deleting needs `discord.messages.manage`, which the plugin does not request).
- **Safety**: a click is checked against the panel as configured now (a stale button never
  grants a role that left the config); answers are ephemeral; a role change Discord refuses
  (403) is explained: "my highest role must be above it… Manage Roles". Additions run before
  removals, so a failed swap keeps the member's current role.
- **Capabilities**: `discord.messages.send`, `discord.messages.read` (find the panel message
  again), `discord.channels.read`, `discord.roles.assign`, `discord.members.read` and
  `discord.reactions.manage` (reaction panels), `storage.kv`. Intent:
  `ServerMessageReactions` (not privileged).
- **Limits**: a reaction panel offers at most 20 options (Discord's limit of distinct
  reactions per message; the rest are reported by `/role-panel` and logged). A reaction cannot be answered privately, so a member without the required role
  is silently ignored (the panel footer states the requirement); in `single` mode the member's
  previous reaction stays on the message (the SDK cannot remove another user's reaction).

| Feature | Test |
|---|---|
| Panels posted at start, reactions added by the bot | rt/ReactionRoles |
| Buttons, single mode swap; select menu with a required role | rt/ReactionRoles |
| Reaction add/remove → role add/remove | rt/ReactionRoles |
| `/role-panel` idempotent, a deleted panel posted again, ManageRoles required | rt/ReactionRoles |
| Role above the bot: explained, current role kept | rt/ReactionRoles (`AdminSetRolePositions`) |
| Panel edited in place on a config change, moved panel | unit tests |

### audit-log

Server events in a staff channel, in compact embeds (one line per event, `<t:…>`
timestamps, no pings).

- **Events** (`events.*`): `joins` (with the account age, ⚠️ under 7 days), `leaves`,
  `roles`, `nicknames`, `messageEdits` (off by default: Discord reports link previews as
  edits), `messageDeletes` (the author when the message was seen since the last start),
  `moderation` (subscription `moderation.case`) and `polls` (subscription `polls.closed`,
  off by default). `ignoredChannels` (message events) and `ignoreBots`.
- **Channels**: `channel` (required), and `channels.members|messages|plugins` to send a
  category elsewhere.
- **Rate limiting**: events are queued in KV and posted together `batchSeconds` (default
  10) after the first one, by a single task per batch; a batch posts at most `maxPerBatch`
  (default 60) events and counts the rest ("…and N more events not posted"). A `@every 1m`
  sweep posts a batch whose task was lost. `batchSeconds: 0` posts each event at once.
- **Capabilities**: `discord.messages.send`, `discord.channels.read`, `storage.kv`,
  `scheduler`, `events.subscribe`. Intents: `ServerMembers` (privileged), `ServerMessages`
  (no `MessageContent`: the log never shows what people wrote).
- **`moderation.case`**: the payload is the moderation plugin's `Case` (`number`, `action`,
  `member {id, name}`, `moderator {id, name} | null`, `reason`, `durationMinutes`),
  published by the moderation plugin for every case (capability `events.publish`).

| Feature | Test |
|---|---|
| Joins, role changes, deletion with the author, `polls.closed`, a `/warn` case (`moderation.case`), one batched message | rt/AuditLog |
| Flush task per batch, `maxPerBatch` cut, sweep, per-category channels, `batchSeconds: 0` | unit tests |
| Nickname changes, edits, `moderation.case`, categories turned off | unit tests |

### announcements

Scheduled announcements from the config, and `/announce` for moderators.

- **Configured** (`announcements[]`): `name`, `channel`, `enabled`, exactly one of `cron`
  (5 fields or `@daily`…), `every` (`30m`, `6h`, `1d`, `2w`, at least 5 minutes, from
  `start`) or `at` (one-off `YYYY-MM-DD HH:MM`), `timeZone`, `content`, `embed`, `mentions`
  (`roles`, `everyone`, `here`). A `* * * * *` schedule evaluates them in their time zone and
  posts each due one once: the last run is kept in KV, so restarts, a second tick and the
  repeated hour of a DST change never post twice; runs missed while the bot was down are
  posted (once) if they are less than `catchUpMinutes` (default 10) old.
- **`/announce channel [when] [mention] [everyone]`** (ManageMessages) opens a form (title,
  message), then posts now or schedules a task (`when`: `in 2h`, `18:30`, `2026-12-24 18:00`
  in the plugin's time zone; up to 90 days, `maxScheduled` waiting at most).
  `/announce-list` shows the scheduled and configured ones (next run, invalid schedules);
  `/announce-cancel id` cancels. `channels` limits where `/announce` posts.
- **Mention safety**: only the configured mentions ping (`allowedMentions`); `@everyone` and
  `@here` need `allowEveryone` (admin) and are neutralized in the text otherwise; other role
  mentions in the text become plain names. `/announce` bodies go in an embed (never ping).
- **Time zones**: the plugin host has no `Intl`, so the plugin carries the offset and DST
  rule (EU, US, Australia, New Zealand) of the zones listed in its schema.
- **Capabilities**: `discord.messages.send`, `discord.channels.read`, `storage.kv`,
  `scheduler`, `portal.routes` (`GET /scheduled`, moderators). No intents.

| Feature | Test |
|---|---|
| Minute tick posts a configured announcement, `@everyone` neutralized | rt/Announcements |
| `/announce` → modal → task → posted; `/announce-list`; `/announce-cancel`; ManageMessages | rt/Announcements |
| `GET /scheduled` (moderator) | rt/Announcements, api/RouteAccess |
| Cron, interval, one-off, DST rules, catch-up, dedupe, invalid schedules, `when` parsing | unit tests |

## Not testable on the simulator

- User messages with **attachments**: the simulator cannot post them (the `attachment`
  command option works). Blanking of attachments without MessageContent is covered by
  `internal/plugin` TestMessageContentGating.
- Reply references (`message.reply`): the simulator validates the referenced message but
  does not keep the reference on the reply.
- Real Discord rate limits, the 3 s deadline under real latency, privileged intents not
  enabled in the developer portal, role hierarchy with a bot that is not Administrator.

## Checklist on real Discord

Deploy `examples/showcase` (put the real `discordId` / `applicationId`, enable the
Server Members and Message Content intents in the developer portal, invite the bot with
Administrator, then try once more without Administrator but with Manage Channels, Manage
Roles, Manage Messages, Moderate/Kick/Ban Members). Use a second account for the "member"
steps.

1. **Reconcile**: `cicord apply` creates the roles, categories and channels of
   `servers/showcase.yaml` (no announcement channel, so no Community feature needed).
2. **Tickets**: as the owner run `/ticket-panel` (then `/ticket-panel channel:#general`);
   check the reply is ephemeral and the member account cannot see the command. As the
   member click **Open a ticket**, fill the modal; check the ephemeral "Your ticket is
   open" (after a short "thinking"), the `ticket-1` channel under *Support*, visible to
   the member, Support and Moderator but not to a third account; the welcome pings the
   member and @Support. Post a few messages, run `cicord sync`/wait for a resync: the
   channel stays. Click **Close** → ephemeral confirmation → **Close ticket**: the summary
   lands in #ticket-log (message count, participants), the channel is deleted. Repeat with
   a bot **without Administrator** (the `member:<bot>` overwrite must keep its access).
3. **Polls**: `/poll question:"Game night?" choices:"Fri | Sat | Sun" hours:0.05 ping:@Member
   thread:True` in #polls: message with 3 buttons + Close, @Member pinged, thread created.
   Vote from two accounts (results update live, private "Your vote" follow-up, clicking
   again removes it). Wait 3 minutes: the poll closes itself (buttons removed, 🏆). Start
   another with `style:Select menu multiple:True`, vote with the select, close it with
   **Close poll** (a non-author gets "Only the author…").
4. **Levels**: chat from the member account (one message per minute earns 15 XP; "ok"
   earns nothing); react 👍 to their message from another account (+2 XP, removed when the
   reaction is removed). Reach level 1 (≈7 messages, or lower `xpPerMessage`): announcement
   in #level-ups and 🎉 on the message; rewards at levels 5/10/25. `/rank` (ephemeral, link
   opens the portal signed in), `/rank member:@x`, `/top limit:3 role:@Moderator`. After a
   poll closes, voters get +10 XP. Next day at 09:00 UTC the recap appears in #level-ups.
5. **Moderation**: as a moderator `/warn member:@x reason:spam evidence:<image>`: case in
   #mod-log with the image, DM to the member; the 3rd warning times them out for 1 h
   automatically. `/warnings member:@x`, `/timeout member:@x duration:5 minutes`,
   `/timeout … duration:Remove the timeout`, `/kick` (DM received before the kick, "left"
   logged), `/ban delete_messages:True`. Change a nickname/role: logged. Post 6 messages in
   5 s from a non-staff account: timed out 5 min, last message deleted. Set
   `webhook.url` to a real Slack or Discord webhook: every case is mirrored.
6. **Portal routes**: with the portal, `GET …/levels/leaderboard` anonymously,
   `…/levels/me` as a member, `…/tickets/tickets` and `…/moderation/warnings/<id>` as a
   moderator, `DELETE …/moderation/warnings/<id>` as an admin (403 for a moderator).
7. **Subcommands and autocomplete**: type `/polls close poll:` as the author of an open poll:
   the suggestions list your open polls (`#<n> <question>`) and narrow while you type; a
   member who opened no poll sees none, a Moderator sees them all. Pick one: the poll message
   closes (🏆) and the ephemeral reply says "Poll #n is closed.". `/polls results poll:` lists
   every poll and answers with the current tally. Check the suggestions appear within the
   3 s deadline over real latency.
8. **Re-invite**: kick the bot from the server and invite it again: the runtime logs "bot
   joined a server" and the slash commands are available again without a push.
9. **Hot reload**: change `defaultHours` in `bots/cicord/bot.yaml` and push: the bot does not
   reconnect, open polls keep their votes and still close on time.
10. **Reaction roles**: the three panels appear in #roles at start. As the member pick Red then
   Blue (Red goes away), pick Events in the select (refused without the Member role), react
   ⛏️ on the games panel and remove it. Change a panel title and push: the message is edited,
   not posted again. Without Administrator, put a panel role above the bot's role: the click
   explains the hierarchy.
11. **Audit log**: join, change a role, delete a message, close a poll: a single batch lands in
   #audit-log about 10 s later.
12. **Announcements**: `/announce channel:#general when:"in 2 min" mention:@Events` → form →
    posted two minutes later with only @Events pinged; `/announce-list`, `/announce-cancel`.
    Set the `game-night` cron to a minute or two ahead (Europe/Paris time): it posts once, at
    that minute.
