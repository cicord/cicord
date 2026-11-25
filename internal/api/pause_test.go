package api

import (
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/runtime"
)

// Pause and resume: moderators and admins may use them (CSRF and auth as usual); a sync
// is refused with 409 paused while the server is paused, unless it resumes; every pause
// and resume is in the audit log and the sync history (not as an error).
func TestPauseResume(t *testing.T) {
	e := start(t)
	base := "/api/v1/servers/" + serverKey

	// a plain member cannot pause
	alice := e.seed.Users[0]
	if err := e.world.AddMember(e.serverID(), alice.ID); err != nil {
		t.Fatal(err)
	}
	m := e.client()
	m.login(alice.ID)
	m.expect(403, "POST", base+"/pause", map[string]string{"reason": "nope"})
	m.expect(403, "GET", base+"/pause", nil)

	mod := e.moderator()
	c := e.client()
	c.login(mod.ID)
	// not paused: moderators get no plan (the plan endpoint is admin-only)
	var idle struct {
		Paused *runtime.PauseInfo `json:"paused"`
		Plan   *planDTO           `json:"plan"`
	}
	c.expect(200, "GET", base+"/pause", nil).json(t, &idle)
	if idle.Paused != nil || idle.Plan != nil {
		t.Fatalf("moderator preview of a running server = %+v", idle)
	}
	// CSRF
	c.noCSRF = true
	if r := c.do("POST", base+"/pause", map[string]string{"reason": "raid"}); r.status != 403 {
		t.Fatalf("pause without X-Cicord-Request = %d %s", r.status, r.body)
	}
	c.noCSRF = false
	c.expect(400, "POST", base+"/pause", map[string]string{"reason": strings.Repeat("x", runtime.MaxPauseReason+1)})

	var paused struct {
		Paused  runtime.PauseInfo `json:"paused"`
		Changed bool              `json:"changed"`
	}
	c.expect(200, "POST", base+"/pause", map[string]string{"reason": "raid"}).json(t, &paused)
	if !paused.Changed || paused.Paused.By != "discord:"+mod.ID || paused.Paused.ByName == "" || paused.Paused.Reason != "raid" {
		t.Fatalf("pause = %+v", paused)
	}
	c.expect(200, "POST", base+"/pause", nil).json(t, &paused)
	if paused.Changed || paused.Paused.Reason != "raid" {
		t.Fatalf("second pause = %+v", paused)
	}

	// shown in the status and the server detail
	var st struct {
		Servers []struct {
			Key    string             `json:"key"`
			Sync   string             `json:"sync"`
			Paused *runtime.PauseInfo `json:"paused"`
		} `json:"servers"`
	}
	c.expect(200, "GET", "/api/v1/status", nil).json(t, &st)
	if len(st.Servers) != 1 || st.Servers[0].Paused == nil || st.Servers[0].Paused.Reason != "raid" {
		t.Fatalf("status = %+v", st)
	}
	var detail map[string]any
	c.expect(200, "GET", base, nil).json(t, &detail)
	if p, _ := detail["paused"].(map[string]any); p["reason"] != "raid" {
		t.Fatalf("detail paused = %v", detail["paused"])
	}
	// the resume preview: the pause and the pending plan (moderators included)
	var preview struct {
		Paused *runtime.PauseInfo `json:"paused"`
		Plan   *planDTO           `json:"plan"`
	}
	c.expect(200, "GET", base+"/pause", nil).json(t, &preview)
	if preview.Paused == nil || preview.Plan == nil || preview.Plan.Server != serverKey {
		t.Fatalf("preview = %+v", preview)
	}

	// Sync now is refused (the owner is an admin)
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	r := owner.expect(409, "POST", base+"/sync", nil)
	if r.errCode() != "paused" || !strings.Contains(string(r.body), "enforcement is paused on server "+serverKey) ||
		!strings.Contains(string(r.body), `{\"resume\": true}`) {
		t.Fatalf("sync while paused = %s", r.body)
	}

	// a moderator resumes
	var resumed struct {
		Resumed bool               `json:"resumed"`
		Pause   *runtime.PauseInfo `json:"pause"`
	}
	c.expect(200, "POST", base+"/resume", nil).json(t, &resumed)
	if !resumed.Resumed || resumed.Pause == nil || resumed.Pause.Reason != "raid" {
		t.Fatalf("resume = %+v", resumed)
	}
	c.expect(200, "POST", base+"/resume", nil).json(t, &resumed)
	if resumed.Resumed {
		t.Fatal("resumed twice")
	}
	owner.expect(200, "POST", base+"/sync", nil)

	// sync with {"resume": true} resumes and syncs in one call
	c.expect(200, "POST", base+"/pause", map[string]string{"reason": "again"})
	owner.expect(200, "POST", base+"/sync", map[string]bool{"resume": true})
	if _, ok := e.rt.Paused(serverKey); ok {
		t.Fatal("still paused after sync with resume")
	}

	// audit log and history
	var audit struct {
		Items []auditDTO `json:"items"`
	}
	c.expect(200, "GET", base+"/audit", nil).json(t, &audit)
	actions := map[string]int{}
	for _, a := range audit.Items {
		actions[a.Action]++
	}
	if actions["enforcement.pause"] != 2 || actions["enforcement.resume"] != 2 {
		t.Fatalf("audit = %v", actions)
	}
	var hist struct{ Items []syncRecordDTO }
	c.expect(200, "GET", "/api/v1/history?target=server:"+serverKey, nil).json(t, &hist)
	seen := map[string]bool{}
	for _, h := range hist.Items {
		if h.Status == runtime.SyncRecordPaused || h.Status == runtime.SyncRecordResumed {
			seen[h.Status] = true
			if h.Error != nil {
				t.Errorf("history %s humanized as an error: %+v", h.Status, h.Error)
			}
		}
	}
	if !seen[runtime.SyncRecordPaused] || !seen[runtime.SyncRecordResumed] {
		t.Fatalf("history = %+v", hist.Items)
	}

	// a moderator cannot sync with resume (sync is admin)
	c.expect(200, "POST", base+"/pause", nil)
	c.expect(403, "POST", base+"/sync", map[string]bool{"resume": true})
	if _, ok := e.rt.Paused(serverKey); !ok {
		t.Fatal("a refused sync resumed the server")
	}
}
