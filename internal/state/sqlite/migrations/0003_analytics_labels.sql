-- 0003_analytics_labels: remembered labels of analytics dimensions
-- (state.AnalyticsLabels).
--
-- Analytics store dimensions as Discord IDs (channels, roles, scheduled events);
-- analytics_labels keeps the last name the collector saw for each, so a deleted
-- channel or role keeps its name in the dashboard. owner is
-- "plugin:<bot>/<instance>" for the channels a plugin created ('' otherwise): it
-- is kept once set, and the API groups those channels per plugin instance.
-- deleted is 0 or 1. seen_at is the last time the collector saw the resource, in
-- unix nanoseconds like the other times; labels not seen within the analytics
-- retention, or whose dimension has no point left, are pruned daily.
CREATE TABLE analytics_labels (
    server  TEXT    NOT NULL,
    dim     TEXT    NOT NULL,
    id      TEXT    NOT NULL,
    name    TEXT    NOT NULL,
    type    TEXT    NOT NULL,
    color   TEXT    NOT NULL DEFAULT '',
    owner   TEXT    NOT NULL DEFAULT '',
    deleted INTEGER NOT NULL DEFAULT 0,
    seen_at INTEGER NOT NULL,
    PRIMARY KEY (server, dim, id)
) WITHOUT ROWID;

-- Retention of one server (PruneLabels).
CREATE INDEX analytics_labels_seen ON analytics_labels (server, seen_at);
