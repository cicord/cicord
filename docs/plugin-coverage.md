# Plugin SDK coverage

Every surface of [`spec/sdk/codi-sdk.d.ts`](../spec/sdk/codi-sdk.d.ts), the plugin of
[`examples/showcase`](../examples/showcase/) that uses it, and the end-to-end test that drives
it through the real runtime on the Discord simulator (reconciler, bot host, plugin host,
scheduler, SQLite store). The last column tracks the manual check on real Discord.

Tests (Go, run by `go test ./...`):

- `rt/…`: `internal/runtime/showcase_test.go` (`TestShowcase*`). The probe fixture
  (`internal/runtime/testdata/probe`) covers error paths a real plugin never hits on purpose.
- `api/…`: `internal/api/showcase_test.go` (`TestShowcaseRouteAccess`): portal routes
  through the HTTP API, which enforces the access levels.
- `cli/…`: `internal/cli/plugin_test.go` (`TestShowcasePlugins`: `codi plugin build`,
  `typegen --check` and `codi plugin test` for every showcase plugin).
- Unit tests of each plugin with `@codi/sdk/testing` live in
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
| `messageUpdate`, `messageDelete` | — (no showcase use) | `internal/plugin` unit tests only | to verify on real Discord |

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
| `ctx.portal.link` | levels `/rank` | rt/Levels (signed `codi_token`) | to verify on real Discord (open the link) |
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
| `channel.messages()` (history; `before` to fetch a message by id) | tickets summary, polls task | rt/Tickets, rt/Polls | to verify on real Discord |
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

1. **Reconcile**: `codi apply` creates the roles, categories and channels of
   `servers/showcase.yaml` (no announcement channel, so no Community feature needed).
2. **Tickets**: as the owner run `/ticket-panel` (then `/ticket-panel channel:#general`);
   check the reply is ephemeral and the member account cannot see the command. As the
   member click **Open a ticket**, fill the modal; check the ephemeral "Your ticket is
   open" (after a short "thinking"), the `ticket-1` channel under *Support*, visible to
   the member, Support and Moderator but not to a third account; the welcome pings the
   member and @Support. Post a few messages, run `codi sync`/wait for a resync: the
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
7. **Hot reload**: change `defaultHours` in `bots/codi/bot.yaml` and push: the bot does not
   reconnect, open polls keep their votes and still close on time.
