package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

type PollAnswer struct {
	ID    int    `json:"id,omitempty"`
	Text  string `json:"text"`
	Emoji string `json:"emoji,omitempty"`
	Votes int    `json:"votes"`
}
type Poll struct {
	Question      string       `json:"question"`
	Answers       []PollAnswer `json:"answers"`
	DurationHours int          `json:"durationHours,omitempty"`
	Multiselect   bool         `json:"multiselect"`
	Finalized     bool         `json:"finalized"`
}

func (p Poll) Validate() error {
	if len(p.Question) == 0 || utf8.RuneCountInString(p.Question) > 300 || len(p.Answers) < 2 || len(p.Answers) > 10 || p.DurationHours < 1 || p.DurationHours > 768 {
		return fmt.Errorf("%w: poll needs a question, 2–10 answers and a duration of 1–768 whole hours", ErrBadRequest)
	}
	for _, a := range p.Answers {
		if len(a.Text) == 0 || utf8.RuneCountInString(a.Text) > 55 {
			return fmt.Errorf("%w: poll answers must be 1–55 characters", ErrBadRequest)
		}
	}
	return nil
}

type ScheduledEvent struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Start       time.Time       `json:"start"`
	End         *time.Time      `json:"end,omitempty"`
	Channel     string          `json:"channel,omitempty"`
	Location    string          `json:"location,omitempty"`
	Image       string          `json:"image,omitempty"`
	Recurrence  json.RawMessage `json:"recurrence,omitempty"`
	Status      string          `json:"status,omitempty"`
}
type ScheduledEvents interface {
	ScheduledEvents(context.Context, string) ([]ScheduledEvent, error)
	CreateEvent(context.Context, string, ScheduledEvent) (ScheduledEvent, error)
	EditEvent(context.Context, string, ScheduledEvent) (ScheduledEvent, error)
	DeleteEvent(context.Context, string, string) error
	EventUsers(context.Context, string, string, string) ([]string, error)
}
type PollClient interface {
	EndPoll(context.Context, string, string) (Message, error)
	PollVoters(context.Context, string, string, int, string) ([]string, error)
}
type CommunityEvent struct {
	Name      string         `json:"-"`
	ServerID  string         `json:"-"`
	Event     ScheduledEvent `json:"event"`
	EventID   string         `json:"eventId,omitempty"`
	UserID    string         `json:"userId,omitempty"`
	ChannelID string         `json:"channelId,omitempty"`
	MessageID string         `json:"messageId,omitempty"`
	AnswerID  int            `json:"answerId,omitempty"`
}

func (CommunityEvent) isEvent() {}
