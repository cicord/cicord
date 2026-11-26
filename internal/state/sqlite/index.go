package sqlite

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cicord/cicord/internal/state"
)

type fullText struct {
	s     *Store
	scope string
}

func (s *Store) Index(scope string) state.FullTextIndex { return fullText{s, scope} }

func (f fullText) Upsert(ctx context.Context, name, lang, id string, d state.IndexDocument) error {
	if err := state.ValidateIndex(name, lang); err != nil {
		return err
	}
	weight, err := state.ValidateDoc(id, d)
	if err != nil {
		return err
	}
	tags, _ := json.Marshal(d.Tags)
	if d.Meta == nil {
		d.Meta = json.RawMessage("null")
	}
	tx, err := f.s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_index_docs WHERE scope=? AND name=? AND id=?", f.scope, name, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_index_docs(scope,name,id,lang,title,body,tags,meta,weight) VALUES(?,?,?,?,?,?,?,?,?)`, f.scope, name, id, lang, d.Title, d.Text, string(tags), string(d.Meta), weight); err != nil {
		return err
	}
	return tx.Commit()
}

func (f fullText) Remove(ctx context.Context, name, id string) error {
	_, err := f.s.w.ExecContext(ctx, "DELETE FROM plugin_index_docs WHERE scope=? AND name=? AND id=?", f.scope, name, id)
	return err
}
func (f fullText) Clear(ctx context.Context, name string) error {
	_, err := f.s.w.ExecContext(ctx, "DELETE FROM plugin_index_docs WHERE scope=? AND name=?", f.scope, name)
	return err
}
func (f fullText) Search(ctx context.Context, name, lang, q string, opts state.IndexQuery) ([]state.IndexResult, error) {
	out := []state.IndexResult{}
	if err := state.ValidateIndex(name, lang); err != nil {
		return nil, err
	}
	terms, err := state.IndexTerms(q, &opts)
	if err != nil || len(terms) == 0 {
		return out, err
	}
	table := "plugin_fts_" + lang // ValidateIndex restricts this identifier to en or fr.
	for i, t := range terms {
		terms[i] = "\"" + t + "\""
	}
	if opts.Tags == nil {
		opts.Tags = []string{}
	}
	tags, _ := json.Marshal(opts.Tags)
	rows, err := f.s.r.QueryContext(ctx, `SELECT d.id, -bm25(`+table+`,3.0,1.0), snippet(`+table+`,1,'','','…',32),d.meta
	FROM `+table+` JOIN plugin_index_docs d ON d.rowid=`+table+`.rowid
	WHERE `+table+` MATCH ? AND d.scope=? AND d.name=? AND NOT EXISTS
	(SELECT value FROM json_each(?) EXCEPT SELECT value FROM json_each(d.tags))
	ORDER BY 2 DESC,d.id LIMIT ?`, strings.Join(terms, " OR "), f.scope, name, string(tags), opts.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r state.IndexResult
		var meta string
		if err := rows.Scan(&r.ID, &r.Score, &r.Snippet, &meta); err != nil {
			return nil, err
		}
		r.Score = r.Score / (r.Score + 0.000001)
		r.Meta = json.RawMessage(meta)
		out = append(out, r)
	}
	return out, rows.Err()
}
