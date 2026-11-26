-- Existing pending tasks keep their original due time.
ALTER TABLE tasks ADD COLUMN available_at BIGINT;
ALTER TABLE tasks ADD COLUMN receipt TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN last_failure TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN dead BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX tasks_available ON tasks (COALESCE(available_at, run_at), id) WHERE NOT dead;
CREATE INDEX tasks_dead_scopes ON tasks (scope) WHERE dead;
