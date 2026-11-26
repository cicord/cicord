package postgres

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
func dict(lang string) string {
	if lang == "fr" {
		return "french"
	}
	return "english"
}

func (f fullText) Upsert(ctx context.Context, name, lang, id string, d state.IndexDocument) error {
	if err := state.ValidateIndex(name, lang); err != nil {
		return err
	}
	weight, err := state.ValidateDoc(id, d)
	if err != nil {
		return err
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	tags, _ := json.Marshal(d.Tags)
	if d.Meta == nil {
		d.Meta = json.RawMessage("null")
	}
	_, err = f.s.pool.Exec(ctx, `INSERT INTO plugin_index_docs(scope,name,id,lang,title,body,tags,meta,words,weight)
	VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,setweight(to_tsvector($9::regconfig,$5),'A') || to_tsvector($9::regconfig,$6),$10)
	ON CONFLICT(scope,name,id) DO UPDATE SET lang=excluded.lang,title=excluded.title,body=excluded.body,tags=excluded.tags,meta=excluded.meta,words=excluded.words,weight=excluded.weight`,
		f.scope, name, id, lang, d.Title, d.Text, string(tags), string(d.Meta), dict(lang), weight)
	return err
}
func (f fullText) Remove(ctx context.Context, name, id string) error {
	_, err := f.s.pool.Exec(ctx, "DELETE FROM plugin_index_docs WHERE scope=$1 AND name=$2 AND id=$3", f.scope, name, id)
	return err
}
func (f fullText) Clear(ctx context.Context, name string) error {
	_, err := f.s.pool.Exec(ctx, "DELETE FROM plugin_index_docs WHERE scope=$1 AND name=$2", f.scope, name)
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
	if opts.Tags == nil {
		opts.Tags = []string{}
	}
	tags, _ := json.Marshal(opts.Tags)
	rows, err := f.s.pool.Query(ctx, `SELECT id,ts_rank(words,query),ts_headline($1::regconfig,body,query,'StartSel="", StopSel="", MaxWords=32, MinWords=8'),meta
	FROM plugin_index_docs,to_tsquery($1::regconfig,$2) query
	WHERE scope=$3 AND name=$4 AND lang=$5 AND words @@ query AND tags @> $6::jsonb
	ORDER BY 2 DESC,id LIMIT $7`, dict(lang), strings.Join(terms, " | "), f.scope, name, lang, string(tags), opts.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r state.IndexResult
		if err := rows.Scan(&r.ID, &r.Score, &r.Snippet, &r.Meta); err != nil {
			return nil, err
		}
		r.Score /= r.Score + 0.05
		out = append(out, r)
	}
	return out, rows.Err()
}

func (f fullText) EraseSubject(ctx context.Context, subject, name, id string) error {
	q := "DELETE FROM plugin_index_docs WHERE scope=$1 AND tags ? $2"
	args := []any{f.scope, "member-" + subject}
	if name != "" {
		q += " AND name=$3 AND id=$4"
		args = append(args, name, id)
	}
	tag, err := f.s.pool.Exec(ctx, q, args...)
	if err != nil || name == "" || tag.RowsAffected() > 0 {
		return err
	}
	var exists bool
	if err := f.s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM plugin_index_docs WHERE scope=$1 AND name=$2 AND id=$3)", f.scope, name, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return state.ErrUnboundIndexDocument
	}
	return nil
}
