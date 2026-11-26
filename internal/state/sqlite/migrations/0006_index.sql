CREATE TABLE plugin_index_docs (
  rowid INTEGER PRIMARY KEY, scope TEXT NOT NULL, name TEXT NOT NULL, id TEXT NOT NULL,
  lang TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, tags TEXT NOT NULL, meta TEXT NOT NULL,
  weight INTEGER NOT NULL, UNIQUE(scope,name,id)
);
CREATE TABLE plugin_index_usage (scope TEXT PRIMARY KEY, size INTEGER NOT NULL CHECK(size BETWEEN 0 AND 20971520));
CREATE VIRTUAL TABLE plugin_fts_en USING fts5(title, body, tokenize='porter unicode61');
CREATE VIRTUAL TABLE plugin_fts_fr USING fts5(title, body, tokenize='unicode61 remove_diacritics 2');
CREATE TRIGGER plugin_doc_add AFTER INSERT ON plugin_index_docs BEGIN
  INSERT INTO plugin_index_usage VALUES(new.scope,new.weight) ON CONFLICT(scope) DO UPDATE SET size=size+new.weight;
  INSERT INTO plugin_fts_en(rowid,title,body) SELECT new.rowid,new.title,new.body WHERE new.lang='en';
  INSERT INTO plugin_fts_fr(rowid,title,body) SELECT new.rowid,new.title,new.body WHERE new.lang='fr';
END;
CREATE TRIGGER plugin_doc_del AFTER DELETE ON plugin_index_docs BEGIN
  UPDATE plugin_index_usage SET size=size-old.weight WHERE scope=old.scope;
  DELETE FROM plugin_fts_en WHERE rowid=old.rowid AND old.lang='en';
  DELETE FROM plugin_fts_fr WHERE rowid=old.rowid AND old.lang='fr';
END;
