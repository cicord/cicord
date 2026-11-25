package runtime

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// ───────────────────────────────────────────────────────────── voice rooms

// voiceOf is the voice channel a member is connected to ("" when not in voice).
func (s *showcase) voiceOf(userID string) string {
	for _, st := range s.world.VoiceStates(s.serverID) {
		if st.UserID == userID {
			return st.ChannelID
		}
	}
	return ""
}

// overwriteOf is the overwrite of a target in a channel.
func overwriteOf(ch discord.Channel, id string) (discord.Overwrite, bool) {
	i := slices.IndexFunc(ch.Overwrites, func(o discord.Overwrite) bool { return o.ID == id })
	if i < 0 {
		return discord.Overwrite{}, false
	}
	return ch.Overwrites[i], true
}

func (s *showcase) liveChannel(id string) (discord.Channel, bool) { return s.world.Channel(id) }

// voiceCommand runs /voice <sub> as userID from channelID and returns the reply.
func (s *showcase) voiceCommand(userID, channelID, sub string, options map[string]any) string {
	s.t.Helper()
	st := wait(s.t, s.world.InvokeCommand(userID, s.serverID, channelID, s.appID, "voice "+sub, options))
	return replyText(st)
}

func TestShowcaseVoiceRooms(t *testing.T) {
	s := startShowcase(t, showcaseOpts{})
	w := s.world
	ctx := context.Background()
	hub, guildHub, general := s.channel("➕ Create a channel"), s.channel("➕ Create a guild channel"), s.channel("general")
	voiceCat, guildsCat := s.channel("Voice"), s.channel("Guilds")
	alice, bob, carl := s.member("alice"), s.member("bob"), s.member("carl", "Guild")

	// Joining the hub: a room named after the member, in the hub's category, owned by them;
	// they are moved into it.
	if err := w.JoinVoice(alice.ID, hub.ID); err != nil {
		t.Fatal(err)
	}
	var room discord.Channel
	s.waitFor("alice's room", func() bool {
		var ok bool
		room, ok = w.ChannelByName(s.serverID, "🔊 alice's channel")
		return ok && s.voiceOf(alice.ID) == room.ID
	})
	if room.Type != discord.ChannelVoice || room.ParentID != voiceCat.ID {
		t.Fatalf("room = %+v", room)
	}
	s.waitFor("owner permissions", func() bool {
		room, _ = s.liveChannel(room.ID)
		_, owner := overwriteOf(room, alice.ID)
		_, bot := overwriteOf(room, s.bot.ID)
		return owner && bot
	})
	if ow, _ := overwriteOf(room, alice.ID); ow.Allow != discord.MustPermissions("ViewChannel", "Connect", "Speak", "Stream", "MoveMembers") {
		t.Errorf("owner overwrite = %v", ow.Allow.Names())
	}
	res, err := s.store.Resources().GetByID(ctx, showcaseServer, room.ID)
	if err != nil || res.Owner != "plugin:cicord/voice-rooms" || res.Kind != state.KindChannel {
		t.Fatalf("ownership = %+v, %v", res, err)
	}

	// The reconciler (prune is on) leaves the room alone: no change planned, no drift.
	plan, err := s.rt.PlanServer(ctx, showcaseServer)
	if err != nil || len(plan.Changes) != 0 {
		t.Fatalf("plan with a room = %+v, %v", plan.Changes, err)
	}
	if _, _, err := s.rt.SyncServer(ctx, showcaseServer); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.liveChannel(room.ID); !ok {
		t.Fatal("a sync deleted the room")
	}
	if drift, err := s.rt.Drift(ctx, showcaseServer); err != nil || len(drift) != 0 {
		t.Fatalf("drift = %+v, %v", drift, err)
	}

	// The guild hub: only for the Guild role; rooms in the private guild category.
	if err := w.JoinVoice(bob.ID, guildHub.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("bob disconnected from the guild hub", func() bool { return s.voiceOf(bob.ID) == "" })
	if err := w.JoinVoice(carl.ID, guildHub.ID); err != nil {
		t.Fatal(err)
	}
	var guildRoom discord.Channel
	s.waitFor("carl's guild room", func() bool {
		var ok bool
		guildRoom, ok = w.ChannelByName(s.serverID, "🛡️ Guild room #1")
		return ok && s.voiceOf(carl.ID) == guildRoom.ID
	})
	if guildRoom.ParentID != guildsCat.ID || guildRoom.UserLimit != 10 {
		t.Fatalf("guild room = %+v", guildRoom)
	}
	// Synced with its category: still private.
	if ow, ok := overwriteOf(guildRoom, s.serverID); !ok || !ow.Deny.Has(discord.MustPermissions("ViewChannel")) {
		t.Fatalf("guild room overwrites = %+v", guildRoom.Overwrites)
	}

	// /voice lock: bob cannot join any more; /voice permit lets him in.
	if got := s.voiceCommand(alice.ID, general.ID, "lock", nil); !strings.HasPrefix(got, "Locked") {
		t.Fatalf("lock = %q", got)
	}
	if err := w.JoinVoice(bob.ID, room.ID); err == nil {
		t.Fatal("bob joined a locked room")
	}
	if got := s.voiceCommand(bob.ID, general.ID, "limit", map[string]any{"count": 2}); !strings.Contains(got, "Join your voice room first") {
		t.Fatalf("limit by a non-owner outside = %q", got)
	}
	s.voiceCommand(alice.ID, general.ID, "permit", map[string]any{"member": bob.ID})
	if err := w.JoinVoice(bob.ID, room.ID); err != nil {
		t.Fatalf("permitted bob: %v", err)
	}
	if got := s.voiceCommand(bob.ID, general.ID, "limit", map[string]any{"count": 2}); !strings.Contains(got, "only they (or the staff)") {
		t.Fatalf("limit by a guest = %q", got)
	}
	s.voiceCommand(alice.ID, general.ID, "limit", map[string]any{"count": 2})
	if ch, _ := s.liveChannel(room.ID); ch.UserLimit != 2 {
		t.Fatalf("limit = %d", ch.UserLimit)
	}

	// /voice kick disconnects bob and keeps him out.
	s.voiceCommand(alice.ID, general.ID, "kick", map[string]any{"member": bob.ID})
	s.waitFor("bob kicked", func() bool { return s.voiceOf(bob.ID) == "" })
	if err := w.JoinVoice(bob.ID, room.ID); err == nil {
		t.Fatal("kicked bob joined again")
	}

	// /voice transfer to dave (in the room), then alice leaves: the room stays, dave owns it.
	dave := s.member("dave")
	s.voiceCommand(alice.ID, general.ID, "permit", map[string]any{"member": dave.ID})
	if err := w.JoinVoice(dave.ID, room.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.voiceCommand(alice.ID, general.ID, "transfer", map[string]any{"member": dave.ID}); !strings.Contains(got, "now belongs to") {
		t.Fatalf("transfer = %q", got)
	}
	if err := w.LeaveVoice(s.serverID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.voiceCommand(dave.ID, general.ID, "rename", map[string]any{"name": "Dave's den"}); !strings.Contains(got, "Renamed") {
		t.Fatalf("rename by the new owner = %q", got)
	}
	s.waitFor("renamed", func() bool { ch, _ := s.liveChannel(room.ID); return ch.Name == "Dave's den" })

	// The last member leaves: the room is deleted, forgotten, and its ownership record too.
	if err := w.LeaveVoice(s.serverID, dave.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("room deleted", func() bool { _, ok := s.liveChannel(room.ID); return !ok })
	s.waitFor("ownership forgotten", func() bool {
		_, err := s.store.Resources().GetByID(ctx, showcaseServer, room.ID)
		return err != nil
	})
	if v, _ := s.kv("voice-rooms").Get(ctx, "room:"+room.ID); v != nil {
		t.Fatalf("room still in KV: %s", v)
	}

	// Moving from a room to another channel empties it too. carl got a room a moment ago:
	// the cooldown (5 s by default) disconnects him from the hub instead of creating another.
	if err := w.JoinVoice(carl.ID, hub.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("guild room deleted", func() bool { _, ok := s.liveChannel(guildRoom.ID); return !ok })
	s.waitFor("carl disconnected (cooldown)", func() bool { return s.voiceOf(carl.ID) == "" })
	if _, ok := w.ChannelByName(s.serverID, "🔊 carl's channel"); ok {
		t.Fatal("a room was created during the cooldown")
	}
	if !strings.Contains(s.logs.String(), "voice room deleted") {
		t.Error("no deletion logged")
	}
}
