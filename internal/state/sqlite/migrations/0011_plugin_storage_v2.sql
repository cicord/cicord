-- 0.2.0 plugin storage, second pass (review of 0.2.0-rc.1).
--
-- 1. The per-installation quota counts bytes as well as entries: 1,000,000
--    entries of 1 MiB fitted the old quota. An entry weighs its key and value
--    bytes; a ranking member its name and member bytes plus 16.
-- 2. Rankings drop materialized positions, which made every move rewrite each
--    intervening row under the writer lock. Ranks are counted on read; boards
--    are capped at 250,000 members and their size is kept for count().
-- 3. Full-text rows carry an owner token per (scope, index name) that MATCH
--    filters on, so a query ranks only its own index's documents.
--
-- Limits here must match internal/state/advanced.go.

ALTER TABLE plugin_storage_usage ADD COLUMN bytes BIGINT NOT NULL DEFAULT 0;
INSERT INTO plugin_storage_usage(scope, entries)
    SELECT DISTINCT scope, 0 FROM rankings WHERE scope LIKE 'plugin:%' AND scope NOT IN (SELECT scope FROM plugin_storage_usage);
UPDATE plugin_storage_usage SET bytes =
    COALESCE((SELECT SUM(length(CAST(key AS BLOB)) + length(CAST(value AS BLOB))) FROM kv WHERE kv.scope = plugin_storage_usage.scope), 0) +
    COALESCE((SELECT SUM(length(CAST(name AS BLOB)) + length(CAST(member AS BLOB)) + 16) FROM rankings WHERE rankings.scope = plugin_storage_usage.scope), 0);

DROP TRIGGER kv_insert_usage;
DROP TRIGGER kv_delete_usage;
DROP TRIGGER rankings_insert_usage;
DROP TRIGGER rankings_delete_usage;

CREATE TRIGGER kv_insert_usage AFTER INSERT ON kv FOR EACH ROW WHEN (NEW.scope LIKE 'plugin:%') BEGIN
    INSERT INTO plugin_storage_usage(scope, entries, bytes) VALUES(NEW.scope, 1, length(CAST(NEW.key AS BLOB)) + length(CAST(NEW.value AS BLOB)))
    ON CONFLICT(scope) DO UPDATE SET entries = entries + 1, bytes = bytes + excluded.bytes;
    SELECT RAISE(ABORT, 'plugin storage quota exceeded') WHERE
        (SELECT entries > 1000000 OR bytes > 268435456 FROM plugin_storage_usage WHERE scope = NEW.scope);
END;
CREATE TRIGGER kv_update_usage AFTER UPDATE OF key, value ON kv FOR EACH ROW WHEN (NEW.scope LIKE 'plugin:%') BEGIN
    UPDATE plugin_storage_usage SET bytes = bytes
        + length(CAST(NEW.key AS BLOB)) + length(CAST(NEW.value AS BLOB))
        - length(CAST(OLD.key AS BLOB)) - length(CAST(OLD.value AS BLOB))
        WHERE scope = NEW.scope;
    SELECT RAISE(ABORT, 'plugin storage quota exceeded') WHERE
        (SELECT bytes > 268435456 FROM plugin_storage_usage WHERE scope = NEW.scope);
END;
CREATE TRIGGER kv_delete_usage AFTER DELETE ON kv FOR EACH ROW WHEN (OLD.scope LIKE 'plugin:%') BEGIN
    UPDATE plugin_storage_usage SET entries = entries - 1,
        bytes = bytes - length(CAST(OLD.key AS BLOB)) - length(CAST(OLD.value AS BLOB))
        WHERE scope = OLD.scope;
END;

DROP INDEX rankings_position;
ALTER TABLE rankings DROP COLUMN position;

CREATE TABLE ranking_sizes (
    scope   TEXT   NOT NULL,
    name    TEXT   NOT NULL,
    members BIGINT NOT NULL,
    PRIMARY KEY (scope, name)
) WITHOUT ROWID;
INSERT INTO ranking_sizes SELECT scope, name, COUNT(*) FROM rankings GROUP BY scope, name;

CREATE TRIGGER rankings_insert_usage AFTER INSERT ON rankings FOR EACH ROW BEGIN
    INSERT INTO ranking_sizes VALUES(NEW.scope, NEW.name, 1)
    ON CONFLICT(scope, name) DO UPDATE SET members = members + 1;
    SELECT RAISE(ABORT, 'ranking member limit exceeded') WHERE
        (SELECT members FROM ranking_sizes WHERE scope = NEW.scope AND name = NEW.name) > 250000;
    INSERT INTO plugin_storage_usage(scope, entries, bytes)
        SELECT NEW.scope, 1, length(CAST(NEW.name AS BLOB)) + length(CAST(NEW.member AS BLOB)) + 16 WHERE NEW.scope LIKE 'plugin:%'
    ON CONFLICT(scope) DO UPDATE SET entries = entries + 1, bytes = bytes + excluded.bytes;
    SELECT RAISE(ABORT, 'plugin storage quota exceeded') WHERE NEW.scope LIKE 'plugin:%' AND
        (SELECT entries > 1000000 OR bytes > 268435456 FROM plugin_storage_usage WHERE scope = NEW.scope);
END;
CREATE TRIGGER rankings_delete_usage AFTER DELETE ON rankings FOR EACH ROW BEGIN
    UPDATE ranking_sizes SET members = members - 1 WHERE scope = OLD.scope AND name = OLD.name;
    DELETE FROM ranking_sizes WHERE scope = OLD.scope AND name = OLD.name AND members <= 0;
    UPDATE plugin_storage_usage SET entries = entries - 1,
        bytes = bytes - length(CAST(OLD.name AS BLOB)) - length(CAST(OLD.member AS BLOB)) - 16
        WHERE scope = OLD.scope AND OLD.scope LIKE 'plugin:%';
END;

-- Owner tokens: "o" and a small integer per (scope, index name). Digits keep
-- them out of reach of the porter stemmer, and they never occur in text.
CREATE TABLE plugin_index_owners (
    id    INTEGER PRIMARY KEY,
    scope TEXT NOT NULL,
    name  TEXT NOT NULL,
    UNIQUE (scope, name)
);
INSERT INTO plugin_index_owners(scope, name) SELECT DISTINCT scope, name FROM plugin_index_docs ORDER BY scope, name;
DROP TRIGGER plugin_doc_add;
DROP TRIGGER plugin_doc_del;
DROP TABLE plugin_fts_en;
DROP TABLE plugin_fts_fr;
CREATE VIRTUAL TABLE plugin_fts_en USING fts5(title, body, owner, tokenize='porter unicode61');
CREATE VIRTUAL TABLE plugin_fts_fr USING fts5(title, body, owner, tokenize='unicode61 remove_diacritics 2');
INSERT INTO plugin_fts_en(rowid, title, body, owner)
    SELECT d.rowid, d.title, d.body, 'o' || o.id FROM plugin_index_docs d JOIN plugin_index_owners o ON o.scope = d.scope AND o.name = d.name WHERE d.lang = 'en';
INSERT INTO plugin_fts_fr(rowid, title, body, owner)
    SELECT d.rowid, d.title, d.body, 'o' || o.id FROM plugin_index_docs d JOIN plugin_index_owners o ON o.scope = d.scope AND o.name = d.name WHERE d.lang = 'fr';
CREATE TRIGGER plugin_doc_add AFTER INSERT ON plugin_index_docs BEGIN
  INSERT INTO plugin_index_usage VALUES(new.scope,new.weight) ON CONFLICT(scope) DO UPDATE SET size=size+new.weight;
  INSERT INTO plugin_index_owners(scope,name) VALUES(new.scope,new.name) ON CONFLICT(scope,name) DO NOTHING;
  INSERT INTO plugin_fts_en(rowid,title,body,owner) SELECT new.rowid,new.title,new.body,'o' || id FROM plugin_index_owners WHERE scope=new.scope AND name=new.name AND new.lang='en';
  INSERT INTO plugin_fts_fr(rowid,title,body,owner) SELECT new.rowid,new.title,new.body,'o' || id FROM plugin_index_owners WHERE scope=new.scope AND name=new.name AND new.lang='fr';
END;
CREATE TRIGGER plugin_doc_del AFTER DELETE ON plugin_index_docs BEGIN
  UPDATE plugin_index_usage SET size=size-old.weight WHERE scope=old.scope;
  DELETE FROM plugin_fts_en WHERE rowid=old.rowid AND old.lang='en';
  DELETE FROM plugin_fts_fr WHERE rowid=old.rowid AND old.lang='fr';
END;
