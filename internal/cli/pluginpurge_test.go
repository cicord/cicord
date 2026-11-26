package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/state"
)

// cicord plugin purge-data lists what an uninstalled installation left, asks for
// confirmation, and refuses installations of the active revision.
func TestPluginPurgeData(t *testing.T) {
	url, _, rt := startRuntimeOf(t, "../../examples/basic", nil)
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	t.Setenv("CICORD_RUNTIME_URL", url)
	ctx := t.Context()
	scope := "plugin:moderator/old-tickets/my-community"
	kv := rt.Store().KV(scope)
	for _, key := range []string{"ticket:1", "ticket:2"} {
		if err := kv.Set(ctx, key, json.RawMessage(`{"user":"111111111111111111"}`), 0); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, errOut := run(t, "plugin", "purge-data", "my-community", "bad"); code != 1 || !strings.Contains(errOut, "expected <bot>/<instance>") {
		t.Fatalf("malformed name = %d: %s", code, errOut)
	}
	if code, _, errOut := run(t, "plugin", "purge-data", "my-community", "moderator/welcome", "--yes"); code != 1 || !strings.Contains(errOut, "uninstalled installations with data: moderator/old-tickets") {
		t.Fatalf("installed instance = %d: %s", code, errOut)
	}
	withStdin(t, false, "")
	code, out, errOut := run(t, "plugin", "purge-data", "my-community", "moderator/old-tickets")
	if code != 1 || !strings.Contains(out, "moderator/old-tickets left on my-community: 2 record(s)") || !strings.Contains(errOut, "pass --yes") {
		t.Fatalf("without a terminal = %d\n%s\n%s", code, out, errOut)
	}
	withStdin(t, true, "no\n")
	if code, _, errOut := run(t, "plugin", "purge-data", "my-community", "moderator/old-tickets"); code != 1 || !strings.Contains(errOut, "nothing deleted") {
		t.Fatalf("answered no = %d: %s", code, errOut)
	}
	if _, err := kv.Get(ctx, "ticket:1"); err != nil {
		t.Fatal("deleted without confirmation", err)
	}
	withStdin(t, true, "yes\n")
	code, out, errOut = run(t, "plugin", "purge-data", "my-community", "moderator/old-tickets")
	if code != 0 || !strings.Contains(out, "Deleted the data of moderator/old-tickets on my-community: 2 record(s)") {
		t.Fatalf("purge = %d\n%s\n%s", code, out, errOut)
	}
	if _, err := kv.Get(ctx, "ticket:1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("data survived the purge", err)
	}
	if code, _, errOut := run(t, "plugin", "purge-data", "my-community", "moderator/old-tickets", "--yes", "-o", "json"); code != 1 || !strings.Contains(errOut, "no uninstalled installation holds data on my-community") {
		t.Fatalf("second purge = %d: %s", code, errOut)
	}
}
