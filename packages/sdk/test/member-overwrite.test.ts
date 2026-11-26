import { expect, test } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";
test("memberOverwrite returns the member's own live row, including a synchronized category copy", async () => {
  const h = createTestContext({
    channels: {
      cat: {
        name: "Rooms",
        type: "category",
        id: "300",
        overwrites: {
          "member:42": { allow: ["Connect"], deny: ["Speak"] },
          "role:42": { allow: ["ViewChannel"] },
        },
      },
    },
    members: [{ id: "42" }],
  });
  const cat = h.ctx.server.channel({ id: "300" });
  const room = await h.ctx.server.createChannel({
    name: "room",
    type: "voice",
    category: { id: "300" },
  });
  expect(await room.memberOverwrite("42")).toEqual({ allow: ["Connect"], deny: ["Speak"] });
  expect(await room.memberOverwrite("43")).toBeUndefined();
  await h.ctx.server.editChannel(room, { overwrites: { "member:42": { deny: ["Connect"] } } });
  expect(await room.memberOverwrite("42")).toEqual({ allow: [], deny: ["Connect", "Speak"] });
  expect(await cat.memberOverwrite("42")).toEqual({ allow: ["Connect"], deny: ["Speak"] });
  await h.ctx.server.deleteChannel(room);
  await expect(room.memberOverwrite("42")).rejects.toThrow();
});
test("memberOverwrite rejects DMs and checks the calling grant", async () => {
  const dm = createTestContext({ channels: { inbox: { name: "inbox", type: "dm" } } });
  await expect(dm.channel("inbox")!.memberOverwrite("42")).rejects.toThrow();
  const h = createTestContext({ grants: [], channels: { room: { name: "room" } } });
  await expect(h.channel("room")!.memberOverwrite("42")).rejects.toThrow("discord.channels.read");
});
