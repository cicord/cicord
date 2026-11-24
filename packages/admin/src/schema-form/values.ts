/** Immutable helpers on JSON values addressed by paths (arrays of keys / indices). */
import type { Json } from "@cicord/client";

export type Segment = string | number;
export type Path = readonly Segment[];
export type JsonObject = { [key: string]: Json };

/** RFC 6901 JSON pointer of a path ("" for the root). */
export function toPointer(path: Path): string {
  return path.map((s) => `/${String(s).replace(/~/g, "~0").replace(/\//g, "~1")}`).join("");
}

export function isObject(v: unknown): v is JsonObject {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function getIn(value: Json | undefined, path: Path): Json | undefined {
  let current: Json | undefined = value;
  for (const seg of path) {
    if (Array.isArray(current) && typeof seg === "number") current = current[seg];
    else if (isObject(current)) current = current[String(seg)];
    else return undefined;
  }
  return current;
}

/**
 * Returns a copy of `root` with `value` at `path`; `undefined` removes an object key (array
 * items are removed with `removeAt`). Missing containers are created.
 */
export function setIn(root: Json | undefined, path: Path, value: Json | undefined): Json {
  if (path.length === 0) return value ?? null;
  const [head, ...rest] = path as [Segment, ...Segment[]];
  if (typeof head === "number") {
    const arr = Array.isArray(root) ? [...root] : [];
    const next = setIn(arr[head], rest, value);
    if (value === undefined && rest.length === 0) arr.splice(head, 1);
    else arr[head] = next;
    return arr;
  }
  const obj: JsonObject = isObject(root) ? { ...root } : {};
  if (rest.length === 0 && value === undefined) {
    delete obj[head];
    return obj;
  }
  obj[head] = setIn(obj[head], rest, value);
  return obj;
}

export function removeAt(list: readonly Json[], index: number): Json[] {
  return list.filter((_, i) => i !== index);
}

export function move(list: readonly Json[], from: number, to: number): Json[] {
  if (to < 0 || to >= list.length || from === to) return [...list];
  const out = [...list];
  const [item] = out.splice(from, 1);
  out.splice(to, 0, item as Json);
  return out;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b))
    return a.length === b.length && a.every((v, i) => deepEqual(v, b[i]));
  if (isObject(a) && isObject(b)) {
    const ka = Object.keys(a).filter((k) => a[k] !== undefined);
    const kb = Object.keys(b).filter((k) => b[k] !== undefined);
    return ka.length === kb.length && ka.every((k) => deepEqual(a[k], b[k]));
  }
  return false;
}

/** Text of a JSON value: strings as is, other scalars stringified, objects as JSON. */
export function jsonText(value: Json | undefined): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}
