---
title: SDK coverage
description: Every surface of the plugin SDK, the official plugin that uses it, the end-to-end test that drives it through the real runtime, and the checklist to verify it on real Discord.
---

Every surface of [`spec/sdk/cicord-sdk.d.ts`](https://github.com/cicord/cicord/blob/main/spec/sdk/cicord-sdk.d.ts),
the plugin of [`examples/showcase`](https://github.com/cicord/cicord/tree/main/examples/showcase)
that uses it, and the end-to-end test that drives it through the real runtime on the Discord
simulator (reconciler, bot host, plugin host, scheduler, SQLite store). The last column tracks
the manual check on real Discord: **to verify** unless noted; the
[checklist](#checklist-on-real-discord) at the end lists the precise steps.

## Where the tests are

The Go tests run with `go test ./...`:

- **`rt/<Name>`**: `TestShowcase<Name>` in `internal/runtime/showcase_test.go` and
  `showcase_more_test.go`. The probe fixture (`internal/runtime/testdata/probe`) covers error paths a
  real plugin never hits on purpose.
- **`api/…`**: `internal/api/showcase_test.go` (`TestShowcaseRouteAccess`): portal routes
  through the HTTP API, which enforces the access levels.
- **`cli/…`**: `internal/cli/plugin_test.go` (`TestShowcasePlugins`): `cicord plugin build`,
  `typegen --check` and `cicord plugin test` for every showcase plugin.

The unit tests of each plugin, written with [`@cicord/sdk/testing`](../testing/#unit-tests-with-cicordsdktesting),
live in `examples/showcase/plugins/*/test` (`pnpm test`) and in the official registry's
`plugins/*/test`. github-releases is not in the showcase: its unit tests cover it.

## Events

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `memberJoin` | moderation (audit log) | rt/Moderation ("joined") | to verify |
| `memberLeave` | moderation | rt/Moderation (after `/kick`) | to verify |
| `memberUpdate` (with `previous`) | moderation (nickname, roles) | rt/Moderation (nickname change) | to verify |
| `messageCreate` **with** MessageContent | levels (short messages earn nothing) | rt/Levels | to verify |
| `messageCreate` **without** MessageContent (content blanked per plugin, even when another plugin of the bot declares it; mention exemption) | tickets (activity), moderation (flood), probe | rt/ProbeErrorsAndGating, rt/Tickets, rt/Moderation; `internal/plugin` TestMessageContentGating | to verify |
| `reactionAdd` / `reactionRemove` (partial message: id and channel only) | levels (author XP via `msg:<id>` KV) | rt/Levels | to verify |
| `messageDelete` (id and channel only) | audit-log (author remembered from `messageCreate`) | rt/AuditLog | to verify |
| `messageUpdate` | audit-log (`events.messageEdits`, off by default) | audit-log unit tests, `internal/plugin` unit tests | to verify |
| `reactionAdd` / `reactionRemove` on a plugin's own message | reaction-roles (reaction panels) | rt/ReactionRoles | to verify |
| `serverJoin` / `serverLeave` (a real join vs the lazy `GUILD_CREATE` after `READY` or after an outage; a leave is a `GUILD_DELETE` without `unavailable`) | none | `internal/plugin` TestServerEvents, `disgoadapter` TestTranslateServerJoinLeave, `internal/sim` TestServerJoinLeaveEvents | to verify |
| Slash commands registered again after a re-invite (`serverJoin`) | all | `internal/runtime` TestCommandsReRegisteredOnServerJoin | to verify |

## Commands

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `string` option (and `choices`) | polls `question`, `choices`, `style` | rt/Polls | to verify |
| `integer` option (`min`/`max`) | levels `/top limit` | rt/Levels | to verify |
| `integer` option with `choices` | moderation `/timeout duration` (0 removes the timeout) | rt/Moderation | to verify |
| `number` option | polls `hours` (2.5) | rt/Polls | to verify |
| `boolean` option | polls `multiple`, `thread`; moderation `/ban delete_messages` | rt/Polls, rt/Moderation | to verify |
| `user` option | levels `/rank member`, moderation `/warn`, `/kick`… | rt/Levels, rt/Moderation | to verify |
| `channel` option | tickets `/ticket-panel channel` | rt/Tickets | to verify |
| `role` option | levels `/top role` | rt/Levels | to verify |
| `mentionable` option | polls `ping` (a member, or a role; a non-mentionable role only by a poll manager: `managerRoles`, Administrator or Manage Server) | rt/Polls; polls unit tests | to verify |
| `attachment` option | moderation `/warn evidence` (image in the case embed) | rt/Moderation | to verify |
| `permissions` (default member permissions) | tickets `/ticket-panel` (ManageChannels), moderation (ModerateMembers, KickMembers, BanMembers) | rt/Tickets, rt/Moderation (a member is refused) | to verify |
| The invoker's role hierarchy (Discord only checks the bot's): the owner passes, an owner target is refused, staff (`spec.access`) only by an admin, highest role strictly above the target's, unverifiable refused | moderation `/warn`, `/timeout`, `/kick`, `/ban` | moderation unit tests; rt/Moderation (a moderator cannot kick an admin) | to verify |
| `ephemeral: true` default (a reply without the flag is ephemeral) | levels `/rank`, tickets, polls, moderation | rt/Levels, rt/AutoDefer, rt/Tickets | to verify |
| Subcommands (`subcommand()`, `interaction.subcommand`, `ephemeral` on the command) | polls `/polls close`, `/polls results` | rt/Polls; polls unit tests; `internal/plugin` TestSubcommandDispatch | to verify |
| Subcommand groups (`interaction.group`) | none | `internal/plugin` TestSubcommandDispatch, `internal/sim` TestInvokeSubcommand, SDK tests | to verify |
| `autocomplete` (string option, filtered by the typed text and the member) | polls `/polls close poll`, `/polls results poll` | rt/Polls; polls unit tests; `internal/plugin` TestAutocompleteDispatch (invalid, failing and slow handlers give an empty list) | to verify |
| `minLength` / `maxLength` (string option) | polls `poll` (`maxLength: 100`) | `internal/sim` TestInvokeSubcommand, `internal/plugin` TestInspectSubcommands | to verify |

## Components, modals and the interaction API

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| Buttons | tickets `open`, `close:<n>`; polls `vote:<poll>:<choice>`, `end:<poll>` | rt/Tickets, rt/Polls | to verify |
| Ids with data (`vote:42` routed to `components.vote`) | polls, tickets | rt/Polls, rt/Tickets | to verify |
| Select menu (several values) | polls `pick:<poll>` | rt/Polls | to verify |
| Link button | levels `/rank` (portal link) | rt/Levels | to verify |
| Buttons on an ephemeral message | tickets close confirmation | rt/Tickets | to verify |
| `showModal` and modal submit (`fields`) | tickets `open` | rt/Tickets | to verify |
| `interaction.update()` | polls (live results), tickets (confirm, cancel) | rt/Polls, rt/Tickets | to verify |
| `interaction.defer({ ephemeral })`, then `reply()` edits it | tickets modal submit | rt/Tickets | to verify |
| `interaction.followUp()` (ephemeral, after `update()`) | polls vote confirmation, moderation (DM failed, escalation) | rt/Polls | to verify |
| Auto-defer of a slow command (the reply edits the deferral) | probe `/probe-slow` | rt/AutoDefer (`AutoDeferAfter` 100 ms) | to verify |
| Auto-defer of a slow component (silent deferred update) | probe `slow` button | rt/AutoDefer | to verify |

## Runtime services

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `schedules` (cron, UTC) | levels daily recap `0 9 * * *` | rt/Levels (`sched.runCron` two days ahead) | to verify (wait for 09:00 UTC or use a `@every 1m` copy) |
| `ctx.schedule.after` and `tasks` | polls auto-close | rt/Polls (`sched.runTasks` in the future) | to verify |
| `ctx.schedule.cancel` | polls closed by hand | rt/Polls (task gone from the store) | to verify |
| A task scheduled before a hot reload fires after it | polls | rt/Polls | to verify |
| KV `get` / `set` / `delete` / `increment` / `list` | all | rt/* | n/a (runtime only) |
| KV `ttlSeconds` (expiry) | moderation warnings, levels cooldown | rt/Moderation (warnings expire after about 2 s) | n/a |
| KV persists across a hot reload (new revision) | polls (poll #2 closed after the reload) | rt/Polls | n/a |
| `http.fetch` to an allow-listed host | moderation webhook (`hooks.slack.com`) | rt/Moderation (TLS `httptest` server via `Options.PluginHTTPClient`) | to verify with a real Slack or Discord webhook |
| `http.fetch` egress refusals (allow-list, private addresses, redirects) | none | `internal/plugin` fetch tests | n/a |
| `events.publish` and `subscriptions` (`polls.closed`) | polls → levels bonus XP | rt/Polls | n/a |
| `ctx.portal.link` | levels `/rank` | rt/Levels (signed `cicord_token`) | to verify (open the link) |
| Portal route `public` | levels `GET /leaderboard`, polls `GET /polls/:id` | api/RouteAccess, rt/Levels, rt/Polls | to verify with the portal |
| Portal route `member` | levels `GET /me` | api/RouteAccess (401 anonymous, 403 non-member) | to verify with the portal |
| Portal route `moderator` | tickets `GET /tickets`, moderation `GET /warnings/:userId` | api/RouteAccess (403 member), rt/Tickets | to verify with the portal |
| Portal route `admin` | moderation `DELETE /warnings/:userId` | api/RouteAccess (403 moderator) | to verify with the portal |
| `ctx.log`, `console.*` → runtime log | all, probe | rt/AutoDefer (`console.log` captured) | n/a |
| `setTimeout` and timers | probe | rt/AutoDefer | n/a |

## Server, channels, members and messages

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `server.channel(key or { id })`, `server.role()` | all | rt/* | to verify |
| `server.member()` | moderation, levels | rt/Moderation, rt/Levels | to verify |
| `role.members()` (members holding a role; ServerMembers intent; shared 60 s listing patched by member events) | tickets 0.2.0 `GET /staff` (registry and bundled copy) | `internal/plugin` TestRoleMembers*; `internal/runtime` TestMemberList*; `internal/sim` TestListMembers; `disgoadapter` TestListMembersPages; SDK role-members tests | to verify |
| `role.position`, `role.mentionable`, `server.ownerId`, `member.permissions` (server-level, no channel overwrites), `member.access` | moderation (hierarchy), polls (ping) | `internal/plugin` TestMemberPermissionsAccessAndRolePositions, TestOwnerAndAdminAccess; `disgoadapter` TestRoleAndServerFromJSON; SDK permission tests | to verify |
| `allowedMentions` id lists (`roles: [id]`, `users: [id]`, `repliedUser`; ≤ 100 snowflakes; a type never both parsed and listed) | polls `ping` (only the pinged member or role) | rt/Polls (`sim` MessageMentions); `internal/plugin` TestAllowedMentionIDLists; `internal/sim` TestAllowedMentionsResolution; `disgoadapter` TestAllowedMentionsToWire; `internal/discord` TestAllowedMentionsValidate | to verify |
| `server.createChannel` (category, topic, overwrites `@everyone` / role key / `member:<id>`) | tickets | rt/Tickets (visibility per member) | to verify |
| A plugin-owned channel is never pruned (`prune: true`) and its overwrites are kept by `SyncServer` | tickets | rt/Tickets | to verify |
| `server.deleteChannel` (ownership record removed) | tickets close | rt/Tickets | to verify |
| `channel.send` | all | rt/* | to verify |
| `channel.messages()` (history) | tickets summary | rt/Tickets | to verify |
| `channel.message(id)` (one message; `undefined` when deleted) | polls task and `/polls close`, reaction-roles panels | rt/Polls, rt/ReactionRoles; `internal/plugin` TestChannelMessageOp | to verify |
| `channel.createThread({ fromMessage })` | polls `thread` | rt/Polls | to verify |
| `message.reply` | probe (bot mentioned), levels (no announce channel) | rt/ProbeErrorsAndGating | to verify |
| `message.edit` (own message, from a task) | polls auto-close | rt/Polls | to verify |
| `message.delete` | moderation flood | rt/Moderation | to verify |
| `message.react` | levels 🎉 | rt/Levels | to verify |
| `member.addRole` / `removeRole` / `hasRole` | levels rewards (`keep: false` replaced) | rt/Levels | to verify |
| `member.timeout` (0 removes it) | moderation | rt/Moderation | to verify |
| `member.kick` / `member.ban` | moderation | rt/Moderation | to verify |
| `member.send` (DM, sent before a kick) | moderation | rt/Moderation | to verify |
| `member.setNickname` | none | `internal/plugin` unit tests | to verify |

## Errors and limits

| Feature | Plugin | Test | Real Discord |
|---|---|---|---|
| `CapabilityError` (`instanceof`, `.capability`) | probe `/probe-cap` | rt/ProbeErrorsAndGating | n/a |
| A handler error gives the generic ephemeral reply | probe `/probe-throw` | rt/ProbeErrorsAndGating | to verify |
| Sync budget: a runaway loop is aborted, the VM keeps serving | probe `/probe-spin` then `/probe-ping` | rt/ProbeErrorsAndGating | n/a |
| Memory limit or killed code: only the instance crashes, shows `Degraded`, restarts with backoff and serves again | probe | rt/ProbeCrashRestarts | n/a |
| Startup budget, handler timeout, `onStop` bound | none | `internal/plugin` unit tests | n/a |
| Channel isolation (other servers) | none | `internal/plugin` unit tests | n/a |
| Config defaults, including array items and `$ref` (and typegen agrees) | moderation `escalation[].durationMinutes`, levels `rewards[].keep`, onboarding `steps[].multiple` | rt/Moderation, rt/Levels; `internal/config` TestApplyDefaultsArraysAndRefs; `internal/typegen` TestDefaultsConformance | n/a |

## reaction-roles, audit-log and announcements

These three [official plugins](../official/) are configured in the showcase too; their unit
tests are in `examples/showcase/plugins/<name>/test`.

| Plugin | Feature | Test |
|---|---|---|
| reaction-roles | Panels posted at start, reactions added by the bot | rt/ReactionRoles |
| reaction-roles | Buttons and the `single` mode swap; a select menu with a required role | rt/ReactionRoles |
| reaction-roles | Reaction added or removed → role added or removed | rt/ReactionRoles |
| reaction-roles | `/role-panel` is idempotent, posts a deleted panel again, needs ManageRoles | rt/ReactionRoles |
| reaction-roles | A role above the bot: explained, the current role kept | rt/ReactionRoles (`AdminSetRolePositions`) |
| reaction-roles | A panel edited in place on a config change, a moved panel | unit tests |
| audit-log | Joins, role changes, a deletion with its author, `polls.closed`, a `/warn` case (`moderation.case`), one batched message | rt/AuditLog |
| audit-log | One flush task per batch, the `maxPerBatch` cut, the sweep, per-category channels, `batchSeconds: 0` | unit tests |
| audit-log | Nickname changes, edits, `moderation.case`, categories turned off | unit tests |
| announcements | The minute tick posts a configured announcement, `@everyone` neutralized | rt/Announcements |
| announcements | `/announce` → modal → task → posted; `/announce-list`; `/announce-cancel`; ManageMessages | rt/Announcements |
| announcements | `GET /scheduled` (moderator) | rt/Announcements, api/RouteAccess |
| announcements | Cron, interval, one-off, daylight-saving rules, catch-up, dedupe, invalid schedules, `when` parsing | unit tests |

## Not testable on the simulator

- User messages with **attachments**: the simulator cannot post them (the `attachment` command
  option works). Blanking attachments without MessageContent is covered by `internal/plugin`
  TestMessageContentGating.
- Reply references (`message.reply`): the simulator checks the referenced message but does not
  keep the reference on the reply.
- Real Discord rate limits, the 3-second deadline under real latency, privileged intents not
  enabled in the developer portal, and the role hierarchy of a bot that is not Administrator.

## Checklist on real Discord

Deploy `examples/showcase`: put the real `discordId` and `applicationId`, enable the Server
Members and Message Content intents in the developer portal, invite the bot with
Administrator, then try once more without Administrator but with Manage Channels, Manage
Roles, Manage Messages, Moderate Members, Kick Members and Ban Members. Use a second account
for the "member" steps.

1. **Reconcile**: `cicord apply` creates the roles, categories and channels of
   `servers/showcase.yaml` (no announcement channel, so no Community feature needed).
2. **Tickets**: as the owner run `/ticket-panel` (then `/ticket-panel channel:#general`);
   check the reply is ephemeral and the member account cannot see the command. As the member
   click **Open a ticket** and fill the modal; check the ephemeral "Your ticket is open" (after
   a short "thinking"), and the `ticket-1` channel under *Support*, visible to the member,
   Support and Moderator but not to a third account; the welcome pings the member and
   @Support. Post a few messages, run `cicord sync` or wait for a resync: the channel stays.
   Click **Close** → ephemeral confirmation → **Close ticket**: the summary lands in
   #ticket-log (message count, participants) and the channel is deleted. Repeat with a bot
   **without Administrator** (the `member:<bot>` overwrite must keep its access).
3. **Polls**: `/poll question:"Game night?" choices:"Fri | Sat | Sun" hours:0.05 ping:@Member
   thread:True` in #polls as a Moderator (a poll manager): a message with 3 buttons and Close,
   @Member pinged (and only @Member: the author named in the text gets no notification), a
   thread created. From the member account, `ping:@Member` (not mentionable) is refused
   ephemerally and nothing is posted; `ping:@Events` (make it mentionable) and
   `ping:@someone` work. Vote from two accounts (results update live, a private "Your vote"
   follow-up, clicking again removes the vote). Wait 3 minutes: the poll closes itself
   (buttons removed, 🏆). Start another with `style:Select menu multiple:True`, vote with the
   select, close it with **Close poll** (a non-author gets "Only the author…").
4. **Levels**: chat from the member account (one message per minute earns 15 XP; "ok" earns
   nothing); react 👍 to their message from another account (+2 XP, removed when the reaction
   is removed). Reach level 1 (about 7 messages, or lower `xpPerMessage`): an announcement in
   #level-ups and 🎉 on the message; rewards at levels 5, 10 and 25. `/rank` (ephemeral, the
   link opens the portal signed in), `/rank member:@x`, `/top limit:3 role:@Moderator`. After
   a poll closes, voters get +10 XP. The next day at 09:00 UTC the recap appears in
   #level-ups.
5. **Moderation**: as a moderator, `/warn member:@x reason:spam evidence:<image>`: the case in
   #mod-log with the image, a DM to the member; the 3rd warning times them out for 1 h
   automatically. `/warnings member:@x`, `/timeout member:@x duration:5 minutes`,
   `/timeout … duration:Remove the timeout`, `/kick` (DM received before the kick, "left"
   logged), `/ban delete_messages:True`. Change a nickname or a role: logged. Post 6 messages
   in 5 s from a non-staff account: timed out 5 min, last message deleted. Set `webhook.url`
   to a real Slack or Discord webhook: every case is mirrored. Role hierarchy: as a Moderator,
   `/kick` an Admin or a Support member (staff), the owner, and a member whose highest role is
   at or above Moderator: each is refused ephemerally and nothing happens; as an Admin,
   `/timeout` a Moderator works; as the owner (even without roles), `/kick` an Admin works.
6. **Portal routes**: with the portal, `GET …/levels/leaderboard` anonymously, `…/levels/me`
   as a member, `…/tickets/tickets` and `…/moderation/warnings/<id>` as a moderator,
   `DELETE …/moderation/warnings/<id>` as an admin (403 for a moderator).
7. **Subcommands and autocomplete**: type `/polls close poll:` as the author of an open poll:
   the suggestions list your open polls (`#<n> <question>`) and narrow while you type; a
   member who opened no poll sees none, a Moderator sees them all. Pick one: the poll message
   closes (🏆) and the ephemeral reply says "Poll #n is closed.". `/polls results poll:` lists
   every poll and answers with the current tally. Check the suggestions appear within the
   3-second deadline over real latency.
8. **Re-invite**: kick the bot from the server and invite it again: the runtime logs "bot
   joined a server" and the slash commands are available again without a push.
9. **Hot reload**: change `defaultHours` in `bots/cicord/bot.yaml` and push: the bot does not
   reconnect, open polls keep their votes and still close on time.
10. **Reaction roles**: the three panels appear in #roles at start. As the member pick Red then
    Blue (Red goes away), pick Events in the select (refused without the Member role), react
    ⛏️ on the games panel and remove the reaction. Change a panel title and push: the message
    is edited, not posted again. Without Administrator, put a panel role above the bot's role:
    the click explains the hierarchy.
11. **Audit log**: join, change a role, delete a message, close a poll: a single batch lands in
    #audit-log about 10 s later.
12. **Announcements**: `/announce channel:#general when:"in 2 min" mention:@Events` → form →
    posted two minutes later with only @Events pinged; `/announce-list`, `/announce-cancel`.
    Set the `game-night` cron to a minute or two ahead (Europe/Paris time): it posts once, at
    that minute.
