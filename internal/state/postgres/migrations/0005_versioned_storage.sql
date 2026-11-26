-- Existing values receive a stable initial version. Every subsequent write
-- assigns an independent random token, including delete/recreate cycles.
ALTER TABLE kv ADD COLUMN version TEXT NOT NULL DEFAULT 'legacy';
CREATE TABLE rankings (
    scope TEXT COLLATE "C" NOT NULL,
    name TEXT COLLATE "C" NOT NULL,
    member TEXT COLLATE "C" NOT NULL,
    score DOUBLE PRECISION NOT NULL,
    position INTEGER NOT NULL,
    PRIMARY KEY (scope, name, member)
);
CREATE INDEX rankings_order ON rankings(scope, name, score DESC, member ASC);
CREATE INDEX rankings_position ON rankings(scope, name, position);
CREATE TABLE plugin_storage_usage (scope TEXT PRIMARY KEY, entries BIGINT NOT NULL);
INSERT INTO plugin_storage_usage SELECT scope, COUNT(*) FROM kv WHERE scope LIKE 'plugin:%' GROUP BY scope;


-- Statement transition tables count multi-row imports once per scope. They
-- include only actual inserts on an upsert and exclude the updated rows.
CREATE FUNCTION cicord_storage_count() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE total BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE plugin_storage_usage AS usage SET entries = usage.entries - seen.n
        FROM (SELECT scope, COUNT(*) AS n FROM removed WHERE scope LIKE 'plugin:%' GROUP BY scope) AS seen
        WHERE usage.scope = seen.scope;
    ELSE
        WITH updated AS (
            INSERT INTO plugin_storage_usage(scope, entries)
            SELECT scope, COUNT(*) FROM added WHERE scope LIKE 'plugin:%' GROUP BY scope ORDER BY scope
            ON CONFLICT(scope) DO UPDATE SET entries = plugin_storage_usage.entries + excluded.entries
            RETURNING entries
        )
        SELECT MAX(entries) INTO total FROM updated;
        IF total > 1000000 THEN RAISE EXCEPTION 'plugin storage quota exceeded'; END IF;
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER rankings_insert_usage AFTER INSERT ON rankings
    REFERENCING NEW TABLE AS added FOR EACH STATEMENT EXECUTE FUNCTION cicord_storage_count();
CREATE TRIGGER rankings_delete_usage AFTER DELETE ON rankings
    REFERENCING OLD TABLE AS removed FOR EACH STATEMENT EXECUTE FUNCTION cicord_storage_count();
CREATE TRIGGER kv_insert_usage AFTER INSERT ON kv
    REFERENCING NEW TABLE AS added FOR EACH STATEMENT EXECUTE FUNCTION cicord_storage_count();
CREATE TRIGGER kv_delete_usage AFTER DELETE ON kv
    REFERENCING OLD TABLE AS removed FOR EACH STATEMENT EXECUTE FUNCTION cicord_storage_count();
