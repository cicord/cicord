import { describe, expect, test } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";

describe("moderation tools", () => {
  test("lists bans by user ID, including an absent user, and lifts a ban idempotently", async () => {
    const t = createTestContext({ members: [{ id: "22" }] });
    await t.ctx.server.ban("22", { reason: "current" });
    await t.ctx.server.ban("9", { reason: "former" });
    expect((await t.ctx.server.bans({ limit: 1 }))[0]?.user.id).toBe("9");
    expect((await t.ctx.server.bans({ after: "9" }))[0]?.reason).toBe("current");
    await t.ctx.server.unban("9");
    await t.ctx.server.unban("9");
    expect(t.moderation.filter((m) => m.action === "unban")).toHaveLength(1);
    await expect(createTestContext({ grants: [] }).ctx.server.bans()).rejects.toThrow();
  });
  test("refuses replacement of roles with declared membership before changing the member", async () => {
    const t = createTestContext({
      roles: { staff: { name: "Staff", declaredMembers: true }, member: { name: "Member" } },
      members: [{ id: "42", roles: ["staff"] }],
    });
    const m = await t.ctx.server.member("42");
    await expect(m!.setRoles(["member"])).rejects.toThrow("declared in git");
    expect(m!.hasRole("staff")).toBe(true);
    expect(m!.hasRole("member")).toBe(false);
  });
  test("purge filters current history and checks content access", async () => {
    const t = createTestContext({ channels: { general: { name: "General" } } });
    const ch = t.ctx.server.channel("general");
    const kept = await ch.send("keep");
    const removed = await ch.send("example");
    expect(await ch.purge({ limit: 10, contains: "example" })).toEqual({ deleted: 1, skipped: 1 });
    expect(await ch.message(removed.id)).toBeUndefined();
    expect((await ch.message(kept.id))?.content).toBe("keep");
    const scoped = createTestContext({ intents: [], channels: { general: { name: "General" } } });
    await expect(
      scoped.ctx.server.channel("general").purge({ limit: 1, attachments: false }),
    ).rejects.toThrow("MessageContent");
  });
});
