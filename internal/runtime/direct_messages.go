package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

const dmSelect = "cicord:dm:"
const dmTTL = 24 * time.Hour

// DM routing limits (m5). Work is queued per user shard, so a slow Discord answer for one
// user never holds the DMs, selections and components of the bot's other users.
const (
	dmShards = 16
	// dmShardQueue bounds the queued work of a shard (dmShards × dmShardQueue in all).
	dmShardQueue = 64
	// A user's DMs and DM interactions spend a token each: dmBurst at once, then one per
	// dmRefill. Beyond, the user is told to slow down before any member lookup runs.
	dmBurst  = 10
	dmRefill = time.Second
	// dmLookupTimeout bounds one membership check; dmLookups run in parallel per DM.
	dmLookupTimeout = 5 * time.Second
	dmLookups       = 4
	// Busy replies: at most dmNotices sends in flight, one per user per dmNoticeEvery.
	dmNotices     = 4
	dmNoticeEvery = 30 * time.Second
)

// All choices and held messages belong to a bot session, outside plugin storage.
// Gateway callbacks enqueue short-lived work in arrival order (per user) without waiting
// on REST. Each shard's conversations are only touched by that shard's worker.
type directMessages struct {
	shards [dmShards]dmShard

	limitMu sync.Mutex
	limits  map[string]*dmBucket
	notices atomic.Int32
}

type dmShard struct {
	mu            sync.Mutex
	queue         []func()
	running       bool
	conversations map[string]*conversation
}

type dmBucket struct {
	tokens   float64
	at       time.Time
	noticeAt time.Time
}

type conversation struct {
	owner, channel, token string
	expires               time.Time
	messages              []discord.Message
	offered               map[string]bool
	timer                 *time.Timer
}

func (d *directMessages) shard(user string) *dmShard {
	f := fnv.New32a()
	_, _ = f.Write([]byte(user))
	return &d.shards[f.Sum32()%dmShards]
}

// queueDM runs f on the user's shard, after the work already queued there. It reports
// false, without queuing, when the shard is full.
func (h *botHost) queueDM(user string, f func()) bool {
	return h.dm.shard(user).enqueue(f, false)
}

func (s *dmShard) enqueue(f func(), force bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) >= dmShardQueue && !force {
		return false
	}
	s.queue = append(s.queue, f)
	if s.running {
		return true
	}
	s.running = true
	go func() {
		for {
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.running = false
				s.mu.Unlock()
				return
			}
			task := s.queue[0]
			s.queue[0] = nil
			s.queue = s.queue[1:]
			s.mu.Unlock()
			task()
		}
	}()
	return true
}

// allowDM spends one of the user's tokens. notice is true when the user should be told
// (at most once per dmNoticeEvery) that the bot is busy.
func (d *directMessages) allowDM(user string, now time.Time) (ok, notice bool) {
	d.limitMu.Lock()
	defer d.limitMu.Unlock()
	if d.limits == nil {
		d.limits = map[string]*dmBucket{}
	}
	b := d.limits[user]
	if b == nil {
		if len(d.limits) > 10000 {
			for k, v := range d.limits {
				if v.tokens+now.Sub(v.at).Seconds()/dmRefill.Seconds() >= dmBurst && now.Sub(v.noticeAt) > dmNoticeEvery {
					delete(d.limits, k)
				}
			}
			if len(d.limits) > 10000 {
				d.limits = map[string]*dmBucket{}
			}
		}
		b = &dmBucket{tokens: dmBurst, at: now}
		d.limits[user] = b
	}
	b.tokens = min(dmBurst, b.tokens+now.Sub(b.at).Seconds()/dmRefill.Seconds())
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, false
	}
	return false, d.noticeDueLocked(b, now)
}

// noticeDue reports whether a busy notice may be sent to the user now.
func (d *directMessages) noticeDue(user string, now time.Time) bool {
	d.limitMu.Lock()
	defer d.limitMu.Unlock()
	if d.limits == nil {
		d.limits = map[string]*dmBucket{}
	}
	b := d.limits[user]
	if b == nil {
		b = &dmBucket{tokens: dmBurst, at: now}
		d.limits[user] = b
	}
	return d.noticeDueLocked(b, now)
}

func (d *directMessages) noticeDueLocked(b *dmBucket, now time.Time) bool {
	if now.Sub(b.noticeAt) < dmNoticeEvery {
		return false
	}
	b.noticeAt = now
	return true
}

// dmBusyText is the degraded-mode reply (P10) of a DM that is not routed now.
func dmBusyText(rt *routing) string {
	return dmText(rt, "Too many messages right now. Please try again in a moment.", "Trop de messages en ce moment. Réessayez dans un instant.")
}

// notifyBusy runs send off the gateway goroutine, with at most dmNotices in flight; a
// notice that finds no slot is only logged.
func (h *botHost) notifyBusy(why string, send func(context.Context) error) {
	if h.dm.notices.Add(1) > dmNotices {
		h.dm.notices.Add(-1)
		h.rt.log.Warn("direct message not routed", "bot", h.key, "reason", why, "notice", "skipped")
		return
	}
	h.rt.log.Warn("direct message not routed", "bot", h.key, "reason", why)
	go func() {
		defer h.dm.notices.Add(-1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := send(ctx); err != nil {
			h.rt.log.Warn("busy reply to a direct message failed", "bot", h.key, "err", err)
		}
	}()
}

// acceptDMMessage rate limits and queues the routing of a DM, or tells its author to retry.
func (h *botHost) acceptDMMessage(m discord.Message, now time.Time) {
	user := m.Author.ID
	ok, notice := h.dm.allowDM(user, now)
	why := "rate limited"
	if ok {
		if h.queueDM(user, func() { h.routeDM(m, time.Now()) }) {
			return
		}
		why, notice = "queue full", h.dm.noticeDue(user, now)
	}
	if !notice {
		h.rt.log.Debug("direct message not routed", "bot", h.key, "reason", why)
		return
	}
	text := dmBusyText(h.routing.Load())
	h.notifyBusy(why, func(ctx context.Context) error {
		_, err := h.session.SendMessage(ctx, m.ChannelID, discord.MessageCreate{Content: text})
		return err
	})
}

// acceptDMInteraction is acceptDMMessage for a DM component or selection: a refused one is
// answered (ephemeral) at once, within Discord's 3 s.
func (h *botHost) acceptDMInteraction(it discord.Interaction, now time.Time, task func()) {
	ok, _ := h.dm.allowDM(it.User.ID, now)
	why := "rate limited"
	if ok {
		if h.queueDM(it.User.ID, task) {
			return
		}
		why = "queue full"
	}
	text := dmBusyText(h.routing.Load())
	h.notifyBusy(why, func(ctx context.Context) error {
		return h.session.RespondInteraction(ctx, it.ID, it.Token, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: text, Flags: discord.MessageFlagEphemeral}})
	})
}

// dmMember checks that user is a member of server sid: from the server's member listing
// when it is fresh (member events keep it current), else from Discord, bounded by
// dmLookupTimeout. found is false for a non-member.
func (h *botHost) dmMember(ctx context.Context, rt *routing, sid, user string) (m discord.Member, found bool, err error) {
	if c := rt.caches[sid]; c != nil {
		if m, member, fresh := c.members.Lookup(user); fresh {
			return m, member, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, dmLookupTimeout)
	defer cancel()
	m, err = h.session.GetMember(ctx, sid, user)
	if errors.Is(err, discord.ErrNotFound) {
		return discord.Member{}, false, nil
	}
	if err != nil {
		return discord.Member{}, false, err
	}
	return m, m.User.ID == user, nil
}

// eligibleServers returns the servers of the DM owner instance where user is a member,
// checking at most dmLookups servers at a time.
func (h *botHost) eligibleServers(ctx context.Context, rt *routing, user string) (map[string]discord.Member, error) {
	targets := rt.byName[rt.dmOwner]
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		sem      = make(chan struct{}, dmLookups)
		out      = map[string]discord.Member{}
	)
	for _, sid := range sortedKeys(targets) {
		if !targets[sid].plugin.def.HasEvent("directMessage") {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			m, found, err := h.dmMember(ctx, rt, sid, user)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				if firstErr == nil {
					firstErr = err
				}
			case found:
				out[sid] = m
			}
		}()
	}
	wg.Wait()
	return out, firstErr
}

// conversation, forgetDM and remember run on the user's shard worker.
func (h *botHost) conversation(rt *routing, user, channel string, now time.Time) *conversation {
	s := h.dm.shard(user)
	if s.conversations == nil {
		s.conversations = map[string]*conversation{}
	}
	c := s.conversations[user]
	if c != nil && c.owner == rt.dmOwner && c.channel == channel && c.expires.After(now) {
		return c
	}
	h.forgetDM(user)
	return nil
}

func (h *botHost) forgetDM(user string) {
	s := h.dm.shard(user)
	if c := s.conversations[user]; c != nil && c.timer != nil {
		c.timer.Stop()
	}
	delete(s.conversations, user)
}

func (h *botHost) remember(user string, c *conversation) {
	h.forgetDM(user)
	s := h.dm.shard(user)
	if s.conversations == nil {
		s.conversations = map[string]*conversation{}
	}
	s.conversations[user] = c
	c.timer = time.AfterFunc(dmTTL, func() {
		s.enqueue(func() {
			if s.conversations[user] == c {
				h.forgetDM(user)
			}
		}, true)
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
	members, err := h.eligibleServers(ctx, rt, user)
	if err != nil {
		send(dmText(rt, "Membership could not be verified. Please try again.", "Impossible de vérifier votre adhésion. Réessayez."))
		return
	}
	eligible := map[string]bool{}
	for sid := range members {
		eligible[sid] = true
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
		h.deliverDM(rt, sid, m, members[sid])
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
	mem, found, err := h.dmMember(ctx, rt, sid, user)
	if err != nil || !found || rt.byName[rt.dmOwner][sid] == nil {
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
		h.deliverDM(rt, sid, m, mem)
	}
}

// deliverDM hands a DM to the owner instance of server sid, with the member just verified
// by the caller (dmMember).
func (h *botHost) deliverDM(rt *routing, sid string, m discord.Message, mem discord.Member) {
	if rt != h.routing.Load() {
		return
	}
	target := rt.byName[rt.dmOwner][sid]
	if target == nil || !target.plugin.def.HasEvent("directMessage") || mem.User.ID != m.Author.ID {
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
	name, local, ok := plugin.ParseCustomID(parts[2])
	if !ok || rt.byName[name][parts[1]] == nil {
		return
	}
	member, found, err := h.dmMember(ctx, rt, parts[1], it.User.ID)
	if err != nil || !found {
		return
	}
	it.CustomID = parts[2]
	it.Member = &member
	kind := "component"
	if it.Type == discord.InteractionModalSubmit {
		kind = "modal"
	} else if it.Type != discord.InteractionComponent {
		return
	}
	h.dispatchInteraction(rt.byName[name][parts[1]], it, kind, local)
}

func (h *botHost) stopDM() {
	h.routing.Store(&routing{})
	for n := range h.dm.shards {
		s := &h.dm.shards[n]
		s.enqueue(func() {
			for user := range s.conversations {
				h.forgetDM(user)
			}
		}, true)
	}
}
