-- 0002_analytics: server activity analytics (state.Analytics).
--
-- analytics_points holds hourly aggregates: one row per (server, metric, dim,
-- bucket). bucket is the start of the UTC hour in unix SECONDS (not nanoseconds
-- like the other times: buckets are always whole hours and the API exposes them
-- in seconds). kind is the merge rule the row was last written with (counter,
-- max, last); Dims uses it to sum counters and take the max of gauges.
CREATE TABLE analytics_points (
    server TEXT    NOT NULL,
    metric TEXT    NOT NULL,
    dim    TEXT    NOT NULL,
    bucket INTEGER NOT NULL,
    kind   TEXT    NOT NULL,
    value  REAL    NOT NULL,
    PRIMARY KEY (server, metric, dim, bucket)
) WITHOUT ROWID;

-- Range reads of every dimension of a metric (Query without Dim, Dims, Totals).
CREATE INDEX analytics_points_range ON analytics_points (server, metric, bucket);
-- Retention of one server (Purge) and its oldest bucket (Since).
CREATE INDEX analytics_points_server_age ON analytics_points (server, bucket);
-- Retention of every server (Purge with server "").
CREATE INDEX analytics_points_age ON analytics_points (bucket);

-- analytics_active holds the opaque daily tokens of distinct active members
-- (an HMAC of the member ID with a key of the day). day is "YYYY-MM-DD" (UTC),
-- so string order is date order; rows are deleted once the day is rolled up.
CREATE TABLE analytics_active (
    server    TEXT NOT NULL,
    set_name  TEXT NOT NULL,
    day       TEXT NOT NULL,
    token     TEXT NOT NULL,
    PRIMARY KEY (server, set_name, day, token)
) WITHOUT ROWID;

CREATE INDEX analytics_active_day ON analytics_active (day);
