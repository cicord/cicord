import { expect, test } from "vite-plus/test";
import { command, definePlugin } from "@cicord/sdk";
import { createTestContext, runCommand, runContextMenu } from "../src/testing.ts";

test("translations use the current interaction and never interpolate member input twice", async () => {
  const catalogs = { en: { greet: "Hello {name}" }, fr: { greet: "Bonjour {name}" } };
  const t = createTestContext({ locales: catalogs, server: { preferredLocale: "en" } });
  const plugin = definePlugin({
    commands: {
      hello: command({
        description: "Say hello",
        async run(ctx, it) {
          await it.reply(ctx.i18n.t("greet", { name: "{staff}", staff: "secret" }));
        },
      }),
    },
  });
  const r = await runCommand(plugin, t, "hello", { user: "1", locale: "fr-CA" });
  expect(r.replies).toEqual(["Bonjour {staff}"]);
  expect(t.ctx.i18n.t("greet", { name: "Ada" })).toBe("Hello Ada");
});

test("a French user-menu interaction uses its locale and canonical handler name", async () => {
  const harness = createTestContext({
    members: [{ id: "viewer" }, { id: "target" }],
    server: { preferredLocale: "en" },
    locales: { en: { opened: "Opened" }, fr: { opened: "Ouvert" } },
  });
  const plugin = definePlugin({
    contextMenus: {
      "Open Profile": {
        type: "user",
        async run(ctx, it) {
          await it.reply(ctx.i18n.t("opened"));
        },
      },
    },
  });
  const result = await runContextMenu(plugin, harness, "Open Profile", {
    user: "viewer",
    targetId: "target",
    locale: "fr-CA",
  });
  expect(result.replies).toEqual(["Ouvert"]);
});
