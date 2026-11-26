package runtime

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// SEC-P-01: only snowflake-shaped strings are member IDs. An erasure of "7"
// is refused, and counts or votes never match an erased member.
func TestPrivacySubjectsAreSnowflakes(t *testing.T) {
	for id, ok := range map[string]bool{"7": false, "1234567812345678": false, "12345678901234567": true, "111111111111111111": true, "12345678901234567890": true, "123456789012345678901": false, "0123456789012345678": false} {
		if ValidPrivacyMember(id) != ok {
			t.Errorf("ValidPrivacyMember(%q) = %v", id, !ok)
		}
	}
	var got []string
	subjects("member:111111111111111111", json.RawMessage(`{"votes":7,"big":222222222222222222,"text":"hi <@333333333333333333> v7","n":"4444"}`), func(id string) { got = append(got, id) })
	slices.Sort(got)
	if !slices.Equal(got, []string{"111111111111111111", "333333333333333333"}) {
		t.Fatal(got)
	}
}

func TestErasureMarkGates(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stamp, _ := json.Marshal(at)
	settled, _ := json.Marshal(erasureMark{At: at, Settled: true, Pending: []string{"plugin:b/off/s"}})
	cases := []struct {
		name    string
		sql     []byte
		file    time.Time
		hasFile bool
		gated   []string // scopes gated; others are not ("" = server-wide)
	}{
		{"settled", settled, at, true, []string{"plugin:b/off/s"}},
		{"settled without journal", settled, time.Time{}, false, []string{"plugin:b/off/s"}},
		{"legacy date", stamp, at, true, []string{"", "plugin:b/on/s", "plugin:b/off/s"}},
		{"restored database", nil, at, true, []string{"", "plugin:b/on/s", "plugin:b/off/s"}},
		{"older database", settled, at.Add(time.Hour), true, []string{"", "plugin:b/on/s", "plugin:b/off/s"}},
		{"unreadable", []byte(`[`), at, true, []string{"", "plugin:b/on/s", "plugin:b/off/s"}},
	}
	for _, c := range cases {
		m, ok := combineMark(c.sql, c.file, c.hasFile)
		if !ok {
			t.Fatal(c.name, "no marker")
		}
		for _, scope := range []string{"", "plugin:b/on/s", "plugin:b/off/s"} {
			if m.gates(scope) != slices.Contains(c.gated, scope) {
				t.Errorf("%s: gates(%q) = %v", c.name, scope, m.gates(scope))
			}
		}
	}
	if _, ok := combineMark(nil, time.Time{}, false); ok {
		t.Fatal("marker without any record")
	}
	m, _ := combineMark(nil, at, true)
	m.cleaned("plugin:b/on/s")
	if m.gates("plugin:b/on/s") || !m.gates("plugin:b/off/s") || !m.gates("") {
		t.Fatal("restore progress", m)
	}
	m, _ = combineMark(settled, at, true)
	m.cleaned("plugin:b/off/s")
	if m.gates("plugin:b/off/s") {
		t.Fatal("pending scope not cleared", m)
	}
}

// SEC-P-01 regression with the reviewer's scenarios: erasing "7" is refused, and
// a returning member's new work is delivered once the erasure settled.
func TestErasureDoesNotBlockReturningMember(t *testing.T) {
	e := startExampleResync(t, nil, nil, -1)
	ctx := t.Context()
	const server, a = "my-community", "111111111111111111"
	scope := "plugin:moderator/welcome/" + server
	if _, err := e.rt.MemberData(ctx, server, "7", "erase"); err == nil {
		t.Fatal("erased a non-snowflake member ID")
	}
	if _, err := e.rt.MemberData(ctx, server, a, "erase"); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"pollId":"abc","votes":7}`, `{"userId":"` + a + `","xp":10}`} {
		delivered := false
		erased, err := e.rt.WithReplayPrivacy(ctx, server, scope, json.RawMessage(payload), func() error { delivered = true; return nil })
		if err != nil || erased || !delivered {
			t.Fatalf("%s: erased=%v delivered=%v %v", payload, erased, delivered, err)
		}
	}
	if gated, err := e.rt.ErasedPayload(ctx, server, "", json.RawMessage(`"`+a+`"`)); err != nil || gated {
		t.Fatal("settled erasure gates server-wide data", gated, err)
	}
}

// MJ4: an uninstalled installation that still holds the member's data makes the
// report incomplete ("orphaned") and keeps gating that scope until an admin
// purges it. The purge refuses installed instances.
func TestOrphanedInstallationData(t *testing.T) {
	e := startExampleResync(t, nil, nil, -1)
	ctx := t.Context()
	const server, a, b = "my-community", "111111111111111111", "222222222222222222"
	orphan := "plugin:moderator/removed/" + server
	kv := e.rt.Store().KV(orphan).(state.AdvancedKV)
	if err := kv.Set(ctx, "member:"+a, json.RawMessage(`{"xp":5}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Rank(ctx, state.RankOp{Name: "xp", Member: b, Method: "set", Score: 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.rt.Store().KV("dm-receipts:moderator/removed/"+server).Set(ctx, "r", json.RawMessage(`1`), 0); err != nil {
		t.Fatal(err)
	}
	items, err := e.rt.OrphanedData(ctx, server)
	if err != nil || len(items) != 1 || items[0].Instance != "moderator/removed" || items[0].Records != 1 || items[0].Rankings != 1 {
		t.Fatalf("orphans %+v %v", items, err)
	}
	rep, err := e.rt.MemberData(ctx, server, a, "erase")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Complete || !slices.ContainsFunc(rep.Plugins, func(p PrivacyItem) bool { return p.Instance == "moderator/removed" && p.Status == "orphaned" }) {
		t.Fatalf("orphaned data missing from the report: %+v", rep)
	}
	// A ranking member counts as data of the orphan too.
	rep, err = e.rt.MemberData(ctx, server, b, "export")
	if err != nil || rep.Complete || !slices.ContainsFunc(rep.Plugins, func(p PrivacyItem) bool { return p.Status == "orphaned" }) {
		t.Fatalf("ranking member not found in orphan: %+v %v", rep, err)
	}
	if rep, err := e.rt.MemberData(ctx, server, "333333333333333333", "export"); err != nil || slices.ContainsFunc(rep.Plugins, func(p PrivacyItem) bool { return p.Status == "orphaned" }) {
		t.Fatalf("orphan listed for a member without data there: %+v %v", rep, err)
	}
	if gated, _ := e.rt.ErasedPayload(ctx, server, orphan, json.RawMessage(`"`+a+`"`)); !gated {
		t.Fatal("orphan scope not pending")
	}
	if _, err := e.rt.PurgeOrphanedData(ctx, server, "moderator/welcome"); !errors.Is(err, ErrInstalled) {
		t.Fatal("purged an installed instance", err)
	}
	if _, err := e.rt.PurgeOrphanedData(ctx, server, "moderator/removed/x"); err == nil {
		t.Fatal("accepted a malformed name")
	}
	usage, err := e.rt.PurgeOrphanedData(ctx, server, "moderator/removed")
	if err != nil || usage.Records != 2 || usage.Rankings != 1 {
		t.Fatalf("purge %+v %v", usage, err)
	}
	if items, err := e.rt.OrphanedData(ctx, server); err != nil || len(items) != 0 {
		t.Fatalf("orphans after purge %+v %v", items, err)
	}
	if gated, _ := e.rt.ErasedPayload(ctx, server, orphan, json.RawMessage(`"`+a+`"`)); gated {
		t.Fatal("purge did not settle the marker")
	}
	rep, err = e.rt.MemberData(ctx, server, a, "export")
	if err != nil || slices.ContainsFunc(rep.Plugins, func(p PrivacyItem) bool { return strings.Contains(p.Instance, "removed") }) {
		t.Fatalf("report after purge %+v %v", rep, err)
	}
}

// An installation disabled at erasure time keeps the member's data: its scope
// stays pending, and the erase hook runs when it is enabled again. After that, a
// restart does not erase the returning member's new data a second time.
func TestErasureOfDisabledInstallation(t *testing.T) {
	e := startExampleResync(t, nil, withPrivacyWelcome(t), -1)
	ctx := t.Context()
	const server, a = "my-community", "111111111111111111"
	scope := "plugin:moderator/welcome/" + server
	kv := e.rt.Store().KV(scope)
	ticket := json.RawMessage(`[{"user":"` + a + `","text":"private A"}]`)
	if err := kv.Set(ctx, "ticket:1", ticket, 0); err != nil {
		t.Fatal(err)
	}
	healthy := func(want string) func() bool {
		return func() bool {
			for _, i := range e.rt.Instances(server) {
				if i.Config.Name == "welcome" {
					return i.Health == want
				}
			}
			return false
		}
	}
	bot := filepath.Join(e.dir, "bots/moderator/bot.yaml")
	replaceIn(t, bot, "        channel: general", "        enabled: false\n        channel: general")
	e.rt.Trigger()
	e.waitFor("installation disabled", healthy(HealthDisabled))
	rep, err := e.rt.MemberData(ctx, server, a, "erase")
	if err != nil || rep.Complete || !slices.ContainsFunc(rep.Plugins, func(p PrivacyItem) bool { return p.Instance == "moderator/welcome" && p.Status == "disabled" }) {
		t.Fatalf("%+v %v", rep, err)
	}
	if gated, _ := e.rt.ErasedPayload(ctx, server, scope, json.RawMessage(`"`+a+`"`)); !gated {
		t.Fatal("disabled installation not pending")
	}
	if gated, _ := e.rt.ErasedPayload(ctx, server, "plugin:moderator/onboarding/"+server, json.RawMessage(`"`+a+`"`)); gated {
		t.Fatal("erased installation still gated")
	}
	replaceIn(t, bot, "        enabled: false\n", "")
	e.rt.Trigger()
	e.waitFor("installation enabled", healthy(HealthHealthy))
	raw, err := kv.Get(ctx, "ticket:1")
	if err != nil || strings.Contains(string(raw), "private A") {
		t.Fatalf("data of the re-enabled installation not erased: %s %v", raw, err)
	}
	if gated, _ := e.rt.ErasedPayload(ctx, server, scope, json.RawMessage(`"`+a+`"`)); gated {
		t.Fatal("cleaned installation still pending")
	}
	if err := kv.Set(ctx, "ticket:1", ticket, 0); err != nil {
		t.Fatal(err)
	}
	rev, _ := e.rt.Revision()
	if err := e.rt.Activate(ctx, rev, true); err != nil {
		t.Fatal(err)
	}
	if raw, err := kv.Get(ctx, "ticket:1"); err != nil || !strings.Contains(string(raw), "private A") {
		t.Fatalf("restart erased a returning member's new data: %s %v", raw, err)
	}
}
