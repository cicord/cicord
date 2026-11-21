/**
 * @codi/sdk runtime for authoring and unit tests.
 *
 * Inside CODI the Go plugin host provides the "@codi/sdk" module; this implementation only
 * exists so that plugins can be imported and tested in Node. The public *types* of this
 * package are spec/sdk/codi-sdk.d.ts, copied verbatim into dist/index.d.ts at build time.
 * The conformance block at the bottom of this file fails type-checking if this runtime ever
 * drifts from the spec.
 */
import type * as Spec from "@codi/sdk";

const CAPABILITY_IN_MESSAGE = /capability "?([a-z]+(?:\.[a-z]+)+)"?/;

/** Thrown when a call needs a capability that is not granted in bot.yaml. */
export class CapabilityError extends Error {
  readonly capability: string;

  /**
   * The spec declares no constructor (it inherits `Error`'s), so the capability is taken from
   * `options.capability` or parsed from a message like `missing capability "storage.kv"`.
   * Use `capabilityError()` from "@codi/sdk/testing" to build one in tests.
   */
  constructor(message?: string, options?: ErrorOptions & { capability?: string }) {
    super(message, options);
    this.name = "CapabilityError";
    this.capability = options?.capability ?? CAPABILITY_IN_MESSAGE.exec(message ?? "")?.[1] ?? "";
  }
}

/** Entry point of a plugin: `export default definePlugin({ ... })`. Identity at run time. */
export function definePlugin(definition: Spec.PluginDefinition): Spec.PluginDefinition {
  return definition;
}

/** Helper that infers option types for `interaction.options`. Identity at run time. */
export function command<const O extends Record<string, Spec.CommandOption> = {}>(
  definition: Spec.CommandDefinition<O>,
): Spec.CommandDefinition<O> {
  return definition;
}

// ───────────────────────────────────────────────────────────── conformance with the spec
// Type-level only: these assignments break `tsc` when the runtime and the spec disagree.
type Assert<T extends true> = T;
type SameConstructor = typeof CapabilityError extends typeof Spec.CapabilityError
  ? InstanceType<typeof CapabilityError> extends Spec.CapabilityError
    ? true
    : false
  : false;
export type __SpecConformance = [
  Assert<typeof definePlugin extends typeof Spec.definePlugin ? true : false>,
  Assert<typeof command extends typeof Spec.command ? true : false>,
  Assert<SameConstructor>,
];
