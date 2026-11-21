# ADR-0012 — Discord simulator

**Status:** Accepted

## Context
Testing the reconciler, the bot host and plugins against real Discord is slow, rate
limited and hard to automate. Most of CODI will be written with AI agents, which are
reliable when they have precise contracts and fast tests.

## Decision
- Build `internal/sim`: an in-memory implementation of the subset of the Discord REST API
  and gateway used by CODI (servers, roles, channels, overwrites, members, messages,
  interactions, application commands).
- Used by Go tests, `codi plugin test`, `codi dev --sim` and spec conformance tests.
- A recorded-fixture mode compares simulator behaviour with real Discord to keep it honest.

## Consequences
- An extra component to maintain, kept narrow to what CODI actually calls.
- Enables deterministic end-to-end tests in CI without tokens.
