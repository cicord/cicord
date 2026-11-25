-- 0003_analytics_labels: remembered labels of analytics dimensions
-- (state.AnalyticsLabels), PostgreSQL.
--
-- Same model as the SQLite schema: the last name the collector saw for each
-- dimension that is a Discord ID (channels, roles, scheduled events), so a
-- deleted channel or role keeps its name in the dashboard; owner is
-- "plugin:<bot>/<instance>" for the channels a plugin created ('' otherwise),
-- kept once set; seen_at is unix nanoseconds; TEXT columns use COLLATE "C".
CREATE TABLE analytics_labels (
    server  TEXT COLLATE "C" NOT NULL,
    dim     TEXT COLLATE "C" NOT NULL,
    id      TEXT COLLATE "C" NOT NULL,
    name    TEXT             NOT NULL,
    type    TEXT COLLATE "C" NOT NULL,
    color   TEXT COLLATE "C" NOT NULL DEFAULT '',
    owner   TEXT COLLATE "C" NOT NULL DEFAULT '',
    deleted BOOLEAN          NOT NULL DEFAULT FALSE,
    seen_at BIGINT           NOT NULL,
    PRIMARY KEY (server, dim, id)
);

-- Retention of one server (PruneLabels).
CREATE INDEX analytics_labels_seen ON analytics_labels (server, seen_at);
