package disgoadapter

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/discord"
)

// TestIntegrationSmoke talks to the real Discord API. It runs only when
// CICORD_TEST_DISCORD_TOKEN and CICORD_TEST_SERVER_ID are set (the bot must be a
// member of that server). It only reads: no server state is modified.
func TestIntegrationSmoke(t *testing.T) {
	token := os.Getenv("CICORD_TEST_DISCORD_TOKEN")
	serverID := os.Getenv("CICORD_TEST_SERVER_ID")
	if token == "" || serverID == "" {
		t.Skip("set CICORD_TEST_DISCORD_TOKEN and CICORD_TEST_SERVER_ID to run the Discord smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s, err := NewConnector().Connect(ctx, token)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	t.Logf("connected as %s (%s), application %s", s.Self().Username, s.Self().ID, s.ApplicationID())

	roles, err := s.ListRoles(ctx, serverID)
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	everyone := false
	for _, r := range roles {
		everyone = everyone || r.IsEveryone(serverID)
	}
	if !everyone {
		t.Errorf("@everyone not found among %d roles", len(roles))
	}
	channels, err := s.ListChannels(ctx, serverID)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	t.Logf("%d roles, %d channels", len(roles), len(channels))

	ready := make(chan discord.ReadyEvent, 1)
	err = s.Open(ctx, discord.MustIntents("Servers"), func(e discord.Event) {
		if r, ok := e.(discord.ReadyEvent); ok {
			select {
			case ready <- r:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	select {
	case r := <-ready:
		if r.User.ID != s.Self().ID {
			t.Errorf("ready user %s, want %s", r.User.ID, s.Self().ID)
		}
		found := false
		for _, id := range r.ServerIDs {
			found = found || id == serverID
		}
		if !found {
			t.Errorf("server %s not in READY server list %v", serverID, r.ServerIDs)
		}
	case <-ctx.Done():
		t.Fatal("no READY event received")
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelClose()
	if err := s.Close(closeCtx); err != nil {
		t.Errorf("close: %v", err)
	}
}
