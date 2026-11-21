/** Field-level diff of two configs, for the "review changes" preview before saving. */
import type { Json } from "@codi/client";
import { deepEqual, isObject, toPointer, type Path, type Segment } from "./values.ts";

export type ChangeKind = "added" | "removed" | "changed";

export interface FieldChange {
  /** JSON pointer relative to the config root, e.g. "/steps/0/title". */
  pointer: string;
  path: Segment[];
  kind: ChangeKind;
  from: Json | undefined;
  to: Json | undefined;
}

/**
 * Leaf-level changes between `before` and `after`. Objects are compared key by key; arrays
 * of the same length element by element; when an array grows or shrinks, the extra items are
 * reported as added / removed whole.
 */
export function diffConfig(before: Json | undefined, after: Json | undefined): FieldChange[] {
  const out: FieldChange[] = [];
  walk(before, after, [], out);
  return out;
}

function walk(a: Json | undefined, b: Json | undefined, path: Segment[], out: FieldChange[]) {
  if (deepEqual(a, b)) return;
  const push = (kind: ChangeKind, from: Json | undefined, to: Json | undefined, p: Path = path) =>
    out.push({ pointer: toPointer(p), path: [...p], kind, from, to });

  if (a === undefined) return push("added", undefined, b);
  if (b === undefined) return push("removed", a, undefined);

  if (isObject(a) && isObject(b)) {
    const keys = [...new Set([...Object.keys(a), ...Object.keys(b)])];
    for (const key of keys) walk(a[key], b[key], [...path, key], out);
    return;
  }
  if (Array.isArray(a) && Array.isArray(b)) {
    const common = Math.min(a.length, b.length);
    // Arrays of primitives read better as a single change.
    const primitive = [...a, ...b].every((v) => !isObject(v) && !Array.isArray(v));
    if (primitive) return push("changed", a, b);
    for (let i = 0; i < common; i++) walk(a[i], b[i], [...path, i], out);
    for (let i = common; i < b.length; i++) push("added", undefined, b[i], [...path, i]);
    for (let i = common; i < a.length; i++) push("removed", a[i], undefined, [...path, i]);
    return;
  }
  push("changed", a, b);
}

/** Top-level keys touched by a diff (what the runtime checks against `editable`). */
export function changedTopLevel(changes: readonly FieldChange[]): string[] {
  return [...new Set(changes.map((c) => String(c.path[0] ?? "")))].filter(Boolean).sort();
}

/** Short human rendering of a value in a diff. */
export function formatValue(value: Json | undefined): string {
  if (value === undefined) return "—";
  if (typeof value === "string") return JSON.stringify(value);
  return JSON.stringify(value);
}
