import { expect, test, vi } from "vite-plus/test";
import { CapabilityError, definePlugin, InteropError } from "../src/index.ts";
import {
  callProvider,
  contractViolation,
  createTestContext,
  fakeProviders,
  readOnlyContext,
} from "../src/testing.ts";

const searchContract = {
  input: {
    type: "object",
    properties: { q: { type: "string", minLength: 1, maxLength: 64 } },
    required: ["q"],
    additionalProperties: false,
  },
  output: {
    type: "object",
    properties: { names: { type: "array", items: { type: "string" }, maxItems: 10 } },
    required: ["names"],
    additionalProperties: false,
  },
};

// A provider whose catalog lives in module scope, filled by onStart (community C-01): the
// host runs each provider call in a fresh realm without onStart, so it sees nothing.
function moduleStateProvider() {
  const catalog = new Map<string, string>();
  return definePlugin({
    async onStart(ctx) {
      for (const { value } of (await ctx.kv.list<string>({ prefix: "tag:" })).items)
        catalog.set(value.toLowerCase(), value);
    },
    queries: {
      search: {
        run(_ctx, input: { q: string }) {
          return { names: [...catalog.values()].filter((n) => n.toLowerCase().includes(input.q)) };
        },
      },
    },
  });
}

// The same provider reading durable state inside the query works in every realm.
function kvProvider() {
  return definePlugin({
    queries: {
      search: {
        async run(ctx, input: { q: string }) {
          const page = await ctx.kv.list<string>({ prefix: "tag:" });
          return {
            names: page.items.map((i) => i.value).filter((n) => n.toLowerCase().includes(input.q)),
          };
        },
      },
      sneaky: {
        async run(ctx) {
          await ctx.kv.set("written", true);
          return { names: [] };
        },
      },
    },
  });
}

test("provider calls run in a fresh realm without onStart, like the host", async () => {
  const h = createTestContext();
  await h.ctx.kv.set("tag:1", "Desk");
  const live = moduleStateProvider();
  await live.onStart!(h.ctx);
  // Unit-calling the live module hides the bug: its onStart filled the map.
  const unit = { caller: { plugin: "c", bot: "b", instance: "c", source: "", verified: false } };
  expect(await live.queries!.search.run(h.ctx, { q: "desk" }, unit)).toEqual({
    names: ["Desk"],
  });
  // The host's view: a fresh module, no onStart.
  const empty = await callProvider({
    load: moduleStateProvider,
    harness: h,
    query: "search",
    input: { q: "desk" },
    contract: searchContract,
  });
  expect(empty).toEqual({ names: [] });
  const ok = await callProvider({
    load: kvProvider,
    harness: h,
    query: "search",
    input: { q: "desk" },
    contract: searchContract,
  });
  expect(ok).toEqual({ names: ["Desk"] });
});

test("a loader that returns an already-run module is refused", async () => {
  const h = createTestContext();
  const live = kvProvider();
  await expect(
    callProvider({ load: () => live, harness: h, query: "search", input: { q: "x" }, live }),
  ).rejects.toThrow("vi.resetModules");
  const once = kvProvider();
  await callProvider({ load: () => once, harness: h, query: "search", input: { q: "x" } });
  await expect(
    callProvider({ load: () => once, harness: h, query: "search", input: { q: "x" } }),
  ).rejects.toThrow("already ran");
  // The usual Vitest loader: a fresh module registry, then a dynamic import.
  const loaded = await callProvider({
    load: async () => {
      vi.resetModules();
      return import("./fixtures/kv-provider.ts");
    },
    harness: h,
    query: "search",
    input: { q: "x" },
  });
  expect(loaded).toEqual({ names: [] });
});

test("queries are read-only and provider errors map to host codes", async () => {
  const h = createTestContext();
  const err = await callProvider({
    load: kvProvider,
    harness: h,
    query: "sneaky",
    input: { q: "x" },
  }).catch((e: unknown) => e);
  expect(err).toBeInstanceOf(InteropError);
  expect((err as InteropError).code).toBe("handler_error");
  expect((err as InteropError).message).toContain("read-only");
  expect(h.kv.has("written")).toBe(false);

  const ro = readOnlyContext(h.ctx);
  expect(() => ro.kv.set("a", 1)).toThrow(CapabilityError);
  expect(() => ro.schedule.after(1, "x")).toThrow(CapabilityError);
  expect(() => ro.status.set("k", "m")).toThrow(CapabilityError);
  expect(() => ro.features.report("f", true)).toThrow(CapabilityError);
  expect(() => ro.index("i").upsert("1", { text: "t" })).toThrow(CapabilityError);
  expect(() => ro.kv.ranking("r").add("1", 1)).toThrow(CapabilityError);
  await expect(ro.kv.ranking("r").count()).resolves.toBe(0);
  const batch = ro.kv.atomic().set("a", 1).delete("b");
  expect(() => batch.commit()).toThrow(CapabilityError);
  await expect(ro.kv.get("tag:none")).resolves.toBeUndefined();
  expect(ro.features.enabled("x")).toBe(false);

  const denied = definePlugin({
    queries: {
      q: {
        run() {
          throw new InteropError("denied", "no access");
        },
      },
      nf: {
        run() {
          throw new InteropError("not_found", "other installation");
        },
      },
      bad: {
        run() {
          return { names: [1] };
        },
      },
    },
  });
  const code = (query: string, contract?: typeof searchContract) =>
    callProvider({
      load: () => ({ ...denied }),
      harness: h,
      query,
      input: { q: "x" },
      contract,
    }).catch((e: InteropError) => e.code);
  expect(await code("q")).toBe("denied");
  expect(await code("nf")).toBe("handler_error");
  expect(await code("bad", searchContract)).toBe("handler_error");
});

test("contract input violations are invalid_input on both sides of a call", async () => {
  const h = createTestContext();
  const err = await callProvider({
    load: kvProvider,
    harness: h,
    query: "search",
    input: { q: "x", publisher: { bot: "b", instance: "i" } },
    contract: searchContract,
  }).catch((e: InteropError) => e);
  expect((err as InteropError).code).toBe("invalid_input");
  expect(contractViolation(searchContract.input, { q: "" })).toContain("shorter than 1");
  expect(() => contractViolation({ format: "date" }, "x")).toThrow("not supported");

  // Consumer side: a stubbed peer still validates the consumer's input, so a consumer that
  // sends an undeclared field fails in the unit test as it would in the runtime.
  const consumer = createTestContext({
    plugins: fakeProviders({
      polls: {
        contracts: { search: searchContract },
        queries: { search: () => ({ names: ["a"] }) },
      },
    }),
  });
  expect(await consumer.ctx.plugins.query("polls", "search", { q: "a" })).toEqual({ names: ["a"] });
  await expect(
    consumer.ctx.plugins.query("polls", "search", { q: "a", extra: 1 }),
  ).rejects.toMatchObject({ code: "invalid_input" });
  await expect(consumer.ctx.plugins.query("tags", "search", { q: "a" })).rejects.toMatchObject({
    code: "not_found",
  });
  // A peer backed by the real provider module runs in its own fresh realm.
  const provider = createTestContext();
  await provider.ctx.kv.set("tag:1", "Desk");
  const wired = createTestContext({
    plugins: fakeProviders({
      tags: {
        contracts: { search: searchContract },
        realm: { load: kvProvider, harness: provider },
      },
    }),
  });
  expect(await wired.ctx.plugins.query("tags", "search", { q: "desk" })).toEqual({
    names: ["Desk"],
  });
});

test("privacy.pseudonym is scoped to the subject of the current privacy hook", async () => {
  const h = createTestContext();
  await expect(h.ctx.privacy.pseudonym("1")).rejects.toThrow(
    "privacy.pseudonym is scoped to the member of the current privacy hook",
  );
  const seen: string[] = [];
  const p = definePlugin({
    events: {
      async memberJoin(ctx, m) {
        await ctx.privacy.pseudonym(m.id);
      },
    },
    privacy: {
      async export(ctx, id) {
        seen.push(await ctx.privacy.pseudonym(id));
        await expect(ctx.privacy.pseudonym(id + "0")).rejects.toThrow("scoped");
        return null;
      },
      async erase(ctx, id) {
        seen.push(await ctx.privacy.pseudonym(id));
        return { erased: 0 };
      },
    },
  });
  const member = h.addMember({ id: "1" });
  await expect(p.events!.memberJoin!(h.ctx, member)).rejects.toThrow("scoped");
  await h.exportMember(p, "1");
  await h.eraseMember(p, "1");
  expect(seen[0]).toMatch(/^deleted-user-[0-9a-f]{16}$/);
  expect(seen[1]).toBe(seen[0]);
});
