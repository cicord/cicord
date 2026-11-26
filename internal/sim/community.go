package sim

import (
	"context"
	"slices"
	"strings"

	"github.com/cicord/cicord/internal/discord"
)

func (c *Client) scheduledAccess(ctx context.Context, id string, manage bool) (*serverActivity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := c.w.server(id)
	if err != nil {
		return nil, err
	}
	if s.members[c.a.userID] == nil {
		return nil, forbidden("server membership required")
	}
	if manage {
		if err = c.w.requireServer(c.a, s, discord.MustPermissions("ManageEvents")); err != nil {
			return nil, err
		}
	}
	return c.w.activity(id), nil
}
func (c *Client) ScheduledEvents(ctx context.Context, id string) ([]discord.ScheduledEvent, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	a, err := c.scheduledAccess(ctx, id, false)
	if err != nil {
		return nil, err
	}
	out := []discord.ScheduledEvent{}
	for _, eid := range a.eventIDs {
		if e, ok := a.scheduled[eid]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}
func (c *Client) CreateEvent(ctx context.Context, id string, e discord.ScheduledEvent) (discord.ScheduledEvent, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	a, err := c.scheduledAccess(ctx, id, true)
	if err != nil {
		return e, err
	}
	e.ID = c.w.newID()
	e.Status = "scheduled"
	a.scheduled[e.ID] = e
	a.eventIDs = append(a.eventIDs, e.ID)
	a.events[e.ID] = &discord.ScheduledEventInterest{ID: e.ID, Name: e.Name, Status: e.Status}
	c.w.emit(discord.CommunityEvent{Name: "scheduledEventCreate", ServerID: id, Event: e, EventID: e.ID})
	return e, nil
}
func (c *Client) EditEvent(ctx context.Context, id string, e discord.ScheduledEvent) (discord.ScheduledEvent, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	a, err := c.scheduledAccess(ctx, id, true)
	if err != nil {
		return e, err
	}
	old, ok := a.scheduled[e.ID]
	if !ok {
		return e, notFound("unknown event")
	}
	e.Status = old.Status
	a.scheduled[e.ID] = e
	a.events[e.ID].Name = e.Name
	c.w.emit(discord.CommunityEvent{Name: "scheduledEventUpdate", ServerID: id, Event: e, EventID: e.ID})
	return e, nil
}
func (c *Client) DeleteEvent(ctx context.Context, id, eid string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	a, err := c.scheduledAccess(ctx, id, true)
	if err != nil {
		return err
	}
	e, ok := a.scheduled[eid]
	if !ok {
		return notFound("unknown event")
	}
	delete(a.scheduled, eid)
	delete(a.events, eid)
	delete(a.interested, eid)
	a.eventIDs = slices.DeleteFunc(a.eventIDs, func(i string) bool { return i == eid })
	c.w.emit(discord.CommunityEvent{Name: "scheduledEventDelete", ServerID: id, Event: e, EventID: eid})
	return nil
}
func idPage(ids map[string]bool, after string) []string {
	out := []string{}
	for id := range ids {
		if after == "" || len(id) > len(after) || len(id) == len(after) && id > after {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
	return out[:min(100, len(out))]
}
func (c *Client) EventUsers(ctx context.Context, id, eid, after string) ([]string, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	a, err := c.scheduledAccess(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if _, ok := a.scheduled[eid]; !ok {
		return nil, notFound("unknown event")
	}
	return idPage(a.interested[eid], after), nil
}
func (w *World) SetEventInterest(id, eid, user string, joined bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(id)
	if err != nil {
		return err
	}
	a := w.activity(id)
	if _, ok := a.scheduled[eid]; !ok || s.members[user] == nil {
		return notFound("unknown event or member")
	}
	us := a.interested[eid]
	if us == nil {
		us = map[string]bool{}
		a.interested[eid] = us
	}
	if us[user] == joined {
		return nil
	}
	name := "scheduledEventUserAdd"
	if joined {
		us[user] = true
	} else {
		delete(us, user)
		name = "scheduledEventUserRemove"
	}
	a.events[eid].Interested = len(us)
	w.emit(discord.CommunityEvent{Name: name, ServerID: id, EventID: eid, UserID: user})
	return nil
}
func (c *Client) EndPoll(ctx context.Context, ch, id string) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	ms := c.w.messages[id]
	if ms == nil || ms.msg.ChannelID != ch || ms.msg.PollResults == nil {
		return discord.Message{}, notFound("unknown poll")
	}
	if ms.msg.Author.ID != c.a.userID {
		return discord.Message{}, forbidden("only the author may end the poll")
	}
	ms.msg.PollResults.Finalized = true
	c.w.emit(discord.MessageUpdateEvent{Message: cloneMessage(ms.msg)})
	return cloneMessage(ms.msg), nil
}
func (c *Client) PollVoters(ctx context.Context, ch, id string, a int, after string) ([]string, error) {
	if _, err := c.GetMessage(ctx, ch, id); err != nil {
		return nil, err
	}
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	ms := c.w.messages[id]
	if ms == nil || ms.msg.PollResults == nil {
		return nil, notFound("unknown poll")
	}
	return idPage(ms.votes[a], after), nil
}
func (w *World) VotePoll(user, ch, id string, answer int, add bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ms := w.messages[id]
	if ms == nil || ms.msg.ChannelID != ch || ms.msg.PollResults == nil {
		return notFound("unknown poll")
	}
	p := ms.msg.PollResults
	if answer < 1 || answer > len(p.Answers) || p.Finalized || !w.now().Before(ms.expires) {
		return badRequest("invalid answer or closed poll")
	}
	if err := w.requireChannel(actor{userID: user}, w.channels[ch], discord.MustPermissions("ViewChannel", "ReadMessageHistory")); err != nil {
		return err
	}
	if ms.votes[answer] == nil {
		ms.votes[answer] = map[string]bool{}
	}
	if ms.votes[answer][user] == add {
		return nil
	}
	change := func(a int, on bool) {
		name := "pollVoteRemove"
		if on {
			ms.votes[a][user] = true
			p.Answers[a-1].Votes++
			name = "pollVoteAdd"
		} else {
			delete(ms.votes[a], user)
			p.Answers[a-1].Votes--
		}
		w.emit(discord.CommunityEvent{Name: name, ServerID: ms.msg.ServerID, ChannelID: ch, MessageID: id, AnswerID: a, UserID: user})
	}
	if add && !p.Multiselect {
		for a, us := range ms.votes {
			if us[user] {
				change(a, false)
			}
		}
	}
	change(answer, add)
	return nil
}
