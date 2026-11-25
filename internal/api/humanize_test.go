package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/runtime"
)

const rawCommunity = `reconcile my-community: 1 change(s) failed: create channel news: discord CreateChannel: discord: bad request ` +
	`(HTTP 400 50035: Invalid Form Body {"type":{"_errors":[{"code":"BASE_TYPE_CHOICES","message":"Value must be one of {0, 2}."}]}})`

func TestHumanStatusJSON(t *testing.T) {
	st := runtime.Status{
		Version: "1", Mode: "dev", LastError: `revision abc rejected: bots/x/bot.yaml: unknown role "xbox"`,
		Servers: []runtime.ServerStatus{
			{Key: "my-community", Health: runtime.HealthDegraded, Sync: runtime.SyncOutOfSync, Message: rawCommunity, Drift: []runtime.DriftEntry{}},
			{Key: "other", Health: runtime.HealthHealthy, Message: "2 change(s) blocked", Drift: []runtime.DriftEntry{}},
		},
		Bots: []runtime.BotStatus{{Key: "b", Health: runtime.HealthMissing, Message: "not connected", Servers: []string{}, Plugins: []runtime.PluginStatus{}}},
	}
	data, err := json.Marshal(humanStatus(st))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Version       string `json:"version"`
		LastError     string `json:"lastError"`
		LastErrorInfo *reconcile.HumanError
		Servers       []struct {
			Key     string                `json:"key"`
			Message string                `json:"message"`
			Error   *reconcile.HumanError `json:"error"`
		} `json:"servers"`
		Bots []struct {
			Key   string                `json:"key"`
			Error *reconcile.HumanError `json:"error"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != "1" || out.LastError == "" || out.LastErrorInfo == nil || out.LastErrorInfo.Code != reconcile.ErrCodeRevisionRejected {
		t.Fatalf("top level = %s", data)
	}
	if len(out.Servers) != 2 || out.Servers[0].Message != rawCommunity || out.Servers[0].Error == nil ||
		out.Servers[0].Error.Code != reconcile.ErrCodeCommunityRequired || out.Servers[0].Error.Details != rawCommunity {
		t.Fatalf("servers = %s", data)
	}
	if out.Servers[1].Error != nil {
		t.Fatalf("a healthy server has no error object: %s", data)
	}
	if len(out.Bots) != 1 || out.Bots[0].Error == nil || out.Bots[0].Error.Code != reconcile.ErrCodeBotNotConnected {
		t.Fatalf("bots = %s", data)
	}
}

func TestHumanPlanAndFailure(t *testing.T) {
	p := humanPlan(reconcile.Plan{Server: "s", Blocked: []reconcile.Blocked{{Action: "delete", Kind: "role", Key: "old", Reason: "deletionProtection"}}})
	data, _ := json.Marshal(p)
	var out struct {
		Changes []any `json:"changes"`
		Blocked []struct {
			Reason string                `json:"reason"`
			Error  *reconcile.HumanError `json:"error"`
		} `json:"blocked"`
	}
	_ = json.Unmarshal(data, &out)
	if out.Changes == nil || len(out.Blocked) != 1 || out.Blocked[0].Reason != "deletionProtection" ||
		out.Blocked[0].Error == nil || out.Blocked[0].Error.Code != reconcile.ErrCodeDeletionProtection {
		t.Fatalf("plan = %s", data)
	}

	w := httptest.NewRecorder()
	failHuman(w, 503, "unavailable", "sync: ", errors.New(rawCommunity))
	var body struct {
		Error struct{ Code, Message, Hint, Details, Reason string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 503 || body.Error.Code != "unavailable" || body.Error.Reason != reconcile.ErrCodeCommunityRequired ||
		body.Error.Hint == "" || body.Error.Details != "sync: "+rawCommunity {
		t.Fatalf("failure = %d %s", w.Code, w.Body.String())
	}
}
