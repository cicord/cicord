import { describe, expect, test } from "vite-plus/test";
import { CapabilityError, command, definePlugin } from "../src/index.ts";
import {
  callRoute,
  capabilityError,
  createTestContext,
  normalizeRouteResponse,
  runCommand,
} from "../src/testing.ts";

describe("runtime", () => {
  test("definePlugin and command are identity functions", () => {
    const def = { description: "hi", run() {} };
    expect(command(def)).toBe(def);
    const plugin = { commands: { hi: def } };
    expect(definePlugin(plugin)).toBe(plugin);
  });

  test("CapabilityError carries the capability", () => {
    const e = capabilityError("storage.kv");
    expect(e).toBeInstanceOf(CapabilityError);
    expect(e).toBeInstanceOf(Error);
    expect(e.name).toBe("CapabilityError");
    expect(e.capability).toBe("storage.kv");
    expect(new CapabilityError('missing capability "portal.links"').capability).toBe(
      "portal.links",
    );
  });
});

describe("testing harness", () => {
  const plugin = definePlugin({
    commands: {
      ping: command({
        description: "Ping",
        options: { who: { type: "string", description: "who" } },
        async run(ctx, interaction) {
          const n = await ctx.kv.increment("pings");
          await interaction.reply({
            content: `pong ${interaction.options.who ?? ""} ${n}`,
            ephemeral: true,
          });
        },
      }),
    },
    routes: {
      "GET /items/:id": {
        access: "member",
        handler: (_ctx, req) => ({ id: req.params["id"] ?? null, q: req.query["q"] ?? null }),
      },
      "POST /roles": {
        access: "member",
        async handler(_ctx, req) {
          await req.viewer?.member?.addRole("pc");
          return { status: 201, body: { ok: true } };
        },
      },
    },
  });

  test("routes: params, query, access and envelopes", async () => {
    const t = createTestContext({ roles: { pc: { name: "PC" } }, members: [{ id: "1" }] });
    expect(await callRoute(plugin, t, "GET /items/42?q=x", { viewer: "1" })).toEqual({
      status: 200,
      body: { id: "42", q: "x" },
      headers: {},
    });
    expect((await callRoute(plugin, t, "GET /items/42")).status).toBe(401);
    expect((await callRoute(plugin, t, "GET /items/42", { viewer: "2" })).status).toBe(403);
    expect((await callRoute(plugin, t, "GET /nope", { viewer: "1" })).status).toBe(404);
    const created = await callRoute(plugin, t, "POST /roles", { viewer: "1" });
    expect(created).toEqual({ status: 201, body: { ok: true }, headers: {} });
    expect(t.member("1")?.hasRole("pc")).toBe(true);
    expect(t.roleChanges).toEqual([{ member: "1", role: "pc", action: "add" }]);
  });

  test("grants are enforced when given", async () => {
    const t = createTestContext({
      grants: [],
      roles: { pc: { name: "PC" } },
      members: [{ id: "1" }],
    });
    await expect(callRoute(plugin, t, "POST /roles", { viewer: "1" })).rejects.toMatchObject({
      name: "CapabilityError",
      capability: "discord.roles.assign",
    });
  });

  test("commands record replies", async () => {
    const t = createTestContext();
    const r = await runCommand(plugin, t, "ping", { user: "7", options: { who: "bob" } });
    expect(r.replies).toEqual([{ content: "pong bob 1", ephemeral: true }]);
    expect(t.kv.get("pings")).toBe(1);
  });

  test("normalizeRouteResponse only unwraps pure envelopes", () => {
    expect(normalizeRouteResponse({ status: 418 })).toEqual({
      status: 418,
      body: undefined,
      headers: {},
    });
    expect(normalizeRouteResponse({ status: 1, other: true })).toEqual({
      status: 200,
      body: { status: 1, other: true },
      headers: {},
    });
    expect(normalizeRouteResponse([1, 2])).toEqual({ status: 200, body: [1, 2], headers: {} });
  });

  test("kv list paginates", async () => {
    const t = createTestContext();
    for (const k of ["a:1", "a:2", "a:3", "b:1"]) await t.ctx.kv.set(k, k);
    const first = await t.ctx.kv.list({ prefix: "a:", limit: 2 });
    expect(first.items.map((i) => i.key)).toEqual(["a:1", "a:2"]);
    const second = await t.ctx.kv.list({
      prefix: "a:",
      limit: 2,
      ...(first.cursor ? { cursor: first.cursor } : {}),
    });
    expect(second).toEqual({ items: [{ key: "a:3", value: "a:3" }] });
  });
});
