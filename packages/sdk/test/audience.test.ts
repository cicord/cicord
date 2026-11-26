import { expect, test } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";

test("private content follows the member's current role and individual overrides", async () => {
  const h = createTestContext({
    server: { ownerId: "1", everyonePermissions: ["ViewChannel", "ReadMessageHistory"] },
    roles: { staff: { name: "Staff" } },
    members: [{ id: "2", roles: ["staff"] }, { id: "3" }],
    channels: {
      public: { name: "General" },
      staff: {
        name: "Staff",
        overwrites: {
          "@everyone": { deny: ["ViewChannel"] },
          staff: { allow: ["ViewChannel"] },
          "member:3": { deny: ["ReadMessageHistory"] },
        },
      },
    },
  });
  const member = h.member("2")!;
  expect(member.canView("staff")).toBe(true);
  expect(h.member("3")!.canView("staff")).toBe(false);
  expect(h.ctx.server.compareAudience("staff", "public")).toBe("wider");
  expect(h.ctx.server.compareAudience("public", "staff")).toBe("narrower");
  await member.removeRole("staff");
  expect(member.canView("staff")).toBe(false);
  expect(h.ctx.server.compareAudience("missing", "public")).toBe("unknown");
});
