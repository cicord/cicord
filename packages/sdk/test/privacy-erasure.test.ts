import { expect, test } from "vite-plus/test";
import { definePlugin } from "../src/index.ts";
import { createTestContext } from "../src/testing.ts";

test("administrative erasure removes tagged documents but preserves another member's records", async () => {
  const h = createTestContext();
  const idx = h.ctx.index("audit");
  for (const id of ["14", "140"])
    await idx.upsert(id, { text: "observation", tags: ["member-" + id] });
  const p = definePlugin({
    privacy: {
      async export() {
        return null;
      },
      async erase(ctx, id) {
        await expect(ctx.privacy.pseudonym(id + "1")).rejects.toThrow("scoped");
        return { erased: 0 };
      },
    },
  });
  await h.eraseMember(p, "14");
  expect((await idx.search("observation")).map((x) => x.id)).toEqual(["140"]);
  const revoked = createTestContext({ grants: [] });
  await expect(revoked.ctx.index("audit").remove("missing")).rejects.toThrow();
  const retry = definePlugin({
    privacy: {
      async export() {
        return null;
      },
      async erase(ctx) {
        await ctx.index("audit").remove("missing");
        return { erased: 0 };
      },
    },
  });
  expect(await revoked.eraseMember(retry, "14")).toEqual({ erased: 0 });
});
