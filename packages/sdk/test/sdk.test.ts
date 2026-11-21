import { describe, expect, test, vi } from "vite-plus/test";
import { CapabilityError, command, definePlugin } from "../src/index.ts";
import {
  applyDefaults,
  callRoute,
  capabilityError,
  createTestContext,
  normalizeRouteResponse,
  runCommand,
  runComponent,
  submitModal,
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

describe("interactions", () => {
  const plugin = definePlugin({
    commands: {
      ask: command({
        description: "Ask",
        async run(ctx, it) {
          if (!it.channel) throw new Error("no channel");
          const msg = await it.channel.send({
            content: "Vote",
            components: [
              { type: "row", components: [{ type: "button", id: "vote:7", label: "7" }] },
            ],
          });
          await ctx.kv.set("msg", msg.id);
          await it.reply({ content: "posted", ephemeral: true });
          await it.followUp("again");
        },
      }),
      form: command({
        description: "Form",
        async run(_ctx, it) {
          await it.showModal({ id: "form:x", title: "Form", fields: [{ id: "a", label: "A" }] });
        },
      }),
    },
    components: {
      async vote(ctx, it) {
        await ctx.kv.increment(`votes:${it.componentId.split(":")[1]}`);
        await it.update({ content: `voted by ${it.user.id} on ${it.message.id}` });
        await it.reply("thanks"); // after update: a follow-up
      },
    },
    modals: {
      async form(_ctx, it) {
        await it.defer({ ephemeral: true });
        await it.reply(`got ${it.fields["a"]} for ${it.modalId}`);
        await expect(it.reply("twice")).rejects.toThrow("already answered");
      },
    },
  });

  test("commands, components with data ids and modals", async () => {
    const t = createTestContext({
      grants: ["discord.messages.send", "storage.kv"],
      channels: { general: { name: "general" } },
    });
    const r = await runCommand(plugin, t, "ask", { user: "1", channel: "general" });
    expect(r.replies).toEqual([{ content: "posted", ephemeral: true }]);
    expect(r.followUps).toEqual(["again"]);
    const id = t.kv.get("msg") as string;
    expect(t.sent).toEqual([expect.objectContaining({ to: "general", id })]);

    const c = await runComponent(plugin, t, "vote:7", { user: "2", message: id });
    expect(c.updates).toEqual([{ content: `voted by 2 on ${id}` }]);
    expect(c.followUps).toEqual(["thanks"]);
    expect(t.kv.get("votes:7")).toBe(1);

    const f = await runCommand(plugin, t, "form", { user: "1" });
    expect(f.modal?.id).toBe("form:x");
    const m = await submitModal(plugin, t, "form:x", { user: "1", fields: { a: "hello" } });
    expect(m.deferred).toEqual({ ephemeral: true });
    expect(m.replies).toEqual(["got hello for form:x"]);
    await expect(runComponent(plugin, t, "nope", { user: "1" })).rejects.toThrow("no component");
  });

  test("followUp before a response throws", async () => {
    const p = definePlugin({
      commands: {
        x: command({ description: "x", run: async (_c, it) => void (await it.followUp("no")) }),
      },
    });
    await expect(runCommand(p, createTestContext(), "x", { user: "1" })).rejects.toThrow(
      "not been answered",
    );
  });
});

describe("fakes", () => {
  test("schema defaults are applied like the runtime (objects, array items, $ref)", () => {
    const schema = {
      type: "object",
      $defs: { color: { type: "string", default: "#fff" } },
      properties: {
        title: { type: "string", default: "Hi" },
        tint: { $ref: "#/$defs/color" },
        steps: {
          type: "array",
          items: { type: "object", properties: { multiple: { type: "boolean", default: false } } },
        },
        nested: { type: "object", properties: { n: { type: "integer", default: 3 } } },
        map: {
          type: "object",
          additionalProperties: { type: "object", properties: { k: { default: 1 } } },
        },
      },
    };
    const input = { steps: [{}, { multiple: true }], map: { a: {} } };
    expect(applyDefaults(schema, input)).toEqual({
      title: "Hi",
      tint: "#fff",
      steps: [{ multiple: false }, { multiple: true }],
      nested: { n: 3 },
      map: { a: {} },
    });
    expect(input.steps[0]).toEqual({}); // not mutated
    const t = createTestContext({ schema, config: {} });
    expect((t.ctx.config as Record<string, unknown>)["title"]).toBe("Hi");
  });

  test("kv ttl, channels, moderation and fetch", async () => {
    vi.useFakeTimers();
    try {
      const t = createTestContext({
        roles: { staff: { name: "Staff" } },
        channels: { tickets: { name: "Tickets", type: "category" } },
        members: [{ id: "1" }],
        fetch: (url) => ({ status: 201, body: { url } }),
      });
      await t.ctx.kv.set("a", 1, { ttlSeconds: 10 });
      vi.advanceTimersByTime(9_000);
      expect(await t.ctx.kv.get("a")).toBe(1);
      vi.advanceTimersByTime(2_000);
      expect(await t.ctx.kv.get("a")).toBeUndefined();
      expect((await t.ctx.kv.list()).items).toEqual([]);

      const ch = await t.ctx.server.createChannel({
        name: "ticket-1",
        category: "tickets",
        overwrites: {
          "@everyone": { deny: ["ViewChannel"] },
          staff: { allow: ["ViewChannel"] },
          "member:1": {},
        },
      });
      expect(t.ctx.server.channel({ id: ch.id }).name).toBe("ticket-1");
      await ch.send("hello");
      expect((await ch.messages()).map((m) => m.content)).toEqual(["hello"]);
      await t.ctx.server.deleteChannel({ id: ch.id }, "closed");
      expect(t.deletedChannels).toEqual([{ id: ch.id, reason: "closed" }]);
      await expect(t.ctx.server.deleteChannel("tickets")).rejects.toThrow("not owned");

      const m = t.member("1")!;
      await m.send("bye");
      await m.timeout(60_000, "calm");
      await m.kick("out");
      expect(t.moderation).toEqual([
        { member: "1", action: "timeout", durationMs: 60_000, reason: "calm" },
        { member: "1", action: "kick", reason: "out" },
      ]);
      expect(t.sent.at(-1)).toMatchObject({ to: "dm:1", message: "bye" });

      const res = await t.ctx.http.fetch("https://hooks.example.com/x", { method: "POST" });
      expect(res.status).toBe(201);
      expect(await res.json()).toEqual({ url: "https://hooks.example.com/x" });
      expect(t.requests).toEqual([
        { url: "https://hooks.example.com/x", init: { method: "POST" } },
      ]);
    } finally {
      vi.useRealTimers();
    }
  });
});
