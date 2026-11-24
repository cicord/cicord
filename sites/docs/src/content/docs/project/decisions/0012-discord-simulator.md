---
title: "ADR-0012 — Discord simulator"
description: "ADR-0012: In-process Discord simulator behind the discord interfaces."
sidebar:
  label: "0012 · Discord simulator"
  order: 12
---

**Status:** Accepted (updated after implementation: in-process, not an HTTP-level fake)

## Context
Testing the reconciler, the bot host and plugins against real Discord is slow, rate
limited and hard to automate. Most of Cicord will be written with AI agents, which are
reliable when they have precise contracts and fast tests. Two designs were possible:
an HTTP/websocket fake that speaks Discord's wire protocol (so the real adapter could be
pointed at it), or an in-process implementation of Cicord's own Discord interface.

## Decision
- All of Cicord talks to Discord through the `internal/discord` interfaces
  (`Connector`, `Session`, `Client`, in Cicord vocabulary, ADR-0009). There are two
  implementations: `internal/discord/disgoadapter` (real Discord, ADR-0014) and
  **`internal/sim`**, an **in-process, in-memory** implementation of the same interfaces.
- The simulator is **not** an HTTP-level fake of the Discord API: no wire protocol, no
  sockets. A `sim.World` holds users, bots (token, bot user, application ID), servers,
  roles, channels, overwrites, members, messages, application commands and interactions
  behind one mutex; `World.Connect(token)` returns a `discord.Session` whose gateway is an
  in-memory event queue with one dispatcher goroutine.
- It follows Discord's observable behaviour closely enough that code tested against it
  works against the real API: permissions and role hierarchy, gateway intents and
  message-content gating, position normalization, and the interaction lifecycle with its
  3 s deadline.
- It also plays the humans: `SendMessage`, `InvokeCommand`, `ClickButton`, `Select`,
  `SubmitModal`, `AddReaction` act as a user (with that user's permissions) and emit the
  corresponding gateway events; `Admin*` methods act as the server owner (drift tests);
  `UserClient` returns a full `discord.Client` for any user. Every mutating call is
  recorded in a call log; `WaitIdle` waits for all event cascades.
- Used by Go tests (reconciler integration tests, runtime and API tests) and by
  `cicord dev --sim` (ADR-0017), where the HTTP API exposes it to the admin UI's Simulator
  panel (`/api/v1/sim/*`, [Simulator API](../../../reference/http-api/simulator/)).
- The wire-level behaviour of the real adapter is tested separately against an
  `httptest` fake of the REST API and gateway inside `disgoadapter`'s own tests.
- Plugin unit tests in Node use `@cicord/sdk/testing` (JS fakes of the SDK context); they do
  not go through the simulator.

## Consequences
- Fast, deterministic end-to-end tests in CI without tokens or network.
- The simulator cannot catch bugs in the disgo adapter's encoding/decoding or in disgo
  itself: those are covered by the adapter's own fake-server tests and by manual runs
  against a real test server. The recorded-fixture mode comparing the simulator with real
  Discord, mentioned in the original proposal, is not implemented.
- Every new method on the `discord` interfaces must be implemented twice (adapter and
  simulator); the simulator stays narrow to what Cicord actually calls.
- Simulator IDs are new on every run, so `cicord dev --sim` keeps its state DB in memory.
