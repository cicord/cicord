package statetest

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// PurgeScope checks the orphaned-installation purge: listing a server's plugin
// scopes with their row counts, and deleting one scope (records, rankings,
// index documents, tasks and a companion KV scope) without touching any other
// scope, including the same installation name on another server.
func PurgeScope(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	const gone, kept, other = "plugin:bot/gone/srv", "plugin:bot/kept/srv", "plugin:bot/gone/elsewhere"
	for _, scope := range []string{gone, kept, other} {
		kv := s.KV(scope).(state.AdvancedKV)
		must(t, kv.Set(ctx, "member:123456789012345678", json.RawMessage(`{"xp":1}`), 0))
		must(t, kv.Set(ctx, "second", json.RawMessage(`2`), 0))
		_, err := kv.Rank(ctx, state.RankOp{Name: "xp", Member: "123456789012345678", Method: "set", Score: 3})
		must(t, err)
		must(t, s.Index(scope).Upsert(ctx, "notes", "en", "doc", state.IndexDocument{Text: "remember this", Tags: []string{"member-123456789012345678"}}))
		must(t, s.Tasks().Schedule(ctx, state.Task{ID: "task-" + scope, Scope: scope, Name: "remind", Payload: json.RawMessage(`{"user":"123456789012345678"}`), RunAt: time.Now().Add(time.Hour)}))
	}
	must(t, s.KV("dm-receipts:bot/gone/srv").Set(ctx, "receipt", json.RawMessage(`1`), 0))
	must(t, s.KV("dm-receipts:bot/kept/srv").Set(ctx, "receipt", json.RawMessage(`1`), 0))

	purger, ok := s.KV(gone).(state.ScopePurger)
	if !ok {
		t.Fatal("backend KV does not implement state.ScopePurger")
	}
	scopes, err := purger.PluginScopes(ctx, "srv")
	must(t, err)
	if len(scopes) != 2 || scopes[0].Scope != gone || scopes[1].Scope != kept {
		t.Fatalf("scopes %+v", scopes)
	}
	want := state.ScopeUsage{Scope: gone, Records: 2, Rankings: 1, Index: 1, Tasks: 1}
	if scopes[0] != want {
		t.Fatalf("usage %+v, want %+v", scopes[0], want)
	}
	if _, err := s.KV("dm-receipts:x").(state.ScopePurger).PurgeScope(ctx); err == nil {
		t.Fatal("purged a non-plugin scope")
	}
	if _, err := purger.PurgeScope(ctx, "plugin:bot/kept/srv"); err == nil {
		t.Fatal("accepted a plugin scope as companion")
	}
	got, err := purger.PurgeScope(ctx, "dm-receipts:bot/gone/srv")
	must(t, err)
	want.Records = 3
	if got != want {
		t.Fatalf("purged %+v, want %+v", got, want)
	}
	scopes, err = purger.PluginScopes(ctx, "srv")
	must(t, err)
	if len(scopes) != 1 || scopes[0].Scope != kept || scopes[0].Total() != 5 {
		t.Fatalf("after purge %+v", scopes)
	}
	if _, err := s.KV("dm-receipts:bot/gone/srv").Get(ctx, "receipt"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("companion scope kept", err)
	}
	if _, err := s.KV("dm-receipts:bot/kept/srv").Get(ctx, "receipt"); err != nil {
		t.Fatal("other companion scope purged", err)
	}
	hits, err := s.Index(gone).Search(ctx, "notes", "en", "remember", state.IndexQuery{})
	must(t, err)
	if len(hits) != 0 {
		t.Fatal("index documents survived", hits)
	}
	hits, err = s.Index(other).Search(ctx, "notes", "en", "remember", state.IndexQuery{})
	must(t, err)
	if len(hits) != 1 {
		t.Fatal("other server's index purged", hits)
	}
	// The usage counters follow the deletes: the scope can be refilled.
	must(t, s.KV(gone).Set(ctx, "again", json.RawMessage(`1`), 0))
	if again, err := purger.PluginScopes(ctx, "elsewhere"); err != nil || len(again) != 1 || again[0].Total() != 5 {
		t.Fatalf("other server %+v %v", again, err)
	}
}
