/** Picker options for `x-cicord-ref` fields, built from `GET /servers/{key}/resources`. */
import type { ServerResources } from "@cicord/client";
import type { PickerOption } from "./Picker.tsx";
import type { RefKind } from "./schema.ts";
import { i18n } from "../i18n/index.ts";

export function Swatch({ color }: { color: string }) {
  const c = /^#[0-9a-f]{6}$/i.test(color) && color.toLowerCase() !== "#000000" ? color : undefined;
  return (
    <span
      className="sf-swatch"
      data-empty={c ? undefined : true}
      style={c ? { background: c } : undefined}
      aria-hidden="true"
    />
  );
}

export function ChannelGlyph({ type }: { type: string | undefined }) {
  const voice = type === "voice" || type === "stage";
  return (
    <span className="sf-glyph" aria-hidden="true">
      {voice ? (
        <svg viewBox="0 0 16 16" width="13" height="13">
          <path
            d="M3 6h2.5L9 3v10L5.5 10H3zM11.5 5.5a3.5 3.5 0 010 5"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinejoin="round"
          />
        </svg>
      ) : type === "announcement" ? (
        <svg viewBox="0 0 16 16" width="13" height="13">
          <path
            d="M3 6.5h2l6-3v9l-6-3H3zM5 9.5l1 3"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinejoin="round"
          />
        </svg>
      ) : type === "forum" ? (
        <svg viewBox="0 0 16 16" width="13" height="13">
          <path
            d="M2.5 3.5h8v5.5h-4.5l-2.5 2v-2h-1zM10.5 6h3v5.5h-1v2l-2.5-2h-3.5v-2.5"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinejoin="round"
          />
        </svg>
      ) : type === "media" ? (
        <svg viewBox="0 0 16 16" width="13" height="13">
          <path
            d="M2.5 3.5h11v9h-11zM2.5 10.5l3.5-3.5 3 3 1.5-1.5 3 3M10.5 6.5h.01"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        </svg>
      ) : (
        "#"
      )}
    </span>
  );
}

export function refOptions(kind: RefKind, resources: ServerResources | undefined): PickerOption[] {
  if (!resources) return [];
  if (kind === "role") {
    return resources.roles
      .filter((r) => r.key)
      .map((r) => ({ value: r.key, label: r.name, hint: r.key, icon: <Swatch color={r.color} /> }));
  }
  if (kind === "category") {
    return resources.categories
      .filter((c) => c.key)
      .map((c) => ({ value: c.key, label: c.name, hint: c.key }));
  }
  const categoryName = new Map(resources.categories.map((c) => [c.key, c.name]));
  const order = new Map(resources.categories.map((c, i) => [c.key, i]));
  const rank = (category: string | undefined) =>
    category ? (order.get(category) ?? resources.categories.length) : -1;
  return resources.channels
    .filter((c) => c.key && c.type !== "category")
    .toSorted((a, b) => rank(a.category) - rank(b.category))
    .map((c) => ({
      value: c.key,
      label: c.name,
      hint: c.key,
      group: c.category ? (categoryName.get(c.category) ?? c.category) : i18n.t("form.noCategory"),
      icon: <ChannelGlyph type={c.type} />,
    }));
}

/** Display name of a reference value ("Minecraft", "#general"), when it resolves. */
export function refLabel(
  kind: RefKind,
  value: string,
  resources: ServerResources | undefined,
): string | undefined {
  if (!resources) return undefined;
  if (kind === "role") return resources.roles.find((r) => r.key === value)?.name;
  if (kind === "category") return resources.categories.find((c) => c.key === value)?.name;
  const ch = resources.channels.find((c) => c.key === value);
  return ch ? `#${ch.name}` : undefined;
}
