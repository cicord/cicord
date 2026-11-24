-- 0001_init: initial Cicord runtime state schema (PostgreSQL).
--
-- Same model as the SQLite schema, with these representation choices:
--   * times are BIGINT unix nanoseconds (UTC), never server-side now(): the
--     runtime's clock is the only source of time;
--   * TEXT columns use COLLATE "C" so ordering (resources by kind/key, task ties
--     by id) is byte order whatever the database locale, like SQLite;
--   * KV keys are BYTEA: keys are arbitrary byte strings (NUL bytes, invalid
--     UTF-8) and list in byte order;
--   * JSON payloads are BYTEA, NULL when absent, so values come back byte for
--     byte (JSONB would normalize them, TEXT rejects invalid UTF-8). Query them
--     with convert_from(value, 'UTF8')::jsonb;
--   * audit and sync IDs come from sequences: newest first means by ID.

CREATE TABLE resources (
    server     TEXT COLLATE "C" NOT NULL,
    kind       TEXT COLLATE "C" NOT NULL,
    key        TEXT COLLATE "C" NOT NULL,
    id         TEXT COLLATE "C" NOT NULL,
    owner      TEXT             NOT NULL,
    updated_at BIGINT           NOT NULL,
    PRIMARY KEY (server, kind, key)
);

CREATE INDEX resources_by_id ON resources (server, id, kind, key);

CREATE TABLE kv (
    scope      TEXT COLLATE "C" NOT NULL,
    key        BYTEA            NOT NULL,
    value      BYTEA            NOT NULL,
    expires_at BIGINT,
    PRIMARY KEY (scope, key)
);

CREATE INDEX kv_expiry ON kv (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE tasks (
    id      TEXT COLLATE "C" NOT NULL PRIMARY KEY,
    scope   TEXT COLLATE "C" NOT NULL,
    name    TEXT             NOT NULL,
    payload BYTEA,
    run_at  BIGINT           NOT NULL
);

CREATE INDEX tasks_due ON tasks (run_at, id);
CREATE INDEX tasks_by_scope ON tasks (scope);

CREATE TABLE audit_log (
    id      BIGSERIAL        PRIMARY KEY,
    time    BIGINT           NOT NULL,
    server  TEXT COLLATE "C" NOT NULL,
    actor   TEXT             NOT NULL,
    action  TEXT             NOT NULL,
    target  TEXT             NOT NULL,
    details BYTEA
);

CREATE INDEX audit_by_server ON audit_log (server, id);

CREATE TABLE sync_log (
    id       BIGSERIAL        PRIMARY KEY,
    time     BIGINT           NOT NULL,
    revision TEXT             NOT NULL,
    target   TEXT COLLATE "C" NOT NULL,
    status   TEXT             NOT NULL,
    message  TEXT             NOT NULL,
    details  BYTEA
);

CREATE INDEX sync_by_target ON sync_log (target, id);

CREATE TABLE sessions (
    id         TEXT COLLATE "C" NOT NULL PRIMARY KEY,
    user_id    TEXT             NOT NULL,
    data       BYTEA,
    expires_at BIGINT           NOT NULL
);

CREATE INDEX sessions_expiry ON sessions (expires_at);
