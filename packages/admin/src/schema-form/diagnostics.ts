/**
 * Maps runtime diagnostics (422 on `PUT …/config`) to form fields. Diagnostic paths are JSON
 * pointers into bot.yaml, e.g. `/spec/plugins/0/overrides/pixel-haven/config/steps/1/title`
 * or `/spec/plugins/0/config/channel`; the part after the first `/config` is the field.
 */
import type { Diagnostic, Json } from "@cicord/client";
import { fieldKind, itemSchemaOf, resolve, type JsonSchema } from "./schema.ts";
import type { FieldError } from "./validate.ts";
import { isObject, toPointer, type Segment } from "./values.ts";

export interface MappedDiagnostics {
  /** Messages per field pointer (relative to the config root, "" = the whole config). */
  byField: Record<string, string[]>;
  /** Diagnostics without a usable path (shown above the form). */
  general: string[];
}

/** "/spec/plugins/0/config/steps/1" → "/steps/1"; a path without "/config" is kept as is. */
export function configPointer(path: string): string {
  const match = /\/config(?=\/|$)/.exec(path);
  return match ? path.slice(match.index + match[0].length) : path;
}

/** Removes the "[server x] " prefix the runtime adds to messages. */
export function cleanMessage(message: string): string {
  return message.replace(/^\[server [^\]]+\]\s*/, "");
}

export function mapDiagnostics(
  diagnostics: readonly Diagnostic[],
  knownPointers?: ReadonlySet<string>,
): MappedDiagnostics {
  const byField: Record<string, string[]> = {};
  const general: string[] = [];
  for (const d of diagnostics) {
    if (d.severity === "warning") continue;
    const message = cleanMessage(d.message);
    if (d.path === undefined || d.path === "") {
      general.push(message);
      continue;
    }
    let pointer = configPointer(d.path);
    // Climb to the closest field the form renders (e.g. a missing property reported on its parent).
    if (knownPointers) {
      while (pointer !== "" && !knownPointers.has(pointer))
        pointer = pointer.slice(0, pointer.lastIndexOf("/"));
    }
    if (pointer === "") general.push(message);
    else (byField[pointer] ??= []).push(message);
  }
  return { byField, general };
}

export function errorsByField(errors: readonly FieldError[]): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const e of errors) (out[e.pointer] ??= []).push(e.message);
  return out;
}

export function mergeErrors(...sources: Record<string, string[]>[]): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const source of sources)
    for (const [k, v] of Object.entries(source)) out[k] = [...new Set([...(out[k] ?? []), ...v])];
  return out;
}

/** Pointers of every field the form renders for `value` (used to place diagnostics). */
export function collectPointers(schema: JsonSchema, value: Json | undefined): Set<string> {
  const out = new Set<string>();
  const walk = (raw: JsonSchema, v: Json | undefined, path: Segment[]) => {
    const s = resolve(raw, schema);
    if (path.length > 0) out.add(toPointer(path));
    const kind = fieldKind(s);
    if (kind === "object") {
      for (const [key, prop] of Object.entries(s.properties ?? {}))
        walk(prop, isObject(v) ? v[key] : undefined, [...path, key]);
    } else if (kind === "array" && Array.isArray(v)) {
      v.forEach((item, i) => walk(itemSchemaOf(s), item, [...path, i]));
    }
  };
  walk(schema, value, []);
  return out;
}
