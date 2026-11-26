package sim

import (
	"context"
	"errors"
	"github.com/cicord/cicord/internal/discord"
	"testing"
)

func TestExtendedMessaging(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	s := f.sess
	ctx := context.Background()
	ch := f.general.ID
	m, err := s.SendMessage(ctx, ch, discord.MessageCreate{Content: "hello", Nonce: "retry", EnforceNonce: true})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SendMessage(ctx, ch, discord.MessageCreate{Content: "hello", Nonce: "retry", EnforceNonce: true})
	if err != nil || again.ID != m.ID {
		t.Fatal(again, err)
	}
	if err = s.SetMessagePin(ctx, ch, m.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = f.w.AddReaction(f.alice.ID, ch, m.ID, "⭐"); err != nil {
		t.Fatal(err)
	}
	if err = s.AddReaction(ctx, ch, m.ID, "⭐"); err != nil {
		t.Fatal(err)
	}
	pins, err := s.ChannelPins(ctx, ch)
	if err != nil || len(pins) != 1 || pins[0].Reactions[0].Count != 2 || !pins[0].Reactions[0].Me {
		t.Fatal(pins, err)
	}
	users, err := s.ReactionUsers(ctx, ch, m.ID, "⭐", 1, "")
	if err != nil || len(users) != 1 {
		t.Fatal(users, err)
	}
	if err = s.RemoveUserReaction(ctx, ch, m.ID, "⭐", f.alice.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, ch, m.ID)
	if err != nil || got.Reactions[0].Count != 1 {
		t.Fatal(got, err)
	}
	if err = s.Crosspost(ctx, ch, m.ID); !errors.Is(err, discord.ErrBadRequest) {
		t.Fatal(err)
	}
	if err = s.Typing(ctx, ch); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		x, err := s.SendAs(ctx, ch, discord.WebhookIdentity{Username: "News"}, discord.MessageCreate{Content: "update"})
		if err != nil || x.WebhookID == "" || x.Author.Username != "News" {
			t.Fatal(x, err)
		}
	}
	if n := f.w.CountCalls(f.bot.ID, "CreateWebhook"); n != 1 {
		t.Fatalf("created %d webhooks", n)
	}
}
