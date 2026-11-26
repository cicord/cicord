---
title: "HTTP API: simulator"
description: The /api/v1/sim endpoints of cicord dev --sim - the simulated world, users, joins, boosts, messages, DMs, slash commands, components, modals and the Server-Sent Events stream.
---

Part of the [HTTP API reference](../). These endpoints exist with `cicord dev --sim` only (see
[Try Cicord offline with the simulator](../../../getting-started/quick-start/#try-cicord-offline-with-the-simulator)).

They let the admin console's **Simulator** panel and tests play the role of Discord users.
They need no authentication (dev only, behind the [dev-mode Host check](../#dev-mode-host-check))
but follow the [CSRF rule](../authentication/#csrf). Without the simulator they answer `404`
("the simulator is not enabled").

`cicord dev --sim` seeds the world from the repository: every server (its `discordId`, named
after `profile.name` or its key) owned by a user `owner`, every bot (its `applicationId`,
named after `profile.username` or its key) invited with `Administrator`, and the users
`alice`, `bob` and `charlie`, who are not members.

## Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/sim/state` | `{"users": [User], "bots": [User], "servers": [SimServer]}` |
| POST | `/api/v1/sim/users` | `{"username"}` (1–32 characters): `201 User`. |
| POST | `/api/v1/sim/servers/{id}/members` | `{"userId"}`: the user joins the server (`memberJoin`). `204`. |
| DELETE | `/api/v1/sim/servers/{id}/members/{userId}` | The user leaves. `204`. |
| POST | `/api/v1/sim/servers/{id}/boosts` | `{"count": n}`: sets the number of boosts (2, 7 and 14 boosts give levels 1, 2 and 3, and the features they unlock). Returns `{"id", "boostLevel", "boostCount", "features"}`. Seeding also honors the server annotations `sim.cicord.dev/boosts: "<n>"` and `sim.cicord.dev/features: "A,B"`. |
| GET | `/api/v1/sim/channels/{id}/messages?limit=50` | `{"items": [Message]}`: the last `limit` (≤ 500) messages, oldest first. |
| GET | `/api/v1/sim/users/{id}/dms?limit=50` | `{"items": [Message]}`: DMs received by the user from bots, oldest first. |
| POST | `/api/v1/sim/messages` | `{"userId", "channelId", "content"}`: `201 Message`. |
| POST | `/api/v1/sim/commands` | `{"userId", "serverId", "channelId", "bot", "name", "options": {}, "autocomplete"?: {"option", "value"}}`: `InteractionResult` ([below](#slash-commands)). |
| POST | `/api/v1/sim/poll-votes` | `{"userId", "channelId", "messageId", "answerId", "voted"}` casts (`true`) or removes (`false`) a native poll vote. Returns `204`. Read the message’s `poll.answers[].votes` for the totals. |
| POST | `/api/v1/sim/channels/{id}/permissions` | Replaces the channel overwrites as the simulated operator. Pass `{"overwrites": [{"id", "type", "allow", "deny"}]}` with role or member IDs and arrays of permission names. Returns `204`. |
| POST | `/api/v1/sim/components` | `{"userId", "channelId"?, "messageId", "customId", "values": []}`: `InteractionResult` (a select when `customId` is a select menu of the message, a button click otherwise). |
| POST | `/api/v1/sim/modals` | `{"userId", "serverId", "channelId", "bot", "customId", "fields": {"<input id>": "value"}}`: `InteractionResult` (submits a modal shown by a previous interaction). |
| GET | `/api/v1/sim/commands?serverId=` | `{"items": [{"bot", "name", "description", "options": [...]}]}`: the registered commands, of every simulated server by default. |
| GET | `/api/v1/sim/events` | Server-Sent Events: `event: change` whenever the simulated world changes (a UI refresh hint). |

A user posts in a channel (`X-Cicord-Request` is the [CSRF](../authentication/#csrf) header):

```sh
curl -X POST http://127.0.0.1:7420/api/v1/sim/messages -H 'X-Cicord-Request: 1' \
  -d '{"userId": "1575125353398009859", "channelId": "1575125354564026388", "content": "Hello everyone!"}'
```

```json
{ "id": "1575126926903738368", "channelId": "1575125354564026388", "serverId": "223344556677889900",
  "author": { "id": "1575125353398009859", "username": "alice", "displayName": "alice", "avatarUrl": "" },
  "content": "Hello everyone!", "embeds": [], "components": [], "ephemeral": false,
  "createdAt": "2026-11-25T13:32:03.817611+01:00" }
```

- `bot` is a bot key (an application ID or a bot user ID is accepted too); unknown: `404`. In
  command listings `bot` is the bot key, or the application ID for bots outside the
  repository.
- Simulated Discord errors map to `404 not_found`, `403 forbidden` and `400 invalid` (unknown
  user or server, missing permission…). An unknown channel or user in the message and DM
  listings answers `404`, and so does an unknown `messageId` (or one not in `channelId`).

### Slash commands

- `name` is the command path: `"poll"`, `"polls close"`, `"polls admin purge"` (a subcommand
  of a group).
- With `autocomplete`, the user types `value` in the autocomplete option `option` instead of
  running the command (`options` are the other options already filled in); the result carries
  the bot's `choices`.
- Command options: `{"name", "description", "type", "required", "choices"?: [{"name", "value"}], "minValue"?, "maxValue"?, "minLength"?, "maxLength"?, "autocomplete"?, "options"?: [...]}`.
  `type` is one of `subcommand`, `subcommandGroup`, `string`, `integer`, `boolean`, `user`,
  `channel`, `role`, `mentionable`, `number` or `attachment`. A command holds either plain
  options or subcommands and groups; `options` lists the options of a subcommand (plain
  options) or of a group (subcommands). `autocomplete: true` marks an option whose values the
  bot suggests while the user types.

### Interaction results

- Validation errors of an interaction (unknown command, missing option, disabled button…) come
  back as `200` with `status: "failed"` and `error` set; so does a bot that never acknowledges
  (`"the application did not respond"`).
- `status: "modal"` carries `modal: {"customId", "title", "components"}` (rows of `textInput`).
- `deferred` means the bot deferred (or had not answered yet) and had not followed up after 5
  seconds; the UI refreshes on SSE events.

### Server-Sent Events

`Content-Type: text/event-stream`; events are `id: <n>`, `event: change`, `data: {}`. An
initial `change` event is sent, then bursts are coalesced (at most one event per 100 ms), with
a `: ping` comment every 25 seconds. The stream ends when the runtime shuts down.

## Shapes

```json
// SimServer
{ "id": "…", "key"?: "my-community", "name": "…", "ownerId": "…",
  "channels": [ { "id", "name", "type", "parentId"?, "position" } ],
  "roles": [ { "id", "name", "color", "position", "managed" } ],
  "members": [ { "user", "nick", "roles": ["<role id>"] } ] }
```

Only repository servers present in the world are listed; `key` is the repository key; `roles`
include `@everyone` (its ID is the server ID); `managed` marks integration roles.

```json
// Message
{ "id": "…", "channelId": "…", "serverId": "…", "author": User, "content": "…", "embeds": [...],
  "components": [ { "type": "row", "components": [ { "type": "button", "customId": "c:welcome:go", "label": "Go", "style": "primary" } ] } ],
  "ephemeral": false, "createdAt": "…", "editedAt": "…" }
// serverId is omitted for DMs, editedAt until the message is edited.
// Components omit empty fields. Types: row | button | select | textInput.
// button: {"type":"button","customId"?,"label"?,"style","emoji"?,"url"? (link buttons),"disabled"?}
//   style: primary | secondary | success | danger | link
// select: {"type":"select","customId","placeholder"?,"minValues"?,"maxValues"?,"disabled"?,"options":[{"label","value","description"?,"emoji"?,"default"?}]}
// textInput (modals): {"type":"textInput","customId","label","style":"short"|"paragraph","required"?,"value"?,"placeholder"?}
// embeds: [{"title"?,"description"?,"url"?,"color"?:"#rrggbb","timestamp"?,"fields"?:[{"name","value","inline"}],
//           "author"?:{"name","url","iconUrl"},"footer"?:{"text","iconUrl"},"imageUrl"?,"thumbnailUrl"?}]

// InteractionResult: waits up to 5 s for the bot's answer
{ "status": "replied",                // replied | deferred | failed | modal
  "response": Message | null, "followups": [Message], "modal"?: {...},
  "choices"?: [{ "name", "value" }],  // autocomplete results (status "replied")
  "error": "" }
```
