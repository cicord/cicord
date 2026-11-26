package sim

import (
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

func TestCommunityLifecycle(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("Servers", "ServerScheduledEvents", "ServerMessagePolls")})
	admin := f.w.AdminClient(f.srv.ID)
	c := f.general.ID
	m, err := admin.SendMessage(ctx, c, discord.MessageCreate{Poll: &discord.Poll{Question: "Q", DurationHours: 12, Answers: []discord.PollAnswer{{Text: "A"}, {Text: "B"}}}})
	isErr(t, err, nil)
	isErr(t, f.w.VotePoll(f.alice.ID, c, m.ID, 1, true), nil)
	isErr(t, f.w.VotePoll(f.alice.ID, c, m.ID, 2, true), nil)
	ids, err := admin.PollVoters(ctx, c, m.ID, 2, "")
	isErr(t, err, nil)
	if len(ids) != 1 || ids[0] != f.alice.ID {
		t.Fatal(ids)
	}
	m, err = admin.EndPoll(ctx, c, m.ID)
	isErr(t, err, nil)
	if !m.PollResults.Finalized || m.PollResults.Answers[0].Votes != 0 || m.PollResults.Answers[1].Votes != 1 {
		t.Fatal(m.PollResults)
	}
	if err = f.w.VotePoll(f.alice.ID, c, m.ID, 1, true); err == nil {
		t.Fatal("vote after closure")
	}
	start := time.Now().Add(time.Hour)
	end := start.Add(time.Hour)
	e, err := admin.CreateEvent(ctx, f.srv.ID, discord.ScheduledEvent{Name: "Night", Start: start, End: &end, Location: "Hall"})
	isErr(t, err, nil)
	isErr(t, f.w.SetEventInterest(f.srv.ID, e.ID, f.alice.ID, true), nil)
	ids, err = admin.EventUsers(ctx, f.srv.ID, e.ID, "")
	isErr(t, err, nil)
	if len(ids) != 1 || ids[0] != f.alice.ID {
		t.Fatal(ids)
	}
	isErr(t, admin.DeleteEvent(ctx, f.srv.ID, e.ID), nil)
	es, err := admin.ScheduledEvents(ctx, f.srv.ID)
	if err != nil || len(es) != 0 {
		t.Fatal(es, err)
	}
	evs := simEvents[discord.CommunityEvent](f)
	if len(evs) != 6 {
		t.Fatal(evs)
	}
}
