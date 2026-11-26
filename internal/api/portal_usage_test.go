package api

import (
	"testing"
	"time"
)

// The portal limiter (default 120 per minute: burst 60, then 2 per second) never refuses the
// portal SPA's normal use, measured in create-cicord-portal: a cold load spends 2 to 6
// limited requests (GET /portal/{s}/me plus the page's plugin routes), the SPA does not poll,
// and revisits hit its query cache. A looping client is still refused within a second.
func TestPortalLimiterFitsNormalUse(t *testing.T) {
	l := newUserLimiter(120)
	now := time.Unix(0, 0)
	spend := func(n int, at time.Time) int {
		refused := 0
		for range n {
			if !l.allow("member", at) {
				refused++
			}
		}
		return refused
	}
	// Three tabs restored at once on the heaviest page (TicketDesk with a ticket open: 6).
	if r := spend(3*6, now); r != 0 {
		t.Fatalf("restored tabs: %d refused", r)
	}
	// Then a quick tour of 25 feature screens (1–3 reads each) within 10 seconds.
	for i := range 25 {
		if r := spend(2, now.Add(time.Duration(i)*400*time.Millisecond)); r != 0 {
			t.Fatalf("navigation step %d: %d refused", i, r)
		}
	}
	// A loop that ignores responses is refused.
	if r := spend(200, now.Add(10*time.Second)); r < 150 {
		t.Fatalf("a tight loop was refused only %d times out of 200", r)
	}
}
