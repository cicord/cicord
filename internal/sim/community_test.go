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
	for _, as := range []string{f.alice.ID, f.owner.ID} {
		view, err := f.w.UserClient(as).GetMessage(ctx, c, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		answers := view.PollResults.Answers
		if answers[0].MeVoted || answers[1].MeVoted != (as == f.alice.ID) || answers[1].Votes != 1 {
			t.Fatalf("user %s received another voter's selection: %+v", as, answers)
		}
	}
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

func TestNativePollMemberWithdrawsAndAuthorCloses(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	author := f.w.UserClient(f.owner.ID)
	voter := f.w.UserClient(f.alice.ID)
	channel := f.general.ID
	m, err := author.SendMessage(ctx, channel, discord.MessageCreate{Poll: &discord.Poll{Question: "QA", DurationHours: 1, Answers: []discord.PollAnswer{{Text: "A"}, {Text: "B"}}}})
	isErr(t, err, nil)
	for _, step := range []struct {
		answer int
		add    bool
		a, b   int
	}{{2, true, 0, 1}, {2, false, 0, 0}, {1, true, 1, 0}} {
		isErr(t, f.w.VotePoll(f.alice.ID, channel, m.ID, step.answer, step.add), nil)
		for _, viewer := range []*Client{author, voter} {
			message, err := viewer.GetMessage(ctx, channel, m.ID)
			isErr(t, err, nil)
			if message.PollResults.Answers[0].Votes != step.a || message.PollResults.Answers[1].Votes != step.b {
				t.Fatal(message.PollResults)
			}
		}
	}
	_, err = voter.EndPoll(ctx, channel, m.ID)
	isErr(t, err, discord.ErrForbidden)
	final, err := author.EndPoll(ctx, channel, m.ID)
	isErr(t, err, nil)
	if !final.PollResults.Finalized || final.PollResults.Answers[0].Votes != 1 || final.PollResults.Answers[1].Votes != 0 {
		t.Fatal(final.PollResults)
	}
}
