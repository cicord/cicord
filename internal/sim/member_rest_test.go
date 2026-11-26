package sim

import (
	"context"
	"errors"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

// Discord sends the author's member on gateway MESSAGE_CREATE only, never in REST message
// objects (fetch, list, pins, send and edit results). The simulator does the same.
func TestMessageMemberOnlyOnGateway(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	ctx := context.Background()
	m, err := f.w.SendMessage(f.alice.ID, f.general.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	f.w.WaitIdle()
	var created *discord.Message
	for _, e := range eventsOf[discord.MessageCreateEvent](f.rec) {
		if e.Message.ID == m.ID {
			created = &e.Message
		}
	}
	if created == nil || created.Member == nil || created.Member.User.ID != f.alice.ID {
		t.Fatalf("gateway message lacks the author's member: %+v", created)
	}
	got, err := f.sess.GetMessage(ctx, f.general.ID, m.ID)
	if err != nil || got.Member != nil {
		t.Fatalf("fetched message carries a member: %+v, %v", got.Member, err)
	}
	list, err := f.sess.ListMessages(ctx, f.general.ID, discord.ListMessagesParams{Limit: 10})
	if err != nil || len(list) == 0 {
		t.Fatal(list, err)
	}
	for _, l := range list {
		if l.Member != nil {
			t.Fatalf("listed message carries a member: %+v", l)
		}
	}
	sent, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "bot"})
	if err != nil || sent.Member != nil {
		t.Fatalf("send result carries a member: %+v, %v", sent.Member, err)
	}
	edited, err := f.sess.EditMessage(ctx, f.general.ID, sent.ID, discord.MessageCreate{Content: "edited"})
	if err != nil || edited.Member != nil {
		t.Fatalf("edit result carries a member: %+v, %v", edited.Member, err)
	}
}

// A test can drop a permission of the bot to exercise a plugin's degraded mode.
func TestSetBotPermissions(t *testing.T) {
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ManageChannels", "ManageServer", "MoveMembers")})
	ctx := context.Background()
	f.resetEvents()
	role, err := f.w.SetBotPermissions(f.srv.ID, f.bot.ID, discord.MustPermissions("ManageChannels"))
	if err != nil || role.ID != f.botRole.ID || role.Permissions != discord.MustPermissions("ManageChannels") {
		t.Fatal(role, err)
	}
	f.w.WaitIdle()
	if ups := eventsOf[discord.RoleUpdateEvent](f.rec); len(ups) == 0 || ups[len(ups)-1].Role.Permissions != role.Permissions {
		t.Fatalf("no role update event: %+v", ups)
	}
	if p := f.w.Permissions(f.srv.ID, "", f.bot.ID); p.Has(discord.MustPermissions("ManageServer")) || !p.Has(discord.MustPermissions("ManageChannels")) {
		t.Fatalf("bot permissions = %v", p.Names())
	}
	if _, err := f.sess.ListInviteUses(ctx, f.srv.ID); !errors.Is(err, discord.ErrForbidden) {
		t.Fatalf("invites without ManageServer: %v", err)
	}
	_, other, _ := f.w.CreateBot("other")
	if _, err := f.w.AddBotToServer(f.srv.ID, other.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.w.SetBotPermissions(f.srv.ID, other.ID, 0); !errors.Is(err, discord.ErrBadRequest) {
		t.Fatalf("bot without a managed role: %v", err)
	}
}
