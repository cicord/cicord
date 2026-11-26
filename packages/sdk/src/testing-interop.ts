/**
 * Interop fakes of @cicord/sdk/testing that follow the host's provider rules
 * (https://cicord.dev/docs/plugins/writing/, "Interop"):
 *
 * - a provider query or action runs in a fresh JS realm: the module is evaluated again and
 *   `onStart` does not run, so module-level state filled by `onStart` or by other handlers
 *   is absent;
 * - a query context is read-only: writes, schedules, status, Discord effects and outbound
 *   HTTP throw `CapabilityError` with capability `"read-only"`;
 * - input and output are JSON and are validated against the provider's contract
 *   (`contracts/<method>.v1.json`): a bad input is `invalid_input`, a bad output
 *   `handler_error`;
 * - an error thrown by the provider reaches the caller as `handler_error`, except an
 *   `InteropError` with code `denied` or `cycle`.
 */
import type { Context, InteropCall, InteropCode, Json, PluginDefinition } from "@cicord/sdk";
import { CapabilityError, InteropError, PluginUnavailableError } from "./index.js";

// ───────────────────────────────────────────────────────────── contracts

/** One contract file: `contracts/<method>.v1.json`. */
export interface ContractSchema {
  input?: object;
  output?: object;
}

const ANNOTATIONS = new Set([
  "$schema",
  "$id",
  "$comment",
  "title",
  "description",
  "default",
  "examples",
  "deprecated",
  "readOnly",
  "writeOnly",
]);

type Schema = Record<string, unknown> | boolean;

function typeOf(v: unknown): string {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  if (typeof v === "number") return Number.isInteger(v) ? "integer" : "number";
  return typeof v;
}

function sameJSON(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/**
 * Validates a JSON value against the JSON Schema subset used by contracts (type, properties,
 * required, additionalProperties, items, min/maxItems, enum, const, min/maxLength, pattern,
 * minimum, maximum, exclusiveMinimum/Maximum, anyOf, oneOf, allOf). Returns the first
 * violation, or undefined. An unsupported validation keyword throws, so a test never
 * validates less than the host.
 */
export function contractViolation(schema: object | boolean, value: unknown): string | undefined {
  return check(schema as Schema, value, "$");
}

function check(schema: Schema, v: unknown, at: string): string | undefined {
  if (schema === true) return undefined;
  if (schema === false) return `${at}: not allowed`;
  for (const key of Object.keys(schema)) {
    if (ANNOTATIONS.has(key)) continue;
    const rule = schema[key];
    const fail = (why: string) => `${at}: ${why}`;
    switch (key) {
      case "type": {
        const types = Array.isArray(rule) ? (rule as string[]) : [rule as string];
        const t = typeOf(v);
        if (!types.some((x) => x === t || (x === "number" && t === "integer")))
          return fail(`expected ${types.join(" or ")}, got ${t}`);
        break;
      }
      case "enum":
        if (!(rule as unknown[]).some((x) => sameJSON(x, v))) return fail("not in enum");
        break;
      case "const":
        if (!sameJSON(rule, v)) return fail("does not match const");
        break;
      case "minLength":
      case "maxLength":
      case "pattern":
        if (typeof v === "string") {
          // JSON Schema counts code points, not UTF-16 units.
          const n = Array.from(v).length;
          if (key === "minLength" && n < Number(rule)) return fail(`shorter than ${Number(rule)}`);
          if (key === "maxLength" && n > Number(rule)) return fail(`longer than ${Number(rule)}`);
          if (key === "pattern" && !new RegExp(String(rule), "u").test(v))
            return fail(`does not match ${String(rule)}`);
        }
        break;
      case "minimum":
      case "maximum":
      case "exclusiveMinimum":
      case "exclusiveMaximum":
        if (typeof v === "number") {
          const n = rule as number;
          if (key === "minimum" && v < n) return fail(`less than ${n}`);
          if (key === "maximum" && v > n) return fail(`greater than ${n}`);
          if (key === "exclusiveMinimum" && v <= n) return fail(`not greater than ${n}`);
          if (key === "exclusiveMaximum" && v >= n) return fail(`not less than ${n}`);
        }
        break;
      case "minItems":
      case "maxItems":
      case "items":
        if (Array.isArray(v)) {
          if (key === "minItems" && v.length < Number(rule))
            return fail(`fewer than ${Number(rule)} items`);
          if (key === "maxItems" && v.length > Number(rule))
            return fail(`more than ${Number(rule)} items`);
          if (key === "items")
            for (let i = 0; i < v.length; i++) {
              const e = check(rule as Schema, v[i], `${at}[${i}]`);
              if (e) return e;
            }
        }
        break;
      case "required":
        if (typeOf(v) === "object")
          for (const name of rule as string[])
            if (!Object.hasOwn(v as object, name)) return fail(`missing property ${name}`);
        break;
      case "properties":
        if (typeOf(v) === "object")
          for (const [name, sub] of Object.entries(rule as Record<string, Schema>)) {
            if (!Object.hasOwn(v as object, name)) continue;
            const e = check(sub, (v as Record<string, unknown>)[name], `${at}.${name}`);
            if (e) return e;
          }
        break;
      case "additionalProperties":
        if (typeOf(v) === "object") {
          const known = (schema.properties ?? {}) as Record<string, unknown>;
          for (const name of Object.keys(v as object)) {
            if (Object.hasOwn(known, name)) continue;
            if (rule === false) return fail(`unexpected property ${name}`);
            const e = check(rule as Schema, (v as Record<string, unknown>)[name], `${at}.${name}`);
            if (e) return e;
          }
        }
        break;
      case "allOf":
        for (const sub of rule as Schema[]) {
          const e = check(sub, v, at);
          if (e) return e;
        }
        break;
      case "anyOf":
        if (!(rule as Schema[]).some((sub) => !check(sub, v, at))) return fail("matches no anyOf");
        break;
      case "oneOf":
        if ((rule as Schema[]).filter((sub) => !check(sub, v, at)).length !== 1)
          return fail("does not match exactly one oneOf");
        break;
      default:
        throw new Error(
          `contract keyword ${JSON.stringify(key)} is not supported by the test harness`,
        );
    }
  }
  return undefined;
}

// ───────────────────────────────────────────────────────────── read-only realm

const WRITES = new Set([
  "send",
  "sendAs",
  "sendDM",
  "bulkDelete",
  "purge",
  "createThread",
  "typing",
  "reply",
  "edit",
  "editRetractableDM",
  "retractDM",
  "react",
  "removeReaction",
  "pin",
  "unpin",
  "crosspost",
  "endPoll",
  "setRoles",
  "addRole",
  "removeRole",
  "setNickname",
  "timeout",
  "removeTimeout",
  "kick",
  "ban",
  "unban",
  "moveTo",
  "disconnect",
  "createChannel",
  "editChannel",
  "deleteChannel",
  "incident",
  "requestSync",
  "recoverOwnedChannel",
  "verifyReaders",
  "createScheduledEvent",
  "publish",
  "fetch",
  "upsert",
  "clear",
  "after",
  "cancel",
  "link",
  "revoke",
  "report",
  "image",
  "pseudonym",
  "call",
  "commit",
  "apply",
  "set",
  "delete",
  "increment",
  "add",
  "remove",
]);
// kv.atomic() only builds a batch; its commit is the write.
const ATOMIC_BUILDER = new Set(["check", "set", "delete", "increment", "rankSet", "rankRemove"]);

function denied(path: readonly string[], name: string): boolean {
  if (path[path.length - 1] === "atomic" && ATOMIC_BUILDER.has(name)) return false;
  // media.hash is a host operation outside the read-only list; media.distance is local.
  if (name === "hash") return path[path.length - 1] === "media";
  return WRITES.has(name);
}

function opaque(v: object): boolean {
  return (
    v instanceof Date ||
    v instanceof RegExp ||
    v instanceof Map ||
    v instanceof Set ||
    v instanceof Error ||
    v instanceof Promise ||
    ArrayBuffer.isView(v) ||
    v instanceof ArrayBuffer
  );
}

/**
 * The read-only view of a context that a provider query receives. Reads (kv.get/list,
 * files, members, messages, ranking reads, plugins.query…) pass through; writes throw
 * `CapabilityError` with capability `"read-only"`, like the host.
 */
export function readOnlyContext(ctx: Context): Context {
  const cache = new WeakMap<object, unknown>();
  const proxies = new WeakSet<object>();
  const wrap = (value: unknown, path: readonly string[]): unknown => {
    if (value instanceof Promise) return value.then((v) => wrap(v, path));
    if ((typeof value !== "object" && typeof value !== "function") || value === null) return value;
    if (typeof value === "object" && opaque(value)) return value;
    if (proxies.has(value)) return value;
    const hit = cache.get(value);
    if (hit) return hit;
    const proxy: object = new Proxy(value, {
      get(target, key, receiver) {
        const v = Reflect.get(target, key, receiver);
        if (typeof key === "symbol") return v;
        const desc = Reflect.getOwnPropertyDescriptor(target, key);
        if (desc && !desc.configurable && !desc.writable && "value" in desc) return v;
        if (typeof v !== "function") return wrap(v, [...path, key]);
        if (denied(path, key))
          return () => {
            throw new CapabilityError(
              `${[...path, key].join(".")} is not allowed in a read-only query`,
              { capability: "read-only" },
            );
          };
        return (...args: unknown[]) => wrap(Reflect.apply(v, proxy, args), [...path, key]);
      },
      set() {
        return false;
      },
    });
    cache.set(value, proxy);
    proxies.add(proxy);
    return proxy;
  };
  return wrap(ctx, []) as Context;
}

// ───────────────────────────────────────────────────────────── provider calls

type Loaded = PluginDefinition | { default: PluginDefinition };
/** Returns a fresh copy of the provider module (see `callProvider`). */
export type ProviderLoader = () => Loaded | Promise<Loaded>;

export interface ProviderCall {
  /**
   * Loads a FRESH copy of the provider module, as the host does for each call. With Vitest:
   * `async () => { vi.resetModules(); return import("../src/index.ts"); }`. A loader that
   * returns a module seen before (or `live`) is refused.
   */
  load: ProviderLoader;
  /** The provider's world: its KV, files, server… are shared with the call. */
  harness: { readonly ctx: Context };
  /** Exactly one of `query` or `action`. */
  query?: string;
  action?: string;
  input: Json;
  /** The provider's `contracts/<method>.v1.json`. The host refuses calls without one. */
  contract?: ContractSchema;
  caller?: Partial<InteropCall["caller"]>;
  onBehalfOf?: InteropCall["onBehalfOf"];
  /** Required for actions. */
  idempotencyKey?: string;
  /** The module instance whose `onStart` ran in the test; it is refused as a realm. */
  live?: PluginDefinition;
}

const realms = new WeakSet<PluginDefinition>();

function interopFailure(code: InteropCode, message: string): InteropError {
  return new InteropError(code, message);
}

function jsonClone(v: unknown, what: string): Json {
  if (v === undefined) throw interopFailure("handler_error", `${what} is not JSON`);
  try {
    return JSON.parse(JSON.stringify(v)) as Json;
  } catch {
    throw interopFailure("handler_error", `${what} is not JSON`);
  }
}

/**
 * Calls a provider query or action like the host: in a fresh module realm without
 * `onStart`, with a read-only context for queries, JSON input and output, contract
 * validation and the host's error mapping. Resolves to the JSON output.
 *
 * ```ts
 * const out = await callProvider({
 *   load: async () => { vi.resetModules(); return import("../src/index.ts"); },
 *   harness: t, query: "search", input: { q: "desk" }, contract: searchContract,
 * });
 * ```
 */
export async function callProvider(call: ProviderCall): Promise<Json> {
  const kind = call.query !== undefined ? "query" : "action";
  const name = call.query ?? call.action;
  if (!name || (call.query !== undefined && call.action !== undefined))
    throw new TypeError("callProvider: pass exactly one of query or action");
  const label = `${call.harness.ctx.plugin.name}.${name}`;
  if (call.contract?.input) {
    if (contractViolation(call.contract.input, call.input))
      throw interopFailure("invalid_input", `input violates ${label} contract`);
  }
  if (kind === "action" && !call.idempotencyKey)
    throw interopFailure("invalid_input", "idempotencyKey is required");
  const loaded = await call.load();
  const def = "default" in loaded && loaded.default ? loaded.default : (loaded as PluginDefinition);
  if (def === call.live || realms.has(def))
    throw new Error(
      "callProvider: the loader returned a module instance that already ran; reset the module registry (vi.resetModules()) before importing",
    );
  realms.add(def);
  const handler = kind === "query" ? def.queries?.[name] : def.actions?.[name];
  if (!handler) throw interopFailure("not_found", `provider does not provide ${label}`);
  const ctx = kind === "query" ? readOnlyContext(call.harness.ctx) : call.harness.ctx;
  const interop = {
    caller: {
      plugin: "test-caller",
      bot: call.harness.ctx.bot.key,
      instance: "test-caller",
      source: "",
      verified: false,
      ...call.caller,
    },
    ...(call.onBehalfOf ? { onBehalfOf: call.onBehalfOf } : {}),
    ...(kind === "action" ? { idempotencyKey: call.idempotencyKey as string } : {}),
  } as InteropCall & { idempotencyKey: string };
  let result: unknown;
  try {
    result = await handler.run(ctx, jsonClone(call.input, "input"), interop);
  } catch (error) {
    if (error instanceof InteropError && (error.code === "denied" || error.code === "cycle"))
      throw interopFailure(error.code, error.message);
    throw interopFailure("handler_error", error instanceof Error ? error.message : String(error));
  }
  const output = jsonClone(result, "provider output");
  if (call.contract?.output && contractViolation(call.contract.output, output))
    throw interopFailure("handler_error", "invalid provider output");
  return output;
}

// ───────────────────────────────────────────────────────────── consumer side

/** A peer seen by a consumer under test. */
export interface FakeProvider {
  /** The provider's contracts by method name (`contracts/<method>.v1.json`). */
  contracts?: Record<string, ContractSchema>;
  queries?: Record<string, (input: Json, call: InteropCall) => unknown>;
  actions?: Record<
    string,
    (input: Json, call: InteropCall & { readonly idempotencyKey: string }) => unknown
  >;
  /** Route calls to the real provider module in a fresh realm (see `callProvider`). */
  realm?: { load: ProviderLoader; harness: { readonly ctx: Context } };
  /** Features the provider reports active (`ctx.plugins.hasFeature`). */
  features?: string[];
}

/**
 * A `ctx.plugins` for `createTestContext({ plugins })` whose peers validate the consumer's
 * input against their contracts, like the host. A missing peer throws
 * `PluginUnavailableError`.
 */
export function fakeProviders(
  providers: Record<string, FakeProvider>,
  caller: Partial<InteropCall["caller"]> = {},
): Context["plugins"] {
  const run = async (
    kind: "query" | "action",
    plugin: string,
    method: string,
    input: unknown,
    opts: { idempotencyKey?: string; timeoutMs?: number } | undefined,
  ): Promise<Json> => {
    const p = providers[plugin];
    if (!p) throw new PluginUnavailableError(plugin);
    const max = kind === "query" ? 5000 : 15000;
    if ((opts?.timeoutMs ?? 0) > max)
      throw interopFailure("invalid_input", "timeout exceeds maximum");
    const contract = p.contracts?.[method];
    const label = `${plugin}.${method}`;
    if (!contract?.input || contractViolation(contract.input, jsonClone(input, "input")))
      throw interopFailure("invalid_input", `input violates ${label} contract`);
    if (p.realm)
      return callProvider({
        load: p.realm.load,
        harness: p.realm.harness,
        [kind]: method,
        input: input as Json,
        contract,
        caller,
        ...(opts?.idempotencyKey ? { idempotencyKey: opts.idempotencyKey } : {}),
      });
    const fn = kind === "query" ? p.queries?.[method] : p.actions?.[method];
    if (!fn) throw interopFailure("not_found", `provider does not provide ${label}`);
    if (kind === "action" && !opts?.idempotencyKey)
      throw interopFailure("invalid_input", "idempotencyKey is required");
    const call = {
      caller: {
        plugin: "test-caller",
        bot: "test-bot",
        instance: "test-caller",
        source: "",
        verified: false,
        ...caller,
      },
      ...(kind === "action" ? { idempotencyKey: opts?.idempotencyKey } : {}),
    } as InteropCall & { idempotencyKey: string };
    let result: unknown;
    try {
      result = await fn(jsonClone(input, "input"), call);
    } catch (error) {
      if (error instanceof InteropError && (error.code === "denied" || error.code === "cycle"))
        throw interopFailure(error.code, error.message);
      throw interopFailure("handler_error", error instanceof Error ? error.message : String(error));
    }
    const output = jsonClone(result, "provider output");
    if (contract.output && contractViolation(contract.output, output))
      throw interopFailure("handler_error", "invalid provider output");
    return output;
  };
  return {
    has: (plugin) => Object.hasOwn(providers, plugin),
    hasFeature: (plugin, feature) => providers[plugin]?.features?.includes(feature) ?? false,
    query: ((plugin: string, method: string, input: unknown, opts?: { timeoutMs?: number }) =>
      run("query", plugin, method, input, opts)) as Context["plugins"]["query"],
    call: ((
      plugin: string,
      method: string,
      input: unknown,
      opts: { idempotencyKey: string; timeoutMs?: number },
    ) => run("action", plugin, method, input, opts)) as Context["plugins"]["call"],
  };
}
