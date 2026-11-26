#!/usr/bin/env node
// Builds the npm packages of the cicord binary from the binaries of a GitHub release
// (.github/workflows/release-npm.yml; https://cicord.dev/docs/project/releasing/):
//
//   node packages/cli/scripts/build-platform-packages.mjs --version <version> --binaries <dir> --out <dir>
//
// <binaries>/<goos>_<goarch>/ holds the binary extracted from cicord_<version>_<goos>_<goarch>
// (cicord, or cicord.exe on Windows), for every platform of platforms.json. It writes:
//
//   <out>/cli-<os>-<cpu>/   @cicord/cli-<os>-<cpu>: package.json ("os", "cpu"), bin/<binary>
//                           (mode 0755), README.md, LICENSE
//   <out>/cli/              @cicord/cli (this package): its files, with every platform package
//                           as an optional dependency at exactly <version>
//
// The version must be the version of packages/cli/package.json. Nothing is published.
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { dirname, join, resolve } from "node:path";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";

const pkgDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

/**
 * @param {string} message
 * @returns {never}
 */
function fail(message) {
  process.stderr.write(`build-platform-packages: ${message}\n`);
  process.exit(1);
}

const { values } = parseArgs({
  options: {
    version: { type: "string" },
    binaries: { type: "string" },
    out: { type: "string" },
  },
});
if (!values.version || !values.binaries || !values.out) {
  fail("usage: build-platform-packages.mjs --version <version> --binaries <dir> --out <dir>");
}
const version = values.version;
const binaries = resolve(values.binaries);
const out = resolve(values.out);

/** @type {Record<string, { goos: string, goarch: string, binary: string }>} */
const platforms = JSON.parse(readFileSync(join(pkgDir, "platforms.json"), "utf8"));
/** @type {Record<string, any>} */
const main = JSON.parse(readFileSync(join(pkgDir, "package.json"), "utf8"));

if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) fail(`${version} is not a version`);
if (main.version !== version) {
  fail(
    `packages/cli/package.json is ${main.version}, not ${version}: bump it (scripts/release.sh)`,
  );
}

// Every binary first: nothing is written for an incomplete release.
const found = Object.entries(platforms).map(([key, p]) => {
  const src = join(binaries, `${p.goos}_${p.goarch}`, p.binary);
  if (!existsSync(src) || !statSync(src).isFile() || statSync(src).size === 0) {
    fail(`missing binary for ${key}: ${src}`);
  }
  return { key, p, src };
});

rmSync(out, { recursive: true, force: true });
const license = join(pkgDir, "LICENSE");

for (const { key, p, src } of found) {
  const [os, cpu] = key.split("-");
  const name = `@cicord/cli-${key}`;
  const dir = join(out, `cli-${key}`);
  mkdirSync(join(dir, "bin"), { recursive: true });
  const dst = join(dir, "bin", p.binary);
  copyFileSync(src, dst);
  chmodSync(dst, 0o755);
  copyFileSync(license, join(dir, "LICENSE"));
  copyFileSync(join(pkgDir, "../../NOTICE"), join(dir, "NOTICE"));
  writeFileSync(
    join(dir, "README.md"),
    `# ${name}\n\nThe \`cicord\` ${version} binary for ${key} (${p.goos}/${p.goarch}), from the GitHub release\n` +
      `[v${version}](https://github.com/cicord/cicord/releases/tag/v${version}). Install\n` +
      `[@cicord/cli](https://www.npmjs.com/package/@cicord/cli) instead: it installs this package on ${key}.\n`,
  );
  const manifest = {
    name,
    version,
    description: `The cicord binary for ${key}. Install @cicord/cli instead.`,
    homepage: main.homepage,
    bugs: main.bugs,
    license: main.license,
    repository: main.repository,
    os: [os],
    cpu: [cpu],
    // No "bin" (the binary is run by @cicord/cli) and no "exports" (bin/<binary> resolves).
    files: ["bin", "README.md", "LICENSE", "NOTICE"],
    preferUnplugged: true,
    publishConfig: main.publishConfig,
  };
  writeFileSync(join(dir, "package.json"), `${JSON.stringify(manifest, null, 2)}\n`);
}

const cli = join(out, "cli");
for (const f of main.files) {
  mkdirSync(dirname(join(cli, f)), { recursive: true });
  copyFileSync(join(pkgDir, f), join(cli, f));
}
chmodSync(join(cli, "bin", "cicord.js"), 0o755);
const published = { ...main };
delete published.scripts;
delete published.devDependencies;
published.optionalDependencies = Object.fromEntries(
  found.map(({ key }) => [`@cicord/cli-${key}`, version]),
);
writeFileSync(join(cli, "package.json"), `${JSON.stringify(published, null, 2)}\n`);

process.stdout.write(
  `${[...found.map(({ key }) => `cli-${key}`), "cli"].map((d) => join(out, d)).join("\n")}\n`,
);
