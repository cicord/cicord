package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
)

// Server activity analytics (https://cicord.dev/docs/reference/http-api/analytics/):
// GET /api/v1/servers/{server}/analytics/summary|series|catalog, for moderators and
// admins of the server, on the admin host. The JSON contract is internal/analytics/api.go.

func (s *server) handleAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	rd, key, st, err := s.analyticsRequest(r)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	req, err := analytics.ParseSummaryRequest(r.URL.Query(), time.Now())
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	out, err := rd.SummaryFor(r.Context(), key, req, st)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAnalyticsSeries(w http.ResponseWriter, r *http.Request) {
	rd, key, st, err := s.analyticsRequest(r)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	req, err := analytics.ParseSeriesRequest(key, r.URL.Query(), time.Now())
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	out, err := rd.Series(r.Context(), req, st)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAnalyticsCatalog(w http.ResponseWriter, r *http.Request) {
	rd, key, st, err := s.analyticsRequest(r)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	out, err := rd.Catalog(r.Context(), key, st)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// analyticsRequest checks access (moderator or admin of the server) and returns the
// reader, the server key and the collection status of the server.
func (s *server) analyticsRequest(r *http.Request) (*analytics.Reader, string, analytics.Status, error) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleModerator); err != nil {
		return nil, "", analytics.Status{}, err
	}
	if s.opts.Analytics == nil {
		return nil, "", analytics.Status{}, httpErr(http.StatusNotFound, "analytics_disabled",
			"analytics are not available on this runtime (no analytics store is configured)")
	}
	labeler, owners := s.analyticsLabels()
	rd := &analytics.Reader{Store: s.opts.Analytics, Labeler: labeler, Owners: owners}
	return rd, key, s.analyticsStatus(key), nil
}

// analyticsLabels are the labelers of the analytics API, in order: the runtime's live
// cache, the resource map with the configuration, then the labels the collector
// remembered (deleted channels and roles); and the resolvers of the channels plugins
// created (grouped per plugin instance): the resource map, then the remembered labels.
func (s *server) analyticsLabels() (analytics.Labeler, analytics.Owners) {
	labelers := analytics.Labelers{runtimeLabeler{s.rt}}
	var owners analytics.OwnersChain
	if s.store != nil {
		rl := analytics.ResourceLabeler{Resources: s.store.Resources(), Spec: s.serverSpec}
		labelers = append(labelers, rl)
		owners = append(owners, rl)
	}
	if stored, ok := s.opts.Analytics.(state.AnalyticsLabels); ok {
		sl := analytics.StoredLabeler{Store: stored, Live: runtimeLabeler{s.rt}.live}
		labelers = append(labelers, sl)
		owners = append(owners, sl)
	}
	return labelers, owners
}

func (s *server) serverSpec(key string) *config.ServerSpec {
	if srv, ok := s.rt.ServerConfig(key); ok {
		return &srv.Spec
	}
	return nil
}

// analyticsStatus is the collection status of a server: from the runtime
// (Options.AnalyticsStatus) or, without it, from the server configuration. Since is
// filled by the reader, from the store.
func (s *server) analyticsStatus(key string) analytics.Status {
	if s.opts.AnalyticsStatus != nil {
		return s.opts.AnalyticsStatus(key)
	}
	st := analytics.Status{Missing: []analytics.Missing{}}
	if srv, ok := s.rt.ServerConfig(key); ok {
		st.Enabled, st.RetentionDays, st.Presence = srv.Spec.AnalyticsSettings()
	}
	return st
}

func (s *server) failAnalytics(w http.ResponseWriter, err error) {
	var re *analytics.RequestError
	if errors.As(err, &re) {
		writeError(w, http.StatusBadRequest, "invalid", re.Message)
		return
	}
	s.fail(w, err)
}

// runtimeLabeler labels channels and roles from the runtime's live cache of the
// server (current names, channel types and role colors, whether declared in git or
// not). It knows nothing while the cache is empty.
type runtimeLabeler struct{ rt *runtime.Runtime }

// live tells whether the runtime's cache of a server is loaded (its channels known).
func (l runtimeLabeler) live(server string) bool {
	if l.rt == nil {
		return false
	}
	_, channels, _, ok := l.rt.ServerResources(server)
	return ok && len(channels) > 0
}

func (l runtimeLabeler) Labels(_ context.Context, server string, dims []analytics.DimRef) map[string]analytics.Label {
	if l.rt == nil {
		return nil
	}
	roles, channels, _, ok := l.rt.ServerResources(server)
	if !ok || (len(roles) == 0 && len(channels) == 0) {
		return nil
	}
	byChannel := make(map[string]discord.Channel, len(channels))
	for _, c := range channels {
		byChannel[c.ID] = c
	}
	byRole := make(map[string]discord.Role, len(roles))
	for _, ro := range roles {
		byRole[ro.ID] = ro
	}
	out := map[string]analytics.Label{}
	for _, d := range dims {
		switch d.Kind {
		case "channel":
			if c, ok := byChannel[d.Value]; ok {
				out[d.Value] = analytics.Label{Name: c.Name, Kind: channelLabelKind(c.Type)}
			}
		case "role":
			if ro, ok := byRole[d.Value]; ok {
				out[d.Value] = analytics.Label{Name: ro.Name, Kind: "role", Color: colorHex(ro.Color)}
			}
		}
	}
	return out
}

func channelLabelKind(t discord.ChannelType) string {
	switch t {
	case discord.ChannelVoice, discord.ChannelStage:
		return "voice"
	case discord.ChannelPublicThread, discord.ChannelPrivateThread, discord.ChannelAnnouncementThread:
		return "thread"
	}
	return "channel"
}

// handlePortalAnalytics answers GET /api/v1/portal/{server}/analytics (the community
// portal's activity view), when spec.analytics.portal allows it: 404 when off, the
// signed-in members of the server for "members", anyone for "public".
func (s *server) handlePortalAnalytics(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	cfg, ok := s.rt.ServerConfig(key)
	if !ok || s.opts.Analytics == nil {
		notFoundHandler(w, r)
		return
	}
	switch cfg.Spec.AnalyticsPortal() {
	case "public":
	case "members":
		id, err := s.requireAuth(r)
		if err != nil {
			s.fail(w, err)
			return
		}
		ro, m, err := s.roleIn(r.Context(), id, key, true)
		if err != nil {
			s.fail(w, err)
			return
		}
		if ro < roleMember || m == nil {
			s.fail(w, httpErr(http.StatusForbidden, "forbidden", "you are not a member of this server"))
			return
		}
	default:
		notFoundHandler(w, r)
		return
	}
	// No labeler: the portal names only the channels everyone can see now (public);
	// the owners group the channels plugins created, never named after a room.
	_, owners := s.analyticsLabels()
	rd := &analytics.Reader{Store: s.opts.Analytics, Owners: owners}
	out, err := rd.Portal(r.Context(), key, r.URL.Query().Get("period"), publicChannels(s.rt, key, cfg.Spec.DiscordID))
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// publicChannels tells whether @everyone can see a channel (its category's overwrites
// are already on the channel; a public thread follows its parent, a private thread is
// never public) and gives its current name, from the runtime's live cache.
func publicChannels(rt *runtime.Runtime, server, serverID string) func(string) (string, bool) {
	roles, channels, _, ok := rt.ServerResources(server)
	if !ok {
		return func(string) (string, bool) { return "", false }
	}
	return publicChannelsOf(roles, channels, serverID)
}

// publicChannelsOf is publicChannels on given roles and channels.
func publicChannelsOf(roles []discord.Role, channels []discord.Channel, serverID string) func(string) (string, bool) {
	return discord.PublicChannels(roles, channels, serverID)
}
