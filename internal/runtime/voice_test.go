package runtime

import (
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestVoiceCache(t *testing.T) {
	c := newVoiceCache(discord.MustIntents("Servers", "ServerVoiceStates"))
	if _, known := c.States("1"); known {
		t.Fatal("known before the first snapshot")
	}
	// Events before the snapshot still give the previous state they know.
	ev := c.Observe(discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: "5", ChannelID: "30"}}).(discord.VoiceStateUpdateEvent)
	if ev.Previous != nil {
		t.Fatalf("previous = %+v", ev.Previous)
	}
	if _, known := c.States("1"); known {
		t.Fatal("an event is not a snapshot")
	}
	alice := discord.Member{ServerID: "1", User: discord.User{ID: "7", Username: "alice"}}
	c.Observe(discord.ServerAvailableEvent{ServerID: "1", VoiceStates: []discord.VoiceState{
		{ServerID: "1", UserID: "7", ChannelID: "30", Member: &alice},
		{ServerID: "1", UserID: "12", ChannelID: "31"},
	}})
	states, known := c.States("1")
	if !known || len(states) != 2 || states[0].UserID != "7" || states[1].UserID != "12" {
		t.Fatalf("states = %+v %v", states, known)
	}
	// A move: previous is the cached state; a state without its member keeps the cached one.
	ev = c.Observe(discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: "7", ChannelID: "31"}}).(discord.VoiceStateUpdateEvent)
	if ev.Previous == nil || ev.Previous.ChannelID != "30" {
		t.Fatalf("previous = %+v", ev.Previous)
	}
	// Member updates refresh the member of a voice state.
	c.Observe(discord.MemberUpdateEvent{Member: discord.Member{ServerID: "1", User: alice.User, Nick: "Al"}})
	states, _ = c.States("1")
	if states[0].ChannelID != "31" || states[0].Member == nil || states[0].Member.Nick != "Al" {
		t.Fatalf("after move = %+v", states[0])
	}
	// A leave removes the state.
	ev = c.Observe(discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "1", UserID: "12"}}).(discord.VoiceStateUpdateEvent)
	if ev.Previous == nil || ev.Previous.ChannelID != "31" {
		t.Fatalf("leave previous = %+v", ev.Previous)
	}
	if states, _ = c.States("1"); len(states) != 1 {
		t.Fatalf("after leave = %+v", states)
	}
	c.Observe(discord.ServerLeaveEvent{ServerID: "1"})
	if _, known := c.States("1"); known {
		t.Fatal("known after the bot left")
	}
	// Without the intent, the snapshots carry no voice state: nothing is ever known.
	off := newVoiceCache(discord.MustIntents("Servers"))
	off.Observe(discord.ServerAvailableEvent{ServerID: "1"})
	if _, known := off.States("1"); known {
		t.Fatal("known without the ServerVoiceStates intent")
	}
}
