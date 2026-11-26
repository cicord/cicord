-- A cicord v0.1.11 state database, dumped after the v0.1.11 store wrote:
-- three overdue legacy tasks and one future task (scope plugin:moderator/moderation/my-community),
-- five plugin KV keys, an enforcement pause, a managed role and an audit entry.
-- Store clock 2025-06-01T12:00:00Z. Used by the upgrade tests; do not edit by hand.
SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;
SET default_tablespace = '';
SET default_table_access_method = heap;
CREATE TABLE analytics_active (
    server text NOT NULL COLLATE pg_catalog."C",
    set_name text NOT NULL COLLATE pg_catalog."C",
    day text NOT NULL COLLATE pg_catalog."C",
    token text NOT NULL COLLATE pg_catalog."C"
);
CREATE TABLE analytics_labels (
    server text NOT NULL COLLATE pg_catalog."C",
    dim text NOT NULL COLLATE pg_catalog."C",
    id text NOT NULL COLLATE pg_catalog."C",
    name text NOT NULL,
    type text NOT NULL COLLATE pg_catalog."C",
    color text DEFAULT ''::text NOT NULL COLLATE pg_catalog."C",
    owner text DEFAULT ''::text NOT NULL COLLATE pg_catalog."C",
    deleted boolean DEFAULT false NOT NULL,
    seen_at bigint NOT NULL
);
CREATE TABLE analytics_points (
    server text NOT NULL COLLATE pg_catalog."C",
    metric text NOT NULL COLLATE pg_catalog."C",
    dim text NOT NULL COLLATE pg_catalog."C",
    bucket bigint NOT NULL,
    kind text NOT NULL COLLATE pg_catalog."C",
    value double precision NOT NULL
);
CREATE TABLE audit_log (
    id bigint NOT NULL,
    "time" bigint NOT NULL,
    server text NOT NULL COLLATE pg_catalog."C",
    actor text NOT NULL,
    action text NOT NULL,
    target text NOT NULL,
    details bytea
);
CREATE SEQUENCE audit_log_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE audit_log_id_seq OWNED BY audit_log.id;
CREATE TABLE kv (
    scope text NOT NULL COLLATE pg_catalog."C",
    key bytea NOT NULL,
    value bytea NOT NULL,
    expires_at bigint
);
CREATE TABLE resources (
    server text NOT NULL COLLATE pg_catalog."C",
    kind text NOT NULL COLLATE pg_catalog."C",
    key text NOT NULL COLLATE pg_catalog."C",
    id text NOT NULL COLLATE pg_catalog."C",
    owner text NOT NULL,
    updated_at bigint NOT NULL
);
CREATE TABLE schema_migrations (
    version integer NOT NULL,
    name text NOT NULL,
    applied_at bigint NOT NULL
);
CREATE TABLE sessions (
    id text NOT NULL COLLATE pg_catalog."C",
    user_id text NOT NULL,
    data bytea,
    expires_at bigint NOT NULL
);
CREATE TABLE sync_log (
    id bigint NOT NULL,
    "time" bigint NOT NULL,
    revision text NOT NULL,
    target text NOT NULL COLLATE pg_catalog."C",
    status text NOT NULL,
    message text NOT NULL,
    details bytea
);
CREATE SEQUENCE sync_log_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE sync_log_id_seq OWNED BY sync_log.id;
CREATE TABLE tasks (
    id text NOT NULL COLLATE pg_catalog."C",
    scope text NOT NULL COLLATE pg_catalog."C",
    name text NOT NULL,
    payload bytea,
    run_at bigint NOT NULL
);
ALTER TABLE ONLY audit_log ALTER COLUMN id SET DEFAULT nextval('audit_log_id_seq'::regclass);
ALTER TABLE ONLY sync_log ALTER COLUMN id SET DEFAULT nextval('sync_log_id_seq'::regclass);
INSERT INTO audit_log VALUES (1, 1748779200000000000, 'my-community', 'cicord', 'sync.apply', '', NULL);
INSERT INTO kv VALUES ('plugin:moderator/moderation/my-community', '\x6b30', '\x31', NULL);
INSERT INTO kv VALUES ('plugin:moderator/moderation/my-community', '\x6b31', '\x31', NULL);
INSERT INTO kv VALUES ('plugin:moderator/moderation/my-community', '\x6b32', '\x31', NULL);
INSERT INTO kv VALUES ('plugin:moderator/moderation/my-community', '\x6b33', '\x31', NULL);
INSERT INTO kv VALUES ('plugin:moderator/moderation/my-community', '\x6b34', '\x31', NULL);
INSERT INTO kv VALUES ('enforcement-pause', '\x6d792d636f6d6d756e697479', '\x7b226279223a2278227d', NULL);
INSERT INTO resources VALUES ('my-community', 'role', 'mod', '42', 'git', 1748775600000000000);
INSERT INTO schema_migrations VALUES (1, '0001_init.sql', 1748779200000000000);
INSERT INTO schema_migrations VALUES (2, '0002_analytics.sql', 1748779200000000000);
INSERT INTO schema_migrations VALUES (3, '0003_analytics_labels.sql', 1748779200000000000);
INSERT INTO tasks VALUES ('legacy-0', 'plugin:moderator/moderation/my-community', 'unban', '\x7b22757365724964223a2231227d', 1748775600000000000);
INSERT INTO tasks VALUES ('legacy-1', 'plugin:moderator/moderation/my-community', 'unban', '\x7b22757365724964223a2231227d', 1748775601000000000);
INSERT INTO tasks VALUES ('legacy-2', 'plugin:moderator/moderation/my-community', 'unban', '\x7b22757365724964223a2231227d', 1748775602000000000);
INSERT INTO tasks VALUES ('future', 'plugin:moderator/moderation/my-community', 'unban', NULL, 7258118400000000000);
SELECT pg_catalog.setval('audit_log_id_seq', 1, true);
SELECT pg_catalog.setval('sync_log_id_seq', 1, false);
ALTER TABLE ONLY analytics_active
    ADD CONSTRAINT analytics_active_pkey PRIMARY KEY (server, set_name, day, token);
ALTER TABLE ONLY analytics_labels
    ADD CONSTRAINT analytics_labels_pkey PRIMARY KEY (server, dim, id);
ALTER TABLE ONLY analytics_points
    ADD CONSTRAINT analytics_points_pkey PRIMARY KEY (server, metric, dim, bucket);
ALTER TABLE ONLY audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);
ALTER TABLE ONLY kv
    ADD CONSTRAINT kv_pkey PRIMARY KEY (scope, key);
ALTER TABLE ONLY resources
    ADD CONSTRAINT resources_pkey PRIMARY KEY (server, kind, key);
ALTER TABLE ONLY schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);
ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);
ALTER TABLE ONLY sync_log
    ADD CONSTRAINT sync_log_pkey PRIMARY KEY (id);
ALTER TABLE ONLY tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);
CREATE INDEX analytics_active_day ON analytics_active USING btree (day);
CREATE INDEX analytics_labels_seen ON analytics_labels USING btree (server, seen_at);
CREATE INDEX analytics_points_age ON analytics_points USING btree (bucket);
CREATE INDEX analytics_points_range ON analytics_points USING btree (server, metric, bucket);
CREATE INDEX analytics_points_server_age ON analytics_points USING btree (server, bucket);
CREATE INDEX audit_by_server ON audit_log USING btree (server, id);
CREATE INDEX kv_expiry ON kv USING btree (expires_at) WHERE (expires_at IS NOT NULL);
CREATE INDEX resources_by_id ON resources USING btree (server, id, kind, key);
CREATE INDEX sessions_expiry ON sessions USING btree (expires_at);
CREATE INDEX sync_by_target ON sync_log USING btree (target, id);
CREATE INDEX tasks_by_scope ON tasks USING btree (scope);
CREATE INDEX tasks_due ON tasks USING btree (run_at, id);
