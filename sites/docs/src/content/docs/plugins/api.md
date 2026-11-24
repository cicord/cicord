---
title: Plugin API
description: definePlugin and the ctx object - events, slash commands, interactions, components, modals, portal routes and typed route contracts, schedules and tasks, plugin-owned channels, cross-plugin events, KV storage, HTTP and logging.
---

A plugin default-exports `definePlugin({...})` from `@cicord/sdk`. The normative contract is
[`spec/sdk/cicord-sdk.d.ts`](https://github.com/npjonath/cicord/blob/main/spec/sdk/cicord-sdk.d.ts);
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
| `ctx.server` | The instance's server: `id`, `key`, `name`, `channel()`, `role()`, `member()`, `createChannel()`, `deleteChannel()` |
| `ctx.config` | The effective config (overrides merged, defaults applied) |
| `ctx.log` | `debug`, `info`, `warn`, `error(message, fields)` |
| `ctx.kv` | Instance-scoped storage |
| `ctx.schedule` | `after()`, `cancel()` |
| `ctx.http` | `fetch()` |
| `ctx.events` | `publish()` |
| `ctx.portal` | `link()` |

Server resources are referenced by their **key** in `servers/<server>.yaml`
(`ctx.server.channel("general")`, `member.addRole("member")`), or by `{ id }` for resources
Cicord does not manage.

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
| `reactionAdd`, `reactionRemove` | `{ message, user, emoji }` (the message is partial) | `ServerMessageReactions` |

Events are delivered in order for each instance.

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
  `choices` (≤ 25) to `string`, `integer` and `number`.
- At most 25 options per command. A command name defined twice on the same bot and server
  fails validation.

## Interactions: reply, defer, follow up

Discord expects an answer within 3 seconds. If a handler has not answered after **2 s**, the
runtime **defers automatically** (ephemerally when the command says `ephemeral: true`;
components get a silent deferred update), and a later `reply()` edits the deferred response.
The first response fixes ephemerality. If a handler throws before answering, the user gets an
ephemeral "Something went wrong while handling this interaction." and the error is logged.

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
bytes). An id can **carry data after a colon**: `"vote:42"` is routed to `components.vote`,
and `interaction.componentId` stays `"vote:42"`.

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

- Buttons: `{ type: "button", id, label?, emoji?, style? }` (`primary`, `secondary`,
  `success`, `danger`); link buttons `{ type: "link", url, label }` trigger no handler.
- Select menus: `{ type: "select", id, options: [{ value, label }], minValues?, maxValues? }`;
  the picked values arrive in `interaction.values`.
- Messages: a string, or `{ content?, embeds?, components?, allowedMentions? }`; interaction
  replies can add `ephemeral: true`.

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
(`Promise<RouteResponse>`). Plugin routes cannot set cookies, CORS or security headers.

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

The runtime does **not** validate queries or bodies against the types: check them like any
input. `cicord plugin build` reports a contract that cannot be copied. Portals pick contracts up
with [`cicord portal typegen`](../../portal/custom-portal/#typed-plugin-routes). The official
`levels` and `polls` plugins have one.

### Portal links

`ctx.portal.link(path, { member, expiresInSeconds })` (`portal.links`) returns a signed portal
URL. With `member`, opening it signs that member in, restricted to this server. Links last 15
minutes by default, 24 hours at most.

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
The SDK has no "get message by id" yet. Keep `{ channelId, messageId }` from the `Message`
that `channel.send()` returns, and in the task fetch it back with
`channel.messages({ before: <messageId + 1>, limit: 1 })`. The official `polls` plugin does
this to close a poll.
:::

## Plugin-owned channels

`ctx.server.createChannel({ name, type, category, topic, overwrites })`
(`discord.channels.manage`) creates a channel owned by the instance: the reconciler never
touches or prunes it, even with `prune: true`, and only that instance can `deleteChannel()`
it. `overwrites` keys are `"@everyone"`, a role key, `"role:<id>"` or `"member:<id>"`.

Give the bot itself access (`member:${ctx.bot.user.id}`) when the channel hides `@everyone`,
or a bot without Administrator locks itself out. The official `tickets` plugin is a complete
example.

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
  `set(key, value, { ttlSeconds })`, `delete`, atomic `increment`,
  `list({ prefix, limit, cursor })`. It persists across revisions and restarts.
- **`ctx.http.fetch(url, { method, headers, body, timeoutMs })`** (`http.fetch`) returns
  `{ status, headers, text(), json() }`, to the `spec.network.allow` hosts only.
- **`ctx.log.debug/info/warn/error(message, fields)`** and `console.*` go to the runtime log,
  tagged with the instance.

## Globals

Plugins get `setTimeout`, `setInterval`, `clearTimeout`, `clearInterval`, `queueMicrotask` and
`console`. Timers belong to the handler that created them and stop with it. There is no
`process`, no global `fetch`, no DOM and no Node.js API.

Next: [testing and limits](../testing/).
