package sqlite

import (
	"fmt"
	"testing"

	"github.com/cicord/cicord/internal/state"
)

// m1: FTS5 tables are shared by every installation. Another installation's
// documents are never matched or ranked by this installation's queries, and the
// score is a coarse absolute relevance, not bm25's raw digits (which also made
// every score about 0.99999).
func TestSearchScoresIgnoreOtherInstallations(t *testing.T) {
	s := mustOpen(t, ":memory:")
	mine, theirs := s.Index("plugin:b/mine/s"), s.Index("plugin:b/theirs/s")
	for i, text := range []string{"refund policy for orders", "refund refund refund", "shipping times"} {
		must(t, mine.Upsert(ctx(t), "faq", "en", fmt.Sprint(i), state.IndexDocument{Title: "Help", Text: text}))
	}
	search := func() []state.IndexResult {
		hits, err := mine.Search(ctx(t), "faq", "en", "refund", state.IndexQuery{})
		must(t, err)
		return hits
	}
	before := search()
	if len(before) != 2 || before[0].ID != "1" {
		t.Fatalf("hits %+v", before)
	}
	for _, h := range before {
		if h.Score < 0 || h.Score > 1 || h.Score*100 != float64(int(h.Score*100)) {
			t.Fatalf("score %v is not a two-decimal relevance", h.Score)
		}
	}
	for i := range 200 {
		must(t, theirs.Upsert(ctx(t), "faq", "en", fmt.Sprint(i), state.IndexDocument{Title: "Refund", Text: fmt.Sprintf("refund request %d with a much longer body than the others", i)}))
	}
	after := search()
	if len(after) != 2 || after[0].ID != before[0].ID || after[1].ID != before[1].ID {
		t.Fatalf("another installation changed the results:\n%+v\n%+v", before, after)
	}
	// Results of a never-indexed name are empty, not an error.
	hits, err := mine.Search(ctx(t), "nothing", "en", "refund", state.IndexQuery{})
	if err != nil || len(hits) != 0 {
		t.Fatalf("unknown index: %v %v", hits, err)
	}
}
