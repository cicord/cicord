package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/state"
)

const deferredTaskPrefix = "private-command:"

type deferredKey struct{}
type deferredInfo struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	ChannelID string    `json:"channelId"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type deferredReceipt struct {
	deferredInfo
	Token, Application, Source, Name string
	Started, Replying                bool
	taskID                           string
	Locale                           string
}

func deferredRecord(user, id string) string { return "user:" + user + ":" + id }
func (i *Instance) deferredBinding(id string) string {
	return i.env.TaskScope + ":" + i.env.DeferredSource + ":" + id
}

var errPrivateTask = errors.New("private command task unavailable")

func opDeferTask(i *Instance, inv *invocation, raw json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capScheduler); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		H, Name string
		Payload json.RawMessage
	}](raw)
	if err != nil {
		return nil, err
	}
	x, err := i.lookupInteraction(a.H)
	if err != nil {
		return nil, err
	}
	if inv == nil || inv.interaction != x || x.kind != "command" || x.it.ServerID == "" || x.it.ServerID != i.env.ServerID || i.env.DeferredSecrets == nil || i.env.DeferredSource == "" || i.env.Tasks == nil {
		return nil, errPrivateTask
	}
	if _, ok := i.env.DeferredRecords.(state.AdvancedKV); !ok {
		return nil, errPrivateTask
	}
	if len(a.Payload) > maxKVValue {
		return nil, argErr("deferTask: payload larger than %d bytes", maxKVValue)
	}
	if !i.Definition().HasTask(a.Name) {
		return nil, errPrivateTask
	}
	return func(ctx context.Context) (any, error) {
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.handedOff || x.replyDone || (x.acked && (!x.deferredMsg || !x.privateDefer)) {
			return nil, errPrivateTask
		}
		if !x.acked {
			if err := x.deferLocked(ctx, true); err != nil {
				return nil, errPrivateTask
			}
		}
		id := deferredTaskPrefix + newID()
		r := deferredReceipt{deferredInfo: deferredInfo{ID: x.it.ID, UserID: x.it.User.ID, ChannelID: x.it.ChannelID, ExpiresAt: x.created.Add(14 * time.Minute)}, Token: x.it.Token, Application: x.appID(), Source: i.env.DeferredSource, Name: a.Name, Locale: x.it.Locale}
		data, _ := json.Marshal(r)
		sealed, err := i.env.DeferredSecrets.SealEphemeral(i.deferredBinding(id), data)
		if err != nil {
			return nil, errPrivateTask
		}
		key := deferredRecord(r.UserID, id)
		if err = i.env.DeferredRecords.Set(ctx, key, sealed, time.Until(r.ExpiresAt)); err != nil {
			return nil, errPrivateTask
		}
		// The queue carries only the caller's payload and erasure subject, never a credential.
		payload, _ := json.Marshal(struct {
			UserID  string          `json:"userId"`
			Payload json.RawMessage `json:"payload,omitempty"`
		}{r.UserID, a.Payload})
		if err = i.env.Tasks.Schedule(ctx, state.Task{ID: id, Scope: i.env.TaskScope, Name: a.Name, Payload: payload, RunAt: time.Now().Add(time.Second)}); err != nil {
			_ = i.env.DeferredRecords.Delete(ctx, key)
			return nil, errPrivateTask
		}
		x.handedOff = true
		return nil, nil
	}, nil
}

func (i *Instance) readDeferredReceipt(id string, encrypted json.RawMessage) (deferredReceipt, bool) {
	var r deferredReceipt
	data, err := i.env.DeferredSecrets.OpenEphemeral(i.deferredBinding(id), encrypted)
	if err != nil {
		return r, false
	}
	valid := json.Unmarshal(data, &r) == nil
	return r, valid
}

func (i *Instance) runDeferred(ctx context.Context, name string, payload json.RawMessage, d TaskDelivery) error {
	kv, ok := i.env.DeferredRecords.(state.AdvancedKV)
	if !ok || i.env.DeferredSecrets == nil {
		return nil
	}
	var p struct {
		UserID  string          `json:"userId"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(payload, &p) != nil || p.UserID == "" {
		return errPrivateTask
	}
	key := deferredRecord(p.UserID, d.ID)
	entries, err := kv.Entries(ctx, []string{key})
	if err != nil {
		return errPrivateTask
	}
	if len(entries) != 1 || entries[0] == nil {
		return nil
	}
	r, readable := i.readDeferredReceipt(d.ID, entries[0].Value)
	if !readable {
		// A different revision or encryption key cannot resume this private interaction.
		return nil
	}
	if r.UserID != p.UserID || r.Source != i.env.DeferredSource || r.Name != name || !time.Now().Before(r.ExpiresAt) {
		_ = kv.Delete(ctx, key)
		return nil
	}
	// A durable CAS precedes all plugin/provider work. Ambiguous execution is never retried.
	r.taskID = d.ID
	if r.Started {
		// A reply already attempted may have succeeded despite a lost response.
		if r.Replying {
			return nil
		}
		content := "This request was interrupted. Please ask again."
		if strings.HasPrefix(strings.ToLower(r.Locale), "fr") {
			content = "Cette demande a été interrompue. Veuillez réessayer."
		}
		message, _ := json.Marshal(map[string]any{"message": map[string]string{"content": content}})
		fn, err := opDeferredReply(i, &invocation{kind: "task", deferred: &r}, message)
		if err != nil {
			return err
		}
		_, err = fn(ctx)
		if err == nil {
			_ = kv.Delete(ctx, key)
		}
		return err
	}
	r.Started = true
	data, _ := json.Marshal(r)
	sealed, err := i.env.DeferredSecrets.SealEphemeral(i.deferredBinding(d.ID), data)
	if err != nil {
		return errPrivateTask
	}
	result, err := kv.Atomic(ctx, []state.KVCheck{{Key: key, Version: &entries[0].Version}}, []state.KVMutation{{Op: "set", Key: key, Value: sealed, TTL: time.Until(r.ExpiresAt)}})
	if err != nil {
		return errPrivateTask
	}
	if !result.OK {
		return nil
	}
	d.Interaction = &r.deferredInfo
	ctx = context.WithValue(ctx, deferredKey{}, &r)
	err = (<-i.dispatch(ctx, "task", name, payloadDTO{Payload: rawOrNil(p.Payload), Task: &d}, nil)).err
	if err == nil {
		_ = kv.Delete(ctx, key)
	}
	return err
}
func opDeferredReply(i *Instance, inv *invocation, raw json.RawMessage) (func(context.Context) (any, error), error) {
	if inv == nil || inv.deferred == nil || inv.kind != "task" {
		return nil, errPrivateTask
	}
	a, err := decode[struct {
		Message messageInput `json:"message"`
	}](raw)
	if err != nil {
		return nil, err
	}
	mc, err := i.messageCreate(a.Message, true)
	if err != nil {
		return nil, err
	}
	// Only a content edit: ephemerality was fixed by the original private deferral.
	if len(mc.Components) > 0 {
		if err = i.scopeDMControls(false, mc.Components); err != nil {
			return nil, err
		}
	}
	mc.Flags = 0
	r := inv.deferred
	return func(ctx context.Context) (any, error) {
		if !time.Now().Before(r.ExpiresAt) {
			return nil, errPrivateTask
		}
		// Record the response attempt before sending: an ambiguous edit is never
		// repeated or replaced with a fallback that could clobber a completed answer.
		kv, ok := i.env.DeferredRecords.(state.AdvancedKV)
		if !ok {
			return nil, errPrivateTask
		}
		key := deferredRecord(r.UserID, r.taskID)
		entries, err := kv.Entries(ctx, []string{key})
		if err != nil || len(entries) != 1 || entries[0] == nil {
			return nil, errPrivateTask
		}
		data, err := i.env.DeferredSecrets.OpenEphemeral(i.deferredBinding(r.taskID), entries[0].Value)
		if err != nil {
			return nil, errPrivateTask
		}
		var saved deferredReceipt
		if json.Unmarshal(data, &saved) != nil || !saved.Started || saved.Replying {
			return nil, errPrivateTask
		}
		saved.Replying = true
		data, _ = json.Marshal(saved)
		sealed, err := i.env.DeferredSecrets.SealEphemeral(i.deferredBinding(r.taskID), data)
		if err != nil {
			return nil, errPrivateTask
		}
		committed, err := kv.Atomic(ctx, []state.KVCheck{{Key: key, Version: &entries[0].Version}}, []state.KVMutation{{Op: "set", Key: key, Value: sealed, TTL: time.Until(r.ExpiresAt)}})
		if err != nil || !committed.OK {
			return nil, errPrivateTask
		}
		r.Replying = true
		m, err := i.env.Discord.GetMember(ctx, i.env.ServerID, r.UserID)
		if err != nil {
			return nil, errPrivateTask
		}
		s := i.readSnapshot()
		policy := i.audience(s, ref{ID: r.ChannelID, set: true})
		if policy == nil {
			return nil, errPrivateTask
		}
		perms, _ := policy.Permissions(r.UserID, m.RoleIDs, s.OwnerID, m.TimeoutUntil != nil && m.TimeoutUntil.After(time.Now()))
		if !perms.Has(contentRead) {
			return nil, errPrivateTask
		}
		if _, err = i.env.Discord.EditOriginalResponse(ctx, r.Application, r.Token, mc); err != nil {
			return nil, errPrivateTask
		}
		return nil, nil
	}, nil
}
