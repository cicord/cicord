import { describe, expect, test } from "vite-plus/test";
import type { Member, Role, Snowflake } from "@cicord/sdk";
import { createTestContext } from "../src/testing.ts";

describe("role.members()", () => {
  const options = {
    roles: { staff: { name: "Staff" }, mod: { name: "Mod", id: "900" } },
    members: [
      { id: "100", roles: ["staff"] },
      { id: "9", roles: ["staff", "mod"], nickname: "Nine" },
      { id: "77", roles: ["staff"] },
      { id: "5" },
    ],
  };

  test("lists the holders in user id order, with limit and after", async () => {
    const t = createTestContext(options);
    const staff = t.ctx.server.role("staff");
    expect((await staff.members()).map((m) => m.id)).toEqual(["9", "77", "100"]);
    expect((await staff.members({ limit: 1 })).map((m) => m.id)).toEqual(["9"]);
    expect((await staff.members({ after: "9" })).map((m) => m.id)).toEqual(["77", "100"]);
    const [nine] = await t.ctx.server.role({ id: "900" }).members();
    expect([nine?.displayName, nine?.hasRole("mod")]).toEqual(["Nine", true]);
    // Live: role changes and removed members are reflected.
    await t.member("5")!.addRole("staff");
    t.removeMember("100");
    expect((await staff.members()).map((m) => m.id)).toEqual(["5", "9", "77"]);
  });

  test("roles stay plain data (members() is on the prototype)", () => {
    const t = createTestContext(options);
    expect(t.ctx.server.role("staff")).toEqual({
      id: "role-staff",
      key: "staff",
      declaredMembers: false,
      managed: false,
      name: "Staff",
      mention: "<@&role-staff>",
      position: 1,
      mentionable: false,
      permissions: "0",
    });
    expect(JSON.parse(JSON.stringify(t.ctx.server.role("mod")))).toMatchObject({ id: "900" });
    expect(typeof t.member("9")!.roles[0].members).toBe("function");
  });

  test("checks the limit, the capability and the ServerMembers intent", async () => {
    const t = createTestContext(options);
    const staff = t.ctx.server.role("staff");
    for (const limit of [0, 1001, 1.5])
      await expect(staff.members({ limit })).rejects.toThrow(TypeError);
    const noGrant = createTestContext({ ...options, grants: ["discord.channels.read"] });
    await expect(noGrant.ctx.server.role("staff").members()).rejects.toMatchObject({
      name: "CapabilityError",
      capability: "discord.members.read",
    });
    const noIntent = createTestContext({ ...options, intents: ["ServerMessages"] });
    await expect(noIntent.ctx.server.role("staff").members()).rejects.toThrow(/ServerMembers/);
    const withIntent = createTestContext({ ...options, intents: ["ServerMembers"] });
    expect(await withIntent.ctx.server.role("staff").members()).toHaveLength(3);
    expect(() => t.ctx.server.role("nope")).toThrow(/unknown role/);
  });
});

type Assert<T extends true> = T;
type Equals<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false;
export type _Types = [
  Assert<
    Equals<
      Parameters<Role["members"]>,
      [options?: { limit?: number; after?: Snowflake } | undefined]
    >
  >,
  Assert<Equals<ReturnType<Role["members"]>, Promise<Member[]>>>,
];
