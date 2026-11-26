package setup

import (
	"errors"
	"testing"

	"github.com/cicord/cicord/internal/writeback"
)

func batchPlan(source, from, to string, servers []string, added []string, edits map[string]writeback.EditFunc) *UpdatePlan {
	p := &UpdatePlan{Spec: UpdateSpec{Source: source, Version: to}, Current: InstalledVersion{Version: from}, Servers: servers, Edits: edits}
	p.Diff.Capabilities.Added = added
	return p
}

func TestUpdateMessage(t *testing.T) {
	one := batchPlan("acme/hello", "1.0.0", "1.1.0", []string{"a", "b"}, []string{"storage.kv"}, nil)
	if got, want := updateMessage([]*UpdatePlan{one}), "plugin: update acme/hello 1.0.0 → 1.1.0\n\nUpdated on a, b.\nNewly granted: storage.kv."; got != want {
		t.Errorf("single = %q, want %q", got, want)
	}
	two := batchPlan("cicord/polls", "0.1.1", "0.2.0", []string{"b", "c"}, nil, nil)
	want := "plugin: update 2 plugins\n\nacme/hello 1.0.0 → 1.1.0\nNewly granted: storage.kv.\n\ncicord/polls 0.1.1 → 0.2.0\n\nUpdated on a, b, c."
	if got := updateMessage([]*UpdatePlan{one, two}); got != want {
		t.Errorf("batch = %q, want %q", got, want)
	}
}

// Edits of the same file are applied in turn (plans in order); any error aborts the file.
func TestMergeEdits(t *testing.T) {
	appendS := func(s string) writeback.EditFunc {
		return func(cur []byte) ([]byte, error) { return append(cur, s...), nil }
	}
	a := batchPlan("a", "", "", nil, nil, map[string]writeback.EditFunc{"bot.yaml": appendS("a"), "plugins/a/x": appendS("x")})
	b := batchPlan("b", "", "", nil, nil, map[string]writeback.EditFunc{"bot.yaml": appendS("b")})
	m := MergeEdits([]*UpdatePlan{a, b})
	if len(m) != 2 {
		t.Fatalf("paths = %d", len(m))
	}
	if got, err := m["bot.yaml"]([]byte(">")); err != nil || string(got) != ">ab" {
		t.Errorf("bot.yaml = %q, %v", got, err)
	}
	boom := errors.New("changed meanwhile")
	c := batchPlan("c", "", "", nil, nil, map[string]writeback.EditFunc{"bot.yaml": func([]byte) ([]byte, error) { return nil, boom }})
	if _, err := MergeEdits([]*UpdatePlan{a, c, b})["bot.yaml"]([]byte(">")); !errors.Is(err, boom) {
		t.Errorf("error of one plan: %v", err)
	}
}
