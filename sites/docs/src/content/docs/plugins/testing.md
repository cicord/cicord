---
title: Testing and limits
description: The plugin execution model and its limits, unit tests with @codi/sdk/testing, codi plugin test on the simulator, and the end-to-end loop with codi dev --sim.
---

## Execution model and limits

Each plugin instance gets its own VM (Sobek, a pure-Go JavaScript engine) and one event loop.
Handlers start in arrival order and interleave at `await` points, like in Node.

| Limit | Value |
|---|---|
| Synchronous JavaScript per event-loop turn | 50 ms (a runaway turn aborts its handler; the instance keeps serving) |
| Module evaluation at start | 2 s |
| Handler duration, awaits included | 10 s (async work it started is cut too) |
| Auto-defer of interactions | 2 s |
| `onStop` | 3 s |
| `http.fetch` | 10 s default timeout (`timeoutMs` up to the handler timeout), response ≤ 5 MiB, request body ≤ 1 MiB, ≤ 5 redirects |
| KV | key 1–512 bytes, value ≤ 1 MiB, `list` limit 1–1000 |
| Task and event payloads | ≤ 1 MiB |
| Event topics | 1–100 characters (`A-Z a-z 0-9 . _ : -`) |
| `channel.messages()` | limit 1–100 |
| Component and modal ids | handler key ≤ 80 bytes; `c:<instance>:<id>` ≤ 100 bytes |
| Commands | name 1–32, description 1–100, ≤ 25 options, ≤ 25 choices |
| Portal links | 15 minutes by default, 24 hours at most |
| `member.setNickname()` | ≤ 32 characters |
| `member.timeout()` | ≤ 28 days |

An uncaught error is logged with the instance and the revision. After **three failures in a
row**, the instance shows as `Degraded` in the status.

## Unit tests with `@codi/sdk/testing`

`@codi/sdk/testing` provides in-memory fakes that follow the SDK contract. They run in
`node:test` or Vitest, without Discord and without the runtime.

```ts title="test/hello.test.ts"
import assert from "node:assert/strict";
import { test } from "node:test";
import { createTestContext, runCommand } from "@codi/sdk/testing";
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

This is the test `codi plugin new` writes. Run it from the plugin directory:

```sh
npm install && npm test      # or: pnpm install && pnpm test
```

Node 24 runs the TypeScript directly, so keep the sources type-strippable: `.ts` extensions on
relative imports, `import type` for types, no `enum` or `namespace`. Prefer Vitest?
`npm i -D vitest` and replace the test script with `vitest run`.

### What the harness offers

`createTestContext(options)` returns a harness whose `ctx` you pass to your handlers.

- **Options**: `config`; `schema` (its defaults are applied like the runtime does;
  `applyDefaults(schema, config)` is exported too); `grants` (when given, calls without the
  grant throw `CapabilityError`); `plugin`, `bot`, `server`; `roles` and `channels` keyed by
  resource key; `members`; `portalUrl`; and `fetch(url, init)`, the fake `ctx.http.fetch`.
- **Recorded effects**: `sent` (messages with their `id`; DMs are `to: "dm:<userId>"`),
  `edits`, `deletedMessages`, `reactions`, `threads`, `roleChanges`, `moderation`
  (timeouts, kicks, bans, nicknames), `createdChannels` / `deletedChannels`, `links`,
  `published`, `scheduled`, `requests` (`http.fetch`), `logs`, and the raw `kv` (TTLs follow
  `Date.now()`, so fake timers expire keys).
- **Helpers**: `member(id)`, `addMember(init)`, `removeMember(id)`, `message(id)`,
  `channel(keyOrId)`.

Runners drive the handlers with fakes that follow the runtime's rules (a second `reply()`
throws, `followUp()` needs a response, `showModal()` must come first, `update()` is for
components, ids fall back to the part before `:`):

| Runner | Returns |
|---|---|
| `runCommand(plugin, harness, "name", { user, channel, options })` | `{ replies, followUps, updates, deferred, modal }` |
| `runComponent(plugin, harness, "vote:42", { user, message, values })` | same |
| `submitModal(plugin, harness, "open", { user, fields })` | same |
| `callRoute(plugin, harness, "POST /complete", { viewer, body, query })` | `{ status, body, headers }`; `401` for anonymous callers of non-public routes, `403` for non-members of `member` routes |

Events, tasks, schedules and subscriptions are plain functions: call them with the harness
context, for example `await plugin.tasks!.close!(t.ctx, { id: "1" })`. `capabilityError(name)`
and `normalizeRouteResponse(response)` are exported helpers.

Importing the JSON schema in tests needs `"resolveJsonModule": true` in the test
`tsconfig.json`.

## `codi plugin test`: a smoke test on the simulator

`codi plugin test <dir>` is different from unit tests: it builds the plugin and deploys it in a
throw-away repository on the Discord simulator, with the **real runtime**. It creates a test
server (with the channels, roles and categories its config references), a bot granted every
requested capability and one instance of the plugin, then runs a short script: a member joins
(when the plugin handles `memberJoin`) and every slash command without required options is
invoked once. Messages, DMs and replies are printed.

```sh
codi plugin test plugins/welcome
codi plugin test plugins/onboarding --config plugins/onboarding/test/config.yaml
```

The test fails when the plugin does not build, its config does not validate, the instance
does not start, or an interaction fails. `--config` takes a YAML or JSON config (required
`x-codi-ref` fields without a value are filled with test resources), `--script=false` skips
the simulation, `-v` shows the runtime and plugin logs, `--timeout` bounds the whole run.

## End to end with `codi dev --sim`

To try a plugin interactively, run the whole runtime on the simulator:

```sh
codi dev --sim
```

Open `http://127.0.0.1:7420/admin/`, sign in as a simulated user and use the **Simulator**
panel: join members, send messages, run slash commands with typed options, click buttons, pick
select values, fill modals and read DMs, rendered like Discord. Every save of a plugin file is
a new revision; KV data and scheduled tasks survive the reload.

Before a real deployment, remember what the simulator cannot show: real rate limits, the
3-second deadline under real latency, privileged intents not enabled in the developer portal,
and the role hierarchy of a bot that is not Administrator.

## In CI

```sh
codi plugin build plugins/hello            # bundles and checks against the manifest (exit 1 if rejected)
codi plugin typegen plugins/hello --check  # src/codi-env.d.ts is up to date
codi plugin test plugins/hello             # smoke test on the simulator
codi validate                              # the whole repository
```
