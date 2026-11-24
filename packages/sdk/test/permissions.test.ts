import { describe, expect, test } from "vite-plus/test";
import type { AllowedMentions, Member, Permission, Role, Server } from "@cicord/sdk";
import { command, definePlugin } from "../src/index.ts";
import { createTestContext, normalizeAllowedMentions, runCommand } from "../src/testing.ts";

describe("roles, permissions and access", () => {
  const t = createTestContext({
    server: { ownerId: "1", everyonePermissions: ["ViewChannel", "SendMessages"] },
    roles: {
      member: { name: "Member", mentionable: true },
      mod: { name: "Mod", position: 5, permissions: ["ModerateMembers", "KickMembers"] },
      admin: { name: "Admin", position: 9, permissions: ["Administrator"] },
    },
    access: { admins: { users: ["4"] }, moderators: { roles: ["mod"] } },
    members: [
      { id: "1" },
      { id: "2", roles: ["member", "mod"] },
      { id: "3", roles: ["admin"] },
      { id: "4" },
    ],
  });

  test("roles carry their position and mentionable", () => {
    const member = t.ctx.server.role("member");
    expect([member.position, member.mentionable]).toEqual([1, true]);
    expect(t.ctx.server.role("mod").position).toBe(5);
    expect(t.ctx.server.ownerId).toBe("1");
  });

  test("permissions are server-level: @everyone + roles; all for the owner and Administrator", () => {
    expect(t.member("2")?.permissions).toEqual([
      "KickMembers",
      "ViewChannel",
      "SendMessages",
      "ModerateMembers",
    ]);
    expect(t.member("3")?.permissions).toContain("BanMembers");
    expect(t.member("1")?.permissions).toContain("Administrator");
    expect(t.member("4")?.permissions).toEqual(["ViewChannel", "SendMessages"]);
  });

  test("access follows spec.access and the owner", () => {
    expect(["1", "2", "3", "4"].map((id) => t.member(id)?.access)).toEqual([
      "admin",
      "moderator",
      "member",
      "admin",
    ]);
  });
});

describe("allowedMentions id lists", () => {
  test("normalized like the host: objects become ids, invalid lists throw", () => {
    const role = { id: "42" };
    expect(
      normalizeAllowedMentions({ content: "x", allowedMentions: { roles: [role], users: [] } }),
    ).toEqual({
      roles: ["42"],
      users: [],
    });
    expect(normalizeAllowedMentions("x")).toBeUndefined();
    expect(() =>
      normalizeAllowedMentions({ content: "x", allowedMentions: { roles: ["staff"] } }),
    ).toThrow(/snowflake/);
    expect(() =>
      normalizeAllowedMentions({
        content: "x",
        allowedMentions: { users: Array.from({ length: 101 }, () => "1") },
      }),
    ).toThrow(/at most 100/);
  });

  test("sent messages and replies are checked; ids of the fakes are accepted", async () => {
    const t = createTestContext({
      roles: { staff: { name: "Staff" } },
      channels: { general: { name: "general" } },
      members: [{ id: "7" }],
    });
    const plugin = definePlugin({
      commands: {
        ping: command({
          description: "Ping",
          async run(ctx, interaction) {
            const staff = ctx.server.role("staff");
            await ctx.server
              .channel("general")
              .send({ content: staff.mention, allowedMentions: { roles: [staff], users: [] } });
            await interaction.reply({ content: "ok", allowedMentions: { users: ["not an id"] } });
          },
        }),
      },
    });
    await expect(runCommand(plugin, t, "ping", { user: "7" })).rejects.toThrow(/snowflake/);
    expect(t.sent[0]?.allowedMentions).toEqual({ roles: ["role-staff"], users: [] });
  });
});

// Type-level: the new SDK fields are optional (older engines), the lists accept ids and objects.
// A wrong type makes `Assert` fail to compile.
type Assert<T extends true> = T;
type Equals<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false;
export type _Types = [
  Assert<Equals<Role["position"], number | undefined>>,
  Assert<Equals<Role["mentionable"], boolean | undefined>>,
  Assert<Equals<Member["permissions"], readonly Permission[] | undefined>>,
  Assert<Equals<Member["access"], "member" | "moderator" | "admin" | undefined>>,
  Assert<Equals<Server["ownerId"], string | undefined>>,
  Assert<
    Equals<
      AllowedMentions["roles"],
      boolean | readonly (string | { readonly id: string })[] | undefined
    >
  >,
  Assert<Equals<AllowedMentions["users"], AllowedMentions["roles"]>>,
];
