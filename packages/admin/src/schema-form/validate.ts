/**
 * Client-side validation for immediate feedback. The runtime stays the authority (schema +
 * `x-codi-ref` checks, 422 diagnostics); this covers the common keywords only.
 */
import type { Json, ServerResources } from "@codi/client";
import { enumOptions, fieldKind, primaryType, resolve, type JsonSchema } from "./schema.ts";
import { deepEqual, isObject, toPointer, type Segment } from "./values.ts";

export interface FieldError {
  pointer: string;
  message: string;
}

const DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;

export function isDuration(value: string): boolean {
  return value === "0" || DURATION.test(value);
}

export function validate(
  schema: JsonSchema,
  value: Json | undefined,
  resources?: ServerResources,
): FieldError[] {
  const out: FieldError[] = [];
  check(schema, schema, value, [], out, resources);
  return out;
}

function check(
  raw: JsonSchema,
  root: JsonSchema,
  value: Json | undefined,
  path: Segment[],
  out: FieldError[],
  resources: ServerResources | undefined,
) {
  const s = resolve(raw, root);
  const pointer = toPointer(path);
  const fail = (message: string) => out.push({ pointer, message });
  if (value === undefined) return;

  const kind = fieldKind(s);
  if (kind === "enum") {
    if (!enumOptions(s).some((o) => deepEqual(o.value, value)))
      fail(
        `Must be one of: ${enumOptions(s)
          .map((o) => o.label)
          .join(", ")}`,
      );
    return;
  }
  const type = primaryType(s);
  const nullable = Array.isArray(s.type) && s.type.includes("null");
  if (value === null) {
    if (!nullable && type) fail("A value is required");
    return;
  }

  if (type === "string" || kind === "ref") {
    if (typeof value !== "string") return fail("Must be text");
    if (s.minLength !== undefined && value.length < s.minLength)
      fail(s.minLength === 1 ? "Required" : `At least ${s.minLength} characters`);
    if (s.maxLength !== undefined && value.length > s.maxLength)
      fail(`At most ${s.maxLength} characters (${value.length})`);
    if (s.pattern && value !== "") {
      try {
        if (!new RegExp(s.pattern, "u").test(value)) fail(`Must match ${s.pattern}`);
      } catch {
        // invalid pattern in the schema: let the runtime decide
      }
    }
    if (s["x-codi-widget"] === "duration" && value !== "" && !isDuration(value))
      fail("Use a duration such as 30s, 10m or 1h30m");
    if (s["x-codi-widget"] === "color" && value !== "" && !/^#[0-9a-fA-F]{6}$/.test(value))
      fail("Use a hex color such as #5865f2");
    const ref = s["x-codi-ref"];
    if (ref && resources && value !== "") {
      const list =
        ref === "role"
          ? resources.roles
          : ref === "category"
            ? resources.categories
            : resources.channels;
      if (!list.some((r) => r.key === value)) fail(`Unknown ${ref} "${value}"`);
    }
    return;
  }
  if (type === "number" || type === "integer") {
    if (typeof value !== "number" || Number.isNaN(value)) return fail("Must be a number");
    if (type === "integer" && !Number.isInteger(value)) fail("Must be a whole number");
    if (s.minimum !== undefined && value < s.minimum) fail(`Minimum ${s.minimum}`);
    if (s.maximum !== undefined && value > s.maximum) fail(`Maximum ${s.maximum}`);
    if (s.exclusiveMinimum !== undefined && value <= s.exclusiveMinimum)
      fail(`Must be greater than ${s.exclusiveMinimum}`);
    if (s.exclusiveMaximum !== undefined && value >= s.exclusiveMaximum)
      fail(`Must be less than ${s.exclusiveMaximum}`);
    return;
  }
  if (type === "boolean") {
    if (typeof value !== "boolean") fail("Must be true or false");
    return;
  }
  if (type === "array") {
    if (!Array.isArray(value)) return fail("Must be a list");
    if (s.minItems !== undefined && value.length < s.minItems)
      fail(s.minItems === 1 ? "Add at least one item" : `Add at least ${s.minItems} items`);
    if (s.maxItems !== undefined && value.length > s.maxItems) fail(`At most ${s.maxItems} items`);
    if (s.uniqueItems) {
      const seen = value.map((v) => JSON.stringify(v));
      if (new Set(seen).size !== seen.length) fail("Items must be unique");
    }
    value.forEach((item, i) => check(s.items ?? {}, root, item, [...path, i], out, resources));
    return;
  }
  if (type === "object") {
    if (!isObject(value)) return fail("Must be an object");
    for (const key of s.required ?? []) {
      const v = value[key];
      if (v === undefined || v === null)
        out.push({ pointer: toPointer([...path, key]), message: "Required" });
    }
    for (const [key, prop] of Object.entries(s.properties ?? {}))
      check(prop, root, value[key], [...path, key], out, resources);
  }
}
