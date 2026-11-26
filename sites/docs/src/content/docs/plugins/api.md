---
title: Plugin API
description: definePlugin and the ctx object - events, slash commands, context menus, interactions, components, portal routes and case links, schedules and tasks, plugin-owned channels, overlays, cross-plugin events, KV, rankings, files, search, HTTP and member privacy.
---

A plugin default-exports `definePlugin({...})` from `@cicord/sdk`. The normative contract is
[`spec/sdk/cicord-sdk.d.ts`](https://github.com/cicord/cicord/blob/main/spec/sdk/cicord-sdk.d.ts);
this page is a guided tour.

```ts
import { command, definePlugin } from "@cicord/sdk";

export default definePlugin({
  onStart(ctx) {},          // instance start (new revision, restart); not run in provider-call realms
  onStop(ctx) {},           // before stop; bounded (3 s)
  events: {},               // gateway events
  commands: {},             // slash commands
  components: {},           // buttons and select menus
  modals: {},               // modal submissions
  contextMenus: {},         // user and message context menus
  schedules: {},            // cron jobs
  tasks: {},                // one-shot delayed jobs
  subscriptions: {},        // events from other plugins
  queries: {}, actions: {}, // methods other plugins call
  routes: {},               // community portal HTTP routes
  privacy: {},              // export(ctx, userId) and erase(ctx, userId)
});
```

## The context

Every handler receives `ctx`:

| Member | What it is |
|---|---|
| `ctx.plugin` | `name`, `version`, `instance` |
| `ctx.bot` | `key`, `user` |
| `ctx.server` | The instance's server: `id`, `key`, `name`, `iconUrl`, `ownerId`, `preferredLocale`, `cicordBotIds` (the Discord user ids of every bot of the project on the server, to exempt them from sanctions), `channel()`, `role()`, `member()`, `members()`, channels, moderation, audit log, overlays… (below) |
| `ctx.config` | The effective config (overrides merged, defaults applied) |
| `ctx.log` | `debug`, `info`, `warn`, `error(message, fields?)` |
| `ctx.kv` | Instance-scoped storage, atomic transactions and rankings |
| `ctx.schedule` | `after()`, `cancel()` |
| `ctx.http` | `fetch()` |
| `ctx.events` | `publish()` |
| `ctx.plugins` | `has()`, `hasFeature()`, `query()`, `call()` ([Versioned events and plugin calls](../writing/#versioned-events-and-plugin-calls)) |
| `ctx.features` | `enabled(feature)`, `report(feature, active, reason?)` for the manifest's `spec.optional` features |
| `ctx.portal` | `link()`, `revoke()` |
| `ctx.i18n` | `t(key, vars?, { locale? })` ([translation catalogues](#translation-catalogues)) |
| `ctx.status` | `set(key, message \| null)`: a status line shown in Diagnostics (key 1–96 bytes, message ≤ 320 bytes, ≤ 32 lines) |
| `ctx.assets`, `ctx.files` | Packaged assets and repository files bound to the instance ([files and search](#files-and-full-text-search)) |
| `ctx.index(name)` | Full-text search index (`storage.index`) |
| `ctx.secrets` | `has(name)`, `ref(name)`: opaque handles for [bound credentials](../writing/#sending-a-webhook-with-a-bound-credential) |
| `ctx.render`, `ctx.media` | PNG rendering (**experimental**) and image hashing ([Attachments, assets and images](../writing/#attachments-assets-and-images)) |
| `ctx.analytics` | `summary({ from, to, granularity })` of the server's analytics (`analytics.read`) |
| `ctx.privacy` | `pseudonym(userId)` inside a privacy hook ([member privacy](#member-privacy)) |

Server resources are referenced by their **key** in `servers/<server>.yaml`
(`ctx.server.channel("general")`, `member.addRole("member")`), or by `{ id }` for resources
Cicord does not manage. `channel()` and `role()` throw when the resource does not exist.

| Object | Methods (capability in [Capabilities and grants](../writing/#capabilities-and-grants)) |
|---|---|
| `TextChannel` | `send(message)`, `messages({ limit?, before?, after? })`, `message(id)` (`undefined` once deleted), `pins()`, `typing()`, `sendAs(identity, message)`, `createThread(name, { private?, fromMessage? })`, `bulkDelete()`, `previewPurge()`, `purge()` ([moderation](#moderation)), `memberOverwrite(userId)`; `id`, `key`, `name`, `type`, `categoryId`, `mention` |
| `Message` | `reply(message)`, `edit(message)`, `delete(reason?)`, `react(emoji)`, `pin()`, `unpin()`, `crosspost()`, `reactionUsers()`, `removeReaction()`, `endPoll()`, `pollVoters()` ([message operations](#message-operations)); `content`, `author`, `member` (on received server messages only: Discord's REST message objects, so `channel.messages()`, `channel.message(id)` and the results of `send`/`reply`/`edit`, carry none; use `ctx.server.member(message.author.id)`), `channel`, `attachments`, `embeds`, `reactions`, `reference`, `poll`, `url`, `createdAt` |
| `Member` | `hasRole(role)`, `addRole(role, reason?)`, `removeRole(role, reason?)`, `setRoles(roles, reason?)`, `setNickname(nickname \| null, reason?)`, `timeout(durationMs, reason?)` (0 removes it), `removeTimeout(reason?)`, `kick(reason?)`, `ban({ reason?, deleteMessagesSeconds? })`, `send(message, { retractable? })` (DM), `moveTo(channel, reason?)` and `disconnect(reason?)` ([voice](#voice)), `permissionsIn(channel)`, `canView(channel)` ([audiences](#channel-audiences)); `user`, `nickname`, `displayName`, `roles`, `declaredRoles`, `joinedAt`, `pending`, `flags`, `premiumSince`, `timedOutUntil`, `avatarUrl` |
| `Role` | `members({ limit?, after? })` ([below](#members-of-a-role)); `id`, `key`, `name`, `color`, `mention`, `position`, `mentionable`, `permissions`, `managed`, `declaredMembers` |

### Permission checks

Discord checks a command's `permissions` and the **bot's** role hierarchy, not the
invoker's. To check what a member may do to another one, or which role they may ping:

| Field | What it is |
|---|---|
| `role.position` | Hierarchy position, higher is higher (`@everyone` is 0) |
| `role.mentionable` | Whether anyone may mention the role |
| `role.permissions` | The server-wide permissions the role grants: Discord's bitfield as a decimal string (`(BigInt(role.permissions) & (1n << 3n)) !== 0n` tests Administrator). Refuse a role whose permissions are unknown |
| `ctx.server.ownerId` | The owner's user id, read live (an ownership transfer is seen at once) |
| `member.permissions` | **Server-level** permissions: `@everyone` and every role; all for the owner and `Administrator`. Channel overwrites and timeouts are not applied: it is not what the member may do in a given channel |
| `member.access` | `"admin"` (owner, `spec.access.admins`), `"moderator"` (`spec.access.moderators`) or `"member"` |

They are `undefined` when the runtime does not know them (a role not cached yet, an older
engine): fail closed, never treat an unknown position or access level as one that passes.
The official `moderation` plugin is the reference: the owner may moderate anyone, nobody may
moderate the owner, staff (moderator or admin access) can only be moderated by an admin, and
the moderator's highest role must be strictly above the target's; anything it cannot verify
is refused.

### Members of a role

`role.members()` lists the members holding a role, for example the staff a ticket can be
assigned to:

```ts
const staff = await ctx.server.role("staff").members({ limit: 1000 });
const next = await ctx.server.role("staff").members({ limit: 1000, after: staff.at(-1)?.id });
```

| | |
|---|---|
| Signature | `role.members(options?: { limit?: number; after?: Snowflake }): Promise<Member[]>` |
| Order | Ascending user id; `after` returns the holders after that user id (to page past `limit`) |
| `limit` | Default 100, at most 1000 (an integer, else a `TypeError`) |
| Needs | The `discord.members.read` capability and the **`ServerMembers` intent** in plugin.yaml `spec.intents` |
| Errors | An unknown role key throws in `ctx.server.role()`; `@everyone` and a missing intent throw; a Discord refusal (Server Members Intent off in the developer portal) rejects |

Discord has no "members of a role" endpoint: the runtime lists the server's members
(`GET /guilds/{id}/members`, 1000 per request, at most 100,000 members: a larger server is an
error, not a partial list) and filters them.
The listing is shared by the plugins of the server, reused for up to 60 seconds and kept
current in between by member joins, updates and leaves, so repeated calls are cheap; the first
call on a large server takes one request per 1000 members (rate limited): it may outlast the
handler timeout, but the listing goes on and later calls are served from it. The intent cannot be seen in the
plugin's definition, so it is checked when `members()` is called; `cicord plugin build` does not
warn about a declared `ServerMembers` without member handlers when the plugin has
`discord.members.read`. In tests, `createTestContext({ intents: [...] })` applies the same
check.

`ctx.server.members({ limit, after })` pages through all the members the same way (same
listing, grant and intent; default 100, at most 1000). Members carry screening (`pending`),
onboarding and rejoin `flags`, `premiumSince`, `timedOutUntil` and their server avatar as
Discord supplies them; `user.createdAt` comes from the snowflake and `user.flags` lists the
public user flags. `interaction.locale` and `ctx.server.preferredLocale` carry Discord's
language tags when known.

### Channel audiences

With `discord.channels.read`, `member.canView(channel)` checks **View Channel** and
**Read Message History** using that handler's authenticated member or a member read in
that handler. `member.permissionsIn(channel)` returns the effective permissions. A
missing role, an unknown channel, a private thread with unverified membership or an
age-restricted channel without verified age eligibility denies the read. The portal
fetches a fresh member for each request, including when a staff role has just been
removed. Reads also require the current guild permissions from the bot’s gateway.
While the gateway is disconnected or catching up, access checks deny the read and
audience comparisons return `unknown`.

Before copying content, use `ctx.server.compareAudience(source, destination)`. Permit
the copy only for `same` or `narrower`. The comparison considers combinations of roles
and individual overrides, including access for future members. `unknown` includes
partially overlapping audiences and a restricted source with an unrestricted-age
destination. `ctx.server.audience(channel)` provides a summary for display.

### Moderation

With `discord.members.moderate`, `ctx.server.ban(userId, options)` can ban a user who has
left the server. `ctx.server.unban(userId, reason)` also succeeds when the ban has already
been lifted. Page through `ctx.server.bans({ limit, after })` in increasing user ID order
(up to 1000 per call). The `banAdd` and `banRemove` events require `ServerModeration`.
`member.removeTimeout(reason)` clears a timeout. `member.setRoles(roles, reason)` uses
`discord.roles.assign` and a single member update. Changes to roles with Git-declared
membership are refused, by `setRoles` and by `addRole`/`removeRole` alike (`role.declaredMembers`
tells them apart): to take such a role from a member temporarily, apply an overlay with
`members: { [id]: { suspendDeclaredRoles: true } }` ([below](#overlays-incidents-and-the-protection-inventory)).
Discord enforces the managing bot's role hierarchy.

A guild channel's `bulkDelete(ids, reason)` accepts 1–100 unique message IDs younger
than 14 days. `purge({limit, author, contains, bots, attachments, before, after, reason})`
examines at most `limit` messages (1–1000) newest first. Both methods require
`discord.messages.read` and `discord.messages.manage`. Text and attachment filters
also require the plugin's `MessageContent` intent. The result reports the counts of
deleted and skipped messages. A plugin should show the scope and obtain moderator
confirmation before a bulk action. Use `channel.previewPurge(filter)` to obtain the exact
message IDs without deleting them, then apply the approved IDs in bounded batches.

`ctx.server.sendDM(userId, message)` reaches a user who left or was banned (an appeal
notice, for example). A DM sent with `{ retractable: true }` keeps a receipt, so the instance
can later `ctx.server.retractDM(id)` or `editRetractableDM(id, message)`, also after a
restart. Both use `discord.dm.send`.

### Overlays, incidents and the protection inventory

YAML stays the source of truth: a plugin never edits managed resources, it applies a
temporary **overlay** that the reconciler layers over the declared state until it expires
(`until`, at most 7 days ahead) or is removed.

| Call | Capability | What it does |
|---|---|---|
| `ctx.server.overlays.apply({ id, reason, until, channels?, server?, members? })`, `remove(id)`, `list()` | `server.overlays` | Slowmode, overwrite denies or neutrals per channel (`"*"` for every managed channel), a raised verification level, `suspendDeclaredRoles` for a member. Targets are in [Incident overlays](../../reference/http-api/servers/#incident-overlays). |
| `ctx.server.incident({ invitesDisabledUntil?, dmsDisabledUntil? })` | `server.incident` | Pauses invites or DMs for up to 24 hours (the bot needs Manage Server). |
| `ctx.server.requestSync(reason)` | `server.sync` | Asks for a full sync of the active revision. |
| `ctx.server.protection()` | `discord.channels.read` | The server's `selfHeal`, `deletionProtection` and pause state, `totalResources`, the `unmanaged` roles and channels and the bot's top role position. |

`protection()` counts eligible roles and visible channels (categories included) and omits
integration roles, `@everyone`, plugin-owned resources and threads. An incomplete Discord or
state read returns `known: false` with an empty inventory: treat it as unverified.

### Translation catalogues

Set `spec.locales: ./locales` in the plugin manifest. Put a flat JSON object of strings in
each language file, with `en.json` as the fallback. Call `ctx.i18n.t("welcome.hello",
{name: member.displayName})` to substitute `{name}` once. The substituted text, and each
variable, may hold up to 64 KiB. A longer result throws a `TypeError` instead of
allocating it. An explicit locale option can
select the language for a particular member. Otherwise Cicord tries the configured
instance locale, the current interaction locale, the server locale and English, falling
back to the base language at each step.

Keys such as `commands.remind.name`, `commands.remind.description` and
`commands.remind.options.when.description` translate the Discord command UI. For
subcommands and groups, insert their canonical path before the field or options segment.
A fixed choice is translated by `<option key>.choices.<value>`, with the value as written in
the plugin: `commands.timeout.options.duration.choices.60` can be `1 heure` in `fr.json`
(1–100 characters). The handler still receives the value (`60`), never the label.
Use `contextMenus.Open ticket on message.name` to translate a USER or MESSAGE menu
named `Open ticket on message`. For example, its French value can be
`Ouvrir un ticket sur ce message`. Menu labels preserve spaces and capitalization and
must contain 1–32 characters. Each menu of the same type on one bot and server needs
a distinct display name in each locale, including menus using their default names.
Handlers continue to receive the canonical command and option names.

## Events

From the official `welcome` plugin (intent `ServerMembers`):

```ts
events: {
  async memberJoin(ctx, member) {
    const { channel, message, autoRole, dmOnboarding } = ctx.config;
    await ctx.server.channel(channel).send(render(message, { member: member.mention, server: ctx.server.name }));
    if (autoRole && !member.hasRole(autoRole)) await member.addRole(autoRole, "welcome: default role");
    if (dmOnboarding) {
      const url = await ctx.portal.link("/onboarding", { member });
      await member.send({ content: "Pick your roles:", components: [{ type: "row", components: [{ type: "link", label: "Start", url }] }] });
    }
    const total = await ctx.kv.increment("stats:joined");
    ctx.log.info("member welcomed", { member: member.id, total });
  },
},
```

| Handler | Receives | Intent |
|---|---|---|
| `memberJoin` | `member` | `ServerMembers` |
| `memberLeave` | `user`, `previous` (the member, when known) | `ServerMembers` |
| `memberUpdate` | `member`, `previous` | `ServerMembers` |
| `messageCreate` | `message` | `ServerMessages` (+ `MessageContent` for the text) |
| `messageUpdate` | `message` | `ServerMessages` |
| `messageDelete` | `{ id, channel }` | `ServerMessages` |
| `reactionAdd`, `reactionRemove` | `{ message, user, emoji }` (the message is partial: `message.channel.message(message.id)` fetches it) | `ServerMessageReactions` |
| `voiceStateUpdate` | `{ user, member, channel, previous, selfMute, selfDeaf, streaming, video }` ([voice](#voice)) | `ServerVoiceStates` |
| `serverJoin` | `{ id, name, iconUrl }` | none |
| `serverLeave` | `{ id }` | none |
| `directMessage` | `message` (with the validated `member` of the server) | `DirectMessages` |
| `banAdd`, `banRemove` | `{ user, reason? }` | `ServerModeration` |
| `auditLogEntry` | `AuditLogEntry` ([below](#structure-and-audit-events)) | `ServerModeration` |
| `channelCreate`/`Update`/`Delete`, `roleCreate`/`Update`/`Delete`, `threadCreate`/`Delete`, `serverUpdate`, `serverAvailable` | [structure events](#structure-and-audit-events) | none |
| `expressionsUpdate`, `webhooksUpdate`, `inviteCreate`/`Delete`, `automodAction` | their own payloads | `ServerExpressions`, `ServerWebhooks`, `ServerInvites`, `AutoModerationExecution` |
| `scheduledEventCreate`/`Update`/`Delete`, `scheduledEventUserAdd`/`Remove` | the event, or `{ eventId, userId }` | `ServerScheduledEvents` |
| `pollVoteAdd`, `pollVoteRemove` | `{ channelId, messageId, userId, answerId }` | `ServerMessagePolls` |

Events are delivered in order for each instance, and only to instances of the servers
declared for the bot: joining an undeclared server runs no plugin. Handlers marked "none"
need no intent (`Servers` is always enabled). A user who sends DMs faster than the host
routes them (a burst of 10, then one per second) is asked to retry and those DMs are not
delivered. `serverJoin` fires when the bot is
invited (again) while connected, not when servers load after a connection or recover from an
outage; `serverLeave` when it is kicked, banned, removed or the server is deleted, not during
a Discord outage. On the gateway, a join is a `GUILD_CREATE` for a server that was neither in
the last `READY` nor seen since (nothing is a join before the first `READY`), and a leave is a
`GUILD_DELETE` without `unavailable`. After a re-invite the runtime registers the server's
slash commands again.

### Structure and audit events

A channel or role event contains its resource, its previous state when available, and
`managed` and `willHeal` flags. Threads expose their parent. Server, expression, webhook,
invite and AutoMod updates carry their own payloads. Structure events do not identify
the actor. `ctx.server.invites()` reads current invite counts with `discord.invites.read`.
`ctx.server.scheduledEvents()` and `event.users()` need `discord.events.read`;
`createScheduledEvent()`, `event.edit()` and `event.delete()` need `discord.events.manage`.

To read the audit log, declare `discord.auditlog.read` and `ServerModeration`. The bot
needs View Audit Log. Subscribe to `auditLogEntry` for new entries or call
`ctx.server.auditLog({action, actorId, before, limit})`. Entries retain Discord's action,
actor, target, reason, options and changes. Permission changes include added and removed
permission names where both bitfields are available.

The runtime retains emitted-operation metadata for seven days, only for calls that Discord
audits. It journals a call before sending it when its target is known (and withdraws it
when Discord refuses the call), and a creation once Discord returns the new ID. One call is
journaled once per audit entry it causes: a channel update that sends overwrites also
records an overwrite operation for each role or member it sends, a member update that sets
roles and a nickname records both. A project bot's action is `confirmed` when the actor,
action type and target match a recorded operation within 30 seconds that no other entry has
claimed; two identical calls in the window confirm one entry each. An overwrite deletion is
matched with the call that replaced the overwrites (Discord's request does not name the
removed overwrite). A gateway entry that arrives before its proof is checked again 1.5 seconds
later. A missing match remains `probable`: the runtime then raises the "bot token possibly
compromised" alert (see [Alerts](../../self-hosting/monitoring/#alerts)). Only a check that ran
can raise it. Gateway entries wait in a queue of 4,096, which holds the burst of a large
first sync. An entry that does not fit is not checked and reaches `auditLogEntry` as
`unknown`. An own entry whose check failed (a state store error or timeout, also on the
second try) is `cicord` with certainty `unknown`. Neither raises the alert, and the runtime
logs one warning per burst with the number of entries it could not check. Actors of gateway
entries are looked up once per user and cached for 10 minutes, so a raid by one account costs
one Discord call. Reorders
(`channel.reorder`, `role.reorder`) are merged by Discord and never confirmed. Not verified
against Discord, hence not journaled: the welcome screen, the widget and incident actions. A human may enter
any audit reason. Use the attribution fields when reporting who acted, and treat missing
evidence as unknown.

## Message operations

| Call | Capability |
|---|---|
| `message.pin()`, `unpin()` (the bot needs Pin Messages), `removeReaction(emoji, userId?)` (without `userId`, the bot's own) | `discord.messages.manage` |
| `channel.pins()`, `message.reactionUsers(emoji, { limit, after })` (normal reactions, 1–100 per page), `message.pollVoters(answerId, after?)` (100 per page) | `discord.messages.read` |
| `message.crosspost()` (announcement channels), `channel.typing()`, `message.endPoll()` | `discord.messages.send` |
| `channel.sendAs({ username, avatarUrl }, message)`: posts through a webhook the runtime creates or reuses in the channel; the plugin only gets the message | `discord.webhooks.manage` |

A received message carries its `reactions` counts, its jump `url`, its reply `reference` and,
for a native poll (`poll` on send: `question`, `answers`, `durationHours`, `multiselect`), the
current `poll` results. For short retries of a channel send, pass the same `nonce` (up to 25
characters) with `enforceNonce: true`: Discord deduplicates it for a few minutes.

## Slash commands

Commands are keyed by name (lowercase letters, digits, `-` and `_`, 1–32 characters) and
synced to each server. The `command()` helper infers the type of `interaction.options`:

```ts
commands: {
  "welcome-preview": command({
    description: "Preview the welcome message", // 1–100 characters
    ephemeral: true,                             // ephemeral replies and auto-defer
    permissions: ["ManageServer"],               // default member permissions
    options: {
      member: { type: "user", description: "Member to greet (defaults to you)" },
    },
    async run(ctx, interaction) {
      const target = interaction.options.member ?? interaction.user; // User | undefined
      await interaction.reply({ content: `Welcome ${target.mention}!`, ephemeral: true });
    },
  }),
},
```

- Option `type`: `string`, `integer`, `number`, `boolean`, `user`, `channel`, `role`,
  `mentionable` or `attachment`.
- `required` options come first in Discord; `min`/`max` apply to `integer` and `number`;
  `minLength`/`maxLength` (0–6000, `maxLength` ≥ 1) to `string`; `choices` (≤ 25) to
  `string`, `integer` and `number`; `autocomplete` to `string`, `integer` and `number`, not
  together with `choices` ([below](#autocomplete)).
- At most 25 options per command. A command name defined twice on the same bot and server
  fails validation.

### Subcommands

`subcommands` (and `groups` of subcommands) turn a command into `/polls close`,
`/polls results`, `/polls admin purge`. Each `subcommand()` has its own typed `options` and
`run`; `interaction.subcommand` and `interaction.group` say which one runs. `ephemeral` and
`permissions` stay on the command.

```ts
import { command, subcommand } from "@cicord/sdk";

polls: command({
  description: "Manage polls",
  subcommands: {
    close: subcommand({
      description: "Close a poll",
      options: { poll: { type: "string", description: "Poll", required: true } },
      async run(ctx, interaction) { /* interaction.options.poll: string */ },
    }),
  },
  groups: { admin: { description: "Admin", subcommands: { purge: subcommand({ description: "Purge", run() {} }) } } },
}),
```

Discord's rules, checked when the plugin is built: a command with subcommands has no `run` or
`options` of its own (it cannot be invoked alone); at most 25 subcommands and groups per
command, with distinct names; a group holds only subcommands (1–25), and subcommands do not
nest further.

### Autocomplete

`autocomplete(ctx, interaction, focused)` on a `string`, `integer` or `number` option (not
with `choices`) suggests values while the user types. The `AutocompleteInteraction` carries
`user`, `member`, `channel`, `command`, `group` and `subcommand`, and `interaction.options`
the raw values of the other options already filled in (ids for users, roles and channels).
`focused` is `{ name, value }`, where `value` is the text typed so far (always a string, even
for `integer` and `number` options). Return at most 25 `{ name, value }` choices (names 1–100
characters, values of the option's type, strings ≤ 100 characters) within **2.5 s**: an
autocomplete cannot be deferred (Discord drops answers after 3 s), and a handler that throws,
is late or returns invalid choices shows an empty list (the error is logged). The user may
still send any text: validate it in `run`.

```ts
poll: {
  type: "string", description: "Poll", required: true,
  async autocomplete(ctx, interaction, focused) {
    const polls = await listPolls(ctx);
    return polls.filter((p) => p.question.includes(focused.value)).slice(0, 25)
      .map((p) => ({ name: `#${p.id} ${p.question}`, value: p.id }));
  },
},
```

## Interactions: reply, defer, follow up

Discord expects an answer within 3 seconds. If a handler has not answered after **2 s**, the
runtime **defers automatically** (ephemerally when the command says `ephemeral: true`;
components get a silent deferred update), and a later `reply()` edits the deferred response.
The first response fixes ephemerality; after `reply()`, use `followUp()`.
`interaction.replied` and `interaction.deferred` tell where the interaction stands. Autocomplete
interactions are the exception: they are never deferred, the handler's return value is the
answer. If a handler throws before answering, the user gets an ephemeral "Something went
wrong while handling this interaction." and the error is logged.

| State | `reply()` | `defer()` | `update()` (components) | `followUp()` | `showModal()` |
|---|---|---|---|---|---|
| unanswered | initial reply | defers | edits the component's message | error | opens the modal |
| deferred (command/modal) | edits the deferred reply | no-op | error | follow-up | error |
| deferred (component) | follow-up | no-op | edits the message | follow-up | error |
| updated | follow-up | error | edits the message | follow-up | error |
| replied | error (use `followUp`) | error | error | follow-up | error |

Follow-ups are accepted for 15 minutes.

### Long private commands: `deferTask`

A handler has 10 seconds. For longer work behind a server command, call
`interaction.deferTask(name, payload?)` (`scheduler`, payload ≤ 1 MiB): it defers ephemerally
and queues `tasks[name]`. The task's third argument carries `interaction`, whose
`reply(message)` edits the original ephemeral response; the reply credential stays encrypted
in the host for at most 14 minutes. The task runs once: an execution that may have started is
not repeated, and a retry only tries to post a private interruption notice.

```ts
commands: {
  report: command({ description: "Build my activity report", ephemeral: true,
    async run(ctx, it) { await it.deferTask("report", { userId: it.user.id }); } }),
},
tasks: {
  async report(ctx, payload, task) {
    const summary = await buildReport(ctx, (payload as { userId: string }).userId);
    await task?.interaction?.reply(summary);
  },
},
```

## Components and modals

Component and modal `id`s are local to the instance; on the wire the runtime namespaces them
as `c:<instance>:<id>`, which must fit Discord's 100-byte `custom_id` limit (handler keys ≤ 80
bytes). An id can **carry data after a colon**: the lookup falls back to the part before the
first `:`, so `"vote:42"` (or `"vote:42:yes"`) is routed to `components.vote`, and
`interaction.componentId` stays `"vote:42"`. The official `polls` and `tickets` plugins are
complete examples.

```ts
commands: {
  poll: command({
    description: "Start a poll",
    async run(ctx, interaction) {
      await interaction.reply({
        content: "Ready?",
        components: [{ type: "row", components: [
          { type: "button", id: "vote:yes", label: "Yes", style: "success" },
          { type: "button", id: "feedback", label: "Feedback…" },
        ] }],
      });
    },
  }),
},
components: {
  async vote(ctx, interaction) {           // receives "vote:yes"
    const [, answer] = interaction.componentId.split(":");
    await ctx.kv.increment(`poll:${answer}`);
    await interaction.update({ content: `Thanks, ${interaction.user.displayName}!`, components: [] });
  },
  async feedback(ctx, interaction) {
    await interaction.showModal({
      id: "feedback", title: "Feedback",
      fields: [{ id: "text", label: "Your feedback", style: "paragraph", required: true, maxLength: 500 }],
    });
  },
},
modals: {
  async feedback(ctx, interaction) {
    ctx.log.info("feedback", { text: interaction.fields.text ?? "" });
    await interaction.reply({ content: "Thanks!", ephemeral: true });
  },
},
```

- Buttons: `{ type: "button", id, label?, emoji?, style?, disabled? }` (`primary`,
  `secondary`, `success`, `danger`); link buttons `{ type: "link", url, label?, emoji?,
  disabled? }` trigger no handler.
- Select menus: `{ type: "select", id, placeholder?, minValues?, maxValues?, options }`, each
  option `{ value, label, description?, emoji?, default? }`; the picked values arrive in
  `interaction.values`.
- Entity selects: `{ type: "userSelect" | "roleSelect" | "channelSelect" | "mentionableSelect",
  id, placeholder?, minValues?, maxValues?, defaultValues?, channelTypes? }`.
- Modals: `{ id, title, fields }`, each field `{ id, label, style?, required?, placeholder?,
  value?, minLength?, maxLength? }` (`short` or `paragraph`); the values arrive in
  `interaction.fields`. A `{ type: "label", label, description?, component }` field wraps a
  select or a text input; the picked ids arrive in `interaction.selections[id]`.
- Messages: a string, or `{ content?, embeds?, components?, allowedMentions?, files?, poll?,
  nonce?, enforceNonce? }`, or Components V2 `{ layout }` (text, sections with an accessory,
  containers, media galleries, separators, files and rows; at most 40 components; exclusive
  with `content`, `embeds` and `components`). Interaction replies can add `ephemeral: true`.

### Context menus

`contextMenus` declares user and message menus, keyed by their name:

```ts
contextMenus: {
  "Open ticket on message": { type: "message", ephemeral: true,
    async run(ctx, it) { await it.reply(`Ticket opened about ${it.target.url}`); } },
},
```

`interaction.target` is the resolved `Member` or `Message`. A bot can register up to 15 menus
of each type per server, checked by `cicord validate` across its instances. Translate the
label with `contextMenus.<name>.name` in the [locale files](#translation-catalogues). To
support an older engine, declare menus only when `sdkFeatures?.contextMenus` is set.

### Mentions: `allowedMentions`

Without `allowedMentions`, every mention in the content notifies, including roles that are not
mentionable when the bot has MentionEveryone. Set it whenever the content contains text from a
member, or a mention the member picked:

```ts
await channel.send({
  content: `${role.mention} — new poll by ${interaction.user.mention}`,
  // Notifies exactly this role: not the author, not any other mention in the text.
  allowedMentions: { roles: [role.id], users: [] },
});
```

- `users` and `roles` take `true` (every mention of that type), `false` (none) or a list of
  **ids** (snowflakes, not resource keys) or of objects with an `id` (a `User`, `Member` or
  `Role`), at most 100 per list. A type cannot be both allowed and listed.
- `everyone` allows `@everyone` and `@here`; `repliedUser` notifies the author of the message
  replied to (`message.reply()`).
- A non-mentionable role, `@everyone` and `@here` still only notify when the bot has
  MentionEveryone in the channel. Invalid lists make the call throw.

The official `polls` plugin only lets poll managers ping a role that is not mentionable, and
notifies only the picked member or role.

## Portal routes

`routes` expose JSON endpoints to the community portal under
`/api/v1/portal/{server}/plugins/{bot}/{instance}/{path}`. They need `portal.routes`. Keys are
`"<GET|POST|PUT|PATCH|DELETE> /path"`, and `:name` segments are parameters.

`access` is required and enforced by the runtime: `public`, `member`, `moderator`, `admin`,
or `scoped` for a [case link](#portal-links) (anonymous callers of non-public routes get
`401`, an insufficient role `403`). The handler receives `request.viewer` (`{ user, member }`),
`request.params`, `request.query` (the first value of each parameter), `request.body` (JSON)
and, on scoped routes, `request.scope`. It returns any JSON value (`200`), an envelope
`{ status, body?, headers? }`, a raw `{ status, raw, contentType }` text response
(`text/calendar`, `text/csv`, `text/plain` or `text/markdown`), or nothing (`204`).

```ts
routes: {
  "GET /steps": {
    access: "member",
    async handler(ctx, request) {
      const member = request.viewer?.member;
      if (!member) return { status: 403, body: { error: { code: "forbidden", message: "members only" } } };
      return await ctx.kv.get(`done:${member.id}`) ?? { done: false };
    },
  },
},
```

A handler that returns both envelopes and plain objects should declare its return type
(`async handler(ctx, request): Promise<RouteResponse>`): TypeScript otherwise infers a union
that does not match `RouteResponse`. Interfaces are not assignable to `Json` (they have no
index signature): cast records with `as unknown as Json`, or use a [typed route](#typed-routes).
Plugin routes can only set a few headers (`Content-Disposition`, `ETag`, `Last-Modified`,
`x-*` except the reserved prefixes, `Cache-Control` on public routes…): never cookies,
redirects, CORS, security headers or the `x-*` headers a reverse proxy acts on. The status must be 200–599. The full path, the header list and the access rules are
in the [HTTP API reference](../../reference/http-api/portal/#plugin-routes).

### Typed routes

A plugin can publish the types of its routes so that portals get a typed client and its
handlers are checked against them. Write a **route contract**: a type-only, self-contained
file (no imports, only `interface` and `type` declarations, since it is copied verbatim into
portals) that exports `interface Routes`, and declare it in `plugin.yaml`:

```yaml title="plugin.yaml"
spec:
  routes:
    types: ./src/routes.ts
```

```ts title="src/routes.ts"
export interface PollResults { id: string; question: string; voters: number }

export interface Routes {
  "GET /polls/:id": { response: PollResults };                 // params come from the key
  "GET /polls": { query: { status?: "open" | "closed" }; response: { items: PollResults[] } };
  "POST /polls/:id/votes": { body: { choice: number }; response: { votes: number } };
  "DELETE /polls/:id": {};                                     // no response: 204
}
```

Wrap the routes with `defineRoutes<Routes>()` (identity at run time):

```ts
import { definePlugin, defineRoutes } from "@cicord/sdk";
import type { Routes } from "./routes.ts";

export default definePlugin({
  routes: defineRoutes<Routes>({
    "GET /polls/:id": {
      access: "public",
      async handler(ctx, request) {
        const poll = await getPoll(ctx, request.params.id); // params: { id: string }
        if (!poll) return { status: 404, body: { error: { code: "not_found", message: "no such poll" } } };
        return toResults(poll);                              // checked against PollResults
      },
    },
    // … every route of Routes must be implemented
  }),
});
```

`request.params` is typed from the key, `request.query` has the declared keys (as the strings
of the URL), `request.body` is the declared body, and the handler must return the declared
`response` or an envelope `{ status, body?, headers? }`. Interfaces can be returned directly
(no `as unknown as Json`). The runtime does **not** validate queries or bodies against the
types: check them like any input. `cicord plugin build` reports a contract that cannot be
copied (an import, a value…). Portals pick contracts up with
[`cicord portal typegen`](../../portal/custom-portal/#typed-plugin-routes). The official
[`levels`](https://github.com/cicord/cicord/blob/main/examples/showcase/plugins/levels/src/routes.ts)
and [`polls`](https://github.com/cicord/cicord/blob/main/examples/showcase/plugins/polls/src/routes.ts)
plugins have one.

In a portal, `@cicord/client`'s `client.routes(plugin, server, instance)` (typed) and
`client.plugin(server, bot, instance)`, and `@cicord/react`'s `useRouteQuery` and
`usePluginQuery`, call the routes (see [Portal SDK](../../portal/sdk/)).

### Portal links

`ctx.portal.link(path, { member, expiresInSeconds })` (`portal.links`) returns a signed portal
URL. With `member` (a `Member` or a `User`), opening it signs that member in, restricted to
this server. Links last 15 minutes by default, 24 hours at most. The `welcome` example above
DMs one to each new member.

A **case link** reaches someone who may have left or been banned, for an appeal or a
verification result: `ctx.portal.link("/appeals", { user, scope: { kind: "case", id }, expiresInSeconds })`
(7 days at most). Opening it requires that user's Discord sign-in, and the session can only call the issuing
instance's `scoped` routes, with `request.scope.id` as the signed case id.
`ctx.portal.revoke(scope)` invalidates its links and sessions. Details:
[Case links](../../reference/http-api/authentication/#case-links).

## Schedules and tasks

Both need `scheduler`. `schedules` run on a cron expression in **UTC**: standard 5-field cron
and descriptors (`@hourly`, `@daily`, `@every 10m`); `TZ=` prefixes are refused. `tasks` are
one-shot jobs started with `ctx.schedule.after()`; they are persisted and survive restarts
and new revisions. The runtime acknowledges a task after its handler succeeds. After a
crash, its lease expires and the same task ID can run again. The third handler argument
carries `id` and `attempt`. Recheck the current state before applying a delayed change.

Failures retry with exponential backoff: five deliveries in all, after about 1 s, 7 s,
45 s and 5 min (with up to 20% jitter), so an outage of a few minutes does not park
anything. After the fifth handler failure, or after a handler (or, for an event, the
subscription) has been unavailable for 24 hours past the due time, the task appears in
the server's admin Diagnostics with a failure category: `handler timed out`, `handler
crashed the instance`, `handler exceeded its execution budget`, `handler failed:
TypeError` (built-in error names only), `handler failed: interop timeout` and so on. The
plugin's error text stays in the runtime log, since it can carry member data. An
administrator can retry or discard it. Task payloads stay private.

A crash of the instance is charged only to the handler whose code was running when the
VM died (a task that exhausts the memory limit, for example). Tasks and events that
waited or ran beside it are released without a failure and delivered again once the
instance has restarted, and no delivery is claimed while it is crashed. A task that
crashes the instance on each attempt becomes a dead letter on its own.

```ts
schedules: {
  digest: { cron: "0 9 * * 1", async run(ctx) {
    await ctx.server.channel("announcements").send("Weekly digest…");
  } },
},
tasks: {
  async remind(ctx, payload) {
    const { userId } = payload as { userId: string };
    await (await ctx.server.member(userId))?.send("Reminder!");
  },
},
// elsewhere: const { id } = await ctx.schedule.after(60 * 60_000, "remind", { userId });
//            await ctx.schedule.cancel(id);
```

:::tip[Editing a message later]
Keep `{ channelId, messageId }` from the `Message` that `channel.send()` returns, and in the
task fetch it back with `ctx.server.channel({ id: channelId }).message(messageId)`
(`discord.messages.read`; `undefined` once the message is deleted). The official `polls`
plugin does this to close a poll.
:::

## Plugin-owned channels

`ctx.server.createChannel({ name, type, category, topic, overwrites, userLimit, bitrate })`
(`discord.channels.manage`; `type` is `text`, `voice` or `forum`) creates a channel owned by
the instance: the reconciler never
touches or prunes it, even with `prune: true`, and only that instance can `editChannel()` and
`deleteChannel()` it. `overwrites` keys are `"@everyone"`, a role key, `"role:<id>"` or
`"member:<id>"`. Without `overwrites`, a channel created in a category gets the category's
(Discord syncs it): a channel of a private category stays private. `userLimit` (0–99, 0 is no
limit) and `bitrate` (8000–384000; above 96000 needs server boosts) are for voice channels.

Give the bot itself access (`member:${ctx.bot.user.id}`) when the channel hides `@everyone`,
or a bot without Administrator locks itself out. The official `tickets` plugin is a complete
example.

A handler that creates a channel and then records it (`ctx.kv.set`) can die in between, and
its replay would create a second channel. Pass `provisionKey` (at most 200 bytes, e.g.
`room:${trigger.id}:${member.id}`): while the channel created with that key still exists and
the instance owns it, `createChannel` returns it instead of creating another one, across
restarts. `ctx.server.recoverOwnedChannel({ provisionKey })` answers `{status: "owned", id}`
or `{status: "missing"}` without creating anything; `recoverOwnedChannel(topic)` finds a
text channel by its unique topic. Keys belong to the instance, are stored as a digest (keys
derived from member ids keep no id), are forgotten 30 days after the creation or once the
channel is deleted, and are purged with the instance's other data.

`ctx.server.editChannel(channel, { name, topic, userLimit, bitrate, overwrites, reason })`
changes a channel the instance owns. Its `overwrites` are **patched**: for each target,
`allow` and `deny` set those permissions (taking them out of the other side), `clear` makes
them neutral again, and `null` removes the target's overwrite. Targets and permissions not
mentioned are kept, including what the channel got from its category:

```ts
// Lock a room: nobody but the members allowed one by one can connect.
await ctx.server.editChannel(room, { overwrites: { "@everyone": { deny: ["Connect"] } } });
await ctx.server.editChannel(room, { overwrites: { [`member:${guest.id}`]: { allow: ["ViewChannel", "Connect"] } } });
// Unlock it.
await ctx.server.editChannel(room, { overwrites: { "@everyone": { clear: ["Connect"] } } });
```

The runtime reads the channel's current overwrites from Discord, applies the patch and writes
them back. A permission in two of `allow`, `deny` and `clear` is a `TypeError`; an empty
member overwrite is removed.

## Voice

| Call | What it does | Needs |
|---|---|---|
| `voiceStateUpdate(ctx, update)` | A member joined, left or moved between voice channels, or changed their voice state (mute, deafen, stream, camera) | `ServerVoiceStates` intent |
| `ctx.server.voiceMembers(channel)` | The members connected to a voice channel, in user id order, bots included | `discord.members.read`, `ServerVoiceStates` intent |
| `member.moveTo(channel, reason?)` | Moves a member who is connected to voice to a voice channel | `discord.voice.move` |
| `member.disconnect(reason?)` | Disconnects a member from voice | `discord.voice.move` |

`update.channel` is where the member is now (`undefined` once they left voice) and
`update.previous` where they were: compare them to tell a join, a leave and a move from a mute.
Discord does not send `previous`: the runtime keeps the voice states of its servers (the
gateway sends them when a server becomes available, then every change) and fills it in. Just
after a start or a reconnect it may not know yet, and `previous` is `undefined`; a plugin that
must not miss a leave also checks its channels periodically, as the official `voice-rooms`
plugin does.

`voiceMembers()` reads the same voice states. Until the gateway has sent them it throws (it
never answers "nobody" when it does not know), so a plugin that cleans up empty channels at
start schedules the check a little later (`ctx.schedule.after`). The intent is checked when
it is called, and `cicord plugin build` does not warn about a declared `ServerVoiceStates`
without `voiceStateUpdate` when the plugin has `discord.members.read`.

`moveTo()` needs the bot to have Move Members, and Connect in the destination; the
destination's user limit does not apply to moves. A member who is not connected to voice
cannot be moved (the call rejects). The bot moving a member sends a `voiceStateUpdate` like
any move.

```ts
events: {
  async voiceStateUpdate(ctx, { member, channel, previous }) {
    if (channel?.id === previous?.id || !member || !channel) return; // mute, leave…
    if (channel.id !== ctx.server.channel("create-room").id) return;
    const room = await ctx.server.createChannel({ name: `${member.displayName}'s room`, type: "voice",
      category: channel.categoryId ? { id: channel.categoryId } : undefined });
    await ctx.server.editChannel(room, { overwrites: { [`member:${member.id}`]: { allow: ["Connect", "MoveMembers"] } } });
    await member.moveTo(room);
  },
},
```

## Cross-plugin events

`ctx.events.publish(topic, payload, { version?, idempotencyKey? })` (`events.publish`)
delivers to the subscribed instances on the same server: of the same bot by default, of
every bot of the project when the manifest declares the topic with `scope: server`. Topics
are namespaced with the publisher's plugin name: plugin `xp` calling `publish("levelUp", …)`
reaches `subscriptions["xp.levelUp"]` (`events.subscribe`). Delivery is at least once, so the
subscriber deduplicates on `meta.id`.

```ts
// plugin "xp"
await ctx.events.publish("levelUp", { userId: member.id, level: 5 }, { idempotencyKey: `level:${member.id}:5` });

// another plugin
subscriptions: {
  async "xp.levelUp"(ctx, payload, meta) {
    const { userId, level } = payload as { userId: string; level: number };
    const first = await ctx.kv.atomic()
      .check(`seen:${meta.id}`, undefined) // not handled yet
      .set(`seen:${meta.id}`, true, { ttlSeconds: 30 * 86_400 })
      .increment(`levelups:${userId}`) // the effect, committed with the marker
      .commit();
    if (first.ok) ctx.log.info("level up", { userId, level });
  },
},
```

Versions, contracts, retries and plugin-to-plugin queries and actions are in
[Versioned events and plugin calls](../writing/#versioned-events-and-plugin-calls). The
official plugins publish `polls.closed` and `moderation.case`, among others.

## Storage, HTTP and logging

- **`ctx.kv`** (`storage.kv`): a JSON key-value store scoped to the instance: `get`,
  `getEntry`, `getMany`, `set(key, value, { ttlSeconds })`, `delete`, atomic
  `increment(key, by?)` (from 0), `list({ prefix, limit, cursor })` (returns
  `{ items, cursor? }`), `atomic()` and `ranking(name)` ([below](#atomic-kv-and-rankings)).
  It persists across revisions and restarts.
- **`ctx.http.fetch(url, { method, headers, body, form, json, timeoutMs })`** (`http.fetch`;
  `body` is a string, `form` and `json` are encoded by the host) returns
  `{ status, headers, bytes(), text(), json() }`, to the allowed hosts only
  ([network egress](../writing/#network-egress)).
- **`ctx.log.debug/info/warn/error(message, fields)`** and `console.*` go to the runtime log,
  tagged with the instance.

### Atomic KV and rankings

`getEntry(key)` returns the JSON value and an opaque version. Use `check(key, version)` to guard a transaction, or pass `undefined` to require an absent or expired key. `getMany(keys)` preserves request order, duplicate keys and the difference between missing values and JSON null.

```ts
const account = await ctx.kv.getEntry<number>("account:27");
const balance = (account?.value ?? 0) + 5;
const result = await ctx.kv.atomic()
  .check("account:27", account?.version)
  .set("account:27", balance)
  .rankSet("balances", "27", balance)
  .commit();
```

A failed check returns `{ok:false}`. Mutation errors reject the promise and roll back every change. Ordinary writes also change the version. An increment preserves a live value’s expiry. A set replaces its expiry and accepts `{ttlSeconds}`. A transaction accepts 128 checks and mutations combined. Keys, board names and member keys allow 1–512 UTF-8 bytes. A value can hold up to 1 MiB. A plugin instance can store 1,000,000 KV keys and ranking members combined, and 256 MiB of them. A key counts its key and value bytes, and a ranking member counts its board name and member bytes plus 16. A write that would exceed either quota fails, and so does an update that grows a value past it. Deletes and expired keys free capacity once swept or removed. A multi-read accepts 128 keys and up to 4 MiB of returned values. A `kv.list` page stops before the entry that would take it past 4 MiB, so it can hold fewer entries than `limit`. Its cursor then continues after the last entry returned.

`ctx.kv.ranking(name)` supports `add`, `set`, `score`, `rank`, `top({offset,limit})`, `around(member,radius)`, `remove` and `count`. Order is by descending finite score, then ascending UTF-8 member key. Ranks start at 1. A page returns `{member,score,rank}[]` and can contain up to 1,000 entries. The maximum radius is 499. A board holds up to 250,000 members, and adding a member past that fails. Writes change one row whatever the move. Ranks are counted on read over the score index. `top()` without an offset and `count()` cost O(log n + limit). `rank()`, `around()` and a deep `top({offset})` also count the members ahead, which costs up to the board size (about 10 ms at 100,000 members on SQLite). Atomic builders also support `rankRemove(name,member)`.

### Files and full-text search

- **`ctx.assets.read(path)`**: bytes of a file under the manifest's `spec.assets`, packaged
  with the code ([details](../writing/#attachments-assets-and-images)).
- **`ctx.files.list(glob?)`, `read(path)`, `bytes(path)`** (`files.read`): repository files
  bound to the instance by its `files:` globs in `bot.yaml` (relative to the repository root,
  for example `files: ["tags/**/*.md"]`), from the active revision. Hidden files and
  `node_modules` are skipped; at most 10,000 files and 20 MiB per instance.
- **`ctx.index(name, { language? })`** (`storage.index`): a full-text index (`en` or `fr`)
  with `upsert(id, { title?, text, tags?, meta? })`, `remove(id)`, `clear()` and
  `search(query, { limit?, tags? })` (1–50 hits, default 10, each with a `snippet`). A
  document holds at most 256 KiB and 32 tags. SQLite uses FTS5, PostgreSQL `tsvector`.

## Globals

Plugins get `setTimeout`, `setInterval`, `clearTimeout`, `clearInterval`, `queueMicrotask`,
`crypto` and `console`. Timers belong to the handler that created them and stop with it.
There is no `process`, no global `fetch`, no DOM and no Node.js API.

`crypto.getRandomValues(view)` fills a `Uint8Array` of up to 65,536 bytes from the host's
cryptographic random source, and `crypto.randomUUID()` returns a version 4 UUID.
`Math.random()` is seeded from the clock and predictable: never use it for draws, tokens or
anything a member could gain by guessing, and store the seed or the result so that a replay
draws the same winners.

## Member privacy

Declare each member-bearing record in `spec.privacy.records`. Include members mentioned inside shared records, ranking members, task payloads, transcripts, Discord messages sent by the bot and other copies. Each entry has `name`, `description`, and a `policy` of `erase`, `anonymize`, or `retain`. Include the retention period and justification for retained data. Declare an empty array when the plugin keeps no member data.

Implement `privacy.export(ctx,userId)` to return the member’s JSON records and `privacy.erase(ctx,userId)` to return `{erased:number}`. Erasure must be idempotent. For shared records, preserve the other members’ data. For anonymized records use `await ctx.privacy.pseudonym(userId)` and clear identifying text. The host restricts pseudonym generation to the subject of the current privacy hook. Track Discord message IDs if the bot must edit or delete those copies.

Store member IDs as strings, as Discord returns them. The host recognises a member in stored work only as a 17 to 20 digit snowflake inside a string or object key (a mention like `<@id>` counts); JSON numbers never match. An erasure does not block the member: if they come back, their new commands, tasks, events and interop calls work normally. Only work that may predate the erasure goes back through `erase` instead of being delivered: work of an installation that was disabled, unavailable, failing or uninstalled at erasure time (until its data is erased), and, after a restore of a database older than the erasure, restored work until startup or a gated delivery has erased the member again.

Server administrators can run both operations in **Server settings → Member data**. The report includes per-instance policies, hook results and failures. An uninstalled installation that still holds data about the member is listed as `orphaned`: purge its data under **Server settings → Data of uninstalled plugins** (or `cicord plugin purge-data`) to complete the erasure. A legacy plugin without a declaration is reported as unverified. Test with two members sharing one record, erase one, and re-export both. A check of storage key prefixes alone cannot verify shared values or remote copies.

An indexed document can bind itself to a member with an exact `member-${userId}` tag. After a successful erasure hook, the host removes any documents still carrying that subject’s tag in the same installation. If the installation has lost its `storage.index` grant, `index.remove(id)` inside its erasure hook can still remove an explicitly tagged document for the current subject. Untagged existing documents report incomplete cleanup. For shared content, rewrite it with the remaining subjects and clear identifying free text.

Member erasure waits for active plugin handlers and their outstanding host operations on that server. Detached callbacks that retain a snapshot from before an erasure must reload current state in a fresh handler. If an instance cannot complete its erasure hook, the runtime preserves that instance’s matching scheduled payloads and defers their execution. This lets a later successful hook establish an opaque replacement timer before the original is purged.

Next: [testing and limits](../testing/).
