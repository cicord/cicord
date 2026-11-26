package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/cicord/cicord/internal/state"
)

type fullText struct {
	s     *Store
	scope string
}

func (s *Store) Index(scope string) state.FullTextIndex { return fullText{s, scope} }

func (f fullText) Upsert(ctx context.Context, name, lang, id string, d state.IndexDocument) (err error) {
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
	defer rollback(tx, &err)
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
	for i, t := range terms {
		terms[i] = "\"" + t + "\""
	}
	if opts.Tags == nil {
		opts.Tags = []string{}
	}
	tags, _ := json.Marshal(opts.Tags)
	var owner int64
	err = f.s.r.QueryRowContext(ctx, "SELECT id FROM plugin_index_owners WHERE scope=? AND name=?", f.scope, name).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil // nothing was ever indexed under this name
	}
	if err != nil {
		return nil, err
	}
	// The owner column restricts the match itself to this index, so bm25 ranks
	// only its documents and the cost follows its own matches. bm25 statistics
	// (document count, average length, term frequencies) stay table-wide, so the
	// score keeps the 0.2.0-rc.1 scale, r/(r+1e-6) (FTS5 clamps the weight of a
	// term found in most documents to 1e-6), but only at two decimals: the raw
	// digits let a plugin probe other installations' corpora.
	// FTS5 requires a table identifier in MATCH, bm25 and snippet.
	// The language selects a fixed table name. All search data is bound.
	const english = `SELECT d.id, -bm25(plugin_fts_en,3.0,1.0,0.0), snippet(plugin_fts_en,1,'','','…',32),d.meta
	FROM plugin_fts_en JOIN plugin_index_docs d ON d.rowid=plugin_fts_en.rowid
	WHERE plugin_fts_en MATCH ? AND d.scope=? AND d.name=? AND NOT EXISTS
	(SELECT value FROM json_each(?) EXCEPT SELECT value FROM json_each(d.tags))
	ORDER BY 2 DESC,d.id LIMIT ?`
	statement := english
	if lang == "fr" {
		statement = strings.ReplaceAll(english, "plugin_fts_en", "plugin_fts_fr")
	}
	match := fmt.Sprintf("owner : o%d AND {title body} : (%s)", owner, strings.Join(terms, " OR "))
	rows, err := f.s.r.QueryContext(ctx, statement, match, f.scope, name, string(tags), opts.Limit)
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
		r.Score = math.Floor(r.Score/(r.Score+0.000001)*100) / 100
		r.Meta = json.RawMessage(meta)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (f fullText) EraseSubject(ctx context.Context, subject, name, id string) error {
	q := "DELETE FROM plugin_index_docs WHERE scope=? AND EXISTS (SELECT 1 FROM json_each(plugin_index_docs.tags) WHERE value=?)"
	args := []any{f.scope, "member-" + subject}
	if name != "" {
		q += " AND name=? AND id=?"
		args = append(args, name, id)
	}
	res, err := f.s.w.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	n, err := res.RowsAffected()
	if err != nil || n > 0 {
		return err
	}
	var exists bool
	if err := f.s.r.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM plugin_index_docs WHERE scope=? AND name=? AND id=?)", f.scope, name, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return state.ErrUnboundIndexDocument
	}
	return nil
}
