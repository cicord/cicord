package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// exampleTaskScopes are the task scopes of the examples/basic plugins that schedule work on
// their own (onboarding's stored-choices migration): the dead-letter tests claim only the
// tasks they schedule.
var exampleTaskScopes = []string{"plugin:moderator/onboarding/" + serverKey}

func TestDeadLetterAdministration(t *testing.T) {
	e := start(t)
	q := e.rt.Store().Tasks()
	now := time.Now()
	const private = "private-payload-canary"
	for _, server := range []string{serverKey, "another-server"} {
		task := state.Task{ID: "dead-" + server, Name: "expire", Scope: "plugin:moderator/welcome/" + server,
			RunAt: now.Add(time.Hour), Payload: json.RawMessage(`"` + private + `"`)}
		if err := q.Schedule(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}
	leases, err := q.ClaimDue(t.Context(), now.Add(time.Hour), now.Add(2*time.Hour), "test", 100, exampleTaskScopes...)
	if err != nil || len(leases) != 2 {
		t.Fatalf("leases: %v, %v", leases, err)
	}
	for _, lease := range leases {
		if err := q.Release(t.Context(), lease, time.Time{}, "handler failed"); err != nil {
			t.Fatal(err)
		}
	}
	base := "/api/v1/servers/" + serverKey + "/dead-letters"
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	mod := e.client()
	mod.login(e.moderator().ID)
	mod.expect(403, "GET", base, nil)
	mod.expect(403, "POST", base+"/dead-"+serverKey+"/retry", nil)
	admin.noCSRF = true
	admin.expect(403, "POST", base+"/dead-"+serverKey+"/discard", nil)
	admin.noCSRF = false
	admin.expect(404, "POST", base+"/dead-another-server/discard", nil)
	list := admin.expect(200, "GET", base, nil)
	var out struct{ Items []state.DeadTask }
	list.json(t, &out)
	if len(out.Items) != 1 || out.Items[0].ID != "dead-"+serverKey || strings.Contains(string(list.body), private) {
		t.Fatalf("unexpected dead letters: %s", list.body)
	}
	instances := e.rt.Instances(serverKey)
	found := false
	for _, in := range instances {
		if in.Config.Name == "welcome" && in.Health == "Degraded" {
			found = true
		}
	}
	if !found {
		t.Fatal("dead letter did not surface on the instance")
	}
	admin.expect(204, "POST", base+"/dead-"+serverKey+"/retry", nil)
	admin.expect(404, "POST", base+"/dead-"+serverKey+"/discard", nil)
	admin.expect(200, "GET", base, nil).json(t, &out)
	if len(out.Items) != 0 {
		t.Fatalf("retried work still parked: %+v", out)
	}
}
