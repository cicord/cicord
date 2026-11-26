import { expect, test } from "vite-plus/test";
import { command, definePlugin } from "../src/index.ts";
import { createTestContext, runCommand } from "../src/testing.ts";

test("a deferred command responds privately through its queued task once", async () => {
  const h = createTestContext({
    grants: ["scheduler", "discord.channels.read"],
    members: [{ id: "1" }],
    channels: { help: { name: "help" } },
    server: { everyonePermissions: ["ViewChannel", "ReadMessageHistory"] },
  });
  const plugin = definePlugin({
    commands: {
      ask: command({
        description: "Ask",
        async run(_ctx, it) {
          await it.deferTask("answer", "job-id");
        },
      }),
    },
    tasks: {
      async answer(_ctx, payload, task) {
        expect(payload).toBe("job-id");
        expect(task?.interaction?.userId).toBe("1");
        await task?.interaction?.reply("Done");
      },
    },
  });
  const r = await runCommand(plugin, h, "ask", { user: "1", channel: "help" });
  expect(r.deferred).toEqual({ ephemeral: true });
  expect(h.scheduled).toHaveLength(1);
  await r.runDeferredTask?.();
  await r.runDeferredTask?.();
  expect(h.scheduled).toHaveLength(0);
  expect(r.replies).toEqual(["Done"]);
});
