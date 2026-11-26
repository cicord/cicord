package statetest

import (
	"github.com/cicord/cicord/internal/state"
	"testing"
)

func FullText(t *testing.T, s state.Store) {
	ctx := t.Context()
	index := s.Index("one")
	other := s.Index("two")
	d := state.IndexDocument{Text: "Réinitialiser les réglages du serveur", Tags: []string{"help"}}
	if err := index.Upsert(ctx, "docs", "fr", "intro", d); err != nil {
		t.Fatal(err)
	}
	if err := other.Upsert(ctx, "docs", "fr", "private", d); err != nil {
		t.Fatal(err)
	}
	read := func(term, tag string, want int) {
		t.Helper()
		got, err := index.Search(ctx, "docs", "fr", term, state.IndexQuery{Tags: []string{tag}})
		if err != nil || len(got) != want {
			t.Fatalf("query: %d matches, error %v, expected %d", len(got), err, want)
		}
		for _, hit := range got {
			if hit.ID != "intro" || hit.Score <= 0 || hit.Snippet == "" {
				t.Fatal(hit)
			}
		}
	}
	read("serveur", "help", 1)
	read("serveur", "staff", 0)
	if err := index.Upsert(ctx, "docs", "fr", "intro", state.IndexDocument{Text: "météo", Tags: d.Tags}); err != nil {
		t.Fatal(err)
	}
	read("serveur", "help", 0)
	read("météo", "help", 1)
	if err := index.Remove(ctx, "docs", "intro"); err != nil {
		t.Fatal(err)
	}
	read("météo", "help", 0)
	if err := index.Upsert(ctx, "docs", "fr", "intro", d); err != nil {
		t.Fatal(err)
	}
	if err := index.Clear(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	read("serveur", "help", 0)
	if _, err := index.Search(ctx, "docs", "en", "test", state.IndexQuery{Limit: 51}); err == nil {
		t.Fatal("query cap")
	}
}
