package discord

import "strconv"

// AuditAction gives plugins stable names independent of Discord's numeric codes.
type AuditAction struct {
	Name      string
	Target    string
	Mergeable bool
}

var auditActions = map[int]AuditAction{
	1:   {"server.update", "server", false},
	10:  {"channel.create", "channel", false},
	11:  {"channel.update", "channel", false},
	12:  {"channel.delete", "channel", false},
	13:  {"overwrite.create", "channel", false},
	14:  {"overwrite.update", "channel", false},
	15:  {"overwrite.delete", "channel", false},
	16:  {"channel.reorder", "channel", true},
	20:  {"member.kick", "member", false},
	21:  {"member.prune", "server", false},
	22:  {"member.ban", "member", false},
	23:  {"member.unban", "member", false},
	24:  {"member.update", "member", false},
	25:  {"member.roleUpdate", "member", false},
	26:  {"member.move", "member", true},
	27:  {"member.disconnect", "member", true},
	28:  {"bot.add", "member", false},
	30:  {"role.create", "role", false},
	31:  {"role.update", "role", false},
	32:  {"role.delete", "role", false},
	33:  {"role.reorder", "role", true},
	40:  {"invite.create", "invite", false},
	41:  {"invite.update", "invite", false},
	42:  {"invite.delete", "invite", false},
	50:  {"webhook.create", "webhook", false},
	51:  {"webhook.update", "webhook", false},
	52:  {"webhook.delete", "webhook", false},
	60:  {"emoji.create", "emoji", false},
	61:  {"emoji.update", "emoji", false},
	62:  {"emoji.delete", "emoji", false},
	72:  {"message.delete", "member", true},
	73:  {"message.bulkDelete", "channel", true},
	74:  {"message.pin", "message", false},
	75:  {"message.unpin", "message", false},
	90:  {"sticker.create", "sticker", false},
	91:  {"sticker.update", "sticker", false},
	92:  {"sticker.delete", "sticker", false},
	100: {"event.create", "event", false},
	101: {"event.update", "event", false},
	102: {"event.delete", "event", false},
	110: {"thread.create", "channel", false},
	111: {"thread.update", "channel", false},
	112: {"thread.delete", "channel", false},
	140: {"automod.create", "automod", false},
	141: {"automod.update", "automod", false},
	142: {"automod.delete", "automod", false},
	143: {"automod.block", "member", false},
	144: {"automod.alert", "member", false},
	145: {"automod.timeout", "member", false},
	146: {"automod.quarantine", "member", false},
}

func DescribeAuditAction(code int) AuditAction {
	if action, ok := auditActions[code]; ok {
		return action
	}
	return AuditAction{Name: "discord." + strconv.Itoa(code)}
}
func AuditActionCode(name string) (int, bool) {
	for code, action := range auditActions {
		if action.Name == name {
			return code, true
		}
	}
	return 0, false
}

type AuditQuery struct {
	Action  int
	ActorID string
	Before  string
	Limit   int
}

// Attribution combines Discord evidence with the emitted-operation journal.
type Attribution struct {
	Kind      string     `json:"kind"`
	Certainty string     `json:"certainty"`
	Operation *Operation `json:"-"`
}
