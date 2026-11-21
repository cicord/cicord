# ADR-0005 — Plugins in TypeScript

**Status:** Accepted (supersedes the initial Lua idea)

## Context
Plugins must be easy to write for the community. Candidates: Lua (gopher-lua), JavaScript /
TypeScript embedded in Go, WASM (Extism), Starlark-like DSLs.

| | Lua | **TypeScript** | WASM |
|---|---|---|---|
| Familiar to Discord bot devs | low | **high** (discord.js) | low |
| Tooling, types, autocompletion | weak | **strong** | varies |
| Same language as the portal SDK | no | **yes** | no |
| Sandbox | CPU limits | CPU limits (memory depends on engine) | best |

## Decision
- Plugins are written in **TypeScript** against `@codi/sdk` (contract: `spec/sdk/codi-sdk.d.ts`).
- Bundled by **esbuild used as a Go library** (`codi plugin build`, and inside the runtime):
  no Node.js dependency anywhere.
- Executed by **Sobek** (Grafana's goja fork used by k6: pure Go, ESM, async/await).
- Bundle contract (`internal/build`, confirmed in implementation): a single IIFE script,
  **target ES2022** minus async generators (Sobek does not support them; esbuild lowers
  them), platform neutral, `@codi/sdk` external (the only module the host resolves, through
  a global `require`), Node built-ins rejected at build time with a readable error, and an
  **inline source map** appended to the bundle so Sobek maps stack traces back to the
  TypeScript sources (`src/index.ts:12:5`).
- Host (`internal/plugin`): one Sobek VM and event loop per instance, a synchronous budget
  per loop turn (Sobek `Interrupt`), a wall-clock timeout per handler, host calls on other
  goroutines settled back on the loop, and `setTimeout`/`console` provided by the host.
- Execution is isolated in `internal/plugin` (bundle in, `Instance` out); a QuickJS-on-wazero
  or WASM backend for SaaS-grade memory isolation would be introduced behind that boundary.
  No language-agnostic `PluginRuntime` interface exists yet: it will be extracted when a
  second backend is added.
- Static information (capabilities, intents, config schema) lives in `plugin.yaml`; dynamic
  definitions (commands, handlers, routes) come from the code via `definePlugin()` and are
  extracted at build time.

## Consequences
- Not Node.js: pure-JS npm libraries can be bundled, Node built-ins (`fs`, `net`, …) cannot.
  All I/O goes through the SDK.
- Plugins can be full-stack (server part + React components) in a single language.
- Sobek has no hard memory cap; acceptable self-hosted, to be addressed for SaaS.
- Sobek supports ES2022 except async generators; plugin authors write modern TypeScript and
  the bundler lowers the rest. Source maps are inline, so a bundle is self-contained (no
  side file to ship with it).
