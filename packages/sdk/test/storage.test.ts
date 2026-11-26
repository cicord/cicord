import { describe, expect, it } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";

describe("versioned storage", () => {
  it("checks versions and rolls back KV and ranking changes together", async () => {
    const {
      ctx: { kv },
    } = createTestContext();
    await kv.set("record", null);
    expect(await kv.getMany(["missing", "record"])).toEqual([undefined, null]);
    const version = (await kv.getEntry("record"))!.version;
    const rivals = Array.from({ length: 8 }, () =>
      kv.atomic().check("record", version).set("record", 1).rankSet("xp", "b", 5).commit(),
    );
    expect((await Promise.all(rivals)).filter((x) => x.ok)).toHaveLength(1);
    await kv.ranking("xp").set("a", 5);
    expect(await kv.ranking("xp").top({ offset: 1 })).toEqual([{ member: "b", score: 5, rank: 2 }]);
    await expect(
      kv.atomic().rankRemove("xp", "a").set("bad", "string").increment("bad").commit(),
    ).rejects.toThrow();
    expect(await kv.ranking("xp").count()).toBe(2);
    expect(await kv.get("bad")).toBeUndefined();
    await kv.delete("record");
    await kv.set("record", null);
    expect((await kv.getEntry("record"))!.version).not.toBe(version);
  });
});
