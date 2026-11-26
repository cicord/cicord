package cli

import (
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/runtime"
)

// A plugin installation disabled by configuration (enabled: false) is a declared state.
// status --wait and healthcheck --service must not wait on it forever (CORE-R11): the
// VPS updater's pre-check and post-update check both rely on them.
func TestStatusWaitTreatsDisabledInstanceAsReady(t *testing.T) {
	st := runtime.Status{
		Servers: []runtime.ServerStatus{{Key: "main", Sync: runtime.SyncSynced, Health: runtime.HealthHealthy}},
		Bots: []runtime.BotStatus{{Key: "main", Health: runtime.HealthHealthy, Plugins: []runtime.PluginStatus{
			{Instance: "welcome", Server: "main", Health: runtime.HealthHealthy},
			{Instance: "levels", Server: "main", Health: runtime.HealthDisabled},
		}}},
	}
	if hasFailures(st) {
		t.Fatal("a disabled instance is not a failure")
	}
	if !ready(st) {
		t.Fatal("a disabled instance must not keep status --wait pending")
	}
	if p := pendingServers(st); strings.Contains(p, "levels") {
		t.Fatalf("pending lists the disabled instance: %q", p)
	}
	st.Bots[0].Plugins[0].Health = runtime.HealthProgressing
	if ready(st) {
		t.Fatal("a starting instance is still pending")
	}
	if p := pendingServers(st); !strings.Contains(p, "welcome") {
		t.Fatalf("pending omits the starting instance: %q", p)
	}
}

// CORE-04: the unconfirmed-activity alert is shown in cicord status but never blocks
// --wait (it is advisory).
func TestStatusShowsUnconfirmedAuditWithoutBlocking(t *testing.T) {
	st := runtime.Status{
		Servers: []runtime.ServerStatus{{Key: "main", Sync: runtime.SyncSynced, Health: runtime.HealthHealthy,
			UnconfirmedAudit: &runtime.UnconfirmedAudit{Bot: "main", Count: 2, LastAction: "webhook.create"}}},
		Bots: []runtime.BotStatus{{Key: "main", Health: runtime.HealthHealthy}},
	}
	if !ready(st) || hasFailures(st) {
		t.Fatal("the CORE-04 advisory must not fail or block --wait")
	}
	var out strings.Builder
	printStatus(&out, st)
	if !strings.Contains(out.String(), "2 audited action(s) by bot main") || !strings.Contains(out.String(), "token may be compromised") {
		t.Fatalf("status output: %s", out.String())
	}
}
