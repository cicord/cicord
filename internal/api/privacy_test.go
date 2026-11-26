package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
)

func TestMemberPrivacyAdministration(t *testing.T) {
	e := start(t)
	path := "/api/v1/servers/" + serverKey + "/privacy/"
	body := map[string]string{"userId": "123456781234567812"}
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	mod := e.client()
	mod.login(e.moderator().ID)
	for _, action := range []string{"export", "erase"} {
		mod.expect(403, "POST", path+action, body)
	}
	admin.noCSRF = true
	admin.expect(403, "POST", path+"erase", body)
	admin.noCSRF = false
	for _, id := range []string{"not-an-id", "7", "1234567812345678"} {
		admin.expect(400, "POST", path+"erase", map[string]string{"userId": id})
	}
	admin.expect(404, "POST", path+"typo", body)
	for _, action := range []string{"export", "erase"} {
		resp := admin.expect(200, "POST", path+action, body)
		if !strings.Contains(resp.header.Get("Cache-Control"), "no-store") || !strings.Contains(string(resp.body), "moderator/welcome") {
			t.Fatal(string(resp.body), resp.header)
		}
	}
	audit := admin.expect(200, "GET", "/api/v1/servers/"+serverKey+"/audit", nil)
	if strings.Contains(string(audit.body), body["userId"]) || !strings.Contains(string(audit.body), "privacy.erase") {
		t.Fatal(string(audit.body))
	}
}

// failingStore makes the private-reply store fail once armed, after the plugin
// erase hooks already ran.
type failingStore struct {
	state.Store
	armed *atomic.Bool
}

func (s failingStore) KV(scope string) state.KV {
	kv := s.Store.KV(scope)
	if strings.HasPrefix(scope, "private-replies:") && s.armed.Load() {
		return failingKV{kv}
	}
	return kv
}

type failingKV struct{ state.KV }

func (failingKV) List(context.Context, string, int, string) ([]state.KVItem, string, error) {
	return nil, "", errors.New("disk I/O error")
}

// SEC-A-02: an erase that fails part-way is audited as incomplete and the
// partial report is returned, so the admin knows to retry.
func TestMemberErasePartialFailureIsAudited(t *testing.T) {
	armed := new(atomic.Bool)
	e := startFull(t, nil, func(o *runtime.Options) { o.Store = failingStore{o.Store, armed} }, nil)
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	const member = "123456781234567812"
	armed.Store(true)
	resp := admin.expect(503, "POST", "/api/v1/servers/"+serverKey+"/privacy/erase", map[string]string{"userId": member})
	var out struct {
		Error  struct{ Code string }
		Report runtime.PrivacyReport
	}
	resp.json(t, &out)
	if out.Error.Code != "unavailable" || len(out.Report.Plugins) == 0 {
		t.Fatalf("partial report missing: %s", resp.body)
	}
	armed.Store(false)
	audit := admin.expect(200, "GET", "/api/v1/servers/"+serverKey+"/audit", nil)
	if strings.Contains(string(audit.body), member) || !strings.Contains(string(audit.body), "privacy.erase") || !strings.Contains(string(audit.body), "failed") {
		t.Fatalf("partial erase not audited: %s", audit.body)
	}
	// The retry completes.
	admin.expect(200, "POST", "/api/v1/servers/"+serverKey+"/privacy/erase", map[string]string{"userId": member})
}

// MJ4: an uninstalled installation's data is listed and can be purged by an
// admin, with the installation name repeated as confirmation.
func TestOrphanedDataPurge(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	scope := "plugin:moderator/removed/" + serverKey
	if err := e.rt.Store().KV(scope).Set(ctx, "member:123456781234567812", json.RawMessage(`{"xp":1}`), 0); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/servers/" + serverKey + "/orphaned-data"
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	mod := e.client()
	mod.login(e.moderator().ID)
	mod.expect(403, "GET", base, nil)
	mod.expect(403, "POST", base+"/purge", map[string]string{"instance": "moderator/removed", "confirm": "moderator/removed"})
	list := admin.expect(200, "GET", base, nil)
	var listed struct {
		Items []runtime.OrphanedInstance `json:"items"`
	}
	list.json(t, &listed)
	if len(listed.Items) != 1 || listed.Items[0].Instance != "moderator/removed" || listed.Items[0].Records != 1 {
		t.Fatalf("%s", list.body)
	}
	admin.expect(400, "POST", base+"/purge", map[string]string{"instance": "moderator/removed"})
	admin.expect(400, "POST", base+"/purge", map[string]string{"instance": "moderator/removed", "confirm": "moderator/welcome"})
	admin.expect(409, "POST", base+"/purge", map[string]string{"instance": "moderator/welcome", "confirm": "moderator/welcome"})
	purged := admin.expect(200, "POST", base+"/purge", map[string]string{"instance": "moderator/removed", "confirm": "moderator/removed"})
	var usage state.ScopeUsage
	purged.json(t, &usage)
	if usage.Records != 1 {
		t.Fatalf("%s", purged.body)
	}
	list = admin.expect(200, "GET", base, nil)
	list.json(t, &listed)
	if len(listed.Items) != 0 {
		t.Fatalf("%s", list.body)
	}
	if _, err := e.rt.Store().KV(scope).Get(ctx, "member:123456781234567812"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("orphaned data survived", err)
	}
	audit := admin.expect(200, "GET", "/api/v1/servers/"+serverKey+"/audit", nil)
	if !strings.Contains(string(audit.body), "plugin.data.purge") {
		t.Fatal(string(audit.body))
	}
}
