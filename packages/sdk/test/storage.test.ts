import { describe, expect, it } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";
import { testStorage } from "../src/testing-storage.ts";

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

// Runtime limits: internal/state/advanced.go:60-74 and migration 0011's triggers.
describe("storage limits like the runtime", () => {
  it("caps one ranking at 250,000 members", async () => {
    const { kv } = createTestContext().ctx;
    const board = kv.ranking("big");
    for (let i = 0; i < 250_000; i++) await board.set("m" + i, i);
    await expect(board.set("one-more", 1)).rejects.toThrow("ranking member limit exceeded");
    // Updating a member and filling another board still work.
    await board.set("m0", 5);
    await kv.ranking("other").set("one-more", 1);
    expect(await board.count()).toBe(250_000);
  });

  it("counts key and value bytes and ranking members against the installation quota", async () => {
    // Real 256 MiB would hold ~260 MB of strings: the same rules with a small budget.
    const data = new Map();
    const kv = testStorage(data, () => {}, {
      entries: 1_000_000,
      bytes: 100,
      boardMembers: 250_000,
      listBytes: 4 << 20,
    });
    await kv.set("a", "x".repeat(40)); // 1 + 42 bytes
    await kv.ranking("r").set("m", 1); // 1 + 1 + 16 bytes
    await expect(kv.set("b", "x".repeat(40))).rejects.toThrow("plugin storage quota exceeded");
    // An update that grows past the quota fails too, and leaves the value.
    await expect(kv.set("a", "x".repeat(80))).rejects.toThrow("plugin storage quota exceeded");
    expect(await kv.get("a")).toBe("x".repeat(40));
    await expect(kv.ranking("r").set("n".repeat(30), 1)).rejects.toThrow(
      "plugin storage quota exceeded",
    );
    // A failed transaction restores the byte count: freeing space then succeeds.
    await expect(kv.atomic().delete("a").set("c", "x".repeat(90)).commit()).rejects.toThrow(
      "plugin storage quota exceeded",
    );
    expect(await kv.get("a")).toBe("x".repeat(40));
    await kv.delete("a");
    await kv.set("b", "x".repeat(40));
    await kv.ranking("r").remove("m");
    await kv.set("c", "x".repeat(30));
  });

  it("uses the 256 MiB byte quota by default", async () => {
    const { kv } = createTestContext().ctx;
    const value = "x".repeat((1 << 20) - 2); // 1 MiB of JSON with its quotes
    // 256 entries of 1 MiB plus their keys cross 256 MiB on the last one.
    for (let i = 0; i < 255; i++) await kv.set("k" + i, value);
    await expect(kv.set("k255", value)).rejects.toThrow("plugin storage quota exceeded");
    await kv.set("small", 1);
  });

  it("ends a kv.list page before 4 MiB, with a cursor", async () => {
    const { kv } = createTestContext().ctx;
    const value = "x".repeat(1_000_000);
    for (let i = 0; i < 6; i++) await kv.set("k" + i, value);
    const first = await kv.list({ limit: 1000 });
    expect(first.items.map((x) => x.key)).toEqual(["k0", "k1", "k2", "k3"]);
    expect(first.cursor).toBe("k3");
    const second = await kv.list({ limit: 1000, cursor: first.cursor });
    expect(second.items.map((x) => x.key)).toEqual(["k4", "k5"]);
    expect(second.cursor).toBeUndefined();
  });

  it("allows 128 operations in one kv.atomic and refuses the 129th", async () => {
    const { kv } = createTestContext().ctx;
    const tx = kv.atomic();
    for (let i = 0; i < 127; i++) tx.set("k" + i, i);
    tx.check("k0", null);
    expect(() => tx.set("k128", 1)).toThrow(new TypeError("kv.atomic: closed or full transaction"));
    expect(await tx.commit()).toEqual({ ok: true });
    expect(await kv.get("k126")).toBe(126);
  });
});
