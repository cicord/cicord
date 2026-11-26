package sim

import (
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// Exercise the same conversation as two identities. The timeout masks the
// member's effective permissions, including permissions granted by a personal
// overwrite. It does not grant access to staff channels or affect a bot DM.
func TestMemberTimeoutAcrossTicketDMAndVoice(t *testing.T) {
	start := time.Date(2026, 11, 26, 7, 28, 0, 0, time.UTC)
	var elapsed atomic.Int64
	now := func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	f := newFixture(t, fixtureOpts{world: []Option{WithClock(now)}, intents: discord.MustIntents("Servers", "ServerMembers", "ServerVoiceStates", "ServerMessages", "MessageContent")})
	actor, owner, server := f.alice.ID, f.owner.ID, f.srv.ID
	member := f.w.UserClient(actor)
	staff := f.w.UserClient(owner)
	other := f.w.CreateUser("other")
	isErr(t, f.w.AddMember(server, other.ID), nil)
	outsider := f.w.UserClient(other.ID)
	private := discord.Overwrite{ID: server, Type: discord.OverwriteRole, Deny: permView | permReadHistory}
	ticket, err := f.w.AdminCreateChannel(server, discord.ChannelParams{Name: ptr("ticket-2"), Type: discord.ChannelText, Overwrites: &[]discord.Overwrite{private, {ID: actor, Type: discord.OverwriteMember, Allow: permView | permReadHistory | permSend | permAddReactions}}})
	isErr(t, err, nil)
	modLog, err := f.w.AdminCreateChannel(server, discord.ChannelParams{Name: ptr("mod-log"), Type: discord.ChannelText, Overwrites: &[]discord.Overwrite{private}})
	isErr(t, err, nil)
	checkTree := func(c *Client, ch string, want bool) {
		t.Helper()
		items, err := c.ListChannels(t.Context(), server)
		isErr(t, err, nil)
		if got := slices.ContainsFunc(items, func(k discord.Channel) bool { return k.ID == ch }); got != want {
			t.Fatalf("view of %s for %s = %t, want %t", ch, c.a.userID, got, want)
		}
	}
	checkTree(staff, modLog.ID, true)
	checkTree(member, modLog.ID, false)
	checkTree(member, ticket.ID, true)
	checkTree(outsider, ticket.ID, false)
	_, err = f.w.SendMessage(actor, ticket.ID, "member question")
	isErr(t, err, nil)
	reply, err := f.w.SendMessage(owner, ticket.ID, "staff answer")
	isErr(t, err, nil)
	_, err = outsider.GetMessage(t.Context(), ticket.ID, reply.ID)
	isErr(t, err, discord.ErrForbidden)
	_, err = f.w.SendMessage(other.ID, ticket.ID, "hello")
	isErr(t, err, discord.ErrForbidden)

	room, err := f.w.AdminCreateChannel(server, discord.ChannelParams{Name: ptr("lobby"), Type: discord.ChannelVoice})
	isErr(t, err, nil)
	privateRoom, err := f.w.AdminCreateChannel(server, discord.ChannelParams{Name: ptr("private-room"), Type: discord.ChannelVoice, Overwrites: &[]discord.Overwrite{private}})
	isErr(t, err, nil)
	isErr(t, f.w.JoinVoice(actor, privateRoom.ID), discord.ErrForbidden)
	isErr(t, f.w.JoinVoice(actor, room.ID), nil)
	isErr(t, f.w.SetVoiceMedia(server, actor, true, false), nil)
	move := func(c *Client, id string) error {
		_, err := c.UpdateMember(t.Context(), server, actor, discord.MemberParams{VoiceChannelID: &id}, "consented move")
		return err
	}
	isErr(t, move(outsider, privateRoom.ID), discord.ErrForbidden)
	isErr(t, move(staff, privateRoom.ID), nil)
	isErr(t, move(staff, room.ID), nil)
	isErr(t, f.w.AddReaction(owner, ticket.ID, reply.ID, "✅"), nil)
	until := start.Add(time.Minute)
	_, err = f.sess.UpdateMember(t.Context(), server, actor, discord.MemberParams{TimeoutUntil: &until}, "consented one-minute check")
	isErr(t, err, nil)
	entries, err := f.sess.ListAuditLog(ctx, server, discord.AuditQuery{Action: discord.AuditMemberUpdate, Limit: 10})
	isErr(t, err, nil)
	if len(entries) != 1 || entries[0].UserID != f.bot.ID || entries[0].TargetID != actor || entries[0].Reason != "consented one-minute check" {
		t.Fatalf("timeout audit: %+v", entries)
	}
	change, _ := entries[0].Change("communication_disabled_until")
	var deadline time.Time
	if err := json.Unmarshal(change.New, &deadline); err != nil || !deadline.Equal(until) {
		t.Fatal(change, err)
	}
	if got := f.w.VoiceStates(server); len(got) != 0 {
		t.Fatalf("still in voice: %+v", got)
	}
	dm, err := f.sess.CreateDM(t.Context(), actor)
	isErr(t, err, nil)
	receipt, err := f.sess.SendMessage(t.Context(), dm.ID, discord.MessageCreate{Content: "One-minute timeout"})
	isErr(t, err, nil)
	for _, at := range []time.Duration{0, 29 * time.Second, 59 * time.Second} {
		elapsed.Store(int64(at))
		history, err := member.ListMessages(t.Context(), ticket.ID, discord.ListMessagesParams{})
		isErr(t, err, nil)
		if len(history) != 2 || history[0].Content != "staff answer" {
			t.Fatalf("ticket history at %s = %+v", at, history)
		}
		_, err = member.GetChannel(t.Context(), modLog.ID)
		isErr(t, err, discord.ErrForbidden)
		_, err = member.GetMessage(t.Context(), dm.ID, receipt.ID)
		isErr(t, err, nil)
		_, err = outsider.GetMessage(t.Context(), dm.ID, receipt.ID)
		isErr(t, err, discord.ErrForbidden)
		_, err = f.w.SendMessage(actor, ticket.ID, "blocked")
		isErr(t, err, discord.ErrForbidden)
		isErr(t, f.w.AddReaction(actor, ticket.ID, reply.ID, "🎯"), discord.ErrForbidden)
		isErr(t, f.w.JoinVoice(actor, room.ID), discord.ErrForbidden)
		isErr(t, f.w.SetVoiceMedia(server, actor, true, true), discord.ErrNotFound)
		if p := f.w.Permissions(server, room.ID, actor); p.Has(discord.MustPermissions("Speak")) || p.Has(discord.MustPermissions("Connect")) {
			t.Fatalf("timeout retained voice permissions: %v", p.Names())
		}
	}
	// Discord's timeout FAQ distinguishes creating a reaction from clicking an
	// emoji already present. The live banner did not exercise that distinction.
	isErr(t, f.w.AddReaction(actor, ticket.ID, reply.ID, "✅"), nil)
	elapsed.Store(int64(time.Minute))
	_, err = f.w.SendMessage(actor, ticket.ID, "message after expiry")
	isErr(t, err, nil)
	isErr(t, f.w.AddReaction(actor, ticket.ID, reply.ID, "🎯"), nil)
	isErr(t, f.w.JoinVoice(actor, room.ID), nil)
	isErr(t, f.w.SetVoiceMedia(server, actor, true, true), nil)
	if !f.w.Permissions(server, room.ID, actor).Has(discord.MustPermissions("Speak")) {
		t.Fatal("speak was not restored")
	}
	checkTree(member, modLog.ID, false)
}

func TestCommandPickerUsesTheInvokingMembersPermissions(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	sid := f.srv.ID
	err := f.sess.SetServerCommands(t.Context(), f.appID, sid, []discord.Command{
		{Name: "timeout", Description: "moderator command", DefaultMemberPermissions: ptr(permModerate)},
		{Name: "rooms", Description: "member command"},
	})
	isErr(t, err, nil)
	pick := func(user string) []string {
		t.Helper()
		cmds, err := f.w.CommandsFor(f.appID, sid, f.general.ID, user)
		isErr(t, err, nil)
		var names []string
		for _, cmd := range cmds {
			names = append(names, cmd.Name)
		}
		return names
	}
	if cmds := pick(f.alice.ID); !slices.Equal(cmds, []string{"rooms"}) {
		t.Fatal(cmds)
	}
	if cmds := pick(f.owner.ID); len(cmds) != 2 {
		t.Fatal(cmds)
	}
	isErr(t, f.w.InvokeCommand(f.alice.ID, sid, f.general.ID, f.appID, "timeout", nil).Err(), discord.ErrForbidden)
	until := time.Now().Add(time.Minute)
	_, err = f.w.AdminUpdateMember(sid, f.alice.ID, discord.MemberParams{TimeoutUntil: &until})
	isErr(t, err, nil)
	if cmds := pick(f.alice.ID); len(cmds) != 0 {
		t.Fatal(cmds)
	}
}

func TestVoiceRoomLockPermitAndOwnerMovement(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("Servers", "ServerVoiceStates")})
	sid := f.srv.ID
	hub, err := f.w.AdminCreateChannel(sid, discord.ChannelParams{Name: ptr("Create a channel"), Type: discord.ChannelVoice})
	isErr(t, err, nil)
	room, err := f.sess.CreateChannel(ctx, sid, discord.ChannelParams{Name: ptr("Owner's channel"), Type: discord.ChannelVoice}, "create private voice room")
	isErr(t, err, nil)
	isErr(t, f.w.JoinVoice(f.owner.ID, hub.ID), nil)
	_, err = f.sess.UpdateMember(ctx, sid, f.owner.ID, discord.MemberParams{VoiceChannelID: &room.ID}, "move owner")
	isErr(t, err, nil)
	lock := []discord.Overwrite{{ID: sid, Type: discord.OverwriteRole, Deny: permConnect}}
	_, err = f.sess.UpdateChannel(ctx, room.ID, discord.ChannelParams{Overwrites: &lock}, "lock")
	isErr(t, err, nil)
	if _, err := f.w.UserClient(f.alice.ID).GetChannel(ctx, room.ID); err != nil {
		t.Fatal("a connection lock hid the room", err)
	}
	isErr(t, f.w.JoinVoice(f.alice.ID, room.ID), discord.ErrForbidden)
	if states := f.w.VoiceStates(sid); len(states) != 1 || states[0].UserID != f.owner.ID || states[0].ChannelID != room.ID {
		t.Fatal(states)
	}
	lock = append(lock, discord.Overwrite{ID: f.alice.ID, Type: discord.OverwriteMember, Allow: permConnect})
	_, err = f.sess.UpdateChannel(ctx, room.ID, discord.ChannelParams{Overwrites: &lock}, "permit member")
	isErr(t, err, nil)
	isErr(t, f.w.JoinVoice(f.alice.ID, room.ID), nil)
	entries, err := f.sess.ListAuditLog(ctx, sid, discord.AuditQuery{Action: discord.AuditMemberMove, Limit: 10})
	isErr(t, err, nil)
	if len(entries) != 1 || entries[0].UserID != f.bot.ID || entries[0].Reason != "move owner" || string(entries[0].Options["channel_id"]) != string(auditValue(room.ID)) || string(entries[0].Options["count"]) != `"1"` {
		t.Fatalf("move audit: %+v", entries)
	}
	if states := f.w.VoiceStates(sid); len(states) != 2 || states[0].ChannelID != room.ID || states[1].ChannelID != room.ID {
		t.Fatal(states)
	}
}
