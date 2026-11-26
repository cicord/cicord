package advanced

import (
	"testing"

	"github.com/cicord/cicord/internal/state"
)

func TestPageCursorAcrossSnapshots(t *testing.T) {
	page := func(keys ...string) []state.KVItem {
		out := make([]state.KVItem, len(keys))
		for i, k := range keys {
			out[i] = state.KVItem{Key: k}
		}
		return out
	}
	for _, c := range []struct {
		name  string
		items []state.KVItem
		n     int
		last  string
		more  bool
		want  string
	}{
		{"complete last page", page("a", "b"), 2, "b", false, ""},
		{"more entries follow", page("a", "b"), 2, "b", true, "b"},
		{"a key inserted before last pushed it out", page("a", "a2"), 2, "b", false, "a2"},
		{"an entry deleted in between", page("a"), 2, "b", false, "a"},
		{"every entry deleted in between", page(), 2, "b", false, "b"},
	} {
		if got := pageCursor(c.items, c.n, c.last, c.more); got != c.want {
			t.Errorf("%s: cursor %q, want %q", c.name, got, c.want)
		}
	}
}
