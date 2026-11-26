package discord

import (
	"context"
	"strings"
	"time"
)

// Operation holds only routing metadata. Request bodies, audit reasons, URLs,
// interaction tokens and credentials never belong in the operation journal.
type Operation struct {
	ServerID string    `json:"serverId"`
	ActorID  string    `json:"actorId"`
	Bot      string    `json:"bot"`
	Plugin   string    `json:"plugin,omitempty"`
	Instance string    `json:"instance,omitempty"`
	Revision string    `json:"revision,omitempty"`
	Method   string    `json:"method"`
	Action   int       `json:"action,omitempty"`
	Target   string    `json:"target,omitempty"`
	At       time.Time `json:"at"`
}
type OperationScope struct {
	Origin Operation
	Record func(context.Context, Operation)
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
func (o *OperationScope) Emitted(ctx context.Context, method, target string, at time.Time) {
	if o == nil || o.Record == nil || strings.HasPrefix(method, "Get") || strings.HasPrefix(method, "List") {
		return
	}
	// Methods are internal adapter names. Only a validated snowflake can be a target.
	op := o.Origin
	op.Method = method
	op.At = at.UTC()
	if IsSnowflake(target) {
		op.Target = target
	}
	op.Action = OperationAction(method)
	o.Record(ctx, op)
}
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
	case "UpdateMember":
		return 24
	case "AddMemberRole", "RemoveMemberRole", "SetMemberRoles":
		return 25
	case "CreateRole":
		return 30
	case "UpdateRole":
		return 31
	case "DeleteRole":
		return 32
	case "SetRolePositions":
		return 33
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
	case "CreateThread":
		return 110
	case "CreateAutoModRule":
		return 140
	case "UpdateAutoModRule":
		return 141
	case "DeleteAutoModRule":
		return 142
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
		case "{guild.id}", "{user.id}", "{role.id}", "{channel.id}", "{emoji.id}", "{sticker.id}", "{message.id}", "{rule.id}", "{sound.id}":
			if IsSnowflake(parts[j]) {
				return parts[j]
			}
			return ""
		}
	}
	return ""
}
