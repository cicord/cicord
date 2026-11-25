import { describe, expect, test } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";

describe("voice", () => {
  const options = {
    channels: {
      hub: { name: "➕ Create a channel", type: "voice" as const, id: "500", categoryId: "300" },
      voice: { name: "Voice", type: "category" as const, id: "300" },
      general: { name: "general" },
    },
    roles: { staff: { name: "Staff" } },
    members: [
      { id: "1", username: "alice" },
      { id: "2", username: "bob" },
    ],
  };

  test("setVoice returns the update of a join, a move and a leave", async () => {
    const t = createTestContext(options);
    const join = t.setVoice("1", "hub");
    expect([
      join.user.id,
      join.member?.id,
      join.channel?.id,
      join.channel?.categoryId,
      join.previous,
    ]).toEqual(["1", "1", "500", "300", undefined]);
    const room = await t.ctx.server.createChannel({
      name: "Room",
      type: "voice",
      category: { id: "300" },
      userLimit: 2,
    });
    const move = t.setVoice("1", room.id);
    expect([move.channel?.id, move.previous?.id]).toEqual([room.id, "500"]);
    expect((await t.ctx.server.voiceMembers(room)).map((m) => m.id)).toEqual(["1"]);
    const leave = t.setVoice("1", null);
    expect([leave.channel, leave.previous?.id, t.voiceChannel("1")]).toEqual([
      undefined,
      room.id,
      undefined,
    ]);
    expect(() => t.setVoice("1", "general")).toThrow("not a voice channel");
  });

  test("moveTo and disconnect need discord.voice.move and a connected member", async () => {
    const t = createTestContext({ ...options, grants: ["discord.channels.manage"] });
    const alice = t.member("1")!;
    await expect(alice.moveTo("hub")).rejects.toMatchObject({ capability: "discord.voice.move" });
    const u = createTestContext({
      ...options,
      grants: ["discord.voice.move", "discord.channels.manage"],
    });
    const bob = u.member("2")!;
    await expect(bob.moveTo("hub")).rejects.toThrow("not connected to voice");
    u.setVoice("2", "hub");
    const room = await u.ctx.server.createChannel({ name: "Room", type: "voice" });
    await bob.moveTo(room, "to your room");
    expect(u.voiceChannel("2")).toBe(room.id);
    await bob.disconnect();
    expect(u.voiceMoves).toEqual([
      { member: "2", channel: room.id, reason: "to your room" },
      { member: "2", channel: null },
    ]);
  });

  test("editChannel patches the overwrites of owned channels", async () => {
    const t = createTestContext(options);
    const room = await t.ctx.server.createChannel({
      name: "Room",
      type: "voice",
      overwrites: { "member:1": { allow: ["Connect", "MoveMembers"] } },
    });
    await t.ctx.server.editChannel(room, {
      name: "Alice's room",
      userLimit: 3,
      overwrites: { "@everyone": { deny: ["Connect"] } },
    });
    await t.ctx.server.editChannel(
      { id: room.id },
      {
        overwrites: {
          "@everyone": { clear: ["Connect"] },
          "member:2": { allow: ["Connect"] },
          "member:1": null,
        },
      },
    );
    expect(t.overwrites(room.id)).toEqual({
      "@everyone": { allow: [], deny: [] },
      "member:2": { allow: ["Connect"], deny: [] },
    });
    expect(room.name).toBe("Alice's room");
    expect(t.editedChannels.map((e) => e.options.userLimit)).toEqual([3, undefined]);
    await expect(t.ctx.server.editChannel("general", { name: "x" })).rejects.toThrow("not owned");
    await expect(t.ctx.server.editChannel(room, { userLimit: 100 })).rejects.toThrow("userLimit");
    await expect(t.ctx.server.createChannel({ name: "t", userLimit: 2 })).rejects.toThrow(
      "only for voice",
    );
    await expect(
      t.ctx.server.editChannel(room, {
        overwrites: { "@everyone": { allow: ["Connect"], deny: ["Connect"] } },
      }),
    ).rejects.toThrow("more than one");
    // Deleting a voice channel disconnects its members.
    t.setVoice("2", room.id);
    await t.ctx.server.deleteChannel(room);
    expect(t.voiceChannel("2")).toBeUndefined();
  });

  test("voiceMembers needs the ServerVoiceStates intent when intents are given", async () => {
    const t = createTestContext({ ...options, intents: ["ServerMembers"] });
    await expect(t.ctx.server.voiceMembers("hub")).rejects.toThrow("ServerVoiceStates");
  });
});
