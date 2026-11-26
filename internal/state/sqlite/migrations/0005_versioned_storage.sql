-- Existing values receive a stable initial version. Every subsequent write
-- assigns an independent random token, including delete/recreate cycles.
ALTER TABLE kv ADD COLUMN version TEXT NOT NULL DEFAULT 'legacy';
CREATE TABLE rankings (
    scope TEXT NOT NULL,
    name TEXT NOT NULL,
    member TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL,
    position INTEGER NOT NULL,
    PRIMARY KEY (scope, name, member)
);
CREATE INDEX rankings_order ON rankings(scope, name, score DESC, member ASC);
CREATE INDEX rankings_position ON rankings(scope, name, position);
CREATE TABLE plugin_storage_usage (scope TEXT PRIMARY KEY, entries BIGINT NOT NULL);
INSERT INTO plugin_storage_usage SELECT scope, COUNT(*) FROM kv WHERE scope LIKE 'plugin:%' GROUP BY scope;
CREATE TRIGGER rankings_insert_usage AFTER INSERT ON rankings FOR EACH ROW WHEN (NEW.scope LIKE 'plugin:%') BEGIN
    INSERT INTO plugin_storage_usage VALUES(NEW.scope, 1)
    ON CONFLICT(scope) DO UPDATE SET entries = entries + 1;
    SELECT RAISE(ABORT, 'plugin storage quota exceeded') WHERE
        (SELECT entries FROM plugin_storage_usage WHERE scope = NEW.scope) > 1000000;
END;
CREATE TRIGGER rankings_delete_usage AFTER DELETE ON rankings FOR EACH ROW WHEN (OLD.scope LIKE 'plugin:%') BEGIN UPDATE plugin_storage_usage SET entries = entries - 1 WHERE scope = OLD.scope; END;
CREATE TRIGGER kv_insert_usage AFTER INSERT ON kv FOR EACH ROW WHEN (NEW.scope LIKE 'plugin:%') BEGIN
    INSERT INTO plugin_storage_usage VALUES(NEW.scope, 1)
    ON CONFLICT(scope) DO UPDATE SET entries = entries + 1;
    SELECT RAISE(ABORT, 'plugin storage quota exceeded') WHERE
        (SELECT entries FROM plugin_storage_usage WHERE scope = NEW.scope) > 1000000;
END;
CREATE TRIGGER kv_delete_usage AFTER DELETE ON kv FOR EACH ROW WHEN (OLD.scope LIKE 'plugin:%') BEGIN UPDATE plugin_storage_usage SET entries = entries - 1 WHERE scope = OLD.scope; END;
