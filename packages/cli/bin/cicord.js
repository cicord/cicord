#!/usr/bin/env node
// The `cicord` command of the npm package @cicord/cli: runs the release binary of the
// platform package (@cicord/cli-<os>-<arch>, an optional dependency of @cicord/cli at the same
// version; npm installs only the one whose "os" and "cpu" match), with the same arguments,
// standard streams and exit code.
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
/** @type {Record<string, { goos: string, goarch: string, binary: string }>} */
const platforms = require("../platforms.json");
const docs = "https://cicord.dev/docs/getting-started/installation/";

/**
 * @param {string} message
 * @returns {never}
 */
function fail(message) {
  process.stderr.write(`@cicord/cli: ${message}\n`);
  process.exit(1);
}

const key = `${process.platform}-${process.arch}`;
const platform = platforms[key];
if (!platform) {
  fail(
    `no cicord binary for ${key}: the npm package supports ${Object.keys(platforms).join(", ")}.\n` +
      `Other platforms: build from source (${docs}).`,
  );
}

const pkg = `@cicord/cli-${key}`;
/** @type {string} */
let binary = "";
try {
  binary = require.resolve(`${pkg}/bin/${platform.binary}`);
} catch {
  fail(
    `the platform package ${pkg} is not installed. It is an optional dependency of @cicord/cli:\n` +
      `reinstall without --no-optional / --omit=optional (npm i -g @cicord/cli), and make sure your\n` +
      `registry or mirror serves ${pkg}. Other ways to install cicord: ${docs}`,
  );
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  fail(`could not run ${binary}: ${result.error.message}`);
}
if (result.signal) {
  // The binary died of a signal: die of the same one, so the caller sees it.
  process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
