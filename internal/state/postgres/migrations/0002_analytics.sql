-- 0002_analytics: server activity analytics (state.Analytics), PostgreSQL.
--
-- Same model as the SQLite schema:
--   * analytics_points holds hourly aggregates, one row per (server, metric,
--     dim, bucket); bucket is the start of the UTC hour in unix SECONDS (buckets
--     are whole hours and the API exposes them in seconds);
--   * kind is the merge rule the row was last written with (counter, max,
--     last): Dims sums counters and takes the max of gauges;
--   * TEXT columns use COLLATE "C" (byte order, like SQLite);
--   * analytics_active holds the opaque daily tokens of distinct active
--     members; day is "YYYY-MM-DD" (UTC), so string order is date order.
CREATE TABLE analytics_points (
    server TEXT COLLATE "C" NOT NULL,
    metric TEXT COLLATE "C" NOT NULL,
    dim    TEXT COLLATE "C" NOT NULL,
    bucket BIGINT           NOT NULL,
    kind   TEXT COLLATE "C" NOT NULL,
    value  DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (server, metric, dim, bucket)
);

-- Range reads of every dimension of a metric (Query without Dim, Dims, Totals).
CREATE INDEX analytics_points_range ON analytics_points (server, metric, bucket);
-- Retention of one server (Purge) and its oldest bucket (Since).
CREATE INDEX analytics_points_server_age ON analytics_points (server, bucket);
-- Retention of every server (Purge with server "").
CREATE INDEX analytics_points_age ON analytics_points (bucket);

CREATE TABLE analytics_active (
    server   TEXT COLLATE "C" NOT NULL,
    set_name TEXT COLLATE "C" NOT NULL,
    day      TEXT COLLATE "C" NOT NULL,
    token    TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (server, set_name, day, token)
);

CREATE INDEX analytics_active_day ON analytics_active (day);
