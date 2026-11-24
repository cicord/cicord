import { describe, expect, test } from "vite-plus/test";
import { command, definePlugin, subcommand } from "../src/index.ts";
import { createTestContext, runAutocomplete, runCommand } from "../src/testing.ts";

const fruits = ["apple", "apricot", "banana"];

const plugin = definePlugin({
  commands: {
    fruit: command({
      description: "Fruits",
      subcommands: {
        pick: subcommand({
          description: "Pick one",
          options: {
            name: {
              type: "string",
              description: "Fruit",
              required: true,
              maxLength: 20,
              autocomplete: (_ctx, interaction, focused) =>
                fruits
                  .filter((f) => f.startsWith(focused.value))
                  .map((f) => ({ name: `${f} (${interaction.subcommand})`, value: f })),
            },
            count: {
              type: "integer",
              description: "How many",
              autocomplete: () => [{ name: "x", value: 1.5 }],
            },
            many: {
              type: "string",
              description: "Many",
              autocomplete: () =>
                Array.from({ length: 26 }, (_, i) => ({ name: `n${i}`, value: `v${i}` })),
            },
            plain: { type: "string", description: "No autocomplete" },
          },
          async run(_ctx, interaction) {
            // Typed per subcommand: name is a required string.
            const name: string = interaction.options.name;
            await interaction.reply(`${interaction.command}/${interaction.subcommand}: ${name}`);
          },
        }),
      },
      groups: {
        admin: {
          description: "Admin",
          subcommands: {
            purge: subcommand({
              description: "Purge",
              async run(_ctx, interaction) {
                await interaction.reply(`${interaction.group} ${interaction.subcommand}`);
              },
            }),
          },
        },
      },
    }),
    ping: command({ description: "Ping", run: (_ctx, i) => i.reply(i.subcommand ?? "pong") }),
  },
});

describe("subcommands", () => {
  test("runCommand takes a subcommand path", async () => {
    const t = createTestContext();
    const r = await runCommand(plugin, t, "fruit pick", { user: "1", options: { name: "kiwi" } });
    expect(r.replies).toEqual(["fruit/pick: kiwi"]);
    expect((await runCommand(plugin, t, "fruit admin purge", { user: "1" })).replies).toEqual([
      "admin purge",
    ]);
    expect((await runCommand(plugin, t, "ping", { user: "1" })).replies).toEqual(["pong"]);
  });

  test("unknown or incomplete paths throw", async () => {
    const t = createTestContext();
    await expect(runCommand(plugin, t, "fruit", { user: "1" })).rejects.toThrow(
      "needs a subcommand",
    );
    await expect(runCommand(plugin, t, "fruit nope", { user: "1" })).rejects.toThrow(
      'no subcommand "nope"',
    );
    await expect(runCommand(plugin, t, "fruit admin", { user: "1" })).rejects.toThrow(
      'no subcommand "admin"',
    );
    await expect(runCommand(plugin, t, "nope", { user: "1" })).rejects.toThrow('no command "nope"');
  });
});

describe("runAutocomplete", () => {
  test("returns the choices", async () => {
    const t = createTestContext();
    expect(
      await runAutocomplete(plugin, t, "fruit pick", { user: "1", focused: "name", value: "ap" }),
    ).toEqual([
      { name: "apple (pick)", value: "apple" },
      { name: "apricot (pick)", value: "apricot" },
    ]);
    expect(
      await runAutocomplete(plugin, t, "fruit pick", { user: "1", focused: "name" }),
    ).toHaveLength(3);
  });

  test("validates like the runtime", async () => {
    const t = createTestContext();
    await expect(
      runAutocomplete(plugin, t, "fruit pick", { user: "1", focused: "count" }),
    ).rejects.toThrow("value must be a integer");
    await expect(
      runAutocomplete(plugin, t, "fruit pick", { user: "1", focused: "many" }),
    ).rejects.toThrow("26 choices");
    await expect(
      runAutocomplete(plugin, t, "fruit pick", { user: "1", focused: "plain" }),
    ).rejects.toThrow("has no autocomplete");
  });
});

describe("channel.message", () => {
  test("finds a message sent through the fakes", async () => {
    const t = createTestContext({ channels: { general: { name: "general" } } });
    const channel = t.ctx.server.channel("general");
    const sent = await channel.send("hello");
    expect((await channel.message(sent.id))?.content).toBe("hello");
    expect(await channel.message("123")).toBeUndefined();
    const denied = createTestContext({
      channels: { general: { name: "general" } },
      grants: ["discord.channels.read"],
    });
    await expect(denied.ctx.server.channel("general").message("1")).rejects.toThrow(
      "discord.messages.read",
    );
  });
});
