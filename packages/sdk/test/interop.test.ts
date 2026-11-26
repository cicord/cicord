import { expect, test, vi } from "vite-plus/test";
import { action, query, PluginUnavailableError } from "../src/index.ts";
import { createTestContext, onceFor, replayActionEffects } from "../src/testing.ts";

test("optional peers, feature reports and stable event deduplication", async () => {
  const h = createTestContext({ features: ["voters"] });
  expect(h.ctx.features.enabled("voters")).toBe(true);
  h.ctx.features.report("voters", true);
  expect(h.ctx.plugins.hasFeature(h.ctx.plugin.name, "voters")).toBe(true);
  expect(h.ctx.plugins.has("polls")).toBe(false);
  await expect(h.ctx.plugins.query("polls", "voters", {})).rejects.toBeInstanceOf(
    PluginUnavailableError,
  );
  const once = onceFor();
  const effect = vi.fn(async () => {});
  await Promise.all([once("poll:1", effect), once("poll:1", effect)]);
  expect(effect).toHaveBeenCalledTimes(1);
  await expect(
    once("other", async () => {
      throw Error("retry");
    }),
  ).rejects.toThrow("retry");
  await once("other", effect);
  expect(effect).toHaveBeenCalledTimes(2);
  const member = query({
    async run() {
      return 1;
    },
  });
  expect(
    await member.run(h.ctx, null, {
      caller: { plugin: "caller", bot: "b", instance: "p", source: "local", verified: false },
    }),
  ).toBe(1);
});
test("each external effect is replayed using its original action key", async () => {
  const handler = action({
    async run(ctx, input: { id: string }) {
      const member = await ctx.server.member(input.id);
      if (!member?.hasRole("member")) await member?.addRole("member");
      await ctx.kv.set("done", true);
      return true;
    },
  });
  const trials = await replayActionEffects(
    handler,
    { id: "user" },
    {
      caller: { plugin: "a", bot: "b", instance: "a", source: "local", verified: false },
      idempotencyKey: "grant:1",
    },
    () => createTestContext({ members: [{ id: "user" }], roles: { member: { name: "Member" } } }),
  );
  expect(trials).toHaveLength(2);
  for (const probe of trials) {
    expect(probe.harness.roleChanges).toHaveLength(1);
    expect(probe.result).toBe(true);
  }
});
