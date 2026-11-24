---
title: "ADR-0009 — \"Server\", never \"guild\""
description: "ADR-0009: \"Server\", never \"guild\", outside the Discord adapter."
sidebar:
  label: '0009 · "Server", never "guild"'
  order: 9
---

**Status:** Accepted

## Context
The Discord API calls servers *guilds*; users and the Discord client call them *servers*.

## Decision
- All user-facing surfaces say **server**: `kind: Server`, `servers/` directory,
  `spec.servers` in bots, SDK types (`Server`, `ctx.server`), permission and intent names
  (`ManageServer`, `ServerMembers`).
- *Guild* only appears inside the Discord adapter (`internal/discord`), which maps names.

## Consequences
- The adapter carries a mapping table for permissions and intents (see [permission names](../../../configuration/server/#permission-names) and
  [gateway intents](../../../configuration/bot/#gateway-intents)).
- Contributors reading Discord docs need to translate; the mapping table is the reference.
