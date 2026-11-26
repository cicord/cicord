-- A cicord v0.1.11 state database, dumped after the v0.1.11 store wrote:
-- three overdue legacy tasks and one future task (scope plugin:moderator/moderation/my-community),
-- five plugin KV keys, an enforcement pause, a managed role and an audit entry.
-- Store clock 2025-06-01T12:00:00Z. Used by the upgrade tests; do not edit by hand.
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	);
INSERT INTO schema_migrations VALUES(1,'0001_init.sql',1748779200000000000);
INSERT INTO schema_migrations VALUES(2,'0002_analytics.sql',1748779200000000000);
INSERT INTO schema_migrations VALUES(3,'0003_analytics_labels.sql',1748779200000000000);
CREATE TABLE resources (
    server     TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    id         TEXT    NOT NULL,
    owner      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (server, kind, key)
) WITHOUT ROWID;
INSERT INTO resources VALUES('my-community','role','mod','42','git',1748775600000000000);
CREATE TABLE kv (
    scope      TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    expires_at INTEGER,
    PRIMARY KEY (scope, key)
) WITHOUT ROWID;
INSERT INTO kv VALUES('enforcement-pause','my-community','{"by":"x"}',NULL);
INSERT INTO kv VALUES('plugin:moderator/moderation/my-community','k0','1',NULL);
INSERT INTO kv VALUES('plugin:moderator/moderation/my-community','k1','1',NULL);
INSERT INTO kv VALUES('plugin:moderator/moderation/my-community','k2','1',NULL);
INSERT INTO kv VALUES('plugin:moderator/moderation/my-community','k3','1',NULL);
INSERT INTO kv VALUES('plugin:moderator/moderation/my-community','k4','1',NULL);
CREATE TABLE tasks (
    id      TEXT    NOT NULL PRIMARY KEY,
    scope   TEXT    NOT NULL,
    name    TEXT    NOT NULL,
    payload TEXT,
    run_at  INTEGER NOT NULL
) WITHOUT ROWID;
INSERT INTO tasks VALUES('future','plugin:moderator/moderation/my-community','unban',NULL,7258118400000000000);
INSERT INTO tasks VALUES('legacy-0','plugin:moderator/moderation/my-community','unban','{"userId":"1"}',1748775600000000000);
INSERT INTO tasks VALUES('legacy-1','plugin:moderator/moderation/my-community','unban','{"userId":"1"}',1748775601000000000);
INSERT INTO tasks VALUES('legacy-2','plugin:moderator/moderation/my-community','unban','{"userId":"1"}',1748775602000000000);
CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    time    INTEGER NOT NULL,
    server  TEXT    NOT NULL,
    actor   TEXT    NOT NULL,
    action  TEXT    NOT NULL,
    target  TEXT    NOT NULL,
    details TEXT
);
INSERT INTO audit_log VALUES(1,1748779200000000000,'my-community','cicord','sync.apply','',NULL);
CREATE TABLE sync_log (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    time     INTEGER NOT NULL,
    revision TEXT    NOT NULL,
    target   TEXT    NOT NULL,
    status   TEXT    NOT NULL,
    message  TEXT    NOT NULL,
    details  TEXT
);
CREATE TABLE sessions (
    id         TEXT    NOT NULL PRIMARY KEY,
    user_id    TEXT    NOT NULL,
    data       TEXT,
    expires_at INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE analytics_points (
    server TEXT    NOT NULL,
    metric TEXT    NOT NULL,
    dim    TEXT    NOT NULL,
    bucket INTEGER NOT NULL,
    kind   TEXT    NOT NULL,
    value  REAL    NOT NULL,
    PRIMARY KEY (server, metric, dim, bucket)
) WITHOUT ROWID;
CREATE TABLE analytics_active (
    server    TEXT NOT NULL,
    set_name  TEXT NOT NULL,
    day       TEXT NOT NULL,
    token     TEXT NOT NULL,
    PRIMARY KEY (server, set_name, day, token)
) WITHOUT ROWID;
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
INSERT INTO sqlite_sequence VALUES('audit_log',1);
CREATE INDEX resources_by_id ON resources (server, id);
CREATE INDEX kv_expiry ON kv (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX tasks_due ON tasks (run_at, id);
CREATE INDEX tasks_by_scope ON tasks (scope);
CREATE INDEX audit_by_server ON audit_log (server, id);
CREATE INDEX sync_by_target ON sync_log (target, id);
CREATE INDEX sessions_expiry ON sessions (expires_at);
CREATE INDEX analytics_points_range ON analytics_points (server, metric, bucket);
CREATE INDEX analytics_points_server_age ON analytics_points (server, bucket);
CREATE INDEX analytics_points_age ON analytics_points (bucket);
CREATE INDEX analytics_active_day ON analytics_active (day);
CREATE INDEX analytics_labels_seen ON analytics_labels (server, seen_at);
COMMIT;
