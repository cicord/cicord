import { expect, test } from "vite-plus/test";
import { createTestContext, runContextMenu } from "../src/testing.ts";
import { definePlugin } from "../src/index.ts";

test("resolves context targets, checks the grant, and shares normal guild messages", async () => {
  const h = createTestContext({
    roles: { moderator: { name: "Mod", permissions: ["ManageMessages"] } },
    members: [{ id: "10", roles: ["moderator"] }, { id: "20" }],
    channels: { news: { name: "news", type: "announcement" } },
  });
  const ch = h.channel("news")!;
  const source = await ch.send({
    layout: [{ type: "container", components: [{ type: "text", content: "Read this" }] }],
  });
  const p = definePlugin({
    contextMenus: {
      Inspect: {
        type: "message",
        permissions: ["ManageMessages"],
        async run(_ctx, it) {
          await it.reply(it.target.url);
        },
      },
    },
  });
  expect(
    (await runContextMenu(p, h, "Inspect", { user: "10", channel: "news", targetId: source.id }))
      .replies,
  ).toEqual([source.url]);
  await expect(
    runContextMenu(p, h, "Inspect", { user: "20", channel: "news", targetId: source.id }),
  ).rejects.toThrow("permissions");
  await expect(
    ch.send({ layout: [{ type: "text", content: "mixed" }], content: "legacy" }),
  ).rejects.toThrow("layout");
  await source.pin();
  expect((await ch.pins())[0]?.id).toBe(source.id);
  h.setReaction(source.id, "⭐", "20");
  await source.react("⭐");
  expect(source.reactions).toEqual([{ emoji: "⭐", count: 2, me: true }]);
  await source.removeReaction("⭐");
  expect((await source.reactionUsers("⭐")).map((u) => u.id)).toEqual(["20"]);
});
