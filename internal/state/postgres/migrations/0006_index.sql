CREATE TABLE plugin_index_docs (
  scope TEXT NOT NULL, name TEXT NOT NULL, id TEXT NOT NULL, lang TEXT NOT NULL,
  title TEXT NOT NULL, body TEXT NOT NULL, tags JSONB NOT NULL, meta JSONB NOT NULL,
  words TSVECTOR NOT NULL, weight INTEGER NOT NULL, PRIMARY KEY(scope,name,id)
);
CREATE INDEX plugin_index_words ON plugin_index_docs USING GIN(words);
CREATE TABLE plugin_index_usage (scope TEXT PRIMARY KEY, size BIGINT NOT NULL CHECK(size BETWEEN 0 AND 20971520));
CREATE FUNCTION plugin_index_account() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE plugin_index_usage SET size=size-OLD.weight WHERE scope=OLD.scope;
  ELSIF TG_OP = 'UPDATE' THEN
    UPDATE plugin_index_usage SET size=size+NEW.weight-OLD.weight WHERE scope=NEW.scope;
  ELSE
    INSERT INTO plugin_index_usage VALUES(NEW.scope,NEW.weight)
      ON CONFLICT(scope) DO UPDATE SET size=plugin_index_usage.size+NEW.weight;
  END IF;
  RETURN NULL;
END;
$$;
CREATE TRIGGER plugin_index_limit AFTER INSERT OR UPDATE OR DELETE ON plugin_index_docs
FOR EACH ROW EXECUTE FUNCTION plugin_index_account();
