package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

const dmSelect = "cicord:dm:"
const dmTTL = 24 * time.Hour

// All choices and held messages belong to a bot session, outside plugin storage.
// Gateway callbacks enqueue short-lived work in arrival order without waiting on REST.
type directMessages struct {
	mu            sync.Mutex
	queue         []func()
	running       bool
	conversations map[string]*conversation
}

type conversation struct {
	owner, channel, token string
	expires               time.Time
	messages              []discord.Message
	offered               map[string]bool
	timer                 *time.Timer
}

func (h *botHost) queueDM(f func()) {
	d := &h.dm
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.queue) >= 1024 {
		return
	}
	d.queue = append(d.queue, f)
	if d.running {
		return
	}
	d.running = true
	go func() {
		for {
			d.mu.Lock()
			if len(d.queue) == 0 {
				d.running = false
				d.mu.Unlock()
				return
			}
			task := d.queue[0]
			d.queue[0] = nil
			d.queue = d.queue[1:]
			d.mu.Unlock()
			task()
		}
	}()
}

func (h *botHost) conversation(rt *routing, user, channel string, now time.Time) *conversation {
	d := &h.dm
	if d.conversations == nil {
		d.conversations = map[string]*conversation{}
	}
	c := d.conversations[user]
	if c != nil && c.owner == rt.dmOwner && c.channel == channel && c.expires.After(now) {
		return c
	}
	h.forgetDM(user)
	return nil
}

func (h *botHost) forgetDM(user string) {
	if c := h.dm.conversations[user]; c != nil && c.timer != nil {
		c.timer.Stop()
	}
	delete(h.dm.conversations, user)
}

func (h *botHost) remember(user string, c *conversation) {
	h.forgetDM(user)
	h.dm.conversations[user] = c
	c.timer = time.AfterFunc(dmTTL, func() {
		h.queueDM(func() {
			if h.dm.conversations[user] == c {
				h.forgetDM(user)
			}
		})
	})
}

// A remembered choice outlives a gateway reconnect. The bot's own namespace
// contains only the selected instance/server and expiry, keyed by a bot-specific HMAC.
type dmChoice struct {
	Owner, Server string
	Expires       time.Time
}

func (h *botHost) choiceStore() state.KV {
	return h.rt.Store().KV("dm-choice:" + h.session.ApplicationID())
}
func (h *botHost) choiceKey(user string) string {
	mac := hmac.New(sha256.New, []byte(h.tokenHash))
	mac.Write([]byte(user))
	return hex.EncodeToString(mac.Sum(nil))
}
func (h *botHost) choice(ctx context.Context, user, owner string, now time.Time) (string, error) {
	raw, err := h.choiceStore().Get(ctx, h.choiceKey(user))
	if errors.Is(err, state.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var c dmChoice
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", err
	}
	if c.Owner != owner || !now.Before(c.Expires) {
		return "", h.choiceStore().Delete(ctx, h.choiceKey(user))
	}
	return c.Server, nil
}
func (h *botHost) saveChoice(ctx context.Context, user, owner, server string, expires time.Time) error {
	raw, err := json.Marshal(dmChoice{owner, server, expires})
	if err != nil {
		return err
	}
	return h.choiceStore().Set(ctx, h.choiceKey(user), raw, dmTTL)
}

func dmText(rt *routing, en, fr string) string {
	if strings.HasPrefix(rt.dmLocale, "fr") {
		return fr
	}
	return en
}

func (h *botHost) routeDM(m discord.Message, now time.Time) {
	rt := h.routing.Load()
	if rt.dmOwner == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	send := func(content string) {
		_, _ = h.session.SendMessage(ctx, m.ChannelID, discord.MessageCreate{Content: content})
	}
	user := m.Author.ID
	c := h.conversation(rt, user, m.ChannelID, now)
	if c != nil {
		if len(c.messages) < 10 {
			c.messages = append(c.messages, m)
		} else {
			send(dmText(rt, "Choose a server before sending more messages.", "Choisissez un serveur avant d’envoyer d’autres messages."))
		}
		return
	}
	targets := rt.byName[rt.dmOwner]
	eligible := map[string]bool{}
	for _, sid := range sortedKeys(targets) {
		target := targets[sid]
		if !target.plugin.def.HasEvent("directMessage") {
			continue
		}
		mem, err := h.session.GetMember(ctx, sid, user)
		if errors.Is(err, discord.ErrNotFound) {
			continue
		}
		if err != nil {
			send(dmText(rt, "Membership could not be verified. Please try again.", "Impossible de vérifier votre adhésion. Réessayez."))
			return
		}
		if mem.User.ID == user {
			eligible[sid] = true
		}
	}
	choice, err := h.choice(ctx, user, rt.dmOwner, now)
	if err != nil {
		send(dmText(rt, "Unable to load your server choice. Please retry.", "Votre choix de serveur est indisponible. Réessayez."))
		return
	}
	if choice != "" && !eligible[choice] {
		_ = h.choiceStore().Delete(ctx, h.choiceKey(user))
		send(dmText(rt, "Your previous server is unavailable. Send a new message to start again.", "Votre serveur précédent est indisponible. Envoyez un nouveau message."))
		return
	}
	if len(eligible) == 0 {
		send(dmText(rt, "No server accepts your direct messages.", "Aucun serveur ne prend vos messages privés."))
		return
	}
	if choice != "" || len(eligible) == 1 {
		sid := ""
		if choice != "" {
			sid = choice
		} else {
			for id := range eligible {
				sid = id
			}
		}
		h.deliverDM(ctx, rt, sid, m)
		return
	}
	c = &conversation{owner: rt.dmOwner, channel: m.ChannelID, token: m.ID, offered: eligible, messages: []discord.Message{m}, expires: now.Add(dmTTL)}
	h.remember(user, c)
	ids := sortedKeys(eligible)
	for len(ids) > 0 {
		n := min(len(ids), 25)
		menu := discord.Component{Type: discord.ComponentStringSelect, CustomID: dmSelect + c.token}
		for _, sid := range ids[:n] {
			menu.Options = append(menu.Options, discord.SelectOption{Value: sid, Label: rt.serverKeys[sid]})
		}
		_, err := h.session.SendMessage(ctx, m.ChannelID, discord.MessageCreate{Content: dmText(rt, "Which server is this message for?", "À quel serveur destinez-vous ce message ?"), Components: []discord.Component{{Type: discord.ComponentActionRow, Components: []discord.Component{menu}}}})
		if err != nil {
			h.forgetDM(user)
			return
		}
		ids = ids[n:]
	}
}

func (h *botHost) selectDM(it discord.Interaction, now time.Time) {
	rt := h.routing.Load()
	user := it.User.ID
	c := h.conversation(rt, user, it.ChannelID, now)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	answer := func(msg string) {
		_ = h.session.RespondInteraction(ctx, it.ID, it.Token, discord.InteractionResponse{Type: discord.ResponseUpdateMessage, Data: &discord.MessageCreate{Content: msg, Components: []discord.Component{}}})
	}
	if it.Type != discord.InteractionComponent || len(it.Values) != 1 || c == nil || c.token != strings.TrimPrefix(it.CustomID, dmSelect) || !c.offered[it.Values[0]] {
		answer(dmText(rt, "This choice has expired.", "Ce choix a expiré."))
		return
	}
	sid := it.Values[0]
	mem, err := h.session.GetMember(ctx, sid, user)
	if err != nil || mem.User.ID != user || rt.byName[rt.dmOwner][sid] == nil {
		h.forgetDM(user)
		answer(dmText(rt, "This server is unavailable.", "Ce serveur est indisponible."))
		return
	}
	if err := h.saveChoice(ctx, user, c.owner, sid, now.Add(dmTTL)); err != nil {
		_ = h.session.RespondInteraction(ctx, it.ID, it.Token, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: dmText(rt, "Unable to save this choice. Please retry.", "Impossible d’enregistrer ce choix. Réessayez.")}})
		return
	}
	answer(dmText(rt, "Selected server: ", "Serveur choisi : ") + rt.serverKeys[sid])
	h.forgetDM(user)
	for _, m := range c.messages {
		h.deliverDM(ctx, rt, sid, m)
	}
}

func (h *botHost) deliverDM(ctx context.Context, rt *routing, sid string, m discord.Message) {
	if rt != h.routing.Load() {
		return
	}
	target := rt.byName[rt.dmOwner][sid]
	if target == nil || !target.plugin.def.HasEvent("directMessage") {
		return
	}
	mem, err := h.session.GetMember(ctx, sid, m.Author.ID)
	if err != nil || mem.User.ID != m.Author.ID {
		return
	}
	m.Member = &mem
	if inst := target.inst(); inst != nil {
		done := inst.DispatchEvent(context.Background(), discord.MessageCreateEvent{Message: m})
		go func() { h.rt.handlerDone(target, <-done) }()
	}
}

func (h *botHost) routeDMComponent(it discord.Interaction) {
	rt := h.routing.Load()
	parts := strings.SplitN(it.CustomID, "/", 3)
	if len(parts) != 3 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	name, _, ok := plugin.ParseCustomID(parts[2])
	if !ok || rt.byName[name][parts[1]] == nil {
		return
	}
	member, err := h.session.GetMember(ctx, parts[1], it.User.ID)
	if err != nil || member.User.ID != it.User.ID {
		return
	}
	it.CustomID = parts[2]
	it.ServerID = parts[1]
	it.Member = &member
	h.routeInteraction(rt, it)
}

func (h *botHost) stopDM() {
	h.routing.Store(&routing{})
	h.queueDM(func() {
		for user := range h.dm.conversations {
			h.forgetDM(user)
		}
	})
}
