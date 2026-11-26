import { expect, test } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";

test("reads exact bytes from an asset, HTTP response and sent file", async () => {
  const data = new Uint8Array([0, 255, 42]);
  const t = createTestContext({
    assets: { "a.bin": data },
    channels: { chat: { name: "chat" } },
    fetch: () => ({ body: data }),
  });
  const body = await (await t.ctx.http.fetch("https://test.invalid")).bytes();
  expect(body).toEqual(data);
  const asset = await t.ctx.assets.read("a.bin");
  const m = await t.ctx.server
    .channel("chat")
    .send({ files: [{ name: "a.bin", data: asset, description: "avatar" }] });
  asset.fill(0);
  expect(m.attachments[0]?.description).toBe("avatar");
  expect(await m.attachments[0]?.bytes()).toEqual(data);
  await expect(t.ctx.assets.read("../a.bin")).rejects.toThrow();
  await expect(
    m.reply({ files: Array.from({ length: 11 }, () => ({ name: "a", data })) }),
  ).rejects.toThrow();
});
test("enforces grants and records native-operation fixture results", async () => {
  const denied = createTestContext({ grants: [] });
  await expect(
    denied.attachment({ name: "a", data: new Uint8Array() }).bytes(),
  ).rejects.toMatchObject({ capability: "discord.messages.read" });
  await expect(denied.ctx.render.image({ width: 1, height: 1 })).rejects.toMatchObject({
    capability: "render",
  });
  await expect(
    denied.ctx.media.hash("https://cdn.discordapp.com/a", { algo: "phash" }),
  ).rejects.toMatchObject({ capability: "media.analyze" });
  const t = createTestContext({
    renderImage: async () => ({
      data: new Uint8Array([137, 80, 78, 71]),
      contentType: "image/png",
    }),
    hashImage: async () => "ffff000000000000",
  });
  const image = await t.ctx.render.image({ width: 4, height: 4 });
  expect(image.contentType).toBe("image/png");
  const digest = await t.ctx.media.hash("https://cdn.discordapp.com/a", { algo: "phash" });
  expect(t.ctx.media.distance(digest, "0000000000000000")).toBe(16);
});
