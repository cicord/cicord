package runtime

import (
	"bufio"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/metrics"
)

// scrapeMetrics parses the exposition of reg into "series → value".
func scrapeMetrics(t *testing.T, reg *metrics.Registry) map[string]float64 {
	t.Helper()
	var b strings.Builder
	if err := reg.Write(&b); err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			t.Fatalf("bad sample %q", line)
		}
		out[line[:i]] = v
	}
	return out
}

func TestRuntimeMetrics(t *testing.T) {
	e := startExample(t)
	w := e.world
	reg := e.rt.opts.Metrics
	welcome := `bot="moderator",server="ma-commu",plugin="welcome",plugin_instance="welcome"`

	alice := w.CreateUser("alice")
	if err := w.AddMember(e.serverID, alice.ID); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the welcome handler", func() bool {
		return scrapeMetrics(t, reg)[`cicord_plugin_handler_duration_seconds_count{`+welcome+`,kind="event"}`] >= 1
	})

	m := scrapeMetrics(t, reg)
	rev, _ := e.rt.Revision()
	for series, want := range map[string]float64{
		`cicord_revision_active`: 1,
		`cicord_revision_info{revision="` + rev.ID + `",source="local"}`:          1,
		`cicord_revision_activations_total{result="activated"}`:                   1,
		`cicord_reconcile_syncs_total{server="ma-commu",result="succeeded"}`:      1,
		`cicord_server_last_sync_success{server="ma-commu"}`:                      1,
		`cicord_server_out_of_sync{server="ma-commu"}`:                            0,
		`cicord_server_health{server="ma-commu",health="Healthy"}`:                1,
		`cicord_server_health{server="ma-commu",health="Missing"}`:                0,
		`cicord_bot_gateway_connected{bot="moderator"}`:                           1,
		`cicord_plugin_instance_running{` + welcome + `}`:                         1,
		`cicord_reconcile_changes_failed_total{server="ma-commu",trigger="sync"}`: 0,
	} {
		if got, ok := m[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}
	if m[`cicord_reconcile_changes_applied_total{server="ma-commu",trigger="sync"}`] == 0 {
		t.Error("the first sync created channels and roles: applied changes must be counted")
	}
	if m[`cicord_server_last_sync_timestamp_seconds{server="ma-commu"}`] == 0 || m[`cicord_revision_activated_timestamp_seconds`] == 0 {
		t.Error("timestamps are not set")
	}
	if _, ok := m[`cicord_plugin_handler_errors_total{`+welcome+`,kind="event"}`]; ok {
		t.Error("a successful handler counted as an error")
	}
	for series := range m {
		if strings.Contains(series, alice.ID) {
			t.Errorf("a user id leaked into a label: %s", series)
		}
	}

	// An invalid revision is counted as rejected.
	replaceIn(t, filepath.Join(e.dir, "bots/moderator/bot.yaml"), "channel: general", "channel: nowhere")
	e.waitFor("rejection", func() bool {
		return scrapeMetrics(t, reg)[`cicord_revision_activations_total{result="rejected"}`] == 1
	})
}
