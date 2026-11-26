---
title: Testing and limits
description: The plugin execution model and its limits, unit tests with @cicord/sdk/testing, cicord plugin test on the simulator, and the end-to-end loop with cicord dev --sim.
---

## Execution model and limits

Each plugin instance gets its own VM and one event loop: QuickJS-ng compiled to WebAssembly,
run inside the Cicord process by wazero (no CGO;
[ADR-0018](../../project/decisions/0018-quickjs-wasm-engine/)).
Handlers start in arrival order and
interleave at `await` points, like in Node.

| Limit | Value |
|---|---|
| Synchronous JavaScript per event-loop turn | 50 ms (a runaway turn aborts its handler; the instance keeps serving) |
| Memory per instance | 64 MiB of JavaScript heap; reaching it discards the instance (even if the error is caught), which restarts with backoff (1 s, doubling up to 5 min) |
| Code that ignores the turn budget (a long native call) | killed 500 ms after the budget (one more budget for budgets above 500 ms); the instance restarts |
| Microtasks still queued after a turn | run in a second turn charged to the same handler; if that turn overruns too, the instance crashes and restarts |
| Microtasks still spinning after their handler was aborted | the instance restarts |
| Pending SDK calls per instance | 256 (beyond, the call rejects) |
| Handler calls waiting for the instance | 1024 (beyond, the call fails as busy) |
| SDK call arguments | ≤ 2 MiB of JSON; `ctx.log` fields ≤ 16 KiB (larger ones are dropped) |
| Module evaluation at start | 2 s |
| Handler duration, awaits included | 10 s (async work it started is cut too) |
| Auto-defer of interactions | 2 s |
| Autocomplete handlers | 2.5 s, never deferred; ≤ 25 choices (name 1–100 characters, string values ≤ 100) |
| `onStop` | 3 s |
| `http.fetch` | 10 s default timeout (`timeoutMs` up to the handler timeout), response ≤ 5 MiB, request body ≤ 1 MiB, ≤ 5 redirects |
| KV | key 1–512 bytes, value ≤ 1 MiB, `list` limit 1–1000 and ≤ 4 MiB per page (a larger page is cut short; its `cursor` continues) |
| Portal route responses | ≤ 4 MiB of JSON |
| Task and event payloads | ≤ 1 MiB |
| Event topics | 1–100 characters (`A-Z a-z 0-9 . _ : -`) |
| `channel.messages()` | limit 1–100 (`channel.message(id)` fetches one) |
| Component and modal ids | handler key ≤ 80 bytes; `c:<instance>:<id>` ≤ 100 bytes |
| Commands | name 1–32, description 1–100, ≤ 25 options, ≤ 25 choices, ≤ 25 subcommands and groups (a group holds ≤ 25 subcommands), string options ≤ 6000 characters (`minLength`/`maxLength`) |
| Portal links | 15 minutes by default, 24 hours at most |
| `member.setNickname()` | ≤ 32 characters |
| `member.timeout()` | ≤ 28 days |

An uncaught error is logged with the instance and the revision. After **three failures in a
row**, the instance shows as `Degraded` in the status. A **crashed** instance (memory limit,
killed code) shows as `Degraded` at once with the reason and restarts automatically; while it
restarts, its calls fail and interactions get the generic error reply.

## Unit tests with `@cicord/sdk/testing`

`@cicord/sdk/testing` provides in-memory fakes that follow the SDK contract. They run in
`node:test` or Vitest, without Discord and without the runtime.

```ts title="test/hello.test.ts"
import assert from "node:assert/strict";
import { test } from "node:test";
import { createTestContext, runCommand } from "@cicord/sdk/testing";
import schema from "../config.schema.json" with { type: "json" };
import plugin from "../src/index.ts";

const grants = ["discord.messages.send"];
const members = [{ id: "1", username: "alice" }];

test("/hello replies with the default greeting", async () => {
  // `schema` applies the config defaults like the runtime does.
  const t = createTestContext({ schema, grants, members });
  const result = await runCommand(plugin, t, "hello", { user: "1" });
  assert.deepEqual(result.replies, ["Hello!"]);
});
```

This is the first test `cicord plugin new` writes. Run it from the plugin directory:

```sh
npm install && npm test      # or: pnpm install && pnpm test
```

It needs no other dependency than the vendored `@cicord/sdk`: `npm test` runs
`test/**/*.test.ts` with the type stripping of Node 24 (the scaffold's `engines`: `>=24`), so keep the sources type-strippable: `.ts`
extensions on relative imports, `import type` for types, no `enum` or `namespace`. Use
`node:assert` where the examples below use `expect`; the rest applies as is. Prefer Vitest?
`npm i -D vitest` and replace the test script with `vitest run`.

The same harness with Vitest, from the official
[`onboarding.test.ts`](https://github.com/cicord/cicord-plugins/blob/main/plugins/onboarding/test/onboarding.test.ts)
(shortened; the plugins import `vite-plus/test`, Vitest's API):

```ts
import { callRoute, createTestContext } from "@cicord/sdk/testing";
import { expect, test } from "vite-plus/test";
import schema from "../config.schema.json" with { type: "json" };
import plugin from "../src/index.ts";

const config = {
  steps: [{ id: "platform", title: "Platform", required: true, options: [{ role: "pc" }, { role: "console" }] }],
  completedRole: "member",
};

function setup(memberRoles: string[] = []) {
  return createTestContext({
    config,
    schema, // applies the config defaults like the runtime (e.g. steps[].multiple)
    grants: ["discord.roles.assign", "storage.kv", "portal.routes", "portal.links"],
    roles: { member: { name: "Member" }, pc: { name: "PC" }, console: { name: "Console" } },
    members: [{ id: "1", username: "alice", roles: memberRoles }],
  });
}

test("GET /steps requires a member", async () => {
  expect((await callRoute(plugin, setup(), "GET /steps")).status).toBe(401);
  expect((await callRoute(plugin, setup(), "GET /steps", { viewer: "999" })).status).toBe(403);
});

test("POST /complete adds the picked roles", async () => {
  const t = setup(["console"]);
  const res = await callRoute(plugin, t, "POST /complete", {
    viewer: "1",
    body: { answers: { platform: ["pc"] } },
  });
  expect(res.body).toMatchObject({ completed: true, added: ["pc", "member"], removed: ["console"] });
});
```

### What the harness offers

`createTestContext(options)` returns a harness whose `ctx` you pass to your handlers.

- **Options**: `config`; `schema` (its defaults are applied like the runtime does;
  `applyDefaults(schema, config)` is exported too); `grants` (when given, calls without the
  grant throw `CapabilityError`); `intents` (plugin.yaml `spec.intents`: when given,
  `role.members()` throws without `ServerMembers` and `voiceMembers()` without
  `ServerVoiceStates`, like the runtime); `plugin`, `bot`, `server`; `roles` and `channels`
  keyed by resource key (a channel takes `type` and `categoryId`); `members` (`role.members()` lists
  those holding the role, in user id order); `portalUrl`; and `fetch(url, init)`, the fake
  `ctx.http.fetch`.
  For permission checks, roles take `position` (default 1, 2, 3… in declaration order),
  `mentionable` and `permissions`; `server` takes `ownerId` and `everyonePermissions`; and
  `access: { admins, moderators }` (`{ users?, roles? }`, role keys) sets `member.access`.
  `member.permissions` is computed like the runtime (server-level).
- **Recorded effects**: `sent` (messages with their `id` and their normalized
  `allowedMentions`, lists holding ids; DMs are `to: "dm:<userId>"`),
  `edits`, `deletedMessages`, `reactions`, `threads`, `roleChanges`, `moderation`
  (timeouts, kicks, bans, nicknames), `createdChannels` / `editedChannels` / `deletedChannels`
  (created channels resolve with `{ id }`; `editChannel()` and `deleteChannel()` refuse
  channels the plugin did not create), `voiceMoves` (`moveTo()` and `disconnect()`), `links`,
  `published`, `scheduled`, `requests` (`http.fetch`), `logs`, and the raw `kv` (TTLs follow
  `Date.now()`, so fake timers expire keys).
- **Helpers**: `member(id)`, `addMember(init)`, `removeMember(id)`, `message(id)`,
  `channel(keyOrId)`.
- **Voice**: `setVoice(userId, channel | null)` connects, moves or disconnects a member and
  returns the update to pass to `voiceStateUpdate` (with `previous`); `voiceChannel(userId)`
  is where they are (`moveTo()` and `disconnect()` change it too, and deleting a channel
  disconnects its members); `overwrites(channelId)` is a created channel's overwrites after
  its `editChannel()` patches (category overwrites are not modelled).

  ```ts
  const t = createTestContext({ channels: { hub: { name: "Create", type: "voice" } }, members: [{ id: "1" }] });
  await plugin.events!.voiceStateUpdate!(t.ctx, t.setVoice("1", "hub"));
  expect(t.voiceChannel("1")).toBe(t.createdChannels[0]!.channel.id);
  ```

Runners drive the handlers with fakes that follow the runtime's rules (a second `reply()`
throws, `followUp()` needs a response, `showModal()` must come first, `update()` is for
components, ids fall back to the part before `:`):

| Runner | Returns |
|---|---|
| `runCommand(plugin, harness, "name", { user, channel, options })` (a subcommand: `"polls close"`, `"polls admin purge"`) | `{ replies, followUps, updates, deferred, modal }` |
| `runAutocomplete(plugin, harness, "polls close", { user, focused: "poll", value, options })` | the choices of the option's `autocomplete`, checked like the runtime (≤ 25, names 1–100, values of the option's type); an invalid result throws |
| `runComponent(plugin, harness, "vote:42", { user, message, values })` | same |
| `submitModal(plugin, harness, "open", { user, fields })` | same |
| `callRoute(plugin, harness, "POST /complete", { viewer, body, query })` | `{ status, body, headers }`, matching the route patterns; `401` for anonymous callers of non-public routes, `403` for non-members of `member` routes |

For runners that take them, `message` is a message sent through the fakes (or its id), and
`channel` sets `interaction.channel`.

Events, tasks, schedules and subscriptions are plain functions: call them with the harness
context, for example `await plugin.tasks!.close!(t.ctx, { id: "1" })`. `capabilityError(name)`,
`normalizeRouteResponse(response)` and `normalizeAllowedMentions(message)` are exported
helpers. Messages, replies, follow-ups and updates check `allowedMentions` like the host
(ids must be snowflakes or ids of the fakes, such as `role-staff`).

Importing the JSON schema in tests needs `"resolveJsonModule": true` in the test
`tsconfig.json`.

### In the Cicord repository

`pnpm test` runs `examples/**/test/**/*.test.ts` (root `vite.config.ts`), and
`pnpm exec vp test examples/showcase/plugins/polls` runs one plugin's unit tests.
[SDK coverage](../coverage/) lists the Go end-to-end tests that drive the official plugins
through the real runtime.

## `cicord plugin test`: a smoke test on the simulator

`cicord plugin test <dir>` is different from unit tests: it builds the plugin and deploys it in a
throw-away repository on the Discord simulator, with the **real runtime**. It creates a test
server (with the channels, roles and categories its config references), a bot granted every
requested capability and one instance of the plugin, then runs a short script: a member joins
(when the plugin handles `memberJoin`) and every slash command without required options is
invoked once. Messages, DMs and replies are printed.

```sh
# in a checkout of cicord/cicord-plugins
cicord plugin test plugins/welcome
cicord plugin test plugins/onboarding --config plugins/onboarding/test/config.yaml
```

Use an optional `test/server.yaml` to configure the disposable simulated server. Its
`syncPolicy` may set either or both booleans below. To simulate a private log, list its
channel key under `privateChannels`. Each key must reference a text channel in the
plugin’s effective test configuration. The simulator denies `ViewChannel` and
`ReadMessageHistory` to `@everyone` on those channels. The fixture accepts up to 32
unique channel keys and is limited to 4 KiB.

```yaml
syncPolicy:
  selfHeal: true
  deletionProtection: true
privateChannels: [audit]
```


```text
Starting the plugin on the simulator…
ok   instance onboarding started
ok   commands registered: /onboarding
ok   /onboarding: "Pick your roles on the community portal:" [button "Open onboarding"] (ephemeral)
PASS
```

The test fails when the plugin does not build, its config does not validate, the instance
does not start, or an interaction fails. `--config` takes a YAML or JSON config (required
`x-cicord-ref` fields without a value are filled with test resources), `--script=false` skips
the simulation, `-v` shows the runtime and plugin logs, `--timeout` bounds the whole run.

## End to end with `cicord dev --sim`

To try a plugin interactively, run the whole runtime on the simulator:

```sh
cicord dev --sim
```

Open `http://127.0.0.1:7420/admin/`, sign in as a simulated user and use the **Simulator**
panel: join members, send messages, run slash commands with typed options, click buttons, pick
select values, fill modals and read DMs, rendered like Discord. Every save of a plugin file is
a new revision; KV data and scheduled tasks survive the reload.

Before a real deployment, remember what the simulator cannot show: real rate limits, the
3-second deadline under real latency, privileged intents not enabled in the developer portal,
and the role hierarchy of a bot that is not Administrator. It also cannot post user messages
with attachments (the `attachment` command option works), and it checks the message a
`message.reply()` refers to but does not keep the reference on the reply. The
[real-Discord checklist](../coverage/#checklist-on-real-discord) covers these.

The [two-account comparison](../discord-parity/) records live results for permissions, moderation, polls, tickets and voice rooms alongside the corresponding candidate tests.

## In CI

```sh
cicord plugin build plugins/hello            # bundles and checks against the manifest (exit 1 if rejected)
cicord plugin typegen plugins/hello --check  # src/cicord-env.d.ts is up to date
cicord plugin test plugins/hello             # smoke test on the simulator
cicord validate                              # the whole repository
```
