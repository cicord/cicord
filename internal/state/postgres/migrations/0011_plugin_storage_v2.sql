-- 0.2.0 plugin storage, second pass (review of 0.2.0-rc.1).
--
-- 1. The per-installation quota counts bytes as well as entries: 1,000,000
--    entries of 1 MiB fitted the old quota. An entry weighs its key and value
--    bytes; a ranking member its name and member bytes plus 16.
-- 2. Rankings drop materialized positions, which made every move rewrite each
--    intervening row. Ranks are counted on read; boards are capped at 250,000
--    members and their size is kept for count().
--
-- Limits here must match internal/state/advanced.go.

ALTER TABLE plugin_storage_usage ADD COLUMN bytes BIGINT NOT NULL DEFAULT 0;
INSERT INTO plugin_storage_usage(scope, entries)
    SELECT DISTINCT scope, 0 FROM rankings WHERE scope LIKE 'plugin:%'
    ON CONFLICT(scope) DO NOTHING;
UPDATE plugin_storage_usage AS usage SET bytes =
    COALESCE((SELECT SUM(octet_length(key) + octet_length(value)) FROM kv WHERE kv.scope = usage.scope), 0) +
    COALESCE((SELECT SUM(octet_length(name) + octet_length(member) + 16) FROM rankings r WHERE r.scope = usage.scope), 0);

DROP TRIGGER kv_insert_usage ON kv;
DROP TRIGGER kv_delete_usage ON kv;
DROP TRIGGER rankings_insert_usage ON rankings;
DROP TRIGGER rankings_delete_usage ON rankings;
DROP FUNCTION cicord_storage_count();

-- Statement transition tables count multi-row writes once per scope. An upsert
-- fires the INSERT trigger with its inserted rows only and the UPDATE trigger
-- with its updated rows only.
CREATE FUNCTION cicord_storage_apply(delta_entries BIGINT, delta_bytes BIGINT, target TEXT) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO plugin_storage_usage(scope, entries, bytes) VALUES(target, delta_entries, delta_bytes)
    ON CONFLICT(scope) DO UPDATE SET entries = plugin_storage_usage.entries + excluded.entries,
        bytes = plugin_storage_usage.bytes + excluded.bytes;
END $$;

CREATE FUNCTION cicord_kv_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE over BOOLEAN;
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM cicord_storage_apply(n, b, scope) FROM (
            SELECT scope, COUNT(*) AS n, SUM(octet_length(key) + octet_length(value)) AS b
            FROM added WHERE scope LIKE 'plugin:%' GROUP BY scope ORDER BY scope) AS seen;
    ELSIF TG_OP = 'UPDATE' THEN
        PERFORM cicord_storage_apply(0, b, scope) FROM (
            SELECT scope, SUM(b) AS b FROM (
                SELECT scope, octet_length(key) + octet_length(value) AS b FROM added WHERE scope LIKE 'plugin:%'
                UNION ALL
                SELECT scope, -(octet_length(key) + octet_length(value)) FROM removed WHERE scope LIKE 'plugin:%') AS d
            GROUP BY scope ORDER BY scope) AS seen;
    ELSE
        PERFORM cicord_storage_apply(-n, -b, scope) FROM (
            SELECT scope, COUNT(*) AS n, SUM(octet_length(key) + octet_length(value)) AS b
            FROM removed WHERE scope LIKE 'plugin:%' GROUP BY scope ORDER BY scope) AS seen;
        RETURN NULL;
    END IF;
    SELECT bool_or(u.entries > 1000000 OR u.bytes > 268435456) INTO over
        FROM plugin_storage_usage u WHERE u.scope IN (SELECT scope FROM added);
    IF over THEN RAISE EXCEPTION 'plugin storage quota exceeded'; END IF;
    RETURN NULL;
END $$;

CREATE FUNCTION cicord_rankings_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE over BOOLEAN;
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO ranking_sizes(scope, name, members)
            SELECT scope, name, COUNT(*) FROM added GROUP BY scope, name ORDER BY scope, name
            ON CONFLICT(scope, name) DO UPDATE SET members = ranking_sizes.members + excluded.members;
        SELECT bool_or(s.members > 250000) INTO over FROM ranking_sizes s
            WHERE (s.scope, s.name) IN (SELECT scope, name FROM added);
        IF over THEN RAISE EXCEPTION 'ranking member limit exceeded'; END IF;
        PERFORM cicord_storage_apply(n, b, scope) FROM (
            SELECT scope, COUNT(*) AS n, SUM(octet_length(name) + octet_length(member) + 16) AS b
            FROM added WHERE scope LIKE 'plugin:%' GROUP BY scope ORDER BY scope) AS seen;
        SELECT bool_or(u.entries > 1000000 OR u.bytes > 268435456) INTO over
            FROM plugin_storage_usage u WHERE u.scope IN (SELECT scope FROM added);
        IF over THEN RAISE EXCEPTION 'plugin storage quota exceeded'; END IF;
    ELSE
        UPDATE ranking_sizes AS s SET members = s.members - seen.n
            FROM (SELECT scope, name, COUNT(*) AS n FROM removed GROUP BY scope, name) AS seen
            WHERE s.scope = seen.scope AND s.name = seen.name;
        DELETE FROM ranking_sizes AS s USING (SELECT DISTINCT scope, name FROM removed) AS seen
            WHERE s.scope = seen.scope AND s.name = seen.name AND s.members <= 0;
        PERFORM cicord_storage_apply(-n, -b, scope) FROM (
            SELECT scope, COUNT(*) AS n, SUM(octet_length(name) + octet_length(member) + 16) AS b
            FROM removed WHERE scope LIKE 'plugin:%' GROUP BY scope ORDER BY scope) AS seen;
    END IF;
    RETURN NULL;
END $$;

DROP INDEX rankings_position;
ALTER TABLE rankings DROP COLUMN position;

CREATE TABLE ranking_sizes (
    scope   TEXT COLLATE "C" NOT NULL,
    name    TEXT COLLATE "C" NOT NULL,
    members BIGINT NOT NULL,
    PRIMARY KEY (scope, name)
);
INSERT INTO ranking_sizes SELECT scope, name, COUNT(*) FROM rankings GROUP BY scope, name;

CREATE TRIGGER kv_insert_usage AFTER INSERT ON kv
    REFERENCING NEW TABLE AS added FOR EACH STATEMENT EXECUTE FUNCTION cicord_kv_usage();
CREATE TRIGGER kv_update_usage AFTER UPDATE ON kv
    REFERENCING OLD TABLE AS removed NEW TABLE AS added FOR EACH STATEMENT EXECUTE FUNCTION cicord_kv_usage();
CREATE TRIGGER kv_delete_usage AFTER DELETE ON kv
    REFERENCING OLD TABLE AS removed FOR EACH STATEMENT EXECUTE FUNCTION cicord_kv_usage();
CREATE TRIGGER rankings_insert_usage AFTER INSERT ON rankings
    REFERENCING NEW TABLE AS added FOR EACH STATEMENT EXECUTE FUNCTION cicord_rankings_usage();
CREATE TRIGGER rankings_delete_usage AFTER DELETE ON rankings
    REFERENCING OLD TABLE AS removed FOR EACH STATEMENT EXECUTE FUNCTION cicord_rankings_usage();
