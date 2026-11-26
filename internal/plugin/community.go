package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

func communityOp(op string) asyncOp {
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		cap := capEventsRead
		if op != "list" && op != "users" {
			cap = capEventsManage
		}
		if err := i.require(cap); err != nil {
			return nil, err
		}
		c, ok := i.env.Discord.(discord.ScheduledEvents)
		if !ok {
			return nil, errors.New("scheduled events unavailable")
		}
		a, err := decode[struct {
			ID, After string
			Event     discord.ScheduledEvent
		}](args)
		if err != nil {
			return nil, err
		}
		if op == "edit" {
			a.ID = a.Event.ID
		}
		if op == "delete" || op == "edit" || op == "users" {
			if err := checkSnowflake("event", a.ID); err != nil {
				return nil, err
			}
		}
		if op == "create" || op == "edit" {
			e := a.Event
			if e.Name == "" || e.Start.IsZero() || (e.Channel == "") == (e.Location == "") || (e.Location != "" && e.End == nil) || (e.End != nil && !e.End.After(e.Start)) {
				return nil, argErr("event needs name, start and either a channel or a location and end")
			}
		}
		if a.After != "" && !discord.IsSnowflake(a.After) {
			return nil, argErr("invalid user cursor")
		}
		return func(ctx context.Context) (any, error) {
			sid := i.env.ServerID
			if a.Event.Channel != "" {
				dm, err := i.channelScope(ctx, a.Event.Channel)
				if err != nil {
					return nil, err
				}
				if dm {
					return nil, argErr("expected a server channel")
				}
			}
			switch op {
			case "list":
				return c.ScheduledEvents(ctx, sid)
			case "users":
				return c.EventUsers(ctx, sid, a.ID, a.After)
			case "create":
				if i.env.Resources == nil {
					return nil, errors.New("event ownership storage unavailable")
				}
				e, err := c.CreateEvent(ctx, sid, a.Event)
				if err != nil {
					return nil, err
				}
				err = i.env.Resources.Put(ctx, state.Resource{Server: i.env.ServerKey, Kind: "scheduled-event", Key: i.env.BotKey + "/" + i.env.InstanceName + "/" + e.ID, ID: e.ID, Owner: i.owner(), UpdatedAt: time.Now().UTC()})
				if err != nil {
					_ = c.DeleteEvent(ctx, sid, e.ID)
				}
				return e, err
			default:
				if i.env.Resources == nil {
					return nil, errors.New("event ownership storage unavailable")
				}
				r, err := i.env.Resources.GetByID(ctx, i.env.ServerKey, a.ID)
				if err != nil {
					return nil, err
				}
				if r.Kind != "scheduled-event" || r.Owner != i.owner() {
					return nil, errors.New("event belongs to another instance")
				}
				if op == "edit" {
					return c.EditEvent(ctx, sid, a.Event)
				}
				if err = c.DeleteEvent(ctx, sid, a.ID); err != nil {
					return nil, err
				}
				return nil, i.env.Resources.Delete(ctx, r.Server, r.Kind, r.Key)
			}
		}, nil
	}
}
func pollOp(read bool) asyncOp {
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		cap := capMessagesSend
		if read {
			cap = capMessagesRead
		}
		if err := i.require(cap); err != nil {
			return nil, err
		}
		a, err := decode[struct {
			ChannelID, MessageID, After string
			AnswerID                    int
		}](args)
		if err != nil {
			return nil, err
		}
		if !discord.IsSnowflake(a.MessageID) || (read && (a.AnswerID < 1 || a.AnswerID > 10 || a.After != "" && !discord.IsSnowflake(a.After))) {
			return nil, argErr("invalid poll or answer")
		}
		c, ok := i.env.Discord.(discord.PollClient)
		if !ok {
			return nil, errors.New("polls unavailable")
		}
		return func(ctx context.Context) (any, error) {
			dm, err := i.channelScope(ctx, a.ChannelID)
			if err != nil {
				return nil, err
			}
			if dm {
				return nil, argErr("expected a server poll")
			}
			if read {
				return c.PollVoters(ctx, a.ChannelID, a.MessageID, a.AnswerID, a.After)
			}
			m, err := c.EndPoll(ctx, a.ChannelID, a.MessageID)
			if err != nil {
				return nil, err
			}
			return i.messageDTO(m), nil
		}, nil
	}
}
func opAnalytics(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require("analytics.read"); err != nil {
		return nil, err
	}
	if i.env.AnalyticsSummary == nil {
		return nil, errors.New("server analytics unavailable")
	}
	a, err := decode[struct {
		From, To    time.Time
		Granularity string
	}](args)
	if err != nil {
		return nil, err
	}
	if a.From.IsZero() || !a.From.Before(a.To) || a.To.Sub(a.From) > 400*24*time.Hour || (a.Granularity != "day" && a.Granularity != "week") {
		return nil, argErr("expected a range of up to 400 days and day or week granularity")
	}
	return func(ctx context.Context) (any, error) {
		return i.env.AnalyticsSummary(ctx, a.From, a.To, a.Granularity)
	}, nil
}
