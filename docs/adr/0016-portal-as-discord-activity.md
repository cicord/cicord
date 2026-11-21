# ADR-0016 — Community portal as a Discord Activity

**Status:** Proposed — requires verification of current Discord constraints.

## Context
Sending a new member to an external website for onboarding adds friction. Discord's
Embedded App SDK lets web apps run inside the Discord client as "Activities".

## Proposal
- Make the portal SDK compatible with the Embedded App SDK so the same React portal can run
  either on the web or inside Discord.
- Keep the external signed link (`ctx.portal.link()`) as the default and fallback.

## To verify
- Where Activities can be launched today (voice channels, text channels, DMs) and from
  which interactions a bot can open one.
- App verification / approval requirements and any limits for unverified apps.
- Authentication flow differences (Embedded App SDK `authorize` vs. web OAuth2).
