package runtime

import (
	"context"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

type cacheRefreshClient struct {
	discord.Client
	channel discord.Channel
	role    discord.Role
	blocked chan struct{}
	proceed chan struct{}
}

func (c cacheRefreshClient) ListChannels(context.Context, string) ([]discord.Channel, error) {
	return []discord.Channel{c.channel}, nil
}
func (c cacheRefreshClient) ListRoles(ctx context.Context, _ string) ([]discord.Role, error) {
	close(c.blocked)
	select {
	case <-c.proceed:
		return []discord.Role{c.role}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (c cacheRefreshClient) GetServer(context.Context, string) (discord.Server, error) {
	return discord.Server{ID: "1", OwnerID: "2"}, nil
}

type emptyResourceMap struct{ state.ResourceMap }

func (emptyResourceMap) List(context.Context, string) ([]state.Resource, error) { return nil, nil }

func TestRefreshPreservesConcurrentChannelDeny(t *testing.T) {
	c := newServerCache("guild", "1")
	base := discord.Channel{ID: "10", ServerID: "1", Type: discord.ChannelText}
	read := discord.MustPermissions("ViewChannel", "ReadMessageHistory")
	role := discord.Role{ID: "1", Permissions: read}
	cl := cacheRefreshClient{channel: base, role: role, blocked: make(chan struct{}), proceed: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- c.Refresh(t.Context(), cl, emptyResourceMap{}) }()
	<-cl.blocked
	denied := base
	denied.Overwrites = []discord.Overwrite{{ID: "1", Type: discord.OverwriteRole, Deny: read}}
	c.Observe(discord.ChannelUpdateEvent{Channel: denied})
	c.Observe(discord.ServerUpdateEvent{Server: discord.Server{ID: "1", OwnerID: "3"}})
	close(cl.proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	roles, channels := c.Snapshot()
	if len(channels) != 1 || c.Server().OwnerID != "3" {
		t.Fatalf("stale cache: %+v %+v", channels, c.Server())
	}
	p := discord.NewAudience(channels[0], roles)
	bits, known := p.Permissions("2", nil, c.Server().OwnerID, false)
	if !known || bits.Has(read) {
		t.Fatal("REST refresh rolled back gateway revocation")
	}
}
