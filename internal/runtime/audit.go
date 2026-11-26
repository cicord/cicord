package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

const operationRetention = 7 * 24 * time.Hour

func operationPrefix(actor string, action int, target string) string {
	return fmt.Sprintf("%s:%d:%s:", actor, action, target)
}
func (r *Runtime) operationScope(origin discord.Operation) *discord.OperationScope {
	return &discord.OperationScope{Origin: origin, Record: func(ctx context.Context, op discord.Operation) {
		// The side effect already succeeded. Keep journal recovery independent of a
		// caller disconnect, and never turn failure to journal into a repeated action.
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		data, err := json.Marshal(op)
		if err == nil {
			key := fmt.Sprintf("%s%020d:%s", operationPrefix(op.ActorID, op.Action, op.Target), op.At.UnixMilli(), rand.Text())
			err = r.opts.Store.KV("emitted:"+op.ServerID).Set(bounded, key, data, operationRetention)
		}
		if err != nil {
			r.log.Warn("could not record emitted operation", "method", op.Method, "err", err)
		}
	}}
}
func (r *Runtime) attribute(ctx context.Context, e discord.AuditLogEntryEvent) discord.Attribution {
	a := discord.Attribution{Kind: "unknown", Certainty: "unknown"}
	if e.UserID == "" {
		return a
	}
	var own bool
	for _, h := range r.hosts() {
		if h.session.Self().ID == e.UserID {
			own = true
			break
		}
	}
	if !own {
		if e.Actor == nil {
			return a
		}
		a.Kind = "human"
		if e.Actor.Bot {
			a.Kind = "bot"
		}
		a.Certainty = "confirmed"
		if discord.DescribeAuditAction(e.ActionType).Mergeable {
			a.Certainty = "probable"
		}
		return a
	}
	a.Kind = "cicord"
	a.Certainty = "probable"
	at := discord.SnowflakeTime(e.ID)
	if at.IsZero() || e.TargetID == "" || discord.DescribeAuditAction(e.ActionType).Mergeable {
		return a
	}
	prefix := operationPrefix(e.UserID, e.ActionType, e.TargetID)
	cursor := fmt.Sprintf("%s%020d", prefix, at.Add(-30*time.Second).UnixMilli())
	entries, _, err := r.opts.Store.KV("emitted:"+e.ServerID).List(ctx, prefix, 2, cursor)
	if err != nil {
		return a
	}
	var match *discord.Operation
	for _, row := range entries {
		var op discord.Operation
		if json.Unmarshal(row.Value, &op) != nil {
			return a
		}
		if op.At.After(at.Add(30 * time.Second)) {
			break
		}
		if match != nil {
			return a
		}
		match = &op
	}
	if match != nil {
		a.Certainty = "confirmed"
		a.Operation = match
	}
	return a
}

func (r *Runtime) enrichAudit(ctx context.Context, client discord.Client, e discord.AuditLogEntryEvent) discord.AuditLogEntryEvent {
	if e.UserID != "" && e.Actor == nil {
		if u, err := client.GetUser(ctx, e.UserID); err == nil {
			e.Actor = &u
		}
	}
	e.Attribution = r.attribute(ctx, e)
	return e
}

// Only declared audit subscribers need actor lookups. Cap concurrent lookups so
// slow Discord responses cannot stall the gateway. A saturated queue reports
// unknown attribution and cannot authorize an automatic sanction.
func (h *botHost) dispatchAudit(e discord.AuditLogEntryEvent) {
	subscribed := false
	for _, i := range h.routing.Load().byServer[e.ServerID] {
		if i.plugin.def.HasEvent("auditLogEntry") {
			subscribed = true
			break
		}
	}
	if !subscribed {
		return
	}
	select {
	case h.rt.auditSlots <- struct{}{}:
		go func() {
			defer func() { <-h.rt.auditSlots }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			entry := h.rt.enrichAudit(ctx, h.session, e)
			if entry.Attribution.Kind == "cicord" && entry.Attribution.Certainty == "probable" && !discord.DescribeAuditAction(entry.ActionType).Mergeable {
				h.rt.log.Warn("unconfirmed audit activity by a project bot", "server", e.ServerID, "entry", e.ID)
			}
			h.dispatchPlugins(h.routing.Load(), entry)
		}()
	default:
		e.Attribution = discord.Attribution{Kind: "unknown", Certainty: "unknown"}
		h.dispatchPlugins(h.routing.Load(), e)
	}
}
