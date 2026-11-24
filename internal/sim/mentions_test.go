package sim

import (
	"reflect"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestAllowedMentionsResolution(t *testing.T) {
	// The bot has SendMessages but not MentionEveryone: non-mentionable roles and
	// @everyone never notify, whatever allowed_mentions says.
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ViewChannel", "SendMessages")})
	// Discord's default @everyone permissions include MentionEveryone: take it away.
	everyone, _ := f.w.Role(f.srv.ID, f.srv.ID)
	_, err := f.w.AdminUpdateRole(f.srv.ID, f.srv.ID, discord.RoleParams{Permissions: ptr(everyone.Permissions &^ discord.MustPermissions("MentionEveryone"))})
	isErr(t, err, nil)
	pub, err := f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("pub"), Mentionable: ptr(true)})
	isErr(t, err, nil)
	pub2, err := f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("pub2"), Mentionable: ptr(true)})
	isErr(t, err, nil)
	staff, err := f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("staff")})
	isErr(t, err, nil)
	content := "<@" + f.alice.ID + "> <@!" + f.owner.ID + "> <@&" + pub.ID + "> <@&" + pub2.ID + "> <@&" + staff.ID + "> @everyone"

	send := func(am *discord.AllowedMentions) Mentions {
		t.Helper()
		m, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: content, AllowedMentions: am})
		isErr(t, err, nil)
		got, ok := f.w.MessageMentions(m.ID)
		if !ok {
			t.Fatal("no mentions recorded")
		}
		return got
	}
	cases := []struct {
		name string
		am   *discord.AllowedMentions
		want Mentions
	}{
		{"default: every mention the author may use", nil,
			Mentions{UserIDs: []string{f.alice.ID, f.owner.ID}, RoleIDs: []string{pub.ID, pub2.ID}}},
		{"none", &discord.AllowedMentions{}, Mentions{}},
		{"only the listed role", &discord.AllowedMentions{RoleIDs: []string{pub.ID}, UserIDs: []string{}},
			Mentions{RoleIDs: []string{pub.ID}}},
		{"listed users, parsed roles", &discord.AllowedMentions{UserIDs: []string{f.alice.ID}, Roles: true, Everyone: true},
			Mentions{UserIDs: []string{f.alice.ID}, RoleIDs: []string{pub.ID, pub2.ID}}},
		{"a listed non-mentionable role does not notify", &discord.AllowedMentions{RoleIDs: []string{staff.ID}}, Mentions{}},
	}
	for _, c := range cases {
		if got := send(c.am); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}

	// Discord's validation: a type both parsed and listed, or a bad id, is a 400.
	for _, am := range []discord.AllowedMentions{
		{Roles: true, RoleIDs: []string{pub.ID}},
		{UserIDs: []string{"alice"}},
	} {
		_, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "x", AllowedMentions: &am})
		isErr(t, err, discord.ErrBadRequest)
	}
}

func TestAllowedMentionsWithMentionEveryone(t *testing.T) {
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ViewChannel", "SendMessages", "MentionEveryone")})
	staff, err := f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("staff")})
	isErr(t, err, nil)
	m, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{
		Content:         "<@&" + staff.ID + "> @here",
		AllowedMentions: &discord.AllowedMentions{RoleIDs: []string{staff.ID}, Everyone: true},
	})
	isErr(t, err, nil)
	got, _ := f.w.MessageMentions(m.ID)
	if want := (Mentions{RoleIDs: []string{staff.ID}, Everyone: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("mentions = %+v, want %+v", got, want)
	}

	// Replies notify the replied author unless allowed_mentions leaves replied_user off.
	orig, err := f.w.SendMessage(f.alice.ID, f.general.ID, "question")
	isErr(t, err, nil)
	r1, _ := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "answer", ReplyTo: orig.ID})
	r2, _ := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "answer", ReplyTo: orig.ID, AllowedMentions: &discord.AllowedMentions{}})
	r3, _ := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "answer", ReplyTo: orig.ID, AllowedMentions: &discord.AllowedMentions{RepliedUser: true}})
	for _, c := range []struct {
		id   string
		want []string
	}{{r1.ID, []string{f.alice.ID}}, {r2.ID, nil}, {r3.ID, []string{f.alice.ID}}} {
		if got, _ := f.w.MessageMentions(c.id); !reflect.DeepEqual(got.UserIDs, c.want) {
			t.Errorf("reply mentions = %v, want %v", got.UserIDs, c.want)
		}
	}
}
