CREATE TABLE interop_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, expires_at BIGINT NOT NULL
);
CREATE INDEX interop_event_expiry ON interop_events(expires_at);
CREATE TABLE interop_actions (
 id TEXT PRIMARY KEY, server TEXT NOT NULL, digest TEXT NOT NULL, status TEXT NOT NULL,
 owner TEXT NOT NULL, lease_until BIGINT NOT NULL, expires_at BIGINT NOT NULL, result TEXT
);
CREATE INDEX interop_action_expiry ON interop_actions(expires_at);
ALTER TABLE tasks ADD COLUMN event_stream TEXT;
ALTER TABLE tasks ADD COLUMN event_seq BIGINT;
ALTER TABLE tasks ADD COLUMN dead_at BIGINT;
CREATE INDEX interop_pending_stream ON tasks(event_stream,event_seq) WHERE event_seq IS NOT NULL AND NOT dead;
