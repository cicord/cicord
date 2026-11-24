// Copies the normative SDK contract into the package: spec/sdk/cicord-sdk.d.ts is the single
// source of truth for @cicord/sdk's types. Never edit dist/index.d.ts by hand.
import { copyFileSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const pkg = join(dirname(fileURLToPath(import.meta.url)), "..");
const spec = join(pkg, "../../spec/sdk/cicord-sdk.d.ts");
const out = join(pkg, "dist");

mkdirSync(out, { recursive: true });
// tsc also emits a declaration for src/index.ts; the spec replaces it.
rmSync(join(out, "index.d.ts"), { force: true });
copyFileSync(spec, join(out, "index.d.ts"));

const copied = readFileSync(join(out, "index.d.ts"), "utf8");
if (!copied.includes("export declare function definePlugin")) {
  throw new Error("copy-spec-types: unexpected content in spec/sdk/cicord-sdk.d.ts");
}
console.log("@cicord/sdk: dist/index.d.ts <- spec/sdk/cicord-sdk.d.ts");
