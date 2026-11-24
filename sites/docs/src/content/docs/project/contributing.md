---
title: Contributing
description: Build Cicord from source, run the Go and JavaScript tests, and find your way around the repository.
---

## Prerequisites

Go 1.27, Node 26 and pnpm (the version is pinned by `packageManager` in the root
`package.json`). See [Installation](../../getting-started/installation/).

## Make targets

| Target | What it does |
|---|---|
| `make all` | `make ui` then `make build`: the complete binary in `bin/cicord`. |
| `make ui` | `pnpm install --frozen-lockfile`, builds the JS packages, then `scripts/sync-ui.sh` copies the admin console, the reference portal and the portal kit (`@cicord/*` tarballs and template) into `internal/webui`, where `go build` embeds them. |
| `make build` | The Go binary only (`CGO_ENABLED=0`, version and commit in the ldflags). Embeds whatever UI build is in `internal/webui`. |
| `make test` | `make test-go` and `make test-js`. |
| `make test-go` | `go test -race ./...` |
| `make test-js` | `pnpm -r test` |
| `make vet` | `go vet ./...` |
| `make check` | `vet` and `test`. |
| `make clean` | Removes `bin/`. |

`VERSION` (default `0.1.0`) can be overridden: `make build VERSION=0.2.0`.

## JavaScript workspace

The JS side is a pnpm workspace (`packages/*` and `sites/*`) using Vite+ (`vp`) for building,
testing, linting and formatting.

```sh
pnpm test      # builds the packages, runs every package's tests and the example plugins' unit tests
pnpm check     # format, lint, types, and type-checks every example plugin
pnpm exec vp test examples/showcase/plugins/polls   # one plugin's unit tests
```

## What CI runs

On every push to `main` and every pull request:

- **Go**: `gofmt -l` (must be empty), `go vet`, `go test -race`, a static build, and
  `cicord validate` on both examples.
- **JS**: `pnpm install --frozen-lockfile`, `pnpm run check` (builds every package and site,
  then format, lint, types and the example plugins) and `pnpm test`.
- **Docker**: the container image is built (not pushed) and smoke-tested.
- **Static analysis**: golangci-lint (`.golangci.yml`: staticcheck, errcheck, gosec, dupl,
  errorlint, noctx…) and govulncheck for Go; oxlint (correctness, suspicious and perf
  categories, React hooks, jsx-a11y), [Knip](https://knip.dev) (unused files, exports and
  dependencies) and jscpd (duplication) for TypeScript; Semgrep (Go, React, TypeScript,
  JavaScript and security-audit rules) on everything; Gitleaks on the whole git history.
  `make lint` runs the same tools locally.

`sites.yml` deploys https://cicord.dev to GitHub Pages (run on demand for now).
Dependabot proposes weekly, grouped updates of Go modules, npm packages and GitHub Actions.

## How things are tested

- The **Discord simulator** (`internal/sim`) is an in-process implementation of Cicord's Discord
  interfaces. Reconciler, runtime and API tests run against it, fast and without tokens.
- `examples/showcase` is the end-to-end fixture: its seven plugins are driven through the real
  runtime on the simulator (`internal/runtime/showcase_test.go`), their portal routes through
  the HTTP API, and `cicord plugin build`, `typegen --check` and `cicord plugin test` run on each.
  [`docs/plugin-coverage.md`](https://github.com/cicord/cicord/blob/main/docs/plugin-coverage.md)
  maps every SDK feature to the test that exercises it.
- The real Discord adapter (disgo) is tested against an HTTP fake of the REST API and gateway.
- Plugin unit tests use `@cicord/sdk/testing`.

## Repository layout

| Path | Content |
|---|---|
| `cmd/cicord/` | The single binary: CLI and `cicord serve`. |
| `internal/` | The runtime: `api`, `build`, `cli`, `config`, `discord` (and `disgoadapter`), `plugin`, `reconcile`, `registry`, `runtime`, `sim`, `source`, `state`, `writeback`, `webui`… |
| `packages/` | `sdk`, `client`, `react`, `ui`, `admin` (the admin console), `create-cicord-portal` (the reference portal). |
| `spec/` | The contracts: JSON Schemas, the SDK types, config semantics, the HTTP API. |
| `docs/` | The plugin guide, the deployment guide, the setup wizard, Discord parity, ADRs. |
| `examples/` | `basic`, `showcase` and `embed`. |
| `sites/` | The landing page and this documentation (Astro). |

`spec/` is normative: when the behavior and a document disagree, the schemas and
`spec/sdk/cicord-sdk.d.ts` win.

## This documentation

The site lives in `sites/docs` (Astro Starlight). Every page has an **Edit page** link.

```sh
pnpm -C sites/docs dev       # live preview
pnpm -C sites/docs build     # static build into sites/docs/dist
```
