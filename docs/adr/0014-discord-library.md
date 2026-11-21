# ADR-0014 — Discord library

**Status:** Proposed

## Context
Candidates in Go: `discordgo` (most used, older design), `disgo` (modern, strong support for
interactions, components and application commands), `arikawa`.

## Proposal
Use **disgo**, wrapped by `internal/discord` so the rest of the code never imports it directly
(this adapter is also where server ↔ guild naming is mapped, ADR-0009).

## To decide
During phase 1, after a spike covering: gateway with selected intents, application command
sync per server, interaction deferral, rate-limit handling on bulk role/channel edits.
