---
title: "HTTP API: live events"
description: The Server-Sent Events stream that keeps the admin console live - the hello event with the runtime version, change hints per topic and server, the stopping event, keep-alives, access and limits.
---

Part of the [HTTP API reference](../). The admin console uses this stream to refresh what it
shows as soon as something changes on the runtime, and to notice an update.

| Method | Path | Role | Result |
|---|---|---|---|
| GET | `/api/v1/events` | moderator | A `text/event-stream` of change hints. |

It is an admin route: on a runtime with an [admin URL](../../cli/), it only answers on the
admin host. Send the session cookie (or a bearer token) as for the other admin endpoints. A
signed-in user who moderates no server gets `403 forbidden`.

## Events

Each event has an `id`, a name and a JSON `data` line. Events carry **no content**: they
tell what changed, and the client refetches it through the usual endpoints, with their own
access checks.

| Event | Data | Meaning |
|---|---|---|
| `hello` | `{"version": "0.1.4", "revision": "53ca230…"}` | Sent first, on every connection. A client that sees another `version` than the one it loaded runs outdated code: the admin console offers to reload. |
| `status` | `{}` | The runtime status changed: revision, bot connections, last error ([`GET /api/v1/status`](servers/#runtime-status)). |
| `sync` | `{"server": "ma-commu"}` | A sync was recorded: the history, and the server's sync state, resources and drift. Without `server`: a revision (activated or rejected). |
| `plugins` | `{"server": "ma-commu"}` | A plugin instance of the server crashed or restarted. |
| `analytics` | `{"server": "ma-commu"}` | New [analytics](analytics/) points were written for the server (at most every 30 seconds while there is activity). |
| `stopping` | `{}` | The runtime is shutting down (a restart or an update): reconnect. |

```text
id: 1
event: hello
data: {"version":"0.1.4","revision":"53ca230"}

id: 2
event: sync
data: {"server":"ma-commu"}
```

- Server events (`sync`, `plugins`, `analytics` with a `server`) are only sent for the
  servers you moderate or administer, as computed when the stream opens.
- Changes are coalesced: within 250 ms, each event name and server is sent once.
- A comment line (`: ping`) is sent every 25 seconds. A client can treat 60 seconds without
  anything as a dead connection (a proxy can keep its side open after the runtime went
  away): `@cicord/client` does it with `idleTimeoutMs`.
- The stream ends after 10 minutes: reconnect, which checks the session and the access
  again. Events missed while disconnected are not replayed (`Last-Event-ID` is ignored):
  refetch what you show after a reconnection, as the admin console does on `hello`.

## In @cicord/client

```ts
const stream = client.admin.subscribe((event) => {
  if (event.type === "hello") console.log("runtime", event.version);
  if (event.type === "analytics") refreshCharts(event.server);
}, { idleTimeoutMs: 60_000 });
// later
stream.close();
```

Without the stream (a proxy that buffers responses), the admin console refreshes the status
every 30 seconds and everything when the tab comes back after a minute or the network
returns.
