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

// internal/plugin/locales.go:16-31 and internal/locales/catalog.go:104-161.
test("translations are capped at 64 KiB per text and per variable, like the runtime", () => {
  const t = createTestContext({ locales: { en: { many: "{v}{v}{v}{v}{v}", one: "<{v}>" } } });
  const { i18n } = t.ctx;
  const big = "é".repeat(16 << 10); // 32 KiB of UTF-8
  expect(i18n.t("one", { v: big })).toBe(`<${big}>`);
  // Five copies of 32 KiB cross 64 KiB while the text is built.
  expect(() => i18n.t("many", { v: big })).toThrow(
    new TypeError('i18n.t "many": translated text exceeds 65536 bytes'),
  );
  expect(() => i18n.t("one", { v: "x".repeat((64 << 10) + 1) })).toThrow(
    new TypeError("i18n.t: a variable exceeds 65536 bytes"),
  );
  expect(i18n.t("one", { v: "x".repeat((64 << 10) - 2) })).toHaveLength(64 << 10);
  expect(() => i18n.t("one", { v: "x".repeat((64 << 10) - 1) })).toThrow(TypeError);
  expect(() => i18n.t("k".repeat(257))).toThrow(new TypeError("invalid translation arguments"));
  const vars = Object.fromEntries(Array.from({ length: 51 }, (_, i) => ["v" + i, i]));
  expect(() => i18n.t("one", vars)).toThrow(new TypeError("invalid translation arguments"));
  // Values other than strings, numbers and booleans keep the placeholder.
  expect(i18n.t("one", { v: null as unknown as string })).toBe("<{v}>");
  expect(i18n.t("one", { v: false })).toBe("<false>");
});
