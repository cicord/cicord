package runtime

import (
	"context"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
)

// Server activity analytics (internal/analytics): one bot per server collects it, the
// first bot key, in sorted order, that lists the server (several bots of a server all
// receive its gateway events; interactions reach only their own application, so every
// bot counts its own).

// analyticsKVScope is the KV scope of the daily HMAC keys and the rollup marker.
const analyticsKVScope = "analytics"

// analyticsBot returns the bot that collects a server's analytics ("" when no bot
// lists it). It only depends on the configuration: the intents are computed from it
// before the bots connect.
func analyticsBot(repo *config.Repo, server string) string {
	for _, key := range sortedKeys(repo.Bots) {
		for _, s := range repo.Bots[key].Spec.ServerKeys() {
			if s == server {
				return key
			}
		}
	}
	return ""
}

// analyticsIntents are the intents added to a bot that collects at least one server
// with analytics enabled: only analytics.CollectorIntents, never a privileged one.
func (r *Runtime) analyticsIntents(repo *config.Repo, bot string) discord.Intents {
	if r.collector == nil {
		return 0
	}
	for _, key := range sortedKeys(repo.Servers) {
		if enabled, _, _ := repo.Servers[key].Spec.AnalyticsSettings(); enabled && analyticsBot(repo, key) == bot {
			return analytics.CollectorIntents
		}
	}
	return 0
}

// configureAnalytics hands the servers of a new active revision to the collector.
func (r *Runtime) configureAnalytics(a *active) {
	if r.collector == nil {
		return
	}
	var servers []analytics.CollectorServer
	for _, key := range sortedKeys(a.servers) {
		srt := a.servers[key]
		enabled, retention, presence := srt.cfg.Spec.AnalyticsSettings()
		cs := analytics.CollectorServer{
			Key: key, ID: srt.cfg.Spec.DiscordID, Enabled: enabled, RetentionDays: retention, Presence: presence,
			Bot: analyticsBot(a.repo, key),
		}
		if b := a.repo.Bots[cs.Bot]; b != nil {
			cs.BotFile, cs.ApplicationID = b.File, b.Spec.ApplicationID
		}
		cache := srt.cache
		cs.Channel, cs.Role, cs.Owner = cache.Channel, cache.Role, cache.Owner
		cs.AFKChannelID = func() string { return cache.Server().AFKChannelID }
		if h := r.bot(cs.Bot); h != nil {
			cs.Connected, cs.Intents, cs.Client, cs.BotUserID = true, h.intents, h.session, h.session.Self().ID
			cs.Permissions = botPermissions(h.session, cache, cs.ID, cs.BotUserID)
		}
		servers = append(servers, cs)
	}
	var botUsers []string
	names := map[string]string{}
	for _, h := range r.hosts() {
		self := h.session.Self()
		botUsers = append(botUsers, self.ID)
		names[h.key] = self.Username
	}
	r.collector.SetBotNames(names)
	r.collector.Configure(servers, botUsers)
}

// botPermissions computes the server-level permissions of the bot from its member
// roles and the cached roles.
func botPermissions(cl discord.Client, cache *serverCache, serverID, botUserID string) func(context.Context) (discord.Permissions, error) {
	return func(ctx context.Context) (discord.Permissions, error) {
		m, err := cl.GetMember(ctx, serverID, botUserID)
		if err != nil {
			return 0, err
		}
		roles, _ := cache.Snapshot()
		if len(roles) == 0 {
			if roles, err = cl.ListRoles(ctx, serverID); err != nil {
				return 0, err
			}
		}
		return reconcile.BasePermissions(serverID, roles, m.RoleIDs, cache.Server().OwnerID == botUserID), nil
	}
}

// runAnalytics runs the collector until ctx ends; the returned channel is closed once
// its last flush is done.
func (r *Runtime) runAnalytics(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if r.collector == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		r.collector.Run(ctx)
	}()
	return done
}

// AnalyticsStatus describes analytics collection for a server (Since is left to the
// caller, which reads it from the store). Without an analytics store, analytics are
// reported as disabled.
func (r *Runtime) AnalyticsStatus(server string) analytics.Status {
	if r.collector == nil {
		return analytics.Status{Missing: []analytics.Missing{}}
	}
	return r.collector.Status(server)
}

// analyticsShutdownTimeout bounds the wait for the collector's last flush.
const analyticsShutdownTimeout = 15 * time.Second
