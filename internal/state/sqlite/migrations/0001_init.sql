-- 0001_init: initial CODI runtime state schema.
-- Times are INTEGER unix nanoseconds (UTC). JSON payloads are TEXT, NULL when absent.

CREATE TABLE resources (
    server     TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    id         TEXT    NOT NULL,
    owner      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (server, kind, key)
) WITHOUT ROWID;

CREATE INDEX resources_by_id ON resources (server, id);

CREATE TABLE kv (
    scope      TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    expires_at INTEGER,
    PRIMARY KEY (scope, key)
) WITHOUT ROWID;

CREATE INDEX kv_expiry ON kv (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE tasks (
    id      TEXT    NOT NULL PRIMARY KEY,
    scope   TEXT    NOT NULL,
    name    TEXT    NOT NULL,
    payload TEXT,
    run_at  INTEGER NOT NULL
) WITHOUT ROWID;

CREATE INDEX tasks_due ON tasks (run_at, id);
CREATE INDEX tasks_by_scope ON tasks (scope);

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    time    INTEGER NOT NULL,
    server  TEXT    NOT NULL,
    actor   TEXT    NOT NULL,
    action  TEXT    NOT NULL,
    target  TEXT    NOT NULL,
    details TEXT
);

CREATE INDEX audit_by_server ON audit_log (server, id);

CREATE TABLE sync_log (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    time     INTEGER NOT NULL,
    revision TEXT    NOT NULL,
    target   TEXT    NOT NULL,
    status   TEXT    NOT NULL,
    message  TEXT    NOT NULL,
    details  TEXT
);

CREATE INDEX sync_by_target ON sync_log (target, id);

CREATE TABLE sessions (
    id         TEXT    NOT NULL PRIMARY KEY,
    user_id    TEXT    NOT NULL,
    data       TEXT,
    expires_at INTEGER NOT NULL
) WITHOUT ROWID;

CREATE INDEX sessions_expiry ON sessions (expires_at);
