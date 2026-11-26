package plugin

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/cicord/cicord/internal/discord"
)

type auditChangeDTO struct {
	AddedPermissions   []string        `json:"addedPermissions,omitempty"`
	RemovedPermissions []string        `json:"removedPermissions,omitempty"`
	Key                string          `json:"key"`
	Old                json.RawMessage `json:"old,omitempty"`
	New                json.RawMessage `json:"new,omitempty"`
}
type attributionDTO struct {
	Kind      string          `json:"kind"`
	Certainty string          `json:"certainty"`
	Plugin    *auditPluginDTO `json:"plugin,omitempty"`
	Revision  string          `json:"revision,omitempty"`
}
type auditPluginDTO struct {
	Plugin   string `json:"plugin"`
	Bot      string `json:"bot"`
	Instance string `json:"instance"`
}
type auditDTO struct {
	ID          string                     `json:"id"`
	At          string                     `json:"at"`
	Action      string                     `json:"action"`
	ActorID     string                     `json:"actorId,omitempty"`
	Actor       *userDTO                   `json:"actor,omitempty"`
	TargetID    string                     `json:"targetId,omitempty"`
	TargetType  string                     `json:"targetType,omitempty"`
	Changes     []auditChangeDTO           `json:"changes"`
	Options     map[string]json.RawMessage `json:"options,omitempty"`
	Reason      string                     `json:"reason,omitempty"`
	Mergeable   bool                       `json:"mergeable"`
	Attribution attributionDTO             `json:"attribution"`
}

func (i *Instance) auditDTO(e discord.AuditLogEntryEvent) auditDTO {
	a := discord.DescribeAuditAction(e.ActionType)
	out := auditDTO{ID: e.ID, At: jsTime(discord.SnowflakeTime(e.ID)), Action: a.Name, ActorID: e.UserID, TargetID: e.TargetID, TargetType: a.Target, Reason: e.Reason, Mergeable: a.Mergeable, Options: e.Options, Changes: []auditChangeDTO{}, Attribution: attributionDTO{Kind: "unknown", Certainty: "unknown"}}
	if e.Actor != nil {
		u := i.userDTO(*e.Actor)
		out.Actor = &u
	}
	for _, ch := range e.Changes {
		change := auditChangeDTO{Key: ch.Key, Old: ch.Old, New: ch.New}
		if ch.Key == "permissions" || ch.Key == "allow" || ch.Key == "deny" {
			old, oldErr := strconv.ParseUint(strings.Trim(string(ch.Old), "\""), 10, 64)
			next, newErr := strconv.ParseUint(strings.Trim(string(ch.New), "\""), 10, 64)
			if oldErr == nil && newErr == nil {
				change.AddedPermissions = discord.Permissions(next &^ old).Names()
				change.RemovedPermissions = discord.Permissions(old &^ next).Names()
			}
		}
		out.Changes = append(out.Changes, change)
	}
	if e.Attribution.Kind != "" {
		out.Attribution.Kind = e.Attribution.Kind
		out.Attribution.Certainty = e.Attribution.Certainty
	}
	if op := e.Attribution.Operation; op != nil {
		if op.Instance != "" {
			out.Attribution.Plugin = &auditPluginDTO{Plugin: op.Plugin, Bot: op.Bot, Instance: op.Instance}
		}
		out.Attribution.Revision = op.Revision
	}
	return out
}
func opAuditLog(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capAuditRead); err != nil {
		return nil, err
	}
	if !i.env.ServerModeration {
		return nil, argErr("auditLog() requires ServerModeration")
	}
	p, err := decode[struct {
		Action  string `json:"action"`
		ActorID string `json:"actorId"`
		Before  string `json:"before"`
		Limit   *int   `json:"limit"`
	}](args)
	if err != nil {
		return nil, err
	}
	q := discord.AuditQuery{ActorID: p.ActorID, Before: p.Before, Limit: 50}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}
	if q.Limit < 1 || q.Limit > 100 {
		return nil, argErr("auditLog(): limit must be 1 to 100")
	}
	if p.Action != "" {
		code, ok := discord.AuditActionCode(p.Action)
		if !ok {
			return nil, argErr("unknown audit action")
		}
		q.Action = code
	}
	for _, id := range []string{p.ActorID, p.Before} {
		if id != "" {
			if err := checkSnowflake("audit filter", id); err != nil {
				return nil, err
			}
		}
	}
	return func(ctx context.Context) (any, error) {
		entries, err := i.env.Discord.ListAuditLog(ctx, i.env.ServerID, q)
		if err != nil {
			return nil, err
		}
		out := make([]auditDTO, 0, len(entries))
		for _, e := range entries {
			if i.env.AttributeAudit != nil {
				e = i.env.AttributeAudit(ctx, e)
			}
			out = append(out, i.auditDTO(e))
		}
		return out, nil
	}, nil
}
