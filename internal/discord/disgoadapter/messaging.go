package disgoadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/cicord/cicord/internal/discord"
	"github.com/disgoorg/disgo/rest"
)

var _ discord.MessagingClient = (*session)(nil)

func (s *session) SetMessagePin(ctx context.Context, channel, message string, pin bool) error {
	ep := rest.UnpinMessage
	op := "UnpinMessage"
	if pin {
		ep = rest.PinMessage
		op = "PinMessage"
	}
	return s.do(ctx, op, ep.Compile(nil, channel, message), nil, nil)
}
func (s *session) ChannelPins(ctx context.Context, channel string) ([]discord.Message, error) {
	out := []discord.Message{}
	q := map[string]any{"limit": "50"}
	for {
		var page struct {
			Items []struct {
				Message  wireMessage
				PinnedAt string `json:"pinned_at"`
			}
			More bool `json:"has_more"`
		}
		if err := s.do(ctx, "ChannelPins", rest.GetChannelPins.Compile(q, channel), nil, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			out = append(out, messageFromWire(item.Message))
		}
		if !page.More {
			return out, nil
		}
		if len(page.Items) == 0 {
			return nil, errors.New("empty Discord pin page with has_more")
		}
		before := page.Items[len(page.Items)-1].PinnedAt
		if before == "" || q["before"] == before {
			return nil, errors.New("Discord pin cursor did not advance")
		}
		q["before"] = before
	}
}
func (s *session) Crosspost(ctx context.Context, channel, message string) error {
	return s.do(ctx, "CrosspostMessage", rest.CrosspostMessage.Compile(nil, channel, message), nil, nil)
}
func (s *session) Typing(ctx context.Context, channel string) error {
	return s.do(ctx, "Typing", rest.SendTyping.Compile(nil, channel), nil, nil)
}
func (s *session) ReactionUsers(ctx context.Context, channel, message, emoji string, limit int, after string) ([]discord.User, error) {
	e, err := reactionEmojiPath(emoji)
	if err != nil {
		return nil, err
	}
	q := map[string]any{"limit": fmt.Sprint(limit)}
	if after != "" {
		q["after"] = after
	}
	var users []wireUser
	if err = s.do(ctx, "ReactionUsers", rest.GetReactions.Compile(q, channel, message, e), nil, &users); err != nil {
		return nil, err
	}
	out := make([]discord.User, 0, len(users))
	for _, u := range users {
		out = append(out, userFromWire(u))
	}
	return out, nil
}
func (s *session) RemoveUserReaction(ctx context.Context, channel, message, emoji, user string) error {
	e, err := reactionEmojiPath(emoji)
	if err != nil {
		return err
	}
	return s.do(ctx, "RemoveUserReaction", rest.RemoveUserReaction.Compile(nil, channel, message, e, user), nil, nil)
}

type managedWebhook struct {
	ID, Token, Name string
	Type            int
	User            wireUser
}

func (s *session) channelWebhook(ctx context.Context, channel string) (managedWebhook, error) {
	// Serialize lookup and creation so concurrent sends share one managed webhook.
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	var hooks []managedWebhook
	if err := s.do(ctx, "ChannelWebhooks", rest.GetChannelWebhooks.Compile(nil, channel), nil, &hooks); err != nil {
		return managedWebhook{}, err
	}
	for _, h := range hooks {
		if h.Type == 1 && h.Name == "Cicord" && h.User.ID == s.Self().ID && h.Token != "" {
			return h, nil
		}
	}
	var hook managedWebhook
	err := s.do(ctx, "CreateWebhook", rest.CreateWebhook.Compile(nil, channel), map[string]string{"name": "Cicord"}, &hook)
	return hook, err
}
func (s *session) SendAs(ctx context.Context, channel string, identity discord.WebhookIdentity, msg discord.MessageCreate) (discord.Message, error) {
	h, err := s.channelWebhook(ctx, channel)
	if err != nil {
		return discord.Message{}, err
	}
	if h.Token == "" || h.ID == "" {
		return discord.Message{}, errors.New("Discord returned a webhook without a credential")
	}
	body := struct {
		wireMessageSend
		Username  string `json:"username"`
		AvatarURL string `json:"avatar_url,omitempty"`
	}{messageSendToWire(msg), identity.Username, identity.AvatarURL}
	var out wireMessage
	err = s.do(ctx, "SendAs", rest.CreateWebhookMessage.Compile(map[string]any{"wait": "true", "with_components": "true"}, h.ID, h.Token), body, &out)
	if err != nil {
		// A transport error may quote the request URL, which contains the webhook token.
		for _, e := range []error{context.Canceled, context.DeadlineExceeded, discord.ErrForbidden, discord.ErrNotFound, discord.ErrBadRequest} {
			if errors.Is(err, e) {
				return discord.Message{}, e
			}
		}
		return discord.Message{}, errors.New("Discord webhook send failed")
	}
	return messageFromWire(out), nil
}
