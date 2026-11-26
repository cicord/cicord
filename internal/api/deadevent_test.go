package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
)

// parkEvent publishes an event (published in 2020) to subscribers on several servers and
// parks every delivery as a dead letter. It returns the dead letter ids per server.
func parkEvent(t *testing.T, e *env, id, payload string, servers ...string) map[string]string {
	t.Helper()
	at := time.Now().Add(time.Hour) // after the scheduler's horizon: only this test claims it
	ev := state.EventFanout{ID: id, Server: serverKey, Publisher: "plugin:moderator/welcome/" + serverKey, Topic: "test.topic", At: at,
		Payload: json.RawMessage(`{"meta":{"id":"` + id + `","topic":"test.topic","version":1,"at":"2020-01-02T03:04:05Z"},"payload":` + payload + `}`)}
	for _, s := range servers {
		ev.Subscribers = append(ev.Subscribers, "plugin:moderator/welcome/"+s)
	}
	q := e.rt.Store().Tasks()
	if err := e.rt.Store().Interop().PublishEvent(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	leases, err := q.ClaimDue(t.Context(), at, at.Add(time.Hour), "test", 100)
	if err != nil || len(leases) != len(servers) {
		t.Fatalf("leases %v %v", leases, err)
	}
	for _, l := range leases {
		if err := q.Release(t.Context(), l, time.Time{}, "handler failed"); err != nil {
			t.Fatal(err)
		}
	}
	out := map[string]string{}
	for _, s := range servers {
		items, err := q.DeadLetters(t.Context(), s, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.Event && out[s] == "" {
				raw, _ := e.rt.Store().Interop().DeadEvent(t.Context(), s, it.ID)
				if strings.Contains(string(raw), `"id":"`+id+`"`) {
					out[s] = it.ID
				}
			}
		}
		if out[s] == "" {
			t.Fatalf("no dead letter of %s on %s", id, s)
		}
	}
	return out
}

// The raw event view (it may describe a private channel) needs current Discord
// Administrator authority on the server named in the URL, and never returns erased data.
func TestDeadEventAccess(t *testing.T) {
	privacy := t.TempDir()
	e := startFull(t, func(dir string) {
		p := filepath.Join(dir, "servers", "my-community.yaml")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// Members are Cicord admins here, without the Discord Administrator permission.
		s := strings.Replace(string(raw), "    admins:\n      roles: [admin]", "    admins:\n      roles: [admin, member]", 1)
		if s == string(raw) {
			t.Fatal("fixture changed")
		}
		writeTestFile(t, p, s)
	}, func(o *runtime.Options) { o.PrivacyDir = privacy }, nil)
	const canary = "private-channel-canary"
	ids := parkEvent(t, e, "ev-access", `{"text":"`+canary+`"}`, serverKey, "another-server")
	path := func(id string) string { return "/api/v1/servers/" + serverKey + "/dead-letters/" + id + "/event" }

	owner := e.client()
	owner.login(e.seed.Owner.ID)
	r := owner.expect(200, "GET", path(ids[serverKey]), nil)
	if !strings.Contains(string(r.body), canary) || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("owner view %s %v", r.body, r.header)
	}
	// The same event fanned out to another server is not reachable through this one.
	owner.expect(404, "GET", path(ids["another-server"]), nil)

	mod := e.client()
	mod.login(e.moderator().ID)
	mod.expect(403, "GET", path(ids[serverKey]), nil)

	admin := e.world.CreateUser("cicord-admin")
	if err := e.world.AddMember(e.serverID(), admin.ID); err != nil {
		t.Fatal(err)
	}
	role, ok := e.world.RoleByName(e.serverID(), "Member")
	if !ok {
		t.Fatal("missing Member role")
	}
	if err := e.world.AdminAddMemberRole(e.serverID(), admin.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	ca := e.client()
	if me := ca.login(admin.ID); len(me.Servers) == 0 || me.Servers[0].Role != "admin" {
		t.Fatalf("fixture: %+v", me)
	}
	ca.expect(200, "GET", "/api/v1/servers/"+serverKey+"/dead-letters", nil) // the queue stays available
	if r := ca.expect(403, "GET", path(ids[serverKey]), nil); strings.Contains(string(r.body), canary) {
		t.Fatal(string(r.body))
	}
	// Discord Administrator granted now: the next read sees it (no cached member).
	adminRole, _ := e.world.RoleByName(e.serverID(), "Admin")
	if err := e.world.AdminAddMemberRole(e.serverID(), admin.ID, adminRole.ID); err != nil {
		t.Fatal(err)
	}
	ca.expect(200, "GET", path(ids[serverKey]), nil)

	// A token session is capped at member outside the portal.
	const fan = "https://fan.example"
	e.setProjectPortal(t, "  portal:\n    allowedOrigins: [\""+fan+"\"]\n")
	e.waitFor("allowed origins", func() bool {
		repo := e.rt.Repo()
		return repo.Project.Spec.Portal != nil && len(repo.Project.Spec.Portal.AllowedOrigins) == 1
	})
	tok := e.client()
	tok.noCSRF = true
	lr := tok.do("POST", "/auth/dev-login", map[string]string{"userId": e.seed.Owner.ID, "mode": "token"}, "Origin", fan, "X-Cicord-Request", "1")
	var ts tokenSessionDTO
	lr.json(t, &ts)
	tok.bearer = ts.Token
	tok.expect(403, "GET", path(ids[serverKey]), nil)

	// Erasure purges the member's dead events; one restored from a backup stays hidden.
	const member = "123456789012345678"
	erasedIDs := parkEvent(t, e, "ev-erased", `{"userId":"`+member+`"}`, serverKey)
	if _, err := e.rt.MemberData(t.Context(), serverKey, member, "erase"); err != nil {
		t.Fatal(err)
	}
	owner.expect(404, "GET", path(erasedIDs[serverKey]), nil)
	// Restore a pre-erasure database: the event is back and the SQL marker is gone, but
	// the marker kept outside the database still hides it.
	marks := e.rt.Store().KV("privacy:v1:" + serverKey)
	entries, _, err := marks.List(t.Context(), "", 100, "")
	if err != nil || len(entries) == 0 {
		t.Fatalf("markers %v %v", entries, err)
	}
	for _, m := range entries {
		if err := marks.Delete(t.Context(), m.Key); err != nil {
			t.Fatal(err)
		}
	}
	restored := parkEvent(t, e, "ev-restored", `{"userId":"`+member+`"}`, serverKey)
	if r := owner.expect(404, "GET", path(restored[serverKey]), nil); strings.Contains(string(r.body), member) {
		t.Fatal(string(r.body))
	}
}
