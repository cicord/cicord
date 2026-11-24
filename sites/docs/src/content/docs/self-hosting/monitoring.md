---
title: Monitoring
description: Scrape the runtime's Prometheus metrics (revisions, syncs, bots, plugin instances, HTTP, webhooks, Go and process) from a dedicated loopback listener, with a scrape configuration and alert examples.
---

`cicord serve` can expose [Prometheus](https://prometheus.io/) metrics: the active revision,
the last sync of each server, the changes applied to Discord, the gateway connection of each
bot, the plugin instances (running, crashes, handler errors and durations), the HTTP API,
webhook deliveries, and the Go runtime and process. They are **off by default**.

## Enable the metrics

Give `cicord serve` an address for its metrics listener, with `--metrics-listen` or
`CICORD_METRICS_LISTEN`:

```sh
cicord serve --metrics-listen 127.0.0.1:9420
```

The runtime then answers `GET http://127.0.0.1:9420/metrics` in the Prometheus text format
(`text/plain; version=0.0.4`). The listener serves nothing else: every other path answers
`404`, and every method but `GET` and `HEAD` answers `405`. If the address cannot be bound,
`cicord serve` does not start.

Only `cicord serve` has metrics, once a repository is configured: neither the setup wizard nor
`cicord dev` serve them.

## Security

The metrics listener has **no authentication**: the network is the boundary. Bind it to a
loopback address, so that only a Prometheus (or an agent) on the same machine reaches it. When
it is bound to another address, the runtime logs a warning at startup.

The metrics hold no secret and no Discord user data: their labels are the keys of your
configuration (servers, bots, plugin instances), plugin names, route patterns and fixed values,
never user IDs, message content or request paths. They still describe your setup and its
activity, so keep them private.

The HTTP API listener (`--listen`, port 7420) never serves the metrics: `/metrics` answers
`404` there on every host, with or without the runtime token. A separate listener keeps them
away from the public reverse proxy, does not depend on the `Host` header of the admin URL (a
scraper on the server would send `127.0.0.1:7420`), and does not need the runtime token,
which also drives the machine endpoints.

## Docker and Compose

Inside a container, `127.0.0.1` is the container's own loopback: Docker cannot forward a port
to it. Listen on every interface of the container and publish the port on the **host's**
loopback only, the way the [VPS install](../production-vps/) publishes port 7420:

```yaml
# compose.yaml (service cicord)
services:
  cicord:
    environment:
      CICORD_METRICS_LISTEN: ":9420"
    ports:
      - "127.0.0.1:7420:7420"
      - "127.0.0.1:9420:9420" # metrics: the host's loopback only
```

With `docker run`, add `-e CICORD_METRICS_LISTEN=:9420 -p 127.0.0.1:9420:9420`. The runtime logs
the non-loopback warning: here the published port is the boundary. Every container on the same
Compose network can also reach `cicord:9420`; a Prometheus container on that network can
scrape it there, without publishing the port at all. Caddy only proxies port 7420.

## Scrape configuration

```yaml
# prometheus.yml
scrape_configs:
  - job_name: cicord
    scrape_interval: 30s
    static_configs:
      - targets: ["127.0.0.1:9420"] # or cicord:9420 from the Compose network
```

Prometheus adds its own `job` and `instance` labels to every series: that is why the plugin
instance label is `plugin_instance`.

## Metrics

All `cicord_*` series of a server, bot or plugin instance use the keys of the active revision
(`servers/<server>.yaml`, `bots/<bot>/bot.yaml`, the instance `name`). Gauges list every
configured server, bot and instance, with `0` when it is missing, disconnected or stopped, so
that you can alert on them. Counters start at zero with the process and keep the series of an
instance removed from the configuration until the next restart.

### Build, revision and source

| Metric | Type | Labels | Description |
|---|---|---|---|
| `cicord_build_info` | gauge | `version`, `commit`, `goversion` | Always `1`: the build of the running binary. |
| `cicord_revision_active` | gauge | | `1` when a revision is active, `0` before the first valid one. |
| `cicord_revision_info` | gauge | `revision`, `source` | Always `1`: the short ID of the active revision and its source (`git`, `local`). |
| `cicord_revision_activated_timestamp_seconds` | gauge | | When the active revision was activated (Unix time). |
| `cicord_revision_activations_total` | counter | `result` | Revisions evaluated: `activated`, or `rejected` (invalid configuration, plugin build failure; the previous revision keeps running). |
| `cicord_source_fetch_errors_total` | counter | | Failed fetches of the source (git fetch, local snapshot). |

### Servers and reconciliation

| Metric | Type | Labels | Description |
|---|---|---|---|
| `cicord_server_last_sync_timestamp_seconds` | gauge | `server` | When the server was last fully reconciled (activation, periodic resync, sync button), Unix time; `0`: never. |
| `cicord_server_last_sync_success` | gauge | `server` | `1` when that last full reconciliation succeeded. |
| `cicord_server_out_of_sync` | gauge | `server` | `1` when Discord drifted from the repository and the drift is not corrected yet. |
| `cicord_server_health` | gauge | `server`, `health` | State set: `1` for the current health (`Healthy`, `Progressing`, `Degraded`, `Missing`), `0` for the others. |
| `cicord_reconcile_syncs_total` | counter | `server`, `result` | Full reconciliations: `succeeded` or `failed`. |
| `cicord_reconcile_changes_applied_total` | counter | `server`, `trigger` | Changes applied to Discord, by `trigger`: `sync` (full reconciliation) or `self_heal` (drift correction). |
| `cicord_reconcile_changes_failed_total` | counter | `server`, `trigger` | Changes Discord refused, by `trigger`. |

### Bots and plugin instances

| Metric | Type | Labels | Description |
|---|---|---|---|
| `cicord_bot_gateway_connected` | gauge | `bot` | `1` when the bot's Discord gateway connection is up (`0` while it reconnects, or when the bot could not connect). |
| `cicord_plugin_instance_running` | gauge | `bot`, `server`, `plugin`, `plugin_instance` | `1` when the instance runs; `0` when it failed to start, crashed or waits for its restart. |
| `cicord_plugin_instance_crashes_total` | counter | `bot`, `server`, `plugin`, `plugin_instance` | Crashes: memory limit, hard stop, engine trap. |
| `cicord_plugin_instance_restarts_total` | counter | `bot`, `server`, `plugin`, `plugin_instance`, `result` | Restarts after a crash: `succeeded` or `failed`. |
| `cicord_plugin_handler_errors_total` | counter | `bot`, `server`, `plugin`, `plugin_instance`, `kind` | Handler calls that failed: an exception, the handler timeout, the sync budget, a crash, or a full queue. |
| `cicord_plugin_handler_duration_seconds` | histogram | `bot`, `server`, `plugin`, `plugin_instance`, `kind` | Duration of handler calls, from the dispatch (queue wait included) to the result. Buckets from 1 ms to 10 s. Calls refused before reaching the queue are not observed. |

`kind` is the handler kind: `start`, `stop`, `event`, `command`, `autocomplete`, `component`,
`modal`, `route`, `schedule`, `task` or `subscription`. Events the plugin has no handler for,
and calls to an instance that is stopping, are not counted.

### HTTP API and webhooks

| Metric | Type | Labels | Description |
|---|---|---|---|
| `cicord_http_requests_total` | counter | `route`, `status` | Requests to the API listener, by route pattern (`GET /api/v1/servers/{server}`, `/` for the portal; `unmatched` when the request was answered before routing: CORS preflight, CSRF, admin host) and status class (`2xx`, `3xx`, `4xx`, `5xx`). |
| `cicord_webhook_deliveries_total` | counter | `result` | GitHub webhook deliveries: `accepted` (a push on the tracked branch, a fetch is triggered), `ignored` (ping, another branch, another event), `rejected` (bad signature, payload too large or invalid) or `disabled` (no `CICORD_WEBHOOK_SECRET`). |

### Go runtime and process

| Metric | Type | Description |
|---|---|---|
| `go_info` | gauge | Always `1`, with the Go `version` label. |
| `go_goroutines` | gauge | Goroutines that currently exist. |
| `go_gomaxprocs` | gauge | `GOMAXPROCS`. |
| `go_memstats_heap_alloc_bytes` | gauge | Bytes of allocated heap objects. |
| `go_memstats_sys_bytes` | gauge | Memory obtained from the OS by the Go runtime. |
| `go_gc_cycles_total` | counter | Completed GC cycles. |
| `process_start_time_seconds` | gauge | Start time of the process (Unix time). |
| `process_cpu_seconds_total` | counter | User and system CPU time (Linux and macOS). |
| `process_resident_memory_bytes` | gauge | Resident memory (Linux). |
| `process_open_fds` | gauge | Open file descriptors (Linux). |

Plugin memory is part of the process: each instance's heap is capped by its memory limit, so
`process_resident_memory_bytes` is the figure to compare with the container's memory limit.

## Alert examples

```yaml
# rules.yml
groups:
  - name: cicord
    rules:
      - alert: CicordDown
        expr: up{job="cicord"} == 0
        for: 5m
      - alert: CicordNoActiveRevision
        expr: cicord_revision_active == 0
        for: 10m
      - alert: CicordRevisionRejected
        expr: increase(cicord_revision_activations_total{result="rejected"}[15m]) > 0
      - alert: CicordSourceFetchFailing
        expr: increase(cicord_source_fetch_errors_total[30m]) >= 3
      - alert: CicordServerSyncFailed
        expr: cicord_server_last_sync_success == 0
        for: 15m
      - alert: CicordServerNotSynced
        # the periodic resync runs every 5 minutes
        expr: time() - cicord_server_last_sync_timestamp_seconds > 3600
      - alert: CicordBotDisconnected
        expr: cicord_bot_gateway_connected == 0
        for: 5m
      - alert: CicordPluginDown
        expr: cicord_plugin_instance_running == 0
        for: 10m
      - alert: CicordPluginCrashLoop
        expr: increase(cicord_plugin_instance_crashes_total[30m]) >= 3
      - alert: CicordPluginErrors
        expr: sum by (plugin, plugin_instance, kind) (rate(cicord_plugin_handler_errors_total[10m])) > 0.1
        for: 10m
```

The 95th percentile of handler durations, per plugin and kind:

```text
histogram_quantile(0.95, sum by (plugin, kind, le) (rate(cicord_plugin_handler_duration_seconds_bucket[5m])))
```
