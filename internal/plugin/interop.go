package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cicord/cicord/internal/state"
)

type Caller struct {
	Plugin   string `json:"plugin"`
	Bot      string `json:"bot"`
	Instance string `json:"instance"`
	Source   string `json:"source"`
	Verified bool   `json:"verified"`
}
type ResourceScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Principal struct {
	ResourceScope *ResourceScope `json:"resourceScope,omitempty"`
	UserID        string         `json:"userId"`
	Access        string         `json:"access"`
}
type Call struct {
	Caller         Caller     `json:"caller"`
	OnBehalfOf     *Principal `json:"onBehalfOf,omitempty"`
	IdempotencyKey string     `json:"idempotencyKey,omitempty"`
}
type Request struct {
	Plugin, Name, IdempotencyKey string
	Input                        json.RawMessage
	TimeoutMs                    int
}
type CallFrame struct {
	PrivacyGuard bool
	Path         []string
	Principal    *Principal
	ReadOnly     bool
	Publish      func(context.Context, string, int, string, json.RawMessage) error
	KV           state.KV
}
type callKey struct{}
type startKey struct{}
type privacyKey struct{}

func WithPrivacyGuard(ctx context.Context) context.Context {
	return context.WithValue(ctx, privacyKey{}, true)
}

func BeforeDelivery(ctx context.Context, start func(context.Context) error) context.Context {
	return context.WithValue(ctx, startKey{}, start)
}

type EventMeta struct {
	ID        string    `json:"id"`
	Topic     string    `json:"topic"`
	Version   int       `json:"version"`
	At        time.Time `json:"at"`
	Publisher Caller    `json:"publisher"`
}
type Event struct {
	Meta    EventMeta       `json:"meta"`
	Payload json.RawMessage `json:"payload"`
}
type InteropError struct {
	Code, Message string
	Unavailable   bool
}

func (e *InteropError) Error() string { return e.Code + ": " + e.Message }

func (i *Instance) invocationKV(inv *invocation) state.KV {
	if inv != nil && inv.frame.KV != nil {
		return inv.frame.KV
	}
	return i.env.KV
}

var interopSlots = make(chan struct{}, 32)

func (i *Instance) Invoke(ctx context.Context, kind, name string, input json.RawMessage, call Call, frame CallFrame) (json.RawMessage, error) {
	select {
	case interopSlots <- struct{}{}:
		defer func() { <-interopSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	vm, err := i.privateVM(ctx, frame)
	if err != nil {
		return nil, err
	}
	defer vm.shutdown()
	ctx = context.WithValue(ctx, callKey{}, frame)
	r := <-vm.dispatch(ctx, kind, name, struct {
		Input json.RawMessage `json:"input"`
		Call  Call            `json:"call"`
	}{input, call}, nil)
	if r.err != nil {
		return nil, r.err
	}
	return json.RawMessage(r.data), nil
}
func (i *Instance) DeliverEvent(ctx context.Context, event Event) error {
	if err := i.require(capEventsSubscribe); err != nil {
		return err
	}
	return (<-i.dispatch(ctx, "subscription", event.Meta.Topic, event, nil)).err
}
func opPluginsHas(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	a, err := decode[struct{ Plugin string }](args)
	if err != nil {
		return nil, err
	}
	return i.env.HasPlugin != nil && i.env.HasPlugin(a.Plugin), nil
}
func opFeature(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	a, err := decode[struct{ Feature string }](args)
	if err != nil {
		return nil, err
	}
	v, ok := i.env.Features[a.Feature]
	return ok && v == "", nil
}
func opInterop(kind string) asyncOp {
	return func(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		a, err := decode[Request](args)
		if err != nil {
			return nil, err
		}
		if i.env.InvokePlugin == nil {
			return nil, &InteropError{"not_found", "no provider installed", true}
		}
		if len(a.Input) == 0 || len(a.Input) > maxKVValue || len(a.IdempotencyKey) > 256 || a.TimeoutMs < 0 {
			return nil, &InteropError{Code: "invalid_input", Message: "invalid call arguments"}
		}
		frame := inv.frame
		return func(ctx context.Context) (any, error) { return i.env.InvokePlugin(ctx, kind, a, frame) }, nil
	}
}
func readonlyOp(op, args string) bool {
	if op == "kv.rank" {
		a, err := decode[struct{ Method string }](json.RawMessage(args))
		if err != nil {
			return false
		}
		switch a.Method {
		case "score", "rank", "top", "around", "count":
			return true
		}
		return false
	}
	switch op {
	case "server.protection", "server.members", "channel.pins", "message.reactionUsers", "server.overlays", "assets.read", "attachment.bytes", "channel.memberOverwrite", "channel.messages", "channel.message", "channel.previewPurge", "server.invites", "server.auditLog", "server.bans", "server.member", "server.voiceMembers", "role.members", "kv.get", "kv.list", "plugins.query", "files.read", "files.list", "files.bytes", "index.search", "kv.entries", "events.list", "events.users", "message.pollVoters", "analytics.summary":
		return true
	}
	return false
}
func trustedPrincipal(data any) *Principal {
	var member *memberDTO
	switch d := data.(type) {
	case interactionDTO:
		member = d.Member
	case routeDTO:
		if d.Viewer != nil {
			member = d.Viewer.Member
		}
	}
	if member == nil {
		return nil
	}
	return &Principal{UserID: member.User.ID, Access: member.Access}
}

var ErrDeliverySuperseded = errors.New("delivery no longer owned by worker")

func opHasFeature(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	a, err := decode[struct{ Plugin, Feature string }](args)
	if err != nil {
		return nil, err
	}
	return i.env.HasFeature != nil && i.env.HasFeature(a.Plugin, a.Feature), nil
}
func opReportFeature(i *Instance, inv *invocation, args json.RawMessage) (any, error) {
	if inv.frame.ReadOnly {
		return nil, &CapabilityError{Capability: "read-only"}
	}
	a, err := decode[struct {
		Feature, Reason string
		Active          bool
	}](args)
	if err != nil {
		return nil, err
	}
	if len(a.Reason) > 256 {
		return nil, argErr("feature reason exceeds 256 bytes")
	}
	if i.env.ReportFeature == nil {
		return nil, argErr("feature reporting unavailable")
	}
	return nil, i.env.ReportFeature(a.Feature, a.Active, a.Reason)
}

// Keep the principal and write permissions of each request in their own JS realm.
// A promise retained in module scope cannot move another request's continuation
// into a privileged host turn. Durable cross-request state is stored in KV.
func (i *Instance) privateVM(ctx context.Context, frame CallFrame) (*Instance, error) {
	if err := i.checkState("interop"); err != nil {
		return nil, err
	}
	def := *i.Definition()
	def.HasOnStart, def.HasOnStop = false, false
	env := i.env
	env.IsolateMemberCalls = false
	// The live installation completed restore before serving. Re-entering it here while a call holds the privacy guard would deadlock.
	env.BeforeStart = nil
	vm, err := NewInstance(i.code, &def, env)
	if err != nil {
		return nil, err
	}
	vm.bg.frame = frame
	if err := vm.Start(ctx); err != nil {
		vm.shutdown()
		return nil, err
	}
	return vm, nil
}

// Member requests and nested peer requests have separate admission pools: one
// pool of callers waiting on their peers cannot consume all provider slots.
var memberSlots = make(chan struct{}, 64)

func (i *Instance) privateMember(ctx context.Context, run func(*Instance) error) error {
	select {
	case memberSlots <- struct{}{}:
		defer func() { <-memberSlots }()
	default:
		return ErrBusy
	}
	vm, err := i.privateVM(ctx, CallFrame{})
	if err != nil {
		return err
	}
	defer vm.shutdown()
	stop := context.AfterFunc(i.ctx, func() { vm.cancel(ErrStopped) })
	defer stop()
	return run(vm)
}
