/** Small color helpers: server colors can be anything, so contrast is computed, not assumed. */

export type Rgb = [number, number, number];

export function parseHex(value: string | undefined): Rgb | undefined {
  if (!value) return undefined;
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(value.trim());
  if (!m) return undefined;
  let hex = m[1] as string;
  if (hex.length === 3)
    hex = Array.from(hex)
      .map((c) => c + c)
      .join("");
  const n = Number.parseInt(hex, 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

export function toHex([r, g, b]: Rgb): string {
  return `#${[r, g, b]
    .map((c) =>
      Math.round(Math.max(0, Math.min(255, c)))
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}

/** WCAG relative luminance. */
export function luminance([r, g, b]: Rgb): number {
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

export function contrast(a: Rgb, b: Rgb): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

export function mix(a: Rgb, b: Rgb, amount: number): Rgb {
  return [
    a[0] + (b[0] - a[0]) * amount,
    a[1] + (b[1] - a[1]) * amount,
    a[2] + (b[2] - a[2]) * amount,
  ];
}

const WHITE: Rgb = [255, 255, 255];
const INK: Rgb = [16, 17, 24];

/** Text color (white or ink) with the best contrast on `background`. */
export function readableOn(background: Rgb): string {
  return contrast(background, WHITE) >= contrast(background, INK) ? "#ffffff" : toHex(INK);
}

/**
 * Variant of `color` usable as text/icon on `surface` (WCAG ≥ `target`): mixed toward white
 * on dark surfaces and toward ink on light ones, as little as needed.
 */
export function legibleOn(color: Rgb, surface: Rgb, target = 4.5): string {
  const toward = luminance(surface) < 0.4 ? WHITE : INK;
  for (let step = 0; step <= 20; step++) {
    const candidate = toHex(mix(color, toward, step / 20));
    if (contrast(parseHex(candidate) as Rgb, surface) >= target) return candidate;
  }
  return toHex(toward);
}
