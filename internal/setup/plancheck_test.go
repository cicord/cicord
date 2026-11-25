package setup

import (
	"testing"

	"github.com/cicord/cicord/internal/reconcile"
)

func TestPlanCheckBoostWaitsAreNotWarnings(t *testing.T) {
	boost := reconcile.Blocked{Action: "update", Kind: "server", Key: "cicord", Reason: "boost: banner requires boost level 2; server is level 0 (0 boost(s))"}
	community := reconcile.Blocked{Action: "create", Kind: "channel", Key: "news", Reason: "community: announcement channels need the Community feature"}
	for _, tc := range []struct {
		name    string
		plan    reconcile.Plan
		status  string
		waiting int
	}{
		{"empty", reconcile.Plan{}, StatusOK, 0},
		{"only boost waits", reconcile.Plan{Blocked: []reconcile.Blocked{boost}}, StatusOK, 1},
		{"other blocked", reconcile.Plan{Blocked: []reconcile.Blocked{boost, community}}, StatusWarn, 1},
		{"pending changes", reconcile.Plan{Changes: []reconcile.Change{{}}, Blocked: []reconcile.Blocked{boost}}, StatusWarn, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, msg, _, waiting := planCheck(tc.plan)
			if status != tc.status || waiting != tc.waiting {
				t.Fatalf("planCheck = %s %q (waiting %d), want %s (waiting %d)", status, msg, waiting, tc.status, tc.waiting)
			}
		})
	}
	if _, msg, _, _ := planCheck(reconcile.Plan{Blocked: []reconcile.Blocked{boost, community}}); msg != "1 change(s) are blocked." {
		t.Fatalf("message counts the boost wait: %q", msg)
	}
}
