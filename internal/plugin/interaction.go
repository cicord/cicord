package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/npjonath/cicord/internal/discord"
)

// GenericErrorMessage is the ephemeral reply sent when a handler fails before answering.
const GenericErrorMessage = "Something went wrong while handling this interaction."

// interactionTTL is how long Discord accepts follow-ups on an interaction token.
const interactionTTL = 15 * time.Minute

// interaction tracks the response state of one Discord interaction. All responses go
// through mu, and the response type is decided when the call runs (not when JS made it), so
// an auto-defer racing a reply() never produces two initial responses.
//
// Response table (kind → call → Discord API):
//
//	state              reply()                 defer()            update() (component)    followUp()   showModal()
//	unanswered         Respond(4)              Respond(5) cmd/modal Respond(7)            error        Respond(9)
//	                                           Respond(6) component
//	deferred (5)       EditOriginalResponse    no-op              error                   CreateFollowup error
//	deferred (6)       CreateFollowup          no-op              EditOriginalResponse    CreateFollowup error
//	updated (7)        CreateFollowup          error              EditOriginalResponse    CreateFollowup error
//	replied            error (use followUp)    error              error                   CreateFollowup error
//
// Ephemerality is fixed by the first response: after a deferral the reply keeps the
// deferral's flags.
type interaction struct {
	i      *Instance
	handle string
	it     discord.Interaction
	kind   string // command | component | modal
	// ephemeralDefault: command declared `ephemeral: true`.
	ephemeralDefault bool
	created          time.Time

	mu           sync.Mutex
	acked        bool
	deferredMsg  bool // acked with DeferredMessage (5)
	deferredUpd  bool // acked with DeferredUpdate (6)
	updated      bool // acked with UpdateMessage (7) or edited after 6
	replyDone    bool
	modalShown   bool
	autoDefer    *time.Timer
	finished     bool
	repliedFlag  atomic.Bool
	deferredFlag atomic.Bool
}

func (i *Instance) newInteraction(it discord.Interaction, kind string, ephemeral bool) *interaction {
	x := &interaction{i: i, handle: it.ID + ":" + newID()[:8], it: it, kind: kind, ephemeralDefault: ephemeral, created: time.Now()}
	i.imu.Lock()
	for h, old := range i.interactions {
		if time.Since(old.created) > interactionTTL {
			delete(i.interactions, h)
		}
	}
	i.interactions[x.handle] = x
	i.imu.Unlock()
	x.mu.Lock()
	x.autoDefer = time.AfterFunc(i.limits.AutoDeferAfter, x.runAutoDefer)
	x.mu.Unlock()
	return x
}

func (i *Instance) lookupInteraction(h string) (*interaction, error) {
	i.imu.Lock()
	defer i.imu.Unlock()
	x, ok := i.interactions[h]
	if !ok {
		return nil, errors.New("this interaction has expired")
	}
	return x, nil
}

func (x *interaction) client() discord.Client { return x.i.env.Discord }
func (x *interaction) appID() string {
	if x.it.ApplicationID != "" {
		return x.it.ApplicationID
	}
	return x.i.env.ApplicationID
}

func (x *interaction) respond(ctx context.Context, r discord.InteractionResponse) error {
	return x.client().RespondInteraction(ctx, x.it.ID, x.it.Token, r)
}

func (x *interaction) syncFlags() {
	x.repliedFlag.Store(x.replyDone || x.updated || x.modalShown)
	x.deferredFlag.Store(x.deferredMsg || x.deferredUpd)
}

func (x *interaction) stopAutoDefer() {
	x.mu.Lock()
	if x.autoDefer != nil {
		x.autoDefer.Stop()
	}
	x.mu.Unlock()
}

func (x *interaction) runAutoDefer() {
	ctx, cancel := context.WithTimeout(x.i.ctx, 5*time.Second)
	defer cancel()
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.acked || x.finished {
		return
	}
	if err := x.deferLocked(ctx, x.ephemeralDefault); err != nil {
		x.i.logger.Warn("automatic interaction deferral failed", "interaction", x.it.ID, "error", err)
		return
	}
	x.i.logger.Debug("interaction deferred automatically", "interaction", x.it.ID, "kind", x.kind)
}

func ephemeralFlags(e bool) discord.MessageFlags {
	if e {
		return discord.MessageFlagEphemeral
	}
	return 0
}

func (x *interaction) deferLocked(ctx context.Context, ephemeral bool) error {
	if x.kind == "component" {
		if err := x.respond(ctx, discord.InteractionResponse{Type: discord.ResponseDeferredUpdate}); err != nil {
			return err
		}
		x.acked, x.deferredUpd = true, true
	} else {
		data := &discord.MessageCreate{Flags: ephemeralFlags(ephemeral)}
		if err := x.respond(ctx, discord.InteractionResponse{Type: discord.ResponseDeferredMessage, Data: data}); err != nil {
			return err
		}
		x.acked, x.deferredMsg = true, true
	}
	x.syncFlags()
	return nil
}

func (x *interaction) reply(ctx context.Context, mc discord.MessageCreate) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	switch {
	case x.modalShown:
		return errors.New("reply(): a modal was shown for this interaction")
	case x.replyDone:
		return errors.New("reply(): this interaction was already replied to (use followUp() to send another message)")
	case !x.acked:
		if err := x.respond(ctx, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &mc}); err != nil {
			return err
		}
		x.acked = true
	case x.deferredUpd || x.updated:
		if _, err := x.client().CreateFollowup(ctx, x.appID(), x.it.Token, mc); err != nil {
			return err
		}
	default: // deferred message: fill it in; flags were fixed by the deferral
		mc.Flags &^= discord.MessageFlagEphemeral
		if _, err := x.client().EditOriginalResponse(ctx, x.appID(), x.it.Token, mc); err != nil {
			return err
		}
	}
	x.replyDone = true
	x.syncFlags()
	return nil
}

func (x *interaction) deferResponse(ctx context.Context, ephemeral *bool) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.acked {
		if x.deferredMsg || x.deferredUpd {
			return nil
		}
		return errors.New("defer(): this interaction was already answered")
	}
	e := x.ephemeralDefault
	if ephemeral != nil {
		e = *ephemeral
	}
	return x.deferLocked(ctx, e)
}

func (x *interaction) update(ctx context.Context, mc discord.MessageCreate) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	switch {
	case x.kind != "component":
		return errors.New("update() is only available on component interactions")
	case !x.acked:
		if err := x.respond(ctx, discord.InteractionResponse{Type: discord.ResponseUpdateMessage, Data: &mc}); err != nil {
			return err
		}
		x.acked = true
	case x.deferredUpd || x.updated:
		if _, err := x.client().EditOriginalResponse(ctx, x.appID(), x.it.Token, mc); err != nil {
			return err
		}
	default:
		return errors.New("update(): not possible after reply()")
	}
	x.updated = true
	x.syncFlags()
	return nil
}

func (x *interaction) followUp(ctx context.Context, mc discord.MessageCreate) (discord.Message, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.acked {
		return discord.Message{}, errors.New("followUp(): reply() or defer() first")
	}
	return x.client().CreateFollowup(ctx, x.appID(), x.it.Token, mc)
}

func (x *interaction) showModal(ctx context.Context, m discord.Modal) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.kind == "modal" {
		return errors.New("showModal(): cannot show a modal in response to a modal submit")
	}
	if x.acked {
		return errors.New("showModal(): must be the first response (before reply() or defer())")
	}
	if err := x.respond(ctx, discord.InteractionResponse{Type: discord.ResponseModal, Modal: &m}); err != nil {
		return err
	}
	x.acked, x.modalShown = true, true
	x.syncFlags()
	return nil
}

// finish runs when the handler settled (err == nil), failed or timed out.
func (x *interaction) finish(err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	x.mu.Lock()
	defer x.mu.Unlock()
	x.finished = true
	if x.autoDefer != nil {
		x.autoDefer.Stop()
	}
	log := x.i.logger
	if err != nil {
		generic := discord.MessageCreate{Content: GenericErrorMessage, Flags: discord.MessageFlagEphemeral}
		var rerr error
		switch {
		case !x.acked:
			rerr = x.respond(ctx, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &generic})
			x.acked, x.replyDone = rerr == nil, rerr == nil
		case x.replyDone || x.modalShown || x.updated:
			// answered already: the error is only logged
		case x.deferredUpd:
			_, rerr = x.client().CreateFollowup(ctx, x.appID(), x.it.Token, generic)
		default:
			generic.Flags = 0
			_, rerr = x.client().EditOriginalResponse(ctx, x.appID(), x.it.Token, generic)
			x.replyDone = rerr == nil
		}
		if rerr != nil {
			log.Warn("could not send the error reply", "interaction", x.it.ID, "error", rerr)
		}
		x.syncFlags()
		return
	}
	if !x.acked {
		if x.kind == "component" {
			// Acknowledge silently so Discord does not show "interaction failed".
			if rerr := x.respond(ctx, discord.InteractionResponse{Type: discord.ResponseDeferredUpdate}); rerr == nil {
				x.acked, x.deferredUpd = true, true
				x.syncFlags()
			}
			return
		}
		log.Warn("interaction handler finished without replying", "kind", x.kind, "interaction", x.it.ID)
	} else if x.deferredMsg && !x.replyDone {
		log.Warn("interaction handler deferred but never replied", "kind", x.kind, "interaction", x.it.ID)
	}
}

// ───────────────────────────────────────────────────────────── ops

type interactionArgs struct {
	H         string          `json:"h"`
	Message   messageInput    `json:"message"`
	Ephemeral *bool           `json:"ephemeral"`
	Modal     json.RawMessage `json:"modal"`
}

func interactionOp(i *Instance, args json.RawMessage) (*interaction, interactionArgs, error) {
	a, err := decode[interactionArgs](args)
	if err != nil {
		return nil, a, err
	}
	x, err := i.lookupInteraction(a.H)
	return x, a, err
}

func opInteractionState(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	x, _, err := interactionOp(i, args)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"replied": x.repliedFlag.Load(), "deferred": x.deferredFlag.Load()}, nil
}

// replyCreate converts a reply input, applying the command's ephemeral default.
func (x *interaction) replyCreate(in messageInput) (discord.MessageCreate, error) {
	mc, err := x.i.messageCreate(in, true)
	if err != nil {
		return mc, err
	}
	e := x.ephemeralDefault
	if in.Ephemeral != nil {
		e = *in.Ephemeral
	}
	mc.Flags |= ephemeralFlags(e)
	return mc, nil
}

func opInteractionReply(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	x, a, err := interactionOp(i, args)
	if err != nil {
		return nil, err
	}
	mc, err := x.replyCreate(a.Message)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) { return nil, x.reply(ctx, mc) }, nil
}

func opInteractionDefer(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	x, a, err := interactionOp(i, args)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) { return nil, x.deferResponse(ctx, a.Ephemeral) }, nil
}

func opInteractionFollowUp(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	x, a, err := interactionOp(i, args)
	if err != nil {
		return nil, err
	}
	mc, err := x.replyCreate(a.Message)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		m, err := x.followUp(ctx, mc)
		if err != nil {
			return nil, err
		}
		return i.messageDTO(m), nil
	}, nil
}

func opInteractionUpdate(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	x, a, err := interactionOp(i, args)
	if err != nil {
		return nil, err
	}
	mc, err := i.messageCreate(a.Message, false)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) { return nil, x.update(ctx, mc) }, nil
}

func opInteractionShowModal(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	x, a, err := interactionOp(i, args)
	if err != nil {
		return nil, err
	}
	var in modalInput
	if err := json.Unmarshal(a.Modal, &in); err != nil {
		return nil, argErr("showModal(): invalid modal: %v", err)
	}
	m, err := i.convertModal(in)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		if err := x.showModal(ctx, m); err != nil {
			return nil, fmt.Errorf("%w", err)
		}
		return nil, nil
	}, nil
}
