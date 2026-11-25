package sim

import (
	"errors"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

var analyticsIntents = discord.MustIntents("Servers", "ServerMembers", "ServerMessages", "ServerVoiceStates", "ServerPresences",
	"ServerModeration", "ServerInvites", "AutoModerationExecution")

func simEvents[T discord.Event](f *fixture) []T {
	f.w.WaitIdle()
	var out []T
	for _, e := range f.rec.all() {
		if v, ok := e.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestSimVoiceAndServerAvailable(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: analyticsIntents, noOpen: true})
	isErr(t, f.w.SetPrivilegedIntents(f.appID, discord.PrivilegedIntents), nil)
	voice, err := f.w.AdminCreateChannel(f.srv.ID, discord.ChannelParams{Name: ptr("Lounge"), Type: discord.ChannelVoice})
	isErr(t, err, nil)
	isErr(t, f.w.JoinVoice(f.alice.ID, voice.ID), nil)
	isErr(t, f.w.SetMemberPresence(f.srv.ID, discord.MemberPresence{UserID: f.alice.ID, Status: "online", Desktop: "online",
		Activities: []discord.Activity{{Type: "playing", Name: "Minecraft"}}}), nil)
	if err := f.w.JoinVoice(f.alice.ID, f.general.ID); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("join a text channel: %v", err)
	}

	// Opening the gateway delivers the current voice states and presences.
	isErr(t, f.sess.Open(ctx, analyticsIntents, f.rec.handle), nil)
	avail := simEvents[discord.ServerAvailableEvent](f)
	if len(avail) != 1 || len(avail[0].VoiceStates) != 1 || avail[0].VoiceStates[0].ChannelID != voice.ID ||
		len(avail[0].Presences) != 1 || avail[0].Presences[0].Activities[0].Name != "Minecraft" {
		t.Fatalf("available = %#v", avail)
	}
	isErr(t, f.w.SetVoiceMedia(f.srv.ID, f.alice.ID, true, false), nil)
	isErr(t, f.w.LeaveVoice(f.srv.ID, f.alice.ID), nil)
	vs := simEvents[discord.VoiceStateUpdateEvent](f)
	if len(vs) != 2 || !vs[0].State.SelfStream || vs[1].State.ChannelID != "" || discord.ServerIDOf(vs[1]) != f.srv.ID {
		t.Fatalf("voice = %#v", vs)
	}
	if len(f.w.VoiceStates(f.srv.ID)) != 0 {
		t.Error("voice state kept after leave")
	}
	isErr(t, f.w.SetMemberPresence(f.srv.ID, discord.MemberPresence{UserID: f.alice.ID, Status: "invisible"}), nil)
	if ps := simEvents[discord.PresenceUpdateEvent](f); len(ps) != 1 || ps[0].Presence.Status != "offline" {
		t.Fatalf("presences = %#v", ps)
	}
	counts, err := f.sess.GetServerCounts(ctx, f.srv.ID)
	isErr(t, err, nil)
	if counts.ApproximateMembers != 3 || counts.ApproximateOnline != 0 {
		t.Errorf("counts = %+v", counts)
	}
}

func TestSimAnalyticsIntentGating(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("Servers", "ServerMessages")})
	voice, _ := f.w.AdminCreateChannel(f.srv.ID, discord.ChannelParams{Name: ptr("Lounge"), Type: discord.ChannelVoice})
	isErr(t, f.w.JoinVoice(f.alice.ID, voice.ID), nil)
	_, err := f.w.CreateInvite(f.general.ID, 0)
	isErr(t, err, nil)
	isErr(t, f.w.AdminKickMember(f.srv.ID, f.alice.ID), nil)
	if n := len(simEvents[discord.VoiceStateUpdateEvent](f)) + len(simEvents[discord.InviteCreateEvent](f)) + len(simEvents[discord.AuditLogEntryEvent](f)); n != 0 {
		t.Errorf("%d analytics events without their intents", n)
	}
}

func TestSimModerationAndInvites(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: analyticsIntents, noOpen: true})
	isErr(t, f.w.SetPrivilegedIntents(f.appID, discord.PrivilegedIntents), nil)
	isErr(t, f.sess.Open(ctx, analyticsIntents, f.rec.handle), nil)
	f.resetEvents()

	// Moderation by a member: audit log entries with the moderator as executor.
	until := f.w.Now().Add(time.Hour)
	_, err := f.w.AdminUpdateMember(f.srv.ID, f.alice.ID, discord.MemberParams{TimeoutUntil: &until})
	isErr(t, err, nil)
	_, err = f.w.AdminUpdateMember(f.srv.ID, f.alice.ID, discord.MemberParams{TimeoutUntil: &time.Time{}})
	isErr(t, err, nil)
	isErr(t, f.w.AdminBanMember(f.srv.ID, f.alice.ID), nil)
	isErr(t, f.w.UnbanMember(f.srv.ID, f.alice.ID), nil)
	audit := simEvents[discord.AuditLogEntryEvent](f)
	if len(audit) != 4 || audit[0].ActionType != discord.AuditMemberUpdate || audit[0].UserID != f.owner.ID || audit[0].TargetID != f.alice.ID ||
		audit[2].ActionType != discord.AuditMemberBanAdd || audit[3].ActionType != discord.AuditMemberBanRemove {
		t.Fatalf("audit = %#v", audit)
	}
	set, _ := audit[0].Change("communication_disabled_until")
	cleared, _ := audit[1].Change("communication_disabled_until")
	if set.New == nil || set.Old != nil || cleared.New != nil || cleared.Old == nil {
		t.Errorf("timeout changes = %#v / %#v", set, cleared)
	}

	// Invites: a 1-use invite is deleted when used; the vanity URL counts its uses.
	code, err := f.w.CreateInvite(f.general.ID, 1)
	isErr(t, err, nil)
	bob := f.w.CreateUser("bob")
	isErr(t, f.w.JoinWithInvite(bob.ID, code), nil)
	if c, d := simEvents[discord.InviteCreateEvent](f), simEvents[discord.InviteDeleteEvent](f); len(c) != 1 || c[0].MaxUses != 1 || len(d) != 1 || d[0].Code != code {
		t.Fatalf("invite events = %#v %#v", c, d)
	}
	isErr(t, f.w.SetVanityURL(f.srv.ID, "cool"), nil)
	carol := f.w.CreateUser("carol")
	isErr(t, f.w.JoinWithInvite(carol.ID, "cool"), nil)
	invs, err := f.sess.ListInviteUses(ctx, f.srv.ID)
	isErr(t, err, nil)
	if len(invs) != 1 || !invs[0].Vanity || invs[0].Uses != 1 {
		t.Errorf("invites = %+v", invs)
	}

	// AutoMod: one event per action of the rule.
	rule, err := f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "w", Trigger: discord.AutoModKeyword, Keywords: []string{"x"}, Enabled: true,
		Actions: []discord.AutoModAction{{Type: discord.AutoModActionBlock}, {Type: discord.AutoModActionAlert, ChannelID: f.general.ID}}}, "")
	isErr(t, err, nil)
	isErr(t, f.w.TriggerAutoMod(f.srv.ID, rule.ID, bob.ID, f.general.ID), nil)
	if am := simEvents[discord.AutoModActionEvent](f); len(am) != 2 || am[0].Action != discord.AutoModActionBlock || am[1].RuleID != rule.ID {
		t.Fatalf("automod = %#v", am)
	}

	// Scheduled events.
	id, err := f.w.CreateScheduledEvent(f.srv.ID, "Game night", 4)
	isErr(t, err, nil)
	isErr(t, f.w.SetScheduledEvent(f.srv.ID, id, 6, "active"), nil)
	evs, err := f.sess.ListScheduledEventInterest(ctx, f.srv.ID)
	isErr(t, err, nil)
	if len(evs) != 1 || evs[0].Interested != 6 || evs[0].Status != "active" {
		t.Errorf("events = %+v", evs)
	}

	// Onboarding, threads and message extras.
	isErr(t, f.w.CompleteOnboarding(f.srv.ID, bob.ID), nil)
	ups := simEvents[discord.MemberUpdateEvent](f)
	last := ups[len(ups)-1]
	if last.Previous == nil || last.Previous.Flags != 0 || last.Member.Flags != discord.MemberFlagCompletedOnboarding {
		t.Errorf("onboarding update = %#v", last)
	}
	th, err := f.sess.CreateThread(ctx, f.general.ID, discord.ThreadParams{Name: "t"})
	isErr(t, err, nil)
	if cs := simEvents[discord.ChannelCreateEvent](f); len(cs) == 0 || cs[len(cs)-1].Channel.ID != th.ID || !cs[len(cs)-1].NewThread {
		t.Errorf("thread create = %#v", cs)
	}
	f.resetEvents()
	first, _ := f.w.SendMessage(bob.ID, f.general.ID, "hi")
	_, err = f.w.SendMessageExtras(bob.ID, f.general.ID, discord.MessageCreate{Content: "look", ReplyTo: first.ID}, MessageExtras{Attachments: 2, StickerIDs: []string{"9"}, Poll: true})
	isErr(t, err, nil)
	_, err = f.w.SendWebhookMessage(f.general.ID, "hook")
	isErr(t, err, nil)
	ms := simEvents[discord.MessageCreateEvent](f)
	// The fixture has no MessageContent intent: attachments and poll are emptied.
	if len(ms) != 3 || ms[1].Message.ReplyToID != first.ID || len(ms[1].Message.StickerIDs) != 1 || ms[1].Message.Poll || len(ms[1].Message.Attachments) != 0 ||
		ms[2].Message.WebhookID == "" || !ms[2].Message.Author.Bot {
		t.Fatalf("messages = %#v", ms)
	}
	// Deleting someone else's message is logged.
	isErr(t, f.w.AdminDeleteMessage(f.general.ID, first.ID), nil)
	if a := simEvents[discord.AuditLogEntryEvent](f); len(a) != 1 || a[0].ActionType != discord.AuditMessageDelete {
		t.Errorf("message delete audit = %#v", a)
	}
}

func TestSimAnalyticsPermissions(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("Servers", "ServerModeration", "AutoModerationExecution"), perms: discord.MustPermissions("ViewChannel")})
	isErr(t, f.w.AdminKickMember(f.srv.ID, f.alice.ID), nil)
	if a := simEvents[discord.AuditLogEntryEvent](f); len(a) != 0 {
		t.Errorf("audit without ViewAuditLog = %#v", a)
	}
	if _, err := f.sess.ListInviteUses(ctx, f.srv.ID); !errors.Is(err, discord.ErrForbidden) {
		t.Errorf("invites without ManageServer: %v", err)
	}
}

func TestSimVoiceMoveDisconnectAndLimits(t *testing.T) {
	voiceIntents := discord.MustIntents("Servers", "ServerVoiceStates")
	f := newFixture(t, fixtureOpts{intents: voiceIntents, perms: discord.MustPermissions("ViewChannel", "Connect", "ManageChannels")})
	bob := f.w.CreateUser("bob")
	isErr(t, f.w.AddMember(f.srv.ID, bob.ID), nil)
	hub, err := f.w.AdminCreateChannel(f.srv.ID, discord.ChannelParams{Name: ptr("Hub"), Type: discord.ChannelVoice})
	isErr(t, err, nil)
	room, err := f.w.AdminCreateChannel(f.srv.ID, discord.ChannelParams{Name: ptr("Room"), Type: discord.ChannelVoice, UserLimit: ptr(1)})
	isErr(t, err, nil)

	// A user limit keeps members out, the member is in the voice state.
	isErr(t, f.w.JoinVoice(f.alice.ID, room.ID), nil)
	isErr(t, f.w.JoinVoice(bob.ID, room.ID), discord.ErrForbidden)
	isErr(t, f.w.JoinVoice(f.alice.ID, room.ID), nil) // already in: does not count
	isErr(t, f.w.JoinVoice(bob.ID, hub.ID), nil)
	if vs := simEvents[discord.VoiceStateUpdateEvent](f); len(vs) != 3 || vs[0].State.Member == nil || vs[0].State.Member.User.ID != f.alice.ID {
		t.Fatalf("voice events = %#v", vs)
	}

	// Moving needs MoveMembers; the destination's user limit does not apply to moves.
	move := func(user, channel string) error {
		_, err := f.sess.UpdateMember(ctx, f.srv.ID, user, discord.MemberParams{VoiceChannelID: &channel}, "")
		return err
	}
	isErr(t, move(bob.ID, room.ID), discord.ErrForbidden)
	_, err = f.w.AdminUpdateRole(f.srv.ID, f.botRole.ID, discord.RoleParams{Permissions: ptr(discord.MustPermissions("ViewChannel", "Connect", "ManageChannels", "MoveMembers"))})
	isErr(t, err, nil)
	f.resetEvents()
	isErr(t, move(bob.ID, room.ID), nil)
	if vs := f.w.VoiceStates(f.srv.ID); len(vs) != 2 || vs[0].ChannelID != room.ID || vs[1].ChannelID != room.ID {
		t.Fatalf("after move = %#v", vs)
	}
	if n := len(simEvents[discord.MemberUpdateEvent](f)); n != 0 {
		t.Errorf("a voice move emitted %d member updates", n)
	}
	isErr(t, move(f.alice.ID, f.general.ID), discord.ErrBadRequest) // not a voice channel
	isErr(t, move(f.owner.ID, hub.ID), discord.ErrBadRequest)       // not connected to voice

	// "" disconnects; deleting a voice channel disconnects its members.
	isErr(t, move(bob.ID, ""), nil)
	f.resetEvents()
	isErr(t, f.w.AdminDeleteChannel(room.ID), nil)
	vs := simEvents[discord.VoiceStateUpdateEvent](f)
	if len(vs) != 1 || vs[0].State.UserID != f.alice.ID || vs[0].State.ChannelID != "" || len(f.w.VoiceStates(f.srv.ID)) != 0 {
		t.Fatalf("delete disconnects = %#v", vs)
	}

	// Members need Connect.
	_, err = f.w.AdminUpdateChannel(hub.ID, discord.ChannelParams{Overwrites: &[]discord.Overwrite{
		{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("Connect")}}})
	isErr(t, err, nil)
	isErr(t, f.w.JoinVoice(f.alice.ID, hub.ID), discord.ErrForbidden)
}
