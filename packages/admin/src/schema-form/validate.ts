/**
 * Client-side validation for immediate feedback. The runtime stays the authority (schema +
 * `x-codi-ref` checks, 422 diagnostics); this covers the common keywords only.
 */
import type { Json, ServerResources } from "@codi/client";
import {
  enumOptions,
  fieldKind,
  itemSchemaOf,
  primaryType,
  resolve,
  type JsonSchema,
} from "./schema.ts";
import { deepEqual, isObject, toPointer, type Segment } from "./values.ts";
import { i18n } from "../i18n/index.ts";

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
    if (typeof value !== "string") return fail(i18n.t("form.errors.text"));
    if (s.minLength !== undefined && value.length < s.minLength)
      fail(
        s.minLength === 1
          ? i18n.t("form.errors.required")
          : i18n.t("form.errors.minLength", { count: s.minLength }),
      );
    if (s.maxLength !== undefined && value.length > s.maxLength)
      fail(i18n.t("form.errors.maxLength", { count: s.maxLength, length: value.length }));
    if (s.pattern && value !== "") {
      try {
        if (!new RegExp(s.pattern, "u").test(value))
          fail(i18n.t("form.errors.pattern", { pattern: s.pattern }));
      } catch {
        // invalid pattern in the schema: let the runtime decide
      }
    }
    if (s["x-codi-widget"] === "duration" && value !== "" && !isDuration(value))
      fail(i18n.t("form.errors.duration"));
    if (s["x-codi-widget"] === "color" && value !== "" && !/^#[0-9a-fA-F]{6}$/.test(value))
      fail(i18n.t("form.errors.color"));
    const ref = s["x-codi-ref"];
    if (ref && resources && value !== "") {
      const list =
        ref === "role"
          ? resources.roles
          : ref === "category"
            ? resources.categories
            : resources.channels;
      if (!list.some((r) => r.key === value))
        fail(i18n.t("form.errors.unknownRef", { kind: i18n.t(`form.ref.${ref}`), value }));
    }
    return;
  }
  if (type === "number" || type === "integer") {
    if (typeof value !== "number" || Number.isNaN(value)) return fail(i18n.t("form.errors.number"));
    if (type === "integer" && !Number.isInteger(value)) fail(i18n.t("form.errors.integer"));
    if (s.minimum !== undefined && value < s.minimum)
      fail(i18n.t("form.errors.minimum", { value: s.minimum }));
    if (s.maximum !== undefined && value > s.maximum)
      fail(i18n.t("form.errors.maximum", { value: s.maximum }));
    if (s.exclusiveMinimum !== undefined && value <= s.exclusiveMinimum)
      fail(i18n.t("form.errors.greaterThan", { value: s.exclusiveMinimum }));
    if (s.exclusiveMaximum !== undefined && value >= s.exclusiveMaximum)
      fail(i18n.t("form.errors.lessThan", { value: s.exclusiveMaximum }));
    return;
  }
  if (type === "boolean") {
    if (typeof value !== "boolean") fail(i18n.t("form.errors.boolean"));
    return;
  }
  if (type === "array") {
    if (!Array.isArray(value)) return fail(i18n.t("form.errors.list"));
    if (s.minItems !== undefined && value.length < s.minItems)
      fail(i18n.t("form.errors.minItems", { count: s.minItems }));
    if (s.maxItems !== undefined && value.length > s.maxItems)
      fail(i18n.t("form.errors.maxItems", { count: s.maxItems }));
    if (s.uniqueItems) {
      const seen = value.map((v) => JSON.stringify(v));
      if (new Set(seen).size !== seen.length) fail(i18n.t("form.errors.unique"));
    }
    value.forEach((item, i) => check(itemSchemaOf(s), root, item, [...path, i], out, resources));
    return;
  }
  if (type === "object") {
    if (!isObject(value)) return fail(i18n.t("form.errors.object"));
    for (const key of s.required ?? []) {
      const v = value[key];
      if (v === undefined || v === null)
        out.push({ pointer: toPointer([...path, key]), message: "Required" });
    }
    for (const [key, prop] of Object.entries(s.properties ?? {}))
      check(prop, root, value[key], [...path, key], out, resources);
  }
}
