package analytics

import (
	"fmt"

	"github.com/cicord/cicord/internal/discord"
)

// Metric groups reported in Status.Missing.
var (
	membersMetrics = []string{MembersJoined, MembersLeft, MembersJoinedByAge, MembersJoinedBots, MembersBots,
		MembersRoleAdded, MembersRoleRemoved, OnboardingCompleted}
	contentMetrics  = []string{MessagesAttachments, PollsCreated}
	auditMetrics    = []string{ModerationActions}
	manageMetrics   = []string{MembersInvitedBy, AutoModTriggers, AutoModBlocked}
	presenceMetrics = []string{PresenceStatus, PresencePlatform, PresenceActivity}
)

// Status describes what is collected for a server (Since is left to the API, which
// reads it from the store). A server the collector does not know is reported as not
// enabled.
func (c *Collector) Status(server string) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{Missing: []Missing{}}
	s := c.byKey[server]
	if s == nil {
		return st
	}
	cfg := s.cfg
	st.Enabled, st.RetentionDays = cfg.Enabled, cfg.RetentionDays
	if !cfg.Enabled {
		return st
	}
	st.Presence = cfg.presenceCollected()
	if cfg.Bot == "" {
		st.Missing = append(st.Missing, Missing{Metrics: allMetrics(),
			Reason: "no bot lists this server",
			Fix:    "add the server to spec.servers of a bot (bots/<bot>.yaml)"})
		return st
	}
	if !cfg.Connected {
		st.Missing = append(st.Missing, Missing{Metrics: allMetrics(),
			Reason: fmt.Sprintf("the bot %s, which collects this server, is not connected", cfg.Bot),
			Fix:    fmt.Sprintf("check the token and intents of %s (the runtime status shows the connection error)", botFile(cfg))})
		return st
	}
	portal := discord.PortalBotURL(cfg.ApplicationID)
	if !cfg.Intents.Has(intentMembers) {
		st.Missing = append(st.Missing, Missing{Metrics: membersMetrics,
			Reason: fmt.Sprintf("the bot %s does not request the privileged ServerMembers intent (member joins, leaves and updates, and which members are bots: without it the member counts include the bots)", cfg.Bot),
			Fix:    fmt.Sprintf("add ServerMembers to spec.intents in %s and turn on Server Members Intent in the Discord developer portal (%s → Privileged Gateway Intents)", botFile(cfg), portal)})
	}
	if !cfg.Intents.Has(intentMessageContent) {
		st.Missing = append(st.Missing, Missing{Metrics: contentMetrics,
			Reason: fmt.Sprintf("Discord removes attachments and polls from the messages the bot %s receives without the privileged MessageContent intent", cfg.Bot),
			Fix:    fmt.Sprintf("add MessageContent to spec.intents in %s and turn on Message Content Intent in the Discord developer portal (%s → Privileged Gateway Intents)", botFile(cfg), portal)})
	}
	if s.permsKnown && !hasPerm(s.perms, permViewAuditLog) {
		st.Missing = append(st.Missing, Missing{Metrics: auditMetrics,
			Reason: fmt.Sprintf("the bot %s lacks the View Audit Log permission, so Discord does not send it audit log entries", cfg.Bot),
			Fix:    "give the bot's role the ViewAuditLog permission in the server"})
	}
	if s.invitesForbidden || s.permsKnown && !hasPerm(s.perms, permManageServer) {
		st.Missing = append(st.Missing, Missing{Metrics: manageMetrics,
			Reason: fmt.Sprintf("the bot %s lacks the Manage Server permission, needed to read invite uses and to receive AutoMod executions", cfg.Bot),
			Fix:    "give the bot's role the ManageServer permission in the server"})
	}
	switch {
	case !cfg.Presence:
		st.Missing = append(st.Missing, Missing{Metrics: presenceMetrics,
			Reason: "detailed presence (statuses, platforms, activities) is not enabled; only Discord's approximate online count is sampled",
			Fix:    fmt.Sprintf("set spec.analytics.presence: true in the server file, add ServerPresences to spec.intents in %s and turn on Presence Intent in the Discord developer portal (%s → Privileged Gateway Intents)", botFile(cfg), portal)})
	case !cfg.Intents.Has(intentPresences):
		st.Missing = append(st.Missing, Missing{Metrics: presenceMetrics,
			Reason: fmt.Sprintf("spec.analytics.presence is true but the bot %s does not request the privileged ServerPresences intent", cfg.Bot),
			Fix:    fmt.Sprintf("add ServerPresences to spec.intents in %s and turn on Presence Intent in the Discord developer portal (%s → Privileged Gateway Intents)", botFile(cfg), portal)})
	}
	return st
}

// hasPerm reports whether server-level permissions grant p (Administrator grants all).
func hasPerm(have, p discord.Permissions) bool {
	return have.Has(permAdministrator) || have.Has(p)
}

func botFile(cfg CollectorServer) string {
	if cfg.BotFile != "" {
		return cfg.BotFile
	}
	return "the bot.yaml of " + cfg.Bot
}

func allMetrics() []string {
	out := make([]string, 0, len(Catalog))
	for _, m := range Catalog {
		out = append(out, m.Name)
	}
	return out
}
