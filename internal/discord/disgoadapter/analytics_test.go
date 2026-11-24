package disgoadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestTranslateServerAvailable(t *testing.T) {
	s := testSession()
	evs := mustTranslate(t, s, "GUILD_CREATE", `{"id":"1","name":"S","members":[],
	  "voice_states":[
	    {"channel_id":"30","user_id":"2","self_stream":true,"member":{"user":{"id":"2","username":"a"},"roles":[]}},
	    {"channel_id":"31","user_id":"3","self_video":true,"member":{"user":{"id":"3","username":"music","bot":true},"roles":[]}},
	    {"channel_id":null,"user_id":"4"}],
	  "presences":[{"user":{"id":"2"},"status":"idle","client_status":{"mobile":"idle"},
	    "activities":[{"name":"Minecraft","type":0},{"name":"Custom Status","type":4,"state":"hi"},{"name":"?","type":42}]}]}`)
	a := one(t, evs).(discord.ServerAvailableEvent)
	want := discord.ServerAvailableEvent{
		ServerID: "1",
		VoiceStates: []discord.VoiceState{
			{ServerID: "1", ChannelID: "30", UserID: "2", SelfStream: true},
			{ServerID: "1", ChannelID: "31", UserID: "3", Bot: true, SelfVideo: true},
		},
		Presences: []discord.MemberPresence{{UserID: "2", Status: "idle", Mobile: "idle",
			Activities: []discord.Activity{{Type: "playing", Name: "Minecraft"}, {Type: "custom", Name: "Custom Status"}}}},
	}
	if !reflect.DeepEqual(a, want) || discord.ServerIDOf(a) != "1" {
		t.Errorf("available = %#v", a)
	}
	// A join emits the join first, then the availability.
	mustTranslate(t, s, "READY", `{"user":{"id":"600"},"guilds":[]}`)
	evs = mustTranslate(t, s, "GUILD_CREATE", `{"id":"9","name":"New"}`)
	if len(evs) != 2 {
		t.Fatalf("join emitted %#v", evs)
	}
	if _, ok := evs[0].(discord.ServerJoinEvent); !ok {
		t.Errorf("first = %#v", evs[0])
	}
	if _, ok := evs[1].(discord.ServerAvailableEvent); !ok {
		t.Errorf("second = %#v", evs[1])
	}
}

func TestTranslateVoiceAndPresence(t *testing.T) {
	s := testSession()
	v := one(t, mustTranslate(t, s, "VOICE_STATE_UPDATE", `{"guild_id":"1","channel_id":"30","user_id":"2","session_id":"x",
	  "self_mute":true,"self_deaf":false,"self_stream":false,"self_video":true,"member":{"user":{"id":"2","username":"a"},"roles":[]}}`)).(discord.VoiceStateUpdateEvent)
	if v.State != (discord.VoiceState{ServerID: "1", ChannelID: "30", UserID: "2", SelfMute: true, SelfVideo: true}) || discord.ServerIDOf(v) != "1" {
		t.Errorf("voice = %#v", v)
	}
	left := one(t, mustTranslate(t, s, "VOICE_STATE_UPDATE", `{"guild_id":"1","channel_id":null,"user_id":"2"}`)).(discord.VoiceStateUpdateEvent)
	if left.State.ChannelID != "" {
		t.Errorf("leave = %#v", left)
	}
	if evs := mustTranslate(t, s, "VOICE_STATE_UPDATE", `{"channel_id":"5","user_id":"2"}`); len(evs) != 0 {
		t.Errorf("DM call voice state emitted %v", evs)
	}
	p := one(t, mustTranslate(t, s, "PRESENCE_UPDATE", `{"user":{"id":"2"},"guild_id":"1","status":"dnd",
	  "client_status":{"desktop":"dnd","web":"idle"},"activities":[{"name":"Spotify","type":2,"details":"song"}]}`)).(discord.PresenceUpdateEvent)
	if p.ServerID != "1" || p.Presence.Status != "dnd" || p.Presence.Desktop != "dnd" || p.Presence.Web != "idle" || p.Presence.Mobile != "" ||
		!reflect.DeepEqual(p.Presence.Activities, []discord.Activity{{Type: "listening", Name: "Spotify"}}) || discord.ServerIDOf(p) != "1" {
		t.Errorf("presence = %#v", p)
	}
}

func TestTranslateModerationEvents(t *testing.T) {
	s := testSession()
	a := one(t, mustTranslate(t, s, "GUILD_AUDIT_LOG_ENTRY_CREATE", `{"id":"50","guild_id":"1","action_type":24,"user_id":"7","target_id":"2",
	  "changes":[{"key":"communication_disabled_until","new_value":"2026-11-25T00:00:00Z"}]}`)).(discord.AuditLogEntryEvent)
	c, ok := a.Change("communication_disabled_until")
	if a.ServerID != "1" || a.ActionType != discord.AuditMemberUpdate || a.UserID != "7" || a.TargetID != "2" || !ok ||
		string(c.New) != `"2026-11-25T00:00:00Z"` || c.Old != nil || discord.ServerIDOf(a) != "1" {
		t.Errorf("audit = %#v", a)
	}
	am := one(t, mustTranslate(t, s, "AUTO_MODERATION_ACTION_EXECUTION", `{"guild_id":"1","action":{"type":1,"metadata":{}},"rule_id":"70",
	  "rule_trigger_type":1,"user_id":"2","channel_id":"10","content":"secret words","matched_keyword":"secret","matched_content":"secret"}`)).(discord.AutoModActionEvent)
	if am != (discord.AutoModActionEvent{ServerID: "1", RuleID: "70", Trigger: discord.AutoModKeyword, Action: discord.AutoModActionBlock, UserID: "2", ChannelID: "10"}) {
		t.Errorf("automod = %#v", am)
	}
	ic := one(t, mustTranslate(t, s, "INVITE_CREATE", `{"guild_id":"1","channel_id":"10","code":"abc","max_uses":5,"uses":0,"inviter":{"id":"2"}}`)).(discord.InviteCreateEvent)
	if ic != (discord.InviteCreateEvent{ServerID: "1", ChannelID: "10", Code: "abc", MaxUses: 5}) || discord.ServerIDOf(ic) != "1" {
		t.Errorf("invite create = %#v", ic)
	}
	id := one(t, mustTranslate(t, s, "INVITE_DELETE", `{"guild_id":"1","channel_id":"10","code":"abc"}`)).(discord.InviteDeleteEvent)
	if id != (discord.InviteDeleteEvent{ServerID: "1", ChannelID: "10", Code: "abc"}) {
		t.Errorf("invite delete = %#v", id)
	}
}

func TestTranslateThreadAndMessageFields(t *testing.T) {
	s := testSession()
	th := one(t, mustTranslate(t, s, "THREAD_CREATE", `{"id":"20","guild_id":"1","parent_id":"10","name":"t","type":11,"newly_created":true}`)).(discord.ChannelCreateEvent)
	if !th.NewThread || th.Channel.ParentID != "10" {
		t.Errorf("new thread = %#v", th)
	}
	if added := one(t, mustTranslate(t, s, "THREAD_CREATE", `{"id":"21","guild_id":"1","parent_id":"10","name":"t","type":12}`)).(discord.ChannelCreateEvent); added.NewThread {
		t.Errorf("thread the bot was added to = %#v", added)
	}
	if ch := one(t, mustTranslate(t, s, "CHANNEL_CREATE", `{"id":"22","guild_id":"1","name":"c","type":0}`)).(discord.ChannelCreateEvent); ch.NewThread {
		t.Errorf("channel = %#v", ch)
	}

	m := one(t, mustTranslate(t, s, "MESSAGE_CREATE", `{"id":"5","channel_id":"10","guild_id":"1","author":{"id":"2","username":"a"},
	  "type":19,"message_reference":{"type":0,"message_id":"4","channel_id":"10"},"sticker_items":[{"id":"81","name":"Hi","format_type":1}],
	  "poll":{"question":{"text":"?"}},"attachments":[{"id":"9","filename":"a.png"}],"timestamp":"2026-11-24T10:00:00Z",
	  "member":{"roles":[],"flags":2}}`)).(discord.MessageCreateEvent).Message
	if m.ReplyToID != "4" || !reflect.DeepEqual(m.StickerIDs, []string{"81"}) || !m.Poll || m.System || len(m.Attachments) != 1 ||
		m.Member == nil || m.Member.Flags != discord.MemberFlagCompletedOnboarding {
		t.Errorf("message = %#v", m)
	}
	sys := one(t, mustTranslate(t, s, "MESSAGE_CREATE", `{"id":"6","channel_id":"10","guild_id":"1","author":{"id":"2","username":"a"},"type":7,"timestamp":"2026-11-24T10:00:00Z"}`)).(discord.MessageCreateEvent).Message
	if !sys.System || sys.ReplyToID != "" || sys.Poll {
		t.Errorf("join message = %#v", sys)
	}
	fwd := one(t, mustTranslate(t, s, "MESSAGE_CREATE", `{"id":"7","channel_id":"10","guild_id":"1","author":{"id":"2","username":"a"},"type":0,
	  "message_reference":{"type":1,"message_id":"3"},"webhook_id":"88","timestamp":"2026-11-24T10:00:00Z"}`)).(discord.MessageCreateEvent).Message
	if fwd.ReplyToID != "" || fwd.WebhookID != "88" {
		t.Errorf("forwarded webhook message = %#v", fwd)
	}
}

func TestAnalyticsReaderRequests(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /guilds/1", 200, `{"id":"1","name":"S","approximate_member_count":120,"approximate_presence_count":33,"premium_tier":2,"premium_subscription_count":9}`)
	f.json("GET /guilds/1/scheduled-events", 200, `[{"id":"40","name":"Game night","status":1,"user_count":12},{"id":"41","name":"Live","status":2}]`)
	f.json("GET /guilds/1/invites", 200, `[{"code":"abc","uses":3,"max_uses":0},{"code":"one","uses":0,"max_uses":1}]`)
	f.json("GET /guilds/1/vanity-url", 200, `{"code":"cicord","uses":42}`)
	s := f.connect(t)
	r := s.(discord.AnalyticsReader)
	ctx := context.Background()

	counts, err := r.GetServerCounts(ctx, "1")
	if err != nil || counts != (discord.ServerCounts{ApproximateMembers: 120, ApproximateOnline: 33, Boosts: 9, BoostTier: 2}) {
		t.Errorf("counts = %+v, %v", counts, err)
	}
	if uri := f.last().URI; uri != "/guilds/1?with_counts=true" {
		t.Errorf("counts uri = %s", uri)
	}
	evs, err := r.ListScheduledEventInterest(ctx, "1")
	if err != nil || !reflect.DeepEqual(evs, []discord.ScheduledEventInterest{
		{ID: "40", Name: "Game night", Status: "scheduled", Interested: 12}, {ID: "41", Name: "Live", Status: "active"}}) {
		t.Errorf("events = %+v, %v", evs, err)
	}
	if uri := f.last().URI; uri != "/guilds/1/scheduled-events?with_user_count=true" {
		t.Errorf("events uri = %s", uri)
	}
	invs, err := r.ListInviteUses(ctx, "1")
	if err != nil || !reflect.DeepEqual(invs, []discord.InviteUses{
		{Code: "abc", Uses: 3}, {Code: "one", MaxUses: 1}, {Code: "cicord", Uses: 42, Vanity: true}}) {
		t.Errorf("invites = %+v, %v", invs, err)
	}

	// No vanity URL: the invites alone.
	f.json("GET /guilds/1/vanity-url", 403, `{"code":50013,"message":"Missing Permissions"}`)
	if invs, err := r.ListInviteUses(ctx, "1"); err != nil || len(invs) != 2 {
		t.Errorf("invites without vanity = %+v, %v", invs, err)
	}
	// Without ManageServer the invites call fails.
	f.json("GET /guilds/1/invites", 403, `{"code":50013,"message":"Missing Permissions"}`)
	if _, err := r.ListInviteUses(ctx, "1"); !errors.Is(err, discord.ErrForbidden) {
		t.Errorf("forbidden invites err = %v", err)
	}
	if _, err := r.GetServerCounts(ctx, "x/../2"); !errors.Is(err, discord.ErrBadRequest) || !strings.Contains(err.Error(), "invalid id") {
		t.Errorf("bad id err = %v", err)
	}
}
