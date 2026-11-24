import { CapabilityError, command, definePlugin } from "@cicord/sdk";

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

export default definePlugin({
  events: {
    // No MessageContent intent: content and attachments must be blank (unless the bot is
    // mentioned), even though another plugin of the same bot declares MessageContent.
    async messageCreate(ctx, message) {
      await ctx.kv.set(`seen:${message.id}`, { content: message.content, attachments: message.attachments.length });
      if (message.content.includes(`<@${ctx.bot.user.id}>`)) await message.reply("you called?");
    },
  },
  commands: {
    "probe-cap": command({
      description: "calls an API without its capability",
      async run(_ctx, it) {
        try {
          await _ctx.server.member(it.user.id); // discord.members.read is not granted
          await it.reply("no error");
        } catch (e) {
          const err = e as CapabilityError;
          await it.reply(JSON.stringify({ name: err.name, capability: err.capability, isCapabilityError: e instanceof CapabilityError }));
        }
      },
    }),
    "probe-throw": command({
      description: "throws",
      run() {
        throw new Error("boom");
      },
    }),
    "probe-spin": command({
      description: "runaway loop",
      run() {
        for (;;) {
          /* spin */
        }
      },
    }),
    "probe-oom": command({
      description: "exhausts the memory limit",
      run() {
        const hog: ArrayBuffer[] = [];
        for (;;) hog.push(new ArrayBuffer(4 * 1024 * 1024));
      },
    }),
    "probe-kill": command({
      description: "ignores the CPU budget inside native calls (hard stop)",
      run() {
        const s = "ab".repeat(1e6);
        for (let i = 0; i < 1e6; i++) s.indexOf("zz");
      },
    }),
    "probe-ping": command({
      description: "pong",
      async run(_ctx, it) {
        await it.reply("pong");
      },
    }),
    "probe-slow": command({
      description: "answers after a while (auto-defer)",
      async run(_ctx, it) {
        console.log("probe slow start");
        await sleep(400);
        await it.reply({
          content: "slow done",
          components: [{ type: "row", components: [{ type: "button", id: "slow", label: "Slow" }] }],
        });
      },
    }),
  },
  components: {
    async slow(_ctx, it) {
      await sleep(400);
      await it.update({ content: "slow click done", components: [] });
    },
  },
  routes: {
    "GET /seen/:id": {
      access: "public",
      async handler(ctx, req) {
        return (await ctx.kv.get(`seen:${req.params["id"]}`)) ?? null;
      },
    },
  },
});
