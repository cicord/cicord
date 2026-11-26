package statetest

import (
	"errors"
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
	limited := index.(state.SubjectIndex)
	for _, i := range []state.FullTextIndex{index, other} {
		for _, subject := range []string{"123", "1234"} {
			if err := i.Upsert(ctx, "bindings", "en", subject, state.IndexDocument{Text: "canary", Tags: []string{"member-" + subject}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := limited.EraseSubject(ctx, "123", "bindings", "1234"); !errors.Is(err, state.ErrUnboundIndexDocument) {
		t.Fatal("unexpected permission", err)
	}
	if err := limited.EraseSubject(ctx, "123", "bindings", "missing"); err != nil {
		t.Fatal(err)
	}
	if err := limited.EraseSubject(ctx, "123", "", ""); err != nil {
		t.Fatal(err)
	}
	kept, err := index.Search(ctx, "bindings", "en", "canary", state.IndexQuery{})
	if err != nil || len(kept) != 1 || kept[0].ID != "1234" {
		t.Fatal(kept, err)
	}
	untouched, err := other.Search(ctx, "bindings", "en", "canary", state.IndexQuery{})
	if err != nil || len(untouched) != 2 {
		t.Fatal(untouched, err)
	}

}
