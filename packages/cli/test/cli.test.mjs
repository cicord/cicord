// Tests of scripts/build-platform-packages.mjs (the packages release-npm.yml publishes) and
// of bin/cicord.js (the `cicord` command of @cicord/cli), with fake binaries: shell scripts
// that print their arguments and exit with a given code.
import { spawnSync } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, describe, expect, test } from "vite-plus/test";

const pkgDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const script = join(pkgDir, "scripts", "build-platform-packages.mjs");
/** @type {Record<string, { goos: string, goarch: string, binary: string }>} */
const platforms = JSON.parse(readFileSync(join(pkgDir, "platforms.json"), "utf8"));
const { version } = JSON.parse(readFileSync(join(pkgDir, "package.json"), "utf8"));
const unix = process.platform !== "win32";

const root = mkdtempSync(join(tmpdir(), "cicord-cli-test-"));
afterAll(() => rmSync(root, { recursive: true, force: true }));
let n = 0;
const tempDir = () => {
  const d = join(root, String(n++));
  mkdirSync(d, { recursive: true });
  return d;
};

const fakeBinary =
  '#!/bin/sh\necho "fake cicord $*"\n[ -z "$FAKE_SIGNAL" ] || kill -"$FAKE_SIGNAL" $$\nexit "${FAKE_EXIT:-0}"\n';

/** Release binaries for every platform (but `skip`), laid out as release-npm.yml extracts them. */
function fakeBinaries(skip = "") {
  const dir = tempDir();
  for (const [key, p] of Object.entries(platforms)) {
    if (key === skip) continue;
    mkdirSync(join(dir, `${p.goos}_${p.goarch}`), { recursive: true });
    writeFileSync(join(dir, `${p.goos}_${p.goarch}`, p.binary), fakeBinary, { mode: 0o644 });
  }
  return dir;
}

/** @param {string[]} args */
const build = (args) => spawnSync(process.execPath, [script, ...args], { encoding: "utf8" });

/** @param {string} dir */
const readJSON = (dir) => JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));

describe("build-platform-packages", () => {
  test("writes one package per platform and the main package", () => {
    const out = join(tempDir(), "out");
    const r = build(["--version", version, "--binaries", fakeBinaries(), "--out", out]);
    expect(r.stderr).toBe("");
    expect(r.status).toBe(0);

    const keys = Object.keys(platforms);
    expect(keys.toSorted()).toEqual([
      "darwin-arm64",
      "darwin-x64",
      "linux-arm64",
      "linux-x64",
      "win32-x64",
    ]);
    for (const key of keys) {
      const dir = join(out, `cli-${key}`);
      const m = readJSON(dir);
      const [os, cpu] = key.split("-");
      expect(m.name).toBe(`@cicord/cli-${key}`);
      expect(m.version).toBe(version);
      expect([m.os, m.cpu]).toEqual([[os], [cpu]]);
      // Provenance needs the repository; no bin (only @cicord/cli has the command).
      expect(m.repository.url).toBe("git+https://github.com/cicord/cicord.git");
      expect(m.publishConfig.provenance).toBe(true);
      expect(m.bin).toBeUndefined();
      const bin = join(dir, "bin", platforms[key].binary);
      expect(readFileSync(bin, "utf8")).toBe(fakeBinary);
      if (unix) expect(statSync(bin).mode & 0o777).toBe(0o755);
      expect(existsSync(join(dir, "LICENSE"))).toBe(true);
      expect(existsSync(join(dir, "README.md"))).toBe(true);
    }

    const cli = join(out, "cli");
    const m = readJSON(cli);
    expect(m.name).toBe("@cicord/cli");
    expect(m.version).toBe(version);
    expect(m.bin).toEqual({ cicord: "bin/cicord.js" });
    expect(m.optionalDependencies).toEqual(
      Object.fromEntries(keys.map((k) => [`@cicord/cli-${k}`, version])),
    );
    expect(m.scripts).toBeUndefined();
    expect(m.devDependencies).toBeUndefined();
    for (const f of m.files) expect(existsSync(join(cli, f)), f).toBe(true);
    expect(r.stdout.trim().split("\n")).toHaveLength(keys.length + 1);
  });

  test("refuses an incomplete release and writes nothing", () => {
    const out = join(tempDir(), "out");
    const r = build([
      "--version",
      version,
      "--binaries",
      fakeBinaries("linux-arm64"),
      "--out",
      out,
    ]);
    expect(r.status).toBe(1);
    expect(r.stderr).toMatch(/missing binary for linux-arm64/);
    expect(existsSync(out)).toBe(false);
  });

  test("refuses a version that is not the package's, and missing arguments", () => {
    const r = build([
      "--version",
      "99.0.0",
      "--binaries",
      fakeBinaries(),
      "--out",
      join(tempDir(), "out"),
    ]);
    expect(r.status).toBe(1);
    expect(r.stderr).toMatch(/packages\/cli\/package.json is .*, not 99\.0\.0/);
    expect(build(["--version", version]).status).toBe(1);
  });
});

describe.skipIf(!unix)("bin/cicord.js", () => {
  const key = `${process.platform}-${process.arch}`;
  const supported = key in platforms;

  /** An installed @cicord/cli, with the platform package of this machine unless `withBinary` is false. */
  function install(withBinary = true) {
    const out = join(tempDir(), "out");
    expect(build(["--version", version, "--binaries", fakeBinaries(), "--out", out]).status).toBe(
      0,
    );
    const modules = join(tempDir(), "node_modules", "@cicord");
    cpSync(join(out, "cli"), join(modules, "cli"), { recursive: true });
    if (withBinary && supported) {
      cpSync(join(out, `cli-${key}`), join(modules, `cli-${key}`), { recursive: true });
    }
    return join(modules, "cli", "bin", "cicord.js");
  }

  /**
   * @param {string} shim
   * @param {string[]} args
   * @param {Record<string, string>} env
   * @param {string[]} nodeArgs
   */
  const run = (shim, args, env = {}, nodeArgs = []) =>
    spawnSync(process.execPath, [...nodeArgs, shim, ...args], {
      encoding: "utf8",
      env: { ...process.env, ...env },
    });

  test.skipIf(!supported)("runs the platform binary with the arguments and its exit code", () => {
    const shim = install();
    const ok = run(shim, ["version", "--json", "a b"]);
    expect(ok.stderr).toBe("");
    expect(ok.status).toBe(0);
    expect(ok.stdout).toBe("fake cicord version --json a b\n");
    expect(run(shim, ["validate"], { FAKE_EXIT: "3" }).status).toBe(3);
  });

  test.skipIf(!supported)("dies of the signal that killed the binary", () => {
    const r = run(install(), [], { FAKE_SIGNAL: "TERM" });
    expect(r.status).toBeNull();
    expect(r.signal).toBe("SIGTERM");
  });

  test.skipIf(!supported)("explains a missing platform package", () => {
    const r = run(install(false), ["version"]);
    expect(r.status).toBe(1);
    expect(r.stderr).toContain(`the platform package @cicord/cli-${key} is not installed`);
    expect(r.stderr).toContain("--omit=optional");
  });

  test("explains an unsupported platform", () => {
    const preload = join(tempDir(), "aix.mjs");
    writeFileSync(preload, 'Object.defineProperty(process, "platform", { value: "aix" });\n');
    const r = run(install(), ["version"], {}, ["--import", preload]);
    expect(r.status).toBe(1);
    expect(r.stderr).toContain(`no cicord binary for aix-${process.arch}`);
    expect(r.stderr).toContain("darwin-arm64, darwin-x64, linux-arm64, linux-x64, win32-x64");
  });
});
