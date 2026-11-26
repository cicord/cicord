package disgoadapter

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
	discordapi "github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

type pollMedia struct {
	Text  string        `json:"text"`
	Emoji *wireEmojiRef `json:"emoji,omitempty"`
}
type wireEmojiRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}
type wireAnswer struct {
	ID    int       `json:"answer_id,omitempty"`
	Media pollMedia `json:"poll_media"`
}
type wirePoll struct {
	Question pollMedia    `json:"question"`
	Answers  []wireAnswer `json:"answers"`
	Duration int          `json:"duration,omitempty"`
	Multi    bool         `json:"allow_multiselect"`
	Results  *struct {
		Final  bool `json:"is_finalized"`
		Counts []struct {
			ID    int `json:"id"`
			Count int `json:"count"`
		} `json:"answer_counts"`
	} `json:"results,omitempty"`
}

func pollToWire(p *discord.Poll) *wirePoll {
	if p == nil {
		return nil
	}
	w := &wirePoll{Question: pollMedia{Text: p.Question}, Multi: p.Multiselect, Duration: p.DurationHours}
	for _, a := range p.Answers {
		media := pollMedia{Text: a.Text}
		if a.Emoji != "" {
			media.Emoji = &wireEmojiRef{Name: a.Emoji}
			if discord.IsSnowflake(a.Emoji) {
				media.Emoji = &wireEmojiRef{ID: a.Emoji}
			}
		}
		w.Answers = append(w.Answers, wireAnswer{Media: media})
	}
	return w
}
func pollFromWire(w *wirePoll) *discord.Poll {
	if w == nil {
		return nil
	}
	p := &discord.Poll{Question: w.Question.Text, Multiselect: w.Multi}
	counts := map[int]int{}
	if w.Results != nil {
		p.Finalized = w.Results.Final
		for _, c := range w.Results.Counts {
			counts[c.ID] = c.Count
		}
	}
	for _, a := range w.Answers {
		ans := discord.PollAnswer{ID: a.ID, Text: a.Media.Text, Votes: counts[a.ID]}
		if e := a.Media.Emoji; e != nil {
			ans.Emoji = e.Name
			if e.ID != "" {
				ans.Emoji = e.ID
			}
		}
		p.Answers = append(p.Answers, ans)
	}
	return p
}
func (s *session) EndPoll(ctx context.Context, c, m string) (discord.Message, error) {
	var w wireMessage
	if err := checkIDs("EndPoll", c, m); err != nil {
		return discord.Message{}, err
	}
	err := s.do(ctx, "EndPoll", rest.ExpirePoll.Compile(nil, c, m), nil, &w)
	return messageFromWire(w), err
}
func (s *session) PollVoters(ctx context.Context, c, m string, a int, after string) ([]string, error) {
	q := discordapi.QueryValues{"limit": 100}
	if after != "" {
		q["after"] = after
	}
	var out struct {
		Users []wireUser `json:"users"`
	}
	err := s.do(ctx, "PollVoters", rest.GetPollAnswerVotes.Compile(q, c, m, a), nil, &out)
	ids := []string{}
	for _, u := range out.Users {
		ids = append(ids, u.ID)
	}
	return ids, err
}

type wireEvent struct {
	ID          string     `json:"id,omitempty"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Start       time.Time  `json:"scheduled_start_time"`
	End         *time.Time `json:"scheduled_end_time,omitempty"`
	Channel     string     `json:"channel_id,omitempty"`
	Metadata    *struct {
		Location string `json:"location"`
	} `json:"entity_metadata,omitempty"`
	Privacy    int             `json:"privacy_level"`
	Entity     int             `json:"entity_type"`
	Image      string          `json:"image,omitempty"`
	Recurrence json.RawMessage `json:"recurrence_rule,omitempty"`
	Status     int             `json:"status,omitempty"`
}

func eventWire(e discord.ScheduledEvent) wireEvent {
	w := wireEvent{Name: e.Name, Description: e.Description, Start: e.Start, End: e.End, Image: e.Image, Recurrence: e.Recurrence, Channel: e.Channel, Privacy: 2, Entity: 2}
	if e.Location != "" {
		w.Entity = 3
		w.Metadata = &struct {
			Location string `json:"location"`
		}{e.Location}
	}
	return w
}
func (w wireEvent) event() discord.ScheduledEvent {
	e := discord.ScheduledEvent{ID: w.ID, Name: w.Name, Description: w.Description, Start: w.Start, End: w.End, Channel: w.Channel, Recurrence: w.Recurrence, Status: scheduledEventStatuses[w.Status]}
	if w.Metadata != nil {
		e.Location = w.Metadata.Location
	}
	return e
}
func (s *session) ScheduledEvents(ctx context.Context, id string) ([]discord.ScheduledEvent, error) {
	var ws []wireEvent
	err := s.do(ctx, "ScheduledEvents", rest.GetGuildScheduledEvents.Compile(nil, id), nil, &ws)
	out := []discord.ScheduledEvent{}
	for _, w := range ws {
		out = append(out, w.event())
	}
	return out, err
}
func (s *session) CreateEvent(ctx context.Context, id string, e discord.ScheduledEvent) (discord.ScheduledEvent, error) {
	var w wireEvent
	err := s.do(ctx, "CreateEvent", rest.CreateGuildScheduledEvent.Compile(nil, id), eventWire(e), &w)
	return w.event(), err
}
func (s *session) EditEvent(ctx context.Context, id string, e discord.ScheduledEvent) (discord.ScheduledEvent, error) {
	var w wireEvent
	err := s.do(ctx, "EditEvent", rest.UpdateGuildScheduledEvent.Compile(nil, id, e.ID), eventWire(e), &w)
	return w.event(), err
}
func (s *session) DeleteEvent(ctx context.Context, id, event string) error {
	return s.do(ctx, "DeleteEvent", rest.DeleteGuildScheduledEvent.Compile(nil, id, event), nil, nil)
}
func (s *session) EventUsers(ctx context.Context, id, event, after string) ([]string, error) {
	q := discordapi.QueryValues{"limit": 100}
	if after != "" {
		q["after"] = after
	}
	var out []struct {
		User wireUser `json:"user"`
	}
	err := s.do(ctx, "EventUsers", rest.GetGuildScheduledEventUsers.Compile(q, id, event), nil, &out)
	ids := []string{}
	for _, u := range out {
		ids = append(ids, u.User.ID)
	}
	return ids, err
}

var communityHandlers = map[string]string{
	"GUILD_SCHEDULED_EVENT_CREATE":      "scheduledEventCreate",
	"GUILD_SCHEDULED_EVENT_UPDATE":      "scheduledEventUpdate",
	"GUILD_SCHEDULED_EVENT_DELETE":      "scheduledEventDelete",
	"GUILD_SCHEDULED_EVENT_USER_ADD":    "scheduledEventUserAdd",
	"GUILD_SCHEDULED_EVENT_USER_REMOVE": "scheduledEventUserRemove",
	"MESSAGE_POLL_VOTE_ADD":             "pollVoteAdd",
	"MESSAGE_POLL_VOTE_REMOVE":          "pollVoteRemove",
}

func communityDispatch(name string, d []byte) ([]discord.Event, bool, error) {
	n, ok := communityHandlers[name]
	if !ok {
		return nil, false, nil
	}
	var data struct {
		wireEvent
		Guild   string `json:"guild_id"`
		EventID string `json:"guild_scheduled_event_id"`
		User    string `json:"user_id"`
		Message string `json:"message_id"`
		Answer  int    `json:"answer_id"`
	}
	if err := json.Unmarshal(d, &data); err != nil {
		return nil, true, err
	}
	if data.Guild == "" {
		return nil, true, nil
	}
	ev := discord.CommunityEvent{Name: n, ServerID: data.Guild, Event: data.event(), EventID: data.EventID, UserID: data.User, MessageID: data.Message, ChannelID: data.Channel, AnswerID: data.Answer}
	if ev.EventID == "" && strings.HasPrefix(n, "scheduledEvent") {
		ev.EventID = data.ID
	}
	return []discord.Event{ev}, true, nil
}
