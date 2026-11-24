# ADR-0014 — Discord library

**Status:** Accepted

## Context
Candidates in Go: `discordgo` (most used, older design), `disgo` (modern, strong support for
interactions, components and application commands), `arikawa`. The runtime must stay a
single static binary (`CGO_ENABLED=0`, ADR-0001) and the rest of the code must not depend
on the library (ADR-0009, ADR-0012).

## Decision
- Use **disgo** (`github.com/disgoorg/disgo`), wrapped by `internal/discord/disgoadapter`,
  which implements the `internal/discord` interfaces (`Connector`, `Session`, `Client`). No
  other package imports disgo; the adapter is the only place where the word "guild"
  appears (ADR-0009).
- The adapter uses disgo's **`rest`** and **`gateway`** packages directly, not the
  high-level `bot` package:
  - `rest.Client` carries every REST call: endpoints, auth, per-route and global rate
    limiting with `429` retries;
  - `gateway.Gateway` carries the websocket: identify with the selected intents,
    heartbeats, resume, reconnect with backoff, zstd compression;
  - payloads are encoded/decoded with the adapter's own flat wire structs (one mapping
    layer shared by REST and gateway, explicit JSON `null` where the contract needs it,
    interaction option order preserved) rather than disgo's typed models.
- disgo's `bot`, `voice`, `sharding` and `httpserver` packages are deliberately **not
  imported**: they pull voice / DAVE code Cicord does not need, which would compromise the
  `CGO_ENABLED=0` build.
- Member chunking is requested only when the `ServerMembers` intent is enabled (overridable).

## Consequences
- The spike criteria of the proposal (gateway with selected intents, per-server
  application command sync, interaction deferral, rate limits on bulk role/channel edits)
  are covered by the adapter and its tests against an `httptest` fake of the REST API and
  gateway.
- No sharding: one gateway connection per bot, which is fine for the target scale
  (self-hosted communities). Sharding would be added inside the adapter.
- disgo upgrades only affect the adapter. Because only `rest` and `gateway` are used, the
  surface exposed to breaking changes is small, at the cost of maintaining our own wire
  structs for the objects Cicord uses.
- Voice features are out of scope; adding them would require revisiting the no-cgo
  constraint.
