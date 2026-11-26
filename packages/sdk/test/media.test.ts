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

test("versioned file globs match literal, nested and Unicode names", async () => {
  const t = createTestContext({
    files: {
      "kb/intro.md": "one",
      "kb/équipe/🎉.md": "two",
      "kb/logs.txt": "three",
      "other/intro.md": "four",
    },
  });
  expect(await t.ctx.files.list("kb/**/*.md")).toEqual(["kb/intro.md", "kb/équipe/🎉.md"]);
  expect(await t.ctx.files.list("kb/équipe/?.md")).toEqual(["kb/équipe/🎉.md"]);
});

// internal/media/paint/spec.go:81-149, draw.go:42-73 and internal/plugin/media.go:102-131.
test("render checks sizes, layers and decoded source pixels like the runtime", async () => {
  const png = (w: number, h: number) => {
    const b = new Uint8Array(33);
    b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 73, 72, 68, 82]);
    new DataView(b.buffer).setUint32(16, w);
    new DataView(b.buffer).setUint32(20, h);
    return b;
  };
  const gif = (w: number, h: number) =>
    new Uint8Array([71, 73, 70, 56, 57, 97, w & 255, w >> 8, h & 255, h >> 8, 0, 0, 0]);
  const rendered: unknown[] = [];
  const t = createTestContext({
    assets: {
      "a.png": png(1024, 512),
      "b.png": png(1, 1),
      "c.gif": gif(1, 1),
      "x.bin": new Uint8Array([1, 2]),
    },
    renderImage: async (spec) => {
      rendered.push(spec);
      return { data: new Uint8Array(), contentType: "image/png" };
    },
  });
  const { render } = t.ctx;
  const image = (asset: string, x = 0) =>
    ({ type: "image", x, y: 0, width: 10, height: 10, source: { asset } }) as const;
  // Sides, area and layers.
  await expect(render.image({ width: 2049, height: 1 })).rejects.toThrow("image size must be");
  await expect(render.image({ width: 1024, height: 513 })).rejects.toThrow("exceeds 524288 pixels");
  await render.image({ width: 2048, height: 256 });
  await expect(
    render.image({ width: 10, height: 10, layers: [{ ...image("c.gif"), width: 2049 }] }),
  ).rejects.toThrow("invalid layer dimensions");
  await expect(
    render.image({
      width: 10,
      height: 10,
      layers: [{ type: "text", x: 0, y: 0, text: "a", size: 300, color: "#000" }],
    }),
  ).rejects.toThrow("invalid text size");
  await expect(
    render.image({ width: 10, height: 10, layers: [{ ...image("c.gif"), x: 3000 }] }),
  ).rejects.toThrow("layer coordinates outside bounds");
  // One 1024×512 source fills the 524,288-pixel budget only once, however often it is drawn.
  await render.image({
    width: 100,
    height: 100,
    background: { asset: "a.png" },
    layers: [image("a.png"), image("a.png", 5)],
  });
  await expect(
    render.image({ width: 100, height: 100, layers: [image("a.png"), image("b.png")] }),
  ).rejects.toThrow("image sources exceed 524288 decoded pixels in total");
  await expect(
    render.image({ width: 10, height: 10, layers: [image("missing.png")] }),
  ).rejects.toThrow("unknown plugin asset");
  await expect(render.image({ width: 10, height: 10, layers: [image("x.bin")] })).rejects.toThrow(
    "image: unknown format",
  );
  // URL sources are Discord CDN only; a harness attachment's bytes are counted.
  const big = t.attachment({ name: "big.png", data: png(1024, 513) });
  await expect(
    render.image({
      width: 10,
      height: 10,
      layers: [{ ...image(""), source: { url: "https://example.com/a.png" } }],
    }),
  ).rejects.toThrow("Discord CDN");
  await expect(
    render.image({ width: 10, height: 10, layers: [{ ...image(""), source: { url: big.url } }] }),
  ).rejects.toThrow("decoded pixels");
  await render.image({
    width: 10,
    height: 10,
    layers: [{ ...image(""), source: { url: "https://cdn.discordapp.com/avatars/1/a.png" } }],
  });
  await expect(
    render.image({
      width: 10,
      height: 10,
      layers: [{ type: "text", x: 0, y: 0, text: "x".repeat(70_000), size: 10, color: "#000" }],
    }),
  ).rejects.toThrow("image specification exceeds 64 KiB");
  expect(rendered).toHaveLength(3);
});

test("render reads JPEG and WebP source sizes like image.DecodeConfig", async () => {
  // SOI, an APP0 segment, then SOF0 with height 600 and width 900.
  const jpeg = new Uint8Array([
    0xff,
    0xd8,
    0xff,
    0xe0,
    0,
    4,
    0,
    0,
    0xff,
    0xc0,
    0,
    11,
    8,
    600 >> 8,
    600 & 255,
    900 >> 8,
    900 & 255,
    1,
    1,
    0x11,
    0,
  ]);
  // RIFF/WEBP with a VP8X chunk: canvas 1000×600 (stored minus one, 24-bit little endian).
  const webp = new Uint8Array(30);
  webp.set([82, 73, 70, 70, 0, 0, 0, 0, 87, 69, 66, 80, 86, 80, 56, 88]);
  webp.set([999 & 255, 999 >> 8, 0, 599 & 255, 599 >> 8, 0], 24);
  const t = createTestContext({
    assets: { "a.jpg": jpeg, "b.webp": webp },
    renderImage: async () => ({ data: new Uint8Array(), contentType: "image/png" }),
  });
  const layer = (asset: string) =>
    ({ type: "image", x: 0, y: 0, width: 1, height: 1, source: { asset } }) as const;
  // 900×600 = 540,000 and 1000×600 = 600,000 decoded pixels: each alone is over the budget.
  for (const asset of ["a.jpg", "b.webp"])
    await expect(
      t.ctx.render.image({ width: 10, height: 10, layers: [layer(asset)] }),
    ).rejects.toThrow("decoded pixels");
});
