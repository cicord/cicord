package disgoadapter

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// CORE-R4 (1) and MOD-01: a call whose target is known is journaled before Discord
// receives it, one operation per audit entry (overwrites included), and withdrawn
// when Discord rejects it. A creation is journaled with the created ID.
func TestAdapterJournalsBeforeTheRequest(t *testing.T) {
	f := newFakeDiscord(t)
	var mu sync.Mutex
	var journal, withdrawn []discord.Operation
	seenAtRequest := -1
	f.handle("PATCH /channels/10", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		seenAtRequest = len(journal)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"10","guild_id":"1","name":"c","type":0}`))
	})
	f.json("PATCH /channels/11", http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`)
	f.json("GET /channels/10/webhooks", 200, `[]`)
	f.json("POST /channels/10/webhooks", 200, `{"id":"77","token":"secret","name":"Cicord","type":1,"user":{"id":"600"}}`)
	s := f.connect(t)
	scope := &discord.OperationScope{Origin: discord.Operation{ServerID: "1", ActorID: "600"},
		Record: func(_ context.Context, op discord.Operation) { mu.Lock(); journal = append(journal, op); mu.Unlock() },
		Forget: func(_ context.Context, op discord.Operation) {
			mu.Lock()
			withdrawn = append(withdrawn, op)
			mu.Unlock()
		}}
	ctx := discord.WithOperations(context.Background(), scope)
	ows := []discord.Overwrite{{ID: "5", Type: discord.OverwriteRole}, {ID: "6", Type: discord.OverwriteMember}}
	if _, err := s.UpdateChannel(ctx, "10", discord.ChannelParams{Overwrites: &ows}, "lock"); err != nil {
		t.Fatal(err)
	}
	if seenAtRequest != 3 {
		t.Fatalf("journal had %d rows when Discord received the request, want 3 (two overwrites, one removal marker)", seenAtRequest)
	}
	for n, id := range []string{"5", "6"} {
		if op := journal[n]; op.Action != discord.AuditOverwriteCreate || op.Target != "10" || op.Detail != id {
			t.Fatalf("overwrite operation %d = %+v", n, op)
		}
	}
	if _, err := s.UpdateChannel(ctx, "11", discord.ChannelParams{Name: ptr("x")}, ""); err == nil {
		t.Fatal("expected Missing Permissions")
	}
	if len(withdrawn) != 1 || withdrawn[0].Target != "11" || withdrawn[0].Action != 11 {
		t.Fatalf("withdrawn = %+v", withdrawn)
	}
	if _, err := s.(*session).channelWebhook(ctx, "10"); err != nil {
		t.Fatal(err)
	}
	if last := journal[len(journal)-1]; last.Action != 50 || last.Target != "77" {
		t.Fatalf("webhook operation = %+v", last)
	}
}

// A call that waited (rate limit, 429 retries) is journaled again at its response, so
// an entry Discord wrote late still falls in the 30 s window.
func TestAdapterJournalsSlowCallsAgain(t *testing.T) {
	old := lateJournal
	lateJournal = 20 * time.Millisecond
	t.Cleanup(func() { lateJournal = old })
	f := newFakeDiscord(t)
	f.handle("PATCH /guilds/1/roles/5", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"5","name":"r"}`))
	})
	s := f.connect(t)
	var journal []discord.Operation
	scope := &discord.OperationScope{Origin: discord.Operation{ServerID: "1", ActorID: "600"},
		Record: func(_ context.Context, op discord.Operation) { journal = append(journal, op) }}
	if _, err := s.UpdateRole(discord.WithOperations(context.Background(), scope), "1", "5", discord.RoleParams{Name: ptr("r")}, ""); err != nil {
		t.Fatal(err)
	}
	if len(journal) != 2 || journal[0].Action != 31 || !journal[1].At.After(journal[0].At.Add(40*time.Millisecond)) {
		t.Fatalf("journal = %+v", journal)
	}
}
