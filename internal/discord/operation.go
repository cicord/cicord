package discord

import (
	"context"
	"crypto/rand"
	"slices"
	"strings"
	"time"
)

// Operation holds only routing metadata. Request bodies, audit reasons, URLs,
// interaction tokens and credentials never belong in the operation journal.
type Operation struct {
	ServerID string `json:"serverId"`
	ActorID  string `json:"actorId"`
	Bot      string `json:"bot"`
	Plugin   string `json:"plugin,omitempty"`
	Instance string `json:"instance,omitempty"`
	Revision string `json:"revision,omitempty"`
	Method   string `json:"method"`
	Action   int    `json:"action,omitempty"`
	Target   string `json:"target,omitempty"`
	// Detail narrows an overwrite operation (audit actions 13–15) to the role or
	// member whose overwrite the call sent.
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
	// Nonce makes the journal key of one operation unique, so a call that failed
	// can withdraw what it journaled before it was sent.
	Nonce string `json:"nonce,omitempty"`
}
type OperationScope struct {
	Origin Operation
	Record func(context.Context, Operation)
	// Forget withdraws an operation journaled before a call that Discord then
	// rejected. Optional.
	Forget func(context.Context, Operation)
}
type operationContextKey struct{}

func WithOperations(ctx context.Context, origin *OperationScope) context.Context {
	if origin == nil {
		return ctx
	}
	return context.WithValue(ctx, operationContextKey{}, origin)
}
func Operations(ctx context.Context) *OperationScope {
	v, _ := ctx.Value(operationContextKey{}).(*OperationScope)
	return v
}
func (o *OperationScope) Reason(original string) string {
	if o == nil {
		return original
	}
	label := "cicord: " + o.Origin.Bot + "/" + o.Origin.Instance
	if o.Origin.Instance == "" {
		label = "cicord: reconcile " + o.Origin.Revision
	}
	if original != "" && original != "cicord "+o.Origin.Revision {
		label += ": " + original
	}
	runes := []rune(label)
	return string(runes[:min(len(runes), 512)])
}

// Effect is one audit-log entry that a successful call makes Discord write.
type Effect struct {
	Action int
	Target string
	// Detail is the overwritten role or member ID of an overwrite effect.
	Detail string
}

// Emitted journals the effects of a call identified by its method and target only
// (no request body). Adapters that know the body use CallEffects and Journal.
func (o *OperationScope) Emitted(ctx context.Context, method, target string, at time.Time) {
	o.Journal(ctx, method, CallEffects(method, target, nil, nil), at)
}

// Journal records the effects of one call and returns a function that withdraws
// them. Callers journal before sending the request when its effects are known
// then (so a gateway audit event can never arrive before its proof), and withdraw
// only when Discord rejected the call. Effects of creations, whose target is the
// created ID, are journaled after the response.
//
// Only effects that CORE-04 attribution can match are recorded: non-zero,
// non-mergeable audit actions with a snowflake target.
func (o *OperationScope) Journal(ctx context.Context, method string, effects []Effect, at time.Time) (withdraw func(context.Context)) {
	withdraw = func(context.Context) {}
	if o == nil || o.Record == nil {
		return withdraw
	}
	var recorded []Operation
	for _, e := range effects {
		if e.Action == 0 || DescribeAuditAction(e.Action).Mergeable || !IsSnowflake(e.Target) {
			continue
		}
		op := o.Origin
		op.Method, op.Action, op.Target, op.At = method, e.Action, e.Target, at.UTC()
		if IsSnowflake(e.Detail) || e.Detail == OverwriteRemovals {
			op.Detail = e.Detail
		}
		op.Nonce = randomNonce()
		o.Record(ctx, op)
		recorded = append(recorded, op)
	}
	if o.Forget == nil || len(recorded) == 0 {
		return withdraw
	}
	return func(ctx context.Context) {
		for _, op := range recorded {
			o.Forget(ctx, op)
		}
	}
}

func randomNonce() string { return rand.Text() }

// JournalAction is the action under which the journal files operations that can
// produce an entry of the given audit action. Discord logs one call that sends
// channel overwrites as overwrite creates, updates or deletes depending on the
// previous state, which the caller does not know: they share one family.
func JournalAction(action int) int {
	switch action {
	case AuditOverwriteUpdate, AuditOverwriteDelete:
		return AuditOverwriteCreate
	case 111, 112:
		// UpdateChannel and DeleteChannel on a thread: Discord logs thread.update
		// and thread.delete.
		return action - 100
	}
	return action
}

// Journaled reports whether the journal can hold the proof of an audit entry of this
// action: whether some adapter call records it (CallEffects). An own entry of another
// action can never be confirmed, so its absence of proof says nothing.
func Journaled(action int) bool {
	switch JournalAction(action) {
	case 1, 10, 11, 12, AuditOverwriteCreate, 20, 22, 23, AuditMemberUpdate, AuditMemberRoleUpdate,
		30, 31, 32, 50, 60, 61, 62, 90, 91, 92, 100, 101, 102, 110, 130, 131, 132, 140, 141, 142, 167:
		return true
	}
	return false
}

// OverwriteRemovals is the detail of the journal marker of a call that replaced a
// channel's overwrites. Discord writes one overwrite.delete entry per overwrite the
// call removed, and the request does not name them, so every such entry of the call
// shares this marker instead of claiming an operation of its own.
const OverwriteRemovals = "removed"

// Audit actions with a dedicated meaning in the journal.
const (
	AuditOverwriteCreate = 13
	AuditOverwriteUpdate = 14
	AuditOverwriteDelete = 15
)

// CallEffects lists the audit entries Discord writes for one successful call.
// fields are the top-level keys of a PATCH body (nil when unknown) and overwrites
// the role or member IDs of the permission overwrites it sends.
func CallEffects(method, target string, fields, overwrites []string) []Effect {
	switch method {
	case "UpdateChannel":
		var out []Effect
		if fields == nil || slices.ContainsFunc(fields, func(f string) bool { return f != "permission_overwrites" }) {
			out = append(out, Effect{Action: 11, Target: target})
		}
		for _, id := range overwrites {
			out = append(out, Effect{Action: AuditOverwriteCreate, Target: target, Detail: id})
		}
		if overwrites != nil || slices.Contains(fields, "permission_overwrites") {
			// A removed overwrite is absent from the body: one marker, without a
			// detail, proves the removals of this call (see OverwriteRemovals).
			out = append(out, Effect{Action: AuditOverwriteCreate, Target: target, Detail: OverwriteRemovals})
		}
		return out
	case "UpdateMember":
		if fields == nil {
			return []Effect{{Action: AuditMemberUpdate, Target: target}}
		}
		var out []Effect
		if slices.Contains(fields, "roles") {
			out = append(out, Effect{Action: AuditMemberRoleUpdate, Target: target})
		}
		if slices.ContainsFunc(fields, func(f string) bool {
			return f == "nick" || f == "communication_disabled_until" || f == "mute" || f == "deaf" || f == "flags"
		}) {
			out = append(out, Effect{Action: AuditMemberUpdate, Target: target})
		}
		return out
	}
	if action := OperationAction(method); action != 0 {
		return []Effect{{Action: action, Target: target}}
	}
	return nil
}

// OperationAction is the main audit action of an adapter method (0: none, or not
// verified). CallEffects also derives secondary entries from the request body.
func OperationAction(method string) int {
	switch method {
	case "UpdateServer":
		return 1
	case "CreateChannel":
		return 10
	case "UpdateChannel":
		return 11
	case "DeleteChannel":
		return 12
	case "SetChannelPositions":
		return 16
	case "KickMember":
		return 20
	case "BanMember":
		return 22
	case "UnbanMember":
		return 23
	case "UpdateMember", "UpdateGuildProfile":
		return AuditMemberUpdate
	case "AddMemberRole", "RemoveMemberRole", "SetMemberRoles":
		return AuditMemberRoleUpdate
	case "CreateRole":
		return 30
	case "UpdateRole":
		return 31
	case "DeleteRole":
		return 32
	case "SetRolePositions":
		return 33
	case "CreateWebhook":
		return 50
	case "CreateEmoji":
		return 60
	case "UpdateEmoji":
		return 61
	case "DeleteEmoji":
		return 62
	case "DeleteMessage":
		return 72
	case "BulkDeleteMessages":
		return 73
	case "CreateSticker":
		return 90
	case "UpdateSticker":
		return 91
	case "DeleteSticker":
		return 92
	case "CreateEvent":
		return 100
	case "EditEvent":
		return 101
	case "DeleteEvent":
		return 102
	case "CreateThread":
		return 110
	case "CreateSoundboardSound":
		return 130
	case "UpdateSoundboardSound":
		return 131
	case "DeleteSoundboardSound":
		return 132
	case "CreateAutoModRule":
		return 140
	case "UpdateAutoModRule":
		return 141
	case "DeleteAutoModRule":
		return 142
	case "UpdateOnboarding":
		return 167
	default:
		return 0
	}
}

// AuditTarget extracts only ID path parameters with known public resource names.
// Never persist webhook or interaction tokens, query values, or unrecognized segments.
func AuditTarget(template, path string) string {
	path, _, _ = strings.Cut(path, "?")
	names, parts := strings.Split(template, "/"), strings.Split(path, "/")
	if len(names) != len(parts) {
		return ""
	}
	for j := len(names) - 1; j >= 0; j-- {
		switch names[j] {
		case "{guild.id}", "{user.id}", "{role.id}", "{channel.id}", "{emoji.id}", "{sticker.id}", "{message.id}", "{rule.id}", "{sound.id}", "{guild_scheduled_event.id}":
			if IsSnowflake(parts[j]) {
				return parts[j]
			}
			return ""
		}
	}
	return ""
}
