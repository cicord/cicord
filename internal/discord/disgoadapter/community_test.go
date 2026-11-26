package disgoadapter

import (
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

func TestNativePollAndScheduledEventREST(t *testing.T) {
	f := newFakeDiscord(t)
	const poll = `{"id":"31","channel_id":"10","author":{"id":"5"},"poll":{"question":{"text":"When?"},"answers":[{"answer_id":1,"poll_media":{"text":"Friday"}},{"answer_id":2,"poll_media":{"text":"Saturday"}}],"results":{"is_finalized":true,"answer_counts":[{"id":2,"count":27}]}}}`
	f.json("POST /channels/10/messages", 200, poll)
	f.json("POST /channels/10/polls/31/expire", 200, poll)
	f.json("GET /channels/10/polls/31/answers/2", 200, `{"users":[{"id":"4"}]}`)
	const event = `{"id":"41","name":"Night","scheduled_start_time":"2026-12-01T12:00:00Z","scheduled_end_time":"2026-12-01T13:00:00Z","entity_metadata":{"location":"Hall"},"status":1}`
	f.json("POST /guilds/1/scheduled-events", 200, event)
	f.json("PATCH /guilds/1/scheduled-events/41", 200, event)
	f.json("GET /guilds/1/scheduled-events/41/users", 200, `[{"user":{"id":"8"}}]`)
	f.json("DELETE /guilds/1/scheduled-events/41", 204, "")
	session := f.connect(t)
	pc := session.(discord.PollClient)
	ec := session.(discord.ScheduledEvents)
	ctx := t.Context()
	m, err := session.SendMessage(ctx, "10", discord.MessageCreate{Poll: &discord.Poll{Question: "When?", DurationHours: 48, Multiselect: true, Answers: []discord.PollAnswer{{Text: "Friday"}, {Text: "Saturday"}}}})
	if err != nil || !m.Poll || m.PollResults.Answers[1].Votes != 27 {
		t.Fatalf("create poll: %+v %v", m, err)
	}
	for _, field := range []string{`"duration":48`, `"allow_multiselect":true`, `"poll_media":{"text":"Saturday"}`} {
		if !strings.Contains(string(f.last().Raw), field) {
			t.Fatalf("missing %q in %s", field, f.last().Raw)
		}
	}
	m, err = pc.EndPoll(ctx, "10", "31")
	if err != nil || !m.PollResults.Finalized {
		t.Fatal(err, m)
	}
	ids, err := pc.PollVoters(ctx, "10", "31", 2, "3")
	if err != nil || len(ids) != 1 || ids[0] != "4" {
		t.Fatal(ids, err)
	}
	start := time.Now().UTC().Add(time.Hour)
	end := start.Add(time.Hour)
	e, err := ec.CreateEvent(ctx, "1", discord.ScheduledEvent{Name: "Night", Start: start, End: &end, Location: "Hall"})
	if err != nil || e.ID != "41" || e.Location != "Hall" {
		t.Fatal(e, err)
	}
	if !strings.Contains(string(f.last().Raw), `"entity_type":3`) {
		t.Fatal(string(f.last().Raw))
	}
	if _, err = ec.EditEvent(ctx, "1", e); err != nil {
		t.Fatal(err)
	}
	ids, err = ec.EventUsers(ctx, "1", "41", "7")
	if err != nil || len(ids) != 1 || ids[0] != "8" {
		t.Fatal(ids, err)
	}
	if err = ec.DeleteEvent(ctx, "1", "41"); err != nil {
		t.Fatal(err)
	}
}
func TestCommunityGateway(t *testing.T) {
	for name, expected := range map[string]string{"GUILD_SCHEDULED_EVENT_CREATE": "scheduledEventCreate", "GUILD_SCHEDULED_EVENT_UPDATE": "scheduledEventUpdate", "GUILD_SCHEDULED_EVENT_DELETE": "scheduledEventDelete", "GUILD_SCHEDULED_EVENT_USER_ADD": "scheduledEventUserAdd", "GUILD_SCHEDULED_EVENT_USER_REMOVE": "scheduledEventUserRemove", "MESSAGE_POLL_VOTE_ADD": "pollVoteAdd", "MESSAGE_POLL_VOTE_REMOVE": "pollVoteRemove"} {
		events, ok, err := communityDispatch(name, []byte(`{"guild_id":"1","channel_id":"10","guild_scheduled_event_id":"21","user_id":"30","message_id":"4","answer_id":2}`))
		if err != nil || !ok || len(events) != 1 {
			t.Fatal(name, events, err)
		}
		e := events[0].(discord.CommunityEvent)
		if e.Name != expected || discord.ServerIDOf(e) != "1" || e.UserID != "30" {
			t.Fatal(e)
		}
	}
}
