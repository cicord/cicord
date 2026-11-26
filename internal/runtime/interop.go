package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

func callerOf(in config.Instance) plugin.Caller {
	return plugin.Caller{Plugin: in.Plugin.Manifest.Metadata.Name, Bot: in.Bot, Instance: in.Name, Source: in.Plugin.Registry, Verified: in.Plugin.Verified}
}
func stableID(parts ...string) string {
	b, _ := json.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func validateJSON(s *jsonschema.Schema, b json.RawMessage) error {
	if s == nil {
		return errors.New("no schema")
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	return s.Validate(v)
}
func eventEnabled(sub *instanceRT, topic string, v int) bool {
	if sub.plugin == nil || !slices.Contains(sub.plugin.def.Subscriptions, topic) || !slices.Contains(sub.cfg.Grants, "events.subscribe") {
		return false
	}
	spec := sub.cfg.Plugin.Manifest.Spec.Interop
	if spec == nil {
		return v == 1
	}
	for _, s := range spec.Subscribes {
		if s.Topic == topic && s.V() == v {
			return true
		}
	}
	return false
}
func (r *Runtime) publishEvent(ctx context.Context, p *instanceRT, name string, v int, key string, payload json.RawMessage) error {
	e, err := r.prepareEvent(p, name, v, key, payload)
	if err != nil {
		return err
	}
	if len(e.Subscribers) == 0 {
		return nil
	}
	return r.opts.Store.Interop().PublishEvent(ctx, e)
}
func (r *Runtime) prepareEvent(p *instanceRT, name string, v int, key string, payload json.RawMessage) (state.EventFanout, error) {
	in := p.cfg
	s := in.Plugin.Manifest.Spec.Interop
	scope := "bot"
	if s != nil {
		c, ok := in.Plugin.Contracts[config.ContractKey(name, v)]
		valid := false
		for _, t := range s.Publishes {
			if t.Topic == name && t.V() == v {
				valid = true
				scope = t.Scope
				break
			}
		}
		if !valid || !ok || validateJSON(c.Input, payload) != nil {
			p.eventRejects.Add(1)
			r.log.Warn("event rejected", "instance", p.id(), "topic", name, "version", v)
			return state.EventFanout{}, fmt.Errorf("events.publish: undeclared topic/version or invalid payload for %s v%d", name, v)
		}
	} else if v != 1 {
		return state.EventFanout{}, errors.New("events.publish: legacy topics use version 1")
	}
	topic := in.Plugin.Manifest.Metadata.Name + "." + name
	if key == "" {
		key = rand.Text()
	}
	meta := plugin.EventMeta{ID: stableID(in.ID(), topic, fmt.Sprint(v), key), Topic: topic, Version: v, At: time.Now().UTC(), Publisher: callerOf(in)}
	data, err := json.Marshal(plugin.Event{Meta: meta, Payload: payload})
	if err != nil {
		return state.EventFanout{}, err
	}
	e := state.EventFanout{ID: meta.ID, Server: in.Server, Publisher: in.ID(), Topic: topic, Payload: data, At: meta.At}
	for _, sub := range p.revision.instances {
		if sub.cfg.Server != in.Server || (sub.cfg.Bot != in.Bot && (scope != "server" || sub.cfg.Plugin.Manifest.Spec.Interop == nil)) || !eventEnabled(sub, topic, v) {
			continue
		}
		e.Subscribers = append(e.Subscribers, instanceScope(sub.cfg))
	}
	return e, nil
}
func (r *Runtime) PurgeInteropMember(ctx context.Context, server, userID string) (int, error) {
	return r.opts.Store.Interop().PurgeInteropMember(ctx, server, userID)
}
func interopFailure(code, msg string) error { return &plugin.InteropError{Code: code, Message: msg} }
func resolvePeer(from *instanceRT, name string) (*instanceRT, error) {
	var target *instanceRT
	choice := from.cfg.Interop.Targets[name]
	found := false
	for _, i := range from.revision.instances {
		if i.cfg.Server != from.cfg.Server || i.cfg.Plugin.Manifest.Metadata.Name != name {
			continue
		}
		if i.cfg.Disabled() {
			if choice == i.cfg.Bot+"/"+i.cfg.Name {
				return nil, &plugin.InteropError{Code: "not_found", Message: "provider installation is disabled", Unavailable: true}
			}
			continue
		}
		found = true
		if choice != "" && choice != i.cfg.Bot+"/"+i.cfg.Name {
			continue
		}
		if target != nil {
			return nil, interopFailure("ambiguous", "configure interop.targets."+name)
		}
		target = i
	}
	if target != nil {
		return target, nil
	}
	if found || choice != "" {
		return nil, interopFailure("denied", "configured provider is unavailable")
	}
	return nil, &plugin.InteropError{Code: "not_found", Message: "no installation of " + name + " on this server", Unavailable: true}
}

type privacyGate interface {
	WithReplayPrivacy(context.Context, string, string, json.RawMessage, func() error) (bool, error)
	ErasedPayload(context.Context, string, string, json.RawMessage) (bool, error)
}

func (r *Runtime) invokePeer(ctx context.Context, from *instanceRT, kind string, req plugin.Request, frame plugin.CallFrame) (out json.RawMessage, err error) {
	gate, ok := any(r).(privacyGate)
	if !ok {
		return r.runPeer(ctx, from, kind, req, frame)
	}
	payload, _ := json.Marshal([]any{frame.Principal, req.Input})
	server := from.cfg.Server
	if frame.PrivacyGuard {
		erased, err := gate.ErasedPayload(ctx, server, instanceScope(from.cfg), payload)
		if err != nil {
			return nil, interopFailure("handler_error", "privacy state unavailable")
		}
		if erased {
			return nil, interopFailure("denied", "member data has been erased")
		}
		return r.runPeer(ctx, from, kind, req, frame)
	}
	frame.PrivacyGuard = true
	erased, err := gate.WithReplayPrivacy(ctx, server, instanceScope(from.cfg), payload, func() error { var e error; out, e = r.runPeer(ctx, from, kind, req, frame); return e })
	if erased {
		return nil, interopFailure("denied", "member data has been erased")
	}
	return out, err
}
func (r *Runtime) runPeer(ctx context.Context, from *instanceRT, kind string, req plugin.Request, frame plugin.CallFrame) (out json.RawMessage, err error) {
	full := req.Plugin + "." + req.Name
	spec := from.cfg.Plugin.Manifest.Spec.Interop
	cap := "plugins.query:" + full
	var consumed, provided []string
	if spec != nil {
		consumed = spec.Consumes.Queries
		if kind == "action" {
			consumed = spec.Consumes.Actions
		}
	}
	if kind == "action" {
		cap = "plugins.call:" + full
	}
	deny := func(detail string) error {
		from.interopProblem.Store(cap + ": " + detail)
		return interopFailure("denied", detail)
	}
	if !config.HasGrant(from.cfg.Grants, cap) || !slices.Contains(consumed, full) {
		return nil, deny("undeclared or ungranted " + cap)
	}
	provider, err := resolvePeer(from, req.Plugin)
	if err != nil {
		return nil, err
	}
	in := provider.cfg
	providerSpec := in.Plugin.Manifest.Spec.Interop
	if providerSpec != nil {
		provided = providerSpec.Provides.Queries
		if kind == "action" {
			provided = providerSpec.Provides.Actions
		}
	}
	if !slices.Contains(provided, req.Name) {
		return nil, interopFailure("not_found", "provider does not provide "+full)
	}
	id := from.cfg.Bot + "/" + from.cfg.Name
	if allow := in.Interop.AllowCallers; allow != nil && !slices.Contains(allow, id) {
		return nil, deny("caller " + id + " is not allowed")
	}
	if provider.inst() == nil {
		return nil, interopFailure("handler_error", "provider is installed but not running")
	}
	path := frame.Path
	if len(path) == 0 {
		path = []string{from.id()}
	}
	if len(path) > 2 || slices.Contains(path, provider.id()) {
		return nil, interopFailure("cycle", "cycle or call depth greater than two")
	}
	c := in.Plugin.Contracts[config.ContractKey(req.Name, 1)]
	if err := validateJSON(c.Input, req.Input); err != nil {
		return nil, interopFailure("invalid_input", "input violates "+full+" contract")
	}
	timeout, maxTimeout := 1000, 5000
	if kind == "action" {
		timeout, maxTimeout = 5000, 15000
	}
	if req.TimeoutMs > maxTimeout {
		return nil, interopFailure("invalid_input", "timeout exceeds maximum")
	}
	if req.TimeoutMs > 0 {
		timeout = req.TimeoutMs
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	call := plugin.Call{Caller: callerOf(from.cfg), OnBehalfOf: frame.Principal, IdempotencyKey: req.IdempotencyKey}
	next := plugin.CallFrame{PrivacyGuard: frame.PrivacyGuard, Path: append(slices.Clone(path), provider.id()), Principal: frame.Principal, ReadOnly: kind == "query" || frame.ReadOnly}
	run := func(kv state.KV) (json.RawMessage, error) {
		next.KV = kv
		result, err := provider.inst().Invoke(ctx, kind, req.Name, req.Input, call, next)
		if ctx.Err() != nil || errors.Is(err, plugin.ErrHandlerTimeout) {
			return nil, interopFailure("timeout", "provider timed out")
		}
		if err != nil {
			var se *plugin.ScriptError
			if errors.As(err, &se) && se.Name == "InteropError" && (se.InteropCode == "cycle" || se.InteropCode == "denied") {
				return nil, interopFailure(se.InteropCode, se.Message)
			}
			return nil, interopFailure("handler_error", err.Error())
		}
		if err := validateJSON(c.Output, result); err != nil {
			return nil, interopFailure("handler_error", "invalid provider output")
		}
		return result, nil
	}
	if kind == "query" {
		return run(nil)
	}
	if next.ReadOnly {
		return nil, deny("read-only query cannot call actions")
	}
	if req.IdempotencyKey == "" {
		return nil, interopFailure("invalid_input", "idempotencyKey is required")
	}
	start := time.Now()
	defer func() {
		status := "done"
		if err != nil {
			status = err.Error()
		}
		for _, i := range []*instanceRT{from, provider} {
			r.log.Info("plugin action", "instance", i.id(), "caller", from.id(), "provider", provider.id(), "action", req.Name, "key", req.IdempotencyKey, "duration", time.Since(start), "outcome", status)
		}
	}()
	// Include authority and canonical JSON input in the receipt fingerprint.
	var input any
	dec := json.NewDecoder(bytes.NewReader(req.Input))
	dec.UseNumber()
	if err := dec.Decode(&input); err != nil {
		return nil, interopFailure("invalid_input", "invalid JSON")
	}
	// The receipt is (provider, caller, action, key): keys derive from the caller's
	// own origin, so another granted caller can neither squat nor probe them. The
	// fingerprint binds the input and the principal's identity (user and resource
	// scope): a stored result computed on behalf of one member is never returned to
	// a request on behalf of another. The principal's access level is not part of
	// it: a retry of the same request after the member's access changed replays the
	// action's original outcome (the effect already happened) instead of failing
	// with invalid_input; a new key reaches the provider, which re-checks access on
	// every call. Caller provenance (source, verified) is not part of it either, so
	// a retry still matches after a registry index refresh.
	raw, _ := json.Marshal([]any{principalIdentity(call.OnBehalfOf), input})
	actionID := stableID(in.ID(), from.cfg.ID(), req.Name, req.IdempotencyKey)
	owner := rand.Text()
	journal := r.opts.Store.Interop()
	for {
		cached, acquired, e := journal.ReserveAction(ctx, actionID, in.Server, stableID(string(raw)), owner, time.Now())
		if e != nil {
			if errors.Is(e, state.ErrErasedResult) {
				return nil, interopFailure("denied", "action result erased")
			}
			if ctx.Err() != nil {
				return nil, interopFailure("timeout", "waiting for action")
			}
			if errors.Is(e, state.ErrActionMismatch) {
				return nil, interopFailure("invalid_input", e.Error())
			}
			return nil, interopFailure("handler_error", "action journal unavailable")
		}
		if cached != nil {
			if gate, ok := any(r).(privacyGate); ok {
				erased, e := gate.ErasedPayload(ctx, in.Server, "", cached)
				if e != nil {
					return nil, interopFailure("handler_error", "privacy state unavailable")
				}
				if erased {
					return nil, interopFailure("denied", "cached result contains erased data")
				}
			}
			return cached, nil
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			return nil, interopFailure("timeout", "waiting for action")
		case <-time.After(15 * time.Millisecond):
		}
	}
	defer func() {
		if err != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer stop()
			_ = journal.FailAction(cleanup, actionID, owner)
		}
	}()
	var events []state.EventFanout
	var eventMu sync.Mutex
	var outboxSize int
	next.Publish = func(_ context.Context, name string, v int, key string, data json.RawMessage) error {
		e, err := r.prepareEvent(provider, name, v, key, data)
		if err != nil {
			return err
		}
		if len(e.Subscribers) == 0 {
			return nil
		}
		eventMu.Lock()
		defer eventMu.Unlock()
		if len(events) >= 32 || outboxSize+len(e.Payload) > 4<<20 {
			return errors.New("action event outbox limit exceeded")
		}
		outboxSize += len(e.Payload)
		events = append(events, e)
		return nil
	}
	scope := instanceScope(in)
	if store, ok := r.opts.Store.(interface {
		BufferedScope(context.Context, string) (state.KV, func(context.Context, state.KV) error)
		WithAtomicScope(context.Context, string, func(state.KV) error) error
	}); ok {
		kv, flush := store.BufferedScope(ctx, scope)
		out, err = run(kv)
		if err != nil {
			return nil, err
		}
		err = store.WithAtomicScope(ctx, scope, func(tx state.KV) error {
			if err := flush(ctx, tx); err != nil {
				return err
			}
			receipt, ok := tx.(interface {
				CompleteInterop(context.Context, string, string, json.RawMessage) error
			})
			if !ok {
				return errors.New("missing transactional action receipt")
			}
			eventMu.Lock()
			defer eventMu.Unlock()
			if len(events) > 0 {
				sqltx, ok := tx.(interface{ InteropTx() *sql.Tx })
				if !ok || sqltx.InteropTx() == nil {
					return errors.New("missing transactional event outbox")
				}
				for _, event := range events {
					if err := journal.PublishEventTx(ctx, sqltx.InteropTx(), event); err != nil {
						return err
					}
				}
			}
			return receipt.CompleteInterop(ctx, actionID, owner, out)
		})
	} else {
		buf := newActionBuffer(r.opts.Store.KV(scope))
		out, err = run(buf)
		if err != nil {
			return nil, err
		}
		eventMu.Lock()
		defer eventMu.Unlock()
		err = journal.CommitAction(ctx, actionID, owner, scope, buf.writes(), out, events...)
	}
	if err != nil {
		return nil, interopFailure("handler_error", err.Error())
	}
	return out, nil
}
func (i *instanceRT) featureState() map[string]string {
	out := maps.Clone(i.cfg.Features)
	i.featureReports.Range(func(k, v any) bool {
		name := k.(string)
		if original, ok := out[name]; ok && original == "" {
			out[name] = v.(string)
		}
		return true
	})
	return out
}
func checkedTask(ctx context.Context, inst *instanceRT, t state.TaskLease, r *Runtime) error {
	data := t.Payload
	deliver := func(ctx context.Context) error {
		return inst.inst().RunTask(ctx, t.Name, t.Payload, plugin.TaskDelivery{ID: t.ID, Attempt: t.Attempt})
	}
	if t.Event {
		var ev plugin.Event
		if err := json.Unmarshal(t.Payload, &ev); err != nil {
			return err
		}
		if !eventEnabled(inst, ev.Meta.Topic, ev.Meta.Version) {
			return errSubscriptionUnavailable
		}
		data = ev.Payload
		deliver = func(ctx context.Context) error { return inst.inst().DeliverEvent(ctx, ev) }
	}
	if gate, ok := any(r).(interface {
		WithReplayPrivacy(context.Context, string, string, json.RawMessage, func() error) (bool, error)
	}); ok {
		_, err := gate.WithReplayPrivacy(ctx, inst.cfg.Server, instanceScope(inst.cfg), data, func() error { return deliver(plugin.WithPrivacyGuard(ctx)) })
		return err
	}
	return deliver(ctx)
}

// principalIdentity is the part of a principal an action receipt binds: who the call is
// on behalf of and in which resource scope, not their current access level.
func principalIdentity(p *plugin.Principal) any {
	if p == nil {
		return nil
	}
	return struct {
		UserID        string                `json:"userId"`
		ResourceScope *plugin.ResourceScope `json:"resourceScope,omitempty"`
	}{p.UserID, p.ResourceScope}
}
