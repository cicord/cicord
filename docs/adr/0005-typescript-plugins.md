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
- Execution sits behind a language-agnostic `PluginRuntime` interface; a QuickJS-on-wazero
  or WASM backend can be added for SaaS-grade memory isolation.
- Static information (capabilities, intents, config schema) lives in `plugin.yaml`; dynamic
  definitions (commands, handlers, routes) come from the code via `definePlugin()` and are
  extracted at build time.

## Consequences
- Not Node.js: pure-JS npm libraries can be bundled, Node built-ins (`fs`, `net`, …) cannot.
  All I/O goes through the SDK.
- Plugins can be full-stack (server part + React components) in a single language.
- Sobek has no hard memory cap; acceptable self-hosted, to be addressed for SaaS.
