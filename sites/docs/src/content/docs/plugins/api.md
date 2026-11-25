---
title: Plugin API
description: definePlugin and the ctx object - events, slash commands, interactions, components, modals, portal routes and typed route contracts, schedules and tasks, plugin-owned channels, cross-plugin events, KV storage, HTTP and logging.
---

A plugin default-exports `definePlugin({...})` from `@cicord/sdk`. The normative contract is
[`spec/sdk/cicord-sdk.d.ts`](https://github.com/cicord/cicord/blob/main/spec/sdk/cicord-sdk.d.ts);
this page is a guided tour.

```ts
import { command, definePlugin } from "@cicord/sdk";

export default definePlugin({
  onStart(ctx) {},          // instance start (new revision, restart)
  onStop(ctx) {},           // before stop; bounded (3 s)
  events: {},               // gateway events
  commands: {},             // slash commands
  components: {},           // buttons and select menus
  modals: {},               // modal submissions
  schedules: {},            // cron jobs
  tasks: {},                // one-shot delayed jobs
  subscriptions: {},        // events from other plugins
  routes: {},               // community portal HTTP routes
});
```

## The context

Every handler receives `ctx`:

| Member | What it is |
|---|---|
| `ctx.plugin` | `name`, `version`, `instance` |
| `ctx.bot` | `key`, `user` |
| `ctx.server` | The instance's server: `id`, `key`, `name`, `iconUrl`, `ownerId`, `channel()`, `role()`, `member()`, `createChannel()`, `editChannel()`, `deleteChannel()`, `voiceMembers()` |
| `ctx.config` | The effective config (overrides merged, defaults applied) |
| `ctx.log` | `debug`, `info`, `warn`, `error(message, fields?)` |
| `ctx.kv` | Instance-scoped storage |
| `ctx.schedule` | `after()`, `cancel()` |
| `ctx.http` | `fetch()` |
| `ctx.events` | `publish()` |
| `ctx.portal` | `link()` |

Server resources are referenced by their **key** in `servers/<server>.yaml`
(`ctx.server.channel("general")`, `member.addRole("member")`), or by `{ id }` for resources
Cicord does not manage. `channel()` and `role()` throw when the resource does not exist.

| Object | Methods (capability in [Capabilities and grants](../writing/#capabilities-and-grants)) |
|---|---|
| `TextChannel` | `send(message)`, `messages({ limit?, before?, after? })`, `message(id)` (`undefined` once deleted), `createThread(name, { private?, fromMessage? })`; `id`, `key`, `name`, `type`, `categoryId`, `mention` |
| `Message` | `reply(message)`, `edit(message)`, `delete(reason?)`, `react(emoji)`; `content`, `author`, `member`, `channel`, `attachments`, `embeds`, `createdAt` |
| `Member` | `hasRole(role)`, `addRole(role, reason?)`, `removeRole(role, reason?)`, `setNickname(nickname \| null, reason?)`, `timeout(durationMs, reason?)` (0 removes it), `kick(reason?)`, `ban({ reason?, deleteMessagesSeconds? })`, `send(message)` (DM), `moveTo(channel, reason?)` and `disconnect(reason?)` ([voice](#voice)); `user`, `nickname`, `displayName`, `roles`, `joinedAt` |
| `Role` | `members({ limit?, after? })` ([below](#members-of-a-role)); `id`, `key`, `name`, `color`, `mention` |

### Permission checks

Discord checks a command's `permissions` and the **bot's** role hierarchy, not the
invoker's. To check what a member may do to another one, or which role they may ping:

| Field | What it is |
|---|---|
| `role.position` | Hierarchy position, higher is higher (`@everyone` is 0) |
| `role.mentionable` | Whether anyone may mention the role |
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
(`GET /guilds/{id}/members`, 1000 per request, at most 100,000 members) and filters them.
The listing is shared by the plugins of the server, reused for up to 60 seconds and kept
current in between by member joins, updates and leaves, so repeated calls are cheap; the first
call on a large server takes one request per 1000 members (rate limited): it may outlast the
handler timeout, but the listing goes on and later calls are served from it. The intent cannot be seen in the
plugin's definition, so it is checked when `members()` is called; `cicord plugin build` does not
warn about a declared `ServerMembers` without member handlers when the plugin has
`discord.members.read`. In tests, `createTestContext({ intents: [...] })` applies the same
check.

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
| `memberLeave` | `user` | `ServerMembers` |
| `memberUpdate` | `member`, `previous` | `ServerMembers` |
| `messageCreate` | `message` | `ServerMessages` (+ `MessageContent` for the text) |
| `messageUpdate` | `message` | `ServerMessages` |
| `messageDelete` | `{ id, channel }` | `ServerMessages` |
| `reactionAdd`, `reactionRemove` | `{ message, user, emoji }` (the message is partial: `message.channel.message(message.id)` fetches it) | `ServerMessageReactions` |
| `voiceStateUpdate` | `{ user, member, channel, previous, selfMute, selfDeaf, streaming, video }` ([voice](#voice)) | `ServerVoiceStates` |
| `serverJoin` | `{ id, name, iconUrl }` | none |
| `serverLeave` | `{ id }` | none |

Events are delivered in order for each instance, and only to instances of the servers
declared for the bot: joining an undeclared server runs no plugin. `serverJoin` and
`serverLeave` need no intent (`Servers` is always enabled). `serverJoin` fires when the bot is
invited (again) while connected, not when servers load after a connection or recover from an
outage; `serverLeave` when it is kicked, banned, removed or the server is deleted, not during
a Discord outage. On the gateway, a join is a `GUILD_CREATE` for a server that was neither in
the last `READY` nor seen since (nothing is a join before the first `READY`), and a leave is a
`GUILD_DELETE` without `unavailable`. After a re-invite the runtime registers the server's
slash commands again.

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
- Modals: `{ id, title, fields }`, each field `{ id, label, style?, required?, placeholder?,
  value?, minLength?, maxLength? }` (`short` or `paragraph`); the values arrive in
  `interaction.fields`.
- Messages: a string, or `{ content?, embeds?, components?, allowedMentions? }`; interaction
  replies can add `ephemeral: true`.

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

`access` is required and enforced by the runtime: `public`, `member`, `moderator` or `admin`
(anonymous callers of non-public routes get `401`, an insufficient role `403`). The handler
receives `request.viewer` (`{ user, member }`), `request.params`, `request.query` (the first
value of each parameter) and `request.body` (JSON). It returns any JSON value (`200`), an
envelope `{ status, body?, headers? }`, or nothing (`204`).

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
Plugin routes cannot set cookies, CORS or security headers. The full path and its access
rules are in the [HTTP API reference](../../reference/http-api/portal/#plugin-routes).

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
this server. Links last 15
minutes by default, 24 hours at most. The `welcome` example above DMs one to each new member.

## Schedules and tasks

Both need `scheduler`. `schedules` run on a cron expression in **UTC**: standard 5-field cron
and descriptors (`@hourly`, `@daily`, `@every 10m`); `TZ=` prefixes are refused. `tasks` are
one-shot jobs started with `ctx.schedule.after()`; they are persisted and survive restarts
and new revisions.

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

`ctx.events.publish(topic, payload)` (`events.publish`) delivers to the instances of the same
(bot, server). Topics are namespaced with the publisher's plugin name: plugin `xp` calling
`publish("levelUp", …)` reaches `subscriptions["xp.levelUp"]` (`events.subscribe`).

```ts
// plugin "xp"
await ctx.events.publish("levelUp", { userId: member.id, level: 5 });

// another plugin
subscriptions: {
  async "xp.levelUp"(ctx, payload) {
    const { userId, level } = payload as { userId: string; level: number };
    ctx.log.info("level up", { userId, level });
  },
},
```

The official plugins publish `polls.closed` and `moderation.case`.

## Storage, HTTP and logging

- **`ctx.kv`** (`storage.kv`): a JSON key-value store scoped to the instance: `get`,
  `set(key, value, { ttlSeconds })`, `delete`, atomic `increment(key, by?)` (from 0),
  `list({ prefix, limit, cursor })` (returns `{ items, cursor? }`). It persists across
  revisions and restarts.
- **`ctx.http.fetch(url, { method, headers, body, timeoutMs })`** (`http.fetch`; `body` is a
  string) returns
  `{ status, headers, text(), json() }`, to the `spec.network.allow` hosts only.
- **`ctx.log.debug/info/warn/error(message, fields)`** and `console.*` go to the runtime log,
  tagged with the instance.

## Globals

Plugins get `setTimeout`, `setInterval`, `clearTimeout`, `clearInterval`, `queueMicrotask` and
`console`. Timers belong to the handler that created them and stop with it. There is no
`process`, no global `fetch`, no DOM and no Node.js API.

Next: [testing and limits](../testing/).
