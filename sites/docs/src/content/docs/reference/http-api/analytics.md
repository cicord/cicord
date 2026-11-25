---
title: "HTTP API: analytics"
description: Server activity analytics - the dashboard summary with KPIs, rankings and heatmap, time series by hour, day or week with top dimensions, the metric catalog, limits and errors.
---

Part of the [HTTP API reference](../). What is collected and how is described in
[Server analytics](../../../self-hosting/analytics/).

| Method | Path | Result |
|---|---|---|
| GET | `/api/v1/servers/{server}/analytics/summary` | [The dashboard's headline numbers, rankings and heatmap](#summary). |
| GET | `/api/v1/servers/{server}/analytics/series` | [Time series of metrics](#series). |
| GET | `/api/v1/servers/{server}/analytics/catalog` | [Every metric and whether it is collected](#catalog). |

**Access:** moderator or admin of the server (`403 forbidden` for a member), on the
[admin host](../authentication/#admin-host---admin-url) only (`404` elsewhere), like the other
`/api/v1/servers` routes. A script can use the runtime token (bearer, admin).

**Times** are RFC 3339 UTC. Points are `[unixSeconds, value]` pairs, where the time is the
start of the bucket. Buckets are aligned on UTC hours, days (00:00) and ISO weeks (Monday
00:00). Lists and maps are never `null`: an empty server answers `[]` and `{}`.

## Status

Every response carries the collection status of the server:

```json
"status": {
  "enabled": true,               // spec.analytics.enabled
  "presence": false,             // detailed presence collected (opt-in and intent granted)
  "since": "2026-08-01T14:00:00Z", // oldest bucket stored; null before the first point
  "retentionDays": 400,
  "missing": [                   // what cannot be collected now; [] when everything is
    { "metrics": ["moderation.actions"],
      "reason": "the bot lacks the View Audit Log permission",
      "fix": "grant View Audit Log to the bot's role" }
  ]
}
```

A server with analytics disabled still answers `200` with `"enabled": false` and the points
recorded before.

## Labels

Dimensions are returned as stored: channel, role, scheduled event and AutoMod rule IDs, emojis
(a unicode emoji or `name:id`), invite codes, command names, fixed labels. `labels` maps each
dimension value of the response to its display form. The total `""` and `"other"` are never
in it.

```json
"labels": {
  "112233445566778899": { "name": "général", "kind": "channel" },
  "223344556677889900": { "name": "Lounge", "kind": "voice" },
  "334455667788990011": { "name": "Modo", "kind": "role", "color": "#e67e22" },
  "pepe:445566778899001122": { "name": "pepe", "kind": "emoji", "imageUrl": "https://cdn.discordapp.com/emojis/445566778899001122.png" },
  "556677889900112233": { "name": "556677889900112233", "kind": "channel", "deleted": true },
  "<7d": { "name": "<7d", "kind": "label" }
}
```

`kind` is `channel`, `voice`, `thread`, `role`, `emoji`, `event`, `invite` or `label`.
Names come from the runtime's live view of the server, then from the configuration. An ID
that matches nothing is shown as `deleted`, with the ID as its name.

## Summary

`GET …/analytics/summary?period=7d|30d|90d|365d` (default `7d`), or
`GET …/analytics/summary?from=<RFC 3339>&to=<RFC 3339>` for a custom range (not both).

A preset period is made of whole UTC days and ends with today: `from` is 00:00 UTC, `period`
days before tomorrow, and `to` is tomorrow 00:00 UTC. A custom range is widened to whole UTC
hours (`from` rounded down, `to` rounded up), lasts 1 hour to 400 days and starts in the
past; `period` is then `"custom"`. Each value is compared with the **previous
period** of the same length, just before. `previous` is `null` when that period is not
covered: collection started after its first day. The previous period of `365d` reaches
730 days back, so with the default retention of 400 days its `previous` values stay `null`.

```json
{
  "server": "ma-commu",
  "period": "7d",
  "step": "day",
  "from": "2026-11-18T00:00:00Z",
  "to": "2026-11-25T00:00:00Z",
  "kpis": [
    { "metric": "members.total", "value": 1250, "previous": 1198,
      "spark": [[1763424000, 1201], [1763510400, 1210]] },
    { "metric": "members.joined", "value": 64, "previous": 51, "spark": [[1763424000, 9], [1763510400, 12]] }
  ],
  "rankings": {
    "messages.sent": [ { "dim": "112233445566778899", "value": 3121, "previous": 2870 } ],
    "invites.uses": []
  },
  "heatmap": [[0, 0, 3], [1, 0, 0]],
  "labels": { "112233445566778899": { "name": "général", "kind": "channel" } },
  "status": { "enabled": true, "presence": false, "since": "2026-08-01T14:00:00Z", "retentionDays": 400, "missing": [] }
}
```

(The example is abridged. There are always 12 KPIs, the 9 or 10 rankings listed below, and a
7 × 24 heatmap.)

`kpis`, in this order:

| Metric | Value |
|---|---|
| `members.total` | The latest sample of the period (human members) |
| `members.bots` | The latest sample of the period |
| `members.joined`, `members.left` | Sum |
| `members.net` | `members.joined` − `members.left` |
| `members.active` | Average of the daily distinct counts (today counted live) |
| `messages.sent`, `voice.seconds` | Sum over every channel |
| `members.online` | Average of the hourly samples (humans) |
| `reactions.added`, `commands.used`, `moderation.actions` | Sum |

`spark` has one point per `step`: `day`, or `hour` for a custom range shorter than two
days. Counters and `members.net` have every step (`0` without activity); sampled values only
have the steps with data.

`rankings` has the top 10 of `messages.sent` (channels), `voice.seconds` (channels),
`reactions.added` (emojis), `invites.uses` (invites), `commands.used`, `moderation.actions`
(actions), `automod.triggers` (rules), `members.roles.added` (roles) and
`members.joined.age` (account ages), plus `presence.activity` when `status.presence` is true.
Counters are ranked by their sum, gauges by their highest value. Each entry's `previous` is
its value over the previous period (`0` if it did not appear, `null` when not covered).

`heatmap[weekday][hour]` is human messages (`messages.sent`) per weekday and UTC hour over
the period, weekday `0` = Monday.

## Series

`GET …/analytics/series?metrics=a,b&from=…&to=…&step=hour|day|week&dim=…&top=N`

| Parameter | Default | |
|---|---|---|
| `metrics` | **required** | Comma-separated metric names of the [catalog](#catalog), at most 20. |
| `to` | now | RFC 3339. Rounded up to the end of its step. |
| `from` | `to` − 7 days | RFC 3339. Rounded down to the start of its step. |
| `step` | `day` | `hour`, `day` or `week` (ISO weeks, from Monday). |
| `dim` | | One dimension value (a channel ID…). Metrics without a dimension ignore it. |
| `top` | `8` | 1 to 50: how many dimensions a metric with a dimension returns without `dim`. |

**Limits:** at most 400 days between `from` and `to`, and at most 2000 points per series
(`step=hour` covers 83 days at most).

Rollups follow the metric's kind: **counters** are summed per step and every step is present
(`0` without activity). **Gauges** keep the step's last value (`last`) or its highest
(`max`), and only steps with data are present. **Daily distinct** metrics (`members.active*`)
have one value per day, today counted live. With `step=hour` they appear at 00:00, with
`step=week` they are the average of the days.

Without `dim`, a metric with a dimension returns, in this order:

1. its total, `"dim": ""`, over every dimension;
2. its top `top` dimensions over the range, largest first;
3. `"dim": "other"` (total − top), only when there are more dimensions than `top`.

```json
{
  "server": "ma-commu",
  "from": "2026-11-17T00:00:00Z",
  "to": "2026-11-25T00:00:00Z",
  "step": "day",
  "series": [
    { "metric": "messages.sent", "dim": "", "points": [[1763337600, 412], [1763424000, 388]] },
    { "metric": "messages.sent", "dim": "112233445566778899", "points": [[1763337600, 301], [1763424000, 260]] },
    { "metric": "messages.sent", "dim": "other", "points": [[1763337600, 111], [1763424000, 128]] },
    { "metric": "members.total", "dim": "", "points": [[1763337600, 1240], [1763424000, 1250]] }
  ],
  "labels": { "112233445566778899": { "name": "général", "kind": "channel" } },
  "status": { "enabled": true, "presence": false, "since": "2026-08-01T14:00:00Z", "retentionDays": 400, "missing": [] }
}
```

`from` and `to` in the response are the aligned bounds.

## Catalog

`GET …/analytics/catalog` lists every metric, in dashboard order:

```json
{
  "metrics": [
    { "name": "members.total", "kind": "last", "unit": "members",
      "source": "Discord's approximate member count, sampled", "collected": true },
    { "name": "messages.sent", "kind": "counter", "dim": "channel", "unit": "count",
      "source": "message events (members, not bots)", "collected": true },
    { "name": "presence.status", "kind": "last", "dim": "status", "unit": "members",
      "source": "presence events (privileged intent)", "presence": true, "collected": false }
  ],
  "status": { "enabled": true, "presence": false, "since": null, "retentionDays": 400, "missing": [] }
}
```

- `kind` is `counter`, `max` or `last`. The daily distinct metrics are `last`.
- `dim` names what the dimension holds: `channel`, `role`, `emoji`, `event`, `invite`,
  `rule`, `command`, `action`, `account age`, `status`, `platform`, `activity`. It is absent
  for metrics without one.
- `unit` is `count`, `members`, `seconds` or `level`.
- `collected` is false when analytics are disabled, when the metric needs presence and
  presence is not collected, or when `status.missing` lists it.

## Errors

| Status | Code | When |
|---|---|---|
| 400 | `invalid` | Unknown or missing metric, bad `step`, `period`, `top`, `from` or `to`, `from` not before `to`, more than 400 days, more than 2000 points per series. `message` says which. |
| 401 | `unauthenticated` | Not signed in. |
| 403 | `forbidden` | Not a moderator or admin of the server. |
| 404 | `not_found` | Unknown server, or a host other than the admin host. |
| 404 | `analytics_disabled` | The runtime has no analytics store. |
| 503 | `unavailable` | No active revision yet. |

```json
{ "error": { "code": "invalid", "message": "2400 points per series with step=hour, at most 2000: use a larger step or a shorter range" } }
```
