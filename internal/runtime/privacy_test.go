package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

func TestMemberErasureReplayAndRestore(t *testing.T) {
	e := startExampleResync(t, nil, withPrivacyWelcome(t), -1)
	ctx := t.Context()
	const server = "my-community"
	const a = "111111111111111111"
	const b = "222222222222222222"
	scope := "plugin:moderator/welcome/" + server
	kv := e.rt.Store().KV(scope).(state.AdvancedKV)
	snapshot := json.RawMessage(`[{"user":"` + a + `","text":"private A"},{"user":"` + b + `","text":"keep B"}]`)
	seed := func() {
		t.Helper()
		for _, user := range []string{a, b} {
			if err := e.rt.Store().KV("private-replies:"+server).Set(ctx, "user:"+user+":private-command:pending", json.RawMessage(`{"ciphertext":"sealed"}`), time.Minute); err != nil {
				t.Fatal(err)
			}
		}
		if err := kv.Set(ctx, "ticket:1", snapshot, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Rank(ctx, state.RankOp{Name: "xp", Member: a, Method: "set", Score: 5}); err != nil {
			t.Fatal(err)
		}
	}
	verify := func() {
		t.Helper()
		for _, user := range []string{a, b} {
			_, err := e.rt.Store().KV("private-replies:"+server).Get(ctx, "user:"+user+":private-command:pending")
			if (user == a && !errors.Is(err, state.ErrNotFound)) || (user == b && err != nil) {
				t.Fatalf("private reply erasure %s: %v", user, err)
			}
		}
		raw, err := kv.Get(ctx, "ticket:1")
		if err != nil || strings.Contains(string(raw), a) || strings.Contains(string(raw), "private A") || !strings.Contains(string(raw), "keep B") {
			t.Fatalf("shared ticket %s %v", raw, err)
		}
		r, err := kv.Rank(ctx, state.RankOp{Name: "xp", Member: a, Method: "rank"})
		if err != nil || r != nil {
			t.Fatal("ranking", r, err)
		}
	}
	seed()
	rep, err := e.rt.MemberData(ctx, server, a, "export")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(rep)
	if !strings.Contains(string(data), "private A") || strings.Contains(string(data), "keep B") {
		t.Fatal(string(data))
	}
	// External journal remains current across a rollback of the state database.
	q := e.rt.Store().Tasks()
	now := time.Now().Add(time.Hour)
	for _, id := range []string{a, b} {
		if err := q.Schedule(ctx, state.Task{ID: "reminder-" + id, Scope: scope, Name: "remind", Payload: json.RawMessage(`{"record":{"user":"` + id + `"}}`), RunAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := q.ClaimDue(ctx, now, now.Add(time.Minute), "test", 2)
	if err != nil || len(claims) != 2 {
		t.Fatal(claims, err)
	}
	for _, claim := range claims {
		if err := q.Release(ctx, claim, time.Time{}, "failed"); err != nil {
			t.Fatal(err)
		}
	}
	rep, err = e.rt.MemberData(ctx, server, a, "erase")
	if err != nil || rep.Tasks != 1 {
		t.Fatal(rep, err)
	}
	verify()
	dead, err := q.DeadLetters(ctx, server, 10)
	if err != nil || len(dead) != 1 || dead[0].ID != "reminder-"+b {
		t.Fatal(dead, err)
	}
	rep, err = e.rt.MemberData(ctx, server, a, "export")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(rep)
	if strings.Contains(string(data), a) || strings.Contains(string(data), "private A") {
		t.Fatal(string(data))
	}
	// The erasure settled: A is a returning member, and new activity that
	// mentions A is delivered normally (SEC-P-01). It is not a replay.
	seed()
	delivered := false
	erased, err := e.rt.WithReplayPrivacy(ctx, server, scope, json.RawMessage(`{"body":"<@`+a+`>"}`), func() error { delivered = true; return nil })
	if err != nil || erased || !delivered {
		t.Fatalf("returning member: erased=%v delivered=%v %v", erased, delivered, err)
	}
	// A restart does not erase the returning member's new data again.
	rev, _ := e.rt.Revision()
	if err := e.rt.Activate(ctx, rev, true); err != nil {
		t.Fatal(err)
	}
	if raw, err := kv.Get(ctx, "ticket:1"); err != nil || !strings.Contains(string(raw), "private A") {
		t.Fatalf("restart erased new data of a returning member: %s %v", raw, err)
	}
	// Restore a pre-erasure SQL snapshot: the SQL marker is gone, the external one
	// stays. Startup runs hooks before onStart/routing.
	if err := e.rt.privacyKV(server).Delete(ctx, e.rt.fingerprint(server, a)); err != nil {
		t.Fatal(err)
	}
	seed()
	if err := q.Schedule(ctx, state.Task{ID: "restored-" + a, Scope: scope, Name: "remind", Payload: json.RawMessage(`{"record":{"user":"` + a + `"}}`), RunAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := e.rt.Activate(ctx, rev, true); err != nil {
		t.Fatal(err)
	}
	verify()
	tasks, err := q.Due(ctx, now.Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == "restored-"+a {
			t.Fatal("restored pre-erasure task survived the restart")
		}
	}
	// The installation and the server-wide stores were cleaned at startup, so the
	// returning member's new work flows again, and a later restart keeps it.
	for _, sc := range []string{scope, ""} {
		if gated, err := e.rt.ErasedPayload(ctx, server, sc, json.RawMessage(`"`+a+`"`)); err != nil || gated {
			t.Fatalf("restored database still gates %q: %v %v", sc, gated, err)
		}
	}
	replies := e.rt.Store().KV("private-replies:" + server)
	if err := replies.Set(ctx, "user:"+a+":private-command:new", json.RawMessage(`{"ciphertext":"sealed"}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := kv.Set(ctx, "ticket:1", snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.rt.Activate(ctx, rev, true); err != nil {
		t.Fatal(err)
	}
	if _, err := replies.Get(ctx, "user:"+a+":private-command:new"); err != nil {
		t.Fatal("restart erased a returning member's new private reply", err)
	}
	if raw, err := kv.Get(ctx, "ticket:1"); err != nil || !strings.Contains(string(raw), "private A") {
		t.Fatalf("restart after restore erased new data: %s %v", raw, err)
	}
	marker := filepath.Join(e.rt.markerDirectory(server), e.rt.fingerprint(server, a))
	stamp, err := os.ReadFile(marker)
	if err != nil || strings.Contains(string(stamp), a) || strings.Contains(marker, a) {
		t.Fatal("journal contains raw member ID or is absent", err)
	}
}

// withPrivacyWelcome gives the example welcome plugin a privacy inventory and
// hooks over a shared "ticket:1" record and an "xp" ranking.
func withPrivacyWelcome(t *testing.T) func(string) {
	return func(dir string) {
		manifest := filepath.Join(dir, "plugins/welcome/plugin.yaml")
		raw, err := os.ReadFile(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "  privacy:") {
			raw = append(raw, []byte("\n  privacy:\n    records:\n      - {name: ticket, policy: anonymize, description: Removes the subject and clears their message text}\n")...)
			if err := os.WriteFile(manifest, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		code := `import {definePlugin} from "@cicord/sdk"; export default definePlugin({
    privacy:{
      async export(ctx,id){return ((await ctx.kv.get("ticket:1"))??[]).filter(m=>m.user===id);},
      async erase(ctx,id){
        try { await ctx.privacy.pseudonym(id+"1"); throw Error("arbitrary pseudonym accepted"); } catch(e) { if (!String(e).includes("scoped")) throw e; }
        const alias=await ctx.privacy.pseudonym(id);
        const old=(await ctx.kv.get("ticket:1"))??[];
        const messages=old.map(m=>m.user===id?{user:alias,text:""}:m);
        await ctx.kv.atomic().set("ticket:1",messages).rankRemove("xp",id).commit();
        return {erased:old.filter(m=>m.user===id).length};
      }
    }
  });`
		if err := os.WriteFile(filepath.Join(dir, "plugins/welcome/src/index.ts"), []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
