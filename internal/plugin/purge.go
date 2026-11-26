package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

const bulkMessageAge = 14 * 24 * time.Hour

type purgeFilter struct {
	ChannelID   string `json:"channelId"`
	Limit       int    `json:"limit"`
	Author      string `json:"author"`
	Contains    string `json:"contains"`
	Bots        *bool  `json:"bots"`
	Attachments *bool  `json:"attachments"`
	Before      string `json:"before"`
	After       string `json:"after"`
	Reason      string `json:"reason"`
}

type purgeResult struct {
	Deleted int `json:"deleted"`
	Skipped int `json:"skipped"`
}

func (i *Instance) requirePurge() error {
	if err := i.require(capMessagesRead); err != nil {
		return err
	}
	return i.require(capMessagesManage)
}

func (i *Instance) checkPurgeChannel(ctx context.Context, id string) error {
	dm, err := i.channelScope(ctx, id)
	if err != nil {
		return err
	}
	if dm {
		return argErr("purge is only available in this server's channels")
	}
	return nil
}

func (i *Instance) deleteBatch(ctx context.Context, channel string, ids []string, reason string) (int, error) {
	switch len(ids) {
	case 0:
		return 0, nil
	case 1:
		err := i.env.Discord.DeleteMessage(ctx, channel, ids[0], reason)
		if errors.Is(err, discord.ErrNotFound) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
	default:
		if err := i.env.Discord.BulkDeleteMessages(ctx, channel, ids, reason); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func opChannelBulkDelete(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.requirePurge(); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		ChannelID string   `json:"channelId"`
		IDs       []string `json:"ids"`
		Reason    string   `json:"reason"`
	}](args)
	if err != nil {
		return nil, err
	}
	if len(a.IDs) == 0 || len(a.IDs) > 100 {
		return nil, argErr("bulkDelete(): provide 1 to 100 message IDs")
	}
	seen := map[string]bool{}
	for _, id := range a.IDs {
		if err := checkSnowflake("message", id); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, argErr("bulkDelete(): duplicate message")
		}
		seen[id] = true
	}
	return func(ctx context.Context) (any, error) {
		if err := i.checkPurgeChannel(ctx, a.ChannelID); err != nil {
			return nil, err
		}
		cutoff := time.Now().Add(-bulkMessageAge)
		for _, id := range a.IDs {
			if !discord.SnowflakeTime(id).After(cutoff) {
				return nil, argErr("bulkDelete(): messages must be younger than 14 days")
			}
		}
		return i.deleteBatch(ctx, a.ChannelID, a.IDs, a.Reason)
	}, nil
}

func (f purgeFilter) matches(m discord.Message, cutoff time.Time) bool {
	return discord.SnowflakeTime(m.ID).After(cutoff) &&
		(f.Author == "" || m.Author.ID == f.Author) &&
		(f.Bots == nil || *f.Bots == m.Author.Bot) &&
		(f.Attachments == nil || *f.Attachments == (len(m.Attachments) > 0)) &&
		(f.Contains == "" || strings.Contains(m.Content, f.Contains))
}

func opChannelPurge(preview bool) asyncOp {
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if err := i.requirePurge(); err != nil {
			return nil, err
		}
		f, err := decode[purgeFilter](args)
		if err != nil {
			return nil, err
		}
		if f.Limit < 1 || f.Limit > 1000 {
			return nil, argErr("purge(): limit must be between 1 and 1000")
		}
		if (f.Contains != "" || f.Attachments != nil) && !i.env.MessageContent {
			return nil, &CapabilityError{Capability: "MessageContent"}
		}
		for _, id := range []string{f.Author, f.Before, f.After} {
			if id != "" {
				if err := checkSnowflake("filter", id); err != nil {
					return nil, err
				}
			}
		}
		return func(ctx context.Context) (any, error) {
			if err := i.checkPurgeChannel(ctx, f.ChannelID); err != nil {
				return nil, err
			}
			var result purgeResult
			var selected []string
			if preview {
				selected = []string{}
			}
			before := f.Before
			for remaining := f.Limit; remaining > 0; {
				pageSize := min(100, remaining)
				page, err := i.env.Discord.ListMessages(ctx, f.ChannelID, discord.ListMessagesParams{Limit: pageSize, Before: before})
				if err != nil {
					return nil, err
				}
				if len(page) == 0 {
					break
				}
				ids := make([]string, 0, len(page))
				cutoff := time.Now().Add(-bulkMessageAge)
				end := false
				for _, m := range page {
					if f.After != "" && !discord.SnowflakeLess(f.After, m.ID) {
						end = true
						break
					}
					if f.matches(m, cutoff) {
						ids = append(ids, m.ID)
					} else {
						result.Skipped++
					}
				}
				if preview {
					selected = append(selected, ids...)
				} else {
					n, err := i.deleteBatch(ctx, f.ChannelID, ids, f.Reason)
					if err != nil {
						return nil, err
					}
					result.Deleted += n
					result.Skipped += len(ids) - n
				}
				if end || len(page) < pageSize {
					break
				}
				before = page[len(page)-1].ID
				remaining -= len(page)
			}
			if preview {
				return map[string]any{"ids": selected, "skipped": result.Skipped}, nil
			}
			return result, nil
		}, nil
	}
}
