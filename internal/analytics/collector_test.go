package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

var ctx = context.Background()

// snowflakeAt returns a user ID created at t.
func snowflakeAt(t time.Time) string {
	return strconv.FormatUint(uint64(t.UnixMilli()-discordEpochMs)<<22, 10)
}

func member(id string, bot bool) discord.Member {
	return discord.Member{ServerID: "1", User: discord.User{ID: id, Bot: bot}}
}

func TestCollectorEventMapping(t *testing.T) {
	f := newCollectorFixture(t)
	now := f.clock.Now()
	young, old := snowflakeAt(now.Add(-2*time.Hour)), snowflakeAt(now.AddDate(-3, 0, 0))
	edited := now
	evs := []discord.Event{
		discord.MemberJoinEvent{Member: member(young, false)},
		discord.MemberJoinEvent{Member: member(old, false)},
		discord.MemberJoinEvent{Member: member("77", true)},
		discord.MemberLeaveEvent{ServerID: "1", User: discord.User{ID: old}},
		discord.MemberLeaveEvent{ServerID: "1", User: discord.User{ID: "77", Bot: true}},
		discord.MemberUpdateEvent{Member: discord.Member{ServerID: "1", User: discord.User{ID: young}, RoleIDs: []string{"r1", "r2"}, Flags: discord.MemberFlagCompletedOnboarding},
			Previous: &discord.Member{ServerID: "1", User: discord.User{ID: young}, RoleIDs: []string{"r2", "r3"}}},
		// Without the previous state nothing is counted.
		discord.MemberUpdateEvent{Member: discord.Member{ServerID: "1", User: discord.User{ID: old}, RoleIDs: []string{"r1"}, Flags: discord.MemberFlagCompletedOnboarding}},
		discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: young},
			Attachments: []discord.Attachment{{ID: "a"}, {ID: "b"}}, ReplyToID: "5", StickerIDs: []string{"s"}, Poll: true}},
		discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "11", Author: discord.User{ID: young}}},
		discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: "77", Bot: true}}},
		discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: "88"}, WebhookID: "88"}},
		discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: old}, System: true}},
		discord.MessageUpdateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: young}, EditedAt: &edited}},
		discord.MessageUpdateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: young}}}, // unfurl
		discord.MessageDeleteEvent{ServerID: "1", ChannelID: "11", ID: "9"},
		discord.ReactionAddEvent{ReactionEvent: discord.ReactionEvent{ServerID: "1", ChannelID: "10", UserID: old, Emoji: "👍"}},
		discord.ReactionAddEvent{ReactionEvent: discord.ReactionEvent{ServerID: "1", ChannelID: "10", UserID: young, Emoji: "party:55"}},
		discord.ReactionAddEvent{ReactionEvent: discord.ReactionEvent{ServerID: "1", ChannelID: "10", UserID: "600", Emoji: "👍"}}, // the bot itself
		discord.ChannelCreateEvent{Channel: discord.Channel{ID: "20", ServerID: "1", ParentID: "10", Type: discord.ChannelPublicThread}, NewThread: true},
		discord.ChannelCreateEvent{Channel: discord.Channel{ID: "21", ServerID: "1", ParentID: "10", Type: discord.ChannelPrivateThread}},
		discord.AuditLogEntryEvent{ServerID: "1", ActionType: discord.AuditMemberBanAdd, UserID: "5"},
		discord.AuditLogEntryEvent{ServerID: "1", ActionType: discord.AuditMemberUpdate, UserID: "5",
			Changes: []discord.AuditLogChange{{Key: "communication_disabled_until", New: json.RawMessage(`"2026-11-25T00:00:00Z"`)}}},
		discord.AuditLogEntryEvent{ServerID: "1", ActionType: discord.AuditMemberUpdate, UserID: "5",
			Changes: []discord.AuditLogChange{{Key: "communication_disabled_until", Old: json.RawMessage(`"2026-11-25T00:00:00Z"`), New: json.RawMessage(`null`)}}},
		discord.AuditLogEntryEvent{ServerID: "1", ActionType: 10, UserID: "600"}, // Cicord reconciling: not moderation
		discord.AuditLogEntryEvent{ServerID: "1", ActionType: 999, UserID: "5"},
		discord.AutoModActionEvent{ServerID: "1", RuleID: "70", Action: discord.AutoModActionBlock, UserID: young, ChannelID: "10"},
		discord.AutoModActionEvent{ServerID: "1", RuleID: "70", Action: discord.AutoModActionAlert, UserID: young, ChannelID: "10"},
		discord.InviteCreateEvent{ServerID: "1", ChannelID: "10", Code: "abc"},
	}
	for _, ev := range evs {
		f.c.Handle("alpha", ev)
	}
	// Interactions: counted from any bot, commands with their subcommand path.
	f.c.Handle("beta", discord.InteractionCreateEvent{Interaction: discord.Interaction{ServerID: "1", Type: discord.InteractionCommand, User: discord.User{ID: old},
		CommandName: "ticket", Options: []discord.InteractionOption{{Name: "close", Type: discord.OptionSubcommand}}}})
	f.c.Handle("alpha", discord.InteractionCreateEvent{Interaction: discord.Interaction{ServerID: "1", Type: discord.InteractionCommand, User: discord.User{ID: young}, CommandName: "ping"}})
	f.c.Handle("alpha", discord.InteractionCreateEvent{Interaction: discord.Interaction{ServerID: "1", Type: discord.InteractionComponent, CustomID: "x"}})
	f.c.Handle("alpha", discord.InteractionCreateEvent{Interaction: discord.Interaction{ServerID: "1", Type: discord.InteractionAutocomplete, CommandName: "ping"}})
	// Another bot's copy of a gateway event is not counted twice.
	f.c.Handle("beta", discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: young}}})
	// Unknown server.
	f.c.Handle("alpha", discord.MessageCreateEvent{Message: discord.Message{ServerID: "2", ChannelID: "10", Author: discord.User{ID: young}}})
	f.c.Flush(ctx)

	h := f.clock.Now()
	want := map[string]map[string]float64{
		MembersJoined:       {"": 2},
		MembersJoinedBots:   {"": 1},
		MembersJoinedByAge:  {"<1d": 1, ">=1y": 1},
		MembersLeft:         {"": 1},
		MembersRoleAdded:    {"r1": 1},
		MembersRoleRemoved:  {"r3": 1},
		OnboardingCompleted: {"": 1},
		MessagesSent:        {"10": 1, "11": 1},
		MessagesSentBots:    {"10": 2},
		MessagesAttachments: {"10": 2},
		MessagesReplies:     {"10": 1},
		MessagesStickers:    {"": 1},
		PollsCreated:        {"10": 1},
		MessagesEdited:      {"10": 1},
		MessagesDeleted:     {"11": 1},
		ReactionsAdded:      {"👍": 1, "party:55": 1},
		ThreadsCreated:      {"10": 1},
		ModerationActions:   {"ban": 1, "timeout": 1, "untimeout": 1, OtherDim: 1},
		AutoModTriggers:     {"70": 1},
		AutoModBlocked:      {"": 1},
		InvitesCreated:      {"": 1},
		CommandsUsed:        {"ticket close": 1, "ping": 1},
		ComponentsUsed:      {"": 1},
	}
	for metric, dims := range want {
		got := f.store.dims("main", metric)
		if fmt.Sprint(got) != fmt.Sprint(dims) {
			t.Errorf("%s = %v, want %v", metric, got, dims)
		}
	}
	if f.store.value("main", MessagesSent, "10", h) != 1 {
		t.Errorf("points not in the current hour: %s", f.store.dump())
	}
	// One Record call per kind.
	if f.store.records[state.AnalyticsCounter] != 1 {
		t.Errorf("counter records = %d", f.store.records[state.AnalyticsCounter])
	}
	// Active today: young (message, reaction, command), old (reaction, command): never the bot.
	day := dayOf(h)
	if n, _ := f.store.CountActive(ctx, "main", ActiveSetAll, day); n != 2 {
		t.Errorf("active all = %d", n)
	}
	if n, _ := f.store.CountActive(ctx, "main", ActiveSetChat, day); n != 1 {
		t.Errorf("active chat = %d", n)
	}
	for _, toks := range f.store.active {
		for tok := range toks {
			if strings.Contains(tok, young) || strings.Contains(tok, old) || len(tok) != 32 {
				t.Errorf("token %q is not an opaque HMAC", tok)
			}
		}
	}
	// AutoMod: a second trigger after the window counts again.
	f.clock.add(10 * time.Second)
	f.c.Handle("alpha", discord.AutoModActionEvent{ServerID: "1", RuleID: "70", Action: discord.AutoModActionAlert, UserID: young, ChannelID: "10"})
	f.c.Flush(ctx)
	if v := f.store.sum("main", AutoModTriggers); v != 2 {
		t.Errorf("automod triggers = %v", v)
	}
}

func TestCollectorDisabledServer(t *testing.T) {
	f := newCollectorFixture(t, func(s *CollectorServer) { s.Enabled = false })
	f.c.Handle("alpha", discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: "5"}}})
	f.c.Tick(ctx)
	if d := f.store.dump(); d != "[]" || f.store.marks != 0 || f.client.count("counts") != 0 {
		t.Errorf("disabled server collected: %s", d)
	}
	if st := f.c.Status("main"); st.Enabled || st.RetentionDays != 30 || st.Missing == nil {
		t.Errorf("status = %+v", st)
	}
}

func TestVoiceTimeSplitAcrossHours(t *testing.T) {
	f := newCollectorFixture(t)
	vs := func(user, ch string, stream bool) discord.Event {
		return discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: user, ChannelID: ch, SelfStream: stream}}
	}
	f.clock.set(f.at(10, 59, 30))
	f.c.Handle("alpha", vs("5", "30", false))
	f.c.Handle("alpha", vs("6", "40", false)) // stage
	f.c.Handle("alpha", vs("7", "39", false)) // AFK channel: not activity
	f.c.Handle("alpha", discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: "77", ChannelID: "30", Bot: true}})
	f.clock.set(f.at(11, 0, 30))
	f.c.Handle("alpha", vs("5", "30", true)) // starts streaming
	f.clock.set(f.at(11, 1, 0))
	f.c.Handle("alpha", vs("5", "31", false)) // moves: a new session
	f.clock.set(f.at(11, 2, 0))
	f.c.Handle("alpha", vs("5", "", false))
	f.c.Handle("alpha", vs("6", "", false))
	f.c.Flush(ctx)

	h10, h11 := f.at(10, 0, 0), f.at(11, 0, 0)
	checks := []struct {
		metric, dim string
		bucket      time.Time
		want        float64
	}{
		{VoiceSeconds, "30", h10, 30}, {VoiceSeconds, "30", h11, 60}, {VoiceSeconds, "31", h11, 60},
		{VoiceStreaming, "30", h11, 30}, {StageSeconds, "40", h10, 30}, {StageSeconds, "40", h11, 120},
		{VoiceSessions, "30", h10, 1}, {VoiceSessions, "31", h11, 1}, {VoicePeak, "", h10, 2}, {VoicePeak, "", h11, 2},
	}
	for _, c := range checks {
		if got := f.store.value("main", c.metric, c.dim, c.bucket); got != c.want {
			t.Errorf("%s[%s] at %s = %v, want %v (%s)", c.metric, c.dim, c.bucket.Format("15h"), got, c.want, f.store.dump())
		}
	}
	if f.store.sum("main", VoiceSessions) != 2 || f.store.sum("main", VoiceSeconds) != 150 {
		t.Errorf("sessions/seconds: %s", f.store.dump())
	}
	if n, _ := f.store.CountActive(ctx, "main", ActiveSetVoice, dayOf(h10)); n != 2 {
		t.Errorf("active in voice = %d", n)
	}
}

func TestVoiceOpenSessionsAreCreditedAtEachFlush(t *testing.T) {
	f := newCollectorFixture(t)
	f.clock.set(f.at(10, 0, 0))
	f.c.Handle("alpha", discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: "5", ChannelID: "30"}})
	f.clock.set(f.at(10, 0, 30))
	f.c.Flush(ctx)
	f.clock.set(f.at(10, 1, 0))
	f.c.Flush(ctx)
	if got := f.store.value("main", VoiceSeconds, "30", f.at(10, 0, 0)); got != 60 {
		t.Errorf("voice seconds = %v", got)
	}

	// A reconnect snapshot keeps the session (no new session) and ends the missing ones.
	f.c.Handle("alpha", discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: "6", ChannelID: "31"}})
	f.clock.set(f.at(10, 2, 0))
	f.c.Handle("alpha", discord.ServerAvailableEvent{ServerID: "1", VoiceStates: []discord.VoiceState{{ServerID: "1", UserID: "5", ChannelID: "30"}}})
	f.clock.set(f.at(10, 3, 0))
	f.c.Flush(ctx)
	if s := f.store.sum("main", VoiceSessions); s != 2 {
		t.Errorf("sessions = %v", s)
	}
	if got := f.store.value("main", VoiceSeconds, "31", f.at(10, 0, 0)); got != 60 {
		t.Errorf("ended session seconds = %v", got)
	}
	if got := f.store.value("main", VoiceSeconds, "30", f.at(10, 0, 0)); got != 180 {
		t.Errorf("kept session seconds = %v", got)
	}

	// After a restart the GUILD_CREATE snapshot (received before Configure) restores
	// the sessions without counting a session.
	c2 := NewCollector(CollectorOptions{Store: f.store, KV: f.kv, Now: f.clock.Now})
	c2.Handle("alpha", discord.ServerAvailableEvent{ServerID: "1", VoiceStates: []discord.VoiceState{{ServerID: "1", UserID: "5", ChannelID: "30"}}})
	c2.Configure([]CollectorServer{f.cfg}, nil)
	f.clock.set(f.at(10, 4, 0))
	c2.Flush(ctx)
	if got := f.store.value("main", VoiceSeconds, "30", f.at(10, 0, 0)); got != 240 {
		t.Errorf("restored session seconds = %v", got)
	}
	if s := f.store.sum("main", VoiceSessions); s != 2 {
		t.Errorf("sessions after restart = %v", s)
	}
}

func TestDailyActiveRollupAcrossDayChangeAndRestart(t *testing.T) {
	f := newCollectorFixture(t)
	msg := func(user string) discord.Event {
		return discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: user}}}
	}
	f.clock.set(f.at(9, 0, 0))
	f.c.Tick(ctx) // first run: no rollup marker → nothing to roll up
	if f.store.sum("main", MembersActive) != 0 || !f.kv.has(kvRolledUp) {
		t.Fatalf("first run rolled up: %s", f.store.dump())
	}
	f.c.Handle("alpha", msg("5"))
	f.c.Handle("alpha", msg("6"))
	f.c.Flush(ctx)
	if !f.kv.has("daykey:2026-11-24") || f.kv.ttls["daykey:2026-11-24"] != 48*time.Hour {
		t.Fatalf("day key not stored with a 48h TTL: %v", f.kv.ttls)
	}

	// Restart within the day: the same members count once.
	f.c.Stop(ctx)
	f.c = f.newCollector()
	f.clock.set(f.at(15, 0, 0))
	f.c.Handle("alpha", msg("5"))
	f.c.Handle("alpha", discord.ReactionAddEvent{ReactionEvent: discord.ReactionEvent{ServerID: "1", ChannelID: "10", UserID: "7", Emoji: "x"}})
	f.c.Tick(ctx)
	if n, _ := f.store.CountActive(ctx, "main", ActiveSetChat, "2026-11-24"); n != 2 {
		t.Errorf("chat after restart = %d", n)
	}

	// The day changes: yesterday is rolled up at its 00:00 bucket, then its tokens and
	// key are deleted.
	f.c.Handle("alpha", msg("8")) // still on the 24th, flushed by the next tick
	f.clock.set(time.Date(2026, 11, 25, 0, 0, 20, 0, time.UTC))
	f.c.Tick(ctx)
	day := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	if a, ch, v := f.store.value("main", MembersActive, "", day), f.store.value("main", MembersActiveChat, "", day), f.store.value("main", MembersActiveVoice, "", day); a != 4 || ch != 3 || v != 0 {
		t.Errorf("rollup all=%v chat=%v voice=%v: %s", a, ch, v, f.store.dump())
	}
	if len(f.store.dropped) != 1 || f.store.dropped[0] != "2026-11-25" || len(f.store.active) != 0 {
		t.Errorf("tokens not dropped: %v %v", f.store.dropped, f.store.active)
	}
	if f.kv.has("daykey:2026-11-24") {
		t.Error("yesterday's day key kept after the rollup")
	}
	var marker string
	raw, _ := f.kv.Get(ctx, kvRolledUp)
	_ = json.Unmarshal(raw, &marker)
	if marker != "2026-11-24" {
		t.Errorf("rollup marker = %q", marker)
	}
	// A restart the same day does not roll up again (no zero over the real count).
	f.c = f.newCollector()
	f.c.Tick(ctx)
	if len(f.store.dropped) != 1 || f.store.value("main", MembersActive, "", day) != 4 {
		t.Errorf("rolled up twice: %v", f.store.dropped)
	}
	// Retention: once a day per enabled server.
	if before := f.store.purges["main"]; !before.Equal(f.clock.Now().AddDate(0, 0, -30)) {
		t.Errorf("purge before = %v", before)
	}
}

func TestRollupCatchesUpMissedDays(t *testing.T) {
	f := newCollectorFixture(t)
	marker, _ := json.Marshal("2026-11-20")
	_ = f.kv.Set(ctx, kvRolledUp, marker, 0)
	_ = f.store.MarkActive(ctx, "main", ActiveSetAll, "2026-11-22", "aa")
	_ = f.store.MarkActive(ctx, "main", ActiveSetAll, "2026-11-23", "bb")
	_ = f.store.MarkActive(ctx, "main", ActiveSetAll, "2026-11-23", "cc")
	f.c.Tick(ctx)
	if v := f.store.value("main", MembersActive, "", time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)); v != 1 {
		t.Errorf("22nd = %v", v)
	}
	if v := f.store.value("main", MembersActive, "", time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)); v != 2 {
		t.Errorf("23rd = %v", v)
	}
	if v := f.store.value("main", MembersActive, "", time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)); v != 0 {
		t.Errorf("21st = %v (zero counts are not written)", v)
	}
}

func TestStoreErrorKeepsTheBuffer(t *testing.T) {
	f := newCollectorFixture(t)
	f.store.setFail(errors.New("database is locked"))
	f.c.Handle("alpha", discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: "5"}}})
	f.c.Flush(ctx)
	f.c.Handle("alpha", discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: "5"}}})
	f.c.Tick(ctx) // the rollup and the purge fail too, without breaking anything
	f.store.setFail(nil)
	f.c.Flush(ctx)
	if v := f.store.value("main", MessagesSent, "10", f.clock.Now()); v != 2 {
		t.Errorf("messages after recovery = %v (%s)", v, f.store.dump())
	}
	if n, _ := f.store.CountActive(ctx, "main", ActiveSetChat, dayOf(f.clock.Now())); n != 1 {
		t.Errorf("active after recovery = %d", n)
	}
}

func TestDimensionsAreCappedPerFlush(t *testing.T) {
	f := newCollectorFixture(t)
	for i := range 150 {
		for range i%3 + 1 {
			f.c.Handle("alpha", discord.ReactionAddEvent{ReactionEvent: discord.ReactionEvent{ServerID: "1", UserID: "5", Emoji: fmt.Sprintf("e%03d:%d", i, i)}})
		}
	}
	f.c.Flush(ctx)
	dims := f.store.dims("main", ReactionsAdded)
	var total float64
	for _, v := range dims {
		total += v
	}
	if len(dims) != maxDimsPerFlush || dims[OtherDim] == 0 || total != 300 {
		t.Errorf("%d dims, other=%v, total=%v", len(dims), dims[OtherDim], total)
	}
}

func TestDiffInvites(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	prev := map[string]discord.InviteUses{
		"abc":  {Code: "abc", Uses: 3},
		"one":  {Code: "one", MaxUses: 1},           // used up and deleted by the join
		"five": {Code: "five", Uses: 2, MaxUses: 5}, // deleted by a moderator: not a use
		"old":  {Code: "old", Uses: 0, MaxUses: 1},  // deleted long ago
		"van":  {Code: "van", Uses: 10, Vanity: true},
	}
	cur := []discord.InviteUses{{Code: "abc", Uses: 5}, {Code: "new", Uses: 1}, {Code: "van", Uses: 11, Vanity: true}}
	deleted := map[string]time.Time{"one": now.Add(-time.Second), "five": now, "old": now.Add(-time.Hour)}
	got := DiffInvites(prev, cur, deleted, now.Add(-10*time.Second))
	want := map[string]int{"abc": 2, "new": 1, "vanity": 1, "one": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("diff = %v, want %v", got, want)
	}
}

func TestInviteAttributionAfterJoins(t *testing.T) {
	f := newCollectorFixture(t)
	f.client.setInvites(discord.InviteUses{Code: "abc", Uses: 1}, discord.InviteUses{Code: "one", MaxUses: 1})
	f.c.Sample(ctx, "main") // baseline
	if f.store.sum("main", MembersInvitedBy) != 0 {
		t.Fatal("baseline counted")
	}
	f.c.Handle("alpha", discord.InviteCreateEvent{ServerID: "1", Code: "fresh"})
	f.client.setInvites(discord.InviteUses{Code: "abc", Uses: 3}, discord.InviteUses{Code: "fresh", Uses: 1})
	f.c.Handle("alpha", discord.InviteDeleteEvent{ServerID: "1", Code: "one"})
	before := f.client.count("invites")
	for range 4 { // a burst of joins: one fetch
		f.c.Handle("alpha", discord.MemberJoinEvent{Member: member("5", false)})
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.client.count("invites") == before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	f.c.Stop(ctx) // waits for the fetch and flushes
	if n := f.client.count("invites") - before; n != 1 {
		t.Errorf("%d invite fetches for a burst", n)
	}
	if got := f.store.dims("main", MembersInvitedBy); fmt.Sprint(got) != fmt.Sprint(map[string]float64{"abc": 2, "fresh": 1, "one": 1}) {
		t.Errorf("invite uses = %v", got)
	}
}

func TestSampling(t *testing.T) {
	f := newCollectorFixture(t, func(s *CollectorServer) {
		s.Presence = true
		s.Intents |= discord.MustIntents("ServerPresences")
	})
	f.client.counts = discord.ServerCounts{ApproximateMembers: 120, ApproximateOnline: 30, Boosts: 7, BoostTier: 2}
	f.client.events = []discord.ScheduledEventInterest{{ID: "e1", Status: "scheduled", Interested: 12}, {ID: "e2", Status: "completed", Interested: 3}}
	pres := func(user, status, desktop, mobile string, acts ...discord.Activity) discord.MemberPresence {
		return discord.MemberPresence{UserID: user, Status: status, Desktop: desktop, Mobile: mobile, Activities: acts}
	}
	f.c.Handle("alpha", discord.ServerAvailableEvent{ServerID: "1", Presences: []discord.MemberPresence{
		pres("5", "online", "online", "", discord.Activity{Type: "playing", Name: "Minecraft"}, discord.Activity{Type: "custom", Name: "Custom Status"}),
		pres("6", "idle", "", "idle", discord.Activity{Type: "playing", Name: "Minecraft"}),
	}})
	f.c.Handle("alpha", discord.PresenceUpdateEvent{ServerID: "1", Presence: pres("7", "dnd", "dnd", "dnd", discord.Activity{Type: "listening", Name: "Spotify"})})
	f.c.Handle("alpha", discord.PresenceUpdateEvent{ServerID: "1", Presence: pres("6", "offline", "", "")})
	f.c.Handle("alpha", discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: "5", ChannelID: "30"}})
	f.c.Tick(ctx)
	h := f.clock.Now()
	checks := map[[2]string]float64{
		{MembersTotal, ""}: 120, {MembersOnline, ""}: 30, {MembersBoosting, ""}: 7, {ServerBoostTier, ""}: 2,
		{EventsInterest, "e1"}: 12, {EventsInterest, "e2"}: 0,
		{PresenceStatus, "online"}: 1, {PresenceStatus, "idle"}: 0, {PresenceStatus, "dnd"}: 1,
		{PresencePlatform, "desktop"}: 2, {PresencePlatform, "mobile"}: 1, {PresencePlatform, "web"}: 0,
		{PresenceActivity, "playing:Minecraft"}: 1, {PresenceActivity, "listening:Spotify"}: 1, {PresenceActivity, "custom:Custom Status"}: 0,
		{VoicePeak, ""}: 1,
	}
	for k, want := range checks {
		if got := f.store.value("main", k[0], k[1], h); got != want {
			t.Errorf("%s[%s] = %v, want %v", k[0], k[1], got, want)
		}
	}
	// Not sampled again before SampleInterval.
	f.clock.add(time.Minute)
	f.c.Tick(ctx)
	if n := f.client.count("counts"); n != 1 {
		t.Errorf("counts read %d times in a minute", n)
	}
	f.clock.add(SampleInterval)
	f.c.Tick(ctx)
	if n := f.client.count("counts"); n != 2 {
		t.Errorf("counts read %d times after the interval", n)
	}
	if st := f.c.Status("main"); !st.Presence || len(st.Missing) != 0 || st.RetentionDays != 30 || !st.Enabled {
		t.Errorf("status = %+v", st)
	}
}

func TestPresenceIgnoredWithoutOptIn(t *testing.T) {
	// The bot has the intent (for a plugin) but spec.analytics.presence is false.
	f := newCollectorFixture(t, func(s *CollectorServer) { s.Intents |= discord.MustIntents("ServerPresences") })
	f.c.Handle("alpha", discord.PresenceUpdateEvent{ServerID: "1", Presence: discord.MemberPresence{UserID: "5", Status: "online"}})
	f.c.Tick(ctx)
	if v := f.store.sum("main", PresenceStatus); v != 0 {
		t.Errorf("presence collected without opt-in: %v", v)
	}
	st := f.c.Status("main")
	if st.Presence || len(st.Missing) != 1 || st.Missing[0].Metrics[0] != PresenceStatus || !strings.Contains(st.Missing[0].Fix, "spec.analytics.presence: true") {
		t.Errorf("status = %+v", st)
	}
}

func TestStatusMissing(t *testing.T) {
	f := newCollectorFixture(t, func(s *CollectorServer) {
		s.Presence = true
		s.Intents = CollectorIntents | discord.MustIntents("Servers")
		s.Permissions = func(context.Context) (discord.Permissions, error) { return discord.MustPermissions("ViewChannel"), nil }
	})
	f.client.inviteErr = fmt.Errorf("list invites: %w", discord.ErrForbidden)
	f.c.Sample(ctx, "main")
	st := f.c.Status("main")
	if !st.Enabled || st.Presence {
		t.Fatalf("status = %+v", st)
	}
	var reasons []string
	for _, m := range st.Missing {
		reasons = append(reasons, m.Metrics[0]+": "+m.Reason+" / "+m.Fix)
	}
	all := strings.Join(reasons, "\n")
	for _, want := range []string{
		MembersJoined + ": the bot alpha does not request the privileged ServerMembers intent",
		"add ServerMembers to spec.intents in bots/alpha.yaml and turn on Server Members Intent in the Discord developer portal (https://discord.com/developers/applications/900/bot",
		MessagesAttachments + ": Discord removes attachments and polls",
		ModerationActions + ": the bot alpha lacks the View Audit Log permission",
		MembersInvitedBy + ": the bot alpha lacks the Manage Server permission",
		PresenceStatus + ": spec.analytics.presence is true but the bot alpha does not request the privileged ServerPresences intent",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if len(st.Missing) != 5 {
		t.Errorf("%d missing groups:\n%s", len(st.Missing), all)
	}

	// A collecting bot that is not connected: nothing is collected.
	f2 := newCollectorFixture(t, func(s *CollectorServer) { s.Connected, s.Client = false, nil })
	if st := f2.c.Status("main"); len(st.Missing) != 1 || len(st.Missing[0].Metrics) != len(Catalog) || !strings.Contains(st.Missing[0].Reason, "not connected") {
		t.Errorf("disconnected status = %+v", st)
	}
	if st := f2.c.Status("unknown"); st.Enabled || st.Missing == nil {
		t.Errorf("unknown server status = %+v", st)
	}
}

func TestAccountAgeBuckets(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for age, want := range map[time.Duration]string{
		time.Hour: "<1d", 3 * 24 * time.Hour: "<7d", 20 * 24 * time.Hour: "<30d", 200 * 24 * time.Hour: "<1y", 800 * 24 * time.Hour: ">=1y",
	} {
		id := snowflakeAt(now.Add(-age))
		if got := AccountAgeBucket(id, now); got != want {
			t.Errorf("age %v: %s, want %s", age, got, want)
		}
		if d := now.Add(-age).Sub(SnowflakeTime(id)); d < 0 || d > time.Millisecond {
			t.Errorf("snowflake time off by %v", d)
		}
	}
	if AccountAgeBucket("not-an-id", now) != ">=1y" {
		t.Error("invalid id bucket")
	}
}

func TestDimensionedMetricsNeverGetAnEmptyDim(t *testing.T) {
	f := newCollectorFixture(t)
	f.c.Handle("alpha", discord.ChannelCreateEvent{Channel: discord.Channel{ID: "20", ServerID: "1"}, NewThread: true}) // no parent
	f.c.Handle("alpha", discord.MessageDeleteEvent{ServerID: "1", ID: "9"})                                             // no channel
	f.c.Flush(ctx)
	for _, m := range Catalog {
		if m.Dim == "" {
			continue
		}
		if _, ok := f.store.dims("main", m.Name)[""]; ok {
			t.Errorf("%s has a \"\" dimension row", m.Name)
		}
	}
}

func TestAttachmentsAndPollsNeedMessageContent(t *testing.T) {
	f := newCollectorFixture(t, func(s *CollectorServer) { s.Intents &^= discord.MustIntents("MessageContent") })
	// A message mentioning the bot keeps its attachments and poll even without the intent.
	f.c.Handle("alpha", discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: "10", Author: discord.User{ID: "5"},
		Content: "<@600> look", Attachments: []discord.Attachment{{ID: "a"}}, Poll: true, StickerIDs: []string{"s"}, ReplyToID: "4"}})
	f.c.Flush(ctx)
	if a, p := f.store.sum("main", MessagesAttachments), f.store.sum("main", PollsCreated); a != 0 || p != 0 {
		t.Errorf("attachments=%v polls=%v counted without MessageContent", a, p)
	}
	if f.store.sum("main", MessagesSent) != 1 || f.store.sum("main", MessagesReplies) != 1 || f.store.sum("main", MessagesStickers) != 1 {
		t.Errorf("messages, replies and stickers need no MessageContent: %s", f.store.dump())
	}
}
