# @cicord/sdk

Authoring types and test helpers for [Cicord](https://github.com/npjonath/cicord) plugins.
Cicord manages Discord servers and bots with GitOps; plugins are small TypeScript modules
that the Cicord runtime bundles and runs in a sandbox (no Node.js APIs: every side effect
goes through `ctx`).

```sh
npm install --save-dev @cicord/sdk
```

Inside the runtime, the host provides the `@cicord/sdk` module: this package is what your
editor, `tsc` and your unit tests see.

## Write a plugin

```ts
import { command, definePlugin } from "@cicord/sdk";

export default definePlugin({
  commands: {
    hello: command({
      description: "Say hello",
      async run(ctx, interaction) {
        await interaction.reply(`Hello ${interaction.user.displayName}!`);
      },
    }),
  },
});
```

`cicord plugin new <dir>` scaffolds a plugin (manifest, config schema, types and a unit
test). Exports: `definePlugin`, `command`, `defineRoutes` (portal routes checked against a
route contract) and `CapabilityError`, plus every type of the plugin API (`Context`,
`Member`, `Message`, `RouteRequest`…). The types are the normative contract
[`spec/sdk/cicord-sdk.d.ts`](https://github.com/npjonath/cicord/blob/main/spec/sdk/cicord-sdk.d.ts).

## Test it: `@cicord/sdk/testing`

In-memory fakes that follow the runtime's rules, for Vitest or `node:test`:

```ts
import assert from "node:assert/strict";
import { test } from "node:test";
import { createTestContext, runCommand } from "@cicord/sdk/testing";
import plugin from "../src/index.ts";

test("/hello", async () => {
  const t = createTestContext({ members: [{ id: "1", username: "alice" }] });
  const result = await runCommand(plugin, t, "hello", { user: "1" });
  assert.equal(result.replies.length, 1);
});
```

`createTestContext` records messages, role changes, KV writes, scheduled tasks, HTTP
requests…; `runCommand`, `runComponent`, `submitModal` and `callRoute` drive the handlers.
See [docs/plugins.md §8](https://github.com/npjonath/cicord/blob/main/docs/plugins.md).

ESM only, with TypeScript declarations. License: Apache-2.0.
