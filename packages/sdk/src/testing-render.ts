import type { ImageSpec } from "@cicord/sdk";

// The runtime's render envelope: internal/media/paint/spec.go:81-149 (Inputs),
// internal/media/paint/draw.go:42-73 (source pixel budget) and internal/plugin/media.go:102-131.
const MAX_SIDE = 2048;
const MAX_CANVAS_PIXELS = 1 << 19;
const MAX_SOURCE_PIXELS = 1 << 19;
const CDN_HOSTS = new Set(["cdn.discordapp.com", "media.discordapp.net"]);

type Loose = Record<string, unknown>;
const isInt = (v: unknown): v is number => Number.isInteger(v);
const int = (v: unknown) => {
  // Go decodes these fields into int: a fraction or a non-number fails the decode.
  if (v === undefined || v === null) return 0;
  if (!isInt(v)) throw new TypeError("invalid arguments: expected an integer");
  return v;
};

function sourceKey(src: Loose): string {
  const asset = typeof src["asset"] === "string" ? src["asset"] : "";
  const url = typeof src["url"] === "string" ? src["url"] : "";
  if (asset && !url) return "asset:" + asset;
  if (url && !asset) return "url:" + url;
  throw new Error("image source must specify an asset or a Discord CDN URL");
}

function fontKey(font: unknown): string {
  if (font && typeof font === "object" && typeof (font as Loose)["asset"] === "string") {
    const asset = (font as Loose)["asset"] as string;
    if (asset) return "asset:" + asset;
  }
  const builtin = typeof font === "string" ? font : "";
  if (builtin === "" || builtin === "Inter") return "font:Inter";
  if (builtin === "Noto" || builtin === "emoji") return "font:" + builtin;
  throw new Error("unknown built-in font");
}

/**
 * paint.Spec.Inputs: size, layer and source checks done before any input is read. Returns
 * the distinct image keys (`asset:…`, `url:…`), in order, and the font keys.
 */
function inputs(spec: ImageSpec): { images: string[]; fonts: string[] } {
  const s = spec as unknown as Loose;
  const width = int(s["width"]),
    height = int(s["height"]);
  const layers = (Array.isArray(s["layers"]) ? s["layers"] : []) as Loose[];
  if (width < 1 || height < 1 || width > MAX_SIDE || height > MAX_SIDE || layers.length > 64)
    throw new Error("image size must be 1..2048, with at most 64 layers");
  if (width * height > MAX_CANVAS_PIXELS)
    throw new Error(
      `image area ${width}×${height} exceeds ${MAX_CANVAS_PIXELS} pixels (for example 1024×512 or 2048×256)`,
    );
  const images: string[] = [],
    fonts: string[] = [];
  const add = (list: string[], key: string) => {
    if (!list.includes(key)) list.push(key);
  };
  const bg = s["background"];
  if (bg && typeof bg === "object") {
    const b = bg as Loose;
    if (b["asset"] || b["url"]) add(images, sourceKey(b));
    if (b["gradient"] !== undefined && b["gradient"] !== null) {
      if (!Array.isArray(b["gradient"]) || b["gradient"].length !== 2)
        throw new Error("gradient needs exactly two colors");
    }
  }
  for (const l of layers) {
    const x = int(l["x"]),
      y = int(l["y"]);
    if (x < -2048 || x > 2048 || y < -2048 || y > 2048)
      throw new Error("layer coordinates outside bounds");
    if (l["type"] === "text") {
      const size = int(l["size"]),
        maxWidth = int(l["maxWidth"]);
      const text = typeof l["text"] === "string" ? l["text"] : "";
      if (
        size < 1 ||
        size > 256 ||
        new TextEncoder().encode(text).length > 4096 ||
        maxWidth < 0 ||
        maxWidth > 2048
      )
        throw new Error("invalid text size, width or length");
      add(fonts, fontKey(l["font"]));
      continue;
    }
    const w = int(l["width"]),
      h = int(l["height"]);
    if (w < 1 || h < 1 || w > 2048 || h > 2048) throw new Error("invalid layer dimensions");
    switch (l["type"]) {
      case "image":
        add(images, sourceKey((l["source"] ?? {}) as Loose));
        break;
      case "progress": {
        const value = typeof l["value"] === "number" ? l["value"] : 0;
        if (value < 0 || value > 1) throw new Error("progress value must be 0..1");
        break;
      }
      case "chart": {
        const values = Array.isArray(l["values"]) ? l["values"] : [];
        if (
          values.length === 0 ||
          values.length > 256 ||
          (l["kind"] !== "bar" && l["kind"] !== "line")
        )
          throw new Error("chart requires bar/line and 1..256 values");
        break;
      }
      case "rect":
        break;
      default:
        throw new Error(
          `unknown layer type ${JSON.stringify(typeof l["type"] === "string" ? l["type"] : "")}`,
        );
    }
  }
  return { images, fonts };
}

const be16 = (b: Uint8Array, i: number) => (b[i] << 8) | b[i + 1];
const le16 = (b: Uint8Array, i: number) => b[i] | (b[i + 1] << 8);
const be32 = (b: Uint8Array, i: number) =>
  ((b[i] << 24) | (b[i + 1] << 16) | (b[i + 2] << 8) | b[i + 3]) >>> 0;
const ascii = (b: Uint8Array, i: number, n: number) => String.fromCharCode(...b.subarray(i, i + n));

/**
 * The size image.DecodeConfig reads for the formats the renderer registers (PNG, JPEG
 * baseline/extended/progressive, GIF, WebP), or undefined for anything else.
 */
function imageSize(b: Uint8Array): { width: number; height: number } | undefined {
  if (
    b.length >= 24 &&
    be32(b, 0) === 0x89504e47 &&
    be32(b, 4) === 0x0d0a1a0a &&
    ascii(b, 12, 4) === "IHDR"
  )
    return { width: be32(b, 16), height: be32(b, 20) };
  if (b.length >= 10 && (ascii(b, 0, 6) === "GIF87a" || ascii(b, 0, 6) === "GIF89a"))
    return { width: le16(b, 6), height: le16(b, 8) };
  if (b.length >= 4 && b[0] === 0xff && b[1] === 0xd8) {
    let i = 2;
    while (i + 4 <= b.length) {
      if (b[i] !== 0xff) return undefined;
      const marker = b[i + 1];
      if (marker === 0xff) {
        i++;
        continue;
      }
      if (marker === 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker === 0x01) {
        i += 2;
        continue;
      }
      const length = be16(b, i + 2);
      if (marker === 0xc0 || marker === 0xc1 || marker === 0xc2) {
        if (i + 9 > b.length) return undefined;
        return { width: be16(b, i + 7), height: be16(b, i + 5) };
      }
      // Other SOF markers (lossless, arithmetic, hierarchical) are unsupported in Go.
      if (marker >= 0xc3 && marker <= 0xcf && marker !== 0xc4 && marker !== 0xc8 && marker !== 0xcc)
        return undefined;
      if (marker === 0xd9 || marker === 0xda) return undefined;
      i += 2 + length;
    }
    return undefined;
  }
  if (b.length >= 30 && ascii(b, 0, 4) === "RIFF" && ascii(b, 8, 4) === "WEBP") {
    const chunk = ascii(b, 12, 4);
    if (chunk === "VP8 ") return { width: le16(b, 26) & 0x3fff, height: le16(b, 28) & 0x3fff };
    if (chunk === "VP8L" && b[20] === 0x2f) {
      const bits = b[21] | (b[22] << 8) | (b[23] << 16) | (b[24] << 24);
      return { width: (bits & 0x3fff) + 1, height: ((bits >>> 14) & 0x3fff) + 1 };
    }
    if (chunk === "VP8X")
      return {
        width: 1 + (b[24] | (b[25] << 8) | (b[26] << 16)),
        height: 1 + (b[27] | (b[28] << 8) | (b[29] << 16)),
      };
  }
  return undefined;
}

/**
 * Checks an image spec like the host before it reaches the injected renderer: the 64 KiB
 * spec, the size, layer and font checks, CDN-only URLs, and the 524,288 decoded pixels
 * shared by every distinct image source (each counted once). `source` returns the bytes the
 * harness knows for a key: plugin assets (a missing one throws) and its own attachment URLs.
 * Other CDN URLs are not counted, since the harness cannot download them.
 */
export function checkRenderSpec(
  spec: ImageSpec,
  source: (key: string) => Uint8Array | undefined,
): void {
  if (new TextEncoder().encode(JSON.stringify(spec) ?? "").length > 64 << 10)
    throw new TypeError("image specification exceeds 64 KiB");
  const { images, fonts } = inputs(spec);
  for (const key of fonts) if (key.startsWith("asset:")) source(key);
  let pixels = 0;
  for (const key of images) {
    if (key.startsWith("url:")) {
      let url: URL;
      try {
        url = new URL(key.slice(4));
      } catch {
        throw new Error("invalid render source URL");
      }
      if (
        url.protocol !== "https:" ||
        url.username ||
        url.password ||
        !CDN_HOSTS.has(url.hostname.toLowerCase().replace(/\.$/, ""))
      )
        throw new Error("render sources must be plugin assets or Discord CDN URLs");
    }
    const bytes = source(key);
    if (!bytes) continue;
    const size = imageSize(bytes);
    if (!size) throw new Error("image: unknown format");
    const px = size.width * size.height;
    if (size.width <= 0 || size.height <= 0 || pixels + px > MAX_SOURCE_PIXELS)
      throw new Error(
        `image sources exceed ${MAX_SOURCE_PIXELS} decoded pixels in total (${size.width}×${size.height})`,
      );
    pixels += px;
  }
}
