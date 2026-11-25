#!/usr/bin/env node
// create-cicord-portal — copies the reference Cicord community portal into a new directory.
//
//   node packages/create-cicord-portal/bin/create-cicord-portal.mjs portal
//
// The package is not published on npm: outside this repository, use `cicord portal init`.
//
// The portal is this package itself (src/, index.html, vite.config.ts…). Workspace-only
// version specifiers (workspace:*, catalog:) are rewritten to real versions.
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const template = resolve(here, "..");
const SKIP = new Set(["node_modules", "dist", "bin", ".turbo", ".vite"]);

function usage(code) {
  console.log(`Usage: create-cicord-portal <directory> [--force]

Creates a Cicord community portal (React + Vite+) in <directory>.
Put it in the "portal/" directory of your Cicord repository and set cicord.yaml
spec.portal.path: ./portal/dist to have the runtime serve its committed build.
Inside a Cicord repository, prefer \`cicord portal init\` (vendors the @cicord/* packages).`);
  process.exit(code);
}

const args = process.argv.slice(2);
if (args.includes("-h") || args.includes("--help")) usage(0);
const force = args.includes("--force");
const targetArg = args.find((a) => !a.startsWith("-"));
if (!targetArg) usage(1);

const target = resolve(process.cwd(), targetArg);
if (existsSync(target) && readdirSync(target).length > 0 && !force) {
  console.error(
    `create-cicord-portal: ${target} is not empty (use --force to write into it anyway)`,
  );
  process.exit(1);
}

// ───────────────────────────────────────────── versions for workspace-only specifiers

/** Minimal reader for the `catalog:` map of a pnpm-workspace.yaml (flat `name: version` lines). */
function readCatalog(file) {
  const catalog = {};
  if (!existsSync(file)) return catalog;
  let inCatalog = false;
  for (const line of readFileSync(file, "utf8").split(/\r?\n/)) {
    if (/^catalog:\s*$/.test(line)) {
      inCatalog = true;
      continue;
    }
    if (inCatalog) {
      if (/^\S/.test(line)) break;
      const m = /^\s+(?:"([^"]+)"|'([^']+)'|([^\s:"']+))\s*:\s*(.+?)\s*$/.exec(line);
      if (m) catalog[m[1] ?? m[2] ?? m[3]] = m[4].replace(/^["']|["']$/g, "");
    }
  }
  return catalog;
}

const workspaceRoot = resolve(template, "../..");
const catalog = readCatalog(join(workspaceRoot, "pnpm-workspace.yaml"));
const siblingVersion = (name) => {
  const dir = {
    "@cicord/client": "client",
    "@cicord/react": "react",
    "@cicord/ui": "ui",
    "@cicord/sdk": "sdk",
  }[name];
  const file = dir ? join(template, "..", dir, "package.json") : undefined;
  if (file && existsSync(file)) return `^${JSON.parse(readFileSync(file, "utf8")).version}`;
  return "latest";
};

function resolveSpec(name, spec) {
  if (spec.startsWith("workspace:")) return siblingVersion(name);
  if (spec.startsWith("catalog:")) {
    const v = catalog[name];
    if (!v) return "latest";
    // npm aliases (vite -> vite-plus-core) stay aliases.
    return v;
  }
  return spec;
}

// ───────────────────────────────────────────── copy

mkdirSync(target, { recursive: true });
for (const entry of readdirSync(template)) {
  if (SKIP.has(entry) || entry === "package.json") continue;
  cpSync(join(template, entry), join(target, entry), {
    recursive: true,
    filter: (src) =>
      !relative(template, src)
        .split(/[\\/]/)
        .some((part) => SKIP.has(part)),
  });
}

const pkg = JSON.parse(readFileSync(join(template, "package.json"), "utf8"));
const out = {
  name:
    basename(target)
      .toLowerCase()
      .replace(/[^a-z0-9._-]+/g, "-") || "cicord-portal",
  private: true,
  version: "0.0.0",
  type: "module",
  scripts: pkg.scripts,
  dependencies: {},
  devDependencies: {},
};
for (const field of ["dependencies", "devDependencies"]) {
  for (const [name, spec] of Object.entries(pkg[field] ?? {}))
    out[field][name] = resolveSpec(name, spec);
}
// Vite+ expects `vite` to be its core build (see pnpm overrides in a Vite+ workspace).
if (out.devDependencies.vite?.startsWith("npm:")) {
  out.pnpm = { overrides: { vite: out.devDependencies.vite } };
}
writeFileSync(join(target, "package.json"), `${JSON.stringify(out, null, 2)}\n`);
// dist/ stays committed: the Cicord runtime serves the committed build (cicord.yaml
// spec.portal.path, https://cicord.dev/docs/portal/custom-portal/). Add it here when the portal is hosted elsewhere.
writeFileSync(join(target, ".gitignore"), "node_modules/\n*.local\n");

const rel = relative(process.cwd(), target) || ".";
console.log(`✓ Cicord portal created in ${rel}

Next steps:
  cd ${rel}
  pnpm install
  pnpm dev            # mock mode: no runtime needed
  pnpm dev:runtime    # against a local \`cicord dev\` (CICORD_RUNTIME_URL, default http://127.0.0.1:7420)
  pnpm build          # static files in dist/: commit them, cicord.yaml spec.portal.path: ./portal/dist`);
