package sim

import (
	"context"
	"github.com/cicord/cicord/internal/discord"
	"slices"
	"sort"
	"time"
)

var _ discord.MessagingClient = (*Client)(nil)

func (c *Client) SetMessagePin(ctx context.Context, ch, id string, pin bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	channel, m, err := w.channelMessage(ch, id)
	if err != nil {
		return err
	}
	a := c.actorFor(ctx)
	if err = w.requireChannel(a, channel, discord.MustPermissions("PinMessages")); err != nil {
		return err
	}
	op := "UnpinMessage"
	if pin {
		op = "PinMessage"
		if m.pinned.IsZero() {
			m.pinned = w.now()
		}
	} else {
		m.pinned = time.Time{}
	}
	return w.record(a, op, channel.ch.ServerID, id, "", nil)
}
func (c *Client) ChannelPins(ctx context.Context, ch string) ([]discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	channel, err := w.channel(ch)
	if err != nil {
		return nil, err
	}
	a := c.actorFor(ctx)
	if err = w.readAccess(a, channel); err != nil {
		return nil, err
	}
	var pins []*messageState
	for _, id := range channel.msgIDs {
		if m := w.messages[id]; m != nil && !m.pinned.IsZero() {
			pins = append(pins, m)
		}
	}
	slices.SortStableFunc(pins, func(a, b *messageState) int { return b.pinned.Compare(a.pinned) })
	out := make([]discord.Message, 0, len(pins))
	for _, m := range pins {
		out = append(out, w.messageFor(a, m))
	}
	return out, nil
}
func (w *World) messageFor(a actor, ms *messageState) discord.Message {
	m := restMessage(ms.msg)
	if p := m.PollResults; p != nil {
		p.Finalized = p.Finalized || !w.now().Before(ms.expires)
		for n, answer := range p.Answers {
			p.Answers[n].MeVoted = ms.votes[answer.ID][a.userID]
		}
	}
	m.Reactions = make([]discord.ReactionSummary, 0, len(ms.reactions))
	for _, r := range ms.reactions {
		m.Reactions = append(m.Reactions, discord.ReactionSummary{Emoji: r.emoji, Count: len(r.users), Me: slices.Contains(r.users, a.userID)})
	}
	if bot := w.botsByUser[a.userID]; bot != nil {
		return w.redact(m, a.userID, bot.privileged.Has(intentMessageContent))
	}
	return m
}
func (c *Client) Crosspost(ctx context.Context, ch, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	channel, m, err := w.channelMessage(ch, id)
	if err != nil {
		return err
	}
	if channel.ch.Type != discord.ChannelAnnouncement {
		return badRequest("crosspost requires an announcement channel")
	}
	a := c.actorFor(ctx)
	need := permSend
	if a.userID != m.msg.Author.ID {
		need |= permManageMessages
	}
	if err = w.requireChannel(a, channel, need); err != nil {
		return err
	}
	if m.msg.Flags&1 != 0 {
		return badRequest("message already crossposted")
	}
	m.msg.Flags |= 1
	w.emit(discord.MessageUpdateEvent{Message: cloneMessage(m.msg)})
	return w.record(a, "CrosspostMessage", channel.ch.ServerID, id, "", nil)
}
func (c *Client) Typing(ctx context.Context, ch string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	channel, err := w.channel(ch)
	if err != nil {
		return err
	}
	a := c.actorFor(ctx)
	if err = w.checkSend(a, channel); err != nil {
		return err
	}
	return w.record(a, "Typing", channel.ch.ServerID, ch, "", nil)
}
func (c *Client) ReactionUsers(ctx context.Context, ch, id, emoji string, limit int, after string) ([]discord.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	channel, m, err := w.channelMessage(ch, id)
	if err != nil {
		return nil, err
	}
	if err = w.readAccess(c.actorFor(ctx), channel); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || !validEmoji(emoji) {
		return nil, badRequest("invalid reaction listing")
	}
	out := []discord.User{}
	for _, r := range m.reactions {
		if r.emoji == emoji {
			for _, uid := range r.users {
				if after == "" || snowLess(after, uid) {
					out = append(out, w.users[uid].user)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return snowLess(out[i].ID, out[j].ID) })
	return out[:min(limit, len(out))], nil
}
func (c *Client) RemoveUserReaction(ctx context.Context, ch, id, emoji, uid string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.reactUser(c.actorFor(ctx), false, ch, id, emoji, true, uid)
}
func (c *Client) SendAs(ctx context.Context, ch string, identity discord.WebhookIdentity, msg discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	channel, err := w.channel(ch)
	if err != nil {
		return discord.Message{}, err
	}
	a := c.actorFor(ctx)
	if channel.ch.ServerID == "" {
		return discord.Message{}, badRequest("guild channel required")
	}
	if err = w.requireChannel(a, channel, discord.MustPermissions("ManageWebhooks")); err != nil {
		return discord.Message{}, err
	}
	if err = w.validateMessage(msg, channel.ch.ServerID); err != nil {
		return discord.Message{}, err
	}
	if channel.webhooks == nil {
		channel.webhooks = map[string]string{}
	}
	hook := channel.webhooks[a.userID]
	if hook == "" {
		hook = w.newID()
		channel.webhooks[a.userID] = hook
		w.recordOK(a, "CreateWebhook", channel.ch.ServerID, hook, "")
	}
	m := w.newMessage(channel, discord.User{ID: hook, Username: identity.Username, AvatarURL: identity.AvatarURL, Bot: true}, msg, "")
	m.msg.WebhookID = hook
	w.recordOK(a, "SendAs", channel.ch.ServerID, m.msg.ID, "")
	return restMessage(m.msg), nil
}
