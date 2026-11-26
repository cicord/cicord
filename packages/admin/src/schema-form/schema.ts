/**
 * JSON Schema (2020-12) subset used by plugin `config.schema.json` files, plus the Cicord
 * extensions (https://cicord.dev/docs/plugins/writing/): `x-cicord-ref`, `x-cicord-widget`, `x-cicord-access`.
 */
import type { Json } from "@cicord/client";
import { jsonText } from "./values.ts";

export type RefKind = "channel" | "category" | "role";
type Widget = "textarea" | "markdown" | "color" | "duration";
type Access = "admin" | "moderator";

export interface JsonSchema {
  $ref?: string;
  $defs?: Record<string, JsonSchema>;
  definitions?: Record<string, JsonSchema>;
  type?: string | string[];
  title?: string;
  description?: string;
  default?: Json;
  const?: Json;
  enum?: Json[];
  properties?: Record<string, JsonSchema>;
  required?: string[];
  additionalProperties?: boolean | JsonSchema;
  items?: JsonSchema;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  format?: string;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  multipleOf?: number;
  oneOf?: JsonSchema[];
  anyOf?: JsonSchema[];
  readOnly?: boolean;
  "x-cicord-ref"?: RefKind;
  "x-cicord-widget"?: Widget;
  "x-cicord-access"?: Access;
  [key: string]: unknown;
}

/** Every installation has a host-owned activation switch, including legacy packages. */
export function installationSchema(schema: JsonSchema): JsonSchema {
  return {
    ...schema,
    "x-cicord-installation": true,
    properties: {
      ...schema.properties,
      enabled: {
        type: "boolean",
        title: "Enabled",
        default: true,
        ...schema.properties?.enabled,
        "x-cicord-instance-switch": true,
        "x-cicord-access": "admin",
      },
    },
  };
}

export type FieldKind =
  | "string"
  | "number"
  | "integer"
  | "boolean"
  | "enum"
  | "ref"
  | "array"
  | "object"
  | "json";

/** Resolves a local `$ref` ("#/$defs/x", "#/definitions/x"); unknown refs resolve to {}. */
export function resolve(schema: JsonSchema | undefined, root: JsonSchema): JsonSchema {
  let current = schema ?? {};
  for (let depth = 0; current.$ref && depth < 16; depth++) {
    const ref = current.$ref;
    if (!ref.startsWith("#/")) return { ...current, $ref: undefined };
    let target: unknown = root;
    for (const part of ref.slice(2).split("/")) {
      const key = part.replace(/~1/g, "/").replace(/~0/g, "~");
      target =
        target && typeof target === "object" ? (target as Record<string, unknown>)[key] : undefined;
    }
    const { $ref: _ignored, ...rest } = current;
    current = { ...(target as JsonSchema | undefined), ...rest };
  }
  return current;
}

/** Primary (non-null) type of a schema. */
export function primaryType(schema: JsonSchema): string | undefined {
  if (Array.isArray(schema.type)) return schema.type.find((t) => t !== "null");
  if (schema.type) return schema.type;
  if (schema.properties) return "object";
  if (schema.items) return "array";
  if (schema.enum) return undefined;
  return undefined;
}

/**
 * Item schema of an array. `x-cicord-ref` on the array itself (a list of resource keys, as in
 * `{"type": "array", "items": {"type": "string"}, "x-cicord-ref": "role"}`) applies to its items.
 */
export function itemSchemaOf(schema: JsonSchema): JsonSchema {
  const items = schema.items ?? {};
  const ref = schema["x-cicord-ref"];
  return ref && !items["x-cicord-ref"] && !items.$ref ? { ...items, "x-cicord-ref": ref } : items;
}

export function fieldKind(schema: JsonSchema): FieldKind {
  if (schema["x-cicord-ref"] && primaryType(schema) !== "array") return "ref";
  if (Array.isArray(schema.enum) && schema.enum.length > 0) return "enum";
  if (schema.const !== undefined) return "enum";
  if (schema.oneOf || schema.anyOf) {
    const options = (schema.oneOf ?? schema.anyOf ?? []).filter((s) => s.const !== undefined);
    if (options.length > 0 && options.length === (schema.oneOf ?? schema.anyOf)!.length)
      return "enum";
    return "json";
  }
  switch (primaryType(schema)) {
    case "string":
      return "string";
    case "number":
      return "number";
    case "integer":
      return "integer";
    case "boolean":
      return "boolean";
    case "array":
      return "array";
    case "object":
      return "object";
    default:
      return "json";
  }
}

export interface EnumOption {
  value: Json;
  label: string;
  description?: string;
}

export function enumOptions(schema: JsonSchema): EnumOption[] {
  if (Array.isArray(schema.enum)) return schema.enum.map((v) => ({ value: v, label: jsonText(v) }));
  if (schema.const !== undefined) return [{ value: schema.const, label: jsonText(schema.const) }];
  return (schema.oneOf ?? schema.anyOf ?? [])
    .filter((s) => s.const !== undefined)
    .map((s) => ({
      value: s.const as Json,
      label: s.title ?? jsonText(s.const),
      ...(s.description ? { description: s.description } : {}),
    }));
}

/** "dmOnboarding" → "Dm onboarding", "completed_role" → "Completed role". */
function humanize(key: string): string {
  const words = key
    .replace(/[_-]+/g, " ")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
    .trim()
    .toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function fieldLabel(schema: JsonSchema, key: string | number): string {
  if (schema.title) return schema.title;
  return typeof key === "number" ? `Item ${key + 1}` : humanize(key);
}

const clone = <T>(v: T): T => (v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T));

/**
 * Initial value for a new field / array item: the schema default, else an empty value of the
 * right type with required properties (and properties that have defaults) filled in.
 */
export function initialValue(schema: JsonSchema, root: JsonSchema = schema): Json | undefined {
  const s = resolve(schema, root);
  if (s.default !== undefined) return clone(s.default);
  if (s.const !== undefined) return clone(s.const);
  switch (fieldKind(s)) {
    case "enum":
      return enumOptions(s)[0]?.value ?? null;
    case "string":
    case "ref":
      return "";
    case "number":
    case "integer":
      return s.minimum ?? 0;
    case "boolean":
      return false;
    case "array": {
      const out: Json[] = [];
      for (let i = 0; i < (s.minItems ?? 0); i++) {
        const item = initialValue(itemSchemaOf(s), root);
        if (item !== undefined) out.push(item);
      }
      return out;
    }
    case "object": {
      const out: { [key: string]: Json } = {};
      const required = new Set(s.required ?? []);
      for (const [key, prop] of Object.entries(s.properties ?? {})) {
        const p = resolve(prop, root);
        if (required.has(key) || p.default !== undefined) {
          const v = initialValue(p, root);
          if (v !== undefined) out[key] = v;
        }
      }
      return out;
    }
    default:
      return undefined;
  }
}

/** Top-level fields a role may edit, from `x-cicord-access` (mirrors the runtime's rule). */
export function editableFor(schema: JsonSchema, role: "admin" | "moderator" | "member"): string[] {
  const out: string[] = [];
  for (const [key, prop] of Object.entries(schema.properties ?? {})) {
    const access = prop["x-cicord-access"] ?? "admin";
    if (role === "admin" || (role === "moderator" && access === "moderator")) out.push(key);
  }
  return out.toSorted();
}

/** Top-level config with schema defaults filled in (what the runtime calls the effective view). */
export function applyDefaults(
  schema: JsonSchema,
  config: { [key: string]: Json },
): { [key: string]: Json } {
  const out = { ...config };
  for (const [key, prop] of Object.entries(resolve(schema, schema).properties ?? {})) {
    const p = resolve(prop, schema);
    if (out[key] === undefined && p.default !== undefined)
      out[key] = JSON.parse(JSON.stringify(p.default)) as Json;
  }
  return out;
}
